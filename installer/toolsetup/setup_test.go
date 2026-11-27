package toolsetup

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/setup"
)

// fake is an in-memory Windows for the tool installer.
type fake struct {
	machine   uint16
	elevated  bool
	self      string
	files     map[string][]byte // lower-case path
	dirs      map[string]bool
	keys      map[string]map[string]setup.Value // lower-case key -> lower-case name
	inUse     map[string]bool
	atReboot  []string
	broadcast int
	noProgs   bool   // CommonPrograms fails
	linkFail  string // CreateShortcut fails for a shortcut file ending in this
}

func newFake() *fake {
	f := &fake{machine: payload.MachineAMD64, elevated: true, self: `C:\Users\t\Downloads\yakbd-setup.exe`,
		files: map[string][]byte{}, dirs: map[string]bool{}, keys: map[string]map[string]setup.Value{}, inUse: map[string]bool{}}
	f.files[strings.ToLower(f.self)] = []byte("MZ yakbd-setup itself")
	for _, d := range []string{`c:\program files`, `c:\windows\temp`, `c:\users\t\downloads`, strings.ToLower(progsDir)} {
		f.dirs[d] = true
	}
	f.keys[strings.ToLower(Environment)] = map[string]setup.Value{"path": {Name: "Path", Type: setup.RegExpandSZ, Str: `%SystemRoot%\system32;%SystemRoot%`}}
	return f
}

func (f *fake) NativeMachine() (uint16, error)      { return f.machine, nil }
func (f *fake) Elevated() bool                      { return f.elevated }
func (f *fake) NativeProgramFiles() (string, error) { return `C:\Program Files`, nil }
func (f *fake) TempDir() string                     { return `C:\Windows\Temp` }
func (f *fake) SelfPath() string                    { return f.self }
func (f *fake) ReadFile(p string) ([]byte, error) {
	if b, ok := f.files[strings.ToLower(p)]; ok {
		return b, nil
	}
	return nil, setup.ErrNotFound
}

