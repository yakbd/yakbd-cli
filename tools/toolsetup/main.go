// Command toolsetup builds yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12): yakbd.exe for
// windows/386, amd64 and arm64 and the installer program installer/toolstub for windows/386, each with fixed flags
// and environment (internal/relbuild: the same source and Go give the same bytes), then the program's resources
// (installer/exeres: manifest, icon, version) and the tool payload. Run from the repository root:
//
//	go run ./tools/toolsetup [-o yakbd-setup.exe] [-version V] [-builds x64,arm64] [-extra [BUILD=]DIR]... [-shortcut "NAME=PATH"]...
//
// -version sets the installer's product version (default: the tool's own); test bundles use it to make an older and a
// newer installer of the same yakbd.exe. With the tool's own version, yakbd.exe is built as a release builds it, so
// that at a release tag and with no other flag the output is that release's yakbd-setup.exe byte for byte. A
// release builds it through tools/release (docs/release.md).
//
// -builds limits the builds of yakbd.exe it carries. -extra adds every regular file under DIR (recursively, in
// name order) to the extra payload, installed into the folder of yakbd.exe at its path below DIR, on every machine
// or, as BUILD=DIR, only on machines of that build (docs/installer-cli.md §12.1). -shortcut NAME=PATH adds a
// Start-menu shortcut NAME to the extra payload's file PATH (relative to the install folder, backslashes or
// slashes), made on the machines whose build carries PATH (§12.2). It prints the SHA-256 of every yakbd.exe build it
// carries; without -extra and -shortcut, at a release tag, the whole file equals the release's yakbd-setup.exe.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/relbuild"
)

// Write builds the installer with the given product version.
func Write(version string) ([]byte, error) {
	b, _, err := relbuild.Setup(".", version, "")
	return b, err
}

// readExtra reads the -extra values: [BUILD=]DIR, every regular file below DIR in name order.
func readExtra(specs []string) ([]toolsetup.Extra, error) {
	var out []toolsetup.Extra
	for _, spec := range specs {
		build, dir := "", spec
		if b, d, ok := strings.Cut(spec, "="); ok && relbuild.WindowsArch[b] != "" {
			build, dir = b, d
		}
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("%s is not a regular file (links are not followed)", p)
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out = append(out, toolsetup.Extra{Path: strings.ReplaceAll(filepath.ToSlash(rel), "/", `\`), Build: build, Data: data})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("-extra %s: %v", spec, err)
		}
	}
	return out, nil
}

// readShortcuts reads the -shortcut values: NAME=PATH, PATH with slashes or backslashes.
func readShortcuts(specs []string) ([]toolsetup.Shortcut, error) {
	var out []toolsetup.Shortcut
	for _, spec := range specs {
		name, target, ok := strings.Cut(spec, "=")
		if !ok || name == "" || target == "" {
			return nil, fmt.Errorf("-shortcut %q: want NAME=PATH, e.g. \"YAKBD Editor=yakbd-gui.exe\"", spec)
		}
		out = append(out, toolsetup.Shortcut{Name: name, Target: strings.ReplaceAll(target, "/", `\`)})
	}
	return out, nil
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	out := flag.String("o", "yakbd-setup.exe", "output file")
	version := flag.String("version", cli.VersionString(), "product version of the installer")
	builds := flag.String("builds", "", "comma-separated builds of yakbd.exe to carry (x86, x64, arm64); default: all three")
	var extras list
	flag.Var(&extras, "extra", "[BUILD=]DIR: add the files under DIR to the extra payload (repeatable)")
	var shortcuts list
	flag.Var(&shortcuts, "shortcut", "NAME=PATH: a Start-menu shortcut NAME to the extra payload's file PATH (repeatable)")
	flag.Parse()
	o := relbuild.SetupOptions{Version: *version}
	if *version == cli.VersionString() {
		o.Release = *version
	}
	if *builds != "" {
		o.Builds = strings.Split(*builds, ",")
	}
	var err error
	if o.Extra, err = readExtra(extras); err != nil {
		fmt.Fprintln(os.Stderr, "toolsetup:", err)
		os.Exit(1)
	}
	if o.Shortcuts, err = readShortcuts(shortcuts); err != nil {
		fmt.Fprintln(os.Stderr, "toolsetup:", err)
		os.Exit(1)
	}
	b, exes, err := relbuild.SetupWith(".", o)
	if err == nil {
		err = os.WriteFile(*out, b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "toolsetup:", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d bytes, yakbd %s, product code %s, %d extra file(s), %d shortcut(s)\n", *out, len(b), *version, installer.ToolProductCode(), len(o.Extra), len(o.Shortcuts))
	for _, name := range relbuild.WindowsBuilds {
		if e, ok := exes[name]; ok {
			sum := sha256.Sum256(e)
			fmt.Printf("yakbd.exe %s %s\n", name, hex.EncodeToString(sum[:]))
		}
	}
}
