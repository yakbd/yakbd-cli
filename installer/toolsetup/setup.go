package toolsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/setup"
)

// System is what the installers need from Windows (a subset of installer/setup's System, plus the native Program
// Files folder, the environment broadcast and shortcuts). Registry paths start with `HKLM\`, the 64-bit view.
type System interface {
	NativeMachine() (uint16, error)
	Elevated() bool
	// NativeProgramFiles is the native Program Files folder (docs/installer-cli.md §12).
	NativeProgramFiles() (string, error)
	TempDir() string
	SelfPath() string
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	Rename(from, to string) error
	Remove(path string) error
	MkdirAll(path string) error
	RemoveDir(path string) error
	DeleteAtReboot(path string) error
	MoveAtReboot(from, to string) error
	CreateKey(path string) (created bool, err error)
	DeleteKey(path string) error
	Values(path string) ([]setup.Value, error)
	SetString(path, name, value string, expand bool) error
	SetDWORD(path, name string, v uint32) error
	// BroadcastEnvironment sends WM_SETTINGCHANGE "Environment" (docs/installer-cli.md §12).
	BroadcastEnvironment() error
	// CommonPrograms is the common Start-menu Programs folder, FOLDERID_CommonPrograms (docs/installer-cli.md §12.4).
	CommonPrograms() (string, error)
	// CreateShortcut writes the shortcut file lnk pointing at target, with target's first icon and the working folder
	// dir; an error unless the file was saved.
	CreateShortcut(lnk, target, dir string) error
	// ReadShortcut reads a shortcut file back: its target and working folder.
	ReadShortcut(lnk string) (target, dir string, err error)
}

