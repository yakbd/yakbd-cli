package apk

import (
	"errors"
	"fmt"
	"sort"
	"unicode/utf16"
)

// Resource is one file resource of a resource table: res/<Type>/<Name>.<ext>, such as an xml or raw resource.
type Resource struct {
	// Type is the resource type ("xml", "raw").
	Type string
	// Name is the resource name: lower-case letters, digits and '_', not starting with a digit.
	Name string
	// File is the entry of the archive that holds it ("res/raw/de.kcm").
	File string
}

// PackageID is the package id of an application's resources (the 0x7f of every resource id 0x7fTTEEEE).
const PackageID = 0x7f

// configSize is the size of the ResTable_config written: the default configuration, all zero after its size.
const configSize = 64

// ResourceTable writes a minimal resources.arsc for the file resources res of package pkg: one package (id 0x7f),
// one type per resource type (ids from 1 in order of first appearance), the entries of each type sorted by name,
// every entry in the default configuration only. It returns the table and the resource id of each resource, keyed
// "<type>/<name>" (what a manifest or XML file references as @<type>/<name>).
func ResourceTable(pkg string, res []Resource) ([]byte, map[string]uint32, error) {
	if pkg == "" || len(utf16.Encode([]rune(pkg))) > 127 {
		return nil, nil, errors.New("apk: resource table: package name empty or longer than 127 UTF-16 units")
	}
	var types []string
	byType := map[string][]Resource{}
	seen := map[string]bool{}
	for _, r := range res {
		if !resourceName(r.Type) || !resourceName(r.Name) || r.File == "" {
			return nil, nil, fmt.Errorf("apk: resource table: bad resource %s/%s", r.Type, r.Name)
		}
		k := r.Type + "/" + r.Name
		if seen[k] {
			return nil, nil, fmt.Errorf("apk: resource table: two resources %s", k)
		}
		seen[k] = true
		if byType[r.Type] == nil {
			types = append(types, r.Type)
		}
		byType[r.Type] = append(byType[r.Type], r)
	}
	if len(types) > 0xfe {
		return nil, nil, errors.New("apk: resource table: too many types")
	}
	var values, keys []string
	ids := map[string]uint32{}
	var chunks []byte
	for ti, t := range types {
		rs := byType[t]
		sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
		if len(rs) > 0xffff {
			return nil, nil, errors.New("apk: resource table: too many entries")
		}
		tid := uint8(ti + 1)
		spec := make([]byte, 16+4*len(rs))
		put16(spec, chunkTableSpec)
		put16(spec[2:], 16)
		put32(spec[4:], uint32(len(spec)))
		spec[8] = tid
		put16(spec[10:], 1)
		put32(spec[12:], uint32(len(rs)))
		chunks = append(chunks, spec...)

		hs := 20 + configSize
		start := hs + 4*len(rs)
		ty := make([]byte, start+16*len(rs))
		put16(ty, chunkTableType)
		put16(ty[2:], uint16(hs))
		put32(ty[4:], uint32(len(ty)))
		ty[8] = tid
		put32(ty[12:], uint32(len(rs)))
		put32(ty[16:], uint32(start))
		put32(ty[20:], configSize)
		for i, r := range rs {
			put32(ty[hs+4*i:], uint32(16*i))
			e := ty[start+16*i:]
			put16(e, 8)
			put32(e[4:], uint32(len(keys)))
			put16(e[8:], 8)
			e[11] = TypeString
			put32(e[12:], uint32(len(values)))
			keys = append(keys, r.Name)
			values = append(values, r.File)
			ids[t+"/"+r.Name] = uint32(PackageID)<<24 | uint32(tid)<<16 | uint32(i)
		}
		chunks = append(chunks, ty...)
	}
	typePool := encodePool(types, false)
	keyPool := encodePool(keys, false)
	const phs = 288
	pk := make([]byte, phs)
	put16(pk, chunkTablePackage)
	put16(pk[2:], phs)
	put32(pk[8:], PackageID)
	for i, u := range utf16.Encode([]rune(pkg)) {
		put16(pk[12+2*i:], u)
	}
	put32(pk[268:], phs)
	put32(pk[272:], uint32(len(types)))
	put32(pk[276:], uint32(phs+len(typePool)))
	put32(pk[280:], uint32(len(keys)))
	pk = append(pk, typePool...)
	pk = append(pk, keyPool...)
	pk = append(pk, chunks...)
	put32(pk[4:], uint32(len(pk)))

	vp := encodePool(values, false)
	out := make([]byte, 12, 12+len(vp)+len(pk))
	put16(out, chunkTable)
	put16(out[2:], 12)
	put32(out[8:], 1)
	out = append(out, vp...)
	out = append(out, pk...)
	put32(out[4:], uint32(len(out)))
	return out, ids, nil
}

// resourceName reports whether s is a valid resource type or entry name.
func resourceName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// TablePackage returns the package name and id of the first package of a resources.arsc.
func TablePackage(b []byte) (string, uint32, error) {
	if len(b) < 12 || le16(b) != chunkTable {
		return "", 0, errors.New("apk: resources.arsc: not a resource table")
	}
	size := int(le32(b[4:]))
	if size > len(b) {
		return "", 0, errors.New("apk: resources.arsc: truncated")
	}
	for off := int(le16(b[2:])); off+8 <= size; {
		cs := int(le32(b[off+4:]))
		if cs < 8 || off+cs > size {
			return "", 0, errors.New("apk: resources.arsc: bad chunk")
		}
		if le16(b[off:]) == chunkTablePackage {
			if cs < 268 {
				return "", 0, errors.New("apk: resources.arsc: package chunk too short")
			}
			var u []uint16
			for i := 0; i < 128; i++ {
				c := le16(b[off+12+2*i:])
				if c == 0 {
					break
				}
				u = append(u, c)
			}
			return string(utf16.Decode(u)), le32(b[off+8:]), nil
		}
		off += cs
	}
	return "", 0, errors.New("apk: resources.arsc: no package")
}
