// Command tm-guest is the guest side of the MSKLC comparison bundle (tools/msklcbundle, docs/dll-import.md §6).
// It runs inside a disposable Windows guest that has MSKLC 1.4 installed, from the bundle folder:
//
//	tm-guest.exe -bundle C:\tm
//
// For every case of cases.txt it builds the .klc with MSKLC's compiler kbdutool (amd64 -m, WOW64 -o, x86 -x) and
// with yakbd.exe, has yakbd-wincheck load and type both DLL sets against the same expectations (64- and 32-bit
// processes), imports each MSKLC DLL with yakbd.exe and compares the result with the import of the .klc
// (kbldiff), and compares the tables. Only text written by this program goes into results\: exit codes, the
// numbers of kbdutool's errors and warnings (never its messages), loss-kind ids, and differences of our own
// layouts. The DLLs stay in work\.
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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/tools/msklcbundle/kbldiff"
)

// builtFrom is set by the bundle tool (-ldflags -X).
var builtFrom = "unknown"

// modes are kbdutool's build switches (MSKLC observations tests/vm/wb-t1.ps1, tests/dll-format/README.md) and the
// yakbd build each one corresponds to.
var modes = []struct{ flag, build string }{{"-m", "x64"}, {"-o", "wow64"}, {"-x", "x86"}}

// kbdutoolPaths are where MSKLC 1.4 installs its compiler (tests/vm/wb-t1.ps1).
var kbdutoolPaths = []string{
	`C:\Program Files (x86)\Microsoft Keyboard Layout Creator 1.4\bin\i386\kbdutool.exe`,
	`C:\Program Files\Microsoft Keyboard Layout Creator 1.4\bin\i386\kbdutool.exe`,
}

type kase struct {
	name, langID, source, note string
}

type env struct {
	bundle, work, results string
	kbdutool, yakbd       string
	timeout               time.Duration
	log                   io.Writer // the console and results\log.txt
	only                  string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tm-guest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bundle := fs.String("bundle", ".", "the bundle folder")
	kbdutool := fs.String("kbdutool", "", "path of kbdutool.exe (default: MSKLC 1.4's install folder)")
	steps := fs.String("steps", "control,build,type,import", "comma-separated steps")
	only := fs.String("only", "", "only the cases whose names contain this text")
	timeout := fs.Duration("timeout", 3*time.Minute, "time limit of one kbdutool or yakbd run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if abs, err := filepath.Abs(*bundle); err == nil {
		*bundle = abs // the programs run with their own working folders
	}
	e := &env{bundle: *bundle, work: filepath.Join(*bundle, "work"), results: filepath.Join(*bundle, "results"),
		yakbd: filepath.Join(*bundle, "yakbd.exe"), timeout: *timeout, only: *only}
	os.MkdirAll(e.work, 0o755)
	os.MkdirAll(e.results, 0o755)
	lf, err := os.OpenFile(filepath.Join(e.results, "log.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(stderr, "tm-guest:", err)
		return 2
	}
	defer lf.Close()
	e.log = io.MultiWriter(stdout, lf)
	fmt.Fprintf(e.log, "tm-guest (built from %s), %s, steps %s\n", builtFrom, time.Now().Format(time.RFC3339), *steps)
	cases, err := readCases(filepath.Join(*bundle, "cases.txt"))
	if err != nil {
		fmt.Fprintln(e.log, "tm-guest:", err)
		return 2
	}
	if *only != "" {
		var keep []kase
		for _, c := range cases {
			if strings.Contains(c.name, *only) {
				keep = append(keep, c)
			}
		}
		cases = keep
	}
	e.kbdutool = *kbdutool
	if e.kbdutool == "" {
		for _, p := range kbdutoolPaths {
			if _, err := os.Stat(p); err == nil {
				e.kbdutool = p
				break
			}
		}
	}
	code := 0
	for _, s := range strings.Split(*steps, ",") {
		var err error
		switch strings.TrimSpace(s) {
		case "control":
			err = e.control(cases)
		case "build":
			if e.kbdutool == "" {
				err = errors.New("kbdutool.exe not found (is MSKLC 1.4 installed? -kbdutool names it)")
				break
			}
			err = e.build(cases)
		case "type":
			err = e.typing(cases)
		case "import":
			err = e.importStep(cases)
		default:
			err = fmt.Errorf("unknown step %q", s)
		}
		if err != nil {
			fmt.Fprintf(e.log, "step %s: %v\n", s, err)
			code = 1
		}
	}
	fmt.Fprintln(e.log, "tm-guest: done")
	return code
}

// readCases reads cases.txt: name, LANGID, source, note (tab-separated; # comments).
func readCases(path string) ([]kase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []kase
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		for len(f) < 4 {
			f = append(f, "")
		}
		out = append(out, kase{f[0], f[1], f[2], f[3]})
	}
	return out, nil
}

