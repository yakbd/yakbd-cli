package keylayout

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Doctype is the document type line of TN2056.
const Doctype = `<!DOCTYPE keyboard SYSTEM "file://localhost/System/Library/DTDs/KeyboardLayout.dtd">`

// Write returns the keyboard as XML in one fixed form: XML 1.1 (so that C0 controls can be character
// references), UTF-8, tabs, elements and keys in the order of k, attributes in TN2056's order. Characters that are
// markup, controls (C0, DEL, C1, which includes NEL), U+2028 (a line end in XML 1.1, which a reader turns into a
// space) or the noncharacters U+FFFE and U+FFFF are written as hexadecimal character references; everything else
// is written as itself.
func Write(k *Keyboard) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.1" encoding="UTF-8"?>` + "\n")
	b.WriteString(Doctype + "\n")
	fmt.Fprintf(&b, `<keyboard group="%d" id="%d" name="%s"`, k.Group, k.ID, Escape(k.Name))
	if k.MaxOut != nil {
		fmt.Fprintf(&b, ` maxout="%d"`, *k.MaxOut)
	}
	b.WriteString(">\n\t<layouts>\n")
	for _, l := range k.Layouts {
		fmt.Fprintf(&b, "\t\t<layout first=\"%d\" last=\"%d\" modifiers=\"%s\" mapSet=\"%s\"/>\n", l.First, l.Last,
			Escape(l.Modifiers), Escape(l.MapSet))
	}
	b.WriteString("\t</layouts>\n")
	for _, m := range k.ModifierMaps {
		fmt.Fprintf(&b, "\t<modifierMap id=\"%s\" defaultIndex=\"%d\">\n", Escape(m.ID), m.DefaultIndex)
		for _, s := range m.Selects {
			fmt.Fprintf(&b, "\t\t<keyMapSelect mapIndex=\"%d\">\n", s.MapIndex)
			for _, mod := range s.Modifiers {
				fmt.Fprintf(&b, "\t\t\t<modifier keys=\"%s\"/>\n", Escape(strings.Join(mod.Keys, " ")))
			}
			b.WriteString("\t\t</keyMapSelect>\n")
		}
		b.WriteString("\t</modifierMap>\n")
	}
	for _, s := range k.KeyMapSets {
		fmt.Fprintf(&b, "\t<keyMapSet id=\"%s\">\n", Escape(s.ID))
		for _, km := range s.KeyMaps {
			fmt.Fprintf(&b, "\t\t<keyMap index=\"%d\"", km.Index)
			if km.BaseMapSet != "" {
				fmt.Fprintf(&b, ` baseMapSet="%s" baseIndex="%d"`, Escape(km.BaseMapSet), km.BaseIndex)
			}
			if len(km.Keys) == 0 {
				b.WriteString("/>\n")
				continue
			}
			b.WriteString(">\n")
			for _, key := range km.Keys {
				fmt.Fprintf(&b, "\t\t\t<key code=\"%d\"", key.Code)
				switch {
				case key.Output != nil:
					fmt.Fprintf(&b, " output=\"%s\"/>\n", Escape(*key.Output))
				case key.Action != "":
					fmt.Fprintf(&b, " action=\"%s\"/>\n", Escape(key.Action))
				case key.Inline != nil:
					b.WriteString(">\n")
					writeAction(&b, "\t\t\t\t", key.Inline)
					b.WriteString("\t\t\t</key>\n")
				default:
					b.WriteString("/>\n")
				}
			}
			b.WriteString("\t\t</keyMap>\n")
		}
		b.WriteString("\t</keyMapSet>\n")
	}
	if len(k.Actions) > 0 {
		b.WriteString("\t<actions>\n")
		for i := range k.Actions {
			writeAction(&b, "\t\t", &k.Actions[i])
		}
		b.WriteString("\t</actions>\n")
	}
	if len(k.Terminators) > 0 {
		b.WriteString("\t<terminators>\n")
		for _, w := range k.Terminators {
			writeWhen(&b, "\t\t", w)
		}
		b.WriteString("\t</terminators>\n")
	}
	b.WriteString("</keyboard>\n")
	return b.Bytes()
}

func writeAction(b *bytes.Buffer, indent string, a *Action) {
	if a.ID != "" {
		fmt.Fprintf(b, "%s<action id=\"%s\">\n", indent, Escape(a.ID))
	} else {
		fmt.Fprintf(b, "%s<action>\n", indent)
	}
	for _, w := range a.Whens {
		writeWhen(b, indent+"\t", w)
	}
	fmt.Fprintf(b, "%s</action>\n", indent)
}

func writeWhen(b *bytes.Buffer, indent string, w When) {
	fmt.Fprintf(b, "%s<when state=\"%s\"", indent, Escape(w.State))
	if w.Through != "" {
		fmt.Fprintf(b, " through=\"%s\"", Escape(w.Through))
	}
	if w.Output != nil {
		fmt.Fprintf(b, " output=\"%s\"", Escape(*w.Output))
	}
	if w.Multiplier != "" {
		fmt.Fprintf(b, " multiplier=\"%s\"", Escape(w.Multiplier))
	}
	if w.Next != "" {
		fmt.Fprintf(b, " next=\"%s\"", Escape(w.Next))
	}
	b.WriteString("/>\n")
}

// Escape returns s as the content of a double-quoted attribute value.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20, r >= 0x7F && r <= 0x9F, r == 0x2028, r == 0xFFFE, r == 0xFFFF, r == '&', r == '<', r == '>', r == '"':
			b.WriteString("&#x")
			h := strings.ToUpper(strconv.FormatInt(int64(r), 16))
			for len(h) < 4 {
				h = "0" + h
			}
			b.WriteString(h)
			b.WriteByte(';')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
