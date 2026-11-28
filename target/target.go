// Package target is the interface between the command line and the platforms a layout is built for: Windows
// today, macOS and XKB next. A platform is one package under target/ that implements Target; target/registry
// lists them, one line each (docs/targets.md). Loss and policy code stays shared (packages loss and policy).
package target

import (
	"errors"
	"flag"
	"fmt"
	"os"
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

// Layers says which layers of a file a target builds from (docs/targets.md §5).
type Layers int

const (
	// Hardware: the hardware layers. Every target without LayerSource (Windows, macOS, XKB); a file without
	// hardware layers is the target's own loss (<name>.noHardwareLayers).
	Hardware Layers = iota
	// Touch: the touch layers only (SPEC §9.4); a file without them is the target's own loss.
	Touch
	// TouchOrDerived: the touch layers; for a file without any, touch layers the target derives from the hardware
	// layers by its default rule, reported as <DefaultsKind>.touchFromHardware.
	TouchOrDerived
)

// LayerSource is the optional interface of a target that builds from touch layers (an on-screen keyboard).
type LayerSource interface {
	Layers() Layers
}

// LayersOf returns what t builds from: Hardware unless t implements LayerSource.
func LayersOf(t Target) Layers {
	if s, ok := t.(LayerSource); ok {
		return s.Layers()
	}
	return Hardware
}

// DeriveTouch reports whether a TouchOrDerived target must derive touch layers for l: l has hardware layers
// and no touch layers.
func DeriveTouch(t Target, l *model.Layout) bool {
	return LayersOf(t) == TouchOrDerived && len(l.Touch) == 0 && l.Hardware != nil
}

// RuntimeFlag registers -runtime on fs: the prebuilt runtime a target builds on (an APK for androidime, a
// source folder for ios; docs/targets.md §5). what says which, for the help text. The command's Check calls
// CheckRuntime.
func RuntimeFlag(fs *flag.FlagSet, what string) *string {
	return fs.String("runtime", "", "the runtime to build on: "+what+" (required)")
}

// CheckRuntime is the usage check of -runtime: given, and an existing file (dir false) or folder (dir true).
func CheckRuntime(path string, dir bool) error {
	if path == "" {
		return errors.New("-runtime is required: the runtime this target builds on")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("-runtime: %v", err)
	}
	if fi.IsDir() != dir {
		if dir {
			return fmt.Errorf("-runtime %s: not a folder", path)
		}
		return fmt.Errorf("-runtime %s: not a file", path)
	}
	return nil
}
