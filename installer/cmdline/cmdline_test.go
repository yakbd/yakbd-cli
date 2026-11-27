package cmdline

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// doc reads docs/installer-cli.md, the contract the tests are driven from.
func doc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "installer-cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the text of "## <n>." up to the next "## ".
func section(t *testing.T, d string, n int) string {
	t.Helper()
	i := strings.Index(d, fmt.Sprintf("\n## %d. ", n))
	if i < 0 {
		t.Fatalf("no section %d", n)
	}
	rest := d[i+1:]
	if j := strings.Index(rest[3:], "\n## "); j >= 0 {
		rest = rest[:j+3]
	}
	return rest
}

// splitArgs splits a command line at spaces, honouring double quotes (enough for the examples).
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	in, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			in, have = !in, true
		case r == ' ' && !in:
			if have {
				out = append(out, cur.String())
			}
			cur.Reset()
			have = false
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

func describe(c *Command) string {
	w := []string{c.Action.String()}
	if c.Operand != "" {
		w = append(w, "op="+c.Operand)
	}
	if c.UI != UIFull {
		w = append(w, "ui="+c.UI.String())
	}
	if c.EndDialog {
		w = append(w, "end")
	}
	switch c.Reboot {
	case RebootSuppress:
		w = append(w, "reboot=suppress")
	case RebootForce:
		w = append(w, "reboot=force")
	}
	switch c.Prompt {
	case PromptSuppress:
		w = append(w, "prompt=suppress")
	case PromptAlways:
		w = append(w, "prompt=always")
	}
	if c.Log != "" {
		w = append(w, "log="+c.Log)
	}
	if c.LogFlags != "" {
		w = append(w, "flags="+c.LogFlags)
	}
	if c.LogAppend {
		w = append(w, "append")
	}
	if c.RepairMode != "" {
		w = append(w, "mode="+c.RepairMode)
	}
	if c.EnableSet {
		e := "0"
		if c.Enable {
			e = "1"
		}
		w = append(w, "enable="+e)
	}
	if c.SignInSet {
		w = append(w, map[bool]string{true: "signin=1", false: "signin=0"}[c.SignIn])
	}
	if c.Force {
		w = append(w, "force")
	}
	return strings.Join(w, " ")
}

// TestExamplesFromDoc runs every row of docs/installer-cli.md §13.
func TestExamplesFromDoc(t *testing.T) {
	row := regexp.MustCompile("^\\| `([^`]*)` \\| (.+?) \\|$")
	n := 0
	for _, l := range strings.Split(section(t, doc(t), 13), "\n") {
		m := row.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n++
		args, want := splitArgs(m[1]), m[2]
		c, err := Parse(args)
		got := ""
		if err != nil {
			e, ok := err.(*Error)
			if !ok {
				t.Fatalf("%q: error of type %T", m[1], err)
			}
			got = "exit " + strconv.Itoa(e.Code)
		} else {
			got = describe(c)
		}
		if got != want {
			t.Errorf("%q: got %q, want %q (%v)", m[1], got, want, err)
		}
	}
	if n < 50 {
		t.Errorf("only %d examples read from the doc", n)
	}
}

// TestCodesInDoc checks that every exit code of this package has its row in §4 with its Windows name, and that
// the table has no code the package lacks.
func TestCodesInDoc(t *testing.T) {
	codes := map[int]string{
		Success: "ERROR_SUCCESS", ElevationRequired: "ERROR_ELEVATION_REQUIRED", UserExit: "ERROR_INSTALL_USEREXIT",
		InstallFailure: "ERROR_INSTALL_FAILURE", UnknownProduct: "ERROR_UNKNOWN_PRODUCT",
		AlreadyRunning: "ERROR_INSTALL_ALREADY_RUNNING", PackageOpenFailed: "ERROR_INSTALL_PACKAGE_OPEN_FAILED",
		PackageInvalid: "ERROR_INSTALL_PACKAGE_INVALID", LogFailure: "ERROR_INSTALL_LOG_FAILURE",
		TransformFailure: "ERROR_INSTALL_TRANSFORM_FAILURE", PlatformUnsupported: "ERROR_INSTALL_PLATFORM_UNSUPPORTED",
		PatchOpenFailed: "ERROR_PATCH_PACKAGE_OPEN_FAILED", ProductVersion: "ERROR_PRODUCT_VERSION",
		InvalidCommandLine: "ERROR_INVALID_COMMAND_LINE", RebootInitiated: "ERROR_SUCCESS_REBOOT_INITIATED",
		RebootRequired: "ERROR_SUCCESS_REBOOT_REQUIRED",
	}
	row := regexp.MustCompile(`^\| (\d+) \| (ERROR_[A-Z_]+) \|`)
	seen := map[int]bool{}
	s := section(t, doc(t), 4)
	for _, l := range strings.Split(s, "\n") {
		m := row.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		c, _ := strconv.Atoi(m[1])
		if codes[c] != m[2] {
			t.Errorf("doc row %d %s: the package has %q", c, m[2], codes[c])
		}
		seen[c] = true
	}
	for c, name := range codes {
		if !seen[c] {
			t.Errorf("code %d %s has no row in §4", c, name)
		}
	}
	for _, c := range []int{0, 1641, 3010} {
		if !Succeeded(c) {
			t.Errorf("%d is success", c)
		}
	}
	for c := range codes {
		if c != 0 && c != 1641 && c != 3010 && Succeeded(c) {
			t.Errorf("%d is not success", c)
		}
	}
}

