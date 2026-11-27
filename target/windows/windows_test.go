package windows

import (
	"bytes"
	"crypto/sha256"
	stdpe "debug/pe"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/pe"
)

var update = flag.Bool("update", false, "rewrite the golden digests")

// sample returns a small layout that uses every table: four columns (base, Shift, Ctrl, AltGr), a dead key, an
// SGCAPS key, a ligature, a chained dead key, key names, a fourth modifier.
func sample() *DLL {
	none := uint16(WCH_NONE)
	t := Tables{
		Modifiers: Modifiers{
			VKToBit:   []VKBit{{0x10, KBDSHIFT}, {0x11, KBDCTRL}, {0x12, KBDALT}},
			ModNumber: []byte{0, 1, 2, SHFT_INVALID, SHFT_INVALID, SHFT_INVALID, 3, SHFT_INVALID},
		},
		CharTables: []CharTable{
			{Width: 4, Rows: []CharRow{
				{VK: 'Q', Attributes: CAPLOK, Chars: []uint16{'q', 'Q', none, '@'}},
				{VK: 0xC0, Attributes: 0, Chars: []uint16{WCH_DEAD, '~', none, none}},
				{VK: 0xFF, Attributes: 0, Chars: []uint16{'`', none, none, none}},
				{VK: 'W', Attributes: SGCAPS, Chars: []uint16{'w', 'W', none, none}},
				{VK: 'W', Attributes: 0, Chars: []uint16{'x', 'X', none, none}},
				{VK: 0xE2, Attributes: 0, Chars: []uint16{'\\', WCH_LGTR, 0x1C, none}},
			}},
			{Width: 2, Rows: []CharRow{
				{VK: 0x09, Chars: []uint16{'\t', '\t'}},
			}},
			{Width: 1, Rows: []CharRow{
				{VK: 0x60, Chars: []uint16{'0'}},
			}},
		},
		DeadKeys: []DeadKey{
			{Dead: '`', Base: 'a', Composed: 0xE0},
			{Dead: '`', Base: ' ', Composed: '`'},
			{Dead: '`', Base: '`', Composed: 0x2DB, Flags: DKF_DEAD},
		},
		KeyNames:      []KeyName{{0x01, "Esc"}, {0x39, "Space"}},
		KeyNamesExt:   []KeyName{{0x1D, "Right Ctrl"}},
		KeyNamesDead:  []DeadName{{'`', "GRAVE ACCENT"}},
		ScanToVK:      make([]uint16, 0x7F),
		E0:            []ScanVK{{0x1C, 0x10D}, {0x1D, 0x1A3}},
		E1:            []ScanVK{{0x1D, 0x13}},
		LocaleFlags:   KLLF_ALTGR,
		Version:       KBD_VERSION,
		LigatureWidth: 2,
		Ligatures:     []Ligature{{VK: 0xE2, Column: 1, Chars: []uint16{'a', 0x301}}},
	}
	for i := range t.ScanToVK {
		t.ScanToVK[i] = 0xFF
	}
	t.ScanToVK[0x10] = 'Q'
	t.ScanToVK[0x11] = 'W'
	t.ScanToVK[0x29] = 0xC0
	t.ScanToVK[0x56] = 0xE2
	d := &DLL{FileName: "kbdtst.dll", Tables: t,
		Strings: []String{
			{StringLayoutName, 0x409, "Test layout"},
			{StringLanguageName, 0x409, "English (United States)"},
			{StringLocaleName, 0x409, "en-US"},
		},
		Version: DefaultVersion("kbdtst.dll", "Test layout", "Someone", "(c) nobody", 0x409, [4]uint16{0, 1, 0, 0}),
	}
	return d
}

