package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/relbuild"
	"github.com/yakbd/yakbd-cli/pe"
)

// plantPrefixCommit is the last tool commit whose installer programs loaded user32, ole32 and wtsapi32 by bare
// name (docs/installer.md §5.2); its installer is the bundle's "before the fix" case.
const plantPrefixCommit = "2063f05"

// writePlant writes the DLL planting bundle (docs/installer.md §8.4): the installers, the 32-bit marker DLL, both
// checkers, the host driver and README.txt.
func writePlant(out string) error {
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("%s exists", out)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := yakbd("installer", "-q", "-o", filepath.Join(out, "kbdxx-setup.exe"), minimal); err != nil {
		return err
	}
	setup, _, err := relbuild.Setup(".", cli.VersionString(), "")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "yakbd-setup.exe"), setup, 0o644); err != nil {
		return err
	}
	if err := prefixInstaller(filepath.Join(out, "kbdxx-prefix-setup.exe")); err != nil {
		return err
	}
	dll, err := markerDLL()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "plant-marker.dll"), dll, 0o644); err != nil {
		return err
	}
	if err := checkers(out, [][2]string{{"amd64", "instcheck.exe"}, {"386", "instcheck-32.exe"}}); err != nil {
		return err
	}
	head, _ := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err := os.WriteFile(filepath.Join(out, "BUILT-FROM.txt"), []byte("yakbd/yakbd-cli "+strings.TrimSpace(string(head))+"\n"), 0o644); err != nil {
		return err
	}
	crlf := func(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }
	if err := os.WriteFile(filepath.Join(out, "run-phase.ps1"), crlf(runPhasePS1), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "drive.ps1"), crlf(drivePlantPS1), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "README.txt"), crlf(plantReadme), 0o644); err != nil {
		return err
	}
	return checkPlantBundle(out)
}

// prefixInstaller builds the same layout installer with the tool at plantPrefixCommit, in a temporary worktree.
func prefixInstaller(dst string) error {
	tmp, err := os.MkdirTemp("", "instbundle-prefix-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	wt := filepath.Join(tmp, "src")
	if o, err := exec.Command("git", "worktree", "add", "--detach", wt, plantPrefixCommit).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %v\n%s", err, o)
	}
	defer exec.Command("git", "worktree", "remove", "--force", wt).Run()
	abs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "run", "./cmd/yakbd", "installer", "-q", "-o", abs, minimal)
	cmd.Dir = wt
	if o, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("installer at %s: %v\n%s", plantPrefixCommit, err, o)
	}
	return nil
}

