// Package wintarget is the Windows target behind the command line's target interface (package target): the
// commands compile (DLL sets), installer (one .exe), pin and validate's build check. The Windows build itself
// stays in target/windows and target/windows/build; this package only wires it to the command line.
package wintarget

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// Target is the Windows target.
type Target struct{}

// Name is "windows".
func (Target) Name() string { return "windows" }

// DefaultsKind is "windowsDefault".
func (Target) DefaultsKind() string { return "windowsDefault" }

// Command returns compile, installer, pin and validate.
func (Target) Command(name string, fs *flag.FlagSet) target.Command {
	switch name {
	case "compile":
		c := &compileCmd{}
		c.out = fs.String("o", ".", "output directory; each build goes into <dir>/<build>/")
		c.builds = fs.String("build", "", "comma-separated builds (x64, wow64, x86, arm64x, arm64: the last two unverified); default: targets.windows.builds of the file")
		c.expect = fs.Bool("emit-expectations", false, "also write <dir>/<name>.expect.txt: the expected result of every key for a test run on Windows")
		c.langID = fs.String("langid", "", "LANGID (4 hex digits) for a file that has no targets.windows.registration.langId")
		return c
	case "installer":
		c := &installerCmd{}
		c.out = fs.String("o", "", "output file (default <name>-setup.exe)")
		c.machines = fs.String("build", "", "comma-separated machine kinds to carry: x64 (x64 + wow64 files), arm64 (arm64x + wow64), x86; default: every kind the tool can build for")
		c.enable = fs.String("enable", "", "true or false: override install.enableForUser (default: the file, else rule enableForUser = true)")
		c.signIn = fs.String("signin", "", "true or false: override install.enableForSignInAndNewUsers (default: the file, else rule enableForSignInAndNewUsers = false)")
		c.langID = fs.String("langid", "", "LANGID (4 hex digits) for a file that has no targets.windows.registration.langId")
		return c
	case "pin":
		return pinCmd{}
	case "validate":
		return validateCmd{}
	}
	return nil
}

// BuildNames are the names of the builds compile takes, in the order its usage lists them.
var BuildNames = []string{"x64", "wow64", "x86", "arm64x", "arm64"}

var buildOfName = map[string]windows.Target{"x64": windows.TargetAMD64, "wow64": windows.TargetWOW64, "x86": windows.TargetX86,
	"arm64x": windows.TargetARM64X, "arm64": windows.TargetARM64}

// IsBuildName reports whether s names a build of compile's -build.
func IsBuildName(s string) bool {
	_, ok := buildOfName[strings.TrimSpace(s)]
	return ok
}

// SetLangID pins registration.langId.
func SetLangID(l *model.Layout, id string) {
	if l.Targets == nil {
		l.Targets = &model.Targets{}
	}
	if l.Targets.Windows == nil {
		l.Targets.Windows = &model.WindowsTarget{}
	}
	if l.Targets.Windows.Registration == nil {
		l.Targets.Windows.Registration = &model.Registration{}
	}
	l.Targets.Windows.Registration.LangID = strings.ToUpper(id)
}

// HasLangID reports whether l pins registration.langId.
func HasLangID(l *model.Layout) bool {
	return l.Targets != nil && l.Targets.Windows != nil && l.Targets.Windows.Registration != nil && l.Targets.Windows.Registration.LangID != ""
}

// applyLangID pins -langid when the file has none.
func applyLangID(l *model.Layout, id string, r *loss.Report) {
	if id != "" && !HasLangID(l) {
		SetLangID(l, id)
		r.Infof("cli.langid", loss.Where{}, "LANGID %s from -langid", id)
	}
}

type compileCmd struct {
	out, builds, langID *string
	expect              *bool
	list                []windows.Target
}

func (c *compileCmd) Check() error {
	if *c.builds == "" {
		return nil
	}
	for _, s := range strings.Split(*c.builds, ",") {
		t, ok := buildOfName[strings.TrimSpace(s)]
		if !ok {
			return fmt.Errorf("unknown build %q (want x64, wow64, x86, arm64x or arm64)", s)
		}
		c.list = append(c.list, t)
	}
	return nil
}

func (c *compileCmd) Run(j *target.Job) *target.Result {
	l, r := j.Layout, j.Report
	applyLangID(l, *c.langID, r)
	res, br := build.Lower(l, build.Options{ToolVersion: j.ToolVersion, Policy: j.Policy})
	r.Merge(br)
	if res == nil {
		return nil
	}
	list := res.Targets
	if c.list != nil {
		list = c.list
	}
	set := compileSet(l, res.DLL, list, j.StrictPolicy, true) // -build arm64x, arm64: the unverified candidates
	r.Merge(set.Report)
	out := &target.Result{}
	if len(set.Builds) == 0 {
		if !r.HasErrors() {
			out.Fail = "no build to make"
		}
		return out
	}
	names := map[windows.Target]string{}
	for n, t := range buildOfName {
		names[t] = n
	}
	dir := *c.out
	for _, b := range set.Builds {
		p := filepath.Join(dir, names[b.Target], b.FileName)
		out.Files = append(out.Files, target.File{Path: p, Data: b.Bytes, MkDir: true,
			Line: fmt.Sprintf("%s\t%s\t%d bytes\tinstall into %%SystemRoot%%\\%s", names[b.Target], p, len(b.Bytes), b.Dir)})
	}
	if *c.expect {
		name := strings.TrimSuffix(res.DLL.FileName, ".dll")
		p := filepath.Join(dir, name+".expect.txt")
		out.Files = append(out.Files, target.File{Path: p, Data: windows.FormatExpectations(name, res.DLL.Tables.Expectations()),
			Line: "expectations\t" + p})
	}
	for _, t := range set.Unsupported {
		out.Lines = append(out.Lines, fmt.Sprintf("%s\tnot supported yet", t))
	}
	return out
}

