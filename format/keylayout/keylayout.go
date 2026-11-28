// Package keylayout reads and writes macOS keyboard layouts, the `.keylayout` XML files of Apple's Technical Note
// TN2056 ("Installing and Creating Keyboard Layouts in Mac OS X"): modifier maps, key map sets, actions with their
// dead-key states, terminators, and the keyboard's group, id, name and maxout.
//
// Read turns the bytes of a file into a Keyboard, a faithful in-memory form of its elements, and a loss.Report with
// every diagnostic (each with its line). Write turns a Keyboard back into XML in one fixed form, so that writing what
// was read from the writer's own output gives the same bytes. The package knows nothing about .kbl.json; the
// conversion is convert/keylayoutkbl, the semantics of modifiers and key maps it needs are here (semantics.go).
package keylayout

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
)

// Keyboard is the root element <keyboard>.
type Keyboard struct {
	Group int    // the script code; 126 is Unicode
	ID    int    // the keyboard id; negative for Unicode keyboards
	Name  string // the name in the input menu
	// MaxOut is the most UTF-16 units one key press produces; nil when the file has no maxout.
	MaxOut       *int
	Layouts      []Layout
	ModifierMaps []ModifierMap
	KeyMapSets   []KeyMapSet
	Actions      []Action // the named actions of <actions>
	Terminators  []When   // <terminators>: state and output only
	Line         int
}

// Layout is one <layout>: the modifier map and key map set for a range of hardware keyboard types.
type Layout struct {
	First, Last int
	Modifiers   string // a ModifierMap id
	MapSet      string // a KeyMapSet id
	Line        int
}

// ModifierMap is one <modifierMap>.
type ModifierMap struct {
	ID           string
	DefaultIndex int
	Selects      []KeyMapSelect
	Line         int
}

// KeyMapSelect is one <keyMapSelect>: the modifier combinations that select the key map MapIndex.
type KeyMapSelect struct {
	MapIndex  int
	Modifiers []Modifier
	Line      int
}

// Modifier is one <modifier keys="...">: its words as written ("anyShift", "caps?").
type Modifier struct {
	Keys []string
	Line int
}

// KeyMapSet is one <keyMapSet>.
type KeyMapSet struct {
	ID      string
	KeyMaps []KeyMap
	Line    int
}

// KeyMap is one <keyMap>. A key map with a base takes every key of the base key map it does not define itself.
type KeyMap struct {
	Index      int
	BaseMapSet string // "" for none
	BaseIndex  int
	Keys       []Key
	Line       int
}

// Key is one <key>: exactly one of Output, Action and Inline is set.
type Key struct {
	Code   int
	Output *string
	Action string  // a named action's id
	Inline *Action // an anonymous action inside the key
	Line   int
}

// Action is one <action>: what a key does in each state. Inline actions have no ID.
type Action struct {
	ID    string
	Whens []When
	Line  int
}

// When is one <when> of an action or of <terminators>. Empty strings and nil mean the attribute is absent.
type When struct {
	State      string
	Through    string // the end of a range action (a number)
	Output     *string
	Multiplier string
	Next       string
	Line       int
}

// Range reports whether the element is a range action (TN2056's form with through and multiplier).
func (w When) Range() bool { return w.Through != "" || w.Multiplier != "" }

// Limits that keep a hostile file from making the reader allocate without bound.
const (
	MaxKeyCode = 65535 // UCKeyTranslate takes a 16-bit key code
	maxIndex   = 255
)

type reader struct {
	name string
	r    *loss.Report
}

func (p *reader) at(line int) loss.Where { return loss.Where{File: p.name, Line: line} }

func (p *reader) errf(line int, code, format string, args ...any) {
	p.r.Errorf(code, p.at(line), format, args...)
}

func (p *reader) warnf(line int, code, format string, args ...any) {
	p.r.Warnf(code, p.at(line), format, args...)
}

// Read parses a .keylayout file. A nil Keyboard or an error entry means the file cannot be used.
func Read(data []byte, name string) (*Keyboard, *loss.Report) {
	p := &reader{name: name, r: &loss.Report{}}
	text, err := decodeText(data)
	if err != nil {
		p.errf(0, "keylayout.encoding", "%v", err)
		return nil, p.r
	}
	root, err := parseXML(text)
	if err != nil {
		line := 0
		if se, ok := err.(*syntaxError); ok {
			line, err = se.line, fmt.Errorf("%s", se.msg)
		}
		p.errf(line, "keylayout.xml", "%v", err)
		return nil, p.r
	}
	if root.name != "keyboard" {
		p.errf(root.line, "keylayout.element", "the root element is <%s>, not <keyboard>", root.name)
		return nil, p.r
	}
	k := p.keyboard(root)
	if p.r.HasErrors() {
		return nil, p.r
	}
	p.check(k)
	if p.r.HasErrors() {
		return nil, p.r
	}
	return k, p.r
}

