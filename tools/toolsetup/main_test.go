package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
	"github.com/yakbd/yakbd-cli/internal/relbuild"
)

// TestWrite builds yakbd-cli-setup.exe (three yakbd builds and the program; skipped with -short) and reads its
// payload back.
func TestWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("builds four programs")
	}
	t.Chdir("../..")
	b, err := Write("9.8.7")
	if err != nil {
		t.Fatal(err)
	}
	p, err := toolsetup.FromExe(b)
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest
	if m.Product.Code != installer.SuiteProductCode() || len(m.Components) != 1 || m.Components[0].Version != "9.8.7" || !m.AddToPath {
		t.Errorf("%+v", m)
	}
	if fs, err := p.FilesFor(toolsetup.CLI, "x64"); err != nil || len(fs) != 3 {
		t.Errorf("CLI files for x64: %d %v (yakbd.exe, LICENSE, NOTICE)", len(fs), err)
	}
}

// TestEditor: -editor and -editor-json make the editor component, its shortcut and settings; the declaration is
// required and strict.
func TestEditor(t *testing.T) {
	d := t.TempDir()
	for p, c := range map[string]string{"x64/yakbd-gui.exe": "g", "x64/qt/Qt6Core.dll": "q", "arm64/yakbd-gui.exe": "a"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(d, p)), 0o755)
		_ = os.WriteFile(filepath.Join(d, p), []byte(c), 0o644)
	}
	js := filepath.Join(d, "editor.json")
	_ = os.WriteFile(js, []byte(`{"version":"0.1.0-preview.3","shortcuts":[{"name":"YAKBD Editor","target":"yakbd-gui.exe"}],
"userData":[{"component":"Editor","kind":"key","path":"HKCU\\Software\\YAKBD\\YAKBD Editor","parentIfEmpty":true}]}`), 0o644)
	e, err := readEditor([]string{"x64=" + filepath.Join(d, "x64"), "arm64=" + filepath.Join(d, "arm64")}, js)
	if err != nil {
		t.Fatal(err)
	}
	if e.Version != "0.1.0-preview.3" || len(e.Files["x64"]) != 2 || e.Files["x64"][0].Path != `qt\Qt6Core.dll` || len(e.Shortcuts) != 1 || len(e.UserData) != 1 {
		t.Errorf("%+v", e)
	}
	if _, err := readEditor([]string{"x64=" + d}, ""); err == nil {
		t.Error("-editor without a declaration accepted")
	}
	_ = os.WriteFile(js, []byte(`{"version":"1.0.0","extra":1}`), 0o644)
	if _, err := readEditor([]string{"x64=" + d}, js); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := readEditor([]string{"mips=" + d}, js); err == nil {
		t.Error("unknown build accepted")
	}
}

// TestGUISetup builds yakbd-gui-setup.exe with two yakbd.exe builds (skipped with -short) and reads it back.
func TestGUISetup(t *testing.T) {
	if testing.Short() {
		t.Skip("builds three programs")
	}
	t.Chdir("../..")
	e := &relbuild.EditorInput{Version: "9.8.7", Files: map[string][]toolsetup.Input{"x64": {{Path: "yakbd-gui.exe", Data: []byte("g")}}},
		Shortcuts: []toolsetup.Shortcut{{Name: "YAKBD Editor", Target: "yakbd-gui.exe"}}}
	b, _, err := relbuild.SetupWith(".", relbuild.SetupOptions{Version: "9.8.6", Builds: []string{"x64", "arm64"}, Editor: e})
	if err != nil {
		t.Fatal(err)
	}
	p, err := toolsetup.FromExe(b)
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest
	if m.Version() != "9.8.7" || m.AddToPath || !p.Supports(toolsetup.Editor, "x64") || p.Supports(toolsetup.Editor, "arm64") || len(m.Shortcuts) != 1 {
		t.Errorf("%+v", m)
	}
}
