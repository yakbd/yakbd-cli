package klckbl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// exportCorpus is every layout of the test corpus as a model: the .klc inputs yakbd reads, the kbl examples and
// the CLDR keyboards.
func exportCorpus(t *testing.T) map[string]*model.Layout {
	t.Helper()
	out := map[string]*model.Layout{}
	for _, g := range []string{"../../testdata/reference-spec/*/*.klc", "../../testdata/kbl-spec/examples/*.kbl.json",
		"../../testdata/third_party/cldr-release-49/keyboards/3.0/*.xml", "../../tools/msklcbundle/cases/*.klc"} {
		files, _ := filepath.Glob(g)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(f)
			var l *model.Layout
			switch {
			case strings.HasSuffix(f, ".klc"):
				kf, r := klc.Parse(b, name)
				if kf == nil || r.HasErrors() {
					continue
				}
				l, _ = FromKLC(kf, name)
			case strings.HasSuffix(f, ".kbl.json"):
				l, _ = kbljson.Read(b, name)
			default:
				l, _ = cldr.Read(b, name)
			}
			if l != nil {
				out[name] = l
			}
		}
	}
	if len(out) < 30 {
		t.Fatalf("corpus: %d layouts", len(out))
	}
	return out
}

// TestExportReadsBack: every corpus layout that the exporter writes as .klc (for yakbd and for MSKLC) parses
// without an error and imports to a layout whose Windows build gives the same tables as the original's, unless
// the export reported what it could not say (klc.notRepresentable). A file the exporter cannot write correctly is
// never written: the export then fails with an error.
func TestExportReadsBack(t *testing.T) {
	opt := build.Options{Policy: &model.OnLoss{Kinds: map[string]string{"windows.*": "accept", "general.*": "accept"}}}
	written := 0
	for name, l := range exportCorpus(t) {
		if l.Targets == nil {
			l.Targets = &model.Targets{}
		}
		if l.Targets.Windows == nil {
			l.Targets.Windows = &model.WindowsTarget{}
		}
		if l.Targets.Windows.Registration == nil {
			l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
		}
		orig, br := build.Lower(l, opt)
		if orig == nil {
			continue // the corpus file does not build at all: nothing to export
		}
		f, er := ToKLC(l, opt)
		if f == nil || er.HasErrors() {
			t.Logf("%s: not exported: %s", name, er.Summary())
			continue
		}
		lossy, added := false, false
		for _, e := range er.Entries {
			lossy = lossy || e.Code == "klc.notRepresentable"
			added = added || e.Code == "klc.impliedRows" && e.Detail["added"] != nil
		}
		for _, msklc := range []bool{false, true} {
			data, wr := klc.Write(f, klc.WriteOptions{ForMSKLC: msklc})
			if data == nil || wr.HasErrors() {
				t.Logf("%s (msklc %v): not written: %s", name, msklc, firstKinds(wr))
				continue
			}
			written++
			kf, pr := klc.Parse(data, name+".klc")
			if kf == nil || pr.HasErrors() {
				t.Errorf("%s (msklc %v): the export does not read back: %s", name, msklc, pr.Summary())
				continue
			}
			l2, ir := FromKLC(kf, name+".klc")
			if l2 == nil || ir.HasErrors() {
				t.Errorf("%s (msklc %v): the export does not import: %s", name, msklc, ir.Summary())
				continue
			}
			if l2.Targets.Windows.Registration == nil {
				l2.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
			}
			back, br2 := build.Lower(l2, opt)
			if back == nil {
				t.Errorf("%s (msklc %v): the import of the export does not build: %s", name, msklc, br2.Summary())
				continue
			}
			if d := windows.Differences(&orig.DLL.Tables, &back.DLL.Tables); len(d) > 0 && !lossy {
				if !added || !onlyRows(d) {
					t.Errorf("%s (msklc %v): tables differ beyond the reported implied rows: %v\n%s", name, msklc, d, er.Summary())
				}
			}
			// The export of the import is the same file: the .klc form is a fixed point.
			g2, er2 := ToKLC(l2, opt)
			if g2 == nil || er2.HasErrors() {
				t.Errorf("%s (msklc %v): the import of the export does not export again: %s", name, msklc, er2.Summary())
				continue
			}
			if data2, _ := klc.Write(g2, klc.WriteOptions{ForMSKLC: msklc}); !bytes.Equal(data, data2) {
				t.Errorf("%s (msklc %v): the second export differs from the first", name, msklc)
			}
		}
		_ = br
	}
	if written < 40 {
		t.Errorf("only %d exports written", written)
	}
}

func firstKinds(r *loss.Report) string {
	var k []string
	seen := map[string]bool{}
	for _, e := range r.Entries {
		if e.Severity >= loss.Error && !seen[e.Code] {
			seen[e.Code] = true
			k = append(k, e.Code)
		}
	}
	return strings.Join(k, " ")
}

