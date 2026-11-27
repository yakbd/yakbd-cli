// Command release builds every asset of a release into one folder (docs/release.md). Run from the repository root:
//
//	go run ./tools/release -version 0.1.0-preview.1 -o DIR
//	go run ./tools/release -version 0.1.0-preview.1 -check   # only the version rules below
//
// It writes, for version V:
//
//	yakbd-setup.exe               the tool's installer (x86, x64 and arm64 builds of yakbd.exe; docs/installer-cli.md §12)
//	yakbd-V-windows-x86.zip       yakbd.exe (the same bytes as in the installer), LICENSE, NOTICE
//	yakbd-V-windows-x64.zip
//	yakbd-V-windows-arm64.zip
//	yakbd-V-linux-amd64           the command-line tool for Linux and macOS
//	yakbd-V-linux-arm64
//	yakbd-V-darwin-amd64
//	yakbd-V-darwin-arm64
//	SHA256SUMS                    one "<sha256>  <name>" line per file above, sorted by name
//
// Every build has fixed flags and environment (internal/relbuild) and the zips have fixed times and attributes, so
// two runs on the same commit with the same Go give the same bytes, in any folder; the release workflow checks it.
// V is a semantic version without "v". It must equal the tool's own version (internal/cli VersionString, e.g.
// 0.1.0-preview.1), except for a rehearsal (V contains "-rehearsal."), which may be anything.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/relbuild"
)

// versionRE is a semantic version (https://semver.org) without build metadata.
var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// checkVersion applies the rules of the package comment.
func checkVersion(v string) error {
	if !versionRE.MatchString(v) {
		return fmt.Errorf("version %q is not a semantic version such as 0.2.0 or 0.2.0-preview.1 (no leading v)", v)
	}
	if strings.Contains(v, "-rehearsal.") {
		return nil
	}
	if tool := cli.VersionString(); v != tool {
		return fmt.Errorf("version %s is not the tool's own version %s (internal/cli Version, PreRelease): change one of them", v, tool)
	}
	return nil
}

// zipTime is the modification time of every zip entry.
var zipTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// zipFiles writes a zip of the given files (name, bytes) in the given order with fixed times and attributes.
func zipFiles(files [][2]any) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		h := &zip.FileHeader{Name: f[0].(string), Method: zip.Deflate, Modified: zipTime}
		h.SetMode(0o644)
		if strings.HasSuffix(h.Name, ".exe") {
			h.SetMode(0o755)
		}
		fw, err := w.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(f[1].([]byte)); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sums is the SHA256SUMS file of the given assets.
func sums(assets map[string][]byte) []byte {
	names := make([]string, 0, len(assets))
	for n := range assets {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	for _, n := range names {
		h := sha256.Sum256(assets[n])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h[:]), n)
	}
	return b.Bytes()
}

// Assets builds every asset of version v from the module at root.
func Assets(root, v string) (map[string][]byte, error) {
	if err := checkVersion(v); err != nil {
		return nil, err
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return nil, err
	}
	notice, err := os.ReadFile(filepath.Join(root, "NOTICE"))
	if err != nil {
		return nil, err
	}
	assets := map[string][]byte{}
	setup, exes, err := relbuild.Setup(root, v, v)
	if err != nil {
		return nil, err
	}
	assets["yakbd-setup.exe"] = setup
	for _, b := range relbuild.WindowsBuilds {
		z, err := zipFiles([][2]any{{"yakbd.exe", exes[b]}, {"LICENSE", license}, {"NOTICE", notice}})
		if err != nil {
			return nil, err
		}
		assets[fmt.Sprintf("yakbd-%s-windows-%s.zip", v, b)] = z
	}
	for _, t := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
		b, err := relbuild.Build(root, "./cmd/yakbd", t[0], t[1], false, v)
		if err != nil {
			return nil, err
		}
		assets[fmt.Sprintf("yakbd-%s-%s-%s", v, t[0], t[1])] = b
	}
	assets["SHA256SUMS"] = sums(assets)
	return assets, nil
}

func main() {
	version := flag.String("version", "", "release version without v, e.g. 0.2.0 or 0.2.0-preview.1")
	out := flag.String("o", "", "output folder (created; must not exist)")
	check := flag.Bool("check", false, "only check the version")
	flag.Parse()
	if *check {
		if err := checkVersion(*version); err != nil {
			fmt.Fprintln(os.Stderr, "release:", err)
			os.Exit(1)
		}
		return
	}
	if *version == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/release -version V -o DIR")
		os.Exit(2)
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintf(os.Stderr, "release: %s exists\n", *out)
		os.Exit(1)
	}
	assets, err := Assets(".", *version)
	if err == nil {
		err = os.MkdirAll(*out, 0o755)
	}
	for n, b := range assets {
		if err != nil {
			break
		}
		mode := os.FileMode(0o644)
		if strings.Contains(n, "-linux-") || strings.Contains(n, "-darwin-") {
			mode = 0o755
		}
		err = os.WriteFile(filepath.Join(*out, n), b, mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
	os.Stdout.Write(assets["SHA256SUMS"])
}
