# macOS layout check

Checks keyboard-layout bundles on a real Mac, GitHub's Mac runners, without anyone at the keyboard. The workflow
`.github/workflows/macos-layout-check.yml` (manual only) runs `run.sh` on `macos-15` and `macos-26`, once with
the bundles in `~/Library/Keyboard Layouts` and once in `/Library/Keyboard Layouts` (`MACOS_CHECK_PLACE=system`,
with sudo). Each job writes one `summary.txt`, uploaded as the artifact `macos-check-<runner>-<user|system>`
together with `hitoolbox.txt` (the `com.apple.HIToolbox` preferences before and after each checker run) and shown on the
run's summary page.

First comes a control pass (`control`): one of Apple's own layouts that is installed but not enabled goes through the
same enable, select and typing steps, with expectations taken from its own `UCKeyTranslate` output. If the control
fails too, the runner session cannot switch layouts at all.

For each case, `run.sh` copies the bundle into the folder. `check.swift`, compiled on the runner
with the preinstalled `swiftc`, then:

1. reads the `KLInfo_<name>` entry of the bundle's `Info.plist` for the input-source id;
2. reports whether the system knows the source after the copy alone (`visible-after-copy`), registers it with
   `TISRegisterInputSource`, enables and selects it, and confirms that it is the current keyboard input source;
3. takes the layout data the system compiled from the XML (`kTISPropertyUnicodeKeyLayoutData`) and runs every
   expectation through `UCKeyTranslate`, carrying the dead-key state from stroke to stroke (`translate:<name>`);
4. opens a text view of its own, brings it to the front and sends every expectation as key codes, never as
   characters, in two ways: posted `CGEvent`s (`type-cgevent:<name>`) and System Events `key code`
   (`type-sysevents:<name>`). It then reads the text back. System Events has no caps-lock modifier, so caps cases are
   `SKIP` there.

Before the checker runs, `run.sh` registers the layout (`<case>/register`: a checker run with
`MACOS_CHECK_REGISTER_ONLY=1` that stops after registration) and then adds it to `AppleEnabledInputSources` in
the `com.apple.HIToolbox` preferences (its keyboard `id` and `name` from the `.keylayout`), in the same shape
as the entry for U.S. that the runners already have (`hitoolbox.txt`). The checker, a new process, then finds it enabled. Without that entry, `TISEnableInputSource`
returned 0 but did not enable a bundle in `~/Library/Keyboard Layouts`; an entry written before registration
was not always honoured (see Runs).
`MACOS_CHECK_WAIT` sets how long the checker waits for a registered layout to be listed (60 s).

## summary.txt

There is one line per check: `PASS|FAIL|SKIP|INFO <case> <check> [: detail]`. Typing is `SKIP` while
the layout is not the current input source; `current-before-typing` and `current-after-typing` check that it
stayed selected. A failure gives what came out and
what was expected, with code points. The checker never stops at the first failure. A 300-second deadline ends a
hung case with `FAIL <case> deadline`. When any line is `FAIL`, the artifact also holds `log.txt`, a filtered
`log show` of the last ten minutes, and the job fails.

## Adding a case

Make a folder `cases/<case>/` with:

- `<Name>.bundle/` holding `Contents/Info.plist` (with `KLInfo_<Name>` → `TISInputSourceID`, `TISIntendedLanguage`)
  and `Contents/Resources/<Name>.keylayout`. Every case needs its own input-source id and its own negative keyboard
  `id`, because all cases are installed side by side. `run.sh` takes the name for the preference entry from the
  bundle's base name, so `<Name>` must be the same in the bundle, the `.keylayout` file, its `name` attribute and
  `KLInfo_<Name>`. It reads the keyboard `id` from the line holding `<keyboard`, so keep that start tag on one line
  with `id` after another attribute (as `group="126" id="..."`); otherwise the case fails with `FAIL <case> case`.
  Give each case at least one output the other cases lack, so typing on the wrong layout cannot pass.
- `expect.tsv`: one expectation per line, `name<TAB>strokes<TAB>expected`. Strokes are virtual key codes
  (0 to 127) separated by spaces, each with optional `+shift`, `+option` or `+caps`, or `+rshift` / `+roption`
  for the right-hand key (typed by `CGEvent` with the key's device bit and the modifier key itself; `SKIP` through
  System Events, which has no right-hand modifiers). For example, `24 14` is two strokes, and `0+shift+option` is one. The expected text is UTF-8; `\s` is a space, `\t` a tab, `\\` a backslash,
  `\u{HEX}` any code point. Lines starting with `#` are comments.
- optionally a file `quarantine`: `run.sh` then marks the installed copy with `com.apple.quarantine`, as a
  browser does for a downloaded file.
- optionally a file `name.txt`: the localised name the system should show for the layout
  (`kTISPropertyLocalizedName`, check `localized-name`); the summary's `INFO host languages` line says which
  languages the session prefers.

