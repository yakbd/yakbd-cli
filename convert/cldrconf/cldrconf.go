// Package cldrconf is the CLDR conformance machinery (docs/cldr.md): it reads a CLDR keyboard with its test file,
// builds it for Windows the way the conformance run does, and turns every check of the CLDR tests into a Windows
// key sequence with the text it must type, or into the reason a Windows build cannot express it. The table-driven
// test of this package runs it over every keyboard of testdata/third_party/cldr-release-49; tools/cldrbundle
// writes the typed cases into a bundle for a Windows guest.
package cldrconf

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/internal/unicode/norm"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// Corpus is the folder of the CLDR files, relative to the repository root.
const Corpus = "testdata/third_party/cldr-release-49/keyboards"

// LangID is the LANGID the conformance build pins: a CLDR keyboard has none and the format has no table from
// BCP 47 to LANGID (kbl D30), so every keyboard gets the same neutral value. It changes no character.
const LangID = "0409"

// Accepted are the loss kinds the conformance build accepts so that the rest of a keyboard still builds:
// positions without a default VK (IntlRo, IntlYen of the abnt2 and jis forms; targets/windows.md Table A) are
// left out of the DLL, and the cases that press them are skipped. The five frame-key warnings every CLDR keyboard gets
// are accepted too (D90). So are the dead-key results without a one-unit form and the dead
// characters that are not one unit, the true losses of CLDR fr (D95).
var Accepted = []string{"windows.vkMissing", "windows.frameKeyWithoutText", "windows.deadKeyResultUnencodable",
	"windows.deadCharacterInvalid"}

// Keyboard is one keyboard of the corpus: the paths of its file and its test file ("" when it has none).
type Keyboard struct {
	Name, Path, TestPath string
}

