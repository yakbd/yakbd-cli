package build

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/keyname"
)

// composeSeq is one Compose sequence of the build.
type composeSeq struct {
	seq    []xkb.Keysym
	result string
}

// compose is rule compose: the sequences of the file's dead keys (targets/xkb.md §4).
func (b *builder) compose() {
	c := &model.XKBCompose{}
	if b.file.Compose != nil {
		*c = *b.file.Compose
	}
	var fields []string
	v := map[string]bool{}
	if c.Write == nil {
		w := len(b.l.DeadKeys) > 0
		c.Write = &w
		fields = append(fields, tp+"compose/write")
		v["write"] = w
	}
	if c.IncludeLocale == nil {
		t := true
		c.IncludeLocale = &t
		fields = append(fields, tp+"compose/includeLocale")
		v["includeLocale"] = true
	}
	if len(fields) > 0 {
		b.defaulted("compose", fields, v, "the Compose file: written when the layout has dead keys, starting with include \"%%L\"")
	}
	b.pin.Compose = c
}

// sequences lists the Compose sequences of every dead key, chains as longer sequences.
func (b *builder) sequences() []composeSeq {
	var out []composeSeq
	seen := map[string]bool{}
	var walk func(id string, prefix []xkb.Keysym, path []string)
	walk = func(id string, prefix []xkb.Keysym, path []string) {
		dk := b.l.DeadKeys[id]
		bases := map[string]bool{}
		for i, e := range dk.Entries {
			at := fmt.Sprintf("/deadKeys/%s/entries/%d", model.Escape(id), i)
			var base xkb.Keysym
			switch e.Base.Kind {
			case model.ValueDeadKey:
				k, ok := b.syms[e.Base.DeadKey]
				if !ok {
					continue
				}
				base = k
			default:
				t := e.Base.Text
				if e.Base.Kind == model.ValueUnits {
					t = string(utf16.Decode(e.Base.Units))
				}
				l, ok := keysymsOf(t)
				if !ok || len(l) != 1 {
					b.lost("xkb.composeBaseNotSingle", at+"/base", "the base %q is not one character: a Compose sequence goes key by key", t)
					continue
				}
				base = l[0].K
			}
			key := base.Name()
			if bases[key] {
				continue // a later entry with the same base is unreachable
			}
			bases[key] = true
			seq := append(append([]xkb.Keysym(nil), prefix...), base)
			switch e.Result.Kind {
			case model.ValueDeadKey:
				next := e.Result.DeadKey
				cut := false
				for _, p := range path {
					if p == next {
						cut = true
					}
				}
				if cut || len(seq) >= 20 || b.l.DeadKeys[next] == nil {
					b.lost("xkb.deadKeyChainCut", at+"/result", "the chain of dead keys %s → %s loops back: the sequence stops here", strings.Join(path, " → "), next)
					continue
				}
				walk(next, seq, append(path, next))
			default:
				t := e.Result.Text
				if e.Result.Kind == model.ValueUnits {
					t = string(utf16.Decode(e.Result.Units))
				}
				if b.l.Normalizes() && e.Result.Kind == model.ValueText {
					t = e.Result.NFC().Text
				}
				out = append(out, composeSeq{seq: seq, result: t})
			}
		}
		if len(path) == 1 && !seen[id] {
			seen[id] = true
			nm := dk.NoMatch
			if nm == "" {
				nm = "emitBoth"
			}
			b.lost("xkb.deadKeyNoMatchNotCompiled", "/deadKeys/"+model.Escape(id), "noMatch (%s) and spacing of %s are not compiled: after the dead key, a key with no sequence is left to the input method, which cancels the dead key", nm, id)
		}
	}
	for _, id := range sortedKeys(b.l.DeadKeys) {
		k, ok := b.syms[id]
		if !ok {
			continue
		}
		walk(id, []xkb.Keysym{k}, []string{id})
	}
	return out
}

// levelKeys are the positions of a level component.
func (b *builder) levelKeys(c string) []string {
	switch c {
	case "altR":
		return []string{"AltRight"}
	case "alt":
		return []string{"AltLeft", "AltRight"}
	case "":
		return nil
	}
	if cm := b.l.CustomModifiers[c]; cm != nil {
		return cm.Positions
	}
	return nil
}

