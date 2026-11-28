// Package xkb reads and writes the XKB text formats a Linux keyboard layout is made of: symbols files (the
// xkb_symbols sections of xkeyboard-config and of ~/.config/xkb/symbols) and Compose files. It knows the subset
// layouts use: include statements, name[], key definitions with type[], symbols[] and actions[] per group,
// modifier_map, key defaults and virtual_modifiers; keysyms by name, Uxxxx, number or UTF-8 string, and several
// keysyms per level. Anything else of a section is kept as a raw statement and written back as read. The key
// types of xkeyboard-config and the level and Caps Lock rules of a key are in types.go, the keysym names in
// keysym.go (generated from xorgproto's keysymdef.h).
package xkb

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yakbd/yakbd-cli/loss"
)

// File is a symbols file: its xkb_symbols sections in order.
type File struct {
	Sections []*Section
}

// Section is one xkb_symbols "name" { ... }; section.
type Section struct {
	Flags []string // default, partial, hidden, alphanumeric_keys, modifier_keys, keypad_keys, function_keys, alternate_group
	Name  string
	Stmts []*Stmt
	Line  int
}

// StmtKind is the kind of a statement of a section.
type StmtKind int

const (
	Include     StmtKind = iota // include "a(b)+c" (Merge: include, augment, override, replace)
	GroupName                   // name[GroupN] = "text"
	KeyDef                      // key <NAME> { ... }
	ModMap                      // modifier_map Mod { <KEY>, keysym, ... }
	KeyDefault                  // key.<field>[...] = value (Text holds what follows "key.", raw)
	VirtualMods                 // virtual_modifiers A, B (Text holds the list, raw)
	Raw                         // any other statement, kept as its tokens (Text)
)

// Stmt is one statement of a section.
type Stmt struct {
	Kind  StmtKind
	Line  int
	Merge string // Include: the keyword; KeyDef, ModMap: "", "override", "augment", "replace", "alternate"
	// Include: the include string; GroupName: the name; KeyDefault, VirtualMods, Raw: the statement's text.
	Text   string
	Group  int // GroupName: the group number (1-based)
	Key    *Key
	ModMap *ModMapDef
}

// Key is a key definition.
type Key struct {
	Name   string  // without the brackets: AE01
	Groups []Group // index = group - 1
	Props  []Prop  // other fields (virtualMods, repeat, locks, ...), raw, in the order read
}

// Group is the data of one group of a key.
type Group struct {
	Type    string  // type[GroupN]; "" for none
	Levels  []Level // the symbols; nil when the key defines none for the group
	Actions string  // actions[GroupN], the raw text between the brackets; "" for none
	HasSyms bool    // the key lists symbols for the group (possibly an empty list)
	HasActs bool
}

// Level is the keysyms of one level: none (NoSymbol), one, or several (typed together).
type Level []Sym

// Sym is one keysym of a level. Unknown is the name as written when it names no keysym (K is then NoSymbol).
type Sym struct {
	K       Keysym
	Unknown string
}

// Prop is a key field the reader does not interpret.
type Prop struct {
	Name  string // with its index, as written: "virtualMods", "repeat", "overlay1"
	Value string // raw
}

// ModMapDef is modifier_map Mod { ... }.
type ModMapDef struct {
	Mod  string
	Keys []string // "<NAME>" for a key, a keysym name otherwise
}

// Bounds of a file, far above any real layout, so that hostile input stays small.
const (
	maxDepth  = 32
	maxGroups = 4
	maxLevels = 256
	maxSyms   = 64
)

type parser struct {
	toks []token
	pos  int
	name string
	r    *loss.Report
}

type parseError struct {
	line int
	msg  string
}

func (p *parser) peek() token { return p.toks[p.pos] }

// peek2 is the token after the next one.
func (p *parser) peek2() token { return p.toks[min(p.pos+1, len(p.toks)-1)] }
func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tEOF {
		p.pos++
	}
	return t
}

func (p *parser) fail(t token, format string, args ...any) {
	panic(&parseError{t.line, fmt.Sprintf(format, args...)})
}

func (p *parser) is(t token, s string) bool { return t.kind == tPunct && t.text == s }

