// Command genucd writes internal/unicode/norm/tables.go, the normalisation tables of the tool, from files of the
// Unicode Character Database (https://www.unicode.org/Public/<version>/ucd/): UnicodeData.txt (canonical
// combining classes and canonical decomposition mappings) and DerivedNormalizationProps.txt
// (Full_Composition_Exclusion). Run it from the repository root with the folder that holds the two files:
//
//	go run ./tools/genucd -ucd /path/to/ucd-18.0.0 -version 18.0.0
//
// The files themselves are not part of the repository; the generated file records their SHA-256.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("genucd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ucd := fs.String("ucd", "", "folder with UnicodeData.txt and DerivedNormalizationProps.txt")
	version := fs.String("version", "", "Unicode version of the files, for example 18.0.0")
	out := fs.String("o", filepath.Join("internal", "unicode", "norm", "tables.go"), "output file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *ucd == "" || *version == "" {
		fmt.Fprintln(stderr, "genucd: -ucd and -version are required")
		return 2
	}
	src, err := generate(*ucd, *version)
	if err != nil {
		fmt.Fprintln(stderr, "genucd:", err)
		return 1
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintln(stderr, "genucd:", err)
		return 1
	}
	return 0
}

type data struct {
	ccc    map[rune]uint8
	decomp map[rune][]rune // one level, canonical mappings only
	fce    map[rune]bool   // Full_Composition_Exclusion
}

func generate(dir, version string) ([]byte, error) {
	d := &data{ccc: map[rune]uint8{}, decomp: map[rune][]rune{}, fce: map[rune]bool{}}
	ud, err := os.ReadFile(filepath.Join(dir, "UnicodeData.txt"))
	if err != nil {
		return nil, err
	}
	dn, err := os.ReadFile(filepath.Join(dir, "DerivedNormalizationProps.txt"))
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(dn[:min(len(dn), 200)], []byte("DerivedNormalizationProps-"+version+".txt")) {
		return nil, fmt.Errorf("DerivedNormalizationProps.txt is not version %s", version)
	}
	if err := d.unicodeData(ud); err != nil {
		return nil, err
	}
	if err := d.exclusions(dn); err != nil {
		return nil, err
	}
	return d.write(version, map[string][]byte{"UnicodeData.txt": ud, "DerivedNormalizationProps.txt": dn})
}

func parseCP(s string) (rune, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 16, 32)
	if err != nil || v > 0x10FFFF {
		return 0, fmt.Errorf("bad code point %q", s)
	}
	return rune(v), nil
}

// unicodeData reads fields 0 (code point), 3 (canonical combining class) and 5 (decomposition mapping).
func (d *data) unicodeData(b []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		f := strings.Split(sc.Text(), ";")
		if len(f) != 15 {
			return fmt.Errorf("UnicodeData.txt line %d: %d fields", n, len(f))
		}
		cp, err := parseCP(f[0])
		if err != nil {
			return fmt.Errorf("UnicodeData.txt line %d: %v", n, err)
		}
		c, err := strconv.ParseUint(f[3], 10, 8)
		if err != nil {
			return fmt.Errorf("UnicodeData.txt line %d: combining class %q", n, f[3])
		}
		if c != 0 {
			if strings.HasSuffix(f[1], ", First>") || strings.HasSuffix(f[1], ", Last>") {
				return fmt.Errorf("UnicodeData.txt line %d: a range with a combining class", n)
			}
			d.ccc[cp] = uint8(c)
		}
		if f[5] == "" || strings.HasPrefix(f[5], "<") {
			continue // none, or a compatibility mapping
		}
		var m []rune
		for _, p := range strings.Fields(f[5]) {
			r, err := parseCP(p)
			if err != nil {
				return fmt.Errorf("UnicodeData.txt line %d: %v", n, err)
			}
			m = append(m, r)
		}
		d.decomp[cp] = m
	}
	return sc.Err()
}

