# Installer command line: msiexec-compatible

Owner decision (2026-11-26): MSI is abandoned, for yakbd itself and for the installers it makes. Every installer
program yakbd makes, the layout installers (`<name>-setup.exe`, docs/installer.md) and the tool's own
`yakbd-setup.exe` (§12), takes **msiexec's command line and gives Windows Installer's exit codes**, so that anything
that drives an `.msi` (a person at a prompt, winget, Intune, Configuration Manager, a script) drives these `.exe`
files the same way: silent install and uninstall, repair, upgrade behaviour, restarts, logging, exit codes.

The program *is* the package: where msiexec takes `/i package.msi`, ours take `/i` alone (or their own path or
product code). Everything here is implemented in `installer/cmdline` (parser, codes, restart and log rules) and
used by `installer/stub` and `installer/toolstub`; the tests in `installer/cmdline` read the tables of §4 and §13 of
this file, so code and contract cannot drift apart.

## 0. Sources

Microsoft's public documentation only (read 2026-11-26); each rule names its source by the short name in brackets.

| short | page |
|---|---|
| [CLO] | Command-Line Options, https://learn.microsoft.com/en-us/windows/win32/msi/command-line-options |
| [STD] | Standard Installer Command-Line Options, https://learn.microsoft.com/en-us/windows/win32/msi/standard-installer-command-line-options |
| [ERR] | MsiExec.exe and InstMsi.exe error messages, https://learn.microsoft.com/en-us/windows/win32/msi/error-codes |
| [SYS] | System error codes, https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes--500-999- , ...--1000-1299- , ...--1300-1699- , ...--1700-3999- |
| [REBOOT] | REBOOT property, https://learn.microsoft.com/en-us/windows/win32/msi/reboot ; System Reboots, https://learn.microsoft.com/en-us/windows/win32/msi/system-reboots |
| [RMC] | MSIRESTARTMANAGERCONTROL, https://learn.microsoft.com/en-us/windows/win32/msi/msirestartmanagercontrol |
| [MUTEX] | _MSIExecute Mutex, https://learn.microsoft.com/en-us/windows/win32/msi/-msiexecute-mutex |
| [LOG] | MsiLogging, https://learn.microsoft.com/en-us/windows/win32/msi/msilogging ; Normal Logging, https://learn.microsoft.com/en-us/windows/win32/msi/normal-logging ; Logging policy, https://learn.microsoft.com/en-us/windows/win32/msi/logging ; Windows Installer Logging, https://learn.microsoft.com/en-us/windows/win32/msi/windows-installer-logging |
| [ALLUSERS] | ALLUSERS, https://learn.microsoft.com/en-us/windows/win32/msi/allusers |
| [REINST] | REINSTALL, https://learn.microsoft.com/en-us/windows/win32/msi/reinstall ; REINSTALLMODE, https://learn.microsoft.com/en-us/windows/win32/msi/reinstallmode ; Applying Small Updates by Reinstalling the Product, https://learn.microsoft.com/en-us/windows/win32/msi/applying-small-updates-by-reinstalling-the-product |
| [UPG] | Major Upgrades, https://learn.microsoft.com/en-us/windows/win32/msi/major-upgrades ; Minor Upgrades, https://learn.microsoft.com/en-us/windows/win32/msi/minor-upgrades ; Small Updates, https://learn.microsoft.com/en-us/windows/win32/msi/small-updates ; Preventing an Old Package from Installing Over a Newer Version, https://learn.microsoft.com/en-us/windows/win32/msi/preventing-an-old-package-from-installing-over-a-newer-version |
| [ARP] | Uninstall Registry Key, https://learn.microsoft.com/en-us/windows/win32/msi/uninstall-registry-key ; ARPNOMODIFY https://learn.microsoft.com/en-us/windows/win32/msi/arpnomodify ; ARPNOREPAIR https://learn.microsoft.com/en-us/windows/win32/msi/arpnorepair ; ARPNOREMOVE https://learn.microsoft.com/en-us/windows/win32/msi/arpnoremove |
| [WINGET] | Create your package manifest, https://learn.microsoft.com/en-us/windows/package-manager/package/manifest ; installer manifest schema 1.12.0, https://github.com/microsoft/winget-pkgs/blob/master/doc/manifest/schema/1.12.0/installer.md ; winget uninstall, https://learn.microsoft.com/en-us/windows/package-manager/winget/uninstall |
| [WINGET-SRC] | public repository microsoft/winget-cli, branch master as of 2026-11-26: `src/AppInstallerCommonCore/Manifest/ManifestCommon.cpp` (default switches and return codes per installer type), `src/AppInstallerCLICore/Workflows/UninstallFlow.cpp`, `.../RepairFlow.cpp`, `src/AppInstallerRepositoryCore/Microsoft/ARPHelper.cpp` (what winget reads from an uninstall entry) |
| [INTUNE] | Add a Win32 app, https://learn.microsoft.com/en-us/intune/app-management/deployment/add-win32 |
| [CM] | Create applications in Configuration Manager, https://learn.microsoft.com/en-us/intune/configmgr/apps/deploy-use/create-applications |
| [SHUT] | InitiateShutdownW, https://learn.microsoft.com/en-us/windows/win32/api/winreg/nf-winreg-initiateshutdownw ; System Shutdown Reason Codes, https://learn.microsoft.com/en-us/windows/win32/shutdown/system-shutdown-reason-codes |
| [ENV] | Environment Variables, https://learn.microsoft.com/en-us/windows/win32/procthread/environment-variables ; WM_SETTINGCHANGE, https://learn.microsoft.com/en-us/windows/win32/winmsg/wm-settingchange ; WOW64 Implementation Details, https://learn.microsoft.com/en-us/windows/win32/winprog64/wow64-implementation-details |

Where the pages say nothing, the rule is marked **our choice** and listed again in §14.

## 1. Syntax

- Options are case-insensitive [CLO] [STD]. They start with `/` or `-` (**our choice**: the pages write `/`; one
  row of [CLO] writes `-m`; deployment tools send `/`).