func (p *parser) expect(s string) token {
	t := p.next()
	if !p.is(t, s) {
		p.fail(t, "expected %q, found %s", s, describe(t))
	}
	return t
}

func describe(t token) string {
	switch t.kind {
	case tEOF:
		return "the end of the file"
	case tString:
		return "a string"
	case tKeyName:
		return "<" + t.text + ">"
	}
	return fmt.Sprintf("%q", t.text)
}

var sectionFlags = map[string]bool{"default": true, "partial": true, "hidden": true, "alphanumeric_keys": true,
	"modifier_keys": true, "keypad_keys": true, "function_keys": true, "alternate_group": true}

// Read reads a symbols file. A file with a syntax error is refused (nil and an error entry, code
// xkbsymbols.syntax); unknown keysym names are warnings (xkbsymbols.keysymUnknown) and are kept.
func Read(data []byte, name string) (*File, *loss.Report) {
	r := &loss.Report{}
	toks, err := lex(data)
	if err != nil {
		le := err.(*lexError)
		r.Errorf("xkbsymbols.syntax", loss.Where{File: name, Line: le.line}, "%s", le.msg)
		return nil, r
	}
	p := &parser{toks: toks, name: name, r: r}
	f, perr := p.file()
	if perr != nil {
		r.Errorf("xkbsymbols.syntax", loss.Where{File: name, Line: perr.line}, "%s", perr.msg)
		return nil, r
	}
	return f, r
}

func (p *parser) file() (f *File, err *parseError) {
	defer func() {
		if e := recover(); e != nil {
			pe, ok := e.(*parseError)
			if !ok {
				panic(e)
			}
			f, err = nil, pe
		}
	}()
	f = &File{}
	for p.peek().kind != tEOF {
		f.Sections = append(f.Sections, p.section())
	}
	if len(f.Sections) == 0 {
		p.fail(p.peek(), "no xkb_symbols section")
	}
	return f, nil
}

func (p *parser) section() *Section {
	s := &Section{Line: p.peek().line}
	for {
		t := p.next()
		if t.kind == tIdent && sectionFlags[t.text] {
			s.Flags = append(s.Flags, t.text)
			continue
		}
		if t.kind == tIdent && t.text == "xkb_symbols" {
			break
		}
		p.fail(t, "expected xkb_symbols, found %s", describe(t))
	}
	if t := p.peek(); t.kind == tString {
		s.Name = p.str(p.next())
	}
	p.expect("{")
	for !p.is(p.peek(), "}") {
		if p.peek().kind == tEOF {
			p.fail(p.peek(), "the section %q is not closed", s.Name)
		}
		if p.is(p.peek(), ";") {
			p.next()
			continue
		}
		s.Stmts = append(s.Stmts, p.stmt())
	}
	p.next()
	if p.is(p.peek(), ";") {
		p.next()
	}
	return s
}

func (p *parser) str(t token) string {
	if !utf8.ValidString(t.text) {
		p.fail(t, "the string is not UTF-8")
	}
	return t.text
}

var mergeWords = map[string]bool{"include": true, "augment": true, "override": true, "replace": true, "alternate": true}

func (p *parser) stmt() *Stmt {
	t := p.peek()
	st := &Stmt{Line: t.line}
	if t.kind == tIdent && mergeWords[t.text] {
		if n := p.peek2(); n.kind == tString {
			p.next()
			st.Kind, st.Merge, st.Text = Include, t.text, p.str(p.next())
			if p.is(p.peek(), ";") {
				p.next()
			}
			return st
		}
		if t.text != "include" {
			p.next()
			st.Merge = t.text
			t = p.peek()
		}
	}
	if t.kind == tIdent {
		switch t.text {
		case "key":
			if n := p.peek2(); p.is(n, ".") && st.Merge == "" {
				p.next()
				p.next()
				st.Kind, st.Text = KeyDefault, p.rawUntil(";")
				p.expect(";")
				return st
			}
			p.next()
			st.Kind, st.Key = KeyDef, p.key()
			return st
		case "modifier_map", "mod_map", "modmap":
			p.next()
			st.Kind, st.ModMap = ModMap, p.modMap()
			return st
		case "name":
			if st.Merge == "" {
				p.next()
				st.Kind = GroupName
				p.expect("[")
				st.Group = p.groupIndex()
				p.expect("]")
				p.expect("=")
				s := p.next()
				if s.kind != tString {
					p.fail(s, "a group name is a string")
				}
				st.Text = p.str(s)
				p.expect(";")
				return st
			}
		case "virtual_modifiers", "virtualmods", "virtualMods":
			if st.Merge == "" {
				p.next()
				st.Kind, st.Text = VirtualMods, p.rawUntil(";")
				p.expect(";")
				return st
			}
		}
	}
	st.Kind = Raw
	st.Text = p.rawUntil(";")
	if st.Merge != "" {
		st.Text = st.Merge + " " + st.Text
		st.Merge = ""
	}
	p.expect(";")
	return st
}

