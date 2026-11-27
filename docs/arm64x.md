# ARM64X layout files: what the tool writes and why

Status (2026-11-22): §5 has the Windows results, the choice (`arm64x-x64`, a default build since VM-6) and the enabling defect of the sign-in test. The facts marked
**[VM]** are the reference specification's measurements of Windows' own files, everything else is the file format
as public sources describe it. The candidates are tested by the bundle of `tools/vmbundle -arm64`.

Sources (all recorded in `CLEAN-ROOM.md`):

- **[SPEC]** the reference specification, revision 10: `dll-format` §1.4, §1.7, §9.1, §11 item 9; `installation`
  §2.4; `installation/tests/vm/arm64/README.md`.
- **[MS-PE]** Microsoft, "PE Format", https://learn.microsoft.com/en-us/windows/win32/debug/pe-format (headers,
  directories, base relocations, load configuration layout).
- **[MS-X]** Microsoft, "Arm64X PE Files", https://learn.microsoft.com/en-us/windows/arm/arm64x-pe .
- **[MS-EC]** Microsoft, "Understanding Arm64EC ABI and assembly code",
  https://learn.microsoft.com/en-us/windows/arm/arm64ec-abi .
- **[LLVM]** the LLVM project at commit f1afebcbe1c6 (2026-11-22), Apache-2.0 with LLVM exception, read for the
  structures only: `llvm/include/llvm/Object/COFF.h` (`coff_load_configuration64`, `chpe_metadata`,
  `chpe_range_entry`, `chpe_code_range_entry`, `chpe_redirection_entry`, `coff_dynamic_reloc_table`,
  `coff_dynamic_relocation64`), `llvm/include/llvm/BinaryFormat/COFF.h` (`IMAGE_DYNAMIC_RELOCATION_ARM64X`,
  `IMAGE_DVRT_ARM64X_FIXUP_TYPE_*`), `lld/COFF/Chunks.cpp` and `Chunks.h` (how ARM64X entries, the code map, the
  code ranges, the redirection metadata and the export thunk are encoded), `lld/COFF/Writer.cpp` (which entries a
  hybrid image gets, the load configurations, section and page placement), `llvm/lib/Object/COFFObjectFile.cpp`
  (how a reader applies the entries), and the lld tests `arm64x-export.test`, `arm64ec-entry-thunk.s`,
  `Inputs/loadconfig-arm64ec.s`.
- **[LAB]** DLLs linked with LLD 23.1.5 (llvm-mingw 20261104) from assembly written for this purpose (no runtime
  library), dumped with `llvm-readobj` and `llvm-objdump`: a local test folder, not in this repository. Used only
  to compare; nothing made by LLVM is a test fixture or part of the repository.

## 1. What Windows ships and what the loader does [SPEC]

- Every layout file in `System32` of Windows 11 on ARM (build 26300) is ARM64X: Machine `AA64`, PE32+, a load
  configuration whose CHPE metadata pointer is set (version 2), a dynamic value relocation table (version 1) whose
  only symbol is 6 (ARM64X). Sections `.hexpthk .text .data .pdata .00cfg .a64xrm .rsrc .reloc`; ImageBase
  1_8000_0000, FileAlignment 0x200, Characteristics 0x2022, DllCharacteristics 0x4160, no entry point, one export
  (ordinal 1, `KbdLayerDescriptor`) at the start of `.text` (dll §1.7).
- Nine ARM64X entries, all "set a value": the Machine field (to 8664), the export, exception and load
  configuration directory entries (address and size each) and two adjacent values of the CHPE metadata (dll §1.7).
- Loads (installation §2.4, dll §9.1): the ARM64X file serves native ARM64 and emulated x64 processes; x86 processes
  load the WOW64 file from `SysWOW64`. The same file with its load-configuration directory entry zeroed (a plain
  ARM64 file) loads in native ARM64 processes and fails with error 193 in x64 processes. A file whose header says
  x64 but whose code is ARM64 crashes x64 processes and `ctfmon.exe`, and stays locked.
- The ARM64 descriptor function `adrp x0, page; add x0, x0, #lo12; ret` is inferred, not decoded (dll §1.4).

Consequences: an x64 process needs (a) the load configuration that leads the loader to the ARM64X entries, (b) an
x64 view whose export points at code an x64 process can run. The tables (8-byte pointers, the amd64 layout of
dll §2 to §6) can be the same bytes for both views: only code and headers differ.

## 2. The parts of an ARM64X file

### 2.1 Two views, one file [MS-X, LLVM]

