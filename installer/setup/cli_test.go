package setup

import (
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// docs/installer-cli.md §6: an older installer over a newer installed version is refused with 1638 and nothing
// changes; the same version is a repair; a newer one an upgrade.
func TestVersionRules(t *testing.T) {
	f := newFake()
	s2 := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Product.Version = "2.0.0.0" })
	if s2.Installed() {
		t.Fatal("installed before the install")
	}
	if _, err := s2.Install(); err != nil {
		t.Fatal(err)
	}
	if !s2.Installed() {
		t.Fatal("not installed after the install")
	}
	before := f.snapshot()
	s1 := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Product.Version = "1.9.9.9" })
	_, err := s1.Install()
	wantCode(t, err, ExitNewer)
	if f.snapshot() != before {
		t.Error("a refused downgrade changed something")
	}
	// The fourth field does not count (Windows Installer compares three): 2.0.0.5 over 2.0.0.0 is the same version.
	same := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Product.Version = "2.0.0.5" })
	if r, err := same.Install(); err != nil || !r.Upgraded {
		t.Fatalf("same version: %+v %v", r, err)
	}
	s3 := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Product.Version = "3.0.0.0" })
	if r, err := s3.Install(); err != nil || !r.Upgraded {
		t.Fatalf("upgrade: %+v %v", r, err)
	}
	if v := val(t, f, s3.arpKey(), "DisplayVersion").Str; v != "3.0.0.0" {
		t.Errorf("DisplayVersion %s", v)
	}
}

// docs/installer-cli.md §7: the uninstall entry's command lines, and the values it must not have.
func TestUninstallEntryCommands(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	exe := `"C:\Program Files (x86)\YAKBD\{01234567-89AB-CDEF-0123-456789ABCDEF}\setup.exe"`
	code := s.m().Product.Code
	for name, want := range map[string]string{
		"UninstallString":      exe + " /x " + code,
		"QuietUninstallString": exe + " /x " + code + " /quiet /norestart",
		"ModifyPath":           exe + " /f " + code,
	} {
		if got := val(t, f, s.arpKey(), name).Str; got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	for _, v := range f.keys[strings.ToLower(s.arpKey())].vals {
		switch strings.ToLower(v.Name) {
		case "windowsinstaller", "nomodify", "norepair", "noremove":
			t.Errorf("the uninstall entry has %s", v.Name)
		}
	}
}
