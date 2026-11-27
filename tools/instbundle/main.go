// Command instbundle writes the Windows test bundle of the installer (docs/installer.md): installers of two layouts
// (all builds, a version 2, a single-build x86 and a single-build x64 one, two with payload files of the wrong
// shape), their expectations, the checker
// tools/instcheck for 64 and 32 bits, and README.txt with the guest commands. Run from the repository root:
//
//	go run ./tools/instbundle -o DIR
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/internal/cli"
)

const (
	demo    = "testdata/kbl-spec/examples/latin-altgr-deadkeys.kbl.json"
	kbdtst  = "testdata/reference-spec/vm/kbdtst.klc"
	minimal = "testdata/kbl-spec/examples/minimal-qwerty.kbl.json"
)

func main() {
	out := flag.String("o", "", "bundle folder (created; must not exist)")
	arm := flag.Bool("arm64", false, "the ARM64 bundle (all-builds installer, cold boot between install and check)")
	assets := flag.String("assets", "", "with -guest w11clean: the folder of files downloaded from a release draft")
	old := flag.String("old", "", "with -guest w11suite: the folder of the published earlier installers (cli-v0.1.0-preview.1, cli-v0.1.0-preview.2, gui-v0.1.0-preview.1)")
	guest := flag.String("guest", "", "a real-user bundle for one guest: w11x64, w10x64 or w10x86; w11signin, w11signin2, w11signin3: the sign-in screen bundles; w11plant: DLL planting; w11clean -assets DIR: a release on a clean machine; w11suite -assets DIR -old DIR: the two YAKBD installers")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/instbundle [-arm64 | -guest w11x64|w10x64|w10x86|w11signin|w11signin2|w11signin3|w11plant|w11clean -assets DIR|w11suite -assets DIR -old DIR] -o DIR")
		os.Exit(2)
	}
	write := write
	if *arm {
		write = writeARM64
	}
	if *guest != "" {
		write = func(out string) error { return writeGuest(out, *guest) }
	}
	switch *guest {
	case "w11signin":
		write = writeSignIn
	case "w11signin2":
		write = writeSignIn2
	case "w11signin3":
		write = writeSignIn3
	case "w11plant":
		write = writePlant
	case "w11clean":
		write = func(out string) error { return writeClean(out, *assets) }
	case "w11suite":
		write = func(out string) error { return writeSuite(out, *assets, *old) }
	}
	if err := write(*out); err != nil {
		fmt.Fprintln(os.Stderr, "instbundle:", err)
		os.Exit(1)
	}
}

func yakbd(args ...string) error {
	var o, e bytes.Buffer
	if code := cli.Run(args, &o, &e); code != cli.ExitOK {
		return fmt.Errorf("yakbd %s: exit %d\n%s%s", strings.Join(args, " "), code, o.String(), e.String())
	}
	fmt.Print(o.String())
	return nil
}

