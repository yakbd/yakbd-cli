package xkb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const corpus = "../../testdata/third_party/xkeyboard-config-2.49/symbols"

func TestKeysyms(t *testing.T) {
	for _, c := range []struct {
		name string
		k    Keysym
		r    rune // 0: types nothing
		out  string
	}{
		{"a", 'a', 'a', "a"},
		{"1", '1', '1', "1"},
		{"aacute", 0xe1, 'á', "aacute"},
		{"EuroSign", 0x20ac, '€', "EuroSign"},
		{"U20AC", 0x10020ac, '€', "U20AC"},
		{"U45F", 0x100045f, 'џ', "U045F"},
		{"U00E9", 0xe9, 'é', "eacute"},
		{"0x1002640", 0x1002640, '♀', "U2640"},
		{"femalesymbol", 0xaf8, '♀', "femalesymbol"},
		{"quoteright", 0x27, '\'', "apostrophe"},
		{"dead_acute", 0xfe51, 0, "dead_acute"},
		{"dead_perispomeni", 0xfe53, 0, "dead_tilde"},
		{"KP_Space", 0xff80, ' ', "KP_Space"},
		{"NoSymbol", 0, 0, "NoSymbol"},
		{"VoidSymbol", 0xffffff, 0, "VoidSymbol"},
	} {
		k, ok := KeysymFromName(c.name)
		if !ok || k != c.k {
			t.Errorf("%s: got %#x %v, want %#x", c.name, k, ok, c.k)
			continue
		}
		r, ok := k.Rune()
		if (c.r == 0) == ok || (ok && r != c.r) {
			t.Errorf("%s: rune %q %v, want %q", c.name, r, ok, c.r)
		}
		if k.Name() != c.out {
			t.Errorf("%s: name %s, want %s", c.name, k.Name(), c.out)
		}
		if back, _ := KeysymFromName(k.Name()); back != k {
			t.Errorf("%s: name %s reads as %#x", c.name, k.Name(), back)
		}
	}
	if _, ok := KeysymFromName("XF86AudioMute"); ok {
		t.Error("XF86 names are not in the table")
	}
	if FromRune('€') != 0x20ac || FromRune('ǘ') != 0x10001d8 || FromRune('é') != 0xe9 {
		t.Error("FromRune")
	}
	if Keysym('ß').Upper() != 'ß' || Keysym('a').Upper() != 'A' || Keysym('1').Upper() != '1' {
		t.Error("Upper")
	}
}

func TestTypes(t *testing.T) {
	ls := func(s ...string) []Level {
		var out []Level
		for _, n := range s {
			k, _ := KeysymFromName(n)
			out = append(out, Level{{K: k}})
		}
		return out
	}
	typed := func(tn string, l []Level, m Mods) string {
		s, _ := StockType(tn).Type(l, m).Text()
		return s
	}
	// FOUR_LEVEL_SEMIALPHABETIC: Caps Lock shifts levels 1-2; on level 3 Lock is preserved, so the
	// transformation capitalises (the stock types cannot say "Caps Lock does not touch level 3")
	thorn := ls("t", "T", "thorn", "THORN")
	for _, c := range []struct {
		m    Mods
		want string
	}{{0, "t"}, {Shift, "T"}, {Lock, "T"}, {Shift | Lock, "t"}, {LevelThree, "þ"}, {Lock | LevelThree, "Þ"},
		{Shift | Lock | LevelThree, "Þ"}} {
		if got := typed("FOUR_LEVEL_SEMIALPHABETIC", thorn, c.m); got != c.want {
			t.Errorf("semi %v: %q, want %q", c.m, got, c.want)
		}
	}
	if got := typed("FOUR_LEVEL_ALPHABETIC", thorn, Lock|LevelThree); got != "Þ" {
		t.Errorf("alpha: %q", got)
	}
	if got := typed("FOUR_LEVEL_ALPHABETIC", thorn, Shift|Lock|LevelThree); got != "þ" {
		t.Errorf("alpha shift: %q", got)
	}
	// FOUR_LEVEL does not read Lock: the transformation capitalises every level
	if got := typed("FOUR_LEVEL", ls("oe", "OE", "ae", "AE"), Lock|LevelThree); got != "Æ" {
		t.Errorf("four: %q", got)
	}
	if got := typed("EIGHT_LEVEL_SEMIALPHABETIC", ls("a", "A", "b", "B", "c", "C", "d", "D"), Lock|LevelFive); got != "C" {
		t.Errorf("eight semi: %q", got)
	}
	for _, c := range []struct {
		l    []Level
		want string
	}{
		{ls("a"), "ONE_LEVEL"}, {ls("a", "A"), "ALPHABETIC"}, {ls("1", "exclam"), "TWO_LEVEL"},
		{ls("KP_1", "KP_End"), "KEYPAD"}, {ls("a", "A", "ae", "AE"), "FOUR_LEVEL_ALPHABETIC"},
		{ls("a", "A", "at"), "FOUR_LEVEL_SEMIALPHABETIC"}, {ls("1", "exclam", "onesuperior"), "FOUR_LEVEL"},
		{ls("a", "A", "b", "B", "c"), ""},
	} {
		if got := AutomaticType(c.l); got != c.want {
			t.Errorf("automatic type %v: %s, want %s", c.l, got, c.want)
		}
	}
}

