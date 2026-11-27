package dllkbl

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// TestMSKLCShape: files in the shape MSKLC's compiler gives its DLLs (OBS D2, D9, D29: one executable .data
// section with tables, export directory and descriptor; the short KBDTABLES with the E1 table directly behind it)
// import into files whose build gives the same tables, for every build MSKLC writes (amd64, WOW64, x86).
func TestMSKLCShape(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		tb := randomTables(seed)
		tb.Type, tb.SubType = 0, 0
		for _, a := range windows.Arches {
			dll := encodeForeign(tb, shape{arch: a, seed: seed, msklc: true})
			im, r := windows.ReadImage(dll)
			if im == nil {
				t.Fatalf("seed %d %v: %s", seed, a, r.Summary())
			}
			if !im.ShortForm {
				t.Errorf("seed %d %v: short form not recognised", seed, a)
			}
			checkRoundTrip(t, fmt.Sprintf("msklc seed %d %v", seed, a), tb, dll)
		}
		if t.Failed() {
			return
		}
	}
}

// tablesOf builds the tables of a .klc text with the tool's own build.
func tablesOf(t *testing.T, src string) *windows.Tables {
	t.Helper()
	kf, r := klc.Parse([]byte(src), "t.klc")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := klckbl.FromKLC(kf, "t.klc")
	res, br := build.Lower(l, build.Options{Policy: rebuildPolicy})
	if res == nil {
		t.Fatal(br.Summary())
	}
	tb := res.DLL.Tables
	return &tb
}

// row finds the row of vk (the first one) in tb.
func row(tb *windows.Tables, vk byte) (ti, ri int) {
	for i, ct := range tb.CharTables {
		for j, r := range ct.Rows {
			if r.VK == vk {
				return i, j
			}
		}
	}
	return -1, -1
}

// TestMSKLCQuirks: what MSKLC writes where its tables differ from the tool's (OBS D14, D15, W3), in the MSKLC
// file shape. Each imports with only the loss kinds that say what is not carried, and builds back to the same
// tables wherever Windows reads them.
func TestMSKLCQuirks(t *testing.T) {
	const head = "KBD\tt\t\"T\"\nSHIFTSTATE\n0\n1\n2\n6\nLAYOUT\n"
	t.Run("D14 SGCAPS row filled with 0000", func(t *testing.T) {
		tb := tablesOf(t, head+"10\tQ\tSGCap\tq\tQ\t-1\t0040\n-1\t-1\t0\t00e5\t00c5\nENDKBD\n")
		ti, ri := row(tb, 'Q')
		caps := tb.CharTables[ti].Rows[ri+1]
		for c := 2; c < len(caps.Chars); c++ {
			caps.Chars[c] = 0
		}
		for _, a := range windows.Arches {
			dll := encodeForeign(tb, shape{arch: a, seed: 5, msklc: true})
			diffs, r, _ := roundTrip(t, dll, "t.dll")
			kinds := map[string]bool{}
			for _, e := range r.Entries {
				kinds[e.Code] = true
			}
			// Caps Lock cells above column 1 are never read (D65): reported, not carried.
			if !kinds["dll.unreachableData"] {
				t.Errorf("%v: the 0000 cells are not reported: %s", a, r.Summary())
			}
			for _, d := range diffs {
				if !strings.Contains(d, "Q") && !strings.Contains(d, "51") {
					t.Errorf("%v: unexpected difference %s", a, d)
				}
			}
		}
	})
	t.Run("D15 SGCAPS with a dead key: one FF row", func(t *testing.T) {
		// What kbdutool writes for "10 Q SGCap q 00b4@ ..." (D15): the SGCAPS row, no second Q row, then one FF
		// row with the first Caps Lock character in column 0 and the dead character in column 1.
		tb := tablesOf(t, head+"10\tQ\t0\tq\tQ\t-1\t0040\n1e\tA\t1\ta\tA\t-1\t-1\nDEADKEY\t00b4\n0061\t00e1\nENDKBD\n")
		ti, ri := row(tb, 'Q')
		ct := &tb.CharTables[ti]
		q := ct.Rows[ri]
		q.Attributes = windows.SGCAPS
		q.Chars[1] = windows.WCH_DEAD
		ff := windows.CharRow{VK: 0xFF, Chars: make([]uint16, ct.Width)}
		for c := range ff.Chars {
			ff.Chars[c] = windows.WCH_NONE
		}
		ff.Chars[0], ff.Chars[1] = 0x00E5, 0x00B4
		ct.Rows[ri] = q
		ct.Rows = append(ct.Rows[:ri+1], append([]windows.CharRow{ff}, ct.Rows[ri+1:]...)...)
		tb.DeadKeys = []windows.DeadKey{{Dead: 0xB4, Base: 'a', Composed: 0xE1}}
		for _, a := range windows.Arches {
			dll := encodeForeign(tb, shape{arch: a, seed: 6, msklc: true})
			diffs, r, _ := roundTrip(t, dll, "t.dll")
			if len(diffs) > 0 {
				t.Errorf("%v: %v\n%s", a, diffs, r.Summary())
			}
		}
	})
	t.Run("W3 ligature entry with the shift-state value", func(t *testing.T) {
		tb := tablesOf(t, head+"10\tQ\t0\tq\tQ\t-1\t%%\nLIGATURE\nQ\t3\td834\tdd1e\nENDKBD\n")
		if len(tb.Ligatures) != 1 || tb.Ligatures[0].Column != 3 {
			t.Fatalf("ligatures %+v", tb.Ligatures)
		}
		tb.Ligatures[0].Column = 6 // what kbdutool writes for its automatic ligatures (W3)
		for _, a := range windows.Arches {
			dll := encodeForeign(tb, shape{arch: a, seed: 7, msklc: true})
			l, r := FromDLL(dll, "t.dll", Options{LangID: "0409"})
			if l == nil {
				t.Fatal(r.Summary())
			}
			n := 0
			for _, e := range r.Entries {
				if e.Code == "dll.ligatureMismatch" {
					n++
				}
			}
			if n == 0 {
				t.Errorf("%v: the stray ligature is not reported: %s", a, r.Summary())
			}
		}
	})
}