type installerCmd struct {
	out, machines, enable, signIn, langID *string
	enableOverride, signInOverride        *bool
	kinds                                 []string
}

func boolFlag(name, v string) (*bool, error) {
	switch v {
	case "":
		return nil, nil
	case "true", "false":
		b := v == "true"
		return &b, nil
	}
	return nil, fmt.Errorf("-%s takes true or false", name)
}

func (c *installerCmd) Check() error {
	var err error
	if c.enableOverride, err = boolFlag("enable", *c.enable); err != nil {
		return err
	}
	if c.signInOverride, err = boolFlag("signin", *c.signIn); err != nil {
		return err
	}
	if *c.machines != "" {
		for _, s := range strings.Split(*c.machines, ",") {
			s = strings.TrimSpace(s)
			if _, ok := installer.BuildsFor[s]; !ok {
				return fmt.Errorf("unknown machine kind %q (want x64, arm64 or x86)", s)
			}
			c.kinds = append(c.kinds, s)
		}
	}
	return nil
}

var targetOfBuild = map[string]windows.Target{
	"x64": windows.TargetAMD64, "wow64": windows.TargetWOW64, "x86": windows.TargetX86, "arm64x": windows.TargetARM64X,
}

func (c *installerCmd) Run(j *target.Job) *target.Result {
	l, r := j.Layout, j.Report
	applyLangID(l, *c.langID, r)
	res, br := build.Lower(l, build.Options{ToolVersion: j.ToolVersion, Policy: j.Policy})
	r.Merge(br)
	if res == nil {
		return nil
	}
	list := res.Targets
	if len(c.kinds) > 0 {
		list = nil
		seen := map[string]bool{}
		for _, k := range c.kinds {
			for _, b := range installer.BuildsFor[k] {
				if !seen[b] {
					seen[b] = true
					list = append(list, targetOfBuild[b])
				}
			}
		}
	}
	// An explicit -build arm64 builds the ARM64X file even while the full set still skips it (it passed the ARM64
	// guest, VM-6; TA makes it a default build).
	set := compileSet(l, res.DLL, list, j.Policy, len(c.kinds) > 0)
	r.Merge(set.Report)
	if r.HasErrors() {
		return nil
	}
	pkg, pr := installer.NewPackage(l, res, set, installer.Options{Machines: c.kinds, Enable: c.enableOverride, SignIn: c.signInOverride,
		Tool: j.ToolName})
	policy.Resolve(pr, l, "windows", j.Policy)
	r.Merge(pr)
	if r.HasErrors() {
		return nil
	}
	data, err := installer.ExeContainer{}.Write(pkg)
	if err != nil {
		return &target.Result{Fail: err.Error()}
	}
	path := *c.out
	if path == "" {
		path = strings.TrimSuffix(res.DLL.FileName, ".dll") + "-setup.exe"
	}
	return &target.Result{Files: []target.File{{Path: path, Data: data,
		Line: fmt.Sprintf("%s\t%d bytes\tfor %s Windows\tfiles %s\tproduct %s\tunsigned", path, len(data),
			strings.Join(pkg.Machines, ", "), strings.Join(pkg.Builds, ", "), pkg.Manifest.Product.Code)}}}
}

type pinCmd struct{}

func (pinCmd) Check() error { return nil }

// Run writes every Windows default and every installation default into the layout. The command line's policy is
// not applied: a pin records choices, it does not judge them.
func (pinCmd) Run(j *target.Job) *target.Result {
	pinned, pr := build.Pin(j.Layout, build.Options{ToolVersion: j.ToolVersion})
	j.Report.Merge(pr)
	if pinned == nil {
		return nil
	}
	j.Report.Merge(installer.Pin(pinned))
	return &target.Result{Layout: pinned}
}

type validateCmd struct{}

func (validateCmd) Check() error { return nil }

// Run builds the layout for Windows without writing anything: the build's report.
func (validateCmd) Run(j *target.Job) *target.Result {
	_, br := build.Lower(j.Layout, build.Options{ToolVersion: j.ToolVersion, Policy: j.Policy})
	j.Report.Merge(br)
	return nil
}

// compileSet builds the DLL set and gives the compiler's codes the spec's loss kinds, under the file's policies.
// unverified allows the ARM64X and ARM64 candidates when asked for (windows.CompileSet).
func compileSet(l *model.Layout, d *windows.DLL, list []windows.Target, extra *model.OnLoss, unverified bool) *windows.BuildSet {
	set := windows.CompileSet(d, list, unverified)
	for i := range set.Report.Entries {
		e := &set.Report.Entries[i]
		switch e.Code {
		case "win.build.unsupported":
			e.Code, e.Class = "windows.architectureUnsupported", loss.Loss
		case "win.size":
			e.Code, e.Class = "windows.sizeLimit", loss.Loss
		}
	}
	policy.Resolve(set.Report, l, "windows", extra)
	return set
}
