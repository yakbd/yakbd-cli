//go:build windows

// Command clicheck runs the msiexec-compatibility scenarios of docs/installer-cli.md in a Windows guest, for a
// layout installer and for yakbd-setup.exe, and writes one PASS/FAIL line per scenario (tools/clibundle writes the
// bundle and its README.txt). Run elevated (an administrator's elevated PowerShell):
//
//	clicheck.exe -bundle C:\tx -phase main           # everything; ends by letting the installer restart Windows
//	clicheck.exe -bundle C:\tx -phase after-restart  # after the restart and sign-in
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
)

const (
	hklm         = syscall.Handle(0x80000002)
	hkcu         = syscall.Handle(0x80000001)
	layoutsKey   = `SYSTEM\CurrentControlSet\Control\Keyboard Layouts`
	uninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	envKey       = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	smKey        = `SYSTEM\CurrentControlSet\Control\Session Manager`
	lockName     = `Global\YAKBD-Setup`
)

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	advapi32      = syscall.NewLazyDLL("advapi32.dll")
	pCreateMutexW = kernel32.NewProc("CreateMutexW")
	pCreateFileW  = kernel32.NewProc("CreateFileW")
	pRegDeleteKey = advapi32.NewProc("RegDeleteKeyExW")
	pRegEnumValue = advapi32.NewProc("RegEnumValueW")
)

type checker struct {
	dir, logs string
	out       *os.File
	detail    *os.File
	pass, all int
	// layout installers L0 (0.5.0), L1 (1.0.0), L2 (2.0.0); tool installers T0, T1, T2.
	l0, l1, l2, t0, t1, t2 string
	code, file             string // the layout's product code and DLL name
	tcode                  string
}

func main() {
	bundle := flag.String("bundle", `C:\tx`, "bundle folder")
	phase := flag.String("phase", "main", "main or after-restart")
	flag.Parse()
	c := &checker{dir: *bundle, logs: filepath.Join(*bundle, "results", "logs")}
	if err := os.MkdirAll(c.logs, 0o755); err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	var err error
	if c.out, err = os.Create(filepath.Join(*bundle, "results", "tx-cli-"+*phase+".txt")); err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	c.detail, _ = os.Create(filepath.Join(*bundle, "results", "tx-cli-"+*phase+"-details.txt"))
	c.l0, c.l1, c.l2 = c.path("txcli-v0-setup.exe"), c.path("txcli-v1-setup.exe"), c.path("txcli-v2-setup.exe")
	c.t0, c.t1, c.t2 = c.path("yakbd-setup-0.1.0.exe"), c.path("yakbd-setup-0.2.0.exe"), c.path("yakbd-setup-0.3.0.exe")
	b, err := os.ReadFile(c.l1)
	if err == nil {
		var p *payload.Payload
		if p, err = payload.FromExe(b); err == nil {
			c.code, c.file = p.Manifest.Product.Code, p.Manifest.Layout.File
		}
	}
	if err == nil {
		if b, err = os.ReadFile(c.t1); err == nil {
			var p *toolsetup.Payload
			if p, err = toolsetup.FromExe(b); err == nil {
				c.tcode = p.Manifest.Product.Code
			}
		}
	}
	if err != nil {
		c.line("FAIL setup: cannot read the installers: %v", err)
		os.Exit(1)
	}
	if *phase == "after-restart" {
		c.afterRestart()
	} else {
		c.layout()
		c.tool()
		c.restart() // last: the installer restarts Windows
	}
	c.line("SUMMARY %d of %d passed", c.pass, c.all)
}

func (c *checker) path(n string) string { return filepath.Join(c.dir, n) }

func (c *checker) line(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	fmt.Println(s)
	fmt.Fprintln(c.out, s)
	fmt.Fprintln(c.detail, s)
	c.out.Sync()
}

// scenario collects the failures of one scenario.
type scenario struct {
	c    *checker
	name string
	fail []string
}

