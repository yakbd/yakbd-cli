// Package cli implements the yakbd command line: one function per subcommand, each taking its arguments and
// output streams, so that tests can run them without a process.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/convert/dllkbl"
	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// Version of the tool: the numbers (what DLL and installer version resources carry, which hold four numbers) and the
// pre-release suffix of a semantic version; VersionString joins them ("0.1.0-preview.2").
var Version = [4]uint16{0, 1, 0, 0}

// PreRelease is the semantic-version pre-release suffix after "-", or empty for a final release.
const PreRelease = "preview.2"

// VersionString is the tool's semantic version, major.minor.patch[-pre-release].
func VersionString() string {
	s := fmt.Sprintf("%d.%d.%d", Version[0], Version[1], Version[2])
	if PreRelease != "" {
		s += "-" + PreRelease
	}
	return s
}

// Release is the release version a release build writes here with -X (docs/release.md), for example
// "0.1.0-preview.2"; empty in every other build. `yakbd version` prints it when set.
var Release string

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1 // the input has errors, or the operation failed
	ExitUsage = 2
)

const usage = `usage: yakbd <command> [arguments]

commands:
  compile   build the Windows keyboard-layout DLLs of a layout (.kbl.json, .klc or CLDR .xml)
  installer write one Windows installer (.exe) with every build of a layout
  convert   convert a layout between .kbl.json, .klc and CLDR .xml
  validate  check a layout file and that it can be built for Windows
  pin       write every Windows default into a .kbl.json file
  version   print the version

Run "yakbd <command> -h" for the arguments of a command.
`

// Run runs the command line args (without the program name) and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "compile":
		return compile(args[1:], stdout, stderr)
	case "installer":
		return installerCmd(args[1:], stdout, stderr)
	case "convert":
		return convert(args[1:], stdout, stderr)
	case "validate":
		return validate(args[1:], stdout, stderr)
	case "pin":
		return pin(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		if Release != "" {
			fmt.Fprintf(stdout, "yakbd %s\n", Release)
			return ExitOK
		}
		fmt.Fprintf(stdout, "yakbd %s\n", VersionString())
		return ExitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return ExitOK
	}
	fmt.Fprintf(stderr, "yakbd: unknown command %q\n\n%s", args[0], usage)
	return ExitUsage
}

// reportFlags are the flags every command shares.
type reportFlags struct {
	jsonPath       string
	strictDefaults bool
	quiet          bool
	command        string // the command, for the report file
	input          string // the input file, once known
}

func (rf *reportFlags) register(fs *flag.FlagSet) {
	rf.command = fs.Name()
	fs.StringVar(&rf.jsonPath, "report", "", "also write the report as JSON to this file")
	fs.BoolVar(&rf.strictDefaults, "strict-defaults", false, `treat every choice taken from a default rule as an error ("windowsDefault.*": "error")`)
	fs.BoolVar(&rf.quiet, "q", false, "print only errors and warnings")
}

func (rf *reportFlags) extra() *model.OnLoss {
	if rf.strictDefaults {
		return policy.StrictDefaults
	}
	return nil
}

// finish prints the report and writes its JSON form. It returns true when the report has no errors.
func (rf *reportFlags) finish(r *loss.Report, stderr io.Writer) bool {
	if rf.quiet {
		for _, e := range r.Entries {
			if e.Severity >= loss.Warning {
				fmt.Fprintln(stderr, e.String())
			}
		}
	} else {
		fmt.Fprint(stderr, r.Summary())
	}
	if rf.jsonPath != "" {
		b, _ := json.MarshalIndent(reportOf(r, rf.command, rf.input), "", "  ")
		if err := os.WriteFile(rf.jsonPath, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintf(stderr, "yakbd: %v\n", err)
			return false
		}
	}
	return !r.HasErrors()
}

func newFlags(name, args string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: yakbd %s [flags] %s\n", name, args)
		fs.PrintDefaults()
	}
	return fs
}

