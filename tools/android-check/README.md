# Android check

Proves on GitHub's Linux runners (Android emulator with KVM), with our own small test files only, what the
Android targets rely on: a code-free keyboard-layout APK installs and is listed; a layout can be chosen for a
physical keyboard without the Settings screens; key codes from a real (uinput) keyboard go through the chosen
`.kcm`; and an on-screen keyboard (input method) can be enabled, tapped and read back headless. The workflow is
`.github/workflows/android-check.yml` (`workflow_dispatch` only; inputs `cases` and `apis`).

## Files

| file | what |
|---|---|
| `kcm/` | the code-free layout APK: manifest with `android:hasCode="false"`, a receiver for `android.hardware.input.action.QUERY_KEYBOARD_LAYOUTS` whose class does not exist, and three `.kcm` overlays. Built with `aapt2`, `zipalign` and `apksigner` only (`build.sh`). |
| `app/` | the check app (Gradle, Kotlin, no libraries): `MainActivity` (one text field; every change is written to `files/text.txt`), `CheckIme` (an input method with one subtype, language `eo`; writes its key rectangles to `files/keys.txt` and the physical keys it sees to `files/ime-keys.txt`), `LayoutTool` (an instrumentation that takes the shell's permissions and calls the hidden keyboard-layout methods of `InputManager`). |
| `build.sh OUTDIR` | builds `kcm.apk` and `app.apk`, both signed with a key generated for the run. |
| `run.sh OUTDIR APKDIR` | the driver: installs both APKs, selects the check keyboard, runs every case against the running emulator and writes `OUTDIR/summary.txt`. |
| `cases/*.case` | one case per file (`key=value` lines): `kind`, the keyboard (`device`, `vid`, `pid`), how its layout is chosen (`select`), the keys or taps, and the expected text (`expect`, `expect_ime`). |

The three layouts differ only in what Q types (`check_auto` ð, `check_api` þ, `check_root` æ); all three type ø on
AltGr+W and make E a dead acute accent, so `Q RALT+W E A` types `ðøá`, `þøá` or `æøá` and shows which layout was
used, that `ralt` applies and that a dead key composes.

## Kinds of case

- `listed`: `kcm.apk` installed, the package has no `HAS_CODE` flag, and `InputManager.getKeyboardLayouts()`
  lists the three layouts (descriptor `org.yakbd.androidcheck.kcm/org.yakbd.androidcheck.kcm.Layouts/<name>`).
- `uinput`: registers a keyboard with `adb shell uinput -` (own name, vendor 0x1209, product per case), chooses its
  layout (`select=auto`: nothing is done, the system picks by the keyboard subtype's language; `api`: `LayoutTool`
  calls `setKeyboardLayoutForInputDevice(identifier, user 0, check keyboard, its subtype, descriptor)`; `root`: the
  selection is written into `/data/system/input-manager-state.xml` (binary XML through `abx2xml`/`xml2abx`) while
  the system is stopped, then the system is started again), sends Linux key codes and reads the text field.
- `keyevent`: the control. `adb shell input keyevent` sends key codes from the virtual keyboard, whose map ignores
  every chosen layout; it must type plain `qea`.
- `tap`: taps the centres of the check keyboard's reported keys with `adb shell input tap` and reads the field.
- `imehw`: a uinput keyboard while the check keyboard is active: the key must reach `CheckIme.onKeyDown` (with the
  character of the chosen layout) and, passed on, the text field.

The current keyboard subtype stays fixed for the whole run (the check keyboard's only subtype), because Android 14
and later store the layout choice per keyboard, user and keyboard subtype.

## Android keyboard flavours

Checked against the AOSP documentation (source.android.com, "Key layout files", "Key character map files", "Input
device configuration files") and the platform sources listed in `CLEAN-ROOM.md`:

| flavour | an app can ship it |
|---|---|
| `.kcm` overlay in an APK (receiver + meta-data `android.hardware.input.metadata.KEYBOARD_LAYOUTS`) | yes; measured here (`listed`, `uinput`) |
| on-screen input method (`InputMethodService`) | yes; measured here (`tap`) |
| an input method handling physical keys (`onKeyDown`) | yes; measured here (`imehw`) |
| `.kl` key layout files | no: read only from `/odm`, `/vendor`, `/system` and `/data/system/devices/keylayout` |
| `.idc` input device configuration files | no: read only from system partitions and `/data/system/devices/idc` |
| the `.kcm` itself | five dead keys only (combining U+0300, U+0301, U+0302, U+0303, U+0308) |

## Results

Pending.