func (e *env) caseDir(c kase) string { return filepath.Join(e.work, c.name) }
func (e *env) klcPath(c kase) string { return filepath.Join(e.bundle, "cases", c.name+".klc") }

// exe runs a program with the time limit and returns its exit code (-1: did not start or timed out) and output.
func (e *env) exe(dir, prog string, args ...string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Dir = dir
	// kbdutool starts a compiler of its own; when the time limit kills kbdutool, a child that still holds the
	// output pipe must not keep Run waiting.
	cmd.WaitDelay = 10 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return -1, "timed out"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), out.String()
	}
	if err != nil {
		return -1, err.Error()
	}
	return 0, out.String()
}

// yakbdRun runs yakbd.exe with -report and returns the exit code and the loss-kind ids of the report with their
// severities ("warning dll.resourceDropped"), never messages.
func (e *env) yakbdRun(dir string, args ...string) (int, []string) {
	rep := filepath.Join(dir, "report.json")
	os.Remove(rep)
	code, _ := e.exe(dir, e.yakbd, append([]string{args[0], "-q", "-report", rep}, args[1:]...)...)
	var r struct {
		Entries []struct{ Severity, Kind string }
	}
	b, _ := os.ReadFile(rep)
	json.Unmarshal(b, &r)
	seen := map[string]bool{}
	var kinds []string
	for _, en := range r.Entries {
		if en.Severity == "info" {
			continue
		}
		k := en.Severity + " " + en.Kind
		if !seen[k] {
			seen[k] = true
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	return code, kinds
}

// results opens a results file (truncated) that also echoes to the log.
func (e *env) resultsFile(name string) (io.Writer, func(), error) {
	f, err := os.Create(filepath.Join(e.results, name))
	if err != nil {
		return nil, nil, err
	}
	return io.MultiWriter(f, e.log), func() { f.Close() }, nil
}

// control: the bundle's control step, the host test TestControl repeated with the Windows build of yakbd: each
// case built by yakbd, imported again and compared with the import of the .klc must show no difference.
func (e *env) control(cases []kase) error {
	w, done, err := e.resultsFile("control.txt")
	if err != nil {
		return err
	}
	defer done()
	fmt.Fprintf(w, "== control (built from %s): yakbd's own DLLs imported again and compared with the .klc import\n", builtFrom)
	pass, fail, skip := 0, 0, 0
	for _, c := range cases {
		dir := filepath.Join(e.caseDir(c), "control")
		os.RemoveAll(dir)
		os.MkdirAll(dir, 0o755)
		klcJSON := filepath.Join(dir, "klc.kbl.json")
		if code, kinds := e.yakbdRun(dir, "convert", e.klcPath(c), klcJSON); code != 0 {
			fmt.Fprintf(w, "SKIP %s: yakbd refuses the .klc (%s)\n", c.name, strings.Join(errorsOnly(kinds), ", "))
			skip++
			continue
		}
		if code, kinds := e.yakbdRun(dir, "compile", "-target", "x64,wow64,x86", "-langid", c.langID, "-accept", "windows.vkMissing",
			"-o", dir, e.klcPath(c)); code != 0 {
			fmt.Fprintf(w, "SKIP %s: yakbd does not build it (%s)\n", c.name, strings.Join(errorsOnly(kinds), ", "))
			skip++
			continue
		}
		a, _ := os.ReadFile(klcJSON)
		for _, m := range modes {
			dll := findDLL(filepath.Join(dir, m.build))
			imp := filepath.Join(dir, m.build+".kbl.json")
			if dll == "" {
				fmt.Fprintf(w, "FAIL %s %s: no DLL\n", c.name, m.build)
				fail++
				continue
			}
			if code, kinds := e.yakbdRun(dir, "convert", "-langid", c.langID, dll, imp); code != 0 {
				fmt.Fprintf(w, "FAIL %s %s: import refused (%s)\n", c.name, m.build, strings.Join(errorsOnly(kinds), ", "))
				fail++
				continue
			}
			b, _ := os.ReadFile(imp)
			diffs, err := kbldiff.Files(a, b)
			if err != nil || len(diffs) > 0 {
				fmt.Fprintf(w, "FAIL %s %s: %d differences %v\n", c.name, m.build, len(diffs), err)
				printDiffs(w, diffs)
				fail++
				continue
			}
			pass++
		}
	}
	fmt.Fprintf(w, "control: %d passed, %d failed, %d cases skipped\n", pass, fail, skip)
	return nil
}

func errorsOnly(kinds []string) []string {
	var out []string
	for _, k := range kinds {
		if strings.HasPrefix(k, "error ") || strings.HasPrefix(k, "fatal ") {
			out = append(out, k)
		}
	}
	if len(out) == 0 {
		return []string{"no error kind in the report"}
	}
	return out
}

func findDLL(dir string) string {
	m, _ := filepath.Glob(filepath.Join(dir, "*.dll"))
	if len(m) == 1 {
		return m[0]
	}
	return ""
}

func printDiffs(w io.Writer, diffs []kbldiff.Difference) {
	for i, d := range diffs {
		if i == 25 {
			fmt.Fprintf(w, "    ... %d more\n", len(diffs)-i)
			return
		}
		fmt.Fprintf(w, "    %s\n", d.Redacted())
	}
}

func printLines(w io.Writer, lines []string) {
	for i, l := range lines {
		if i == 25 {
			fmt.Fprintf(w, "    ... %d more\n", len(lines)-i)
			return
		}
		fmt.Fprintf(w, "    %s\n", l)
	}
}

// numbers matches kbdutool's diagnostics by number: only "E2013" / "W1009" leave the guest (as in the reference
// specification's runs, MSKLC observations tests/klc-format/README.md), never message text.
var numbers = regexp.MustCompile(`(?i)\b(error|warning)\b[^0-9\r\n]{0,16}([0-9]{4})\b`)

func diagnostics(out string) string {
	var s []string
	seen := map[string]bool{}
	for _, m := range numbers.FindAllStringSubmatch(out, -1) {
		t := strings.ToUpper(m[1][:1]) + m[2]
		if !seen[t] {
			seen[t] = true
			s = append(s, t)
		}
	}
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, " ")
}

// build: kbdutool and yakbd build every case.
func (e *env) build(cases []kase) error {
	w, done, err := e.resultsFile("build.txt")
	if err != nil {
		return err
	}
	defer done()
	fmt.Fprintf(w, "== build (built from %s): kbdutool -u -v -w <mode> in.klc, and yakbd compile, per case\n", builtFrom)
	counts := map[string]int{}
	for _, c := range cases {
		dir := e.caseDir(c)
		in, err := os.ReadFile(e.klcPath(c))
		if err != nil {
			return err
		}
		var cols []string
		for _, m := range modes {
			md := filepath.Join(dir, "msklc", m.build)
			os.RemoveAll(md)
			os.MkdirAll(md, 0o755)
			os.WriteFile(filepath.Join(md, "in.klc"), in, 0o644)
			code, out := e.exe(md, e.kbdutool, "-u", "-v", "-w", m.flag, "in.klc")
			built := findDLL(md) != ""
			state := "built"
			if !built {
				state = "nothing"
			}
			counts["msklc "+m.build+" "+state]++
			cols = append(cols, fmt.Sprintf("msklc %s %s (exit %d, %s)", m.flag, state, code, diagnostics(out)))
		}
		yd := filepath.Join(dir, "yakbd")
		os.RemoveAll(yd)
		os.MkdirAll(yd, 0o755)
		code, kinds := e.yakbdRun(yd, "compile", "-target", "x64,wow64,x86", "-emit-expectations", "-langid", c.langID,
			"-accept", "windows.vkMissing", "-o", yd, e.klcPath(c))
		state := "built"
		if code != 0 {
			state = "refused: " + strings.Join(errorsOnly(kinds), ", ")
			counts["yakbd refused"]++
		} else {
			counts["yakbd built"]++
		}
		cols = append(cols, "yakbd "+state)
		note := ""
		if c.note != "" {
			note = "   [expected: " + c.note + "]"
		}
		fmt.Fprintf(w, "%s: %s%s\n", c.name, strings.Join(cols, "; "), note)
	}
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %s: %d\n", k, counts[k])
	}
	return nil
}

