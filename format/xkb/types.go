package xkb

// Mods is a set of the modifiers a layout's key types use.
type Mods uint8

// The modifiers of a key type: the real modifiers Shift and Lock (Caps Lock) and the virtual modifiers LevelThree
// (AltGr) and LevelFive, which a layout's level switch keys set.
const (
	Shift Mods = 1 << iota
	Lock
	LevelThree
	LevelFive
	NumLock
)

// AllStates is the number of modifier states the types are evaluated over (every combination of the five).
const AllStates = 32

// KeyType is a key type: which modifiers it looks at, the level of each combination, and what it leaves for the
// Caps Lock transformation (preserve).
type KeyType struct {
	Name     string
	Mods     Mods
	Levels   int
	Map      []TypeEntry
	Fallback bool // not a type of xkeyboard-config's "complete" set (an unknown name evaluates as ONE_LEVEL)
}

// TypeEntry is map[Mods] = Level (1-based), with preserve[Mods] = Preserve.
type TypeEntry struct {
	Mods     Mods
	Level    int
	Preserve Mods
}

// The key types of xkeyboard-config's types "complete" (types/basic, extra, level5, numpad) that layouts use,
// written from those files' definitions as data. Types that read Control or Alt (types/pc, FOUR_LEVEL_X) are not
// layout types and are left out: a key that names one is read as ONE_LEVEL and reported.
var stockTypes = []KeyType{
	{Name: "ONE_LEVEL", Levels: 1},
	{Name: "TWO_LEVEL", Mods: Shift, Levels: 2, Map: []TypeEntry{{Shift, 2, 0}}},
	{Name: "ALPHABETIC", Mods: Shift | Lock, Levels: 2, Map: []TypeEntry{{Shift, 2, 0}, {Lock, 2, 0}}},
	{Name: "KEYPAD", Mods: Shift | NumLock, Levels: 2, Map: []TypeEntry{{Shift, 2, 0}, {NumLock, 2, 0}}},
	{Name: "FOUR_LEVEL", Mods: Shift | LevelThree, Levels: 4, Map: []TypeEntry{
		{Shift, 2, 0}, {LevelThree, 3, 0}, {Shift | LevelThree, 4, 0}}},
	{Name: "FOUR_LEVEL_ALPHABETIC", Mods: Shift | Lock | LevelThree, Levels: 4, Map: []TypeEntry{
		{Shift, 2, 0}, {Lock, 2, 0}, {Shift | Lock, 1, 0},
		{LevelThree, 3, 0}, {Shift | LevelThree, 4, 0}, {Lock | LevelThree, 4, 0}, {Shift | Lock | LevelThree, 3, 0}}},
	{Name: "FOUR_LEVEL_SEMIALPHABETIC", Mods: Shift | Lock | LevelThree, Levels: 4, Map: []TypeEntry{
		{Shift, 2, 0}, {Lock, 2, 0}, {Shift | Lock, 1, 0},
		{LevelThree, 3, 0}, {Shift | LevelThree, 4, 0}, {Lock | LevelThree, 3, Lock}, {Shift | Lock | LevelThree, 4, Lock}}},
	{Name: "FOUR_LEVEL_PLUS_LOCK", Mods: Shift | Lock | LevelThree, Levels: 5, Map: []TypeEntry{
		{Shift, 2, 0}, {LevelThree, 3, 0}, {Shift | LevelThree, 4, 0},
		{Lock, 5, 0}, {Shift | Lock, 2, 0}, {Lock | LevelThree, 3, 0}, {Shift | Lock | LevelThree, 4, 0}}},
	{Name: "SEPARATE_CAPS_AND_SHIFT_ALPHABETIC", Mods: Shift | Lock | LevelThree, Levels: 4, Map: []TypeEntry{
		{Shift, 2, 0}, {Lock, 4, Lock}, {LevelThree, 3, 0}, {Shift | LevelThree, 4, 0},
		{Lock | LevelThree, 3, Lock}, {Shift | Lock | LevelThree, 3, 0}}},
	{Name: "FOUR_LEVEL_LOCKABLE_LEVEL2", Mods: Shift | LevelThree | LevelFive, Levels: 4, Map: []TypeEntry{
		{Shift, 2, 0}, {LevelFive, 2, 0}, {Shift | LevelFive, 1, 0},
		{LevelThree, 3, 0}, {Shift | LevelThree, 4, 0}, {LevelFive | LevelThree, 3, 0}, {Shift | LevelFive | LevelThree, 4, 0}}},
	{Name: "FOUR_LEVEL_KEYPAD", Mods: Shift | NumLock | LevelThree, Levels: 4, Map: []TypeEntry{
		{Shift, 2, 0}, {NumLock, 2, 0}, {Shift | NumLock, 1, 0},
		{LevelThree, 3, 0}, {Shift | LevelThree, 4, 0}, {NumLock | LevelThree, 4, 0}, {Shift | NumLock | LevelThree, 3, 0}}},
	{Name: "FOUR_LEVEL_MIXED_KEYPAD", Mods: Shift | NumLock | LevelThree, Levels: 4, Map: []TypeEntry{
		{NumLock, 2, 0}, {Shift, 2, 0}, {Shift | NumLock, 1, 0},
		{LevelThree, 3, 0}, {NumLock | LevelThree, 3, 0}, {Shift | LevelThree, 4, 0}, {Shift | NumLock | LevelThree, 4, 0}}},
	{Name: "EIGHT_LEVEL", Mods: Shift | LevelThree | LevelFive, Levels: 8, Map: eight(false, false)},
	{Name: "EIGHT_LEVEL_ALPHABETIC", Mods: Shift | Lock | LevelThree | LevelFive, Levels: 8, Map: eight(true, false)},
	{Name: "EIGHT_LEVEL_SEMIALPHABETIC", Mods: Shift | Lock | LevelThree | LevelFive, Levels: 8, Map: eight(true, true)},
}