// TestRestartTable checks §5's restart table.
func TestRestartTable(t *testing.T) {
	for _, tc := range []struct {
		args   string
		needed bool
		want   Restart
	}{
		{"/quiet /norestart", true, NoRestart},
		{"/norestart", true, NoRestart},
		{"REBOOT=Suppress /qn", true, NoRestart},
		{"/quiet", true, AutoRestart},
		{"/passive", true, AutoRestart},
		{"/qb", true, AskRestart},
		{"", true, AskRestart},
		{"/quiet", false, NoRestart},
		{"", false, NoRestart},
		{"/forcerestart /quiet", false, AutoRestart},
		{"/forcerestart", false, AskRestart},
		{"/forcerestart /passive", true, AutoRestart},
		{"/promptrestart /qb", true, AskRestart},
	} {
		c, err := Parse(splitArgs(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if got := c.RestartDecision(tc.needed); got != tc.want {
			t.Errorf("%q needed=%v: %d, want %d", tc.args, tc.needed, got, tc.want)
		}
	}
}

// TestUITable checks §5's UI table.
func TestUITable(t *testing.T) {
	for _, tc := range []struct {
		args           string
		ask, end, elev bool
	}{
		{"", true, true, true}, {"/qf", true, true, true}, {"/qr", false, false, true}, {"/qb", false, false, true},
		{"/passive", false, false, true}, {"/qb+", false, true, true}, {"/quiet", false, false, false},
		{"/qn+", false, true, false},
	} {
		c, err := Parse(splitArgs(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if c.Asks() != tc.ask || c.ShowsEnd() != tc.end || c.MayPromptElevation() != tc.elev {
			t.Errorf("%q: ask %v end %v elevation %v", tc.args, c.Asks(), c.ShowsEnd(), c.MayPromptElevation())
		}
	}
}

// TestOperand checks §1's operand table.
func TestOperand(t *testing.T) {
	const code = "{01234567-89AB-CDEF-0123-456789ABCDEF}"
	self := `C:\Program Files (x86)\YAKBD\` + code + `\setup.exe`
	files := map[string]string{`c:\dl\same.exe`: code, `c:\dl\other.exe`: "{11111111-2222-3333-4444-555555555555}"}
	productOf := func(p string) (string, error) {
		if c, ok := files[strings.ToLower(p)]; ok {
			return c, nil
		}
		return "", fmt.Errorf("not a yakbd installer")
	}
	for _, tc := range []struct {
		args string
		want int
	}{
		{"/x", 0}, {"/x " + strings.ToLower(code), 0}, {`/x "` + strings.ToUpper(self) + `"`, 0},
		{"/x {11111111-2222-3333-4444-555555555555}", UnknownProduct}, {"/i {11111111-2222-3333-4444-555555555555}", UnknownProduct},
		{`/x C:\dl\same.exe`, 0}, {`/i C:\dl\same.exe`, PackageOpenFailed}, {`/f C:\dl\same.exe`, PackageOpenFailed},
		{`/x C:\dl\other.exe`, PackageOpenFailed}, {`/x C:\dl\missing.msi`, PackageOpenFailed},
	} {
		c, err := Parse(splitArgs(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		if e := c.CheckOperand(self, code, productOf); e != nil {
			got = e.Code
		}
		if got != tc.want {
			t.Errorf("%s: %d, want %d", tc.args, got, tc.want)
		}
	}
}

func TestChildArgs(t *testing.T) {
	c, err := Parse(splitArgs(`/x /quiet /l*v "C:\l\x.log" FORCE=1 /forcerestart`))
	if err != nil {
		t.Fatal(err)
	}
	child, err := Parse(c.ChildArgs(`C:\t\r.txt`))
	if err != nil {
		t.Fatal(err)
	}
	if !child.Elevated || child.Action != Uninstall || child.UI != UINone || child.Reboot != RebootSuppress ||
		child.Log != `C:\l\x.log` || !child.LogAppend || !child.Force || child.Result != `C:\t\r.txt` {
		t.Errorf("%+v", child)
	}
	s, _ := Parse([]string{"/f", "SIGNIN=0"})
	if ch, err := Parse(s.ChildArgs("r")); err != nil || !ch.SignInSet || ch.SignIn {
		t.Errorf("SIGNIN=0 not passed to the elevated copy: %+v %v", ch, err)
	}
	x, _ := Parse([]string{"/x", "SIGNIN=1"})
	if ch, _ := Parse(x.ChildArgs("r")); ch.SignInSet {
		t.Errorf("SIGNIN passed to an uninstall: %+v", ch)
	}
	r, _ := Parse([]string{"/fvomus"})
	if ch, err := Parse(r.ChildArgs("r")); err != nil || ch.Action != Repair || ch.RepairMode != "vomus" {
		t.Errorf("%+v %v", ch, err)
	}
}

func TestOpenLog(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"/l*v", p}, "new\n"},
		{[]string{"/l*v+", p}, "new\nnew\n"},
		{[]string{"/elevated", "/l*v", p}, "new\nnew\nnew\n"},
	} {
		c, _ := Parse(tc.args)
		f, _, e := c.OpenLog(filepath.Join(dir, "default.log"))
		if e != nil {
			t.Fatal(e)
		}
		f.WriteString("new\n")
		f.Close()
		if b, _ := os.ReadFile(p); string(b) != tc.want {
			t.Errorf("%v: %q", tc.args, b)
		}
	}
	c, _ := Parse([]string{"/l*v", filepath.Join(dir, "no", "such", "x.log")})
	if _, _, e := c.OpenLog("d"); e == nil || e.Code != LogFailure {
		t.Errorf("missing folder: %v", e)
	}
	c, _ = Parse(nil)
	f, path, e := c.OpenLog(filepath.Join(dir, "default.log"))
	if e != nil || f == nil || path != filepath.Join(dir, "default.log") {
		t.Errorf("default: %v %v", path, e)
	}
	f.Close()
	if f, _, e := c.OpenLog(filepath.Join(dir, "no", "default.log")); e != nil || f != nil {
		t.Error("a default log that cannot be opened is no error and no file")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.0.0.0", "1.0.0.0", 0}, {"1.0.0.9", "1.0.0.0", 0}, {"1.2", "1.2.0", 0}, {"1.10.0", "1.9.0", 1},
		{"0.2.0", "0.3.0", -1}, {"2", "10", -1}, {"", "0.0.1", -1},
		{"0.1.0-preview.1", "0.1.0-preview.2", 0}, {"0.1.0-preview.1", "0.1.0", 0}, {"0.1.2-rc.1", "0.1.1", 1},
		{"0.1.0-preview.1", "0.2.0-preview.1", -1}, {"0.1.0-preview.1", "0.1.0.0", 0},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("%s vs %s: %d", tc.a, tc.b, got)
		}
	}
}

func TestIsProductCode(t *testing.T) {
	if !IsProductCode("{01234567-89ab-CDEF-0123-456789ABCDEF}") || IsProductCode("01234567-89AB-CDEF-0123-456789ABCDEF") ||
		IsProductCode("{01234567-89AB-CDEF-0123-456789ABCDEG}") || IsProductCode("{0123456789AB-CDEF-0123-456789ABCDEF-}") {
		t.Error("IsProductCode")
	}
}

// TestLogParentAndChild: the first process replaces the /l file, the elevated copy appends, and the first
// process's later lines go after the copy's (docs/installer-cli.md §7).
func TestLogParentAndChild(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	_ = os.WriteFile(p, []byte("old\n"), 0o644)
	parent, _ := Parse([]string{"/log", p})
	pf, _, e := parent.OpenLog("d")
	if e != nil {
		t.Fatal(e)
	}
	pf.WriteString("parent 1\n")
	child, _ := Parse(parent.ChildArgs("r"))
	cf, _, e := child.OpenLog("d")
	if e != nil {
		t.Fatal(e)
	}
	cf.WriteString("child\n")
	cf.Close()
	pf.WriteString("parent 2\n")
	pf.Close()
	if b, _ := os.ReadFile(p); string(b) != "parent 1\nchild\nparent 2\n" {
		t.Errorf("%q", b)
	}
}
