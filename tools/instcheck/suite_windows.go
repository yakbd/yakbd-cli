package main

// The two YAKBD installers on a fresh guest (docs/installer-cli.md §12, bundle `instbundle -guest w11suite`):
//
//	suite-silent       (elevated) a subset of the state table of installer/toolsetup: fresh installs of both
//	                   installers, changes between them, the PATH choice, refusals, and the takeover of the earlier
//	                   layout from the published 0.1.0-preview.1 and preview.2 installers (old\)
//	suite-state-N      (elevated) puts the machine into wizard state N
//	suite-wizard-N     (the signed-in administrator, not elevated) opens the wizard of state N as a double-click
//	                   does, reads every control (id, text, shown, enabled, ticked) against the boxes the state
//	                   must show, writes results\wizard-N-ready.txt for the host's screenshot, waits for
//	                   results\wizard-N-shot.txt, then presses Cancel (exit 1602)
//
// The wizard window is found by its class (installer/toolstub WizardClass) and read with window messages
// (WM_GETTEXT, BM_GETCHECK, IsWindowVisible, IsWindowEnabled), as ui_windows.go reads message boxes.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
)

var suitePhases = func() []string {
	out := []string{"suite-silent"}
	for i := 1; i <= 8; i++ {
		out = append(out, fmt.Sprintf("suite-state-%d", i), fmt.Sprintf("suite-wizard-%d", i))
	}
	return out
}()

var (
	pFindWindowW     = user32.NewProc("FindWindowW")
	pIsWindowEnabled = user32.NewProc("IsWindowEnabled")
	pSendMessageW    = user32.NewProc("SendMessageW")
)

const (
	wizardClass = "YakbdSetupWizard"
	bmGetCheck  = 0x00F0
	legacyKey   = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{EBE1EAEC-C574-5473-8489-B3FF22FC6506}`
)

func (c *checker) suiteFile(name string) string {
	return filepath.Join(c.bundle, filepath.FromSlash(name))
}

func suiteKey() string { return uninstallKey + `\` + installer.SuiteProductCode() }

func editorDir() string { return filepath.Join(rootDir(), "Editor") }

func commonPrograms() string {
	return filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`)
}

func (c *checker) suite(phase string) bool {
	switch {
	case phase == "suite-silent":
		c.suiteSilent()
	case strings.HasPrefix(phase, "suite-state-"):
		c.suiteState(strings.TrimPrefix(phase, "suite-state-"))
	case strings.HasPrefix(phase, "suite-wizard-"):
		c.suiteWizard(strings.TrimPrefix(phase, "suite-wizard-"))
	default:
		return false
	}
	return true
}

// machine is what is installed, read from the guest itself.
type machine struct {
	cli, editor, legacy, path, shortcut bool
	version                             string
	components                          string
}

func readMachine() machine {
	var m machine
	_, err := os.Stat(filepath.Join(toolDir(), "yakbd.exe"))
	m.cli = err == nil
	_, err = os.Stat(filepath.Join(editorDir(), "yakbd-gui.exe"))
	m.editor = err == nil
	m.legacy = regString(hkeyLocalMachine, legacyKey, "DisplayName") != ""
	m.path = pathHas(toolDir())
	_, err = os.Stat(filepath.Join(commonPrograms(), "YAKBD Editor.lnk"))
	m.shortcut = err == nil
	m.version = regString(hkeyLocalMachine, suiteKey(), "DisplayVersion")
	m.components = regString(hkeyLocalMachine, suiteKey(), toolsetup.RecComponents)
	return m
}

func (m machine) String() string {
	return fmt.Sprintf("cli=%v editor=%v legacy=%v path=%v shortcut=%v entry=%q %s", m.cli, m.editor, m.legacy, m.path, m.shortcut, m.components, m.version)
}

