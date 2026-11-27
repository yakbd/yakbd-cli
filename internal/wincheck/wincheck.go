// Package wincheck is the portable part of the Windows test runner (cmd/yakbd-wincheck): it reads the
// expectations that `yakbd compile -emit-expectations` writes and the bundle manifest, drives a Prober (the
// Windows API on a guest, an emulator in the tests) through every line, and reports the differences. Nothing here
// calls Windows; the system calls are in cmd/yakbd-wincheck.
package wincheck

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Press is a virtual key with the modifier keys held.
type Press struct {
	VK   byte
	Mods []byte
}

func (p Press) String() string {
	s := fmt.Sprintf("%02x", p.VK)
	for _, m := range p.Mods {
		s += fmt.Sprintf("+%02x", m)
	}
	return s
}

// Line is one expectation: a key press, or a dead key followed by a key press, or a typed sequence (Seq: every
// press typed for real, the units of all of them together are the result), and the expected result.
type Line struct {
	No    int
	Dead  *Press
	Key   Press
	Seq   []Press
	Kind  string // chars, dead, chain or none
	Units []uint16
}

func (l Line) String() string {
	s := "key " + l.Key.String()
	if l.Dead != nil {
		s = "seq " + l.Dead.String() + " " + l.Key.String()
	}
	if l.Seq != nil {
		s = "type"
		for _, p := range l.Seq {
			s += " " + p.String()
		}
	}
	return s + " -> " + result(l.Kind, l.Units)
}

func result(kind string, units []uint16) string {
	s := kind
	for _, u := range units {
		s += fmt.Sprintf(" %04x", u)
	}
	return s
}

func parsePress(s string) (Press, error) {
	var p Press
	for i, part := range strings.Split(s, "+") {
		v, err := strconv.ParseUint(part, 16, 8)
		if err != nil {
			return p, fmt.Errorf("press %q", s)
		}
		if i == 0 {
			p.VK = byte(v)
		} else {
			p.Mods = append(p.Mods, byte(v))
		}
	}
	return p, nil
}

// ParseExpectations reads the expectations format of target/windows.FormatExpectations.
func ParseExpectations(r io.Reader) ([]Line, error) {
	var out []Line
	sc := bufio.NewScanner(r)
	no := 0
	for sc.Scan() {
		no++
		t := strings.TrimSpace(sc.Text())
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		lhs, rhs, ok := strings.Cut(t, " -> ")
		if !ok {
			return nil, fmt.Errorf("line %d: no ->", no)
		}
		f := strings.Fields(lhs)
		l := Line{No: no}
		var err error
		switch {
		case len(f) == 2 && f[0] == "key":
			l.Key, err = parsePress(f[1])
		case len(f) >= 2 && f[0] == "type":
			for _, s := range f[1:] {
				var p Press
				if p, err = parsePress(s); err != nil {
					break
				}
				l.Seq = append(l.Seq, p)
			}
		case len(f) == 3 && f[0] == "seq":
			var d Press
			d, err = parsePress(f[1])
			l.Dead = &d
			if err == nil {
				l.Key, err = parsePress(f[2])
			}
		default:
			err = fmt.Errorf("want key or seq")
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", no, err)
		}
		r := strings.Fields(rhs)
		if len(r) == 0 {
			return nil, fmt.Errorf("line %d: no result", no)
		}
		l.Kind = r[0]
		switch l.Kind {
		case "chars", "dead", "chain", "none":
		default:
			return nil, fmt.Errorf("line %d: result %q", no, l.Kind)
		}
		for _, u := range r[1:] {
			v, err := strconv.ParseUint(u, 16, 16)
			if err != nil {
				return nil, fmt.Errorf("line %d: unit %q", no, u)
			}
			l.Units = append(l.Units, uint16(v))
		}
		out = append(out, l)
	}
	return out, sc.Err()
}

// Peek is the ToUnicodeEx flag that reads the dead-key state without changing it (windows-behaviour §6.1).
const Peek = 4

// Prober translates key presses with a loaded layout, as ToUnicodeEx does: rc < 0 dead key (units[0] the dead
// character, or nothing for a chained step), 0 nothing, > 0 the number of units.
type Prober interface {
	ToUnicode(p Press, flags uint32) (rc int, units []uint16)
}

// Diff is one line whose result differs.
type Diff struct {
	Line Line
	Got  string
}

func (d Diff) String() string {
	return fmt.Sprintf("line %d: %s, got %s", d.Line.No, d.Line, d.Got)
}

// Check runs every line through p and returns the differences. Single keys are translated with Peek, so they
// never change the dead-key state; a sequence is typed for real and the state is cleared afterwards. A typed
// sequence (Seq) is typed for real and its units are concatenated; the dead-key state is cleared before and after.
func Check(lines []Line, p Prober) []Diff {
	var diffs []Diff
	reset := resetKey(lines)
	clear := func() {
		for i := 0; i < 4 && reset != nil; i++ {
			if rc, _ := p.ToUnicode(*reset, 0); rc >= 0 {
				return
			}
		}
	}
	clear()
	for _, l := range lines {
		if l.Seq != nil {
			clear()
			var all []uint16
			for _, k := range l.Seq {
				if rc, units := p.ToUnicode(k, 0); rc > 0 {
					all = append(all, units[:min(rc, len(units))]...)
				}
			}
			got := result("none", nil)
			if len(all) > 0 {
				got = result("chars", all)
			}
			if got != result(l.Kind, l.Units) {
				diffs = append(diffs, Diff{l, got})
			}
			clear()
			continue
		}
		if l.Dead == nil {
			rc, units := p.ToUnicode(l.Key, Peek)
			if got := describe(rc, units); got != result(l.Kind, l.Units) {
				diffs = append(diffs, Diff{l, got})
			}
			continue
		}
		rc, units := p.ToUnicode(*l.Dead, 0)
		if rc >= 0 {
			diffs = append(diffs, Diff{l, "the first key is not dead (" + describe(rc, units) + ")"})
			clear()
			continue
		}
		rc, units = p.ToUnicode(l.Key, 0)
		got := describe(rc, units)
		if l.Kind == "chain" && rc < 0 {
			// A chained step writes nothing (windows-behaviour §6.2): the new dead character is not readable here.
			got = result("chain", l.Units)
		}
		if got != result(l.Kind, l.Units) {
			diffs = append(diffs, Diff{l, got})
		}
		clear()
	}
	return diffs
}

func describe(rc int, units []uint16) string {
	switch {
	case rc < 0:
		if len(units) > 0 {
			return result("dead", units[:1])
		}
		return "chain"
	case rc == 0:
		return "none"
	}
	if rc > len(units) {
		rc = len(units)
	}
	return result("chars", units[:rc])
}

// resetKey is a key without modifiers that types one plain unit: pressing it clears a pending dead key.
func resetKey(lines []Line) *Press {
	for _, l := range lines {
		if l.Seq == nil && l.Dead == nil && len(l.Key.Mods) == 0 && l.Kind == "chars" && len(l.Units) == 1 {
			k := l.Key
			return &k
		}
	}
	return nil
}
