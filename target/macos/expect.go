package macos

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/macos/build"
)

// Expectation is one line of the macOS check's expect.tsv (tools/macos-check/README.md): key strokes and the text
// the layout must type for them.
type Expectation struct {
	Name    string
	Strokes []Stroke
	Want    string
}

// Stroke is one key code with the modifiers the check can send.
type Stroke struct {
	Code                int
	Shift, Option, Caps bool
}

// The modifier combinations the check sends.
var checkStates = []model.State{{}, {Shift: true}, {AltL: true}, {Shift: true, AltL: true}, {Caps: true}}

func strokeState(s model.State) (bool, bool, bool) { return s.Shift, s.AltL, s.Caps }

// Expectations derives what the layout must type from the model, not from the built file: model.Resolve (SPEC
// §9.3) and the dead-key machine of SPEC §14.2, at the key codes of the build. It covers every position with a
// key code below 128 in the check's modifier states, every dead key followed by each base text an entry names, by
// a dead key and by Space. Outputs with control characters are left out (a text view acts on them), and so are
// empty ones. At most max lines (0: all), dead-key lines first.
func Expectations(l *model.Layout, res *build.Result, max int) []Expectation {
	norm := l.Normalizes()
	baseText := func(v model.Value) string { // as the build compares it
		o := model.Output{Units: v.UTF16(), Exact: v.Kind == model.ValueUnits}
		if norm && !o.Exact {
			o = o.NFC()
		}
		return string(utf16.Decode(o.Units))
	}
	text := func(o model.Output) (string, bool) {
		if o.DeadKey != "" || len(o.Units) == 0 {
			return "", false
		}
		if norm && !o.Exact {
			o = o.NFC()
		}
		return string(utf16.Decode(o.Units)), true
	}
	type key struct {
		pos  string
		code int
		st   model.State
	}
	var keys []key
	for _, p := range sortedPositions(res.Codes) {
		if c := res.Codes[p]; c < 128 {
			for _, s := range checkStates {
				keys = append(keys, key{p, c, s})
			}
		}
	}
	out := func(k key) model.Output { return l.Effective(l.Resolve(k.pos, k.st)) }
	stroke := func(k key) Stroke {
		sh, op, ca := strokeState(k.st)
		return Stroke{k.code, sh, op, ca}
	}
	// which key types a text (first in order), which keys are dead keys
	typer := map[string]key{}
	deadKeys := map[string]key{}
	var singles []Expectation
	for _, k := range keys {
		o := out(k)
		if o.DeadKey != "" {
			if _, ok := deadKeys[o.DeadKey]; !ok {
				deadKeys[o.DeadKey] = k
			}
			continue
		}
		t, ok := text(o)
		if !ok {
			continue
		}
		if _, seen := typer[t]; !seen {
			typer[t] = k
		}
		singles = append(singles, Expectation{Name: name(k.pos, k.st), Strokes: []Stroke{stroke(k)}, Want: t})
	}
	var deadLines []Expectation
	ids := make([]string, 0, len(deadKeys))
	for id := range deadKeys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		dk := l.DeadKeys[id]
		first := deadKeys[id]
		seq := func(next ...key) []Stroke {
			s := []Stroke{stroke(first)}
			for _, k := range next {
				s = append(s, stroke(k))
			}
			return s
		}
		// each entry whose base some key types, chained results followed one step
		for _, e := range dk.Entries {
			var bk key
			switch e.Base.Kind {
			case model.ValueDeadKey:
				k, ok := deadKeys[e.Base.DeadKey]
				if !ok {
					continue
				}
				bk = k
			default:
				k, ok := typer[baseText(e.Base)]
				if !ok {
					continue
				}
				bk = k
			}
			typed := TypeModel(l, []key2{{first.pos, first.st}, {bk.pos, bk.st}})
			deadLines = append(deadLines, Expectation{Name: "dead-" + id + "-" + name(bk.pos, bk.st), Strokes: seq(bk), Want: typed})
			if e.Result.Kind == model.ValueDeadKey {
				if f := l.DeadKeys[e.Result.DeadKey]; f != nil {
					for _, fe := range f.Entries {
						if fe.Base.Kind == model.ValueDeadKey {
							continue
						}
						if k3, ok := typer[baseText(fe.Base)]; ok {
							deadLines = append(deadLines, Expectation{Name: "chain-" + id + "-" + e.Result.DeadKey + "-" + name(k3.pos, k3.st),
								Strokes: seq(bk, k3), Want: TypeModel(l, []key2{{first.pos, first.st}, {bk.pos, bk.st}, {k3.pos, k3.st}})})
						}
					}
				}
			}
		}
		if sp, ok := typer[" "]; ok {
			deadLines = append(deadLines, Expectation{Name: "dead-" + id + "-space", Strokes: seq(sp),
				Want: TypeModel(l, []key2{{first.pos, first.st}, {sp.pos, sp.st}})})
		}
		deadLines = append(deadLines, Expectation{Name: "dead-" + id + "-twice", Strokes: seq(first),
			Want: TypeModel(l, []key2{{first.pos, first.st}, {first.pos, first.st}})})
	}
	var all []Expectation
	seenSeq := map[string]bool{}
	for _, e := range append(deadLines, singles...) {
		sig := fmt.Sprint(e.Strokes)
		if e.Want == "" || hasControl(e.Want) || seenSeq[sig] {
			continue
		}
		seenSeq[sig] = true
		all = append(all, e)
	}
	if max > 0 && len(all) > max {
		// keep the dead-key lines (up to half), then spread the rest evenly
		var nd int
		for nd < len(all) && (strings.HasPrefix(all[nd].Name, "dead-") || strings.HasPrefix(all[nd].Name, "chain-")) {
			nd++
		}
		keepD := min(nd, max/2)
		rest := all[nd:]
		n := max - keepD
		picked := append([]Expectation(nil), all[:keepD]...)
		for i := 0; i < n && i < len(rest); i++ {
			picked = append(picked, rest[i*len(rest)/n])
		}
		all = picked
	}
	return all
}

