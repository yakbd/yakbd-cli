# Getting a layout back from an installed DLL

A Windows keyboard layout is a DLL file in `C:\Windows\System32`. If you have lost the `.klc` or `.kbl.json` source
of a layout that is still installed somewhere, yakbd can read that DLL and write an editable `.kbl.json` file whose
build gives the same key tables again. This works for DLLs built by MSKLC, by yakbd and by other tools that write
the usual layout DLL format.

Only recover layouts you have the right to: your own, or ones whose licence allows it. The layouts that come with
Windows are Microsoft's.

## 1. Find the DLL

On the Windows PC where the layout is installed, open a Command Prompt and search the registered layouts for the
name you see in the language list (here "My Layout"):

    reg query "HKLM\SYSTEM\CurrentControlSet\Control\Keyboard Layouts" /s /f "My Layout" /d

Each match prints a key named after the layout's eight-digit identifier, for example `...\Keyboard Layouts\a0000409`.
Ask that key for its file name:

    reg query "HKLM\SYSTEM\CurrentControlSet\Control\Keyboard Layouts\a0000409" /v "Layout File"

The value, for example `kbdmine.dll`, is a file in `C:\Windows\System32`. Copy it somewhere you can work with it,
for example `copy C:\Windows\System32\kbdmine.dll %USERPROFILE%\Documents\`. On 64-bit Windows this is the 64-bit
build (x64, or ARM64 on an ARM PC); either one is fine.

Note the last four digits of the identifier (`0409` above): they are the layout's language id, which the DLL
itself does not record.

## 2. Convert it

Copy the DLL to a machine with `yakbd` (any operating system) and run:

    yakbd convert -langid 0409 kbdmine.dll kbdmine.kbl.json

`-langid` gives the language id from step 1; without it the file has none, and a later build asks for one. The
report lists what the DLL could not tell:

    0 error(s), 2 warning(s), 0 accepted, 0 info; 2 loss(es), 0 default(s) used
    kbdmine.dll: warning (loss): version string InternalName, version string OriginalFilename, version string ProductName [dll.resourceDropped]
    kbdmine.dll: warning (loss): a DLL holds no registration data: klid, layoutId, layoutText, productCode, customLanguageName are not set [dll.registrationMissing]

Both warnings are expected. The registration (the identifier and the name Windows shows) lives in the registry,
not in the DLL, so the installer chooses them again by its default rules. Everything else the DLL holds is in the
file: every key on every layer, dead keys, ligatures, Caps Lock behaviour, key names, modifier settings and flags,
and the layout's name and copyright from the DLL's resources (its company name goes to `metadata.attribution`). What the reader accepts and how it maps each
part is in [`dll-import.md`](dll-import.md).

## 3. Check it and go on

    yakbd validate -windows kbdmine.kbl.json     # the file is valid and builds for Windows
    yakbd convert kbdmine.kbl.json kbdmine.klc   # a .klc file again, where the .klc format can hold it

From here the file is an ordinary `.kbl.json` file: edit it, build it with `yakbd compile`, or make an installer
with `yakbd installer`. To replace the installed layout with the rebuilt one, follow the side-by-side steps in
[`migrating-from-msklc.md`](migrating-from-msklc.md) §4: the installer will not overwrite a DLL it did not install.

The recovered file states what the DLL states, which is more than a `.klc` file usually does: the virtual key of
every scan code, the order of the tables, the flags. A `.klc` file cannot hold all of that: when the DLL relies
on such details (rows for keys no position has, unusual modifier bits or row attributes, scan codes without a
key), writing a `.klc` file stops with `klc.notRepresentable` errors that list them, and you keep working with the
`.kbl.json` file. Add `-msklc` to write a `.klc` file meant for MSKLC's editor.

## 4. Limits

- A DLL holds the compiled layout, not your source: comments and the formatting of your `.klc` file are gone.
- The keyboard type stored in the DLL is not kept unless you add `-keep-keyboard-type`.
- Some layout DLLs also export code that picks other layout DLLs (dispatchers). That code is not read
  (`dll.dispatcherNotRead`); the DLL's own table is.
- A file that is not a readable layout DLL is refused with `dll.malformed` and the reason. The reader checks every
  offset and size and never runs code from the DLL.
