package windows

import (
	"encoding/binary"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/pe"
)

// dataSection is the name of the one section that holds every table (dll-format §1.2).
const dataSection = ".data"

// kbdTablesFull is the size of the full form of KBDTABLES in the 64-bit layout. No table may start inside
// [KBDTABLES, KBDTABLES + kbdTablesFull) (dll-format §2); the writer keeps this zone free on every architecture.
const kbdTablesFull = 0x68

// builder appends little-endian values to a section and records pointer fixups.
type builder struct {
	buf   []byte
	fix   []pe.Fixup
	psize int
	kind  pe.FixupKind
	// pointers lists the offsets of every non-zero pointer slot with its target, for the writer's checks.
	pointers []pointer
}

type pointer struct{ slot, target uint32 }

func newBuilder(a Arch) *builder {
	b := &builder{psize: a.PointerSize(), kind: pe.Abs32}
	if a != WOW64 && a.PointerSize() == 8 {
		b.kind = pe.Abs64
	}
	return b
}

func (b *builder) off() uint32 { return uint32(len(b.buf)) }

func (b *builder) align(n int) {
	for len(b.buf)%n != 0 {
		b.buf = append(b.buf, 0)
	}
}

func (b *builder) u8(v byte)    { b.buf = append(b.buf, v) }
func (b *builder) u16(v uint16) { b.buf = binary.LittleEndian.AppendUint16(b.buf, v) }
func (b *builder) u32(v uint32) { b.buf = binary.LittleEndian.AppendUint32(b.buf, v) }
func (b *builder) zero(n int)   { b.buf = append(b.buf, make([]byte, n)...) }

// ptr appends a pointer slot to target; a negative target writes a zero pointer without a relocation.
func (b *builder) ptr(target int64) {
	b.align(b.psize)
	at := b.off()
	b.zero(b.psize)
	b.setPtr(at, target)
}

// setPtr fills a pointer slot that was reserved earlier.
func (b *builder) setPtr(at uint32, target int64) {
	if target < 0 {
		return
	}
	b.fix = append(b.fix, pe.Fixup{Offset: at, Kind: b.kind, Target: pe.Ref{Section: dataSection, Offset: uint32(target)}})
	b.pointers = append(b.pointers, pointer{at, uint32(target)})
}

// wstr appends a NUL-terminated UTF-16 string and returns its offset.
func (b *builder) wstr(s string) uint32 {
	b.align(2)
	at := b.off()
	for _, u := range utf16.Encode([]rune(s)) {
		b.u16(u)
	}
	b.u16(0)
	return at
}

// encoded is the result of encoding the tables.
type encoded struct {
	data      []byte
	fixups    []pe.Fixup
	kbdTables uint32 // offset of KBDTABLES in .data
	pointers  []pointer
	size      uint32 // size of KBDTABLES in this architecture's full form
}