func build(t *testing.T, d *DLL, a Arch) []byte {
	t.Helper()
	b, r := Compile(d, a)
	if r.HasErrors() {
		t.Fatalf("%v: %s", a, r.Summary())
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	for _, a := range Arches {
		t.Run(a.String(), func(t *testing.T) {
			d := sample()
			b := build(t, d, a)
			got, arch, err := Read(b)
			if err != nil {
				t.Fatal(err)
			}
			if arch != a {
				t.Errorf("read back as %v", arch)
			}
			if !reflect.DeepEqual(got, d) {
				t.Errorf("round trip differs:\n got %+v\nwant %+v", got, d)
			}
		})
	}
}

func TestContainerRules(t *testing.T) {
	for _, a := range Arches {
		t.Run(a.String(), func(t *testing.T) {
			b := build(t, sample(), a)
			f, err := pe.Parse(b)
			if err != nil {
				t.Fatal(err)
			}
			h := a.header()
			if f.Machine != h.machine || f.PE32Plus != h.plus || f.ImageBase != h.imageBase ||
				f.Characteristics != h.characteristics || f.DllCharacteristics != h.dllCharacteristics ||
				f.FileAlignment != h.fileAlign || f.SectionAlignment != 0x1000 {
				t.Errorf("header values differ from dll-format §1.1")
			}
			if f.EntryPoint != 0 || f.Dir(pe.DirImport).RVA != 0 || f.Subsystem != pe.SubsystemNative {
				t.Errorf("entry point, imports or subsystem")
			}
			if f.CheckSum != f.ComputeChecksum() {
				t.Errorf("checksum")
			}
			var names []string
			for _, s := range f.Sections {
				names = append(names, s.Name)
			}
			want := ".data .rsrc .reloc"
			if a == AMD64 {
				want = ".text " + want
			}
			if strings.Join(names, " ") != want {
				t.Errorf("sections %v", names)
			}
			data := f.Section(".data")
			if uint64(data.PointerToRawData)+uint64(data.VirtualSize) >= uint64(len(b)) {
				t.Error(".data extent rule")
			}
			wantData := uint32(pe.SecReadOnlyData)
			if a != AMD64 {
				wantData = pe.SecInitData | pe.SecMemExecute | pe.SecMemRead
			}
			if data.Characteristics != wantData {
				t.Errorf(".data characteristics %#x", data.Characteristics)
			}
			for _, n := range []string{".rsrc", ".reloc"} {
				if c := f.Section(n).Characteristics; c != 0x4200_0040 {
					t.Errorf("%s characteristics %#x, want 4200_0040", n, c)
				}
			}
			// Exports: ordinal 1 by name, inside the export directory in .data.
			dll, exps, err := f.Exports()
			if err != nil || dll != "kbdtst.dll" || len(exps) != 1 || exps[0].Ordinal != 1 || exps[0].Name != ExportName {
				t.Errorf("exports %q %+v %v", dll, exps, err)
			}
			if f.SectionAt(f.Dir(pe.DirExport).RVA).Name != ".data" {
				t.Error("export directory not in .data")
			}
			// Descriptor function shape (§1.4).
			code, _ := f.Bytes(exps[0].RVA, 8)
			switch a {
			case AMD64:
				if !bytes.Equal(code[:3], []byte{0x48, 0x8D, 0x05}) || code[7] != 0xC3 {
					t.Errorf("stub % x", code)
				}
			case WOW64:
				if code[0] != 0xB8 || code[5] != 0x99 || code[6] != 0xC3 {
					t.Errorf("stub % x", code)
				}
			case X86:
				if code[0] != 0xB8 || code[5] != 0xC3 {
					t.Errorf("stub % x", code)
				}
			}
			// Every relocation is of the architecture's type and lies in .data; every non-zero pointer slot of the
			// tables has one (§1.5).
			rels, err := f.Relocs()
			if err != nil {
				t.Fatal(err)
			}
			wantType := uint16(pe.RelDir64)
			if a != AMD64 {
				wantType = pe.RelHighLow
			}
			relAt := map[uint32]bool{}
			for _, rl := range rels {
				if rl.Type != wantType {
					t.Errorf("relocation type %d", rl.Type)
				}
				if f.SectionAt(rl.RVA).Name != ".data" {
					t.Errorf("relocation outside .data")
				}
				relAt[rl.RVA] = true
				// The pointer it relocates points into .data.
				raw, _ := f.Bytes(rl.RVA, 4)
				va := uint64(binary.LittleEndian.Uint32(raw))
				if a == AMD64 {
					raw, _ = f.Bytes(rl.RVA, 8)
					va = binary.LittleEndian.Uint64(raw)
				}
				if s := f.SectionAt(uint32(va - f.ImageBase)); s == nil || s.Name != ".data" {
					t.Errorf("pointer at %#x to %#x leaves .data", rl.RVA, va)
				}
			}
			enc := encodeTables(&sample().Tables, a)
			for _, p := range enc.pointers {
				if !relAt[data.RVA+p.slot] {
					t.Errorf("pointer slot .data+%#x has no relocation", p.slot)
				}
			}
			if a != AMD64 && !relAt[exps[0].RVA+1] {
				t.Error("descriptor address has no relocation")
			}
			// The standard library reads the container too.
			if _, err := stdpe.NewFile(bytes.NewReader(b)); err != nil {
				t.Errorf("debug/pe: %v", err)
			}
		})
	}
}

func TestKBDTablesZoneIsFree(t *testing.T) {
	for _, a := range Arches {
		enc := encodeTables(&sample().Tables, a)
		for _, p := range enc.pointers {
			if p.target >= enc.kbdTables && p.target < enc.kbdTables+kbdTablesFull {
				t.Errorf("%v: pointer into KBDTABLES", a)
			}
		}
	}
}

func TestDeterministicAndGolden(t *testing.T) {
	path := filepath.Join("testdata", "golden.sha256")
	var lines []string
	for _, a := range Arches {
		b1 := build(t, sample(), a)
		b2 := build(t, sample(), a)
		if !bytes.Equal(b1, b2) {
			t.Errorf("%v: two builds differ", a)
		}
		sum := sha256.Sum256(b1)
		lines = append(lines, a.String()+" "+hex.EncodeToString(sum[:]))
	}
	got := strings.Join(lines, "\n") + "\n"
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(path, []byte(got), 0o644)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update once)", err)
	}
	if string(want) != got {
		t.Errorf("golden digests changed; if intended run go test ./target/windows -update\n got %s\nwant %s", got, want)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name string
		code string
		mod  func(*Tables)
	}{
		{"dead row missing", "win.row.dead", func(t *Tables) { t.CharTables[0].Rows[2].VK = 'Z' }},
		{"sgcaps row missing", "win.row.sgcaps", func(t *Tables) { t.CharTables[0].Rows[4].VK = 'Z' }},
		{"ligature missing", "win.row.ligature", func(t *Tables) { t.Ligatures[0].Column = 0 }},
		{"ligature column", "win.ligature.column", func(t *Tables) { t.Ligatures = append(t.Ligatures, Ligature{VK: 'A', Column: 9, Chars: []uint16{1}}) }},
		{"ligature version", "win.ligature.version", func(t *Tables) { t.Version = 0 }},
		{"row width", "win.row.width", func(t *Tables) { t.CharTables[1].Rows[0].Chars = []uint16{1} }},
		{"table width", "win.table.width", func(t *Tables) { t.CharTables[1].Width = 0 }},
		{"modnumber column", "win.modnumber.column", func(t *Tables) { t.Modifiers.ModNumber[1] = 20 }},
		{"modnumber bits", "win.modnumber.bits", func(t *Tables) { t.Modifiers.VKToBit = t.Modifiers.VKToBit[:2] }},
		{"flags", "win.vsc.flags", func(t *Tables) { t.ScanToVK[3] = 0x1041 }},
		{"e0 zero", "win.vscvk.zero", func(t *Tables) { t.E0[0].Scan = 0 }},
		{"vsc size", "win.vsc.size", func(t *Tables) { t.ScanToVK = nil }},
	}
	for _, tt := range tests {
		d := sample()
		tt.mod(&d.Tables)
		_, r := Compile(d, AMD64)
		found := false
		for _, e := range r.Entries {
			if e.Code == tt.code {
				found = true
			}
		}
		if !found || !r.HasErrors() {
			t.Errorf("%s: want error %s, got %s", tt.name, tt.code, r.Summary())
		}
	}
}

