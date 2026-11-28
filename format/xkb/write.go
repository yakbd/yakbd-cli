package xkb

import (
	"fmt"
	"strings"
)

// WriteOptions control how Write spells keysyms.
type WriteOptions struct {
	// UTF8 writes the keysyms of a level that types characters as a UTF-8 string ("é"), which needs libxkbcommon
	// 1.9 or later and is refused by xkbcomp; otherwise keysym names and Uxxxx are written.
	UTF8 bool
	// Header lines are written first, each as a // comment.
	Header []string
}

// Write writes a symbols file in one fixed form: one statement per line, four spaces of indentation, keysyms by
// their canonical names. Write → Read → Write is byte-stable.
func Write(f *File, o WriteOptions) []byte {
	var b strings.Builder
	for _, h := range o.Header {
		b.WriteString(strings.TrimRight("// "+h, " ") + "\n")
	}
	if len(o.Header) > 0 {
		b.WriteString("\n")
	}
	for i, s := range f.Sections {
		if i > 0 {
			b.WriteString("\n")
		}
		if len(s.Flags) > 0 {
			b.WriteString(strings.Join(s.Flags, " ") + "\n")
		}
		fmt.Fprintf(&b, "xkb_symbols %s {\n", quote(s.Name))
		for _, st := range s.Stmts {
			b.WriteString("    ")
			b.WriteString(stmtText(st, o))
			b.WriteString("\n")
		}
		b.WriteString("};\n")
	}
	return []byte(b.String())
}

func stmtText(st *Stmt, o WriteOptions) string {
	switch st.Kind {
	case Include:
		return st.Merge + " " + quote(st.Text)
	case GroupName:
		return fmt.Sprintf("name[Group%d] = %s;", st.Group, quote(st.Text))
	case KeyDef:
		s := KeyText(st.Key, o) + ";"
		if st.Merge != "" {
			s = st.Merge + " " + s
		}
		return s
	case ModMap:
		s := fmt.Sprintf("modifier_map %s { %s };", st.ModMap.Mod, strings.Join(st.ModMap.Keys, ", "))
		if st.Merge != "" {
			s = st.Merge + " " + s
		}
		return s
	case KeyDefault:
		return "key." + st.Text + ";"
	case VirtualMods:
		return "virtual_modifiers " + st.Text + ";"
	}
	return st.Text + ";"
}

// KeyText writes a key definition without the final semicolon: key <AE01> { [ 1, exclam ] }.
func KeyText(k *Key, o WriteOptions) string {
	var items []string
	simple := len(k.Groups) == 1 && len(k.Props) == 0 && k.Groups[0].Type == "" && !k.Groups[0].HasActs && k.Groups[0].HasSyms
	for _, p := range k.Props {
		if p.Name == "type" {
			items = append(items, "type = "+p.Value)
		}
	}
	for i, g := range k.Groups {
		n := i + 1
		if g.Type != "" {
			items = append(items, fmt.Sprintf("type[Group%d] = %s", n, quote(g.Type)))
		}
		if g.HasSyms {
			if simple {
				items = append(items, levelsText(g.Levels, o))
			} else {
				items = append(items, fmt.Sprintf("symbols[Group%d] = %s", n, levelsText(g.Levels, o)))
			}
		}
		if g.HasActs {
			items = append(items, fmt.Sprintf("actions[Group%d] = [ %s ]", n, g.Actions))
		}
	}
	for _, p := range k.Props {
		if p.Name != "type" {
			items = append(items, p.Name+" = "+p.Value)
		}
	}
	if len(items) == 0 {
		return fmt.Sprintf("key <%s> { }", k.Name)
	}
	return fmt.Sprintf("key <%s> { %s }", k.Name, strings.Join(items, ", "))
}

func levelsText(ls []Level, o WriteOptions) string {
	if len(ls) == 0 {
		return "[ ]"
	}
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = LevelText(l, o)
	}
	return "[ " + strings.Join(parts, ", ") + " ]"
}

// LevelText writes one level: NoSymbol, a keysym, { a, b } for several, or with UTF8 a string for characters.
func LevelText(l Level, o WriteOptions) string {
	if len(l) == 0 {
		return "NoSymbol"
	}
	if o.UTF8 {
		if s, ok := l.Text(); ok && printable(s) && sameKeysyms(s, l) {
			return quote(s)
		}
	}
	if len(l) == 1 {
		return symText(l[0])
	}
	parts := make([]string, len(l))
	for i, s := range l {
		parts[i] = symText(s)
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// sameKeysyms reports whether the string s reads back as the keysyms of l.
func sameKeysyms(s string, l Level) bool {
	i := 0
	for _, r := range s {
		if i >= len(l) || l[i].K != FromRune(r) {
			return false
		}
		i++
	}
	return i == len(l)
}

func printable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return s != ""
}

func symText(s Sym) string {
	if s.Unknown != "" {
		return s.Unknown
	}
	return s.K.Name()
}

// Text returns the characters a level types, and false when one of its keysyms types none.
func (l Level) Text() (string, bool) {
	if len(l) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, s := range l {
		r, ok := s.K.Rune()
		if !ok || s.Unknown != "" {
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), true
}