- Arguments are split by Windows' usual command-line rules (the program is a Go program: `os.Args`). A value with
  spaces is quoted, `PROPERTY="a b"`; `PROPERTY=""` clears a property [CLO].
- `NAME=value` sets a property. The name is read in upper case, the value keeps its case [CLO]. A property may
  stand anywhere except between an option and its argument [CLO].
- At most one action (§2). Repeating the same action is allowed (winget appends its repair switch to `ModifyPath`,
  which already holds `/f`; §9); two different actions are refused with 1639 [CLO: "should not be used together"].
- The operand of `/i`, `/package`, `/x`, `/uninstall` and `/f` is **optional** (msiexec requires it): the program is
  the package, and winget's defaults for MSI-like types send `/quiet /norestart` with no action [WINGET-SRC].
  Attached forms work as in msiexec's own uninstall strings: `/x{GUID}`, `/i{GUID}`, `/fvomus{GUID}`.

### Operand rules (`CheckOperand`)

| operand | result |
|---|---|
| none, or this program's own path | the action runs on this program's product |
| a product code (`{GUID}`) equal to this program's (case-insensitive) | the action runs |
| any other product code | 1605 ERROR_UNKNOWN_PRODUCT (as msiexec for a product it does not know) |
| another file that is a yakbd installer of the same product, with `/x` | the uninstall runs (it acts on the product code) |
| another file with `/i` or `/f`, a file of another product, or a file that is not a yakbd installer | 1619 ERROR_INSTALL_PACKAGE_OPEN_FAILED: "run that file itself" (**our choice**: one program never installs another's payload) |

## 2. Options

"accepted" means the option does what the right column says; "no-op" means accepted with no effect (logged);
"refused" means the program exits with the code given and changes nothing.

| option | msiexec [CLO] [STD] | ours |
|---|---|---|
| *(no action)* | – | install (§6): fresh install, upgrade in place, or repair of the same version |
| `/i [op]`, `/package [op]` | install or configure a product | as *(no action)* |
| `/x [op]`, `/uninstall [op]` | uninstall | uninstall (docs/installer.md §2.4); 1605 when not installed |
| `/f[p\|o\|e\|d\|c\|a\|u\|m\|s\|v] [op]` | repair; letters say which files and registry to rewrite; default `omus` | repair; 1605 when not installed. Every letter is accepted; our repair always does the whole job (files compared and rewritten, KLID and values rewritten, the uninstall entry and the cached installer rewritten, the user's list fixed: in msiexec's letters `amusv`), because the parts cannot be repaired separately without risk. Other letters: 1639 |
| `/a` | administrative install (network image) | refused, 1639: there is no administrative image of these installers |
| `/j[u\|m]` (with `/t`, `/g`) | advertise | refused, 1639: nothing to advertise |
| `/p patch`, `/update patch`, `/uninstall x.msp` | apply or remove a patch | refused, 1635 ERROR_PATCH_PACKAGE_OPEN_FAILED: there are no patches; a new version is a new installer (§6). `/uninstall` with a `.msp` file is an operand that is not a yakbd installer: 1619 |
| `/t transforms` | transforms (with `/j`) | refused, 1624 ERROR_INSTALL_TRANSFORM_FAILURE |
| `/c`, `/g`, `/n {code}`, `/m file`, `/y`, `/z` | instance, language, multiple instances, SMS .mif, DllRegisterServer | refused, 1639 |
| `/?`, `/h`, `/help` | help | the usage dialog, exit 0 |
| `/q`, `/qn` | no UI | no UI (§5) |
| `/qn+` | no UI, a modal dialog at the end | no UI, our end dialog |
| `/qb`, `/qb-`, `/qb!`, `/qb!-`, `/qb-!` | basic UI (progress), `-` no modal dialogs, `!` no Cancel | no question; no end dialog. There is no progress window (a run takes about a second); `!` is a no-op (there is no Cancel button) |
| `/qb+`, `/qb+!`, `/qb!+` | basic UI with a modal dialog at the end | no question; our end dialog |
| `/qr` | reduced UI, no modal dialog at the end | as `/qb` |
| `/qf` | full UI | as the default (full UI) |
| `/qb+-` and other letter mixes | "not a supported UI level" | refused, 1639 |
| `/quiet` | `/qn` | `/qn` |
| `/passive` | `/qb!-` with `REBOOTPROMPT=S` | `/qb!-` with `REBOOTPROMPT=S` |
| `/norestart` | `REBOOT=ReallySuppress` | the same (§5) |
| `/forcerestart` | `REBOOT=Force` | the same (§5) |
| `/promptrestart` | `REBOOTPROMPT=""` (prompt); not with `/quiet` | the same; with `/quiet` or `/qn`: 1639 |
| `/norestart` with `/forcerestart` (or REBOOT values that disagree) | – | refused, 1639 (**our choice**) |
| `/l[i\|w\|e\|a\|r\|u\|c\|m\|o\|p\|v\|x\|+\|!\|*] file` | log; default letters `iwearmo`; `*` = all but `v` `x`; `+` append; `!` flush every line | log to the file (§7): `+` appends, otherwise the file is replaced; the letters are accepted; other letters: 1639 |
| `/log file` | `/l*` | `/l*` |
| `/elevated`, `/result file` | – | internal: the elevated copy the program starts of itself (docs/installer.md §3) |

Switches of ours that msiexec covers were removed without aliases (prototype, no transition period): `/repair`
(now `/f`), `/enable` and `/noenable` (now `ENABLE=1` and `ENABLE=0`), `/force` (now `FORCE=1`), and the aliases
`/s`, `/silent`, `/u`. `/quiet`, `/uninstall`, `/log` and `/q` keep their names: msiexec has them.

## 3. Properties

| property | Windows Installer | ours |
|---|---|---|
| `REBOOT=Force\|Suppress\|ReallySuppress` | only the first character counts [REBOOT] | `F`: force a restart; `S` or `R`: never restart, 3010 instead (Suppress and ReallySuppress do the same here: there is no ForceReboot in the middle of a run). Other values: no effect |
| `REBOOTPROMPT=S` / `""` | suppress the restart prompt / prompt [STD] | the same (§5) |
| `ALLUSERS=1` | per-machine [ALLUSERS] | accepted: these installers are per machine only |
| `ALLUSERS=2` | per-machine or per-user by privileges and `MSIINSTALLPERUSER` | accepted (per machine); with `MSIINSTALLPERUSER=1`: refused, 1639 |
| `ALLUSERS=""` | per-user | refused, 1639: a keyboard layout and the tool are installed per machine |
| `MSIINSTALLPERUSER` | read only with `ALLUSERS=2` | as Windows Installer |
| `ENABLE=0\|1` | – | **ours** (layout installers): add the layout to the input list of the user who runs the installer, overriding `install.enableForUser` of the `.kbl.json`. Other values: 1639. The tool installer ignores it |
| `SIGNIN=0\|1` | – | **ours** (layout installers): also put the layout on the sign-in screen and into the profile new accounts are made from (`1`), or not (`0`), overriding `install.enableForSignInAndNewUsers` of the `.kbl.json` (default off; kbl targets/windows-install.md §7.1, D87; docs/installer.md §2.5). With `/i` on a fresh install it replaces the file's value; on an upgrade or repair (`/i` or `/f`) it replaces the recorded state: `SIGNIN=1` writes the entries and records them, `SIGNIN=0` removes the layout from those places and records that. Without it an upgrade or repair keeps the recorded state. With `/x` it has no effect: uninstall always cleans those places. Not applied (and not a failure) on ARM64 and for a `0C00` layout. A layout whose language the sign-in screen lacks and no signed-in user has: 1603, the layout installed; enable it for a user, then `/f SIGNIN=1` (D97); run as LocalSystem the installer gets the language from Windows itself (D98). Other values: 1639. The tool installer ignores it |
| `FORCE=1` | – | **ours** (layout installers): uninstall even when the layout is the only layout of a user (docs/installer.md §2.4). Other values than 0 and 1: 1639 |
| `REINSTALL=ALL` | reinstall the installed features [REINST] | repair (as `/f`); other values: 1639 (one feature, `ALL`) |
| `REINSTALLMODE=letters` | as `/f`'s letters | letters checked as `/f`'s, logged; with `REINSTALL=ALL`, `msiexec /i x.msi REINSTALL=ALL REINSTALLMODE=vomus` [REINST] works the same as `/fvomus` |
| `REMOVE=ALL` | with `/i`: uninstall | uninstall; other values: 1639 |
| `ADDLOCAL=ALL` | install all features locally | no-op; other values, `ADDSOURCE`, `ADDDEFAULT`, `ADVERTISE`: 1639 |
| `TRANSFORMS=...` | transforms | refused, 1624 (empty value: no-op) |
| `PATCH=...`, `MSIPATCHREMOVE=...` | patches | refused, 1635 (empty value: no-op) |
| `TARGETDIR`, `INSTALLDIR`, `INSTALLLOCATION`, `APPLICATIONFOLDER` | install location (winget's default InstallLocation switch for MSI types is `TARGETDIR="<INSTALLPATH>"` [WINGET-SRC]) | refused, 1639: the layout files go into the system folders, the tool into `%ProgramFiles%\YAKBD` |
| `MSIRESTARTMANAGERCONTROL=0\|Disable\|DisableShutdown` | whether Restart Manager is used for files in use [RMC] | no-op: these installers never close applications; a file in use is replaced or deleted at the next restart (§5) |
| `MSIDISABLERMRESTART`, `MSIRMSHUTDOWN` | Restart Manager behaviour [REBOOT] | no-op, as above |
| any other `NAME=value` | sets a public property the package may read | accepted, logged as "accepted without effect" (as Windows Installer sets a property no table reads) |

[CLO] says `/f` ignores properties on the command line. Ours honour `REBOOT`, `REBOOTPROMPT`, `ENABLE`, `SIGNIN` and `FORCE`
with `/f` too (**our choice**: deployment tools add `/norestart` to a repair and expect it to count).

## 4. Exit codes

The program returns Windows Installer's codes [ERR], [SYS]; 0, 1641 and 3010 are success [ERR]. (Table read by the
tests: every code in `installer/cmdline` has a row here with its name.)

| code | name | msiexec meaning | ours: when |
|---|---|---|---|
| 0 | ERROR_SUCCESS | done | done; also an install whose enabling for the user was skipped by design (another account, LocalSystem: docs/installer.md §3) |
| 740 | ERROR_ELEVATION_REQUIRED | the requested operation requires elevation [SYS] | not elevated, and the UI level allows no elevation prompt (`/quiet`, `/qn`); **our choice**: msiexec elevates through its service and has no such case |
| 1602 | ERROR_INSTALL_USEREXIT | user cancelled | the user said No to our question or declined the elevation prompt (ShellExecuteEx reports 1223 ERROR_CANCELLED for the prompt [SYS]; the program returns 1602, as msiexec would) |
| 1603 | ERROR_INSTALL_FAILURE | fatal error during installation | failed and rolled back, or refused with nothing changed: foreign file, KLID or Layout Id taken, file waiting for deletion at restart, uninstall refused because the layout is a user's only layout or the only layout of the sign-in screen (`FORCE=1` overrides); and an install whose enabling for the user failed (the machine part stays installed; run the installer again, or add the layout in Settings), or whose sign-in screen step (`SIGNIN`, docs/installer.md §2.5) failed (the machine part and the user's enabling stay; nothing is left on the sign-in screen or in the Default profile) |
| 1605 | ERROR_UNKNOWN_PRODUCT | action only valid for installed products | `/x` or `/f` when the product is not installed; a product code that is not this program's |
| 1618 | ERROR_INSTALL_ALREADY_RUNNING | another installation is in progress | another yakbd installer (layout or tool) is changing the machine (§8) |
| 1619 | ERROR_INSTALL_PACKAGE_OPEN_FAILED | package could not be opened | the operand names a file that is not this program's package (§1) |
| 1620 | ERROR_INSTALL_PACKAGE_INVALID | not a valid installation package | this installer is damaged: its payload does not read, or a file fails its Machine and shape check (docs/installer.md §2.1 step 1) |
| 1622 | ERROR_INSTALL_LOG_FAILURE | log file could not be opened | the `/l` file cannot be opened (its folder must exist [CLO]) |
| 1624 | ERROR_INSTALL_TRANSFORM_FAILURE | transform could not be applied | `/t`, `TRANSFORMS=` |
| 1633 | ERROR_INSTALL_PLATFORM_UNSUPPORTED | package not supported on this platform | the installer carries no build for this machine (docs/installer.md §1) |
| 1635 | ERROR_PATCH_PACKAGE_OPEN_FAILED | patch package could not be opened | `/p`, `/update`, `PATCH=`, `MSIPATCHREMOVE=` |
| 1638 | ERROR_PRODUCT_VERSION | another version of this product is already installed | a newer version of the product is installed (§6) |
| 1639 | ERROR_INVALID_COMMAND_LINE | invalid command-line argument | anything §1–§3 refuses with 1639 |
| 1641 | ERROR_SUCCESS_REBOOT_INITIATED | the installer started a restart | done, and the program started a restart (§5) |
| 3010 | ERROR_SUCCESS_REBOOT_REQUIRED | a restart is required to finish | done; a file waits for the next restart, and no restart was started (`/norestart`, the user said No, or the restart failed) |

