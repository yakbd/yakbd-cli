# Releases are not code-signed

No file of a YAKBD release is code-signed: not the Windows installers (`yakbd-cli-setup.exe`,
`yakbd-gui-setup.exe`), not the programs they install, not the macOS or Linux downloads (the macOS binary has only the ad-hoc signature
arm64 code needs to run), and not the layout installers that `yakbd installer` writes. Like most software on GitHub, YAKBD has no free code-signing certificate available to
it (the free open-source signing programme it applied to found that it does not qualify), so signing is dropped until further notice.

## How to check a download

Every release has a `SHA256SUMS` file and a build provenance attestation for each file, made by the release workflow
on GitHub-hosted runners:

    sha256sum -c --ignore-missing SHA256SUMS                     # Linux; macOS: shasum -a 256 -c --ignore-missing SHA256SUMS
    (Get-FileHash .\yakbd-cli-setup.exe).Hash.ToLower()           # Windows: compare with the line in SHA256SUMS
    gh attestation verify yakbd-cli-setup.exe -R yakbd/yakbd-cli  # GitHub CLI; -R yakbd/yakbd-gui for the editor

The attestation names the repository, the workflow and the commit that built the file. The exact steps, and how to
rebuild a release yourself and compare, are in [`release.md`](release.md) §2–§3.

## What the operating system shows

- **Windows.** SmartScreen shows "Windows protected your PC": check the file name, then choose **More info** →
  **Run anyway**. The administrator prompt is yellow and names no verified publisher. Where **Smart App Control** is on
  (some Windows 11 PCs), it blocks unsigned installers and cannot make an exception for one program. Details, and
  what was measured: [Windows security prompts](windows-security-prompts.md).
- **macOS.** The `yakbd` binary of the `.tar.gz` is not notarized: run it from a terminal after
  `xattr -d com.apple.quarantine yakbd`. For the editor app, Gatekeeper is expected to block the first start: open it once, then
  click **Open Anyway** in **System Settings › Privacy & Security**. A keyboard layout bundle holds no program code
  ([macOS](macos.md)).
- **Linux.** Nothing to click: unpack the archive and run it.

## Privacy

`yakbd`, its installer and the layout installers it writes contain no network code: they send nothing anywhere.
