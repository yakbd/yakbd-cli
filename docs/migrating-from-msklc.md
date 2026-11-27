# Moving a layout from MSKLC to yakbd

You have a keyboard layout made with Microsoft Keyboard Layout Creator (MSKLC): a `.klc` source file, and
perhaps the layout installed on your PC from the installer MSKLC built. This page takes that `.klc` file to an
installed layout built by yakbd, explains what comes out differently, and shows an order of steps that never leaves
you without a working layout.

If you no longer have the `.klc` file, first get it back from the installed layout:
[`recover-a-layout.md`](recover-a-layout.md).

## 1. Three commands

`yakbd` runs on Windows, Linux and macOS; the installer it writes runs on Windows. In this example the file is
`my-layout.klc`.

    yakbd convert my-layout.klc my-layout.kbl.json
    yakbd validate -windows my-layout.kbl.json
    yakbd installer my-layout.kbl.json

1. `convert` reads the `.klc` file (UTF-16 as MSKLC saves it, or UTF-8) and writes the same layout as a `.kbl.json`
   file, the format yakbd works in. You can keep editing either file; the `.kbl.json` file can say more than a
   `.klc` file can (section 2).
2. `validate -windows` checks the file and builds it for Windows without writing anything, and prints the build's
   report. Without `-windows` it checks only the file itself.
3. `installer` writes `<dll name>-setup.exe`, for example `kbdmine-setup.exe`. That one program carries the builds
   for x64, x86 and ARM64 Windows and installs the right ones on the PC it runs on. Run it on Windows like any
   installer; it is unsigned, so read [`windows-security-prompts.md`](windows-security-prompts.md) first.

The first step is optional: `yakbd installer my-layout.klc` (and `yakbd validate -windows my-layout.klc`) read the
`.klc` file directly. Convert first when you want to change something the `.klc` format cannot hold, such as the
DLL name in section 3.

Every command prints a report and stops with an error before writing anything when the layout cannot be built.
Add `-report report.json` to any command to save the report as JSON.

## 2. Reading the report

The report has one line per thing that is not a plain copy of your file. A typical `.klc` gives lines like these:

    0 error(s), 1 warning(s), 17 accepted, 0 info; 3 loss(es), 15 default(s) used
    /positions/NumpadDecimal: warning (loss): NumpadDecimal has outputs with Shift; Windows turns Shift+number pad into navigation [windows.numpadShiftUnreachable]
    my-layout.klc: accept (loss): the rows MSKLC adds (control characters, Tab, number pad) are written explicitly for Backspace, Escape, ... [klc.impliedRows]
    /targets/windows: accept (defaulted): virtual keys from Table A for 19 position(s); 66 Table A position(s) added [windowsDefault.vk]
    ...

