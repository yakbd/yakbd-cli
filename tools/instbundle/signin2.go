package main

import "strings"

// runPhasePS1 starts one checker phase as a scheduled task with an interactive logon of an account signed in at the
// console, and waits for its result file. Run 1 of the first sign-in bundle: "schtasks /run" failed with "Element not found" for some
// tasks; Register-ScheduledTask with an interactive-logon principal worked.
const runPhasePS1 = `# yakbd bundle helper: run one instcheck phase in the console session of <User> (signed in there).
# Usage (PowerShell Direct, as the administrator):
#   powershell -ExecutionPolicy Bypass -File C:\yk\run-phase.ps1 -Phase <phase> -User <account> [-Limited]
param([Parameter(Mandatory)][string]$Phase, [Parameter(Mandatory)][string]$User, [switch]$Limited)
$ErrorActionPreference = 'Stop'
$res = "C:\yk\results\$Phase.txt"
Remove-Item $res -ErrorAction SilentlyContinue
$level = if ($Limited) { 'Limited' } else { 'Highest' }
$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument "/c C:\yk\instcheck.exe -bundle C:\yk -phase $Phase > C:\yk\results\console-$Phase.txt 2>&1"
if ($User -eq 'SYSTEM') { $principal = New-ScheduledTaskPrincipal -UserId SYSTEM -LogonType ServiceAccount -RunLevel Highest }
else { $principal = New-ScheduledTaskPrincipal -UserId "$env:COMPUTERNAME\$User" -LogonType Interactive -RunLevel $level }
Unregister-ScheduledTask -TaskName yk -Confirm:$false -ErrorAction SilentlyContinue
Register-ScheduledTask -TaskName yk -Action $action -Principal $principal | Out-Null
Start-ScheduledTask -TaskName yk
$deadline = (Get-Date).AddMinutes(30)
while ((Get-Date) -lt $deadline) {
  if ((Test-Path $res) -and ((Get-Content $res -Tail 1) -match 'passed, \d+ failed$')) { Get-Content $res; exit 0 }
  Start-Sleep -Seconds 10
}
Write-Output "TIMEOUT: $Phase has no count line after 30 minutes (task state: $((Get-ScheduledTask -TaskName yk).State))"
exit 1
`

// drivePS1 runs the whole bundle from the Hyper-V host, unattended: two legs, each on a fresh restore of the
// checkpoint, with the restarts, the new accounts' first sign-ins and the screenshots (Hyper-V WMI, public
// documentation: Msvm_VirtualSystemManagementService.GetVirtualSystemThumbnailImage gives raw RGB 565,
// Msvm_SyntheticMouse.SetAbsolutePosition takes pixels, ClickButton a 1-based button, Msvm_Keyboard.TypeKey).
// drivePS1 is the second sign-in bundle's driver: driveHead, its two legs, driveTail.
var drivePS1 = driveHead + driveLegs2 + strings.Replace(driveTail, "__LEGS__", "@(@('legA', 'w11signin2'), @('legB', 'w11q14'))", 1)

