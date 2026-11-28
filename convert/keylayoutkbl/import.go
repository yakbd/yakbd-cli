// Package keylayoutkbl converts between macOS .keylayout files (format/keylayout) and the model, by the kbl
// specification's coverage/macos.md (import) and targets/macos.md (export, which is the macOS build of
// target/macos/build).
package keylayoutkbl

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/macos/build"
	"github.com/yakbd/yakbd-cli/target/macos/keycode"
)

// cell is what a key does in the base state: a text, a dead key, or nothing.
type cell struct {
	text string
	dead string // a state name
}

func (c cell) none() bool { return c.text == "" && c.dead == "" }

// table is one key map's cells by key code.
type table map[int]cell

type importer struct {
	k      *keylayout.Keyboard
	name   string
	r      *loss.Report
	lay    keylayout.Layout
	mods   *keylayout.ModifierMap
	idx    [build.States]int // build state -> key map index
	tables map[int]table     // key map index -> cells
	keys   map[int]map[int]keylayout.Key
	shapes map[string][]string // stateNotExpressible cases
	ranges map[int]bool        // lines of dropped range whens
	ids    map[string]string   // state name -> dead-key id
}

func (im *importer) lost(kind string, line int, detail map[string]any, format string, args ...any) {
	sev := loss.Warning
	switch kind {
	case "macos.xmlDetailsIgnored", "macos.keyCodeUnknown":
		sev = loss.Accepted
	}
	im.r.Append(loss.Entry{Class: loss.Loss, Severity: sev, Code: kind, Where: loss.Where{File: im.name, Line: line},
		Message: fmt.Sprintf(format, args...), Detail: detail})
}

func (im *importer) shape(n int, what string) {
	key := fmt.Sprintf("case%d", n)
	for _, s := range im.shapes[key] {
		if s == what {
			return
		}
	}
	im.shapes[key] = append(im.shapes[key], what)
}

// macState is the TN2056 modifier state of a build state, with the left-side keys.
func macState(s int) keylayout.State {
	var m keylayout.State
	if s&build.StShift != 0 {
		m |= keylayout.Shift
	}
	if s&build.StOption != 0 {
		m |= keylayout.Option
	}
	if s&build.StControl != 0 {
		m |= keylayout.Control
	}
	if s&build.StCommand != 0 {
		m |= keylayout.Command
	}
	if s&build.StCaps != 0 {
		m |= keylayout.Caps
	}
	return m
}

var stateNameRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// FromKeylayout converts a keyboard read by keylayout.Read into a layout (coverage/macos.md §2 to §6). id is the
// layout's metadata.id; empty gives und-x-<slug of the name, each part at most 8 characters>.
func FromKeylayout(k *keylayout.Keyboard, name, id string) (*model.Layout, *loss.Report) {
	im := &importer{k: k, name: name, r: &loss.Report{}, tables: map[int]table{}, keys: map[int]map[int]keylayout.Key{},
		shapes: map[string][]string{}, ranges: map[int]bool{}, ids: deadIDs(k)}
	im.lost("macos.xmlDetailsIgnored", 0, nil, "XML details that are not layout data (declaration, comments, white space, attribute order, character references, element ids and numbering) are not kept")
	im.lay = k.Layouts[0]
	im.mods = k.ModifierMap(im.lay.Modifiers)
	mt := &model.MacOSTarget{}
	for _, l := range k.Layouts {
		if l.Modifiers == im.lay.Modifiers && l.MapSet == im.lay.MapSet {
			mt.HardwareRanges = append(mt.HardwareRanges, model.MacOSHardwareRange{First: min(l.First, 255), Last: min(l.Last, 255)})
		} else {
			im.lost("macos.keyMapSetDropped", l.Line, nil, "the <layout> for hardware types %d to %d selects another key map set (%q) or modifier map: one .kbl.json file holds one set", l.First, l.Last, l.MapSet)
		}
	}
	im.states()
	// positions
	used := map[int]bool{}
	for _, t := range im.tables {
		for c, cl := range t {
			if !cl.none() {
				used[c] = true
			}
		}
	}
	posOf, pins := im.positions(used)
	// standard keys: left out where they type the table's output in every state
	leftOut := map[int]bool{}
	for c := range used {
		p, ok := posOf[c]
		if !ok {
			continue
		}
		row, ok := keycode.ForPosition(p)
		if !ok || row.Output == "" || pins[p] {
			continue
		}
		all := true
		for s := 0; s < build.States; s++ {
			if im.tables[im.idx[s]][c] != (cell{text: row.Output}) {
				all = false
				break
			}
		}
		if all {
			leftOut[c] = true
		}
	}
	// standardKeys only when it rebuilds exactly what the file has: some keys left out and no standard key missing
	std := len(leftOut) > 0
	for _, row := range keycode.Rows {
		if row.Output != "" && !used[row.Code] {
			std = false
		}
	}
	if !std {
		leftOut = map[int]bool{}
	}
	mt.StandardKeys = &std
	view := func(s int) table { // the state's cells without the left-out standard keys
		out := table{}
		for c, cl := range im.tables[im.idx[s]] {
			if !cl.none() && !leftOut[c] {
				if _, ok := posOf[c]; ok {
					out[c] = cl
				}
			}
		}
		return out
	}
	// Command and Control (coverage/macos.md §3)
	cmdFit := ""
	for _, mode := range []string{"withoutCommand", "base"} {
		ok := true
		for s := 0; s < build.States && ok; s++ {
			if s&build.StCommand == 0 {
				continue
			}
			src := s &^ build.StCommand
			if mode == "base" {
				src = 0
			}
			ok = equal(view(s), view(src))
		}
		if ok {
			cmdFit = mode
			break
		}
	}
	ctrlFit := ""
	for _, mode := range []string{"controlCharacters", "withoutControl", "nothing"} {
		ok := true
		for s := 0; s < build.States && ok; s++ {
			if s&build.StControl == 0 || s&build.StCommand != 0 {
				continue
			}
			want := table{}
			if mode != "nothing" {
				for c, cl := range view(s &^ build.StControl) {
					if mode == "controlCharacters" && len(cl.text) == 1 {
						if ch := cl.text[0]; ch >= 'a' && ch <= 'z' {
							cl.text = string(rune(ch - 'a' + 1))
						} else if ch >= 'A' && ch <= 'Z' {
							cl.text = string(rune(ch - 'A' + 1))
						}
					}
					want[c] = cl
				}
			}
			ok = equal(view(s), want)
		}
		if ok {
			ctrlFit = mode
			break
		}
	}
	l := &model.Layout{Kbl: model.FormatVersion, Positions: map[string]*model.Position{}}
	l.Metadata.Name = k.Name
	if id == "" {
		parts := strings.Split(build.Slug(k.Name), "-")
		for i, p := range parts {
			if len(p) > 8 {
				parts[i] = p[:8] // a private-use subtag has at most 8 characters (BCP 47)
			}
		}
		id = "und-x-" + strings.Join(parts, "-")
	}
	l.Metadata.ID = id
	l.Metadata.Provenance = &model.Provenance{Source: name, Format: "keylayout"}
	f := false
	// a .keylayout types its outputs exactly: no normalisation (macOS does not normalise while typing)
	l.Settings = &model.Settings{CapsLockFallback: &f, Normalization: "disabled"}
	mt.CommandMap, mt.ControlMap = "withoutCommand", "controlCharacters" // pinned also when layers serve every state
	if cmdFit != "" {
		mt.CommandMap = cmdFit
	} else {
		l.CustomModifiers = map[string]*model.CustomModifier{"cmd": {Kind: "held", Positions: []string{"MetaLeft", "MetaRight"}}}
		l.Positions["MetaLeft"], l.Positions["MetaRight"] = &model.Position{}, &model.Position{}
		mt.Modifiers = &model.MacOSModifiers{Command: "cmd"}
	}
	if ctrlFit != "" {
		mt.ControlMap = ctrlFit
	}
	// layers: one per key map some remaining state uses, in key-map order
	byIndex := map[int][]int{}
	for s := 0; s < build.States; s++ {
		if s&build.StCommand != 0 && cmdFit != "" || s&build.StControl != 0 && s&build.StCommand == 0 && ctrlFit != "" {
			continue
		}
		byIndex[im.idx[s]] = append(byIndex[im.idx[s]], s)
	}
	var indices []int
	for i := range byIndex {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	l.Hardware = &model.Hardware{}
	for _, i := range indices {
		layer := &model.HardwareLayer{Bindings: map[string]*model.Binding{}}
		for _, s := range byIndex[i] {
			layer.Modifiers = append(layer.Modifiers, setOf(s))
		}
		v := view(byIndex[i][0])
		for c, cl := range v {
			p := posOf[c]
			if cl.dead != "" {
				layer.Bindings[p] = &model.Binding{Kind: model.BindDeadKey, DeadKey: cl.dead}
			} else {
				layer.Bindings[p] = model.TextBinding(cl.text)
			}
		}
		if std {
			for c, p := range posOf {
				if row, ok := keycode.ForPosition(p); ok && row.Output != "" && !leftOut[c] {
					if _, has := layer.Bindings[p]; !has {
						layer.Bindings[p] = &model.Binding{Kind: model.BindOutput, Output: model.NewText("")}
					}
				}
			}
		}
		l.Hardware.Layers = append(l.Hardware.Layers, layer)
	}
	for c, p := range posOf {
		if !leftOut[c] {
			l.Positions[p] = &model.Position{}
		}
	}
	for p := range pins {
		if mt.Keys == nil {
			mt.Keys = map[string]*model.MacOSKey{}
		}
		mt.Keys[p] = &model.MacOSKey{KeyCode: pinCode(posOf, p)}
	}
	im.deadKeys(l, mt)
	// pins of coverage/macos.md §6
	if stateNameOK(k.Name) {
		mt.LayoutName = k.Name
	}
	g, kid := k.Group, k.ID
	mt.Group, mt.ID = &g, &kid
	if len(im.ranges) > 0 {
		var lines []int
		for ln := range im.ranges {
			lines = append(lines, ln)
		}
		sort.Ints(lines)
		im.lost("macos.rangeActionDropped", lines[0], map[string]any{"lines": lines}, "%d range actions (through, multiplier) have no counterpart in deadKeys", len(lines))
	}
	if len(im.shapes) > 0 {
		d := map[string]any{}
		for key, v := range im.shapes {
			d[key] = v
		}
		im.lost("macos.stateNotExpressible", 0, d, "state-machine shapes the dead-key model cannot say were imported as close as it can")
	}
	l.Targets = &model.Targets{MacOS: mt}
	// maxout: pinned when present and enough for what the build writes
	if k.MaxOut != nil {
		if res, _ := build.Build(l, build.Options{Policy: &model.OnLoss{Default: "accept"}}); res != nil && *res.Keyboard.MaxOut <= *k.MaxOut {
			m := *k.MaxOut
			mt.MaxOut = &m
		}
	}
	return l, im.r
}

func pinCode(posOf map[int]string, p string) int {
	for c, q := range posOf {
		if q == p {
			return c
		}
	}
	return 0
}

// stateNameOK reports a valid targets.macos file name (MacosFileName).
func stateNameOK(s string) bool {
	n := 0
	for _, r := range s {
		if r == '/' || r == ':' || r < 0x20 || r == 0x7F {
			return false
		}
		n++
	}
	return n >= 1 && n <= 128
}

// setOf is the kbl modifier set of a build state.
func setOf(s int) model.ModifierSet {
	set := model.ModifierSet{}
	if s&build.StShift != 0 {
		set = append(set, "shift")
	}
	if s&build.StOption != 0 {
		set = append(set, "alt")
	}
	if s&build.StControl != 0 {
		set = append(set, "ctrl")
	}
	if s&build.StCaps != 0 {
		set = append(set, "caps")
	}
	if s&build.StCommand != 0 {
		set = append(set, "cmd")
	}
	return set
}

func equal(a, b table) bool {
	if len(a) != len(b) {
		return false
	}
	for c, x := range a {
		if b[c] != x {
			return false
		}
	}
	return true
}

// states evaluates the modifier map over the 32 build states and reads each key map (coverage/macos.md §3).
func (im *importer) states() {
	sideDiff := map[string]bool{}
	for s := 0; s < build.States; s++ {
		m := macState(s)
		im.idx[s] = im.mods.Index(m)
		for _, side := range [][2]keylayout.State{{keylayout.Shift, keylayout.RightShift}, {keylayout.Option, keylayout.RightOption}, {keylayout.Control, keylayout.RightControl}} {
			if m&side[0] != 0 && im.mods.Index(m&^side[0]|side[1]) != im.idx[s] {
				sideDiff[fmt.Sprintf("%v", build.Words(s))] = true
			}
		}
		if _, ok := im.tables[im.idx[s]]; !ok {
			im.tables[im.idx[s]] = im.keyMap(im.idx[s])
		}
	}
	if len(sideDiff) > 0 {
		var list []string
		for k := range sideDiff {
			list = append(list, k)
		}
		sort.Strings(list)
		im.lost("macos.sideModifierDropped", im.mods.Line, map[string]any{"states": list}, "%d states give a right-side key another key map than the left-side one; the left side's is kept", len(list))
	}
}

func (im *importer) keyMap(index int) table {
	keys := im.k.Keys(im.lay.MapSet, index)
	im.keys[index] = keys
	t := table{}
	for code, key := range keys {
		t[code] = im.baseCell(key)
	}
	return t
}

// baseCell is what a key does in the base state.
func (im *importer) baseCell(key keylayout.Key) cell {
	if key.Output != nil {
		return cell{text: *key.Output}
	}
	a := im.k.KeyAction(key)
	if a == nil {
		return cell{}
	}
	for _, w := range a.Whens {
		if w.Range() {
			im.ranges[w.Line] = true
		}
	}
	w := a.When("none")
	if w == nil {
		im.shape(2, fmt.Sprintf("line %d: an action without a when for state none", a.Line))
		return cell{}
	}
	if w.Output != nil {
		if w.Next != "" && w.Next != "none" {
			im.shape(1, fmt.Sprintf("line %d: output and next together", w.Line))
		}
		return cell{text: *w.Output}
	}
	if w.Next != "" && w.Next != "none" {
		return cell{dead: im.ids[w.Next]}
	}
	return cell{}
}

// positions maps every used key code to a position (the table in reverse, rule isoKeys in reverse), and returns
// the positions that need a keyCode pin.
func (im *importer) positions(used map[int]bool) (map[int]string, map[string]bool) {
	posOf := map[int]string{}
	pins := map[string]bool{}
	var codes []int
	for c := range used {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	var unknown, dropped []int
	for _, c := range codes {
		switch {
		case c == 10 && used[50]:
			posOf[c] = "Backquote"
		case c == 50 && used[10]:
			posOf[c] = "IntlBackslash"
		case c == 10:
			posOf[c] = "IntlBackslash"
			pins["IntlBackslash"] = true
		default:
			if row, ok := keycode.ForCode(c); ok {
				posOf[c] = row.Position
			} else if c <= 255 {
				p := fmt.Sprintf("MacKey%d", c)
				posOf[c] = p
				pins[p] = true
				unknown = append(unknown, c)
			} else {
				dropped = append(dropped, c)
			}
		}
	}
	if len(unknown) > 0 {
		im.lost("macos.keyCodeUnknown", 0, map[string]any{"codes": unknown}, "%d key codes no row of the position table names became positions MacKey<code> with a keyCode pin", len(unknown))
	}
	if len(dropped) > 0 {
		im.lost("macos.keyCodeUnknown", 0, map[string]any{"codes": dropped}, "%d key codes above 255 have no position and were dropped", len(dropped))
	}
	return posOf, pins
}
