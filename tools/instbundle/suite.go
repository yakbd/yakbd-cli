package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/toolsetup"
	"github.com/yakbd/yakbd-cli/internal/relbuild"
)

// writeSuite writes the bundle of the two YAKBD installers (docs/installer-cli.md §12.7): the clean-machine bundle of
// the release (writeClean, ci\ from the draft), a yakbd-gui-setup.exe built here from this checkout with a dummy
// editor (gui\, labelled as such), the published installers of the earlier layout from old (old\, each checked
// against its SHA256SUMS), and a driver that runs the clean checks, the silent state-table subset and the eight
// wizard states with a screenshot each.
func writeSuite(out, assets, old string) error {
	if old == "" {
		return fmt.Errorf("-guest w11suite needs -old DIR (cli-v0.1.0-preview.1, cli-v0.1.0-preview.2, gui-v0.1.0-preview.1, each with yakbd-setup.exe and SHA256SUMS)")
	}
	if err := writeClean(out, assets); err != nil {
		return err
	}
	for _, rel := range []string{"cli-v0.1.0-preview.1", "cli-v0.1.0-preview.2", "gui-v0.1.0-preview.1"} {
		b, err := os.ReadFile(filepath.Join(old, rel, "yakbd-setup.exe"))
		if err != nil {
			return err
		}
		sums, err := os.ReadFile(filepath.Join(old, rel, "SHA256SUMS"))
		if err != nil {
			return err
		}
		if !strings.Contains(string(sums), fmt.Sprintf("%x  yakbd-setup.exe", sha256.Sum256(b))) {
			return fmt.Errorf("%s: yakbd-setup.exe does not match its SHA256SUMS", rel)
		}
		dir := filepath.Join(out, "old", rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "yakbd-setup.exe"), b, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), sums, 0o644); err != nil {
			return err
		}
	}
	ed := &relbuild.EditorInput{Version: "0.1.0-preview.3", Files: map[string][]toolsetup.Input{},
		Shortcuts: []toolsetup.Shortcut{{Name: "YAKBD Editor", Target: "yakbd-gui.exe"}},
		UserData: []toolsetup.UserData{
			{Kind: "key", Path: `HKCU\Software\YAKBD\YAKBD Editor`, ParentIfEmpty: true},
			{Kind: "dir", Path: `%LOCALAPPDATA%\YAKBD\YAKBD Editor`, ParentIfEmpty: true},
		}}
	for _, b := range []string{toolsetup.BuildX64, toolsetup.BuildARM64} {
		exe, err := relbuild.Build(".", "./tools/instbundle/dummyeditor", "windows", relbuild.WindowsArch[b], true, "")
		if err != nil {
			return err
		}
		ed.Files[b] = []toolsetup.Input{
			{Path: `qt\qt.conf`, Data: []byte("[Paths]\r\nPrefix = .\r\n")},
			{Path: `qt\README.txt`, Data: []byte("A test payload of the installer bundle, not the editor.\r\n")},
			{Path: "yakbd-gui.exe", Data: exe},
		}
	}
	gui, exes, err := relbuild.SetupWith(".", relbuild.SetupOptions{Version: cleanVersionOf(out), Release: cleanVersionOf(out), Editor: ed})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(out, "gui"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "gui", relbuild.GUISetupName), gui, 0o644); err != nil {
		return err
	}
	// The gui setup's CLI must be the draft's: built from the draft's commit, its yakbd.exe equals the one inside
	// ci\yakbd-cli-setup.exe.
	ciSetup, err := os.ReadFile(filepath.Join(out, "ci", relbuild.CLISetupName))
	if err != nil {
		return err
	}
	cp, err := toolsetup.FromExe(ciSetup)
	if err != nil {
		return err
	}
	ciFiles, err := cp.FilesFor(toolsetup.CLI, toolsetup.BuildX64)
	if err != nil {
		return err
	}
	for _, f := range ciFiles {
		if f.Path == toolsetup.CLIExe && !bytes.Equal(f.Data, exes[toolsetup.BuildX64]) {
			return fmt.Errorf("the gui setup's yakbd.exe differs from the draft's: run instbundle from a checkout at the draft's commit")
		}
	}
	x64 := sha256.Sum256(exes[toolsetup.BuildX64])
	built, _ := os.ReadFile(filepath.Join(out, "BUILT-FROM.txt"))
	built = append(built, []byte(strings.ReplaceAll(fmt.Sprintf("gui\\yakbd-gui-setup.exe: built by instbundle -guest w11suite from this checkout with a DUMMY editor "+
		"(tools/instbundle/dummyeditor, x64 and arm64), not the real editor; its yakbd.exe x64 %x\n"+
		"old\\: the published yakbd-setup.exe of yakbd-cli v0.1.0-preview.1, v0.1.0-preview.2 and yakbd-gui v0.1.0-preview.1, checked against their SHA256SUMS\n", x64[:8]), "\n", "\r\n"))...)
	if err := os.WriteFile(filepath.Join(out, "BUILT-FROM.txt"), built, 0o644); err != nil {
		return err
	}
	crlf := func(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }
	v := cleanVersionOf(out)
	if err := os.WriteFile(filepath.Join(out, "drive.ps1"), crlf(driveSuitePS1), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "README.txt"), crlf(strings.ReplaceAll(cleanReadme, "__VERSION__", v)+suiteReadme), 0o644)
}

