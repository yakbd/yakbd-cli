# xkb-check: does a Linux layout type what it should?

`tools/xkb-check/` installs XKB layouts in a user's home folder (`~/.config/xkb/symbols/`,
`~/.config/xkb/rules/evdev.xml`, `~/.XCompose`), the way a yakbd build will, and types test cases through every
Linux display stack it can start in rootless Docker: no root, no `/dev/uinput`, no GPU, no KVM. Each key press goes
in **by key code** and through the stack's own keymap; a client that uploads its own keymap (the virtual-keyboard
protocol) would bypass the layout, so none is used. The typed text is read back from a GTK 4 and a Qt 6 window.

    tools/xkb-check/run.sh OUTDIR                       # builds the images, runs everything, writes OUTDIR/summary.txt
    XKB_CHECK_STACKS="distro x11" tools/xkb-check/run.sh OUTDIR    # a subset
    XKB_CHECK_NO_BUILD=1 tools/xkb-check/run.sh OUTDIR  # reuse the images of an earlier run

`XKB_CHECK_PREFIX` (default `xkb-check-`) prefixes every image and container name; `XKB_CHECK_DISTROS` overrides the
distribution list. A full run takes about 5 to 7 minutes once the images exist; the first image build takes several minutes more, mostly for the desktop image.

## summary.txt

One line per case: `STACK CLIENT LAYOUTDIR CASE PASS` or `... FAIL reason` (the reason says what was expected and
what came back, or which step did not start), one `version` line per image, and a `total` line. Logs of every
group (stack, client, injector) are under `OUTDIR/logs/`.

Many FAIL lines are known losses (see Results). `baseline.txt` holds the result lines of the run recorded below
(reasons cut off); `run.sh` lists every line that differs from it as `changed was:` / `changed now:` and counts them in
`CHANGED=` on the `total` line, so `CHANGED=0` means "same behaviour as recorded". A stack that prints nothing gets one
`FAIL no result lines` line. After an intended change, copy `OUTDIR/results.txt` to `baseline.txt` and update Results.

`inside.sh` (what runs in the containers) also reads `CASE_TIMEOUT` (seconds per group, default 40) and `KEY_GAP_MS`
(an extra pause after every key event); pass them with `docker run -e` when running one stack by hand:

    docker run --rm -u tester -v "$PWD/tools/xkb-check:/xkb-check:ro" xkb-check-desktop bash /xkb-check/inside.sh sway gtk

## Stacks

| stack | display server | how the layout is chosen | key presses by key code | clients |
|---|---|---|---|---|
| `distro(<image>)` | none | the layout name, through libxkbcommon's default include path | `xkbtype` (libxkbcommon state + Compose API) | `xkbtype`; plus `xkbcli list`, `compile-keymap`, `how-to-type` |
| `x11` | Xvfb `-noreset` | `setxkbmap -I ~/.config/xkb -print \| xkbcomp -I ~/.config/xkb - :1` | XTest (`xtestkeys`) | GTK 4 (X11), Qt 6 (xcb) |
| `sway` | sway, wlroots X11 backend in Xvfb | `input * xkb_layout` in `~/.config/sway/config` | XTest into Xvfb; sway maps the code with its own keymap | GTK 4, Qt 6 (Wayland) |
| `weston` | weston `--backend=x11` in Xvfb | the X root property `_XKB_RULES_NAMES` (see limits) | XTest into Xvfb | GTK 4, Qt 6 (Wayland) |
| `kwin` | `kwin_wayland --virtual` | `~/.config/kxkbrc` (`LayoutList`) | KWin's fake-input protocol, `keyboard_key` (`kwinkeys`) | GTK 4, Qt 6 (Wayland) |
| `mutter` | `mutter --headless --virtual-monitor` | systemd-localed X11Layout, answered by `localed` on a private system bus | mutter's remote-desktop D-Bus API, `NotifyKeyboardKeycode` (`rdkeys`) | GTK 4, Qt 6 (Wayland) |

Images: `images/distro.Dockerfile` (one per distribution: Debian stable, Ubuntu LTS, Fedora, Arch, each with its own
libxkbcommon) and `images/desktop.Dockerfile` (Arch Linux, the newest compositors). The helpers in `src/` are C,
built inside the images; KWin's protocol code is generated from the distribution's `fake-input.xml`.

## Adding a layout

A layout is a folder under `layouts/`:

- `xkb/symbols/<name>` and `xkb/rules/evdev.xml`: copied to `~/.config/xkb/`.
- `compose/XCompose`: installed as `~/.XCompose` (scenario `build`). The probe scenario also installs
  `compose/config-XCompose` as `~/.config/XCompose` and `compose/gtk4-Compose` as `~/.config/gtk-4.0/Compose`.
- `cases.tsv`: `case`, `layout`, `compose` scenario, `keys`, `expected` (tab-separated). Keys are XKB key names:
  `<AC01>` taps a key, `+<LFSH>` holds it down, `-<LFSH>` lets it go. Every key name must be in `keycodes.tsv`.

Cases with the same layout and scenario run in one session, each followed by Return; the text is split by lines.
Every case must leave the modifiers as it found them (Caps Lock off again).

The test layout `yakbdtest` covers levels 1 to 4 (Right Alt is level 3), one key per four-level key type
(`FOUR_LEVEL_ALPHABETIC`, `FOUR_LEVEL_SEMIALPHABETIC`, `FOUR_LEVEL`), a dead key with its Compose table, and a key with
two characters on one level. `yakbdtestutf8` is the same layout written with UTF-8 strings (libxkbcommon 1.9 and
later). The case `control-us` types with the stock `us` layout: when it passes and the `yakbdtest` cases do too, the
key presses went through the stack's own keymap.

## Layouts generated by yakbd

