package windows

import (
	"encoding/binary"
	"fmt"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/pe"
)

// ARM64 and ARM64X files (docs/arm64x.md). The tables are the bytes of the amd64 build (8-byte pointers, DIR64
// relocations); what differs is the code, the headers and, for ARM64X, the x64 view: a second export directory,
// load configurations, CHPE metadata and the ARM64X entries that switch an x64 process to that view.

// ARM64XVariant names one candidate shape of the x64 view (docs/arm64x.md §3). Until a Windows test settles
// which ones work, the bundle tests all of them.
type ARM64XVariant string

const (
	// VariantEC is the shape of Windows' own files and of lld: the x64 export is an export thunk in .hexpthk that
	// jumps to an ARM64EC function with an entry thunk.
	VariantEC ARM64XVariant = "ec"
	// VariantX64 exports a plain x64 function (the amd64 stub) in the x64 view; no ARM64EC code.
	VariantX64 ARM64XVariant = "x64"
	// VariantX64OneCfg is VariantX64 with one load configuration for both views.
	VariantX64OneCfg ARM64XVariant = "x64-onecfg"
)

// ARM64XVariants lists the variants in the order a test runs them (Windows' shape first).
var ARM64XVariants = []ARM64XVariant{VariantEC, VariantX64, VariantX64OneCfg}

// CompileARM64X builds the ARM64X file in one variant, with the same checks as Compile.
func CompileARM64X(d *DLL, v ARM64XVariant) ([]byte, *loss.Report) {
	r := d.Tables.Validate()
	if r.HasErrors() {
		return nil, r
	}
	return compileARM64(d, ARM64X, v, r)
}

// A64 instructions with zero immediates; the fixups fill them (Arm A64 encodings).
const (
	a64AdrpX0   = 0x90000000 // adrp x0, page
	a64AddX0X0  = 0x91000000 // add x0, x0, #lo12
	a64AdrpX8   = 0x90000008 // adrp x8, page
	a64AddX8X8  = 0x91000108 // add x8, x8, #lo12
	a64AdrpX16  = 0x90000010 // adrp x16, page
	a64LdrX16   = 0xF9400210 // ldr x16, [x16, #lo12]
	a64BrX16    = 0xD61F0200 // br x16
	a64Ret      = 0xD65F03C0 // ret
	pageSize    = 0x1000
	cfgSection  = ".00cfg"
	rmSection   = ".a64xrm"
	thkSection  = ".hexpthk"
	textSection = ".text"
)

func a64(words ...uint32) []byte {
	b := make([]byte, 4*len(words))
	for i, w := range words {
		binary.LittleEndian.PutUint32(b[4*i:], w)
	}
	return b
}

// x64ExportThunk is the export thunk ("fast-forward sequence") of docs/arm64x.md §2.5: mov rax,rsp; mov
// [rax+20h],rbx; push rbp; pop rbp; jmp rel32; int3; int3. The rel32 field is at offset 10.
var x64ExportThunk = []byte{0x48, 0x8B, 0xC4, 0x48, 0x89, 0x58, 0x20, 0x55, 0x5D, 0xE9, 0, 0, 0, 0, 0xCC, 0xCC}

