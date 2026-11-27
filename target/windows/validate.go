package windows

import (
	"github.com/yakbd/yakbd-cli/loss"
)

// MaxFileSize is the largest layout file Windows loads (dll-format §1.2).
const MaxFileSize = 262144

// maxRowWidth: a row of width n has cbSize 2 + 2n, which must fit a byte.
const maxRowWidth = 126

// MaxLigatureWidth: cbLgEntry = 4 + 2n must fit a byte (dll-format §4.7).
const MaxLigatureWidth = 125

// Validate checks the writer rules of dll-format §2 to §4 that do not depend on the architecture. It returns a
// report; Compile refuses tables with errors.
func (t *Tables) Validate() *loss.Report {
	r := &loss.Report{}
	where := func(path string) loss.Where { return loss.Where{Path: path} }

	// MODIFIERS (§4.1).
	m := t.Modifiers
	if len(m.ModNumber) == 0 || len(m.ModNumber) > 256 {
		r.Errorf("win.modnumber.size", where("MODIFIERS"), "ModNumber needs 1 to 256 entries, has %d", len(m.ModNumber))
	}
	var allBits byte
	for _, vb := range m.VKToBit {
		if vb.VK == 0 {
			r.Errorf("win.vktobit.zero", where("MODIFIERS"), "VK_TO_BIT entry with virtual key 0 ends the array early")
		}
		allBits |= vb.Bits
	}
	for i, c := range m.ModNumber {
		if c == SHFT_INVALID {
			continue
		}
		if c >= MaxColumns {
			r.Errorf("win.modnumber.column", where("MODIFIERS"), "ModNumber[%d] = %d is not a column (0 to 14 or 15 = invalid)", i, c)
		}
		if byte(i)&^allBits != 0 {
			r.Errorf("win.modnumber.bits", where("MODIFIERS"),
				"ModNumber[%d] uses bits that no VK_TO_BIT entry sets (bits %#x)", i, allBits)
		}
	}
	cols := m.Columns()

	// Character tables (§4.2).
	for ti, ct := range t.CharTables {
		path := "table " + itoa(ti)
		if ct.Width < 1 || ct.Width > maxRowWidth {
			r.Errorf("win.table.width", where(path), "row width %d outside 1 to %d", ct.Width, maxRowWidth)
			continue
		}
		for ri, row := range ct.Rows {
			rp := path + " row " + itoa(ri)
			if len(row.Chars) != ct.Width {
				r.Errorf("win.row.width", where(rp), "row has %d characters in a table of width %d", len(row.Chars), ct.Width)
				continue
			}
			if row.VK == 0 {
				r.Errorf("win.row.vk0", where(rp), "row with virtual key 0 ends the table early")
			}
			dead := false
			for _, c := range row.Chars {
				if c == WCH_DEAD {
					dead = true
				}
			}
			next := (*CharRow)(nil)
			if ri+1 < len(ct.Rows) {
				next = &ct.Rows[ri+1]
			}
			switch {
			case dead && (next == nil || next.VK != 0xFF):
				r.Errorf("win.row.dead", where(rp), "a row with WCH_DEAD must be followed by its dead-character row (virtual key FF)")
			case row.Attributes&SGCAPS != 0 && dead:
				r.Warnf("win.row.sgcaps-dead", where(rp),
					"SGCAPS key with a dead key: the one FF row is also the Caps Lock row, so with Caps Lock the key gives its dead characters at once (dll-format §4.2)")
			case row.Attributes&SGCAPS != 0 && (next == nil || next.VK != row.VK || next.Attributes != 0):
				r.Errorf("win.row.sgcaps", where(rp), "an SGCAPS row must be followed by a row of the same virtual key with attributes 0")
			}
			for ci, c := range row.Chars {
				if c == WCH_LGTR && !t.hasLigature(row.VK, uint16(ci)) {
					r.Errorf("win.row.ligature", where(rp), "ligature cell in column %d has no LIGATURE entry", ci)
				}
			}
		}
	}

	// DEADKEY (§4.3).
	seen := map[uint32]bool{}
	for i, d := range t.DeadKeys {
		k := uint32(d.Dead)<<16 | uint32(d.Base)
		if k == 0 {
			r.Errorf("win.deadkey.zero", where("DEADKEY"), "entry %d has dead and base 0, which ends the table", i)
		}
		if seen[k] {
			r.Warnf("win.deadkey.dup", where("DEADKEY"), "entry %d repeats (%04x, %04x); the first one wins", i, d.Dead, d.Base)
		}
		seen[k] = true
		if d.Flags&^DKF_DEAD != 0 {
			r.Warnf("win.deadkey.flags", where("DEADKEY"), "entry %d has flags %#x; Windows uses only DKF_DEAD", i, d.Flags)
		}
	}

	// Ligatures (§4.7).
	if len(t.Ligatures) > 0 {
		if t.LigatureWidth < 1 || t.LigatureWidth > MaxLigatureWidth {
			r.Errorf("win.ligature.width", where("LIGATURE"), "ligature width %d outside 1 to %d", t.LigatureWidth, MaxLigatureWidth)
		}
		if t.Version == 0 {
			r.Errorf("win.ligature.version", where("LIGATURE"), "ligatures need a version word that is not 0")
		}
		lseen := map[[2]uint16]bool{}
		for i, l := range t.Ligatures {
			if l.VK == 0 {
				r.Errorf("win.ligature.vk0", where("LIGATURE"), "entry %d has virtual key 0, which ends the table", i)
			}
			if len(l.Chars) == 0 || len(l.Chars) > t.LigatureWidth {
				r.Errorf("win.ligature.chars", where("LIGATURE"), "entry %d has %d characters for width %d", i, len(l.Chars), t.LigatureWidth)
			}
			if int(l.Column) >= cols {
				r.Errorf("win.ligature.column", where("LIGATURE"), "entry %d names column %d of %d", i, l.Column, cols)
			}
			k := [2]uint16{uint16(l.VK), l.Column}
			if lseen[k] {
				r.Warnf("win.ligature.dup", where("LIGATURE"), "entry %d repeats (%02x, %d); the first one wins", i, l.VK, l.Column)
			}
			lseen[k] = true
		}
	} else if t.LigatureWidth != 0 {
		r.Errorf("win.ligature.width", where("LIGATURE"), "ligature width %d without ligatures", t.LigatureWidth)
	}

	// Scan-code tables (§4.5, §4.6).
	if len(t.ScanToVK) == 0 || len(t.ScanToVK) > 0xFF {
		r.Errorf("win.vsc.size", where("pusVSCtoVK"), "the scan-code table needs 1 to 255 entries, has %d", len(t.ScanToVK))
	}
	const unnamedFlags = 0xF000
	for sc, v := range t.ScanToVK {
		if v&unnamedFlags != 0 {
			r.Errorf("win.vsc.flags", where("pusVSCtoVK"), "scan code %02x sets flags %#x, which the writer never sets", sc, v&unnamedFlags)
		}
	}
	for _, list := range []struct {
		name string
		l    []ScanVK
	}{{"E0", t.E0}, {"E1", t.E1}} {
		for _, e := range list.l {
			if e.Scan == 0 {
				r.Errorf("win.vscvk.zero", where(list.name), "entry with scan code 0 ends the table early")
			}
			if e.VK&unnamedFlags != 0 {
				r.Errorf("win.vsc.flags", where(list.name), "scan code %02x sets flags %#x, which the writer never sets", e.Scan, e.VK&unnamedFlags)
			}
		}
	}
	for _, kn := range append(append([]KeyName(nil), t.KeyNames...), t.KeyNamesExt...) {
		if kn.Scan == 0 {
			r.Errorf("win.keyname.zero", where("key names"), "key name with scan code 0 ends the table early")
		}
	}

	if t.LocaleFlags&^(KLLF_ALTGR|KLLF_SHIFTLOCK|KLLF_LRM_RLM) != 0 {
		r.Warnf("win.flags.unknown", where("fLocaleFlags"), "locale flags %#x include bits with no defined meaning", t.LocaleFlags)
	}
	if t.Version != KBD_VERSION {
		r.Warnf("win.flags.version", where("fLocaleFlags"), "version word %d; the tool writes %d (dll-format §4.7)", t.Version, KBD_VERSION)
	}
	return r
}

func (t *Tables) hasLigature(vk byte, col uint16) bool {
	for _, l := range t.Ligatures {
		if l.VK == vk && l.Column == col {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
