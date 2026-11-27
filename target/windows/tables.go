// Package windows compiles keyboard layouts into Windows keyboard-layout DLLs and reads such DLLs back.
//
// It works on a Windows-shaped intermediate, Tables: the structures of a layout DLL (reference spec
// dll-format §3 to §5) as Go values, one field per table, rows in table order. Compile turns a DLL value into the
// bytes of a DLL for one architecture; Read turns such bytes back into a DLL value. The intermediate is
// deliberately low level: deciding which rows a layout needs (dead-key rows, SGCAPS rows, the number pad) is the
// job of the code that lowers a layout into it, not of this package.
//
// Names of structures, fields and constants are those of the windows-sys crate (see NOTICE).
package windows

// Modifier bits of VK_TO_BIT.ModBits (dll-format §4.1).
const (
	KBDSHIFT     = 0x01
	KBDCTRL      = 0x02
	KBDALT       = 0x04
	KBDKANA      = 0x08
	KBDROYA      = 0x10
	KBDLOYA      = 0x20
	KBDGRPSELTAP = 0x80
)

// SHFT_INVALID is the ModNumber value of a modifier combination without a column.
const SHFT_INVALID = 0x0F

// MaxColumns is the number of usable columns (ModNumber values 0 to 14).
const MaxColumns = 15

// Row attributes of VK_TO_WCHARSn.Attributes (dll-format §4.2).
const (
	CAPLOK      = 0x01
	SGCAPS      = 0x02
	CAPLOKALTGR = 0x04
	KANALOK     = 0x08
	GRPSELTAP   = 0x80
)

// Special cell values (dll-format §4.2).
const (
	WCH_NONE = 0xF000
	WCH_DEAD = 0xF001
	WCH_LGTR = 0xF002
)

// DKF_DEAD marks a dead-key entry whose result is itself a dead character (dll-format §4.3).
const DKF_DEAD = 0x0001

// Locale flags, low word of fLocaleFlags (dll-format §4.8).
const (
	KLLF_ALTGR     = 0x0001
	KLLF_SHIFTLOCK = 0x0002
	KLLF_LRM_RLM   = 0x0004
)

// KBD_VERSION is the version word the tool writes into the high word of fLocaleFlags (dll-format §4.7).
const KBD_VERSION = 1

// VKBit is one VK_TO_BIT entry: a modifier key and the bits it sets.
type VKBit struct {
	VK   byte
	Bits byte
}

// Modifiers is the MODIFIERS structure: the modifier keys and the map from modifier bits to column.
type Modifiers struct {
	VKToBit   []VKBit
	ModNumber []byte // index = ORed bits, value = column or SHFT_INVALID; len = wMaxModBits + 1
}

// Columns returns the number of columns: one more than the highest valid ModNumber value.
func (m Modifiers) Columns() int {
	n := 0
	for _, c := range m.ModNumber {
		if c != SHFT_INVALID && int(c)+1 > n {
			n = int(c) + 1
		}
	}
	return n
}

// CharRow is one VK_TO_WCHARSn row. len(Chars) equals the width of its table.
type CharRow struct {
	VK         byte
	Attributes byte
	Chars      []uint16
}

// CharTable is one VK_TO_WCHAR_TABLE entry with its rows.
type CharTable struct {
	Width int
	Rows  []CharRow
}

// DeadKey is one DEADKEY entry.
type DeadKey struct {
	Dead     uint16 // high word of dwBoth
	Base     uint16 // low word of dwBoth
	Composed uint16
	Flags    uint16
}

// KeyName is one VSC_LPWSTR entry.
type KeyName struct {
	Scan byte
	Name string
}

// DeadName is one entry of pKeyNamesDead: the dead character and its name.
type DeadName struct {
	Dead uint16
	Name string
}

// ScanVK is one VSC_VK entry (E0 and E1 tables).
type ScanVK struct {
	Scan byte
	VK   uint16 // virtual key in the low byte, scan-code flags in the high byte
}

// Ligature is one LIGATUREn entry. Chars has 1 to Tables.LigatureWidth units; the rest is WCH_NONE.
type Ligature struct {
	VK     byte
	Column uint16 // ModificationNumber: the column index (dll-format §4.7)
	Chars  []uint16
}

// Tables is the content of KBDTABLES and everything it points to.
type Tables struct {
	Modifiers    Modifiers
	CharTables   []CharTable
	DeadKeys     []DeadKey
	KeyNames     []KeyName
	KeyNamesExt  []KeyName
	KeyNamesDead []DeadName
	ScanToVK     []uint16 // pusVSCtoVK, indexed by scan code; len = bMaxVSCtoVK
	E0           []ScanVK
	E1           []ScanVK
	LocaleFlags  uint16 // low word of fLocaleFlags
	Version      uint16 // high word of fLocaleFlags
	// LigatureWidth is nLgMax. It is 0 when there are no ligatures.
	LigatureWidth int
	Ligatures     []Ligature
	Type          uint32 // dwType
	SubType       uint32 // dwSubType
}

// String is one string resource.
type String struct {
	ID   uint16
	Lang uint16
	Text string
}

// Layout string ids (dll-format §1.6).
const (
	StringLayoutName   = 1000
	StringLanguageName = 1100
	StringLocaleName   = 1200
)

// VersionInfo is the content of the version resource.
type VersionInfo struct {
	FileVersion    [4]uint16
	ProductVersion [4]uint16
	Lang           uint16      // language of the string table and the translation entry
	Strings        []VersionKV // in the order they are written
}

// VersionKV is one string of the version resource.
type VersionKV struct{ Key, Value string }

// DLL is a whole layout DLL: its tables and resources.
type DLL struct {
	// FileName is the DLL file name with extension, stored in the export directory, such as "kbdtst.dll".
	FileName string
	Tables   Tables
	Strings  []String
	Version  *VersionInfo // nil: no version resource
}
