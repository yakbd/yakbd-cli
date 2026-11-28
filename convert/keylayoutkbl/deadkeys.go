package keylayoutkbl

import (
	"fmt"
	"sort"

	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/model"
)

// deadKeys imports the states, actions and terminators (coverage/macos.md §4).
func (im *importer) deadKeys(l *model.Layout, mt *model.MacOSTarget) {
	k := im.k
	// every action the imported key maps use, in key-map order and ascending key code
	type use struct {
		code int
		key  keylayout.Key
		a    *keylayout.Action
	}
	var uses []use
	var indices []int
	for i := range im.keys {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	seenAction := map[*keylayout.Action]bool{}
	for _, i := range indices {
		var codes []int
		for c := range im.keys[i] {
			codes = append(codes, c)
		}
		sort.Ints(codes)
		for _, c := range codes {
			key := im.keys[i][c]
			a := k.KeyAction(key)
			if a != nil && seenAction[a] {
				uses = append(uses, use{c, key, nil})
				continue
			}
			if a != nil {
				seenAction[a] = true
			}
			uses = append(uses, use{c, key, a})
		}
	}
	// states: every state some when of an imported key enters through next
	states := map[string]bool{}
	allActions := map[*keylayout.Action]bool{} // the actions of the imported key maps
	for _, u := range uses {
		if u.a != nil {
			allActions[u.a] = true
		}
	}
	for a := range allActions {
		for _, w := range a.Whens {
			if w.Next != "" && w.Next != "none" && !w.Range() {
				states[w.Next] = true
			}
		}
	}
	if len(states) == 0 {
		return
	}
	l.DeadKeys = map[string]*model.DeadKey{}
	mt.DeadKeys = map[string]*model.MacOSDeadKey{}
	for st := range states {
		dk := &model.DeadKey{Entries: []model.DeadKeyEntry{}}
		if t, ok := k.Terminator(st); ok {
			dk.Spacing = model.TextOrUnits{Text: t}
		} else {
			dk.NoMatch = "emitText"
		}
		l.DeadKeys[im.ids[st]] = dk
		if stateNameRE.MatchString(st) {
			mt.DeadKeys[im.ids[st]] = &model.MacOSDeadKey{State: st}
		}
	}
	// entries: the first key (key-map order, ascending code) with a base decides
	type entryKey struct{ state, base string }
	results := map[entryKey]model.Value{}
	baseOf := func(c cell) (model.Value, string) {
		if c.dead != "" {
			return model.Value{Kind: model.ValueDeadKey, DeadKey: c.dead}, "dead:" + c.dead
		}
		return model.Value{Kind: model.ValueText, Text: c.text}, "text:" + c.text
	}
	var stateList []string
	for st := range states {
		stateList = append(stateList, st)
	}
	sort.Strings(stateList)
	for _, u := range uses {
		if u.a == nil {
			continue
		}
		bc := im.baseCell(u.key)
		if bc.none() {
			continue
		}
		base, bk := baseOf(bc)
		for _, w := range u.a.Whens {
			if w.State == "none" || w.Range() || !states[w.State] {
				continue
			}
			var res model.Value
			switch {
			case w.Output != nil:
				if w.Next != "" && w.Next != "none" {
					im.shape(1, fmt.Sprintf("line %d: output and next together", w.Line))
				}
				res = model.Value{Kind: model.ValueText, Text: *w.Output}
			case w.Next != "" && w.Next != "none":
				res = model.Value{Kind: model.ValueDeadKey, DeadKey: im.ids[w.Next]}
			default:
				res = model.Value{Kind: model.ValueText, Text: ""}
			}
			ek := entryKey{w.State, bk}
			if prev, ok := results[ek]; ok {
				if prev.Kind != res.Kind || prev.Text != res.Text || prev.DeadKey != res.DeadKey {
					im.shape(3, fmt.Sprintf("state %s: keys with the same base %s give different results", w.State, bk))
				}
				continue
			}
			results[ek] = res
			dk := l.DeadKeys[im.ids[w.State]]
			dk.Entries = append(dk.Entries, model.DeadKeyEntry{Base: base, Result: res})
		}
	}
	// keys whose base has an entry they do not share (plain outputs, actions without that when)
	for _, i := range indices {
		for c, key := range im.keys[i] {
			bc := im.baseCell(key)
			if bc.none() {
				continue
			}
			_, bk := baseOf(bc)
			a := k.KeyAction(key)
			for _, st := range stateList {
				_, has := results[entryKey{st, bk}]
				var w *keylayout.When
				if a != nil {
					w = a.When(st)
				}
				switch {
				case has && w == nil:
					im.shape(3, fmt.Sprintf("state %s: key code %d types %s like keys with an entry, but has no when for the state", st, c, bk))
				case !has && w == nil && bc.dead != "":
					im.shape(4, fmt.Sprintf("state %s: key code %d starts the dead key %s without a when for the state", st, c, bc.dead))
				}
			}
		}
	}
}

// deadIDs gives every state name of the file a dead-key id: the name with white space and commas turned into '_',
// cut to 64 characters, made unique with a number (the file's names in sorted order).
func deadIDs(k *keylayout.Keyboard) map[string]string {
	names := map[string]bool{}
	add := func(a *keylayout.Action) {
		for _, w := range a.Whens {
			if w.Next != "" && w.Next != "none" {
				names[w.Next] = true
			}
		}
	}
	for i := range k.Actions {
		add(&k.Actions[i])
	}
	for _, s := range k.KeyMapSets {
		for _, km := range s.KeyMaps {
			for _, key := range km.Keys {
				if key.Inline != nil {
					add(key.Inline)
				}
			}
		}
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	out := map[string]string{}
	taken := map[string]bool{}
	for _, n := range sorted {
		r := []rune(n)
		for i, c := range r {
			if c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == 0x85 || c == 0xA0 || c >= 0x2000 && c <= 0x200A || c == 0x2028 || c == 0x2029 || c == 0x202F || c == 0x205F || c == 0x3000 || c == 0x1680 || c == 0x0B || c == 0x0C {
				r[i] = '_'
			}
		}
		if len(r) > 60 {
			r = r[:60]
		}
		id := string(r)
		for i := 2; taken[id]; i++ {
			id = fmt.Sprintf("%s_%d", string(r), i)
		}
		taken[id] = true
		out[n] = id
	}
	return out
}
