package build

import (
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// nfcLayout has outputs NFC changes: U+2126 (singleton) and a + U+0301 on keys, a spacing U+1FFD (singleton of
// U+00B4), a dead-key result U+212B (singleton of U+00C5) and a base A + U+030A (NFC U+00C5). Shift+KeyQ is the
// CodeUnits form of U+2126, which is exact. SETTINGS and EXTRA are replaced per case.
const nfcLayout = `{"kbl":"0.1","metadata":{"id":"en","name":"T"},SETTINGS
 "positions":{"KeyQ":{"scanCode":"10"},"KeyW":{"scanCode":"11"},"Backquote":{"scanCode":"29"}},
 "hardware":{"layers":[
  {"modifiers":[[]],"bindings":{"KeyQ":"Ω","KeyW":"á","Backquote":{"deadKey":"oxia"}}},
  {"modifiers":[["shift"]],"bindings":{"KeyQ":{"utf16":["2126"]},"KeyW":"A"}}]},
 "deadKeys":{"oxia":{"spacing":"´","entries":[{"base":"a","result":"Å"}EXTRA]}},
 "onLoss":{"kinds":{"general.codeUnitsAreText":"accept"}},
 "targets":{"windows":{"registration":{"langId":"0409"}}}}`

func nfcCase(t *testing.T, settings, extra string) (*windows.Tables, map[string]string) {
	t.Helper()
	src := strings.NewReplacer("SETTINGS", settings, "EXTRA", extra).Replace(nfcLayout)
	l, r := kbljson.Read([]byte(src), "nfc")
	if l == nil {
		t.Fatal(r.Summary())
	}
	res, r := Lower(l, Options{})
	if res == nil {
		t.Fatal(r.Summary())
	}
	changed := map[string]string{}
	for _, e := range r.Entries {
		if e.Code == kindNormalized {
			changed[e.Where.Path] = e.Detail["before"].(string) + " > " + e.Detail["after"].(string)
		}
	}
	return &res.DLL.Tables, changed
}

func rowOf(tb *windows.Tables, v byte) windows.CharRow {
	for _, ct := range tb.CharTables {
		for _, r := range ct.Rows {
			if r.VK == v {
				return r
			}
		}
	}
	return windows.CharRow{}
}

func TestNormalization(t *testing.T) {
	tb, changed := nfcCase(t, "", `,{"base":"Å","result":"x"}`)
	if q := rowOf(tb, 'Q'); q.Chars[0] != 0x03A9 || q.Chars[1] != 0x2126 {
		t.Errorf("Q %04X, want 03A9 (NFC) and 2126 (CodeUnits, exact)", q.Chars)
	}
	if w := rowOf(tb, 'W'); w.Chars[0] != 0x00E1 || len(tb.Ligatures) != 0 {
		t.Errorf("W %04X ligatures %v, want 00E1 and no ligature", w.Chars, tb.Ligatures)
	}
	want := []windows.DeadKey{{Dead: 0x00B4, Base: 'a', Composed: 0x00C5}, {Dead: 0x00B4, Base: 0x00C5, Composed: 'x'}}
	if len(tb.DeadKeys) != 2 || tb.DeadKeys[0] != want[0] || tb.DeadKeys[1] != want[1] {
		t.Errorf("dead keys %+v, want %+v", tb.DeadKeys, want)
	}
	for path, w := range map[string]string{
		"/hardware/layers (KeyQ)":         "2126 > 03A9",
		"/hardware/layers (KeyW)":         "0061 0301 > 00E1",
		"/deadKeys/oxia/spacing":          "1FFD > 00B4",
		"/deadKeys/oxia/entries/0/result": "212B > 00C5",
	} {
		if changed[path] != w {
			t.Errorf("%s: entry %q, want %q", path, changed[path], w)
		}
	}
	if len(changed) != 4 {
		t.Errorf("entries %v, want 4 (the base is an input; the CodeUnits output is exact)", changed)
	}

	tb, changed = nfcCase(t, `"settings":{"normalization":"disabled"},`, "")
	if q := rowOf(tb, 'Q'); q.Chars[0] != 0x2126 {
		t.Errorf("disabled: Q %04X, want 2126", q.Chars)
	}
	if w := rowOf(tb, 'W'); w.Chars[0] != windows.WCH_LGTR {
		t.Errorf("disabled: W %04X, want a ligature", w.Chars)
	}
	if len(tb.DeadKeys) != 1 || tb.DeadKeys[0] != (windows.DeadKey{Dead: 0x1FFD, Base: 'a', Composed: 0x212B}) {
		t.Errorf("disabled: dead keys %+v", tb.DeadKeys)
	}
	if len(changed) != 0 {
		t.Errorf("disabled: entries %v", changed)
	}
}
