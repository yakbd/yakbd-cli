package windows

import (
	"encoding/binary"
	"fmt"
	"sort"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/pe"
)

// Limits that keep the reader finite on hostile input (docs/dll-import.md, "Never trust the file").
const (
	maxRows      = 4096     // rows of one character table
	maxTables    = 64       // entries of VK_TO_WCHAR_TABLE
	maxEntries   = 65536    // DEADKEY, ligature and dead-key name entries
	maxString    = 4096     // UTF-16 units of one key name
	maxScanTable = 256      // VSC_LPWSTR and VSC_VK entries
	maxNLS       = 256      // VK_F and mouse VK entries
	MaxInput     = 64 << 20 // the largest file the reader looks at
)

// NLSTables is KBDNLSTABLES, the table of export ordinal 2 (dll-format §6.1).
type NLSTables struct {
	OEMIdentifier     uint16
	LayoutInformation uint16
	VkToF             []VKF
	MouseVKeys        []uint16 // VK in the low byte, flags in the high byte
}

// VKF is one VK_F entry.
type VKF struct {
	VK, ProcType, ProcCurrent, ProcSwitch byte
	Proc, ProcAlt                         [8]VKFParam
}

// VKFParam is one VK_FPARAM.
type VKFParam struct {
	Index byte
	Param uint32
}

// Image is what ReadImage finds in a layout DLL: the DLL value Compile would take, plus the facts about the file
// that have no place in it.
type Image struct {
	DLL  *DLL
	Arch Arch
	// NLS is the KBDNLSTABLES of export ordinal 2, nil without that export.
	NLS *NLSTables
	// Exports lists the ordinals the file exports.
	Exports []uint16
	// ShortForm reports a KBDTABLES that ends after pLigature (dll-format §3): dwType and dwSubType were not read.
	ShortForm bool
	// KeyboardTypeUntrusted reports dwType/dwSubType values that were not kept (dll-format §3, "short-form trap").
	KeyboardTypeUntrusted bool
	// LigatureWidthRead is nLgMax as stored (Tables.LigatureWidth keeps it too); entries end at the first WCH_NONE.
	LigatureWidthRead int
	// Container lists what the file has that the tool's own builds do not (sections, entry point, imports, ...).
	Container []string
	// OtherResources lists resources the reader did not decode ("type/name/lang").
	OtherResources []string
}

// Read parses a layout DLL and returns its tables, or the first error ReadImage reports.
func Read(b []byte) (*DLL, Arch, error) {
	im, r := ReadImage(b)
	if r.HasErrors() || im == nil {
		return nil, 0, r.Err()
	}
	return im.DLL, im.Arch, nil
}

func dllWhere() loss.Where { return loss.Where{Path: "DLL"} }