// markerDLL writes a 32-bit x86 DLL whose entry point, on DLL_PROCESS_ATTACH, creates the file "<its own
// path>.loaded" (GetModuleFileNameW, CreateFileW, CloseHandle from kernel32.dll) and returns TRUE. It exports
// nothing; a program that links to a function of the name it is planted as fails to start, which the checker sees
// as a failed install.
func markerDLL() ([]byte, error) {
	// .idata: import directory (one entry and the null entry), lookup table, address table, hint/name entries,
	// the DLL name.
	funcs := []string{"GetModuleFileNameW", "CreateFileW", "CloseHandle"}
	const ilt, iat = 40, 56
	idata := make([]byte, 72)
	var fix []pe.Fixup
	rva := func(at, target uint32) {
		fix = append(fix, pe.Fixup{Offset: at, Kind: pe.RVA32, Target: pe.Ref{Section: ".idata", Offset: target}})
	}
	for i, f := range funcs {
		off := uint32(len(idata))
		idata = append(idata, 0, 0)
		idata = append(idata, f...)
		idata = append(idata, 0)
		if len(idata)%2 == 1 {
			idata = append(idata, 0)
		}
		rva(ilt+uint32(4*i), off)
		rva(iat+uint32(4*i), off)
	}
	name := uint32(len(idata))
	idata = append(idata, "KERNEL32.dll\x00"...)
	rva(0, ilt)   // OriginalFirstThunk
	rva(12, name) // Name
	rva(16, iat)  // FirstThunk

	code := []byte{
		0x83, 0x7C, 0x24, 0x08, 0x01, // cmp dword [esp+8], 1 (DLL_PROCESS_ATTACH)
		0x75, 0x66, // jne done
		0x53,                               // push ebx
		0x81, 0xEC, 0x10, 0x04, 0x00, 0x00, // sub esp, 0x410 (520 UTF-16 units)
		0x89, 0xE3, // mov ebx, esp
		0x68, 0x00, 0x02, 0x00, 0x00, // push 512
		0x53,                                     // push ebx
		0xFF, 0xB4, 0x24, 0x20, 0x04, 0x00, 0x00, // push dword [esp+0x420] (hinstDLL)
		0xFF, 0x15, 0, 0, 0, 0, // call [GetModuleFileNameW]
		0x8D, 0x14, 0x43, // lea edx, [ebx+eax*2]
		0xC7, 0x02, '.', 0, 'l', 0, // mov dword [edx], ".l"
		0xC7, 0x42, 0x04, 'o', 0, 'a', 0, // mov dword [edx+4], "oa"
		0xC7, 0x42, 0x08, 'd', 0, 'e', 0, // mov dword [edx+8], "de"
		0xC7, 0x42, 0x0C, 'd', 0, 0, 0, // mov dword [edx+12], "d\0"
		0x6A, 0x00, // push 0 (hTemplateFile)
		0x68, 0x80, 0x00, 0x00, 0x00, // push FILE_ATTRIBUTE_NORMAL
		0x6A, 0x02, // push CREATE_ALWAYS
		0x6A, 0x00, // push NULL (security)
		0x6A, 0x00, // push 0 (no sharing)
		0x68, 0x00, 0x00, 0x00, 0x40, // push GENERIC_WRITE
		0x53,                   // push ebx
		0xFF, 0x15, 0, 0, 0, 0, // call [CreateFileW]
		0x83, 0xF8, 0xFF, // cmp eax, INVALID_HANDLE_VALUE
		0x74, 0x07, // je skip
		0x50,                   // push eax
		0xFF, 0x15, 0, 0, 0, 0, // call [CloseHandle]
		0x81, 0xC4, 0x10, 0x04, 0x00, 0x00, // skip: add esp, 0x410
		0x5B,                         // pop ebx
		0xB8, 0x01, 0x00, 0x00, 0x00, // done: mov eax, TRUE
		0xC2, 0x0C, 0x00, // ret 12
	}
	if len(code) != 117 || code[109] != 0xB8 {
		return nil, fmt.Errorf("marker code: %d bytes", len(code))
	}
	var cfix []pe.Fixup
	for i, at := range []uint32{31, 86, 98} {
		cfix = append(cfix, pe.Fixup{Offset: at, Kind: pe.Abs32, Target: pe.Ref{Section: ".idata", Offset: iat + uint32(4*i)}})
	}
	im := &pe.Image{
		Machine: pe.MachineI386, ImageBase: 0x10000000, SectionAlignment: 0x1000, FileAlignment: 0x200,
		Characteristics:    pe.FileExecutableImage | pe.File32BitMachine | pe.FileDLL,
		DllCharacteristics: pe.DllDynamicBase | pe.DllNXCompat | pe.DllNoSEH,
		Subsystem:          2, // Windows GUI
		LinkerVersion:      [2]uint8{14, 0}, OSVersion: [2]uint16{6, 0}, SubsystemVersion: [2]uint16{6, 0},
		Sections: []*pe.Section{
			{Name: ".text", Characteristics: pe.SecCode | pe.SecMemExecute | pe.SecMemRead, Data: code, Fixups: cfix},
			{Name: ".idata", Characteristics: pe.SecInitData | pe.SecMemRead | pe.SecMemWrite, Data: idata, Fixups: fix},
		},
		RelocChars: pe.SecInitData | pe.SecMemRead | pe.SecDiscardable,
		Directories: []pe.Directory{
			{Index: pe.DirImport, Target: pe.Ref{Section: ".idata"}, Size: 40},
			{Index: 12, Target: pe.Ref{Section: ".idata", Offset: iat}, Size: 16}, // import address table
		},
	}
	b, lay, err := im.Write()
	if err != nil {
		return nil, err
	}
	// The writer leaves AddressOfEntryPoint 0 (layout DLLs have none): the entry point is the start of .text.
	opt := binary.LittleEndian.Uint32(b[0x3C:]) + 4 + 20
	var text uint32
	for _, s := range lay.Sections {
		if s.Name == ".text" {
			text = s.RVA
		}
	}
	binary.LittleEndian.PutUint32(b[opt+16:], text)
	binary.LittleEndian.PutUint32(b[opt+64:], pe.Checksum(b, opt+64))
	return b, nil
}

// plantOut writes host-results next to the bundle only when Windows can write there (a bundle on a WSL or
// network path may be read-only): otherwise into %TEMP%\yakbd-tr-planting\host-results, named in the log.
const plantOut = `if (-not $Out) {
  $Out = Join-Path $Bundle 'host-results'
  try { New-Item -ItemType Directory -Force $Out | Out-Null; [IO.File]::WriteAllText((Join-Path $Out '.probe'), 'x'); Remove-Item (Join-Path $Out '.probe') }
  catch { $Out = Join-Path $env:TEMP 'yakbd-tr-planting\host-results' }
}
Write-Output "results: $Out"`

