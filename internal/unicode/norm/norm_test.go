package norm

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var testFile = filepath.Join("..", "..", "..", "testdata", "third_party", "unicode-"+UnicodeVersion, "NormalizationTest.txt")

func parseSeq(t *testing.T, line int, s string) string {
	var b strings.Builder
	for _, f := range strings.Fields(s) {
		v, err := strconv.ParseUint(f, 16, 32)
		if err != nil {
			t.Fatalf("line %d: %q", line, f)
		}
		b.WriteRune(rune(v))
	}
	return b.String()
}

// TestNormalizationTest runs every line of every part of the UCD's NormalizationTest.txt (UAX #15 conformance):
// NFC and NFD of all five columns, and for Part 1 the rule that every other code point is unchanged.
func TestNormalizationTest(t *testing.T) {
	f, err := os.Open(testFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	part := ""
	parts := map[string]int{}
	part1 := map[rune]bool{}
	first := true
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if first {
			if want := "# NormalizationTest-" + UnicodeVersion + ".txt"; line != want {
				t.Fatalf("first line %q, want %q", line, want)
			}
			first = false
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "@") {
			part = strings.Fields(line)[0]
			continue
		}
		f := strings.Split(line, ";")
		if len(f) != 6 {
			t.Fatalf("line %d: %d fields", n, len(f))
		}
		var c [6]string
		for i := 1; i <= 5; i++ {
			c[i] = parseSeq(t, n, f[i-1])
		}
		parts[part]++
		if part == "@Part1" {
			r := []rune(c[1])
			if len(r) != 1 {
				t.Fatalf("line %d: Part 1 column 1 is not one code point", n)
			}
			part1[r[0]] = true
		}
		check := func(name string, got, want string, col int) {
			if got != want {
				t.Errorf("line %d (%s): %s(c%d) = %q, want %q", n, part, name, col, got, want)
			}
		}
		for _, i := range []int{1, 2, 3} {
			check("NFC", NFC(c[i]), c[2], i)
			check("NFD", NFD(c[i]), c[3], i)
		}
		for _, i := range []int{4, 5} {
			check("NFC", NFC(c[i]), c[4], i)
			check("NFD", NFD(c[i]), c[5], i)
		}
		if IsNFC(c[1]) != (c[1] == c[2]) {
			t.Errorf("line %d: IsNFC(c1) = %v", n, IsNFC(c[1]))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"@Part0", "@Part1", "@Part2", "@Part3", "@Part4", "@Part5"} {
		if parts[p] == 0 {
			t.Errorf("%s has no lines", p)
		}
	}
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF || part1[r] {
			continue
		}
		s := string(r)
		if NFC(s) != s || NFD(s) != s {
			t.Errorf("U+%04X is not in Part 1 but changes: NFC %q, NFD %q", r, NFC(s), NFD(s))
		}
	}
}

func TestExamples(t *testing.T) {
	for _, c := range [][3]string{ // input, NFC, NFD as code points
		{"0061 0301", "00E1", "0061 0301"},
		{"00E4 0301", "00E4 0301", "0061 0308 0301"}, // no precomposed form
		{"2126", "03A9", "03A9"},                     // singleton
		{"0958", "0915 093C", "0915 093C"},           // composition exclusion
		{"AC00 11A8", "AC01", "1100 1161 11A8"},      // Hangul LV + T
		{"0061 0323 0302", "1EAD", "0061 0323 0302"}, // composition across a lower class
		{"0061 0302 0323", "1EAD", "0061 0323 0302"}, // canonical order first
		{"0300 0061", "0300 0061", "0300 0061"},      // leading non-starter
		{"0061 0062 0063", "0061 0062 0063", "0061 0062 0063"},
		{"", "", ""},
	} {
		in, nfc, nfd := parseSeq(t, 0, c[0]), parseSeq(t, 0, c[1]), parseSeq(t, 0, c[2])
		if got := NFC(in); got != nfc {
			t.Errorf("NFC(%s) = %+q, want %+q", c[0], got, nfc)
		}
		if got := NFD(in); got != nfd {
			t.Errorf("NFD(%s) = %+q, want %+q", c[0], got, nfd)
		}
	}
}
