// Package setup is the installer's logic: which files go where, the KLID and Layout Id, the registry values, the
// uninstall entry, upgrade and repair, enabling for the user and uninstall (docs/installer.md §1–§4, from the
// reference specification's installation topic). It talks to Windows only through System, so that every decision
// is tested on any OS against a fake; installer/stub implements System with the Windows API. It imports nothing but
// the standard library and installer/payload, because it is compiled into the stub.
package setup

import "errors"

// Registry value types (the Windows API's REG_* numbers).
const (
	RegSZ       uint32 = 1
	RegExpandSZ uint32 = 2
	RegDWORD    uint32 = 4
	RegMultiSZ  uint32 = 7
)

// InstallLayoutOrTip flags (inst §4.3).
const (
	ILOTUninstall uint32 = 0x01
)

// Value is one registry value. Str holds REG_SZ and REG_EXPAND_SZ data (REG_MULTI_SZ: the strings joined by NUL),
// DWord REG_DWORD data.
type Value struct {
	Name  string
	Type  uint32
	Str   string
	DWord uint32
}

// DirKind names a system directory.
type DirKind int

// The two system directories (inst §2.1).
const (
	DirNative DirKind = iota // System32 as the native processes see it
	DirWOW64                 // SysWOW64
)

func (d DirKind) String() string {
	if d == DirWOW64 {
		return "SysWOW64"
	}
	return "System32"
}

// ErrNotFound is returned for a missing registry key or file.
var ErrNotFound = errors.New("not found")

// ErrInUse is returned when a file cannot be replaced or deleted because a session has it loaded (inst §5).
var ErrInUse = errors.New("file in use")

// System is what the installer needs from Windows. Registry paths start with `HKLM\` or `HKU\`; HKLM is always
// the 64-bit view.
type System interface {
	// NativeMachine is the native machine of IsWow64Process2 (inst §2.2), a PE Machine value.
	NativeMachine() (uint16, error)
	Elevated() bool
	// UserSID is the SID string of the process's user (its hive is HKU\<SID>).
	UserSID() (string, error)

	// Dir is the path through which this process reaches a system directory (inst §2.2).
	Dir(k DirKind) (string, error)
	ProgramFiles() (string, error)
	TempDir() string
	SelfPath() string
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	// Rename moves a file, replacing the target.
	Rename(from, to string) error
	Remove(path string) error
	MkdirAll(path string) error
	RemoveDir(path string) error
	// DeleteAtReboot marks a file or an empty directory for deletion at the next restart; MoveAtReboot moves
	// from over to at the next restart (MoveFileExW with MOVEFILE_DELAY_UNTIL_REBOOT, inst §5, §10).
	DeleteAtReboot(path string) error
	MoveAtReboot(from, to string) error

	// CreateKey creates a key; created is false when it existed.
	CreateKey(path string) (created bool, err error)
	// DeleteKey deletes a key without subkeys; a missing key is not an error.
	DeleteKey(path string) error
	SubKeys(path string) ([]string, error)
	Values(path string) ([]Value, error)
	SetString(path, name, value string, expand bool) error
	SetDWORD(path, name string, v uint32) error
	DeleteValue(path, name string) error

	// InstallLayoutOrTip and InstallLayoutOrTipUserReg call input.dll (inst §4.3); an error means FALSE.
	// AddLanguageTag and RemoveLanguageTag change the language list of the process's user by read-modify-write
	// (Get-WinUserLanguageList, Set-WinUserLanguageList; inst section 4.4).
	AddLanguageTag(tag string) error
	RemoveLanguageTag(tag string) error
	// SetMultiString writes a REG_MULTI_SZ value.
	SetMultiString(path, name string, values []string) error
	InstallLayoutOrTip(s string, flags uint32) error
	InstallLayoutOrTipUserReg(hive, s string, flags uint32) error
	// DefaultProfileHive is the path of the Default profile's hive file (NTUSER.DAT of the folder
	// GetDefaultUserProfileDirectoryW names). LoadHive loads a hive file under HKU\<name> (RegLoadKeyW, with the
	// backup and restore privileges); UnloadHive unloads it (RegUnLoadKeyW). Used for the Default profile (kbl
	// targets/windows-install.md §7.1).
	DefaultProfileHive() (string, error)
	LoadHive(name, file string) error
	UnloadHive(name string) error
	// LayoutList is GetKeyboardLayoutList of this process (HKLs as 32-bit values).
	LayoutList() ([]uint32, error)
}