// compileARM64 builds the plain ARM64 file or the ARM64X file in variant v.
func compileARM64(d *DLL, a Arch, v ARM64XVariant, r *loss.Report) ([]byte, *loss.Report) {
	switch v {
	case VariantEC, VariantX64, VariantX64OneCfg:
	default:
		r.Errorf("win.arm64x.variant", loss.Where{}, "unknown ARM64X variant %q", v)
		return nil, r
	}
	enc := encodeTables(&d.Tables, a)
	data := &pe.Section{Name: dataSection, Characteristics: pe.SecReadOnlyData, Data: enc.data, Fixups: enc.fixups}
	tables := pe.Ref{Section: dataSection, Offset: enc.kbdTables}
	code := pe.SecCode | pe.SecMemExecute | pe.SecMemRead

	// The native function: adrp x0; add x0; ret at the start of .text (dll-format §1.4, [INF]).
	text := &pe.Section{Name: textSection, Characteristics: uint32(code), Data: a64(a64AdrpX0, a64AddX0X0, a64Ret),
		Fixups: []pe.Fixup{{Offset: 0, Kind: pe.Arm64Page21, Target: tables}, {Offset: 4, Kind: pe.Arm64Lo12, Target: tables}}}
	res, err := resourcesOf(d)
	if err != nil {
		r.Errorf("win.strings", loss.Where{}, "%v", err)
		return nil, r
	}
	im := &pe.Image{
		Machine: pe.MachineARM64, PE32Plus: true, ImageBase: 0x1_8000_0000,
		SectionAlignment: pageSize, FileAlignment: 0x200,
		Characteristics:    pe.FileExecutableImage | pe.FileLargeAddress | pe.FileDLL,
		DllCharacteristics: pe.DllNXCompat | pe.DllDynamicBase | pe.DllHighEntropyVA,
		Subsystem:          pe.SubsystemNative, LinkerVersion: LinkerVersion,
		OSVersion: [2]uint16{10, 0}, ImageVersion: [2]uint16{10, 0}, SubsystemVersion: [2]uint16{10, 0},
		DLLName: d.FileName, ExportSection: dataSection,
		Exports:       []pe.Export{{Name: ExportName, Ordinal: 1, Target: pe.Ref{Section: textSection}}},
		Resources:     res,
		ResourceChars: pe.SecReadOnlyData | pe.SecDiscardable,
		RelocChars:    pe.SecReadOnlyData | pe.SecDiscardable,
	}
	if a == ARM64 {
		im.Sections = []*pe.Section{text, data}
		return writeARM64(im, enc, r)
	}

	// The x64 view's code and the code map (docs/arm64x.md §2.4 to §2.7). Each kind of code starts a page.
	type rng struct {
		ref  pe.Ref
		typ  uint32
		size uint32
	}
	ranges := []rng{{pe.Ref{Section: textSection}, pe.RangeARM64, uint32(len(text.Data))}}
	var sections []*pe.Section
	var x64Export pe.Ref
	var thunk *pe.Section
	for len(text.Data) < pageSize {
		text.Data = append(text.Data, 0)
	}
	cfg := &pe.Section{Name: cfgSection, Characteristics: pe.SecReadOnlyData}
	rm := &pe.Section{Name: rmSection, Characteristics: pe.SecReadOnlyData}
	var ldrAt uint32
	switch v {
	case VariantEC:
		// Page 2 of .text: the 4-byte entry-thunk offset, the ARM64EC function, its entry thunk.
		ec := uint32(len(text.Data))
		fn := ec + 4
		th := fn + 12
		text.Data = append(text.Data, make([]byte, 4)...)
		text.Data = append(text.Data, a64(a64AdrpX0, a64AddX0X0, a64Ret)...)
		text.Data = append(text.Data, a64(a64AdrpX8, a64AddX8X8, a64AdrpX16, a64LdrX16, a64BrX16)...)
		text.Fixups = append(text.Fixups,
			// thunk − function + 1, written as a displacement from the end of the slot (= the function) to thunk+1
			pe.Fixup{Offset: ec, Kind: pe.Rel32, Target: pe.Ref{Section: textSection, Offset: th + 1}},
			pe.Fixup{Offset: fn, Kind: pe.Arm64Page21, Target: tables},
			pe.Fixup{Offset: fn + 4, Kind: pe.Arm64Lo12, Target: tables},
			pe.Fixup{Offset: th, Kind: pe.Arm64Page21, Target: tables},
			pe.Fixup{Offset: th + 4, Kind: pe.Arm64Lo12, Target: tables})
		ranges = append(ranges, rng{pe.Ref{Section: textSection, Offset: ec}, pe.RangeARM64EC, uint32(len(text.Data)) - ec})
		ldrAt = th + 12 // the ldr of the dispatch_ret slot; its fixups follow once the slot is placed
		thunk = &pe.Section{Name: thkSection, Characteristics: uint32(code), Data: append([]byte(nil), x64ExportThunk...),
			Fixups: []pe.Fixup{{Offset: 10, Kind: pe.Rel32, Target: pe.Ref{Section: textSection, Offset: fn}}}}
		ranges = append([]rng{{pe.Ref{Section: thkSection}, pe.RangeAMD64, uint32(len(thunk.Data))}}, ranges...)
		sections = append(sections, thunk)
		x64Export = pe.Ref{Section: thkSection}
	default:
		// Page 2 of .text: the amd64 descriptor function, lea rax,[rip+tables]; ret.
		x := uint32(len(text.Data))
		stub, field, kind := stubFor(AMD64)
		text.Data = append(text.Data, stub...)
		text.Fixups = append(text.Fixups, pe.Fixup{Offset: x + field, Kind: kind, Target: tables})
		ranges = append(ranges, rng{pe.Ref{Section: textSection, Offset: x}, pe.RangeAMD64, uint32(len(stub))})
		x64Export = pe.Ref{Section: textSection, Offset: x}
	}
	sections = append(sections, text, data, cfg, rm)
	im.Sections = sections

	// The x64 view's export directory, in .data after the native one (the writer appends that one first, so this
	// one is appended here and placed before it; both are inside .data).
	expOff, expSize, err := pe.AppendExportDirectory(data, d.FileName,
		[]pe.Export{{Name: ExportName, Ordinal: 1, Target: x64Export}})
	if err != nil {
		r.Errorf("win.pe", loss.Where{}, "%v", err)
		return nil, r
	}

	// .00cfg: load configuration(s), helper slots, CHPE metadata, code map, code ranges to entry points.
	le := binary.LittleEndian
	nativeLC := uint32(0)
	cfg.Data = make([]byte, pe.LoadConfigSize64)
	le.PutUint32(cfg.Data, pe.LoadConfigSize64)
	lcs := []pe.Ref{{Section: cfgSection, Offset: nativeLC}}
	ecLC := nativeLC
	if v != VariantX64OneCfg {
		ecLC = uint32(len(cfg.Data))
		cfg.Data = append(cfg.Data, make([]byte, pe.LoadConfigSize64)...)
		le.PutUint32(cfg.Data[ecLC:], pe.LoadConfigSize64)
		lcs = append(lcs, pe.Ref{Section: cfgSection, Offset: ecLC})
	}
	// 14 helper slots of 8 bytes: dispatch_call_no_redirect, dispatch_ret, dispatch_call (check_call),
	// dispatch_icall (check_icall), dispatch_icall_cfg, get/set x64 information, dispatch_fptr, helper3..8.
	slotBase := uint32(len(cfg.Data))
	cfg.Data = append(cfg.Data, make([]byte, 8*14)...)
	slot := func(i int) pe.Ref { return pe.Ref{Section: cfgSection, Offset: slotBase + uint32(8*i)} }
	if v == VariantEC {
		text.Fixups = append(text.Fixups,
			pe.Fixup{Offset: ldrAt - 4, Kind: pe.Arm64Page21, Target: slot(1)},
			pe.Fixup{Offset: ldrAt, Kind: pe.Arm64Lo12Ldr64, Target: slot(1)})
	}
	// Code map and code ranges after the metadata.
	chpe := uint32(len(cfg.Data))
	const chpeWords = 29 // the 23 fields of version 2 and six helper slot RVAs (docs/arm64x.md §2.4)
	cfg.Data = append(cfg.Data, make([]byte, 4*chpeWords)...)
	codeMap := uint32(len(cfg.Data))
	for _, x := range ranges {
		at := uint32(len(cfg.Data))
		cfg.Data = append(cfg.Data, make([]byte, 8)...)
		// StartOffset = page RVA | type: an RVA fixup to (page + type); every range starts a page.
		cfg.Fixups = append(cfg.Fixups, pe.Fixup{Offset: at, Kind: pe.RVA32, Target: pe.Ref{Section: x.ref.Section, Offset: x.ref.Offset + x.typ}})
		le.PutUint32(cfg.Data[at+4:], x.size)
		if x.ref.Offset%pageSize != 0 {
			r.Errorf("win.arm64x.range", loss.Where{}, "code range at %s+%#x does not start a page", x.ref.Section, x.ref.Offset)
		}
	}
	entryRanges := uint32(len(cfg.Data))
	nThunks := uint32(0)
	if thunk != nil {
		nThunks = 1
		cfg.Data = append(cfg.Data, make([]byte, 12)...)
		cfg.Fixups = append(cfg.Fixups,
			pe.Fixup{Offset: entryRanges, Kind: pe.RVA32, Target: pe.Ref{Section: thkSection}},
			pe.Fixup{Offset: entryRanges + 4, Kind: pe.RVA32, Target: pe.Ref{Section: thkSection, Offset: uint32(len(thunk.Data))}},
			pe.Fixup{Offset: entryRanges + 8, Kind: pe.RVA32, Target: pe.Ref{Section: thkSection}})
		// Redirection metadata: thunk -> ARM64EC function, then an empty terminator entry.
		rm.Data = make([]byte, 16)
		rm.Fixups = []pe.Fixup{
			{Offset: 0, Kind: pe.RVA32, Target: pe.Ref{Section: thkSection}},
			{Offset: 4, Kind: pe.RVA32, Target: pe.Ref{Section: textSection, Offset: pageSize + 4}}}
	} else {
		rm.Data = make([]byte, 8) // the terminator only
	}
	// CHPE metadata fields (u32 each).
	w := func(i int, val uint32) { le.PutUint32(cfg.Data[chpe+uint32(4*i):], val) }
	rva := func(i int, t pe.Ref) {
		cfg.Fixups = append(cfg.Fixups, pe.Fixup{Offset: chpe + uint32(4*i), Kind: pe.RVA32, Target: t})
	}
	w(0, 2)
	rva(1, pe.Ref{Section: cfgSection, Offset: codeMap})
	w(2, uint32(len(ranges)))
	rva(3, pe.Ref{Section: cfgSection, Offset: entryRanges})
	rva(4, pe.Ref{Section: rmSection})
	for i, s := range []int{0, 1, 2, 3, 4} { // fields 5..9
		rva(5+i, slot(s))
	}
	// 10 AlternateEntryPoint, 11 AuxiliaryIAT: 0.
	w(12, nThunks)
	w(13, nThunks)
	rva(14, slot(5))
	rva(15, slot(6))
	// 16 ExtraRFETable, 17 ExtraRFETableSize: 0 (no .pdata).
	rva(18, slot(7))
	// 19 AuxiliaryIATCopy, 20/21 delay-load IATs, 22 HybridImageInfoBitfield: 0.
	for i := 0; i < 6; i++ {
		rva(23+i, slot(8+i))
	}
	for _, lc := range lcs {
		cfg.Fixups = append(cfg.Fixups, pe.Fixup{Offset: lc.Offset + pe.LoadConfigCHPEPointer, Kind: pe.Abs64,
			Target: pe.Ref{Section: cfgSection, Offset: chpe}})
	}
	im.Directories = []pe.Directory{{Index: pe.DirLoadConf, Target: lcs[0], Size: pe.LoadConfigSize64}}
	im.LoadConfigs = lcs

	// The ARM64X entries: Machine, export directory, and (two load configurations) the load-configuration
	// directory.
	im.DynamicRelocs = []pe.DynamicValue{
		{At: pe.Ref{Offset: pe.MachineField}, Size: 2, Value: uint64(pe.MachineAMD64)},
		{At: pe.Ref{Offset: pe.DirectoryField(pe.DirExport, true)}, Size: 4, ValueRVA: &pe.Ref{Section: dataSection, Offset: expOff}},
		{At: pe.Ref{Offset: pe.DirectoryField(pe.DirExport, true) + 4}, Size: 4, Value: uint64(expSize)},
	}
	if ecLC != nativeLC {
		im.DynamicRelocs = append(im.DynamicRelocs,
			pe.DynamicValue{At: pe.Ref{Offset: pe.DirectoryField(pe.DirLoadConf, true)}, Size: 4, ValueRVA: &pe.Ref{Section: cfgSection, Offset: ecLC}},
			pe.DynamicValue{At: pe.Ref{Offset: pe.DirectoryField(pe.DirLoadConf, true) + 4}, Size: 4, Value: pe.LoadConfigSize64})
	}
	if r.HasErrors() {
		return nil, r
	}
	return writeARM64(im, enc, r)
}

