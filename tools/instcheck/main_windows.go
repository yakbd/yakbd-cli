//go:build windows

// Command instcheck is the unattended Windows test of yakbd's installers (bundle made by tools/instbundle). Inside a
// disposable Windows guest, elevated, in the interactive console session, it runs each installer quietly, checks the
// files, registry values, uninstall entry and enabled state against docs/installer.md (the reference spec's
// installation §2, §3.2, §4, §10), types the installed layout through ToUnicodeEx in a 64-bit and a 32-bit
// process, uninstalls and checks that nothing is left. It prints one PASS or FAIL line per scenario and a count;
// details go to a second file. The 32-bit build (instcheck-32.exe) is only the typing helper.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/setup"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pLoadKeyboardLayoutW   = user32.NewProc("LoadKeyboardLayoutW")
	pUnloadKeyboardLayout  = user32.NewProc("UnloadKeyboardLayout")
	pToUnicodeEx           = user32.NewProc("ToUnicodeEx")
	pMapVirtualKeyExW      = user32.NewProc("MapVirtualKeyExW")
	pGetKeyboardLayoutList = user32.NewProc("GetKeyboardLayoutList")
	pOpenInputDesktop      = user32.NewProc("OpenInputDesktop")
	pCloseDesktop          = user32.NewProc("CloseDesktop")
	pRegDeleteKeyW         = advapi32.NewProc("RegDeleteKeyW")
	pRegEnumValueW         = advapi32.NewProc("RegEnumValueW")
	pGetSystemWow64Dir     = kernel32.NewProc("GetSystemWow64DirectoryW")
	pGetSystemDirectoryW   = kernel32.NewProc("GetSystemDirectoryW")
)

const (
	hkeyLocalMachine = syscall.Handle(0x80000002)
	hkeyCurrentUser  = syscall.Handle(0x80000001)
	hkeyUsers        = syscall.Handle(0x80000003)
	keyRead          = 0x20019
	klfActivate      = 1
	layoutsKey       = `SYSTEM\CurrentControlSet\Control\Keyboard Layouts`
	uninstallKey     = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
)

func main() {
	runtime.LockOSThread()
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("instcheck", flag.ContinueOnError)
	bundle := fs.String("bundle", ".", "the bundle folder")
	out := fs.String("out", "", "results file (default <bundle>\\results\\instcheck.txt)")
	typeKLID := fs.String("type", "", "internal: load this KLID, type the expectations file given as argument, print PASS/FAIL")
	hklFlag := fs.String("hkl", "", "internal: exit 0 when this new process's GetKeyboardLayoutList has this HKL (hex)")
	phase := fs.String("phase", "all", "all (the x64 or x86 run); reenable-prepare, reenable-check (around a cold boot); arm64-install, arm64-check, arm64-uninstall (the ARM64 run around a cold boot); the phases of the bundle READMEs; summary")
	guest := fs.String("guest", "", "with -phase summary: w11x64, w10x64 or w10x86")
	holdFiles := fs.String("hold", "", "internal: hold these files (;-separated) open without delete sharing")
	ready := fs.String("ready", "", "internal: with -hold, written once the files are open")
	stop := fs.String("stop", "", "internal: with -hold, the files are closed once this exists")
	bare := fs.String("loadbare", "", "internal (plant-control): load this DLL (a bare name or a full path) with LoadLibraryW")
	probe := fs.Bool("probe-delay", false, "internal (32-bit helper): mark the probe files for deletion at the restart")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *holdFiles != "" {
		return hold(strings.Split(*holdFiles, ";"), *ready, *stop)
	}
	if *bare != "" {
		return loadBare(*bare)
	}
	if *probe {
		return probeDelay()
	}
	if *hklFlag != "" {
		if layoutList(uint32(hex(*hklFlag))) {
			return 0
		}
		return 1
	}
	if *typeKLID != "" {
		msg, ok := typeLayout(*typeKLID, fs.Arg(0), 0)
		fmt.Println(msg)
		if !ok {
			return 1
		}
		return 0
	}
	res := filepath.Join(*bundle, "results")
	_ = os.MkdirAll(filepath.Join(res, "logs"), 0o755)
	if *out == "" {
		*out = filepath.Join(res, "instcheck.txt")
		if *phase != "all" {
			*out = filepath.Join(res, *phase+".txt")
		}
	}
	if *phase == "summary" {
		ph, ok := guestPhases[*guest]
		if !ok {
			fmt.Println("-guest must be w11x64, w10x64, w10x86, w11settings or w11signin")
			return 2
		}
		return summary(res, *guest, ph)
	}
	c := &checker{bundle: *bundle, logs: filepath.Join(res, "logs"), res: res}
	c.detail = &bytes.Buffer{}
	if err := interactive(); err != nil && !noDesktop[*phase] {
		fmt.Println("FAIL setup:", err)
		_ = os.WriteFile(*out, []byte("FAIL setup: "+err.Error()+"\n"), 0o644)
		return 1
	}
	if *phase == "all" {
		c.all()
	} else if !c.plant(*phase) && !c.clean(*phase) && !c.reenablePhase(*phase, res) && !c.arm64(*phase, res) && !c.realUser(*phase, res) && !c.signin(*phase, res) {
		fmt.Println("unknown phase", *phase)
		return 2
	}
	var short bytes.Buffer
	for _, l := range c.lines {
		short.WriteString(l + "\n")
	}
	fmt.Fprintf(&short, "%d passed, %d failed\n", c.passed, c.failed)
	_ = os.WriteFile(*out, short.Bytes(), 0o644)
	_ = os.WriteFile(strings.TrimSuffix(*out, ".txt")+"-details.txt", c.detail.Bytes(), 0o644)
	os.Stdout.Write(short.Bytes())
	if c.failed > 0 {
		return 1
	}
	return 0
}

