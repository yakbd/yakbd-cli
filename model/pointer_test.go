package model

import "testing"

func TestPointer(t *testing.T) {
	for want, toks := range map[string][]string{"": nil, "/a/0": {"a", "0"}, "/a~1b/m~0n": {"a/b", "m~n"}, "/": {""}} {
		if got := Pointer(toks...); got != want {
			t.Errorf("%q: %q, want %q", toks, got, want)
		}
	}
}
