package main

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestCheckVersion(t *testing.T) {
	for v, ok := range map[string]bool{
		"0.1.0-preview.1": true, "0.1.0": false, "0.1.0-preview.2": false, "0.0.0-rehearsal.1": true, "9.9.9-rehearsal.2": true,
		"0.3.0": false, "v0.1.0-preview.1": false, "0.2": false, "0.1.0+build": false, "0.1.0-": false, "01.2.0": false,
	} {
		if err := checkVersion(v); (err == nil) != ok {
			t.Errorf("%s: %v", v, err)
		}
	}
}

// TestZipFixed checks that a zip does not depend on when it is written and that it reads back.
func TestZipFixed(t *testing.T) {
	files := [][2]any{{"yakbd.exe", []byte("MZ program")}, {"LICENSE", []byte("text")}}
	a, err := zipFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := zipFiles(files)
	if !bytes.Equal(a, b) {
		t.Error("two zips of the same files differ")
	}
	r, err := zip.NewReader(bytes.NewReader(a), int64(len(a)))
	if err != nil || len(r.File) != 2 || r.File[0].Name != "yakbd.exe" || !r.File[0].Modified.Equal(zipTime) {
		t.Fatalf("%v %+v", err, r)
	}
}