// onlyRows says whether the differences are those that added implied rows make: rows, the number and widths of
// character tables, and ModNumber (the shift states a .klc file must declare).
func onlyRows(d []string) bool {
	for _, s := range d {
		if s != "ModNumber" && !strings.HasPrefix(s, "character tables:") && !strings.HasPrefix(s, "table ") {
			return false
		}
	}
	return true
}

// TestMSKLCForms: what the VM run of 2026-11-26 showed about kbdutool: an SGCAP continuation cell -1 under a
// character is "Caps Lock gives nothing" (explicit nothing, D64); a supplementary character is exported as a
// LIGATURE line with the column index (OBS W3), and every row is written with a cell for every state.
func TestMSKLCForms(t *testing.T) {
	src := "KBD\tt\t\"T\"\nSHIFTSTATE\n0\n1\n2\n6\nLAYOUT\n16\tU\tSGCap\tu\tU\t-1\t-1\n-1\t-1\t0\t00fc\t-1\n" +
		"39\tSPACE\t0\t0020\t0020\n32\tM\t1\tm\tM\t-1\t%%\nLIGATURE\nM\t3\td835\tdd2a\nENDKBD\n"
	kf, r := klc.Parse([]byte(src), "t.klc")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := FromKLC(kf, "t.klc")
	var capsShift *model.Binding
	for _, layer := range l.Hardware.Layers {
		if layer.ID == "capsS1" {
			capsShift = layer.Bindings["KeyU"]
		}
	}
	if capsShift == nil || capsShift.Kind != model.BindOutput {
		t.Errorf("Caps Lock + Shift + U: %+v, want the explicit nothing", capsShift)
	}
	l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
	f, er := ToKLC(l, build.Options{})
	if f == nil || er.HasErrors() {
		t.Fatal(er.Summary())
	}
	data, _ := klc.Write(f, klc.WriteOptions{ForMSKLC: true})
	text := decodeUTF16(data)
	if !strings.Contains(text, "M\t3\td835\tdd2a") || strings.Contains(text, "1d52a") {
		t.Errorf("the supplementary character is not a LIGATURE line:\n%s", text)
	}
	if !strings.Contains(text, "39\tSPACE") {
		t.Errorf("no SPACE row:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "39\tSPACE") && len(strings.Fields(strings.SplitN(line, "//", 2)[0])) != 3+4 {
			t.Errorf("row without a cell per state: %q", line)
		}
	}
}

func decodeUTF16(b []byte) string {
	var u []uint16
	for i := 2; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return strings.ReplaceAll(string(utf16.Decode(u)), "\r", "")
}

// TestCapsOnlyDeadNotRepresentable: a dead key only on the Caps Lock row (writer rule W-2) has no .klc form (kbl
// D81): the exporter writes the plain dead character in the continuation line and reports klc.notRepresentable.
func TestCapsOnlyDeadNotRepresentable(t *testing.T) {
	src := `{"kbl":"0.1","metadata":{"id":"en","name":"T"},
	 "positions":{"KeyQ":{"scanCode":"10"},"KeyA":{"scanCode":"1E"}},
	 "hardware":{"layers":[{"id":"s0","modifiers":[[]],"bindings":{"KeyQ":"q","KeyA":"a"}},
	   {"id":"s1","modifiers":[["shift"]],"bindings":{"KeyQ":"Q","KeyA":"A"}},
	   {"id":"capsS0","modifiers":[["caps"]],"bindings":{"KeyQ":{"deadKey":"u00b4"}}},
	   {"id":"capsS1","modifiers":[["caps","shift"]],"bindings":{"KeyQ":"q"}}]},
	 "deadKeys":{"u00b4":{"spacing":"´","entries":[{"base":"a","result":"á"}]}},
	 "targets":{"windows":{"registration":{"langId":"0409"}}}}`
	l, r := kbljson.Read([]byte(src), "t.kbl.json")
	if l == nil || r.HasErrors() {
		t.Fatal(r.Summary())
	}
	opt := build.Options{Policy: &model.OnLoss{Kinds: map[string]string{"klc.notRepresentable": "accept"}}}
	f, er := ToKLC(l, opt)
	if f == nil {
		t.Fatal(er.Summary())
	}
	reported := false
	for _, e := range er.Entries {
		reported = reported || e.Code == "klc.notRepresentable" && strings.Contains(e.Message, "Caps Lock dead key")
	}
	if !reported {
		t.Errorf("no klc.notRepresentable: %s", er.Summary())
	}
	for _, k := range f.Keys {
		if k.VK == 'Q' {
			if len(k.SGCap) < 1 || k.SGCap[0].Kind != klc.CellChar || k.SGCap[0].Rune != 0xB4 {
				t.Errorf("continuation %+v, want the plain character 00B4", k.SGCap)
			}
		}
	}
}
