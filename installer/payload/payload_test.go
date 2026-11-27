package payload

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func pe(machine uint16) []byte {
	b := make([]byte, 0x100)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3C:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x44:], machine)
	return b
}

func manifest() Manifest {
	return Manifest{Tool: "t", Product: Product{Name: "N", Version: "1.0.0.0", Code: "{01234567-89AB-CDEF-0123-456789ABCDEF}"},
		Layout: Layout{File: "kbdtst.dll", LangID: "0409", LayoutText: "N"}}
}

func TestRoundTrip(t *testing.T) {
	data := map[string][]byte{BuildX64: pe(MachineAMD64), BuildWOW64: append(pe(MachineI386), 1, 2, 3), BuildX86: pe(MachineI386)}
	enc, err := Encode(manifest(), []string{BuildX64, BuildWOW64, BuildX86}, data)
	if err != nil {
		t.Fatal(err)
	}
	enc2, _ := Encode(manifest(), []string{BuildX64, BuildWOW64, BuildX86}, data)
	if !bytes.Equal(enc, enc2) {
		t.Error("not deterministic")
	}
	// Trailing bytes (a signature) are ignored.
	p, err := Decode(append(append([]byte(nil), enc...), "SIGNATURE"...))
	if err != nil {
		t.Fatal(err)
	}
	for b, d := range data {
		if !bytes.Equal(p.Data[b], d) {
			t.Errorf("%s differs", b)
		}
	}
	if strings.Join(p.Manifest.Builds(), ",") != "x64,wow64,x86" || p.Manifest.Files[1].Offset != 0x100 {
		t.Errorf("%+v", p.Manifest.Files)
	}
	for _, c := range []struct {
		name string
		mod  func([]byte) []byte
	}{
		{"magic", func(b []byte) []byte { b[0] = 'X'; return b }},
		{"version", func(b []byte) []byte { b[8] = 9; return b }},
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }},
		{"checksum", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }},
	} {
		if _, err := Decode(c.mod(append([]byte(nil), enc...))); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestEncodeChecks(t *testing.T) {
	wrong := map[string][]byte{BuildX64: pe(MachineI386)}
	enc, err := Encode(manifest(), []string{BuildX64}, wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(enc); err == nil {
		t.Error("a file with the wrong machine was accepted")
	}
	for _, mod := range []func(*Manifest){
		func(m *Manifest) { m.Product.Code = "{01234567-89ab-CDEF-0123-456789ABCDEF}" },
		func(m *Manifest) { m.Layout.File = "1abc.dll" },
		func(m *Manifest) { m.Layout.File = "kbd-x.dll" },
		func(m *Manifest) { m.Layout.LangID = "409" },
		func(m *Manifest) { m.Layout.KLID = "a000040" },
		func(m *Manifest) { m.Layout.LayoutText = "" },
	} {
		m := manifest()
		mod(&m)
		if _, err := Encode(m, nil, nil); err == nil {
			t.Errorf("accepted %+v", m)
		}
	}
	if _, err := Encode(manifest(), []string{"arm32"}, map[string][]byte{"arm32": pe(1)}); err == nil {
		t.Error("unknown build accepted")
	}
}

func TestImageEnd(t *testing.T) {
	b := make([]byte, 0x400)
	copy(b, pe(MachineI386))
	binary.LittleEndian.PutUint16(b[0x46:], 2)    // sections
	binary.LittleEndian.PutUint16(b[0x54:], 0xE0) // optional header size
	sec := 0x40 + 24 + 0xE0
	binary.LittleEndian.PutUint32(b[sec+16:], 0x100)
	binary.LittleEndian.PutUint32(b[sec+20:], 0x200)
	binary.LittleEndian.PutUint32(b[sec+40+16:], 0x80)
	binary.LittleEndian.PutUint32(b[sec+40+20:], 0x300)
	end, err := ImageEnd(b)
	if err != nil || end != 0x380 {
		t.Fatalf("%#x %v", end, err)
	}
	enc, _ := Encode(manifest(), nil, nil)
	exe := append(b[:0x380:0x380], enc...)
	if _, err := FromExe(append(exe, "signature"...)); err != nil {
		t.Fatal(err)
	}
}
