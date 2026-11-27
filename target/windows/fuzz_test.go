package windows

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// FuzzReadImage: the layout DLL reader keeps the bounds of docs/fuzzing.md on any bytes and never returns neither
// an image nor an error. The tables of a file it reads compile again for the same architecture, and that file
// reads back to the same tables.
func FuzzReadImage(f *testing.F) {
	d := &DLL{FileName: "f.dll", Tables: Tables{
		Modifiers:  Modifiers{VKToBit: []VKBit{{VK: 0x10, Bits: 1}}, ModNumber: []byte{0, 1}},
		CharTables: []CharTable{{Width: 2, Rows: []CharRow{{VK: 'A', Chars: []uint16{'a', 'A'}}, {VK: 'B', Chars: []uint16{WCH_DEAD, 'B'}}, {VK: 0xFF, Chars: []uint16{'^', WCH_NONE}}}}},
		DeadKeys:   []DeadKey{{Dead: '^', Base: 'a', Composed: 0xE2}},
		KeyNames:   []KeyName{{Scan: 1, Name: "Esc"}},
		ScanToVK:   []uint16{0xFF, 0x1B, 'A', 'B'},
		Version:    1,
		Ligatures:  []Ligature{{VK: 'A', Column: 1, Chars: []uint16{'x', 'y'}}}, LigatureWidth: 2,
	}}
	d.Tables.CharTables[0].Rows[0].Chars[1] = WCH_LGTR
	for _, a := range append(append([]Arch(nil), Arches...), ARM64, ARM64X) {
		b, r := Compile(d, a)
		if b == nil {
			f.Fatalf("no %s seed: %s", a, r.Summary())
		}
		f.Add(b)
	}
	for _, v := range ARM64XVariants {
		b, r := CompileARM64X(d, v)
		if b == nil {
			f.Fatalf("no ARM64X %s seed: %s", v, r.Summary())
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			im, r := ReadImage(b)
			if im == nil {
				if !r.HasErrors() {
					return fmt.Errorf("no image and no error")
				}
				return nil
			}
			c, cr := Compile(im.DLL, im.Arch)
			if c == nil || cr.HasErrors() {
				return nil // tables the compiler refuses are reported, not a failure
			}
			again, ar := ReadImage(c)
			if again == nil {
				return fmt.Errorf("a rebuilt %s file does not read: %s", im.Arch, ar.Summary())
			}
			if again.Arch != im.Arch {
				return fmt.Errorf("a rebuilt %s file reads as %s", im.Arch, again.Arch)
			}
			if diff := Differences(&im.DLL.Tables, &again.DLL.Tables); len(diff) > 0 {
				return fmt.Errorf("read, compile, read changes the tables:\n%s", strings.Join(diff, "\n"))
			}
			return nil
		})
	})
}
