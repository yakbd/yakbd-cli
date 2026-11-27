// Package exeres gives the installer program its Windows resources when `yakbd installer` writes it
// (docs/installer.md §5.1): an application manifest, an icon drawn by this package and a version resource made from
// the installer's product data. Add appends them as one new .rsrc section to the stub's PE image, before the
// payload. The stub itself is built without resources (tools/buildstub), so the resources never need a stub
// rebuild, and nothing here is imported by the stub.
//
// Formats: PE/COFF section table and resource directory
// (https://learn.microsoft.com/en-us/windows/win32/debug/pe-format, "The .rsrc Section"); resource types
// (https://learn.microsoft.com/en-us/windows/win32/menurc/resource-types); manifest
// (https://learn.microsoft.com/en-us/windows/win32/sbscs/application-manifests); version resource (VS_VERSIONINFO
// and its child structures, https://learn.microsoft.com/en-us/windows/win32/menurc/vs-versioninfo); icon groups
// (GRPICONDIR, https://learn.microsoft.com/en-us/windows/win32/menurc/newheader and resdir).
package exeres

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/yakbd/yakbd-cli/pe"
)

// Resource types (Win32 resource type ids).
const (
	TypeIcon      = 3
	TypeGroupIcon = 14
	TypeVersion   = 16
	TypeManifest  = 24
)

// LangEnglishUS is the language id the resources are filed under.
const LangEnglishUS = 0x0409

// Resource is one leaf of the resource tree.
type Resource struct {
	Type, Name, Lang uint16
	Data             []byte
}

// Product is what the version resource says.
type Product struct {
	Name, Version, Publisher string
	// FileName is the installer's default file name (OriginalFilename).
	FileName string
}

// For returns every resource of an installer: the manifest, the icon group with its images and the version.
func For(p Product) ([]Resource, error) {
	res := []Resource{{TypeManifest, 1, LangEnglishUS, []byte(Manifest)}}
	icons, err := IconImages()
	if err != nil {
		return nil, err
	}
	group := make([]byte, 6, 6+14*len(icons))
	binary.LittleEndian.PutUint16(group[2:], 1)
	binary.LittleEndian.PutUint16(group[4:], uint16(len(icons)))
	for i, ic := range icons {
		id := uint16(i + 1)
		res = append(res, Resource{TypeIcon, id, LangEnglishUS, ic.Data})
		e := make([]byte, 14)
		w := byte(ic.Size)
		if ic.Size >= 256 {
			w = 0 // 0 means 256
		}
		e[0], e[1] = w, w
		binary.LittleEndian.PutUint16(e[4:], 1)  // planes
		binary.LittleEndian.PutUint16(e[6:], 32) // bits per pixel
		binary.LittleEndian.PutUint32(e[8:], uint32(len(ic.Data)))
		binary.LittleEndian.PutUint16(e[12:], id)
		group = append(group, e...)
	}
	res = append(res, Resource{TypeGroupIcon, 1, LangEnglishUS, group})
	res = append(res, Resource{TypeVersion, 1, LangEnglishUS, Version(p)})
	return res, nil
}

