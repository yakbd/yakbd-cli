package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"strings"
	"testing"
)

// reportFile is the -report file as a reader of docs/report-file.md sees it.
type reportFile struct {
	Format  int `json:"format"`
	Tool    struct{ Name, Version string }
	Command string `json:"command"`
	Input   *struct {
		File   string `json:"file"`
		Format string `json:"format"`
	} `json:"input"`
	Entries []struct {
		Class    string         `json:"class"`
		Severity string         `json:"severity"`
		Kind     string         `json:"kind"`
		Message  string         `json:"message"`
		Detail   map[string]any `json:"detail"`
		Where    struct {
			File    string  `json:"file"`
			Line    int     `json:"line"`
			Pointer *string `json:"pointer"`
			Missing bool    `json:"missing"`
			Path    string  `json:"path"`
		} `json:"where"`
	} `json:"entries"`
}

func readReport(t *testing.T, path string) *reportFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := &reportFile{}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(r); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return r
}

// reportCorpus is every .kbl.json the test data gives: the spec's examples, and the reference specification's
// .klc corpus and the CLDR keyboards converted to .kbl.json.
func reportCorpus(t *testing.T, dir string) []string {
	files, _ := filepath.Glob("../../testdata/kbl-spec/examples/*.kbl.json")
	var srcs []string
	for _, g := range []string{"../../testdata/reference-spec/*/*.klc", "../../testdata/third_party/cldr-*/keyboards/3.0/*.xml"} {
		m, _ := filepath.Glob(g)
		srcs = append(srcs, m...)
	}
	for i, s := range srcs {
		out := filepath.Join(dir, fmt.Sprintf("c%03d-%s.kbl.json", i, strings.TrimSuffix(filepath.Base(s), filepath.Ext(s))))
		if code, _, _ := run("convert", s, out); code == ExitOK {
			files = append(files, out)
		}
	}
	if len(files) < 40 {
		t.Fatalf("corpus of %d files", len(files))
	}
	return files
}

// TestReportPointers is the contract of docs/report-file.md §3-§4 over the whole corpus and every command that reads a
// .kbl.json: every location in the input document is a JSON pointer (where.pointer), never a where.path; a pointer
// that is not marked missing resolves in the input; a missing one of a default rule resolves in the pinned file
// (the place `yakbd pin` writes the value), and so does every pointer of its detail.fields; any other missing one
// names a member of an object or array the input has.
func TestReportPointers(t *testing.T) {
	dir := t.TempDir()
	seen, rules := map[string]int{}, map[string]bool{}
	for _, in := range reportCorpus(t, dir) {
		doc := readDoc(in)
		if doc == nil {
			t.Fatalf("%s: not JSON", in)
		}
		pinnedPath := filepath.Join(dir, "pinned.kbl.json")
		_ = os.Remove(pinnedPath)
		var pinned any
		if code, _, _ := run("pin", in, pinnedPath); code == ExitOK {
			pinned = readDoc(pinnedPath)
		}
		rep := filepath.Join(dir, "r.json")
		for _, args := range [][]string{
			{"validate", "-windows", in},
			{"compile", "-o", filepath.Join(dir, "out"), in},
			{"installer", "-langid", "0C00", "-o", filepath.Join(dir, "out.exe"), in},
			{"convert", in, filepath.Join(dir, "out.klc")},
			{"convert", in, filepath.Join(dir, "out.xml")},
			{"pin", in, filepath.Join(dir, "out.kbl.json")},
		} {
			_ = os.Remove(rep)
			cmd := append(append([]string{}, args...), "-report", rep, "-q")
			if code, _, stderr := run(cmd...); code == ExitUsage {
				t.Fatalf("%v: usage error %s", cmd, stderr)
			}
			r := readReport(t, rep)
			if r.Format != ReportFormat || r.Tool.Name != "yakbd" || r.Tool.Version != ToolVersion() || r.Command != args[0] ||
				r.Input == nil || r.Input.File != in || r.Input.Format != "kbl.json" {
				t.Fatalf("%v: header %+v %+v", cmd, r, r.Input)
			}
			for _, e := range r.Entries {
				w := e.Where
				id := fmt.Sprintf("%s %s: %s %s", filepath.Base(in), args[0], e.Kind, e.Message)
				if strings.HasPrefix(w.Path, "/") {
					t.Errorf("%s: path %q is a pointer into the input but not where.pointer", id, w.Path)
				}
				if w.Pointer == nil {
					if e.Class == "defaulted" {
						t.Errorf("%s: a default without a pointer (%+v)", id, w)
					}
					continue
				}
				p := *w.Pointer
				seen[e.Class]++
				if e.Class == "defaulted" {
					if p == "/targets/windows" || p == "" {
						t.Errorf("%s: a default points at %q", id, p)
					}
					rules[e.Kind] = true
				}
				if got := resolves(doc, p); got == w.Missing {
					t.Errorf("%s: pointer %q resolves %v but missing is %v", id, p, got, w.Missing)
				}
				if !w.Missing {
					continue
				}
				if e.Class == "defaulted" {
					if pinned == nil {
						continue
					}
					fields := []string{p}
					if fs, ok := e.Detail["fields"].([]any); ok {
						fields = nil
						for _, f := range fs {
							fields = append(fields, f.(string))
						}
					}
					for _, f := range fields {
						if !resolves(pinned, f) {
							t.Errorf("%s: default fills %q, which the pinned file does not have", id, f)
						}
					}
					continue
				}
				if !missingMember(doc, p) || !resolves(pinned, p) && !absentMember.MatchString(p) {
					t.Errorf("%s: missing pointer %q does not name an absent object member of the input", id, p)
				}
			}
		}
	}
	if seen["defaulted"] == 0 || seen["loss"] == 0 || len(rules) < 20 {
		t.Errorf("pointers per class %v, %d default rules: the corpus does not exercise the contract", seen, len(rules))
	}
}

