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
	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows/wintarget"
)

// Version of the tool: the numbers (what DLL and installer version resources carry, which hold four numbers) and the
// pre-release suffix of a semantic version; VersionString joins them ("0.1.0-preview.3").
var Version = [4]uint16{0, 1, 0, 0}

// PreRelease is the semantic-version pre-release suffix after "-", or empty for a final release.
const PreRelease = "preview.3"

// VersionString is the tool's semantic version, major.minor.patch[-pre-release].
func VersionString() string {
	s := fmt.Sprintf("%d.%d.%d", Version[0], Version[1], Version[2])
	if PreRelease != "" {
		s += "-" + PreRelease
	}
	return s
}

// Release is the release version a release build writes here with -X (docs/release.md), for example
// "0.1.0-preview.3"; empty in every other build. `yakbd version` prints it when set.
var Release string

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1 // the input has errors, or the operation failed
	ExitUsage = 2
)

const usage = `usage: yakbd <command> [arguments]

commands:
  compile   build the installable files of a layout (.kbl.json, .klc or CLDR .xml) for a target; Windows: the layout DLLs
  installer write one installer with every build of a layout; Windows: one .exe
  convert   convert a layout between .kbl.json, .klc and CLDR .xml
  validate  check a layout file and, with -target, that it can be built for that target
  pin       write every default of a target into a .kbl.json file
  version   print the version

-target names the platform of compile, installer, pin and validate: windows (the default).
Run "yakbd <command> -h" for the arguments of a command.
`

// Run runs the command line args (without the program name) and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "compile", "installer", "pin", "validate":
		return targetCmd(args[0], args[1:], stdout, stderr)
	case "convert":
		return convert(args[1:], stdout, stderr)
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
	defaultsKind   string // the prefix -strict-defaults makes an error ("windowsDefault")
}

func (rf *reportFlags) register(fs *flag.FlagSet, defaultsKind string) {
	rf.command = fs.Name()
	rf.defaultsKind = defaultsKind
	fs.StringVar(&rf.jsonPath, "report", "", "also write the report as JSON to this file")
	fs.BoolVar(&rf.strictDefaults, "strict-defaults", false, fmt.Sprintf(`treat every choice taken from a default rule as an error ("%s.*": "error")`, defaultsKind))
	fs.BoolVar(&rf.quiet, "q", false, "print only errors and warnings")
}

// extra is the policy of -strict-defaults, nil without it.
func (rf *reportFlags) extra() *model.OnLoss {
	if rf.strictDefaults {
		return &model.OnLoss{Kinds: map[string]string{rf.defaultsKind + ".*": "error"}}
	}
	return nil
}

// extraWith adds the kinds of -accept to the command line's policy.
func (rf *reportFlags) extraWith(accept string) *model.OnLoss {
	if accept == "" {
		return rf.extra()
	}
	p := &model.OnLoss{Kinds: map[string]string{}}
	if base := rf.extra(); base != nil {
		for k, v := range base.Kinds {
			p.Kinds[k] = v
		}
	}
	for _, k := range strings.Split(accept, ",") {
		p.Kinds[strings.TrimSpace(k)] = "accept"
	}
	return p
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

// readLayout reads a layout in any input format into the model. testsPath, if not empty, names a CLDR keyboard
// test file whose tests are added.
func readLayout(path, testsPath string) (*model.Layout, *loss.Report) {
	r := &loss.Report{}
	b, err := os.ReadFile(path)
	if err != nil {
		r.Errorf("io.read", loss.Where{File: path}, "%v", err)
		return nil, r
	}
	f := formatByPath(path)
	if f == nil || f.read == nil {
		r.Errorf("io.format", loss.Where{File: path}, "unknown format (want %s)", extList(false))
		return nil, r
	}
	l, fr := f.read(b, filepath.Base(path))
	r.Merge(fr)
	if l == nil {
		return nil, r
	}
	if testsPath != "" {
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

func convert(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("convert", "input output", stderr)
	var rf reportFlags
	rf.register(fs, "windowsDefault") // a .klc output runs the Windows build
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
	if *langID != "" && !wintarget.HasLangID(l) {
		wintarget.SetLangID(l, *langID)
		r.Infof("cli.langid", loss.Where{}, "LANGID %s from -langid", *langID)
	}
	f := formatByPath(outPath)
	if f == nil || f.write == nil {
		fmt.Fprintf(stderr, "yakbd convert: unknown output format of %s (want %s)\n", outPath, extList(true))
		return ExitUsage
	}
	data, wr := f.write(l, writeOptions{extra: rf.extra(), msklc: *forMSKLC})
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

// pinOutput reports whether path can be pin's output.
func pinOutput(path string) bool { return formatOf(path) == "kbl" }

// writePinned writes the layout a pin made.
func writePinned(l *model.Layout, r *loss.Report) []byte {
	data, wr := kbljson.Write(l)
	r.Merge(wr)
	return data
}