// groupIndex reads Group1 to Group4 or 1 to 4.
func (p *parser) groupIndex() int {
	t := p.next()
	s := t.text
	if t.kind == tIdent && len(s) == 6 && strings.EqualFold(s[:5], "group") {
		s = s[5:]
	} else if t.kind != tInt {
		p.fail(t, "expected a group (Group1 to Group4), found %s", describe(t))
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxGroups {
		p.fail(t, "the group %s is not 1 to %d", t.text, maxGroups)
	}
	return n
}

// rawUntil collects the tokens up to the first stop at bracket depth 0 (not consumed) as text.
func (p *parser) rawUntil(stops ...string) string {
	var parts []string
	depth := 0
	for {
		t := p.peek()
		if t.kind == tEOF {
			p.fail(t, "the statement is not closed")
		}
		if depth == 0 && t.kind == tPunct {
			for _, s := range stops {
				if t.text == s {
					return strings.Join(parts, " ")
				}
			}
		}
		if t.kind == tPunct {
			switch t.text {
			case "{", "[", "(":
				depth++
				if depth > maxDepth {
					p.fail(t, "brackets nested too deeply")
				}
			case "}", "]", ")":
				depth--
				if depth < 0 {
					p.fail(t, "unbalanced %q", t.text)
				}
			}
		}
		parts = append(parts, p.tokenText(t))
		p.next()
	}
}

func (p *parser) tokenText(t token) string {
	switch t.kind {
	case tString:
		return quote(p.str(t))
	case tKeyName:
		return "<" + t.text + ">"
	}
	return t.text
}

func (p *parser) key() *Key {
	t := p.next()
	if t.kind != tKeyName {
		p.fail(t, "expected a key name <...>, found %s", describe(t))
	}
	k := &Key{Name: t.text}
	p.expect("{")
	implicit := 0
	for !p.is(p.peek(), "}") {
		it := p.peek()
		switch {
		case p.is(it, "["):
			implicit++
			if implicit > maxGroups {
				p.fail(it, "more than %d groups", maxGroups)
			}
			g := k.group(implicit)
			g.Levels, g.HasSyms = p.levels(), true
		case it.kind == tIdent:
			p.next()
			field := it.text
			gi := 0
			if p.is(p.peek(), "[") {
				p.next()
				gi = p.groupIndex()
				p.expect("]")
			}
			p.expect("=")
			switch strings.ToLower(field) {
			case "symbols":
				if gi == 0 {
					gi = 1
				}
				g := k.group(gi)
				g.Levels, g.HasSyms = p.levels(), true
			case "type":
				s := p.next()
				if s.kind != tString {
					p.fail(s, "a key type is a string")
				}
				if gi == 0 {
					// type = "T" applies to every group of the key
					k.Props = append(k.Props, Prop{Name: "type", Value: quote(p.str(s))})
				} else {
					k.group(gi).Type = p.str(s)
				}
			case "actions":
				if gi == 0 {
					gi = 1
				}
				p.expect("[")
				g := k.group(gi)
				g.Actions, g.HasActs = p.rawUntil("]"), true
				p.expect("]")
			default:
				name := field
				if gi > 0 {
					name = fmt.Sprintf("%s[Group%d]", field, gi)
				}
				k.Props = append(k.Props, Prop{Name: name, Value: p.rawUntil(",", "}")})
			}
		default:
			p.fail(it, "unexpected %s in the key <%s>", describe(it), k.Name)
		}
		if p.is(p.peek(), ",") {
			p.next()
		} else if !p.is(p.peek(), "}") {
			p.fail(p.peek(), "expected \",\" or \"}\" in the key <%s>, found %s", k.Name, describe(p.peek()))
		}
	}
	p.next()
	p.expect(";")
	return k
}

// group returns the group n (1-based), growing the list.
func (k *Key) group(n int) *Group {
	for len(k.Groups) < n {
		k.Groups = append(k.Groups, Group{})
	}
	return &k.Groups[n-1]
}

// levels reads [ sym, {sym, sym}, "text", ... ].
func (p *parser) levels() []Level {
	p.expect("[")
	ls := []Level{}
	if p.is(p.peek(), "]") {
		p.next()
		return ls
	}
	for {
		if len(ls) >= maxLevels {
			p.fail(p.peek(), "more than %d levels", maxLevels)
		}
		t := p.peek()
		var l Level
		if p.is(t, "{") {
			p.next()
			for !p.is(p.peek(), "}") {
				l = append(l, p.syms()...)
				if len(l) > maxSyms {
					p.fail(t, "more than %d keysyms on a level", maxSyms)
				}
				if p.is(p.peek(), ",") {
					p.next()
				} else if !p.is(p.peek(), "}") {
					p.fail(p.peek(), "expected \",\" or \"}\", found %s", describe(p.peek()))
				}
			}
			p.next()
		} else {
			l = p.syms()
		}
		ls = append(ls, l.clean())
		t = p.next()
		if p.is(t, "]") {
			return ls
		}
		if !p.is(t, ",") {
			p.fail(t, "expected \",\" or \"]\", found %s", describe(t))
		}
	}
}

// clean drops NoSymbol entries of a level with several keysyms.
func (l Level) clean() Level {
	if len(l) < 2 {
		if len(l) == 1 && l[0].K == NoSymbol && l[0].Unknown == "" {
			return nil
		}
		return l
	}
	var out Level
	for _, s := range l {
		if s.K != NoSymbol || s.Unknown != "" {
			out = append(out, s)
		}
	}
	return out
}

// syms reads one keysym, or a string (one keysym per character).
func (p *parser) syms() Level {
	t := p.next()
	switch t.kind {
	case tString:
		s := p.str(t)
		if s == "" {
			return nil
		}
		var l Level
		for _, r := range s {
			l = append(l, Sym{K: FromRune(r)})
		}
		if len(l) > maxSyms {
			p.fail(t, "more than %d keysyms on a level", maxSyms)
		}
		return l
	case tIdent, tInt:
		k, ok := KeysymFromName(t.text)
		if !ok {
			p.r.Warnf("xkbsymbols.keysymUnknown", loss.Where{File: p.name, Line: t.line}, "%q names no keysym; it is kept as written and types nothing", t.text)
			return Level{{Unknown: t.text}}
		}
		return Level{{K: k}}
	}
	p.fail(t, "expected a keysym, found %s", describe(t))
	return nil
}

func (p *parser) modMap() *ModMapDef {
	t := p.next()
	if t.kind != tIdent {
		p.fail(t, "expected a modifier name, found %s", describe(t))
	}
	m := &ModMapDef{Mod: t.text}
	p.expect("{")
	for !p.is(p.peek(), "}") {
		k := p.next()
		switch k.kind {
		case tKeyName:
			m.Keys = append(m.Keys, "<"+k.text+">")
		case tIdent, tInt:
			m.Keys = append(m.Keys, k.text)
		default:
			p.fail(k, "expected a key or keysym in modifier_map, found %s", describe(k))
		}
		if p.is(p.peek(), ",") {
			p.next()
		} else if !p.is(p.peek(), "}") {
			p.fail(p.peek(), "expected \",\" or \"}\", found %s", describe(p.peek()))
		}
	}
	p.next()
	p.expect(";")
	return m
}

// Section returns the section of the given name, or for "" the default section (the one flagged default, else
// the first).
func (f *File) Section(name string) *Section {
	if name == "" {
		for _, s := range f.Sections {
			for _, fl := range s.Flags {
				if fl == "default" {
					return s
				}
			}
		}
		return f.Sections[0]
	}
	for _, s := range f.Sections {
		if s.Name == name {
			return s
		}
	}
	return nil
}
