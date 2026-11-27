// Command clibundle writes the Windows test bundle of the msiexec-compatible command line (docs/installer-cli.md):
// a layout installer in three versions, yakbd-setup.exe in three versions, the checker tools/clicheck and
// README.txt with the guest commands. Run from the repository root:
//
//	go run ./tools/clibundle -o DIR
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/internal/cli"
)

const minimal = "testdata/kbl-spec/examples/minimal-qwerty.kbl.json"

func main() {
	out := flag.String("o", "", "bundle folder (created; must not exist)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/clibundle -o DIR")
		os.Exit(2)
	}
	if err := write(*out); err != nil {
		fmt.Fprintln(os.Stderr, "clibundle:", err)
		os.Exit(1)
	}
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var e bytes.Buffer
	cmd.Stdout, cmd.Stderr = os.Stdout, &e
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, e.String())
	}
	return nil
}

func write(out string) error {
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s exists", out)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "clibundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	b, err := os.ReadFile(minimal)
	if err != nil {
		return err
	}
	// One layout of its own (own id and DLL name, so its own product code), in three versions.
	for _, v := range []struct{ tag, version string }{{"v0", "0.5.0"}, {"v1", "1.0.0"}, {"v2", "2.0.0"}} {
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			return err
		}
		meta := doc["metadata"].(map[string]any)
		meta["id"], meta["name"], meta["version"] = "en-t-k0-txcli", "TX command-line test", v.version
		doc["targets"].(map[string]any)["windows"].(map[string]any)["dllName"] = "kbdtxcli"
		js, _ := json.MarshalIndent(doc, "", "  ")
		src := filepath.Join(tmp, "txcli-"+v.tag+".kbl.json")
		if err := os.WriteFile(src, js, 0o644); err != nil {
			return err
		}
		var o, e bytes.Buffer
		if code := cli.Run([]string{"installer", "-q", "-enable", "false", "-o", filepath.Join(out, "txcli-"+v.tag+"-setup.exe"), src}, &o, &e); code != cli.ExitOK {
			return fmt.Errorf("yakbd installer: exit %d\n%s%s", code, o.String(), e.String())
		}
	}
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0"} {
		if err := run("go", "run", "./tools/toolsetup", "-version", v+".0", "-o", filepath.Join(out, "yakbd-setup-"+v+".exe")); err != nil {
			return err
		}
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(out, "clicheck.exe"), "./tools/clicheck")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if o, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build clicheck: %v\n%s", err, o)
	}
	rev, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	dirty, _ := exec.Command("git", "status", "--porcelain").Output()
	built := "tool commit " + strings.TrimSpace(string(rev))
	if len(dirty) > 0 {
		built += " (with uncommitted changes)"
	}
	gv, _ := exec.Command("go", "env", "GOVERSION").Output()
	if err := os.WriteFile(filepath.Join(out, "BUILT-FROM.txt"), []byte(built+"\n"+strings.TrimSpace(string(gv))+"\ngo run ./tools/clibundle -o DIR\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "README.txt"), []byte(readme), 0o644)
}

const readme = `Bundle tx-cli: the msiexec-compatible command line of yakbd's installers
==========================================================================================

Guest: w11x64 only (Windows 11 x64), a fresh disposable guest. Contract: docs/installer-cli.md of the tool repo.
No screenshots: everything runs without UI or with /passive (no dialogs).

Contents
  txcli-v0-setup.exe, txcli-v1-setup.exe, txcli-v2-setup.exe
                       one layout (kbdtxcli.dll, LANGID 0409, not enabled by default), versions 0.5.0, 1.0.0, 2.0.0,
                       one product code
  yakbd-setup-0.1.0.exe, yakbd-setup-0.2.0.exe, yakbd-setup-0.3.0.exe
                       the tool's installer (the same yakbd.exe builds, product versions 0.1.0.0/0.2.0.0/0.3.0.0)
  clicheck.exe         the checker (64-bit)

Steps (as an administrator, in an ELEVATED PowerShell, signed in interactively):
  1. Copy the bundle folder to C:\tx.
  2. C:\tx\clicheck.exe -bundle C:\tx -phase main
     It runs every scenario below and, as its LAST step, starts the layout installer with /quiet and no /norestart
     while a layout file is held open: the installer restarts Windows by itself (msiexec's rule). That restart is
     expected. If Windows has not restarted after 2 minutes the checker writes a FAIL line and ends.
  3. After the restart, sign in as the same administrator, elevated PowerShell:
     C:\tx\clicheck.exe -bundle C:\tx -phase after-restart
  4. Copy C:\tx\results\ out (tx-cli-main.txt, tx-cli-after-restart.txt, their *-details.txt, logs\).
     Send back the two result files; the details and logs only for a FAIL.

Scenarios (one PASS/FAIL line each):
  L-bad-command-line-1639      /bogus /quiet, /quiet /promptrestart, /x /i, the removed /repair, ALLUSERS="" -> 1639;
                               /p x.msp -> 1635; TRANSFORMS= -> 1624; nothing changed
  L-not-installed-1605         /x, /f, /x {other code} before any install -> 1605
  L-log-folder-missing-1622    /l*v C:\no-such-folder-tx\x.log -> 1622
  L-install-quiet-and-entry    v1 /quiet /norestart /l*v log -> 0; log replaced (not appended); DisplayVersion
                               1.0.0.0; UninstallString / QuietUninstallString / ModifyPath as the contract; no
                               WindowsInstaller, NoModify, NoRepair, NoRemove
  L-same-version-repair-log-append  v1 /i /qn and /package {code} /qn with /l*v+ -> 0 each, log appended, state same
  L-another-installer-running-1618  the checker holds Global\YAKBD-Setup; v2 /quiet -> 1618, nothing changed
  L-downgrade-refused-1638     v0 over v1 -> 1638, nothing changed
  L-upgrade-file-in-use-3010   System32\kbdtxcli.dll held open; v2 /passive /norestart -> 3010; DisplayVersion
                               2.0.0.0, same KLID, a pending .yakbd-new replacement
  L-repair-modifypath-winget-style  KLID key deleted by hand; ModifyPath + " /quiet /norestart" -> 0, KLID back;
                               /i REINSTALL=ALL REINSTALLMODE=vomus /qn and /fvomus {code} /qn -> 0
  L-quiet-uninstall-string     QuietUninstallString -> 0; /x again -> 1605; state as at the start
  L-uninstall-by-package-file-and-code  v2 /uninstall <v1 file> -> 0; /X{code} -> 0; /x yakbd-setup file -> 1619
  L-winget-manifest-switches   /quiet /norestart /log, /passive /norestart /log, ModifyPath + /quiet /norestart,
                               QuietUninstallString -> 0 each
  L-enable-property            ENABLE=1 -> in the user's list; ENABLE=0 -> not; each uninstalled
  T-install-quiet-path-entry   yakbd-setup-0.2.0 /quiet /norestart -> 0; Program Files\YAKBD\yakbd\yakbd.exe is the
                               x64 build; the machine Path has the folder; "yakbd version" runs through that Path in a
                               new cmd.exe; entry values
  T-downgrade-refused-1638     0.1.0 over 0.2.0 -> 1638
  T-another-installer-running-1618
  T-repair-modifypath          yakbd.exe deleted; ModifyPath /quiet /norestart -> 0, back
  T-upgrade-in-use-3010        yakbd.exe held open; 0.3.0 /quiet /norestart -> 3010, pending .yakbd-new
  T-quiet-uninstall-string-path-removed  QuietUninstallString -> 0; Path entry, files, entry gone
  T-uninstall-by-product-code  install, /x {code} -> 0, /x again -> 1605, INSTALLDIR= -> 1639
  R-prepare-quiet-restart-1641 v1 installed, the layout file held; then v2 /quiet (no /norestart): Windows restarts
  after-restart phase:
  R-restart-1641               the run's log says "restart: initiating a restart (exit code 1641)"
  R-delayed-replacement-landed v2 installed, its x64 file in System32 after the restart, no .yakbd-* left (the
                               delayed move written through Sysnative; docs/installer.md §8.1)
  R-cleanup                    /x -> 0; nothing of the layout or the tool left
Not in the VM (Linux unit tests only): 740 (no UI and not elevated: the checker runs elevated), the dialogs of the
full UI, /forcerestart (the same restart path as R-*).
`
