package pe

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// DataDir is one entry of the optional header's data directory.
type DataDir struct{ RVA, Size uint32 }

// SectionHeader is a parsed section header.
type SectionHeader struct {
	Name             string
	VirtualSize      uint32
	RVA              uint32
	RawSize          uint32
	PointerToRawData uint32
	Characteristics  uint32
}

// File is a parsed image.
type File struct {
	Machine            Machine
	PE32Plus           bool
	Characteristics    uint16
	TimeDateStamp      uint32
	LinkerVersion      [2]uint8
	ImageBase          uint64
	EntryPoint         uint32
	SectionAlignment   uint32
	FileAlignment      uint32
	OSVersion          [2]uint16
	ImageVersion       [2]uint16
	SubsystemVersion   [2]uint16
	SizeOfImage        uint32
	SizeOfHeaders      uint32
	CheckSum           uint32
	CheckSumOffset     uint32
	Subsystem          uint16
	DllCharacteristics uint16
	Sizes              [4]uint64 // stack reserve, stack commit, heap reserve, heap commit
	Dirs               []DataDir
	Sections           []SectionHeader

	data []byte
}

// ErrFormat is wrapped by every parse error.
var ErrFormat = errors.New("pe: malformed image")

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrFormat, fmt.Sprintf(format, args...))
}

// Parse parses the headers and section table of an image. It never panics on malformed input.
func Parse(b []byte) (*File, error) {
	le := binary.LittleEndian
	if len(b) < dosHeaderSize || b[0] != 'M' || b[1] != 'Z' {
		return nil, bad("no MZ header")
	}
	lfanew := le.Uint32(b[0x3C:])
	if uint64(lfanew)+4+coffSize > uint64(len(b)) || string(b[lfanew:lfanew+4]) != "PE\x00\x00" {
		return nil, bad("no PE signature")
	}
	f := &File{data: b}
	c := lfanew + 4
	f.Machine = Machine(le.Uint16(b[c:]))
	nsec := uint32(le.Uint16(b[c+2:]))
	f.TimeDateStamp = le.Uint32(b[c+4:])
	optSize := uint32(le.Uint16(b[c+16:]))
	f.Characteristics = le.Uint16(b[c+18:])
	opt := c + coffSize
	if uint64(opt)+uint64(optSize) > uint64(len(b)) || optSize < 2 {
		return nil, bad("optional header beyond the file")
	}
	o := b[opt : opt+optSize]
	magic := le.Uint16(o)
	var w uint32
	switch magic {
	case 0x10B:
		if optSize < 96 {
			return nil, bad("PE32 optional header too short")
		}
		f.ImageBase = uint64(le.Uint32(o[28:]))
		w = 32
	case 0x20B:
		if optSize < 112 {
			return nil, bad("PE32+ optional header too short")
		}
		f.PE32Plus = true
		f.ImageBase = le.Uint64(o[24:])
		w = 32
	default:
		return nil, bad("optional header magic %#x", magic)
	}
	f.LinkerVersion = [2]uint8{o[2], o[3]}
	f.EntryPoint = le.Uint32(o[16:])
	f.SectionAlignment = le.Uint32(o[w:])
	f.FileAlignment = le.Uint32(o[w+4:])
	f.OSVersion = [2]uint16{le.Uint16(o[w+8:]), le.Uint16(o[w+10:])}
	f.ImageVersion = [2]uint16{le.Uint16(o[w+12:]), le.Uint16(o[w+14:])}
	f.SubsystemVersion = [2]uint16{le.Uint16(o[w+16:]), le.Uint16(o[w+18:])}
	f.SizeOfImage = le.Uint32(o[w+24:])
	f.SizeOfHeaders = le.Uint32(o[w+28:])
	f.CheckSum = le.Uint32(o[w+32:])
	f.CheckSumOffset = opt + w + 32
	f.Subsystem = le.Uint16(o[w+36:])
	f.DllCharacteristics = le.Uint16(o[w+38:])
	var ndirs, dirs uint32
	if f.PE32Plus {
		for i := range f.Sizes {
			f.Sizes[i] = le.Uint64(o[w+40+uint32(8*i):])
		}
		ndirs, dirs = le.Uint32(o[w+76:]), w+80
	} else {
		for i := range f.Sizes {
			f.Sizes[i] = uint64(le.Uint32(o[w+40+uint32(4*i):]))
		}
		ndirs, dirs = le.Uint32(o[w+60:]), w+64
	}
	if ndirs > NumDirs || dirs+8*ndirs > optSize {
		return nil, bad("data directory count %d does not fit", ndirs)
	}
	for i := uint32(0); i < ndirs; i++ {
		f.Dirs = append(f.Dirs, DataDir{le.Uint32(o[dirs+8*i:]), le.Uint32(o[dirs+8*i+4:])})
	}
	sh := opt + optSize
	if uint64(sh)+uint64(nsec)*sectionHdrLen > uint64(len(b)) {
		return nil, bad("section table beyond the file")
	}
	for i := uint32(0); i < nsec; i++ {
		h := b[sh+i*sectionHdrLen:]
		name := h[:8]
		n := 0
		for n < 8 && name[n] != 0 {
			n++
		}
		s := SectionHeader{Name: string(name[:n]), VirtualSize: le.Uint32(h[8:]), RVA: le.Uint32(h[12:]),
			RawSize: le.Uint32(h[16:]), PointerToRawData: le.Uint32(h[20:]), Characteristics: le.Uint32(h[36:])}
		if s.RawSize > 0 && uint64(s.PointerToRawData)+uint64(s.RawSize) > uint64(len(b)) {
			return nil, bad("section %q raw data beyond the file", s.Name)
		}
		f.Sections = append(f.Sections, s)
	}
	return f, nil
}

