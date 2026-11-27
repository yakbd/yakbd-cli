# Importing existing layout DLLs

`yakbd convert some.dll some.kbl.json` (or `-o some.kbl.json`, or `some.klc`) reads any Windows keyboard-layout
DLL that follows the reference specification's `dll-format` and writes a `.kbl.json` file whose Windows build
gives the same tables again. `yakbd compile some.dll` builds a DLL set from it directly. Code:
`target/windows/decode.go` (`ReadImage`, the reader), `convert/dllkbl` (DLL → model), `target/windows/compare.go`
(`Canonical`, `Differences`: what "the same tables" means).

Sections cited as "dll §4.2" are `spec/dll-format/README.md` of the reference specification, revision 10;
"OBS D9" is the MSKLC observations (`dll-format.md`). Decisions marked [YAKBD] are this tool's.

## 1. What the reader accepts

| item | accepted |
|---|---|
| machine | x64 (`8664`, PE32+), i386 (`014C`, PE32). The i386 file is a WOW64 build (8-byte pointer slots) or a native x86 build (4-byte pointers): told apart by the `KBDTABLES` itself (the high dwords of 8-byte slots are zero; in an x86 file the same bytes are `pVkToWcharTable` and `pKeyNames`), and by the descriptor function when the tables leave it open (`cdq` or `xor edx, edx` = WOW64) |
| ARM64 / ARM64X (`AA64`) | the native ARM64 view, which is the file on disk (`docs/arm64x.md` §2.1): descriptor `adrp x0; add x0; ret`, the 64-bit table layout. The x64 view of an ARM64X file (its own export and code) is not read; the tables are the same bytes for both views in the files the ARM64X research (docs/arm64x.md) describes. Listed in `dll.containerIgnored` |
| descriptor (export ordinal 1, found by ordinal, dll §1.3) | the shapes of dll §1.4, and what other compilers emit for the same function: up to three `jmp` (incremental-link thunks), a frame-pointer prologue/epilogue (`push rbp; mov rbp, rsp … pop rbp`), `mov rax, imm64`; any other code is refused (`dll.malformed`) [YAKBD]. The function is decoded, never run |
| sections | any number and order, any names; tables may lie in any section with raw data. Only raw data is read: a table in the zero-filled tail beyond `SizeOfRawData` is refused, because Windows' kernel reads the file, not the mapped image (dll §9), and what it reads there is not established |
| table placement | any order of the sub-tables, any padding, `KBDTABLES` anywhere (dll §3, OBS D10) |
| `KBDTABLES` | full and short form (dll §3). Short form is recognised when a table starts inside the 8 bytes of the full form or the file ends there; dwType/dwSubType are also not kept when they are implausible (type > FF, subtype > FFFF: what a linker left behind a short structure, OBS D9). Either case reports `dll.keyboardTypeNotTrusted`. Plausible values are not kept either unless `-keep-keyboard-type` is given (coverage §11 item 11; `dll.keyboardTypeNotTrusted` when they are not 0) |
| zero pointers | pDeadKey, the three key-name tables, E0, E1, pLigature, pVkToWcharTable (dll §9.1); pCharModifiers and pusVSCtoVK must be set |
| export ordinal 2 | `KBDNLSTABLES` read into `targets.windows.nls` (carried, not compiled: `windows.nlsNotCompiled` on build); a table that cannot be read is skipped with `dll.nlsUnreadable` |
| exports 3, 5, 6 (dispatchers, dll §6.3) | they are code that names other files; nothing is read: `dll.dispatcherNotRead`. Their own ordinal 1 table is imported when it has a known descriptor shape |
| resources | strings 1000, 1100, 1200 per language → `targets.windows.strings`; the version resource's file and product version, LegalCopyright and FileDescription → `targets.windows.versionInfo` (and `metadata.copyright`); CompanyName → `metadata.attribution` only, never `metadata.organization` or `versionInfo.companyName`: a rebuilt file is made by yakbd (D58, dll §1.6). Everything else, and a resource that cannot be read (the layout is still read), → `dll.resourceDropped` |

