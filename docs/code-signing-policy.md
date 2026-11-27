# Code signing policy

**Status: planned.** Signing has not started yet: the 0.1 previews ship every file unsigned, including
the installers. The project has applied to SignPath Foundation, and signing starts with a release after the
project has been accepted. This page describes how signing will work then. Releases can
already be checked with their `SHA256SUMS` file and build provenance attestation ([`release.md`](release.md)).

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by
[SignPath Foundation](https://signpath.org).

## The project

The project is **yakbd**, one SignPath project for two repositories of the
[yakbd organisation](https://github.com/yakbd):

- [`yakbd/yakbd-cli`](https://github.com/yakbd/yakbd-cli): the command-line tool `yakbd` and its installer (this
  repository);
- [`yakbd/yakbd-gui`](https://github.com/yakbd/yakbd-gui): the graphical editor, released with the tool. Its own
  policy page mirrors this one.

## What is signed

Only Windows programs this project builds itself, from its own source, in the release workflows of these two
repositories:

- the installers, each one file for every Windows architecture (x86, x64 and arm64): `yakbd-cli-setup.exe`,
  published in the releases of [`yakbd/yakbd-cli`](https://github.com/yakbd/yakbd-cli/releases/latest), and
  `yakbd-gui-setup.exe`, published in the releases of [`yakbd/yakbd-gui`](https://github.com/yakbd/yakbd-gui/releases/latest);
- nested inside them, the project's own programs they install: `yakbd.exe` (each architecture's build) and, in the
  editor's installer, `yakbd-gui.exe`.

The certificate is SignPath Foundation's, so Windows shows "SignPath Foundation" as the publisher.

Not signed:

- third-party files, never: for example the Qt libraries and plugins the editor ships with keep the form their
  makers give them;
- the Linux and macOS archives of a release;
- the layout installers (`<dll name>-setup.exe`) that `yakbd installer` writes. They are built by the authors of
  the layouts on their own machines, not by this project, and are never signed with this project's certificate.
  An author can sign their own: [`windows-security-prompts.md`](windows-security-prompts.md) §4.

## How a signed release is made

- **Built from this repository's source only**, by the release workflow `.github/workflows/release.yml` on
  GitHub-hosted runners. Nobody builds a release on their own machine. The build is reproducible: the workflow
  builds every binary twice and compares the hashes.
- **Started by hand.** The workflow runs only when a maintainer starts it for a version; it has no automatic
  trigger.
- **Signed only after approval.** The release workflow sends what it built to SignPath: the project's own programs
  first, then each installer assembled around the signed programs (the workflow change for the nested programs
  comes with enabling signing, [`release.md`](release.md) §4). An approver
  checks that the request comes from the release workflow of this repository for the intended version, and approves
  it in SignPath. Every release is approved by hand.
- **Published unchanged.** The release is published with its assets and tag frozen (an immutable GitHub
  release), its `SHA256SUMS` file and a build provenance attestation, so anyone can check that a file comes from
  this repository's source and workflow.
- **Released before it is signed.** A form of each installer is published in a release before its signed form
  is.

## Team roles

- **Authors** (may change the source without further review): the members of the
  [yakbd organisation](https://github.com/orgs/yakbd/people) with write access to `yakbd/yakbd-cli` or
  `yakbd/yakbd-gui`.
- **Reviewers**: every change from someone outside the authors, such as a pull request, is reviewed by an author
  before it is merged.
- **Approvers** (approve each signing request): the [owners of the yakbd
  organisation](https://github.com/orgs/yakbd/people?query=role%3Aowner).

Every team member must use multi-factor authentication for GitHub and for SignPath.

## Privacy

This program will not transfer any information to other networked systems unless specifically requested by the
user or the person installing or operating it.

This applies to `yakbd.exe`, `yakbd-cli-setup.exe`, `yakbd-gui-setup.exe` and the layout installers `yakbd` writes: none of them contains
network code. The editor's policy page says the same of `yakbd-gui.exe`.

## Reporting

If you think a file signed under this policy is not what this page says, or was signed by mistake, open an issue
in [`yakbd/yakbd-cli`](https://github.com/yakbd/yakbd-cli/issues) (or in
[`yakbd/yakbd-gui`](https://github.com/yakbd/yakbd-gui/issues) for the editor), or write to SignPath Foundation
([signpath.org](https://signpath.org)).
