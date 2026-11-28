package keylayout

// Typist types on a keyboard the way the TN2056 state machine does, for tests and conversions: the first <layout>
// picks the modifier map and key map set, the modifier map picks the key map, and the key's action (or output)
// runs in the current state.
type Typist struct {
	k     *Keyboard
	mods  *ModifierMap
	set   string
	maps  map[int]map[int]Key
	State string // the current state; "" or "none" is the base state
}

// NewTypist returns a typist in the base state, or nil when the keyboard has no layout.
func NewTypist(k *Keyboard) *Typist {
	if len(k.Layouts) == 0 {
		return nil
	}
	l := k.Layouts[0]
	return &Typist{k: k, mods: k.ModifierMap(l.Modifiers), set: l.MapSet, maps: map[int]map[int]Key{}}
}

// Press types key code in modifier state s and returns the output. The rules (TN2056, and the macOS check's
// case dead-terminator): a key whose action has a <when> for the current state does what it says; any other key
// in a pending state first emits that state's terminator (if any) and then acts as in the base state. A key that
// does nothing leaves a pending state pending (unmeasured; the kbl model's reading). Range actions are not run.
func (t *Typist) Press(code int, s State) string {
	if t.mods == nil {
		return ""
	}
	idx := t.mods.Index(s)
	keys, ok := t.maps[idx]
	if !ok {
		keys = t.k.Keys(t.set, idx)
		t.maps[idx] = keys
	}
	key, ok := keys[code]
	if !ok {
		return ""
	}
	cur := t.State
	if cur == "" {
		cur = "none"
	}
	out := ""
	if a := t.k.KeyAction(key); a != nil {
		if w := a.When(cur); w != nil {
			return t.apply(w)
		}
		if cur != "none" {
			out, _ = t.k.Terminator(cur)
			t.State = "none"
		}
		if w := a.When("none"); w != nil {
			return out + t.apply(w)
		}
		return out
	}
	if cur != "none" {
		out, _ = t.k.Terminator(cur)
		t.State = "none"
	}
	if key.Output != nil {
		out += *key.Output
	}
	return out
}

func (t *Typist) apply(w *When) string {
	t.State = w.Next
	if t.State == "" {
		t.State = "none"
	}
	if w.Output != nil {
		return *w.Output
	}
	return ""
}