// TestMSKLCUnownedBits: kbdutool numbers MODIFIERS entries by position but writes the named bit into VK_TO_BIT
// (OBS D12), and accepts states whose bits no entry owns (altgr-012-12, mod-roya-first, mod-two, w-shiftstate-66 in
// the VM run of 2026-11-26): ModNumber then has columns for states no key can make. The import writes those states
// as invalid (dll.unreachableColumn), and its build is accepted and gives the same tables as far as Windows can
// reach them.
func TestMSKLCUnownedBits(t *testing.T) {
	tb := tablesOf(t, "KBD\tt\t\"T\"\nSHIFTSTATE\n0\n1\n2\n6\nLAYOUT\n10\tQ\t0\tq\tQ\t-1\t0040\nENDKBD\n")
	// mod-roya-first: VK_TO_BIT gives OEM_8 bit 10, the column sits at state 8.
	tb.Modifiers.VKToBit = append(tb.Modifiers.VKToBit, windows.VKBit{VK: 0xDF, Bits: 0x10})
	for len(tb.Modifiers.ModNumber) < 13 {
		tb.Modifiers.ModNumber = append(tb.Modifiers.ModNumber, windows.SHFT_INVALID)
	}
	tb.Modifiers.ModNumber[8] = 4
	tb.Modifiers.ModNumber[12] = 4 // altgr-012-12: state 12 without a bit 8 entry
	for ti := range tb.CharTables {
		ct := &tb.CharTables[ti]
		if ct.Width == 4 {
			ct.Width = 5
			for ri := range ct.Rows {
				ct.Rows[ri].Chars = append(ct.Rows[ri].Chars, 0x0101)
			}
		}
	}
	for _, a := range windows.Arches {
		dll := encodeForeign(tb, shape{arch: a, seed: 8, msklc: true})
		diffs, r, rebuilt := roundTrip(t, dll, "t.dll")
		if len(diffs) > 0 {
			t.Errorf("%v: %v\n%s", a, diffs, r.Summary())
		}
		if out, cr := windows.Compile(&windows.DLL{FileName: "t.dll", Tables: *rebuilt}, a); out == nil {
			t.Errorf("%v: the rebuilt tables do not compile: %s", a, cr.Summary())
		}
		found := false
		for _, e := range r.Entries {
			found = found || e.Code == "dll.unreachableColumn"
		}
		if !found {
			t.Errorf("%v: no dll.unreachableColumn: %s", a, r.Summary())
		}
	}
}
