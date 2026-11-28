# The report file (`-report`): a public interface

Every command that reads a layout (`compile`, `installer`, `convert`, `validate`, `pin`) prints its report as text
on stderr and, with `-report FILE`, also writes it as JSON. The text is for people and may change. The JSON file is
for programs: an editor that runs `yakbd` and shows the problems next to the fields they concern, a build script, a
CI check. Its shape is described here and kept stable (§6). It is written by `internal/cli/report.go`; the tests in
`internal/cli/report_test.go` check this contract for every command over the spec's examples, the reference
corpus and the CLDR keyboards (each converted to `.kbl.json` first).

## 1. When it is written

- Exit code 0: success. Exit code 1: the input has errors, or the operation failed; no output file is written, but
  the report file is.
- Exit code 2: a usage error (unknown flag, wrong number of arguments, an unknown `-target` or a command the target does not have, docs/targets.md). The usage text goes to stderr and **no**
  report file is written. A program that builds the command line itself can treat exit 2 as its own bug.
- `yakbd version` prints `yakbd V` and writes no report; `tool.version` of every report (§2) is the same `V`.

## 2. Shape

```json
{
  "format": 1,
  "tool": { "name": "yakbd", "version": "0.1.0-preview.3" },
  "command": "validate",
  "input": { "file": "minimal-qwerty.kbl.json", "format": "kbl.json" },
  "entries": [
    {
      "class": "loss",
      "severity": "accept",
      "kind": "windows.ctrlLetterFallback",
      "where": { "pointer": "/hardware/layers" },
      "message": "Ctrl+letter gives U+0001 to U+001A on 26 key(s) the file leaves empty"
    },
    {
      "class": "defaulted",
      "severity": "accept",
      "kind": "windowsDefault.dllName",
      "where": { "pointer": "/targets/windows/dllName", "missing": true },
      "message": "DLL name entk0kbl from metadata.id",
      "detail": { "value": "entk0kbl" }
    }
  ]
}
```

| member | meaning |
|---|---|
| `format` | the version of this shape, 1 today (§6) |
| `tool.name`, `tool.version` | `yakbd` and the version `yakbd version` prints |
| `command` | the command: `compile`, `installer`, `convert`, `validate` or `pin` |
| `input.file` | the input file as given on the command line |
| `input.format` | `kbl.json`, `klc`, `cldr` (CLDR keyboard XML), `dll`, `keylayout` (macOS, since 2026-11-28) or `xkb` (an XKB symbols file, named by `convert -from xkb`, since 2026-11-28), by the file's extension or convert's `-from`; empty for an unknown one |
| `entries` | every entry, in the order the steps made them; an empty list when there is nothing to say |
| `class` | `diagnostic` (a remark on the input), `loss` (data a conversion or build dropped or changed) or `defaulted` (a value a default rule chose, §4) |
| `severity` | `info`, `accept` (a loss a policy accepts), `warning` or `error`; any `error` means exit code 1 |
| `kind` | a stable identifier: the spec's loss kinds (SPEC §17.3), `windowsDefault.<rule>`, or a diagnostic code of the tool (`kbl.schema`, `klc.vk.unknown`, ...); policies (`onLoss`, `-accept`) refer to it |
| `where` | the location, §3; every member optional |
| `message` | for people; its wording may change |
| `detail` | free-form data of some kinds; for `defaulted` entries `value` (the chosen value) and, for a rule that fills several members, `fields` (§4) |

## 3. Locations

