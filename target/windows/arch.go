package windows

import (
	"fmt"

	"github.com/yakbd/yakbd-cli/pe"
)

// Arch is a target architecture of a layout DLL.
type Arch int

const (
	// AMD64 is the native 64-bit x64 file for System32.
	AMD64 Arch = iota
	// WOW64 is the 32-bit x86 file with 8-byte pointer slots for SysWOW64 on 64-bit Windows (dll-format §2).
	WOW64
	// X86 is the 32-bit file with 4-byte pointers for 32-bit Windows (dll-format §2, [INF]).
	X86
	// ARM64 is a plain ARM64 file (no x64 view): the control of the ARM64 tests; x64 processes cannot load it
	// (installation §2.4).
	ARM64
	// ARM64X is the file for System32 on Windows on ARM: an ARM64 view and an x64 view in one file (docs/arm64x.md).
	ARM64X
)

// Arches lists the architectures the compiler supports, in a fixed order.
var Arches = []Arch{AMD64, WOW64, X86}

// String returns the command-line name of the architecture.
func (a Arch) String() string {
	switch a {
	case AMD64:
		return "amd64"
	case WOW64:
		return "wow64"
	case X86:
		return "x86"
	case ARM64:
		return "arm64"
	case ARM64X:
		return "arm64x"
	}
	return fmt.Sprintf("arch(%d)", int(a))
}

// ParseArch parses a command-line architecture name.
func ParseArch(s string) (Arch, error) {
	for _, a := range append(Arches, ARM64, ARM64X) {
		if a.String() == s {
			return a, nil
		}
	}
	return 0, fmt.Errorf("unknown architecture %q (want amd64, wow64, x86, arm64 or arm64x)", s)
}

// PointerSize is the size of a pointer slot in the tables.
func (a Arch) PointerSize() int {
	if a == X86 {
		return 4
	}
	return 8
}

// header holds the PE header values of an architecture (dll-format §1.1, §1.2; the Windows 11 columns).
type header struct {
	machine            pe.Machine
	plus               bool
	imageBase          uint64
	sectionAlign       uint32
	fileAlign          uint32
	characteristics    uint16
	dllCharacteristics uint16
	separateText       bool // descriptor code in its own .text section
}

func (a Arch) header() header {
	switch a {
	case AMD64:
		return header{pe.MachineAMD64, true, 0x1_8000_0000, 0x1000, 0x1000,
			pe.FileExecutableImage | pe.FileLargeAddress | pe.FileDLL,
			pe.DllGuardCF | pe.DllNXCompat | pe.DllDynamicBase | pe.DllHighEntropyVA, true}
	default:
		// WOW64 values of dll-format §1.1; a native x86 file has no Windows 11 column and takes the same values.
		return header{pe.MachineI386, false, 0x1000_0000, 0x1000, 0x200,
			pe.FileExecutableImage | pe.File32BitMachine | pe.FileDLL,
			pe.DllGuardCF | pe.DllNoSEH | pe.DllNXCompat | pe.DllDynamicBase, false}
	}
}
