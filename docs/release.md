# Releases: how they are made, checked and signed

A release of yakbd is built only by the workflow [`.github/workflows/release.yml`](../.github/workflows/release.yml)
on GitHub-hosted runners, from the commit it is started on. Nobody builds release files on their own machine. The
workflow leaves a **draft** release; a maintainer reads it and publishes it.

## 1. What a release contains

For version `V` (for example `0.1.0-preview.2`; the tag is `vV`):

| file | what it is |
|---|---|
| `yakbd-setup.exe` | the Windows installer: one 32-bit program carrying `yakbd.exe` for x86, x64 and arm64; it installs the one for the PC into Program Files and adds it to the PATH ([`installer-cli.md`](installer-cli.md) §12) |
| `yakbd-V-windows-x86.zip`, `-x64.zip`, `-arm64.zip` | `yakbd.exe` (byte for byte the one inside `yakbd-setup.exe`), `LICENSE`, `NOTICE`; needs no installation |
| `yakbd-V-linux-amd64`, `-linux-arm64`, `-darwin-amd64`, `-darwin-arm64` | the command-line tool for Linux and macOS (`chmod +x` after downloading) |
| `SHA256SUMS` | the SHA-256 of every other file, in the format of `sha256sum` |

`yakbd version` prints `yakbd V` and the uninstall entry's `DisplayVersion` is `V`. Windows version resources hold
four numbers: they get the leading numbers of `V` (`0.1.0.0` for `0.1.0-preview.2`) and their version strings `V`.
A semantic-version pre-release (such as a preview) and the final release of the same numbers count as the same version when one is installed over the
other ([`installer-cli.md`](installer-cli.md) §6). The
arm64 build of `yakbd.exe` has so far been tested on Windows on ARM in emulation only.

## 2. How a release is made

1. Set the tool's version in `internal/cli/cli.go` (`Version`, the numbers, and `PreRelease`, the suffix after `-`
   or empty) to the release's version, commit and push to `main`, and wait for CI to pass.
2. Start the workflow: in the repository, Actions > release > Run workflow, or

       gh workflow run release.yml -R yakbd/yakbd-cli -f version=0.1.0-preview.2

   Inputs: `version` (required; it must equal the tool's own version, `0.1.0-preview.2` today, which `go run ./tools/release -version V
   -check` checks; a version containing `-rehearsal.` is exempt, for trying the workflow out) and `sign` (default
   off, §4).
3. The workflow, in three jobs:
   - **build**: checks that the tag does not exist yet; `gofmt`, `go vet`, `go test`; checks that the committed
     installer stub `installer/stub-386.exe` equals a fresh build ([`installer.md`](installer.md) §6); builds every
     file with `go run ./tools/release`, then a second time from a fresh copy of the source in another folder with an
     empty build cache, and fails unless both are identical byte for byte;
   - **sign** (only with `sign`, §4);
   - **release**: puts the signed installer in place when there is one, writes `SHA256SUMS`, creates the build
     provenance attestation for every file (`actions/attest-build-provenance`), pushes the tag `vV` on the built
     commit and creates the draft release with all files. A preview (`x.y.z-preview.N`) is a normal release, marked
     as the latest (maintainer decision: a GitHub pre-release is not shown as the release on the repository page,
     and the previews are what people should download). A rehearsal (`-rehearsal.N`) and any other suffix are marked as a pre-release.
4. Check the draft (files, notes), then publish it, as the latest release for a preview or final version:

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
removed with the command above (the docs do not discuss drafts explicitly; tested here without immutability). Once a
release is published, its tag cannot be moved or deleted while the release exists, and after deleting the release the
tag name can never be used again, so a mistake found after publishing means a new version.

### Reproducible builds

`tools/release` (through `internal/relbuild`) builds with `-trimpath -buildvcs=false`, `-ldflags "-s -w
-buildid="`, `CGO_ENABLED=0`, `GOTOOLCHAIN=local`, an empty `GOFLAGS` and fixed `GO386`, `GOAMD64` and `GOARM64`, so
the files depend only on the source and the Go version (the workflow uses the Go version recorded in
`installer/stub-386.version`). The zips have fixed times and attributes. Anyone can rebuild a release and compare:

    git clone https://github.com/yakbd/yakbd-cli && cd yakbd-cli && git checkout vV
    go run ./tools/release -version V -o ../rebuilt     # with the Go version of installer/stub-386.version
    diff ../rebuilt/SHA256SUMS SHA256SUMS                # the release's SHA256SUMS (unsigned files only, §4)

## 3. How to check a downloaded file

**Checksum.** Download `SHA256SUMS` with the file. Linux and macOS: `sha256sum -c --ignore-missing SHA256SUMS`
(macOS: `shasum -a 256 -c --ignore-missing SHA256SUMS`). Windows PowerShell:

    (Get-FileHash .\yakbd-setup.exe).Hash.ToLower()     # must equal the line for yakbd-setup.exe in SHA256SUMS

**Build provenance.** Every file of a release has an attestation that names the repository, the workflow and the
commit that built it. With the [GitHub CLI](https://cli.github.com):

    gh attestation verify yakbd-setup.exe -R yakbd/yakbd-cli

It succeeds only for a file built by a workflow of `yakbd/yakbd-cli`; the output names the workflow file and the
commit. Add `--signer-workflow yakbd/yakbd-cli/.github/workflows/release.yml` to require the release workflow.

**Signature** (once signing has started, §4): `Get-AuthenticodeSignature .\yakbd-setup.exe` shows `Valid` and the
signer SignPath Foundation.

## 4. Signing

Only `yakbd-setup.exe` is to be signed, through [SignPath Foundation](https://signpath.org), which signs open-source
projects for free with its own certificate; the policy is [`code-signing-policy.md`](code-signing-policy.md). It is
**not enabled yet**: SignPath Foundation signs a project only after it has released the files it is asked to
sign, so the first releases ship `yakbd-setup.exe` unsigned.

The workflow is ready for it: with the input `sign` on, the job `sign` sends the unsigned `yakbd-setup.exe` (as a
workflow artifact) to SignPath with `signpath/github-action-submit-signing-request@v3`, waits until a person has
approved the request at SignPath, and passes the signed file to the release job, which puts it in the release and
computes `SHA256SUMS` and the attestation over it. To enable it, once the SignPath project exists:

| repository setting | kind | value |
|---|---|---|
| `SIGNPATH_API_TOKEN` | secret | an API token of a SignPath user allowed to submit signing requests |
| `SIGNPATH_ORGANIZATION_ID` | variable | the SignPath organization id |
| `SIGNPATH_PROJECT_SLUG` | variable | the project slug (the project is named after this repository's product, `yakbd`) |
| `SIGNPATH_SIGNING_POLICY_SLUG` | variable | the signing policy, e.g. `release-signing` |
| `SIGNPATH_ARTIFACT_CONFIGURATION_SLUG` | variable | the artifact configuration that signs `yakbd-setup.exe` inside the artifact zip |

and run the workflow with `sign` on. In SignPath, connect the project to this repository as a trusted build system
(GitHub-hosted runners only) and let only the `release` workflow on `main` submit requests.

Signing appends the signature and also changes the header (its checksum and certificate table entry), so a rebuild
(§2) matches the unsigned `yakbd-setup.exe` only. The unsigned file stays available as the artifact
`setup-unsigned` of the workflow run; every other file of the release is never signed and matches a rebuild
directly.
