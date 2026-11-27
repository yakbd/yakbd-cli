package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundle(t *testing.T) {
	out := filepath.Join(t.TempDir(), "b")
	var o, e bytes.Buffer
	if code := run([]string{"-o", out, "-repo", "../.."}, &o, &e, false); code != 0 {
		t.Fatalf("exit %d: %s", code, e.String())
	}
	list, _ := os.ReadFile(filepath.Join(out, "cases.txt"))
	lines := strings.Split(strings.TrimSpace(string(list)), "\n")
	if len(lines) < 1+46+7 {
		t.Errorf("%d cases", len(lines)-1)
	}
	if !strings.Contains(string(list), "tmaltgr\t0407\t") || !strings.Contains(string(list), "mod-kana\t0409\t") {
		t.Errorf("LANGIDs:\n%s", list)
	}
	for _, l := range lines[1:] {
		name := strings.SplitN(l, "\t", 2)[0]
		b, err := os.ReadFile(filepath.Join(out, "cases", name+".klc"))
		if err != nil || len(b) < 4 || b[0] != 0xFF || b[1] != 0xFE || bytes.Contains(b, []byte{'\n', 0, 'K'}) && !bytes.Contains(b, []byte{'\r', 0, '\n', 0}) {
			t.Errorf("%s: not UTF-16LE with CRLF", name)
		}
	}
	for _, f := range []string{"README.txt", "BUILT-FROM.txt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Error(err)
		}
	}
}
