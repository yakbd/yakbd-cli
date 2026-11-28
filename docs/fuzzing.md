# Fuzzing

Every reader of outside input has a Go native fuzz target (since 2026-11-26). The targets run locally, one
at a time; CI runs only their seed corpora, as ordinary tests in `go test ./...`, never the fuzzer.

## What every target checks

Each call goes through `internal/fuzzcheck.Check`, which fails the input when the reader

- panics (the fuzzer keeps the input and the stack);
- runs longer than the bound's time (5 s for a reader, 20 s for a whole build): a hang or a quadratic walk;
- allocates more than the bound's base plus a fixed number of bytes per input byte (32 MiB + 512 × size for a
  reader, 128 MiB + 4096 × size for a build): an attacker-chosen count or an amplifying structure;
- breaks the target's property (below), for example refuses an input without reporting an error.

For a reader that has a writer, a successful read is written, read again and written again, and the two
written forms must be byte-equal (the first read may be lossy; the second round trip may not).

## The targets

| target | package | input | property beyond the bounds |
|---|---|---|---|
| `FuzzParse` | `format/klc` | `.klc` bytes (any encoding) | write → parse → write is stable; the converted layout writes as `.kbl.json` |
| `FuzzRead` | `format/cldr` | CLDR keyboard3 XML, `<import>` of the embedded CLDR imports included | writes as `.kbl.json`; CLDR write → read → write is stable |
| `FuzzReadTests` | `format/cldr` | CLDR keyboardTest3 XML | each test file: write → read → write is stable |
| `FuzzRead` | `format/kbljson` | `.kbl.json` bytes | validator and reader; an accepted file writes, the written file validates, and write → read → write is stable |
| `FuzzRead` | `format/keylayout` | macOS `.keylayout` bytes (UTF-8 or UTF-16, XML 1.0 or 1.1) | write → read → write is stable; the import (`convert/keylayoutkbl`) reports no error on a file the reader accepts, writes as `.kbl.json`, and builds for macOS (`target/macos/build`) |
| `FuzzRead` | `format/xkb` | XKB symbols file bytes | write → read → write is stable; each section compiles with its includes (from the xkeyboard-config corpus, under the bound of a build) and imports (`convert/xkbkbl`) as a layout that writes as `.kbl.json` |
| `FuzzReadCompose` | `format/xkb` | Compose file bytes | write → read → write is stable and keeps every line |
| `FuzzParse` | `pe` | PE image bytes | `Parse` and every table accessor (exports, relocations, resources, load config, CHPE, dynamic relocations, ARM64X view) |
| `FuzzReadImage` | `target/windows` | layout DLL bytes | an image or an error; tables that compile again for the same architecture read back without `Differences` |
| `FuzzImport` | `convert/dllkbl` | layout DLL bytes | DLL → layout (the importer) → `.kbl.json` writer → `build.Lower` |
| `FuzzParse` | `installer/cmdline` | the argument list, joined by NUL | errors are `*Error`; the elevated copy's `ChildArgs` parse to the same action, repair mode, log and FORCE; no file access |
| `FuzzDecode` | `installer/payload` | manifest JSON and file bytes | decoded raw and as a payload whose header and SHA-256 are made to match; decode → encode → decode is stable |
| `FuzzFromExe` | `installer/payload` | installer bytes | `PEMachine`, `ImageEnd`, `FromExe` (what the installer does with its own file) |
| `FuzzDecode` | `installer/toolsetup` | tool payload manifest JSON and file bytes | as `installer/payload`'s, format 2 (components, DEFLATE-compressed files): no decoded path that `CheckPath` refuses and no settings location that `CheckUserData` refuses; files inflated only within the budget; decode → inflate → encode → decode is stable |
| `FuzzBuild` | `installer` | `.kbl.json` bytes | `yakbd installer`'s path: read, lower, compile all five builds, package, write the `setup.exe`; its payload decodes to the packaged builds |

Seeds come from the repository's test data, read by glob at test time, so new corpus files are picked up: the
reference specification's `.klc` corpus (`testdata/reference-spec/*/`, also re-encoded as UTF-16LE), every CLDR
keyboard and test file under `testdata/third_party/cldr-*/`, the spec's `.kbl.json` examples, the `.kbl.json` of
the `.klc` and CLDR corpus (for `FuzzBuild`), DLLs of every architecture and ARM64X variant, the `.klc` corpus
compiled for x64 and ARM64X (for `FuzzImport`), the example rows of `docs/installer-cli.md`. A glob that matches
nothing, or an architecture whose seed does not build, fails the test.

