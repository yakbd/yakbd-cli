# yakbd

**One keyboard layout file, built for every platform.** `yakbd` is the command-line tool of
[YAKBD](https://yakbd.github.io/): it converts layouts to and from the open [`.kbl.json`](https://yakbd.github.io/spec/)
format, checks them, and builds layouts that install like any other, on any computer.

| platform | state |
|---|---|
| Windows (`.klc`, layout DLLs, one installer for x64, x86 and ARM64) | available |
| macOS (`.keylayout` bundle) | available |
| Linux (XKB, Wayland and X11) | available |
| Android, iOS, ChromeOS, Keyman | planned |

## Get it

- Windows: [`yakbd-cli-setup.exe`](https://github.com/yakbd/yakbd-cli/releases/latest/download/yakbd-cli-setup.exe)
- Linux: [`yakbd-linux-amd64.tar.gz`](https://github.com/yakbd/yakbd-cli/releases/latest/download/yakbd-linux-amd64.tar.gz),
  [`yakbd-linux-arm64.tar.gz`](https://github.com/yakbd/yakbd-cli/releases/latest/download/yakbd-linux-arm64.tar.gz)
- macOS: [`yakbd-macos.tar.gz`](https://github.com/yakbd/yakbd-cli/releases/latest/download/yakbd-macos.tar.gz)
- With Go: `go install github.com/yakbd/yakbd-cli/cmd/yakbd@latest`

```
yakbd convert my-layout.klc my-layout.kbl.json   # into the open format, with a report of every change
yakbd installer my-layout.kbl.json               # a Windows installer; -target macos or xkb for the others
```

## Documentation

- [Install](https://yakbd.github.io/guide/) and [quick start](https://yakbd.github.io/guide/quick-start)
- [Moving from MSKLC](https://yakbd.github.io/guide/migrating-from-msklc), [recovering a layout](https://yakbd.github.io/guide/recover-a-layout)
- [Windows](https://yakbd.github.io/guide/windows), [macOS](https://yakbd.github.io/guide/macos) and [Linux](https://yakbd.github.io/guide/linux)
- [Command reference](https://yakbd.github.io/cli/)
- [What's new](https://yakbd.github.io/releases)

For contributors: [`ARCHITECTURE.md`](ARCHITECTURE.md) and [`AGENTS.md`](AGENTS.md).

## Contributing and licence

Bug reports and pull requests are welcome: see [contributing](https://yakbd.github.io/contributing).
MIT-0 ([`LICENSE`](LICENSE)); third-party material keeps its own licence ([`NOTICE`](NOTICE)).
Releases are not code-signed: check a download with its checksum and attestation ([why and how](https://yakbd.github.io/guide/code-signing-policy)).
