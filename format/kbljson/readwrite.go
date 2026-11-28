package kbljson

import (
	"bytes"
	"encoding/json"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// Read validates a `.kbl.json` file and decodes it into the model. The layout is nil when the report has errors.
func Read(data []byte, name string) (*model.Layout, *loss.Report) {
	r := Validate(data, name)
	if r.HasErrors() {
		return nil, r
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var l model.Layout
	if err := d.Decode(&l); err != nil {
		r.Errorf("kbl.decode", loss.Where{File: name}, "%v", err)
		return nil, r
	}
	// The rules of SPEC §19 that the schema cannot check.
	r.Merge(l.Check())
	if r.HasErrors() {
		return nil, r
	}
	return &l, r
}

// Write encodes a layout as `.kbl.json`: UTF-8, two-space indentation, object properties in a fixed order (struct
// fields in schema order, map keys sorted), no HTML escaping, a final newline. The output is validated; a
// layout the schema refuses gives a report with errors and no bytes. The version written is the lowest that holds
// the layout (SPEC §1.1), or the layout's own `kbl` when that is higher (a file that names 0.2 stays 0.2); raising
// it changes `kbl` and a `$schema` of an implemented version, nothing else.
func Write(l *model.Layout) ([]byte, *loss.Report) {
	if l.Kbl == "" {
		l.Kbl = model.FormatVersion
	}
	if need := model.MinVersion(l); versionIndex(l.Kbl) < versionIndex(need) {
		if old := VersionOf(l.Kbl); old != nil && l.Schema == old.SchemaID {
			l.Schema = VersionOf(need).SchemaID
		}
		l.Kbl = need
	}
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	r := &loss.Report{}
	if err := e.Encode(l); err != nil {
		r.Errorf("kbl.encode", loss.Where{}, "%v", err)
		return nil, r
	}
	out := buf.Bytes()
	vr := Validate(out, "(written)")
	if vr.HasErrors() {
		return nil, vr
	}
	return out, r
}

// versionIndex is the place of kbl in Versions; -1 for a version this package does not implement.
func versionIndex(kbl string) int {
	for i, v := range Versions {
		if v.Kbl == kbl {
			return i
		}
	}
	return -1
}
