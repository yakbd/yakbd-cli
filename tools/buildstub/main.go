// Command buildstub builds the installer program and writes installer/stub-386.exe and installer/stub-386.version
// (docs/installer.md §6). Run it from the repository root after changing installer/stub, installer/setup or
// installer/payload: go run ./tools/buildstub
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yakbd/yakbd-cli/installer/stubbuild"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	b, ver, err := stubbuild.Build(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "buildstub:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(root, "installer", "stub-386.exe"), b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "buildstub:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(root, "installer", "stub-386.version"), []byte(ver+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "buildstub:", err)
		os.Exit(1)
	}
	fmt.Printf("installer/stub-386.exe: %d bytes, %s\n", len(b), ver)
}
