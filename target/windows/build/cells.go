package build

import (
	"fmt"

	"github.com/yakbd/yakbd-cli/loss"
	"reflect"
	"sort"

	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// deadCharacters decides the dead character of every dead key (§8, rule deadCharacter).
func (b *lowerer) deadCharacters() {
	b.dead = map[string]uint16{}
	b.pin.DeadKeys = map[string]*model.WindowsDeadKey{}
	owner := map[uint16]string{}
	var defaulted []string
	for _, id := range sortedKeys(b.l.DeadKeys) {
		dk := b.l.DeadKeys[id]
		var units []uint16
		ch := dk.Spacing
		if w := b.file.DeadKeys[id]; w != nil && w.Character != nil {
			ch = *w.Character
		} else {
			defaulted = append(defaulted, id)
			ch = b.nfcText(ch, "/deadKeys/"+id+"/spacing")
		}
		units = ch.UTF16()
		c := ch
		b.pin.DeadKeys[id] = &model.WindowsDeadKey{Character: &c}
		if len(units) != 1 {
			b.lost("windows.deadCharacterInvalid", "/deadKeys/"+id, "dead key %s: the dead character must be one unit, it has %d", id, len(units))
			continue
		}
		if o, ok := owner[units[0]]; ok {
			b.lost("windows.deadCharacterCollision", "/deadKeys/"+id, "dead keys %s and %s have the same dead character %04X", o, id, units[0])
			continue
		}
		owner[units[0]] = id
		b.dead[id] = units[0]
	}
	if len(defaulted) > 0 {
		b.defaulted("deadCharacter", defaulted, "dead characters from spacing for %d dead key(s)", len(defaulted))
	}
}

// cell is one character cell before encoding.
type cell struct {
	units []uint16 // nil = nothing
	dead  string   // dead-key id
	// none marks an explicit nothing ({"output": ""}, D64) where a fallback would give something
	none bool
}

func (c cell) empty() bool { return len(c.units) == 0 && c.dead == "" }

func (b *lowerer) cellOf(o model.Output, path string) cell {
	if o.Unresolved != "" {
		b.lost("general.unresolvedReference", path, "%q is not defined", o.Unresolved)
	}
	if o.Markers {
		b.lost("windows.markerNotCompiled", path, "markers are dropped from the output")
	}
	if o.DeadKey != "" {
		if _, ok := b.l.DeadKeys[o.DeadKey]; !ok {
			b.lost("general.unresolvedReference", path, "dead key %q is not defined", o.DeadKey)
			return cell{}
		}
		return cell{dead: o.DeadKey}
	}
	return cell{units: b.nfc(o, path).Units}
}

// row is a character row being built for one VK.
type row struct {
	vk    byte
	scan  uint16
	pos   string
	cells []cell
	attrs byte
	caps  []cell // SGCAPS Caps Lock row (columns 0 and 1)
	extra bool
}

// rows builds the character rows of every position (§6) and the tables (§7).
func (b *lowerer) rows() {
	var rows []*row
	rowOfVK := map[byte]*row{}
	var ctrlLetter []string
	cols := len(b.states)
	for _, pi := range b.pos {
		if pi.fromTable || b.isModifierPosition(pi.id) || !b.bound(pi.id) {
			if isFrame(pi.vk) && !b.bound(pi.id) && !b.vkBound(pi.vk) {
				b.lost("windows.frameKeyWithoutText", "/positions/"+pi.id, "%s (%s) has no base-layer text", pi.id, vk.Name(pi.vk))
			}
			continue
		}
		r := &row{vk: pi.vk, scan: pi.scan, pos: pi.id, cells: make([]cell, cols)}
		if pi.numLock {
			on, ok := numpadOn[pi.vk]
			if !ok {
				continue
			}
			r.vk = on
		}
		path := "/hardware/layers"
		for c := 0; c < cols; c++ {
			bnd := b.l.Resolve(pi.id, b.reps[c])
			r.cells[c] = b.cellOf(b.l.Effective(bnd), path+" ("+pi.id+")")
			if pi.numLock && b.states[c]&1 != 0 && !r.cells[c].empty() {
				b.lost("windows.numpadShiftUnreachable", "/positions/"+pi.id, "%s has outputs with Shift; Windows turns Shift+number pad into navigation", pi.id)
			}
		}
		b.checkSameState(pi.id)
		if pi.vk == 0x08 && b.t.LocaleFlags&windows.KLLF_LRM_RLM != 0 {
			for c, s := range b.states {
				if cl := r.cells[c]; s&1 != 0 && !cl.empty() && !(len(cl.units) == 1 && (cl.units[0] == 0x200E || cl.units[0] == 0x200F)) {
					b.lost("windows.bidiMarksConflict", "/positions/"+pi.id, "%s: with KLLF_LRM_RLM, Shift+Backspace gives U+200E/U+200F, not the output of state %d", pi.id, s)
					break
				}
			}
		}
		b.attributes(pi, r)
		if pi.vk >= 'A' && pi.vk <= 'Z' {
			if c, ok := b.colOf[2]; !ok || r.cells[c].empty() {
				ctrlLetter = append(ctrlLetter, pi.id)
			}
		}
		if isFrame(pi.vk) && (cols == 0 || r.cells[0].empty()) {
			b.lost("windows.frameKeyWithoutText", "/positions/"+pi.id, "%s (%s) has no base-layer text", pi.id, vk.Name(pi.vk))
		}
		if prev, ok := rowOfVK[r.vk]; ok {
			if !reflect.DeepEqual(prev.cells, r.cells) {
				b.lost("windows.sharedVkRowConflict", "/positions/"+pi.id, "%s and %s share %s; the row of %s is used", prev.pos, pi.id, vk.Name(r.vk), prev.pos)
			}
			continue
		}
		rowOfVK[r.vk] = r
		rows = append(rows, r)
	}
	if len(b.attrDefaulted) > 0 {
		b.defaulted("attributes", b.attrDefaulted, "Caps Lock attributes from the layers for %d position(s)", len(b.attrDefaulted))
	}
	if len(b.capsUnmatched) > 0 {
		b.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Warning, Code: "windows.capsUnmatched", Where: loss.Where{Path: "/hardware/layers"},
			Message: fmt.Sprintf("%d key(s) give nothing with Caps Lock where Windows types the output without it", len(b.capsUnmatched)),
			Detail:  map[string]any{"positions": b.capsUnmatched}})
	}
	if len(ctrlLetter) > 0 {
		b.lost("windows.ctrlLetterFallback", "/hardware/layers", "Ctrl+letter gives U+0001 to U+001A on %d key(s) the file leaves empty", len(ctrlLetter))
	}
	for i, er := range b.file.ExtraRows {
		v, ok := vk.Parse(er.VK)
		if !ok {
			b.lost("windows.vkMissing", fmt.Sprintf("/targets/windows/extraRows/%d", i), "unknown virtual key %q", er.VK)
			continue
		}
		r := &row{vk: v, scan: 0xFFFF, extra: true, cells: make([]cell, len(er.Cells)), attrs: attrBits(er.Attributes)}
		for c, wc := range er.Cells {
			switch wc.Kind {
			case model.CellText:
				r.cells[c] = b.cellOf(model.Output{Units: utf16Of(wc.Text), Exact: true}, "/targets/windows/extraRows")
			case model.CellUnits:
				r.cells[c] = cell{units: wc.Units}
			case model.CellDeadKey:
				r.cells[c] = cell{dead: wc.DeadKey}
			}
		}
		rows = append(rows, r)
	}
	b.tables(rows)
}

