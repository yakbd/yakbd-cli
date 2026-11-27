package klckbl

import (
	"reflect"
	"testing"

	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// nfcKLC is a .klc file with NFC-clean outputs; each case replaces one part with an output NFC would change.
func nfcKLC(q, e, lig, dead, entry string) string {
	return "KBD\tkbdn\t\"Test\"\nLOCALEID\t\"00000409\"\nSHIFTSTATE\n0\n1\n2\nLAYOUT\n" +
		"10\tQ\t0\t" + q + "\tQ\t-1\n11\tW\t0\t" + dead + "@\tW\t-1\n12\tE\t0\t%%\t" + e + "\t-1\n" +
		"LIGATURE\nE\t0\t" + lig + "\n" +
		"DEADKEY\t" + dead + "\n" + entry + "\nENDKBD\n"
}

// TestImportKeepsCodePoints: importing a .klc file never rewrites its outputs (kbl D74). Each kind of output D74
// names, made non-NFC on its own, makes the importer write normalization "disabled", and the build then emits the
// file's code points; an NFC-clean file keeps the default and builds the same tables.
func TestImportKeepsCodePoints(t *testing.T) {
	clean := []string{"q", "E", "0061 0062", "00b4", "0061\t00e1"}
	cases := []struct {
		name string
		at   int
		with string
	}{
		{"key cell U+2126 (singleton)", 0, "2126"},
		{"key cell U+0958 (exclusion)", 0, "0958"},
		{"key cell U+212B", 1, "212b"},
		{"ligature a U+0301", 2, "0061 0301"},
		{"dead character U+1FFD", 3, "1ffd"},
		{"dead-key result U+212B", 4, "0061\t212b"},
		{"dead-key base U+2126", 4, "2126\t0078"},
	}
	lower := func(src string) (*model.Layout, *build.Result) {
		t.Helper()
		f, r := klc.Parse([]byte(src), "n.klc")
		if r.HasErrors() {
			t.Fatal(r.Summary())
		}
		l, _ := FromKLC(f, "n.klc")
		res, br := build.Lower(l, build.Options{})
		if res == nil {
			t.Fatal(br.Summary())
		}
		for _, e := range br.Entries {
			if e.Code == "general.outputNormalized" {
				t.Errorf("the build normalised an imported output: %s", e)
			}
		}
		return l, res
	}
	l, _ := lower(nfcKLC(clean[0], clean[1], clean[2], clean[3], clean[4]))
	if !l.Normalizes() {
		t.Errorf("an NFC-clean file got normalization %q", l.Settings.Normalization)
	}
	for _, c := range cases {
		p := append([]string(nil), clean...)
		p[c.at] = c.with
		l, res := lower(nfcKLC(p[0], p[1], p[2], p[3], p[4]))
		if l.Normalizes() {
			t.Errorf("%s: normalization not disabled", c.name)
			continue
		}
		// what the file says, unit by unit
		l.Settings.Normalization = "enabled"
		if res2, _ := build.Lower(l, build.Options{Policy: &model.OnLoss{Kinds: map[string]string{"*": "accept"}}}); res2 != nil &&
			reflect.DeepEqual(res2.DLL.Tables, res.DLL.Tables) {
			t.Errorf("%s: NFC would not change the tables, so the case tests nothing", c.name)
		}
	}
}
