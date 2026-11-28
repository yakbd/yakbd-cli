package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/target/windows"
)

const (
	kbdtst  = "../../testdata/reference-spec/vm/kbdtst.klc"
	example = "../../testdata/kbl-spec/examples/latin-altgr-deadkeys.kbl.json"
	cldrPT  = "../../testdata/third_party/cldr-release-49/keyboards/3.0/pt-t-k0-abnt2.xml"
	xkbFO   = "../../testdata/third_party/xkeyboard-config-2.49/symbols/fo"
	xkbDemo = "../../testdata/kbl-spec/examples/xkb-caps-deadkeys.kbl.json"
)

func run(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := Run(args, &o, &e)
	return code, o.String(), e.String()
}

func TestCommands(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
		code int
		out  string // substring of stdout
		err  string // substring of stderr
	}{
		{"no args", nil, ExitUsage, "", "usage"},
		{"unknown", []string{"frob"}, ExitUsage, "", "unknown command"},
		{"version", []string{"version"}, ExitOK, "yakbd 0.1.0-preview.3", ""},
		{"validate klc", []string{"validate", kbdtst}, ExitOK, "valid", "default(s) used"},
		{"validate kbl", []string{"validate", example}, ExitOK, "valid", ""},
		{"validate strict", []string{"validate", "-windows", "-strict-defaults", example}, ExitError, "", "error (defaulted)"},
		{"validate bad klc", []string{"validate", "../../testdata/reference-spec/klc-format/w-vk-unknown.klc"}, ExitError, "", "klc.vk.unknown"},
		{"validate cldr", []string{"validate", cldrPT}, ExitOK, "valid", ""},
		{"validate cldr for windows needs pins", []string{"validate", "-windows", cldrPT}, ExitError, "", "windows.langIdMissing"},
		{"compile klc", []string{"compile", "-o", dir, "-emit-expectations", kbdtst}, ExitOK, "kbdtst.expect.txt", "builds x86, x64, wow64, arm64x"},
		{"compile kbl", []string{"compile", "-o", filepath.Join(dir, "kbl"), example}, ExitOK, "x64", ""},
		{"compile one", []string{"compile", "-o", filepath.Join(dir, "one"), "-target", "wow64", kbdtst}, ExitOK, "SysWOW64", ""},
		{"compile arm64x only", []string{"compile", "-o", dir, "-target", "arm64x,arm64", kbdtst}, ExitOK, "arm64x/kbdtst.dll", "plain arm64 build is a test control"},
		{"compile bad target", []string{"compile", "-target", "mips", kbdtst}, ExitUsage, "", "unknown target \"mips\" (known: macos, windows, xkb"},
		{"compile bad build in a legacy list", []string{"compile", "-target", "x64,mips", kbdtst}, ExitUsage, "", "unknown build \"mips\""},
		{"compile -target windows -build", []string{"compile", "-o", filepath.Join(dir, "two"), "-target", "windows", "-build", "wow64", kbdtst}, ExitOK, "SysWOW64", ""},
		{"compile -target=wow64", []string{"compile", "-o", filepath.Join(dir, "three"), "-target=wow64", kbdtst}, ExitOK, "SysWOW64", ""},
		{"installer unknown target", []string{"installer", "-target", "amiga", kbdtst}, ExitUsage, "", "unknown target \"amiga\" (known: macos, windows, xkb)"},
		{"pin unknown target", []string{"pin", "-target=amiga", example, filepath.Join(dir, "p2.kbl.json")}, ExitUsage, "", "unknown target"},
		{"validate -target windows", []string{"validate", "-target", "windows", "-strict-defaults", example}, ExitError, "", "error (defaulted)"},
		{"validate -windows and an unknown -target", []string{"validate", "-windows", "-target", "amiga", example}, ExitUsage, "", "unknown target"},
		{"convert klc to kbl", []string{"convert", kbdtst, filepath.Join(dir, "k.kbl.json")}, ExitOK, "", "klc.impliedRows"},
		{"convert kbl to klc", []string{"convert", filepath.Join(dir, "k.kbl.json"), filepath.Join(dir, "k.klc")}, ExitOK, "", ""},
		{"convert cldr to kbl", []string{"convert", cldrPT, filepath.Join(dir, "pt.kbl.json")}, ExitOK, "", "general.importInlined"},
		{"compile -target xkb", []string{"compile", "-target", "xkb", "-o", filepath.Join(dir, "x"), "-emit-expectations", xkbDemo}, ExitOK, "xkb/types/yakbd-fo-xkbdemo-types", "xkb.customKeyType"},
		{"installer -target xkb", []string{"installer", "-target", "xkb", "-o", filepath.Join(dir, "x.tar.gz"), xkbDemo}, ExitOK, "install.sh (--uninstall)", ""},
		{"pin -target xkb", []string{"pin", "-target", "xkb", xkbDemo, filepath.Join(dir, "xp.kbl.json")}, ExitOK, "pinned", "xkbDefault.variant"},
		{"validate -target xkb strict", []string{"validate", "-target", "xkb", "-strict-defaults", xkbDemo}, ExitError, "", "error (defaulted)"},
		{"validate -target xkb pinned", []string{"validate", "-target", "xkb", "-strict-defaults", filepath.Join(dir, "xp.kbl.json")}, ExitOK, "valid", ""},
		{"convert kbl to xkb", []string{"convert", "-to", "xkb", xkbDemo, filepath.Join(dir, "demo-symbols")}, ExitOK, "demo-symbols.XCompose", "xkb.deadKeyStacks"},
		{"convert xkb back to kbl", []string{"convert", "-from", "xkb", "-compose", filepath.Join(dir, "demo-symbols.XCompose"), filepath.Join(dir, "demo-symbols"), filepath.Join(dir, "demo.kbl.json")}, ExitOK, "", "xkb.syntaxDetailsIgnored"},
		{"convert xkb to kbl", []string{"convert", "-from", "xkb", "-variant", "nodeadkeys", xkbFO, filepath.Join(dir, "fo.kbl.json")}, ExitOK, "", "include latin(type2) inlined"},
		{"convert xkb with compose", []string{"convert", "-from", "xkb", "-compose", "../../testdata/xkb/test.XCompose", xkbFO, filepath.Join(dir, "fo2.kbl.json")}, ExitOK, "", "xkb.composeSequenceDropped"},
		{"convert xkb unknown variant", []string{"convert", "-from", "xkb", "-variant", "nope", xkbFO, filepath.Join(dir, "fo3.kbl.json")}, ExitError, "", "io.variant"},
		{"convert unknown -from", []string{"convert", "-from", "amiga", xkbFO, filepath.Join(dir, "fo4.kbl.json")}, ExitUsage, "", "unknown format \"amiga\""},
		{"convert xkb without -from", []string{"convert", xkbFO, filepath.Join(dir, "fo5.kbl.json")}, ExitError, "", "io.format"},
		{"convert kbl to cldr", []string{"convert", filepath.Join(dir, "pt.kbl.json"), filepath.Join(dir, "pt.xml")}, ExitOK, "", ""},
		{"convert unknown", []string{"convert", kbdtst, filepath.Join(dir, "x.txt")}, ExitUsage, "", "unknown output format"},
		{"pin", []string{"pin", example, filepath.Join(dir, "pinned.kbl.json")}, ExitOK, "pinned", ""},
		{"pinned is explicit", []string{"validate", "-windows", "-strict-defaults", filepath.Join(dir, "pinned.kbl.json")}, ExitOK, "valid", ""},
		{"pin wrong output", []string{"pin", example, filepath.Join(dir, "x.klc")}, ExitUsage, "", "must be a .kbl.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := run(tt.args...)
			if code != tt.code || !strings.Contains(out, tt.out) || !strings.Contains(errOut, tt.err) {
				t.Errorf("exit %d (want %d)\nstdout %s\nstderr %s", code, tt.code, out, errOut)
			}
		})
	}
	for _, a := range []string{"x64", "wow64", "x86"} {
		b, err := os.ReadFile(filepath.Join(dir, a, "kbdtst.dll"))
		if err != nil {
			t.Fatal(err)
		}
		if _, arch, err := windows.Read(b); err != nil {
			t.Errorf("%s: %v %v", a, arch, err)
		}
	}
	a, _ := os.ReadFile(filepath.Join(dir, "k.kbl.json"))
	if _, r := kbljson.Read(a, "k"); r.HasErrors() {
		t.Errorf("converted file invalid: %s", r.Summary())
	}
}

