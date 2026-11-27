// Command toolsetup builds the YAKBD installers (docs/installer-cli.md §12): yakbd-cli-setup.exe, the CLI's
// installer (yakbd.exe for windows/386, amd64 and arm64 with LICENSE and NOTICE), and with -editor
// yakbd-gui-setup.exe, the editor's installer (the editor component besides the CLI). Every program is built with
// fixed flags and environment (internal/relbuild: the same source and Go give the same bytes), then the installer
// program installer/toolstub gets its resources (installer/exeres) and the tool payload. Run from the repository
// root:
//
//	go run ./tools/toolsetup [-o FILE] [-version V] [-builds x64,arm64] [-editor BUILD=DIR]... [-editor-json FILE]
//
// -version sets the CLI component's version (default: the tool's own); test bundles use it to make an older and a
// newer installer of the same yakbd.exe. With the tool's own version, yakbd.exe is built as a release builds it, so
// that at a release tag and with no other flag the output is that release's yakbd-cli-setup.exe byte for byte. A
// release builds it through tools/release (docs/release.md).
//
// -builds limits the builds of yakbd.exe. -editor BUILD=DIR adds every regular file under DIR (recursively, in name
// order) as the editor's files for machines of that build, installed below Program Files\YAKBD\Editor at its path
// below DIR; -editor-json names the editor's declaration (docs/installer-cli.md §12.1): its version, Start-menu
// shortcuts and per-user settings. The default output name follows: yakbd-gui-setup.exe with -editor. It prints the
// SHA-256 of every yakbd.exe build it carries.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Write builds yakbd-cli-setup.exe with the given CLI version.
func Write(version string) ([]byte, error) {
	b, _, err := relbuild.Setup(".", version, "")
	return b, err
}

// readDir reads every regular file below dir in name order, paths with backslashes.
func readDir(dir string) ([]toolsetup.Input, error) {
	var out []toolsetup.Input
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
		out = append(out, toolsetup.Input{Path: strings.ReplaceAll(filepath.ToSlash(rel), "/", `\`), Data: data})
		return nil
	})
	return out, err
}

// EditorJSON is the editor's declaration for -editor-json (docs/installer-cli.md §12.1).
type EditorJSON struct {
	Version   string               `json:"version"`
	Shortcuts []EditorShortcut     `json:"shortcuts"`
	UserData  []toolsetup.UserData `json:"userData"`
}

// EditorShortcut is a Start-menu shortcut to a file of the editor (target relative to its folder).
type EditorShortcut struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// readEditor reads -editor BUILD=DIR values and the declaration.
func readEditor(specs []string, declPath string) (*relbuild.EditorInput, error) {
	if len(specs) == 0 {
		if declPath != "" {
			return nil, fmt.Errorf("-editor-json without -editor")
		}
		return nil, nil
	}
	if declPath == "" {
		return nil, fmt.Errorf("-editor needs -editor-json (the editor's version, shortcuts and settings)")
	}
	b, err := os.ReadFile(declPath)
	if err != nil {
		return nil, err
	}
	var d EditorJSON
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("%s: %v", declPath, err)
	}
	e := &relbuild.EditorInput{Version: d.Version, Files: map[string][]toolsetup.Input{}, UserData: d.UserData}
	for _, sc := range d.Shortcuts {
		e.Shortcuts = append(e.Shortcuts, toolsetup.Shortcut{Name: sc.Name, Target: strings.ReplaceAll(sc.Target, "/", `\`)})
	}
	for _, spec := range specs {
		build, dir, ok := strings.Cut(spec, "=")
		if !ok || relbuild.WindowsArch[build] == "" {
			return nil, fmt.Errorf("-editor %q: want BUILD=DIR with BUILD x64 or arm64", spec)
		}
		if _, dup := e.Files[build]; dup {
			return nil, fmt.Errorf("-editor %s given twice", build)
		}
		if e.Files[build], err = readDir(dir); err != nil {
			return nil, fmt.Errorf("-editor %s: %v", spec, err)
		}
	}
	return e, nil
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	out := flag.String("o", "", "output file (default yakbd-cli-setup.exe, with -editor yakbd-gui-setup.exe)")
	version := flag.String("version", cli.VersionString(), "the CLI component's version")
	builds := flag.String("builds", "", "comma-separated builds of yakbd.exe to carry (x86, x64, arm64); default: all three")
	var editors list
	flag.Var(&editors, "editor", "BUILD=DIR: the editor's files for machines of BUILD (repeatable)")
	decl := flag.String("editor-json", "", "the editor's declaration: version, shortcuts, per-user settings")
	flag.Parse()
	o := relbuild.SetupOptions{Version: *version}
	if *version == cli.VersionString() {
		o.Release = *version
	}
	if *builds != "" {
		o.Builds = strings.Split(*builds, ",")
	}
	var err error
	if o.Editor, err = readEditor(editors, *decl); err != nil {
		fmt.Fprintln(os.Stderr, "toolsetup:", err)
		os.Exit(1)
	}
	if *out == "" {
		*out = relbuild.CLISetupName
		if o.Editor != nil {
			*out = relbuild.GUISetupName
		}
	}
	b, exes, err := relbuild.SetupWith(".", o)
	if err == nil {
		err = os.WriteFile(*out, b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "toolsetup:", err)
		os.Exit(1)
	}
	ed := "no editor"
	if o.Editor != nil {
		ed = "editor " + o.Editor.Version
	}
	fmt.Printf("%s: %d bytes, CLI %s, %s, product code %s\n", *out, len(b), *version, ed, installer.SuiteProductCode())
	for _, name := range relbuild.WindowsBuilds {
		if e, ok := exes[name]; ok {
			sum := sha256.Sum256(e)
			fmt.Printf("yakbd.exe %s %s\n", name, hex.EncodeToString(sum[:]))
		}
	}
}
