package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer"
)

// features is the example with LANGID 0438 (no such language on an en-US guest): VM-19.
const features = "testdata/kbl-spec/examples/windows-features.kbl.json"

// writeSignIn writes the first bundle of the sign-in screen and new users (kbl D87, targets/windows-install.md
// §7.1, §9 step 1; VM-18 to VM-23) for a Windows 11 x64 guest: three installers, their expectations, the checkers,
// BUILT-FROM.txt and README.txt. Phases: tools/instcheck/signin_windows.go.
func writeSignIn(out string) error { return writeSignInBundle(out, signInReadme) }

// writeSignIn2 writes the second sign-in bundle (kbl D97): the same installers, the run-phase.ps1 helper, its own README.
func writeSignIn2(out string) error {
	if err := writeSignInBundle(out, signIn2Readme); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "run-phase.ps1"), []byte(strings.ReplaceAll(runPhasePS1, "\n", "\r\n")), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "drive.ps1"), []byte(strings.ReplaceAll(drivePS1, "\n", "\r\n")), 0o644)
}

func writeSignInBundle(out, readme string) error {
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s exists", out)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "instbundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// The 0438 example with the file field on (VM-18 "with the file field on", VM-19).
	b, err := os.ReadFile(features)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	doc["targets"].(map[string]any)["windows"].(map[string]any)["install"].(map[string]any)["enableForSignInAndNewUsers"] = true
	fb, _ := json.MarshalIndent(doc, "", "  ")
	fo := filepath.Join(tmp, "fo-signin.kbl.json")
	if err := os.WriteFile(fo, fb, 0o644); err != nil {
		return err
	}
	for _, s := range [][]string{
		{"installer", "-q", "-build", "x64", "-o", filepath.Join(out, "demo-setup.exe"), demo},
		{"installer", "-q", "-build", "x64", "-o", filepath.Join(out, "fo-signin-setup.exe"), fo},
		{"installer", "-q", "-build", "x64", "-o", filepath.Join(out, "minimal-setup.exe"), minimal},
	} {
		if err := yakbd(s...); err != nil {
			return err
		}
	}
	for _, e := range [][2]string{{demo, "demo.expect.txt"}, {fo, "fo.expect.txt"}, {minimal, "minimal.expect.txt"}} {
		if err := expectations(e[0], tmp, filepath.Join(out, e[1])); err != nil {
			return err
		}
	}
	if err := checkers(out, [][2]string{{"amd64", "instcheck.exe"}, {"386", "instcheck-32.exe"}}); err != nil {
		return err
	}
	rev, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	dirty, _ := exec.Command("git", "status", "--porcelain", "--untracked-files=no").Output()
	built := fmt.Sprintf("tool commit %s%s\ninstaller program built with %s\nguest w11x64\n", strings.TrimSpace(string(rev)),
		map[bool]string{true: " (with uncommitted changes)"}[len(strings.TrimSpace(string(dirty))) > 0], installer.StubGoVersion())
	if err := os.WriteFile(filepath.Join(out, "BUILT-FROM.txt"), []byte(built), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "README.txt"), []byte(strings.ReplaceAll(readme, "\n", "\r\n")), 0o644)
}

