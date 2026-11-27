// Package dllkbl converts an existing Windows keyboard-layout DLL into the format-neutral model (docs/dll-import.md).
//
// The import pins every Windows value the DLL states into targets.windows (keys, flags, modifiers, columns, table
// order, scan-code table size, version word, keyboard type), so that the Windows build of the result gives the
// same tables again; the general data (positions, layers, dead keys) says what the tables mean, by the
// conventions of the kbl specification's coverage/windows.md §10 (layer ids s<state>, dead keys u<hex>, positions
// by SPEC Appendix A or sc<hex>; coverage/windows.md §11, D53-D55). What the result cannot carry is reported
// with a dll.* loss kind.
package dllkbl

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// The loss kinds are the dll.* kinds of SPEC §17.3 (D55, D60, D61), with their default policies from package
// policy.

// Options of an import.
type Options struct {
	// LangID, if not empty, is pinned as targets.windows.registration.langId (4 hex digits).
	LangID string
	// KeepKeyboardType keeps dwType and dwSubType (coverage/windows.md §11 item 11: not kept unless asked for).
	KeepKeyboardType bool
}

// FromDLL reads the bytes of a layout DLL and converts them. name is the file name (it gives dllName and the
// provenance). The layout is nil when the file cannot be read.
func FromDLL(b []byte, name string, opt Options) (*model.Layout, *loss.Report) {
	im, r := windows.ReadImage(b)
	for i := range r.Entries {
		r.Entries[i].Where.File = name
	}
	if im == nil || r.HasErrors() {
		return nil, r
	}
	l, ir := FromImage(im, name, opt)
	r.Merge(ir)
	if r.HasErrors() {
		return nil, r
	}
	return l, r
}

// numpadOn maps a number-pad key's Num-Lock-off VK to its Num-Lock-on VK (windows-behaviour §3.2).
var numpadOn = map[byte]byte{0x24: 0x67, 0x26: 0x68, 0x21: 0x69, 0x25: 0x64, 0x0C: 0x65, 0x27: 0x66, 0x23: 0x61,
	0x28: 0x62, 0x22: 0x63, 0x2D: 0x60, 0x2E: 0x6E}

// bitComponents names the custom modifier of each extra bit (coverage/windows.md §10 item 2; 40 has no constant).
var bitComponents = map[byte]string{0x08: "kana", 0x10: "roya", 0x20: "loya", 0x40: "bit40", 0x80: "grpSelTap"}

var attrNames = []struct {
	name string
	bit  byte
}{{"CAPLOK", windows.CAPLOK}, {"SGCAPS", windows.SGCAPS}, {"CAPLOKALTGR", windows.CAPLOKALTGR}, {"KANALOK", windows.KANALOK}, {"GRPSELTAP", windows.GRPSELTAP}}

type position struct {
	id      string
	scan    uint16
	vk      byte
	rowVK   byte
	none    bool // pinned to _none_
	numLock bool
}

type importer struct {
	im   *windows.Image
	t    *windows.Tables
	name string
	r    *loss.Report
	l    *model.Layout
	w    *model.WindowsTarget

	pos       []*position
	posByID   map[string]*position
	posOf     map[uint16]*position
	modVK     map[byte]bool
	layers    []*model.HardwareLayer // column -> layer (nil: column not reachable)
	states    [][]int                // column -> states
	caps      map[string]*model.HardwareLayer
	capsIDs   []string
	altGr     bool
	comps     map[byte]string // bit -> component
	deadIDs   map[uint16]string
	lost      map[string][]string
	noName    bool
	kanaLayer *model.HardwareLayer
	langID    string
	lostKind  []string
}

func (im *importer) loss(kind, format string, args ...any) {
	if _, ok := im.lost[kind]; !ok {
		im.lostKind = append(im.lostKind, kind)
	}
	im.lost[kind] = append(im.lost[kind], fmt.Sprintf(format, args...))
}

// FromImage converts a DLL read by windows.ReadImage.
func FromImage(img *windows.Image, name string, opt Options) (*model.Layout, *loss.Report) {
	im := &importer{im: img, t: &img.DLL.Tables, name: name, r: &loss.Report{}, posByID: map[string]*position{},
		posOf: map[uint16]*position{}, modVK: map[byte]bool{}, caps: map[string]*model.HardwareLayer{},
		comps: map[byte]string{}, deadIDs: map[uint16]string{}, lost: map[string][]string{}}
	im.run(opt)
	for _, kind := range im.lostKind {
		items := im.lost[kind]
		msg := strings.Join(items, "; ")
		if len(items) > 6 {
			msg = strings.Join(items[:6], "; ") + fmt.Sprintf("; and %d more", len(items)-6)
		}
		im.r.Append(loss.Entry{Class: loss.Loss, Severity: severity(policy.Default(kind)), Code: kind,
			Where: loss.Where{File: name}, Message: msg, Detail: map[string]any{"items": items}})
	}
	return im.l, im.r
}

func severity(p string) loss.Severity {
	switch p {
	case "error", "fatal":
		return loss.Error
	case "accept":
		return loss.Accepted
	}
	return loss.Warning
}

