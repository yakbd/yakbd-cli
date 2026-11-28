package model

import "strings"

// MinVersion is the lowest format version that holds l (SPEC §1.1): "0.3" when l uses a 0.3 target (android,
// androidime, ios, chromeos, keyman), a 0.3 touch member (a key's hint or function nextKeyboard, a touch layer's
// rowHeights or returnLayerId, a touch set's orientation, deviceClass or systemRow), a loss kind of a 0.3 family or
// a 0.3 kind of an older family, or a 0.3 provenance format (kcm, kbdgen, kmn, keymantouch); else "0.2" when l
// uses targets.macos, targets.xkb, a loss kind of a 0.2-only family (macos, macosDefault, xkb, xkbDefault) or a 0.2
// provenance format (keylayout, xkb); else "0.1".
func MinVersion(l *Layout) string {
	if uses03(l) {
		return "0.3"
	}
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

// families03 are the loss-kind families new in 0.3; kinds03 the 0.3 kinds of older families (SPEC §17.3).
var (
	families03 = map[string]bool{"android": true, "androidDefault": true, "androidime": true, "androidimeDefault": true,
		"ios": true, "iosDefault": true, "chromeos": true, "chromeosDefault": true, "keyman": true, "keymanDefault": true,
		"kbdgen": true, "kmn": true, "keymantouch": true}
	kinds03 = map[string]bool{"cldr.touchDataDropped": true, "general.deviceClassDerived": true, "general.rowHeightsMismatch": true}
)

func uses03(l *Layout) bool {
	if t := l.Targets; t != nil {
		if t.Android != nil || t.AndroidIME != nil || t.IOS != nil || t.ChromeOS != nil || t.Keyman != nil {
			return true
		}
	}
	if p := l.Metadata.Provenance; p != nil {
		switch p.Format {
		case "kcm", "kbdgen", "kmn", "keymantouch":
			return true
		}
	}
	for _, k := range l.Keys {
		if k.Hint != "" || k.Function == "nextKeyboard" {
			return true
		}
	}
	for _, set := range l.Touch {
		if set.Orientation != "" || set.DeviceClass != "" || set.SystemRow != nil {
			return true
		}
		for _, layer := range set.Layers {
			if layer.RowHeights != nil || layer.ReturnLayerID != "" {
				return true
			}
		}
	}
	for _, o := range l.onLosses() {
		for k := range o.Kinds {
			fam, _, _ := strings.Cut(k, ".")
			if families03[fam] || kinds03[k] {
				return true
			}
		}
	}
	return false
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
		if t.MacOS != nil {
			out = append(out, t.MacOS.OnLoss)
		}
		if t.XKB != nil {
			out = append(out, t.XKB.OnLoss)
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
