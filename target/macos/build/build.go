// Package build turns a format-neutral layout (model.Layout) into a macOS keyboard layout (format/keylayout) by the
// pins of targets.macos and, where the file is silent, the default rules of the kbl specification's
// targets/macos.md. Every value a default rule decides is reported as a `macosDefault.<rule>` entry and kept in
// Result.Pins, so that `pin` can write it into the file. Everything a .keylayout cannot express is reported with the
// loss kinds of SPEC §17.3.
package build

import (
	"fmt"
	"github.com/yakbd/yakbd-cli/target/postable"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target/macos/keycode"
)

// Options of a build.
type Options struct {
	// Policy, if set, is applied after the file's onLoss policies (for example the command line's -strict-defaults).
	Policy *model.OnLoss
}

// States is the number of build states: Shift 1, Option 2, Control 4, Command 8, Caps Lock 16 (targets/macos.md §3).
const States = 32

// The bits of a build state.
const (
	StShift   = 1
	StOption  = 2
	StControl = 4
	StCommand = 8
	StCaps    = 16
)

// CellKind is what a key does in one state.
type CellKind int

const (
	Nothing         CellKind = iota // no binding: nothing
	ExplicitNothing                 // {"output": ""}: nothing, and standardKeys leaves it alone
	Text                            // the key types Text
	Dead                            // the key is the dead key Dead
)

// Cell is one key in one state.
type Cell struct {
	Kind CellKind
	Text string
	Dead string // a dead-key id
}

// Table is one state's keys by key code; Nothing cells are left out.
type Table map[int]Cell

// Result is a built layout.
type Result struct {
	Keyboard *keylayout.Keyboard
	// Pins holds every value of targets.macos, pinned by the file or decided by a rule.
	Pins *model.MacOSTarget
	// AddedPositions are the table positions the layout gets through standardKeys that the file lacks.
	AddedPositions map[string]*model.Position
	// Tables are the 32 states' key tables before the dead-key actions (for checks and the importer).
	Tables [States]Table
	// States maps each dead-key id to its state name.
	States map[string]string
	// Codes maps each position with a key code to it.
	Codes map[string]int
	// Icon is the decoded .icns file, nil for none.
	Icon []byte
}

// Build builds l. The result is nil when the report has errors after the file's onLoss policies are applied.
func Build(l *model.Layout, opt Options) (*Result, *loss.Report) {
	b := &builder{l: l, r: &loss.Report{}}
	b.file = &model.MacOSTarget{}
	if l.Targets != nil && l.Targets.MacOS != nil {
		b.file = l.Targets.MacOS
	}
	b.pin = &model.MacOSTarget{}
	b.norm = l.Normalizes()
	res := b.run()
	policy.Resolve(b.r, l, "macos", opt.Policy)
	if b.r.HasErrors() || res == nil {
		return nil, b.r
	}
	return res, b.r
}

type builder struct {
	l    *model.Layout
	r    *loss.Report
	file *model.MacOSTarget
	pin  *model.MacOSTarget
	norm bool

	codes    map[string]int // position -> key code
	posOrder []string       // positions with a code, sorted
	added    map[string]*model.Position
	wide     *model.Layout // l with the layers the build uses, widened
	layerIdx []int         // wide layer -> index in l
	cmdComp  string
	tables   [States]Table
	done     [States]bool
	states   map[string]string // dead key -> state
	seen     map[string]bool   // reported once
}

func (b *builder) lost(kind, path, format string, args ...any) *loss.Entry {
	sev := loss.Warning
	switch policy.Default(kind) {
	case "error", "fatal":
		sev = loss.Error
	case "accept":
		sev = loss.Accepted
	}
	b.r.Append(loss.Entry{Class: loss.Loss, Severity: sev, Code: kind, Where: loss.Where{Path: path},
		Message: fmt.Sprintf(format, args...)})
	return &b.r.Entries[len(b.r.Entries)-1]
}

