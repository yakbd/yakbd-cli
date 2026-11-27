package setup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

func newSetup(t *testing.T, f *fakeSys, builds []string, mod func(*payload.Manifest)) *Setup {
	return &Setup{Sys: f, P: testPayload(t, builds, mod), Log: t.Logf}
}

func val(t *testing.T, f *fakeSys, key, name string) Value {
	t.Helper()
	k := f.keys[strings.ToLower(key)]
	if k == nil {
		t.Fatalf("key %s missing", key)
	}
	for _, v := range k.vals {
		if strings.EqualFold(v.Name, name) {
			return v
		}
	}
	t.Fatalf("value %s\\%s missing", key, name)
	return Value{}
}

func wantCode(t *testing.T, err error, code int) {
	t.Helper()
	if ExitCode(err) != code {
		t.Fatalf("exit code %d (%v), want %d", ExitCode(err), err, code)
	}
}

func TestPlan(t *testing.T) {
	all := testPayload(t, allBuilds, nil).Manifest
	x64only := testPayload(t, []string{payload.BuildX64, payload.BuildWOW64}, nil).Manifest
	arm := testPayload(t, []string{payload.BuildARM64X, payload.BuildWOW64}, nil).Manifest
	cases := []struct {
		native uint16
		m      *payload.Manifest
		want   string
		code   int
	}{
		{payload.MachineI386, all, "[{x86 0}]", 0},
		{payload.MachineAMD64, all, "[{x64 0} {wow64 1}]", 0},
		{payload.MachineARM64, all, "", ExitMachine},
		{payload.MachineARM64, arm, "[{arm64x 0} {wow64 1}]", 0},
		{payload.MachineI386, x64only, "", ExitMachine},
		{payload.MachineAMD64, arm, "", ExitMachine},
		{0x1C4, all, "", ExitMachine},
	}
	for _, c := range cases {
		pl, err := Plan(c.native, c.m)
		if ExitCode(err) != c.code {
			t.Errorf("%#x %v: %v", c.native, c.m.Builds(), err)
			continue
		}
		if err == nil {
			if got := strings.ReplaceAll(strings.TrimSpace(fmtPlan(pl)), "\n", " "); got != c.want {
				t.Errorf("%#x %v: %s, want %s", c.native, c.m.Builds(), got, c.want)
			}
		}
	}
	_, err := Plan(payload.MachineARM64, all)
	if !strings.Contains(err.Error(), "Windows on ARM") {
		t.Errorf("ARM64 message: %v", err)
	}
}

func fmtPlan(pl []Placement) string {
	var b strings.Builder
	b.WriteString("[")
	for i, p := range pl {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString("{" + p.Build + " ")
		if p.Dir == DirWOW64 {
			b.WriteString("1}")
		} else {
			b.WriteString("0}")
		}
	}
	return b.String() + "]"
}

func TestParseLayoutID(t *testing.T) {
	for in, want := range map[string]uint16{"c7": 0xc7, "00C8": 0xc8, "0xc9": 0xc9, "000ca": 0xca, "": 0, "zz": 0, "1000": 0, "0fff": 0xfff} {
		if got := ParseLayoutID(Value{Type: RegSZ, Str: in}); got != want {
			t.Errorf("%q: %#x, want %#x", in, got, want)
		}
	}
	if ParseLayoutID(Value{Type: RegDWORD, DWord: 0xc5}) != 0 {
		t.Error("a DWORD counts as 0")
	}
	if HKL(0x0409, 0x00ff) != 0xF0FF0409 || HKL(0x0409, 0x1000) != 0xF0000409 {
		t.Error("HKL")
	}
}

