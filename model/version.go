package model

import "strings"

// MinVersion is the lowest format version that holds l (SPEC §1.1): "0.2" when l uses targets.macos,
// targets.xkb, a loss kind of a 0.2-only family (macos, macosDefault, xkb, xkbDefault) or a 0.2 provenance format
// (keylayout, xkb); else "0.1".
func MinVersion(l *Layout) string {
	if t := l.Targets; t != nil {
		if t.MacOS != nil || t.XKB != nil {
			return "0.2"
		}
	}
	if p := l.Metadata.Provenance; p != nil && (p.Format == "keylayout" || p.Format == "xkb") {
		return "0.2"
	}
	for _, o := range l.onLosses() {
		for k := range o.Kinds {
			fam, _, _ := strings.Cut(k, ".")
			switch fam {
			case "macos", "macosDefault", "xkb", "xkbDefault":
				return "0.2"
			}
		}
	}
	return "0.1"
}

// onLosses are every onLoss policy of l.
func (l *Layout) onLosses() []*OnLoss {
	out := []*OnLoss{l.OnLoss}
	if t := l.Targets; t != nil {
		if t.Windows != nil {
			out = append(out, t.Windows.OnLoss)
		}
		if t.KLC != nil {
			out = append(out, t.KLC.OnLoss)
		}
		if t.CLDR != nil {
			out = append(out, t.CLDR.OnLoss)
		}
	}
	var nn []*OnLoss
	for _, o := range out {
		if o != nil {
			nn = append(nn, o)
		}
	}
	return nn
}
