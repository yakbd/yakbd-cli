# Windows security prompts

Windows checks programs you download before it runs them. This page says what you can expect to see for the two
kinds of program yakbd gives you, and what you can do about it:

- `yakbd-cli-setup.exe` and `yakbd-gui-setup.exe`, the YAKBD installers (the CLI's and the editor's), downloaded from the
  [latest releases](https://github.com/yakbd/yakbd-cli/releases/latest) of the two repositories, and the programs they install;
- the layout installers (`<dll name>-setup.exe`) that `yakbd installer` writes for a layout. These are unsigned.

Statements below are marked **[measured]** when we tested them on Windows, and otherwise give the Microsoft page
they come from (read 2026-11-27). Windows changes these checks from time to time.

**Signing.** The 0.1 preview release ships `yakbd-cli-setup.exe` **unsigned**; you can check it with the release's
`SHA256SUMS` file and its GitHub release attestation (§3). The project applies to SignPath Foundation for a code
signing certificate right after that release, so signing starts with a later release
([`code-signing-policy.md`](code-signing-policy.md)).

## 1. The three checks

- **Smart App Control** (Windows 11). When it is on, Windows runs a program only if Microsoft's cloud service
  knows it as safe or it is signed with a certificate from a certificate authority that Microsoft trusts; anything
  else is blocked, and there is no way to allow one program: you can only turn Smart App Control off
  ([What is Smart App Control](https://support.microsoft.com/en-us/topic/what-is-smart-app-control-285ea03d-fa88-4d56-882e-6698afdb7003);
  [Smart App Control overview](https://learn.microsoft.com/en-us/windows/apps/develop/smart-app-control/overview)).
  It is on only on some PCs; Windows Security → App & browser control shows "On", "Evaluation" or "Off".
- **Microsoft Defender SmartScreen.** For a downloaded program it does not know, it shows "Windows protected your
  PC"; you can still choose "Run anyway". A signed program can also get a warning until its certificate or file
  has built up reputation from downloads, but the warning then shows the verified publisher's name
  ([SmartScreen reputation for apps](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation)).
- **User Account Control (UAC).** An installer that changes the whole PC asks for administrator rights. The prompt
  has a grey background for a program from a verified publisher and a yellow one for an unsigned program
  ([How UAC works](https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/how-it-works));
  an unsigned program does not show a verified publisher.

## 2. What you will see

| | Smart App Control on | SmartScreen | UAC |
|---|---|---|---|
| `yakbd-cli-setup.exe` once signed (a later release, see [`code-signing-policy.md`](code-signing-policy.md)) | expected to run, as it is signed by a trusted certificate authority (not yet tested) | expected: may warn while the certificate is new, naming SignPath Foundation as the publisher | expected: grey prompt, publisher SignPath Foundation |
| `yakbd-cli-setup.exe` of the 0.1 preview, and every layout installer | **blocked** **[measured]**: an unsigned program in the Downloads folder was blocked, with and without the download mark | "Windows protected your PC", with "Run anyway" | yellow prompt, no verified publisher |
| `yakbd.exe`, installed by `yakbd-cli-setup.exe` | unsigned, so treated like any unsigned program: expect it to be blocked (not yet tested) | not shown: it is not a download | none: `yakbd.exe` needs no administrator rights |

Smart App Control checks every program it starts, not only downloads
([SmartScreen reputation for apps](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation)).
Only `yakbd-cli-setup.exe` will be signed, so with Smart App Control on, the `yakbd.exe` it installs is expected to be
blocked like any unsigned program; this has not been tested yet.

The layout DLL itself is not stopped by Smart App Control: an unsigned layout DLL, installed by hand, loaded and
typed with Smart App Control on **[measured]**. Only the installer program is affected.

The layout installers stay unsigned on purpose, as MSKLC's are ([DECISIONS.md](https://github.com/yakbd/kbl-spec/blob/main/DECISIONS.md) D76). §4 shows how
an author can sign their own.

## 3. What you can do

**Check that the file is the one we published.** Every release lists a `SHA256SUMS` file and has a build
provenance attestation that ties each file to the source and the workflow run that built it. In PowerShell:

    Get-FileHash .\yakbd-cli-setup.exe          # SHA256 by default; compare with the line in SHA256SUMS
    Get-AuthenticodeSignature .\yakbd-cli-setup.exe   # once signing has started: Status Valid, signer SignPath Foundation

The GitHub CLI checks the attestation with `gh attestation verify`. The exact steps for both checks are in
[`release.md`](release.md).

**Build layouts without Windows.** You do not need Windows, or `yakbd.exe`, to build a layout: the Linux and macOS
builds of `yakbd` (the release's `.tar.gz` files, which also run in WSL) write the same layout installers. Only the
layout installer then runs on Windows.

**Run a file you trust.**

- SmartScreen: check the file name in the warning, then choose "Run anyway".
- The download mark: Windows notes that a file came from the internet; File Explorer → Properties → "Unblock", or
  `Unblock-File .\file.exe` in PowerShell, removes it
  ([Unblock-File](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.utility/unblock-file)).
  It does not help with Smart App Control, which blocked our unsigned test program without the mark as well
  **[measured]**.
- Smart App Control: it cannot make an exception for one program. Turning it off is a decision for the whole PC;
  turning it back on is possible from Windows Security on current Windows versions
  ([What is Smart App Control](https://support.microsoft.com/en-us/topic/what-is-smart-app-control-285ea03d-fa88-4d56-882e-6698afdb7003)).
  Rather than turning it off for a layout, ask its author for a signed installer (§4).

## 4. Signing your own layout installer

A layout installer has room for a standard Authenticode signature: the installer finds its own data from its PE
header, not from the end of the file, so a signature appended after `yakbd installer` has written the file should
not break it ([`installer.md`](installer.md) §5). The tests append bytes to the file and check that the installer
still reads itself; a file signed with a real certificate has not been tested yet. Sign the finished file as the last step:

1. Get a code-signing certificate from a certificate authority in Microsoft's Trusted Root Program, or use
   Microsoft's Artifact Signing service. Smart App Control only trusts certificates from such providers; a
   self-made certificate does not help
   ([Code signing for Smart App Control](https://learn.microsoft.com/en-us/windows/apps/develop/smart-app-control/code-signing-for-smart-app-control)).
2. Build the installer: `yakbd installer my-layout.kbl.json`.
3. Sign it with Microsoft's `signtool` (Windows SDK) or your provider's tool, with SHA-256 and a timestamp, for
   example `signtool sign /fd SHA256 /tr <your provider's timestamp URL> /td SHA256 /a kbdmine-setup.exe`
   ([SignTool](https://learn.microsoft.com/en-us/windows/win32/seccrypto/signtool)).
4. Check it: `Get-AuthenticodeSignature .\kbdmine-setup.exe` shows `Valid` and your name.

Do not change the file after signing. Rebuild and sign again for every new version; SmartScreen reputation carries
over between versions signed by the same publisher.