var reDLLName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,7}$`)

func (im *importer) run(opt Options) {
	d := im.im.DLL
	l := &model.Layout{Kbl: model.FormatVersion, Positions: map[string]*model.Position{}}
	im.l = l
	w := &model.WindowsTarget{Keys: map[string]*model.WindowsKey{}}
	im.w = w
	l.Targets = &model.Targets{Windows: w}
	l.Metadata.ID = "und"
	l.Metadata.Provenance = &model.Provenance{Source: im.name, Format: "windowsDll",
		Notes: "imported from the " + im.im.Arch.String() + " build"}
	// dllName: the input file's name in lower case (§11 item 13; the export directory's name is not reliable).
	base := strings.ToLower(filepath.Base(im.name))
	base = strings.TrimSuffix(base, ".dll")
	if reDLLName.MatchString(base) {
		w.DLLName = base
	} else {
		im.noName = true
	}
	_ = d
	f := false
	w.UseTableA = &f // D54: the build holds exactly the DLL's scan codes
	im.meta(opt)
	im.flags()
	im.modifiers()
	im.positions()
	im.deadKeys()
	im.rows()
	im.keyNames()
	im.finish()
	if !l.OutputsNFC() { // Windows does not normalise: keep the DLL's code points
		if l.Settings == nil {
			l.Settings = &model.Settings{}
		}
		l.Settings.Normalization = "disabled"
	}
}

func (im *importer) meta(opt Options) {
	d, l, w := im.im.DLL, im.l, im.w
	st := &model.WindowsStrings{}
	var dropped []string
	for _, s := range d.Strings {
		lang := fmt.Sprintf("%04X", s.Lang)
		switch s.ID {
		case windows.StringLayoutName:
			if st.LayoutName == nil {
				st.LayoutName = map[string]string{}
			}
			st.LayoutName[lang] = s.Text
		case windows.StringLanguageName:
			if st.LanguageName == nil {
				st.LanguageName = map[string]string{}
			}
			st.LanguageName[lang] = s.Text
		case windows.StringLocaleName:
			if st.LocaleName == nil {
				v := s.Text
				st.LocaleName = &v
			} else if *st.LocaleName != s.Text {
				dropped = append(dropped, fmt.Sprintf("string 1200 in language %s", lang))
			}
		default:
			dropped = append(dropped, fmt.Sprintf("string %d", s.ID))
		}
	}
	if st.LayoutName != nil || st.LanguageName != nil || st.LocaleName != nil {
		w.Strings = st
	}
	if st.LocaleName != nil && regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`).MatchString(*st.LocaleName) {
		l.Metadata.ID = *st.LocaleName
	}
	if n, ok := st.LayoutName[strings.ToUpper(opt.LangID)]; ok {
		l.Metadata.Name = n
	} else if len(st.LayoutName) > 0 {
		keys := sortedKeys(st.LayoutName)
		l.Metadata.Name = st.LayoutName[keys[0]]
	}
	if v := d.Version; v != nil {
		vi := &model.VersionInfo{FileVersion: fmt.Sprintf("%d.%d.%d.%d", v.FileVersion[0], v.FileVersion[1], v.FileVersion[2], v.FileVersion[3]),
			ProductVersion: fmt.Sprintf("%d.%d.%d.%d", v.ProductVersion[0], v.ProductVersion[1], v.ProductVersion[2], v.ProductVersion[3])}
		for _, kv := range v.Strings {
			switch kv.Key {
			case "FileDescription":
				s := kv.Value
				vi.FileDescription = &s
				if l.Metadata.Name == "" {
					l.Metadata.Name = s
				}
			case "LegalCopyright":
				l.Metadata.Copyright = kv.Value
				s := kv.Value
				vi.LegalCopyright = &s
			case "CompanyName":
				// D58: the original vendor is an attribution, never the company of a file yakbd builds (dll §1.6)
				l.Metadata.Attribution = kv.Value
			case "FileVersion", "ProductVersion":
				// the fixed numbers are carried
			default:
				dropped = append(dropped, "version string "+kv.Key)
			}
		}
		w.VersionInfo = vi
	}
	if l.Metadata.Name == "" {
		l.Metadata.Name = strings.TrimSuffix(filepath.Base(im.name), filepath.Ext(im.name))
	}
	dropped = append(dropped, im.im.OtherResources...)
	if len(dropped) > 0 {
		im.loss("dll.resourceDropped", "%s", strings.Join(dropped, ", "))
	}
	if len(im.im.Container) > 0 {
		im.loss("dll.containerIgnored", "%s", strings.Join(im.im.Container, ", "))
	}
	for _, o := range im.im.Exports {
		if o == 3 || o == 5 || o == 6 {
			im.loss("dll.dispatcherNotRead", "export ordinal %d is code that names other layout files; it is not read (dll-format §6.3)", o)
		}
	}
	switch {
	case im.im.ShortForm || im.im.KeyboardTypeUntrusted:
		im.loss("dll.keyboardTypeNotTrusted", "dwType and dwSubType are not kept (a short KBDTABLES or implausible values, dll-format §3)")
	case opt.KeepKeyboardType:
		w.KeyboardType = &model.KeyboardType{Type: im.t.Type, SubType: im.t.SubType}
	case im.t.Type != 0 || im.t.SubType != 0:
		im.loss("dll.keyboardTypeNotTrusted", "dwType %d and dwSubType %d are not kept (dll-format §3; -keep-keyboard-type keeps them)", im.t.Type, im.t.SubType)
	}
	missing := []string{"klid", "layoutId", "layoutText", "productCode", "customLanguageName"}
	if im.noName {
		missing = append(missing, "dllName")
	}
	if opt.LangID != "" {
		w.Registration = &model.Registration{LangID: strings.ToUpper(opt.LangID)}
	} else {
		missing = append([]string{"langId"}, missing...)
	}
	im.loss("dll.registrationMissing", "a DLL holds no registration data: %s are not set", strings.Join(missing, ", "))
	if n := im.im.NLS; n != nil {
		oem, info := int(n.OEMIdentifier), int(n.LayoutInformation)
		nls := &model.NLS{OEMIdentifier: &oem, LayoutInformation: &info}
		for _, f := range n.VkToF {
			e := model.NLSVkToF{VK: vk.Name(f.VK), ProcType: int(f.ProcType), ProcCurrent: int(f.ProcCurrent), ProcSwitch: int(f.ProcSwitch)}
			for k := 0; k < 8; k++ {
				e.Proc = append(e.Proc, model.NLSParam{Index: int(f.Proc[k].Index), Param: f.Proc[k].Param})
				e.ProcAlt = append(e.ProcAlt, model.NLSParam{Index: int(f.ProcAlt[k].Index), Param: f.ProcAlt[k].Param})
			}
			nls.VkToF = append(nls.VkToF, e)
		}
		for _, m := range n.MouseVKeys {
			nls.MouseVKeys = append(nls.MouseVKeys, model.NLSMouse{VK: vk.Name(byte(m)), Flags: vk.FlagList(m & 0xFF00)})
		}
		w.NLS = nls
	}
	ver := int(im.t.Version)
	w.KbdVersion = &ver
	size := len(im.t.ScanToVK)
	w.ScanCodeTableSize = &size
}

