package cldr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

const fixtures = "../../testdata/third_party/cldr-release-49/keyboards"

// readFixture reads a CLDR keyboard and its test file, if it has one.
func readFixture(t *testing.T, path, name string) *model.Layout {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l, r := Read(b, name)
	if l == nil {
		t.Fatalf("%s: %s", path, r.Summary())
	}
	base := strings.TrimSuffix(filepath.Base(path), ".xml")
	if tb, err := os.ReadFile(filepath.Join(fixtures, "test", base+"-test.xml")); err == nil {
		tests, tr := ReadTests(tb, base+"-test.xml")
		if tests == nil {
			t.Fatalf("%s tests: %s", path, tr.Summary())
		}
		l.Tests = tests
	}
	return l
}

func kbl(t *testing.T, l *model.Layout) []byte {
	t.Helper()
	b, r := kbljson.Write(l)
	if b == nil {
		t.Fatalf("not schema-valid: %s", r.Summary())
	}
	return b
}

// throughCLDR writes l as CLDR (and its tests as CLDR test files) and reads it back.
func throughCLDR(t *testing.T, l *model.Layout) (*model.Layout, *loss.Report) {
	t.Helper()
	x, wr := Write(l)
	back, r := Read(x, l.Metadata.Provenance.Source)
	if back == nil {
		t.Fatalf("written CLDR does not read back: %s\n%s", r.Summary(), x)
	}
	for _, tf := range l.Tests {
		tb, _ := WriteTests(tf)
		tests, tr := ReadTests(tb, "t")
		if tests == nil {
			t.Fatalf("tests: %s", tr.Summary())
		}
		back.Tests = append(back.Tests, tests...)
	}
	return back, wr
}

func TestFixturesRoundTrip(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(fixtures, "3.0", "*.xml"))
	if len(files) != 13 {
		t.Fatalf("fixtures: %v", files)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			l := readFixture(t, f, filepath.Base(f))
			first := kbl(t, l)
			back, wr := throughCLDR(t, l)
			if wr.HasErrors() {
				t.Errorf("writing reports errors: %s", wr.Summary())
			}
			second := kbl(t, back)
			if d := kbljson.DiffJSON(first, second); d != "" {
				t.Errorf("CLDR → kbl → CLDR → kbl differs at %s", d)
			}
		})
	}
}

// TestPortugueseExample compares the reader's result with the spec's hand-made example (c). The example adds, by
// hand, metadata.license, metadata.copyright and metadata.description, which the CLDR file does not have; they are
// copied over before the comparison. Everything else must agree.
func TestPortugueseExample(t *testing.T) {
	src := "keyboards/3.0/pt-t-k0-abnt2.xml (CLDR release-49)"
	l := readFixture(t, filepath.Join(fixtures, "3.0", "pt-t-k0-abnt2.xml"), src)
	eb, err := os.ReadFile("../../testdata/kbl-spec/examples/cldr-pt-abnt2.kbl.json")
	if err != nil {
		t.Fatal(err)
	}
	ex, r := kbljson.Read(eb, "example")
	if ex == nil {
		t.Fatal(r.Summary())
	}
	l.Schema = ex.Schema
	l.Metadata.License = ex.Metadata.License
	l.Metadata.Copyright = ex.Metadata.Copyright
	l.Metadata.Description = ex.Metadata.Description
	if d := kbljson.DiffJSON(eb, kbl(t, l)); d != "" {
		t.Errorf("differs from the spec example at %s", d)
	}
}