func (b *builder) once(key string) bool {
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	if b.seen[key] {
		return false
	}
	b.seen[key] = true
	return true
}

// defaulted reports a use of rule that filled the members at the JSON pointers fields.
func (b *builder) defaulted(rule string, fields []string, value any, format string, args ...any) {
	b.r.Append(DefaultedEntry(rule, fields, value, fmt.Sprintf(format, args...)))
}

// DefaultedEntry is the report entry of a default rule (as for Windows, docs/report-file.md §4).
func DefaultedEntry(rule string, fields []string, value any, msg string) loss.Entry {
	e := loss.Entry{Class: loss.Defaulted, Severity: loss.Accepted, Code: "macosDefault." + rule,
		Where: loss.Where{Path: commonParent(fields)}, Message: msg, Detail: map[string]any{"value": value}}
	if len(fields) > 1 {
		e.Detail["fields"] = fields
	}
	return e
}

func commonParent(ps []string) string {
	if len(ps) == 1 {
		return ps[0]
	}
	var common []string
	for i, p := range ps {
		toks := strings.Split(p, "/")
		if i == 0 {
			common = toks[:len(toks)-1]
			continue
		}
		n := 0
		for n < len(common) && n < len(toks)-1 && common[n] == toks[n] {
			n++
		}
		common = common[:n]
	}
	return strings.Join(common, "/")
}

const tp = "/targets/macos/"

func (b *builder) run() *Result {
	b.drops()
	if b.l.Hardware == nil || len(b.l.Hardware.Layers) == 0 {
		b.lost("macos.noHardwareLayers", "/hardware", "the file has no hardware layers: the layout would hold only the standard keys")
		return nil
	}
	b.names()
	b.layers()
	b.positions()
	b.stateNames()
	b.choices()
	for s := 0; s < States; s++ {
		b.table(s)
	}
	k := b.keyboard()
	res := &Result{Keyboard: k, Pins: b.pin, AddedPositions: b.added, Tables: b.tables, States: b.states, Codes: b.codes}
	if b.file.Icon != nil {
		res.Icon = decodeIcon(b.file.Icon.ICNS)
		if res.Icon == nil {
			b.lost("macos.outputUnencodable", tp+"icon/icns", "the icon is not valid base64")
		}
	}
	return res
}

// drops reports the general data a .keylayout has no place for (targets/macos.md §3, §4, §7).
func (b *builder) drops() {
	l := b.l
	if len(l.Touch) > 0 || len(l.Flicks) > 0 {
		b.lost("general.touchDropped", "/touch", "touch layouts and gestures are not part of a macOS layout")
	} else {
		for _, id := range sortedKeys(l.Keys) {
			if k := l.Keys[id]; len(k.LongPressKeyIDs) > 0 || len(k.MultiTapKeyIDs) > 0 {
				b.lost("general.touchDropped", "/keys/"+model.Escape(id), "long-press and multi-tap choices are not part of a macOS layout")
				break
			}
		}
	}
	if l.Displays != nil {
		b.lost("general.displayDropped", "/displays", "displays are not part of a macOS layout")
	}
	if len(l.Tests) > 0 {
		b.lost("general.testsDropped", "/tests", "tests are not part of a macOS layout")
	}
	if len(l.Extensions) > 0 || len(l.CLDRSpecial) > 0 {
		b.lost("general.extensionDropped", "/extensions", "extensions and CLDR special elements are not part of a macOS layout")
	}
	m := l.Metadata
	if len(m.Locales) > 0 || m.Description != "" || len(m.Authors) > 0 || m.Attribution != "" || m.LayoutPattern != "" ||
		m.Indicator != "" || m.License != "" || m.Copyright != "" || m.Organization != "" || m.Draft != "" || m.Provenance != nil {
		b.lost("general.metadataDropped", "/metadata", "metadata beyond the names and the version is not part of a macOS layout")
	}
	if t := l.Transforms; t != nil {
		for i := range t.Simple {
			b.lost("macos.transformNotCompiled", fmt.Sprintf("/transforms/simple/%d", i), "transforms are not compiled")
		}
		for i := range t.Backspace {
			b.lost("macos.transformNotCompiled", fmt.Sprintf("/transforms/backspace/%d", i), "backspace transforms are not compiled")
		}
	}
	if s := l.Settings; s != nil {
		if s.CapsLockOffBy == "shift" {
			b.lost("macos.settingNotCompiled", "/settings/capsLockOffBy", "Shift turning Caps Lock off has no .keylayout form")
		}
		if s.BidiMarksOnShiftBackspace != nil && *s.BidiMarksOnShiftBackspace {
			b.lost("macos.settingNotCompiled", "/settings/bidiMarksOnShiftBackspace", "bidi marks on Shift+Backspace have no .keylayout form")
		}
	}
	if b.norm {
		b.lost("general.normalizationNotApplied", "/settings/normalization", "each output is NFC-normalised at build time; text that several keystrokes make together is not (macOS does not normalise while typing)")
	}
}

