package cldr

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/model"
)

// KBLNamespace is the namespace of the <special> element that carries what CLDR elements cannot hold
// (coverage/cldr-keyboard3.md §4).
const KBLNamespace = "https://yakbd.github.io/kbl-spec/ns/0.1"

// kblSpecial is the content of that element: top-level properties of the .kbl.json file, by name, with the value
// the file had (JSON null: the property was absent). Read replaces those properties after reading the CLDR
// elements, so a file written by Write reads back with the same meaning.
type kblSpecial struct {
	replace map[string]json.RawMessage
}

// parseKBLSpecial recognises a <special> element holding <kbl:replace> in the kbl namespace.
func parseKBLSpecial(raw string) (*kblSpecial, bool) {
	d := xml.NewDecoder(strings.NewReader(raw))
	inReplace := false
	var text bytes.Buffer
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == KBLNamespace && t.Name.Local == "replace" {
				inReplace = true
			}
		case xml.EndElement:
			if t.Name.Space == KBLNamespace && t.Name.Local == "replace" {
				var m map[string]json.RawMessage
				if json.Unmarshal(text.Bytes(), &m) != nil {
					return nil, false
				}
				return &kblSpecial{replace: m}, true
			}
		case xml.CharData:
			if inReplace {
				text.Write(t)
			}
		}
	}
	return nil, false
}

// apply replaces the carried properties of l.
func (s *kblSpecial) apply(l *model.Layout) {
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return
	}
	for k, v := range s.replace {
		if string(v) == "null" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, err = json.Marshal(m)
	if err != nil {
		return
	}
	var out model.Layout
	if json.Unmarshal(b, &out) == nil {
		*l = out
	}
}

// encode writes the <special> element.
func (s *kblSpecial) encode() string {
	keys := make([]string, 0, len(s.replace))
	for k := range s.replace {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var obj bytes.Buffer
	obj.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			obj.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		obj.Write(kb)
		obj.WriteByte(':')
		obj.Write(s.replace[k])
	}
	obj.WriteByte('}')
	var esc bytes.Buffer
	xml.EscapeText(&esc, obj.Bytes())
	return `<special xmlns:kbl="` + KBLNamespace + `"><kbl:replace>` + esc.String() + `</kbl:replace></special>`
}
