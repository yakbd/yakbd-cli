package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer"
)

// writeGuest writes a real-user bundle for one guest kind: the installers and checkers of the hardening bundle,
// BUILT-FROM.txt and a README with the guest's phases; for the Windows 10 guests also the typing
// bundle of tools/vmbundle in vm\.
func writeGuest(out, guest string) error {
	readme, ok := guestReadme[guest]
	if !ok {
		return fmt.Errorf("unknown guest %q (w11x64, w10x64, w10x86)", guest)
	}
	if err := write(out); err != nil {
		return err
	}
	if strings.HasPrefix(guest, "w10") {
		cmd := exec.Command("go", "run", "./tools/vmbundle", "-o", filepath.Join(out, "vm"))
		if o, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("vmbundle: %v\n%s", err, o)
		}
	}
	rev, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	dirty, _ := exec.Command("git", "status", "--porcelain", "--untracked-files=no").Output()
	built := fmt.Sprintf("tool commit %s%s\ninstaller program built with %s\nguest %s\n", strings.TrimSpace(string(rev)),
		map[bool]string{true: " (with uncommitted changes)"}[len(strings.TrimSpace(string(dirty))) > 0], installer.StubGoVersion(), guest)
	if err := os.WriteFile(filepath.Join(out, "BUILT-FROM.txt"), []byte(built), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "README.txt"), []byte(strings.ReplaceAll(readme, "\n", "\r\n")), 0o644)
}

const commonTop = `ONLY inside the disposable guest named above, restored to its checkpoint "base" first. Every installer is
unsigned (Windows shows "Unknown publisher"). Exit codes are msiexec's (docs/installer-cli.md §4: 0 done, 3010
restart to finish, 1602 cancelled, 1603 failed or refused, 1620 damaged installer, 1633 no build for this machine);
the checker answers the installer's "Restart now?" question with No. Notation: <admin> and <adminpw> are the guest's administrator
account and password (the account signed in automatically at the console in "base"; called tester below).

Running a phase (the checker needs the interactive desktop; one scheduled task per phase, started through
PowerShell Direct as <admin>; <phase>, <run as>, <pw>, <level> from the table of the guest):

    schtasks /create /tn yk /sc once /st 00:00 /ru <run as> /rp <pw> /it /rl <level> /f /tr "cmd /c C:\yk\<checker> -bundle C:\yk -phase <phase> > C:\yk\results\console-<phase>.txt 2>&1"
    schtasks /run /tn yk

A phase is finished when C:\yk\results\<phase>.txt exists and its last line is "<n> passed, <m> failed". Check
for that (and for the marker below) through PowerShell Direct at most every 10 seconds. Phases marked "PSD" run
directly in the PowerShell Direct session instead (no task): C:\yk\<checker> -bundle C:\yk -phase <phase>.

Restart with automatic sign-in of <account> (<password>): through PowerShell Direct

    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoAdminLogon /t REG_SZ /d 1 /f
    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultUserName /t REG_SZ /d <account> /f
    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultPassword /t REG_SZ /d <password> /f
    reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultDomainName /t REG_SZ /d $env:COMPUTERNAME /f
    reg delete "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoLogonCount /f
    shutdown /r /t 0

(PowerShell syntax; an error "unable to find" from the reg delete is fine; the reference specification's
tests/vm/console-user.ps1 does the same), then wait until explorer.exe runs as that
account (Get-Process explorer -IncludeUserName) and two more minutes: an account's first sign-in creates its
profile and takes longer. If a phase of a freshly signed-in account gets stuck, take a screenshot (a first-sign-in
page may cover the desktop) and report it.
`

const resultsBottom = `
At the end, through PowerShell Direct as <admin>:  C:\yk\<checker> -bundle C:\yk -phase summary -guest %s
Copy C:\yk\results\ out of the guest (the combined result file %s.txt; each phase's <phase>.txt and
<phase>-details.txt; logs\; console-*.txt). Report the count line of %s.txt and every FAIL line. Then restore
"base".
`

