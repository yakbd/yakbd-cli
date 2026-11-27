package model

import (
	"sort"
	"unicode/utf16"
)

// State is a modifier state (SPEC §7.3): held keys, Caps Lock, and the active custom components.
type State struct {
	Shift        bool
	CtrlL, CtrlR bool
	AltL, AltR   bool
	Caps         bool
	Custom       map[string]bool
}

// With returns a copy of s with Caps Lock set to caps.
func (s State) WithCaps(caps bool) State {
	t := s
	t.Caps = caps
	return t
}

func (s State) without(comp string) State {
	t := s
	t.Custom = map[string]bool{}
	for k, v := range s.Custom {
		if k != comp {
			t.Custom[k] = v
		}
	}
	return t
}

// Builtin components (SPEC §7.1).
var Builtin = map[string]bool{"shift": true, "ctrl": true, "ctrlL": true, "ctrlR": true, "alt": true,
	"altL": true, "altR": true, "caps": true, "none": true, "other": true}

// IsOther reports the set ["other"].
func (m ModifierSet) IsOther() bool { return len(m) == 1 && m[0] == "other" }

// Has reports whether the set contains a component.
func (m ModifierSet) Has(c string) bool {
	for _, x := range m {
		if x == c {
			return true
		}
	}
	return false
}

// Matches reports whether the set matches the state (SPEC §7.3).
func (m ModifierSet) Matches(s State) bool {
	if m.IsOther() {
		return false
	}
	var shift, ctrl, alt, caps bool
	ctrlOK, altOK := true, true
	custom := map[string]bool{}
	for _, c := range m {
		switch c {
		case "shift":
			shift = true
		case "caps":
			caps = true
		case "ctrl":
			ctrl, ctrlOK = true, s.CtrlL || s.CtrlR
		case "ctrlL":
			ctrl, ctrlOK = true, s.CtrlL && !s.CtrlR
		case "ctrlR":
			ctrl, ctrlOK = true, s.CtrlR && !s.CtrlL
		case "alt":
			alt, altOK = true, s.AltL || s.AltR
		case "altL":
			alt, altOK = true, s.AltL && !s.AltR
		case "altR":
			alt, altOK = true, s.AltR && !s.AltL
		default:
			custom[c] = true
		}
	}
	if shift != s.Shift || caps != s.Caps || !ctrlOK || !altOK {
		return false
	}
	if !ctrl && (s.CtrlL || s.CtrlR) || !alt && (s.AltL || s.AltR) {
		return false
	}
	for c := range custom {
		if !s.Custom[c] {
			return false
		}
	}
	for c, on := range s.Custom {
		if on && !custom[c] {
			return false
		}
	}
	return true
}

// Layer returns the hardware layer whose modifier sets match s, or nil.
func (l *Layout) Layer(s State) *HardwareLayer {
	if l.Hardware == nil {
		return nil
	}
	for _, layer := range l.Hardware.Layers {
		for _, m := range layer.Modifiers {
			if m.Matches(s) {
				return layer
			}
		}
	}
	return nil
}

func (l *Layout) otherLayer() *HardwareLayer {
	if l.Hardware == nil {
		return nil
	}
	for _, layer := range l.Hardware.Layers {
		for _, m := range layer.Modifiers {
			if m.IsOther() {
				return layer
			}
		}
	}
	return nil
}

// CapsLockFallback returns settings.capsLockFallback with its default true.
func (l *Layout) CapsLockFallback() bool {
	if l.Settings != nil && l.Settings.CapsLockFallback != nil {
		return *l.Settings.CapsLockFallback
	}
	return true
}

// Resolve returns the binding of position p in state s (SPEC §9.3), or nil for nothing.
func (l *Layout) Resolve(p string, s State) *Binding {
	layer := l.Layer(s)
	if layer != nil {
		if b, ok := layer.Bindings[p]; ok {
			return b
		}
	}
	if s.Caps && l.CapsLockFallback() {
		return l.Resolve(p, s.WithCaps(false))
	}
	var locks []string
	for c, on := range s.Custom {
		if cm := l.CustomModifiers[c]; on && cm != nil && cm.Kind == "lock" {
			locks = append(locks, c)
		}
	}
	sort.Strings(locks)
	for _, c := range locks {
		cm := l.CustomModifiers[c]
		if cm.Fallback == nil || *cm.Fallback {
			return l.Resolve(p, s.without(c))
		}
	}
	if layer != nil {
		return nil
	}
	if o := l.otherLayer(); o != nil {
		return o.Bindings[p]
	}
	return nil
}

