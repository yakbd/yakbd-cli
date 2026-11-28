package build

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/model"
)

// Element ids of the written file (the writer's choice, TN2056).
const (
	ModifierMapID = "modifiers"
	KeyMapSetID   = "keys"
)

// Words returns the <modifier keys> words of a build state (targets/macos.md §3).
func Words(s int) []string {
	var w []string
	if s&StShift != 0 {
		w = append(w, "anyShift")
	}
	if s&StOption != 0 {
		w = append(w, "anyOption")
	}
	if s&StControl != 0 {
		w = append(w, "anyControl")
	}
	if s&StCommand != 0 {
		w = append(w, "command")
	}
	if s&StCaps != 0 {
		w = append(w, "caps")
	}
	return w
}

// deadInfo is one dead key as the build writes it.
type deadInfo struct {
	id, state string
	spacing   string
	noMatch   string
	entries   []model.DeadKeyEntry
}

// keyboard writes the tables and dead keys as a .keylayout (targets/macos.md §3 to §5).
func (b *builder) keyboard() *keylayout.Keyboard {
	p := b.pin
	k := &keylayout.Keyboard{Group: *p.Group, ID: *p.ID, Name: p.LayoutName}
	for _, r := range p.HardwareRanges {
		k.Layouts = append(k.Layouts, keylayout.Layout{First: r.First, Last: r.Last, Modifiers: ModifierMapID, MapSet: KeyMapSetID})
	}
	dead := b.deadInfos()
	// which texts need an action: a base of some entry, or every text when a dead key expands its noMatch
	bases := map[string]bool{}
	expandAll := false
	var expanded []string
	for _, d := range dead {
		for _, e := range d.entries {
			if e.Base.Kind != model.ValueDeadKey {
				bases[d.valueText(e.Base)] = true
			}
		}
		if d.noMatch == "emitSpacing" || d.noMatch == "emitNothing" {
			expandAll = true
			expanded = append(expanded, d.id)
		}
	}
	// key maps: identical tables share one, numbered by first use
	var keyMaps []keylayout.KeyMap
	var sels []keylayout.KeyMapSelect
	index := map[string]int{}
	textActions := map[string]bool{}
	deadActions := map[string]bool{}
	for s := 0; s < States; s++ {
		t := b.tables[s]
		sig := signature(t)
		i, ok := index[sig]
		if !ok {
			i = len(keyMaps)
			index[sig] = i
			km := keylayout.KeyMap{Index: i}
			for _, code := range sortedCodes(t) {
				c := t[code]
				key := keylayout.Key{Code: code}
				switch c.Kind {
				case Text:
					if bases[c.Text] || expandAll {
						key.Action = textActionID(c.Text)
						textActions[c.Text] = true
					} else {
						o := c.Text
						key.Output = &o
					}
				case Dead:
					key.Action = "dead_" + b.states[c.Dead]
					deadActions[c.Dead] = true
				default:
					continue
				}
				km.Keys = append(km.Keys, key)
			}
			keyMaps = append(keyMaps, km)
			sels = append(sels, keylayout.KeyMapSelect{MapIndex: i})
		}
		sels[i].Modifiers = append(sels[i].Modifiers, keylayout.Modifier{Keys: Words(s)})
	}
	k.ModifierMaps = []keylayout.ModifierMap{{ID: ModifierMapID, DefaultIndex: 0, Selects: sels}}
	k.KeyMapSets = []keylayout.KeyMapSet{{ID: KeyMapSetID, KeyMaps: keyMaps}}
	// actions: dead keys by state name, then texts by id
	chainedExpanded := false
	var deadIDs []string
	for id := range deadActions {
		deadIDs = append(deadIDs, id)
	}
	sort.Slice(deadIDs, func(i, j int) bool { return b.states[deadIDs[i]] < b.states[deadIDs[j]] })
	for _, e := range deadIDs {
		a := keylayout.Action{ID: "dead_" + b.states[e], Whens: []keylayout.When{{State: "none", Next: b.states[e]}}}
		spE := b.spacingOf(e)
		for _, d := range dead {
			w := keylayout.When{State: d.state}
			if en := d.entry(model.Value{Kind: model.ValueDeadKey, DeadKey: e}, d); en != nil {
				b.setResult(&w, en.Result, d)
			} else {
				chainedExpanded = true
				switch d.noMatch {
				case "emitText":
					setOutput(&w, spE)
				case "emitSpacing":
					setOutput(&w, d.spacing)
				case "emitNothing":
					w.Next = "none"
				default:
					setOutput(&w, d.spacing+spE)
				}
			}
			a.Whens = append(a.Whens, w)
		}
		k.Actions = append(k.Actions, a)
	}
	var texts []string
	for t := range textActions {
		texts = append(texts, t)
	}
	sort.Slice(texts, func(i, j int) bool { return textActionID(texts[i]) < textActionID(texts[j]) })
	for _, t := range texts {
		o := t
		a := keylayout.Action{ID: textActionID(t), Whens: []keylayout.When{{State: "none", Output: &o}}}
		for _, d := range dead {
			w := keylayout.When{State: d.state}
			if en := d.entry(model.Value{Kind: model.ValueText, Text: t}, d); en != nil {
				b.setResult(&w, en.Result, d)
			} else {
				switch d.noMatch {
				case "emitSpacing":
					setOutput(&w, d.spacing)
				case "emitNothing":
					w.Next = "none"
				default:
					continue
				}
			}
			a.Whens = append(a.Whens, w)
		}
		k.Actions = append(k.Actions, a)
	}
	for _, d := range dead {
		if (d.noMatch == "emitBoth" || d.noMatch == "emitSpacing") && d.spacing != "" {
			o := d.spacing
			k.Terminators = append(k.Terminators, keylayout.When{State: d.state, Output: &o})
		}
	}
	if chainedExpanded && len(dead) > 0 {
		for _, e := range deadIDs {
			expanded = append(expanded, e)
		}
	}
	if len(expanded) > 0 {
		e := b.lost("macos.deadKeyNoMatchExpanded", "/deadKeys", "noMatch and dead keys pressed while another is pending are compiled as explicit <when> elements")
		e.Detail = map[string]any{"deadKeys": dedupe(expanded)}
	}
	// maxout
	// maxout: what one key press can emit, a terminator and then a key's own output (rule maxout)
	longest := func(m *int, s *string) {
		if s != nil {
			if n := len(utf16.Encode([]rune(*s))); n > *m {
				*m = n
			}
		}
	}
	keyMost, termMost := 0, 0
	for _, km := range keyMaps {
		for _, key := range km.Keys {
			longest(&keyMost, key.Output)
		}
	}
	for _, a := range k.Actions {
		for _, w := range a.Whens {
			longest(&keyMost, w.Output)
		}
	}
	for _, w := range k.Terminators {
		longest(&termMost, w.Output)
	}
	most := max(1, keyMost+termMost)
	if b.file.MaxOut != nil {
		if *b.file.MaxOut < most {
			b.lost("macos.outputBeyondMaxout", tp+"maxout", "an output has %d UTF-16 units, more than maxout %d", most, *b.file.MaxOut)
		}
		most = *b.file.MaxOut
	} else {
		b.defaulted("maxout", []string{tp + "maxout"}, most, "maxout %d, the longest output", most)
	}
	b.pin.MaxOut = &most
	k.MaxOut = &most
	return k
}