No other change is needed. The cases run in name order.

### Cases generated by yakbd

A case folder can hold a `layout.kbl.json` instead of a bundle and `expect.tsv` (and, optionally, `quarantine`).
`run.sh` then builds yakbd from this repository (`go build`; the workflow sets up Go 1.26.8) and runs
`yakbd compile -target macos -emit-expectations -max-expectations 60`, which writes the bundle and an `expect.tsv`
into `OUT/cases/<case>/` (uploaded with the artifact). The expectations come from the `.kbl.json` file, not from the
built `.keylayout`: the model's resolution (SPEC §9.3) and dead-key machine (SPEC §14.2) at the build's key codes, for
every position with a code below 128 in the states none, Shift, Option, Shift+Option and Caps Lock, each dead key
followed by every base an entry names (chained dead keys one step further), by Space and by itself; outputs with
control characters and empty outputs are left out; dead-key lines first, then the rest spread evenly. So a pass
means macOS types what the format says the file means. The file must pin a plain `layoutName` (the bundle name
follows it), its own `inputSourceId` and a keyboard id of its own (pinned, or the hash of the input-source id).
`target/macos` tests check every such case in `go test`. A generated case may add an `extra.tsv` (same format as
`expect.tsv`), appended to the generated lines: hand-written expectations for what the generated ones do not cover.

## The cases here

- `check`: a US-like layout written for this check. It has two swapped keys (Y and Z), `ø` on the semicolon key,
  a two-character output (`ng`), Option outputs (`ß`, `æ`, `Æ`), a dead acute key on `=` (`é`, `á`, the terminator
  `´`), and a caps-lock table that differs from the shift table (caps + 1 gives `1`, not `!`).
