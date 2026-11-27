package wincheck

import (
	"bytes"
	"encoding/binary"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/pe"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

func tablesOf(t *testing.T, path string) *windows.DLL {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, r := klc.Parse(b, path)
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	l, _ := klckbl.FromKLC(f, path)
	if l.Targets.Windows.Registration == nil {
		l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
	}
	res, br := build.Lower(l, build.Options{})
	if res == nil {
		t.Fatal(br.Summary())
	}
	return res.DLL
}

// TestExpectationsPassTheEmulator: for corpus layouts, the expectations parse, and the emulator of the Windows
// rules passes every line, so a failure on Windows means Windows differs from the specification's rules.
func TestExpectationsPassTheEmulator(t *testing.T) {
	for _, p := range []string{"../../testdata/reference-spec/vm/kbdtst.klc", "../../testdata/reference-spec/vm/wbt1.klc",
		"../../testdata/reference-spec/dll-format/sgcaps-plain.klc"} {
		d := tablesOf(t, p)
		text := windows.FormatExpectations("x", d.Tables.Expectations())
		lines, err := ParseExpectations(bytes.NewReader(text))
		if err != nil || len(lines) == 0 {
			t.Fatalf("%s: %v", p, err)
		}
		for i, l := range lines {
			if l.String() != strings.Split(string(text), "\n")[l.No-1] {
				t.Errorf("line %d: %q round-trips as %q", i, strings.Split(string(text), "\n")[l.No-1], l.String())
				break
			}
		}
		if diffs := Check(lines, &Emulator{T: &d.Tables}); len(diffs) > 0 {
			t.Errorf("%s: %d diffs, first %s", p, len(diffs), diffs[0])
		}
	}
}

// TestCheckFindsDifferences: a layout whose tables differ from the expectations fails with readable diffs.
func TestCheckFindsDifferences(t *testing.T) {
	d := tablesOf(t, "../../testdata/reference-spec/vm/kbdtst.klc")
	lines, _ := ParseExpectations(bytes.NewReader(windows.FormatExpectations("x", d.Tables.Expectations())))
	broken := d.Tables
	broken.DeadKeys = nil
	diffs := Check(lines, &Emulator{T: &broken})
	if len(diffs) == 0 {
		t.Fatal("no difference found")
	}
	o := Outcome{Name: "kbdtst", Diffs: diffs, Lines: len(lines)}
	if s := o.Summary(); !strings.HasPrefix(s, "FAIL kbdtst: line ") || strings.Count(s, "line ") > 3 {
		t.Errorf("summary %q", s)
	}
	var short, full bytes.Buffer
	p, f := Report(&short, &full, "64", []Outcome{{Name: "ok", Lines: 3}, o})
	if p != 1 || f != 1 || !strings.Contains(short.String(), "64-bit runner: 1 passed, 1 failed") ||
		strings.Count(full.String(), "\n") < len(diffs) {
		t.Errorf("report %q", short.String())
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"key 51", "key zz -> chars 0041", "seq 51 -> chars 0041", "key 51 -> maybe", "key 51 -> chars xyz"} {
		if _, err := ParseExpectations(strings.NewReader(s)); err == nil {
			t.Errorf("%q parsed", s)
		}
	}
}

func TestManifestAndSlots(t *testing.T) {
	in := []Entry{
		{Name: "a", DLL: map[string]string{"x64": "a/x64/a.dll", "wow64": "a/wow64/a.dll"}, Expect: "a/a.expect.txt", Note: "plain"},
		{Name: "b", DLL: map[string]string{"x86": "b/x86/b.dll"}, Expect: "b/b.expect.txt", Rebase: true},
	}
	var buf bytes.Buffer
	if err := WriteManifest(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadManifest(&buf)
	if err != nil || len(out) != 2 || out[0].DLL["wow64"] != "a/wow64/a.dll" || out[0].DLL["x86"] != "" || !out[1].Rebase || out[0].Note != "plain" {
		t.Errorf("manifest %+v %v", out, err)
	}
	seen := map[string]bool{}
	for _, r := range []string{"64", "32"} {
		for i := 0; i < 200; i++ {
			s := SlotFor(r, i)
			for _, k := range []string{s.File, s.KLID, string(rune(s.LayoutID))} {
				if seen[k] {
					t.Fatalf("slot %s %d repeats %q", r, i, k)
				}
				seen[k] = true
			}
			if len(s.KLID) != 8 || s.LayoutID < 0x100 || s.LayoutID > 0xFFF {
				t.Errorf("slot %+v", s)
			}
		}
	}
}

func TestDescriptorRelocs(t *testing.T) {
	d := tablesOf(t, "../../testdata/reference-spec/vm/kbdtst.klc")
	normal, _ := windows.Compile(d, windows.WOW64)
	variant := withoutStubReloc(t, normal)
	x64, _ := windows.Compile(d, windows.AMD64)
	with, without := DescriptorRelocs(map[string][]byte{"normal.dll": normal, "variant.dll": variant, "x64.dll": x64})
	sort.Strings(with)
	if len(with) != 1 || with[0] != "normal.dll" || len(without) != 1 || without[0] != "variant.dll" {
		t.Errorf("with %v without %v", with, without)
	}
}

// withoutStubReloc returns a copy of a 32-bit DLL whose relocation on the descriptor address is turned into a
// padding entry (the shape VM run 1 showed Windows refuses when the file is rebased).
func withoutStubReloc(t *testing.T, b []byte) []byte {
	t.Helper()
	out := append([]byte(nil), b...)
	f, err := pe.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	_, exps, _ := f.Exports()
	want := exps[0].RVA + 1
	d := f.Dir(pe.DirBaseReloc)
	off, _ := f.Offset(d.RVA)
	for p := uint32(0); p+8 <= d.Size; {
		page := binary.LittleEndian.Uint32(out[off+p:])
		size := binary.LittleEndian.Uint32(out[off+p+4:])
		for q := p + 8; q < p+size; q += 2 {
			e := binary.LittleEndian.Uint16(out[off+q:])
			if page+uint32(e&0xFFF) == want && e>>12 == pe.RelHighLow {
				binary.LittleEndian.PutUint16(out[off+q:], 0)
				return out
			}
		}
		p += size
	}
	t.Fatal("no descriptor relocation found")
	return nil
}

// TestRLMPassesTheEmulator: the right-Shift lines of a KLLF_LRM_RLM layout (RLM alone, LRM when both are down).
func TestRLMPassesTheEmulator(t *testing.T) {
	d := tablesOf(t, "../../testdata/reference-spec/vm/kbdtst.klc")
	d.Tables.LocaleFlags |= windows.KLLF_LRM_RLM
	text := windows.FormatExpectations("x", d.Tables.Expectations())
	lines, err := ParseExpectations(bytes.NewReader(text))
	if err != nil || !strings.Contains(string(text), "key 08+a1 -> chars 200f") {
		t.Fatalf("%v\n%s", err, text)
	}
	if diffs := Check(lines, &Emulator{T: &d.Tables}); len(diffs) > 0 {
		t.Errorf("%d diffs, first %s", len(diffs), diffs[0])
	}
}

// TestARM64Slots: the three runners of an ARM64 guest have distinct slots, and both 64-bit runners there load the
// arm64 column; a manifest of format 1 still reads.
func TestARM64Slots(t *testing.T) {
	seen := map[string]bool{}
	ids := map[uint16]bool{}
	for _, r := range []string{"64", "32", "a64"} {
		for _, i := range []int{0, 7, 255} {
			s := SlotFor(r, i)
			if seen[s.File] || seen[s.KLID] || ids[s.LayoutID] || len(s.KLID) != 8 {
				t.Errorf("%s %d: slot %+v shared or malformed", r, i, s)
			}
			seen[s.File], seen[s.KLID], ids[s.LayoutID] = true, true, true
		}
	}
	if Column("arm64") != "arm64" || Column("x64-on-arm64") != "arm64" || Column("wow64") != "wow64" {
		t.Error("columns")
	}
	old := "# yakbd-wincheck manifest 1: name, x64, wow64, x86, expectations, options, note\na\tx.dll\tw.dll\t-\ta.txt\t-\tnote\n"
	es, err := ReadManifest(strings.NewReader(old))
	if err != nil || len(es) != 1 || es[0].DLL["wow64"] != "w.dll" || es[0].Expect != "a.txt" || es[0].Note != "note" {
		t.Errorf("format 1: %+v %v", es, err)
	}
}

// TestTypedSequences: a "type" line types every press for real and compares the units of all of them together.
// Dead-key sequences of the expectations, rewritten as typed lines, and two plain keys in a row pass the emulator;
// a wrong result fails; the line round-trips through its text.
func TestTypedSequences(t *testing.T) {
	d := tablesOf(t, "../../testdata/reference-spec/vm/kbdtst.klc")
	lines, _ := ParseExpectations(bytes.NewReader(windows.FormatExpectations("x", d.Tables.Expectations())))
	var typed []Line
	var plain []Line
	for _, l := range lines {
		switch {
		case l.Dead != nil && l.Kind == "chars":
			typed = append(typed, Line{Seq: []Press{*l.Dead, l.Key}, Kind: "chars", Units: l.Units})
		case l.Dead == nil && l.Kind == "chars" && len(plain) < 2:
			plain = append(plain, l)
		}
	}
	if len(typed) == 0 || len(plain) < 2 {
		t.Fatalf("corpus: %d dead-key sequences, %d plain keys", len(typed), len(plain))
	}
	two := Line{Seq: []Press{plain[0].Key, plain[1].Key}, Kind: "chars", Units: append(append([]uint16{}, plain[0].Units...), plain[1].Units...)}
	typed = append(typed, two)
	var text strings.Builder
	for _, l := range typed {
		text.WriteString(l.String() + "\n")
	}
	back, err := ParseExpectations(strings.NewReader(text.String()))
	if err != nil || len(back) != len(typed) || back[len(back)-1].String() != two.String() {
		t.Fatalf("round trip: %v %d", err, len(back))
	}
	if diffs := Check(back, &Emulator{T: &d.Tables}); len(diffs) > 0 {
		t.Errorf("%d diffs, first %s", len(diffs), diffs[0])
	}
	wrong := two
	wrong.Units = plain[0].Units
	if diffs := Check([]Line{wrong}, &Emulator{T: &d.Tables}); len(diffs) != 1 {
		t.Errorf("a wrong typed result passed")
	}
	if _, err := ParseExpectations(strings.NewReader("type 51 zz -> chars 0041")); err == nil {
		t.Error("a bad press in a typed line parsed")
	}
}