func TestValidateWarnings(t *testing.T) {
	d := sample()
	d.Tables.DeadKeys = append(d.Tables.DeadKeys, DeadKey{Dead: '`', Base: 'a', Composed: 'x', Flags: 4})
	b, r := Compile(d, AMD64)
	if b == nil || r.Count(0) != 0 {
		t.Fatalf("warnings must not stop the build: %s", r.Summary())
	}
	codes := strings.Join(r.Codes(), ",")
	if !strings.Contains(codes, "win.deadkey.dup") || !strings.Contains(codes, "win.deadkey.flags") {
		t.Errorf("codes %s", codes)
	}
}

func TestSizeLimit(t *testing.T) {
	d := sample()
	long := strings.Repeat("x", 30000)
	for i := 1; i < 0x60; i++ {
		d.Tables.KeyNames = append(d.Tables.KeyNames, KeyName{Scan: byte(i + 0x40), Name: long})
	}
	d.Tables.KeyNames = d.Tables.KeyNames[:12]
	b, r := Compile(d, AMD64)
	if b != nil || !strings.Contains(strings.Join(r.Codes(), ","), "win.size") {
		t.Errorf("oversized DLL not refused: %d bytes, %s", len(b), r.Summary())
	}
}

func TestSGCapsDeadShape(t *testing.T) {
	d := sample()
	rows := d.Tables.CharTables[0].Rows
	// W: SGCAPS with a dead key in column 1, followed by the one FF row (the shipped shape, §4.2).
	rows[3] = CharRow{VK: 'W', Attributes: SGCAPS, Chars: []uint16{'w', WCH_DEAD, WCH_NONE, WCH_NONE}}
	rows[4] = CharRow{VK: 0xFF, Chars: []uint16{WCH_NONE, '^', WCH_NONE, WCH_NONE}}
	b, r := Compile(d, AMD64)
	if b == nil || !strings.Contains(strings.Join(r.Codes(), ","), "win.row.sgcaps-dead") {
		t.Errorf("shipped shape not accepted with a warning: %s", r.Summary())
	}
}