// typing: two wincheck bundles (MSKLC's DLLs and yakbd's), one set of expectations (yakbd's, from the .klc),
// each run by the 64-bit runner (x64 DLLs) and the 32-bit runner (WOW64 DLLs).
func (e *env) typing(cases []kase) error {
	sets := map[string][]wincheck.Entry{}
	for _, c := range cases {
		exp, _ := filepath.Glob(filepath.Join(e.caseDir(c), "yakbd", "*.expect.txt"))
		if len(exp) != 1 {
			continue // yakbd refused the case: no expectations
		}
		rel := func(p string) string {
			r, _ := filepath.Rel(filepath.Join(e.work, "set-msklc"), p)
			return filepath.ToSlash(r)
		}
		for _, set := range []string{"msklc", "yakbd"} {
			en := wincheck.Entry{Name: c.name, DLL: map[string]string{}, Expect: rel(exp[0]), Note: c.note}
			for _, b := range []string{"x64", "wow64"} {
				d := filepath.Join(e.caseDir(c), set, b)
				if set == "msklc" {
					d = filepath.Join(e.caseDir(c), "msklc", b)
				}
				if p := findDLL(d); p != "" {
					en.DLL[b] = rel(p)
				}
			}
			if len(en.DLL) > 0 {
				sets[set] = append(sets[set], en)
			}
		}
	}
	for _, set := range []string{"msklc", "yakbd"} {
		// Both manifests live in work\set-<name>\, so the relative paths (..\<case>\...) hold for both.
		dir := filepath.Join(e.work, "set-"+set)
		os.MkdirAll(dir, 0o755)
		var m bytes.Buffer
		wincheck.WriteManifest(&m, sets[set])
		if err := os.WriteFile(filepath.Join(dir, "manifest.txt"), m.Bytes(), 0o644); err != nil {
			return err
		}
		for _, bits := range []string{"64", "32"} {
			out := filepath.Join(e.results, "type-"+set+"-"+bits+".txt")
			runner := filepath.Join(e.bundle, "yakbd-wincheck-"+bits+".exe")
			fmt.Fprintf(e.log, "typing: %s DLLs, %s-bit runner, %d layouts\n", set, bits, len(sets[set]))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
			cmd := exec.CommandContext(ctx, runner, "-bundle", dir, "-out", out)
			cmd.WaitDelay = 10 * time.Second
			cmd.Stdout, cmd.Stderr = io.Discard, e.log
			err := cmd.Run()
			cancel()
			var ee *exec.ExitError
			if err != nil && !errors.As(err, &ee) {
				fmt.Fprintf(e.log, "typing: %s %s-bit runner did not run: %v\n", set, bits, err)
			}
		}
	}
	return nil
}