const driveHead = `# yakbd sign-in driver: run on the Hyper-V host, elevated, once, in the background. It needs nothing else.
#   powershell -ExecutionPolicy Bypass -File <bundle>\drive.ps1 -VMName <vm> -AdminUser <admin> -AdminPassword <pw>
# Writes <bundle>\host-results\: summary.txt (the only file to report), driver.log, legA\ and legB\ (the guest's
# results folders: every readback), screenshot-*.png.
param([Parameter(Mandatory)][string]$VMName, [Parameter(Mandatory)][string]$AdminUser,
      [Parameter(Mandatory)][string]$AdminPassword, [string]$Checkpoint = 'base', [string]$Out = '')
$ErrorActionPreference = 'Stop'
$Bundle = $PSScriptRoot
if (-not $Out) { $Out = Join-Path $Bundle 'host-results' }
New-Item -ItemType Directory -Force $Out | Out-Null
$Admin = ($AdminUser -split '\\')[-1]
$Cred = New-Object System.Management.Automation.PSCredential($AdminUser, (ConvertTo-SecureString $AdminPassword -AsPlainText -Force))
$Users = @{ $Admin = $AdminPassword; 'yk3' = 'Yk3-test-pw1'; 'yk4' = 'Yk4-test-pw1'; 'yk5' = 'Yk5-test-pw1' }
$Problems = New-Object System.Collections.Generic.List[string]

function Log($m) { "$(Get-Date -Format s) $m" | Tee-Object -FilePath (Join-Path $Out 'driver.log') -Append | Out-Host }
function NewSession {
  for ($i = 0; $i -lt 180; $i++) {
    try { return New-PSSession -VMName $VMName -Credential $Cred -ErrorAction Stop } catch { Start-Sleep 5 }
  }
  throw 'no PowerShell Direct session after 15 minutes'
}
function InGuest($s, [scriptblock]$b, $a = @()) { Invoke-Command -Session $s -ScriptBlock $b -ArgumentList $a }
function WaitDesktop($user) {
  for ($i = 0; $i -lt 120; $i++) {
    $s = NewSession
    $u = InGuest $s { Get-Process explorer -IncludeUserName -ErrorAction SilentlyContinue | ForEach-Object { $_.UserName } }
    if ($u | Where-Object { $_ -like "*\$user" }) { Start-Sleep 120; return $s }
    Remove-PSSession $s; Start-Sleep 10
  }
  throw "explorer.exe of $user did not start"
}
function WaitSignInScreen {
  for ($i = 0; $i -lt 120; $i++) {
    $s = NewSession
    $r = InGuest $s { [bool](Get-Process LogonUI -ErrorAction SilentlyContinue) -and -not (Get-Process explorer -ErrorAction SilentlyContinue) }
    Remove-PSSession $s
    if ($r) { Start-Sleep 60; return }
    Start-Sleep 10
  }
  throw 'the sign-in screen did not appear'
}
function Restart($s, $user) {
  # $user: the account to sign in automatically, or '' for the sign-in screen.
  InGuest $s {
    param($u, $p)
    $w = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
    if ($u) {
      Set-ItemProperty $w AutoAdminLogon '1'; Set-ItemProperty $w DefaultUserName $u
      Set-ItemProperty $w DefaultPassword $p; Set-ItemProperty $w DefaultDomainName $env:COMPUTERNAME
    } else { Set-ItemProperty $w AutoAdminLogon '0' }
    Remove-ItemProperty $w AutoLogonCount -ErrorAction SilentlyContinue
    shutdown /r /t 5
  } @($user, $Users[$user])
  Remove-PSSession $s
  Start-Sleep 60
  if ($user) { return WaitDesktop $user }
  WaitSignInScreen
}
function Phase($s, $phase) {
  Log "phase $phase (PowerShell Direct)"
  InGuest $s { param($p) & C:\yk\instcheck.exe -bundle C:\yk -phase $p } @($phase) | Out-Host
}
function Task($s, $phase, $user, [switch]$Limited) {
  Log "phase $phase (task as $user)"
  $r = InGuest $s { param($p, $u, $l) if ($l) { & powershell -ExecutionPolicy Bypass -File C:\yk\run-phase.ps1 -Phase $p -User $u -Limited } else { & powershell -ExecutionPolicy Bypass -File C:\yk\run-phase.ps1 -Phase $p -User $u } } @($phase, $user, [bool]$Limited)
  $r | Out-Host
  if ($r -match 'TIMEOUT') { $Problems.Add("$phase timed out") }
}
function NewUser($s, $name) {
  InGuest $s { param($n, $p) New-LocalUser -Name $n -Password (ConvertTo-SecureString $p -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires | Out-Null; Add-LocalGroupMember -Group Users -Member $n } @($name, $Users[$name])
}
function Cim($class) { Get-CimInstance -Namespace root\virtualization\v2 -ClassName $class }
function VMObject { Get-CimInstance -Namespace root\virtualization\v2 -ClassName Msvm_ComputerSystem -Filter "ElementName='$VMName'" }
function Shot($name, [switch]$Lock) {
  # The keyboard list of the lock or sign-in screen: (Space on the lock screen,) a click on the language indicator
  # at the bottom right, then one screenshot.
  $vm = VMObject
  $head = Get-CimAssociatedInstance -InputObject $vm -ResultClassName Msvm_SyntheticDisplayController |  ForEach-Object { Get-CimAssociatedInstance -InputObject $_ -ResultClassName Msvm_VideoHead } | Select-Object -First 1
  $w = [int]$head.CurrentHorizontalResolution; $h = [int]$head.CurrentVerticalResolution
  if ($Lock) {
    $kb = Get-CimAssociatedInstance -InputObject $vm -ResultClassName Msvm_Keyboard
    Invoke-CimMethod -InputObject $kb -MethodName TypeKey -Arguments @{ keyCode = [uint32]0x20 } | Out-Null
    Start-Sleep 5
  }
  $mouse = Get-CimAssociatedInstance -InputObject $vm -ResultClassName Msvm_SyntheticMouse
  # Run 1 opened the list with a click at 837,727 of 1024x768 (run 2 at 854,723 only hovered): that point, scaled;
  # move first, wait, click, then wait for the list.
  $cx = [int]($w * 837 / 1024); $cy = [int]($h * 727 / 768)
  Invoke-CimMethod -InputObject $mouse -MethodName SetAbsolutePosition -Arguments @{ horizontalPosition = $cx; verticalPosition = $cy } | Out-Null
  Start-Sleep 2
  Invoke-CimMethod -InputObject $mouse -MethodName ClickButton -Arguments @{ buttonIndex = [uint32]1 } | Out-Null
  Start-Sleep 5
  Start-Sleep 5
  $svc = Cim Msvm_VirtualSystemManagementService
  $set = Get-CimAssociatedInstance -InputObject $vm -ResultClassName Msvm_VirtualSystemSettingData | Where-Object { $_.VirtualSystemType -eq 'Microsoft:Hyper-V:System:Realized' } | Select-Object -First 1
  $r = Invoke-CimMethod -InputObject $svc -MethodName GetVirtualSystemThumbnailImage -Arguments @{ TargetSystem = $set; WidthPixels = [uint16]$w; HeightPixels = [uint16]$h }
  Add-Type -AssemblyName System.Drawing
  $bmp = New-Object System.Drawing.Bitmap($w, $h, [System.Drawing.Imaging.PixelFormat]::Format16bppRgb565)
  $data = $bmp.LockBits((New-Object System.Drawing.Rectangle(0, 0, $w, $h)), 'WriteOnly', $bmp.PixelFormat)
  for ($y = 0; $y -lt $h; $y++) { [System.Runtime.InteropServices.Marshal]::Copy($r.ImageData, $y * $w * 2, [IntPtr]($data.Scan0.ToInt64() + $y * $data.Stride), $w * 2) }
  $bmp.UnlockBits($data)
  $path = Join-Path $Out "screenshot-$name.png"
  $bmp.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); $bmp.Dispose()
  Log "screenshot $path ($w x $h, click at $cx,$cy)"
}
function Fresh {
  Log "restore checkpoint $Checkpoint"
  Stop-VM -Name $VMName -TurnOff -Force -ErrorAction SilentlyContinue
  Restore-VMCheckpoint -VMName $VMName -Name $Checkpoint -Confirm:$false
  if ((Get-VM -Name $VMName).State -ne 'Running') { Start-VM -Name $VMName }
  $s = WaitDesktop $Admin
  InGuest $s { Remove-Item C:\yk -Recurse -Force -ErrorAction SilentlyContinue; New-Item -ItemType Directory C:\yk | Out-Null }
  Get-ChildItem $Bundle -Exclude host-results | ForEach-Object { Copy-Item -ToSession $s -Path $_.FullName -Destination C:\yk -Recurse -Force }
  InGuest $s { New-Item -ItemType Directory C:\yk\results | Out-Null; icacls C:\yk /grant 'Users:(OI)(CI)M' | Out-Null }
  return $s
}
function Collect($s, $leg, $guest) {
  InGuest $s { param($g) & C:\yk\instcheck.exe -bundle C:\yk -phase summary -guest $g } @($guest) | Out-Null
  $dst = Join-Path $Out $leg
  Remove-Item $dst -Recurse -Force -ErrorAction SilentlyContinue
  Copy-Item -FromSession $s -Path C:\yk\results -Destination $dst -Recurse -Force
}

`

