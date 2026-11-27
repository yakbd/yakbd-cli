package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerate runs the generator on a small hand-written database in the UCD's file format.
func TestGenerate(t *testing.T) {
	dir := t.TempDir()
	ud := strings.Join([]string{
		"0041;LATIN CAPITAL LETTER A;Lu;0;L;;;;;N;;;;0061;",
		"00C0;LATIN CAPITAL LETTER A WITH GRAVE;Lu;0;L;0041 0300;;;;N;;;;00E0;",
		"01DE;LATIN CAPITAL LETTER A WITH DIAERESIS AND MACRON;Lu;0;L;00C4 0304;;;;N;;;;01DF;",
		"00C4;LATIN CAPITAL LETTER A WITH DIAERESIS;Lu;0;L;0041 0308;;;;N;;;;00E4;",
		"0300;COMBINING GRAVE ACCENT;Mn;230;NSM;;;;;N;;;;;",
		"0301;COMBINING ACUTE ACCENT;Mn;230;NSM;;;;;N;;;;;",
		"0308;COMBINING DIAERESIS;Mn;230;NSM;;;;;N;;;;;",
		"0340;COMBINING GRAVE TONE MARK;Mn;230;NSM;0300;;;;N;;;;;",
		"00BD;VULGAR FRACTION ONE HALF;No;0;ON;<fraction> 0031 2044;;;1/2;N;;;;;",
		"",
	}, "\n")
	dn := "# DerivedNormalizationProps-1.0.0.txt\n0340..0341    ; Full_Composition_Exclusion # Mn\n"
	os.WriteFile(filepath.Join(dir, "UnicodeData.txt"), []byte(ud), 0o644)
	os.WriteFile(filepath.Join(dir, "DerivedNormalizationProps.txt"), []byte(dn), 0o644)
	src, err := generate(dir, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		`const UnicodeVersion = "1.0.0"`,
		"{0x0300, 0x0301, 230}",     // a range of equal class
		`{0x01DE, "A\u0308\u0304"}`, // full decomposition
		"{0x0041, 0x0300, 0x00C0}",  // a primary composite
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "0x00BD") {
		t.Error("a compatibility mapping was taken")
	}
	if _, err := generate(dir, "2.0.0"); err == nil {
		t.Error("a wrong version was accepted")
	}
}
