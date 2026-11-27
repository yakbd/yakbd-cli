// Package installer writes yakbd's Windows installers: one program that carries every build of a layout and
// installs the files the machine it runs on needs (docs/installer.md). It turns a compiled build set into a
// Package (manifest + files, installer/payload) and a Container writes the Package as a file. The only container is
// ExeContainer: the Windows program of installer/stub, embedded in this package as stub-386.exe, followed by the
// payload. Nothing here runs on Windows; the install logic itself is installer/setup.
package installer

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/exeres"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/shape"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// stub is the installer program, built by `go run ./tools/buildstub` (docs/installer.md §6).
//
//go:embed stub-386.exe
var stub []byte

// stubGoVersion is the Go version stub-386.exe was built with.
//
//go:embed stub-386.version
var stubGoVersion string

// Stub returns the embedded installer program.
func Stub() []byte { return stub }

// StubGoVersion returns the Go version the embedded program was built with.
func StubGoVersion() string { return strings.TrimSpace(stubGoVersion) }

// Machine kinds an installer can carry, and the builds each needs (inst §2.1, §2.4).
const (
	MachineX86   = "x86"
	MachineX64   = "x64"
	MachineARM64 = "arm64"
)

// Machines lists the machine kinds in the order of a full installer.
var Machines = []string{MachineX64, MachineARM64, MachineX86}

// BuildsFor gives the builds a machine kind needs.
var BuildsFor = map[string][]string{
	MachineX86:   {payload.BuildX86},
	MachineX64:   {payload.BuildX64, payload.BuildWOW64},
	MachineARM64: {payload.BuildARM64X, payload.BuildWOW64},
}

// buildOf maps the compiler's targets to payload builds.
var buildOf = map[windows.Target]string{
	windows.TargetAMD64:  payload.BuildX64,
	windows.TargetWOW64:  payload.BuildWOW64,
	windows.TargetX86:    payload.BuildX86,
	windows.TargetARM64X: payload.BuildARM64X,
}

// Package is what an installer carries.
type Package struct {
	Manifest payload.Manifest
	Builds   []string
	Data     map[string][]byte
	// Machines are the machine kinds the package serves.
	Machines []string
}

// Options of a package.
type Options struct {
	// Machines limits the machine kinds (MachineX86, ...); empty means every kind whose builds exist.
	Machines []string
	// Enable, when set, overrides rule enableForUser (targets/windows-install.md section 7).
	Enable *bool
	// SignIn, when set, overrides rule enableForSignInAndNewUsers (section 7.1).
	SignIn *bool
	// Tool names the tool and its version for the manifest.
	Tool string
}

