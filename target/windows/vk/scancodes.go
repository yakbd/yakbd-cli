package vk

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

// Scan-code flags of the scan-code tables (names and values from FreeRDP's WinPR header, see NOTICE; reference
// spec dll-format §4.5).
const (
	KBDEXT        = 0x0100
	KBDMULTIVK    = 0x0200
	KBDSPECIAL    = 0x0400
	KBDNUMPAD     = 0x0800
	KBDUNICODE    = 0x1000
	KBDINJECTEDVK = 0x2000
	KBDMAPPEDVK   = 0x4000
	KBDBREAK      = 0x8000
)

// FlagNames maps the `.kbl.json` names of scan-code flags (SPEC §16.1) to their values, in bit order.
var FlagNames = []struct {
	Name string
	Bit  uint16
}{
	{"ext", KBDEXT}, {"multiVk", KBDMULTIVK}, {"special", KBDSPECIAL}, {"numpad", KBDNUMPAD},
	{"unicode", KBDUNICODE}, {"injectedVk", KBDINJECTEDVK}, {"mappedVk", KBDMAPPEDVK}, {"break", KBDBREAK},
}

// FlagBit returns the value of a scan-code flag name.
func FlagBit(name string) (uint16, bool) {
	for _, f := range FlagNames {
		if f.Name == name {
			return f.Bit, true
		}
	}
	return 0, false
}

// FlagList returns the names of the flags set in v.
func FlagList(v uint16) []string {
	out := []string{}
	for _, f := range FlagNames {
		if v&f.Bit != 0 {
			out = append(out, f.Name)
		}
	}
	return out
}

// Prefix of a scan code.
const (
	PrefixNone = 0x00
	PrefixE0   = 0xE0
	PrefixE1   = 0xE1
)

// Default is one row of Table A: a position, its scan code (prefix in the high byte), its virtual key with the
// scan-code flags in the high byte, and the source of the row.
type Default struct {
	Position string
	Scan     uint16
	VK       uint16
	Source   string
}

// MaxVSC is the default number of entries of the unprefixed table (bMaxVSCtoVK, rule scanCodeTableSize).
const MaxVSC = 0x7F

// tableA is Table A of the `.kbl.json` specification, targets/windows-default-vk.tsv, copied unchanged from
// the specification at tag TableASpecTag. It is the only default table of the tool.
//
//go:embed windows-default-vk.tsv
var tableA string

// TableASpecTag is the specification release tag whose targets/windows-default-vk.tsv the embedded Table A equals
// (TestTableAMatchesSpec; the tag is spec commit ac08c88).
const TableASpecTag = "v0.1.0-preview"

// Defaults is Table A, parsed.
var Defaults []Default

func parseTableA(s string) []Default {
	var out []Default
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "position\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			panic(fmt.Sprintf("windows-default-vk.tsv line %d: %d fields", i+1, len(f)))
		}
		sc, err := strconv.ParseUint(f[1], 16, 16)
		if err != nil {
			panic(fmt.Sprintf("windows-default-vk.tsv line %d: scan code %q", i+1, f[1]))
		}
		v, ok := Parse(f[2])
		if !ok {
			panic(fmt.Sprintf("windows-default-vk.tsv line %d: virtual key %q", i+1, f[2]))
		}
		var flags uint16
		if f[3] != "-" {
			for _, n := range strings.Split(f[3], ",") {
				b, ok := FlagBit(n)
				if !ok {
					panic(fmt.Sprintf("windows-default-vk.tsv line %d: flag %q", i+1, n))
				}
				flags |= b
			}
		}
		out = append(out, Default{Position: f[0], Scan: uint16(sc), VK: uint16(v) | flags, Source: f[4]})
	}
	return out
}

// DefaultFor returns the Table A virtual key (with flags) of a scan code, or false.
func DefaultFor(scan uint16) (uint16, bool) {
	for _, d := range Defaults {
		if d.Scan == scan {
			return d.VK, true
		}
	}
	return 0, false
}

// DefaultForPosition returns the Table A row of a position name, or false.
func DefaultForPosition(name string) (Default, bool) {
	for _, d := range Defaults {
		if d.Position == name {
			return d, true
		}
	}
	return Default{}, false
}

// DefaultFlags returns the scan-code flags of rule scanCodeFlags for any scan code (targets/windows.md §5): ext for
// every E0 code and for 36 and 45; multiVk for 37, 45, 46; numpad and special for the number-pad keys.
func DefaultFlags(scan uint16) uint16 {
	var f uint16
	if scan>>8 == PrefixE0 || scan == 0x36 || scan == 0x45 {
		f |= KBDEXT
	}
	switch scan {
	case 0x37, 0x45, 0x46:
		f |= KBDMULTIVK
	case 0x47, 0x48, 0x49, 0x4B, 0x4C, 0x4D, 0x4F, 0x50, 0x51, 0x52, 0x53:
		f |= KBDNUMPAD | KBDSPECIAL
	}
	return f
}
