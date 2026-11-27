package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
)

func TestBundle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "b")
	var o, e bytes.Buffer
	if code := run([]string{"-o", out, "-repo", "../.."}, &o, &e, false); code != 0 {
		t.Fatalf("%s%s", o.String(), e.String())
	}
	mf, _ := os.ReadFile(filepath.Join(out, "manifest.txt"))
	entries, err := wincheck.ReadManifest(bytes.NewReader(mf))
	if err != nil || len(entries) < 25 {
		t.Fatalf("%d entries, %v\n%s", len(entries), err, o.String())
	}
	names := map[string]bool{}
	for _, en := range entries {
		names[en.Name] = true
		for _, b := range wincheck.Builds[:3] {
			if _, err := os.Stat(filepath.Join(out, en.DLL[b])); err != nil {
				t.Errorf("%s: %s DLL missing", en.Name, b)
			}
		}
		if _, err := os.Stat(filepath.Join(out, en.Expect)); err != nil {
			t.Errorf("%s: expectations missing", en.Name)
		}
	}
	for _, n := range []string{"kbl-minimal-qwerty", "klc-kbdtst", "cldr-pt-t-k0-abnt2", "rebase-kbdtst", "vm4-caps-dead-c0-ff", "vm4-caps-dead-c1-noff"} {
		if !names[n] {
			t.Errorf("%s missing", n)
		}
	}
	readme, _ := os.ReadFile(filepath.Join(out, "README.txt"))
	if !strings.Contains(string(readme), "yakbd-wincheck-32.exe -bundle") || !strings.Contains(string(readme), "Not in the bundle") {
		t.Errorf("README:\n%s", readme)
	}
	if code := run([]string{"-o", out, "-repo", "../.."}, &o, &e, false); code != 2 {
		t.Error("an existing folder was overwritten")
	}
}

func TestARM64Bundle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "b")
	var o, e bytes.Buffer
	if code := run([]string{"-arm64", "-o", out, "-repo", "../.."}, &o, &e, false); code != 0 {
		t.Fatalf("%s%s", o.String(), e.String())
	}
	mf, _ := os.ReadFile(filepath.Join(out, "manifest.txt"))
	entries, err := wincheck.ReadManifest(bytes.NewReader(mf))
	if err != nil || len(entries) != 15 {
		t.Fatalf("%d entries, %v", len(entries), err)
	}
	if entries[0].Name != "arm64.klc-kbdtst" || !entries[len(entries)-1].Rebase {
		t.Errorf("order: %s ... %s", entries[0].Name, entries[len(entries)-1].Name)
	}
	ec := 0
	for _, en := range entries {
		for _, b := range wincheck.Builds {
			if _, err := os.Stat(filepath.Join(out, en.DLL[b])); err != nil || en.DLL[b] == "" {
				t.Errorf("%s: %s DLL missing", en.Name, b)
			}
		}
		if strings.Contains(en.Name, "arm64x-ec.") {
			ec++
		}
		arm, _ := os.ReadFile(filepath.Join(out, en.DLL["arm64"]))
		x64, _ := os.ReadFile(filepath.Join(out, en.DLL["x64"]))
		d, a, err := windows.Read(arm)
		dx, _, errx := windows.Read(x64)
		if err != nil || errx != nil || (a != windows.ARM64X) != strings.HasPrefix(en.Name, "arm64.") {
			t.Fatalf("%s: read back as %v: %v %v", en.Name, a, err, errx)
		}
		// The candidate holds exactly the tables the expectations were made from (those of the x64 build).
		if !reflect.DeepEqual(d, dx) {
			t.Errorf("%s: the arm64 file differs from the x64 build", en.Name)
		}
	}
	if ec != len(arm64Layouts) {
		t.Errorf("%d entries for the 32-bit run (-only arm64x-ec.), want %d", ec, len(arm64Layouts))
	}
	if c, err := os.ReadFile(filepath.Join(out, "control", "us.expect.txt")); err != nil || string(c) != wincheck.ControlExpectations {
		t.Error("control expectations missing")
	}
	readme, _ := os.ReadFile(filepath.Join(out, "README.txt"))
	for _, s := range []string{"yakbd-wincheck-arm64.exe -bundle", "-only arm64x-ec.", "193", "snapshot", "-control", "control.wow64"} {
		if !strings.Contains(string(readme), s) {
			t.Errorf("README lacks %q", s)
		}
	}
}

func TestSigninBundle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "b")
	var o, e bytes.Buffer
	if code := run([]string{"-arm64", "-signin", "-o", out, "-repo", "../.."}, &o, &e, false); code != 0 {
		t.Fatalf("%s%s", o.String(), e.String())
	}
	mf, _ := os.ReadFile(filepath.Join(out, "manifest.txt"))
	entries, err := wincheck.ReadManifest(bytes.NewReader(mf))
	if err != nil || len(entries) != len(arm64Layouts)+1 || entries[0].Name != "arm64x-x64.klc-kbdtst" {
		t.Fatalf("%d entries, %v", len(entries), err)
	}
	readme, _ := os.ReadFile(filepath.Join(out, "README.txt"))
	for _, s := range []string{"-signin install", "-signin check", "-signin list -repeat 4", "AutoAdminLogon", "cold-boot", "A. x64 Hyper-V VM"} {
		if !strings.Contains(string(readme), s) {
			t.Errorf("README lacks %q", s)
		}
	}
}
