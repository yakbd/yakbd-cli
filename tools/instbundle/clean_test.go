package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCleanDryRun checks the clean-machine bundle as far as Linux can: the driver runs exactly the clean phases of
// tools/instcheck, the checker dispatches them before the groups that open other bundles' files, the files the
// checker opens are the ones writeClean puts in the bundle, and writeClean takes its release files from -assets.
func TestCleanDryRun(t *testing.T) {
	src, err := os.ReadFile("../instcheck/clean_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`var cleanPhases = \[\]string\{([^}]*)\}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("cleanPhases not found")
	}
	var phases, ran []string
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(m[1], -1) {
		phases = append(phases, string(q[1]))
	}
	for _, q := range regexp.MustCompile(`(?m)^\s*Task \$s (\S+)`).FindAllStringSubmatch(driveCleanPS1, -1) {
		ran = append(ran, q[1])
	}
	if strings.Join(ran, " ") != strings.Join(phases, " ") {
		t.Errorf("driver runs %v, checker has %v", ran, phases)
	}
	main, _ := os.ReadFile("../instcheck/main_windows.go")
	if !strings.Contains(string(main), "else if !c.plant(*phase) && !c.clean(*phase) &&") {
		t.Error("instcheck does not dispatch clean phases before the other groups")
	}
	for _, q := range regexp.MustCompile(`c\.ci\("([^"]+)"\)`).FindAllSubmatch(src, -1) {
		if !contains(cleanFiles("V"), "ci/"+string(q[1])) {
			t.Errorf("checker opens ci\\%s, which the bundle does not write", q[1])
		}
	}

	if testing.Short() {
		return
	}
	assets := t.TempDir()
	sums := "aa  yakbd-9.9.9-rehearsal.1-linux-amd64.tar.gz\nbb  yakbd-9.9.9-rehearsal.1-macos.tar.gz\ncc  yakbd-setup.exe\n"
	for f, c := range map[string]string{"yakbd-setup.exe": "x", "SHA256SUMS": sums} {
		if err := os.WriteFile(filepath.Join(assets, f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir("tools/instbundle")
	out := filepath.Join(t.TempDir(), "b")
	if err := writeClean(out, assets); err != nil {
		t.Fatal(err)
	}
	if v, _ := os.ReadFile(filepath.Join(out, "ci", "VERSION.txt")); strings.TrimSpace(string(v)) != "9.9.9-rehearsal.1" {
		t.Errorf("VERSION.txt %q", v)
	}
	for _, f := range cleanFiles("9.9.9-rehearsal.1") {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Error(err)
		}
	}
}

func contains(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}
