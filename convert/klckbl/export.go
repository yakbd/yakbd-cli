package klckbl

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// exportPolicy accepts the Windows build losses that do not concern a `.klc` file (the LANGID is optional there,
// and the builds are not written).
var exportPolicy = &model.OnLoss{Kinds: map[string]string{
	"windows.langIdMissing": "accept", "windows.architectureUnsupported": "accept", "windows.wow64Missing": "accept",
}}

var bitNames = map[int]string{0x08: "KBDKANA", 0x10: "KBDROYA", 0x20: "KBDLOYA", 0x80: "KBDGRPSELTAP"}

// ToKLC converts a layout into a `.klc` file. Every Windows value comes from the Windows build (pins and default
// rules); what a `.klc` file cannot hold is reported as klc.notRepresentable, and the rows MSKLC adds by itself are
// left out when they are unchanged (D36).
func ToKLC(l *model.Layout, opt build.Options) (*klc.File, *loss.Report) {
	opt.Policy = ExportPolicy(opt.Policy)
	res, r := build.Lower(l, opt)
	if res == nil {
		return nil, r
	}
	ex := &exporter{l: l, res: res, r: r, t: &res.DLL.Tables}
	f := ex.run()
	ex.pinsWithoutKLC(opt)
	return f, r
}

// pinsWithoutKLC reports two pins a .klc file cannot hold when they change the tables (an imported DLL has both,
// coverage/windows.md §11): useTableA false (MSKLC adds its default scan codes, OBS D20) and the table order
// (MSKLC orders the tables itself, OBS D13).
func (ex *exporter) pinsWithoutKLC(opt build.Options) {
	w := ex.l.Targets
	if w == nil || w.Windows == nil {
		return
	}
	rebuild := func(change func(t *model.WindowsTarget)) *windows.Tables {
		l := *ex.l
		t := *ex.l.Targets
		wt := *ex.l.Targets.Windows
		change(&wt)
		t.Windows, l.Targets = &wt, &t
		res, _ := build.Lower(&l, opt)
		if res == nil {
			return nil
		}
		return windows.Canonical(&res.DLL.Tables)
	}
	mine := windows.Canonical(ex.t)
	if u := w.Windows.UseTableA; u != nil && !*u {
		if o := rebuild(func(t *model.WindowsTarget) { t.UseTableA = nil }); o == nil || !reflect.DeepEqual(o.ScanToVK, mine.ScanToVK) || !reflect.DeepEqual(o.E0, mine.E0) || !reflect.DeepEqual(o.E1, mine.E1) {
			ex.lost("klc.notRepresentable", "useTableA false: the scan-code tables differ from what a .klc file gives (MSKLC adds its default keys)")
		}
	}
	if len(w.Windows.Tables) > 0 {
		if o := rebuild(func(t *model.WindowsTarget) { t.Tables = nil }); o == nil || !reflect.DeepEqual(o.CharTables, mine.CharTables) {
			ex.lost("klc.notRepresentable", "the pinned order of the character tables (a .klc file has none)")
		}
	}
}

type exporter struct {
	l   *model.Layout
	res *build.Result
	r   *loss.Report
	t   *windows.Tables
	f   *klc.File
}

func (ex *exporter) lost(kind, format string, args ...any) {
	ex.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Error, Code: kind, Message: fmt.Sprintf(format, args...)})
}

func strPtr(s string) *string { return &s }

