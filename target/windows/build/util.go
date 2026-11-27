package build

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/model"
)

// kindNormalized is the loss kind of an output NFC changed (kbl D73).
const kindNormalized = "general.outputNormalized"

func utf16Of(s string) []uint16 { return utf16.Encode([]rune(s)) }

// nfc returns o as the build emits it: in NFC when the layout normalises (a CodeUnits output is exact), with an
// entry when that changes the output.
func (b *lowerer) nfc(o model.Output, path string) model.Output {
	if !b.norm {
		return o
	}
	n := o.NFC()
	if !slices.Equal(n.Units, o.Units) {
		b.normalized(path, o.Units, n.Units)
	}
	return n
}

// nfcText is nfc for a dead key's spacing.
func (b *lowerer) nfcText(t model.TextOrUnits, path string) model.TextOrUnits {
	if !b.norm {
		return t
	}
	n := t.NFC()
	if n.Text != t.Text {
		b.normalized(path, utf16Of(t.Text), utf16Of(n.Text))
	}
	return n
}

// compared is an output in the form two outputs are compared in: normalised as built, whatever form it had.
func (b *lowerer) compared(o model.Output) model.Output {
	if b.norm {
		o = o.NFC()
	}
	o.Exact = false
	return o
}

// normalized reports one output NFC changed, once per path and output (a binding used in several columns once).
func (b *lowerer) normalized(path string, before, after []uint16) {
	key := path + " " + hexUnits(before)
	if b.normSeen[key] {
		return
	}
	if b.normSeen == nil {
		b.normSeen = map[string]bool{}
	}
	b.normSeen[key] = true
	b.lost(kindNormalized, path, "NFC changes the output %s to %s", hexUnits(before), hexUnits(after))
	e := &b.r.Entries[len(b.r.Entries)-1]
	e.Detail = map[string]any{"before": hexUnits(before), "after": hexUnits(after)}
}

func hexUnits(u []uint16) string {
	var s strings.Builder
	for i, c := range u {
		if i > 0 {
			s.WriteByte(' ')
		}
		fmt.Fprintf(&s, "%04X", c)
	}
	return s.String()
}
