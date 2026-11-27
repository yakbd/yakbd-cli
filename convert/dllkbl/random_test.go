package dllkbl

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// randomTables makes tables of the kinds Windows' own files have (dll-format §8): Table A scan codes with some
// keys moved or unmapped, rows of several widths in tables of any order, dead keys with chains, ligatures,
// CAPLOK and SGCAPS rows, number-pad rows, extra rows, key names. Nothing in them is a loss: an import must give
// them back exactly (after Canonical).
func randomTables(seed int64) *windows.Tables {
	rng := rand.New(rand.NewSource(seed))
	t := &windows.Tables{Version: 1}
	// Modifiers and columns.
	t.Modifiers.VKToBit = []windows.VKBit{{VK: 0x10, Bits: 1}, {VK: 0x11, Bits: 2}, {VK: 0x12, Bits: 4}}
	states := []int{0, 1, 2}
	if rng.Intn(3) > 0 {
		states = append(states, 6, 7)
		t.LocaleFlags |= windows.KLLF_ALTGR
	}
	if rng.Intn(4) == 0 {
		states = append(states, 3)
	}
	kanaKey := false
	if rng.Intn(5) == 0 {
		kanaVK := byte(0x15)
		if rng.Intn(2) == 0 {
			kanaVK = 0xDF // OEM_8, as the Canadian Multilingual layout (dll-format §4.1)
		}
		t.Modifiers.VKToBit = append(t.Modifiers.VKToBit, windows.VKBit{VK: kanaVK, Bits: 8})
		kanaKey = kanaVK == 0x15
		states = append(states, 8, 9)
	}
	if rng.Intn(6) == 0 {
		t.LocaleFlags |= windows.KLLF_SHIFTLOCK
	}
	sort.Ints(states)
	t.Modifiers.ModNumber = make([]byte, states[len(states)-1]+1)
	for i := range t.Modifiers.ModNumber {
		t.Modifiers.ModNumber[i] = windows.SHFT_INVALID
	}
	cols := len(states)
	colOf := map[int]int{}
	for c, s := range states {
		t.Modifiers.ModNumber[s] = byte(c)
		colOf[s] = c
	}
	// Scan codes: Table A, some keys swapped, some unmapped.
	size := []int{0x7F, 0x7F, 0x80, 0x60, 0x7C}[rng.Intn(5)]
	t.ScanToVK = make([]uint16, size)
	for i := range t.ScanToVK {
		t.ScanToVK[i] = vk.None
	}
	for _, d := range vk.Defaults {
		switch {
		case d.Scan>>8 == 0 && int(d.Scan) < size:
			t.ScanToVK[d.Scan] = d.VK
		case d.Scan>>8 == 0xE0 && rng.Intn(6) > 0:
			t.E0 = append(t.E0, windows.ScanVK{Scan: byte(d.Scan), VK: d.VK})
		case d.Scan == 0xE11D && rng.Intn(4) > 0:
			t.E1 = append(t.E1, windows.ScanVK{Scan: 0x1D, VK: d.VK})
		}
	}
	rng.Shuffle(len(t.E0), func(i, j int) { t.E0[i], t.E0[j] = t.E0[j], t.E0[i] })
	letters := []int{0x10, 0x11, 0x15, 0x1E, 0x2C, 0x2D}
	if rng.Intn(2) == 0 { // AZERTY-like moves
		a, b := letters[rng.Intn(3)], letters[3+rng.Intn(3)]
		t.ScanToVK[a], t.ScanToVK[b] = t.ScanToVK[b], t.ScanToVK[a]
	}
	for i := 0; i < 3; i++ { // unmapped keys
		sc := 0x54 + rng.Intn(size-0x54)
		t.ScanToVK[sc] = vk.None
	}
	if rng.Intn(3) == 0 {
		t.ScanToVK[0x56] = vk.None | 0x0100 // FF with a flag (kept as it is)
	}
	if rng.Intn(3) == 0 { // a key no Table A row has
		t.ScanToVK[0x59] = 0xC1
	}
	// Character rows.
	vks := map[byte]bool{}
	for _, v := range t.ScanToVK {
		if byte(v) != vk.None {
			vks[byte(v)] = true
		}
	}
	var keys []byte
	for v := range vks {
		if v >= '0' && v <= 'Z' || v >= 0xBA && v <= 0xE2 || v == 0xC1 {
			keys = append(keys, v)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	keys = keys[:len(keys)*2/3]
	char := func() uint16 { return uint16(0x21 + rng.Intn(0x2000)) }
	deadChars := []uint16{0x60, 0xB4, 0x5E, 0xA8, 0x7E}[:1+rng.Intn(4)]
	byWidth := map[int][]windows.CharRow{}
	type deadUse struct{ dead uint16 }
	var used []uint16
	for _, v := range keys {
		w := 1 + rng.Intn(cols)
		row := windows.CharRow{VK: v, Chars: make([]uint16, w)}
		var deadRow []uint16
		for c := 0; c < w; c++ {
			switch x := rng.Intn(12); {
			case x == 0:
				row.Chars[c] = windows.WCH_NONE
			case x == 1 && c < 2 && len(deadChars) > 0:
				if deadRow == nil {
					deadRow = make([]uint16, w)
					for i := range deadRow {
						deadRow[i] = windows.WCH_NONE
					}
				}
				d := deadChars[rng.Intn(len(deadChars))]
				row.Chars[c], deadRow[c] = windows.WCH_DEAD, d
				used = append(used, d)
			case x == 2:
				row.Chars[c] = windows.WCH_LGTR
				n := 2 + rng.Intn(3)
				lg := windows.Ligature{VK: v, Column: uint16(c)}
				for i := 0; i < n; i++ {
					lg.Chars = append(lg.Chars, char())
				}
				t.Ligatures = append(t.Ligatures, lg)
				t.LigatureWidth = max(t.LigatureWidth, n)
			default:
				row.Chars[c] = char()
			}
		}
		if row.Chars[w-1] == windows.WCH_NONE {
			row.Chars[w-1] = char()
		}
		rows := []windows.CharRow{row}
		switch {
		case deadRow != nil:
			rows = append(rows, windows.CharRow{VK: 0xFF, Chars: deadRow})
		case w >= 2 && rng.Intn(8) == 0 && row.Chars[0] != windows.WCH_LGTR && row.Chars[1] != windows.WCH_LGTR:
			rows[0].Attributes = windows.SGCAPS
			caps := make([]uint16, w)
			for i := range caps {
				caps[i] = windows.WCH_NONE
			}
			caps[0], caps[1] = char(), char()
			rows = append(rows, windows.CharRow{VK: v, Chars: caps})
		case w >= 2 && rng.Intn(3) == 0 && row.Chars[0] != row.Chars[1] && row.Chars[0] != windows.WCH_NONE && row.Chars[1] != windows.WCH_NONE:
			rows[0].Attributes = windows.CAPLOK
		}
		if _, ok := colOf[6]; ok && w > colOf[7] && rows[0].Attributes&windows.SGCAPS == 0 && rng.Intn(6) == 0 && row.Chars[colOf[6]] != row.Chars[colOf[7]] &&
			row.Chars[colOf[6]] != windows.WCH_NONE && row.Chars[colOf[7]] != windows.WCH_NONE {
			rows[0].Attributes |= windows.CAPLOKALTGR
		}
		if c, ok := colOf[8]; ok && kanaKey && w > c && rng.Intn(3) == 0 && row.Chars[c] != row.Chars[0] &&
			row.Chars[c] < 0xF000 && row.Chars[0] < 0xF000 {
			rows[0].Attributes |= windows.KANALOK
		}
		byWidth[w] = append(byWidth[w], rows...)
	}
	// Number pad (width 1) and an extra row whose VK no scan code gives.
	for i := 0; i < 10; i++ {
		byWidth[1] = append(byWidth[1], windows.CharRow{VK: byte(0x60 + i), Chars: []uint16{uint16('0' + i)}})
	}
	if rng.Intn(2) == 0 {
		byWidth[2] = append(byWidth[2], windows.CharRow{VK: 0x92, Chars: []uint16{'x', 'X'}})
	}
	var widths []int
	for w := range byWidth {
		widths = append(widths, w)
	}
	sort.Ints(widths)
	rng.Shuffle(len(widths), func(i, j int) { widths[i], widths[j] = widths[j], widths[i] })
	for _, w := range widths {
		rows := byWidth[w]
		// Sometimes two tables of one width (dll-format §8: 6,8,4,4).
		if len(rows) > 4 && rng.Intn(4) == 0 {
			cut := len(rows) / 2
			for cut < len(rows) && (rows[cut].VK == 0xFF || rows[cut-1].Attributes&windows.SGCAPS != 0) {
				cut++
			}
			if cut < len(rows) {
				t.CharTables = append(t.CharTables, windows.CharTable{Width: w, Rows: rows[:cut]}, windows.CharTable{Width: w, Rows: rows[cut:]})
				continue
			}
		}
		t.CharTables = append(t.CharTables, windows.CharTable{Width: w, Rows: rows})
	}
	// Dead keys: entries for the dead characters in use, a chain, names.
	seen := map[uint16]bool{}
	for _, d := range used {
		if seen[d] {
			continue
		}
		seen[d] = true
		for i := 0; i < 1+rng.Intn(5); i++ {
			t.DeadKeys = append(t.DeadKeys, windows.DeadKey{Dead: d, Base: uint16('a' + i), Composed: char()})
		}
		t.KeyNamesDead = append(t.KeyNamesDead, windows.DeadName{Dead: d, Name: fmt.Sprintf("DEAD %04X", d)})
	}
	if len(used) > 1 && used[0] != used[1] {
		t.DeadKeys = append(t.DeadKeys, windows.DeadKey{Dead: used[0], Base: used[1], Composed: used[1], Flags: windows.DKF_DEAD})
	}
	rng.Shuffle(len(t.DeadKeys), func(i, j int) { t.DeadKeys[i], t.DeadKeys[j] = t.DeadKeys[j], t.DeadKeys[i] })
	for _, sc := range []byte{0x01, 0x0E, 0x0F, 0x1C, 0x39} {
		t.KeyNames = append(t.KeyNames, windows.KeyName{Scan: sc, Name: fmt.Sprintf("Key %02X", sc)})
	}
	t.KeyNamesExt = []windows.KeyName{{Scan: 0x1C, Name: "Num Enter"}, {Scan: 0x5B, Name: "Left Windows"}}
	rng.Shuffle(len(t.KeyNames), func(i, j int) { t.KeyNames[i], t.KeyNames[j] = t.KeyNames[j], t.KeyNames[i] })
	if rng.Intn(3) == 0 {
		t.Type, t.SubType = 7, 2
	}
	return t
}

// expected are the report kinds an import of a well-formed foreign file may carry.
var expected = map[string]bool{"dll.registrationMissing": true, "dll.containerIgnored": true, "dll.resourceDropped": true,
	"dll.keyboardTypeNotTrusted": true}

func checkRoundTrip(t *testing.T, label string, tb *windows.Tables, dll []byte) {
	t.Helper()
	diffs, r, _ := roundTrip(t, dll, "foreign.dll")
	if len(diffs) > 0 {
		t.Errorf("%s: tables differ after the round trip: %v\n%s", label, diffs, r.Summary())
	}
	for _, e := range r.Entries {
		if e.Severity >= loss.Warning && !expected[e.Code] {
			t.Errorf("%s: unexpected %s", label, e)
		}
	}
}

// TestRandomForeign: random Windows-like tables, written by the second encoder in random shapes, import into files
// whose build gives the same tables.
func TestRandomForeign(t *testing.T) {
	stubs := map[windows.Arch][]string{windows.AMD64: {"", "movabs", "frame", "thunk"}, windows.WOW64: {"", "frame"}, windows.X86: {""}}
	for seed := int64(1); seed <= 300; seed++ {
		tb := randomTables(seed)
		a := windows.Arches[seed%3]
		sh := shape{arch: a, seed: seed, pad: int(seed % 24), stub: stubs[a][int(seed)%len(stubs[a])],
			extra: seed%5 == 0, keepEmpty: seed%7 == 0, ktFirst: seed%2 == 0}
		dll := encodeForeign(tb, sh)
		checkRoundTrip(t, fmt.Sprintf("seed %d %v %+v", seed, a, sh), tb, dll)
		if t.Failed() {
			return
		}
	}
}

// TestRandomThroughOwnWriter: the same tables through the tool's own writer.
func TestRandomThroughOwnWriter(t *testing.T) {
	for seed := int64(1000); seed < 1100; seed++ {
		tb := randomTables(seed)
		d := &windows.DLL{FileName: "rnd.dll", Tables: *tb}
		for _, a := range append(append([]windows.Arch(nil), windows.Arches...), windows.ARM64, windows.ARM64X) {
			dll, r := windows.Compile(d, a)
			if dll == nil {
				t.Fatalf("seed %d: %s", seed, r.Summary())
			}
			checkRoundTrip(t, fmt.Sprintf("seed %d %v", seed, a), tb, dll)
		}
		if t.Failed() {
			return
		}
	}
}

// TestShortForm: a KBDTABLES that ends after pLigature, with garbage behind it, keeps no keyboard type.
func TestShortForm(t *testing.T) {
	tb := randomTables(7)
	tb.Type, tb.SubType = 0, 0
	for _, a := range windows.Arches {
		dll := encodeForeign(tb, shape{arch: a, seed: 3, short: true})
		im, r := windows.ReadImage(dll)
		if im == nil {
			t.Fatal(r.Summary())
		}
		if !im.KeyboardTypeUntrusted || im.DLL.Tables.Type != 0 {
			t.Errorf("%v: type %#x untrusted %v", a, im.DLL.Tables.Type, im.KeyboardTypeUntrusted)
		}
		checkRoundTrip(t, "short "+a.String(), tb, dll)
	}
}

var _ = build.Options{}
