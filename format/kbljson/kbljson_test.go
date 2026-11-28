package kbljson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExamplesValidate(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/kbl-spec/examples/*.kbl.json")
	if len(files) < 4 {
		t.Fatalf("examples missing: %v", files)
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if r := Validate(b, filepath.Base(f)); len(r.Entries) != 0 {
			t.Errorf("%s:\n%s", f, r.Summary())
		}
	}
}

func TestValidateRejects(t *testing.T) {
	ok := `{"kbl":"0.1","metadata":{"id":"en","name":"x"}}`
	tests := []struct{ name, doc, code, path string }{
		{"unknown property", `{"kbl":"0.1","metadata":{"id":"en","name":"x"},"colour":1}`, "kbl.schema", "/colour"},
		{"missing metadata", `{"kbl":"0.1"}`, "kbl.schema", ""},
		{"unknown version", `{"kbl":"0.3","metadata":{"id":"en","name":"x"}}`, "kbl.version", "/kbl"},
		{"version not a string", `{"kbl":2,"metadata":{"id":"en","name":"x"}}`, "kbl.schema", "/kbl"},
		{"0.2 member in a 0.1 file", `{"kbl":"0.1","metadata":{"id":"en","name":"x"},"targets":{"macos":{}}}`, "kbl.schema", "/targets"},
		{"lone high", `{"kbl":"0.1","metadata":{"id":"en","name":"\ud83d"}}`, "kbl.loneSurrogate", ""},
		{"lone low", `{"kbl":"0.1","metadata":{"id":"en","name":"a\ude00"}}`, "kbl.loneSurrogate", ""},
		{"bom", "\xef\xbb\xbf" + ok, "kbl.bom", ""},
		{"not json", `{"kbl":`, "kbl.json", ""},
		{"trailing", ok + `{}`, "kbl.json", ""},
	}
	for _, tt := range tests {
		r := Validate([]byte(tt.doc), "t")
		found := false
		for _, e := range r.Entries {
			if e.Code == tt.code && strings.HasPrefix(e.Where.Path, tt.path) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s at %s, got\n%s", tt.name, tt.code, tt.path, r.Summary())
		}
	}
	if r := Validate([]byte(`{"kbl":"0.1","metadata":{"id":"en","name":"\ud83d\ude00 \\ud83d"}}`), "t"); len(r.Entries) != 0 {
		t.Errorf("a pair or an escaped backslash was taken as a lone surrogate: %s", r.Summary())
	}
	if r := Validate([]byte(ok), "t"); len(r.Entries) != 0 {
		t.Errorf("minimal file: %s", r.Summary())
	}
}

func TestExamplesRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/kbl-spec/examples/*.kbl.json")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		l, r := Read(b, f)
		if l == nil {
			t.Fatalf("%s: %s", f, r.Summary())
		}
		out, wr := Write(l)
		if out == nil {
			t.Fatalf("%s: %s", f, wr.Summary())
		}
		if !SameJSON(b, out) {
			t.Errorf("%s: written file differs from the original at %s", f, DiffJSON(b, out))
		}
		l2, _ := Read(out, f)
		out2, _ := Write(l2)
		if string(out) != string(out2) {
			t.Errorf("%s: second write differs", f)
		}
	}
}
