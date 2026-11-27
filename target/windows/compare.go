package windows

import (
	"fmt"
	"reflect"
	"sort"
)

// Canonical returns a copy of t without what carries no meaning for Windows, so that two files that Windows
// reads the same way compare equal (docs/dll-import.md, "Same tables"):
//
//   - rows Windows never reaches: a second row for a VK, an FF row that follows no dead key, empty tables, cells of a
//     dead-character row outside the dead columns, columns beyond 15;
//   - the order of the E0 and E1 entries, of the key names, of the dead-key names and of the ligatures; the
//     order of the DEADKEY entries of different dead characters (lookups take the first match of the same key);
//   - entries after the first for the same key (scan code, dead character and base, VK and column); E0/E1 entries
//     with VK FF and no flags; empty key names;
//   - ligatures no cell refers to, nLgMax, and a one-unit ligature that is an ordinary character;
//   - DEADKEY flag bits other than DKF_DEAD (Windows ignores them, dll-format §4.3).
func Canonical(t *Tables) *Tables {
	c := &Tables{Modifiers: Modifiers{VKToBit: append([]VKBit(nil), t.Modifiers.VKToBit...),
		ModNumber: append([]byte(nil), t.Modifiers.ModNumber...)},
		ScanToVK: append([]uint16(nil), t.ScanToVK...), LocaleFlags: t.LocaleFlags, Version: t.Version,
		Type: t.Type, SubType: t.SubType}
	// A state with a bit no VK_TO_BIT entry sets is never made (OBS D12: kbdutool writes such states), and states past
	// the last valid one are SHFT_INVALID anyway; a column no state reaches is never read: its cells carry nothing.
	var allBits byte
	for _, vb := range c.Modifiers.VKToBit {
		allBits |= vb.Bits
	}
	reach := 0
	for i, col := range c.Modifiers.ModNumber {
		if col != SHFT_INVALID && byte(i)&^allBits != 0 {
			c.Modifiers.ModNumber[i] = SHFT_INVALID
		}
		if col := c.Modifiers.ModNumber[i]; col != SHFT_INVALID && int(col)+1 > reach {
			reach = int(col) + 1
		}
	}
	for n := len(c.Modifiers.ModNumber); n > 1 && c.Modifiers.ModNumber[n-1] == SHFT_INVALID; n-- {
		c.Modifiers.ModNumber = c.Modifiers.ModNumber[:n-1]
	}
	reached := map[int]bool{}
	for _, col := range c.Modifiers.ModNumber {
		reached[int(col)] = true
	}
	lig := map[[2]int][]uint16{}
	for _, l := range t.Ligatures {
		k := [2]int{int(l.VK), int(l.Column)}
		if _, ok := lig[k]; !ok {
			lig[k] = l.Chars
		}
	}
	usedLig := map[[2]int]bool{}
	seenVK := map[byte]bool{}
	for _, ct := range t.CharTables {
		w := max(1, min(ct.Width, MaxColumns, reach))
		out := CharTable{Width: w}
		for k := 0; k < len(ct.Rows); k++ {
			r := ct.Rows[k]
			var next *CharRow
			hasDead := false
			for _, u := range r.Chars {
				hasDead = hasDead || u == WCH_DEAD
			}
			if (hasDead || r.Attributes&SGCAPS != 0) && k+1 < len(ct.Rows) {
				n := ct.Rows[k+1]
				next = &n
				k++
			}
			// W-2 (D63): the dead characters of a Caps Lock row with dead keys are in the row after it.
			var capsDead *CharRow
			if !hasDead && r.Attributes&SGCAPS != 0 && next != nil && k+1 < len(ct.Rows) {
				for _, u := range next.Chars {
					if u == WCH_DEAD && capsDead == nil {
						n := ct.Rows[k+1]
						capsDead = &n
						k++
					}
				}
			}
			if r.VK == 0xFF || seenVK[r.VK] {
				continue
			}
			seenVK[r.VK] = true
			main := CharRow{VK: r.VK, Attributes: r.Attributes, Chars: append([]uint16(nil), r.Chars[:w]...)}
			for col := range main.Chars {
				if !reached[col] {
					main.Chars[col] = WCH_NONE
				}
			}
			for col, u := range main.Chars {
				if u != WCH_LGTR {
					continue
				}
				k := [2]int{int(r.VK), col}
				chars, ok := lig[k]
				switch {
				case !ok || len(chars) == 0:
					main.Chars[col] = WCH_NONE
				case len(chars) == 1 && chars[0] != WCH_DEAD && chars[0] != WCH_LGTR && chars[0] != WCH_NONE:
					main.Chars[col] = chars[0]
				default:
					usedLig[k] = true
				}
			}
			out.Rows = append(out.Rows, main)
			if next != nil {
				n := CharRow{VK: next.VK, Attributes: next.Attributes, Chars: append([]uint16(nil), next.Chars[:w]...)}
				for col := range n.Chars {
					if !(main.Chars[col] == WCH_DEAD || r.Attributes&SGCAPS != 0 && col < 2) { // D65: Caps Lock cells above column 1 are never read
						n.Chars[col] = WCH_NONE
					}
				}
				out.Rows = append(out.Rows, n)
				if capsDead != nil {
					cd := CharRow{VK: capsDead.VK, Attributes: capsDead.Attributes, Chars: append([]uint16(nil), capsDead.Chars[:w]...)}
					for col := range cd.Chars {
						if n.Chars[col] != WCH_DEAD {
							cd.Chars[col] = WCH_NONE
						}
					}
					out.Rows = append(out.Rows, cd)
				}
			}
		}
		if len(out.Rows) > 0 {
			c.CharTables = append(c.CharTables, out)
		}
	}
	for k, chars := range lig {
		if usedLig[k] {
			c.Ligatures = append(c.Ligatures, Ligature{VK: byte(k[0]), Column: uint16(k[1]), Chars: chars})
		}
	}
	sort.Slice(c.Ligatures, func(i, j int) bool {
		a, b := c.Ligatures[i], c.Ligatures[j]
		if a.VK != b.VK {
			return a.VK < b.VK
		}
		return a.Column < b.Column
	})
	seenDK := map[uint32]bool{}
	for _, d := range t.DeadKeys {
		k := uint32(d.Dead)<<16 | uint32(d.Base)
		if seenDK[k] {
			continue
		}
		seenDK[k] = true
		d.Flags &= DKF_DEAD
		c.DeadKeys = append(c.DeadKeys, d)
	}
	sort.SliceStable(c.DeadKeys, func(i, j int) bool { return c.DeadKeys[i].Dead < c.DeadKeys[j].Dead })
	c.KeyNames = canonNames(t.KeyNames)
	c.KeyNamesExt = canonNames(t.KeyNamesExt)
	seenDead := map[uint16]bool{}
	for _, n := range t.KeyNamesDead {
		if seenDead[n.Dead] || n.Name == "" {
			seenDead[n.Dead] = true
			continue
		}
		seenDead[n.Dead] = true
		c.KeyNamesDead = append(c.KeyNamesDead, n)
	}
	sort.SliceStable(c.KeyNamesDead, func(i, j int) bool { return c.KeyNamesDead[i].Dead < c.KeyNamesDead[j].Dead })
	c.E0 = canonScan(t.E0)
	c.E1 = canonScan(t.E1)
	return c
}

