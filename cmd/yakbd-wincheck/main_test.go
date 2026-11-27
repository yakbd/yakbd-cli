package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yakbd/yakbd-cli/internal/cli"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// fake is a system that keeps "installed" files in memory and translates with the emulator.
type fake struct {
	build                  string
	files                  map[string][]byte // file -> DLL
	klids                  map[string]wincheck.Slot
	removed                int
	broken                 bool // ignore dead keys
	noDesktop              bool
	installed              []string // slot file names, in order
	system                 []byte   // the file SystemFile returns
	notInSession, noCtfmon bool
	enabled                map[string]bool
	firstOnly              bool
	machines               []uint16 // Machine field of each installed file
}

func (f *fake) Build() string { return f.build }
func (f *fake) Install(s wincheck.Slot, dll []byte, text string) error {
	f.files[s.File], f.klids[s.KLID] = dll, s
	f.installed = append(f.installed, s.File)
	f.machines = append(f.machines, uint16(dll[0x44])|uint16(dll[0x45])<<8)
	return nil
}
func (f *fake) Uninstall(s wincheck.Slot) error {
	delete(f.files, s.File)
	delete(f.klids, s.KLID)
	f.removed++
	return nil
}
func (f *fake) Load(klid string) (uintptr, error) {
	s, ok := f.klids[klid]
	if !ok {
		return 0, fmt.Errorf("not registered")
	}
	return uintptr(0xF000|s.LayoutID)<<16 | 0x0409, nil
}
func (f *fake) Unload(uintptr) {}
func (f *fake) Prober(hkl uintptr) wincheck.Prober {
	if hkl == usHKL {
		d, _, err := windows.Read(f.system)
		if err != nil {
			panic(err)
		}
		return &wincheck.Emulator{T: &d.Tables}
	}
	for _, s := range f.klids {
		if uintptr(0xF000|s.LayoutID)<<16|0x0409 == hkl {
			d, _, err := windows.Read(f.files[s.File])
			if err != nil {
				panic(err)
			}
			if f.broken {
				d.Tables.DeadKeys = nil
			}
			return &wincheck.Emulator{T: &d.Tables}
		}
	}
	panic("no layout")
}
func (f *fake) Occupy(uint64, uint32) (func(), error) { return func() {}, nil }
func (f *fake) SystemFile(name string) ([]byte, error) {
	if f.system == nil {
		return nil, fmt.Errorf("%s: not found", name)
	}
	return f.system, nil
}
func (f *fake) SysWOW64Layouts() (map[string][]byte, error) { return nil, nil }
func (f *fake) Interactive() error {
	if f.noDesktop {
		return fmt.Errorf("no interactive desktop")
	}
	return nil
}

// bundle makes a small bundle with the compile command line.
func bundle(t *testing.T) string {
	dir := t.TempDir()
	var entries []wincheck.Entry
	for _, src := range []string{"../../testdata/reference-spec/vm/kbdtst.klc", "../../testdata/kbl-spec/examples/latin-altgr-deadkeys.kbl.json"} {
		name := strings.Split(filepath.Base(src), ".")[0]
		out := filepath.Join(dir, name)
		var o, e bytes.Buffer
		if code := cli.Run([]string{"compile", "-q", "-o", out, "-emit-expectations", "-langid", "0409", "-target", "x64,wow64,x86,arm64x", src}, &o, &e); code != 0 {
			t.Fatal(e.String())
		}
		exp, _ := filepath.Glob(filepath.Join(out, "*.expect.txt"))
		dll := map[string]string{}
		for _, b := range wincheck.Builds {
			folder := b
			if b == "arm64" {
				folder = "arm64x"
			}
			m, _ := filepath.Glob(filepath.Join(out, folder, "*.dll"))
			rel, _ := filepath.Rel(dir, m[0])
			dll[b] = rel
		}
		rel, _ := filepath.Rel(dir, exp[0])
		entries = append(entries, wincheck.Entry{Name: name, DLL: dll, Expect: rel})
	}
	var m bytes.Buffer
	wincheck.WriteManifest(&m, entries)
	os.WriteFile(filepath.Join(dir, "manifest.txt"), m.Bytes(), 0o644)
	return dir
}