// ReadImage parses a layout DLL: it finds export ordinal 1, decodes the descriptor function, and walks KBDTABLES
// and the tables it points to (dll-format §10). Every pointer is bounds-checked and every walk is bounded. The
// report holds errors (the file cannot be read: the result is nil) and warnings about parts that were skipped
// (a malformed resource, an NLS table that cannot be read).
func ReadImage(b []byte) (*Image, *loss.Report) {
	r := &loss.Report{}
	fail := func(format string, args ...any) (*Image, *loss.Report) {
		r.Errorf("dll.malformed", dllWhere(), format, args...)
		return nil, r
	}
	if len(b) > MaxInput {
		return fail("the file has %d bytes; the reader looks at most at %d (Windows loads at most %d)", len(b), MaxInput, MaxFileSize)
	}
	f, err := pe.Parse(b)
	if err != nil {
		return fail("%v", err)
	}
	switch f.Machine {
	case pe.MachineAMD64, pe.MachineI386:
	case pe.MachineARM64:
	default:
		r.Errorf("dll.architectureUnsupported", dllWhere(), "machine %#04x is not a layout architecture the reader knows", uint16(f.Machine))
		return nil, r
	}
	if (f.Machine != pe.MachineI386) != f.PE32Plus {
		return fail("machine %#04x with a %s optional header", uint16(f.Machine), map[bool]string{true: "PE32+", false: "PE32"}[f.PE32Plus])
	}
	dllName, exps, err := f.Exports()
	if err != nil {
		return fail("export directory: %v", err)
	}
	im := &Image{}
	var fn, nlsFn uint32
	for _, e := range exps {
		im.Exports = append(im.Exports, e.Ordinal)
		switch e.Ordinal {
		case 1:
			fn = e.RVA
		case 2:
			nlsFn = e.RVA
		}
	}
	sort.Slice(im.Exports, func(i, j int) bool { return im.Exports[i] < im.Exports[j] })
	if fn == 0 {
		return fail("no export ordinal 1 (KbdLayerDescriptor)")
	}
	tablesRVA, wide, err := descriptor(f, fn)
	if err != nil {
		return fail("export ordinal 1: %v", err)
	}
	rd := &tableReader{f: f, p: 8, budget: 16*len(b) + 1<<20}
	switch {
	case f.Machine == pe.MachineAMD64:
		im.Arch = AMD64
	case f.Machine == pe.MachineARM64:
		// The file on disk is the native ARM64 view (docs/arm64x.md §2.1); its tables are the ones imported.
		im.Arch = ARM64
		if lc, err := f.LoadConfig(); err == nil && lc != nil && lc.HasCHPEMetadata {
			im.Arch = ARM64X
		}
	default:
		im.Arch = rd.i386Width(tablesRVA, wide)
		rd.p = im.Arch.PointerSize()
	}
	t, short, err := rd.tables(tablesRVA)
	if err != nil {
		return fail("%v", err)
	}
	im.ShortForm = short
	im.LigatureWidthRead = t.LigatureWidth
	if short {
		t.Type, t.SubType = 0, 0
	} else if t.Type > 0xFF || t.SubType > 0xFFFF {
		// The 8 bytes after a short structure hold whatever came next (OBS D9): implausible values are not kept.
		im.KeyboardTypeUntrusted = true
		t.Type, t.SubType = 0, 0
	}
	if len(t.Ligatures) > 0 {
		w := 0
		for _, l := range t.Ligatures {
			w = max(w, len(l.Chars))
		}
		t.LigatureWidth = w
	}
	im.DLL = &DLL{FileName: dllName, Tables: *t}
	if nlsFn != 0 {
		if nlsRVA, _, err := descriptor(f, nlsFn); err != nil {
			r.Warnf("dll.nlsUnreadable", dllWhere(), "export ordinal 2 (KbdNlsLayerDescriptor): %v", err)
		} else if n, err := rd.nls(nlsRVA); err != nil {
			r.Warnf("dll.nlsUnreadable", dllWhere(), "KBDNLSTABLES: %v", err)
		} else {
			im.NLS = n
		}
	}
	im.Container = container(f, exps)
	res, err := f.Resources()
	if err != nil {
		r.Warnf("dll.resourceDropped", dllWhere(), "resources skipped: %v", err)
	}
	for _, rs := range res {
		switch rs.Type {
		case pe.RTString:
			ss, err := decodeStrings(rs.Name, rs.Lang, rs.Data)
			if err != nil {
				r.Warnf("dll.resourceDropped", dllWhere(), "string block %d skipped: %v", rs.Name, err)
				continue
			}
			im.DLL.Strings = append(im.DLL.Strings, ss...)
		case pe.RTVersion:
			v, err := decodeVersion(rs.Data)
			if err != nil || im.DLL.Version != nil {
				im.OtherResources = append(im.OtherResources, fmt.Sprintf("%d/%d/%04x", rs.Type, rs.Name, rs.Lang))
				continue
			}
			im.DLL.Version = v
		default:
			im.OtherResources = append(im.OtherResources, fmt.Sprintf("%d/%d/%04x", rs.Type, rs.Name, rs.Lang))
		}
	}
	return im, r
}