func (ex *exporter) run() *klc.File {
	l, p, t := ex.l, ex.res.Pins, ex.t
	f := &klc.File{Name: p.DLLName, Description: l.Metadata.Name, EndKBD: true}
	ex.f = f
	if len(f.Name) > 8 || len([]rune(f.Description)) > 255 {
		ex.lost("klc.nameTooLong", "DLL name or description beyond 8 / 255 characters")
	}
	if l.Metadata.Copyright != "" {
		f.Copyright = strPtr(l.Metadata.Copyright)
	}
	if l.Metadata.Organization != "" {
		f.Company = strPtr(l.Metadata.Organization)
	}
	fs := fileStrings(l)
	if fs.LocaleName != nil && *fs.LocaleName != "" {
		f.LocaleName = fs.LocaleName
	}
	if p.Registration != nil && p.Registration.LangID != "" {
		f.LocaleID = strPtr("0000" + strings.ToLower(p.Registration.LangID))
	}
	if l.Targets != nil && l.Targets.KLC != nil && l.Targets.KLC.VersionLine != nil {
		f.Version = l.Targets.KLC.VersionLine
	} else {
		f.Version = strPtr("1.0")
	}
	if t.Type != 0 || t.SubType != 0 {
		ex.lost("klc.notRepresentable", "keyboard type %d/%d", t.Type, t.SubType)
	}
	if t.Version != windows.KBD_VERSION {
		ex.lost("klc.notRepresentable", "version word %d", t.Version)
	}
	for _, er := range p.ExtraRows {
		if !IsImpliedExtraRow(er, ex.res.Columns()) {
			ex.lost("klc.notRepresentable", "extraRows")
			break
		}
	}
	// Modifiers and shift states.
	for _, vb := range t.Modifiers.VKToBit {
		if vb.VK == 0x10 && vb.Bits == windows.KBDSHIFT || vb.VK == 0x11 && vb.Bits == windows.KBDCTRL || vb.VK == 0x12 && vb.Bits == windows.KBDALT {
			continue
		}
		name, ok := bitNames[int(vb.Bits)]
		if !ok {
			ex.lost("klc.notRepresentable", "modifier bit %#x", vb.Bits)
			continue
		}
		f.Modifiers = append(f.Modifiers, klc.Modifier{VK: vb.VK, Bit: name})
	}
	cols := ex.res.Columns()
	f.ShiftStates = append([]int(nil), cols...)
	ex.requiredStates()
	seen := map[int]bool{}
	for s, c := range t.Modifiers.ModNumber {
		if c == windows.SHFT_INVALID {
			continue
		}
		if seen[int(c)] && cols[c] != s {
			ex.lost("klc.notRepresentable", "modNumber with several states in column %d", c)
		}
		seen[int(c)] = true
	}
	if t.LocaleFlags&windows.KLLF_ALTGR != 0 {
		f.Attributes = append(f.Attributes, "ALTGR")
	} else {
		// MSKLC sets KLLF_ALTGR whenever a Ctrl+Alt column exists (OBS D25, D26; the .klc import does the same), so
		// a .klc file cannot say "Ctrl+Alt column, no AltGr" (CLDR fr: layers "ctrl alt", no altR).
		for _, s := range cols {
			if s&6 == 6 {
				ex.lost("klc.notRepresentable", "a Ctrl+Alt column without KLLF_ALTGR (MSKLC sets ALTGR for it)")
				break
			}
		}
	}
	if t.LocaleFlags&windows.KLLF_SHIFTLOCK != 0 {
		f.Attributes = append(f.Attributes, "SHIFTLOCK")
	}
	if t.LocaleFlags&windows.KLLF_LRM_RLM != 0 {
		f.Attributes = append(f.Attributes, "LRM_RLM")
	}
	ex.keys(len(f.ShiftStates)) // the states requiredStates appended get -1 cells
	ex.impliedAdditions()
	ex.deadKeys()
	for _, kn := range t.KeyNames {
		f.KeyNames = append(f.KeyNames, klc.KeyName{Code: uint16(kn.Scan), Name: kn.Name})
	}
	for _, kn := range t.KeyNamesExt {
		f.KeyNamesExt = append(f.KeyNamesExt, klc.KeyName{Code: uint16(kn.Scan), Name: kn.Name})
	}
	for _, dn := range t.KeyNamesDead {
		f.KeyNamesDead = append(f.KeyNamesDead, klc.DeadName{Dead: dn.Dead, Name: dn.Name})
	}
	f.Descriptions = langTexts(fs.LayoutName)
	f.LanguageNames = langTexts(fs.LanguageName)
	return f
}

