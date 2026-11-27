package windows

import (
	"bytes"
	"crypto/sha256"
	stdpe "debug/pe"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/pe"
)

// x64Tables follows the x64 view's export to the tables: an x64 descriptor function in an x64 range, or an export
// thunk in an x64 range that jumps to an ARM64EC function whose entry thunk computes the tables (docs/arm64x.md).
func x64Tables(t *testing.T, f *pe.File, c *pe.CHPE) uint32 {
	t.Helper()
	_, exps, err := f.Exports()
	if err != nil || len(exps) != 1 || exps[0].Ordinal != 1 || exps[0].Name != ExportName {
		t.Fatalf("x64 exports %+v %v", exps, err)
	}
	fn := exps[0].RVA
	if c.RangeAt(fn) != pe.RangeAMD64 {
		t.Fatalf("x64 export %#x is in a range of type %d", fn, c.RangeAt(fn))
	}
	code, err := f.Bytes(fn, 16)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if bytes.Equal(code[:3], []byte{0x48, 0x8D, 0x05}) && code[7] == 0xC3 {
		return uint32(int64(fn) + 7 + int64(int32(le.Uint32(code[3:]))))
	}
	if !bytes.Equal(code[:10], x64ExportThunk[:10]) || code[14] != 0xCC {
		t.Fatalf("x64 export has an unknown shape % x", code)
	}
	ec := uint32(int64(fn) + 14 + int64(int32(le.Uint32(code[10:]))))
	if c.RangeAt(ec) != pe.RangeARM64EC {
		t.Fatalf("export thunk jumps to %#x, range type %d", ec, c.RangeAt(ec))
	}
	if len(c.Redirection) != 1 || c.Redirection[0] != [2]uint32{fn, ec} || len(c.EntryRanges) != 1 || c.EntryRanges[0][0] != fn {
		t.Errorf("redirection %v, entry ranges %v", c.Redirection, c.EntryRanges)
	}
	pre, _ := f.Bytes(ec-4, 4)
	thunk := ec + le.Uint32(pre)&^3
	if c.RangeAt(thunk) != pe.RangeARM64EC {
		t.Fatalf("entry thunk %#x outside the ARM64EC range", thunk)
	}
	body, _ := f.Bytes(ec, 12)
	direct, err := arm64Target(body, ec)
	if err != nil {
		t.Fatal(err)
	}
	// The entry thunk: adrp x8; add x8 (the tables, into RAX), then adrp x16; ldr x16, [dispatch_ret]; br x16.
	th, _ := f.Bytes(thunk, 20)
	x0 := append([]byte(nil), th[:8]...)
	le.PutUint32(x0, le.Uint32(x0)&^0x1F)
	le.PutUint32(x0[4:], le.Uint32(x0[4:])&^0x3FF)
	viaThunk, err := arm64Target(append(x0, a64(a64Ret)...), thunk)
	if err != nil || th[0]&0x1F != 8 || le.Uint32(th[16:]) != a64BrX16 {
		t.Fatalf("entry thunk % x: %v", th, err)
	}
	ldr := le.Uint32(th[12:])
	adrp := le.Uint32(th[8:])
	imm := int64((adrp>>29)&3 | ((adrp>>5)&0x7FFFF)<<2)
	slot := uint32(int64((thunk+8)&^0xFFF)+imm<<12) + (ldr>>10&0xFFF)*8
	if slot != c.Slots[1] {
		t.Errorf("entry thunk returns through %#x, dispatch_ret slot is %#x", slot, c.Slots[1])
	}
	if direct != viaThunk {
		t.Errorf("function gives %#x, entry thunk %#x", direct, viaThunk)
	}
	return direct
}

