package pe_test

import (
	"testing"

	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
	"github.com/yakbd/yakbd-cli/pe"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// FuzzParse: the PE reader and every table accessor keep the bounds of docs/fuzzing.md on any bytes. Seeds: the
// tool's own layout DLLs for x86, x64 (and WOW64), ARM64 and each ARM64X variant.
func FuzzParse(f *testing.F) {
	for _, b := range windowsSeeds(f) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			p, err := pe.Parse(b)
			if err != nil {
				return nil
			}
			p.Exports()
			p.Relocs()
			p.Resources()
			p.LoadConfig()
			p.CHPEMetadata()
			p.DynamicRelocs()
			p.ARM64XView()
			p.ComputeChecksum()
			for i := range p.Dirs {
				d := p.Dir(i)
				p.Offset(d.RVA)
				p.Bytes(d.RVA, d.Size)
				p.CString(d.RVA)
				p.SectionAt(d.RVA)
			}
			p.Section(".data")
			return nil
		})
	})
}

// windowsSeeds compiles one small layout for every architecture and ARM64X variant and fails f when one of them
// does not build: a seed that silently drops out leaves a machine unfuzzed.
func windowsSeeds(f *testing.F) [][]byte {
	d := &windows.DLL{FileName: "f.dll", Tables: windows.Tables{
		Modifiers:  windows.Modifiers{VKToBit: []windows.VKBit{{VK: 0x10, Bits: 1}}, ModNumber: []byte{0, 1}},
		CharTables: []windows.CharTable{{Width: 2, Rows: []windows.CharRow{{VK: 'A', Chars: []uint16{'a', 'A'}}, {VK: 'B', Chars: []uint16{windows.WCH_DEAD, 'B'}}, {VK: 0xFF, Chars: []uint16{'^', windows.WCH_NONE}}}}},
		DeadKeys:   []windows.DeadKey{{Dead: '^', Base: 'a', Composed: 0xE2}},
		KeyNames:   []windows.KeyName{{Scan: 1, Name: "Esc"}},
		ScanToVK:   []uint16{0xFF, 0x1B, 'A', 'B'},
		Version:    1,
	}}
	var out [][]byte
	for _, a := range append(append([]windows.Arch(nil), windows.Arches...), windows.ARM64, windows.ARM64X) {
		b, r := windows.Compile(d, a)
		if b == nil {
			f.Fatalf("no %s seed: %s", a, r.Summary())
		}
		out = append(out, b)
	}
	for _, v := range windows.ARM64XVariants {
		b, r := windows.CompileARM64X(d, v)
		if b == nil {
			f.Fatalf("no ARM64X %s seed: %s", v, r.Summary())
		}
		out = append(out, b)
	}
	return out
}
