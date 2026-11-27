package klc

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// Parse reads a `.klc` file. name is used in the locations of the report. The returned File is nil only when
// the bytes cannot be decoded at all; a File with errors in the report must not be used for a build.
func Parse(data []byte, name string) (*File, *loss.Report) {
	p := &parser{r: &loss.Report{}, name: name, f: &File{}, seen: map[string]int{}}
	units, enc, err := decode(data)
	if err != nil {
		p.r.Errorf("klc.encoding", loss.Where{File: name}, "%v", err)
		return nil, p.r
	}
	if enc == Windows1252 {
		p.r.Infof("klc.encoding.1252", loss.Where{File: name}, "no byte-order mark and not UTF-8: read as Windows-1252")
	}
	p.run(units)
	p.check()
	return p.f, p.r
}

type parser struct {
	r       *loss.Report
	name    string
	f       *File
	seen    map[string]int // keyword -> line of its first occurrence
	section string
	lastKey *Key // the previous LAYOUT row, for the SGCAP continuation line
	dead    *DeadKey
}

func (p *parser) at(no int) loss.Where { return loss.Where{File: p.name, Line: no} }

func (p *parser) errf(no int, code, format string, args ...any) {
	p.r.Errorf("klc."+code, p.at(no), format, args...)
}

func (p *parser) warnf(no int, code, format string, args ...any) {
	p.r.Warnf("klc."+code, p.at(no), format, args...)
}

var quotedKeywords = map[string]bool{"KBD": true, "COPYRIGHT": true, "COMPANY": true, "LOCALENAME": true, "LOCALEID": true}
var textSections = map[string]bool{"KEYNAME": true, "KEYNAME_EXT": true, "KEYNAME_DEAD": true, "DESCRIPTIONS": true, "LANGUAGENAMES": true}

func (p *parser) run(units []uint16) {
	for i, raw := range splitLines(units) {
		no := i + 1
		for _, c := range raw {
			if c == 0 {
				p.errf(no, "nul", "NUL character in the text")
				break
			}
		}
		if isCommentLine(raw) {
			continue
		}
		kw := keywordOf(cutComment(raw, false))
		quotes := quotedKeywords[kw] || (kw == "" && textSections[p.section])
		l := tokenize(no, cutComment(raw, quotes))
		if len(l.toks) == 0 {
			continue
		}
		if kw == "" {
			p.dataLine(l)
			continue
		}
		if kw == "ENDKBD" {
			p.f.EndKBD = true
			p.finishKey()
			return
		}
		p.keywordLine(kw, l)
	}
	p.finishKey()
	p.warnf(0, "endkbd.missing", "the file has no ENDKBD")
}

