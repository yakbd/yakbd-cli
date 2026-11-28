package xkbkbl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
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
			im := newImporter(km, where, Options{})
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
	if chain != "acute-diaeresis" || len(l.DeadKeys[chain].Entries) != 1 || l.DeadKeys[chain].Entries[0].Result.Text != "ǘ" {
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

// importText reads a symbols file and imports its default section.
func importText(t *testing.T, sym, comp []byte, name string) (*model.Layout, *importer) {
	t.Helper()
	f, r := xkb.Read(sym, name)
	if f == nil {
		t.Fatalf("%s: %s\n%s", name, r.Summary(), sym)
	}
	o := Options{SymbolsName: name}
	if comp != nil {
		c, cr := xkb.ReadCompose(comp, name+".XCompose")
		if c == nil {
			t.Fatalf("%s: %s", name, cr.Summary())
		}
		o.Compose, o.ComposeName = c, name+".XCompose"
	}
	km, _ := xkb.Compile(f, f.Section(""), name, nil)
	l, _ := FromXKB(km, name, o)
	im := newImporter(km, name, o)
	im.switches()
	im.keys()
	return l, im
}

// TestRoundTrip: every corpus section, imported and built again, types the same in every state where the build
// reported no loss for the key (capsLockNotExpressible), and a second import and build gives the same files.
func TestRoundTrip(t *testing.T) {
	comp := testCompose(t)
	inc := xkb.NewIncludes(func(n string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(corpus, filepath.FromSlash(n)))
		return b, err == nil
	})
	ents, _ := os.ReadDir(corpus)
	n := 0
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(corpus, e.Name()))
		f, _ := xkb.Read(b, e.Name())
		for _, s := range f.Sections {
			where := e.Name() + "(" + s.Name + ")"
			km, _ := xkb.Compile(f, s, e.Name(), inc)
			l, _ := FromXKB(km, where, Options{Compose: comp, ComposeName: "c", SymbolsName: e.Name()})
			im := newImporter(km, where, Options{})
			im.switches()
			im.keys()
			if len(im.cells) == 0 {
				continue
			}
			res, br := build.Build(l, build.Options{})
			if res == nil {
				t.Errorf("%s: build: %s", where, br.Summary())
				continue
			}
			n++
			inexact := map[string]bool{}
			for _, en := range br.Entries {
				if en.Code == "xkb.capsLockNotExpressible" || en.Code == "xkb.numLockKeyReduced" {
					for p := range im.cells {
						if strings.Contains(en.Where.Path, "/"+p+"/") || strings.Contains(en.Message, p) {
							inexact[p] = true
						}
					}
				}
			}
			l2, im2 := importText(t, res.SymbolsText, res.ComposeText, "rt")
			for p, cs := range im.cells {
				cs2 := im2.cells[p]
				if cs2 == nil {
					t.Errorf("%s: %s lost", where, p)
					continue
				}
				for st := 0; st < states; st++ {
					if st&4 != 0 && im.l3comp == "" || st&8 != 0 && len(im.l5pos) == 0 {
						continue
					}
					if st&2 != 0 && inexact[p] {
						continue
					}
					if cs[st] != cs2[st] {
						t.Errorf("%s %s state %d: %+v, after the round trip %+v", where, p, st, cs[st], cs2[st])
					}
				}
			}
			res2, _ := build.Build(l2, build.Options{})
			res3 := res2
			if res2 != nil {
				l3, _ := importText(t, res2.SymbolsText, res2.ComposeText, "rt")
				res3, _ = build.Build(l3, build.Options{})
			}
			if res2 == nil || res3 == nil || string(res2.SymbolsText) != string(res3.SymbolsText) || string(res2.ComposeText) != string(res3.ComposeText) {
				t.Errorf("%s: the second round trip is not stable", where)
			}
		}
	}
	if n < 100 {
		t.Errorf("only %d sections built", n)
	}
}

// TestKblRoundTrip: each spec example that builds for XKB, imported again from its symbols and Compose files,
// types what the build says it types (res.Keys) in every build state, except Caps Lock states of keys the build
// reported, and its dead keys compose what the original's entries say.
func TestKblRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/kbl-spec/examples/*.kbl.json")
	n := 0
	for _, p := range files {
		b, _ := os.ReadFile(p)
		l, _ := kbljson.Read(b, p)
		res, br := build.Build(l, build.Options{})
		if res == nil {
			continue
		}
		n++
		inexact := map[string]bool{}
		for _, e := range br.Entries {
			if e.Code == "xkb.capsLockNotExpressible" || e.Code == "xkb.keyTypeNotExact" {
				inexact[e.Where.Path] = true
			}
		}
		back, im := importText(t, res.SymbolsText, res.ComposeText, res.Pins.SymbolsName)
		if cr := back.Check(); cr.HasErrors() {
			t.Errorf("%s: %s", p, cr.Summary())
		}
		for _, k := range res.Keys {
			if k.Keypad != "" {
				continue
			}
			cs := im.cells[k.Position]
			for s, v := range k.Values {
				if s&build.StLock != 0 && inexact["/targets/xkb/keys/"+k.Position+"/type"] {
					continue
				}
				var got cell
				if cs != nil {
					got = cs[s]
				}
				want := cell{text: v.Text}
				if v.Dead != "" {
					want = cell{dead: res.Keysyms[v.Dead]}
				}
				if got != want && !strings.ContainsRune(v.Text, 0xfffd) {
					t.Errorf("%s %s state %d: built %+v, imported %+v", filepath.Base(p), k.Position, s, want, got)
				}
			}
		}
		for id, k := range res.Keysyms {
			orig, re := l.DeadKeys[id], back.DeadKeys[deadID(k)]
			if re == nil {
				t.Errorf("%s: dead key %s lost", p, id)
				continue
			}
			got := map[string]string{}
			for _, e := range re.Entries {
				if e.Base.Kind == model.ValueText && e.Result.Kind == model.ValueText {
					got[e.Base.Text] = e.Result.Text
				}
			}
			for _, e := range orig.Entries {
				if e.Base.Kind != model.ValueText || e.Result.Kind != model.ValueText || len([]rune(e.Base.Text)) != 1 {
					continue
				}
				if _, typed := im.typed[xkb.FromRune([]rune(e.Base.Text)[0])]; !typed {
					continue
				}
				want := e.Result.Text
				if l.Normalizes() {
					want = e.Result.NFC().Text
				}
				if g, ok := got[e.Base.Text]; ok && g != want {
					t.Errorf("%s: %s + %q gives %q, want %q", filepath.Base(p), id, e.Base.Text, g, want)
				}
			}
		}
	}
	if n < 4 {
		t.Errorf("only %d examples built", n)
	}
}