// container lists what the file has beyond the shape the tool writes (dll-format §1): none of it is layout data.
func container(f *pe.File, exps []pe.ExportEntry) []string {
	var out []string
	if f.Machine == pe.MachineARM64 {
		if lc, err := f.LoadConfig(); err == nil && lc != nil && lc.HasCHPEMetadata {
			out = append(out, "the x64 view of the ARM64X file (its code and headers; the tables are the native view's)")
		}
	}
	if f.EntryPoint != 0 {
		out = append(out, "entry point")
	}
	if f.Dir(pe.DirImport).RVA != 0 {
		out = append(out, "imports")
	}
	if f.Dir(pe.DirDebug).RVA != 0 {
		out = append(out, "debug directory")
	}
	for _, s := range f.Sections {
		switch s.Name {
		case ".text", ".data", ".rsrc", ".reloc", ".hexpthk", ".pdata", ".00cfg", ".a64xrm":
		default:
			out = append(out, "section "+s.Name)
		}
	}
	for _, e := range exps {
		if e.Ordinal > 2 {
			out = append(out, fmt.Sprintf("export ordinal %d", e.Ordinal))
		}
	}
	return out
}

// descriptor decodes a function that returns a constant address (dll-format §1.4) and returns that address as an
// RVA. Besides the shapes Windows ships it accepts what other compilers emit for the same function: jumps to it
// (incremental-link thunks), a frame-pointer prologue and epilogue, and a 64-bit immediate. wide reports an i386
// function that sets EDX too (cdq or xor edx, edx: the WOW64 shape).
func descriptor(f *pe.File, rva uint32) (uint32, bool, error) {
	if f.Machine == pe.MachineARM64 {
		code, err := codeAt(f, rva)
		if err != nil {
			return 0, false, err
		}
		t, err := arm64Target(code, rva)
		if err != nil {
			return 0, false, fmt.Errorf("the descriptor function has a shape the reader does not know")
		}
		return t, false, nil
	}
	amd64 := f.Machine == pe.MachineAMD64
	for hop := 0; hop < 4; hop++ {
		code, err := codeAt(f, rva)
		if err != nil {
			return 0, false, err
		}
		switch {
		case code[0] == 0xE9: // jmp rel32
			rva = uint32(int64(rva) + 5 + int64(int32(binary.LittleEndian.Uint32(code[1:]))))
			continue
		case code[0] == 0xEB: // jmp rel8
			rva = uint32(int64(rva) + 2 + int64(int8(code[1])))
			continue
		}
		p := 0
		frame := false
		switch {
		case amd64 && code[0] == 0x55 && code[1] == 0x48 && code[2] == 0x89 && code[3] == 0xE5: // push rbp; mov rbp, rsp
			p, frame = 4, true
		case !amd64 && code[0] == 0x55 && (code[1] == 0x89 && code[2] == 0xE5 || code[1] == 0x8B && code[2] == 0xEC): // push ebp; mov ebp, esp
			p, frame = 3, true
		}
		var va uint64
		var target uint32
		wide := false
		switch {
		case amd64 && code[p] == 0x48 && code[p+1] == 0x8D && code[p+2] == 0x05: // lea rax, [rip+rel32]
			target = uint32(int64(rva) + int64(p) + 7 + int64(int32(binary.LittleEndian.Uint32(code[p+3:]))))
			p += 7
		case amd64 && code[p] == 0x48 && code[p+1] == 0xB8: // mov rax, imm64
			va = binary.LittleEndian.Uint64(code[p+2:])
			p += 10
		case !amd64 && code[p] == 0xB8: // mov eax, imm32
			va = uint64(binary.LittleEndian.Uint32(code[p+1:]))
			p += 5
			switch {
			case code[p] == 0x99: // cdq
				wide, p = true, p+1
			case (code[p] == 0x31 || code[p] == 0x33) && code[p+1] == 0xD2: // xor edx, edx
				wide, p = true, p+2
			}
		default:
			return 0, false, fmt.Errorf("the descriptor function has a shape the reader does not know")
		}
		if frame {
			if code[p] != 0x5D { // pop rbp
				return 0, false, fmt.Errorf("the descriptor function has a shape the reader does not know")
			}
			p++
		}
		if code[p] != 0xC3 {
			return 0, false, fmt.Errorf("the descriptor function has a shape the reader does not know")
		}
		if target == 0 {
			if va < f.ImageBase || va-f.ImageBase >= 1<<32 {
				return 0, false, fmt.Errorf("the descriptor returns %#x, outside the image", va)
			}
			target = uint32(va - f.ImageBase)
		}
		return target, wide, nil
	}
	return 0, false, fmt.Errorf("the descriptor function jumps more than three times")
}