// expectMachine fails s unless the machine has exactly these components and PATH state, the earlier layout gone.
func expectMachine(s *scenario, cli, editor, path bool) {
	m := readMachine()
	if m.cli != cli || m.editor != editor || m.path != path || m.shortcut != editor || m.legacy {
		s.failf("machine %s, want cli=%v editor=%v path=%v", m, cli, editor, path)
	}
	want := ""
	switch {
	case cli && editor:
		want = "CLI,Editor"
	case cli:
		want = "CLI"
	}
	if m.components != want {
		s.failf("entry components %q, want %q", m.components, want)
	}
	if !cli && !editor {
		if _, err := os.Stat(rootDir()); err == nil {
			s.failf("%s still exists", rootDir())
		}
		if pathHas(filepath.Join(rootDir(), "yakbd")) {
			s.failf("the earlier PATH entry is left")
		}
	}
	if editor && m.shortcut {
		target, dir, err := readLink(filepath.Join(commonPrograms(), "YAKBD Editor.lnk"))
		if err != nil || !strings.EqualFold(target, filepath.Join(editorDir(), "yakbd-gui.exe")) || !strings.EqualFold(strings.TrimRight(dir, `\`), editorDir()) {
			s.failf("shortcut %q in %q (%v), want %s", target, dir, err, filepath.Join(editorDir(), "yakbd-gui.exe"))
		}
	}
}

// readLink reads a shortcut's target and working folder with the Shell's own object, from 64-bit PowerShell.
func readLink(lnk string) (string, string, error) {
	ps := filepath.Join(sysDir(), `WindowsPowerShell\v1.0\powershell.exe`)
	script := fmt.Sprintf(`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s'); $s.TargetPath; $s.WorkingDirectory`, strings.ReplaceAll(lnk, "'", "''"))
	out, err := exec.Command(ps, "-NoProfile", "-Command", script).Output()
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r", ""), "\n")
	if err != nil || len(lines) < 2 {
		return "", "", fmt.Errorf("powershell: %v %q", err, out)
	}
	return lines[0], lines[1], nil
}

// clean removes every installation: the new entry's (its own installer copy /x) and the earlier layout's.
func (c *checker) wipe(s *scenario, tag string) {
	if regString(hkeyLocalMachine, suiteKey(), "DisplayName") != "" {
		c.runInstaller(s, c.suiteFile("gui/yakbd-gui-setup.exe"), tag+"-x", "/x", "/quiet", "/norestart")
	}
	if regString(hkeyLocalMachine, legacyKey, "DisplayName") != "" {
		c.runInstaller(s, c.suiteFile("old/cli-v0.1.0-preview.2/yakbd-setup.exe"), tag+"-oldx", "/x", "/quiet", "/norestart")
	}
	if m := readMachine(); m.cli || m.editor || m.legacy || m.path {
		s.failf("cleanup left %s", m)
	}
}

// want fails s when an installer's exit code is not code, with the failure lines of its last log.
func (c *checker) want(s *scenario, code, got int, what string) {
	if got != code {
		s.failf("%s: exit %d, want %d%s", what, got, code, c.lastLogProblem())
	}
}

// lastLogProblem is the refusal or failure line of the newest installer log, for a FAIL line that explains itself.
func (c *checker) lastLogProblem() string {
	ents, _ := os.ReadDir(c.logs)
	var newest string
	var when time.Time
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && fi.ModTime().After(when) {
			newest, when = e.Name(), fi.ModTime()
		}
	}
	b, _ := os.ReadFile(filepath.Join(c.logs, newest))
	var found []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "refused") || strings.Contains(l, "failed") || strings.Contains(l, "rolling back") || strings.Contains(l, "damaged") {
			found = append(found, strings.TrimSpace(l))
		}
	}
	if len(found) == 0 {
		return ""
	}
	return " [" + newest + ": " + found[len(found)-1] + "]"
}

