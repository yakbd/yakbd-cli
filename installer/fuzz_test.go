package installer

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// buildBound allows for what a build makes from a small file: up to five DLLs and an installer of the 3 MB stub.
var buildBound = fuzzcheck.Bound{Time: 20 * time.Second, Base: 128 << 20, PerByte: 4096}

// FuzzBuild: a .kbl.json file goes the way of `yakbd installer` (read, lower, compile every build including the
// unverified ARM64X and ARM64 ones, package, write the setup.exe) within the bounds of docs/fuzzing.md, and the
// written installer's payload decodes to a manifest that lists exactly the packaged builds. Seeds: the spec's
// examples and the .kbl.json of every .klc file of the reference specification's corpus and every CLDR keyboard
// of the test data.
func FuzzBuild(f *testing.F) {
	fuzzcheck.Seeds(f, "../testdata/kbl-spec/examples/*.kbl.json")
	for _, p := range fuzzcheck.Files(f, "../testdata/reference-spec/*/*.klc", "../testdata/third_party/cldr-*/keyboards/3.0/*.xml") {
		b := fuzzcheck.Read(f, []string{p})[0]
		if len(b) > fuzzcheck.MaxSeed {
			continue
		}
		var l *model.Layout
		if filepath.Ext(p) == ".klc" {
			if k, r := klc.Parse(b, filepath.Base(p)); k != nil && !r.HasErrors() {
				l, _ = klckbl.FromKLC(k, filepath.Base(p))
			}
		} else {
			l, _ = cldr.Read(b, filepath.Base(p))
		}
		if l == nil {
			continue
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
		if data, _ := kbljson.Write(l); data != nil && len(data) <= fuzzcheck.MaxSeed {
			f.Add(data)
		}
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, buildBound, len(b), func() error {
			l, r := kbljson.Read(b, "fuzz.kbl.json")
			if l == nil || r.HasErrors() {
				return nil
			}
			res, _ := build.Lower(l, build.Options{})
			if res == nil {
				return nil
			}
			targets := append(append([]windows.Target(nil), windows.AllTargets...), windows.TargetARM64)
			set := windows.CompileSet(res.DLL, targets, true)
			if set.Report.HasErrors() {
				return nil
			}
			pkg, pr := NewPackage(l, res, set, Options{Tool: "fuzz"})
			if pkg == nil || pr.HasErrors() {
				return nil
			}
			exe, err := ExeContainer{}.Write(pkg)
			if err != nil {
				return nil // a refusal of the container is an error message, not a crash
			}
			p, err := payload.FromExe(exe)
			if err != nil {
				return fmt.Errorf("the written installer's payload does not decode: %v", err)
			}
			if got, want := len(p.Manifest.Files), len(pkg.Builds); got != want {
				return fmt.Errorf("the installer holds %d files, the package %d", got, want)
			}
			return nil
		})
	})
}