const driveLegs2 = `
try {
  # Leg A: D97 with the 0438 layout (failure without a user, Windows' copy shape with enabling), VM-20, VM-23.
  $s = Fresh
  Phase $s si2-baseline
  Task $s si2-fail $Admin
  Task $s si2-install $Admin
  Task $s si-lock $Admin
  Start-Sleep 10
  Shot 'A1-lock' -Lock
  $s = NewSession
  Restart $s ''
  Shot 'A2-signin'
  $s = NewSession
  $s = Restart $s $Admin
  Task $s si2-after-restart $Admin
  NewUser $s yk3
  $s = Restart $s yk3
  Task $s si2-new-account yk3 -Limited
  $s = Restart $s $Admin
  Task $s si2-uninstall $Admin
  NewUser $s yk4
  $s = Restart $s yk4
  Task $s si2-after-uninstall yk4 -Limited
  $s = Restart $s $Admin
  Collect $s legA w11signin2
  Remove-PSSession $s

  # Leg B: Q14 (measurement): the installer and InstallLayoutOrTip as LocalSystem, no user has the language.
  $s = Fresh
  Phase $s si2-baseline
  Task $s q14-before $Admin
  Task $s q14-system SYSTEM
  NewUser $s yk5
  Restart $s ''
  Shot 'B1-signin'
  $s = NewSession
  $s = Restart $s yk5
  Task $s q14-new-account yk5 -Limited
  $s = Restart $s $Admin
  Task $s q14-cleanup $Admin
  Collect $s legB w11q14
  Remove-PSSession $s
} catch {
  $Problems.Add("driver stopped: $($_.Exception.Message) at $($_.InvocationInfo.ScriptLineNumber)")
  Log "ERROR $($_.Exception.Message)"
}
try { Stop-VM -Name $VMName -TurnOff -Force; Restore-VMCheckpoint -VMName $VMName -Name $Checkpoint -Confirm:$false } catch { $Problems.Add("restore at the end: $($_.Exception.Message)") }

`

