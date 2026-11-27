package setup

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/payload"
)

// Exit codes of the installer program: Windows Installer's (docs/installer-cli.md §4, installer/cmdline).
const (
	ExitOK           = cmdline.Success
	ExitFailed       = cmdline.InstallFailure      // failed, rolled back, or refused with nothing changed
	ExitUsage        = cmdline.InvalidCommandLine  // bad command line
	ExitMachine      = cmdline.PlatformUnsupported // no build for this machine
	ExitNeedAdmin    = cmdline.ElevationRequired   // not elevated and no UI allowed
	ExitRefused      = cmdline.InstallFailure      // uninstall refused: the only layout of a user
	ExitEnable       = cmdline.InstallFailure      // installed, but enabling for the user failed
	ExitDamaged      = cmdline.PackageInvalid      // a payload file fails its check
	ExitNotInstalled = cmdline.UnknownProduct      // uninstall or repair of a product that is not installed
	ExitNewer        = cmdline.ProductVersion      // a newer version is installed
	ExitCancelled    = cmdline.UserExit
	ExitReboot       = cmdline.RebootRequired
)

// Registry paths.
const (
	KeyboardLayouts = `HKLM\SYSTEM\CurrentControlSet\Control\Keyboard Layouts`
	UninstallRoot   = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	Users           = `HKU`
)

// Names of the installer's own record in its uninstall key (docs/installer.md §2.3).
const (
	RecKLID     = "YakbdKLID"
	RecLayoutID = "YakbdLayoutId"
	RecFile     = "YakbdLayoutFile"
	RecLangID   = "YakbdLangId"
)

// Error is a failure with the installer's exit code.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func fail(code int, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// ExitCode maps an error of this package to the exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ExitFailed
}

// Result tells what a run did.
type Result struct {
	KLID     string
	LayoutID string
	Upgraded bool
	// Reboot is set when a file is replaced or deleted at the next restart.
	Reboot bool
	// SignInFailed is set when putting the layout on the sign-in screen and into the Default profile failed
	// (kbl targets/windows-install.md §7.1): the run goes on with the user's enabling and ends with its failure code.
	SignInFailed error
	// Notes are sentences for the person who ran the installer.
	Notes []string
}

func (r *Result) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// Setup runs the installer's steps for one payload.
type Setup struct {
	Sys System
	P   *payload.Payload
	// Log receives one line per step; nil discards.
	Log func(format string, args ...any)
	// Sleep waits between tries; nil does not wait (tests).
	Sleep func(time.Duration)
	// CheckFile checks that a payload file has its build's Machine and shape (installer/shape.Check, kbl
	// targets/windows-install.md §3); nil checks the PE Machine only (the tests' stand-in files).
	CheckFile func(build string, data []byte) error
	// SignIn is SIGNIN=0|1 from the command line, nil when not given: it overrides the manifest on a fresh install
	// and the recorded state on an upgrade or repair (kbl targets/windows-install.md §7.1).
	SignIn *bool
	// filedTag is the tag Windows filed the language under in .DEFAULT on the LocalSystem route (D98), during a run.
	filedTag string
}

// checkFile runs CheckFile, or the Machine check when it is nil.
func (s *Setup) checkFile(build string, data []byte) error {
	if s.CheckFile != nil {
		return s.CheckFile(build, data)
	}
	if mach, err := payload.PEMachine(data); err != nil || mach != payload.MachineOf[build] {
		return fmt.Errorf("the %s file does not have Machine %04X", build, payload.MachineOf[build])
	}
	return nil
}

// checkFiles checks every file the machine gets before anything is written (inst §10 step 1).
func (s *Setup) checkFiles(pl []Placement) error {
	for _, p := range pl {
		if err := s.checkFile(p.Build, s.P.Data[p.Build]); err != nil {
			s.logf("refused: %v", err)
			return fail(ExitDamaged, "This installer is damaged: %v. Nothing was changed.", err)
		}
		s.logf("%s file: Machine and shape checked", p.Build)
	}
	return nil
}

func (s *Setup) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

func (s *Setup) m() *payload.Manifest { return s.P.Manifest }

// Placement is one file of an install: a build into a system directory.
type Placement struct {
	Build string
	Dir   DirKind
}

// Plan gives the files a machine needs (inst §2.1, §2.4; docs/installer.md §1). It never falls back to another
// build.
func Plan(native uint16, m *payload.Manifest) ([]Placement, error) {
	var want []Placement
	var kind string
	switch native {
	case payload.MachineI386:
		kind, want = "x86", []Placement{{payload.BuildX86, DirNative}}
	case payload.MachineAMD64:
		kind, want = "x64", []Placement{{payload.BuildX64, DirNative}, {payload.BuildWOW64, DirWOW64}}
	case payload.MachineARM64:
		if !m.Has(payload.BuildARM64X) {
			return nil, fail(ExitMachine, "This installer has no build for Windows on ARM (ARM64X); it carries %s. Nothing was changed.", strings.Join(m.Builds(), ", "))
		}
		kind, want = "ARM64", []Placement{{payload.BuildARM64X, DirNative}, {payload.BuildWOW64, DirWOW64}}
	default:
		return nil, fail(ExitMachine, "This computer's processor (machine %#04x) is not supported. Nothing was changed.", native)
	}
	for _, p := range want {
		if !m.Has(p.Build) {
			return nil, fail(ExitMachine, "This installer has no %s build for %s Windows; it carries %s. Nothing was changed.", p.Build, kind, strings.Join(m.Builds(), ", "))
		}
	}
	return want, nil
}

