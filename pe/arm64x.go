package pe

// ARM64X images: the dynamic value relocation table (writer and reader), the load configuration fields an ARM64X
// image needs, the CHPE metadata and its code map, and the x64 view. Format sources: docs/arm64x.md §2 (the PE
// Format page, Microsoft's Arm64X and Arm64EC pages, and the structures of LLVM's COFF headers).

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"
	"sort"
)

// Load configuration offsets (64-bit structure, docs/arm64x.md §2.3).
const (
	LoadConfigSize64          = 0x140
	LoadConfigCHPEPointer     = 0xC8
	LoadConfigDynRelocOffset  = 0xE0
	LoadConfigDynRelocSection = 0xE4
)

// CHPE code-map range types (docs/arm64x.md §2.4).
const (
	RangeARM64   = 0
	RangeARM64EC = 1
	RangeAMD64   = 2
)

// dynamicRelocTable encodes the table: header, one ARM64X relocation header, blocks per 4 KiB page.
func dynamicRelocTable(vals []DynamicValue, rvaOf map[string]uint32) ([]byte, error) {
	type ent struct {
		rva  uint32
		size int
		val  uint64
	}
	var es []ent
	for _, v := range vals {
		rva := v.At.Offset
		if v.At.Section != "" {
			base, ok := rvaOf[v.At.Section]
			if !ok {
				return nil, fmt.Errorf("pe: dynamic value in unknown section %q", v.At.Section)
			}
			rva += base
		}
		val := v.Value
		if v.ValueRVA != nil {
			base, ok := rvaOf[v.ValueRVA.Section]
			if !ok {
				return nil, fmt.Errorf("pe: dynamic value refers to unknown section %q", v.ValueRVA.Section)
			}
			val = uint64(base + v.ValueRVA.Offset)
		}
		if v.Size != 2 && v.Size != 4 && v.Size != 8 {
			return nil, fmt.Errorf("pe: dynamic value of %d bytes", v.Size)
		}
		es = append(es, ent{rva, v.Size, val})
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].rva < es[j].rva })
	le := binary.LittleEndian
	var body bytes.Buffer
	for i := 0; i < len(es); {
		page := es[i].rva &^ 0xFFF
		start := body.Len()
		var hdr [8]byte
		le.PutUint32(hdr[:], page)
		body.Write(hdr[:])
		for ; i < len(es) && es[i].rva&^0xFFF == page; i++ {
			e := es[i]
			binary.Write(&body, le, uint16(e.rva&0xFFF)|ARM64XValue<<12|uint16(bits.Len(uint(e.size))-1)<<14)
			var v [8]byte
			le.PutUint64(v[:], e.val)
			body.Write(v[:e.size])
		}
		for body.Len()%4 != 0 {
			body.WriteByte(0)
		}
		le.PutUint32(body.Bytes()[start+4:], uint32(body.Len()-start))
	}
	out := make([]byte, 8+12, 8+12+body.Len())
	le.PutUint32(out[0:], 1)                     // Version
	le.PutUint32(out[4:], uint32(12+body.Len())) // Size of what follows the table header
	le.PutUint64(out[8:], DynamicRelocARM64X)    // Symbol
	le.PutUint32(out[16:], uint32(body.Len()))   // BaseRelocSize
	return append(out, body.Bytes()...), nil
}

// LoadConfig is the part of a 64-bit load configuration this package reads.
type LoadConfig struct {
	Size              uint32
	CHPEMetadata      uint64 // VA
	DynRelocOffset    uint32
	DynRelocSection   uint16
	RVA, DirSize      uint32
	HasDynamicRelocs  bool
	HasCHPEMetadata   bool
	SecurityCookie    uint64
	GuardFlags        uint32
	GuardCFCheck      uint64
	GuardCFDispatch   uint64
	GuardCFFunctions  uint64
	GuardCFFuncsCount uint64
}

