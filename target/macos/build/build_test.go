package build

import (
	"os"
	"path/filepath"
	"strings"
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

// TestBuildKeepsInput: a build never changes the layout it builds (pin writes the layout, expectations read it).
func TestBuildKeepsInput(t *testing.T) {
	src := `{"kbl": "0.2", "metadata": {"id": "en", "name": "N"}, "positions": {"KeyA": {}, "KeyE": {}},
	 "hardware": {"layers": [{"modifiers": [[]], "bindings": {"KeyA": {"deadKey": "acute"}, "KeyE": "e"}}]},
	 "deadKeys": {"acute": {"spacing": "´", "entries": [{"base": "e", "result": "é"}, {"base": {"utf16": ["0061"]}, "result": "á"}]}}}`
	l, r := kbljson.Read([]byte(src), "t.kbl.json")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	before, _ := kbljson.Write(l)
	res, br := Build(l, Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
	after, _ := kbljson.Write(l)
	if string(before) != string(after) {
		t.Errorf("the build changed its input:\n%s\n---\n%s", before, after)
	}
	if !strings.Contains(string(keylayout.Write(res.Keyboard)), `output="é"`) {
		t.Errorf("the result is not NFC")
	}
}

// TestEmptyOutputAndMaxout: noMatch emitNothing is written as output="" (a <when> with only next="none" made macOS
// reject the layout), and rule maxout counts each output on its own, not a terminator and an output together.
func TestEmptyOutputAndMaxout(t *testing.T) {
	src := `{"kbl": "0.2", "metadata": {"id": "en", "name": "N"},
	 "positions": {"KeyA": {}, "KeyE": {}, "KeyN": {}, "KeyX": {}, "Equal": {}, "Minus": {}},
	 "hardware": {"layers": [{"modifiers": [[]], "bindings": {"KeyA": "a", "KeyE": "e", "KeyN": "ng", "KeyX": "x",
	   "Equal": {"deadKey": "acute"}, "Minus": {"deadKey": "macron"}}}]},
	 "deadKeys": {"acute": {"spacing": "´", "entries": [{"base": "e", "result": "é"}]},
	   "macron": {"spacing": "¯", "noMatch": "emitNothing", "entries": [{"base": "a", "result": "ā"}]}}}`
	l, r := kbljson.Read([]byte(src), "t.kbl.json")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	res, br := Build(l, Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
	out := string(keylayout.Write(res.Keyboard))
	if strings.Contains(out, `next="none"`) {
		t.Errorf("a <when> has next=\"none\":\n%s", out)
	}
	if !strings.Contains(out, `<when state="macron" output=""/>`) {
		t.Errorf("emitNothing is not written as output=\"\":\n%s", out)
	}
	if res.Keyboard.MaxOut == nil || *res.Keyboard.MaxOut != 2 {
		t.Errorf("maxout %v, want 2 (ng; the terminator ´ is not added)", res.Keyboard.MaxOut)
	}
	k, _ := keylayout.Read([]byte(out), "x")
	ty := keylayout.NewTypist(k)
	// macron (Minus 27), x (7), e (14): nothing, then e
	if got := ty.Press(27, 0) + ty.Press(7, 0) + ty.Press(14, 0); got != "e" {
		t.Errorf("macron x e typed %q, want \"e\"", got)
	}
}

// TestNextNamesDefinedState: every next="X" names a state that some <when state="X"> or terminator defines; macOS
// rejects a layout whose next names a state nothing defines (kbl spec targets/macos.md §4). Over the examples, the
// check cases and a file whose dead keys are bound to no key but chain through an entry.
func TestNextNamesDefinedState(t *testing.T) {
	files, _ := filepath.Glob("../../../testdata/kbl-spec/examples/*.kbl.json")
	cases, _ := filepath.Glob("../../../tools/macos-check/cases/*/layout.kbl.json")
	srcs := map[string][]byte{"unbound": []byte(`{"kbl": "0.2", "metadata": {"id": "en", "name": "N"},
	 "positions": {"KeyA": {}, "KeyE": {}},
	 "hardware": {"layers": [{"modifiers": [[]], "bindings": {"KeyA": "a", "KeyE": "e"}}]},
	 "deadKeys": {"acute": {"spacing": "´", "entries": [{"base": "e", "result": {"deadKey": "grave"}}]},
	   "grave": {"spacing": "` + "`" + `", "noMatch": "emitText", "entries": [{"base": "x", "result": "y"}]}}}`)}
	for _, f := range append(files, cases...) {
		data, _ := os.ReadFile(f)
		srcs[f] = data
	}
	for name, data := range srcs {
		l, r := kbljson.Read(data, "t.kbl.json")
		if r.HasErrors() {
			t.Fatalf("%s: %s", name, r.Summary())
		}
		res, _ := Build(l, Options{})
		if res == nil {
			continue
		}
		k := res.Keyboard
		defined := map[string]bool{"none": true}
		for _, a := range k.Actions {
			for _, w := range a.Whens {
				defined[w.State] = true
			}
		}
		for _, w := range k.Terminators {
			defined[w.State] = true
		}
		for _, a := range k.Actions {
			for _, w := range a.Whens {
				if w.Next != "" && (w.Next == "none" || !defined[w.Next]) {
					t.Errorf("%s: action %s: next=%q names no defined state", filepath.Base(name), a.ID, w.Next)
				}
			}
		}
	}
}