func (im *importer) flags() {
	f := im.t.LocaleFlags
	altGr, shiftLock, lrm := f&windows.KLLF_ALTGR != 0, f&windows.KLLF_SHIFTLOCK != 0, f&windows.KLLF_LRM_RLM != 0
	im.altGr = altGr
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
	if rest := f &^ (windows.KLLF_ALTGR | windows.KLLF_SHIFTLOCK | windows.KLLF_LRM_RLM); rest != 0 {
		im.loss("dll.unknownFlags", "locale flag bits %#04x without a name", rest)
	}
}

func single(b byte) bool { return b != 0 && b&(b-1) == 0 }

// modifiers pins VK_TO_BIT and ModNumber as they are and makes one layer per reachable column.
func (im *importer) modifiers() {
	m := im.t.Modifiers
	mods := &model.WindowsModifiers{}
	for _, b := range []byte{windows.KBDSHIFT, windows.KBDCTRL, windows.KBDALT} {
		im.comps[b] = map[byte]string{windows.KBDSHIFT: "shift", windows.KBDCTRL: "ctrl", windows.KBDALT: "alt"}[b]
	}
	for _, e := range m.VKToBit {
		im.modVK[e.VK] = true
		if e.Bits == 0 {
			im.loss("dll.unreachableData", "VK_TO_BIT %s sets no bit", vk.Name(e.VK))
			continue
		}
		if !single(e.Bits) {
			// D61: an error (the file would build different columns)
			im.loss("dll.modifierDropped", "VK_TO_BIT %s -> %#02x sets several bits", vk.Name(e.VK), e.Bits)
			continue
		}
		comp := im.comps[e.Bits]
		if comp == "" {
			comp = bitComponents[e.Bits]
			im.comps[e.Bits] = comp
		}
		mods.Bits = append(mods.Bits, model.ModifierBit{VK: vk.Name(e.VK), Bit: int(e.Bits), Component: comp})
	}
	if len(mods.Bits) == 0 {
		// The schema wants at least one entry; an empty VK_TO_BIT reaches column ModNumber[0] only.
		mods.Bits = []model.ModifierBit{{VK: "SHIFT", Bit: 1, Component: "shift"}}
		im.loss("dll.unreachableData", "VK_TO_BIT is empty; the pin adds SHIFT -> 1")
	}
	cols := 0
	for _, c := range m.ModNumber {
		if c != windows.SHFT_INVALID && int(c)+1 > cols {
			cols = int(c) + 1
		}
	}
	if cols > windows.MaxColumns {
		cols = windows.MaxColumns
	}
	var owned byte
	for _, e := range m.VKToBit {
		owned |= e.Bits
	}
	mods.ModNumber = make([]*int, len(m.ModNumber))
	im.states = make([][]int, cols)
	for s, c := range m.ModNumber {
		if c == windows.SHFT_INVALID {
			continue
		}
		if byte(s)&^owned != 0 {
			// No key makes this state (OBS D12: kbdutool numbers MODIFIERS entries by position, whatever bit they
			// name); a build refuses it (win.modnumber.bits), so it is written as invalid.
			im.loss("dll.unreachableColumn", "ModNumber[%d] = %d: state %#x has a modifier bit no VK_TO_BIT entry names; written as invalid", s, c, s)
			continue
		}
		if int(c) >= windows.MaxColumns {
			im.loss("dll.unreachableColumn", "ModNumber[%d] = %d: Windows has 15 columns; the state is written as invalid", s, c)
			continue
		}
		v := int(c)
		mods.ModNumber[s] = &v
		im.states[c] = append(im.states[c], s)
	}
	im.w.Modifiers = mods
	im.layers = make([]*model.HardwareLayer, cols)
	for c, states := range im.states {
		var sets []model.ModifierSet
		for _, s := range states {
			ss, ok := im.setsOf(s, nil)
			if !ok {
				continue
			}
			sets = append(sets, ss...)
		}
		if len(sets) == 0 {
			if len(states) > 0 {
				im.loss("dll.unreachableColumn", "column %d: state %#x has a modifier bit no VK_TO_BIT entry names", c, states[0])
			}
			continue
		}
		im.layers[c] = &model.HardwareLayer{ID: fmt.Sprintf("s%d", states[0]), Modifiers: sets, Bindings: map[string]*model.Binding{}}
	}
	// Custom modifiers: the positions with the bit's VK (found later), or a position of their own.
	for _, mb := range mods.Bits {
		if mb.Bit < 8 {
			continue
		}
		if im.l.CustomModifiers == nil {
			im.l.CustomModifiers = map[string]*model.CustomModifier{}
		}
		if im.l.CustomModifiers[mb.Component] == nil {
			im.l.CustomModifiers[mb.Component] = &model.CustomModifier{Kind: "held"}
		}
	}
}