func (c *checker) begin(name string) *scenario {
	fmt.Fprintf(c.detail, "== %s\n", name)
	return &scenario{c: c, name: name}
}

func (s *scenario) failf(format string, a ...any) { s.fail = append(s.fail, fmt.Sprintf(format, a...)) }

func (s *scenario) end() {
	s.c.all++
	if len(s.fail) == 0 {
		s.c.pass++
		s.c.line("PASS %s", s.name)
		return
	}
	s.c.line("FAIL %s: %s", s.name, strings.Join(s.fail, "; "))
}

// run runs a program with the arguments and returns its exit code.
func (c *checker) run(s *scenario, exe string, args ...string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	start := time.Now()
	err := cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	fmt.Fprintf(c.detail, "  ran %s %q: exit %d (%v) in %s\n", filepath.Base(exe), args, code, err, time.Since(start).Round(time.Millisecond))
	if ctx.Err() != nil {
		s.failf("%s %v timed out", filepath.Base(exe), args)
	}
	return code
}

// expect runs and checks the exit code.
func (c *checker) expect(s *scenario, want int, exe string, args ...string) int {
	got := c.run(s, exe, args...)
	if got != want {
		s.failf("%s %s: exit %d, want %d", filepath.Base(exe), strings.Join(args, " "), got, want)
	}
	return got
}

func (c *checker) log(tag string) string { return filepath.Join(c.logs, tag+".log") }

// splitCommand splits an uninstall-entry command line: a quoted program, then arguments at spaces.
func splitCommand(s string) (string, []string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		if i := strings.Index(s[1:], `"`); i >= 0 {
			return s[1 : i+1], strings.Fields(s[i+2:])
		}
	}
	f := strings.Fields(s)
	if len(f) == 0 {
		return "", nil
	}
	return f[0], f[1:]
}

