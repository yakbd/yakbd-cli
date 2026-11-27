# yakbd

`yakbd` is a command-line tool for keyboard layouts. It converts layouts between `.kbl.json`, MSKLC's `.klc` files
and CLDR Keyboard 3.0 XML, checks them, and builds them into Windows keyboard-layout DLLs and a ready-to-run
installer. It is part of YAKBD ("Yet Another KeyBoard layout Designer", https://github.com/yakbd), together with
the [`.kbl.json` format specification](https://github.com/yakbd/kbl-spec).

- **Your MSKLC layouts keep working.** Microsoft Keyboard Layout Creator (MSKLC) is no longer developed and runs
  only on Windows. yakbd reads its `.klc` files and builds them on Windows, Linux or macOS, including native ARM64
  builds MSKLC cannot make.
- **One installer per layout** for x64, x86 and ARM64 Windows. It upgrades in place, is listed under "Installed
  apps", and takes `msiexec`'s command-line switches and exit codes, so deployment tools can run it.
- **Every loss is reported.** When a format or Windows cannot hold part of a layout, the report says exactly what
  was dropped or changed, and the layout file can say whether that is an error, a warning or fine.
- **Lost the source?** yakbd reads an installed layout DLL back into an editable file.

Windows is the only build target so far; other operating systems may follow.

## Install

From the [releases](https://github.com/yakbd/yakbd-cli/releases) of this repository:

- **Windows**: `yakbd-setup.exe` installs `yakbd.exe` for the PC's architecture into Program Files and puts it on
  the PATH (for an unattended install, `yakbd-setup.exe /quiet /norestart` from an elevated prompt; uninstall
  under "Installed apps"). Or take the zip for your architecture and run `yakbd.exe` from wherever you unzip
  it; it needs no installation. Windows may warn about either: [`docs/windows-security-prompts.md`](docs/windows-security-prompts.md).
- **Linux and macOS**: the `yakbd` binary for your system.
- **Check the download** against the release's `SHA256SUMS` file and its build provenance attestation
  ([`docs/release.md`](docs/release.md)).

With Go 1.26 or later you can also build it yourself:

    go install github.com/yakbd/yakbd-cli/cmd/yakbd@latest

## Quick start: from a `.klc` file to an installed layout

    yakbd convert my-layout.klc my-layout.kbl.json   # the layout as .kbl.json, with a report of every change
    yakbd validate -windows my-layout.kbl.json       # check it and that it builds for Windows
    yakbd installer my-layout.kbl.json               # writes <dll name>-setup.exe

Run the `-setup.exe` on Windows. `yakbd installer my-layout.klc` also works directly on the `.klc` file. If the
same layout is installed from MSKLC, read [`docs/migrating-from-msklc.md`](docs/migrating-from-msklc.md) first: it
shows how to install the new build next to the old one, check it, and only then remove the old one.

## Commands

    yakbd convert in.klc out.kbl.json        # any of .kbl.json, .klc, CLDR .xml, in both directions
    yakbd convert kbdmine.dll out.kbl.json   # read an installed layout DLL back
    yakbd validate layout.kbl.json           # the file; add -windows for the Windows build's report
    yakbd compile layout.kbl.json            # the DLLs for x64, WOW64, x86 and ARM64 in folders of their own
    yakbd installer layout.kbl.json          # one installer with every build
    yakbd pin layout.kbl.json                # write every Windows default choice into the file
    yakbd version

`yakbd <command> -h` lists a command's options. Every command prints a report (errors, warnings, losses, and each
choice taken from a default rule); `-report report.json` saves it as JSON. A command with an error writes nothing.

## Documentation

| page | what it covers |
|---|---|
| [`docs/migrating-from-msklc.md`](docs/migrating-from-msklc.md) | from a `.klc` file to an installed layout; what differs from MSKLC; replacing an installed MSKLC layout safely |
| [`docs/recover-a-layout.md`](docs/recover-a-layout.md) | getting an editable file back from an installed layout DLL |
| [`docs/windows-security-prompts.md`](docs/windows-security-prompts.md) | Smart App Control, SmartScreen and UAC; checking a download; signing your own layout installer |
| [`docs/code-signing-policy.md`](docs/code-signing-policy.md) | **Code signing policy** of `yakbd-setup.exe` (planned) |
| [`docs/installer.md`](docs/installer.md), [`docs/installer-cli.md`](docs/installer-cli.md) | what the installers do, their command line and exit codes |
| [`docs/dll-import.md`](docs/dll-import.md) | how layout DLLs are read |
| [`docs/cldr.md`](docs/cldr.md) | CLDR keyboards: import, Windows builds, the conformance test |
| [`docs/release.md`](docs/release.md) | how releases are built, checked and signed |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | the design and the package map |

## Status

A preview: commands, options and reports may still change. Layouts built by yakbd have been tested in Windows 10
and Windows 11 virtual machines (x64 and x86): they load, type as expected, and install, upgrade and uninstall
cleanly. ARM64 support is tested in emulation only. Every CLDR release 49 keyboard imports and builds.

## Code signing policy

The 0.1 preview ships `yakbd-setup.exe` unsigned; it is to be signed through SignPath Foundation from a later
release. Free code signing provided
by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org). The policy:
[`docs/code-signing-policy.md`](docs/code-signing-policy.md).

## Contributing

Bug reports and pull requests are welcome: the organisation's [`CONTRIBUTING.md`](https://github.com/yakbd/.github/blob/main/CONTRIBUTING.md) says how to report a layout bug,
how to run the tests and the fuzzers, and the clean-room rule every contribution follows.

## Licence

MIT-0 (MIT No Attribution), see [`LICENSE`](LICENSE). Third-party material keeps its own licence and is listed in
[`NOTICE`](NOTICE).
