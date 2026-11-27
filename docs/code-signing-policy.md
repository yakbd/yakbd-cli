# Code signing policy

**Status: planned.** Signing has not started yet. The 0.1 preview release ships every file unsigned, including
`yakbd-setup.exe`; the project applies to SignPath Foundation right after that release, and signing starts with a
later release. This page describes how signing will work once the project has been accepted. Releases can
already be checked with their `SHA256SUMS` file and build provenance attestation ([`release.md`](release.md)).

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by
[SignPath Foundation](https://signpath.org).

## What is signed

Only `yakbd-setup.exe`, the installer of the yakbd tool, published in the
[releases](https://github.com/yakbd/yakbd-cli/releases) of `yakbd/yakbd-cli`. The certificate is SignPath
Foundation's, so Windows shows "SignPath Foundation" as the publisher.

Not signed:

- the `yakbd.exe` zips and the Linux and macOS binaries of a release;
- the layout installers (`<dll name>-setup.exe`) that `yakbd installer` writes. They are built by the authors of
  the layouts on their own machines, not by this project, and are never signed with this project's certificate.
  An author can sign their own: [`windows-security-prompts.md`](windows-security-prompts.md) §4.

## How a signed release is made

- **Built from this repository's source only**, by the release workflow `.github/workflows/release.yml` on
  GitHub-hosted runners. Nobody builds a release on their own machine. The build is reproducible: the workflow
  builds every binary twice and compares the hashes.
- **Started by hand.** The workflow runs only when a maintainer starts it for a version; it has no automatic
  trigger.
- **Signed only after approval.** The signing step sends the built `yakbd-setup.exe` to SignPath. An approver
  checks that the request comes from the release workflow of this repository for the intended version, and approves
  it in SignPath. Every release is approved by hand.
- **Published unchanged.** The release is published with its assets and tag frozen (an immutable GitHub
  release), its `SHA256SUMS` file and a build provenance attestation, so anyone can check that a file comes from
  this repository's source and workflow.
- **Released before it is signed.** A form of `yakbd-setup.exe` is published in a release before its signed form
  is.

## Team roles

- **Authors** (may change the source without further review): the members of the
  [yakbd organisation](https://github.com/orgs/yakbd/people) with write access to `yakbd/yakbd-cli`.
- **Reviewers**: every change from someone outside the authors, such as a pull request, is reviewed by an author
  before it is merged.
- **Approvers** (approve each signing request): the [owners of the yakbd
  organisation](https://github.com/orgs/yakbd/people?query=role%3Aowner).

Every team member must use multi-factor authentication for GitHub and for SignPath.

## Privacy

This program will not transfer any information to other networked systems unless specifically requested by the
user or the person installing or operating it.

This applies to `yakbd.exe`, `yakbd-setup.exe` and the layout installers `yakbd` writes: none of them contains
network code.

## Reporting

If you think a file signed under this policy is not what this page says, or was signed by mistake, open an issue
in [`yakbd/yakbd-cli`](https://github.com/yakbd/yakbd-cli/issues), or write to SignPath Foundation
([signpath.org](https://signpath.org)).
