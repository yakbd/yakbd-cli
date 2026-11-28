# Releases: how they are made and checked

A release of yakbd is built only by the workflow [`.github/workflows/release.yml`](../.github/workflows/release.yml)
on GitHub-hosted runners, from the commit it is started on. Nobody builds release files on their own machine. The
workflow leaves a **draft** release; a maintainer reads it and publishes it.

## 1. What a release contains

For version `V` (for example `0.1.0-preview.3`; the tag is `vV`):

| file | what it is |
|---|---|
| `yakbd-cli-setup.exe` | the Windows installer of YAKBD CLI: one 32-bit program carrying `yakbd.exe` for x86, x64 and arm64; it installs the one for the PC into `Program Files\YAKBD\CLI` and, by default, adds it to the PATH ([`installer-cli.md`](installer-cli.md) §12). The editor's release has `yakbd-gui-setup.exe`, made from this release with the same tool |
| `yakbd-linux-amd64.tar.gz`, `yakbd-linux-arm64.tar.gz` | `yakbd` for Linux on x86-64 or ARM64, `LICENSE`, `NOTICE` |
| `yakbd-macos.tar.gz` | `yakbd` for macOS as one universal binary (x86-64 and arm64 slices, each the plain Go build), `LICENSE`, `NOTICE` |
| `SHA256SUMS` | the SHA-256 of every other file, in the format of `sha256sum` |

Every archive unpacks into the current folder (`tar -xzf FILE`) with `yakbd` executable. There is no Windows archive
of `yakbd.exe` (maintainer decision, from 0.1.0-preview.3 on): `yakbd-cli-setup.exe` is the Windows download, and the
`yakbd.exe` it installs is the release build. To check that the editor's `yakbd-gui-setup.exe` carries the released
`yakbd.exe`, see [`installer-cli.md`](installer-cli.md) §12.1. Up to 0.1.0-preview.2 a release had
zips of `yakbd.exe` and four bare Linux and macOS binaries instead of the archives, and up to 0.1.0-preview.3
the archives had the version in their names (`yakbd-V-macos.tar.gz`); from the next release on every file name is
version-free, so `releases/latest/download/<name>` always gets the newest. `go run ./tools/instbundle -assets DIR`
then needs `-version V`.

The universal macOS binary is joined in Go (`tools/release/macho.go`: the universal-binary header Go's `debug/macho`
reads, each slice 16 KiB-aligned and unchanged, so the arm64 slice keeps the ad-hoc code signature the Go linker
gives it), not with Apple's `lipo`, so that every file is built on Linux and rebuilt identically anywhere. The
release workflow's `macos` job runs it on a macOS runner before the draft is made: `lipo -archs` and
`lipo -verify_arch` name both slices, `codesign --verify` accepts the arm64 slice, and `yakbd version` prints the
version (on x86-64 through Rosetta 2 when the runner has it). It is unsigned and not notarized, so macOS may refuse
to open it from Finder; from a terminal, remove the download mark with `xattr -d com.apple.quarantine yakbd`.

`yakbd version` prints `yakbd V` and the uninstall entry's `DisplayVersion` is `V`. Windows version resources hold
four numbers: they get the leading numbers of `V` (`0.1.0.0` for `0.1.0-preview.3`) and their version strings `V`.
A semantic-version pre-release (such as a preview) and the final release of the same numbers count as the same version when one is installed over the
other ([`installer-cli.md`](installer-cli.md) §6). The
arm64 build of `yakbd.exe` has so far been tested on Windows on ARM in emulation only.

## 2. How a release is made

1. Set the tool's version in `internal/cli/cli.go` (`Version`, the numbers, and `PreRelease`, the suffix after `-`
   or empty) to the release's version, commit and push to `main`, and wait for CI to pass.