func (c *checker) suiteSilent() {
	cli, gui := c.suiteFile("ci/yakbd-cli-setup.exe"), c.suiteFile("gui/yakbd-gui-setup.exe")
	q := []string{"/quiet", "/norestart"}
	run := func(s *scenario, exe, tag string, args ...string) int {
		return c.runInstaller(s, exe, tag, append(append([]string(nil), q...), args...)...)
	}

	s := c.begin("suite-cli-setup-installs-cli-on-path")
	c.want(s, 0, run(s, cli, "s1"), "cli setup")
	expectMachine(s, true, false, true)
	for _, f := range []string{"yakbd.exe", "LICENSE", "NOTICE"} {
		if _, err := os.Stat(filepath.Join(toolDir(), f)); err != nil {
			s.failf("%v", err)
		}
	}
	if regString(hkeyLocalMachine, suiteKey(), "DisplayName") != "YAKBD" {
		s.failf("Installed apps entry %q, want YAKBD", regString(hkeyLocalMachine, suiteKey(), "DisplayName"))
	}
	c.want(s, 0, run(s, cli, "s1x", "/x"), "/x")
	expectMachine(s, false, false, false)
	s.end()

	s = c.begin("suite-gui-setup-installs-both-path-off")
	c.want(s, 0, run(s, gui, "s2"), "gui setup")
	expectMachine(s, true, true, false)
	c.want(s, 0, run(s, gui, "s2x", "/x"), "/x")
	expectMachine(s, false, false, false)
	s.end()

	s = c.begin("suite-gui-setup-cli-only")
	c.want(s, 0, run(s, gui, "s3", "ADDLOCAL=CLI"), "ADDLOCAL=CLI")
	expectMachine(s, true, false, false)
	c.wipe(s, "s3c")
	s.end()

	s = c.begin("suite-gui-over-cli-keeps-path-cli-over-both-keeps-editor")
	c.want(s, 0, run(s, cli, "s4a"), "cli setup")
	c.want(s, 0, run(s, gui, "s4b"), "gui setup over it")
	expectMachine(s, true, true, true)
	c.want(s, 0, run(s, cli, "s4c"), "cli setup over both")
	expectMachine(s, true, true, true)
	inst := filepath.Join(rootDir(), "setup.exe")
	c.want(s, 0, run(s, inst, "s4d", "/i", installer.SuiteProductCode(), "REMOVE=Editor"), "REMOVE=Editor through the entry's setup.exe")
	expectMachine(s, true, false, true)
	c.wipe(s, "s4e")
	s.end()

	s = c.begin("suite-addtopath-on-and-off")
	c.want(s, 0, run(s, gui, "s5a", "ADDTOPATH=1"), "gui ADDTOPATH=1")
	expectMachine(s, true, true, true)
	c.want(s, 0, run(s, cli, "s5b", "ADDTOPATH=0"), "cli ADDTOPATH=0")
	expectMachine(s, true, true, false)
	c.wipe(s, "s5c")
	s.end()

	s = c.begin("suite-refusals-change-nothing")
	c.want(s, 1639, run(s, gui, "s6a", "ADDLOCAL=Editor"), "ADDLOCAL=Editor alone")
	c.want(s, 1639, run(s, cli, "s6b", "ADDLOCAL=CLI,Editor"), "the editor from the CLI installer")
	expectMachine(s, false, false, false)
	c.want(s, 0, run(s, gui, "s6c"), "gui setup")
	c.want(s, 1639, run(s, gui, "s6d", "REMOVE=CLI"), "REMOVE=CLI with the editor")
	expectMachine(s, true, true, false)
	c.wipe(s, "s6e")
	s.end()

	for _, old := range []string{"cli-v0.1.0-preview.1", "cli-v0.1.0-preview.2"} {
		s = c.begin("suite-takeover-from-" + old)
		c.want(s, 0, run(s, c.suiteFile("old/"+old+"/yakbd-setup.exe"), "s7a-"+old), "the published "+old)
		if m := readMachine(); !m.legacy || !pathHas(filepath.Join(rootDir(), "yakbd")) {
			s.failf("the published installer left %s", m)
		}
		c.want(s, 0, run(s, cli, "s7b-"+old), "cli setup over it")
		expectMachine(s, true, false, true)
		c.wipe(s, "s7c-"+old)
		s.end()
	}

	s = c.begin("suite-takeover-from-gui-v0.1.0-preview.1")
	c.want(s, 0, run(s, c.suiteFile("old/gui-v0.1.0-preview.1/yakbd-setup.exe"), "s8a"), "the published editor installer")
	c.want(s, 1603, run(s, cli, "s8b"), "the cli setup refuses to take over an editor")
	if m := readMachine(); !m.legacy || m.cli {
		s.failf("the refusal changed the machine: %s", m)
	}
	c.want(s, 0, run(s, gui, "s8c"), "gui setup over it")
	expectMachine(s, true, true, true)
	c.wipe(s, "s8d")
	s.end()

	s = c.begin("suite-new-x-removes-the-earlier-layout")
	c.want(s, 0, run(s, c.suiteFile("old/cli-v0.1.0-preview.2/yakbd-setup.exe"), "s9a"), "the published preview.2")
	c.want(s, 0, run(s, cli, "s9b", "/x"), "the new /x")
	expectMachine(s, false, false, false)
	s.end()
}

