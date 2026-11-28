// Package xkbkbl converts between XKB symbols and Compose files (format/xkb) and the model, by the kbl
// specification's coverage/xkb.md (import) and targets/xkb.md (export, which is the XKB build of
// target/xkb/build).
package xkbkbl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/keyname"
)

// The modifier states the importer reads: every combination of Shift, Lock, LevelThree and LevelFive.
const states = 16

// Keysyms of the level switches and of modifier keys (keysymdef.h).
const (
	ksLevel3Shift = 0xfe03
	ksLevel5Shift = 0xfe11
)

// modifierKeysym reports keysyms of modifier, lock and group keys: a key with one of them operates a modifier
// and types nothing, so it is no loss.
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
	ID      string       // metadata.id; "" gives und-x-<slug of the name>
	Compose *xkb.Compose // the Compose file of the layout's dead keys, nil for none
	// ComposeName is the Compose file's name in reports.
	ComposeName string
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
func xkbMods(s int) xkb.Mods {
	var m xkb.Mods
	if s&1 != 0 {
		m |= xkb.Shift
	}
	if s&2 != 0 {
		m |= xkb.Lock
	}
	if s&4 != 0 {
		m |= xkb.LevelThree
	}
	if s&8 != 0 {
		m |= xkb.LevelFive
	}
	return m
}

// FromXKB converts a resolved symbols section (xkb.Compile) and, optionally, its Compose file into a layout
// (coverage/xkb.md). name is the file's name in reports and provenance.
func FromXKB(km *xkb.Keymap, name string, o Options) (*model.Layout, *loss.Report) {
	im := &importer{km: km, name: name, o: o, r: &loss.Report{}, once: map[string]bool{}, posOf: map[string]string{},
		cells: map[string]*[states]cell{}, deadSyms: map[xkb.Keysym]bool{}}
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
		l.Metadata.ID = "und-x-" + slug(l.Metadata.Name)
	}
	l.Metadata.Provenance = &model.Provenance{Source: name, Format: "xkb"}
	// XKB types exactly the keysyms a level lists: no normalisation while typing
	l.Settings = &model.Settings{Normalization: "disabled"}
	im.layers(l)
	im.deadKeys(l)
	if len(im.l3pos) > 0 || len(im.l5pos) > 0 {
		l.CustomModifiers = map[string]*model.CustomModifier{}
		if len(im.l3pos) > 0 {
			l.CustomModifiers["level3"] = &model.CustomModifier{Kind: "held", Positions: im.l3pos}
		}
		if len(im.l5pos) > 0 {
			l.CustomModifiers["level5"] = &model.CustomModifier{Kind: "held", Positions: im.l5pos}
		}
	}
	for _, p := range im.posOf {
		l.Positions[p] = &model.Position{}
	}
	for _, ps := range [][]string{im.l3pos, im.l5pos} {
		for _, p := range ps {
			l.Positions[p] = &model.Position{}
		}
	}
	if im.l3comp == "altR" {
		l.Positions["AltRight"] = &model.Position{}
	}
	return l, im.r
}

func slug(s string) string {
	var parts []string
	cur := []byte{}
	flush := func() {
		if len(cur) > 0 {
			p := string(cur)
			if len(p) > 8 {
				p = p[:8]
			}
			parts = append(parts, p)
			cur = cur[:0]
		}
	}
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			cur = append(cur, byte(r))
		} else {
			flush()
		}
	}
	flush()
	if len(parts) == 0 {
		return "layout"
	}
	return strings.Join(parts, "-")
}

// switches finds the keys that set LevelThree and LevelFive.
func (im *importer) switches() {
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
		pos, ok := keyname.ForKeyName(n)
		switch syms[0] {
		case ksLevel3Shift:
			if keyname.Canonical(n) == "RALT" {
				im.l3comp = "altR"
			} else if ok {
				im.l3pos = append(im.l3pos, pos)
			}
		case ksLevel5Shift:
			if ok {
				im.l5pos = append(im.l5pos, pos)
			}
		}
	}
	if im.l3comp == "" && len(im.l3pos) > 0 {
		im.l3comp = "level3"
	}
	if im.l3comp == "altR" && len(im.l3pos) > 0 {
		im.lostOnce("l3", "xkb.keysymNotText", loss.Warning, "LevelThree is set by <RALT> and by other keys (%s): only AltRight is kept", strings.Join(im.l3pos, ", "))
		im.l3pos = nil
	}
	sort.Strings(im.l3pos)
	sort.Strings(im.l5pos)
}