func setOutput(w *keylayout.When, s string) {
	if s == "" {
		w.Next = "none"
		return
	}
	w.Output = &s
}

func (b *builder) setResult(w *keylayout.When, v model.Value, d *deadInfo) {
	if v.Kind == model.ValueDeadKey {
		w.Next = b.states[v.DeadKey]
		return
	}
	setOutput(w, d.valueText(v))
}

// deadInfos are the layout's dead keys in ascending state-name order, outputs as built.
func (b *builder) deadInfos() []*deadInfo {
	var out []*deadInfo
	for _, id := range sortedKeys(b.l.DeadKeys) {
		dk := b.l.DeadKeys[id]
		path := "/deadKeys/" + model.Escape(id)
		d := &deadInfo{id: id, state: b.states[id], noMatch: dk.NoMatch, entries: append([]model.DeadKeyEntry(nil), dk.Entries...)} // a copy: the build never changes its input
		if d.noMatch == "" {
			d.noMatch = "emitBoth"
		}
		d.spacing, _ = b.text(dk.Spacing.UTF16(), dk.Spacing.Units != nil, path+"/spacing")
		// results and bases as built
		for i := range d.entries {
			e := &d.entries[i]
			for j, v := range []*model.Value{&e.Base, &e.Result} {
				if v.Kind == model.ValueDeadKey {
					continue
				}
				part := "/base"
				if j == 1 {
					part = "/result"
				}
				t, _ := b.text(v.UTF16(), v.Kind == model.ValueUnits, fmt.Sprintf("%s/entries/%d%s", path, i, part))
				*v = model.Value{Kind: model.ValueText, Text: t}
			}
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].state < out[j].state })
	return out
}

func (d *deadInfo) valueText(v model.Value) string { return v.Text }

// entry returns the first entry of d whose base is v.
func (d *deadInfo) entry(v model.Value, _ *deadInfo) *model.DeadKeyEntry {
	for i := range d.entries {
		e := &d.entries[i]
		if e.Base.Kind == v.Kind && (v.Kind == model.ValueDeadKey && e.Base.DeadKey == v.DeadKey || v.Kind != model.ValueDeadKey && e.Base.Text == v.Text) {
			return e
		}
	}
	return nil
}

func (b *builder) spacingOf(id string) string {
	dk := b.l.DeadKeys[id]
	if dk == nil {
		return ""
	}
	s, _ := b.text(dk.Spacing.UTF16(), dk.Spacing.Units != nil, "/deadKeys/"+model.Escape(id)+"/spacing")
	return s
}

// textActionID is the id of the action of keys that type t: "t" and its UTF-16 units in hex.
func textActionID(t string) string {
	var s strings.Builder
	s.WriteString("t")
	for i, c := range utf16.Encode([]rune(t)) {
		if i > 0 {
			s.WriteByte('_')
		}
		fmt.Fprintf(&s, "%04X", c)
	}
	return s.String()
}

func signature(t Table) string {
	var s strings.Builder
	for _, c := range sortedCodes(t) {
		cell := t[c]
		fmt.Fprintf(&s, "%d:%d:%q:%q;", c, cell.Kind, cell.Text, cell.Dead)
	}
	return s.String()
}

func sortedCodes(t Table) []int {
	out := make([]int, 0, len(t))
	for c, cell := range t {
		if cell.Kind == Text || cell.Kind == Dead {
			out = append(out, c)
		}
	}
	sort.Ints(out)
	return out
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func decodeIcon(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}