type checker struct {
	bundle, logs   string
	res            string // the results folder
	lines          []string
	passed, failed int
	detail         *bytes.Buffer
}

// scenario collects the problems of one scenario.
type scenario struct {
	c     *checker
	name  string
	probs []string
	notes []string
}

func (c *checker) begin(name string) *scenario {
	fmt.Fprintf(c.detail, "\n== %s\n", name)
	return &scenario{c: c, name: name}
}

func (s *scenario) failf(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	s.probs = append(s.probs, m)
	fmt.Fprintf(s.c.detail, "  FAIL %s\n", m)
}

func (s *scenario) notef(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	s.notes = append(s.notes, m)
	fmt.Fprintf(s.c.detail, "  note %s\n", m)
}

func (s *scenario) end() bool {
	line := "PASS " + s.name
	if len(s.probs) > 0 {
		p := s.probs
		if len(p) > 3 {
			p = p[:3]
		}
		line = "FAIL " + s.name + ": " + strings.Join(p, "; ")
		s.c.failed++
	} else {
		s.c.passed++
	}
	if len(s.notes) > 0 && len(s.probs) == 0 {
		line += " (" + strings.Join(s.notes, "; ") + ")"
	}
	s.c.lines = append(s.c.lines, line)
	fmt.Println(line)
	return len(s.probs) == 0
}

// layout is one installer of the bundle with what its payload says.
type layout struct {
	exe    string
	p      *payload.Payload
	expect string
}

