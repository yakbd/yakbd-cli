package cli

import (
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/convert/dllkbl"
	"github.com/yakbd/yakbd-cli/convert/keylayoutkbl"
	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/cldr"
	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/keylayout"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
	macbuild "github.com/yakbd/yakbd-cli/target/macos/build"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// fileFormat is one file format the tool reads (every command's input) or writes (convert's output). A native
// format of a new target is one more row of formats (docs/targets.md).
type fileFormat struct {
	name   string // internal name: what formatOf returns
	report string // input.format of the -report file (docs/report-file.md §2)
	ext    string // the file name suffix, lower case
	// read reads a file of the format into the model, with the checks of SPEC §19 (Layout.Check) for every format
	// other than .kbl.json, whose reader runs them itself. nil: the format is not read.
	read func(data []byte, name string) (*model.Layout, *loss.Report)
	// write writes a layout in the format, its losses resolved under the file's policies and extra. nil: the
	// format is not written.
	write func(l *model.Layout, o writeOptions) ([]byte, *loss.Report)
}

// writeOptions are convert's options of the output.
type writeOptions struct {
	extra *model.OnLoss // the command line's policy
	msklc bool          // -msklc
}

var formats = []fileFormat{
	{name: "kbl", report: "kbl.json", ext: ".kbl.json", read: readKBL, write: writeKBL},
	{name: "klc", report: "klc", ext: ".klc", read: readKLC, write: writeKLC},
	{name: "cldr", report: "cldr", ext: ".xml", read: readCLDR, write: writeCLDR},
	{name: "dll", report: "dll", ext: ".dll", read: readDLL},
	{name: "keylayout", report: "keylayout", ext: ".keylayout", read: readKeylayout, write: writeKeylayout},
}

// formatByPath returns the format of a file by its name's suffix.
func formatByPath(path string) *fileFormat {
	p := strings.ToLower(path)
	for i := range formats {
		if strings.HasSuffix(p, formats[i].ext) {
			return &formats[i]
		}
	}
	return nil
}

// formatOf returns the internal name of a file's format by extension, "" for none.
func formatOf(path string) string {
	if f := formatByPath(path); f != nil {
		return f.name
	}
	return ""
}

// extList lists the suffixes of the formats that can (read) or (write): ".kbl.json, .klc or .xml".
func extList(write bool) string {
	var s []string
	for _, f := range formats {
		if (write && f.write != nil) || (!write && f.read != nil) {
			s = append(s, f.ext)
		}
	}
	if len(s) < 2 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + " or " + s[len(s)-1]
}

func readKBL(b []byte, name string) (*model.Layout, *loss.Report) { return kbljson.Read(b, name) }

func readKLC(b []byte, name string) (*model.Layout, *loss.Report) {
	f, r := klc.Parse(b, name)
	if f == nil || r.HasErrors() {
		return nil, r
	}
	l, ir := klckbl.FromKLC(f, name)
	r.Merge(ir)
	checked(l, r)
	return l, r
}

func readCLDR(b []byte, name string) (*model.Layout, *loss.Report) {
	l, r := cldr.Read(b, name)
	checked(l, r)
	return l, r
}

func readDLL(b []byte, name string) (*model.Layout, *loss.Report) {
	l, r := dllkbl.FromDLL(b, name, importOptions)
	if l == nil || r.HasErrors() {
		return nil, r
	}
	checked(l, r)
	return l, r
}

// checked adds the checks of SPEC §19 of a layout read from a format other than .kbl.json.
func checked(l *model.Layout, r *loss.Report) {
	if l != nil {
		r.Merge(l.Check())
	}
}

func writeKBL(l *model.Layout, o writeOptions) ([]byte, *loss.Report) {
	data, r := kbljson.Write(l)
	policy.Resolve(r, l, "", o.extra)
	return data, r
}

func writeKLC(l *model.Layout, o writeOptions) ([]byte, *loss.Report) {
	f, wr := klckbl.ToKLC(l, build.Options{ToolVersion: Version})
	var data []byte
	if f != nil {
		msklc := o.msklc
		if t := l.Targets; t != nil && t.KLC != nil && t.KLC.MSKLCEditorCompatible != nil {
			msklc = msklc || *t.KLC.MSKLCEditorCompatible
		}
		var kr *loss.Report
		data, kr = klc.Write(f, klc.WriteOptions{ForMSKLC: msklc})
		for i := range kr.Entries {
			if strings.HasPrefix(kr.Entries[i].Code, "klc.msklc.") {
				kr.Entries[i].Class, kr.Entries[i].Code = loss.Loss, "klc.editorDropped"
			}
		}
		wr.Merge(kr)
	}
	policy.Resolve(wr, l, "klc", klckbl.ExportPolicy(o.extra))
	return data, wr
}

func writeCLDR(l *model.Layout, o writeOptions) ([]byte, *loss.Report) {
	data, r := cldr.Write(l)
	policy.Resolve(r, l, "cldr", o.extra)
	return data, r
}

func readKeylayout(b []byte, name string) (*model.Layout, *loss.Report) {
	k, r := keylayout.Read(b, name)
	if k == nil || r.HasErrors() {
		return nil, r
	}
	l, ir := keylayoutkbl.FromKeylayout(k, filepath.Base(name), "")
	r.Merge(ir)
	checked(l, r)
	return l, r
}

func writeKeylayout(l *model.Layout, o writeOptions) ([]byte, *loss.Report) {
	return keylayoutkbl.ToKeylayout(l, macbuild.Options{Policy: o.extra})
}
