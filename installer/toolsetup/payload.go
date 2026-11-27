// Package toolsetup is the logic of the YAKBD installers, yakbd-cli-setup.exe and yakbd-gui-setup.exe
// (docs/installer-cli.md §12): their payload (components: YAKBD CLI, yakbd.exe for x86, x64 and arm64 with LICENSE
// and NOTICE, and optionally YAKBD Editor for x64 and arm64), what is installed on the machine (also the earlier
// one-folder layout of 0.1.0-preview.1 and preview.2), the plan a command line or the wizard asks for, and install,
// change, repair and uninstall into Program Files\YAKBD with one uninstall entry. It talks to Windows only through
// System (implemented by installer/winhost and installer/toolstub), so that every decision is tested on any OS.
// Standard library and the installer packages it shares with the layout installers only: it is compiled into
// installer/toolstub.
package toolsetup

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/payload"
)

// Magic starts a tool payload; the layout installers' payload has payload.Magic.
const Magic = "YAKBDTOL"

// Version is the tool payload format version: 2 since the two installers (format 1, up to 0.1.0-preview.2, had one
// product with yakbd.exe and an extra payload).
const Version = 2

const headerSize = 56

// Builds of the programs and their PE Machine values.
const (
	BuildX86   = "x86"
	BuildX64   = "x64"
	BuildARM64 = "arm64"
)

// MachineOf is the PE Machine of each build.
var MachineOf = map[string]uint16{BuildX86: payload.MachineI386, BuildX64: payload.MachineAMD64, BuildARM64: payload.MachineARM64}

// BuildFor is the build a native machine gets; no fallback to another build.
var BuildFor = map[uint16]string{payload.MachineI386: BuildX86, payload.MachineAMD64: BuildX64, payload.MachineARM64: BuildARM64}

// The components.
const (
	CLI    = "CLI"
	Editor = "Editor"
)

// ComponentIDs are the components in their fixed order.
var ComponentIDs = []string{CLI, Editor}

// ComponentName is each component's name in the wizard and the log.
var ComponentName = map[string]string{CLI: "YAKBD CLI", Editor: "YAKBD Editor"}

// CLIExe is the CLI's program in its folder; the CLI component carries one per build.
const CLIExe = "yakbd.exe"

// Manifest describes an installer's content.
type Manifest struct {
	Format  int     `json:"format"`
	Product Product `json:"product"`
	// AddToPath is the installer's default for "Add YAKBD CLI to PATH" on a machine without the entry: on for the
	// CLI's installer, off for the editor's.
	AddToPath  bool        `json:"addToPath"`
	Components []Component `json:"components"`
	// Shortcuts are Start-menu shortcuts to files of a component (made when that component is installed).
	Shortcuts []Shortcut `json:"shortcuts,omitempty"`
	// UserData is what "Remove my settings" (REMOVEUSERDATA=1) deletes for the starting user, declared by the
	// editor's payload (CheckUserData has the rules).
	UserData []UserData `json:"userData,omitempty"`
}

// Product is the uninstall entry's data: one entry for both components.
type Product struct {
	Name      string `json:"name"`
	Publisher string `json:"publisher"`
	Code      string `json:"code"`
}

// Component is one installable part, in its own folder of Program Files\YAKBD.
type Component struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// Builds are the machines it installs on (the CLI: x86, x64, arm64; the editor: x64, arm64).
	Builds []string `json:"builds"`
	Files  []File   `json:"files"`
}