// TestSpecExamplesThroughCLDR writes each spec example as CLDR and reads it back: with the kbl <special> the file
// keeps its meaning (tests that press positions are the one part CLDR test files cannot carry).
func TestSpecExamplesThroughCLDR(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/kbl-spec/examples/*.kbl.json")
	if len(files) != 7 {
		t.Fatalf("examples: %v", files)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, _ := os.ReadFile(f)
			l, r := kbljson.Read(b, f)
			if l == nil {
				t.Fatal(r.Summary())
			}
			if l.Metadata.Provenance == nil {
				l.Metadata.Provenance = &model.Provenance{}
			}
			x, wr := Write(l)
			back, rr := Read(x, "")
			if back == nil {
				t.Fatalf("%s\n%s", rr.Summary(), x)
			}
			back.Tests = l.Tests
			orig, _ := kbljson.Write(l)
			got := kbl(t, back)
			if d := kbljson.DiffJSON(orig, got); d != "" {
				t.Errorf("meaning changed at %s", d)
			}
			t.Logf("reported: %s", strings.Join(wr.Codes(), " "))
			// Without the special, what CLDR elements hold still reads back schema-valid.
			off := false
			l.Targets = withCarry(l.Targets, &off)
			x2, _ := Write(l)
			plain, pr := Read(x2, "")
			if plain == nil {
				t.Fatalf("without special: %s", pr.Summary())
			}
			kbl(t, plain)
		})
	}
}

func withCarry(t *model.Targets, v *bool) *model.Targets {
	if t == nil {
		t = &model.Targets{}
	}
	if t.CLDR == nil {
		t.CLDR = &model.CLDRTarget{}
	}
	t.CLDR.CarryInSpecial = v
	return t
}

func TestParseModifiers(t *testing.T) {
	tests := []struct {
		in   string
		want []model.ModifierSet
	}{
		{"none", []model.ModifierSet{{}}},
		{"", []model.ModifierSet{{}}},
		{"shift", []model.ModifierSet{{"shift"}}},
		{"shift caps, caps", []model.ModifierSet{{"shift", "caps"}, {"caps"}}},
		{"other", []model.ModifierSet{{"other"}}},
		{"none, altR", []model.ModifierSet{{}, {"altR"}}},
	}
	for _, tt := range tests {
		if got := parseModifiers(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q: %v, want %v", tt.in, got, tt.want)
		}
		if tt.in != "" && modifierString(tt.want) != strings.ReplaceAll(tt.in, ",  ", ", ") {
			if !reflect.DeepEqual(parseModifiers(modifierString(tt.want)), tt.want) {
				t.Errorf("%q does not round-trip", tt.in)
			}
		}
	}
}

func TestImpliedKeys(t *testing.T) {
	tests := []struct {
		id, out string
		ok      bool
	}{{"a", "a", true}, {"Z", "Z", true}, {"7", "7", true}, {"space", " ", true}, {"gap", "", false}, {"ab", "", false}, {"é", "", false}}
	for _, tt := range tests {
		out, ok := impliedKey(tt.id)
		if out != tt.out || ok != tt.ok {
			t.Errorf("%q: %q %v", tt.id, out, ok)
		}
	}
	for _, s := range []string{"a", " ", "0"} {
		if id, ok := impliedID(s); !ok {
			t.Errorf("%q has no implied id", s)
		} else if out, _ := impliedKey(id); out != s {
			t.Errorf("%q → %q → %q", s, id, out)
		}
	}
}

func TestMarkedText(t *testing.T) {
	tests := []struct {
		in    string
		items []model.MarkedItem
		out   string // canonical form written back
	}{
		{`a`, []model.MarkedItem{{Text: "a"}}, `a`},
		{`\u{0300}`, []model.MarkedItem{{Text: "̀"}}, "̀"},
		{`\u{61 62}c`, []model.MarkedItem{{Text: "abc"}}, `abc`},
		{`\m{acute}`, []model.MarkedItem{{Marker: "acute"}}, `\m{acute}`},
		{`x\m{a}${v}y`, []model.MarkedItem{{Text: "x"}, {Marker: "a"}, {Variable: "v"}, {Text: "y"}}, `x\m{a}${v}y`},
		{`\u{5C}`, []model.MarkedItem{{Text: `\`}}, `\u{5C}`},
		{`$`, []model.MarkedItem{{Text: `$`}}, `$`},
		{`\u{24}{`, []model.MarkedItem{{Text: `${`}}, `\u{24}{`},
	}
	for _, tt := range tests {
		m, err := parseMarked(tt.in)
		if err != nil {
			t.Fatalf("%q: %v", tt.in, err)
		}
		if !reflect.DeepEqual(m.Items, tt.items) {
			t.Errorf("%q: %+v", tt.in, m.Items)
		}
		if got := encodeMarked(m); got != tt.out {
			t.Errorf("%q written as %q, want %q", tt.in, got, tt.out)
		}
	}
	for _, bad := range []string{`\u{D800}`, `\u{110000}`, `\u{`, `\m{x`, `\u{}`} {
		if _, err := parseMarked(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const mini = `<?xml version="1.0" encoding="UTF-8"?>
<keyboard3 xmlns="https://schemas.unicode.org/cldr/45/keyboard3" locale="und-t-k0-test" conformsTo="45">
	<info name="Test" />
	<displays>
		<display output="\m{acute}" display="´" />
		<display keyId="q" display="Q!" />
	</displays>
	<keys>
		<key id="d-acute" output="\m{acute}" />
		<key id="q" output="q" flickId="f" longPressKeyIds="a e" longPressDefaultKeyId="e" multiTapKeyIds="a" />
		<special><x:note xmlns:x="urn:x">keys</x:note></special>
	</keys>
	<flicks>
		<flick id="f"><flickSegment directions="n se" keyId="e" /></flick>
	</flicks>
	<layers formId="us">
		<layer modifiers="none">
			<row keys="gap 1 2" />
			<row keys="q d-acute" />
			<special><x:note xmlns:x="urn:x">layer</x:note></special>
		</layer>
	</layers>
	<variables>
		<string id="v" value="x" />
		<uset id="u" value="[a-z]" />
	</variables>
	<transforms type="simple">
		<transformGroup>
			<transform from="\m{acute}a" to="á" />
			<transform from="\m{acute}e" to="\u{E9}" />
		</transformGroup>
		<transformGroup>
			<reorder from="\u{1A60}" order="127 3" tertiaryBase="true" />
		</transformGroup>
	</transforms>
	<special><x:note xmlns:x="urn:x">root</x:note></special>
</keyboard3>
`

func TestMiniKeyboard(t *testing.T) {
	l, r := Read([]byte(mini), "mini.xml")
	if l == nil {
		t.Fatal(r.Summary())
	}
	// §3.6: the marker became a dead key with two entries and the display as spacing.
	dk := l.DeadKeys["acute"]
	if dk == nil || dk.Spacing.Text != "´" || len(dk.Entries) != 2 || dk.Entries[1].Result.Text != "é" || dk.NoMatch != "emitText" {
		t.Fatalf("dead key %+v", dk)
	}
	if l.Keys["d-acute"].DeadKey != "acute" || len(l.Displays.Entries) != 1 {
		t.Errorf("keys %+v displays %+v", l.Keys["d-acute"], l.Displays)
	}
	if len(l.Transforms.Simple) != 1 || len(l.Transforms.Simple[0].Reorders) != 1 || string(l.Transforms.Simple[0].Reorders[0].Order) != "[127,3]" {
		t.Errorf("transforms %+v", l.Transforms)
	}
	b := l.Hardware.Layers[0].Bindings
	if _, ok := b["Backquote"]; ok || b["Digit1"].Text != "1" || b["KeyQ"].Key != "q" || b["KeyW"].Key != "d-acute" {
		t.Errorf("bindings %+v", b)
	}
	if l.Flicks["f"].Segments[0].Directions[1] != "se" || l.Keys["q"].LongPressDefaultKeyID != "e" {
		t.Error("flicks or gestures")
	}
	if len(l.CLDRSpecial) != 2 || !strings.HasPrefix(l.CLDRSpecial[0], "<!--kbl:parent=keys-->") || len(l.Hardware.Layers[0].CLDRSpecial) != 1 {
		t.Errorf("specials %q %q", l.CLDRSpecial, l.Hardware.Layers[0].CLDRSpecial)
	}
	if *l.Settings.CapsLockFallback {
		t.Error("CLDR imports have no Caps Lock fallback")
	}
	if l.Forms["us"] == nil || l.Positions["KeyQ"].ScanCode != "10" {
		t.Error("implied form not inlined")
	}
	codes := strings.Join(r.Codes(), " ")
	if !strings.Contains(codes, "general.importInlined") {
		t.Errorf("codes %s", codes)
	}
	first := kbl(t, l)
	x, _ := Write(l)
	for _, want := range []string{`<key id="d-acute" output="\m{acute}" />`, `<special><x:note xmlns:x="urn:x">keys</x:note></special>`,
		`order="127 3"`, `tertiaryBase="true"`, `<row keys="gap 1 2" />`} {
		if !strings.Contains(string(x), want) {
			t.Errorf("missing %s in\n%s", want, x)
		}
	}
	back, br := Read(x, "mini.xml")
	if back == nil {
		t.Fatal(br.Summary())
	}
	if d := kbljson.DiffJSON(first, kbl(t, back)); d != "" {
		t.Errorf("round trip differs at %s\n%s", d, x)
	}
}

func TestMarkerNotConverted(t *testing.T) {
	tests := []struct{ name, transforms string }{
		{"no transforms", ""},
		{"pattern", `<transforms type="simple"><transformGroup><transform from="\m{acute}[ae]" to="x" /></transformGroup></transforms>`},
		{"two groups", `<transforms type="simple"><transformGroup><transform from="\m{acute}a" to="á" /></transformGroup><transformGroup><transform from="\m{acute}e" to="é" /></transformGroup></transforms>`},
		{"any marker", `<transforms type="simple"><transformGroup><transform from="\m{acute}a" to="á" /><transform from="\m{.}" to="" /></transformGroup></transforms>`},
		{"backspace", `<transforms type="simple"><transformGroup><transform from="\m{acute}a" to="á" /></transformGroup></transforms><transforms type="backspace"><transformGroup><transform from="\m{acute}" /></transformGroup></transforms>`},
	}
	for _, tt := range tests {
		src := strings.Replace(mini, `<transforms type="simple">`+mini[strings.Index(mini, `<transforms type="simple">`)+len(`<transforms type="simple">`):strings.Index(mini, "</transforms>")]+"</transforms>", tt.transforms, 1)
		l, r := Read([]byte(src), "t")
		if l == nil {
			t.Fatalf("%s: %s", tt.name, r.Summary())
		}
		if len(l.DeadKeys) != 0 || l.Keys["d-acute"].Output == nil {
			t.Errorf("%s: converted", tt.name)
		}
	}
}

func TestWriteKinds(t *testing.T) {
	tr := true
	l := &model.Layout{Kbl: "0.1", Metadata: model.Metadata{ID: "en", Name: "x", License: "MIT-0"},
		Settings:        &model.Settings{CapsLockOffBy: "shift"},
		Positions:       map[string]*model.Position{"KeyQ": {ScanCode: "10", Name: "Q"}, "Escape": {ScanCode: "01"}},
		CustomModifiers: map[string]*model.CustomModifier{"kana": {Kind: "held", Positions: []string{"Escape"}}},
		Hardware: &model.Hardware{FormID: "us", Layers: []*model.HardwareLayer{
			{Modifiers: []model.ModifierSet{{}}, Bindings: map[string]*model.Binding{
				"KeyQ": model.TextBinding("é"), "Escape": model.TextBinding("\x1b"), "KeyW": {Kind: model.BindUnits, Units: model.Units{0xD800}}}},
			{Modifiers: []model.ModifierSet{{"kana"}}, Bindings: map[string]*model.Binding{"KeyQ": model.TextBinding("x")}},
		}},
		Targets: &model.Targets{Windows: &model.WindowsTarget{DLLName: "kbdx"}, CLDR: &model.CLDRTarget{CarryInSpecial: &tr}},
	}
	x, r := Write(l)
	codes := strings.Join(r.Codes(), " ")
	for _, k := range []string{"general.metadataDropped", "cldr.windowsOnlyDropped", "cldr.customModifierDropped",
		"cldr.positionNotInForm", "cldr.keyIdGenerated", "cldr.capsFallbackExpanded"} {
		if !strings.Contains(codes, k) {
			t.Errorf("missing %s in %s", k, codes)
		}
	}
	for _, e := range r.Entries {
		if e.Class != loss.Loss {
			t.Errorf("%s is not a loss entry", e.Code)
		}
	}
	if !strings.Contains(string(x), `<key id="kbl-00e9" output="é" />`) || !strings.Contains(string(x), `modifiers="caps"`) {
		t.Errorf("generated key or caps layer missing:\n%s", x)
	}
	back, br := Read(x, "")
	if back == nil {
		t.Fatal(br.Summary())
	}
	a, _ := json.Marshal(l)
	b, _ := json.Marshal(back)
	if d := kbljson.DiffJSON(a, b); d != "" {
		t.Errorf("special did not restore the file: %s", d)
	}
}

func TestTestsRoundTrip(t *testing.T) {
	for _, f := range []string{"fr-t-k0-test-test.xml", "pt-t-k0-abnt2-test.xml"} {
		b, _ := os.ReadFile(filepath.Join(fixtures, "test", f))
		tf, r := ReadTests(b, f)
		if tf == nil {
			t.Fatal(r.Summary())
		}
		x, _ := WriteTests(tf[0])
		tf2, r2 := ReadTests(x, f)
		if tf2 == nil || !reflect.DeepEqual(tf, tf2) {
			t.Errorf("%s: %s\n%s", f, r2.Summary(), x)
		}
	}
	p := 0
	tf := &model.TestFile{Info: model.TestInfo{Name: "x"}, Suites: []*model.Suite{{Name: "s", Tests: []*model.Test{
		{Name: "hw", Steps: []model.Step{{Keystroke: &model.Keystroke{Position: "KeyQ"}}}},
		{Name: "key", Steps: []model.Step{{Keystroke: &model.Keystroke{Key: "q", LongPress: &p}}}},
	}}}}
	x, r := WriteTests(tf)
	if !strings.Contains(strings.Join(r.Codes(), " "), "general.testsDropped") || !strings.Contains(string(x), `longPress="0"`) {
		t.Errorf("%s\n%s", r.Summary(), x)
	}
}

func TestReadErrors(t *testing.T) {
	tests := []struct{ name, src, code string }{
		{"not xml", "<keyboard3", "cldr.xml.syntax"},
		{"wrong root", "<keys/>", "cldr.xml.root"},
		{"no info", `<keyboard3 locale="en" conformsTo="45"></keyboard3>`, "cldr.xml.info"},
		{"unknown import", `<keyboard3 locale="en" conformsTo="45"><info name="x"/><keys><import base="cldr" path="45/nope.xml"/></keys></keyboard3>`, "cldr.xml.import"},
		{"bad form", `<keyboard3 locale="en" conformsTo="45"><info name="x"/><layers formId="zz"><layer modifiers="none"/></layers></keyboard3>`, "cldr.xml.formId"},
		{"bad conformsTo", `<keyboard3 locale="en" conformsTo="40"><info name="x"/></keyboard3>`, "cldr.xml.conformsTo"},
	}
	for _, tt := range tests {
		l, r := Read([]byte(tt.src), "t")
		if l != nil || !strings.Contains(strings.Join(r.Codes(), " "), tt.code) {
			t.Errorf("%s: %s", tt.name, r.Summary())
		}
	}
}

// TestMarkerSetDeadKeys: coverage §3.6 rule 2 set forms (D88). \m{m}($[v]) → $1 + literal and → $[1:w] expand to
// one entry per member in set order (nested sets in place, a later duplicate base dropped), and the file
// round-trips through the literal transforms the writer gives back.
func TestMarkerSetDeadKeys(t *testing.T) {
	vars := `<variables>
		<string id="v" value="x" />
		<uset id="u" value="[a-z]" />
		<set id="up" value="A E" />
		<set id="all" value="$[up] a e" />
		<set id="to" value="Ȧ Ė ȧ ė" />
	</variables>`
	cases := []struct{ name, group, base, result string }{
		{"capture", `<transform from="\m{acute}a" to="á" /><transform from="\m{acute}($[all])" to="$1\u{301}" />`, "a A E e", "á Á É é"},
		{"mapped", `<transform from="\m{acute}($[all])" to="$[1:to]" />`, "A E a e", "Ȧ Ė ȧ ė"},
	}
	for _, c := range cases {
		src := strings.Replace(mini, mini[strings.Index(mini, "<variables>"):strings.Index(mini, "</variables>")+len("</variables>")], vars, 1)
		i := strings.Index(src, `<transform from="\m{acute}a"`)
		j := strings.Index(src[i:], "</transformGroup>")
		src = src[:i] + c.group + src[i+j:]
		l, r := Read([]byte(src), "t")
		if l == nil {
			t.Fatalf("%s: %s", c.name, r.Summary())
		}
		dk := l.DeadKeys["acute"]
		if dk == nil {
			t.Fatalf("%s: not converted", c.name)
		}
		var bases, results []string
		for _, e := range dk.Entries {
			bases = append(bases, e.Base.Text)
			results = append(results, e.Result.Text)
		}
		if strings.Join(bases, " ") != c.base || strings.Join(results, " ") != c.result {
			t.Errorf("%s: entries %q → %q", c.name, bases, results)
		}
		first := kbl(t, l)
		x, _ := Write(l)
		back, br := Read(x, "t")
		if back == nil {
			t.Fatal(br.Summary())
		}
		if d := kbljson.DiffJSON(first, kbl(t, back)); d != "" {
			t.Errorf("%s: round trip differs at %s\n%s", c.name, d, x)
		}
	}
	// Not a set form: a UnicodeSet variable, a capture with more than one set, a $1 with pattern syntax after it.
	for _, g := range []string{`<transform from="\m{acute}($[u])" to="$1\u{301}" />`,
		`<transform from="\m{acute}($[up]$[up])" to="$1" />`, `<transform from="\m{acute}($[up])" to="$1." />`,
		`<transform from="\m{acute}($[up])" to="$[1:all]" />`} {
		src := strings.Replace(mini, mini[strings.Index(mini, "<variables>"):strings.Index(mini, "</variables>")+len("</variables>")], vars, 1)
		i := strings.Index(src, `<transform from="\m{acute}a"`)
		j := strings.Index(src[i:], "</transformGroup>")
		src = src[:i] + g + src[i+j:]
		if l, r := Read([]byte(src), "t"); l == nil {
			t.Fatal(r.Summary())
		} else if len(l.DeadKeys) != 0 {
			t.Errorf("%s: converted", g)
		}
	}
}

// TestMarkerCatchAll: a later-group \m{.}(.) → $1 or \m{.} → nothing does not block the conversion (D95) and stays in
// the file; in the dead key's own group, or with another result, it does.
func TestMarkerCatchAll(t *testing.T) {
	for _, c := range []struct {
		group     string
		later, ok bool
	}{
		{`<transform from="\m{.}(.)" to="$1" />`, true, true},
		{`<transform from="\m{.}" />`, true, true},
		{`<transform from="\m{.}(.)" to="$1$1" />`, true, false},
		{`<transform from="\m{.}(.)" to="$1" />`, false, false},
	} {
		var src string
		if c.later {
			src = strings.Replace(mini, "</transforms>", "<transformGroup>"+c.group+"</transformGroup></transforms>", 1)
		} else {
			src = strings.Replace(mini, `<transform from="\m{acute}e"`, c.group+`<transform from="\m{acute}e"`, 1)
		}
		l, r := Read([]byte(src), "t")
		if l == nil {
			t.Fatal(r.Summary())
		}
		if got := l.DeadKeys["acute"] != nil; got != c.ok {
			t.Errorf("%s (later group %v): converted %v", c.group, c.later, got)
		}
		if c.ok && !strings.Contains(string(kbl(t, l)), `\\m{.}`) {
			t.Errorf("%s: the catch-all left the file", c.group)
		}
	}
}
