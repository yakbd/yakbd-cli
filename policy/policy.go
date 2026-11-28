// Package policy resolves the severity of loss and defaulted-choice entries from a file's onLoss policies (SPEC
// §17.2) and the default policies of the loss kinds (SPEC §17.3).
package policy

import (
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// Known reports whether kind is a loss kind of SPEC §17.3 or a defaulted-choice kind (`<target>Default.<rule>`).
func Known(kind string) bool {
	if _, ok := kindDefaults[kind]; ok {
		return true
	}
	for _, p := range []string{"windowsDefault.", "macosDefault.", "xkbDefault.", "androidDefault.", "androidimeDefault.", "iosDefault.",
		"chromeosDefault.", "keymanDefault."} {
		if strings.HasPrefix(kind, p) {
			return true
		}
	}
	return false
}

// Fatal reports whether a kind is always an error.
func Fatal(kind string) bool { return kindDefaults[kind] == "fatal" }

func severity(p string) (loss.Severity, bool) {
	switch p {
	case "error", "fatal":
		return loss.Error, true
	case "warning":
		return loss.Warning, true
	case "accept":
		return loss.Accepted, true
	}
	return 0, false
}

// Resolve sets the severity of every Loss and Defaulted entry of r, for target ("windows", "klc", "cldr", "macos", "xkb", "android", "androidime", "ios", "chromeos", "keyman", or ""
// for none), by the rules of SPEC §17.2: target kind, file kind, target prefix.*, file prefix.*, target
// default, file default, the kind's default. Fatal kinds stay errors. extra, if not nil, is applied after the
// file (the command line's -strict-defaults, for example).
func Resolve(r *loss.Report, l *model.Layout, target string, extra *model.OnLoss) {
	var file, tgt *model.OnLoss
	if l != nil {
		file = l.OnLoss
		if t := l.Targets; t != nil {
			switch target {
			case "windows":
				if t.Windows != nil {
					tgt = t.Windows.OnLoss
				}
			case "klc":
				if t.KLC != nil {
					tgt = t.KLC.OnLoss
				}
			case "cldr":
				if t.CLDR != nil {
					tgt = t.CLDR.OnLoss
				}
			case "macos":
				if t.MacOS != nil {
					tgt = t.MacOS.OnLoss
				}
			case "xkb":
				if t.XKB != nil {
					tgt = t.XKB.OnLoss
				}
			case "android":
				if t.Android != nil {
					tgt = t.Android.OnLoss
				}
			case "androidime":
				if t.AndroidIME != nil {
					tgt = t.AndroidIME.OnLoss
				}
			case "ios":
				if t.IOS != nil {
					tgt = t.IOS.OnLoss
				}
			case "chromeos":
				if t.ChromeOS != nil {
					tgt = t.ChromeOS.OnLoss
				}
			case "keyman":
				if t.Keyman != nil {
					tgt = t.Keyman.OnLoss
				}
			}
		}
	}
	for i := range r.Entries {
		e := &r.Entries[i]
		if e.Class == loss.Diagnostic {
			continue
		}
		p := lookup(e.Code, tgt, file)
		if extra != nil {
			if q := lookup(e.Code, extra, nil); q != "" && extraWins(extra, e.Code) {
				p = q
			}
		}
		if p == "" {
			p = Default(e.Code)
		}
		if Fatal(e.Code) {
			p = "error"
		}
		if s, ok := severity(p); ok {
			e.Severity = s
		}
	}
}

// Default returns the default policy of a kind: the table of §17.3, "accept" for defaulted-choice kinds,
// "warning" for an unknown kind.
func Default(kind string) string {
	if p, ok := kindDefaults[kind]; ok {
		return p
	}
	if strings.Contains(kind, "Default.") {
		return "accept"
	}
	return "warning"
}

func extraWins(p *model.OnLoss, kind string) bool {
	if _, ok := p.Kinds[kind]; ok {
		return true
	}
	_, ok := p.Kinds[prefix(kind)+".*"]
	return ok
}

func prefix(kind string) string {
	if i := strings.IndexByte(kind, '.'); i >= 0 {
		return kind[:i]
	}
	return kind
}

func lookup(kind string, tgt, file *model.OnLoss) string {
	get := func(p *model.OnLoss, k string) string {
		if p == nil {
			return ""
		}
		return p.Kinds[k]
	}
	for _, v := range []string{get(tgt, kind), get(file, kind), get(tgt, prefix(kind)+".*"), get(file, prefix(kind)+".*")} {
		if v != "" {
			return v
		}
	}
	if tgt != nil && tgt.Default != "" {
		return tgt.Default
	}
	if file != nil && file.Default != "" {
		return file.Default
	}
	return ""
}

// StrictDefaults is the policy of the command line's -strict-defaults flag.
var StrictDefaults = &model.OnLoss{Kinds: map[string]string{"windowsDefault.*": "error"}}
