package build

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
)

func read(t *testing.T, path string) *model.Layout {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l, r := kbljson.Read(b, path)
	if l == nil {
		t.Fatal(r.Summary())
	}
	return l
}

func kinds(r *loss.Report) string { return strings.Join(r.Codes(), " ") }

// TestExamples builds the specification's examples; pt-abnt2 (imported from CLDR) needs pins the file does not
// have (a LANGID and the VK of IntlRo), as the spec intends.
func TestExamples(t *testing.T) {
	tests := []struct {
		file   string
		ok     bool
		kinds  []string
		builds int
	}{
		{"minimal-qwerty", true, []string{"windowsDefault.vk", "windowsDefault.builds"}, 4},
		{"latin-altgr-deadkeys", true, []string{"windowsDefault.altGr", "windows.deadKeyNoMatchExpanded", "windows.deadKeyFirstUnitComposes"}, 4},
		{"windows-features", true, []string{"windows.nlsNotCompiled", "windows.pinOverridesModel", "windows.altOnlyLayer", "windows.bidiMarksConflict"}, 0},
		{"cldr-pt-abnt2", false, []string{"windows.langIdMissing", "windows.vkMissing", "windows.markerNotCompiled"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			l := read(t, "../../../testdata/kbl-spec/examples/"+tt.file+".kbl.json")
			res, r := Lower(l, Options{})
			if (res != nil) != tt.ok {
				t.Fatalf("ok = %v:\n%s", res != nil, r.Summary())
			}
			for _, k := range tt.kinds {
				if !strings.Contains(kinds(r), k) {
					t.Errorf("missing %s in %s", k, kinds(r))
				}
			}
			if res == nil {
				return
			}
			set := windows.CompileAll(res.DLL, res.Targets)
			if set.Report.HasErrors() || (tt.builds > 0 && len(set.Builds) != tt.builds) {
				t.Fatalf("%d builds: %s", len(set.Builds), set.Report.Summary())
			}
			for _, bld := range set.Builds {
				got, _, err := windows.Read(bld.Bytes)
				if err != nil || !reflect.DeepEqual(got, res.DLL) {
					t.Errorf("%v: read back differs (%v)", bld.Target, err)
				}
			}
		})
	}
}

// TestPin checks that a pinned file builds the same DLL with no windowsDefault entry, and that the pinned file is
// valid and stable under a second pin.
func TestPin(t *testing.T) {
	for _, name := range []string{"minimal-qwerty", "latin-altgr-deadkeys", "windows-features"} {
		t.Run(name, func(t *testing.T) {
			l := read(t, "../../../testdata/kbl-spec/examples/"+name+".kbl.json")
			res, _ := Lower(l, Options{})
			pinned, r := Pin(l, Options{})
			if pinned == nil {
				t.Fatal(r.Summary())
			}
			out, wr := kbljson.Write(pinned)
			if out == nil {
				t.Fatalf("pinned file invalid: %s", wr.Summary())
			}
			again, _ := kbljson.Read(out, "pinned")
			res2, r2 := Lower(again, Options{})
			if res2 == nil {
				t.Fatal(r2.Summary())
			}
			for _, e := range r2.Entries {
				if e.Class == loss.Defaulted {
					t.Errorf("pinned file still uses %s: %s", e.Code, e.Message)
				}
			}
			a, _ := windows.Compile(res.DLL, windows.AMD64)
			b, _ := windows.Compile(res2.DLL, windows.AMD64)
			if !bytes.Equal(a, b) {
				t.Errorf("the pinned file builds a different DLL")
			}
			p2, _ := Pin(again, Options{})
			out2, _ := kbljson.Write(p2)
			if !kbljson.SameJSON(out, out2) {
				t.Errorf("second pin changes the file at %s", kbljson.DiffJSON(out, out2))
			}
		})
	}
}

