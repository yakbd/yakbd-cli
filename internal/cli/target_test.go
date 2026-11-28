package cli

import (
	"flag"
	"os"
	"path/filepath"
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
	if code, _, se := run("compile", "-target", "amiga", example); code != ExitUsage || !strings.Contains(se, "known: fake, windows") {
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
