# CLDR Keyboard 3.0: import, export and conformance

How yakbd reads and writes CLDR keyboards (UTS #35 Part 7, https://www.unicode.org/reports/tr35/tr35-keyboards.html),
what a Windows build of a CLDR keyboard loses, and how the conformance test over CLDR's own keyboards runs. The
mapping of every CLDR element is the spec repo's `coverage/cldr-keyboard3.md`; this page says what the tool does.

## 1. What is imported

`yakbd convert k.xml k.kbl.json [-tests k-test.xml]` (package `format/cldr`) reads a `keyboard3` file into a
self-contained `.kbl.json`:

- CLDR imports (`<import base="cldr">`) are inlined from the embedded copies in `format/cldr/import/`
  (`general.importInlined`, accepted); a local import is an error. Implied forms (`us`, `iso`, `abnt2`, `jis`, `ks`)
  are inlined as `forms` with SPEC Appendix A positions; implied keys (`a`–`z`, `A`–`Z`, `0`–`9`, `space`, `gap`)
  become Text bindings.
- Hardware rows are placed on the form's positions in order, from the first position of each form row.
- Markers stay markers with their transforms, except a marker dead key that `coverage/cldr-keyboard3.md` §3.6
  allows to become a `deadKeys` entry: literal transforms, and (D88) the set forms `\m{m}($[v])` → `$1` + literal or → `$[1:w]`, one entry per member of `v` in set order. A later group's catch-all `\m{.}(.)` → `$1` (or `\m{.}` → nothing) stays in the file and does not block (D95). The CLDR writer gives the entries back as literal transforms.
- Touch layers, flicks, long-press, multi-tap, displays, variables, transforms, reorders and `<special>` content are
  kept as they are; a CLDR test file becomes the file's `tests`.

`yakbd convert k.kbl.json k.xml` writes CLDR back. What CLDR elements cannot hold goes, by default, into one kbl
`<special>` element that the reader restores, so `.kbl.json` → CLDR → `.kbl.json` gives the same file.

## 2. What a Windows build loses, and why

A Windows layout DLL has key cells, dead keys and ligatures only (`targets/windows.md`). The CLDR corpus shows
these losses (all reported; kinds of SPEC §17.3):

| loss | why |
|---|---|
| `windows.transformNotCompiled`, `windows.markerNotCompiled` | a DLL has no context rules: transforms, reorders and markers do not exist there. A marker dead key becomes a real dead key only through §3.6. CLDR `fr.xml` writes its dead keys over variable sets (D88) with a later catch-all `\m{.}(.)` → `$1` (D95), so 20 of its 23 markers become dead keys; greek, currency and euro stay markers (their `\m{x}\m{x}` transforms) and are lost |
| `general.normalizationNotApplied` | each output is NFC-normalised at build time, but text made by several key presses is not: Windows does not normalise while typing (D73) |
| `windows.capsUnmatched` | CLDR has no Caps Lock fallback (D11): a key with no Caps Lock layer types nothing with Caps Lock on, where Windows would type the plain output |
| `windows.vkMissing` (error) | `pt-t-k0-abnt2` and `ja-Latn` bind IntlRo (abnt2 and jis forms), which Table A gives no VK; pin `targets.windows.keys.IntlRo.vk`, or accept the kind to build without that key |
| `windows.langIdMissing` (error) | a CLDR keyboard has no LANGID; pin `targets.windows.registration.langId` or pass `-langid` (D30) |
| `windows.frameKeyWithoutText` | Table A adds Escape, Backspace, Tab, Enter and NumpadEnter, which CLDR keyboards do not bind |
| `windows.deadKeyResultUnencodable`, `windows.deadCharacterInvalid` (errors) | `fr`: dead-key results with no one-unit NFC form (a letter with a mark Unicode has no precomposed character for), and dead-key displays that are not one unit; Windows dead keys compose to one UTF-16 unit. A file pins `deadKeys.<id>.character` or drops the entries; the conformance run accepts them (D95) |
| `windows.ctrlLetterFallback` (accepted) | Windows types U+0001–U+001A for empty Ctrl+letter cells |
| `general.touchDropped`, `general.displayDropped`, `general.testsDropped` | touch layers, flicks, long-press and multi-tap choices (also on hardware keys: `fr` super-2), key-cap displays and tests are not part of a DLL. A touch-only keyboard (`ja-Hira-t-k0-flicks`) does not build: `windows.noHardwareLayers` (error, D89) |

## 3. The conformance test

`go test ./convert/cldrconf` runs every keyboard of `testdata/third_party/cldr-release-49/keyboards/3.0` (13
keyboards, 5 test files; README there) through the tool and compares the result with
`convert/cldrconf/testdata/<keyboard>.txt`:

1. **import**: `yakbd convert -tests` writes a schema-valid `.kbl.json`; its losses are recorded;
2. **round trip**: `.kbl.json` → CLDR XML (and CLDR test files) → `.kbl.json` must be **equal**: equal as JSON values
   (`kbljson.SameJSON`: property order and white space do not count, an empty array or object counts as absent),
   tests included. The same round trip without the kbl `<special>` records the JSON paths that differ: today only
   `metadata.provenance`, so everything else is held by plain CLDR elements;
3. **tests**: one `.kbl.json` test per `<test>` of the CLDR test file;
4. **Windows**: `yakbd validate -windows` and `yakbd compile -langid 0409` (again accepting `windows.vkMissing`, `windows.frameKeyWithoutText`, `windows.deadKeyResultUnencodable` and `windows.deadCharacterInvalid`, D90, D91, D95,
   when the plain build fails); severity, kind and location of every loss and error are recorded, no message text;
5. **cases** (`coverage/cldr-keyboard3.md` §3.7, D92): each `<check>` of a CLDR test becomes a case, the key
   presses from the start of the test up to the check. A CLDR key id is pressed in the first hardware layer (in
   `hardware.layers` order) that binds it, with that layer's first modifier set Windows can type (no Caps Lock, no
   `other`, no Alt without Ctrl; `altR` is Ctrl+Alt with KLLF_ALTGR), at the first binding position in `positions`
   order (the sorted order `.kbl.json` is written in). `startContext` and `emit` are text already in the field:
   Windows typing never reads it, so the guest types the presses only and compares what they give. The emulator of
   the ToUnicodeEx rules (`internal/wincheck`) types the presses; start context + emitted text + typed text must
   equal the check, in NFC when the file normalises. A case is skipped, with the loss kind that explains it, when a
   step needs a touch gesture or backspace, a key is on no layer Windows reaches or its position is not in the
   build, or the emulated text differs (a skip no kind explains fails the test as a tool bug). Today: 12 typed, 2
   skipped (`bn` au: the au-length key outputs a marker that a transform turns into U+09CC; `pcm` dot below: a
   transform).

A change in what is imported, lost or typed fails the test. After an intended change:

    go test ./convert/cldrconf -update     # rewrites testdata/<keyboard>.txt
    git diff convert/cldrconf/testdata     # review; say in the commit what changed and why

`TestImportsEqualTheCorpus` checks that the embedded import files equal the corpus copies.

## 4. On Windows

`go run ./tools/cldrbundle -o DIR` writes a bundle for a Windows guest: the two `yakbd-wincheck` runners, the DLLs
of every keyboard with typed cases, one manifest entry and one `type` expectations line per typed case
(`cases/NNN.expect.txt`), `cases.txt` with every case and skip, and the README with the guest commands. Each runner
(64-bit process with the x64 DLLs, 32-bit process with the WOW64 DLLs) prints `PASS <keyboard>:<case>` or `FAIL`
per case and a count line.

VM runs: none recorded yet.