func TestDLLName(t *testing.T) {
	for in, want := range map[string]string{"pt-t-k0-abnt2": "pttk0abn", "123": "k123", "": "k", "fr": "fr"} {
		if got := DefaultDLLName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if localeOf("pt-t-k0-abnt2") != "pt" || localeOf("en-US") != "en-US" {
		t.Error("localeOf")
	}
}

const tiny = `{"kbl":"0.1","metadata":{"id":"en","name":"T"},
 "positions":{"KeyQ":{"scanCode":"10"},"KeyW":{"scanCode":"11"},"Backquote":{"scanCode":"29"}},
 "hardware":{"layers":[
  {"modifiers":[[]],"bindings":{"KeyQ":"q","KeyW":"w","Backquote":{"deadKey":"grave"}}},
  {"modifiers":[["shift"]],"bindings":{"KeyQ":"Q","KeyW":"W","Backquote":"~"}},
  {"modifiers":[["caps"]],"bindings":{"KeyQ":"Q","KeyW":"x"}},
  {"modifiers":[["caps","shift"]],"bindings":{"KeyQ":"q","KeyW":"X"}},
  {"modifiers":[["altR"],["ctrl","alt"]],"bindings":{"KeyQ":"@","KeyW":"\ud83d\ude00"}}]},
 "deadKeys":{"grave":{"spacing":"` + "`" + `","entries":[{"base":"a","result":"à"},{"base":{"deadKey":"grave"},"result":"` + "`" + `"}]}},
 "targets":{"windows":{"registration":{"langId":"0409"}}}}`

func TestRules(t *testing.T) {
	l, r := kbljson.Read([]byte(tiny), "tiny")
	if l == nil {
		t.Fatal(r.Summary())
	}
	res, r := Lower(l, Options{})
	if res == nil {
		t.Fatal(r.Summary())
	}
	tb := res.DLL.Tables
	if !reflect.DeepEqual(tb.Modifiers.ModNumber, []byte{0, 1, 15, 15, 15, 15, 2}) || tb.LocaleFlags != windows.KLLF_ALTGR {
		t.Errorf("columns %v flags %#x", tb.Modifiers.ModNumber, tb.LocaleFlags)
	}
	rows := map[byte][]windows.CharRow{}
	for _, ct := range tb.CharTables {
		for i, row := range ct.Rows {
			rows[row.VK] = append(rows[row.VK], row)
			if row.VK == 0xC0 && ct.Rows[i+1].VK != 0xFF {
				t.Error("dead row missing")
			}
		}
	}
	if q := rows['Q'][0]; q.Attributes != windows.CAPLOK || q.Chars[2] != '@' {
		t.Errorf("Q %+v", q)
	}
	if w := rows['W']; w[0].Attributes != windows.SGCAPS || len(w) != 2 || w[1].Chars[0] != 'x' || w[1].Chars[1] != 'X' || w[0].Chars[2] != windows.WCH_LGTR {
		t.Errorf("W %+v", w)
	}
	if len(tb.Ligatures) != 1 || tb.Ligatures[0].Column != 2 {
		t.Errorf("ligatures %+v", tb.Ligatures)
	}
	if len(tb.DeadKeys) != 2 || tb.DeadKeys[1] != (windows.DeadKey{Dead: '`', Base: '`', Composed: '`'}) {
		t.Errorf("dead keys %+v", tb.DeadKeys)
	}
	if res.DLL.FileName != "en.dll" || res.Pins.Strings.LayoutName["0409"] != "T" {
		t.Errorf("name %s strings %+v", res.DLL.FileName, res.Pins.Strings)
	}
	// Table A rows for Escape and the arrows are added; KeyQ's default is not duplicated.
	if tb.ScanToVK[0x01] != 0x1B || res.AddedPositions["KeyQ"] != nil || res.AddedPositions["Escape"] == nil {
		t.Errorf("Table A additions: %#x %v", tb.ScanToVK[1], res.AddedPositions["Escape"])
	}
}

func TestPolicyFromFile(t *testing.T) {
	src := strings.Replace(tiny, `"targets":{"windows":{`, `"onLoss":{"kinds":{"windowsDefault.*":"error"}},"targets":{"windows":{`, 1)
	l, _ := kbljson.Read([]byte(src), "t")
	if res, r := Lower(l, Options{}); res != nil || !r.HasErrors() {
		t.Error("onLoss windowsDefault.* error did not fail the build")
	}
}

func TestLossCases(t *testing.T) {
	tests := []struct {
		name, edit, kind string
	}{
		{"alt only", `{"modifiers":[["shift","alt"]],"bindings":{"KeyQ":"z"}},`, "windows.altOnlyLayer"},
		{"side widened", `{"modifiers":[["ctrlR"]],"bindings":{"KeyQ":"z"}},`, "windows.sideModifierWidened"},
		{"unencodable", `{"modifiers":[["ctrl"]],"bindings":{"KeyQ":{"utf16":["F000"]}}},`, "windows.outputUnencodable"},
		{"missing dead key", `{"modifiers":[["ctrl"]],"bindings":{"KeyQ":{"deadKey":"nope"}}},`, "general.unresolvedReference"},
	}
	for _, tt := range tests {
		src := strings.Replace(tiny, `"layers":[`, `"layers":[`+tt.edit, 1)
		l, r := kbljson.Read([]byte(src), tt.name)
		if l != nil {
			_, r = Lower(l, Options{}) // otherwise the reader already refused the file (SPEC §19)
		}
		if !strings.Contains(kinds(r), tt.kind) {
			t.Errorf("%s: want %s in %s", tt.name, tt.kind, kinds(r))
		}
	}
}

// TestCLDRToDLL builds every CLDR 3.0 fixture once the two pins the format requires are added (a LANGID and the
// VKs of positions Table A has no row for).
func TestCLDRToDLL(t *testing.T) {
	files, _ := filepath.Glob("../../../testdata/third_party/cldr-release-49/keyboards/3.0/*.xml")
	if len(files) == 0 {
		t.Fatal("no CLDR fixtures")
	}
	for _, p := range files {
		t.Run(filepath.Base(p), func(t *testing.T) {
			b, _ := os.ReadFile(p)
			l, r := cldr.Read(b, filepath.Base(p))
			if l == nil {
				t.Fatal(r.Summary())
			}
			if l.Targets == nil {
				l.Targets = &model.Targets{}
			}
			if l.Targets.Windows == nil {
				l.Targets.Windows = &model.WindowsTarget{}
			}
			w := l.Targets.Windows
			w.Registration = &model.Registration{LangID: "0416"}
			w.Keys = map[string]*model.WindowsKey{"IntlRo": {VK: "ABNT_C1"}}
			res, br := Lower(l, Options{Policy: &model.OnLoss{Kinds: map[string]string{"windows.*": "warning"}}})
			if res == nil {
				t.Fatal(br.Summary())
			}
			set := windows.CompileAll(res.DLL, res.Targets)
			if set.Report.HasErrors() || len(set.Builds) != 4 {
				t.Fatalf("%s", set.Report.Summary())
			}
			if l.Hardware != nil && len(res.DLL.Tables.Expectations()) == 0 {
				t.Error("no expectations")
			}
		})
	}
}

// TestDecisions37to41 covers D37 to D41 of the kbl specification.
func TestDecisions37to41(t *testing.T) {
	lower := func(src string) *loss.Report {
		l, r := kbljson.Read([]byte(src), "t")
		if l == nil {
			return r
		}
		_, br := Lower(l, Options{})
		return br
	}
	edit := func(old, new string) string { return strings.Replace(tiny, old, new, 1) }
	// D38: Numpad7 and Home both keep their Table A VK HOME: no vkDuplicate. Pinning Home to another position's
	// VK is a duplicate.
	ok := edit(`"Backquote":{"scanCode":"29"}`, `"Backquote":{"scanCode":"29"},"Numpad7":{"scanCode":"47"},"Home":{"scanCode":"E047"}`)
	if k := kinds(lower(ok)); strings.Contains(k, "windows.vkDuplicate") {
		t.Errorf("D38: %s", k)
	}
	dup := edit(`"registration":{"langId":"0409"}`, `"registration":{"langId":"0409"},"keys":{"KeyW":{"vk":"Q"}}`)
	if k := kinds(lower(dup)); !strings.Contains(k, "windows.vkDuplicate") {
		t.Errorf("explicit duplicate not reported: %s", k)
	}
	// D39, D66: arm64x is a build the tool can make: listed, it is built; the default builds include it.
	arm := edit(`"registration":{"langId":"0409"}`, `"registration":{"langId":"0409"},"builds":["x64","wow64","arm64x"]`)
	if r := lower(arm); r.HasErrors() || strings.Contains(kinds(r), "windows.architectureUnsupported") {
		t.Errorf("D39 explicit arm64x: %s", r.Summary())
	}
	if r := lower(tiny); r.HasErrors() || strings.Contains(kinds(r), "windows.architectureUnsupported") {
		t.Errorf("D39 default builds: %s", r.Summary())
	}
	// D40: Caps Lock layers that stop at Shift give capsUnmatched for the AltGr column, not capsNotExpressible.
	noFallback := edit(`"kbl":"0.1",`, `"kbl":"0.1","settings":{"capsLockFallback":false},`)
	r := lower(noFallback)
	if k := kinds(r); !strings.Contains(k, "windows.capsUnmatched") || strings.Contains(k, "windows.capsNotExpressible") || r.HasErrors() {
		t.Errorf("D40: %s", r.Summary())
	}
	// D37: a file position that keeps its Table A VK does not take the row of a Table A twin away.
	l, _ := kbljson.Read([]byte(ok), "t")
	res, _ := Lower(l, Options{})
	found := false
	for _, e := range res.DLL.Tables.E0 {
		found = found || e.Scan == 0x47
	}
	if !found {
		t.Error("D37: Home (E047) missing")
	}
}

// TestD86: rule vk adds every Table A row whose scan code no position of the file uses, whatever VKs the file's
// positions have (D86 replaces the explicit-remap exception of D35/D37): a file that puts W on scan code 10 and does
// not name 11 gets W on both, the added row is listed under windowsDefault.vk, no windows.vkDuplicate is reported
// between the file's position and the added row, and the file's position keeps the character row.
func TestD86(t *testing.T) {
	src := `{"kbl":"0.1","metadata":{"id":"en","name":"T"},"positions":{"KeyQ":{"scanCode":"10"}},
	 "hardware":{"layers":[{"modifiers":[[]],"bindings":{"KeyQ":"q"}},{"modifiers":[["shift"]],"bindings":{"KeyQ":"Q"}}]},
	 "targets":{"windows":{"registration":{"langId":"0409"},"keys":{"KeyQ":{"vk":"W"}}}}}`
	l, r := kbljson.Read([]byte(src), "t")
	if l == nil {
		t.Fatalf("%s\n%s", r.Summary(), src)
	}
	res, br := Lower(l, Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
	if k := kinds(br); strings.Contains(k, "windows.vkDuplicate") || strings.Contains(k, "windows.sharedVkRowConflict") {
		t.Errorf("D86: %s", br.Summary())
	}
	tb := res.DLL.Tables
	if tb.ScanToVK[0x10]&0xFF != 'W' || tb.ScanToVK[0x11]&0xFF != 'W' {
		t.Errorf("scan codes 10, 11: %04x %04x, want W on both", tb.ScanToVK[0x10], tb.ScanToVK[0x11])
	}
	listed := false
	for _, e := range br.Entries {
		if e.Code == "windowsDefault.vk" {
			if d, ok := e.Detail["value"].(map[string]any); ok {
				ids, _ := d["addedFromTableA"].([]string)
				for _, id := range ids {
					listed = listed || id == "KeyW"
				}
			}
		}
	}
	if !listed {
		t.Errorf("KeyW not listed under windowsDefault.vk: %s", br.Summary())
	}
	for _, ct := range tb.CharTables {
		for _, row := range ct.Rows {
			if row.VK == 'W' && row.Chars[0] != 'q' {
				t.Errorf("W row %v, want the file's KeyQ cells", row.Chars)
			}
		}
	}
}