func TestRunWithEmulator(t *testing.T) {
	dir := bundle(t)
	for _, build := range []string{"x64", "wow64", "x86", "arm64", "x64-on-arm64"} {
		f := &fake{build: build, files: map[string][]byte{}, klids: map[string]wincheck.Slot{}}
		var o, e bytes.Buffer
		res := filepath.Join(dir, "results-"+build+".txt")
		if code := run([]string{"-bundle", dir, "-out", res}, &o, &e, f); code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", build, code, o.String(), e.String())
		}
		if !strings.Contains(o.String(), "PASS kbdtst") || !strings.Contains(o.String(), "2 passed, 0 failed") {
			t.Errorf("%s: %s", build, o.String())
		}
		wantFile, wantMachine := "yk64", uint16(0x8664)
		switch build {
		case "wow64", "x86":
			wantFile, wantMachine = "yk64", 0x014C // the fake runs as a 64-bit test process
		case "arm64":
			wantFile, wantMachine = "yka64", 0xAA64
		case "x64-on-arm64":
			wantMachine = 0xAA64
		}
		if !strings.HasPrefix(f.installed[0], wantFile) || f.machines[0] != wantMachine {
			t.Errorf("%s: installed %v machines %x", build, f.installed, f.machines)
		}
		if len(f.files) != 0 || len(f.klids) != 0 || f.removed != 2 {
			t.Errorf("%s: not cleaned up: %d files, %d keys", build, len(f.files), len(f.klids))
		}
	}
	f := &fake{build: "x64", files: map[string][]byte{}, klids: map[string]wincheck.Slot{}, broken: true}
	var o, e bytes.Buffer
	res := filepath.Join(dir, "broken.txt")
	if code := run([]string{"-bundle", dir, "-out", res}, &o, &e, f); code != 1 || !strings.Contains(o.String(), "FAIL kbdtst: line") {
		t.Errorf("broken tables not caught: %d %s", code, o.String())
	}
	full, _ := os.ReadFile(res)
	if !strings.Contains(string(full), "== kbdtst (64-bit runner)") {
		t.Errorf("full diffs missing: %s", full)
	}
}

// TestOutcomeJSON: a child's outcome survives the JSON trip to the parent.
func TestOutcomeJSON(t *testing.T) {
	d := &wincheck.Press{VK: 0xC0}
	in := wincheck.Outcome{Name: "x", Lines: 3, Notes: []string{"n"}, Diffs: []wincheck.Diff{{Line: wincheck.Line{No: 2, Dead: d,
		Key: wincheck.Press{VK: 0x41, Mods: []byte{0x10}}, Kind: "chars", Units: []uint16{0xE0}}, Got: "none"}}}
	b, _ := json.Marshal(in)
	var out wincheck.Outcome
	if err := json.Unmarshal(b, &out); err != nil || out.Summary() != in.Summary() {
		t.Errorf("%v\n%s\n%s", err, in.Summary(), out.Summary())
	}
}

func TestNoDesktop(t *testing.T) {
	dir := bundle(t)
	f := &fake{build: "x64", files: map[string][]byte{}, klids: map[string]wincheck.Slot{}, noDesktop: true}
	var o, e bytes.Buffer
	if code := run([]string{"-bundle", dir, "-out", filepath.Join(dir, "r.txt")}, &o, &e, f); code != 2 ||
		!strings.Contains(e.String(), "no interactive desktop") || strings.Contains(o.String(), "FAIL") {
		t.Errorf("exit %d: %s %s", code, o.String(), e.String())
	}
}

// TestControl: -control first loads the system directory's US layout under a test slot, names the line
// control.<process>, is not filtered by -only, and reports counts only, never typed characters.
func TestControl(t *testing.T) {
	dir := bundle(t)
	os.MkdirAll(filepath.Join(dir, "control"), 0o755)
	os.WriteFile(filepath.Join(dir, controlExpect), []byte(wincheck.ControlExpectations), 0o644)
	us, _ := os.ReadFile(filepath.Join(dir, "kbdtst", "x64", "kbdtst.dll")) // stands in for Windows' file
	for _, build := range []string{"arm64", "x64-on-arm64", "wow64"} {
		f := &fake{build: build, files: map[string][]byte{}, klids: map[string]wincheck.Slot{}, system: us}
		var o, e bytes.Buffer
		if code := run([]string{"-bundle", dir, "-control", "-only", "nothing", "-out", filepath.Join(dir, "c.txt")}, &o, &e, f); code != 0 ||
			!strings.Contains(o.String(), "PASS control."+build+" (6 lines)") || !strings.Contains(o.String(), "1 passed, 0 failed") {
			t.Errorf("%s: %d %s%s", build, code, o.String(), e.String())
		}
	}
	f := &fake{build: "arm64", files: map[string][]byte{}, klids: map[string]wincheck.Slot{}, system: us, broken: true}
	os.WriteFile(filepath.Join(dir, controlExpect), []byte(wincheck.ControlExpectations+"key 41 -> chars 0062\n"), 0o644)
	var o, e bytes.Buffer
	run([]string{"-bundle", dir, "-control", "-only", "nothing", "-out", filepath.Join(dir, "c.txt")}, &o, &e, f)
	full, _ := os.ReadFile(filepath.Join(dir, "c.txt"))
	if !strings.Contains(o.String(), "FAIL control.arm64: 1 of 7 lines differ") || strings.Contains(string(full), "0061") {
		t.Errorf("control failure must give counts only: %s\n%s", o.String(), full)
	}
	f = &fake{build: "arm64", files: map[string][]byte{}, klids: map[string]wincheck.Slot{}}
	o.Reset()
	run([]string{"-bundle", dir, "-control", "-only", "nothing", "-out", filepath.Join(dir, "c.txt")}, &o, &e, f)
	if !strings.Contains(o.String(), "FAIL control.arm64: kbdus.dll: not found") {
		t.Errorf("missing system file: %s", o.String())
	}
}

