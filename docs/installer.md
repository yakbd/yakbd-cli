# The yakbd installer

`yakbd installer layout.kbl.json` writes **one Windows program**, `<name>-setup.exe`, that carries every build of the
layout (x86, x64, WOW64, and ARM64X once the tool can make it) and installs the files the machine it runs on needs.
Double-click it: it asks once, Windows asks for administrator rights, it installs, and the layout is listed under
"Installed apps", where it can be removed. It adds the layout to the input list of the user who runs it (rule `enableForUser`,
default true) unless the file says `install.enableForUser: false`; `ENABLE=0` and `ENABLE=1` override at run time. Only
when asked (`install.enableForSignInAndNewUsers: true`, or `SIGNIN=1`) it also puts the layout on the sign-in screen
and into the profile new accounts are made from (§2.5). Its command line and exit codes are msiexec's (§4, docs/installer-cli.md).

The format side is kbl-spec `targets/windows-install.md` (D46 to D57), which this design follows.
References: "inst §n" is `spec/installation/README.md` of the reference specification, revision 10;
"kbl windows.md §n" is `targets/windows.md` of yakbd/kbl-spec. Facts about Windows that the reference
specification does not hold come from public Microsoft pages, each named where it is used (and in `CLEAN-ROOM.md`).

## 1. What a machine gets (inst §1, §2.1, §2.4)

| native machine (from `IsWow64Process2`, inst §2.2) | `System32` | `SysWOW64` |
|---|---|---|
| x86 (0x14C) | the `x86` build | – |
| x64 (0x8664) | the `x64` build | the `wow64` build (inst §2.1: always on 64-bit Windows) |
| ARM64 (0xAA64) | the `arm64x` build (inst §2.4) | the `wow64` build |
| anything else | refused | |

The installer never falls back to another build (inst §10 "never fall back to another arch"). An installer
without the machine's builds refuses before it changes anything, with exit code 1633 and a message naming the builds
it does carry. **ARM64**: an installer that carries the `arm64x` build (it passed the ARM64 guest, VM-6) installs it into `System32`
and the `wow64` file into `SysWOW64`; one without it is refused the same way, "this installer has no build for
Windows on ARM" (D46). `-build arm64` builds the ARM64X file explicitly; it joins the default set when TA makes it a
default build.
An AMD64 file is never put into an ARM64 `System32` (inst §2.4).

## 2. The sequence (inst §10, §3.4)

The installer program runs as a 32-bit (x86) process on every machine: it is the one kind of program that starts on
x86, x64 and ARM64 Windows (inst §2.2: an x86 process on ARM64 is redirected like one on x64). Consequences:

- **Files.** The native system directory is addressed as `%SystemRoot%\Sysnative` when the process runs under
  WOW64, `GetSystemDirectoryW` otherwise; the WOW64 directory with `GetSystemWow64DirectoryW` (inst §2.2; the
  `Sysnative` alias: https://learn.microsoft.com/en-us/windows/win32/winprog64/file-system-redirector). The
  per-thread redirection switch is not used for that (Go moves goroutines between threads). **Operations at the next
  restart** (`MoveFileExW` with `MOVEFILE_DELAY_UNTIL_REBOOT`) are the exception: Windows stores the path as given and
  carries the list out at boot, where `Sysnative` does not exist. In the run of the real-user bundle on Windows 10 x64 (tool
  7c4165b) a file marked as `Sysnative\<file>` was stored as `\??\C:\Windows\Sysnative\<file>` and was not deleted;
  marked as `System32\<file>` with redirection off (`Wow64DisableWow64FsRedirection`, on a locked OS thread) it was
  stored as `\??\C:\Windows\System32\<file>` and deleted. So a delayed delete or move of a native file is made the
  second way (`winhost.BootPath`).
- **Registry.** Every HKLM key is opened with the 64-bit view (`KEY_WOW64_64KEY`): `HKLM\SOFTWARE`, where the
  uninstall entry lives, is redirected for 32-bit processes, `HKLM\SYSTEM` is shared
  (https://learn.microsoft.com/en-us/windows/win32/winprog64/shared-registry-keys).

### 2.1 Install

0. Require an elevated token (inst §10). Not elevated and not `/quiet`: the program starts itself again with the
   `runas` verb and waits for it (§3). `/quiet` (`/qn`) never prompts: exit code 740.
1. Machine → files (§1). Check every payload file's SHA-256 and PE Machine when the payload is read, and, before
   anything is written, the Machine and shape of each file the machine gets (kbl windows-install.md §3, inst §10
   step 1): `installer/shape` reads it with the tool's own DLL reader and wants x86 = 014C with 4-byte pointers,
   x64 = 8664, wow64 = 014C with the 8-byte slots, arm64x = AA64 with an x64 view in the shipped `arm64x-x64` shape
   (docs/arm64x.md §5). A file that fails: exit code 1620, "This installer is damaged: ... Nothing was changed." The
   file written is checked again after it is read back.
2. **Ownership.** Look for KLIDs whose `Layout Product Code` is the installer's product code (inst §3.2, §10 step
   0), and for the installer's own record (its uninstall entry, §2.3). Found → **upgrade in place / repair** (2.2).
3. **Files** (inst §10 step 1, §2.3). For `System32` (native) and, on 64-bit Windows, `SysWOW64`: a file of the
   layout's name that exists and is not this product's (no KLID of the product names it) is a foreign file,
   even when its bytes are equal: refuse ("name taken"), never overwrite (kbl windows-install.md §4 step 2). A target that
   an earlier uninstall left for deletion at the next restart is a refusal too: restart first (D62).
   Write `<name>.dll.yakbd-tmp`, rename it over the target, read it back and compare.
4. **KLID** (inst §3.1, §10 step 2). A file that pins `registration.klid` gets exactly that KLID, created
   atomically (`RegCreateKeyExW`, disposition `REG_CREATED_NEW_KEY`); an existing one is a refusal. Otherwise the
   first `aNNNLLLL`, NNN from `000` up to `ffe` (high word `0xA000`–`0xAFFE`), that no loaded user hive
   (`HKU\*`, `.DEFAULT` included) references in `Keyboard Layout\Preload`, `Keyboard Layout\Substitutes` or
   `Control Panel\International\User Profile\*\<LLLL>:<KLID>` and that is created new. LLLL = `registration.langId`.
5. **Layout Id** (inst §3.3). Every `Layout Id` value of every KLID is parsed the way Windows reads it (hex text of
   any length, optional `0x`, any case; a DWORD, empty or non-hex value counts as 0); a KLID with a non-zero high
   word and no `Layout Id` counts as using 0xFFE (its HKL high word is `FFFE`). A pinned `registration.layoutId`
   that another KLID of the same LANGID uses is a refusal. Otherwise the highest free id from `00ff` down to `00c0`
   (inst §10 step 2; §3.3 "counting down from 00ff"); none free → refuse.
6. **Values** (inst §3.2, §10 step 2), `Layout File` first: `Layout File` = `<name>.dll`; `Layout Text` =
   `registration.layoutText`; `Layout Display Name` (REG_EXPAND_SZ) = `@%SystemRoot%\system32\<name>.dll,-1000`;
   `Layout Id` = 4 lower-case hex digits; `Layout Product Code` = the product code. For a custom language
   (`customLanguageName` given, or LANGID `0C00`, inst §6): `Custom Language Name` and `Custom Language Display
   Name` (`...,-1100`); LANGID `0C00`: `Layout Locale Name`.
7. **Uninstall entry** (§2.3): the installer copies itself to `%ProgramFiles%\YAKBD\<product code>\setup.exe` and
   writes `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\<product code>`.
8. Any failure in 3–7 rolls back in reverse order: uninstall entry, KLID key, the files this run created
   (inst §3.4, §7 "fail visibly and roll back").
8a. **Sign-in screen and new users** (§2.5), still in the elevated part, when the option is on. Its failure does
   not roll back steps 3–7.
9. **Enable** (rule `enableForUser`, default true; `ENABLE=0` skips it; inst §4.3, §10 step 3): `InstallLayoutOrTip("LLLL:KLID", 0)` with upper-case hex,
   no prefix (the spelling Windows stores, inst §4.3), called by the user's own, unelevated process (§3). The return
   value proves nothing (inst §4.3), and of several quick calls only the first reached the list in the ARM64 bundle's sign-in
   run, so the program reads `User Profile` back and calls again (three tries, 0.5 s apart growing); still missing
   is an enabling failure (exit code 1603). It then also looks for the HKL `MAKELONG(LANGID, 0xF000 | LayoutId)`
   (inst §3.2) in `GetKeyboardLayoutList`; when it is missing it says "sign out and in again" (inst §5) and still
   counts the install as done. LANGID `0C00` (kbl windows-install.md §6, D63): the program adds `registration.localeName` to the user's
   language list when it is missing (Get-/Set-WinUserLanguageList, read-modify-write, inst §4.4), records that in
   `HKCU\Software\YAKBD\<product code>\AddedLanguageTag`, reads the tag's `TransientLangId` and enables
   `<transient>:<KLID>`; the uninstaller removes the tag only when it added it (the default input method Windows
   gave the new tag goes with it). Any enabling failure
   keeps the machine part and ends with exit code 1603.

### 2.2 Upgrade in place and repair (inst §10 `UPGRADE_IN_PLACE`, §4.6, §7 M5b, M7)

Same KLID, same Layout Id. Files: replaced as in step 3; a file that is in use (sharing violation) is written as
`<name>.dll.yakbd-new` and moved over the old one at the next restart (`MoveFileExW` with
`MOVEFILE_DELAY_UNTIL_REBOOT | MOVEFILE_REPLACE_EXISTING`), exit code 3010. Values: rewritten, `Layout Id` kept.
When the KLID key is gone but the uninstall entry names it (a feature update dropped it, M7), the key is created
again with the recorded KLID and Layout Id; when Windows has taken that Layout Id meanwhile (another KLID of the same
LANGID has it), a new one is allocated. `setup.exe /f` (the copy in Program Files; the uninstall entry's `ModifyPath`) runs this path on purpose. An installer older than the installed version is refused with 1638 (docs/installer-cli.md §6).

**Re-enabling after a new Layout Id** (kbl windows-install.md §5, inst §10). The KLID stays, so every user's
`Preload`, `Substitutes` and `User Profile` entries still name the layout; only the HKL changes (inst §3.2), and
the CTF state holds it (inst §4.5, OBS I19). The machine part does not touch per-user state. Before it, the program
notes whether its own user has the layout (a hive entry naming the KLID) and the recorded Layout Id; afterwards,
when the Layout Id changed and the user had the layout, that user's own process (the unelevated first process, §3)
removes the layout (`ILOT_UNINSTALL`) and adds it again with each `<LANGID>:<KLID>` string of its `User Profile`
(D69, VM-9: in the hardening bundle's first run one plain `InstallLayoutOrTip(..., 0)` left the session on the old
HKL, as inst §5 has a loaded layout stay loaded until sign-out); the user's only layout is not removed first. It
reads the list back and logs whether the new HKL is in `GetKeyboardLayoutList` (that list may lag behind, so
missing only gives "sign out and sign in again"). Removing an LCID language's only input method removes the
language too, and adding brings it back (inst §6 item 4). `ENABLE=0`
skips this. Other users' hives are logged, not changed; the program tells them to sign out and in again (D69).

### 2.3 The uninstall entry ("Installed apps")

Key `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\<product code>` (per machine, inst §7 M4), values from
https://learn.microsoft.com/en-us/windows/win32/msi/uninstall-registry-key : `DisplayName`, `DisplayVersion`,
`Publisher` (when known), `InstallLocation`, `UninstallString` = `"<InstallLocation>\setup.exe" /x <product code>`, `QuietUninstallString` (the same with `/quiet /norestart`), `ModifyPath` (`/f`), `DisplayIcon` (docs/installer-cli.md §9),
`EstimatedSize` (KiB, DWORD). The installer's own record, in the same key: `YakbdKLID`, `YakbdLayoutId`,
`YakbdLayoutFile`, `YakbdLangId`, and `YakbdSignIn` (`1` when §2.5 was applied, `0` when it was turned off or not
applied; absent after a plain install, read as `0`). Nothing else is stored anywhere (the 0C00 language tag record of
§2.1 step 9 excepted).

### 2.4 Uninstall (inst §10 `UNINSTALL`, refusal policy)

1. Require elevation (as install).
2. KLIDs = every KLID with the product code, plus the one the record names (inst §10: all of them).
2a. **The sign-in screen and the Default profile first, always** (§2.5, kbl windows-install.md §9 step 1), whether
   or not the install applied the option and whatever `SIGNIN` says. The refusals of this step and of step 3 are
   checked before anything is removed.
3. For every loaded user hive (`HKU\<SID>` without `_Classes`; `.DEFAULT` = S-1-5-18, S-1-5-19 and S-1-5-20 are
   not touched, D57): find references (step 4 of §2.1).
   A hive whose `Preload` resolves (through `Substitutes`) to this layout and nothing else: **refuse**, naming the
   hive, exit code 1603 (`FORCE=1` overrides). Otherwise remove: the elevated process's own hive with
   `InstallLayoutOrTip("LLLL:KLID", ILOT_UNINSTALL)`; other hives with `InstallLayoutOrTipUserReg(<SID>, NULL,
   NULL, "LLLL:KLID", ILOT_UNINSTALL)` and the `User Profile` value removed by hand (inst §4.3 "the replacement must
   write (or remove) the User Profile value itself"); references still left afterwards are removed by hand
   (`Substitutes` value, `Preload` entry, Preload renumbered 1..n; inst §10 fallback).
4. Delete the KLID keys.
5. Delete `System32\<name>.dll` and `SysWOW64\<name>.dll`; a file in use is deleted at the next restart
   (`MOVEFILE_DELAY_UNTIL_REBOOT`, inst §5, §10), exit code 3010 and the message "restart to finish".
6. Delete the uninstall entry and `%ProgramFiles%\YAKBD\<product code>`. The running `setup.exe` cannot delete
   itself: it moves itself into `%TEMP%` (same volume) and marks that copy for deletion at restart; if the move
   fails, the file itself is marked.

**Not done** (inst §10 branches left out): offline user profiles (kbl
windows-install.md §9 step 2: stale references there are ignored); "default input method" detection beyond "only
layout of the hive"; `SetDefaultLayoutOrTip`; restarting ctfmon. The welcome screen and new users are §2.5,
the transient-LANGID path is §2.1 step 9.

### 2.5 Sign-in screen and new users (opt-in; kbl windows-install.md §7.1, §9 step 1, D87)

Rule `enableForSignInAndNewUsers`, **default off** (inst §4.7: a layout on the sign-in screen that fails to load is
risky). On with `install.enableForSignInAndNewUsers: true` in the file, `yakbd installer -signin true`, or the
property `SIGNIN=1` at run time (docs/installer-cli.md §3). It is independent of enabling for the user.

**The four places**: `HKU\.DEFAULT` (the sign-in screen), `HKU\S-1-5-19`, `HKU\S-1-5-20`, and the Default profile,
`NTUSER.DAT` in the folder `GetDefaultUserProfileDirectoryW` names
(https://learn.microsoft.com/en-us/windows/win32/api/userenv/nf-userenv-getdefaultuserprofiledirectoryw), loaded with
`RegLoadKeyW` as `HKU\YAKBD-<product code>` (with `SeRestorePrivilege` and `SeBackupPrivilege` enabled, as the
RegLoadKeyW page requires; the file must exist, because RegLoadKeyW would create it) and unloaded with
`RegUnLoadKeyW` on every path. A name that is still loaded from an earlier run whose unload failed is used as it is
and unloaded. Existing users' hives are never touched here.

**Install** (the elevated part, after the uninstall entry, D97 order: when the elevated process is the session
user's own account it enables the layout for that user first, then writes the places; after over-the-shoulder
elevation the first process enables later, as before), for the installer's own KLID only, writing only what is
missing (a second run adds nothing), in each place:
0. the language: `<tag>` is the subkey of the place's `User Profile` that already holds a `<LLLL>:` value. When there
   is none, the installer writes Windows' own copy shape (VM-19): the tag under which Windows filed the LANGID in a
   loaded user hive (`HKU\S-1-5-21-*`, this process's user first; a `User Profile` subkey with a `<LLLL>:` value) is
   appended (at the end, never first) to `User Profile` `Languages` and, in the three system hives, to
   `User Profile System Backup` `Languages`, and the `<tag>` subkeys are created; the uninstall entry records it
   (`YakbdSignInTags`, REG_MULTI_SZ, `<place>=<tag>`). No user hive has the language: as LocalSystem (D98) the installer
   calls `InstallLayoutOrTip("<LLLL>:<KLID>", 0)`, which files the language in `.DEFAULT` under Windows' own tag
   (VM-26: `fo`), reads that tag back, uses it for every place, adds the System Backup part in `.DEFAULT` and records
   `.DEFAULT=<tag>` as added (uninstall and rollback remove it from `.DEFAULT`'s `Languages`); otherwise the option
   fails (below) and the message says to add the layout to a user's list and run `setup.exe /f SIGNIN=1` (D97);
1. `InstallLayoutOrTipUserReg(<hive name>, NULL, NULL, "<LLLL>:<KLID>", 0)`: `Preload` and `Substitutes` (T8's shape
   `<n> = d<NNN><LLLL>`, `d<NNN><LLLL> = <klid>`; for the first layout of a language Windows writes `0000<LLLL>`);
2. the DWORD value `<LLLL>:<KLID>` under `Control Panel\International\User Profile\<tag>`, data 1 + the highest data
   in that subkey (D97);
3. in the three system hives the same value under `User Profile System Backup\<tag>` (required: D97, Windows removed
   entries without it from `.DEFAULT` at the restart).
`S-1-5-19` or `S-1-5-20` not loaded: skipped and logged. Every place is read back. Any failure takes back what this
run wrote (and only that: entries that were there before stay; an added tag goes again), records `YakbdSignIn = 0`,
lets the user's enabling run, and ends with **1603** (the elevated copy marks it in its result file for the first
process). A failed unload is reported and is not a failure (the hive stays loaded until the restart). The end message
says that a restart may be needed before the sign-in screen shows the layout.

**Not applied**, with a note and `YakbdSignIn = 0`, exit code that of the rest of the run: on ARM64 (inst §2.4) and
for a LANGID `0C00` layout (per-user transient LANGID, inst §6; such a file with the field on does not build,
`windows.registrationInvalid`, and neither does `-signin true`).

**Upgrade and repair**: without `SIGNIN` the recorded state is kept (recorded on: the missing entries are written
again; off: nothing is added; the file's field decides a fresh install only). `SIGNIN=1` applies it and records on;
`SIGNIN=0` removes the layout from the four places as uninstall does and records off.

**Uninstall** (§2.4 step 2a): in each loaded place `InstallLayoutOrTipUserReg(..., ILOT_UNINSTALL)` for every
`<LLLL>:<KLID>` string found (and the KLID's own LANGID), then the `<LLLL>:<KLID>` values under `User Profile\*` and
`User Profile System Backup\*` (a System Backup key left empty is deleted), and a `Substitutes` value still naming the
KLID with its `Preload` entry (Preload renumbered); languages stay, except a tag `YakbdSignInTags` records for that place, which is removed from both `Languages` values with its subkeys when no layout value is left in them (D97). Read back: anything left is a failure (1603). The
layout as the only layout of the sign-in screen (`.DEFAULT`) is a refusal with nothing changed (`FORCE=1` overrides). A Default profile that
cannot be loaded is skipped with a note (stale entries there are ignored, inst §4.7).

## 3. Elevation and who is enabled (inst §4.3 "over the shoulder", §10 notes)

Double-click → the program (unelevated, the user at the screen) asks "Install <layout>?", then starts itself again
elevated (`ShellExecuteExW`, verb `runas`, with `/elevated /qn /norestart /l*+ <its log>`) and waits for its exit code. The
elevated copy does §2.1 steps 0–8. Back in the first process, the user's own, step 9 runs when enabling was asked
for: the layout lands in the hive of the person at the keyboard even when the administrator was another account.
A program started elevated already (an elevated prompt, a deployment tool, `/quiet`) does everything itself and
logs the SID of the hive it enabled the layout in. Before enabling, such a program compares its account with the user of its
session (`WTSQuerySessionInformationW`, user and domain name): when they differ (elevation with another account's
credentials, or LocalSystem) it does not enable, says so, and still ends with exit code 0: the per-machine install is done (a deployment as LocalSystem is a success; docs/installer-cli.md §4). This matters because Windows
may elevate the program at launch: UAC's installer detection is on by default on Home editions
(https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/settings-and-configuration,
"Detect application installations and prompt for elevation"; registry value `EnableInstallerDetection`, on by default
for Home editions only). Uninstall works the same way (§2.4 step 3 covers the hive of the
user at the keyboard either way, because it is loaded).

### 3.1 Application manifest: execution level `asInvoker`

The program carries an application manifest (§5.1) that asks for `requestedExecutionLevel level="asInvoker"`
(https://learn.microsoft.com/en-us/windows/win32/sbscs/application-manifests, "trustInfo"). Why this level and no
other: the per-user step must run as the user at the screen (inst §4.3, measured: after "over the shoulder"
elevation the elevated process is the administrator and `InstallLayoutOrTip` writes the administrator's hive; inst §10
notes: enable from the original user's unelevated process).

| level | a double-click by an administrator (consent prompt) | by a standard user (credentials of an administrator) |
|---|---|---|
| `asInvoker` (chosen) | first process: the user, unelevated; one prompt for the elevated copy; enabling in the first process | the same: the first process is the standard user, the elevated copy the administrator, enabling in the first process, so the standard user's list |
| `requireAdministrator` | the whole program elevated at start, the same account: works | the whole program is the administrator: enabling would write the administrator's list, so the program skips it (exit code 0) |
| `highestAvailable` | as `requireAdministrator` | as `asInvoker` |
| no manifest | depends on installer detection (above): may be elevated at start | may be elevated at start with the administrator's credentials, then no enabling (exit code 0) |

With `asInvoker` both kinds of user take the same path (§3 above): the question in the user's own process, one
prompt, the machine part in the elevated copy, enabling back in the user's process. A deployment tool that already
runs elevated gets no prompt (an elevated caller starts the program elevated). Whether Windows' installer detection
still elevates a program whose manifest names a level is not stated in the pages read; the w11x64 bundle checks it
once with `EnableInstallerDetection` = 1. `uiAccess` is false. The manifest also
names Windows 10/11 as supported OS, Common Controls 6 (current look of the message boxes) and DPI awareness
`PerMonitorV2` (`true/pm` before Windows 10 1607), so the message boxes are drawn at the monitor's scale rather than
stretched. The program is a GUI-subsystem program, so no console window opens.

## 4. Command line of the installer program

msiexec's command line and Windows Installer's exit codes (owner 2026-11-26; the contract with its sources:
docs/installer-cli.md). The program is the package, so the operand of `/i`, `/x` and `/f` is optional:

    setup.exe [/i] [/quiet | /passive | /qn | /qb ...] [/norestart] [/l*v <file> | /log <file>] [ENABLE=0|1] [SIGNIN=0|1]
    setup.exe /x [{product code}] [/quiet] [/norestart] [FORCE=1] [/log <file>]
    setup.exe /f [{product code}] [/quiet] [/norestart] [SIGNIN=0|1] [/log <file>]

Scripts and test bundles: `/quiet /norestart` for silent runs (without `/norestart`, `/quiet` restarts Windows by
itself when a file waits for the restart, as msiexec does, exit 1641); `/f` repairs (was `/repair`); `ENABLE=1` /
`ENABLE=0` (were `/enable` / `/noenable`); `FORCE=1` (was `/force`). Log: `%TEMP%\yakbd-<name>-setup.log` by
default, appended to; `/l` replaces its file unless `+`.

| exit code | meaning |
|---|---|
| 0 | done (also when enabling was skipped because the program runs as another account than the session's user) |
| 3010 | done; a file waits for the restart, restart to finish |
| 1641 | done; the program started the restart |
| 1602 | the user said no (a dialog or the elevation prompt) |
| 1603 | failed and rolled back, or refused with nothing changed (foreign file, only layout of a user: `FORCE=1`), or installed but enabling for the user failed |
| 1605 | `/x` or `/f` when the layout is not installed |
| 1618 | another yakbd installer is running |
| 1620 | this installer is damaged (payload, file shape) |
| 1622 | the `/l` file cannot be opened |
| 1633 | this machine needs a build the installer does not carry (or an unsupported machine) |
| 1638 | a newer version of the layout is installed |
| 1639 | bad command line |
| 740 | needs administrator rights (`/quiet` without elevation) |

## 5. The file: stub + payload

    <name>-setup.exe = stub (a Windows x86 PE image) + .rsrc section (§5.1) | payload | [Authenticode signature]

The **stub** is a Go program (`installer/stub`, standard library only, `syscall` for the Windows API), built for
`windows/386`. The **payload** starts at the end of the stub's last section (the largest `PointerToRawData +
SizeOfRawData`), so the stub finds it by reading its own PE header, not by counting from the end of the file: a
signature appended later (certificate table) does not move it. This is the **signing point**: a signing step runs
on the finished file (for example `signtool sign`). yakbd's installers are unsigned on purpose, as MSKLC's are (inst
§7 M5c; owner decision 2026-11-25): the elevation prompt shows "Unknown publisher", Smart App Control blocks them
(inst §11 T12) and SmartScreen may warn (inst §7 M8). There is no `.msi`: the owner abandoned MSI (2026-11-26); the
`.exe` takes msiexec-style switches instead (docs/installer-cli.md).

Payload (all integers little-endian):

| offset | size | field |
|---|---|---|
| 0 | 8 | magic `YAKBDPAY` |
| 8 | 4 | format version, 1 |
| 12 | 4 | manifest length M |
| 16 | 8 | data length D |
| 24 | 32 | SHA-256 of the M + D bytes that follow |
| 56 | M | manifest, UTF-8 JSON |
| 56+M | D | the files, back to back, at the offsets the manifest gives |

Manifest:

```json
{
  "format": 1,
  "tool": "yakbd 0.2.0",
  "product": {"name": "...", "version": "1.0.0.0", "publisher": "...", "code": "{8-4-4-4-12 GUID, upper case}"},
  "layout": {"file": "kbdtst.dll", "langId": "0409", "layoutText": "...", "klid": "", "layoutId": "",
             "localeName": "", "customLanguageName": ""},
  "enable": false,
  "signIn": true,
  "files": [{"build": "x64", "machine": 34404, "offset": 0, "size": 5632, "sha256": "..."}]
}
```

`signIn` is written only when true (§2.5). `build` is one of `x86`, `x64`, `wow64`, `arm64x`. The writer is deterministic: the same input and tool give the
same bytes (no time stamps, fixed order).

### 5.1 Resources: manifest, icon, version

`installer/exeres` appends one section `.rsrc` to the stub when `yakbd installer` writes the file (the version depends on
the layout, so the stub itself is built without resources and `tools/buildstub` is unchanged). The section starts at
the stub's old `SizeOfImage` and at the old end of the file; the section count, `SizeOfImage` and data directory 2 are
updated (PE format, https://learn.microsoft.com/en-us/windows/win32/debug/pe-format). The payload follows it, so the
stub still finds the payload at the end of its last section. Resources, all language 0409:

| type | id | content |
|---|---|---|
| RT_MANIFEST (24) | 1 | the manifest of §3.1 |
| RT_ICON (3) | 1–4 | the icon at 16, 32 and 48 pixels (32-bit DIB with alpha) and 256 pixels (PNG), drawn by `exeres`' own code: a dark keyboard with two rows of light keys, one blue key and a space bar (no third-party art) |
| RT_GROUP_ICON (14) | 1 | the group of the four images, the program's icon in Explorer |
| RT_VERSION (16) | 1 | file type application; numeric file and product version from the leading numbers of the product version (`0.2.0-beta` → 0.2.0.0); strings `CompanyName` (the publisher, only when there is one), `FileDescription` "<product> setup", `FileVersion` and `ProductVersion` (the product version as written), `InternalName` "setup", `OriginalFilename` "<dll name>-setup.exe", `ProductName` |

Product name, version and publisher are those of the uninstall entry (rule `uninstallEntry`, §7). Deterministic like
the rest: the same input gives the same bytes. Tested on Linux (`installer/exeres`: section placement, `debug/pe` reads the
file, the manifest's level and namespaces, the version strings with and without a publisher, the icon group and
every image); on Windows by every real-user bundle (§8.2), whose installers all carry it.

**Container interface.** `installer.Container` (`Name() string`, `Write(*Package) ([]byte, error)`) turns a
`Package` (manifest + files) into the output file; `ExeContainer` is the only one. MSI is not offered (owner,
2026-11-26): deployment tools get msiexec-style switches, exit codes and logging from the `.exe` itself
(docs/installer-cli.md).

### 5.2 DLL planting

An installer usually runs from a Downloads folder, where other files may lie next to it. Windows searches the
program's own folder for a DLL named without a path (and for the dependencies of every DLL it loads) before the
system folder, unless the name is a Known DLL
(https://learn.microsoft.com/en-us/windows/win32/dlls/dynamic-link-library-search-order, read 2026-11-27). So the
installer programs (`installer/stub`, `installer/toolstub`, through `installer/winhost`) load nothing from their folder:

- at start (package `winhost`'s `init`) they call `SetDefaultDllDirectories(LOAD_LIBRARY_SEARCH_SYSTEM32)`, so the
  default search path of the process is the system directory alone; the page says this path is also used when
  Windows searches for the dependencies of a DLL loaded by full path, and the check of §8.4 measures it
  (https://learn.microsoft.com/en-us/windows/win32/api/libloaderapi/nf-libloaderapi-setdefaultdlldirectories);
- every DLL the code names is loaded by its full path in the system directory (`systemDLL`), except `kernel32.dll` and
  `advapi32.dll`, which Go's `syscall` package itself loads from System32 only (Go source, `syscall/dll_windows.go`,
  `LoadDLL`).

Before this (tool commit 2063f05 and earlier) `user32`, `ole32` and `wtsapi32` were loaded by bare name.
`go run ./tools/instbundle -guest w11plant -o DIR` writes the Windows check: a planted copy of every DLL name of the
guest's system folders next to the installer, for the double-click and the `/quiet` path (§8.4).

## 6. How the stub gets into yakbd

`installer/stub-386.exe` is committed and embedded with `//go:embed`. It is built by

    go run ./tools/buildstub          # writes installer/stub-386.exe

which runs `go build` for `GOOS=windows GOARCH=386 GO386=sse2 CGO_ENABLED=0` with `-trimpath -buildvcs=false
-ldflags "-s -w -H=windowsgui -buildid="` and an empty `GOFLAGS`, and records the Go version in
`installer/stub-386.version`. The stub imports the standard library, `installer/cmdline`, `installer/payload`, `installer/setup`,
`installer/winhost` (the Windows API side) and `installer/shape`, which brings the tool's DLL reader (`target/windows`, `pe`, `loss`; all standard library only),
so a change in any of those eight packages changes the stub, and nothing else does. The test
`TestEmbeddedStubIsFresh` builds the stub the same way and compares it byte for byte with the embedded one; it
fails in CI (`CI` set) and is skipped elsewhere when the local Go version differs from the recorded one. CI pins
that exact Go version. `yakbd` itself needs no Go, WiX or MSVC at run time, on any OS.

## 7. `yakbd installer`

    yakbd installer [flags] layout.kbl.json|layout.klc|layout.xml
      -o file           output (default <name>-setup.exe in the current directory)
      -build list       machines to carry: x86, x64, arm64 (default: all the tool can build for)
      -enable true|false  override install.enableForUser (default: the file, else rule enableForUser = true)
      -signin true|false  override install.enableForSignInAndNewUsers (default: the file, else rule enableForSignInAndNewUsers = false)
      -langid, -accept, -report, -strict-defaults, -q   as for compile

`-build x64` carries the `x64` and the `wow64` file (inst §2.1 requires both on x64 Windows), `-build arm64` the
`arm64x` and the `wow64` file, `-build x86` the `x86` file. With the default, a machine kind whose builds the tool
cannot make (ARM64 today) is left out and reported; an explicit `-build arm64` is then an error.

Rules of kbl windows-install.md §1, each use reported as `windowsDefault.<rule>` and written into the file by
`yakbd pin`: `productCode` (UUID version 5 of the namespace `52711d5d-5dcb-4d0b-b8bb-9852abd899ea` and the
lower-case `metadata.id`, a space and the lower-case DLL name (D61), so every rebuild upgrades in place), `enableForUser` (true), `enableForSignInAndNewUsers` (false, §2.5), `uninstallEntry` (name
`metadata.name`, version `versionInfo.productVersion`, publisher `metadata.organization`). The build reports
`windows.registrationInvalid` (a KLID not of the LANGID, a Layout Id outside 0001-0FFF, a per-user LANGID, the sign-in option for a LANGID 0C00 layout) and
`windows.installerMachinesMissing` (the machine kinds the installer refuses, ARM64 today). Every file is put through
the shape check of §2.1 step 1 before it is packed; one that fails is an error (`installer.fileShape`) and nothing is
written.

## 8. Tests

- Linux, in CI: `installer/payload` (format, damage, the payload found from the section table with bytes after
  it), `installer/setup` against an in-memory Windows (placement per machine; Layout Id parsing and allocation;
  KLID allocation skipping referenced KLIDs; foreign file; rollback after a failure at each stage; pinned KLID and
  Layout Id; custom language; refusals change nothing; upgrade in place with a locked file; repair from the
  uninstall entry, also when the Layout Id was taken; enable; uninstall from several hives with Preload
  renumbering; the only-layout refusal and `FORCE=1`; versions (1638 for an older installer); the uninstall entry's command lines; files in use; a pending replacement removed; a file that fails
  the shape check refuses a fresh install and an upgrade with nothing changed; re-enabling after a new Layout Id,
  the new HKL seen or not, a user who never had the layout; the sign-in screen and new users of §2.5: T8's shape in
  the four places, a second run adds nothing, a place without the language, a failed load taking back only what the
  run wrote, a failed unload reported and the still-loaded hive reused, repair and `SIGNIN=0`/`1`, not applied on
  ARM64 and for 0C00, uninstall cleaning entries Windows' own copy left, the only-layout refusal of the sign-in
  screen, a Default profile that cannot be loaded), `installer/shape`
  (every build's file passes as its own build and fails as each other one; plain ARM64, `arm64x-ec` and
  `arm64x-x64-onecfg` fail as arm64x; truncated, non-PE, wrong-Machine, export-less and load-configuration-less
  files fail), `installer`
  (package contents, determinism, the stub's PE shape, a bad-shape file is not packed, `TestEmbeddedStubIsFresh`),
  `internal/cli`.
- Windows: `go run ./tools/instbundle -o DIR` writes a bundle (installers, expectations, `tools/instcheck` for 64
  and 32 bits, README.txt) that a runner executes in a disposable guest; scenarios in its README.

### 8.1 Verified on Windows (2026-11-22)

Bundles for x64 and ARM64, made by `tools/instbundle`, run in disposable
guests by the runner:

- **x64** (Windows 11 25H2, tool commit 0537866): 13 of 13. The x86-only installer refuses; the all-builds installer
  places the x64 file in System32 and the WOW64 file in SysWOW64, registers the KLID with every value, writes the
  uninstall entry and enables the layout; it types in 64- and 32-bit processes (216 lines each, HKL F0FF0409);
  install again, upgrade and repair keep KLID and Layout Id; uninstall leaves nothing; a foreign file is never
  overwritten; an x64-only installer built with `-enable false` does not enable; three layouts of one language
  installed back to back are all in the list and type; the `0C00` layout is enabled through its tag and transient
  LANGID and removed with the tag (Windows' own `CachedLanguageName` value excepted). Run 1 (earlier commit) had
  11 of 12, the failure being that value.
- **ARM64** (Windows 11 ARM64, tool commit 0537866): install 1/1 (ARM64X file in System32, WOW64 file in SysWOW64),
  after a cold boot with automatic sign-in the layout is in the user's list and the session with ctfmon running and
  types in native ARM64, x64 and x86 processes (216 lines each), uninstall 1/1.
- In both runs no layout file was in use at uninstall: the files were deleted at once (through `Sysnative`), and the
  only operation left for the restart was the uninstaller's own copy in `%TEMP%`. How a `Sysnative` path is stored
  for a delayed operation is therefore still not observed.
- **Hardening run** (x64, tool commit 58e90f3): 18 of 18. The 13 earlier scenarios pass
  again; installers whose wow64 file is the x86 build or whose x64 file is truncated (hashes and Machine right) exit 1
  and change nothing, the log naming the file; a repair whose Layout Id was taken by another KLID of the language
  (M7) gives Layout Id 00fe, removes the layout and adds it again (D69), and HKL F0FE0409 is in a new process at once
  and after a cold boot with automatic sign-in, typing 216 of 216 lines in 64- and 32-bit processes; cleanup leaves
  nothing. Run 1 (2036f80, one plain `InstallLayoutOrTip` call) kept the old HKL F0FF0409 in the session (VM-9).
- Not run: the double-click path (the unelevated first process, its dialogs and the elevation prompt), the
  over-the-shoulder case, anything finished by a restart, x86 Windows.

### 8.2 Real-user bundles

`go run ./tools/instbundle -guest w11x64|w10x64|w10x86 -o DIR` writes one bundle per guest kind (README.txt with the
phases, BUILT-FROM.txt, one combined result file `results\<guest>.txt` from `instcheck -phase summary`):

- **w11x64**: the double-click path as Explorer starts it (ShellExecute "open"): the checker presses the installer's
  own dialogs (window messages to its message boxes); the elevation prompt, on the secure desktop, is answered by
  the runner with host keystrokes when the marker file `results\uac-waiting.txt` says so (consent: Alt+Y;
  credentials: the administrator's password), two screenshots of the prompts. An administrator at the consent
  prompt and a standard user over the shoulder install, type, and remove the layout through the uninstall entry's
  command (the command Settings runs); installer detection switched on for one phase must not elevate the
  asInvoker program at its start; D69: the standard user signed in while an administrator repairs with a taken
  Layout Id, then after the restart; the only-layout repair around a restart; a layout file held open at uninstall
  (3010), gone after the restart, with three probe files that show how a path through `Sysnative` is stored.
- **w10x64** and **w10x86**: the hardening scenarios (on x86 Windows with x86-shaped bad payloads, the x86-only
  installer installing and the x64-only one refusing), the sign-in check after a repair, the held file at
  uninstall, and the typing run of `tools/vmbundle`.

Results (Windows 10 22H2, runners in the console session):
- **w10x64**, run 2 (tool fdfa2cc): 87 of 87. The 15 hardening scenarios; the sign-in check after a repair that took a
  new Layout Id (3 of 3); the held layout files at uninstall (3010, stored as `\??\C:\Windows\System32\<file>` and
  `\??\C:\Windows\SysWOW64\<file>`, both gone after the restart, install and removal again clean); the typing run 33
  of 33 in the x64 run and 33 of 33 in the WOW64 run. Run 1 (7c4165b) had 86 of 87: the delayed delete through
  `Sysnative` was stored as given and not done at boot (§2, fixed in fdfa2cc).
- **w10x86**, native 32-bit (tool 7c4165b): 53 of 53. The x86-only installer installs the x86 file into System32,
  the x64-only one refuses (1633); the 15 scenarios, with x86-shaped bad payloads refused (1620); the sign-in check
  after a repair; the held file at uninstall (stored as `\??\C:\Windows\system32\<file>`, gone after the restart);
  the typing run of every corpus layout's x86 DLL, 33 of 33. The Sysnative fix does not concern x86 Windows.
- **w11x64** (Windows 11 25H2), run 2 (tool 778b292): 15 of 17, and the two failures were only the test's UI
  Automation of Settings (below). Passed: the double-click install by an administrator (consent prompt: "demo-setup.exe",
  "Publisher: Unknown", "File origin: Hard drive on this computer") and by a standard user with an administrator's
  credentials (the layout in the standard user's list only), every prompt in front and answered once; installer
  detection on with the asInvoker manifest (the program's question came before any prompt); the D69 cases (the
  second user signed in during the repair, and after the restart with the new HKL, typing 216/216 in 64- and 32-bit
  processes; the only-layout repair); the held files at uninstall, gone after the restart (run 1, 7c4165b, had
  stored the Sysnative path as `\??\C:\Windows\SysWOW64\<file>` here, so the native file stayed: fixed in fdfa2cc).
- **Removal in Settings > Installed apps**, verified by hand (bundle `ti4-w11settings`, tool cf377bf, Windows 11
  25H2): the runner pressed the entry's "..." > Uninstall > Uninstall. For the administrator Settings showed the
  consent prompt for setup.exe first (Settings starts the uninstall entry's command elevated), then our question,
  "was removed", exit 0; for the standard user the credential prompt (administrator offered) first, then the same.
  Both logs show the run started with the entry's command (`/x {product code}`); entry, KLID and Program Files
  folder gone (`instcheck -phase settings-manual-check`, PASS for both). Because Settings elevates first, the
  uninstaller runs as the administrator, its log is in the administrator's `%TEMP%`, and the standard user's own
  entries are removed by the loaded-hive pass of §2.4 step 3 (`InstallLayoutOrTipUserReg` and the `User Profile`
  value), the same pass that removed them in w11x64 run 2 when the entry's command ran over the shoulder (log:
  "hive S-1-5-21-...-1002: InstallLayoutOrTipUserReg(\"0409:A0000409\", ILOT_UNINSTALL): ok", "User Profile value
  en-US\0409:A0000409 removed"; the check found the layout gone from the user's list). The manual run itself had the
  layout enabled for nobody, so it did not repeat that check. UI Automation of Settings failed in three runs
  (wrong search box, then names and ids that did not lead to a started uninstaller) and was dropped: the bundles
  now start the entry's command directly for the automated phases, and Settings is a by-hand check.
- **tx-cli** (msiexec-compatible command line, w11x64, tool 3db00a5): 24 of 24, for a layout installer and
  `yakbd-setup.exe`, including an automatic restart by `/quiet` (1641) and the delayed replacement landing after it
  (docs/installer-cli.md §12, "Verified on Windows").

### 8.3 Sign-in screen bundles: the sign-in screen and new users

`go run ./tools/instbundle -guest w11signin -o DIR` writes the w11x64 bundle of §2.5 (kbl D87; questions VM-18 to
VM-23 of the spec's `targets/windows-vm-tests.md`): `demo-setup.exe` (0409, `SIGNIN=1` at run time),
`fo-signin-setup.exe` (the 0438 example with `install.enableForSignInAndNewUsers: true`), `minimal-setup.exe` (whose
entries the test writes into `.DEFAULT` and the Default profile only, for VM-23), and `tools/instcheck` phases `si-*`
(`tools/instcheck/signin_windows.go`): readback of the four places (the Default profile from a copy, loaded and
unloaded), two screenshots of the sign-in screen's input switcher, an account created after the install and one
after the uninstall, repair and `SIGNIN=0`/`1`, uninstall by `/x /quiet` and by the uninstall entry's command, and
Windows' own `Copy-UserInternationalSettingsToSystem` after a plain install. Run with tool 87f647f, Windows 11 x64 (2026-11-26): 20 of 22; both failures were the checker
comparing `YAKBD-TEST\yk3` with `yk3` (fixed). The spec recorded the results as VM-18 to VM-24 and changed the
rules (D97).

`go run ./tools/instbundle -guest w11signin2 -o DIR` writes the second sign-in bundle (D97 and the owner's additions):
the 0438 layout whose language the places lack, failing without a user who has it and getting Windows' copy shape
with enabling on; the display language checked unchanged after install and uninstall; Q14 measured (the installer
and `InstallLayoutOrTip` as LocalSystem, `LCIDToLocaleName` and `ResolveLocaleName` for 0438); VM-20 and VM-23
again. It is autonomous: `drive.ps1` runs on the Hyper-V host, restores the checkpoint for each leg, runs every
phase through `run-phase.ps1` (Register-ScheduledTask with an interactive logon), restarts, signs in the new
accounts, takes 3 screenshots (Hyper-V WMI: GetVirtualSystemThumbnailImage, Msvm_SyntheticMouse, Msvm_Keyboard)
and writes `host-results\summary.txt`. Run with tool 9318191 (2026-11-26): leg A 12 of 13, leg B 8 of 8; the one FAIL was the checker
reading `PreferredUILanguages=en-US` appearing after the uninstall as a display-language change (the effect was
unchanged; D98 Q15: compare the effective language, fixed). The spec recorded VM-25, VM-26 and ruled D98 (the
LocalSystem route).

`go run ./tools/instbundle -guest w11signin3 -o DIR` writes the VM-27 bundle (autonomous like
w11signin2, one screenshot): the installer as SYSTEM for the 0438 layout with no user having the language. Results
pending.

### 8.4 DLL planting check

`go run ./tools/instbundle -guest w11plant -o DIR` writes the check of §5.2 for a w11x64 guest, autonomous like
w11signin2 (`drive.ps1` on the host, `summary.txt`): a 32-bit marker DLL (written with package `pe`; when loaded it
creates `<its path>.loaded`) planted under every DLL name of System32 and SysWOW64 and every DLL name in the
installers' bytes, next to `kbdxx-setup.exe` (a layout installer) and the YAKBD installer (`yakbd-cli-setup.exe`; `yakbd-setup.exe` at the time of run 1) in the administrator's
Downloads folder. Phases `plant-*` of `tools/instcheck` (`plant_windows.go`): a control that loads a probe name by
bare name (its marker must appear), the double-click path (unelevated first process and its elevated copy; the
consent prompt is switched off in the guest so that no one has to answer it), `/quiet` install and `/x /quiet`
uninstall for both installers (a restart before each later install of the same layout), and the same layout installer built by the tool before the fix as evidence that
the check sees the defect.

Run 1 (bundle `tr-planting`, tool 3ecd712, Windows 11 x64, 2026-11-27): 6 of 7. The double-click (install and enable
by the unelevated first process, its elevated copy), `/quiet` and `yakbd-setup.exe /quiet` legs loaded no planted
DLL; the pre-fix installer loaded the planted `wtsapi32.dll` (so the check sees the defect). The FAIL was the
control: the checker copy started inside the planted folder wrote no marker for its bare-name probe, not even for
`wtsapi32.dll`, so it did not reach its loads; the control now loads the probe by full path from the bundle's own
32-bit checker and keeps the in-folder copy as a note (test fix, not rerun).
