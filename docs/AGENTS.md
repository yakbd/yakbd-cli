# docs: agent rules

What the tool does, for people and for programs. Most files here are pages of https://yakbd.github.io/, fetched by
`yakbd/yakbd.github.io` at the commit pinned in its `sources.json`; a change shows on the site only after that re-pin.

| file | site page | kind |
|---|---|---|
| `commands.md` | `/cli/` | every command and its main flags; update with any command or flag change |
| `targets.md` | `/cli/targets` | `-target` and the target interface |
| `report-file.md` | `/cli/report-file` | public contract for programs: additive changes only |
| `installer.md`, `installer-cli.md` | `/cli/installer`, `/cli/installer-cli` | Windows installers; `installer-cli.md` tables drive `installer/cmdline` tests |
| `cldr.md`, `dll-import.md`, `release.md` | `/cli/cldr`, `/cli/dll-import`, `/cli/release` | reference |
| `migrating-from-msklc.md`, `recover-a-layout.md`, `windows-security-prompts.md`, `code-signing-policy.md` | `/guide/...` (same name) | guides |
| `macos.md`, `linux.md` | `/guide/macos`, `/guide/linux` | platform guides |
| `arm64x.md`, `fuzzing.md` | none (GitHub only) | developer notes |

    go test ./tools/release ./installer/...   # link rule (no tag links) and the installer-cli.md tables

- Keep normal relative links between these files: the site's fetch script rewrites them to site pages, or to GitHub
  at the pinned commit for files it does not fetch.
- A new page for people: add the file here, then ask for it in the site repo (`sources.json` entry, nav). Each topic
  has one home: link to the site page instead of repeating it in `README.md` or another file.
- Where a document and the spec repo disagree, the spec wins (root `AGENTS.md`, "Which text wins").
