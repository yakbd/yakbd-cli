package dllkbl

import (
	"strings"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"testing"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/target/windows"
)

func rowAt(tb *windows.Tables, pred func(r windows.CharRow) bool) (int, int) {
	for ti, ct := range tb.CharTables {
		for k, r := range ct.Rows {
			if pred(r) {
				return ti, k
			}
		}
	}
	return -1, -1
}

func plain(r windows.CharRow) bool {
	if r.VK == 0xFF || r.Attributes != 0 || r.VK < 'A' || r.VK > 'Z' {
		return false
	}
	for _, c := range r.Chars {
		if c == windows.WCH_DEAD || c == windows.WCH_LGTR {
			return false
		}
	}
	return true
}

func codes(r *loss.Report) map[string]bool {
	out := map[string]bool{}
	for _, e := range r.Entries {
		out[e.Code] = true
	}
	return out
}

// TestOddTables: tables Windows reads in some way the tool's writer never produces. Each case must report its loss
// kind; diffs says whether the rebuilt tables may differ (only where the report says something is not carried).
func TestOddTables(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(tb *windows.Tables)
		kind   string
		diffs  bool
	}{
		{"second row for a VK", func(tb *windows.Tables) {
			ti, k := rowAt(tb, plain)
			r := tb.CharTables[ti].Rows[k]
			dup := windows.CharRow{VK: r.VK, Chars: append([]uint16(nil), r.Chars...)}
			dup.Chars[0] = 'Z'
			tb.CharTables[ti].Rows = append(tb.CharTables[ti].Rows, dup)
		}, "dll.duplicateRow", false},
		{"ligature cell without entry", func(tb *windows.Tables) {
			ti, k := rowAt(tb, plain)
			tb.CharTables[ti].Rows[k].Chars[0] = windows.WCH_LGTR
		}, "dll.ligatureMismatch", false},
		{"unused ligature", func(tb *windows.Tables) {
			tb.Ligatures = append(tb.Ligatures, windows.Ligature{VK: 0x92, Column: 3, Chars: []uint16{'a', 'b'}})
			tb.LigatureWidth = max(tb.LigatureWidth, 2)
		}, "dll.ligatureMismatch", false},
		{"duplicate DEADKEY and odd flags", func(tb *windows.Tables) {
			tb.DeadKeys = append(tb.DeadKeys, windows.DeadKey{Dead: 0x60, Base: 'a', Composed: 'x', Flags: 0x8000},
				windows.DeadKey{Dead: 0x60, Base: 'a', Composed: 'y'})
		}, "dll.unreachableData", false},
		{"DEADKEY flags", func(tb *windows.Tables) {
			tb.DeadKeys = append(tb.DeadKeys, windows.DeadKey{Dead: 0x2DD, Base: 'q', Composed: 'x', Flags: 0x0006})
		}, "", false},
		{"stray FF row", func(tb *windows.Tables) {
			ct := &tb.CharTables[0]
			r := windows.CharRow{VK: 0xFF, Chars: make([]uint16, ct.Width)}
			for i := range r.Chars {
				r.Chars[i] = 'q'
			}
			ct.Rows = append([]windows.CharRow{r}, ct.Rows...)
		}, "dll.unreachableData", false},
		{"empty table", func(tb *windows.Tables) {
			tb.CharTables = append(tb.CharTables, windows.CharTable{Width: 4})
		}, "dll.unreachableData", false},
		{"one-unit ligature", func(tb *windows.Tables) {
			ti, k := rowAt(tb, plain)
			r := &tb.CharTables[ti].Rows[k]
			tb.Ligatures = append(tb.Ligatures, windows.Ligature{VK: r.VK, Column: 0, Chars: []uint16{r.Chars[0]}})
			tb.LigatureWidth = max(tb.LigatureWidth, 1)
			r.Chars[0] = windows.WCH_LGTR
		}, "", false},
		{"two names for a scan code", func(tb *windows.Tables) {
			tb.KeyNames = append(tb.KeyNames, windows.KeyName{Scan: 0x01, Name: "Other"}, windows.KeyName{Scan: 0x02, Name: ""})
		}, "dll.unreachableData", false},
		{"two scan codes with one VK", func(tb *windows.Tables) {
			tb.ScanToVK[0x59] = tb.ScanToVK[0x1E] // no windows.vkDuplicate under useTableA false (D59)
		}, "", false},
		{"VK_TO_BIT entry without a bit", func(tb *windows.Tables) {
			tb.Modifiers.VKToBit = append(tb.Modifiers.VKToBit, windows.VKBit{VK: 0xA5, Bits: 0})
		}, "dll.unreachableData", true},
		{"empty SGCAPS cell", func(tb *windows.Tables) {
			ct := &tb.CharTables[0]
			if ct.Width < 2 {
				ct = &tb.CharTables[1]
			}
			r := windows.CharRow{VK: 0xC1, Attributes: windows.SGCAPS, Chars: make([]uint16, ct.Width)}
			caps := windows.CharRow{VK: 0xC1, Chars: make([]uint16, ct.Width)}
			for i := range r.Chars {
				r.Chars[i], caps.Chars[i] = 'r', windows.WCH_NONE
			}
			caps.Chars[1] = 'R'
			ct.Rows = append(ct.Rows, r, caps)
			tb.ScanToVK[0x59] = 0xC1
		}, "", false}, // D64: the empty Caps Lock cell is an explicit nothing
		{"CAPLOK with an empty Shift cell", func(tb *windows.Tables) {
			ti, k := rowAt(tb, func(r windows.CharRow) bool { return plain(r) && len(r.Chars) >= 2 })
			r := &tb.CharTables[ti].Rows[k]
			r.Attributes, r.Chars[1] = windows.CAPLOK, windows.WCH_NONE
		}, "", false},
		{"dead key only in the Caps Lock row (W-2)", func(tb *windows.Tables) {
			ct := &tb.CharTables[0]
			if ct.Width < 2 {
				ct = &tb.CharTables[1]
			}
			r := windows.CharRow{VK: 0xC1, Attributes: windows.SGCAPS, Chars: make([]uint16, ct.Width)}
			caps := windows.CharRow{VK: 0xC1, Chars: make([]uint16, ct.Width)}
			ff := windows.CharRow{VK: 0xFF, Chars: make([]uint16, ct.Width)}
			for i := range r.Chars {
				r.Chars[i], caps.Chars[i], ff.Chars[i] = 'r', windows.WCH_NONE, windows.WCH_NONE
			}
			caps.Chars[0], caps.Chars[1], ff.Chars[0] = windows.WCH_DEAD, 'R', 0x2D8
			ct.Rows = append(ct.Rows, r, caps, ff)
			tb.ScanToVK[0x59] = 0xC1
		}, "", false},
		{"unknown attribute", func(tb *windows.Tables) {
			ti, k := rowAt(tb, plain)
			tb.CharTables[ti].Rows[k].Attributes = 0x40
		}, "dll.unknownFlags", true},
	}
	for _, c := range cases {
		for _, a := range windows.Arches {
			tb := randomTables(42)
			for len(tb.CharTables) < 2 || tb.CharTables[0].Width < 2 && tb.CharTables[1].Width < 2 {
				t.Fatal("seed 42 has too few tables")
			}
			c.mutate(tb)
			dll := encodeForeign(tb, shape{arch: a, seed: 9, pad: 5})
			diffs, r, _ := roundTrip(t, dll, "odd.dll")
			got := codes(r)
			if c.kind != "" && !got[c.kind] {
				t.Errorf("%s %v: no %s in\n%s", c.name, a, c.kind, r.Summary())
			}
			if (len(diffs) > 0) != c.diffs {
				t.Errorf("%s %v: diffs %v (want any: %v)\n%s", c.name, a, diffs, c.diffs, r.Summary())
			}
		}
	}
}

