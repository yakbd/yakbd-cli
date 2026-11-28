package xkb

import (
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
)

// layoutOf reads a 0.2 file with the given positions, base and shift bindings (JSON objects) and targets.xkb.
func layoutOf(t *testing.T, positions, base, shift, target string) *model.Layout {
	t.Helper()
	if shift == "" {
		shift = "{}"
	}
	js := `{"kbl":"0.2","metadata":{"id":"und","name":"R"},"positions":` + positions +
		`,"hardware":{"layers":[{"id":"b","modifiers":[[]],"bindings":` + base + `},{"id":"s","modifiers":[["shift"]],"bindings":` + shift + `}]}`
	if target != "" {
		js += `,"targets":{"xkb":` + target + `}`
	}
	l, r := kbljson.Read([]byte(js+"}"), "t.kbl.json")
	if l == nil || r.HasErrors() {
		t.Fatal(r.Summary())
	}
	return l
}

func hasKind(r *loss.Report, kind string) bool {
	for _, e := range r.Entries {
		if e.Code == kind {
			return true
		}
	}
	return false
}

// keyLine is the line of key <name> in the first section of the symbols file.
func keyLine(res *build.Result, name string) string {
	for _, l := range strings.Split(string(res.SymbolsText), "\n") {
		if strings.Contains(l, "key <"+name+">") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// TestReplacementCharacter: a real U+FFFD is a character (rule characters: UFFFD); code units that are not
// well-formed UTF-16 are not (xkb.outputUnencodable).
func TestReplacementCharacter(t *testing.T) {
	res := built(t, layoutOf(t, `{"KeyA":{}}`, `{"KeyA":"�"}`, "", ""))
	if l := keyLine(res, "AC01"); !strings.Contains(l, "UFFFD") {
		t.Errorf("U+FFFD: %s", l)
	}
	_, r := build.Build(layoutOf(t, `{"KeyA":{}}`, `{"KeyA":{"utf16":["D800"]}}`, "", ""), build.Options{})
	if !hasKind(r, "xkb.outputUnencodable") {
		t.Errorf("a lone surrogate: %s", r.Summary())
	}
}

// TestNothingIsVoidSymbol: a level that types nothing is VoidSymbol and every level of the type is written, so
// that libxkbcommon does not fill it from symbols/pc (Numpad1 without numLock: Shift would type KP_1), kbl D146.
func TestNothingIsVoidSymbol(t *testing.T) {
	res := built(t, layoutOf(t, `{"Numpad1":{},"KeyA":{}}`, `{"Numpad1":"¹","KeyA":"a"}`, `{"KeyA":"A"}`, ""))
	if l := keyLine(res, "KP1"); !strings.Contains(l, `"TWO_LEVEL"`) || !strings.Contains(l, "[ onesuperior, VoidSymbol ]") {
		t.Errorf("Numpad1: %s", l)
	}
	if strings.Contains(string(res.SymbolsText), "NoSymbol") {
		t.Errorf("NoSymbol written:\n%s", res.SymbolsText)
	}
}

// TestCtrlAltKeys: the number-pad operators and F1-F12 get CTRL+ALT (five levels, the fifth NoSymbol for the
// system's Control+Alt action), so that X11 does not refuse the keymap (every converted .klc file), kbl D146.
func TestCtrlAltKeys(t *testing.T) {
	res := built(t, read(t, "../../tools/xkb-check/gen/gen-klc/layout.kbl.json"))
	for _, k := range []string{"KPMU", "KPSU", "KPAD", "KPDV"} {
		if l := keyLine(res, k); !strings.Contains(l, `"CTRL+ALT"`) || strings.Count(l, ",") != 5 || !strings.HasSuffix(l, "NoSymbol ] };") {
			t.Errorf("%s: %s", k, l)
		}
	}
	res = built(t, layoutOf(t, `{"F1":{}}`, `{"F1":"1"}`, `{"F1":"!"}`, ""))
	if l := keyLine(res, "FK01"); !strings.Contains(l, `"CTRL+ALT"`) || !strings.Contains(l, "[ 1, exclam, VoidSymbol, VoidSymbol, NoSymbol ]") {
		t.Errorf("F1: %s", l)
	}
}

// TestUnicodeKeysymNames: a Unicode keysym is written U+hex even where xorgproto names it, since older
// libxkbcommon does not know the newer names (leftsingleanglequotemark, U+2039), kbl D147.
func TestUnicodeKeysymNames(t *testing.T) {
	res := built(t, layoutOf(t, `{"KeyA":{},"KeyB":{},"KeyC":{}}`, `{"KeyA":"‹","KeyB":"œ","KeyC":"ա"}`, "", ""))
	for k, want := range map[string]string{"AC01": "U2039", "AB05": "oe", "AB03": "U0561"} {
		if l := keyLine(res, k); !strings.Contains(l, "[ "+want+",") {
			t.Errorf("%s: want %s: %s", k, want, l)
		}
	}
}

// TestCapsLockStacks: a letter that Caps Lock leaves lower case is reported (GTK 4 on X11 upper-cases it), kbl D148.
func TestCapsLockStacks(t *testing.T) {
	_, r := build.Build(read(t, "../../tools/xkb-check/gen/gen-probe/layout.kbl.json"), build.Options{})
	if !hasKind(r, "xkb.capsLockStacks") {
		t.Errorf("gen-probe: %s", r.Summary())
	}
	l := layoutOf(t, `{"KeyA":{}}`, `{"KeyA":"a"}`, `{"KeyA":"A"}`, "")
	l.Hardware.Layers[1].Modifiers = append(l.Hardware.Layers[1].Modifiers, model.ModifierSet{"caps"})
	_, r = build.Build(l, build.Options{})
	if hasKind(r, "xkb.capsLockStacks") {
		t.Errorf("a letter Caps Lock capitalises: %s", r.Summary())
	}
}

// TestSystemLayoutShadowed: a variant whose symbols name is not a yakbd- name may shadow a system layout, kbl D149.
func TestSystemLayoutShadowed(t *testing.T) {
	_, r := build.Build(layoutOf(t, `{"KeyA":{}}`, `{"KeyA":"a"}`, "", `{"symbolsName":"fo","variant":"v"}`), build.Options{})
	if !hasKind(r, "xkb.systemLayoutShadowed") {
		t.Errorf("fo(v): %s", r.Summary())
	}
	for _, target := range []string{`{"variant":"v"}`, `{"symbolsName":"fo"}`} {
		_, r = build.Build(layoutOf(t, `{"KeyA":{}}`, `{"KeyA":"a"}`, "", target), build.Options{})
		if hasKind(r, "xkb.systemLayoutShadowed") {
			t.Errorf("%s: %s", target, r.Summary())
		}
	}
}