2. Start the workflow: in the repository, Actions > release > Run workflow, or

       gh workflow run release.yml -R yakbd/yakbd-cli -f version=0.1.0-preview.3

   Inputs: `version` (required; it must equal the tool's own version, `0.1.0-preview.3` today, which `go run ./tools/release -version V
   -check` checks; a version containing `-rehearsal.` is exempt, for trying the workflow out).
3. The workflow, in three jobs:
   - **build**: checks that the tag does not exist yet; `gofmt`, `go vet`, `go test`; checks that the committed
     installer stub `installer/stub-386.exe` equals a fresh build ([`installer.md`](installer.md) §6); builds every
     file with `go run ./tools/release`, then a second time from a fresh copy of the source in another folder with an
     empty build cache, and fails unless both are identical byte for byte;
   - **macos**: runs the universal macOS binary on a macOS runner (§1);
   - **release**: writes `SHA256SUMS`, creates the build
     provenance attestation for every file (`actions/attest-build-provenance`), pushes the tag `vV` on the built
     commit and creates the draft release with all files. A preview (`x.y.z-preview.N`) is a normal release, not a
     pre-release (maintainer decision: a GitHub pre-release is not shown as the release on the repository page, and
     the previews are what people should download); it is made the latest release when it is published (step 4). A
     rehearsal (`-rehearsal.N`) and any other suffix are marked as a pre-release.
4. Check the draft's files. Its notes are a few lines from the workflow (the site's What's new page, the downloads, how to
   check them): add one or two lines on what is new at the top, and the release's entry on
   https://yakbd.github.io/releases (repository `yakbd/yakbd.github.io`). Then publish it, as the latest release for a
   preview or final version:

       gh release edit vV -R yakbd/yakbd-cli --draft=false --latest

   or publish it on the web page with the latest-release option on. Releases of `yakbd/yakbd-cli` are immutable (a repository setting): once published,
   the tag and the files cannot change, so a mistake means a new version, not a fix in place.

Do not push a change to `.github/workflows/` while the workflow runs: GitHub then refused (seen once) the workflow's own push of
the tag (a token without the `workflows` permission may not create a ref whose workflow files differ from the
branch), and the run fails before the draft is made; start it again.

Removing a draft that will not be published, with its tag:

    gh release delete vV -R yakbd/yakbd-cli --yes --cleanup-tag

**With release immutability on** (GitHub Docs, "Immutable releases" and "Managing releases in a repository", read
2026-11-27): immutability applies when a release is published, not to drafts. A failed run leaves at most the pushed
tag `vV` (if it failed after the tag step) or the tag and an unpublished draft; neither is immutable, and both are
removed with the command above (the docs do not discuss drafts explicitly; tested here with immutability on, 2026-11-27: the rehearsal draft `v0.1.0-rehearsal.7` and its tag were deleted with that command, and the tag was gone). Once a
release is published, its tag cannot be moved or deleted while the release exists, and after deleting the release the
tag name can never be used again, so a mistake found after publishing means a new version.

### Reproducible builds

`tools/release` (through `internal/relbuild`) builds with `-trimpath -buildvcs=false`, `-ldflags "-s -w
-buildid="`, `CGO_ENABLED=0`, `GOTOOLCHAIN=local`, an empty `GOFLAGS` and fixed `GO386`, `GOAMD64` and `GOARM64`, so
the files depend only on the source and the Go version (the workflow uses the Go version recorded in
`installer/stub-386.version`). The archives have fixed times, owners and modes, and the gzip header no name or time. Anyone can rebuild a release and compare:

    git clone https://github.com/yakbd/yakbd-cli && cd yakbd-cli && git checkout vV
    go run ./tools/release -version V -o ../rebuilt     # with the Go version of installer/stub-386.version
    diff ../rebuilt/SHA256SUMS SHA256SUMS                # the release's SHA256SUMS

## 3. How to check a downloaded file

**Checksum.** Download `SHA256SUMS` with the file. Linux and macOS: `sha256sum -c --ignore-missing SHA256SUMS`
(macOS: `shasum -a 256 -c --ignore-missing SHA256SUMS`). Windows PowerShell:

    (Get-FileHash .\yakbd-cli-setup.exe).Hash.ToLower()     # must equal the line for yakbd-cli-setup.exe in SHA256SUMS

**Build provenance.** Every file of a release has an attestation that names the repository, the workflow and the
commit that built it. With the [GitHub CLI](https://cli.github.com):

    gh attestation verify yakbd-cli-setup.exe -R yakbd/yakbd-cli

It succeeds only for a file built by a workflow of `yakbd/yakbd-cli`; the output names the workflow file and the
commit. Add `--signer-workflow yakbd/yakbd-cli/.github/workflows/release.yml` to require the release workflow.

## 4. Signing

Releases are not code-signed, until further notice: no free code-signing certificate is available to the project
([`code-signing-policy.md`](code-signing-policy.md)). The checksum and the attestation (§3) are how a download is
checked.
