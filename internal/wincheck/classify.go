package wincheck

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/target/windows"
)

// Classify names the mechanisms behind differing probes, from the tables of the original (o) and the rebuilt
// file (r): which modifier column, Caps Lock, row attributes, the kind of cell (ligature, dead key, empty,
// character), whether the rows of the two files differ, dead-key entries (chain, fallback without entry,
// multi-unit base), the locale flags. It never names a character or a key: the result is a count per mechanism
// tag, so that it can leave the guest (docs/dll-import.md §5).
func Classify(o, r *windows.Tables, extra []byte, samples []Probe) map[string]int {
	states := ProbeStates(extra)
	out := map[string]int{}
	add := func(tags []string) {
		for _, t := range tags {
			out[t]++
		}
	}
	for _, p := range samples {
		switch {
		case p.Scan != 0:
			add([]string{"scan code mapping"})
		case p.Seq:
			if p.DeadState >= len(states) || p.State >= len(states) {
				continue
			}
			tags := []string{"sequence"}
			for _, t := range keyTags(o, r, p.DeadVK, states[p.DeadState]) {
				tags = append(tags, "dead key: "+t)
			}
			for _, t := range keyTags(o, r, p.VK, states[p.State]) {
				tags = append(tags, "next key: "+t)
			}
			tags = append(tags, entryTags(o, r, p, states)...)
			add(tags)
		default:
			if p.State < len(states) {
				add(append([]string{"single key"}, keyTags(o, r, p.VK, states[p.State])...))
			}
		}
	}
	if o.LocaleFlags != r.LocaleFlags {
		out["file: locale flags (KLLF) differ"]++
	}
	if !reflect.DeepEqual(o.Modifiers, r.Modifiers) {
		out["file: modifier tables differ"]++
	}
	return out
}

// FormatTags writes a tag count as one line, sorted.
func FormatTags(m map[string]int) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, "; ")
}

// column returns the modifier column of a probe state in t (-1: none).
func column(t *windows.Tables, st *[256]byte) int {
	bits := 0
	for _, e := range t.Modifiers.VKToBit {
		if st[e.VK]&0x80 != 0 {
			bits |= int(e.Bits)
		}
	}
	if st[vkRMenu]&0x80 != 0 && t.LocaleFlags&windows.KLLF_ALTGR != 0 {
		bits |= windows.KBDCTRL // right Alt is Ctrl+Alt with KLLF_ALTGR
	}
	if bits >= len(t.Modifiers.ModNumber) || t.Modifiers.ModNumber[bits] == windows.SHFT_INVALID {
		return -1
	}
	return int(t.Modifiers.ModNumber[bits])
}

// rowOf returns the first row of a VK and the row Windows reads after it.
func rowOf(t *windows.Tables, v byte) (*windows.CharRow, *windows.CharRow) {
	for ti := range t.CharTables {
		rows := t.CharTables[ti].Rows
		for k := range rows {
			if rows[k].VK == v {
				var next *windows.CharRow
				if k+1 < len(rows) {
					next = &rows[k+1]
				}
				return &rows[k], next
			}
		}
	}
	return nil, nil
}

func cellKind(c uint16) string {
	switch c {
	case windows.WCH_NONE:
		return "empty cell"
	case windows.WCH_DEAD:
		return "dead-key cell"
	case windows.WCH_LGTR:
		return "ligature cell"
	}
	return "character cell"
}