The file on disk is the native ARM64 view ("by default, Arm64X binaries appear to be Arm64 binaries", [MS-X]). An
x64 (Arm64EC) process applies the ARM64X entries of the dynamic value relocation table to its mapped copy and sees
the "EC" view. Each entry writes a value at an RVA (the headers included), so the views can differ in any header
field or data word; in practice (Windows, lld) they differ in: Machine, the export directory entry, the exception
directory entry, the load-configuration directory entry, and values of the CHPE metadata [SPEC dll §1.7, LLVM
`Writer::createDynamicRelocs`].

### 2.2 The dynamic value relocation table [LLVM COFF.h, Chunks.cpp; LAB]

Found through the load configuration: `DynamicValueRelocTableOffset` (u32, load config +0xE0) is the offset inside
the section numbered `DynamicValueRelocTableSection` (u16, +0xE4, 1-based). lld places it in `.reloc` right after
the base-relocation blocks; the base-relocation directory size covers only the base relocations [LAB].

```
coff_dynamic_reloc_table   { u32 Version = 1; u32 Size }             Size = bytes after this header
coff_dynamic_relocation64  { u64 Symbol = 6 (ARM64X); u32 BaseRelocSize }
blocks                     { u32 PageRVA; u32 BlockSize; entries... } each block padded to 4 bytes
entry (u16)                bits 0-11 offset in page, bits 12-13 type, bits 14-15 meta
  type 0 zero fill   meta = log2(size)                       no payload
  type 1 value       meta = log2(size), size 2, 4 or 8       payload: the value, `size` bytes
  type 2 delta       meta bit 14 sign, bit 15 scale (×8, else ×4); u16 payload
```

### 2.3 The load configuration [MS-PE, LLVM COFF.h, Writer.cpp]

64-bit `IMAGE_LOAD_CONFIG_DIRECTORY`, Size 0x140 in lld's inputs. Fields used: `Size` (+0x00),
`CHPEMetadataPointer` (+0xC8, a VA, so it has a DIR64 base relocation), `DynamicValueRelocTableOffset` (+0xE0),
`DynamicValueRelocTableSection` (+0xE4). Everything else is 0 (no security cookie, no CFG tables, GuardFlags 0).
A hybrid image has two: the native one and the EC one; the ARM64X entries switch the directory entry to the EC one.
lld copies the CHPE pointer into the native one and writes the table's offset and section into both.

### 2.4 CHPE metadata, version 2 [LLVM COFF.h, Inputs/loadconfig-arm64ec.s, Writer.cpp]

23 u32 fields: `Version`(2), `CodeMap`, `CodeMapCount`, `CodeRangesToEntryPoints`, `RedirectionMetadata`, five
`__os_arm64x_dispatch_*` slot RVAs (call_no_redirect, ret, call, icall, icall_cfg), `AlternateEntryPoint`,
`AuxiliaryIAT`, `CodeRangesToEntryPointsCount`, `RedirectionMetadataCount`, `GetX64InformationFunctionPointer`,
`SetX64InformationFunctionPointer` (slot RVAs), `ExtraRFETable`, `ExtraRFETableSize`, `__os_arm64x_dispatch_fptr`
(slot RVA), `AuxiliaryIATCopy`, and in version 2 `AuxiliaryDelayloadIAT`, `AuxiliaryDelayloadIATCopy`,
`HybridImageInfoBitfield`. lld's test input `Inputs/loadconfig-arm64ec.s` continues the structure with six more slot RVAs (`__os_arm64x_helper3` to `helper8`); the tool writes those too, 29 words in all, each pointing at a slot of its own. The slots are 8-byte words the loader fills with the emulator's helpers; the entry thunk
of §2.6 jumps through the `dispatch_ret` slot [MS-EC]. lld's test input puts the slots in the `.00cfg` section, a
read-only data section; Windows' files have a `.00cfg` section too.

- **Code map**: entries `{u32 StartOffset | type; u32 Length}`, sorted, type in the two low bits: 0 ARM64, 1
  ARM64EC, 2 x64. lld starts a new page at every change of code type, so a range starts on a page boundary
  [LLVM Writer.cpp, ECCodeMapChunk; LAB]. "Arm64EC binaries contain a range table for Arm64EC versus x64 code
  pages" [MS-EC].
- **Code ranges to entry points** `{StartRva, EndRva, EntryPoint}` and **redirection metadata** `{Source,
  Destination}` (terminated by an empty entry, in `.a64xrm`): one entry per export thunk, from the thunk to its
  ARM64EC target [LLVM CHPECodeRangesChunk, CHPERedirectionChunk].

