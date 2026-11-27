package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleReadme(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "b")
	if code := run([]string{"-o", dir, "-repo", "../.."}, io.Discard, false); code != 0 {
		t.Fatal(code)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "README.txt"))
	if !strings.Contains(string(b), "yakbd-wincheck-64.exe -import -yakbd") {
		t.Error("README without the guest command")
	}
	if code := run([]string{"-o", dir}, io.Discard, false); code != 2 {
		t.Error("an existing folder was reused")
	}
}
