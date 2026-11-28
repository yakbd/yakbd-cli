# yakbd tool repository: agent rules

`yakbd` (Go) converts keyboard layouts between `.kbl.json`, `.klc`, CLDR Keyboard 3.0 XML, macOS `.keylayout` and Linux XKB symbols files (`convert -from xkb`), validates them, and
compiles them into Windows layout DLLs and installers and macOS layout bundles. Windows and macOS are the first targets: the official
formats of other operating systems come next, then third-party formats such as Keyman. The `.kbl.json` format lives
in the sibling repository `yakbd/kbl-spec` (organisation https://github.com/yakbd). Design and package map:
`ARCHITECTURE.md`; how a platform plugs in (`-target`, package `target`): `docs/targets.md`.

## Commands

Run from the repository root. Go 1.26.8 (the version CI and `installer/stub-386.version` pin), standard library
only, no other setup.

    gofmt -l .                                  # must print nothing; CI fails on any file listed
    go vet ./... && go test ./...               # what CI runs; about 10 s
    go test ./target/windows/build -run TestD86 # one package, one test
    go build -o /dev/null ./cmd/yakbd           # build check (./yakbd and /dist/ are ignored build output)
    go run ./cmd/yakbd validate testdata/kbl-spec/examples/minimal-qwerty.kbl.json
    go test -run='^$' -fuzz='^FuzzParse$' -fuzztime=10m -fuzzminimizetime=10s -parallel 4 -timeout 0 ./format/klc   # one fuzz target, locally only (docs/fuzzing.md)
    go run ./tools/genkeysyms -h <keysymdef.h of xorgproto> -version xorgproto-2026.1   # regenerate format/xkb/keysyms_table.go

CI (`.github/workflows/ci.yml`: gofmt, vet, test with the fuzz seeds but never the fuzzer, a cross-build for six
OS/arch pairs plus `cmd/yakbd-wincheck` for windows/amd64 and 386) runs only on push to `main` and `workflow_dispatch`, and only when
a path in its `paths` filter changes: a push that touches only Markdown starts no run. Keep it that way (owner
rule: no `pull_request` trigger, no runner matrix). Push in batches, after vet and test pass locally.

GitHub API: the rate limit is shared. Never poll `gh` in a loop; wait for a run with one
`gh run watch <id> --interval 120 --exit-status` in the background, or check at most every 2 to 3 minutes. On 403
or "rate limit", wait for the reset time from `gh api rate_limit` instead of retrying. GitHub code search always
with `--repo <named repo>`.

## Gotchas

- The installer stub `installer/stub-386.exe` is committed and embedded. It compiles in more than `installer/`:
  list its packages with `GOOS=windows GOARCH=386 go list -deps ./installer/stub | grep yakbd` (today including
  `pe`, `loss`, `target/windows`). After changing any of them run `go run ./tools/buildstub` and commit
  `installer/stub-386.exe` and `.version` with the change; otherwise `TestEmbeddedStubIsFresh` fails in CI. Locally
  that test skips unless your Go version equals the recorded one.
- Spec copies must equal the spec repository: `format/kbljson/schema/<version>/kbl.schema.json`, one per format version
  the reader implements (0.1, 0.2; `Versions` with each `SchemaCommit` in `format/kbljson/kbljson.go`; 0.2 is a
  draft on the spec branch `spec-0.2`, so its drift test needs that branch checked out), `testdata/kbl-spec/examples/`, `target/macos/keycode/macos-default-keycode.tsv` (`SpecCommit`, a commit of `spec-0.2`), `target/windows/vk/windows-default-vk.tsv`
  (`TableASpecTag` in `target/windows/vk/scancodes.go`: a spec release tag, read at the tag when the checkout has it). Their drift tests skip silently, in CI too, unless the spec
  repo is checked out next to this one as `../kbl-spec` (or under its former name `../kbl-spec-prototype`) or `YAKBD_SPEC_DIR` names it. When you copy new
  versions, copy the files unchanged, move the commit constant, and run
  `YAKBD_SPEC_DIR=<spec checkout> go test -count=1 ./format/kbljson ./target/windows/vk ./target/macos/keycode ./policy`. The spec
  repo's `STATUS.md` changelog names the format changes the tool has to follow.
- Golden files: an intended change of DLL bytes needs `go test ./target/windows -update`, of `.klc` diagnostics
  `go test ./format/klc -update`. A change in what a CLDR keyboard imports, loses or types fails
  `convert/cldrconf` (docs/cldr.md §3): `go test ./convert/cldrconf -update`. Review the diff of `testdata/` and say in the commit which layouts changed.
- `installer/cmdline` tests read their tables from `docs/installer-cli.md`: editing that document's tables changes
  the tests. Run `go test ./installer/...` after editing it.

## Clean room (binding)

The tool is written only from these sources:
- this repository and the spec repository;
- the clean YAKBD reference specification (the revision `CLEAN-ROOM.md` names, with its tree hashes). It is
  the only source for Windows DLL, Windows behaviour, installation and `.klc` facts. Without it, a Windows fact
  needs a public source or a measurement (the organisation's `CONTRIBUTING.md`,
  https://github.com/yakbd/.github). Record in `CLEAN-ROOM.md` which of its sections, and
  which part beyond its specification documents, you read;
- public pages and public repositories, for example CLDR `keyboards/` and the LDML keyboard spec (UTS #35 Part 7),
  Unicode data files and UAX #15, the PE format and other Microsoft Learn pages, Go documentation. Facts only, in
  your own words, with URL and date; `CLEAN-ROOM.md` lists every one used so far.

Never read Windows SDK or WDK headers, leaked Windows source, decompiled or disassembled Microsoft code, or Wine or
ReactOS source. When you read something new, add it to `CLEAN-ROOM.md` (what, which sections). A fact the
reference spec lacks is never filled in from memory of Windows internals: write the question and what the tool
does meanwhile in the spec repo's `OPEN-QUESTIONS.md`. Questions for the owner go there too, format decisions to
its `DECISIONS.md` (cited in code as "kbl D86"). Describe CLDR in
your own words and link to it; do not copy its prose.

## Which text wins

- What a `.kbl.json` file means: the spec repo's `SPEC.md` (normative) and `targets/windows.md`,
  `targets/windows-install.md`; the schema checks structure only.
- Windows facts: the reference specification (revision 10).
- This repo's `ARCHITECTURE.md` and `docs/*.md` describe what the tool does. Where they disagree with the spec,
  the spec wins: change the tool, or record the difference in the spec repo's `OPEN-QUESTIONS.md`.
- Owner decisions are the spec repo's `DECISIONS.md`.

## Repository rules

- Standard library only: `go.mod` has no requirements, and the stub's reproducible build depends on that. Windows
  calls use plain `syscall`, not `golang.org/x/sys`.
- No Python anywhere: code, tests, CI, scripts or documented procedures (owner rule). A check is a Go test or a Go
  command under `tools/`.
- Licence MIT-0 (`LICENSE`). Third-party material keeps its licence and gets an entry in `NOTICE`: CLDR and
  Unicode files under `testdata/third_party/` with their licence text, inputs copied from the reference
  specification under `testdata/reference-spec/`.
- No Windows host or VM is reachable from here. Windows checks are bundles a separate runner executes
  (`tools/AGENTS.md`); record their results in `docs/*.md`.

## More instructions

Folder rules load when you work there: `installer/AGENTS.md`, `pe/AGENTS.md`, `tools/AGENTS.md`,
`internal/unicode/norm/AGENTS.md`. Change this file in the same commit as any command, path or rule it names.
