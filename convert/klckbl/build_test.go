package klckbl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

func lower(t *testing.T, src []byte, name string) *build.Result {
	t.Helper()
	f, r := klc.Parse(src, name)
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := FromKLC(f, name)
	if l.Targets.Windows.Registration == nil {
		l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
	}
	res, br := build.Lower(l, build.Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
	return res
}

func findRow(d *windows.DLL, v byte) (windows.CharRow, windows.CharRow) {
	for _, ct := range d.Tables.CharTables {
		for i, r := range ct.Rows {
			if r.VK == v {
				var next windows.CharRow
				if i+1 < len(ct.Rows) {
					next = ct.Rows[i+1]
				}
				return r, next
			}
		}
	}
	return windows.CharRow{}, windows.CharRow{}
}

// TestKbdtstTables builds kbdtst.klc through the model and checks its tables on all three builds.
func TestKbdtstTables(t *testing.T) {
	b, _ := os.ReadFile("../../testdata/reference-spec/vm/kbdtst.klc")
	res := lower(t, b, "kbdtst.klc")
	d := res.DLL
	set := windows.CompileAll(d, res.Targets)
	if set.Report.HasErrors() || len(set.Builds) != 4 {
		t.Fatal(set.Report.Summary())
	}
	for _, bld := range set.Builds {
		got, _, err := windows.Read(bld.Bytes)
		if err != nil || !reflect.DeepEqual(got, d) {
			t.Fatalf("%v: read back differs (%v)", bld.Target, err)
		}
	}
	tb := d.Tables
	if tb.LocaleFlags != windows.KLLF_ALTGR || !reflect.DeepEqual(tb.Modifiers.ModNumber, []byte{0, 1, 2, 15, 15, 15, 3}) {
		t.Errorf("flags %#x ModNumber %v", tb.LocaleFlags, tb.Modifiers.ModNumber)
	}
	if q, _ := findRow(d, 'Q'); !reflect.DeepEqual(q.Chars, []uint16{0x298, 0x1c3, windows.WCH_NONE, 0x40}) {
		t.Errorf("Q %v", q.Chars)
	}
	if g, ff := findRow(d, 0xC0); g.Chars[0] != windows.WCH_DEAD || ff.VK != 0xFF || ff.Chars[0] != 0x60 {
		t.Errorf("dead rows %v %v", g, ff)
	}
	if r, _ := findRow(d, 0xE2); r.Chars[1] != windows.WCH_LGTR || len(tb.Ligatures) != 1 || tb.Ligatures[0].Column != 1 {
		t.Errorf("ligature %v %+v", r, tb.Ligatures)
	}
	if tb.ScanToVK[0x53] != 0x2E|vk.KBDNUMPAD|vk.KBDSPECIAL {
		t.Errorf("scan 53 = %#x (D33: the row gives characters only)", tb.ScanToVK[0x53])
	}
	if r, _ := findRow(d, 0x6E); r.Chars[0] != '.' {
		t.Errorf("DECIMAL row %v", r)
	}
	if r, _ := findRow(d, 0x08); !reflect.DeepEqual(r.Chars, []uint16{8, 8, 0x7F}) {
		t.Errorf("Backspace row (D36 implied) %v", r.Chars)
	}
	strs := map[uint16]string{}
	for _, s := range d.Strings {
		strs[s.ID] = s.Text
	}
	if strs[1000] != "Yakbd VM test layout" || strs[1100] != "English (United States)" || strs[1200] != "en-US" {
		t.Errorf("strings %v", strs)
	}
}

// TestTableAPresent: every Table A scan code the file does not remap is in the DLL with its Table A VK.
func TestTableAPresent(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/reference-spec/*/*.klc")
	for _, p := range files {
		b, _ := os.ReadFile(p)
		f, r := klc.Parse(b, p)
		if r.HasErrors() || strings.Contains(p, "sgcaps-dead") {
			continue
		}
		res := lower(t, b, filepath.Base(p))
		remapped := map[uint16]bool{}
		files := map[byte]bool{}
		for _, k := range f.Keys {
			remapped[k.Scan] = true
			if d, ok := vk.DefaultFor(k.Scan); !ok || byte(d) != k.VK {
				files[k.VK] = true
			}
		}
		got := map[uint16]uint16{}
		for sc, v := range res.DLL.Tables.ScanToVK {
			got[uint16(sc)] = v
		}
		for _, e := range res.DLL.Tables.E0 {
			got[0xE000|uint16(e.Scan)] = e.VK
		}
		for _, e := range res.DLL.Tables.E1 {
			got[0xE100|uint16(e.Scan)] = e.VK
		}
		for _, d := range vk.Defaults {
			if remapped[d.Scan] || files[byte(d.VK)] {
				continue
			}
			if got[d.Scan] != d.VK {
				t.Errorf("%s: %s (%04X) = %#x, want %#x", filepath.Base(p), d.Position, d.Scan, got[d.Scan], d.VK)
			}
		}
	}
}

// TestSupplementaryRecipe: the spec-v9 recipe from a .klc file through the model to all three builds.
func TestSupplementaryRecipe(t *testing.T) {
	src := "KBD\tkbdt\t\"Test\"\nLOCALEID\t\"00000409\"\nSHIFTSTATE\n0\n1\n2\nLAYOUT\n14\tT\t0\t1f600\n29\tOEM_3\t0\te000@\nDEADKEY\te000\nd83d\td83e\nENDKBD\n"
	res := lower(t, []byte(src), "t")
	set := windows.CompileAll(res.DLL, res.Targets)
	if set.Report.HasErrors() || len(set.Builds) != 4 {
		t.Fatal(set.Report.Summary())
	}
	want, _ := windows.SupplementaryEntry(0xE000, 0x1F600, 0x1FA00)
	if len(res.DLL.Tables.DeadKeys) != 1 || res.DLL.Tables.DeadKeys[0] != want {
		t.Errorf("entries %+v, want %+v", res.DLL.Tables.DeadKeys, want)
	}
	exp := string(windows.FormatExpectations("t", res.DLL.Tables.Expectations()))
	if !strings.Contains(exp, "seq c0 54 -> chars d83e de00\n") {
		t.Errorf("recipe expectation missing:\n%s", exp)
	}
}

// TestCorpusBuilds builds every parseable corpus file (with LANGID 0409 where the file has none).
func TestCorpusBuilds(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/reference-spec/*/*.klc")
	n := 0
	for _, p := range files {
		b, _ := os.ReadFile(p)
		f, r := klc.Parse(b, p)
		if r.HasErrors() {
			continue
		}
		l, _ := FromKLC(f, p)
		if l.Targets.Windows.Registration == nil {
			l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
		}
		res, br := build.Lower(l, build.Options{})
		if res == nil {
			if !strings.Contains(strings.Join(br.Codes(), " "), "windows.sgcapsDeadKey") {
				t.Errorf("%s: %s", p, br.Summary())
			}
			continue
		}
		set := windows.CompileAll(res.DLL, res.Targets)
		if set.Report.HasErrors() || len(set.Builds) != 4 {
			t.Errorf("%s: %s", p, set.Report.Summary())
		}
		n++
	}
	if n < 15 {
		t.Errorf("only %d corpus files built", n)
	}
}

// TestW2CapsOnlyDead: an SGCAPS key whose dead key is only in the Caps Lock line is built as main row, Caps Lock
// row with the plain character: hhhh@ in the continuation line is the character, as MSKLC builds it (kbl D81, OBS
// K14); other SGCAPS and dead-key combinations stay windows.sgcapsDeadKey.
func TestW2CapsOnlyDead(t *testing.T) {
	for _, c := range []struct {
		file string
		col  int
	}{{"sgcaps-dead-cont.klc", 0}, {"sgcaps-dead-cont2.klc", 1}} {
		b, _ := os.ReadFile("../../testdata/reference-spec/dll-format/" + c.file)
		res := lower(t, b, c.file)
		var rows []windows.CharRow
		for _, ct := range res.DLL.Tables.CharTables {
			for i, r := range ct.Rows {
				if r.VK == 'Q' && i+1 < len(ct.Rows) && rows == nil {
					rows = ct.Rows[i : i+2]
				}
			}
		}
		if rows == nil {
			t.Fatalf("%s: no Q rows", c.file)
		}
		main, caps := rows[0], rows[1]
		if main.Attributes&windows.SGCAPS == 0 || main.Chars[0] != 'q' || main.Chars[1] != 'Q' ||
			caps.VK != 'Q' || caps.Chars[c.col] != 0x00B4 || caps.Chars[1-c.col] == windows.WCH_DEAD {
			t.Errorf("%s: rows %+v", c.file, rows)
		}
		if r := res.DLL.Tables.Validate(); r.HasErrors() {
			t.Errorf("%s: %s", c.file, r.Summary())
		}
	}
	for _, f := range []string{"sgcaps-dead-main.klc", "sgcaps-dead-shift.klc", "sgcaps-dead-both.klc"} {
		b, _ := os.ReadFile("../../testdata/reference-spec/dll-format/" + f)
		kf, _ := klc.Parse(b, f)
		l, _ := FromKLC(kf, f)
		l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
		if res, r := build.Lower(l, build.Options{}); res != nil || !strings.Contains(r.Summary(), "windows.sgcapsDeadKey") {
			t.Errorf("%s: built", f)
		}
	}
}

// TestD86E0Used: e0-used.klc maps E035 to OEM_2; scan code 35 keeps its Table A VK OEM_2 as well (D86; MSKLC does the
// same, VM run 2 of the MSKLC comparison bundle), and both keys share the file's OEM_2 row.
func TestD86E0Used(t *testing.T) {
	b, _ := os.ReadFile("../../testdata/reference-spec/dll-format/e0-used.klc")
	res := lower(t, b, "e0-used.klc")
	tb := res.DLL.Tables
	if tb.ScanToVK[0x35]&0xFF != 0xBF {
		t.Errorf("scan code 35: %04x, want OEM_2", tb.ScanToVK[0x35])
	}
	ok := false
	for _, e := range tb.E0 {
		ok = ok || e.Scan == 0x35 && e.VK&0xFF == 0xBF
	}
	if !ok {
		t.Error("E035 is not OEM_2")
	}
}
