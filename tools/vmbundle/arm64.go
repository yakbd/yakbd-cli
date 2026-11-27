package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// The ARM64 bundle (-arm64): the candidates of docs/arm64x.md for System32 of Windows on ARM, tested by three
// runners in one guest: the native ARM64 runner and the x64 runner on the arm64 column (the System32 file), the
// 32-bit runner on the WOW64 file. Entry names are <build>.<layout>, so a result line says which candidate passed.

// arm64Layouts are the layouts of the ARM64 bundle: spec examples, .klc inputs and a CLDR keyboard.
var arm64Layouts = []struct{ name, path string }{
	{"klc-kbdtst", "testdata/reference-spec/vm/kbdtst.klc"},
	{"klc-wbt1", "testdata/reference-spec/vm/wbt1.klc"},
	{"kbl-minimal-qwerty", "testdata/kbl-spec/examples/minimal-qwerty.kbl.json"},
	{"kbl-latin-altgr-deadkeys", "testdata/kbl-spec/examples/latin-altgr-deadkeys.kbl.json"},
	{"kbl-windows-features", "testdata/kbl-spec/examples/windows-features.kbl.json"},
	{"cldr-pt-t-k0-abnt2", "testdata/third_party/cldr-release-49/keyboards/3.0/pt-t-k0-abnt2.xml"},
}

// arm64Entry is one System32 candidate: a build name and how to make it from the layout's DLL value.
type arm64Entry struct {
	build  string // part of the entry name
	all    bool   // for every layout (else only the first)
	rebase bool
	make   func(d *windows.DLL) ([]byte, error)
	note   string
}

func arm64x(v windows.ARM64XVariant) func(d *windows.DLL) ([]byte, error) {
	return func(d *windows.DLL) ([]byte, error) {
		b, r := windows.CompileARM64X(d, v)
		if b == nil {
			return nil, fmt.Errorf("%s", r.Summary())
		}
		return b, nil
	}
}

// arm64Entries in run order: the control first, Windows' shape next, the simplifications and the rebased load
// last.
var arm64Entries = []arm64Entry{
	{build: "arm64", make: func(d *windows.DLL) ([]byte, error) {
		b, r := windows.Compile(d, windows.ARM64)
		if b == nil {
			return nil, fmt.Errorf("%s", r.Summary())
		}
		return b, nil
	}, note: "control: plain ARM64 file; native ARM64 should PASS, x64 should FAIL with NULL / 193 (installation §2.4)"},
	{build: "arm64x-ec", all: true, make: arm64x(windows.VariantEC),
		note: "ARM64X, x64 export thunk -> ARM64EC function with entry thunk (Windows' and lld's shape)"},
	{build: "arm64x-x64", all: true, make: arm64x(windows.VariantX64),
		note: "ARM64X, x64 export -> plain x64 function"},
	{build: "arm64x-x64-onecfg", make: arm64x(windows.VariantX64OneCfg),
		note: "ARM64X as arm64x-x64 with one load configuration for both views"},
	{build: "arm64x-ec-rebased", rebase: true, make: arm64x(windows.VariantEC),
		note: "arm64x-ec loaded after its preferred image base was taken"},
}

