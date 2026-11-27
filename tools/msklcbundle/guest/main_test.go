package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

// TestMain lets the test binary stand in for kbdutool (TM_FAKE_KBDUTOOL=1): it builds in.klc with yakbd's own
// writer for the mode's build and leaves the DLL in the working folder, as kbdutool does. A dry run of the guest
// steps then compares yakbd with itself, so every import must pass.
func TestMain(m *testing.M) {
	if os.Getenv("TM_FAKE_KBDUTOOL") == "1" {
		args := os.Args[1:]
		target := map[string]string{"-m": "x64", "-o": "wow64", "-x": "x86"}[args[len(args)-2]]
		tmp, _ := os.MkdirTemp("", "fake")
		var o, e bytes.Buffer
		if cli.Run([]string{"compile", "-q", "-target", target, "-langid", "0409", "-accept", "windows.vkMissing", "-o", tmp, args[len(args)-1]}, &o, &e) != 0 {
			os.Stdout.WriteString("error 2000: fake\n")
			os.Exit(1)
		}
		dlls, _ := filepath.Glob(filepath.Join(tmp, target, "*.dll"))
		for _, d := range dlls {
			b, _ := os.ReadFile(d)
			os.WriteFile(filepath.Base(d), b, 0o644)
		}
		os.RemoveAll(tmp)
		os.Stdout.WriteString("warning 1009: fake\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestDryRun runs the guest steps on the bundle that tools/msklcbundle writes, with the test binary standing in
// for kbdutool (TestMain): MSKLC is then yakbd, so every DLL must import as the same layout, and the typing step
// must write manifests whose paths resolve (the runners themselves need Windows).
func TestDryRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds yakbd and runs every case")
	}
	bundle := filepath.Join(t.TempDir(), "b")
	if out, err := exec.Command("go", "run", "..", "-repo", "../../..", "-exes=false", "-o", bundle).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bundle, "yakbd.exe"), "../../../cmd/yakbd").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	self, _ := os.Executable()
	t.Setenv("TM_FAKE_KBDUTOOL", "1")
	var o, e bytes.Buffer
	code := run([]string{"-bundle", bundle, "-kbdutool", self, "-steps", "control,build,type,import"}, &o, &e)
	os.Unsetenv("TM_FAKE_KBDUTOOL")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, o.String(), e.String())
	}
	read := func(n string) string {
		b, _ := os.ReadFile(filepath.Join(bundle, "results", n))
		return string(b)
	}
	if c := read("control.txt"); !strings.Contains(c, " 0 failed,") || strings.Contains(c, "FAIL") {
		t.Errorf("control.txt:\n%s", c)
	}
	b := read("build.txt")
	if !strings.Contains(b, "kbdtst: msklc -m built (exit 0, W1009)") || !strings.Contains(b, "mod-two: msklc -m nothing (exit 1, E2000)") ||
		!strings.Contains(b, "[expected: D29") {
		t.Errorf("build.txt:\n%s", b)
	}
	im := read("import.txt")
	if strings.Contains(im, "FAIL") || !strings.Contains(im, "  import same: ") {
		t.Errorf("import.txt:\n%s", im)
	}
	t.Logf("control: %s", lastLine(read("control.txt")))
	for _, set := range []string{"msklc", "yakbd"} {
		dir := filepath.Join(bundle, "work", "set-"+set)
		mf, err := os.ReadFile(filepath.Join(dir, "manifest.txt"))
		if err != nil {
			t.Fatal(err)
		}
		entries, err := wincheck.ReadManifest(bytes.NewReader(mf))
		if err != nil || len(entries) < 25 {
			t.Fatalf("%s: %d entries, %v", set, len(entries), err)
		}
		for _, en := range entries {
			paths := []string{en.Expect}
			for _, b := range []string{"x64", "wow64"} {
				if en.DLL[b] == "" {
					t.Errorf("%s %s: no %s DLL", set, en.Name, b)
				}
				paths = append(paths, en.DLL[b])
			}
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
					t.Errorf("%s %s: %v", set, en.Name, err)
				}
			}
		}
		t.Logf("%s: %d layouts to type", set, len(entries))
	}
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	return l[len(l)-1]
}
