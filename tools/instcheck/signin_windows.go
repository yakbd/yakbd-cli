//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/setup"
	"github.com/yakbd/yakbd-cli/installer/winhost"
)

// First sign-in bundle: the sign-in screen and new users (kbl D87, targets/windows-install.md §7.1 and §9 step 1;
// questions VM-18 to VM-23 of targets/windows-vm-tests.md). Bundle: tools/instbundle -guest w11signin. Phases and
// their order: the bundle's README.txt.
//
// "The four places" are HKU\.DEFAULT, HKU\S-1-5-19, HKU\S-1-5-20 and the Default profile. The checker reads the
// Default profile from a copy of its file, loaded under HKU\YKCHECK-COPY and unloaded at once, so the original is
// never written by a read; only the phases that must change it by hand (VM-22, VM-23) load the file itself, under
// HKU\YKCHECK-EDIT, and unload it before any installer runs.

const (
	copyHive = "YKCHECK-COPY"
	editHive = "YKCHECK-EDIT"
	newUser1 = "yk3" // created after the install (VM-18)
	newUser2 = "yk4" // created after the uninstall (VM-20)
)

var (
	pInstallLayoutOrTipUserReg = syscall.NewLazyDLL(sysDir() + `\input.dll`).NewProc("InstallLayoutOrTipUserReg")
	pRegSetValueExW            = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW           = advapi32.NewProc("RegDeleteValueW")
)

func regKeyExists(root syscall.Handle, path string) bool {
	var k syscall.Handle
	if syscall.RegOpenKeyEx(root, u16(path), 0, keyRead, &k) != nil {
		return false
	}
	syscall.RegCloseKey(k)
	return true
}

const keySetValue = 0x0002

func setDWORD(root syscall.Handle, path, name string, v uint32) {
	var k syscall.Handle
	if syscall.RegOpenKeyEx(root, u16(path), 0, keySetValue, &k) != nil {
		return
	}
	defer syscall.RegCloseKey(k)
	pRegSetValueExW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))), 0, 4, uintptr(unsafe.Pointer(&v)), 4)
}

func deleteValue(root syscall.Handle, path, name string) {
	var k syscall.Handle
	if syscall.RegOpenKeyEx(root, u16(path), 0, keySetValue, &k) != nil {
		return
	}
	defer syscall.RegCloseKey(k)
	pRegDeleteValueW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))))
}

// placeholders are the Substitutes names (d<NNN><LLLL>) a dump maps to the KLID.
func placeholders(dump, klid string) map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(dump, "\n") {
		if x, ok := strings.CutPrefix(l, `Keyboard Layout\Substitutes `); ok {
			if k, v, _ := strings.Cut(x, "="); v == klid {
				m[k] = true
			}
		}
	}
	return m
}

var signInPhases = []string{"si-baseline", "si-install", "si-after-restart", "si-new-account", "si-repair", "si-uninstall",
	"si-after-uninstall", "si-copy"}

// The layouts of the bundle.
type siLayouts struct{ demo, fo, mini *layout }

func (c *checker) signin(phase, res string) bool {
	if !contains(signInPhases, phase) && !contains(signIn2Phases, phase) && !contains(q14Phases, phase) && !contains(vm27Phases, phase) && phase != "si-lock" {
		return false
	}
	var l siLayouts
	var err error
	for _, x := range []struct {
		p           **layout
		exe, expect string
	}{{&l.demo, "demo-setup.exe", "demo.expect.txt"}, {&l.fo, "fo-signin-setup.exe", "fo.expect.txt"}, {&l.mini, "minimal-setup.exe", "minimal.expect.txt"}} {
		if *x.p, err = c.load(x.exe, x.expect); err != nil {
			c.begin(phase).failf("%v", err)
			return true
		}
		watched = append(watched, (*x.p).p.Manifest.Layout.File)
	}
	switch phase {
	case "si-baseline":
		c.siBaseline()
	case "si-install":
		c.siInstall(l)
	case "si-after-restart":
		c.siAfterRestart(l)
	case "si-new-account":
		c.siNewAccount(l, newUser1, true)
	case "si-repair":
		c.siRepair(l)
	case "si-uninstall":
		c.siUninstall(l)
	case "si-after-uninstall":
		c.siNewAccount(l, newUser2, false)
	case "si-copy":
		c.siCopy(l)
	default:
		c.signIn2(phase, l)
	}
	return true
}

// --- the four places ---

// place is one of the four places: its name for the results and the hive name it is read under.
type siPlace struct{ name, hive string }