// importStep: every MSKLC DLL is imported by yakbd.exe; the result is compared with the import of the .klc
// (kbldiff), built again and compared table by table with the MSKLC DLL (the import's fidelity), and the MSKLC
// DLL's tables are compared with those of yakbd's DLL of the same build (how the two compilers differ).
func (e *env) importStep(cases []kase) error {
	w, done, err := e.resultsFile("import.txt")
	if err != nil {
		return err
	}
	defer done()
	fmt.Fprintf(w, "== import (built from %s): yakbd convert <MSKLC DLL> against yakbd convert <.klc>\n", builtFrom)
	fmt.Fprintln(w, "   semantic: kbldiff of the two .kbl.json (first = .klc import, second = DLL import)")
	fmt.Fprintln(w, "   round trip: tables of the MSKLC DLL against the yakbd build of its import (windows.Differences)")
	fmt.Fprintln(w, "   compilers (information): tables of the MSKLC DLL against yakbd's DLL of the same .klc and build")
	fmt.Fprintln(w, "   builds with the same result share one line")
	counts := map[string]int{}
	kindCount := map[string]int{}
	factCount := map[string]int{} // per semantic fact kind / table field: number of DLLs
	for _, c := range cases {
		dir := filepath.Join(e.caseDir(c), "import")
		os.RemoveAll(dir)
		os.MkdirAll(dir, 0o755)
		klcJSON := filepath.Join(dir, "klc.kbl.json")
		klcOK := false
		if code, _ := e.yakbdRun(dir, "convert", e.klcPath(c), klcJSON); code == 0 {
			klcOK = true
		}
		a, _ := os.ReadFile(klcJSON)
		type result struct{ head, body string }
		var results []result
		var builds []string
		for _, m := range modes {
			msklc := findDLL(filepath.Join(e.caseDir(c), "msklc", m.build))
			if msklc == "" {
				counts["no MSKLC DLL"]++
				continue
			}
			orig, err := os.ReadFile(msklc)
			if err != nil {
				return err
			}
			bd := filepath.Join(dir, m.build)
			os.MkdirAll(bd, 0o755)
			imp := filepath.Join(bd, "imported.kbl.json")
			code, kinds := e.yakbdRun(bd, "convert", "-langid", c.langID, msklc, imp)
			for _, k := range kinds {
				kindCount[k]++
			}
			if code != 0 {
				results = append(results, result{"FAIL: import refused (" + strings.Join(errorsOnly(kinds), ", ") + ")", ""})
				builds = append(builds, m.build)
				counts["import refused"]++
				continue
			}
			var problems, lines, notes []string
			seen := map[string]bool{}
			count := func(k string) {
				if !seen[k] {
					seen[k] = true
					factCount[k]++
				}
			}
			if klcOK {
				b, _ := os.ReadFile(imp)
				diffs, err := kbldiff.Files(a, b)
				if err != nil {
					problems = append(problems, "kbldiff: "+err.Error())
				} else if len(diffs) > 0 {
					problems = append(problems, fmt.Sprintf("semantic %d", len(diffs)))
					for _, d := range diffs {
						lines = append(lines, "semantic: "+d.Redacted())
						count("semantic " + strings.SplitN(d.Key, " ", 2)[0])
					}
				}
			} else {
				notes = append(notes, "yakbd refuses the .klc: no semantic comparison")
			}
			rt := filepath.Join(bd, "rebuilt")
			if code, kinds := e.yakbdRun(bd, "compile", "-target", m.build, "-o", rt, imp); code != 0 {
				problems = append(problems, "rebuild refused ("+strings.Join(errorsOnly(kinds), ", ")+")")
			} else {
				for _, l := range tableDiffs(orig, findDLL(filepath.Join(rt, m.build))) {
					if l == "keyboard type" {
						// not kept by the import on purpose (coverage §11 item 11; OBS D9, D28)
						notes = append(notes, "keyboard type not kept")
						continue
					}
					lines = append(lines, "round trip: "+l)
					count("round trip " + field(l))
				}
				if n := len(lines) - countPrefix(lines, "semantic: "); n > 0 {
					problems = append(problems, fmt.Sprintf("round trip %d", n))
				}
			}
			var info []string
			if y := findDLL(filepath.Join(e.caseDir(c), "yakbd", m.build)); y != "" {
				for _, l := range tableDiffs(orig, y) {
					info = append(info, "compilers: "+l+scanDetail(l, orig, y))
					count("compilers " + field(l))
				}
				if len(info) > 0 {
					counts["compilers differ"]++
				} else {
					counts["compilers same tables"]++
				}
			}
			status := "PASS"
			if len(problems) == 0 {
				problems = []string{"same"}
				counts["import same"]++
			} else {
				status = "FAIL"
				counts["import differs"]++
			}
			head := fmt.Sprintf("%s: %s; import kinds: %s; compilers: %d table differences", status,
				strings.Join(append(problems, notes...), ", "), strings.Join(kinds, ", "), len(info))
			var body strings.Builder
			printLines(&body, append(lines, info...))
			results = append(results, result{head, body.String()})
			builds = append(builds, m.build)
		}
		// Builds with the same result share one entry.
		for i := 0; i < len(results); i++ {
			if results[i].head == "" {
				continue
			}
			names := []string{builds[i]}
			for j := i + 1; j < len(results); j++ {
				if results[j] == results[i] {
					names = append(names, builds[j])
					results[j].head = ""
				}
			}
			status, rest, _ := strings.Cut(results[i].head, ": ")
			fmt.Fprintf(w, "%s %s %s: %s\n", status, c.name, strings.Join(names, ","), rest)
			io.WriteString(w, results[i].body)
		}
	}
	fmt.Fprintf(w, "import check: %d of %d MSKLC DLLs pass (%d refused, %d differ)\n", counts["import same"],
		counts["import same"]+counts["import differs"]+counts["import refused"], counts["import refused"], counts["import differs"])
	for _, k := range sortedKeys(counts) {
		fmt.Fprintf(w, "  %s: %d DLLs\n", k, counts[k])
	}
	for _, k := range sortedKeys(factCount) {
		fmt.Fprintf(w, "  differs in %s: %d DLLs\n", k, factCount[k])
	}
	for _, k := range sortedKeys(kindCount) {
		fmt.Fprintf(w, "  import reported %s: %d DLLs\n", k, kindCount[k])
	}
	return nil
}