func TestInstallFresh(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	res, err := s.Install()
	if err != nil {
		t.Fatal(err)
	}
	if res.KLID != "a0000409" || res.LayoutID != "00ff" {
		t.Fatalf("KLID %s Layout Id %s", res.KLID, res.LayoutID)
	}
	if string(f.files[`c:\windows\sysnative\kbdtst.dll`]) != string(s.P.Data["x64"]) ||
		string(f.files[`c:\windows\syswow64\kbdtst.dll`]) != string(s.P.Data["wow64"]) {
		t.Fatal("files not placed")
	}
	k := klidKey("a0000409")
	if v := val(t, f, k, "Layout Display Name"); v.Type != RegExpandSZ || v.Str != `@%SystemRoot%\system32\kbdtst.dll,-1000` {
		t.Errorf("display name %+v", v)
	}
	if val(t, f, k, "Layout File").Str != "kbdtst.dll" || val(t, f, k, "Layout Id").Str != "00ff" ||
		val(t, f, k, "Layout Product Code").Str != s.m().Product.Code || val(t, f, k, "Layout Text").Str != "Test layout" {
		t.Error("KLID values")
	}
	if f.keys[strings.ToLower(k)].vals[0].Name != "Layout File" {
		t.Error("Layout File is not written first")
	}
	arp := s.arpKey()
	if val(t, f, arp, "UninstallString").Str != `"C:\Program Files (x86)\YAKBD\{01234567-89AB-CDEF-0123-456789ABCDEF}\setup.exe" /x {01234567-89AB-CDEF-0123-456789ABCDEF}` ||
		val(t, f, arp, RecKLID).Str != "a0000409" || val(t, f, arp, "DisplayName").Str != "Test layout" || val(t, f, arp, "Publisher").Str != "Tester" {
		t.Error("uninstall entry")
	}
	if f.files[strings.ToLower(`C:\Program Files (x86)\YAKBD\{01234567-89AB-CDEF-0123-456789ABCDEF}\setup.exe`)] == nil {
		t.Error("installer copy missing")
	}
	if len(f.ilot) != 0 {
		t.Error("Install must not enable")
	}
	for k := range f.files {
		if strings.Contains(k, ".yakbd-") {
			t.Errorf("temporary file left: %s", k)
		}
	}
}