func langTexts(m map[string]string) []klc.LangText {
	var out []klc.LangText
	for _, k := range sortedKeys(m) {
		v, _ := strconv.ParseUint(k, 16, 16)
		out = append(out, klc.LangText{LangID: uint16(v), Text: m[k]})
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// rowsByVK returns the rows of each VK with the row that follows (dead-character or SGCAPS row).
func (ex *exporter) rowsByVK() map[byte][2]*windows.CharRow {
	out := map[byte][2]*windows.CharRow{}
	for ti := range ex.t.CharTables {
		rows := ex.t.CharTables[ti].Rows
		for i := range rows {
			r := &rows[i]
			if r.VK == 0xFF {
				continue
			}
			if _, ok := out[r.VK]; ok {
				continue
			}
			var next *windows.CharRow
			if i+1 < len(rows) && (rows[i+1].VK == 0xFF || r.Attributes&windows.SGCAPS != 0 && rows[i+1].VK == r.VK) {
				next = &rows[i+1]
			}
			out[r.VK] = [2]*windows.CharRow{r, next}
		}
	}
	return out
}

// rowAfter returns the row that follows r in its table, or nil.
func (ex *exporter) rowAfter(r *windows.CharRow) *windows.CharRow {
	for ti := range ex.t.CharTables {
		rows := ex.t.CharTables[ti].Rows
		for i := range rows {
			if &rows[i] == r && i+1 < len(rows) {
				return &rows[i+1]
			}
		}
	}
	return nil
}

func (ex *exporter) ligature(v byte, col int) []uint16 {
	for _, lg := range ex.t.Ligatures {
		if lg.VK == v && int(lg.Column) == col {
			return lg.Chars
		}
	}
	return nil
}

func (ex *exporter) cell(row, next *windows.CharRow, col int) klc.Cell {
	if row == nil || col >= len(row.Chars) {
		return klc.Cell{Kind: klc.CellNone}
	}
	switch c := row.Chars[col]; c {
	case windows.WCH_NONE:
		return klc.Cell{Kind: klc.CellNone}
	case windows.WCH_DEAD:
		return klc.Cell{Kind: klc.CellDead, Rune: rune(next.Chars[col])}
	case windows.WCH_LGTR:
		// A supplementary character stays a LIGATURE line with the column index: written as a 5- or 6-digit cell,
		// kbdutool makes the ligature itself with the shift-state value (OBS W3), and Windows finds nothing in AltGr
		// columns (VM run of 2026-11-26, x-latin-altgr-deadkeys).
		chars := ex.ligature(row.VK, col)
		if len(chars) < 2 || len(chars) > 5 {
			ex.lost("klc.notRepresentable", "an output of %d units on %s column %d", len(chars), vk.Name(row.VK), col)
			return klc.Cell{Kind: klc.CellNone}
		}
		ex.f.Ligatures = append(ex.f.Ligatures, klc.Ligature{VK: row.VK, Column: col, Chars: chars})
		return klc.Cell{Kind: klc.CellLigature}
	default:
		if utf16.IsSurrogate(rune(c)) {
			// kbl D79. A lone surrogate half: a LAYOUT cell holds a character, and a .klc file has no way to write one unit of
			// a pair (klc §4: surrogates only in a LIGATURE line, as a pair).
			ex.lost("klc.notRepresentable", "the lone surrogate %04X on %s column %d", c, vk.Name(row.VK), col)
			return klc.Cell{Kind: klc.CellNone}
		}
		return klc.Cell{Kind: klc.CellChar, Rune: rune(c)}
	}
}

// keys writes one LAYOUT row per position of the file that has characters or a non-default VK.
func (ex *exporter) keys(cols int) {
	rows := ex.rowsByVK()
	implied := ImpliedRows()
	used := map[byte]bool{}
	for _, pi := range ex.res.PositionList() {
		if pi.FromTableA {
			continue
		}
		if pi.VK == vk.None {
			// A scan code pinned to "no key" (an imported DLL): a .klc file cannot say that, and MSKLC gives the
			// scan code its own default where it has one (OBS D20).
			if _, ok := vk.DefaultFor(pi.Scan); ok || pi.Flags != 0 {
				ex.lost("klc.notRepresentable", "scan code %s has no key", pi.ID)
			}
			continue
		}
		if pi.Flags != vk.DefaultFlags(pi.Scan) {
			ex.lost("klc.notRepresentable", "scan-code flags of %s", pi.ID)
		}
		pair, has := rows[pi.RowVK]
		if used[pi.RowVK] {
			has = false
		}
		def, hasDef := vk.DefaultFor(pi.Scan)
		k := klc.Key{Scan: pi.Scan, VK: pi.RowVK}
		if !pi.NumLock {
			k.VK = pi.VK
		}
		if has {
			used[pi.RowVK] = true
			row, next := pair[0], pair[1]
			k.HasCells = true
			k.Cap = row.Attributes & (windows.CAPLOK | windows.SGCAPS | windows.CAPLOKALTGR)
			if row.Attributes&^(windows.CAPLOK|windows.SGCAPS|windows.CAPLOKALTGR) != 0 {
				ex.lost("klc.notRepresentable", "row attributes %#x of %s", row.Attributes, pi.ID)
			}
			last := -1
			for c := 0; c < cols; c++ {
				cell := ex.cell(row, next, c)
				k.Cells = append(k.Cells, cell)
				if cell.Kind != klc.CellNone {
					last = c
				}
			}
			// Every column is written, -1 included: a row shorter than SHIFTSTATE is not read the same way by
			// kbdutool (VM run of 2026-11-26: Ctrl+Space typed a space for "39 SPACE 0 0020 0020").
			if last < 0 {
				k.Cells = nil
			}
			if row.Attributes&windows.SGCAPS != 0 && next != nil {
				for c := 0; c < 2 && c < len(next.Chars); c++ {
					if next.Chars[c] == windows.WCH_NONE {
						k.SGCap = append(k.SGCap, klc.Cell{Kind: klc.CellNone})
					} else if ff := ex.rowAfter(next); next.Chars[c] == windows.WCH_DEAD && ff != nil && ff.VK == 0xFF {
						// A dead key only in the Caps Lock row (writer rule W-2) has no .klc form: MSKLC reads hhhh@ in the
						// continuation line as the character (OBS K14), and so does the reader (kbl D81). The plain
						// dead character is written, and the loss reported.
						ex.lost("klc.notRepresentable", "the Caps Lock dead key %04X of %s column %d (a .klc continuation line holds characters only, D81)", ff.Chars[c], pi.ID, c)
						k.SGCap = append(k.SGCap, klc.Cell{Kind: klc.CellChar, Rune: rune(ff.Chars[c])})
					} else {
						k.SGCap = append(k.SGCap, klc.Cell{Kind: klc.CellChar, Rune: rune(next.Chars[c])})
					}
				}
			}
			if isImplied(k, ex.f.ShiftStates, implied) && hasDef && byte(def) == pi.VK {
				continue
			}
			if k.Cap == 0 && len(k.Cells) == 0 {
				k.HasCells = false
			}
		} else if hasDef && byte(def) == pi.VK {
			continue // nothing to say
		}
		ex.f.Keys = append(ex.f.Keys, k)
	}
}

// isImplied reports a row that equals what MSKLC adds by itself (OBS D16).
func isImplied(k klc.Key, states []int, implied map[byte]map[int]uint16) bool {
	chars, ok := implied[k.VK]
	if !ok || k.Cap != 0 {
		return false
	}
	for i, s := range states {
		want, has := chars[s]
		var got klc.Cell
		if i < len(k.Cells) {
			got = k.Cells[i]
		}
		if has {
			// An empty cell where MSKLC adds a character is the same file: MSKLC (and the .klc import) fill it
			// either way, and impliedAdditions reports it.
			if got.Kind != klc.CellNone && (got.Kind != klc.CellChar || got.Rune != rune(want)) {
				return false
			}
		} else if got.Kind != klc.CellNone {
			return false
		}
	}
	return true
}

func (ex *exporter) deadKeys() {
	for _, d := range ex.t.DeadKeys {
		var blk *klc.DeadKey
		for i := range ex.f.DeadKeys {
			if ex.f.DeadKeys[i].Dead == d.Dead {
				blk = &ex.f.DeadKeys[i]
			}
		}
		if blk == nil {
			ex.f.DeadKeys = append(ex.f.DeadKeys, klc.DeadKey{Dead: d.Dead})
			blk = &ex.f.DeadKeys[len(ex.f.DeadKeys)-1]
		}
		blk.Entries = append(blk.Entries, klc.DeadEntry{Base: d.Base, Result: d.Composed, Chained: d.Flags&windows.DKF_DEAD != 0})
	}
}

// fileStrings returns the strings the layout itself pins (not the default rule's).
func fileStrings(l *model.Layout) *model.WindowsStrings {
	if l.Targets != nil && l.Targets.Windows != nil && l.Targets.Windows.Strings != nil {
		return l.Targets.Windows.Strings
	}
	return &model.WindowsStrings{}
}

// ExportPolicy returns the policy ToKLC applies, merged with extra (for example -strict-defaults), so that a
// caller resolving the report again keeps it.
func ExportPolicy(extra *model.OnLoss) *model.OnLoss {
	p := &model.OnLoss{Kinds: map[string]string{}}
	for k, v := range exportPolicy.Kinds {
		p.Kinds[k] = v
	}
	if extra != nil {
		for k, v := range extra.Kinds {
			p.Kinds[k] = v
		}
	}
	return p
}

// requiredStates (kbl D78) adds the shift states a .klc file must declare (klc §3: SHIFTSTATE has at least two entries and
// state 2) when the build has no such column. The added columns hold no cells. They are appended, so the columns
// of the build keep their positions.
func (ex *exporter) requiredStates() {
	for _, s := range []int{0, 1, 2} {
		has := false
		for _, v := range ex.f.ShiftStates {
			has = has || v == s
		}
		if !has {
			ex.f.ShiftStates = append(ex.f.ShiftStates, s)
		}
	}
}

// impliedAdditions reports (klc.impliedRows, accepted by default) the rows MSKLC adds by itself (OBS D16; the .klc
// importer writes them too, D36) where the
// layout has no output: a .klc file cannot say "nothing" there, so its build gains these characters.
func (ex *exporter) impliedAdditions() {
	rows := ex.rowsByVK()
	cols := map[int]int{}
	for i, s := range ex.f.ShiftStates {
		cols[s] = i
	}
	var gained []string
	for _, ir := range impliedRows {
		pair, has := rows[ir.vk]
		for s := range ir.chars {
			c, declared := cols[s]
			if !declared {
				continue
			}
			if has && pair[0] != nil && c < len(pair[0].Chars) && pair[0].Chars[c] != windows.WCH_NONE {
				continue
			}
			gained = append(gained, fmt.Sprintf("%s state %d", vk.Name(ir.vk), s))
		}
	}
	if len(gained) > 0 {
		sort.Strings(gained)
		ex.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Accepted, Code: "klc.impliedRows",
			Message: "a .klc file cannot leave these outputs empty: MSKLC (and the .klc import, D36) add " + strings.Join(gained, ", "),
			Detail:  map[string]any{"added": gained}})
	}
}