// setsOf returns the modifier sets of a Windows state (coverage/windows.md §10 item 2); extra components (caps)
// are added to each. ok is false for a state with a bit no component names.
func (im *importer) setsOf(s int, extra []string) ([]model.ModifierSet, bool) {
	var custom []string
	for _, b := range []byte{0x08, 0x10, 0x20, 0x40, 0x80} {
		if s&int(b) == 0 {
			continue
		}
		comp, ok := im.comps[b]
		if !ok {
			return nil, false
		}
		custom = append(custom, comp)
	}
	var base []string
	if s&1 != 0 {
		base = append(base, "shift")
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
	case s&6 == 6 && im.altGr:
		return []model.ModifierSet{mk("altR"), mk("ctrl", "alt")}, true
	case s&6 == 6:
		return []model.ModifierSet{mk("ctrl", "alt")}, true
	case s&2 != 0:
		return []model.ModifierSet{mk("ctrl")}, true
	case s&4 != 0 && im.altGr:
		return []model.ModifierSet{mk("altL")}, true
	case s&4 != 0:
		return []model.ModifierSet{mk("alt")}, true
	}
	return []model.ModifierSet{mk()}, true
}

func scanText(sc uint16) string {
	if sc>>8 != 0 {
		return fmt.Sprintf("%04X", sc)
	}
	return fmt.Sprintf("%02X", sc)
}

// addPos creates the position of a scan code with its pinned VK and flags.
func (im *importer) addPos(sc uint16, v uint16) *position {
	id := klckbl.PositionID(sc)
	p := &position{id: id, scan: sc, vk: byte(v), rowVK: byte(v)}
	p.none = p.vk == vk.None
	flags := v & 0xFF00
	if flags&vk.KBDNUMPAD != 0 {
		if on, ok := numpadOn[p.vk]; ok {
			p.numLock, p.rowVK = true, on
		}
	}
	pos := &model.Position{ScanCode: scanText(sc)}
	if p.numLock {
		nl := p.numLock
		pos.NumLock = &nl
	}
	im.l.Positions[id] = pos
	im.w.Keys[id] = &model.WindowsKey{VK: vk.Name(p.vk), ScanCodeFlags: vk.FlagList(flags)}
	im.pos = append(im.pos, p)
	im.posByID[id] = p
	im.posOf[sc] = p
	return p
}