// attrs checks that n has only the attributes allowed (a trailing "!" marks a required one).
func (p *reader) attrs(n *node, allowed ...string) {
	ok := map[string]bool{}
	for _, a := range allowed {
		a, req := strings.CutSuffix(a, "!")
		ok[a] = true
		if _, has := n.get(a); req && !has {
			p.errf(n.line, "keylayout.attribute", "<%s> without the required attribute %s", n.name, a)
		}
	}
	for _, a := range n.attrs {
		if !ok[a.name] {
			p.warnf(n.line, "keylayout.attribute", "<%s>: unknown attribute %s ignored", n.name, a.name)
		}
	}
}

func (p *reader) int(n *node, name string, lo, hi int) int {
	s, ok := n.get(name)
	if !ok {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v < lo || v > hi {
		p.errf(n.line, "keylayout.attribute", "<%s %s=%q>: an integer from %d to %d expected", n.name, name, s, lo, hi)
		return 0
	}
	return v
}

func (p *reader) children(n *node, allowed ...string) {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, c := range n.children {
		if !ok[c.name] {
			p.warnf(c.line, "keylayout.element", "<%s> inside <%s> ignored", c.name, n.name)
		}
	}
}

func (p *reader) keyboard(n *node) *Keyboard {
	p.attrs(n, "group!", "id!", "name!", "maxout")
	k := &Keyboard{Line: n.line}
	k.Group = p.int(n, "group", -32768, 32767)
	k.ID = p.int(n, "id", -32768, 32767)
	k.Name, _ = n.get("name")
	if _, ok := n.get("maxout"); ok {
		v := p.int(n, "maxout", 1, 255)
		k.MaxOut = &v
	}
	p.children(n, "layouts", "modifierMap", "keyMapSet", "actions", "terminators")
	var nLayouts, nActions, nTerm int
	for _, c := range n.children {
		switch c.name {
		case "layouts":
			nLayouts++
			p.attrs(c)
			p.children(c, "layout")
			for _, l := range c.children {
				if l.name != "layout" {
					continue
				}
				p.attrs(l, "first!", "last!", "modifiers!", "mapSet!")
				lay := Layout{Line: l.line, First: p.int(l, "first", 0, 65535), Last: p.int(l, "last", 0, 65535)}
				lay.Modifiers, _ = l.get("modifiers")
				lay.MapSet, _ = l.get("mapSet")
				k.Layouts = append(k.Layouts, lay)
			}
		case "modifierMap":
			k.ModifierMaps = append(k.ModifierMaps, p.modifierMap(c))
		case "keyMapSet":
			k.KeyMapSets = append(k.KeyMapSets, p.keyMapSet(c))
		case "actions":
			nActions++
			p.attrs(c)
			p.children(c, "action")
			for _, a := range c.children {
				if a.name == "action" {
					k.Actions = append(k.Actions, p.action(a, true))
				}
			}
		case "terminators":
			nTerm++
			p.attrs(c)
			p.children(c, "when")
			for _, w := range c.children {
				if w.name == "when" {
					p.attrs(w, "state!", "output!")
					k.Terminators = append(k.Terminators, p.when(w))
				}
			}
		}
	}
	if nLayouts != 1 {
		p.errf(n.line, "keylayout.element", "%d <layouts> elements (exactly one expected)", nLayouts)
	}
	if nActions > 1 || nTerm > 1 {
		p.errf(n.line, "keylayout.element", "more than one <actions> or <terminators>")
	}
	if len(k.Layouts) == 0 && nLayouts == 1 {
		p.errf(n.line, "keylayout.element", "<layouts> without a <layout>")
	}
	if len(k.ModifierMaps) == 0 {
		p.errf(n.line, "keylayout.element", "no <modifierMap>")
	}
	if len(k.KeyMapSets) == 0 {
		p.errf(n.line, "keylayout.element", "no <keyMapSet>")
	}
	return k
}

func (p *reader) modifierMap(n *node) ModifierMap {
	p.attrs(n, "id!", "defaultIndex!")
	m := ModifierMap{Line: n.line, DefaultIndex: p.int(n, "defaultIndex", 0, maxIndex)}
	m.ID, _ = n.get("id")
	p.children(n, "keyMapSelect")
	for _, s := range n.children {
		if s.name != "keyMapSelect" {
			continue
		}
		p.attrs(s, "mapIndex!")
		sel := KeyMapSelect{Line: s.line, MapIndex: p.int(s, "mapIndex", 0, maxIndex)}
		p.children(s, "modifier")
		for _, c := range s.children {
			if c.name != "modifier" {
				continue
			}
			p.attrs(c, "keys!")
			v, _ := c.get("keys")
			mod := Modifier{Line: c.line, Keys: strings.Fields(v)}
			for _, w := range mod.Keys {
				if _, ok := modifierWords[strings.TrimSuffix(w, "?")]; !ok {
					p.errf(c.line, "keylayout.modifier", "unknown modifier %q", w)
				}
			}
			sel.Modifiers = append(sel.Modifiers, mod)
		}
		m.Selects = append(m.Selects, sel)
	}
	return m
}

func (p *reader) keyMapSet(n *node) KeyMapSet {
	p.attrs(n, "id!")
	s := KeyMapSet{Line: n.line}
	s.ID, _ = n.get("id")
	p.children(n, "keyMap")
	for _, c := range n.children {
		if c.name != "keyMap" {
			continue
		}
		p.attrs(c, "index!", "baseMapSet", "baseIndex")
		km := KeyMap{Line: c.line, Index: p.int(c, "index", 0, maxIndex)}
		km.BaseMapSet, _ = c.get("baseMapSet")
		_, hasIndex := c.get("baseIndex")
		if (km.BaseMapSet != "") != hasIndex {
			p.errf(c.line, "keylayout.attribute", "<keyMap>: baseMapSet and baseIndex go together")
		}
		km.BaseIndex = p.int(c, "baseIndex", 0, maxIndex)
		p.children(c, "key")
		for _, kn := range c.children {
			if kn.name != "key" {
				continue
			}
			p.attrs(kn, "code!", "output", "action")
			key := Key{Line: kn.line, Code: p.int(kn, "code", 0, MaxKeyCode)}
			if o, ok := kn.get("output"); ok {
				key.Output = &o
			}
			key.Action, _ = kn.get("action")
			p.children(kn, "action")
			for _, a := range kn.children {
				if a.name == "action" {
					if key.Inline != nil {
						p.errf(a.line, "keylayout.element", "<key> with more than one inline <action>")
					}
					act := p.action(a, false)
					key.Inline = &act
				}
			}
			forms := 0
			if key.Output != nil {
				forms++
			}
			if key.Action != "" {
				forms++
			}
			if key.Inline != nil {
				forms++
			}
			if forms != 1 {
				p.errf(kn.line, "keylayout.key", "<key code=\"%d\">: exactly one of output, action and an inline <action> expected", key.Code)
			}
			km.Keys = append(km.Keys, key)
		}
		s.KeyMaps = append(s.KeyMaps, km)
	}
	return s
}

func (p *reader) action(n *node, named bool) Action {
	if named {
		p.attrs(n, "id!")
	} else {
		p.attrs(n)
	}
	a := Action{Line: n.line}
	a.ID, _ = n.get("id")
	p.children(n, "when")
	for _, w := range n.children {
		if w.name == "when" {
			p.attrs(w, "state!", "through", "output", "multiplier", "next")
			a.Whens = append(a.Whens, p.when(w))
		}
	}
	if len(a.Whens) == 0 {
		p.errf(n.line, "keylayout.action", "<action %s> without a <when>", a.ID)
	}
	return a
}

func (p *reader) when(n *node) When {
	w := When{Line: n.line}
	w.State, _ = n.get("state")
	w.Through, _ = n.get("through")
	w.Multiplier, _ = n.get("multiplier")
	w.Next, _ = n.get("next")
	if o, ok := n.get("output"); ok {
		w.Output = &o
	}
	if w.State == "" {
		p.errf(n.line, "keylayout.when", "<when> with an empty state")
	}
	return w
}

// check checks the references and uniqueness rules of TN2056.
func (p *reader) check(k *Keyboard) {
	mods := map[string]bool{}
	for _, m := range k.ModifierMaps {
		if mods[m.ID] {
			p.errf(m.Line, "keylayout.duplicate", "two <modifierMap id=%q>", m.ID)
		}
		mods[m.ID] = true
	}
	sets := map[string]*KeyMapSet{}
	for i := range k.KeyMapSets {
		s := &k.KeyMapSets[i]
		if sets[s.ID] != nil {
			p.errf(s.Line, "keylayout.duplicate", "two <keyMapSet id=%q>", s.ID)
		}
		sets[s.ID] = s
	}
	actions := map[string]bool{}
	for _, a := range k.Actions {
		if actions[a.ID] {
			p.errf(a.Line, "keylayout.duplicate", "two <action id=%q>", a.ID)
		}
		actions[a.ID] = true
	}
	for _, l := range k.Layouts {
		if !mods[l.Modifiers] {
			p.errf(l.Line, "keylayout.ref", "<layout modifiers=%q>: no such modifierMap", l.Modifiers)
		}
		if sets[l.MapSet] == nil {
			p.errf(l.Line, "keylayout.ref", "<layout mapSet=%q>: no such keyMapSet", l.MapSet)
		}
	}
	for _, s := range k.KeyMapSets {
		idx := map[int]bool{}
		for _, km := range s.KeyMaps {
			if idx[km.Index] {
				p.errf(km.Line, "keylayout.duplicate", "two <keyMap index=\"%d\"> in %q", km.Index, s.ID)
			}
			idx[km.Index] = true
			if km.BaseMapSet != "" {
				if b := sets[km.BaseMapSet]; b == nil || b.keyMap(km.BaseIndex) == nil {
					p.errf(km.Line, "keylayout.ref", "<keyMap baseMapSet=%q baseIndex=\"%d\">: no such key map", km.BaseMapSet, km.BaseIndex)
				}
			}
			codes := map[int]bool{}
			for _, key := range km.Keys {
				if codes[key.Code] {
					p.errf(key.Line, "keylayout.duplicate", "two <key code=\"%d\"> in key map %d of %q", key.Code, km.Index, s.ID)
				}
				codes[key.Code] = true
				if key.Action != "" && !actions[key.Action] {
					p.errf(key.Line, "keylayout.ref", "<key action=%q>: no such action", key.Action)
				}
			}
		}
	}
	if p.r.HasErrors() {
		return
	}
	// a base chain must end
	for _, s := range k.KeyMapSets {
		for _, km := range s.KeyMaps {
			seen := map[string]bool{}
			cur := &km
			set := s.ID
			for cur.BaseMapSet != "" {
				key := fmt.Sprintf("%s/%d", set, cur.Index)
				if seen[key] {
					p.errf(km.Line, "keylayout.ref", "key map %d of %q: the baseMapSet chain is a cycle", km.Index, s.ID)
					break
				}
				seen[key] = true
				set = cur.BaseMapSet
				cur = sets[set].keyMap(cur.BaseIndex)
			}
		}
	}
	for _, m := range k.ModifierMaps {
		for _, sel := range m.Selects {
			for _, l := range k.Layouts {
				if l.Modifiers == m.ID && sets[l.MapSet].keyMap(sel.MapIndex) == nil {
					p.warnf(sel.Line, "keylayout.ref", "<keyMapSelect mapIndex=\"%d\">: key map set %q has no such key map", sel.MapIndex, l.MapSet)
				}
			}
		}
	}
}

func (s *KeyMapSet) keyMap(index int) *KeyMap {
	for i := range s.KeyMaps {
		if s.KeyMaps[i].Index == index {
			return &s.KeyMaps[i]
		}
	}
	return nil
}

// KeyMapSet returns the key map set with the id, or nil.
func (k *Keyboard) KeyMapSet(id string) *KeyMapSet {
	for i := range k.KeyMapSets {
		if k.KeyMapSets[i].ID == id {
			return &k.KeyMapSets[i]
		}
	}
	return nil
}

// ModifierMap returns the modifier map with the id, or nil.
func (k *Keyboard) ModifierMap(id string) *ModifierMap {
	for i := range k.ModifierMaps {
		if k.ModifierMaps[i].ID == id {
			return &k.ModifierMaps[i]
		}
	}
	return nil
}

// Action returns the named action with the id, or nil.
func (k *Keyboard) Action(id string) *Action {
	for i := range k.Actions {
		if k.Actions[i].ID == id {
			return &k.Actions[i]
		}
	}
	return nil
}
