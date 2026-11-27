package toolsetup

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/payload"
)

// editor is an extra payload with a program for x64 and arm64 only and a file for every build.
func editor(tag string) []Extra {
	return []Extra{
		{Path: "yakbd-gui.exe", Build: BuildX64, Data: []byte("gui x64 " + tag)},
		{Path: "yakbd-gui.exe", Build: BuildARM64, Data: []byte("gui arm64 " + tag)},
		{Path: `tools\viewer.exe`, Data: []byte("viewer " + tag)},
	}
}

var editorShortcuts = []Shortcut{{Name: "YAKBD Editor", Target: "yakbd-gui.exe"}, {Name: "YAKBD Viewer", Target: `tools\viewer.exe`}}

// newWithShortcuts is newBundled with shortcuts.
func newWithShortcuts(t *testing.T, f *fake, version string, extra []Extra, scs []Shortcut) *Setup {
	t.Helper()
	builds := []string{BuildX86, BuildX64, BuildARM64}
	data := map[string][]byte{}
	for _, b := range builds {
		data[b] = pe(MachineOf[b], b+" "+version)
	}
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: version, Publisher: "The YAKBD contributors", Code: code}, Shortcuts: scs}, builds, data, extra)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	return &Setup{Sys: f, P: p, Log: t.Logf}
}

var progsLower = strings.ToLower(progsDir)