// NewPackage makes the package of a compiled layout: l is the layout, res its lowered form (for the pinned and
// decided registration values) and set its build set.
func NewPackage(l *model.Layout, res *build.Result, set *windows.BuildSet, opt Options) (*Package, *loss.Report) {
	r := &loss.Report{}
	have := map[string][]byte{}
	for _, b := range set.Builds {
		if name, ok := buildOf[b.Target]; ok {
			have[name] = b.Bytes
		}
	}
	explicit := len(opt.Machines) > 0
	machines := opt.Machines
	if !explicit {
		machines = Machines
	}
	p := &Package{Data: map[string][]byte{}}
	for _, mk := range machines {
		need, ok := BuildsFor[mk]
		if !ok {
			r.Errorf("installer.machine", loss.Where{}, "unknown machine kind %q (want x86, x64 or arm64)", mk)
			continue
		}
		var missing []string
		for _, b := range need {
			if have[b] == nil {
				missing = append(missing, b)
			}
		}
		if len(missing) > 0 {
			if explicit {
				r.Errorf("installer.machineMissing", loss.Where{Path: mk}, "no %s build for %s Windows", strings.Join(missing, " and "), mk)
			}
			continue
		}
		p.Machines = append(p.Machines, mk)
		for _, b := range need {
			if p.Data[b] == nil {
				p.Data[b] = have[b]
			}
		}
	}
	for _, b := range []string{payload.BuildX64, payload.BuildARM64X, payload.BuildWOW64, payload.BuildX86} {
		if p.Data[b] != nil {
			p.Builds = append(p.Builds, b)
		}
	}
	var refused []string
	for _, mk := range Machines {
		if !slices.Contains(p.Machines, mk) {
			refused = append(refused, mk)
		}
	}
	if len(refused) > 0 {
		r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Warning, Code: "windows.installerMachinesMissing", Where: loss.Where{Path: "/targets/windows/builds"},
			Message: "the installer refuses on " + strings.Join(refused, ", ") + " Windows (no build for it)", Detail: map[string]any{"machines": refused}})
	}
	// The check the installer program makes before it copies (installer/shape): a file that would be refused on
	// the machine is never packed.
	for _, b := range p.Builds {
		if err := shape.Check(b, p.Data[b]); err != nil {
			r.Errorf("installer.fileShape", loss.Where{Path: b}, "%v", err)
		}
	}
	if len(p.Builds) == 0 {
		r.Errorf("installer.empty", loss.Where{}, "no machine kind can be served: nothing to install")
	}

	dec, dr := Decide(l, res.Pins)
	r.Merge(dr)
	enable := dec.Enable
	if opt.Enable != nil {
		enable = *opt.Enable
	}
	signIn := dec.SignIn
	if opt.SignIn != nil {
		signIn = *opt.SignIn
	}
	reg := &model.Registration{}
	if res.Pins != nil && res.Pins.Registration != nil {
		reg = res.Pins.Registration
	}
	p.Manifest = payload.Manifest{
		Format:  payload.Version,
		Tool:    opt.Tool,
		Product: payload.Product{Name: dec.ProductName, Version: dec.ProductVersion, Publisher: dec.Publisher, Code: dec.ProductCode},
		Layout: payload.Layout{File: res.DLL.FileName, LangID: strings.ToUpper(reg.LangID), LayoutText: reg.LayoutText,
			KLID: strings.ToLower(reg.KLID), LayoutID: strings.ToLower(reg.LayoutID), LocaleName: reg.LocaleName, CustomLanguageName: reg.CustomLanguageName},
		Enable: enable,
		SignIn: signIn,
	}
	if opt.SignIn != nil && *opt.SignIn && strings.EqualFold(reg.LangID, "0C00") {
		r.Errorf("windows.registrationInvalid", loss.Where{Path: "/targets/windows/install/enableForSignInAndNewUsers"},
			"-signin true: a LANGID 0C00 layout cannot be put on the sign-in screen or into new accounts (targets/windows-install.md section 7.1)")
	}
	if p.Manifest.Layout.LayoutText == "" {
		p.Manifest.Layout.LayoutText = l.Metadata.Name
	}
	if err := p.Manifest.Check(); err != nil {
		r.Errorf("installer.manifest", loss.Where{}, "%v", err)
	}
	return p, r
}

// Container writes a package as an installer file.
type Container interface {
	// Name is the container's name ("exe").
	Name() string
	Write(p *Package) ([]byte, error)
}

// ExeContainer writes a self-contained Windows program: the stub with the installer's resources (manifest, icon,
// version; installer/exeres) as a new last section, followed by the payload (docs/installer.md §5).
// The output is unsigned; a signing step, when there is one, runs on the finished file.
type ExeContainer struct {
	// Stub is the program to use; nil means the embedded one.
	Stub []byte
}

// Name implements Container.
func (ExeContainer) Name() string { return "exe" }

// Write implements Container.
func (c ExeContainer) Write(p *Package) ([]byte, error) {
	s := c.Stub
	if s == nil {
		s = stub
	}
	end, err := payload.ImageEnd(s)
	if err != nil {
		return nil, fmt.Errorf("installer program: %v", err)
	}
	if end != int64(len(s)) {
		return nil, fmt.Errorf("installer program has %d bytes after its last section", int64(len(s))-end)
	}
	m := p.Manifest
	res, err := exeres.For(exeres.Product{Name: m.Product.Name, Version: m.Product.Version, Publisher: m.Product.Publisher,
		FileName: strings.TrimSuffix(strings.ToLower(m.Layout.File), ".dll") + "-setup.exe"})
	if err != nil {
		return nil, err
	}
	if s, err = exeres.Add(s, res); err != nil {
		return nil, fmt.Errorf("installer program resources: %v", err)
	}
	pl, err := payload.Encode(p.Manifest, p.Builds, p.Data)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(s)+len(pl))
	return append(append(out, s...), pl...), nil
}
