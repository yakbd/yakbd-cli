package main

import (
	"fmt"
	"strings"
)

func readme(n int, skipped []string) string {
	var b strings.Builder
	b.WriteString(`yakbd MSKLC comparison
==========================================

Guest: w11x64 ONLY (a disposable Windows 11 x64 guest; take a checkpoint first). MSKLC never runs on the host.
No screenshots are needed. KbdEdit is NOT part of this bundle: do not download it (docs/dll-import.md §6.4).

What it does: every .klc of cases\ (the tool's corpus, targeted cases, and an extra set x-*: the corpus's kbl
examples and CLDR keyboards as yakbd itself exports them to .klc) is
built inside the guest by MSKLC 1.4's compiler kbdutool.exe for amd64 (-m), WOW64 (-o) and x86 (-x), and by
yakbd.exe; yakbd-wincheck loads and types both DLL sets against the same expectations (64-bit runner = the x64
DLLs, 32-bit runner = the WOW64 DLLs); every MSKLC DLL is imported by yakbd.exe and compared with the import of
its .klc. The x86 builds are imported and compared, not typed: a 32-bit process on 64-bit Windows loads the
WOW64 file, and MSKLC does not install on 32-bit Windows (reference spec, MSKLC observations I33, I40).

Only text that our own programs write leaves the guest (results\*.txt): exit codes, the NUMBERS of kbdutool's
errors and warnings (never its messages), yakbd's loss-kind ids, PASS/FAIL lines and differences of our own test
layouts. MSKLC's DLLs, its install log and everything else stay in the guest and go away with the checkpoint.

1. Host: download MSKLC 1.4 from Microsoft's download centre
       https://www.microsoft.com/en-us/download/details.aspx?id=102134
   (file MSKLC.exe, https://download.microsoft.com/download/6/f/5/6f5ce43a-e892-4fd1-b9a6-1a0cbb64e6e2/MSKLC.exe ,
   into a new empty folder). Do not run it on the host. The reference runs installed MSKLC.msi from that download
   with msiexec; to get it, open MSKLC.exe as an archive on the host without running it (7-Zip: 7z x MSKLC.exe
   -oMSKLC). If that does not give MSKLC.msi, copy MSKLC.exe into the guest and run it THERE once: it unpacks its
   files into a folder you choose (C:\msklc-setup).
2. Guest: copy this folder to C:\tm and MSKLC.msi (or the unpacked folder) to C:\msklc-setup.
3. Guest, elevated: the reference x64 guest had .NET Framework 3.5 enabled (the MSKLC editor needs it; the compiler
   may not, but enable it the same way if you can): with the Windows installation ISO attached as D:
       dism /online /enable-feature /featurename:NetFx3 /all /source:D:\sources\sxs /limitaccess
   Then install MSKLC silently and note the exit code (0 expected; the log stays in the guest):
       msiexec /i C:\msklc-setup\MSKLC.msi /qn /l*v C:\msklc-install.log
       echo %ERRORLEVEL%
   Check that one of these exists:
       C:\Program Files (x86)\Microsoft Keyboard Layout Creator 1.4\bin\i386\kbdutool.exe
       C:\Program Files\Microsoft Keyboard Layout Creator 1.4\bin\i386\kbdutool.exe
   (If MSKLC sits elsewhere, add -kbdutool <path> to the command in step 4.)
4. Run the driver in the interactive session of the signed-in user (the typing step needs the desktop:
   LoadKeyboardLayoutW returns NULL without one). From a remote session as a scheduled task:

       schtasks /create /tn tm /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\tm\tm-guest.exe -bundle C:\tm > C:\tm\results-console.txt 2>&1"
       schtasks /run /tn tm

   or at the console in an elevated Command Prompt:  C:\tm\tm-guest.exe -bundle C:\tm
   <user> is the account signed in at the console. It is done when C:\tm\results\log.txt ends with the line
   "tm-guest: done" (about 20 to 60 minutes; each kbdutool or yakbd run has a limit of 3 minutes, each typing run
   of 2 hours). Steps can be repeated alone: -steps build,type,import (or one of control, build, type, import);
   -only <text> limits the run to cases whose names contain the text.
5. Copy out of the guest ONLY these text files (nothing from C:\tm\work):
       C:\tm\results\control.txt  build.txt  type-msklc-64.txt  type-msklc-32.txt  type-yakbd-64.txt
       type-yakbd-32.txt  import.txt  log.txt     and C:\tm\results-console.txt
   and the msiexec exit code of step 3.
6. Restore the checkpoint.

Result files
------------
control.txt   yakbd's own DLLs (x64, wow64, x86) imported again and compared with the .klc import: the harness
              control; it lists only FAIL and SKIP lines and should end with "control: N passed, 0 failed, ...".
build.txt     per case: what kbdutool built per mode (exit code, error/warning numbers) and whether yakbd built it,
              with the reference specification's prediction in [expected: ...]; counts at the end.
type-*.txt    yakbd-wincheck's usual results ("PASS name" / "FAIL name: first differences", a count, details) for
              MSKLC's DLLs and yakbd's DLLs typed against yakbd's expectations of the same .klc.
import.txt    per MSKLC DLL: PASS/FAIL of the import (semantic = kbldiff of the two .kbl.json, round trip = tables of
              the MSKLC DLL against yakbd's rebuild of its import), the import's loss kinds, and as information
              how MSKLC's tables differ from yakbd's for the same .klc; counts at the end.
log.txt       the driver's progress lines and the runners' start-up errors, if any.

`)
	fmt.Fprintf(&b, "Cases in this bundle: %d (cases.txt lists them with their sources and expectations).\n", n)
	if len(skipped) > 0 {
		b.WriteString("Not in the bundle:\n")
		for _, s := range skipped {
			b.WriteString("  " + s + "\n")
		}
	}
	return b.String()
}
