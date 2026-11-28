# macOS layout check

Checks keyboard-layout bundles on a real Mac, GitHub's Mac runners, without anyone at the keyboard. The workflow
`.github/workflows/macos-layout-check.yml` (manual only) runs `run.sh` on `macos-15` and `macos-26`, once with
the bundles in `~/Library/Keyboard Layouts` and once in `/Library/Keyboard Layouts` (`MACOS_CHECK_PLACE=system`,
with sudo). Each job writes one `summary.txt`, uploaded as the artifact `macos-check-<runner>-<user|system>`
together with `hitoolbox.txt` (the `com.apple.HIToolbox` preferences before and after each pass) and shown on the
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

Until the layout is the current input source, `run.sh` repeats the checker in further passes, tagged in the
case name: `<case>/again` (a fresh process), `<case>/prefs` (after adding the layout to `AppleEnabledInputSources`
in the HIToolbox preferences) and `<case>/restart` (after restarting `cfprefsd` and the text-input agents). A
pass that a later pass repeats keeps its failures as `RETRY`. `MACOS_CHECK_WAIT` sets how long the checker
waits for a registered layout to be listed (60 s on the first pass).

## summary.txt

There is one line per check: `PASS|FAIL|SKIP|RETRY|INFO <case> <check> [: detail]`. Typing is `SKIP` while
the layout is not the current input source. A failure gives what came out and
what was expected, with code points. The checker never stops at the first failure. A 300-second deadline ends a
hung case with `FAIL <case> deadline`. When any line is `FAIL`, the artifact also holds `log.txt`, a filtered
`log show` of the last ten minutes, and the job fails.

## Adding a case

Make a folder `cases/<case>/` with:

- `<Name>.bundle/` holding `Contents/Info.plist` (with `KLInfo_<Name>` → `TISInputSourceID`, `TISIntendedLanguage`)
  and `Contents/Resources/<Name>.keylayout`. Every case needs its own input-source id and its own negative keyboard
  `id`, because all cases are installed side by side.
- `expect.tsv`: one expectation per line, `name<TAB>strokes<TAB>expected`. Strokes are virtual key codes
  (0 to 127) separated by spaces, each with optional `+shift`, `+option` or `+caps`. For example, `24 14` is two
  strokes, and `0+shift+option` is one. The expected text is UTF-8; `\s` is a space, `\t` a tab, `\\` a backslash,
  `\u{HEX}` any code point. Lines starting with `#` are comments.
- optionally a file `quarantine`: `run.sh` then marks the installed copy with `com.apple.quarantine`, as a
  browser does for a downloaded file.

No other change is needed. The cases run in name order.

## The cases here

- `check`: a US-like layout written for this check. It has two swapped keys (Y and Z), `ø` on the semicolon key,
  a two-character output (`ng`), Option outputs (`ß`, `æ`, `Æ`), a dead acute key on `=` (`é`, `á`, the terminator
  `´`), and a caps-lock table that differs from the shift table (caps + 1 gives `1`, not `!`).
- `quarantined`: the same layout under another name and id, installed with the quarantine attribute, to see
  whether Gatekeeper blocks an unsigned layout bundle that was downloaded.

## Locally

On a Mac, `tools/macos-check/run.sh OUT` does the same as the workflow. It installs into your own
`~/Library/Keyboard Layouts` and selects each test layout, so switch back afterwards.
