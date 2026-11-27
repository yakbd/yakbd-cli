package klc

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"
)

var update = flag.Bool("update", false, "rewrite the golden diagnostics")

const header = "KBD\tkbdt\t\"Test\"\r\nSHIFTSTATE\r\n0\r\n1\r\n2\r\n6\r\nLAYOUT\r\n"

func parseString(t *testing.T, s string) (*File, []string) {
	t.Helper()
	f, r := Parse([]byte(s), "t.klc")
	var codes []string
	for _, e := range r.Entries {
		codes = append(codes, fmt.Sprintf("%s@%d", e.Code, e.Where.Line))
	}
	return f, codes
}

func TestRows(t *testing.T) {
	tests := []struct {
		name  string
		rows  string
		codes string // expected diagnostics, sorted, joined by space
	}{
		{"plain", "10\tQ\t1\tq\tQ\t-1\t0040\r\n", ""},
		{"lower vk, 0x sc", "0x10\tq\t1\tq\tQ\r\n", ""},
		{"vk name with prefix", "27\tVK_OEM_1\t0\t003b\t003a\r\n", ""},
		{"remap only", "10\tW\r\n", ""},
		{"unknown vk", "10\tNOPE\t0\tq\r\n", "klc.vk.unknown@8"},
		{"bad sc prefix", "e210\tQ\t0\tq\r\n", "klc.sc.invalid@8"},
		{"duplicate sc", "10\tQ\t0\tq\r\n10\tW\t0\tw\r\n", "klc.sc.duplicate@9"},
		{"duplicate vk", "10\tQ\t0\tq\r\n11\tQ\t0\tw\r\n", "klc.vk.duplicate@9"},
		{"enter pair", "1c\tRETURN\t0\t000d\r\ne01c\tRETURN\t0\t000d\r\n", ""},
		{"cap 3", "10\tQ\t3\tq\r\n", "klc.cap.invalid@8"},
		{"cap 6", "10\tQ\t6\tq\r\n", "klc.cap.invalid@8"},
		{"too many cells", "10\tQ\t0\tq\tQ\t-1\t-1\tx\r\n", "klc.cells.count@8"},
		{"two hex digits", "10\tQ\t0\t41\r\n", "klc.cell.invalid@8"},
		{"three hex digits", "10\tQ\t0\t041\r\n", "klc.cell.invalid@8"},
		{"two letters", "10\tQ\t0\tqq\r\n", "klc.cell.invalid@8"},
		{"marker f001", "10\tQ\t0\tf001\r\n", "klc.cell.invalid@8"},
		{"surrogate", "10\tQ\t0\td83d\r\n", "klc.cell.invalid@8"},
		{"fffe", "10\tQ\t0\tfffe\r\n", "klc.cell.invalid@8"},
		{"supplementary 5", "10\tQ\t0\t1f600\r\n", ""},
		{"supplementary 6", "10\tQ\t0\t10fffd\r\n", ""},
		{"supplementary leading zero", "10\tQ\t0\t01f60\r\n", "klc.cell.invalid@8"},
		{"supplementary out of range", "10\tQ\t0\t110000\r\n", "klc.cell.invalid@8"},
		{"dead 1fbf@ is a dead key", "10\tQ\t0\t1fbf@\r\nDEADKEY\t1fbf\r\n0061\t00e0\r\n", ""},
		{"dead without table", "10\tQ\t0\t0060@\r\n", "klc.deadkey.table.missing@8"},
		{"literal at", "10\tQ\t0\t@\r\n", "klc.cell.invalid@8"},
		{"literal dead", "10\tQ\t0\t`@\r\nDEADKEY\t0060\r\n0061\t00e0\r\n", ""},
		{"ligature ok", "10\tQ\t0\t%%\r\nLIGATURE\r\nQ\t0\t0061\t0301\r\n", ""},
		{"ligature missing", "10\tQ\t0\t%%\r\n", "klc.ligature.missing@8"},
		{"ligature column out of range", "10\tQ\t0\tq\r\nLIGATURE\r\nQ\t4\t0061\t0301\r\n", "klc.ligature.column@10 klc.ligature.unused@10"},
		{"ligature six", "10\tQ\t0\t%%\r\nLIGATURE\r\nQ\t0\t1\t2\t3\t4\t5\t6\r\n", "klc.ligature.length@10 klc.ligature.missing@8"},
		{"ligature surrogates", "10\tQ\t0\t%%\r\nLIGATURE\r\nQ\t0\td83d\tde00\r\n", ""},
		{"ligature duplicate", "10\tQ\t0\t%%\r\nLIGATURE\r\nQ\t0\t1\t2\r\nQ\t0\t1\t2\r\n", "klc.ligature.duplicate@11"},
		{"sgcap", "10\tQ\tSGCap\tq\tQ\r\n-1\t-1\t0\tx\tX\r\n", ""},
		{"sgcap missing", "10\tQ\tSGCap\tq\tQ\r\n11\tW\t0\tw\r\n", "klc.sgcap.missing@8"},
		{"sgcap unexpected", "10\tQ\t0\tq\tQ\r\n-1\t-1\t0\tx\tX\r\n", "klc.sgcap.unexpected@9"},
		{"deadkey dup", "10\tQ\t0\t0060@\r\nDEADKEY\t0060\r\n0061\t00e0\r\n0061\t00e1\r\n", "klc.deadkey.duplicate@11"},
		{"deadkey merged", "10\tQ\t0\t0060@\r\nDEADKEY\t0060\r\n0061\t00e0\r\nDEADKEY\t0060\r\n0065\t00e8\r\n", "klc.deadkey.merged@11"},
		{"deadkey chained", "10\tQ\t0\t0060@\r\nDEADKEY\t0060 // grave\r\n0060\t02cb@\r\n", ""},
		{"semicolon comment and remark", "; a comment\r\n   ; another\r\n10\tQ\t0\tq\t// cells\r\n", ""},
		{"middle semicolon is a cell", "10\tQ\t0\t;\r\n", "klc.cell.invalid@8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, codes := parseString(t, header+tt.rows+"ENDKBD\r\n")
			sort.Strings(codes)
			if got := strings.Join(codes, " "); got != tt.codes {
				t.Errorf("got %q, want %q", got, tt.codes)
			}
		})
	}
}

