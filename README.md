# yakbd

**One keyboard layout file, built for every platform.** `yakbd` is the command-line tool of
[YAKBD](https://yakbd.github.io/): it converts layouts to and from the open [`.kbl.json`](https://yakbd.github.io/spec/)
format, checks them, and builds layouts that install like any other, on any computer.

| platform | state |
|---|---|
| Windows (`.klc`, layout DLLs, one installer for x64, x86 and ARM64) | available |
| macOS (`.keylayout` bundle) | in the next release |
| Linux (XKB, Wayland and X11) | in the next release |
| Android, iOS, ChromeOS, Keyman | planned |

## Get it

- Windows: [`yakbd-cli-setup.exe`](https://github.com/yakbd/yakbd-cli/releases/latest/download/yakbd-cli-setup.exe)
- Linux and macOS: the `.tar.gz` archives of the [latest release](https://github.com/yakbd/yakbd-cli/releases/latest)
- With Go: `go install github.com/yakbd/yakbd-cli/cmd/yakbd@latest`

```
yakbd convert my-layout.klc my-layout.kbl.json   # into the open format, with a report of every change
yakbd installer my-layout.kbl.json               # a Windows installer (-target macos and xkb: next release)
```

## Documentation

- [Install and quick start](https://yakbd.github.io/guide/)
- [Moving from MSKLC](https://yakbd.github.io/guide/migrating-from-msklc), [recovering a layout](https://yakbd.github.io/guide/recover-a-layout)
- [macOS](docs/macos.md) and [Linux](docs/linux.md) (next release)
- [Command reference](docs/commands.md)
- [What's new](https://github.com/yakbd/yakbd-cli/releases/latest)

For contributors: [`ARCHITECTURE.md`](ARCHITECTURE.md) and [`AGENTS.md`](AGENTS.md).

## Contributing and licence

Bug reports and pull requests are welcome: see [contributing](https://github.com/yakbd/.github/blob/main/CONTRIBUTING.md).
MIT-0 ([`LICENSE`](LICENSE)); third-party material keeps its own licence ([`NOTICE`](NOTICE)).

## Code signing policy

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by
[SignPath Foundation](https://signpath.org) ([policy](https://yakbd.github.io/guide/code-signing-policy); the installers are unsigned until signing starts).