func (p *parser) keywordLine(kw string, l line) {
	p.finishKey()
	if first, ok := p.seen[kw]; ok && kw != "DEADKEY" {
		p.errf(l.no, "keyword.repeated", "%s appears again (first at line %d)", kw, first)
	} else if !ok {
		p.seen[kw] = l.no
	}
	p.section = kw
	switch kw {
	case "KBD":
		if len(l.toks) < 2 {
			p.errf(l.no, "kbd.name", "KBD needs a name and a quoted description")
			return
		}
		p.f.Name = l.tok(1)
		desc, ok := p.quoted(l, 2, "KBD description")
		if ok {
			p.f.Description = desc
		}
	case "VERSION":
		v := l.rest(1)
		p.f.Version = &v
	case "COPYRIGHT", "COMPANY", "LOCALENAME", "LOCALEID":
		s, ok := "", true
		if len(l.toks) > 1 {
			s, ok = p.quoted(l, 1, kw)
		}
		if !ok {
			return
		}
		switch kw {
		case "COPYRIGHT":
			p.f.Copyright = &s
		case "COMPANY":
			p.f.Company = &s
		case "LOCALENAME":
			p.f.LocaleName = &s
		case "LOCALEID":
			p.f.LocaleID = &s
		}
	case "ATTRIBUTES":
		p.attributes(l, 1)
	case "DEADKEY":
		if len(l.toks) < 2 {
			p.errf(l.no, "deadkey.header", "DEADKEY needs the dead character")
			p.dead = nil
			return
		}
		d, ok := p.char(l.tok(1), l.tokUnits(1))
		if !ok {
			p.errf(l.no, "deadkey.header", "DEADKEY header %q is not 4 hex digits or one character", l.tok(1))
			p.dead = nil
			return
		}
		for i := range p.f.DeadKeys {
			if p.f.DeadKeys[i].Dead == d {
				p.warnf(l.no, "deadkey.merged", "a second DEADKEY block for %04x is merged into the first (line %d)", d, p.f.DeadKeys[i].Line)
				p.dead = &p.f.DeadKeys[i]
				return
			}
		}
		p.f.DeadKeys = append(p.f.DeadKeys, DeadKey{Dead: d, Line: l.no})
		p.dead = &p.f.DeadKeys[len(p.f.DeadKeys)-1]
	}
	// MODIFIERS, SHIFTSTATE, LAYOUT, LIGATURE, KEYNAME*, DESCRIPTIONS, LANGUAGENAMES: text after the keyword on the
	// same line is a remark (the MSKLC editor writes one after LAYOUT, OBS K33) and is ignored.
}

// quoted reads a quoted string that starts at token i and may contain blanks (§2).
func (p *parser) quoted(l line, i int, what string) (string, bool) {
	if i >= len(l.toks) {
		p.errf(l.no, "quote.missing", "%s: a quoted string is missing", what)
		return "", false
	}
	u := l.units[l.toks[i][0]:]
	if u[0] != '"' {
		p.errf(l.no, "quote.missing", "%s must be in double quotes", what)
		return "", false
	}
	for j := 1; j < len(u); j++ {
		if u[j] == '"' {
			for _, c := range u[j+1:] {
				if !isBlank(c) {
					p.warnf(l.no, "quote.trailing", "text after the closing quote of %s is ignored", what)
					break
				}
			}
			return string(utf16.Decode(u[1:j])), true
		}
	}
	p.errf(l.no, "quote.unterminated", "%s: the closing quote is missing", what)
	return "", false
}

func (p *parser) dataLine(l line) {
	switch p.section {
	case "MODIFIERS":
		p.modifier(l)
	case "SHIFTSTATE":
		p.shiftState(l)
	case "ATTRIBUTES":
		p.attributes(l, 0)
	case "LAYOUT":
		p.layoutRow(l)
	case "LIGATURE":
		p.ligature(l)
	case "DEADKEY":
		p.deadEntry(l)
	case "KEYNAME", "KEYNAME_EXT":
		p.keyName(l)
	case "KEYNAME_DEAD":
		p.deadName(l)
	case "DESCRIPTIONS", "LANGUAGENAMES":
		p.langText(l)
	case "":
		p.errf(l.no, "line.outside", "line before the first keyword")
	default:
		p.errf(l.no, "line.unexpected", "%s takes no further lines", p.section)
	}
}

func (p *parser) modifier(l line) {
	if len(l.toks) < 2 {
		p.errf(l.no, "modifiers.entry", "a MODIFIERS entry needs a virtual key and a bit name")
		return
	}
	v, ok := vk.Parse(l.tok(0))
	if !ok {
		p.errf(l.no, "vk.unknown", "unknown virtual key %q", l.tok(0))
		return
	}
	bit := strings.ToUpper(l.tok(1))
	if _, ok := modifierBits[bit]; !ok {
		p.errf(l.no, "modifiers.bit", "unknown bit name %q (KBDKANA, KBDROYA, KBDLOYA or KBDGRPSELTAP)", l.tok(1))
		return
	}
	p.f.Modifiers = append(p.f.Modifiers, Modifier{VK: v, Bit: bit, Line: l.no})
}

