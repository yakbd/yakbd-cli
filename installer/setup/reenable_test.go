package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// TestShapeRefused: a payload file that fails the shape check stops a fresh install and an upgrade before
// anything changes (kbl targets/windows-install.md §3).
func TestShapeRefused(t *testing.T) {
	f := newFake()
	bad := func(build string, data []byte) error {
		if build == payload.BuildWOW64 {
			return errors.New("the wow64 file is not a WOW64 layout file: it has the shape of a file for x86")
		}
		return nil
	}
	before := f.snapshot()
	s := newSetup(t, f, allBuilds, nil)
	s.CheckFile = bad
	_, err := s.Install()
	wantCode(t, err, ExitDamaged)
	if err == nil || !strings.Contains(err.Error(), "damaged") || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Errorf("message %v", err)
	}
	if f.snapshot() != before {
		t.Error("a refused install changed something")
	}
	s.CheckFile = nil
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	installed := f.snapshot()
	s2 := newSetup(t, f, allBuilds, func(m *payload.Manifest) { m.Product.Version = "2.0.0.0" })
	s2.CheckFile = bad
	_, err = s2.Install()
	wantCode(t, err, ExitDamaged)
	if f.snapshot() != installed {
		t.Error("a refused upgrade changed something")
	}
	// Only the files of this machine are checked: on x86 Windows the bad wow64 file is not used.
	f2 := newFake()
	f2.native = payload.MachineI386
	s3 := newSetup(t, f2, allBuilds, nil)
	s3.CheckFile = bad
	if _, err := s3.Install(); err != nil {
		t.Errorf("x86 machine: %v", err)
	}
}

// installedEnabled installs and enables the layout and returns the setup, its KLID and Layout Id.
func installedEnabled(t *testing.T, f *fakeSys) (*Setup, *Result) {
	t.Helper()
	s := newSetup(t, f, allBuilds, nil)
	first, err := s.Install()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	return s, first
}

// takeID makes the M7 case: the KLID key is gone and another layout of the language has its Layout Id.
func takeID(t *testing.T, f *fakeSys, first *Result) {
	t.Helper()
	if err := f.DeleteKey(klidKey(first.KLID)); err != nil {
		t.Fatal(err)
	}
	f.mk(klidKey("00990409"))
	f.set(klidKey("00990409"), Value{Name: "Layout Id", Type: RegSZ, Str: first.LayoutID})
}

func TestReenableAfterNewLayoutID(t *testing.T) {
	f := newFake()
	s, first := installedEnabled(t, f)
	b := s.Before()
	if !b.Enabled || b.KLID != first.KLID || b.LayoutID != first.LayoutID {
		t.Fatalf("before %+v", b)
	}
	// Nothing changed: nothing to do.
	f.ilot = nil
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	if _, ran, err := s.Reenable(b); ran || err != nil || len(f.ilot) != 0 {
		t.Fatalf("same Layout Id: ran %v %v %v", ran, err, f.ilot)
	}

	takeID(t, f, first)
	res, err := s.Install()
	if err != nil || res.LayoutID != "00fe" {
		t.Fatalf("repair %+v %v", res, err)
	}
	// Windows loads the new HKL on the first call.
	f.onILOT = func(str string, flags uint32) {
		if flags == 0 {
			f.layouts = []uint32{0x04090409, HKL(0x0409, 0xFE)}
		}
	}
	f.ilot = nil
	r, ran, err := s.Reenable(b)
	if !ran || err != nil || strings.Join(f.ilot, ",") != "0409:A0000409/1,0409:A0000409/0" || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "enabled for you again") {
		t.Fatalf("ran %v %v %v %+v", ran, err, f.ilot, r)
	}
	if !s.inProfile(f.sid, first.KLID) {
		t.Error("not in User Profile")
	}
	// A second run with the old state: the record says 00fe now, so a Before taken now gives nothing to do.
	if _, ran, _ := s.Reenable(s.Before()); ran {
		t.Error("ran again")
	}
}

// TestReenableHKLNotSeen: the new HKL is not in this process's list: one call, a note to sign out, no error.
func TestReenableHKLNotSeen(t *testing.T) {
	f := newFake()
	s, first := installedEnabled(t, f)
	b := s.Before()
	takeID(t, f, first)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	f.layouts = nil
	f.ilot = nil
	r, ran, err := s.Reenable(b)
	if !ran || err != nil || strings.Join(f.ilot, ",") != "0409:A0000409/1,0409:A0000409/0" || !strings.Contains(r.Notes[0], "sign out") {
		t.Fatalf("ran %v %v %v %+v", ran, err, f.ilot, r)
	}
}

func TestReenableNotForOthers(t *testing.T) {
	f := newFake()
	s := newSetup(t, f, allBuilds, nil)
	first, err := s.Install()
	if err != nil {
		t.Fatal(err)
	}
	// This user never had the layout.
	b := s.Before()
	if b.Enabled {
		t.Fatal("enabled without Enable")
	}
	takeID(t, f, first)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	f.ilot = nil
	if _, ran, err := s.Reenable(b); ran || err != nil || len(f.ilot) != 0 {
		t.Fatalf("ran %v %v %v", ran, err, f.ilot)
	}
	// Not installed at all: nothing.
	if _, ran, _ := newSetup(t, newFake(), allBuilds, nil).Reenable(Before{}); ran {
		t.Error("ran without an installation")
	}
}

// TestReenableOnlyLayout: the user's only layout is not removed first; it is added again only.
func TestReenableOnlyLayout(t *testing.T) {
	f := newFake()
	s, first := installedEnabled(t, f)
	_ = f.DeleteValue(Users+`\`+f.sid+`\Keyboard Layout\Preload`, "1") // Preload now holds only the layout
	b := s.Before()
	takeID(t, f, first)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	f.ilot = nil
	_, ran, err := s.Reenable(b)
	if !ran || err != nil || strings.Join(f.ilot, ",") != "0409:A0000409/0" {
		t.Fatalf("ran %v %v %v", ran, err, f.ilot)
	}
}
