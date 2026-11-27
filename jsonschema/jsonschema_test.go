package jsonschema

import (
	"strings"
	"testing"
)

const schema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object", "required": ["kbl"], "additionalProperties": false,
  "properties": {
    "kbl": {"const": "0.1"},
    "n": {"type": "integer", "minimum": 1, "maximum": 3},
    "s": {"type": "string", "pattern": "^[a-z]+$", "minLength": 2, "maxLength": 4},
    "a": {"type": "array", "items": {"$ref": "#/$defs/Hex"}, "minItems": 1, "maxItems": 2, "uniqueItems": true},
    "o": {"type": "object", "propertyNames": {"pattern": "^[A-Z]"}, "additionalProperties": {"type": "boolean"}},
    "u": {"oneOf": [{"type": "string"}, {"type": "object", "required": ["x"]}]},
    "e": {"enum": ["a", "b"]},
    "t": {"not": {"const": 0}}
  },
  "$defs": {"Hex": {"type": "string", "pattern": "^[0-9A-F]{4}$", "description": "four hex digits"}}
}`

func TestValidate(t *testing.T) {
	s, err := Compile([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		doc  string
		want string // substring of the joined errors; "" = valid
	}{
		{`{"kbl":"0.1"}`, ""},
		{`{"kbl":"0.1","n":2,"s":"abc","a":["00FF"],"o":{"A":true},"u":{"x":1},"e":"b","t":1}`, ""},
		{`{}`, `"kbl" is required`},
		{`{"kbl":"0.2"}`, "must be"},
		{`{"kbl":"0.1","x":1}`, "/x: unknown property"},
		{`{"kbl":"0.1","n":1.5}`, "is number"},
		{`{"kbl":"0.1","n":4}`, "above the maximum"},
		{`{"kbl":"0.1","s":"ABC"}`, "does not match"},
		{`{"kbl":"0.1","s":"a"}`, "shorter"},
		{`{"kbl":"0.1","a":[]}`, "fewer than"},
		{`{"kbl":"0.1","a":["00FF","00FF"]}`, "equal"},
		{`{"kbl":"0.1","a":["xyz"]}`, "/a/0"},
		{`{"kbl":"0.1","o":{"a":true}}`, "/o/a"},
		{`{"kbl":"0.1","o":{"A":1}}`, "is integer"},
		{`{"kbl":"0.1","u":3}`, "no alternative"},
		{`{"kbl":"0.1","e":"c"}`, "not one of"},
		{`{"kbl":"0.1","t":0}`, "must not match"},
	}
	for _, tt := range tests {
		doc, err := Decode([]byte(tt.doc))
		if err != nil {
			t.Fatal(err)
		}
		var msgs []string
		for _, e := range s.Validate(doc) {
			msgs = append(msgs, e.Error())
		}
		got := strings.Join(msgs, "; ")
		if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.doc, got, tt.want)
		}
	}
}

func TestCompileRejects(t *testing.T) {
	for _, sch := range []string{`{"format":"email"}`, `{"$ref":"other.json#/x"}`, `{"$ref":"#/$defs/missing"}`, `{"pattern":"("}`} {
		if _, err := Compile([]byte(sch)); err == nil {
			t.Errorf("%s compiled", sch)
		}
	}
}
