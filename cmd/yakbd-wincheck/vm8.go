package main

import (
	"fmt"
	"strings"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// VM-8 (kbl-spec targets/windows-vm-tests.md, D65): does Caps Lock with AltGr ever read the Caps Lock row
// of an SGCAPS key? Test layouts of yakbd's own (no Windows data), KLLF_ALTGR, columns for states 0, 1, 2, 6, 7:
//
//	shape A: Q SGCAPS, main row q Q – å Å, Caps Lock row X Y – Z W
//	shape B: Q SGCAPS, main row DEAD Q – DEAD Å, one FF row ´ – – ˛ Z (shared shape), entries ´a → á, ˛a → ą
//	both:    W CAPLOKALTGR only, w W – ŵ Ŵ (control); A plain, a A (the key after a dead key)
func vm8Tables(shapeB bool) *windows.Tables {
	t := vm7Tables()
	t.LocaleFlags = windows.KLLF_ALTGR
	t.Modifiers.ModNumber = []byte{0, 1, 2, windows.SHFT_INVALID, windows.SHFT_INVALID, windows.SHFT_INVALID, 3, 4}
	N, D := uint16(windows.WCH_NONE), uint16(windows.WCH_DEAD)
	rows := []windows.CharRow{{VK: 'A', Chars: []uint16{'a', 'A', N, N, N}}}
	if shapeB {
		rows = append(rows, windows.CharRow{VK: 'Q', Attributes: windows.SGCAPS, Chars: []uint16{D, 'Q', N, D, 0xC5}},
			windows.CharRow{VK: 0xFF, Chars: []uint16{0xB4, N, N, 0x2DB, 'Z'}})
		t.DeadKeys = []windows.DeadKey{{Dead: 0xB4, Base: 'a', Composed: 0xE1}, {Dead: 0x2DB, Base: 'a', Composed: 0x105}}
	} else {
		rows = append(rows, windows.CharRow{VK: 'Q', Attributes: windows.SGCAPS, Chars: []uint16{'q', 'Q', N, 0xE5, 0xC5}},
			windows.CharRow{VK: 'Q', Chars: []uint16{'X', 'Y', N, 'Z', 'W'}})
	}
	rows = append(rows, windows.CharRow{VK: 'W', Attributes: windows.CAPLOKALTGR, Chars: []uint16{'w', 'W', N, 0x175, 0x174}})
	t.CharTables = []windows.CharTable{{Width: 5, Rows: rows}}
	return t
}

// vm8 runs both shapes and returns one result line each.
func vm8(is importSystem, runner string) []string {
	a := windows.AMD64
	switch is.Build() {
	case "wow64":
		a = windows.WOW64
	case "x86":
		a = windows.X86
	}
	var out []string
	for i, shapeB := range []bool{false, true} {
		name := map[bool]string{false: "A (two rows)", true: "B (shared FF row)"}[shapeB]
		b, r := windows.Compile(&windows.DLL{FileName: "ykvm8.dll", Tables: *vm8Tables(shapeB)}, a)
		if b == nil {
			out = append(out, "VM-8 shape "+name+": the test layout was not built: "+r.Summary())
			continue
		}
		s := wincheck.ImportSlot(runner, 253+i, false)
		if err := is.Install(s, b, "yakbd VM-8"); err != nil {
			out = append(out, "VM-8 shape "+name+": install failed: "+err.Error())
			is.Uninstall(s)
			continue
		}
		h, err := is.Load(s.KLID)
		if err != nil {
			out = append(out, "VM-8 shape "+name+": LoadKeyboardLayoutW: "+err.Error())
			is.Uninstall(s)
			continue
		}
		k := is.Keyboard(h)
		state := func(caps, shift, altgr bool) *[256]byte {
			st := new([256]byte)
			if caps {
				st[0x14] = 0x01
			}
			if shift {
				st[0x10], st[0xA0] = 0x80, 0x80
			}
			if altgr {
				st[0x11], st[0xA2], st[0x12], st[0xA5] = 0x80, 0x80, 0x80, 0x80
			}
			return st
		}
		probe := func(v byte, caps, shift, altgr bool) string {
			rc, u := k.Translate(v, state(caps, shift, altgr), wincheck.Peek)
			return wincheck.Describe(rc, u)
		}
		seq := func(caps, shift, altgr bool) string {
			rc, u := k.Translate('Q', state(caps, shift, altgr), 0)
			first := wincheck.Describe(rc, u)
			rc, u = k.Translate('A', state(caps, false, false), 0)
			res := first + " then A: " + wincheck.Describe(rc, u)
			for j := 0; j < 3; j++ { // leave nothing pending
				if rc, _ := k.Translate('A', state(false, false, false), 0); rc >= 0 {
					break
				}
			}
			return res
		}
		var parts []string
		for _, c := range []struct {
			label             string
			caps, shift, altg bool
		}{{"Caps+AltGr", true, false, true}, {"Caps+Shift+AltGr", true, true, true}, {"AltGr", false, false, true},
			{"Shift+AltGr", false, true, true}, {"Caps", true, false, false}, {"Caps+Shift", true, true, false}} {
			p := fmt.Sprintf("Q %s %s; W %s %s", c.label, probe('Q', c.caps, c.shift, c.altg), c.label, probe('W', c.caps, c.shift, c.altg))
			if shapeB {
				p += fmt.Sprintf("; Q-then-A %s %s", c.label, seq(c.caps, c.shift, c.altg))
			}
			parts = append(parts, p)
		}
		is.Unload(h)
		is.Uninstall(s)
		out = append(out, fmt.Sprintf("VM-8 shape %s (%s): %s", name, is.Build(), strings.Join(parts, " | ")))
	}
	return out
}