// withDefault runs f with the Default profile loaded: a copy (edit false) or the file itself (edit true).
func withDefault(edit bool, f func(hive string)) error {
	h := winhost.New()
	file, err := h.DefaultProfileHive()
	if err != nil {
		return err
	}
	name, load := editHive, file
	if !edit {
		name = copyHive
		load = filepath.Join(os.TempDir(), "yk-default-copy.dat")
		removeCopies(load)
		b, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("cannot copy the Default profile (%v): is it still loaded?", err)
		}
		if err := os.WriteFile(load, b, 0o600); err != nil {
			return err
		}
		// The hive's transaction logs go with it (NTUSER.DAT.LOG1, .LOG2), in case the last writes are only there.
		for _, ext := range []string{".LOG1", ".LOG2"} {
			if lb, err := os.ReadFile(file + ext); err == nil {
				_ = os.WriteFile(load+ext, lb, 0o600)
			}
		}
		defer removeCopies(load)
	}
	if err := h.LoadHive(name, load); err != nil {
		return fmt.Errorf("loading %s as HKU\\%s: %v", load, name, err)
	}
	f(name)
	if err := h.UnloadHive(name); err != nil {
		return fmt.Errorf("unloading HKU\\%s: %v", name, err)
	}
	return nil
}

func removeCopies(p string) {
	m, _ := filepath.Glob(p + "*")
	for _, x := range m {
		_ = os.Remove(x)
	}
}

// loadedHives are the names under HKU.
func loadedHives() []string { return subKeys(hkeyUsers, "") }

// yakbdHiveLoaded names a HKU\YAKBD-* key (an installer left the Default profile loaded), "" when none.
func yakbdHiveLoaded() string {
	for _, h := range loadedHives() {
		if strings.HasPrefix(strings.ToUpper(h), "YAKBD-") {
			return h
		}
	}
	return ""
}

