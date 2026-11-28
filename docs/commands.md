# Commands

`yakbd` has six commands. Each reads one layout file and prints a report: errors, warnings, losses (what a format
or platform could not hold), and every choice taken from a default rule. `yakbd <command> -h` lists a command's
flags; `yakbd <command> -target T -h` adds that target's own flags. Flags may come before or after the file names.

| command | what it does |
|---|---|
| [`convert`](#convert) | converts a layout between `.kbl.json`, `.klc`, CLDR `.xml`, macOS `.keylayout` and XKB symbols, and reads a Windows layout DLL back |
| [`validate`](#validate) | checks a layout file and, with `-target`, that it builds for a platform |
| [`compile`](#compile) | builds the platform files of a layout (Windows DLLs, a macOS bundle, XKB files) |
| [`installer`](#installer) | builds one ready-to-install package (Windows `-setup.exe`, macOS `.zip`, Linux `.tar.gz`) |
| [`pin`](#pin) | writes every default choice of a target into the `.kbl.json` file |
| `version` | prints `yakbd` and its version |

## Targets

`compile`, `installer`, `pin` and `validate -target` build for a platform, chosen with `-target`:

| `-target` | builds | guide |
|---|---|---|
| `windows` (the default) | layout DLLs for x64, WOW64, x86 and ARM64; one `-setup.exe` | [Moving from MSKLC](migrating-from-msklc.md) |
| `macos` | a `.keylayout` in a `.bundle`; a `.zip` with the bundle and install steps | [macOS](macos.md) |
| `xkb` | XKB symbols, registry and Compose files for Wayland and X11; a `.tar.gz` with `install.sh` | [Linux](linux.md) |

How a target plugs into the command line: [targets.md](targets.md).

## Flags every command shares

| flag | meaning |
|---|---|
| `-report FILE` | also write the report as JSON ([report-file.md](report-file.md)) |
| `-q` | print only errors and warnings |
| `-strict-defaults` | make every choice taken from a default rule an error (`windowsDefault.*`, `macosDefault.*`, `xkbDefault.*`) |
| `-accept KINDS` | `compile`, `installer`: comma-separated loss kinds (or `prefix.*`) to accept for this run |

Exit codes: `0` success; `1` the input has errors or the operation failed: no output file is written, but the report file is; `2` a usage error
(unknown flag, wrong arguments, unknown target).

## convert

    yakbd convert in.klc out.kbl.json              # the format comes from the suffix, in both directions
    yakbd convert layout.kbl.json out.keylayout    # .kbl.json, .klc, CLDR .xml, .keylayout
    yakbd convert -from xkb symbols out.kbl.json   # XKB symbols files have no suffix: -from / -to xkb
    yakbd convert kbdmine.dll out.kbl.json         # read an installed Windows layout DLL back

| flag | meaning |
|---|---|
| `-from F`, `-to F` | the format by name, for a file without a suffix: `kbl`, `klc`, `cldr`, `dll`, `keylayout`, `xkb` |
| `-o FILE` | the output file, instead of the second argument |
| `-variant V` | XKB input: the `xkb_symbols` section to read (default: the file's default section) |
| `-compose FILE` | XKB: the Compose file of the dead keys (read with an XKB input; written next to an XKB output) |
| `-include DIRS` | XKB input: folders of included symbols files, separated by `:` |
| `-tests FILE` | CLDR input: a CLDR keyboard test file to add |
| `-msklc` | `.klc` output for MSKLC's editor: report what it would change |
| `-langid XXXX` | the Windows LANGID to record when the input has none (a DLL never has one) |
| `-keep-keyboard-type` | DLL input: keep its keyboard type and subtype |

More: [Recovering a layout from a DLL](recover-a-layout.md), [CLDR keyboards](cldr.md).

## validate

    yakbd validate layout.kbl.json                 # the file only
    yakbd validate -target macos layout.kbl.json   # and what a macOS build would drop or default

`-windows` is the same as `-target windows`.

## compile

    yakbd compile layout.kbl.json                        # Windows DLLs, one folder per build
    yakbd compile -build x64,wow64 layout.kbl.json       # only these Windows builds
    yakbd compile -target macos layout.kbl.json          # <bundleName>.bundle
    yakbd compile -target xkb -o out layout.kbl.json     # out/xkb/ and out/compose/

| flag | meaning |
|---|---|
| `-o DIR` | output folder (default `.`) |
| `-build LIST` | Windows: the builds (`x64`, `wow64`, `x86`, `arm64x`, `arm64`); default: the file's `targets.windows.builds` |
| `-langid XXXX` | Windows: LANGID for a file without one |
| `-emit-expectations` | also write what every key types, for the test runners |

## installer

    yakbd installer layout.kbl.json                  # <name>-setup.exe for every Windows architecture
    yakbd installer -target macos layout.kbl.json    # <bundleName>.zip: the bundle and a README
    yakbd installer -target xkb layout.kbl.json      # <name>.tar.gz: the files, install.sh and a README

| flag | meaning |
|---|---|
| `-o FILE` | the output file |
| `-build LIST` | Windows: the machine kinds to carry (`x64`, `arm64`, `x86`); default: all |
| `-enable`, `-signin` | Windows: `true`/`false`, enable the layout for the user, or also for the sign-in screen and new users |
| `-langid XXXX` | Windows: LANGID for a file without one |

What the Windows installer does and its command line: [installer.md](installer.md),
[installer-cli.md](installer-cli.md).

## pin

    yakbd pin layout.kbl.json                        # rewrite the file with every Windows default written out
    yakbd pin -target xkb layout.kbl.json pinned.kbl.json

A pinned file carries every choice itself and no longer depends on yakbd's default rules for that target.
