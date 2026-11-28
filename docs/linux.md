# Linux (XKB)

yakbd builds a Linux keyboard layout from a `.kbl.json` file: an XKB symbols file with its registry entry (so that
GNOME, KDE Plasma and the other settings apps list it), a Compose file for its dead keys, and an archive with an
install script for your own user, no root needed. It also converts XKB symbols files (with their Compose file) to
`.kbl.json` and back. What every choice means is the kbl specification's `targets/xkb.md` (the build) and
`coverage/xkb.md` (the import), in [`yakbd/kbl-spec`](https://github.com/yakbd/kbl-spec).

## Build

    yakbd compile -target xkb -o out layout.kbl.json        # out/xkb/... and out/compose/...
    yakbd installer -target xkb layout.kbl.json             # <name>.tar.gz: the files, install.sh, README
    yakbd validate -target xkb layout.kbl.json              # the build's report, nothing written
    yakbd pin -target xkb layout.kbl.json pinned.kbl.json   # every decided value written into the file
    yakbd convert -to xkb layout.kbl.json symbols-file      # the symbols file alone (+ symbols-file.XCompose)

`compile` writes, for the layout name N (`targets.xkb.symbolsName`, by default `yakbd-` and the slug of
`metadata.id`):

| file | what it is |
|---|---|
| `xkb/symbols/N` | the symbols file: one section `basic` (or the variant), every key with its key type; for X11 a second section `basic-x11` when some key types several characters |
| `xkb/rules/evdev.xml` | the registry entry the settings apps read |
| `compose/N.XCompose` | the dead-key sequences, after `include "%L"` (only when the layout has dead keys) |
| `xkb/types/N-types`, `xkb/rules/evdev.post` | yakbd key types (only with `targets.xkb.customTypes`) |

Everything the file does not state comes from a default rule, listed in the report as `xkbDefault.<rule>`: the
names, the key name of each position (the table `targets/xkb-default-keyname.tsv`), Right Alt as AltGr (level 3),
the key type of each key, the dead keysym of each dead key, how characters are spelled, and the Compose file. The
rules are simple on purpose and often not what a layout wants: put the real choice into `targets.xkb`, or run
`pin` and edit the file. `-strict-defaults` makes every default an error.

**Key types and Caps Lock.** XKB decides per key how Shift, Caps Lock and AltGr pick a level, by the key's type.
The build gives each key the first stock type that types every state as the file says. When none does, usually
because Caps Lock must leave an AltGr letter alone (`þ` with Caps Lock and AltGr), the build uses the closest stock
type and reports `xkb.capsLockNotExpressible`; with `"customTypes": true` it writes a yakbd key type instead
(`YAKBD_12213434`: the level of each state), installed with the layout. Those types need libxkbcommon 1.13 or
later; older versions give the key a default type. A level that types nothing is written `VoidSymbol` (a
`NoSymbol` level would be filled from the system's key of the same name), and F1 to F12 and the number-pad operators
get xkeyboard-config's `CTRL+ALT` type, which keeps the system's Control+Alt level (virtual terminals, grabs).

## Install and select

    tar -xzf <name>.tar.gz && <name>/install.sh          # remove again: <name>/install.sh --uninstall

The script adds the layout's section to `~/.config/xkb/symbols/N` and the layout to `~/.config/xkb/rules/evdev.xml`,
each between marker lines named after the layout (`N`, or `N(V)` for a variant V, so that several variants of one
layout can be installed side by side), copies the key types, and puts the dead-key sequences where every program finds
them: a marked block in the file libxkbcommon and Qt read (`$XCOMPOSEFILE`, else `~/.config/XCompose` or
`~/.XCompose`) and in the one GTK 4 reads (`~/.config/gtk-4.0/Compose`, else `~/.XCompose`; GTK never reads
`~/.config/XCompose`), keeping a copy of each as `<file>.yakbd-backup`; where no such file exists, a new `~/.XCompose`. Uninstall removes exactly its own blocks and files,
and a symbols file once no other variant's block is left in it. It then says how to select the layout:

- **GNOME**: Settings › Keyboard › Input Sources, or `gsettings set org.gnome.desktop.input-sources sources
  "[('xkb', 'us'), ('xkb', 'N')]"`.
- **KDE Plasma**: System Settings › Keyboard › Layouts.
- **sway**: `input type:keyboard xkb_layout N` in `~/.config/sway/config`.
- **X11 sessions** do not read `~/.config/xkb`: `setxkbmap -I"$HOME/.config/xkb" -layout N -print | xkbcomp
  -I"$HOME/.config/xkb" - "$DISPLAY"` loads it for the running session (`-variant basic-x11` for a layout with
  several characters on one key, see below).

The settings apps may list a new layout only after you sign out and in again; programs read the Compose file when
they start.

## Import

    yakbd convert -from xkb -compose ~/.XCompose ~/.config/xkb/symbols/mine mine.kbl.json
    yakbd convert -from xkb -variant nodeadkeys /usr/share/X11/xkb/symbols/fo fo-nodeadkeys.kbl.json

XKB symbols files have no suffix, so `-from xkb` names the format. `-variant` picks a section (default: the file's
default section), `-include` lists the folders of the files an `include` names (default: the input's folder), and
`-compose` gives the Compose file whose sequences become the dead keys' entries; without it the dead keys come
without entries (`xkb.deadKeyNoCompose`), since the system's sequences are not part of the layout. Group 1 is read,
in every combination of Shift, Caps Lock, AltGr and level 5, through the key types of xkeyboard-config.

## What Linux cannot hold, or not everywhere

- **Several characters on one key** type in libxkbcommon and Qt; GTK 4 types nothing, and an X11 server refuses
  the whole layout (measured with Xvfb 21.1: `XkbSetMap` fails). Such a build adds a second section to the symbols
  file, `basic-x11` (or `<variant>-x11`), in which those keys type nothing; the X11 command the install script
  prints loads that section (`xkb.multiCharacterOutput`).
- **Keys Linux has no key for**: `SysRq` and `Break` are levels of Print Screen and Pause in XKB. Every layout
  converted from a `.klc` file binds `Break` (MSKLC adds that row); the build leaves it out with a warning
  (`xkb.positionWithoutKey`). A position no position table names still needs `targets.xkb.keys.<p>.keyName`
  (`xkb.keyNameMissing`).
- **Dead keys** need an input method that reads your Compose file: libxkbcommon, Qt 6, GTK 4 on X11 and weston do;
  GTK 4 on sway, KDE Plasma and GNOME without IBus drop them unless `GTK_IM_MODULE=simple` is set
  (`xkb.deadKeyStacks`). What a key without a sequence types after a dead key is up to the input method
  (`xkb.deadKeyNoMatchNotCompiled`).
- **Control and Alt layers** are not built: Linux programs read those shortcuts themselves
  (`xkb.controlAltLayerDropped`). The number-pad keys get xkeyboard-config's `KEYPAD` type; Shift with Num Lock on gives
  the binding with xkeyboard-config 2.48 and later, the navigation key with 2.42 and 2.46 (`xkb.numLockKeyReduced`).
- **Letters Caps Lock leaves lower case** type upper case in GTK 4 on X11, whatever the key type (`xkb.capsLockStacks`).
- **A variant of a system layout** (`symbolsName` such as `fo` with a `variant`): the user file hides the system
  layout, and libxkbcommon before 1.13.2 gives the variant for the layout alone (`xkb.systemLayoutShadowed`).
- **UTF-8 characters in the symbols file** (`"characters": "utf8"`) need libxkbcommon 1.9 and do not load in X11;
  the default spells keysyms by name, which every version reads.

Measured on Debian 13, Ubuntu 26.04, Fedora 45 and Arch Linux, and under X11, weston, sway, KWin and mutter in
containers, by `tools/xkb-check/` (its README records the runs).