// field folds a windows.Differences line into its field ("table 2 row 7" -> "character rows").
func field(d string) string {
	switch {
	case strings.HasPrefix(d, "character tables:"):
		return "number of character tables"
	case strings.HasPrefix(d, "table ") && strings.Contains(d, " row "):
		return "character rows"
	case strings.HasPrefix(d, "table "):
		return "table widths or row counts"
	}
	return d
}

func countPrefix(lines []string, p string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, p) {
			n++
		}
	}
	return n
}

// tableDiffs compares the tables of two DLL files after windows.Canonical.
func tableDiffs(a []byte, bPath string) []string {
	if bPath == "" {
		return []string{"no second DLL"}
	}
	b, err := os.ReadFile(bPath)
	if err != nil {
		return []string{err.Error()}
	}
	da, _, err1 := windows.Read(a)
	db, _, err2 := windows.Read(b)
	if err1 != nil || err2 != nil {
		return []string{fmt.Sprintf("unreadable: %v %v", err1, err2)}
	}
	return windows.Differences(&da.Tables, &db.Tables)
}

func sortedKeys(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scanDetail says, for a scan-code table that differs between MSKLC's and yakbd's DLL, which scan codes differ
// and how (VK and flags in hex, first ones only): MSKLC's default scan-code table is that of Windows' own US
// layout (OBS D20), so these are differences of yakbd's Table A, not data of MSKLC's.
func scanDetail(field string, a []byte, bPath string) string {
	if field != "pusVSCtoVK" && field != "E0 table" && field != "E1 table" {
		return ""
	}
	b, err := os.ReadFile(bPath)
	if err != nil {
		return ""
	}
	da, _, err1 := windows.Read(a)
	db, _, err2 := windows.Read(b)
	if err1 != nil || err2 != nil {
		return ""
	}
	var diffs []string
	add := func(prefix string, sc int, x, y uint16, hx, hy bool) {
		if hx == hy && x == y {
			return
		}
		f := func(v uint16, h bool) string {
			if !h {
				return "-"
			}
			return fmt.Sprintf("%04x", v)
		}
		diffs = append(diffs, fmt.Sprintf("%s%02x %s/%s", prefix, sc, f(x, hx), f(y, hy)))
	}
	if field == "pusVSCtoVK" {
		ta, tb := da.Tables.ScanToVK, db.Tables.ScanToVK
		for i := 0; i < max(len(ta), len(tb)); i++ {
			var x, y uint16
			if i < len(ta) {
				x = ta[i]
			}
			if i < len(tb) {
				y = tb[i]
			}
			add("", i, x, y, i < len(ta), i < len(tb))
		}
	} else {
		ta, tb := da.Tables.E0, db.Tables.E0
		prefix := "e0"
		if field == "E1 table" {
			ta, tb, prefix = da.Tables.E1, db.Tables.E1, "e1"
		}
		ma, mb := map[int]uint16{}, map[int]uint16{}
		for _, e := range ta {
			if _, ok := ma[int(e.Scan)]; !ok {
				ma[int(e.Scan)] = e.VK
			}
		}
		for _, e := range tb {
			if _, ok := mb[int(e.Scan)]; !ok {
				mb[int(e.Scan)] = e.VK
			}
		}
		for sc := 0; sc < 256; sc++ {
			x, hx := ma[sc]
			y, hy := mb[sc]
			add(prefix, sc, x, y, hx, hy)
		}
	}
	if len(diffs) == 0 {
		return " (same entries, other length or order)"
	}
	more := ""
	if len(diffs) > 12 {
		more = fmt.Sprintf(" ... %d in all", len(diffs))
		diffs = diffs[:12]
	}
	return " (scan code MSKLC/yakbd: " + strings.Join(diffs, " ") + more + ")"
}