// positions creates a position for every scan-code entry, and pins _none_ on the scan codes of Table A rows the
// DLL does not have (targets/windows.md §5 would add them).
func (im *importer) positions() {
	t := im.t
	for i, v := range t.ScanToVK {
		sc := uint16(i)
		if v == uint16(vk.None) {
			continue
		}
		im.addPos(sc, v)
	}
	for _, tbl := range []struct {
		prefix uint16
		list   []windows.ScanVK
	}{{0xE000, t.E0}, {0xE100, t.E1}} {
		for _, e := range tbl.list {
			sc := tbl.prefix | uint16(e.Scan)
			if im.posOf[sc] != nil {
				im.loss("dll.unreachableData", "a second entry for scan code %s", scanText(sc))
				continue
			}
			im.addPos(sc, e.VK)
		}
	}
	// Custom modifiers get the positions of their VKs.
	for _, mb := range im.w.Modifiers.Bits {
		cm := im.l.CustomModifiers[mb.Component]
		if cm == nil {
			continue
		}
		v, _ := vk.Parse(mb.VK)
		for _, p := range im.pos {
			if p.vk == v && !p.none && !contains(cm.Positions, p.id) {
				cm.Positions = append(cm.Positions, p.id)
			}
		}
	}
	for _, name := range sortedKeys(im.l.CustomModifiers) {
		cm := im.l.CustomModifiers[name]
		if len(cm.Positions) > 0 {
			continue
		}
		var v string
		for _, mb := range im.w.Modifiers.Bits {
			if mb.Component == name {
				v = mb.VK
				break
			}
		}
		pos := "vk" + v
		im.l.Positions[pos] = &model.Position{Description: "modifier key " + v + " of VK_TO_BIT (no scan code gives it)"}
		im.w.Keys[pos] = &model.WindowsKey{VK: v}
		cm.Positions = []string{pos}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func deadID(u uint16) string { return fmt.Sprintf("u%04x", u) }

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

func textOrUnits(units []uint16) model.TextOrUnits {
	if validText(units) {
		return model.TextOrUnits{Text: string(utf16.Decode(units))}
	}
	return model.TextOrUnits{Units: units}
}

func value(units []uint16) model.Value {
	t := textOrUnits(units)
	if t.Units != nil {
		return model.Value{Kind: model.ValueUnits, Units: t.Units}
	}
	return model.Value{Kind: model.ValueText, Text: t.Text}
}

func bindingOf(units []uint16) *model.Binding {
	t := textOrUnits(units)
	if t.Units != nil {
		return &model.Binding{Kind: model.BindUnits, Units: t.Units}
	}
	return model.TextBinding(t.Text)
}

func (im *importer) ensureDead(u uint16) string {
	id := deadID(u)
	if im.l.DeadKeys == nil {
		im.l.DeadKeys = map[string]*model.DeadKey{}
	}
	if im.l.DeadKeys[id] == nil {
		im.l.DeadKeys[id] = &model.DeadKey{Spacing: textOrUnits([]uint16{u}), Entries: []model.DeadKeyEntry{}}
		im.deadIDs[u] = id
	}
	return id
}

// deadKeys creates one dead key per dead character (coverage/windows.md §10 item 4), entries in file order; the
// first of two entries for one (dead, base) is the one Windows uses (dll-format §4.3).
func (im *importer) deadKeys() {
	seen := map[uint32]bool{}
	flagsDropped := 0
	for _, e := range im.t.DeadKeys {
		k := uint32(e.Dead)<<16 | uint32(e.Base)
		if seen[k] {
			im.loss("dll.unreachableData", "a second DEADKEY entry for dead %04X and base %04X", e.Dead, e.Base)
			continue
		}
		seen[k] = true
		dk := im.l.DeadKeys[im.ensureDead(e.Dead)]
		entry := model.DeadKeyEntry{Base: value([]uint16{e.Base})}
		if e.Flags&windows.DKF_DEAD != 0 {
			entry.Result = model.Value{Kind: model.ValueDeadKey, DeadKey: im.ensureDead(e.Composed)}
		} else {
			entry.Result = value([]uint16{e.Composed})
		}
		if e.Flags&^windows.DKF_DEAD != 0 {
			flagsDropped++
		}
		dk.Entries = append(dk.Entries, entry)
	}
	_ = flagsDropped // not kept and not reported (coverage/windows.md §5, §11 item 9)
	seenName := map[uint16]bool{}
	for _, n := range im.t.KeyNamesDead {
		if seenName[n.Dead] {
			im.loss("dll.unreachableData", "a second name for dead character %04X", n.Dead)
			continue
		}
		seenName[n.Dead] = true
		if n.Name == "" {
			im.loss("dll.unreachableData", "an empty name for dead character %04X", n.Dead)
			continue
		}
		im.l.DeadKeys[im.ensureDead(n.Dead)].Name = n.Name
	}
}

// mainRow is a key row with the row that follows it when Windows reads that one too.
type mainRow struct {
	table int
	row   *windows.CharRow
	next  *windows.CharRow // dead-character row and/or SGCAPS row
	// capsDead is the row after an SGCAPS key's Caps Lock row when that row has dead keys (writer rule W-2, D63)
	capsDead *windows.CharRow
	width    int
}

func (im *importer) ligature(v byte, col int) ([]uint16, bool) {
	for _, lg := range im.t.Ligatures {
		if lg.VK == v && int(lg.Column) == col {
			return lg.Chars, true
		}
	}
	return nil, false
}

// cell is a decoded character cell.
type cell struct {
	units []uint16
	dead  string
}

func (c cell) empty() bool { return c.units == nil && c.dead == "" }

func (im *importer) cellOf(mr mainRow, col int, usedLig map[[2]int]bool) cell {
	r := mr.row
	switch u := r.Chars[col]; u {
	case windows.WCH_NONE:
		return cell{}
	case windows.WCH_DEAD:
		if mr.next == nil {
			im.r.Errorf("dll.malformed", loss.Where{File: im.name}, "%s column %d is a dead key but no row follows it (coverage/windows.md §11)", vk.Name(r.VK), col)
			return cell{}
		}
		return cell{dead: im.ensureDead(mr.next.Chars[col])}
	case windows.WCH_LGTR:
		chars, ok := im.ligature(r.VK, col)
		if !ok {
			im.loss("dll.ligatureMismatch", "%s column %d refers to a ligature the table does not have", vk.Name(r.VK), col)
			return cell{}
		}
		usedLig[[2]int{int(r.VK), col}] = true
		if len(chars) == 0 {
			im.loss("dll.ligatureMismatch", "%s column %d: the ligature is empty", vk.Name(r.VK), col)
			return cell{}
		}
		return cell{units: chars}
	default:
		return cell{units: []uint16{u}}
	}
}

func (c cell) binding() *model.Binding {
	if c.dead != "" {
		return &model.Binding{Kind: model.BindDeadKey, DeadKey: c.dead}
	}
	return bindingOf(c.units)
}

func (c cell) winCell() model.WinCell {
	switch {
	case c.dead != "":
		return model.WinCell{Kind: model.CellDeadKey, DeadKey: c.dead}
	case c.units == nil:
		return model.WinCell{}
	case validText(c.units):
		return model.WinCell{Kind: model.CellText, Text: string(utf16.Decode(c.units))}
	}
	return model.WinCell{Kind: model.CellUnits, Units: c.units}
}

func isModifierVK(v byte) bool {
	return v >= 0x10 && v <= 0x12 || v >= 0xA0 && v <= 0xA5
}

// rows walks the character tables in order: every row Windows reaches becomes the bindings of the positions with
// its VK, or an extra row; the tables are pinned as they are.
func (im *importer) rows() {
	t := im.t
	var mains []mainRow
	seenVK := map[byte]bool{}
	for ti := range t.CharTables {
		ct := &t.CharTables[ti]
		if len(ct.Rows) == 0 {
			im.loss("dll.unreachableData", "table %d (width %d) has no rows", ti, ct.Width)
			continue
		}
		if ct.Width > windows.MaxColumns {
			im.loss("dll.unreachableData", "table %d has width %d; Windows has 15 columns: the table is cut to 15", ti, ct.Width)
		}
		for k := 0; k < len(ct.Rows); k++ {
			r := &ct.Rows[k]
			var next *windows.CharRow
			if k+1 < len(ct.Rows) {
				next = &ct.Rows[k+1]
			}
			hasDead := false
			for _, c := range r.Chars {
				hasDead = hasDead || c == windows.WCH_DEAD
			}
			mr := mainRow{table: ti, row: r, width: min(ct.Width, windows.MaxColumns)}
			if hasDead || r.Attributes&windows.SGCAPS != 0 {
				if next != nil {
					mr.next = next
					k++ // Windows reads the next row as this key's dead-character or Caps Lock row
					if hasDead && next.VK != 0xFF {
						im.loss("dll.unusualShape", "the dead-character row of %s has VK %s, not FF; it is read as dead characters only", vk.Name(r.VK), vk.Name(next.VK))
					}
					capsHasDead := false
					for _, c := range next.Chars {
						capsHasDead = capsHasDead || c == windows.WCH_DEAD
					}
					if !hasDead && capsHasDead {
						// W-2 (D63, VM-4): the dead characters of the Caps Lock row are in the row after it
						if k+1 < len(ct.Rows) {
							mr.capsDead = &ct.Rows[k+1]
							k++
						} else {
							im.r.Errorf("dll.malformed", loss.Where{File: im.name}, "the Caps Lock row of %s has a dead key but no row follows it (coverage/windows.md §11)", vk.Name(r.VK))
						}
					}
				} else if r.Attributes&windows.SGCAPS != 0 && !hasDead {
					im.loss("dll.unusualShape", "the SGCAPS row %s is the last of its table: no Caps Lock row follows", vk.Name(r.VK))
				}
			}
			if r.VK == 0xFF {
				im.loss("dll.unreachableData", "an FF row that follows no dead key in table %d", ti)
				continue
			}
			if seenVK[r.VK] {
				im.loss("dll.duplicateRow", "a second row for %s (Windows uses the first)", vk.Name(r.VK))
				continue
			}
			seenVK[r.VK] = true
			mains = append(mains, mr)
		}
	}
	// Which positions take each row's VK.
	byRowVK := map[byte][]*position{}
	for _, p := range im.pos {
		if !p.none {
			byRowVK[p.rowVK] = append(byRowVK[p.rowVK], p)
		}
	}
	for _, ps := range byRowVK {
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].scan < ps[j].scan })
	}
	usedLig := map[[2]int]bool{}
	tables := make([]model.WindowsTable, len(t.CharTables))
	for ti := range t.CharTables {
		tables[ti] = model.WindowsTable{Width: min(t.CharTables[ti].Width, windows.MaxColumns), Rows: []model.TableRow{}}
	}
	for _, mr := range mains {
		r := mr.row
		cells := make([]cell, mr.width)
		for c := range cells {
			cells[c] = im.cellOf(mr, c, usedLig)
			if c >= len(im.layers) || im.layers[c] == nil {
				if !cells[c].empty() {
					im.loss("dll.unreachableColumn", "%s column %d: no modifier state reaches the column", vk.Name(r.VK), c)
				}
				cells[c] = cell{}
			}
		}
		for c := mr.width; c < len(r.Chars); c++ {
			if r.Chars[c] != windows.WCH_NONE {
				im.loss("dll.unreachableData", "%s column %d is beyond 15 columns", vk.Name(r.VK), c)
			}
		}
		if mr.next != nil {
			for c, u := range mr.next.Chars {
				dead := c < len(r.Chars) && r.Chars[c] == windows.WCH_DEAD
				sg := r.Attributes&windows.SGCAPS != 0 && c < 2 // D65: Caps Lock cells above column 1 are never read (wb §5)
				if u != windows.WCH_NONE && !dead && !sg {
					im.loss("dll.unreachableData", "%s: the row after it has %04X in column %d, which Windows does not read", vk.Name(r.VK), u, c)
				}
			}
		}
		attrs := im.attrs(r)
		ps := byRowVK[r.VK]
		modifierRow := isModifierVK(r.VK) || im.modVK[r.VK]
		for _, p := range ps {
			if im.isCustomModifierPos(p.id) {
				modifierRow = true
			}
		}
		// A row that gives nothing at all is no binding of any position: it is kept as an extra row.
		silent := true
		for _, c := range cells {
			silent = silent && c.empty()
		}
		if r.Attributes&windows.SGCAPS != 0 && mr.next != nil {
			for c := 0; c < 2 && c < len(mr.next.Chars); c++ {
				silent = silent && mr.next.Chars[c] == windows.WCH_NONE
			}
		}
		if len(ps) == 0 || modifierRow || silent {
			er := model.ExtraRow{VK: vk.Name(r.VK), Attributes: attrs}
			for _, c := range cells {
				er.Cells = append(er.Cells, c.winCell())
			}
			if r.Attributes&windows.SGCAPS != 0 && mr.next != nil {
				for c, u := range mr.next.Chars {
					if u != windows.WCH_NONE && !(c < len(r.Chars) && r.Chars[c] == windows.WCH_DEAD) {
						im.loss("dll.capsCellUnexpressed", "the Caps Lock row of the extra row %s is not carried", vk.Name(r.VK))
						break
					}
				}
			}
			im.w.ExtraRows = append(im.w.ExtraRows, er)
			tables[mr.table].Rows = append(tables[mr.table].Rows, model.TableRow{VK: vk.Name(r.VK)})
			continue
		}
		for _, p := range ps {
			for c, cl := range cells {
				if !cl.empty() {
					im.layers[c].Bindings[p.id] = cl.binding()
				}
			}
			im.w.Keys[p.id].Attributes = attrs
			im.capsBindings(p, r, mr.next, mr.capsDead, cells)
			if r.Attributes&windows.KANALOK != 0 {
				im.kanaLock(p, cells)
			}
		}
		tables[mr.table].Rows = append(tables[mr.table].Rows, model.TableRow{Position: ps[0].id})
	}
	for _, lg := range t.Ligatures {
		if !usedLig[[2]int{int(lg.VK), int(lg.Column)}] {
			im.loss("dll.ligatureMismatch", "the ligature of %s column %d is not used by any cell (or is a second one)", vk.Name(lg.VK), lg.Column)
		}
	}
	for _, tb := range tables {
		if len(tb.Rows) > 0 {
			im.w.Tables = append(im.w.Tables, tb)
		}
	}
}

