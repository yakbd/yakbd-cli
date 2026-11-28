# yakbd

`yakbd` is a command-line tool for keyboard layouts. It works with one open format,
[`.kbl.json`](https://github.com/yakbd/kbl-spec) (a superset of Unicode CLDR Keyboard 3.0): it converts layouts
between that format and the layout formats of operating systems and keyboard tools, checks them, and builds native
layouts that install like any other. It is part of YAKBD ("Yet Another KeyBoard layout Designer",
https://github.com/yakbd), whose aim is that a custom or minority-language layout can be made, shared and kept up to
date by anyone, on every platform, without depending on a single vendor's tool.

Windows comes first. Today yakbd converts between `.kbl.json`, MSKLC's `.klc` files and CLDR Keyboard 3.0 XML,
reads installed Windows layout DLLs back, and builds Windows layout DLLs with one ready-to-run installer for every
Windows architecture.

- **Your MSKLC layouts keep working.** Microsoft Keyboard Layout Creator (MSKLC) is no longer developed and runs
  only on Windows. yakbd reads its `.klc` files and builds them on Windows, Linux or macOS, including native ARM64
  builds MSKLC cannot make.
- **One installer per layout** for x64, x86 and ARM64 Windows. It upgrades in place, is listed under "Installed
  apps", and takes `msiexec`'s command-line switches and exit codes, so deployment tools can run it.
- **Every loss is reported.** When a format or Windows cannot hold part of a layout, the report says exactly what
  was dropped or changed, and the layout file can say whether that is an error, a warning or fine.
- **Lost the source?** yakbd reads an installed layout DLL back into an editable file.

macOS is the second target: yakbd converts `.keylayout` files and builds a layout bundle and a zip with install
steps (`-target macos`, [`docs/macos.md`](docs/macos.md)), on any computer and without Apple's tools. Linux is the
third: yakbd converts XKB symbols and Compose files and builds a layout with an install script for your user
(`-target xkb`, [`docs/linux.md`](docs/linux.md)). The formats of other operating systems come next, then
third-party formats such as Keyman. The format keeps the
choices of each target in a section of its own (`targets.*`); `targets.macos` and `targets.xkb` are new in format
version 0.2 ([kbl-spec](https://github.com/yakbd/kbl-spec)), and yakbd writes 0.2 only for a file that uses them.

## Install

From the [latest release](https://github.com/yakbd/yakbd-cli/releases/latest) of this repository:

- **Windows**: [`yakbd-cli-setup.exe`](https://github.com/yakbd/yakbd-cli/releases/latest/download/yakbd-cli-setup.exe)
  installs YAKBD CLI (`yakbd.exe` for the PC's architecture) into `Program Files\YAKBD\CLI` and puts it on the PATH
  (a choice in its wizard; unattended: `yakbd-cli-setup.exe /quiet /norestart` from an elevated prompt; uninstall
  under "Installed apps", entry "YAKBD"). The editor's installer,
  [`yakbd-gui-setup.exe`](https://github.com/yakbd/yakbd-gui/releases/latest/download/yakbd-gui-setup.exe) from
  [yakbd/yakbd-gui](https://github.com/yakbd/yakbd-gui/releases/latest), installs the editor and the CLI together.
  Windows may warn about either: [`docs/windows-security-prompts.md`](docs/windows-security-prompts.md).
- **Linux and macOS**: `yakbd-<version>-linux-amd64.tar.gz`, `-linux-arm64.tar.gz` or `-macos.tar.gz` (one
  universal binary for Intel and Apple silicon Macs); each holds `yakbd`, `LICENSE` and `NOTICE`.
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

For a Mac, build the same file with `-target macos`:

    yakbd validate -target macos my-layout.kbl.json  # check that it builds for macOS
    yakbd installer -target macos my-layout.kbl.json # writes <name>.zip: the layout bundle and a README

Copy the bundle from the zip into `~/Library/Keyboard Layouts`, then add the layout in System Settings ›
Keyboard › Text Input ([`docs/macos.md`](docs/macos.md)).

For Linux (Wayland and X11), build it with `-target xkb`:

    yakbd validate -target xkb my-layout.kbl.json    # check that it builds for XKB
    yakbd installer -target xkb my-layout.kbl.json   # writes <name>.tar.gz: the layout files, install.sh, a README

Unpack it and run `<name>/install.sh` as yourself (no root), then pick the layout in your desktop's keyboard
settings ([`docs/linux.md`](docs/linux.md)).

## Commands

    yakbd convert in.klc out.kbl.json        # any of .kbl.json, .klc, CLDR .xml, .keylayout, in both directions
    yakbd convert -from xkb symbols out.kbl.json   # XKB symbols files have no suffix: -from / -to xkb
    yakbd convert kbdmine.dll out.kbl.json   # read an installed layout DLL back
    yakbd validate layout.kbl.json           # the file; add -windows for the Windows build's report
    yakbd compile layout.kbl.json            # the DLLs for x64, WOW64, x86 and ARM64 in folders of their own
    yakbd installer layout.kbl.json          # one installer with every build
    yakbd pin layout.kbl.json                # write every Windows default choice into the file
    yakbd compile -target macos layout.kbl.json    # the macOS layout bundle
    yakbd installer -target macos layout.kbl.json  # a zip with the bundle and a README
    yakbd compile -target xkb -o out layout.kbl.json   # the XKB symbols, registry and Compose files
    yakbd installer -target xkb layout.kbl.json    # a .tar.gz with the files and an install script
    yakbd version

`compile`, `installer`, `pin` and `validate -target` build for a target: `-target windows` is the default, `-target macos`
and `-target xkb` (Linux) the others ([`docs/targets.md`](docs/targets.md)). `yakbd <command> -h` lists a
command's options. Every command prints a report (errors, warnings, losses, and each
choice taken from a default rule); `-report report.json` saves it as JSON, in the shape of
[`docs/report-file.md`](docs/report-file.md) (for programs: JSON pointers into the input file, the tool version). A
command with an error writes nothing. Flags may come before or after the file names.

## Documentation

| page | what it covers |
|---|---|
| [`docs/migrating-from-msklc.md`](docs/migrating-from-msklc.md) | from a `.klc` file to an installed layout; what differs from MSKLC; replacing an installed MSKLC layout safely |
| [`docs/recover-a-layout.md`](docs/recover-a-layout.md) | getting an editable file back from an installed layout DLL |
| [`docs/macos.md`](docs/macos.md) | macOS: building, installing and selecting a layout; what a `.keylayout` cannot hold |
| [`docs/linux.md`](docs/linux.md) | Linux (XKB): building, installing and selecting a layout on GNOME, KDE, sway and X11; importing symbols files; what XKB cannot hold |
| [`docs/windows-security-prompts.md`](docs/windows-security-prompts.md) | Smart App Control, SmartScreen and UAC; checking a download; signing your own layout installer |
| [`docs/code-signing-policy.md`](docs/code-signing-policy.md) | **Code signing policy** of the YAKBD installers (planned) |
| [`docs/installer.md`](docs/installer.md), [`docs/installer-cli.md`](docs/installer-cli.md) | what the installers do, their command line and exit codes; the YAKBD installers and their wizard (§12) |
| [`docs/report-file.md`](docs/report-file.md) | the `-report` JSON file, for programs that run yakbd (an editor, a build script) |
| [`docs/dll-import.md`](docs/dll-import.md) | how layout DLLs are read |
| [`docs/cldr.md`](docs/cldr.md) | CLDR keyboards: import, Windows builds, the conformance test |
| [`docs/release.md`](docs/release.md) | how releases are built, checked and signed |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | the design and the package map |

## Status

A preview: commands, options and reports may still change. Layouts built by yakbd have been tested in Windows 10
and Windows 11 virtual machines (x64 and x86): they load, type as expected, and install, upgrade and uninstall
cleanly. ARM64 support is tested in emulation only. Every CLDR release 49 keyboard imports and builds. macOS
layout bundles have been installed, selected and typed on GitHub's macOS 15 and 26 runners (Apple silicon), by
script ([`tools/macos-check/README.md`](tools/macos-check/README.md)); the steps in System Settings and Intel Macs
have not been tested. Test layouts installed the way the Linux build installs them have been typed under
libxkbcommon (Debian 13, Ubuntu 26.04, Fedora 45, Arch Linux), X11, weston, sway, KWin and mutter in containers,
by key code ([`tools/xkb-check/README.md`](tools/xkb-check/README.md)); a full GNOME Shell session, IBus and
real keyboards have not been tested.

## Code signing policy

The 0.1 previews ship `yakbd-cli-setup.exe` unsigned; it is to be signed through SignPath Foundation from a later
release. Free code signing provided
by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org). The policy:
[`docs/code-signing-policy.md`](docs/code-signing-policy.md).

## Contributing

Bug reports and pull requests are welcome: the organisation's [`CONTRIBUTING.md`](https://github.com/yakbd/.github/blob/main/CONTRIBUTING.md) says how to report a layout bug,
how to run the tests and the fuzzers, and the clean-room rule every contribution follows.

## Licence

MIT-0 (MIT No Attribution), see [`LICENSE`](LICENSE). Third-party material keeps its own licence and is listed in
[`NOTICE`](NOTICE).
