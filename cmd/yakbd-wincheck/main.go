// Command yakbd-wincheck is the unattended Windows test of yakbd's layout DLLs. It runs inside a disposable
// Windows virtual machine, never on a working PC: for every layout of a bundle made by tools/vmbundle it copies the
// DLL into the system directory under a test name, registers it under a test KLID, loads it with
// LoadKeyboardLayoutW, types every line of the layout's expectations through ToUnicodeEx, unloads it and removes
// the file and the registry key again. Each layout is loaded and typed in a child process of its own, so that a
// crash or a hang costs that layout only. The runner prints one line per layout and a count, and writes the
// results file as it goes. The 64-bit build tests the x64 DLLs; the 32-bit build the WOW64 DLLs on 64-bit Windows
// (the x86 DLLs on 32-bit Windows). See tools/vmbundle for the guest procedure.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/pe"
)

// system is what the runner needs from Windows. sys_windows.go implements it with the Windows API.
type system interface {
	// Build returns the build this process loads: "x64", "wow64" or "x86".
	Build() string
	Install(s wincheck.Slot, dll []byte, text string) error
	Uninstall(s wincheck.Slot) error
	// Load loads a registered KLID and returns the HKL exactly as Windows returned it.
	Load(klid string) (uintptr, error)
	Unload(hkl uintptr)
	Prober(hkl uintptr) wincheck.Prober
	// Occupy reserves [base, base+size) of the process's address space, so that a DLL with that preferred base
	// must be rebased; release frees it.
	Occupy(base uint64, size uint32) (release func(), err error)
	// Interactive fails when the process has no interactive desktop: LoadKeyboardLayoutW returns NULL there
	// (VM run 1: PowerShell Direct).
	Interactive() error
	// SystemFile reads a file of the system directory as this process sees it (System32, or SysWOW64 for a
	// 32-bit process on 64-bit Windows). Used only to copy Windows' own layout for the control; its bytes never
	// go into a result.
	SystemFile(name string) ([]byte, error)
	// SysWOW64Layouts returns Windows' own SysWOW64 layout files, read as data (empty on 32-bit Windows).
	SysWOW64Layouts() (map[string][]byte, error)
}