func resolver(dir string) Resolver {
	return func(n string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n)))
		return b, err == nil
	}
}

func corpusFiles(t *testing.T) []string {
	var files []string
	filepath.Walk(corpus, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) < 20 {
		t.Fatalf("the corpus %s has %d files", corpus, len(files))
	}
	return files
}

// TestCorpus: every file of the xkeyboard-config subset reads, writes stably in both spellings, and every
// section compiles with all its includes found.
func TestCorpus(t *testing.T) {
	inc := NewIncludes(resolver(corpus))
	for _, p := range corpusFiles(t) {
		b, _ := os.ReadFile(p)
		f, r := Read(b, p)
		if f == nil {
			t.Errorf("%s: %s", p, r.Summary())
			continue
		}
		w1 := Write(f, WriteOptions{Header: []string{"test"}})
		f2, r2 := Read(w1, "w1")
		if f2 == nil {
			t.Errorf("%s: written form does not read: %s", p, r2.Summary())
			continue
		}
		if w2 := Write(f2, WriteOptions{Header: []string{"test"}}); string(w2) != string(w1) {
			t.Errorf("%s: write, read, write is not stable", p)
		}
		f3, _ := Read(Write(f, WriteOptions{UTF8: true}), "w3")
		if f3 == nil || string(Write(f3, WriteOptions{Header: []string{"test"}})) != string(w1) {
			t.Errorf("%s: the UTF-8 spelling reads as other keysyms", p)
		}
		for _, s := range f.Sections {
			km, cr := Compile(f, s, p, inc)
			for _, e := range cr.Entries {
				t.Errorf("%s(%s): %s", p, s.Name, e.String())
			}
			if len(km.Keys) == 0 && !strings.Contains(p, "srvr_ctrl") {
				t.Logf("%s(%s): no keys", p, s.Name)
			}
		}
	}
}

func TestMerge(t *testing.T) {
	files := map[string]string{
		"base": `default xkb_symbols "basic" { key <AE01> { [ 1, exclam, onesuperior, exclamdown ] }; key <AD01> { type[Group1] = "FOUR_LEVEL", [ q, Q ] }; };`,
		"top":  `xkb_symbols "x" { include "base" key <AE01> { [ NoSymbol, at ] }; augment key <AD01> { [ w, W, at ] }; replace key <AC01> { [ a ] }; };`,
	}
	res := func(n string) ([]byte, bool) { s, ok := files[n]; return []byte(s), ok }
	f, _ := Read([]byte(files["top"]), "top")
	km, r := Compile(f, f.Sections[0], "top", NewIncludes(res))
	if r.HasErrors() || len(r.Entries) > 0 {
		t.Fatal(r.Summary())
	}
	got := []string{km.Keys["AE01"].Groups[0].String(), km.Keys["AD01"].Groups[0].String(), km.Keys["AC01"].Groups[0].String()}
	want := []string{"FOUR_LEVEL [1, at, onesuperior, exclamdown]", "FOUR_LEVEL [q, Q, at]", "ONE_LEVEL [a]"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%d: %s, want %s", i, got[i], want[i])
		}
	}
	_, r = Compile(f, f.Sections[0], "top", nil)
	if len(r.Entries) != 1 || r.Entries[0].Code != "xkb.includeUnresolved" {
		t.Errorf("missing include: %s", r.Summary())
	}
}

func TestSyntaxErrors(t *testing.T) {
	for _, src := range []string{
		``, `xkb_symbols "a" {`, `xkb_symbols "a" { key <AE01> { [ a, ] }; };`, `xkb_symbols "a" { key AE01 { [ a ] }; };`,
		`xkb_symbols "a" { name[Group9] = "x"; };`, "xkb_symbols \"a\" { name[Group1] = \"\\777\"; };", `foo`,
	} {
		f, r := Read([]byte(src), "t")
		if f != nil || !r.HasErrors() {
			t.Errorf("%q: read without an error", src)
		}
	}
}

func TestCompose(t *testing.T) {
	b, err := os.ReadFile("../../testdata/xkb/test.XCompose")
	if err != nil {
		t.Fatal(err)
	}
	c, r := ReadCompose(b, "test.XCompose")
	if c == nil || r.HasErrors() || len(r.Entries) > 0 {
		t.Fatalf("%s", r.Summary())
	}
	if c.Lines[0].Include != "%L" || len(c.Lines) != 29 {
		t.Errorf("lines: %d, first %+v", len(c.Lines), c.Lines[0])
	}
	w1 := WriteCompose(c, []string{"h"})
	c2, _ := ReadCompose(w1, "w")
	if string(WriteCompose(c2, []string{"h"})) != string(w1) {
		t.Error("write, read, write is not stable")
	}
	if !strings.Contains(string(w1), "<dead_acute> <dead_diaeresis> <u> : \"ǘ\"\n") {
		t.Errorf("written form:\n%s", w1)
	}
	_, r = ReadCompose([]byte("<dead_acute> <nokeysym> : \"x\"\n!Ctrl <a> : \"y\"\n<a> : \"\\x41\\101\\\"\"\n"), "s")
	if len(r.Entries) != 2 || r.HasErrors() {
		t.Errorf("skipped lines: %s", r.Summary())
	}
}
