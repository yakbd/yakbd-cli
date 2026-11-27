// Command vmbundle builds the folder for the unattended Windows test: both builds of yakbd-wincheck, every
// layout of the test corpus compiled with its expectations, a rebased-load case, a manifest and a
// README with the guest commands. Run it from the repository root:
//
//	go run ./tools/vmbundle -o /path/to/bundle
//
// Nothing is run on Windows here; the bundle is copied into a disposable virtual machine.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, true))
}

// source is one layout of the corpus.
type source struct {
	name, path string
	extra      []string // compile flags
	rebase     bool
	note       string
}

func corpus(repo string) []source {
	var out []source
	add := func(prefix, glob string) {
		files, _ := filepath.Glob(filepath.Join(repo, glob))
		sort.Strings(files)
		for _, f := range files {
			base := filepath.Base(f)
			base = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(base, ".kbl.json"), ".klc"), ".xml")
			out = append(out, source{name: prefix + base, path: f})
		}
	}
	add("kbl-", "testdata/kbl-spec/examples/*.kbl.json")
	add("klc-", "testdata/reference-spec/*/*.klc")
	add("cldr-", "testdata/third_party/cldr-release-49/keyboards/3.0/*.xml")
	base := filepath.Join(repo, "testdata/reference-spec/vm/kbdtst.klc")
	out = append(out,
		// A rebased load: the runner reserves the DLL's preferred image base first (the case of VM-1, VM run 1).
		source{"rebase-kbdtst", base, nil, true, "kbdtst.klc loaded after its preferred image base was taken"},
	)
	return out
}

func run(args []string, stdout, stderr io.Writer, buildRunners bool) int {
	fs := flag.NewFlagSet("vmbundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "vmbundle", "output folder (created; must not exist)")
	repo := fs.String("repo", ".", "the repository root")
	arm := fs.Bool("arm64", false, "make the Windows on ARM bundle (ARM64X candidates, three runners) instead")
	signin := fs.Bool("signin", false, "with -arm64: the sign-in bundle (persistent install, restart, check with ctfmon and real input) instead")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintf(stderr, "vmbundle: %s exists; choose a new folder\n", *out)
		return 2
	}
	if err := os.MkdirAll(filepath.Join(*out, "layouts"), 0o755); err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 2
	}
	if *arm {
		return runARM64(*repo, *out, stdout, stderr, buildRunners, *signin)
	}
	if buildRunners {
		for _, t := range []struct{ arch, file string }{{"amd64", "yakbd-wincheck-64.exe"}, {"386", "yakbd-wincheck-32.exe"}} {
			cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(*out, t.file), "./cmd/yakbd-wincheck")
			cmd.Dir = *repo
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+t.arch, "CGO_ENABLED=0")
			cmd.Stdout, cmd.Stderr = stderr, stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(stderr, "vmbundle: building %s: %v\n", t.file, err)
				return 1
			}
		}
	}
	var entries []wincheck.Entry
	var skipped []string
	for _, s := range corpus(*repo) {
		dir := filepath.Join(*out, "layouts", s.name)
		args := append([]string{"compile", "-q", "-o", dir, "-emit-expectations", "-langid", "0409",
			"-accept", "windows.vkMissing"}, s.extra...)
		var o, e bytes.Buffer
		if code := cli.Run(append(args, s.path), &o, &e); code != 0 {
			reason := strings.TrimSpace(firstLine(e.String()))
			skipped = append(skipped, fmt.Sprintf("%s: %s", s.name, reason))
			os.RemoveAll(dir)
			continue
		}
		exp, _ := filepath.Glob(filepath.Join(dir, "*.expect.txt"))
		if len(exp) != 1 {
			skipped = append(skipped, s.name+": no expectations")
			continue
		}
		e2 := wincheck.Entry{Name: s.name, DLL: map[string]string{}, Rebase: s.rebase, Note: s.note}
		e2.Expect, _ = filepath.Rel(*out, exp[0])
		for _, b := range wincheck.Builds {
			if m, _ := filepath.Glob(filepath.Join(dir, b, "*.dll")); len(m) == 1 {
				e2.DLL[b], _ = filepath.Rel(*out, m[0])
				e2.DLL[b] = filepath.ToSlash(e2.DLL[b])
			}
		}
		e2.Expect = filepath.ToSlash(e2.Expect)
		entries = append(entries, e2)
	}
	v4, err := vm4(*repo, *out)
	if err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 1
	}
	entries = append(entries, v4...)
	var m bytes.Buffer
	wincheck.WriteManifest(&m, entries)
	if err := os.WriteFile(filepath.Join(*out, "manifest.txt"), m.Bytes(), 0o644); err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(*out, "README.txt"), []byte(readme(len(entries), skipped)), 0o644); err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %d layouts, %d skipped\n", *out, len(entries), len(skipped))
	return 0
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "error") {
			return l
		}
	}
	return strings.SplitN(s, "\n", 2)[0]
}

