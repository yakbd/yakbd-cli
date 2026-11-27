package toolsetup

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/setup"
)

const code = "{11111111-2222-5333-8444-555555555555}"

const root = `c:\program files\yakbd`

// editorUserData is the editor's declaration (the editor's own settings: Qt's registry key and its local folder).
var editorUserData = []UserData{
	{Component: Editor, Kind: "key", Path: `HKCU\Software\YAKBD\YAKBD Editor`, ParentIfEmpty: true},
	{Component: Editor, Kind: "dir", Path: `%LOCALAPPDATA%\YAKBD\YAKBD Editor`, ParentIfEmpty: true},
}

// cliComp and editorComp are the components of version v: the CLI for x86, x64, arm64; the editor for x64, arm64
// (a file per build, a shared file, a subfolder).
func cliComp(v string) ComponentInput {
	c := ComponentInput{ID: CLI, Version: v, Builds: []string{BuildX86, BuildX64, BuildARM64}}
	for _, b := range c.Builds {
		c.Files = append(c.Files, Input{Path: CLIExe, Build: b, Data: pe(MachineOf[b], "cli "+v+" "+b)})
	}
	c.Files = append(c.Files, Input{Path: "LICENSE", Data: []byte("license " + v)}, Input{Path: "NOTICE", Data: []byte("notice " + v)})
	return c
}

func editorComp(v string, builds ...string) ComponentInput {
	if len(builds) == 0 {
		builds = []string{BuildX64, BuildARM64}
	}
	c := ComponentInput{ID: Editor, Version: v, Builds: builds}
	for _, b := range builds {
		c.Files = append(c.Files, Input{Path: "yakbd-gui.exe", Build: b, Data: []byte("gui " + v + " " + b)},
			Input{Path: `qt\Qt6Core.dll`, Build: b, Data: []byte("qtcore " + v + " " + b)})
	}
	c.Files = append(c.Files, Input{Path: `qt\qml\Yakbd\qmldir`, Data: []byte("qmldir " + v)})
	return c
}