**Never trust the file.** Every pointer is converted with bounds checks and must lead into raw data; every
array walk is bounded (64 character tables, 4096 rows per table, 65536 dead-key and ligature entries, 256
key-name and VSC_VK entries, 4096 units per key name, 256 NLS entries, wMaxModBits ≤ FF); address arithmetic
fails instead of wrapping; the reader has a work budget of 16 × the file size + 1 MiB bytes read, so overlapping
or shared tables cannot amplify; `cbSize` must be `2 + 2n` and `cbLgEntry` `4 + 2n` before rows are walked
(dll §4.2, §4.7: 0 hangs Windows); the PE reader visits at most 4096 resource entries and reads names of at most
1024 bytes; files above 64 MiB are refused. `FuzzReadImage` and `FuzzImport` (Read → convert → `.kbl.json`
writer → Windows build) cover this; CI runs their seeds, the fuzzer runs locally (`docs/fuzzing.md`).

## 2. What goes where

The import follows `coverage/windows.md` of the kbl specification (§1–§6) and its importer conventions (§10):

- **Positions**: one per entry of `pusVSCtoVK` (index = scan code), of the E0 and of the E1 table, ids from
  SPEC Appendix A or `sc<hex>`; `targets.windows.keys.<p>.vk` and `.scanCodeFlags` pinned for every one.
  `numLock: true` where the entry has `KBDNUMPAD` and a navigation VK (windows-behaviour §3.2).
- **Table A off**: `useTableA: false` is written (D54), so the build holds exactly the DLL's scan codes: no Table A
  row is added for a scan code the DLL leaves unmapped, and no position gets a VK the DLL does not give it. A scan
  code whose entry is VK FF *with* flags is pinned `vk: "_none_"` with those flags; such a position is written as
  FF with its flags, left out of E0/E1 when it has no flags,. Under `useTableA: false` several scan codes may share a VK
  without `windows.vkDuplicate` (D59).
- **Modifiers**: `VK_TO_BIT` and `ModNumber` pinned exactly; bits 8, 10, 20, 40, 80 become `held` custom
  modifiers `kana`, `roya`, `loya`, `bit40`, `grpSelTap` on the positions with the entry's VK (a position
  `vk<NAME>` without scan code when none has it). One layer per reachable column, id `s<lowest state>`, with
  the modifier sets of every state that reaches it (AltGr as in §10 item 2: `KLLF_ALTGR` gives `altR` and
  `ctrl alt` for 6, `altL` for 4).
- **Rows**: tables walked in order; the row after a row with `WCH_DEAD` is its dead-character row and the row after
  an `SGCAPS` row its Caps Lock row, whatever their VK (dll §4.2); one row serves both in the shipped SGCAPS+dead
  shape. The first row of a VK gives the bindings of every position whose row VK it is (a Num-Lock position's
  row VK is its Num-Lock-on VK); a row of no position, of a modifier key, or one that gives nothing at all is an
  `extraRows` entry. `WCH_LGTR` cells take their ligature by (VK, column). Caps Lock layers `capsS0/1/6/7` as in
  §10 item 3; where Caps Lock gives nothing although the state without it gives something (an empty SGCAPS Caps
  Lock cell, a CAPLOK key with one empty column), the binding is the explicit nothing `{"output": ""}` (D64,
  VM-7). The Caps Lock row of an SGCAPS key is read in columns 0 and 1 only (targets/windows.md §6 rule 1, D65);
  in the shared FF row of an SGCAPS key with dead keys, every cell under a `WCH_DEAD` (AltGr columns included) is a
  dead character, and its other cells above column 1 are `dll.unreachableData` (VM-8 tests whether Windows reads
  them). Row attributes, table widths and the order of tables and rows are pinned (`keys.<p>.attributes`,
  `tables`), so tables of equal width stay apart and Windows' order (3, 4, 2, 1, …) is kept.
- **Dead keys**: one per dead character, id `u<hex>`, entries in file order, `DKF_DEAD` results as dead-key
  references, names from `pKeyNamesDead`.