Not returned: 1601, 1604, 1606–1617, 1621, 1623, 1625–1632, 1634, 1636, 1637, 1640, 1642–1654, 3011: they concern
the Windows Installer service, its database, features, sources, patches and policies, which these programs do not
have. 1223 ERROR_CANCELLED is never returned (1602 is). 1707 (counted as success by [CM]'s defaults) is
RPC_S_INVALID_NET_ADDR in [SYS] and is never returned.

### Old codes → new (2026-11-26)

| old | meaning | new |
|---|---|---|
| 0 | done | 0 |
| 1 | failed, rolled back | 1603 (1620 for a damaged installer) |
| 2 | bad command line | 1639 (1624, 1635 for the option kinds above) |
| 3 | no build for this machine | 1633 |
| 4 | `/quiet` without administrator rights | 740 |
| 5 | uninstall refused: the only layout of a user | 1603 |
| 6 | installed, not enabled | 1603 when enabling failed; 0 when it was skipped because the program runs as another account than the session's user (a deployment as LocalSystem is a successful per-machine install) |
| 1223 | cancelled | 1602 |
| 3010 | restart to finish | 3010, or 1641 when the program started the restart |
| – | uninstall or repair of a product that is not installed (was 0 with a note) | 1605 |
| – | older version over a newer one (was an upgrade in place) | 1638 |

## 5. User interface and restarts

| level | question first | end dialog | may show the elevation prompt |
|---|---|---|---|
| full (default, `/qf`) | yes | yes | yes |
| reduced (`/qr`) | no | no | yes |
| basic (`/qb`, `/qb-`, `/qb!`, `/passive`) | no | no | yes |
| basic with `+` (`/qb+`) | no | yes | yes |
| none (`/q`, `/qn`, `/quiet`) | no | no | no: not elevated gives 740 |
| none with `+` (`/qn+`) | no | yes | no |

**Restarts.** A run needs a restart when a file was in use: an upgrade writes the new file for the next restart,
an uninstall deletes it then (docs/installer.md §2.2, §2.4). At the end of a successful run the program decides as
Windows Installer does with `REBOOT` and `REBOOTPROMPT` [REBOOT] [STD]:

| `REBOOT` | restart needed | UI none, or `REBOOTPROMPT=S` (`/passive`) | other UI levels |
|---|---|---|---|
| `ReallySuppress`/`Suppress` (`/norestart`) | yes | no restart, 3010 | no restart, 3010 |
| not set | yes | restart without asking, 1641 | ask "Restart now?": Yes 1641, No 3010 |
| not set | no | nothing, 0 | nothing, 0 |
| `Force` (`/forcerestart`) | either | restart without asking, 1641 | ask: Yes 1641, No 3010 |

This matches msiexec, including its sharpest edge: `/quiet` without `/norestart` restarts the computer by itself
when a file was in use ([STD]: "/quiet ... restarts automatically when necessary"). Deployment tools pass
`/norestart` (winget's defaults do [WINGET-SRC]) and read 3010; the uninstall entry's `QuietUninstallString` has it
(§9). The restart is `InitiateShutdownW(NULL, message, 0, SHUTDOWN_RESTART, SHTDN_REASON_MAJOR_APPLICATION |
SHTDN_REASON_MINOR_INSTALLATION | SHTDN_REASON_FLAG_PLANNED)` after enabling `SeShutdownPrivilege` [SHUT]; without
`SHUTDOWN_FORCE_OTHERS` it fails while other users are signed in, and the program then returns 3010 (**our choice**:
never sign other people out). The log line "restart: initiating a restart (exit code 1641)" is written first;
Windows may restart before the caller sees the code, as with msiexec [REBOOT: restarts are asynchronous].

The elevated copy (docs/installer.md §3) never restarts: the first process decides.

## 6. Install, upgrade, repair, downgrade

A product's code is stable: for a layout it is derived from `metadata.id` and the DLL name (docs/installer.md §7,
D61); for the tool it is fixed (§12). A rebuilt or newer installer of the same layout has the same product code. So
every installer of a product relates to an installed copy as a **minor upgrade** does in Windows Installer (the
product code stays, the version changes [UPG]); there are no major upgrades (no UpgradeCode), patches or small
updates.

Versions are compared by their first three fields, as Windows Installer compares ProductVersion for upgrades [UPG]
(`2.0.0.5` equals `2.0.0.0`). A field counts by its leading digits, so a semantic-version pre-release suffix is
ignored: `0.1.0-preview.1`, `0.1.0-preview.2` and `0.1.0` are the same version, and installing one over another is a
repair in place, never 1638 (**our choice**, simple and the same as Windows Installer for the numbers). The installed
version is the uninstall entry's `DisplayVersion`, written as given (`0.1.0-preview.1`); the version resource holds
four numbers, the leading numbers of the version (`0.1.0.0`), and its `FileVersion` and `ProductVersion` strings the
version as given (docs/installer.md §5.1).

| installed | this installer | `/i` (or no action) | `/f` | `/x` |
|---|---|---|---|---|
| nothing | any | fresh install | 1605 | 1605 |
| older | newer | upgrade in place (same KLID and Layout Id, files replaced, values and entry rewritten) | the same as `/i` | uninstall |
| same | same | repair (the same steps) | repair | uninstall |
| newer | older | refused, **1638**, nothing changed: uninstall first | refused, 1638 | uninstall |

The downgrade refusal follows [UPG] "Preventing an Old Package from Installing Over a Newer Version" (which, for
Windows Installer, only works for major upgrades; ours applies it to every run, **our choice**) and the meaning of
1638 [ERR]. A different product code is a different product: two products never share a layout file (the second is
refused as a foreign file, 1603).

## 7. Logging

- `/l... file` or `/log file`: the program writes its log to that file; without `+` the file is replaced, with `+`
  appended [CLO]. A file that cannot be opened (for example a folder that does not exist: msiexec does not create
  folders either [CLO]) ends the run with 1622 before anything changes.
- The log has one level: one line per step, with time stamps, always as much as `/l*v` would ask for. The letters
  `iwearucmopvx*` are therefore accepted without changing the content; `!` (flush every line) is what the program
  does anyway: each line is written straight to the file.
- **Default (the MsiLogging analogue).** Without `/l`, the program logs anyway, appending to
  `%TEMP%\yakbd-<dll name>-setup.log` (layout) or `%TEMP%\yakbd-setup.log` (tool). Windows Installer logs by default
  only when a package sets `MsiLogging` (then to a random `MSI*.LOG` in `%TEMP%`) or the `Logging` machine policy is
  set [LOG]; ours behave like a package with `MsiLogging` set, under a fixed name, so that a support person finds
  the file. The `Logging` policy is not read (it is Windows Installer's).
- The elevated copy appends to the same file (docs/installer.md §3).

## 8. Concurrency

Windows Installer refuses a second installation while one is executing, with 1618, and marks the executing phase
with the `_MSIExecute` mutex [MUTEX]. Ours hold a machine-wide mutex `Global\YAKBD-Setup` while they change the
machine (the elevated part: files, registry, uninstall entry), in every layout installer and in `yakbd-setup.exe`. A
second yakbd installer that finds it held exits with 1618 and changes nothing; [CM] retries such a code ("Fast
Retry"), [INTUNE] has a "Retry" code type, winget maps 1618 to `installInProgress` for MSI types [WINGET-SRC].
**Our choice**: `_MSIExecute` itself is neither taken nor checked. [MUTEX] gives neither its namespace nor a way to
take it, and its only test for a running installation is a check of the Windows Installer service's state. These
programs do not run in that service, so they do not conflict with an MSI installation and do not wait for one.

## 9. Uninstall entry

`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\<product code>` (docs/installer.md §2.3) [ARP]:

| value | content | read by |
|---|---|---|
| `UninstallString` | `"<InstallLocation>\setup.exe" /x <product code>` | Settings, Control Panel; winget when there is no quiet string or `--interactive` [WINGET-SRC] |
| `QuietUninstallString` | `"<InstallLocation>\setup.exe" /x <product code> /quiet /norestart` | winget's default for exe types [WINGET-SRC]; deployment scripts |
| `ModifyPath` | `"<InstallLocation>\setup.exe" /f <product code>` | Settings' Modify button (full UI: "Repair ...?"); winget's `RepairBehavior: modify`, which appends the manifest's `Repair` switch [WINGET-SRC] |
| `DisplayIcon` | `<InstallLocation>\setup.exe` (its icon, docs/installer.md §5.1) | Settings |
| `DisplayName`, `DisplayVersion`, `Publisher`, `InstallLocation`, `EstimatedSize` | as docs/installer.md §2.3 | winget identifies the installed package by these [WINGET-SRC] |
| `NoModify`, `NoRepair`, `NoRemove` | **not written** | winget refuses a repair when `NoModify` or `NoRepair` is set [WINGET-SRC]; repair is supported, removal too. ([ARP] ARPNOMODIFY and ARPNOREMOVE: Windows Installer writes these values only when the package asks for it) |
| `WindowsInstaller` | **not written** | winget treats an entry with it as an MSI product and runs msiexec on its product code [WINGET-SRC]; msiexec does not know ours, so it must not be there |

The key name is a GUID like an MSI product code; tools that look products up through the Windows Installer API (an
Intune "MSI" detection rule) do not find it. Detect with a registry rule on this key and `DisplayVersion` [INTUNE].

## 10. What deployment tools send

**winget** [WINGET] [WINGET-SRC]. For `InstallerType: msi`, `wix` and `burn`, winget's default switches are
`/quiet /norestart` (silent), `/passive /norestart` (silent with progress, the default experience) and `/log
"<LOGPATH>"`; ours accept them unchanged. Its default return codes for these types map 1618 to `installInProgress`,
3010 to `rebootRequiredToFinish`, 1641 to `rebootInitiated`, 1602 to `cancelledByUser`, 1638 to
`alreadyInstalled`, 1639 and 87 to `invalidParameter`, 1633 to `systemNotSupported`. For `InstallerType: exe`
winget has no defaults: a manifest must say them. A manifest for a yakbd installer (tested in the VM bundle as a
literal command line, §13):

```yaml
InstallerType: exe
Scope: machine
ElevationRequirement: elevationRequired   # winget elevates; /quiet unelevated would be 740
InstallModes: [interactive, silent, silentWithProgress]
InstallerSwitches:
  Silent: /quiet /norestart
  SilentWithProgress: /passive /norestart
  Log: /log "<LOGPATH>"
  Repair: /quiet /norestart
UpgradeBehavior: install                  # same product code: upgrade in place (§6)
RepairBehavior: modify                    # ModifyPath + Repair: setup.exe /f {code} /quiet /norestart
InstallerSuccessCodes: [3010, 1641]
ExpectedReturnCodes:
  - {InstallerReturnCode: 1602, ReturnResponse: cancelledByUser}
  - {InstallerReturnCode: 1618, ReturnResponse: installInProgress}
  - {InstallerReturnCode: 1633, ReturnResponse: systemNotSupported}
  - {InstallerReturnCode: 1638, ReturnResponse: downgrade}
  - {InstallerReturnCode: 1639, ReturnResponse: invalidParameter}
ProductCode: '{...}'                      # the uninstall key's name
AppsAndFeaturesEntries:
  - {ProductCode: '{...}', DisplayName: ..., Publisher: ..., InstallerType: exe}
```

winget uninstalls an exe-type package with the entry's `QuietUninstallString` (with `UninstallString` as the
fallback and for `--interactive`) [WINGET-SRC].

**Intune Win32 apps** [INTUNE]: install command `setup.exe /quiet /norestart`, uninstall command `"%ProgramFiles%..."`
is not expanded by Intune, so use `setup.exe /x /quiet /norestart` from the package (the program's own path is
accepted as package) or the product code form `setup.exe /x {code} /quiet /norestart`. Install behaviour: System (the
layout is installed per machine; enabling for a user is skipped, exit 0, §4). Return codes: the page names the types
Success, Soft reboot, Hard reboot, Retry and Failed, and lists no numbers; **our choice** for an Intune app entry:
3010 Soft reboot, 1641 Hard reboot, 1618 Retry (the numbers [CM] uses for the same types).

**Configuration Manager** [CM]: the Script Installer deployment type's defaults are 0 success, 3010 soft reboot,
1641 hard reboot, 1618 fast retry, which are exactly the success-and-retry codes ours return. Installation program
`setup.exe /quiet /norestart`, uninstall program `setup.exe /x /quiet /norestart`, repair program `setup.exe /f
/quiet /norestart`. "Run installation and uninstall program as 32-bit process on 64-bit clients" changes nothing:
the program is 32-bit anyway and reaches the 64-bit system folder and registry itself (docs/installer.md §2).

## 11. Layout installers: what changed

`installer/stub` parses with `installer/cmdline`; its steps are those of docs/installer.md §2. The `/repair` path
is now `/f`; `/enable`/`/noenable` are `ENABLE=1`/`ENABLE=0`; `/force` is `FORCE=1`. A later change adds `SIGNIN=0|1` (§3; docs/installer.md §2.5). A deployment as LocalSystem
returns 0, not the old 6. Repair and uninstall of a product that is not installed return 1605 (a repair used to
install it fresh). An older installer over a newer version returns 1638 (it used to downgrade).

## 12. The tool's own installer, `yakbd-setup.exe`

One 32-bit program (the same reason as docs/installer.md §2: it starts on x86, x64 and ARM64 Windows) carrying the
x86, x64 and arm64 builds of `yakbd.exe`. It takes the same command line and gives the same codes (`ENABLE`,
`SIGNIN` and `FORCE` are accepted and ignored).

- **Files**: `yakbd.exe` of the machine's architecture (x86, x64 or arm64 by IsWow64Process2, inst §2.2; no
  fallback: a machine without its build gets 1633) into `%ProgramW6432%\YAKBD\yakbd\` (the native Program Files:
  a 32-bit process sees `ProgramFiles` as the x86 folder and `ProgramW6432` as the native one [ENV] WOW64
  Implementation Details; on 32-bit Windows `ProgramFiles`), and a copy of the installer as `setup.exe` next to it.
- **PATH**: the folder is appended to the machine `Path`
  (`HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, the value's type kept, REG_EXPAND_SZ when
  new), and `WM_SETTINGCHANGE` with `"Environment"` is broadcast with `SendMessageTimeout` [ENV]. When the folder is
  already in `Path` nothing is added and the record says so; uninstall removes only the entry the installer added.