func writeARM64(im *pe.Image, enc *encoded, r *loss.Report) ([]byte, *loss.Report) {
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

// arm64Target decodes the descriptor function adrp x0, page; add x0, x0, #lo12; ret at rva and returns the RVA
// it yields.
func arm64Target(code []byte, rva uint32) (uint32, error) {
	if len(code) < 12 {
		return 0, fmt.Errorf("windows: descriptor function too short")
	}
	le := binary.LittleEndian
	adrp, add, ret := le.Uint32(code), le.Uint32(code[4:]), le.Uint32(code[8:])
	if adrp&0x9F00001F != a64AdrpX0 || add&0xFFC003FF != a64AddX0X0 || ret != a64Ret {
		return 0, fmt.Errorf("windows: descriptor function has an unknown shape % x", code[:12])
	}
	imm := int64((adrp>>29)&3 | ((adrp>>5)&0x7FFFF)<<2)
	if imm&(1<<20) != 0 {
		imm -= 1 << 21
	}
	page := int64(rva&^0xFFF) + imm<<12
	return uint32(page + int64((add>>10)&0xFFF)), nil
}

// ShippedVariant is the ARM64X shape Compile writes: the simplest one that passed ARM64 run 1 in native ARM64 and
// x64 processes and that a public linker also produces (docs/arm64x.md §5).
const ShippedVariant = VariantX64