| member of `where` | meaning |
|---|---|
| `pointer` | a JSON Pointer ([RFC 6901](https://www.rfc-editor.org/rfc/rfc6901)) into the **input document**. Present only when the input is a `.kbl.json` file and the entry concerns a place in it; `""` is the whole document (a schema error on the top-level object) |
| `missing` | `true` when the input document has no value at `pointer`: the entry concerns a member the file leaves out (a default rule fills it, §4, or the entry asks for it, such as `windows.langIdMissing` at `/targets/windows/registration/langId`). The pointer then names that member; the deepest value of the document along it is an object, the place the member would be added |
| `path` | any other location. It **never** points into the input document. For a `.kbl.json` input it names a part of the output: a table of the DLL (`DLL`, `MODIFIERS`, `pusVSCtoVK`), a build (`x64`), a machine kind of the installer (`arm64`). For any other input (`.klc`, CLDR XML, a DLL), a `path` that starts with `/` names a place in the layout read from it, in `.kbl.json` terms (what `convert` would write), not a place in the input file |
| `file`, `line` | the file the entry comes from, and the 1-based line when known: `.klc` diagnostics give both; the CLDR reader gives the file |

A program that shows entries next to the fields of a `.kbl.json` document uses `pointer` (and `missing` to tell
whether the field is there yet), and shows every other entry as a message about the whole run: an entry without
`pointer` is never about a particular place of the input document. A binding is located at the layer that holds it
(`/hardware/layers/2/bindings/KeyQ`); an output no layer binds is located at its position (`/positions/KeyQ`).

There are no line or column numbers for `.kbl.json` input; a file that is not valid JSON gives one entry
(`kbl.json`) without a pointer. A program that parses the file itself knows the offset.

## 4. Defaults

Each choice a default rule of the Windows target makes (the spec's `targets/windows.md` and
`targets/windows-install.md`) is one entry of class `defaulted`, kind `windowsDefault.<rule>`, severity `accept`
(`-strict-defaults` makes them errors). It points at the member the rule fills, which is where `yakbd pin` writes
the value; `missing` is `true` unless that member (or, for several members, the object that holds them) is in the
file already. A rule that fills several members points at the deepest object that holds all of them and lists their
pointers in `detail.fields`:

| rule | `pointer` | `detail.fields` |
|---|---|---|
| `dllName`, `builds`, `kbdVersion`, `keyboardType`, `scanCodeTableSize`, `tables` | `/targets/windows/<rule>` | |
| `altGr`, `shiftLock`, `lrmRlm` | `/targets/windows/localeFlags/<rule>` | |
| `modifierBits`, `modNumber` | `/targets/windows/modifiers/bits`, `/targets/windows/modifiers/modNumber` | |
| `vk` | `/targets/windows/keys/<id>/vk`, or `/targets/windows/keys` | each `/targets/windows/keys/<id>/vk`, positions added from Table A included (`detail.value.addedFromTableA` names those; pin also writes them to `/positions/<id>`) |
| `scanCodeFlags`, `attributes` | `/targets/windows/keys/<id>/<rule>`, or `/targets/windows/keys` | each `/targets/windows/keys/<id>/<rule>` |
| `deadCharacter` | `/targets/windows/deadKeys/<id>/character`, or `/targets/windows/deadKeys` | each `/targets/windows/deadKeys/<id>/character` |
| `strings`, `versionInfo`, `registration` | `/targets/windows/<object>/<member>`, or `/targets/windows/<object>` | each member filled |
| `productCode` | `/targets/windows/registration/productCode` | |
| `enableForUser`, `enableForSignInAndNewUsers` | `/targets/windows/install/<rule>` | |
| `uninstallEntry` | `/targets/windows/install/<member>`, or `/targets/windows/install` | each of `productName`, `productVersion`, `publisher` filled |

With one member, `detail.fields` is left out and `pointer` is that member. `detail.value` holds the chosen value,
as before (for the multi-member rules, the members or positions it concerns).

## 5. Command line

Flags may come before, between or after the arguments, in every command: `yakbd validate layout.kbl.json -windows
-report r.json` is the same as `yakbd validate -windows -report r.json layout.kbl.json`. `--` ends the flags: every
argument after it is a file name, even one that starts with `-`. There is no standard input or output mode: `-` is
a file name.

## 6. Stability

- Within one `format`, members are only added, never renamed or removed; a reader ignores members it does not know.
- A change that would make an old reader misread the file (a member's meaning, the rules of §3) raises `format`.
- `kind` values are stable: a kind is renamed only with the spec's loss kinds, and a renamed kind is named in the
  release notes. Messages are not stable.
- 0.1.0-preview.1 wrote only `entries`, with every location in `where.path` (pointers mixed with labels, every
  default at `/targets/windows`), and took flags only before the arguments (except `convert`). From
  0.1.0-preview.2 on, the file and the command line are as described here.
