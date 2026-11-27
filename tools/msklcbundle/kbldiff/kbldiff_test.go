package kbldiff

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/cli"
)

// corpus are the .klc inputs of the control: the tool's corpus and the targeted cases of the MSKLC bundle.
func corpus(t *testing.T) []string {
	var files []string
	for _, g := range []string{"../../../testdata/reference-spec/*/*.klc", "../cases/*.klc"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	sort.Strings(files)
	if len(files) < 46 {
		t.Fatalf("corpus: %d files", len(files))
	}
	return files
}

// TestControl is the host side of the bundle's control step: a .klc built by yakbd itself and imported again
// must mean the same as the .klc imported directly, for every build.
func TestControl(t *testing.T) {
	dir := t.TempDir()
	compared := 0
	defer func() { t.Logf("%d layouts compared", compared) }()
	for _, src := range corpus(t) {
		name := strings.TrimSuffix(filepath.Base(src), ".klc")
		out := filepath.Join(dir, name)
		var o, e bytes.Buffer
		klcJSON := filepath.Join(out, "klc.kbl.json")
		os.MkdirAll(out, 0o755)
		if cli.Run([]string{"convert", "-q", src, klcJSON}, &o, &e) != 0 {
			continue // not importable: the bundle reports it
		}
		if cli.Run([]string{"compile", "-q", "-target", "x64,wow64,x86", "-langid", "0409", "-accept", "windows.vkMissing", "-o", out, src}, &o, &e) != 0 {
			continue
		}
		compared++
		a, _ := os.ReadFile(klcJSON)
		for _, b := range []string{"x64", "wow64", "x86"} {
			dlls, _ := filepath.Glob(filepath.Join(out, b, "*.dll"))
			if len(dlls) != 1 {
				t.Errorf("%s %s: %d DLLs", name, b, len(dlls))
				continue
			}
			imp := filepath.Join(out, b+".kbl.json")
			if cli.Run([]string{"convert", "-q", dlls[0], imp}, &o, &e) != 0 {
				t.Errorf("%s %s: import refused: %s", name, b, e.String())
				continue
			}
			bb, _ := os.ReadFile(imp)
			diffs, err := Files(a, bb)
			if err != nil {
				t.Fatal(err)
			}
			for i, d := range diffs {
				if i == 40 {
					t.Errorf("%s %s: ... %d differences", name, b, len(diffs))
					break
				}
				t.Errorf("%s %s: %s", name, b, d)
			}
		}
	}
}

// TestDetects: the rules that remove differences of form leave real ones: a changed binding, a binding the DLL
// lacks, a Caps Lock binding that is not the fallback, and a changed dead-key entry.
func TestDetects(t *testing.T) {
	a := []byte(`{"kbl":"0.1","metadata":{"id":"en","name":"T"},
	 "positions":{"KeyQ":{"scanCode":"10"},"KeyW":{"scanCode":"11"}},
	 "hardware":{"layers":[{"id":"s0","modifiers":[[]],"bindings":{"KeyQ":"q","KeyW":{"deadKey":"u00b4"}}},
	   {"id":"s1","modifiers":[["shift"]],"bindings":{"KeyQ":"Q","KeyW":"W"}}]},
	 "deadKeys":{"u00b4":{"spacing":"´","entries":[{"base":"a","result":"á"}]}}}`)
	b := []byte(`{"kbl":"0.1","metadata":{"id":"en","name":"T"},
	 "positions":{"KeyQ":{"scanCode":"10"},"KeyW":{"scanCode":"11"},"KeyE":{"scanCode":"12"}},
	 "hardware":{"layers":[{"id":"s0","modifiers":[[]],"bindings":{"KeyQ":"q","KeyW":{"deadKey":"u00b4"},"KeyE":"e"}},
	   {"id":"s1","modifiers":[["shift"]],"bindings":{"KeyQ":"X"}},
	   {"id":"capsS1","modifiers":[["caps","shift"]],"bindings":{"KeyQ":"X","KeyW":"\u0000"}}]},
	 "deadKeys":{"u00b4":{"spacing":"´","entries":[{"base":"a","result":"à"}]}}}`)
	diffs, err := Files(a, b)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range diffs {
		got = append(got, d.Key)
	}
	want := "bind 10 [caps shift],bind 10 [shift],bind 11 [caps shift],bind 11 [shift],deadKey u00b4 entry \"a\""
	if strings.Join(got, ",") != want {
		t.Errorf("got %q\nwant %q", strings.Join(got, ","), want)
	}
}
