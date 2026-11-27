package cmdline

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// Asks reports whether the program asks "Install ...?" before it starts: full UI only.
func (c *Command) Asks() bool { return c.UI == UIFull }

// ShowsEnd reports whether the program shows its dialog at the end (success or failure): full UI, or the "+" of
// /qn+ and /qb+.
func (c *Command) ShowsEnd() bool { return c.UI == UIFull || c.EndDialog }

// MayPromptElevation reports whether the program may start itself again with administrator rights (the UAC
// prompt): any UI level but none.
func (c *Command) MayPromptElevation() bool { return c.UI != UINone }

// Restart is what the program does at the end about a restart.
type Restart int

// The restart decisions.
const (
	NoRestart   Restart = iota // nothing (exit code 3010 when one is needed)
	AskRestart                 // a Yes/No question; Yes restarts (1641), No gives 3010
	AutoRestart                // restart without asking (1641)
)

// RestartDecision applies REBOOT and REBOOTPROMPT to the end of a run (https://learn.microsoft.com/en-us/windows/win32/msi/reboot,
// .../standard-installer-command-line-options): needed is whether a file waits for the restart.
func (c *Command) RestartDecision(needed bool) Restart {
	switch {
	case c.Reboot == RebootSuppress:
		return NoRestart
	case !needed && c.Reboot != RebootForce:
		return NoRestart
	case c.UI == UINone || c.Prompt == PromptSuppress:
		return AutoRestart
	}
	return AskRestart
}

// ChildArgs are the arguments of the elevated copy of the program (docs/installer.md §3): no UI, no restart (the
// first process decides), the log appended, the notes into result.
func (c *Command) ChildArgs(result string) []string {
	a := []string{"/elevated", "/qn", "/norestart", "/result", result}
	switch c.Action {
	case Uninstall:
		a = append(a, "/x")
	case Repair:
		a = append(a, "/f"+c.RepairMode)
	}
	if c.Log != "" {
		a = append(a, "/l*+", c.Log)
	}
	if c.Force {
		a = append(a, "FORCE=1")
	}
	if c.SignInSet && c.Action != Uninstall {
		a = append(a, map[bool]string{true: "SIGNIN=1", false: "SIGNIN=0"}[c.SignIn])
	}
	return a
}

// IsProductCode reports whether s is a product code: a GUID in braces.
func IsProductCode(s string) bool {
	if len(s) != 38 || s[0] != '{' || s[37] != '}' {
		return false
	}
	for i, r := range s[1:37] {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

// CheckOperand decides whether the package or product code given on the command line is this program's
// (docs/installer-cli.md §2): "" and the program's own path are; a product code must be the program's
// (UnknownProduct otherwise, as msiexec gives for a product code it does not know); another file is accepted for
// an uninstall when it is an installer of the same product (productOf reads its product code), and refused
// with PackageOpenFailed otherwise.
func (c *Command) CheckOperand(self, product string, productOf func(path string) (string, error)) *Error {
	op := c.Operand
	switch {
	case op == "" || samePath(op, self):
		return nil
	case IsProductCode(op):
		if !strings.EqualFold(op, product) {
			return refuse(UnknownProduct, "%s is not the product of this installer (%s)", op, product)
		}
		return nil
	}
	code, err := productOf(op)
	if err != nil {
		return refuse(PackageOpenFailed, "%s cannot be opened as a yakbd installer: %v", op, err)
	}
	if !strings.EqualFold(code, product) {
		return refuse(PackageOpenFailed, "%s is the installer of another product (%s); run it itself", op, code)
	}
	if c.Action != Uninstall {
		return refuse(PackageOpenFailed, "%s is another copy of this product's installer; run that file itself to install or repair from it", op)
	}
	return nil
}

func samePath(a, b string) bool {
	n := func(p string) string {
		return strings.ToLower(path.Clean(strings.ReplaceAll(strings.Trim(p, `"`), `\`, "/")))
	}
	return n(a) == n(b)
}

// OpenLog opens the log: the /l file (replaced unless "+"; a file that cannot be opened is LogFailure, as msiexec
// does not create folders either), else def, appended, and silently nothing when def cannot be opened (the
// MsiLogging analogue, docs/installer-cli.md §5). The elevated copy always appends.
func (c *Command) OpenLog(def string) (*os.File, string, *Error) {
	if c.Log == "" {
		f, err := os.OpenFile(def, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, def, nil
		}
		return f, def, nil
	}
	// Replaced once, then always appended to: the elevated copy writes into the same file meanwhile.
	if !c.LogAppend && !c.Elevated {
		if t, err := os.OpenFile(c.Log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644); err == nil {
			t.Close()
		}
	}
	f, err := os.OpenFile(c.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, c.Log, refuse(LogFailure, "the log file %s cannot be opened: %v", c.Log, err)
	}
	return f, c.Log, nil
}

// CompareVersions compares two versions "a.b.c[.d]" by their first three fields, as Windows Installer compares
// ProductVersion for upgrades (https://learn.microsoft.com/en-us/windows/win32/msi/major-upgrades): -1, 0 or 1.
// A field counts by its leading digits ("0-preview" is 0, "2-rc" is 2), a missing or non-numeric one as 0: a
// semantic-version pre-release suffix is ignored, so 0.1.0-preview.1, 0.1.0-preview.2 and 0.1.0 are the same
// version (an install over another repairs in place; docs/installer-cli.md §6).
func CompareVersions(a, b string) int {
	fa, fb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, y := field(fa, i), field(fb, i)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func field(f []string, i int) uint64 {
	if i >= len(f) {
		return 0
	}
	s := strings.TrimSpace(f[i])
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	n, _ := strconv.ParseUint(s[:j], 10, 32)
	return n
}

// Usage is the /? text of a program.
func Usage(program string) string {
	return fmt.Sprintf(`usage (msiexec's command line; options are case-insensitive, "/" or "-"):
  %[1]s [/i | /package] [/quiet | /passive | /q[n|b|r|f][+|-|!]] [/norestart | /forcerestart | /promptrestart]
        [/l[iwearucmopvx*+!] file | /log file] [PROPERTY=value ...]       install, upgrade, or repair the same version
  %[1]s /x | /uninstall [{product code} | package] [options]               uninstall
  %[1]s /f[podecamusv] [{product code} | package] [options]                repair
properties: ENABLE=0|1, SIGNIN=0|1, FORCE=1, REBOOT=ReallySuppress|Suppress|Force, REBOOTPROMPT=S, ALLUSERS=1|2,
  REINSTALL=ALL, REINSTALLMODE=..., REMOVE=ALL; others are accepted and ignored
exit codes: 0 done, 3010 done (restart to finish), 1641 done (restart started), 1602 cancelled, 1603 failed
  (nothing changed), 1605 not installed, 1618 another yakbd installer is running, 1620 installer damaged,
  1622 log file, 1633 no build for this machine, 1638 a newer version is installed, 1639 bad command line,
  740 needs administrator rights without UI`, program)
}
