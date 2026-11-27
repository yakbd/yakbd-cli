package windows

import (
	"fmt"
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/pe"
)

// ExportName is the name of the descriptor export, ordinal 1 (dll-format §1.3).
const ExportName = "KbdLayerDescriptor"

// LinkerVersion is the tool's own value for the linker-version field: no other vendor's version is copied
// (dll-format §1.1).
var LinkerVersion = [2]uint8{0, 1}

// stubFor returns the descriptor function of an architecture and the offset of its address field
// (dll-format §1.4).
func stubFor(a Arch) (code []byte, field uint32, kind pe.FixupKind) {
	switch a {
	case AMD64:
		return []byte{0x48, 0x8D, 0x05, 0, 0, 0, 0, 0xC3}, 3, pe.Rel32 // lea rax, [rip+rel32]; ret
	case WOW64:
		return []byte{0xB8, 0, 0, 0, 0, 0x99, 0xC3}, 1, pe.Abs32 // mov eax, imm32; cdq; ret
	default:
		return []byte{0xB8, 0, 0, 0, 0, 0xC3}, 1, pe.Abs32 // mov eax, imm32; ret
	}
}

// Compile builds the DLL for one architecture. The report holds the validation entries; on any error the
// returned bytes are nil. The x86 and WOW64 descriptor function always gets a relocation on its address: without
// it a rebased file does not load in a 32-bit process (VM run 1, test VM-1), and Windows' own SysWOW64 files have
// it (17 of 17 with that function shape).
func Compile(d *DLL, a Arch) ([]byte, *loss.Report) {
	return compile(d, a, d.Tables.Validate())
}

// CompileUnvalidated builds the DLL without the checks of Tables.Validate, for test variants that Windows has to
// settle (tools/vmbundle, VM-4: shapes the normal build refuses). Never use it for a layout that gets installed.
func CompileUnvalidated(d *DLL, a Arch) ([]byte, *loss.Report) {
	return compile(d, a, &loss.Report{})
}

func compile(d *DLL, a Arch, r *loss.Report) ([]byte, *loss.Report) {
	if !strings.HasSuffix(strings.ToLower(d.FileName), ".dll") || len(d.FileName) < 5 {
		r.Errorf("win.dll.name", loss.Where{}, "file name %q must end in .dll", d.FileName)
	}
	if r.HasErrors() {
		return nil, r
	}
	if a == ARM64 || a == ARM64X {
		return compileARM64(d, a, ShippedVariant, r)
	}
	h := a.header()
	enc := encodeTables(&d.Tables, a)
	data := &pe.Section{Name: dataSection, Characteristics: pe.SecReadOnlyData, Data: enc.data, Fixups: enc.fixups}
	code, field, kind := stubFor(a)
	codeSec := dataSection
	var sections []*pe.Section
	var stubOff uint32
	if h.separateText {
		text := &pe.Section{Name: ".text", Characteristics: pe.SecCode | pe.SecMemExecute | pe.SecMemRead, Data: code,
			Fixups: []pe.Fixup{{Offset: field, Kind: kind, Target: pe.Ref{Section: dataSection, Offset: enc.kbdTables}}}}
		sections = []*pe.Section{text, data}
		codeSec = ".text"
	} else {
		// Code merged into .data, which is then marked executable (dll-format §1.2).
		data.Characteristics = pe.SecInitData | pe.SecMemExecute | pe.SecMemRead
		for len(data.Data)%16 != 0 {
			data.Data = append(data.Data, 0)
		}
		stubOff = uint32(len(data.Data))
		data.Data = append(data.Data, code...)
		data.Fixups = append(data.Fixups, pe.Fixup{Offset: stubOff + field, Kind: kind,
			Target: pe.Ref{Section: dataSection, Offset: enc.kbdTables}})
		sections = []*pe.Section{data}
	}
	res, err := resourcesOf(d)
	if err != nil {
		r.Errorf("win.strings", loss.Where{}, "%v", err)
		return nil, r
	}
	im := &pe.Image{
		Machine: h.machine, PE32Plus: h.plus, ImageBase: h.imageBase,
		SectionAlignment: h.sectionAlign, FileAlignment: h.fileAlign,
		Characteristics: h.characteristics, DllCharacteristics: h.dllCharacteristics,
		Subsystem: pe.SubsystemNative, LinkerVersion: LinkerVersion,
		OSVersion: [2]uint16{10, 0}, ImageVersion: [2]uint16{10, 0}, SubsystemVersion: [2]uint16{10, 0},
		Sections: sections, DLLName: d.FileName, ExportSection: dataSection,
		Exports:       []pe.Export{{Name: ExportName, Ordinal: 1, Target: pe.Ref{Section: codeSec, Offset: stubOff}}},
		Resources:     res,
		ResourceChars: pe.SecReadOnlyData | pe.SecDiscardable, // 4200_0040 (dll-format §1.2)
		RelocChars:    pe.SecReadOnlyData | pe.SecDiscardable,
	}
	out, lay, err := im.Write()
	if err != nil {
		r.Errorf("win.pe", loss.Where{}, "%v", err)
		return nil, r
	}
	checkImage(r, out, lay, enc)
	if r.HasErrors() {
		return nil, r
	}
	return out, r
}