func isFrame(v byte) bool { return v == 0x0D || v == 0x08 || v == 0x09 || v == 0x1B }

func (b *lowerer) isModifierPosition(id string) bool {
	for _, cm := range b.l.CustomModifiers {
		for _, p := range cm.Positions {
			if p == id {
				return true
			}
		}
	}
	return false
}

// checkSameState reports modifier sets that Windows maps to the same column but whose bindings differ from the
// representative's (§4).
func (b *lowerer) checkSameState(id string) {
	if b.l.Hardware == nil {
		return
	}
	for _, layer := range b.l.Hardware.Layers {
		for _, m := range layer.Modifiers {
			s, ok := b.stateOf(m)
			if !ok {
				continue
			}
			c, ok := b.colOf[s]
			if !ok {
				continue
			}
			want := b.compared(b.l.Effective(b.l.Resolve(id, b.reps[c])))
			got := b.compared(b.l.Effective(b.l.Resolve(id, stateFromSet(m))))
			if !reflect.DeepEqual(want, got) {
				kind := "windows.sideModifierWidened"
				if s == 6 {
					kind = "windows.altGrConflict"
				}
				b.lost(kind, "/positions/"+id, "%s: modifier set %v gives a different output than the column's representative state", id, []string(m))
			}
		}
	}
}

