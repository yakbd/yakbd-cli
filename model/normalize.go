package model

import (
	"slices"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/internal/unicode/norm"
)

// Normalizes reports whether settings.normalization is enabled (the default, SPEC §5): a target that does not
// normalise gets NFC outputs.
func (l *Layout) Normalizes() bool {
	return l.Settings == nil || l.Settings.Normalization != "disabled"
}

// nfcUnits returns the NFC form of text given as UTF-16 units; units with a lone surrogate are not text and are
// returned as they are.
func nfcUnits(u []uint16) []uint16 {
	r := utf16.Decode(u)
	if !slices.Equal(utf16.Encode(r), u) {
		return u
	}
	n := utf16.Encode([]rune(norm.NFC(string(r))))
	if slices.Equal(n, u) {
		return u
	}
	return n
}

// NFC returns the output with its text in NFC. A CodeUnits output (Exact) and a dead key are unchanged.
func (o Output) NFC() Output {
	if o.Exact || len(o.Units) == 0 {
		return o
	}
	o.Units = nfcUnits(o.Units)
	return o
}

// NFC returns a Text value in NFC; a dead-key reference and a CodeUnits value are unchanged.
func (v Value) NFC() Value {
	if v.Kind == ValueText {
		v.Text = norm.NFC(v.Text)
	}
	return v
}

// NFC returns a Text in NFC; a CodeUnits value is unchanged.
func (t TextOrUnits) NFC() TextOrUnits {
	if t.Units == nil {
		t.Text = norm.NFC(t.Text)
	}
	return t
}

// OutputsNFC reports whether every output a target normalises is already in NFC, so that normalising changes
// nothing: the effective output of every hardware binding and key, and every dead key's spacing, bases and
// results. Importers of formats whose targets do not normalise (.klc, DLL) use it to decide
// settings.normalization.
func (l *Layout) OutputsNFC() bool {
	same := func(o Output) bool { return slices.Equal(o.NFC().Units, o.Units) }
	if l.Hardware != nil {
		for _, layer := range l.Hardware.Layers {
			for _, b := range layer.Bindings {
				if !same(l.Effective(b)) {
					return false
				}
			}
		}
	}
	for _, k := range l.Keys {
		if k.Output != nil && !same(l.markedOutput(*k.Output)) {
			return false
		}
	}
	for _, dk := range l.DeadKeys {
		if dk.Spacing.NFC().Text != dk.Spacing.Text {
			return false
		}
		for _, e := range dk.Entries {
			if e.Base.NFC().Text != e.Base.Text || e.Result.NFC().Text != e.Result.Text {
				return false
			}
		}
	}
	return true
}
