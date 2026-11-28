// Package xkbkbl converts between XKB symbols and Compose files (format/xkb) and the model, by the kbl
// specification's coverage/xkb.md (import) and targets/xkb.md (export, which is the XKB build of
// target/xkb/build).
package xkbkbl

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
	"github.com/yakbd/yakbd-cli/target/xkb/keyname"
)

// The modifier states the importer reads: every combination of Shift, Lock, LevelThree and LevelFive
// (targets/xkb.md §3).
const states = 16

// Keysyms of the level shifts (keysymdef.h).
const (
	ksLevel3Shift = 0xfe03
	ksLevel5Shift = 0xfe11
)

// modifierKeysym reports keysyms of modifier, lock and group keys: a key with one of them operates a modifier
// and types nothing; symbols/pc gives them again, so they are no loss.
func modifierKeysym(k xkb.Keysym) bool {
	switch {
	case k >= 0xffe1 && k <= 0xffee: // Shift_L .. Hyper_R, Caps_Lock, Shift_Lock, Meta, Alt, Super
		return true
	case k >= 0xfe01 && k <= 0xfe0f, k >= 0xfe11 && k <= 0xfe13: // ISO_Lock .. ISO_Last_Group_Lock, Level5
		return true
	case k == 0xff7e || k == 0xff7f: // Mode_switch, Num_Lock
		return true
	}
	return false
}

// Options of an import.
type Options struct {
	ID string // metadata.id; "" gives und-x-<slug of the symbols file name>
	// SymbolsName is the symbols file's name (targets.xkb.symbolsName); Variant the section's name when it is not
	// the file's default section, "" otherwise.
	SymbolsName string
	Variant     string
	Compose     *xkb.Compose // the Compose file of the layout's dead keys, nil for none
	ComposeName string       // its name in reports
}

type importer struct {
	km   *xkb.Keymap
	name string
	o    Options
	r    *loss.Report
	once map[string]bool

	l3comp   string   // the component of LevelThree: altR, level3 or ""
	l3pos    []string // positions of a level3 custom component
	l5pos    []string
	posOf    map[string]string // key name -> position
	cells    map[string]*[states]cell
	deadSyms map[xkb.Keysym]bool
	typed    map[xkb.Keysym]bool // every keysym some key types
	types    map[string]string   // position -> type to pin
	numLock  map[string]bool     // positions of keypad types
	unknown  map[string]string   // position -> key name of a position named Xkb...
}

type cell struct {
	text string
	dead xkb.Keysym
}

func (c cell) none() bool { return c.text == "" && c.dead == 0 }

func (im *importer) lost(kind string, sev loss.Severity, line int, format string, args ...any) {
	im.r.Append(loss.Entry{Class: loss.Loss, Severity: sev, Code: kind, Where: loss.Where{File: im.name, Line: line},
		Message: fmt.Sprintf(format, args...)})
}

func (im *importer) lostOnce(key, kind string, sev loss.Severity, format string, args ...any) {
	if im.once[key] {
		return
	}
	im.once[key] = true
	im.lost(kind, sev, 0, format, args...)
}

// xkbMods is the XKB modifier state of an importer state.
func xkbMods(s int) xkb.Mods { return build.Mods(s) }

func newImporter(km *xkb.Keymap, name string, o Options) *importer {
	return &importer{km: km, name: name, o: o, r: &loss.Report{}, once: map[string]bool{}, posOf: map[string]string{},
		cells: map[string]*[states]cell{}, deadSyms: map[xkb.Keysym]bool{}, typed: map[xkb.Keysym]bool{},
		types: map[string]string{}, numLock: map[string]bool{}, unknown: map[string]string{}}
}

