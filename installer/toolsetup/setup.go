package toolsetup

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/setup"
)

// System is what the tool installer needs from Windows (a subset of installer/setup's System, plus the native
// Program Files folder and the environment broadcast). Registry paths start with `HKLM\`, the 64-bit view.
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
}

// Registry paths and record values.
const (
	Environment   = `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	RecFile       = "YakbdToolFile"
	RecPathAdded  = "YakbdPathAdded"
	RecBuild      = "YakbdToolBuild"
	ExeName       = "yakbd.exe"
	InstallerName = "setup.exe"
)

// Result tells what a run did.
type Result struct {
	Upgraded bool
	// Reboot is set when a file is replaced or deleted at the next restart.
	Reboot bool
	Notes  []string
}

// Setup runs the tool installer's steps for one payload.
type Setup struct {
	Sys System
	P   *Payload
	Log func(format string, args ...any)
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

func (s *Setup) arpKey() string { return setup.UninstallRoot + `\` + s.P.Manifest.Product.Code }

func join(dir, name string) string { return strings.TrimRight(dir, `\/`) + `\` + name }

// Dir is the install folder, %ProgramW6432%\YAKBD\yakbd.
func (s *Setup) Dir() (string, error) {
	pf, err := s.Sys.NativeProgramFiles()
	if err != nil {
		return "", fmt.Errorf("cannot find Program Files: %v", err)
	}
	return join(join(pf, "YAKBD"), "yakbd"), nil
}

func (s *Setup) values(p string) map[string]setup.Value {
	vs, err := s.Sys.Values(p)
	if err != nil {
		return nil
	}
	m := map[string]setup.Value{}
	for _, v := range vs {
		m[strings.ToLower(v.Name)] = v
	}
	return m
}

// Installed reports whether the tool is installed (its uninstall entry has the record).
func (s *Setup) Installed() bool {
	return s.values(s.arpKey())[strings.ToLower(RecFile)].Str != ""
}

func samePath(a, b string) bool {
	n := func(p string) string { return strings.ToLower(strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)) }
	return n(a) == n(b)
}

// pathEntries splits the PATH value; has reports whether dir is one of them.
func pathEntries(v string) []string { return strings.Split(v, ";") }

func pathHas(v, dir string) bool {
	for _, e := range pathEntries(v) {
		if samePath(strings.TrimSpace(e), dir) {
			return true
		}
	}
	return false
}

// readPath returns the machine Path value and whether it is REG_EXPAND_SZ.
func (s *Setup) readPath() (string, bool, bool) {
	v, ok := s.values(Environment)["path"]
	return v.Str, v.Type == setup.RegExpandSZ, ok
}

// pendingDeletion returns a PendingFileRenameOperations entry that deletes the target or its folder at restart.
func (s *Setup) pendingDeletion(dir string) string {
	for _, v := range s.values(setup.SessionManager) {
		if !strings.EqualFold(v.Name, "PendingFileRenameOperations") || v.Type != setup.RegMultiSZ {
			continue
		}
		parts := strings.Split(v.Str, "\x00")
		for i := 0; i+1 < len(parts); i += 2 {
			src := strings.TrimPrefix(parts[i], `\??\`)
			if parts[i+1] == "" && (samePath(src, join(dir, ExeName)) || samePath(src, dir)) {
				return src
			}
		}
	}
	return ""
}

// Install installs, upgrades in place, or repairs the tool (docs/installer-cli.md §6, §12).
func (s *Setup) Install() (*Result, error) {
	if !s.Sys.Elevated() {
		return nil, fail(cmdline.ElevationRequired, "Administrator rights are needed (run the installer elevated).")
	}
	m := s.P.Manifest
	native, err := s.Sys.NativeMachine()
	if err != nil {
		return nil, fmt.Errorf("cannot tell the machine's architecture: %v", err)
	}
	build, ok := BuildFor[native]
	if _, has := s.P.Data[build]; !ok || !has {
		return nil, fail(cmdline.PlatformUnsupported, "This installer has no build of yakbd for this computer (machine %#04x); it carries %s. Nothing was changed.", native, strings.Join(m.Builds(), ", "))
	}
	installed := s.Installed()
	arp := s.values(s.arpKey())
	if installed && cmdline.CompareVersions(arp["displayversion"].Str, m.Product.Version) > 0 {
		return nil, fail(cmdline.ProductVersion, "yakbd %s is installed, newer than this installer's %s. To go back, uninstall it first. Nothing was changed.", arp["displayversion"].Str, m.Product.Version)
	}
	dir, err := s.Dir()
	if err != nil {
		return nil, err
	}
	if p := s.pendingDeletion(dir); p != "" {
		return nil, fail(cmdline.InstallFailure, "%s will be deleted when Windows restarts (an earlier uninstall). Restart Windows, then run the installer again. Nothing was changed.", p)
	}
	s.logf("install yakbd %s (%s build) into %s, installed before: %v", m.Product.Version, build, dir, installed)
	res := &Result{Upgraded: installed}
	var undo []func()
	rollback := func(err error) (*Result, error) {
		if !installed {
			s.logf("failed: %v; rolling back", err)
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
		return nil, err
	}
	if err := s.Sys.MkdirAll(dir); err != nil {
		return nil, fmt.Errorf("cannot create %s: %v", dir, err)
	}
	undo = append(undo, func() { _ = s.Sys.RemoveDir(dir); _ = s.Sys.RemoveDir(strings.TrimSuffix(dir, `\yakbd`)) })

	// 1. yakbd.exe: written beside, renamed over, read back; in use: replaced at the next restart.
	exe := join(dir, ExeName)
	data := s.P.Data[build]
	old, rerr := s.Sys.ReadFile(exe)
	switch {
	case rerr == nil && bytes.Equal(old, data):
		s.logf("%s: already present and identical", exe)
	default:
		tmp := exe + ".yakbd-tmp"
		if err := s.Sys.WriteFile(tmp, data); err != nil {
			return rollback(fmt.Errorf("cannot write %s: %v", tmp, err))
		}
		if err := s.Sys.Rename(tmp, exe); err != nil {
			_ = s.Sys.Remove(tmp)
			if !errors.Is(err, setup.ErrInUse) {
				return rollback(fmt.Errorf("cannot write %s: %v", exe, err))
			}
			next := exe + ".yakbd-new"
			if err := s.Sys.WriteFile(next, data); err != nil {
				return rollback(fmt.Errorf("cannot write %s: %v", next, err))
			}
			if err := s.Sys.MoveAtReboot(next, exe); err != nil {
				return rollback(fmt.Errorf("cannot schedule the replacement of %s: %v", exe, err))
			}
			s.logf("%s is in use: replaced at the next restart", exe)
			res.Reboot = true
		} else {
			if rerr != nil {
				undo = append(undo, func() { _ = s.Sys.Remove(exe) })
			}
			if back, err := s.Sys.ReadFile(exe); err != nil || !bytes.Equal(back, data) {
				return rollback(fmt.Errorf("%s did not read back as written", exe))
			}
			s.logf("wrote %s (%s, %d bytes)", exe, build, len(data))
		}
	}

	// 2. The installer itself, for the uninstall entry.
	inst := join(dir, InstallerName)
	self, err := s.Sys.ReadFile(s.Sys.SelfPath())
	if err != nil {
		return rollback(fmt.Errorf("cannot read the installer itself: %v", err))
	}
	if !samePath(s.Sys.SelfPath(), inst) {
		if err := s.Sys.WriteFile(inst, self); err != nil {
			if !errors.Is(err, setup.ErrInUse) {
				return rollback(fmt.Errorf("cannot write %s: %v", inst, err))
			}
			s.logf("%s is in use (an uninstaller running?): kept", inst)
		} else {
			undo = append(undo, func() { _ = s.Sys.Remove(inst) })
		}
	}

	// 3. PATH.
	added := arp[strings.ToLower(RecPathAdded)].Str == "1"
	pv, expand, exists := s.readPath()
	switch {
	case !exists:
		s.logf("PATH: the machine Path value cannot be read; not changed")
	case pathHas(pv, dir):
		s.logf("PATH has %s already (added by this installer: %v)", dir, added)
	case installed && !added:
		s.logf("PATH: %s was not added by this installer and is not there; left alone", dir)
	default:
		nv := addPath(pv, dir)
		if err := s.Sys.SetString(Environment, "Path", nv, expand); err != nil {
			return rollback(fmt.Errorf("cannot write the machine PATH: %v", err))
		}
		undo = append(undo, func() { _ = s.Sys.SetString(Environment, "Path", pv, expand) })
		added = true
		s.logf("PATH: %s appended", dir)
		if err := s.Sys.BroadcastEnvironment(); err != nil {
			s.logf("environment broadcast: %v", err)
		}
		res.Notes = append(res.Notes, "Programs started from now on find yakbd on the PATH.")
	}

	// 4. Uninstall entry.
	key := s.arpKey()
	created, err := s.Sys.CreateKey(key)
	if err != nil {
		return rollback(fmt.Errorf("cannot create the uninstall entry %s: %v", key, err))
	}
	if created {
		undo = append(undo, func() { _ = s.Sys.DeleteKey(key) })
	}
	q := `"` + inst + `"`
	flag := "0"
	if added {
		flag = "1"
	}
	strs := [][2]string{
		{"DisplayName", m.Product.Name},
		{"DisplayVersion", m.Product.Version},
		{"Publisher", m.Product.Publisher},
		{"InstallLocation", dir},
		{"DisplayIcon", inst},
		{"UninstallString", q + " /x " + m.Product.Code},
		{"QuietUninstallString", q + " /x " + m.Product.Code + " /quiet /norestart"},
		{"ModifyPath", q + " /f " + m.Product.Code},
		{RecFile, ExeName},
		{RecBuild, build},
		{RecPathAdded, flag},
	}
	for _, v := range strs {
		if err := s.Sys.SetString(key, v[0], v[1], false); err != nil {
			return rollback(fmt.Errorf("cannot write %s\\%s: %v", key, v[0], err))
		}
	}
	if err := s.Sys.SetDWORD(key, "EstimatedSize", uint32((len(data)+len(self)+1023)/1024)); err != nil {
		return rollback(fmt.Errorf("cannot write %s\\EstimatedSize: %v", key, err))
	}
	s.logf("uninstall entry %s written", key)
	if res.Reboot {
		res.Notes = append(res.Notes, "yakbd.exe is in use; restart Windows to finish.")
	}
	return res, nil
}

// Uninstall removes the tool: yakbd.exe, its PATH entry when this installer added it, the uninstall entry and
// the folder (docs/installer-cli.md §12).
func (s *Setup) Uninstall() (*Result, error) {
	if !s.Sys.Elevated() {
		return nil, fail(cmdline.ElevationRequired, "Administrator rights are needed (run the installer elevated).")
	}
	if !s.Installed() {
		return nil, fail(cmdline.UnknownProduct, "yakbd is not installed; nothing was changed.")
	}
	arp := s.values(s.arpKey())
	dir := arp["installlocation"].Str
	if dir == "" {
		var err error
		if dir, err = s.Dir(); err != nil {
			return nil, err
		}
	}
	res := &Result{}
	exe := join(dir, ExeName)
	for _, ext := range []string{".yakbd-new", ".yakbd-tmp"} {
		if err := s.Sys.Remove(exe + ext); err == nil {
			s.logf("%s deleted", exe+ext)
		}
	}
	if err := s.Sys.Remove(exe); err != nil && !errors.Is(err, setup.ErrNotFound) {
		if !errors.Is(err, setup.ErrInUse) {
			return nil, fmt.Errorf("cannot delete %s: %v", exe, err)
		}
		if err := s.Sys.DeleteAtReboot(exe); err != nil {
			return nil, fmt.Errorf("cannot schedule the deletion of %s: %v", exe, err)
		}
		res.Reboot = true
		s.logf("%s is in use: deleted at the next restart", exe)
	} else {
		s.logf("%s deleted", exe)
	}

	if arp[strings.ToLower(RecPathAdded)].Str == "1" {
		pv, expand, _ := s.readPath()
		if nv := removePath(pv, dir); nv != pv {
			if err := s.Sys.SetString(Environment, "Path", nv, expand); err != nil {
				return nil, fmt.Errorf("cannot write the machine PATH: %v", err)
			}
			s.logf("PATH: %s removed", dir)
			if err := s.Sys.BroadcastEnvironment(); err != nil {
				s.logf("environment broadcast: %v", err)
			}
		}
	}

	if err := s.Sys.DeleteKey(s.arpKey()); err != nil {
		return nil, fmt.Errorf("cannot delete the uninstall entry: %v", err)
	}
	s.logf("uninstall entry deleted")

	inst := join(dir, InstallerName)
	if samePath(s.Sys.SelfPath(), inst) {
		tmp := join(s.Sys.TempDir(), "yakbd-uninstall-"+strings.Trim(s.P.Manifest.Product.Code, "{}")+".exe")
		_ = s.Sys.Remove(tmp)
		if err := s.Sys.Rename(inst, tmp); err == nil {
			_ = s.Sys.DeleteAtReboot(tmp)
			s.logf("the running uninstaller moved to %s (deleted at the next restart)", tmp)
		} else {
			_ = s.Sys.DeleteAtReboot(inst)
			s.logf("%s cannot be moved (%v): deleted at the next restart", inst, err)
		}
	} else if err := s.Sys.Remove(inst); err != nil && !errors.Is(err, setup.ErrNotFound) {
		s.logf("cannot delete %s: %v", inst, err)
	}
	if err := s.Sys.RemoveDir(dir); err != nil {
		_ = s.Sys.DeleteAtReboot(dir) // still holds a file that waits for the restart
		s.logf("%s: deleted at the next restart", dir)
	} else {
		s.logf("%s deleted", dir)
	}
	_ = s.Sys.RemoveDir(strings.TrimSuffix(strings.TrimRight(dir, `\`), `\yakbd`)) // YAKBD, only when empty
	if res.Reboot {
		res.Notes = append(res.Notes, "yakbd.exe is in use; restart Windows to finish removing it.")
	}
	return res, nil
}

// addPath appends dir to a PATH value so that removePath gives the value back exactly: "a;b" -> "a;b;dir",
// "a;b;" -> "a;b;dir;", "" -> "dir".
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
	for _, e := range pathEntries(pv) {
		if !samePath(strings.TrimSpace(e), dir) {
			keep = append(keep, e)
		}
	}
	return strings.Join(keep, ";")
}