// LoadConfig reads the 64-bit load configuration of the directory entry, or nil when there is none.
func (f *File) LoadConfig() (*LoadConfig, error) {
	d := f.Dir(DirLoadConf)
	if d.RVA == 0 {
		return nil, nil
	}
	if !f.PE32Plus {
		return nil, bad("32-bit load configurations are not read")
	}
	b, err := f.Bytes(d.RVA, 4)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	size := le.Uint32(b)
	if size < 4 || size > 0x1000 {
		return nil, bad("load configuration size %#x", size)
	}
	b, err = f.Bytes(d.RVA, size)
	if err != nil {
		return nil, err
	}
	lc := &LoadConfig{Size: size, RVA: d.RVA, DirSize: d.Size}
	u64 := func(off uint32) uint64 {
		if off+8 <= size {
			return le.Uint64(b[off:])
		}
		return 0
	}
	lc.SecurityCookie = u64(0x58)
	lc.GuardCFCheck, lc.GuardCFDispatch, lc.GuardCFFunctions, lc.GuardCFFuncsCount = u64(0x70), u64(0x78), u64(0x80), u64(0x88)
	if size >= 0x94 {
		lc.GuardFlags = le.Uint32(b[0x90:])
	}
	if size >= LoadConfigCHPEPointer+8 {
		lc.CHPEMetadata = le.Uint64(b[LoadConfigCHPEPointer:])
		lc.HasCHPEMetadata = lc.CHPEMetadata != 0
	}
	if size >= LoadConfigDynRelocSection+2 {
		lc.DynRelocOffset = le.Uint32(b[LoadConfigDynRelocOffset:])
		lc.DynRelocSection = le.Uint16(b[LoadConfigDynRelocSection:])
		lc.HasDynamicRelocs = lc.DynRelocSection != 0
	}
	return lc, nil
}

// CodeRange is one entry of the CHPE code map.
type CodeRange struct {
	Start, Length uint32
	Type          int // RangeARM64, RangeARM64EC or RangeAMD64
}

// CHPE is the CHPE metadata as read.
type CHPE struct {
	Version     uint32
	CodeMap     []CodeRange
	EntryRanges [][3]uint32 // start, end, entry point
	Redirection [][2]uint32 // source, destination
	// Slots are the RVAs of the helper slots: dispatch call_no_redirect, ret, call, icall, icall_cfg,
	// get/set x64 information, dispatch_fptr.
	Slots []uint32
}

// CHPEMetadata reads the CHPE metadata the load configuration points to, or nil.
func (f *File) CHPEMetadata() (*CHPE, error) {
	lc, err := f.LoadConfig()
	if err != nil || lc == nil || !lc.HasCHPEMetadata {
		return nil, err
	}
	if lc.CHPEMetadata < f.ImageBase {
		return nil, bad("CHPE pointer %#x below the image base", lc.CHPEMetadata)
	}
	rva := uint32(lc.CHPEMetadata - f.ImageBase)
	b, err := f.Bytes(rva, 4)
	if err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	c := &CHPE{Version: le.Uint32(b)}
	n := uint32(20 * 4)
	if c.Version >= 2 {
		n = 23 * 4
	}
	if b, err = f.Bytes(rva, n); err != nil {
		return nil, err
	}
	w := func(i int) uint32 { return le.Uint32(b[4*i:]) }
	read := func(at, count, size uint32, limit uint32) ([]byte, error) {
		if count > limit {
			return nil, bad("CHPE table of %d entries", count)
		}
		if count == 0 {
			return nil, nil
		}
		return f.Bytes(at, count*size)
	}
	m, err := read(w(1), w(2), 8, 1024)
	if err != nil {
		return nil, err
	}
	for i := 0; i+8 <= len(m); i += 8 {
		s := le.Uint32(m[i:])
		c.CodeMap = append(c.CodeMap, CodeRange{Start: s &^ 3, Length: le.Uint32(m[i+4:]), Type: int(s & 3)})
	}
	r, err := read(w(3), w(12), 12, 1024)
	if err != nil {
		return nil, err
	}
	for i := 0; i+12 <= len(r); i += 12 {
		c.EntryRanges = append(c.EntryRanges, [3]uint32{le.Uint32(r[i:]), le.Uint32(r[i+4:]), le.Uint32(r[i+8:])})
	}
	red, err := read(w(4), w(13), 8, 1024)
	if err != nil {
		return nil, err
	}
	for i := 0; i+8 <= len(red); i += 8 {
		c.Redirection = append(c.Redirection, [2]uint32{le.Uint32(red[i:]), le.Uint32(red[i+4:])})
	}
	c.Slots = []uint32{w(5), w(6), w(7), w(8), w(9), w(14), w(15), w(18)}
	return c, nil
}

// RangeAt returns the code-map type of rva, or -1 when no range holds it.
func (c *CHPE) RangeAt(rva uint32) int {
	for _, r := range c.CodeMap {
		if rva >= r.Start && uint64(rva) < uint64(r.Start)+uint64(r.Length) {
			return r.Type
		}
	}
	return -1
}

// DynamicFixup is one ARM64X entry of the dynamic value relocation table.
type DynamicFixup struct {
	RVA   uint32
	Type  int // ARM64XZeroFill, ARM64XValue, ARM64XDelta
	Size  int // bytes written (zero fill, value) or 4 (delta)
	Value uint64
	Delta int64
}

