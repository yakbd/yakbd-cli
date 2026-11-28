// Package model is the format-neutral in-memory layout: the hub every format reader writes into and every writer
// and compiler reads from. Its types follow the `.kbl.json` format 0.2 (kbl-spec SPEC.md and
// schema/0.2/kbl.schema.json, which holds 0.1) one to one, so that a `.kbl.json` file decodes into it and encodes back with the
// same meaning. Union types of the format (Binding, MarkedText, TextOrUnits, ...) have their own JSON methods in
// json.go. The semantic rules of the format (resolution, matching) are in resolve.go.
package model

import "encoding/json"

// FormatVersion is the lowest `kbl` value this package implements: what a converter writes into a new layout. A
// writer raises it to the lowest version that holds the layout (MinVersion, SPEC §1.1).
const FormatVersion = "0.1"

// Layout is one `.kbl.json` file (SPEC §3).
type Layout struct {
	Schema          string                     `json:"$schema,omitempty"`
	Kbl             string                     `json:"kbl"`
	Metadata        Metadata                   `json:"metadata"`
	Settings        *Settings                  `json:"settings,omitempty"`
	Positions       map[string]*Position       `json:"positions,omitempty"`
	Forms           map[string]*Form           `json:"forms,omitempty"`
	CustomModifiers map[string]*CustomModifier `json:"customModifiers,omitempty"`
	Keys            map[string]*Key            `json:"keys,omitempty"`
	Hardware        *Hardware                  `json:"hardware,omitempty"`
	Touch           []*TouchLayerSet           `json:"touch,omitempty"`
	DeadKeys        map[string]*DeadKey        `json:"deadKeys,omitempty"`
	Flicks          map[string]*Flick          `json:"flicks,omitempty"`
	Displays        *Displays                  `json:"displays,omitempty"`
	Variables       []*Variable                `json:"variables,omitempty"`
	Transforms      *Transforms                `json:"transforms,omitempty"`
	Tests           []*TestFile                `json:"tests,omitempty"`
	Targets         *Targets                   `json:"targets,omitempty"`
	OnLoss          *OnLoss                    `json:"onLoss,omitempty"`
	Extensions      map[string]json.RawMessage `json:"extensions,omitempty"`
	CLDRSpecial     []string                   `json:"cldrSpecial,omitempty"`
}

// Metadata is SPEC §4.
type Metadata struct {
	ID             string            `json:"id"`
	Locales        []string          `json:"locales,omitempty"`
	Name           string            `json:"name"`
	LocalizedNames map[string]string `json:"localizedNames,omitempty"`
	Description    string            `json:"description,omitempty"`
	Authors        []Author          `json:"authors,omitempty"`
	Attribution    string            `json:"attribution,omitempty"`
	LayoutPattern  string            `json:"layoutPattern,omitempty"`
	Indicator      string            `json:"indicator,omitempty"`
	Version        string            `json:"version,omitempty"`
	License        string            `json:"license,omitempty"`
	Copyright      string            `json:"copyright,omitempty"`
	Organization   string            `json:"organization,omitempty"`
	Draft          string            `json:"draft,omitempty"`
	Provenance     *Provenance       `json:"provenance,omitempty"`
}

