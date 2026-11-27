package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
	"github.com/yakbd/yakbd-cli/internal/relbuild"
)

// TestWrite builds yakbd-setup.exe (three yakbd builds and the program; skipped with -short) and reads its payload
// back: every build, the version, the product code.
func TestWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("builds four programs")
	}
	t.Chdir("../..")
	b, err := Write("9.8.7.6")
	if err != nil {
		t.Fatal(err)
	}
	p, err := toolsetup.FromExe(b)
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest
	if m.Product.Version != "9.8.7.6" || m.Product.Code != installer.ToolProductCode() || len(p.Data) != 3 {
		t.Errorf("%+v, %d builds", m.Product, len(p.Data))
	}
}

// TestReadExtra: -extra reads every regular file below the folder, in name order, with backslash paths, for every
// build or for one build.
func TestReadExtra(t *testing.T) {
	d := t.TempDir()
	for p, c := range map[string]string{"b.exe": "b", "qml/x/a.qml": "a", "a.dll": "d"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(d, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	xs, err := readExtra([]string{d, "arm64=" + filepath.Join(d, "qml")})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range xs {
		got = append(got, x.Build+":"+x.Path+":"+string(x.Data))
	}
	if want := []string{":a.dll:d", ":b.exe:b", `:qml\x\a.qml:a`, `arm64:x\a.qml:a`}; !reflect.DeepEqual(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	if err := os.Symlink("b.exe", filepath.Join(d, "link")); err == nil {
		if _, err := readExtra([]string{d}); err == nil {
			t.Error("a link was accepted")
		}
	}
}

// TestSetupWithExtra builds an installer of two yakbd.exe builds with an extra payload (skipped with -short) and
// reads it back.
func TestSetupWithExtra(t *testing.T) {
	if testing.Short() {
		t.Skip("builds three programs")
	}
	t.Chdir("../..")
	b, exes, err := relbuild.SetupWith(".", relbuild.SetupOptions{Version: "9.8.7", Builds: []string{"x64", "arm64"},
		Extra: []toolsetup.Extra{{Path: "yakbd-gui.exe", Data: []byte("gui")}, {Path: `plugins\p.dll`, Build: "arm64", Data: []byte("p")}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := toolsetup.FromExe(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Data) != 2 || !bytes.Equal(p.Data["arm64"], exes["arm64"]) || len(p.ExtraFor("x64")) != 1 || len(p.ExtraFor("arm64")) != 2 {
		t.Errorf("builds %v, extra %+v", p.Manifest.Builds(), p.Manifest.Extra)
	}
	if _, _, err := relbuild.SetupWith(".", relbuild.SetupOptions{Version: "1", Builds: []string{"mips"}}); err == nil {
		t.Error("unknown build accepted")
	}
}

// TestReadShortcuts: -shortcut NAME=PATH, slashes turned into backslashes; a value without both parts refused.
func TestReadShortcuts(t *testing.T) {
	got, err := readShortcuts([]string{"YAKBD Editor=yakbd-gui.exe", "Viewer=tools/viewer.exe"})
	want := []toolsetup.Shortcut{{Name: "YAKBD Editor", Target: "yakbd-gui.exe"}, {Name: "Viewer", Target: `tools\viewer.exe`}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []string{"x", "=a.exe", "a="} {
		if _, err := readShortcuts([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
