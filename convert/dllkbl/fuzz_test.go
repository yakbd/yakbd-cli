package dllkbl

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// FuzzImport: any bytes go through the reader, the converter, the .kbl.json writer and the Windows build within
// the bounds of docs/fuzzing.md. Seeds: foreign-shaped and own DLLs of random tables for every architecture, and
// the x64 and ARM64X builds of every .klc file of the reference specification's corpus.
func FuzzImport(f *testing.F) {
	for seed := int64(1); seed <= 6; seed++ {
		a := windows.Arches[seed%3]
		f.Add(encodeForeign(randomTables(seed), shape{arch: a, seed: seed, pad: 3, stub: "", extra: seed == 3}))
	}
	d := &windows.DLL{FileName: "f.dll", Tables: *randomTables(9)}
	for _, a := range append(append([]windows.Arch(nil), windows.Arches...), windows.ARM64, windows.ARM64X) {
		b, r := windows.Compile(d, a)
		if b == nil {
			f.Fatalf("no %s seed: %s", a, r.Summary())
		}
		f.Add(b)
	}
	for _, p := range fuzzcheck.Files(f, "../../testdata/reference-spec/*/*.klc") {
		for _, b := range klcBuilds(f, p) {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			l, r := FromDLL(b, "fuzz.dll", Options{LangID: "0409"})
			if l == nil {
				if !r.HasErrors() {
					return fmt.Errorf("refused without an error")
				}
				return nil
			}
			if data, wr := kbljson.Write(l); data == nil || wr.HasErrors() {
				return fmt.Errorf("an imported layout does not write: %s", wr.Summary())
			}
			build.Lower(l, build.Options{Policy: rebuildPolicy})
			return nil
		})
	})
}

// klcBuilds returns the x64 and ARM64X DLLs of a .klc file, none when it does not build.
func klcBuilds(f *testing.F, path string) [][]byte {
	k, r := klc.Parse(fuzzcheck.Read(f, []string{path})[0], filepath.Base(path))
	if k == nil || r.HasErrors() {
		return nil
	}
	l, _ := klckbl.FromKLC(k, filepath.Base(path))
	if l == nil {
		return nil
	}
	if l.Targets == nil {
		l.Targets = &model.Targets{}
	}
	if l.Targets.Windows == nil {
		l.Targets.Windows = &model.WindowsTarget{}
	}
	if l.Targets.Windows.Registration == nil {
		l.Targets.Windows.Registration = &model.Registration{}
	}
	if l.Targets.Windows.Registration.LangID == "" {
		l.Targets.Windows.Registration.LangID = "0409" // as yakbd compile -langid 0409
	}
	res, _ := build.Lower(l, build.Options{})
	if res == nil {
		return nil
	}
	var out [][]byte
	for _, a := range []windows.Arch{windows.AMD64, windows.ARM64X} {
		if b, _ := windows.Compile(res.DLL, a); b != nil {
			out = append(out, b)
		}
	}
	return out
}