- `quarantined`: the same layout under another name and id, but with `þ` on the grave key (`` ` `` in `check`), installed with the quarantine attribute, to see
  whether Gatekeeper blocks an unsigned layout bundle that was downloaded.
- `gen-demo` (generated): the kbl spec's macOS example (f) with names of its own: an ISO layout (Backquote on
  code 10, IntlBackslash on 50), an Option layer, a Caps Lock layer that differs per key, the two-character output
  `ng`, dead keys on Option and a chained dead key (acute then circumflex), a Command component.
- `gen-remap` (generated): written for this check: swapped Y and Z, `ŋ` and the two-character `ch` (`Ch`, `CH` under
  Caps Lock), an Option layer (`å ø € ß þ`), a Caps Lock layer that differs from Shift (Caps + 1 gives `1`) with an
  explicit nothing, and a dead ring key with `noMatch` `emitText`.
- `gen-corpus` (generated): `us_ee.keylayout` of the corpus (`testdata/third_party/keylayout/us-ee-keylayout-osx`,
  MIT, its LICENSE beside it) converted to `.kbl.json` by `yakbd convert`, with the names, group 126 and id -14844
  set for this check: U.S. with Estonian letters and four Option dead keys.

- `sides` (hand-written from a yakbd build): key maps chosen by the left-hand words `shift` and `option` and the
  right-hand words `rightShift` and `rightOption`, which differ only in key code 0 (`A` or `Ʀ`, `å` or `ɒ`): does
  macOS hand a layout the right-hand state (kbl spec `targets/macos.md` §8 item 3)? Measured (run 46117765009): no, the
  right-hand key selects the left-hand map (`A`, `å`), so `expect.tsv` holds that. `translate:` lines of right-hand
  strokes are `INFO` only (which bits `UCKeyTranslate` wants for them is this checker's guess).
- `gen-pending` (generated, with `extra.tsv`): a key with no output while a dead key is pending, a dead key with
  `noMatch` `emitNothing` (written as `<when state="macron" output=""/>`), and a terminator plus a two-character output
  in one press under the default `maxout` (§8 items 1, 2, 7); the `extra.tsv` line of `gen-demo` does that press too.
  Built with `next="none"` (no output) instead, the layout was not listed at all (runs 46117765009, 46119172899).
- `gen-spacing` (generated, with `extra.tsv`): `gen-pending` with `noMatch` `emitSpacing`: the same build path (every
  text an action, control characters in `<when>` outputs) without an empty output, to tell that path apart from the
  empty output.
- `twin-next-none`, `twin-empty-output`, `twin-next-sink` (hand-written from one yakbd build): three small layouts that
  differ only in one `<when>` of a dead key on a printable key: `next="none"` alone, `output=""`, and `next` to a state
  with no `<when>` and no terminator. Which of them the system lists says which form of "nothing" macOS accepts.
- `gen-maxout` (generated, with `extra.tsv`): the same press with `maxout` pinned to 2, the longest single output and
  one less than terminator and output together (§8 item 7), and a key code without a key pressed while the dead key is
  pending (§8 item 1, in a layout that is listed). It also shows that key codes without a key do not stop a layout
  from loading.
- `gen-lproj-hyphen`, `gen-lproj-utf8` (generated, with `name.txt`): the localised name only in a hyphenated
  `en-US.lproj`, and one with letters outside ASCII in the UTF-8 `InfoPlist.strings` the build writes (§8 item 4).
- `gen-klc` (generated): the repository's `testdata/klc/msklc-style.klc` converted by `yakbd convert` (rows MSKLC adds,
  `Break` among them, which has no macOS key: `macos.positionWithoutKey`); `gen-cldr`: CLDR release 49
  `keyboards/3.0/mt.xml` converted (Unicode License v3, `LICENSE` beside it); `gen-specg`: the kbl spec's example (g)
  (`xkb-caps-deadkeys`), which `tools/xkb-check` builds too, with a LANGID so the same file also builds for Windows.

Not measurable here: adding a layout by hand in System Settings (§8 item 5) and the ISO swap of codes 10 and 50 (item
6), which needs a real ISO keyboard (a runner types by key code).

## Locally

On a Mac, `tools/macos-check/run.sh OUT` does the same as the workflow. `MACOS_CHECK_CASES="a b"` (the workflow's
input `cases`) runs only those cases, for a re-run of a few. It installs into your own
`~/Library/Keyboard Layouts`, adds each test layout to your enabled input sources and selects it, so remove them and switch back afterwards.

## Runs

All on GitHub's arm64 images, `macos-15` (15.8.2) and `macos-26` (26.7.2), 2026-11-28.

| run | commit | result |
|---|---|---|
| 46102612485 | cff8cb5 | UCKeyTranslate: every case passes on both images. Key codes typed into the text view arrive by both methods (AXIsProcessTrusted and CGPreflightPostEventAccess true), but come out as US, because the layout was not selected: registered (0), enable 0 but still disabled, select -50 |
| 46102900903 | aeee2c4 | the same; an enabled-sources entry written by the checker itself, mid-run, did not help |
| 46103925085 | 39c126d | control (Apple's German) enables, selects and types at once; our layouts: `TISEnableInputSource` alone enabled one in `/Library` but none in `~/Library`; after the enabled-sources entry, a new checker process found every layout enabled, selected it, and every typing case passed, quarantined copy included, in both folders on both images; no restart of the text-input agents needed |
| 46104372309 | 0a17aee | entry written right after the copy, then one checker run: `check` passed everywhere, `quarantined` was selected only on macos-15 in `/Library` (enable stayed false elsewhere, with the entry present), so the entry's timing matters, not the quarantine |
| 46104797718 | 5c091fb | register, entry, checker: all four jobs (both images, `~/Library` and `/Library`) 102 PASS, 0 FAIL, 4 SKIP (caps cases through System Events): control, every UCKeyTranslate case, every typing case by `CGEvent` and by System Events, the quarantined copy included |
| 46105185134 | 38e2165 | the same with layouts that type differently (`þ` against `` ` ``) and selection checked before and after typing: all four jobs 114 PASS, 0 FAIL, 4 SKIP |
| 46108712955 | 0ea9ff3 | first run with the cases yakbd generates from a `.kbl.json` (gen-corpus, gen-demo, gen-remap: bundle by `compile -target macos`, expectations from the model): all four jobs 661 PASS, 0 FAIL, 27 SKIP (Caps Lock cases through System Events); a smoke run, the round's verifier runs it again |
| 46117765009 | 649858d | the round's verification run, 12 cases: every job 1476 PASS, 7 FAIL, 276 SKIP. Right Shift and Right Option type with the left-hand key map (`sides`); localised names from `en-US.lproj` and from a UTF-8 `InfoPlist.strings` shown (`AppleLanguages` `en-US`); `gen-klc`, `gen-cldr`, `gen-specg` and `gen-demo`'s terminator plus `ng` pass; `gen-pending` not listed at all |
| 46119172899 | 042a5f5 | re-run of `sides`, `gen-pending`, `gen-maxout` (`cases` input): `sides` passes; with `maxout` 2 the system still types terminator and `ng` in one press (3 units); `gen-pending` (now without the `maxout` pin) is still not listed: the system rejects the layout with `noMatch` `emitNothing` (`next="none"`) or the key code without a key |

What this settles: an unsigned, hand-copied layout bundle (and one carrying `com.apple.quarantine`,
set by `xattr` here, not by a real download) is listed, compiled and usable at once in the same session;
`spctl` rejects it ("no usable signature"), but nothing enforces that for a layout. Selection by a script
needs `TISRegisterInputSource`, then the enabled-sources preference entry, then `TISSelectInputSource` from a
process started after the entry was written. That order has passed in two runs of four jobs each. No sign-out is
needed for a scripted install; the path through System Settings was not exercised.
