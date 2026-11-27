# CLDR keyboard files (release-49)

Unmodified copies of files from https://github.com/unicode-org/cldr/tree/release-49/keyboards: every keyboard of
`keyboards/3.0/`, every file of `keyboards/import/` and every test file of `keyboards/test/` at tag `release-49`
(published 2026-10-21, still the newest CLDR release on 2026-11-26). The first seven files were fetched on
2026-11-22, the other ten on 2026-11-26 (the seven were compared again then: byte-identical). They are the CLDR
conformance corpus (`convert/cldrconf`, `docs/cldr.md`) and the source of the spec's `examples/cldr-pt-abnt2.kbl.json`.
They are under the Unicode License v3 (`LICENSE` in this folder), not under the repository's MIT-0.

`format/cldr/import/` holds the same four import files, embedded because the reader inlines them; a test checks
that they equal the copies here. Keyboards name their imports with a CLDR version prefix (`45/…`, `47/…`); the
release has one copy of each.

| keyboard (`keyboards/3.0/`) | form | test file (`keyboards/test/`) | notes |
|---|---|---|---|
| `bn.xml` | us | `bn-test.xml` | markers with transforms, reorders |
| `egy-Egyp-t-k0-qwerty.xml` | us | – | hieroglyphs: many markers and transforms (414 kB) |
| `fr-t-k0-test.xml` | iso + touch | `fr-t-k0-test-test.xml` | touch layers, flicks, long-press, multi-tap, displays |
| `fr.xml` | iso | – | markers as dead keys over variable sets, displays |
| `ja-Hira-t-k0-flicks.xml` | touch only | – | a flick-based touch keyboard |
| `ja-Latn.xml` | jis | `ja-Latn-test.xml` | binds IntlRo (no default VK); row 1 has 13 keys for 14 positions |
| `mt-t-k0-47key.xml` | us | – | |
| `mt.xml` | iso | – | |
| `pcm.xml` | iso | `pcm-test.xml` | one transform (`''` → dot below) |
| `pgd-Khar-t-k0-qwerty.xml` | us | – | markers and transforms, long outputs |
| `pt-t-k0-abnt2.xml` | abnt2 | `pt-t-k0-abnt2-test.xml` | source of example (c); markers without transforms |
| `sa-Deva-t-k0-qwerty.xml` | us | – | transforms |
| `xct-Tibt-t-k0-qwerty.xml` | us | – | Caps Lock layers, markers, transforms |

| import (`keyboards/import/`) | use |
|---|---|
| `keys-Zyyy-punctuation.xml`, `keys-Zyyy-currency.xml` | imported by every keyboard; inlined |
| `keys-Latn-implied.xml`, `scanCodes-implied.xml` | CLDR's implied keys and hardware forms (SPEC §8, Appendix A) |
