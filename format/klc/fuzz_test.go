package klc_test

import (
	"errors"
	"fmt"
	"testing"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// FuzzParse: the .klc reader keeps the bounds of docs/fuzzing.md on any bytes; a file it reads is written, read
// again and written again to the same bytes, and the layout converted from it writes as .kbl.json.
func FuzzParse(f *testing.F) {
	for _, b := range fuzzcheck.Read(f, fuzzcheck.Files(f, "../../testdata/reference-spec/*/*.klc")) {
		f.Add(b)
		f.Add(utf16LE(b)) // MSKLC saves UTF-16LE with a byte-order mark
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			k, r := klc.Parse(b, "fuzz.klc")
			if k == nil || r.HasErrors() {
				if !r.HasErrors() {
					return errors.New("refused without an error")
				}
				return nil
			}
			w1, wr := klc.Write(k, klc.WriteOptions{})
			if wr.HasErrors() {
				return nil // a file the writer cannot express is reported, not a failure
			}
			k2, r2 := klc.Parse(w1, "again.klc")
			if k2 == nil || r2.HasErrors() {
				return fmt.Errorf("the written file does not read again: %s\n%s", r2.Summary(), w1)
			}
			w2, _ := klc.Write(k2, klc.WriteOptions{})
			if string(w1) != string(w2) {
				return fmt.Errorf("write, read, write is not stable:\n--- first\n%s\n--- second\n%s", w1, w2)
			}
			l, cr := klckbl.FromKLC(k, "fuzz.klc")
			if l != nil && !cr.HasErrors() {
				if data, jr := kbljson.Write(l); data == nil || jr.HasErrors() {
					return fmt.Errorf("a converted layout does not write as .kbl.json: %s", jr.Summary())
				}
			}
			return nil
		})
	})
}

// utf16LE re-encodes a UTF-8 seed as UTF-16LE with a byte-order mark; other bytes are returned unchanged.
func utf16LE(b []byte) []byte {
	u := utf16.Encode([]rune(string(b)))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}
