# pe: agent rules

PE/COFF writer and reader with no keyboard knowledge: keyboard tables, stubs and checks such as the 0x68-byte
`KBDTABLES` rule live in `target/windows`. ARM64X specifics: `docs/arm64x.md`.

    go test ./pe ./target/windows
    go test -run='^$' -fuzz='^FuzzParse$' -fuzztime=10m -fuzzminimizetime=10s -parallel 4 -timeout 0 ./pe   # after reader changes (docs/fuzzing.md)

- Format facts come from the public "PE Format" page (URL in `pe/pe.go`), the reference spec's `dll-format`, and
  for ARM64X the sources listed in `docs/arm64x.md` and the ARM64X section of `CLEAN-ROOM.md`. Cite the source of
  any new field; add a new source to `CLEAN-ROOM.md`.
- Output must stay byte-for-byte deterministic: time stamp 0, sorted resources, no map iterated into the output.
  The golden digests in `target/windows/testdata/` catch any change.
- The reader never trusts its input: bound every pointer, size and count before use.
- `pe` is compiled into the installer stub: rebuild it after a change (root `AGENTS.md`).