// resourcesOf returns the string blocks and the version resource of d.
func resourcesOf(d *DLL) ([]pe.Resource, error) {
	var res []pe.Resource
	blocks, err := encodeStrings(d.Strings)
	if err != nil {
		return nil, err
	}
	for _, k := range sortedBlocks(blocks) {
		res = append(res, pe.Resource{Type: pe.RTString, Name: k.block, Lang: k.lang, Data: blocks[k]})
	}
	if d.Version != nil {
		res = append(res, pe.Resource{Type: pe.RTVersion, Name: 1, Lang: d.Version.Lang, Data: encodeVersion(d.Version)})
	}
	return res, nil
}

// checkImage asserts the container rules of dll-format §1.2, §2 and §6.1 on the written file.
func checkImage(r *loss.Report, out []byte, lay *pe.Layout, enc *encoded) {
	w := loss.Where{Path: "DLL"}
	if len(out) > MaxFileSize {
		r.Errorf("win.size", w, "the DLL has %d bytes; Windows loads at most %d (dll-format §1.2)", len(out), MaxFileSize)
	}
	var data *pe.PlacedSection
	for i := range lay.Sections {
		if lay.Sections[i].Name == dataSection {
			data = &lay.Sections[i]
		}
	}
	if data == nil {
		r.Errorf("win.data", w, "no .data section")
		return
	}
	if uint64(data.PointerToRawData)+uint64(data.VirtualSize) >= uint64(len(out)) {
		r.Errorf("win.data.extent", w, ".data must end before the end of the file (dll-format §1.2)")
	}
	if data.RVA+enc.kbdTables >= 0x10000 {
		r.Errorf("win.rva", w, "KBDTABLES at RVA %#x; it must stay below 0x10000 (dll-format §6.1)", data.RVA+enc.kbdTables)
	}
	for _, p := range enc.pointers {
		if p.target >= uint32(len(enc.data)) {
			r.Errorf("win.pointer.outside", w, "pointer at .data+%#x leaves .data", p.slot)
		}
		if p.target >= enc.kbdTables && p.target < enc.kbdTables+kbdTablesFull {
			r.Errorf("win.pointer.inside", w, "pointer at .data+%#x points into KBDTABLES (dll-format §2)", p.slot)
		}
	}
}

// DefaultVersion returns the version resource the tool writes: its own product name and version, never another
// company's name (dll-format §1.6).
func DefaultVersion(fileName, description, company, copyright string, lang uint16, toolVersion [4]uint16) *VersionInfo {
	base := strings.TrimSuffix(fileName, ".dll")
	kv := []VersionKV{}
	if company != "" {
		kv = append(kv, VersionKV{"CompanyName", company})
	}
	kv = append(kv,
		VersionKV{"FileDescription", description},
		VersionKV{"FileVersion", "1.0.0.0"},
		VersionKV{"InternalName", base},
	)
	if copyright != "" {
		kv = append(kv, VersionKV{"LegalCopyright", copyright})
	}
	kv = append(kv,
		VersionKV{"OriginalFilename", fileName},
		VersionKV{"ProductName", "yakbd keyboard layout"},
		VersionKV{"ProductVersion", fmt.Sprintf("%d.%d.%d.%d", toolVersion[0], toolVersion[1], toolVersion[2], toolVersion[3])},
	)
	return &VersionInfo{FileVersion: [4]uint16{1, 0, 0, 0}, ProductVersion: toolVersion, Lang: lang, Strings: kv}
}
