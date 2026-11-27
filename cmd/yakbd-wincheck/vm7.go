package main

import (
	"fmt"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// VM-7 (kbl-spec targets/windows-vm-tests.md): an SGCAPS key whose Caps Lock row has WCH_NONE in a
// column where the main row has output. Does Windows type the main row's output with Caps Lock on, or nothing?
// The test layout is the tool's own (no Windows data): Q is SGCAPS with main row q Q and Caps Lock row NONE X,
// W is SGCAPS with main row w W and Caps Lock row Y NONE; A is a plain control key.

// vm7Tables is the VM-7 test layout.
func vm7Tables() *windows.Tables {
	t := &windows.Tables{Version: windows.KBD_VERSION,
		Modifiers: windows.Modifiers{VKToBit: []windows.VKBit{{VK: 0x10, Bits: 1}, {VK: 0x11, Bits: 2}, {VK: 0x12, Bits: 4}}, ModNumber: []byte{0, 1}}}
	t.ScanToVK = make([]uint16, vk.MaxVSC)
	for i := range t.ScanToVK {
		t.ScanToVK[i] = vk.None
	}
	for _, d := range vk.Defaults {
		switch d.Scan >> 8 {
		case 0:
			t.ScanToVK[d.Scan] = d.VK
		case 0xE0:
			t.E0 = append(t.E0, windows.ScanVK{Scan: byte(d.Scan), VK: d.VK})
		case 0xE1:
			t.E1 = append(t.E1, windows.ScanVK{Scan: byte(d.Scan), VK: d.VK})
		}
	}
	N := uint16(windows.WCH_NONE)
	t.CharTables = []windows.CharTable{{Width: 2, Rows: []windows.CharRow{
		{VK: 'A', Chars: []uint16{'a', 'A'}},
		{VK: 'Q', Attributes: windows.SGCAPS, Chars: []uint16{'q', 'Q'}}, {VK: 'Q', Chars: []uint16{N, 'X'}},
		{VK: 'W', Attributes: windows.SGCAPS, Chars: []uint16{'w', 'W'}}, {VK: 'W', Chars: []uint16{'Y', N}},
	}}}
	return t
}

// vm7 installs, loads and probes the VM-7 layout and returns the result line.
func vm7(is importSystem, runner string) string {
	a := windows.AMD64
	switch is.Build() {
	case "wow64":
		a = windows.WOW64
	case "x86":
		a = windows.X86
	}
	b, r := windows.Compile(&windows.DLL{FileName: "ykvm7.dll", Tables: *vm7Tables()}, a)
	if b == nil {
		return "VM-7: the test layout was not built: " + r.Summary()
	}
	s := wincheck.ImportSlot(runner, 255, false)
	defer is.Uninstall(s)
	if err := is.Install(s, b, "yakbd VM-7"); err != nil {
		return "VM-7: install failed: " + err.Error()
	}
	h, err := is.Load(s.KLID)
	if err != nil {
		return "VM-7: LoadKeyboardLayoutW: " + err.Error()
	}
	defer is.Unload(h)
	k := is.Keyboard(h)
	probe := func(v byte, shift, caps bool) string {
		st := new([256]byte)
		if shift {
			st[0x10], st[0xA0] = 0x80, 0x80
		}
		if caps {
			st[0x14] = 0x01
		}
		rc, u := k.Translate(v, st, wincheck.Peek)
		return wincheck.Describe(rc, u)
	}
	return fmt.Sprintf("VM-7 (%s): Caps Lock+Q (Caps Lock row NONE, main q) gives %s; Caps Lock+Shift+W (Caps Lock row NONE, main W) gives %s; "+
		"controls: Caps Lock+Shift+Q %s (row: X), Caps Lock+W %s (row: Y), Q %s (q), Caps Lock+A %s (a)",
		is.Build(), probe('Q', false, true), probe('W', true, true), probe('Q', true, true), probe('W', false, true),
		probe('Q', false, false), probe('A', false, true))
}
