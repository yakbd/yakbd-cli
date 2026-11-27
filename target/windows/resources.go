package windows

import (
	"encoding/binary"
	"fmt"
	"sort"
	"unicode/utf16"
)

// String tables: a string with id N lives in RT_STRING block N/16 + 1, slot N mod 16; a slot is a WORD length and
// that many UTF-16 units (dll-format §1.6; https://learn.microsoft.com/en-us/windows/win32/menurc/stringtable-resource).

type stringBlock struct {
	block, lang uint16
}

// encodeStrings groups strings into RT_STRING blocks.
func encodeStrings(list []String) (map[stringBlock][]byte, error) {
	slots := map[stringBlock]*[16][]uint16{}
	seen := map[[2]uint16]bool{}
	for _, s := range list {
		k := [2]uint16{s.ID, s.Lang}
		if seen[k] {
			return nil, fmt.Errorf("string %d in language %04x twice", s.ID, s.Lang)
		}
		seen[k] = true
		units := utf16.Encode([]rune(s.Text))
		if len(units) > 0xFFFF {
			return nil, fmt.Errorf("string %d too long", s.ID)
		}
		bk := stringBlock{s.ID/16 + 1, s.Lang}
		if slots[bk] == nil {
			slots[bk] = &[16][]uint16{}
		}
		slots[bk][s.ID%16] = units
	}
	out := map[stringBlock][]byte{}
	for bk, sl := range slots {
		var b []byte
		for _, u := range sl {
			b = binary.LittleEndian.AppendUint16(b, uint16(len(u)))
			for _, c := range u {
				b = binary.LittleEndian.AppendUint16(b, c)
			}
		}
		out[bk] = b
	}
	return out, nil
}

// decodeStrings reads one RT_STRING block.
func decodeStrings(block, lang uint16, data []byte) ([]String, error) {
	var out []String
	p := 0
	for slot := 0; slot < 16; slot++ {
		if p+2 > len(data) {
			return nil, fmt.Errorf("string block %d truncated", block)
		}
		n := int(binary.LittleEndian.Uint16(data[p:]))
		p += 2
		if p+2*n > len(data) {
			return nil, fmt.Errorf("string block %d slot %d truncated", block, slot)
		}
		if n > 0 {
			u := make([]uint16, n)
			for i := range u {
				u[i] = binary.LittleEndian.Uint16(data[p+2*i:])
			}
			out = append(out, String{ID: (block-1)*16 + uint16(slot), Lang: lang, Text: string(utf16.Decode(u))})
		}
		p += 2 * n
	}
	return out, nil
}

// Version resource (https://learn.microsoft.com/en-us/windows/win32/menurc/vs-versioninfo and the pages of
// VS_FIXEDFILEINFO, StringFileInfo, StringTable, String, VarFileInfo and Var). Constants of VS_FIXEDFILEINFO:
// signature FEEF04BD, structure version 1.0, VOS_NT_WINDOWS32 (0x40004), VFT_DRV (3), VFT2_DRV_KEYBOARD (2): the
// file type "driver" with subtype "keyboard" that the public sample uses (dll-format §1.6).
const (
	vsSignature    = 0xFEEF04BD
	vsStrucVersion = 0x00010000
	vsFlagsMask    = 0x3F
	vosNTWindows32 = 0x00040004
	vftDrv         = 3
	vft2Keyboard   = 2
	codePageUTF16  = 0x04B0
)

