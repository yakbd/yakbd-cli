package kbljson

import (
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/model"
)

// TestVersions: a 0.1 file and a 0.2 file both read; a writer writes the lowest version that holds the layout
// (SPEC §1.1), and changes `kbl` and `$schema` together.
func TestVersions(t *testing.T) {
	const s01 = "https://yakbd.github.io/kbl-spec/schema/0.1/kbl.schema.json"
	const s02 = "https://yakbd.github.io/kbl-spec/schema/0.2/kbl.schema.json"
	body := `"metadata":{"id":"en","name":"x"},"positions":{"KeyA":{"scanCode":"1E"}},"hardware":{"layers":[{"modifiers":[[]],"bindings":{"KeyA":"a"}}]}`
	tests := []struct {
		name, doc, kbl, schema string
		addMacOS               bool
	}{
		{"0.1 stays 0.1", `{"$schema":"` + s01 + `","kbl":"0.1",` + body + `}`, "0.1", s01, false},
		{"0.2 using nothing new is written as 0.1", `{"$schema":"` + s02 + `","kbl":"0.2",` + body + `}`, "0.1", s01, false},
		{"0.2 with macos", `{"kbl":"0.2",` + body + `,"targets":{"macos":{"onLoss":{"kinds":{"macos.*":"error"}}}}}`, "0.2", "", false},
		{"0.1 raised by targets.macos", `{"$schema":"` + s01 + `","kbl":"0.1",` + body + `}`, "0.2", s02, true},
		{"0.1 with a windows kind stays 0.1", `{"kbl":"0.1",` + strings.Replace(body, `"metadata":{`, `"onLoss":{"kinds":{"windows.*":"accept"}},"metadata":{`, 1) + `}`, "0.1", "", false},
	}
	for _, tt := range tests {
		l, r := Read([]byte(tt.doc), "t")
		if l == nil {
			t.Fatalf("%s: %s", tt.name, r.Summary())
		}
		if tt.addMacOS {
			l.Targets = &model.Targets{MacOS: &model.MacOSTarget{}}
		}
		out, wr := Write(l)
		if out == nil {
			t.Fatalf("%s: %s", tt.name, wr.Summary())
		}
		back, rr := Read(out, "back")
		if back == nil {
			t.Fatalf("%s: written file: %s", tt.name, rr.Summary())
		}
		if back.Kbl != tt.kbl || back.Schema != tt.schema {
			t.Errorf("%s: wrote kbl %q $schema %q, want %q %q", tt.name, back.Kbl, back.Schema, tt.kbl, tt.schema)
		}
	}
	l := &model.Layout{Kbl: "0.1", OnLoss: &model.OnLoss{Kinds: map[string]string{"xkbDefault.*": "error"}}}
	if v := model.MinVersion(l); v != "0.2" {
		t.Errorf("xkbDefault kind: MinVersion %s", v)
	}
	l = &model.Layout{Kbl: "0.1", Metadata: model.Metadata{Provenance: &model.Provenance{Format: "keylayout"}}}
	if v := model.MinVersion(l); v != "0.2" {
		t.Errorf("keylayout provenance: MinVersion %s", v)
	}
}

// TestVersion03: what only 0.3 has raises a file to 0.3 (SPEC §1.1); a 0.3 file that uses none of it is written as
// the lowest version that holds it.
func TestVersion03(t *testing.T) {
	const s03 = "https://yakbd.github.io/kbl-spec/schema/0.3/kbl.schema.json"
	touch := `"keys":{"a":{"output":"a"}},"touch":[{"layers":[{"id":"base","rows":[["a"]]}]}]`
	for _, tt := range []struct{ name, doc, kbl string }{
		{"0.3 using nothing new", `{"$schema":"` + s03 + `","kbl":"0.3","metadata":{"id":"en","name":"x"},` + touch + `}`, "0.1"},
		{"targets.androidime", `{"kbl":"0.3","metadata":{"id":"en","name":"x"},` + touch + `,"targets":{"androidime":{}}}`, "0.3"},
		{"hint", `{"kbl":"0.3","metadata":{"id":"en","name":"x"},"keys":{"a":{"output":"a","hint":"á"}},"touch":[{"layers":[{"id":"base","rows":[["a"]]}]}]}`, "0.3"},
		{"rowHeights", `{"kbl":"0.3","metadata":{"id":"en","name":"x"},"keys":{"a":{"output":"a"}},"touch":[{"layers":[{"id":"base","rows":[["a"]],"rowHeights":[1.5]}]}]}`, "0.3"},
		{"systemRow false", `{"kbl":"0.3","metadata":{"id":"en","name":"x"},"keys":{"a":{"output":"a"}},"touch":[{"systemRow":false,"layers":[{"id":"base","rows":[["a"]]}]}]}`, "0.3"},
		{"a 0.3 kind", `{"kbl":"0.3","metadata":{"id":"en","name":"x"},"onLoss":{"kinds":{"cldr.touchDataDropped":"error"}},` + touch + `}`, "0.3"},
		{"a 0.3 family", `{"kbl":"0.3","metadata":{"id":"en","name":"x"},"onLoss":{"kinds":{"iosDefault.*":"error"}},` + touch + `}`, "0.3"},
		{"provenance kcm", `{"kbl":"0.3","metadata":{"id":"en","name":"x","provenance":{"format":"kcm"}},` + touch + `}`, "0.3"},
	} {
		l, r := Read([]byte(tt.doc), "t")
		if l == nil {
			t.Fatalf("%s: %s", tt.name, r.Summary())
		}
		out, wr := Write(l)
		if out == nil {
			t.Fatalf("%s: %s", tt.name, wr.Summary())
		}
		back, rr := Read(out, "back")
		if back == nil {
			t.Fatalf("%s: written file: %s", tt.name, rr.Summary())
		}
		if back.Kbl != tt.kbl {
			t.Errorf("%s: wrote kbl %q, want %q", tt.name, back.Kbl, tt.kbl)
		}
		if again, _ := Write(back); string(again) != string(out) {
			t.Errorf("%s: write → read → write differs", tt.name)
		}
	}
	// The 0.3 members are refused in a 0.2 file (each schema accepts only its own version).
	if r := Validate([]byte(`{"kbl":"0.2","metadata":{"id":"en","name":"x"},"targets":{"ios":{}}}`), "t"); !r.HasErrors() {
		t.Error("targets.ios accepted in a 0.2 file")
	}
}
