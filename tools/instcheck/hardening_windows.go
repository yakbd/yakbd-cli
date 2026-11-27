//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/setup"
)

// hardening runs the hardening scenarios: payload files of the wrong shape are refused with nothing changed, and a repair
// that has to take a new Layout Id enables the layout again (kbl targets/windows-install.md §3, §5).
func (c *checker) hardening() {
	c.badShape()
	c.reenable()
}

// badShape runs two installers whose payload hashes and Machine fields are right but whose files are not: the
// x86 file in the wow64 slot (014C with 4-byte pointers) and a truncated x64 file.
func (c *checker) badShape() {
	s := c.begin("refuse-bad-shape-payload")
	base := snapshot()
	bad := []string{"demo-badshape-setup.exe", "demo-damaged-setup.exe"}
	if nativeMachine() == payload.MachineI386 {
		bad = []string{"demo-badx86-setup.exe", "demo-damagedx86-setup.exe"} // x86 Windows gets only the x86 file
	}
	for _, n := range bad {
		l, err := c.load(n, "demo.expect.txt")
		if err != nil {
			s.failf("%v", err)
			continue
		}
		tag := "hardening-" + strings.TrimSuffix(n, "-setup.exe")
		if code := c.runInstaller(s, l.exe, tag, "/quiet", "/enable"); code != setup.ExitDamaged {
			s.failf("%s: exit code %d, want %d", n, code, setup.ExitDamaged)
		}
		log, _ := os.ReadFile(filepath.Join(c.logs, tag+".log"))
		if !strings.Contains(string(log), "refused: the ") {
			s.failf("%s: the log has no shape refusal", n)
		}
		for _, line := range strings.Split(string(log), "\n") {
			if strings.Contains(line, "refused: ") {
				s.notef("%s: %s", n, strings.TrimSpace(line[strings.Index(line, "refused: "):]))
			}
		}
		if after := snapshot(); after != base {
			s.failf("%s changed the state: %s", n, firstDiff(base, after))
		}
	}
	s.end()
}

const taker = "b0990409"

