package main

import (
	"os"
	"path/filepath"
	"strings"
)

// writeSignIn3 writes the third sign-in bundle (kbl D98, VM-27): the same installers and helpers, a driver with the one
// LocalSystem leg, its own README.
func writeSignIn3(out string) error {
	if err := writeSignInBundle(out, signIn3Readme); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "run-phase.ps1"), []byte(strings.ReplaceAll(runPhasePS1, "\n", "\r\n")), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "drive.ps1"), []byte(strings.ReplaceAll(drive3PS1, "\n", "\r\n")), 0o644)
}

var drive3PS1 = strings.Replace(driveHead, "sign-in driver", "sign-in driver (VM-27)", 1) + driveLegs3 +
	strings.Replace(driveTail, "__LEGS__", "@(,@('legC', 'w11signin3'))", 1)

const driveLegs3 = `
try {
  # VM-27 (D98): the installer as LocalSystem for fo, no user having fo.
  $s = Fresh
  Phase $s si2-baseline
  Task $s vm27-before $Admin
  Task $s vm27-system SYSTEM
  Restart $s ''
  Shot 'C1-signin' -Lock
  $s = NewSession
  $s = Restart $s $Admin
  Task $s vm27-after-restart $Admin
  NewUser $s yk3
  $s = Restart $s yk3
  Task $s vm27-new-account yk3 -Limited
  $s = Restart $s $Admin
  Task $s vm27-uninstall $Admin
  Collect $s legC w11signin3
  Remove-PSSession $s
} catch {
  $Problems.Add("driver stopped: $($_.Exception.Message) at $($_.InvocationInfo.ScriptLineNumber)")
  Log "ERROR $($_.Exception.Message)"
}
try { Stop-VM -Name $VMName -TurnOff -Force; Restore-VMCheckpoint -VMName $VMName -Name $Checkpoint -Confirm:$false } catch { $Problems.Add("restore at the end: $($_.Exception.Message)") }

`

const signIn3Readme = `yakbd installer test bundle (third sign-in bundle): VM-27, the installer as LocalSystem, guest w11x64
=================================================================================================

FOR THE RUNNER: start one command on the Hyper-V host (elevated PowerShell), in the background, wait for it, report
host-results\summary.txt and what the one screenshot shows. Nothing else is interactive.

    powershell -ExecutionPolicy Bypass -File <this folder>\drive.ps1 -VMName <the w11x64 guest> -AdminUser <admin> -AdminPassword <adminpw>

<admin> is the guest's administrator (tester), signed in automatically at the console in the checkpoint "base"
(en-US, the US keyboard only; -Checkpoint <name> otherwise). The driver restores "base", copies this folder to C:\yk,
runs every phase (PowerShell Direct, and scheduled tasks through C:\yk\run-phase.ps1: Register-ScheduledTask with an
interactive logon, or as SYSTEM), restarts, creates yk3 and waits for its first sign-in, takes the screenshot,
restores "base" at the end. About 40 minutes. Outputs in host-results\: summary.txt, driver.log, legC\ (every
readback: places-*.txt, diff-*.txt, ui-*.txt, logs\), screenshot-C1-signin.png.

Screenshot (1): C1-signin, the sign-in screen after the restart: the driver presses Space, moves the mouse to the
point where run 1 opened the keyboard list (837,727 of 1024x768, scaled to the guest's resolution), waits, clicks and
waits 10 seconds. Report the exact names in the list, or that no list is open. legC\layout-names.txt names the
layout (fo, 0438); Windows may list it as "Faeroese keyboard".

kbl D98 (spec ea4c4de): as LocalSystem, with no loaded user hive having the language, the installer calls
InstallLayoutOrTip("0438:<KLID>", 0) itself, reads back the tag Windows filed in .DEFAULT (fo), writes the D97 shape
in HKU\.DEFAULT, S-1-5-19, S-1-5-20 and the Default profile (System Backup values included) and records .DEFAULT=fo
as added.

Result lines:
  si-baseline                               the four places before anything
  vm27-before                               tester has no fo; the display language recorded
  vm27-installer-as-localsystem-full-shape  fo-signin-setup.exe /quiet SIGNIN=1 as SYSTEM: exit 0, YakbdSignIn 1,
                                            YakbdSignInTags names .DEFAULT=fo; in each place fo in Languages (and
                                            System Backup Languages in the system hives), User Profile\fo value 1
                                            (and System Backup\fo), Preload 00000438, Substitutes 00000438 = the KLID
  vm27-shape-kept-after-restart             the same after the restart (places-vm27-after-restart.txt); the display
                                            language unchanged (effective first UI language, override)
  vm27-new-account-gets-fo-and-types        yk3, made after the install: fo in its languages, its HKL loaded in this
                                            and a new process, typing 64- and 32-bit; its display language recorded
  vm27-snapshot-after-new-account           the four places and tester's display language after yk3's sign-in
  vm27-uninstall-back-to-baseline           /x /quiet: the four places equal the baseline, .DEFAULT's Languages
                                            included; the display language unchanged
`