// Add returns a copy of the PE image exe with res in a new last section ".rsrc" (data directory entry 2). The
// image must have no resources yet and nothing after its last section.
func Add(exe []byte, res []Resource) ([]byte, error) {
	f, err := pe.Parse(exe)
	if err != nil {
		return nil, err
	}
	if d := f.Dir(pe.DirResource); d.RVA != 0 || d.Size != 0 {
		return nil, errors.New("the program already has resources")
	}
	if len(f.Sections) == 0 || f.FileAlignment == 0 || f.SectionAlignment == 0 {
		return nil, errors.New("the program has no sections")
	}
	var end, vend uint32
	for _, s := range f.Sections {
		end = max(end, s.PointerToRawData+s.RawSize)
		vend = max(vend, s.RVA+alignUp(max(s.VirtualSize, s.RawSize), f.SectionAlignment))
	}
	if int(end) != len(exe) {
		return nil, fmt.Errorf("the program has %d bytes after its last section", len(exe)-int(end))
	}
	if vend != f.SizeOfImage {
		return nil, fmt.Errorf("SizeOfImage %#x, sections end at %#x", f.SizeOfImage, vend)
	}
	le := binary.LittleEndian
	lfanew := le.Uint32(exe[0x3C:])
	coff := lfanew + 4
	optSize := uint32(le.Uint16(exe[coff+16:]))
	shdr := coff + 20 + optSize + 40*uint32(len(f.Sections))
	if shdr+40 > f.SizeOfHeaders {
		return nil, errors.New("no room for another section header")
	}
	for _, b := range exe[shdr : shdr+40] {
		if b != 0 {
			return nil, errors.New("the space after the section table is not empty")
		}
	}
	rva := f.SizeOfImage
	data := Tree(res, rva)
	raw := alignUp(uint32(len(data)), f.FileAlignment)
	out := make([]byte, len(exe), len(exe)+int(raw))
	copy(out, exe)
	out = append(out, data...)
	out = append(out, make([]byte, int(raw)-len(data))...)

	h := out[shdr : shdr+40]
	copy(h, ".rsrc\x00\x00\x00")
	le.PutUint32(h[8:], uint32(len(data)))
	le.PutUint32(h[12:], rva)
	le.PutUint32(h[16:], raw)
	le.PutUint32(h[20:], end)
	le.PutUint32(h[36:], pe.SecInitData|pe.SecMemRead)
	le.PutUint16(out[coff+2:], uint16(len(f.Sections)+1))
	opt := coff + 20
	w := uint32(56) // SizeOfImage in a PE32 or PE32+ optional header
	le.PutUint32(out[opt+w:], rva+alignUp(uint32(len(data)), f.SectionAlignment))
	dirs := opt + 96
	if f.PE32Plus {
		dirs = opt + 112
	}
	if len(f.Dirs) <= pe.DirResource {
		return nil, errors.New("the optional header has no resource directory entry")
	}
	le.PutUint32(out[dirs+8*pe.DirResource:], rva)
	le.PutUint32(out[dirs+8*pe.DirResource+4:], uint32(len(data)))
	if f.CheckSum != 0 {
		le.PutUint32(out[f.CheckSumOffset:], pe.Checksum(out, f.CheckSumOffset))
	}
	return out, nil
}

func alignUp(v, a uint32) uint32 { return (v + a - 1) / a * a }

// Tree lays out a resource section that will be loaded at rva: three directory levels of id entries (type, name,
// language), sorted by id as the format requires, then the data entries, then the data, each blob 8-byte aligned.
func Tree(res []Resource, rva uint32) []byte {
	rs := append([]Resource(nil), res...)
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Lang < b.Lang
	})
	type node struct {
		id   uint16
		kids []*node
		leaf int // index into rs at the language level
	}
	var root node
	for i, r := range rs {
		if n := len(root.kids); n == 0 || root.kids[n-1].id != r.Type {
			root.kids = append(root.kids, &node{id: r.Type})
		}
		t := root.kids[len(root.kids)-1]
		if n := len(t.kids); n == 0 || t.kids[n-1].id != r.Name {
			t.kids = append(t.kids, &node{id: r.Name})
		}
		nn := t.kids[len(t.kids)-1]
		nn.kids = append(nn.kids, &node{id: r.Lang, leaf: i})
	}
	dirSize := func(n *node) uint32 { return 16 + 8*uint32(len(n.kids)) }
	off := map[*node]uint32{&root: 0}
	pos := dirSize(&root)
	for _, t := range root.kids {
		off[t] = pos
		pos += dirSize(t)
	}
	for _, t := range root.kids {
		for _, nn := range t.kids {
			off[nn] = pos
			pos += dirSize(nn)
		}
	}
	entries := pos
	pos += 16 * uint32(len(rs))
	dataAt := make([]uint32, len(rs))
	for i, r := range rs {
		pos = alignUp(pos, 8)
		dataAt[i] = pos
		pos += uint32(len(r.Data))
	}
	buf := make([]byte, alignUp(pos, 8))
	le := binary.LittleEndian
	dir := func(n *node, target func(k *node) uint32) {
		at := off[n]
		le.PutUint16(buf[at+14:], uint16(len(n.kids)))
		for i, k := range n.kids {
			e := at + 16 + 8*uint32(i)
			le.PutUint32(buf[e:], uint32(k.id))
			le.PutUint32(buf[e+4:], target(k))
		}
	}
	sub := func(k *node) uint32 { return off[k] | 0x8000_0000 }
	dir(&root, sub)
	for _, t := range root.kids {
		dir(t, sub)
		for _, nn := range t.kids {
			dir(nn, func(k *node) uint32 { return entries + 16*uint32(k.leaf) })
		}
	}
	for i, r := range rs {
		e := entries + 16*uint32(i)
		le.PutUint32(buf[e:], rva+dataAt[i])
		le.PutUint32(buf[e+4:], uint32(len(r.Data)))
		copy(buf[dataAt[i]:], r.Data)
	}
	return buf
}
