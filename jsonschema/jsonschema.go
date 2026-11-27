// Package jsonschema validates JSON documents against a JSON Schema (draft 2020-12), restricted to the keywords
// the `.kbl.json` schema uses: type, properties, required, additionalProperties, patternProperties,
// propertyNames, items, minItems, maxItems, uniqueItems, enum, const, pattern, minLength, maxLength, minimum,
// maximum, oneOf, anyOf, allOf, not, and local $ref ("#/$defs/..."). Annotation keywords are ignored. A schema
// that uses any other assertion keyword is rejected by Compile, so nothing is silently skipped.
//
// Regular expressions are Go's RE2; the patterns of the kbl schema use only syntax in which RE2 and ECMA-262 agree,
// except that \s and \d match ASCII only in RE2.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Error is one validation failure, at a JSON Pointer into the document.
type Error struct {
	Path    string
	Message string
}

func (e Error) Error() string { return e.Path + ": " + e.Message }

// Schema is a compiled schema.
type Schema struct {
	root    any
	regexps map[string]*regexp.Regexp
}

var annotations = map[string]bool{"$schema": true, "$id": true, "$comment": true, "$defs": true, "title": true,
	"description": true, "default": true, "examples": true, "deprecated": true, "readOnly": true, "writeOnly": true}

var assertions = map[string]bool{"type": true, "properties": true, "required": true, "additionalProperties": true,
	"patternProperties": true, "propertyNames": true, "items": true, "minItems": true, "maxItems": true,
	"uniqueItems": true, "enum": true, "const": true, "pattern": true, "minLength": true, "maxLength": true,
	"minimum": true, "maximum": true, "oneOf": true, "anyOf": true, "allOf": true, "not": true, "$ref": true}

// Compile parses a schema and checks that it uses only supported keywords.
func Compile(data []byte) (*Schema, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var root any
	if err := d.Decode(&root); err != nil {
		return nil, err
	}
	s := &Schema{root: root, regexps: map[string]*regexp.Regexp{}}
	if err := s.check(root, "#"); err != nil {
		return nil, err
	}
	return s, nil
}

// check walks every subschema once.
func (s *Schema) check(n any, at string) error {
	m, ok := n.(map[string]any)
	if !ok {
		if _, isBool := n.(bool); isBool {
			return nil
		}
		return fmt.Errorf("jsonschema: %s is not a schema", at)
	}
	for k, v := range m {
		switch {
		case annotations[k]:
			if k == "$defs" {
				defs, _ := v.(map[string]any)
				for name, d := range defs {
					if err := s.check(d, at+"/$defs/"+name); err != nil {
						return err
					}
				}
			}
			continue
		case !assertions[k]:
			return fmt.Errorf("jsonschema: %s uses the unsupported keyword %q", at, k)
		}
		switch k {
		case "properties", "patternProperties":
			for name, sub := range v.(map[string]any) {
				if k == "patternProperties" {
					if err := s.compileRegexp(name); err != nil {
						return err
					}
				}
				if err := s.check(sub, at+"/"+k+"/"+name); err != nil {
					return err
				}
			}
		case "items", "additionalProperties", "propertyNames", "not":
			if err := s.check(v, at+"/"+k); err != nil {
				return err
			}
		case "oneOf", "anyOf", "allOf":
			for i, sub := range v.([]any) {
				if err := s.check(sub, fmt.Sprintf("%s/%s/%d", at, k, i)); err != nil {
					return err
				}
			}
		case "pattern":
			if err := s.compileRegexp(v.(string)); err != nil {
				return err
			}
		case "$ref":
			if _, err := s.resolve(v.(string)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Schema) compileRegexp(p string) error {
	if _, ok := s.regexps[p]; ok {
		return nil
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return fmt.Errorf("jsonschema: pattern %q: %v", p, err)
	}
	s.regexps[p] = re
	return nil
}

func (s *Schema) resolve(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("jsonschema: only local references are supported, not %q", ref)
	}
	n := s.root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#"), "/")[1:] {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := n.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("jsonschema: reference %q does not resolve", ref)
		}
		if n, ok = m[part]; !ok {
			return nil, fmt.Errorf("jsonschema: reference %q does not resolve", ref)
		}
	}
	return n, nil
}

// Decode parses a JSON document with numbers kept exact, as Validate expects.
func Decode(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if d.More() {
		return nil, fmt.Errorf("data after the JSON value")
	}
	return v, nil
}

// Validate checks a document decoded by Decode. It returns every failure; for oneOf and anyOf it reports the
// failures of the closest alternative.
func (s *Schema) Validate(doc any) []Error {
	var errs []Error
	s.validate(s.root, doc, "", &errs, 0)
	return errs
}

func pointer(base, tok string) string {
	return base + "/" + strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case json.Number:
		if _, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
			return "integer"
		}
		if r, ok := new(big.Rat).SetString(x.String()); ok && r.IsInt() {
			return "integer"
		}
		return "number"
	}
	return "unknown"
}

func num(v any) (*big.Rat, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return nil, false
	}
	return new(big.Rat).SetString(n.String())
}