// names decides the layout's names, ids and version (targets/macos.md §1, §5).
func (b *builder) names() {
	f, p, m := b.file, b.pin, b.l.Metadata
	p.LayoutName = f.LayoutName
	if p.LayoutName == "" {
		p.LayoutName = LayoutNameOf(m.Name, m.ID)
		b.defaulted("layoutName", []string{tp + "layoutName"}, p.LayoutName, "layout name %q from metadata.name", p.LayoutName)
	}
	p.BundleName = f.BundleName
	if p.BundleName == "" {
		p.BundleName = p.LayoutName
		b.defaulted("bundleName", []string{tp + "bundleName"}, p.BundleName, "bundle name %q = the layout name", p.BundleName)
	}
	p.BundleID = f.BundleID
	if p.BundleID == "" {
		p.BundleID = "org.yakbd.keyboardlayout." + Slug(m.ID)
		b.defaulted("bundleId", []string{tp + "bundleId"}, p.BundleID, "bundle id %s from metadata.id", p.BundleID)
	}
	p.InputSourceID = f.InputSourceID
	if p.InputSourceID == "" {
		p.InputSourceID = p.BundleID + "." + Slug(p.LayoutName)
		b.defaulted("inputSourceId", []string{tp + "inputSourceId"}, p.InputSourceID, "input-source id %s", p.InputSourceID)
	}
	p.Language = f.Language
	if p.Language == "" {
		p.Language, _, _ = strings.Cut(m.ID, "-")
		b.defaulted("language", []string{tp + "language"}, p.Language, "language %s from metadata.id", p.Language)
	}
	p.LocalizedNames = f.LocalizedNames
	if p.LocalizedNames == nil && len(m.LocalizedNames) > 0 {
		p.LocalizedNames = m.LocalizedNames
		b.defaulted("localizedNames", []string{tp + "localizedNames"}, p.LocalizedNames, "localised names from metadata.localizedNames")
	}
	p.BundleVersion = f.BundleVersion
	if p.BundleVersion == "" {
		p.BundleVersion = versionOf(m.Version)
		b.defaulted("bundleVersion", []string{tp + "bundleVersion"}, p.BundleVersion, "bundle version %s", p.BundleVersion)
	}
	p.Icon = f.Icon
	p.Group = f.Group
	if p.Group == nil {
		g := 126
		p.Group = &g
		b.defaulted("group", []string{tp + "group"}, g, "group 126 (Unicode)")
	}
	p.ID = f.ID
	if p.ID == nil {
		id := KeyboardID(p.InputSourceID)
		p.ID = &id
		b.defaulted("keyboardId", []string{tp + "id"}, id, "keyboard id %d from the input-source id", id)
	}
	p.HardwareRanges = f.HardwareRanges
	if p.HardwareRanges == nil {
		p.HardwareRanges = []model.MacOSHardwareRange{{First: 0, Last: 255}}
		b.defaulted("hardwareRanges", []string{tp + "hardwareRanges"}, p.HardwareRanges, "one <layout> for hardware types 0 to 255")
	}
	p.Modifiers = f.Modifiers
	if f.Modifiers != nil {
		b.cmdComp = f.Modifiers.Command
	}
}

