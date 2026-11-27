// Package klckbl converts between `.klc` files (format/klc) and the format-neutral model, by the import
// conventions of the kbl specification's coverage/windows.md §7 and §10 (decisions D28, D33, D36). The import
// pins every Windows value the file states into targets.windows, so that the layout builds the same tables; the
// export goes through the Windows build (target/windows/build), so that every `.klc` value comes from the same
// rules as a DLL.
package klckbl

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// appendixA are the position names of SPEC Appendix A that Table A does not have (Table A gives the others).
var appendixA = map[uint16]string{0x59: "NumpadEqual", 0x70: "KanaMode", 0x73: "IntlRo", 0x79: "Convert",
	0x7B: "NonConvert", 0x7D: "IntlYen", 0x7E: "NumpadComma"}

// PositionID returns the position id of a scan code (coverage/windows.md §10 item 1).
func PositionID(sc uint16) string {
	if n, ok := appendixA[sc]; ok {
		return n
	}
	for _, d := range vk.Defaults {
		if d.Scan == sc {
			return d.Position
		}
	}
	return fmt.Sprintf("sc%02X", sc)
}

func scanText(sc uint16) string {
	if sc>>8 != 0 {
		return fmt.Sprintf("%04X", sc)
	}
	return fmt.Sprintf("%02X", sc)
}

var compNames = map[string]string{"KBDKANA": "kana", "KBDROYA": "roya", "KBDLOYA": "loya", "KBDGRPSELTAP": "grpSelTap"}

// numpadOff maps a Num-Lock-on VK to the Num-Lock-off VK of its key (windows-behaviour §3.2).
var numpadOff = map[byte]byte{0x67: 0x24, 0x68: 0x26, 0x69: 0x21, 0x64: 0x25, 0x65: 0x0C, 0x66: 0x27, 0x61: 0x23,
	0x62: 0x28, 0x63: 0x22, 0x60: 0x2D, 0x6E: 0x2E}

var reVersion = regexp.MustCompile(`^\d+(\.\d+){0,3}$`)

type importer struct {
	f      *klc.File
	r      *loss.Report
	name   string
	l      *model.Layout
	w      *model.WindowsTarget
	layers map[int]*model.HardwareLayer // shift state -> layer
	caps   map[string]*model.HardwareLayer
	order  []string // layer ids in output order
	byID   map[string]*model.HardwareLayer
	posOf  map[uint16]string
	vkPos  map[byte]string // VK -> position (first)
}

// FromKLC converts a parsed `.klc` file into a layout. The report lists what was implied (klc.impliedRows).
func FromKLC(f *klc.File, name string) (*model.Layout, *loss.Report) {
	im := &importer{f: f, r: &loss.Report{}, name: name, layers: map[int]*model.HardwareLayer{},
		caps: map[string]*model.HardwareLayer{}, byID: map[string]*model.HardwareLayer{},
		posOf: map[uint16]string{}, vkPos: map[byte]string{}}
	im.run()
	return im.l, im.r
}

