// Package keyname is the position table of the XKB target: the default XKB key name (evdev keycodes) of each
// position name.
package keyname

// PROVISIONAL: replaced by the spec's targets/xkb-default-keyname.tsv, embedded unchanged, once it is published.
var rows = [][2]string{
	{"Escape", "ESC"}, {"Digit1", "AE01"}, {"Digit2", "AE02"}, {"Digit3", "AE03"}, {"Digit4", "AE04"},
	{"Digit5", "AE05"}, {"Digit6", "AE06"}, {"Digit7", "AE07"}, {"Digit8", "AE08"}, {"Digit9", "AE09"},
	{"Digit0", "AE10"}, {"Minus", "AE11"}, {"Equal", "AE12"}, {"Backspace", "BKSP"}, {"Tab", "TAB"},
	{"KeyQ", "AD01"}, {"KeyW", "AD02"}, {"KeyE", "AD03"}, {"KeyR", "AD04"}, {"KeyT", "AD05"}, {"KeyY", "AD06"},
	{"KeyU", "AD07"}, {"KeyI", "AD08"}, {"KeyO", "AD09"}, {"KeyP", "AD10"}, {"BracketLeft", "AD11"},
	{"BracketRight", "AD12"}, {"Enter", "RTRN"}, {"ControlLeft", "LCTL"}, {"KeyA", "AC01"}, {"KeyS", "AC02"},
	{"KeyD", "AC03"}, {"KeyF", "AC04"}, {"KeyG", "AC05"}, {"KeyH", "AC06"}, {"KeyJ", "AC07"}, {"KeyK", "AC08"},
	{"KeyL", "AC09"}, {"Semicolon", "AC10"}, {"Quote", "AC11"}, {"Backquote", "TLDE"}, {"ShiftLeft", "LFSH"},
	{"Backslash", "BKSL"}, {"KeyZ", "AB01"}, {"KeyX", "AB02"}, {"KeyC", "AB03"}, {"KeyV", "AB04"},
	{"KeyB", "AB05"}, {"KeyN", "AB06"}, {"KeyM", "AB07"}, {"Comma", "AB08"}, {"Period", "AB09"},
	{"Slash", "AB10"}, {"ShiftRight", "RTSH"}, {"NumpadMultiply", "KPMU"}, {"AltLeft", "LALT"},
	{"Space", "SPCE"}, {"CapsLock", "CAPS"}, {"F1", "FK01"}, {"F2", "FK02"}, {"F3", "FK03"}, {"F4", "FK04"},
	{"F5", "FK05"}, {"F6", "FK06"}, {"F7", "FK07"}, {"F8", "FK08"}, {"F9", "FK09"}, {"F10", "FK10"},
	{"NumLock", "NMLK"}, {"ScrollLock", "SCLK"}, {"Numpad7", "KP7"}, {"Numpad8", "KP8"}, {"Numpad9", "KP9"},
	{"NumpadSubtract", "KPSU"}, {"Numpad4", "KP4"}, {"Numpad5", "KP5"}, {"Numpad6", "KP6"}, {"NumpadAdd", "KPAD"},
	{"Numpad1", "KP1"}, {"Numpad2", "KP2"}, {"Numpad3", "KP3"}, {"Numpad0", "KP0"}, {"NumpadDecimal", "KPDL"},
	{"IntlBackslash", "LSGT"}, {"F11", "FK11"}, {"F12", "FK12"}, {"IntlRo", "AB11"}, {"IntlYen", "AE13"},
	{"NumpadEnter", "KPEN"}, {"ControlRight", "RCTL"}, {"NumpadDivide", "KPDV"}, {"PrintScreen", "PRSC"},
	{"AltRight", "RALT"}, {"Home", "HOME"}, {"ArrowUp", "UP"}, {"PageUp", "PGUP"}, {"ArrowLeft", "LEFT"},
	{"ArrowRight", "RGHT"}, {"End", "END"}, {"ArrowDown", "DOWN"}, {"PageDown", "PGDN"}, {"Insert", "INS"},
	{"Delete", "DELE"}, {"MetaLeft", "LWIN"}, {"MetaRight", "RWIN"}, {"ContextMenu", "COMP"}, {"Pause", "PAUS"},
	{"NumpadEqual", "KPEQ"}, {"NumpadComma", "KPDC"},
}

var byPos, byName = func() (map[string]string, map[string]string) {
	p, n := map[string]string{}, map[string]string{}
	for _, r := range rows {
		p[r[0]], n[r[1]] = r[1], r[0]
	}
	return p, n
}()

// ForPosition returns the default key name of a position.
func ForPosition(pos string) (string, bool) { n, ok := byPos[pos]; return n, ok }

// ForKeyName returns the position of a key name or alias.
func ForKeyName(name string) (string, bool) { p, ok := byName[Canonical(name)]; return p, ok }

// aliases are the key-name aliases of xkeyboard-config's keycodes "evdev" and aliases "qwerty" that layouts use
// in place of the evdev names.
var aliases = map[string]string{
	"AC12": "BKSL", "ALGR": "RALT", "MENU": "COMP", "HZTG": "TLDE", "LMTA": "LWIN", "RMTA": "RWIN",
	"I132": "AE13", "I125": "KPEQ",
	"LatQ": "AD01", "LatW": "AD02", "LatE": "AD03", "LatR": "AD04", "LatT": "AD05", "LatY": "AD06",
	"LatU": "AD07", "LatI": "AD08", "LatO": "AD09", "LatP": "AD10", "LatA": "AC01", "LatS": "AC02",
	"LatD": "AC03", "LatF": "AC04", "LatG": "AC05", "LatH": "AC06", "LatJ": "AC07", "LatK": "AC08",
	"LatL": "AC09", "LatZ": "AB01", "LatX": "AB02", "LatC": "AB03", "LatV": "AB04", "LatB": "AB05",
	"LatN": "AB06", "LatM": "AB07",
}

// Canonical returns the evdev key name of an alias, or the name itself.
func Canonical(name string) string {
	if c, ok := aliases[name]; ok {
		return c
	}
	return name
}