func TestHeaderFields(t *testing.T) {
	tests := []struct {
		name, src, codes string
	}{
		{"missing kbd", "SHIFTSTATE\n0\n1\n2\nENDKBD\n", "klc.kbd.missing@0"},
		{"long name", "KBD\tabcdefghi\t\"x\"\nSHIFTSTATE\n0\n1\n2\nENDKBD\n", "klc.kbd.name@1"},
		{"unterminated", "KBD\tabc\t\"x\nSHIFTSTATE\n0\n1\n2\nENDKBD\n", "klc.kbd.description@1 klc.quote.unterminated@1"},
		{"slashes in quotes", "KBD\tabc\t\"a // b\"\nSHIFTSTATE\n0\n1\n2\nENDKBD\n", ""},
		{"no ctrl", "KBD\tabc\t\"x\"\nSHIFTSTATE\n0\n1\nENDKBD\n", "klc.shiftstate.ctrl@2"},
		{"one state", "KBD\tabc\t\"x\"\nSHIFTSTATE\n2\nENDKBD\n", "klc.shiftstate.count@2"},
		{"repeated keyword", "KBD\tabc\t\"x\"\nKBD\tabc\t\"x\"\nSHIFTSTATE\n0\n2\nENDKBD\n", "klc.keyword.repeated@2"},
		{"lower-case keywords", "kbd\tabc\t\"x\"\nshiftstate\n0\n2\nendkbd\n", ""},
		{"no endkbd", "KBD\tabc\t\"x\"\nSHIFTSTATE\n0\n2\n", "klc.endkbd.missing@0"},
		{"garbage after endkbd", "KBD\tabc\t\"x\"\nSHIFTSTATE\n0\n2\nENDKBD\nnot parsed at all\n", ""},
		{"bad locale", "KBD\tabc\t\"x\"\nLOCALENAME\t\"en_US\"\nSHIFTSTATE\n0\n2\nENDKBD\n", "klc.localename@2"},
		{"bad localeid", "KBD\tabc\t\"x\"\nLOCALEID\t\"409\"\nSHIFTSTATE\n0\n2\nENDKBD\n", "klc.localeid@2"},
		{"attributes", "KBD\tabc\t\"x\"\nATTRIBUTES\nAltGr SHIFTLOCK\nlrm_rlm\nSHIFTSTATE\n0\n2\nENDKBD\n", ""},
		{"unknown attribute", "KBD\tabc\t\"x\"\nATTRIBUTES\nFOO\nSHIFTSTATE\n0\n2\nENDKBD\n", "klc.attributes.unknown@3"},
		{"modifier owns its bit", "KBD\tabc\t\"x\"\nMODIFIERS\nOEM_8\tKBDROYA\nSHIFTSTATE\n0\n2\n16\nENDKBD\n", ""},
		{"modifier bit by position", "KBD\tabc\t\"x\"\nMODIFIERS\nOEM_8\tKBDROYA\nSHIFTSTATE\n0\n2\n8\nENDKBD\n", "klc.shiftstate.bits@4"},
		{"line before keyword", "10\tQ\nKBD\tabc\t\"x\"\nSHIFTSTATE\n0\n2\nENDKBD\n", "klc.line.outside@1"},
		{"nul", "KBD\tabc\t\"x\x01\"\nSHIFTSTATE\n0\n2\nENDKBD\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, codes := parseString(t, tt.src)
			sort.Strings(codes)
			if got := strings.Join(codes, " "); got != tt.codes {
				t.Errorf("got %q, want %q", got, tt.codes)
			}
		})
	}
}

