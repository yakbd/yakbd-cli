package klc

import (
	"strings"
	"unicode/utf16"
)

// line is one source line after comment removal, split into blank-delimited tokens.
type line struct {
	no    int
	units []uint16
	toks  [][2]int // start and end of each token in units
}

func isBlank(u uint16) bool { return u == ' ' || u == '\t' || u == '\v' || u == '\f' }

// splitLines splits units at CRLF, LF and CR (§1).
func splitLines(u []uint16) [][]uint16 {
	var out [][]uint16
	start := 0
	for i := 0; i < len(u); i++ {
		if u[i] == '\r' || u[i] == '\n' {
			out = append(out, u[start:i])
			if u[i] == '\r' && i+1 < len(u) && u[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(u) {
		out = append(out, u[start:])
	}
	return out
}

// isCommentLine reports a line whose first non-blank unit is ';' (§2).
func isCommentLine(u []uint16) bool {
	for _, c := range u {
		if isBlank(c) {
			continue
		}
		return c == ';'
	}
	return false
}

// cutComment removes a // comment. With quotes set, // inside "..." is text (§2).
func cutComment(u []uint16, quotes bool) []uint16 {
	in := false
	for i := 0; i+1 < len(u); i++ {
		if quotes && u[i] == '"' {
			in = !in
		}
		if !in && u[i] == '/' && u[i+1] == '/' {
			return u[:i]
		}
	}
	return u
}

func tokenize(no int, u []uint16) line {
	l := line{no: no, units: u}
	for i := 0; i < len(u); {
		for i < len(u) && isBlank(u[i]) {
			i++
		}
		if i >= len(u) {
			break
		}
		s := i
		for i < len(u) && !isBlank(u[i]) {
			i++
		}
		l.toks = append(l.toks, [2]int{s, i})
	}
	return l
}

func (l line) tok(i int) string {
	t := l.toks[i]
	return string(utf16.Decode(l.units[t[0]:t[1]]))
}

func (l line) tokUnits(i int) []uint16 {
	t := l.toks[i]
	return l.units[t[0]:t[1]]
}

// rest returns the text from token i to the end of the line, trailing blanks removed.
func (l line) rest(i int) string {
	if i >= len(l.toks) {
		return ""
	}
	u := l.units[l.toks[i][0]:]
	end := len(u)
	for end > 0 && isBlank(u[end-1]) {
		end--
	}
	return string(utf16.Decode(u[:end]))
}

// keywords of §3.
var keywords = map[string]bool{
	"KBD": true, "VERSION": true, "COPYRIGHT": true, "COMPANY": true, "LOCALENAME": true, "LOCALEID": true,
	"MODIFIERS": true, "SHIFTSTATE": true, "ATTRIBUTES": true, "LAYOUT": true, "DEADKEY": true, "LIGATURE": true,
	"KEYNAME": true, "KEYNAME_EXT": true, "KEYNAME_DEAD": true, "DESCRIPTIONS": true, "LANGUAGENAMES": true,
	"ENDKBD": true,
}

// keywordOf returns the upper-case keyword of a line, or "".
func keywordOf(u []uint16) string {
	l := tokenize(0, u)
	if len(l.toks) == 0 {
		return ""
	}
	k := strings.ToUpper(l.tok(0))
	if keywords[k] {
		return k
	}
	return ""
}
