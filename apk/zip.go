// Package apk writes and rewrites Android application packages with the standard library only: the zip container
// with the alignment Android needs, Android binary XML (read, write, rewrite), a minimal resource table, and APK
// signing (JAR signing "v1", APK Signature Scheme v2 and v3). It holds no keyboard knowledge: the targets android
// (a code-free APK with key character maps) and androidime (a prebuilt runtime APK repackaged for one layout) use it.
//
// Formats: AOSP's public documentation and sources (Apache-2.0), read for facts only; CLEAN-ROOM.md "Android".
package apk

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"strings"
)

// Entry is one file of an archive.
type Entry struct {
	Name string
	Data []byte
	// Store keeps the data uncompressed, its first byte aligned (Align). resources.arsc must be stored (Android
	// 11 and later refuse a compressed one for targetSdkVersion 30 and up), and so must native libraries that are
	// not extracted.
	Store bool
}

// Archive is the list of an APK's files, in the order they are written.
type Archive struct {
	Entries []*Entry
}

// Get returns the entry called name, or nil.
func (a *Archive) Get(name string) *Entry {
	for _, e := range a.Entries {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Put replaces the data of the entry called name, or appends a new entry. store applies to a new entry only;
// a replaced entry keeps its compression.
func (a *Archive) Put(name string, data []byte, store bool) {
	if e := a.Get(name); e != nil {
		e.Data = data
		return
	}
	a.Entries = append(a.Entries, &Entry{Name: name, Data: data, Store: store})
}

// Remove drops the entries for which drop returns true.
func (a *Archive) Remove(drop func(name string) bool) {
	out := a.Entries[:0]
	for _, e := range a.Entries {
		if !drop(e.Name) {
			out = append(out, e)
		}
	}
	a.Entries = out
}

// Align is the alignment of the data of a stored entry: 16 KiB for a native library (so it can be mapped from the
// APK on devices with 16 KiB pages), 4 bytes for every other file (what zipalign does).
func Align(name string) int {
	if strings.HasSuffix(name, ".so") {
		return 16384
	}
	return 4
}

// zipAlignID is the extra field that pads a local header so that the data starts aligned; its data is the
// alignment (uint16) and zero bytes (the form zipalign and apksigner write).
const zipAlignID = 0xd935

// dosDate and dosTime are the fixed time stamp of every entry (1981-01-01 01:01:02), so the same input gives the
// same bytes.
const (
	dosDate = 1<<9 | 1<<5 | 1
	dosTime = 1<<11 | 1<<5 | 1
)

// zipFile is one written entry, for the central directory.
type zipFile struct {
	name         string
	method       uint16
	crc          uint32
	csize, usize uint32
	offset       uint32
	extra        []byte
}

// zipBytes writes the archive: local headers and data in order, then the central directory and its end record.
// It returns the file and the offset of the central directory.
func (a *Archive) zipBytes() ([]byte, int, error) {
	var buf bytes.Buffer
	var files []zipFile
	seen := map[string]bool{}
	for _, e := range a.Entries {
		if seen[e.Name] {
			return nil, 0, fmt.Errorf("apk: two entries called %q", e.Name)
		}
		seen[e.Name] = true
		if e.Name == "" || strings.HasPrefix(e.Name, "/") || strings.Contains(e.Name, "\\") {
			return nil, 0, fmt.Errorf("apk: bad entry name %q", e.Name)
		}
		f := zipFile{name: e.Name, crc: crc32.ChecksumIEEE(e.Data), usize: uint32(len(e.Data))}
		data := e.Data
		if e.Store {
			f.method = zip.Store
		} else {
			var c bytes.Buffer
			w, _ := flate.NewWriter(&c, flate.BestCompression)
			w.Write(e.Data)
			w.Close()
			f.method, data = zip.Deflate, c.Bytes()
		}
		f.csize = uint32(len(data))
		if len(e.Data) > 0xfffffff0 || len(data) > 0xfffffff0 || buf.Len() > 0xfffffff0 {
			return nil, 0, errors.New("apk: archive too large (no zip64)")
		}
		f.offset = uint32(buf.Len())
		if e.Store {
			al := Align(e.Name)
			start := buf.Len() + 30 + len(e.Name) + 6
			pad := (al - start%al) % al
			f.extra = make([]byte, 6+pad)
			binary.LittleEndian.PutUint16(f.extra[0:], zipAlignID)
			binary.LittleEndian.PutUint16(f.extra[2:], uint16(2+pad))
			binary.LittleEndian.PutUint16(f.extra[4:], uint16(al))
		}
		var h [30]byte
		binary.LittleEndian.PutUint32(h[0:], 0x04034b50)
		binary.LittleEndian.PutUint16(h[4:], 20)
		binary.LittleEndian.PutUint16(h[6:], 0x0800) // names are UTF-8
		binary.LittleEndian.PutUint16(h[8:], f.method)
		binary.LittleEndian.PutUint16(h[10:], dosTime)
		binary.LittleEndian.PutUint16(h[12:], dosDate)
		binary.LittleEndian.PutUint32(h[14:], f.crc)
		binary.LittleEndian.PutUint32(h[18:], f.csize)
		binary.LittleEndian.PutUint32(h[22:], f.usize)
		binary.LittleEndian.PutUint16(h[26:], uint16(len(e.Name)))
		binary.LittleEndian.PutUint16(h[28:], uint16(len(f.extra)))
		buf.Write(h[:])
		buf.WriteString(e.Name)
		buf.Write(f.extra)
		buf.Write(data)
		files = append(files, f)
	}
	cdStart := buf.Len()
	for _, f := range files {
		var h [46]byte
		binary.LittleEndian.PutUint32(h[0:], 0x02014b50)
		binary.LittleEndian.PutUint16(h[4:], 20)
		binary.LittleEndian.PutUint16(h[6:], 20)
		binary.LittleEndian.PutUint16(h[8:], 0x0800)
		binary.LittleEndian.PutUint16(h[10:], f.method)
		binary.LittleEndian.PutUint16(h[12:], dosTime)
		binary.LittleEndian.PutUint16(h[14:], dosDate)
		binary.LittleEndian.PutUint32(h[16:], f.crc)
		binary.LittleEndian.PutUint32(h[20:], f.csize)
		binary.LittleEndian.PutUint32(h[24:], f.usize)
		binary.LittleEndian.PutUint16(h[28:], uint16(len(f.name)))
		// The central directory carries no extra field: the padding matters in the local header only.
		binary.LittleEndian.PutUint32(h[42:], f.offset)
		buf.Write(h[:])
		buf.WriteString(f.name)
	}
	cdSize := buf.Len() - cdStart
	if len(files) > 0xffff {
		return nil, 0, errors.New("apk: too many entries (no zip64)")
	}
	var end [22]byte
	binary.LittleEndian.PutUint32(end[0:], 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(files)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(files)))
	binary.LittleEndian.PutUint32(end[12:], uint32(cdSize))
	binary.LittleEndian.PutUint32(end[16:], uint32(cdStart))
	buf.Write(end[:])
	return buf.Bytes(), cdStart, nil
}

// Unsigned returns the archive as a zip file without any signature: for tests and tools only, Android installs
// only signed packages.
func (a *Archive) Unsigned() ([]byte, error) {
	b, _, err := a.zipBytes()
	return b, err
}

// maxEntrySize bounds an entry read from an APK (a runtime APK is a few megabytes).
const maxEntrySize = 256 << 20

// Read reads an APK (or any zip file): every entry with its data, in central-directory order, Store set for
// stored entries. Directory entries are dropped. The signatures are entries like any other (META-INF/*.SF ...);
// the APK Signing Block before the central directory is not an entry and is not kept: Sign writes new ones.
func Read(b []byte) (*Archive, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("apk: %v", err)
	}
	a := &Archive{}
	seen := map[string]bool{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("apk: two entries called %q", f.Name)
		}
		seen[f.Name] = true
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, fmt.Errorf("apk: %s: compression method %d", f.Name, f.Method)
		}
		if f.UncompressedSize64 > maxEntrySize {
			return nil, fmt.Errorf("apk: %s: entry too large", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("apk: %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxEntrySize+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("apk: %s: %v", f.Name, err)
		}
		a.Entries = append(a.Entries, &Entry{Name: f.Name, Data: data, Store: f.Method == zip.Store})
	}
	return a, nil
}

// IsSignatureFile reports whether name is a file of a JAR signature (v1): META-INF/MANIFEST.MF and the
// META-INF/*.SF, *.RSA, *.DSA and *.EC files, which Sign replaces.
func IsSignatureFile(name string) bool {
	if !strings.HasPrefix(name, "META-INF/") || strings.Contains(name[len("META-INF/"):], "/") {
		return false
	}
	if name == "META-INF/MANIFEST.MF" {
		return true
	}
	up := strings.ToUpper(name)
	for _, s := range []string{".SF", ".RSA", ".DSA", ".EC"} {
		if strings.HasSuffix(up, s) {
			return true
		}
	}
	return strings.HasPrefix(up, "META-INF/SIG-")
}

// Names returns the entry names, sorted.
func (a *Archive) Names() []string {
	var n []string
	for _, e := range a.Entries {
		n = append(n, e.Name)
	}
	sort.Strings(n)
	return n
}

func sortedKeys(m map[string][]byte) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}