func TestFields(t *testing.T) {
	src := "KBD\tkbdt\t\"Test layout\"\nCOPYRIGHT\t\"(c) me\"\nCOMPANY\t\"\"\nLOCALENAME\t\"fo-FO\"\nLOCALEID\t\"00000438\"\n" +
		"VERSION\t7.5\nSHIFTSTATE\n0\n1\n2\nLAYOUT\n10\tQ\tSGCap\tq\tQ\n-1\t-1\t0\t0101\t0100\n" +
		"KEYNAME\n01\tEsc\n0e\t\"Back Space\"\nKEYNAME_EXT\n1c\t\"Num Enter\"\nKEYNAME_DEAD\n0060\t\"GRAVE ACCENT\"\n" +
		"DESCRIPTIONS\n0409\tTest layout two\nLANGUAGENAMES\n0438\tFaroese (Faroe Islands)\nENDKBD\n"
	f, codes := parseString(t, src)
	if len(codes) != 0 {
		t.Fatalf("diagnostics %v", codes)
	}
	if f.Name != "kbdt" || f.Description != "Test layout" || *f.Copyright != "(c) me" || *f.Company != "" ||
		*f.LocaleName != "fo-FO" || *f.LocaleID != "00000438" || *f.Version != "7.5" {
		t.Errorf("header fields %+v", f)
	}
	k := f.Keys[0]
	if k.Cap != CapSGCAPS || len(k.SGCap) != 2 || k.SGCap[0].Rune != 0x101 {
		t.Errorf("sgcap key %+v", k)
	}
	if f.KeyNames[1].Name != "Back Space" || f.KeyNamesExt[0].Code != 0x1c || f.KeyNamesDead[0].Name != "GRAVE ACCENT" {
		t.Errorf("names %+v %+v %+v", f.KeyNames, f.KeyNamesExt, f.KeyNamesDead)
	}
	if f.Descriptions[0].Text != "Test layout two" || f.LanguageNames[0].LangID != 0x438 {
		t.Errorf("texts %+v %+v", f.Descriptions, f.LanguageNames)
	}
}

func TestEncodings(t *testing.T) {
	src := header + "10\tQ\t0\tq\té\r\nENDKBD\r\n"
	le := func(s string, bom bool, big bool) []byte {
		u := utf16.Encode([]rune(s))
		if bom {
			u = append([]uint16{0xFEFF}, u...)
		}
		var b []byte
		for _, c := range u {
			if big {
				b = append(b, byte(c>>8), byte(c))
			} else {
				b = append(b, byte(c), byte(c>>8))
			}
		}
		return b
	}
	cp := []byte(strings.ReplaceAll(src, "é", "\xe9"))
	for name, b := range map[string][]byte{
		"utf16le bom": le(src, true, false), "utf16le no bom": le(src, false, false), "utf16be bom": le(src, true, true),
		"utf8 bom": append([]byte{0xEF, 0xBB, 0xBF}, src...), "utf8": []byte(src), "cp1252": cp,
		"lf": []byte(strings.ReplaceAll(src, "\r\n", "\n")), "cr": []byte(strings.ReplaceAll(src, "\r\n", "\r")),
	} {
		f, r := Parse(b, name)
		if r.HasErrors() || len(f.Keys) != 1 || f.Keys[0].Cells[1].Rune != 0xE9 {
			t.Errorf("%s: %s %+v", name, r.Summary(), f)
		}
	}
	if _, r := Parse([]byte{0xFF, 0xFE, 'K'}, "odd"); !r.HasErrors() {
		t.Error("odd UTF-16 accepted")
	}
	if got := cp1252[0x80-0x80]; got != 0x20AC {
		t.Errorf("cp1252 80 = %04x", got)
	}
}

func TestNULRejected(t *testing.T) {
	src := utf16.Encode([]rune(header + "10\tQ\t0\tq\x00\r\nENDKBD\r\n"))
	b := []byte{0xFF, 0xFE}
	for _, c := range src {
		b = append(b, byte(c), byte(c>>8))
	}
	_, r := Parse(b, "nul")
	if !strings.Contains(strings.Join(r.Codes(), ","), "klc.nul") {
		t.Errorf("NUL not rejected: %s", r.Summary())
	}
}

