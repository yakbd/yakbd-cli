# internal/unicode/norm: agent rules

NFD and NFC (UAX #15) with the standard library only. `tables.go` is generated; never edit it by hand.

    go test ./internal/unicode/norm ./tools/genucd
    go run ./tools/genucd -ucd <folder> -version 18.0.0    # rewrites tables.go

- `<folder>` holds `UnicodeData.txt` and `DerivedNormalizationProps.txt` from
  https://www.unicode.org/Public/18.0.0/ucd/, downloaded into a new empty folder outside the repository. They are
  not committed; their SHA-256 sit in the header of `tables.go`, and a regeneration from the same files leaves it
  unchanged.
- Moving to another Unicode version: regenerate with `-version`, replace
  `testdata/third_party/unicode-<version>/NormalizationTest.txt` and its `LICENSE`, and update the version and date
  in `NOTICE` §5 and the version in `ARCHITECTURE.md` (package table, `internal/unicode/norm` row).
