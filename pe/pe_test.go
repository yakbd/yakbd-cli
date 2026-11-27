package pe

import (
	"bytes"
	stdpe "debug/pe"
	"encoding/binary"
	"testing"
)

func sampleImage(plus bool) *Image {
	data := make([]byte, 0x30)
	text := []byte{0x48, 0x8D, 0x05, 0, 0, 0, 0, 0xC3}
	im := &Image{
		Machine: MachineAMD64, PE32Plus: plus, ImageBase: 0x1_8000_0000,
		SectionAlignment: 0x1000, FileAlignment: 0x200,
		Characteristics:    FileExecutableImage | FileDLL | FileLargeAddress,
		DllCharacteristics: DllDynamicBase | DllNXCompat,
		Subsystem:          SubsystemNative, OSVersion: [2]uint16{10, 0}, SubsystemVersion: [2]uint16{10, 0},
		Sections: []*Section{
			{Name: ".text", Characteristics: SecCode | SecMemExecute | SecMemRead, Data: text,
				Fixups: []Fixup{{Offset: 3, Kind: Rel32, Target: Ref{".data", 0x10}}}},
			{Name: ".data", Characteristics: SecReadOnlyData, Data: data},
		},
		DLLName:       "test.dll",
		ExportSection: ".data",
		Exports: []Export{
			{Name: "Second", Ordinal: 2, Target: Ref{".text", 0}},
			{Name: "First", Ordinal: 1, Target: Ref{".text", 0}},
		},
		Resources: []Resource{
			{Type: RTString, Name: 63, Lang: 0x409, Data: []byte("abc")},
			{Type: RTString, Name: 63, Lang: 0x407, Data: []byte("de")},
			{Type: RTVersion, Name: 1, Lang: 0x409, Data: []byte("version")},
		},
		ResourceChars: SecReadOnlyData,
		RelocChars:    SecReadOnlyData | SecDiscardable,
	}
	if plus {
		im.Sections[1].Fixups = []Fixup{{Offset: 0, Kind: Abs64, Target: Ref{".data", 0x20}}}
	} else {
		im.Machine = MachineI386
		im.ImageBase = 0x1000_0000
		im.Sections[1].Fixups = []Fixup{{Offset: 0, Kind: Abs32, Target: Ref{".data", 0x20}}}
	}
	return im
}

