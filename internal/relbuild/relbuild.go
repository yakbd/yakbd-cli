// Package relbuild builds what a release ships (docs/release.md): yakbd for every operating system and architecture,
// and yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12). Every build uses fixed flags and
// environment (-trimpath, no VCS stamp, an empty build id, no cgo, the local Go toolchain only), so the same commit
// and Go version give the same bytes on any machine and in any folder. tools/toolsetup and tools/release use it.
package relbuild

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/exeres"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
)

// ReleaseVar is the variable a release build sets with -X: the release version `yakbd version` prints.
const ReleaseVar = "github.com/yakbd/yakbd-cli/internal/cli.Release"

// Env is the environment that overrides the caller's for every build.
var Env = []string{"CGO_ENABLED=0", "GOFLAGS=", "GOEXPERIMENT=", "GOTOOLCHAIN=local", "GO386=sse2", "GOAMD64=v1", "GOARM64=v8.0"}

// Build runs go build in the module at root for one package and target and returns the program's bytes. gui
// makes a Windows GUI-subsystem program; release, when set, is written into ReleaseVar.
func Build(root, pkg, goos, goarch string, gui bool, release string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "yakbd-relbuild-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "out")
	ld := "-s -w -buildid="
	if gui {
		ld += " -H=windowsgui"
	}
	if release != "" {
		if strings.ContainsAny(release, " '\"\\") {
			return nil, fmt.Errorf("release version %q: no spaces, quotes or backslashes", release)
		}
		ld += " -X " + ReleaseVar + "=" + release
	}
	cmd := exec.Command("go", "build", "-o", out, "-trimpath", "-buildvcs=false", "-ldflags="+ld, pkg)
	cmd.Dir = root
	cmd.Env = append(append(os.Environ(), Env...), "GOOS="+goos, "GOARCH="+goarch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go build %s for %s/%s: %v\n%s", pkg, goos, goarch, err, stderr.String())
	}
	return os.ReadFile(out)
}

// WindowsArch maps the installer's build names to Go's architectures.
var WindowsArch = map[string]string{toolsetup.BuildX86: "386", toolsetup.BuildX64: "amd64", toolsetup.BuildARM64: "arm64"}

// WindowsBuilds are the installer's builds of yakbd.exe in payload order.
var WindowsBuilds = []string{toolsetup.BuildX86, toolsetup.BuildX64, toolsetup.BuildARM64}

// yakbdIcon is YAKBD's icon (the editor's, yakbd/yakbd-gui packaging/windows/yakbd.ico, MIT-0; NOTICE): the YAKBD
// installers' icon, in Explorer, the title bar, the taskbar and the Installed apps entry (its DisplayIcon is the
// installer copy).
//
//go:embed yakbd.ico
var yakbdIcon []byte

// Installer file names (docs/installer-cli.md §12).
const (
	CLISetupName = "yakbd-cli-setup.exe"
	GUISetupName = "yakbd-gui-setup.exe"
)

// Setup builds yakbd.exe for x86, x64 and arm64 and yakbd-cli-setup.exe carrying them (the CLI component), with the
// given CLI version. It returns the installer and the yakbd.exe builds by build name, the same bytes the installer
// carries.
func Setup(root, version, release string) ([]byte, map[string][]byte, error) {
	return SetupWith(root, SetupOptions{Version: version, Release: release})
}

// SetupOptions choose what SetupWith puts into the installer.
type SetupOptions struct {
	Version string // the CLI component's version
	Release string // written into ReleaseVar of yakbd.exe when set
	// Builds are the builds of yakbd.exe it carries, a subset of WindowsBuilds in that order; nil: all of them. A
	// machine whose build is missing gets exit code 1633.
	Builds []string
	// Editor, when set, makes yakbd-gui-setup.exe: the editor component besides the CLI.
	Editor *EditorInput
}

// EditorInput is the editor component of yakbd-gui-setup.exe: its version, its files per build (each build's files
// in Files[build], paths relative to the Editor folder), its Start-menu shortcuts (targets in that folder) and the
// per-user settings "Remove my settings" deletes.
type EditorInput struct {
	Version   string
	Files     map[string][]toolsetup.Input
	Shortcuts []toolsetup.Shortcut
	UserData  []toolsetup.UserData
}

// SetupWith is Setup with the builds and the editor chosen.
func SetupWith(root string, o SetupOptions) ([]byte, map[string][]byte, error) {
	version, release := o.Version, o.Release
	builds := o.Builds
	if builds == nil {
		builds = WindowsBuilds
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return nil, nil, err
	}
	notice, err := os.ReadFile(filepath.Join(root, "NOTICE"))
	if err != nil {
		return nil, nil, err
	}
	exes := map[string][]byte{}
	cli := toolsetup.ComponentInput{ID: toolsetup.CLI, Version: version, Builds: builds}
	for _, b := range builds {
		if WindowsArch[b] == "" {
			return nil, nil, fmt.Errorf("unknown build %q (want x86, x64 or arm64)", b)
		}
		if exes[b], err = Build(root, "./cmd/yakbd", "windows", WindowsArch[b], false, release); err != nil {
			return nil, nil, err
		}
		cli.Files = append(cli.Files, toolsetup.Input{Path: toolsetup.CLIExe, Build: b, Data: exes[b]})
	}
	cli.Files = append(cli.Files, toolsetup.Input{Path: "LICENSE", Data: license}, toolsetup.Input{Path: "NOTICE", Data: notice})
	comps := []toolsetup.ComponentInput{cli}
	m := toolsetup.Manifest{Product: toolsetup.Product{Name: toolsetup.ProductName, Publisher: "The YAKBD contributors", Code: installer.SuiteProductCode()},
		AddToPath: true}
	name := CLISetupName
	if e := o.Editor; e != nil {
		ed := toolsetup.ComponentInput{ID: toolsetup.Editor, Version: e.Version}
		for _, b := range WindowsBuilds {
			if fs, ok := e.Files[b]; ok {
				ed.Builds = append(ed.Builds, b)
				for _, f := range fs {
					f.Build = b
					ed.Files = append(ed.Files, f)
				}
			}
		}
		comps = append(comps, ed)
		for _, sc := range e.Shortcuts {
			sc.Component = toolsetup.Editor
			m.Shortcuts = append(m.Shortcuts, sc)
		}
		for _, u := range e.UserData {
			u.Component = toolsetup.Editor
			m.UserData = append(m.UserData, u)
		}
		m.AddToPath = false
		name = GUISetupName
	}
	stub, err := Build(root, "./installer/toolstub", "windows", "386", true, "")
	if err != nil {
		return nil, nil, err
	}
	if end, err := payload.ImageEnd(stub); err != nil || end != int64(len(stub)) {
		return nil, nil, fmt.Errorf("installer program: bytes after the last section or no PE image (%v)", err)
	}
	pl, err := toolsetup.Encode(m, comps)
	if err != nil {
		return nil, nil, err
	}
	dec, err := toolsetup.Decode(pl)
	if err != nil {
		return nil, nil, err
	}
	icon, err := exeres.ParseICO(yakbdIcon)
	if err != nil {
		return nil, nil, err
	}
	res, err := exeres.For(exeres.Product{Name: toolsetup.ProductName, Version: dec.Manifest.Version(), Publisher: m.Product.Publisher, FileName: name, Icon: icon})
	if err != nil {
		return nil, nil, err
	}
	if stub, err = exeres.Add(stub, res); err != nil {
		return nil, nil, err
	}
	return append(stub, pl...), exes, nil
}
