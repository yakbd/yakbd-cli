package model

import "testing"

func TestMatches(t *testing.T) {
	tests := []struct {
		set  ModifierSet
		s    State
		want bool
	}{
		{ModifierSet{}, State{}, true},
		{ModifierSet{}, State{Shift: true}, false},
		{ModifierSet{"shift"}, State{Shift: true}, true},
		{ModifierSet{"ctrl", "alt"}, State{CtrlL: true, AltL: true}, true},
		{ModifierSet{"ctrl", "alt"}, State{CtrlR: true, AltR: true}, true},
		{ModifierSet{"altR"}, State{AltR: true}, true},
		{ModifierSet{"altR"}, State{AltL: true}, false},
		{ModifierSet{"altR"}, State{AltR: true, CtrlL: true}, false},
		{ModifierSet{"caps"}, State{Caps: true}, true},
		{ModifierSet{"caps"}, State{}, false},
		{ModifierSet{"kana"}, State{Custom: map[string]bool{"kana": true}}, true},
		{ModifierSet{}, State{Custom: map[string]bool{"kana": true}}, false},
		{ModifierSet{"other"}, State{}, false},
	}
	for _, tt := range tests {
		if got := tt.set.Matches(tt.s); got != tt.want {
			t.Errorf("%v matches %+v = %v", tt.set, tt.s, got)
		}
	}
}

func TestResolve(t *testing.T) {
	f := false
	l := &Layout{Hardware: &Hardware{Layers: []*HardwareLayer{
		{Modifiers: []ModifierSet{{}}, Bindings: map[string]*Binding{"KeyA": TextBinding("a"), "KeyB": TextBinding("b")}},
		{Modifiers: []ModifierSet{{"shift"}}, Bindings: map[string]*Binding{"KeyA": TextBinding("A")}},
		{Modifiers: []ModifierSet{{"caps"}}, Bindings: map[string]*Binding{"KeyA": TextBinding("Ä")}},
		{Modifiers: []ModifierSet{{"other"}}, Bindings: map[string]*Binding{"KeyB": TextBinding("o")}},
	}}}
	txt := func(b *Binding) string {
		if b == nil {
			return "-"
		}
		return b.Text
	}
	if got := txt(l.Resolve("KeyA", State{Caps: true})); got != "Ä" {
		t.Errorf("caps layer: %s", got)
	}
	if got := txt(l.Resolve("KeyB", State{Caps: true})); got != "b" {
		t.Errorf("caps fallback: %s", got)
	}
	if got := txt(l.Resolve("KeyB", State{Shift: true})); got != "-" {
		t.Errorf("shift layer without KeyB: %s", got)
	}
	if got := txt(l.Resolve("KeyB", State{CtrlL: true})); got != "o" {
		t.Errorf("other layer: %s", got)
	}
	l.Settings = &Settings{CapsLockFallback: &f}
	if got := txt(l.Resolve("KeyB", State{Caps: true})); got != "-" {
		t.Errorf("no fallback: %s", got)
	}
}

func TestEffective(t *testing.T) {
	l := &Layout{Variables: []*Variable{{ID: "v", Type: "string", Value: `x\u{301}`}},
		Keys: map[string]*Key{"dk": {DeadKey: "grave"}}}
	o := l.Effective(&Binding{Kind: BindOutput, Output: MarkedText{Items: []MarkedItem{{Marker: "m"}, {Variable: "v"}}}})
	if string(rune(o.Units[0])) != "x" || o.Units[1] != 0x301 || !o.Markers {
		t.Errorf("%+v", o)
	}
	if o := l.Effective(&Binding{Kind: BindKey, Key: "dk"}); o.DeadKey != "grave" {
		t.Errorf("%+v", o)
	}
	if o := l.Effective(&Binding{Kind: BindKey, Key: "Q"}); len(o.Units) != 1 || o.Units[0] != 'Q' {
		t.Errorf("implied %+v", o)
	}
	if DecodeEscapes(`a\u{1F600 41}b`) != "a😀Ab" {
		t.Error("escapes")
	}
}
