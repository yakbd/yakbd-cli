package wincheck

import (
	"fmt"
	"sort"
)

// The import check (yakbd-wincheck -import, docs/dll-import.md §5) compares two loaded layouts by what Windows
// does with them: an original layout file and the file yakbd rebuilt from its import. It never looks at the
// characters themselves, only at whether both layouts give the same result, so nothing of the original file has to
// leave the guest.

// Keyboard is one loaded layout.
type Keyboard interface {
	// Translate is ToUnicodeEx with a full key state (flags as ToUnicodeEx: Peek leaves the dead-key state alone).
	Translate(vk byte, state *[256]byte, flags uint32) (rc int, units []uint16)
	// ScanToVK is MapVirtualKeyEx(sc, MAPVK_VSC_TO_VK_EX); sc has the E0 or E1 prefix in the high byte.
	ScanToVK(sc uint16) uint32
}

// Modifier keys of a probe state.
const (
	vkShift, vkControl, vkMenu = 0x10, 0x11, 0x12
	vkLShift, vkLControl       = 0xA0, 0xA2
	vkLMenu, vkRMenu           = 0xA4, 0xA5
	vkCapital, vkKana          = 0x14, 0x15
)

// ProbeStates returns the key states the check uses: every combination of Shift, Ctrl, left Alt, right Alt and
// the extra modifier keys (at most four, VK_TO_BIT entries beyond Shift, Ctrl and Alt of either layout), each with
// Caps Lock off and on, and with Kana toggled when a layout has VK_KANA as a modifier.
func ProbeStates(extra []byte) []*[256]byte {
	seen := map[byte]bool{}
	var ex []byte
	for _, v := range extra {
		if v == vkShift || v == vkControl || v == vkMenu || seen[v] || len(ex) == 4 {
			continue
		}
		seen[v] = true
		ex = append(ex, v)
	}
	sort.Slice(ex, func(i, j int) bool { return ex[i] < ex[j] })
	toggles := []int{0, 1}
	if seen[vkKana] {
		toggles = []int{0, 1, 2, 3}
	}
	n := 4 + len(ex)
	var out []*[256]byte
	for _, tg := range toggles {
		for m := 0; m < 1<<n; m++ {
			s := new([256]byte)
			if m&1 != 0 {
				s[vkShift], s[vkLShift] = 0x80, 0x80
			}
			if m&2 != 0 {
				s[vkControl], s[vkLControl] = 0x80, 0x80
			}
			if m&4 != 0 {
				s[vkMenu], s[vkLMenu] = 0x80, 0x80
			}
			if m&8 != 0 {
				s[vkMenu], s[vkRMenu] = 0x80, 0x80
			}
			for i, v := range ex {
				if m&(16<<i) != 0 {
					s[v] = 0x80
				}
			}
			if tg&1 != 0 {
				s[vkCapital] |= 0x01
			}
			if tg&2 != 0 {
				s[vkKana] |= 0x01
			}
			out = append(out, s)
		}
	}
	return out
}

// Describe describes a ToUnicodeEx result: "dead XXXX", "chain", "none" or "chars ...".
func Describe(rc int, units []uint16) string { return outcome(rc, units) }

func outcome(rc int, units []uint16) string {
	switch {
	case rc < 0 && len(units) > 0:
		return fmt.Sprintf("dead %04x", units[0])
	case rc < 0:
		return "chain"
	case rc == 0:
		return "none"
	}
	if rc > len(units) {
		rc = len(units)
	}
	s := "chars"
	for _, u := range units[:rc] {
		s += fmt.Sprintf(" %04x", u)
	}
	return s
}

// Comparison is the result of comparing two layouts: how many probes were made and how many gave different
// results (single keys, dead key + key sequences, scan codes).
type Comparison struct {
	Probes, Diffs int
	Keys, Seqs    int // differing single keys and sequences
	Scans         int // differing scan-code mappings
	// Samples are the first differing probes (at most MaxSamples), for Classify; they stay inside the guest.
	Samples []Probe `json:",omitempty"`
}

// Probe names one probe: a key in a state (index into ProbeStates), after a dead key in a state for a sequence,
// or a scan code.
type Probe struct {
	VK, DeadVK       byte
	State, DeadState int
	Seq              bool
	Scan             uint16
}

// MaxSamples bounds Comparison.Samples.
const MaxSamples = 500

func (c *Comparison) sample(p Probe) {
	if len(c.Samples) < MaxSamples {
		c.Samples = append(c.Samples, p)
	}
}

