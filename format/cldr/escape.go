package cldr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yakbd/yakbd-cli/model"
)

// decodeEscapes decodes CLDR's \u{h h ...} escapes in a text (outputs, displays, test contexts). Other text,
// including a backslash not followed by u{, is kept as it is.
func decodeEscapes(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], `\u{`) {
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("unterminated escape in %q", s)
			}
			hex := strings.Fields(s[i+3 : i+end])
			if len(hex) == 0 {
				return "", fmt.Errorf("empty escape in %q", s)
			}
			for _, h := range hex {
				v, err := strconv.ParseUint(h, 16, 32)
				if err != nil || v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) {
					return "", fmt.Errorf("bad escape \\u{%s} in %q", h, s)
				}
				b.WriteRune(rune(v))
			}
			i += end + 1
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += n
	}
	return b.String(), nil
}

// parseMarked parses an output or display attribute into a MarkedText: \m{id} markers, ${id} string variables,
// and text with its \u{} escapes decoded.
func parseMarked(s string) (model.MarkedText, error) {
	var items []model.MarkedItem
	var text strings.Builder
	flush := func() error {
		if text.Len() == 0 {
			return nil
		}
		t, err := decodeEscapes(text.String())
		if err != nil {
			return err
		}
		items = append(items, model.MarkedItem{Text: t})
		text.Reset()
		return nil
	}
	for i := 0; i < len(s); {
		marker := strings.HasPrefix(s[i:], `\m{`)
		variable := strings.HasPrefix(s[i:], `${`)
		if !marker && !variable {
			r, n := utf8.DecodeRuneInString(s[i:])
			text.WriteRune(r)
			i += n
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return model.MarkedText{}, fmt.Errorf("unterminated marker or variable in %q", s)
		}
		if err := flush(); err != nil {
			return model.MarkedText{}, err
		}
		if marker {
			items = append(items, model.MarkedItem{Marker: s[i+3 : i+end]})
		} else {
			items = append(items, model.MarkedItem{Variable: s[i+2 : i+end]})
		}
		i += end + 1
	}
	if err := flush(); err != nil {
		return model.MarkedText{}, err
	}
	if len(items) == 0 {
		items = []model.MarkedItem{{Text: ""}}
	}
	m := model.MarkedText{Items: items}
	if _, plain := m.PlainText(); !plain {
		m.Array = true
	}
	return m, nil
}

// needsEscape reports the characters the writer writes as \u{...}: a backslash, a $ before {, controls and the
// characters XML 1.0 cannot hold.
func needsEscape(r, next rune) bool {
	return r == '\\' || (r == '$' && next == '{') || r < 0x20 || (r >= 0x7F && r < 0xA0) || r == 0xFFFE || r == 0xFFFF
}

// encodeText writes a text for a CLDR output, display or test attribute.
func encodeText(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		if needsEscape(r, next) {
			fmt.Fprintf(&b, `\u{%X}`, r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// encodeMarked writes a MarkedText as a CLDR attribute value.
func encodeMarked(m model.MarkedText) string {
	var b strings.Builder
	for i, it := range m.Items {
		switch {
		case it.Marker != "":
			b.WriteString(`\m{` + it.Marker + `}`)
		case it.Variable != "":
			b.WriteString(`${` + it.Variable + `}`)
		default:
			t := encodeText(it.Text)
			// A literal $ at the end of a text item followed by a variable item would read as "${".
			if strings.HasSuffix(it.Text, "$") && i+1 < len(m.Items) {
				t = strings.TrimSuffix(t, "$") + `\u{24}`
			}
			b.WriteString(t)
		}
	}
	return b.String()
}
