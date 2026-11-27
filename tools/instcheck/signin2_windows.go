//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/setup"
)

// Second sign-in bundle (kbl D97, spec b893f2e): the 0438 layout, whose language the four places do not have. With
// enabling on, the elevated process (the user's own account) enables first, so the user's hive gives the tag (fo),
// and the installer writes Windows' copy shape; with enabling off no user has the language and the option fails
// (1603, machine part kept, nothing in the places). Plus an account made after the uninstall (VM-20) and the VM-23
// layout written by the test with System Backup values. Bundle: tools/instbundle -guest w11signin2.

var signIn2Phases = []string{"si2-baseline", "si2-fail", "si2-install", "si2-after-restart", "si2-new-account", "si2-uninstall",
	"si2-after-uninstall"}

var pLockWorkStation = user32.NewProc("LockWorkStation")

func (c *checker) signIn2(phase string, l siLayouts) {
	switch phase {
	case "si2-baseline":
		c.siBaseline()
	case "si-lock":
		s := c.begin("si-lock")
		if r, _, err := pLockWorkStation.Call(); r == 0 {
			s.failf("LockWorkStation: %v", err)
		}
		s.end()
	case "si2-fail":
		c.si2Fail(l)
	case "si2-install":
		c.si2Install(l)
	case "si2-after-restart":
		c.si2AfterRestart(l)
	case "si2-new-account":
		c.si2NewAccount(l)
	case "si2-uninstall":
		c.si2Uninstall(l)
	case "si2-after-uninstall":
		c.siNewAccount(l, newUser2, false)
	default:
		if !c.vm27(phase, l) {
			c.q14(phase, l)
		}
	}
}

// writeFull writes every line of a reading of the four places and every difference to a baseline, untruncated.
func (c *checker) writeFull(tag string, base, m map[string]string) {
	c.writeDumps(tag, m)
	var b strings.Builder
	for _, n := range placeNames {
		fmt.Fprintf(&b, "== %s\n%s\n", n, firstDiffAll(base[n], m[n]))
	}
	_ = os.WriteFile(filepath.Join(c.res, "diff-"+tag+".txt"), []byte(b.String()), 0o644)
}

