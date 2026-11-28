# Architecture

One Go module, standard library only, one binary (`cmd/yakbd`). Every package has one job; data flows through
plain Go values, and every step returns a `*loss.Report` next to its result.

```
       .klc                      .kbl.json                    CLDR 3.0 XML              (formats)
     format/klc                format/kbljson                 format/cldr
         |                     (schema + SPEC §19)        (imports inlined)
   convert/klckbl                    |                            |
         +-------------------> model.Layout <---------------------+   the hub (SPEC 0.2, holds 0.1)
                                     |  resolve, check, policy (onLoss)
                                     v
                     target/windows/build: default rules of targets/windows.md,
                     windowsDefault.<rule> entries, spec loss kinds, Pin
                                     v
               target/windows: Tables / DLL (Windows-shaped intermediate)
                                     |  Compile / CompileAll        ^ Read
                                     v                              |
                     pe: PE/COFF writer and reader (no keyboard knowledge)
                                     v
              BuildSet: x64, wow64, x86, arm64x files + expectations
                                     v
          installer: one program with every build (stub + payload), docs/installer.md
```

The `.klc` export runs the same Windows build and turns its tables back into a `.klc` file, so a `.klc` file and
a DLL always come from the same rules. All paths go through the model: `.klc` → `.kbl.json` → `.klc`, CLDR →
`.kbl.json` → CLDR, and every input → DLL set.

## Packages