// LayoutNameOf is rule layoutName: the name without '/', ':' and control characters, at most 128 characters; the
// id when that is empty.
func LayoutNameOf(name, id string) string {
	var out []rune
	for _, r := range name {
		if r == '/' || r == ':' || r < 0x20 || r == 0x7F {
			continue
		}
		if len(out) == 128 {
			break
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return id
	}
	return string(out)
}

// Slug is the slug of rules bundleId and inputSourceId: lower case, every run of characters outside a–z and 0–9 one
// '-', no leading or trailing '-'; "layout" when empty.
func Slug(s string) string {
	var out []byte
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && len(out) > 0 {
				out = append(out, '-')
			}
			dash = false
			out = append(out, byte(r))
		} else {
			dash = true
		}
	}
	if len(out) == 0 {
		return "layout"
	}
	return string(out)
}

var versionRE = regexp.MustCompile(`^[0-9]{1,9}(\.[0-9]{1,9}){0,2}`)

func versionOf(v string) string {
	if m := versionRE.FindString(v); m != "" {
		return m
	}
	return "1.0"
}

// KeyboardID is rule keyboardId: -2 - (FNV-1a 32 of the UTF-8 input-source id mod 32767).
func KeyboardID(inputSourceID string) int {
	h := uint32(2166136261)
	for i := 0; i < len(inputSourceID); i++ {
		h ^= uint32(inputSourceID[i])
		h *= 16777619
	}
	return -2 - int(h%32767)
}

// layers makes the widened copy of the hardware layers the build uses (targets/macos.md §3, "Sides" and "Custom
// components").
func (b *builder) layers() {
	l := b.l
	w := *l
	w.Hardware = &model.Hardware{}
	widened := false
	for i, layer := range l.Hardware.Layers {
		path := fmt.Sprintf("/hardware/layers/%d", i)
		ok := true
		var sets []model.ModifierSet
		for _, m := range layer.Modifiers {
			var set model.ModifierSet
			for _, c := range m {
				switch c {
				case "ctrlL", "ctrlR":
					c, widened = "ctrl", true
				case "altL", "altR":
					c, widened = "alt", true
				}
				if !model.Builtin[c] && c != b.cmdComp {
					cm := l.CustomModifiers[c]
					if cm != nil && cm.Kind == "lock" {
						b.lost("macos.lockUnmapped", path+"/modifiers", "the layer needs the lock %q; Caps Lock is the only lock of a .keylayout (the layer is left out)", c)
					} else {
						b.lost("macos.modifierUnmapped", path+"/modifiers", "the layer needs the component %q, which is not a macOS modifier (the layer is left out)", c)
					}
					ok = false
				}
				set = append(set, c)
			}
			sets = append(sets, set)
		}
		if !ok {
			continue
		}
		nl := *layer
		nl.Modifiers = sets
		w.Hardware.Layers = append(w.Hardware.Layers, &nl)
		b.layerIdx = append(b.layerIdx, i)
	}
	if widened {
		b.lost("macos.sideModifierWidened", "/hardware/layers", "layers for one side of Control or Option are built for either side; where two layers then serve the same state, the earlier one wins")
	}
	b.wide = &w
}

// modelState is the kbl modifier state of a build state.
func (b *builder) modelState(s int) model.State {
	st := model.State{Shift: s&StShift != 0, AltL: s&StOption != 0, CtrlL: s&StControl != 0, Caps: s&StCaps != 0}
	if s&StCommand != 0 && b.cmdComp != "" {
		st.Custom = map[string]bool{b.cmdComp: true}
	}
	return st
}

