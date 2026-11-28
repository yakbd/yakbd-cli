package keylayout

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The .keylayout files of macOS are XML 1.1 (Apple writes C0 control characters as character references, which
// XML 1.0 forbids), and the standard library's decoder accepts neither the version nor those references. This is
// a small reader for the subset the format uses: a declaration, comments, a DOCTYPE without an internal subset
// of any depth, elements with quoted attributes, whitespace between elements. Text content is not part of the
// format; non-blank text is an error.

// node is one element.
type node struct {
	name     string
	attrs    []attr
	children []*node
	line     int
}

type attr struct {
	name, value string
}

// get returns an attribute's value and whether it is present.
func (n *node) get(name string) (string, bool) {
	for _, a := range n.attrs {
		if a.name == name {
			return a.value, true
		}
	}
	return "", false
}

// syntaxError is an XML error at a line.
type syntaxError struct {
	line int
	msg  string
}

func (e *syntaxError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

const (
	maxDepth = 32
	maxNodes = 1 << 20
)

type xmlReader struct {
	s     string
	i     int
	line  int
	nodes int
}

// decodeText returns the document as UTF-8 text: UTF-8 (with or without a byte-order mark) or UTF-16 with a
// byte-order mark, as TN2056 allows.
func decodeText(b []byte) (string, error) {
	switch {
	case len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF):
		if len(b)%2 != 0 {
			return "", errors.New("UTF-16 text with an odd number of bytes")
		}
		le := b[0] == 0xFF
		u := make([]uint16, 0, len(b)/2-1)
		for i := 2; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		for i := 0; i < len(u); i++ {
			c := u[i]
			if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
				i++
				continue
			}
			if c >= 0xD800 && c <= 0xDFFF {
				return "", errors.New("UTF-16 text with an unpaired surrogate")
			}
		}
		return string(utf16.Decode(u)), nil
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		b = b[3:]
	}
	if !utf8.Valid(b) {
		return "", errors.New("the file is neither UTF-8 nor UTF-16 with a byte-order mark")
	}
	return string(b), nil
}

// parseXML returns the root element.
func parseXML(text string) (*node, error) {
	x := &xmlReader{s: text, line: 1}
	var root *node
	for {
		x.skipSpace()
		if x.i >= len(x.s) {
			break
		}
		switch {
		case x.has("<?"):
			if err := x.decl(root == nil); err != nil {
				return nil, err
			}
		case x.has("<!--"):
			if err := x.comment(); err != nil {
				return nil, err
			}
		case x.has("<!DOCTYPE"):
			if root != nil {
				return nil, x.errf("DOCTYPE after the root element")
			}
			if err := x.doctype(); err != nil {
				return nil, err
			}
		case x.has("<"):
			if root != nil {
				return nil, x.errf("a second root element")
			}
			n, err := x.element(0)
			if err != nil {
				return nil, err
			}
			root = n
		default:
			return nil, x.errf("text outside the root element")
		}
	}
	if root == nil {
		return nil, x.errf("no root element")
	}
	return root, nil
}

func (x *xmlReader) errf(format string, args ...any) error {
	return &syntaxError{x.line, fmt.Sprintf(format, args...)}
}

func (x *xmlReader) has(p string) bool { return strings.HasPrefix(x.s[x.i:], p) }

func (x *xmlReader) advance(n int) {
	x.line += strings.Count(x.s[x.i:x.i+n], "\n")
	x.i += n
}

func (x *xmlReader) skipSpace() {
	for x.i < len(x.s) {
		switch x.s[x.i] {
		case '\n':
			x.line++
			fallthrough
		case ' ', '\t', '\r':
			x.i++
		default:
			return
		}
	}
}

