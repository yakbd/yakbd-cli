//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/setup"
)

// VM-27 (kbl D98, spec ea4c4de): the installer as LocalSystem for the 0438 layout, no user having its language. It
// lets InstallLayoutOrTip file the language in .DEFAULT, reads Windows' tag back and writes the D97 shape in all four
// places; kept after a restart, listed on the sign-in screen, given to a new account; uninstall restores the
// baseline, .DEFAULT's Languages included. Display language snapshots after each step. Bundle: -guest w11signin3.

var vm27Phases = []string{"si2-baseline", "vm27-before", "vm27-system", "vm27-after-restart", "vm27-new-account", "vm27-uninstall"}

func (c *checker) vm27(phase string, l siLayouts) bool {
	switch phase {
	case "vm27-before":
		s := c.begin("vm27-before")
		if contains(userLanguages(), "fo") {
			s.failf("this user has fo already: the guest is not fresh")
		}
		c.uiRecord()
		s.notef("%s", strings.ReplaceAll(uiState(), "\n", "; "))
		s.end()
	case "vm27-system":
		c.vm27System(l)
	case "vm27-after-restart":
		c.vm27AfterRestart(l)
	case "vm27-new-account":
		c.vm27NewAccount(l)
	case "vm27-uninstall":
		c.vm27Uninstall(l)
	default:
		return false
	}
	return true
}

// vm27-system (a scheduled task as SYSTEM).
func (c *checker) vm27System(l siLayouts) {
	base := c.readDumps("baseline")
	name, _ := currentUser()
	s := c.begin("vm27-installer-as-localsystem-full-shape")
	s.notef("runs as %s", name)
	if code := c.runInstaller(s, l.fo.exe, "vm27-fo-system", "/quiet", "SIGNIN=1"); code != 0 {
		s.failf("exit code %d, want 0 (D98)", code)
	}
	loadedCheck(s)
	fo := installedKLID(l.fo)
	if fo == "" {
		s.failf("not installed")
		s.end()
		return
	}
	if v := arpValue(l.fo, setup.RecSignIn); v != "1" {
		s.failf("%s = %q", setup.RecSignIn, v)
	}
	tags := strings.ReplaceAll(arpValue(l.fo, setup.RecSignInTags), "\x00", ",")
	s.notef("%s = %q", setup.RecSignInTags, tags)
	if !strings.Contains(","+tags+",", ",.DEFAULT=fo,") {
		s.failf("the record does not name .DEFAULT=fo")
	}
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	for _, n := range placeNames {
		if m[n] != "(not loaded)" {
			checkCopyShape(s, n, m[n], fo, "0438", "fo")
		}
	}
	c.writeFull("vm27-installed", base, m)
	if b, _ := os.ReadFile(filepath.Join(c.logs, "vm27-fo-system.log")); !strings.Contains(string(b), "filed LANGID 0438 as fo") {
		s.notef("the log does not show the LocalSystem route")
	}
	s.end()
}

func (c *checker) vm27AfterRestart(l siLayouts) {
	base := c.readDumps("baseline")
	before := c.readDumps("vm27-installed")
	s := c.begin("vm27-shape-kept-after-restart")
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	c.writeFull("vm27-after-restart", base, m)
	fo := installedKLID(l.fo)
	for _, n := range placeNames {
		if m[n] != "(not loaded)" {
			checkCopyShape(s, n, m[n], fo, "0438", "fo")
		}
	}
	if d := diffPlaces(before, m); len(d) > 0 {
		s.notef("Windows changed (diff-vm27-after-restart.txt): %s", strings.Join(d, "; "))
	}
	c.uiCheck(s, "vm27-after-restart")
	c.writeNames(l)
	s.end()
}

func (c *checker) vm27NewAccount(l siLayouts) {
	name, _ := currentUser()
	s := c.begin("vm27-new-account-gets-fo-and-types")
	if !strings.EqualFold(name[strings.LastIndexByte(name, '\\')+1:], newUser1) {
		s.failf("runs as %s, want %s", name, newUser1)
	}
	fo := installedKLID(l.fo)
	if !contains(userLanguages(), "fo") {
		s.failf("fo not in the account's languages %v", userLanguages())
	}
	if !userHas(fo) {
		s.failf("the account's hive does not name %s", fo)
	}
	hkl := layoutHKL(l.fo)
	if !hklLoaded(hkl) {
		s.failf("HKL %08x not in this process's GetKeyboardLayoutList", hkl)
	}
	if !c.hklInNewProcess(hkl) {
		s.failf("HKL %08x not in a new process's GetKeyboardLayoutList", hkl)
	}
	c.typeBoth(s, l.fo, fo, hkl)
	_ = os.WriteFile(filepath.Join(c.res, "ui-vm27-new-account.txt"), []byte(uiState()+"\n"), 0o644)
	s.notef("languages %s; %s", strings.Join(userLanguages(), ","), strings.ReplaceAll(uiState(), "\n", "; "))
	s.end()
}

func (c *checker) vm27Uninstall(l siLayouts) {
	base := c.readDumps("baseline")
	s := c.begin("vm27-snapshot-after-new-account")
	m0, _ := places()
	c.writeFull("vm27-after-new-account", base, m0)
	c.uiCheck(s, "vm27-after-new-account")
	s.end()
	s = c.begin("vm27-uninstall-back-to-baseline")
	if code := c.runInstaller(s, c.installedExe(l.fo), "vm27-fo-uninstall", "/x", "/quiet"); code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	loadedCheck(s)
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	c.writeFull("vm27-uninstalled", base, m)
	if d := diffPlaces(base, m); len(d) > 0 {
		s.failf("differ from the baseline (diff-vm27-uninstalled.txt): %s", strings.Join(d, "; "))
	}
	c.uiCheck(s, "vm27-after-uninstall")
	s.end()
}