// The wizard states: what the machine holds, which installer opens and how, and what the boxes must show.
type wizState struct {
	prepare func(c *checker, s *scenario)
	exe     string
	args    []string
	// expected boxes: id -> "shown checked enabled" as three letters (y/n), "-" for hidden
	boxes map[int]string
	click int // a box to click before the screenshot (0: none)
	note  string
}

func wizardStates(c *checker) map[string]wizState {
	cli, gui := "ci/yakbd-cli-setup.exe", "gui/yakbd-gui-setup.exe"
	q := []string{"/quiet", "/norestart"}
	inst := func(exe string, args ...string) func(c *checker, s *scenario) {
		return func(c *checker, s *scenario) {
			c.want(s, 0, c.runInstaller(s, c.suiteFile(exe), "prep", append(append([]string(nil), q...), args...)...), exe)
		}
	}
	none := func(c *checker, s *scenario) {}
	const ed, cl, pa, se, pr = 101, 102, 103, 104, 105
	return map[string]wizState{
		"1": {none, cli, nil, map[int]string{ed: "-", cl: "yyy", pa: "yyy", se: "-", pr: "-"}, 0, "CLI installer, nothing installed"},
		"2": {none, gui, nil, map[int]string{ed: "yyy", cl: "yyn", pa: "yny", se: "-", pr: "-"}, 0, "editor installer, nothing installed"},
		"3": {inst(cli), gui, nil, map[int]string{ed: "yyy", cl: "yyn", pa: "yyy", se: "-", pr: "-"}, 0, "editor installer over the CLI on PATH"},
		"4": {inst(gui), cli, nil, map[int]string{ed: "yyy", cl: "yyn", pa: "yny", se: "-", pr: "-"}, 0, "CLI installer over CLI + editor (change)"},
		"5": {inst("old/gui-v0.1.0-preview.1/yakbd-setup.exe"), gui, nil, map[int]string{ed: "yyy", cl: "yyn", pa: "yyy", se: "-", pr: "-"}, 0, "editor installer over the published editor 0.1.0-preview.1"},
		"6": {inst(gui, "ADDTOPATH=1"), gui, []string{"/x"}, map[int]string{ed: "yyn", cl: "yyy", pa: "-", se: "-", pr: "-"}, 0, "uninstall wizard, CLI + editor"},
		"7": {inst(gui, "ADDTOPATH=1"), gui, []string{"/x"}, map[int]string{ed: "yyn", cl: "yyy", pa: "-", se: "yny", pr: "-"}, 0, "uninstall wizard, with editor settings"},
		"8": {inst(gui, "ADDTOPATH=1"), gui, []string{"/x"}, map[int]string{ed: "yyy", cl: "yny", pa: "-", se: "yny", pr: "yny"}, 102, "uninstall wizard, CLI unticked (editor only)"},
	}
}

func (c *checker) suiteState(n string) {
	st, ok := wizardStates(c)[n]
	s := c.begin("suite-state-" + n)
	if !ok {
		s.failf("no state %s", n)
		s.end()
		return
	}
	c.wipe(s, "state"+n+"-clean")
	st.prepare(c, s)
	s.notef("%s", readMachine())
	s.end()
}

