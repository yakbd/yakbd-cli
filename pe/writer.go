package pe

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// FixupKind says how a fixup is written into its slot.
type FixupKind int

const (
	// Abs32 writes the 32-bit virtual address of the target (ImageBase + RVA) and a HIGHLOW base relocation.
	Abs32 FixupKind = iota
	// Abs64 writes the 64-bit virtual address of the target and a DIR64 base relocation.
	Abs64
	// RVA32 writes the 32-bit relative virtual address of the target; no relocation.
	RVA32
	// Rel32 writes target RVA − (slot RVA + 4): an x64 RIP-relative displacement whose slot ends the instruction.
	Rel32
	// Arm64Page21 fills the immediate of an ARM64 ADRP instruction in the slot: the 4 KiB page of the target
	// minus the page of the instruction (Arm A64 encoding, immlo bits 29-30, immhi bits 5-23). PC-relative: no
	// relocation.
	Arm64Page21
	// Arm64Lo12 fills the 12-bit immediate (bits 10-21) of an ARM64 ADD (immediate) with the low 12 bits of the
	// target RVA.
	Arm64Lo12
	// Arm64Lo12Ldr64 fills the scaled 12-bit offset of an ARM64 64-bit LDR (unsigned offset) with the low 12 bits
	// of the target RVA divided by 8; the target must be 8-byte aligned.
	Arm64Lo12Ldr64
)

// Ref names a place in the image: a section by name and a byte offset into its data.
type Ref struct {
	Section string
	Offset  uint32
}

// Fixup is a slot in a section's data whose value depends on where the image places a target.
type Fixup struct {
	Offset uint32 // offset of the slot in the section's data
	Kind   FixupKind
	Target Ref
}

// Section is a section of raw bytes.
type Section struct {
	Name            string
	Characteristics uint32
	Data            []byte
	Fixups          []Fixup
}

// Export is one exported function.
type Export struct {
	Name    string // empty for an export by ordinal only
	Ordinal uint16
	Target  Ref
}

// Resource is one leaf of the resource tree: type, name and language ids with the data.
type Resource struct {
	Type, Name, Lang uint16
	Data             []byte
}

// Image describes the image to write.
type Image struct {
	Machine            Machine
	PE32Plus           bool
	ImageBase          uint64
	SectionAlignment   uint32
	FileAlignment      uint32
	Characteristics    uint16
	DllCharacteristics uint16
	Subsystem          uint16
	LinkerVersion      [2]uint8
	OSVersion          [2]uint16
	ImageVersion       [2]uint16
	SubsystemVersion   [2]uint16
	TimeDateStamp      uint32

	Sections []*Section // in file order; .rsrc and .reloc are added by the writer

	DLLName       string   // name stored in the export directory
	Exports       []Export // ordinals must be distinct
	ExportSection string   // section that receives the export directory

	Resources []Resource // written into a section named .rsrc, characteristics ResourceChars
	// ResourceChars and RelocChars are the characteristics of the generated sections.
	ResourceChars uint32
	RelocChars    uint32

	// Directories are further data directory entries (load configuration, exception table, ...).
	Directories []Directory
	// DynamicRelocs are ARM64X entries of a dynamic value relocation table (symbol 6). When set, the writer appends
	// the table to .reloc, after the base relocations, and writes its offset and section number into every load
	// configuration in LoadConfigs.
	DynamicRelocs []DynamicValue
	// LoadConfigs are 64-bit load configuration structures whose DynamicValueRelocTableOffset (+0xE0) and
	// DynamicValueRelocTableSection (+0xE4) the writer fills.
	LoadConfigs []Ref
}

// Directory is one data directory entry: index, start and size.
type Directory struct {
	Index  int
	Target Ref
	Size   uint32
}

// DynamicValue is one ARM64X "set a value" entry: in the alternative (x64) view, the Size bytes at At hold Value,
// or the RVA of ValueRVA when it is set. At with an empty section name is an offset in the headers (HeaderRVA).
type DynamicValue struct {
	At       Ref
	Size     int // 2, 4 or 8
	Value    uint64
	ValueRVA *Ref
}

// Places in the headers the writer produces (they are RVAs as well as file offsets).
const (
	// MachineField is the Machine field of the COFF header.
	MachineField = dosHeaderSize + 4
)