// write writes the files of the build.
func (b *builder) write(res *Result) {
	p := b.pin
	sec := &xkb.Section{Name: "basic", Flags: []string{"default", "partial", "alphanumeric_keys"}}
	if v := VariantOf(p); v != "" {
		sec.Name, sec.Flags = v, []string{"partial", "alphanumeric_keys"}
	}
	sec.Stmts = append(sec.Stmts, &xkb.Stmt{Kind: xkb.GroupName, Group: 1, Text: p.Description})
	var types []*xkb.KeyType
	typeSeen := map[string]bool{}
	for _, k := range b.keys {
		levels := make([]xkb.Level, len(k.Levels))
		for i, v := range k.Levels {
			levels[i] = b.levelOf(v)
		}
		if k.Keypad != "" {
			ks, _ := xkb.KeysymFromName(k.Keypad)
			levels[0] = xkb.Level{{K: ks}}
		}
		key := &xkb.Key{Name: k.Name, Groups: []xkb.Group{{Type: k.Type, Levels: levels, HasSyms: true}}}
		sec.Stmts = append(sec.Stmts, &xkb.Stmt{Kind: xkb.KeyDef, Key: key})
		if strings.HasPrefix(k.Type, "YAKBD_") && !typeSeen[k.Type] {
			typeSeen[k.Type] = true
			types = append(types, TypeOf(k.Type))
		}
	}
	shift := func(c string, sym string) {
		k, _ := xkb.KeysymFromName(sym)
		for _, pos := range b.levelKeys(c) {
			name := ""
			if kk := b.file.Keys[pos]; kk != nil && kk.KeyName != "" {
				name = kk.KeyName
			} else if r, ok := keyname.ForPosition(pos); ok {
				name = r.KeyName
			}
			if name == "" {
				b.lost("xkb.keyNameMissing", "/positions/"+model.Escape(pos), "the level-shift position %s has no XKB key name", pos)
				continue
			}
			key := &xkb.Key{Name: name, Groups: []xkb.Group{{Type: "ONE_LEVEL", Levels: []xkb.Level{{{K: k}}}, HasSyms: true}}}
			sec.Stmts = append(sec.Stmts, &xkb.Stmt{Kind: xkb.KeyDef, Key: key})
		}
	}
	if b.usesL3 {
		shift(b.l3, "ISO_Level3_Shift")
	}
	if b.usesL5 {
		shift(b.l5, "ISO_Level5_Shift")
	}
	header := []string{fmt.Sprintf("The XKB layout %s, written by yakbd from %s.", p.SymbolsName, b.l.Metadata.ID)}
	res.Symbols = &xkb.File{Sections: []*xkb.Section{sec}}
	res.SymbolsText = xkb.Write(res.Symbols, xkb.WriteOptions{UTF8: p.Characters == "utf8", Header: header})
	if len(types) > 0 {
		res.TypesName = p.SymbolsName
		if v := VariantOf(p); v != "" {
			res.TypesName += "-" + v
		}
		res.TypesName += "-types"
		res.Types = typesText(res.TypesName, types, header)
		res.Rules = rulesPost(p, res.TypesName)
	}
	if *p.Compose.Write {
		seqs := b.sequences()
		if len(b.syms) > 0 {
			b.lost("xkb.deadKeyStacks", "/deadKeys", "the dead keys type where the input method reads the user's Compose file: libxkbcommon, Qt 6, GTK 4 on X11 and weston; GTK 4 on sway, KWin and GNOME drops them unless an input method runs or GTK_IM_MODULE=simple is set")
		}
		c := &xkb.Compose{}
		if *p.Compose.IncludeLocale {
			c.Lines = append(c.Lines, xkb.ComposeLine{Include: "%L"})
			if len(b.l.DeadKeys) > 0 {
				b.lost("xkb.systemComposeSequences", tp+"compose/includeLocale", "the Compose file includes the system's sequences (include \"%%L\"): its dead keys also make what the system's table has for them")
			}
		}
		lines := &xkb.Compose{}
		for _, s := range seqs {
			l := xkb.ComposeLine{Seq: s.seq, Result: s.result, Empty: s.result == ""}
			c.Lines = append(c.Lines, l)
			lines.Lines = append(lines.Lines, l)
		}
		res.Compose = c
		res.ComposeText = xkb.WriteCompose(c, []string{fmt.Sprintf("Dead keys of the XKB layout %s (yakbd)", p.SymbolsName)})
		res.ComposeLines = xkb.WriteCompose(lines, nil)
	}
	res.Registry = registry(p)
}

