// Package pe writes and reads PE/COFF images (Windows DLLs) with no knowledge of what they contain.
//
// The writer takes sections of raw bytes with symbolic fixups (pointer slots that refer to a place in a
// section), an export list and a resource tree. It lays the sections out, resolves every fixup, writes one base
// relocation per absolute pointer, builds the export directory, the resource section and the relocation section,
// and computes the checksum. The reader parses what the writer writes, and ordinary DLLs of the same shape.
//
// Format source: the public "PE Format" specification,
// https://learn.microsoft.com/en-us/windows/win32/debug/pe-format (read 2026-11-22).
package pe

// Machine is the COFF machine type.
type Machine uint16

// Machine types (PE Format, "Machine Types").
const (
	MachineI386  Machine = 0x014C
	MachineAMD64 Machine = 0x8664
	MachineARM64 Machine = 0xAA64
)

// COFF characteristics (PE Format, "Characteristics").
const (
	FileExecutableImage = 0x0002
	FileLargeAddress    = 0x0020
	File32BitMachine    = 0x0100
	FileDLL             = 0x2000
)

// DLL characteristics (PE Format, "DLL Characteristics").
const (
	DllHighEntropyVA = 0x0020
	DllDynamicBase   = 0x0040
	DllNXCompat      = 0x0100
	DllNoSEH         = 0x0400
	DllGuardCF       = 0x4000
)

// Section characteristics (PE Format, "Section Flags").
const (
	SecCode         = 0x0000_0020
	SecInitData     = 0x0000_0040
	SecDiscardable  = 0x0200_0000
	SecMemExecute   = 0x2000_0000
	SecMemRead      = 0x4000_0000
	SecMemWrite     = 0x8000_0000
	SecReadOnlyData = SecInitData | SecMemRead
)

// Base relocation types (PE Format, "Base Relocation Types").
const (
	RelAbsolute = 0
	RelHighLow  = 3
	RelDir64    = 10
)

// Data directory indexes (PE Format, "Optional Header Data Directories").
const (
	DirExport    = 0
	DirImport    = 1
	DirResource  = 2
	DirException = 3
	DirBaseReloc = 5
	DirDebug     = 6
	DirLoadConf  = 10
	NumDirs      = 16
)

// Subsystem values.
const (
	SubsystemNative = 1
)

// Resource types (Win32 resource type ids, https://learn.microsoft.com/en-us/windows/win32/menurc/resource-types).
const (
	RTString  = 6
	RTVersion = 16
)