// File is one file of a component.
type File struct {
	// Path is relative to the component's folder, with backslashes (`qt\Qt6Core.dll`); CheckPath has the rules.
	Path string `json:"path"`
	// Build limits the file to the machines of one build; empty: every build of the component.
	Build string `json:"build,omitempty"`
	// Offset and Packed place the file's DEFLATE stream (RFC 1951, compress/flate at BestCompression) in the
	// payload; Size and SHA256 are of the file itself.
	Offset int64  `json:"offset"`
	Packed int64  `json:"packed"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Shortcut is one Start-menu shortcut: the file Name + ".lnk" in the common Start-menu Programs folder, pointing at
// Target, a file of component Component, made on the machines whose build carries that file.
type Shortcut struct {
	Name      string `json:"name"`
	Component string `json:"component"`
	Target    string `json:"target"`
}

// UserData is one per-user location of a component's settings: a registry key under HKCU\Software (Kind "key")
// or a folder under %LOCALAPPDATA% or %APPDATA% (Kind "dir"). ParentIfEmpty also deletes its parent when that is
// empty afterwards (HKCU\Software\YAKBD for HKCU\Software\YAKBD\YAKBD Editor).
type UserData struct {
	Component     string `json:"component"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	ParentIfEmpty bool   `json:"parentIfEmpty,omitempty"`
}

// Input is one file for Encode.
type Input struct {
	Path  string
	Build string // empty: every build of the component
	Data  []byte
}

// ComponentInput is one component for Encode.
type ComponentInput struct {
	ID      string
	Version string
	Builds  []string
	Files   []Input
}

// Payload is a decoded tool payload.
type Payload struct {
	Manifest *Manifest
	// Packed holds the DEFLATE stream of Manifest.Components[i].Files[j] at Packed[i][j]; FilesFor and Verify
	// inflate them.
	Packed [][][]byte
}

