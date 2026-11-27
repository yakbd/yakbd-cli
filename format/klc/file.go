// Package klc reads and writes `.klc` files, the layout source format of MSKLC 1.4, as the reference
// specification `spec/klc-format` (revision 10) describes it.
//
// Parse turns the bytes of a file into a File, a faithful in-memory form of its sections, and a loss.Report with
// every diagnostic (each with its line). Write turns a File back into the canonical form of §7. The package knows
// nothing about DLLs; lowering a File into a Windows layout is done elsewhere.
package klc

// File is a parsed `.klc` file. Optional single-value fields are nil when the file does not have them.
type File struct {
	Name        string // KBD name, 1 to 8 characters
	Description string // KBD description

	Version    *string
	Copyright  *string
	Company    *string
	LocaleName *string
	LocaleID   *string

	Modifiers   []Modifier
	ShiftStates []int // SHIFTSTATE values; the position is the column
	Attributes  []string

	Keys      []Key
	Ligatures []Ligature
	DeadKeys  []DeadKey

	KeyNames     []KeyName
	KeyNamesExt  []KeyName
	KeyNamesDead []DeadName

	Descriptions  []LangText
	LanguageNames []LangText

	EndKBD bool // the file ends with ENDKBD
}

// Modifier is one MODIFIERS entry: a virtual key and the name of the bit constant it owns.
type Modifier struct {
	VK   byte
	Bit  string // KBDKANA, KBDROYA, KBDLOYA or KBDGRPSELTAP
	Line int
}

// Cap bits of the cap column (§4.3).
const (
	CapCAPLOK      = 1
	CapSGCAPS      = 2
	CapCAPLOKALTGR = 4
)

// Key is one LAYOUT row with its SGCAP continuation line.
type Key struct {
	Line     int
	Scan     uint16 // with the E0 or E1 prefix in the high byte
	VK       byte
	HasCells bool // false for a row with only sc and vk
	Cap      byte
	Cells    []Cell // one per SHIFTSTATE column, at most; missing columns have no character
	// SGCap holds the cells of the continuation line (Caps Lock, Caps Lock + Shift) when Cap has CapSGCAPS.
	SGCap     []Cell
	SGCapLine int
}

// CellKind says what a character cell holds.
type CellKind int

const (
	// CellNone is -1: no character.
	CellNone CellKind = iota
	// CellChar is one character: a UTF-16 unit, or a supplementary character (written hhhhh or hhhhhh) that
	// becomes a two-unit ligature.
	CellChar
	// CellDead is a dead key whose dead character is Rune.
	CellDead
	// CellLigature is %%, c% or hhhh%: the characters are in the LIGATURE line for the key and column.
	CellLigature
)

// Cell is one character cell.
type Cell struct {
	Kind CellKind
	Rune rune // the unit or supplementary character; for CellLigature the unit before % (0 for %%)
}

// Ligature is one LIGATURE line.
type Ligature struct {
	VK     byte
	Column int // 0-based column index, the position in SHIFTSTATE
	Chars  []uint16
	Line   int
}

// DeadKey is one DEADKEY block (blocks for the same dead character are merged).
type DeadKey struct {
	Dead    uint16
	Entries []DeadEntry
	Line    int
}

// DeadEntry is one base/result pair of a DEADKEY block.
type DeadEntry struct {
	Base    uint16
	Result  uint16
	Chained bool // the result carries @: DKF_DEAD
	Line    int
}

// KeyName is one KEYNAME or KEYNAME_EXT line.
type KeyName struct {
	Code uint16
	Name string
}

// DeadName is one KEYNAME_DEAD line.
type DeadName struct {
	Dead uint16
	Name string
}

// LangText is one DESCRIPTIONS or LANGUAGENAMES line.
type LangText struct {
	LangID uint16
	Text   string
}

// Attribute names (§5) and their KLLF_* values.
var attributeFlags = map[string]uint16{"ALTGR": 1, "SHIFTLOCK": 2, "LRM_RLM": 4}

// AttributeFlags returns the KLLF_* flags of the ATTRIBUTES section.
func (f *File) AttributeFlags() uint16 {
	var v uint16
	for _, a := range f.Attributes {
		v |= attributeFlags[a]
	}
	return v
}

// Modifier bit names (§3.1) and their values (windows-sys).
var modifierBits = map[string]byte{"KBDKANA": 0x08, "KBDROYA": 0x10, "KBDLOYA": 0x20, "KBDGRPSELTAP": 0x80}

// ModifierBit returns the bit value of a MODIFIERS bit name.
func ModifierBit(name string) (byte, bool) {
	b, ok := modifierBits[name]
	return b, ok
}