func (c *checker) load(name, expect string) (*layout, error) {
	exe := filepath.Join(c.bundle, name)
	b, err := os.ReadFile(exe)
	if err != nil {
		return nil, err
	}
	p, err := payload.FromExe(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return &layout{exe: exe, p: p, expect: filepath.Join(c.bundle, expect)}, nil
}

// runInstaller runs an installer and returns its exit code; the installer's log goes to logs\<tag>.log.
func (c *checker) runInstaller(s *scenario, exe, tag string, args ...string) int {
	log := filepath.Join(c.logs, tag+".log")
	args = cliArgs(append(args, "/log", log))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	start := time.Now()
	err := cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	fmt.Fprintf(c.detail, "  ran %s %v: exit %d (%v) in %s\n", filepath.Base(exe), args, code, err, time.Since(start).Round(time.Millisecond))
	if ctx.Err() != nil {
		s.failf("%s %v timed out", filepath.Base(exe), args)
	}
	return code
}

func (c *checker) all() {
	a, err := c.load("demo-setup.exe", "demo.expect.txt")
	if err != nil {
		c.begin("bundle").failf("%v", err)
		return
	}
	v2, err := c.load("demo-v2-setup.exe", "demo.expect.txt")
	if err != nil {
		c.begin("bundle").failf("%v", err)
		return
	}
	x86, _ := c.load("demo-x86only-setup.exe", "demo.expect.txt")
	b, err := c.load("kbdtst-x64only-setup.exe", "kbdtst.expect.txt")
	if err != nil {
		c.begin("bundle").failf("%v", err)
		return
	}
	for _, x := range []*layout{a, b} {
		watched = append(watched, x.p.Manifest.Layout.File)
	}
	before := snapshot()
	fmt.Fprintf(c.detail, "state before:\n%s\n", before)

	// 1. A single-build installer for another machine kind refuses and changes nothing: the x86-only one on 64-bit
	// Windows, the x64-only one on x86 Windows.
	onX86 := nativeMachine() == payload.MachineI386
	s := c.begin("refuse-x86-only-on-x64")
	wrong := x86
	if onX86 {
		s = c.begin("refuse-x64-only-on-x86")
		wrong = b
	}
	if wrong == nil {
		s.failf("demo-x86only-setup.exe missing")
	} else if code := c.runInstaller(s, wrong.exe, "1-refuse", "/quiet"); code != exitNoBuild {
		s.failf("exit code %d, want %d", code, exitNoBuild)
	}
	if after := snapshot(); after != before {
		s.failf("the state changed: %s", firstDiff(before, after))
	}
	s.end()

	// 2. The all-builds installer, quiet, enabled for this user.
	s = c.begin("install-all-builds-enabled")
	code := c.runInstaller(s, a.exe, "2-install", "/quiet", "/enable")
	if code != 0 {
		s.failf("exit code %d, want 0", code)
	}
	klid, id := c.checkInstalled(s, a, true)
	s.end()
	if klid == "" {
		return
	}

	// 3. Typing through the installed layout in a 64-bit and a 32-bit process.
	s = c.begin("type-64-and-32-bit")
	hkl := uint32(hex(a.p.Manifest.Layout.LangID)) | uint32(0xF000|id)<<16
	if msg, ok := typeLayout(klid, a.expect, hkl); !ok {
		s.failf("64-bit: %s", msg)
	} else {
		s.notef("64-bit %s", msg)
	}
	if out, err := exec.Command(filepath.Join(c.bundle, "instcheck-32.exe"), "-type", klid, a.expect).CombinedOutput(); err != nil {
		s.failf("32-bit: %s (%v)", strings.TrimSpace(string(out)), err)
	} else {
		s.notef("32-bit %s", strings.TrimSpace(string(out)))
	}
	s.end()

	// 4. Running the same installer again changes nothing that matters (same KLID and Layout Id).
	s = c.begin("install-again-same-klid")
	if code := c.runInstaller(s, a.exe, "4-again", "/quiet"); code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	if k, i := c.checkInstalled(s, a, true); k != klid || i != id {
		s.failf("KLID %s Layout Id %04x, before %s %04x", k, i, klid, id)
	}
	s.end()

	// 5. Upgrade in place to version 2 (the file may be in use: then exit 3010 and a replacement at restart).
	s = c.begin("upgrade-in-place")
	code = c.runInstaller(s, v2.exe, "5-upgrade", "/quiet")
	if code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	if code == 3010 {
		s.notef("3010: a file was in use, replaced at restart")
		pending := pendingRenames()
		fmt.Fprintf(c.detail, "  pending renames: %q\n", pending)
		if !strings.Contains(strings.ToLower(strings.Join(pending, "|")), strings.ToLower(a.p.Manifest.Layout.File)) {
			s.failf("exit 3010 but no pending rename names %s", a.p.Manifest.Layout.File)
		}
		c.checkPendingPaths(s, a.p.Manifest.Layout.File)
		c.checkRegistry(s, v2, klid, id)
	} else if k, i := c.checkInstalled(s, v2, true); k != klid || i != id {
		s.failf("KLID %s Layout Id %04x, before %s %04x", k, i, klid, id)
	}
	if v := regString(hkeyLocalMachine, uninstallKey+`\`+a.p.Manifest.Product.Code, "DisplayVersion"); v != v2.p.Manifest.Product.Version {
		s.failf("DisplayVersion %q, want %q", v, v2.p.Manifest.Product.Version)
	}
	s.end()

	// 6. Repair: the KLID key is deleted by hand (as a feature update may do, inst §7 M7); setup.exe /repair from
	// Program Files restores it with the same KLID and Layout Id.
	s = c.begin("repair-lost-klid")
	if r, _, _ := pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(layoutsKey+`\`+klid)))); r != 0 {
		s.failf("cannot delete the KLID key for the test: %v", syscall.Errno(r))
	}
	installed := c.installedExe(a)
	if installed == "" {
		s.failf("no installed setup.exe")
	} else if code := c.runInstaller(s, installed, "6-repair", "/repair", "/quiet"); code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	c.checkRegistry(s, v2, klid, id)
	s.end()

	// 7. Uninstall through the uninstall entry's command line, quietly; then nothing may be left.
	s = c.begin("uninstall-leaves-nothing")
	c.uninstall(s, a, "7-uninstall", before)
	s.end()

	// 7b. D62: while a layout file waits for deletion at the next restart, installing again is refused.
	s = c.begin("reinstall-before-restart-refused")
	if !strings.Contains(strings.ToLower(strings.Join(pendingRenames(), "|")), strings.ToLower(a.p.Manifest.Layout.File)) {
		s.notef("not applicable: nothing of %s waits for the restart", a.p.Manifest.Layout.File)
	} else {
		base := snapshot()
		if code := c.runInstaller(s, a.exe, "7b-reinstall", "/quiet", "/noenable"); code != setup.ExitFailed {
			s.failf("exit code %d, want %d (restart first)", code, setup.ExitFailed)
		}
		if after := snapshot(); after != base {
			s.failf("the state changed: %s", firstDiff(base, after))
		}
	}
	s.end()

	// 8. A foreign file with the layout's name is never overwritten; the install fails and rolls back (the
	// all-builds kbdtst installer, so that x86 Windows gets that far too).
	s = c.begin("foreign-file-refused")
	fb, err := c.load("kbdtst-setup.exe", "kbdtst.expect.txt")
	if err != nil {
		s.failf("%v", err)
		fb = b
	}
	sys32 := sysDir()
	foreign := filepath.Join(sys32, fb.p.Manifest.Layout.File)
	if _, err := os.Stat(foreign); err == nil {
		s.failf("%s exists before the test", foreign)
	} else {
		base := snapshot()
		_ = os.WriteFile(foreign, []byte("not a layout of this product"), 0o644)
		if code := c.runInstaller(s, fb.exe, "8-foreign", "/quiet"); code != setup.ExitFailed {
			s.failf("exit code %d, want %d", code, setup.ExitFailed)
		}
		if got, _ := os.ReadFile(foreign); string(got) != "not a layout of this product" {
			s.failf("the foreign file was changed")
		}
		_ = os.Remove(foreign)
		if after := snapshot(); after != base {
			s.failf("not rolled back: %s", firstDiff(base, after))
		}
	}
	s.end()

	if onX86 {
		c.x86Only(x86)
		c.multi()
		c.vm5()
		c.hardening()
		return
	}
	// 9. The x64-only installer of a second layout, not enabled: installed, not in the user's list, types, and
	// uninstalls cleanly.
	s = c.begin("x64-only-not-enabled")
	base := snapshot()
	if code := c.runInstaller(s, b.exe, "9-install", "/quiet"); code != 0 {
		s.failf("exit code %d", code)
	}
	k2, id2 := c.checkInstalled(s, b, false)
	if k2 != "" {
		hkl2 := uint32(hex(b.p.Manifest.Layout.LangID)) | uint32(0xF000|id2)<<16
		// 32-bit first: the first process that loads a layout decides which file the session uses (inst §2.1).
		if out, err := exec.Command(filepath.Join(c.bundle, "instcheck-32.exe"), "-type", k2, b.expect).CombinedOutput(); err != nil {
			s.failf("32-bit typing: %s (%v)", strings.TrimSpace(string(out)), err)
		}
		if msg, ok := typeLayout(k2, b.expect, hkl2); !ok {
			s.failf("64-bit typing: %s", msg)
		}
	}
	c.uninstall(s, b, "9-uninstall", base)
	s.end()

	c.multi()
	c.vm5()
	c.hardening()
}

// checkInstalled checks files, registry, the uninstall entry and the enabled state, and returns the KLID and
// Layout Id.
func (c *checker) checkInstalled(s *scenario, l *layout, enabled bool) (string, uint16) {
	m := l.p.Manifest
	pl, err := setup.Plan(nativeMachine(), m)
	if err != nil {
		s.failf("plan: %v", err)
	}
	for _, p := range pl {
		f := struct{ dir, build string }{sysDir(), p.Build}
		if p.Dir == setup.DirWOW64 {
			f.dir = wowDir()
		}
		got, err := os.ReadFile(filepath.Join(f.dir, m.Layout.File))
		if err != nil {
			s.failf("%s: %v", filepath.Join(f.dir, m.Layout.File), err)
		} else if !bytes.Equal(got, l.p.Data[f.build]) {
			s.failf("%s is not the %s build", filepath.Join(f.dir, m.Layout.File), f.build)
		}
	}
	klid := ""
	for _, k := range subKeys(hkeyLocalMachine, layoutsKey) {
		if strings.EqualFold(regString(hkeyLocalMachine, layoutsKey+`\`+k, "Layout Product Code"), m.Product.Code) {
			if klid != "" {
				s.failf("two KLIDs carry the product code: %s, %s", klid, k)
			}
			klid = k
		}
	}
	if klid == "" {
		s.failf("no KLID carries the product code %s", m.Product.Code)
		return "", 0
	}
	klid = strings.ToLower(klid)
	if !strings.HasPrefix(klid, "a") || !strings.HasSuffix(klid, strings.ToLower(m.Layout.LangID)) {
		s.failf("KLID %s is not a...%s", klid, m.Layout.LangID)
	}
	id := c.checkRegistry(s, l, klid, 0)
	s.notef("KLID %s, Layout Id %04x", klid, id)
	arp := uninstallKey + `\` + m.Product.Code
	if v := regString(hkeyLocalMachine, arp, "DisplayName"); v != m.Product.Name {
		s.failf("uninstall entry DisplayName %q", v)
	}
	us := regString(hkeyLocalMachine, arp, "UninstallString")
	if exe := c.installedExe(l); exe == "" || !strings.Contains(us, exe) || !strings.HasSuffix(us, "/x "+l.p.Manifest.Product.Code) {
		s.failf("UninstallString %q", us)
	}
	inList := userHas(klid)
	if enabled && !inList {
		s.failf("the layout is not in the user's input list (User Profile / Substitutes)")
	}
	if !enabled && inList {
		s.failf("the layout was enabled although nobody asked")
	}
	if enabled {
		hkl := uint32(hex(m.Layout.LangID)) | uint32(0xF000|id)<<16
		if !hklLoaded(hkl) {
			s.notef("HKL %08x not in this process's GetKeyboardLayoutList", hkl)
		}
	}
	return klid, id
}

// checkRegistry checks the KLID's values (inst §3.2, docs/installer.md §2.1 step 6); id 0 accepts any id in
// 00c0..00ff.
func (c *checker) checkRegistry(s *scenario, l *layout, klid string, id uint16) uint16 {
	m := l.p.Manifest
	k := layoutsKey + `\` + klid
	vals := regValues(hkeyLocalMachine, k)
	if vals == nil {
		s.failf("KLID key %s missing", klid)
		return 0
	}
	if len(vals) == 0 || !strings.EqualFold(vals[0].name, "Layout File") {
		s.notef("first value is not Layout File")
	}
	want := map[string]struct {
		typ uint32
		s   string
	}{
		"layout file":         {1, m.Layout.File},
		"layout text":         {1, m.Layout.LayoutText},
		"layout display name": {2, `@%SystemRoot%\system32\` + m.Layout.File + `,-1000`},
		"layout product code": {1, m.Product.Code},
	}
	got := map[string]regValue{}
	for _, v := range vals {
		got[strings.ToLower(v.name)] = v
	}
	for name, w := range want {
		v, ok := got[name]
		if !ok || v.typ != w.typ || v.str != w.s {
			s.failf("%s\\%s = %d %q, want %d %q", klid, name, v.typ, v.str, w.typ, w.s)
		}
	}
	lid := got["layout id"]
	n := uint16(hex(lid.str))
	if lid.typ != 1 || len(lid.str) != 4 || n < 0xc0 || n > 0xff || id != 0 && n != id {
		s.failf("Layout Id %q (want 4 hex digits in 00c0..00ff%s)", lid.str, map[bool]string{true: fmt.Sprintf(", %04x", id)}[id != 0])
	}
	return n
}

func (c *checker) installedExe(l *layout) string {
	loc := regString(hkeyLocalMachine, uninstallKey+`\`+l.p.Manifest.Product.Code, "InstallLocation")
	if loc == "" {
		return ""
	}
	return filepath.Join(loc, "setup.exe")
}

// uninstall runs the uninstall entry's command quietly and checks that the state equals base, apart from files
// that are deleted at the next restart.
func (c *checker) uninstall(s *scenario, l *layout, tag, base string) {
	m := l.p.Manifest
	exe := c.installedExe(l)
	loc := filepath.Dir(exe)
	if exe == "" {
		s.failf("no uninstall entry")
		return
	}
	code := c.runInstaller(s, exe, tag, "/uninstall", "/quiet")
	if code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	pending := strings.ToLower(strings.Join(pendingRenames(), "|"))
	fmt.Fprintf(c.detail, "  pending renames: %s\n", pending)
	for _, dir := range []string{sysDir(), wowDir()} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, m.Layout.File)
		if _, err := os.Stat(p); err == nil {
			if code == 3010 && strings.Contains(pending, strings.ToLower(p)) {
				s.notef("%s deleted at restart", filepath.Base(dir)+`\`+m.Layout.File)
			} else {
				s.failf("%s is still there", p)
			}
		}
	}
	if _, err := os.Stat(loc); err == nil {
		s.failf("%s is still there", loc)
	}
	if regString(hkeyLocalMachine, uninstallKey+`\`+m.Product.Code, "DisplayName") != "" {
		s.failf("the uninstall entry is still there")
	}
	after := snapshot()
	if code == 3010 {
		after = withoutPending(after, pending)
	}
	c.checkPendingPaths(s, m.Layout.File)
	if after != base {
		s.failf("left behind: %s", firstDiff(base, after))
	}
}

// withoutPending drops the snapshot's file lines of files that are deleted at the next restart.
func withoutPending(snap, pending string) string {
	var keep []string
	for _, l := range strings.Split(snap, "\n") {
		if p, ok := strings.CutPrefix(l, "file "); ok && strings.Contains(pending, strings.ToLower(p)) {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// checkPendingPaths puts the stored paths of the pending operations naming file into the result and fails when
// one names Sysnative: the list is processed at boot, where that alias does not exist.
func (c *checker) checkPendingPaths(s *scenario, file string) {
	var names []string
	for _, p := range pendingRenames() {
		if lp := strings.ToLower(p); strings.Contains(lp, strings.ToLower(file)) || strings.Contains(lp, "yakbd-uninstall") {
			names = append(names, p)
			if strings.Contains(strings.ToLower(p), "sysnative") {
				s.failf("pending operation stored as %s", p)
			}
		}
	}
	if len(names) == 0 {
		names = []string{"none"}
	}
	s.notef("pending at restart: %s", strings.Join(names, " | "))
}

// snapshot is the state the tests compare: yakbd KLIDs (a...), uninstall entries, the system directories'
// file names, the user's Preload, Substitutes and User Profile, and Program Files\YAKBD.
func snapshot() string {
	var l []string
	for _, k := range subKeys(hkeyLocalMachine, layoutsKey) {
		for _, v := range regValues(hkeyLocalMachine, layoutsKey+`\`+k) {
			if strings.HasPrefix(strings.ToLower(k), "a") {
				l = append(l, fmt.Sprintf("klid %s %s=%q", k, v.name, v.str))
			}
		}
	}
	for _, k := range subKeys(hkeyLocalMachine, uninstallKey) {
		if strings.HasPrefix(k, "{") {
			l = append(l, "arp "+k+" "+regString(hkeyLocalMachine, uninstallKey+`\`+k, "DisplayName"))
		}
	}
	for _, d := range []string{sysDir(), wowDir()} {
		if d == "" {
			continue // x86 Windows has no SysWOW64
		}
		for _, n := range watched {
			if _, err := os.Stat(filepath.Join(d, n)); err == nil {
				l = append(l, "file "+filepath.Join(d, n))
			}
		}
		tmp, _ := filepath.Glob(filepath.Join(d, "*.yakbd-*"))
		for _, n := range tmp {
			l = append(l, "file "+n)
		}
	}
	for _, sub := range []string{`Keyboard Layout\Preload`, `Keyboard Layout\Substitutes`} {
		for _, v := range regValues(hkeyCurrentUser, sub) {
			l = append(l, fmt.Sprintf("hkcu %s %s=%s", sub, v.name, v.str))
		}
	}
	up := `Control Panel\International\User Profile`
	for _, t := range subKeys(hkeyCurrentUser, up) {
		for _, v := range regValues(hkeyCurrentUser, up+`\`+t) {
			if strings.EqualFold(v.name, "CachedLanguageName") {
				continue // Windows' own cache of the language list (installation tests/vm/results.txt, T8), not the installer's
			}
			l = append(l, fmt.Sprintf("hkcu profile %s %s", t, v.name))
		}
	}
	pf := os.Getenv("ProgramFiles(x86)")
	if pf == "" {
		pf = os.Getenv("ProgramFiles")
	}
	if es, err := os.ReadDir(filepath.Join(pf, "YAKBD")); err == nil {
		l = append(l, "dir Program Files\\YAKBD")
		for _, e := range es {
			l = append(l, "dir YAKBD\\"+e.Name())
		}
	}
	return strings.Join(l, "\n")
}

func firstDiff(a, b string) string {
	as, bs := map[string]bool{}, map[string]bool{}
	for _, x := range strings.Split(a, "\n") {
		as[x] = true
	}
	for _, x := range strings.Split(b, "\n") {
		bs[x] = true
	}
	var d []string
	for x := range bs {
		if !as[x] && x != "" {
			d = append(d, "+"+x)
		}
	}
	for x := range as {
		if !bs[x] && x != "" {
			d = append(d, "-"+x)
		}
	}
	if len(d) > 4 {
		d = append(d[:4], "...")
	}
	return strings.Join(d, ", ")
}

func userHas(klid string) bool {
	for _, v := range regValues(hkeyCurrentUser, `Keyboard Layout\Substitutes`) {
		if strings.EqualFold(v.str, klid) {
			return true
		}
	}
	for _, v := range regValues(hkeyCurrentUser, `Keyboard Layout\Preload`) {
		if strings.EqualFold(v.str, klid) {
			return true
		}
	}
	up := `Control Panel\International\User Profile`
	for _, t := range subKeys(hkeyCurrentUser, up) {
		for _, v := range regValues(hkeyCurrentUser, up+`\`+t) {
			if strings.HasSuffix(strings.ToLower(v.name), ":"+strings.ToLower(klid)) {
				return true
			}
		}
	}
	return false
}

func pendingRenames() []string {
	for _, v := range regValues(hkeyLocalMachine, `SYSTEM\CurrentControlSet\Control\Session Manager`) {
		if v.name == "PendingFileRenameOperations" {
			return strings.Split(v.str, "\x00")
		}
	}
	return nil
}

// Typing.

func typeLayout(klid, expectPath string, wantHKL uint32) (string, bool) {
	f, err := os.Open(expectPath)
	if err != nil {
		return err.Error(), false
	}
	defer f.Close()
	lines, err := wincheck.ParseExpectations(f)
	if err != nil {
		return err.Error(), false
	}
	h, _, lerr := pLoadKeyboardLayoutW.Call(uintptr(unsafe.Pointer(u16(klid))), klfActivate)
	if h == 0 {
		return fmt.Sprintf("LoadKeyboardLayoutW(%s) returned NULL (%v)", klid, lerr), false
	}
	if wantHKL != 0 && uint32(h) != wantHKL {
		return fmt.Sprintf("HKL %08x, want %08x", uint32(h), wantHKL), false
	}
	diffs := wincheck.Check(lines, &prober{hkl: h})
	pUnloadKeyboardLayout.Call(h)
	if len(diffs) > 0 {
		var d []string
		for i, x := range diffs {
			if i == 3 {
				break
			}
			d = append(d, x.String())
		}
		return fmt.Sprintf("%d of %d lines differ: %s", len(diffs), len(lines), strings.Join(d, "; ")), false
	}
	return fmt.Sprintf("PASS %d lines, HKL %08x", len(lines), uint32(h)), true
}

type prober struct{ hkl uintptr }

func (p *prober) ToUnicode(k wincheck.Press, flags uint32) (int, []uint16) {
	var state [256]byte
	for _, m := range k.Mods {
		state[m] = 0x80
		switch m { // as cmd/yakbd-wincheck
		case 0xA1:
			state[0x10] = 0x80
		case 0x14:
			state[0x14] = 0x01
		case 0x10:
			state[0xA0] = 0x80
		case 0x11:
			state[0xA2] = 0x80
		case 0x12:
			state[0xA4] = 0x80
		}
	}
	sc, _, _ := pMapVirtualKeyExW.Call(uintptr(k.VK), 0, p.hkl)
	var buf [64]uint16
	r, _, _ := pToUnicodeEx.Call(uintptr(k.VK), sc, uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(flags), p.hkl)
	rc := int(int32(r))
	switch {
	case rc < 0 && buf[0] == 0:
		return rc, nil
	case rc < 0:
		return rc, buf[:1]
	case rc > len(buf):
		rc = len(buf)
	}
	return rc, append([]uint16(nil), buf[:rc]...)
}

func hklLoaded(hkl uint32) bool {
	n, _, _ := pGetKeyboardLayoutList.Call(0, 0)
	if n == 0 {
		return false
	}
	buf := make([]uintptr, n)
	n, _, _ = pGetKeyboardLayoutList.Call(n, uintptr(unsafe.Pointer(&buf[0])))
	for _, h := range buf[:n] {
		if uint32(h) == hkl {
			return true
		}
	}
	return false
}

func interactive() error {
	d, _, err := pOpenInputDesktop.Call(0, 0, 1)
	if d == 0 {
		return fmt.Errorf("no interactive desktop (%v): run it at the console or as a scheduled task with an interactive logon and highest privileges", err)
	}
	pCloseDesktop.Call(d)
	return nil
}

// Registry and directories (64-bit process: no redirection).

type regValue struct {
	name string
	typ  uint32
	str  string
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func hex(s string) uint64 {
	var n uint64
	fmt.Sscanf(strings.TrimPrefix(strings.ToLower(s), "0x"), "%x", &n)
	return n
}

func subKeys(root syscall.Handle, path string) []string {
	var k syscall.Handle
	if syscall.RegOpenKeyEx(root, u16(path), 0, keyRead, &k) != nil {
		return nil
	}
	defer syscall.RegCloseKey(k)
	var out []string
	for i := uint32(0); ; i++ {
		buf := make([]uint16, 512)
		n := uint32(len(buf))
		if syscall.RegEnumKeyEx(k, i, &buf[0], &n, nil, nil, nil, nil) != nil {
			return out
		}
		out = append(out, syscall.UTF16ToString(buf[:n]))
	}
}

// regValues returns the values in enumeration order; nil when the key is missing.
func regValues(root syscall.Handle, path string) []regValue {
	var k syscall.Handle
	if syscall.RegOpenKeyEx(root, u16(path), 0, keyRead, &k) != nil {
		return nil
	}
	defer syscall.RegCloseKey(k)
	out := []regValue{}
	for i := uint32(0); ; i++ {
		name := make([]uint16, 16384)
		data := make([]byte, 1<<16)
		nn, dn := uint32(len(name)), uint32(len(data))
		var typ uint32
		r, _, _ := pRegEnumValueW.Call(uintptr(k), uintptr(i), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nn)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dn)))
		if r == 234 {
			continue
		}
		if r != 0 {
			return out
		}
		v := regValue{name: syscall.UTF16ToString(name[:nn]), typ: typ}
		switch typ {
		case 1, 2:
			u := make([]uint16, dn/2)
			for j := range u {
				u[j] = uint16(data[2*j]) | uint16(data[2*j+1])<<8
			}
			v.str = syscall.UTF16ToString(u)
		case 7:
			u := make([]uint16, dn/2)
			for j := range u {
				u[j] = uint16(data[2*j]) | uint16(data[2*j+1])<<8
			}
			v.str = strings.TrimRight(string(utf16ToRunes(u)), "\x00")
		case 4:
			v.str = fmt.Sprint(uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24)
		}
		out = append(out, v)
	}
}

func utf16ToRunes(u []uint16) []rune {
	var r []rune
	for _, c := range u {
		r = append(r, rune(c))
	}
	return r
}

func regString(root syscall.Handle, path, name string) string {
	for _, v := range regValues(root, path) {
		if strings.EqualFold(v.name, name) {
			return v.str
		}
	}
	return ""
}

func dirOf(p *syscall.LazyProc) string {
	buf := make([]uint16, 512)
	n, _, _ := p.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func sysDir() string { return dirOf(pGetSystemDirectoryW) }
func wowDir() string { return dirOf(pGetSystemWow64Dir) } // "" on x86 Windows (the call fails)

// watched are the layout file names whose presence the snapshot records.
var watched []string

var pIsWow64Process2 = kernel32.NewProc("IsWow64Process2")

// nativeMachine is the native machine of IsWow64Process2 (inst §2.2).
func nativeMachine() uint16 {
	cur, _ := syscall.GetCurrentProcess()
	var proc, native uint16
	if r, _, _ := pIsWow64Process2.Call(uintptr(cur), uintptr(unsafe.Pointer(&proc)), uintptr(unsafe.Pointer(&native))); r == 0 {
		if runtime.GOARCH == "386" && wowDir() == "" {
			return payload.MachineI386
		}
		return payload.MachineAMD64
	}
	return native
}