func lnk(f *fake, name string) string {
	return string(f.files[progsLower+`\`+strings.ToLower(name)+".lnk"])
}

func TestShortcutInstallUninstall(t *testing.T) {
	for _, m := range []struct {
		machine uint16
		rec     string
	}{{payload.MachineAMD64, "YAKBD Editor|YAKBD Viewer"}, {payload.MachineARM64, "YAKBD Editor|YAKBD Viewer"}, {payload.MachineI386, "YAKBD Viewer"}} {
		f := newFake()
		f.machine = m.machine
		before := f.snapshot()
		s := newWithShortcuts(t, f, "0.2.0", editor("1"), editorShortcuts)
		if _, err := s.Install(); err != nil {
			t.Fatal(err)
		}
		want := `lnk C:\Program Files\YAKBD\yakbd\yakbd-gui.exe | C:\Program Files\YAKBD\yakbd`
		if m.machine == payload.MachineI386 {
			want = "" // the x86 build carries no yakbd-gui.exe, so no shortcut to it
		}
		if got := lnk(f, "YAKBD Editor"); got != want {
			t.Errorf("%04x: editor shortcut %q, want %q", m.machine, got, want)
		}
		if got := lnk(f, "YAKBD Viewer"); got != `lnk C:\Program Files\YAKBD\yakbd\tools\viewer.exe | C:\Program Files\YAKBD\yakbd` {
			t.Errorf("%04x: viewer shortcut %q", m.machine, got)
		}
		if rec := arpOf(f)[strings.ToLower(RecShortcuts)].Str; rec != m.rec {
			t.Errorf("%04x: record %q, want %q", m.machine, rec, m.rec)
		}
		if _, err := s.Uninstall(); err != nil {
			t.Fatal(err)
		}
		if after := f.snapshot(); after != before {
			t.Errorf("%04x: uninstall left:\n%s", m.machine, after)
		}
	}
}

// TestShortcutNoneForMachine: a machine whose build has no extra payload gets no shortcut and no record value.
func TestShortcutNoneForMachine(t *testing.T) {
	f := newFake()
	f.machine = payload.MachineI386
	x := []Extra{{Path: "yakbd-gui.exe", Build: BuildX64, Data: []byte("gui")}}
	if _, err := newWithShortcuts(t, f, "0.2.0", x, editorShortcuts[:1]).Install(); err != nil {
		t.Fatal(err)
	}
	if _, ok := arpOf(f)[strings.ToLower(RecShortcuts)]; ok || lnk(f, "YAKBD Editor") != "" {
		t.Error("x86 got a shortcut or a record for an x64-only payload")
	}
}

// TestShortcutSwitch: the plain installer over a bundle removes the shortcuts; the bundle again makes them; a repair
// makes a deleted one again; changed shortcut sets replace each other.
func TestShortcutSwitch(t *testing.T) {
	f := newFake()
	before := f.snapshot()
	bundle := newWithShortcuts(t, f, "0.2.0", editor("1"), editorShortcuts)
	if _, err := bundle.Install(); err != nil {
		t.Fatal(err)
	}
	if _, err := newSetup(t, f, "0.2.0").Install(); err != nil {
		t.Fatal(err)
	}
	if lnk(f, "YAKBD Editor") != "" || lnk(f, "YAKBD Viewer") != "" {
		t.Error("the plain installer over the bundle left a shortcut")
	}
	if v, ok := arpOf(f)[strings.ToLower(RecShortcuts)]; !ok || v.Str != "" {
		t.Errorf("record after plain over bundle: %+v", v)
	}
	if _, err := bundle.Install(); err != nil {
		t.Fatal(err)
	}
	if lnk(f, "YAKBD Editor") == "" || lnk(f, "YAKBD Viewer") == "" {
		t.Fatal("the bundle again did not make its shortcuts")
	}
	// Repair: a shortcut the user deleted comes back.
	delete(f.files, progsLower+`\yakbd editor.lnk`)
	if r, err := bundle.Install(); err != nil || !r.Upgraded {
		t.Fatalf("%+v %v", r, err)
	}
	if lnk(f, "YAKBD Editor") == "" {
		t.Error("repair did not make the deleted shortcut again")
	}
	// A newer bundle with another set: the editor kept, the viewer's shortcut gone, a new one made.
	next := newWithShortcuts(t, f, "0.3.0", editor("2"), []Shortcut{{Name: "YAKBD Editor", Target: "yakbd-gui.exe"}, {Name: "Viewer", Target: `tools\viewer.exe`}})
	if _, err := next.Install(); err != nil {
		t.Fatal(err)
	}
	if lnk(f, "YAKBD Viewer") != "" || lnk(f, "Viewer") == "" || lnk(f, "YAKBD Editor") == "" {
		t.Error("a changed shortcut set did not replace the earlier one")
	}
	if rec := arpOf(f)[strings.ToLower(RecShortcuts)].Str; rec != "YAKBD Editor|Viewer" {
		t.Errorf("record %q", rec)
	}
	// The plain installer uninstalls the bundle, shortcuts included.
	if _, err := newSetup(t, f, "0.3.0").Uninstall(); err != nil {
		t.Fatal(err)
	}
	if after := f.snapshot(); after != before {
		t.Errorf("uninstall left:\n%s", after)
	}
}

// TestShortcutRollback: a fresh install whose second shortcut cannot be made removes the first one and everything else;
// without the Programs folder nothing is installed.
func TestShortcutRollback(t *testing.T) {
	f := newFake()
	before := f.snapshot()
	f.linkFail = `\YAKBD Viewer.lnk`
	_, err := newWithShortcuts(t, f, "0.2.0", editor("1"), editorShortcuts).Install()
	wantCode(t, err, cmdline.InstallFailure)
	if after := f.snapshot(); after != before {
		t.Errorf("rollback left:\n%s", after)
	}
	f.linkFail, f.noProgs = "", true
	_, err = newWithShortcuts(t, f, "0.2.0", editor("1"), editorShortcuts).Install()
	wantCode(t, err, cmdline.InstallFailure)
	if after := f.snapshot(); after != before {
		t.Errorf("rollback without the Programs folder left:\n%s", after)
	}
}

// TestShortcutRecordIsNames: uninstall deletes only "<name>.lnk" in the Programs folder for each valid name of the
// record, never a path the registry holds.
func TestShortcutRecordIsNames(t *testing.T) {
	f := newFake()
	if _, err := newWithShortcuts(t, f, "0.2.0", editor("1"), editorShortcuts).Install(); err != nil {
		t.Fatal(err)
	}
	f.files[`c:\keep.lnk`] = []byte("x")
	f.files[`c:\programdata\microsoft\windows\start menu\keep.lnk`] = []byte("x")
	v := arpOf(f)[strings.ToLower(RecShortcuts)]
	v.Str += `|C:\keep|..\keep|`
	arpOf(f)[strings.ToLower(RecShortcuts)] = v
	if _, err := newSetup(t, f, "0.2.0").Uninstall(); err != nil {
		t.Fatal(err)
	}
	if f.files[`c:\keep.lnk`] == nil || f.files[`c:\programdata\microsoft\windows\start menu\keep.lnk`] == nil {
		t.Error("uninstall deleted a file named by a path in the record")
	}
	if lnk(f, "YAKBD Editor") != "" {
		t.Error("uninstall left the shortcut")
	}
}

func TestShortcutEncodeRefusals(t *testing.T) {
	x := editor("1")
	allBuilds := []string{BuildX86, BuildX64, BuildARM64}
	allData := map[string][]byte{}
	for _, b := range allBuilds {
		allData[b] = pe(MachineOf[b], b)
	}
	for _, scs := range [][]Shortcut{
		{{Name: "", Target: "yakbd-gui.exe"}},
		{{Name: `a\b`, Target: "yakbd-gui.exe"}},
		{{Name: "a|b", Target: "yakbd-gui.exe"}},
		{{Name: "dot.", Target: "yakbd-gui.exe"}},
		{{Name: "x", Target: "missing.exe"}},
		{{Name: "x", Target: "yakbd.exe"}},
		{{Name: "Editor", Target: "yakbd-gui.exe"}, {Name: "editor", Target: `tools\viewer.exe`}},
	} {
		_, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}, Shortcuts: scs}, allBuilds, allData, x)
		if err == nil {
			t.Errorf("%+v accepted", scs)
		}
	}
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}}, allBuilds, allData, x)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte(`"shortcuts"`)) {
		t.Error("a payload without shortcuts names them")
	}
	// A target matches the payload path with its letter case ignored.
	if _, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}, Shortcuts: []Shortcut{{Name: "E", Target: "YAKBD-GUI.EXE"}}}, allBuilds, allData, x); err != nil {
		t.Error(err)
	}
}
