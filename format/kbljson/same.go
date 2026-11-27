package kbljson

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
)

// SameJSON reports whether two JSON documents are equal as values: object property order and white space are
// ignored, numbers are compared as written, and an empty array or object counts as absent (the writer leaves
// empty optional values out).
func SameJSON(a, b []byte) bool { return DiffJSON(a, b) == "" }

// DiffJSON returns the JSON Pointer of the first difference between two documents in the sense of SameJSON, or
// "" when there is none.
func DiffJSON(a, b []byte) string {
	var x, y any
	da := json.NewDecoder(bytes.NewReader(a))
	da.UseNumber()
	db := json.NewDecoder(bytes.NewReader(b))
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return "(invalid JSON)"
	}
	return diff(x, y, "")
}

func empty(v any) bool {
	switch x := v.(type) {
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func diff(x, y any, at string) string {
	switch a := x.(type) {
	case map[string]any:
		b, ok := y.(map[string]any)
		if !ok {
			return at
		}
		for k, v := range a {
			w, ok := b[k]
			if !ok {
				if empty(v) {
					continue
				}
				return at + "/" + k + " (missing on the right)"
			}
			if d := diff(v, w, at+"/"+k); d != "" {
				return d
			}
		}
		for k, w := range b {
			if _, ok := a[k]; !ok && !empty(w) {
				return at + "/" + k + " (missing on the left)"
			}
		}
		return ""
	case []any:
		b, ok := y.([]any)
		if !ok || len(a) != len(b) {
			return at + " (length)"
		}
		for i := range a {
			if d := diff(a[i], b[i], at+"/"+strconv.Itoa(i)); d != "" {
				return d
			}
		}
		return ""
	}
	if !reflect.DeepEqual(x, y) {
		return at
	}
	return ""
}
