// Command msklcbundle writes the folder for the MSKLC comparison on Windows (docs/dll-import.md §6): the same
// .klc files built by MSKLC 1.4's compiler and by yakbd inside a disposable guest, both DLL sets typed against
// the same expectations, and every MSKLC DLL imported by yakbd and compared with the import of its .klc. Run it
// from the repository root:
//
//	go run ./tools/msklcbundle -o /path/to/bundle
//
// The bundle holds our own files only: the Windows builds of yakbd, yakbd-wincheck and the guest driver
// (tools/msklcbundle/guest), the .klc inputs and a README. MSKLC itself is not part of it, and nothing MSKLC
// produces leaves the guest except the text the guest driver writes.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/internal/cli"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, true)) }

// kase is one input of the bundle.
type kase struct {
	name, langID, source, note string
	text                       []byte // the .klc as UTF-16LE with byte-order mark and CRLF
}

// notes are what the reference specification's MSKLC observations predict for the corpus inputs (OBS = its
// MSKLC observations folder). A case without a note is expected to build and type the same with both compilers.
var notes = map[string]string{
	"altgr-012-12":          "D26 KLLF_ALTGR set for highest state 12; yakbd refuses (state 12 needs a MODIFIERS bit)",
	"altgr-012-5":           "D26 no KLLF_ALTGR for highest state 5",
	"altgr-0123":            "D26 no KLLF_ALTGR for highest state 3",
	"altgr-0124":            "D26 no KLLF_ALTGR for highest state 4",
	"altgr-0126":            "D26 KLLF_ALTGR for highest state 6",
	"deadkey-order":         "D17 a repeated (dead, base) pair is written twice; yakbd refuses the duplicate",
	"e0-newvk":              "D23 E0 table: named entries first, then the defaults",
	"e0-same":               "D23",
	"e0-two":                "D23",
	"e0-unknown":            "D23 a 39th E0 entry; kbdutool warning 1009",
	"e0-used":               "D23; D16 MSKLC writes the DIVIDE row although no key gives DIVIDE (an extra row of the .klc import too)",
	"groupshift-build":      "D21 kbdutool builds nothing; yakbd refuses GROUPSHIFT",
	"groupshift":            "D21 sources-only input; yakbd refuses GROUPSHIFT",
	"mod-kana":              "D29 the amd64 build may not load on Windows 11 (E1 table at KBDTABLES+60); D26 KLLF_ALTGR set (state 8)",
	"mod-number":            "D12, D29; yakbd refuses the numeric bit name",
	"mod-roya-first":        "D12 VK_TO_BIT and ModNumber disagree (column unreachable), D29; yakbd refuses",
	"mod-two":               "D12, D29; yakbd refuses",
	"sgcaps-dead-both":      "D15 SGCAPS with dead keys: no second row; yakbd refuses (windows.sgcapsDeadKey)",
	"sgcaps-dead-cont":      "K14, D81: @ in the continuation line is the character for MSKLC and (since D81) for yakbd",
	"sgcaps-dead-cont2":     "K14, D81 as sgcaps-dead-cont",
	"sgcaps-dead-cont3":     "K13 a lone @ cell; yakbd refuses the cell",
	"sgcaps-dead-cont4":     "K13; yakbd refuses the cell",
	"sgcaps-dead-main":      "D15 Caps Lock characters lost or moved into the FF row; yakbd refuses",
	"sgcaps-dead-shift":     "D15; yakbd refuses",
	"sgcaps-plain":          "D14 the Caps Lock row's other columns hold 0000",
	"wbt1":                  "W3 the supplementary character's ligature carries the shift-state value: AltGr+T may give nothing",
	"tmcaps":                "D14 Caps Lock rows filled with 0000 where the file gives no character; -1 in a continuation line is WCH_NONE (Caps Lock gives nothing)",
	"tmkana":                "D29 MODIFIERS: the amd64 build may not load on Windows 11; D26 KLLF_ALTGR set (state 8)",
	"tmroya":                "D29 MODIFIERS: the amd64 build may not load on Windows 11; D26 KLLF_ALTGR set (state 8)",
	"tmlig":                 "W2 LIGATURE columns are copied as written (column indexes here)",
	"x-ja-Hira-t-k0-flicks": "exported from CLDR by yakbd",
	"x-fr-t-k0-test":        "exported from CLDR by yakbd",
}

func run(args []string, stdout, stderr io.Writer, buildExes bool) int {
	fs := flag.NewFlagSet("msklcbundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "msklcbundle", "output folder (created; must not exist)")
	repo := fs.String("repo", ".", "the repository root")
	exes := fs.Bool("exes", true, "build the Windows programs (false: cases and README only, for the host dry run)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintf(stderr, "msklcbundle: %s exists; choose a new folder\n", *out)
		return 2
	}
	if err := os.MkdirAll(filepath.Join(*out, "cases"), 0o755); err != nil {
		fmt.Fprintln(stderr, "msklcbundle:", err)
		return 2
	}
	rev, _ := exec.Command("git", "-C", *repo, "rev-parse", "--short", "HEAD").Output()
	built := strings.TrimSpace(string(rev))
	if dirty, _ := exec.Command("git", "-C", *repo, "status", "--porcelain").Output(); len(dirty) > 0 {
		built += "+changes"
	}
	if buildExes && *exes {
		abs, _ := filepath.Abs(*out)
		for _, t := range []struct{ arch, pkg, file, ldflags string }{
			{"amd64", "./cmd/yakbd", "yakbd.exe", "-X main.builtFrom=" + built},
			{"amd64", "./cmd/yakbd-wincheck", "yakbd-wincheck-64.exe", "-X main.builtFrom=" + built},
			{"386", "./cmd/yakbd-wincheck", "yakbd-wincheck-32.exe", "-X main.builtFrom=" + built},
			{"amd64", "./tools/msklcbundle/guest", "tm-guest.exe", "-X main.builtFrom=" + built},
		} {
			cmd := exec.Command("go", "build", "-trimpath", "-ldflags", t.ldflags, "-o", filepath.Join(abs, t.file), t.pkg)
			cmd.Dir = *repo
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+t.arch, "CGO_ENABLED=0")
			cmd.Stdout, cmd.Stderr = stderr, stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintln(stderr, "msklcbundle:", err)
				return 2
			}
		}
	}
	cases, skipped, err := collect(*repo)
	if err != nil {
		fmt.Fprintln(stderr, "msklcbundle:", err)
		return 2
	}
	if err := write(*out, cases, built); err != nil {
		fmt.Fprintln(stderr, "msklcbundle:", err)
		return 2
	}
	os.WriteFile(filepath.Join(*out, "README.txt"), []byte(readme(len(cases), skipped)), 0o644)
	fmt.Fprintf(stdout, "%s: %d cases (built from %s)\n", *out, len(cases), built)
	return 0
}

