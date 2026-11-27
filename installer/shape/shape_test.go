package shape

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/pe"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// files builds kbdtst.klc for every payload build, plus the shapes that must be refused: a plain ARM64 file and
// the two ARM64X variants yakbd does not ship.
func files(t *testing.T) (good map[string][]byte, other map[string][]byte) {
	t.Helper()
	b, err := os.ReadFile("../../testdata/reference-spec/vm/kbdtst.klc")
	if err != nil {
		t.Fatal(err)
	}
	f, r := klc.Parse(b, "kbdtst.klc")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := klckbl.FromKLC(f, "kbdtst.klc")
	res, r := build.Lower(l, build.Options{ToolVersion: [4]uint16{0, 2, 0, 0}})
	if res == nil {
		t.Fatal(r.Summary())
	}
	comp := func(a windows.Arch) []byte {
		b, r := windows.Compile(res.DLL, a)
		if b == nil {
			t.Fatal(r.Summary())
		}
		return b
	}
	arm := func(v windows.ARM64XVariant) []byte {
		b, r := windows.CompileARM64X(res.DLL, v)
		if b == nil {
			t.Fatal(r.Summary())
		}
		return b
	}
	good = map[string][]byte{
		payload.BuildX86: comp(windows.X86), payload.BuildX64: comp(windows.AMD64), payload.BuildWOW64: comp(windows.WOW64),
		payload.BuildARM64X: arm(windows.ShippedVariant),
	}
	other = map[string][]byte{
		"arm64": comp(windows.ARM64), "arm64x-ec": arm(windows.VariantEC), "arm64x-x64-onecfg": arm(windows.VariantX64OneCfg),
	}
	return good, other
}

var builds = []string{payload.BuildX86, payload.BuildX64, payload.BuildWOW64, payload.BuildARM64X}

// TestGoodAndSwapped: every build's own file passes, and every build's file offered as any other build fails.
func TestGoodAndSwapped(t *testing.T) {
	good, _ := files(t)
	for _, want := range builds {
		for _, have := range builds {
			err := Check(want, good[have])
			if (want == have) != (err == nil) {
				t.Errorf("%s file as %s: %v", have, want, err)
			}
			if err != nil {
				t.Logf("%s file as %s: %v", have, want, err)
			}
		}
	}
}

func TestOtherARM64Shapes(t *testing.T) {
	_, other := files(t)
	for name, b := range other {
		err := Check(payload.BuildARM64X, b)
		if err == nil {
			t.Errorf("%s accepted as arm64x", name)
			continue
		}
		t.Logf("%s: %v", name, err)
	}
}

// TestBroken: damaged copies of good files fail, each with a message that names the build.
func TestBroken(t *testing.T) {
	good, _ := files(t)
	le := binary.LittleEndian
	lfanew := func(b []byte) int { return int(le.Uint32(b[0x3C:])) }
	type mod struct {
		name  string
		build string
		edit  func([]byte) []byte
	}
	mods := []mod{
		{"empty", payload.BuildX64, func(b []byte) []byte { return nil }},
		{"truncated", payload.BuildX64, func(b []byte) []byte { return b[:0x400] }},
		{"not PE", payload.BuildX86, func(b []byte) []byte { b[0] = 'X'; return b }},
		{"x64 Machine on a WOW64 file", payload.BuildX64, func(b []byte) []byte {
			b = append([]byte(nil), good[payload.BuildWOW64]...)
			le.PutUint16(b[lfanew(b)+4:], 0x8664)
			return b
		}},
		{"014C Machine on an x64 file", payload.BuildWOW64, func(b []byte) []byte {
			b = append([]byte(nil), good[payload.BuildX64]...)
			le.PutUint16(b[lfanew(b)+4:], 0x014C)
			return b
		}},
		{"no export directory", payload.BuildWOW64, func(b []byte) []byte {
			f, _ := pe.Parse(b)
			off := f.CheckSumOffset + 32 // PE32: the data directory starts 32 bytes after CheckSum; entry 0 is exports
			clear(b[off : off+8])
			return b
		}},
		{"ARM64X without its load configuration", payload.BuildARM64X, func(b []byte) []byte {
			f, _ := pe.Parse(b)
			off := f.CheckSumOffset + 48 + 8*pe.DirLoadConf // PE32+: 48 bytes after CheckSum
			clear(b[off : off+8])
			return b
		}},
	}
	for _, m := range mods {
		b := m.edit(append([]byte(nil), good[m.build]...))
		err := Check(m.build, b)
		if err == nil {
			t.Errorf("%s: accepted", m.name)
			continue
		}
		if !strings.Contains(err.Error(), "the "+m.build+" file") {
			t.Errorf("%s: message %q does not name the build", m.name, err)
		}
		t.Logf("%s: %v", m.name, err)
	}
	if err := Check("arm32", good[payload.BuildX86]); err == nil {
		t.Error("unknown build accepted")
	}
}