func readme(n int, skipped []string) string {
	var b strings.Builder
	b.WriteString(`yakbd Windows test bundle
=========================

ONLY inside a disposable Windows virtual machine (Windows 10 or 11, x64), never on a working PC: the runner
writes into the system directories and the registry. Take a checkpoint first.

1. Copy this folder into the guest as C:\yk.
2. The runner needs the interactive desktop of the signed-in user: LoadKeyboardLayoutW returns NULL in a session
   without one (PowerShell Direct, a remote PowerShell session, a service). It refuses to start there. Run it
   either from an elevated Command Prompt opened at the console, or, from a remote session, as a scheduled task
   with an interactive logon and highest privileges (this is what VM run 1 used):

       schtasks /create /tn yk64 /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\yk\yakbd-wincheck-64.exe -bundle C:\yk -out C:\yk\results-64.txt > C:\yk\console-64.txt 2>&1"
       schtasks /run /tn yk64

   and the same with yk32 and yakbd-wincheck-32.exe once results-64.txt has its count line. <user> is the
   account signed in at the console.
3. At the console, run one after the other (the 64-bit run tests the x64 DLLs, the 32-bit run the WOW64 DLLs):

       cd /d C:\yk
       yakbd-wincheck-64.exe -bundle C:\yk -out C:\yk\results-64.txt
       timeout /t 5
       yakbd-wincheck-32.exe -bundle C:\yk -out C:\yk\results-32.txt

   On a 32-bit Windows run only yakbd-wincheck-32.exe; it tests the x86 DLLs.
4. Copy results-64.txt and results-32.txt out of the guest. Each starts with one line per layout
   ("PASS name" or "FAIL name: first differences") and a count; the full differences follow. The 64-bit
   results also have the line "VM-1 Windows files", read from Windows' own SysWOW64 layout files as data.
5. Restore the checkpoint (every test layout is removed by the runner; a file that was still locked is removed at
   the next start).

What the runner does per layout: copies the DLL into the system directory as yk64NNN.dll / yk32NNN.dll, writes
Layout File, Layout Text and Layout Id under KLID a1NN0409 (64-bit) or a2NN0409 (32-bit), calls
LoadKeyboardLayoutW (KLF_ACTIVATE) on its own thread, checks that the HKL carries the test Layout Id, types every
line of the expectations through ToUnicodeEx (single keys with flag 4, dead-key sequences for real, clearing the
dead-key state after each), unloads the layout and removes the file and the key. Each layout is loaded and typed in a child process of its
own with a time limit (-timeout, default 2m), so a crash or hang fails that layout only ("the test process
crashed", "timed out"); the results file is written line by line as the run goes. Exit code 0 = all passed.
A file still locked after the test is noted next to its PASS or FAIL and removed at the next start.

Difference to the reference specification's procedure (dll-format tests/vm): there, every variant was put into
both System32 and SysWOW64 and loaded by a 64-bit and a 32-bit probe at once. Here each runner installs only the
build it loads (the 64-bit runner the x64 DLL into System32, the 32-bit runner the WOW64 DLL, which file-system
redirection puts into SysWOW64), under KLIDs and Layout Ids of its own, so the two runs never share a layout.

Tests VM-1 to VM-3 were settled by VM run 1 (Windows 11 25H2): the descriptor relocation of the 32-bit DLLs is
required (a rebased file without it does not load; 17 of 17 SysWOW64 files with that function have it), and the
linker version and the stack and heap sizes make no difference. The layout rebase-kbdtst keeps a rebased load in
every run: the runner reserves its preferred image base before loading it.

VM-4 (open, kbl-spec targets/windows-vm-tests.md): the layouts vm4-caps-dead-c<col>-<ff|noff> are kbdtst.klc
with key Q made SGCAPS (main row q Q, Caps Lock row Q q) and WCH_DEAD in column <col> of the Caps Lock row,
with (ff) or without (noff) a dead-character row after it; the normal build refuses that shape. Their last
lines (after "# VM-4 hypothesis H") type Q with Caps Lock on (modifier 14 = Caps Lock toggled on) and then a
base letter. The expected values are a hypothesis (the dead character is taken from the row after the Caps Lock
row); a FAIL line of a vm4 layout is therefore a result to copy out, not a broken run: its "got" values are
what ToUnicodeEx returned. WM_DEADCHAR / WM_CHAR are not recorded (the runner has no message loop).

Right Shift: for a layout with KLLF_LRM_RLM (kbl-windows-features) the lines "key 08+a1" (right Shift alone,
expected U+200F) and "key 08+10+a1" (both Shift keys, expected U+200E) test the right-Shift case of
windows-behaviour §11; modifier a1 means the right Shift key (generic Shift and VK_RSHIFT down).

`)
	fmt.Fprintf(&b, "Layouts in this bundle: %d.\n", n)
	if len(skipped) > 0 {
		b.WriteString("\nNot in the bundle (the tool refuses to build them):\n")
		for _, s := range skipped {
			b.WriteString("  " + s + "\n")
		}
	}
	return b.String()
}