func (s *Setup) placements() ([]Placement, uint16, error) {
	native, err := s.Sys.NativeMachine()
	if err != nil {
		return nil, 0, fmt.Errorf("cannot tell the machine's architecture: %v", err)
	}
	pl, err := Plan(native, s.m())
	return pl, native, err
}

func (s *Setup) requireAdmin() error {
	if !s.Sys.Elevated() {
		return fail(ExitNeedAdmin, "Administrator rights are needed (run the installer elevated).")
	}
	return nil
}

func klidKey(klid string) string { return KeyboardLayouts + `\` + klid }

func (s *Setup) arpKey() string { return UninstallRoot + `\` + s.m().Product.Code }

func valueMap(vs []Value) map[string]Value {
	m := map[string]Value{}
	for _, v := range vs {
		m[strings.ToLower(v.Name)] = v
	}
	return m
}

func (s *Setup) values(p string) map[string]Value {
	vs, err := s.Sys.Values(p)
	if err != nil {
		return nil
	}
	return valueMap(vs)
}

// ParseLayoutID reads a Layout Id value the way Windows does (inst §3.3): hexadecimal text of any length, with
// or without 0x, in either case; a DWORD, an empty or non-hex string count as 0. Values above 0xFFF keep their low
// 12 bits.
func ParseLayoutID(v Value) uint16 {
	if v.Type != RegSZ && v.Type != RegExpandSZ {
		return 0
	}
	t := strings.TrimSpace(v.Str)
	t = strings.TrimPrefix(strings.TrimPrefix(t, "0x"), "0X")
	if t == "" {
		return 0
	}
	n, err := strconv.ParseUint(t, 16, 64)
	if err != nil {
		return 0
	}
	return uint16(n & 0xFFF)
}

// klidInfo is one subkey of Keyboard Layouts.
type klidInfo struct {
	name     string
	layoutID uint16
	hasID    bool
	product  string
	file     string
}

func (s *Setup) scanKLIDs() ([]klidInfo, error) {
	names, err := s.Sys.SubKeys(KeyboardLayouts)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %v", KeyboardLayouts, err)
	}
	var out []klidInfo
	for _, n := range names {
		vm := s.values(klidKey(n))
		k := klidInfo{name: n}
		if v, ok := vm["layout id"]; ok {
			k.layoutID, k.hasID = ParseLayoutID(v), true
		}
		if v, ok := vm["layout product code"]; ok {
			k.product = v.Str
		}
		if v, ok := vm["layout file"]; ok {
			k.file = v.Str
		}
		out = append(out, k)
	}
	return out, nil
}

// usedIDs is the set of Layout Ids in use, overall and per LANGID (the low word of the KLID). A KLID with a
// non-zero high word and no Layout Id uses 0xFFE (HKL high word FFFE, inst §3.3).
func usedIDs(ks []klidInfo, except string) (all map[uint16]bool, perLang map[string]map[uint16]bool) {
	all, perLang = map[uint16]bool{}, map[string]map[uint16]bool{}
	for _, k := range ks {
		if strings.EqualFold(k.name, except) || len(k.name) != 8 {
			continue
		}
		id := k.layoutID
		if !k.hasID {
			if strings.HasPrefix(k.name, "0000") {
				continue
			}
			id = 0xFFE
		}
		lang := strings.ToLower(k.name[4:])
		if perLang[lang] == nil {
			perLang[lang] = map[uint16]bool{}
		}
		all[id], perLang[lang][id] = true, true
	}
	return all, perLang
}

// AllocateLayoutID gives the highest free id from 0x00FF down to 0x00C0 (inst §3.3, §10).
func AllocateLayoutID(used map[uint16]bool) (uint16, bool) {
	for id := uint16(0xFF); id >= 0xC0; id-- {
		if !used[id] {
			return id, true
		}
	}
	return 0, false
}

// hiveRefs is what one user hive holds about a set of KLIDs.
type hiveRefs struct {
	hive        string
	preload     []Value           // Preload values, in numeric order
	subst       map[string]string // Substitutes: name (lower) -> data (lower)
	profileVals []string          // "<tag>\<LLLL:KLID>" User Profile entries naming one of the KLIDs
	refs        bool
}

func (s *Setup) hives() []string {
	names, _ := s.Sys.SubKeys(Users)
	var out []string
	for _, n := range names {
		if !strings.HasSuffix(strings.ToLower(n), "_classes") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func preloadOrder(vs []Value) []Value {
	var out []Value
	for _, v := range vs {
		if _, err := strconv.Atoi(v.Name); err == nil {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].Name)
		b, _ := strconv.Atoi(out[j].Name)
		return a < b
	})
	return out
}

// refsIn reads the references of one hive to the KLIDs in set (lower case).
func (s *Setup) refsIn(hive string, set map[string]bool) hiveRefs {
	h := hiveRefs{hive: hive, subst: map[string]string{}}
	base := Users + `\` + hive
	if vs, err := s.Sys.Values(base + `\Keyboard Layout\Preload`); err == nil {
		h.preload = preloadOrder(vs)
	}
	if vs, err := s.Sys.Values(base + `\Keyboard Layout\Substitutes`); err == nil {
		for _, v := range vs {
			h.subst[strings.ToLower(v.Name)] = strings.ToLower(v.Str)
		}
	}
	for _, v := range h.preload {
		if set[strings.ToLower(v.Str)] {
			h.refs = true
		}
	}
	for _, d := range h.subst {
		if set[d] {
			h.refs = true
		}
	}
	up := base + `\Control Panel\International\User Profile`
	tags, _ := s.Sys.SubKeys(up)
	for _, t := range tags {
		vs, _ := s.Sys.Values(up + `\` + t)
		for _, v := range vs {
			if i := strings.IndexByte(v.Name, ':'); i >= 0 && set[strings.ToLower(v.Name[i+1:])] {
				h.profileVals = append(h.profileVals, t+`\`+v.Name)
				h.refs = true
			}
		}
	}
	return h
}

// onlyLayout reports whether every Preload entry of the hive resolves (through Substitutes) to a KLID in set.
func (h hiveRefs) onlyLayout(set map[string]bool) bool {
	if len(h.preload) == 0 {
		return false
	}
	for _, v := range h.preload {
		k := strings.ToLower(v.Str)
		if d, ok := h.subst[k]; ok {
			k = d
		}
		if !set[k] {
			return false
		}
	}
	return true
}

// referenced reports whether any loaded hive refers to the KLID (inst §3.1, §10: never reuse such a KLID).
func (s *Setup) referenced(klid string) bool {
	set := map[string]bool{strings.ToLower(klid): true}
	for _, h := range s.hives() {
		if s.refsIn(h, set).refs {
			return true
		}
	}
	return false
}

// record is the installer's own record in its uninstall key.
type record struct{ klid, layoutID, file, langID string }

func (s *Setup) readRecord() (record, bool) {
	vm := s.values(s.arpKey())
	if vm == nil {
		return record{}, false
	}
	r := record{klid: vm[strings.ToLower(RecKLID)].Str, layoutID: vm[strings.ToLower(RecLayoutID)].Str,
		file: vm[strings.ToLower(RecFile)].Str, langID: vm[strings.ToLower(RecLangID)].Str}
	return r, r.klid != ""
}

// HKL is the keyboard layout handle Windows gives a KLID with a Layout Id (inst §3.2): MAKELONG(LANGID,
// 0xF000 | id), the id cut to 12 bits.
func HKL(langID, layoutID uint16) uint32 {
	return uint32(langID) | uint32(0xF000|layoutID&0xFFF)<<16
}

func hex16(s string) uint16 {
	n, _ := strconv.ParseUint(s, 16, 16)
	return uint16(n)
}

func (s *Setup) targetPath(d DirKind) (string, error) {
	dir, err := s.Sys.Dir(d)
	if err != nil {
		return "", fmt.Errorf("cannot find %s: %v", d, err)
	}
	return joinPath(dir, s.m().Layout.File), nil
}

func joinPath(dir, name string) string {
	return strings.TrimRight(dir, `\/`) + `\` + name
}

func (s *Setup) installDir() (string, error) {
	pf, err := s.Sys.ProgramFiles()
	if err != nil {
		return "", fmt.Errorf("cannot find Program Files: %v", err)
	}
	return joinPath(joinPath(pf, "YAKBD"), s.m().Product.Code), nil
}

// samePath compares two Windows paths, ignoring case and the kind of slash.
func samePath(a, b string) bool {
	n := func(p string) string {
		return strings.ToLower(path.Clean(strings.ReplaceAll(p, `\`, "/")))
	}
	return n(a) == n(b)
}

// Installed reports whether the product is installed: its uninstall entry holds the record, or a KLID carries its
// product code. It needs no elevation.
func (s *Setup) Installed() bool {
	if _, ok := s.readRecord(); ok {
		return true
	}
	ks, _ := s.scanKLIDs()
	for _, k := range ks {
		if strings.EqualFold(k.product, s.m().Product.Code) {
			return true
		}
	}
	return false
}