| package | job |
|---|---|
| `loss` | the report: entries with a class (diagnostic, loss, defaulted), a severity (info, accept, warning, error), a kind (`kind` in JSON: the spec's loss kinds, `windowsDefault.<rule>`, or a tool diagnostic code), a location, a message and a free-form detail; human summary and JSON |
| `policy` | the loss kinds of SPEC §17.3 with their default policies (tested against the schema's enum) and the `onLoss` resolution of SPEC §17.2 (target kind, file kind, prefixes, defaults; fatal kinds); `-strict-defaults` is the policy `"windowsDefault.*": "error"` |
| `model` | the layout of SPEC 0.2 (which holds 0.1) as Go types; `MinVersion`, the lowest version that holds a layout (union types with their own JSON forms: Binding, MarkedText, TextOrUnits, CodeUnits `{"utf16": [...]}`, dead-key values, Windows cells); modifier matching (§7.3), resolution (§9.3), effective outputs (keys, markers, variables), and the checks of SPEC §19 |
| `format/kbljson` | `.kbl.json` read (format versions 0.1 and 0.2, each file against the schema of the `kbl` it names, an unknown `kbl` is a `kbl.schema` error at `/kbl`; schema, UTF-8, no lone surrogates, closed objects, §19 checks) and write (the lowest version that holds the layout, SPEC §1.1; fixed property order, validated output); `SameJSON` compares documents by meaning |
| `jsonschema` | a JSON Schema 2020-12 validator for exactly the keywords the kbl schema uses; any other assertion keyword makes `Compile` fail |
| `format/klc` | `.klc` reader and writer per `spec/klc-format` of the reference specification, revision 10, with a canonical and an MSKLC-editor mode |
| `format/keylayout` | macOS `.keylayout` read and write (Apple TN2056): its own small XML reader for XML 1.0 and 1.1 (C0 controls as character references, which the standard library refuses), modifier maps and their matching over the 256 modifier states, key map sets with `baseMapSet`, named and inline actions, range actions kept as written, terminators; one fixed written form, so write → read → write is byte-stable |
| `convert/klckbl` | `.klc` ↔ model by `coverage/windows.md` §7 and §10 (D28, D33, D36): positions, layers `s<state>`, Caps Lock layers, dead keys `u<hex>`, MSKLC's implied rows written explicitly (`klc.impliedRows`); the export drops them again when unchanged |
| `convert/keylayoutkbl` | `.keylayout` ↔ model by the spec's `coverage/macos.md` (import: the 32 states of the first modifier map and key map set, Command and Control as `commandMap`/`controlMap` when they fit or as layers, standard keys left to rule `standardKeys` when that rebuilds them exactly, the ISO key rule in reverse, states and terminators as dead keys, every shape the model cannot say a loss) and `targets/macos.md` (export: the macOS build of `target/macos/build`) |
| `format/xkb` | XKB symbols files (the `xkb_symbols` sections layouts use: include, `name[]`, keys with `type[]`, `symbols[]`, `actions[]` per group, `modifier_map`, keysyms by name, `Uxxxx`, number or UTF-8 string, several per level; anything else kept raw) and Compose files, read and written in one fixed form; includes resolved and merged level by level as libxkbcommon does (`Compile`); the stock key types of xkeyboard-config as data with the automatic type rule and the Caps Lock transformation; keysym names from `keysyms_table.go` (generated by `tools/genkeysyms` from xorgproto's `keysymdef.h`) |
| `convert/xkbkbl` | XKB ↔ model by the spec's `coverage/xkb.md` (import) and `targets/xkb.md` (export, the build of `target/xkb/build`): group 1 typed in the 16 states of Shift, Caps Lock, LevelThree and LevelFive by its key type, LevelThree on `<RALT>` as `altR` (elsewhere a custom `level3`), LevelFive as a custom `level5`, Caps Lock layers only where they differ, `dead_*` keysyms as dead keys with the entries of the Compose file given (longer sequences as chained dead keys) |
| `target/xkb` | the XKB target behind that interface: compile (symbols file, registry entry, Compose file, yakbd key types; optionally `expect.tsv` cases for `tools/xkb-check`), installer (the release `.tar.gz` with `install.sh` for the user path, which merges marked blocks into the shared registry, rules and Compose files and removes them again, and a README), pin and validate |
| `target/xkb/build` | model → XKB by the pins of `targets.xkb` and the default rules of `targets/xkb.md`: the 4, 8 or 16 states of Shift, Caps Lock, level 3 and level 5 resolved per position, the first stock key type that fits each key (or a yakbd type `YAKBD_<levels>`), level-shift keys, dead keysyms and the Compose sequences of the dead keys, the registry entry; every rule use an `xkbDefault.<rule>` entry; `Pin` |
| `target/xkb/keyname` | the position table of the XKB target (`xkb-default-keyname.tsv`, embedded unchanged from the kbl spec branch; a test fails if the two differ), with the key-name aliases importers read |
| `format/cldr` | CLDR Keyboard 3.0 read and write (and keyboard test files), imports inlined from the embedded CLDR release 49 import files, implied forms translated to positions, marker dead keys, kbl-only data carried in a `<special>` element so that a round trip loses nothing |
| `convert/cldrconf` | the CLDR conformance run (docs/cldr.md): corpus list, the conformance build (LANGID 0409, `windows.vkMissing` accepted), CLDR test checks → Windows key sequences or skip reasons; its test compares every keyboard of the release-49 corpus with `testdata/<keyboard>.txt` |
| `target/windows/build` | model → Windows intermediate by the pins of `targets.windows` and the default rules of `targets/windows.md`; every rule use is a `windowsDefault.<rule>` entry; `Pin` writes all decided values into the file |
| `target` | the interface between the command line and a platform (docs/targets.md): `Target` (name, defaulted-kind prefix, its commands with their own flags), `Command` (`Check`, `Run`), `Job`, `Result` |
| `target/registry` | the known targets, one line each; `windows` is the default |
| `target/windows/wintarget` | the Windows target behind that interface: compile (DLL set), installer, pin and validate's build check over `target/windows/build`, `target/windows` and `installer`; outside the stub's packages |
| `target/macos` | the macOS target behind that interface: compile (the layout bundle, optionally `expect.tsv` derived from the model for `tools/macos-check`), installer (the release zip with a README), pin and validate |
| `target/macos/build` | model → `.keylayout` by the pins of `targets.macos` and the default rules of `targets/macos.md`: 32 states (Shift, Option, Control, Command, Caps Lock) resolved per position, identical tables sharing a key map, rules `commandMap`, `controlMap`, `standardKeys`, `isoKeys`, dead keys as actions, states and terminators; every rule use a `macosDefault.<rule>` entry; `Pin` |
| `target/macos/keycode` | the position table of the macOS target (`macos-default-keycode.tsv`, embedded unchanged from the kbl spec branch; a test fails if the two differ) |
| `target/windows` | the Windows intermediate (`Tables`, `DLL`), its writer checks, `Compile` (one architecture), `CompileAll` (a build set), `Read`, the expectations, the supplementary dead-key recipe |
| `target/windows/vk` | virtual-key names (from the reference spec) and Table A, the default VK table (`windows-default-vk.tsv`, embedded unchanged from the kbl spec; a test fails if the two differ) |
| `pe` | PE/COFF images: sections with symbolic fixups, exports, resources, base relocations, checksum; a reader |
| `internal/unicode/norm` | Unicode normalisation forms NFD and NFC (UAX #15) with the standard library only; tables generated from the Unicode 18.0.0 UCD, Hangul by algorithm; tested against the full `NormalizationTest.txt` |
| `tools/genucd` | writes `internal/unicode/norm/tables.go` from the UCD files `UnicodeData.txt` and `DerivedNormalizationProps.txt` (not in the repository; the generated file records their SHA-256) |
| `tools/genkeysyms` | writes `format/xkb/keysyms_table.go` from xorgproto's `keysymdef.h` (not in the repository; the generated file records its SHA-256) |
| `internal/cli` | the subcommands `compile`, `convert`, `validate`, `pin`, `installer`, `version`, and the `-report` file (docs/report-file.md: tool version, JSON pointers into the input); `-target` and the run of a target's command (`target.go`), the table of file formats every input and `convert` go through (`formats.go`) |
| `installer` | a build set → `Package` (manifest + files; machine kinds x64 = x64 + wow64, arm64 = arm64x + wow64, x86), the product-code rule, `Container` with its one implementation `ExeContainer` (embedded stub + payload); embeds `stub-386.exe` |
| `installer/cmdline` | the installers' msiexec-compatible command line, exit codes, UI-level, restart and log rules (docs/installer-cli.md, whose tables its tests read); pure, standard library only (compiled into the stub) |
| `installer/exeres` | the installer program's resources (manifest `asInvoker`, an icon drawn by code, a version resource from the install data), added as a new `.rsrc` section when `yakbd installer` writes the file (docs/installer.md §3.1, §5.1); not compiled into the stub |
| `installer/payload` | the payload format (header, JSON manifest, files, SHA-256 checks) and locating it after a PE image; standard library only (compiled into the stub) |
| `installer/setup` | the install, upgrade/repair, enable and uninstall logic of `installation` §10 behind a `System` interface, tested against an in-memory Windows; standard library only |
| `installer/shape` | the Machine and shape check of one layout file per build (kbl windows-install.md §3): the tool's own DLL reader (`target/windows`, `pe`) must read it back as that build; run by `yakbd installer` on every file and by the stub before it copies; compiled into the stub |
| `installer/stub` | the Windows program of every installer (`windows/386`, plain `syscall`): arguments (installer/cmdline), self-elevation, dialogs, log |
| `installer/winhost` | the `System` of real Windows for both installer programs (plain `syscall`), plus elevation, dialogs, the `Global\YAKBD-Setup` lock, restart, PATH broadcast |
| `installer/toolsetup`, `installer/toolstub`, `tools/toolsetup` | the YAKBD installers `yakbd-cli-setup.exe` and `yakbd-gui-setup.exe`: payload of components (CLI: yakbd.exe x86/x64/arm64; Editor: x64/arm64), compressed; state detection (also the earlier one-folder layout), one decision for the wizard and the command line, install into Program Files\YAKBD\CLI and \Editor + machine PATH + one uninstall entry; the wizard's model (toolsetup) and its Win32 window (toolstub); the reproducible build (docs/installer-cli.md §12) |
| `installer/stubbuild`, `tools/buildstub` | the one reproducible build of the stub, and the command that writes it into `installer/` |

## Rules the code keeps

- **One report per operation.** Readers report diagnostics, converters and the compiler report losses, and
  every choice taken from a default rule is a `defaulted` entry of kind `windowsDefault.<rule>` with the chosen
  value in `detail.value` (owner decision 2026-11-22; SPEC §17.1). Severities come from the file's `onLoss`
  policies and the kinds' defaults (SPEC §17.2, §17.3); `-strict-defaults` adds `"windowsDefault.*": "error"`. A
  build or conversion with an error writes nothing.
- **Writer rules are checked twice**: `Tables.Validate` checks the tables before encoding, and `checkImage`
  checks the written file (size limit 262,144 bytes, `.data` extent, every pointer inside `.data` and outside
  the 0x68 bytes of `KBDTABLES`, table RVAs below 0x10000).
- **The writer is deterministic**: fixed time stamp 0, sorted resources, no maps iterated into the output.
- **The reader never trusts the file**: every pointer is bounds-checked, every array has an upper bound,
  `cbSize` must be `2 + 2n` and `cbLgEntry` must match before rows are walked.

## Builds: one call, the whole set

`windows.CompileAll` (and `yakbd compile`) builds every supported target of a layout in one call and returns a
`BuildSet`: the files with the system directory each belongs in, the targets it cannot build yet, and one
report. The default is `targets.windows.builds`; when the file has none, rule `builds` gives every build the tool
can make (D39: x86, x64, wow64 and, since its writer passed the gate of kbl D44 in VM-6, D66, arm64x). `-build`
(formerly `-target`, which still takes the list) limits the set; `-build arm64` adds a plain ARM64 file, a test control only (x64 processes cannot load it).

The installer package consumes a `BuildSet` as it is; nothing in `target/windows` installs anything.

## File layout of a built DLL

| | amd64 | WOW64 | x86 |
|---|---|---|---|
| sections | `.text` (stub), `.data`, `.rsrc`, `.reloc` | `.data` (tables + stub, executable), `.rsrc`, `.reloc` | as WOW64 |
| pointers | 8 bytes, DIR64 relocations | 8-byte slots, HIGHLOW on the low dword | 4 bytes, HIGHLOW |
| stub | `lea rax,[rip+x]; ret` | `mov eax,x; cdq; ret` (+ relocation) | `mov eax,x; ret` (+ relocation) |
| header | dll-format §1.1 Windows 11 column; linker version 0.1 (the tool's own) | WOW64 column | WOW64 column |

The arm64x file (System32 of Windows on ARM; `docs/arm64x.md`): the same `.data` as amd64, ARM64 code `adrp x0; add
x0; ret` at the start of `.text` for the native view, an x64 function `lea rax,[rip+x]; ret` on a page of its own
for the x64 view, two load configurations, CHPE metadata (code map ARM64 + x64), and five ARM64X entries in a
dynamic value relocation table at the end of `.reloc` that give an x64 process Machine 8664, its own export
directory and its own load configuration.

Inside `.data`: `KBDTABLES` first in its full form, then a free zone up to +0x70 (so that no table starts within
the 0x68 bytes Windows treats as the structure, on every architecture), then the modifier tables, character
rows and the `VK_TO_WCHAR_TABLE` array, `DEADKEY`, key-name strings and arrays, the scan-code tables, ligatures,
the export directory, and (WOW64/x86) the stub. Resources: strings 1000, 1100, 1200 and a version resource with
the tool's own product name, file type driver, subtype keyboard.

## Tests

Table-driven unit tests in every package, and these round trips and corpora:

- the kbl spec's four examples: read → write gives the same document (`SameJSON`); .kbl.json → CLDR → .kbl.json
  keeps the meaning; the three Windows-ready ones build, read back equal, and pin (a pinned file builds the same
  DLL with no `windowsDefault.*` entry and is stable under a second pin); pt-abnt2 reports the pins it needs
  (`windows.langIdMissing`, `windows.vkMissing`), as the spec intends;
- the reference spec's 47 `.klc` inputs: golden parse diagnostics; every parseable one converts to a
  schema-valid .kbl.json and back to the same `.klc` meaning, except five SGCAPS-with-dead-key files whose shape
  Windows cannot hold; `kbdtst.klc` and its .kbl.json conversion build byte-identical DLLs;
- the CLDR 3.0 keyboards of CLDR release 49 (`testdata/third_party`): CLDR → .kbl.json → CLDR → .kbl.json with the
  same meaning; pt-abnt2 equals the spec's example; every fixture builds a DLL set once a LANGID is pinned;
- golden digests of built DLLs; the standard library's `debug/pe` reads every container; Table A, the schema and
  the examples are compared with the spec repository when it is checked out next to this one.

All checks are Go tests; the repository has no Python in code, tests, CI or procedures (owner rule). A one-time
cross-check during development is recorded in `CLEAN-ROOM.md`.

## Expectations for unattended tests on Windows

`yakbd compile -emit-expectations` writes, next to the build directories, `<name>.expect.txt`: one line per
reachable cell (`key 51+10 -> chars 0051`, `key c0 -> dead 0060`) and per dead-key entry whose keys exist
(`seq c0 41 -> chars 00e0`), derived from the tables by `Tables.Expectations` (Caps Lock and Num Lock off; Alt
without Ctrl skipped; empty Ctrl cells left out because Windows may fall back to a control character). The plan
for the approved VM runs: one script inside a Windows guest registers every DLL of a build set, presses each
line's keys through `ToUnicodeEx` in a 64-bit and a 32-bit process, and prints one pass/fail line per layout and
process with the first differing lines. Every line starts from a clean state: after a `dead` or `chain` line
the dead key is still pending, so the script resets it (for example with a key that has no character) before
the next line; a `chain` line is checked by the key that follows in the same `seq`. `cmd/yakbd-wincheck` (below) is that runner; the DLL set and this file are its input.

## The Windows test runner

`cmd/yakbd-wincheck` (Go, plain `syscall` on user32, advapi32 and kernel32; built for windows/amd64 and
windows/386) runs a bundle made by `tools/vmbundle` inside a disposable Windows guest: per layout it copies the DLL
into the system directory under a test name, writes `Layout File`, `Layout Text` and `Layout Id` under a test KLID
(a Layout Id of its own per layout and runner, so no two loads share an HKL), loads it with `LoadKeyboardLayoutW`,
checks the HKL, types every expectation through `ToUnicodeEx`, unloads, and removes file and key. It prints `PASS
name` or `FAIL name: first 3 diffs` and a count, and writes all differences to a file; the 64-bit run also reads
Windows' own SysWOW64 layout files as data for VM-1. The portable logic (expectations, manifest, slots, the
comparison, an emulator of the ToUnicodeEx rules of windows-behaviour §6) is `internal/wincheck` and is tested on
Linux, the runner itself against a fake system.
Each layout is loaded in a child process with a time limit; the runner refuses to start without an interactive
desktop. VM-1 to VM-3 were settled by VM run 1 (the descriptor relocation is required and always written; linker
version and sizes do not matter); the bundle keeps one rebased load. `go run ./tools/vmbundle -o DIR` writes the
bundle and its README with the guest commands.

## The installer

`yakbd installer` (owner decision 2026-11-22) writes **one** Windows program per layout that carries every build
and installs the right files on the machine it runs on; `-build` limits it to machine kinds. Design, sequence with
the reference spec's sections, payload format, resources and exit codes: `docs/installer.md`; command line: `docs/installer-cli.md` (msiexec-style;
no `.msi`, owner 2026-11-26).

```
 BuildSet + build.Result --installer.NewPackage--> Package --ExeContainer.Write--> <name>-setup.exe
                                                                                   = stub-386.exe + .rsrc | payload
                                 (.rsrc = manifest asInvoker, icon, version: installer/exeres)
 on Windows:  installer/stub (windows/386) --reads its payload--> installer/setup (System = Windows API)
```

- **One x86 program for every machine.** It is the one kind that runs on x86, x64 and ARM64 Windows; it takes the
  architecture from `IsWow64Process2`, writes the native directory through `Sysnative` and opens HKLM in the 64-bit
  view (`installation` §2.2).
- **Logic apart from the API.** Every decision (files per machine, KLID and Layout Id allocation, value order,
  rollback, upgrade in place, repair from the uninstall entry, uninstall from every loaded hive) is
  `installer/setup`, tested on Linux against an in-memory Windows; the stub only maps `System` to Windows calls.
- **How the stub gets into yakbd.** `installer/stub-386.exe` is committed and embedded with `//go:embed`. It is
  produced only by `go run ./tools/buildstub` (`installer/stubbuild`: `GOOS=windows GOARCH=386 GO386=sse2
  CGO_ENABLED=0`, empty `GOFLAGS`, `GOTOOLCHAIN=local`, `-trimpath -buildvcs=false -ldflags "-s -w -H=windowsgui
  -buildid="`), which also writes the Go version into `installer/stub-386.version`. The stub imports the standard
  library, `installer/cmdline`, `installer/payload`, `installer/setup`, `installer/winhost` and `installer/shape`; the shape check
  reuses the tool's DLL reader, so `target/windows`, `pe` and `loss` are compiled into the stub too (all standard library
  only; `GOOS=windows GOARCH=386 go list -deps ./installer/stub` lists them).
  `TestEmbeddedStubIsFresh` rebuilds it and compares byte for byte; CI pins exactly the recorded Go version, so
  CI tests the very program that ships (locally the test is skipped under another Go version). After changing any
  of those packages or `installer/stub` itself: run `go run ./tools/buildstub` and commit `installer/stub-386.*` with the change.
- **No toolchain at run time.** `yakbd installer` only adds the resource section to the embedded stub and appends
  the payload, on any OS.
- **Unsigned.** The payload is found from the stub's section table, not from the end of the file, so an
  Authenticode signature can be appended later by a signing step (docs/installer.md §5).