func TestInstallX86Machine(t *testing.T) {
	f := newFake()
	f.native = payload.MachineI386
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	if string(f.files[`c:\windows\sysnative\kbdtst.dll`]) != string(s.P.Data["x86"]) || f.files[`c:\windows\syswow64\kbdtst.dll`] != nil {
		t.Error("x86 placement")
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
}

func TestAllocation(t *testing.T) {
	f := newFake()
	add := func(klid string, v Value) {
		f.mk(klidKey(klid))
		if v.Name != "" {
			f.set(klidKey(klid), v)
		}
	}
	add("a0000409", Value{Name: "Layout Id", Type: RegSZ, Str: "00ff"})
	add("a0010409", Value{Name: "Layout Id", Type: RegSZ, Str: "0xFE"})
	add("00060409", Value{Name: "Layout Id", Type: RegSZ, Str: "fd"})
	add("b0010407", Value{Name: "Layout Id", Type: RegDWORD, DWord: 0xfc}) // counts as 0
	// a0020409 is still named by a user's Substitutes: skipped.
	f.mk(Users + `\S-1-5-21-9\Keyboard Layout\Substitutes`)
	f.set(Users+`\S-1-5-21-9\Keyboard Layout\Substitutes`, Value{Name: "d0010409", Type: RegSZ, Str: "A0020409"})
	s := newSetup(t, f, allBuilds, nil)
	res, err := s.Install()
	if err != nil {
		t.Fatal(err)
	}
	if res.KLID != "a0030409" || res.LayoutID != "00fc" {
		t.Fatalf("KLID %s Layout Id %s, want a0030409 00fc", res.KLID, res.LayoutID)
	}
}

func TestNoFreeLayoutID(t *testing.T) {
	f := newFake()
	for id := 0xc0; id <= 0xff; id++ {
		k := klidKey("b0" + hexByte(id) + "0407")
		f.mk(k)
		f.set(k, Value{Name: "Layout Id", Type: RegSZ, Str: "00" + hexByte(id)})
	}
	before := f.snapshot()
	s := newSetup(t, f, allBuilds, nil)
	_, err := s.Install()
	wantCode(t, err, ExitFailed)
	if f.snapshot() != before {
		t.Error("not rolled back")
	}
}

func hexByte(i int) string { return fmt.Sprintf("%02x", i) }

func TestForeignFileRefused(t *testing.T) {
	f := newFake()
	f.files[`c:\windows\syswow64\kbdtst.dll`] = []byte("someone else's")
	before := f.snapshot()
	s := newSetup(t, f, allBuilds, nil)
	_, err := s.Install()
	wantCode(t, err, ExitFailed)
	if !strings.Contains(err.Error(), "not overwritten") || f.snapshot() != before {
		t.Errorf("%v; changed: %v", err, f.snapshot() != before)
	}
}

func TestIdenticalFileNotOursRefused(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	f.files[`c:\windows\sysnative\kbdtst.dll`] = s.P.Data["x64"]
	before := f.snapshot()
	_, err := s.Install()
	wantCode(t, err, ExitFailed)
	if f.snapshot() != before {
		t.Error("changed")
	}
}

func TestRollbackOnValueFailure(t *testing.T) {
	for _, name := range []string{"Layout Id", "UninstallString", "Layout Product Code"} {
		f := newFake()
		f.failSet = name
		before := f.snapshot()
		s := newSetup(t, f, allBuilds, nil)
		if _, err := s.Install(); err == nil {
			t.Fatalf("%s: no error", name)
		}
		if f.snapshot() != before {
			t.Errorf("%s: not rolled back:\n%s\n---\n%s", name, before, f.snapshot())
		}
	}
}

func TestPinned(t *testing.T) {
	f := newFake()
	f.mk(klidKey("a0050409"))
	s := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Layout.KLID = "A0050409" })
	before := f.snapshot()
	_, err := s.Install()
	wantCode(t, err, ExitFailed)
	if f.snapshot() != before {
		t.Error("pinned KLID taken: not rolled back")
	}

	f = newFake()
	f.mk(klidKey("a0050409"))
	f.set(klidKey("a0050409"), Value{Name: "Layout Id", Type: RegSZ, Str: "00d0"})
	s = newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Layout.LayoutID = "00D0" })
	_, err = s.Install()
	wantCode(t, err, ExitFailed)

	f = newFake()
	s = newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Layout.KLID = "A0420409"; m.Layout.LayoutID = "0123" })
	res, err := s.Install()
	if err != nil || res.KLID != "a0420409" || res.LayoutID != "0123" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestCustomLanguage(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, func(m *payload.Manifest) {
		m.Layout.LangID = "0C00"
		m.Layout.LocaleName = "qaa-Latn"
		m.Layout.CustomLanguageName = "Testish"
	})
	res, err := s.Install()
	if err != nil {
		t.Fatal(err)
	}
	k := klidKey(res.KLID)
	if res.KLID != "a0000c00" || val(t, f, k, "Layout Locale Name").Str != "qaa-Latn" || val(t, f, k, "Custom Language Name").Str != "Testish" ||
		val(t, f, k, "Custom Language Display Name").Str != `@%SystemRoot%\system32\kbdtst.dll,-1100` {
		t.Error("custom-language values")
	}
	// The route of kbl targets/windows-install.md section 6: tag added, transient LANGID, recorded.
	f.mk(f.profile())
	f.set(f.profile(), Value{Name: "Languages", Type: RegMultiSZ, Str: "en-US"})
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if len(f.ilot) != 1 || f.ilot[0] != "2000:A0000C00/0" || len(f.tagCalls) != 1 || f.tagCalls[0] != "add qaa-Latn" {
		t.Fatalf("%v %v", f.ilot, f.tagCalls)
	}
	if val(t, f, s.tagRecordKey(f.sid), RecTag).Str != "qaa-Latn" {
		t.Error("tag not recorded")
	}
	// Enabling again does not add the tag twice.
	if _, err := s.Enable(); err != nil || len(f.tagCalls) != 1 {
		t.Errorf("%v %v", f.tagCalls, err)
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	if f.tagCalls[len(f.tagCalls)-1] != "remove qaa-Latn" || f.keys[strings.ToLower(s.tagRecordKey(f.sid))] != nil {
		t.Errorf("tag not removed: %v", f.tagCalls)
	}
	for k := range f.keys {
		if strings.Contains(k, "qaa-latn") || strings.Contains(k, `\software\yakbd`) {
			t.Errorf("left: %s", k)
		}
	}
}

func TestCustomLanguageTagKept(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Layout.LangID = "0C00"; m.Layout.LocaleName = "qaa-Latn" })
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	_ = f.AddLanguageTag("qaa-Latn") // the user had the language already
	f.tagCalls = nil
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	if len(f.tagCalls) != 0 {
		t.Errorf("a tag the installer did not add was touched: %v", f.tagCalls)
	}
	_, err := (&Setup{Sys: f, P: testPayload(t, allBuilds, func(m *payload.Manifest) { m.Layout.LangID = "0C00"; m.Layout.LocaleName = "bad tag!" })}).customLangID(f.sid)
	wantCode(t, err, ExitEnable)
}