// collect gathers the cases: the tool's .klc corpus, the targeted cases of tools/msklcbundle/cases, and the
// corpus's .kbl.json and CLDR layouts exported to .klc by yakbd (convert -msklc).
func collect(repo string) ([]kase, []string, error) {
	var out []kase
	var skipped []string
	seen := map[string]bool{}
	add := func(name, source string, b []byte) error {
		if seen[name] {
			return fmt.Errorf("two cases named %s", name)
		}
		seen[name] = true
		text := klcText(b)
		out = append(out, kase{name: name, langID: langID(text), source: source, note: notes[name], text: text})
		return nil
	}
	for _, g := range []string{"testdata/reference-spec/*/*.klc", "tools/msklcbundle/cases/*.klc"} {
		files, _ := filepath.Glob(filepath.Join(repo, g))
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, nil, err
			}
			rel, _ := filepath.Rel(repo, f)
			if err := add(strings.TrimSuffix(filepath.Base(f), ".klc"), filepath.ToSlash(rel), b); err != nil {
				return nil, nil, err
			}
		}
	}
	tmp, err := os.MkdirTemp("", "msklcbundle")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	for _, g := range []string{"testdata/kbl-spec/examples/*.kbl.json", "testdata/third_party/cldr-release-49/keyboards/3.0/*.xml"} {
		files, _ := filepath.Glob(filepath.Join(repo, g))
		sort.Strings(files)
		for _, f := range files {
			base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(f), ".kbl.json"), ".xml")
			dst := filepath.Join(tmp, base+".klc")
			var o, e bytes.Buffer
			rel, _ := filepath.Rel(repo, f)
			if cli.Run([]string{"convert", "-q", "-msklc", f, dst}, &o, &e) != 0 {
				skipped = append(skipped, fmt.Sprintf("%s: yakbd does not export it to .klc (%s)", filepath.ToSlash(rel), firstError(e.String())))
				continue
			}
			b, err := os.ReadFile(dst)
			if err != nil {
				return nil, nil, err
			}
			// An export that yakbd's own .klc reader refuses is no input for the comparison.
			o.Reset()
			e.Reset()
			if cli.Run([]string{"convert", "-q", dst, filepath.Join(tmp, base+".kbl.json")}, &o, &e) != 0 {
				skipped = append(skipped, fmt.Sprintf("%s: yakbd's .klc export of it does not read back (%s)", filepath.ToSlash(rel), firstError(e.String())))
				continue
			}
			if err := add("x-"+base, filepath.ToSlash(rel)+" (exported by yakbd convert -msklc)", b); err != nil {
				return nil, nil, err
			}
			out[len(out)-1].note = "extra set: yakbd's own .klc export (convert -msklc); MSKLC must build it as yakbd does"
		}
	}
	return out, skipped, nil
}

func firstError(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "error") {
			if i := strings.LastIndex(l, "["); i >= 0 {
				return strings.Trim(l[i:], "[]")
			}
			return l
		}
	}
	return strings.TrimSpace(s)
}

// klcText returns a .klc file as kbdutool reads it: UTF-16LE with byte-order mark and CRLF line ends (the
// reference corpus is kept as UTF-8 with LF, MSKLC observations tests/dll-format/README.md). A file that already
// is UTF-16LE is kept as it is.
func klcText(b []byte) []byte {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return b
	}
	s := strings.TrimPrefix(string(b), "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	u := utf16.Encode([]rune(s))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

var localeID = regexp.MustCompile(`(?m)^LOCALEID\s+"?[0-9A-Fa-f]{4}([0-9A-Fa-f]{4})"?`)

// langID is the low word of the file's LOCALEID, else 0409.
func langID(text []byte) string {
	var u []uint16
	for i := 2; i+1 < len(text); i += 2 {
		u = append(u, uint16(text[i])|uint16(text[i+1])<<8)
	}
	if m := localeID.FindStringSubmatch(string(utf16.Decode(u))); m != nil {
		return strings.ToLower(m[1])
	}
	return "0409"
}

func write(dir string, cases []kase, built string) error {
	var list strings.Builder
	list.WriteString("# name\tLANGID\tsource\texpected (reference specification, MSKLC observations)\n")
	for _, c := range cases {
		if err := os.WriteFile(filepath.Join(dir, "cases", c.name+".klc"), c.text, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(&list, "%s\t%s\t%s\t%s\n", c.name, c.langID, c.source, c.note)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.txt"), []byte(list.String()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "BUILT-FROM.txt"), []byte("yakbd/yakbd-cli "+built+
		"\nmade by: go run ./tools/msklcbundle\n"), 0o644)
}