// typesText writes the yakbd key types as an xkb_types file.
func typesText(name string, ts []*xkb.KeyType, header []string) []byte {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Name < ts[j].Name })
	var s strings.Builder
	for _, h := range header {
		s.WriteString("// " + h + "\n")
	}
	s.WriteString("\ndefault partial xkb_types \"basic\" {\n    virtual_modifiers LevelThree, LevelFive;\n")
	for _, t := range ts {
		mods := "Shift + Lock + LevelThree"
		if t.Mods&xkb.LevelFive != 0 {
			mods += " + LevelFive"
		}
		fmt.Fprintf(&s, "    type %q {\n        modifiers = %s;\n", t.Name, mods)
		for _, e := range t.Map {
			if e.Level != 1 {
				fmt.Fprintf(&s, "        map[%s] = Level%d;\n", modsText(e.Mods), e.Level)
			}
		}
		for i := 1; i <= t.Levels; i++ {
			fmt.Fprintf(&s, "        level_name[Level%d] = \"%d\";\n", i, i)
		}
		s.WriteString("    };\n")
	}
	s.WriteString("};\n")
	return []byte(s.String())
}

func modsText(m xkb.Mods) string {
	var p []string
	for _, x := range []struct {
		m xkb.Mods
		n string
	}{{xkb.Shift, "Shift"}, {xkb.Lock, "Lock"}, {xkb.LevelThree, "LevelThree"}, {xkb.LevelFive, "LevelFive"}} {
		if m&x.m != 0 {
			p = append(p, x.n)
		}
	}
	return strings.Join(p, "+")
}

// registry writes rules/evdev.xml with the layout (or the variant of a layout).
func registry(p *model.XKBTarget) []byte {
	var s strings.Builder
	s.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE xkbConfigRegistry SYSTEM \"xkb.dtd\">\n")
	s.WriteString("<xkbConfigRegistry version=\"1.1\">\n  <layoutList>\n")
	s.WriteString(RegistryLayout(p))
	s.WriteString("  </layoutList>\n</xkbConfigRegistry>\n")
	return []byte(s.String())
}

// RegistryLayout is the <layout> element of the layout, indented for a layoutList.
func RegistryLayout(p *model.XKBTarget) string {
	var s strings.Builder
	item := func(ind, name string) {
		fmt.Fprintf(&s, "%s<configItem>\n%s  <name>%s</name>\n", ind, ind, xmlEscape(name))
		fmt.Fprintf(&s, "%s  <shortDescription>%s</shortDescription>\n", ind, xmlEscape(p.ShortDescription))
		fmt.Fprintf(&s, "%s  <description>%s</description>\n", ind, xmlEscape(p.Description))
		if len(p.Languages) > 0 {
			fmt.Fprintf(&s, "%s  <languageList>\n", ind)
			for _, l := range p.Languages {
				fmt.Fprintf(&s, "%s    <iso639Id>%s</iso639Id>\n", ind, xmlEscape(l))
			}
			fmt.Fprintf(&s, "%s  </languageList>\n", ind)
		}
		fmt.Fprintf(&s, "%s</configItem>\n", ind)
	}
	s.WriteString("    <layout>\n")
	if VariantOf(p) == "" {
		item("      ", p.SymbolsName)
	} else {
		fmt.Fprintf(&s, "      <configItem>\n        <name>%s</name>\n      </configItem>\n", xmlEscape(p.SymbolsName))
		s.WriteString("      <variantList>\n        <variant>\n")
		item("          ", VariantOf(p))
		s.WriteString("        </variant>\n      </variantList>\n")
	}
	s.WriteString("    </layout>\n")
	return s.String()
}

func xmlEscape(t string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(t)
}

// rulesPost is rules/evdev.post: the rule set that adds the types file to every keymap with the layout.
func rulesPost(p *model.XKBTarget, types string) []byte {
	if VariantOf(p) == "" {
		return []byte(fmt.Sprintf("! layout[any] = types\n  %s = +%s\n", p.SymbolsName, types))
	}
	return []byte(fmt.Sprintf("! layout[any] variant[any] = types\n  %s %s = +%s\n", p.SymbolsName, p.Variant, types))
}