// Keyboards lists the corpus below repo, sorted by name.
func Keyboards(repo string) ([]Keyboard, error) {
	files, err := filepath.Glob(filepath.Join(repo, Corpus, "3.0", "*.xml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []Keyboard
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".xml")
		k := Keyboard{Name: name, Path: f}
		if t := filepath.Join(repo, Corpus, "test", name+"-test.xml"); fileExists(t) {
			k.TestPath = t
		}
		out = append(out, k)
	}
	return out, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Load reads a keyboard and its tests.
func Load(k Keyboard) (*model.Layout, *loss.Report) {
	r := &loss.Report{}
	b, err := os.ReadFile(k.Path)
	if err != nil {
		r.Errorf("io.read", loss.Where{File: k.Path}, "%v", err)
		return nil, r
	}
	l, cr := cldr.Read(b, filepath.Base(k.Path))
	r.Merge(cr)
	if l == nil {
		return nil, r
	}
	if k.TestPath != "" {
		tb, err := os.ReadFile(k.TestPath)
		if err != nil {
			r.Errorf("io.read", loss.Where{File: k.TestPath}, "%v", err)
			return nil, r
		}
		tests, tr := cldr.ReadTests(tb, filepath.Base(k.TestPath))
		r.Merge(tr)
		if tests == nil {
			return nil, r
		}
		l.Tests = tests
	}
	return l, r
}

// Build lowers l for Windows as the conformance run does: LANGID pinned to LangID where the file has none, the
// Accepted kinds accepted. l is not changed.
func Build(l *model.Layout) (*build.Result, *loss.Report) {
	c := *l
	t := model.Targets{}
	if l.Targets != nil {
		t = *l.Targets
	}
	w := model.WindowsTarget{}
	if t.Windows != nil {
		w = *t.Windows
	}
	if w.Registration == nil || w.Registration.LangID == "" {
		reg := model.Registration{}
		if w.Registration != nil {
			reg = *w.Registration
		}
		reg.LangID = LangID
		w.Registration = &reg
	}
	t.Windows = &w
	c.Targets = &t
	kinds := map[string]string{}
	for _, k := range Accepted {
		kinds[k] = "accept"
	}
	return build.Lower(&c, build.Options{Policy: &model.OnLoss{Kinds: kinds}})
}

// Case is one check of a CLDR test (coverage §3.7, D92): the key presses from the start of the test up to the check
// and the text they must type: what the Windows tables type for them, which together with the start context and
// the emitted texts equals the check (in NFC when the file normalises). Skip says why a Windows
// build cannot express the case; it is empty for a case a Windows guest types.
type Case struct {
	Name string
	Seq  []wincheck.Press
	Want string
	Skip string
}

// Line is the expectations line of a typed case.
func (c Case) Line() wincheck.Line {
	return wincheck.Line{Seq: c.Seq, Kind: "chars", Units: utf16.Encode([]rune(c.Want))}
}

// String is the case as the conformance expectation files record it.
func (c Case) String() string {
	if c.Skip != "" {
		return fmt.Sprintf("case %s: skip: %s", c.Name, c.Skip)
	}
	return fmt.Sprintf("case %s: %s", c.Name, c.Line())
}

// Cases turns every check of l's tests into a case for the build res (from Build). A case is skipped when a step
// before its check is something a hardware key press on Windows cannot do (a touch gesture, emit, backspace, a key
// on no layer Windows reaches, a position the build left out), when typing changes the start context, or when the
// Windows tables, run through the emulator of the ToUnicodeEx rules (internal/wincheck), type another text than
// CLDR expects: then the skip names that text, and the build's loss report says why.
func Cases(l *model.Layout, res *build.Result) []Case {
	vks := map[string]byte{}
	for _, p := range res.PositionList() {
		v := p.RowVK
		if v == 0 {
			v = p.VK
		}
		vks[p.ID] = v
	}
	altGr := res.DLL.Tables.LocaleFlags&windows.KLLF_ALTGR != 0
	nf := func(s string) string { return s }
	if l.Normalizes() {
		nf = norm.NFC // coverage §3.7: compare in NFC when normalisation is enabled
	}
	var out []Case
	for _, tf := range l.Tests {
		for _, s := range tf.Suites {
			for _, t := range s.Tests {
				ctx := ""
				if t.StartContext != nil {
					ctx = *t.StartContext
				}
				var seq []wincheck.Press
				var emits []emitAt
				sticky := ""
				n := 0
				for _, st := range t.Steps {
					switch {
					case st.Keystroke != nil:
						if sticky != "" {
							continue
						}
						p, why := press(l, vks, altGr, st.Keystroke)
						if why != "" {
							sticky = why
							continue
						}
						seq = append(seq, p)
					case st.Emit != nil:
						// coverage §3.7: emit inserts text, not key presses. Windows typing never reads the text
						// before the insertion point, so the guest types the presses only and the emitted text is
						// left out of what it compares.
						emits = append(emits, emitAt{len(seq), *st.Emit})
					case st.Backspace:
						if sticky == "" {
							sticky = "a backspace step (the guest types through ToUnicodeEx, without an edit control)"
						}
					case st.Check != nil:
						n++
						c := Case{Name: fmt.Sprintf("%s/%s/%s#%d", tf.Info.Name, s.Name, t.Name, n), Skip: sticky}
						want := nf(*st.Check)
						switch {
						case c.Skip != "":
						case !strings.HasPrefix(want, ctx):
							c.Skip = "typing changes the start context"
						case len(seq) == 0:
							c.Skip = "no key pressed"
						default:
							full, typed := emulateWith(&res.DLL.Tables, seq, emits)
							if nf(ctx+full) != want {
								c.Skip = fmt.Sprintf("%s: Windows types %+q, CLDR expects %+q", explain(l, ctx+full, want), full, want[len(ctx):])
								break
							}
							c.Want = typed
							c.Seq = append([]wincheck.Press(nil), seq...)
						}
						out = append(out, c)
					}
				}
			}
		}
	}
	return out
}

// Emulate types seq through the emulator of the ToUnicodeEx rules and returns the text.
func Emulate(t *windows.Tables, seq []wincheck.Press) string {
	e := &wincheck.Emulator{T: t}
	var units []uint16
	for _, p := range seq {
		if rc, u := e.ToUnicode(p, 0); rc > 0 {
			units = append(units, u[:min(rc, len(u))]...)
		}
	}
	return string(utf16.Decode(units))
}

// press finds the hardware press of a keystroke, or says why there is none.
func press(l *model.Layout, vks map[string]byte, altGr bool, k *model.Keystroke) (wincheck.Press, string) {
	if len(k.Flick) > 0 || k.LongPress != nil || k.TapCount > 0 {
		return wincheck.Press{}, "general.touchDropped: a touch gesture (flick, long press or multi-tap)"
	}
	if k.Position != "" {
		if len(k.Locks) > 0 {
			return wincheck.Press{}, "a keystroke with locks"
		}
		return pressAt(vks, altGr, k.Position, model.ModifierSet(k.Modifiers))
	}
	if l.Hardware == nil {
		return wincheck.Press{}, "windows.noHardwareLayers: no hardware layers (touch only)"
	}
	why := fmt.Sprintf("key %q is on no hardware layer", k.Key)
	// coverage §3.7 (D92): the first layer in hardware.layers order, then its first modifier set the target can
	// type, then the first position in positions order (the sorted order kbljson writes).
	for _, layer := range l.Hardware.Layers {
		for _, set := range layer.Modifiers {
			for _, pos := range sortedPositions(layer.Bindings) {
				if !binds(l, layer.Bindings[pos], k.Key) {
					continue
				}
				p, w := pressAt(vks, altGr, pos, set)
				if w == "" {
					return p, ""
				}
				why = fmt.Sprintf("key %q: %s", k.Key, w)
			}
		}
	}
	return wincheck.Press{}, why
}

func sortedPositions(m map[string]*model.Binding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// binds says whether a binding is the key id: a {"key": id} binding, or the Text binding the CLDR reader gives an
// implied key the file does not define (SPEC §8).
func binds(l *model.Layout, b *model.Binding, id string) bool {
	if b == nil {
		return false
	}
	if b.Kind == model.BindKey {
		return b.Key == id
	}
	if _, defined := l.Keys[id]; defined || b.Kind != model.BindText {
		return false
	}
	out, ok := implied(id)
	return ok && b.Text == out
}

func implied(id string) (string, bool) {
	if id == "space" {
		return " ", true
	}
	if len(id) == 1 {
		c := id[0]
		if c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			return id, true
		}
	}
	return "", false
}

// pressAt is the press of a position in a modifier set: the position's VK with VK_SHIFT, VK_CONTROL and VK_MENU
// held for the Windows state of the set (targets/windows.md §4: altR is Ctrl+Alt with KLLF_ALTGR, Alt without).
func pressAt(vks map[string]byte, altGr bool, pos string, set model.ModifierSet) (wincheck.Press, string) {
	vk, ok := vks[pos]
	if !ok || vk == 0 || vk == 0xFF {
		return wincheck.Press{}, fmt.Sprintf("windows.vkMissing: position %s is not in the Windows build", pos)
	}
	if set.IsOther() {
		return wincheck.Press{}, "a layer for \"other\" modifier states"
	}
	s := 0
	for _, c := range set {
		switch c {
		case "shift":
			s |= 1
		case "ctrl", "ctrlL", "ctrlR":
			s |= 2
		case "alt", "altL":
			s |= 4
		case "altR":
			if altGr {
				s |= 6
			} else {
				s |= 4
			}
		case "caps":
			return wincheck.Press{}, "a Caps Lock layer (the guest types with Caps Lock off)"
		default:
			return wincheck.Press{}, fmt.Sprintf("modifier %q", c)
		}
	}
	if s&4 != 0 && s&2 == 0 {
		return wincheck.Press{}, "an Alt layer without Ctrl (no typing state on Windows)"
	}
	p := wincheck.Press{VK: vk}
	for _, m := range []struct {
		bit int
		vk  byte
	}{{1, 0x10}, {2, 0x11}, {4, 0x12}} {
		if s&m.bit != 0 {
			p.Mods = append(p.Mods, m.vk)
		}
	}
	return p, ""
}

// emitAt is an emit step: its text, inserted before press number at.
type emitAt struct {
	at   int
	text string
}

// emulateWith types seq through the emulator with the emitted texts inserted at their places (a pending dead key
// stays pending across them, as text placed into the field does not reach ToUnicodeEx). full is the whole text,
// typed what the key presses alone give.
func emulateWith(t *windows.Tables, seq []wincheck.Press, emits []emitAt) (full, typed string) {
	e := &wincheck.Emulator{T: t}
	var all, keys []uint16
	for i := 0; i <= len(seq); i++ {
		for _, em := range emits {
			if em.at == i {
				all = append(all, utf16.Encode([]rune(em.text))...)
			}
		}
		if i == len(seq) {
			break
		}
		if rc, u := e.ToUnicode(seq[i], 0); rc > 0 {
			all = append(all, u[:min(rc, len(u))]...)
			keys = append(keys, u[:min(rc, len(u))]...)
		}
	}
	return string(utf16.Decode(all)), string(utf16.Decode(keys))
}

// explain names the loss kind behind a case Windows types differently (coverage §3.7: skipped cases are listed
// with the kind that explains them): the same text once normalised (D73), or a transform or marker the build
// does not compile. "unexplained" is a tool bug; the conformance test fails on it.
func explain(l *model.Layout, got, want string) string {
	switch {
	case norm.NFC(got) == norm.NFC(want):
		return "general.normalizationNotApplied"
	case l.Transforms != nil && (len(l.Transforms.Simple) > 0 || len(l.Transforms.Backspace) > 0):
		return "windows.transformNotCompiled"
	}
	return "unexplained"
}
