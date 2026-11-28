package kbljson

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yakbd/yakbd-cli/jsonschema"
)

// TestSchemaMatchesSpec fails when an embedded schema or the copied examples differ from the specification's, and
// checks the specification's schema test cases (testdata/schema/<version>/valid, invalid) with this reader.
// It runs when the spec repository is checked out next to this one (or YAKBD_SPEC_DIR names it); while 0.2 is a
// draft, that checkout must be on the branch spec-0.2 for the 0.2 schema to be there.
func TestSchemaMatchesSpec(t *testing.T) {
	dir := specDir()
	if _, err := os.Stat(filepath.Join(dir, "schema", "0.1", "kbl.schema.json")); err != nil {
		t.Skipf("specification not found (%v); set YAKBD_SPEC_DIR", err)
	}
	for _, v := range Versions {
		b, err := os.ReadFile(filepath.Join(dir, "schema", v.Kbl, "kbl.schema.json"))
		if err != nil {
			t.Logf("schema %s not in the checkout, not compared (%v): a draft version needs its spec branch", v.Kbl, err)
			continue
		}
		if string(b) != string(v.schemaJSON) {
			t.Errorf("format/kbljson/schema/%s/kbl.schema.json differs from %s; copy it and update its SchemaCommit", v.Kbl, dir)
		}
		for _, valid := range []bool{true, false} {
			sub := "invalid"
			if valid {
				sub = "valid"
			}
			cases, _ := filepath.Glob(filepath.Join(dir, "testdata", "schema", v.Kbl, sub, "*.json"))
			for _, c := range cases {
				data, _ := os.ReadFile(c)
				doc, err := jsonschema.Decode(data)
				if err != nil {
					t.Errorf("%s: %v", c, err)
					continue
				}
				// The case against its version's schema; a valid case also through Validate, which picks the schema itself.
				if errs := v.schema.Validate(doc); (len(errs) == 0) != valid {
					t.Errorf("%s/%s/%s: schema errors %v", v.Kbl, sub, filepath.Base(c), errs)
				}
				if r := Validate(data, filepath.Base(c)); valid && r.HasErrors() {
					t.Errorf("%s/%s/%s:\n%s", v.Kbl, sub, filepath.Base(c), r.Summary())
				}
			}
		}
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