func canonical(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *Schema) validate(sch, v any, at string, errs *[]Error, depth int) {
	if depth > 200 {
		*errs = append(*errs, Error{at, "schema nesting too deep"})
		return
	}
	if b, ok := sch.(bool); ok {
		if !b {
			*errs = append(*errs, Error{at, "not allowed here"})
		}
		return
	}
	m := sch.(map[string]any)
	fail := func(format string, args ...any) { *errs = append(*errs, Error{at, fmt.Sprintf(format, args...)}) }
	if ref, ok := m["$ref"].(string); ok {
		target, _ := s.resolve(ref)
		s.validate(target, v, at, errs, depth+1)
	}
	if t, ok := m["type"]; ok {
		got := typeOf(v)
		match := func(want string) bool { return want == got || (want == "number" && got == "integer") }
		okType := false
		switch tt := t.(type) {
		case string:
			okType = match(tt)
		case []any:
			for _, x := range tt {
				okType = okType || match(x.(string))
			}
		}
		if !okType {
			fail("is %s, want %v", got, t)
			return
		}
	}
	if e, ok := m["enum"].([]any); ok {
		found := false
		for _, x := range e {
			found = found || canonical(x) == canonical(v)
		}
		if !found {
			fail("is not one of %s", canonical(e))
		}
	}
	if c, ok := m["const"]; ok && canonical(c) != canonical(v) {
		fail("must be %s", canonical(c))
	}
	switch x := v.(type) {
	case string:
		n := utf8.RuneCountInString(x)
		if p, ok := m["pattern"].(string); ok && !s.regexps[p].MatchString(x) {
			fail("%q does not match %s", x, p)
		}
		if mn, ok := num(m["minLength"]); ok && big.NewRat(int64(n), 1).Cmp(mn) < 0 {
			fail("shorter than %s characters", mn.RatString())
		}
		if mx, ok := num(m["maxLength"]); ok && big.NewRat(int64(n), 1).Cmp(mx) > 0 {
			fail("longer than %s characters", mx.RatString())
		}
	case json.Number:
		r, _ := num(x)
		if mn, ok := num(m["minimum"]); ok && r.Cmp(mn) < 0 {
			fail("%s is below the minimum %s", x, mn.RatString())
		}
		if mx, ok := num(m["maximum"]); ok && r.Cmp(mx) > 0 {
			fail("%s is above the maximum %s", x, mx.RatString())
		}
	case []any:
		if mn, ok := num(m["minItems"]); ok && big.NewRat(int64(len(x)), 1).Cmp(mn) < 0 {
			fail("has fewer than %s items", mn.RatString())
		}
		if mx, ok := num(m["maxItems"]); ok && big.NewRat(int64(len(x)), 1).Cmp(mx) > 0 {
			fail("has more than %s items", mx.RatString())
		}
		if u, ok := m["uniqueItems"].(bool); ok && u {
			seen := map[string]int{}
			for i, it := range x {
				c := canonical(it)
				if j, dup := seen[c]; dup {
					fail("items %d and %d are equal", j, i)
				}
				seen[c] = i
			}
		}
		if items, ok := m["items"]; ok {
			for i, it := range x {
				s.validate(items, it, pointer(at, strconv.Itoa(i)), errs, depth+1)
			}
		}
	case map[string]any:
		if req, ok := m["required"].([]any); ok {
			for _, r := range req {
				if _, has := x[r.(string)]; !has {
					fail("property %q is required", r)
				}
			}
		}
		props, _ := m["properties"].(map[string]any)
		pprops, _ := m["patternProperties"].(map[string]any)
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := x[k]
			if pn, ok := m["propertyNames"]; ok {
				s.validate(pn, k, pointer(at, k), errs, depth+1)
			}
			matched := false
			if ps, ok := props[k]; ok {
				matched = true
				s.validate(ps, val, pointer(at, k), errs, depth+1)
			}
			for p, ps := range pprops {
				if s.regexps[p].MatchString(k) {
					matched = true
					s.validate(ps, val, pointer(at, k), errs, depth+1)
				}
			}
			if ap, ok := m["additionalProperties"]; ok && !matched {
				if b, isBool := ap.(bool); isBool && !b {
					*errs = append(*errs, Error{pointer(at, k), "unknown property"})
				} else {
					s.validate(ap, val, pointer(at, k), errs, depth+1)
				}
			}
		}
	}
	if all, ok := m["allOf"].([]any); ok {
		for _, sub := range all {
			s.validate(sub, v, at, errs, depth+1)
		}
	}
	for _, kw := range []string{"oneOf", "anyOf"} {
		alts, ok := m[kw].([]any)
		if !ok {
			continue
		}
		pass := 0
		var best []Error
		for _, sub := range alts {
			var e []Error
			s.validate(sub, v, at, &e, depth+1)
			if len(e) == 0 {
				pass++
			} else if best == nil || len(e) < len(best) {
				best = e
			}
		}
		switch {
		case kw == "oneOf" && pass > 1:
			fail("matches %d alternatives of oneOf, want exactly one", pass)
		case pass == 0:
			*errs = append(*errs, best...)
			fail("matches no alternative of %s", kw)
		}
	}
	if not, ok := m["not"]; ok {
		var e []Error
		s.validate(not, v, at, &e, depth+1)
		if len(e) == 0 {
			fail("must not match the schema under \"not\"")
		}
	}
}
