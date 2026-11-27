// Package kbljson reads and writes `.kbl.json` files, the YAKBD canonical format (kbl-spec, format
// version 0.1). Phase 2 has started with validation: Validate checks a file against the embedded schema and the
// file rules of SPEC §1 and §2.1 that a schema cannot express. Decoding into the model package follows.
package kbljson

import (
	_ "embed"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/yakbd/yakbd-cli/jsonschema"
	"github.com/yakbd/yakbd-cli/loss"
)

// SchemaID is the `$id` of the schema this package implements.
const SchemaID = "https://yakbd.github.io/kbl-spec/schema/0.1/kbl.schema.json"

// SchemaCommit is the kbl-spec commit the embedded schema was copied from.
const SchemaCommit = "a264cf0adf04694f5cb175afe5d4428d1da18154"

//go:embed schema/kbl.schema.json
var schemaJSON []byte

var schema = mustCompile()

func mustCompile() *jsonschema.Schema {
	s, err := jsonschema.Compile(schemaJSON)
	if err != nil {
		panic(err)
	}
	return s
}

// Validate checks the bytes of a `.kbl.json` file: UTF-8 without a byte-order mark (§1), no lone surrogate escape
// (§2.1), and the schema (closed objects: unknown properties are errors, §1). Entries are of kind Diagnostic with
// codes "kbl.*"; the path is a JSON Pointer.
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
	for _, e := range schema.Validate(doc) {
		r.Errorf("kbl.schema", w(e.Path), "%s", e.Message)
	}
	return r
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
