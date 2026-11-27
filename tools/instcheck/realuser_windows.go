//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/yakbd/yakbd-cli/installer/setup"
)

// Real-user bundles: the double-click path with UAC, over-the-shoulder elevation, the D69 cases (a second user
// signed in during, and signing in after, a repair that took a new Layout Id; the only-layout repair), a layout file
// in use at uninstall whose deletion waits for the restart, x86 Windows, and one combined result file per guest.
// Phases and the order the runner runs them in: the bundle's README.txt.

// cliMap translates the switches the scenarios are written with into the installer's command line of the tool
// commit the bundle is built from (docs/installer-cli.md). Empty: the switches are the installer's own.
var cliMap = map[string][]string{
	"/enable":   {"ENABLE=1"},
	"/noenable": {"ENABLE=0"},
	"/repair":   {"/f"},
	"/force":    {"FORCE=1"},
	// msiexec restarts by itself after /quiet when a file waits for the restart; the scenarios expect 3010.
	"/quiet": {"/quiet", "/norestart"},
}

func cliArgs(a []string) []string {
	var out []string
	for _, x := range a {
		if r, ok := cliMap[strings.ToLower(x)]; ok {
			out = append(out, r...)
		} else {
			out = append(out, x)
		}
	}
	return out
}

const (
	exitNoBuild = setup.ExitMachine
	takerKey    = layoutsKey + `\` + taker
	otsUser     = "yk2" // the standard user the README creates
	usKLID      = "0409:00000409"
)

var (
	pInstallLayoutOrTip = syscall.NewLazyDLL(sysDir() + `\input.dll`).NewProc("InstallLayoutOrTip")
	pCreateFileW        = kernel32.NewProc("CreateFileW")
	pMoveFileExW        = kernel32.NewProc("MoveFileExW")
	pWow64DisableRedir  = kernel32.NewProc("Wow64DisableWow64FsRedirection")
	pWow64RevertRedir   = kernel32.NewProc("Wow64RevertWow64FsRedirection")
)

func installLayoutOrTip(str string, flags uintptr) bool {
	r, _, _ := pInstallLayoutOrTip.Call(uintptr(unsafe.Pointer(u16(str))), flags)
	return r != 0
}

// noDesktop are the phases that run without the interactive desktop (PowerShell Direct, session 0).
var noDesktop = map[string]bool{"si-baseline": true, "si2-baseline": true, "q14-system": true, "vm27-system": true, "d69-repair": true, "uac-settings": true, "settings-prepare": true, "settings-prepare2": true, "settings-manual-check": true, "plant-prepare": true}

// guestPhases are the result files the summary of each guest collects, in the README's order. vm-64 and vm-32 are
// the typing runs of yakbd-wincheck (tools/vmbundle).
var guestPhases = map[string][]string{
	"w11x64": {"uac-settings", "dbl-admin", "dbl-detect", "ots-install", "d69-repair", "d69-signed-in", "d69-after-sign-in",
		"after-ots", "only-prepare", "only-check", "deferred-prepare", "deferred-check"},
	"w10x64": {"instcheck", "reenable-prepare", "deferred-prepare", "deferred-check", "reenable-check", "vm-64", "vm-32"},
	"w10x86": {"instcheck", "reenable-prepare", "deferred-prepare", "deferred-check", "reenable-check", "vm-32"},
	// The Settings removal alone (the third w11 run).
	"w11settings": {"settings-prepare", "settings-manual-tester", "settings-prepare2", "settings-manual-yk2"},
	// The sign-in screen and new users (signin_windows.go).
	"w11signin": signInPhases,
	// Second sign-in bundle (D97): the missing-language legs (signin2_windows.go).
	"w11signin2": signIn2Phases,
	"w11q14":     q14Phases,
	"w11signin3": vm27Phases,
	// DLL planting (plant_windows.go).
	"w11plant": plantPhases,
	// Clean-machine check of a release (clean_windows.go).
	"w11clean": cleanPhases,
}

// realUser runs a real-user phase; false when the phase is not one of them.
func (c *checker) realUser(phase, res string) bool {
	if !contains(guestPhases["w11x64"], phase) && !contains(guestPhases["w11settings"], phase) && phase != "settings-manual-check" {
		return false
	}
	a, err := c.load("demo-setup.exe", "demo.expect.txt")
	if err != nil {
		c.begin(phase).failf("%v", err)
		return true
	}
	mini, err := c.load("minimal-setup.exe", "minimal.expect.txt")
	if err != nil {
		c.begin(phase).failf("%v", err)
		return true
	}
	watched = append(watched, a.p.Manifest.Layout.File, mini.p.Manifest.Layout.File)
	switch phase {
	case "uac-settings":
		c.uacSettings()
	case "dbl-admin":
		c.dblAdmin(a)
	case "dbl-detect":
		c.dblDetect(a)
	case "ots-install":
		c.otsInstall(a)
	case "d69-repair":
		c.d69Repair(a)
	case "d69-signed-in":
		c.d69SignedIn(a)
	case "d69-after-sign-in":
		c.d69AfterSignIn(a)
	case "after-ots":
		c.afterOTS(a)
	case "only-prepare":
		c.onlyPrepare(a)
	case "only-check":
		c.onlyCheck(a)
	case "deferred-prepare":
		c.deferredPrepare(mini)
	case "deferred-check":
		c.deferredCheck(mini)
	case "settings-prepare", "settings-prepare2":
		c.settingsPrepare(a, phase)
	case "settings-manual-check":
		c.settingsManualCheck(a)
	default:
		return false
	}
	return true
}

// State between phases: key value lines in results\realuser-state.txt.
func (c *checker) state() map[string]string {
	m := map[string]string{}
	b, _ := os.ReadFile(filepath.Join(c.res, "realuser-state.txt"))
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
			m[k] = v
		}
	}
	return m
}

func (c *checker) setState(kv ...string) {
	m := c.state()
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	var b strings.Builder
	for k, v := range m {
		fmt.Fprintf(&b, "%s %s\n", k, v)
	}
	_ = os.WriteFile(filepath.Join(c.res, "realuser-state.txt"), []byte(b.String()), 0o644)
}

// machineSnapshot is the part of snapshot that does not depend on which user runs the check.
func machineSnapshot() string {
	var keep []string
	for _, l := range strings.Split(snapshot(), "\n") {
		if !strings.HasPrefix(l, "hkcu ") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// uacSettings records the elevation policy the guest has (settings-and-configuration page values) and this token.
func (c *checker) uacSettings() {
	s := c.begin("uac-settings")
	const pol = `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`
	var parts []string
	for _, n := range []string{"EnableLUA", "ConsentPromptBehaviorAdmin", "ConsentPromptBehaviorUser", "PromptOnSecureDesktop", "EnableInstallerDetection"} {
		v := regString(hkeyLocalMachine, pol, n)
		if v == "" {
			v = "absent"
		}
		parts = append(parts, n+"="+v)
	}
	s.notef("%s", strings.Join(parts, " "))
	if regString(hkeyLocalMachine, pol, "EnableLUA") == "0" {
		s.failf("UAC is off: the double-click scenarios need it on")
	}
	s.end()
}

// copyLogs copies the installer logs of the user's temporary folder (the default log of a run started without
// /log) into results\logs\<tag>-*.log.
func (c *checker) copyLogs(tag string) {
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), "yakbd-*-setup.log"))
	for _, p := range m {
		if b, err := os.ReadFile(p); err == nil {
			_ = os.WriteFile(filepath.Join(c.logs, tag+"-"+filepath.Base(p)), b, 0o644)
		}
	}
}

func clearLogs() {
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), "yakbd-*-setup.log"))
	for _, p := range m {
		_ = os.Remove(p)
	}
}

// uacMarker tells the runner that an elevation prompt is up and how to answer it.
func (c *checker) uacMarker(text string) string {
	p := filepath.Join(c.res, "uac-waiting.txt")
	_ = os.WriteFile(p, []byte(text+"\n"), 0o644)
	return p
}

// promptKind says which elevation prompt this user gets.
func promptKind() string {
	if elevationType() == elevationLimited {
		return "CONSENT: answer Yes (keyboard: Alt+Y)"
	}
	return "CREDENTIALS: type the password of the administrator account (tester) and press Enter"
}

// driveSetup drives one run of the installer's first process: the question (answered with answer: idYes or
// idNo), the elevation prompt (answered by the runner from the host while uac-waiting.txt exists), the final
// message (pressed OK). It returns the exit code and the two dialog texts.
func (c *checker) driveSetup(s *scenario, proc syscall.Handle, pid uint32, want string, answer int) (int, string, string) {
	var question, final string
	var dlg uintptr
	if !waitFor(90*time.Second, func() bool { dlg = dialogOf(pid); return dlg != 0 || exited(proc) }) || dlg == 0 {
		s.failf("no question dialog (process exited: %v; elevation prompt up: %v)", exited(proc), len(processes("consent.exe")) > 0)
		return exitCode(proc, 0), "", ""
	}
	question = dialogText(dlg)
	fmt.Fprintf(c.detail, "  dialog: %s\n", question)
	if !strings.Contains(question, want) {
		s.failf("question %q does not contain %q", question, want)
	}
	if !toForeground(dlg) {
		s.notef("the question could not be brought to the foreground")
	}
	if !press(dlg, answer) {
		s.failf("the question has no button %d", answer)
	}
	if answer == idYes {
		var consent bool
		waitFor(60*time.Second, func() bool {
			consent = len(processes("consent.exe")) > 0
			return consent || exited(proc) || dialogOf(pid) != 0 && dialogOf(pid) != dlg
		})
		if consent {
			start := time.Now()
			marker := c.uacMarker("WAITING " + promptKind())
			fmt.Fprintf(c.detail, "  elevation prompt up; waiting for the runner\n")
			if !waitFor(20*time.Minute, func() bool { return len(processes("consent.exe")) == 0 }) {
				s.failf("the elevation prompt was not answered within 20 minutes")
			}
			_ = os.Remove(marker)
			s.notef("elevation prompt answered after %s", time.Since(start).Round(time.Second))
		} else {
			s.notef("no elevation prompt appeared")
		}
	}
	// Every later dialog until the program ends: the end message (OK), and, when a file waits for the restart, the
	// question "Windows must restart to finish. Restart now?" (docs/installer-cli.md §5), answered No so that the
	// guest does not restart in the middle of a phase (the program then ends with 3010).
	seen := dlg
	for !exited(proc) {
		dlg2 := uintptr(0)
		if !waitFor(4*time.Minute, func() bool { dlg2 = dialogOf(pid); return dlg2 != 0 && dlg2 != seen || exited(proc) }) || dlg2 == 0 || exited(proc) {
			break
		}
		seen = dlg2
		t := dialogText(dlg2)
		fmt.Fprintf(c.detail, "  dialog: %s\n", t)
		if strings.Contains(t, "Restart now?") {
			s.notef("restart question answered No")
			press(dlg2, idNo)
			continue
		}
		final = t
		press(dlg2, idOK, idCancel)
	}
	code := exitCode(proc, 2*time.Minute)
	if code == -1 {
		s.failf("the installer did not end")
	}
	return code, question, final
}

// doubleClick starts an installer as Explorer does and drives it.
func (c *checker) doubleClick(s *scenario, exe, want string, answer int) (int, string) {
	closeStart()
	proc, pid, err := shellOpen(exe)
	if err != nil {
		s.failf("%v", err)
		return -1, ""
	}
	defer syscall.CloseHandle(proc)
	code, _, final := c.driveSetup(s, proc, pid, want, answer)
	fmt.Fprintf(c.detail, "  %s (double-click): exit %d\n", filepath.Base(exe), code)
	return code, final
}

// requireUnelevated fails the scenario when this process is elevated: a double-click comes from the user's own,
// unelevated Explorer.
func requireUnelevated(s *scenario) bool {
	if elevated() {
		s.failf("the checker runs elevated: run this phase as a task with the limited run level")
		return false
	}
	return true
}

// dblAdmin: an administrator double-clicks the installer (consent prompt), then removes it in Settings.
func (c *checker) dblAdmin(a *layout) {
	s := c.begin("dbl-admin-install")
	if !requireUnelevated(s) {
		s.end()
		return
	}
	if elevationType() != elevationLimited {
		s.failf("this account is not an administrator in Admin Approval Mode (token type %d)", elevationType())
	}
	base := machineSnapshot()
	_ = os.WriteFile(filepath.Join(c.res, "machine-base.txt"), []byte(base), 0o644)
	clearLogs()
	code, final := c.doubleClick(s, a.exe, "Install the keyboard layout", idYes)
	c.copyLogs("dbl-admin-install")
	if code != setup.ExitOK {
		s.failf("exit code %d, want %d", code, setup.ExitOK)
	}
	if !strings.Contains(final, "is installed") {
		s.failf("final message %q", final)
	}
	klid, id := c.checkInstalled(s, a, true)
	if klid != "" {
		c.typeBoth(s, a, klid, uint32(hex(a.p.Manifest.Layout.LangID))|uint32(0xF000|id)<<16)
	}
	s.end()

	s = c.begin("dbl-admin-uninstall-entry")
	c.entryUninstall(s, a, "dbl-admin-uninstall")
	if userHas(klid) {
		s.failf("the layout is still in the user's list")
	}
	c.checkGone(s, a, base)
	s.end()
}

// typeBoth types the layout in this process and in the 32-bit helper.
func (c *checker) typeBoth(s *scenario, a *layout, klid string, hkl uint32) {
	if msg, ok := typeLayout(klid, a.expect, hkl); !ok {
		s.failf("typing in this process: %s", msg)
	} else {
		s.notef("this process %s", msg)
	}
	if out, err := exec.Command(filepath.Join(c.bundle, "instcheck-32.exe"), "-type", klid, a.expect).CombinedOutput(); err != nil {
		s.failf("32-bit typing: %s (%v)", strings.TrimSpace(string(out)), err)
	} else {
		s.notef("32-bit %s", strings.TrimSpace(string(out)))
	}
}

// checkGone checks that the product is gone from the machine: the state equals base apart from files that wait
// for the restart (exit 3010).
func (c *checker) checkGone(s *scenario, a *layout, base string) {
	m := a.p.Manifest
	if regString(hkeyLocalMachine, uninstallKey+`\`+m.Product.Code, "DisplayName") != "" {
		s.failf("the uninstall entry is still there")
	}
	pending := strings.ToLower(strings.Join(pendingRenames(), "|"))
	after := withoutPending(machineSnapshot(), pending)
	if after != base {
		s.failf("left behind: %s", firstDiff(base, after))
	}
	c.checkPendingPaths(s, m.Layout.File)
}

// entryUninstall starts the uninstall entry's command (UninstallString) as this user, through the shell, and drives
// the installer's dialogs. Settings > Installed apps runs the same command, but elevated first (VM run of the
// w11settings bundle, by hand): that path is verified by hand (docs/installer.md 8.2); UI Automation of Settings
// failed in three runs and was dropped.
func (c *checker) entryUninstall(s *scenario, a *layout, tag string) int {
	clearLogs()
	cmd := regString(hkeyLocalMachine, uninstallKey+`\`+a.p.Manifest.Product.Code, "UninstallString")
	exe, args := splitCommand(cmd)
	s.notef("the uninstall entry's command %q", cmd)
	proc, pid, err := shellOpenArgs(exe, args)
	if err != nil {
		s.failf("%v", err)
		return -1
	}
	defer syscall.CloseHandle(proc)
	code, _, final := c.driveSetup(s, proc, pid, "Remove the keyboard layout", idYes)
	c.copyLogs(tag)
	fmt.Fprintf(c.detail, "  uninstall: exit %d, %q\n", code, final)
	if code != setup.ExitOK && code != setup.ExitReboot {
		s.failf("exit code %d", code)
	}
	if code == setup.ExitReboot {
		s.notef("exit 3010: a file waits for the restart")
	}
	return code
}

// splitCommand splits `"C:\path\setup.exe" /a /b` into the program and its arguments.
func splitCommand(cmd string) (string, string) {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if i := strings.Index(cmd[1:], `"`); i >= 0 {
			return cmd[1 : i+1], strings.TrimSpace(cmd[i+2:])
		}
	}
	exe, args, _ := strings.Cut(cmd, " ")
	return exe, args
}