var attrNames = []struct {
	name string
	bit  byte
}{{"CAPLOK", windows.CAPLOK}, {"SGCAPS", windows.SGCAPS}, {"CAPLOKALTGR", windows.CAPLOKALTGR}, {"KANALOK", windows.KANALOK}, {"GRPSELTAP", windows.GRPSELTAP}}

func attrBits(names []string) byte {
	var v byte
	for _, n := range names {
		for _, a := range attrNames {
			if a.name == n {
				v |= a.bit
			}
		}
	}
	return v
}

func attrList(v byte) []string {
	out := []string{}
	for _, a := range attrNames {
		if v&a.bit != 0 {
			out = append(out, a.name)
		}
	}
	return out
}

// attributes decides the Caps Lock attributes of a row (§6) and fills its SGCAPS row.
func (b *lowerer) attributes(pi *posInfo, r *row) {
	out := func(s int) cell {
		if c, ok := b.colOf[s]; ok {
			return r.cells[c]
		}
		return cell{}
	}
	unmatched := false
	// D40: in every column, Caps Lock giving nothing where the column gives something is windows.capsUnmatched
	// (Windows types the column's output), and the column's output stands in for it below.
	capsOut := func(s int) cell {
		if _, ok := b.colOf[s]; !ok {
			return cell{}
		}
		bnd := b.l.Resolve(pi.id, b.representative(s).WithCaps(true))
		c := b.cellOf(b.l.Effective(bnd), "/hardware/layers ("+pi.id+")")
		if c.empty() && bnd != nil && !out(s).empty() {
			// D64: an explicit nothing is a real Caps Lock output (WCH_NONE in the Caps Lock row), not D40's case
			return cell{none: true}
		}
		if c.empty() && !out(s).empty() {
			unmatched = true
			return out(s)
		}
		return c
	}
	eq := func(a, c any) bool {
		norm := func(x any) any {
			if cl, ok := x.(cell); ok && cl.empty() {
				return cell{}
			}
			return x
		}
		return reflect.DeepEqual(norm(a), norm(c))
	}
	var attrs byte
	o0, o1, c0, c1 := out(0), out(1), capsOut(0), capsOut(1)
	switch {
	case eq(c0, o0) && eq(c1, o1):
	case eq(c0, o1) && eq(c1, o0):
		attrs |= windows.CAPLOK
	default:
		attrs |= windows.SGCAPS
		r.caps = []cell{c0, c1}
		for _, c := range r.caps {
			if len(c.units) > 1 {
				b.lost("windows.sgcapsLigature", "/positions/"+pi.id, "%s: a Caps Lock output needs more than one unit", pi.id)
			}
		}
		// Writer rule W-2 (D63, VM-4): a key whose dead keys are only in the Caps Lock row is written as the Caps Lock
		// row followed by an FF row; the shipped shape (rule 4) covers dead keys of the main row.
		mainDead := false
		for _, c := range r.cells {
			mainDead = mainDead || c.dead != ""
		}
		for i := 0; i < 2; i++ {
			if !mainDead && r.caps[i].dead != "" {
				continue
			}
			if (out(i).dead != "" || r.caps[i].dead != "") && out(i).dead != r.caps[i].dead {
				b.lost("windows.sgcapsDeadKey", "/positions/"+pi.id, "%s is SGCAPS and dead in a shape Windows cannot express", pi.id)
			}
		}
	}
	if _, ok := b.colOf[6]; ok {
		c6, c7, o6, o7 := capsOut(6), capsOut(7), out(6), out(7)
		switch {
		case eq(c6, o6) && eq(c7, o7):
		case eq(c6, o7) && eq(c7, o6):
			attrs |= windows.CAPLOKALTGR
		default:
			b.lost("windows.capsNotExpressible", "/positions/"+pi.id, "%s: Caps Lock outputs with AltGr cannot be expressed", pi.id)
		}
	}
	for s := range b.colOf {
		if s == 0 || s == 1 || s == 6 || s == 7 {
			continue
		}
		if !eq(capsOut(s), out(s)) {
			b.lost("windows.capsNotExpressible", "/positions/"+pi.id, "%s: the Caps Lock output of state %d differs", pi.id, s)
		}
	}
	if unmatched {
		b.capsUnmatched = append(b.capsUnmatched, pi.id)
	}
	// KANALOK (rule 5): only with a pinned Kana lock component; the Kana column is the state of bit KBDKANA.
	if lock := b.pin.Modifiers.KanaLockComponent; lock != "" {
		st := b.representative(0)
		st.Custom[lock] = true
		lockOut := b.cellOf(b.l.Effective(b.l.Resolve(pi.id, st)), "/hardware/layers ("+pi.id+")")
		kana := out(windows.KBDKANA)
		switch {
		case eq(lockOut, o0):
		case eq(lockOut, kana):
			attrs |= windows.KANALOK
		default:
			b.lost("windows.kanaLockNotExpressible", "/positions/"+pi.id, "%s: Kana-lock outputs are neither the Kana column's nor the base's", pi.id)
		}
	}
	if pi.attrs == nil {
		b.attrDefaulted = append(b.attrDefaulted, pi.id)
	} else {
		pinned := attrBits(pi.attrs)
		if pinned != attrs {
			b.lost("windows.pinOverridesModel", "/targets/windows/keys/"+pi.id+"/attributes", "pinned attributes %v differ from the Caps Lock layers (%v)", pi.attrs, attrList(attrs))
			if pinned&windows.SGCAPS != 0 && r.caps == nil {
				r.caps = []cell{c0, c1}
			}
		}
		attrs = pinned
	}
	r.attrs = attrs
	b.pin.Keys[pi.id].Attributes = attrList(attrs)
}