// TestHostileFiles: damaged and hostile files are refused or read; nothing panics, and every refusal is dll.malformed.
func TestHostileFiles(t *testing.T) {
	tb := randomTables(5)
	for _, a := range windows.Arches {
		dll := encodeForeign(tb, shape{arch: a, seed: 1})
		for n := 0; n < len(dll); n += 37 {
			l, r := FromDLL(dll[:n], "cut.dll", Options{})
			if l != nil && n < 0x200 {
				t.Fatalf("%v: a file cut at %d was read", a, n)
			}
			if l == nil && !r.HasErrors() {
				t.Fatalf("%v cut at %d: refused without an error", a, n)
			}
		}
		// Flip every byte of the file in turn (a sample): no panic, and the result builds or the report says why.
		for i := 0; i < len(dll); i += 11 {
			b := append([]byte(nil), dll...)
			b[i] ^= 0xA5
			FromDLL(b, "flip.dll", Options{})
		}
	}
	// cbSize 0: refused before rows are walked (dll-format §4.2).
	bad := randomTables(5)
	dll := encodeForeign(bad, shape{arch: windows.AMD64, seed: 1})
	im, _ := windows.ReadImage(dll)
	if im == nil {
		t.Fatal("control not read")
	}
	_, r := windows.ReadImage(patchCbSize(t, dll))
	if !r.HasErrors() || !strings.Contains(r.Summary(), "cbSize") {
		t.Errorf("cbSize 0 not refused: %s", r.Summary())
	}
}

