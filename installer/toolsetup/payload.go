// Package toolsetup is the logic of yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12): its
// payload (yakbd.exe for x86, x64 and arm64, and an optional extra payload of more files for the same folder,
// §12.1), and install, upgrade, repair and uninstall per machine into Program Files with the folder on the machine
// PATH and an uninstall entry. It talks to Windows only through System (implemented by installer/winhost), so that
// every decision is tested on any OS. Standard library and the installer packages it shares with the layout
// installers only: it is compiled into installer/toolstub.
package toolsetup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	// Extra is the extra payload (docs/installer-cli.md §12.1): more files installed into the folder of yakbd.exe,
	// such as a graphical program. Left out when empty, so that a payload without it keeps its earlier bytes.
	Extra []ExtraFile `json:"extra,omitempty"`
	// Shortcuts are Start-menu shortcuts to files of the extra payload (docs/installer-cli.md §12.2). Left out when
	// empty, like Extra.
	Shortcuts []Shortcut `json:"shortcuts,omitempty"`
}

// Shortcut is one Start-menu shortcut: the file Name + ".lnk" in the common Start-menu Programs folder, pointing at
// Target, a path of the extra payload. A machine gets it only when its build carries Target.
type Shortcut struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// ExtraFile is one file of the extra payload.
type ExtraFile struct {
	// Path is relative to the install folder, with backslashes (`qml\a.dll`); CheckExtraPath has the rules.
	Path string `json:"path"`
	// Build limits the file to the machines of one build of yakbd.exe; empty: every build.
	Build  string `json:"build,omitempty"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Extra is one file for Encode's extra payload.
type Extra struct {
	Path  string
	Build string // empty: every build
	Data  []byte
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
	// ExtraData holds the bytes of Manifest.Extra[i] at index i.
	ExtraData [][]byte
}

// Encode writes the payload; builds and extra give the order (deterministic output). extra may be empty.
func Encode(m Manifest, builds []string, data map[string][]byte, extra []Extra) ([]byte, error) {
	m.Format, m.Files, m.Extra = Version, nil, nil
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
	for _, x := range extra {
		sum := sha256.Sum256(x.Data)
		m.Extra = append(m.Extra, ExtraFile{Path: x.Path, Build: x.Build, Offset: int64(blob.Len()), Size: int64(len(x.Data)), SHA256: hex.EncodeToString(sum[:])})
		blob.Write(x.Data)
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
	if err := checkExtra(m.Extra, seen); err != nil {
		return err
	}
	return checkShortcuts(m.Shortcuts, m.Extra)
}

// reservedSuffixes are the endings of the installer's temporary names (Install, Uninstall).
var reservedSuffixes = []string{".yakbd-tmp", ".yakbd-new"}

// CheckExtraPath checks one path of the extra payload: relative, with backslashes, every part a plain name
// (not empty, "." or "..", no control character or any of <>:"/|?*, not ending in a space or a dot), not
// yakbd.exe or setup.exe at the top, and not ending in a name the installer uses for its temporary files.
func CheckExtraPath(p string) error {
	if p == "" {
		return errors.New("an extra payload path is empty")
	}
	for _, part := range strings.Split(p, `\`) {
		if err := checkPart(part); err != nil {
			return fmt.Errorf("extra payload path %q: %v", p, err)
		}
	}
	l := strings.ToLower(p)
	if top, _, _ := strings.Cut(l, `\`); top == strings.ToLower(ExeName) || top == strings.ToLower(InstallerName) {
		return fmt.Errorf("extra payload path %q: the installer writes that file itself", p)
	}
	for _, s := range reservedSuffixes {
		if strings.HasSuffix(l, s) {
			return fmt.Errorf("extra payload path %q: the installer uses names ending in %s", p, s)
		}
	}
	return nil
}

// checkExtra checks the extra payload: every path (CheckExtraPath), every build one of builds, and for each
// machine no path twice (letter case ignored) and no file where another file needs a folder.
func checkExtra(xs []ExtraFile, builds map[string]bool) error {
	for _, x := range xs {
		if err := CheckExtraPath(x.Path); err != nil {
			return err
		}
		if x.Build != "" && !builds[x.Build] {
			return fmt.Errorf("extra payload file %s: build %q is not in the payload", x.Path, x.Build)
		}
	}
	for b := range builds {
		files, dirs := map[string]string{}, map[string]string{}
		for _, x := range xs {
			if x.Build != "" && x.Build != b {
				continue
			}
			l := strings.ToLower(x.Path)
			if o, ok := files[l]; ok {
				return fmt.Errorf("extra payload: %s and %s are the same file for %s", o, x.Path, b)
			}
			files[l] = x.Path
			for i := strings.LastIndex(l, `\`); i > 0; i = strings.LastIndex(l[:i], `\`) {
				dirs[l[:i]] = x.Path
			}
		}
		for d, by := range dirs {
			if f, ok := files[d]; ok {
				return fmt.Errorf("extra payload: %s is a file, but %s needs it as a folder", f, by)
			}
		}
	}
	return nil
}

// ExtraFor returns the extra payload's files for a build, in payload order, with their bytes.
func (p *Payload) ExtraFor(build string) []Extra {
	var out []Extra
	for i, x := range p.Manifest.Extra {
		if x.Build == "" || x.Build == build {
			out = append(out, Extra{Path: x.Path, Build: x.Build, Data: p.ExtraData[i]})
		}
	}
	return out
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
	for _, x := range m.Extra {
		if x.Offset < 0 || x.Size < 0 || x.Offset+x.Size > int64(len(data)) || x.Offset+x.Size < x.Offset {
			return nil, fmt.Errorf("tool payload file %s is outside the payload", x.Path)
		}
		d := data[x.Offset : x.Offset+x.Size]
		sum := sha256.Sum256(d)
		if hex.EncodeToString(sum[:]) != x.SHA256 {
			return nil, fmt.Errorf("tool payload file %s: checksum does not match", x.Path)
		}
		p.ExtraData = append(p.ExtraData, d)
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

// CheckShortcutName checks a shortcut's name, which becomes the file name Name + ".lnk": one plain name by the rules
// of a part of CheckExtraPath (so no backslash and no "|", the record's separator), at most 200 characters.
func CheckShortcutName(name string) error {
	if name == "" || len(name) > 200 || strings.Contains(name, `\`) {
		return fmt.Errorf("shortcut name %q: one name of 1 to 200 characters, without a backslash", name)
	}
	if err := checkPart(name); err != nil {
		return fmt.Errorf("shortcut name %q: %v", name, err)
	}
	return nil
}

// checkPart applies the rules of one part of an extra payload path.
func checkPart(part string) error {
	if part == "" || part == "." || part == ".." || strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
		return fmt.Errorf("%q is not a file or folder name", part)
	}
	for _, c := range part {
		if c < 0x20 || strings.ContainsRune(`<>:"/|?*`, c) {
			return fmt.Errorf("character %q is not allowed", c)
		}
	}
	return nil
}