// TestKLCAndKBLBuildTheSameDLL: a .klc file and its .kbl.json conversion build byte-identical DLLs.
func TestKLCAndKBLBuildTheSameDLL(t *testing.T) {
	dir := t.TempDir()
	if code, _, e := run("convert", kbdtst, filepath.Join(dir, "k.kbl.json")); code != ExitOK {
		t.Fatal(e)
	}
	if code, _, e := run("compile", "-o", filepath.Join(dir, "a"), kbdtst); code != ExitOK {
		t.Fatal(e)
	}
	if code, _, e := run("compile", "-o", filepath.Join(dir, "b"), filepath.Join(dir, "k.kbl.json")); code != ExitOK {
		t.Fatal(e)
	}
	for _, a := range []string{"x64", "wow64", "x86"} {
		x, _ := os.ReadFile(filepath.Join(dir, "a", a, "kbdtst.dll"))
		y, _ := os.ReadFile(filepath.Join(dir, "b", a, "kbdtst.dll"))
		if len(x) == 0 || !bytes.Equal(x, y) {
			t.Errorf("%s: the two DLLs differ", a)
		}
	}
}

func TestJSONReport(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "r.json")
	if code, _, e := run("compile", "-o", dir, "-report", rp, kbdtst); code != ExitOK {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(rp)
	var r struct {
		Entries []struct{ Class, Severity, Kind string }
	}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, e := range r.Entries {
		kinds[e.Class] = true
		if e.Class == "defaulted" && !strings.HasPrefix(e.Kind, "windowsDefault.") {
			t.Errorf("defaulted kind %s", e.Kind)
		}
	}
	if !kinds["defaulted"] || !kinds["loss"] {
		t.Errorf("classes %v", kinds)
	}
}