const signInReadme = `yakbd installer test bundle: the sign-in screen and new users, guest w11x64
=============================================================================================

kbl-spec D87 (targets/windows-install.md section 7.1 and section 9 step 1): with SIGNIN=1, or the file field
install.enableForSignInAndNewUsers, the installer writes the layout's entries into HKU\.DEFAULT (the sign-in
screen), HKU\S-1-5-19, HKU\S-1-5-20 and the Default profile (C:\Users\Default\NTUSER.DAT, the profile new accounts
are made from); uninstall always removes them from those four places. Questions VM-18 to VM-23 of the spec's
targets/windows-vm-tests.md.

ONLY inside a disposable Windows 11 x64 guest restored to its checkpoint "base" (en-US, the US keyboard only).
The installers are unsigned. Exit codes are msiexec's (0 done, 3010 restart to finish, 1603 failed).
Notation: <admin>/<adminpw> = the guest's administrator account (called tester), signed in automatically at the
console in "base".

Running a phase: "task" = a scheduled task with an interactive logon in the console session, started through
PowerShell Direct as <admin> (<run as>, <pw>, <level> as the step says):

    schtasks /create /tn yk /sc once /st 00:00 /ru <run as> /rp <pw> /it /rl <level> /f /tr "cmd /c C:\yk\instcheck.exe -bundle C:\yk -phase <phase> > C:\yk\results\console-<phase>.txt 2>&1"
    schtasks /run /tn yk

"PSD" = directly in the PowerShell Direct session as <admin>: C:\yk\instcheck.exe -bundle C:\yk -phase <phase>
A phase is finished when C:\yk\results\<phase>.txt exists and its last line is "<n> passed, <m> failed" (check
at most every 10 seconds).

Restart with automatic sign-in of <account> (<password>), through PowerShell Direct:

    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoAdminLogon /t REG_SZ /d 1 /f
    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultUserName /t REG_SZ /d <account> /f
    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultPassword /t REG_SZ /d <password> /f
    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultDomainName /t REG_SZ /d $env:COMPUTERNAME /f
    reg delete "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoLogonCount /f
    shutdown /r /t 0

then wait until explorer.exe runs as that account (Get-Process explorer -IncludeUserName) and two more minutes (a
first sign-in creates the profile and takes longer; if a phase of a new account gets stuck, one screenshot of the
screen and report it).
Restart WITHOUT automatic sign-in: the same with "/d 0" in the AutoAdminLogon line, then wait until the sign-in
screen is up (about a minute after the boot).

Screenshots: exactly 2, of the VM's screen from the host, with the sign-in screen's input switcher open:
  To open it: on the lock screen press Space (from the host keyboard: Hyper-V Msvm_Keyboard TypeKey 0x20) so the
  password box shows, then press Windows key + Space (PressKey 0x5B, TypeKey 0x20, ReleaseKey 0x5B); if no list of
  keyboards opens, click the language indicator (for example "ENG") at the bottom right once. Do not type anything
  else: do not sign in from this screen.
  1. after step 2: lock the screen (Windows key + L), open the switcher, screenshot;
  2. after step 3 (a restart without automatic sign-in): open the switcher on the sign-in screen, screenshot.
Report for each the list of names it shows. results\layout-names.txt (written in step 2) says which name is which
layout: demo (SIGNIN=1, VM-18), fo (the file field, LANGID 0438, VM-19), minimal (written by the test into
.DEFAULT and the Default profile only, VM-23). A layout of a language Windows shows with its own name may appear
under that language.

Steps (<checker> is C:\yk\instcheck.exe):
0. PSD: copy this folder to C:\yk; mkdir C:\yk\results; icacls C:\yk /grant "Users:(OI)(CI)M"
1. PSD   si-baseline                         (the four places before anything; each place's Preload count and language)
2. task  si-install         as tester, level highest, pw <adminpw>
         Then screenshot 1 (lock the screen first).
3. Restart WITHOUT automatic sign-in; screenshot 2 at the sign-in screen.
   Then, through PowerShell Direct: restart with automatic sign-in of tester.
4. task  si-after-restart   as tester, level highest
5. PSD:  New-LocalUser -Name yk3 -Password (ConvertTo-SecureString 'Yk3-test-pw1' -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires
         Add-LocalGroupMember -Group Users -Member yk3
   Restart with automatic sign-in of yk3 (Yk3-test-pw1).
6. task  si-new-account     as yk3, level limited, pw Yk3-test-pw1
7. Restart with automatic sign-in of tester.
8. task  si-repair          as tester, level highest
9. task  si-uninstall       as tester, level highest   (one uninstall runs through the uninstall entry's command;
                            the checker presses the installer's own dialogs; no elevation prompt is expected because
                            the task is elevated)
10. PSD: New-LocalUser -Name yk4 -Password (ConvertTo-SecureString 'Yk4-test-pw1' -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires
         Add-LocalGroupMember -Group Users -Member yk4
    Restart with automatic sign-in of yk4 (Yk4-test-pw1). (Report anything unusual on the screen at this sign-in.)
11. task si-after-uninstall as yk4, level limited, pw Yk4-test-pw1
12. Restart with automatic sign-in of tester.
13. task si-copy            as tester, level highest   (runs Windows' own Copy-UserInternationalSettingsToSystem
                            twice; it changes the guest's sign-in settings, which is why the guest is restored after)
14. PSD  <checker> -bundle C:\yk -phase summary -guest w11signin
Copy C:\yk\results\ out of the guest (w11signin.txt, each phase's <phase>.txt and -details.txt, places-*.txt,
layout-names.txt, logs\, console-*.txt). Report the count line of w11signin.txt, every FAIL line, the notes of the
vm19-*, vm23-* and vm18-entries-survive-restart lines, and what the two screenshots show. Then restore "base".

What the result lines check (one PASS/FAIL line each; PASS lines carry notes in brackets):
  si-baseline                          the four places read (the Default profile from a copy); whether S-1-5-19
                                       and S-1-5-20 are loaded
  vm18-install-signin                  demo-setup.exe /quiet /norestart ENABLE=0 SIGNIN=1 (not in tester's own list,
                                       so screenshot 1 shows only what the option wrote): exit 0, YakbdSignIn = 1,
                                       no HKU\YAKBD-* left loaded; in each place a Preload entry resolving to the
                                       KLID, the User Profile value <LLLL>:<KLID> = that number, the System Backup
                                       value in the three system hives only; nothing else changed (VM-18; and
                                       whether InstallLayoutOrTipUserReg took .DEFAULT, S-1-5-19, S-1-5-20: the log)
  vm18-second-run-adds-nothing         the same again: the four places unchanged
  vm19-install-0438-file-field         fo-signin-setup.exe /quiet (the file asks for it): record 1; where the place
                                       has no 0438 language, Preload and Substitutes only (noted per place)
  vm23-prepare-default-and-defaultprofile-only  minimal-setup.exe /quiet ENABLE=0, then the test writes its
                                       entries into .DEFAULT and the Default profile only (not S-1-5-19/20)
  vm18-entries-survive-restart         after the restart: demo still in T8's shape; what Windows changed (noted)
  vm23-after-restart                   how many entries name minimal in each place (noted)
  vm18-new-account-has-layout          yk3, created after the install, first sign-in: its hive names demo, Preload
                                       resolves to it, the HKL in this and a new process, typing 64- and 32-bit
  vm19-new-account-0438, vm23-new-account-minimal   the same, recorded (informative)
  vm22-repair-restores-removed-entries demo's entries removed by hand from .DEFAULT and the Default profile, then
                                       /f /quiet: back in all four places (recorded on)
  vm22-repair-signin0-cleans           /f /quiet SIGNIN=0: the four places clean, record 0
  vm22-install-again-keeps-state       /quiet without SIGNIN: nothing changes, record 0; then /f SIGNIN=1 for the
                                       next phase: back in T8's shape
  vm20-uninstall-quiet-demo            /x /quiet: the four places do not name it; entry gone
  vm20-uninstall-entry-command-fo      the uninstall entry's UninstallString, started through the shell: the same
  vm20-uninstall-quiet-minimal         /x /quiet of the layout the test had put there: the same (section 9: always)
  vm20-four-places-as-before           the four places equal si-baseline
  vm20-new-account-after-uninstall     yk4, created after the uninstall: signs in, no trace of the three layouts
  vm20-places-clean-after-restart      after the restart: the four places still equal si-baseline
  vm21-plain-install-uninstall-leaves-places  demo /quiet ENABLE=1 without SIGNIN, then /x: the places unchanged
  vm21-windows-copy-then-uninstall-cleans     demo enabled, Windows' copy (which then names it in the places),
                                       /x /quiet: no place names it
  vm19-windows-copy-0438               fo enabled for tester (SIGNIN=0), Windows' copy: what it wrote into the four
                                       places (noted; places-vm19-*.txt), then /x: no place names it
VM-23 (are S-1-5-19 and S-1-5-20 needed?) is answered by screenshot 2 (does minimal appear) together with
vm23-after-restart.
`
