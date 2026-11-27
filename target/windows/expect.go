package windows

import (
	"fmt"
	"sort"
	"strings"
)

// Expectation is one key press (or a dead key followed by a key) and what ToUnicodeEx should return for it on
// a layout built from the tables, with Caps Lock and Num Lock off. A guest script loads each built DLL, presses
// the keys in 64-bit and 32-bit processes and compares (see ARCHITECTURE.md, "Expectations").
type Expectation struct {
	// Dead is the dead key pressed first, if any.
	Dead *Press
	Key  Press
	// Kind is "chars" (Units are typed), "dead" (the key is a dead key; ToUnicodeEx returns -1 with Units[0]),
	// "chain" (a chained dead-key step: ToUnicodeEx returns -1 and writes nothing; Units[0] is the new pending
	// dead character, to be checked by a following key) or "none".
	Kind  string
	Units []uint16
}

// Press is one key press: a virtual key and the modifier keys held, as virtual keys. Two values have a meaning of
// their own: VKRShift is the right Shift key alone (Shift, 10, is the left one), and VKCapsLock means Caps Lock is
// on (toggled), not held.
type Press struct {
	VK   byte
	Mods []byte
}

// Modifier values of a Press with a meaning of their own.
const (
	VKRShift   = 0xA1 // VK_RSHIFT: the right Shift key down (and the generic Shift state)
	VKCapsLock = 0x14 // VK_CAPITAL: Caps Lock toggled on
)

// stateFor returns the lowest modifier-bit combination whose ModNumber is col, and the modifier keys that make
// it, or false. Combinations with Alt and without Ctrl are skipped: Windows never reaches them
// (windows-behaviour §4.2).
func stateFor(m Modifiers, col int) ([]byte, bool) {
	for b, c := range m.ModNumber {
		if int(c) != col || c == SHFT_INVALID {
			continue
		}
		if b&KBDALT != 0 && b&KBDCTRL == 0 {
			continue
		}
		var mods []byte
		rest := byte(b)
		for _, vb := range m.VKToBit {
			if vb.Bits&rest != 0 && vb.Bits&^byte(b) == 0 {
				mods = append(mods, vb.VK)
				rest &^= vb.Bits
			}
		}
		if rest == 0 {
			return mods, true
		}
	}
	return nil, false
}