func (im *importer) isCustomModifierPos(id string) bool {
	for _, cm := range im.l.CustomModifiers {
		if contains(cm.Positions, id) {
			return true
		}
	}
	return false
}

func (im *importer) attrs(r *windows.CharRow) []string {
	out := []string{}
	known := byte(0)
	for _, a := range attrNames {
		known |= a.bit
		if r.Attributes&a.bit != 0 {
			out = append(out, a.name)
		}
	}
	if r.Attributes&^known != 0 {
		im.loss("dll.unknownFlags", "%s has attribute bits %#02x without a name", vk.Name(r.VK), r.Attributes&^known)
	}
	return out
}

// capsLayer returns the Caps Lock layer of state s.
func (im *importer) capsLayer(s int) *model.HardwareLayer {
	id := fmt.Sprintf("capsS%d", s)
	if layer := im.caps[id]; layer != nil {
		return layer
	}
	sets, _ := im.setsOf(s, []string{"caps"})
	layer := &model.HardwareLayer{ID: id, Modifiers: sets, Bindings: map[string]*model.Binding{}}
	im.caps[id] = layer
	im.capsIDs = append(im.capsIDs, id)
	return layer
}

func (im *importer) colOf(s int) (int, bool) {
	mn := im.t.Modifiers.ModNumber
	if s >= len(mn) || mn[s] == windows.SHFT_INVALID || int(mn[s]) >= len(im.layers) || im.layers[mn[s]] == nil {
		return 0, false
	}
	return int(mn[s]), true
}