// patchCbSize sets cbSize of the first VK_TO_WCHAR_TABLE entry of an amd64 file to 0.
func patchCbSize(t *testing.T, dll []byte) []byte {
	t.Helper()
	b := append([]byte(nil), dll...)
	im, _ := windows.ReadImage(b)
	tb := im.DLL.Tables
	// Find the VK_TO_WCHAR_TABLE array: the 16-byte entries whose bytes 8 and 9 are (n, 2+2n) of every table.
	for off := 0; off+16*len(tb.CharTables) <= len(b); off += 8 {
		ok := true
		for i, ct := range tb.CharTables {
			e := b[off+16*i:]
			if int(e[8]) != ct.Width || int(e[9]) != 2+2*ct.Width {
				ok = false
				break
			}
		}
		if ok {
			b[off+9] = 0
			return b
		}
	}
	t.Fatal("VK_TO_WCHAR_TABLE not found")
	return nil
}

// TestNLSAndDispatcher: the NLS table of export ordinal 2 is carried into targets.windows.nls; the dispatcher
// exports are reported, and the tables still round-trip.
func TestNLSAndDispatcher(t *testing.T) {
	for _, a := range windows.Arches {
		tb := randomTables(11)
		dll := encodeForeign(tb, shape{arch: a, seed: 2, nls: true})
		diffs, r, _ := roundTrip(t, dll, "nls.dll")
		if len(diffs) > 0 {
			t.Errorf("%v: %v", a, diffs)
		}
		if !codes(r)["dll.dispatcherNotRead"] {
			t.Errorf("%v: no dll.dispatcherNotRead: %s", a, r.Summary())
		}
		l, _ := FromDLL(dll, "nls.dll", Options{})
		n := l.Targets.Windows.NLS
		if n == nil || *n.OEMIdentifier != 0x0E || len(n.VkToF) != 1 || n.VkToF[0].VK != "CAPITAL" || n.VkToF[0].Proc[0].Index != 5 ||
			n.VkToF[0].Proc[0].Param != 0x33 || n.VkToF[0].ProcAlt[0].Index != 7 || len(n.MouseVKeys) != 2 || n.MouseVKeys[0].Flags[0] != "ext" {
			t.Errorf("%v: NLS %+v", a, n)
		}
	}
}

// TestDeadKeyWithoutRow: a dead-key cell in the last row of its table has no dead-character row: dll.malformed
// (coverage/windows.md §11), nothing is imported.
func TestDeadKeyWithoutRow(t *testing.T) {
	tb := randomTables(42)
	ct := &tb.CharTables[0]
	r := windows.CharRow{VK: 0xC1, Chars: make([]uint16, ct.Width)}
	for i := range r.Chars {
		r.Chars[i] = windows.WCH_NONE
	}
	r.Chars[0] = windows.WCH_DEAD
	ct.Rows = append(ct.Rows, r)
	tb.ScanToVK[0x59] = 0xC1
	l, rep := FromDLL(encodeForeign(tb, shape{arch: windows.AMD64, seed: 4}), "dead.dll", Options{})
	if l != nil || !codes(rep)["dll.malformed"] {
		t.Errorf("not refused: %s", rep.Summary())
	}
}

