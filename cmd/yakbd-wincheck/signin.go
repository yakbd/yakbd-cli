package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

// The sign-in test (-signin install|check|remove), reference spec dll-format §11 item 9 and kbl D44: the layouts
// are installed as they would be for real (the System32 file and the SysWOW64 file under one name, one KLID, the
// layout in the signed-in user's input list), the guest restarts and signs in by itself, and then each runner checks
// that ctfmon.exe runs in its session, that the layout is in the session without loading it, and types the
// expectations through ToUnicodeEx and through real input into a window of its own.

// signinSystem is what the sign-in test needs beyond system.
type signinSystem interface {
	// InstallPersistent copies the System32 and SysWOW64 files under s.File and writes the KLID.
	InstallPersistent(s wincheck.Slot, sys32, wow64 []byte, text string) error
	// Enable adds "0409:<KLID>" of every slot to the signed-in user's input list in one InstallLayoutOrTip call
	// (installation §4.3).
	Enable(slots []wincheck.Slot) error
	// RemovePersistent undoes InstallPersistent (files still locked are removed at the next start).
	RemovePersistent(s wincheck.Slot) error
	// SessionLayouts returns GetKeyboardLayoutList of this process.
	SessionLayouts() []uintptr
	// Ctfmon says whether ctfmon.exe runs in this process's session.
	Ctfmon() (bool, string)
	// Typer activates hkl in a window of this process and returns a typer into that window.
	Typer(hkl uintptr) (wincheck.Typer, error)
	// UserList says whether the signed-in user's input list holds the KLID: in User Profile (the Win8+ list,
	// installation §4.1) and as a Substitutes value (§4.2), with the Preload slot that maps to it ("" if none).
	UserList(klid string) (profile, substitute bool, preload string)
}

const (
	inputKeys = 40 // single keys per layout typed through real input
	inputSeqs = 20 // dead-key sequences per layout
	usHKL     = 0x04090409
)

func signin(sys system, o options, build string, entries []wincheck.Entry, mode string, stdout, stderr io.Writer) int {
	ss, ok := sys.(signinSystem)
	if !ok {
		fmt.Fprintln(stderr, "yakbd-wincheck: this system cannot run the sign-in test")
		return 2
	}
	switch mode {
	case "install", "remove":
		if build == "wow64" {
			fmt.Fprintln(stderr, "yakbd-wincheck: run -signin install and remove with the 64-bit runner")
			return 2
		}
		failed := 0
		var installed []wincheck.Slot
		var names []string
		for i, e := range entries {
			slot := wincheck.SlotFor("s", i)
			var err error
			if mode == "remove" {
				err = ss.RemovePersistent(slot)
			} else {
				err = installEntry(ss, o, build, slot, e)
			}
			if err != nil {
				failed++
				fmt.Fprintf(stdout, "FAIL %s %s: %v\n", mode, e.Name, err)
				continue
			}
			installed, names = append(installed, slot), append(names, e.Name)
			fmt.Fprintf(stdout, "%s %s: %s KLID %s Layout Id %04x\n", mode, e.Name, slot.File, slot.KLID, slot.LayoutID)
		}
		if mode == "install" && len(installed) > 0 {
			failed += enableAll(ss, installed, names, stdout)
		}
		if failed > 0 {
			return 1
		}
		if mode == "install" {
			fmt.Fprintln(stdout, "installed; restart the guest and let it sign in, then run -signin check in each runner")
		}
		return 0
	case "check":
		return signinCheck(ss, sys, o, build, entries, stdout, stderr)
	case "list":
		return signinList(ss, o, build, entries, stdout, stderr)
	}
	fmt.Fprintf(stderr, "yakbd-wincheck: -signin %q (want install, check, list or remove)\n", mode)
	return 2
}

func installEntry(ss signinSystem, o options, build string, slot wincheck.Slot, e wincheck.Entry) error {
	// The System32 file of this machine: the arm64 column on ARM64 Windows, the x64 column on x64 Windows.
	col := wincheck.Column(build)
	if e.DLL[col] == "" || e.DLL["wow64"] == "" {
		return fmt.Errorf("the entry needs a %s and a wow64 file", col)
	}
	a, err := os.ReadFile(filepath.Join(o.bundle, e.DLL[col]))
	if err != nil {
		return err
	}
	w, err := os.ReadFile(filepath.Join(o.bundle, e.DLL["wow64"]))
	if err != nil {
		return err
	}
	return ss.InstallPersistent(slot, a, w, "yakbd sign-in test "+e.Name)
}

