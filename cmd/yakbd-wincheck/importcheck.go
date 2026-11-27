package main

// The import check (docs/dll-import.md §5): for every layout file of the system directory this process sees
// (System32 for the 64-bit runner, SysWOW64 for the 32-bit one), yakbd imports it and builds it again, and the
// runner loads the original and the rebuilt file under test names and compares what ToUnicodeEx and
// MapVirtualKeyEx give for both. Only counts and, per failing file, its name and a fixed category are printed: no
// character, table or message of the original file leaves the guest.

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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target/windows"
)

// importSystem is what the import check needs beyond system.
type importSystem interface {
	system
	Keyboard(hkl uintptr) wincheck.Keyboard
	// LayoutFiles returns the kbd*.dll files of the system directory this process sees, read as data.
	LayoutFiles() (map[string][]byte, error)
}

// rebuildFunc imports a layout file and builds it again for one build; category is "" on success, else "import
// refused" or "build refused". kinds counts the report's loss kinds (ids only).
type rebuildFunc func(name string, orig []byte, build string) (rebuilt []byte, category string, kinds []string)

// importOutcome is the result of one file; it holds counts and a category only.
type importOutcome struct {
	Name     string
	Category string `json:",omitempty"` // "" = pass
	Probes   int
	Diffs    int
	Split    wincheck.Comparison
	Tables   bool // the tables differ after windows.Canonical
	// Mechanisms and Fields name only mechanisms and table fields, never data (wincheck.Classify).
	Mechanisms, Fields string
}

// line is the result line of a failing file (passing files are only counted).
func (o importOutcome) line() string {
	if strings.HasSuffix(o.Category, " cells differ") {
		return fmt.Sprintf("FAIL %s: %s (keys %d, dead-key sequences %d, scan codes %d)", o.Name, o.Category, o.Split.Keys, o.Split.Seqs, o.Split.Scans)
	}
	return fmt.Sprintf("FAIL %s: %s", o.Name, o.Category)
}

// runImport is yakbd-wincheck -import.
func runImport(args []string, stdout, stderr io.Writer, sys system, rebuild rebuildFunc) int {
	fs := flag.NewFlagSet("yakbd-wincheck -import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bits := strconv.Itoa(strconv.IntSize)
	self, _ := os.Executable()
	yakbd := fs.String("yakbd", filepath.Join(filepath.Dir(self), "yakbd.exe"), "the Windows build of yakbd")
	out := fs.String("out", "results-import-"+bits+".txt", "results file, written as the run goes")
	only := fs.String("only", "", "only the files whose names contain this text")
	timeout := fs.Duration("timeout", 10*time.Minute, "time limit of one file's comparison")
	control := fs.Bool("control", false, "control run: compare every original file with itself (no yakbd); every file must pass")
	limit := fs.Int("limit", 256, "at most this many files (in name order)")
	child := fs.Int("child", -1, "internal: compare file N in this process and print the counts")
	extra := fs.String("extra", "", "internal: the extra modifier VKs (hex, comma-separated)")
	inProcess := fs.Bool("in-process", false, "compare in this process (no crash protection)")
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
		*inProcess = true
	}
	is, ok := sys.(importSystem)
	if !ok {
		fmt.Fprintln(stderr, "yakbd-wincheck: this system cannot run the import check")
		return 2
	}
	if err := sys.Interactive(); err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	runner := "64"
	if sys.Build() != "x64" {
		runner = "32"
	}
	if *child >= 0 {
		c, err := compareInstalled(is, runner, *child, parseExtra(*extra))
		json.NewEncoder(stdout).Encode(struct {
			C   wincheck.Comparison
			Err string
		}{c, err})
		return 0
	}
	files, err := is.LayoutFiles()
	if err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	var names []string
	for n := range files {
		if *only == "" || strings.Contains(strings.ToLower(n), strings.ToLower(*only)) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) > min(*limit, 253) {
		names = names[:min(*limit, 253)] // slots 253-255 are the VM-7 and VM-8 layouts
	}
	if *control {
		rebuild = func(_ string, orig []byte, _ string) ([]byte, string, []string) { return orig, "", nil }
	} else if rebuild == nil {
		rebuild = execRebuild(*yakbd)
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(stderr, "yakbd-wincheck:", err)
		return 2
	}
	defer f.Close()
	w := io.MultiWriter(stdout, f)
	mode := ""
	if *control {
		mode = " CONTROL (each file against itself)"
	}
	fmt.Fprintf(w, "yakbd-wincheck -import%s (built from %s), %s-bit runner, %s build, %d layout files; only failing files are listed\n", mode, builtFrom, bits, sys.Build(), len(names))
	if !*control {
		fmt.Fprintln(w, vm7(is, runner))
		for _, l := range vm8(is, runner) {
			fmt.Fprintln(w, l)
		}
	}
	cats := map[string]int{}
	kindFiles := map[string]int{}
	tablesDiffer, passed := 0, 0
	for i, name := range names {
		o := importOne(is, runner, i, name, files[name], rebuild, kindFiles, *inProcess, *timeout)
		if o.Category != "" {
			fmt.Fprintln(w, o.line())
			if o.Mechanisms != "" {
				fmt.Fprintf(w, "  mechanisms (counts of the first %d differing probes): %s\n", wincheck.MaxSamples, o.Mechanisms)
			}
			if o.Fields != "" {
				fmt.Fprintf(w, "  tables differ in: %s\n", o.Fields)
			}
		}
		f.Sync()
		if o.Tables {
			tablesDiffer++
		}
		if o.Category == "" {
			passed++
		} else {
			cats[categoryKey(o.Category)]++
		}
	}
	fmt.Fprintf(w, "%s-bit import check: %d of %d files pass\n", bits, passed, len(names))
	for _, k := range sortedKeys(cats) {
		fmt.Fprintf(w, "  %s: %d files\n", k, cats[k])
	}
	fmt.Fprintf(w, "  tables differ after the round trip (windows.Differences): %d files\n", tablesDiffer)
	for _, k := range sortedKeys(kindFiles) {
		fmt.Fprintf(w, "  import or build reported %s: %d files\n", k, kindFiles[k])
	}
	if passed < len(names) {
		return 1
	}
	return 0
}