// Output is what a binding does after key indirection: text units, a dead key, or nothing.
type Output struct {
	Units   []uint16
	DeadKey string
	// Markers is true when markers were dropped from the text.
	Markers bool
	// Unresolved names a key or variable that does not exist.
	Unresolved string
	// Exact is true for a CodeUnits binding: its units are kept as they are (never normalised).
	Exact bool
}

// None reports an output that does nothing.
func (o Output) None() bool { return len(o.Units) == 0 && o.DeadKey == "" }

// Effective turns a binding into its output (SPEC §9.2, §8): {"key"} is replaced by the key's output or dead key
// (implied keys included); markers are dropped; variables are inserted with \u{...} escapes decoded.
func (l *Layout) Effective(b *Binding) Output {
	if b == nil {
		return Output{}
	}
	switch b.Kind {
	case BindText:
		return Output{Units: utf16.Encode([]rune(b.Text))}
	case BindUnits:
		return Output{Units: append([]uint16(nil), b.Units...), Exact: true}
	case BindDeadKey:
		return Output{DeadKey: b.DeadKey}
	case BindOutput:
		return l.markedOutput(b.Output)
	case BindKey:
		k := l.Keys[b.Key]
		if k == nil {
			if implied, ok := ImpliedKey(b.Key); ok {
				k = implied
			} else {
				return Output{Unresolved: b.Key}
			}
		}
		if k.DeadKey != "" {
			return Output{DeadKey: k.DeadKey}
		}
		if k.Output != nil {
			return l.markedOutput(*k.Output)
		}
	}
	return Output{}
}

func (l *Layout) markedOutput(m MarkedText) Output {
	var o Output
	var text []rune
	for _, it := range m.Items {
		switch {
		case it.Marker != "":
			o.Markers = true
		case it.Variable != "":
			found := false
			for _, v := range l.Variables {
				if v.ID == it.Variable && v.Type == "string" {
					text = append(text, []rune(DecodeEscapes(v.Value))...)
					found = true
				}
			}
			if !found {
				o.Unresolved = it.Variable
			}
		default:
			text = append(text, []rune(it.Text)...)
		}
	}
	o.Units = utf16.Encode(text)
	return o
}

// ImpliedKey returns the implied key of SPEC §8 for an id.
func ImpliedKey(id string) (*Key, bool) {
	switch {
	case id == "space":
		t := NewText(" ")
		return &Key{Output: &t, Stretch: true}, true
	case id == "gap":
		return &Key{Gap: true}, true
	case len(id) == 1 && (id[0] >= '0' && id[0] <= '9' || id[0] >= 'A' && id[0] <= 'Z' || id[0] >= 'a' && id[0] <= 'z'):
		t := NewText(id)
		return &Key{Output: &t}, true
	}
	return nil, false
}

// DecodeEscapes decodes the \u{hhhh} and \u{hh hh} escapes of a CLDR string value.
func DecodeEscapes(s string) string {
	var out []rune
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] == '\\' && i+2 < len(r) && r[i+1] == 'u' && r[i+2] == '{' {
			j := i + 3
			for j < len(r) && r[j] != '}' {
				j++
			}
			if j < len(r) {
				var cur rune
				have := false
				for _, c := range r[i+3 : j] {
					switch {
					case c == ' ':
						if have {
							out = append(out, cur)
						}
						cur, have = 0, false
					case c >= '0' && c <= '9':
						cur, have = cur*16+c-'0', true
					case c >= 'a' && c <= 'f':
						cur, have = cur*16+c-'a'+10, true
					case c >= 'A' && c <= 'F':
						cur, have = cur*16+c-'A'+10, true
					}
				}
				if have {
					out = append(out, cur)
				}
				i = j
				continue
			}
		}
		out = append(out, r[i])
	}
	return string(out)
}