A folder `gen/<case>/` with a `layout.kbl.json` is built by the tool itself: `run.sh` builds yakbd (`go build`;
Go 1.26.8 on the host), runs `yakbd installer -target xkb` (the release archive) and `yakbd compile -target xkb
-emit-expectations -max-expectations 40`, and writes the unpacked archive with the generated `cases.tsv` to
`OUTDIR/gen/<case>/`, which the containers see as `/xkb-gen/`. A case may add `extra.tsv` (lines in the format of
`cases.tsv`, appended to the generated ones, also with other Compose scenarios) and a file `distro-only` (typed in the
`distro` stack only, for variants and keymap comparisons). Cases of a variant name their layout `NAME(VARIANT)`, which
`xkbtype` splits. The `distro` stack also runs `extra_checks` (lines `distro(<image>) xkbcli <case> <check>`). `inside.sh` installs such a layout with the archive's
own `install.sh` (so the install script is checked too), and the X11 stack adds its yakbd key types with `-types`.
The expectations come from the `.kbl.json` (the model: `resolve()` and the dead keys' entries), not from the built
files; `TestCheckCases` (`target/xkb`) checks that every case builds and presses only keys of `keycodes.tsv`, which
holds every row of the kbl spec's position table.

| case | what it checks |
|---|---|
| `gen-demo` | the spec example `xkb-caps-deadkeys`: levels 1 to 4, Caps Lock per key type (`FOUR_LEVEL_SEMIALPHABETIC`, `FOUR_LEVEL_ALPHABETIC`, and the yakbd type `YAKBD_12213434` that leaves Caps Lock out of AltGr: þ, not Þ), a dead key through the generated Compose file |
| `gen-multi` | several characters on one level (`ng`, `x` + U+0301): expected to type in libxkbcommon and Qt and to fail in GTK 4; the build adds the section `basic-x11` with those levels empty, which the x11 stack loads, so there only those levels are expected to fail (not yet run: `baseline.txt` holds the run before the X11 section, in which X11 refused the whole keymap); `FOUR_LEVEL_PLUS_LOCK` for keys that Caps Lock leaves alone |
| `gen-probe` | written for this check, with hand-written lines in `extra.tsv` (kbl spec `targets/xkb.md` §8): a key without a sequence and Backspace after a dead key (item 3), an entry whose result is `""` (item 8), a `numLock` position (type `KEYPAD`) with and without Num Lock and Shift (item 10), and the Compose scenarios `pre-both` and `pre-config` (item 7): `~/.config/XCompose` (and, for `pre-both`, `~/.config/gtk-4.0/Compose`) exist with a sequence of their own before `install.sh` appends its block; each outcome types something different, so a FAIL's `got` is the measurement |
| `gen-chars`, `gen-chars-utf8` | the same 202 characters over every row of rule `characters`, written as keysym names and as UTF-8 strings; `extra_checks` compares the two compiled keymaps key by key, so the names yakbd writes are checked against `xkb_utf32_to_keysym` (item 9) |
| `gen-var-a`, `gen-var-b`, `gen-var-sys` | variants `va` and `vb` of one layout installed side by side, then `va` removed (item 12), and a variant `yakbdv` of the system layout `fo` from the user path (item 5): `xkbcli list` and `compile-keymap` in `extra_checks`; `gen-demo` as the second of two layouts (`us,yakbd-fo-xkbdemo`) keeps its yakbd key type (item 4) |
| `gen-klc` | the repository's `testdata/klc/msklc-style.klc` converted by `yakbd convert` (rows MSKLC adds; `Break` has no XKB key: `xkb.positionWithoutKey`); the macOS check builds the same file |
| `gen-cldr` | CLDR release 49 `keyboards/3.0/mt.xml` converted (Unicode License v3, `LICENSE` beside it); the macOS check builds the same file |
| `gen-corpus` | xkeyboard-config's `fo(basic)` converted to `.kbl.json` with `testdata/xkb/test.XCompose` (MIT/X11 notices: `COPYING` beside it), renamed `yakbd-gen-fo`: a real layout with its includes inlined and its dead keys |

## Results

Run of 2026-11-28 with the generated cases (`XKB_CHECK_NO_BUILD=1`, about 9 minutes): 1930 result lines, 1732 PASS, 198
FAIL, no line of the hand-written layouts changed; the 1564 lines of `gen-demo`, `gen-multi` and `gen-corpus` were added
to `baseline.txt`. Every FAIL of a generated case is a loss the build reports:

- the yakbd key type `YAKBD_12213434` (`gen-demo` KeyT: AltGr+Caps Lock gives þ) types as built on every stack with
  libxkbcommon 1.13 (through `~/.config/xkb/rules/evdev.post`) and on X11 with `-types complete+<name>-types`; Debian
  13 (libxkbcommon 1.7) ignores the rules file and gives the key a default type (6 + 6 FAIL, `xkb.customKeyType`);
- dead keys from the generated Compose file type everywhere except GTK 4 on sway, KWin and mutter (`gen-demo` 4,
  `gen-corpus` 17 per stack, `xkb.deadKeyStacks`);
- several characters on one level type in libxkbcommon and Qt; GTK 4 types nothing (8 per Wayland stack), and Xvfb
  21.1 refuses the whole keymap of `gen-multi` (`XkbSetMap` BadValue, all 24 cases in both clients; the same file
  with the multi-keysym levels replaced by one keysym loads) (`xkb.multiCharacterOutput`).

The first run, of the hand-written layouts only:
Run of 2026-11-28 (`run.sh`, 5 minutes with the images already built): 366 result lines, 322 PASS, 44 FAIL; `baseline.txt`
holds them, with one correction: `x11 qt yakbdtest control-us` failed in that run because the injector focused a small
helper window of Qt; it now focuses the largest window, and a rerun of the x11 stack passed it. Every FAIL in
`baseline.txt` is a measured behaviour, not a harness fault; the table below is what a yakbd Linux build has to live with.

Versions measured in the containers:

| image | libxkbcommon | xkeyboard-config | UTF-8 symbols (1.9), measured | extension directory (1.13), by version only |
|---|---|---|---|---|
| Debian 13 "trixie" (`debian:stable`) | 1.7.0 | 2.42 | no: syntax error | no |
| Ubuntu 26.04 LTS (`ubuntu:latest`) | 1.13.1 | 2.46 | yes | yes |
| Fedora 45 (`fedora:latest`) | 1.13.1 | 2.48 | yes | yes |
| Arch Linux (`archlinux:latest`, also the desktop image) | 1.13.2 | 2.49 | yes | yes |

Desktop image: weston 15.0.1, sway 1.12, KWin 6.8.3, mutter and gnome-shell 51.1, GTK 4.24.2, Qt 6.12.1, Xvfb 21.1.

Per stack (`yakbdtest` unless named):

| stack | layout from the user path | levels 1-4, Caps per key type | dead key + `~/.XCompose` | two characters on one level | UTF-8 file |
|---|---|---|---|---|---|
| libxkbcommon (`xkbtype`), 1.13 | yes | yes | yes | yes | yes |
| libxkbcommon 1.7 (Debian 13) | yes | yes, but no ß to ẞ under Caps | yes | yes | rejected |
| X11 (Xvfb + xkbcomp), Qt | yes, via `setxkbmap -I` / `xkbcomp -I` | yes | yes | **no**: xkbcomp replaces the list with NoSymbol, the included `us` key shows (`=`) | **rejected by xkbcomp** |
| X11, GTK 4 | yes | Caps does **not** capitalise a `FOUR_LEVEL` letter (œ, ß) | yes | no (as above) | rejected |
| sway, weston, KWin, mutter: Qt 6 | yes | yes | yes | yes | yes |
| weston: GTK 4 | yes | yes | yes | **no**: nothing is typed | yes |
| sway, KWin, mutter: GTK 4 | yes | yes | **no**: the dead key is dropped, the base letter follows | **no**: nothing is typed | yes |

- Caps Lock and the stock key types: `FOUR_LEVEL_SEMIALPHABETIC` keeps (preserves) Lock on level 3, so Caps+AltGr
  still capitalises a level-3 letter with a case pair (þ gives Þ) everywhere. `FOUR_LEVEL` never consumes Lock, so
  libxkbcommon and Qt capitalise its letters under Caps too (œ gives Œ); GTK on X11 does not. A layout whose Caps Lock
  must leave level 3 alone, or leave a key alone, presumably needs a key type of its own that consumes Lock; neither
  such a type nor whether a file in the user path can bring one was tested.
- Compose files: with all three files present, GTK 4 takes `~/.config/gtk-4.0/Compose` (case `probe-file` types
  `3`), Qt and libxkbcommon take `~/.config/XCompose` (`2`, the order `$XCOMPOSEFILE`, `~/.config/XCompose`,
  `~/.XCompose`). With `~/.XCompose` alone (the `build` scenario, `include "%L"` first) both GTK's own Compose
  handling and Qt type the custom sequence and the system ones.
- GTK 4 on Wayland and dead keys: on sway, KWin and mutter, which offer the text-input protocol, GTK 4 hands keys to
  its Wayland input-method module; with no input method running the dead key is lost. With `GTK_IM_MODULE=simple`
  (checked once on sway) GTK's own Compose handling types all three dead-key cases. The same cases with 300 ms
  between key events (`KEY_GAP_MS=300`, sway) fail the same way, so it is not a timing effect. Why the Wayland module
  drops the key was not traced. A real GNOME session runs IBus,
  which this check does not cover.
- GTK 4 reads one keysym per key, so a level with several keysyms types nothing on every Wayland stack; Qt types
  both characters. A multi-character output is a reported loss for GTK and for X11.
- `xkbcli list` shows both layouts from the user `evdev.xml`, `xkbcli compile-keymap` and `xkbcli how-to-type` work on
  every distribution (on Debian only for the keysym-name file).


## Limits

- **No real input devices.** The settings each compositor reads for a physical keyboard are used where the backend
  allows it (sway's config, kxkbrc, locale1 for mutter), but key presses come from XTest into a nested backend,
  KWin's fake keyboard, or mutter's remote-desktop keyboard, never from libinput.
- **weston** runs nested in Xvfb, and its X11 backend ignores `weston.ini [keyboard]`: it compiles its keymap from the
  X root property `_XKB_RULES_NAMES`, which `xtestkeys names` sets. The keymap is still compiled by weston's own
  libxkbcommon from the user path. weston's `[keyboard]` section itself (DRM backend) is not tested.
- **sway and weston** take the modifier state from the X server, so Xvfb runs `us` with `lv3:ralt_switch`; a layout
  with other modifier keys needs the same modifiers on Xvfb.
- **KWin**: the nested X11 backend needs DRI3 1.2, which Xvfb lacks, so KWin runs with `--virtual`. KWin's own
  System Settings path (kxkbrc) is used.
- **GNOME**: the stack `mutter` is bare `mutter --headless`; it takes its keymap from systemd-localed's X11Layout,
  which `localed` answers on a private system bus (on a real system: `localectl set-x11-keymap`). The GNOME Settings
  path (`org.gnome.desktop.input-sources`) needs gnome-shell: `gnome-shell --headless` starts here (with a private
  system bus) and clients connect, but no window got the keyboard focus, and key presses from the remote-desktop API
  never reached a client (by hand: after an Escape key the window was active, and still no key arrived). The stack
  stays available as `XKB_CHECK_STACKS=gnome-shell` and fails today; it is not in the default run.
- **IBus and fcitx5** are not tested; neither are Plasma's or GNOME's input-method settings.
- The first key of a new mutter remote-desktop keyboard was seen to get lost; `rdkeys` taps Left Shift first.
- The X server must run with `-noreset`: otherwise it resets when its last client (`setxkbmap`, `xkbcomp`) leaves,
  and the keymap with it.
- Only Arch Linux has the desktops; the other distributions run the UI-free checks.

