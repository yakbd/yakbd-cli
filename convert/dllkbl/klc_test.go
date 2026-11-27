package dllkbl

import (
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// TestToKLC: an imported DLL written as .klc and read back builds the same tables, or the export reported
// klc.notRepresentable (table order, Table A defaults, flags a .klc file cannot hold).
func TestToKLC(t *testing.T) {
	stats := map[string]int{}
	for seed := int64(1); seed <= 60; seed++ {
		tb := randomTables(seed)
		dll := encodeForeign(tb, shape{arch: windows.AMD64, seed: seed})
		l, _ := FromDLL(dll, "k.dll", Options{LangID: "0409", KeepKeyboardType: true})
		f, r := klckbl.ToKLC(l, build.Options{})
		if f == nil {
			stats["toklc failed"]++
			continue
		}
		bad := false
		for _, e := range r.Entries {
			if e.Severity >= loss.Error {
				bad = true
				stats["err "+e.Code]++
			}
		}
		data, _ := klc.Write(f, klc.WriteOptions{})
		kf, _ := klc.Parse(data, "k.klc")
		l2, _ := klckbl.FromKLC(kf, "k.klc")
		l2.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
		res, _ := build.Lower(l2, build.Options{Policy: rebuildPolicy})
		if res == nil {
			stats["rebuild failed"]++
			continue
		}
		d := windows.Differences(tb, &res.DLL.Tables)
		if len(d) == 0 {
			stats["same"]++
		} else if bad {
			stats["differ, reported"]++
		} else {
			stats["differ, unreported"]++
			if stats["differ, unreported"] < 3 {
				t.Log(d)
			}
		}
	}
	if stats["differ, unreported"] > 0 || stats["toklc failed"] > 0 || stats["rebuild failed"] > 0 {
		t.Errorf("%v", stats)
	}
}