func signinCheck(ss signinSystem, sys system, o options, build string, entries []wincheck.Entry, stdout, stderr io.Writer) int {
	f, err := os.Create(o.out)
	if err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	defer f.Close()
	w := io.MultiWriter(stdout, f)
	fmt.Fprintf(w, "yakbd-wincheck %s-bit sign-in check, %s process\n", o.bits, build)
	running, detail := ss.Ctfmon()
	if running {
		fmt.Fprintf(w, "ctfmon.exe in this session: running (%s)\n", detail)
	} else {
		fmt.Fprintf(w, "ctfmon.exe in this session: NOT running (%s)\n", detail)
	}
	layouts := ss.SessionLayouts()
	find := func(match func(uintptr) bool) (uintptr, bool) {
		for _, h := range layouts {
			if match(h) {
				return h, true
			}
		}
		return 0, false
	}
	var outs []wincheck.Outcome
	report := func(out wincheck.Outcome) {
		outs = append(outs, out)
		fmt.Fprintln(w, out.Summary())
		f.Sync()
	}
	// The control: Windows' own US layout, in the session or loaded by its in-box KLID.
	ctl := wincheck.Outcome{Name: "control." + build}
	if hkl, in := find(func(h uintptr) bool { return uint32(h) == usHKL }); in {
		ctl = typeAll(ss, sys, hkl, ctl, wincheck.ControlExpectations, "")
	} else if hkl, err := sys.Load("00000409"); err == nil {
		ctl = typeAll(ss, sys, hkl, ctl, wincheck.ControlExpectations, "")
		ctl.Notes = append(ctl.Notes, "US layout loaded by KLID 00000409 (not in the session list)")
	} else {
		ctl.Err = "US layout: " + err.Error()
	}
	report(redact(ctl))
	for i, e := range entries {
		if o.only != "" && !strings.Contains(e.Name, o.only) {
			continue
		}
		slot := wincheck.SlotFor("s", i)
		out := wincheck.Outcome{Name: e.Name}
		exp, err := os.ReadFile(filepath.Join(o.bundle, e.Expect))
		if err != nil {
			out.Err = err.Error()
			report(out)
			continue
		}
		want := uint32(0xF000|slot.LayoutID) << 16
		hkl, in := find(func(h uintptr) bool { return uint32(h)&0xFFFF0000 == want })
		if !in {
			// Not loaded by the sign-in: a FAIL; still load and type it, so the line says whether the file works.
			hkl, err = sys.Load(slot.KLID)
			if err != nil {
				out.Err = "not in the session after sign-in, and LoadKeyboardLayoutW: " + err.Error()
				report(out)
				continue
			}
		}
		out = typeAll(ss, sys, hkl, out, string(exp), slot.KLID)
		if !in {
			out.Err = strings.TrimPrefix(out.Err+"; ", "; ") + "not in the session after sign-in (loaded by the runner instead)"
		} else {
			out.Notes = append(out.Notes, "in the session after sign-in")
		}
		report(out)
	}
	var full bytes.Buffer
	_, failed := wincheck.Report(io.Discard, &full, o.bits, outs)
	fmt.Fprintf(w, "%s-bit sign-in check: %d passed, %d failed\n", o.bits, len(outs)-failed, failed)
	f.Write([]byte("\n" + full.String()))
	if failed > 0 || !running {
		return 1
	}
	return 0
}

// typeAll checks one layout: every line through ToUnicodeEx, then the real-input lines into the window.
func typeAll(ss signinSystem, sys system, hkl uintptr, out wincheck.Outcome, exp, klid string) wincheck.Outcome {
	lines, err := wincheck.ParseExpectations(strings.NewReader(exp))
	if err != nil {
		out.Err = "expectations: " + err.Error()
		return out
	}
	out.Lines = len(lines)
	out.Diffs = wincheck.Check(lines, sys.Prober(hkl))
	typer, err := ss.Typer(hkl)
	if err != nil {
		out.Err = "real input: " + err.Error()
		return out
	}
	in := wincheck.InputLines(lines, inputKeys, inputSeqs)
	d := wincheck.CheckInput(in, typer)
	for i := range d {
		d[i].Got = "real input: " + d[i].Got
	}
	out.Diffs = append(out.Diffs, d...)
	out.Notes = append(out.Notes, fmt.Sprintf("%d typed as real input", len(in)))
	return out
}

