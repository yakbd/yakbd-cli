// Command importbundle writes the folder for the DLL import check on Windows (docs/dll-import.md §5): the Windows
// build of yakbd, both builds of yakbd-wincheck and a README with the guest commands. Run it from the repository
// root:
//
//	go run ./tools/importbundle -o /path/to/bundle
//
// Nothing is run on Windows here; the bundle is copied into a disposable virtual machine.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr, true)) }

func run(args []string, stderr io.Writer, buildExes bool) int {
	fs := flag.NewFlagSet("importbundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "importbundle", "output folder (created; must not exist)")
	repo := fs.String("repo", ".", "the repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintf(stderr, "importbundle: %s exists; choose a new folder\n", *out)
		return 2
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(stderr, "importbundle:", err)
		return 2
	}
	abs, _ := filepath.Abs(*out)
	if buildExes {
		rev, _ := exec.Command("git", "-C", *repo, "rev-parse", "--short", "HEAD").Output()
		ldflags := "-X main.builtFrom=" + strings.TrimSpace(string(rev))
		for _, t := range []struct{ arch, pkg, file string }{
			{"amd64", "./cmd/yakbd", "yakbd.exe"},
			{"amd64", "./cmd/yakbd-wincheck", "yakbd-wincheck-64.exe"},
			{"386", "./cmd/yakbd-wincheck", "yakbd-wincheck-32.exe"},
		} {
			cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", filepath.Join(abs, t.file), t.pkg)
			cmd.Dir = *repo
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+t.arch, "CGO_ENABLED=0")
			cmd.Stdout, cmd.Stderr = stderr, stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintln(stderr, "importbundle:", err)
				return 2
			}
		}
	}
	if err := os.WriteFile(filepath.Join(*out, "README.txt"), []byte(readme), 0o644); err != nil {
		fmt.Fprintln(stderr, "importbundle:", err)
		return 2
	}
	return 0
}