// decl reads a processing instruction; the XML declaration must be first and name version 1.0 or 1.1.
func (x *xmlReader) decl(first bool) error {
	end := strings.Index(x.s[x.i+2:], "?>")
	if end < 0 {
		return x.errf("unterminated processing instruction")
	}
	body := x.s[x.i+2 : x.i+2+end]
	if strings.HasPrefix(body, "xml ") || body == "xml" {
		if !first || x.i != 0 {
			return x.errf("the XML declaration is not at the start")
		}
		v := declValue(body, "version")
		if v != "1.0" && v != "1.1" {
			return x.errf("XML version %q (1.0 or 1.1 expected)", v)
		}
		if e := strings.ToUpper(declValue(body, "encoding")); e != "" && e != "UTF-8" && e != "UTF-16" {
			return x.errf("encoding %q (UTF-8 or UTF-16 expected)", e)
		}
	}
	x.advance(end + 4)
	return nil
}

func declValue(body, name string) string {
	i := strings.Index(body, name+"=")
	if i < 0 {
		return ""
	}
	r := body[i+len(name)+1:]
	if len(r) < 2 || (r[0] != '"' && r[0] != '\'') {
		return ""
	}
	j := strings.IndexByte(r[1:], r[0])
	if j < 0 {
		return ""
	}
	return r[1 : j+1]
}

func (x *xmlReader) comment() error {
	end := strings.Index(x.s[x.i+4:], "-->")
	if end < 0 {
		return x.errf("unterminated comment")
	}
	x.advance(4 + end + 3)
	return nil
}

func (x *xmlReader) doctype() error {
	depth := 0
	for j := x.i; j < len(x.s); j++ {
		switch x.s[j] {
		case '[':
			depth++
		case ']':
			depth--
		case '>':
			if depth <= 0 {
				x.advance(j + 1 - x.i)
				return nil
			}
		}
	}
	return x.errf("unterminated DOCTYPE")
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' ||
		c == '.' || c == ':' || c >= 0x80
}

func (x *xmlReader) name() string {
	j := x.i
	for j < len(x.s) && isNameByte(x.s[j]) {
		j++
	}
	n := x.s[x.i:j]
	x.i = j
	return n
}

// element reads one element starting at '<'.
func (x *xmlReader) element(depth int) (*node, error) {
	if depth >= maxDepth {
		return nil, x.errf("elements nested deeper than %d", maxDepth)
	}
	x.nodes++
	if x.nodes > maxNodes {
		return nil, x.errf("more than %d elements", maxNodes)
	}
	n := &node{line: x.line}
	x.i++ // '<'
	n.name = x.name()
	if n.name == "" {
		return nil, x.errf("an element without a name")
	}
	for {
		before := x.i
		x.skipSpace()
		if x.i >= len(x.s) {
			return nil, x.errf("unterminated start tag <%s>", n.name)
		}
		if x.has("/>") {
			x.i += 2
			return n, nil
		}
		if x.s[x.i] == '>' {
			x.i++
			break
		}
		if x.i == before {
			return nil, x.errf("no space before an attribute of <%s>", n.name)
		}
		a, err := x.attribute()
		if err != nil {
			return nil, err
		}
		if _, dup := n.get(a.name); dup {
			return nil, x.errf("attribute %s given twice", a.name)
		}
		n.attrs = append(n.attrs, a)
	}
	for {
		j := x.i
		for j < len(x.s) && x.s[j] != '<' {
			j++
		}
		if t := x.s[x.i:j]; strings.TrimSpace(t) != "" {
			return nil, x.errf("text %q inside <%s>", clip(strings.TrimSpace(t)), n.name)
		}
		x.advance(j - x.i)
		if x.i >= len(x.s) {
			return nil, x.errf("element <%s> is not closed", n.name)
		}
		switch {
		case x.has("</"):
			x.i += 2
			if got := x.name(); got != n.name {
				return nil, x.errf("</%s> closes <%s>", got, n.name)
			}
			x.skipSpace()
			if !x.has(">") {
				return nil, x.errf("malformed end tag </%s>", n.name)
			}
			x.i++
			return n, nil
		case x.has("<!--"):
			if err := x.comment(); err != nil {
				return nil, err
			}
		case x.has("<![CDATA["), x.has("<?"), x.has("<!"):
			return nil, x.errf("unexpected markup inside <%s>", n.name)
		default:
			c, err := x.element(depth + 1)
			if err != nil {
				return nil, err
			}
			n.children = append(n.children, c)
		}
	}
}

