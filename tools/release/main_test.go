package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/macho"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/yakbd/yakbd-cli/internal/relbuild"
)

func TestCheckVersion(t *testing.T) {
	for v, ok := range map[string]bool{
		"0.2.0-preview.1": true, "0.2.0": false, "0.1.0-preview.3": false, "0.0.0-rehearsal.1": true, "9.9.9-rehearsal.2": true,
		"0.3.0": false, "v0.2.0-preview.1": false, "0.2": false, "0.1.0+build": false, "0.1.0-": false, "01.2.0": false,
	} {
		if err := checkVersion(v); (err == nil) != ok {
			t.Errorf("%s: %v", v, err)
		}
	}
}

// TestTarGzFixed checks that an archive does not depend on when it is written and that it reads back with fixed
// names, modes, times and owners.
func TestTarGzFixed(t *testing.T) {
	files := [][2]any{{"yakbd", []byte("program")}, {"LICENSE", []byte("text")}}
	a, err := tarGz(files)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	b, _ := tarGz(files)
	if !bytes.Equal(a, b) {
		t.Error("two archives of the same files differ")
	}
	zr, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	if zr.Name != "" || !zr.ModTime.IsZero() {
		t.Errorf("gzip header name %q time %v", zr.Name, zr.ModTime)
	}
	tr := tar.NewReader(zr)
	for i, want := range []struct {
		name string
		mode int64
		data string
	}{{"yakbd", 0o755, "program"}, {"LICENSE", 0o644, "text"}} {
		h, err := tr.Next()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		if h.Name != want.name || h.Mode != want.mode || string(data) != want.data || !h.ModTime.Equal(archiveTime) || h.Uid != 0 || h.Uname != "" {
			t.Errorf("entry %d: %+v %q", i, h, data)
		}
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("more entries: %v", err)
	}
}

// thinMachO is a Mach-O header without load commands, for a cpu type, followed by tag.
func thinMachO(cpu macho.Cpu, sub uint32, tag string) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b, macho.Magic64)
	binary.LittleEndian.PutUint32(b[4:], uint32(cpu))
	binary.LittleEndian.PutUint32(b[8:], sub)
	binary.LittleEndian.PutUint32(b[12:], uint32(macho.TypeExec))
	return append(b, tag...)
}

// TestFatMachO: the universal binary reads back with Go's debug/macho, each slice the thin program byte for byte at a
// 16 KiB-aligned offset; two programs for one cpu are refused.
func TestFatMachO(t *testing.T) {
	x := thinMachO(macho.CpuAmd64, 3, "x86-64 program")
	a := thinMachO(macho.CpuArm64, 0, "arm64 program")
	fat, err := fatMachO(x, a)
	if err != nil {
		t.Fatal(err)
	}
	checkFat(t, fat, x, a)
	if _, err := fatMachO(x, x); err == nil {
		t.Error("two x86-64 programs accepted")
	}
}

func checkFat(t *testing.T, fat []byte, thin ...[]byte) {
	t.Helper()
	ff, err := macho.NewFatFile(bytes.NewReader(fat))
	if err != nil {
		t.Fatal(err)
	}
	if len(ff.Arches) != len(thin) {
		t.Fatalf("%d slices", len(ff.Arches))
	}
	for i, arch := range ff.Arches {
		f, _ := macho.NewFile(bytes.NewReader(thin[i]))
		if arch.Cpu != f.Cpu || arch.SubCpu != f.SubCpu || arch.Align != fatAlign || arch.Offset%(1<<fatAlign) != 0 {
			t.Errorf("slice %d: %+v", i, arch.FatArchHeader)
		}
		if !bytes.Equal(fat[arch.Offset:arch.Offset+arch.Size], thin[i]) {
			t.Errorf("slice %d differs from its program", i)
		}
	}
}

// TestFatMachOReal joins the real darwin builds (skipped with -short) and checks that the arm64 slice keeps the code
// signature load command (LC_CODE_SIGNATURE, 0x1d) the Go linker gives darwin/arm64 programs, which macOS needs to run
// it.
func TestFatMachOReal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two programs")
	}
	var thin [][]byte
	for _, arch := range []string{"amd64", "arm64"} {
		b, err := relbuild.Build("../..", "./cmd/yakbd", "darwin", arch, false, "")
		if err != nil {
			t.Fatal(err)
		}
		thin = append(thin, b)
	}
	fat, err := fatMachO(thin...)
	if err != nil {
		t.Fatal(err)
	}
	checkFat(t, fat, thin...)
	ff, _ := macho.NewFatFile(bytes.NewReader(fat))
	signed := false
	for _, l := range ff.Arches[1].Loads {
		if raw := l.Raw(); len(raw) >= 4 && binary.LittleEndian.Uint32(raw) == 0x1d {
			signed = true
		}
	}
	if !signed {
		t.Error("the arm64 slice has no code signature")
	}
}
