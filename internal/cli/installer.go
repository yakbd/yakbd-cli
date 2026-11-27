package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// installerCmd writes one Windows installer with every build of a layout (docs/installer.md §7).
func installerCmd(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("installer", "layout", stderr)
	var rf reportFlags
	rf.register(fs)
	out := fs.String("o", "", "output file (default <name>-setup.exe)")
	machines := fs.String("build", "", "comma-separated machine kinds to carry: x64 (x64 + wow64 files), arm64 (arm64x + wow64), x86; default: every kind the tool can build for")
	enable := fs.String("enable", "", "true or false: override install.enableForUser (default: the file, else rule enableForUser = true)")
	signIn := fs.String("signin", "", "true or false: override install.enableForSignInAndNewUsers (default: the file, else rule enableForSignInAndNewUsers = false)")
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
	var enableOverride *bool
	switch *enable {
	case "":
	case "true", "false":
		v := *enable == "true"
		enableOverride = &v
	default:
		fmt.Fprintf(stderr, "yakbd installer: -enable takes true or false\n")
		return ExitUsage
	}
	var signInOverride *bool
	switch *signIn {
	case "":
	case "true", "false":
		v := *signIn == "true"
		signInOverride = &v
	default:
		fmt.Fprintf(stderr, "yakbd installer: -signin takes true or false\n")
		return ExitUsage
	}
	var kinds []string
	if *machines != "" {
		for _, s := range strings.Split(*machines, ",") {
			s = strings.TrimSpace(s)
			if _, ok := installer.BuildsFor[s]; !ok {
				fmt.Fprintf(stderr, "yakbd installer: unknown machine kind %q (want x64, arm64 or x86)\n", s)
				return ExitUsage
			}
			kinds = append(kinds, s)
		}
	}
	l, r := readLayout(pos[0], "")
	if l == nil || r.HasErrors() {
		rf.finish(r, stderr)
		return ExitError
	}
	if *langID != "" && (l.Targets == nil || l.Targets.Windows == nil || l.Targets.Windows.Registration == nil || l.Targets.Windows.Registration.LangID == "") {
		setLangID(l, *langID)
		r.Infof("cli.langid", loss.Where{}, "LANGID %s from -langid", *langID)
	}
	extra := rf.extraWith(*accept)
	res, br := build.Lower(l, build.Options{ToolVersion: Version, Policy: extra})
	r.Merge(br)
	if res == nil {
		rf.finish(r, stderr)
		return ExitError
	}
	list := res.Targets
	if len(kinds) > 0 {
		list = nil
		seen := map[string]bool{}
		for _, k := range kinds {
			for _, b := range installer.BuildsFor[k] {
				if !seen[b] {
					seen[b] = true
					list = append(list, targetOfBuild[b])
				}
			}
		}
	}
	// An explicit -build arm64 builds the ARM64X file even while the full set still skips it (it passed the ARM64
	// guest, VM-6; TA makes it a default build).
	set := compileSet(l, res.DLL, list, extra, len(kinds) > 0)
	r.Merge(set.Report)
	if r.HasErrors() {
		rf.finish(r, stderr)
		return ExitError
	}
	pkg, pr := installer.NewPackage(l, res, set, installer.Options{Machines: kinds, Enable: enableOverride, SignIn: signInOverride,
		Tool: "yakbd " + VersionString()})
	policy.Resolve(pr, l, "windows", extra)
	r.Merge(pr)
	if !rf.finish(r, stderr) {
		return ExitError
	}
	data, err := installer.ExeContainer{}.Write(pkg)
	if err != nil {
		fmt.Fprintf(stderr, "yakbd installer: %v\n", err)
		return ExitError
	}
	path := *out
	if path == "" {
		path = strings.TrimSuffix(res.DLL.FileName, ".dll") + "-setup.exe"
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "yakbd installer: %v\n", err)
		return ExitError
	}
	fmt.Fprintf(stdout, "%s\t%d bytes\tfor %s Windows\tfiles %s\tproduct %s\tunsigned\n", path, len(data),
		strings.Join(pkg.Machines, ", "), strings.Join(pkg.Builds, ", "), pkg.Manifest.Product.Code)
	return ExitOK
}

var targetOfBuild = map[string]windows.Target{
	"x64": windows.TargetAMD64, "wow64": windows.TargetWOW64, "x86": windows.TargetX86, "arm64x": windows.TargetARM64X,
}
