package klc

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// WriteOptions select a writer variant.
type WriteOptions struct {
	// ForMSKLC applies the extra writer rules for files meant for MSKLC's compiler and editor (§4.6, §10): a dead
	// character from U+1000 up is written as a literal character with @, and every construct the editor would
	// change or drop is reported as a warning.
	ForMSKLC bool
}

// Write renders f in the canonical form (§7): UTF-16LE with byte-order mark, CRLF, sections in the order of the
// grammar, tabs between fields. The report lists what the written file does not keep (the VERSION text) and,
// with ForMSKLC, what MSKLC would not accept.
func Write(f *File, opt WriteOptions) ([]byte, *loss.Report) {
	w := &writer{f: f, opt: opt, r: &loss.Report{}}
	w.all()
	units := []uint16{0xFEFF}
	for _, l := range w.lines {
		units = append(units, utf16.Encode([]rune(l))...)
		units = append(units, '\r', '\n')
	}
	out := make([]byte, 2*len(units))
	for i, u := range units {
		out[2*i] = byte(u)
		out[2*i+1] = byte(u >> 8)
	}
	return out, w.r
}

type writer struct {
	f     *File
	opt   WriteOptions
	r     *loss.Report
	lines []string
}

func (w *writer) add(format string, args ...any) {
	w.lines = append(w.lines, fmt.Sprintf(format, args...))
}
func (w *writer) blank() { w.lines = append(w.lines, "") }

func (w *writer) msklc(code, format string, args ...any) {
	if w.opt.ForMSKLC {
		w.r.Warnf("klc.msklc."+code, loss.Where{}, format, args...)
	}
}

func quote(s string) string { return `"` + s + `"` }

func hex4(u uint16) string { return fmt.Sprintf("%04x", u) }

// upperKeyword reports an upper-case keyword inside a text (OBS K34).
func upperKeyword(s string) string {
	for k := range keywords {
		if strings.Contains(s, k) {
			return k
		}
	}
	return ""
}

func (w *writer) text(what, s string) {
	if k := upperKeyword(s); k != "" {
		w.msklc("keyword-in-text", "%s %q contains the keyword %s, which the MSKLC editor reads as a keyword", what, s, k)
	}
}

func (w *writer) all() {
	f := w.f
	w.text("description", f.Description)
	w.add("KBD\t%s\t%s", f.Name, quote(f.Description))
	w.blank()
	w.add("VERSION\t1.0")
	w.blank()
	if f.Version != nil && strings.TrimSpace(*f.Version) != "1.0" {
		w.r.Lost(loss.Info, "klc.version.rewritten", loss.Where{}, "VERSION %q is written as 1.0 (§3.1)", *f.Version)
	}
	for _, x := range []struct {
		kw string
		v  *string
	}{{"COPYRIGHT", f.Copyright}, {"COMPANY", f.Company}, {"LOCALENAME", f.LocaleName}, {"LOCALEID", f.LocaleID}} {
		if x.v != nil {
			w.text(strings.ToLower(x.kw), *x.v)
			w.add("%s\t%s", x.kw, quote(*x.v))
			w.blank()
		}
	}
	if len(f.Modifiers) > 0 {
		w.add("MODIFIERS")
		w.blank()
		for _, m := range f.Modifiers {
			w.add("%s\t%s", vk.Name(m.VK), m.Bit)
		}
		w.blank()
	}
	w.add("SHIFTSTATE")
	w.blank()
	for i, s := range f.ShiftStates {
		w.add("%d\t// column %d%s", s, i, stateName(s))
	}
	w.blank()
	if len(f.Attributes) > 0 {
		w.add("ATTRIBUTES")
		for _, a := range f.Attributes {
			w.add("%s", a)
		}
		w.blank()
	}
	w.layout()
	for _, d := range f.DeadKeys {
		w.add("DEADKEY\t%s", hex4(d.Dead))
		w.blank()
		for _, e := range d.Entries {
			res := hex4(e.Result)
			if e.Chained {
				res += "@"
				w.msklc("chained", "dead key %04x: the MSKLC editor drops the chained result %04x (OBS K43)", d.Dead, e.Result)
			}
			w.add("%s\t%s", hex4(e.Base), res)
		}
		w.blank()
	}
	if len(f.Ligatures) > 0 {
		w.add("LIGATURE")
		w.blank()
		for _, lg := range f.Ligatures {
			var cs []string
			for _, c := range lg.Chars {
				cs = append(cs, hex4(c))
			}
			if len(lg.Chars) > 4 {
				w.msklc("ligature-length", "ligature of %s column %d has %d characters; the MSKLC editor keeps 4 (OBS K42)", vk.Name(lg.VK), lg.Column, len(lg.Chars))
			}
			w.add("%s\t%d\t%s", vk.Name(lg.VK), lg.Column, strings.Join(cs, "\t"))
		}
		w.blank()
	}
	names := func(kw string, list []KeyName) {
		if len(list) == 0 {
			return
		}
		w.add("%s", kw)
		w.blank()
		for _, kn := range list {
			w.add("%02x\t%s", kn.Code, w.nameText(kn.Name))
		}
		w.blank()
	}
	names("KEYNAME", f.KeyNames)
	names("KEYNAME_EXT", f.KeyNamesExt)
	if len(f.KeyNamesDead) > 0 {
		if len(f.KeyNames) == 0 || len(f.KeyNamesExt) == 0 {
			w.msklc("keyname-dead", "KEYNAME_DEAD without KEYNAME and KEYNAME_EXT is misread by the MSKLC editor (OBS K34)")
		}
		w.add("KEYNAME_DEAD")
		w.blank()
		for _, dn := range f.KeyNamesDead {
			w.add("%s\t%s", hex4(dn.Dead), w.nameText(dn.Name))
		}
		w.blank()
	}
	texts := func(kw string, list []LangText) {
		if len(list) == 0 {
			return
		}
		w.add("%s", kw)
		w.blank()
		for _, t := range list {
			w.text(strings.ToLower(kw), t.Text)
			w.add("%04x\t%s", t.LangID, t.Text)
		}
		w.blank()
	}
	texts("DESCRIPTIONS", f.Descriptions)
	texts("LANGUAGENAMES", f.LanguageNames)
	w.add("ENDKBD")
}

