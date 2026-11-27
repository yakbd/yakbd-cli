// Command cldrbundle builds the folder for the CLDR conformance run on Windows (docs/cldr.md §4): every check of
// the CLDR test files that a Windows build can express (convert/cldrconf.Cases) becomes one manifest entry of the
// yakbd-wincheck runners, with the keyboard's DLLs and a one-line expectations file that types the key presses of
// the case and compares the text with CLDR's expected result. The runners print one PASS or FAIL line per case
// and a count. Run it from the repository root:
//
//	go run ./tools/cldrbundle -o /path/to/bundle
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
	"strings"

	"github.com/yakbd/yakbd-cli/convert/cldrconf"
	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, true))
}

func run(args []string, stdout, stderr io.Writer, buildRunners bool) int {
	fs := flag.NewFlagSet("cldrbundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "cldrbundle", "output folder (created; must not exist)")
	repo := fs.String("repo", ".", "the repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintf(stderr, "cldrbundle: %s exists; choose a new folder\n", *out)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "cldrbundle:", err)
		return 1
	}
	for _, d := range []string{"layouts", "cases", "results", "control"} {
		if err := os.MkdirAll(filepath.Join(*out, d), 0o755); err != nil {
			return fail(err)
		}
	}
	if buildRunners {
		for _, t := range []struct{ arch, file string }{{"amd64", "yakbd-wincheck-64.exe"}, {"386", "yakbd-wincheck-32.exe"}} {
			cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(*out, t.file), "./cmd/yakbd-wincheck")
			cmd.Dir = *repo
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+t.arch, "CGO_ENABLED=0")
			cmd.Stdout, cmd.Stderr = stderr, stderr
			if err := cmd.Run(); err != nil {
				return fail(fmt.Errorf("building %s: %v", t.file, err))
			}
		}
	}
	ks, err := cldrconf.Keyboards(*repo)
	if err != nil {
		return fail(err)
	}
	var entries []wincheck.Entry
	var table strings.Builder
	table.WriteString("# Every check of the CLDR release-49 test files: typed in the guest, or why a Windows build cannot express it.\n")
	typed, skipped := 0, 0
	for _, k := range ks {
		if k.TestPath == "" {
			continue
		}
		l, r := cldrconf.Load(k)
		if l == nil {
			return fail(fmt.Errorf("%s: %s", k.Name, r.Summary()))
		}
		res, br := cldrconf.Build(l)
		if res == nil {
			return fail(fmt.Errorf("%s: %s", k.Name, br.Summary()))
		}
		cases := cldrconf.Cases(l, res)
		dlls := map[string]string{}
		for _, c := range cases {
			fmt.Fprintf(&table, "%s\t%s\n", k.Name, c)
			if c.Skip != "" {
				skipped++
				continue
			}
			typed++
			if len(dlls) == 0 {
				if dlls, err = compile(*repo, *out, k); err != nil {
					return fail(err)
				}
			}
			file := "cases/" + fmt.Sprintf("%03d", typed) + ".expect.txt"
			line := fmt.Sprintf("# %s %s (%s)\n%s\n", k.Name, c.Name, filepath.Base(k.TestPath), c.Line())
			if err := os.WriteFile(filepath.Join(*out, filepath.FromSlash(file)), []byte(line), 0o644); err != nil {
				return fail(err)
			}
			entries = append(entries, wincheck.Entry{Name: k.Name + ":" + c.Name, DLL: dlls, Expect: file,
				Note: "CLDR " + filepath.Base(k.TestPath)})
		}
	}
	if len(entries) == 0 || len(entries) > 255 {
		return fail(fmt.Errorf("%d typed cases (want 1 to 255)", len(entries)))
	}
	fmt.Fprintf(&table, "# %d typed, %d skipped\n", typed, skipped)
	var m bytes.Buffer
	wincheck.WriteManifest(&m, entries)
	files := map[string]string{
		"manifest.txt":          m.String(),
		"cases.txt":             table.String(),
		"README.txt":            readme(typed, skipped),
		"control/us.expect.txt": wincheck.ControlExpectations,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(*out, name), []byte(text), 0o644); err != nil {
			return fail(err)
		}
	}
	fmt.Fprintf(stdout, "%s: %d typed cases, %d skipped\n", *out, typed, skipped)
	return 0
}

