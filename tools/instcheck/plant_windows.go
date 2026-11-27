package main

// DLL planting check (docs/installer.md §5.2 and §8.4): a copy of the bundle's marker DLL under every DLL
// name of the guest's system folders and every DLL name in the installer programs' own bytes lies next to each
// installer in the administrator's Downloads folder. The marker DLL (32-bit, written by tools/instbundle) writes
// "<its path>.loaded" when it is loaded, so any such file names a DLL the installer loaded from its folder.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/setup"
)

var plantPhases = []string{"plant-prepare", "plant-control", "plant-dblclick", "plant-dblclick-cleanup", "plant-quiet", "plant-toolsetup", "plant-prefix"}

// plantCases are the folders under Downloads\yakbd-plant and the installer each holds.
var plantCases = [][2]string{
	{"control", "instcheck-32.exe"},
	{"dblclick", "kbdxx-setup.exe"},
	{"quiet", "kbdxx-setup.exe"},
	{"toolsetup", "yakbd-setup.exe"},
	{"prefix", "kbdxx-prefix-setup.exe"},
}

// probeName is a DLL name that exists nowhere but in the control folder: the control loads it by bare name, so
// the program's folder is the only place it can come from.
const probeName = "yakbdplantprobe.dll"

func plantRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads", "yakbd-plant")
}

// plantNeeds are the bundle files each phase opens (tools/instbundle plantFiles holds them all).
var plantNeeds = map[string][]string{
	"plant-prepare":          {"plant-marker.dll", "kbdxx-setup.exe", "yakbd-setup.exe", "kbdxx-prefix-setup.exe", "instcheck-32.exe"},
	"plant-control":          {"instcheck-32.exe"},
	"plant-dblclick":         {"kbdxx-setup.exe"},
	"plant-dblclick-cleanup": {"kbdxx-setup.exe"},
	"plant-quiet":            {"kbdxx-setup.exe"},
	"plant-toolsetup":        {},
	"plant-prefix":           {"kbdxx-prefix-setup.exe"},
}

func (c *checker) plant(phase string) bool {
	if !contains(plantPhases, phase) {
		return false
	}
	for _, f := range plantNeeds[phase] {
		if _, err := os.Stat(filepath.Join(c.bundle, f)); err != nil {
			s := c.begin(phase)
			s.failf("bundle file %s: %v", f, err)
			s.end()
			return true
		}
	}
	switch phase {
	case "plant-prepare":
		c.plantPrepare()
	case "plant-control":
		c.plantControl()
	case "plant-dblclick":
		c.plantDoubleClick()
	case "plant-dblclick-cleanup":
		c.plantDoubleClickCleanup()
	case "plant-quiet":
		c.plantQuiet("quiet", "kbdxx-setup.exe", false)
	case "plant-toolsetup":
		c.plantQuiet("toolsetup", "yakbd-setup.exe", false)
	case "plant-prefix":
		c.plantQuiet("prefix", "kbdxx-prefix-setup.exe", true)
	}
	return true
}

// dllNameRE finds DLL names in ASCII text; the UTF-16 text of a program is searched after removing its zero bytes.
var dllNameRE = regexp.MustCompile(`(?i)[a-z0-9_\-.]{1,64}\.dll`)

// plantNames are the names to plant: every *.dll of System32 and SysWOW64, and every DLL name in the installers.
func (c *checker) plantNames() (names []string, fromExe int, err error) {
	set := map[string]bool{}
	for _, d := range []string{sysDir(), wowDir()} {
		if d == "" {
			continue
		}
		m, err := filepath.Glob(filepath.Join(d, "*.dll"))
		if err != nil {
			return nil, 0, err
		}
		for _, p := range m {
			set[strings.ToLower(filepath.Base(p))] = true
		}
	}
	for _, exe := range []string{"kbdxx-setup.exe", "yakbd-setup.exe"} {
		b, err := os.ReadFile(filepath.Join(c.bundle, exe))
		if err != nil {
			return nil, 0, err
		}
		for _, t := range [][]byte{b, bytes.ReplaceAll(b, []byte{0}, nil)} {
			for _, n := range dllNameRE.FindAll(t, -1) {
				k := strings.ToLower(strings.TrimLeft(string(n), "."))
				if !set[k] && k != ".dll" && k != "" {
					set[k] = true
					fromExe++
				}
			}
		}
	}
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, fromExe, nil
}

