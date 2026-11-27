package model

import (
	"strconv"
	"strings"
)

// Pointer joins reference tokens into a JSON pointer (RFC 6901): "/" before each token, "~" written "~0" and "/"
// written "~1". Pointer() is "", the whole document.
func Pointer(tokens ...string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(Escape(t))
	}
	return b.String()
}

// Escape writes one reference token of a JSON pointer: "~" as "~0", "/" as "~1". Ids (key, flick, form, layer and
// dead-key ids, SPEC §2.5) may hold both.
func Escape(t string) string {
	if strings.ContainsAny(t, "~/") {
		t = strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1")
	}
	return t
}

// BindingPointer is the JSON pointer of binding b of position pos: /hardware/layers/<i>/bindings/<pos> of the
// hardware layer that holds it, or /positions/<pos> when none does (nothing bound, or a binding made by a
// conversion).
func (l *Layout) BindingPointer(pos string, b *Binding) string {
	if l.Hardware != nil && b != nil {
		for i, layer := range l.Hardware.Layers {
			if layer.Bindings[pos] == b {
				return Pointer("hardware", "layers", strconv.Itoa(i), "bindings", pos)
			}
		}
	}
	return Pointer("positions", pos)
}