// codeAt returns up to 24 bytes of code at rva, padded with zero bytes after the end of its section.
func codeAt(f *pe.File, rva uint32) ([]byte, error) {
	out := make([]byte, 24)
	s := f.SectionAt(rva)
	if s == nil {
		return nil, fmt.Errorf("the function at RVA %#x is in no section", rva)
	}
	d := rva - s.RVA
	if d >= s.RawSize {
		return nil, fmt.Errorf("the function at RVA %#x is beyond the raw data of %s", rva, s.Name)
	}
	n := min(uint32(len(out)), s.RawSize-d)
	b, err := f.Bytes(rva, n)
	if err != nil {
		return nil, err
	}
	copy(out, b)
	return out, nil
}

type tableReader struct {
	f      *pe.File
	p      int
	budget int // bytes the reader may still look at
	starts []uint32
}

// at returns base + i*size, failing instead of wrapping around.
func at(base uint32, i, size int) (uint32, error) {
	v := uint64(base) + uint64(i)*uint64(size)
	if v >= 1<<32 {
		return 0, fmt.Errorf("windows: address beyond 4 GB")
	}
	return uint32(v), nil
}

func (r *tableReader) bytes(rva uint32, n int) ([]byte, error) {
	r.budget -= n
	if r.budget < 0 {
		return nil, fmt.Errorf("windows: the tables need more reading than the file's size allows (a hostile file?)")
	}
	return r.f.Bytes(rva, uint32(n))
}

func (r *tableReader) elem(base uint32, i, size int) ([]byte, error) {
	a, err := at(base, i, size)
	if err != nil {
		return nil, err
	}
	return r.bytes(a, size)
}

// ptr reads a pointer slot and converts it to an RVA; 0 stays 0. Every non-zero target is remembered, so that a
// short KBDTABLES can be recognised.
func (r *tableReader) ptr(b []byte) (uint32, error) {
	var va uint64
	if r.p == 8 {
		va = binary.LittleEndian.Uint64(b)
	} else {
		va = uint64(binary.LittleEndian.Uint32(b))
	}
	if va == 0 {
		return 0, nil
	}
	if va < r.f.ImageBase || va-r.f.ImageBase > 0xFFFF_FFFF {
		return 0, fmt.Errorf("windows: pointer %#x outside the image", va)
	}
	rva := uint32(va - r.f.ImageBase)
	r.starts = append(r.starts, rva)
	return rva, nil
}

// i386Width tells a WOW64 file (8-byte slots) from a native x86 file (4-byte pointers) by the KBDTABLES itself:
// with 8-byte slots the dword at +4 is the high half of pCharModifiers and is zero; with 4-byte pointers it is
// pVkToWcharTable (an address) or zero. The descriptor shape (wide) decides when the tables leave it open.
func (r *tableReader) i386Width(rva uint32, wide bool) Arch {
	b, err := r.f.Bytes(rva, 0x10)
	if err != nil {
		if wide {
			return WOW64
		}
		return X86
	}
	le := binary.LittleEndian
	if le.Uint32(b[4:]) != 0 || le.Uint32(b[0xC:]) != 0 {
		return X86 // a high half of an 8-byte slot is never set
	}
	if wide {
		return WOW64
	}
	// mov eax, imm32; ret with both dwords zero: the shape of the function says x86.
	return X86
}