// tables groups the rows into tables (§7) and writes the cells (§6).
func (b *lowerer) tables(rows []*row) {
	type built struct {
		r     *row
		width int
		group int // pinned table index, or -1
		order int // pinned row order
		lines []windows.CharRow
	}
	// Pinned tables decide widths, the order of the tables and of their rows, and which rows share a table (two
	// tables of the same width stay apart: dll-format §8); other rows follow the rule, after the pinned ones.
	type pinnedRow struct{ table, width, order int }
	pinned := map[string]pinnedRow{}
	order := 0
	for ti, t := range b.file.Tables {
		for _, tr := range t.Rows {
			key := tr.Position
			if tr.VK != "" {
				if v, ok := vk.Parse(tr.VK); ok {
					key = "vk:" + vk.Name(v)
				}
			}
			if _, dup := pinned[key]; !dup {
				pinned[key] = pinnedRow{ti, t.Width, order}
			}
			order++
		}
	}
	keyOf := func(r *row) string {
		if r.extra {
			return "vk:" + vk.Name(r.vk)
		}
		return r.pos
	}
	var all []*built
	ligs := []windows.Ligature{}
	for _, r := range rows {
		w := 0
		for i, c := range r.cells {
			if !c.empty() {
				w = i + 1
			}
		}
		for i, c := range r.caps {
			if !c.empty() {
				w = max(w, i+1)
			}
		}
		x := &built{r: r, width: max(w, 1), group: -1}
		if p, ok := pinned[keyOf(r)]; ok {
			if p.width < w {
				b.lost("windows.pinOverridesModel", "/targets/windows/tables", "row %s needs width %d, pinned %d: columns are cut", keyOf(r), w, p.width)
			}
			x.width, x.group, x.order = p.width, p.table, p.order
		} else if w == 0 && r.attrs == 0 {
			continue // an empty row says nothing, unless the file pins it
		}
		all = append(all, x)
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, c := all[i], all[j]
		if (a.group >= 0) != (c.group >= 0) {
			return a.group >= 0
		}
		if a.group >= 0 {
			return a.order < c.order
		}
		if a.width != c.width {
			return a.width < c.width
		}
		if a.r.extra != c.r.extra {
			return !a.r.extra
		}
		return a.r.scan < c.r.scan
	})
	if len(b.file.Tables) == 0 && len(all) > 0 {
		b.defaulted("tables", nil, "rows by width, then by scan code")
	}
	for _, x := range all {
		r, w := x.r, x.width
		main := windows.CharRow{VK: r.vk, Attributes: r.attrs, Chars: make([]uint16, w)}
		deadRow := make([]uint16, w)
		anyDead := false
		for i := 0; i < w; i++ {
			main.Chars[i], deadRow[i] = windows.WCH_NONE, windows.WCH_NONE
			if i >= len(r.cells) {
				continue
			}
			c := r.cells[i]
			switch {
			case c.dead != "":
				main.Chars[i] = windows.WCH_DEAD
				deadRow[i] = b.dead[c.dead]
				anyDead = true
			case len(c.units) == 1 && c.units[0] == windows.WCH_NONE:
				b.lost("windows.outputUnencodable", "/positions/"+r.pos, "U+F000 alone cannot be emitted")
			case len(c.units) == 1 && c.units[0] != windows.WCH_DEAD && c.units[0] != windows.WCH_LGTR:
				main.Chars[i] = c.units[0]
			case len(c.units) >= 1:
				if len(c.units) > windows.MaxLigatureWidth {
					b.lost("windows.ligatureTooLong", "/positions/"+r.pos, "an output of %d units", len(c.units))
					continue
				}
				if len(c.units) > 16 {
					b.lost("windows.ligatureTruncatedTyped", "/positions/"+r.pos, "an output of %d units: typing delivers 16", len(c.units))
				}
				for _, u := range c.units {
					if u == windows.WCH_NONE {
						b.lost("windows.outputUnencodable", "/positions/"+r.pos, "U+F000 inside a ligature")
					}
				}
				main.Chars[i] = windows.WCH_LGTR
				ligs = append(ligs, windows.Ligature{VK: r.vk, Column: uint16(i), Chars: c.units})
			}
		}
		x.lines = []windows.CharRow{main}
		switch {
		case r.attrs&windows.SGCAPS != 0 && anyDead:
			// The shape of dll-format §4.2: one FF row is also the Caps Lock row.
			for i, c := range r.caps {
				if i < w && deadRow[i] == windows.WCH_NONE && len(c.units) == 1 {
					deadRow[i] = c.units[0]
				}
			}
			x.lines = append(x.lines, windows.CharRow{VK: 0xFF, Chars: deadRow})
		case r.attrs&windows.SGCAPS != 0:
			capsRow := windows.CharRow{VK: r.vk, Chars: make([]uint16, w)}
			capsDead := map[int]uint16{}
			for i := range capsRow.Chars {
				capsRow.Chars[i] = windows.WCH_NONE
				if i < len(r.caps) && len(r.caps[i].units) >= 1 {
					capsRow.Chars[i] = r.caps[i].units[0]
				} else if i < len(r.caps) && r.caps[i].dead != "" {
					capsRow.Chars[i] = windows.WCH_DEAD
					capsDead[i] = b.dead[r.caps[i].dead]
				}
			}
			x.lines = append(x.lines, capsRow)
			if len(capsDead) > 0 { // W-2: the dead characters of the Caps Lock row follow it
				ff := windows.CharRow{VK: 0xFF, Chars: make([]uint16, w)}
				for i := range ff.Chars {
					ff.Chars[i] = windows.WCH_NONE
					if d, ok := capsDead[i]; ok {
						ff.Chars[i] = d
					}
				}
				x.lines = append(x.lines, ff)
			}
		case anyDead:
			x.lines = append(x.lines, windows.CharRow{VK: 0xFF, Chars: deadRow})
		}
	}
	var pinTables []model.WindowsTable
	prevGroup := -2
	for _, x := range all {
		n := len(b.t.CharTables)
		if n == 0 || b.t.CharTables[n-1].Width != x.width || x.group != prevGroup {
			prevGroup = x.group
			b.t.CharTables = append(b.t.CharTables, windows.CharTable{Width: x.width})
			pinTables = append(pinTables, model.WindowsTable{Width: x.width})
			n++
		}
		b.t.CharTables[n-1].Rows = append(b.t.CharTables[n-1].Rows, x.lines...)
		tr := model.TableRow{Position: x.r.pos}
		if x.r.extra {
			tr = model.TableRow{VK: vk.Name(x.r.vk)}
		}
		pinTables[len(pinTables)-1].Rows = append(pinTables[len(pinTables)-1].Rows, tr)
	}
	b.pin.Tables = pinTables
	b.pin.ExtraRows = b.file.ExtraRows
	width := 0
	for _, lg := range ligs {
		width = max(width, len(lg.Chars))
	}
	if len(ligs) > 0 {
		b.t.Ligatures, b.t.LigatureWidth = ligs, width
	}
}

// vkBound reports whether some bound position of the file has VK v (Enter for number-pad Enter: one row per VK).
func (b *lowerer) vkBound(v byte) bool {
	for _, pi := range b.pos {
		if pi.vk == v && !pi.fromTable && b.bound(pi.id) {
			return true
		}
	}
	return false
}
