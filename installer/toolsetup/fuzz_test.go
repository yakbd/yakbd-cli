package toolsetup

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// FuzzDecode: the tool payload reader keeps the bounds of docs/fuzzing.md on any manifest and file bytes, and
// accepts no extra payload path that CheckExtraPath refuses. As payload.FuzzDecode, each input is decoded as it
// is and as manifest and file bytes behind a matching header; a payload it decodes encodes again to the same one.
func FuzzDecode(f *testing.F) {
	data := map[string][]byte{BuildX64: pe(payload.MachineAMD64, "x"), BuildARM64: pe(payload.MachineARM64, "a")}
	enc, err := Encode(Manifest{Product: Product{Name: "yakbd", Version: "0.1.0", Code: code}}, []string{BuildX64, BuildARM64}, data,
		[]Extra{{Path: "yakbd-gui.exe", Data: []byte("g")}, {Path: `qml\a.qml`, Build: BuildARM64, Data: []byte("q")}})
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
			var extra []Extra
			for i, x := range p.Manifest.Extra {
				if CheckExtraPath(x.Path) != nil {
					return fmt.Errorf("decoded a refused path %q", x.Path)
				}
				extra = append(extra, Extra{Path: x.Path, Build: x.Build, Data: p.ExtraData[i]})
			}
			again, err := Encode(*p.Manifest, p.Manifest.Builds(), p.Data, extra)
			if err != nil {
				return fmt.Errorf("a decoded payload does not encode: %v", err)
			}
			q, err := Decode(again)
			if err != nil {
				return fmt.Errorf("a re-encoded payload does not decode: %v", err)
			}
			if !reflect.DeepEqual(p.Manifest.Extra, q.Manifest.Extra) || !reflect.DeepEqual(p.ExtraData, q.ExtraData) || !reflect.DeepEqual(p.Data, q.Data) {
				return fmt.Errorf("decode, encode, decode changes the payload:\n%+v\n%+v", p.Manifest, q.Manifest)
			}
			return nil
		})
	})
}