// FromXKB converts a resolved symbols section (xkb.Compile) and, optionally, its Compose file into a layout
// (coverage/xkb.md). name is the section's name in reports and provenance.
func FromXKB(km *xkb.Keymap, name string, o Options) (*model.Layout, *loss.Report) {
	im := newImporter(km, name, o)
	im.lost("xkb.syntaxDetailsIgnored", loss.Accepted, 0, "comments, white space, section flags and the spelling of keysyms are not kept")
	for _, inc := range km.Inlined {
		im.lost("general.importInlined", loss.Accepted, 0, "include %s inlined", inc)
	}
	im.switches()
	im.keys()
	l := &model.Layout{Kbl: model.FormatVersion, Positions: map[string]*model.Position{}}
	l.Metadata.Name = km.Names[1]
	if l.Metadata.Name == "" {
		l.Metadata.Name = name
	}
	for g := range km.Names {
		if g > 1 {
			im.lostOnce("names", "xkb.groupDropped", loss.Warning, "the names of groups 2 to 4 are not kept: one .kbl.json file holds one group")
		}
	}
	l.Metadata.ID = o.ID
	if l.Metadata.ID == "" {
		src := o.SymbolsName
		if src == "" {
			src = l.Metadata.Name
		}
		parts := strings.Split(build.Slug(src), "-")
		for i, p := range parts {
			if len(p) > 8 {
				parts[i] = p[:8]
			}
		}
		l.Metadata.ID = "und-x-" + strings.Join(parts, "-")
	}
	l.Metadata.Provenance = &model.Provenance{Source: name, Format: "xkb"}
	t := true
	// XKB types exactly the keysyms a level lists: no normalisation while typing
	l.Settings = &model.Settings{Normalization: "disabled", CapsLockFallback: &t}
	im.layers(l)
	x := &model.XKBTarget{Description: km.Names[1]}
	if fileNameRE.MatchString(o.SymbolsName) {
		x.SymbolsName = o.SymbolsName
	}
	switch {
	case o.Variant == "":
		x.Variant = "basic"
	case fileNameRE.MatchString(o.Variant):
		x.Variant = o.Variant
	}
	im.deadKeys(l, x)
	if len(im.l3pos) > 0 || len(im.l5pos) > 0 {
		l.CustomModifiers = map[string]*model.CustomModifier{}
		x.Modifiers = &model.XKBModifiers{}
		if len(im.l3pos) > 0 {
			l.CustomModifiers["level3"] = &model.CustomModifier{Kind: "held", Positions: im.l3pos}
			x.Modifiers.LevelThree = "level3"
		}
		if len(im.l5pos) > 0 {
			l.CustomModifiers["level5"] = &model.CustomModifier{Kind: "held", Positions: im.l5pos}
			x.Modifiers.LevelFive = "level5"
		}
	}
	for _, p := range im.posOf {
		l.Positions[p] = &model.Position{}
		if im.numLock[p] {
			l.Positions[p].NumLock = &t
		}
	}
	for _, ps := range [][]string{im.l3pos, im.l5pos} {
		for _, p := range ps {
			l.Positions[p] = &model.Position{}
		}
	}
	if im.l3comp == "altR" {
		l.Positions["AltRight"] = &model.Position{}
	}
	for _, p := range im.posOf {
		k := &model.XKBKey{Type: im.types[p], KeyName: im.unknown[p]}
		if *k != (model.XKBKey{}) {
			if x.Keys == nil {
				x.Keys = map[string]*model.XKBKey{}
			}
			x.Keys[p] = k
		}
	}
	l.Targets = &model.Targets{XKB: x}
	return l, im.r
}

var (
	fileNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	keyNameRE  = regexp.MustCompile(`^[0-9A-Za-z_+-]{1,4}$`)
)

// switches finds the keys that set LevelThree and LevelFive.
func (im *importer) switches() {
	ralt := false
	for _, n := range im.km.Order {
		k := im.km.Keys[n]
		if len(k.Groups) == 0 {
			continue
		}
		var syms []xkb.Keysym
		for _, lv := range k.Groups[0].Levels {
			for _, s := range lv {
				syms = append(syms, s.K)
			}
		}
		if len(syms) == 0 {
			continue
		}
		pos, _ := im.position(n)
		switch syms[0] {
		case ksLevel3Shift:
			if keyname.Canonical(n) == "RALT" {
				ralt = true
			} else {
				im.l3pos = append(im.l3pos, pos)
			}
		case ksLevel5Shift:
			im.l5pos = append(im.l5pos, pos)
		}
	}
	switch {
	case len(im.l3pos) > 0:
		im.l3comp = "level3"
		if ralt {
			im.l3pos = append(im.l3pos, "AltRight")
		}
	case ralt:
		im.l3comp = "altR"
	}
	sort.Strings(im.l3pos)
	sort.Strings(im.l5pos)
}

