// Package xkb is the Linux XKB target behind the command line's target interface (package target): compile
// (the symbols file, the registry entry, the Compose file and yakbd key types), installer (the release archive
// with an install script and a README), pin and validate's build check (kbl-spec targets/xkb.md). The build
// itself is target/xkb/build.
package xkb

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/yakbd/yakbd-cli/target"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
)

// Target is the XKB target.
type Target struct{}

// Name is "xkb".
func (Target) Name() string { return "xkb" }

// DefaultsKind is "xkbDefault".
func (Target) DefaultsKind() string { return "xkbDefault" }

// Command returns compile, installer, pin and validate.
func (Target) Command(name string, fs *flag.FlagSet) target.Command {
	switch name {
	case "compile":
		c := &compileCmd{}
		c.out = fs.String("o", ".", "output directory: the files are written under <dir>/xkb/ and <dir>/compose/")
		c.expect = fs.Bool("emit-expectations", false, "also write <dir>/expect.tsv: what the layout types, for tools/xkb-check")
		c.max = fs.Int("max-expectations", 80, "with -emit-expectations: at most this many lines (0: all)")
		return c
	case "installer":
		c := &installerCmd{}
		c.out = fs.String("o", "", "output file (default <symbolsName>[-<variant>].tar.gz)")
		return c
	case "pin":
		return pinCmd{}
	case "validate":
		return validateCmd{}
	}
	return nil
}

type compileCmd struct {
	out    *string
	expect *bool
	max    *int
}

func (c *compileCmd) Check() error {
	if *c.max < 0 {
		return fmt.Errorf("-max-expectations must not be negative")
	}
	return nil
}

func (c *compileCmd) Run(j *target.Job) *target.Result {
	res, br := build.Build(j.Layout, build.Options{Policy: j.Policy})
	j.Report.Merge(br)
	if res == nil {
		return nil
	}
	out := &target.Result{}
	for _, f := range Files(res) {
		p := filepath.Join(*c.out, filepath.FromSlash(f.Path))
		out.Files = append(out.Files, target.File{Path: p, Data: f.Data, MkDir: true, Line: "xkb\t" + p})
	}
	if *c.expect {
		p := filepath.Join(*c.out, "expect.tsv")
		out.Files = append(out.Files, target.File{Path: p, Data: FormatExpectations(Expectations(j.Layout, res, *c.max)), MkDir: true,
			Line: "expectations\t" + p})
	}
	return out
}

type installerCmd struct{ out *string }

func (c *installerCmd) Check() error { return nil }

func (c *installerCmd) Run(j *target.Job) *target.Result {
	res, br := build.Build(j.Layout, build.Options{Policy: j.Policy})
	j.Report.Merge(br)
	if res == nil {
		return nil
	}
	data, err := Archive(res)
	if err != nil {
		return &target.Result{Fail: err.Error()}
	}
	p := *c.out
	if p == "" {
		p = Folder(res) + ".tar.gz"
	}
	return &target.Result{Files: []target.File{{Path: p, Data: data,
		Line: fmt.Sprintf("%s\t%d bytes\t%s/: xkb/, install.sh (--uninstall), README", p, len(data), Folder(res))}}}
}

type pinCmd struct{}

func (pinCmd) Check() error { return nil }

// Run writes every XKB default into the layout. The command line's policy is not applied: a pin records choices,
// it does not judge them.
func (pinCmd) Run(j *target.Job) *target.Result {
	pinned, pr := build.Pin(j.Layout, build.Options{})
	j.Report.Merge(pr)
	if pinned == nil {
		return nil
	}
	return &target.Result{Layout: pinned}
}

type validateCmd struct{}

func (validateCmd) Check() error { return nil }

// Run builds the layout for XKB without writing anything: the build's report.
func (validateCmd) Run(j *target.Job) *target.Result {
	_, br := build.Build(j.Layout, build.Options{Policy: j.Policy})
	j.Report.Merge(br)
	return nil
}
