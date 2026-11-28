package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/target"
	"github.com/yakbd/yakbd-cli/target/registry"
)

// fakeTarget has only compile, with a flag of its own; it writes one file.
type fakeTarget struct{}

func (fakeTarget) Name() string         { return "fake" }
func (fakeTarget) DefaultsKind() string { return "fakeDefault" }
func (fakeTarget) Command(name string, fs *flag.FlagSet) target.Command {
	if name != "compile" {
		return nil
	}
	c := &fakeCompile{}
	c.out = fs.String("o", "", "output file")
	c.word = fs.String("word", "", "the file's content")
	return c
}

type fakeCompile struct{ out, word *string }

func (c *fakeCompile) Check() error { return nil }
func (c *fakeCompile) Run(j *target.Job) *target.Result {
	if j.Policy == nil || j.Policy.Kinds["fakeDefault.*"] != "error" {
		return &target.Result{Fail: "no -strict-defaults policy"}
	}
	return &target.Result{Files: []target.File{{Path: *c.out, Data: []byte(*c.word), Line: "wrote " + *c.out}}}
}

func TestTargetInterface(t *testing.T) {
	saved := registry.Targets
	registry.Targets = append(append([]target.Target(nil), saved...), fakeTarget{})
	defer func() { registry.Targets = saved }()
	dir := t.TempDir()
	out := filepath.Join(dir, "x.txt")
	code, so, se := run("compile", "-target", "fake", "-strict-defaults", "-word", "hello", "-o", out, example)
	if code != ExitOK || !strings.Contains(so, "wrote "+out) {
		t.Fatalf("%d %s %s", code, so, se)
	}
	if b, _ := os.ReadFile(out); string(b) != "hello" {
		t.Errorf("file %q", b)
	}
	if code, _, se := run("installer", "-target", "fake", example); code != ExitUsage || !strings.Contains(se, "the target fake has no installer command") {
		t.Errorf("installer: %d %s", code, se)
	}
	if code, _, se := run("compile", "-target", "amiga", example); code != ExitUsage || !strings.Contains(se, "known: fake, macos, windows") {
		t.Errorf("unknown: %d %s", code, se)
	}
	if code, _, se := run("validate", "-windows", "-target", "fake", example); code != ExitUsage || !strings.Contains(se, "-windows and -target fake disagree") {
		t.Errorf("-windows -target fake: %d %s", code, se)
	}
	// A Windows flag is not a flag of another target.
	if code, _, _ := run("compile", "-target", "fake", "-langid", "0409", example); code != ExitUsage {
		t.Errorf("-langid accepted for fake")
	}
}

// TestMacOS runs the macOS target's commands and the .keylayout format row end to end.
func TestMacOS(t *testing.T) {
	const demo = "../../testdata/kbl-spec/examples/macos-option-deadkeys.kbl.json"
	dir := t.TempDir()
	code, so, se := run("compile", "-target", "macos", "-q", "-emit-expectations", "-o", dir, demo)
	if code != ExitOK || !strings.Contains(so, ".bundle") {
		t.Fatalf("compile: %d %s %s", code, so, se)
	}
	bundles, _ := filepath.Glob(filepath.Join(dir, "*.bundle", "Contents", "Resources", "*.keylayout"))
	if len(bundles) != 1 {
		t.Fatalf("bundle files: %v", bundles)
	}
	if _, err := os.Stat(filepath.Join(dir, "expect.tsv")); err != nil {
		t.Error(err)
	}
	zipPath := filepath.Join(dir, "demo.zip")
	if code, so, se := run("installer", "-target", "macos", "-q", "-o", zipPath, demo); code != ExitOK || !strings.Contains(so, "unsigned") {
		t.Fatalf("installer: %d %s %s", code, so, se)
	}
	pinned := filepath.Join(dir, "pinned.kbl.json")
	if code, _, se := run("pin", "-target", "macos", "-q", demo, pinned); code != ExitOK {
		t.Fatalf("pin: %d %s", code, se)
	}
	if code, so, se := run("validate", "-target", "macos", "-strict-defaults", pinned); code != ExitOK {
		t.Errorf("validate of the pinned file with -strict-defaults: %d %s %s", code, so, se)
	}
	if code, _, _ := run("validate", "-target", "macos", "-strict-defaults", demo); code == ExitOK {
		t.Errorf("validate -strict-defaults of an unpinned file passed")
	}
	// .keylayout in and out
	kl := filepath.Join(dir, "demo.keylayout")
	if code, _, se := run("convert", "-q", demo, kl); code != ExitOK {
		t.Fatalf("convert to .keylayout: %d %s", code, se)
	}
	back := filepath.Join(dir, "back.kbl.json")
	if code, _, se := run("convert", "-q", kl, back); code != ExitOK {
		t.Fatalf("convert from .keylayout: %d %s", code, se)
	}
	if code, _, se := run("compile", "-target", "macos", "-q", "-o", filepath.Join(dir, "again"), kl); code != ExitOK {
		t.Fatalf("compile a .keylayout: %d %s", code, se)
	}
	again, _ := filepath.Glob(filepath.Join(dir, "again", "*.bundle", "Contents", "Resources", "*.keylayout"))
	a, _ := os.ReadFile(bundles[0])
	b, _ := os.ReadFile(again[0])
	if string(a) != string(b) {
		t.Errorf("the .keylayout built from the imported .keylayout differs")
	}
}

