package dllkbl

import (
	"encoding/binary"
	"math/rand"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/pe"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// This file is a second, independent DLL encoder for the tests: it writes the tables of dll-format §3 to §5 in
// shapes the tool's own writer never emits (any order of the sub-tables, padding, the short KBDTABLES, other
// sections, other descriptor functions), so that the reader is tested against files "made by other compilers".

// shape says how a foreign file is laid out.
type shape struct {
	arch      windows.Arch
	seed      int64 // order of the blobs and the padding between them
	pad       int   // up to this many padding bytes between blobs
	short     bool  // KBDTABLES without dwType/dwSubType; garbage follows
	ktFirst   bool  // KBDTABLES before every sub-table
	stub      string
	extra     bool // extra sections around .data
	keepEmpty bool // write pKeyNames etc. as empty arrays instead of zero pointers
	nls       bool // export ordinal 2 with a KBDNLSTABLES, and dispatcher exports 5 and 6
	// msklc: the shape MSKLC's compiler gives its files (OBS D2, D9, D29): one section .data (initialised data,
	// execute, read) that holds the tables, the export directory and the descriptor function, the short
	// KBDTABLES, and the E1 table directly behind it, at KBDTABLES + 34 (x86) or + 60 (64-bit slots).
	msklc bool
}

type reloc struct {
	at     int // offset of the pointer slot in the blob
	target int // blob index
	add    int // offset inside the target blob
}

type blob struct {
	data   []byte
	align  int
	relocs []reloc
}

type linker struct {
	p     int
	blobs []*blob
}

func (l *linker) add(b *blob) int {
	if b.align == 0 {
		b.align = 2
	}
	l.blobs = append(l.blobs, b)
	return len(l.blobs) - 1
}

// ptr appends a pointer slot to target (-1: zero pointer).
func (l *linker) ptr(b *blob, target, add int) {
	for len(b.data)%l.p != 0 {
		b.data = append(b.data, 0)
	}
	if target >= 0 {
		b.relocs = append(b.relocs, reloc{len(b.data), target, add})
	}
	b.data = append(b.data, make([]byte, l.p)...)
}

func u16(b *blob, v uint16) { b.data = binary.LittleEndian.AppendUint16(b.data, v) }

func (l *linker) wstr(s []uint16) int {
	b := &blob{align: 2}
	for _, u := range s {
		u16(b, u)
	}
	u16(b, 0)
	return l.add(b)
}

// encodeForeign writes t as a layout DLL of the given shape.
func encodeForeign(t *windows.Tables, sh shape) []byte {
	p := sh.arch.PointerSize()
	l := &linker{p: p}
	// KBDTABLES first in the blob list (blob 0), filled in at the end.
	kt := &blob{align: 8}
	ktID := l.add(kt)
	// MODIFIERS
	vtb := &blob{}
	for _, e := range t.Modifiers.VKToBit {
		vtb.data = append(vtb.data, e.VK, e.Bits)
	}
	vtb.data = append(vtb.data, 0, 0)
	vtbID := l.add(vtb)
	mods := &blob{align: p}
	l.ptr(mods, vtbID, 0)
	u16(mods, uint16(len(t.Modifiers.ModNumber)-1))
	mods.data = append(mods.data, t.Modifiers.ModNumber...)
	modsID := l.add(mods)
	// character tables
	vtw := &blob{align: p}
	for _, ct := range t.CharTables {
		rows := &blob{}
		for _, r := range ct.Rows {
			rows.data = append(rows.data, r.VK, r.Attributes)
			for _, c := range r.Chars {
				u16(rows, c)
			}
		}
		rows.data = append(rows.data, make([]byte, 2+2*ct.Width)...)
		id := l.add(rows)
		l.ptr(vtw, id, 0)
		vtw.data = append(vtw.data, byte(ct.Width), byte(2+2*ct.Width))
		for len(vtw.data)%p != 0 {
			vtw.data = append(vtw.data, 0)
		}
	}
	l.ptr(vtw, -1, 0)
	vtw.data = append(vtw.data, make([]byte, p)...)
	vtwID := l.add(vtw)
	dkID := -1
	if len(t.DeadKeys) > 0 {
		dk := &blob{align: 4}
		for _, d := range t.DeadKeys {
			dk.data = binary.LittleEndian.AppendUint32(dk.data, uint32(d.Dead)<<16|uint32(d.Base))
			u16(dk, d.Composed)
			u16(dk, d.Flags)
		}
		dk.data = append(dk.data, make([]byte, 8)...)
		dkID = l.add(dk)
	}
	names := func(list []windows.KeyName) int {
		if len(list) == 0 && !sh.keepEmpty {
			return -1
		}
		b := &blob{align: p}
		for _, kn := range list {
			s := l.wstr(utf16.Encode([]rune(kn.Name)))
			b.data = append(b.data, kn.Scan)
			l.ptr(b, s, 0)
		}
		b.data = append(b.data, 0)
		l.ptr(b, -1, 0)
		return l.add(b)
	}
	knID, kneID := names(t.KeyNames), names(t.KeyNamesExt)
	kndID := -1
	if len(t.KeyNamesDead) > 0 || sh.keepEmpty {
		b := &blob{align: p}
		for _, dn := range t.KeyNamesDead {
			s := l.wstr(append([]uint16{dn.Dead}, utf16.Encode([]rune(dn.Name))...))
			l.ptr(b, s, 0)
		}
		l.ptr(b, -1, 0)
		kndID = l.add(b)
	}
	vsc := &blob{}
	for _, v := range t.ScanToVK {
		u16(vsc, v)
	}
	vscID := l.add(vsc)
	scanTable := func(list []windows.ScanVK) int {
		b := &blob{}
		for _, e := range list {
			b.data = append(b.data, e.Scan, 0)
			u16(b, e.VK)
		}
		b.data = append(b.data, 0, 0, 0, 0)
		return l.add(b)
	}
	e0ID, e1ID := scanTable(t.E0), scanTable(t.E1)
	ligID := -1
	if len(t.Ligatures) > 0 {
		b := &blob{}
		for _, lg := range t.Ligatures {
			b.data = append(b.data, lg.VK, 0)
			u16(b, lg.Column)
			for i := 0; i < t.LigatureWidth; i++ {
				if i < len(lg.Chars) {
					u16(b, lg.Chars[i])
				} else {
					u16(b, windows.WCH_NONE)
				}
			}
		}
		b.data = append(b.data, make([]byte, 4+2*t.LigatureWidth)...)
		ligID = l.add(b)
	}
	nlsID := -1
	if sh.nls {
		vkf := &blob{align: 4, data: make([]byte, 0x84)}
		vkf.data[0], vkf.data[1], vkf.data[2], vkf.data[3] = 0x14, 2, 1, 0
		vkf.data[4], vkf.data[8] = 5, 0x33
		vkf.data[0x44] = 7
		vkfID := l.add(vkf)
		mouse := &blob{}
		u16(mouse, 0x0162)
		u16(mouse, 0x0163)
		mouseID := l.add(mouse)
		n := &blob{align: p}
		u16(n, 0x0E)
		u16(n, 0x0002)
		n.data = binary.LittleEndian.AppendUint32(n.data, 1)
		l.ptr(n, vkfID, 0)
		n.data = binary.LittleEndian.AppendUint32(n.data, 2)
		l.ptr(n, mouseID, 0)
		nlsID = l.add(n)
	}
	// KBDTABLES
	l.ptr(kt, modsID, 0)
	l.ptr(kt, vtwID, 0)
	l.ptr(kt, dkID, 0)
	l.ptr(kt, knID, 0)
	l.ptr(kt, kneID, 0)
	l.ptr(kt, kndID, 0)
	l.ptr(kt, vscID, 0)
	kt.data = append(kt.data, byte(len(t.ScanToVK)))
	l.ptr(kt, e0ID, 0)
	l.ptr(kt, e1ID, 0)
	kt.data = binary.LittleEndian.AppendUint32(kt.data, uint32(t.Version)<<16|uint32(t.LocaleFlags))
	if t.LigatureWidth > 0 {
		kt.data = append(kt.data, byte(t.LigatureWidth), byte(4+2*t.LigatureWidth), 0, 0)
	} else {
		kt.data = append(kt.data, 0, 0, 0, 0)
	}
	l.ptr(kt, ligID, 0)
	if !sh.short && !sh.msklc {
		kt.data = binary.LittleEndian.AppendUint32(kt.data, t.Type)
		kt.data = binary.LittleEndian.AppendUint32(kt.data, t.SubType)
	}
	// Place the blobs.
	rng := rand.New(rand.NewSource(sh.seed))
	order := rng.Perm(len(l.blobs))
	if sh.ktFirst || sh.short || sh.msklc {
		for i, v := range order {
			if v == ktID {
				order[0], order[i] = order[i], order[0]
			}
		}
	}
	var data []byte
	offs := make([]int, len(l.blobs))
	placed := map[int]bool{}
	for n, i := range order {
		b := l.blobs[i]
		if placed[i] {
			continue
		}
		if sh.msklc && i == ktID {
			offs[i] = len(data)
			data = append(data, b.data...)
			offs[e1ID] = len(data)
			data = append(data, l.blobs[e1ID].data...)
			placed[e1ID] = true
			continue
		}
		if n > 0 && sh.pad > 0 {
			data = append(data, make([]byte, rng.Intn(sh.pad+1))...)
		}
		for len(data)%b.align != 0 {
			data = append(data, 0)
		}
		if sh.short && i == ktID {
			offs[i] = len(data)
			data = append(data, b.data...)
			// What a linker puts behind a short structure (OBS D9): implausible values, and then 8 more bytes so
			// that no table starts inside the full form's extent.
			data = append(data, 0x10, 0xFF, 0x11, 0x02, 0x12, 0x04, 0, 0)
			data = append(data, make([]byte, 8)...)
			continue
		}
		offs[i] = len(data)
		data = append(data, b.data...)
	}
	kind := pe.Abs64
	if p == 4 {
		kind = pe.Abs32
	}
	var fix []pe.Fixup
	for i, b := range l.blobs {
		for _, r := range b.relocs {
			fix = append(fix, pe.Fixup{Offset: uint32(offs[i] + r.at), Kind: kind,
				Target: pe.Ref{Section: ".data", Offset: uint32(offs[r.target] + r.add)}})
		}
	}
	if sh.arch != windows.AMD64 {
		// WOW64 slots: the 32-bit address in the low dword of each 8-byte slot.
		for i := range fix {
			fix[i].Kind = pe.Abs32
		}
	}
	ktOff := uint32(offs[ktID])
	// The descriptor function.
	var code []byte
	var cfix []pe.Fixup
	switch {
	case sh.arch == windows.AMD64 && sh.stub == "movabs":
		code = []byte{0x48, 0xB8, 0, 0, 0, 0, 0, 0, 0, 0, 0xC3}
		cfix = []pe.Fixup{{Offset: 2, Kind: pe.Abs64, Target: pe.Ref{Section: ".data", Offset: ktOff}}}
	case sh.arch == windows.AMD64 && sh.stub == "frame":
		code = []byte{0x55, 0x48, 0x89, 0xE5, 0x48, 0x8D, 0x05, 0, 0, 0, 0, 0x5D, 0xC3}
		cfix = []pe.Fixup{{Offset: 7, Kind: pe.Rel32, Target: pe.Ref{Section: ".data", Offset: ktOff}}}
	case sh.arch == windows.AMD64 && sh.stub == "thunk":
		code = []byte{0xE9, 11, 0, 0, 0, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC, 0xCC,
			0x48, 0x8D, 0x05, 0, 0, 0, 0, 0xC3}
		cfix = []pe.Fixup{{Offset: 19, Kind: pe.Rel32, Target: pe.Ref{Section: ".data", Offset: ktOff}}}
	case sh.arch == windows.AMD64:
		code = []byte{0x48, 0x8D, 0x05, 0, 0, 0, 0, 0xC3}
		cfix = []pe.Fixup{{Offset: 3, Kind: pe.Rel32, Target: pe.Ref{Section: ".data", Offset: ktOff}}}
	case sh.stub == "frame":
		code = []byte{0x55, 0x89, 0xE5, 0xB8, 0, 0, 0, 0, 0x31, 0xD2, 0x5D, 0xC3}
		cfix = []pe.Fixup{{Offset: 4, Kind: pe.Abs32, Target: pe.Ref{Section: ".data", Offset: ktOff}}}
	case sh.arch == windows.WOW64:
		code = []byte{0xB8, 0, 0, 0, 0, 0x99, 0xC3}
		cfix = []pe.Fixup{{Offset: 1, Kind: pe.Abs32, Target: pe.Ref{Section: ".data", Offset: ktOff}}}
	default:
		code = []byte{0xB8, 0, 0, 0, 0, 0xC3}
		cfix = []pe.Fixup{{Offset: 1, Kind: pe.Abs32, Target: pe.Ref{Section: ".data", Offset: ktOff}}}
	}
	var more []pe.Export
	if sh.nls {
		for len(code)%16 != 0 {
			code = append(code, 0xCC)
		}
		at := uint32(len(code))
		if sh.arch == windows.AMD64 {
			code = append(code, 0x48, 0x8D, 0x05, 0, 0, 0, 0, 0xC3)
			cfix = append(cfix, pe.Fixup{Offset: at + 3, Kind: pe.Rel32, Target: pe.Ref{Section: ".data", Offset: uint32(offs[nlsID])}})
		} else {
			code = append(code, 0xB8, 0, 0, 0, 0, 0x99, 0xC3)
			cfix = append(cfix, pe.Fixup{Offset: at + 1, Kind: pe.Abs32, Target: pe.Ref{Section: ".data", Offset: uint32(offs[nlsID])}})
		}
		ret := uint32(len(code))
		code = append(code, 0x33, 0xC0, 0xC3)
		more = []pe.Export{{Name: "KbdNlsLayerDescriptor", Ordinal: 2, Target: pe.Ref{Section: ".text", Offset: at}},
			{Name: "KbdLayerRealDllFile", Ordinal: 5, Target: pe.Ref{Section: ".text", Offset: ret}},
			{Name: "KbdLayerMultiDescriptor", Ordinal: 6, Target: pe.Ref{Section: ".text", Offset: ret}}}
	}
	text := &pe.Section{Name: ".text", Characteristics: pe.SecCode | pe.SecMemExecute | pe.SecMemRead, Data: code, Fixups: cfix}
	ds := &pe.Section{Name: ".data", Characteristics: pe.SecReadOnlyData, Data: data, Fixups: fix}
	sections := []*pe.Section{text, ds}
	codeSec, codeAt := ".text", uint32(0)
	if sh.msklc {
		for len(ds.Data)%16 != 0 {
			ds.Data = append(ds.Data, 0)
		}
		codeAt = uint32(len(ds.Data))
		ds.Data = append(ds.Data, code...)
		for _, f := range cfix {
			f.Offset += codeAt
			ds.Fixups = append(ds.Fixups, f)
		}
		ds.Characteristics = pe.SecInitData | pe.SecMemExecute | pe.SecMemRead
		sections, codeSec = []*pe.Section{ds}, ".data"
		for i := range more {
			more[i].Target = pe.Ref{Section: ".data", Offset: codeAt + more[i].Target.Offset}
		}
	}
	if sh.extra {
		junk := &pe.Section{Name: ".rdata", Characteristics: pe.SecReadOnlyData, Data: []byte("other compiler's read-only data\x00")}
		pdata := &pe.Section{Name: ".pdata", Characteristics: pe.SecReadOnlyData, Data: make([]byte, 24)}
		sections = []*pe.Section{junk, ds, pdata, text}
	}
	im := &pe.Image{Machine: pe.MachineI386, ImageBase: 0x1000_0000, SectionAlignment: 0x1000, FileAlignment: 0x200,
		Characteristics: pe.FileExecutableImage | pe.File32BitMachine | pe.FileDLL, Subsystem: pe.SubsystemNative,
		Sections: sections, DLLName: "foreign.dll", ExportSection: ".data",
		Exports:       []pe.Export{{Name: "KbdLayerDescriptor", Ordinal: 1, Target: pe.Ref{Section: codeSec, Offset: codeAt}}},
		ResourceChars: pe.SecReadOnlyData, RelocChars: pe.SecReadOnlyData}
	im.Exports = append(im.Exports, more...)
	if sh.arch == windows.AMD64 {
		im.Machine, im.PE32Plus, im.ImageBase = pe.MachineAMD64, true, 0x1_8000_0000
		im.Characteristics = pe.FileExecutableImage | pe.FileLargeAddress | pe.FileDLL
	}
	out, _, err := im.Write()
	if err != nil {
		panic(err)
	}
	return out
}
