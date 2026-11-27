package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yakbd/yakbd-cli/convert/klckbl"
	"github.com/yakbd/yakbd-cli/format/klc"
	"github.com/yakbd/yakbd-cli/internal/wincheck"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/build"
)

// VM-4 (kbl-spec targets/windows-vm-tests.md): an SGCAPS key whose main row is plain and whose Caps
// Lock row has WCH_DEAD in column 0 or 1, with and without a dead-character (FF) row after the Caps Lock row. The
// normal build refused such a key until writer rule W-2 (D63; it now writes the ff shape), so the variants are made from kbdtst.klc by changing
// its tables directly and compiled without the table checks (windows.CompileUnvalidated).
//
// The key is Q (VK 51): main row q / Q, Caps Lock row Q / q, the dead character U+00B4 with the entries a → á,
// A → Á and space → ´. The expectations are a hypothesis, H: the dead cell of the Caps Lock row works like one of a
// main row, and its dead character is the cell in the same column of the row after the Caps Lock row (the rule
// dll-format §4.2 measured for main rows: "whatever its virtual key"). Without an FF row that is the next key's
// row. A FAIL line therefore records what Windows does instead; it is a result, not a broken bundle.

const vm4Dead = 0x00B4

type vm4Variant struct {
	name string
	col  int  // the dead column of the Caps Lock row
	ff   bool // an FF row after the Caps Lock row
}

var vm4Variants = []vm4Variant{
	{"vm4-caps-dead-c0-ff", 0, true},
	{"vm4-caps-dead-c0-noff", 0, false},
	{"vm4-caps-dead-c1-ff", 1, true},
	{"vm4-caps-dead-c1-noff", 1, false},
}

// vm4 writes the variants into out/layouts and returns their manifest entries.
func vm4(repo, out string) ([]wincheck.Entry, error) {
	src := filepath.Join(repo, "testdata/reference-spec/vm/kbdtst.klc")
	var entries []wincheck.Entry
	for _, v := range vm4Variants {
		d, err := lowerKLC(src)
		if err != nil {
			return nil, err
		}
		exp, err := vm4Tables(&d.Tables, v)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(out, "layouts", v.name)
		e := wincheck.Entry{Name: v.name, DLL: map[string]string{}, Note: fmt.Sprintf(
			"VM-4: SGCAPS key Q, WCH_DEAD in column %d of the Caps Lock row, FF row after it: %v; expectations = hypothesis H",
			v.col, v.ff)}
		for _, b := range []struct {
			name string
			arch windows.Arch
		}{{"x64", windows.AMD64}, {"wow64", windows.WOW64}, {"x86", windows.X86}} {
			bin, r := windows.CompileUnvalidated(d, b.arch)
			if bin == nil {
				return nil, fmt.Errorf("%s %s: %s", v.name, b.name, r.Summary())
			}
			p := filepath.Join(dir, b.name, d.FileName)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(p, bin, 0o644); err != nil {
				return nil, err
			}
			e.DLL[b.name] = filepath.ToSlash(filepath.Join("layouts", v.name, b.name, d.FileName))
		}
		p := filepath.Join(dir, v.name+".expect.txt")
		if err := os.WriteFile(p, exp, 0o644); err != nil {
			return nil, err
		}
		e.Expect = filepath.ToSlash(filepath.Join("layouts", v.name, v.name+".expect.txt"))
		entries = append(entries, e)
	}
	return entries, nil
}

func lowerKLC(path string) (*windows.DLL, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, r := klc.Parse(b, path)
	if r.HasErrors() {
		return nil, fmt.Errorf("%s", r.Summary())
	}
	l, _ := klckbl.FromKLC(f, path)
	if l.Targets.Windows.Registration == nil {
		l.Targets.Windows.Registration = &model.Registration{LangID: "0409"}
	}
	res, br := build.Lower(l, build.Options{})
	if res == nil {
		return nil, fmt.Errorf("%s", br.Summary())
	}
	return res.DLL, nil
}

