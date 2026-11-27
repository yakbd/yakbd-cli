package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// importFake adds what the import check needs to the fake system: the layout files of its "system directory"
// and keyboards on the emulator.
type importFake struct {
	*fake
	layouts map[string][]byte
}

func (f *importFake) LayoutFiles() (map[string][]byte, error) { return f.layouts, nil }

func (f *importFake) Keyboard(hkl uintptr) wincheck.Keyboard {
	return emuKeyboard{f.Prober(hkl).(*wincheck.Emulator)}
}

type emuKeyboard struct{ e *wincheck.Emulator }

func (k emuKeyboard) Translate(v byte, st *[256]byte, flags uint32) (int, []uint16) {
	var mods []byte
	for _, m := range []byte{0x10, 0x11, 0x12} {
		if st[m]&0x80 != 0 {
			mods = append(mods, m)
		}
	}
	return k.e.ToUnicode(wincheck.Press{VK: v, Mods: mods}, flags)
}

func (k emuKeyboard) ScanToVK(sc uint16) uint32 {
	if int(sc) < len(k.e.T.ScanToVK) {
		return uint32(k.e.T.ScanToVK[sc] & 0xFF)
	}
	return 0xFF
}

// cliRebuild is execRebuild with the command line run in this process.
func cliRebuild(t *testing.T, mutate func(name string, b []byte) []byte) rebuildFunc {
	return func(name string, orig []byte, build string) ([]byte, string, []string) {
		dir := t.TempDir()
		in := filepath.Join(dir, strings.ToLower(name))
		os.WriteFile(in, orig, 0o644)
		kbl := filepath.Join(dir, "layout.kbl.json")
		var o, e bytes.Buffer
		if cli.Run([]string{"convert", "-q", "-report", filepath.Join(dir, "i.json"), "-langid", "0409", "-keep-keyboard-type", in, kbl}, &o, &e) != 0 {
			return nil, "import refused", reportKinds(filepath.Join(dir, "i.json"))
		}
		out := filepath.Join(dir, "out")
		if cli.Run([]string{"compile", "-q", "-o", out, "-target", build, "-langid", "0409", "-accept", "windows.*,general.*", kbl}, &o, &e) != 0 {
			return nil, "build refused", nil
		}
		dlls, _ := filepath.Glob(filepath.Join(out, build, "*.dll"))
		b, _ := os.ReadFile(dlls[0])
		if mutate != nil {
			b = mutate(name, b)
		}
		return b, "", reportKinds(filepath.Join(dir, "i.json"))
	}
}

func TestImportCheckWithEmulator(t *testing.T) {
	dir := t.TempDir()
	var o, e bytes.Buffer
	if code := cli.Run([]string{"compile", "-q", "-o", dir, "-langid", "0409", "../../testdata/reference-spec/vm/kbdtst.klc"}, &o, &e); code != 0 {
		t.Fatal(e.String())
	}
	for _, build := range []string{"x64", "wow64"} {
		good, _ := os.ReadFile(filepath.Join(dir, build, "kbdtst.dll"))
		f := &importFake{fake: &fake{build: build, files: map[string][]byte{}, klids: map[string]wincheck.Slot{}},
			layouts: map[string][]byte{"KBDTST.DLL": good, "kbdbad.dll": []byte("MZ not a layout"), "kbdtwo.dll": good}}
		// kbdtwo's rebuild is broken on purpose: one cell changed.
		mutate := func(name string, b []byte) []byte {
			if name != "kbdtwo.dll" {
				return b
			}
			d, _, _ := windows.Read(b)
			for ti := range d.Tables.CharTables {
				for k := range d.Tables.CharTables[ti].Rows {
					if r := &d.Tables.CharTables[ti].Rows[k]; r.VK == 'Q' {
						r.Chars[0] = 'z'
					}
				}
			}
			a := windows.AMD64
			if build == "wow64" {
				a = windows.WOW64
			}
			nb, _ := windows.Compile(d, a)
			return nb
		}
		var so, se bytes.Buffer
		res := filepath.Join(dir, "import-"+build+".txt")
		code := runImport([]string{"-out", res}, &so, &se, f, cliRebuild(t, mutate))
		out := so.String()
		t.Log(out)
		if code != 1 || strings.Contains(out, "KBDTST") || !strings.Contains(out, "FAIL kbdbad.dll: import refused") ||
			!strings.Contains(out, "FAIL kbdtwo.dll: ") || !strings.Contains(out, "cells differ") || !strings.Contains(out, "1 of 3 files pass") ||
			!strings.Contains(out, "dll.malformed: 1 files") || !strings.Contains(out, "mechanisms (") || !strings.Contains(out, "tables differ in: character rows 1") || !strings.Contains(out, "main rows differ") {
			t.Errorf("%s: exit %d\n%s%s", build, code, out, se.String())
		}
		if len(f.files) != 0 || len(f.klids) != 0 {
			t.Errorf("%s: not cleaned up", build)
		}
		// The control run compares each original with itself.
		so.Reset()
		delete(f.layouts, "kbdbad.dll")
		if code := runImport([]string{"-control", "-out", res}, &so, &se, f, nil); code != 0 || !strings.Contains(so.String(), "2 of 2 files pass") {
			t.Errorf("%s control: %d %s", build, code, so.String())
		}
		// Nothing of a layout appears in the results: no hex units, no characters.
		var noVM7 []string
		for _, ln := range strings.Split(out, "\n") {
			if !strings.HasPrefix(ln, "VM-7") && !strings.HasPrefix(ln, "VM-8") {
				noVM7 = append(noVM7, ln)
			}
		}
		if s := strings.Join(noVM7, "\n"); regexp.MustCompile(`chars [0-9a-f]{4}|dead [0-9a-f]{4}|[0-9a-f]{4} [0-9a-f]{4}`).MatchString(s) || !strings.Contains(out, "VM-7 (") || !strings.Contains(out, "VM-8 shape B") {
			t.Errorf("%s: results quote layout data:\n%s", build, out)
		}
	}
}

// TestExecRebuild runs the real yakbd binary (built for this machine) the way the guest runs yakbd.exe.
func TestExecRebuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds yakbd")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "yakbd")
	cmd := exec.Command("go", "build", "-o", bin, "../yakbd")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var o, e bytes.Buffer
	if code := cli.Run([]string{"compile", "-q", "-o", dir, "-langid", "0409", "../../testdata/reference-spec/vm/kbdtst.klc"}, &o, &e); code != 0 {
		t.Fatal(e.String())
	}
	orig, _ := os.ReadFile(filepath.Join(dir, "wow64", "kbdtst.dll"))
	b, cat, kinds := execRebuild(bin)("KBDTST.DLL", orig, "wow64")
	if cat != "" || b == nil {
		t.Fatalf("category %q kinds %v", cat, kinds)
	}
	x, _, _ := windows.Read(orig)
	y, _, _ := windows.Read(b)
	if d := windows.Differences(&x.Tables, &y.Tables); len(d) > 0 {
		t.Errorf("rebuilt tables differ: %v", d)
	}
	if _, cat, _ := execRebuild(bin)("kbdbad.dll", []byte("MZ"), "x64"); cat != "import refused" {
		t.Errorf("bad file: %q", cat)
	}
}
