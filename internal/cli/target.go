package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/target"
	"github.com/yakbd/yakbd-cli/target/registry"
	"github.com/yakbd/yakbd-cli/target/windows/wintarget"
)

// The commands that build for a target: compile, installer, pin and validate (docs/targets.md). -target picks the
// target (registry.Default without it); the target adds its own flags and does the work, the command line reads
// the layout, prints the report and writes the files.

var targetArgs = map[string]string{
	"compile":   "layout",
	"installer": "layout",
	"pin":       "layout [output.kbl.json]",
	"validate":  "layout",
}

// findTarget finds -target (and validate's -windows) in args before they are parsed: the target's own flags must
// be registered first. It returns the target's name ("" for validate without either: no build check) and the
// arguments to parse. A compile -target whose value lists Windows builds (x64,wow64: its meaning before targets)
// still means -target windows -build <list>; the arguments then carry -build.
func findTarget(cmd string, args []string) (string, []string, error) {
	name, at, inline := "", -1, false
	windows := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		n, v, hasV := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		switch {
		case n == "target":
			if !hasV {
				if i+1 >= len(args) {
					continue // the flag package reports the missing value
				}
				i++
				v = args[i]
			}
			name, at, inline = v, i, hasV
		case n == "windows" && cmd == "validate":
			b, err := strconv.ParseBool(v)
			windows = !hasV || (err == nil && b)
		}
	}
	if at < 0 {
		if cmd == "validate" && !windows {
			return "", args, nil
		}
		return registry.Default, args, nil
	}
	if _, ok := registry.Lookup(name); ok {
		if windows && name != "windows" {
			return "", nil, fmt.Errorf("-windows and -target %s disagree", name)
		}
		return name, args, nil
	}
	if cmd == "compile" {
		for _, s := range strings.Split(name, ",") {
			if wintarget.IsBuildName(s) {
				out := append([]string(nil), args...)
				if inline {
					out[at] = "-build=" + name
				} else {
					out[at-1] = "-build"
				}
				return "windows", out, nil
			}
		}
		return "", nil, fmt.Errorf("unknown target %q (known: %s; a list of Windows builds such as x64,wow64 means -target windows -build <list>)",
			name, strings.Join(registry.Names(), ", "))
	}
	return "", nil, fmt.Errorf("unknown target %q (known: %s)", name, strings.Join(registry.Names(), ", "))
}

func targetCmd(cmd string, args []string, stdout, stderr io.Writer) int {
	tname, args, err := findTarget(cmd, args)
	if err != nil {
		fmt.Fprintf(stderr, "yakbd %s: %v\n", cmd, err)
		return ExitUsage
	}
	var t target.Target
	if tname != "" {
		t, _ = registry.Lookup(tname)
	}
	fs := newFlags(cmd, targetArgs[cmd], stderr)
	var rf reportFlags
	kind := "windowsDefault"
	if t != nil {
		kind = t.DefaultsKind()
	}
	rf.register(fs, kind)
	var accept *string
	switch cmd {
	case "validate":
		fs.String("target", "", "also check that the layout builds for this target ("+strings.Join(registry.Names(), ", ")+"): the build's report")
		fs.Bool("windows", false, "the same as -target windows")
	default:
		help := "the target to build for: " + strings.Join(registry.Names(), ", ")
		if cmd == "compile" {
			help += "; a list of Windows builds (x64,wow64) still means -target windows -build <list>"
		}
		fs.String("target", registry.Default, help)
	}
	if cmd == "compile" || cmd == "installer" {
		accept = fs.String("accept", "", "comma-separated loss kinds (or prefix.*) to accept for this run")
	}
	var c target.Command
	if t != nil {
		if c = t.Command(cmd, fs); c == nil {
			fmt.Fprintf(stderr, "yakbd %s: the target %s has no %s command\n", cmd, tname, cmd)
			return ExitUsage
		}
	}
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return ExitUsage
	}
	if len(pos) < 1 || len(pos) > 1 && cmd != "pin" || len(pos) > 2 {
		fs.Usage()
		return ExitUsage
	}
	outPath := pos[0]
	if cmd == "pin" {
		if len(pos) == 2 {
			outPath = pos[1]
		}
		if !pinOutput(outPath) {
			fmt.Fprintln(stderr, "yakbd pin: the output must be a .kbl.json file")
			return ExitUsage
		}
	}
	if c != nil {
		if err := c.Check(); err != nil {
			fmt.Fprintf(stderr, "yakbd %s: %v\n", cmd, err)
			return ExitUsage
		}
	}
	rf.input = pos[0]
	l, r := readLayout(pos[0], "")
	if l == nil || r.HasErrors() {
		rf.finish(r, stderr)
		return ExitError
	}
	var res *target.Result
	if c != nil {
		policy := rf.extra()
		if accept != nil {
			policy = rf.extraWith(*accept)
		}
		res = c.Run(&target.Job{Layout: l, Report: r, Policy: policy, StrictPolicy: rf.extra(), ToolVersion: Version,
			ToolName: "yakbd " + VersionString()})
	}
	var pinned []byte
	if cmd == "pin" {
		if res == nil || res.Layout == nil {
			rf.finish(r, stderr)
			return ExitError
		}
		pinned = writePinned(res.Layout, r)
	}
	if !rf.finish(r, stderr) {
		return ExitError
	}
	switch cmd {
	case "validate":
		fmt.Fprintf(stdout, "%s: valid\n", pos[0])
		return ExitOK
	case "pin":
		if err := os.WriteFile(outPath, pinned, 0o644); err != nil {
			fmt.Fprintf(stderr, "yakbd pin: %v\n", err)
			return ExitError
		}
		fmt.Fprintf(stdout, "%s: pinned\n", outPath)
		return ExitOK
	}
	if res == nil {
		return ExitError
	}
	if res.Fail != "" {
		fmt.Fprintf(stderr, "yakbd %s: %s\n", cmd, res.Fail)
		return ExitError
	}
	for _, f := range res.Files {
		if f.MkDir {
			if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
				fmt.Fprintf(stderr, "yakbd %s: %v\n", cmd, err)
				return ExitError
			}
		}
		if err := os.WriteFile(f.Path, f.Data, 0o644); err != nil {
			fmt.Fprintf(stderr, "yakbd %s: %v\n", cmd, err)
			return ExitError
		}
		if f.Line != "" {
			fmt.Fprintln(stdout, f.Line)
		}
	}
	for _, s := range res.Lines {
		fmt.Fprintln(stdout, s)
	}
	return ExitOK
}