func cleanVersionOf(out string) string {
	b, _ := os.ReadFile(filepath.Join(out, "ci", "VERSION.txt"))
	return strings.TrimSpace(string(b))
}

var driveSuitePS1 = strings.Replace(strings.Replace(strings.Replace(driveHead, "sign-in driver", "YAKBD installers driver", 1),
	"if (-not $Out) { $Out = Join-Path $Bundle 'host-results' }", strings.ReplaceAll(plantOut, "yakbd-tr-planting", "yakbd-tr-suite"), 1),
	"function Fresh {", shotPlain+"\nfunction Fresh {", 1) + `
try {
  $s = Fresh
  Task $s clean-install $Admin
  Task $s clean-use $Admin
  Task $s clean-uninstall $Admin
  Task $s suite-silent $Admin
  foreach ($n in 1..8) {
    Task $s "suite-state-$n" $Admin
    Wizard $s $n $Admin
  }
  Collect $s legS w11suite
  Remove-PSSession $s
} catch {
  $Problems.Add("driver stopped: $($_.Exception.Message) at $($_.InvocationInfo.ScriptLineNumber)")
  Log "ERROR $($_.Exception.Message)"
}
try { Stop-VM -Name $VMName -TurnOff -Force; Restore-VMCheckpoint -VMName $VMName -Name $Checkpoint -Confirm:$false } catch { $Problems.Add("restore at the end: $($_.Exception.Message)") }

` + strings.Replace(driveTail, "__LEGS__", "@(,@('legS', 'w11suite'))", 1)

