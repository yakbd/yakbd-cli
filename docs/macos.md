# macOS

yakbd builds a macOS keyboard layout from a `.kbl.json` file: a `.keylayout` file inside a layout bundle, and a
zip to hand out. It also converts `.keylayout` files to `.kbl.json` and back. What every choice means is the kbl
specification's `targets/macos.md` (the build) and `coverage/macos.md` (the import), in
[`yakbd/kbl-spec`](https://github.com/yakbd/kbl-spec).

## Build

    yakbd compile -target macos layout.kbl.json            # ./<name>.bundle
    yakbd compile -target macos -o out layout.kbl.json     # out/<name>.bundle
    yakbd installer -target macos layout.kbl.json          # <name>.zip: the bundle and README.txt
    yakbd convert layout.kbl.json layout.keylayout         # the .keylayout file alone
    yakbd convert layout.keylayout layout.kbl.json         # a .keylayout file into .kbl.json
    yakbd validate -target macos layout.kbl.json           # the build's report, nothing written
    yakbd pin -target macos layout.kbl.json pinned.kbl.json

The bundle is `<name>.bundle/Contents/Info.plist` with `Resources/<name>.keylayout`, the icon `Resources/<name>.icns`
when the file has one (`targets.macos.icon`), and `Resources/<language>.lproj/InfoPlist.strings` for each localised
name. Any computer builds it; no Apple tool is needed.

Everything the file does not state comes from a default rule, and each one is listed in the report as
`macosDefault.<rule>`: the names (from `metadata.name` and `metadata.id`), the bundle and input-source ids
(`org.yakbd.keyboardlayout.<id>`), the keyboard id (a hash of the input-source id), the key code of each position
(the table `targets/macos-default-keycode.tsv`), Return, Tab, the arrows and the other standard keys, what
Command and Control combinations type, and the dead-key state names. The rules are simple on purpose and often
not what a layout wants: put the real choice into `targets.macos` (or run `pin` to write every decided value
into the file and edit it there). `-strict-defaults` makes every default an error.

`compile -emit-expectations` also writes `expect.tsv`, what the layout must type for each key, derived from the
`.kbl.json` file, for the check on GitHub's Mac runners (`tools/macos-check/README.md`).

## Install and select

1. Copy the bundle into `~/Library/Keyboard Layouts` (you only) or `/Library/Keyboard Layouts` (every user of
   the Mac; an administrator's password is asked for). In Finder, Go › Go to Folder opens either folder.
2. In System Settings › Keyboard › Text Input › Edit, click +, find the layout under its language (or Others) and
   add it. If it is not listed, sign out and in again.
3. Select it in the input menu of the menu bar.

The zip's README says the same. A layout bundle holds no program code and is not signed. On GitHub's macOS 15 and
26 runners an unsigned bundle, also one marked as downloaded, was listed, selected and typed at once, with no
"Open Anyway" step and no sign-out (`tools/macos-check/README.md`, runs 46104797718 and 46105185134; that check
enables the layout by script, not through System Settings).

To remove a layout, remove it in Text Input, then delete the bundle from the folder.

## What a `.keylayout` cannot hold

Each of these is a loss in the report (kinds of SPEC §17.3; a file's `onLoss` can make any of them an error):

| loss | what happens |
|---|---|
| `macos.sideModifierWidened` | a layer for one side of Control or Option (`ctrlL`, `altR`, ...) works with either side |
| `macos.modifierUnmapped`, `macos.lockUnmapped` | a layer that needs a custom modifier or lock is left out; macOS selects key maps by Shift, Option, Control, Command and Caps Lock only (a custom held component can be the Command key: `targets.macos.modifiers.command`) |
| `macos.numLockIgnored` | number-pad keys always type their binding: macOS has no Num Lock |
| `macos.settingNotCompiled` | Shift turning Caps Lock off, and bidi marks on Shift+Backspace |
| `macos.markerNotCompiled`, `macos.transformNotCompiled` | markers, transforms and reorders; dead keys are compiled |
| `macos.deadKeyNoMatchExpanded` | (accepted) noMatch rules and a dead key pressed while another is pending are written as explicit states |
| `macos.deadKeyEndedByEmptyKey` | a key that types nothing (in that key map) ends a pending dead key on macOS and the dead key's terminator is typed, where the format keeps the dead key pending; reported once per build with dead keys |
| `macos.positionWithoutKey` | a bound position the macOS table has no key for but another target's table has (`Break`, which every layout converted from a `.klc` file binds, `SysRq`, `PrintScreen`, media keys): its bindings are left out; pin `keys.<p>.keyCode` to build them |
| `macos.keyCodeMissing`, `macos.keyCodeDuplicate`, `macos.outputUnencodable`, `macos.outputBeyondMaxout` | errors: no key code for a bound position, two positions on one code, U+0000 or a lone surrogate, an output longer than a pinned `maxout` |

Caps Lock is no loss: every Caps Lock combination gets its own key map, filled key by key. Outputs are
NFC-normalised at build time unless `settings.normalization` is `disabled` (macOS does not normalise while typing).

Importing a `.keylayout` reports what `.kbl.json` cannot say: other key map sets for other hardware (a JIS set:
`macos.keyMapSetDropped`), right-hand modifiers with key maps of their own (`macos.sideModifierDropped`), range
actions such as hex input (`macos.rangeActionDropped`), and dead-key shapes the model reads differently
(`macos.stateNotExpressible`, for example a dead key pressed twice, which on macOS types the accent and starts the
dead key again). Key codes no table row names become positions `MacKey<code>`.
