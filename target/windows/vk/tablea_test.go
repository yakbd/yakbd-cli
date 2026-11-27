package vk

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTableAMatchesSpec fails when the embedded Table A differs from the specification's file at tag TableASpecTag.
// It runs when the spec repository is checked out next to this one (or YAKBD_SPEC_DIR names it) and is skipped
// otherwise. In a git checkout that has the tag, the file is read at the tag; without the tag (before the spec has
// made it) the checked-out file is compared and the test says so.
func TestTableAMatchesSpec(t *testing.T) {
	dir := specDir()
	const file = "targets/windows-default-vk.tsv"
	from := dir + " at tag " + TableASpecTag
	b, err := exec.Command("git", "-C", dir, "show", "refs/tags/"+TableASpecTag+":"+file).Output()
	if err != nil {
		from = dir + " (checked-out file; tag " + TableASpecTag + " not found)"
		if b, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
			t.Skipf("specification not found (%v); set YAKBD_SPEC_DIR", err)
		}
		t.Logf("tag %s not in %s: compared with the checked-out file", TableASpecTag, dir)
	}
	if string(b) != tableA {
		t.Fatalf("target/windows/vk/windows-default-vk.tsv differs from %s; copy it unchanged and update TableASpecTag", from)
	}
}

func TestTableA(t *testing.T) {
	if len(Defaults) != 135 {
		t.Errorf("%d rows, want 135", len(Defaults))
	}
	for _, d := range Defaults {
		if d.VK&0xFF00 != DefaultFlags(d.Scan) {
			t.Errorf("%s: flags %#x differ from rule scanCodeFlags %#x", d.Position, d.VK&0xFF00, DefaultFlags(d.Scan))
		}
	}
	if d, ok := DefaultForPosition("NumpadDecimal"); !ok || d.VK != 0x2E|KBDNUMPAD|KBDSPECIAL || d.Scan != 0x53 {
		t.Errorf("NumpadDecimal %+v", d)
	}
}

// specDir is YAKBD_SPEC_DIR, else the spec repository checked out next to this one: ../kbl-spec, or under its
// former name ../kbl-spec-prototype.
func specDir() string {
	if d := os.Getenv("YAKBD_SPEC_DIR"); d != "" {
		return d
	}
	for _, name := range []string{"kbl-spec", "kbl-spec-prototype"} {
		d := filepath.Join("..", "..", "..", "..", name)
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return filepath.Join("..", "..", "..", "..", "kbl-spec")
}