// checkShortcuts checks the shortcuts: every name (CheckShortcutName), no name twice (letter case ignored), and every
// target a path of the extra payload.
func checkShortcuts(scs []Shortcut, xs []ExtraFile) error {
	paths := map[string]bool{}
	for _, x := range xs {
		paths[strings.ToLower(x.Path)] = true
	}
	names := map[string]bool{}
	for _, sc := range scs {
		if err := CheckShortcutName(sc.Name); err != nil {
			return err
		}
		if names[strings.ToLower(sc.Name)] {
			return fmt.Errorf("shortcut %q is listed twice", sc.Name)
		}
		names[strings.ToLower(sc.Name)] = true
		if !paths[strings.ToLower(sc.Target)] {
			return fmt.Errorf("shortcut %q: target %q is not a file of the extra payload", sc.Name, sc.Target)
		}
	}
	return nil
}

// ShortcutsFor returns the shortcuts of a build: those whose target the build's extra payload carries.
func (p *Payload) ShortcutsFor(build string) []Shortcut {
	has := map[string]bool{}
	for _, x := range p.ExtraFor(build) {
		has[strings.ToLower(x.Path)] = true
	}
	var out []Shortcut
	for _, sc := range p.Manifest.Shortcuts {
		if has[strings.ToLower(sc.Target)] {
			out = append(out, sc)
		}
	}
	return out
}