var guestReadme = map[string]string{
	"w11x64": `yakbd installer test bundle, real-user paths: guest w11x64 (Windows 11 x64)
=================================================================================

The double-click path with UAC (an administrator at the consent prompt; a standard user with an administrator's
credentials, "over the shoulder"), removal through the uninstall entry's command, installer detection, the D69 cases (a
second user signed in during a repair that took a new Layout Id, and signing in afterwards; the only-layout repair)
and a layout file in use at uninstall whose deletion waits for the restart.

` + commonTop + `
Elevation prompts (the double-click phases): the installer's own dialogs are pressed by the checker, but the
elevation prompt is on the secure desktop, out of reach of any program in the guest. When the file
C:\yk\results\uac-waiting.txt exists, the prompt is up; its line says which one and how to answer:
  WAITING CONSENT ...      press Alt+Y (Yes) on the VM's keyboard from the host (Hyper-V: the VM's Msvm_Keyboard,
                           PressKey/ReleaseKey or TypeKey);
  WAITING CREDENTIALS ...  the prompt asks for an administrator's password: type <adminpw> and press Enter (if the
                           prompt shows a user-name box instead, type tester, Tab, <adminpw>, Enter);
  WAITING DETECT ...       press Alt+N (No): this only happens when the test has failed.
Nothing in the guest is changed to answer them. The marker disappears once the prompt has closed. Screenshots
(host, of the VM's screen): exactly 2, both while a marker exists, BEFORE the keys:
  1. phase dbl-admin, the first prompt (consent): it must show the program name and "Unknown publisher";
  2. phase ots-install (credentials): the same, with the credential fields.
Further screenshots only when a phase is stuck, at most 4. Report what screenshots 1 and 2 show (program name,
publisher line, which account is offered).

0. Setup, through PowerShell Direct as <admin>:
       copy this folder to C:\yk
       New-LocalUser -Name yk2 -Password (ConvertTo-SecureString 'Yk2-test-pw1' -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires
       Add-LocalGroupMember -Group Users -Member yk2   (a standard user, as the reference's tests/vm/mkuser.ps1)
       icacls C:\yk /grant "Users:(OI)(CI)M"
       mkdir C:\yk\results
1. PSD  uac-settings                              (records the UAC policy values; UAC must be on, the defaults)
2. task dbl-admin          run as tester, level limited, pw <adminpw>   (2 elevation prompts: consent, consent;
                           screenshot 1 at the first). Double-click install, typing, removal through the entry's command.
3. PSD: reg add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System /v EnableInstallerDetection /t REG_DWORD /d 1 /f
   task dbl-detect         run as tester, level limited                 (no prompt expected; if an elevation
                           prompt appears WITHOUT a marker file, that is the finding: installer detection elevated
                           the program at its start; take one screenshot, answer No with Alt+N, report it)
   PSD: set EnableInstallerDetection back to the value phase 1 recorded (results\uac-settings.txt; "absent":
        reg delete ... /v EnableInstallerDetection /f)
4. Restart with automatic sign-in of yk2 (Yk2-test-pw1).
5. task ots-install        run as yk2, level limited, pw Yk2-test-pw1   (1 prompt: credentials; screenshot 2)
6. PSD  d69-repair         (as <admin>, while yk2 stays signed in at the console; no task)
7. task d69-signed-in      run as yk2, level limited
8. Restart with automatic sign-in of yk2.
9. task d69-after-sign-in  run as yk2, level limited                    (1 prompt: credentials, at the removal
                           through the entry's command)
10. Restart with automatic sign-in of tester (<adminpw>).
11. task after-ots         run as tester, level highest
12. task only-prepare      run as tester, level highest
13. Restart with automatic sign-in of tester.
14. task only-check        run as tester, level highest
15. task deferred-prepare  run as tester, level highest
16. Restart with automatic sign-in of tester.
17. task deferred-check    run as tester, level highest
<checker> is instcheck.exe in every step.

What the phases check (one PASS/FAIL line each in the result file):
  uac-settings                      UAC on; the policy values as found
  dbl-admin-install                 demo-setup.exe started as Explorer starts it (ShellExecute "open", the
                                    administrator's unelevated token): question "Install the keyboard layout ...?"
                                    pressed Yes, consent prompt, final message "... is installed", exit 0; files,
                                    KLID, uninstall entry, the layout in this user's list; typing in a 64- and a
                                    32-bit process
  dbl-admin-uninstall-entry         the uninstall entry's command (UninstallString, the command Settings runs)
                                    started as this user: question pressed Yes, consent prompt, exit 0 (or 3010);
                                    nothing left. Settings itself: the w11settings bundle, by hand
  installer-detection-asinvoker     with EnableInstallerDetection = 1 the program's question still comes before any
                                    elevation prompt (its manifest asks for asInvoker); No gives the cancel code
  ots-install                       yk2 (standard user) double-clicks; credential prompt with tester's password;
                                    exit 0; the layout in yk2's list (not tester's); typing as yk2
  ots-admin-list-untouched          tester's hive does not name the layout; yk2's hive is loaded (signed in)
  d69-repair-while-second-user-signed-in  the Layout Id given to KLID b0990409, the layout's KLID key deleted,
                                    setup.exe /f /quiet by tester: a new Layout Id; yk2's entries unchanged
  d69-second-user-session-after-repair    yk2's running session: the layout still listed; which HKL a new process
                                    sees (recorded, D69 says "sign out and in")
  d69-second-user-after-sign-in     after the restart: yk2 has the layout with the NEW HKL in this and a new
                                    process and types with it (64- and 32-bit)
  ots-uninstall-entry               yk2 runs the uninstall entry's command, credentials of tester; exit 0 or 3010;
                                    gone from yk2's list, KLID and uninstall entry gone
  ots-nothing-left-after-restart    after the restart as tester: b0990409 deleted, the machine as before step 2
  only-layout-repair                demo installed for tester and US removed, so demo is tester's only layout; the
                                    repair with a taken Layout Id adds it without removing it first (log)
  only-layout-after-sign-in         after the restart: the new HKL in this and a new process, typing
  only-layout-cleanup               US added back, b0990409 deleted, uninstalled, nothing left
  deferred-delete-uninstall         minimal-setup.exe installed; both layout files held open by a helper (no delete
                                    sharing); uninstall gives 3010, both files pending for the restart (the stored
                                    paths are recorded: how a path through Sysnative is stored), KLID and uninstall
                                    entry gone at once; three probe files marked from a 32-bit process (through
                                    Sysnative, through System32 with redirection off, in SysWOW64)
  deferred-delete-after-restart     after the restart: both files gone from System32 and SysWOW64, nothing pending,
                                    nothing left; installing and removing again works
  probe-delay-paths                 which probe files the restart deleted (recorded)
` + fmt.Sprintf(resultsBottom, "w11x64", "w11x64", "w11x64"),

	"w10x64": `yakbd installer test bundle, real-user paths: guest w10x64 (Windows 10 22H2 x64)
=====================================================================================

The hardening installer scenarios, the sign-in check after a repair, a layout file in use at uninstall, and the
typing run of every corpus layout, on Windows 10 x64.

` + commonTop + w10Steps("instcheck.exe", true) + fmt.Sprintf(resultsBottom, "w10x64", "w10x64", "w10x64"),

	"w10x86": `yakbd installer test bundle, real-user paths: guest w10x86 (Windows 10 22H2 x86, 32-bit)
=============================================================================================

The hardening installer scenarios on native 32-bit Windows (the x86 build in System32, no SysWOW64: the x86-only
installer installs, the x64-only one refuses), the sign-in check after a repair, a layout file in use at
uninstall, and the typing run of every corpus layout's x86 DLL. The checker is instcheck-32.exe (instcheck.exe is
the 64-bit build and does not run here). "64-bit" in a result line means the checker's own process, which is
x86 here.

` + commonTop + w10Steps("instcheck-32.exe", false) + fmt.Sprintf(resultsBottom, "w10x86", "w10x86", "w10x86"),
}