// signinList reports, without loading anything, which installed layouts are in this process's session
// (GetKeyboardLayoutList) and which are in the signed-in user's input list, -repeat times -interval apart: it tells
// "not loaded at sign-in" from "not in the list", and shows whether Windows loads layouts of the list later.
func signinList(ss signinSystem, o options, build string, entries []wincheck.Entry, stdout, stderr io.Writer) int {
	f, err := os.Create(o.out)
	if err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	defer f.Close()
	w := io.MultiWriter(stdout, f)
	fmt.Fprintf(w, "yakbd-wincheck %s-bit sign-in list, %s process (nothing is loaded)\n", o.bits, build)
	running, detail := ss.Ctfmon()
	fmt.Fprintf(w, "ctfmon.exe in this session: %v (%s)\n", running, detail)
	start := time.Now()
	for pass := 1; pass <= max(o.repeat, 1); pass++ {
		if pass > 1 {
			time.Sleep(o.interval)
		}
		layouts := ss.SessionLayouts()
		in := 0
		var lines []string
		for i, e := range entries {
			slot := wincheck.SlotFor("s", i)
			want := uint32(0xF000|slot.LayoutID) << 16
			found := false
			for _, h := range layouts {
				found = found || uint32(h)&0xFFFF0000 == want
			}
			if found {
				in++
			}
			prof, sub, pre := ss.UserList(slot.KLID)
			if pre == "" {
				pre = "-"
			}
			lines = append(lines, fmt.Sprintf("LIST %s (KLID %s, entry %d): session %s; user list: User Profile %s, Substitutes %s, Preload %s",
				e.Name, slot.KLID, i, yes(found), yes(prof), yes(sub), pre))
		}
		fmt.Fprintf(w, "pass %d at +%ds: %d of %d layouts in the session, %d layouts in GetKeyboardLayoutList in all\n",
			pass, int(time.Since(start).Seconds()), in, len(entries), len(layouts))
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
		f.Sync()
	}
	return 0
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// enableTimeout is how long enableAll waits for the user's list to show an enabled layout.
var enableTimeout = 15 * time.Second

// enableAll puts every installed layout into the user's input list and checks that each one arrived there.
// Sign-in run 2 called InstallLayoutOrTip once per layout, in quick succession: every call returned TRUE, but only
// the first KLID reached User Profile and Substitutes. So the layouts are enabled in one call (entries joined by
// ";"), the list is read back, and a layout still missing is enabled again on its own after a pause; every layout
// that never arrives is a FAIL line. Returns the number of failures.
func enableAll(ss signinSystem, slots []wincheck.Slot, names []string, w io.Writer) int {
	inList := func(s wincheck.Slot) bool { p, sub, _ := ss.UserList(s.KLID); return p && sub }
	wait := func(s wincheck.Slot) bool {
		for end := time.Now().Add(enableTimeout); ; time.Sleep(500 * time.Millisecond) {
			if inList(s) {
				return true
			}
			if time.Now().After(end) {
				return false
			}
		}
	}
	if err := ss.Enable(slots); err != nil {
		fmt.Fprintf(w, "FAIL enable (one call for %d layouts): %v\n", len(slots), err)
	}
	failed := 0
	for i, s := range slots {
		how := "one call for all"
		ok := wait(s)
		if !ok {
			how = "missing after the call for all; enabled again on its own"
			if err := ss.Enable([]wincheck.Slot{s}); err == nil {
				ok = wait(s)
			}
		}
		if !ok {
			failed++
			fmt.Fprintf(w, "FAIL enable %s: KLID %s is not in the user's list (User Profile and Substitutes)\n", names[i], s.KLID)
			continue
		}
		fmt.Fprintf(w, "enabled %s: KLID %s in the user's list (%s)\n", names[i], s.KLID, how)
	}
	return failed
}
