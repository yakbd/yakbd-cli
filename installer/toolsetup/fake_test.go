package toolsetup

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

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
	failWrite string // WriteFile fails for a path ending in this (lower case)
	linkLost  string // CreateShortcut reports success but writes nothing, for a file ending in this
	linkWrong string // CreateShortcut stores another target, for a file ending in this
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
	if f.failWrite != "" && strings.HasSuffix(strings.ToLower(p), f.failWrite) {
		return fmt.Errorf("disk full")
	}
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
	switch {
	case f.linkLost != "" && strings.HasSuffix(lnk, f.linkLost):
		return nil // reported as saved, but no file (what the read-back must catch)
	case f.linkWrong != "" && strings.HasSuffix(lnk, f.linkWrong):
		target = strings.Replace(target, `C:\Program Files\`, `C:\Program Files (x86)\`, 1)
	}
	return f.WriteFile(lnk, []byte("lnk "+target+" | "+dir))
}

// ReadShortcut reads back what CreateShortcut stored.
func (f *fake) ReadShortcut(lnk string) (string, string, error) {
	b, err := f.ReadFile(lnk)
	if err != nil {
		return "", "", err
	}
	t, d, ok := strings.Cut(strings.TrimPrefix(string(b), "lnk "), " | ")
	if !ok {
		return "", "", fmt.Errorf("not a shortcut")
	}
	return t, d, nil
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

// fakeUser is the starting user's side: environment, HKCU keys (lower-case path -> values count, subkeys by
// prefix), folders and files.
type fakeUser struct {
	env   map[string]string
	keys  map[string]int // lower-case key below HKCU -> number of values
	dirs  map[string]bool
	files map[string]bool
}

func newUser() *fakeUser {
	return &fakeUser{env: map[string]string{"LOCALAPPDATA": `C:\Users\t\AppData\Local`, "APPDATA": `C:\Users\t\AppData\Roaming`},
		keys: map[string]int{}, dirs: map[string]bool{}, files: map[string]bool{}}
}

func (u *fakeUser) Getenv(n string) string { return u.env[n] }
func (u *fakeUser) UserKeyExists(p string) bool {
	_, ok := u.keys[strings.ToLower(p)]
	return ok
}
func (u *fakeUser) UserKeyEmpty(p string) bool {
	l := strings.ToLower(p)
	for k := range u.keys {
		if strings.HasPrefix(k, l+`\`) {
			return false
		}
	}
	return u.keys[l] == 0
}
func (u *fakeUser) DeleteUserKeyTree(p string) error {
	l := strings.ToLower(p)
	for k := range u.keys {
		if k == l || strings.HasPrefix(k, l+`\`) {
			delete(u.keys, k)
		}
	}
	return nil
}
func (u *fakeUser) DirExists(p string) bool { return u.dirs[strings.ToLower(p)] }
func (u *fakeUser) RemoveTree(p string) error {
	l := strings.ToLower(p)
	for _, m := range []map[string]bool{u.dirs, u.files} {
		for k := range m {
			if k == l || strings.HasPrefix(k, l+`\`) {
				delete(m, k)
			}
		}
	}
	return nil
}
func (u *fakeUser) RemoveDir(p string) error {
	l := strings.ToLower(p)
	for _, m := range []map[string]bool{u.dirs, u.files} {
		for k := range m {
			if strings.HasPrefix(k, l+`\`) {
				return fmt.Errorf("not empty")
			}
		}
	}
	delete(u.dirs, l)
	return nil
}