// formatOf returns the format of a file by extension.
func formatOf(path string) string {
	p := strings.ToLower(path)
	switch {
	case strings.HasSuffix(p, ".klc"):
		return "klc"
	case strings.HasSuffix(p, ".kbl.json"):
		return "kbl"
	case strings.HasSuffix(p, ".xml"):
		return "cldr"
	case strings.HasSuffix(p, ".dll"):
		return "dll"
	}
	return ""
}

// readLayout reads a layout in any input format into the model. testsPath, if not empty, names a CLDR keyboard
// test file whose tests are added.
func readLayout(path, testsPath string) (*model.Layout, *loss.Report) {
	r := &loss.Report{}
	b, err := os.ReadFile(path)
	if err != nil {
		r.Errorf("io.read", loss.Where{File: path}, "%v", err)
		return nil, r
	}
	name := filepath.Base(path)
	var l *model.Layout
	switch formatOf(path) {
	case "klc":
		f, pr := klc.Parse(b, name)
		r.Merge(pr)
		if f == nil || pr.HasErrors() {
			return nil, r
		}
		var ir *loss.Report
		l, ir = klckbl.FromKLC(f, name)
		r.Merge(ir)
	case "kbl":
		var kr *loss.Report
		l, kr = kbljson.Read(b, name)
		r.Merge(kr)
	case "cldr":
		var cr *loss.Report
		l, cr = cldr.Read(b, name)
		r.Merge(cr)
	case "dll":
		if l = readDLL(b, name, r); l == nil {
			return nil, r
		}
	default:
		r.Errorf("io.format", loss.Where{File: path}, "unknown format (want .kbl.json, .klc, .xml or .dll)")
		return nil, r
	}
	if l != nil && formatOf(path) != "kbl" {
		r.Merge(l.Check())
	}
	if l != nil && testsPath != "" {
		tb, err := os.ReadFile(testsPath)
		if err != nil {
			r.Errorf("io.read", loss.Where{File: testsPath}, "%v", err)
			return nil, r
		}
		tests, tr := cldr.ReadTests(tb, filepath.Base(testsPath))
		r.Merge(tr)
		l.Tests = append(l.Tests, tests...)
	}
	return l, r
}

