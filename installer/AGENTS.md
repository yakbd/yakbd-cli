# installer: agent rules

Layout installers (`yakbd installer`) and the tool's own `yakbd-setup.exe`. The contract is `docs/installer.md`
(design, payload, resources) and `docs/installer-cli.md` (command line, exit codes); the spec repo's
`targets/windows-install.md` and the reference spec's `installation` text are the normative sources.

    go test ./installer/...
    go run ./tools/buildstub      # after any change to a package compiled into the stub (root AGENTS.md)
    go run ./tools/toolsetup -o DIR/yakbd-setup.exe   # without -o it writes into the repository root;
                                                      # -builds, -extra [BUILD=]DIR: extra payload (docs/installer-cli.md §12.1)
                                                      # -shortcut "NAME=PATH": Start-menu shortcut to it (§12.2)
                                                      # reproducible; releases: go run ./tools/release (docs/release.md)

- Put every install decision (files per machine, KLID and Layout Id, value order, rollback, upgrade, repair,
  uninstall) in `installer/setup` behind the `System` interface and test it against the in-memory Windows of
  `installer/setup/fake_test.go`. `installer/stub`, `installer/toolstub` and `installer/winhost` only map calls to
  the Windows API, with plain `syscall`.
- The stub is one `windows/386` program for x86, x64 and ARM64 machines. Build it only through `tools/buildstub`
  (`installer/stubbuild` holds the fixed flags); a hand-made `go build` gives different bytes.
- `installer/exeres` is added when `yakbd installer` writes the file and is not compiled into the stub; changing it
  needs no stub rebuild.
- Installers are unsigned on purpose and there is no `.msi` (owner decisions). Keep the payload findable from the
  section table, so that a signature can be appended later (`docs/installer.md` §5).
- The command line follows `msiexec` switches and exit codes. A change there is a change to `docs/installer-cli.md`
  first; its tables drive the `installer/cmdline` tests.