// checkCopyShape checks Windows' copy shape for a language the place did not have (D97, VM-19): the tag in
// Languages (and System Backup Languages in the system hives), the subkeys, the value 1 there, Preload 0000<LLLL>
// with Substitutes 0000<LLLL> = the KLID.
func checkCopyShape(s *scenario, name, dump, klid, lang, tag string) {
	str := strings.ToUpper(lang + ":" + klid)
	want := []string{
		`Keyboard Layout\Substitutes 0000` + strings.ToLower(lang) + "=" + klid,
		`Control Panel\International\User Profile\` + tag + " " + str + "=1",
	}
	if name != "Default" {
		want = append(want, `Control Panel\International\User Profile System Backup\`+tag+" "+str+"=1")
	}
	lines := map[string]bool{}
	for _, l := range strings.Split(dump, "\n") {
		lines[l] = true
	}
	for _, w := range want {
		if !lines[w] {
			s.failf("%s: no %q", name, w)
		}
	}
	pre := false
	for l := range lines {
		pre = pre || strings.HasPrefix(l, `Keyboard Layout\Preload `) && strings.HasSuffix(l, "=0000"+strings.ToLower(lang))
	}
	if !pre {
		s.failf("%s: no Preload entry 0000%s", name, strings.ToLower(lang))
	}
	langs := func(key string) bool {
		for l := range lines {
			if v, ok := strings.CutPrefix(l, key+" Languages="); ok {
				for _, t := range strings.Split(v, ",") {
					if strings.EqualFold(t, tag) {
						return true
					}
				}
			}
		}
		return false
	}
	if !langs(`Control Panel\International\User Profile`) {
		s.failf("%s: %s not in User Profile Languages", name, tag)
	}
	if name != "Default" && !langs(`Control Panel\International\User Profile System Backup`) {
		s.failf("%s: %s not in System Backup Languages", name, tag)
	}
}

// userLanguages is this user's User Profile Languages.
func userLanguages() []string {
	for _, v := range regValues(hkeyCurrentUser, `Control Panel\International\User Profile`) {
		if strings.EqualFold(v.name, "Languages") {
			return strings.Split(strings.TrimRight(v.str, "\x00"), "\x00")
		}
	}
	return nil
}

// si2-fail: fo with ENABLE=0 SIGNIN=1 on a guest where no user has its language: 1603, the machine part kept, the
// four places unchanged; then removed again.
func (c *checker) si2Fail(l siLayouts) {
	base := c.readDumps("baseline")
	s := c.begin("d97-missing-language-without-user-fails")
	c.uiRecord()
	if contains(userLanguages(), "fo") {
		s.failf("this user has fo already: the guest is not fresh")
	}
	code := c.runInstaller(s, l.fo.exe, "si2-fo-fail", "/quiet", "ENABLE=0", "SIGNIN=1")
	if code != setup.ExitEnable {
		s.failf("exit code %d, want %d", code, setup.ExitEnable)
	}
	loadedCheck(s)
	if installedKLID(l.fo) == "" || arpValue(l.fo, "DisplayName") == "" {
		s.failf("the machine part is not installed")
	}
	if v := arpValue(l.fo, setup.RecSignIn); v != "0" {
		s.failf("%s = %q, want 0", setup.RecSignIn, v)
	}
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	if d := diffPlaces(base, m); len(d) > 0 {
		s.failf("the places changed: %s", strings.Join(d, "; "))
	}
	if b, _ := os.ReadFile(filepath.Join(c.logs, "si2-fo-fail.log")); !strings.Contains(string(b), "/f SIGNIN=1") {
		s.failf("the log does not name /f SIGNIN=1")
	}
	if code := c.runInstaller(s, c.installedExe(l.fo), "si2-fo-fail-uninstall", "/x", "/quiet"); code != 0 && code != 3010 {
		s.failf("uninstall: exit code %d", code)
	}
	if m2, _ := places(); len(diffPlaces(base, m2)) > 0 {
		s.failf("after the uninstall the places differ: %s", strings.Join(diffPlaces(base, m2), "; "))
	}
	c.uiCheck(s, "after-fail")
	s.end()
}

// si2-install: fo with enabling on (the elevated task is tester's own account: enable first, then the places) and
// the VM-23 layout written by the test into .DEFAULT (with its System Backup value) and the Default profile only.
func (c *checker) si2Install(l siLayouts) {
	base := c.readDumps("baseline")
	s := c.begin("d97-missing-language-copy-shape")
	if code := c.runInstaller(s, l.fo.exe, "si2-fo-install", "/quiet", "ENABLE=1"); code != 0 {
		s.failf("exit code %d", code)
	}
	loadedCheck(s)
	fo := installedKLID(l.fo)
	tag := ""
	for _, t := range subKeys(hkeyCurrentUser, `Control Panel\International\User Profile`) {
		for _, v := range regValues(hkeyCurrentUser, `Control Panel\International\User Profile\`+t) {
			if strings.EqualFold(v.name, "0438:"+fo) {
				tag = t
			}
		}
	}
	if tag == "" {
		s.failf("not in this user's list (enabling first, D97)")
		tag = "fo"
	}
	s.notef("this user's tag %s, languages %s", tag, strings.Join(userLanguages(), ","))
	if v := arpValue(l.fo, setup.RecSignIn); v != "1" {
		s.failf("%s = %q", setup.RecSignIn, v)
	}
	s.notef("%s = %q", setup.RecSignInTags, strings.ReplaceAll(arpValue(l.fo, setup.RecSignInTags), "\x00", ","))
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	for _, n := range placeNames {
		if m[n] == "(not loaded)" {
			s.notef("%s not loaded", n)
			continue
		}
		checkCopyShape(s, n, m[n], fo, "0438", tag)
	}
	c.writeFull("installed", base, m)
	c.uiCheck(s, "after-install")
	s.end()

	s = c.begin("d97-second-run-adds-nothing")
	if code := c.runInstaller(s, l.fo.exe, "si2-fo-again", "/quiet", "ENABLE=1"); code != 0 {
		s.failf("exit code %d", code)
	}
	if m2, _ := places(); len(diffPlaces(m, m2)) > 0 {
		s.failf("changed: %s", strings.Join(diffPlaces(m, m2), "; "))
	}
	s.end()

	s = c.begin("vm23-prepare-with-system-backup")
	if code := c.runInstaller(s, l.mini.exe, "si2-mini-install", "/quiet", "ENABLE=0"); code != 0 {
		s.failf("exit code %d", code)
	}
	mini := installedKLID(l.mini)
	str := "0409:" + strings.ToUpper(mini)
	write := func(hive string, backup bool) {
		r, _, e := pInstallLayoutOrTipUserReg.Call(uintptr(unsafe.Pointer(u16(hive))), 0, 0, uintptr(unsafe.Pointer(u16(str))), 0)
		if r == 0 {
			s.failf("%s: InstallLayoutOrTipUserReg: %v", hive, e)
			return
		}
		d := placeDump(hive)
		t := langTag(d, "0409")
		keys := []string{hive + `\Control Panel\International\User Profile\` + t}
		if backup {
			keys = append(keys, hive+`\Control Panel\International\User Profile System Backup\`+t)
		}
		n := uint32(0)
		for _, v := range regValues(hkeyUsers, keys[0]) {
			if x, err := strconv.Atoi(v.str); err == nil && v.typ == 4 && uint32(x) > n {
				n = uint32(x)
			}
		}
		for _, k := range keys {
			if t == "" || !regKeyExists(hkeyUsers, k) {
				s.failf("%s: no 0409 language key %s", hive, k)
				continue
			}
			setDWORD(hkeyUsers, k, str, n+1)
		}
	}
	write(".DEFAULT", true)
	if err := withDefault(true, func(h string) { write(h, false) }); err != nil {
		s.failf("Default profile: %v", err)
	}
	s.notef("minimal %s written into .DEFAULT (with System Backup) and the Default profile only", mini)
	m3, _ := places()
	c.writeDumps("installed-all", m3)
	c.writeNames(l)
	s.end()
}

// si2-after-restart: the full readback after the restart (every line, untruncated, in places-after-restart.txt and
// diff-after-restart.txt).
func (c *checker) si2AfterRestart(l siLayouts) {
	base := c.readDumps("baseline")
	before := c.readDumps("installed-all")
	s := c.begin("d97-copy-shape-survives-restart")
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	c.writeFull("after-restart", base, m)
	fo := installedKLID(l.fo)
	for _, n := range placeNames {
		if m[n] != "(not loaded)" {
			checkCopyShape(s, n, m[n], fo, "0438", "fo")
		}
	}
	if d := diffPlaces(before, m); len(d) > 0 {
		s.notef("Windows changed since the install, see diff-after-restart.txt: %d place(s)", len(d))
	}
	s.end()
	s = c.begin("vm23-with-system-backup-after-restart")
	mini := installedKLID(l.mini)
	for _, n := range placeNames {
		s.notef("%s: %d entries name minimal", n, len(refsTo(m[n], mini)))
	}
	s.end()
}

// si2-new-account: yk3, created after the install, first sign-in: fo in its language list, loaded, typing.
func (c *checker) si2NewAccount(l siLayouts) {
	name, _ := currentUser()
	s := c.begin("d97-new-account-has-language-and-layout")
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
	s.notef("languages %s, Preload %v", strings.Join(userLanguages(), ","), preloadKLIDs())
	s.end()
	s = c.begin("vm23-new-account-minimal")
	k, h := installedKLID(l.mini), layoutHKL(l.mini)
	s.notef("hive names it %v, loaded %v", userHas(k), hklLoaded(h))
	s.end()
}

// si2-uninstall: fo by /x /quiet, minimal through the uninstall entry's command; the four places equal the baseline,
// Languages included.
func (c *checker) si2Uninstall(l siLayouts) {
	base := c.readDumps("baseline")
	for _, x := range []*layout{l.demo, l.fo, l.mini} {
		if k := installedKLID(x); k != "" {
			c.setState("klid-"+filepath.Base(x.exe), k, "hkl-"+filepath.Base(x.exe), fmt.Sprintf("%08x", layoutHKL(x)))
		}
	}
	s := c.begin("d97-uninstall-fo-quiet")
	if code := c.runInstaller(s, c.installedExe(l.fo), "si2-fo-uninstall", "/x", "/quiet"); code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	loadedCheck(s)
	s.end()
	s = c.begin("d97-uninstall-minimal-entry-command")
	if code := c.entryUninstall(s, l.mini, "si2-mini-uninstall"); code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	loadedCheck(s)
	s.end()
	s = c.begin("d97-four-places-as-before")
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	c.writeFull("uninstalled", base, m)
	if d := diffPlaces(base, m); len(d) > 0 {
		s.failf("differ from the baseline (diff-uninstalled.txt): %s", strings.Join(d, "; "))
	}
	c.uiCheck(s, "after-uninstall")
	s.end()
}
