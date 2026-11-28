package build

import (
	"fmt"
	"strings"

	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/keyname"
)

// StockTypes are the stock key types rule keyType tries, in order (targets/xkb.md §3).
var StockTypes = []string{"ONE_LEVEL", "TWO_LEVEL", "ALPHABETIC", "FOUR_LEVEL", "FOUR_LEVEL_ALPHABETIC",
	"FOUR_LEVEL_SEMIALPHABETIC", "FOUR_LEVEL_PLUS_LOCK", "EIGHT_LEVEL", "EIGHT_LEVEL_ALPHABETIC",
	"EIGHT_LEVEL_SEMIALPHABETIC"}

// Mods is the XKB modifier state of a build state.
func Mods(s int) xkb.Mods {
	var m xkb.Mods
	if s&StShift != 0 {
		m |= xkb.Shift
	}
	if s&StLock != 0 {
		m |= xkb.Lock
	}
	if s&StL3 != 0 {
		m |= xkb.LevelThree
	}
	if s&StL5 != 0 {
		m |= xkb.LevelFive
	}
	return m
}

// TypeOf returns the key type of a name: a stock type or a yakbd type YAKBD_<levels>; nil for neither.
func TypeOf(name string) *xkb.KeyType {
	if t := xkb.StockType(name); t != nil {
		return t
	}
	digits, ok := strings.CutPrefix(name, "YAKBD_")
	if !ok || (len(digits) != 8 && len(digits) != 16) {
		return nil
	}
	t := &xkb.KeyType{Name: name, Mods: xkb.Shift | xkb.Lock | xkb.LevelThree}
	if len(digits) == 16 {
		t.Mods |= xkb.LevelFive
	}
	for s, d := range digits {
		if d < '1' || d > '8' {
			return nil
		}
		lv := int(d - '0')
		t.Levels = max(t.Levels, lv)
		if s != 0 {
			t.Map = append(t.Map, xkb.TypeEntry{Mods: Mods(s), Level: lv})
		}
	}
	return t
}

// levelOf is a value as XKB keysyms.
func (b *builder) levelOf(v Value) xkb.Level {
	if v.Dead != "" {
		if k, ok := b.syms[v.Dead]; ok {
			return xkb.Level{{K: k}}
		}
		return nil
	}
	if v.Text == "" {
		return nil
	}
	l, _ := keysymsOf(v.Text)
	return l
}

// valueOf is what keysyms type.
func (b *builder) valueOf(l xkb.Level) Value {
	if len(l) == 1 && l[0].K.IsDead() {
		for id, k := range b.syms {
			if k == l[0].K {
				return Value{Dead: id}
			}
		}
		return Value{}
	}
	t, _ := l.Text()
	return Value{Text: t}
}

// fit builds the levels of type t for the values of a key and counts the states it types exactly.
func (b *builder) fit(t *xkb.KeyType, vals []Value) ([]Value, int) {
	levels := make([]Value, max(t.Levels, 1))
	set := make([]bool, len(levels))
	for s := range vals {
		if b.unused(s) {
			continue
		}
		lv, _ := t.Level(Mods(s))
		if lv <= len(levels) && !set[lv-1] {
			levels[lv-1], set[lv-1] = vals[s], true
		}
	}
	exact := 0
	for s, want := range vals {
		if b.unused(s) {
			exact++ // the modifier is never on: any level is right
			continue
		}
		lv, consumed := t.Level(Mods(s))
		var got Value
		if lv <= len(levels) {
			got = levels[lv-1]
		}
		// a state with Caps Lock on that the type does not consume: the level must be case-stable, since
		// libxkbcommon and Qt upper-case it and GTK on X11 does not (targets/xkb.md §3)
		if got == want && (s&StLock == 0 || consumed&xkb.Lock != 0 || caseStable(got)) {
			exact++
		}
	}
	for len(levels) > 1 && levels[len(levels)-1].None() {
		levels = levels[:len(levels)-1]
	}
	return levels, exact
}