func TestReadRejectsZeroCbSize(t *testing.T) {
	b := build(t, sample(), AMD64)
	f, _ := pe.Parse(b)
	_, exps, _ := f.Exports()
	code, _ := f.Bytes(exps[0].RVA, 8)
	kt := uint32(int64(exps[0].RVA) + 7 + int64(int32(binary.LittleEndian.Uint32(code[3:]))))
	ktb, _ := f.Bytes(kt, 0x68)
	vkt := uint32(binary.LittleEndian.Uint64(ktb[8:]) - f.ImageBase)
	off, _ := f.Offset(vkt)
	bad := append([]byte(nil), b...)
	bad[off+9] = 0 // cbSize of the first table
	if _, _, err := Read(bad); err == nil || !strings.Contains(err.Error(), "cbSize") {
		t.Errorf("cbSize 0 not rejected: %v", err)
	}
}

func TestReadHostile(t *testing.T) {
	good := build(t, sample(), WOW64)
	for i := 0; i < len(good); i += 3 {
		b := append([]byte(nil), good...)
		b[i] ^= 0xA5
		Read(b) // must return, never panic or loop
	}
	for n := 0; n < len(good); n += 97 {
		Read(good[:n])
	}
}

func TestSupplementary(t *testing.T) {
	e, err := SupplementaryEntry(0xE000, 0x1F600, 0x1FA00)
	if err != nil || e != (DeadKey{Dead: 0xE000, Base: 0xD83D, Composed: 0xD83E}) {
		t.Errorf("recipe entry %+v %v", e, err)
	}
	if _, err := SupplementaryEntry(0xE000, 0x1F600, 0x1F601); err == nil {
		t.Error("different low surrogates accepted")
	}
	if _, err := SupplementaryEntry(0xE000, 'a', 0x1F600); err == nil {
		t.Error("BMP base accepted")
	}
	got, errs := SupplementaryEntries([]Composition{
		{0xE000, 0x1F600, 0x1FA00},
		{0xE000, 0x1F600, 0x1FA00}, // same entry, same result: shared
		{0xE000, 0x1F700, 0x1FB00}, // same high surrogate D83D, different result D83E vs D83E? see below
	})
	_ = got
	// U+1F700 = D83D DF00 and U+1FB00 = D83E DF00: entry (E000, D83D) -> D83E again, so no conflict.
	if len(errs) != 0 || len(got) != 1 {
		t.Errorf("shared entries: %+v %v", got, errs)
	}
	_, errs = SupplementaryEntries([]Composition{
		{0xE000, 0x1F600, 0x1FA00},
		{0xE000, 0x1F601, 0x20001}, // D83D DE01 -> D840 DC01: low differs
		{0xE000, 0x1F610, 0x1F810}, // D83D DE10 -> D83E DC10? no: U+1F810 = D83E DC10, low differs too
		{0xE000, 0x1F680, 0x1FE80}, // D83D DE80 -> D83F DE80: same entry (E000, D83D) wants D83F: conflict
	})
	if len(errs) != 3 {
		t.Errorf("want 3 losses, got %v", errs)
	}
}