// Dir returns data directory i, or a zero entry.
func (f *File) Dir(i int) DataDir {
	if i < len(f.Dirs) {
		return f.Dirs[i]
	}
	return DataDir{}
}

// Data returns the bytes of the whole file.
func (f *File) Data() []byte { return f.data }

// Section returns the section named name, or nil.
func (f *File) Section(name string) *SectionHeader {
	for i := range f.Sections {
		if f.Sections[i].Name == name {
			return &f.Sections[i]
		}
	}
	return nil
}

// SectionAt returns the section that contains rva (by virtual extent), or nil.
func (f *File) SectionAt(rva uint32) *SectionHeader {
	for i := range f.Sections {
		s := &f.Sections[i]
		size := max(s.VirtualSize, s.RawSize)
		if rva >= s.RVA && uint64(rva) < uint64(s.RVA)+uint64(size) {
			return s
		}
	}
	return nil
}

// Offset converts an RVA to a file offset. It fails for an RVA outside every section's raw data.
func (f *File) Offset(rva uint32) (uint32, error) {
	s := f.SectionAt(rva)
	if s == nil {
		return 0, bad("RVA %#x in no section", rva)
	}
	d := rva - s.RVA
	if d >= s.RawSize {
		return 0, bad("RVA %#x beyond the raw data of %s", rva, s.Name)
	}
	return s.PointerToRawData + d, nil
}

// Bytes returns n bytes at rva. They must lie in the raw data of one section.
func (f *File) Bytes(rva, n uint32) ([]byte, error) {
	s := f.SectionAt(rva)
	if s == nil {
		return nil, bad("RVA %#x in no section", rva)
	}
	d := rva - s.RVA
	if uint64(d)+uint64(n) > uint64(s.RawSize) {
		return nil, bad("%d bytes at RVA %#x run beyond the raw data of %s", n, rva, s.Name)
	}
	if n == 0 {
		return nil, nil // a section without raw data may name any PointerToRawData
	}
	off := s.PointerToRawData + d
	return f.data[off : off+n], nil
}

// MaxCString bounds the strings CString reads (export and DLL names), so that a hostile export table of 65536
// names cannot make the reader scan the section 65536 times.
const MaxCString = 1024

// CString reads a NUL-terminated byte string of at most MaxCString bytes at rva.
func (f *File) CString(rva uint32) (string, error) {
	s := f.SectionAt(rva)
	if s == nil {
		return "", bad("RVA %#x in no section", rva)
	}
	d := rva - s.RVA
	for end := d; end < s.RawSize && end-d <= MaxCString; end++ {
		if f.data[s.PointerToRawData+end] == 0 {
			return string(f.data[s.PointerToRawData+d : s.PointerToRawData+end]), nil
		}
	}
	return "", bad("unterminated string at RVA %#x", rva)
}

// ExportEntry is one exported function.
type ExportEntry struct {
	Name    string
	Ordinal uint16
	RVA     uint32
}

