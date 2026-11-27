package windows

import (
	"fmt"
	"unicode/utf16"
)

// SupplementaryEntry returns the DEADKEY entry that makes a dead key followed by a key whose ligature types the
// supplementary character base give the supplementary character result, by the recipe of windows-behaviour §8
// (spec-v9): the entry (dead, high surrogate of base) → high surrogate of result, flags 0; the low surrogate is
// the base key's own. It fails when the two characters do not share their low surrogate, because only the high
// surrogate can be changed this way.
//
// The base key must type base as a two-unit ligature (high, low); that is the caller's job. Several keys whose
// ligatures start with the same high surrogate share the entry: SupplementaryEntries checks that.
func SupplementaryEntry(dead uint16, base, result rune) (DeadKey, error) {
	bh, bl := utf16.EncodeRune(base)
	rh, rl := utf16.EncodeRune(result)
	if bh == 0xFFFD || rh == 0xFFFD {
		return DeadKey{}, fmt.Errorf("U+%04X and U+%04X must both be supplementary characters", base, result)
	}
	if bl != rl {
		return DeadKey{}, fmt.Errorf("U+%04X and U+%04X have different low surrogates (%04X, %04X); a dead key can change only the high surrogate", base, result, bl, rl)
	}
	return DeadKey{Dead: dead, Base: uint16(bh), Composed: uint16(rh)}, nil
}

// Composition is one wanted dead-key result: dead character, base character, result character.
type Composition struct {
	Dead         uint16
	Base, Result rune
}

// SupplementaryEntries lowers a set of supplementary compositions into DEADKEY entries. Two compositions that need
// the same (dead, high surrogate) entry with different results cannot both be built; the first wins and the
// others are returned as errors, one per lost composition.
func SupplementaryEntries(comps []Composition) ([]DeadKey, []error) {
	var out []DeadKey
	var errs []error
	have := map[uint32]DeadKey{}
	for _, c := range comps {
		e, err := SupplementaryEntry(c.Dead, c.Base, c.Result)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		k := uint32(e.Dead)<<16 | uint32(e.Base)
		if prev, ok := have[k]; ok {
			if prev.Composed != e.Composed {
				errs = append(errs, fmt.Errorf("dead %04X + U+%04X → U+%04X conflicts with an earlier composition that shares the entry (%04X, %04X) → %04X",
					c.Dead, c.Base, c.Result, e.Dead, e.Base, prev.Composed))
			}
			continue
		}
		have[k] = e
		out = append(out, e)
	}
	return out, errs
}
