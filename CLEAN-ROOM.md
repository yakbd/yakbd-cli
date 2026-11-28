# Clean-room record

This file says where the content of this repository came from, so that anyone can check that it was written from
public sources and measurements only.

Every fact about Windows keyboard-layout DLLs, Windows' keyboard behaviour, layout installation and the `.klc`
format comes from one clean reference specification: the **YAKBD reference specification, revision 10** (frozen
2026-11-21). It was written from public documents and from black-box measurements of Windows and of Microsoft
Keyboard Layout Creator (MSKLC) 1.4 in disposable virtual machines; every rule in it is tagged with its public
source or with the test that repeats it. The authors of this repository worked from the frozen revision only,
under its clean-room rules. The reference specification is not public. Citations such as "dll §4.2", "inst §10",
"wb" or "OBS D9" name its sections (the last its MSKLC observations: what MSKLC's own tools
were seen to do, from the outside); they record where a fact came from, and publishing the reference
specification is a separate decision. Its owner relicensed it, including its test inputs, to MIT-0 for use in the
yakbd repositories on 2026-11-22.

## Specification used

| item | value |
|---|---|
| specification | the YAKBD reference specification (not public), revision 10 (frozen 2026-11-21) |
| tree hash of `spec/` | `24fb0165bc1ef4c96c0b45c2859a933a9bd39e95` |
| tree hash of its MSKLC observations | `bb4ea4cea00d74aff890e8b27c5ef73d1fb39fe1` |
| tree hash of its notes on public sources | `0bb60d45d5fe5ee0294d26f1e9ba397056f06a22` |
| its MSKLC observations read | **yes**: `README.md`, `NOTICE`, `dll-format.md` (whole), `klc-format.md` (K17 to K28, K33, K34, K44, K45 and the entry list), `tests/klc-format/README.md` and `expected.txt`, `tests/dll-format/README.md`, and the `.klc` inputs of `tests/dll-format/`, `tests/klc-format/`, `tests/vm/kbdtst.klc` and `tests/vm/wbt1.klc` |
| its notes on public sources read | **seen only through search hits** (lines matching "scan code" in its three files); no entry was used as a source |

## Instructions, notes and history

The written instructions each author worked from, the owner decisions given during the work, and the development
notes are kept in the private prototype repository. Commit hashes in this record name commits of that private
history, which the public history does not contain.

## CLDR files

The CLDR release-49 files (`testdata/third_party/cldr-release-49/`, `format/cldr/import/`) were copied unchanged
from the spec repository's `testdata/third_party/`, whose README records them as unmodified copies of
https://github.com/unicode-org/cldr/tree/release-49/keyboards fetched 2026-11-22. The CLDR reader and writer were
written by a second author under the same rules; it fetched nothing from the web and read only the
two yakbd repositories and the reference specification.

2026-11-26: the release list of https://github.com/unicode-org/cldr (GitHub API, releases and
`release-*` tags: `release-49` is the newest release) and the ten further keyboard and test files of `keyboards/3.0`
and `keyboards/test` at that tag were fetched from raw.githubusercontent.com, unchanged; the LDML keyboard and test
formats were read only as the spec repo's `coverage/cldr-keyboard3.md` describes them. Neither the reference specification's MSKLC
observations nor its notes on public sources were read.

## Verification

Every check the repository relies on is a Go test (the tool's own DLL reader, the standard library's `debug/pe`,
golden files). History only: during development the built DLLs were cross-checked once, as black boxes, with the
reference spec's table reader, an external PE-parsing library and `objdump -p`, and the 32-entry Windows-1252 table
of `format/klc/encoding.go` once with an external codec; all agreed. None of these is part of the repository or
its procedures. The WHATWG index cited for Windows-1252 was not fetched.

## What the implementer read

- The reference specification at the tag above, and no private material of its authors.
- The `.kbl.json` specification (yakbd/kbl-spec, up to commit e2a0b7b): SPEC.md, the schema,
  `targets/windows.md`, Table A, `coverage/windows.md`, `coverage/cldr-keyboard3.md`, DECISIONS.md,
  its triage of the tool's notes, the examples and its CLDR test fixtures.
