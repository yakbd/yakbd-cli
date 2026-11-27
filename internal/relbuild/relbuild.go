// Package relbuild builds what a release ships (docs/release.md): yakbd for every operating system and architecture,
// and yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12). Every build uses fixed flags and
// environment (-trimpath, no VCS stamp, an empty build id, no cgo, the local Go toolchain only), so the same commit
// and Go version give the same bytes on any machine and in any folder. tools/toolsetup and tools/release use it.
package relbuild

import (
	"bytes"
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

// Setup builds yakbd.exe for x86, x64 and arm64 and yakbd-setup.exe carrying them, with the given product version
// (the uninstall entry's and the version resource's). It returns the installer and the yakbd.exe builds by build
// name, the same bytes the installer carries.
func Setup(root, version, release string) ([]byte, map[string][]byte, error) {
	return SetupWith(root, SetupOptions{Version: version, Release: release})
}

// SetupOptions choose what SetupWith puts into yakbd-setup.exe.
type SetupOptions struct {
	Version string // the product version
	Release string // written into ReleaseVar of yakbd.exe when set
	// Builds are the builds of yakbd.exe it carries, a subset of WindowsBuilds in that order; nil: all of them. A
	// machine whose build is missing gets exit code 1633 (docs/installer-cli.md §12).
	Builds []string
	// Extra is the extra payload (docs/installer-cli.md §12.1), in this order.
	Extra []toolsetup.Extra
	// Shortcuts are Start-menu shortcuts to files of Extra (docs/installer-cli.md §12.2).
	Shortcuts []toolsetup.Shortcut
}

// SetupWith is Setup with the builds and the extra payload chosen.
func SetupWith(root string, o SetupOptions) ([]byte, map[string][]byte, error) {
	version, release := o.Version, o.Release
	builds := o.Builds
	if builds == nil {
		builds = WindowsBuilds
	}
	exes := map[string][]byte{}
	for _, b := range builds {
		if WindowsArch[b] == "" {
			return nil, nil, fmt.Errorf("unknown build %q (want x86, x64 or arm64)", b)
		}
		var err error
		if exes[b], err = Build(root, "./cmd/yakbd", "windows", WindowsArch[b], false, release); err != nil {
			return nil, nil, err
		}
	}
	stub, err := Build(root, "./installer/toolstub", "windows", "386", true, "")
	if err != nil {
		return nil, nil, err
	}
	if end, err := payload.ImageEnd(stub); err != nil || end != int64(len(stub)) {
		return nil, nil, fmt.Errorf("installer program: bytes after the last section or no PE image (%v)", err)
	}
	prod := toolsetup.Product{Name: "yakbd", Version: version, Publisher: "The YAKBD contributors", Code: installer.ToolProductCode()}
	res, err := exeres.For(exeres.Product{Name: prod.Name, Version: prod.Version, Publisher: prod.Publisher, FileName: "yakbd-setup.exe"})
	if err != nil {
		return nil, nil, err
	}
	if stub, err = exeres.Add(stub, res); err != nil {
		return nil, nil, err
	}
	pl, err := toolsetup.Encode(toolsetup.Manifest{Product: prod, Shortcuts: o.Shortcuts}, builds, exes, o.Extra)
	if err != nil {
		return nil, nil, err
	}
	return append(stub, pl...), exes, nil
}
