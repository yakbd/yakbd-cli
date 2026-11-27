//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// arm64 runs one phase of the ARM64 test (bundle ti-installer-arm64): install the all-builds installer and check
// the files (ARM64X file in System32, WOW64 file in SysWOW64, installation §2.4), registry and enabled state; after a
// cold boot and automatic sign-in, check that the layout is in the session and types in a native ARM64, an x64 and
// a 32-bit process; then uninstall and check that nothing is left. State between phases is kept in results\.
func (c *checker) arm64(phase, res string) bool {
	l, err := c.load("demo-setup.exe", "demo.expect.txt")
	if err != nil {
		s := c.begin(phase)
		s.failf("%v", err)
		s.end()
		return true
	}
	watched = append(watched, l.p.Manifest.Layout.File)
	stateFile := filepath.Join(res, "arm64-state.txt")
	baseFile := filepath.Join(res, "arm64-base.txt")
	switch phase {
	case "arm64-install":
		s := c.begin("arm64-install")
		if n := nativeMachine(); n != payload.MachineARM64 {
			s.failf("native machine %#04x, not ARM64", n)
		}
		if !l.p.Manifest.Has(payload.BuildARM64X) {
			s.failf("the installer has no arm64x build")
		}
		base := snapshot()
		_ = os.WriteFile(baseFile, []byte(base), 0o644)
		if code := c.runInstaller(s, l.exe, "arm64-install", "/quiet"); code != 0 {
			s.failf("exit code %d, want 0", code)
		}
		klid, id := c.checkInstalled(s, l, true)
		_ = os.WriteFile(stateFile, []byte(fmt.Sprintf("%s %04x\n", klid, id)), 0o644)
		s.end()
	case "arm64-check":
		var klid string
		var id uint16
		b, err := os.ReadFile(stateFile)
		if err == nil {
			_, err = fmt.Sscanf(string(b), "%s %x", &klid, &id)
		}
		s := c.begin("arm64-after-cold-boot")
		if err != nil || klid == "" {
			s.failf("no state from arm64-install (%v)", err)
			s.end()
			return true
		}
		hkl := uint32(hex(l.p.Manifest.Layout.LangID)) | uint32(0xF000|id)<<16
		if !userHas(klid) {
			s.failf("%s is not in the user's list after the restart", klid)
		}
		if hklLoaded(hkl) {
			s.notef("HKL %08x is in the session", hkl)
		} else {
			s.failf("HKL %08x is not in GetKeyboardLayoutList after sign-in", hkl)
		}
		if out, err := exec.Command("tasklist", "/fi", "imagename eq ctfmon.exe", "/nh").CombinedOutput(); err == nil {
			s.notef("ctfmon running: %v", strings.Contains(strings.ToLower(string(out)), "ctfmon.exe"))
		}
		s.end()
		for _, p := range []struct{ name, exe string }{{"arm64-type-native", ""}, {"arm64-type-x64", "instcheck-x64.exe"}, {"arm64-type-x86", "instcheck-32.exe"}} {
			s := c.begin(p.name)
			if p.exe == "" {
				if msg, ok := typeLayout(klid, l.expect, hkl); !ok {
					s.failf("%s", msg)
				} else {
					s.notef("%s", msg)
				}
			} else if out, err := exec.Command(filepath.Join(c.bundle, p.exe), "-type", klid, l.expect).CombinedOutput(); err != nil {
				s.failf("%s (%v)", strings.TrimSpace(string(out)), err)
			} else {
				s.notef("%s", strings.TrimSpace(string(out)))
			}
			s.end()
		}
	case "arm64-uninstall":
		base, _ := os.ReadFile(baseFile)
		s := c.begin("arm64-uninstall-leaves-nothing")
		c.uninstall(s, l, "arm64-uninstall", string(base))
		s.end()
	default:
		return false
	}
	return true
}
