package main

import "fmt"

func init() { guestReadme["w11settings"] = readmeSettings }

// readmeSettings is the README of the bundle that checks the removal in Settings > Installed apps by hand: does
// Settings' Uninstall run our uninstall entry, for an administrator and for a standard user. UI Automation of
// Settings failed in three VM runs and was dropped (docs/installer.md 8.2); the runner presses the buttons.
var readmeSettings = `yakbd installer test bundle, real-user paths: guest w11x64, removal in Settings, by hand
=====================================================================================

One question: does Settings > Apps > Installed apps > (entry) ... > Uninstall run our uninstall entry, for an
administrator (consent prompt) and for a standard user (an administrator's credentials)? The runner presses
Settings' buttons; the checker checks the installer's log and that nothing is left.

` + commonTop + `
0. Setup, through PowerShell Direct as <admin>: copy this folder to C:\yk; mkdir C:\yk\results;
       icacls C:\yk /grant "Users:(OI)(CI)M"
       New-LocalUser -Name yk2 -Password (ConvertTo-SecureString 'Yk2-test-pw1' -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires
       Add-LocalGroupMember -Group Users -Member yk2
1. PSD  settings-prepare                      (installs the demo layout quietly, enabled for nobody)
2. M with <account> = tester, then
   PSD  C:\yk\instcheck.exe -bundle C:\yk -phase settings-manual-check -out C:\yk\results\settings-manual-tester.txt
3. PSD  settings-prepare2                     (installs it again)
4. Restart with automatic sign-in of yk2 (Yk2-test-pw1); wait for the desktop as described above.
5. M with <account> = yk2, then
   PSD  C:\yk\instcheck.exe -bundle C:\yk -phase settings-manual-check -out C:\yk\results\settings-manual-yk2.txt
6. PSD  C:\yk\instcheck.exe -bundle C:\yk -phase summary -guest w11settings

M. By hand (at most 3 screenshots in all; host keyboard and mouse into the VM's console):
   a. Delete the account's old installer logs (the uninstaller runs elevated, as tester, so its log is in tester's
      temporary folder whoever presses Uninstall):  del C:\Users\tester\AppData\Local\Temp\yakbd-kbldemo-setup.log
      Start Settings on the account's desktop:
          schtasks /create /tn yks /sc once /st 00:00 /ru <account> /rp <password> /it /rl limited /f /tr "explorer.exe ms-settings:appsfeatures"
          schtasks /run /tn yks
   b. In the list find "Latin AltGr demo (example)" (type its name into the "Search" box above the list if it is not
      visible), click the "..." button at the right end of that row, "Uninstall" in the menu, then "Uninstall" in the
      small confirmation below it.
   c. Settings asks for elevation first: administrator Alt+Y; standard user: type <adminpw>, Enter. Then our question
      "Remove the keyboard layout ...?": Enter (Yes). The message "... was removed.": Enter.
   d. copy C:\Users\tester\AppData\Local\Temp\yakbd-kbldemo-setup.log C:\yk\results\logs\manual.log
   settings-manual-check passes when that log has a run started with the entry's command (/x {product code}) that
   ended with 0 or 3010, and the entry, the KLID and the Program Files folder are gone.
` + fmt.Sprintf(resultsBottom, "w11settings", "w11settings", "w11settings")
