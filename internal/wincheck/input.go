package wincheck

import (
	"fmt"
	"strings"
)

// Real input (the sign-in test, yakbd-wincheck -signin check): keys are sent through the input queue into a window
// of the runner, and the characters that window receives (WM_CHAR) are compared with the expectations. Only lines
// that real input can reproduce without side effects are used.

// Typer sends key presses as real input into a window that has the layout active and returns the UTF-16 units the
// window received as WM_CHAR. A sequence is a dead key followed by a key, or one key.
type Typer interface {
	Type(seq []Press) ([]uint16, error)
}

// shiftOnly reports a press with no modifier or only Shift, on a key real input can send without side effects:
// no number-pad keys (their meaning depends on Num Lock), no keys above VK_DIVIDE.
func shiftOnly(p Press) bool {
	if p.VK >= 0x60 && p.VK <= 0x87 || p.VK == 0x5B || p.VK == 0x5C || p.VK == 0x5D {
		return false
	}
	for _, m := range p.Mods {
		if m != 0x10 {
			return false
		}
	}
	return true
}

// InputLines picks the lines of an expectations file that the real-input check types: single keys and dead-key
// sequences giving characters, pressed alone or with Shift. At most maxKeys single keys and maxSeqs sequences
// are taken, in file order, to keep a run in an emulated guest short.
func InputLines(lines []Line, maxKeys, maxSeqs int) []Line {
	var out []Line
	keys, seqs := 0, 0
	for _, l := range lines {
		if l.Kind != "chars" || !shiftOnly(l.Key) || l.Dead != nil && !shiftOnly(*l.Dead) {
			continue
		}
		if l.Dead == nil && keys < maxKeys {
			keys++
			out = append(out, l)
		}
		if l.Dead != nil && seqs < maxSeqs {
			seqs++
			out = append(out, l)
		}
	}
	return out
}

// CheckInput types every line through t and returns the differences.
func CheckInput(lines []Line, t Typer) []Diff {
	var diffs []Diff
	for _, l := range lines {
		seq := []Press{l.Key}
		if l.Dead != nil {
			seq = []Press{*l.Dead, l.Key}
		}
		got, err := t.Type(seq)
		switch {
		case err != nil:
			diffs = append(diffs, Diff{l, "input failed: " + err.Error()})
			return diffs // the window lost the input; the rest would only repeat it
		case !equalUnits(got, l.Units):
			diffs = append(diffs, Diff{l, "WM_CHAR " + units(got)})
		}
	}
	return diffs
}

func equalUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func units(u []uint16) string {
	if len(u) == 0 {
		return "nothing"
	}
	var s []string
	for _, x := range u {
		s = append(s, fmt.Sprintf("%04x", x))
	}
	return strings.Join(s, " ")
}

// EmulatorTyper is a Typer for tests: it types through the emulator as a window would see it (each press of the
// sequence translated for real).
type EmulatorTyper struct{ E *Emulator }

// Type implements Typer.
func (t EmulatorTyper) Type(seq []Press) ([]uint16, error) {
	var out []uint16
	for _, p := range seq {
		rc, u := t.E.ToUnicode(p, 0)
		if rc > 0 {
			out = append(out, u[:rc]...)
		}
	}
	return out, nil
}