// TestCorpus parses every input of the reference specification's test folders and compares the diagnostics with
// testdata/corpus.golden. The expectations follow the [YAKBD] rules of klc-format, not what MSKLC's compiler
// did with the files.
func TestCorpus(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/reference-spec/*/*.klc")
	if len(files) == 0 {
		t.Fatal("no corpus")
	}
	var out []string
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(filepath.Dir(path)) + "/" + filepath.Base(path)
		f, r := Parse(b, name)
		seen := map[string]bool{}
		var codes []string
		for _, e := range r.Entries {
			c := fmt.Sprintf("%s:%s@%d", e.Severity, e.Code, e.Where.Line)
			if !seen[c] {
				seen[c] = true
				codes = append(codes, c)
			}
		}
		out = append(out, name+" "+strings.Join(codes, " "))
		if !r.HasErrors() {
			roundTrip(t, name, f)
		}
	}
	got := strings.Join(out, "\n") + "\n"
	golden := filepath.Join("testdata", "corpus.golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(golden, []byte(got), 0o644)
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test -update once)", err)
	}
	if string(want) != got {
		t.Errorf("corpus diagnostics changed:\n%s", got)
	}
}

// roundTrip writes f and parses the result: the two files must be equal apart from line numbers and VERSION.
func roundTrip(t *testing.T, name string, f *File) {
	t.Helper()
	for _, opt := range []WriteOptions{{}, {ForMSKLC: true}} {
		b, _ := Write(f, opt)
		g, r := Parse(b, name+" (written)")
		if r.HasErrors() {
			t.Errorf("%s: written file has diagnostics:\n%s", name, r.Summary())
			continue
		}
		a, c := normalise(f), normalise(g)
		if !reflect.DeepEqual(a, c) {
			t.Errorf("%s: round trip differs\n got %+v\nwant %+v", name, c, a)
		}
	}
}

func normalise(f *File) File {
	n := *f
	n.Version = nil
	n.Keys = nil
	for _, k := range f.Keys {
		k.Line, k.SGCapLine = 0, 0
		n.Keys = append(n.Keys, k)
	}
	n.Modifiers = nil
	for _, m := range f.Modifiers {
		m.Line = 0
		n.Modifiers = append(n.Modifiers, m)
	}
	n.Ligatures = nil
	for _, l := range f.Ligatures {
		l.Line = 0
		n.Ligatures = append(n.Ligatures, l)
	}
	n.DeadKeys = nil
	for _, d := range f.DeadKeys {
		d.Line = 0
		var es []DeadEntry
		for _, e := range d.Entries {
			e.Line = 0
			es = append(es, e)
		}
		d.Entries = es
		n.DeadKeys = append(n.DeadKeys, d)
	}
	n.EndKBD = true
	return n
}

func TestWriterCanonicalForm(t *testing.T) {
	f, _ := parseString(t, header+"10\tQ\t1\tq\tQ\t-1\té\r\n29\tOEM_3\t0\t0060@\t1f600\r\nDEADKEY\t0060\r\n0061\t00e0\r\nENDKBD\r\n")
	b, r := Write(f, WriteOptions{})
	if b[0] != 0xFF || b[1] != 0xFE {
		t.Error("no UTF-16LE byte-order mark")
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
	}
	s := string(utf16.Decode(u))
	for _, want := range []string{"KBD\tkbdt\t\"Test\"\r\n", "\r\n10\tQ\t1\tq\tQ\t-1\t00e9\r\n", "\r\n29\tOEM_3\t0\t0060@\t1f600\r\n",
		"DEADKEY\t0060\r\n\r\n0061\t00e0\r\n", "\r\nENDKBD\r\n", "VERSION\t1.0\r\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "\n\n") && !strings.Contains(s, "\r\n\r\n") {
		t.Error("bare LF")
	}
	_ = r
}

func TestWriterMSKLCWarnings(t *testing.T) {
	f, _ := parseString(t, header+"10\tQ\t0\t%%\t-1\t-1\t1fbf@\r\ne05b\tLWIN\r\nLIGATURE\r\nQ\t0\t1\t2\t3\t4\t5\r\nDEADKEY\t1fbf\r\n0061\t1fbf@\r\nDESCRIPTIONS\r\n0409\tMy LAYOUT\r\nENDKBD\r\n")
	b, r := Write(f, WriteOptions{ForMSKLC: true})
	codes := strings.Join(r.Codes(), " ")
	for _, c := range []string{"klc.msklc.ligature-length", "klc.msklc.chained", "klc.msklc.e0-row", "klc.msklc.vk-name", "klc.msklc.keyword-in-text"} {
		if !strings.Contains(codes, c) {
			t.Errorf("missing %s in %s", c, codes)
		}
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
	}
	if !strings.Contains(string(utf16.Decode(u)), "᾿@") {
		t.Error("dead character from U+1000 up not written as a literal for MSKLC")
	}
	if _, r := Write(f, WriteOptions{}); len(r.Entries) != 0 {
		t.Errorf("canonical writer warns: %s", r.Summary())
	}
}