- **Uninstall entry** `...\Uninstall\{product code}` as §9, product code fixed:
  `{EBE1EAEC-C574-5473-8489-B3FF22FC6506}` (UUID version 5 of the namespace of docs/installer.md §7 and the
  name `yakbd tool installer`: `installer.ToolProductCode`); record values `YakbdToolFile`, `YakbdPathAdded`,
  `YakbdToolBuild` and, with an extra payload, `YakbdToolExtraFiles` (§12.1).
- **Upgrade, repair, downgrade** as §6; `yakbd.exe` in use (running) is replaced or deleted at the next restart,
  3010, and the restart rules of §5 apply.
- **Built** by `go run ./tools/toolsetup -o yakbd-setup.exe` for tests and by the release workflow for releases (reproducible: fixed flags and environment, as the
  layout stub; docs/release.md). At a release tag, with the tool's own version and no other option, `tools/toolsetup`
  writes that release's `yakbd-setup.exe` byte for byte.

### 12.1 Extra payload: more files next to `yakbd.exe`

A `yakbd-setup.exe` can carry an **extra payload**: files installed into the same folder as `yakbd.exe`, for example
the graphical editor that ships with the tool. It is the same product (same product code, name and uninstall entry),
so an installer with the extra payload and one without it replace each other in place, in both directions, by the
rules of §6. Nothing else changes: command line, exit codes, PATH and uninstall entry are those of §12.

