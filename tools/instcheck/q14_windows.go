//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/setup"
)

// Owner additions to the second sign-in bundle (2026-11-26): the display language is never changed by a leg that adds a language,
// and the Q14 measurement (spec OPEN-QUESTIONS Q14): the installer and InstallLayoutOrTip run as LocalSystem for a
// layout whose language no signed-in user has, Windows' locale names for 0438, and what the sign-in screen and a
// new account get afterwards. The Q14 phases measure; the installer's behaviour stays per D97 (1603).

var q14Phases = []string{"si2-baseline", "q14-before", "q14-system", "q14-new-account", "q14-cleanup"}

var (
	pLCIDToLocaleName  = kernel32.NewProc("LCIDToLocaleName")
	pResolveLocaleName = kernel32.NewProc("ResolveLocaleName")
)

// uiState is the effective display language (D98: compare the effect, not whether a value is present): the user's
// first UI language (PreferredUILanguages when set, else the cached machine languages), the UI culture of a new
// process, whether a UI-language override is set, the sign-in screen's first UI language worked out the same way,
// and the first entry of the user's and of each system place's language list.
func uiState() string {
	firstOf := func(h syscall.Handle, path, value string) string {
		for _, v := range regValues(h, path) {
			if strings.EqualFold(v.name, value) {
				if f := strings.Split(strings.TrimRight(v.str, "\x00"), "\x00")[0]; f != "" {
					return f
				}
			}
		}
		return ""
	}
	effective := func(h syscall.Handle, base string) string {
		if f := firstOf(h, base+`Control Panel\Desktop`, "PreferredUILanguages"); f != "" {
			return f
		}
		if f := firstOf(h, base+`Control Panel\Desktop\MuiCached`, "MachinePreferredUILanguages"); f != "" {
			return f
		}
		return firstOf(hkeyLocalMachine, `SYSTEM\CurrentControlSet\Control\MUI\Settings`, "PreferredUILanguages")
	}
	var l []string
	if langs := userLanguages(); len(langs) > 0 {
		l = append(l, "user first language "+langs[0])
	}
	l = append(l, "user display language "+effective(hkeyCurrentUser, ""))
	out, _ := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"[Globalization.CultureInfo]::CurrentUICulture.Name; [bool](Get-WinUILanguageOverride)").Output()
	r := strings.Fields(strings.TrimSpace(string(out)))
	if len(r) == 2 {
		l = append(l, "user UI culture "+r[0], "UI-language override set "+r[1])
	} else {
		l = append(l, "powershell: "+strings.TrimSpace(string(out)))
	}
	l = append(l, "sign-in screen display language "+effective(hkeyUsers, `.DEFAULT\`))
	for _, h := range []string{".DEFAULT", "S-1-5-19", "S-1-5-20"} {
		l = append(l, h+" first language "+firstOf(hkeyUsers, h+`\Control Panel\International\User Profile`, "Languages"))
	}
	return strings.Join(l, "\n")
}

// uiRecord stores the display-language state; uiCheck compares with it.
func (c *checker) uiRecord() {
	_ = os.WriteFile(filepath.Join(c.res, "ui-before.txt"), []byte(uiState()+"\n"), 0o644)
}

func (c *checker) uiCheck(s *scenario, when string) {
	b, err := os.ReadFile(filepath.Join(c.res, "ui-before.txt"))
	if err != nil {
		s.failf("no ui-before.txt")
		return
	}
	now := uiState()
	_ = os.WriteFile(filepath.Join(c.res, "ui-"+when+".txt"), []byte(now+"\n"), 0o644)
	if d := firstDiffAll(strings.TrimSpace(string(b)), now); d != "" {
		s.failf("the display language changed %s: %s", when, strings.ReplaceAll(d, "\n", "; "))
	}
}

func (c *checker) q14(phase string, l siLayouts) {
	switch phase {
	case "q14-before":
		s := c.begin("q14-before")
		if contains(userLanguages(), "fo") {
			s.failf("this user has fo already: the guest is not fresh")
		}
		c.uiRecord()
		s.notef("display language: %s", strings.ReplaceAll(uiState(), "\n", "; "))
		s.end()
	case "q14-system":
		c.q14System(l)
	case "q14-new-account":
		c.q14NewAccount(l)
	case "q14-cleanup":
		c.q14Cleanup(l)
	}
}

// allHives reads every hive under HKU (the four places as places() reads them, plus every other loaded hive).
func allHives() map[string]string {
	m, _ := places()
	for _, h := range loadedHives() {
		if _, ok := m[strings.ToUpper(h)]; ok || strings.HasSuffix(strings.ToLower(h), "_classes") || strings.EqualFold(h, ".DEFAULT") {
			continue
		}
		m[h] = placeDump(h)
	}
	return m
}

func (c *checker) writeAll(tag string, m map[string]string) {
	var names []string
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "== %s\n%s\n", n, m[n])
	}
	_ = os.WriteFile(filepath.Join(c.res, "hives-"+tag+".txt"), []byte(b.String()), 0o644)
}

func diffAll(a, b map[string]string) (string, []string) {
	var names []string
	for n := range b {
		names = append(names, n)
	}
	for n := range a {
		if _, ok := b[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var out strings.Builder
	var changed []string
	for _, n := range names {
		if d := firstDiffAll(a[n], b[n]); d != "" {
			changed = append(changed, n)
			fmt.Fprintf(&out, "== %s\n%s\n", n, d)
		}
	}
	return out.String(), changed
}

func localeName(lcid uint32, flags uint32) string {
	buf := make([]uint16, 85)
	n, _, _ := pLCIDToLocaleName.Call(uintptr(lcid), uintptr(unsafe.Pointer(&buf[0])), 85, uintptr(flags))
	if n == 0 {
		return "(none)"
	}
	return syscall.UTF16ToString(buf)
}

func resolveLocale(name string) string {
	buf := make([]uint16, 85)
	n, _, _ := pResolveLocaleName.Call(uintptr(unsafe.Pointer(u16(name))), uintptr(unsafe.Pointer(&buf[0])), 85)
	if n == 0 {
		return "(none)"
	}
	return syscall.UTF16ToString(buf)
}

// q14-system (a scheduled task as SYSTEM): the installer as LocalSystem, then InstallLayoutOrTip as LocalSystem,
// then Windows' locale names for 0438.
func (c *checker) q14System(l siLayouts) {
	base := c.readDumps("baseline")
	name, _ := currentUser()
	s := c.begin("q14-installer-as-localsystem")
	s.notef("runs as %s", name)
	code := c.runInstaller(s, l.fo.exe, "q14-fo-system", "/quiet", "SIGNIN=1")
	s.notef("exit code %d (D97: %d)", code, setup.ExitEnable)
	if code != setup.ExitEnable {
		s.failf("exit code %d, want %d (D97: no user has the language)", code, setup.ExitEnable)
	}
	loadedCheck(s)
	if installedKLID(l.fo) == "" {
		s.failf("the machine part is not installed")
	}
	m, _ := places()
	if d := diffPlaces(base, m); len(d) > 0 {
		s.failf("the places changed: %s", strings.Join(d, "; "))
	}
	s.end()

	s = c.begin("q14-installlayoutortip-as-localsystem")
	fo := installedKLID(l.fo)
	before := allHives()
	c.writeAll("q14-before-ilot", before)
	str := "0438:" + strings.ToUpper(fo)
	ok := installLayoutOrTip(str, 0)
	s.notef("InstallLayoutOrTip(%q, 0) as %s: %v", str, name, ok)
	after := allHives()
	c.writeAll("q14-after-ilot", after)
	d, changed := diffAll(before, after)
	_ = os.WriteFile(filepath.Join(c.res, "diff-q14-ilot.txt"), []byte(d), 0o644)
	s.notef("hives changed: %s", strings.Join(changed, ","))
	tag := langTag(after[".DEFAULT"], "0438")
	s.notef(".DEFAULT tag for 0438: %q; Languages: %s", tag, firstLine(after[".DEFAULT"], `Control Panel\International\User Profile Languages=`))
	c.setState("q14-default-tag", tag)
	s.end()

	s = c.begin("q14-locale-names")
	n := localeName(0x0438, 0)
	nn, _, _ := strings.Cut(n, "-") // the language part
	s.notef("LCIDToLocaleName(0x0438) %q, its language part %q, ResolveLocaleName(%q) %q, ResolveLocaleName(%q) %q",
		n, nn, n, resolveLocale(n), nn, resolveLocale(nn))
	s.notef("tag Windows filed in .DEFAULT: %q; equal to LCIDToLocaleName: %v, to the language part: %v", tag, strings.EqualFold(tag, n), strings.EqualFold(tag, nn))
	s.end()
}

func firstLine(dump, prefix string) string {
	for _, l := range strings.Split(dump, "\n") {
		if v, ok := strings.CutPrefix(l, prefix); ok {
			return v
		}
	}
	return "(absent)"
}

// q14-new-account: an account made after the LocalSystem leg, at its first sign-in (after a restart).
func (c *checker) q14NewAccount(l siLayouts) {
	s := c.begin("q14-new-account")
	fo := installedKLID(l.fo)
	hkl := layoutHKL(l.fo)
	s.notef("languages %s; hive names the layout %v; Preload %v; loaded %v; in a new process %v",
		strings.Join(userLanguages(), ","), userHas(fo), preloadKLIDs(), hklLoaded(hkl), c.hklInNewProcess(hkl))
	if hklLoaded(hkl) {
		msg, _ := typeLayout(fo, l.fo.expect, hkl)
		s.notef("typing: %s", msg)
	}
	s.end()
}

// q14-cleanup: after the restart, the places as Windows left them, the display language, then the uninstall.
func (c *checker) q14Cleanup(l siLayouts) {
	base := c.readDumps("baseline")
	s := c.begin("q14-after-restart")
	m := allHives()
	c.writeAll("q14-after-restart", m)
	fo := installedKLID(l.fo)
	for _, n := range placeNames {
		s.notef("%s: %d entries name it, tag %q", n, len(refsTo(m[n], fo)), langTag(m[n], "0438"))
	}
	c.uiCheck(s, "after-localsystem")
	s.end()
	s = c.begin("q14-uninstall")
	if code := c.runInstaller(s, c.installedExe(l.fo), "q14-fo-uninstall", "/x", "/quiet"); code != 0 && code != 3010 {
		s.failf("exit code %d", code)
	}
	m2, _ := places()
	c.writeFull("q14-uninstalled", base, m2)
	for _, n := range placeNames {
		if r := refsTo(m2[n], fo); len(r) > 0 {
			s.failf("%s still names it: %v", n, r)
		}
	}
	if d := diffPlaces(base, m2); len(d) > 0 {
		s.notef("differs from the baseline (diff-q14-uninstalled.txt; a language Windows added there is not the installer's): %s", strings.Join(d, "; "))
	}
	c.uiCheck(s, "after-q14-uninstall")
	s.end()
}