func (p *parser) shiftState(l line) {
	v, err := strconv.Atoi(l.tok(0))
	if err != nil || v < 0 || v > 255 {
		p.errf(l.no, "shiftstate.value", "shift state %q is not a number from 0 to 255", l.tok(0))
		return
	}
	if len(l.toks) > 1 {
		p.warnf(l.no, "shiftstate.extra", "text after the shift state is ignored")
	}
	for _, s := range p.f.ShiftStates {
		if s == v {
			p.errf(l.no, "shiftstate.duplicate", "shift state %d appears twice", v)
			return
		}
	}
	p.f.ShiftStates = append(p.f.ShiftStates, v)
}

func (p *parser) attributes(l line, from int) {
	for i := from; i < len(l.toks); i++ {
		a := strings.ToUpper(l.tok(i))
		if _, ok := attributeFlags[a]; !ok {
			p.errf(l.no, "attributes.unknown", "unknown attribute %q (ALTGR, SHIFTLOCK or LRM_RLM)", l.tok(i))
			continue
		}
		p.f.Attributes = append(p.f.Attributes, a)
	}
}

// finishKey checks that an SGCAP row got its continuation line.
func (p *parser) finishKey() {
	if p.lastKey != nil && p.lastKey.Cap&CapSGCAPS != 0 && p.lastKey.SGCapLine == 0 {
		p.errf(p.lastKey.Line, "sgcap.missing", "a row with SGCap needs the continuation line -1 -1 0 c0 [c1]")
	}
	p.lastKey = nil
}

func parseHex(s string, maxDigits int) (uint64, bool) {
	if s == "" || len(s) > maxDigits {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 64)
	return v, err == nil
}

func (p *parser) layoutRow(l line) {
	if l.tok(0) == "-1" {
		p.sgcapLine(l)
		return
	}
	p.finishKey()
	if len(l.toks) < 2 {
		p.errf(l.no, "layout.row", "a LAYOUT row needs at least a scan code and a virtual key")
		return
	}
	st := strings.ToLower(l.tok(0))
	st = strings.TrimPrefix(st, "0x")
	sc, ok := parseHex(st, 4)
	if !ok || sc&0xFF == 0 || (sc>>8 != 0 && sc>>8 != 0xE0 && sc>>8 != 0xE1) {
		p.errf(l.no, "sc.invalid", "scan code %q: hexadecimal with high byte 00, e0 or e1 and a non-zero low byte", l.tok(0))
		return
	}
	v, ok := vk.Parse(l.tok(1))
	if !ok {
		p.errf(l.no, "vk.unknown", "unknown virtual key %q", l.tok(1))
		return
	}
	k := Key{Line: l.no, Scan: uint16(sc), VK: v}
	if len(l.toks) > 2 {
		k.HasCells = true
		c := l.tok(2)
		switch {
		case strings.EqualFold(c, "SGCAP"):
			k.Cap = CapSGCAPS
		case len(c) == 1 && strings.Contains("01245", c):
			k.Cap = c[0] - '0'
		default:
			p.errf(l.no, "cap.invalid", "cap %q: SGCap, 0, 1, 2, 4 or 5", c)
			return
		}
		for i := 3; i < len(l.toks); i++ {
			cell, ok := p.cell(l, i, false)
			if !ok {
				return
			}
			k.Cells = append(k.Cells, cell)
		}
	}
	p.f.Keys = append(p.f.Keys, k)
	p.lastKey = &p.f.Keys[len(p.f.Keys)-1]
}