func canonNames(in []KeyName) []KeyName {
	var out []KeyName
	seen := map[byte]bool{}
	for _, n := range in {
		if seen[n.Scan] || n.Name == "" {
			seen[n.Scan] = true
			continue
		}
		seen[n.Scan] = true
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Scan < out[j].Scan })
	return out
}

func canonScan(in []ScanVK) []ScanVK {
	var out []ScanVK
	seen := map[byte]bool{}
	for _, e := range in {
		if seen[e.Scan] {
			continue
		}
		seen[e.Scan] = true
		if e.VK == 0x00FF {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Scan < out[j].Scan })
	return out
}

// Differences compares two tables after Canonical and describes every field that differs (nil when Windows
// reads both the same way, as far as the tables say).
func Differences(a, b *Tables) []string {
	x, y := Canonical(a), Canonical(b)
	var out []string
	diff := func(name string, p, q any) {
		if !reflect.DeepEqual(p, q) {
			out = append(out, name)
		}
	}
	diff("VK_TO_BIT", x.Modifiers.VKToBit, y.Modifiers.VKToBit)
	diff("ModNumber", x.Modifiers.ModNumber, y.Modifiers.ModNumber)
	if len(x.CharTables) != len(y.CharTables) {
		out = append(out, fmt.Sprintf("character tables: %d against %d", len(x.CharTables), len(y.CharTables)))
	} else {
		for i := range x.CharTables {
			p, q := x.CharTables[i], y.CharTables[i]
			if p.Width != q.Width || len(p.Rows) != len(q.Rows) {
				out = append(out, fmt.Sprintf("table %d: width %d with %d rows against width %d with %d rows", i, p.Width, len(p.Rows), q.Width, len(q.Rows)))
				continue
			}
			for k := range p.Rows {
				diff(fmt.Sprintf("table %d row %d", i, k), p.Rows[k], q.Rows[k])
			}
		}
	}
	diff("DEADKEY", x.DeadKeys, y.DeadKeys)
	diff("key names", x.KeyNames, y.KeyNames)
	diff("extended key names", x.KeyNamesExt, y.KeyNamesExt)
	diff("dead-key names", x.KeyNamesDead, y.KeyNamesDead)
	diff("pusVSCtoVK", x.ScanToVK, y.ScanToVK)
	diff("E0 table", x.E0, y.E0)
	diff("E1 table", x.E1, y.E1)
	diff("locale flags", x.LocaleFlags, y.LocaleFlags)
	diff("version word", x.Version, y.Version)
	diff("keyboard type", [2]uint32{x.Type, x.SubType}, [2]uint32{y.Type, y.SubType})
	diff("ligatures", x.Ligatures, y.Ligatures)
	return out
}
