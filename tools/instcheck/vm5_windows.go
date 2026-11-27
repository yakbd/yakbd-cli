//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// vm5 checks the custom-language route of kbl targets/windows-install.md section 6 (VM-5, D63) as the installer now
// implements it: a layout with LANGID 0C00 is registered with Layout Locale Name and the Custom Language values; the
// installer adds the tag to the user's language list (it was not there), reads the transient LANGID Windows gave it
// and enables "<transient>:<KLID>"; the uninstaller removes the layout and, because it added it, the tag.
func (c *checker) vm5() {
	l, err := c.load("custom-0c00-setup.exe", "custom.expect.txt")
	if err != nil {
		s := c.begin("vm5-custom-language")
		s.failf("%v", err)
		s.end()
		return
	}
	tag := l.p.Manifest.Layout.LocaleName
	watched = append(watched, l.p.Manifest.Layout.File)
	up := `Control Panel\International\User Profile`
	ps := func(script string) {
		out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
		fmt.Fprintf(c.detail, "  powershell %q: %v\n%s\n", script, err, out)
	}
	hasTag := func() bool {
		for _, v := range regValues(hkeyCurrentUser, up) {
			if v.name == "Languages" {
				for _, x := range strings.Split(v.str, "\x00") {
					if strings.EqualFold(x, tag) {
						return true
					}
				}
			}
		}
		return false
	}
	base := snapshot()
	s := c.begin("vm5-install-enables-0c00")
	if hasTag() {
		s.failf("the tag %s is in the user's list before the test", tag)
	}
	if code := c.runInstaller(s, l.exe, "10-custom", "/quiet"); code != 0 {
		s.failf("exit code %d, want 0", code)
	}
	ps(`Get-WinUserLanguageList | Format-List LanguageTag, InputMethodTips`)
	klid := ""
	for _, k := range subKeys(hkeyLocalMachine, layoutsKey) {
		if strings.EqualFold(regString(hkeyLocalMachine, layoutsKey+`\`+k, "Layout Product Code"), l.p.Manifest.Product.Code) {
			klid = strings.ToLower(k)
		}
	}
	if klid == "" || !strings.HasSuffix(klid, "0c00") {
		s.failf("no KLID ...0c00 with the product code (%q)", klid)
		s.end()
		return
	}
	k := layoutsKey + `\` + klid
	file := l.p.Manifest.Layout.File
	for name, want := range map[string]string{
		"Layout Locale Name":           tag,
		"Custom Language Name":         l.p.Manifest.Layout.CustomLanguageName,
		"Custom Language Display Name": `@%SystemRoot%\system32\` + file + `,-1100`,
	} {
		if got := regString(hkeyLocalMachine, k, name); got != want {
			s.failf("%s = %q, want %q", name, got, want)
		}
	}
	if !hasTag() {
		s.failf("the installer did not add %s to the user's list", tag)
	}
	transient := ""
	for _, t := range subKeys(hkeyCurrentUser, up) {
		if strings.EqualFold(t, tag) {
			if v := regString(hkeyCurrentUser, up+`\`+t, "TransientLangId"); v != "" {
				var n uint32
				fmt.Sscan(v, &n)
				transient = fmt.Sprintf("%04X", n)
			}
		}
	}
	if transient == "" {
		s.failf("no TransientLangId for %s", tag)
	}
	if !userHas(klid) {
		s.failf("the KLID is not in the user's list")
	}
	id := uint32(hex(regString(hkeyLocalMachine, k, "Layout Id")))
	hkl := uint32(hex(transient)) | (0xF000|id)<<16
	if hklLoaded(hkl) {
		s.notef("KLID %s, transient LANGID %s, HKL %08x loaded", klid, transient, hkl)
	} else {
		s.notef("KLID %s, transient LANGID %s, HKL %08x not in this process's list", klid, transient, hkl)
	}
	if msg, ok := typeLayout(klid, l.expect, 0); !ok {
		s.failf("typing: %s", msg)
	}
	s.end()

	s = c.begin("vm5-uninstall-removes-layout-and-tag")
	exe := c.installedExe(l)
	if code := c.runInstaller(s, exe, "10-custom-uninstall", "/uninstall", "/quiet"); code != 0 && code != 3010 {
		s.failf("uninstall exit code %d", code)
	}
	ps(`Get-WinUserLanguageList | Format-List LanguageTag, InputMethodTips`)
	if userHas(klid) {
		s.failf("the KLID is still in the user's list")
	}
	if hasTag() {
		s.failf("the tag %s is still in the user's list", tag)
	}
	for _, t := range subKeys(hkeyCurrentUser, up) {
		if strings.EqualFold(t, tag) {
			s.failf("User Profile\\%s is still there", t)
		}
	}
	if regValues(hkeyCurrentUser, `Software\YAKBD\`+l.p.Manifest.Product.Code) != nil {
		s.failf("the installer's tag record is still there")
	}
	after := snapshot()
	if after != base {
		s.failf("left behind: %s", firstDiff(base, after))
	}
	s.end()
}
