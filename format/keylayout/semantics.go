package keylayout

import "strings"

// State is a macOS modifier state as a key map selection sees it: one bit per modifier word of TN2056.
type State uint8

// The bits of a State. Left and right keys are separate bits; Caps Lock is the lock state.
const (
	Shift State = 1 << iota
	RightShift
	Option
	RightOption
	Control
	RightControl
	Command
	Caps
)

// AllStates is the number of States.
const AllStates = 256

// modifierWords are the words of <modifier keys>, each with the bits it names (left, right). The "any" words
// match when either bit is set.
var modifierWords = map[string]struct {
	left, right State
	any         bool
}{
	"shift":        {Shift, 0, false},
	"rightShift":   {0, RightShift, false},
	"anyShift":     {Shift, RightShift, true},
	"option":       {Option, 0, false},
	"rightOption":  {0, RightOption, false},
	"anyOption":    {Option, RightOption, true},
	"control":      {Control, 0, false},
	"rightControl": {0, RightControl, false},
	"anyControl":   {Control, RightControl, true},
	"command":      {Command, 0, false},
	"caps":         {Caps, 0, false},
}

// pairs are the modifier pairs and single modifiers, each as its left and right bit.
var pairs = [][2]State{{Shift, RightShift}, {Option, RightOption}, {Control, RightControl}, {Command, 0}, {Caps, 0}}

// Matches reports whether a <modifier keys> list matches the state (TN2056): a word without "?" must be pressed,
// a word with "?" may be either, a modifier no word names must be up; "any" words are satisfied by either key.
func (m Modifier) Matches(s State) bool {
	type cond struct{ required, optional bool }
	conds := map[State]*cond{} // per bit
	anyReq := map[[2]State]bool{}
	anyOpt := map[[2]State]bool{}
	for _, w := range m.Keys {
		base, opt := strings.CutSuffix(w, "?")
		mw, ok := modifierWords[base]
		if !ok {
			return false
		}
		if mw.any {
			p := [2]State{mw.left, mw.right}
			if opt {
				anyOpt[p] = true
			} else {
				anyReq[p] = true
			}
			continue
		}
		bit := mw.left | mw.right
		c := conds[bit]
		if c == nil {
			c = &cond{}
			conds[bit] = c
		}
		if opt {
			c.optional = true
		} else {
			c.required = true
		}
	}
	for _, p := range pairs {
		if anyOpt[p] && !anyReq[p] {
			// irrelevant, unless a side word demands its key
			for _, b := range p {
				if c := conds[b]; b != 0 && c != nil && c.required && !c.optional && s&b == 0 {
					return false
				}
			}
			continue
		}
		if anyReq[p] && s&(p[0]|p[1]) == 0 {
			return false
		}
		for _, b := range p {
			if b == 0 {
				continue
			}
			c := conds[b]
			switch {
			case c != nil && c.optional:
			case c != nil && c.required:
				if s&b == 0 {
					return false
				}
			case anyReq[p]:
				// either side may carry the "any" word
			default:
				if s&b != 0 {
					return false
				}
			}
		}
	}
	return true
}

// Index returns the key map index the modifier map selects for a state: the last <keyMapSelect> with a matching
// <modifier> (later elements override earlier ones, TN2056), or defaultIndex.
func (m *ModifierMap) Index(s State) int {
	idx := m.DefaultIndex
	for _, sel := range m.Selects {
		for _, mod := range sel.Modifiers {
			if mod.Matches(s) {
				idx = sel.MapIndex
				break
			}
		}
	}
	return idx
}

// Keys returns the effective keys of key map index of the set, by code: the keys of its base key map (followed
// to the end of the chain) that it does not define itself, and its own. Nil when the set has no such key map.
func (k *Keyboard) Keys(set string, index int) map[int]Key {
	s := k.KeyMapSet(set)
	if s == nil {
		return nil
	}
	km := s.keyMap(index)
	if km == nil {
		return nil
	}
	out := map[int]Key{}
	if km.BaseMapSet != "" {
		guard := map[string]bool{}
		var walk func(set string, index int)
		walk = func(set string, index int) {
			key := set + "/" + string(rune(index))
			if guard[key] {
				return
			}
			guard[key] = true
			bs := k.KeyMapSet(set)
			if bs == nil {
				return
			}
			b := bs.keyMap(index)
			if b == nil {
				return
			}
			if b.BaseMapSet != "" {
				walk(b.BaseMapSet, b.BaseIndex)
			}
			for _, key := range b.Keys {
				out[key.Code] = key
			}
		}
		walk(km.BaseMapSet, km.BaseIndex)
	}
	for _, key := range km.Keys {
		out[key.Code] = key
	}
	return out
}

// KeyAction returns the action a key runs: its inline action or its named one; nil for an output key.
func (k *Keyboard) KeyAction(key Key) *Action {
	if key.Inline != nil {
		return key.Inline
	}
	if key.Action != "" {
		return k.Action(key.Action)
	}
	return nil
}

// Terminator returns the terminator output of a state and whether it has one.
func (k *Keyboard) Terminator(state string) (string, bool) {
	for _, w := range k.Terminators {
		if w.State == state && w.Output != nil {
			return *w.Output, true
		}
	}
	return "", false
}

// When returns the action's first <when> for a state (not a range), or nil.
func (a *Action) When(state string) *When {
	for i := range a.Whens {
		if a.Whens[i].State == state && !a.Whens[i].Range() {
			return &a.Whens[i]
		}
	}
	return nil
}