// DynamicRelocs reads the ARM64X entries of the dynamic value relocation table the load configuration names.
func (f *File) DynamicRelocs() ([]DynamicFixup, error) {
	lc, err := f.LoadConfig()
	if err != nil || lc == nil || !lc.HasDynamicRelocs {
		return nil, err
	}
	si := int(lc.DynRelocSection) - 1
	if si < 0 || si >= len(f.Sections) {
		return nil, bad("dynamic relocation table in section %d", lc.DynRelocSection)
	}
	s := f.Sections[si]
	le := binary.LittleEndian
	b, err := f.Bytes(s.RVA+lc.DynRelocOffset, 8)
	if err != nil {
		return nil, err
	}
	if v := le.Uint32(b); v != 1 {
		return nil, bad("dynamic relocation table version %d", v)
	}
	size := le.Uint32(b[4:])
	if size > 0x10000 {
		return nil, bad("dynamic relocation table of %d bytes", size)
	}
	t, err := f.Bytes(s.RVA+lc.DynRelocOffset+8, size)
	if err != nil {
		return nil, err
	}
	var out []DynamicFixup
	for p := uint32(0); p+12 <= size; {
		sym := le.Uint64(t[p:])
		n := le.Uint32(t[p+8:])
		p += 12
		if uint64(p)+uint64(n) > uint64(size) {
			return nil, bad("dynamic relocation of %d bytes beyond the table", n)
		}
		if sym != DynamicRelocARM64X {
			p += n
			continue
		}
		blk := t[p : p+n]
		for q := uint32(0); q+8 <= n; {
			page, bs := le.Uint32(blk[q:]), le.Uint32(blk[q+4:])
			if bs < 8 || q+bs > n {
				return nil, bad("ARM64X block of %d bytes", bs)
			}
			for r := q + 8; r+2 <= q+bs; {
				e := le.Uint16(blk[r:])
				r += 2
				fx := DynamicFixup{RVA: page + uint32(e&0xFFF), Type: int(e>>12) & 3}
				meta := int(e >> 14)
				switch fx.Type {
				case ARM64XZeroFill:
					if e == 0 {
						continue // padding
					}
					fx.Size = 1 << meta
				case ARM64XValue:
					fx.Size = 1 << meta
					if fx.Size == 1 || r+uint32(fx.Size) > q+bs {
						return nil, bad("ARM64X value of %d bytes", fx.Size)
					}
					var v [8]byte
					copy(v[:], blk[r:r+uint32(fx.Size)])
					fx.Value = le.Uint64(v[:])
					r += uint32(fx.Size)
				case ARM64XDelta:
					if r+2 > q+bs {
						return nil, bad("ARM64X delta beyond its block")
					}
					d := int64(le.Uint16(blk[r:]))
					r += 2
					if meta&2 != 0 {
						d *= 8
					} else {
						d *= 4
					}
					if meta&1 != 0 {
						d = -d
					}
					fx.Size, fx.Delta = 4, d // a 32-bit value (LLVM Arm64XRelocRef::getSize)
				default:
					return nil, bad("ARM64X entry type 3")
				}
				out = append(out, fx)
			}
			q += bs
		}
		p += n
	}
	return out, nil
}

// ARM64XView returns the bytes an x64 process sees: a copy of the file with every ARM64X entry applied. Entries
// must fall into the headers or into the raw data of a section.
func (f *File) ARM64XView() ([]byte, error) {
	fx, err := f.DynamicRelocs()
	if err != nil {
		return nil, err
	}
	if len(fx) == 0 {
		return nil, bad("no ARM64X entries")
	}
	out := append([]byte(nil), f.data...)
	le := binary.LittleEndian
	for _, x := range fx {
		var off uint32
		if x.RVA < f.SizeOfHeaders {
			off = x.RVA
		} else if off, err = f.Offset(x.RVA); err != nil {
			return nil, err
		}
		if uint64(off)+uint64(x.Size) > uint64(len(out)) {
			return nil, bad("ARM64X entry at %#x beyond the file", x.RVA)
		}
		switch x.Type {
		case ARM64XZeroFill:
			clear(out[off : off+uint32(x.Size)])
		case ARM64XValue:
			var v [8]byte
			le.PutUint64(v[:], x.Value)
			copy(out[off:], v[:x.Size])
		case ARM64XDelta:
			le.PutUint32(out[off:], uint32(int64(le.Uint32(out[off:]))+x.Delta))
		}
	}
	return out, nil
}