// categoryKey folds "N cells differ" into one key for the counts.
func categoryKey(c string) string {
	if strings.HasSuffix(c, " cells differ") {
		return "cells differ"
	}
	return c
}

func sortedKeys(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseExtra(s string) []byte {
	var out []byte
	for _, p := range strings.Split(s, ",") {
		if v, err := strconv.ParseUint(strings.TrimSpace(p), 16, 8); err == nil {
			out = append(out, byte(v))
		}
	}
	return out
}

// extraModifiers are the VK_TO_BIT keys of both files beyond Shift, Ctrl and Alt.
func extraModifiers(ts ...*windows.Tables) []byte {
	var out []byte
	for _, t := range ts {
		for _, e := range t.Modifiers.VKToBit {
			if e.VK != 0x10 && e.VK != 0x11 && e.VK != 0x12 {
				out = append(out, e.VK)
			}
		}
	}
	return out
}

func importOne(is importSystem, runner string, i int, name string, orig []byte, rebuild rebuildFunc, kindFiles map[string]int,
	inProcess bool, timeout time.Duration) importOutcome {
	o := importOutcome{Name: name}
	build := is.Build()
	rebuilt, cat, kinds := rebuild(name, orig, build)
	for _, k := range kinds {
		kindFiles[k]++
	}
	if cat != "" {
		o.Category = cat
		return o
	}
	ot, _, err1 := windows.Read(orig)
	rt, _, err2 := windows.Read(rebuilt)
	if err1 != nil || err2 != nil {
		o.Category = "rebuilt file unreadable"
		return o
	}
	diffs := windows.Differences(&ot.Tables, &rt.Tables)
	o.Tables = len(diffs) > 0
	o.Fields = fieldKinds(diffs)
	sa, sb := wincheck.ImportSlot(runner, i, false), wincheck.ImportSlot(runner, i, true)
	defer func() {
		for _, s := range []wincheck.Slot{sa, sb} {
			for try := 0; try < 5; try++ {
				if is.Uninstall(s) == nil {
					break
				}
				time.Sleep(time.Second)
			}
		}
	}()
	if is.Install(sa, orig, "yakbd import check original") != nil || is.Install(sb, rebuilt, "yakbd import check rebuilt") != nil {
		o.Category = "setup failed"
		return o
	}
	extra := extraModifiers(&ot.Tables, &rt.Tables)
	var c wincheck.Comparison
	var errText string
	if inProcess {
		c, errText = compareInstalled(is, runner, i, extra)
	} else {
		c, errText = compareChild(i, extra, timeout)
	}
	o.Probes, o.Diffs, o.Split = c.Probes, c.Diffs, c
	switch {
	case errText != "":
		o.Category = errText
	case c.Diffs > 0:
		o.Category = fmt.Sprintf("%d cells differ", c.Diffs)
		tags := wincheck.Classify(&ot.Tables, &rt.Tables, extra, c.Samples)
		if oi, _ := windows.ReadImage(orig); oi != nil {
			if oi.NLS != nil {
				tags["file: NLS table (export 2)"]++
			}
			for _, e := range oi.Exports {
				if e == 3 || e == 5 || e == 6 {
					tags["file: dispatcher exports"]++
				}
			}
		}
		o.Mechanisms = wincheck.FormatTags(tags)
	}
	return o
}

// compareInstalled loads both files of file i and compares them; the error text is a fixed category.
func compareInstalled(is importSystem, runner string, i int, extra []byte) (wincheck.Comparison, string) {
	var hkls [2]uintptr
	for k, rb := range []bool{false, true} {
		s := wincheck.ImportSlot(runner, i, rb)
		h, err := is.Load(s.KLID)
		what := map[bool]string{false: "original", true: "rebuilt"}[rb]
		if err != nil {
			return wincheck.Comparison{}, "load refused (" + what + ")"
		}
		defer is.Unload(h)
		if uint32(h)&0xFFFF0000 != uint32(0xF000|s.LayoutID)<<16 {
			return wincheck.Comparison{}, "another layout answered (" + what + ")"
		}
		hkls[k] = h
	}
	return wincheck.Compare(is.Keyboard(hkls[0]), is.Keyboard(hkls[1]), extra), ""
}

// compareChild runs the comparison in a child process, so that a crash or a hang costs this file only.
func compareChild(i int, extra []byte, timeout time.Duration) (wincheck.Comparison, string) {
	self, err := os.Executable()
	if err != nil {
		return wincheck.Comparison{}, "setup failed"
	}
	var ex []string
	for _, v := range extra {
		ex = append(ex, fmt.Sprintf("%02x", v))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-import", "-child", strconv.Itoa(i), "-extra", strings.Join(ex, ","))
	var so bytes.Buffer
	cmd.Stdout = &so
	err = cmd.Run()
	if ctx.Err() != nil {
		return wincheck.Comparison{}, "timed out"
	}
	var res struct {
		C   wincheck.Comparison
		Err string
	}
	if json.NewDecoder(&so).Decode(&res) == nil && err == nil {
		return res.C, res.Err
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return wincheck.Comparison{}, fmt.Sprintf("the test process crashed (exit code %#x)", uint32(ee.ExitCode()))
	}
	return wincheck.Comparison{}, "the test process failed"
}

// execRebuild runs the Windows build of yakbd: convert (import) and compile, in a temporary folder that is removed
// afterwards. Its output is not printed: it could quote the file.
func execRebuild(yakbd string) rebuildFunc {
	return func(name string, orig []byte, build string) ([]byte, string, []string) {
		dir, err := os.MkdirTemp("", "yakbd-import-")
		if err != nil {
			return nil, "setup failed", nil
		}
		defer os.RemoveAll(dir)
		in := filepath.Join(dir, strings.ToLower(name))
		if os.WriteFile(in, orig, 0o644) != nil {
			return nil, "setup failed", nil
		}
		kbl := filepath.Join(dir, "layout.kbl.json")
		rep1, rep2 := filepath.Join(dir, "import.json"), filepath.Join(dir, "build.json")
		run := func(args ...string) error {
			cmd := exec.Command(yakbd, args...)
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			return cmd.Run()
		}
		err = run("convert", "-q", "-report", rep1, "-langid", "0409", "-keep-keyboard-type", in, kbl)
		kinds := reportKinds(rep1)
		if err != nil {
			return nil, "import refused", kinds
		}
		outDir := filepath.Join(dir, "out")
		// First as the imported file says; a file that needs more is counted (a pseudo-kind) and built with the
		// build's own losses accepted, so that its behaviour is still compared.
		err = run("compile", "-q", "-report", rep2, "-o", outDir, "-target", build, "-langid", "0409", kbl)
		if err != nil {
			kinds = append(kinds, reportKinds(rep2)...)
			kinds = append(kinds, "(builds only with -accept windows.*,general.*)")
			err = run("compile", "-q", "-report", rep2, "-o", outDir, "-target", build, "-langid", "0409", "-accept", "windows.*,general.*", kbl)
		}
		kinds = append(kinds, reportKinds(rep2)...)
		if err != nil {
			return nil, "build refused", kinds
		}
		dlls, _ := filepath.Glob(filepath.Join(outDir, build, "*.dll"))
		if len(dlls) != 1 {
			return nil, "build refused", kinds
		}
		b, err := os.ReadFile(dlls[0])
		if err != nil {
			return nil, "build refused", kinds
		}
		return b, "", kinds
	}
}

// reportKinds returns the distinct loss kinds of a yakbd report file (ids only; defaulted choices are left out).
func reportKinds(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rep struct {
		Entries []struct {
			Class string `json:"class"`
			Kind  string `json:"kind"`
		} `json:"entries"`
	}
	if json.Unmarshal(b, &rep) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range rep.Entries {
		if e.Class == "defaulted" || seen[e.Kind] || !policy.Known(e.Kind) { // only the loss kinds of SPEC §17.3
			continue
		}
		seen[e.Kind] = true
		out = append(out, e.Kind)
	}
	return out
}

// fieldKinds turns windows.Differences into the kinds of table fields that differ, with counts.
func fieldKinds(diffs []string) string {
	m := map[string]int{}
	for _, d := range diffs {
		switch {
		case strings.HasPrefix(d, "character tables:"):
			m["number of character tables"]++
		case strings.HasPrefix(d, "table ") && strings.Contains(d, " row "):
			m["character rows"]++
		case strings.HasPrefix(d, "table "):
			m["table widths or row counts"]++
		default:
			m[d]++
		}
	}
	return wincheck.FormatTags(m)
}

// builtFrom is the commit the bundle was built from (tools/importbundle sets it with -ldflags -X).
var builtFrom = "an unrecorded commit"