Inputs that once failed are committed under the package's `testdata/fuzz/<Target>/` and run in every
`go test`. A new reader of outside input gets a target of its own: a `fuzz_test.go` in its package that
seeds from the test data through `fuzzcheck.Seeds` or `fuzzcheck.Files` and wraps each call in `fuzzcheck.Check`,
and a row in the table above.

## Running a target

One target at a time, from the repository root, with at most 4 workers so the machine stays usable:

    go test -run='^$' -fuzz='^FuzzParse$' -fuzztime=10m -fuzzminimizetime=10s -parallel 4 -timeout 0 ./format/klc

Anchor the name: a pattern that matches two targets of one package refuses to run. `-fuzzminimizetime=10s`
matters: the fuzzer minimises every new interesting input for up to a minute by default, which on the DLL
targets (inputs of 10 to 20 KiB) left it executing almost nothing (FuzzImport: 156 executions in 30 s, 418 616
with the flag). `-timeout 0` keeps the test timeout (10 minutes by default) from ending a long run. On a
failure the fuzzer minimises the input and writes it to `testdata/fuzz/<Target>/<hash>`; `go test -run=<Target>/<hash> ./<pkg>`
re-runs it. Fix the cause, keep the file, run the target again.

## Limits

The readers keep the limits they already had: the DLL reader's (`docs/dll-import.md` §1, "Never trust the file":
at most 64 MiB, bounded table walks, a work budget of 16 × the file size + 1 MiB) and the PE reader's (4096 resource
entries, names of at most 1024 bytes). No new size, nesting or count limit was needed: within the bounds above no
target found a hang or an allocation out of proportion. Refusals were added where a reader accepted input it
could not write back or that the format forbids (results below). Two findings concerned the specification too: a
literal surrogate unit in a `.klc` cell, which the specification's text allows and the tool now refuses, and VK names
longer than the schema allowed (the specification widened the limit to 32 characters); both are in the klc
`FuzzParse` row below.

The seed corpus leaves out files above 64 KiB (`fuzzcheck.MaxSeed`): CLDR `egy-Egyp-t-k0-qwerty.xml` (414 KB)
would make every mutation slow. Its whole round trip allocates about 1 KB per input byte, linear, so it is no
finding; the CLDR conformance tests read it.

## Results of 2026-11-26 (4 workers, tool at the commits of that day)

Each target ran 10 minutes with `-parallel 4 -fuzzminimizetime=10s`; a target that failed was fixed and run again
for the full 10 minutes. Executions are those of the last, clean run.

| target | executions (10 min) | findings (all fixed; regression input under the package's `testdata/fuzz/`) |
|---|---|---|
| klc `FuzzParse` | 22 879 925 | a literal surrogate unit in a cell was read but written as refused `hhhh` (now refused); an empty key name `""` was written bare and did not read again; a key on VK D1 converted to a VK name longer than the schema's 24 characters (spec D96 widened the pattern to 32; names are written as they are); a name from an unterminated quote was written so that `//` became a comment (the writer now checks the form it writes) |
| cldr `FuzzRead` | 2 806 132 | `<layers>` without `<layer>`, a touch layer without id or row, `<locale/>` without id gave layouts the `.kbl.json` writer refused; `Read` now ends with the schema check (`cldr.xml.invalid`) |
| cldr `FuzzReadTests` | 40 819 590 | none |
| kbljson `FuzzRead` | 7 005 930 | none |
| pe `FuzzParse` | 16 977 376 | `Bytes(rva, 0)` in a section without raw data sliced at an arbitrary PointerToRawData (panic) |
| windows `FuzzReadImage` | 3 500 526 | none |
| dllkbl `FuzzImport` | 2 614 012 | none |
| cmdline `FuzzParse` | 58 794 348 | a lone `/` or `-` panicked (`setup.exe /` crashed the installer); now 1639. One property of the target was too strict (repair mode of an install) |
| payload `FuzzDecode` | 53 131 484 | none |
| payload `FuzzFromExe` | 62 166 029 | none |
| installer `FuzzBuild` | 5 371 306 | none |

After the interim VK-spelling change (it touched the `.klc` importer and the Windows build), `FuzzImport` and `FuzzBuild`
ran again for 2 minutes each: 309 620 and 1 347 447 executions, no finding.

toolsetup `FuzzDecode` (added 2026-11-27 with the extra payload, docs/installer-cli.md §12.1) ran 45 seconds with
the default workers: 5 339 425 executions, no finding.
With payload format 2 (2026-11-27: components, DEFLATE-compressed files) it ran 45 seconds locally with no failure;
its one finding during development (a decoded `yakbd.exe` without a build that did not encode again) is fixed and
kept as a seed in `installer/toolsetup/testdata/fuzz`.