func runARM64(repo, out string, stdout, stderr io.Writer, buildRunners, signin bool) int {
	if buildRunners {
		for _, t := range []struct{ arch, file string }{{"arm64", "yakbd-wincheck-arm64.exe"},
			{"amd64", "yakbd-wincheck-64.exe"}, {"386", "yakbd-wincheck-32.exe"}} {
			cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(out, t.file), "./cmd/yakbd-wincheck")
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+t.arch, "CGO_ENABLED=0")
			cmd.Stdout, cmd.Stderr = stderr, stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(stderr, "vmbundle: building %s: %v\n", t.file, err)
				return 1
			}
		}
	}
	type built struct {
		dll     *windows.DLL
		files   map[string]string // x64, wow64, x86 -> bundle path
		expect  string
		dllName string
	}
	var layouts []built
	for _, l := range arm64Layouts {
		dir := filepath.Join(out, "layouts", l.name)
		var o, e bytes.Buffer
		args := []string{"compile", "-q", "-o", dir, "-emit-expectations", "-langid", "0409", "-accept", "windows.vkMissing",
			"-target", "x64,wow64,x86", filepath.Join(repo, l.path)}
		if code := cli.Run(args, &o, &e); code != 0 {
			fmt.Fprintf(stderr, "vmbundle: %s: %s\n", l.name, firstLine(e.String()))
			return 1
		}
		b := built{files: map[string]string{}}
		for _, c := range []string{"x64", "wow64", "x86"} {
			m, _ := filepath.Glob(filepath.Join(dir, c, "*.dll"))
			if len(m) != 1 {
				fmt.Fprintf(stderr, "vmbundle: %s: no %s DLL\n", l.name, c)
				return 1
			}
			rel, _ := filepath.Rel(out, m[0])
			b.files[c] = filepath.ToSlash(rel)
		}
		exp, _ := filepath.Glob(filepath.Join(dir, "*.expect.txt"))
		if len(exp) != 1 {
			fmt.Fprintf(stderr, "vmbundle: %s: no expectations\n", l.name)
			return 1
		}
		rel, _ := filepath.Rel(out, exp[0])
		b.expect = filepath.ToSlash(rel)
		// The DLL value, read back from the x64 build (the reader gives back exactly what was compiled).
		x64, _ := os.ReadFile(filepath.Join(out, b.files["x64"]))
		d, _, err := windows.Read(x64)
		if err != nil {
			fmt.Fprintf(stderr, "vmbundle: %s: %v\n", l.name, err)
			return 1
		}
		b.dll, b.dllName = d, d.FileName
		layouts = append(layouts, b)
	}
	var entries []wincheck.Entry
	list := arm64Entries
	if signin {
		list = signinEntries
	}
	for _, ae := range list {
		for i, b := range layouts {
			if i > 0 && !ae.all {
				break
			}
			bin, err := ae.make(b.dll)
			if err != nil {
				fmt.Fprintf(stderr, "vmbundle: %s.%s: %v\n", ae.build, arm64Layouts[i].name, err)
				return 1
			}
			p := filepath.Join(out, "layouts", arm64Layouts[i].name, ae.build, b.dllName)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				fmt.Fprintln(stderr, "vmbundle:", err)
				return 1
			}
			if err := os.WriteFile(p, bin, 0o644); err != nil {
				fmt.Fprintln(stderr, "vmbundle:", err)
				return 1
			}
			e := wincheck.Entry{Name: ae.build + "." + arm64Layouts[i].name, DLL: map[string]string{}, Expect: b.expect,
				Rebase: ae.rebase, Note: ae.note}
			for c, f := range b.files {
				e.DLL[c] = f
			}
			rel, _ := filepath.Rel(out, p)
			e.DLL["arm64"] = filepath.ToSlash(rel)
			entries = append(entries, e)
		}
	}
	if err := os.MkdirAll(filepath.Join(out, "control"), 0o755); err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(out, "control", "us.expect.txt"), []byte(wincheck.ControlExpectations), 0o644); err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 1
	}
	var m bytes.Buffer
	wincheck.WriteManifest(&m, entries)
	if err := os.WriteFile(filepath.Join(out, "manifest.txt"), m.Bytes(), 0o644); err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(out, "README.txt"), []byte(readmeFor(entries, signin)), 0o644); err != nil {
		fmt.Fprintln(stderr, "vmbundle:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %d entries (ARM64 bundle)\n", out, len(entries))
	return 0
}