func compile(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("compile", "layout", stderr)
	var rf reportFlags
	rf.register(fs)
	targets := fs.String("target", "", "comma-separated builds (x64, wow64, x86, arm64x, arm64: the last two unverified); default: targets.windows.builds of the file")
	out := fs.String("o", ".", "output directory; each build goes into <dir>/<build>/")
	expect := fs.Bool("emit-expectations", false, "also write <dir>/<name>.expect.txt: the expected result of every key for a test run on Windows")
	langID := fs.String("langid", "", "LANGID (4 hex digits) for a file that has no targets.windows.registration.langId")
	accept := fs.String("accept", "", "comma-separated loss kinds (or prefix.*) to accept for this run")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(pos) != 1 {
		fs.Usage()
		return ExitUsage
	}
	rf.input = pos[0]
	l, r := readLayout(pos[0], "")
	if l == nil || r.HasErrors() {
		rf.finish(r, stderr)
		return ExitError
	}
	if *langID != "" && (l.Targets == nil || l.Targets.Windows == nil || l.Targets.Windows.Registration == nil || l.Targets.Windows.Registration.LangID == "") {
		setLangID(l, *langID)
		r.Infof("cli.langid", loss.Where{}, "LANGID %s from -langid", *langID)
	}
	res, br := build.Lower(l, build.Options{ToolVersion: Version, Policy: rf.extraWith(*accept)})
	r.Merge(br)
	if res == nil {
		rf.finish(r, stderr)
		return ExitError
	}
	list := res.Targets
	if *targets != "" {
		list = nil
		for _, s := range strings.Split(*targets, ",") {
			switch strings.TrimSpace(s) {
			case "x64":
				list = append(list, windows.TargetAMD64)
			case "wow64":
				list = append(list, windows.TargetWOW64)
			case "x86":
				list = append(list, windows.TargetX86)
			case "arm64x":
				list = append(list, windows.TargetARM64X)
			case "arm64":
				list = append(list, windows.TargetARM64)
			default:
				fmt.Fprintf(stderr, "yakbd compile: unknown build %q (want x64, wow64, x86, arm64x or arm64)\n", s)
				return ExitUsage
			}
		}
	}
	set := compileSet(l, res.DLL, list, rf.extra(), true) // -target arm64x, arm64: the unverified candidates
	r.Merge(set.Report)
	if !rf.finish(r, stderr) || len(set.Builds) == 0 {
		if len(set.Builds) == 0 && !r.HasErrors() {
			fmt.Fprintln(stderr, "yakbd compile: no build to make")
		}
		return ExitError
	}
	names := map[windows.Target]string{windows.TargetAMD64: "x64", windows.TargetWOW64: "wow64", windows.TargetX86: "x86",
		windows.TargetARM64X: "arm64x", windows.TargetARM64: "arm64"}
	for _, b := range set.Builds {
		dir := filepath.Join(*out, names[b.Target])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "yakbd compile: %v\n", err)
			return ExitError
		}
		p := filepath.Join(dir, b.FileName)
		if err := os.WriteFile(p, b.Bytes, 0o644); err != nil {
			fmt.Fprintf(stderr, "yakbd compile: %v\n", err)
			return ExitError
		}
		fmt.Fprintf(stdout, "%s\t%s\t%d bytes\tinstall into %%SystemRoot%%\\%s\n", names[b.Target], p, len(b.Bytes), b.Dir)
	}
	if *expect {
		name := strings.TrimSuffix(res.DLL.FileName, ".dll")
		p := filepath.Join(*out, name+".expect.txt")
		if err := os.WriteFile(p, windows.FormatExpectations(name, res.DLL.Tables.Expectations()), 0o644); err != nil {
			fmt.Fprintf(stderr, "yakbd compile: %v\n", err)
			return ExitError
		}
		fmt.Fprintf(stdout, "expectations\t%s\n", p)
	}
	for _, t := range set.Unsupported {
		fmt.Fprintf(stdout, "%s\tnot supported yet\n", t)
	}
	return ExitOK
}

