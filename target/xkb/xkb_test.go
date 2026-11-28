package xkb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
)

const demo = "../../testdata/kbl-spec/examples/xkb-caps-deadkeys.kbl.json"

func read(t *testing.T, p string) *model.Layout {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	l, r := kbljson.Read(b, p)
	if l == nil || r.HasErrors() {
		t.Fatal(r.Summary())
	}
	return l
}

func built(t *testing.T, l *model.Layout) *build.Result {
	t.Helper()
	res, r := build.Build(l, build.Options{})
	if res == nil {
		t.Fatal(r.Summary())
	}
	return res
}

// TestDemo: the spec example builds the files of targets/xkb.md §5, with the yakbd type of its §8 case (1).
func TestDemo(t *testing.T) {
	res := built(t, read(t, demo))
	var names []string
	for _, f := range Files(res) {
		names = append(names, f.Path)
	}
	want := "xkb/symbols/yakbd-fo-xkbdemo xkb/rules/evdev.xml xkb/types/yakbd-fo-xkbdemo-types xkb/rules/evdev.post compose/yakbd-fo-xkbdemo.XCompose"
	if strings.Join(names, " ") != want {
		t.Errorf("files %v", names)
	}
	for _, s := range []string{`key <AD05> { type[Group1] = "YAKBD_12213434", symbols[Group1] = [ t, T, thorn, THORN ] };`,
		`key <RALT> { type[Group1] = "ONE_LEVEL", symbols[Group1] = [ ISO_Level3_Shift ] };`, "default partial alphanumeric_keys\nxkb_symbols \"basic\" {"} {
		if !strings.Contains(string(res.SymbolsText), s) {
			t.Errorf("symbols file lacks %q:\n%s", s, res.SymbolsText)
		}
	}
	if !strings.Contains(string(res.Types), "map[Lock+LevelThree] = Level3;") || !strings.HasPrefix(string(res.Rules), "! layout[any] = types\n  yakbd-fo-xkbdemo = +yakbd-fo-xkbdemo-types\n") {
		t.Errorf("types:\n%s\nrules:\n%s", res.Types, res.Rules)
	}
	if !strings.Contains(string(res.ComposeText), "include \"%L\"\n<dead_acute> <a> : \"á\"\n") {
		t.Errorf("compose:\n%s", res.ComposeText)
	}
	a1, _ := Archive(res)
	a2, _ := Archive(built(t, read(t, demo)))
	if !bytes.Equal(a1, a2) {
		t.Error("the archive is not deterministic")
	}
	if len(Expectations(read(t, demo), res, 0)) < 20 {
		t.Error("expectations")
	}
}

// TestPin: a pinned file builds with no default rule and gives the same files.
func TestPin(t *testing.T) {
	for _, p := range []string{demo, "../../testdata/kbl-spec/examples/latin-altgr-deadkeys.kbl.json", "../../testdata/kbl-spec/examples/minimal-qwerty.kbl.json"} {
		l := read(t, p)
		res := built(t, l)
		pinned, _ := build.Pin(l, build.Options{})
		data, wr := kbljson.Write(pinned)
		if data == nil || wr.HasErrors() {
			t.Fatalf("%s: %s", p, wr.Summary())
		}
		back, _ := kbljson.Read(data, "pinned")
		res2, r2 := build.Build(back, build.Options{})
		if res2 == nil {
			t.Fatalf("%s: %s", p, r2.Summary())
		}
		for _, e := range r2.Entries {
			if strings.HasPrefix(e.Code, "xkbDefault.") {
				t.Errorf("%s: after pin: %s", p, e.String())
			}
		}
		if string(res.SymbolsText) != string(res2.SymbolsText) || string(res.ComposeText) != string(res2.ComposeText) || string(res.Registry) != string(res2.Registry) {
			t.Errorf("%s: pinned build differs", p)
		}
	}
}

// TestInstallScript runs install.sh in a temporary home: a fresh install and uninstall, and an install next to the
// user's own registry and Compose file, which uninstall leaves as they were.
func TestInstallScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("no awk")
	}
	data, err := Archive(built(t, read(t, demo)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	extract(t, data, dir)
	script := filepath.Join(dir, "yakbd-fo-xkbdemo", "install.sh")
	run := func(home string, args ...string) string {
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install.sh %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	files := func(home string) string {
		var fs []string
		filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				fs = append(fs, strings.TrimPrefix(p, home+"/"))
			}
			return nil
		})
		return strings.Join(fs, " ")
	}
	home := filepath.Join(dir, "h1")
	os.MkdirAll(home, 0o755)
	if out := run(home); !strings.Contains(out, "gsettings set org.gnome.desktop.input-sources") || !strings.Contains(out, "xkb_layout yakbd-fo-xkbdemo") {
		t.Errorf("output:\n%s", out)
	}
	run(home) // again: blocks are replaced, not added
	x, _ := os.ReadFile(filepath.Join(home, ".XCompose"))
	if strings.Count(string(x), "# yakbd yakbd-fo-xkbdemo begin") != 1 || !strings.Contains(string(x), "include \"%L\"") {
		t.Errorf(".XCompose:\n%s", x)
	}
	run(home, "--uninstall")
	if got := files(home); got != ".config/xkb/rules/evdev.post .config/xkb/rules/evdev.xml" {
		t.Errorf("after uninstall: %s", got)
	}

	home = filepath.Join(dir, "h2")
	os.MkdirAll(filepath.Join(home, ".config", "xkb", "rules"), 0o755)
	reg := "<?xml version=\"1.0\"?>\n<xkbConfigRegistry version=\"1.1\">\n  <layoutList>\n    <layout><configItem><name>mine</name></configItem></layout>\n  </layoutList>\n</xkbConfigRegistry>\n"
	comp := "include \"%L\"\n<Multi_key> <x> <x> : \"×\"\n"
	os.WriteFile(filepath.Join(home, ".config", "xkb", "rules", "evdev.xml"), []byte(reg), 0o644)
	os.WriteFile(filepath.Join(home, ".config", "XCompose"), []byte(comp), 0o644)
	run(home)
	if _, err := os.Stat(filepath.Join(home, ".XCompose")); err == nil {
		t.Error("~/.XCompose written although ~/.config/XCompose exists")
	}
	c, _ := os.ReadFile(filepath.Join(home, ".config", "XCompose"))
	if !strings.HasPrefix(string(c), comp) || !strings.Contains(string(c), "<dead_acute> <a> : \"á\"") || strings.Count(string(c), "include") != 1 {
		t.Errorf("~/.config/XCompose:\n%s", c)
	}
	run(home, "--uninstall")
	c, _ = os.ReadFile(filepath.Join(home, ".config", "XCompose"))
	r, _ := os.ReadFile(filepath.Join(home, ".config", "xkb", "rules", "evdev.xml"))
	if string(c) != comp || string(r) != reg {
		t.Errorf("after uninstall:\n%s\n%s", c, r)
	}
}

func extract(t *testing.T, data []byte, dir string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, filepath.FromSlash(h.Name))
		if h.Typeflag == tar.TypeDir {
			os.MkdirAll(p, 0o755)
			continue
		}
		b, _ := io.ReadAll(tr)
		os.WriteFile(p, b, os.FileMode(h.Mode))
	}
}
