package setup

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// fakeSys is an in-memory Windows for the tests: a registry, a file system with locked files, and input.dll.
type fakeSys struct {
	native   uint16
	elevated bool
	sid      string
	keys     map[string]*fkey // lower-case path -> key
	files    map[string][]byte
	inUse    map[string]bool
	atReboot []string
	self     string
	// failSet makes SetString fail for this value name.
	failSet string
	// ilot records InstallLayoutOrTip calls; layouts is what GetKeyboardLayoutList returns.
	ilot     []string
	layouts  []uint32
	tagCalls []string
	// onILOT, when set, runs after each InstallLayoutOrTip call (tests change layouts with it).
	onILOT func(s string, flags uint32)
	// drop makes this many InstallLayoutOrTip calls return TRUE without writing anything.
	drop int
	// The Default profile hive: load and unload calls, injected failures, the file loaded.
	hiveCalls []string
	// systemTags: the tag Windows files a LANGID under when LocalSystem enables it (VM-26: 0438 -> fo).
	systemTags           map[string]string
	loadFail, unloadFail bool
	loadedFrom           string
}

type fkey struct {
	name string
	vals []Value
}

func newFake() *fakeSys {
	f := &fakeSys{native: payload.MachineAMD64, elevated: true, sid: "S-1-5-21-1-1001",
		keys: map[string]*fkey{}, files: map[string][]byte{}, inUse: map[string]bool{},
		self: `C:\Users\u\Downloads\kbdtst-setup.exe`}
	f.files[strings.ToLower(f.self)] = []byte("MZ installer itself")
	f.mk(KeyboardLayouts + `\00000409`)
	f.set(KeyboardLayouts+`\00000409`, Value{Name: "Layout File", Type: RegSZ, Str: "KBDUS.DLL"})
	f.mk(UninstallRoot)
	f.mk(Users + `\.DEFAULT`)
	f.mk(Users + `\` + f.sid + `\Keyboard Layout\Preload`)
	f.set(Users+`\`+f.sid+`\Keyboard Layout\Preload`, Value{Name: "1", Type: RegSZ, Str: "00000409"})
	f.mk(Users + `\` + f.sid + `_Classes`)
	f.mk(defaultHive())
	f.mk(`HKLM\SOFTWARE`)
	return f
}

func (f *fakeSys) mk(p string) bool {
	created := false
	parts := strings.Split(p, `\`)
	for i := range parts {
		q := strings.ToLower(strings.Join(parts[:i+1], `\`))
		if f.keys[q] == nil {
			f.keys[q] = &fkey{name: parts[i]}
			created = true
		}
	}
	return created
}

func (f *fakeSys) set(p string, v Value) {
	k := f.keys[strings.ToLower(p)]
	for i := range k.vals {
		if strings.EqualFold(k.vals[i].Name, v.Name) {
			k.vals[i] = v
			return
		}
	}
	k.vals = append(k.vals, v)
}

func (f *fakeSys) NativeMachine() (uint16, error) { return f.native, nil }
func (f *fakeSys) Elevated() bool                 { return f.elevated }
func (f *fakeSys) UserSID() (string, error)       { return f.sid, nil }
func (f *fakeSys) Dir(k DirKind) (string, error) {
	if k == DirWOW64 {
		if f.native == payload.MachineI386 {
			return "", errors.New("no SysWOW64 on 32-bit Windows")
		}
		return `C:\Windows\SysWOW64`, nil
	}
	return `C:\Windows\Sysnative`, nil
}
func (f *fakeSys) ProgramFiles() (string, error) { return `C:\Program Files (x86)`, nil }
func (f *fakeSys) TempDir() string               { return `C:\Users\u\AppData\Local\Temp` }
func (f *fakeSys) SelfPath() string              { return f.self }
func (f *fakeSys) ReadFile(p string) ([]byte, error) {
	b, ok := f.files[strings.ToLower(p)]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), b...), nil
}
func (f *fakeSys) WriteFile(p string, d []byte) error {
	if f.inUse[strings.ToLower(p)] {
		return ErrInUse
	}
	f.files[strings.ToLower(p)] = append([]byte(nil), d...)
	return nil
}
func (f *fakeSys) Rename(from, to string) error {
	if f.inUse[strings.ToLower(to)] {
		return ErrInUse
	}
	b, ok := f.files[strings.ToLower(from)]
	if !ok {
		return ErrNotFound
	}
	delete(f.files, strings.ToLower(from))
	f.files[strings.ToLower(to)] = b
	return nil
}
func (f *fakeSys) Remove(p string) error {
	if f.inUse[strings.ToLower(p)] {
		return ErrInUse
	}
	if _, ok := f.files[strings.ToLower(p)]; !ok {
		return ErrNotFound
	}
	delete(f.files, strings.ToLower(p))
	return nil
}
func (f *fakeSys) MkdirAll(string) error { return nil }
func (f *fakeSys) RemoveDir(p string) error {
	for k := range f.files {
		if strings.HasPrefix(k, strings.ToLower(p)+`\`) {
			return errors.New("not empty")
		}
	}
	return nil
}
func (f *fakeSys) DeleteAtReboot(p string) error {
	f.atReboot = append(f.atReboot, "delete "+strings.ToLower(p))
	return nil
}
func (f *fakeSys) MoveAtReboot(from, to string) error {
	f.atReboot = append(f.atReboot, "move "+strings.ToLower(from)+" "+strings.ToLower(to))
	return nil
}
func (f *fakeSys) CreateKey(p string) (bool, error) { return f.mk(p), nil }
func (f *fakeSys) DeleteKey(p string) error {
	lp := strings.ToLower(p)
	for k := range f.keys {
		if strings.HasPrefix(k, lp+`\`) {
			return errors.New("has subkeys")
		}
	}
	delete(f.keys, lp)
	return nil
}
func (f *fakeSys) SubKeys(p string) ([]string, error) {
	lp := strings.ToLower(p)
	if f.keys[lp] == nil {
		return nil, ErrNotFound
	}
	var out []string
	for k, v := range f.keys {
		if strings.HasPrefix(k, lp+`\`) && !strings.Contains(k[len(lp)+1:], `\`) {
			out = append(out, v.name)
		}
	}
	sort.Strings(out)
	return out, nil
}
func (f *fakeSys) Values(p string) ([]Value, error) {
	k := f.keys[strings.ToLower(p)]
	if k == nil {
		return nil, ErrNotFound
	}
	return append([]Value(nil), k.vals...), nil
}
func (f *fakeSys) SetString(p, name, v string, expand bool) error {
	if name == f.failSet {
		return errors.New("injected failure")
	}
	if f.keys[strings.ToLower(p)] == nil {
		return ErrNotFound
	}
	t := RegSZ
	if expand {
		t = RegExpandSZ
	}
	f.set(p, Value{Name: name, Type: t, Str: v})
	return nil
}
func (f *fakeSys) SetDWORD(p, name string, v uint32) error {
	f.set(p, Value{Name: name, Type: RegDWORD, DWord: v})
	return nil
}
func (f *fakeSys) DeleteValue(p, name string) error {
	k := f.keys[strings.ToLower(p)]
	if k == nil {
		return ErrNotFound
	}
	for i := range k.vals {
		if strings.EqualFold(k.vals[i].Name, name) {
			k.vals = append(k.vals[:i], k.vals[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// InstallLayoutOrTip writes the three places of inst §4.3 into the caller's hive, as Windows does.
func (f *fakeSys) InstallLayoutOrTip(s string, flags uint32) error {
	f.ilot = append(f.ilot, fmt.Sprintf("%s/%d", s, flags))
	if f.drop > 0 {
		f.drop--
		return nil
	}
	hive := f.sid
	if hive == LocalSystemSID {
		hive = ".DEFAULT" // LocalSystem's hive (inst §4.3 T3)
		if lang, _, _ := strings.Cut(s, ":"); f.systemTags[strings.ToUpper(lang)] != "" && flags&ILOTUninstall == 0 {
			// VM-26: as LocalSystem the call files a missing language in .DEFAULT under Windows' own tag.
			t := f.systemTags[strings.ToUpper(lang)]
			up := Users + `\.DEFAULT\Control Panel\International\User Profile`
			f.mk(up + `\` + t)
			l := f.keys[strings.ToLower(up)]
			cur := ""
			for _, v := range l.vals {
				if v.Name == "Languages" {
					cur = v.Str
				}
			}
			if !strings.Contains("\x00"+cur+"\x00", "\x00"+t+"\x00") {
				f.set(up, Value{Name: "Languages", Type: RegMultiSZ, Str: strings.TrimPrefix(cur+"\x00"+t, "\x00")})
			}
		}
	}
	err := f.userReg(hive, s, flags, true)
	if f.onILOT != nil {
		f.onILOT(s, flags)
	}
	return err
}

// InstallLayoutOrTipUserReg writes only Preload and Substitutes (inst §4.3, measured).
func (f *fakeSys) InstallLayoutOrTipUserReg(hive, s string, flags uint32) error {
	f.ilot = append(f.ilot, fmt.Sprintf("%s:%s/%d", hive, s, flags))
	return f.userReg(hive, s, flags, false)
}

func (f *fakeSys) userReg(hive, s string, flags uint32, profile bool) error {
	lang, klid, _ := strings.Cut(s, ":")
	base := Users + `\` + hive
	pre, sub := base+`\Keyboard Layout\Preload`, base+`\Keyboard Layout\Substitutes`
	up := base + `\Control Panel\International\User Profile\en-US`
	if tags, err := f.SubKeys(base + `\Control Panel\International\User Profile`); err == nil {
		for _, t := range tags {
			k := base + `\Control Panel\International\User Profile\` + t
			for _, v := range f.keys[strings.ToLower(k)].vals {
				if v.Name == "TransientLangId" && fmt.Sprintf("%04X", v.DWord) == strings.ToUpper(lang) {
					up = k
				}
			}
		}
	}
	if t := f.systemTags[strings.ToUpper(lang)]; t != "" && f.keys[strings.ToLower(base+`\Control Panel\International\User Profile\`+t)] != nil {
		up = base + `\Control Panel\International\User Profile\` + t
	}
	// The KLID's placeholder (Windows: d<NNN><LLLL>, inst §4.2): the one Substitutes maps to it, else the next free.
	ph := ""
	subs, _ := f.Values(sub)
	for _, v := range subs {
		if strings.EqualFold(v.Str, klid) {
			ph = strings.ToLower(v.Name)
		}
	}
	// The first layout of a language takes the plain 0000<LLLL> name (inst §4.2; VM-19: Preload 00000438).
	first := true
	pres, _ := f.Values(pre)
	for _, v := range pres {
		first = first && !strings.HasSuffix(strings.ToLower(v.Str), strings.ToLower(lang))
	}
	if _, taken := valueMap(subs)["0000"+strings.ToLower(lang)]; ph == "" && first && !taken {
		ph = "0000" + strings.ToLower(lang)
	}
	for i := 1; ph == ""; i++ {
		c := fmt.Sprintf("d%03d%s", i, strings.ToLower(lang))
		if _, taken := valueMap(subs)[c]; !taken {
			ph = c
		}
	}
	if flags&ILOTUninstall != 0 {
		_ = f.DeleteValue(sub, ph)
		vs, _ := f.Values(pre)
		for _, v := range vs {
			if v.Str == ph {
				_ = f.DeleteValue(pre, v.Name)
			}
		}
		if profile {
			_ = f.DeleteValue(up, strings.ToUpper(s))
		}
		return nil
	}
	f.mk(pre)
	f.mk(sub)
	if profile {
		f.mk(up)
	}
	vs, _ := f.Values(pre)
	f.set(pre, Value{Name: fmt.Sprint(len(vs) + 1), Type: RegSZ, Str: ph})
	f.set(sub, Value{Name: ph, Type: RegSZ, Str: strings.ToLower(klid)})
	if profile {
		f.set(up, Value{Name: strings.ToUpper(s), Type: RegDWORD, DWord: 1})
	}
	return nil
}
func (f *fakeSys) LayoutList() ([]uint32, error) { return f.layouts, nil }

// snapshot is the whole state as text, for "nothing changed" comparisons.
func (f *fakeSys) snapshot() string {
	var lines []string
	for k, v := range f.keys {
		lines = append(lines, "key "+k)
		for _, x := range v.vals {
			lines = append(lines, fmt.Sprintf("val %s %s=%d:%s:%d", k, strings.ToLower(x.Name), x.Type, x.Str, x.DWord))
		}
	}
	for k, v := range f.files {
		lines = append(lines, fmt.Sprintf("file %s %x", k, v))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func fakePE(machine uint16, fill byte) []byte {
	b := make([]byte, 0x200)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[0x3C:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x44:], machine)
	for i := 0x100; i < len(b); i++ {
		b[i] = fill
	}
	return b
}

func testPayload(t *testing.T, builds []string, mod func(*payload.Manifest)) *payload.Payload {
	t.Helper()
	m := payload.Manifest{Tool: "test", Product: payload.Product{Name: "Test layout", Version: "1.0.0.0", Publisher: "Tester",
		Code: "{01234567-89AB-CDEF-0123-456789ABCDEF}"},
		Layout: payload.Layout{File: "kbdtst.dll", LangID: "0409", LayoutText: "Test layout"}}
	if mod != nil {
		mod(&m)
	}
	data := map[string][]byte{}
	for i, b := range builds {
		data[b] = fakePE(payload.MachineOf[b], byte(i+1))
	}
	enc, err := payload.Encode(m, builds, data)
	if err != nil {
		t.Fatal(err)
	}
	p, err := payload.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var allBuilds = []string{payload.BuildX64, payload.BuildWOW64, payload.BuildX86}

func (f *fakeSys) profile() string {
	return Users + `\` + f.sid + `\Control Panel\International\User Profile`
}

// AddLanguageTag adds the tag with the next transient LANGID and Windows' own default input method for it.
func (f *fakeSys) AddLanguageTag(tag string) error {
	up := f.profile()
	f.mk(up)
	var langs []string
	for _, v := range f.keys[strings.ToLower(up)].vals {
		if v.Name == "Languages" {
			langs = strings.Split(v.Str, "\x00")
		}
	}
	f.set(up, Value{Name: "Languages", Type: RegMultiSZ, Str: strings.Join(append(langs, tag), "\x00")})
	f.mk(up + `\` + tag)
	f.set(up+`\`+tag, Value{Name: "TransientLangId", Type: RegDWORD, DWord: 0x2000})
	f.set(up+`\`+tag, Value{Name: "2000:00000409", Type: RegDWORD, DWord: 1})
	f.tagCalls = append(f.tagCalls, "add "+tag)
	return nil
}

func (f *fakeSys) RemoveLanguageTag(tag string) error {
	up := f.profile()
	var keep []string
	for _, v := range f.keys[strings.ToLower(up)].vals {
		if v.Name == "Languages" {
			for _, l := range strings.Split(v.Str, "\x00") {
				if l != "" && !strings.EqualFold(l, tag) {
					keep = append(keep, l)
				}
			}
		}
	}
	_ = f.SetMultiString(up, "Languages", keep)
	delete(f.keys, strings.ToLower(up+`\`+tag))
	f.tagCalls = append(f.tagCalls, "remove "+tag)
	return nil
}

func (f *fakeSys) SetMultiString(p, name string, vals []string) error {
	if f.keys[strings.ToLower(p)] == nil {
		return ErrNotFound
	}
	f.set(p, Value{Name: name, Type: RegMultiSZ, Str: strings.Join(vals, "\x00")})
	return nil
}

// The Default profile: its hive file's keys live under "FILE\<file>" until LoadHive moves them to HKU\<name>.
const fakeDefaultFile = `C:\Users\Default\NTUSER.DAT`

func fileRoot(file string) string { return `FILE\` + file }

func (f *fakeSys) DefaultProfileHive() (string, error) { return fakeDefaultFile, nil }

func (f *fakeSys) moveTree(from, to string) {
	lf, lt := strings.ToLower(from), strings.ToLower(to)
	for k, v := range f.keys {
		if k == lf || strings.HasPrefix(k, lf+`\`) {
			delete(f.keys, k)
			nk := lt + k[len(lf):]
			if k == lf {
				v.name = to[strings.LastIndexByte(to, '\\')+1:]
			}
			f.keys[nk] = v
		}
	}
}

func (f *fakeSys) LoadHive(name, file string) error {
	f.hiveCalls = append(f.hiveCalls, "load "+name)
	if f.loadFail {
		return errors.New("injected load failure")
	}
	if f.keys[strings.ToLower(Users+`\`+name)] != nil {
		return errors.New("name in use")
	}
	f.mk(fileRoot(file))
	f.moveTree(fileRoot(file), Users+`\`+name)
	f.loadedFrom = file
	return nil
}

func (f *fakeSys) UnloadHive(name string) error {
	f.hiveCalls = append(f.hiveCalls, "unload "+name)
	if f.unloadFail {
		return errors.New("injected unload failure")
	}
	f.moveTree(Users+`\`+name, fileRoot(f.loadedFrom))
	return nil
}

// defaultHive is the Default profile's key path while it is not loaded.
func defaultHive() string { return fileRoot(fakeDefaultFile) }