var drivePlantPS1 = strings.Replace(strings.Replace(driveHead, "sign-in driver", "DLL planting driver", 1),
	"if (-not $Out) { $Out = Join-Path $Bundle 'host-results' }", plantOut, 1) + `
try {
  $s = Fresh
  Phase $s plant-prepare
  Task $s plant-control $Admin -Limited
  Task $s plant-dblclick $Admin -Limited
  Task $s plant-dblclick-cleanup $Admin
  # A layout file held at uninstall waits for the restart (3010), and a reinstall before it is refused: restart
  # before each later install of the same layout.
  $s = Restart $s $Admin
  Task $s plant-quiet $Admin
  Task $s plant-toolsetup $Admin
  $s = Restart $s $Admin
  Task $s plant-prefix $Admin
  Collect $s legP w11plant
  Remove-PSSession $s
} catch {
  $Problems.Add("driver stopped: $($_.Exception.Message) at $($_.InvocationInfo.ScriptLineNumber)")
  Log "ERROR $($_.Exception.Message)"
}
try { Stop-VM -Name $VMName -TurnOff -Force; Restore-VMCheckpoint -VMName $VMName -Name $Checkpoint -Confirm:$false } catch { $Problems.Add("restore at the end: $($_.Exception.Message)") }

` + strings.Replace(driveTail, "__LEGS__", "@(,@('legP', 'w11plant'))", 1)

const plantReadme = `yakbd installer test bundle: DLL planting, guest w11x64
===========================================================================

FOR THE RUNNER: start one command on the Hyper-V host (elevated PowerShell), in the background, wait for it, report
host-results\summary.txt. Nothing is interactive and there are no screenshots.

    powershell -ExecutionPolicy Bypass -File <this folder>\drive.ps1 -VMName <the w11x64 guest> -AdminUser <admin> -AdminPassword <adminpw>

<admin> is the guest's administrator (tester), signed in automatically at the console in the checkpoint "base"
(-Checkpoint <name> otherwise). The driver restores "base", copies this folder to C:\yk, runs the phases below
(PowerShell Direct, and scheduled tasks in the console session through C:\yk\run-phase.ps1), collects
C:\yk\results into host-results\legP\ (next to drive.ps1 when Windows can write there, otherwise
%TEMP%\yakbd-tr-planting\host-results; the first output line and driver.log name the folder; -Out <folder> chooses) and restores "base" at the end, with two restarts in between. About 25 minutes.

What it checks (docs/installer.md §5.2): an installer started from a Downloads folder loads no DLL from that folder.
plant-prepare puts, into five folders under %USERPROFILE%\Downloads\yakbd-plant\, one installer and a copy of
plant-marker.dll under every DLL name of System32 and SysWOW64 and every DLL name found in the installers' bytes
(results\plant-names.txt). plant-marker.dll is a 32-bit DLL made by tools/instbundle: when anything loads it, it
writes "<its path>.loaded". plant-prepare also sets ConsentPromptBehaviorAdmin = 0 (an administrator is elevated
without a prompt), so the double-click's elevated copy needs no one at the prompt; the checkpoint is restored after.

Result lines (results\w11plant.txt, copied into summary.txt):
  plant-prepare                             the folders and copies; the count of names; all copies still there after 30 s
  plant-control-marker-dll-loads            C:\yk\instcheck-32.exe loads the control folder's yakbdplantprobe.dll by
                                            full path: its marker must appear (the marker DLL runs); the note gives a
                                            copy started inside the folder loading it and wtsapi32.dll by bare name
  plant-double-click-loads-no-planted-dll   kbdxx-setup.exe opened as Explorer opens it (unelevated, the question
                                            answered Yes, the elevated copy started by the program): exit 0, the
                                            uninstall entry there, no marker
  plant-double-click-cleanup                kbdxx-setup.exe /x /quiet from the same folder (elevated): removed, no marker
  plant-quiet-loads-no-planted-dll          kbdxx-setup.exe /quiet and /x /quiet, elevated: exit 0, no marker
  plant-toolsetup-loads-no-planted-dll      yakbd-setup.exe /quiet and /x /quiet: the same
  plant-prefix-installer-before-the-fix     kbdxx-prefix-setup.exe, the same layout built by the tool before the fix
                                            (commit 2063f05, which loaded user32, ole32 and wtsapi32 by bare name):
                                            always PASS; the note lists the planted DLLs it loaded (expected: some),
                                            which shows that the check sees the defect it guards against

Limit: a planted DLL that a program links to statically (by import table) and that lacks the functions it needs
may fail to load before its entry point runs; it then leaves no marker but makes the program fail, which the
scenarios see as a wrong exit code or a missing install.

Exit code: summary.txt ends with "w11plant: N passed, M failed". BUILT-FROM.txt names the tool commit.
`

// plantFiles are the bundle files the driver and every plant-* phase of tools/instcheck open (plantNeeds in
// tools/instcheck/plant_windows.go names the same per phase); writePlant checks that each one was written.
var plantFiles = []string{"drive.ps1", "run-phase.ps1", "instcheck.exe", "instcheck-32.exe", "plant-marker.dll",
	"kbdxx-setup.exe", "yakbd-setup.exe", "kbdxx-prefix-setup.exe"}

func checkPlantBundle(out string) error {
	for _, f := range plantFiles {
		if st, err := os.Stat(filepath.Join(out, f)); err != nil || st.Size() == 0 {
			return fmt.Errorf("bundle: %s missing or empty", f)
		}
	}
	return nil
}
