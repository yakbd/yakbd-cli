package setup

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// The sign-in screen and new users (kbl targets/windows-install.md §7.1, §9 step 1, D87).

const klidA = "a0000409"

// placeRoots are the key paths of the four §7.1 places in the fake while the Default profile is not loaded.
func placeRoots() []string {
	return []string{Users + `\.DEFAULT`, Users + `\S-1-5-19`, Users + `\S-1-5-20`, defaultHive()}
}

// withPlaces gives every §7.1 place a US layout in Preload and an en-US language with it, as a fresh Windows has.
func withPlaces(f *fakeSys) *fakeSys {
	for _, r := range placeRoots() {
		f.mk(r + `\Keyboard Layout\Preload`)
		f.set(r+`\Keyboard Layout\Preload`, Value{Name: "1", Type: RegSZ, Str: "00000409"})
		f.mk(r + `\Keyboard Layout\Substitutes`)
		f.mk(r + `\Control Panel\International\User Profile\en-US`)
		f.set(r+`\Control Panel\International\User Profile\en-US`, Value{Name: "0409:00000409", Type: RegDWORD, DWord: 1})
	}
	return f
}

// tree is a key and everything under it, as text.
func (f *fakeSys) tree(root string) string {
	lr := strings.ToLower(root)
	var lines []string
	for k, v := range f.keys {
		if k == lr || strings.HasPrefix(k, lr+`\`) {
			lines = append(lines, "key "+k[len(lr):])
			for _, x := range v.vals {
				lines = append(lines, fmt.Sprintf("val %s %s=%d:%s:%d", k[len(lr):], strings.ToLower(x.Name), x.Type, x.Str, x.DWord))
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func (f *fakeSys) places() map[string]string {
	m := map[string]string{}
	for _, r := range placeRoots() {
		m[r] = f.tree(r)
	}
	return m
}

func signInOn(m *payload.Manifest) { m.SignIn = true }

func recVal(f *fakeSys, name string) string {
	k := f.keys[strings.ToLower(UninstallRoot+`\{01234567-89AB-CDEF-0123-456789ABCDEF}`)]
	if k == nil {
		return "<no entry>"
	}
	for _, v := range k.vals {
		if strings.EqualFold(v.Name, name) {
			return v.Str
		}
	}
	return "<none>"
}

// checkT8 checks one place for T8's shape: Preload 2 = d0010409, Substitutes d0010409 = the KLID, the User Profile
// value = 2, and the System Backup value only in the system hives.
func checkT8(t *testing.T, f *fakeSys, root string, backup bool) {
	t.Helper()
	if v := val(t, f, root+`\Keyboard Layout\Preload`, "2"); v.Str != "d0010409" {
		t.Errorf("%s Preload 2 = %q", root, v.Str)
	}
	if v := val(t, f, root+`\Keyboard Layout\Substitutes`, "d0010409"); v.Str != klidA {
		t.Errorf("%s Substitutes = %q", root, v.Str)
	}
	if v := val(t, f, root+`\Control Panel\International\User Profile\en-US`, "0409:A0000409"); v.Type != RegDWORD || v.DWord != 2 {
		t.Errorf("%s User Profile value %+v", root, v)
	}
	bk := f.keys[strings.ToLower(root+`\Control Panel\International\User Profile System Backup\en-US`)]
	if backup != (bk != nil && len(bk.vals) == 1 && bk.vals[0].Name == "0409:A0000409" && bk.vals[0].DWord == 2) {
		t.Errorf("%s System Backup value: %+v, want present %v", root, bk, backup)
	}
}

func TestSignInInstallWritesT8Shape(t *testing.T) {
	f := withPlaces(newFake())
	s := newSetup(t, f, allBuilds, signInOn)
	res, err := s.installSI()
	if err != nil || res.SignInFailed != nil {
		t.Fatal(err, res.SignInFailed)
	}
	for i, r := range placeRoots() {
		checkT8(t, f, r, i < 3)
	}
	if got := recVal(f, RecSignIn); got != "1" {
		t.Errorf("record %s = %s", RecSignIn, got)
	}
	if f.keys[strings.ToLower(Users+`\`+s.DefaultHiveName())] != nil || strings.Join(f.hiveCalls, ",") != "load YAKBD-{01234567-89AB-CDEF-0123-456789ABCDEF},unload YAKBD-{01234567-89AB-CDEF-0123-456789ABCDEF}" {
		t.Errorf("Default profile not loaded and unloaded once: %v", f.hiveCalls)
	}
	// A second run (repair) adds nothing.
	before := f.snapshot()
	n := len(f.ilot)
	if res, err := newSetup(t, f, allBuilds, signInOn).installSI(); err != nil || !res.Upgraded || res.SignInFailed != nil {
		t.Fatal(err)
	}
	if f.snapshot() != before || len(f.ilot) != n {
		t.Errorf("the second run changed something: %v", f.ilot[n:])
	}
}

func TestSignInUninstallCleansEveryPlace(t *testing.T) {
	f := withPlaces(newFake())
	start := f.places()
	s := newSetup(t, f, allBuilds, signInOn)
	if _, err := s.installSI(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	for r, want := range start {
		if got := f.tree(r); got != want {
			t.Errorf("%s after uninstall:\n%s\nwant\n%s", r, got, want)
		}
	}
	if f.keys[strings.ToLower(Users+`\`+s.DefaultHiveName())] != nil {
		t.Error("the Default profile stays loaded")
	}
}

func TestPlainInstallLeavesPlacesAndUninstallCleansWindowsCopy(t *testing.T) {
	f := withPlaces(newFake())
	start := f.places()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.installSI(); err != nil {
		t.Fatal(err)
	}
	for r, want := range start {
		if f.tree(r) != want {
			t.Errorf("a plain install changed %s", r)
		}
	}
	if got := recVal(f, RecSignIn); got != "<none>" {
		t.Errorf("plain install recorded %s = %s", RecSignIn, got)
	}
	// Windows' own copy ("Copy settings", inst §4.7) after the plain install: the same entries, written here by hand.
	for i, r := range placeRoots() {
		f.set(r+`\Keyboard Layout\Preload`, Value{Name: "2", Type: RegSZ, Str: "d0010409"})
		f.set(r+`\Keyboard Layout\Substitutes`, Value{Name: "d0010409", Type: RegSZ, Str: klidA})
		f.set(r+`\Control Panel\International\User Profile\en-US`, Value{Name: "0409:A0000409", Type: RegDWORD, DWord: 2})
		if i < 3 {
			b := r + `\Control Panel\International\User Profile System Backup\en-US`
			f.mk(b)
			f.set(b, Value{Name: "0409:a0000409", Type: RegDWORD, DWord: 2})
		}
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	for i, r := range placeRoots() {
		want := start[r]
		if i < 3 {
			// The (now empty) System Backup key of the copy stays; languages are left as they are.
			want = f.tree(r)
			if strings.Contains(want, "a0000409") || strings.Contains(want, "d0010409") {
				t.Errorf("%s still names the layout:\n%s", r, want)
			}
			continue
		}
		if got := f.tree(r); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", r, got, want)
		}
	}
}

func TestSignInFailureRollsBackPlacesKeepsMachine(t *testing.T) {
	f := withPlaces(newFake())
	start := f.places()
	f.loadFail = true
	res, err := newSetup(t, f, allBuilds, signInOn).installSI()
	if err != nil {
		t.Fatal(err)
	}
	if ExitCode(res.SignInFailed) != ExitEnable {
		t.Fatalf("SignInFailed %v", res.SignInFailed)
	}
	for r, want := range start {
		if f.tree(r) != want {
			t.Errorf("%s not rolled back:\n%s", r, f.tree(r))
		}
	}
	if recVal(f, RecSignIn) != "0" || recVal(f, RecKLID) != klidA {
		t.Errorf("record %s, KLID %s", recVal(f, RecSignIn), recVal(f, RecKLID))
	}
	// An entry that was there before the run (Windows' own copy put the layout into .DEFAULT) stays when a later
	// place fails: only what this run wrote is taken back.
	f3 := withPlaces(newFake())
	d := Users + `\.DEFAULT`
	f3.set(d+`\Keyboard Layout\Preload`, Value{Name: "2", Type: RegSZ, Str: "d0010409"})
	f3.set(d+`\Keyboard Layout\Substitutes`, Value{Name: "d0010409", Type: RegSZ, Str: klidA})
	f3.set(d+`\Control Panel\International\User Profile\en-US`, Value{Name: "0409:A0000409", Type: RegDWORD, DWord: 2})
	start3 := f3.places()
	f3.loadFail = true
	if res, err := newSetup(t, f3, allBuilds, signInOn).installSI(); err != nil || res.SignInFailed == nil {
		t.Fatal(err)
	}
	for r, want := range start3 {
		if f3.tree(r) != want {
			t.Errorf("%s:\n%s\nwant\n%s", r, f3.tree(r), want)
		}
	}
}

// An unload that fails is reported, not a failure (§7.1); the next run finds the hive still loaded and uses it.
func TestSignInUnloadFailureIsReported(t *testing.T) {
	f := withPlaces(newFake())
	f.unloadFail = true
	s := newSetup(t, f, allBuilds, signInOn)
	res, err := s.installSI()
	if err != nil || res.SignInFailed != nil || recVal(f, RecSignIn) != "1" || !strings.Contains(strings.Join(res.Notes, " "), "could not be closed") {
		t.Fatalf("%v %v %s %v", err, res.SignInFailed, recVal(f, RecSignIn), res.Notes)
	}
	dh := Users + `\` + s.DefaultHiveName()
	if !strings.Contains(f.tree(dh), "a0000409") {
		t.Fatal("the loaded Default profile does not have the layout")
	}
	f.unloadFail = false
	n := len(f.hiveCalls)
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.hiveCalls[n:], ",") != "unload "+s.DefaultHiveName() || f.keys[strings.ToLower(dh)] != nil {
		t.Errorf("hive calls %v", f.hiveCalls[n:])
	}
	if strings.Contains(f.tree(defaultHive()), "a0000409") {
		t.Error("the Default profile still names the layout")
	}
}

func TestSignInRepairAndOverride(t *testing.T) {
	f := withPlaces(newFake())
	start := f.places()
	if _, err := newSetup(t, f, allBuilds, signInOn).installSI(); err != nil {
		t.Fatal(err)
	}
	// Removed by hand from .DEFAULT and the Default profile; a repair without SIGNIN (an installer whose manifest
	// says off) restores them: recorded on.
	for _, r := range []string{Users + `\.DEFAULT`, defaultHive()} {
		_ = f.DeleteValue(r+`\Control Panel\International\User Profile\en-US`, "0409:A0000409")
		_ = f.DeleteValue(r+`\Keyboard Layout\Preload`, "2")
		_ = f.DeleteValue(r+`\Keyboard Layout\Substitutes`, "d0010409")
	}
	if res, err := newSetup(t, f, allBuilds, nil).installSI(); err != nil || res.SignInFailed != nil {
		t.Fatal(err)
	}
	for i, r := range placeRoots() {
		checkT8(t, f, r, i < 3)
	}
	// SIGNIN=0 on a repair: the places are cleaned and the record is off.
	off := false
	s := newSetup(t, f, allBuilds, signInOn)
	s.SignIn = &off
	if res, err := s.installSI(); err != nil || res.SignInFailed != nil {
		t.Fatal(err)
	}
	if recVal(f, RecSignIn) != "0" {
		t.Errorf("record %s", recVal(f, RecSignIn))
	}
	for i, r := range placeRoots() {
		if strings.Contains(f.tree(r), "a0000409") {
			t.Errorf("%s still names the layout", r)
		}
		if i == 3 && f.tree(r) != start[r] {
			t.Errorf("Default profile:\n%s\nwant\n%s", f.tree(r), start[r])
		}
	}
	// Recorded off: a repair with a manifest that says on adds nothing; SIGNIN=1 does.
	before := f.places()
	if _, err := newSetup(t, f, allBuilds, signInOn).installSI(); err != nil {
		t.Fatal(err)
	}
	for r, want := range before {
		if f.tree(r) != want {
			t.Errorf("recorded off, but %s changed", r)
		}
	}
	on := true
	s = newSetup(t, f, allBuilds, nil)
	s.SignIn = &on
	if res, err := s.installSI(); err != nil || res.SignInFailed != nil || recVal(f, RecSignIn) != "1" {
		t.Fatal(err, recVal(f, RecSignIn))
	}
	checkT8(t, f, defaultHive(), false)
}

func TestSignInNotAppliedOnARM64And0C00(t *testing.T) {
	f := withPlaces(newFake())
	f.native = payload.MachineARM64
	start := f.places()
	res, err := newSetup(t, f, []string{payload.BuildARM64X, payload.BuildWOW64}, signInOn).installSI()
	if err != nil || res.SignInFailed != nil {
		t.Fatal(err)
	}
	if recVal(f, RecSignIn) != "0" || len(f.hiveCalls) != 0 || !strings.Contains(strings.Join(res.Notes, " "), "Windows on ARM") {
		t.Errorf("ARM64: record %s, hive calls %v, notes %v", recVal(f, RecSignIn), f.hiveCalls, res.Notes)
	}
	for r, want := range start {
		if f.tree(r) != want {
			t.Errorf("ARM64: %s changed", r)
		}
	}
	f = withPlaces(newFake())
	on := true
	s := newSetup(t, f, allBuilds, func(m *payload.Manifest) {
		m.Layout.LangID, m.Layout.LocaleName, m.Layout.CustomLanguageName = "0C00", "qaa-Latn", "Testish"
	})
	s.SignIn = &on
	res, err = s.installSI()
	if err != nil || res.SignInFailed != nil || recVal(f, RecSignIn) != "0" || len(f.hiveCalls) != 0 {
		t.Errorf("0C00: %v %v %s %v", err, res, recVal(f, RecSignIn), f.hiveCalls)
	}
}

func TestUninstallRefusesOnlyLayoutOfSignInScreen(t *testing.T) {
	f := withPlaces(newFake())
	s := newSetup(t, f, allBuilds, signInOn)
	if _, err := s.installSI(); err != nil {
		t.Fatal(err)
	}
	// An administrator removed US from the sign-in screen: the layout is its only layout.
	pre := Users + `\.DEFAULT\Keyboard Layout\Preload`
	_ = f.DeleteValue(pre, "1")
	before := f.snapshot()
	_, err := s.Uninstall(false)
	if ExitCode(err) != ExitRefused || !strings.Contains(err.Error(), "sign-in screen") {
		t.Fatalf("%v", err)
	}
	if f.snapshot() != before {
		t.Error("a refused uninstall changed something")
	}
	if _, err := s.Uninstall(true); err != nil {
		t.Fatal(err)
	}
	for _, r := range placeRoots() {
		if strings.Contains(f.tree(r), "a0000409") {
			t.Errorf("%s still names the layout", r)
		}
	}
}

func TestUninstallGoesOnWithoutDefaultProfile(t *testing.T) {
	f := withPlaces(newFake())
	s := newSetup(t, f, allBuilds, signInOn)
	if _, err := s.installSI(); err != nil {
		t.Fatal(err)
	}
	f.loadFail = true
	res, err := s.Uninstall(false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "Default profile") || strings.Contains(f.tree(Users+`\.DEFAULT`), "a0000409") {
		t.Errorf("notes %v; .DEFAULT:\n%s", res.Notes, f.tree(Users+`\.DEFAULT`))
	}
	if !strings.Contains(f.tree(defaultHive()), "a0000409") {
		t.Error("the Default profile changed although it could not be loaded")
	}
}

// S-1-5-19, S-1-5-20 and the Default profile without any Preload (and without a language): the install makes the
// layout their only entry, and the uninstall removes it again without the only-layout refusal.
func TestSignInPlacesWithoutPreload(t *testing.T) {
	f := withPlaces(newFake())
	for _, r := range placeRoots()[1:] {
		for k := range f.keys {
			if strings.HasPrefix(k, strings.ToLower(r)+`\`) {
				delete(f.keys, k)
			}
		}
	}
	user := Users + `\` + f.sid + `\Control Panel\International\User Profile\en-US`
	f.mk(user)
	f.set(user, Value{Name: "0409:00000409", Type: RegDWORD, DWord: 1})
	s := newSetup(t, f, allBuilds, signInOn)
	res, err := s.installSI()
	if err != nil || res.SignInFailed != nil {
		t.Fatal(err, res.SignInFailed)
	}
	if v := val(t, f, Users+`\S-1-5-19\Keyboard Layout\Preload`, "1"); v.Str != "00000409" {
		t.Errorf("S-1-5-19 Preload 1 = %q", v.Str)
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	for _, r := range placeRoots() {
		if strings.Contains(f.tree(r), "a0000409") {
			t.Errorf("%s still names the layout", r)
		}
	}
}

// installSI is the elevated part as the program runs it when this process is the user's own account: install, then
// the sign-in step (D97: after the user's enabling, which these tests leave out).
func (s *Setup) installSI() (*Result, error) {
	r, err := s.Install()
	if err == nil {
		s.SignInStep(r)
	}
	return r, err
}
