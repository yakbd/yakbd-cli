package dllkbl

import (
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// TestImportKeepsCodePoints: a DLL whose outputs NFC would change (U+2126, U+0958 on a key, a ligature a U+0301, a
// dead character U+1FFD, a base U+2126, U+212B as a dead-key result) imports with settings.normalization "disabled" and builds the same tables again (kbl D74).
func TestImportKeepsCodePoints(t *testing.T) {
	src := "KBD\tkbdn\t\"Test\"\nLOCALEID\t\"00000409\"\nSHIFTSTATE\n0\n1\n2\nLAYOUT\n10\tQ\t0\t2126\t0958\t-1\n11\tW\t0\te000@\tw\t-1\n12\tE\t0\t%%\te\t-1\n13\tR\t0\t1ffd@\tr\t-1\nLIGATURE\nE\t0\t0061 0301\nDEADKEY\te000\n0061\t212b\nDEADKEY\t1ffd\n2126\t0078\nENDKBD\n"
	f, r := klc.Parse([]byte(src), "n.klc")
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := klckbl.FromKLC(f, "n.klc")
	res, br := build.Lower(l, build.Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
	for _, a := range windows.Arches {
		dll, cr := windows.Compile(res.DLL, a)
		if dll == nil {
			t.Fatal(cr.Summary())
		}
		im, ir := FromDLL(dll, "kbdn.dll", Options{LangID: "0409"})
		if im == nil {
			t.Fatal(ir.Summary())
		}
		if im.Settings == nil || im.Settings.Normalization != "disabled" {
			t.Errorf("%v: settings %+v, want normalization disabled", a, im.Settings)
		}
		if diffs, rr, _ := roundTrip(t, dll, "kbdn.dll"); len(diffs) > 0 {
			t.Errorf("%v: tables differ after the round trip: %v\n%s", a, diffs, rr.Summary())
		}
	}
}
