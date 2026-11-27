package dllkbl

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// rebuildPolicy accepts what an imported file legitimately reports when it is built again.
var rebuildPolicy = &model.OnLoss{Kinds: map[string]string{"windows.*": "accept", "general.*": "accept"}}

// corpus returns every layout of the test corpus as a model, with a LANGID.
func corpus(t *testing.T) map[string]*model.Layout {
	t.Helper()
	out := map[string]*model.Layout{}
	globs := []string{"../../testdata/reference-spec/*/*.klc", "../../testdata/kbl-spec/examples/*.kbl.json",
		"../../testdata/third_party/cldr-release-49/keyboards/3.0/*.xml"}
	for _, g := range globs {
		files, _ := filepath.Glob(g)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(f)
			var l *model.Layout
			switch {
			case strings.HasSuffix(f, ".klc"):
				kf, r := klc.Parse(b, name)
				if kf == nil || r.HasErrors() {
					continue
				}
				l, _ = klckbl.FromKLC(kf, name)
			case strings.HasSuffix(f, ".kbl.json"):
				l, _ = kbljson.Read(b, name)
			default:
				l, _ = cldr.Read(b, name)
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
				l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
			}
			out[name] = l
		}
	}
	return out
}

// roundTrip imports a DLL, builds the result again and returns the differences of the tables, the import report
// and the rebuilt tables.
func roundTrip(t *testing.T, dll []byte, name string) ([]string, *loss.Report, *windows.Tables) {
	t.Helper()
	orig, _, err := windows.Read(dll)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	l, r := FromDLL(dll, name, Options{LangID: "0409", KeepKeyboardType: true})
	if l == nil {
		t.Fatalf("%s: import refused: %s", name, r.Summary())
	}
	// The imported file must be a valid .kbl.json and read back to the same model.
	data, wr := kbljson.Write(l)
	if data == nil || wr.HasErrors() {
		t.Fatalf("%s: the imported file does not write: %s", name, wr.Summary())
	}
	l2, rr := kbljson.Read(data, name+".kbl.json")
	if l2 == nil || rr.HasErrors() {
		t.Fatalf("%s: the imported file does not read back: %s\n%s", name, rr.Summary(), data)
	}
	res, br := build.Lower(l2, build.Options{}) // no blanket policy: the imported file itself must build
	if res == nil {
		t.Fatalf("%s: the imported file does not build: %s", name, br.Summary())
	}
	return windows.Differences(&orig.Tables, &res.DLL.Tables), r, &res.DLL.Tables
}

// TestCorpusRoundTrip: every corpus layout, built for every architecture, imports into a file whose build gives
// the same tables.
func TestCorpusRoundTrip(t *testing.T) {
	n := 0
	for name, l := range corpus(t) {
		res, _ := build.Lower(l, build.Options{Policy: rebuildPolicy})
		if res == nil {
			continue
		}
		for _, a := range windows.Arches {
			dll, cr := windows.Compile(res.DLL, a)
			if dll == nil {
				t.Logf("%s %v: not compiled: %s", name, a, cr.Summary())
				continue
			}
			n++
			diffs, r, _ := roundTrip(t, dll, res.DLL.FileName)
			if len(diffs) > 0 {
				t.Errorf("%s %v: tables differ after the round trip: %v\n%s", name, a, diffs, r.Summary())
			}
			for _, e := range r.Entries {
				if !expected[e.Code] && e.Severity >= loss.Warning {
					t.Errorf("%s %v: the tool's own DLL reports %s", name, a, e)
				}
			}
		}
	}
	if n < 60 {
		t.Fatalf("only %d DLLs in the corpus round trip", n)
	}
	t.Logf("%d DLLs round-tripped", n)
}

func sortedCodes(r *loss.Report) []string {
	var out []string
	for _, e := range r.Entries {
		out = append(out, e.Code)
	}
	sort.Strings(out)
	return out
}
