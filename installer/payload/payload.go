// Package payload is the data an installer carries after its program: a manifest and the layout files
// (docs/installer.md §5). It is shared by the payload writer (package installer) and the Windows program that
// reads it (installer/stub), and so imports nothing but the standard library: a change here changes the stub.
package payload

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Magic starts a payload.
const Magic = "YAKBDPAY"

// Version is the payload format version this package writes and reads.
const Version = 1

const headerSize = 56

// Build names of the files.
const (
	BuildX86    = "x86"
	BuildX64    = "x64"
	BuildWOW64  = "wow64"
	BuildARM64X = "arm64x"
)

// PE machine values (inst §2.1).
const (
	MachineI386  uint16 = 0x014C
	MachineAMD64 uint16 = 0x8664
	MachineARM64 uint16 = 0xAA64
)

// MachineOf is the PE Machine value each build must have.
var MachineOf = map[string]uint16{BuildX86: MachineI386, BuildX64: MachineAMD64, BuildWOW64: MachineI386, BuildARM64X: MachineARM64}

// Manifest describes an installer's content.
type Manifest struct {
	Format  int     `json:"format"`
	Tool    string  `json:"tool"`
	Product Product `json:"product"`
	Layout  Layout  `json:"layout"`
	// Enable adds the layout to the input list of the user who runs the installer (inst §4.3).
	Enable bool `json:"enable"`
	// SignIn also puts the layout on the sign-in screen and into the Default profile (rule
	// enableForSignInAndNewUsers, kbl targets/windows-install.md §7.1, D87).
	SignIn bool   `json:"signIn,omitempty"`
	Files  []File `json:"files"`
}

// Product is the uninstall entry's data and the ownership marker.
type Product struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher,omitempty"`
	// Code is the product code GUID, "{XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}" in upper case: the value of
	// "Layout Product Code" (inst §3.2, §10) and the name of the uninstall key.
	Code string `json:"code"`
}

// Layout is the registration data (inst §3.2).
type Layout struct {
	File       string `json:"file"`
	LangID     string `json:"langId"`
	LayoutText string `json:"layoutText"`
	// KLID and LayoutID are set when the file pins them; empty means "allocate" (inst §3.1, §3.3).
	KLID               string `json:"klid,omitempty"`
	LayoutID           string `json:"layoutId,omitempty"`
	LocaleName         string `json:"localeName,omitempty"`
	CustomLanguageName string `json:"customLanguageName,omitempty"`
}