// DirectoryField returns where data directory entry i (its RVA; the size follows at +4) lies in the headers of a
// PE32+ or PE32 image the writer produces.
func DirectoryField(i int, plus bool) uint32 {
	opt := uint32(dosHeaderSize + 4 + coffSize)
	if plus {
		return opt + 112 + uint32(8*i)
	}
	return opt + 96 + uint32(8*i)
}

// Dynamic value relocation table constants (LLVM BinaryFormat/COFF.h; docs/arm64x.md §2.2).
const (
	DynamicRelocARM64X = 6
	ARM64XZeroFill     = 0
	ARM64XValue        = 1
	ARM64XDelta        = 2
)

// AppendExportDirectory appends an export directory to s (for a second view of an ARM64X image) and returns its
// offset and size.
func AppendExportDirectory(s *Section, dllName string, exports []Export) (off, size uint32, err error) {
	if len(exports) == 0 {
		return 0, 0, fmt.Errorf("pe: no exports")
	}
	return appendExports(s, dllName, exports)
}

// Layout is where the writer placed each section; returned for tests and diagnostics.
type Layout struct {
	Sections []PlacedSection
}

// PlacedSection is one section as laid out.
type PlacedSection struct {
	Name                      string
	RVA, VirtualSize          uint32
	PointerToRawData, RawSize uint32
}

const (
	dosHeaderSize = 0x40
	coffSize      = 20
	sectionHdrLen = 40
)

func align(v, a uint32) uint32 {
	if a == 0 {
		return v
	}
	return (v + a - 1) / a * a
}

