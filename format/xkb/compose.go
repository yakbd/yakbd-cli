package xkb

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yakbd/yakbd-cli/loss"
)

// Compose is a Compose file (the XCompose format libxkbcommon, GTK and X11 read): include lines and sequences, in
// order.
type Compose struct {
	Lines []ComposeLine
}

// ComposeLine is an include or a sequence.
type ComposeLine struct {
	Include string   // include "%L": the include string ("" for a sequence)
	Seq     []Keysym // the keys of the sequence
	Result  string   // the text the sequence types
	Keysym  Keysym   // the keysym the sequence gives, NoSymbol for none
	Line    int
}

// maxSeq bounds a sequence (libxkbcommon allows 20 keys).
const maxSeq = 20

// ReadCompose reads a Compose file. Lines it cannot use (modifier prefixes, unknown keysyms, longer sequences)
// are skipped with a warning (xkbcompose.lineSkipped), as libxkbcommon skips them; a broken string is a
// syntax error (xkbcompose.syntax).
func ReadCompose(data []byte, name string) (*Compose, *loss.Report) {
	r := &loss.Report{}
	if !utf8.Valid(data) {
		r.Errorf("xkbcompose.syntax", loss.Where{File: name, Line: 1}, "the file is not UTF-8")
		return nil, r
	}
	c := &Compose{}
	for i, raw := range strings.Split(strings.TrimPrefix(string(data), "\uFEFF"), "\n") {
		line := i + 1
		at := loss.Where{File: name, Line: line}
		s := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if s == "" || s[0] == '#' {
			continue
		}
		if strings.HasPrefix(s, "include") && len(s) > 7 && (s[7] == ' ' || s[7] == '\t' || s[7] == '"') {
			rest := strings.TrimSpace(s[7:])
			v, n, err := unquoteCompose(rest, line)
			if err != nil {
				r.Errorf("xkbcompose.syntax", at, "%v", err.(*lexError).msg)
				return nil, r
			}
			if tail := strings.TrimSpace(rest[n:]); tail != "" && tail[0] != '#' {
				r.Warnf("xkbcompose.lineSkipped", at, "text after the include string")
				continue
			}
			c.Lines = append(c.Lines, ComposeLine{Include: v, Line: line})
			continue
		}
		colon := strings.IndexByte(s, ':')
		if colon < 0 {
			r.Warnf("xkbcompose.lineSkipped", at, "no ':' on the line")
			continue
		}
		lhs, rhs := strings.TrimSpace(s[:colon]), strings.TrimSpace(s[colon+1:])
		var seq []Keysym
		ok := true
		for lhs != "" {
			if lhs[0] != '<' {
				r.Warnf("xkbcompose.lineSkipped", at, "only <keysym> events are kept (no modifier prefixes)")
				ok = false
				break
			}
			end := strings.IndexByte(lhs, '>')
			if end < 0 {
				r.Warnf("xkbcompose.lineSkipped", at, "unterminated <keysym>")
				ok = false
				break
			}
			k, known := KeysymFromName(lhs[1:end])
			if !known || k == NoSymbol {
				r.Warnf("xkbcompose.lineSkipped", at, "%q names no keysym", lhs[1:end])
				ok = false
				break
			}
			seq = append(seq, k)
			lhs = strings.TrimSpace(lhs[end+1:])
		}
		if !ok {
			continue
		}
		if len(seq) == 0 || len(seq) > maxSeq {
			r.Warnf("xkbcompose.lineSkipped", at, "a sequence has 1 to %d keys", maxSeq)
			continue
		}
		l := ComposeLine{Seq: seq, Line: line}
		if strings.HasPrefix(rhs, "\"") {
			v, n, err := unquoteCompose(rhs, line)
			if err != nil {
				r.Errorf("xkbcompose.syntax", at, "%v", err.(*lexError).msg)
				return nil, r
			}
			if !utf8.ValidString(v) || v == "" {
				r.Warnf("xkbcompose.lineSkipped", at, "the result is empty or not UTF-8")
				continue
			}
			l.Result = v
			rhs = strings.TrimSpace(rhs[n:])
		}
		if rhs != "" && rhs[0] != '#' {
			f := strings.Fields(rhs)[0]
			k, known := KeysymFromName(f)
			if !known {
				r.Warnf("xkbcompose.lineSkipped", at, "%q names no keysym", f)
				continue
			}
			l.Keysym = k
		}
		if l.Result == "" && l.Keysym == NoSymbol {
			r.Warnf("xkbcompose.lineSkipped", at, "the sequence gives nothing")
			continue
		}
		c.Lines = append(c.Lines, l)
	}
	return c, r
}

// unquoteCompose decodes a Compose string: as unquote, plus \x hex escapes.
func unquoteCompose(s string, line int) (string, int, error) {
	if !strings.HasPrefix(s, "\"") {
		return "", 0, &lexError{line, "expected a string"}
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		if c == '"' {
			return b.String(), i + 1, nil
		}
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		e := s[i+1]
		if e == 'x' || e == 'X' {
			j, v := i+2, 0
			for n := 0; n < 2 && j < len(s) && isHex(s[j]); n++ {
				v = v*16 + hexVal(s[j])
				j++
			}
			if j == i+2 {
				return "", 0, &lexError{line, `\x needs hex digits`}
			}
			b.WriteByte(byte(v))
			i = j
			continue
		}
		one, m, err2 := unquote("\""+s[i:i+escLen(s[i:])]+"\"", line)
		if err2 != nil {
			return "", 0, err2
		}
		b.WriteString(one)
		i += m - 2
	}
	return "", 0, &lexError{line, "unterminated string"}
}

// escLen is the length of the escape at the start of s ("\\n", "\\123", "\\u{20ac}").
func escLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	e := s[1]
	switch {
	case e >= '0' && e <= '7':
		n := 2
		for n < 4 && n < len(s) && s[n] >= '0' && s[n] <= '7' {
			n++
		}
		return n
	case e == 'u':
		if end := strings.IndexByte(s, '}'); end > 0 && end < 12 {
			return end + 1
		}
	}
	return 2
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return int(c-'A') + 10
}

// WriteCompose writes a Compose file in one fixed form: the header lines as # comments, then one line per
// include or sequence: <dead_acute> <a> : "á" aacute. Write → Read → Write is byte-stable.
func WriteCompose(c *Compose, header []string) []byte {
	var b strings.Builder
	for _, h := range header {
		b.WriteString(strings.TrimRight("# "+h, " ") + "\n")
	}
	for _, l := range c.Lines {
		if l.Seq == nil {
			fmt.Fprintf(&b, "include %s\n", quote(l.Include))
			continue
		}
		for i, k := range l.Seq {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("<" + k.Name() + ">")
		}
		b.WriteString(" :")
		if l.Result != "" {
			b.WriteString(" " + quote(l.Result))
		}
		if l.Keysym != NoSymbol {
			b.WriteString(" " + l.Keysym.Name())
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