// File is one layout file of the payload.
type File struct {
	Build   string `json:"build"`
	Machine uint16 `json:"machine"`
	Offset  int64  `json:"offset"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// Payload is a decoded payload: the manifest and the bytes of each file, by build.
type Payload struct {
	Manifest *Manifest
	Data     map[string][]byte
}

// Encode writes a payload for m and the file contents by build name. The Files list of m is filled in (offset,
// size, hash, machine) in the order of builds; the output is deterministic.
func Encode(m Manifest, builds []string, data map[string][]byte) ([]byte, error) {
	m.Format = Version
	m.Files = nil
	var blob bytes.Buffer
	for _, b := range builds {
		d, ok := data[b]
		if !ok {
			return nil, fmt.Errorf("payload: no data for build %s", b)
		}
		mach, ok := MachineOf[b]
		if !ok {
			return nil, fmt.Errorf("payload: unknown build %q", b)
		}
		sum := sha256.Sum256(d)
		m.Files = append(m.Files, File{Build: b, Machine: mach, Offset: int64(blob.Len()), Size: int64(len(d)), SHA256: hex.EncodeToString(sum[:])})
		blob.Write(d)
	}
	if err := m.Check(); err != nil {
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

// Decode reads a payload at the start of b (bytes after it, such as a signature, are ignored). Every file's hash
// and PE Machine are checked.
func Decode(b []byte) (*Payload, error) {
	if len(b) < headerSize || string(b[:8]) != Magic {
		return nil, errors.New("no installer payload found (the file is damaged or not a yakbd installer)")
	}
	if v := binary.LittleEndian.Uint32(b[8:]); v != Version {
		return nil, fmt.Errorf("payload format %d; this program reads format %d", v, Version)
	}
	ml := uint64(binary.LittleEndian.Uint32(b[12:]))
	dl := binary.LittleEndian.Uint64(b[16:])
	if ml+dl < ml || uint64(len(b)-headerSize) < ml+dl {
		return nil, errors.New("payload is truncated")
	}
	body := b[headerSize : headerSize+ml+dl]
	if sum := sha256.Sum256(body); !bytes.Equal(sum[:], b[24:56]) {
		return nil, errors.New("payload checksum does not match (the file is damaged)")
	}
	m := &Manifest{}
	dec := json.NewDecoder(bytes.NewReader(body[:ml]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return nil, fmt.Errorf("payload manifest: %v", err)
	}
	if err := m.Check(); err != nil {
		return nil, err
	}
	data := body[ml:]
	p := &Payload{Manifest: m, Data: map[string][]byte{}}
	for _, f := range m.Files {
		if f.Offset < 0 || f.Size < 0 || f.Offset+f.Size > int64(len(data)) || f.Offset+f.Size < f.Offset {
			return nil, fmt.Errorf("payload file %s is outside the payload", f.Build)
		}
		d := data[f.Offset : f.Offset+f.Size]
		sum := sha256.Sum256(d)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return nil, fmt.Errorf("payload file %s: checksum does not match", f.Build)
		}
		if mach, err := PEMachine(d); err != nil || mach != f.Machine || mach != MachineOf[f.Build] {
			return nil, fmt.Errorf("payload file %s is not a %s file", f.Build, f.Build)
		}
		p.Data[f.Build] = d
	}
	return p, nil
}

// Check checks the manifest's own fields.
func (m *Manifest) Check() error {
	if m.Format != Version {
		return fmt.Errorf("manifest format %d, want %d", m.Format, Version)
	}
	if !IsGUID(m.Product.Code) {
		return fmt.Errorf("product code %q is not an upper-case {GUID}", m.Product.Code)
	}
	if !ValidFileName(m.Layout.File) {
		return fmt.Errorf("layout file name %q: want letters, digits and _ before .dll (inst §2.3)", m.Layout.File)
	}
	if !isHex(m.Layout.LangID, 4) {
		return fmt.Errorf("LANGID %q is not 4 hex digits", m.Layout.LangID)
	}
	if m.Layout.KLID != "" && !isHex(m.Layout.KLID, 8) {
		return fmt.Errorf("KLID %q is not 8 hex digits", m.Layout.KLID)
	}
	if m.Layout.LayoutID != "" && !isHex(m.Layout.LayoutID, 4) {
		return fmt.Errorf("Layout Id %q is not 4 hex digits", m.Layout.LayoutID)
	}
	if m.Layout.LayoutText == "" || m.Product.Name == "" {
		return errors.New("layout text and product name must not be empty")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if _, ok := MachineOf[f.Build]; !ok || seen[f.Build] {
			return fmt.Errorf("payload build %q unknown or listed twice", f.Build)
		}
		seen[f.Build] = true
	}
	return nil
}

// Has reports whether the manifest carries a build.
func (m *Manifest) Has(build string) bool {
	for _, f := range m.Files {
		if f.Build == build {
			return true
		}
	}
	return false
}

// Builds lists the builds the manifest carries.
func (m *Manifest) Builds() []string {
	var out []string
	for _, f := range m.Files {
		out = append(out, f.Build)
	}
	return out
}

// ValidFileName reports whether s is a layout file name the installer accepts: [A-Za-z][A-Za-z0-9_]* + ".dll",
// at most 218 characters in all (inst §2.3).
func ValidFileName(s string) bool {
	base, ok := strings.CutSuffix(strings.ToLower(s), ".dll")
	if !ok || base == "" || len(s) > 218 || !(base[0] >= 'a' && base[0] <= 'z') {
		return false
	}
	for _, c := range base {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// IsGUID reports whether s is "{8-4-4-4-12}" upper-case hex.
func IsGUID(s string) bool {
	if len(s) != 38 || s[0] != '{' || s[37] != '}' {
		return false
	}
	for i, c := range s[1:37] {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// PEMachine returns the Machine field of a PE image.
func PEMachine(b []byte) (uint16, error) {
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return 0, errors.New("not a PE file")
	}
	off := int(binary.LittleEndian.Uint32(b[0x3C:]))
	if off < 0 || off+6 > len(b) || string(b[off:off+4]) != "PE\x00\x00" {
		return 0, errors.New("not a PE file")
	}
	return binary.LittleEndian.Uint16(b[off+4:]), nil
}

// ImageEnd returns the end of a PE image's last section in the file: where an installer's payload starts
// (docs/installer.md §5).
func ImageEnd(b []byte) (int64, error) {
	if _, err := PEMachine(b); err != nil {
		return 0, err
	}
	off := int(binary.LittleEndian.Uint32(b[0x3C:]))
	if off+24 > len(b) {
		return 0, errors.New("PE header truncated")
	}
	n := int(binary.LittleEndian.Uint16(b[off+6:]))
	optSize := int(binary.LittleEndian.Uint16(b[off+20:]))
	sec := off + 24 + optSize
	if sec+40*n > len(b) {
		return 0, errors.New("PE section table truncated")
	}
	end := int64(sec + 40*n)
	for i := 0; i < n; i++ {
		s := b[sec+40*i:]
		size := int64(binary.LittleEndian.Uint32(s[16:]))
		ptr := int64(binary.LittleEndian.Uint32(s[20:]))
		if size > 0 && ptr+size > end {
			end = ptr + size
		}
	}
	if end > int64(len(b)) {
		return 0, errors.New("PE section outside the file")
	}
	return end, nil
}

// FromExe finds and decodes the payload of an installer file.
func FromExe(b []byte) (*Payload, error) {
	end, err := ImageEnd(b)
	if err != nil {
		return nil, err
	}
	return Decode(b[end:])
}