// touchTarget builds from touch layers (deriving them from hardware layers) on a -runtime file.
type touchTarget struct{}

func (touchTarget) Name() string          { return "faketouch" }
func (touchTarget) DefaultsKind() string  { return "faketouchDefault" }
func (touchTarget) Layers() target.Layers { return target.TouchOrDerived }
func (touchTarget) Command(name string, fs *flag.FlagSet) target.Command {
	if name != "compile" && name != "validate" {
		return nil
	}
	c := &touchCompile{}
	c.runtime = target.RuntimeFlag(fs, "a test file")
	return c
}

type touchCompile struct{ runtime *string }

func (c *touchCompile) Check() error { return target.CheckRuntime(*c.runtime, false) }
func (c *touchCompile) Run(j *target.Job) *target.Result {
	line := "touch layer sets: " + strconv.Itoa(len(j.Layout.Touch))
	if target.DeriveTouch(touchTarget{}, j.Layout) {
		line = "derive touch layers"
	}
	return &target.Result{Lines: []string{line}}
}

// TestTouchTarget: a touch-only file reaches a touch target's command; -runtime is the target's usage check.
func TestTouchTarget(t *testing.T) {
	saved := registry.Targets
	registry.Targets = append(append([]target.Target(nil), saved...), touchTarget{})
	defer func() { registry.Targets = saved }()
	dir := t.TempDir()
	touchOnly := filepath.Join(dir, "touch.kbl.json")
	os.WriteFile(touchOnly, []byte(`{"kbl":"0.1","metadata":{"id":"und-t-k0-touch","name":"Touch only"},"keys":{"a":{"output":"a"}},"touch":[{"layers":[{"id":"base","rows":[["a"]]}]}]}`), 0o644)
	rt := filepath.Join(dir, "runtime.bin")
	os.WriteFile(rt, []byte("x"), 0o644)
	if code, so, se := run("compile", "-target", "faketouch", "-runtime", rt, touchOnly); code != ExitOK || !strings.Contains(so, "touch layer sets: 1") {
		t.Errorf("touch-only: %d %s %s", code, so, se)
	}
	if code, so, se := run("compile", "-target", "faketouch", "-runtime", rt, example); code != ExitOK || !strings.Contains(so, "derive touch layers") {
		t.Errorf("hardware only: %d %s %s", code, so, se)
	}
	if code, _, se := run("compile", "-target", "faketouch", touchOnly); code != ExitUsage || !strings.Contains(se, "-runtime is required") {
		t.Errorf("no -runtime: %d %s", code, se)
	}
	if code, _, se := run("compile", "-target", "faketouch", "-runtime", dir, touchOnly); code != ExitUsage || !strings.Contains(se, "not a file") {
		t.Errorf("-runtime folder: %d %s", code, se)
	}
	if code, _, _ := run("compile", "-target", "faketouch", "-runtime", rt, touchOnly, example); code != ExitUsage {
		t.Errorf("two layouts accepted")
	}
	if code, _, se := run("compile", "-target", "faketouch", "-h"); code != ExitUsage || !strings.Contains(se, "on-screen keyboard") || !strings.Contains(se, "-runtime") {
		t.Errorf("-h: %d %s", code, se)
	}
	if code, so, se := run("validate", "-target", "faketouch", "-runtime", rt, touchOnly); code != ExitOK || !strings.Contains(so, "valid") {
		t.Errorf("validate: %d %s %s", code, so, se)
	}
	// The hardware targets are unchanged: a touch-only file is their loss, not a usage error.
	if code, _, se := run("compile", "-target", "macos", "-o", dir, touchOnly); code != ExitError || !strings.Contains(se, "macos.noHardwareLayers") {
		t.Errorf("macos: %d %s", code, se)
	}
}