// Expectations derives the expected results of every reachable cell of the tables and of every dead-key entry
// whose dead character and base character are each typed by some key.
func (t *Tables) Expectations() []Expectation {
	var out []Expectation
	typer := map[uint16]Press{}      // character -> first key that types it alone
	ligRest := map[uint16][]uint16{} // first unit of a ligature -> the units after it, for keys only in typer via a ligature
	deadKey := map[uint16]Press{}
	seenVK := map[byte]bool{}
	for _, ct := range t.CharTables {
		for i, r := range ct.Rows {
			if r.VK == 0xFF || seenVK[r.VK] {
				continue // dead-character rows and SGCAPS second rows; first row of a key wins
			}
			seenVK[r.VK] = true
			for col, c := range r.Chars {
				mods, ok := stateFor(t.Modifiers, col)
				if !ok {
					continue
				}
				p := Press{VK: r.VK, Mods: mods}
				e := Expectation{Key: p}
				if r.VK == 0x08 && t.LocaleFlags&KLLF_LRM_RLM != 0 && hasShift(mods) {
					// KLLF_LRM_RLM: Backspace with left Shift down gives U+200E whatever else is down (windows-behaviour
					// §11; seen again in VM run 1). The runner holds the left Shift key.
					e.Kind, e.Units = "chars", []uint16{0x200E}
					out = append(out, e)
					continue
				}
				switch c {
				case WCH_NONE:
					if hasCtrl(t.Modifiers, mods) {
						continue // Windows may fall back to a control character (windows-behaviour §6.2)
					}
					e.Kind = "none"
				case WCH_DEAD:
					e.Kind = "dead"
					if i+1 < len(ct.Rows) {
						d := ct.Rows[i+1].Chars[col]
						e.Units = []uint16{d}
						if _, ok := deadKey[d]; !ok {
							deadKey[d] = p
						}
					}
				case WCH_LGTR:
					e.Kind = "chars"
					for _, l := range t.Ligatures {
						if l.VK == r.VK && int(l.Column) == col {
							e.Units = l.Chars
							break
						}
					}
					if len(e.Units) == 0 {
						e.Kind = "none"
					} else if _, ok := typer[e.Units[0]]; !ok {
						typer[e.Units[0]] = p
						ligRest[e.Units[0]] = e.Units[1:]
					}
				default:
					e.Kind = "chars"
					e.Units = []uint16{c}
					if _, ok := typer[c]; !ok {
						typer[c] = p
					}
				}
				out = append(out, e)
			}
		}
	}
	if seenVK[0x08] && t.LocaleFlags&KLLF_LRM_RLM != 0 {
		// The right Shift key (VK_RSHIFT, A1) gives U+200F; with both Shift keys down the left one wins
		// (windows-behaviour §11). Only the left-Shift case was seen on Windows so far (VM run 1).
		out = append(out,
			Expectation{Key: Press{VK: 0x08, Mods: []byte{VKRShift}}, Kind: "chars", Units: []uint16{0x200F}},
			Expectation{Key: Press{VK: 0x08, Mods: []byte{0x10, VKRShift}}, Kind: "chars", Units: []uint16{0x200E}})
	}
	for _, d := range t.DeadKeys {
		dp, ok1 := deadKey[d.Dead]
		bp, ok2 := typer[d.Base]
		if !ok2 {
			// A dead key pressed as the second key meets the entry with its own dead character
			// (windows-behaviour §6.2).
			bp, ok2 = deadKey[d.Base]
		}
		if !ok1 || !ok2 {
			continue
		}
		dpc := dp
		e := Expectation{Dead: &dpc, Key: bp, Kind: "chars", Units: append([]uint16{d.Composed}, ligRest[d.Base]...)}
		if d.Flags&DKF_DEAD != 0 {
			e.Kind = "chain"
			e.Units = []uint16{d.Composed}
		}
		out = append(out, e)
	}
	return out
}

// FormatExpectations renders expectations as the line format of the guest script:
//
//	key <press> -> <result>
//	seq <press> <press> -> <result>
//
// where a press is VK[+MOD...] in hex and a result is "chars u u ...", "dead u" or "none".
func FormatExpectations(name string, es []Expectation) []byte {
	press := func(p Press) string {
		s := fmt.Sprintf("%02x", p.VK)
		mods := append([]byte(nil), p.Mods...)
		sort.Slice(mods, func(i, j int) bool { return mods[i] < mods[j] })
		for _, m := range mods {
			s += fmt.Sprintf("+%02x", m)
		}
		return s
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# yakbd expectations 1: %s\n# Caps Lock and Num Lock off; ToUnicodeEx results; units in hex; Shift (10) means the left Shift key, a1 the right one alone, 14 Caps Lock on\n", name)
	for _, e := range es {
		res := e.Kind
		for _, u := range e.Units {
			res += fmt.Sprintf(" %04x", u)
		}
		if e.Dead != nil {
			fmt.Fprintf(&b, "seq %s %s -> %s\n", press(*e.Dead), press(e.Key), res)
		} else {
			fmt.Fprintf(&b, "key %s -> %s\n", press(e.Key), res)
		}
	}
	return []byte(b.String())
}

func hasCtrl(m Modifiers, mods []byte) bool {
	for _, v := range mods {
		for _, vb := range m.VKToBit {
			if vb.VK == v && vb.Bits&KBDCTRL != 0 {
				return true
			}
		}
	}
	return false
}

func hasShift(mods []byte) bool {
	for _, m := range mods {
		if m == 0x10 {
			return true
		}
	}
	return false
}