// placeDump is what a place holds of the input settings, one line each: Preload, Substitutes, User Profile and
// User Profile System Backup (Languages and every tag's values).
func placeDump(hive string) string {
	var l []string
	for _, sub := range []string{`Keyboard Layout\Preload`, `Keyboard Layout\Substitutes`} {
		for _, v := range regValues(hkeyUsers, hive+`\`+sub) {
			l = append(l, fmt.Sprintf("%s %s=%s", sub, strings.ToLower(v.name), strings.ToLower(v.str)))
		}
	}
	for _, up := range []string{`Control Panel\International\User Profile`, `Control Panel\International\User Profile System Backup`} {
		for _, v := range regValues(hkeyUsers, hive+`\`+up) {
			l = append(l, fmt.Sprintf("%s %s=%s", up, v.name, strings.ReplaceAll(v.str, "\x00", ",")))
		}
		for _, t := range subKeys(hkeyUsers, hive+`\`+up) {
			l = append(l, fmt.Sprintf("%s\\%s", up, t))
			for _, v := range regValues(hkeyUsers, hive+`\`+up+`\`+t) {
				if strings.EqualFold(v.name, "CachedLanguageName") {
					continue
				}
				l = append(l, fmt.Sprintf("%s\\%s %s=%s", up, t, strings.ToUpper(v.name), v.str))
			}
		}
	}
	sort.Strings(l)
	return strings.Join(l, "\n")
}

// places reads the four places: name -> dump. A system hive that is not loaded is "(not loaded)".
func places() (map[string]string, error) {
	m := map[string]string{}
	have := map[string]bool{}
	for _, h := range loadedHives() {
		have[strings.ToUpper(h)] = true
	}
	for _, h := range []string{".DEFAULT", "S-1-5-19", "S-1-5-20"} {
		if have[h] {
			m[h] = placeDump(h)
		} else {
			m[h] = "(not loaded)"
		}
	}
	if h := yakbdHiveLoaded(); h != "" {
		// An installer left the Default profile loaded (loadedCheck reports it): read it there.
		m["Default"] = placeDump(h)
		return m, nil
	}
	err := withDefault(false, func(h string) { m["Default"] = placeDump(h) })
	return m, err
}

var placeNames = []string{".DEFAULT", "S-1-5-19", "S-1-5-20", "Default"}

// refsTo lists the lines of a dump that name the KLID (Preload through Substitutes, Substitutes, User Profile
// values, System Backup values).
func refsTo(dump, klid string) []string {
	var out []string
	klid = strings.ToLower(klid)
	for _, l := range strings.Split(dump, "\n") {
		if strings.Contains(strings.ToLower(l), klid) {
			out = append(out, l)
		}
	}
	return out
}

func (c *checker) writeDumps(tag string, m map[string]string) {
	var b strings.Builder
	for _, n := range placeNames {
		fmt.Fprintf(&b, "== %s\n%s\n", n, m[n])
	}
	_ = os.WriteFile(filepath.Join(c.res, "places-"+tag+".txt"), []byte(b.String()), 0o644)
}

func (c *checker) readDumps(tag string) map[string]string {
	b, err := os.ReadFile(filepath.Join(c.res, "places-"+tag+".txt"))
	if err != nil {
		return nil
	}
	m := map[string]string{}
	cur := ""
	var lines []string
	flush := func() {
		if cur != "" {
			m[cur] = strings.Join(lines, "\n")
		}
	}
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.HasPrefix(l, "== ") {
			flush()
			cur, lines = l[3:], nil
			continue
		}
		lines = append(lines, l)
	}
	flush()
	return m
}

// diffPlaces compares two readings of the four places, one entry per place that differs.
func diffPlaces(a, b map[string]string) []string {
	var d []string
	for _, n := range placeNames {
		if strings.TrimSpace(a[n]) != strings.TrimSpace(b[n]) {
			d = append(d, n+": "+firstDiff(a[n], b[n]))
		}
	}
	return d
}

// checkT8 checks one place for the shape of §7.1 (T8): a Preload entry resolving to the KLID through Substitutes,
// the User Profile value "<LLLL>:<KLID>" = that Preload number under the tag holding the language, and in the
// system hives the same under User Profile System Backup. withLang false: the place has no language of the
// LANGID, so only Preload and Substitutes (VM-19).
func checkT8(s *scenario, name, dump, klid, lang string, withLang bool) {
	klid = strings.ToLower(klid)
	sub := map[string]string{}
	pre := map[string]string{}
	for _, l := range strings.Split(dump, "\n") {
		if r, ok := strings.CutPrefix(l, `Keyboard Layout\Substitutes `); ok {
			k, v, _ := strings.Cut(r, "=")
			sub[k] = v
		}
		if r, ok := strings.CutPrefix(l, `Keyboard Layout\Preload `); ok {
			k, v, _ := strings.Cut(r, "=")
			pre[k] = v
		}
	}
	n := ""
	for k, v := range pre {
		if v == klid || sub[v] == klid {
			n = k
		}
	}
	if n == "" {
		s.failf("%s: no Preload entry for %s", name, klid)
		return
	}
	str := strings.ToUpper(lang + ":" + klid)
	has := func(prefix string) bool {
		for _, l := range strings.Split(dump, "\n") {
			if strings.HasPrefix(l, prefix) && strings.HasSuffix(l, " "+str+"="+n) {
				return true
			}
		}
		return false
	}
	up, bk := `Control Panel\International\User Profile\`, `Control Panel\International\User Profile System Backup\`
	switch {
	case !withLang:
		if len(refsTo(dump, klid)) > 2 {
			s.failf("%s: more than Preload and Substitutes name %s: %v", name, klid, refsTo(dump, klid))
		}
	case !has(up):
		s.failf("%s: no User Profile value %s=%s", name, str, n)
	case name != "Default" && !has(bk):
		s.failf("%s: no User Profile System Backup value %s=%s", name, str, n)
	case name == "Default" && has(bk):
		s.failf("Default: a System Backup value (T8: only the system hives have it)")
	}
}

// arpValue reads a value of a product's uninstall entry.
func arpValue(l *layout, name string) string {
	return regString(hkeyLocalMachine, uninstallKey+`\`+l.p.Manifest.Product.Code, name)
}

func installedKLID(l *layout) string { return strings.ToLower(arpValue(l, setup.RecKLID)) }

func layoutHKL(l *layout) uint32 {
	klid := installedKLID(l)
	id := hex(regString(hkeyLocalMachine, layoutsKey+`\`+klid, "Layout Id"))
	return uint32(hex(l.p.Manifest.Layout.LangID)) | uint32(0xF000|id&0xFFF)<<16
}

// loadedCheck fails the scenario when an installer left the Default profile loaded.
func loadedCheck(s *scenario) {
	if h := yakbdHiveLoaded(); h != "" {
		s.failf("HKU\\%s is still loaded after the installer ended", h)
	}
}

// --- phases ---

// si-baseline (PowerShell Direct): the four places before anything runs.
func (c *checker) siBaseline() {
	s := c.begin("si-baseline")
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	c.writeDumps("baseline", m)
	for _, n := range placeNames {
		s.notef("%s: %d Preload entries, 0409 under %q", n, strings.Count(m[n], `Keyboard Layout\Preload `), langTag(m[n], "0409"))
	}
	for _, n := range []string{"S-1-5-19", "S-1-5-20"} {
		s.notef("%s %s", n, map[bool]string{true: "not loaded", false: "loaded"}[m[n] == "(not loaded)"])
	}
	for _, n := range placeNames {
		if strings.Contains(m[n], "a0") {
			s.notef("%s names a custom KLID already", n)
		}
	}
	s.end()
}

// si-install: VM-18 (SIGNIN=1), VM-19 (a 0438 layout whose file asks for it), the second run, and VM-23's
// preparation (a third layout written into .DEFAULT and the Default profile only, by the test).
func (c *checker) siInstall(l siLayouts) {
	base := c.readDumps("baseline")
	if base == nil {
		c.begin("si-install").failf("no baseline (run si-baseline first)")
		return
	}
	s := c.begin("vm18-install-signin")
	if code := c.runInstaller(s, l.demo.exe, "si-demo-install", "/quiet", "ENABLE=0", "SIGNIN=1"); code != 0 {
		s.failf("exit code %d", code)
	}
	loadedCheck(s)
	klid := installedKLID(l.demo)
	if klid == "" {
		s.failf("not installed")
		s.end()
		return
	}
	if v := arpValue(l.demo, setup.RecSignIn); v != "1" {
		s.failf("%s = %q", setup.RecSignIn, v)
	}
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	for _, n := range placeNames {
		if m[n] == "(not loaded)" {
			s.notef("%s not loaded", n)
			continue
		}
		checkT8(s, n, m[n], klid, "0409", langTag(base[n], "0409") != "")
	}
	// Exactly the entries for the KLID: nothing else changed.
	for _, n := range placeNames {
		var extra []string
		ph := placeholders(m[n], klid)
		for _, line := range strings.Split(firstDiffAll(base[n], m[n]), "\n") {
			if line != "" && !strings.Contains(strings.ToLower(line), klid) && !ph[strings.ToLower(line[strings.LastIndexByte(line, '=')+1:])] &&
				!strings.Contains(line, `User Profile System Backup\`) && !strings.HasSuffix(line, `User Profile System Backup`) {
				extra = append(extra, line)
			}
		}
		if len(extra) > 0 {
			s.failf("%s: other changes: %s", n, strings.Join(extra, ", "))
		}
	}
	s.notef("KLID %s, HKL %08x", klid, layoutHKL(l.demo))
	if userHas(klid) {
		s.notef("enabled for this user too (rule enableForUser)")
	}
	s.end()

	s = c.begin("vm18-second-run-adds-nothing")
	if code := c.runInstaller(s, l.demo.exe, "si-demo-again", "/quiet", "ENABLE=0", "SIGNIN=1"); code != 0 {
		s.failf("exit code %d", code)
	}
	m2, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	if d := diffPlaces(m, m2); len(d) > 0 {
		s.failf("changed: %s", strings.Join(d, "; "))
	}
	s.end()

	s = c.begin("vm19-install-0438-file-field")
	if code := c.runInstaller(s, l.fo.exe, "si-fo-install", "/quiet"); code != 0 {
		s.failf("exit code %d", code)
	}
	loadedCheck(s)
	fo := installedKLID(l.fo)
	if v := arpValue(l.fo, setup.RecSignIn); v != "1" {
		s.failf("%s = %q (the file field asks for it)", setup.RecSignIn, v)
	}
	m3, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	for _, n := range placeNames {
		if m3[n] == "(not loaded)" {
			continue
		}
		withLang := strings.Contains(m3[n], `User Profile\`) && strings.Contains(m3[n], " 0438:")
		checkT8(s, n, m3[n], fo, "0438", withLang)
		s.notef("%s: %s", n, map[bool]string{true: "with its language", false: "Preload and Substitutes only"}[withLang])
	}
	if b, _ := os.ReadFile(filepath.Join(c.logs, "si-fo-install.log")); !strings.Contains(string(b), "added without its language") {
		s.notef("the log does not say \"added without its language\"")
	}
	s.end()

	s = c.begin("vm23-prepare-default-and-defaultprofile-only")
	if code := c.runInstaller(s, l.mini.exe, "si-mini-install", "/quiet", "ENABLE=0"); code != 0 {
		s.failf("exit code %d", code)
	}
	mini := installedKLID(l.mini)
	str := "0409:" + strings.ToUpper(mini)
	writeT8 := func(hive string) {
		r, _, e := pInstallLayoutOrTipUserReg.Call(uintptr(unsafe.Pointer(u16(hive))), 0, 0, uintptr(unsafe.Pointer(u16(str))), 0)
		if r == 0 {
			s.failf("%s: InstallLayoutOrTipUserReg: %v", hive, e)
			return
		}
		d := placeDump(hive)
		idx := ""
		sub := map[string]string{}
		for _, line := range strings.Split(d, "\n") {
			if x, ok := strings.CutPrefix(line, `Keyboard Layout\Substitutes `); ok {
				k, v, _ := strings.Cut(x, "=")
				sub[k] = v
			}
		}
		for _, line := range strings.Split(d, "\n") {
			if x, ok := strings.CutPrefix(line, `Keyboard Layout\Preload `); ok {
				k, v, _ := strings.Cut(x, "=")
				if v == mini || sub[v] == mini {
					idx = k
				}
			}
		}
		up := hive + `\Control Panel\International\User Profile\` + langTag(d, "0409")
		if i, err := strconv.Atoi(idx); err == nil && regKeyExists(hkeyUsers, up) {
			setDWORD(hkeyUsers, up, str, uint32(i))
		} else {
			s.failf("%s: no Preload index or no 0409 language (%q)", hive, idx)
		}
	}
	writeT8(".DEFAULT")
	if err := withDefault(true, writeT8); err != nil {
		s.failf("Default profile: %v", err)
	}
	s.notef("minimal %s written into .DEFAULT and the Default profile only", mini)
	m4, _ := places()
	c.writeDumps("installed", m4)
	c.writeNames(l)
	s.end()
}

// writeNames records which name the sign-in screen shows for which layout (README: screenshots).
func (c *checker) writeNames(l siLayouts) {
	var b strings.Builder
	for _, x := range []*layout{l.demo, l.fo, l.mini} {
		fmt.Fprintf(&b, "%s  KLID %s  Layout Text %q\n", filepath.Base(x.exe), installedKLID(x), regString(hkeyLocalMachine, layoutsKey+`\`+installedKLID(x), "Layout Text"))
	}
	b.WriteString("demo: SIGNIN=1 (VM-18); fo: the file field, 0438 (VM-19); minimal: written by the test into .DEFAULT and the Default profile only (VM-23)\n")
	_ = os.WriteFile(filepath.Join(c.res, "layout-names.txt"), []byte(b.String()), 0o644)
}

// si-after-restart: the entries after the restart (VM-18 "survive a restart"; T8 saw Windows rewrite .DEFAULT).
func (c *checker) siAfterRestart(l siLayouts) {
	base := c.readDumps("baseline")
	before := c.readDumps("installed")
	s := c.begin("vm18-entries-survive-restart")
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	for _, n := range placeNames {
		if m[n] == "(not loaded)" {
			continue
		}
		checkT8(s, n, m[n], installedKLID(l.demo), "0409", langTag(base[n], "0409") != "")
	}
	if d := diffPlaces(before, m); len(d) > 0 {
		s.notef("changed by Windows since the install: %s", strings.Join(d, "; "))
	} else {
		s.notef("unchanged since the install")
	}
	s.end()
	s = c.begin("vm23-after-restart")
	mini := installedKLID(l.mini)
	for _, n := range placeNames {
		s.notef("%s: %d entries name minimal", n, len(refsTo(m[n], mini)))
	}
	s.end()
}

// si-new-account: an account created after the install (VM-18; VM-19 and VM-23 recorded) or after the uninstall
// (VM-20), at its first sign-in, as that account.
func (c *checker) siNewAccount(l siLayouts, account string, installed bool) {
	name, _ := currentUser()
	s := c.begin(map[bool]string{true: "vm18-new-account-has-layout", false: "vm20-new-account-after-uninstall"}[installed])
	if !strings.EqualFold(name[strings.LastIndexByte(name, '\\')+1:], account) {
		s.failf("runs as %s, want %s", name, account)
	}
	if len(regValues(hkeyCurrentUser, `Keyboard Layout\Preload`)) == 0 {
		s.failf("the account has no Preload (first sign-in not finished?)")
	}
	if installed {
		klid := installedKLID(l.demo)
		if !userHas(klid) {
			s.failf("the account's hive does not name %s", klid)
		}
		has := false
		for _, k := range preloadKLIDs() {
			has = has || k == klid
		}
		if !has {
			s.failf("Preload does not resolve to %s", klid)
		}
		hkl := layoutHKL(l.demo)
		if !hklLoaded(hkl) {
			s.failf("HKL %08x not in this process's GetKeyboardLayoutList", hkl)
		}
		if !c.hklInNewProcess(hkl) {
			s.failf("HKL %08x not in a new process's GetKeyboardLayoutList", hkl)
		}
		c.typeBoth(s, l.demo, klid, hkl)
		c.setState("demo-hkl", fmt.Sprintf("%08x", hkl))
		s.end()
		for _, x := range []struct {
			name string
			l    *layout
		}{{"vm19-new-account-0438", l.fo}, {"vm23-new-account-minimal", l.mini}} {
			s = c.begin(x.name)
			k, h := installedKLID(x.l), layoutHKL(x.l)
			s.notef("hive names it %v, Preload %v, loaded %v, in a new process %v", userHas(k), contains(preloadKLIDs(), k), hklLoaded(h), c.hklInNewProcess(h))
			var tags []string
			for _, t := range subKeys(hkeyCurrentUser, `Control Panel\International\User Profile`) {
				tags = append(tags, t)
			}
			s.notef("languages %s", strings.Join(tags, ","))
			if hklLoaded(h) {
				if msg, ok := typeLayout(k, x.l.expect, h); ok {
					s.notef("types: %s", msg)
				} else {
					s.notef("typing: %s", msg)
				}
			}
			s.end()
		}
		return
	}
	for _, x := range []*layout{l.demo, l.fo, l.mini} {
		k := strings.ToLower(c.state()["klid-"+filepath.Base(x.exe)])
		if k != "" && userHas(k) {
			s.failf("the new account's hive names %s", k)
		}
		if h := c.state()["hkl-"+filepath.Base(x.exe)]; h != "" && hklLoaded(uint32(hex(h))) {
			s.failf("HKL %s loaded", h)
		}
	}
	s.notef("Preload %v", preloadKLIDs())
	if d := placeDump(".DEFAULT"); d != "" {
		s.notef(".DEFAULT readable; custom KLIDs there: %d", strings.Count(strings.ToLower(d), "=a0"))
	}
	s.end()
}

// si-repair: VM-22.
func (c *checker) siRepair(l siLayouts) {
	base := c.readDumps("baseline")
	klid := installedKLID(l.demo)
	lang := "0409"
	s := c.begin("vm22-repair-restores-removed-entries")
	str := strings.ToUpper(lang + ":" + klid)
	remove := func(hive string) {
		d := placeDump(hive)
		for _, line := range strings.Split(d, "\n") {
			if x, ok := strings.CutPrefix(line, `Keyboard Layout\Substitutes `); ok {
				k, v, _ := strings.Cut(x, "=")
				if v == klid {
					deleteValue(hkeyUsers, hive+`\Keyboard Layout\Substitutes`, k)
					for _, pl := range regValues(hkeyUsers, hive+`\Keyboard Layout\Preload`) {
						if strings.EqualFold(pl.str, k) {
							deleteValue(hkeyUsers, hive+`\Keyboard Layout\Preload`, pl.name)
						}
					}
				}
			}
		}
		for _, up := range []string{`\Control Panel\International\User Profile`, `\Control Panel\International\User Profile System Backup`} {
			for _, t := range subKeys(hkeyUsers, hive+up) {
				deleteValue(hkeyUsers, hive+up+`\`+t, str)
			}
		}
		renumberPreload(hive)
	}
	remove(".DEFAULT")
	if err := withDefault(true, remove); err != nil {
		s.failf("Default profile: %v", err)
	}
	m, _ := places()
	for _, n := range []string{".DEFAULT", "Default"} {
		if len(refsTo(m[n], klid)) > 0 {
			s.failf("%s still names it after the removal by hand: %v", n, refsTo(m[n], klid))
		}
	}
	if code := c.runInstaller(s, l.demo.exe, "si-repair-f", "/f", "/quiet"); code != 0 {
		s.failf("/f: exit code %d", code)
	}
	loadedCheck(s)
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	for _, n := range placeNames {
		if m[n] != "(not loaded)" {
			checkT8(s, n, m[n], klid, lang, langTag(base[n], lang) != "")
		}
	}
	s.end()

	s = c.begin("vm22-repair-signin0-cleans")
	if code := c.runInstaller(s, l.demo.exe, "si-repair-off", "/f", "/quiet", "SIGNIN=0"); code != 0 {
		s.failf("exit code %d", code)
	}
	loadedCheck(s)
	m, _ = places()
	for _, n := range placeNames {
		if r := refsTo(m[n], klid); len(r) > 0 {
			s.failf("%s still names it: %v", n, r)
		}
	}
	if v := arpValue(l.demo, setup.RecSignIn); v != "0" {
		s.failf("%s = %q", setup.RecSignIn, v)
	}
	s.end()

	s = c.begin("vm22-install-again-keeps-state")
	if code := c.runInstaller(s, l.demo.exe, "si-again-kept", "/quiet"); code != 0 {
		s.failf("exit code %d", code)
	}
	m2, _ := places()
	if d := diffPlaces(m, m2); len(d) > 0 {
		s.failf("changed: %s", strings.Join(d, "; "))
	}
	if v := arpValue(l.demo, setup.RecSignIn); v != "0" {
		s.failf("%s = %q", setup.RecSignIn, v)
	}
	// Back on, for the uninstall phase.
	if code := c.runInstaller(s, l.demo.exe, "si-repair-on", "/f", "/quiet", "SIGNIN=1"); code != 0 {
		s.failf("/f SIGNIN=1: exit code %d", code)
	}
	m3, _ := places()
	for _, n := range placeNames {
		if m3[n] != "(not loaded)" {
			checkT8(s, n, m3[n], klid, lang, langTag(base[n], lang) != "")
		}
	}
	s.end()
}

// si-uninstall: VM-20, by /x /quiet and through the uninstall entry's command; the third layout's entries, put there
// by the test, are cleaned too (§9 step 1: always).
func (c *checker) siUninstall(l siLayouts) {
	base := c.readDumps("baseline")
	for _, x := range []*layout{l.demo, l.fo, l.mini} {
		c.setState("klid-"+filepath.Base(x.exe), installedKLID(x), "hkl-"+filepath.Base(x.exe), fmt.Sprintf("%08x", layoutHKL(x)))
	}
	run := []struct {
		name  string
		l     *layout
		entry bool
	}{{"vm20-uninstall-quiet-demo", l.demo, false}, {"vm20-uninstall-entry-command-fo", l.fo, true}, {"vm20-uninstall-quiet-minimal", l.mini, false}}
	for _, r := range run {
		s := c.begin(r.name)
		klid := installedKLID(r.l)
		var code int
		if r.entry {
			code = c.entryUninstall(s, r.l, "si-"+r.name)
		} else {
			code = c.runInstaller(s, c.installedExe(r.l), "si-"+r.name, "/x", "/quiet")
		}
		if code != 0 && code != 3010 {
			s.failf("exit code %d", code)
			if code == setup.ExitRefused && arpValue(r.l, "DisplayName") != "" {
				// Keep the later phases valid: remove it with FORCE=1 (the FAIL line stays).
				fc := c.runInstaller(s, c.installedExe(r.l), "si-"+r.name+"-force", "/x", "/quiet", "FORCE=1")
				s.notef("refused; FORCE=1 gave exit %d", fc)
			}
		}
		loadedCheck(s)
		m, err := places()
		if err != nil {
			s.failf("Default profile: %v", err)
		}
		for _, n := range placeNames {
			if rr := refsTo(m[n], klid); len(rr) > 0 {
				s.failf("%s still names %s: %v", n, klid, rr)
			}
		}
		if arpValue(r.l, "DisplayName") != "" {
			s.failf("the uninstall entry is still there")
		}
		s.end()
	}
	s := c.begin("vm20-four-places-as-before")
	m, _ := places()
	c.writeDumps("uninstalled", m)
	if d := diffPlaces(base, m); len(d) > 0 {
		s.failf("differ from the baseline: %s", strings.Join(d, "; "))
	}
	s.end()
}

// si-copy: after the restart, the places still clean (VM-20); VM-21 (a plain install leaves the places alone;
// Windows' own copy after a plain install, then uninstall cleans); VM-19's follow-up (what Windows' copy writes for
// a 0438 layout, informative).
func (c *checker) siCopy(l siLayouts) {
	base := c.readDumps("baseline")
	s := c.begin("vm20-places-clean-after-restart")
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	if d := diffPlaces(base, m); len(d) > 0 {
		s.failf("differ from the baseline: %s", strings.Join(d, "; "))
	}
	s.end()

	s = c.begin("vm21-plain-install-uninstall-leaves-places")
	if code := c.runInstaller(s, l.demo.exe, "si-plain-install", "/quiet", "ENABLE=1"); code != 0 {
		s.failf("install: exit code %d", code)
	}
	m1, _ := places()
	if d := diffPlaces(m, m1); len(d) > 0 {
		s.failf("the install changed: %s", strings.Join(d, "; "))
	}
	if code := c.runInstaller(s, c.installedExe(l.demo), "si-plain-uninstall", "/x", "/quiet"); code != 0 && code != 3010 {
		s.failf("uninstall: exit code %d", code)
	}
	m2, _ := places()
	if d := diffPlaces(m, m2); len(d) > 0 {
		s.failf("the uninstall changed: %s", strings.Join(d, "; "))
	}
	s.end()

	s = c.begin("vm21-windows-copy-then-uninstall-cleans")
	if code := c.runInstaller(s, l.demo.exe, "si-copy-install", "/quiet", "ENABLE=1"); code != 0 {
		s.failf("install: exit code %d", code)
	}
	klid := installedKLID(l.demo)
	copyToSystem(s, c)
	after := snapPlaces(s)
	for _, n := range placeNames {
		s.notef("%s: %d entries name it after the copy", n, len(refsTo(after[n], klid)))
	}
	if len(refsTo(after[".DEFAULT"], klid)) == 0 {
		s.failf("Windows' copy did not put the layout into .DEFAULT (the test proves nothing)")
	}
	if code := c.runInstaller(s, c.installedExe(l.demo), "si-copy-uninstall", "/x", "/quiet"); code != 0 && code != 3010 {
		s.failf("uninstall: exit code %d", code)
	}
	m3, _ := places()
	for _, n := range placeNames {
		if r := refsTo(m3[n], klid); len(r) > 0 {
			s.failf("%s still names it: %v", n, r)
		}
	}
	c.writeDumps("after-copy-uninstall", m3)
	s.end()

	s = c.begin("vm19-windows-copy-0438")
	if code := c.runInstaller(s, l.fo.exe, "si-fo-enable", "/quiet", "ENABLE=1", "SIGNIN=0"); code != 0 {
		s.failf("install: exit code %d", code)
	}
	fo := installedKLID(l.fo)
	var tags []string
	for _, t := range subKeys(hkeyCurrentUser, `Control Panel\International\User Profile`) {
		tags = append(tags, t)
	}
	s.notef("this user's languages after enabling: %s", strings.Join(tags, ","))
	b4 := snapPlaces(s)
	copyToSystem(s, c)
	af := snapPlaces(s)
	c.writeDumps("vm19-before-copy", b4)
	c.writeDumps("vm19-after-copy", af)
	for _, n := range placeNames {
		s.notef("%s: %s", n, firstDiff(b4[n], af[n]))
	}
	if code := c.runInstaller(s, c.installedExe(l.fo), "si-fo-uninstall", "/x", "/quiet"); code != 0 && code != 3010 {
		s.failf("uninstall: exit code %d", code)
	}
	m4, _ := places()
	for _, n := range placeNames {
		if r := refsTo(m4[n], fo); len(r) > 0 {
			s.failf("%s still names it: %v", n, r)
		}
	}
	c.writeDumps("end", m4)
	s.end()
}

func snapPlaces(s *scenario) map[string]string {
	m, err := places()
	if err != nil {
		s.failf("Default profile: %v", err)
	}
	return m
}

// copyToSystem runs Windows' own copy (inst §4.4, §4.7: Windows 11, administrator).
func copyToSystem(s *scenario, c *checker) {
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Copy-UserInternationalSettingsToSystem -WelcomeScreen $true -NewUser $true").CombinedOutput()
	fmt.Fprintf(c.detail, "  Copy-UserInternationalSettingsToSystem: %v %s\n", err, strings.TrimSpace(string(out)))
	if err != nil {
		s.failf("Copy-UserInternationalSettingsToSystem: %v %s", err, strings.TrimSpace(string(out)))
	}
}

// firstDiffAll is every line that differs between two dumps ("+" in b only, "-" in a only).
func firstDiffAll(a, b string) string {
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
	sort.Strings(d)
	return strings.Join(d, "\n")
}

// langTag is the User Profile tag of a dump that holds a value of the LANGID, "" when none.
func langTag(dump, lang string) string {
	p := `Control Panel\International\User Profile\`
	for _, l := range strings.Split(dump, "\n") {
		if r, ok := strings.CutPrefix(l, p); ok {
			if t, v, ok := strings.Cut(r, " "); ok && strings.HasPrefix(strings.ToUpper(v), strings.ToUpper(lang)+":") {
				return t
			}
		}
	}
	return ""
}

// renumberPreload rewrites a hive's Preload as 1..n after an entry was removed by hand.
func renumberPreload(hive string) {
	k := hive + `\Keyboard Layout\Preload`
	vs := regValues(hkeyUsers, k)
	sort.Slice(vs, func(i, j int) bool {
		a, _ := strconv.Atoi(vs[i].name)
		b, _ := strconv.Atoi(vs[j].name)
		return a < b
	})
	for _, v := range vs {
		deleteValue(hkeyUsers, k, v.name)
	}
	for i, v := range vs {
		setString(hkeyUsers, k, strconv.Itoa(i+1), v.str)
	}
}

func setString(root syscall.Handle, path, name, v string) {
	var k syscall.Handle
	if syscall.RegOpenKeyEx(root, u16(path), 0, keySetValue, &k) != nil {
		return
	}
	defer syscall.RegCloseKey(k)
	d, _ := syscall.UTF16FromString(v)
	pRegSetValueExW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))), 0, 1, uintptr(unsafe.Pointer(&d[0])), uintptr(2*len(d)))
}