// nameText is the text of a key name: bare when it has no blank, else in quotes (an empty name as ""), whichever
// the reader takes back as the same name. A name no form keeps (a quote followed by //, say) is an error.
func (w *writer) nameText(s string) string {
	forms := []string{s, quote(s)}
	if s == "" || strings.ContainsAny(s, " \t") {
		forms[0], forms[1] = forms[1], forms[0]
	}
	for _, t := range forms {
		if readName(t) == s {
			return t
		}
	}
	w.r.Errorf("klc.write.name", loss.Where{}, "key name %q cannot be written so that it reads back the same", s)
	return forms[0]
}

// readName is the name the reader takes from the text of a key-name line after its code (parse.go, keyName).
func readName(t string) string {
	u := utf16.Encode([]rune("10\t" + t))
	l := tokenize(1, cutComment(u, true))
	if len(l.toks) < 2 {
		return "\x00" // no name: never equal to a name
	}
	return unquote(l.rest(1))
}

func stateName(s int) string {
	var parts []string
	for _, m := range []struct {
		bit  int
		name string
	}{{1, "Shift"}, {2, "Ctrl"}, {4, "Alt"}, {8, "8"}, {16, "16"}, {32, "32"}, {64, "64"}, {128, "128"}} {
		if s&m.bit != 0 {
			parts = append(parts, m.name)
		}
	}
	if len(parts) == 0 {
		return ": base"
	}
	return ": " + strings.Join(parts, "+")
}

func (w *writer) layout() {
	f := w.f
	w.add("LAYOUT")
	w.blank()
	heads := []string{"//sc", "vk", "cap"}
	for i := range f.ShiftStates {
		heads = append(heads, strconv.Itoa(i))
	}
	w.add("%s", strings.Join(heads, "\t"))
	w.blank()
	for _, k := range f.Keys {
		if k.Scan>>8 != 0 {
			w.msklc("e0-row", "scan code %s: the MSKLC editor does not keep E0 scan codes in LAYOUT", scanString(k.Scan))
		}
		if !vk.KnownToMSKLC(k.VK) {
			w.msklc("vk-name", "virtual key %s is not a name MSKLC knows (OBS K21)", vk.Name(k.VK))
		}
		fields := []string{scanString(k.Scan), vk.Name(k.VK)}
		if k.HasCells {
			fields = append(fields, capString(k.Cap))
			for _, c := range k.Cells {
				fields = append(fields, w.cell(c))
			}
		}
		w.add("%s", strings.Join(fields, "\t"))
		if k.Cap&CapSGCAPS != 0 {
			cont := []string{"-1", "-1", "0"}
			for _, c := range k.SGCap {
				cont = append(cont, w.cell(c))
			}
			w.add("%s", strings.Join(cont, "\t"))
		}
	}
	w.blank()
}

func capString(c byte) string {
	if c == CapSGCAPS {
		return "SGCap"
	}
	return strconv.Itoa(int(c))
}

// cell writes a cell canonically (§4.6): ASCII letters and digits as literals, everything else as 4 lower-case
// hex digits, supplementary characters as bare 5 or 6 digits, dead keys as hhhh@.
func (w *writer) cell(c Cell) string {
	switch c.Kind {
	case CellNone:
		return "-1"
	case CellLigature:
		return "%%"
	case CellDead:
		if w.opt.ForMSKLC && c.Rune >= 0x1000 && !isSurrogate(uint16(c.Rune)) {
			return string(c.Rune) + "@"
		}
		return hex4(uint16(c.Rune)) + "@"
	}
	r := c.Rune
	if r > 0xFFFF {
		return strconv.FormatInt(int64(r), 16)
	}
	if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
		return string(r)
	}
	return hex4(uint16(r))
}
