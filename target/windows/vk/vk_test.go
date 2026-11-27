package vk

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		tok  string
		want byte
		ok   bool
	}{
		{"A", 'A', true}, {"a", 'A', true}, {"7", '7', true},
		{"OEM_1", 0xBA, true}, {"oem_1", 0xBA, true}, {"VK_OEM_1", 0xBA, true}, {"vk_space", 0x20, true},
		{"0xdf", 0xDF, true}, {"0XDF", 0xDF, true}, {"0x1ff", 0, false}, {"0x", 0, false},
		{"NOPE", 0, false}, {"VK_A", 0, false}, {"-", 0, false}, {"_none_", 0xFF, true},
		{"KANA", 0x15, true}, {"HANGUL", 0x15, true},
	}
	for _, tt := range tests {
		got, ok := Parse(tt.tok)
		if ok != tt.ok || got != tt.want {
			t.Errorf("Parse(%q) = %#x, %v; want %#x, %v", tt.tok, got, ok, tt.want, tt.ok)
		}
	}
}

func TestName(t *testing.T) {
	tests := []struct {
		v    byte
		want string
	}{
		{'A', "A"}, {'0', "0"}, {0xBA, "OEM_1"}, {0x0D, "RETURN"}, {0x15, "HANGEUL"}, {0x07, "0x07"},
		{0x2E, "DELETE"}, {0x6E, "DECIMAL"},
	}
	for _, tt := range tests {
		if got := Name(tt.v); got != tt.want {
			t.Errorf("Name(%#x) = %q, want %q", tt.v, got, tt.want)
		}
	}
	for v := 0; v < 256; v++ {
		if Named(byte(v)) {
			back, ok := Parse(Name(byte(v)))
			if !ok || back != byte(v) {
				t.Errorf("round trip of %#x via %q gave %#x %v", v, Name(byte(v)), back, ok)
			}
		}
	}
}

func TestDefaults(t *testing.T) {
	seen := map[uint16]bool{}
	for _, d := range Defaults {
		if seen[d.Scan] {
			t.Errorf("scan code %#x twice", d.Scan)
		}
		seen[d.Scan] = true
		switch d.Scan >> 8 {
		case PrefixNone:
			if d.Scan >= MaxVSC {
				t.Errorf("scan code %#x beyond the table", d.Scan)
			}
		case PrefixE0:
			if d.VK&KBDEXT == 0 {
				t.Errorf("E0 entry %#x without KBDEXT", d.Scan)
			}
		case PrefixE1:
		default:
			t.Errorf("bad prefix %#x", d.Scan)
		}
	}
	if v, ok := DefaultFor(0x10); !ok || v != 'Q' {
		t.Errorf("DefaultFor(0x10) = %#x %v", v, ok)
	}
	if _, ok := DefaultFor(0x55); ok {
		t.Error("0x55 has a default")
	}
}