type key2 struct {
	pos string
	st  model.State
}

// TypeModel types positions in states with the dead-key machine of SPEC §14.2 and returns the text emitted
// (markers dropped, each emitted text NFC per settings.normalization, a dead key still pending at the end emits
// nothing).
func TypeModel(l *model.Layout, strokes []key2) string {
	var out []uint16
	pending := ""
	// emit normalises each emitted text on its own, as the build normalises each output (CodeUnits stay exact)
	emit := func(u []uint16, exact bool) {
		if l.Normalizes() && !exact {
			u = model.Output{Units: u}.NFC().Units
		}
		out = append(out, u...)
	}
	spacing := func(id string) []uint16 {
		if dk := l.DeadKeys[id]; dk != nil {
			return dk.Spacing.UTF16()
		}
		return nil
	}
	for _, s := range strokes {
		o := l.Effective(l.Resolve(s.pos, s.st))
		if o.None() {
			continue
		}
		if pending == "" {
			if o.DeadKey != "" {
				pending = o.DeadKey
			} else {
				emit(o.Units, o.Exact)
			}
			continue
		}
		dk := l.DeadKeys[pending]
		var entry *model.DeadKeyEntry
		for i := range dk.Entries {
			e := &dk.Entries[i]
			if o.DeadKey != "" && e.Base.Kind == model.ValueDeadKey && e.Base.DeadKey == o.DeadKey ||
				o.DeadKey == "" && e.Base.Kind != model.ValueDeadKey && string(utf16.Decode(e.Base.UTF16())) == string(utf16.Decode(o.Units)) {
				entry = e
				break
			}
		}
		if entry != nil {
			if entry.Result.Kind == model.ValueDeadKey {
				pending = entry.Result.DeadKey
			} else {
				emit(entry.Result.UTF16(), entry.Result.Kind == model.ValueUnits)
				pending = ""
			}
			continue
		}
		t := o.Units
		if o.DeadKey != "" {
			t = spacing(o.DeadKey)
		}
		switch dk.NoMatch {
		case "emitText":
			emit(t, false)
		case "emitSpacing":
			emit(spacing(pending), false)
		case "emitNothing":
		default:
			emit(spacing(pending), false)
			emit(t, false)
		}
		pending = ""
	}
	return string(utf16.Decode(out))
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r >= 0x7F && r <= 0x9F {
			return true
		}
	}
	return false
}

func name(pos string, s model.State) string {
	n := pos
	if s.Shift {
		n += "+shift"
	}
	if s.AltL {
		n += "+option"
	}
	if s.Caps {
		n += "+caps"
	}
	return n
}

func sortedPositions(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// FormatExpectations writes expect.tsv.
func FormatExpectations(es []Expectation) []byte {
	var b strings.Builder
	b.WriteString("# Generated by yakbd compile -target macos -emit-expectations from the .kbl.json (model, SPEC §9.3 and §14.2).\n")
	b.WriteString("# name<TAB>strokes<TAB>expected text. Strokes: key codes separated by spaces, each with optional +shift +option +caps.\n")
	for _, e := range es {
		var st []string
		for _, s := range e.Strokes {
			t := fmt.Sprint(s.Code)
			if s.Shift {
				t += "+shift"
			}
			if s.Option {
				t += "+option"
			}
			if s.Caps {
				t += "+caps"
			}
			st = append(st, t)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", e.Name, strings.Join(st, " "), escapeExpected(e.Want))
	}
	return []byte(b.String())
}

func escapeExpected(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ':
			b.WriteString(`\s`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r >= 0x7F && r <= 0xA0 || r >= 0x300 && r <= 0x36F || r == 0x200B || r >= 0x2028 && r <= 0x202F:
			fmt.Fprintf(&b, `\u{%X}`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