// wizardControls reads the wizard's boxes: id -> "-" when hidden, else shown+checked+enabled as y/n letters.
func wizardControls(w uintptr) (map[int]string, string) {
	out := map[int]string{}
	var dump []string
	yn := func(b bool) string {
		if b {
			return "y"
		}
		return "n"
	}
	for _, id := range []int{1, 2, 3, 101, 102, 103, 104, 105, 201, 202, 203, 204, 205} {
		h, _, _ := pGetDlgItem.Call(w, uintptr(id))
		if h == 0 {
			continue
		}
		vis, _, _ := pIsWindowVisible.Call(h)
		en, _, _ := pIsWindowEnabled.Call(h)
		chk, _, _ := pSendMessageW.Call(h, bmGetCheck, 0, 0)
		state := yn(vis != 0) + yn(chk == 1) + yn(en != 0)
		if vis == 0 {
			state = "-"
		}
		if id >= 101 && id <= 105 {
			out[id] = state
		}
		dump = append(dump, fmt.Sprintf("%d %s %q", id, state, strings.ReplaceAll(windowText(h), "\r\n", " / ")))
	}
	return out, strings.Join(dump, "\n")
}

func (c *checker) suiteWizard(n string) {
	st := wizardStates(c)[n]
	s := c.begin("suite-wizard-" + n)
	defer s.end()
	s.notef("%s", st.note)
	if n == "7" || n == "8" {
		// The starting user's editor settings, so that "Remove my settings" shows.
		_ = os.MkdirAll(filepath.Join(os.Getenv("LOCALAPPDATA"), `YAKBD\YAKBD Editor`), 0o755)
	}
	exe := c.suiteFile(st.exe)
	var p syscall.Handle
	var err error
	if len(st.args) == 0 {
		p, _, err = shellOpen(exe)
	} else {
		var cmd *exec.Cmd
		cmd = exec.Command(exe, st.args...)
		if err = cmd.Start(); err == nil {
			h, e := syscall.OpenProcess(syscall.SYNCHRONIZE|0x1000, false, uint32(cmd.Process.Pid))
			p, err = h, e
		}
	}
	if err != nil {
		s.failf("start: %v", err)
		return
	}
	var w uintptr
	if !waitFor(60*time.Second, func() bool {
		w, _, _ = pFindWindowW.Call(uintptr(unsafe.Pointer(u16(wizardClass))), 0)
		return w != 0
	}) {
		s.failf("no wizard window within 60 s")
		return
	}
	time.Sleep(2 * time.Second)
	if st.click != 0 {
		// As a click: WM_COMMAND with the box's id and BN_CLICKED, posted to the wizard (press, ui_windows.go).
		if !press(w, st.click) {
			s.failf("no control %d to click", st.click)
		}
		time.Sleep(time.Second)
	}
	got, dump := wizardControls(w)
	_ = os.WriteFile(filepath.Join(c.res, "wizard-"+n+"-controls.txt"), []byte(dump+"\n"), 0o644)
	for id, want := range st.boxes {
		if got[id] != want && !(want == "-" && got[id] == "") {
			s.failf("box %d: %q, want %q (shown, ticked, enabled)", id, got[id], want)
		}
	}
	m := readMachine()
	_ = os.WriteFile(filepath.Join(c.res, "wizard-"+n+"-ready.txt"), []byte(st.note+"\n"), 0o644)
	if !waitFor(120*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(c.res, "wizard-"+n+"-shot.txt"))
		return err == nil
	}) {
		s.notef("no screenshot marker within 120 s")
	}
	if !press(w, 2) {
		s.failf("no Cancel button")
	}
	if code := exitCode(p, 30*time.Second); code != 1602 {
		s.failf("Cancel: exit %d, want 1602", code)
	}
	if after := readMachine(); after != m {
		s.failf("the cancelled wizard changed the machine: %s, before %s", after, m)
	}
}
