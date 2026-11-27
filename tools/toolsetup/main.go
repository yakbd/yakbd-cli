// Command toolsetup builds yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12): yakbd.exe for
// windows/386, amd64 and arm64 and the installer program installer/toolstub for windows/386, each with fixed flags
// and environment (internal/relbuild: the same source and Go give the same bytes), then the program's resources
// (installer/exeres: manifest, icon, version) and the tool payload. Run from the repository root:
//
//	go run ./tools/toolsetup [-o yakbd-setup.exe] [-version 0.1.0-preview.1]
//
// -version sets the installer's product version (default: the tool's own); test bundles use it to make an older and a
// newer installer of the same yakbd.exe. A release builds it through tools/release instead (docs/release.md).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/relbuild"
)

// Write builds the installer with the given product version.
func Write(version string) ([]byte, error) {
	b, _, err := relbuild.Setup(".", version, "")
	return b, err
}

func main() {
	out := flag.String("o", "yakbd-setup.exe", "output file")
	version := flag.String("version", cli.VersionString(), "product version of the installer")
	flag.Parse()
	b, err := Write(*version)
	if err == nil {
		err = os.WriteFile(*out, b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "toolsetup:", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d bytes, yakbd %s, product code %s\n", *out, len(b), *version, installer.ToolProductCode())
}