// runEntry runs a command line from the uninstall entry with extra arguments appended (as winget appends its
// repair switch to ModifyPath).
func (c *checker) runEntry(s *scenario, want int, key, value string, extra ...string) {
	cmd := regString(hklm, uninstallKey+`\`+key, value)
	if cmd == "" {
		s.failf("no %s in the uninstall entry", value)
		return
	}
	exe, args := splitCommand(cmd)
	c.expect(s, want, exe, append(args, extra...)...)
}

// ---- registry and files ----

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

type regValue struct {
	name string
	typ  uint32
	str  string
}

func openKey(root syscall.Handle, path string) (syscall.Handle, bool) {
	var k syscall.Handle
	if syscall.RegOpenKeyEx(root, u16(path), 0, syscall.KEY_READ|0x0100, &k) != nil {
		return 0, false
	}
	return k, true
}

func subKeys(root syscall.Handle, path string) []string {
	k, ok := openKey(root, path)
	if !ok {
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

func regValues(root syscall.Handle, path string) []regValue {
	k, ok := openKey(root, path)
	if !ok {
		return nil
	}
	defer syscall.RegCloseKey(k)
	var out []regValue
	for i := uint32(0); ; i++ {
		name := make([]uint16, 16384)
		data := make([]byte, 1<<20)
		nn, dn := uint32(len(name)), uint32(len(data))
		var typ uint32
		r, _, _ := pRegEnumValue.Call(uintptr(k), uintptr(i), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nn)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dn)))
		if r != 0 {
			return out
		}
		v := regValue{name: syscall.UTF16ToString(name[:nn]), typ: typ}
		if typ == 1 || typ == 2 || typ == 7 {
			u := make([]uint16, dn/2)
			for j := range u {
				u[j] = uint16(data[2*j]) | uint16(data[2*j+1])<<8
			}
			v.str = strings.TrimRight(string(utf16.Decode(u)), "\x00")
		}
		out = append(out, v)
	}
}

func regString(root syscall.Handle, path, name string) string {
	for _, v := range regValues(root, path) {
		if strings.EqualFold(v.name, name) {
			return v.str
		}
	}
	return ""
}

func hasValue(root syscall.Handle, path, name string) bool {
	for _, v := range regValues(root, path) {
		if strings.EqualFold(v.name, name) {
			return true
		}
	}
	return false
}

func keyExists(root syscall.Handle, path string) bool {
	k, ok := openKey(root, path)
	if ok {
		syscall.RegCloseKey(k)
	}
	return ok
}

func deleteKey(path string) error {
	if r, _, _ := pRegDeleteKey.Call(uintptr(hklm), uintptr(unsafe.Pointer(u16(path))), 0x0100, 0); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// sysDir is the native System32 (this checker is a 64-bit program).
func sysDir() string { return filepath.Join(os.Getenv("SystemRoot"), "System32") }

func wowDir() string { return filepath.Join(os.Getenv("SystemRoot"), "SysWOW64") }

func toolDir() string { return filepath.Join(os.Getenv("ProgramW6432"), "YAKBD", "yakbd") }

// layoutKLIDs lists the KLIDs whose Layout Product Code is code.
func layoutKLIDs(code string) []string {
	var out []string
	for _, k := range subKeys(hklm, layoutsKey) {
		if strings.EqualFold(regString(hklm, layoutsKey+`\`+k, "Layout Product Code"), code) {
			out = append(out, k)
		}
	}
	return out
}

// snapshot is the state the scenarios compare: layout KLIDs, uninstall entries, layout and tool files, PATH.
func (c *checker) snapshot() string {
	var l []string
	for _, k := range layoutKLIDs(c.code) {
		l = append(l, "klid "+k+" id "+regString(hklm, layoutsKey+`\`+k, "Layout Id"))
	}
	for _, code := range []string{c.code, c.tcode} {
		k := uninstallKey + `\` + code
		if keyExists(hklm, k) {
			l = append(l, "arp "+code+" "+regString(hklm, k, "DisplayVersion"))
		}
	}
	for _, d := range []string{sysDir(), wowDir()} {
		m, _ := filepath.Glob(filepath.Join(d, strings.TrimSuffix(c.file, ".dll")+".dll*"))
		l = append(l, m...)
	}
	m, _ := filepath.Glob(filepath.Join(toolDir(), "*"))
	l = append(l, m...)
	l = append(l, "path "+regString(hklm, envKey, "Path"))
	sort.Strings(l)
	return strings.Join(l, "\n")
}

func (c *checker) unchanged(s *scenario, before string) {
	if after := c.snapshot(); after != before {
		s.failf("state changed:\n%s\n-- before:\n%s", after, before)
	}
}

// hold opens a file without FILE_SHARE_DELETE or write sharing, so that it cannot be replaced or deleted (a file in
// use); the returned function closes it.
func hold(path string) (func(), error) {
	const genericRead, shareRead, openExisting = 0x80000000, 0x1, 3
	h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(path))), genericRead, shareRead, 0, openExisting, 0, 0)
	if syscall.Handle(h) == syscall.InvalidHandle {
		return nil, err
	}
	return func() { syscall.CloseHandle(syscall.Handle(h)) }, nil
}

// lock holds the installers' mutex like a running installer.
func lock() (func(), error) {
	m, _, err := pCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(u16(lockName))))
	if m == 0 {
		return nil, err
	}
	return func() { syscall.CloseHandle(syscall.Handle(m)) }, nil
}

func pending() string {
	for _, v := range regValues(hklm, smKey) {
		if strings.EqualFold(v.name, "PendingFileRenameOperations") {
			return strings.ReplaceAll(v.str, "\x00", " | ")
		}
	}
	return ""
}

func fileHas(path, s string) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Contains(b, []byte(s))
}

// ---- layout installer ----

func (c *checker) layout() {
	base := c.snapshot()
	arp := uninstallKey + `\` + c.code
	l1 := c.l1

	s := c.begin("L-bad-command-line-1639")
	// With no /q option a bad command line shows the usage dialog (as msiexec shows its help), which waits for a
	// click: the checker sends /quiet with it.
	for _, a := range [][]string{{"/bogus", "/quiet"}, {"/quiet", "/promptrestart"}, {"/x", "/i", "/quiet"}, {"/repair", "/quiet"}, {"/quiet", "ALLUSERS="}} {
		c.expect(s, 1639, l1, a...)
	}
	c.expect(s, 1635, l1, "/p", "x.msp", "/quiet")
	c.expect(s, 1624, l1, "/quiet", "TRANSFORMS=x.mst")
	c.unchanged(s, base)
	s.end()

	s = c.begin("L-not-installed-1605")
	c.expect(s, 1605, l1, "/x", "/quiet", "/norestart")
	c.expect(s, 1605, l1, "/f", "/quiet", "/norestart")
	c.expect(s, 1605, l1, "/x", "{11111111-2222-3333-4444-555555555555}", "/quiet")
	c.unchanged(s, base)
	s.end()

	s = c.begin("L-log-folder-missing-1622")
	c.expect(s, 1622, l1, "/quiet", "/norestart", "/l*v", `C:\no-such-folder-tx\x.log`)
	c.unchanged(s, base)
	s.end()

	s = c.begin("L-install-quiet-and-entry")
	lg := c.log("l-install")
	_ = os.WriteFile(lg, []byte("OLD CONTENT\n"), 0o644)
	c.expect(s, 0, l1, "/quiet", "/norestart", "/l*v", lg)
	if fileHas(lg, "OLD CONTENT") || !fileHas(lg, "done: exit code 0") {
		s.failf("log not replaced or not complete")
	}
	if v := regString(hklm, arp, "DisplayVersion"); v != "1.0.0.0" {
		s.failf("DisplayVersion %q", v)
	}
	if len(layoutKLIDs(c.code)) != 1 {
		s.failf("KLIDs %v", layoutKLIDs(c.code))
	}
	exe := `"` + filepath.Join(regString(hklm, arp, "InstallLocation"), "setup.exe") + `"`
	for name, want := range map[string]string{"UninstallString": exe + " /x " + c.code,
		"QuietUninstallString": exe + " /x " + c.code + " /quiet /norestart", "ModifyPath": exe + " /f " + c.code} {
		if got := regString(hklm, arp, name); got != want {
			s.failf("%s = %q, want %q", name, got, want)
		}
	}
	for _, n := range []string{"WindowsInstaller", "NoModify", "NoRepair", "NoRemove"} {
		if hasValue(hklm, arp, n) {
			s.failf("entry has %s", n)
		}
	}
	s.end()
	installed := c.snapshot()

	s = c.begin("L-same-version-repair-log-append")
	lg = c.log("l-append")
	c.expect(s, 0, l1, "/i", "/qn", "/norestart", "/l*v+", lg)
	c.expect(s, 0, l1, "/package", c.code, "/qn", "/norestart", "/l*v+", lg)
	if b, _ := os.ReadFile(lg); bytes.Count(b, []byte("start:")) < 2 {
		s.failf("log not appended (%d starts)", bytes.Count(b, []byte("start:")))
	}
	c.unchanged(s, installed)
	s.end()

	s = c.begin("L-another-installer-running-1618")
	if rel, err := lock(); err != nil {
		s.failf("cannot take the mutex: %v", err)
	} else {
		c.expect(s, 1618, c.l2, "/quiet", "/norestart")
		rel()
	}
	c.unchanged(s, installed)
	s.end()

	s = c.begin("L-downgrade-refused-1638")
	c.expect(s, 1638, c.l0, "/quiet", "/norestart")
	c.unchanged(s, installed)
	s.end()

	s = c.begin("L-upgrade-file-in-use-3010")
	klid := layoutKLIDs(c.code)
	rel, err := hold(filepath.Join(sysDir(), c.file))
	if err != nil {
		s.failf("cannot hold the layout file: %v", err)
	}
	c.expect(s, 3010, c.l2, "/passive", "/norestart", "/log", c.log("l-upgrade"))
	if rel != nil {
		rel()
	}
	if v := regString(hklm, arp, "DisplayVersion"); v != "2.0.0.0" {
		s.failf("DisplayVersion %q", v)
	}
	if k := layoutKLIDs(c.code); fmt.Sprint(k) != fmt.Sprint(klid) {
		s.failf("KLID %v, before %v", k, klid)
	}
	if p := pending(); !strings.Contains(strings.ToLower(p), strings.ToLower(c.file)+".yakbd-new") {
		s.failf("no pending replacement: %s", p)
	}
	s.end()

	s = c.begin("L-repair-modifypath-winget-style")
	k := layoutKLIDs(c.code)
	if len(k) == 1 {
		if err := deleteKey(layoutsKey + `\` + k[0]); err != nil {
			s.failf("cannot delete the KLID for the test: %v", err)
		}
	}
	c.runEntry(s, 0, c.code, "ModifyPath", "/quiet", "/norestart", "/log", c.log("l-modify"))
	if fmt.Sprint(layoutKLIDs(c.code)) != fmt.Sprint(k) {
		s.failf("KLID %v after the repair, want %v", layoutKLIDs(c.code), k)
	}
	c.expect(s, 0, c.l2, "/i", "REINSTALL=ALL", "REINSTALLMODE=vomus", "/qn", "/norestart")
	c.expect(s, 0, c.l2, "/fvomus", c.code, "/qn", "/norestart")
	s.end()

	s = c.begin("L-quiet-uninstall-string")
	c.runEntry(s, 0, c.code, "QuietUninstallString", "/log", c.log("l-uninstall"))
	c.expect(s, 1605, c.l2, "/x", "/quiet", "/norestart")
	c.unchanged(s, base)
	s.end()

	s = c.begin("L-uninstall-by-package-file-and-code")
	c.expect(s, 0, l1, "/quiet", "/norestart")
	c.expect(s, 0, c.l2, "/uninstall", l1, "/quiet", "/norestart")
	c.expect(s, 0, l1, "/quiet", "/norestart")
	c.expect(s, 0, l1, "/X"+c.code, "/qn", "/norestart")
	c.expect(s, 1619, l1, "/x", c.t1, "/quiet")
	c.unchanged(s, base)
	s.end()

	s = c.begin("L-winget-manifest-switches")
	// docs/installer-cli.md §10: Silent, SilentWithProgress, Log, Repair (ModifyPath + Repair), uninstall
	// (QuietUninstallString).
	c.expect(s, 0, l1, "/quiet", "/norestart", "/log", c.log("winget-silent"))
	c.expect(s, 0, c.l2, "/passive", "/norestart", "/log", c.log("winget-progress"))
	c.runEntry(s, 0, c.code, "ModifyPath", "/quiet", "/norestart")
	c.runEntry(s, 0, c.code, "QuietUninstallString")
	c.unchanged(s, base)
	s.end()

	s = c.begin("L-enable-property")
	c.expect(s, 0, l1, "/quiet", "/norestart", "ENABLE=1")
	if k := layoutKLIDs(c.code); len(k) != 1 || !userHas(k[0]) {
		s.failf("ENABLE=1: not in the user's list")
	}
	c.expect(s, 0, l1, "/x", "/quiet", "/norestart")
	c.expect(s, 0, l1, "/quiet", "/norestart", "ENABLE=0")
	if k := layoutKLIDs(c.code); len(k) != 1 || userHas(k[0]) {
		s.failf("ENABLE=0: in the user's list")
	}
	c.expect(s, 0, l1, "/x", "/quiet", "/norestart")
	c.unchanged(s, base)
	s.end()
}

// userHas reports whether the current user's Preload or User Profile names the KLID.
func userHas(klid string) bool {
	for _, v := range regValues(hkcu, `Keyboard Layout\Preload`) {
		if strings.EqualFold(v.str, klid) {
			return true
		}
	}
	up := `Control Panel\International\User Profile`
	for _, t := range subKeys(hkcu, up) {
		for _, v := range regValues(hkcu, up+`\`+t) {
			if strings.HasSuffix(strings.ToLower(v.name), ":"+strings.ToLower(klid)) {
				return true
			}
		}
	}
	return false
}

// ---- tool installer ----

func (c *checker) tool() {
	base := c.snapshot()
	arp := uninstallKey + `\` + c.tcode
	dir := toolDir()
	exe := filepath.Join(dir, "yakbd.exe")

	s := c.begin("T-install-quiet-path-entry")
	c.expect(s, 0, c.t1, "/quiet", "/norestart", "/l*v", c.log("t-install"))
	if b, err := os.ReadFile(exe); err != nil {
		s.failf("no %s", exe)
	} else if m, _ := payload.PEMachine(b); m != 0x8664 {
		s.failf("yakbd.exe has Machine %04X, want 8664", m)
	}
	path := regString(hklm, envKey, "Path")
	if !strings.Contains(strings.ToLower(path), strings.ToLower(dir)) {
		s.failf("machine Path lacks %s: %s", dir, path)
	}
	if out, err := runWithMachinePath(); err != nil || !strings.HasPrefix(out, "yakbd 0.") {
		s.failf("yakbd version through the machine PATH: %q %v", out, err)
	}
	if v := regString(hklm, arp, "DisplayVersion"); v != "0.2.0" && v != "0.2.0.0" {
		s.failf("DisplayVersion %q", v)
	}
	if !strings.HasSuffix(regString(hklm, arp, "QuietUninstallString"), "/x "+c.tcode+" /quiet /norestart") || hasValue(hklm, arp, "WindowsInstaller") {
		s.failf("uninstall entry values")
	}
	s.end()
	installed := c.snapshot()

	s = c.begin("T-downgrade-refused-1638")
	c.expect(s, 1638, c.t0, "/quiet", "/norestart")
	c.unchanged(s, installed)
	s.end()

	s = c.begin("T-another-installer-running-1618")
	if rel, err := lock(); err == nil {
		c.expect(s, 1618, c.t2, "/quiet", "/norestart")
		rel()
	} else {
		s.failf("mutex: %v", err)
	}
	c.unchanged(s, installed)
	s.end()

	s = c.begin("T-repair-modifypath")
	_ = os.Remove(exe)
	c.runEntry(s, 0, c.tcode, "ModifyPath", "/quiet", "/norestart")
	c.unchanged(s, installed)
	s.end()

	s = c.begin("T-upgrade-in-use-3010")
	// The three versions carry the same yakbd.exe bytes: make the installed one differ, so the upgrade must replace it.
	if f, err := os.OpenFile(exe, os.O_APPEND|os.O_WRONLY, 0); err == nil {
		f.Write([]byte{0})
		f.Close()
	}
	rel, err := hold(exe)
	if err != nil {
		s.failf("cannot hold yakbd.exe: %v", err)
	}
	c.expect(s, 3010, c.t2, "/quiet", "/norestart", "/log", c.log("t-upgrade"))
	if rel != nil {
		rel()
	}
	if v := regString(hklm, arp, "DisplayVersion"); v != "0.3.0.0" {
		s.failf("DisplayVersion %q", v)
	}
	if p := pending(); !strings.Contains(strings.ToLower(p), "yakbd.exe.yakbd-new") {
		s.failf("no pending replacement: %s", p)
	}
	s.end()

	s = c.begin("T-quiet-uninstall-string-path-removed")
	c.runEntry(s, 0, c.tcode, "QuietUninstallString", "/log", c.log("t-uninstall"))
	if strings.Contains(strings.ToLower(regString(hklm, envKey, "Path")), strings.ToLower(dir)) {
		s.failf("machine Path still has %s", dir)
	}
	if _, err := os.Stat(exe); err == nil {
		s.failf("%s left", exe)
	}
	c.unchanged(s, base)
	s.end()

	s = c.begin("T-uninstall-by-product-code")
	c.expect(s, 0, c.t1, "/quiet", "/norestart")
	c.expect(s, 0, c.t1, "/x", c.tcode, "/quiet", "/norestart")
	c.expect(s, 1605, c.t1, "/x", "/quiet", "/norestart")
	c.expect(s, 1639, c.t1, "/quiet", "INSTALLDIR=C:\\x")
	c.unchanged(s, base)
	s.end()
}

// runWithMachinePath runs "yakbd version" the way a new terminal would find it: PATH from the machine and user
// values in the registry, expanded.
func runWithMachinePath() (string, error) {
	p := expandPercent(regString(hklm, envKey, "Path"))
	env := []string{"PATH=" + p, "SystemRoot=" + os.Getenv("SystemRoot")}
	cmd := exec.Command(filepath.Join(sysDir(), "cmd.exe"), "/c", "yakbd version")
	cmd.Env = env
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// expandPercent expands %NAME% from this process's environment.
func expandPercent(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		name := s[i+1 : i+1+j]
		if v, ok := os.LookupEnv(name); ok {
			b.WriteString(v)
		} else {
			b.WriteString("%" + name + "%")
		}
		s = s[i+2+j:]
	}
}

// ---- restart (last) ----

func (c *checker) restart() {
	s := c.begin("R-prepare-quiet-restart-1641")
	c.expect(s, 0, c.l1, "/quiet", "/norestart")
	rel, err := hold(filepath.Join(sysDir(), c.file))
	if err != nil {
		s.failf("cannot hold the layout file: %v", err)
	}
	s.end()
	c.line("NOTE the next run (layout v2 /quiet without /norestart, a file in use) restarts Windows; then run -phase after-restart")
	// No /norestart: msiexec's rule, the installer restarts Windows by itself (docs/installer-cli.md §5).
	code := c.run(s, c.l2, "/quiet", "/log", c.log("r-restart"))
	c.line("NOTE installer exit code %d (1641 expected; Windows may restart before this line is written)", code)
	if rel != nil {
		rel()
	}
	time.Sleep(2 * time.Minute)
	c.line("FAIL R-restart-started: Windows did not restart within 2 minutes")
}

func (c *checker) afterRestart() {
	s := c.begin("R-restart-1641")
	// The line written before InitiateShutdownW; "done: exit code 1641" may not get written before Windows stops.
	if !fileHas(c.log("r-restart"), "restart: initiating a restart (exit code 1641)") {
		s.failf("the log of the run does not say it started the restart")
	}
	s.end()

	// Whether the delayed replacement through Sysnative lands (docs/installer.md §8.1: not observed before).
	s = c.begin("R-delayed-replacement-landed")
	if v := regString(hklm, uninstallKey+`\`+c.code, "DisplayVersion"); v != "2.0.0.0" {
		s.failf("DisplayVersion %q", v)
	}
	if m, _ := filepath.Glob(filepath.Join(sysDir(), c.file+".yakbd-*")); len(m) > 0 {
		s.failf("left after the restart: %v", m)
	}
	want, _ := os.ReadFile(c.l2)
	if p, err := payload.FromExe(want); err == nil {
		if got, _ := os.ReadFile(filepath.Join(sysDir(), c.file)); !bytes.Equal(got, p.Data[payload.BuildX64]) {
			s.failf("System32\\%s is not the v2 file after the restart", c.file)
		}
	}
	s.end()

	s = c.begin("R-cleanup")
	c.expect(s, 0, c.l2, "/x", "/quiet", "/norestart")
	if k := layoutKLIDs(c.code); len(k) != 0 || keyExists(hklm, uninstallKey+`\`+c.code) || keyExists(hklm, uninstallKey+`\`+c.tcode) {
		s.failf("left: KLIDs %v", k)
	}
	s.end()
}