func (p *parser) sgcapLine(l line) {
	k := p.lastKey
	if k == nil || k.Cap&CapSGCAPS == 0 || k.SGCapLine != 0 {
		p.errf(l.no, "sgcap.unexpected", "an SGCAP continuation line must follow a row whose cap has bit 2")
		return
	}
	if len(l.toks) < 4 || len(l.toks) > 5 || l.tok(1) != "-1" || l.tok(2) != "0" {
		p.errf(l.no, "sgcap.form", "the continuation line is -1 -1 0 c0 [c1]")
		k.SGCapLine = l.no
		return
	}
	k.SGCapLine = l.no
	for i := 3; i < len(l.toks); i++ {
		c, ok := p.cell(l, i, false)
		if !ok {
			return
		}
		if c.Kind == CellDead {
			// kbl D81: MSKLC builds hhhh@ in the continuation line as the character (OBS K14); so does the reader.
			p.warnf(l.no, "sgcap.atIsCharacter", "@ in an SGCAP continuation line: %04X is read as a character, not a dead key (as MSKLC builds it)", c.Rune)
			c = Cell{Kind: CellChar, Rune: c.Rune}
		}
		k.SGCap = append(k.SGCap, c)
	}
}

// char reads a body (§4.6): 4 hex digits or one UTF-16 unit that is neither white space nor ';'.
func (p *parser) char(s string, u []uint16) (uint16, bool) {
	if len(u) == 4 {
		if v, ok := parseHex(s, 4); ok {
			return uint16(v), true
		}
	}
	if len(u) == 1 && u[0] != ';' && !isBlank(u[0]) {
		return u[0], true
	}
	return 0, false
}

func isSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDFFF }

// cell reads a character cell (§4.6). In a LIGATURE line (lig set) surrogate halves are allowed and only plain
// characters are.
func (p *parser) cell(l line, i int, lig bool) (Cell, bool) {
	s, u := l.tok(i), l.tokUnits(i)
	fail := func(why string) (Cell, bool) {
		p.errf(l.no, "cell.invalid", "cell %q: %s", s, why)
		return Cell{}, false
	}
	if s == "-1" && !lig {
		return Cell{Kind: CellNone}, true
	}
	if s == "%%" && !lig {
		return Cell{Kind: CellLigature}, true
	}
	kind := CellChar
	body, bodyU := s, u
	if !lig && len(u) >= 2 && (u[len(u)-1] == '@' || u[len(u)-1] == '%') {
		if u[len(u)-1] == '@' {
			kind = CellDead
		} else {
			kind = CellLigature
		}
		body, bodyU = string(utf16.Decode(u[:len(u)-1])), u[:len(u)-1]
	}
	if kind == CellChar && !lig && (len(u) == 5 || len(u) == 6) {
		v, ok := parseHex(s, 6)
		if !ok || v < 0x10000 || v > 0x10FFFF || (len(u) == 5 && s[0] == '0') || (len(u) == 6 && v < 0x100000) {
			return fail("5 or 6 hex digits must name a character from U+10000 to U+10FFFF without leading zeros")
		}
		return Cell{Kind: CellChar, Rune: rune(v)}, true
	}
	if len(bodyU) == 2 || len(bodyU) == 3 {
		if _, ok := parseHex(body, 3); ok {
			return fail("hex values need 4 digits")
		}
	}
	c, ok := p.char(body, bodyU)
	if !ok {
		return fail("not -1, %%, 4 hex digits, one character, or one of those with @ or %")
	}
	if c == '@' && len(bodyU) == 1 && kind == CellChar || c == '%' && len(bodyU) == 1 && kind == CellChar {
		return fail("write @ and % as 0040 and 0025")
	}
	if isSurrogate(c) && !lig { // as hhhh or, in a UTF-16 file, as a literal unit
		return fail("a surrogate half is allowed only in a LIGATURE line; write the character as 5 or 6 hex digits")
	}
	if c >= 0xF000 && c <= 0xF002 && len(bodyU) == 4 {
		return fail("F000 to F002 are the table markers WCH_NONE, WCH_DEAD and WCH_LGTR")
	}
	if c == 0xFFFE || c == 0xFFFF {
		return fail("U+FFFE and U+FFFF are not characters")
	}
	return Cell{Kind: kind, Rune: rune(c)}, true
}