// TestARM64X writes every variant, reads it back in both views with this package's reader and checks the
// container rules of docs/arm64x.md.
func TestARM64X(t *testing.T) {
	for _, v := range ARM64XVariants {
		t.Run(string(v), func(t *testing.T) {
			d := sample()
			b, r := CompileARM64X(d, v)
			if b == nil {
				t.Fatal(r.Summary())
			}
			// Native view: an ARM64 file whose tables read back equal.
			got, arch, err := Read(b)
			if err != nil || arch != ARM64X || !reflect.DeepEqual(got, d) {
				t.Fatalf("native view: %v %v %v", arch, err, !reflect.DeepEqual(got, d))
			}
			f, _ := pe.Parse(b)
			if f.Machine != pe.MachineARM64 || f.FileAlignment != 0x200 || f.DllCharacteristics != 0x0160 || f.Characteristics != 0x2022 {
				t.Errorf("header %+v", f)
			}
			c, err := f.CHPEMetadata()
			if err != nil || c == nil || c.Version != 2 {
				t.Fatalf("CHPE %+v %v", c, err)
			}
			_, exps, _ := f.Exports()
			if len(exps) != 1 || c.RangeAt(exps[0].RVA) != pe.RangeARM64 {
				t.Errorf("native export %+v not in an ARM64 range", exps)
			}
			for i := 1; i < len(c.CodeMap); i++ {
				if c.CodeMap[i].Start <= c.CodeMap[i-1].Start {
					t.Errorf("code map not sorted: %+v", c.CodeMap)
				}
			}
			for _, cr := range c.CodeMap {
				if cr.Start%0x1000 != 0 {
					t.Errorf("code range %+v does not start a page", cr)
				}
			}
			for _, s := range c.Slots {
				sec := f.SectionAt(s)
				if s == 0 || sec == nil || sec.Name != cfgSection || s%8 != 0 {
					t.Errorf("helper slot %#x", s)
				}
			}
			fx, err := f.DynamicRelocs()
			want := 5
			if v == VariantX64OneCfg {
				want = 3
			}
			if err != nil || len(fx) != want {
				t.Fatalf("%d ARM64X entries (%v), want %d", len(fx), err, want)
			}
			lc, _ := f.LoadConfig()
			if bt := f.Dir(pe.DirBaseReloc); bt.RVA+bt.Size > f.Sections[lc.DynRelocSection-1].RVA+lc.DynRelocOffset {
				t.Error("the base-relocation directory covers the dynamic relocation table")
			}
			// x64 view.
			xb, err := f.ARM64XView()
			if err != nil {
				t.Fatal(err)
			}
			x, err := pe.Parse(xb)
			if err != nil || x.Machine != pe.MachineAMD64 {
				t.Fatalf("x64 view: %v %v", x.Machine, err)
			}
			xc, err := x.CHPEMetadata()
			if err != nil || xc == nil {
				t.Fatalf("x64 view CHPE %v", err)
			}
			xlc, _ := x.LoadConfig()
			if (v == VariantX64OneCfg) != (xlc.RVA == lc.RVA) {
				t.Errorf("load configurations: native %#x, x64 %#x", lc.RVA, xlc.RVA)
			}
			rva := x64Tables(t, x, xc)
			rd := &tableReader{f: x, p: 8, budget: 1 << 24}
			tb, _, err := rd.tables(rva)
			if err != nil || !reflect.DeepEqual(*tb, d.Tables) {
				t.Errorf("x64 view tables: %v", err)
			}
			if v == VariantX64 || v == VariantX64OneCfg {
				if got, arch, err := Read(xb); err != nil || arch != AMD64 || !reflect.DeepEqual(got.Tables, d.Tables) {
					t.Errorf("x64 view through Read: %v %v", arch, err)
				}
			}
			// The standard library reads both views as containers.
			for _, img := range [][]byte{b, xb} {
				sf, err := stdpe.NewFile(bytes.NewReader(img))
				if err != nil {
					t.Fatal(err)
				}
				sf.Close()
			}
			if len(b) > MaxFileSize {
				t.Errorf("%d bytes", len(b))
			}
		})
	}
}

func TestPlainARM64(t *testing.T) {
	d := sample()
	b, r := Compile(d, ARM64)
	if b == nil {
		t.Fatal(r.Summary())
	}
	got, arch, err := Read(b)
	if err != nil || arch != ARM64 || !reflect.DeepEqual(got, d) {
		t.Fatalf("%v %v", arch, err)
	}
	f, _ := pe.Parse(b)
	if f.Dir(pe.DirLoadConf).RVA != 0 {
		t.Error("a plain ARM64 file has a load configuration")
	}
	if _, err := f.ARM64XView(); err == nil {
		t.Error("a plain ARM64 file has an x64 view")
	}
}

func TestARM64Golden(t *testing.T) {
	path := filepath.Join("testdata", "golden-arm64.sha256")
	var lines []string
	add := func(name string, b []byte) {
		sum := sha256.Sum256(b)
		lines = append(lines, name+" "+hex.EncodeToString(sum[:]))
	}
	b, _ := Compile(sample(), ARM64)
	add("arm64", b)
	for _, v := range ARM64XVariants {
		b1, _ := CompileARM64X(sample(), v)
		b2, _ := CompileARM64X(sample(), v)
		if !bytes.Equal(b1, b2) {
			t.Errorf("%s: two builds differ", v)
		}
		add("arm64x-"+string(v), b1)
	}
	got := strings.Join(lines, "\n") + "\n"
	if *update {
		os.WriteFile(path, []byte(got), 0o644)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update once)", err)
	}
	if string(want) != got {
		t.Errorf("golden digests changed; if intended run go test ./target/windows -update\n got %s\nwant %s", got, want)
	}
}

func TestARM64XUnknownVariant(t *testing.T) {
	if b, r := CompileARM64X(sample(), "nope"); b != nil || !r.HasErrors() {
		t.Error("unknown variant built")
	}
}
