package wincheck

import (
	"github.com/yakbd/yakbd-cli/pe"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// Emulator translates key presses with a layout's tables by the ToUnicodeEx rules of windows-behaviour §6
// (Caps Lock and Num Lock off, no Alt+number pad). The tests use it as the Prober on Linux; the runner uses the
// real ToUnicodeEx.
type Emulator struct {
	T       *windows.Tables
	pending uint16
}

func (e *Emulator) bits(mods []byte) int {
	b := 0
	for _, m := range mods {
		if m == windows.VKRShift {
			m = 0x10 // the right Shift key sets the Shift state as well
		}
		for _, vb := range e.T.Modifiers.VKToBit {
			if vb.VK == m {
				b |= int(vb.Bits)
			}
		}
	}
	return b
}

// lookup returns the cell of a press and, for a dead key, its dead character.
func (e *Emulator) lookup(p Press) (cell uint16, dead uint16, lig []uint16, ok bool) {
	b := e.bits(p.Mods)
	if b >= len(e.T.Modifiers.ModNumber) {
		return 0, 0, nil, false
	}
	col := int(e.T.Modifiers.ModNumber[b])
	if col == windows.SHFT_INVALID {
		return 0, 0, nil, false
	}
	for _, ct := range e.T.CharTables {
		for i, r := range ct.Rows {
			if r.VK != p.VK {
				continue
			}
			if col >= len(r.Chars) {
				return 0, 0, nil, false
			}
			c := r.Chars[col]
			switch c {
			case windows.WCH_DEAD:
				if i+1 < len(ct.Rows) {
					return c, ct.Rows[i+1].Chars[col], nil, true
				}
			case windows.WCH_LGTR:
				for _, l := range e.T.Ligatures {
					if l.VK == p.VK && int(l.Column) == col {
						return c, 0, l.Chars, true
					}
				}
				return 0, 0, nil, false
			}
			return c, 0, nil, c != windows.WCH_NONE
		}
	}
	return 0, 0, nil, false
}

func (e *Emulator) compose(p, c uint16) (uint16, bool, bool) {
	for _, d := range e.T.DeadKeys {
		if d.Dead == p && d.Base == c {
			return d.Composed, d.Flags&windows.DKF_DEAD != 0, true
		}
	}
	return 0, false, false
}

// ToUnicode implements Prober.
func (e *Emulator) ToUnicode(p Press, flags uint32) (int, []uint16) {
	// KLLF_LRM_RLM with the left Shift key down (the runner's Shift): U+200E, whatever else is down, and the
	// dead-key state stays as it is (windows-behaviour §11; VM run 1).
	if p.VK == 0x08 && e.T.LocaleFlags&windows.KLLF_LRM_RLM != 0 {
		for _, m := range p.Mods {
			if m == 0x10 {
				return 1, []uint16{0x200E}
			}
		}
		for _, m := range p.Mods {
			if m == windows.VKRShift {
				return 1, []uint16{0x200F} // the right Shift key alone: RLM
			}
		}
	}
	peek := flags&Peek != 0
	set := func(v uint16) {
		if !peek {
			e.pending = v
		}
	}
	c, dead, lig, ok := e.lookup(p)
	if !ok {
		return 0, nil
	}
	pend := e.pending
	switch c {
	case windows.WCH_LGTR:
		var out []uint16
		for _, u := range lig {
			if pend != 0 {
				comp, chain, found := e.compose(pend, u)
				switch {
				case found && chain:
					set(comp)
					pend = comp
					continue
				case found:
					out = append(out, comp)
				default:
					out = append(out, pend, u)
				}
				set(0)
				if !peek {
					pend = 0
				}
				continue
			}
			out = append(out, u)
		}
		return len(out), out
	case windows.WCH_DEAD:
		c = dead
		if pend == 0 {
			set(dead)
			return -1, []uint16{dead}
		}
	}
	if pend != 0 {
		comp, chain, found := e.compose(pend, c)
		if found && chain {
			set(comp)
			return -1, nil
		}
		set(0)
		if found {
			return 1, []uint16{comp}
		}
		return 2, []uint16{pend, c}
	}
	return 1, []uint16{c}
}

// DescriptorRelocs inspects layout DLLs read as data (VM-1, part 1): for each 32-bit file whose descriptor
// function is `mov eax, imm32`, whether a HIGHLOW relocation covers the immediate (export RVA + 1).
func DescriptorRelocs(files map[string][]byte) (with, without []string) {
	for name, b := range files {
		f, err := pe.Parse(b)
		if err != nil || f.Machine != pe.MachineI386 {
			continue
		}
		_, exps, err := f.Exports()
		if err != nil {
			continue
		}
		var fn uint32
		for _, x := range exps {
			if x.Ordinal == 1 {
				fn = x.RVA
			}
		}
		code, err := f.Bytes(fn, 1)
		if fn == 0 || err != nil || code[0] != 0xB8 {
			continue
		}
		rels, _ := f.Relocs()
		found := false
		for _, r := range rels {
			found = found || r.RVA == fn+1 && r.Type == pe.RelHighLow
		}
		if found {
			with = append(with, name)
		} else {
			without = append(without, name)
		}
	}
	return with, without
}
