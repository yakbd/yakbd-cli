package xkb_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yakbd/yakbd-cli/convert/xkbkbl"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
	"github.com/yakbd/yakbd-cli/loss"
)

const corpus = "../../testdata/third_party/xkeyboard-config-2.49/symbols"

// build is the budget of a whole build (docs/fuzzing.md).
var build = fuzzcheck.Bound{Time: 20 * time.Second, Base: 128 << 20, PerByte: 4096}

// FuzzRead: the symbols reader keeps the bounds of docs/fuzzing.md on any bytes; a file it reads is written,
// read again and written again to the same bytes; each section compiles (includes from the corpus) and imports
// as a layout that writes as .kbl.json.
func FuzzRead(f *testing.F) {
	fuzzcheck.Seeds(f, corpus+"/[a-r]*", corpus+"/sun_vndr/*") // the files, not the folder sun_vndr
	files := map[string][]byte{}
	for _, p := range fuzzcheck.Files(f, corpus+"/[a-r]*", corpus+"/s*", corpus+"/sun_vndr/*") {
		if b, err := os.ReadFile(p); err == nil {
			files[filepath.ToSlash(p[len(corpus)+1:])] = b
		}
	}
	res := func(n string) ([]byte, bool) { b, ok := files[n]; return b, ok }
	f.Fuzz(func(t *testing.T, b []byte) {
		var sf *xkb.File
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			var r *loss.Report
			sf, r = xkb.Read(b, "fuzz")
			if sf == nil || r.HasErrors() {
				if !r.HasErrors() {
					return errors.New("refused without an error")
				}
				return nil
			}
			w1 := xkb.Write(sf, xkb.WriteOptions{})
			sf2, r2 := xkb.Read(w1, "again")
			if sf2 == nil {
				return fmt.Errorf("the written file does not read again: %s\n%s", r2.Summary(), w1)
			}
			if w2 := xkb.Write(sf2, xkb.WriteOptions{}); string(w1) != string(w2) {
				return fmt.Errorf("write, read, write is not stable:\n--- first\n%s\n--- second\n%s", w1, w2)
			}
			return nil
		})
		if sf == nil {
			return
		}
		// the includes are read too: the budget of a build
		fuzzcheck.Check(t, build, len(b), func() error {
			inc := xkb.NewIncludes(res)
			for _, s := range sf.Sections {
				km, _ := xkb.Compile(sf, s, "fuzz", inc)
				l, ir := xkbkbl.FromXKB(km, "fuzz", xkbkbl.Options{})
				if ir.HasErrors() {
					return fmt.Errorf("the import reports an error: %s", ir.Summary())
				}
				if l.Check().HasErrors() {
					continue
				}
				if data, jr := kbljson.Write(l); data == nil || jr.HasErrors() {
					return fmt.Errorf("an imported layout does not write as .kbl.json: %s", jr.Summary())
				}
			}
			return nil
		})
	})
}

// FuzzReadCompose: the Compose reader keeps the bounds on any bytes; write, read, write is stable.
func FuzzReadCompose(f *testing.F) {
	fuzzcheck.Seeds(f, "../../testdata/xkb/*.XCompose")
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			c, r := xkb.ReadCompose(b, "fuzz")
			if c == nil {
				if !r.HasErrors() {
					return errors.New("refused without an error")
				}
				return nil
			}
			w1 := xkb.WriteCompose(c, nil)
			c2, r2 := xkb.ReadCompose(w1, "again")
			if c2 == nil || len(c2.Lines) != len(c.Lines) {
				return fmt.Errorf("the written file does not read again the same: %s\n%s", r2.Summary(), w1)
			}
			if w2 := xkb.WriteCompose(c2, nil); string(w1) != string(w2) {
				return fmt.Errorf("write, read, write is not stable:\n%s\n---\n%s", w1, w2)
			}
			return nil
		})
	})
}