// Author is one entry of metadata.authors.
type Author struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Provenance is metadata.provenance (informative).
type Provenance struct {
	Source         string   `json:"source,omitempty"`
	Format         string   `json:"format,omitempty"`
	CLDRConformsTo int      `json:"cldrConformsTo,omitempty"`
	CLDRImports    []string `json:"cldrImports,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}

// Settings is SPEC §5. Nil pointers mean the default.
type Settings struct {
	Normalization             string `json:"normalization,omitempty"`
	CapsLockFallback          *bool  `json:"capsLockFallback,omitempty"`
	CapsLockOffBy             string `json:"capsLockOffBy,omitempty"`
	BidiMarksOnShiftBackspace *bool  `json:"bidiMarksOnShiftBackspace,omitempty"`
}

// Position is one physical key (SPEC §6.1).
type Position struct {
	ScanCode    string `json:"scanCode,omitempty"`
	Name        string `json:"name,omitempty"`
	NumLock     *bool  `json:"numLock,omitempty"`
	Description string `json:"description,omitempty"`
}

// Form is a hardware arrangement (SPEC §6.2).
type Form struct {
	Rows        [][]string `json:"rows"`
	CLDRSpecial []string   `json:"cldrSpecial,omitempty"`
}

// CustomModifier is SPEC §7.2.
type CustomModifier struct {
	Kind        string   `json:"kind"` // held or lock
	Positions   []string `json:"positions"`
	Fallback    *bool    `json:"fallback,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Key is one entry of the bag of keys (SPEC §8).
type Key struct {
	Output                *MarkedText `json:"output,omitempty"`
	DeadKey               string      `json:"deadKey,omitempty"`
	Gap                   bool        `json:"gap,omitempty"`
	Width                 *float64    `json:"width,omitempty"`
	Stretch               bool        `json:"stretch,omitempty"`
	LayerID               string      `json:"layerId,omitempty"`
	FlickID               string      `json:"flickId,omitempty"`
	LongPressKeyIDs       []string    `json:"longPressKeyIds,omitempty"`
	LongPressDefaultKeyID string      `json:"longPressDefaultKeyId,omitempty"`
	MultiTapKeyIDs        []string    `json:"multiTapKeyIds,omitempty"`
	Function              string      `json:"function,omitempty"`
}

// ModifierSet is an array of modifier components (SPEC §7.1); empty = none, ["other"] = other.
type ModifierSet []string

// Hardware is SPEC §9.1.
type Hardware struct {
	FormID         string           `json:"formId,omitempty"`
	MinDeviceWidth int              `json:"minDeviceWidth,omitempty"`
	Layers         []*HardwareLayer `json:"layers"`
	CLDRSpecial    []string         `json:"cldrSpecial,omitempty"`
}

// HardwareLayer is one hardware layer.
type HardwareLayer struct {
	ID          string              `json:"id,omitempty"`
	Modifiers   []ModifierSet       `json:"modifiers"`
	Bindings    map[string]*Binding `json:"bindings"`
	CLDRSpecial []string            `json:"cldrSpecial,omitempty"`
}

// TouchLayerSet is SPEC §9.4.
type TouchLayerSet struct {
	MinDeviceWidth int           `json:"minDeviceWidth,omitempty"`
	Layers         []*TouchLayer `json:"layers"`
	CLDRSpecial    []string      `json:"cldrSpecial,omitempty"`
}

// TouchLayer is one touch layer.
type TouchLayer struct {
	ID          string        `json:"id"`
	Modifiers   []ModifierSet `json:"modifiers,omitempty"`
	Rows        [][]string    `json:"rows"`
	CLDRSpecial []string      `json:"cldrSpecial,omitempty"`
}

// DeadKey is SPEC §10.
type DeadKey struct {
	Spacing TextOrUnits    `json:"spacing"`
	Name    string         `json:"name,omitempty"`
	Entries []DeadKeyEntry `json:"entries"`
	NoMatch string         `json:"noMatch,omitempty"`
	Display *MarkedText    `json:"display,omitempty"`
}

// DeadKeyEntry is one composition.
type DeadKeyEntry struct {
	Base   Value `json:"base"`
	Result Value `json:"result"`
}

// Flick is SPEC §11.1.
type Flick struct {
	Segments    []FlickSegment `json:"segments"`
	CLDRSpecial []string       `json:"cldrSpecial,omitempty"`
}

// FlickSegment is one flick path.
type FlickSegment struct {
	Directions []string `json:"directions"`
	KeyID      string   `json:"keyId"`
}

// Displays is SPEC §12.
type Displays struct {
	Entries       []DisplayEntry `json:"entries,omitempty"`
	BaseCharacter string         `json:"baseCharacter,omitempty"`
	CLDRSpecial   []string       `json:"cldrSpecial,omitempty"`
}

// DisplayEntry is one displays entry.
type DisplayEntry struct {
	Output  *MarkedText `json:"output,omitempty"`
	KeyID   string      `json:"keyId,omitempty"`
	Display MarkedText  `json:"display"`
}

// Variable is SPEC §13.1.
type Variable struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Transforms is SPEC §13.2.
type Transforms struct {
	Simple      []*TransformGroup      `json:"simple,omitempty"`
	Backspace   []*TransformGroup      `json:"backspace,omitempty"`
	CLDRSpecial *TransformsCLDRSpecial `json:"cldrSpecial,omitempty"`
}

// TransformsCLDRSpecial keeps the special elements of the transforms elements.
type TransformsCLDRSpecial struct {
	Simple    []string `json:"simple,omitempty"`
	Backspace []string `json:"backspace,omitempty"`
}

// TransformGroup is a group of transforms or of reorders.
type TransformGroup struct {
	Transforms  []Transform `json:"transforms,omitempty"`
	Reorders    []Reorder   `json:"reorders,omitempty"`
	CLDRSpecial []string    `json:"cldrSpecial,omitempty"`
}

// Transform is one transform (CLDR syntax, verbatim).
type Transform struct {
	From string  `json:"from"`
	To   *string `json:"to,omitempty"`
}

// Reorder is one reorder; the numeric fields are an integer or an array of integers, kept as JSON.
type Reorder struct {
	From         string          `json:"from"`
	Before       string          `json:"before,omitempty"`
	Order        json.RawMessage `json:"order,omitempty"`
	Tertiary     json.RawMessage `json:"tertiary,omitempty"`
	TertiaryBase json.RawMessage `json:"tertiaryBase,omitempty"`
	PreBase      json.RawMessage `json:"preBase,omitempty"`
}

// TestFile is SPEC §15.
type TestFile struct {
	Info        TestInfo     `json:"info"`
	Repertoires []Repertoire `json:"repertoires,omitempty"`
	Suites      []*Suite     `json:"suites,omitempty"`
	CLDRSpecial []string     `json:"cldrSpecial,omitempty"`
}

// TestInfo is a test file's info.
type TestInfo struct {
	Name     string `json:"name"`
	Author   string `json:"author,omitempty"`
	Keyboard string `json:"keyboard,omitempty"`
}

// Repertoire is one repertoire test.
type Repertoire struct {
	Name  string `json:"name"`
	Chars string `json:"chars"`
	Type  string `json:"type,omitempty"`
}

// Suite is a test suite.
type Suite struct {
	Name        string   `json:"name"`
	Tests       []*Test  `json:"tests"`
	CLDRSpecial []string `json:"cldrSpecial,omitempty"`
}

// Test is one test.
type Test struct {
	Name         string   `json:"name"`
	StartContext *string  `json:"startContext,omitempty"`
	Steps        []Step   `json:"steps"`
	CLDRSpecial  []string `json:"cldrSpecial,omitempty"`
}

// Step is one test step; exactly one field is set.
type Step struct {
	Keystroke *Keystroke `json:"keystroke,omitempty"`
	Emit      *string    `json:"emit,omitempty"`
	Backspace bool       `json:"backspace,omitempty"`
	Check     *string    `json:"check,omitempty"`
}

// Keystroke is a CLDR keystroke (Key) or a kbl hardware keystroke (Position).
type Keystroke struct {
	Key       string   `json:"key,omitempty"`
	Flick     []string `json:"flick,omitempty"`
	LongPress *int     `json:"longPress,omitempty"`
	TapCount  int      `json:"tapCount,omitempty"`
	Position  string   `json:"position,omitempty"`
	Modifiers []string `json:"modifiers,omitempty"`
	Locks     []string `json:"locks,omitempty"`
}

// Targets is SPEC §16.
type Targets struct {
	Windows *WindowsTarget `json:"windows,omitempty"`
	KLC     *KLCTarget     `json:"klc,omitempty"`
	CLDR    *CLDRTarget    `json:"cldr,omitempty"`
	// MacOS and XKB are new in 0.2 (SPEC §16.4, §16.5); the platform targets fill them.
	MacOS *MacOSTarget `json:"macos,omitempty"`
	XKB   *XKBTarget   `json:"xkb,omitempty"`
}

// KLCTarget is SPEC §16.2.
type KLCTarget struct {
	MSKLCEditorCompatible *bool   `json:"msklcEditorCompatible,omitempty"`
	VersionLine           *string `json:"versionLine,omitempty"`
	OnLoss                *OnLoss `json:"onLoss,omitempty"`
}

// CLDRTarget is SPEC §16.3.
type CLDRTarget struct {
	ConformsTo     int     `json:"conformsTo,omitempty"`
	Locale         string  `json:"locale,omitempty"`
	CarryInSpecial *bool   `json:"carryInSpecial,omitempty"`
	OnLoss         *OnLoss `json:"onLoss,omitempty"`
}

// OnLoss is SPEC §17.2.
type OnLoss struct {
	Default string            `json:"default,omitempty"`
	Kinds   map[string]string `json:"kinds,omitempty"`
}

// WindowsTarget is SPEC §16.1.
type WindowsTarget struct {
	DLLName           string                     `json:"dllName,omitempty"`
	Builds            []string                   `json:"builds,omitempty"`
	UseTableA         *bool                      `json:"useTableA,omitempty"`
	LocaleFlags       *LocaleFlags               `json:"localeFlags,omitempty"`
	KbdVersion        *int                       `json:"kbdVersion,omitempty"`
	KeyboardType      *KeyboardType              `json:"keyboardType,omitempty"`
	Modifiers         *WindowsModifiers          `json:"modifiers,omitempty"`
	Keys              map[string]*WindowsKey     `json:"keys,omitempty"`
	ScanCodeTableSize *int                       `json:"scanCodeTableSize,omitempty"`
	Tables            []WindowsTable             `json:"tables,omitempty"`
	ExtraRows         []ExtraRow                 `json:"extraRows,omitempty"`
	DeadKeys          map[string]*WindowsDeadKey `json:"deadKeys,omitempty"`
	Strings           *WindowsStrings            `json:"strings,omitempty"`
	VersionInfo       *VersionInfo               `json:"versionInfo,omitempty"`
	Registration      *Registration              `json:"registration,omitempty"`
	Install           *Install                   `json:"install,omitempty"`
	NLS               *NLS                       `json:"nls,omitempty"`
	Dispatcher        *Dispatcher                `json:"dispatcher,omitempty"`
	OnLoss            *OnLoss                    `json:"onLoss,omitempty"`
}

// LocaleFlags pins KLLF_*.
type LocaleFlags struct {
	AltGr     *bool `json:"altGr,omitempty"`
	ShiftLock *bool `json:"shiftLock,omitempty"`
	LrmRlm    *bool `json:"lrmRlm,omitempty"`
}

// KeyboardType pins dwType and dwSubType.
type KeyboardType struct {
	Type    uint32 `json:"type"`
	SubType uint32 `json:"subType"`
}

// WindowsModifiers pins VK_TO_BIT and ModNumber.
type WindowsModifiers struct {
	Bits              []ModifierBit `json:"bits,omitempty"`
	ModNumber         []*int        `json:"modNumber,omitempty"` // nil entry = SHFT_INVALID
	KanaLockComponent string        `json:"kanaLockComponent,omitempty"`
}

// ModifierBit is one pinned VK_TO_BIT entry.
type ModifierBit struct {
	VK        string `json:"vk"`
	Bit       int    `json:"bit"`
	Component string `json:"component"`
}

// WindowsKey pins one position's VK, scan-code flags and row attributes.
type WindowsKey struct {
	VK            string   `json:"vk,omitempty"`
	ScanCodeFlags []string `json:"scanCodeFlags,omitzero"` // nil: not pinned; empty: pinned to none
	Attributes    []string `json:"attributes,omitzero"`    // nil: not pinned; empty: pinned to none
}

// WindowsTable pins one character table: its width and row order.
type WindowsTable struct {
	Width int        `json:"width"`
	Rows  []TableRow `json:"rows"`
}

// TableRow is a PositionId or {"vk": VK}.
type TableRow struct {
	Position string
	VK       string
}

// ExtraRow is a character row for a VK no position produces.
type ExtraRow struct {
	VK         string    `json:"vk"`
	Attributes []string  `json:"attributes,omitempty"`
	Cells      []WinCell `json:"cells"`
}

// WindowsDeadKey pins a dead key's Windows dead character.
type WindowsDeadKey struct {
	Character *TextOrUnits `json:"character,omitempty"`
}

// WindowsStrings pins strings 1000, 1100, 1200; the maps go from LANGID (4 hex digits) to text.
type WindowsStrings struct {
	LayoutName   map[string]string `json:"layoutName,omitempty"`
	LanguageName map[string]string `json:"languageName,omitempty"`
	LocaleName   *string           `json:"localeName,omitempty"`
}

// VersionInfo pins the version resource.
type VersionInfo struct {
	FileVersion     string  `json:"fileVersion,omitempty"`
	ProductVersion  string  `json:"productVersion,omitempty"`
	CompanyName     *string `json:"companyName,omitempty"`
	LegalCopyright  *string `json:"legalCopyright,omitempty"`
	FileDescription *string `json:"fileDescription,omitempty"`
}

// Registration is the installer's registry data.
type Registration struct {
	KLID               string `json:"klid,omitempty"`
	LangID             string `json:"langId,omitempty"`
	LayoutID           string `json:"layoutId,omitempty"`
	LayoutText         string `json:"layoutText,omitempty"`
	LocaleName         string `json:"localeName,omitempty"`
	CustomLanguageName string `json:"customLanguageName,omitempty"`
	ProductCode        string `json:"productCode,omitempty"`
}

// Install holds the installer choices of targets/windows-install.md (section 2).
type Install struct {
	EnableForUser *bool `json:"enableForUser,omitempty"`
	// EnableForSignInAndNewUsers is rule enableForSignInAndNewUsers (targets/windows-install.md section 7.1, D87).
	EnableForSignInAndNewUsers *bool  `json:"enableForSignInAndNewUsers,omitempty"`
	ProductName                string `json:"productName,omitempty"`
	ProductVersion             string `json:"productVersion,omitempty"`
	Publisher                  string `json:"publisher,omitempty"`
}

// NLS is the carried KbdNlsLayerDescriptor table.
type NLS struct {
	OEMIdentifier     *int       `json:"oemIdentifier,omitempty"`
	LayoutInformation *int       `json:"layoutInformation,omitempty"`
	VkToF             []NLSVkToF `json:"vkToF,omitempty"`
	MouseVKeys        []NLSMouse `json:"mouseVKeys,omitempty"`
}

// NLSVkToF is one VK_F entry.
type NLSVkToF struct {
	VK          string     `json:"vk"`
	ProcType    int        `json:"procType"`
	ProcCurrent int        `json:"procCurrent"`
	ProcSwitch  int        `json:"procSwitch"`
	Proc        []NLSParam `json:"proc"`
	ProcAlt     []NLSParam `json:"procAlt"`
}

// NLSParam is one VK_FPARAM.
type NLSParam struct {
	Index int    `json:"index"`
	Param uint32 `json:"param"`
}

// NLSMouse is one mouse VK entry.
type NLSMouse struct {
	VK    string   `json:"vk"`
	Flags []string `json:"flags,omitempty"`
}

// Dispatcher is the carried multi-table data.
type Dispatcher struct {
	Tables      []DispatcherTable `json:"tables,omitempty"`
	RealDllFile string            `json:"realDllFile,omitempty"`
}

// DispatcherTable is one KBDTABLE_DESC.
type DispatcherTable struct {
	DLLName string `json:"dllName"`
	Type    uint32 `json:"type"`
	SubType uint32 `json:"subType"`
}

// MacOSTarget is SPEC §16.4 (0.2): the macOS build choices (kbl-spec targets/macos.md).
type MacOSTarget struct {
	LayoutName     string                   `json:"layoutName,omitempty"`
	BundleName     string                   `json:"bundleName,omitempty"`
	BundleID       string                   `json:"bundleId,omitempty"`
	InputSourceID  string                   `json:"inputSourceId,omitempty"`
	Language       string                   `json:"language,omitempty"`
	LocalizedNames map[string]string        `json:"localizedNames,omitempty"`
	BundleVersion  string                   `json:"bundleVersion,omitempty"`
	Icon           *MacOSIcon               `json:"icon,omitempty"`
	Group          *int                     `json:"group,omitempty"`
	ID             *int                     `json:"id,omitempty"`
	MaxOut         *int                     `json:"maxout,omitempty"`
	HardwareRanges []MacOSHardwareRange     `json:"hardwareRanges,omitempty"`
	Keys           map[string]*MacOSKey     `json:"keys,omitempty"`
	StandardKeys   *bool                    `json:"standardKeys,omitempty"`
	CommandMap     string                   `json:"commandMap,omitempty"`
	ControlMap     string                   `json:"controlMap,omitempty"`
	Modifiers      *MacOSModifiers          `json:"modifiers,omitempty"`
	DeadKeys       map[string]*MacOSDeadKey `json:"deadKeys,omitempty"`
	OnLoss         *OnLoss                  `json:"onLoss,omitempty"`
}

// MacOSIcon is targets.macos.icon: the .icns file in base64.
type MacOSIcon struct {
	ICNS     string `json:"icns"`
	Template *bool  `json:"template,omitempty"`
}

// MacOSHardwareRange is one <layout> element's range of hardware keyboard types.
type MacOSHardwareRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
}

