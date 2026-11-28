package xkb

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Keysym is an X11 keysym value (29 bits).
type Keysym uint32

// Special keysyms.
const (
	NoSymbol   Keysym = 0
	VoidSymbol Keysym = 0xffffff
)

// The map kinds of keysymTable (tools/genkeysyms).
const (
	mapNone     = 0
	mapExact    = 1
	mapSpecific = 2
	mapLegacy   = 3
)

type keysymEntry struct {
	name       string
	value      uint32
	r          rune
	kind       uint8
	deprecated bool
}

var tables struct {
	once     sync.Once
	byName   map[string]Keysym
	foldName map[string]Keysym // lower-case name -> first value, for names written in another case
	name     map[Keysym]string // canonical name
	char     map[Keysym]rune   // character of a named keysym
	forRune  map[rune]Keysym   // first non-deprecated keysym that stands exactly for a character
}

func initTables() {
	tables.once.Do(func() {
		tables.byName = make(map[string]Keysym, len(keysymTable))
		tables.foldName = map[string]Keysym{}
		tables.name = map[Keysym]string{}
		tables.char = map[Keysym]rune{}
		tables.forRune = map[rune]Keysym{}
		for _, e := range keysymTable {
			k := Keysym(e.value)
			tables.byName[e.name] = k
			if _, ok := tables.foldName[strings.ToLower(e.name)]; !ok {
				tables.foldName[strings.ToLower(e.name)] = k
			}
			if _, ok := tables.name[k]; !ok {
				tables.name[k] = e.name
			}
			if e.kind != mapNone {
				if _, ok := tables.char[k]; !ok {
					tables.char[k] = e.r
				}
			}
			if e.kind == mapExact && !e.deprecated {
				if _, ok := tables.forRune[e.r]; !ok {
					tables.forRune[e.r] = k
				}
			}
		}
	})
}

// KeysymFromName returns the keysym a name stands for in a symbols or Compose file: a keysym name of
// keysymdef.h, U followed by 1 to 6 hex digits (a Unicode keysym), 0x followed by hex digits, or a decimal
// number (the digits 0 to 9 alone are the keysyms 0 to 9). Uxxxx is the Unicode keysym 0x01000000 + xxxx (the
// Latin-1 keysym below U+0100), as libxkbcommon reads it. NoSymbol and VoidSymbol are names too; ok is false
// for a name that names nothing.
func KeysymFromName(s string) (Keysym, bool) {
	initTables()
	if k, ok := tables.byName[s]; ok {
		return k, true
	}
	switch strings.ToLower(s) {
	case "nosymbol", "any":
		return NoSymbol, true
	case "voidsymbol", "none":
		return VoidSymbol, true
	}
	if len(s) >= 2 && len(s) <= 7 && s[0] == 'U' {
		if v, err := strconv.ParseUint(s[1:], 16, 32); err == nil && v <= unicode.MaxRune {
			if v < 0x100 {
				return Keysym(v), true
			}
			return Keysym(0x01000000 + v), true
		}
	}
	if len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X") {
		if v, err := strconv.ParseUint(s[2:], 16, 32); err == nil && v <= 0x1fffffff {
			return Keysym(v), true
		}
		return 0, false
	}
	if len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
		return Keysym(s[0]), true
	}
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		if v, err := strconv.ParseUint(s, 10, 32); err == nil && v <= 0x1fffffff {
			return Keysym(v), true
		}
	}
	return 0, false
}

// Name returns the canonical name of k: its first name in keysymdef.h, U followed by the hex digits of a Unicode
// keysym, or 0x followed by hex digits.
func (k Keysym) Name() string {
	initTables()
	switch k {
	case NoSymbol:
		return "NoSymbol"
	case VoidSymbol:
		return "VoidSymbol"
	}
	if n, ok := tables.name[k]; ok {
		return n
	}
	if k >= 0x01000100 && k <= 0x0110ffff {
		return fmt.Sprintf("U%04X", uint32(k-0x01000000))
	}
	return fmt.Sprintf("0x%08x", uint32(k))
}

// Rune returns the character k types, and false for a keysym that types none (a function or dead key).
func (k Keysym) Rune() (rune, bool) {
	initTables()
	if k >= 0x01000000 && k <= 0x0110ffff {
		r := rune(k - 0x01000000)
		return r, utf8.ValidRune(r)
	}
	if (k >= 0x20 && k <= 0x7e) || (k >= 0xa0 && k <= 0xff) {
		return rune(k), true
	}
	r, ok := tables.char[k]
	return r, ok
}

// FromRune returns the keysym of a character: the first non-deprecated keysym that stands exactly for it, the
// Latin-1 keysym for U+0020 to U+007E and U+00A0 to U+00FF, else the Unicode keysym 0x01000000 + r.
func FromRune(r rune) Keysym {
	initTables()
	if (r >= 0x20 && r <= 0x7e) || (r >= 0xa0 && r <= 0xff) {
		return Keysym(r)
	}
	if k, ok := tables.forRune[r]; ok {
		return k
	}
	return Keysym(0x01000000 + uint32(r))
}

// IsDead reports whether k is a dead key (dead_*, 0xfe50 to 0xfe93).
func (k Keysym) IsDead() bool { return k >= 0xfe50 && k <= 0xfe93 }

// Upper is the keysym Caps Lock turns k into when the key type does not use Lock: the upper case of the
// character it types (Unicode simple case mapping, and ß to ẞ as libxkbcommon 1.13 does), else k.
func (k Keysym) Upper() Keysym {
	r, ok := k.Rune()
	if !ok {
		return k
	}
	u := unicode.ToUpper(r)
	if r == 'ß' {
		u = 'ẞ'
	}
	if u == r {
		return k
	}
	return FromRune(u)
}

// IsLower reports whether k types a lower-case letter; IsUpper whether an upper- or title-case one.
func (k Keysym) IsLower() bool {
	r, ok := k.Rune()
	return ok && unicode.IsLower(r) && unicode.ToUpper(r) != r
}

// IsUpper reports whether k types an upper- or title-case letter.
func (k Keysym) IsUpper() bool {
	r, ok := k.Rune()
	return ok && (unicode.IsUpper(r) || unicode.IsTitle(r)) && unicode.ToLower(r) != r
}

// IsKeypad reports whether k is a keypad keysym (KP_*, 0xff80 to 0xffbd).
func (k Keysym) IsKeypad() bool { return k >= 0xff80 && k <= 0xffbd }