func (r *tableReader) tables(rva uint32) (*Tables, bool, error) {
	p := r.p
	shortSize, fullSize := 0x34, 0x3C
	if p == 8 {
		shortSize, fullSize = 0x60, 0x68
	}
	kt, err := r.bytes(rva, shortSize)
	if err != nil {
		return nil, false, fmt.Errorf("windows: KBDTABLES: %w", err)
	}
	full, ferr := r.f.Bytes(rva, uint32(fullSize))
	le := binary.LittleEndian
	field := func(i int) (uint32, error) { return r.ptr(kt[i*p:]) }
	t := &Tables{}
	ptrs := make([]uint32, 7)
	for i := range ptrs {
		if ptrs[i], err = field(i); err != nil {
			return nil, false, err
		}
	}
	bMax := int(kt[7*p])
	e0, err := field(8)
	if err != nil {
		return nil, false, err
	}
	e1, err := field(9)
	if err != nil {
		return nil, false, err
	}
	flags := le.Uint32(kt[10*p:])
	t.LocaleFlags, t.Version = uint16(flags), uint16(flags>>16)
	nLg := int(kt[10*p+4])
	cbLg := int(kt[10*p+5])
	// pLigature follows fLocaleFlags, nLgMax, cbLgEntry and two padding bytes; dwType and dwSubType follow it.
	ligOff := 10*p + 8
	pLig, err := r.ptr(kt[ligOff:])
	if err != nil {
		return nil, false, err
	}

	if ptrs[0] == 0 {
		return nil, false, fmt.Errorf("windows: pCharModifiers is 0")
	}
	if t.Modifiers, err = r.modifiers(ptrs[0]); err != nil {
		return nil, false, err
	}
	if ptrs[1] != 0 {
		if t.CharTables, err = r.charTables(ptrs[1]); err != nil {
			return nil, false, err
		}
	}
	if ptrs[2] != 0 {
		if t.DeadKeys, err = r.deadKeys(ptrs[2]); err != nil {
			return nil, false, err
		}
	}
	if ptrs[3] != 0 {
		if t.KeyNames, err = r.keyNames(ptrs[3]); err != nil {
			return nil, false, err
		}
	}
	if ptrs[4] != 0 {
		if t.KeyNamesExt, err = r.keyNames(ptrs[4]); err != nil {
			return nil, false, err
		}
	}
	if ptrs[5] != 0 {
		if t.KeyNamesDead, err = r.deadNames(ptrs[5]); err != nil {
			return nil, false, err
		}
	}
	if ptrs[6] == 0 {
		return nil, false, fmt.Errorf("windows: pusVSCtoVK is 0")
	}
	vsc, err := r.bytes(ptrs[6], 2*bMax)
	if err != nil {
		return nil, false, err
	}
	t.ScanToVK = make([]uint16, bMax)
	for i := range t.ScanToVK {
		t.ScanToVK[i] = le.Uint16(vsc[2*i:])
	}
	if e0 != 0 {
		if t.E0, err = r.vscVK(e0); err != nil {
			return nil, false, err
		}
	}
	if e1 != 0 {
		if t.E1, err = r.vscVK(e1); err != nil {
			return nil, false, err
		}
	}
	if pLig != 0 && nLg != 0 {
		// cbLgEntry 0 makes Windows hang (dll-format §4.7); any other mismatch is not something to guess around.
		if cbLg != 4+2*nLg {
			return nil, false, fmt.Errorf("windows: ligature entry size %d does not match width %d", cbLg, nLg)
		}
		t.LigatureWidth = nLg
		if t.Ligatures, err = r.ligatures(pLig, nLg); err != nil {
			return nil, false, err
		}
		if len(t.Ligatures) == 0 {
			t.LigatureWidth = 0
		}
	}
	// A short KBDTABLES (dll-format §3) is recognised when something the tables point to starts inside the 8
	// bytes of the full form, or the file ends there; dwType and dwSubType are then not read.
	short := ferr != nil
	for _, s := range r.starts {
		if s >= rva+uint32(shortSize) && s < rva+uint32(fullSize) {
			short = true
		}
	}
	if !short {
		t.Type = le.Uint32(full[ligOff+p:])
		t.SubType = le.Uint32(full[ligOff+p+4:])
	}
	return t, short, nil
}

