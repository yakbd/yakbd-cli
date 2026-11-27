package klc

import (
	"errors"
	"unicode/utf16"
	"unicode/utf8"
)

// Encoding is the text encoding a file was read in.
type Encoding int

const (
	UTF16LE Encoding = iota
	UTF16BE
	UTF8
	Windows1252
)

func (e Encoding) String() string {
	return [...]string{"UTF-16LE", "UTF-16BE", "UTF-8", "Windows-1252"}[e]
}

// cp1252 maps the bytes 80 to 9F of Windows-1252 (https://encoding.spec.whatwg.org/index-windows-1252.txt). The
// five bytes without a character map to the C1 control of the same value, as the WHATWG decoder does.
var cp1252 = [32]uint16{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

var errOddLength = errors.New("UTF-16 file with an odd number of bytes")

// decode turns the bytes of a file into UTF-16 units (§1): UTF-16LE/BE or UTF-8 by byte-order mark; without one,
// NUL bytes mean UTF-16LE, else strict UTF-8, else Windows-1252.
func decode(b []byte) ([]uint16, Encoding, error) {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		u, err := utf16Units(b[2:], false)
		return u, UTF16LE, err
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		u, err := utf16Units(b[2:], true)
		return u, UTF16BE, err
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		if !utf8.Valid(b[3:]) {
			return nil, UTF8, errors.New("invalid UTF-8 after a UTF-8 byte-order mark")
		}
		return utf16.Encode([]rune(string(b[3:]))), UTF8, nil
	}
	for _, c := range b {
		if c == 0 {
			u, err := utf16Units(b, false)
			return u, UTF16LE, err
		}
	}
	if utf8.Valid(b) {
		return utf16.Encode([]rune(string(b))), UTF8, nil
	}
	u := make([]uint16, len(b))
	for i, c := range b {
		if c >= 0x80 && c < 0xA0 {
			u[i] = cp1252[c-0x80]
		} else {
			u[i] = uint16(c)
		}
	}
	return u, Windows1252, nil
}

func utf16Units(b []byte, big bool) ([]uint16, error) {
	if len(b)%2 != 0 {
		return nil, errOddLength
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		if big {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
	}
	return u, nil
}
