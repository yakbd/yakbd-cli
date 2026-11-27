package cldr

import (
	"embed"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// appendixA maps scan codes to position names: the table of SPEC Appendix A (W3C UI Events code values).
var appendixA = map[string]string{
	"01": "Escape", "02": "Digit1", "03": "Digit2", "04": "Digit3", "05": "Digit4", "06": "Digit5", "07": "Digit6",
	"08": "Digit7", "09": "Digit8", "0A": "Digit9", "0B": "Digit0", "0C": "Minus", "0D": "Equal", "0E": "Backspace",
	"0F": "Tab", "10": "KeyQ", "11": "KeyW", "12": "KeyE", "13": "KeyR", "14": "KeyT", "15": "KeyY", "16": "KeyU",
	"17": "KeyI", "18": "KeyO", "19": "KeyP", "1A": "BracketLeft", "1B": "BracketRight", "1C": "Enter",
	"1D": "ControlLeft", "1E": "KeyA", "1F": "KeyS", "20": "KeyD", "21": "KeyF", "22": "KeyG", "23": "KeyH",
	"24": "KeyJ", "25": "KeyK", "26": "KeyL", "27": "Semicolon", "28": "Quote", "29": "Backquote", "2A": "ShiftLeft",
	"2B": "Backslash", "2C": "KeyZ", "2D": "KeyX", "2E": "KeyC", "2F": "KeyV", "30": "KeyB", "31": "KeyN",
	"32": "KeyM", "33": "Comma", "34": "Period", "35": "Slash", "36": "ShiftRight", "37": "NumpadMultiply",
	"38": "AltLeft", "39": "Space", "3A": "CapsLock", "3B": "F1", "3C": "F2", "3D": "F3", "3E": "F4", "3F": "F5",
	"40": "F6", "41": "F7", "42": "F8", "43": "F9", "44": "F10", "45": "NumLock", "46": "ScrollLock", "47": "Numpad7",
	"48": "Numpad8", "49": "Numpad9", "4A": "NumpadSubtract", "4B": "Numpad4", "4C": "Numpad5", "4D": "Numpad6",
	"4E": "NumpadAdd", "4F": "Numpad1", "50": "Numpad2", "51": "Numpad3", "52": "Numpad0", "53": "NumpadDecimal",
	"56": "IntlBackslash", "57": "F11", "58": "F12", "59": "NumpadEqual", "64": "F13", "65": "F14", "66": "F15",
	"67": "F16", "68": "F17", "69": "F18", "6A": "F19", "6B": "F20", "6C": "F21", "6D": "F22", "6E": "F23",
	"70": "KanaMode", "73": "IntlRo", "76": "F24", "79": "Convert", "7B": "NonConvert", "7D": "IntlYen",
	"7E": "NumpadComma", "E01C": "NumpadEnter", "E01D": "ControlRight", "E035": "NumpadDivide",
	"E037": "PrintScreen", "E038": "AltRight", "E047": "Home", "E048": "ArrowUp", "E049": "PageUp",
	"E04B": "ArrowLeft", "E04D": "ArrowRight", "E04F": "End", "E050": "ArrowDown", "E051": "PageDown",
	"E052": "Insert", "E053": "Delete", "E05B": "MetaLeft", "E05C": "MetaRight", "E05D": "ContextMenu",
	"E11D": "Pause",
}

// positionFor returns the position id of a CLDR scan code (two hex digits, or four with a prefix): the Appendix A
// name, else "sc" + the code in upper case (coverage §2.1, `scanCodes`).
func positionFor(code string) (id, scan string, err error) {
	c := strings.ToUpper(code)
	if _, e := strconv.ParseUint(c, 16, 16); e != nil || !(len(c) == 2 || (len(c) == 4 && (c[:2] == "E0" || c[:2] == "E1"))) {
		return "", "", fmt.Errorf("scan code %q", code)
	}
	if n, ok := appendixA[c]; ok {
		return n, c, nil
	}
	return "sc" + c, c, nil
}

// impliedForms are CLDR's implied form ids.
var impliedForms = []string{"us", "iso", "abnt2", "jis", "ks"}

func isImpliedForm(id string) bool {
	for _, f := range impliedForms {
		if f == id {
			return true
		}
	}
	return false
}

//go:embed import/*.xml
var imports embed.FS

// importFile returns the embedded CLDR import file of an import path ("45/keys-Zyyy-punctuation.xml"); the
// version prefix is ignored because the files are those of release 49, which every earlier path names unchanged.
func importFile(p string) ([]byte, error) {
	return imports.ReadFile("import/" + path.Base(p))
}

// impliedFormRows returns the scan-code rows of an implied form from the embedded scanCodes-implied.xml.
func impliedFormRows(id string) ([][]string, bool) {
	data, err := importFile("scanCodes-implied.xml")
	if err != nil {
		return nil, false
	}
	root, err := parseTree(data)
	if err != nil {
		return nil, false
	}
	for _, f := range root.kids("form") {
		if f.get("id") != id {
			continue
		}
		var rows [][]string
		for _, sc := range f.kids("scanCodes") {
			rows = append(rows, strings.Fields(sc.get("codes")))
		}
		return rows, true
	}
	return nil, false
}