func (r *tableReader) modifiers(rva uint32) (Modifiers, error) {
	var m Modifiers
	h, err := r.bytes(rva, r.p+2)
	if err != nil {
		return m, err
	}
	vtb, err := r.ptr(h)
	if err != nil {
		return m, err
	}
	maxBits := int(binary.LittleEndian.Uint16(h[r.p:]))
	if maxBits > 0xFF {
		return m, fmt.Errorf("windows: wMaxModBits %#x: modifier bits are one byte", maxBits)
	}
	mn, err := r.bytes(rva+uint32(r.p)+2, maxBits+1)
	if err != nil {
		return m, err
	}
	m.ModNumber = append([]byte(nil), mn...)
	if vtb == 0 {
		return m, nil
	}
	for i := 0; i < 256; i++ {
		e, err := r.elem(vtb, i, 2)
		if err != nil {
			return m, err
		}
		if e[0] == 0 {
			return m, nil
		}
		m.VKToBit = append(m.VKToBit, VKBit{VK: e[0], Bits: e[1]})
	}
	return m, fmt.Errorf("windows: VK_TO_BIT array not terminated")
}

func (r *tableReader) charTables(rva uint32) ([]CharTable, error) {
	es := 2 * r.p
	var out []CharTable
	for i := 0; i < maxTables; i++ {
		e, err := r.elem(rva, i, es)
		if err != nil {
			return nil, err
		}
		rows, err := r.ptr(e)
		if err != nil {
			return nil, err
		}
		if rows == 0 {
			return out, nil
		}
		n, cb := int(e[r.p]), int(e[r.p+1])
		if cb != 2+2*n || n == 0 {
			return nil, fmt.Errorf("windows: table %d has cbSize %d for width %d (must be 2 + 2n, never 0)", i, cb, n)
		}
		ct := CharTable{Width: n}
		for k := 0; ; k++ {
			if k >= maxRows {
				return nil, fmt.Errorf("windows: table %d not terminated", i)
			}
			row, err := r.elem(rows, k, cb)
			if err != nil {
				return nil, err
			}
			if row[0] == 0 {
				break
			}
			cr := CharRow{VK: row[0], Attributes: row[1], Chars: make([]uint16, n)}
			for c := 0; c < n; c++ {
				cr.Chars[c] = binary.LittleEndian.Uint16(row[2+2*c:])
			}
			ct.Rows = append(ct.Rows, cr)
		}
		out = append(out, ct)
	}
	return nil, fmt.Errorf("windows: VK_TO_WCHAR_TABLE array not terminated within %d entries", maxTables)
}

func (r *tableReader) deadKeys(rva uint32) ([]DeadKey, error) {
	var out []DeadKey
	for i := 0; i < maxEntries; i++ {
		e, err := r.elem(rva, i, 8)
		if err != nil {
			return nil, err
		}
		both := binary.LittleEndian.Uint32(e)
		if both == 0 {
			return out, nil
		}
		out = append(out, DeadKey{Dead: uint16(both >> 16), Base: uint16(both),
			Composed: binary.LittleEndian.Uint16(e[4:]), Flags: binary.LittleEndian.Uint16(e[6:])})
	}
	return nil, fmt.Errorf("windows: DEADKEY table not terminated")
}

func (r *tableReader) wstr(rva uint32) ([]uint16, error) {
	var u []uint16
	for i := 0; i < maxString; i++ {
		b, err := r.elem(rva, i, 2)
		if err != nil {
			return nil, err
		}
		c := binary.LittleEndian.Uint16(b)
		if c == 0 {
			return u, nil
		}
		u = append(u, c)
	}
	return nil, fmt.Errorf("windows: string not terminated within %d units", maxString)
}

func (r *tableReader) keyNames(rva uint32) ([]KeyName, error) {
	es := 2 * r.p
	var out []KeyName
	for i := 0; i < maxScanTable; i++ {
		e, err := r.elem(rva, i, es)
		if err != nil {
			return nil, err
		}
		s, err := r.ptr(e[r.p:])
		if err != nil {
			return nil, err
		}
		if e[0] == 0 && s == 0 {
			return out, nil
		}
		if s == 0 {
			return nil, fmt.Errorf("windows: key name of scan code %02x has no string", e[0])
		}
		u, err := r.wstr(s)
		if err != nil {
			return nil, err
		}
		out = append(out, KeyName{Scan: e[0], Name: string(utf16.Decode(u))})
	}
	return nil, fmt.Errorf("windows: key-name table not terminated")
}