func w10Steps(checker string, x64 bool) string {
	typing := `7. task: run as tester, level highest, instead of the checker:
       C:\yk\vm\yakbd-wincheck-64.exe -bundle C:\yk\vm -out C:\yk\results\vm-64.txt
   then, when vm-64.txt has its count line, the same with yakbd-wincheck-32.exe and vm-32.txt.
`
	if !x64 {
		typing = `7. task: run as tester, level highest, instead of the checker:
       C:\yk\vm\yakbd-wincheck-32.exe -bundle C:\yk\vm -out C:\yk\results\vm-32.txt
`
	}
	return `
0. Setup, through PowerShell Direct as <admin>: copy this folder to C:\yk; mkdir C:\yk\results.
1. task (default phase: give no -phase)  run as tester, level highest; result file instcheck.txt (15 scenarios of
   the hardening bundle; the x86 guest runs x86-only-installer-on-x86 and refuse-x64-only-on-x86 instead of the
   x86/x64 pair). The task line is the one above without "-phase <phase>" and with
   > C:\yk\results\console-instcheck.txt.
2. task reenable-prepare   run as tester, level highest
3. task deferred-prepare   run as tester, level highest
4. Restart with automatic sign-in of tester (<adminpw>).
5. task deferred-check     run as tester, level highest
6. task reenable-check     run as tester, level highest
` + typing + `<checker> is ` + checker + ` in every step. No elevation prompt appears in this bundle (every task runs
elevated) and no screenshot is needed.

Phases: instcheck (the hardening scenarios, README of the hardening bundle); reenable-prepare / reenable-check (a
repair that takes a new Layout Id, then after the restart the new HKL in the session and typing); deferred-prepare
/ deferred-check (minimal-setup.exe's files held open at uninstall: 3010, the files pending; after the restart
gone, nothing left, install and remove again work); vm-64 / vm-32 (yakbd-wincheck, one line per corpus layout:
the x64 and WOW64 DLLs on x64 Windows, the x86 DLLs on x86 Windows).
`
}
