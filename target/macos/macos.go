// Package macos is the macOS target behind the command line's target interface (package target): compile (the
// layout bundle), installer (the release zip: the bundle and a README), pin and validate's build check
// (kbl-spec targets/macos.md). The build itself is target/macos/build.
package macos

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/yakbd/yakbd-cli/target"
	"github.com/yakbd/yakbd-cli/target/macos/build"
)

// Target is the macOS target.
type Target struct{}

// Name is "macos".
func (Target) Name() string { return "macos" }

// DefaultsKind is "macosDefault".
func (Target) DefaultsKind() string { return "macosDefault" }

// Command returns compile, installer, pin and validate.
func (Target) Command(name string, fs *flag.FlagSet) target.Command {
	switch name {
	case "compile":
		c := &compileCmd{}
		c.out = fs.String("o", ".", "output directory: the bundle is written as <dir>/<bundleName>.bundle")
		c.expect = fs.Bool("emit-expectations", false, "also write <dir>/expect.tsv: what the layout types, for tools/macos-check")
		c.max = fs.Int("max-expectations", 80, "with -emit-expectations: at most this many lines (0: all)")
		return c
	case "installer":
		c := &installerCmd{}
		c.out = fs.String("o", "", "output file (default <bundleName>.zip)")
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
	for _, f := range Bundle(res) {
		p := filepath.Join(*c.out, filepath.FromSlash(f.Path))
		out.Files = append(out.Files, target.File{Path: p, Data: f.Data, MkDir: true})
	}
	bundle := filepath.Join(*c.out, res.Pins.BundleName+".bundle")
	out.Lines = append(out.Lines, fmt.Sprintf("macos\t%s\tlayout %q\tinput source %s\tcopy into ~/Library/Keyboard Layouts", bundle,
		res.Pins.LayoutName, res.Pins.InputSourceID))
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
	data, err := Zip(res)
	if err != nil {
		return &target.Result{Fail: err.Error()}
	}
	p := *c.out
	if p == "" {
		p = res.Pins.BundleName + ".zip"
	}
	return &target.Result{Files: []target.File{{Path: p, Data: data,
		Line: fmt.Sprintf("%s\t%d bytes\t%s.bundle and README.txt\tunsigned", p, len(data), res.Pins.BundleName)}}}
}

type pinCmd struct{}

func (pinCmd) Check() error { return nil }

// Run writes every macOS default into the layout. The command line's policy is not applied: a pin records
// choices, it does not judge them.
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

// Run builds the layout for macOS without writing anything: the build's report.
func (validateCmd) Run(j *target.Job) *target.Result {
	_, br := build.Build(j.Layout, build.Options{Policy: j.Policy})
	j.Report.Merge(br)
	return nil
}
