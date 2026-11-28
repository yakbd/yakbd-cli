// Package target is the interface between the command line and the platforms a layout is built for: Windows
// today, macOS and XKB next. A platform is one package under target/ that implements Target; target/registry
// lists them, one line each (docs/targets.md). Loss and policy code stays shared (packages loss and policy).
package target

import (
	"flag"
	"sort"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// Target is one platform. The command line asks it for a command by name ("compile", "installer", "pin",
// "validate") and runs what it returns.
type Target interface {
	// Name is the value of -target ("windows").
	Name() string
	// DefaultsKind is the prefix of the kinds of its defaulted entries ("windowsDefault"): -strict-defaults makes
	// "<DefaultsKind>.*" an error.
	DefaultsKind() string
	// Command adds the target's own flags of the command to fs, before the command line is parsed, and returns
	// the command; nil when the target has no such command.
	Command(name string, fs *flag.FlagSet) Command
}

// Command is one command of a target, its flags already registered.
type Command interface {
	// Check runs after the flags are parsed and before the layout is read; an error is a usage error (exit code
	// 2, no report file).
	Check() error
	// Run does the work on a layout read without errors. It adds its entries to j.Report, under the file's
	// policies and j.Policy; any error entry means exit code 1 and no file is written.
	Run(j *Job) *Result
}

// Job is what the command line hands a command.
type Job struct {
	Layout *model.Layout
	Report *loss.Report
	// Policy is the command line's policy (-strict-defaults, -accept), applied after the file's; nil for none.
	Policy *model.OnLoss
	// StrictPolicy is -strict-defaults alone (without -accept), what compile applies to its build set.
	StrictPolicy *model.OnLoss
	// ToolVersion is the tool's version in four numbers, ToolName "yakbd <version>".
	ToolVersion [4]uint16
	ToolName    string
}

// Result is what a command made. A nil Result means nothing was made (the report says why).
type Result struct {
	// Files are written in order, each followed by its Line on stdout.
	Files []File
	// Lines are printed on stdout after the files.
	Lines []string
	// Layout is the layout a pin made, written by the command line as .kbl.json.
	Layout *model.Layout
	// Fail, when not empty, is printed after the report ("yakbd <command>: <Fail>") and the command exits 1.
	Fail string
}

// File is one output file.
type File struct {
	Path string
	Data []byte
	// MkDir creates the file's directory first.
	MkDir bool
	// Line is printed on stdout once the file is written; empty for none.
	Line string
}

// Names returns the names of ts, sorted.
func Names(ts []Target) []string {
	var n []string
	for _, t := range ts {
		n = append(n, t.Name())
	}
	sort.Strings(n)
	return n
}

// Lookup returns the target of ts called name.
func Lookup(ts []Target, name string) (Target, bool) {
	for _, t := range ts {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}
