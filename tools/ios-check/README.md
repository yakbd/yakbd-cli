# iOS keyboard check

Proves, on GitHub's Mac runners and without an Apple account, that an iOS app with one custom keyboard extension
builds unsigned, installs in the simulator, can be enabled without the Settings app, and types the expected text
when a UI test taps its keys. The workflow `.github/workflows/ios-check.yml` (manual only) runs `run.sh` in three
jobs: `macos-15` with Xcode 16.4, `macos-15` with the newest Xcode 26, and `macos-26` with its newest Xcode. Each
job tests every iOS simulator runtime its Xcode can use and writes one `summary.txt`; a last job joins them into
the artifact `summary` (`summary.txt`, each line with the job's name after the status).

Everything here is our own code and text. The keyboard and its layout file are a small format of this check, not
the touch runtime file of the yakbd iOS target.

## What run.sh does

1. Lists the installed Xcodes and the iOS runtimes (`INFO` lines), picks the Xcode (`IOS_CHECK_XCODE`: a version,
   a version prefix such as `26`, or `latest`) through `DEVELOPER_DIR`, and keeps the runtimes that are available
   and not newer than that Xcode's simulator SDK (`IOS_CHECK_RUNTIMES` narrows them).
2. Copies this folder and one case (`cases/<case>/layout.json`, `expect.tsv`) into a build folder and generates the
   Xcode project with XcodeGen (`project.yml`; the generated `project.pbxproj` is kept as `<case>-project.pbxproj`).
3. `xcodebuild build-for-testing` for the simulator, unsigned: `CODE_SIGNING_ALLOWED=NO`, or with
   `IOS_CHECK_SIGNING=adhoc` the ad-hoc identity `-`. Checks that the app holds `PlugIns/YakCheckKeyboard.appex`
   with the keyboard extension point.
4. Per runtime: a fresh iPhone simulator (`simctl create`), boot, `simctl install`, then
   - the hosted unit test `YakCheckUnitTests` (the weaker level, below);
   - enabling the keyboard: `simctl spawn <udid> defaults write -g AppleKeyboards -array "en_US@sw=QWERTY;hw=Automatic"
     org.yakbd.check.keyboard` and `defaults write -g AppleKeyboardsExpanded -bool true`, a reboot, and the value
     read back (`enable-default`);
   - the UI test `YakCheckUITests`: launches the app, taps its text field, taps the system next-keyboard key until
     the check keyboard's first key appears (`reach`), then types every case by tapping keys, which XCUITest sends as
     touch events at the keys' places, and reads the field back (`ui:<case>`).
   The simulator is deleted afterwards. Before the first boot the Simulator preference `ConnectHardwareKeyboard` is
   set to false, and the UI test is one method with one app launch, both because the simulator can otherwise
   connect the Mac's keyboard and hide software keyboards.

## summary.txt

One line per check: `PASS|FAIL|INFO <case>/<check> [: detail]`, with `<case>/iOS-<version>/<check>` for the checks
per runtime. A typing failure gives what came out and what was expected as code points. Times: `build`, `unit-run`,
`ui-run` and `time` (everything for one runtime). Next to it in the artifact: the build and test logs, the
`.xcresult` bundles (screenshots of the keyboard when reached and on each failure), a filtered simulator log per
runtime, the runtime list and the generated project.

## The two levels

- **`ui:<case>` is the bar**: iOS runs the extension as the system keyboard of another app's text field, enabled
  through the system's own keyboard list; the test only taps the screen and reads the field.
- **`unit-weaker:<case>` is weaker**: the extension's sources are compiled into a unit-test bundle hosted in the app,
  its controller gets a fake `UITextDocumentProxy` (a subclass overrides `textDocumentProxy`), and the test sends
  the buttons' actions directly. It proves the key logic and the layout file, not that iOS loads, enables or shows
  the extension. Report it as such, never as the bar.

## Cases

`cases/<case>/layout.json`: rows of keys (`id`, `label`, `output`, `special` = `backspace`, `space` or `globe`,
`longPress` choices) and `deadKeys` (dead key text → next text → composed text). Each key's `id` is its
accessibility identifier; long-press choices are `lp-<key id>-<n>`, n from 1. A dead key types its own character at
once; the next key replaces it through the text document proxy (`documentContextBeforeInput`, `deleteBackward`,
`insertText`) when a composition exists, and a space leaves it. The globe key shows only when
`needsInputModeSwitchKey` is true.

`cases/<case>/expect.tsv`: `name<TAB>steps<TAB>expected`. A step is a key id (tap) or `long:<key id>=<choice id>`
(hold one second, then tap the choice). In the expected text `\s` is a space, `\\` a backslash and `\u{HEX}` any
code point; lines starting with `#` are comments. The first step of the first case must be a key of the check
keyboard: the UI test waits for it to know the keyboard is up. Give a case an output the system keyboard cannot
type at that place (here `k-Q` types `æ`), so typing with the wrong keyboard cannot pass.

## What waits for an Apple account

Nothing in this check needs one. The account (the builder's own) is needed for: installing on a device (a free
Apple ID allows 7-day installs on up to 3 devices; a paid membership for longer), TestFlight, the App Store and
distribution signing. None of that is tested here; a device install is the next level above this check.

## Runs

(filled in from the workflow runs)