// vsNode writes one node: wLength, wValueLength, wType, key, padding, value, padding, children.
func vsNode(key string, value []byte, valueLen uint16, text bool, children ...[]byte) []byte {
	var b []byte
	b = append(b, 0, 0) // wLength, filled in below
	b = binary.LittleEndian.AppendUint16(b, valueLen)
	typ := uint16(0)
	if text {
		typ = 1
	}
	b = binary.LittleEndian.AppendUint16(b, typ)
	for _, u := range utf16.Encode([]rune(key)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	b = append(b, 0, 0)
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

func encodeVersion(v *VersionInfo) []byte {
	fixed := make([]byte, 52)
	le := binary.LittleEndian
	le.PutUint32(fixed[0:], vsSignature)
	le.PutUint32(fixed[4:], vsStrucVersion)
	le.PutUint32(fixed[8:], uint32(v.FileVersion[0])<<16|uint32(v.FileVersion[1]))
	le.PutUint32(fixed[12:], uint32(v.FileVersion[2])<<16|uint32(v.FileVersion[3]))
	le.PutUint32(fixed[16:], uint32(v.ProductVersion[0])<<16|uint32(v.ProductVersion[1]))
	le.PutUint32(fixed[20:], uint32(v.ProductVersion[2])<<16|uint32(v.ProductVersion[3]))
	le.PutUint32(fixed[24:], vsFlagsMask)
	le.PutUint32(fixed[28:], 0)
	le.PutUint32(fixed[32:], vosNTWindows32)
	le.PutUint32(fixed[36:], vftDrv)
	le.PutUint32(fixed[40:], vft2Keyboard)
	var strs [][]byte
	for _, kv := range v.Strings {
		val := utf16z(kv.Value)
		strs = append(strs, vsNode(kv.Key, val, uint16(len(val)/2), true))
	}
	table := vsNode(fmt.Sprintf("%04X%04X", v.Lang, codePageUTF16), nil, 0, true, strs...)
	sfi := vsNode("StringFileInfo", nil, 0, true, table)
	trans := make([]byte, 4)
	le.PutUint16(trans[0:], v.Lang)
	le.PutUint16(trans[2:], codePageUTF16)
	vfi := vsNode("VarFileInfo", nil, 0, true, vsNode("Translation", trans, 4, false))
	return vsNode("VS_VERSION_INFO", fixed, 52, false, sfi, vfi)
}

// node is a parsed version-resource node.
type vsParsed struct {
	key      string
	value    []byte
	text     bool
	children []vsParsed
}

func parseVS(b []byte) (vsParsed, int, error) {
	le := binary.LittleEndian
	if len(b) < 6 {
		return vsParsed{}, 0, fmt.Errorf("version node truncated")
	}
	length := int(le.Uint16(b))
	vlen := int(le.Uint16(b[2:]))
	typ := le.Uint16(b[4:])
	if length < 6 || length > len(b) {
		return vsParsed{}, 0, fmt.Errorf("version node length %d", length)
	}
	b = b[:length]
	p := 6
	var key []uint16
	for {
		if p+2 > len(b) {
			return vsParsed{}, 0, fmt.Errorf("version key unterminated")
		}
		u := le.Uint16(b[p:])
		p += 2
		if u == 0 {
			break
		}
		key = append(key, u)
	}
	for p%4 != 0 {
		p++
	}
	n := vsParsed{key: string(utf16.Decode(key)), text: typ == 1}
	vbytes := vlen
	if typ == 1 {
		vbytes = 2 * vlen
	}
	if p+vbytes > len(b) {
		return vsParsed{}, 0, fmt.Errorf("version value of %q beyond its node", n.key)
	}
	n.value = b[p : p+vbytes]
	p += vbytes
	for {
		for p%4 != 0 {
			p++
		}
		if p >= len(b) {
			break
		}
		c, used, err := parseVS(b[p:])
		if err != nil {
			return vsParsed{}, 0, err
		}
		n.children = append(n.children, c)
		p += used
	}
	return n, length, nil
}

func decodeVersion(b []byte) (*VersionInfo, error) {
	root, _, err := parseVS(b)
	if err != nil {
		return nil, err
	}
	if root.key != "VS_VERSION_INFO" || len(root.value) != 52 {
		return nil, fmt.Errorf("not a version resource")
	}
	le := binary.LittleEndian
	f := root.value
	if le.Uint32(f) != vsSignature {
		return nil, fmt.Errorf("version signature %#x", le.Uint32(f))
	}
	v := &VersionInfo{}
	split := func(ms, ls uint32) [4]uint16 {
		return [4]uint16{uint16(ms >> 16), uint16(ms), uint16(ls >> 16), uint16(ls)}
	}
	v.FileVersion = split(le.Uint32(f[8:]), le.Uint32(f[12:]))
	v.ProductVersion = split(le.Uint32(f[16:]), le.Uint32(f[20:]))
	for _, c := range root.children {
		switch c.key {
		case "StringFileInfo":
			for _, tbl := range c.children {
				var lang, cp uint32
				fmt.Sscanf(tbl.key, "%04X%04X", &lang, &cp)
				v.Lang = uint16(lang)
				for _, s := range tbl.children {
					u := make([]uint16, 0, len(s.value)/2)
					for i := 0; i+1 < len(s.value); i += 2 {
						if w := le.Uint16(s.value[i:]); w != 0 {
							u = append(u, w)
						} else {
							break
						}
					}
					v.Strings = append(v.Strings, VersionKV{s.key, string(utf16.Decode(u))})
				}
			}
		}
	}
	return v, nil
}

func sortedBlocks(m map[stringBlock][]byte) []stringBlock {
	var keys []stringBlock
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].block != keys[j].block {
			return keys[i].block < keys[j].block
		}
		return keys[i].lang < keys[j].lang
	})
	return keys
}
