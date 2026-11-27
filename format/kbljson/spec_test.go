package kbljson

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSchemaMatchesSpec fails when the embedded schema or the copied examples differ from the specification's.
// It runs when the spec repository is checked out next to this one (or YAKBD_SPEC_DIR names it).
func TestSchemaMatchesSpec(t *testing.T) {
	dir := specDir()
	b, err := os.ReadFile(filepath.Join(dir, "schema", "0.1", "kbl.schema.json"))
	if err != nil {
		t.Skipf("specification not found (%v); set YAKBD_SPEC_DIR", err)
	}
	if string(b) != string(schemaJSON) {
		t.Errorf("format/kbljson/schema/kbl.schema.json differs from %s; copy it and update SchemaCommit", dir)
	}
	examples, _ := filepath.Glob(filepath.Join(dir, "examples", "*.kbl.json"))
	for _, e := range examples {
		want, _ := os.ReadFile(e)
		got, err := os.ReadFile(filepath.Join("..", "..", "testdata", "kbl-spec", "examples", filepath.Base(e)))
		if err != nil || string(got) != string(want) {
			t.Errorf("testdata copy of %s differs", filepath.Base(e))
		}
	}
}

// specDir is YAKBD_SPEC_DIR, else the spec repository checked out next to this one: ../kbl-spec, or under its
// former name ../kbl-spec-prototype.
func specDir() string {
	if d := os.Getenv("YAKBD_SPEC_DIR"); d != "" {
		return d
	}
	for _, name := range []string{"kbl-spec", "kbl-spec-prototype"} {
		d := filepath.Join("..", "..", "..", name)
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return filepath.Join("..", "..", "..", "kbl-spec")
}
