package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// TestBundle: the bundle has one manifest entry per typed case of the conformance test, each with its three DLLs
// and a one-line expectations file that parses; cases.txt lists the skipped ones too.
func TestBundle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "b")
	var o, e bytes.Buffer
	if code := run([]string{"-o", out, "-repo", "../.."}, &o, &e, false); code != 0 {
		t.Fatalf("%s%s", o.String(), e.String())
	}
	mf, _ := os.ReadFile(filepath.Join(out, "manifest.txt"))
	entries, err := wincheck.ReadManifest(bytes.NewReader(mf))
	if err != nil || len(entries) != 12 {
		t.Fatalf("%d entries, %v", len(entries), err)
	}
	for _, en := range entries {
		for _, b := range wincheck.Builds[:3] {
			if _, err := os.Stat(filepath.Join(out, en.DLL[b])); err != nil {
				t.Errorf("%s: %s DLL missing", en.Name, b)
			}
		}
		x, _ := os.ReadFile(filepath.Join(out, en.Expect))
		lines, err := wincheck.ParseExpectations(bytes.NewReader(x))
		if err != nil || len(lines) != 1 || lines[0].Seq == nil {
			t.Errorf("%s: %v %v", en.Name, lines, err)
			continue
		}
		// The DLL files the guest loads, read back, type the case in the emulator: a FAIL on Windows can then only
		// mean that Windows differs from the reference specification's rules.
		for _, b := range wincheck.Builds[:3] {
			data, _ := os.ReadFile(filepath.Join(out, en.DLL[b]))
			d, _, err := windows.Read(data)
			if err != nil {
				t.Errorf("%s %s: %v", en.Name, b, err)
				continue
			}
			if diffs := wincheck.Check(lines, &wincheck.Emulator{T: &d.Tables}); len(diffs) > 0 {
				t.Errorf("%s %s: %s", en.Name, b, diffs[0])
			}
		}
	}
	table, _ := os.ReadFile(filepath.Join(out, "cases.txt"))
	if !strings.Contains(string(table), "# 12 typed, 2 skipped") {
		t.Errorf("cases.txt:\n%s", table)
	}
	if _, err := os.Stat(filepath.Join(out, "control", "us.expect.txt")); err != nil {
		t.Error(err)
	}
	if code := run([]string{"-o", out, "-repo", "../.."}, &o, &e, false); code != 2 {
		t.Error("an existing folder was overwritten")
	}
}