// Write lays out and serialises the image.
func (im *Image) Write() ([]byte, *Layout, error) {
	if im.FileAlignment == 0 || im.SectionAlignment == 0 {
		return nil, nil, fmt.Errorf("pe: alignments must be set")
	}
	secs := make([]*Section, 0, len(im.Sections)+2)
	byName := map[string]*Section{}
	for _, s := range im.Sections {
		if len(s.Name) > 8 {
			return nil, nil, fmt.Errorf("pe: section name %q longer than 8 bytes", s.Name)
		}
		if byName[s.Name] != nil {
			return nil, nil, fmt.Errorf("pe: section %q twice", s.Name)
		}
		c := &Section{Name: s.Name, Characteristics: s.Characteristics, Data: append([]byte(nil), s.Data...),
			Fixups: append([]Fixup(nil), s.Fixups...)}
		secs = append(secs, c)
		byName[s.Name] = c
	}

	var exportRef *Ref
	var exportSize uint32
	if len(im.Exports) > 0 {
		es := byName[im.ExportSection]
		if es == nil {
			return nil, nil, fmt.Errorf("pe: export section %q not found", im.ExportSection)
		}
		off, size, err := appendExports(es, im.DLLName, im.Exports)
		if err != nil {
			return nil, nil, err
		}
		exportRef = &Ref{Section: es.Name, Offset: off}
		exportSize = size
	}
	if len(im.Resources) > 0 {
		if byName[".rsrc"] != nil {
			return nil, nil, fmt.Errorf("pe: section .rsrc given and resources set")
		}
		rs := buildResources(im.Resources)
		rs.Characteristics = im.ResourceChars
		secs = append(secs, rs)
		byName[".rsrc"] = rs
	}

	optSize := uint32(224)
	if im.PE32Plus {
		optSize = 240
	}
	nsec := uint32(len(secs) + 1) // + .reloc
	headers := align(dosHeaderSize+4+coffSize+optSize+nsec*sectionHdrLen, im.FileAlignment)

	// Place sections.
	rvaOf := map[string]uint32{}
	type placed struct {
		s                *Section
		rva, raw, rawLen uint32
	}
	var pl []placed
	rva := align(headers, im.SectionAlignment)
	raw := headers
	for _, s := range secs {
		p := placed{s: s, rva: rva, raw: raw, rawLen: align(uint32(len(s.Data)), im.FileAlignment)}
		if len(s.Data) == 0 {
			p.raw = 0
		}
		pl = append(pl, p)
		rvaOf[s.Name] = rva
		rva = align(rva+uint32(max(len(s.Data), 1)), im.SectionAlignment)
		raw += p.rawLen
	}

	// Resolve fixups and collect relocations.
	type rel struct {
		rva uint32
		typ uint16
	}
	var rels []rel
	for _, p := range pl {
		for _, f := range p.s.Fixups {
			trva, ok := rvaOf[f.Target.Section]
			if !ok {
				return nil, nil, fmt.Errorf("pe: fixup in %s at %#x: unknown section %q", p.s.Name, f.Offset, f.Target.Section)
			}
			tgt := uint64(trva) + uint64(f.Target.Offset)
			slot := p.rva + f.Offset
			d := p.s.Data
			need := uint32(4)
			if f.Kind == Abs64 {
				need = 8
			}
			if f.Offset+need > uint32(len(d)) {
				return nil, nil, fmt.Errorf("pe: fixup in %s at %#x beyond the data", p.s.Name, f.Offset)
			}
			switch f.Kind {
			case Abs32:
				va := im.ImageBase + tgt
				if va>>32 != 0 {
					return nil, nil, fmt.Errorf("pe: 32-bit fixup to %#x does not fit", va)
				}
				binary.LittleEndian.PutUint32(d[f.Offset:], uint32(va))
				rels = append(rels, rel{slot, RelHighLow})
			case Abs64:
				binary.LittleEndian.PutUint64(d[f.Offset:], im.ImageBase+tgt)
				rels = append(rels, rel{slot, RelDir64})
			case RVA32:
				binary.LittleEndian.PutUint32(d[f.Offset:], uint32(tgt))
			case Rel32:
				binary.LittleEndian.PutUint32(d[f.Offset:], uint32(int32(int64(tgt)-int64(slot+4))))
			case Arm64Page21:
				delta := int64(tgt>>12) - int64(slot>>12)
				if delta < -(1<<20) || delta >= 1<<20 {
					return nil, nil, fmt.Errorf("pe: ADRP in %s at %#x out of range", p.s.Name, f.Offset)
				}
				ins := binary.LittleEndian.Uint32(d[f.Offset:]) &^ (3<<29 | 0x7FFFF<<5)
				u := uint32(delta) & 0x1FFFFF
				ins |= (u&3)<<29 | (u>>2)<<5
				binary.LittleEndian.PutUint32(d[f.Offset:], ins)
			case Arm64Lo12, Arm64Lo12Ldr64:
				lo := uint32(tgt) & 0xFFF
				if f.Kind == Arm64Lo12Ldr64 {
					if lo%8 != 0 {
						return nil, nil, fmt.Errorf("pe: LDR target in %s at %#x not 8-byte aligned", p.s.Name, f.Offset)
					}
					lo /= 8
				}
				ins := binary.LittleEndian.Uint32(d[f.Offset:]) &^ (0xFFF << 10)
				binary.LittleEndian.PutUint32(d[f.Offset:], ins|lo<<10)
			default:
				return nil, nil, fmt.Errorf("pe: unknown fixup kind %d", f.Kind)
			}
		}
	}

	// Build .reloc: blocks per 4 KiB page, entries sorted, each block padded to a multiple of 4 bytes.
	sort.Slice(rels, func(i, j int) bool { return rels[i].rva < rels[j].rva })
	var rb bytes.Buffer
	for i := 0; i < len(rels); {
		page := rels[i].rva &^ 0xFFF
		j := i
		for j < len(rels) && rels[j].rva&^0xFFF == page {
			j++
		}
		n := j - i
		pad := n % 2
		size := uint32(8 + 2*(n+pad))
		binary.Write(&rb, binary.LittleEndian, page)
		binary.Write(&rb, binary.LittleEndian, size)
		for k := i; k < j; k++ {
			binary.Write(&rb, binary.LittleEndian, rels[k].typ<<12|uint16(rels[k].rva&0xFFF))
		}
		if pad == 1 {
			binary.Write(&rb, binary.LittleEndian, uint16(RelAbsolute))
		}
		i = j
	}
	relocSec := &Section{Name: ".reloc", Characteristics: im.RelocChars, Data: rb.Bytes()}
	if len(relocSec.Data) == 0 {
		// An empty block keeps the directory present; a reader sees zero entries.
		relocSec.Data = []byte{0, 0, 0, 0, 8, 0, 0, 0}
	}
	baseRelocSize := uint32(len(relocSec.Data))
	if len(im.DynamicRelocs) > 0 {
		for len(relocSec.Data)%4 != 0 {
			relocSec.Data = append(relocSec.Data, 0)
		}
		dvrtOff := uint32(len(relocSec.Data))
		table, err := dynamicRelocTable(im.DynamicRelocs, rvaOf)
		if err != nil {
			return nil, nil, err
		}
		relocSec.Data = append(relocSec.Data, table...)
		index := uint16(len(pl) + 1) // .reloc is the last section; numbers start at 1
		for _, lc := range im.LoadConfigs {
			s := byName[lc.Section]
			if s == nil || uint64(lc.Offset)+0xE6 > uint64(len(s.Data)) {
				return nil, nil, fmt.Errorf("pe: load configuration at %s+%#x outside its section", lc.Section, lc.Offset)
			}
			binary.LittleEndian.PutUint32(s.Data[lc.Offset+0xE0:], dvrtOff)
			binary.LittleEndian.PutUint16(s.Data[lc.Offset+0xE4:], index)
		}
	}
	pr := placed{s: relocSec, rva: rva, raw: raw, rawLen: align(uint32(len(relocSec.Data)), im.FileAlignment)}
	pl = append(pl, pr)
	rvaOf[".reloc"] = rva
	sizeOfImage := align(rva+uint32(len(relocSec.Data)), im.SectionAlignment)
	fileSize := raw + pr.rawLen

	// Serialise.
	out := make([]byte, fileSize)
	copy(out, "MZ")
	binary.LittleEndian.PutUint32(out[0x3C:], dosHeaderSize)
	p := uint32(dosHeaderSize)
	copy(out[p:], "PE\x00\x00")
	p += 4
	le := binary.LittleEndian
	le.PutUint16(out[p:], uint16(im.Machine))
	le.PutUint16(out[p+2:], uint16(len(pl)))
	le.PutUint32(out[p+4:], im.TimeDateStamp)
	le.PutUint16(out[p+16:], uint16(optSize))
	le.PutUint16(out[p+18:], im.Characteristics)
	p += coffSize
	opt := p
	var sizeCode, sizeInit, baseCode, baseData uint32
	for _, q := range pl {
		if q.s.Characteristics&SecCode != 0 {
			sizeCode += q.rawLen
			if baseCode == 0 {
				baseCode = q.rva
			}
		}
		if q.s.Characteristics&SecInitData != 0 {
			sizeInit += q.rawLen
			if baseData == 0 {
				baseData = q.rva
			}
		}
	}
	if im.PE32Plus {
		le.PutUint16(out[opt:], 0x20B)
	} else {
		le.PutUint16(out[opt:], 0x10B)
	}
	out[opt+2], out[opt+3] = im.LinkerVersion[0], im.LinkerVersion[1]
	le.PutUint32(out[opt+4:], sizeCode)
	le.PutUint32(out[opt+8:], sizeInit)
	le.PutUint32(out[opt+16:], 0) // AddressOfEntryPoint
	le.PutUint32(out[opt+20:], baseCode)
	var w uint32 // offset of the Windows-specific fields
	if im.PE32Plus {
		le.PutUint64(out[opt+24:], im.ImageBase)
		w = opt + 32
	} else {
		le.PutUint32(out[opt+24:], baseData)
		le.PutUint32(out[opt+28:], uint32(im.ImageBase))
		w = opt + 32
	}
	le.PutUint32(out[w:], im.SectionAlignment)
	le.PutUint32(out[w+4:], im.FileAlignment)
	le.PutUint16(out[w+8:], im.OSVersion[0])
	le.PutUint16(out[w+10:], im.OSVersion[1])
	le.PutUint16(out[w+12:], im.ImageVersion[0])
	le.PutUint16(out[w+14:], im.ImageVersion[1])
	le.PutUint16(out[w+16:], im.SubsystemVersion[0])
	le.PutUint16(out[w+18:], im.SubsystemVersion[1])
	le.PutUint32(out[w+24:], sizeOfImage)
	le.PutUint32(out[w+28:], headers)
	checksumAt := w + 32
	le.PutUint16(out[w+36:], im.Subsystem)
	le.PutUint16(out[w+38:], im.DllCharacteristics)
	var dirs uint32
	if im.PE32Plus {
		le.PutUint64(out[w+40:], 0x100000) // stack reserve
		le.PutUint64(out[w+48:], 0x1000)   // stack commit
		le.PutUint64(out[w+56:], 0x100000) // heap reserve
		le.PutUint64(out[w+64:], 0x1000)   // heap commit
		le.PutUint32(out[w+76:], NumDirs)
		dirs = w + 80
	} else {
		le.PutUint32(out[w+40:], 0x100000)
		le.PutUint32(out[w+44:], 0x1000)
		le.PutUint32(out[w+48:], 0x100000)
		le.PutUint32(out[w+52:], 0x1000)
		le.PutUint32(out[w+60:], NumDirs)
		dirs = w + 64
	}
	setDir := func(i int, rva, size uint32) {
		le.PutUint32(out[dirs+uint32(i)*8:], rva)
		le.PutUint32(out[dirs+uint32(i)*8+4:], size)
	}
	if exportRef != nil {
		setDir(DirExport, rvaOf[exportRef.Section]+exportRef.Offset, exportSize)
	}
	if rs := byName[".rsrc"]; rs != nil && len(im.Resources) > 0 {
		setDir(DirResource, rvaOf[".rsrc"], uint32(len(rs.Data)))
	}
	setDir(DirBaseReloc, rvaOf[".reloc"], baseRelocSize)
	for _, d := range im.Directories {
		trva, ok := rvaOf[d.Target.Section]
		if !ok || d.Index < 0 || d.Index >= NumDirs {
			return nil, nil, fmt.Errorf("pe: directory %d: unknown section %q", d.Index, d.Target.Section)
		}
		setDir(d.Index, trva+d.Target.Offset, d.Size)
	}

	sh := opt + optSize
	layout := &Layout{}
	for i, q := range pl {
		h := sh + uint32(i)*sectionHdrLen
		copy(out[h:h+8], q.s.Name)
		le.PutUint32(out[h+8:], uint32(len(q.s.Data)))
		le.PutUint32(out[h+12:], q.rva)
		le.PutUint32(out[h+16:], q.rawLen)
		le.PutUint32(out[h+20:], q.raw)
		le.PutUint32(out[h+36:], q.s.Characteristics)
		copy(out[q.raw:], q.s.Data)
		layout.Sections = append(layout.Sections, PlacedSection{Name: q.s.Name, RVA: q.rva,
			VirtualSize: uint32(len(q.s.Data)), PointerToRawData: q.raw, RawSize: q.rawLen})
	}
	le.PutUint32(out[checksumAt:], Checksum(out, checksumAt))
	return out, layout, nil
}