const driveTail = `
$sum = New-Object System.Collections.Generic.List[string]
foreach ($leg in __LEGS__) {
  $f = Join-Path $Out "$($leg[0])\$($leg[1]).txt"
  if (Test-Path $f) { $sum.Add("== $($leg[0]) ($($leg[1]))"); Get-Content $f | ForEach-Object { $sum.Add($_) } } else { $sum.Add("== $($leg[0]): no summary (see driver.log)") }
}
$sum.Add("== screenshots: " + ((Get-ChildItem $Out -Filter 'screenshot-*.png' | ForEach-Object Name) -join ', '))
foreach ($p in $Problems) { $sum.Add("DRIVER PROBLEM $p") }
$sum | Set-Content (Join-Path $Out 'summary.txt')
Log 'done'
`

const signIn2Readme = `yakbd installer test bundle (second sign-in bundle): a language the sign-in screen lacks; Q14; guest w11x64
========================================================================================================

FOR THE RUNNER: start one command on the Hyper-V host (elevated PowerShell), in the background, wait for it, report
host-results\summary.txt. Nothing else is interactive.

    powershell -ExecutionPolicy Bypass -File <this folder>\drive.ps1 -VMName <the w11x64 guest> -AdminUser <admin> -AdminPassword <adminpw>

<admin> is the guest's administrator account (tester), signed in automatically at the console in the checkpoint
"base" (en-US, the US keyboard only; -Checkpoint <name> if it is named otherwise). The driver restores "base"
before each of its two legs and at the end, copies this folder to C:\yk, runs every phase (PowerShell Direct, and
scheduled tasks with an interactive logon through C:\yk\run-phase.ps1: in the last run "schtasks /run" failed with
"Element not found" for some tasks and Register-ScheduledTask worked), restarts with automatic sign-ins, creates
the accounts yk3, yk4, yk5 and waits for their first sign-in, and takes the screenshots. It takes about 1.5 hours.
Report: the whole of host-results\summary.txt (one PASS/FAIL line per check and a count line per leg, then the
screenshot names), and what each of the 3 screenshots shows: the names in the keyboard list of the lock or sign-in
screen (or that no list is open: the driver clicks at the bottom right, where the language indicator ("ENG") was in
the last run; the last run opened the list by LockWorkStation from a console task and a click on ENG, which the
driver repeats). host-results\legA, legB hold every readback (places-*.txt, diff-*.txt, hives-*.txt, ui-*.txt).

Screenshots (3): A1-lock (after the install, lock screen), A2-signin (after a restart, sign-in screen), B1-signin
(after the LocalSystem leg and a restart, sign-in screen). legA\layout-names.txt says which layout is which: fo
(0438) and minimal (0409, written by the test into .DEFAULT with its System Backup value and the Default profile
only, VM-23). Windows may list fo under its own name for KLID 00000438 ("Faeroese keyboard" in the last run).

What the legs do (kbl D97, spec b893f2e):
Leg A (fresh "base"):
  si-baseline                               the four places (HKU\.DEFAULT, S-1-5-19, S-1-5-20, the Default profile)
  d97-missing-language-without-user-fails   fo-signin-setup.exe /quiet ENABLE=0 SIGNIN=1: 1603, the layout installed,
                                            YakbdSignIn 0, the four places unchanged, the log names "/f SIGNIN=1";
                                            uninstalled again; the display language unchanged
  d97-missing-language-copy-shape           /quiet ENABLE=1 (tester's own elevated task: enables first, D97): fo in
                                            Languages and System Backup Languages, User Profile\fo value 1 (and under
                                            System Backup in the system hives), Preload 00000438; the display language
                                            unchanged (first list entry, PreferredUILanguages, Get-WinUILanguageOverride,
                                            the sign-in screen's UI language)
  d97-second-run-adds-nothing, vm23-prepare-with-system-backup
  d97-copy-shape-survives-restart           every line after the restart (places-after-restart.txt, diff-*.txt)
  vm23-with-system-backup-after-restart     recorded
  d97-new-account-has-language-and-layout   yk3 (made after the install): fo in its languages, loaded, typing
  d97-uninstall-*, d97-four-places-as-before  the four places equal the baseline, Languages included; display language
  vm20-new-account-after-uninstall          yk4 (made after the uninstall): first sign-in, no trace
Leg B (fresh "base"), Q14, measurement:
  q14-before                                tester has no fo; the display language recorded
  q14-installer-as-localsystem              the installer as SYSTEM, /quiet SIGNIN=1: 1603 (D97), places unchanged
  q14-installlayoutortip-as-localsystem     InstallLayoutOrTip("0438:<KLID>", 0) as SYSTEM: which hives changed
                                            (diff-q14-ilot.txt), the tag .DEFAULT got
  q14-locale-names                          LCIDToLocaleName / ResolveLocaleName for 0438 against that tag
  q14-new-account                           yk5, made then, after a restart: languages, loaded, typing (recorded)
  q14-after-restart, q14-uninstall          the places after the restart; the uninstall; the display language
`