// plantPrepare (PowerShell Direct, elevated): the folders, the planted copies, and the consent prompt of an
// administrator switched off (ConsentPromptBehaviorAdmin 0: elevate without prompting), so that the double-click's
// elevated copy starts without a person at the prompt.
func (c *checker) plantPrepare() {
	s := c.begin("plant-prepare")
	marker, err := os.ReadFile(filepath.Join(c.bundle, "plant-marker.dll"))
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	names, fromExe, err := c.plantNames()
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	_ = os.WriteFile(filepath.Join(c.res, "plant-names.txt"), []byte(strings.Join(names, "\n")+"\n"), 0o644)
	root := plantRoot()
	_ = os.RemoveAll(root)
	for _, pc := range plantCases {
		dir := filepath.Join(root, pc[0])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			s.failf("%v", err)
			break
		}
		b, err := os.ReadFile(filepath.Join(c.bundle, pc[1]))
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, pc[1]), b, 0o755)
		}
		list := names
		if pc[0] == "control" {
			list = append([]string{probeName}, names...)
		}
		for _, n := range list {
			if err != nil {
				break
			}
			err = os.WriteFile(filepath.Join(dir, n), marker, 0o644)
		}
		if err != nil {
			s.failf("%s: %v", pc[0], err)
		}
	}
	out, err := exec.Command("reg", "add", `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, "/v", "ConsentPromptBehaviorAdmin", "/t", "REG_DWORD", "/d", "0", "/f").CombinedOutput()
	if err != nil {
		s.failf("ConsentPromptBehaviorAdmin: %v %s", err, out)
	}
	// Give an antivirus scan time, then count again: every planted file must still be there.
	time.Sleep(30 * time.Second)
	for _, pc := range plantCases {
		if n := countPlanted(filepath.Join(root, pc[0])); n < len(names) {
			s.failf("%s: %d of %d planted DLLs left (removed by an antivirus?)", pc[0], n, len(names))
		}
	}
	s.notef("%d DLL names planted per folder (%d from the installers' bytes only), in %s", len(names), fromExe, root)
	s.end()
}

func countPlanted(dir string) int {
	m, _ := filepath.Glob(filepath.Join(dir, "*.dll"))
	return len(m)
}

// markers lists the planted DLLs that were loaded, and removes their marker files.
func markers(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "*.loaded"))
	var names []string
	for _, p := range m {
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".loaded"))
		_ = os.Remove(p)
	}
	sort.Strings(names)
	return names
}

// plantControl: the check can see a load. The bundle's own 32-bit checker (C:\yk\instcheck-32.exe, outside the
// planted folders) loads the control folder's probe by its full path: the marker must appear, so the marker DLL
// runs and writes next to itself. That a bare name is searched for in the program's folder is shown by the
// installer from before the fix (plant-prefix). Informational: a copy of the checker started inside the control
// folder loads the probe and wtsapi32.dll by bare name; its exit code and output are in the note.
//
// Run 1 (tool 3ecd712) had only that copy and no marker at all: the copy did not reach its loads, while the same
// marker DLL ran in the pre-fix installer, so the control now loads by full path from outside the planted folder.
func (c *checker) plantControl() {
	s := c.begin("plant-control-marker-dll-loads")
	dir := filepath.Join(plantRoot(), "control")
	markers(dir)
	helper := filepath.Join(c.bundle, "instcheck-32.exe")
	out, err := exec.Command(helper, "-loadbare", filepath.Join(dir, probeName)).CombinedOutput()
	fmt.Fprintf(c.detail, "  %s -loadbare <control>\\%s: %v %s\n", helper, probeName, err, bytes.TrimSpace(out))
	if got := markers(dir); !contains(got, probeName) {
		s.failf("no marker for %s loaded by full path (%v, %q, markers %v)", probeName, err, bytes.TrimSpace(out), got)
	}
	copyExe := filepath.Join(dir, "instcheck-32.exe")
	var info []string
	for _, n := range []string{probeName, "wtsapi32.dll"} {
		out, err := exec.Command(copyExe, "-loadbare", n).CombinedOutput()
		fmt.Fprintf(c.detail, "  %s -loadbare %s: %v %s\n", copyExe, n, err, bytes.TrimSpace(out))
		info = append(info, fmt.Sprintf("%s: %v %q", n, err, bytes.TrimSpace(out)))
	}
	s.notef("copy in the planted folder, bare names: %s; markers %v", strings.Join(info, "; "), markers(dir))
	s.end()
}

// plantDoubleClick: the administrator double-clicks kbdxx-setup.exe in Downloads (unelevated first process, the
// elevated copy started by it), then it is uninstalled from the same folder; no planted DLL may be loaded.
func (c *checker) plantDoubleClick() {
	s := c.begin("plant-double-click-loads-no-planted-dll")
	if !requireUnelevated(s) {
		s.end()
		return
	}
	dir := filepath.Join(plantRoot(), "dblclick")
	exe := filepath.Join(dir, "kbdxx-setup.exe")
	a, err := c.load("kbdxx-setup.exe", "")
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	markers(dir)
	clearLogs()
	code, final := c.doubleClick(s, exe, "Install the keyboard layout", idYes)
	c.copyLogs("plant-dblclick")
	if code != setup.ExitOK {
		s.failf("exit code %d, want 0 (final message %q)", code, final)
	}
	// Installed and enabled for this user by the first process (input.dll, docs/installer.md §3).
	c.checkInstalled(s, a, true)
	if got := markers(dir); len(got) > 0 {
		s.failf("planted DLLs loaded by the double-click: %s", strings.Join(got, ", "))
	}
	s.end()
}

// plantDoubleClickCleanup (elevated): the double-clicked install is removed from the same folder by /x /quiet.
func (c *checker) plantDoubleClickCleanup() {
	s := c.begin("plant-double-click-cleanup")
	dir := filepath.Join(plantRoot(), "dblclick")
	a, err := c.load("kbdxx-setup.exe", "")
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	if l := c.plantUninstall(s, filepath.Join(dir, "kbdxx-setup.exe"), a.p.Manifest.Product.Code, dir); len(l) > 0 {
		s.failf("planted DLLs loaded by the uninstall: %s", strings.Join(l, ", "))
	}
	s.end()
}

// plantQuiet: an elevated /quiet install and uninstall from the planted folder. With want, planted loads are
// expected (the installer from before the fix, which shows that the check sees the real defect); a load is then a
// note, not a failure.
func (c *checker) plantQuiet(name, file string, want bool) {
	title := "plant-" + name + "-loads-no-planted-dll"
	if want {
		title = "plant-prefix-installer-before-the-fix"
	}
	s := c.begin(title)
	dir := filepath.Join(plantRoot(), name)
	exe := filepath.Join(dir, file)
	code := installer.ToolProductCode()
	if file != "yakbd-setup.exe" {
		a, err := c.load(file, "")
		if err != nil {
			s.failf("%v", err)
			s.end()
			return
		}
		code = a.p.Manifest.Product.Code
	}
	markers(dir)
	got := c.runInstaller(s, exe, "plant-"+name+"-install", "/quiet")
	if got != setup.ExitOK && !want {
		s.failf("install exit code %d, want 0", got)
	}
	loaded := markers(dir)
	us := s
	if want {
		us = &scenario{c: c, name: "prefix-uninstall"} // the old installer's own problems are not this check's
	}
	loaded = append(loaded, c.plantUninstall(us, exe, code, dir)...)
	if want && len(us.probs) > 0 {
		s.notef("uninstall: %s", strings.Join(us.probs, "; "))
	}
	switch {
	case want && len(loaded) > 0:
		s.notef("install exit %d; the check sees the defect: planted DLLs loaded %s", got, strings.Join(loaded, ", "))
	case want:
		s.notef("install exit %d; no planted DLL loaded even before the fix (the bare names were Known DLLs or already loaded)", got)
	case len(loaded) > 0:
		s.failf("planted DLLs loaded: %s", strings.Join(loaded, ", "))
	}
	s.end()
}

// plantUninstall runs exe /x /quiet from the planted folder, checks that the product is gone and returns the
// planted DLLs the uninstall loaded.
func (c *checker) plantUninstall(s *scenario, exe, code, dir string) []string {
	got := c.runInstaller(s, exe, "plant-"+filepath.Base(dir)+"-uninstall", "/x", "/quiet")
	if got != setup.ExitOK && got != setup.ExitReboot {
		s.failf("uninstall exit code %d", got)
	}
	if regString(hkeyLocalMachine, uninstallKey+`\`+code, "DisplayName") != "" {
		s.failf("the uninstall entry is still there")
	}
	return markers(dir)
}

// loadBare loads a DLL by bare name (LoadLibraryW, the standard search order) and reports whether it loaded.
func loadBare(name string) int {
	h, err := syscall.LoadLibrary(name)
	if err != nil {
		fmt.Println("not loaded:", err)
		return 1
	}
	fmt.Printf("loaded %s at %#x\n", name, h)
	return 0
}