func (p *parser) ligature(l line) {
	if len(l.toks) < 4 {
		p.errf(l.no, "ligature.form", "a LIGATURE line is vk column c0 c1 [c2 [c3 [c4]]]")
		return
	}
	v, ok := vk.Parse(l.tok(0))
	if !ok {
		p.errf(l.no, "vk.unknown", "unknown virtual key %q", l.tok(0))
		return
	}
	col, err := strconv.Atoi(l.tok(1))
	if err != nil || col < 0 {
		p.errf(l.no, "ligature.column", "column %q is not a column index", l.tok(1))
		return
	}
	n := len(l.toks) - 2
	if n > 5 {
		p.errf(l.no, "ligature.length", "a ligature has 2 to 5 characters, this one %d", n)
		return
	}
	lg := Ligature{VK: v, Column: col, Line: l.no}
	for i := 2; i < len(l.toks); i++ {
		c, ok := p.cell(l, i, true)
		if !ok {
			return
		}
		lg.Chars = append(lg.Chars, uint16(c.Rune))
	}
	for _, o := range p.f.Ligatures {
		if o.VK == v && o.Column == col {
			p.errf(l.no, "ligature.duplicate", "a second LIGATURE line for %s column %d (first at line %d)", vk.Name(v), col, o.Line)
			return
		}
	}
	p.f.Ligatures = append(p.f.Ligatures, lg)
}

func (p *parser) deadEntry(l line) {
	if p.dead == nil {
		return // the header was in error
	}
	if len(l.toks) < 2 {
		p.errf(l.no, "deadkey.entry", "a DEADKEY entry needs a base and a result")
		return
	}
	base, ok := p.char(l.tok(0), l.tokUnits(0))
	if !ok {
		p.errf(l.no, "deadkey.base", "base %q is not 4 hex digits or one character", l.tok(0))
		return
	}
	ru := l.tokUnits(1)
	chained := false
	if len(ru) >= 2 && ru[len(ru)-1] == '@' {
		chained = true
		ru = ru[:len(ru)-1]
	}
	res, ok := p.char(string(utf16.Decode(ru)), ru)
	if !ok {
		p.errf(l.no, "deadkey.result", "result %q is not 4 hex digits or one character (optionally with @)", l.tok(1))
		return
	}
	if len(l.toks) > 2 {
		p.warnf(l.no, "deadkey.extra", "text after the result is ignored")
	}
	for _, e := range p.dead.Entries {
		if e.Base == base {
			p.errf(l.no, "deadkey.duplicate", "dead key %04x has base %04x twice (first at line %d)", p.dead.Dead, base, e.Line)
			return
		}
	}
	p.dead.Entries = append(p.dead.Entries, DeadEntry{Base: base, Result: res, Chained: chained, Line: l.no})
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func (p *parser) keyName(l line) {
	code, ok := parseHex(l.tok(0), 4)
	if !ok || code == 0 {
		p.errf(l.no, "keyname.code", "key-name code %q is not 1 to 4 hex digits", l.tok(0))
		return
	}
	if len(l.toks) < 2 {
		p.errf(l.no, "keyname.name", "key-name line without a name")
		return
	}
	kn := KeyName{Code: uint16(code), Name: unquote(l.rest(1))}
	if p.section == "KEYNAME" {
		p.f.KeyNames = append(p.f.KeyNames, kn)
	} else {
		p.f.KeyNamesExt = append(p.f.KeyNamesExt, kn)
	}
}

func (p *parser) deadName(l line) {
	d, ok := p.char(l.tok(0), l.tokUnits(0))
	if !ok || len(l.tokUnits(0)) != 4 {
		p.errf(l.no, "keyname.code", "KEYNAME_DEAD needs the dead character as 4 hex digits")
		return
	}
	if len(l.toks) < 2 {
		p.errf(l.no, "keyname.name", "key-name line without a name")
		return
	}
	p.f.KeyNamesDead = append(p.f.KeyNamesDead, DeadName{Dead: d, Name: unquote(l.rest(1))})
}

func (p *parser) langText(l line) {
	id, ok := parseHex(l.tok(0), 4)
	if !ok {
		p.errf(l.no, "langid", "LANGID %q is not 1 to 4 hex digits", l.tok(0))
		return
	}
	lt := LangText{LangID: uint16(id), Text: l.rest(1)}
	if lt.Text == "" {
		p.warnf(l.no, "text.empty", "%s line for %04x has no text", p.section, id)
	}
	if p.section == "DESCRIPTIONS" {
		p.f.Descriptions = append(p.f.Descriptions, lt)
	} else {
		p.f.LanguageNames = append(p.f.LanguageNames, lt)
	}
}

var (
	reName     = regexp.MustCompile(`^[\x21-\x7E]{1,8}$`)
	reLocale   = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)
	reLocaleID = regexp.MustCompile(`^[0-9A-Fa-f]{8}$`)
)

