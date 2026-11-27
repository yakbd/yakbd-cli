package build

import (
	"fmt"
	"reflect"

	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// deadKeys writes the DEADKEY table (§8): entries per dead key in file order, dead keys by dead character.
func (b *lowerer) deadKeys() {
	ids := sortedKeys(b.l.DeadKeys)
	// order by dead character, for a stable table
	sortByDead := func(a, c string) bool { return b.dead[a] < b.dead[c] }
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && sortByDead(ids[j], ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	have := map[uint32]uint16{}
	starts := b.cellStarts()
	for _, id := range ids {
		dk := b.l.DeadKeys[id]
		d, ok := b.dead[id]
		if !ok {
			continue
		}
		path := "/deadKeys/" + model.Escape(id)
		for i, e := range dk.Entries {
			ep := fmt.Sprintf("%s/entries/%d", path, i)
			if b.norm {
				e.Base = e.Base.NFC() // an input: matched as NFC, as the key outputs are
				if r := e.Result.NFC(); r.Text != e.Result.Text {
					b.normalized(ep+"/result", utf16Of(e.Result.Text), utf16Of(r.Text))
					e.Result = r
				}
			}
			var base []uint16
			if e.Base.Kind == model.ValueDeadKey {
				c, ok := b.dead[e.Base.DeadKey]
				if !ok {
					b.lost("general.unresolvedReference", ep, "dead key %q is not defined", e.Base.DeadKey)
					continue
				}
				base = []uint16{c}
			} else {
				base = e.Base.UTF16()
			}
			var res []uint16
			flags := uint16(0)
			if e.Result.Kind == model.ValueDeadKey {
				c, ok := b.dead[e.Result.DeadKey]
				if !ok {
					b.lost("general.unresolvedReference", ep, "dead key %q is not defined", e.Result.DeadKey)
					continue
				}
				res, flags = []uint16{c}, windows.DKF_DEAD
			} else {
				res = e.Result.UTF16()
			}
			var entry windows.DeadKey
			switch {
			case len(base) == 1 && len(res) == 1:
				entry = windows.DeadKey{Dead: d, Base: base[0], Composed: res[0], Flags: flags}
			case len(base) >= 1 && flags == 0 && dk.NoMatch != "emitText" && dk.NoMatch != "emitSpacing" && dk.NoMatch != "emitNothing" &&
				reflect.DeepEqual(res, append(b.spacingUnits(dk), base...)):
				continue // emitBoth already gives the spacing and the base
			case len(base) >= 2 && len(res) == len(base) && reflect.DeepEqual(res[1:], base[1:]):
				entry = windows.DeadKey{Dead: d, Base: base[0], Composed: res[0]} // the multi-unit recipe (spec-v9)
			case len(base) >= 2 || len(base) == 0:
				b.lost("windows.deadKeyBaseUnencodable", ep, "the base cannot be encoded")
				continue
			default:
				b.lost("windows.deadKeyResultUnencodable", ep, "the result cannot be encoded")
				continue
			}
			k := uint32(entry.Dead)<<16 | uint32(entry.Base)
			if prev, ok := have[k]; ok {
				switch {
				case len(base) == 1:
					b.lost("general.duplicateDeadKeyEntry", ep, "an earlier entry has the same dead key and base; the first wins")
				case prev != entry.Composed:
					b.lost("windows.deadKeyBaseUnencodable", ep, "the entry needs (%04X, %04X) -> %04X, but an earlier one gives %04X; the first wins", entry.Dead, entry.Base, entry.Composed, prev)
				}
				continue
			}
			have[k] = entry.Composed
			b.t.DeadKeys = append(b.t.DeadKeys, entry)
		}
		// Windows composes on the first unit of a multi-unit output (§8).
		for first, rest := range starts {
			if _, ok := have[uint32(d)<<16|uint32(first)]; rest && ok && dk.NoMatch != "" && dk.NoMatch != "emitBoth" {
				b.lost("windows.deadKeyFirstUnitComposes", path, "an output that starts with %04X composes its first unit", first)
			}
		}
		switch dk.NoMatch {
		case "emitText":
			n := 0
			for _, u := range b.allUnits() {
				k := uint32(d)<<16 | uint32(u)
				if _, ok := have[k]; !ok {
					have[k] = u
					b.t.DeadKeys = append(b.t.DeadKeys, windows.DeadKey{Dead: d, Base: u, Composed: u})
					n++
				}
			}
			b.lost("windows.deadKeyNoMatchExpanded", path, "noMatch emitText compiled into %d entries", n)
		case "emitSpacing", "emitNothing":
			b.lost("windows.deadKeyNoMatchNotExpressible", path, "noMatch %s cannot be expressed", dk.NoMatch)
		}
		if dk.Name != "" {
			b.t.KeyNamesDead = append(b.t.KeyNamesDead, windows.DeadName{Dead: d, Name: dk.Name})
		}
	}
}

// spacingUnits is the spacing of dk as the build emits it.
func (b *lowerer) spacingUnits(dk *model.DeadKey) []uint16 {
	if b.norm {
		return dk.Spacing.NFC().UTF16()
	}
	return dk.Spacing.UTF16()
}

// cellStarts returns the first units of cells, true when the cell has more units.
func (b *lowerer) cellStarts() map[uint16]bool {
	out := map[uint16]bool{}
	for _, ct := range b.t.CharTables {
		for _, r := range ct.Rows {
			for _, c := range r.Chars {
				if c < 0xF000 || c > 0xF002 {
					out[c] = out[c] || false
				}
			}
		}
	}
	for _, l := range b.t.Ligatures {
		if len(l.Chars) > 1 {
			out[l.Chars[0]] = true
		}
	}
	return out
}

// allUnits lists every one-unit cell, first ligature unit and dead character of the tables, in table order.
func (b *lowerer) allUnits() []uint16 {
	seen := map[uint16]bool{}
	var out []uint16
	add := func(u uint16) {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, ct := range b.t.CharTables {
		for _, r := range ct.Rows {
			for _, c := range r.Chars {
				if c != windows.WCH_NONE && c != windows.WCH_DEAD && c != windows.WCH_LGTR {
					add(c)
				}
			}
		}
	}
	for _, l := range b.t.Ligatures {
		add(l.Chars[0])
	}
	return out
}
