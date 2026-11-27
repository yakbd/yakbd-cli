package cldr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

const corpus = "../../testdata/third_party/cldr-*/keyboards/"

// FuzzRead: the CLDR keyboard reader, imports included, keeps the bounds of docs/fuzzing.md on any bytes. A file
// it reads writes as CLDR XML and as .kbl.json, and the written XML reads again and writes the same bytes.
func FuzzRead(f *testing.F) {
	fuzzcheck.Seeds(f, corpus+"3.0/*.xml", "import/*.xml")
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			l, r := Read(b, "fuzz.xml")
			if l == nil {
				if !r.HasErrors() {
					return errors.New("refused without an error")
				}
				return nil
			}
			if data, jr := kbljson.Write(l); data == nil || jr.HasErrors() {
				return fmt.Errorf("a read layout does not write as .kbl.json: %s", jr.Summary())
			}
			w1, wr := Write(l)
			if w1 == nil || wr.HasErrors() {
				return nil // a layout the writer cannot express is reported, not a failure
			}
			l2, r2 := Read(w1, "again.xml")
			if l2 == nil {
				return fmt.Errorf("the written file does not read again: %s\n%s", r2.Summary(), w1)
			}
			w2, _ := Write(l2)
			if string(w1) != string(w2) {
				return fmt.Errorf("write, read, write is not stable:\n--- first\n%s\n--- second\n%s", w1, w2)
			}
			return nil
		})
	})
}

// FuzzReadTests: the CLDR keyboard test reader keeps the bounds on any bytes; every test file it reads writes,
// and the written file reads again and writes the same bytes.
func FuzzReadTests(f *testing.F) {
	fuzzcheck.Seeds(f, corpus+"test/*.xml")
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			tfs, r := ReadTests(b, "fuzz-test.xml")
			if r.HasErrors() {
				return nil
			}
			for _, tf := range tfs {
				w1, wr := WriteTests(tf)
				if w1 == nil || wr.HasErrors() {
					continue
				}
				again, r2 := ReadTests(w1, "again-test.xml")
				if r2.HasErrors() || len(again) != 1 {
					return fmt.Errorf("the written test file does not read again as one file: %s\n%s", r2.Summary(), w1)
				}
				w2, _ := WriteTests(again[0])
				if string(w1) != string(w2) {
					return fmt.Errorf("write, read, write is not stable:\n--- first\n%s\n--- second\n%s", w1, w2)
				}
			}
			return nil
		})
	})
}