- **Build it** from a checkout of this repository at the release tag of the CLI that is to ship:

      go run ./tools/toolsetup -o yakbd-setup.exe -builds x64,arm64 -extra x64=DIR-x64 -extra arm64=DIR-arm64

  `-extra DIR` adds every regular file below `DIR` (in name order, links refused) for every machine;
  `-extra BUILD=DIR` for the machines of one build only (the same path may appear once per build). `-builds` limits
  the builds of `yakbd.exe`; a machine whose build is missing gets 1633 and nothing changes, as in §12 (a payload
  that exists only for x64 and arm64 is built with `-builds x64,arm64`). The command prints the SHA-256 of each
  `yakbd.exe` it carries: with the tag's own version they equal the `yakbd.exe` in that release's
  `yakbd-V-windows-*.zip` (whose sums are in `SHA256SUMS`), which is how a consumer shows the bundled CLI is the
  released one. In Go, the same is `relbuild.SetupWith` with `SetupOptions{Builds, Extra}` (an internal package: it
  is used from this repository's checkout, not imported).
- **Product version**: the installer's version is the CLI's (`-version`, default the tool's own), because §6 compares
  it with the installed one. A bundle of the same CLI version as the installed copy is a repair in place, which
  writes every file that differs (so a newer editor with the same CLI goes over an older one, and an older one over a
  newer one too).
- **Paths** (`toolsetup.CheckExtraPath`, checked when the payload is written and when it is read): relative, with
  backslashes; every part a plain name (not empty, `.` or `..`, none of `<>:"/|?*` or a control character, not ending
  in a space or a dot); not `yakbd.exe` or `setup.exe` as the file or its top folder; not ending in `.yakbd-tmp` or
  `.yakbd-new`. Per machine, no path twice (letter case ignored) and no file where another path needs a folder.
- **Install**: after `yakbd.exe`, each file of the machine's build is written like `yakbd.exe` (beside it, renamed
  over it, read back; an identical file is left alone; a file in use is replaced at the next restart, 3010), its
  folders created as needed. The uninstall entry gets the record value `YakbdToolExtraFiles`: the installed paths
  joined by `|`, written when there are extra files now or were before (an installer without the extra payload
  writes no such value over a CLI-only install, so that its entry stays as before). Files of the earlier record that
  this installer does not carry are deleted after the entry is written, with their folders when empty: installing
  the CLI alone over a bundle removes the bundle's files. A fresh install that fails is rolled back, extra files and
  folders included. `EstimatedSize` counts the extra files.
- **Uninstall** deletes the recorded files (a file in use at the next restart, 3010), then their folders deepest first
  when empty (a folder that still holds a waiting file at the restart, after it), then the rest as in §12. Any
  `yakbd-setup.exe` of this version or later uninstalls a bundle, with or without the extra payload itself; one of
  0.1.0-preview.1 knows no record and leaves the extra files.
- **Refused like §12**: a pending deletion (an earlier uninstall waiting for the restart) of the folder or anything in
  it stops the install with 1603, nothing changed.
- **Size**: the installer reads itself once into memory and copies itself as `setup.exe` into the folder, so a
  bundle takes its own size once more in the folder and in memory while it runs.
- **No Start-menu shortcut**: the reference specification has no facts on shortcut files or the Start menu folders,
  and this repository takes no Windows fact from elsewhere without a source; the extra payload installs files only.
  A consumer that wants a shortcut has to wait for that fact (an open question).
- **Not yet run on Windows**: the extra payload, and the `yakbd-setup.exe` of 0.1.0-preview.2 with it, are tested
  only against the in-memory Windows of `installer/toolsetup` (`extra_test.go`: every machine, CLI-only and bundled
  in both directions, older/newer refusals, changed file sets, files in use, rollback) and by `FuzzDecode`. The
  Windows run of §12 (24 of 24) predates them.

Windows test: `go run ./tools/clibundle -o DIR` writes the bundle (a layout installer and `yakbd-setup.exe`, three
versions each, and `tools/clicheck`) that runs every switch combination of this contract a deployment tool sends, in
a disposable w11x64 guest; scenarios in its README.

**Verified on Windows** (2026-11-26, tool commit 3db00a5, Windows 11 x64): 24 of 24. Layout
installer: 1639, 1635, 1624, 1605, 1622, a quiet install with the entry values of §9, `/l*v` replaced and `/l*v+`
appended, 1618 with the mutex held, 1638 for the older version, 3010 for an upgrade with the layout file held open,
repairs through `ModifyPath` + `/quiet /norestart`, `REINSTALL=ALL` and `/fvomus`, uninstall through
`QuietUninstallString`, `/uninstall <other file of the product>`, `/X{code}`, 1619 for a foreign file, winget's
switches, `ENABLE=1`/`ENABLE=0`. `yakbd-setup.exe`: the x64 build into Program Files, `yakbd version` through the
machine Path in a new `cmd.exe`, 1638, 1618, repair, 3010 with `yakbd.exe` held, uninstall removing the Path entry,
`/x {code}`, 1605, 1639 for `INSTALLDIR=`. `/quiet` without `/norestart` with a file in use restarted Windows by
itself (log line before `InitiateShutdownW`, exit 1641), and the delayed replacement (through System32, fdfa2cc) had
landed after the restart. Run 1 (92877ac) had 23 of 24: `/bogus` without `/q` waited at the usage dialog (the
checker now sends `/quiet`), and the delayed replacement was registered through Sysnative (fixed by fdfa2cc).
Not run in the VM: 740 (the checker runs elevated), the full-UI dialogs, `/forcerestart`.

## 13. Examples (tested)

`installer/cmdline`'s test parses every row: the command line (split like Windows' argv, `"` quoting) and the
expected parse, or `exit <code>`. Result words: the action, then `op=`, `ui=`, `end`, `reboot=`, `prompt=`,
`log=`, `flags=`, `append`, `mode=`, `enable=`, `force` for what differs from the default.