func (x *xmlReader) attribute() (attr, error) {
	var a attr
	a.name = x.name()
	if a.name == "" {
		return a, x.errf("malformed attribute")
	}
	x.skipSpace()
	if !x.has("=") {
		return a, x.errf("attribute %s without a value", a.name)
	}
	x.i++
	x.skipSpace()
	if x.i >= len(x.s) || (x.s[x.i] != '"' && x.s[x.i] != '\'') {
		return a, x.errf("attribute %s: value not quoted", a.name)
	}
	q := x.s[x.i]
	end := strings.IndexByte(x.s[x.i+1:], q)
	if end < 0 {
		return a, x.errf("attribute %s: unterminated value", a.name)
	}
	raw := x.s[x.i+1 : x.i+1+end]
	line := x.line
	x.advance(end + 2)
	v, err := unescape(raw)
	if err != nil {
		return a, &syntaxError{line, fmt.Sprintf("attribute %s: %v", a.name, err)}
	}
	a.value = v
	return a, nil
}

// unescape decodes an attribute value: character references (a high and a low surrogate written as two references
// join into one character), the five predefined entities, and the XML rule that a literal tab, line feed or
// carriage return becomes a space.
func unescape(raw string) (string, error) {
	if !strings.ContainsAny(raw, "&<\t\n\r") {
		return raw, nil
	}
	var b strings.Builder
	pendingHigh := rune(-1)
	flush := func() error {
		if pendingHigh >= 0 {
			return fmt.Errorf("an unpaired surrogate &#x%X;", pendingHigh)
		}
		return nil
	}
	for i := 0; i < len(raw); {
		c := raw[i]
		switch c {
		case '<':
			return "", errors.New("'<' in a value")
		case '\t', '\n', '\r':
			if err := flush(); err != nil {
				return "", err
			}
			if c == '\r' && i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
			b.WriteByte(' ')
			i++
			continue
		case '&':
			end := strings.IndexByte(raw[i:], ';')
			if end < 0 {
				return "", errors.New("'&' without ';'")
			}
			ref := raw[i+1 : i+end]
			i += end + 1
			var r rune
			switch ref {
			case "amp":
				r = '&'
			case "lt":
				r = '<'
			case "gt":
				r = '>'
			case "quot":
				r = '"'
			case "apos":
				r = '\''
			default:
				if !strings.HasPrefix(ref, "#") {
					return "", fmt.Errorf("entity &%s; (only character references and the five XML entities)", clip(ref))
				}
				num, base := ref[1:], 10
				if strings.HasPrefix(num, "x") {
					num, base = num[1:], 16
				}
				if num == "" || len(num) > 8 {
					return "", fmt.Errorf("character reference &%s;", clip(ref))
				}
				v, err := strconv.ParseUint(num, base, 32)
				if err != nil || v == 0 || v > unicode10FFFF {
					return "", fmt.Errorf("character reference &%s;", clip(ref))
				}
				r = rune(v)
			}
			if r >= 0xD800 && r <= 0xDBFF {
				if err := flush(); err != nil {
					return "", err
				}
				pendingHigh = r
				continue
			}
			if r >= 0xDC00 && r <= 0xDFFF {
				if pendingHigh < 0 {
					return "", fmt.Errorf("an unpaired surrogate &#x%X;", r)
				}
				r = utf16.DecodeRune(pendingHigh, r)
				pendingHigh = -1
			} else if err := flush(); err != nil {
				return "", err
			}
			b.WriteRune(r)
			continue
		}
		if err := flush(); err != nil {
			return "", err
		}
		b.WriteByte(c)
		i++
	}
	if err := flush(); err != nil {
		return "", err
	}
	return b.String(), nil
}

const unicode10FFFF = 0x10FFFF

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}
