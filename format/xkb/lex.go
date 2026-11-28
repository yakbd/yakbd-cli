package xkb

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// tokKind is the kind of a token of the XKB text formats.
type tokKind int

const (
	tEOF     tokKind = iota
	tIdent           // a name: letters, digits, '_'
	tInt             // a decimal or 0x number
	tString          // "..." (Text holds the decoded value)
	tKeyName         // <...> (Text holds the name without the brackets)
	tPunct           // one of { } [ ] ( ) , ; = + - ! . ~ | : (Text holds it)
)

type token struct {
	kind tokKind
	text string
	line int
}

// lexError is a syntax error at a line.
type lexError struct {
	line int
	msg  string
}

func (e *lexError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

// maxTokens bounds a file: no layout comes near it, and it keeps a hostile input linear.
const maxTokens = 1 << 20

// lex splits an XKB symbols file into tokens. Comments are //, # and /* */.
func lex(src []byte) ([]token, error) {
	if !utf8.Valid(src) {
		return nil, &lexError{1, "the file is not UTF-8"}
	}
	s := string(src)
	s = strings.TrimPrefix(s, "\uFEFF")
	var toks []token
	line := 1
	i := 0
	for i < len(s) {
		if len(toks) > maxTokens {
			return nil, &lexError{line, "too many tokens"}
		}
		c := s[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '#' || (c == '/' && i+1 < len(s) && s[i+1] == '/'):
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, &lexError{line, "unterminated comment"}
			}
			line += strings.Count(s[i:i+2+end], "\n")
			i += end + 4
		case c == '"':
			v, n, err := unquote(s[i:], line)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{tString, v, line})
			line += strings.Count(s[i:i+n], "\n")
			i += n
		case c == '<':
			end := strings.IndexByte(s[i:], '>')
			if end < 0 || strings.ContainsAny(s[i+1:i+end], " \t\n\"<") {
				return nil, &lexError{line, "unterminated key name"}
			}
			toks = append(toks, token{tKeyName, s[i+1 : i+end], line})
			i += end + 1
		case isIdentStart(c) || c >= 0x80:
			j := i
			for j < len(s) && (isIdentPart(s[j]) || s[j] >= 0x80) {
				j++
			}
			toks = append(toks, token{tIdent, s[i:j], line})
			i = j
		case c >= '0' && c <= '9':
			j := i
			if c == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') {
				j += 2
			}
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			toks = append(toks, token{tInt, s[i:j], line})
			i = j
		case strings.IndexByte("{}[](),;=+-!.~|:*", c) >= 0:
			toks = append(toks, token{tPunct, s[i : i+1], line})
			i++
		default:
			return nil, &lexError{line, fmt.Sprintf("unexpected character %q", c)}
		}
	}
	toks = append(toks, token{tEOF, "", line})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// unquote decodes the string literal at the start of s: escapes \\ \" \n \t \r \b \f \v \e, octal \ooo and
// \u{hex}. It returns the value and the number of bytes the literal takes.
func unquote(s string, line int) (string, int, error) {
	var b strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		switch c {
		case '"':
			return b.String(), i + 1, nil
		case '\n':
			return "", 0, &lexError{line, "unterminated string"}
		case '\\':
			if i+1 >= len(s) {
				return "", 0, &lexError{line, "unterminated string"}
			}
			e := s[i+1]
			i += 2
			switch e {
			case '\\', '"':
				b.WriteByte(e)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'v':
				b.WriteByte('\v')
			case 'e':
				b.WriteByte(0x1b)
			case 'u':
				if i >= len(s) || s[i] != '{' {
					return "", 0, &lexError{line, `\u needs {hex digits}`}
				}
				end := strings.IndexByte(s[i:], '}')
				if end < 0 || end > 9 {
					return "", 0, &lexError{line, `unterminated \u{...}`}
				}
				v, err := strconv.ParseUint(s[i+1:i+end], 16, 32)
				if err != nil || !utf8.ValidRune(rune(v)) {
					return "", 0, &lexError{line, `invalid \u{...}`}
				}
				b.WriteRune(rune(v))
				i += end + 1
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for n := 0; n < 2 && i < len(s) && s[i] >= '0' && s[i] <= '7'; n++ {
						v = v*8 + int(s[i]-'0')
						i++
					}
					if v > 0xff {
						return "", 0, &lexError{line, "octal escape above \\377"}
					}
					b.WriteByte(byte(v))
				} else {
					return "", 0, &lexError{line, fmt.Sprintf("unknown escape \\%c", e)}
				}
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", 0, &lexError{line, "unterminated string"}
}

// quote writes s as a string literal: \\, \" and control characters escaped (controls as octal).
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\%03o", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
