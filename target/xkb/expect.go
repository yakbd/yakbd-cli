package xkb

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
	"github.com/yakbd/yakbd-cli/target/xkb/keyname"
)

// Expectation is one case of the Linux check's cases.tsv (tools/xkb-check/README.md): the keys to press and the
// text the layout must type.
type Expectation struct {
	Name   string
	Layout string // the layout name the stack selects
	Keys   string // <NAME> taps a key, +<NAME> holds it, -<NAME> lets it go
	Want   string
}

// Expectations derives what the layout must type from the model, not from the built files: model.Resolve (SPEC
// §9.3) and the dead keys' entries (SPEC §14.2), at the build's key names. It covers every built key in every
// build state (Caps Lock by tapping CapsLock before and after), and every dead key a key types followed by each
// one-character base of its entries that a key types. Outputs with white space or control characters and empty
// ones are left out (the check splits lines and compares text). At most max lines (0: all), dead-key lines first.
func Expectations(l *model.Layout, res *build.Result, max int) []Expectation {
	norm := l.Normalizes()
	layoutName := res.Pins.SymbolsName
	if v := build.VariantOf(res.Pins); v != "" {
		layoutName += "(" + v + ")"
	}
	shiftKeys := func(c string) []string {
		var ps []string
		switch c {
		case "altR":
			ps = []string{"AltRight"}
		case "alt":
			ps = []string{"AltRight"}
		default:
			if cm := l.CustomModifiers[c]; cm != nil && len(cm.Positions) > 0 {
				ps = []string{cm.Positions[0]}
			}
		}
		var names []string
		for _, p := range ps {
			if k := res.Pins.Keys[p]; k != nil && k.KeyName != "" {
				names = append(names, k.KeyName)
			} else if r, ok := keyname.ForPosition(p); ok {
				names = append(names, r.KeyName)
			}
		}
		return names
	}
	var l3, l5 []string
	if m := res.Pins.Modifiers; m != nil {
		l3, l5 = shiftKeys(m.LevelThree), shiftKeys(m.LevelFive)
	}
	state := func(s int) model.State {
		st := model.State{Shift: s&build.StShift != 0, Caps: s&build.StLock != 0, Custom: map[string]bool{}}
		set := func(c string) {
			switch c {
			case "altR", "alt":
				st.AltR = true
			case "":
			default:
				st.Custom[c] = true
			}
		}
		if m := res.Pins.Modifiers; m != nil {
			if s&build.StL3 != 0 {
				set(m.LevelThree)
			}
			if s&build.StL5 != 0 {
				set(m.LevelFive)
			}
		}
		return st
	}
	// keys wraps a key name in the holds of state s
	keys := func(s int, name string) string {
		var pre, post []string
		hold := func(ns []string) {
			for _, n := range ns {
				pre = append(pre, "+<"+n+">")
				post = append([]string{"-<" + n + ">"}, post...)
			}
		}
		if s&build.StShift != 0 {
			hold([]string{"LFSH"})
		}
		if s&build.StL3 != 0 {
			hold(l3)
		}
		if s&build.StL5 != 0 {
			hold(l5)
		}
		k := strings.Join(append(append(pre, "<"+name+">"), post...), " ")
		if s&build.StLock != 0 {
			k = "<CAPS> " + k + " <CAPS>"
		}
		return k
	}
	text := func(o model.Output) (string, bool) {
		if o.DeadKey != "" || len(o.Units) == 0 {
			return "", false
		}
		if norm && !o.Exact {
			o = o.NFC()
		}
		t := string(utf16.Decode(o.Units))
		return t, printable(t)
	}
	reachable := func(s int) bool {
		return (s&build.StL3 == 0 || len(l3) > 0) && (s&build.StL5 == 0 || len(l5) > 0)
	}
	typer := map[string]string{} // text -> keys that type it
	deadKeys := map[string]string{}
	var singles []Expectation
	for _, k := range res.Keys {
		if k.Keypad != "" {
			continue
		}
		for s := 0; s < res.States; s++ {
			if !reachable(s) {
				continue
			}
			o := l.Effective(l.Resolve(k.Position, state(s)))
			if o.DeadKey != "" {
				if _, ok := deadKeys[o.DeadKey]; !ok {
					deadKeys[o.DeadKey] = keys(s, k.Name)
				}
				continue
			}
			t, ok := text(o)
			if !ok {
				continue
			}
			ks := keys(s, k.Name)
			if _, ok := typer[t]; !ok && s&build.StLock == 0 {
				typer[t] = ks
			}
			singles = append(singles, Expectation{Name: fmt.Sprintf("%s-s%d", k.Position, s), Layout: layoutName, Keys: ks, Want: t})
		}
	}
	var deads []Expectation
	ids := make([]string, 0, len(deadKeys))
	for id := range deadKeys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		dk := l.DeadKeys[id]
		seen := map[string]bool{}
		for i, e := range dk.Entries {
			if e.Base.Kind == model.ValueDeadKey || e.Result.Kind == model.ValueDeadKey {
				continue
			}
			b := string(utf16.Decode(e.Base.UTF16()))
			if norm && e.Base.Kind == model.ValueText {
				b = e.Base.NFC().Text
			}
			if seen[b] {
				continue
			}
			seen[b] = true
			bk, ok := typer[b]
			r := string(utf16.Decode(e.Result.UTF16()))
			if norm && e.Result.Kind == model.ValueText {
				r = e.Result.NFC().Text
			}
			if !ok || !printable(r) {
				continue
			}
			deads = append(deads, Expectation{Name: fmt.Sprintf("dead-%s-%d", id, i), Layout: layoutName, Keys: deadKeys[id] + " " + bk, Want: r})
		}
	}
	all := append(deads, singles...)
	if max > 0 && len(all) > max {
		all = all[:max]
	}
	return all
}

func printable(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\t' {
			return false
		}
	}
	return true
}

// FormatExpectations writes the expectations as the check's cases.tsv (scenario build).
func FormatExpectations(es []Expectation) []byte {
	var b strings.Builder
	b.WriteString("# case\tlayout\tcompose\tkeys\texpected\n# Generated by yakbd compile -target xkb -emit-expectations from the .kbl.json (the model).\n")
	for _, e := range es {
		fmt.Fprintf(&b, "%s\t%s\tbuild\t%s\t%s\n", e.Name, e.Layout, e.Keys, e.Want)
	}
	return []byte(b.String())
}
