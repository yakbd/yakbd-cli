package toolsetup

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// FuzzDecode: the tool payload reader keeps the bounds of docs/fuzzing.md on any manifest and file bytes, and
// accepts no path that CheckPath refuses and no user data that CheckUserData refuses. As payload.FuzzDecode, each
// input is decoded as it is and as manifest and file bytes behind a matching header; a payload it decodes encodes
// again to the same one.
func FuzzDecode(f *testing.F) {
	m := Manifest{Product: Product{Name: "YAKBD", Code: code}, UserData: editorUserData,
		Shortcuts: []Shortcut{{Name: "YAKBD Editor", Component: Editor, Target: "yakbd-gui.exe"}}}
	enc, err := Encode(m, []ComponentInput{cliComp("0.1.0"), editorComp("0.1.0")})
	if err != nil {
		f.Fatal(err)
	}
	ml := binary.LittleEndian.Uint32(enc[12:])
	f.Add(enc, []byte(nil))
	f.Add(enc[headerSize:headerSize+int(ml)], enc[headerSize+int(ml):])
	f.Fuzz(func(t *testing.T, js, blob []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(js)+len(blob), func() error {
			Decode(js)
			body := append(append([]byte(nil), js...), blob...)
			sum := sha256.Sum256(body)
			b := make([]byte, headerSize, headerSize+len(body))
			copy(b, Magic)
			binary.LittleEndian.PutUint32(b[8:], Version)
			binary.LittleEndian.PutUint32(b[12:], uint32(len(js)))
			binary.LittleEndian.PutUint64(b[16:], uint64(len(blob)))
			copy(b[24:], sum[:])
			p, err := Decode(append(b, body...))
			if err != nil {
				return nil
			}
			for _, c := range p.Manifest.Components {
				for _, fl := range c.Files {
					if CheckPath(fl.Path) != nil {
						return fmt.Errorf("decoded a refused path %q", fl.Path)
					}
				}
			}
			for _, u := range p.Manifest.UserData {
				if CheckUserData(u) != nil {
					return fmt.Errorf("decoded refused user data %+v", u)
				}
			}
			// Inflating is bounded by the claimed sizes; only payloads that claim no more than the budget are
			// inflated (a real installer inflates what it installs, from its own file).
			var total int64
			for _, c := range p.Manifest.Components {
				for _, fl := range c.Files {
					total += fl.Size
				}
			}
			if total > int64(256*(len(js)+len(blob))) {
				return nil
			}
			for _, c := range p.Manifest.Components {
				for _, b := range c.Builds {
					if _, err := p.FilesFor(c.ID, b); err != nil {
						return nil // damaged file bytes: refused at install (Verify)
					}
				}
			}
			var comps []ComponentInput
			for i, c := range p.Manifest.Components {
				ci := ComponentInput{ID: c.ID, Version: c.Version, Builds: c.Builds}
				for j, fl := range c.Files {
					d, err := inflate(p.Packed[i][j], fl.Size)
					if err != nil {
						return nil // damaged file bytes: refused at install
					}
					ci.Files = append(ci.Files, Input{Path: fl.Path, Build: fl.Build, Data: d})
				}
				comps = append(comps, ci)
			}
			again, err := Encode(*p.Manifest, comps)
			if err != nil {
				return fmt.Errorf("a decoded payload does not encode: %v", err)
			}
			q, err := Decode(again)
			if err != nil {
				return fmt.Errorf("a re-encoded payload does not decode: %v", err)
			}
			for i, c := range q.Manifest.Components {
				for _, b := range c.Builds {
					x, _ := p.FilesFor(c.ID, b)
					y, err := q.FilesFor(c.ID, b)
					if err != nil || !reflect.DeepEqual(x, y) || c.Version != p.Manifest.Components[i].Version {
						return fmt.Errorf("decode, encode, decode changes the files of %s for %s", c.ID, b)
					}
				}
			}
			return nil
		})
	})
}