func (r *tableReader) deadNames(rva uint32) ([]DeadName, error) {
	var out []DeadName
	for i := 0; i < maxEntries; i++ {
		e, err := r.elem(rva, i, r.p)
		if err != nil {
			return nil, err
		}
		s, err := r.ptr(e)
		if err != nil {
			return nil, err
		}
		if s == 0 {
			return out, nil
		}
		u, err := r.wstr(s)
		if err != nil {
			return nil, err
		}
		if len(u) == 0 {
			return nil, fmt.Errorf("windows: empty dead-key name")
		}
		out = append(out, DeadName{Dead: u[0], Name: string(utf16.Decode(u[1:]))})
	}
	return nil, fmt.Errorf("windows: dead-key name table not terminated")
}

func (r *tableReader) vscVK(rva uint32) ([]ScanVK, error) {
	out := []ScanVK{}
	for i := 0; i < maxScanTable; i++ {
		e, err := r.elem(rva, i, 4)
		if err != nil {
			return nil, err
		}
		if e[0] == 0 {
			if len(out) == 0 {
				return nil, nil
			}
			return out, nil
		}
		out = append(out, ScanVK{Scan: e[0], VK: binary.LittleEndian.Uint16(e[2:])})
	}
	return nil, fmt.Errorf("windows: VSC_VK table not terminated")
}

func (r *tableReader) ligatures(rva uint32, n int) ([]Ligature, error) {
	cb := 4 + 2*n
	var out []Ligature
	for i := 0; i < maxEntries; i++ {
		e, err := r.elem(rva, i, cb)
		if err != nil {
			return nil, err
		}
		if e[0] == 0 {
			return out, nil
		}
		l := Ligature{VK: e[0], Column: binary.LittleEndian.Uint16(e[2:])}
		for c := 0; c < n; c++ {
			u := binary.LittleEndian.Uint16(e[4+2*c:])
			if u == WCH_NONE {
				break
			}
			l.Chars = append(l.Chars, u)
		}
		out = append(out, l)
	}
	return nil, fmt.Errorf("windows: ligature table not terminated")
}

// nls reads KBDNLSTABLES (dll-format §6.1). The counts are taken as stated (Windows reads that many entries) but
// are bounded.
func (r *tableReader) nls(rva uint32) (*NLSTables, error) {
	p := r.p
	size := 0x14
	offF, offNM, offM := 8, 0x0C, 0x10
	if p == 8 {
		size, offNM, offM = 0x20, 0x10, 0x18
	}
	b, err := r.bytes(rva, size)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	n := &NLSTables{OEMIdentifier: le.Uint16(b), LayoutInformation: le.Uint16(b[2:])}
	nf := le.Uint32(b[4:])
	pf, err := r.ptr(b[offF:])
	if err != nil {
		return nil, err
	}
	nm := int32(le.Uint32(b[offNM:]))
	pm, err := r.ptr(b[offM:])
	if err != nil {
		return nil, err
	}
	if nf > maxNLS || nm < 0 || nm > maxNLS {
		return nil, fmt.Errorf("windows: NLS counts %d and %d", nf, nm)
	}
	if pf == 0 {
		nf = 0
	}
	for i := 0; i < int(nf); i++ {
		e, err := r.elem(pf, i, 0x84)
		if err != nil {
			return nil, err
		}
		v := VKF{VK: e[0], ProcType: e[1], ProcCurrent: e[2], ProcSwitch: e[3]}
		for k := 0; k < 8; k++ {
			v.Proc[k] = VKFParam{Index: e[4+8*k], Param: le.Uint32(e[8+8*k:])}
			v.ProcAlt[k] = VKFParam{Index: e[0x44+8*k], Param: le.Uint32(e[0x48+8*k:])}
		}
		n.VkToF = append(n.VkToF, v)
	}
	if pm != 0 {
		for i := 0; i < int(nm); i++ {
			e, err := r.elem(pm, i, 2)
			if err != nil {
				return nil, err
			}
			n.MouseVKeys = append(n.MouseVKeys, le.Uint16(e))
		}
	}
	return n, nil
}