// The sign-in side of the fake: a persistent install is "in the session" afterwards.
func (f *fake) InstallPersistent(s wincheck.Slot, sys32, wow64 []byte, text string) error {
	if len(wow64) == 0 {
		return fmt.Errorf("no wow64 file")
	}
	file := sys32
	if f.build == "wow64" {
		file = wow64
	}
	return f.Install(s, file, text)
}
func (f *fake) RemovePersistent(s wincheck.Slot) error { return f.Uninstall(s) }
func (f *fake) SessionLayouts() []uintptr {
	out := []uintptr{usHKL}
	for _, s := range f.klids {
		if !f.notInSession {
			out = append(out, uintptr(0xF000|s.LayoutID)<<16|0x0409)
		}
	}
	return out
}
func (f *fake) UserList(klid string) (bool, bool, string) {
	if f.enabled[klid] {
		return true, true, "2"
	}
	return false, false, ""
}

// Enable mimics sign-in run 2 with firstOnly: of several layouts in one call only the first arrives.
func (f *fake) Enable(slots []wincheck.Slot) error {
	if f.enabled == nil {
		f.enabled = map[string]bool{}
	}
	for i, s := range slots {
		if i == 0 || !f.firstOnly {
			f.enabled[s.KLID] = true
		}
	}
	return nil
}
func (f *fake) Ctfmon() (bool, string) { return !f.noCtfmon, "fake" }
func (f *fake) Typer(hkl uintptr) (wincheck.Typer, error) {
	return wincheck.EmulatorTyper{E: f.Prober(hkl).(*wincheck.Emulator)}, nil
}

func TestSignin(t *testing.T) {
	dir := bundle(t)
	enableTimeout = 10 * time.Millisecond
	us, _ := os.ReadFile(filepath.Join(dir, "kbdtst", "x64", "kbdtst.dll"))
	f := &fake{build: "arm64", files: map[string][]byte{}, klids: map[string]wincheck.Slot{}, system: us, firstOnly: true}
	var o, e bytes.Buffer
	if code := run([]string{"-bundle", dir, "-signin", "install"}, &o, &e, f); code != 0 || len(f.klids) != 2 ||
		!strings.HasPrefix(f.installed[0], "yks") {
		t.Fatalf("install: %d %s%s %v", code, o.String(), e.String(), f.installed)
	}
	if !strings.Contains(o.String(), "enabled latin-altgr-deadkeys: KLID a4010409 in the user's list (missing after the call for all; enabled again on its own)") {
		t.Errorf("enable retry not reported: %s", o.String())
	}
	o.Reset()
	res := filepath.Join(dir, "s.txt")
	if code := run([]string{"-bundle", dir, "-signin", "check", "-out", res}, &o, &e, f); code != 0 ||
		!strings.Contains(o.String(), "ctfmon.exe in this session: running") || !strings.Contains(o.String(), "PASS control.arm64") ||
		!strings.Contains(o.String(), "in the session after sign-in") || !strings.Contains(o.String(), "typed as real input") ||
		!strings.Contains(o.String(), "3 passed, 0 failed") {
		t.Errorf("check: %d %s%s", code, o.String(), e.String())
	}
	f.notInSession, f.noCtfmon = true, true
	o.Reset()
	if code := run([]string{"-bundle", dir, "-signin", "check", "-out", res}, &o, &e, f); code != 1 ||
		!strings.Contains(o.String(), "NOT running") || !strings.Contains(o.String(), "not in the session after sign-in") {
		t.Errorf("check without sign-in: %d %s", code, o.String())
	}
	o.Reset()
	if code := run([]string{"-bundle", dir, "-signin", "list", "-repeat", "2", "-interval", "1ms", "-out", res}, &o, &e, f); code != 0 ||
		!strings.Contains(o.String(), "pass 2 at") || !strings.Contains(o.String(), "session no; user list: User Profile yes, Substitutes yes, Preload 2") {
		t.Errorf("list: %d %s", code, o.String())
	}
	o.Reset()
	if code := run([]string{"-bundle", dir, "-signin", "remove"}, &o, &e, f); code != 0 || len(f.klids) != 0 {
		t.Errorf("remove: %d %s", code, o.String())
	}
	x := &fake{build: "x64", files: map[string][]byte{}, klids: map[string]wincheck.Slot{}}
	if code := run([]string{"-bundle", dir, "-signin", "install"}, &o, &e, x); code != 0 || x.machines[0] != 0x8664 {
		t.Errorf("install on x64 Windows must take the x64 file: %d %x", code, x.machines)
	}
	w := &fake{build: "wow64", files: map[string][]byte{}, klids: map[string]wincheck.Slot{}}
	if code := run([]string{"-bundle", dir, "-signin", "install"}, &o, &e, w); code != 2 {
		t.Error("install from the 32-bit runner must be refused")
	}
}