// exclusions reads the Full_Composition_Exclusion lines.
func (d *data) exclusions(b []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		f := strings.Split(line, ";")
		if len(f) < 2 || strings.TrimSpace(f[1]) != "Full_Composition_Exclusion" {
			continue
		}
		lo, hi, ok := strings.Cut(strings.TrimSpace(f[0]), "..")
		if !ok {
			hi = lo
		}
		a, err := parseCP(lo)
		if err != nil {
			return fmt.Errorf("DerivedNormalizationProps.txt line %d: %v", n, err)
		}
		z, err := parseCP(hi)
		if err != nil {
			return fmt.Errorf("DerivedNormalizationProps.txt line %d: %v", n, err)
		}
		for r := a; r <= z; r++ {
			d.fce[r] = true
		}
	}
	if len(d.fce) == 0 {
		return fmt.Errorf("DerivedNormalizationProps.txt has no Full_Composition_Exclusion")
	}
	return sc.Err()
}

// full returns the full canonical decomposition of r (UAX #15 D68), Hangul excluded (it is algorithmic).
func (d *data) full(r rune) []rune {
	m, ok := d.decomp[r]
	if !ok {
		return []rune{r}
	}
	var out []rune
	for _, c := range m {
		out = append(out, d.full(c)...)
	}
	return out
}

func sortedRunes[V any](m map[rune]V) []rune {
	var k []rune
	for r := range m {
		k = append(k, r)
	}
	sort.Slice(k, func(i, j int) bool { return k[i] < k[j] })
	return k
}

func (d *data) write(version string, inputs map[string][]byte) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by tools/genucd from the Unicode %s Character Database. DO NOT EDIT.\n//\n", version)
	fmt.Fprintf(&b, "// Inputs (https://www.unicode.org/Public/%s/ucd/, Unicode License v3, see NOTICE):\n", version)
	for _, name := range []string{"UnicodeData.txt", "DerivedNormalizationProps.txt"} {
		fmt.Fprintf(&b, "//   %s sha256 %x\n", name, sha256.Sum256(inputs[name]))
	}
	fmt.Fprintf(&b, "\npackage norm\n\n// UnicodeVersion is the version of the Unicode Character Database the tables come from.\nconst UnicodeVersion = %q\n\n", version)

	// combining classes as ranges of equal class
	b.WriteString("// cccRanges are the code points with a non-zero canonical combining class, as sorted ranges.\nvar cccRanges = [...]cccRange{\n")
	cps := sortedRunes(d.ccc)
	for i := 0; i < len(cps); {
		j := i
		for j+1 < len(cps) && cps[j+1] == cps[j]+1 && d.ccc[cps[j+1]] == d.ccc[cps[i]] {
			j++
		}
		fmt.Fprintf(&b, "{0x%04X, 0x%04X, %d},\n", cps[i], cps[j], d.ccc[cps[i]])
		i = j + 1
	}
	b.WriteString("}\n\n")

	b.WriteString("// decompositions are the full canonical decompositions (Hangul syllables excluded), sorted by code point.\nvar decompositions = [...]decomposition{\n")
	for _, r := range sortedRunes(d.decomp) {
		fmt.Fprintf(&b, "{0x%04X, %s},\n", r, strconv.QuoteToASCII(string(d.full(r))))
	}
	b.WriteString("}\n\n")

	// primary composites: canonical mappings of two code points that are not fully excluded (UAX #15 D114)
	type pair struct{ a, c, p rune }
	var pairs []pair
	for _, r := range sortedRunes(d.decomp) {
		m := d.decomp[r]
		if len(m) == 2 && !d.fce[r] {
			pairs = append(pairs, pair{m[0], m[1], r})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].a != pairs[j].a {
			return pairs[i].a < pairs[j].a
		}
		return pairs[i].c < pairs[j].c
	})
	b.WriteString("// compositions are the primary composites by their two-code-point canonical mapping, sorted by (first, second).\nvar compositions = [...]composition{\n")
	for _, p := range pairs {
		fmt.Fprintf(&b, "{0x%04X, 0x%04X, 0x%04X},\n", p.a, p.c, p.p)
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}
