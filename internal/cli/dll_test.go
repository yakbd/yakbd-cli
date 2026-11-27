package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yakbd/yakbd-cli/target/windows"
)

// TestDLLCommands: convert reads a DLL (flags after the arguments, -o, -langid), its .kbl.json and .klc build
// the same tables, and a file that is no DLL is refused with dll.malformed.
func TestDLLCommands(t *testing.T) {
	dir := t.TempDir()
	if code, _, e := run("compile", "-o", dir, kbdtst); code != ExitOK {
		t.Fatal(e)
	}
	for _, a := range []string{"x64", "wow64", "x86"} {
		in := filepath.Join(dir, a, "kbdtst.dll")
		out := filepath.Join(dir, a, "kbdtst.kbl.json")
		if code, _, e := run("convert", in, "-o", out, "-langid", "0409"); code != ExitOK || !strings.Contains(e, "dll.registrationMissing") {
			t.Fatalf("%s: convert: %d %s", a, code, e)
		}
		if code, _, e := run("convert", in, filepath.Join(dir, a, "kbdtst.klc")); code != ExitOK {
			t.Fatalf("%s: convert to .klc: %s", a, e)
		}
		for _, src := range []string{out, filepath.Join(dir, a, "kbdtst.klc")} {
			re := filepath.Join(dir, a, "re-"+filepath.Base(src))
			if code, _, e := run("compile", "-o", re, "-langid", "0409", "-target", "x64", src); code != ExitOK {
				t.Fatalf("%s: compile %s: %s", a, src, e)
			}
			orig, _ := os.ReadFile(in)
			rebuilt, _ := os.ReadFile(filepath.Join(re, "x64", "kbdtst.dll"))
			x, _, err1 := windows.Read(orig)
			y, _, err2 := windows.Read(rebuilt)
			if err1 != nil || err2 != nil {
				t.Fatal(err1, err2)
			}
			if d := windows.Differences(&x.Tables, &y.Tables); len(d) > 0 {
				t.Errorf("%s via %s: %v", a, filepath.Base(src), d)
			}
		}
	}
	bad := filepath.Join(dir, "bad.dll")
	os.WriteFile(bad, bytes.Repeat([]byte("MZ"), 100), 0o644)
	if code, _, e := run("convert", bad, filepath.Join(dir, "bad.kbl.json")); code != ExitError || !strings.Contains(e, "dll.malformed") {
		t.Errorf("bad DLL: %d %s", code, e)
	}
}
