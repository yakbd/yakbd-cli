// Package toolsetup is the logic of yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12): its
// payload (yakbd.exe for x86, x64 and arm64), and install, upgrade, repair and uninstall per machine into Program
// Files with the folder on the machine PATH and an uninstall entry. It talks to Windows only through System
// (implemented by installer/winhost), so that every decision is tested on any OS. Standard library and the
// installer packages it shares with the layout installers only: it is compiled into installer/toolstub.
package toolsetup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// Magic starts a tool payload; the layout installers' payload has payload.Magic.
const Magic = "YAKBDTOL"

// Version is the tool payload format version.
const Version = 1

const headerSize = 56

// Builds of yakbd.exe and their PE Machine values.
const (
	BuildX86   = "x86"
	BuildX64   = "x64"
	BuildARM64 = "arm64"
)

// MachineOf is the PE Machine of each build.
var MachineOf = map[string]uint16{BuildX86: payload.MachineI386, BuildX64: payload.MachineAMD64, BuildARM64: payload.MachineARM64}

// BuildFor is the build a native machine gets; no fallback to another build.
var BuildFor = map[uint16]string{payload.MachineI386: BuildX86, payload.MachineAMD64: BuildX64, payload.MachineARM64: BuildARM64}

// Manifest describes a tool installer's content.
type Manifest struct {
	Format  int     `json:"format"`
	Product Product `json:"product"`
	Files   []File  `json:"files"`
}

// Product is the uninstall entry's data.
type Product struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
	Code      string `json:"code"`
}

// File is one build of yakbd.exe.
type File struct {
	Build   string `json:"build"`
	Machine uint16 `json:"machine"`
	Offset  int64  `json:"offset"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// Payload is a decoded tool payload.
type Payload struct {
	Manifest *Manifest
	Data     map[string][]byte
}

// Encode writes the payload; builds gives the order (deterministic output).
func Encode(m Manifest, builds []string, data map[string][]byte) ([]byte, error) {
	m.Format, m.Files = Version, nil
	var blob bytes.Buffer
	for _, b := range builds {
		d, ok := data[b]
		mach, known := MachineOf[b]
		if !ok || !known {
			return nil, fmt.Errorf("tool payload: build %q unknown or without data", b)
		}
		if got, err := payload.PEMachine(d); err != nil || got != mach {
			return nil, fmt.Errorf("tool payload: the %s file is not a PE file of machine %04X", b, mach)
		}
		sum := sha256.Sum256(d)
		m.Files = append(m.Files, File{Build: b, Machine: mach, Offset: int64(blob.Len()), Size: int64(len(d)), SHA256: hex.EncodeToString(sum[:])})
		blob.Write(d)
	}
	if err := m.check(); err != nil {
		return nil, err
	}
	js, err := json.MarshalIndent(&m, "", " ")
	if err != nil {
		return nil, err
	}
	body := append(js, blob.Bytes()...)
	sum := sha256.Sum256(body)
	out := make([]byte, headerSize, headerSize+len(body))
	copy(out, Magic)
	binary.LittleEndian.PutUint32(out[8:], Version)
	binary.LittleEndian.PutUint32(out[12:], uint32(len(js)))
	binary.LittleEndian.PutUint64(out[16:], uint64(blob.Len()))
	copy(out[24:], sum[:])
	return append(out, body...), nil
}

func (m *Manifest) check() error {
	if m.Format != Version {
		return fmt.Errorf("tool manifest format %d, want %d", m.Format, Version)
	}
	if !payload.IsGUID(m.Product.Code) || m.Product.Name == "" || m.Product.Version == "" {
		return errors.New("tool manifest: product code, name or version missing")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if _, ok := MachineOf[f.Build]; !ok || seen[f.Build] {
			return fmt.Errorf("tool payload build %q unknown or listed twice", f.Build)
		}
		seen[f.Build] = true
	}
	return nil
}

// Decode reads a tool payload at the start of b, checking every hash and PE Machine.
func Decode(b []byte) (*Payload, error) {
	if len(b) < headerSize || string(b[:8]) != Magic {
		return nil, errors.New("no yakbd tool payload found (the file is damaged or not yakbd-setup.exe)")
	}
	if v := binary.LittleEndian.Uint32(b[8:]); v != Version {
		return nil, fmt.Errorf("tool payload format %d; this program reads format %d", v, Version)
	}
	ml := uint64(binary.LittleEndian.Uint32(b[12:]))
	dl := binary.LittleEndian.Uint64(b[16:])
	if ml+dl < ml || uint64(len(b)-headerSize) < ml+dl {
		return nil, errors.New("tool payload is truncated")
	}
	body := b[headerSize : headerSize+ml+dl]
	if sum := sha256.Sum256(body); !bytes.Equal(sum[:], b[24:56]) {
		return nil, errors.New("tool payload checksum does not match (the file is damaged)")
	}
	m := &Manifest{}
	dec := json.NewDecoder(bytes.NewReader(body[:ml]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return nil, fmt.Errorf("tool payload manifest: %v", err)
	}
	if err := m.check(); err != nil {
		return nil, err
	}
	data := body[ml:]
	p := &Payload{Manifest: m, Data: map[string][]byte{}}
	for _, f := range m.Files {
		if f.Offset < 0 || f.Size < 0 || f.Offset+f.Size > int64(len(data)) || f.Offset+f.Size < f.Offset {
			return nil, fmt.Errorf("tool payload file %s is outside the payload", f.Build)
		}
		d := data[f.Offset : f.Offset+f.Size]
		sum := sha256.Sum256(d)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return nil, fmt.Errorf("tool payload file %s: checksum does not match", f.Build)
		}
		if mach, err := payload.PEMachine(d); err != nil || mach != f.Machine || mach != MachineOf[f.Build] {
			return nil, fmt.Errorf("tool payload file %s is not a %s file", f.Build, f.Build)
		}
		p.Data[f.Build] = d
	}
	return p, nil
}

// FromExe finds and decodes the payload after the PE image of an installer file.
func FromExe(b []byte) (*Payload, error) {
	end, err := payload.ImageEnd(b)
	if err != nil {
		return nil, err
	}
	return Decode(b[end:])
}

// Builds lists the builds the manifest carries.
func (m *Manifest) Builds() []string {
	var out []string
	for _, f := range m.Files {
		out = append(out, f.Build)
	}
	return out
}
