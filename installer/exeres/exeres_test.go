package exeres

import (
	"bytes"
	stdpe "debug/pe"
	"encoding/binary"
	"encoding/xml"
	"image/png"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/pe"
)

func stub(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../stub-386.exe")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var demo = Product{Name: "Latin AltGr test", Version: "0.2.0-beta", Publisher: "YAKBD", FileName: "kbldemo-setup.exe"}

func added(t *testing.T, p Product) ([]byte, *pe.File) {
	t.Helper()
	res, err := For(p)
	if err != nil {
		t.Fatal(err)
	}
	s := stub(t)
	out, err := Add(s, res)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[:0x40], s[:0x40]) {
		t.Error("the DOS header changed")
	}
	f, err := pe.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, f
}

func TestAddSection(t *testing.T) {
	out, f := added(t, demo)
	orig, _ := pe.Parse(stub(t))
	if len(f.Sections) != len(orig.Sections)+1 {
		t.Fatalf("%d sections", len(f.Sections))
	}
	last := f.Sections[len(f.Sections)-1]
	d := f.Dir(pe.DirResource)
	switch {
	case last.Name != ".rsrc" || last.RVA != orig.SizeOfImage || d.RVA != last.RVA || d.Size != last.VirtualSize:
		t.Errorf("section %+v, directory %+v, old SizeOfImage %#x", last, d, orig.SizeOfImage)
	case last.PointerToRawData != uint32(len(stub(t))) || last.PointerToRawData+last.RawSize != uint32(len(out)) || last.RawSize%f.FileAlignment != 0:
		t.Errorf("raw data %#x+%#x, file %#x", last.PointerToRawData, last.RawSize, len(out))
	case f.SizeOfImage != last.RVA+alignUp(last.VirtualSize, f.SectionAlignment):
		t.Errorf("SizeOfImage %#x", f.SizeOfImage)
	case last.Characteristics != pe.SecInitData|pe.SecMemRead:
		t.Errorf("characteristics %#x", last.Characteristics)
	}
	// The standard library's reader agrees.
	sf, err := stdpe.NewFile(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if sf.Section(".rsrc") == nil {
		t.Error("debug/pe sees no .rsrc")
	}
	// Twice the same input, the same bytes.
	again, _ := added(t, demo)
	if !bytes.Equal(out, again) {
		t.Error("not deterministic")
	}
	if _, err := Add(out, nil); err == nil {
		t.Error("a second resource section must be refused")
	}
	if _, err := Add(append(stub(t), 1, 2, 3), nil); err == nil {
		t.Error("bytes after the last section must be refused")
	}
}

func resources(t *testing.T, f *pe.File) map[[2]uint16][]byte {
	t.Helper()
	rs, err := f.Resources()
	if err != nil {
		t.Fatal(err)
	}
	m := map[[2]uint16][]byte{}
	for _, r := range rs {
		if r.Lang != LangEnglishUS {
			t.Errorf("resource %d/%d language %#x", r.Type, r.Name, r.Lang)
		}
		m[[2]uint16{r.Type, r.Name}] = r.Data
	}
	return m
}

func TestManifest(t *testing.T) {
	_, f := added(t, demo)
	m := resources(t, f)[[2]uint16{TypeManifest, 1}]
	// Every element and attribute by its namespace and local name; character data by element.
	attrs, text := map[string]string{}, map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(m))
	var stack []string
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch e := tok.(type) {
		case xml.StartElement:
			name := e.Name.Space + " " + e.Name.Local
			stack = append(stack, name)
			for _, a := range e.Attr {
				attrs[name+" @"+a.Name.Local] = a.Value
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 && strings.TrimSpace(string(e)) != "" {
				text[stack[len(stack)-1]] = strings.TrimSpace(string(e))
			}
		}
	}
	want := map[string]string{
		"urn:schemas-microsoft-com:asm.v3 requestedExecutionLevel @level":    "asInvoker",
		"urn:schemas-microsoft-com:asm.v3 requestedExecutionLevel @uiAccess": "false",
		"urn:schemas-microsoft-com:compatibility.v1 supportedOS @Id":         "{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}",
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("%s = %q, want %q", k, attrs[k], v)
		}
	}
	if got := text["http://schemas.microsoft.com/SMI/2016/WindowsSettings dpiAwareness"]; got != "PerMonitorV2" {
		t.Errorf("dpiAwareness %q", got)
	}
	if !strings.HasPrefix(string(m), "<?xml") {
		t.Error("no XML declaration")
	}
}

// parseVS reads a version resource node: key, value and children.
type vsNode2 struct {
	key      string
	value    []byte
	children []vsNode2
}

func parseVS(t *testing.T, b []byte) vsNode2 {
	t.Helper()
	le := binary.LittleEndian
	n := int(le.Uint16(b))
	vlen := int(le.Uint16(b[2:]))
	text := le.Uint16(b[4:]) == 1
	p := 6
	var key []uint16
	for le.Uint16(b[p:]) != 0 {
		key = append(key, le.Uint16(b[p:]))
		p += 2
	}
	p += 2
	p = (p + 3) &^ 3
	if text {
		vlen *= 2
	}
	node := vsNode2{key: string(utf16.Decode(key)), value: b[p : p+vlen]}
	p = (p + vlen + 3) &^ 3
	for p < n {
		c := parseVS(t, b[p:n])
		node.children = append(node.children, c)
		p = (p + int(le.Uint16(b[p:])) + 3) &^ 3
	}
	return node
}