// compile builds the keyboard's DLLs with the command line, as cldrconf.Build lowers it, and returns the manifest
// paths of each build.
func compile(repo, out string, k cldrconf.Keyboard) (map[string]string, error) {
	dir := filepath.Join(out, "layouts", k.Name)
	args := []string{"compile", "-q", "-o", dir, "-langid", cldrconf.LangID, "-accept", strings.Join(cldrconf.Accepted, ","), k.Path}
	var o, e bytes.Buffer
	if code := cli.Run(args, &o, &e); code != 0 {
		return nil, fmt.Errorf("%s: compile: %s", k.Name, e.String())
	}
	dlls := map[string]string{}
	for _, b := range wincheck.Builds[:3] {
		m, _ := filepath.Glob(filepath.Join(dir, b, "*.dll"))
		if len(m) != 1 {
			return nil, fmt.Errorf("%s: no %s DLL", k.Name, b)
		}
		rel, _ := filepath.Rel(out, m[0])
		dlls[b] = filepath.ToSlash(rel)
	}
	return dlls, nil
}

func readme(typed, skipped int) string {
	return fmt.Sprintf(`yakbd CLDR conformance bundle (docs/cldr.md)
============================================

Guest: w11x64 (Windows 11 x64), disposable, restored to its checkpoint before the run. ONLY inside that virtual
machine: the runner writes into the system directories and the registry.

What it tests: every check of the CLDR release-49 keyboard test files that a Windows build can express (%d
cases; %d more are listed in cases.txt with the reason they are skipped). Each case is one manifest entry: the
keyboard's DLL (built by yakbd with LANGID 0409) and a one-line expectations file

    type P1 P2 ... -> chars U1 U2 ...

P = virtual key in hex, +10 Shift, +11 Ctrl, +12 Alt held; U = the UTF-16 units CLDR's check expects (after the
test's start context). The runner loads the layout, types every press through ToUnicodeEx for real (dead keys
included), and compares the units of all presses together with U.

1. Copy this folder into the guest as C:\yk.
2. Run the two runners in the console session (they need the interactive desktop; from PowerShell Direct use
   a scheduled task with an interactive logon, as below). <user> is the account signed in at the console.

       schtasks /create /tn yk64 /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\yk\yakbd-wincheck-64.exe -control -bundle C:\yk -out C:\yk\results\results-64.txt > C:\yk\results\console-64.txt 2>&1"
       schtasks /run /tn yk64

   wait until results\results-64.txt ends its first block with the line "64-bit runner: N passed, M failed",
   then the same with yk32, yakbd-wincheck-32.exe and results-32.txt / console-32.txt:

       schtasks /create /tn yk32 /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\yk\yakbd-wincheck-32.exe -control -bundle C:\yk -out C:\yk\results\results-32.txt > C:\yk\results\console-32.txt 2>&1"
       schtasks /run /tn yk32

   (At an elevated console prompt instead: cd /d C:\yk, then yakbd-wincheck-64.exe -control -bundle C:\yk -out
   C:\yk\results\results-64.txt, then the same with -32.) The 64-bit runner types in a 64-bit process with the
   x64 DLLs, the 32-bit runner in a 32-bit process with the WOW64 DLLs.
3. Copy results\ out of the guest. Report per runner: the count line, every FAIL line (with its first differences),
   and the control line. Each file has one line per case, "PASS <keyboard>:<test file>/<suite>/<test>#<check>" or
   "FAIL ...: line 2: type ... -> chars ..., got ...", then the count; full differences follow. The line
   "control.<build>" is Windows' own US layout typed first, a known-good check that the runner works.
   No screenshots are needed.
4. Restore the checkpoint.

A FAIL means Windows typed something else than the emulator of the reference specification's ToUnicodeEx rules
predicted for yakbd's DLL (every case here passes that emulator): copy its "got" units out unchanged.
`, typed, skipped)
}