// position is the position of a key name: the table's, or Xkb + the name (false: not in the table).
func (im *importer) position(n string) (string, bool) {
	if p, ok := keyname.ForKeyName(n); ok {
		return p, true
	}
	var b strings.Builder
	b.WriteString("Xkb")
	for _, r := range n {
		if r < 128 && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String(), false
}

// keypadType reports the types whose level 2 is the Num Lock level.
func keypadType(name string) bool {
	switch name {
	case "KEYPAD", "FOUR_LEVEL_KEYPAD", "FOUR_LEVEL_MIXED_KEYPAD":
		return true
	}
	return false
}

// pinnable reports the types of targets/xkb.md §3.
func pinnable(name string) bool {
	for _, t := range build.StockTypes {
		if t == name {
			return true
		}
	}
	return strings.HasPrefix(name, "YAKBD_") && build.TypeOf(name) != nil
}

// keys evaluates every key in the 16 states.
func (im *importer) keys() {
	modmaps := false
	for _, m := range im.km.ModMaps {
		for _, k := range m.Keys {
			if k != "ISO_Level3_Shift" && k != "ISO_Level5_Shift" && k != "<LVL3>" && k != "<LVL5>" {
				modmaps = true
			}
		}
	}
	if len(im.km.Defaults) > 0 {
		im.lostOnce("defaults", "xkb.actionsDropped", loss.Warning, "the key defaults %s are not kept", strings.Join(im.km.Defaults, "; "))
	}
	if modmaps {
		im.lostOnce("modmap", "xkb.actionsDropped", loss.Warning, "modifier_map statements are not kept")
	}
	for _, n := range im.km.Order {
		k := im.km.Keys[n]
		if len(k.Groups) == 0 {
			continue
		}
		if len(k.Groups) > 1 {
			im.lostOnce("groups", "xkb.groupDropped", loss.Warning, "groups 2 to 4 of keys are not kept: one .kbl.json file holds one group (first: <%s>)", n)
		}
		g := k.Groups[0]
		if g.Actions != "" || len(k.Props) > 0 {
			im.lostOnce("act:"+n, "xkb.actionsDropped", loss.Warning, "the actions and other fields of <%s> are not kept", n)
		}
		if len(g.Levels) == 0 {
			continue
		}
		if mod := len(g.Levels[0]) > 0 && modifierKeysym(g.Levels[0][0].K); mod {
			continue
		}
		t, known := xkb.TypeOf(g)
		if yt := build.TypeOf(g.Type); !known && yt != nil {
			t, known = yt, true // a yakbd type: its name is its definition
		}
		pos, inTable := im.position(n)
		keypad := keypadType(t.Name)
		var cs [states]cell
		any := false
		for s := 0; s < states; s++ {
			if s&4 != 0 && im.l3comp == "" || s&8 != 0 && len(im.l5pos) == 0 {
				continue
			}
			var lv xkb.Level
			if keypad {
				// the Num Lock level; the navigation level is the position table's
				if s == 0 && len(g.Levels) > 1 {
					lv = g.Levels[1]
				}
			} else {
				lv = t.Type(g.Levels, xkbMods(s))
			}
			// VoidSymbol is an explicit nothing (a yakbd build writes it for every level that types nothing, kbl D146)
			if len(lv) == 0 || len(lv) == 1 && lv[0].K == xkb.VoidSymbol {
				continue
			}
			for _, x := range lv {
				im.typed[x.K] = true
			}
			if txt, ok := lv.Text(); ok {
				cs[s] = cell{text: txt}
				any = true
				continue
			}
			if len(lv) == 1 && lv[0].K.IsDead() {
				cs[s] = cell{dead: lv[0].K}
				im.deadSyms[lv[0].K] = true
				any = true
				continue
			}
			name := xkb.LevelText(lv, xkb.WriteOptions{})
			im.lostOnce("sym:"+name, "xkb.keysymNotText", loss.Warning, "%s types no text: the level is no binding (first: <%s>)", name, n)
		}
		if !any {
			continue
		}
		if !known {
			im.lostOnce("type:"+g.Type, "xkb.keyTypeUnknown", loss.Warning, "the key type %q has no definition here: its keys are read as if they named no type (first: <%s>)", g.Type, n)
		}
		if t.Mods&^(xkb.Shift|xkb.Lock|xkb.LevelThree|xkb.LevelFive) != 0 && !keypad {
			im.lostOnce("typemods:"+t.Name, "xkb.typeModifierDropped", loss.Warning, "the states of %s with modifiers other than Shift, Caps Lock, LevelThree and LevelFive are not imported (first: <%s>)", t.Name, n)
		}
		if keypad {
			r, _ := keyname.ForPosition(pos)
			if nav := xkb.LevelText(g.Levels[0], xkb.WriteOptions{}); r.Keypad == "" || nav != r.Keypad {
				im.lostOnce("nav:"+n, "xkb.typeModifierDropped", loss.Warning, "the Num Lock off level of <%s> (%s) is not imported: a build gives the position table's navigation keysym", n, nav)
			}
			im.numLock[pos] = true
		} else if known && pinnable(t.Name) {
			im.types[pos] = t.Name
		}
		if !inTable && !keyNameRE.MatchString(keyname.Canonical(n)) {
			im.lostOnce("name:"+n, "xkb.keyNameUnknown", loss.Warning, "the key <%s> has no row in the position table and a name targets.xkb.keys cannot hold: it is not kept", n)
			continue
		}
		if !inTable {
			im.lostOnce("name:"+n, "xkb.keyNameUnknown", loss.Accepted, "the key <%s> has no row in the position table: it is the position %s with its key name pinned", n, pos)
			im.unknown[pos] = keyname.Canonical(n)
		}
		im.posOf[n] = pos
		im.cells[pos] = &cs
	}
}

// comps is the modifier set of a state, in the order caps, shift, LevelThree, level5.
func (im *importer) comps(s int) model.ModifierSet {
	m := model.ModifierSet{}
	if s&2 != 0 {
		m = append(m, "caps")
	}
	if s&1 != 0 {
		m = append(m, "shift")
	}
	if s&4 != 0 {
		m = append(m, im.l3comp)
	}
	if s&8 != 0 {
		m = append(m, "level5")
	}
	return m
}

func deadID(k xkb.Keysym) string { return strings.TrimPrefix(k.Name(), "dead_") }

func (im *importer) binding(c cell) *model.Binding {
	if c.dead != 0 {
		return &model.Binding{Kind: model.BindDeadKey, DeadKey: deadID(c.dead)}
	}
	return model.TextBinding(c.text)
}

// layers writes one layer per state that has a binding. A Caps Lock state lists only what differs from the
// same state without Caps Lock (settings.capsLockFallback true), with {"output": ""} where Caps Lock gives
// nothing but the state without it types something.
func (im *importer) layers(l *model.Layout) {
	positions := make([]string, 0, len(im.cells))
	for p := range im.cells {
		positions = append(positions, p)
	}
	sort.Strings(positions)
	hw := &model.Hardware{}
	for _, l5 := range []int{0, 8} {
		for _, l3 := range []int{0, 4} {
			for _, caps := range []int{0, 2} {
				for _, sh := range []int{0, 1} {
					s := l5 | l3 | caps | sh
					layer := &model.HardwareLayer{Modifiers: []model.ModifierSet{im.comps(s)}, Bindings: map[string]*model.Binding{}}
					for _, p := range positions {
						c := im.cells[p][s]
						if caps != 0 {
							plain := im.cells[p][s&^2]
							if c == plain {
								continue
							}
							if c.none() {
								layer.Bindings[p] = &model.Binding{Kind: model.BindOutput, Output: model.NewText("")}
								continue
							}
						}
						if !c.none() {
							layer.Bindings[p] = im.binding(c)
						}
					}
					if len(layer.Bindings) > 0 || s == 0 {
						hw.Layers = append(hw.Layers, layer)
					}
				}
			}
		}
	}
	l.Hardware = hw
}
