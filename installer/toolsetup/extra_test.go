package toolsetup

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/setup"
)

// gui is an extra payload like a graphical program's: files for every build, and a folder per build.
func gui(tag string) []Extra {
	return []Extra{
		{Path: "yakbd-gui.exe", Data: []byte("gui " + tag)},
		{Path: `qml\QtQuick\Controls\a.qml`, Data: []byte("qml " + tag)},
		{Path: `plugins\platforms\qwindows.dll`, Build: BuildX64, Data: []byte("x64 plugin " + tag)},
		{Path: `plugins\platforms\qwindows.dll`, Build: BuildARM64, Data: []byte("arm64 plugin " + tag)},
	}
}

// newBundled is newSetup with an extra payload.
func newBundled(t *testing.T, f *fake, version string, extra []Extra, builds ...string) *Setup {
	t.Helper()
	if len(builds) == 0 {
		builds = []string{BuildX86, BuildX64, BuildARM64}
	}
	data := map[string][]byte{}
	for _, b := range builds {
		data[b] = pe(MachineOf[b], b+" "+version)
	}
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: version, Publisher: "The YAKBD contributors", Code: code}}, builds, data, extra)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	return &Setup{Sys: f, P: p, Log: t.Logf}
}

func arpOf(f *fake) map[string]setup.Value {
	return f.keys[strings.ToLower(setup.UninstallRoot+`\`+code)]
}

func TestExtraInstallUninstall(t *testing.T) {
	for _, m := range []struct {
		machine uint16
		plugin  string // the plugin's content, or "" for none
	}{{payload.MachineAMD64, "x64 plugin 1"}, {payload.MachineARM64, "arm64 plugin 1"}, {payload.MachineI386, ""}} {
		f := newFake()
		f.machine = m.machine
		before := f.snapshot()
		s := newBundled(t, f, "0.2.0", gui("1"))
		if _, err := s.Install(); err != nil {
			t.Fatal(err)
		}
		if string(f.files[dir+`\yakbd-gui.exe`]) != "gui 1" || string(f.files[dir+`\qml\qtquick\controls\a.qml`]) != "qml 1" {
			t.Errorf("%04x: files for every build missing", m.machine)
		}
		if got := string(f.files[dir+`\plugins\platforms\qwindows.dll`]); got != m.plugin {
			t.Errorf("%04x: plugin %q, want %q", m.machine, got, m.plugin)
		}
		want := `yakbd-gui.exe|qml\QtQuick\Controls\a.qml`
		if m.plugin != "" {
			want += `|plugins\platforms\qwindows.dll`
		}
		if rec := arpOf(f)[strings.ToLower(RecExtra)].Str; rec != want {
			t.Errorf("record %q, want %q", rec, want)
		}
		if _, err := s.Uninstall(); err != nil {
			t.Fatal(err)
		}
		if after := f.snapshot(); after != before {
			t.Errorf("%04x: uninstall left:\n%s", m.machine, after)
		}
	}
}

// TestCLIOnlyEntryUnchanged: an installer without an extra payload writes no record value, so that its uninstall
// entry is the one of the installers before the extra payload existed.
func TestCLIOnlyEntryUnchanged(t *testing.T) {
	f := newFake()
	if _, err := newSetup(t, f, "0.2.0").Install(); err != nil {
		t.Fatal(err)
	}
	if _, ok := arpOf(f)[strings.ToLower(RecExtra)]; ok {
		t.Error("a CLI-only install wrote the extra record")
	}
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}}, []string{BuildX64}, map[string][]byte{BuildX64: pe(payload.MachineAMD64, "x")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte(`"extra"`)) {
		t.Error("a payload without extra files names them")
	}
}

// TestExtraSwitch: the GUI-bundled and the CLI-only installer are the same product and replace each other in place,
// in both directions, and either one uninstalls what the other installed.
func TestExtraSwitch(t *testing.T) {
	f := newFake()
	before := f.snapshot()
	if _, err := newSetup(t, f, "0.2.0").Install(); err != nil {
		t.Fatal(err)
	}
	cliOnly := f.snapshot()
	// The same version with the extra payload: a repair that adds the files.
	r, err := newBundled(t, f, "0.2.0", gui("1")).Install()
	if err != nil || !r.Upgraded {
		t.Fatalf("%+v %v", r, err)
	}
	if f.files[dir+`\yakbd-gui.exe`] == nil || strings.Count(f.keys[strings.ToLower(Environment)]["path"].Str, "YAKBD") != 1 {
		t.Fatal("bundled over CLI-only: files missing or PATH twice")
	}
	// CLI-only over bundled: the extra files and their folders go; only the empty record value stays.
	if _, err := newSetup(t, f, "0.2.0").Install(); err != nil {
		t.Fatal(err)
	}
	for k := range f.files {
		if strings.HasPrefix(k, dir+`\`) && !strings.HasSuffix(k, `\yakbd.exe`) && !strings.HasSuffix(k, `\setup.exe`) {
			t.Errorf("CLI-only over bundled left %s", k)
		}
	}
	for k := range f.dirs {
		if strings.HasPrefix(k, dir+`\`) {
			t.Errorf("CLI-only over bundled left the folder %s", k)
		}
	}
	if v, ok := arpOf(f)[strings.ToLower(RecExtra)]; !ok || v.Str != "" {
		t.Errorf("record after CLI-only over bundled: %+v", v)
	}
	delete(arpOf(f), strings.ToLower(RecExtra))
	if f.snapshot() != cliOnly {
		t.Error("CLI-only over bundled differs from a CLI-only install by more than the record")
	}
	// A newer bundled installer, then an uninstall by the CLI-only installer (/x through another file of the
	// product): the record says what to remove.
	if _, err := newBundled(t, f, "0.3.0", gui("2")).Install(); err != nil {
		t.Fatal(err)
	}
	if string(f.files[dir+`\yakbd-gui.exe`]) != "gui 2" {
		t.Error("upgrade did not replace the extra file")
	}
	if _, err := newSetup(t, f, "0.3.0").Uninstall(); err != nil {
		t.Fatal(err)
	}
	if after := f.snapshot(); after != before {
		t.Errorf("uninstall by the CLI-only installer left:\n%s", after)
	}
}

func TestExtraVersions(t *testing.T) {
	// Versions are compared as for every installer (§6): an older installer never goes over a newer one, with or
	// without the extra payload, in either direction.
	for _, c := range []struct{ first, second bool }{{false, true}, {true, false}, {true, true}} {
		f := newFake()
		mk := func(bundled bool, v string) *Setup {
			if bundled {
				return newBundled(t, f, v, gui(v))
			}
			return newSetup(t, f, v)
		}
		if _, err := mk(c.first, "0.3.0").Install(); err != nil {
			t.Fatal(err)
		}
		installed := f.snapshot()
		_, err := mk(c.second, "0.2.0").Install()
		wantCode(t, err, cmdline.ProductVersion)
		if f.snapshot() != installed {
			t.Errorf("%+v: a refused downgrade changed something", c)
		}
	}
}

func TestExtraFileSetChanges(t *testing.T) {
	f := newFake()
	a := []Extra{{Path: "a.exe", Data: []byte("a")}, {Path: `old\deep\b.dll`, Data: []byte("b")}}
	b := []Extra{{Path: "a.exe", Data: []byte("a2")}, {Path: `new\c.dll`, Data: []byte("c")}}
	if _, err := newBundled(t, f, "0.2.0", a).Install(); err != nil {
		t.Fatal(err)
	}
	if _, err := newBundled(t, f, "0.3.0", b).Install(); err != nil {
		t.Fatal(err)
	}
	if f.files[dir+`\old\deep\b.dll`] != nil || f.dirs[dir+`\old\deep`] || f.dirs[dir+`\old`] {
		t.Error("a file the new payload does not carry, or its folders, stayed")
	}
	if string(f.files[dir+`\new\c.dll`]) != "c" || string(f.files[dir+`\a.exe`]) != "a2" {
		t.Error("new payload not installed")
	}
	// A folder that also holds a file of the user's stays.
	f.files[dir+`\new\notes.txt`] = []byte("mine")
	if _, err := newBundled(t, f, "0.3.0", a).Install(); err != nil {
		t.Fatal(err)
	}
	if f.files[dir+`\new\c.dll`] != nil || !f.dirs[dir+`\new`] || f.files[dir+`\new\notes.txt`] == nil {
		t.Error("the folder with the user's file was not kept as it is")
	}
}

func TestExtraInUse(t *testing.T) {
	f := newFake()
	s := newBundled(t, f, "0.2.0", gui("1"))
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	// Repair with the program running: an identical file is not touched, so no restart.
	f.inUse[dir+`\yakbd-gui.exe`] = true
	if r, err := s.Install(); err != nil || r.Reboot {
		t.Fatalf("repair of identical files: %+v %v", r, err)
	}
	// Upgrade: replaced at the restart.
	r, err := newBundled(t, f, "0.3.0", gui("2")).Install()
	if err != nil || !r.Reboot || !strings.Contains(strings.Join(f.atReboot, ";"), "move "+dir+`\yakbd-gui.exe.yakbd-new `+dir+`\yakbd-gui.exe`) {
		t.Fatalf("%+v %v %v", r, err, f.atReboot)
	}
	if !strings.Contains(strings.Join(r.Notes, " "), "a file installed with it") {
		t.Errorf("notes %q", r.Notes)
	}
	// Uninstall while it runs: the file, then the folder at the restart; the rest goes now.
	f.atReboot = nil
	f.inUse[dir+`\qml\qtquick\controls\a.qml`] = true
	r, err = newBundled(t, f, "0.3.0", gui("2")).Uninstall()
	if err != nil || !r.Reboot {
		t.Fatalf("%+v %v", r, err)
	}
	got := strings.Join(f.atReboot, ";")
	for _, w := range []string{"delete " + dir + `\yakbd-gui.exe`, "delete " + dir + `\qml\qtquick\controls\a.qml`, "delete " + dir + `\qml\qtquick\controls`, "delete " + dir + `\qml`, "delete " + dir} {
		if !strings.Contains(got, w+";") && !strings.HasSuffix(got, w) {
			t.Errorf("not deleted at the restart: %s (%s)", w, got)
		}
	}
	if strings.Index(got, "delete "+dir+`\qml\qtquick\controls\a.qml`) > strings.Index(got, "delete "+dir+`\qml;`) {
		t.Errorf("a folder before its file: %s", got)
	}
	if f.files[dir+`\yakbd-gui.exe.yakbd-new`] != nil {
		t.Error("the waiting replacement was left")
	}
}

func TestExtraRollback(t *testing.T) {
	f := newFake()
	before := f.snapshot()
	f.inUse[dir+`\qml\qtquick\controls\a.qml.yakbd-tmp`] = true // the write of the second file fails
	_, err := newBundled(t, f, "0.2.0", gui("1")).Install()
	if err == nil {
		t.Fatal("no error")
	}
	if f.snapshot() != before {
		t.Errorf("a failed fresh install left:\n%s", f.snapshot())
	}
}

func TestExtraPaths(t *testing.T) {
	for _, p := range []string{"", `a\`, `\a`, `a\\b`, `.\a`, `a\..\b`, "a/b", "c:x", `a\b.`, "a ", "a|b", "a*", "a\x01", "yakbd.exe", "SETUP.EXE",
		`yakbd.exe\x`, "a.dll.yakbd-tmp", "A.yakbd-NEW"} {
		if CheckExtraPath(p) == nil {
			t.Errorf("%q accepted", p)
		}
	}
	for _, p := range []string{"yakbd-gui.exe", `qml\QtQuick\a.qml`, "a.b.c", "setup.exe.txt", "x yakbd.exe"} {
		if err := CheckExtraPath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	builds := []string{BuildX64, BuildARM64}
	data := map[string][]byte{BuildX64: pe(payload.MachineAMD64, "x"), BuildARM64: pe(payload.MachineARM64, "a")}
	enc := func(xs ...Extra) error {
		_, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}}, builds, data, xs)
		return err
	}
	for name, xs := range map[string][]Extra{
		"same path, letter case": {{Path: "a.dll"}, {Path: "A.DLL"}},
		"every build and one":    {{Path: "a.dll"}, {Path: "a.dll", Build: BuildX64}},
		"file and folder":        {{Path: "a"}, {Path: `a\b`}},
		"file and folder, case":  {{Path: `A\b`, Build: BuildARM64}, {Path: "a"}},
		"build not carried":      {{Path: "a", Build: BuildX86}},
		"build unknown":          {{Path: "a", Build: "mips"}},
	} {
		if enc(xs...) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The same path once per build, and a folder a next to a file a.dll: accepted.
	if err := enc(Extra{Path: "a.dll", Build: BuildX64}, Extra{Path: "a.dll", Build: BuildARM64}, Extra{Path: `a\b`, Build: BuildX64}); err != nil {
		t.Error(err)
	}
}

func TestExtraDamage(t *testing.T) {
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}}, []string{BuildX64},
		map[string][]byte{BuildX64: pe(payload.MachineAMD64, "x")}, []Extra{{Path: "a.dll", Data: []byte("payload")}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(enc)
	if err != nil || len(p.ExtraData) != 1 || string(p.ExtraData[0]) != "payload" {
		t.Fatalf("%v %v", p, err)
	}
	enc[len(enc)-1] ^= 1
	if _, err := Decode(enc); err == nil {
		t.Error("damage not found")
	}
}

// TestExtraStaleInUse: an upgrade whose dropped file is in use deletes it at the restart but never schedules its
// folder, which holds a file the upgrade keeps; the next install is not refused for a pending folder deletion.
func TestExtraStaleInUse(t *testing.T) {
	f := newFake()
	a := []Extra{{Path: `lib\keep.dll`, Data: []byte("k")}, {Path: `lib\old.dll`, Data: []byte("o")}, {Path: `other\x.dll`, Data: []byte("x")}}
	b := []Extra{{Path: `lib\keep.dll`, Data: []byte("k")}, {Path: `other\x.dll`, Data: []byte("x")}}
	if _, err := newBundled(t, f, "0.2.0", a).Install(); err != nil {
		t.Fatal(err)
	}
	f.inUse[dir+`\lib\old.dll`] = true
	f.inUse[dir+`\other\x.dll`] = true
	r, err := newBundled(t, f, "0.3.0", b).Install()
	if err != nil || !r.Reboot {
		t.Fatalf("%+v %v", r, err)
	}
	if got := strings.Join(f.atReboot, ";"); got != "delete "+dir+`\lib\old.dll` {
		t.Errorf("at the restart: %s", got)
	}
	// The restart happened: the file is gone, nothing else waits.
	delete(f.inUse, dir+`\lib\old.dll`)
	delete(f.files, dir+`\lib\old.dll`)
	f.atReboot = nil
	if _, err := newBundled(t, f, "0.3.0", b).Install(); err != nil {
		t.Fatal(err)
	}
	if f.files[dir+`\lib\keep.dll`] == nil || !f.dirs[dir+`\lib`] {
		t.Error("the kept file or its folder is gone")
	}
}