// vm4Tables rewrites the Q row of t into the variant and returns the expectations: those of the main rows
// (Tables.Expectations) followed by the Caps Lock lines of hypothesis H.
func vm4Tables(t *windows.Tables, v vm4Variant) ([]byte, error) {
	const vkQ, vkA, vkSpace = 0x51, 0x41, 0x20
	ti, ri := -1, -1
	for i, ct := range t.CharTables {
		for j, r := range ct.Rows {
			if r.VK == vkQ {
				ti, ri = i, j
			}
		}
	}
	if ti < 0 {
		return nil, fmt.Errorf("vm4: kbdtst has no Q row")
	}
	ct := &t.CharTables[ti]
	none := func() []uint16 {
		c := make([]uint16, ct.Width)
		for i := range c {
			c[i] = windows.WCH_NONE
		}
		return c
	}
	main := windows.CharRow{VK: vkQ, Attributes: windows.SGCAPS, Chars: none()}
	main.Chars[0], main.Chars[1] = 'q', 'Q'
	caps := windows.CharRow{VK: vkQ, Chars: none()}
	caps.Chars[0], caps.Chars[1] = 'Q', 'q'
	caps.Chars[v.col] = windows.WCH_DEAD
	rows := []windows.CharRow{main, caps}
	if v.ff {
		ff := windows.CharRow{VK: 0xFF, Chars: none()}
		ff.Chars[v.col] = vm4Dead
		rows = append(rows, ff)
	}
	// The Q rows go first in their table, so that another key's row follows them (Q is the last row of kbdtst's table).
	rest := append(append([]windows.CharRow(nil), ct.Rows[:ri]...), ct.Rows[ri+1:]...)
	ct.Rows = append(rows, rest...)
	t.DeadKeys = append(t.DeadKeys,
		windows.DeadKey{Dead: vm4Dead, Base: 'a', Composed: 0x00E1},
		windows.DeadKey{Dead: vm4Dead, Base: 'A', Composed: 0x00C1},
		windows.DeadKey{Dead: vm4Dead, Base: ' ', Composed: vm4Dead})

	// The dead character by H: the cell in the dead column of the row after the Caps Lock row.
	next := 2
	dead := uint16(windows.WCH_NONE)
	if next < len(ct.Rows) {
		dead = ct.Rows[next].Chars[v.col]
	}
	compose := func(base uint16) string {
		for _, d := range t.DeadKeys {
			if d.Dead == dead && d.Base == base {
				return fmt.Sprintf("chars %04x", d.Composed)
			}
		}
		return fmt.Sprintf("chars %04x %04x", dead, base)
	}
	var b bytes.Buffer
	b.Write(windows.FormatExpectations(v.name, t.Expectations()))
	fmt.Fprintf(&b, "# VM-4 hypothesis H: with Caps Lock on (14), column %d of Q is dead with %04x (the row after the Caps Lock row)\n", v.col, dead)
	deadPress, otherPress := "51+14", "51+10+14"
	other := "0071"
	if v.col == 1 {
		deadPress, otherPress, other = "51+10+14", "51+14", "0051"
	}
	fmt.Fprintf(&b, "key %s -> chars %s\n", otherPress, other)
	if dead == windows.WCH_NONE {
		fmt.Fprintf(&b, "key %s -> none\n", deadPress)
		return b.Bytes(), nil
	}
	fmt.Fprintf(&b, "key %s -> dead %04x\n", deadPress, dead)
	fmt.Fprintf(&b, "seq %s %02x+14 -> %s\n", deadPress, vkA, compose('A')) // Caps Lock on: the A key types A
	fmt.Fprintf(&b, "seq %s %02x -> %s\n", deadPress, vkA, compose('a'))
	fmt.Fprintf(&b, "seq %s %02x -> %s\n", deadPress, vkSpace, compose(' '))
	return b.Bytes(), nil
}