// expectations compiles a layout and copies its expectations file to dst.
func expectations(in, tmp, dst string) error {
	dir := filepath.Join(tmp, filepath.Base(dst))
	if err := yakbd("compile", "-q", "-emit-expectations", "-o", dir, in); err != nil {
		return err
	}
	m, _ := filepath.Glob(filepath.Join(dir, "*.expect.txt"))
	if len(m) != 1 {
		return fmt.Errorf("no expectations for %s", in)
	}
	b, err := os.ReadFile(m[0])
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func write(out string) error {
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

	// Version 2 of the demo layout: the same layout id, so the same product code; another version.
	b, err := os.ReadFile(demo)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	doc["metadata"].(map[string]any)["version"] = "0.2.0"
	v2b, _ := json.MarshalIndent(doc, "", "  ")
	v2 := filepath.Join(tmp, "demo-v2.kbl.json")
	if err := os.WriteFile(v2, v2b, 0o644); err != nil {
		return err
	}
	// VM-5: a layout for a language without LCID (LANGID 0C00), its own product.
	b, err = os.ReadFile(minimal)
	if err != nil {
		return err
	}
	var cdoc map[string]any
	if err := json.Unmarshal(b, &cdoc); err != nil {
		return err
	}
	meta := cdoc["metadata"].(map[string]any)
	meta["id"], meta["name"] = "qaa-t-k0-yktest", "YAKBD custom-language test"
	cdoc["targets"].(map[string]any)["windows"].(map[string]any)["registration"] = map[string]any{"langId": "0C00", "localeName": "qaa-Latn", "customLanguageName": "Yakbd Testish"}
	cb, _ := json.MarshalIndent(cdoc, "", "  ")
	custom := filepath.Join(tmp, "custom.kbl.json")
	if err := os.WriteFile(custom, cb, 0o644); err != nil {
		return err
	}
	steps := [][]string{
		{"installer", "-q", "-o", filepath.Join(out, "custom-0c00-setup.exe"), custom},
		{"installer", "-q", "-o", filepath.Join(out, "minimal-setup.exe"), minimal},
		{"installer", "-q", "-o", filepath.Join(out, "demo-setup.exe"), demo},
		{"installer", "-q", "-o", filepath.Join(out, "demo-v2-setup.exe"), v2},
		{"installer", "-q", "-build", "x86", "-o", filepath.Join(out, "demo-x86only-setup.exe"), demo},
		{"installer", "-q", "-build", "x64", "-enable", "false", "-o", filepath.Join(out, "kbdtst-x64only-setup.exe"), kbdtst},
		{"installer", "-q", "-enable", "false", "-o", filepath.Join(out, "kbdtst-setup.exe"), kbdtst},
	}
	for _, s := range steps {
		if err := yakbd(s...); err != nil {
			return err
		}
	}
	if err := badInstallers(out); err != nil {
		return err
	}
	if err := expectations(demo, tmp, filepath.Join(out, "demo.expect.txt")); err != nil {
		return err
	}
	if err := expectations(kbdtst, tmp, filepath.Join(out, "kbdtst.expect.txt")); err != nil {
		return err
	}
	if err := expectations(custom, tmp, filepath.Join(out, "custom.expect.txt")); err != nil {
		return err
	}
	if err := expectations(minimal, tmp, filepath.Join(out, "minimal.expect.txt")); err != nil {
		return err
	}
	if err := checkers(out, [][2]string{{"amd64", "instcheck.exe"}, {"386", "instcheck-32.exe"}}); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "README.txt"), []byte(readme), 0o644)
}

func checkers(out string, list [][2]string) error {
	for _, c := range list {
		cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(out, c[1]), "./tools/instcheck")
		cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+c[0], "CGO_ENABLED=0")
		if o, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %v\n%s", c[1], err, o)
		}
	}
	return nil
}

