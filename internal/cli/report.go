package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
)

// The -report file (docs/report-file.md): what a program reads, so its shape is a public interface. Change it only
// with docs/report-file.md, and bump ReportFormat when a reader of the old shape would misread the new one.

// ReportFormat is the version of the -report file's shape.
const ReportFormat = 1

type reportJSON struct {
	Format  int         `json:"format"`
	Tool    toolJSON    `json:"tool"`
	Command string      `json:"command"`
	Input   *inputJSON  `json:"input,omitempty"`
	Entries []entryJSON `json:"entries"`
}

type toolJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type inputJSON struct {
	File   string `json:"file"`
	Format string `json:"format"` // kbl.json, klc, cldr or dll; "" when the extension is none of them
}

type entryJSON struct {
	Class    loss.Class     `json:"class"`
	Severity loss.Severity  `json:"severity"`
	Code     string         `json:"kind"`
	Where    whereJSON      `json:"where"`
	Message  string         `json:"message"`
	Detail   map[string]any `json:"detail,omitempty"`
}

// whereJSON is an entry's location. Pointer is set only for a place in the input document (a .kbl.json input);
// Path holds every other location, which never points into the input document.
type whereJSON struct {
	File    string  `json:"file,omitempty"`
	Line    int     `json:"line,omitempty"`
	Pointer *string `json:"pointer,omitempty"`
	// Missing: the input document has no member at Pointer (a default rule fills it, or the entry asks for it).
	Missing bool   `json:"missing,omitempty"`
	Path    string `json:"path,omitempty"`
}

// ToolVersion is what `yakbd version` prints after "yakbd ".
func ToolVersion() string {
	if Release != "" {
		return Release
	}
	return VersionString()
}

// inputFormat names a format for the report.
func inputFormat(path string) string {
	if forcedFormat.path == path && forcedFormat.name != "" {
		return formatByName(forcedFormat.name).report
	}
	if f := formatOf(path); f != "kbl" {
		return f
	}
	return "kbl.json"
}

// reportOf builds the -report file of a run of command on input (empty: none).
func reportOf(r *loss.Report, command, input string) *reportJSON {
	out := &reportJSON{Format: ReportFormat, Tool: toolJSON{Name: "yakbd", Version: ToolVersion()}, Command: command, Entries: []entryJSON{}}
	var doc any
	isKBL := false
	if input != "" {
		out.Input = &inputJSON{File: input, Format: inputFormat(input)}
		if isKBL = formatOf(input) == "kbl"; isKBL {
			doc = readDoc(input)
		}
	}
	for _, e := range r.Entries {
		w := whereJSON{File: e.Where.File, Line: e.Where.Line, Path: e.Where.Path}
		if isKBL && (strings.HasPrefix(e.Where.Path, "/") || e.Code == "kbl.schema" || e.Class == loss.Defaulted) {
			p := e.Where.Path
			w.Pointer, w.Path = &p, ""
			w.Missing = doc == nil || !resolves(doc, p)
		}
		out.Entries = append(out.Entries, entryJSON{Class: e.Class, Severity: e.Severity, Code: e.Code, Where: w, Message: e.Message, Detail: e.Detail})
	}
	return out
}

// readDoc reads a JSON document for resolving pointers; nil when it is not JSON.
func readDoc(path string) any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))))
	d.UseNumber()
	var doc any
	if d.Decode(&doc) != nil {
		return nil
	}
	return doc
}

// resolves reports whether the JSON pointer p (RFC 6901) names a value of doc.
func resolves(doc any, p string) bool {
	if p == "" {
		return true
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	cur := doc
	for _, tok := range strings.Split(p[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[tok]
			if !ok {
				return false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) || (len(tok) > 1 && tok[0] == '0') {
				return false
			}
			cur = v[i]
		default:
			return false
		}
	}
	return true
}