- Public pages: the CLDR keyboard specification (UTS #35 Part 7) and CLDR's `keyboards/` folder, Microsoft Learn
  "About Keyboard Input" (scan-code table) and "Virtual-Key Codes", the public PE
  format specification and the resource-format pages cited in the source, the Go documentation, the CC0 legal
  code.
- No Windows SDK or WDK header (including `kbd.h`), no leaked source, no Wine or ReactOS source, no decompiled or
  disassembled code of any Microsoft program.

Facts that the specification lacks are not filled in from memory of Windows internals: they are asked in
the spec repository's `OPEN-QUESTIONS.md` with the behaviour the tool chose, and answered in its `DECISIONS.md`.

## The installer (2026-11-22)

Written from the same specification tag. Read: `spec/installation/README.md` (whole; the rules used are §1 to §6
and §10, with §7 and §8 for the requirements they state), the written instructions and the kbl spec's
`targets/windows.md` §2 and §9 and its schema's `registration`. the MSKLC observations and the notes on public sources were
**not** read; OBS pointers in the installation text were not followed.

Public pages read on 2026-11-22 for facts the reference spec does not hold (facts only, own words):

- https://learn.microsoft.com/en-us/windows/win32/msi/uninstall-registry-key : the Uninstall key and its value
  names (`DisplayName`, `DisplayVersion`, `Publisher`, `InstallLocation`, `UninstallString`, `EstimatedSize`).
- https://learn.microsoft.com/en-us/windows/win32/winprog64/shared-registry-keys : `HKLM\SOFTWARE` is redirected
  for 32-bit processes, `HKLM\SYSTEM` shared.
- https://learn.microsoft.com/en-us/windows/win32/winprog64/file-system-redirector : the `Sysnative` alias.
- The names, prototypes and constants of the general Win32 functions the installer program calls (registry,
  files, `MoveFileExW`, tokens, `ShellExecuteExW` with `runas`, `MessageBoxW`, `SHGetKnownFolderPath`,
  `GetKeyboardLayoutList`) as Microsoft Learn documents them. The keyboard-layout functions and their flags
  (`InstallLayoutOrTip`, `InstallLayoutOrTipUserReg`, `ILOT_UNINSTALL`) are taken from the reference spec (inst §4.3).
- Go documentation (`syscall`, `debug/pe`, `embed`, `go build` flags).

## ARM64X builds and the Windows on ARM runner (2026-11-22)

Written from the same specification tag. Read: `spec/dll-format/README.md` §1.4, §1.7, §9.1 ("ARM64"), §11 item 9;
`spec/installation/README.md` §2.4; `spec/installation/tests/vm/arm64/README.md` (procedure, "Traps", results);
`spec/windows-behaviour/README.md` §11 (right Shift and KLLF_LRM_RLM). the MSKLC observations and the notes on public sources
were **not** read in this round. Also the kbl spec's `targets/windows-vm-tests.md` (VM-4) and `targets/windows.md` §3.

Public sources for the ARM64X file format (facts only, own words; `docs/arm64x.md` cites each fact):

- Microsoft Learn, read 2026-11-22: "Arm64X PE Files" (https://learn.microsoft.com/en-us/windows/arm/arm64x-pe),
  "Understanding Arm64EC ABI and assembly code" (https://learn.microsoft.com/en-us/windows/arm/arm64ec-abi), the
  "PE Format" page (load configuration layout).
- The LLVM project (Apache-2.0 with LLVM exception), a sparse clone of https://github.com/llvm/llvm-project at
  commit f1afebcbe1c627a3a18d7b3a7b3fce4baff45223 (2026-11-22) (a local copy). Read for structure layouts and encodings only: `llvm/include/llvm/Object/COFF.h`,
  `llvm/include/llvm/BinaryFormat/COFF.h`, `llvm/lib/Object/COFFObjectFile.cpp` (the ARM64X and CHPE parts),
  `lld/COFF/Chunks.cpp`, `lld/COFF/Chunks.h`, `lld/COFF/Writer.cpp` (hybrid images, load configurations, dynamic
  relocations, code map), and the lld tests `arm64x-export.test`, `arm64ec-entry-thunk.s`,
  `Inputs/loadconfig-arm64ec.s`, `Inputs/loadconfig-arm64.s`. No LLVM code was copied into this repository.
- Released LLVM tools: llvm-mingw 20261104 (LLD 23.1.5; https://github.com/mstorsjo/llvm-mingw/releases/tag/20261104,
  `llvm-mingw-20261104-ucrt-ubuntu-22.04-x86_64.tar.xz`) (a local copy). Used to assemble and link
  two small ARM64X DLLs from assembly written for this purpose with no runtime library (not in this repository:
  `native.s`, `ec.s`, `x64.s`), and to dump them and the tool's own ARM64X files with `llvm-readobj` and
  `llvm-objdump` while developing. These LLVM-made files are a comparison only: none is a test fixture, and every
  check the repository relies on is a Go test with the tool's own reader (`pe/arm64x.go`).
- the spec repository's `targets/windows-install.md`, the triage of the installer notes and D46 to D57 at
  commit 40b1e6b, and D61, D62 at 027a7fc; the installer follows them.
- https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/how-it-works
  and `.../settings-and-configuration`: UAC's installer detection policy and its defaults.
- RFC 9562 (UUID version 5, and its appendix A.4 test vector) for rule `productCode`.
- `WTSQuerySessionInformationW` (session user and domain) as Microsoft Learn documents it.
## DLL import (2026-11-22)

Written from the reference specification, revision 10 (`spec/dll-format/README.md` whole; its MSKLC observations (`dll-format.md`) D8 to D29 and its summary, for the shapes of files other tools write), the kbl specification
(coverage/windows.md, targets/windows.md) and the written instructions of that work. The descriptor
shapes other compilers emit (jump thunks, frame-pointer prologue, `mov rax, imm64`) are general x86 instruction
encodings, not taken from any Microsoft or third-party layout file. Test fixtures are only DLLs the tool builds
and DLLs the tests construct (`convert/dllkbl/foreign_test.go`, a second encoder written for the tests); no
Microsoft-built or MSKLC-built DLL was read, copied or used. Windows' own layout files are read only inside the
disposable guest of the import bundle (`tools/importbundle`), and only counts leave it.

## Installer hardening (2026-11-22)

Written from the reference specification, revision 10 (`spec/installation/README.md` §2.4, §3, §4, §5, §7, §10 and
§11 T7, T13) and the spec repository's `targets/windows-install.md` at commit 006c4d3. its MSKLC observations (`installation.md`) I15 to I20 were read (the shape of `User Profile`, `Preload`/`Substitutes` and the CTF profile
entry that holds the HKL, I19), as the installation text points to them; nothing else of the MSKLC observations and
nothing of the notes on public sources. The shape check reuses the tool's own readers (`target/windows`, `pe`); no other
source was used.

## Unicode normalisation (2026-11-25)

`internal/unicode/norm` (NFD, NFC) is written from UAX #15 (https://www.unicode.org/reports/tr15/, the
composition and decomposition algorithms and their definitions) and the Hangul algorithm of the Unicode core
specification section 3.12; its tables are generated by `tools/genucd` from the Unicode 18.0.0 UCD files
`UnicodeData.txt` and `DerivedNormalizationProps.txt` (UAX #44 for their format), and it is tested against the
release's `NormalizationTest.txt`. The UCD files were fetched from https://www.unicode.org/Public/18.0.0/ucd/ into a local folder. No other normalisation library (including `golang.org/x/text`) was
read or used; the version follows CLDR release-49 (its release note: Unicode 18). Nothing of the reference
specification's MSKLC observations or the notes on public sources was read for this work.

## Installer for real users (2026-11-25)

Written from the reference specification, revision 10 (`spec/installation/README.md` §4.3 "over the shoulder", §5,
§7, §10 and its `tests/vm` READMEs for methods) and the spec repository's `targets/windows-install.md` at 9f2de64.
the MSKLC observations (`installation.md`) I10 to I45 were skimmed for anything on the elevation prompt (nothing beyond
I42/I45); nothing of the notes on public sources. Public Microsoft pages, read 2026-11-25:
- https://learn.microsoft.com/en-us/windows/win32/sbscs/application-manifests (requestedExecutionLevel values,
  supportedOS ids, dpiAware/dpiAwareness, Common Controls dependency, namespaces);
- https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/how-it-works
  (consent and credential prompts, secure desktop, unsigned programs) and `.../settings-and-configuration`
  (policy registry values `ConsentPromptBehaviorAdmin`, `ConsentPromptBehaviorUser`, `EnableInstallerDetection`,
  `PromptOnSecureDesktop` and their defaults);
- https://learn.microsoft.com/en-us/cpp/security/how-user-account-control-uac-affects-your-application and the
  archived policy page "User Account Control: Detect application installations and prompt for elevation" (neither
  says whether a manifest's level turns installer detection off: left to a VM test);
- the PE format page (section table, resource directory), resource types, VS_VERSIONINFO / VS_FIXEDFILEINFO and the
  icon resource pages (GRPICONDIR) on learn.microsoft.com. The icon is drawn by `installer/exeres`' own code.
## MSKLC comparison (2026-11-25)

Written from the reference specification, revision 10: the MSKLC observations (`README.md`), `dll-format.md` (whole),
`windows-behaviour.md` W1 to W6, `klc-format.md` K13 to K16 and the tables of its first part,
`installation.md` I40 to I43, `tests/vm/README.md`, `tests/vm/wb-t1.ps1` (how kbdutool is run: switches, folder,
input encoding), `tests/dll-format/README.md`, `tests/klc-format/README.md`, `tests/vm/arm64/README.md`; and
the spec repository's `coverage/windows.md` §7, §10, §11 at 9f2de64. Public pages: the MSKLC 1.4 page of Microsoft's
download centre (https://www.microsoft.com/en-us/download/details.aspx?id=102134: file name and URL only) and
KbdEdit's site (https://kbdedit.com/demo.html, https://www.kbdedit.com/manual/editions.html; local copies kept), read 2026-11-25 to decide whether a free edition
can build layouts (docs/dll-import.md §6.4). MSKLC was not run, installed or downloaded by the author; no MSKLC-
or Microsoft-built file was read. The MSKLC-shaped test files are written by the tests' own encoder from the
observations' descriptions.

## The msiexec-compatible installer command line and the tool installer (2026-11-26)

Written from public Microsoft documentation only, read 2026-11-26 (each page listed with what it gave in
`docs/installer-cli.md` §0): the Windows Installer pages Command-Line Options, Standard Installer Command-Line
Options, MsiExec.exe and InstMsi.exe error messages (`/msi/error-codes`), REBOOT, System Reboots,
MSIRESTARTMANAGERCONTROL, _MSIExecute Mutex, MsiLogging, Normal Logging, Logging (policy), Windows Installer
Logging, ALLUSERS, REINSTALL, REINSTALLMODE, Applying Small Updates by Reinstalling the Product, Major Upgrades,
Minor Upgrades, Small Updates, Preventing an Old Package from Installing Over a Newer Version, Uninstall Registry
Key, ARPNOMODIFY, ARPNOREPAIR, ARPNOREMOVE; the system error code pages (500–999, 1000–1299, 1300–1699,
1700–3999); InitiateShutdownW and System Shutdown Reason Codes; Environment Variables, WM_SETTINGCHANGE, WOW64
Implementation Details; winget's "Create your package manifest" and "uninstall command" pages; Intune "Add a Win32
app"; Configuration Manager "Create applications". Public repositories: microsoft/winget-pkgs
`doc/manifest/schema/1.12.0/installer.md`, and microsoft/winget-cli at master (2026-11-26) `ManifestCommon.cpp`,
`UninstallFlow.cpp`, `RepairFlow.cpp`, `ARPHelper.cpp`, read for which switches, return codes and uninstall-entry
values winget uses (facts only, no code copied). Nothing of the reference specification's MSKLC observations or
the notes on public sources was read for this work.

## AGENTS.md files (2026-11-26)

`AGENTS.md` files written from this repository, the kbl spec repository (`SPEC.md` opening, `README.md`,
`STATUS.md`, `OPEN-QUESTIONS.md` heading) and two public agent-instruction skills: Microsoft's `wiki-agents-md`
(https://github.com/microsoft/skills, deep-wiki plugin) and mblode's `agents-md`
(https://github.com/mblode/agent-skills). No text of the reference specification was read beyond its folder
names; nothing of the MSKLC observations or the notes on public sources. To check `tools/genucd`, `UnicodeData.txt` and
`DerivedNormalizationProps.txt` were fetched again from https://www.unicode.org/Public/18.0.0/ucd/; their SHA-256
match the header of `internal/unicode/norm/tables.go`, and the regenerated file was identical.

## Sign-in screen and new users (2026-11-26)

Rules from the kbl spec repository at 737e1a7 (`targets/windows-install.md` §7.1, §9, §10; D87; VM-18 to VM-23)
and the reference specification, revision 10: `spec/installation/README.md` §4.2, §4.3 (`InstallLayoutOrTipUserReg`
measured), §4.7, §10, §11 T8, and `spec/installation/tests/vm/README.md` (the T8 results). Nothing of
the MSKLC observations or the notes on public sources was read. Public Microsoft Learn pages, read 2026-11-26, facts only:
RegLoadKeyW (https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-regloadkeyw: HKEY_USERS, both
privileges, the file is created when missing), RegUnLoadKeyW (.../nf-winreg-regunloadkeyw), RegLoadAppKeyW
(.../nf-winreg-regloadappkeyw: read and not used, it needs one security descriptor for every key of the hive; the
checker instead loads a copy of the Default profile with RegLoadKeyW, so the original file is never written), GetDefaultUserProfileDirectoryW
(https://learn.microsoft.com/en-us/windows/win32/api/userenv/nf-userenv-getdefaultuserprofiledirectoryw: size in
characters with the terminating null, NULL buffer gives the size), AdjustTokenPrivileges
(https://learn.microsoft.com/en-us/windows/win32/api/securitybaseapi/nf-securitybaseapi-adjusttokenprivileges:
ERROR_NOT_ALL_ASSIGNED on success when the token lacks a privilege), Privilege Constants
(https://learn.microsoft.com/en-us/windows/win32/secauthz/privilege-constants: SeBackupPrivilege,
SeRestorePrivilege).

## Fuzzing (2026-11-26)

Fuzz targets and fixes written from this repository, the kbl spec repository's schema copy (Vk pattern, touch
layer and locale rules) and the reference specification, revision 10: `spec/klc-format/README.md` §4.6 (cell
grammar; surrogate values only in LIGATURE lines). Nothing of the MSKLC observations or the notes on public sources was read.
Public: the Go documentation of `testing` fuzzing (`go help testflag`: `-fuzz`, `-fuzztime`, `-fuzzminimizetime`,
`-parallel`) and of `runtime/metrics` (`/gc/heap/allocs:bytes`). That CLDR's keyboard3 `<layers>` holds one or
more `<layer>` is the schema's `minItems: 1` here, not a reading of the DTD.

## Sign-in screen per D97 (2026-11-26)

kbl spec b893f2e (D97, §7.1, §9, Q14); Microsoft Learn: LCIDToLocaleName
(https://learn.microsoft.com/en-us/windows/win32/api/winnls/nf-winnls-lcidtolocalename), ResolveLocaleName
(.../nf-winnls-resolvelocalename), and for the host driver Hyper-V WMI: Msvm_SyntheticMouse, its SetAbsolutePosition
(pixels) and ClickButton (1-based), GetVirtualSystemThumbnailImage (raw RGB 565)
(https://learn.microsoft.com/en-us/windows/win32/hyperv_v2/msvm-syntheticmouse and the method pages beside it).

## Release pipeline and DLL planting (2026-11-27)

Nothing of the reference specification was needed; its MSKLC observation and public source folders were not
read. Public sources, read 2026-11-27:
- Microsoft Learn, Dynamic-link library search order
  (https://learn.microsoft.com/en-us/windows/win32/dlls/dynamic-link-library-search-order): the application's
  folder is searched after Known DLLs and before the system folder; dependents are searched for by module name.
- Microsoft Learn, SetDefaultDllDirectories
  (https://learn.microsoft.com/en-us/windows/win32/api/libloaderapi/nf-libloaderapi-setdefaultdlldirectories):
  signature, `LOAD_LIBRARY_SEARCH_SYSTEM32` = 0x800, used for loads without search flags and for dependents of a
  DLL loaded by full path, Windows 8 and later.
- Go source (`syscall/dll_windows.go` `LoadDLL`, `internal/syscall/windows/sysdll`, `syscall/zsyscall_windows.go`):
  the DLL names Go's `syscall` package loads from System32 only.
- PE Format (already listed): import directory, import lookup and address tables, hint/name entries, the
  AddressOfEntryPoint field, for the marker DLL of `tools/instbundle/plant.go`; the x86 instruction encodings of its
  entry point are standard IA-32 encodings (ModRM/SIB), written by hand.
- SignPath: https://github.com/signpath/github-action-submit-signing-request (`action.yml` inputs, tag v3).
- GitHub Docs: artifact attestations (permissions `id-token`/`attestations: write`, `gh attestation verify -R`),
  managing releases; https://github.com/actions/attest-build-provenance (tag v4).

## Public documentation for the 0.1 preview (2026-11-27)

The README, the MSKLC migration page, the layout-recovery page, the Windows security prompts page and the code
signing policy were written from this repository, the spec repository, and the reference specification's
`spec/installation/README.md` §3, §7 M1 to M8 and T12, and `spec/installation/tests/vm/README.md` (T12). Neither
its MSKLC observations nor its notes on public sources were opened. Public sources, read 2026-11-27, facts only:
SignPath Foundation's terms (https://signpath.org/terms); Microsoft's pages on Smart App Control (support FAQ,
overview, and its code-signing guidance), SmartScreen reputation, User Account Control, and the `Unblock-File`,
`Get-FileHash` and `Get-AuthenticodeSignature` cmdlets and SignTool.

## Extra payload of yakbd-setup.exe and the report file (2026-11-27)

The extra payload (docs/installer-cli.md §12.1) uses only facts §12 already used: files written into the install
folder, delayed replacement and deletion at restart, the uninstall entry. The reference specification (revision 10)
was searched for facts on Start-menu shortcuts and shortcut files: its specification documents have none, and the
only matches, three lines of its notes on public sources, are about the Ctrl shortcuts a layout's virtual keys
trigger, not about shortcut files; nothing was taken, and no shortcut is made. The report file
(docs/report-file.md) locates entries by JSON Pointer, RFC 6901 (https://www.rfc-editor.org/rfc/rfc6901: the
`/` syntax and the `~0`/`~1` escapes, which the schema validator already used). The release workflow's `--latest`
publish step is from the GitHub CLI's own help (`gh release create --help`, `gh release edit --help`).

## Start-menu shortcuts of yakbd-setup.exe (2026-11-27)

The reference specification has no facts on shortcut files (see the entry above), so docs/installer-cli.md §12.2
and `installer/winhost/shortcut_windows.go` rest on public sources only, read 2026-11-27, facts only:
- Microsoft Learn: "Shell Links" (https://learn.microsoft.com/en-us/windows/win32/shell/links: a shortcut is made
  with CoCreateInstance of CLSID_ShellLink in process for IShellLink, its Set methods, then QueryInterface for
  IPersistFile and Save; COM must be initialized first; the shortcut's name is its file name, `.lnk`);
  IShellLinkW (https://learn.microsoft.com/en-us/windows/win32/api/shobjidl_core/nn-shobjidl_core-ishelllinkw) and
  IShellLinkW::SetIconLocation (`.../nf-shobjidl_core-ishelllinkw-seticonlocation`) and GetPath
  (`.../nf-shobjidl_core-ishelllinkw-getpath`: flags, MAX_PATH, S_FALSE for no path); IPersistFile::Load
  (https://learn.microsoft.com/en-us/windows/win32/api/objidl/nf-objidl-ipersistfile-load); IPersistFile::Save
  (https://learn.microsoft.com/en-us/windows/win32/api/objidl/nf-objidl-ipersistfile-save: absolute path,
  fRemember); CoInitializeEx (https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coinitializeex:
  S_FALSE, RPC_E_CHANGED_MODE, balancing CoUninitialize); CoCreateInstance
  (https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-cocreateinstance); SHGetKnownFolderPath
  (https://learn.microsoft.com/en-us/windows/win32/api/shlobj_core/nf-shlobj_core-shgetknownfolderpath: flags 0,
  CoTaskMemFree in every case); KNOWNFOLDERID (https://learn.microsoft.com/en-us/windows/win32/shell/knownfolderid:
  FOLDERID_CommonPrograms, its GUID and default path); Application User Model IDs
  (https://learn.microsoft.com/en-us/windows/win32/shell/appids: optional, set through IPropertyStore; not used).
- Learn lists an interface's methods by name, not in the order of its method table, and does not give the ids of
  CLSID_ShellLink, IID_IShellLinkW and IID_IPersistFile. Those ids and the method order of IUnknown, IShellLinkW,
  IPersist and IPersistFile are from Microsoft's own generated Rust bindings: the `windows` crate 0.61.3
  (`src/Windows/Win32/UI/Shell/mod.rs`: `IShellLinkW_Vtbl`, `define_interface!(IShellLinkW, ...)`, `ShellLink`;
  `src/Windows/Win32/System/Com/mod.rs`: `IPersist_Vtbl`, `IPersistFile_Vtbl`, `define_interface!(IPersistFile, ...)`,
  `CLSCTX_INPROC_SERVER`, `COINIT_APARTMENTTHREADED`, `COINIT_DISABLE_OLE1DDE`, `STGM_READ`; `Foundation`:
  `MAX_PATH`) and `windows-core` 0.61.2
  (`src/unknown.rs`: `IUnknown_Vtbl`), downloaded from crates.io (MIT OR Apache-2.0; NOTICE §1). Only those ids,
  positions and constants were taken; no code.
- Go: the standard library's `syscall` package (`SyscallN`, and `LazyProc.Call`'s `//go:uintptrescapes`, the
  pattern `comCall` follows) and `runtime.LockOSThread`, from general knowledge of Go, no page read for them.

## Release archives and the universal macOS binary (2026-11-27)

`tools/release/macho.go` writes the universal ("fat") Mach-O layout as Go's own reader expects it: Go 1.26.8
`src/debug/macho/fat.go` (`NewFatFile`, `FatArchHeader`: big-endian magic `MagicFat`, slice count, five uint32
fields per slice), read 2026-11-27. The 16 KiB slice alignment is our choice; that the result is accepted by
Apple's tools and runs was measured on a GitHub `macos-15` arm64 runner, release workflow run 46021005907
(rehearsal) and 46021784416 (0.1.0-preview.3): `lipo -archs`/`-verify_arch` name both slices, `codesign --verify`
accepts the arm64 slice (the Go linker's ad-hoc signature, kept unchanged in the slice), and `yakbd version` ran.
The tar and gzip archives use Go's `archive/tar` and `compress/gzip` only.

## The two YAKBD installers and their wizard (2026-11-27)

The components, the decisions, the earlier layout's takeover and the settings rules (docs/installer-cli.md §12) use
only facts the earlier installer already used (files, delayed replacement, the uninstall entry, the machine PATH),
the shortcut facts above, and the earlier installer's own record and the three published installers' payloads
(read with this repository's reader). Public sources read 2026-11-27, facts only:
- Microsoft Learn: Enabling Visual Styles (https://learn.microsoft.com/en-us/windows/win32/controls/cookbook-overview:
  the Common Controls 6 manifest dependency, which the installers' manifest already had); IsDialogMessageW
  (https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-isdialogmessagew: keyboard selection in any
  window with controls, DM_GETDEFID); WM_DPICHANGED (https://learn.microsoft.com/en-us/windows/win32/hidpi/wm-dpichanged:
  the suggested rectangle, SetWindowPos, scaling by DPI/96); GetDpiForWindow
  (https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getdpiforwindow: Windows 10 1607 and later,
  so the wizard falls back to 96 DPI before it); RegDeleteTreeW
  (https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-regdeletetreew: subkeys and values of the
  named key; whether the key itself goes is not said, so RegDeleteKeyW follows and a missing key counts as done);
  semantic version precedence, https://semver.org §11.
- The other functions the wizard and the settings removal call (window class and window creation, the message
  loop, control messages, fonts, InitCommonControlsEx, AdjustWindowRectEx(ForDpi), SystemParametersInfoW,
  RtlMoveMemory, RegOpenKeyExW, RegQueryInfoKeyW, RegDeleteKeyW): their signatures, the constants' values (DC_HASDEFID
  among them) and the structures' field order from Microsoft's `windows` crate 0.61.3 (already downloaded for the
  shortcut work; NOTICE §1); their Learn pages were not read for this work. No code was taken.
- Go documentation: `compress/flate` (DEFLATE, RFC 1951) for the payload's compression.
- The icon: the .ico file layout (ICONDIR, ICONDIRENTRY) from Microsoft Learn's archived article "Icons"
  (https://learn.microsoft.com/en-us/previous-versions/ms997538(v=msdn.10), read 2026-11-27), which also says
  LoadImage loads an icon of a given size from an EXE; the icon itself is the project's own drawing (yakbd-gui,
  MIT-0). LoadImageW, GetSystemMetrics and their constants: the `windows` crate.

## macOS

### The macOS layout check (2026-11-28)

`tools/macos-check/` (test layouts, bundles, `check.swift`, `run.sh`) and `.github/workflows/macos-layout-check.yml`
are our own text and code. The two test layouts were written for this check, and no layout was copied. Public
sources read 2026-11-28, facts only:
- Apple Technical Note TN2056, "Installing and using keyboard layouts"
  (https://developer.apple.com/library/archive/technotes/tn2056/_index.html, Apple's archived documentation): the
  `.keylayout` elements and attributes (`keyboard` group/id/name/maxout, `layouts`, `modifierMap`/`keyMapSelect`/
  `modifier` and its key words with `?`, later matches winning, `keyMapSet`/`keyMap`/`key`, `actions`/`when`,
  `terminators`), numeric entities for C0 controls, group 126 and negative ids for Unicode layouts, the install
  folders and the bundle shape, and the advice to sign out after installing.
- Kiimo (https://github.com/Manoonchai/kiimo, MIT), `generator/generateMacBundle.ts` and one generated
  `output/*/*.bundle/Contents/info.plist`: the `Info.plist` keys of a layout bundle (`KLInfo_<layout name>` holding
  `TISInputSourceID` and `TISIntendedLanguage`), a source id that extends the bundle id, and `Resources/` holding
  the `.keylayout`. Only key names were taken; no code or text.
- The runner-images README (https://github.com/actions/runner-images, MIT): the `macos-15` and `macos-26` arm64 labels.
- Web search result snippets only, no page opened: a Ukelele users' thread and a Keyman issue saying that
  `TISIntendedLanguage` holds a BCP 47 tag.
- The Text Input Sources and Unicode Utilities calls (`TISRegisterInputSource`, `TISEnableInputSource`,
  `TISSelectInputSource`, `TISCreateInputSourceList`, `TISCopyCurrentKeyboardInputSource`,
  `kTISPropertyUnicodeKeyLayoutData`, `UCKeyTranslate`, `LMGetKbdType`, Carbon modifier bits), `CGEvent` keyboard
  events, `AXIsProcessTrusted`, `CGPreflightPostEventAccess`, AppKit, the System Events `key code` command, `xattr`,
  `spctl`, `com.apple.quarantine` and the `AppleEnabledInputSources` entry of the `com.apple.HIToolbox` preferences
  (`InputSourceKind`, `KeyboardLayout ID`, `KeyboardLayout Name`): from general knowledge of the public macOS SDK, as Swift imports it. No header
  file or page was read for them, and the runner's compiler checks every call.

Measured on GitHub's `macos-15` (15.8.2) and `macos-26` (26.7.2) arm64 runners, workflow runs 46102612485,
46102900903, 46103925085, 46104372309, 46104797718 and 46105185134 (2026-11-28; results in `tools/macos-check/README.md`): that
`TISEnableInputSource` alone did not enable a bundle in `~/Library/Keyboard Layouts`; the shape of an
`AppleEnabledInputSources` entry, from the runners' own entry for U.S. in a `defaults read com.apple.HIToolbox`
dump; that the entry, written after registration, makes a new process find the layout enabled; and that `spctl`
rejects an unsigned layout bundle while the system still lists, compiles and selects it.

### The .keylayout reader and writer, the macOS conversion and target (2026-11-28)

`format/keylayout`, `convert/keylayoutkbl` and `target/macos` are our own code, written from these public sources
(read 2026-11-28, facts only, no code or prose copied):
- Apple Technical Note TN2056 (https://developer.apple.com/library/archive/technotes/tn2056/_index.html, Apple's
  archived documentation), read again for the reader and writer: every element and attribute with its required and
  optional parts, the `?` suffix and the "any" modifier words, later `keyMapSelect` matches winning,
  `baseMapSet`/`baseIndex` copying, inline and named actions, `when` single and range forms, `terminators`, that
  named entities are not supported in outputs and that a supplementary character is one numeric reference, UTF-8
  or UTF-16 files.
- The test layouts of `tools/macos-check/cases/` (this repository) and the measurements recorded in its README: XML
  1.1 with C0 controls as references compiles on macOS 15 and 26; a plain output key in a dead-key state gives the
  terminator followed by the key's output (case `dead-terminator`).
- The fixtures of `testdata/third_party/keylayout/` (NOTICE section 6, all MIT), read as data to test
  the reader: their XML shapes (XML 1.0 and 1.1, `DOCTYPE ... PUBLIC ""`, comments, attribute orders, key codes
  above 127).
- Read as reader test data only, not committed: every `.keylayout` file of https://github.com/ysoftwareab/xkb-for-macos
  (Unlicense, generated from xkeyboard-config data; parsed by the reader and written again to check stability,
  then left out of the fixtures because of the notices of the data it was generated from), and
  `tests/us_test.keylayout` of https://github.com/adobe-type-tools/keyboard-layouts (its header says it is a
  `kluchrtoxml` dump of an installed layout, so it was parsed in that run and its header read, then excluded).
- CLDR `keyboards/osx/da-t-k0-osx.xml` at release 43 (https://github.com/unicode-org/cldr, Unicode licence): only its
  first lines, to see the format. It is LDML keyboard XML (`ldmlKeyboard.dtd`, `keyMap`/`map iso=`), not a
  `.keylayout`, so the CLDR osx data is not a corpus for this reader.
- The kbl specification's `targets/macos.md`, `coverage/macos.md` and `targets/macos-default-keycode.tsv` (branch
  `spec-0.2`, this project's own repository) for every build and import rule.

## Linux / XKB

### The Linux layout check (2026-11-28)

`tools/xkb-check/` (test layouts, Compose files, case table, the C helpers, `inside.sh`, `run.sh`, Dockerfiles) is
our own text and code. The two test layouts and the Compose files were written for this check; no layout was
copied, and no protocol file is committed (KWin's is generated from the distribution's copy when the image is
built). Public sources read 2026-11-28, facts only, no code or prose taken:
- libxkbcommon (https://github.com/xkbcommon/libxkbcommon, MIT): `NEWS.md` (UTF-8 strings as keysyms from 1.9,
  the text format V2 from 1.11, the extension directories from 1.13, the ß to ẞ upper-case mapping),
  `doc/custom-configuration.md` (the user path `$XDG_CONFIG_HOME/xkb`, a user `rules/evdev.xml` for the settings
  apps, "does not work in X11 sessions"), `doc/compatibility.md` (several keysyms per level: parsing only in X11),
  `doc/keymap-text-format-v1-v2.md` (the four-level key types and their Lock handling), `src/compose/paths.c` (the
  Compose file order `$XCOMPOSEFILE`, `$XDG_CONFIG_HOME/XCompose`, `~/.XCompose`), and the public API in
  `xkbcommon.h` / `xkbcommon-compose.h` as installed by the distributions.
- mutter (https://gitlab.gnome.org/GNOME/mutter, GPL-2.0-or-later): `data/dbus-interfaces/org.gnome.Mutter.RemoteDesktop.xml`,
  for the method names and signatures `CreateSession`, `Start`, `Stop`, `NotifyKeyboardKeycode`, and
  `src/compositor/plugins/default.c` (bare mutter reads its keymap from `org.freedesktop.locale1` `X11Layout`,
  `X11Model`, `X11Variant`, `X11Options` on the system bus).
- weston (https://gitlab.freedesktop.org/wayland/weston, MIT): `libweston/backend-x11/x11.c` (the nested X11
  backend compiles its keymap from the X root property `_XKB_RULES_NAMES` and takes the modifier state from the X
  server).
- wlroots (https://gitlab.freedesktop.org/wlroots/wlroots, MIT): `backend/x11/input_device.c` (the nested X11
  backend passes key codes and takes the modifier state from the X server's events).
- KWin (https://invent.kde.org/plasma/kwin, GPL-2.0-or-later): `src/wayland_server.cpp` (fake input is restricted
  only for sandboxed clients) and `src/backends/fakeinput/fakeinputbackend.cpp` (`authenticate` always succeeds;
  `keyboard_key` feeds a fake keyboard device). plasma-wayland-protocols
  (https://invent.kde.org/libraries/plasma-wayland-protocols, LGPL-2.1-or-later): `src/protocols/fake-input.xml`
  (request names and versions; `keyboard_key` since version 4).
- xkeyboard-config's `types/caps` and `types/extra` as installed in the images (the `FOUR_LEVEL_SEMIALPHABETIC`
  type keeps Lock on level 3).
- Runtime messages of the programs themselves (KWin: the nested X11 backend needs DRI3 1.2; xkbcomp's warnings).
