package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeARM64 writes the ARM64 bundle: the demo layout's installer with every build (x64, arm64x, wow64, x86), its
// expectations, the checker for native ARM64, x64 and 32-bit processes, and README.txt.
func writeARM64(out string) error {
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
	if err := yakbd("installer", "-q", "-build", "x64,arm64,x86", "-o", filepath.Join(out, "demo-setup.exe"), demo); err != nil {
		return err
	}
	if err := expectations(demo, tmp, filepath.Join(out, "demo.expect.txt")); err != nil {
		return err
	}
	if err := checkers(out, [][2]string{{"arm64", "instcheck.exe"}, {"amd64", "instcheck-x64.exe"}, {"386", "instcheck-32.exe"}}); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "README.txt"), []byte(readmeARM64), 0o644)
}

const readmeARM64 = `yakbd installer test bundle for Windows on ARM
==================================================================

ONLY inside the disposable Windows 11 ARM64 guest (a fresh overlay), never on a working PC.

demo-setup.exe is the all-builds installer of example (b) latin-altgr-deadkeys (x64, arm64x, wow64, x86; enables
for the installing user). On ARM64 it must put the ARM64X file into System32 and the WOW64 file into SysWOW64
(installation section 2.4). instcheck.exe is the native ARM64 checker; instcheck-x64.exe and instcheck-32.exe are
typing helpers it starts itself.

Every step runs ELEVATED IN THE INTERACTIVE CONSOLE SESSION of the signed-in administrator (a scheduled task with an
interactive logon and highest privileges, as in the earlier runs), from C:\yk:

1. Copy this folder into the guest as C:\yk.
2. Install and check:          instcheck.exe -bundle C:\yk -phase arm64-install
3. Cold boot: set automatic sign-in again; the guest switches it off and deletes DefaultPassword after each
   automatic sign-in, so write both (the administrator's password of the guest in place of <password>):
       reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoAdminLogon /t REG_SZ /d 1 /f
       reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultPassword /t REG_SZ /d <password> /f
   then power off (shutdown /s /t 0) and start the VM again from the host. Do not restart from inside the guest
   (it hangs in the firmware). Wait for the automatic sign-in and about one minute (ctfmon.exe running).
4. After sign-in:              instcheck.exe -bundle C:\yk -phase arm64-check
5. Uninstall and check:        instcheck.exe -bundle C:\yk -phase arm64-uninstall
6. Copy C:\yk\results\ out: arm64-install.txt, arm64-check.txt, arm64-uninstall.txt (PASS/FAIL lines and a count
   each), the matching *-details.txt, logs\*.log (the installer's own logs); arm64-state.txt and arm64-base.txt
   carry state between the phases.

Result lines:
  arm64-install                 exit 0; System32 file = the arm64x build, SysWOW64 file = the wow64 build; KLID,
                                values, uninstall entry; the layout in the user's list
  arm64-after-cold-boot         after the cold boot the KLID is in the user's list and its HKL is in the session
                                (GetKeyboardLayoutList); notes whether ctfmon.exe runs
  arm64-type-native             every expectation line through ToUnicodeEx in the native ARM64 checker
  arm64-type-x64                the same in an x64 process (System32 ARM64X file, x64 view)
  arm64-type-x86                the same in a 32-bit process (the SysWOW64 file)
  arm64-uninstall-leaves-nothing  the installed setup.exe /uninstall /quiet: exit 0, or 3010 with the files pending
                                deletion at restart (the line lists the stored pending paths); nothing else left
`
