// Package fuzzcheck holds what the Go fuzz targets of the tool share (docs/fuzzing.md): the time and allocation
// bounds every reader must keep on any input, and the seed corpus taken from the repository's test data. Only
// tests import it.
package fuzzcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"testing"
	"time"
)

// Bound is the budget of one call on an input of n bytes: at most Time, and at most Base + PerByte*n bytes
// allocated. A hang shows as a call that exceeds Time; an allocation out of proportion to the input as one that
// exceeds the byte budget.
type Bound struct {
	Time    time.Duration
	Base    uint64
	PerByte uint64
}

// Default is the bound of a reader: generous enough for a slow CI runner and the race detector, far below what a
// loop or an attacker-chosen count costs.
var Default = Bound{Time: 5 * time.Second, Base: 32 << 20, PerByte: 512}

// Check runs fn under the bound for an input of n bytes and fails t when it panics, runs too long or allocates too
// much, or when fn returns an error (a broken property). A panic is reported as a failure with its stack, so that
// the fuzzer can minimise the input; a call that runs too long fails without waiting for it to end.
func Check(t testing.TB, b Bound, n int, fn func() error) {
	t.Helper()
	sample := []metrics.Sample{{Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(sample)
	before := sample[0].Value.Uint64()
	done := make(chan struct{})
	var err error
	var crash string
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				crash = fmt.Sprintf("panic: %v\n%s", p, debug.Stack())
			}
		}()
		err = fn()
	}()
	timer := time.NewTimer(b.Time)
	defer timer.Stop()
	select {
	case <-done:
		if crash != "" {
			t.Fatal(crash) // a failure, not a crash of the worker, so that the fuzzer can minimise the input
		}
	case <-timer.C:
		t.Fatalf("no result after %v on an input of %d bytes (a hang)", b.Time, n)
	}
	metrics.Read(sample)
	if got, limit := sample[0].Value.Uint64()-before, b.Base+b.PerByte*uint64(n); got > limit {
		t.Fatalf("allocated %d bytes on an input of %d bytes (limit %d)", got, n, limit)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// Files returns the files that match the glob patterns (relative to the test's package directory), sorted. A
// pattern that matches nothing fails t: a seed corpus that silently became empty is a broken test.
func Files(t testing.TB, patterns ...string) []string {
	t.Helper()
	var out []string
	for _, p := range patterns {
		m, err := filepath.Glob(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(m) == 0 {
			t.Fatalf("seed pattern %s matches no file", p)
		}
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

// Read returns the bytes of each file, failing t on an error.
func Read(t testing.TB, paths []string) [][]byte {
	t.Helper()
	out := make([][]byte, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// MaxSeed is the largest file Seeds adds: the fuzzer mutates a large seed slowly, and the bounds are calibrated for
// small inputs (CLDR egy-Egyp-t-k0-qwerty.xml, 414 KB, is left out; the conformance tests read it).
const MaxSeed = 64 << 10

// Seeds reads every file the patterns match, up to MaxSeed bytes, and adds it to the fuzz corpus; it returns the
// number added.
func Seeds(f *testing.F, patterns ...string) int {
	f.Helper()
	n := 0
	for _, b := range Read(f, Files(f, patterns...)) {
		if len(b) <= MaxSeed {
			f.Add(b)
			n++
		}
	}
	return n
}

// Equal fails t with both texts when a and b differ.
func Equal(t testing.TB, what string, a, b []byte) {
	t.Helper()
	if string(a) != string(b) {
		t.Fatalf("%s is not stable:\n--- first\n%s\n--- second\n%s", what, clip(a), clip(b))
	}
}

func clip(b []byte) string {
	if len(b) > 4000 {
		return fmt.Sprintf("%s\n... (%d bytes)", b[:4000], len(b))
	}
	return string(b)
}