// TestModifierDropped: a VK_TO_BIT entry with several bits cannot be carried: dll.modifierDropped, an error (D61).
func TestModifierDropped(t *testing.T) {
	tb := randomTables(42)
	tb.Modifiers.VKToBit = append(tb.Modifiers.VKToBit, windows.VKBit{VK: 0xA5, Bits: 6})
	l, rep := FromDLL(encodeForeign(tb, shape{arch: windows.AMD64, seed: 4}), "mod.dll", Options{})
	if l != nil || !codes(rep)["dll.modifierDropped"] {
		t.Errorf("not refused: %s", rep.Summary())
	}
}

// TestSharedFFRowShape is the shape of the one Windows key that is SGCAPS and dead at once (KBDINUK3, dll §4.2):
// dead keys in the main row (column 0 and an AltGr column), one FF row that is both the dead-character row and the
// Caps Lock row, an empty Caps Lock cell under Shift, and a cell above column 1 that is no dead character. D65: the
// FF row's cells in dead columns are dead characters, AltGr included; its other cells above column 1 are never read
// (dll.unreachableData). The round trip gives the same tables, and the empty cell becomes the explicit nothing
// {"output": ""} of D64.
func TestSharedFFRowShape(t *testing.T) {
	N, D := uint16(windows.WCH_NONE), uint16(windows.WCH_DEAD)
	for seed := int64(1); seed <= 40; seed++ {
		tb := randomTables(seed)
		if tb.LocaleFlags&windows.KLLF_ALTGR == 0 {
			continue
		}
		var ct *windows.CharTable
		for i := range tb.CharTables {
			if tb.CharTables[i].Width >= 5 {
				ct = &tb.CharTables[i]
			}
		}
		if ct == nil {
			continue
		}
		w := ct.Width
		main, ff := make([]uint16, w), make([]uint16, w)
		for c := range main {
			main[c], ff[c] = uint16('a'+c), N
		}
		main[0], ff[0] = D, 0x2C6 // dead key; with Caps Lock the dead character at once
		main[1], ff[1] = 'B', N   // Caps Lock + Shift: nothing (VM-7)
		ff[3] = 'Z'               // never read (D65)
		main[4], ff[4] = D, 0x2DB // a dead key in an AltGr column
		ct.Rows = append(ct.Rows, windows.CharRow{VK: 0xC1, Attributes: windows.SGCAPS, Chars: main}, windows.CharRow{VK: 0xFF, Chars: ff})
		tb.ScanToVK[0x59] = 0xC1
		tb.DeadKeys = append(tb.DeadKeys, windows.DeadKey{Dead: 0x2C6, Base: 'e', Composed: 0xEA}, windows.DeadKey{Dead: 0x2DB, Base: 'a', Composed: 0x105})
		for _, a := range windows.Arches {
			dll := encodeForeign(tb, shape{arch: a, seed: seed})
			diffs, r, _ := roundTrip(t, dll, "inuk.dll")
			if len(diffs) > 0 {
				t.Errorf("seed %d %v: %v\n%s", seed, a, diffs, r.Summary())
			}
			for _, e := range r.Entries {
				if e.Severity >= loss.Warning && !expected[e.Code] && e.Code != "dll.unreachableData" {
					t.Errorf("seed %d %v: %s", seed, a, e)
				}
			}
			l, _ := FromDLL(dll, "inuk.dll", Options{LangID: "0409"})
			data, _ := kbljson.Write(l)
			if !codes(r)["dll.unreachableData"] || !strings.Contains(string(data), `"u02db"`) {
				t.Errorf("seed %d: the FF row is not read as D65 says", seed)
			}
			if !strings.Contains(string(data), `"output": ""`) {
				t.Errorf("seed %d: no explicit nothing in the imported file", seed)
			}
		}
		return
	}
	t.Fatal("no seed with AltGr and a table of width 5")
}