// TestKBDTablesOffsets checks the field offsets of dll-format §3 for both pointer sizes.
func TestKBDTablesOffsets(t *testing.T) {
	for _, tc := range []struct {
		a                          Arch
		bMax, flags, nLg, lig, typ int
	}{
		{AMD64, 0x38, 0x50, 0x54, 0x58, 0x60},
		{X86, 0x1C, 0x28, 0x2C, 0x30, 0x34},
	} {
		tb := sample().Tables
		tb.Type, tb.SubType = 0x11223344, 0x55667788
		enc := encodeTables(&tb, tc.a)
		k := enc.data[enc.kbdTables:]
		le := binary.LittleEndian
		if int(k[tc.bMax]) != len(tb.ScanToVK) || le.Uint32(k[tc.flags:]) != 0x0001_0001 ||
			k[tc.nLg] != 2 || k[tc.nLg+1] != 8 || le.Uint32(k[tc.typ:]) != 0x11223344 ||
			le.Uint32(k[tc.typ+4:]) != 0x55667788 {
			t.Errorf("%v: KBDTABLES fields at the wrong offsets", tc.a)
		}
		ligSlot := false
		for _, p := range enc.pointers {
			if p.slot == enc.kbdTables+uint32(tc.lig) {
				ligSlot = true
			}
		}
		if !ligSlot {
			t.Errorf("%v: pLigature not at %#x", tc.a, tc.lig)
		}
	}
}

func TestCompileAll(t *testing.T) {
	set := CompileAll(sample(), nil)
	if len(set.Builds) != 4 || len(set.Unsupported) != 0 || set.Builds[3].Arch != ARM64X || set.Builds[3].Dir != "System32" || set.Report.HasErrors() {
		t.Fatalf("full set: %d builds, %v, %s", len(set.Builds), set.Unsupported, set.Report.Summary())
	}
	if set.Builds[1].Dir != "SysWOW64" || set.Builds[0].Arch != AMD64 {
		t.Errorf("builds %+v", set.Builds[0].Target)
	}
	// Asked for, ARM64X and the plain ARM64 control are built for System32 (not verified on Windows yet).
	if o := CompileAll(sample(), []Target{TargetARM64}); !o.Report.HasErrors() || len(o.Builds) != 0 {
		t.Error("the plain arm64 control must be an error unless test builds are allowed")
	}
	if o := CompileAll(sample(), []Target{TargetARM64X}); o.Report.HasErrors() || len(o.Builds) != 1 {
		t.Error("arm64x alone must build")
	}
	only := CompileSet(sample(), []Target{TargetARM64X, TargetARM64}, true)
	if only.Report.HasErrors() || len(only.Builds) != 2 || only.Builds[0].Arch != ARM64X || only.Builds[1].Arch != ARM64 || only.Builds[0].Dir != "System32" {
		t.Errorf("arm64x and arm64 asked for: %+v %s", len(only.Builds), only.Report.Summary())
	}
	one := CompileAll(sample(), []Target{TargetWOW64})
	if len(one.Builds) != 1 || one.Report.HasErrors() {
		t.Error("single target")
	}
}

func TestExpectations(t *testing.T) {
	got := string(FormatExpectations("kbdtst", sample().Tables.Expectations()))
	for _, want := range []string{
		"key 51 -> chars 0071\n", "key 51+10 -> chars 0051\n", "key 51+11+12 -> chars 0040\n",
		"key c0 -> dead 0060\n", "key e2+10 -> chars 0061 0301\n", "seq c0 c0 -> chain 02db\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "key 51+11 -> none") {
		t.Error("Ctrl column without a character must not be expected as none")
	}
	if strings.Contains(got, "key 57 -> chars 0078") {
		t.Error("SGCAPS second row taken as the key's row")
	}
}

func TestExpectationsLRM(t *testing.T) {
	d := sample()
	d.Tables.LocaleFlags |= KLLF_LRM_RLM
	d.Tables.CharTables[1].Rows = append(d.Tables.CharTables[1].Rows, CharRow{VK: 0x08, Chars: []uint16{8, 8}})
	got := string(FormatExpectations("x", d.Tables.Expectations()))
	if !strings.Contains(got, "key 08+a1 -> chars 200f\n") || !strings.Contains(got, "key 08+10+a1 -> chars 200e\n") {
		t.Errorf("right Shift (RLM) expectations missing:\n%s", got)
	}
	if !strings.Contains(got, "key 08+10 -> chars 200e\n") || !strings.Contains(got, "key 08 -> chars 0008\n") {
		t.Errorf("LRM expectations:\n%s", got)
	}
}