func (im *importer) run() {
	f := im.f
	l := &model.Layout{Kbl: model.FormatVersion, Positions: map[string]*model.Position{}}
	im.l = l
	w := &model.WindowsTarget{Keys: map[string]*model.WindowsKey{}}
	im.w = w
	l.Targets = &model.Targets{Windows: w}
	l.Metadata.ID = "und"
	if f.LocaleName != nil && *f.LocaleName != "" {
		l.Metadata.ID = *f.LocaleName
	}
	l.Metadata.Name = f.Description
	l.Metadata.Provenance = &model.Provenance{Source: im.name, Format: "klc"}
	if f.Copyright != nil {
		l.Metadata.Copyright = *f.Copyright
	}
	if f.Company != nil {
		l.Metadata.Organization = *f.Company
	}
	if f.Version != nil {
		v := strings.TrimSpace(*f.Version)
		l.Targets.KLC = &model.KLCTarget{VersionLine: &v}
		if reVersion.MatchString(v) {
			l.Metadata.Version = v
		}
	}
	if regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,7}$`).MatchString(f.Name) {
		w.DLLName = f.Name
	} else {
		im.r.Warnf("klc.name", loss.Where{File: im.name}, "KBD name %q is not a valid DLL name; the default rule applies", f.Name)
	}
	// Strings and registration.
	st := &model.WindowsStrings{}
	for _, t := range f.Descriptions {
		if st.LayoutName == nil {
			st.LayoutName = map[string]string{}
		}
		st.LayoutName[fmt.Sprintf("%04X", t.LangID)] = t.Text
	}
	for _, t := range f.LanguageNames {
		if st.LanguageName == nil {
			st.LanguageName = map[string]string{}
		}
		st.LanguageName[fmt.Sprintf("%04X", t.LangID)] = t.Text
	}
	if f.LocaleName != nil {
		v := *f.LocaleName
		st.LocaleName = &v
	}
	if st.LayoutName != nil || st.LanguageName != nil || st.LocaleName != nil {
		w.Strings = st
	}
	if f.LocaleID != nil && len(*f.LocaleID) == 8 {
		w.Registration = &model.Registration{LangID: strings.ToUpper((*f.LocaleID)[4:])}
	}
	im.modifiers()
	im.flags()
	im.positions()
	im.deadKeys()
	im.bindings()
	im.implied()
	im.finishLayers()
	keepExact(l)
}

// keepExact sets settings.normalization to disabled when NFC would change an imported output: Windows does not
// normalise, so a .klc file means its code points as they are.
func keepExact(l *model.Layout) {
	if l.OutputsNFC() {
		return
	}
	if l.Settings == nil {
		l.Settings = &model.Settings{}
	}
	l.Settings.Normalization = "disabled"
}

func (im *importer) flags() {
	has := map[string]bool{}
	for _, a := range im.f.Attributes {
		has[a] = true
	}
	// MSKLC sets KLLF_ALTGR when a Ctrl+Alt column exists (OBS D25, D26); that is part of what the file means.
	altGr := has["ALTGR"]
	for _, s := range im.f.ShiftStates {
		altGr = altGr || s&6 == 6
	}
	shiftLock, lrm := has["SHIFTLOCK"], has["LRM_RLM"]
	im.w.LocaleFlags = &model.LocaleFlags{AltGr: &altGr, ShiftLock: &shiftLock, LrmRlm: &lrm}
	if shiftLock || lrm {
		im.l.Settings = &model.Settings{}
		if shiftLock {
			im.l.Settings.CapsLockOffBy = "shift"
		}
		if lrm {
			im.l.Settings.BidiMarksOnShiftBackspace = &lrm
		}
	}
	// Layers per shift state, in file order (§10 item 2).
	for _, s := range im.f.ShiftStates {
		layer := &model.HardwareLayer{ID: fmt.Sprintf("s%d", s), Bindings: map[string]*model.Binding{}}
		layer.Modifiers = im.setsOf(s, altGr, nil)
		im.layers[s] = layer
		im.addLayer(layer)
	}
}

func (im *importer) addLayer(layer *model.HardwareLayer) {
	im.order = append(im.order, layer.ID)
	im.byID[layer.ID] = layer
}

// setsOf returns the modifier sets of a shift state; extra components (caps) are added to each.
func (im *importer) setsOf(s int, altGr bool, extra []string) []model.ModifierSet {
	var base []string
	if s&1 != 0 {
		base = append(base, "shift")
	}
	var custom []string
	for _, m := range im.f.Modifiers {
		bit, _ := klc.ModifierBit(m.Bit)
		if s&int(bit) != 0 {
			custom = append(custom, compNames[m.Bit])
		}
	}
	mk := func(parts ...string) model.ModifierSet {
		set := model.ModifierSet{}
		set = append(set, extra...)
		set = append(set, base...)
		set = append(set, parts...)
		set = append(set, custom...)
		return set
	}
	switch {
	case s&6 == 6 && altGr:
		return []model.ModifierSet{mk("altR"), mk("ctrl", "alt")}
	case s&6 == 6:
		return []model.ModifierSet{mk("ctrl", "alt")}
	case s&2 != 0:
		return []model.ModifierSet{mk("ctrl")}
	case s&4 != 0 && altGr:
		// With AltGr, right Alt alone is the Ctrl+Alt column; an Alt-only state can only be left Alt.
		return []model.ModifierSet{mk("altL")}
	case s&4 != 0:
		return []model.ModifierSet{mk("alt")}
	}
	return []model.ModifierSet{mk()}
}

func (im *importer) modifiers() {
	f, w := im.f, im.w
	mods := &model.WindowsModifiers{Bits: []model.ModifierBit{{VK: "SHIFT", Bit: 1, Component: "shift"},
		{VK: "CONTROL", Bit: 2, Component: "ctrl"}, {VK: "MENU", Bit: 4, Component: "alt"}}}
	for _, m := range f.Modifiers {
		bit, _ := klc.ModifierBit(m.Bit)
		comp := compNames[m.Bit]
		mods.Bits = append(mods.Bits, model.ModifierBit{VK: vk.Name(m.VK), Bit: int(bit), Component: comp})
		pos := ""
		for _, k := range f.Keys {
			if k.VK == m.VK {
				pos = PositionID(k.Scan)
			}
		}
		if pos == "" {
			pos = "vk" + vk.Name(m.VK)
			im.l.Positions[pos] = &model.Position{Description: "modifier key " + vk.Name(m.VK) + " of the .klc MODIFIERS section"}
			w.Keys[pos] = &model.WindowsKey{VK: vk.Name(m.VK)}
		}
		if im.l.CustomModifiers == nil {
			im.l.CustomModifiers = map[string]*model.CustomModifier{}
		}
		im.l.CustomModifiers[comp] = &model.CustomModifier{Kind: "held", Positions: []string{pos}}
	}
	maxS := 0
	for _, s := range f.ShiftStates {
		maxS = max(maxS, s)
	}
	mods.ModNumber = make([]*int, maxS+1)
	for i, s := range f.ShiftStates {
		c := i
		mods.ModNumber[s] = &c
	}
	w.Modifiers = mods
}

// positions creates one position per LAYOUT row and key-name code (§10 item 1, D33).
func (im *importer) positions() {
	f, l, w := im.f, im.l, im.w
	ensure := func(sc uint16) string {
		if id, ok := im.posOf[sc]; ok {
			return id
		}
		id := PositionID(sc)
		if l.Positions[id] == nil {
			l.Positions[id] = &model.Position{}
		}
		l.Positions[id].ScanCode = scanText(sc)
		im.posOf[sc] = id
		return id
	}
	for _, k := range f.Keys {
		id := ensure(k.Scan)
		v := k.VK
		if d, ok := vk.DefaultFor(k.Scan); ok && d&vk.KBDNUMPAD != 0 && numpadOff[k.VK] == byte(d) {
			// D33: the row gives the characters of the Num-Lock-on key; the position keeps its Table A VK.
			v = byte(d)
			t := true
			l.Positions[id].NumLock = &t
		}
		wk := &model.WindowsKey{VK: vk.Name(v)}
		var attrs []string
		if k.Cap&klc.CapCAPLOK != 0 {
			attrs = append(attrs, "CAPLOK")
		}
		if k.Cap&klc.CapSGCAPS != 0 {
			attrs = append(attrs, "SGCAPS")
		}
		if k.Cap&klc.CapCAPLOKALTGR != 0 {
			attrs = append(attrs, "CAPLOKALTGR")
		}
		if k.HasCells {
			wk.Attributes = attrs
			if wk.Attributes == nil {
				wk.Attributes = []string{}
			}
		}
		w.Keys[id] = wk
		if _, ok := im.vkPos[k.VK]; !ok {
			im.vkPos[k.VK] = id
		}
	}
	for _, kn := range f.KeyNames {
		id := ensure(kn.Code & 0xFF)
		l.Positions[id].Name = kn.Name
	}
	for _, kn := range f.KeyNamesExt {
		id := ensure(0xE000 | kn.Code&0xFF)
		l.Positions[id].Name = kn.Name
	}
}

func deadID(u uint16) string { return fmt.Sprintf("u%04x", u) }

func textOrUnits(units []uint16) model.TextOrUnits {
	if validText(units) {
		return model.TextOrUnits{Text: string(utf16.Decode(units))}
	}
	return model.TextOrUnits{Units: units}
}

func validText(units []uint16) bool {
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u >= 0xD800 && u <= 0xDBFF:
			if i+1 >= len(units) || units[i+1] < 0xDC00 || units[i+1] > 0xDFFF {
				return false
			}
			i++
		case u >= 0xDC00 && u <= 0xDFFF:
			return false
		}
	}
	return true
}

func value(units []uint16) model.Value {
	t := textOrUnits(units)
	if t.Units != nil {
		return model.Value{Kind: model.ValueUnits, Units: t.Units}
	}
	return model.Value{Kind: model.ValueText, Text: t.Text}
}

// deadKeys creates one dead key per DEADKEY block (§10 item 4).
func (im *importer) deadKeys() {
	l := im.l
	l.DeadKeys = map[string]*model.DeadKey{}
	for _, d := range im.f.DeadKeys {
		dk := im.ensureDead(d.Dead)
		for _, e := range d.Entries {
			entry := model.DeadKeyEntry{Base: value([]uint16{e.Base})}
			if e.Chained {
				im.ensureDead(e.Result)
				entry.Result = model.Value{Kind: model.ValueDeadKey, DeadKey: deadID(e.Result)}
			} else {
				entry.Result = value([]uint16{e.Result})
			}
			dk.Entries = append(dk.Entries, entry)
		}
	}
	for _, n := range im.f.KeyNamesDead {
		im.ensureDead(n.Dead).Name = n.Name
	}
}

func (im *importer) ensureDead(u uint16) *model.DeadKey {
	id := deadID(u)
	if dk := im.l.DeadKeys[id]; dk != nil {
		return dk
	}
	dk := &model.DeadKey{Spacing: textOrUnits([]uint16{u}), Entries: []model.DeadKeyEntry{}}
	im.l.DeadKeys[id] = dk
	return dk
}

func (im *importer) bindingOf(k klc.Key, col int, c klc.Cell) *model.Binding {
	switch c.Kind {
	case klc.CellChar:
		units := utf16.Encode([]rune{c.Rune})
		if c.Rune <= 0xFFFF {
			units = []uint16{uint16(c.Rune)}
		}
		return bindingOfUnits(units)
	case klc.CellDead:
		im.ensureDead(uint16(c.Rune))
		return &model.Binding{Kind: model.BindDeadKey, DeadKey: deadID(uint16(c.Rune))}
	case klc.CellLigature:
		for _, lg := range im.f.Ligatures {
			if lg.VK == k.VK && lg.Column == col {
				return bindingOfUnits(lg.Chars)
			}
		}
	}
	return nil
}

func bindingOfUnits(units []uint16) *model.Binding {
	t := textOrUnits(units)
	if t.Units != nil {
		return &model.Binding{Kind: model.BindUnits, Units: t.Units}
	}
	return model.TextBinding(t.Text)
}

// bindings fills the layers and the Caps Lock layers (§10 items 2, 3).
func (im *importer) bindings() {
	f := im.f
	altGr := *im.w.LocaleFlags.AltGr
	capsLayer := func(s int) *model.HardwareLayer {
		id := fmt.Sprintf("capsS%d", s)
		if layer := im.caps[id]; layer != nil {
			return layer
		}
		layer := &model.HardwareLayer{ID: id, Bindings: map[string]*model.Binding{}, Modifiers: im.setsOf(s, altGr, []string{"caps"})}
		im.caps[id] = layer
		return layer
	}
	colOf := map[int]int{}
	for i, s := range f.ShiftStates {
		colOf[s] = i
	}
	for _, k := range f.Keys {
		if !k.HasCells {
			continue
		}
		id := im.posOf[k.Scan]
		cellAt := func(s int) *model.Binding {
			c, ok := colOf[s]
			if !ok || c >= len(k.Cells) {
				return nil
			}
			return im.bindingOf(k, c, k.Cells[c])
		}
		for i, s := range f.ShiftStates {
			if i < len(k.Cells) {
				if b := im.bindingOf(k, i, k.Cells[i]); b != nil {
					im.layers[s].Bindings[id] = b
				}
			}
		}
		put := func(layerState, fromState int, b *model.Binding) {
			if b != nil {
				capsLayer(layerState).Bindings[id] = b
			}
			_ = fromState
		}
		// capsCell is the Caps Lock binding of state own when CAPLOK makes it read state from (windows-behaviour §5: Caps
		// Lock flips the Shift bit, then the cell of that column is used): a -1 there gives
		// nothing with Caps Lock (Windows reads the -1 cell), so where state own has output the binding is the
		// explicit nothing of D64, not the fallback to the state without Caps Lock (CLDR xct-Tibt KeyY).
		capsCell := func(from, own int) *model.Binding {
			if b := cellAt(from); b != nil {
				return b
			}
			if c, ok := colOf[from]; ok && c < len(k.Cells) && k.Cells[c].Kind == klc.CellNone && cellAt(own) != nil {
				return &model.Binding{Kind: model.BindOutput, Output: model.NewText("")}
			}
			return nil
		}
		if k.Cap&klc.CapCAPLOK != 0 {
			put(0, 1, capsCell(1, 0))
			put(1, 0, capsCell(0, 1))
		}
		if k.Cap&klc.CapSGCAPS != 0 {
			for i, c := range k.SGCap {
				b := im.bindingOf(k, i, c)
				if b == nil && c.Kind == klc.CellNone && cellAt(i) != nil {
					// -1 in the continuation line where the main row has output: Caps Lock gives nothing there (MSKLC
					// writes WCH_NONE, VM run of 2026-11-26, tmcaps), so the binding is the explicit nothing of D64,
					// not the fallback to the state without Caps Lock.
					b = &model.Binding{Kind: model.BindOutput, Output: model.NewText("")}
				}
				put(i, i, b)
			}
		}
		if k.Cap&klc.CapCAPLOKALTGR != 0 {
			put(6, 7, capsCell(7, 6))
			put(7, 6, capsCell(6, 7))
		}
	}
}

// implied writes the rows MSKLC adds to every layout (OBS D16; D36) where the file leaves the cells empty.
var impliedRows = []struct {
	vk    byte
	chars map[int]uint16 // shift state -> character
}{
	{0x08, map[int]uint16{0: 0x08, 1: 0x08, 2: 0x7F}},
	{0x1B, map[int]uint16{0: 0x1B, 1: 0x1B, 2: 0x1B}},
	{0x0D, map[int]uint16{0: 0x0D, 1: 0x0D, 2: 0x0A}},
	{0x03, map[int]uint16{0: 0x03, 1: 0x03, 2: 0x03}},
	{0x09, map[int]uint16{0: 0x09, 1: 0x09}},
	{0x6B, map[int]uint16{0: '+', 1: '+'}},
	{0x6F, map[int]uint16{0: '/', 1: '/'}},
	{0x6A, map[int]uint16{0: '*', 1: '*'}},
	{0x6D, map[int]uint16{0: '-', 1: '-'}},
	{0x60, map[int]uint16{0: '0'}}, {0x61, map[int]uint16{0: '1'}}, {0x62, map[int]uint16{0: '2'}},
	{0x63, map[int]uint16{0: '3'}}, {0x64, map[int]uint16{0: '4'}}, {0x65, map[int]uint16{0: '5'}},
	{0x66, map[int]uint16{0: '6'}}, {0x67, map[int]uint16{0: '7'}}, {0x68, map[int]uint16{0: '8'}},
	{0x69, map[int]uint16{0: '9'}},
}

// ImpliedRows exposes the implied rows to the exporter: VK -> shift state -> character.
func ImpliedRows() map[byte]map[int]uint16 {
	out := map[byte]map[int]uint16{}
	for _, r := range impliedRows {
		out[r.vk] = r.chars
	}
	return out
}

// positionForVK finds the position that types a VK: a row of the file (Num-Lock-on VKs through their key),
// else the Table A position.
func (im *importer) positionForVK(v byte) (string, bool) {
	for _, k := range im.f.Keys {
		if k.VK == v {
			return im.posOf[k.Scan], true
		}
	}
	want := v
	if off, ok := numpadOff[v]; ok {
		want = off
	}
	for _, d := range vk.Defaults {
		if byte(d.VK) == want && (want == v || d.VK&vk.KBDNUMPAD != 0) {
			if im.rowAt(d.Scan) && want == v {
				// a file row uses this scan code for another key
				continue
			}
			id := d.Position
			if have, ok := im.posOf[d.Scan]; ok {
				id = have // a position a key-name section made (KEYNAME 01 Esc): the row still applies to it
			} else if im.l.Positions[id] == nil {
				im.l.Positions[id] = &model.Position{ScanCode: scanText(d.Scan)}
				im.posOf[d.Scan] = id
			}
			return id, true
		}
	}
	return "", false
}

// rowAt says whether a LAYOUT row of the file has this scan code.
func (im *importer) rowAt(scan uint16) bool {
	for _, k := range im.f.Keys {
		if k.Scan == scan {
			return true
		}
	}
	return false
}

func (im *importer) implied() {
	var added []string
	for _, ir := range impliedRows {
		id, ok := im.positionForVK(ir.vk)
		if !ok {
			// No key of the file gives this VK (e0-used: E035 remapped): MSKLC writes the row anyway (VM run of
			// 2026-11-26), so it is an extra row of the layout.
			if er, ok := im.impliedExtraRow(ir.vk, ir.chars); ok {
				im.w.ExtraRows = append(im.w.ExtraRows, er)
				added = append(added, "extra row "+vk.Name(ir.vk))
			}
			continue
		}
		changed := false
		for s, ch := range ir.chars {
			layer := im.layers[s]
			if layer == nil {
				continue
			}
			if _, has := layer.Bindings[id]; !has {
				layer.Bindings[id] = model.TextBinding(string(rune(ch)))
				changed = true
			}
		}
		if changed {
			added = append(added, id)
		}
	}
	if len(added) > 0 {
		im.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Accepted, Code: "klc.impliedRows", Where: loss.Where{File: im.name},
			Message: fmt.Sprintf("the rows MSKLC adds (control characters, Tab, number pad) are written explicitly for %s", strings.Join(added, ", ")),
			Detail:  map[string]any{"positions": added}})
	}
}

func (im *importer) finishLayers() {
	hw := &model.Hardware{}
	for _, id := range im.order {
		hw.Layers = append(hw.Layers, im.byID[id])
	}
	for _, s := range []int{0, 1, 6, 7} {
		if layer := im.caps[fmt.Sprintf("capsS%d", s)]; layer != nil && len(layer.Bindings) > 0 {
			hw.Layers = append(hw.Layers, layer)
		}
	}
	if len(hw.Layers) > 0 {
		im.l.Hardware = hw
	}
	if len(im.l.DeadKeys) == 0 {
		im.l.DeadKeys = nil
	}
	if len(im.w.Keys) == 0 {
		im.w.Keys = nil
	}
}

// impliedExtraRow is an implied row for a VK no key of the file gives: its cells in SHIFTSTATE order, up to the
// last declared state with a character.
func (im *importer) impliedExtraRow(v byte, chars map[int]uint16) (model.ExtraRow, bool) {
	er := model.ExtraRow{VK: vk.Name(v)}
	last := -1
	for i, s := range im.f.ShiftStates {
		if ch, ok := chars[s]; ok {
			for len(er.Cells) < i {
				er.Cells = append(er.Cells, model.WinCell{})
			}
			er.Cells = append(er.Cells, model.WinCell{Kind: model.CellText, Text: string(rune(ch))})
			last = i
		}
	}
	return er, last >= 0
}

// IsImpliedExtraRow says whether an extra row is one the .klc import writes for an implied row (the exporter
// leaves it out: MSKLC adds it by itself).
func IsImpliedExtraRow(er model.ExtraRow, states []int) bool {
	for _, ir := range impliedRows {
		if vk.Name(ir.vk) != er.VK || len(er.Attributes) > 0 {
			continue
		}
		for i, c := range er.Cells {
			if i >= len(states) {
				return false
			}
			ch, ok := ir.chars[states[i]]
			switch {
			case c.Kind == model.CellText && ok && c.Text == string(rune(ch)):
			case c.Kind == model.CellNone && !ok:
			default:
				return false
			}
		}
		return true
	}
	return false
}