// appendExports appends an export directory to s and returns its offset and size.
func appendExports(s *Section, dllName string, exports []Export) (uint32, uint32, error) {
	exps := append([]Export(nil), exports...)
	sort.Slice(exps, func(i, j int) bool { return exps[i].Ordinal < exps[j].Ordinal })
	base := exps[0].Ordinal
	if base == 0 {
		return 0, 0, fmt.Errorf("pe: ordinal 0")
	}
	n := int(exps[len(exps)-1].Ordinal-base) + 1
	for i := 1; i < len(exps); i++ {
		if exps[i].Ordinal == exps[i-1].Ordinal {
			return 0, 0, fmt.Errorf("pe: ordinal %d twice", exps[i].Ordinal)
		}
	}
	var named []Export
	for _, e := range exps {
		if e.Name != "" {
			named = append(named, e)
		}
	}
	sort.Slice(named, func(i, j int) bool { return named[i].Name < named[j].Name })

	for len(s.Data)%4 != 0 {
		s.Data = append(s.Data, 0)
	}
	start := uint32(len(s.Data))
	dirLen := uint32(40)
	eat := start + dirLen
	npt := eat + uint32(4*n)
	ot := npt + uint32(4*len(named))
	strs := ot + uint32(2*len(named))
	strs = align(strs, 2)

	var str bytes.Buffer
	nameOff := strs
	str.WriteString(dllName)
	str.WriteByte(0)
	var nameOffs []uint32
	for _, e := range named {
		nameOffs = append(nameOffs, strs+uint32(str.Len()))
		str.WriteString(e.Name)
		str.WriteByte(0)
	}
	total := strs + uint32(str.Len()) - start
	buf := make([]byte, total)
	le := binary.LittleEndian
	le.PutUint32(buf[16:], uint32(base))
	le.PutUint32(buf[20:], uint32(n))
	le.PutUint32(buf[24:], uint32(len(named)))
	copy(buf[strs-start:], str.Bytes())
	s.Data = append(s.Data, buf...)
	fix := func(off uint32, t Ref) {
		s.Fixups = append(s.Fixups, Fixup{Offset: off, Kind: RVA32, Target: t})
	}
	self := func(off uint32) Ref { return Ref{Section: s.Name, Offset: off} }
	fix(start+12, self(nameOff))
	fix(start+28, self(eat))
	if len(named) > 0 {
		fix(start+32, self(npt))
		fix(start+36, self(ot))
	}
	for _, e := range exps {
		fix(eat+uint32(4*int(e.Ordinal-base)), e.Target)
	}
	for i, e := range named {
		fix(npt+uint32(4*i), self(nameOffs[i]))
		le.PutUint16(s.Data[ot+uint32(2*i):], e.Ordinal-base)
	}
	return start, total, nil
}