// check applies the rules that need the whole file.
func (p *parser) check() {
	f := p.f
	w0 := p.at(0)
	if _, ok := p.seen["KBD"]; !ok {
		p.r.Errorf("klc.kbd.missing", w0, "the KBD line is missing")
	} else {
		no := p.seen["KBD"]
		if !reName.MatchString(f.Name) || strings.ContainsAny(f.Name, `".`) {
			p.errf(no, "kbd.name", "KBD name %q: 1 to 8 ASCII characters without blanks, quotes or dots", f.Name)
		}
		if n := len(utf16.Encode([]rune(f.Description))); n < 1 || n > 255 {
			p.errf(no, "kbd.description", "the description has %d characters; 1 to 255 are allowed", n)
		}
	}
	if f.Copyright != nil && len(utf16.Encode([]rune(*f.Copyright))) > 255 {
		p.errf(p.seen["COPYRIGHT"], "copyright.length", "the copyright text is longer than 255 characters")
	}
	if f.Company != nil && len(utf16.Encode([]rune(*f.Company))) > 255 {
		p.errf(p.seen["COMPANY"], "company.length", "the company text is longer than 255 characters")
	}
	if f.LocaleName != nil && !reLocale.MatchString(*f.LocaleName) {
		p.errf(p.seen["LOCALENAME"], "localename", "LOCALENAME %q is not a BCP 47 tag", *f.LocaleName)
	}
	if f.LocaleID != nil && !reLocaleID.MatchString(*f.LocaleID) {
		p.errf(p.seen["LOCALEID"], "localeid", "LOCALEID %q is not 8 hex digits", *f.LocaleID)
	}

	// Shift states and modifiers (§3.1, §5).
	ssLine := p.seen["SHIFTSTATE"]
	if len(f.ShiftStates) < 2 {
		p.errf(ssLine, "shiftstate.count", "SHIFTSTATE needs at least 2 entries")
	}
	if len(f.ShiftStates) > 15 {
		p.errf(ssLine, "shiftstate.count", "SHIFTSTATE has %d entries; a DLL has at most 15 columns", len(f.ShiftStates))
	}
	hasCtrl := false
	for _, s := range f.ShiftStates {
		hasCtrl = hasCtrl || s == 2
	}
	if !hasCtrl && len(f.ShiftStates) > 0 {
		p.errf(ssLine, "shiftstate.ctrl", "SHIFTSTATE must contain state 2 (Ctrl)")
	}
	if len(f.Modifiers) > 8 {
		p.errf(p.seen["MODIFIERS"], "modifiers.count", "MODIFIERS has %d entries; at most 8", len(f.Modifiers))
	}
	allowed := 7
	for _, m := range f.Modifiers {
		allowed |= int(modifierBits[m.Bit])
	}
	for _, s := range f.ShiftStates {
		if extra := s &^ allowed; extra != 0 {
			msg := ""
			for i, m := range f.Modifiers {
				if extra&(8<<i) != 0 {
					msg = "; kbdutool numbers MODIFIERS entries by position, so entry " + strconv.Itoa(i+1) + " (" + m.Bit +
						") would have bit " + strconv.Itoa(8<<i) + " there, but this tool gives each entry the bit of its constant"
				}
			}
			p.errf(ssLine, "shiftstate.bits", "shift state %d uses bits %#x that no MODIFIERS entry owns%s", s, extra, msg)
		}
	}

	// Rows (§4.1).
	scans := map[uint16]int{}
	vks := map[byte]Key{}
	for _, k := range f.Keys {
		if first, ok := scans[k.Scan]; ok {
			p.errf(k.Line, "sc.duplicate", "scan code %s appears again (first at line %d)", scanString(k.Scan), first)
		}
		scans[k.Scan] = k.Line
		if o, ok := vks[k.VK]; ok && !(k.VK == 0x0D && isEnterPair(o.Scan, k.Scan)) {
			p.errf(k.Line, "vk.duplicate", "virtual key %s is already on scan code %s (line %d)", vk.Name(k.VK), scanString(o.Scan), o.Line)
		} else if !ok {
			vks[k.VK] = k
		}
		if len(k.Cells) > len(f.ShiftStates) {
			p.errf(k.Line, "cells.count", "%d cells for %d shift states", len(k.Cells), len(f.ShiftStates))
		}
		if k.Cap&CapSGCAPS == 0 && k.SGCapLine != 0 {
			p.errf(k.SGCapLine, "sgcap.unexpected", "continuation line after a row without SGCap")
		}
		for col, c := range k.Cells {
			if c.Kind == CellLigature && !p.hasLigature(k.VK, col) {
				p.errf(k.Line, "ligature.missing", "ligature cell in column %d of %s has no LIGATURE line", col, vk.Name(k.VK))
			}
			if c.Kind == CellDead && !p.hasDead(uint16(c.Rune)) {
				p.warnf(k.Line, "deadkey.table.missing", "dead key %04x has no DEADKEY block; it composes nothing", c.Rune)
			}
			if c.Kind == CellDead && k.Cap&CapSGCAPS != 0 {
				p.warnf(k.Line, "sgcap.dead", "SGCap key with a dead key (§4.7): it can be built only when the Caps Lock character of the dead column is the dead character itself")
			}
		}
		for _, c := range k.SGCap {
			if c.Kind == CellLigature {
				p.errf(k.SGCapLine, "sgcap.ligature", "the continuation line cannot hold a ligature")
			}
		}
	}
	for _, lg := range f.Ligatures {
		if lg.Column >= len(f.ShiftStates) {
			p.errf(lg.Line, "ligature.column", "column %d, but SHIFTSTATE has %d entries (the column is the position, not the state value)", lg.Column, len(f.ShiftStates))
		}
		if !p.ligatureUsed(lg) {
			p.warnf(lg.Line, "ligature.unused", "no %% cell in column %d of %s uses this LIGATURE line", lg.Column, vk.Name(lg.VK))
		}
	}
}

func isEnterPair(a, b uint16) bool { return (a == 0x1C && b == 0xE01C) || (a == 0xE01C && b == 0x1C) }

func (p *parser) hasLigature(v byte, col int) bool {
	for _, lg := range p.f.Ligatures {
		if lg.VK == v && lg.Column == col {
			return true
		}
	}
	return false
}

func (p *parser) ligatureUsed(lg Ligature) bool {
	for _, k := range p.f.Keys {
		if k.VK == lg.VK && lg.Column < len(k.Cells) && k.Cells[lg.Column].Kind == CellLigature {
			return true
		}
	}
	return false
}

func (p *parser) hasDead(d uint16) bool {
	for _, dk := range p.f.DeadKeys {
		if dk.Dead == d {
			return true
		}
	}
	return false
}

func scanString(sc uint16) string {
	if sc>>8 != 0 {
		return strconv.FormatUint(uint64(sc), 16)
	}
	s := strconv.FormatUint(uint64(sc), 16)
	if len(s) < 2 {
		s = "0" + s
	}
	return s
}
