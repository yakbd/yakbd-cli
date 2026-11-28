package keycode

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
	dir := specDir()
	const file = "targets/macos-default-keycode.tsv"
	b, err := exec.Command("git", "-C", dir, "show", SpecCommit+":"+file).Output()
	if err != nil {
		if b, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
			t.Skipf("specification not found (%v); set YAKBD_SPEC_DIR", err)
		}
		t.Logf("commit %s not in %s: compared with the checked-out file", SpecCommit, dir)
	}
	if string(b) != table {
		t.Fatalf("target/macos/keycode/macos-default-keycode.tsv differs from %s; copy it unchanged and update SpecCommit", dir)
	}
}

func TestTable(t *testing.T) {
	if len(Rows) < 100 {
		t.Fatalf("%d rows", len(Rows))
	}
	seen := map[int]string{}
	for _, r := range Rows {
		if p, dup := seen[r.Code]; dup {
			t.Errorf("code %d: %s and %s", r.Code, p, r.Position)
		}
		seen[r.Code] = r.Position
	}
	if r, ok := ForPosition("KeyA"); !ok || r.Code != 0 || r.Output != "" {
		t.Errorf("KeyA %+v", r)
	}
	if r, ok := ForPosition("Enter"); !ok || r.Code != 36 || r.Output != "\r" {
		t.Errorf("Enter %+v", r)
	}
	if r, ok := ForCode(50); !ok || r.Position != "Backquote" {
		t.Errorf("code 50 %+v", r)
	}
}

func specDir() string {
	if d := os.Getenv("YAKBD_SPEC_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "..", "..", "kbl-spec")
}