### 2.5 Exports per view [LLVM arm64x-export.test; LAB]

Each view has its own export directory: the native one names the ARM64 function, the EC one the x64-callable
address. Both can name `KbdLayerDescriptor` as ordinal 1. With an ARM64EC function, the EC export points at an x64
**export thunk** ("fast-forward sequence", [MS-EC]) in `.hexpthk`: `mov rax,rsp; mov [rax+20h],rbx; push rbp; pop
rbp; jmp rel32 <EC function>; int3; int3` (16 bytes; the instruction sequence is the one [MS-EC] prints).

### 2.6 ARM64EC code: the function and its entry thunk [MS-EC]

An x64 caller reaching an ARM64EC function makes the emulator read the four bytes before the function, mask the low
two bits and add them to the function's address: that is the **entry thunk**, called with the target in `x9` and
`lr` holding the x64 return address. The thunk converts the calling convention, and returns through the
`__os_arm64x_dispatch_ret` helper. Register mapping: RAX is `x8` [MS-EC, exit-thunk example]. lld writes the prefix
as `thunk − function + 1` [LLVM Writer.cpp]. For the descriptor (no arguments, returns a pointer) the thunk does not
need to call the function: it can compute the same pointer itself (as [MS-EC]'s custom entry thunk embeds the
work), which keeps it a leaf without stack use, so no unwind data is needed:

```
adrp x8, KBDTABLES ; add x8, x8, #lo12(KBDTABLES)      ; RAX = the tables
adrp x16, dispatch_ret_slot ; ldr x16, [x16, #lo12] ; br x16
```

### 2.7 x64 code inside the file [MS-EC, LAB]

An ARM64EC image may hold x64 code: pages not in an ARM64EC range run under the emulator ([MS-EC]: x64 code
versus Arm64EC code pages). lld links an x64 object into an ARM64X DLL as an x64 range of the code map [LAB]. So
the x64 view can also export a plain x64 descriptor function (`lea rax,[rip+x]; ret`, the amd64 stub) on a page of
its own, with no thunk, no ARM64EC code and no redirection entries.

## 3. Candidates the tool builds

All share: Machine AA64, PE32+, ImageBase 1_8000_0000, SectionAlignment 0x1000, FileAlignment 0x200,
Characteristics 0x2022, DllCharacteristics 0x0160 (no GuardCF: the file has no CFG tables), the tables in `.data`
exactly as for amd64 (same rules: KBDTABLES below RVA 0x10000, every pointer inside `.data`, size limit), the native
function `adrp x0; add x0; ret` at the start of `.text`, resources and `.reloc` as for amd64.

| build / variant | x64 view | code map | load configs | entries |
|---|---|---|---|---|
| `arm64` (control) | none (no load configuration) | none | none | none |
| `arm64x` = `arm64x-ec` (primary; Windows' and lld's shape) | export → thunk in `.hexpthk` → ARM64EC function with entry thunk | ARM64, ARM64EC, x64 | native + EC | Machine, export dir, load-config dir (5) |
| `arm64x-x64` | export → x64 function (amd64 stub) on its own page | ARM64, x64 | native + EC | 5 |
| `arm64x-x64-onecfg` (simplification) | as `arm64x-x64` | ARM64, x64 | one, shared | Machine, export dir (3) |
| `arm64x-ec-rebased` (bundle entry only) | `arm64x-ec` loaded after its preferred image base was taken | | | |

What is left out compared with Windows' files, and why: `.pdata` and the exception directory entries (every
function is a leaf; [MS-EC] needs unwind data only for non-leaf functions), the two CHPE `ExtraRFETable` entries
(they describe `.pdata`), the debug directory, CFG.

## 4. Unknowns (what the VM run decides)

1. Whether the loader accepts an ARM64X image without `.pdata`/exception directory in either view.
2. Whether x64 code reached directly through the EC export (variant `arm64x-x64`) works as well as the thunk path.
3. Whether one shared load configuration is enough (`-onecfg`).
4. Whether the loader writes the dispatch slots in a read-only `.00cfg` section (the `arm64x-ec` entry thunk
   depends on `dispatch_ret` being filled).
5. Whether `ctfmon.exe` and sign-in behave with the built file as with Windows' file (installation §2.4); the runner
   loads through `LoadKeyboardLayoutW` only. "After sign-in" stays unverified.
6. A physical ARM64 PC (the run is an emulated guest).

## 5. Result of ARM64 run 1 and the shape the tool ships