func readmeARM64(entries []wincheck.Entry) string {
	var b strings.Builder
	b.WriteString(`yakbd Windows on ARM test bundle (ARM64X candidates)
=====================================================

ONLY inside a disposable Windows 11 ARM64 virtual machine, never on a working PC: the runners write into the
system directories and the registry. Take a snapshot (overlay) first. Never add a test layout to a user's input
list or to the welcome screen: a public report describes a machine that did not boot after an ARM64X layout
(reference spec installation §2.4); the runners only load, type and unload.

What is tested (tool repository docs/arm64x.md): for each layout one file for System32 per candidate, named in
the entry: <candidate>.<layout>.

  arm64.*              control: a plain ARM64 file. Native ARM64 should PASS; the x64 runner should FAIL
                       with "LoadKeyboardLayoutW: NULL" (error 193): that is the expected result, not a defect.
  arm64x-ec.*          ARM64X in the shape of Windows' own files: x64 export thunk -> ARM64EC code.
  arm64x-x64.*         ARM64X whose x64 view exports a plain x64 function.
  arm64x-x64-onecfg.*  as arm64x-x64, one load configuration for both views.
  arm64x-ec-rebased.*  arm64x-ec loaded after its preferred image base was taken (relocations).

Each runner starts with a known-good control, control.<process> (control.arm64, control.x64-on-arm64,
control.wow64): it copies the guest's own US layout file (kbdus.dll of System32; of SysWOW64 for the 32-bit
runner) under the runner's test registration, loads it the same way and types six keys (control/us.expect.txt).
It reports PASS or FAIL with line counts only; nothing of Windows' file is written into a result. If
control.<process> FAILs, that runner's other lines say nothing about the candidates.

A candidate works when it PASSes in both the native ARM64 run and the x64 run. The 32-bit run tests the WOW64
file of each layout once (the arm64x-ec.* entries).

1. Copy this folder into the guest as C:\yk.
2. The runners need the interactive desktop of the signed-in user (LoadKeyboardLayoutW returns NULL without one;
   they refuse to start there). Either run them at the console from an elevated Command Prompt:

       cd /d C:\yk
       yakbd-wincheck-arm64.exe -bundle C:\yk -control -timeout 10m -out C:\yk\results-a64.txt
       yakbd-wincheck-64.exe    -bundle C:\yk -control -timeout 10m -out C:\yk\results-64.txt
       yakbd-wincheck-32.exe    -bundle C:\yk -control -timeout 10m -only arm64x-ec. -out C:\yk\results-32.txt

   or, from a remote session, as scheduled tasks with an interactive logon and highest privileges (<user> is the
   account signed in at the console), each started after the previous results file has its count line:

       schtasks /create /tn ykA /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\yk\yakbd-wincheck-arm64.exe -bundle C:\yk -control -timeout 10m -out C:\yk\results-a64.txt > C:\yk\console-a64.txt 2>&1"
       schtasks /run /tn ykA
       schtasks /create /tn ykX /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\yk\yakbd-wincheck-64.exe -bundle C:\yk -control -timeout 10m -out C:\yk\results-64.txt > C:\yk\console-64.txt 2>&1"
       schtasks /run /tn ykX
       schtasks /create /tn yk32 /sc once /st 00:00 /ru <user> /it /rl highest /f ^
         /tr "cmd /c C:\yk\yakbd-wincheck-32.exe -bundle C:\yk -control -timeout 10m -only arm64x-ec. -out C:\yk\results-32.txt > C:\yk\console-32.txt 2>&1"
       schtasks /run /tn yk32

3. The first line of each results file says which process it is: "a64-bit ... testing the arm64 DLLs" (native
   ARM64), "64-bit ... testing the x64-on-arm64 DLLs" (the x64 process under emulation; if it says "testing the
   x64 DLLs" the runner did not detect ARM64 and that run is not valid), "32-bit ... testing the wow64 DLLs".
   The guest is emulated and slow: allow one to two minutes per entry and runner (-timeout 10m per entry). A
   failed load prints "LoadKeyboardLayoutW: NULL (GetLastError <number>: <text>)".
4. Let every runner finish, even after a crash: each entry runs in a child process of its own, and a crashed
   candidate's file stays locked under its own name without affecting the later entries (installation
   tests/vm/arm64 README, "Traps"). Then copy results-a64.txt, results-64.txt, results-32.txt and the
   console-*.txt files out of the guest.
5. Only if a runner itself stops or the guest hangs: copy out what exists, restore the snapshot, and rerun the
   missing entries with -only <text> (entries whose names do not contain the text are skipped) and a NEW -out
   file name, so no earlier result is overwritten.
6. Restore the snapshot once at the end.

What the runner does per entry: copies the file into System32 (the 32-bit runner: SysWOW64) under a test name
(yka64NNN.dll, yk64NNN.dll, yk32NNN.dll), writes Layout File, Layout Text and Layout Id under a test KLID of its own
(a3NN0409, a1NN0409, a2NN0409), loads it with LoadKeyboardLayoutW in a child process, checks the HKL, types every
expectation through ToUnicodeEx, unloads it and removes file and key. The three runners never share a file, a KLID
or a Layout Id, so no run finds another run's layout already loaded in the session. Not covered: ctfmon.exe and
the layout after sign-in (reference spec dll-format §11 item 9 asks for both; they stay unverified).

`)
	fmt.Fprintf(&b, "Entries in this bundle: %d.\n", len(entries))
	for _, e := range entries {
		fmt.Fprintf(&b, "  %s\n", e.Name)
	}
	return b.String()
}