// parentExists reports whether the folder of p exists (Windows creates no folder when it writes a file).
func (f *fake) parentExists(p string) bool {
	l := strings.ToLower(p)
	i := strings.LastIndex(l, `\`)
	return i > 0 && f.dirs[l[:i]]
}

func (f *fake) WriteFile(p string, d []byte) error {
	if f.inUse[strings.ToLower(p)] {
		return setup.ErrInUse
	}
	if !f.parentExists(p) {
		return setup.ErrNotFound
	}
	f.files[strings.ToLower(p)] = append([]byte(nil), d...)
	return nil
}
func (f *fake) Rename(from, to string) error {
	if f.inUse[strings.ToLower(to)] || f.inUse[strings.ToLower(from)] {
		return setup.ErrInUse
	}
	b, ok := f.files[strings.ToLower(from)]
	if !ok || !f.parentExists(to) {
		return setup.ErrNotFound
	}
	delete(f.files, strings.ToLower(from))
	f.files[strings.ToLower(to)] = b
	return nil
}
func (f *fake) Remove(p string) error {
	if f.inUse[strings.ToLower(p)] {
		return setup.ErrInUse
	}
	if _, ok := f.files[strings.ToLower(p)]; !ok {
		return setup.ErrNotFound
	}
	delete(f.files, strings.ToLower(p))
	return nil
}
func (f *fake) MkdirAll(p string) error {
	l := strings.ToLower(strings.TrimRight(p, `\`))
	if _, ok := f.files[l]; ok {
		return fmt.Errorf("%s is a file", p)
	}
	for ; strings.Contains(l, `\`); l = l[:strings.LastIndex(l, `\`)] {
		f.dirs[l] = true
	}
	return nil
}
func (f *fake) RemoveDir(p string) error {
	l := strings.ToLower(strings.TrimRight(p, `\`))
	for k := range f.files {
		if strings.HasPrefix(k, l+`\`) {
			return fmt.Errorf("not empty")
		}
	}
	for k := range f.dirs {
		if k != l && strings.HasPrefix(k, l+`\`) {
			return fmt.Errorf("not empty")
		}
	}
	delete(f.dirs, l)
	return nil
}
func (f *fake) DeleteAtReboot(p string) error {
	f.atReboot = append(f.atReboot, "delete "+strings.ToLower(p))
	return nil
}
func (f *fake) MoveAtReboot(from, to string) error {
	f.atReboot = append(f.atReboot, "move "+strings.ToLower(from)+" "+strings.ToLower(to))
	return nil
}
func (f *fake) CreateKey(p string) (bool, error) {
	if f.keys[strings.ToLower(p)] != nil {
		return false, nil
	}
	f.keys[strings.ToLower(p)] = map[string]setup.Value{}
	return true, nil
}
func (f *fake) DeleteKey(p string) error { delete(f.keys, strings.ToLower(p)); return nil }
func (f *fake) Values(p string) ([]setup.Value, error) {
	k := f.keys[strings.ToLower(p)]
	if k == nil {
		return nil, setup.ErrNotFound
	}
	var out []setup.Value
	for _, v := range k {
		out = append(out, v)
	}
	return out, nil
}
func (f *fake) SetString(p, name, value string, expand bool) error {
	k := f.keys[strings.ToLower(p)]
	if k == nil {
		return setup.ErrNotFound
	}
	t := setup.RegSZ
	if expand {
		t = setup.RegExpandSZ
	}
	k[strings.ToLower(name)] = setup.Value{Name: name, Type: t, Str: value}
	return nil
}
func (f *fake) SetDWORD(p, name string, v uint32) error {
	k := f.keys[strings.ToLower(p)]
	if k == nil {
		return setup.ErrNotFound
	}
	k[strings.ToLower(name)] = setup.Value{Name: name, Type: setup.RegDWORD, DWord: v}
	return nil
}
func (f *fake) BroadcastEnvironment() error { f.broadcast++; return nil }
func (f *fake) CommonPrograms() (string, error) {
	if f.noProgs {
		return "", fmt.Errorf("no such folder")
	}
	return progsDir, nil
}

// CreateShortcut stores a shortcut as a file "lnk TARGET | DIR"; the target must exist, as SetPath needs no file
// but the test wants to see the target placed first.
func (f *fake) CreateShortcut(lnk, target, dir string) error {
	if f.linkFail != "" && strings.HasSuffix(lnk, f.linkFail) {
		return fmt.Errorf("CoCreateInstance failed")
	}
	if _, ok := f.files[strings.ToLower(target)]; !ok {
		return fmt.Errorf("target %s missing", target)
	}
	return f.WriteFile(lnk, []byte("lnk "+target+" | "+dir))
}

const progsDir = `C:\ProgramData\Microsoft\Windows\Start Menu\Programs`

func (f *fake) snapshot() string {
	var out []string
	for k, v := range f.files {
		out = append(out, "file "+k+" "+string(v))
	}
	for k := range f.dirs {
		out = append(out, "dir "+k)
	}
	for k, vs := range f.keys {
		for n, v := range vs {
			out = append(out, fmt.Sprintf("reg %s %s %d %q %d", k, n, v.Type, v.Str, v.DWord))
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// pe returns a minimal file with a PE header of the machine.
func pe(machine uint16, tag string) []byte {
	b := make([]byte, 0x100)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x80)
	copy(b[0x80:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x84:], machine)
	return append(b, tag...)
}

const code = "{EBE1EAEC-C574-5473-8489-B3FF22FC6506}"

func newSetup(t *testing.T, f *fake, version string, builds ...string) *Setup {
	t.Helper()
	if len(builds) == 0 {
		builds = []string{BuildX86, BuildX64, BuildARM64}
	}
	data := map[string][]byte{}
	for _, b := range builds {
		data[b] = pe(MachineOf[b], b+" "+version)
	}
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: version, Publisher: "The YAKBD contributors", Code: code}}, builds, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	return &Setup{Sys: f, P: p, Log: t.Logf}
}

func wantCode(t *testing.T, err error, c int) {
	t.Helper()
	if got := ExitCode(err); got != c {
		t.Fatalf("exit code %d (%v), want %d", got, err, c)
	}
}

const dir = `c:\program files\yakbd\yakbd`

func TestInstallUninstall(t *testing.T) {
	for _, m := range []struct {
		machine uint16
		build   string
	}{{payload.MachineAMD64, "x64"}, {payload.MachineI386, "x86"}, {payload.MachineARM64, "arm64"}} {
		f := newFake()
		f.machine = m.machine
		before := f.snapshot()
		s := newSetup(t, f, "0.2.0.0")
		if _, err := s.Install(); err != nil {
			t.Fatal(err)
		}
		if got := string(f.files[dir+`\yakbd.exe`]); !strings.HasSuffix(got, m.build+" 0.2.0.0") {
			t.Errorf("%s: yakbd.exe is %q", m.build, got)
		}
		if f.files[dir+`\setup.exe`] == nil {
			t.Error("no setup.exe copy")
		}
		pv := f.keys[strings.ToLower(Environment)]["path"]
		if pv.Str != `%SystemRoot%\system32;%SystemRoot%;C:\Program Files\YAKBD\yakbd` || pv.Type != setup.RegExpandSZ || f.broadcast != 1 {
			t.Errorf("PATH %+v, broadcasts %d", pv, f.broadcast)
		}
		arp := f.keys[strings.ToLower(setup.UninstallRoot+`\`+code)]
		if arp["quietuninstallstring"].Str != `"C:\Program Files\YAKBD\yakbd\setup.exe" /x `+code+` /quiet /norestart` ||
			arp["modifypath"].Str != `"C:\Program Files\YAKBD\yakbd\setup.exe" /f `+code || arp["yakbdpathadded"].Str != "1" ||
			arp["yakbdtoolbuild"].Str != m.build {
			t.Errorf("uninstall entry %v", arp)
		}
		for _, n := range []string{"windowsinstaller", "nomodify", "norepair"} {
			if _, ok := arp[n]; ok {
				t.Errorf("entry has %s", n)
			}
		}
		if _, err := s.Uninstall(); err != nil {
			t.Fatal(err)
		}
		if after := f.snapshot(); after != before {
			t.Errorf("%s: uninstall left:\n%s\nbefore:\n%s", m.build, after, before)
		}
		_, err := s.Uninstall()
		wantCode(t, err, cmdline.UnknownProduct)
	}
}

func TestVersionsAndRepair(t *testing.T) {
	f := newFake()
	if _, err := newSetup(t, f, "0.3.0.0").Install(); err != nil {
		t.Fatal(err)
	}
	installed := f.snapshot()
	_, err := newSetup(t, f, "0.2.0.0").Install()
	wantCode(t, err, cmdline.ProductVersion)
	if f.snapshot() != installed {
		t.Error("a refused downgrade changed something")
	}
	// Repair: yakbd.exe deleted by hand, the same version brings it back; PATH not added twice.
	delete(f.files, dir+`\yakbd.exe`)
	r, err := newSetup(t, f, "0.3.0.0").Install()
	if err != nil || !r.Upgraded {
		t.Fatalf("%+v %v", r, err)
	}
	if f.snapshot() != installed {
		t.Error("repair did not restore the same state")
	}
	if strings.Count(f.keys[strings.ToLower(Environment)]["path"].Str, "YAKBD") != 1 {
		t.Error("PATH entry twice")
	}
	// Upgrade with yakbd.exe running: replaced at the restart.
	f.inUse[dir+`\yakbd.exe`] = true
	r, err = newSetup(t, f, "0.4.0.0").Install()
	if err != nil || !r.Reboot || len(f.atReboot) != 1 || !strings.HasPrefix(f.atReboot[0], "move "+dir+`\yakbd.exe.yakbd-new`) {
		t.Fatalf("%+v %v %v", r, err, f.atReboot)
	}
	if v := f.keys[strings.ToLower(setup.UninstallRoot+`\`+code)]["displayversion"].Str; v != "0.4.0.0" {
		t.Errorf("DisplayVersion %s", v)
	}
	// Uninstall while it runs: deleted at the restart, with the folder after it; the rest goes now.
	r, err = newSetup(t, f, "0.4.0.0").Uninstall()
	if err != nil || !r.Reboot {
		t.Fatalf("%+v %v", r, err)
	}
	if got := f.atReboot[len(f.atReboot)-2:]; got[0] != "delete "+dir+`\yakbd.exe` || got[1] != "delete "+dir {
		t.Errorf("%v", f.atReboot)
	}
	if strings.Contains(f.keys[strings.ToLower(Environment)]["path"].Str, "YAKBD") || f.keys[strings.ToLower(setup.UninstallRoot+`\`+code)] != nil {
		t.Error("PATH or entry left")
	}
}

func TestPathAlreadyThere(t *testing.T) {
	f := newFake()
	f.keys[strings.ToLower(Environment)]["path"] = setup.Value{Name: "Path", Type: setup.RegSZ, Str: `C:\x;c:\program files\yakbd\yakbd\`}
	s := newSetup(t, f, "0.2.0.0")
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	if f.keys[strings.ToLower(setup.UninstallRoot+`\`+code)]["yakbdpathadded"].Str != "0" || f.broadcast != 0 {
		t.Error("PATH entry that was there counted as added")
	}
	if _, err := s.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if v := f.keys[strings.ToLower(Environment)]["path"]; v.Str != `C:\x;c:\program files\yakbd\yakbd\` || v.Type != setup.RegSZ {
		t.Errorf("someone else's PATH entry changed: %+v", v)
	}
}

func TestRefusals(t *testing.T) {
	f := newFake()
	f.machine = payload.MachineARM64
	before := f.snapshot()
	_, err := newSetup(t, f, "0.2.0.0", BuildX64, BuildX86).Install()
	wantCode(t, err, cmdline.PlatformUnsupported)
	f.elevated = false
	_, err = newSetup(t, f, "0.2.0.0").Install()
	wantCode(t, err, cmdline.ElevationRequired)
	f.elevated = true
	f.keys[strings.ToLower(setup.SessionManager)] = map[string]setup.Value{"pendingfilerenameoperations": {Name: "PendingFileRenameOperations", Type: setup.RegMultiSZ,
		Str: `\??\C:\Program Files\YAKBD\yakbd\yakbd.exe` + "\x00\x00"}}
	before = f.snapshot()
	_, err = newSetup(t, f, "0.2.0.0").Install()
	wantCode(t, err, cmdline.InstallFailure)
	if f.snapshot() != before {
		t.Error("a refusal changed something")
	}
}

func TestPayloadDamage(t *testing.T) {
	data := map[string][]byte{BuildX64: pe(payload.MachineAMD64, "x")}
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}}, []string{BuildX64}, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	enc[len(enc)-1] ^= 1
	if _, err := Decode(enc); err == nil {
		t.Error("damage not found")
	}
	if _, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "1", Code: code}}, []string{BuildARM64}, map[string][]byte{BuildARM64: pe(payload.MachineAMD64, "x")}, nil); err == nil {
		t.Error("an x64 file accepted as arm64")
	}
}

func TestPathRoundTrip(t *testing.T) {
	const d = `C:\Program Files\YAKBD\yakbd`
	for _, pv := range []string{"", `a;b`, `a;b;`, `a;;b`, `%SystemRoot%\system32;%SystemRoot%;`} {
		if got := removePath(addPath(pv, d), d); got != pv {
			t.Errorf("%q -> %q -> %q", pv, addPath(pv, d), got)
		}
	}
	if got := removePath(`x;`+d+`;y`, d); got != "x;y" {
		t.Errorf("moved entry: %q", got)
	}
}