func shellOpenArgs(file, args string) (syscall.Handle, uint32, error) {
	info := shellExecuteInfo{fMask: 0x40 | 0x100, lpFile: u16(file), lpParameters: u16(args), nShow: 1}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, err := pShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, 0, fmt.Errorf("ShellExecuteExW(%s %s): %v", file, args, err)
	}
	pid, _, _ := pGetProcessId.Call(uintptr(info.hProcess))
	return info.hProcess, uint32(pid), nil
}

// dblDetect: with UAC's installer detection switched on (the runner sets EnableInstallerDetection = 1 before this
// phase and back afterwards), a double-click must still start the program unelevated: its question comes before any
// elevation prompt. The answer No ends it with the cancel code and nothing changed.
func (c *checker) dblDetect(a *layout) {
	s := c.begin("installer-detection-asinvoker")
	if !requireUnelevated(s) {
		s.end()
		return
	}
	det := regString(hkeyLocalMachine, `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, "EnableInstallerDetection")
	if det != "1" {
		s.failf("EnableInstallerDetection is %q, the runner sets 1 for this phase", det)
	}
	base := machineSnapshot()
	proc, pid, err := shellOpen(a.exe)
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	defer syscall.CloseHandle(proc)
	var dlg uintptr
	consent := false
	waitFor(90*time.Second, func() bool {
		dlg = dialogOf(pid)
		consent = len(processes("consent.exe")) > 0
		return dlg != 0 || consent || exited(proc)
	})
	switch {
	case consent:
		c.uacMarker("WAITING DETECT: answer No (keyboard: Alt+N)")
		s.failf("an elevation prompt came before the program's question: installer detection elevated the program at start")
		waitFor(20*time.Minute, func() bool { return len(processes("consent.exe")) == 0 })
		_ = os.Remove(filepath.Join(c.res, "uac-waiting.txt"))
	case dlg == 0:
		s.failf("no question dialog")
	default:
		s.notef("the question came first, from the unelevated program: %s", dialogText(dlg))
		press(dlg, idNo)
	}
	if code := exitCode(proc, time.Minute); code != setup.ExitCancelled {
		s.failf("exit code %d, want %d", code, setup.ExitCancelled)
	}
	if after := machineSnapshot(); after != base {
		s.failf("the state changed: %s", firstDiff(base, after))
	}
	s.end()
}

// otsInstall: a standard user double-clicks the installer and types an administrator's credentials into the
// prompt; the layout must land in this user's list (inst §4.3 over the shoulder).
func (c *checker) otsInstall(a *layout) {
	s := c.begin("ots-install")
	if !requireUnelevated(s) {
		s.end()
		return
	}
	name, sid := currentUser()
	if elevationType() != elevationDefault {
		s.failf("%s is not a standard user (token type %d)", name, elevationType())
	}
	clearLogs()
	code, final := c.doubleClick(s, a.exe, "Install the keyboard layout", idYes)
	c.copyLogs("ots-install")
	if code != setup.ExitOK {
		s.failf("exit code %d, want %d (final message %q)", code, setup.ExitOK, final)
	}
	klid, id := c.checkInstalled(s, a, true)
	if klid != "" {
		hkl := uint32(hex(a.p.Manifest.Layout.LangID)) | uint32(0xF000|id)<<16
		c.typeBoth(s, a, klid, hkl)
		c.setState("klid", klid, "id", fmt.Sprintf("%04x", id), "otsSID", sid)
	}
	s.notef("user %s", name)
	s.end()
}

// d69Repair: run by the runner through PowerShell Direct as the administrator (session 0) while the standard user
// is signed in at the console with the layout: the Layout Id is given to another KLID and the KLID key deleted (M7),
// then the installed setup.exe repairs. Also: the administrator's own hive never got the layout (the install was
// over the shoulder).
func (c *checker) d69Repair(a *layout) {
	st := c.state()
	klid := st["klid"]
	s := c.begin("ots-admin-list-untouched")
	if klid == "" {
		s.failf("no state from ots-install")
		s.end()
		return
	}
	name, own := currentUser()
	switch {
	case regValues(hkeyUsers, own+`\Keyboard Layout`) == nil:
		s.notef("not checked: the administrator's hive HKU\\%s is not loaded in this session", own)
	case hiveHas(own, klid):
		s.failf("the administrator's hive (%s) names %s: the over-the-shoulder install enabled the wrong user", name, klid)
	}
	otsSID := st["otsSID"]
	if vals := regValues(hkeyUsers, otsSID+`\Keyboard Layout\Substitutes`); vals == nil {
		s.failf("the standard user's hive %s is not loaded: is %s signed in at the console?", otsSID, otsUser)
	}
	s.notef("administrator %s", name)
	s.end()

	s = c.begin("d69-repair-while-second-user-signed-in")
	id := uint16(hex(st["id"]))
	n := c.takeAndRepair(s, a, klid, id, "d69", "/noenable") // tester never had it; session 0 must not enable
	if hiveHas(otsSID, klid) {
		s.notef("the standard user's entries still name %s", klid)
	} else {
		s.failf("the standard user's hive lost %s", klid)
	}
	newHKL := uint32(hex(a.p.Manifest.Layout.LangID)) | uint32(0xF000|n)<<16
	oldHKL := uint32(hex(a.p.Manifest.Layout.LangID)) | uint32(0xF000|id)<<16
	c.setState("newHKL", fmt.Sprintf("%08x", newHKL), "oldHKL", fmt.Sprintf("%08x", oldHKL))
	s.end()
}

// takeAndRepair gives the Layout Id to b0990409, deletes the KLID key and runs the installed setup.exe /repair
// /quiet; it checks the new Layout Id and returns it.
func (c *checker) takeAndRepair(s *scenario, a *layout, klid string, id uint16, tag string, extra ...string) uint16 {
	for _, v := range [][2]string{{"Layout File", "kbdus.dll"}, {"Layout Text", "yakbd TI4 Layout Id taker"}, {"Layout Id", fmt.Sprintf("%04x", id)}} {
		if out, err := exec.Command("reg.exe", "add", `HKLM\`+takerKey, "/v", v[0], "/t", "REG_SZ", "/d", v[1], "/f").CombinedOutput(); err != nil {
			s.failf("cannot write %s\\%s: %v %s", taker, v[0], err, strings.TrimSpace(string(out)))
		}
	}
	if r, _, _ := pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(layoutsKey+`\`+klid)))); r != 0 {
		s.failf("cannot delete the KLID key: %v", syscall.Errno(r))
	}
	installed := c.installedExe(a)
	if installed == "" {
		s.failf("no installed setup.exe")
		return 0
	}
	if code := c.runInstaller(s, installed, tag+"-repair", append([]string{"/repair", "/quiet"}, extra...)...); code != setup.ExitOK && code != setup.ExitReboot {
		s.failf("repair: exit code %d", code)
	}
	n := c.checkRegistry(s, a, klid, 0)
	rec := regString(hkeyLocalMachine, uninstallKey+`\`+a.p.Manifest.Product.Code, "YakbdLayoutId")
	if n == id {
		s.failf("the Layout Id is still %04x", id)
	} else if rec != fmt.Sprintf("%04x", n) {
		s.failf("the record says Layout Id %q, the KLID %04x", rec, n)
	}
	s.notef("Layout Id %04x -> %04x", id, n)
	return n
}

// hiveHas reports whether a loaded user hive (HKU\<sid>) names the KLID in Preload, Substitutes or User Profile.
func hiveHas(sid, klid string) bool {
	for _, sub := range []string{`\Keyboard Layout\Substitutes`, `\Keyboard Layout\Preload`} {
		for _, v := range regValues(hkeyUsers, sid+sub) {
			if strings.EqualFold(v.str, klid) {
				return true
			}
		}
	}
	up := sid + `\Control Panel\International\User Profile`
	for _, t := range subKeys(hkeyUsers, up) {
		for _, v := range regValues(hkeyUsers, up+`\`+t) {
			if strings.HasSuffix(strings.ToLower(v.name), ":"+strings.ToLower(klid)) {
				return true
			}
		}
	}
	return false
}

// d69SignedIn: the standard user's session that was signed in during the repair. Recorded, not judged: D69 tells
// such users to sign out and in again.
func (c *checker) d69SignedIn(a *layout) {
	st := c.state()
	s := c.begin("d69-second-user-session-after-repair")
	klid := st["klid"]
	if !userHas(klid) {
		s.failf("%s is not in this user's list", klid)
	}
	newHKL, oldHKL := uint32(hex(st["newHKL"])), uint32(hex(st["oldHKL"]))
	s.notef("new HKL %08x in a new process: %v; old HKL %08x in a new process: %v", newHKL, c.hklInNewProcess(newHKL), oldHKL, c.hklInNewProcess(oldHKL))
	s.end()
}

// d69AfterSignIn: after a restart and the automatic sign-in of the standard user, the layout must work with the
// new HKL; then the user removes it in Settings (over the shoulder again).
func (c *checker) d69AfterSignIn(a *layout) {
	st := c.state()
	klid := st["klid"]
	newHKL := uint32(hex(st["newHKL"]))
	s := c.begin("d69-second-user-after-sign-in")
	if klid == "" {
		s.failf("no state from the earlier phases")
		s.end()
		return
	}
	if !userHas(klid) {
		s.failf("%s is not in this user's list", klid)
	}
	if !hklLoaded(newHKL) {
		s.failf("HKL %08x is not in this process's GetKeyboardLayoutList", newHKL)
	}
	if !c.hklInNewProcess(newHKL) {
		s.failf("HKL %08x is not in a new process's GetKeyboardLayoutList", newHKL)
	}
	c.typeBoth(s, a, klid, newHKL)
	s.end()

	s = c.begin("ots-uninstall-entry")
	if !requireUnelevated(s) {
		s.end()
		return
	}
	code := c.entryUninstall(s, a, "ots-uninstall")
	if userHas(klid) {
		s.failf("the layout is still in this user's list")
	}
	if regString(hkeyLocalMachine, uninstallKey+`\`+a.p.Manifest.Product.Code, "DisplayName") != "" {
		s.failf("the uninstall entry is still there")
	}
	if regValues(hkeyLocalMachine, layoutsKey+`\`+klid) != nil {
		s.failf("the KLID key is still there")
	}
	c.checkPendingPaths(s, a.p.Manifest.Layout.File)
	c.setState("otsUninstallCode", fmt.Sprint(code))
	s.end()
}

// afterOTS: back as the administrator after a restart: the taker KLID removed, the machine as before dbl-admin.
func (c *checker) afterOTS(a *layout) {
	s := c.begin("ots-nothing-left-after-restart")
	if r, _, _ := pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(takerKey)))); r != 0 && r != 2 {
		s.failf("cannot delete %s: %v", taker, syscall.Errno(r))
	}
	base, _ := os.ReadFile(filepath.Join(c.res, "machine-base.txt"))
	if after := machineSnapshot(); after != string(base) {
		s.failf("left behind: %s", firstDiff(string(base), after))
	}
	for _, p := range pendingRenames() {
		if strings.Contains(strings.ToLower(p), strings.ToLower(a.p.Manifest.Layout.File)) {
			s.failf("still pending: %s", p)
		}
	}
	if len(s.probs) > 0 {
		cleanProduct(s, a) // so that the later phases do not fail for this phase's reason
	}
	s.end()
}

// cleanProduct removes what a failed phase left of the product: its KLIDs, uninstall entry, files and Program
// Files folder (by hand; noted).
func cleanProduct(s *scenario, a *layout) {
	m := a.p.Manifest
	for _, k := range subKeys(hkeyLocalMachine, layoutsKey) {
		if strings.EqualFold(regString(hkeyLocalMachine, layoutsKey+`\`+k, "Layout Product Code"), m.Product.Code) {
			pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(layoutsKey+`\`+k))))
		}
	}
	loc := regString(hkeyLocalMachine, uninstallKey+`\`+m.Product.Code, "InstallLocation")
	pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(uninstallKey+`\`+m.Product.Code))))
	for _, d := range []string{sysDir(), wowDir()} {
		if d != "" {
			_ = os.Remove(filepath.Join(d, m.Layout.File))
		}
	}
	if loc != "" {
		_ = os.RemoveAll(loc)
	}
	s.notef("cleaned up by hand for the next phases")
}

// usOnly is the user's Preload resolved through Substitutes.
func preloadKLIDs() []string {
	sub := map[string]string{}
	for _, v := range regValues(hkeyCurrentUser, `Keyboard Layout\Substitutes`) {
		sub[strings.ToLower(v.name)] = strings.ToLower(v.str)
	}
	var out []string
	for _, v := range regValues(hkeyCurrentUser, `Keyboard Layout\Preload`) {
		k := strings.ToLower(v.str)
		if s, ok := sub[k]; ok {
			k = s
		}
		out = append(out, k)
	}
	return out
}

// onlyPrepare: the layout as the administrator's only layout, then a repair that takes a new Layout Id (D69: added
// again without removing it first; sign out and in).
func (c *checker) onlyPrepare(a *layout) {
	s := c.begin("only-layout-repair")
	base := snapshot()
	_ = os.WriteFile(filepath.Join(c.res, "only-base.txt"), []byte(base), 0o644)
	if code := c.runInstaller(s, a.exe, "only-install", "/quiet", "/enable"); code != setup.ExitOK {
		s.failf("install: exit code %d", code)
	}
	klid, id := c.checkInstalled(s, a, true)
	if klid == "" {
		s.end()
		return
	}
	if !installLayoutOrTip(usKLID, 1) {
		s.failf("InstallLayoutOrTip(%s, ILOT_UNINSTALL) failed", usKLID)
	}
	if pl := preloadKLIDs(); len(pl) != 1 || pl[0] != klid {
		s.failf("Preload after removing US: %v, want only %s", pl, klid)
	}
	n := c.takeAndRepair(s, a, klid, id, "only")
	log, _ := os.ReadFile(filepath.Join(c.logs, "only-repair.log"))
	if !strings.Contains(string(log), "only layout: not removed first") {
		s.failf("the repair log does not say the only layout was added without removing it")
	}
	if !userHas(klid) {
		s.failf("the layout is not in the list after the repair")
	}
	newHKL := uint32(hex(a.p.Manifest.Layout.LangID)) | uint32(0xF000|n)<<16
	s.notef("new HKL %08x in a new process before the sign-out: %v", newHKL, c.hklInNewProcess(newHKL))
	c.setState("onlyKLID", klid, "onlyHKL", fmt.Sprintf("%08x", newHKL))
	s.end()
}

// onlyCheck: after the restart and sign-in the only layout works with its new HKL; then US is added back, the taker
// deleted, the layout uninstalled and nothing is left.
func (c *checker) onlyCheck(a *layout) {
	st := c.state()
	klid, hkl := st["onlyKLID"], uint32(hex(st["onlyHKL"]))
	s := c.begin("only-layout-after-sign-in")
	if klid == "" {
		s.failf("no state from only-prepare")
		s.end()
		return
	}
	if !hklLoaded(hkl) {
		s.failf("HKL %08x is not in this process's GetKeyboardLayoutList", hkl)
	}
	if !c.hklInNewProcess(hkl) {
		s.failf("HKL %08x is not in a new process's GetKeyboardLayoutList", hkl)
	}
	c.typeBoth(s, a, klid, hkl)
	s.end()

	s = c.begin("only-layout-cleanup")
	if !installLayoutOrTip(usKLID, 0) {
		s.failf("InstallLayoutOrTip(%s, 0) failed", usKLID)
	}
	if r, _, _ := pRegDeleteKeyW.Call(uintptr(hkeyLocalMachine), uintptr(unsafe.Pointer(u16(takerKey)))); r != 0 {
		s.failf("cannot delete %s: %v", taker, syscall.Errno(r))
	}
	// The user's own entries are compared by meaning (Windows numbers Preload itself when US comes back), the
	// machine by the snapshot.
	base, _ := os.ReadFile(filepath.Join(c.res, "only-base.txt"))
	if code := c.runInstaller(s, c.installedExe(a), "only-uninstall", "/uninstall", "/quiet"); code != setup.ExitOK && code != setup.ExitReboot {
		s.failf("uninstall: exit code %d", code)
	}
	if userHas(klid) {
		s.failf("%s is still in the user's list", klid)
	}
	if pl := preloadKLIDs(); !contains(pl, "00000409") {
		s.failf("US is not in Preload after the cleanup: %v", pl)
	}
	var machine []string
	for _, l := range strings.Split(string(base), "\n") {
		if !strings.HasPrefix(l, "hkcu ") {
			machine = append(machine, l)
		}
	}
	c.checkGone(s, a, strings.Join(machine, "\n"))
	s.end()
}

// Deferred deletion: both layout files held open (no FILE_SHARE_DELETE) by a helper while the uninstaller runs.

const (
	genericRead   = 0x80000000
	fileShareRead = 1
	openExisting  = 3
)

// hold opens the files without delete sharing and keeps them open until stop exists (at most 15 minutes).
func hold(paths []string, ready, stop string) int {
	for _, p := range paths {
		h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(p))), genericRead, fileShareRead, 0, openExisting, 0, 0)
		if syscall.Handle(h) == syscall.InvalidHandle {
			fmt.Printf("cannot open %s: %v\n", p, err)
			return 1
		}
	}
	_ = os.WriteFile(ready, []byte("held\n"), 0o644)
	waitFor(15*time.Minute, func() bool { _, err := os.Stat(stop); return err == nil })
	return 0
}

// Probe files in the native System32 that the 32-bit helper marks for deletion at the restart in two ways, to see
// which stored path the restart honours: through Sysnative (as the installer does), and through System32 with the
// thread's file-system redirection switched off.
var probes = []string{"yakbd-probe-sysnative.tmp", "yakbd-probe-redir-off.tmp", "yakbd-probe-syswow64.tmp"}

// probeDelay runs in the 32-bit helper (WOW64): it marks the three probe files.
func probeDelay() int {
	win := os.Getenv("SystemRoot")
	mark := func(p string) {
		r, _, err := pMoveFileExW.Call(uintptr(unsafe.Pointer(u16(p))), 0, 4)
		fmt.Printf("MoveFileExW(%s, NULL, DELAY_UNTIL_REBOOT): %v (%v)\n", p, r != 0, err)
	}
	mark(win + `\Sysnative\` + probes[0])
	var old uintptr
	if r, _, err := pWow64DisableRedir.Call(uintptr(unsafe.Pointer(&old))); r == 0 {
		fmt.Printf("Wow64DisableWow64FsRedirection: %v\n", err)
	} else {
		mark(win + `\System32\` + probes[1])
		pWow64RevertRedir.Call(old)
	}
	mark(wowDir() + `\` + probes[2])
	return 0
}

func (c *checker) deferredPrepare(a *layout) {
	s := c.begin("deferred-delete-uninstall")
	base := snapshot()
	_ = os.WriteFile(filepath.Join(c.res, "deferred-base.txt"), []byte(base), 0o644)
	if code := c.runInstaller(s, a.exe, "deferred-install", "/quiet", "/noenable"); code != setup.ExitOK {
		s.failf("install: exit code %d", code)
	}
	klid, _ := c.checkInstalled(s, a, false)
	files := []string{filepath.Join(sysDir(), a.p.Manifest.Layout.File)}
	if w := wowDir(); w != "" {
		files = append(files, filepath.Join(w, a.p.Manifest.Layout.File))
	}
	ready, stop := filepath.Join(c.res, "hold-ready.txt"), filepath.Join(c.res, "hold-stop.txt")
	_ = os.Remove(ready)
	_ = os.Remove(stop)
	self, _ := os.Executable()
	holder := exec.Command(self, "-hold", strings.Join(files, ";"), "-ready", ready, "-stop", stop)
	if err := holder.Start(); err != nil {
		s.failf("holder: %v", err)
	}
	if !waitFor(30*time.Second, func() bool { _, err := os.Stat(ready); return err == nil }) {
		s.failf("the holder did not open the files")
	}
	// Probes (64-bit Windows only).
	if wowDir() != "" {
		for i, p := range probes {
			dir := sysDir()
			if i == 2 {
				dir = wowDir()
			}
			_ = os.WriteFile(filepath.Join(dir, p), []byte("probe\n"), 0o644)
		}
		out, err := exec.Command(filepath.Join(c.bundle, "instcheck-32.exe"), "-probe-delay").CombinedOutput()
		fmt.Fprintf(c.detail, "  probe: %s (%v)\n", strings.TrimSpace(string(out)), err)
	}
	exe := c.installedExe(a)
	code := c.runInstaller(s, exe, "deferred-uninstall", "/uninstall", "/quiet")
	_ = os.WriteFile(stop, []byte("stop\n"), 0o644)
	holder.Wait()
	if code != setup.ExitReboot {
		s.failf("exit code %d, want 3010 (files in use)", code)
	}
	var stored []string
	for _, p := range pendingRenames() {
		lp := strings.ToLower(p)
		if strings.Contains(lp, strings.ToLower(a.p.Manifest.Layout.File)) || strings.Contains(lp, "yakbd-probe") {
			stored = append(stored, p)
		}
	}
	s.notef("stored: %s", strings.Join(stored, " | "))
	c.checkPendingPaths(s, a.p.Manifest.Layout.File) // a path through Sysnative is a failure: boot does not know it
	for _, f := range files {
		if !strings.Contains(strings.ToLower(strings.Join(stored, "|")), strings.ToLower(filepath.Base(f))) {
			s.failf("no pending operation names %s", f)
		}
	}
	if regValues(hkeyLocalMachine, layoutsKey+`\`+klid) != nil || regString(hkeyLocalMachine, uninstallKey+`\`+a.p.Manifest.Product.Code, "DisplayName") != "" {
		s.failf("the KLID key or the uninstall entry is still there")
	}
	c.setState("deferredFiles", strings.Join(files, ";"))
	s.end()
}

func (c *checker) deferredCheck(a *layout) {
	st := c.state()
	s := c.begin("deferred-delete-after-restart")
	files := strings.Split(st["deferredFiles"], ";")
	if st["deferredFiles"] == "" {
		s.failf("no state from deferred-prepare")
	}
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			s.failf("%s is still there after the restart", f)
		}
	}
	for _, p := range pendingRenames() {
		if strings.Contains(strings.ToLower(p), strings.ToLower(a.p.Manifest.Layout.File)) {
			s.failf("still pending: %s", p)
		}
	}
	base, _ := os.ReadFile(filepath.Join(c.res, "deferred-base.txt"))
	if after := snapshot(); after != string(base) {
		s.failf("left behind: %s", firstDiff(string(base), after))
	}
	if code := c.runInstaller(s, a.exe, "deferred-reinstall", "/quiet", "/noenable"); code != setup.ExitOK {
		s.failf("install after the restart: exit code %d", code)
	}
	c.uninstall(s, a, "deferred-reuninstall", string(base))
	s.end()

	if wowDir() != "" {
		s = c.begin("probe-delay-paths")
		for i, p := range probes {
			dir := sysDir()
			if i == 2 {
				dir = wowDir()
			}
			_, err := os.Stat(filepath.Join(dir, p))
			s.notef("%s deleted at the restart: %v", p, err != nil)
			_ = os.Remove(filepath.Join(dir, p))
		}
		s.end()
	}
}

// summary writes results\<guest>.txt: every phase's lines in the README's order and one count.
func summary(res, guest string, phases []string) int {
	var b strings.Builder
	passed, failed := 0, 0
	for _, ph := range phases {
		f, err := os.Open(filepath.Join(res, ph+".txt"))
		if err != nil {
			fmt.Fprintf(&b, "FAIL %s: no result file (phase not run)\n", ph)
			failed++
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			if strings.Contains(l, " passed, ") && strings.HasSuffix(l, " failed") {
				break // the count line; what follows (yakbd-wincheck's differences) is detail
			}
			switch {
			case strings.HasPrefix(l, "PASS "):
				passed++
			case strings.HasPrefix(l, "FAIL "):
				failed++
			default:
				continue // count lines and details
			}
			fmt.Fprintf(&b, "%s [%s]\n", l, ph)
		}
		f.Close()
	}
	fmt.Fprintf(&b, "%s: %d passed, %d failed\n", guest, passed, failed)
	out := filepath.Join(res, guest+".txt")
	_ = os.WriteFile(out, []byte(b.String()), 0o644)
	fmt.Print(b.String())
	if failed > 0 {
		return 1
	}
	return 0
}

// x86Only: on x86 Windows the x86-only installer installs the x86 file into System32, types and uninstalls.
func (c *checker) x86Only(x86 *layout) {
	s := c.begin("x86-only-installer-on-x86")
	if x86 == nil {
		s.failf("demo-x86only-setup.exe missing")
		s.end()
		return
	}
	base := snapshot()
	if code := c.runInstaller(s, x86.exe, "9-x86only", "/quiet", "/noenable"); code != setup.ExitOK {
		s.failf("exit code %d", code)
	}
	if k, id := c.checkInstalled(s, x86, false); k != "" {
		hkl := uint32(hex(x86.p.Manifest.Layout.LangID)) | uint32(0xF000|id)<<16
		if msg, ok := typeLayout(k, x86.expect, hkl); !ok {
			s.failf("typing: %s", msg)
		}
	}
	c.uninstall(s, x86, "9-x86only-uninstall", base)
	s.end()
}

func contains(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

// The Settings removal alone (README of the w11settings bundle), by hand: settings-prepare installs the demo layout
// quietly (through PowerShell Direct, without enabling it for anyone); the runner removes it in Settings as the
// administrator or the standard user; settings-manual-check checks the installer's log and that nothing is left.
func (c *checker) settingsPrepare(a *layout, phase string) {
	s := c.begin(phase)
	if code := c.runInstaller(s, a.exe, phase, "/quiet", "/noenable"); code != setup.ExitOK {
		s.failf("install: exit code %d", code)
	}
	c.checkInstalled(s, a, false)
	_ = os.WriteFile(filepath.Join(c.res, "settings-base.txt"), []byte(withoutProduct(machineSnapshot(), a)), 0o644)
	s.end()
}

// withoutProduct drops the product's own lines from a machine snapshot: the state the removal must return to.
func withoutProduct(snap string, a *layout) string {
	m := a.p.Manifest
	var keep []string
	for _, l := range strings.Split(snap, "\n") {
		ll := strings.ToLower(l)
		if strings.Contains(ll, strings.ToLower(m.Product.Code)) || strings.Contains(ll, strings.ToLower(m.Layout.File)) ||
			strings.HasPrefix(l, "klid ") || strings.HasPrefix(l, "dir ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// settingsGone: the uninstall entry, the KLID and the Program Files folder are gone; files may wait for the restart.
func (c *checker) settingsGone(s *scenario, a *layout) {
	base, _ := os.ReadFile(filepath.Join(c.res, "settings-base.txt"))
	pending := strings.ToLower(strings.Join(pendingRenames(), "|"))
	if after := withoutPending(machineSnapshot(), pending); after != string(base) {
		s.failf("left behind: %s", firstDiff(string(base), after))
	}
}

// settingsManualCheck: after the runner removed the entry in Settings by hand, the installer's own log (in the
// temporary folder of the user who pressed Uninstall, copied to results\logs\manual.log by the runner) must show a
// run that Settings started with the uninstall entry's command, ending with 0 or 3010, and the product must be gone.
func (c *checker) settingsManualCheck(a *layout) {
	s := c.begin("settings-manual-uninstall")
	log, err := os.ReadFile(filepath.Join(c.logs, "manual.log"))
	if err != nil {
		s.failf("no results\\logs\\manual.log: %v", err)
	}
	started, done := false, ""
	for _, l := range strings.Split(string(log), "\n") {
		if strings.Contains(l, "start: uninstall") && strings.Contains(l, "/x") {
			started = true
		}
		if i := strings.Index(l, "done: exit code "); i >= 0 {
			done = strings.TrimSpace(l[i+len("done: exit code "):])
		}
	}
	if !started {
		s.failf("the log has no uninstall run started with /x (the uninstall entry's command)")
	}
	if done != fmt.Sprint(setup.ExitOK) && done != fmt.Sprint(setup.ExitReboot) {
		s.failf("the log's last run ended with %q", done)
	}
	c.settingsGone(s, a)
	s.end()
}