// Encode writes the payload: m's product, defaults, shortcuts and user data, and the components in the given order
// (deterministic output).
func Encode(m Manifest, comps []ComponentInput) ([]byte, error) {
	m.Format, m.Components = Version, nil
	var blob bytes.Buffer
	for _, c := range comps {
		mc := Component{ID: c.ID, Version: c.Version, Builds: c.Builds}
		for _, f := range c.Files {
			sum := sha256.Sum256(f.Data)
			packed, err := deflate(f.Data)
			if err != nil {
				return nil, err
			}
			mc.Files = append(mc.Files, File{Path: f.Path, Build: f.Build, Offset: int64(blob.Len()), Packed: int64(len(packed)), Size: int64(len(f.Data)), SHA256: hex.EncodeToString(sum[:])})
			blob.Write(packed)
		}
		m.Components = append(m.Components, mc)
	}
	if err := m.check(); err != nil {
		return nil, err
	}
	// The CLI's yakbd.exe of each build must be a PE file of that build's machine.
	for _, c := range comps {
		if c.ID != CLI {
			continue
		}
		for _, f := range c.Files {
			if strings.EqualFold(f.Path, CLIExe) {
				if got, err := payload.PEMachine(f.Data); err != nil || got != MachineOf[f.Build] {
					return nil, fmt.Errorf("tool payload: the %s yakbd.exe is not a PE file of machine %04X", f.Build, MachineOf[f.Build])
				}
			}
		}
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
	if !payload.IsGUID(m.Product.Code) || m.Product.Name == "" {
		return errors.New("tool manifest: product code or name missing")
	}
	seen := map[string]bool{}
	for i, c := range m.Components {
		if ComponentName[c.ID] == "" || seen[c.ID] {
			return fmt.Errorf("tool payload component %q unknown or listed twice", c.ID)
		}
		seen[c.ID] = true
		if i > 0 && c.ID == CLI {
			return errors.New("tool payload: the CLI comes first")
		}
		if !versionRE(c.Version) {
			return fmt.Errorf("tool payload component %s: version %q is not a semantic version", c.ID, c.Version)
		}
		builds := map[string]bool{}
		for _, b := range c.Builds {
			if _, ok := MachineOf[b]; !ok || builds[b] {
				return fmt.Errorf("tool payload component %s: build %q unknown or listed twice", c.ID, b)
			}
			builds[b] = true
		}
		if len(builds) == 0 {
			return fmt.Errorf("tool payload component %s has no build", c.ID)
		}
		if err := checkFiles(c, builds); err != nil {
			return err
		}
	}
	if !seen[CLI] {
		return errors.New("tool payload: no CLI component (every installer carries the CLI)")
	}
	if err := checkShortcuts(m); err != nil {
		return err
	}
	for _, u := range m.UserData {
		if err := CheckUserData(u); err != nil {
			return err
		}
		if !seen[u.Component] {
			return fmt.Errorf("user data %s: component %q is not in the payload", u.Path, u.Component)
		}
	}
	return nil
}

// reservedSuffixes are the endings of the installer's temporary names.
var reservedSuffixes = []string{".yakbd-tmp", ".yakbd-new"}

// checkPart applies the rules of one part of a path.
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

// CheckPath checks one file path of a component: relative, with backslashes, every part a plain name (not empty,
// "." or "..", no control character or any of <>:"/|?*, not ending in a space or a dot), and not ending in a name
// the installer uses for its temporary files.
func CheckPath(p string) error {
	if p == "" {
		return errors.New("a payload path is empty")
	}
	for _, part := range strings.Split(p, `\`) {
		if err := checkPart(part); err != nil {
			return fmt.Errorf("payload path %q: %v", p, err)
		}
	}
	l := strings.ToLower(p)
	for _, s := range reservedSuffixes {
		if strings.HasSuffix(l, s) {
			return fmt.Errorf("payload path %q: the installer uses names ending in %s", p, s)
		}
	}
	return nil
}

// checkFiles checks a component's files: every path (CheckPath), every build one of the component's, for each
// build no path twice (letter case ignored) and no file where another needs a folder, and the CLI's yakbd.exe for
// every build.
func checkFiles(c Component, builds map[string]bool) error {
	for _, f := range c.Files {
		if err := CheckPath(f.Path); err != nil {
			return err
		}
		if c.ID == CLI && strings.EqualFold(f.Path, CLIExe) && f.Build == "" {
			return fmt.Errorf("component CLI: yakbd.exe without a build")
		}
		if f.Build != "" && !builds[f.Build] {
			return fmt.Errorf("component %s file %s: build %q is not one of the component's", c.ID, f.Path, f.Build)
		}
	}
	for b := range builds {
		files, dirs := map[string]string{}, map[string]string{}
		for _, f := range c.Files {
			if f.Build != "" && f.Build != b {
				continue
			}
			l := strings.ToLower(f.Path)
			if o, ok := files[l]; ok {
				return fmt.Errorf("component %s: %s and %s are the same file for %s", c.ID, o, f.Path, b)
			}
			files[l] = f.Path
			for i := strings.LastIndex(l, `\`); i > 0; i = strings.LastIndex(l[:i], `\`) {
				dirs[l[:i]] = f.Path
			}
		}
		for d, by := range dirs {
			if f, ok := files[d]; ok {
				return fmt.Errorf("component %s: %s is a file, but %s needs it as a folder", c.ID, f, by)
			}
		}
		if c.ID == CLI && files[CLIExe] == "" {
			return fmt.Errorf("component CLI: no yakbd.exe for %s", b)
		}
	}
	return nil
}

// CheckShortcutName checks a shortcut's name, which becomes the file name Name + ".lnk": one plain name by the
// rules of a part of CheckPath (so no backslash and no "|", the record's separator), at most 200 characters.
func CheckShortcutName(name string) error {
	if name == "" || len(name) > 200 || strings.Contains(name, `\`) {
		return fmt.Errorf("shortcut name %q: one name of 1 to 200 characters, without a backslash", name)
	}
	if err := checkPart(name); err != nil {
		return fmt.Errorf("shortcut name %q: %v", name, err)
	}
	return nil
}

func checkShortcuts(m *Manifest) error {
	names := map[string]bool{}
	for _, sc := range m.Shortcuts {
		if err := CheckShortcutName(sc.Name); err != nil {
			return err
		}
		if names[strings.ToLower(sc.Name)] {
			return fmt.Errorf("shortcut %q is listed twice", sc.Name)
		}
		names[strings.ToLower(sc.Name)] = true
		c := m.Component(sc.Component)
		if c == nil {
			return fmt.Errorf("shortcut %q: component %q is not in the payload", sc.Name, sc.Component)
		}
		found := false
		for _, f := range c.Files {
			found = found || strings.EqualFold(f.Path, sc.Target)
		}
		if !found {
			return fmt.Errorf("shortcut %q: target %q is not a file of component %s", sc.Name, sc.Target, sc.Component)
		}
	}
	return nil
}

// CheckUserData checks one user-data declaration: a known component; "key": HKCU\Software\ and at least one more
// name; "dir": %LOCALAPPDATA%\ or %APPDATA%\ and at least one more name; every name plain (checkPart). So nothing
// outside the starting user's own settings can be named, whatever the payload or the registry holds.
func CheckUserData(u UserData) error {
	if ComponentName[u.Component] == "" {
		return fmt.Errorf("user data %q: unknown component %q", u.Path, u.Component)
	}
	var rest string
	switch u.Kind {
	case "key":
		r, ok := cutPrefixFold(u.Path, `HKCU\Software\`)
		if !ok {
			return fmt.Errorf("user data %q: a key must be under HKCU\\Software\\", u.Path)
		}
		rest = r
	case "dir":
		r, ok := cutPrefixFold(u.Path, `%LOCALAPPDATA%\`)
		if !ok {
			r, ok = cutPrefixFold(u.Path, `%APPDATA%\`)
		}
		if !ok {
			return fmt.Errorf("user data %q: a folder must be under %%LOCALAPPDATA%%\\ or %%APPDATA%%\\", u.Path)
		}
		rest = r
	default:
		return fmt.Errorf("user data %q: kind %q is not key or dir", u.Path, u.Kind)
	}
	parts := strings.Split(rest, `\`)
	if u.ParentIfEmpty && len(parts) < 2 {
		return fmt.Errorf("user data %q: parentIfEmpty needs a parent below the root", u.Path)
	}
	for _, p := range parts {
		if err := checkPart(p); err != nil {
			return fmt.Errorf("user data %q: %v", u.Path, err)
		}
	}
	return nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// Component returns the manifest's component id, or nil.
func (m *Manifest) Component(id string) *Component {
	for i := range m.Components {
		if m.Components[i].ID == id {
			return &m.Components[i]
		}
	}
	return nil
}

// Supports reports whether the payload carries component id for build.
func (p *Payload) Supports(id, build string) bool {
	c := p.Manifest.Component(id)
	if c == nil {
		return false
	}
	for _, b := range c.Builds {
		if b == build {
			return true
		}
	}
	return false
}

// FilesFor returns a component's files for a build, in payload order, inflated and checked (SHA-256, size, and the
// CLI's yakbd.exe a PE file of the build's machine).
func (p *Payload) FilesFor(id, build string) ([]Input, error) {
	var out []Input
	for i, c := range p.Manifest.Components {
		if c.ID != id {
			continue
		}
		for j, f := range c.Files {
			if f.Build != "" && f.Build != build {
				continue
			}
			d, err := inflate(p.Packed[i][j], f.Size)
			if err == nil {
				if sum := sha256.Sum256(d); hex.EncodeToString(sum[:]) != f.SHA256 {
					err = errors.New("checksum does not match")
				}
			}
			if err == nil && id == CLI && strings.EqualFold(f.Path, CLIExe) {
				if mach, perr := payload.PEMachine(d); perr != nil || mach != MachineOf[f.Build] {
					err = fmt.Errorf("not a %s program", f.Build)
				}
			}
			if err != nil {
				return nil, fmt.Errorf("tool payload file %s\\%s: %v (the installer is damaged)", id, f.Path, err)
			}
			out = append(out, Input{Path: f.Path, Build: f.Build, Data: d})
		}
	}
	return out, nil
}

// Verify inflates and checks every file a build installs (FilesFor of each component it carries for the build).
func (p *Payload) Verify(build string) error {
	for _, c := range p.Manifest.Components {
		if p.Supports(c.ID, build) {
			if _, err := p.FilesFor(c.ID, build); err != nil {
				return err
			}
		}
	}
	return nil
}

// ShortcutsFor returns the shortcuts of a component for a build: those whose target the build carries.
func (p *Payload) ShortcutsFor(id, build string) []Shortcut {
	has := map[string]bool{}
	for _, c := range p.Manifest.Components {
		if c.ID != id {
			continue
		}
		for _, f := range c.Files {
			if f.Build == "" || f.Build == build {
				has[strings.ToLower(f.Path)] = true
			}
		}
	}
	var out []Shortcut
	for _, sc := range p.Manifest.Shortcuts {
		if sc.Component == id && has[strings.ToLower(sc.Target)] {
			out = append(out, sc)
		}
	}
	return out
}

// Version is the installer's product version: the editor's when it carries one, else the CLI's.
func (m *Manifest) Version() string {
	if c := m.Component(Editor); c != nil {
		return c.Version
	}
	if c := m.Component(CLI); c != nil {
		return c.Version
	}
	return ""
}

// Decode reads a tool payload at the start of b, checking every hash and the CLI's PE machines.
func Decode(b []byte) (*Payload, error) {
	if len(b) < headerSize || string(b[:8]) != Magic {
		return nil, errors.New("no yakbd tool payload found (the file is damaged or not a YAKBD installer)")
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
	p := &Payload{Manifest: m}
	for _, c := range m.Components {
		var cd [][]byte
		for _, f := range c.Files {
			if f.Offset < 0 || f.Packed < 0 || f.Offset+f.Packed > int64(len(data)) || f.Offset+f.Packed < f.Offset {
				return nil, fmt.Errorf("tool payload file %s\\%s is outside the payload", c.ID, f.Path)
			}
			// DEFLATE expands at most about 1032 times; a larger claimed size is a damaged manifest.
			if f.Size < 0 || f.Size > 1032*f.Packed+64 {
				return nil, fmt.Errorf("tool payload file %s\\%s: size %d does not fit its %d packed bytes", c.ID, f.Path, f.Size, f.Packed)
			}
			cd = append(cd, data[f.Offset:f.Offset+f.Packed])
		}
		p.Packed = append(p.Packed, cd)
	}
	return p, nil
}

// deflate compresses one file (RFC 1951) at compress/flate's best compression: the same bytes for the same input and
// Go version, so the installers stay reproducible.
func deflate(d []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(d); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// inflate expands one file, which must come to exactly size bytes.
func inflate(packed []byte, size int64) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(packed))
	defer r.Close()
	var b bytes.Buffer
	n, err := io.Copy(&b, io.LimitReader(r, size+1))
	if err != nil {
		return nil, err
	}
	if n != size {
		return nil, fmt.Errorf("%d bytes, want %d", n, size)
	}
	return b.Bytes(), nil
}

// FromExe finds and decodes the payload after the PE image of an installer file.
func FromExe(b []byte) (*Payload, error) {
	end, err := payload.ImageEnd(b)
	if err != nil {
		return nil, err
	}
	return Decode(b[end:])
}

// Summary names the components and their builds, for the log ("CLI 0.1.0 [x86 x64 arm64], Editor 0.1.0 [x64]").
func (m *Manifest) Summary() string {
	var out []string
	for _, c := range m.Components {
		out = append(out, fmt.Sprintf("%s %s [%s]", c.ID, c.Version, strings.Join(c.Builds, " ")))
	}
	return strings.Join(out, ", ")
}