// capsBindings fills the Caps Lock layers of a key (coverage/windows.md §10 item 3).
func (im *importer) capsBindings(p *position, r, next, capsDead *windows.CharRow, cells []cell) {
	at := func(s int) cell {
		if c, ok := im.colOf(s); ok && c < len(cells) {
			return cells[c]
		}
		return cell{}
	}
	// put binds the key in the Caps Lock layer of state s. Where Caps Lock gives nothing but the state without it
	// gives something, the binding is the explicit nothing {"output": ""} (D64, VM-7), which stops the fallback.
	put := func(s int, c cell) {
		if _, ok := im.colOf(s); !ok {
			return
		}
		switch {
		case !c.empty():
			im.capsLayer(s).Bindings[p.id] = c.binding()
		case !at(s).empty():
			im.capsLayer(s).Bindings[p.id] = &model.Binding{Kind: model.BindOutput, Output: model.NewText("")}
		}
	}
	if r.Attributes&windows.CAPLOK != 0 && r.Attributes&windows.SGCAPS == 0 {
		put(0, at(1))
		put(1, at(0))
	}
	if r.Attributes&windows.SGCAPS != 0 && next != nil {
		for i := 0; i < 2; i++ {
			c, ok := im.colOf(i)
			if !ok || c >= len(next.Chars) {
				continue
			}
			var cl cell
			switch {
			case c < len(r.Chars) && r.Chars[c] == windows.WCH_DEAD:
				cl = cells[c] // the shipped shape: the one FF row is also the Caps Lock row (dll-format §4.2)
			case next.Chars[c] == windows.WCH_DEAD && capsDead != nil && c < len(capsDead.Chars):
				cl = cell{dead: im.ensureDead(capsDead.Chars[c])} // W-2
			case next.Chars[c] != windows.WCH_NONE:
				cl = cell{units: []uint16{next.Chars[c]}}
			}
			put(i, cl)
		}
	}
	if r.Attributes&windows.CAPLOKALTGR != 0 {
		put(6, at(7))
		put(7, at(6))
	}
}