- **Flags and the rest**: `localeFlags`, `kbdVersion`, `scanCodeTableSize` pinned (`keyboardType` only with `-keep-keyboard-type`); `KLLF_SHIFTLOCK`
  and `KLLF_LRM_RLM` also as `settings`; key names → `positions.<p>.name`; `dllName` from the file name (the
  export directory's name is not reliable, dll §1.3); `metadata.id` from string 1200, `metadata.name` from string
  1000 (the `-langid` LANGID first, else the lowest), else FileDescription, else the file name.
- **Registration**: a DLL has none. `-langid` (convert and compile) pins `registration.langId`; KLID, Layout Id and
  Layout Text stay to the installer; `dll.registrationMissing` (accept) lists what is not set.
- **Kana lock**: rows with `KANALOK` give a custom `lock` component `kanaLock` on the Kana key, pinned as
  `modifiers.kanaLockComponent`, whose layer holds the Kana column's cells of those rows (coverage §11 item 5).
- **CompanyName** goes to `metadata.attribution` only (D58).
- **Normalisation** (kbl D74): the DLL and `.klc` importers write `settings.normalization: "disabled"` only when an
  imported output is not already NFC, so an import never rewrites an output (TN, `convert/dllkbl`, `convert/klckbl`).

## 3. Same tables

`windows.Differences(a, b)` compares two `Tables` after `windows.Canonical`, which removes what Windows never
reads or what carries no meaning: rows after the first for a VK, FF rows that follow no dead key, empty tables,
cells of dead-character rows outside the dead columns, the order of E0/E1 entries (OBS D23), of key names, of
dead-key names and of ligatures, the order of `DEADKEY` entries of different dead characters (lookups take the
first match of one key, dll §4.3), entries after the first for the same key, E0/E1 entries `{sc, FF}`, empty key
names, unreferenced ligatures, `nLgMax`, a one-unit ligature that is an ordinary character, and `DEADKEY` flag
bits other than `DKF_DEAD` (dll §4.3: ignored). Everything else must be equal. The tests hold the import to this:

- every corpus layout (the 47 `.klc` inputs, the four kbl examples, the CLDR keyboards) built for x64, WOW64 and
  x86 by the tool's writer imports and builds back to the same tables, with no import warning beyond the expected `dll.registrationMissing` and `dll.resourceDropped`, and the imported file builds with no policy of the test (only what the import itself writes into `targets.windows.onLoss`: `windows.capsNotExpressible` and `windows.sgcapsLigature` where a Caps Lock cell is reported as `dll.capsCellUnexpressed`);
- 300 random Windows-like tables (dll §8 shapes: table orders, two tables of one width, unmapped and moved keys,
  `bMaxVSCtoVK` 60/7C/7F/80, FF entries with flags, E0 subsets in any order, dead keys and chains, ligatures,
  CAPLOK, SGCAPS, CAPLOKALTGR, KANALOK, extra rows, a Kana or OEM_8 column, keyboard types) written by a second, independent
  encoder in the tests (`convert/dllkbl/foreign_test.go`: random sub-table order and padding, extra sections,
  other descriptor shapes, empty arrays instead of zero pointers, short `KBDTABLES`, NLS and dispatcher exports),
  and 100 through the tool's own writer, round-trip on x64, WOW64 and x86 (and ARM64, ARM64X through the tool's own writer) with no unexpected warning;
- odd tables (`odd_test.go`) report their loss kind, and differ after the round trip only where the kind says
  something is not carried; damaged and hostile files are refused with `dll.malformed` or read, never a panic;
- an imported DLL written as `.klc` and read back builds the same tables, or the export reports
  `klc.notRepresentable` (the `.klc` exporter rebuilds without `useTableA: false` and without the pinned table
  order and reports either pin when it changes the tables: a `.klc` file holds neither, OBS D13, D20).

## 4. Loss kinds of the import

The kinds of SPEC §17.3 with prefix `dll.` (D55, D60, D61; defaults from `policy`):

| kind | default | what |
|---|---|---|
| `dll.malformed` | fatal | not a readable layout DLL (the message says why), or a dead-key cell without its following row |
| `dll.architectureUnsupported` | error | a Machine other than x64, i386, ARM64 |
| `dll.containerIgnored` | accept | sections other than the shipped ones, entry point, imports, debug directory, exports above 2, the x64 view of an ARM64X file |
| `dll.keyboardTypeNotTrusted` | accept | dwType/dwSubType not kept (§1) |
| `dll.resourceDropped` | warning | resources and version strings not carried |
| `dll.unreachableColumn` | warning | a ModNumber entry ≥ 15, or a column only states with an unnamed bit reach |
| `dll.duplicateRow` | warning | a second row for a VK |
| `dll.ligatureMismatch` | warning | a `WCH_LGTR` cell without its entry (Windows gives nothing), or an entry no cell uses |
| `dll.unknownFlags` | warning | row attribute bits 10, 20, 40; locale-flag bits other than the three |
| `dll.registrationMissing` | warning | `langId` (unless `-langid`), `klid`, `layoutId`, `layoutText`, `productCode`, `customLanguageName`, and `dllName` when the file name is not one |
| `dll.unreachableData` | warning | stray FF rows, empty tables, cells of dead-character rows outside the dead columns, second DEADKEY entries for one pair, second E0/E1 entries for one scan code, second or empty key names, columns beyond 15, a `VK_TO_BIT` entry with no bit |
| `dll.unusualShape` | warning | a dead-character row whose VK is not FF, an SGCAPS row at the end of its table |
| `dll.modifierDropped` | error | a `VK_TO_BIT` entry with several bits (D61): the file is not imported |
| `dll.capsCellUnexpressed` | warning | the Caps Lock row of an extra row (the only case left: an empty Caps Lock cell is the explicit nothing `{"output": ""}` of D64; Caps Lock cells above column 1 are `dll.unreachableData`, D65) |
| `dll.dispatcherNotRead` | warning | exports 3, 5, 6: code, nothing to carry into `dispatcher` |
| `dll.nlsUnreadable` | warning | export 2 present, its table unreadable |