// eight builds the maps of the EIGHT_LEVEL types: levels 1-2 / 3-4 / 5-6 / 7-8 by Shift within LevelThree and
// LevelFive; alphabetic: Lock acts as Shift on every pair; semi: Lock acts as Shift on levels 1-2 and 5-6 only
// and is preserved on the LevelThree pairs.
func eight(alpha, semi bool) []TypeEntry {
	var m []TypeEntry
	for _, l3 := range []Mods{0, LevelThree} {
		for _, l5 := range []Mods{0, LevelFive} {
			base := 1
			if l3 != 0 {
				base += 2
			}
			if l5 != 0 {
				base += 4
			}
			for _, sh := range []Mods{0, Shift} {
				for _, lk := range []Mods{0, Lock} {
					mods := l3 | l5 | sh | lk
					if mods == 0 || (!alpha && lk != 0) {
						continue
					}
					shifted := sh != 0
					var pres Mods
					if lk != 0 {
						if semi && l3 != 0 {
							pres = Lock
						} else {
							shifted = shifted != true
						}
					}
					lv := base
					if shifted {
						lv++
					}
					m = append(m, TypeEntry{mods, lv, pres})
				}
			}
		}
	}
	return m
}

// StockType returns the stock key type of a name, or nil.
func StockType(name string) *KeyType {
	for i := range stockTypes {
		if stockTypes[i].Name == name {
			return &stockTypes[i]
		}
	}
	return nil
}

// AutomaticType is the type a key gets when its definition names none, by libxkbcommon's rule (the type
// depends on the number of levels and on whether the first levels are lower/upper case letter pairs or keypad
// keysyms); levels beyond 4 have no automatic type and fall back to ONE_LEVEL ("" then).
func AutomaticType(ls []Level) string {
	sym := func(i int) Keysym {
		if i < len(ls) && len(ls[i]) > 0 {
			return ls[i][0].K
		}
		return NoSymbol
	}
	n := len(ls)
	switch {
	case n <= 1:
		return "ONE_LEVEL"
	case n == 2:
		if sym(0).IsLower() && sym(1).IsUpper() {
			return "ALPHABETIC"
		}
		if sym(0).IsKeypad() || sym(1).IsKeypad() {
			return "KEYPAD"
		}
		return "TWO_LEVEL"
	case n <= 4:
		if sym(0).IsLower() && sym(1).IsUpper() {
			if sym(2).IsLower() && sym(3).IsUpper() {
				return "FOUR_LEVEL_ALPHABETIC"
			}
			return "FOUR_LEVEL_SEMIALPHABETIC"
		}
		if sym(0).IsKeypad() || sym(1).IsKeypad() {
			return "FOUR_LEVEL_KEYPAD"
		}
		return "FOUR_LEVEL"
	}
	return ""
}

// Level returns the level (1-based) the type gives in the modifier state s, and the modifiers it consumes there
// (its modifiers less those preserved).
func (t *KeyType) Level(s Mods) (int, Mods) {
	act := s & t.Mods
	for _, e := range t.Map {
		if e.Mods == act {
			return e.Level, t.Mods &^ e.Preserve
		}
	}
	return 1, t.Mods
}

// Type evaluates a level list under a type in the modifier state s: the keysyms typed, with Caps Lock's
// transformation applied (each keysym to its upper case) when Lock is active and not consumed.
func (t *KeyType) Type(ls []Level, s Mods) Level {
	lv, consumed := t.Level(s)
	if lv > len(ls) {
		return nil
	}
	l := ls[lv-1]
	if s&Lock != 0 && consumed&Lock == 0 {
		out := make(Level, len(l))
		for i, x := range l {
			out[i] = Sym{K: x.K.Upper(), Unknown: x.Unknown}
		}
		return out
	}
	return l
}