// served reports whether resolve() finds a layer for the state directly (targets/macos.md §3): a set of a layer
// other than ["other"] matches it or, with capsLockFallback, matches it without Caps Lock; a Command state only
// through a set that holds modifiers.command.
func (b *builder) served(s int) bool {
	if s&StCommand != 0 && b.cmdComp == "" {
		return false
	}
	ms := b.modelState(s)
	fallback := s&StCaps != 0 && b.l.CapsLockFallback()
	for _, layer := range b.wide.Hardware.Layers {
		for _, m := range layer.Modifiers {
			if m.IsOther() || s&StCommand != 0 && !m.Has(b.cmdComp) {
				continue
			}
			if m.Matches(ms) || fallback && m.Matches(ms.WithCaps(false)) {
				return true
			}
		}
	}
	return false
}

// positions decides every position's key code (rules keyCode, isoKeys; targets/macos.md §2).
func (b *builder) positions() {
	l, f := b.l, b.file
	bound := map[string]bool{}
	for _, layer := range b.wide.Hardware.Layers {
		for p := range layer.Bindings {
			bound[p] = true
		}
	}
	all := map[string]bool{}
	for p := range l.Positions {
		all[p] = true
	}
	for p := range bound {
		all[p] = true
	}
	if b.cmdComp != "" {
		if cm := l.CustomModifiers[b.cmdComp]; cm != nil {
			for _, p := range cm.Positions {
				all[p] = true
			}
		}
	}
	b.codes = map[string]int{}
	b.pin.Keys = map[string]*model.MacOSKey{}
	var fromTable []string
	pinned := func(p string) bool { return f.Keys != nil && f.Keys[p] != nil }
	iso := bound["IntlBackslash"] && !pinned("Backquote") && !pinned("IntlBackslash")
	for _, p := range sortedSet(all) {
		code, ok := 0, false
		if pinned(p) {
			code, ok = f.Keys[p].KeyCode, true
		} else if row, has := keycode.ForPosition(p); has {
			code, ok = row.Code, true
			if iso && p == "Backquote" {
				code = 10
			} else if iso && p == "IntlBackslash" {
				code = 50
			} else {
				fromTable = append(fromTable, p)
			}
		}
		if !ok {
			if bound[p] && postable.Known(p) {
				b.lost("macos.positionWithoutKey", "/positions/"+model.Escape(p), "the position %s has no key in the macOS position table: its bindings are left out (pin targets.macos.keys.%s.keyCode to build them)", p, p)
			} else if bound[p] {
				b.lost("macos.keyCodeMissing", "/positions/"+model.Escape(p), "the position %s has bindings but no key code (no pin, no row of the position table)", p)
			}
			continue
		}
		b.codes[p] = code
	}
	if iso {
		var fields []string
		v := map[string]int{}
		for _, p := range []string{"Backquote", "IntlBackslash"} {
			if c, ok := b.codes[p]; ok {
				fields = append(fields, tp+"keys/"+p+"/keyCode")
				v[p] = c
			}
		}
		b.defaulted("isoKeys", fields, v, "the layout binds IntlBackslash: Backquote gets key code 10 and IntlBackslash 50 (ISO keyboards)")
	}
	// standard keys of the table the file does not have
	b.added = map[string]*model.Position{}
	std := true
	if f.StandardKeys != nil {
		std = *f.StandardKeys
	}
	if std {
		for _, row := range keycode.Rows {
			if row.Output == "" {
				continue
			}
			if _, has := b.codes[row.Position]; !has && !all[row.Position] {
				b.codes[row.Position] = row.Code
				b.added[row.Position] = &model.Position{}
				fromTable = append(fromTable, row.Position)
			}
		}
	}
	sort.Strings(fromTable)
	if len(fromTable) > 0 {
		var fields []string
		v := map[string]int{}
		for _, p := range fromTable {
			fields = append(fields, tp+"keys/"+model.Escape(p)+"/keyCode")
			v[p] = b.codes[p]
		}
		b.defaulted("keyCode", fields, v, "key codes of %d positions from the position table", len(fromTable))
	}
	// duplicates
	byCode := map[int]string{}
	for _, p := range sortedKeys(b.codes) {
		c := b.codes[p]
		if q, dup := byCode[c]; dup {
			b.lost("macos.keyCodeDuplicate", tp+"keys/"+model.Escape(p), "the positions %s and %s both get key code %d", q, p, c)
			continue
		}
		byCode[c] = p
	}
	for _, p := range sortedKeys(b.codes) {
		b.pin.Keys[p] = &model.MacOSKey{KeyCode: b.codes[p]}
		b.posOrder = append(b.posOrder, p)
	}
	for _, p := range sortedKeys(l.Positions) {
		if pos := l.Positions[p]; pos.NumLock != nil && *pos.NumLock && bound[p] {
			b.lost("macos.numLockIgnored", "/positions/"+model.Escape(p), "%s always types its binding: macOS has no Num Lock", p)
		}
	}
}