func TestRefusals(t *testing.T) {
	f := newFake()
	f.elevated = false
	before := f.snapshot()
	s := newSetup(t, f, allBuilds, nil)
	_, err := s.Install()
	wantCode(t, err, ExitNeedAdmin)
	_, err = s.Uninstall(false)
	wantCode(t, err, ExitNeedAdmin)
	f.elevated = true
	f.native = payload.MachineARM64
	_, err = s.Install()
	wantCode(t, err, ExitMachine)
	if f.snapshot() != before {
		t.Error("a refusal changed something")
	}
	f.native = payload.MachineI386
	s = newSetup(t, f, []string{payload.BuildX64, payload.BuildWOW64}, nil)
	_, err = s.Install()
	wantCode(t, err, ExitMachine)
	if f.snapshot() != before {
		t.Error("a refusal changed something")
	}
}

func TestUpgradeAndRepair(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	first, err := s.Install()
	if err != nil {
		t.Fatal(err)
	}
	// A new version with other bytes, one file in use.
	s2 := newSetup(t, f, []string{payload.BuildX86, payload.BuildWOW64, payload.BuildX64}, func(m *payload.Manifest) { m.Product.Version = "2.0.0.0" })
	f.inUse[`c:\windows\sysnative\kbdtst.dll`] = true
	res, err := s2.Install()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Upgraded || res.KLID != first.KLID || res.LayoutID != first.LayoutID || !res.Reboot {
		t.Fatalf("upgrade %+v", res)
	}
	if string(f.files[`c:\windows\syswow64\kbdtst.dll`]) != string(s2.P.Data["wow64"]) {
		t.Error("WOW64 file not replaced")
	}
	if len(f.atReboot) != 1 || !strings.HasPrefix(f.atReboot[0], `move c:\windows\sysnative\kbdtst.dll.yakbd-new c:\windows\sysnative\kbdtst.dll`) {
		t.Errorf("at reboot: %v", f.atReboot)
	}
	if val(t, f, s.arpKey(), "DisplayVersion").Str != "2.0.0.0" {
		t.Error("version not updated")
	}
	delete(f.inUse, `c:\windows\sysnative\kbdtst.dll`)

	// A feature update dropped the KLID (M7): the record brings it back with the same id.
	if err := f.DeleteKey(klidKey(first.KLID)); err != nil {
		t.Fatal(err)
	}
	res, err = s2.Install()
	if err != nil || res.KLID != first.KLID || res.LayoutID != first.LayoutID {
		t.Fatalf("repair %+v %v", res, err)
	}
	if val(t, f, klidKey(first.KLID), "Layout Product Code").Str != s.m().Product.Code {
		t.Error("repair values")
	}

	// Another layout took the Layout Id meanwhile: a new one, with a note.
	_ = f.DeleteKey(klidKey(first.KLID))
	f.mk(klidKey("00990409"))
	f.set(klidKey("00990409"), Value{Name: "Layout Id", Type: RegSZ, Str: first.LayoutID})
	res, err = s2.Install()
	if err != nil || res.KLID != first.KLID || res.LayoutID != "00fe" || len(res.Notes) == 0 {
		t.Fatalf("repair with a taken id %+v %v", res, err)
	}
}

func TestEnable(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Enable(); err == nil {
		t.Error("enable before install")
	}
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	f.layouts = []uint32{0x04090409, 0xF0FF0409}
	res, err := s.Enable()
	if err != nil || len(f.ilot) != 1 || f.ilot[0] != "0409:A0000409/0" || len(res.Notes) != 0 {
		t.Fatalf("%v %v %v", f.ilot, err, res.Notes)
	}
	f.layouts = nil
	res, _ = s.Enable()
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "sign out") {
		t.Errorf("notes %v", res.Notes)
	}
}

