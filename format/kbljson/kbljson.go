// Package kbljson reads and writes `.kbl.json` files, the YAKBD canonical format (kbl-spec). It reads every format
// version it implements (0.1, 0.2 and 0.3, SPEC §1.1): a file is checked against the schema of the version its `kbl`
// names, and a `kbl` it does not implement is an error (D3, D108).
package kbljson

import (
	"embed"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/yakbd/yakbd-cli/jsonschema"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// Version is one format version this package implements.
type Version struct {
	Kbl string // the `kbl` value
	// SchemaID is the `$id` of its schema.
	SchemaID string
	// SchemaCommit is the kbl-spec commit the embedded schema was copied from.
	SchemaCommit string
	schemaJSON   []byte
	schema       *jsonschema.Schema
}

// Versions are the format versions this package reads, oldest first; their schemas are embedded unchanged from
// kbl-spec `schema/<version>/kbl.schema.json`.
var Versions = []*Version{
	{Kbl: "0.1", SchemaID: "https://yakbd.github.io/kbl-spec/schema/0.1/kbl.schema.json",
		SchemaCommit: "a264cf0adf04694f5cb175afe5d4428d1da18154"},
	{Kbl: "0.2", SchemaID: "https://yakbd.github.io/kbl-spec/schema/0.2/kbl.schema.json",
		SchemaCommit: "886856bc1c2cbf2c9dcc5796a1b2aaa6bf42516d"}, // branch spec-0.2, a draft (COMPATIBILITY.md)
	{Kbl: "0.3", SchemaID: "https://yakbd.github.io/kbl-spec/schema/0.3/kbl.schema.json",
		SchemaCommit: "be71647f5a3edd935bb827fecf629e0ea88a1234"}, // branch spec-0.3, a draft (COMPATIBILITY.md)
}

// SchemaID is the `$id` of the 0.1 schema.
var SchemaID = Versions[0].SchemaID

// SchemaCommit is the kbl-spec commit the 0.1 schema was copied from.
var SchemaCommit = Versions[0].SchemaCommit

//go:embed schema
var schemaFiles embed.FS

func init() {
	for _, v := range Versions {
		b, err := schemaFiles.ReadFile("schema/" + v.Kbl + "/kbl.schema.json")
		if err != nil {
			panic(err)
		}
		if v.schema, err = jsonschema.Compile(b); err != nil {
			panic(err)
		}
		v.schemaJSON = b
	}
	if Versions[0].Kbl != model.FormatVersion {
		panic("kbljson: the oldest version is not model.FormatVersion")
	}
}

// VersionOf returns the implemented version whose `kbl` value is kbl, nil for none.
func VersionOf(kbl string) *Version {
	for _, v := range Versions {
		if v.Kbl == kbl {
			return v
		}
	}
	return nil
}

// Validate checks the bytes of a `.kbl.json` file: UTF-8 without a byte-order mark (§1), no lone surrogate escape
// (§2.1), and the schema of the version the file names (closed objects: unknown properties are errors, §1, §1.1).
// A file without a `kbl` string is checked with the oldest schema, which reports it. Entries are of kind
// Diagnostic with codes "kbl.*"; the path is a JSON Pointer.
func Validate(data []byte, name string) *loss.Report {
	r := &loss.Report{}
	w := func(path string) loss.Where { return loss.Where{File: name, Path: path} }
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		r.Errorf("kbl.bom", w(""), "a .kbl.json file is UTF-8 without a byte-order mark")
		data = data[3:]
	}
	if !utf8.Valid(data) {
		r.Errorf("kbl.utf8", w(""), "the file is not valid UTF-8")
		return r
	}
	if off, ok := loneSurrogate(data); ok {
		r.Errorf("kbl.loneSurrogate", w(""), "byte %d: a lone surrogate escape; use CodeUnits for such a value (§2.1, §2.2)", off)
	}
	doc, err := jsonschema.Decode(data)
	if err != nil {
		r.Errorf("kbl.json", w(""), "%v", err)
		return r
	}
	v := Versions[0]
	if obj, ok := doc.(map[string]any); ok {
		if k, ok := obj["kbl"].(string); ok {
			if v = VersionOf(k); v == nil {
				r.Errorf("kbl.schema", w("/kbl"), "format version %q is not one this tool reads (%s); a newer yakbd may read it", k, versionList())
				return r
			}
		}
	}
	for _, e := range v.schema.Validate(doc) {
		r.Errorf("kbl.schema", w(e.Path), "%s", e.Message)
	}
	return r
}

func versionList() string {
	s := ""
	for i, v := range Versions {
		if i > 0 {
			s += ", "
		}
		s += v.Kbl
	}
	return s
}

// loneSurrogate finds a \uD800-\uDFFF escape inside a JSON string that is not part of a high-low pair.
func loneSurrogate(b []byte) (int, bool) {
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 < len(b) && b[i+1] == 'u' && i+5 < len(b) {
				u, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
				if err == nil && u >= 0xD800 && u <= 0xDFFF {
					if u >= 0xDC00 {
						return i, true
					}
					// high surrogate: a low surrogate escape must follow
					if i+11 >= len(b) || b[i+6] != '\\' || b[i+7] != 'u' {
						return i, true
					}
					lo, err := strconv.ParseUint(string(b[i+8:i+12]), 16, 16)
					if err != nil || lo < 0xDC00 || lo > 0xDFFF {
						return i, true
					}
					i += 11
					continue
				}
				i += 5
				continue
			}
			i++ // skip the escaped character
		}
	}
	return 0, false
}

// ErrNotImplemented is returned by the parts of phase 2 that do not exist yet.
var ErrNotImplemented = fmt.Errorf("kbljson: decoding into the model is not implemented yet")
