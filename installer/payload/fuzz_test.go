package payload

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/yakbd/yakbd-cli/internal/fuzzcheck"
)

// seedPayload is a valid payload of three builds, and its manifest and file bytes on their own.
func seedPayload(f *testing.F) (enc, manifestJSON, blob []byte) {
	data := map[string][]byte{BuildX64: pe(MachineAMD64), BuildWOW64: append(pe(MachineI386), 1, 2, 3), BuildX86: pe(MachineI386)}
	enc, err := Encode(manifest(), []string{BuildX64, BuildWOW64, BuildX86}, data)
	if err != nil {
		f.Fatal(err)
	}
	ml := binary.LittleEndian.Uint32(enc[12:])
	return enc, enc[headerSize : headerSize+int(ml)], enc[headerSize+int(ml):]
}

// FuzzDecode: the payload and manifest reader keeps the bounds of docs/fuzzing.md on any manifest and file bytes.
// The fuzzer cannot forge SHA-256, so each input is decoded twice: as it is, and as the manifest and file bytes
// of a payload whose header (lengths, checksum) is made to match. A payload it decodes encodes again to a payload
// that decodes to the same manifest and files.
func FuzzDecode(f *testing.F) {
	enc, js, blob := seedPayload(f)
	f.Add(enc, []byte(nil))
	f.Add(js, blob)
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
			again, err := Encode(*p.Manifest, p.Manifest.Builds(), p.Data)
			if err != nil {
				return fmt.Errorf("a decoded payload does not encode: %v", err)
			}
			q, err := Decode(again)
			if err != nil {
				return fmt.Errorf("a re-encoded payload does not decode: %v", err)
			}
			if !reflect.DeepEqual(p.Manifest, q.Manifest) || !reflect.DeepEqual(p.Data, q.Data) {
				return fmt.Errorf("decode, encode, decode changes the payload:\n%+v\n%+v", p.Manifest, q.Manifest)
			}
			return nil
		})
	})
}

// FuzzFromExe: finding the payload behind a PE image (what the installer does with its own file) keeps the
// bounds on any bytes.
func FuzzFromExe(f *testing.F) {
	enc, _, _ := seedPayload(f)
	for _, m := range []uint16{MachineI386, MachineAMD64} {
		exe := pe(m)
		binary.LittleEndian.PutUint16(exe[0x46:], 1)       // one section header, at 0x58
		binary.LittleEndian.PutUint32(exe[0x58+16:], 16)   // SizeOfRawData
		binary.LittleEndian.PutUint32(exe[0x58+20:], 0xF0) // PointerToRawData: the image ends at 0x100
		if p, err := FromExe(append(exe, enc...)); err != nil || p == nil {
			f.Fatalf("seed: %v", err)
		}
		f.Add(append(exe, enc...))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fuzzcheck.Check(t, fuzzcheck.Default, len(b), func() error {
			PEMachine(b)
			ImageEnd(b)
			FromExe(b)
			return nil
		})
	})
}