// Exports reads the export directory.
func (f *File) Exports() (string, []ExportEntry, error) {
	d := f.Dir(DirExport)
	if d.RVA == 0 {
		return "", nil, nil
	}
	b, err := f.Bytes(d.RVA, 40)
	if err != nil {
		return "", nil, err
	}
	le := binary.LittleEndian
	dllName, err := f.CString(le.Uint32(b[12:]))
	if err != nil {
		return "", nil, err
	}
	base := le.Uint32(b[16:])
	nfunc := le.Uint32(b[20:])
	nname := le.Uint32(b[24:])
	if nfunc > 0x10000 || nname > nfunc {
		return "", nil, bad("export counts %d/%d", nfunc, nname)
	}
	eat, err := f.Bytes(le.Uint32(b[28:]), 4*nfunc)
	if err != nil {
		return "", nil, err
	}
	names := map[uint32]string{}
	if nname > 0 {
		npt, err := f.Bytes(le.Uint32(b[32:]), 4*nname)
		if err != nil {
			return "", nil, err
		}
		ot, err := f.Bytes(le.Uint32(b[36:]), 2*nname)
		if err != nil {
			return "", nil, err
		}
		for i := uint32(0); i < nname; i++ {
			n, err := f.CString(le.Uint32(npt[4*i:]))
			if err != nil {
				return "", nil, err
			}
			idx := uint32(le.Uint16(ot[2*i:]))
			if idx >= nfunc {
				return "", nil, bad("export name %q has index %d", n, idx)
			}
			names[idx] = n
		}
	}
	var out []ExportEntry
	for i := uint32(0); i < nfunc; i++ {
		rva := le.Uint32(eat[4*i:])
		if rva == 0 {
			continue
		}
		out = append(out, ExportEntry{Name: names[i], Ordinal: uint16(base + i), RVA: rva})
	}
	return dllName, out, nil
}

// Reloc is one base relocation.
type Reloc struct {
	RVA  uint32
	Type uint16
}

// Relocs reads the base relocation table, without the padding entries.
func (f *File) Relocs() ([]Reloc, error) {
	d := f.Dir(DirBaseReloc)
	if d.RVA == 0 {
		return nil, nil
	}
	b, err := f.Bytes(d.RVA, d.Size)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	var out []Reloc
	for p := uint32(0); p+8 <= uint32(len(b)); {
		page := le.Uint32(b[p:])
		size := le.Uint32(b[p+4:])
		if size < 8 || p+size > uint32(len(b)) || size%2 != 0 {
			return nil, bad("relocation block at %#x has size %d", p, size)
		}
		for q := p + 8; q < p+size; q += 2 {
			e := le.Uint16(b[q:])
			if e>>12 == RelAbsolute {
				continue
			}
			out = append(out, Reloc{RVA: page + uint32(e&0xFFF), Type: e >> 12})
		}
		p += size
	}
	return out, nil
}

// MaxResourceEntries bounds the directory entries Resources visits: directories may share subtrees, so a hostile
// file could otherwise make the three nested levels name billions of entries.
const MaxResourceEntries = 4096

// Resources reads a resource tree of three levels with id entries (named entries are skipped).
func (f *File) Resources() ([]Resource, error) {
	visited := 0
	d := f.Dir(DirResource)
	if d.RVA == 0 {
		return nil, nil
	}
	root, err := f.Bytes(d.RVA, d.Size)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	type ent struct {
		id  uint32
		off uint32
		dir bool
	}
	readDir := func(off uint32) ([]ent, error) {
		if uint64(off)+16 > uint64(len(root)) {
			return nil, bad("resource directory at %#x beyond the section", off)
		}
		nNamed := uint32(le.Uint16(root[off+12:]))
		nID := uint32(le.Uint16(root[off+14:]))
		if uint64(off)+16+8*uint64(nNamed+nID) > uint64(len(root)) {
			return nil, bad("resource directory at %#x too long", off)
		}
		visited += int(nID)
		if visited > MaxResourceEntries {
			return nil, bad("more than %d resource directory entries", MaxResourceEntries)
		}
		var out []ent
		for i := nNamed; i < nNamed+nID; i++ {
			e := off + 16 + 8*i
			v := le.Uint32(root[e+4:])
			out = append(out, ent{id: le.Uint32(root[e:]), off: v &^ 0x8000_0000, dir: v&0x8000_0000 != 0})
		}
		return out, nil
	}
	var out []Resource
	types, err := readDir(0)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if !t.dir {
			return nil, bad("resource type %d is a leaf", t.id)
		}
		names, err := readDir(t.off)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if !n.dir {
				return nil, bad("resource name %d is a leaf", n.id)
			}
			langs, err := readDir(n.off)
			if err != nil {
				return nil, err
			}
			for _, l := range langs {
				if l.dir || uint64(l.off)+16 > uint64(len(root)) {
					return nil, bad("resource language entry %d malformed", l.id)
				}
				rva := le.Uint32(root[l.off:])
				size := le.Uint32(root[l.off+4:])
				data, err := f.Bytes(rva, size)
				if err != nil {
					return nil, err
				}
				out = append(out, Resource{Type: uint16(t.id), Name: uint16(n.id), Lang: uint16(l.id),
					Data: append([]byte(nil), data...)})
			}
		}
	}
	return out, nil
}

// ComputeChecksum computes the checksum of the file as it is.
func (f *File) ComputeChecksum() uint32 { return Checksum(f.data, f.CheckSumOffset) }