// takenRepair installs and enables demo, then makes the M7 case with a taken Layout Id (the KLID key deleted and
// another KLID of LANGID 0409 given the layout's Layout Id) and runs setup.exe /repair. It checks the new Layout
// Id (record and KLID agree) and that the layout is in the user's list, and returns the KLID and the new HKL.
func (c *checker) takenRepair(s *scenario, a *layout, tag string) (string, uint32, bool) {
	if code := c.runInstaller(s, a.exe, tag+"-install", "/quiet", "/enable"); code != 0 {
		s.failf("install: exit code %d", code)
	}
	klid, id := c.checkInstalled(s, a, true)
	if klid == "" {
		return "", 0, false
	}
	lang := uint32(hex(a.p.Manifest.Layout.LangID))
	oldHKL := lang | uint32(0xF000|id)<<16
	s.notef("before: KLID %s, Layout Id %04x, HKL %08x in a new process: %v", klid, id, oldHKL, c.hklInNewProcess(oldHKL))
	tk := layoutsKey + `\` + taker
	for _, v := range [][2]string{{"Layout File", "kbdus.dll"}, {"Layout Text", "yakbd TI3 Layout Id taker"}, {"Layout Id", fmt.Sprintf("%04x", id)}} {
		if out, err := exec.Command("reg.exe", "add", `HKLM\`+tk, "/v", v[0], "/t", "REG_SZ", "/d", v[1], "/f").CombinedOutput(); err != nil {
			s.failf("cannot write %s\\%s: %v %s", taker, v[0], err, strings.TrimSpace(string(out)))
		}
	}
	if r, _, _ := pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(layoutsKey+`\`+klid)))); r != 0 {
		s.failf("cannot delete the KLID key for the test: %v", syscall.Errno(r))
	}
	installed := c.installedExe(a)
	if installed == "" {
		s.failf("no installed setup.exe")
	} else if code := c.runInstaller(s, installed, tag+"-repair", "/repair", "/quiet"); code != 0 && code != 3010 {
		s.failf("repair: exit code %d", code)
	}
	n := c.checkRegistry(s, a, klid, 0)
	rec := regString(hkeyLocalMachine, uninstallKey+`\`+a.p.Manifest.Product.Code, "YakbdLayoutId")
	switch {
	case n == id:
		s.failf("the Layout Id is still %04x, which %s has", id, taker)
	case rec != fmt.Sprintf("%04x", n):
		s.failf("the record says Layout Id %q, the KLID %04x", rec, n)
	}
	if !userHas(klid) {
		s.failf("the layout is not in the user's list after the repair")
	}
	log, _ := os.ReadFile(filepath.Join(c.logs, tag+"-repair.log"))
	if !strings.Contains(string(log), "re-enable: Layout Id") {
		s.failf("the repair log has no re-enable step")
	}
	newHKL := lang | uint32(0xF000|n)<<16
	s.notef("after: Layout Id %04x, HKL %08x", n, newHKL)
	return klid, newHKL, true
}

// cleanTaken deletes the taker KLID and uninstalls; the state must equal base.
func (c *checker) cleanTaken(s *scenario, a *layout, tag, base string) {
	if r, _, _ := pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(layoutsKey+`\`+taker)))); r != 0 {
		s.failf("cannot delete %s: %v", taker, syscall.Errno(r))
	}
	c.uninstall(s, a, tag+"-uninstall", base)
}

// reenable is VM-9 without a sign-in: after the repair (which removes the layout and adds it again in the user's
// own process, D69) the new HKL must be in a new process's GetKeyboardLayoutList and type. Bundle run 1, with one
// plain call, kept the old HKL in the session. The sign-in phases (reenable-prepare, reenable-check) check the
// new HKL after a cold boot as well.
func (c *checker) reenable() {
	a, err := c.load("demo-setup.exe", "demo.expect.txt")
	s := c.begin("repair-new-layout-id-reenables")
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	base := snapshot()
	klid, newHKL, ok := c.takenRepair(s, a, "hardening")
	if ok {
		if !c.hklInNewProcess(newHKL) {
			s.failf("the new HKL %08x is not in a new process's GetKeyboardLayoutList (VM-9)", newHKL)
		}
		if msg, ok := typeLayout(klid, a.expect, newHKL); !ok {
			s.failf("64-bit typing: %s", msg)
		} else {
			s.notef("64-bit %s", msg)
		}
		if out, err := exec.Command(filepath.Join(c.bundle, "instcheck-32.exe"), "-type", klid, a.expect).CombinedOutput(); err != nil {
			s.failf("32-bit typing: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}
	c.cleanTaken(s, a, "hardening", base)
	s.end()
}

// reenablePhase runs the sign-in test in two phases around a cold boot with automatic sign-in: prepare = install,
// enable, taken Layout Id, repair, left installed; check = after the sign-in the new HKL is in the session and in a
// new process, the layout types with it in 64- and 32-bit processes; then cleaned up, nothing left.
func (c *checker) reenablePhase(phase, res string) bool {
	if !strings.HasPrefix(phase, "reenable-") {
		return false
	}
	stateFile, baseFile := filepath.Join(res, "reenable-state.txt"), filepath.Join(res, "reenable-base.txt")
	a, err := c.load("demo-setup.exe", "demo.expect.txt")
	if err != nil {
		s := c.begin(phase)
		s.failf("%v", err)
		s.end()
		return true
	}
	watched = append(watched, a.p.Manifest.Layout.File)
	switch phase {
	case "reenable-prepare":
		s := c.begin("reenable-prepare")
		base := snapshot()
		_ = os.WriteFile(baseFile, []byte(base), 0o644)
		if klid, hkl, ok := c.takenRepair(s, a, "signin"); ok {
			_ = os.WriteFile(stateFile, []byte(fmt.Sprintf("%s %08x\n", klid, hkl)), 0o644)
		}
		s.end()
	case "reenable-check":
		var klid string
		var hkl uint32
		b, err := os.ReadFile(stateFile)
		if err == nil {
			_, err = fmt.Sscanf(string(b), "%s %x", &klid, &hkl)
		}
		s := c.begin("reenable-after-sign-in")
		if err != nil || klid == "" {
			s.failf("no state from reenable-prepare (%v)", err)
			s.end()
			return true
		}
		if !userHas(klid) {
			s.failf("%s is not in the user's list after the sign-in", klid)
		}
		if !hklLoaded(hkl) {
			s.failf("HKL %08x is not in this process's GetKeyboardLayoutList after the sign-in", hkl)
		}
		if !c.hklInNewProcess(hkl) {
			s.failf("HKL %08x is not in a new process's GetKeyboardLayoutList after the sign-in", hkl)
		}
		if msg, ok := typeLayout(klid, a.expect, hkl); !ok {
			s.failf("64-bit typing: %s", msg)
		} else {
			s.notef("64-bit %s", msg)
		}
		if out, err := exec.Command(filepath.Join(c.bundle, "instcheck-32.exe"), "-type", klid, a.expect).CombinedOutput(); err != nil {
			s.failf("32-bit typing: %s (%v)", strings.TrimSpace(string(out)), err)
		}
		s.end()
		base, _ := os.ReadFile(baseFile)
		s = c.begin("reenable-cleanup-leaves-nothing")
		c.cleanTaken(s, a, "signin", string(base))
		s.end()
	default:
		return false
	}
	return true
}

func (s *scenario) probsNone() bool { return len(s.probs) == 0 }

// hklInNewProcess asks a new instcheck process whether its GetKeyboardLayoutList has the HKL (an existing process
// may keep the list it had).
func (c *checker) hklInNewProcess(hkl uint32) bool {
	exe, _ := os.Executable()
	out, err := exec.Command(exe, "-hkl", fmt.Sprintf("%08x", hkl)).CombinedOutput()
	fmt.Fprintf(c.detail, "  new process, HKL %08x: %s (%v)\n", hkl, strings.TrimSpace(string(out)), err)
	return err == nil
}

// layoutList prints this process's GetKeyboardLayoutList and reports whether it has the HKL.
func layoutList(want uint32) bool {
	n, _, _ := pGetKeyboardLayoutList.Call(0, 0)
	var hs []string
	found := false
	if n > 0 {
		buf := make([]uintptr, n)
		n, _, _ = pGetKeyboardLayoutList.Call(n, uintptr(unsafe.Pointer(&buf[0])))
		for _, h := range buf[:n] {
			hs = append(hs, fmt.Sprintf("%08x", uint32(h)))
			found = found || uint32(h) == want
		}
	}
	fmt.Println(strings.Join(hs, " "))
	return found
}
