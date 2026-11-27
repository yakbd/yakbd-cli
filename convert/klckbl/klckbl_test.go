package klckbl

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/target/windows/build"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// noExactRoundTrip are corpus files whose meaning the Windows tables cannot hold, so that .klc -> .kbl.json ->
// .klc cannot give the same file: an SGCAP key with a dead key in a shape Windows refuses (dll-format §4.2).
var noExactRoundTrip = map[string]string{
	"sgcaps-dead-both.klc":  "SGCAPS with dead keys",
	"sgcaps-dead-main.klc":  "dead column with another Caps Lock character",
	"sgcaps-dead-shift.klc": "dead column with another Caps Lock character",
}

// TestCorpusRoundTrip converts every parseable corpus file to .kbl.json (schema-valid), back to .klc, and compares.
func TestCorpusRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/reference-spec/*/*.klc")
	n := 0
	for _, p := range files {
		name := filepath.Base(p)
		b, _ := os.ReadFile(p)
		f, r := klc.Parse(b, name)
		if r.HasErrors() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			l, _ := FromKLC(f, name)
			out, wr := kbljson.Write(l)
			if out == nil {
				t.Fatalf("not schema-valid: %s", wr.Summary())
			}
			l2, _ := kbljson.Read(out, name)
			g, er := ToKLC(l2, build.Options{})
			if why, skip := noExactRoundTrip[name]; skip {
				if g != nil && !er.HasErrors() {
					t.Logf("exports (%s)", why)
				}
				return
			}
			if g == nil {
				t.Fatalf("export failed: %s", er.Summary())
			}
			a, c := normalise(f), normalise(g)
			if !reflect.DeepEqual(a, c) {
				t.Errorf("round trip differs: %s", firstDiff(a, c))
			}
			if _, wr := klc.Write(g, klc.WriteOptions{}); wr.HasErrors() {
				t.Errorf("write: %s", wr.Summary())
			}
		})
		n++
	}
	if n < 20 {
		t.Errorf("only %d corpus files", n)
	}
}

// normalise drops what carries no meaning: line numbers, row order, trailing empty cells, VERSION, ENDKBD, the
// ALTGR attribute that MSKLC implies for a Ctrl+Alt column, and duplicate key names.
func normalise(f *klc.File) klc.File {
	n := *f
	n.Version, n.EndKBD = nil, true
	n.Keys = nil
	pairLig := map[[2]int]rune{} // (vk, column) -> supplementary character of a surrogate-pair ligature
	for _, lg := range f.Ligatures {
		if len(lg.Chars) == 2 && utf16.IsSurrogate(rune(lg.Chars[0])) {
			pairLig[[2]int{int(lg.VK), lg.Column}] = utf16.DecodeRune(rune(lg.Chars[0]), rune(lg.Chars[1]))
		}
	}
	for _, k := range f.Keys {
		k.Line, k.SGCapLine = 0, 0
		if !k.HasCells && k.Cap == 0 {
			if d, ok := vk.DefaultFor(k.Scan); ok && byte(d) == k.VK {
				continue // a row that states the default
			}
		}
		cells := append([]klc.Cell(nil), k.Cells...)
		for c := range cells {
			if r, ok := pairLig[[2]int{int(k.VK), c}]; ok && cells[c].Kind == klc.CellLigature {
				cells[c] = klc.Cell{Kind: klc.CellChar, Rune: r}
			}
		}
		k.Cells = cells
		for len(k.Cells) > 0 && k.Cells[len(k.Cells)-1].Kind == klc.CellNone {
			k.Cells = k.Cells[:len(k.Cells)-1]
		}
		if len(k.Cells) == 0 && k.Cap == 0 {
			k.HasCells, k.Cells = false, nil
		}
		n.Keys = append(n.Keys, k)
	}
	sort.Slice(n.Keys, func(i, j int) bool { return n.Keys[i].Scan < n.Keys[j].Scan })
	n.Modifiers = nil
	for _, m := range f.Modifiers {
		m.Line = 0
		n.Modifiers = append(n.Modifiers, m)
	}
	n.Ligatures = nil
	for _, lg := range f.Ligatures {
		if _, ok := pairLig[[2]int{int(lg.VK), lg.Column}]; ok {
			continue
		}
		lg.Line = 0
		n.Ligatures = append(n.Ligatures, lg)
	}
	sort.Slice(n.Ligatures, func(i, j int) bool {
		if n.Ligatures[i].VK != n.Ligatures[j].VK {
			return n.Ligatures[i].VK < n.Ligatures[j].VK
		}
		return n.Ligatures[i].Column < n.Ligatures[j].Column
	})
	n.DeadKeys = nil
	for _, d := range f.DeadKeys {
		d.Line = 0
		var es []klc.DeadEntry
		for _, e := range d.Entries {
			e.Line = 0
			es = append(es, e)
		}
		d.Entries = es
		n.DeadKeys = append(n.DeadKeys, d)
	}
	sort.SliceStable(n.DeadKeys, func(i, j int) bool { return n.DeadKeys[i].Dead < n.DeadKeys[j].Dead })
	altgr := false
	for _, s := range f.ShiftStates {
		altgr = altgr || s&6 == 6
	}
	n.Attributes = nil
	for _, a := range f.Attributes {
		if a != "ALTGR" {
			n.Attributes = append(n.Attributes, a)
		}
	}
	if altgr || strings.Contains(strings.Join(f.Attributes, " "), "ALTGR") {
		n.Attributes = append(n.Attributes, "ALTGR")
	}
	sort.Strings(n.Attributes)
	sort.Slice(n.KeyNames, func(i, j int) bool { return n.KeyNames[i].Code < n.KeyNames[j].Code })
	sort.Slice(n.KeyNamesExt, func(i, j int) bool { return n.KeyNamesExt[i].Code < n.KeyNamesExt[j].Code })
	sort.Slice(n.KeyNamesDead, func(i, j int) bool { return n.KeyNamesDead[i].Dead < n.KeyNamesDead[j].Dead })
	return n
}

func TestImportConventions(t *testing.T) {
	b, _ := os.ReadFile("../../testdata/reference-spec/vm/kbdtst.klc")
	f, _ := klc.Parse(b, "kbdtst.klc")
	l, r := FromKLC(f, "kbdtst.klc")
	if l.Metadata.ID != "en-US" || l.Targets.Windows.DLLName != "kbdtst" || l.Targets.Windows.Registration.LangID != "0409" {
		t.Errorf("metadata %+v", l.Metadata)
	}
	ids := []string{}
	for _, layer := range l.Hardware.Layers {
		ids = append(ids, layer.ID)
	}
	if got := strings.Join(ids, " "); got != "s0 s1 s2 s6 capsS0 capsS1" {
		t.Errorf("layers %s", got)
	}
	if s6 := l.Hardware.Layers[3]; len(s6.Modifiers) != 2 || s6.Modifiers[0][0] != "altR" {
		t.Errorf("s6 modifiers %v", s6.Modifiers)
	}
	if nd := l.Positions["NumpadDecimal"]; nd == nil || nd.NumLock == nil || !*nd.NumLock || l.Targets.Windows.Keys["NumpadDecimal"].VK != "DELETE" {
		t.Errorf("D33 number-pad row: %+v %+v", nd, l.Targets.Windows.Keys["NumpadDecimal"])
	}
	if dk := l.DeadKeys["u0060"]; dk == nil || len(dk.Entries) != 3 || dk.Name != "GRAVE ACCENT" {
		t.Errorf("dead key %+v", dk)
	}
	if b := l.Hardware.Layers[1].Bindings["IntlBackslash"]; b == nil || b.Text != "á" {
		t.Errorf("ligature binding %+v", b)
	}
	if !strings.Contains(strings.Join(r.Codes(), " "), "klc.impliedRows") {
		t.Errorf("implied rows not reported: %s", r.Summary())
	}
	if b := l.Hardware.Layers[2].Bindings["Backspace"]; b == nil || b.Text != "\x7f" {
		t.Errorf("implied Ctrl+Backspace %+v", b)
	}
	res, br := build.Lower(l, build.Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
}

func firstDiff(a, b klc.File) string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := 0; i < va.NumField(); i++ {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			name := va.Type().Field(i).Name
			if name == "Keys" {
				ka, kb := a.Keys, b.Keys
				for j := 0; j < len(ka) || j < len(kb); j++ {
					if j >= len(ka) || j >= len(kb) || !reflect.DeepEqual(ka[j], kb[j]) {
						var x, y any = "-", "-"
						if j < len(ka) {
							x = ka[j]
						}
						if j < len(kb) {
							y = kb[j]
						}
						return fmt.Sprintf("Keys[%d]: %+v vs %+v", j, x, y)
					}
				}
			}
			return fmt.Sprintf("%s: %+v vs %+v", name, va.Field(i).Interface(), vb.Field(i).Interface())
		}
	}
	return ""
}

// TestD41AltL: with AltGr, an Alt-only state imports as altL, so that it does not overlap the altR layer.
func TestD41AltL(t *testing.T) {
	src := "KBD\tkbdt\t\"T\"\nSHIFTSTATE\n0\n1\n2\n4\n6\nLAYOUT\n10\tQ\t0\tq\tQ\t-1\tz\t0040\nENDKBD\n"
	f, r := klc.Parse([]byte(src), "t")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := FromKLC(f, "t")
	if !*l.Targets.Windows.LocaleFlags.AltGr {
		t.Error("altGr not pinned")
	}
	var s4, s6 []string
	for _, layer := range l.Hardware.Layers {
		switch layer.ID {
		case "s4":
			s4 = layer.Modifiers[0]
		case "s6":
			s6 = layer.Modifiers[0]
		}
	}
	if strings.Join(s4, " ") != "altL" || strings.Join(s6, " ") != "altR" {
		t.Errorf("s4 %v s6 %v", s4, s6)
	}
	if r := l.Check(); r.HasErrors() {
		t.Errorf("layers overlap: %s", r.Summary())
	}
}

// TestImpliedRowsOnNamedKey: a KEYNAME line makes the position of its scan code, and the implied rows (D36) still
// apply to it: MSKLC adds the Escape row whether or not the file names the key.
func TestImpliedRowsOnNamedKey(t *testing.T) {
	src := "KBD\tkbdt\t\"T\"\nSHIFTSTATE\n0\n1\n2\nLAYOUT\n10\tQ\t0\tq\tQ\t-1\nKEYNAME\n01\tEsc\n0e\tBackspace\nENDKBD\n"
	f, r := klc.Parse([]byte(src), "t")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := FromKLC(f, "t")
	for _, layer := range l.Hardware.Layers {
		want := map[string]string{"s0": "\x1b", "s1": "\x1b", "s2": "\x1b"}[layer.ID]
		if b := layer.Bindings["Escape"]; b == nil || b.Text != want {
			t.Errorf("layer %s: Escape %v, want %q", layer.ID, b, want)
		}
		if b := layer.Bindings["Backspace"]; b == nil {
			t.Errorf("layer %s: no Backspace binding", layer.ID)
		}
	}
}
