package main

import (
	"fmt"
	"strings"

	"github.com/yakbd/yakbd-cli/internal/wincheck"
)

// The known-good control (-control): Windows' own US layout file, copied inside the guest from the system
// directory under the runner's test registration and loaded the same way as the candidates. A FAIL of
// control.<process> means that runner or that process kind cannot load layouts at all, so its other results say
// nothing about the candidates. Nothing of Windows' file goes into a result: the control reports PASS or FAIL
// with line counts only, never the characters typed.
const (
	// ControlDLL is the file name in the system directory (System32; SysWOW64 for a 32-bit process).
	ControlDLL = "kbdus.dll"
	// controlExpect is the bundle file with the keys typed (US layout: a, A, 1, !, space, period).
	controlExpect = "control/us.expect.txt"
)

func isControl(e wincheck.Entry) bool { return strings.HasPrefix(e.Name, "control.") }

// redact keeps only counts of a control's outcome.
func redact(o wincheck.Outcome) wincheck.Outcome {
	if len(o.Diffs) > 0 {
		o.Err = fmt.Sprintf("%d of %d lines differ", len(o.Diffs), o.Lines)
		o.Diffs = nil
	}
	return o
}
