//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/setup"
)

// multi installs three layouts of one language (LANGID 0409), each enabled, and checks that all three are in the
// user's list at once, have their own KLID and Layout Id, and type; then uninstalls them and checks that nothing is
// left. (The ARM64 bundle's run saw only the first of several same-language layouts stay in the list.)
func (c *checker) multi() {
	names := [][2]string{{"demo-setup.exe", "demo.expect.txt"}, {"kbdtst-setup.exe", "kbdtst.expect.txt"}, {"minimal-setup.exe", "minimal.expect.txt"}}
	var ls []*layout
	for _, n := range names {
		l, err := c.load(n[0], n[1])
		if err != nil {
			s := c.begin("multi-layouts-one-language")
			s.failf("%v", err)
			s.end()
			return
		}
		ls = append(ls, l)
		watched = append(watched, l.p.Manifest.Layout.File)
	}
	base := snapshot()
	s := c.begin("multi-layouts-one-language")
	klids := map[string]bool{}
	ids := map[uint16]bool{}
	type inst struct {
		klid string
		id   uint16
	}
	var got []inst
	// Back to back, as quickly as the installers run: the ARM64 bundle's sign-in run saw only the first of several quick
	// InstallLayoutOrTip calls reach the list; each installer reads User Profile back and calls again.
	for i, l := range ls {
		if code := c.runInstaller(s, l.exe, fmt.Sprintf("11-multi-%d", i), "/quiet", "/enable"); code != setup.ExitOK {
			s.failf("%s: exit code %d", filepath.Base(l.exe), code)
		}
	}
	for _, l := range ls {
		k, id := c.checkInstalled(s, l, true)
		got = append(got, inst{k, id})
		if k != "" && (klids[k] || ids[id]) {
			s.failf("%s: KLID %s or Layout Id %04x used twice", filepath.Base(l.exe), k, id)
		}
		klids[k], ids[id] = true, true
	}
	// All of them, after the last install.
	var listed []string
	for i, l := range ls {
		k := got[i].klid
		if k == "" {
			continue
		}
		if !userHas(k) {
			s.failf("%s (%s) is no longer in the user's list after the others were enabled", filepath.Base(l.exe), k)
		}
		hkl := uint32(hex(l.p.Manifest.Layout.LangID)) | uint32(0xF000|got[i].id)<<16
		if hklLoaded(hkl) {
			listed = append(listed, fmt.Sprintf("%08x", hkl))
		}
		if msg, ok := typeLayout(k, l.expect, hkl); !ok {
			s.failf("%s 64-bit typing: %s", k, msg)
		}
		if out, err := exec.Command(filepath.Join(c.bundle, "instcheck-32.exe"), "-type", k, l.expect).CombinedOutput(); err != nil {
			s.failf("%s 32-bit typing: %s (%v)", k, strings.TrimSpace(string(out)), err)
		}
	}
	s.notef("KLIDs %v; HKLs loaded in this process: %v", got, listed)
	for _, sub := range []string{`Keyboard Layout\Preload`, `Keyboard Layout\Substitutes`} {
		for _, v := range regValues(hkeyCurrentUser, sub) {
			fmt.Fprintf(c.detail, "  hkcu %s %s=%s\n", sub, v.name, v.str)
		}
	}
	pending := ""
	anyReboot := false
	for i, l := range ls {
		exe := c.installedExe(l)
		if exe == "" {
			s.failf("%s: no uninstall entry", filepath.Base(l.exe))
			continue
		}
		code := c.runInstaller(s, exe, fmt.Sprintf("11-multi-%d-uninstall", i), "/uninstall", "/quiet")
		if code != 0 && code != 3010 {
			s.failf("%s uninstall: exit code %d", filepath.Base(l.exe), code)
		}
		anyReboot = anyReboot || code == 3010
		c.checkPendingPaths(s, l.p.Manifest.Layout.File)
	}
	after := snapshot()
	if anyReboot {
		pending = strings.ToLower(strings.Join(pendingRenames(), "|"))
		after = withoutPending(after, pending)
	}
	if after != base {
		s.failf("left behind: %s", firstDiff(base, after))
	}
	s.end()
}