`DEADKEY` flag bits other than `DKF_DEAD` are not kept and not reported (coverage §5, §11 item 9).

## 5. Windows check

The import bundle (`tools/importbundle`): inside a disposable guest, every `kbd*.dll` of System32 (x64
build) and SysWOW64 (WOW64 build) is imported and rebuilt by the Windows build of `yakbd`, and
`yakbd-wincheck -import` loads original and rebuilt under test names and compares `ToUnicodeEx` over all
virtual keys, all modifier combinations with Caps Lock off and on, and every dead key followed by every key.
Only counts and per-file categories leave the guest; passing files are only counted. A failing file also gets
counts of mechanism tags (`wincheck.Classify`: modifier column, Caps Lock, attributes, cell kinds, dead-key entry
kinds, file features) and the names of the table fields that differ, never characters. A control run
(`-control`: each original against itself, no yakbd) on the first files checks the harness first. A file that
builds only with `-accept windows.*,general.*` is counted under that name and compared anyway.

## 6. MSKLC-built DLLs

Goal (owner): the same `.klc` built by MSKLC 1.4 and by yakbd types the same on Windows, and yakbd imports
MSKLC's DLL correctly. MSKLC runs only inside a disposable guest; its DLLs never leave it, and nothing it produces
is in this repository (`CLEAN-ROOM.md`). What the reader must accept of MSKLC's files is OBS D1 to D29
of the reference specification (its MSKLC observations, `dll-format.md`).

### 6.1 Host tests

- `convert/dllkbl/msklc_test.go`: the second encoder (`foreign_test.go`) writes the MSKLC file shape: one section
  `.data` (initialised data, execute, read) holding the tables, the export directory and the descriptor
  (OBS D2, D4), the short `KBDTABLES` (D9) and the E1 table directly behind it at `KBDTABLES` + 34 / + 60, the
  layout of the amd64 builds Windows 11 refuses (D29). 60 random tables × amd64, WOW64, x86 import and build back
  to the same tables with no unexpected loss kind; the reader sees the short form. Three MSKLC quirks: the
  Caps Lock row of an SGCAPS key filled with 0000 beyond column 1 (D14: reported as `dll.unreachableData`, never
  read by Windows, D65), the one FF row of an SGCAPS key with a dead key (D15: the shared shape of §2, same tables),
  and a ligature entry that carries the shift-state value instead of the column (W3: `dll.ligatureMismatch`, both
  halves, as Windows finds no entry).
- `tools/msklcbundle/kbldiff`: what "the same layout" means between `yakbd convert x.klc` and `yakbd convert x.dll`.
  The two files are written differently by design (coverage §10 and §11), so they are compared as facts keyed by
  scan code and modifier set: bindings, key names, dead keys (first entry per base, dll §4.3), settings, custom
  modifiers, locale flags, modifier bits and ModNumber, extra rows, strings (where both have them; an empty string
  counts as absent), metadata id and copyright (not the name: the two importers take it from different places; string 1000 is compared as a string), and the maker (`organization` of the `.klc` import against
  `attribution` of the DLL import, D58). Not compared: provenance, the version line and version resource (MSKLC
  writes its own names there, OBS D7), `useTableA`, pinned tables, scan-code flags, `numLock` and VKs where only
  one file pins them, keyboard type, registration, and the positions only the DLL import has (a position for
  every scan code; their effect is checked by the table comparison). A Caps Lock binding only one file has is
  equal when it is the other file's binding without Caps Lock (Caps Lock fallback: the build writes it into the
  Caps Lock row, the import reads it back). `TestControl`: every corpus `.klc` and targeted case built by yakbd
  for x64, WOW64, x86 and imported again shows no difference; `TestDetects` checks that real differences remain.
