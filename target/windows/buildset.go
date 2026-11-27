package windows

import (
	"fmt"

	"github.com/yakbd/yakbd-cli/loss"
)

// Target names one build of a layout. The supported ones are the architectures; others are planned.
type Target string

// Targets of a full build set, in the order they are built. ARM64X is listed so that a full set says it is
// missing: it is built only when asked for until a Windows test has passed (dll-format §1.7, §11 item 9;
// docs/arm64x.md).
const (
	TargetAMD64  Target = "amd64"
	TargetWOW64  Target = "wow64"
	TargetX86    Target = "x86"
	TargetARM64X Target = "arm64x"
	// TargetARM64 is a plain ARM64 file, built only when asked for: the control of the ARM64 tests (x64
	// processes cannot load it, installation §2.4).
	TargetARM64 Target = "arm64"
)

// AllTargets is the default set of one compile call.
var AllTargets = []Target{TargetAMD64, TargetWOW64, TargetX86, TargetARM64X}

// Build is one built file of a build set.
type Build struct {
	Target Target
	Arch   Arch
	// Dir is the system directory the file is installed into on a machine of the matching kind
	// (installation topic): System32 for the native file, SysWOW64 for the WOW64 file on 64-bit Windows,
	// System32 for the x86 file on 32-bit Windows.
	Dir      string
	FileName string
	Bytes    []byte
}

// BuildSet is the result of compiling one layout for a set of targets: every file an installer needs, and one
// report for the whole set. The installer package of the next round consumes it.
type BuildSet struct {
	Builds []Build
	// Unsupported lists the requested targets that the compiler cannot build yet.
	Unsupported []Target
	Report      *loss.Report
}

// ParseTarget parses a target name.
func ParseTarget(s string) (Target, error) {
	for _, t := range append(AllTargets, TargetARM64) {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown target %q (want amd64, wow64, x86, arm64x or arm64)", s)
}

// CompileAll builds d for every target in targets (AllTargets when empty). A target that cannot be built yet
// is reported as a warning in a full set and as an error when it was asked for alone. Entries of the
// validation, which do not depend on the target, appear once.
func CompileAll(d *DLL, targets []Target) *BuildSet { return CompileSet(d, targets, false) }

// CompileSet is CompileAll; with unverified, the ARM64X and ARM64 builds that have not passed a Windows test yet
// are built when asked for (`yakbd compile -target arm64x`, the ARM64 test bundle) instead of being unsupported.
func CompileSet(d *DLL, targets []Target, unverified bool) *BuildSet {
	explicit := len(targets) > 0
	if !explicit {
		targets = AllTargets
	}
	set := &BuildSet{Report: &loss.Report{}}
	validated := false
	for _, t := range targets {
		var a Arch
		var dir string
		switch t {
		case TargetAMD64:
			a, dir = AMD64, "System32"
		case TargetWOW64:
			a, dir = WOW64, "SysWOW64"
		case TargetX86:
			a, dir = X86, "System32"
		case TargetARM64X:
			// The shape ShippedVariant passed the gate of kbl D44 (VM-6, D66; docs/arm64x.md §5).
			a, dir = ARM64X, "System32"
		case TargetARM64:
			if explicit && unverified {
				// The plain ARM64 control of the tests: x64 processes cannot load it (installation §2.4).
				a, dir = ARM64, "System32"
				set.Report.Infof("win.build.unverified", loss.Where{Path: string(t)},
					"the plain arm64 build is a test control: x64 processes cannot load it (docs/arm64x.md)")
				break
			}
			fallthrough
		default:
			set.Unsupported = append(set.Unsupported, t)
			sev := loss.Warning
			if explicit {
				sev = loss.Error
			}
			set.Report.Lost(sev, "win.build.unsupported", loss.Where{Path: string(t)}, "target %s is not supported yet; no file is built", t)
			continue
		}
		b, r := Compile(d, a)
		if !validated {
			set.Report.Merge(r)
			validated = true
		} else {
			for _, e := range r.Entries {
				if e.Severity == loss.Error {
					set.Report.Entries = append(set.Report.Entries, e)
				}
			}
		}
		if b != nil {
			set.Builds = append(set.Builds, Build{Target: t, Arch: a, Dir: dir, FileName: d.FileName, Bytes: b})
		}
	}
	return set
}
