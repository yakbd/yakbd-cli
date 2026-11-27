package exeres

import (
	"encoding/binary"
	"strconv"
	"strings"
	"unicode/utf16"
)

// VS_FIXEDFILEINFO constants (https://learn.microsoft.com/en-us/windows/win32/api/verrsrc/ns-verrsrc-vs_fixedfileinfo):
// signature, structure version 1.0, VOS_NT_WINDOWS32, VFT_APP.
const (
	vsSignature    = 0xFEEF04BD
	vsStrucVersion = 0x00010000
	vsFlagsMask    = 0x3F
	vosNTWindows32 = 0x00040004
	vftApp         = 1
	codePageUTF16  = 0x04B0
)

// Numbers reads up to four dot-separated numbers from the start of a free-text version ("1.2", "0.2.0.0",
// "2.1-beta" gives 2.1.0.0); parts that do not parse or exceed 65535 end the reading, the rest is 0.
func Numbers(v string) [4]uint16 {
	var out [4]uint16
	parts := strings.SplitN(v, ".", 4)
	for i, p := range parts {
		j := 0
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		n, err := strconv.ParseUint(p[:j], 10, 16)
		if err != nil {
			break
		}
		out[i] = uint16(n)
		if j < len(p) {
			break
		}
	}
	return out
}

// Strings gives the StringFileInfo entries in the order they are written; CompanyName only when there is a
// publisher (never another vendor's name, kbl windows-install.md §2).
func Strings(p Product) [][2]string {
	var s [][2]string
	if p.Publisher != "" {
		s = append(s, [2]string{"CompanyName", p.Publisher})
	}
	s = append(s,
		[2]string{"FileDescription", p.Name + " setup"},
		[2]string{"FileVersion", p.Version},
		[2]string{"InternalName", "setup"},
		[2]string{"OriginalFilename", p.FileName},
		[2]string{"ProductName", p.Name},
		[2]string{"ProductVersion", p.Version},
	)
	return s
}

// Version encodes the version resource of an installer: the numeric file and product version from Numbers of the
// product version, file type application, and the strings of Strings in one US English / UTF-16 table.
func Version(p Product) []byte {
	n := Numbers(p.Version)
	fixed := make([]byte, 52)
	le := binary.LittleEndian
	le.PutUint32(fixed[0:], vsSignature)
	le.PutUint32(fixed[4:], vsStrucVersion)
	for _, at := range []int{8, 16} { // file version, product version
		le.PutUint32(fixed[at:], uint32(n[0])<<16|uint32(n[1]))
		le.PutUint32(fixed[at+4:], uint32(n[2])<<16|uint32(n[3]))
	}
	le.PutUint32(fixed[24:], vsFlagsMask)
	le.PutUint32(fixed[32:], vosNTWindows32)
	le.PutUint32(fixed[36:], vftApp)
	var strs [][]byte
	for _, kv := range Strings(p) {
		v := utf16z(kv[1])
		strs = append(strs, vsNode(kv[0], v, uint16(len(v)/2), true))
	}
	table := vsNode("040904B0", nil, 0, true, strs...)
	sfi := vsNode("StringFileInfo", nil, 0, true, table)
	trans := make([]byte, 4)
	le.PutUint16(trans[0:], LangEnglishUS)
	le.PutUint16(trans[2:], codePageUTF16)
	vfi := vsNode("VarFileInfo", nil, 0, true, vsNode("Translation", trans, 4, false))
	return vsNode("VS_VERSION_INFO", fixed, 52, false, sfi, vfi)
}

// vsNode writes one node: wLength, wValueLength, wType, the key, padding to 32 bits, the value, padding, children.
func vsNode(key string, value []byte, valueLen uint16, text bool, children ...[]byte) []byte {
	b := []byte{0, 0}
	b = binary.LittleEndian.AppendUint16(b, valueLen)
	typ := uint16(0)
	if text {
		typ = 1
	}
	b = binary.LittleEndian.AppendUint16(b, typ)
	b = append(b, utf16z(key)...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	b = append(b, value...)
	for _, c := range children {
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
		b = append(b, c...)
	}
	binary.LittleEndian.PutUint16(b, uint16(len(b)))
	return b
}

func utf16z(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return append(b, 0, 0)
}
