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
	"github.com/yakbd/yakbd-cli/format/xkb"
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
	// GTK 4 does not read ~/.config/XCompose (measured, kbl D148): its target is a new ~/.XCompose
	if x, _ := os.ReadFile(filepath.Join(home, ".XCompose")); !strings.HasPrefix(string(x), "# Created by yakbd\ninclude \"%L\"\n") || !strings.Contains(string(x), "<dead_acute> <a> : \"á\"") {
		t.Errorf("~/.XCompose next to ~/.config/XCompose:\n%s", x)
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
	if _, err := os.Stat(filepath.Join(home, ".XCompose")); err == nil {
		t.Error("~/.XCompose left after uninstall")
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

// TestCheckCases: every generated case of tools/xkb-check builds, gives expectations, and presses only keys
// keycodes.tsv knows.
func TestCheckCases(t *testing.T) {
	kc, err := os.ReadFile("../../tools/xkb-check/keycodes.tsv")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, line := range strings.Split(string(kc), "\n") {
		if f := strings.Fields(line); len(f) == 2 && !strings.HasPrefix(line, "#") {
			known[f[0]] = true
		}
	}
	cases, _ := filepath.Glob("../../tools/xkb-check/gen/*/layout.kbl.json")
	if len(cases) < 3 {
		t.Fatalf("cases: %v", cases)
	}
	for _, c := range cases {
		l := read(t, c)
		res := built(t, l)
		es := Expectations(l, res, 40)
		if len(es) < 10 {
			t.Errorf("%s: %d expectations", c, len(es))
		}
		for _, e := range es {
			for _, k := range strings.Fields(e.Keys) {
				n := strings.Trim(k, "+-<>")
				if !known[n] {
					t.Errorf("%s %s: key %s not in keycodes.tsv", c, e.Name, n)
				}
			}
		}
	}
}

// TestX11Section: a build with levels of several keysyms adds the section basic-x11 with those levels VoidSymbol, and
// the X11 instructions load it (targets/xkb.md §3, D143, D146); a build without such levels has no X11 section.
func TestX11Section(t *testing.T) {
	if res := built(t, read(t, demo)); res.X11Section != "" || len(res.Symbols.Sections) != 1 {
		t.Errorf("demo: X11 section %q", res.X11Section)
	}
	res := built(t, read(t, "../../tools/xkb-check/gen/gen-multi/layout.kbl.json"))
	if res.X11Section != "basic-x11" || len(res.Symbols.Sections) != 2 {
		t.Fatalf("X11 section %q, %d sections", res.X11Section, len(res.Symbols.Sections))
	}
	back, r := xkb.Read(res.SymbolsText, "symbols")
	if back == nil || r.HasErrors() {
		t.Fatal(r.Summary())
	}
	first, x11 := back.Sections[0], back.Sections[1]
	if x11.Name != "basic-x11" || len(x11.Flags) == 0 || x11.Flags[0] == "default" || len(first.Stmts) != len(x11.Stmts) {
		t.Fatalf("sections %q %v", x11.Name, x11.Flags)
	}
	multi := 0
	for i, st := range first.Stmts {
		if st.Kind != xkb.KeyDef {
			continue
		}
		for li, l := range st.Key.Groups[0].Levels {
			xl := x11.Stmts[i].Key.Groups[0].Levels
			switch {
			case len(l) > 1:
				multi++
				if li >= len(xl) || len(xl[li]) != 1 || xl[li][0].K != xkb.VoidSymbol {
					t.Errorf("<%s> level %d in the X11 section: %v", st.Key.Name, li+1, xl[li])
				}
			case li >= len(xl) || len(xl[li]) != len(l) || len(l) == 1 && xl[li][0] != l[0]:
				t.Errorf("<%s> level %d changed in the X11 section", st.Key.Name, li+1)
			}
		}
	}
	if multi == 0 {
		t.Error("the first section has no level of several keysyms")
	}
	sel := strings.Join(Selection(res), "\n")
	if !strings.Contains(sel, "-layout yakbd-gen-multi -variant basic-x11 -print") || !strings.Contains(string(Readme(res)), "basic-x11") {
		t.Errorf("selection:\n%s", sel)
	}
}

// TestTwoVariants installs two variants of one layout side by side in a temporary home and removes them one by one
// (targets/xkb.md §5, D144): each keeps its own section in the shared symbols file, its registry entry, rules,
// types and Compose blocks; removing one leaves the other whole; removing both leaves the home as before.
func TestTwoVariants(t *testing.T) {
	for _, tool := range []string{"sh", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	dir := t.TempDir()
	scripts := map[string]string{}
	for _, v := range []string{"one", "two"} {
		l := read(t, demo)
		l.Targets.XKB.Variant = v
		res := built(t, l)
		if BlockName(res) != "yakbd-fo-xkbdemo("+v+")" {
			t.Fatalf("block name %q", BlockName(res))
		}
		data, err := Archive(res)
		if err != nil {
			t.Fatal(err)
		}
		extract(t, data, dir)
		scripts[v] = filepath.Join(dir, "yakbd-fo-xkbdemo-"+v, "install.sh")
	}
	home := filepath.Join(dir, "home")
	os.MkdirAll(home, 0o755)
	run := func(v string, args ...string) {
		cmd := exec.Command("sh", append([]string{scripts[v]}, args...)...)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s install.sh %v: %v\n%s", v, args, err, out)
		}
	}
	cat := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(home, p))
		return string(b)
	}
	has := func(v string) bool {
		t.Helper()
		sym, reg, post, comp := cat(".config/xkb/symbols/yakbd-fo-xkbdemo"), cat(".config/xkb/rules/evdev.xml"), cat(".config/xkb/rules/evdev.post"), cat(".XCompose")
		_, typesErr := os.Stat(filepath.Join(home, ".config/xkb/types/yakbd-fo-xkbdemo-"+v+"-types"))
		parts := []bool{
			strings.Contains(sym, `xkb_symbols "`+v+`"`), strings.Contains(sym, "// yakbd yakbd-fo-xkbdemo("+v+") begin"),
			strings.Contains(reg, "<name>"+v+"</name>"), strings.Contains(post, "yakbd-fo-xkbdemo "+v+" = +yakbd-fo-xkbdemo-"+v+"-types"),
			typesErr == nil, strings.Contains(comp, "# yakbd yakbd-fo-xkbdemo("+v+") begin"),
		}
		n := 0
		for _, p := range parts {
			if p {
				n++
			}
		}
		if n != 0 && n != len(parts) {
			t.Errorf("variant %s is installed in part: %v", v, parts)
		}
		return n == len(parts)
	}
	run("one")
	run("two")
	run("one") // again: its blocks are replaced, not added
	if !has("one") || !has("two") || strings.Count(cat(".config/xkb/symbols/yakbd-fo-xkbdemo"), `xkb_symbols "one"`) != 1 {
		t.Fatalf("both installed:\n%s", cat(".config/xkb/symbols/yakbd-fo-xkbdemo"))
	}
	f, r := xkb.Read([]byte(cat(".config/xkb/symbols/yakbd-fo-xkbdemo")), "symbols")
	if f == nil || r.HasErrors() || len(f.Sections) != 2 {
		t.Fatalf("the shared symbols file does not read as two sections: %s", r.Summary())
	}
	run("one", "--uninstall")
	if has("one") || !has("two") {
		t.Error("after removing one: two must stay whole and one be gone")
	}
	run("two", "--uninstall")
	if has("two") {
		t.Error("after removing two")
	}
	var fs []string
	filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			fs = append(fs, strings.TrimPrefix(p, home+"/"))
		}
		return nil
	})
	if got := strings.Join(fs, " "); got != ".config/xkb/rules/evdev.post .config/xkb/rules/evdev.xml" {
		t.Errorf("after removing both: %s", got)
	}
	if strings.Contains(cat(".config/xkb/rules/evdev.xml"), "yakbd") || strings.TrimSpace(cat(".config/xkb/rules/evdev.post")) != "" {
		t.Errorf("registry or rules not clean:\n%s\n%s", cat(".config/xkb/rules/evdev.xml"), cat(".config/xkb/rules/evdev.post"))
	}
}