// encodeTables writes KBDTABLES and every table it points to into one .data buffer. KBDTABLES comes first, in
// its full form, followed by a free zone up to kbdTablesFull + 8 bytes; then the sub-tables. Every pointer
// points backwards except those of KBDTABLES, which are filled in last.
func encodeTables(t *Tables, a Arch) *encoded {
	b := newBuilder(a)
	p := b.psize
	// KBDTABLES layout (dll-format §3).
	kt := b.off()
	fieldOff := func(i int) uint32 { return kt + uint32(i*p) } // the seven leading pointers
	ktSize := 0x3C
	if p == 8 {
		ktSize = 0x68
	}
	b.zero(ktSize)
	b.zero(kbdTablesFull + 8 - ktSize) // free zone, also on x86
	offMax := kt + uint32(7*p)
	offE0 := kt + uint32(8*p)
	offE1 := kt + uint32(9*p)
	offFlags := kt + uint32(10*p)
	offLg := offFlags + 4
	offPLig := offFlags + 8 // nLgMax, cbLgEntry and 2 padding bytes follow the flags
	offType := offPLig + uint32(p)

	// MODIFIERS: VK_TO_BIT array, then the structure.
	b.align(2)
	vkToBit := b.off()
	for _, m := range t.Modifiers.VKToBit {
		b.u8(m.VK)
		b.u8(m.Bits)
	}
	b.u16(0)
	b.align(p)
	mods := b.off()
	b.ptr(int64(vkToBit))
	b.u16(uint16(len(t.Modifiers.ModNumber) - 1))
	b.buf = append(b.buf, t.Modifiers.ModNumber...)

	// Character rows, then the VK_TO_WCHAR_TABLE array.
	rowOffs := make([]uint32, len(t.CharTables))
	for i, ct := range t.CharTables {
		b.align(2)
		rowOffs[i] = b.off()
		for _, r := range ct.Rows {
			b.u8(r.VK)
			b.u8(r.Attributes)
			for _, c := range r.Chars {
				b.u16(c)
			}
		}
		b.zero(2 + 2*ct.Width) // terminator row
	}
	b.align(p)
	vkTables := b.off()
	for i, ct := range t.CharTables {
		b.ptr(int64(rowOffs[i]))
		b.u8(byte(ct.Width))
		b.u8(byte(2 + 2*ct.Width))
		b.align(p)
	}
	b.ptr(-1)
	b.u8(0)
	b.u8(0)
	b.align(p)

	// DEADKEY: written whenever there are entries or a dead cell (dll-format §4.3).
	deadKeys := int64(-1)
	if len(t.DeadKeys) > 0 || t.hasCell(WCH_DEAD) {
		b.align(4)
		deadKeys = int64(b.off())
		for _, d := range t.DeadKeys {
			b.u32(uint32(d.Dead)<<16 | uint32(d.Base))
			b.u16(d.Composed)
			b.u16(d.Flags)
		}
		b.zero(8)
	}

	// Key names: strings, then the arrays; each pointer is zero when its list is empty.
	// A list without names gets a zero pointer: the tested shape (dll-format §4.4).
	names := func(list []KeyName) int64 {
		if len(list) == 0 {
			return -1
		}
		offs := make([]uint32, len(list))
		for i, kn := range list {
			offs[i] = b.wstr(kn.Name)
		}
		b.align(p)
		at := b.off()
		for i, kn := range list {
			b.u8(kn.Scan)
			b.zero(p - 1)
			b.ptr(int64(offs[i]))
		}
		b.zero(2 * p)
		return int64(at)
	}
	keyNames := names(t.KeyNames)
	keyNamesExt := names(t.KeyNamesExt)
	keyNamesDead := int64(-1)
	if len(t.KeyNamesDead) > 0 {
		offs := make([]uint32, len(t.KeyNamesDead))
		for i, dn := range t.KeyNamesDead {
			offs[i] = b.wstr(string(utf16.Decode([]uint16{dn.Dead})) + dn.Name)
			// A dead character that is not a valid scalar on its own is written as its unit.
			if !validUnit(dn.Dead) {
				binary.LittleEndian.PutUint16(b.buf[offs[i]:], dn.Dead)
			}
		}
		b.align(p)
		keyNamesDead = int64(b.off())
		for _, o := range offs {
			b.ptr(int64(o))
		}
		b.ptr(-1)
	}

	// Scan-code tables.
	b.align(2)
	vsc := b.off()
	for _, v := range t.ScanToVK {
		b.u16(v)
	}
	vscVK := func(list []ScanVK) int64 {
		b.align(4)
		at := b.off()
		for _, e := range list {
			b.u8(e.Scan)
			b.u8(0)
			b.u16(e.VK)
		}
		b.zero(4)
		return int64(at)
	}
	e0 := vscVK(t.E0)
	e1 := vscVK(t.E1)

	// Ligatures.
	lig := int64(-1)
	if len(t.Ligatures) > 0 {
		b.align(2)
		lig = int64(b.off())
		for _, l := range t.Ligatures {
			b.u8(l.VK)
			b.u8(0)
			b.u16(l.Column)
			for i := 0; i < t.LigatureWidth; i++ {
				if i < len(l.Chars) {
					b.u16(l.Chars[i])
				} else {
					b.u16(WCH_NONE)
				}
			}
		}
		b.zero(4 + 2*t.LigatureWidth)
	}

	// Fill in KBDTABLES.
	b.setPtr(fieldOff(0), int64(mods))
	b.setPtr(fieldOff(1), int64(vkTables))
	b.setPtr(fieldOff(2), deadKeys)
	b.setPtr(fieldOff(3), keyNames)
	b.setPtr(fieldOff(4), keyNamesExt)
	b.setPtr(fieldOff(5), keyNamesDead)
	b.setPtr(fieldOff(6), int64(vsc))
	b.buf[offMax] = byte(len(t.ScanToVK))
	b.setPtr(offE0, e0)
	b.setPtr(offE1, e1)
	binary.LittleEndian.PutUint32(b.buf[offFlags:], uint32(t.Version)<<16|uint32(t.LocaleFlags))
	if len(t.Ligatures) > 0 {
		b.buf[offLg] = byte(t.LigatureWidth)
		b.buf[offLg+1] = byte(4 + 2*t.LigatureWidth)
	}
	b.setPtr(offPLig, lig)
	binary.LittleEndian.PutUint32(b.buf[offType:], t.Type)
	binary.LittleEndian.PutUint32(b.buf[offType+4:], t.SubType)
	b.align(8)
	return &encoded{data: b.buf, fixups: b.fix, kbdTables: kt, pointers: b.pointers, size: uint32(ktSize)}
}

func (t *Tables) hasCell(v uint16) bool {
	for _, ct := range t.CharTables {
		for _, r := range ct.Rows {
			for _, c := range r.Chars {
				if c == v {
					return true
				}
			}
		}
	}
	return false
}

func validUnit(u uint16) bool { return u < 0xD800 || u > 0xDFFF }