// key builds one position: rule keyType.
func (b *builder) key(p string) {
	vals := make([]Value, b.states)
	any := false
	for s := range vals {
		vals[s] = b.value(p, s)
		if !vals[s].None() {
			any = true
			if vals[s].Text != "" {
				b.check(p, vals[s].Text)
			}
		}
	}
	if !any {
		return
	}
	k := Key{Position: p, Name: b.names[p], Values: vals}
	pin := b.pin.Keys[p]
	if pos := b.l.Positions[p]; pos != nil && pos.NumLock != nil && *pos.NumLock {
		if r, ok := keyname.ForPosition(p); ok && r.Keypad != "" {
			k.Type = "KEYPAD"
			k.Keypad, k.Levels = r.Keypad, []Value{{}, vals[0]}
			b.numpad = append(b.numpad, p)
			pin.Type = ""
			b.keys = append(b.keys, k)
			return
		}
	}
	path := tp + "keys/" + model.Escape(p) + "/type"
	if f := b.file.Keys[p]; f != nil && f.Type != "" {
		t := TypeOf(f.Type)
		if t == nil {
			b.lost("xkb.keyTypeNotExact", path, "the key type %q is not known", f.Type)
			return
		}
		levels, exact := b.fit(t, vals)
		if exact < len(vals) {
			b.lost("xkb.keyTypeNotExact", path, "with the key type %s, %s types as the layout says in %d of %d states", f.Type, p, exact, len(vals))
		}
		k.Type, k.Levels = f.Type, levels
		pin.Type = f.Type
		if strings.HasPrefix(f.Type, "YAKBD_") {
			b.lost("xkb.customKeyType", path, "%s has the yakbd key type %s, installed with the layout", p, f.Type)
		}
		b.keys = append(b.keys, k)
		return
	}
	best, bestN := "", -1
	var bestLevels []Value
	for _, name := range StockTypes {
		levels, exact := b.fit(xkb.StockType(name), vals)
		if exact == len(vals) {
			k.Type, k.Levels = name, levels
			break
		}
		if exact > bestN {
			best, bestN, bestLevels = name, exact, levels
		}
	}
	if k.Type == "" {
		if b.file.CustomTypes != nil && *b.file.CustomTypes {
			k.Type, k.Levels = b.yakbdType(p, vals)
			if k.Type == "" {
				return
			}
			b.lost("xkb.customKeyType", path, "no stock key type types %s as the layout says: it gets the yakbd key type %s, installed with the layout", p, k.Type)
		} else {
			k.Type, k.Levels = best, bestLevels
			b.lost("xkb.capsLockNotExpressible", path, "no stock key type types %s as the layout says (best: %s, %d of %d states); targets.xkb.customTypes gives it a yakbd type", p, best, bestN, len(vals))
		}
	}
	pin.Type = k.Type
	b.typeRule = append(b.typeRule, path)
	if b.typeValues == nil {
		b.typeValues = map[string]string{}
	}
	b.typeValues[p] = k.Type
	b.keys = append(b.keys, k)
}

// yakbdType is the yakbd key type of a key: the level of each of the 8 (16) states, levels numbered by first
// appearance; Lock always consumed.
func (b *builder) yakbdType(p string, vals []Value) (string, []Value) {
	n := 8
	if b.states == 16 {
		n = 16
	}
	var levels []Value
	var digits strings.Builder
	for s := 0; s < n; s++ {
		// a state with a modifier the build does not use gets the digit of the state without it
		v := vals[b.reduce(s)]
		lv := -1
		for i, x := range levels {
			if x == v {
				lv = i
			}
		}
		if lv < 0 {
			levels = append(levels, v)
			lv = len(levels) - 1
		}
		if lv >= 8 {
			b.lost("xkb.tooManyLevels", "/positions/"+model.Escape(p), "%s types more than 8 different things: an XKB key has at most 8 levels", p)
			return "", nil
		}
		fmt.Fprintf(&digits, "%d", lv+1)
	}
	return "YAKBD_" + digits.String(), levels
}

// check reports outputs XKB cannot hold or not everywhere.
func (b *builder) check(p, t string) {
	if _, ok := keysymsOf(t); !ok {
		if b.once("unenc:" + p + t) {
			b.lost("xkb.outputUnencodable", "/positions/"+model.Escape(p), "%s types %q, which has a character no keysym stands for", p, t)
		}
		return
	}
	if len([]rune(t)) > 1 && b.once("multi:"+p+t) {
		b.multi = append(b.multi, fmt.Sprintf("%s %q", p, t))
	}
}

// unused reports a state with a level modifier the build does not use.
func (b *builder) unused(s int) bool { return s&StL3 != 0 && !b.usesL3 || s&StL5 != 0 && !b.usesL5 }

// reduce is the state without the level modifiers the build does not use.
func (b *builder) reduce(s int) int {
	if !b.usesL3 {
		s &^= StL3
	}
	if !b.usesL5 {
		s &^= StL5
	}
	return s
}