func keyTags(o, r *windows.Tables, v byte, st *[256]byte) []string {
	var tags []string
	caps := st[vkCapital]&1 != 0
	if caps {
		tags = append(tags, "Caps Lock on")
	}
	if st[vkKana]&1 != 0 {
		tags = append(tags, "Kana toggled")
	}
	col := column(o, st)
	if col < 0 {
		tags = append(tags, "no column")
	} else {
		tags = append(tags, fmt.Sprintf("column %d", col))
	}
	if column(r, st) != col {
		tags = append(tags, "column differs")
	}
	or, on := rowOf(o, v)
	rr, rn := rowOf(r, v)
	switch {
	case or == nil && rr == nil:
		return append(tags, "no row in either file")
	case or == nil:
		return append(tags, "row only in the rebuilt file")
	case rr == nil:
		return append(tags, "row only in the original")
	}
	for _, a := range []struct {
		bit  byte
		name string
	}{{windows.CAPLOK, "CAPLOK"}, {windows.SGCAPS, "SGCAPS"}, {windows.CAPLOKALTGR, "CAPLOKALTGR"}, {windows.KANALOK, "KANALOK"}} {
		if or.Attributes&a.bit != 0 {
			tags = append(tags, a.name)
		}
	}
	if or.Attributes != rr.Attributes {
		tags = append(tags, "attributes differ")
	}
	if col >= 0 && col < len(or.Chars) {
		tags = append(tags, cellKind(or.Chars[col]))
		if or.Attributes&windows.SGCAPS != 0 && caps && on != nil && col < 2 && col < len(on.Chars) {
			tags = append(tags, "SGCAPS Caps Lock row: "+cellKind(on.Chars[col]))
			if on.VK == 0xFF {
				tags = append(tags, "SGCAPS with dead key (shared FF row)")
			}
		}
	} else if col >= 0 {
		tags = append(tags, "column beyond the row")
	}
	if !reflect.DeepEqual(or.Chars, rr.Chars) {
		tags = append(tags, "main rows differ")
	}
	if (on == nil) != (rn == nil) || on != nil && (on.VK != rn.VK || !reflect.DeepEqual(on.Chars, rn.Chars)) {
		hasNext := or.Attributes&windows.SGCAPS != 0
		for _, c := range or.Chars {
			hasNext = hasNext || c == windows.WCH_DEAD
		}
		if hasNext {
			tags = append(tags, "following rows differ")
		}
	}
	return tags
}

// entryTags describes the DEADKEY entries a sequence uses.
func entryTags(o, r *windows.Tables, p Probe, states []*[256]byte) []string {
	dead := func(t *windows.Tables) (uint16, bool) {
		row, next := rowOf(t, p.DeadVK)
		c := column(t, states[p.DeadState])
		if row == nil || next == nil || c < 0 || c >= len(row.Chars) || c >= len(next.Chars) || row.Chars[c] != windows.WCH_DEAD {
			return 0, false
		}
		return next.Chars[c], true
	}
	base := func(t *windows.Tables) ([]uint16, bool) {
		row, _ := rowOf(t, p.VK)
		c := column(t, states[p.State])
		if row == nil || c < 0 || c >= len(row.Chars) {
			return nil, false
		}
		switch u := row.Chars[c]; u {
		case windows.WCH_NONE, windows.WCH_DEAD:
			return nil, false
		case windows.WCH_LGTR:
			for _, l := range t.Ligatures {
				if l.VK == p.VK && int(l.Column) == c {
					return l.Chars, true
				}
			}
			return nil, false
		default:
			return []uint16{u}, true
		}
	}
	var tags []string
	od, ok1 := dead(o)
	rd, ok2 := dead(r)
	if !ok1 || !ok2 {
		return append(tags, "dead key not dead in one file")
	}
	if od != rd {
		tags = append(tags, "dead characters differ")
	}
	ob, okb := base(o)
	if !okb {
		return append(tags, "next key has no text")
	}
	if len(ob) > 1 {
		tags = append(tags, "multi-unit next key")
	}
	find := func(t *windows.Tables, d, b uint16) *windows.DeadKey {
		for i := range t.DeadKeys {
			if t.DeadKeys[i].Dead == d && t.DeadKeys[i].Base == b {
				return &t.DeadKeys[i]
			}
		}
		return nil
	}
	oe, re := find(o, od, ob[0]), find(r, rd, ob[0])
	switch {
	case oe == nil && re == nil:
		tags = append(tags, "no entry in either file (fallback)")
	case oe == nil || re == nil:
		tags = append(tags, "entry in one file only")
	case oe.Composed != re.Composed || oe.Flags&windows.DKF_DEAD != re.Flags&windows.DKF_DEAD:
		tags = append(tags, "entries differ")
	default:
		tags = append(tags, "same entry")
	}
	if oe != nil && oe.Flags&windows.DKF_DEAD != 0 {
		tags = append(tags, "chain entry (DKF_DEAD)")
	}
	return tags
}