// buildResources builds the .rsrc section: three directory levels (type, name, language) of id entries,
// then the data entries, then the data, each blob aligned to 8 bytes.
func buildResources(res []Resource) *Section {
	rs := append([]Resource(nil), res...)
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Lang < b.Lang
	})
	type nameNode struct {
		id    uint16
		langs []int // indexes into rs
	}
	type typeNode struct {
		id    uint16
		names []*nameNode
	}
	var types []*typeNode
	for i, r := range rs {
		if len(types) == 0 || types[len(types)-1].id != r.Type {
			types = append(types, &typeNode{id: r.Type})
		}
		t := types[len(types)-1]
		if len(t.names) == 0 || t.names[len(t.names)-1].id != r.Name {
			t.names = append(t.names, &nameNode{id: r.Name})
		}
		nn := t.names[len(t.names)-1]
		nn.langs = append(nn.langs, i)
	}
	dirSize := func(entries int) uint32 { return 16 + 8*uint32(entries) }
	// Offsets: root, then each type directory, then each name directory (language level).
	off := dirSize(len(types))
	typeOff := make([]uint32, len(types))
	for i, t := range types {
		typeOff[i] = off
		off += dirSize(len(t.names))
	}
	nameOff := map[*nameNode]uint32{}
	for _, t := range types {
		for _, nn := range t.names {
			nameOff[nn] = off
			off += dirSize(len(nn.langs))
		}
	}
	dataEntryOff := off
	off += 16 * uint32(len(rs))
	dataOff := make([]uint32, len(rs))
	for i, r := range rs {
		off = align(off, 8)
		dataOff[i] = off
		off += uint32(len(r.Data))
	}
	buf := make([]byte, align(off, 8))
	le := binary.LittleEndian
	writeDir := func(at uint32, ids []uint16, targets []uint32, leaf bool) {
		le.PutUint16(buf[at+14:], uint16(len(ids)))
		for i, id := range ids {
			e := at + 16 + 8*uint32(i)
			le.PutUint32(buf[e:], uint32(id))
			v := targets[i]
			if !leaf {
				v |= 0x8000_0000
			}
			le.PutUint32(buf[e+4:], v)
		}
	}
	var ids []uint16
	var tg []uint32
	for i, t := range types {
		ids = append(ids, t.id)
		tg = append(tg, typeOff[i])
	}
	writeDir(0, ids, tg, false)
	sec := &Section{Name: ".rsrc"}
	idx := 0
	for i, t := range types {
		ids, tg = nil, nil
		for _, nn := range t.names {
			ids = append(ids, nn.id)
			tg = append(tg, nameOff[nn])
		}
		writeDir(typeOff[i], ids, tg, false)
		for _, nn := range t.names {
			ids, tg = nil, nil
			for _, li := range nn.langs {
				ids = append(ids, rs[li].Lang)
				de := dataEntryOff + 16*uint32(idx)
				tg = append(tg, de)
				le.PutUint32(buf[de+4:], uint32(len(rs[li].Data)))
				copy(buf[dataOff[li]:], rs[li].Data)
				sec.Fixups = append(sec.Fixups, Fixup{Offset: de, Kind: RVA32, Target: Ref{".rsrc", dataOff[li]}})
				idx++
			}
			writeDir(nameOff[nn], ids, tg, true)
		}
	}
	sec.Data = buf
	return sec
}

// Checksum computes the image checksum: the 16-bit one's-complement-style sum of the file taken as
// little-endian 16-bit words with the carry folded back, the four bytes of the CheckSum field counted as zero,
// plus the file length. The PE Format page names the field but not the algorithm; this is the widely documented
// folded 16-bit sum, cross-checked once during development against an independent PE library (CLEAN-ROOM.md).
func Checksum(file []byte, checksumOffset uint32) uint32 {
	var sum uint64
	n := len(file)
	for i := 0; i < n; i += 2 {
		if uint32(i) == checksumOffset || uint32(i) == checksumOffset+2 {
			continue
		}
		var w uint64
		if i+1 < n {
			w = uint64(file[i]) | uint64(file[i+1])<<8
		} else {
			w = uint64(file[i])
		}
		sum += w
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	sum = (sum & 0xFFFF) + (sum >> 16)
	return uint32(sum) + uint32(n)
}
