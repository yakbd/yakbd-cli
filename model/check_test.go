package model

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	base := func() *Layout {
		return &Layout{
			Positions: map[string]*Position{"KeyA": {ScanCode: "1E"}, "KeyB": {ScanCode: "30"}},
			Hardware: &Hardware{Layers: []*HardwareLayer{
				{Modifiers: []ModifierSet{{}}, Bindings: map[string]*Binding{"KeyA": TextBinding("a")}},
				{Modifiers: []ModifierSet{{"shift"}}, Bindings: map[string]*Binding{"KeyA": TextBinding("A")}},
			}},
		}
	}
	if r := base().Check(); len(r.Entries) != 0 {
		t.Fatalf("clean layout: %s", r.Summary())
	}
	tests := []struct {
		name string
		mod  func(*Layout)
		code string
	}{
		{"overlap", func(l *Layout) { l.Hardware.Layers[1].Modifiers = []ModifierSet{{}} }, "general.layerOverlap"},
		{"mixed sides", func(l *Layout) { l.Hardware.Layers[1].Modifiers = []ModifierSet{{"ctrl", "ctrlL"}} }, "general.modifierSetInvalid"},
		{"unknown position", func(l *Layout) { l.Hardware.Layers[0].Bindings["KeyZ"] = TextBinding("z") }, "general.unresolvedReference"},
		{"unknown key", func(l *Layout) { l.Hardware.Layers[0].Bindings["KeyB"] = &Binding{Kind: BindKey, Key: "nope"} }, "general.unresolvedReference"},
		{"unknown dead key", func(l *Layout) { l.Hardware.Layers[0].Bindings["KeyB"] = &Binding{Kind: BindDeadKey, DeadKey: "x"} }, "general.unresolvedReference"},
		{"unknown component", func(l *Layout) { l.Hardware.Layers[1].Modifiers = []ModifierSet{{"kana"}} }, "general.unresolvedReference"},
		{"duplicate scan code", func(l *Layout) { l.Positions["KeyB"].ScanCode = "1e" }, "general.duplicateScanCode"},
		{"code units as text", func(l *Layout) { l.Hardware.Layers[0].Bindings["KeyB"] = &Binding{Kind: BindUnits, Units: Units{'b'}} }, "general.codeUnitsAreText"},
		{"binding on modifier", func(l *Layout) {
			l.CustomModifiers = map[string]*CustomModifier{"kana": {Kind: "held", Positions: []string{"KeyA"}}}
		}, "general.bindingOnModifier"},
	}
	for _, tt := range tests {
		l := base()
		tt.mod(l)
		if got := strings.Join(l.Check().Codes(), " "); !strings.Contains(got, tt.code) {
			t.Errorf("%s: got %q", tt.name, got)
		}
	}
}

// TestCheckKindsD42 covers the other five kinds of D42.
func TestCheckKindsD42(t *testing.T) {
	gap := &Key{Gap: true}
	tests := []struct {
		name string
		l    *Layout
		kind string
	}{
		{"variable twice", &Layout{Variables: []*Variable{{ID: "a", Type: "string", Value: "x"}, {ID: "a", Type: "set", Value: "y"}}}, "general.duplicateVariableId"},
		{"long press default", &Layout{Keys: map[string]*Key{"k": {Gap: true, LongPressKeyIDs: []string{"a"}, LongPressDefaultKeyID: "b"}}}, "general.longPressDefaultInvalid"},
		{"multi-tap self", &Layout{Keys: map[string]*Key{"k": {Gap: true, MultiTapKeyIDs: []string{"k"}}}}, "general.multiTapSelf"},
		{"touch base", &Layout{Keys: map[string]*Key{"g": gap}, Touch: []*TouchLayerSet{{Layers: []*TouchLayer{{ID: "other", Rows: [][]string{{"g"}}}}}}}, "general.touchBaseMissing"},
		{"other mixed", &Layout{Hardware: &Hardware{Layers: []*HardwareLayer{{Modifiers: []ModifierSet{{"other", "shift"}}, Bindings: map[string]*Binding{}}}}}, "general.modifierSetInvalid"},
	}
	for _, tt := range tests {
		if got := strings.Join(tt.l.Check().Codes(), " "); !strings.Contains(got, tt.kind) {
			t.Errorf("%s: got %q", tt.name, got)
		}
	}
}

// TestCheckTouch03: the 0.3 rules of SPEC §19 item 5.
func TestCheckTouch03(t *testing.T) {
	l := &Layout{Keys: map[string]*Key{"a": {}}, Touch: []*TouchLayerSet{
		{Layers: []*TouchLayer{{ID: "base", Rows: [][]string{{"a"}, {"a"}}, RowHeights: []float64{1}}, {ID: "shift", Rows: [][]string{{"a"}}, ReturnLayerID: "base"}}},
		{Layers: []*TouchLayer{{ID: "base", Rows: [][]string{{"a"}}, ReturnLayerID: "shift"}}},
	}}
	r := l.Check()
	got := map[string]string{}
	for _, e := range r.Entries {
		got[e.Where.Path] = e.Code
	}
	if got["/touch/0/layers/0/rowHeights"] != "general.rowHeightsMismatch" || got["/touch/1/layers/0/returnLayerId"] != "general.unresolvedReference" || len(got) != 2 {
		t.Errorf("entries: %v", got)
	}
}