// MacOSKey is the macOS data of one position.
type MacOSKey struct {
	KeyCode int `json:"keyCode"`
}

// MacOSModifiers names the custom components that are macOS modifier keys.
type MacOSModifiers struct {
	Command string `json:"command,omitempty"`
}

// MacOSDeadKey is the macOS data of one dead key.
type MacOSDeadKey struct {
	State string `json:"state,omitempty"`
}

// XKBTarget is SPEC §16.5 (0.2): the Linux XKB build choices (kbl-spec targets/xkb.md).
type XKBTarget struct {
	SymbolsName      string                 `json:"symbolsName,omitempty"`
	Variant          string                 `json:"variant,omitempty"`
	Description      string                 `json:"description,omitempty"`
	ShortDescription string                 `json:"shortDescription,omitempty"`
	Languages        []string               `json:"languages,omitempty"`
	Characters       string                 `json:"characters,omitempty"`
	Keys             map[string]*XKBKey     `json:"keys,omitempty"`
	CustomTypes      *bool                  `json:"customTypes,omitempty"`
	Modifiers        *XKBModifiers          `json:"modifiers,omitempty"`
	DeadKeys         map[string]*XKBDeadKey `json:"deadKeys,omitempty"`
	Compose          *XKBCompose            `json:"compose,omitempty"`
	OnLoss           *OnLoss                `json:"onLoss,omitempty"`
}

// XKBKey is the XKB data of one position.
type XKBKey struct {
	KeyName string `json:"keyName,omitempty"`
	Type    string `json:"type,omitempty"`
}

// XKBModifiers names the components that shift XKB levels.
type XKBModifiers struct {
	LevelThree string `json:"levelThree,omitempty"`
	LevelFive  string `json:"levelFive,omitempty"`
}

// XKBDeadKey is the XKB data of one dead key.
type XKBDeadKey struct {
	Keysym string `json:"keysym,omitempty"`
}

// XKBCompose is targets.xkb.compose.
type XKBCompose struct {
	Write         *bool `json:"write,omitempty"`
	IncludeLocale *bool `json:"includeLocale,omitempty"`
}
