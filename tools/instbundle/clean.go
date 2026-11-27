package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// writeClean writes the clean-machine bundle of a release (docs/release.md §3): the files a release workflow run
// built, as downloaded from its draft into assets (never local builds), the corpus .klc, both checkers, the host
// driver and README.txt.
func writeClean(out, assets string) error {
	if assets == "" {
		return fmt.Errorf("-guest w11clean needs -assets DIR (the files downloaded from a release draft)")
	}
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s exists", out)
	}
	zips, _ := filepath.Glob(filepath.Join(assets, "yakbd-*-windows-x64.zip"))
	if len(zips) != 1 {
		return fmt.Errorf("%s: want one yakbd-V-windows-x64.zip, found %d", assets, len(zips))
	}
	version := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(zips[0]), "yakbd-"), "-windows-x64.zip")
	if err := os.MkdirAll(filepath.Join(out, "ci"), 0o755); err != nil {
		return err
	}
	for _, f := range []string{"yakbd-setup.exe", filepath.Base(zips[0]), "SHA256SUMS"} {
		b, err := os.ReadFile(filepath.Join(assets, f))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, "ci", f), b, 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(out, "ci", "VERSION.txt"), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	b, err := os.ReadFile(kbdtst)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "kbdtst.klc"), b, 0o644); err != nil {
		return err
	}
	if err := checkers(out, [][2]string{{"amd64", "instcheck.exe"}, {"386", "instcheck-32.exe"}}); err != nil {
		return err
	}
	head, _ := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	built := fmt.Sprintf("checkers: yakbd/yakbd-cli %s\nci\\: the release files of version %s as downloaded (see %s)\n",
		strings.TrimSpace(string(head)), version, filepath.Base(assets))
	if src, err := os.ReadFile(filepath.Join(assets, "SOURCE.txt")); err == nil {
		built += string(src)
	}
	crlf := func(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }
	for name, text := range map[string]string{"BUILT-FROM.txt": built, "run-phase.ps1": runPhasePS1, "drive.ps1": driveCleanPS1,
		"README.txt": strings.ReplaceAll(cleanReadme, "__VERSION__", version)} {
		if err := os.WriteFile(filepath.Join(out, name), crlf(text), 0o644); err != nil {
			return err
		}
	}
	for _, f := range cleanFiles(version) {
		if st, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil || st.Size() == 0 {
			return fmt.Errorf("bundle: %s missing or empty", f)
		}
	}
	return nil
}

// cleanFiles are the files the driver and the clean-* phases of tools/instcheck open.
func cleanFiles(version string) []string {
	return []string{"drive.ps1", "run-phase.ps1", "instcheck.exe", "kbdtst.klc", "ci/yakbd-setup.exe", "ci/SHA256SUMS",
		"ci/VERSION.txt", "ci/yakbd-" + version + "-windows-x64.zip"}
}

var driveCleanPS1 = strings.Replace(strings.Replace(strings.Replace(driveHead, "round 5b driver", "clean-machine release driver", 1),
	"sign-in driver", "clean-machine release driver", 1),
	"if (-not $Out) { $Out = Join-Path $Bundle 'host-results' }", strings.ReplaceAll(plantOut, "yakbd-tr-planting", "yakbd-tr-clean"), 1) + `
try {
  $s = Fresh
  Task $s clean-install $Admin
  Task $s clean-use $Admin
  Task $s clean-uninstall $Admin
  Collect $s legC w11clean
  Remove-PSSession $s
} catch {
  $Problems.Add("driver stopped: $($_.Exception.Message) at $($_.InvocationInfo.ScriptLineNumber)")
  Log "ERROR $($_.Exception.Message)"
}
try { Stop-VM -Name $VMName -TurnOff -Force; Restore-VMCheckpoint -VMName $VMName -Name $Checkpoint -Confirm:$false } catch { $Problems.Add("restore at the end: $($_.Exception.Message)") }

` + strings.Replace(driveTail, "__LEGS__", "@(,@('legC', 'w11clean'))", 1)

const cleanReadme = `yakbd release check on a clean machine, guest w11x64: version __VERSION__ as the release workflow built it
=====================================================================================================

FOR THE RUNNER: start one command on the Hyper-V host (elevated PowerShell), in the background, wait for it, report
summary.txt from the results folder. Nothing is interactive.

    powershell -ExecutionPolicy Bypass -File <this folder>\drive.ps1 -VMName <the w11x64 guest> -AdminUser <admin> -AdminPassword <adminpw>

<admin> is the guest's administrator (tester), signed in automatically at the console in the checkpoint "base"
(-Checkpoint <name> otherwise). The driver restores "base", copies this folder to C:\yk, runs three phases as
scheduled tasks in the console session (each a new program, so each sees the machine PATH as a new window does),
collects C:\yk\results into host-results\legC\ (next to drive.ps1 when Windows can write there, otherwise
%TEMP%\yakbd-tr-clean\host-results; the first output line and driver.log name it) and restores "base". About
10 minutes.

ci\ holds the release files exactly as downloaded from the workflow's draft release (BUILT-FROM.txt: run and
commit): yakbd-setup.exe, the x64 zip, SHA256SUMS. Nothing in ci\ was built on another machine.

Result lines (summary.txt):
  clean-assets-match-sha256sums           the ci\ files have the hashes of SHA256SUMS
  clean-setup-quiet-installs              ci\yakbd-setup.exe /quiet /norestart: exit 0; Installed apps entry with
                                          DisplayVersion __VERSION__; yakbd.exe in Program Files\YAKBD\yakbd; the
                                          folder in the machine Path
  clean-yakbd-on-path-prints-version      in a new task: "yakbd version" through cmd.exe prints "yakbd __VERSION__";
                                          "where yakbd" finds the installed one
  clean-convert-validate-installer        that yakbd: convert kbdtst.klc to .kbl.json, validate -windows, installer
                                          kbdtst.klc, compile -emit-expectations
  clean-layout-installs-types-uninstalls  kbdtst-setup.exe /quiet /norestart: installed and enabled; the layout
                                          types every expectation line; /x /quiet removes it (0 or 3010)
  clean-zip-yakbd-runs-without-install    yakbd.exe unzipped from the x64 zip prints "yakbd __VERSION__"
  clean-setup-uninstall-leaves-nothing    ci\yakbd-setup.exe /x /quiet /norestart: exit 0; entry, Path entry and
                                          folder gone

Exit: summary.txt ends with "w11clean: N passed, M failed".
`