// TestFlagsAfterArguments: every command takes its flags before, between and after its arguments, with the same
// result (docs/report-file.md §5).
func TestFlagsAfterArguments(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ before, after []string }{
		{[]string{"validate", "-windows", "-report", filepath.Join(dir, "a.json"), example}, []string{"validate", example, "-windows", "-report", filepath.Join(dir, "b.json")}},
		{[]string{"compile", "-o", filepath.Join(dir, "a"), "-report", filepath.Join(dir, "a.json"), example}, []string{"compile", example, "-o", filepath.Join(dir, "b"), "-report", filepath.Join(dir, "b.json")}},
		{[]string{"installer", "-o", filepath.Join(dir, "a.exe"), "-report", filepath.Join(dir, "a.json"), example}, []string{"installer", example, "-report", filepath.Join(dir, "b.json"), "-o", filepath.Join(dir, "b.exe")}},
		{[]string{"pin", "-report", filepath.Join(dir, "a.json"), example, filepath.Join(dir, "a.kbl.json")}, []string{"pin", example, "-report", filepath.Join(dir, "b.json"), filepath.Join(dir, "b.kbl.json")}},
		{[]string{"convert", "-report", filepath.Join(dir, "a.json"), example, filepath.Join(dir, "a.klc")}, []string{"convert", example, filepath.Join(dir, "b.klc"), "-report", filepath.Join(dir, "b.json")}},
	} {
		ca, oa, ea := run(c.before...)
		cb, ob, eb := run(c.after...)
		ja, _ := os.ReadFile(filepath.Join(dir, "a.json"))
		jb, _ := os.ReadFile(filepath.Join(dir, "b.json"))
		norm := func(s string) string {
			return strings.NewReplacer(filepath.Join(dir, "a"), "X", filepath.Join(dir, "b"), "X").Replace(s)
		}
		if ca != ExitOK || ca != cb || norm(oa) != norm(ob) || ea != eb || len(ja) == 0 || string(ja) != string(jb) {
			t.Errorf("%v: %d %q %q\n%v: %d %q %q", c.before, ca, oa, ea, c.after, cb, ob, eb)
		}
		for _, f := range []string{"a.json", "b.json"} {
			_ = os.Remove(filepath.Join(dir, f))
		}
	}
	if code, _, _ := run("validate", example, "--", "-windows"); code != ExitUsage {
		t.Error("an argument after -- was read as a flag")
	}
}

// missingMember reports whether pointer p, which does not resolve in doc, fails at a member of an object: the
// place a value would be added (docs/report-file.md §3), not an array index out of range or a step into a scalar.
func missingMember(doc any, p string) bool {
	cur := doc
	for _, tok := range strings.Split(p[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[tok]; !ok {
			return true
		}
	}
	return false
}

// absentMember are the members the corpus's entries ask for without a default rule filling them: a setting, a
// position the layout does not declare, the LANGID, the hardware layers.
var absentMember = regexp.MustCompile(`^(/settings/[A-Za-z]+|/positions/[A-Za-z0-9_]+|/targets/windows/registration/langId|/hardware)$`)

// TestReportOtherInputs: the report of an input that is not a .kbl.json has no pointer (its locations are file and
// line, or a path in the converted layout); a schema error points into the file, the whole file too; a file that is
// not JSON gets entries without a pointer (docs/report-file.md §3).
func TestReportOtherInputs(t *testing.T) {
	dir := t.TempDir()
	rep := filepath.Join(dir, "r.json")
	for _, in := range []string{kbdtst, cldrPT} {
		run("validate", "-windows", "-report", rep, in)
		r := readReport(t, rep)
		if r.Input == nil || r.Input.Format != map[string]string{kbdtst: "klc", cldrPT: "cldr"}[in] || len(r.Entries) == 0 {
			t.Fatalf("%s: %+v", in, r.Input)
		}
		for _, e := range r.Entries {
			if e.Where.Pointer != nil {
				t.Errorf("%s: %s has a pointer %q", in, e.Kind, *e.Where.Pointer)
			}
		}
	}
	bad := filepath.Join(dir, "bad.kbl.json")
	if err := os.WriteFile(bad, []byte(`{"format":"kbl/0.1","metadata":{"name":"x"},"frob":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run("validate", bad, "-report", rep); code != ExitError {
		t.Fatal("bad file valid")
	}
	got := map[string]bool{}
	for _, e := range readReport(t, rep).Entries {
		if e.Where.Pointer == nil || e.Kind != "kbl.schema" {
			t.Errorf("%s %s without a pointer", e.Kind, e.Message)
			continue
		}
		got[fmt.Sprintf("%s %v", *e.Where.Pointer, e.Where.Missing)] = true
	}
	if !got[" false"] || !got["/metadata false"] {
		t.Errorf("schema pointers %v, want the file (\"\") and /metadata among them", got)
	}
	if err := os.WriteFile(bad, []byte(`{"format":`), 0o644); err != nil {
		t.Fatal(err)
	}
	run("validate", bad, "-report", rep)
	for _, e := range readReport(t, rep).Entries {
		if e.Where.Pointer != nil {
			t.Errorf("%s: pointer %q into a file that is not JSON", e.Kind, *e.Where.Pointer)
		}
	}
}