// shotPlain takes one screenshot of the guest's screen (no clicks), and Wizard runs one wizard phase: the checker in
// a task as the signed-in administrator, not elevated, the screenshot when it says the wizard is open, then the
// marker that lets it press Cancel.
const shotPlain = `function ShotPlain($name) {
  $vm = VMObject
  $head = Get-CimAssociatedInstance -InputObject $vm -ResultClassName Msvm_SyntheticDisplayController | ForEach-Object { Get-CimAssociatedInstance -InputObject $_ -ResultClassName Msvm_VideoHead } | Select-Object -First 1
  $w = [int]$head.CurrentHorizontalResolution; $h = [int]$head.CurrentVerticalResolution
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
  Log "screenshot $path ($w x $h)"
}
function Wizard($s, $n, $user) {
  $phase = "suite-wizard-$n"
  Log "phase $phase (task as $user, not elevated)"
  InGuest $s { param($p, $u)
    $action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument "/c C:\yk\instcheck.exe -bundle C:\yk -phase $p > C:\yk\results\console-$p.txt 2>&1"
    $principal = New-ScheduledTaskPrincipal -UserId "$env:COMPUTERNAME\$u" -LogonType Interactive -RunLevel Limited
    Unregister-ScheduledTask -TaskName ykw -Confirm:$false -ErrorAction SilentlyContinue
    Register-ScheduledTask -TaskName ykw -Action $action -Principal $principal | Out-Null
    Start-ScheduledTask -TaskName ykw } @($phase, $user)
  $ready = $false
  for ($i = 0; $i -lt 60 -and -not $ready; $i++) { Start-Sleep 2; $ready = InGuest $s { param($n) Test-Path "C:\yk\results\wizard-$n-ready.txt" } @($n) }
  if ($ready) { Start-Sleep 2; ShotPlain "wizard-$n" } else { $Problems.Add("$phase never opened its wizard"); Log "no wizard $n" }
  InGuest $s { param($n) Set-Content "C:\yk\results\wizard-$n-shot.txt" 'taken' } @($n)
  $done = $false
  for ($i = 0; $i -lt 60 -and -not $done; $i++) { Start-Sleep 2; $done = InGuest $s { param($p) (Test-Path "C:\yk\results\$p.txt") -and ((Get-Content "C:\yk\results\$p.txt" -Tail 1) -match 'passed, \d+ failed$') } @($phase) }
  if (-not $done) { $Problems.Add("$phase timed out") }
}`

const suiteReadme = `
THEN, the two YAKBD installers (results in the same summary.txt after the clean block, legS):

gui\yakbd-gui-setup.exe is built from this checkout with a DUMMY editor (tools/instbundle/dummyeditor; BUILT-FROM.txt),
because the editor's own release is made from this tool's tagged release. old\ holds the published installers of
the earlier layout (yakbd-cli 0.1.0-preview.1 and preview.2, yakbd-gui 0.1.0-preview.1).

  suite-cli-setup-installs-cli-on-path                 ci\yakbd-cli-setup.exe /quiet: YAKBD\CLI\yakbd.exe, LICENSE,
                                                       NOTICE, the CLI folder on PATH, one entry "YAKBD"; /x: nothing left
  suite-gui-setup-installs-both-path-off               gui setup /quiet: CLI + editor (shortcut read back), no PATH
  suite-gui-setup-cli-only                             gui setup ADDLOCAL=CLI: the CLI alone
  suite-gui-over-cli-keeps-path-cli-over-both-keeps-editor
                                                       cli, then gui over it (PATH kept), cli over both (editor
                                                       kept), REMOVE=Editor through the entry's setup.exe
  suite-addtopath-on-and-off                           ADDTOPATH=1 then ADDTOPATH=0
  suite-refusals-change-nothing                        ADDLOCAL=Editor alone, the editor from the CLI installer,
                                                       REMOVE=CLI with the editor: 1639, nothing changed
  suite-takeover-from-cli-v0.1.0-preview.1, -preview.2 the published installer, then the new one: the earlier
                                                       entry, folder and PATH entry gone, the CLI in YAKBD\CLI on PATH
  suite-takeover-from-gui-v0.1.0-preview.1             the CLI installer refuses (1603, nothing changed); the editor
                                                       installer takes over CLI + editor, PATH kept
  suite-new-x-removes-the-earlier-layout               the new /x on a preview.2 installation: nothing left
  suite-state-N / suite-wizard-N (N = 1..8)            each state prepared elevated, then the wizard opened as a
                                                       double-click does (not elevated), every control read
                                                       (results\wizard-N-controls.txt), the pre-ticked boxes
                                                       checked against the state, a screenshot
                                                       (screenshot-wizard-N.png), Cancel (exit 1602, nothing changed):
     1 CLI installer, nothing installed      2 editor installer, nothing installed
     3 editor installer over the CLI on PATH 4 CLI installer over CLI + editor (change)
     5 editor installer over the published editor 0.1.0-preview.1
     6 uninstall wizard, CLI + editor        7 the same with editor settings ("Remove my settings" shown)
     8 the uninstall wizard with the CLI box clicked (editor only: "Remove YAKBD CLI from PATH" shown)
About 20 minutes in all.
`