**ARM64 run 1** (yakbd VM run 2, 2026-11-22; Windows 11 ARM64 build 26300 in an emulated guest, fresh overlay,
console session, elevated; bundle `tools/vmbundle -arm64` at 3e331b7): the three known-good controls (Windows' own
US layout under a test registration) passed in every process kind. Native ARM64 runner 16 of 16, x64 runner 15 of
16, 32-bit runner 7 of 7. Every ARM64X candidate (`ec`, `x64`, `x64-onecfg`, `ec` rebased) loaded and typed in the
native ARM64 and the x64 process, on all six layouts where it was built; the WOW64 files typed in the 32-bit
process. The plain `arm64` control loaded in the native ARM64 process and failed in the x64 process with
`LoadKeyboardLayoutW` NULL, error 193, as installation §2.4 predicts. Nothing crashed or hung.

**Choice: `arm64x-x64` (VariantX64)** is the ARM64X file `yakbd compile` writes. Why:

- It is the simplest shape that passed and that a public linker also produces (lld links an x64 object into an
  ARM64X DLL in exactly this shape, [LAB]): no ARM64EC code, no entry thunk, no export thunk, no redirection
  metadata, and so no dependence on the loader filling the helper slots of `.00cfg` at all.
- `arm64x-ec` (Windows' own shape) passed too but needs all of those parts; nothing in the run shows an advantage
  for a layout file, whose only code is the descriptor function. It stays available as a variant.
- `arm64x-x64-onecfg` passed as well and is smaller by one load configuration and two ARM64X entries, but no linker
  or Windows file has that shape (both lld and Windows switch to a separate load configuration for the x64 view),
  so a later Windows loader that checks the EC load configuration could refuse it. The saving (0x140 bytes) does
  not pay for that risk.

Still open before `arm64x` becomes a default build (kbl D44): the layout installed for real and found in the
session after sign-in, with `ctfmon.exe` running, typed through real input in a native ARM64, an x64 and an x86
process. The sign-in bundle (`tools/vmbundle -arm64 -signin`, runner `-signin install|check|remove`) tests
exactly that for the shipped shape, with Windows' shape for one layout as a fallback.

**Sign-in run 1** (bundle `ta-arm64-2`, twice, same result): `ctfmon.exe` ran in the session; the controls and all
real-input lines passed in all three process kinds. But the native ARM64 runner, which ran first, found only the
first of the seven installed layouts in its session after sign-in (the other six it had to load itself); the x64 and
x86 runners after it found all seven. The reference spec does not say whether Windows loads every layout of the list
at sign-in (installation §4.2 cites a public source for "Preload is loaded at sign-in"; its ARM64 run had two custom
layouts, both loaded). Bundle 3 (`tools/vmbundle -arm64 -signin`, runner `-signin list`) tells Windows behaviour from
our file: the same flow on x64 Windows with x64 builds, and on ARM64 a listing that loads nothing, in every process
kind, before any check.

**Sign-in bundle 3** (`ta-arm64-3`, the x64 Hyper-V guest and the ARM64 guest, same result): of the seven layouts the
runner had installed, only the first (KLID `a4000409`) was ever in the user's list (`User Profile`, `Substitutes`,
`Preload`), and that one was in the session after sign-in in every pass and every process kind; the other six were
never in the list, and `GetKeyboardLayoutList` never grew. So the cause of sign-in run 1 was the runner's enabling
step, not Windows' sign-in and not the file. The shipped `arm64x-x64` file passed the gate of kbl D44 (VM-6, D66):
in the session after sign-in with `ctfmon.exe` running, typed through `ToUnicodeEx` and real input in native ARM64,
x64 and x86 processes. **`arm64x` is in the default build set since then** (rule `builds`, D39), and
`windows.architectureUnsupported` no longer applies to it.

**The enabling defect.** The runner called `InstallLayoutOrTip("0409:<KLID>", 0)` once per layout, seven times in
quick succession from one process. Every call returned TRUE, but only the first KLID arrived in `User Profile` and
`Substitutes`. Not established is why (the calls return before the list is written, a later call of the same
language overwriting an earlier one, or something else); the reference spec's own tests enabled one or two layouts,
with time between them. The runner now enables all layouts in ONE call, entries joined by `;` (installation §4.3),
reads the user's list back, enables a missing layout again on its own after a pause, and fails the install line of
any layout that never arrives. Advice for any installer that enables several layouts (or runs several installers
back to back): read `User Profile` back after `InstallLayoutOrTip` instead of trusting its return value.