// keyNames writes the key names into the positions (a position of its own for a named scan code without one).
func (im *importer) keyNames() {
	for _, tbl := range []struct {
		prefix uint16
		list   []windows.KeyName
	}{{0, im.t.KeyNames}, {0xE000, im.t.KeyNamesExt}} {
		seen := map[uint16]bool{}
		for _, kn := range tbl.list {
			sc := tbl.prefix | uint16(kn.Scan)
			if seen[sc] {
				im.loss("dll.unreachableData", "a second name for scan code %s", scanText(sc))
				continue
			}
			seen[sc] = true
			if kn.Name == "" {
				im.loss("dll.unreachableData", "an empty name for scan code %s", scanText(sc))
				continue
			}
			p := im.posOf[sc]
			if p == nil {
				id := klckbl.PositionID(sc)
				im.l.Positions[id] = &model.Position{ScanCode: scanText(sc)}
				p = &position{id: id, scan: sc, none: true}
				im.posOf[sc] = p
			}
			im.l.Positions[p.id].Name = kn.Name
		}
	}
}

// kanaLock gives the Kana-lock layer the Kana column's cell of a KANALOK row (coverage/windows.md §11 item 5): a
// custom lock kanaLock on the position with VK KANA, pinned as modifiers.kanaLockComponent.
func (im *importer) kanaLock(p *position, cells []cell) {
	c, ok := im.colOf(windows.KBDKANA)
	if !ok || c >= len(cells) {
		return
	}
	if im.kanaLayer == nil {
		const comp = "kanaLock"
		var ps []string
		for _, q := range im.pos {
			if q.vk == 0x15 && !q.none {
				ps = append(ps, q.id)
			}
		}
		if len(ps) == 0 {
			ps = []string{"vkKANA"}
			im.l.Positions["vkKANA"] = &model.Position{Description: "the Kana key (no scan code of the DLL gives it)"}
			im.w.Keys["vkKANA"] = &model.WindowsKey{VK: "KANA"}
		}
		if im.l.CustomModifiers == nil {
			im.l.CustomModifiers = map[string]*model.CustomModifier{}
		}
		im.l.CustomModifiers[comp] = &model.CustomModifier{Kind: "lock", Positions: ps}
		im.w.Modifiers.KanaLockComponent = comp
		im.kanaLayer = &model.HardwareLayer{ID: comp, Modifiers: []model.ModifierSet{{comp}}, Bindings: map[string]*model.Binding{}}
	}
	if !cells[c].empty() {
		im.kanaLayer.Bindings[p.id] = cells[c].binding()
	}
}

func (im *importer) finish() {
	hw := &model.Hardware{}
	for _, layer := range im.layers {
		if layer != nil {
			hw.Layers = append(hw.Layers, layer)
		}
	}
	ids := append([]string(nil), im.capsIDs...)
	sort.Slice(ids, func(i, j int) bool {
		var a, c int
		fmt.Sscanf(ids[i], "capsS%d", &a)
		fmt.Sscanf(ids[j], "capsS%d", &c)
		return a < c
	})
	for _, id := range ids {
		if layer := im.caps[id]; layer != nil && len(layer.Bindings) > 0 {
			hw.Layers = append(hw.Layers, layer)
		}
	}
	if im.kanaLayer != nil {
		hw.Layers = append(hw.Layers, im.kanaLayer)
	}
	if len(hw.Layers) > 0 {
		im.l.Hardware = hw
	}
	for _, p := range im.pos {
		if p.none {
			im.w.Keys[p.id].Attributes = nil
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
