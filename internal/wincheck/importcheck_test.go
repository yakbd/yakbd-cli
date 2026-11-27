package wincheck

import (
	"testing"

	"github.com/yakbd/yakbd-cli/target/windows"
)

// emuKeyboard is a Keyboard on the emulator: the held modifier keys of the state become the press's modifiers.
type emuKeyboard struct{ e *Emulator }

func (k emuKeyboard) Translate(v byte, st *[256]byte, flags uint32) (int, []uint16) {
	var mods []byte
	for _, m := range []byte{0x10, 0x11, 0x12, 0x15, 0xDF} {
		if st[m]&0x80 != 0 {
			mods = append(mods, m)
		}
	}
	return k.e.ToUnicode(Press{VK: v, Mods: mods}, flags)
}

func (k emuKeyboard) ScanToVK(sc uint16) uint32 {
	t := k.e.T
	switch sc >> 8 {
	case 0:
		if int(sc) < len(t.ScanToVK) {
			return uint32(t.ScanToVK[sc] & 0xFF)
		}
	case 0xE0:
		for _, e := range t.E0 {
			if uint16(e.Scan) == sc&0xFF {
				return uint32(e.VK & 0xFF)
			}
		}
	}
	return 0xFF
}

// TestCompare: a layout compared with itself has no difference; one changed cell, one changed dead-key entry and
// one changed scan code are found.
func TestCompare(t *testing.T) {
	d := tablesOf(t, "../../testdata/reference-spec/vm/kbdtst.klc")
	same := Compare(emuKeyboard{&Emulator{T: &d.Tables}}, emuKeyboard{&Emulator{T: &d.Tables}}, nil)
	if same.Diffs != 0 || same.Probes < 254*32 || same.Seqs != 0 {
		t.Fatalf("self comparison %+v", same)
	}
	broken := d.Tables
	broken.CharTables = append([]windows.CharTable(nil), d.Tables.CharTables...)
	for ti := range broken.CharTables {
		ct := &broken.CharTables[ti]
		ct.Rows = append([]windows.CharRow(nil), ct.Rows...)
		for k := range ct.Rows {
			if ct.Rows[k].VK == 'Q' {
				ct.Rows[k].Chars = append([]uint16(nil), ct.Rows[k].Chars...)
				ct.Rows[k].Chars[0] = 'z'
			}
		}
	}
	broken.DeadKeys = append([]windows.DeadKey(nil), d.Tables.DeadKeys...)
	broken.DeadKeys[0].Composed = 'z'
	broken.ScanToVK = append([]uint16(nil), d.Tables.ScanToVK...)
	broken.ScanToVK[0x10], broken.ScanToVK[0x11] = broken.ScanToVK[0x11], broken.ScanToVK[0x10]
	c := Compare(emuKeyboard{&Emulator{T: &d.Tables}}, emuKeyboard{&Emulator{T: &broken}}, nil)
	if c.Keys == 0 || c.Seqs == 0 || c.Scans != 2 {
		t.Errorf("broken comparison %+v", c)
	}
}

func TestImportSlots(t *testing.T) {
	seen := map[string]bool{}
	ids := map[uint16]bool{}
	for _, r := range []string{"64", "32"} {
		for i := 0; i < 256; i++ {
			for _, rb := range []bool{false, true} {
				s := ImportSlot(r, i, rb)
				if seen[s.KLID] || seen[s.File] || len(s.KLID) != 8 || s.LayoutID >= 0x0A00 || s.LayoutID < 0x0100 || ids[s.LayoutID] {
					t.Fatalf("slot %+v", s)
				}
				seen[s.KLID], seen[s.File] = true, true
				ids[s.LayoutID] = true
			}
		}
	}
}
