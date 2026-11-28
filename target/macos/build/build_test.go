package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/keylayout"
)

func TestExamplesBuild(t *testing.T) {
	files, _ := filepath.Glob("../../../testdata/kbl-spec/examples/*.kbl.json")
	if len(files) == 0 {
		t.Fatal("no examples")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		l, r := kbljson.Read(data, f)
		if r.HasErrors() {
			t.Fatalf("%s: %s", f, r.Summary())
		}
		res, br := Build(l, Options{})
		if res == nil {
			t.Logf("%s: no build: %s", filepath.Base(f), br.Summary())
			continue
		}
		out := keylayout.Write(res.Keyboard)
		if os.Getenv("DUMP") != "" {
			os.WriteFile(filepath.Join(os.Getenv("DUMP"), filepath.Base(f)+".keylayout"), out, 0o644)
			t.Logf("%s:\n%s", filepath.Base(f), br.Summary())
		}
		k, kr := keylayout.Read(out, "x")
		if k == nil {
			t.Errorf("%s: written file does not read: %s", f, kr.Summary())
		}
	}
}

// TestPin: a pinned example builds the same layout and reports no macosDefault entry; a second pin changes nothing.
func TestPin(t *testing.T) {
	files, _ := filepath.Glob("../../../testdata/kbl-spec/examples/*.kbl.json")
	for _, f := range files {
		data, _ := os.ReadFile(f)
		l, _ := kbljson.Read(data, f)
		res, _ := Build(l, Options{})
		if res == nil {
			continue
		}
		p, _ := Pin(l, Options{})
		js, jr := kbljson.Write(p)
		if js == nil {
			t.Fatalf("%s: pinned file does not write: %s", f, jr.Summary())
		}
		p2, rr := kbljson.Read(js, "p.kbl.json")
		if rr.HasErrors() {
			t.Fatalf("%s: %s", f, rr.Summary())
		}
		res2, r2 := Build(p2, Options{})
		if res2 == nil {
			t.Fatalf("%s: pinned file does not build: %s", f, r2.Summary())
		}
		for _, e := range r2.Entries {
			if e.Class == 2 { // loss.Defaulted
				t.Errorf("%s: pinned build used rule %s", filepath.Base(f), e.Code)
			}
		}
		if string(keylayout.Write(res.Keyboard)) != string(keylayout.Write(res2.Keyboard)) {
			t.Errorf("%s: pinned build differs", filepath.Base(f))
		}
		p3, _ := Pin(p2, Options{})
		js3, _ := kbljson.Write(p3)
		if string(js3) != string(js) {
			t.Errorf("%s: second pin changes the file", filepath.Base(f))
		}
	}
}
