package keyname

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTableMatchesSpec fails when the embedded table differs from the specification's file at SpecCommit (or the
// checked-out file when the checkout lacks that commit). Skipped when the spec repository is not next to this one
// and YAKBD_SPEC_DIR is not set.
func TestTableMatchesSpec(t *testing.T) {
	dir := os.Getenv("YAKBD_SPEC_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "..", "kbl-spec")
	}
	const file = "targets/xkb-default-keyname.tsv"
	b, err := exec.Command("git", "-C", dir, "show", SpecCommit+":"+file).Output()
	if err != nil {
		if b, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
			t.Skipf("specification not found (%v); set YAKBD_SPEC_DIR", err)
		}
		t.Logf("commit %s not in %s: compared with the checked-out file", SpecCommit, dir)
	}
	if string(b) != table {
		t.Fatalf("target/xkb/keyname/xkb-default-keyname.tsv differs from %s; copy it unchanged and update SpecCommit", dir)
	}
}

func TestTable(t *testing.T) {
	if len(Rows) < 100 {
		t.Fatalf("%d rows", len(Rows))
	}
	for _, c := range [][2]string{{"KeyQ", "AD01"}, {"LatQ", "AD01"}, {"AC12", "BKSL"}, {"MENU", "COMP"}, {"I133", "LWIN"}} {
		p, ok := ForKeyName(c[0])
		r, _ := ForPosition(p)
		if c[0] == "KeyQ" {
			r, ok = ForPosition(c[0])
		}
		if !ok || r.KeyName != c[1] || Canonical(c[0]) != c[1] && c[0] != "KeyQ" {
			t.Errorf("%s: %+v", c[0], r)
		}
	}
	if r, _ := ForPosition("Numpad7"); r.Keypad != "KP_Home" {
		t.Errorf("Numpad7: %+v", r)
	}
}
