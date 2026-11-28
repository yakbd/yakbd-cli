package keylayoutkbl

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/target/macos/build"
)

// corpus are the openly licensed .keylayout fixtures and the macOS check's own layouts.
func corpus(t *testing.T) []string {
	a, _ := filepath.Glob("../../testdata/third_party/keylayout/*/*.keylayout")
	b, _ := filepath.Glob("../../tools/macos-check/cases/*/*.bundle/Contents/Resources/*.keylayout")
	if len(a) == 0 || len(b) == 0 {
		t.Fatal("no corpus")
	}
	return append(a, b...)
}

var accept = build.Options{}

// typeDiffs types every key code 0..127 in every build state from the base state, and after every dead key of a
// state, on both keyboards, and lists where they differ.
func typeDiffs(a, b *keylayout.Keyboard) []string {
	var diffs []string
	press := func(k *keylayout.Keyboard, seq [][2]int) string {
		ty := keylayout.NewTypist(k)
		var out strings.Builder
		for _, p := range seq {
			out.WriteString(ty.Press(p[0], macState(p[1])))
		}
		return out.String() + "|" + ty.State
	}
	norm := func(s string) string { // the state name after "|" is the writer's choice: keep only none or not
		i := strings.LastIndexByte(s, '|')
		if s[i+1:] == "none" || s[i+1:] == "" {
			return s[:i] + "|none"
		}
		return s[:i] + "|dead"
	}
	var starts [][2]int
	for s := 0; s < build.States; s++ {
		for c := 0; c < 128; c++ {
			seq := [][2]int{{c, s}}
			x, y := norm(press(a, seq)), norm(press(b, seq))
			if x != y {
				diffs = append(diffs, fmt.Sprintf("%d in state %d: %q vs %q", c, s, x, y))
			}
			if strings.HasSuffix(x, "|dead") && s&(build.StControl|build.StCommand) == 0 {
				starts = append(starts, [2]int{c, s})
			}
		}
	}
	for _, st := range starts {
		for _, s := range []int{0, 1, 2, 3, 16} {
			for c := 0; c < 128; c++ {
				seq := [][2]int{st, {c, s}}
				x, y := norm(press(a, seq)), norm(press(b, seq))
				if x != y {
					diffs = append(diffs, fmt.Sprintf("%v then %d in state %d: %q vs %q", st, c, s, x, y))
				}
			}
		}
	}
	return diffs
}

func warnings(r *loss.Report) []string {
	var w []string
	for _, e := range r.Entries {
		if e.Severity >= loss.Warning {
			w = append(w, e.Code)
		}
	}
	sort.Strings(w)
	return w
}

// TestCorpus: every fixture imports to a schema-valid .kbl.json, builds again, and types the same as the original
// wherever the import reported no warning-level loss; a second round trip is byte-stable.
func TestCorpus(t *testing.T) {
	for _, f := range corpus(t) {
		t.Run(filepath.Base(filepath.Dir(f))+"/"+filepath.Base(f), func(t *testing.T) {
			data, _ := os.ReadFile(f)
			k, r := keylayout.Read(data, f)
			if k == nil {
				t.Fatal(r.Summary())
			}
			l, ir := FromKeylayout(k, filepath.Base(f), "")
			ir.Merge(l.Check())
			if ir.HasErrors() {
				t.Fatalf("import: %s", ir.Summary())
			}
			js, jr := kbljson.Write(l)
			if js == nil || jr.HasErrors() {
				t.Fatalf("kbl.json: %s", jr.Summary())
			}
			l2, rr := kbljson.Read(js, "x.kbl.json")
			if rr.HasErrors() {
				t.Fatalf("reread: %s", rr.Summary())
			}
			out, br := ToKeylayout(l2, accept)
			if out == nil {
				t.Fatalf("build: %s", br.Summary())
			}
			k2, _ := keylayout.Read(out, "built")
			// One key press types the same everywhere; a dead key followed by a key may differ only where the import
			// reported a dead-key shape the model cannot say (coverage/macos.md §4).
			diffs := typeDiffs(k, k2)
			w := warnings(ir)
			var single, pairs []string
			for _, d := range diffs {
				if strings.HasPrefix(d, "[") {
					pairs = append(pairs, d)
				} else {
					single = append(single, d)
				}
			}
			if len(single) > 0 {
				t.Errorf("%d single key presses type differently, first: %s", len(single), strings.Join(single[:min(5, len(single))], "; "))
			}
			if len(pairs) > 0 {
				if !slices.Contains(w, "macos.stateNotExpressible") {
					t.Errorf("%d dead-key sequences type differently without macos.stateNotExpressible, first: %s", len(pairs), strings.Join(pairs[:min(5, len(pairs))], "; "))
				} else {
					t.Logf("%d dead-key sequences differ (macos.stateNotExpressible); first: %s", len(pairs), strings.Join(pairs[:min(3, len(pairs))], "; "))
				}
			}
			// second round: built file -> kbl -> built file is stable
			l3, r3 := FromKeylayout(k2, "built", l.Metadata.ID)
			if r3.HasErrors() {
				t.Fatal(r3.Summary())
			}
			out2, _ := ToKeylayout(l3, accept)
			if string(out2) != string(out) {
				t.Errorf("second round trip differs")
				if os.Getenv("DUMP") != "" {
					os.WriteFile(filepath.Join(os.Getenv("DUMP"), filepath.Base(f)+".1"), out, 0o644)
					os.WriteFile(filepath.Join(os.Getenv("DUMP"), filepath.Base(f)+".2"), out2, 0o644)
				}
			}
		})
	}
}

// TestExamplesRoundTrip: each spec example that builds for macOS gives a .keylayout that imports and builds again
// to the same bytes (.kbl.json -> .keylayout -> .kbl.json -> .keylayout).
func TestExamplesRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/kbl-spec/examples/*.kbl.json")
	built := 0
	for _, f := range files {
		data, _ := os.ReadFile(f)
		l, r := kbljson.Read(data, f)
		if r.HasErrors() {
			t.Fatal(r.Summary())
		}
		out, _ := ToKeylayout(l, accept)
		if out == nil {
			continue
		}
		built++
		k, kr := keylayout.Read(out, "built")
		if k == nil {
			t.Fatalf("%s: %s", f, kr.Summary())
		}
		l2, ir := FromKeylayout(k, "built", l.Metadata.ID)
		if w := warnings(ir); len(w) > 0 {
			t.Errorf("%s: the import of a built file reports %v", filepath.Base(f), w)
		}
		js, jr := kbljson.Write(l2)
		if js == nil {
			t.Fatalf("%s: %s", f, jr.Summary())
		}
		l3, _ := kbljson.Read(js, "x.kbl.json")
		out2, br := ToKeylayout(l3, accept)
		if out2 == nil {
			t.Fatalf("%s: %s", f, br.Summary())
		}
		if string(out2) != string(out) {
			k2, _ := keylayout.Read(out2, "again")
			t.Errorf("%s: the round trip changes the .keylayout; typing differences: %v", filepath.Base(f), typeDiffs(k, k2))
		}
	}
	if built < 4 {
		t.Errorf("only %d examples build", built)
	}
}
