package pe

import (
	"encoding/binary"
	"testing"
)

// TestDynamicRelocsRoundTrip: the writer's ARM64X entries and load-configuration fields read back, and the x64
// view has them applied.
func TestDynamicRelocsRoundTrip(t *testing.T) {
	im := sampleImage(true)
	im.Machine = MachineARM64
	cfg := &Section{Name: ".00cfg", Characteristics: SecReadOnlyData, Data: make([]byte, LoadConfigSize64)}
	binary.LittleEndian.PutUint32(cfg.Data, LoadConfigSize64)
	im.Sections = append(im.Sections, cfg)
	im.Directories = []Directory{{Index: DirLoadConf, Target: Ref{Section: ".00cfg"}, Size: LoadConfigSize64}}
	im.LoadConfigs = []Ref{{Section: ".00cfg"}}
	im.DynamicRelocs = []DynamicValue{
		{At: Ref{Offset: DirectoryField(DirExport, true) + 4}, Size: 4, Value: 0x1234},
		{At: Ref{Offset: MachineField}, Size: 2, Value: uint64(MachineAMD64)},
		{At: Ref{Section: ".data", Offset: 8}, Size: 8, ValueRVA: &Ref{Section: ".text", Offset: 2}},
	}
	b, _, err := im.Write()
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := f.LoadConfig()
	if err != nil || lc == nil || !lc.HasDynamicRelocs || int(lc.DynRelocSection) != len(f.Sections) {
		t.Fatalf("load config %+v %v", lc, err)
	}
	fx, err := f.DynamicRelocs()
	if err != nil || len(fx) != 3 || fx[0].RVA != MachineField || fx[0].Value != 0x8664 || fx[2].Size != 8 {
		t.Fatalf("entries %+v %v", fx, err)
	}
	v, err := f.ARM64XView()
	if err != nil {
		t.Fatal(err)
	}
	g, _ := Parse(v)
	text := f.Section(".text").RVA
	slot, _ := g.Bytes(f.Section(".data").RVA+8, 8)
	if g.Machine != MachineAMD64 || g.Dir(DirExport).Size != 0x1234 || binary.LittleEndian.Uint64(slot) != uint64(text+2) {
		t.Errorf("view: machine %v, export size %#x, slot %x", g.Machine, g.Dir(DirExport).Size, slot)
	}
	if r := f.Dir(DirBaseReloc); r.Size == 0 || r.RVA != f.Section(".reloc").RVA || r.Size >= f.Section(".reloc").VirtualSize {
		t.Errorf("base relocation directory %+v", r)
	}
}

// TestArm64Fixups: ADRP pages forwards and backwards, ADD and LDR low bits.
func TestArm64Fixups(t *testing.T) {
	ins := func(w ...uint32) []byte {
		b := make([]byte, 4*len(w))
		for i, x := range w {
			binary.LittleEndian.PutUint32(b[4*i:], x)
		}
		return b
	}
	im := sampleImage(true)
	im.Machine = MachineARM64
	im.Sections[0].Data = ins(0x90000000, 0x91000000, 0xF9400210, 0x90000000)
	im.Sections[0].Fixups = []Fixup{
		{Offset: 0, Kind: Arm64Page21, Target: Ref{".data", 0x28}},
		{Offset: 4, Kind: Arm64Lo12, Target: Ref{".data", 0x28}},
		{Offset: 8, Kind: Arm64Lo12Ldr64, Target: Ref{".data", 0x28}},
		{Offset: 12, Kind: Arm64Page21, Target: Ref{".text", 0}},
	}
	b, lay, err := im.Write()
	if err != nil {
		t.Fatal(err)
	}
	f, _ := Parse(b)
	text, data := lay.Sections[0].RVA, lay.Sections[1].RVA
	code, _ := f.Bytes(text, 16)
	le := binary.LittleEndian
	page := func(w uint32) int64 {
		imm := int64((w>>29)&3 | ((w>>5)&0x7FFFF)<<2)
		if imm&(1<<20) != 0 {
			imm -= 1 << 21
		}
		return imm
	}
	if got := page(le.Uint32(code)); got != int64(data>>12)-int64(text>>12) {
		t.Errorf("adrp page delta %d", got)
	}
	if got := page(le.Uint32(code[12:])); got != 0 {
		t.Errorf("adrp to own page %d", got)
	}
	if lo := le.Uint32(code[4:]) >> 10 & 0xFFF; lo != (data+0x28)&0xFFF {
		t.Errorf("add lo12 %#x", lo)
	}
	if lo := le.Uint32(code[8:]) >> 10 & 0xFFF; lo != ((data+0x28)&0xFFF)/8 {
		t.Errorf("ldr lo12 %#x", lo)
	}
	im.Sections[0].Fixups = []Fixup{{Offset: 8, Kind: Arm64Lo12Ldr64, Target: Ref{".data", 0x29}}}
	if _, _, err := im.Write(); err == nil {
		t.Error("unaligned LDR target accepted")
	}
}