const readme = `yakbd installer test bundle (hardening; x64 guest only)
=================================================================

ONLY inside a disposable Windows virtual machine (Windows 10 or 11, x64), never on a working PC: the installers
write into the system directories, HKLM and the user's input list. Take a checkpoint first.

1. Copy this folder into the guest as C:\yk.
2. Run the checker ELEVATED IN THE INTERACTIVE CONSOLE SESSION (not PowerShell Direct, not session 0: loading
   layouts and InstallLayoutOrTip need the user's interactive session), as in VM run 1, e.g. a scheduled task with
   an interactive logon and highest privileges for the signed-in administrator:

       cd /d C:\yk
       instcheck.exe -bundle C:\yk

   It takes about a minute (each installer has a 3-minute limit). instcheck-32.exe is a helper it starts itself.
3. Copy C:\yk\results\ out of the guest: instcheck.txt (one PASS/FAIL line per scenario and a count),
   instcheck-details.txt (every check and installer exit code), logs\*.log (each installer run's own log).
4. Sign-in part (same session rules), right after step 2 without restoring the checkpoint:
       instcheck.exe -bundle C:\yk -phase reenable-prepare
   Cold boot with automatic sign-in of the same account (set these again before every cold boot; <account> and
   <password> of the account the scheduled task runs as):
       reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoAdminLogon /t REG_SZ /d 1 /f
       reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultUserName /t REG_SZ /d <account> /f
       reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v DefaultPassword /t REG_SZ /d <password> /f
   then power off (shutdown /s /t 0), start the VM from the host, wait for the sign-in and about one minute, and run
       instcheck.exe -bundle C:\yk -phase reenable-check
   Copy C:\yk\results\ out again (reenable-prepare.txt, reenable-check.txt, their *-details.txt, logs\signin-*).
5. Restore the checkpoint.

Installers in the bundle (all unsigned):
  demo-setup.exe            example (b) latin-altgr-deadkeys, all builds (x64 + wow64 + x86); enables for the
                            installing user (rule enableForUser = true)
  demo-v2-setup.exe         the same layout, version 0.2.0 (same product code: upgrades in place)
  demo-x86only-setup.exe    the same layout, built with -build x86 (only the x86 file)
  kbdtst-x64only-setup.exe  kbdtst.klc, built with -build x64 -enable false (x64 + wow64 files, not enabled)
  minimal-setup.exe         example (a) minimal-qwerty (LANGID 0409), all builds
  custom-0c00-setup.exe     minimal-qwerty with LANGID 0C00, Layout Locale Name qaa-Latn (VM-5 route, D63)
  demo-badshape-setup.exe   demo with the x86 file in the wow64 slot (right Machine and hashes, wrong shape)
  demo-damaged-setup.exe    demo with a truncated x64 file (right Machine and hashes); both made by a test-only
                            path, yakbd installer refuses to write them

Scenarios, in order:
  refuse-x86-only-on-x64       x86-only installer /quiet: exit 1633, nothing changed
  install-all-builds-enabled   demo-setup.exe /quiet ENABLE=1: exit 0; System32 = x64 build, SysWOW64 = wow64 build;
                               KLID a...LLLL with Layout File/Text/Display Name (REG_EXPAND_SZ)/Id (00c0..00ff)/
                               Product Code; uninstall entry; the layout in the user's input list
  type-64-and-32-bit           LoadKeyboardLayoutW(KLID) gives HKL F<id>LLLL; every expectation line through
                               ToUnicodeEx in this 64-bit process and in instcheck-32.exe
  install-again-same-klid      demo-setup.exe again: same KLID and Layout Id
  upgrade-in-place             demo-v2-setup.exe: same KLID and Layout Id, DisplayVersion 0.2.0.0; a file in use
                               gives exit 3010 and a pending rename (allowed)
  repair-lost-klid             the KLID key deleted by hand, then Program Files\YAKBD\{code}\setup.exe /f:
                               the same KLID and Layout Id again
  uninstall-leaves-nothing     the installed setup.exe /uninstall /quiet: exit 0 (or 3010 with the files pending
                               deletion at restart); KLID, files, uninstall entry, Program Files folder and the
                               user's Preload/Substitutes/User Profile entries as before the first install;
                               the line lists what waits for the restart (stored paths)
  reinstall-before-restart-refused  only when the uninstall left a file for deletion at restart: demo-setup.exe
                               again gives exit 1603 and changes nothing (D62); otherwise PASS "not applicable"
  foreign-file-refused         a foreign System32\kbdtst.dll: the kbdtst installer fails (exit 1603), the file is
                               untouched, nothing else is left
  x64-only-not-enabled         kbdtst-x64only-setup.exe /quiet: installed, NOT in the user's list, types in a 32-bit
                               (first, so the session uses the SysWOW64 file) and a 64-bit process, uninstalls cleanly

  multi-layouts-one-language   demo, kbdtst and minimal (all LANGID 0409) installed /quiet ENABLE=1 back to back (the
                               installer reads User Profile back): three KLIDs and Layout Ids, all three in the list, each
                               types (64- and 32-bit); then all uninstalled, nothing left
  vm5-install-enables-0c00     custom-0c00-setup.exe /quiet: exit 0; KLID ...0c00 with Layout Locale Name and the
                               Custom Language values; the installer added qaa-Latn to the user's list, Windows
                               gave it a TransientLangId, the layout is in the list and types (D63)
  vm5-uninstall-removes-layout-and-tag  uninstall: the layout, the tag it added, its record gone; nothing left
                               (Windows' own CachedLanguageName value is not counted)

  refuse-bad-shape-payload     demo-badshape-setup.exe and demo-damaged-setup.exe /quiet ENABLE=1: exit 1620 each, the
                               log names the file that failed the shape check, nothing changed
  repair-new-layout-id-reenables  demo installed and enabled; then the Layout Id is given to another KLID of
                               LANGID 0409 (b0990409, reg.exe) and the layout's KLID key deleted (M7); setup.exe
                               /f /quiet: a new Layout Id (record and KLID agree), the layout in the user's
                               list, its new HKL in a NEW process's GetKeyboardLayoutList (VM-9; the repair removes
                               the layout and adds it again), typing 64- and 32-bit with the new HKL;
                               the note gives the old and new Layout Id and HKL; then b0990409 deleted,
                               uninstalled, nothing left

15 scenarios. Exit code of instcheck.exe: 0 when every scenario passed. BUILT-FROM.txt names the tool commit.

Then the sign-in part (step 4 above): results\reenable-prepare.txt and results\reenable-check.txt
  reenable-prepare             as repair-new-layout-id-reenables up to the repair; the layout is left installed
  reenable-after-sign-in       after the cold boot and automatic sign-in: the layout in the user's list, the new
                               HKL in this process's and in a new process's GetKeyboardLayoutList, typing with the
                               new HKL (64-bit) and in a 32-bit process
  reenable-cleanup-leaves-nothing  b0990409 deleted, uninstalled, nothing left
`