const readme = `yakbd DLL import check
==========================================

ONLY inside a disposable Windows virtual machine (Windows 11 or 10, x64), never on a working PC: the runner
writes test files into System32 / SysWOW64 and test keys into the registry. Take a checkpoint first.

Each results file starts with the commit the bundle was built from ("built from ..."): check it against
BUILT-FROM.txt.

THIS ROUND: the full run again (D65 merged, VM-7 and VM-8 settled; this confirms the final reading on every file):
the control (step 1) and both full runs (step 2), as written below.

What it does, per layout file Windows ships (every kbd*.dll of System32 for the 64-bit run, of SysWOW64 for the
32-bit run, read as data):
  1. yakbd.exe imports the file (yakbd convert <copy> layout.kbl.json -langid 0409 -keep-keyboard-type) and builds
     it again (yakbd compile -target x64 or wow64; a file that builds only with -accept windows.*,general.* is
     counted as such and built with it), in a temporary folder that is deleted right after;
  2. the original and the rebuilt file are installed under test names (yktiNNN.dll / yktjNNN.dll), KLIDs a4NN0409 to
     a7NN0409 and Layout Ids 0200-05FF, loaded with LoadKeyboardLayoutW in a child process with a time limit, and
     compared: ToUnicodeEx for every virtual key 01-FE in every combination of Shift, Ctrl, left Alt, right Alt and
     the layout's extra modifier keys, with Caps Lock off and on (and Kana toggled where the layout has a Kana
     modifier); every dead key of either file followed by every key; MapVirtualKeyEx for every scan code (plain,
     E0, E1);
  3. both are unloaded and removed again.

VM-7 (kbl-spec targets/windows-vm-tests.md): before the files, each run (not the control) loads a test
layout of yakbd's own (not Windows data) with two SGCAPS keys whose Caps Lock row has WCH_NONE where the main
row has output, and prints one line "VM-7 (<build>): ..." with what ToUnicodeEx gives for them with Caps Lock on,
and four controls. That line is the result of VM-7; please copy it as it is.
VM-8 (D65): then two lines "VM-8 shape A ..." and "VM-8 shape B ...": two more test layouts of yakbd's own (an
SGCAPS key with Caps Lock forms in its AltGr columns, as two rows and as one shared FF row, and a CAPLOKALTGR
control key), probed with Caps Lock and AltGr and, for shape B, a dead key followed by A. Copy them as they are.

Privacy: apart from the VM-7 and VM-8 lines, the results hold ONLY counts and, per failing file, its file name and one fixed category (passing files are
only counted): "import
refused", "build refused", "load refused (original|rebuilt)", "another layout answered (...)", "N cells differ",
"timed out", "the test process crashed (...)", "setup failed", "rebuilt file unreadable"; a "cells differ" line adds
three counts (keys, dead-key sequences, scan codes) and two lines that name only mechanisms and table fields, with
counts: "mechanisms: ..." (modifier column numbers, Caps Lock, row attributes, cell kinds such as ligature or
dead-key cell, whether the rows of the two files differ, dead-key entry kinds, NLS or dispatcher features) and
"tables differ in: ..." (field names such as character rows or DEADKEY). Then summary counts per
category, the number of files whose tables differ (yakbd's own comparison), and per yakbd loss-kind id the number
of files that reported it. No character, table, message or file content of Windows' layouts is written anywhere
outside the temporary folders, which are deleted. Copy back ONLY the results files (step 3).

Steps
-----
1. Copy this folder into the guest as C:\ykimp.
   FIRST run the control (each original compared with itself, yakbd not used; checks the harness), on the first
   10 files, in the same way as the runs in step 2 (scheduled task or console):

       C:\ykimp\yakbd-wincheck-64.exe -import -control -limit 10 -out C:\ykimp\results-control-64.txt
       C:\ykimp\yakbd-wincheck-32.exe -import -control -limit 10 -out C:\ykimp\results-control-32.txt

   Every file must pass ("10 of 10 files pass"). If not, still do step 2, but say so with the results.
2. The runner needs the interactive desktop of the signed-in user (LoadKeyboardLayoutW returns NULL without one:
   PowerShell Direct, a remote session, a service); it refuses to start there. From a remote session run it as a
   scheduled task with an interactive logon and highest privileges, as in VM run 1:

       schtasks /create /tn ykimp64 /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\ykimp\yakbd-wincheck-64.exe -import -yakbd C:\ykimp\yakbd.exe -out C:\ykimp\results-import-64.txt > C:\ykimp\console-64.txt 2>&1"
       schtasks /run /tn ykimp64

   Wait until results-import-64.txt has its line "64-bit import check: N of M files pass" and the count lines
   after it (a few minutes to two hours; each file has a limit of 10 minutes), then the same with the 32-bit runner:

       schtasks /create /tn ykimp32 /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\ykimp\yakbd-wincheck-32.exe -import -yakbd C:\ykimp\yakbd.exe -out C:\ykimp\results-import-32.txt > C:\ykimp\console-32.txt 2>&1"
       schtasks /run /tn ykimp32

   <user> is the account signed in at the console. Or, at the console, in an elevated Command Prompt:

       cd /d C:\ykimp
       yakbd-wincheck-64.exe -import -yakbd C:\ykimp\yakbd.exe -out C:\ykimp\results-import-64.txt
       yakbd-wincheck-32.exe -import -yakbd C:\ykimp\yakbd.exe -out C:\ykimp\results-import-32.txt

   Optional: -only kbdus limits a run to file names containing that text; -timeout 10m is the limit per file.
3. Copy results-control-64.txt, results-control-32.txt, results-import-64.txt and results-import-32.txt out of the guest (console-*.txt hold the same lines
   and the runner's own start-up errors, if any; they may be copied too). Nothing else.
4. Restore the checkpoint. (The runner removes every test file and key; a file still locked is removed at the
   next start.)

Exit code 0 = every file passed, 1 = some failed, 2 = the run could not start.
`
