package main

import "testing"

func TestQuietIn(t *testing.T) {
	for _, a := range [][]string{{"/quiet"}, {"-QN"}, {"/qb!-"}, {"/passive"}, {"/q"}} {
		if !quietIn(a) {
			t.Errorf("%v", a)
		}
	}
	if quietIn([]string{"/x", "/log", "a.log"}) {
		t.Error("not quiet")
	}
}