| command line | result |
|---|---|
| `` | install |
| `/quiet /norestart` | install ui=none reboot=suppress |
| `/passive /norestart` | install ui=basic reboot=suppress prompt=suppress |
| `/log "C:\a b\x.log"` | install log=C:\a b\x.log flags=* |
| `/i /qn /l*v x.log` | install ui=none log=x.log flags=*v |
| `-I -QN -L*V+ x.log` | install ui=none log=x.log flags=*v append |
| `/l*vx! x.log` | install log=x.log flags=*vx |
| `/l x.log` | install log=x.log |
| `/package {01234567-89AB-CDEF-0123-456789ABCDEF} /quiet` | install op={01234567-89AB-CDEF-0123-456789ABCDEF} ui=none |
| `/x` | uninstall |
| `/x {01234567-89AB-CDEF-0123-456789ABCDEF} /quiet /norestart` | uninstall op={01234567-89AB-CDEF-0123-456789ABCDEF} ui=none reboot=suppress |
| `/X{01234567-89AB-CDEF-0123-456789ABCDEF}` | uninstall op={01234567-89AB-CDEF-0123-456789ABCDEF} |
| `/uninstall C:\x\kbdtst-setup.exe /passive` | uninstall op=C:\x\kbdtst-setup.exe ui=basic prompt=suppress |
| `/uninstall FORCE=1 /quiet` | uninstall ui=none force |
| `/f` | repair |
| `/fvomus {01234567-89AB-CDEF-0123-456789ABCDEF}` | repair op={01234567-89AB-CDEF-0123-456789ABCDEF} mode=vomus |
| `/famus{01234567-89AB-CDEF-0123-456789ABCDEF} /qb` | repair op={01234567-89AB-CDEF-0123-456789ABCDEF} ui=basic mode=amus |
| `/f {01234567-89AB-CDEF-0123-456789ABCDEF} /f /quiet /norestart` | repair op={01234567-89AB-CDEF-0123-456789ABCDEF} ui=none reboot=suppress |
| `/i REINSTALL=ALL REINSTALLMODE=vomus` | repair mode=vomus |
| `REMOVE=ALL /qn` | uninstall ui=none |
| `/qn+` | install ui=none end |
| `/qb+!` | install ui=basic end |
| `/qb!-` | install ui=basic |
| `/qr` | install ui=reduced |
| `/qf` | install |
| `/q` | install ui=none |
| `/forcerestart /qn` | install ui=none reboot=force |
| `/promptrestart` | install prompt=always |
| `REBOOT=ReallySuppress` | install reboot=suppress |
| `REBOOT=s` | install reboot=suppress |
| `REBOOT=Force` | install reboot=force |
| `REBOOT=Maybe` | install |
| `REBOOTPROMPT=S` | install prompt=suppress |
| `ENABLE=0 /quiet` | install ui=none enable=0 |
| `enable=1` | install enable=1 |
| `SIGNIN=1 /quiet /norestart` | install ui=none reboot=suppress signin=1 |
| `/f SIGNIN=0 /quiet` | repair ui=none signin=0 |
| `/x signin=1` | uninstall signin=1 |
| `ALLUSERS=1 MSIRESTARTMANAGERCONTROL=Disable MSIFASTINSTALL=7` | install |
| `ALLUSERS=2` | install |
| `ADDLOCAL=ALL` | install |
| `/?` | help |
| `/help` | help |
| `/quiet /qf` | install |
| `/x /i` | exit 1639 |
| `/f /x` | exit 1639 |
| `/x REINSTALL=ALL` | exit 1639 |
| `/fz` | exit 1639 |
| `/qb+-` | exit 1639 |
| `/qn-` | exit 1639 |
| `/qr+` | exit 1639 |
| `/qx` | exit 1639 |
| `/l*q x.log` | exit 1639 |
| `/log` | exit 1639 |
| `/l*v` | exit 1639 |
| `/norestart /forcerestart` | exit 1639 |
| `/norestart REBOOT=Force` | exit 1639 |
| `/quiet /promptrestart` | exit 1639 |
| `/a` | exit 1639 |
| `/ju` | exit 1639 |
| `/n {01234567-89AB-CDEF-0123-456789ABCDEF}` | exit 1639 |
| `/y x.dll` | exit 1639 |
| `/m mif` | exit 1639 |
| `/p patch.msp` | exit 1635 |
| `/update patch.msp` | exit 1635 |
| `PATCH=x.msp` | exit 1635 |
| `MSIPATCHREMOVE={01234567-89AB-CDEF-0123-456789ABCDEF}` | exit 1635 |
| `/t x.mst` | exit 1624 |
| `TRANSFORMS=x.mst` | exit 1624 |
| `ALLUSERS=""` | exit 1639 |
| `ALLUSERS=2 MSIINSTALLPERUSER=1` | exit 1639 |
| `ALLUSERS=3` | exit 1639 |
| `ENABLE=yes` | exit 1639 |
| `SIGNIN=2` | exit 1639 |
| `REINSTALL=Feature1` | exit 1639 |
| `REINSTALLMODE=vomusz` | exit 1639 |
| `REMOVE=Feature1` | exit 1639 |
| `TARGETDIR=C:\x` | exit 1639 |
| `INSTALLDIR=C:\x` | exit 1639 |
| `/install` | exit 1639 |
| `/silent` | exit 1639 |
| `/s` | exit 1639 |
| `/repair` | exit 1639 |
| `/enable` | exit 1639 |
| `/force` | exit 1639 |
| `stray` | exit 1639 |
| `/x a.exe b.exe` | exit 1639 |

## 14. Our choices (not stated by the sources)

1. `-` accepted like `/` (§1).
2. Operands optional; operand rules (§1).
3. `/f` letters: one full repair for every combination (§2); properties honoured with `/f` (§3).
4. Unknown properties accepted and logged; the install-location properties refused (§3).
5. 740 for "no UI, not elevated" (§4); 0 for an enabling skipped by design, 1603 for an enabling that failed.
6. 1602 for a declined elevation prompt (§4).
7. Conflicting restart options refused (§2); no `SHUTDOWN_FORCE_OTHERS` (§5).
8. Versions compared by three fields and a downgrade refused on every run (§6).
9. One log level; default log under a fixed name (§7).
10. Our own mutex, `_MSIExecute` not used (§8).
11. `NoModify`/`NoRepair`/`WindowsInstaller` not written; `DisplayIcon` written (§9).