// signinEntries are the layouts of the sign-in bundle: the candidate that ships (docs/arm64x.md §5) for every
// layout, and Windows' shape for one layout as a fallback.
var signinEntries = []arm64Entry{
	{build: "arm64x-x64", all: true, make: arm64x(windows.VariantX64),
		note: "the shipped ARM64X shape, installed for real and checked after sign-in"},
	{build: "arm64x-ec", make: arm64x(windows.VariantEC),
		note: "ARM64X in Windows' shape, checked after sign-in (fallback)"},
}

func readmeFor(entries []wincheck.Entry, signin bool) string {
	if signin {
		return readmeSignin(entries)
	}
	return readmeARM64(entries)
}

func readmeSignin(entries []wincheck.Entry) string {
	var b strings.Builder
	b.WriteString(`yakbd sign-in test bundle 3: which installed layouts does Windows load at sign-in?
==================================================================================

Run 2 (ta-arm64-2) found, after sign-in on ARM64, only the first of seven test layouts in the native ARM64
process's session (GetKeyboardLayoutList); the native runner then loaded the others itself, and the x64 and x86
runners that ran after it found all of them. Question: is that Windows (it loads only some layouts of the user's
list at sign-in, the rest on demand) or our files? The reference spec does not settle it: installation §4.2 says
the Preload entries are loaded at sign-in (a public source, not a measurement), and its own ARM64 run (tests/vm/arm64,
"After sign-in") had TWO custom layouts in the list, both in the session after sign-in. Seven custom layouts of one
language were never tested.

This bundle runs on BOTH guests: the x64 Hyper-V VM (fast; the files there are the x64 builds) and the ARM64 VM.
The same seven entries as run 2: arm64x-x64.<layout> (6 layouts) and arm64x-ec.klc-kbdtst; on x64 Windows each entry
installs its x64 build into System32 and its WOW64 build into SysWOW64.

New: "-signin list" loads nothing. Per entry it prints whether the layout is in this process's session and whether
it is in the user's list (User Profile, Substitutes, Preload slot), and with -repeat it does that again later, so
"never loaded" can be told from "loaded later".

ONLY in a disposable VM, on a fresh overlay that signs in by itself. The layouts go into the signed-in user's input
list (never the welcome screen); restore the snapshot afterwards. Run everything elevated in the console session of
the signed-in user (scheduled tasks with /it /rl highest from a remote session, as in run 2), one command after the
other, and do not touch the guest meanwhile. -out names below are all different: copy all of them out.

Restarting: the guest turns automatic sign-in off after each use. Before every power-off, switch automatic sign-in
back on the way the guest's setup does it (AutoAdminLogon = 1 under
HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon, with the user name and password values it already has).
On ARM64 a restart from inside the guest hangs in the firmware: power off (shutdown /s /t 0) and cold-boot the VM
from the host instead. On x64 a normal restart is fine.

A. x64 Hyper-V VM
-----------------
1. Copy this folder into the guest as C:\yk.
2. Install:        C:\yk\yakbd-wincheck-64.exe -bundle C:\yk -signin install > C:\yk\x-install.txt 2>&1
3. Switch automatic sign-in back on, restart, let it sign in; wait until the desktop has been up for two minutes.
4. List first, before anything loads a layout (the 32-bit runner four times, a minute apart, then the 64-bit one):
       C:\yk\yakbd-wincheck-32.exe -bundle C:\yk -signin list -repeat 4 -interval 60s -out C:\yk\x-list-32.txt
       C:\yk\yakbd-wincheck-64.exe -bundle C:\yk -signin list -out C:\yk\x-list-64.txt
5. Check (types; loads what is missing), 32-bit first:
       C:\yk\yakbd-wincheck-32.exe -bundle C:\yk -signin check -out C:\yk\x-check-32.txt
       C:\yk\yakbd-wincheck-64.exe -bundle C:\yk -signin check -out C:\yk\x-check-64.txt
6. Copy C:\yk\x-*.txt out. Restore the snapshot.

B. ARM64 VM (the order is the reverse of run 2: the native ARM64 runner comes last)
------------------------------------------------------------------------------------
1. Copy this folder into the guest as C:\yk.
2. Install:        C:\yk\yakbd-wincheck-arm64.exe -bundle C:\yk -signin install > C:\yk\a-install.txt 2>&1
3. Switch automatic sign-in back on, power off, cold-boot, let it sign in; wait until the desktop has been up for two
   minutes.
4. List, loading nothing: the 32-bit runner four times a minute apart, then the x64 and the native ARM64 runner once:
       C:\yk\yakbd-wincheck-32.exe    -bundle C:\yk -signin list -repeat 4 -interval 60s -out C:\yk\a-list-32.txt
       C:\yk\yakbd-wincheck-64.exe    -bundle C:\yk -signin list -out C:\yk\a-list-64.txt
       C:\yk\yakbd-wincheck-arm64.exe -bundle C:\yk -signin list -out C:\yk\a-list-a64.txt
5. Check, ARM64 last:
       C:\yk\yakbd-wincheck-32.exe    -bundle C:\yk -signin check -out C:\yk\a-check-32.txt
       C:\yk\yakbd-wincheck-64.exe    -bundle C:\yk -signin check -out C:\yk\a-check-64.txt
       C:\yk\yakbd-wincheck-arm64.exe -bundle C:\yk -signin check -out C:\yk\a-check-a64.txt
6. Copy C:\yk\a-*.txt out. Restore the snapshot.

How to read it
--------------
- x-list / a-list "pass 1": how many of the 7 are in the session right after sign-in, per process kind, with nothing
  loaded by us. The "user list" columns must say yes for all 7 (else the install, not the sign-in, is the cause).
- Only the first (or a few) in the session on x64 as well: Windows behaviour, not our ARM64X file.
- The count grows between the passes of x-list-32 / a-list-32: Windows loads the rest later, on its own.
- All 7 on x64 but not on ARM64 (in a-list, before any check): something ARM64-specific (our ARM64X file or ARM64
  Windows); the a-list lines of the three process kinds then tell which.
- The check lines: "in the session after sign-in" or "not in the session ... (loaded by the runner instead)", as in
  run 2; a check line can only pass for a layout the session already had.

`)
	fmt.Fprintf(&b, "Entries in this bundle (manifest order = install order = KLID a4NN0409, NN = 00 ...): %d.\n", len(entries))
	for _, e := range entries {
		fmt.Fprintf(&b, "  %s\n", e.Name)
	}
	return b.String()
}