// choices decides standardKeys, commandMap and controlMap.
func (b *builder) choices() {
	f, p := b.file, b.pin
	p.StandardKeys = f.StandardKeys
	if p.StandardKeys == nil {
		t := true
		p.StandardKeys = &t
		b.defaulted("standardKeys", []string{tp + "standardKeys"}, true, "standard keys (Return, Tab, arrows, ...) get the position table's outputs")
	}
	p.CommandMap = f.CommandMap
	if p.CommandMap == "" {
		p.CommandMap = "withoutCommand"
		b.defaulted("commandMap", []string{tp + "commandMap"}, p.CommandMap, "a Command state no layer serves types as the same state without Command")
	}
	p.ControlMap = f.ControlMap
	if p.ControlMap == "" {
		p.ControlMap = "controlCharacters"
		b.defaulted("controlMap", []string{tp + "controlMap"}, p.ControlMap, "a Control state no layer serves types as the state without Control, letters as control characters")
	}
}

var stateRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// stateNames is rule stateName (targets/macos.md §1, §4).
func (b *builder) stateNames() {
	b.states = map[string]string{}
	ids := sortedKeys(b.l.DeadKeys)
	used := map[string]string{}
	var fromRule []string
	v := map[string]string{}
	for n, id := range ids {
		st := ""
		if f := b.file.DeadKeys; f != nil && f[id] != nil && f[id].State != "" {
			st = f[id].State
		} else {
			st = id
			if !stateRE.MatchString(id) || id == "none" {
				st = fmt.Sprintf("dk%d", n+1)
			}
			fromRule = append(fromRule, tp+"deadKeys/"+model.Escape(id)+"/state")
			v[id] = st
		}
		if other, dup := used[st]; dup || st == "none" {
			if st == "none" {
				b.lost("macos.stateNameDuplicate", "/deadKeys/"+model.Escape(id), "the dead key %s has the state name none, the base state", id)
			} else {
				b.lost("macos.stateNameDuplicate", "/deadKeys/"+model.Escape(id), "the dead keys %s and %s have the same state name %q", other, id, st)
			}
		}
		used[st] = id
		b.states[id] = st
	}
	if b.pin.DeadKeys == nil && len(ids) > 0 {
		b.pin.DeadKeys = map[string]*model.MacOSDeadKey{}
	}
	for _, id := range ids {
		b.pin.DeadKeys[id] = &model.MacOSDeadKey{State: b.states[id]}
	}
	if len(fromRule) > 0 {
		b.defaulted("stateName", fromRule, v, "state names of %d dead keys", len(fromRule))
	}
}