func TestUninstallLeavesNothing(t *testing.T) {
	f := newFake()
	before := f.snapshot()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	// Another signed-in user enabled it too, through Settings (all three places).
	other := Users + `\S-1-5-21-1-1002`
	f.mk(other + `\Keyboard Layout\Preload`)
	f.set(other+`\Keyboard Layout\Preload`, Value{Name: "1", Type: RegSZ, Str: "00000407"})
	f.set(other+`\Keyboard Layout\Preload`, Value{Name: "2", Type: RegSZ, Str: "d0010409"})
	f.set(other+`\Keyboard Layout\Preload`, Value{Name: "3", Type: RegSZ, Str: "00000409"})
	f.mk(other + `\Keyboard Layout\Substitutes`)
	f.set(other+`\Keyboard Layout\Substitutes`, Value{Name: "d0010409", Type: RegSZ, Str: "a0000409"})
	f.mk(other + `\Control Panel\International\User Profile\en-US`)
	f.set(other+`\Control Panel\International\User Profile\en-US`, Value{Name: "0409:A0000409", Type: RegDWORD, DWord: 1})
	f.set(other+`\Control Panel\International\User Profile\en-US`, Value{Name: "0409:00000409", Type: RegDWORD, DWord: 2})
	// The uninstaller runs from Program Files, as "Installed apps" starts it.
	f.self = `C:\Program Files (x86)\YAKBD\{01234567-89AB-CDEF-0123-456789ABCDEF}\setup.exe`
	res, err := s.Uninstall(false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reboot {
		t.Error("reboot not expected")
	}
	pre, _ := f.Values(other + `\Keyboard Layout\Preload`)
	if len(pre) != 2 || pre[0].Name != "1" || pre[0].Str != "00000407" || pre[1].Name != "2" || pre[1].Str != "00000409" {
		t.Errorf("other user's Preload: %+v", pre)
	}
	if sub, _ := f.Values(other + `\Keyboard Layout\Substitutes`); len(sub) != 0 {
		t.Errorf("Substitutes left: %+v", sub)
	}
	if up, _ := f.Values(other + `\Control Panel\International\User Profile\en-US`); len(up) != 1 {
		t.Errorf("User Profile left: %+v", up)
	}
	// Remove the other user's (expected) leftovers of the test setup and compare with the start.
	for _, k := range []string{`\Keyboard Layout\Preload`, `\Keyboard Layout\Substitutes`, `\Control Panel\International\User Profile\en-US`,
		`\Control Panel\International\User Profile`, `\Control Panel\International`, `\Control Panel`, `\Keyboard Layout`, ``} {
		delete(f.keys, strings.ToLower(other+k))
	}
	for _, k := range []string{`\Keyboard Layout\Substitutes`, `\Control Panel\International\User Profile\en-US`, `\Control Panel\International\User Profile`, `\Control Panel\International`, `\Control Panel`} {
		delete(f.keys, strings.ToLower(Users+`\`+f.sid+k))
	}
	moved := `c:\users\u\appdata\local\temp\yakbd-uninstall-01234567-89ab-cdef-0123-456789abcdef.exe`
	if f.files[moved] == nil || len(f.atReboot) != 1 || f.atReboot[0] != "delete "+moved {
		t.Errorf("self removal: %v", f.atReboot)
	}
	delete(f.files, moved)
	if after := f.snapshot(); after != before {
		t.Errorf("uninstall left something:\n%s\n---\n%s", before, after)
	}
}

func TestUninstallRefusesOnlyLayout(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	h := Users + `\S-1-5-21-1-1003\Keyboard Layout`
	f.mk(h + `\Preload`)
	f.set(h+`\Preload`, Value{Name: "1", Type: RegSZ, Str: "00000409"})
	f.mk(h + `\Substitutes`)
	f.set(h+`\Substitutes`, Value{Name: "00000409", Type: RegSZ, Str: "a0000409"})
	before := f.snapshot()
	_, err := s.Uninstall(false)
	wantCode(t, err, ExitRefused)
	if !strings.Contains(err.Error(), "S-1-5-21-1-1003") || f.snapshot() != before {
		t.Errorf("%v", err)
	}
	if _, err := s.Uninstall(true); err != nil {
		t.Fatal(err)
	}
	if pre, _ := f.Values(h + `\Preload`); len(pre) != 0 {
		t.Errorf("forced: Preload %+v", pre)
	}
}

func TestUninstallFileInUse(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	f.inUse[`c:\windows\sysnative\kbdtst.dll`] = true
	res, err := s.Uninstall(false)
	if err != nil || !res.Reboot || len(res.Notes) == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if f.atReboot[0] != `delete c:\windows\sysnative\kbdtst.dll` {
		t.Errorf("%v", f.atReboot)
	}
	if f.files[`c:\windows\syswow64\kbdtst.dll`] != nil || f.keys[strings.ToLower(klidKey("a0000409"))] != nil || f.keys[strings.ToLower(s.arpKey())] != nil {
		t.Error("the rest must be removed at once")
	}
	_, err = s.Uninstall(false)
	wantCode(t, err, ExitNotInstalled)
}

func TestUninstallAfterPendingUpgrade(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	f.inUse[`c:\windows\syswow64\kbdtst.dll`] = true
	s2 := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Product.Version = "2" })
	s2.P.Data["wow64"] = append(append([]byte(nil), s2.P.Data["wow64"]...), 9)
	if res, err := s2.Install(); err != nil || !res.Reboot {
		t.Fatalf("%+v %v", res, err)
	}
	if f.files[`c:\windows\syswow64\kbdtst.dll.yakbd-new`] == nil {
		t.Fatal("no pending replacement")
	}
	if _, err := s2.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	if f.files[`c:\windows\syswow64\kbdtst.dll.yakbd-new`] != nil {
		t.Error("the pending replacement would bring the file back at restart")
	}
}

func TestUninstallCleansDefaultHive(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	d := Users + `\.DEFAULT\Keyboard Layout\Substitutes`
	f.mk(d)
	f.set(d, Value{Name: "d0010409", Type: RegSZ, Str: "a0000409"})
	sys := Users + `\S-1-5-18\Keyboard Layout\Substitutes`
	f.mk(sys)
	f.set(sys, Value{Name: "d0010409", Type: RegSZ, Str: "a0000409"})
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	// D87 (kbl targets/windows-install.md §9 step 1): .DEFAULT is a §7.1 place and is cleaned always; a hive named
	// S-1-5-18 is not one of the places' names and stays as it is.
	if vs, _ := f.Values(sys); len(vs) != 1 {
		t.Errorf("S-1-5-18 was changed: %+v", vs)
	}
	if vs, _ := f.Values(d); len(vs) != 0 {
		t.Errorf(".DEFAULT still names the layout: %+v", vs)
	}
	called := false
	for _, c := range f.ilot {
		called = called || c == ".DEFAULT:0409:A0000409/1"
	}
	if !called {
		t.Errorf("no InstallLayoutOrTipUserReg for .DEFAULT: %v", f.ilot)
	}
}

func TestPendingDeletionRefused(t *testing.T) {
	f := newFake()
	f.mk(SessionManager)
	f.set(SessionManager, Value{Name: "PendingFileRenameOperations", Type: RegMultiSZ,
		Str: "\\??\\C:\\Temp\\x.tmp\x00\x00\\??\\C:\\Windows\\System32\\KBDTST.DLL\x00"})
	before := f.snapshot()
	s := newSetup(t, f, allBuilds, nil)
	_, err := s.Install()
	wantCode(t, err, ExitFailed)
	if !strings.Contains(err.Error(), "Restart Windows") || f.snapshot() != before {
		t.Errorf("%v", err)
	}
	// A pending rename (not a deletion) of the file does not block.
	f.set(SessionManager, Value{Name: "PendingFileRenameOperations", Type: RegMultiSZ,
		Str: "\\??\\C:\\Windows\\System32\\kbdtst.dll.yakbd-new\x00!\\??\\C:\\Windows\\System32\\kbdtst.dll"})
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
}

func TestThreeLayoutsOneLanguage(t *testing.T) {
	f := newFake()
	var setups []*Setup
	for i, file := range []string{"kbdone.dll", "kbdtwo.dll", "kbdthr.dll"} {
		code := fmt.Sprintf("{01234567-89AB-CDEF-0123-45678900000%d}", i)
		file := file
		s := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Layout.File = file; m.Product.Code = code })
		res, err := s.Install()
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("a00%d0409", i); res.KLID != want || res.LayoutID != fmt.Sprintf("00%02x", 0xff-i) {
			t.Errorf("%s: KLID %s Layout Id %s", file, res.KLID, res.LayoutID)
		}
		if _, err := s.Enable(); err != nil {
			t.Fatal(err)
		}
		setups = append(setups, s)
	}
	up := f.profile() + `\en-US`
	for i := range setups {
		found := false
		for _, v := range f.keys[strings.ToLower(up)].vals {
			found = found || v.Name == fmt.Sprintf("0409:A00%d0409", i)
		}
		if !found {
			t.Errorf("layout %d is not in the user's list", i)
		}
	}
	for _, s := range setups {
		if _, err := s.Uninstall(false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnableReadsTheListBack(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	f.drop = 1 // TRUE, but nothing written (the ARM64 bundle's sign-in run)
	if _, err := s.Enable(); err != nil || len(f.ilot) != 2 {
		t.Fatalf("%v %v", f.ilot, err)
	}
	f.ilot, f.drop = nil, 99
	f2 := newFake()
	s2 := newSetup(t, f2, allBuilds, nil)
	if _, err := s2.Install(); err != nil {
		t.Fatal(err)
	}
	f2.drop = 99
	_, err := s2.Enable()
	wantCode(t, err, ExitEnable)
	if len(f2.ilot) != 3 {
		t.Errorf("tries: %v", f2.ilot)
	}
}