func main() {
	runtime.LockOSThread() // keyboard layouts are loaded per thread
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

type options struct {
	bundle, out, only, bits string
	child                   int
	timeout                 time.Duration
	inProcess               bool
	control                 bool
	signin                  string
	repeat                  int
	interval                time.Duration
}

func run(args []string, stdout, stderr io.Writer, sys system) int {
	if len(args) > 0 && args[0] == "-import" {
		return runImport(args[1:], stdout, stderr, sys, nil) // the DLL import check (importcheck.go)
	}
	fs := flag.NewFlagSet("yakbd-wincheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	o.bits = strconv.Itoa(strconv.IntSize)
	fs.StringVar(&o.bundle, "bundle", ".", "the bundle folder (with manifest.txt)")
	fs.StringVar(&o.out, "out", "", "results file, written as the run goes (default results-64.txt, results-32.txt or results-a64.txt)")
	fs.StringVar(&o.only, "only", "", "run only the layouts whose names contain this text")
	fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "time limit of one layout")
	fs.IntVar(&o.child, "child", -1, "internal: load and type layout N in this process and print its outcome")
	fs.BoolVar(&o.inProcess, "in-process", false, "load and type every layout in this process (no crash protection)")
	fs.StringVar(&o.signin, "signin", "", "the sign-in test: install (persistent registration and input list, 64-bit runner), check (after restart and sign-in, every runner), list (report session and user list, load nothing) or remove")
	fs.IntVar(&o.repeat, "repeat", 1, "-signin list: number of passes")
	fs.DurationVar(&o.interval, "interval", time.Minute, "-signin list: time between passes")
	fs.BoolVar(&o.control, "control", false, "first load Windows' own US layout file (kbdus.dll of the system directory) under a test registration and type the keys of control/us.expect.txt: a known-good control, reported as control.<process>")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if sys == nil {
		var err error
		if sys, err = newSystem(); err != nil {
			fmt.Fprintln(stderr, "yakbd-wincheck:", err)
			return 2
		}
	} else {
		o.inProcess = true // a test system lives in this process
	}
	mf, err := os.ReadFile(filepath.Join(o.bundle, "manifest.txt"))
	if err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	entries, err := wincheck.ReadManifest(bytes.NewReader(mf))
	if err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	if len(entries) > 256 {
		fmt.Fprintln(stderr, "yakbd-wincheck: at most 256 layouts per bundle")
		return 2
	}
	if err := sys.Interactive(); err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	build := sys.Build()
	if build == "arm64" {
		o.bits = "a64" // the native ARM64 runner: slots of its own (wincheck.SlotFor)
	}
	if o.out == "" {
		o.out = "results-" + o.bits + ".txt"
	}
	if o.signin != "" {
		if o.out == "results-"+o.bits+".txt" {
			o.out = "signin-" + o.bits + ".txt"
		}
		return signin(sys, o, build, entries, o.signin, stdout, stderr)
	}
	if o.control {
		// The known-good control goes first (slot 0), with the DLL read from the system directory (ControlDLL).
		entries = append([]wincheck.Entry{{Name: "control." + build, Expect: controlExpect,
			DLL: map[string]string{wincheck.Column(build): ControlDLL}, Note: "Windows' own US layout"}}, entries...)
		if len(entries) > 256 {
			fmt.Fprintln(stderr, "yakbd-wincheck: at most 255 layouts per bundle with -control")
			return 2
		}
	}
	if o.child >= 0 {
		if o.child >= len(entries) {
			return 2
		}
		out := loadAndCheck(sys, o, build, o.child, entries[o.child])
		json.NewEncoder(stdout).Encode(out)
		return 0
	}
	return parent(sys, o, build, entries, stdout, stderr)
}

func parent(sys system, o options, build string, entries []wincheck.Entry, stdout, stderr io.Writer) int {
	f, err := os.Create(o.out)
	if err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	defer f.Close()
	head := fmt.Sprintf("yakbd-wincheck %s-bit, testing the %s DLLs\n", o.bits, build)
	io.MultiWriter(stdout, f).Write([]byte(head))
	var outs []wincheck.Outcome
	for i, e := range entries {
		if o.only != "" && !strings.Contains(e.Name, o.only) && !isControl(e) {
			continue
		}
		out := one(sys, o, build, i, e)
		outs = append(outs, out)
		line := out.Summary() + "\n"
		io.MultiWriter(stdout, f).Write([]byte(line))
		f.Sync()
	}
	var full bytes.Buffer
	_, failed := wincheck.Report(io.Discard, &full, o.bits, outs)
	passed := len(outs) - failed
	count := fmt.Sprintf("%s-bit runner: %d passed, %d failed\n", o.bits, passed, failed)
	io.MultiWriter(stdout, f).Write([]byte(count))
	if build == "x64" {
		vm1(sys, io.MultiWriter(stdout, f), &full)
	}
	f.Write([]byte("\n" + full.String()))
	if failed > 0 {
		return 1
	}
	return 0
}

// one installs a layout, has it loaded and typed (in a child process unless in-process), and removes it again.
func one(sys system, o options, build string, i int, e wincheck.Entry) wincheck.Outcome {
	out := wincheck.Outcome{Name: e.Name}
	rel := e.DLL[wincheck.Column(build)]
	if rel == "" {
		out.Err = "no " + wincheck.Column(build) + " DLL in the bundle"
		return out
	}
	var dll []byte
	var err error
	if rel == ControlDLL {
		dll, err = sys.SystemFile(ControlDLL)
	} else {
		dll, err = os.ReadFile(filepath.Join(o.bundle, rel))
	}
	if err != nil {
		out.Err = err.Error()
		return out
	}
	slot := wincheck.SlotFor(o.bits, i)
	if err := sys.Install(slot, dll, "yakbd test "+e.Name); err != nil {
		out.Err = "install: " + err.Error()
		sys.Uninstall(slot)
		return out
	}
	if o.inProcess {
		out = loadAndCheck(sys, o, build, i, e)
	} else {
		out = child(o, i, e)
	}
	if isControl(e) {
		out = redact(out)
	}
	var cerr error
	for try := 0; try < 5; try++ {
		if cerr = sys.Uninstall(slot); cerr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if cerr != nil {
		out.Notes = append(out.Notes, "cleanup: "+cerr.Error())
	}
	return out
}

// child runs this program with -child i and reads the outcome it prints.
func child(o options, i int, e wincheck.Entry) wincheck.Outcome {
	out := wincheck.Outcome{Name: e.Name}
	self, err := os.Executable()
	if err != nil {
		out.Err = err.Error()
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	args := []string{"-bundle", o.bundle, "-child", strconv.Itoa(i)}
	if o.control {
		args = append(args, "-control")
	}
	cmd := exec.CommandContext(ctx, self, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		out.Err = fmt.Sprintf("timed out after %v", o.timeout)
		return out
	}
	if dec := json.NewDecoder(&stdout); dec.Decode(&out) == nil && err == nil {
		return out
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		out.Err = fmt.Sprintf("the test process crashed (exit code %#x) %s", uint32(ee.ExitCode()), strings.TrimSpace(firstLine(stderr.String())))
	} else {
		out.Err = fmt.Sprintf("the test process failed: %v", err)
	}
	return out
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

// loadAndCheck loads an installed layout in this process and types its expectations.
func loadAndCheck(sys system, o options, build string, i int, e wincheck.Entry) (out wincheck.Outcome) {
	out.Name = e.Name
	exp, err := os.ReadFile(filepath.Join(o.bundle, e.Expect))
	if err != nil {
		out.Err = err.Error()
		return out
	}
	lines, err := wincheck.ParseExpectations(bytes.NewReader(exp))
	if err != nil {
		out.Err = "expectations: " + err.Error()
		return out
	}
	out.Lines = len(lines)
	slot := wincheck.SlotFor(o.bits, i)
	if e.Rebase {
		dll, _ := os.ReadFile(filepath.Join(o.bundle, e.DLL[wincheck.Column(build)]))
		if f, err := pe.Parse(dll); err == nil {
			release, err := sys.Occupy(f.ImageBase, f.SizeOfImage)
			if err != nil {
				out.Notes = append(out.Notes, "image base already taken, so the DLL is rebased anyway ("+err.Error()+")")
			} else {
				defer release()
			}
		}
	}
	hkl, err := sys.Load(slot.KLID)
	if err != nil {
		out.Err = "LoadKeyboardLayoutW: " + err.Error()
		return out
	}
	defer sys.Unload(hkl)
	if want := uint32(0xF000|slot.LayoutID) << 16; uint32(hkl)&0xFFFF0000 != want {
		out.Err = fmt.Sprintf("LoadKeyboardLayoutW returned HKL %08x, not that of the test Layout Id (%08x): another layout answered",
			uint32(hkl), want|0x0409)
		return out
	}
	out.Diffs = wincheck.Check(lines, sys.Prober(hkl))
	return out
}

// vm1 reports, for test VM-1, which of Windows' own SysWOW64 layout files relocate the descriptor address.
func vm1(sys system, short, full io.Writer) {
	files, err := sys.SysWOW64Layouts()
	if err != nil || len(files) == 0 {
		fmt.Fprintf(short, "VM-1 Windows files: not read (%v)\n", err)
		return
	}
	with, without := wincheck.DescriptorRelocs(files)
	sort.Strings(with)
	sort.Strings(without)
	fmt.Fprintf(short, "VM-1 Windows files: %d of %d SysWOW64 layout files with mov eax,imm32 relocate the address\n",
		len(with), len(with)+len(without))
	fmt.Fprintf(full, "== VM-1: SysWOW64 files with the relocation (%d): %s\n", len(with), strings.Join(with, " "))
	fmt.Fprintf(full, "== VM-1: SysWOW64 files without it (%d): %s\n", len(without), strings.Join(without, " "))
}