func TestWriteRead(t *testing.T) {
	for _, plus := range []bool{true, false} {
		im := sampleImage(plus)
		b, lay, err := im.Write()
		if err != nil {
			t.Fatal(err)
		}
		f, err := Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		if f.PE32Plus != plus || f.ImageBase != im.ImageBase || f.Machine != im.Machine {
			t.Errorf("header mismatch: %+v", f)
		}
		if got := f.ComputeChecksum(); got != f.CheckSum || got == 0 {
			t.Errorf("checksum stored %#x computed %#x", f.CheckSum, got)
		}
		names := []string{}
		for _, s := range f.Sections {
			names = append(names, s.Name)
		}
		if want := ".text .data .rsrc .reloc"; joined(names) != want {
			t.Errorf("sections %v", names)
		}
		data := f.Section(".data")
		text := f.Section(".text")
		// Rel32: target = slot + 4 + disp.
		tb, _ := f.Bytes(text.RVA, 8)
		disp := int32(binary.LittleEndian.Uint32(tb[3:]))
		if uint32(int64(text.RVA)+3+4+int64(disp)) != data.RVA+0x10 {
			t.Errorf("rel32 resolves wrong")
		}
		db, _ := f.Bytes(data.RVA, 8)
		var va uint64
		if plus {
			va = binary.LittleEndian.Uint64(db)
		} else {
			va = uint64(binary.LittleEndian.Uint32(db))
		}
		if va != im.ImageBase+uint64(data.RVA)+0x20 {
			t.Errorf("absolute pointer %#x", va)
		}
		rel, err := f.Relocs()
		if err != nil || len(rel) != 1 || rel[0].RVA != data.RVA {
			t.Errorf("relocs %v %v", rel, err)
		}
		wantType := uint16(RelDir64)
		if !plus {
			wantType = RelHighLow
		}
		if rel[0].Type != wantType {
			t.Errorf("reloc type %d", rel[0].Type)
		}
		dll, exps, err := f.Exports()
		if err != nil || dll != "test.dll" || len(exps) != 2 || exps[0].Name != "First" || exps[0].Ordinal != 1 ||
			exps[1].Name != "Second" || exps[0].RVA != text.RVA {
			t.Errorf("exports %q %+v %v", dll, exps, err)
		}
		res, err := f.Resources()
		if err != nil || len(res) != 3 {
			t.Fatalf("resources %v %v", res, err)
		}
		if res[0].Lang != 0x407 || string(res[0].Data) != "de" || res[2].Type != RTVersion {
			t.Errorf("resource order %+v", res)
		}
		if len(lay.Sections) != 4 {
			t.Errorf("layout %+v", lay)
		}
		// The standard library must agree on the container.
		sf, err := stdpe.NewFile(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("debug/pe: %v", err)
		}
		if len(sf.Sections) != 4 || sf.Section(".data").VirtualAddress != data.RVA {
			t.Errorf("debug/pe sections differ")
		}
		var soi uint32
		switch oh := sf.OptionalHeader.(type) {
		case *stdpe.OptionalHeader64:
			soi = oh.SizeOfImage
		case *stdpe.OptionalHeader32:
			soi = oh.SizeOfImage
		}
		if soi != f.SizeOfImage || soi%0x1000 != 0 {
			t.Errorf("SizeOfImage %#x", soi)
		}
	}
}

func joined(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out
}

func TestDeterministic(t *testing.T) {
	a, _, _ := sampleImage(true).Write()
	b, _, _ := sampleImage(true).Write()
	if !bytes.Equal(a, b) {
		t.Error("two writes differ")
	}
}

func TestWriteErrors(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Image)
	}{
		{"long name", func(im *Image) { im.Sections[0].Name = ".toolongname" }},
		{"dup section", func(im *Image) { im.Sections[1].Name = ".text" }},
		{"no export section", func(im *Image) { im.ExportSection = ".nope" }},
		{"dup ordinal", func(im *Image) { im.Exports[0].Ordinal = 1 }},
		{"unknown target", func(im *Image) { im.Sections[0].Fixups[0].Target.Section = ".x" }},
		{"fixup beyond", func(im *Image) { im.Sections[0].Fixups[0].Offset = 7 }},
		{"no alignment", func(im *Image) { im.FileAlignment = 0 }},
	}
	for _, tt := range tests {
		im := sampleImage(true)
		tt.mod(im)
		if _, _, err := im.Write(); err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
}

func TestParseMalformed(t *testing.T) {
	good, _, _ := sampleImage(true).Write()
	if _, err := Parse(nil); err == nil {
		t.Error("nil parsed")
	}
	// Every truncation and every single-byte corruption of the headers must fail cleanly or parse; never panic.
	for n := 0; n < 0x400 && n < len(good); n++ {
		f, err := Parse(good[:n])
		if err == nil {
			f.Exports()
			f.Relocs()
			f.Resources()
		}
	}
	for i := 0; i < 0x400; i++ {
		b := append([]byte(nil), good...)
		b[i] ^= 0xFF
		if f, err := Parse(b); err == nil {
			f.Exports()
			f.Relocs()
			f.Resources()
		}
	}
}

func TestChecksumOddLength(t *testing.T) {
	// 3 bytes: words 0x0201 and 0x03, plus length 3.
	if got := Checksum([]byte{1, 2, 3}, 100); got != 0x0201+0x03+3 {
		t.Errorf("got %#x", got)
	}
}
