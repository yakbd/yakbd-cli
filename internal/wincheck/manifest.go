package wincheck

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Entry is one layout of a test bundle.
type Entry struct {
	Name   string
	DLL    map[string]string // column ("x64", "wow64", "x86", "arm64") -> path relative to the bundle
	Expect string            // path of the expectations
	// Rebase: the runner occupies the DLL's preferred ImageBase before loading it, so that Windows must rebase
	// it (test VM-1).
	Rebase bool
	Note   string // what the entry tests
}

// Builds are the DLL columns of a manifest, in order: the System32 file of x64 Windows, the SysWOW64 file, the
// System32 file of 32-bit Windows, and the System32 file of ARM64 Windows (an ARM64X or a plain ARM64 build), which
// both the native ARM64 runner and the x64 runner on ARM64 load.
var Builds = []string{"x64", "wow64", "x86", "arm64"}

// Column returns the manifest column a runner of the given build loads: "arm64" for the native ARM64 runner and
// for the x64 runner on ARM64 Windows ("x64-on-arm64"), the build itself otherwise.
func Column(build string) string {
	if build == "arm64" || build == "x64-on-arm64" {
		return "arm64"
	}
	return build
}

// WriteManifest writes the manifest: a header, then one tab-separated line per entry: name, the four DLL paths
// of Builds ("-" when missing), the expectations path, the options, the note.
func WriteManifest(w io.Writer, entries []Entry) error {
	fmt.Fprintln(w, "# yakbd-wincheck manifest 2: name, x64, wow64, x86, arm64, expectations, options, note")
	for _, e := range entries {
		cols := []string{e.Name}
		for _, b := range Builds {
			p := e.DLL[b]
			if p == "" {
				p = "-"
			}
			cols = append(cols, p)
		}
		opt := "-"
		if e.Rebase {
			opt = "rebase"
		}
		cols = append(cols, e.Expect, opt, e.Note)
		if _, err := fmt.Fprintln(w, strings.Join(cols, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// ReadManifest reads a manifest of format 2, or of format 1 (no arm64 column).
func ReadManifest(r io.Reader) ([]Entry, error) {
	var out []Entry
	sc := bufio.NewScanner(r)
	no := 0
	cols := Builds
	for sc.Scan() {
		no++
		t := sc.Text()
		if strings.HasPrefix(t, "# yakbd-wincheck manifest 1:") {
			cols = Builds[:3]
		}
		if strings.TrimSpace(t) == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Split(t, "\t")
		n := len(cols)
		if len(f) < n+3 {
			return nil, fmt.Errorf("manifest line %d: %d columns", no, len(f))
		}
		e := Entry{Name: f[0], DLL: map[string]string{}, Expect: f[n+1], Rebase: f[n+2] == "rebase"}
		for i, b := range cols {
			if f[1+i] != "-" {
				e.DLL[b] = f[1+i]
			}
		}
		if len(f) > n+3 {
			e.Note = f[n+3]
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Slot is where the runner puts one layout on the guest: a file name in the system directory, a KLID and a
// Layout Id of its own, so that no two loads share an HKL (dll-format tests/vm README, "Traps").
type Slot struct {
	File     string
	KLID     string
	LayoutID uint16
}

// SlotFor returns the slot of entry i for a runner ("64", "32" or "a64"). The 64-bit runner uses KLIDs a1NN0409
// and Layout Ids 0A00+i, the 32-bit runner a2NN0409 and 0C00+i, the native ARM64 runner a3NN0409 and 0E00+i, so
// the runs of one guest never share a file, a KLID or an HKL; ids above 00ff are those Windows and other tools do
// not use (installation §3.3).
func SlotFor(runner string, i int) Slot {
	base, hi := uint16(0x0A00), 0x1
	switch runner {
	case "32":
		base, hi = 0x0C00, 0x2
	case "a64":
		base, hi = 0x0E00, 0x3
	case "s":
		base, hi = 0x0D00, 0x4 // the sign-in test: one persistent registration per layout for every process kind
	}
	return Slot{
		File:     fmt.Sprintf("yk%s%03d.dll", runner, i),
		KLID:     fmt.Sprintf("a%x%02x0409", hi, i&0xFF),
		LayoutID: base + uint16(i&0x1FF),
	}
}

// Outcome is the result of one layout in one runner.
type Outcome struct {
	Name  string
	Err   string // load or setup failure
	Diffs []Diff
	Lines int
	Notes []string // remarks that do not fail the layout (cleanup, rebase)
}

// Pass reports a layout without failure.
func (o Outcome) Pass() bool { return o.Err == "" && len(o.Diffs) == 0 }

// Summary returns the one-line result: "PASS name" or "FAIL name: ..." with at most three differences.
func (o Outcome) Summary() string {
	if o.Pass() {
		s := fmt.Sprintf("PASS %s (%d lines)", o.Name, o.Lines)
		if o.Lines == 0 {
			s = fmt.Sprintf("PASS %s (load only: no key to type)", o.Name)
		}
		if len(o.Notes) > 0 {
			s += " [" + strings.Join(o.Notes, "; ") + "]"
		}
		return s
	}
	if o.Err != "" {
		return fmt.Sprintf("FAIL %s: %s", o.Name, o.Err)
	}
	var parts []string
	for i, d := range o.Diffs {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("... %d more", len(o.Diffs)-3))
			break
		}
		parts = append(parts, d.String())
	}
	return fmt.Sprintf("FAIL %s: %s", o.Name, strings.Join(parts, "; "))
}

// Report writes the summary lines and the final count to short, and every difference to full.
func Report(short, full io.Writer, runner string, outs []Outcome) (passed, failed int) {
	for _, o := range outs {
		fmt.Fprintln(short, o.Summary())
		if o.Pass() {
			passed++
			continue
		}
		failed++
		fmt.Fprintf(full, "== %s (%s-bit runner)\n", o.Name, runner)
		if o.Err != "" {
			fmt.Fprintln(full, o.Err)
		}
		for _, d := range o.Diffs {
			fmt.Fprintln(full, d.String())
		}
	}
	fmt.Fprintf(short, "%s-bit runner: %d passed, %d failed\n", runner, passed, failed)
	return passed, failed
}

// ControlExpectations are the keys of the runners' known-good control (yakbd-wincheck -control): what Windows'
// own US layout types for them with Caps Lock off. tools/vmbundle writes them as control/us.expect.txt.
const ControlExpectations = `# yakbd control: Windows' US layout (kbdus.dll), counts only in the results
key 41 -> chars 0061
key 41+10 -> chars 0041
key 31 -> chars 0031
key 31+10 -> chars 0021
key 20 -> chars 0020
key be -> chars 002e
`
