// Package vk knows the Windows virtual-key names and the default scan-code to virtual-key table.
//
// The names and values come from vk-names.tsv, a table of the VK_* constants of the windows-sys crate 0.61.2
// (see NOTICE). The default table of scancodes.go is assembled from public documentation, row by row.
package vk

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed vk-names.tsv
var namesTSV string

// None is the virtual key that means "no key" in a scan-code table (VK__none_).
const None = 0xFF

var (
	byName = map[string]byte{} // upper-case name without VK_ -> value
	byVal  = map[byte]string{} // value -> first name in the table
)

// klcNames are the names MSKLC's compiler accepts (reference spec, MSKLC observations K21). The canonical writer
// prefers them, so that its files pass that compiler where possible.
var klcNames = []string{
	"BACK", "CANCEL", "ESCAPE", "RETURN", "SPACE", "DECIMAL", "OEM_1", "OEM_2", "OEM_3", "OEM_4", "OEM_5",
	"OEM_6", "OEM_7", "OEM_8", "OEM_PLUS", "OEM_COMMA", "OEM_MINUS", "OEM_PERIOD", "OEM_102", "ABNT_C1",
	"ABNT_C2", "SHIFT", "LSHIFT", "RSHIFT", "MENU", "LMENU", "RMENU", "CONTROL", "LCONTROL", "RCONTROL",
	"SEPARATOR", "ICO_00", "DELETE", "INSERT",
}

var klcKnown = map[byte]string{}

func init() {
	for i, line := range strings.Split(namesTSV, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "name\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 {
			panic(fmt.Sprintf("vk-names.tsv line %d: %q", i+1, line))
		}
		v, err := strconv.ParseUint(f[1], 16, 8)
		if err != nil {
			panic(fmt.Sprintf("vk-names.tsv line %d: %v", i+1, err))
		}
		name := strings.ToUpper(f[0])
		byName[name] = byte(v)
		if _, ok := byVal[byte(v)]; !ok {
			byVal[byte(v)] = name
		}
	}
	for _, n := range klcNames {
		klcKnown[byName[n]] = n
	}
	Defaults = parseTableA(tableA) // needs the names above
}

// Parse reads a virtual-key token as `.klc` files write it: a digit or letter (any case), a name of the table
// (any case, with or without VK_), or 0xNN. It reports false for anything else.
func Parse(tok string) (byte, bool) {
	if len(tok) == 1 {
		c := tok[0]
		switch {
		case c >= '0' && c <= '9':
			return c, true
		case c >= 'A' && c <= 'Z':
			return c, true
		case c >= 'a' && c <= 'z':
			return c - 'a' + 'A', true
		}
		return 0, false
	}
	if len(tok) > 2 && (tok[:2] == "0x" || tok[:2] == "0X") {
		v, err := strconv.ParseUint(tok[2:], 16, 8)
		if err != nil || len(tok) > 4 {
			return 0, false
		}
		return byte(v), true
	}
	name := strings.ToUpper(tok)
	name = strings.TrimPrefix(name, "VK_")
	if len(name) == 1 {
		return 0, false // "VK_A" is not a name of the table
	}
	v, ok := byName[name]
	return v, ok
}

// Name returns the canonical `.klc` spelling of a virtual key: the digit or letter itself, a name MSKLC's
// compiler knows, the first table name, or 0xNN when the value has no name.
func Name(v byte) string {
	if (v >= '0' && v <= '9') || (v >= 'A' && v <= 'Z') {
		return string(rune(v))
	}
	if n, ok := klcKnown[v]; ok {
		return n
	}
	if n, ok := byVal[v]; ok {
		return n
	}
	return fmt.Sprintf("0x%02x", v)
}

// KnownToMSKLC reports whether MSKLC's compiler accepts the canonical name of v (reference spec, OBS K21).
func KnownToMSKLC(v byte) bool {
	if (v >= '0' && v <= '9') || (v >= 'A' && v <= 'Z') {
		return true
	}
	_, ok := klcKnown[v]
	return ok
}

// Named reports whether v has a name in the table (or is a digit or letter).
func Named(v byte) bool {
	if (v >= '0' && v <= '9') || (v >= 'A' && v <= 'Z') {
		return true
	}
	_, ok := byVal[v]
	return ok
}