// keys evaluates every key in the 16 states.
func (im *importer) keys() {
	for _, n := range im.km.Order {
		k := im.km.Keys[n]
		if len(k.Groups) == 0 {
			continue
		}
		if len(k.Groups) > 1 {
			im.lostOnce("groups", "xkb.groupDropped", loss.Warning, "groups 2 to 4 of keys are not kept: one .kbl.json file holds one group (first: <%s>)", n)
		}
		g := k.Groups[0]
		if g.Actions != "" {
			im.lostOnce("act:"+n, "xkb.actionsDropped", loss.Warning, "the actions of <%s> are not kept", n)
		}
		if len(g.Levels) == 0 {
			continue
		}
		if mod := len(g.Levels[0]) > 0 && modifierKeysym(g.Levels[0][0].K); mod {
			continue
		}
		t, known := xkb.TypeOf(g)
		var cs [states]cell
		any := false
		for s := 0; s < states; s++ {
			if s&4 != 0 && im.l3comp == "" || s&8 != 0 && len(im.l5pos) == 0 {
				continue
			}
			lv := t.Type(g.Levels, xkbMods(s))
			if len(lv) == 0 {
				continue
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
			im.lostOnce("sym:"+name, "xkb.keysymNotText", loss.Warning, "%s types no text: it is not kept (first: <%s>)", name, n)
		}
		if !any {
			continue
		}
		if !known {
			im.lostOnce("type:"+g.Type, "xkb.keyTypeUnknown", loss.Warning, "the key type %q is not a type the tool knows: its keys are read as ONE_LEVEL (first: <%s>)", g.Type, n)
		}
		if t.Mods&xkb.NumLock != 0 {
			im.lostOnce("numlock", "xkb.numLockLevelsDropped", loss.Accepted, "keys whose type uses Num Lock are read with Num Lock off (first: <%s>)", n)
		}
		pos, ok := keyname.ForKeyName(n)
		if !ok {
			im.lostOnce("name:"+n, "xkb.keyNameUnknown", loss.Warning, "the key <%s> has no position in the position table: it is not kept", n)
			continue
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
// same state without Caps Lock (settings.capsLockFallback, default true), with {"output": ""} where Caps Lock
// gives nothing but the state without it types something.
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

// node is a prefix of Compose sequences that start with a dead key of the layout.
type node struct {
	result string
	kids   map[xkb.Keysym]*node
	order  []xkb.Keysym
	line   int
}

// deadKeys builds the dead keys of the layout from the Compose file: every sequence that starts with a dead key
// of the layout becomes an entry (longer ones a chain of dead keys); other sequences are not kept.
func (im *importer) deadKeys(l *model.Layout) {
	if len(im.deadSyms) == 0 {
		if im.o.Compose != nil && hasSequences(im.o.Compose) {
			im.lost("xkb.composeSequenceDropped", loss.Warning, 0, "the layout has no dead keys: the sequences of %s are not kept", im.o.ComposeName)
		}
		return
	}
	roots := map[xkb.Keysym]*node{}
	dropped, includes := 0, 0
	if c := im.o.Compose; c != nil {
		for _, ln := range c.Lines {
			if ln.Seq == nil {
				includes++
				continue
			}
			if !im.deadSyms[ln.Seq[0]] || ln.Result == "" || !im.typable(ln.Seq[1:]) {
				dropped++
				continue
			}
			n := roots[ln.Seq[0]]
			if n == nil {
				n = &node{}
				roots[ln.Seq[0]] = n
			}
			for _, k := range ln.Seq[1:] {
				if n.kids == nil {
					n.kids = map[xkb.Keysym]*node{}
				}
				nx := n.kids[k]
				if nx == nil {
					nx = &node{}
					n.kids[k] = nx
					n.order = append(n.order, k)
				}
				n = nx
			}
			if n.result == "" {
				n.result, n.line = ln.Result, ln.Line
			}
		}
	}
	if dropped > 0 {
		im.lost("xkb.composeSequenceDropped", loss.Warning, 0, "%d sequences of %s do not start with a dead key of the layout or use keys that type nothing: they are not kept", dropped, im.o.ComposeName)
	}
	if includes > 0 {
		im.lost("xkb.composeSequenceDropped", loss.Accepted, 0, "the include lines of %s (the system's sequences) are not kept: a build writes include \"%%L\" again", im.o.ComposeName)
	}
	l.DeadKeys = map[string]*model.DeadKey{}
	syms := make([]xkb.Keysym, 0, len(im.deadSyms))
	for k := range im.deadSyms {
		syms = append(syms, k)
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i] < syms[j] })
	for _, k := range syms {
		n := roots[k]
		if n == nil {
			n = &node{}
			if im.o.Compose == nil {
				im.lost("xkb.deadKeyNoCompose", loss.Warning, 0, "the dead key %s has no Compose file: its compositions are those of the system, which a .kbl.json file cannot name", k.Name())
			} else {
				im.lost("xkb.deadKeyNoCompose", loss.Warning, 0, "the dead key %s has no sequence in %s", k.Name(), im.o.ComposeName)
			}
		}
		im.addDead(l, deadID(k), k, n)
	}
}

func (im *importer) typable(seq []xkb.Keysym) bool {
	for _, k := range seq {
		if _, ok := k.Rune(); !ok && !im.deadSyms[k] {
			return false
		}
	}
	return true
}

func hasSequences(c *xkb.Compose) bool {
	for _, l := range c.Lines {
		if l.Seq != nil {
			return true
		}
	}
	return false
}

func (im *importer) addDead(l *model.Layout, id string, k xkb.Keysym, n *node) {
	dk := &model.DeadKey{NoMatch: "emitNothing", Entries: []model.DeadKeyEntry{}}
	dk.Spacing = model.TextOrUnits{Text: spacingOf(k, n)}
	for _, b := range n.order {
		kid := n.kids[b]
		var e model.DeadKeyEntry
		if r, ok := b.Rune(); ok {
			e.Base = model.Value{Kind: model.ValueText, Text: string(r)}
		} else {
			e.Base = model.Value{Kind: model.ValueDeadKey, DeadKey: deadID(b)}
		}
		if len(kid.kids) > 0 {
			if kid.result != "" {
				im.lost("xkb.composeSequenceDropped", loss.Warning, kid.line, "a sequence that is the start of longer ones gives %q: the longer ones are kept", kid.result)
			}
			sub := id + "+" + deadID(b)
			e.Result = model.Value{Kind: model.ValueDeadKey, DeadKey: sub}
			im.addDead(l, sub, k, kid)
		} else {
			e.Result = model.Value{Kind: model.ValueText, Text: kid.result}
		}
		dk.Entries = append(dk.Entries, e)
	}
	l.DeadKeys[id] = dk
}

// spacingOf is the spacing text of a dead key: what <dead> <space> gives in the Compose file, else the spacing
// form of the accent the keysym names, else its combining mark, else the name.
func spacingOf(k xkb.Keysym, n *node) string {
	if kid := n.kids[xkb.Keysym(' ')]; kid != nil && kid.result != "" {
		return kid.result
	}
	if s, ok := deadSpacing[deadID(k)]; ok {
		return s
	}
	return deadID(k)
}

// deadSpacing is the spacing character of the common accents (Unicode spacing modifier letters and Latin-1).
var deadSpacing = map[string]string{
	"grave": "`", "acute": "´", "circumflex": "^", "tilde": "~", "macron": "¯", "breve": "˘", "abovedot": "˙",
	"diaeresis": "¨", "abovering": "˚", "doubleacute": "˝", "caron": "ˇ", "cedilla": "¸", "ogonek": "˛",
	"iota": "ͺ", "voiced_sound": "゛", "semivoiced_sound": "゜", "belowdot": "̣", "hook": "̉",
	"horn": "̛", "stroke": "̵", "abovecomma": "̓", "abovereversedcomma": "̔",
	"doublegrave": "̏", "belowring": "̥", "belowmacron": "̱", "belowcircumflex": "̭",
	"belowtilde": "̰", "belowbreve": "̮", "belowdiaeresis": "̤", "invertedbreve": "̑",
	"belowcomma": "̦", "currency": "¤", "greek": "µ", "lowline": "_", "aboveverticalline": "̍",
	"belowverticalline": "̩", "longsolidusoverlay": "̸",
}
