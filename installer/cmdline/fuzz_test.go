package cmdline

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// FuzzParse: the installer's command-line parser and the decisions it feeds keep the bounds of docs/fuzzing.md on
// any arguments (the input is the argument list joined by NUL, which a Windows command line cannot contain). A
// command line it accepts gives the elevated copy arguments that parse to the same action, repair mode (of a
// repair), log, FORCE and SIGNIN (of an install or repair). Nothing here touches the file system: OpenLog is never called and CheckOperand gets a fake lookup.
// Seeds: every example row of docs/installer-cli.md.
func FuzzParse(f *testing.F) {
	row := regexp.MustCompile("^\\| `([^`]*)` \\| (.+?) \\|$")
	n := 0
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "installer-cli.md"))
	if err != nil {
		f.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if m := row.FindStringSubmatch(l); m != nil {
			f.Add(strings.Join(splitArgs(m[1]), "\x00"))
			n++
		}
	}
	if n == 0 {
		f.Fatal("no examples in docs/installer-cli.md")
	}
	f.Fuzz(func(t *testing.T, s string) {
		var args []string
		if s != "" {
			args = strings.Split(s, "\x00")
		}
		fuzzcheck.Check(t, fuzzcheck.Default, len(s), func() error {
			c, err := Parse(args)
			if err != nil {
				if _, ok := err.(*Error); !ok {
					return fmt.Errorf("an error of type %T, not *Error: %v", err, err)
				}
				return nil
			}
			c.Asks()
			c.MayPromptElevation()
			c.ShowsEnd()
			c.RestartDecision(true)
			c.RestartDecision(false)
			const code = "{01234567-89AB-CDEF-0123-456789ABCDEF}"
			c.CheckOperand(`C:\p\`+code+`\setup.exe`, code, func(p string) (string, error) {
				if strings.HasSuffix(strings.ToLower(p), ".exe") {
					return code, nil
				}
				return "", fmt.Errorf("not an installer")
			})
			for _, a := range args {
				IsProductCode(a)
				CompareVersions(a, "1.2.3")
			}
			if c.Action != Install && c.Action != Uninstall && c.Action != Repair {
				return nil
			}
			child, err := Parse(c.ChildArgs(`C:\t\r.txt`))
			if err != nil {
				return fmt.Errorf("the elevated copy's arguments %q do not parse: %v", c.ChildArgs(`C:\t\r.txt`), err)
			}
			if !child.Elevated || child.Result != `C:\t\r.txt` || child.Action != c.Action ||
				c.Action == Repair && child.RepairMode != c.RepairMode || child.Log != c.Log || child.Force != c.Force ||
				c.SignInSet && c.Action != Uninstall && child.SignIn != c.SignIn {
				return fmt.Errorf("the elevated copy's arguments %q parse to %+v, from %+v", c.ChildArgs(`C:\t\r.txt`), child, c)
			}
			return nil
		})
	})
}