func u16s(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:]))
	}
	return strings.TrimRight(string(utf16.Decode(u)), "\x00")
}

func TestVersion(t *testing.T) {
	for _, p := range []Product{demo, {Name: "No publisher", Version: "1.2", FileName: "x-setup.exe"}} {
		_, f := added(t, p)
		root := parseVS(t, resources(t, f)[[2]uint16{TypeVersion, 1}])
		le := binary.LittleEndian
		fixed := root.value
		n := Numbers(p.Version)
		if root.key != "VS_VERSION_INFO" || len(fixed) != 52 || le.Uint32(fixed) != vsSignature || le.Uint32(fixed[36:]) != vftApp ||
			le.Uint32(fixed[16:]) != uint32(n[0])<<16|uint32(n[1]) || le.Uint32(fixed[20:]) != uint32(n[2])<<16|uint32(n[3]) {
			t.Errorf("%s: fixed info % x", p.Name, fixed)
		}
		got := map[string]string{}
		for _, c := range root.children {
			if c.key != "StringFileInfo" {
				continue
			}
			for _, tab := range c.children {
				if tab.key != "040904B0" {
					t.Errorf("table %s", tab.key)
				}
				for _, s := range tab.children {
					got[s.key] = u16s(s.value)
				}
			}
		}
		want := map[string]string{"ProductName": p.Name, "ProductVersion": p.Version, "FileVersion": p.Version,
			"FileDescription": p.Name + " setup", "OriginalFilename": p.FileName}
		if p.Publisher != "" {
			want["CompanyName"] = p.Publisher
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", p.Name, k, got[k], v)
			}
		}
		if _, ok := got["CompanyName"]; ok && p.Publisher == "" {
			t.Errorf("%s: CompanyName without a publisher", p.Name)
		}
	}
}

func TestNumbers(t *testing.T) {
	for in, want := range map[string][4]uint16{"1.0.0.0": {1, 0, 0, 0}, "0.2.0-beta": {0, 2, 0, 0}, "0.1.0-preview.1": {0, 1, 0, 0}, "0.1.2-rc.1": {0, 1, 2, 0}, "2": {2, 0, 0, 0},
		"v3": {}, "1.70000.2": {1, 0, 0, 0}, "1.2.3.4.5": {1, 2, 3, 4}, "": {}} {
		if got := Numbers(in); got != want {
			t.Errorf("Numbers(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIcon(t *testing.T) {
	_, f := added(t, demo)
	m := resources(t, f)
	g := m[[2]uint16{TypeGroupIcon, 1}]
	le := binary.LittleEndian
	if len(g) != 6+14*len(IconSizes) || le.Uint16(g[2:]) != 1 || int(le.Uint16(g[4:])) != len(IconSizes) {
		t.Fatalf("group % x", g)
	}
	for i, size := range IconSizes {
		e := g[6+14*i:]
		id := le.Uint16(e[12:])
		img := m[[2]uint16{TypeIcon, id}]
		if int(le.Uint32(e[8:])) != len(img) || img == nil {
			t.Errorf("size %d: entry says %d bytes, image has %d", size, le.Uint32(e[8:]), len(img))
		}
		w := int(e[0])
		if w == 0 {
			w = 256
		}
		if w != size {
			t.Errorf("entry %d width %d", i, w)
		}
		if size >= 256 {
			p, err := png.Decode(bytes.NewReader(img))
			if err != nil || p.Bounds().Dx() != 256 {
				t.Errorf("256 PNG: %v", err)
			}
			continue
		}
		if le.Uint32(img) != 40 || int(le.Uint32(img[4:])) != size || int(le.Uint32(img[8:])) != 2*size || le.Uint16(img[14:]) != 32 ||
			len(img) != 40+4*size*size+(size+31)/32*4*size {
			t.Errorf("size %d: header % x, %d bytes", size, img[:16], len(img))
		}
	}
	// The picture: transparent corners, opaque body.
	px := render(32)
	if px[0].A != 0 || px[16*32+2].A != 255 {
		t.Errorf("corner alpha %d, body alpha %d", px[0].A, px[16*32+2].A)
	}
}

// TestParseICO reads an .ico file's images; a damaged one is refused.
func TestParseICO(t *testing.T) {
	img := []byte("\x89PNG image")
	b := []byte{0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 1, 0, 32, 0}
	b = binary.LittleEndian.AppendUint32(b, uint32(len(img)))
	b = binary.LittleEndian.AppendUint32(b, 22)
	b = append(b, img...)
	got, err := ParseICO(b)
	if err != nil || len(got) != 1 || got[0].Size != 256 || string(got[0].Data) != string(img) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ParseICO(b[:30]); err == nil {
		t.Error("a truncated file accepted")
	}
	if _, err := ParseICO(append([]byte{0, 0, 2, 0}, b[4:]...)); err == nil {
		t.Error("a cursor file accepted")
	}
}