// Registry paths, folders and record values (docs/installer-cli.md §12.2).
const (
	Environment = `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	// RootName is the installers' folder in Program Files; each component has its own folder in it.
	RootName = "YAKBD"
	// InstallerName is the copy of an installer in the root folder that the uninstall entry runs.
	InstallerName = "setup.exe"
	// ProductName is the uninstall entry's DisplayName.
	ProductName = "YAKBD"

	RecComponents = "YakbdComponents" // the installed components, "CLI" or "CLI,Editor"
	RecBuild      = "YakbdBuild"      // the machine's build the files are for
	RecPathAdded  = "YakbdPathAdded"  // "1": the installer added the CLI folder to the machine Path
	RecShortcuts  = "YakbdShortcuts"  // the Start-menu shortcut names, joined by "|"
	RecUserData   = "YakbdUserData"   // the installed components' user-data declarations, JSON
)

// RecVersion and RecFiles are the record values of one component: its version, and its files relative to its
// folder joined by "|" (a character no path has).
func RecVersion(id string) string { return "Yakbd" + id + "Version" }
func RecFiles(id string) string   { return "Yakbd" + id + "Files" }

// The one-folder layout of 0.1.0-preview.1 and preview.2 (yakbd-setup.exe; the editor's 0.1.0-preview.1 carried
// its files as that installer's extra payload).
const (
	LegacyCode      = "{EBE1EAEC-C574-5473-8489-B3FF22FC6506}"
	LegacyFolder    = "yakbd"
	LegacyRecFile   = "YakbdToolFile"
	LegacyRecExtra  = "YakbdToolExtraFiles"
	LegacyRecPath   = "YakbdPathAdded"
	legacyEditorExe = "yakbd-gui.exe"
	legacyExe       = "yakbd.exe"
)

// Result tells what a run did.
type Result struct {
	// Reboot is set when a file is replaced or deleted at the next restart.
	Reboot bool
	Notes  []string
}

// Setup runs the installer's steps for one payload.
type Setup struct {
	Sys System
	P   *Payload
	// Self is the installer file's bytes when the caller has read them already; nil: Apply reads SelfPath.
	Self []byte
	Log  func(format string, args ...any)
}

func (s *Setup) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

func fail(code int, format string, args ...any) error {
	return &cmdline.Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// ExitCode maps an error of this package to the exit code (1603 for anything unclassified).
func ExitCode(err error) int {
	var e *cmdline.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return cmdline.Success
	}
	return cmdline.InstallFailure
}

func join(dir, name string) string { return strings.TrimRight(dir, `\/`) + `\` + name }

func (s *Setup) arpKey() string { return setup.UninstallRoot + `\` + s.P.Manifest.Product.Code }

func legacyKey() string { return setup.UninstallRoot + `\` + LegacyCode }

// Root is the installers' folder, %ProgramW6432%\YAKBD.
func (s *Setup) Root() (string, error) {
	pf, err := s.Sys.NativeProgramFiles()
	if err != nil {
		return "", fmt.Errorf("cannot find Program Files: %v", err)
	}
	return join(pf, RootName), nil
}

func values(sys System, p string) map[string]setup.Value {
	vs, err := sys.Values(p)
	if err != nil {
		return nil
	}
	m := map[string]setup.Value{}
	for _, v := range vs {
		m[strings.ToLower(v.Name)] = v
	}
	return m
}

func str(m map[string]setup.Value, name string) string { return m[strings.ToLower(name)].Str }

func samePath(a, b string) bool {
	n := func(p string) string { return strings.ToLower(strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)) }
	return n(a) == n(b)
}

func pathHas(v, dir string) bool {
	for _, e := range strings.Split(v, ";") {
		if samePath(strings.TrimSpace(e), dir) {
			return true
		}
	}
	return false
}

// readPath returns the machine Path value and whether it is REG_EXPAND_SZ and exists.
func (s *Setup) readPath() (string, bool, bool) {
	v, ok := values(s.Sys, Environment)["path"]
	return v.Str, v.Type == setup.RegExpandSZ, ok
}

// splitRecord splits a "|" record into its entries that pass check.
func splitRecord(v string, check func(string) error) []string {
	var out []string
	for _, e := range strings.Split(v, "|") {
		if e != "" && check(e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Installed is one installed component.
type Installed struct {
	Version string
	Files   []string // relative to the component's folder (the legacy layout: to the legacy folder)
}

// Legacy is an installation of the one-folder layout (0.1.0-preview.1 and preview.2).
type Legacy struct {
	Version   string
	Editor    bool     // the extra payload had yakbd-gui.exe
	Extra     []string // the extra files, relative to the legacy folder
	PathAdded bool
	Dir       string // its InstallLocation
	Shortcuts []string
}

// State is what is installed on the machine.
type State struct {
	Root string
	// Entry: the uninstall entry of the two installers exists.
	Entry     bool
	Installed map[string]Installed // by component, from the entry
	Build     string               // the entry's build
	PathAdded bool                 // the entry says the installer added the CLI folder to the Path
	// PathPresent: the CLI folder is in the machine Path now (added by anyone).
	PathPresent bool
	Shortcuts   []string
	UserData    []UserData
	Legacy      *Legacy
}

// Has reports whether component id is installed, in either layout.
func (st *State) Has(id string) bool {
	if _, ok := st.Installed[id]; ok {
		return true
	}
	if st.Legacy != nil {
		return id == CLI || id == Editor && st.Legacy.Editor
	}
	return false
}

// VersionOf is the installed version of component id ("" when not installed).
func (st *State) VersionOf(id string) string {
	if c, ok := st.Installed[id]; ok {
		return c.Version
	}
	if st.Has(id) && st.Legacy != nil {
		return st.Legacy.Version
	}
	return ""
}

// Detect reads what is installed: the entry of the two installers, the legacy entry, the machine Path.
func (s *Setup) Detect() (*State, error) {
	root, err := s.Root()
	if err != nil {
		return nil, err
	}
	st := &State{Root: root, Installed: map[string]Installed{}}
	arp := values(s.Sys, s.arpKey())
	if arp != nil && str(arp, RecComponents) != "" {
		st.Entry = true
		st.Build = str(arp, RecBuild)
		st.PathAdded = str(arp, RecPathAdded) == "1"
		for _, id := range strings.Split(str(arp, RecComponents), ",") {
			if ComponentName[id] == "" {
				continue
			}
			st.Installed[id] = Installed{Version: str(arp, RecVersion(id)), Files: splitRecord(str(arp, RecFiles(id)), CheckPath)}
		}
		st.Shortcuts = splitRecord(str(arp, RecShortcuts), CheckShortcutName)
		var ud []UserData
		if json.Unmarshal([]byte(str(arp, RecUserData)), &ud) == nil {
			for _, u := range ud {
				if CheckUserData(u) == nil { // never a path outside the user's settings, whatever the registry holds
					st.UserData = append(st.UserData, u)
				}
			}
		}
	}
	if old := values(s.Sys, legacyKey()); old != nil && str(old, LegacyRecFile) != "" {
		l := &Legacy{Version: str(old, "DisplayVersion"), PathAdded: str(old, LegacyRecPath) == "1", Dir: str(old, "InstallLocation")}
		if l.Dir == "" {
			l.Dir = join(root, LegacyFolder)
		}
		l.Extra = splitRecord(str(old, LegacyRecExtra), checkLegacyPath)
		for _, x := range l.Extra {
			l.Editor = l.Editor || strings.EqualFold(x, legacyEditorExe)
		}
		l.Shortcuts = splitRecord(str(old, RecShortcuts), CheckShortcutName) // "YakbdToolShortcuts" was never published
		st.Legacy = l
	}
	if pv, _, ok := s.readPath(); ok {
		st.PathPresent = pathHas(pv, join(root, CLI))
	}
	return st, nil
}

// checkLegacyPath accepts a legacy extra path (CheckPath, and not the legacy program or installer copy).
func checkLegacyPath(p string) error {
	if err := CheckPath(p); err != nil {
		return err
	}
	if top, _, _ := strings.Cut(strings.ToLower(p), `\`); top == legacyExe || top == InstallerName {
		return fmt.Errorf("%s: written by the installer itself", p)
	}
	return nil
}

// Request is what a command line (or the wizard, through the same properties) asks for (docs/installer-cli.md
// §12.3).
type Request struct {
	Uninstall bool // /x, REMOVE=ALL
	Repair    bool // /f, REINSTALL=ALL
	// AddLocal is ADDLOCAL (the components to have installed, besides those installed); nil: not given, the
	// installer's defaults.
	AddLocal []string
	Remove   []string // REMOVE=Editor
	// AddToPath is ADDTOPATH; nil: not given.
	AddToPath      *bool
	RemoveUserData bool // REMOVEUSERDATA=1
}

// Action is what a run does with one component.
type Action int

// The actions.
const (
	None      Action = iota // not installed, not installed afterwards
	Install                 // new (or moved from the legacy layout)
	Upgrade                 // this installer's version is newer
	Repair                  // the same version: every differing file written again
	Keep                    // stays as it is (this installer does not carry it)
	KeepNewer               // stays: the installed one is newer than this installer's
	Remove
)

var actionWord = map[Action]string{None: "-", Install: "install", Upgrade: "upgrade", Repair: "repair", Keep: "keep", KeepNewer: "keep (newer)", Remove: "remove"}

func (a Action) String() string { return actionWord[a] }

// Plan is what a run will do.
type Plan struct {
	Build string
	Comp  map[string]Action
	// Path is whether the CLI folder is in the machine Path afterwards (by this installer).
	Path bool
	// Notes are said in the wizard, the end dialog and the log.
	Notes []string
	// RemoveUserData: delete the starting user's settings of removed components (done by the first process).
	RemoveUserData bool
}

// Final reports whether component id is installed after the plan.
func (p *Plan) Final(id string) bool {
	a := p.Comp[id]
	return a != None && a != Remove
}

// Empty reports whether nothing is installed after the plan (an uninstall).
func (p *Plan) Empty() bool { return !p.Final(CLI) && !p.Final(Editor) }

// String is the plan for the log: "CLI upgrade, Editor install, PATH on".
func (p *Plan) String() string {
	var out []string
	for _, id := range ComponentIDs {
		if p.Comp[id] != None {
			out = append(out, id+" "+p.Comp[id].String())
		}
	}
	if len(out) == 0 {
		out = append(out, "nothing")
	}
	return strings.Join(out, ", ") + fmt.Sprintf(", PATH %v", map[bool]string{true: "on", false: "off"}[p.Path])
}

// Machine is the machine's build for this installer: the build of its architecture (IsWow64Process2).
func (s *Setup) Machine() (string, error) {
	native, err := s.Sys.NativeMachine()
	if err != nil {
		return "", fmt.Errorf("cannot tell the machine's architecture: %v", err)
	}
	b, ok := BuildFor[native]
	if !ok {
		return "", fail(cmdline.PlatformUnsupported, "This installer does not support this computer (machine %#04x). Nothing was changed.", native)
	}
	return b, nil
}

// Defaults are the components this installer installs when ADDLOCAL is not given: every component it carries for
// the build.
func (s *Setup) Defaults(build string) []string {
	var out []string
	for _, id := range ComponentIDs {
		if s.P.Supports(id, build) {
			out = append(out, id)
		}
	}
	return out
}

// Decide turns what is installed and the request into a plan, or refuses (docs/installer-cli.md §12.3).
func (s *Setup) Decide(st *State, build string, req Request) (*Plan, error) {
	p := &Plan{Build: build, Comp: map[string]Action{}}
	if !s.P.Supports(CLI, build) {
		return nil, fail(cmdline.PlatformUnsupported, "This installer has no build of YAKBD CLI for this computer (%s); it carries %s. Nothing was changed.", build, s.P.Manifest.Summary())
	}
	final := map[string]bool{}
	installed := map[string]bool{}
	for _, id := range ComponentIDs {
		installed[id] = st.Has(id)
	}
	if (req.Uninstall || req.Repair) && !installed[CLI] && !installed[Editor] {
		return nil, fail(cmdline.UnknownProduct, "YAKBD is not installed; nothing was changed.")
	}
	add := req.AddLocal
	if add == nil && !req.Repair {
		add = s.Defaults(build)
	}
	for _, id := range add {
		if ComponentName[id] == "" {
			return nil, fail(cmdline.InvalidCommandLine, "ADDLOCAL: unknown component %q (CLI, Editor)", id)
		}
		if !s.P.Supports(id, build) {
			switch {
			case id == Editor && build == BuildX86:
				return nil, fail(cmdline.InvalidCommandLine, "YAKBD Editor needs 64-bit Windows; this is 32-bit Windows. Nothing was changed.")
			case s.P.Manifest.Component(id) == nil:
				return nil, fail(cmdline.InvalidCommandLine, "This installer does not carry %s (yakbd-gui-setup.exe does). Nothing was changed.", ComponentName[id])
			default:
				return nil, fail(cmdline.PlatformUnsupported, "This installer has no build of %s for this computer (%s). Nothing was changed.", ComponentName[id], build)
			}
		}
	}
	if !req.Uninstall {
		for _, id := range ComponentIDs {
			final[id] = installed[id]
		}
		for _, id := range add {
			final[id] = true
		}
	}
	for _, id := range req.Remove {
		if ComponentName[id] == "" {
			return nil, fail(cmdline.InvalidCommandLine, "REMOVE: unknown component %q (Editor, ALL)", id)
		}
		final[id] = false
	}
	if final[Editor] && !final[CLI] {
		return nil, fail(cmdline.InvalidCommandLine, "YAKBD Editor needs YAKBD CLI: the CLI cannot be removed while the editor stays (REMOVE=ALL removes both). Nothing was changed.")
	}
	if st.Legacy != nil && st.Legacy.Editor && final[Editor] && !s.P.Supports(Editor, build) {
		return nil, fail(cmdline.InstallFailure, "YAKBD Editor %s is installed in the earlier layout (Program Files\\YAKBD\\yakbd), and this installer cannot move it, as it does not carry the editor. Run yakbd-gui-setup.exe instead, or remove YAKBD first. Nothing was changed.", st.Legacy.Version)
	}
	for _, id := range ComponentIDs {
		_, inNew := st.Installed[id]
		switch {
		case final[id] && s.P.Supports(id, build):
			mine := s.P.Manifest.Component(id).Version
			switch c := CompareVersions(mine, st.Installed[id].Version); {
			case !inNew:
				p.Comp[id] = Install
			case c > 0:
				p.Comp[id] = Upgrade
			case c == 0:
				p.Comp[id] = Repair
			default:
				p.Comp[id] = KeepNewer
				p.Notes = append(p.Notes, fmt.Sprintf("%s %s is installed, newer than this installer's %s; it is kept.", ComponentName[id], st.Installed[id].Version, mine))
			}
		case final[id]:
			p.Comp[id] = Keep
		case installed[id]:
			p.Comp[id] = Remove
		}
	}
	switch {
	case !p.Final(CLI):
		p.Path = false
	case req.AddToPath != nil:
		p.Path = *req.AddToPath
	case st.PathPresent:
		p.Path = true
	case st.Legacy != nil && st.Legacy.PathAdded:
		p.Path = true
	case len(st.Installed) > 0 && p.Comp[CLI] != Install:
		p.Path = false // the CLI is installed without the PATH entry: kept so
	default:
		p.Path = s.P.Manifest.AddToPath
	}
	if req.RemoveUserData {
		p.RemoveUserData = true
	}
	if st.Legacy != nil {
		p.Notes = append(p.Notes, "The earlier installation in Program Files\\YAKBD\\yakbd is taken over and removed.")
	}
	return p, nil
}

// place writes one file: beside it, renamed over it, read back; when the file is in use, the new bytes wait beside
// it and replace it at the next restart. fresh tells whether the file was not there before (so that a rollback
// deletes it).
func (s *Setup) place(path string, data []byte, res *Result) (fresh bool, err error) {
	old, rerr := s.Sys.ReadFile(path)
	if rerr == nil && bytes.Equal(old, data) {
		s.logf("%s: already present and identical", path)
		return false, nil
	}
	tmp := path + ".yakbd-tmp"
	if err := s.Sys.WriteFile(tmp, data); err != nil {
		return false, fmt.Errorf("cannot write %s: %v", tmp, err)
	}
	if err := s.Sys.Rename(tmp, path); err != nil {
		_ = s.Sys.Remove(tmp)
		if !errors.Is(err, setup.ErrInUse) {
			return false, fmt.Errorf("cannot write %s: %v", path, err)
		}
		next := path + ".yakbd-new"
		if err := s.Sys.WriteFile(next, data); err != nil {
			return false, fmt.Errorf("cannot write %s: %v", next, err)
		}
		if err := s.Sys.MoveAtReboot(next, path); err != nil {
			return false, fmt.Errorf("cannot schedule the replacement of %s: %v", path, err)
		}
		s.logf("%s is in use: replaced at the next restart", path)
		res.Reboot = true
		return false, nil
	}
	if back, err := s.Sys.ReadFile(path); err != nil || !bytes.Equal(back, data) {
		return rerr != nil, fmt.Errorf("%s did not read back as written", path)
	}
	s.logf("wrote %s (%d bytes)", path, len(data))
	return rerr != nil, nil
}

// removeFiles deletes files of a folder (relative paths, with what place may have left beside them), a file in
// use at the next restart, then every folder that held them and is empty, deepest first. With laterFolders (the
// folder is emptied for good), a folder that still holds a file waiting for the restart is deleted after it; an
// upgrade never schedules a folder, which may hold files it keeps.
func (s *Setup) removeFiles(dir string, rel []string, res *Result, laterFolders bool) error {
	folders := map[string]bool{}
	waiting := map[string]bool{}
	for _, r := range rel {
		p := join(dir, r)
		for _, ext := range reservedSuffixes {
			if err := s.Sys.Remove(p + ext); err == nil {
				s.logf("%s deleted", p+ext)
			}
		}
		if err := s.Sys.Remove(p); err != nil && !errors.Is(err, setup.ErrNotFound) {
			if !errors.Is(err, setup.ErrInUse) {
				return fmt.Errorf("cannot delete %s: %v", p, err)
			}
			if err := s.Sys.DeleteAtReboot(p); err != nil {
				return fmt.Errorf("cannot schedule the deletion of %s: %v", p, err)
			}
			res.Reboot = true
			s.logf("%s is in use: deleted at the next restart", p)
			for i := strings.LastIndex(r, `\`); i > 0; i = strings.LastIndex(r[:i], `\`) {
				waiting[r[:i]] = true
			}
		} else if err == nil {
			s.logf("%s deleted", p)
		}
		for i := strings.LastIndex(r, `\`); i > 0; i = strings.LastIndex(r[:i], `\`) {
			folders[r[:i]] = true
		}
	}
	var list []string
	for f := range folders {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool {
		ni, nj := strings.Count(list[i], `\`), strings.Count(list[j], `\`)
		if ni != nj {
			return ni > nj
		}
		return list[i] < list[j]
	})
	for _, f := range list {
		p := join(dir, f)
		if err := s.Sys.RemoveDir(p); err != nil {
			if laterFolders && waiting[f] {
				_ = s.Sys.DeleteAtReboot(p)
			}
			continue
		}
		s.logf("%s deleted", p)
	}
	return nil
}

// removeDir deletes an emptied folder, or at the next restart when it still holds a file that waits for it.
func (s *Setup) removeDir(dir string, res *Result) {
	if err := s.Sys.RemoveDir(dir); err != nil {
		if res.Reboot {
			_ = s.Sys.DeleteAtReboot(dir)
			s.logf("%s: deleted at the next restart", dir)
		}
		return
	}
	s.logf("%s deleted", dir)
}

func lnkName(name string) string { return name + ".lnk" }

// removeShortcuts deletes shortcut files of the common Programs folder by name; one in use at the next restart.
func (s *Setup) removeShortcuts(progs string, names []string, res *Result) error {
	for _, n := range names {
		p := join(progs, lnkName(n))
		if err := s.Sys.Remove(p); err != nil && !errors.Is(err, setup.ErrNotFound) {
			if !errors.Is(err, setup.ErrInUse) {
				return fmt.Errorf("cannot delete %s: %v", p, err)
			}
			if err := s.Sys.DeleteAtReboot(p); err != nil {
				return fmt.Errorf("cannot schedule the deletion of %s: %v", p, err)
			}
			res.Reboot = true
		} else if err == nil {
			s.logf("%s deleted", p)
		}
	}
	return nil
}

// pendingDeletion returns a PendingFileRenameOperations entry that deletes the folder or anything in it at restart.
func (s *Setup) pendingDeletion(dir string) string {
	d := strings.ToLower(strings.TrimRight(strings.ReplaceAll(dir, "/", `\`), `\`))
	for _, v := range values(s.Sys, setup.SessionManager) {
		if !strings.EqualFold(v.Name, "PendingFileRenameOperations") || v.Type != setup.RegMultiSZ {
			continue
		}
		parts := strings.Split(v.Str, "\x00")
		for i := 0; i+1 < len(parts); i += 2 {
			src := strings.TrimPrefix(parts[i], `\??\`)
			if l := strings.ToLower(strings.ReplaceAll(src, "/", `\`)); parts[i+1] == "" && (samePath(src, dir) || strings.HasPrefix(l, d+`\`)) {
				return src
			}
		}
	}
	return ""
}

// addPath appends dir to a PATH value so that removePath gives the value back exactly.
func addPath(pv, dir string) string {
	switch {
	case pv == "":
		return dir
	case strings.HasSuffix(pv, ";"):
		return pv + dir + ";"
	}
	return pv + ";" + dir
}

// removePath undoes addPath; an entry moved or edited since is removed wherever it is.
func removePath(pv, dir string) string {
	l, d := strings.ToLower(pv), strings.ToLower(dir)
	switch {
	case l == d:
		return ""
	case strings.HasSuffix(l, ";"+d+";"):
		return pv[:len(pv)-len(dir)-1]
	case strings.HasSuffix(l, ";"+d):
		return pv[:len(pv)-len(dir)-1]
	}
	var keep []string
	for _, e := range strings.Split(pv, ";") {
		if !samePath(strings.TrimSpace(e), dir) {
			keep = append(keep, e)
		}
	}
	return strings.Join(keep, ";")
}

// setPath adds or removes dir in the machine Path and broadcasts the change; it returns the undo.
func (s *Setup) setPath(dir string, on bool) (func(), error) {
	pv, expand, ok := s.readPath()
	if !ok {
		s.logf("PATH: the machine Path value cannot be read; not changed")
		return func() {}, nil
	}
	nv := pv
	if on && !pathHas(pv, dir) {
		nv = addPath(pv, dir)
	} else if !on {
		nv = removePath(pv, dir)
	}
	if nv == pv {
		return func() {}, nil
	}
	if err := s.Sys.SetString(Environment, "Path", nv, expand); err != nil {
		return nil, fmt.Errorf("cannot write the machine PATH: %v", err)
	}
	s.logf("PATH: %s %s", dir, map[bool]string{true: "appended", false: "removed"}[on])
	if err := s.Sys.BroadcastEnvironment(); err != nil {
		s.logf("environment broadcast: %v", err)
	}
	return func() { _ = s.Sys.SetString(Environment, "Path", pv, expand) }, nil
}

// Apply carries out a plan (docs/installer-cli.md §12.2): the components' files, shortcuts, the installer copy, the
// Path, the uninstall entry, then what goes (removed components, stale files and shortcuts, the earlier layout). A
// first installation (no entry before) that fails is rolled back, the earlier layout untouched.
func (s *Setup) Apply(st *State, plan *Plan) (*Result, error) {
	if !s.Sys.Elevated() {
		return nil, fail(cmdline.ElevationRequired, "Administrator rights are needed (run the installer elevated).")
	}
	root := st.Root
	if !plan.Empty() {
		if p := s.pendingDeletion(root); p != "" {
			return nil, fail(cmdline.InstallFailure, "%s will be deleted when Windows restarts (an earlier uninstall). Restart Windows, then run the installer again. Nothing was changed.", p)
		}
	}
	s.logf("plan: %s; installed before: %s", plan, describe(st))
	res := &Result{Notes: append([]string(nil), plan.Notes...)}
	var undo []func()
	rollback := func(err error) (*Result, error) {
		if !st.Entry {
			s.logf("failed: %v; rolling back", err)
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
		return nil, err
	}
	build := plan.Build
	versions := map[string]string{}
	files := map[string][]string{}
	size := 0
	for _, id := range ComponentIDs {
		if c, ok := st.Installed[id]; ok {
			versions[id], files[id] = c.Version, c.Files
		}
	}

	// 1. Components this installer writes.
	if !plan.Empty() {
		if err := s.Sys.MkdirAll(root); err != nil {
			return nil, fmt.Errorf("cannot create %s: %v", root, err)
		}
		undo = append(undo, func() { _ = s.Sys.RemoveDir(root) })
	}
	stale := map[string][]string{}
	for _, id := range ComponentIDs {
		a := plan.Comp[id]
		if a != Install && a != Upgrade && a != Repair {
			continue
		}
		dir := join(root, id)
		if err := s.Sys.MkdirAll(dir); err != nil {
			return rollback(fmt.Errorf("cannot create %s: %v", dir, err))
		}
		undo = append(undo, func() { _ = s.Sys.RemoveDir(dir) })
		made := map[string]bool{}
		var rel []string
		input, err := s.P.FilesFor(id, build)
		if err != nil {
			return rollback(fail(cmdline.PackageInvalid, "%v", err))
		}
		for _, f := range input {
			p := join(dir, f.Path)
			for i, c := range f.Path {
				if sub := strings.ToLower(f.Path[:i]); c == '\\' && !made[sub] {
					made[sub] = true
					d := join(dir, f.Path[:i])
					if err := s.Sys.MkdirAll(d); err != nil {
						return rollback(fmt.Errorf("cannot create %s: %v", d, err))
					}
					undo = append(undo, func() { _ = s.Sys.RemoveDir(d) })
				}
			}
			fresh, err := s.place(p, f.Data, res)
			if fresh {
				undo = append(undo, func() { _ = s.Sys.Remove(p) })
			}
			if err != nil {
				return rollback(err)
			}
			rel = append(rel, f.Path)
			size += len(f.Data)
		}
		keep := map[string]bool{}
		for _, r := range rel {
			keep[strings.ToLower(r)] = true
		}
		for _, r := range files[id] {
			if !keep[strings.ToLower(r)] {
				stale[id] = append(stale[id], r)
			}
		}
		versions[id], files[id] = s.P.Manifest.Component(id).Version, rel
		s.logf("%s %s: %s into %s", ComponentName[id], versions[id], a, dir)
	}

	// 2. Shortcuts: those of the components this installer writes, made anew and read back; those of kept
	// components stay as recorded.
	var shortcuts []string
	needProgs := len(st.Shortcuts) > 0 || st.Legacy != nil && len(st.Legacy.Shortcuts) > 0
	var mine []Shortcut
	for _, id := range ComponentIDs {
		if a := plan.Comp[id]; a == Install || a == Upgrade || a == Repair {
			mine = append(mine, s.P.ShortcutsFor(id, build)...)
		}
	}
	progs := ""
	if len(mine) > 0 || needProgs {
		var err error
		if progs, err = s.Sys.CommonPrograms(); err != nil {
			if len(mine) > 0 {
				return rollback(fmt.Errorf("cannot find the Start menu's Programs folder: %v", err))
			}
			s.logf("cannot find the Start menu's Programs folder (%v): earlier shortcuts left alone", err)
		}
	}
	for _, sc := range mine {
		lnk := join(progs, lnkName(sc.Name))
		_, rerr := s.Sys.ReadFile(lnk)
		if rerr == nil {
			_ = s.Sys.Remove(lnk) // written anew, not saved over (the sources do not say that Save replaces a file)
		} else {
			undo = append(undo, func() { _ = s.Sys.Remove(lnk) })
		}
		dir := join(root, sc.Component)
		want := join(dir, sc.Target)
		if err := s.Sys.CreateShortcut(lnk, want, dir); err != nil {
			return rollback(fmt.Errorf("cannot create the shortcut %s: %v", lnk, err))
		}
		if t, d, err := s.Sys.ReadShortcut(lnk); err != nil || !samePath(t, want) || !samePath(d, dir) {
			if err == nil {
				err = fmt.Errorf("it points at %q in %q, want %q in %q", t, d, want, dir)
			}
			return rollback(fmt.Errorf("the shortcut %s did not read back as written: %v", lnk, err))
		}
		s.logf("shortcut %s -> %s written and read back", lnk, want)
		shortcuts = append(shortcuts, sc.Name)
	}
	// Recorded shortcuts of kept components stay; the others go in step 6.
	keptShort := map[string]bool{}
	for _, n := range shortcuts {
		keptShort[strings.ToLower(n)] = true
	}
	if plan.Comp[Editor] == Keep || plan.Comp[Editor] == KeepNewer {
		for _, n := range st.Shortcuts {
			if !keptShort[strings.ToLower(n)] {
				shortcuts = append(shortcuts, n)
				keptShort[strings.ToLower(n)] = true
			}
		}
	}

	// 3. The installer copy for the uninstall entry: this installer, unless it lacks a component that stays (then
	// the copy that carries it stays).
	inst := join(root, InstallerName)
	self := s.Self
	keepCopy := false
	for _, id := range ComponentIDs {
		if plan.Final(id) && s.P.Manifest.Component(id) == nil {
			keepCopy = true
		}
	}
	if !plan.Empty() && !keepCopy && !samePath(s.Sys.SelfPath(), inst) {
		if self == nil {
			var err error
			if self, err = s.Sys.ReadFile(s.Sys.SelfPath()); err != nil {
				return rollback(fmt.Errorf("cannot read the installer itself: %v", err))
			}
		}
		if err := s.Sys.WriteFile(inst, self); err != nil {
			if !errors.Is(err, setup.ErrInUse) {
				return rollback(fmt.Errorf("cannot write %s: %v", inst, err))
			}
			s.logf("%s is in use (an uninstaller running?): kept", inst)
		} else {
			undo = append(undo, func() { _ = s.Sys.Remove(inst) })
		}
	}

	// 4. PATH: the CLI folder.
	cliDir := join(root, CLI)
	pathAdded := st.PathAdded
	switch {
	case plan.Path:
		u, err := s.setPath(cliDir, true)
		if err != nil {
			return rollback(err)
		}
		undo = append(undo, u)
		if !st.PathPresent {
			pathAdded = true
			res.Notes = append(res.Notes, "Programs started from now on find yakbd on the PATH.")
		}
	case st.PathPresent:
		// Removed: the CLI goes (its entry would point at nothing), or ADDTOPATH=0 (the wizard's box) asks.
		u, err := s.setPath(cliDir, false)
		if err != nil {
			return rollback(err)
		}
		undo = append(undo, u)
		pathAdded = false
	}

	// 5. The uninstall entry.
	key := s.arpKey()
	if plan.Empty() {
		if st.Entry {
			if err := s.Sys.DeleteKey(key); err != nil {
				return nil, fmt.Errorf("cannot delete the uninstall entry: %v", err)
			}
			s.logf("uninstall entry deleted")
		}
	} else {
		created, err := s.Sys.CreateKey(key)
		if err != nil {
			return rollback(fmt.Errorf("cannot create the uninstall entry %s: %v", key, err))
		}
		if created {
			undo = append(undo, func() { _ = s.Sys.DeleteKey(key) })
		}
		if err := s.writeEntry(key, root, plan, versions, files, shortcuts, pathAdded, size+len(self)); err != nil {
			return rollback(err)
		}
		s.logf("uninstall entry %s written", key)
	}

	// 6. What goes: removed components, stale files and shortcuts, the earlier layout.
	var problems []string
	note := func(err error) {
		if err != nil {
			s.logf("%v", err)
			problems = append(problems, err.Error())
		}
	}
	var goneShort []string
	for _, n := range st.Shortcuts {
		if !keptShort[strings.ToLower(n)] {
			goneShort = append(goneShort, n)
		}
	}
	if progs != "" && len(goneShort) > 0 {
		note(s.removeShortcuts(progs, goneShort, res))
	}
	for _, id := range ComponentIDs {
		dir := join(root, id)
		if plan.Comp[id] == Remove {
			note(s.removeFiles(dir, st.Installed[id].Files, res, true))
			s.removeDir(dir, res)
			s.logf("%s removed", ComponentName[id])
		} else if len(stale[id]) > 0 {
			note(s.removeFiles(dir, stale[id], res, false))
		}
	}
	if st.Legacy != nil {
		note(s.removeLegacy(st, progs, res))
	}
	if plan.Empty() {
		s.removeSelf(root, res)
	}
	if len(problems) > 0 {
		res.Notes = append(res.Notes, "Something of the earlier installation could not be removed: "+strings.Join(problems, "; "))
	}
	if res.Reboot {
		res.Notes = append(res.Notes, "A file of YAKBD is in use; restart Windows to finish.")
	}
	return res, nil
}

// writeEntry writes the uninstall entry's values (docs/installer-cli.md §12.2).
func (s *Setup) writeEntry(key, root string, plan *Plan, versions map[string]string, files map[string][]string, shortcuts []string, pathAdded bool, size int) error {
	inst := join(root, InstallerName)
	q := `"` + inst + `"`
	code := s.P.Manifest.Product.Code
	display := versions[CLI]
	if plan.Final(Editor) {
		display = versions[Editor]
	}
	var comps []string
	var ud []UserData
	for _, id := range ComponentIDs {
		if plan.Final(id) {
			comps = append(comps, id)
		}
	}
	for _, u := range s.P.Manifest.UserData {
		if plan.Final(u.Component) {
			ud = append(ud, u)
		}
	}
	udj := ""
	if len(ud) > 0 {
		b, _ := json.Marshal(ud)
		udj = string(b)
	}
	flag := "0"
	if pathAdded {
		flag = "1"
	}
	strs := [][2]string{
		{"DisplayName", ProductName},
		{"DisplayVersion", display},
		{"Publisher", s.P.Manifest.Product.Publisher},
		{"InstallLocation", root},
		{"DisplayIcon", inst},
		{"UninstallString", q + " /x " + code},
		{"QuietUninstallString", q + " /x " + code + " /quiet /norestart"},
		{"ModifyPath", q + " /i " + code},
		{RecComponents, strings.Join(comps, ",")},
		{RecBuild, plan.Build},
		{RecPathAdded, flag},
		{RecShortcuts, strings.Join(shortcuts, "|")},
		{RecUserData, udj},
	}
	for _, id := range ComponentIDs {
		v, f := "", ""
		if plan.Final(id) {
			v, f = versions[id], strings.Join(files[id], "|")
		}
		strs = append(strs, [2]string{RecVersion(id), v}, [2]string{RecFiles(id), f})
	}
	for _, v := range strs {
		if err := s.Sys.SetString(key, v[0], v[1], false); err != nil {
			return fmt.Errorf("cannot write %s\\%s: %v", key, v[0], err)
		}
	}
	if err := s.Sys.SetDWORD(key, "EstimatedSize", uint32((size+1023)/1024)); err != nil {
		return fmt.Errorf("cannot write %s\\EstimatedSize: %v", key, err)
	}
	return nil
}

// removeLegacy deletes the earlier layout after the new one is in place: its files, shortcuts, installer copy and
// folder, its PATH entry when it added it, and its uninstall entry.
func (s *Setup) removeLegacy(st *State, progs string, res *Result) error {
	l := st.Legacy
	s.logf("taking over the earlier installation in %s (yakbd %s, editor: %v)", l.Dir, l.Version, l.Editor)
	if len(l.Shortcuts) > 0 && progs != "" {
		if err := s.removeShortcuts(progs, l.Shortcuts, res); err != nil {
			return err
		}
	}
	if err := s.removeFiles(l.Dir, append([]string{legacyExe}, l.Extra...), res, true); err != nil {
		return err
	}
	inst := join(l.Dir, InstallerName)
	if samePath(s.Sys.SelfPath(), inst) {
		s.moveAway(inst)
	} else if err := s.Sys.Remove(inst); err != nil && !errors.Is(err, setup.ErrNotFound) {
		s.logf("cannot delete %s: %v", inst, err)
	}
	s.removeDir(l.Dir, res)
	if l.PathAdded {
		if _, err := s.setPath(l.Dir, false); err != nil {
			return err
		}
	}
	if err := s.Sys.DeleteKey(legacyKey()); err != nil {
		return fmt.Errorf("cannot delete the earlier uninstall entry: %v", err)
	}
	s.logf("earlier uninstall entry %s deleted", LegacyCode)
	return nil
}

// moveAway moves a running installer copy out of its folder (deleted at the next restart).
func (s *Setup) moveAway(inst string) {
	tmp := join(s.Sys.TempDir(), "yakbd-uninstall-"+strings.Trim(s.P.Manifest.Product.Code, "{}")+".exe")
	_ = s.Sys.Remove(tmp)
	if err := s.Sys.Rename(inst, tmp); err == nil {
		_ = s.Sys.DeleteAtReboot(tmp)
		s.logf("the running uninstaller moved to %s (deleted at the next restart)", tmp)
	} else {
		_ = s.Sys.DeleteAtReboot(inst)
		s.logf("%s cannot be moved (%v): deleted at the next restart", inst, err)
	}
}

// removeSelf deletes the installer copy and the root folder at the end of an uninstall.
func (s *Setup) removeSelf(root string, res *Result) {
	inst := join(root, InstallerName)
	if samePath(s.Sys.SelfPath(), inst) {
		s.moveAway(inst)
	} else if err := s.Sys.Remove(inst); err != nil && !errors.Is(err, setup.ErrNotFound) {
		s.logf("cannot delete %s: %v", inst, err)
	}
	if err := s.Sys.RemoveDir(root); err != nil {
		_ = s.Sys.DeleteAtReboot(root)
		s.logf("%s: deleted at the next restart", root)
	} else {
		s.logf("%s deleted", root)
	}
}

// describe is the installed state for the log.
func describe(st *State) string {
	var out []string
	for _, id := range ComponentIDs {
		if v := st.VersionOf(id); st.Has(id) {
			out = append(out, id+" "+v)
		}
	}
	if st.Legacy != nil {
		out = append(out, "(earlier layout)")
	}
	if st.PathPresent {
		out = append(out, "on PATH")
	}
	if len(out) == 0 {
		return "nothing"
	}
	return strings.Join(out, ", ")
}
