package xkbkbl

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/model"
)

const corpus = "../../testdata/third_party/xkeyboard-config-2.49/symbols"

func modelState(im *importer, s int) model.State {
	st := model.State{Shift: s&1 != 0, Caps: s&2 != 0, Custom: map[string]bool{}}
	if s&4 != 0 {
		if im.l3comp == "altR" {
			st.AltR = true
		} else {
			st.Custom["level3"] = true
		}
	}
	if s&8 != 0 {
		st.Custom["level5"] = true
	}
	return st
}

func testCompose(t *testing.T) *xkb.Compose {
	b, err := os.ReadFile("../../testdata/xkb/test.XCompose")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := xkb.ReadCompose(b, "test.XCompose")
	return c
}

// TestCorpusImport: every section of the xkeyboard-config subset imports as a layout that passes the checks,
// writes as .kbl.json and reads back, and types what XKB types in all 16 states (Shift, Caps Lock, LevelThree,
// LevelFive) at every position it kept.
func TestCorpusImport(t *testing.T) {
	comp := testCompose(t)
	inc := xkb.NewIncludes(func(n string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(corpus, filepath.FromSlash(n)))
		return b, err == nil
	})
	ents, err := os.ReadDir(corpus)
	if err != nil {
		t.Fatal(err)
	}
	sections, dead := 0, 0
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(corpus, e.Name()))
		f, r := xkb.Read(b, e.Name())
		if f == nil {
			t.Fatalf("%s: %s", e.Name(), r.Summary())
		}
		for _, s := range f.Sections {
			sections++
			where := e.Name() + "(" + s.Name + ")"
			km, _ := xkb.Compile(f, s, e.Name(), inc)
			l, ir := FromXKB(km, where, Options{Compose: comp, ComposeName: "test.XCompose"})
			if ir.HasErrors() {
				t.Errorf("%s: %s", where, ir.Summary())
				continue
			}
			if cr := l.Check(); cr.HasErrors() {
				t.Errorf("%s: check: %s", where, cr.Summary())
				continue
			}
			data, jr := kbljson.Write(l)
			if data == nil || jr.HasErrors() {
				t.Errorf("%s: write: %s", where, jr.Summary())
				continue
			}
			if back, br := kbljson.Read(data, "back"); back == nil || br.HasErrors() {
				t.Errorf("%s: read back: %s", where, br.Summary())
			}
			for _, dk := range l.DeadKeys {
				dead += len(dk.Entries)
			}
			im := &importer{km: km, name: where, r: ir, once: map[string]bool{}, posOf: map[string]string{},
				cells: map[string]*[states]cell{}, deadSyms: map[xkb.Keysym]bool{}}
			im.switches()
			im.keys()
			for p, cs := range im.cells {
				for st := 0; st < states; st++ {
					if st&4 != 0 && im.l3comp == "" || st&8 != 0 && len(im.l5pos) == 0 {
						continue
					}
					o := l.Effective(l.Resolve(p, modelState(im, st)))
					want := cs[st]
					got := cell{text: string(utf16.Decode(o.Units))}
					if o.DeadKey != "" && want.dead != 0 && deadID(want.dead) == o.DeadKey {
						got = want
					}
					if got != want {
						t.Errorf("%s %s state %d: the layout gives %+v, XKB %+v", where, p, st, got, want)
					}
				}
			}
		}
	}
	if sections < 100 || dead < 50 {
		t.Errorf("only %d sections and %d dead-key entries", sections, dead)
	}
}

func TestDeadKeys(t *testing.T) {
	src := `xkb_symbols "t" {
		name[Group1] = "Test";
		key <AE12> { [ dead_acute, dead_grave, dead_diaeresis ] };
		key <AC01> { [ a, A ] };
		key <AD07> { [ u, U ] };
		key <RALT> { [ ISO_Level3_Shift ] };
	};`
	f, _ := xkb.Read([]byte(src), "t")
	km, _ := xkb.Compile(f, f.Sections[0], "t", nil)
	l, r := FromXKB(km, "t", Options{Compose: testCompose(t), ComposeName: "c"})
	if r.HasErrors() {
		t.Fatal(r.Summary())
	}
	ac := l.DeadKeys["acute"]
	if ac == nil || ac.Spacing.Text != "´" || ac.NoMatch != "emitNothing" {
		t.Fatalf("acute: %+v", ac)
	}
	var chain string
	for _, e := range ac.Entries {
		if e.Base.Kind == model.ValueDeadKey && e.Base.DeadKey == "diaeresis" {
			chain = e.Result.DeadKey
		}
	}
	if chain != "acute+diaeresis" || len(l.DeadKeys[chain].Entries) != 1 || l.DeadKeys[chain].Entries[0].Result.Text != "ǘ" {
		t.Errorf("chain: %q %+v", chain, l.DeadKeys[chain])
	}
	if l.DeadKeys["grave"] == nil || l.DeadKeys["diaeresis"] == nil {
		t.Error("grave, diaeresis")
	}
	if got := l.Resolve("Equal", model.State{AltR: true}); got == nil || got.DeadKey != "diaeresis" {
		t.Errorf("AltGr+Equal: %+v", got)
	}
	l2, r2 := FromXKB(km, "t", Options{})
	if len(l2.DeadKeys["acute"].Entries) != 0 || !hasCode(r2, "xkb.deadKeyNoCompose") {
		t.Errorf("without a Compose file: %s", r2.Summary())
	}
}

func hasCode(r interface{ Codes() []string }, c string) bool {
	for _, x := range r.Codes() {
		if x == c {
			return true
		}
	}
	return false
}