// MaxSequences bounds the dead key + key sequences of one comparison: beyond it only dead keys and following keys
// with Shift, Ctrl and the Alt keys are tried (Caps Lock off), and then only as many dead keys as fit.
const MaxSequences = 300000

// Compare probes both layouts with every virtual key in every state of ProbeStates (single keys with Peek), every
// dead key either layout has followed by every key, and every scan code, and counts the differences.
func Compare(a, b Keyboard, extra []byte) Comparison {
	var c Comparison
	states := ProbeStates(extra)
	type press struct {
		vk byte
		st *[256]byte
		si int
	}
	var dead []press
	var reset *press
	for v := 1; v < 0xFF; v++ {
		for si, st := range states {
			ra, ua := a.Translate(byte(v), st, Peek)
			rb, ub := b.Translate(byte(v), st, Peek)
			c.Probes++
			if outcome(ra, ua) != outcome(rb, ub) {
				c.Diffs++
				c.Keys++
				c.sample(Probe{VK: byte(v), State: si})
			}
			if ra < 0 || rb < 0 {
				dead = append(dead, press{byte(v), st, si})
			}
			if si == 0 && reset == nil && ra == 1 && rb == 1 && v >= 0x30 {
				reset = &press{byte(v), st, si}
			}
		}
	}
	clear := func(k Keyboard) {
		for i := 0; i < 4 && reset != nil; i++ {
			if rc, _ := k.Translate(reset.vk, reset.st, 0); rc >= 0 {
				return
			}
		}
	}
	if reset != nil && len(dead) > 0 {
		bases := states
		if len(dead)*254*len(bases) > MaxSequences {
			// Dead keys and following keys with Shift, Ctrl and the Alt keys only (Caps Lock off), then a hard cap.
			bases = states[:min(len(states), 16)]
			var d16 []press
			for _, d := range dead {
				if d.si < 16 {
					d16 = append(d16, d)
				}
			}
			dead = d16
			if n := MaxSequences / (254 * len(bases)); len(dead) > n {
				dead = dead[:n]
			}
		}
		clear(a)
		clear(b)
		for _, d := range dead {
			for v := 1; v < 0xFF; v++ {
				for bi, st := range bases {
					ra, ua := a.Translate(d.vk, d.st, 0)
					rb, ub := b.Translate(d.vk, d.st, 0)
					sa, sb := outcome(ra, ua), outcome(rb, ub)
					if ra < 0 {
						ra, ua = a.Translate(byte(v), st, 0)
						sa += " " + outcome(ra, ua)
					}
					if rb < 0 {
						rb, ub = b.Translate(byte(v), st, 0)
						sb += " " + outcome(rb, ub)
					}
					c.Probes++
					if sa != sb {
						c.Diffs++
						c.Seqs++
						c.sample(Probe{VK: byte(v), State: bi, DeadVK: d.vk, DeadState: d.si, Seq: true})
					}
					clear(a)
					clear(b)
				}
			}
		}
	}
	for _, pre := range []uint16{0, 0xE000, 0xE100} {
		for sc := uint16(1); sc < 0x80; sc++ {
			c.Probes++
			if a.ScanToVK(pre|sc) != b.ScanToVK(pre|sc) {
				c.Diffs++
				c.Scans++
				c.sample(Probe{Scan: pre | sc})
			}
		}
	}
	return c
}

// ImportSlot is where the import check puts one of the two files of one layout: a test file name, KLID and Layout
// Id of its own. The 64-bit runner uses KLIDs a4NN0409/a5NN0409 and Layout Ids 0200+j, the 32-bit runner
// a6NN/a7NN and 0400+j (j = 2 × file + 0 for the original, 1 for the rebuilt file; at most 256 files), apart from
// the slots of the other runs (installation §3.3: ids above 00ff are not used by Windows).
func ImportSlot(runner string, file int, rebuilt bool) Slot {
	j := 2 * file
	if rebuilt {
		j++
	}
	hi, base, prefix := 0x4, uint16(0x0200), "ykti"
	if runner == "32" {
		hi, base, prefix = 0x6, 0x0400, "yktj"
	}
	return Slot{
		File:     fmt.Sprintf("%s%03x.dll", prefix, j),
		KLID:     fmt.Sprintf("a%x%02x0409", hi+j>>8, j&0xFF),
		LayoutID: base + uint16(j&0x1FF),
	}
}
