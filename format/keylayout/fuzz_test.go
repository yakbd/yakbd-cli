package keylayout_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yakbd/yakbd-cli/convert/keylayoutkbl"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
	"github.com/yakbd/yakbd-cli/target/macos/build"
)

// FuzzRead: the .keylayout reader keeps the bounds of docs/fuzzing.md on any bytes; a file it reads is written,
// read again and written again to the same bytes; it imports as a layout that writes as .kbl.json and builds
// for macOS again without a panic.
func FuzzRead(f *testing.F) {
	fuzzcheck.Seeds(f, "../../tools/macos-check/cases/*/*.bundle/Contents/Resources/*.keylayout",
		"../../testdata/third_party/keylayout/*/*.keylayout")
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			k, r := keylayout.Read(b, "fuzz.keylayout")
			if k == nil || r.HasErrors() {
				if !r.HasErrors() {
					return errors.New("refused without an error")
				}
				return nil
			}
			w1 := keylayout.Write(k)
			k2, r2 := keylayout.Read(w1, "again.keylayout")
			if k2 == nil || r2.HasErrors() {
				return fmt.Errorf("the written file does not read again: %s\n%s", r2.Summary(), w1)
			}
			if w2 := keylayout.Write(k2); string(w1) != string(w2) {
				return fmt.Errorf("write, read, write is not stable:\n--- first\n%s\n--- second\n%s", w1, w2)
			}
			l, ir := keylayoutkbl.FromKeylayout(k, "fuzz.keylayout", "")
			if ir.HasErrors() {
				return fmt.Errorf("the import reports an error: %s", ir.Summary())
			}
			if l.Check().HasErrors() {
				return nil // a file the model refuses (for example an id it cannot hold) is reported, not a failure
			}
			if data, jr := kbljson.Write(l); data == nil || jr.HasErrors() {
				return fmt.Errorf("an imported layout does not write as .kbl.json: %s", jr.Summary())
			}
			build.Build(l, build.Options{})
			return nil
		})
	})
}