- **loss**: something of the layout that the output does not carry, or that Windows will not do. The name in
  brackets is the loss kind, listed in the specification's
  [loss kinds](https://github.com/yakbd/kbl-spec/blob/main/SPEC.md#173-loss-kinds) (SPEC §17.3).
- **defaulted**: a choice your file leaves open, made by one of the documented
  [default rules](https://github.com/yakbd/kbl-spec/blob/main/targets/windows.md). `yakbd pin my-layout.kbl.json`
  writes every such choice into the file, so you can see and change it.
- **severity**: `error` stops the command, `warning` is printed, `accept` is expected. A `.kbl.json` file decides
  this per loss kind in its `onLoss` member; `compile` and `installer` also take `-accept kind` for one run.

## 3. What differs from MSKLC, and why

yakbd builds the layout from your `.klc` file and from public documentation, not by copying what MSKLC does, so a
few things come out differently. Each one is in the report.

- **The keys your file does not mention.** MSKLC fills them from its own table; yakbd fills them from a table of
  keys Microsoft documents publicly ("Table A" in the specification). The two agree on every key of an ordinary
  keyboard. About 18 rare keys (old terminal keys such as Help and Clear, the Brazilian ABNT key next to right
  Shift, and three application launch keys) get no key code from yakbd unless your file names them; see
  [DECISIONS.md](https://github.com/yakbd/kbl-spec/blob/main/DECISIONS.md) D85. If your keyboard has such a key,
  name it in the layout.
- **The rows MSKLC adds by itself** (Ctrl characters, Tab, Escape, the number pad) are written out explicitly
  (`klc.impliedRows`); they type the same.
- **Files yakbd refuses.** yakbd stops with an error on a few `.klc` files MSKLC accepts: malformed lines MSKLC
  tolerates, and a key with Caps Lock behaviour (`SGCAP`) on a dead-key column, for which MSKLC's build silently
  loses Caps Lock characters. The error says which line or key and why.
- **The installer.** One `.exe` for every Windows architecture, including native ARM64 (MSKLC's packages have
  no ARM64 build). It upgrades an installed version in place, is listed under "Installed apps", takes the same
  command-line switches and exit codes as `msiexec` ([`installer-cli.md`](installer-cli.md)), and writes a log to
  `%TEMP%\yakbd-<dll name>-setup.log`. There is no `.msi`. Like MSKLC's, it is unsigned.
- **Names inside the DLL.** The DLL's version information takes the layout's name, `COMPANY`, `COPYRIGHT` and
  `VERSION` from your file; only the product name is yakbd's. MSKLC writes some of its own names there instead.
- **Text normalisation.** Characters are written in Unicode NFC unless the file sets
  `"normalization": "disabled"`; a `.klc` file whose characters are not all NFC gets that setting on import, so
  its output does not change.

**How close the two are.** We built a set of 58 synthetic test layouts (the specification's test corpus and
targeted cases with dead keys, Caps Lock variants, ligatures, AltGr and Kana states) with MSKLC 1.4 and with yakbd
on Windows 11 x64, and typed every key and modifier combination in 64-bit and 32-bit apps. MSKLC built 48 of them
and yakbd 32 (it refuses the others on purpose, as above). yakbd's builds typed as expected in all 32 cases in
both kinds of app; MSKLC's typed the same in 27 of 28 comparable cases in 64-bit apps (the x64 build of a layout
with a Kana state does not load) and in 28 of 28 in 32-bit apps. yakbd also read 132 of the 144 MSKLC-built DLLs
back to exactly the layout of their `.klc` file; the other 12 differ only where MSKLC's build loses Caps Lock
characters on a dead-key column or adds the AltGr flag for a Kana state.

## 4. Replacing an installed MSKLC layout safely

Install the yakbd build next to the old layout, check it, and only then remove the old one. That way you always
have a working layout.

yakbd's installer never overwrites a layout file it did not install. A `.klc` file names its DLL in the `KBD`
line, so a build of the same file has the same DLL name as the installed MSKLC layout, and its installer stops
with "A file ... exists already and is not this layout's ... Nothing was changed." To install both side by side,
give the yakbd build its own DLL name and a display name you can tell apart:

1. Convert: `yakbd convert my-layout.klc my-layout.kbl.json`.
2. In `my-layout.kbl.json`, change `targets.windows.dllName` (up to 8 letters, digits or `_`, starting with a
   letter, for example `kbdmine` → `kbdminey`), and give the layout a name you can tell apart, for example
   "My Layout (yakbd)": in `metadata.name` and in every entry of `targets.windows.strings.layoutName`, which the
   import fills from the `.klc` file.
3. `yakbd validate -windows my-layout.kbl.json`, then `yakbd installer my-layout.kbl.json`, which writes
   `kbdminey-setup.exe`.
4. On Windows, run `kbdminey-setup.exe`. It adds the layout to your input list.
5. Switch to the new layout (Windows key + Space) and type with it in a few apps, including the keys with dead
   keys, AltGr and Caps Lock that matter to you.
6. When it works, remove the MSKLC layout in Settings → Apps → Installed apps (its entry has the name you gave it
   in MSKLC), and remove it from your input list in Settings → Time & language → Language & region if it is
   still listed.
7. If you want the old DLL name back later, uninstall the yakbd build, change `dllName` back and install again;
   once MSKLC's file is gone, the name is free.

If you skip the side-by-side step, remove the MSKLC layout first and then run an installer built straight from the
`.klc` file.

To roll back at any point, uninstall the yakbd build under "Installed apps" (or run `kbdminey-setup.exe /x`); the
MSKLC layout is untouched until you remove it yourself.

## 5. If something types differently

Open an issue with the `.klc` file (or the `.kbl.json` file), the report saved with `-report report.json`, the
installer log from `%TEMP%`, and your Windows version and architecture: see ["Reporting a layout bug"](https://github.com/yakbd/.github/blob/main/CONTRIBUTING.md#reporting-a-layout-bug).