func mkPayload(t *testing.T, comps ...ComponentInput) *Payload {
	t.Helper()
	m := Manifest{Product: Product{Name: "YAKBD", Publisher: "The YAKBD contributors", Code: code}, AddToPath: len(comps) == 1}
	for _, c := range comps {
		if c.ID == Editor {
			m.Shortcuts = []Shortcut{{Name: "YAKBD Editor", Component: Editor, Target: "yakbd-gui.exe"}}
			m.UserData = editorUserData
		}
	}
	enc, err := Encode(m, comps)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cliSetup(t *testing.T, f *fake, v string) *Setup {
	return &Setup{Sys: f, P: mkPayload(t, cliComp(v)), Log: t.Logf}
}

func guiSetup(t *testing.T, f *fake, cliV, edV string) *Setup {
	return &Setup{Sys: f, P: mkPayload(t, cliComp(cliV), editorComp(edV)), Log: t.Logf}
}

var machineOf = map[string]uint16{BuildX86: payload.MachineI386, BuildX64: payload.MachineAMD64, BuildARM64: payload.MachineARM64}

// run detects, decides and applies a request.
func run(s *Setup, req Request) (*Plan, *Result, error) {
	st, err := s.Detect()
	if err != nil {
		return nil, nil, err
	}
	b, err := s.Machine()
	if err != nil {
		return nil, nil, err
	}
	p, err := s.Decide(st, b, req)
	if err != nil {
		return nil, nil, err
	}
	r, err := s.Apply(st, p)
	return p, r, err
}

func mustRun(t *testing.T, s *Setup, req Request) {
	t.Helper()
	if _, _, err := run(s, req); err != nil {
		t.Fatal(err)
	}
}

func arp(f *fake) map[string]setup.Value {
	return f.keys[strings.ToLower(setup.UninstallRoot+`\`+code)]
}

func pathValue(f *fake) string { return f.keys[strings.ToLower(Environment)]["path"].Str }

func file(f *fake, p string) string { return string(f.files[strings.ToLower(p)]) }

// plantLegacy writes an installation of the earlier layout as 0.1.0-preview.2's yakbd-setup.exe left it (its record
// values; with editor, the extra payload of the editor's 0.1.0-preview.1, as its published installer carries it).
func plantLegacy(f *fake, editor bool) {
	dir := root + `\yakbd`
	f.dirs[root], f.dirs[dir] = true, true
	f.files[dir+`\yakbd.exe`] = []byte("old cli")
	f.files[dir+`\setup.exe`] = []byte("old setup")
	key := strings.ToLower(setup.UninstallRoot + `\` + LegacyCode)
	vals := map[string]string{"DisplayName": "yakbd", "DisplayVersion": "0.1.0-preview.2", "InstallLocation": `C:\Program Files\YAKBD\yakbd`,
		"YakbdToolFile": "yakbd.exe", "YakbdToolBuild": "x64", "YakbdPathAdded": "1"}
	if editor {
		extra := []string{"LICENSE", "Qt6Core.dll", `qml\QtQml\qmldir`, "yakbd-gui.exe"}
		vals["YakbdToolExtraFiles"] = strings.Join(extra, "|")
		f.dirs[dir+`\qml`], f.dirs[dir+`\qml\qtqml`] = true, true
		for _, x := range extra {
			f.files[strings.ToLower(dir+`\`+x)] = []byte("old " + x)
		}
	}
	f.keys[key] = map[string]setup.Value{}
	for n, v := range vals {
		f.keys[key][strings.ToLower(n)] = setup.Value{Name: n, Type: setup.RegSZ, Str: v}
	}
	env := f.keys[strings.ToLower(Environment)]["path"]
	env.Str += `;C:\Program Files\YAKBD\yakbd`
	f.keys[strings.ToLower(Environment)]["path"] = env
}

// The machine states of the table.
type mstate struct {
	name  string
	setup func(t *testing.T, f *fake)
	// what is installed (component -> version) and whether the CLI folder is on PATH
	has  map[string]string
	path bool
}

var states = []mstate{
	{name: "nothing", setup: func(t *testing.T, f *fake) {}, has: map[string]string{}},
	{name: "cli", setup: func(t *testing.T, f *fake) { mustRun(t, cliSetup(t, f, "0.1.0-preview.3"), Request{}) },
		has: map[string]string{CLI: "0.1.0-preview.3"}, path: true},
	{name: "cli-nopath", setup: func(t *testing.T, f *fake) {
		off := false
		mustRun(t, cliSetup(t, f, "0.1.0-preview.3"), Request{AddToPath: &off})
	}, has: map[string]string{CLI: "0.1.0-preview.3"}},
	{name: "cli-newer", setup: func(t *testing.T, f *fake) { mustRun(t, cliSetup(t, f, "0.1.0-preview.4"), Request{}) },
		has: map[string]string{CLI: "0.1.0-preview.4"}, path: true},
	{name: "cli-older", setup: func(t *testing.T, f *fake) { mustRun(t, cliSetup(t, f, "0.1.0-preview.2"), Request{}) },
		has: map[string]string{CLI: "0.1.0-preview.2"}, path: true},
	{name: "cli+editor", setup: func(t *testing.T, f *fake) {
		mustRun(t, guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3"), Request{})
	}, has: map[string]string{CLI: "0.1.0-preview.3", Editor: "0.1.0-preview.3"}},
	{name: "legacy-cli", setup: func(t *testing.T, f *fake) { plantLegacy(f, false) }, has: map[string]string{CLI: "0.1.0-preview.2"}, path: true},
	{name: "legacy-cli+editor", setup: func(t *testing.T, f *fake) { plantLegacy(f, true) },
		has: map[string]string{CLI: "0.1.0-preview.2", Editor: "0.1.0-preview.2"}, path: true},
}

// The requests of the table: each wizard choice reaches Decide as one of these.
func bp(b bool) *bool { return &b }

var requests = []struct {
	name string
	req  Request
}{
	{"defaults", Request{}},
	{"ADDLOCAL=CLI", Request{AddLocal: []string{CLI}}},
	{"ADDLOCAL=CLI,Editor", Request{AddLocal: []string{CLI, Editor}}},
	{"ADDLOCAL=Editor", Request{AddLocal: []string{Editor}}},
	{"REMOVE=Editor", Request{Remove: []string{Editor}}},
	{"REMOVE=CLI", Request{Remove: []string{CLI}}},
	{"ADDTOPATH=0", Request{AddToPath: bp(false)}},
	{"ADDTOPATH=1", Request{AddToPath: bp(true)}},
	{"/x", Request{Uninstall: true}},
	{"/f", Request{Repair: true}},
}

// expect is the table's oracle: the rules of docs/installer-cli.md §12.3 written out once more, plainly.
type outcome struct {
	code  int               // exit code of a refusal, 0 for success
	final map[string]string // component -> version afterwards
	path  bool
}

func expect(ms mstate, gui bool, build string, req Request) outcome {
	mine := map[string]string{CLI: "0.1.0-preview.3"}
	carries := map[string]bool{CLI: true}
	if gui {
		mine[Editor] = "0.1.0-preview.3"
		carries[Editor] = build != BuildX86
	}
	installed := len(ms.has) > 0
	legacy := strings.HasPrefix(ms.name, "legacy")
	if (req.Uninstall || req.Repair) && !installed {
		return outcome{code: 1605}
	}
	add := req.AddLocal
	if add == nil && !req.Repair {
		for _, id := range ComponentIDs {
			if carries[id] {
				add = append(add, id)
			}
		}
	}
	for _, id := range add {
		if !carries[id] {
			return outcome{code: 1639}
		}
	}
	final := map[string]bool{}
	if !req.Uninstall {
		for id := range ms.has {
			final[id] = true
		}
		for _, id := range add {
			final[id] = true
		}
	}
	for _, id := range req.Remove {
		final[id] = false
	}
	if final[Editor] && !final[CLI] {
		return outcome{code: 1639}
	}
	if legacy && final[Editor] && !carries[Editor] {
		return outcome{code: 1603}
	}
	o := outcome{final: map[string]string{}}
	for _, id := range ComponentIDs {
		if !final[id] {
			continue
		}
		v := ms.has[id]
		if carries[id] && (legacy || v == "" || CompareVersions(mine[id], v) >= 0) {
			v = mine[id]
		}
		o.final[id] = v
	}
	_, hadCLI := ms.has[CLI]
	switch {
	case !final[CLI]:
	case req.AddToPath != nil:
		o.path = *req.AddToPath
	case ms.path:
		o.path = true
	case hadCLI:
		o.path = false
	default:
		o.path = !gui
	}
	return o
}

// check compares the machine with an outcome: the entry, every component's files with their version's bytes, the
// shortcut, the PATH, the earlier layout gone.
func check(t *testing.T, f *fake, build string, o outcome) {
	t.Helper()
	a := arp(f)
	if len(o.final) == 0 {
		if a != nil {
			t.Errorf("entry left: %v", a)
		}
	} else {
		if a == nil {
			t.Fatalf("no entry")
		}
		var comps []string
		for _, id := range ComponentIDs {
			if o.final[id] != "" {
				comps = append(comps, id)
			}
		}
		if got := a[strings.ToLower(RecComponents)].Str; got != strings.Join(comps, ",") {
			t.Errorf("components %q, want %v", got, comps)
		}
		want := o.final[CLI]
		if o.final[Editor] != "" {
			want = o.final[Editor]
		}
		if got := a["displayversion"].Str; got != want {
			t.Errorf("DisplayVersion %q, want %q", got, want)
		}
		if a["displayname"].Str != "YAKBD" {
			t.Errorf("DisplayName %q", a["displayname"].Str)
		}
	}
	if v := o.final[CLI]; v != "" {
		if got := file(f, root+`\cli\yakbd.exe`); !strings.HasSuffix(got, "cli "+v+" "+build) {
			t.Errorf("yakbd.exe %q, want version %s", got[len(got)-24:], v)
		}
		if file(f, root+`\cli\license`) != "license "+v {
			t.Errorf("LICENSE %q", file(f, root+`\cli\license`))
		}
	} else if f.dirs[root+`\cli`] {
		t.Errorf("CLI folder left")
	}
	lnk := `c:\programdata\microsoft\windows\start menu\programs\yakbd editor.lnk`
	if v := o.final[Editor]; v != "" {
		if got := file(f, root+`\editor\yakbd-gui.exe`); got != "gui "+v+" "+build {
			t.Errorf("yakbd-gui.exe %q, want %s", got, v)
		}
		if file(f, root+`\editor\qt\qml\yakbd\qmldir`) != "qmldir "+v {
			t.Errorf("qmldir missing")
		}
		if file(f, lnk) == "" {
			t.Errorf("no shortcut")
		}
	} else {
		if f.dirs[root+`\editor`] {
			t.Errorf("Editor folder left")
		}
		if file(f, lnk) != "" {
			t.Errorf("shortcut left")
		}
	}
	if got := pathHas(pathValue(f), `C:\Program Files\YAKBD\CLI`); got != o.path {
		t.Errorf("CLI on PATH %v, want %v (%s)", got, o.path, pathValue(f))
	}
	if pathHas(pathValue(f), `C:\Program Files\YAKBD\yakbd`) || f.dirs[root+`\yakbd`] || f.keys[strings.ToLower(setup.UninstallRoot+`\`+LegacyCode)] != nil {
		t.Errorf("the earlier layout is left")
	}
	if len(o.final) == 0 && f.dirs[root] {
		var left []string
		for k := range f.files {
			if strings.HasPrefix(k, root) {
				left = append(left, k)
			}
		}
		t.Errorf("YAKBD folder left: %v", left)
	}
}

// TestStateTable: every machine state × each installer × each request × every architecture. A refusal changes
// nothing; a success leaves what the oracle says; then the installer's /x leaves the machine as before anything.
func TestStateTable(t *testing.T) {
	n := 0
	for _, ms := range states {
		for _, gui := range []bool{false, true} {
			for _, rq := range requests {
				for _, build := range []string{BuildX86, BuildX64, BuildARM64} {
					if strings.HasPrefix(ms.name, "legacy") && build != BuildX64 {
						continue // the published editor installer is x64 only; the legacy CLI case is the same on every build
					}
					name := fmt.Sprintf("%s/%v/%s/%s", ms.name, map[bool]string{false: "cli-setup", true: "gui-setup"}[gui], rq.name, build)
					if (ms.name == "cli+editor") && build == BuildX86 {
						continue // no editor on x86
					}
					t.Run(name, func(t *testing.T) {
						f := newFake()
						f.machine = machineOf[build]
						base := f.snapshot()
						ms.setup(t, f)
						before := f.snapshot()
						var s *Setup
						if gui {
							s = guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3")
						} else {
							s = cliSetup(t, f, "0.1.0-preview.3")
						}
						s.Log = func(string, ...any) {}
						o := expect(ms, gui, build, rq.req)
						_, _, err := run(s, rq.req)
						if got := ExitCode(err); got != o.code {
							t.Fatalf("exit %d (%v), want %d", got, err, o.code)
						}
						if err != nil {
							if f.snapshot() != before {
								t.Errorf("a refusal changed the machine")
							}
							return
						}
						check(t, f, build, o)
						if len(o.final) > 0 {
							if _, _, err := run(s, Request{Uninstall: true}); err != nil {
								t.Fatal(err)
							}
						}
						if after := f.snapshot(); after != base {
							t.Errorf("after /x the machine is not as before anything:\n%s\n---\n%s", after, base)
						}
					})
					n++
				}
			}
		}
	}
	t.Logf("%d cases", n)
}

// TestWizardDefaultsAreQuiet: in every state, for both installers and the install and uninstall wizards on every
// architecture, the wizard's boxes as they start give the same plan as the command line without properties.
func TestWizardDefaultsAreQuiet(t *testing.T) {
	for _, ms := range states {
		for _, gui := range []bool{false, true} {
			for _, build := range []string{BuildX86, BuildX64, BuildARM64} {
				if ms.name == "cli+editor" && build == BuildX86 {
					continue
				}
				for _, un := range []bool{false, true} {
					f := newFake()
					f.machine = machineOf[build]
					ms.setup(t, f)
					s := cliSetup(t, f, "0.1.0-preview.3")
					if gui {
						s = guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3")
					}
					st, _ := s.Detect()
					w, err := NewWizard(s, st, build, un, false)
					quiet, qerr := s.Decide(st, build, Request{Uninstall: un})
					if err != nil || qerr != nil {
						if ExitCode(err) != ExitCode(qerr) {
							t.Errorf("%s gui=%v %s un=%v: wizard %v, quiet %v", ms.name, gui, build, un, err, qerr)
						}
						continue
					}
					wp, err := w.Plan()
					if err != nil {
						t.Errorf("%s gui=%v %s un=%v: wizard plan %v", ms.name, gui, build, un, err)
						continue
					}
					if wp.String() != quiet.String() {
						t.Errorf("%s gui=%v %s un=%v: wizard %s, quiet %s", ms.name, gui, build, un, wp, quiet)
					}
				}
			}
		}
	}
}

// TestWizardBoxes: what the boxes show and lock.
func TestWizardBoxes(t *testing.T) {
	f := newFake()
	s := guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3")
	st, _ := s.Detect()
	w, _ := NewWizard(s, st, BuildX64, false, false)
	if !w.Editor.Checked || !w.Editor.Enabled || !w.CLI.Checked || w.CLI.Enabled || w.Path.Checked || !w.Path.Visible {
		t.Errorf("gui setup, nothing installed: %+v %+v %+v", w.Editor, w.CLI, w.Path)
	}
	w.Toggle(BoxCLI) // locked
	if !w.CLI.Checked {
		t.Error("the CLI box could be unticked while the editor is ticked")
	}
	w.Toggle(BoxEditor)
	if w.Editor.Checked || !w.CLI.Enabled {
		t.Error("unticking the editor did not unlock the CLI")
	}
	w.Toggle(BoxCLI)
	if w.CanGo() || w.Path.Visible {
		t.Error("nothing ticked on an empty machine, yet the wizard can go")
	}
	// 32-bit Windows: the editor greyed and unticked, with the reason.
	w, _ = NewWizard(s, st, BuildX86, false, false)
	if w.Editor.Checked || w.Editor.Enabled || w.Editor.Note != "The editor needs 64-bit Windows." || !w.CLI.Enabled {
		t.Errorf("x86: %+v %+v", w.Editor, w.CLI)
	}
	w.Toggle(BoxEditor)
	if w.Editor.Checked {
		t.Error("the greyed editor box could be ticked")
	}
	// The CLI installer: PATH ticked.
	w, _ = NewWizard(cliSetup(t, f, "0.1.0-preview.3"), st, BuildX64, false, false)
	if w.Editor.Visible || !w.Path.Checked {
		t.Errorf("cli setup: %+v %+v", w.Editor, w.Path)
	}
	// Uninstall of CLI + editor with settings and PATH: both ticked, the editor locked; unticking the CLI shows
	// "Remove YAKBD CLI from PATH" and keeps the settings box.
	mustRun(t, s, Request{AddToPath: bp(true)})
	st, _ = s.Detect()
	w, _ = NewWizard(s, st, BuildX64, true, true)
	if !w.CLI.Checked || !w.Editor.Checked || w.Editor.Enabled || !w.Settings.Visible || w.Settings.Checked || w.PathRemove.Visible {
		t.Errorf("uninstall: %+v %+v %+v %+v", w.CLI, w.Editor, w.Settings, w.PathRemove)
	}
	if w.CLI.Note == "" {
		t.Error("the wizard does not say the PATH entry goes with the CLI")
	}
	w.Toggle(BoxCLI)
	if !w.Editor.Checked || !w.Editor.Enabled || !w.PathRemove.Visible || w.PathRemove.Checked {
		t.Errorf("uninstall, CLI kept: %+v %+v", w.Editor, w.PathRemove)
	}
	if r := w.Request(); r.Uninstall || len(r.Remove) != 1 || r.Remove[0] != Editor || r.AddToPath == nil || !*r.AddToPath {
		t.Errorf("remove-editor request %+v", r)
	}
	w, _ = NewWizard(s, st, BuildX64, true, false)
	if w.Settings.Visible {
		t.Error("the settings box shown for a user without settings")
	}
}

// TestPropsAndChildArgs: the installers' properties out of a command line, and back for the elevated copy without
// REMOVEUSERDATA.
func TestPropsAndChildArgs(t *testing.T) {
	rest, req, err := TakeProps([]string{"/quiet", "ADDLOCAL=cli,editor", "addtopath=0", "REMOVEUSERDATA=1", "REMOVE=ALL", "REBOOT=S"})
	if err != nil || strings.Join(rest, " ") != "/quiet REMOVE=ALL REBOOT=S" || len(req.AddLocal) != 2 || *req.AddToPath || !req.RemoveUserData {
		t.Fatalf("%q %+v %v", rest, req, err)
	}
	if got := strings.Join(PropArgs(req), " "); got != "ADDLOCAL=CLI,Editor ADDTOPATH=0" {
		t.Errorf("child args %q", got)
	}
	for _, bad := range []string{"ADDLOCAL=Foo", "ADDTOPATH=2", "REMOVE=CLI,x", "REMOVEUSERDATA=yes"} {
		if _, _, err := TakeProps([]string{bad}); err == nil || err.Code != cmdline.InvalidCommandLine {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if _, req, _ := TakeProps([]string{"ADDLOCAL=ALL", "REMOVE=Editor"}); req.AddLocal != nil || req.Remove[0] != Editor {
		t.Errorf("%+v", req)
	}
}

// TestUserData: the starting user's settings removed, parents only when empty; nothing outside the declaration.
func TestUserData(t *testing.T) {
	u := newUser()
	u.keys[`software\yakbd\yakbd editor`] = 3
	u.keys[`software\yakbd`] = 0
	u.dirs[`c:\users\t\appdata\local\yakbd\yakbd editor`] = true
	u.dirs[`c:\users\t\appdata\local\yakbd`] = true
	u.files[`c:\users\t\appdata\local\yakbd\yakbd editor\recent.json`] = true
	comps := map[string]bool{Editor: true}
	if !HasUserData(u, editorUserData, comps) {
		t.Fatal("settings not found")
	}
	if err := RemoveUserData(u, editorUserData, comps, t.Logf); err != nil {
		t.Fatal(err)
	}
	if len(u.keys) != 0 || len(u.dirs) != 0 || len(u.files) != 0 {
		t.Errorf("left: %v %v %v", u.keys, u.dirs, u.files)
	}
	// A parent with something else stays.
	u = newUser()
	u.keys[`software\yakbd\yakbd editor`] = 1
	u.keys[`software\yakbd\other`] = 1
	u.keys[`software\yakbd`] = 0
	_ = RemoveUserData(u, editorUserData, comps, t.Logf)
	if !u.UserKeyExists(`Software\YAKBD`) || !u.UserKeyExists(`Software\YAKBD\other`) {
		t.Error("a non-empty parent or a sibling was deleted")
	}
	for _, bad := range []UserData{
		{Component: Editor, Kind: "key", Path: `HKLM\Software\YAKBD`},
		{Component: Editor, Kind: "key", Path: `HKCU\Software`},
		{Component: Editor, Kind: "key", Path: `HKCU\Software\..\x`},
		{Component: Editor, Kind: "dir", Path: `C:\Windows`},
		{Component: Editor, Kind: "dir", Path: `%USERPROFILE%\x`},
		{Component: Editor, Kind: "dir", Path: `%LOCALAPPDATA%\YAKBD`, ParentIfEmpty: true},
		{Component: "X", Kind: "dir", Path: `%LOCALAPPDATA%\YAKBD\x`},
	} {
		if CheckUserData(bad) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// TestUserDataFromRecord: the declaration travels in the uninstall entry, so a CLI installer that removes the editor
// still knows what to delete; a damaged record names nothing outside the user's settings.
func TestUserDataFromRecord(t *testing.T) {
	f := newFake()
	mustRun(t, guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3"), Request{})
	s := cliSetup(t, f, "0.1.0-preview.3")
	st, _ := s.Detect()
	if len(st.UserData) != 2 {
		t.Fatalf("record user data %+v", st.UserData)
	}
	v := arp(f)[strings.ToLower(RecUserData)]
	v.Str = `[{"component":"Editor","kind":"dir","path":"C:\\Windows"}]`
	arp(f)[strings.ToLower(RecUserData)] = v
	st, _ = s.Detect()
	if len(st.UserData) != 0 {
		t.Errorf("a damaged declaration was read: %+v", st.UserData)
	}
}

// TestKeepNewerAndNotes: the editor installer with an older CLI keeps the newer CLI and says so.
func TestKeepNewerAndNotes(t *testing.T) {
	f := newFake()
	mustRun(t, cliSetup(t, f, "0.1.0-preview.4"), Request{})
	p, r, err := run(guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3"), Request{})
	if err != nil || p.Comp[CLI] != KeepNewer || p.Comp[Editor] != Install {
		t.Fatalf("%v %v", p, err)
	}
	if !strings.Contains(strings.Join(r.Notes, "\n"), "newer than this installer's") {
		t.Errorf("notes %q", r.Notes)
	}
	// The kept CLI's installer copy is replaced by the editor installer (it carries every component that stays).
	if !strings.HasPrefix(file(f, root+`\setup.exe`), "MZ yakbd-setup itself") {
		t.Errorf("setup.exe %q", file(f, root+`\setup.exe`))
	}
	// A CLI installer over CLI + editor keeps the copy that carries the editor.
	f.files[strings.ToLower(f.self)] = []byte("MZ the cli setup")
	mustRun(t, cliSetup(t, f, "0.1.0-preview.4"), Request{})
	if file(f, root+`\setup.exe`) == "MZ the cli setup" {
		t.Error("the CLI installer replaced the copy that carries the editor")
	}
}

// TestRollbackKeepsLegacy: a takeover that fails at any step leaves the earlier layout as it was.
func TestRollbackKeepsLegacy(t *testing.T) {
	for _, failAt := range []string{`\cli\yakbd.exe.yakbd-tmp`, `\editor\qt\qt6core.dll.yakbd-tmp`, `\setup.exe`, `yakbd editor.lnk`} {
		f := newFake()
		plantLegacy(f, true)
		before := f.snapshot()
		f.failWrite = failAt
		_, _, err := run(guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3"), Request{})
		if err == nil {
			t.Fatalf("%s: no failure", failAt)
		}
		if after := f.snapshot(); after != before {
			t.Errorf("%s: rollback left:\n%s\n---\n%s", failAt, after, before)
		}
	}
}

// TestShortcutReadBack: a shortcut that is not there after it was reported saved, or points elsewhere, fails with 1603.
func TestShortcutReadBack(t *testing.T) {
	for _, bad := range []func(f *fake){
		func(f *fake) { f.linkLost = `\YAKBD Editor.lnk` },
		func(f *fake) { f.linkWrong = `\YAKBD Editor.lnk` },
		func(f *fake) { f.linkFail = `\YAKBD Editor.lnk` },
	} {
		f := newFake()
		before := f.snapshot()
		bad(f)
		_, _, err := run(guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3"), Request{})
		if ExitCode(err) != cmdline.InstallFailure {
			t.Errorf("exit %d (%v)", ExitCode(err), err)
		}
		if f.snapshot() != before {
			t.Error("rollback incomplete")
		}
	}
}

// TestRemoveUserDataOnlyWhenEditorGoes: REMOVEUSERDATA is a plan flag; the elevated copy never gets it.
func TestRemoveUserDataFlag(t *testing.T) {
	f := newFake()
	s := guiSetup(t, f, "0.1.0-preview.3", "0.1.0-preview.3")
	mustRun(t, s, Request{})
	st, _ := s.Detect()
	p, _ := s.Decide(st, BuildX64, Request{Uninstall: true, RemoveUserData: true})
	if !p.RemoveUserData {
		t.Error("flag lost")
	}
	for _, a := range PropArgs(Request{Uninstall: true, RemoveUserData: true}) {
		if strings.Contains(a, "REMOVEUSERDATA") {
			t.Error("REMOVEUSERDATA passed on")
		}
	}
}

// TestPayloadRefusals: bad payloads do not encode.
func TestPayloadRefusals(t *testing.T) {
	m := Manifest{Product: Product{Name: "YAKBD", Code: code}}
	bad := [][]ComponentInput{
		{editorComp("1.0.0")},                                  // no CLI
		{editorComp("1.0.0"), cliComp("1.0.0")},                // CLI not first
		{cliComp("1.0")},                                       // not semver
		{{ID: CLI, Version: "1.0.0", Builds: []string{"x64"}}}, // no yakbd.exe
		{cliComp("1.0.0"), {ID: "Foo", Version: "1.0.0", Builds: []string{"x64"}}},
	}
	for i, comps := range bad {
		if _, err := Encode(m, comps); err == nil {
			t.Errorf("%d accepted", i)
		}
	}
	m.Shortcuts = []Shortcut{{Name: "x", Component: Editor, Target: "missing.exe"}}
	if _, err := Encode(m, []ComponentInput{cliComp("1.0.0"), editorComp("1.0.0")}); err == nil {
		t.Error("a shortcut to a missing file accepted")
	}
}

func TestCompareVersions(t *testing.T) {
	order := []string{"0.1.0-preview.2", "0.1.0-preview.3", "0.1.0-preview.10", "0.1.0-rc.1", "0.1.0", "0.1.1", "0.2.0"}
	for i := range order {
		for j := range order {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := CompareVersions(order[i], order[j]); got != want {
				t.Errorf("%s vs %s: %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	vs := append([]string(nil), order...)
	sort.Slice(vs, func(i, j int) bool { return CompareVersions(vs[i], vs[j]) < 0 })
	if strings.Join(vs, " ") != strings.Join(order, " ") {
		t.Error(vs)
	}
}
