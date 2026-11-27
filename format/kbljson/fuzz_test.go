package kbljson

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// FuzzRead: the .kbl.json validator and reader keep the bounds of docs/fuzzing.md on any bytes. A file the reader
// accepts writes, and the written file validates, reads again and writes the same bytes.
func FuzzRead(f *testing.F) {
	fuzzcheck.Seeds(f, "../../testdata/kbl-spec/examples/*.kbl.json")
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			Validate(b, "fuzz.kbl.json")
			l, r := Read(b, "fuzz.kbl.json")
			if l == nil {
				if !r.HasErrors() {
					return errors.New("refused without an error")
				}
				return nil
			}
			w1, wr := Write(l)
			if w1 == nil || wr.HasErrors() {
				return fmt.Errorf("a file the reader accepts does not write: %s", wr.Summary())
			}
			if vr := Validate(w1, "again.kbl.json"); vr.HasErrors() {
				return fmt.Errorf("the written file does not validate: %s\n%s", vr.Summary(), w1)
			}
			l2, r2 := Read(w1, "again.kbl.json")
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