func TestExamplesValidate(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/kbl-spec/examples/*.kbl.json")
	for _, f := range files {
		if code, _, e := run("validate", f); code != ExitOK {
			t.Errorf("%s: %s", f, e)
		}
	}
}

// TestCorpusThroughCLI converts every parseable corpus file .klc -> .kbl.json -> .klc with the command line.
func TestCorpusThroughCLI(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/reference-spec/*/*.klc")
	dir := t.TempDir()
	n := 0
	for _, p := range files {
		if code, _, _ := run("validate", p); code != ExitOK {
			continue // a file with errors of its own (the w-*.klc inputs)
		}
		base := strings.TrimSuffix(filepath.Base(p), ".klc")
		k := filepath.Join(dir, base+".kbl.json")
		if code, _, e := run("convert", p, k); code != ExitOK {
			t.Errorf("%s -> kbl: %s", p, e)
			continue
		}
		code, _, e := run("convert", k, filepath.Join(dir, base+".klc"))
		if code != ExitOK && !strings.Contains(e, "windows.sgcapsDeadKey") {
			t.Errorf("%s -> klc: %s", p, e)
		}
		n++
	}
	if n < 20 {
		t.Errorf("only %d files", n)
	}
}

func TestCompileLangIDAndAccept(t *testing.T) {
	dir := t.TempDir()
	base := "../../testdata/reference-spec/dll-format/base.klc"
	if code, _, e := run("compile", "-o", dir, base); code != ExitError || !strings.Contains(e, "windows.langIdMissing") {
		t.Fatalf("without LANGID: %d %s", code, e)
	}
	if code, _, e := run("compile", "-o", dir, "-langid", "0409", base); code != ExitOK {
		t.Fatal(e)
	}
	if code, _, e := run("compile", "-o", filepath.Join(dir, "pt"), "-langid", "0416", "-accept", "windows.vkMissing", cldrPT); code != ExitOK {
		t.Errorf("CLDR with -langid and -accept: %s", e)
	}
}

func TestInstallerCommand(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "k.exe")
	if code, so, se := run("installer", "-q", "-o", out, kbdtst); code != ExitOK || !strings.Contains(so, "for x64, arm64, x86 Windows") || !strings.Contains(so, "files x64, arm64x, wow64, x86") {
		t.Fatalf("%d %s %s", code, so, se)
	}
	if b, err := os.ReadFile(out); err != nil || len(b) < len(installer.Stub()) {
		t.Fatalf("output: %v", err)
	}
	if code, so, _ := run("installer", "-q", "-build", "x64", "-enable", "false", "-o", out, kbdtst); code != ExitOK || !strings.Contains(so, "files x64, wow64\t") {
		t.Errorf("x64 only: %s", so)
	}
	if code, so, se := run("installer", "-q", "-build", "arm64", "-o", out, kbdtst); code != ExitOK || !strings.Contains(so, "files arm64x, wow64\t") {
		t.Errorf("arm64: %d %s %s", code, so, se)
	}
	if code, _, se := run("installer", "-q", "-signin", "true", "-o", out, kbdtst); code != ExitOK {
		t.Errorf("-signin true: %d %s", code, se)
	} else if b, _ := os.ReadFile(out); !bytes.Contains(b, []byte(`"signIn": true`)) {
		t.Error("-signin true: the manifest has no signIn")
	}
	if code, _, _ := run("installer", "-signin", "yes", kbdtst); code != ExitUsage {
		t.Error("-signin yes accepted")
	}
	if code, _, _ := run("installer", "-build", "mips", kbdtst); code != ExitUsage {
		t.Error("unknown machine kind")
	}
	pinned := filepath.Join(dir, "p.kbl.json")
	if code, _, se := run("pin", "-q", example, pinned); code != ExitOK {
		t.Fatal(se)
	}
	if code, _, se := run("installer", "-q", "-strict-defaults", "-o", out, pinned); code != ExitOK || strings.Contains(se, "windowsDefault") {
		t.Errorf("strict defaults: %d %s", code, se)
	}
}
