# Targets: how a platform plugs into the command line

A **target** is a platform a layout is built for. Windows is the first; macOS (`.keylayout` in a `.bundle`) and XKB
(symbols and Compose files for Wayland and X11) come next. The commands `compile`, `installer`, `pin` and
`validate` work on a target, picked with `-target`:

    yakbd compile -target windows layout.kbl.json        # the same as without -target
    yakbd compile -target windows -build x64,wow64 layout.kbl.json
    yakbd validate -target windows layout.kbl.json       # the same as validate -windows

## 1. The command line

- Without `-target`, `compile`, `installer` and `pin` use `windows`, so every command line written before targets
  existed means what it meant. `validate` without `-target` (and without `-windows`) checks the file only, as
  before; with `-target T` it also builds for `T` and reports what that build would drop or default. `-windows` is
  `-target windows`.
- Before targets, `compile -target` took the list of Windows builds (`-target x64,wow64`). That spelling still works:
  a `-target` value that names no target but lists a Windows build means `-target windows -build <list>`. New
  command lines use `-build`.
- An unknown target is a usage error: exit code 2, no report file (docs/report-file.md §1), and the message lists
  the known targets. So is a command the target does not have (`the target T has no installer command`).
- Each target adds its own flags to a command (Windows: `-build`, `-langid`, `-emit-expectations` of compile;
  `-build`, `-enable`, `-signin`, `-langid` of installer), and `-h` lists those of the target given. The shared
  flags are the command line's: `-report`, `-q`, `-strict-defaults` (which makes `<DefaultsKind>.*` an error, for
  Windows `windowsDefault.*`) and `-accept` of compile and installer. A target never registers these names (nor `-target`
  or validate's `-windows`): the flag package panics on a flag defined twice.
- The report file does not change: its entries carry the target's loss kinds (`windows.*`, later `macos.*`,
  `xkb.*`) and defaulted kinds (`windowsDefault.*`, later `macosDefault.*`, ...).

## 2. The interface

Package `target` (`target/target.go`) holds the interface; `target/registry` lists the targets; the command line
(`internal/cli/target.go`) reads the layout, runs the target's command, prints the report and writes the files.

| | job |
|---|---|
| `Target.Name()` | the `-target` value |
| `Target.DefaultsKind()` | the prefix of the target's defaulted kinds (`windowsDefault`) |
| `Target.Command(name, fs)` | registers the command's own flags on `fs` and returns the command, or nil when the target has no such command; called before the flags are parsed |
| `Command.Check()` | after parsing, before the layout is read: an error is a usage error |
| `Command.Run(job)` | the work, on a layout read without errors; adds entries to `job.Report` and returns a `Result` |

`Job` carries the layout, the report, the command line's policy (`Policy`: `-strict-defaults` and `-accept`;
`StrictPolicy`: `-strict-defaults` alone) and the tool's version. `Result` holds the files to write (each with the
line to print once it is written), more lines to print, the pinned layout of `pin`, or a failure message. A command
with an error entry writes nothing: the command line checks the report before it writes any file.

Rules a target keeps:
- Every choice taken from a default rule is a `defaulted` entry of kind `<DefaultsKind>.<rule>` with the value in
  `detail.value` (SPEC §17.1), and every loss a loss kind of the spec. Resolve them with `policy.Resolve(report,
  layout, "<target name>", job.Policy)`: the loss and policy code is shared.
- A target never writes files itself and never prints; it returns them.
- Output paths are the target's business (its own `-o` flag), and so is what each printed line says.

## 3. Adding a target

1. A package `target/<name>` (for example `target/macos`) with a type that implements `target.Target`. Keep the
   build itself in that package or below it; the type only wires it to the command line (Windows:
   `target/windows/wintarget` over `target/windows` and `target/windows/build`).
2. One line in `target/registry/registry.go`: `macos.Target{},` in `Targets`.
3. The file's own choices: `targets.<name>` in `model.Targets` (`model/model.go`) as the spec's schema defines it
   (the 0.3 targets `Android`, `AndroidIME`, `IOS`, `ChromeOS` and `Keyman` exist, each its own type holding `onLoss` only: add members to your own type, in schema order), and a `case "<name>"` in `policy.Resolve`
   (every target of the schema is there). A member new in a format version also goes into `model.MinVersion`, so that a writer
   raises `kbl` only when a file needs it. The loss kinds `<name>.*` come from the
   spec's kind table (`policy/kinds.go`, tested against the schema).
4. Tests: the target's own package tests, plus one command-line case in `internal/cli` (see `TestTargetInterface`
   for a target with one command and a flag of its own).

A target can start with one command (`compile`) and add `installer` or `pin` later; the others give the usage error
of §1 until then.

## 4. Native formats (`convert`)

`convert` and every command's input go through one table of file formats, `formats` in `internal/cli/formats.go`:
a name, the report's `input.format`, the file name suffix, a reader (file → model, with the checks of SPEC §19)
and a writer (model → file, its losses resolved under the file's policies). A native format of a new target
(`.keylayout`, an XKB symbols file) is one more row, its code in `format/<name>` and `convert/<name>kbl` as for
`.klc`. A format without a fixed suffix (XKB symbols files have none) needs a way to name it on the command line;
add that flag with the format, additively: XKB symbols files are named with `convert -from xkb` / `-to xkb` (`formatByName`), and a writer may add files next to its output (`writeOptions.side`, the Compose file of an XKB output).

## 5. On-screen keyboards, runtimes and signing keys

Format 0.3 adds targets for on-screen keyboards (Android `androidime`, `ios`) and for platforms whose packages are
signed by the builder (Android). What they share:

- **Which layers a target builds from.** A target may implement `target.LayerSource` (`Layers()`): `Hardware` (the
  default: every target without it, Windows, macOS, XKB), `Touch` (the touch layers only) or `TouchOrDerived` (the
  touch layers; for a file that has none, touch layers derived from its hardware layers by the target's default
  rule, reported as `<DefaultsKind>.touchFromHardware`; `target.DeriveTouch(t, layout)` says when). The command line
  rejects no file for its layers: a file without the layers a target needs is that target's own loss
  (`<name>.noHardwareLayers` and the like), so `validate -target` reports it like any other. `-h` of a touch target
  says that it builds an on-screen keyboard.
- **One layout per build.** Every building command takes exactly one layout file and writes one installable
  keyboard for the target; there is no list or set input.
- **`-runtime <file|dir>`.** A target built on a prebuilt runtime (the Android IME APK for `androidime`, the
  runtime's source folder for `ios`) registers `-runtime` with `target.RuntimeFlag(fs, what)` and checks it in
  `Check` with `target.CheckRuntime(path, dir)`: required, and an existing file or folder (else exit 2). The
  public tool never embeds a runtime: the builder names it.
- **`-key <file.pem>`.** An Android target registers `-key` with `apk.KeyFlag(fs)` and gets its signer from
  `apk.SignerFor(path)`: the builder's own key (PEM: a PKCS #8 private key, RSA 2048 or more or EC P-256, and
  optionally its certificate; an RSA key without one gets a certificate derived from the key alone, the same on
  every build; an EC key needs its certificate), or without `-key` a generated test key. It reports each APK with
  `apk.ReportSigned`: an info entry `apk.signed` with the package name and the certificate's SHA-256 fingerprint (what
  a builder registers for Android developer verification), and with a test key a warning `apk.testKey` (a later
  build cannot update that APK). The package itself is described in [apk.md](apk.md).

`-runtime` and `-key` are conventions, not names the command line reserves: only targets that need them register
them, always through these helpers, so the flags mean the same on every target.