func convert(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("convert", "input output", stderr)
	var rf reportFlags
	rf.register(fs)
	forMSKLC := fs.Bool("msklc", false, "write a .klc file for MSKLC's editor: report what it would change")
	tests := fs.String("tests", "", "a CLDR keyboard test file to add to a CLDR input")
	outFlag := fs.String("o", "", "the output file (instead of the second argument)")
	langID := fs.String("langid", "", "LANGID (4 hex digits) to pin as targets.windows.registration.langId when the input has none (a DLL never has one)")
	keepType := fs.Bool("keep-keyboard-type", false, "keep dwType and dwSubType of an imported DLL (not kept by default: coverage/windows.md §11 item 11)")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return ExitUsage
	}
	importOptions = dllkbl.Options{LangID: *langID, KeepKeyboardType: *keepType}
	defer func() { importOptions = dllkbl.Options{} }()
	if *outFlag != "" {
		pos = append(pos, *outFlag)
	}
	if len(pos) != 2 {
		fs.Usage()
		return ExitUsage
	}
	in, outPath := pos[0], pos[1]
	rf.input = in
	l, r := readLayout(in, *tests)
	if l == nil || r.HasErrors() {
		rf.finish(r, stderr)
		return ExitError
	}
	if *langID != "" && (l.Targets == nil || l.Targets.Windows == nil || l.Targets.Windows.Registration == nil || l.Targets.Windows.Registration.LangID == "") {
		setLangID(l, *langID)
		r.Infof("cli.langid", loss.Where{}, "LANGID %s from -langid", *langID)
	}
	var data []byte
	var wr *loss.Report
	target := ""
	switch formatOf(outPath) {
	case "kbl":
		data, wr = kbljson.Write(l)
	case "klc":
		target = "klc"
		var f *klc.File
		f, wr = klckbl.ToKLC(l, build.Options{ToolVersion: Version})
		if f != nil {
			msklc := *forMSKLC
			if t := l.Targets; t != nil && t.KLC != nil && t.KLC.MSKLCEditorCompatible != nil {
				msklc = msklc || *t.KLC.MSKLCEditorCompatible
			}
			var kr *loss.Report
			data, kr = klc.Write(f, klc.WriteOptions{ForMSKLC: msklc})
			for i := range kr.Entries {
				if strings.HasPrefix(kr.Entries[i].Code, "klc.msklc.") {
					kr.Entries[i].Class, kr.Entries[i].Code = loss.Loss, "klc.editorDropped"
				}
			}
			wr.Merge(kr)
		}
	case "cldr":
		target = "cldr"
		data, wr = cldr.Write(l)
	default:
		fmt.Fprintf(stderr, "yakbd convert: unknown output format of %s (want .kbl.json, .klc or .xml)\n", outPath)
		return ExitUsage
	}
	extra := rf.extra()
	if target == "klc" {
		extra = klckbl.ExportPolicy(extra)
	}
	policy.Resolve(wr, l, target, extra)
	r.Merge(wr)
	if !rf.finish(r, stderr) || data == nil {
		return ExitError
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "yakbd convert: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func validate(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("validate", "layout", stderr)
	var rf reportFlags
	rf.register(fs)
	forWindows := fs.Bool("windows", false, "also check that the layout builds for Windows (the build's report)")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(pos) != 1 {
		fs.Usage()
		return ExitUsage
	}
	in := pos[0]
	rf.input = in
	// The file itself: format rules, schema and the checks of SPEC §19.
	l, r := readLayout(in, "")
	if l != nil && !r.HasErrors() && *forWindows {
		_, br := build.Lower(l, build.Options{ToolVersion: Version, Policy: rf.extra()})
		r.Merge(br)
	}
	if !rf.finish(r, stderr) {
		return ExitError
	}
	fmt.Fprintf(stdout, "%s: valid\n", in)
	return ExitOK
}

func pin(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("pin", "layout [output.kbl.json]", stderr)
	var rf reportFlags
	rf.register(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(pos) < 1 || len(pos) > 2 {
		fs.Usage()
		return ExitUsage
	}
	in := pos[0]
	rf.input = in
	outPath := in
	if len(pos) == 2 {
		outPath = pos[1]
	}
	if formatOf(outPath) != "kbl" {
		fmt.Fprintln(stderr, "yakbd pin: the output must be a .kbl.json file")
		return ExitUsage
	}
	l, r := readLayout(in, "")
	if l == nil || r.HasErrors() {
		rf.finish(r, stderr)
		return ExitError
	}
	pinned, pr := build.Pin(l, build.Options{ToolVersion: Version})
	r.Merge(pr)
	if pinned == nil {
		rf.finish(r, stderr)
		return ExitError
	}
	r.Merge(installer.Pin(pinned))
	data, wr := kbljson.Write(pinned)
	r.Merge(wr)
	if !rf.finish(r, stderr) {
		return ExitError
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "yakbd pin: %v\n", err)
		return ExitError
	}
	fmt.Fprintf(stdout, "%s: pinned\n", outPath)
	return ExitOK
}

// compileSet builds the DLL set and gives the compiler's codes the spec's loss kinds, under the file's policies.
// unverified allows the ARM64X and ARM64 candidates when asked for (windows.CompileSet).
func compileSet(l *model.Layout, d *windows.DLL, list []windows.Target, extra *model.OnLoss, unverified bool) *windows.BuildSet {
	set := windows.CompileSet(d, list, unverified)
	for i := range set.Report.Entries {
		e := &set.Report.Entries[i]
		switch e.Code {
		case "win.build.unsupported":
			e.Code, e.Class = "windows.architectureUnsupported", loss.Loss
		case "win.size":
			e.Code, e.Class = "windows.sizeLimit", loss.Loss
		}
	}
	policy.Resolve(set.Report, l, "windows", extra)
	return set
}
