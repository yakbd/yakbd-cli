package setup

import (
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// withLanguages gives every place a Languages list "en-US" (and the system hives the System Backup one), as Windows has.
func withLanguages(f *fakeSys) *fakeSys {
	for i, r := range placeRoots() {
		f.set(r+`\Control Panel\International\User Profile`, Value{Name: "Languages", Type: RegMultiSZ, Str: "en-US"})
		if i < 3 {
			b := r + `\Control Panel\International\User Profile System Backup`
			f.mk(b + `\en-US`)
			f.set(b, Value{Name: "Languages", Type: RegMultiSZ, Str: "en-US"})
			f.set(b+`\en-US`, Value{Name: "0409:00000409", Type: RegDWORD, DWord: 1})
		}
	}
	return f
}

func faroese(m *payload.Manifest) { m.SignIn, m.Layout.LangID = true, "0438" }

// D97: a language the places do not have gets Windows' copy shape (VM-19): the tag a user hive gives, appended to
// Languages (and System Backup Languages), its subkeys, Preload 0000<LLLL>, the value 1 in the new subkey; uninstall
// takes it all back, Languages included.
func TestSignInMissingLanguageWindowsCopyShape(t *testing.T) {
	f := withLanguages(withPlaces(newFake()))
	// The installing user has the language, filed by Windows as fo (inst §6 item 4).
	up := Users + `\` + f.sid + `\Control Panel\International\User Profile\fo`
	f.mk(up)
	f.set(up, Value{Name: "0438:00000438", Type: RegDWORD, DWord: 1})
	start := f.places()
	s := newSetup(t, f, allBuilds, faroese)
	res, err := s.installSI()
	if err != nil || res.SignInFailed != nil {
		t.Fatal(err, res.SignInFailed)
	}
	for i, r := range placeRoots() {
		if v := val(t, f, r+`\Keyboard Layout\Preload`, "2"); v.Str != "00000438" {
			t.Errorf("%s Preload 2 = %q", r, v.Str)
		}
		if v := val(t, f, r+`\Keyboard Layout\Substitutes`, "00000438"); v.Str != "a0000438" {
			t.Errorf("%s Substitutes %+v", r, v)
		}
		if v := val(t, f, r+`\Control Panel\International\User Profile\fo`, "0438:A0000438"); v.DWord != 1 {
			t.Errorf("%s User Profile\\fo value %d, want 1", r, v.DWord)
		}
		if v := val(t, f, r+`\Control Panel\International\User Profile`, "Languages"); v.Str != "en-US\x00fo" {
			t.Errorf("%s Languages %q", r, v.Str)
		}
		bk := r + `\Control Panel\International\User Profile System Backup`
		if i < 3 {
			if v := val(t, f, bk, "Languages"); v.Str != "en-US\x00fo" {
				t.Errorf("%s System Backup Languages %q", r, v.Str)
			}
			if v := val(t, f, bk+`\fo`, "0438:A0000438"); v.DWord != 1 {
				t.Errorf("%s System Backup\\fo %d", r, v.DWord)
			}
		} else if f.keys[strings.ToLower(bk)] != nil {
			t.Errorf("the Default profile got a System Backup key")
		}
	}
	if got := recVal(f, RecSignInTags); got != ".DEFAULT=fo\x00Default=fo\x00S-1-5-19=fo\x00S-1-5-20=fo" {
		t.Errorf("record %q", got)
	}
	// A second run adds nothing.
	before := f.snapshot()
	if res, err := newSetup(t, f, allBuilds, faroese).installSI(); err != nil || res.SignInFailed != nil {
		t.Fatal(err)
	}
	if f.snapshot() != before {
		t.Error("the second run changed something")
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	for r, want := range start {
		if got := f.tree(r); got != want {
			t.Errorf("%s after uninstall:\n%s\nwant\n%s", r, got, want)
		}
	}
}

// D97 / Q14: no loaded user hive has the language: the option fails (1603 after enabling), nothing is left in the
// places, the machine part stays, and the message names /f SIGNIN=1.
func TestSignInMissingLanguageWithoutUserFails(t *testing.T) {
	f := withLanguages(withPlaces(newFake()))
	start := f.places()
	res, err := newSetup(t, f, allBuilds, faroese).installSI()
	if err != nil {
		t.Fatal(err)
	}
	if ExitCode(res.SignInFailed) != ExitEnable || !strings.Contains(res.SignInFailed.Error(), "/f SIGNIN=1") {
		t.Fatalf("%v", res.SignInFailed)
	}
	for r, want := range start {
		if f.tree(r) != want {
			t.Errorf("%s changed:\n%s", r, f.tree(r))
		}
	}
	if recVal(f, RecSignIn) != "0" || recVal(f, RecKLID) != "a0000438" {
		t.Errorf("record %s, KLID %s", recVal(f, RecSignIn), recVal(f, RecKLID))
	}
}

// D97: the User Profile value is 1 + the highest in its subkey, not the Preload number.
func TestSignInValueNumber(t *testing.T) {
	f := withPlaces(newFake())
	d := Users + `\.DEFAULT\Control Panel\International\User Profile\en-US`
	f.set(d, Value{Name: "0409:00020409", Type: RegDWORD, DWord: 7})
	if res, err := newSetup(t, f, allBuilds, signInOn).installSI(); err != nil || res.SignInFailed != nil {
		t.Fatal(err)
	}
	if v := val(t, f, d, "0409:A0000409"); v.DWord != 8 {
		t.Errorf("value %d, want 8", v.DWord)
	}
	if v := val(t, f, Users+`\.DEFAULT\Control Panel\International\User Profile System Backup\en-US`, "0409:A0000409"); v.DWord != 8 {
		t.Errorf("System Backup value %d, want 8", v.DWord)
	}
}

// A failure after the tag was added to the system hives takes the tag back too (Languages included).
func TestSignInMissingLanguageRollback(t *testing.T) {
	f := withLanguages(withPlaces(newFake()))
	up := Users + `\` + f.sid + `\Control Panel\International\User Profile\fo`
	f.mk(up)
	f.set(up, Value{Name: "0438:00000438", Type: RegDWORD, DWord: 1})
	start := f.places()
	f.loadFail = true
	res, err := newSetup(t, f, allBuilds, faroese).installSI()
	if err != nil || res.SignInFailed == nil {
		t.Fatal(err)
	}
	for r, want := range start {
		if got := f.tree(r); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", r, got, want)
		}
	}
	if recVal(f, RecSignInTags) != "<none>" {
		t.Errorf("tags recorded: %q", recVal(f, RecSignInTags))
	}
}

// D98 (Q14): as LocalSystem with no user who has the language, the installer lets InstallLayoutOrTip file it in
// .DEFAULT, takes Windows' tag from there, writes the D97 shape in all four places (System Backup included) and
// records .DEFAULT's tag as added; uninstall takes it all back, .DEFAULT's Languages included.
func TestSignInLocalSystemFilesTheLanguage(t *testing.T) {
	f := withLanguages(withPlaces(newFake()))
	// LocalSystem: its hive is .DEFAULT; no S-1-5-21 hive is loaded.
	for k := range f.keys {
		if strings.HasPrefix(k, strings.ToLower(Users+`\`+f.sid)) {
			delete(f.keys, k)
		}
	}
	f.sid = LocalSystemSID
	f.systemTags = map[string]string{"0438": "fo"}
	start := f.places()
	s := newSetup(t, f, allBuilds, faroese)
	res, err := s.installSI()
	if err != nil || res.SignInFailed != nil {
		t.Fatal(err, res.SignInFailed)
	}
	for i, r := range placeRoots() {
		if v := val(t, f, r+`\Control Panel\International\User Profile\fo`, "0438:A0000438"); v.DWord != 1 {
			t.Errorf("%s User Profile\\fo %d", r, v.DWord)
		}
		if v := val(t, f, r+`\Control Panel\International\User Profile`, "Languages"); v.Str != "en-US\x00fo" {
			t.Errorf("%s Languages %q", r, v.Str)
		}
		if i < 3 {
			bk := r + `\Control Panel\International\User Profile System Backup`
			if v := val(t, f, bk, "Languages"); v.Str != "en-US\x00fo" {
				t.Errorf("%s System Backup Languages %q", r, v.Str)
			}
			if v := val(t, f, bk+`\fo`, "0438:A0000438"); v.DWord != 1 {
				t.Errorf("%s System Backup\\fo %d", r, v.DWord)
			}
		}
	}
	if got := recVal(f, RecSignInTags); got != ".DEFAULT=fo\x00Default=fo\x00S-1-5-19=fo\x00S-1-5-20=fo" {
		t.Errorf("record %q", got)
	}
	if _, err := s.Uninstall(false); err != nil {
		t.Fatal(err)
	}
	for r, want := range start {
		if got := f.tree(r); got != want {
			t.Errorf("%s after uninstall:\n%s\nwant\n%s", r, got, want)
		}
	}
}

// Outside LocalSystem the D97 failure stays (TestSignInMissingLanguageWithoutUserFails); as LocalSystem a failure
// later in the run takes back what the call put into .DEFAULT.
func TestSignInLocalSystemRollback(t *testing.T) {
	f := withLanguages(withPlaces(newFake()))
	for k := range f.keys {
		if strings.HasPrefix(k, strings.ToLower(Users+`\`+f.sid)) {
			delete(f.keys, k)
		}
	}
	f.sid = LocalSystemSID
	f.systemTags = map[string]string{"0438": "fo"}
	start := f.places()
	f.loadFail = true
	res, err := newSetup(t, f, allBuilds, faroese).installSI()
	if err != nil || res.SignInFailed == nil {
		t.Fatal(err)
	}
	for r, want := range start {
		if got := f.tree(r); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", r, got, want)
		}
	}
}