// table builds the key table of state s (targets/macos.md §3).
func (b *builder) table(s int) Table {
	if b.done[s] {
		return b.tables[s]
	}
	t := Table{}
	switch {
	case s&StCommand != 0 && !b.served(s):
		src := s &^ StCommand
		if b.pin.CommandMap == "base" {
			src = 0
		}
		for c, cell := range b.table(src) {
			t[c] = cell
		}
	case s&StControl != 0 && !b.served(s):
		src := b.table(s &^ StControl)
		switch b.pin.ControlMap {
		case "withoutControl":
			for c, cell := range src {
				t[c] = cell
			}
		case "controlCharacters":
			for c, cell := range src {
				if cell.Kind == Text && len(cell.Text) == 1 {
					ch := cell.Text[0]
					switch {
					case ch >= 'a' && ch <= 'z':
						cell.Text = string(rune(ch - 'a' + 1))
					case ch >= 'A' && ch <= 'Z':
						cell.Text = string(rune(ch - 'A' + 1))
					}
				}
				t[c] = cell
			}
		}
	default:
		ms := b.modelState(s)
		for _, p := range b.posOrder {
			bnd := b.wide.Resolve(p, ms)
			if bnd == nil {
				continue
			}
			if cell := b.cellOf(p, bnd); cell.Kind != Nothing {
				t[b.codes[p]] = cell
			}
		}
	}
	if *b.pin.StandardKeys {
		for _, row := range keycode.Rows {
			if row.Output == "" {
				continue
			}
			code, ok := b.codes[row.Position]
			if !ok {
				continue
			}
			if _, has := t[code]; !has {
				t[code] = Cell{Kind: Text, Text: row.Output}
			}
		}
	}
	b.tables[s], b.done[s] = t, true
	return t
}

// cellOf turns a resolved binding into a cell, with the losses of its output.
func (b *builder) cellOf(p string, bnd *model.Binding) Cell {
	o := b.l.Effective(bnd)
	path := b.bindingPath(p, bnd)
	if o.Markers && b.once("marker "+path) {
		b.lost("macos.markerNotCompiled", path, "the output's markers are left out")
	}
	if o.DeadKey != "" {
		return Cell{Kind: Dead, Dead: o.DeadKey}
	}
	if len(o.Units) == 0 {
		return Cell{Kind: ExplicitNothing}
	}
	text, ok := b.text(o.Units, o.Exact, path)
	if !ok {
		return Cell{Kind: ExplicitNothing}
	}
	return Cell{Kind: Text, Text: text}
}

// text returns units as a .keylayout output: NFC when the layout normalises (not for CodeUnits), U+0000 and lone
// surrogates refused.
func (b *builder) text(u []uint16, exact bool, path string) (string, bool) {
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c == 0 {
			b.unencodable(path, "U+0000")
			return "", false
		}
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			i++
			continue
		}
		if c >= 0xD800 && c <= 0xDFFF {
			b.unencodable(path, "a lone surrogate")
			return "", false
		}
	}
	o := model.Output{Units: u, Exact: exact}
	if b.norm && !exact {
		n := o.NFC()
		if string(utf16.Decode(n.Units)) != string(utf16.Decode(u)) && b.once("nfc "+path+" "+string(utf16.Decode(u))) {
			e := b.lost("general.outputNormalized", path, "NFC changes the output %s to %s", hexUnits(u), hexUnits(n.Units))
			e.Detail = map[string]any{"before": hexUnits(u), "after": hexUnits(n.Units)}
		}
		o = n
	}
	return string(utf16.Decode(o.Units)), true
}

func (b *builder) unencodable(path, what string) {
	if b.once("unenc " + path) {
		b.lost("macos.outputUnencodable", path, "the output holds %s, which a .keylayout cannot hold", what)
	}
}

func hexUnits(u []uint16) string {
	var s []string
	for _, c := range u {
		s = append(s, fmt.Sprintf("%04X", c))
	}
	return strings.Join(s, " ")
}

// bindingPath is the JSON pointer of a binding (the layer it is in).
func (b *builder) bindingPath(p string, bnd *model.Binding) string {
	for i, layer := range b.wide.Hardware.Layers {
		if layer.Bindings[p] == bnd {
			return fmt.Sprintf("/hardware/layers/%d/bindings/%s", b.layerIdx[i], model.Escape(p))
		}
	}
	return "/hardware"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSet(m map[string]bool) []string { return sortedKeys(m) }