- `tools/msklcbundle/guest` `TestDryRun`: the whole guest procedure on the host with a stand-in for kbdutool
  (yakbd's own writer), so every import must pass.

### 6.2 The guest bundle

`go run ./tools/msklcbundle -o DIR` writes the bundle (README.txt has the runner's steps): the Windows builds of
yakbd, both yakbd-wincheck runners and `tm-guest.exe`, `cases/*.klc` as UTF-16LE with CRLF, and `cases.txt` (case,
LANGID from `LOCALEID`, source, what the reference specification predicts). Cases: the 46 `.klc` inputs of the
corpus, the seven targeted cases of `tools/msklcbundle/cases` (dead keys with a chain and AltGr dead keys; SGCAPS,
CAPLOKALTGR and CAPLOK+CAPLOKALTGR keys; ligatures of 2 to 5 units and a surrogate pair; AltGr with Ctrl and
Shift+Ctrl columns, two languages and key names; a Kana state; two `MODIFIERS` entries numbered as kbdutool
numbers them, K16; `SHIFTLOCK` and `LRM_RLM`), and an extra set `x-*`: the kbl examples and CLDR keyboards as
yakbd itself exports them to `.klc` (`convert -msklc`; those it cannot export are listed in the README with the
reason). `tm-guest.exe` then, inside `w11x64`:

1. **control**: the host's `TestControl` with the Windows build of yakbd;
2. **build**: `kbdutool -u -v -w -m|-o|-x in.klc` per case in a folder of its own (OBS tests/vm/wb-t1.ps1),
   recording the exit code and the numbers of errors and warnings only, and `yakbd compile -emit-expectations`;
3. **type**: two yakbd-wincheck bundles (MSKLC's DLLs, yakbd's DLLs) with yakbd's expectations of the same `.klc`,
   each run by the 64-bit runner (x64 files) and the 32-bit runner (WOW64 files). The x86 files are not typed
   (OBS I33, I40: no 32-bit Windows guest with MSKLC);
4. **import**: each MSKLC DLL imported by `yakbd convert`, compared with the `.klc` import (kbldiff), built again
   and compared with the MSKLC DLL (`windows.Differences`), and, as information, MSKLC's tables against yakbd's for
   the same `.klc` and build.

What a compiler fills in by itself where the `.klc` says nothing (metadata, the maker, the strings: MSKLC writes
its own names there, OBS D6, D7) is compared only where both files state it, and `import.txt` never prints the
DLL side of such a fact. Builds with the same result share one line; counts per fact kind and table field end
the file.
A case yakbd refuses (28 corpus inputs are refused on purpose) is still built by kbdutool
and its DLL imported and rebuilt; it has no expectations and no semantic comparison.

### 6.3 Results

VM run of 2026-11-26 (tool 3dd6f39, Windows 11 x64). The import read every
MSKLC DLL back exactly wherever its file built; one import fix (states with bits no VK_TO_BIT entry owns are
written as invalid, `dll.unreachableColumn`), two `.klc` import fixes (SGCAP `-1` is the explicit nothing; implied
rows of a VK no key gives are extra rows), two exporter fixes (supplementary characters as LIGATURE lines, rows
with a cell for every state). MSKLC behaviours the reference specification predicts and the run confirmed: D15,
D26, D29.

VM run 2 (bundle b887c18, w11x64): control 96/0; typing yakbd 32/32 in 64- and 32-bit processes, MSKLC 27/28 and
28/28 (the one failure: `mod-kana` amd64 does not load, D29); import 132 of 144 MSKLC DLLs the same, none refused, the
12 others the predicted D26 and D15 behaviours, every round trip exact. The scan-code tables differ in every DLL by
keys Table A does not have (kbl D83).
### 6.4 KbdEdit is out

KbdEdit (https://kbdedit.com/, read 2026-11-25) has no free edition that builds layouts: the free download is a
demo version, and its page (https://kbdedit.com/demo.html) says that in the demo changes to DLL layout files
cannot be saved, new layouts cannot be created, and "you cannot create an actual layout DLL file"; the editions
that can (Premium, Personal, Lite, https://www.kbdedit.com/manual/editions.html) are paid. A KbdEdit-built DLL
therefore cannot be made legitimately without buying a licence, so KbdEdit-built files are not part of the
comparison; the runner does not download KbdEdit. The reader's tests with foreign shapes (§3) stand in for
other compilers' files.
