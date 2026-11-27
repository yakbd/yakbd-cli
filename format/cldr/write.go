package cldr

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// xw writes indented XML.
type xw struct {
	b     strings.Builder
	depth int
}

// attrEscaper escapes an XML attribute value (built once: a replacer per call was most of what a Write allocated).
var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\t", "&#9;", "\n", "&#10;", "\r", "&#13;")

func attrEscape(s string) string { return attrEscaper.Replace(s) }

// kv is one attribute; empty values are left out.
type kv struct{ k, v string }

func (w *xw) line(s string) {
	w.b.WriteString(strings.Repeat("\t", w.depth))
	w.b.WriteString(s)
	w.b.WriteByte('\n')
}

func (w *xw) tag(name string, attrs []kv, empty bool) {
	var s strings.Builder
	s.WriteString("<" + name)
	for _, a := range attrs {
		if a.v != "" {
			s.WriteString(" " + a.k + `="` + attrEscape(a.v) + `"`)
		}
	}
	if empty {
		s.WriteString(" />")
	} else {
		s.WriteString(">")
	}
	w.line(s.String())
	if !empty {
		w.depth++
	}
}

func (w *xw) end(name string) {
	w.depth--
	w.line("</" + name + ">")
}

func (w *xw) raw(specials []string) {
	for _, s := range specials {
		w.line(s)
	}
}

// writer holds the state of one Write.
type writer struct {
	l   *model.Layout
	r   *loss.Report
	w   xw
	gen map[string]*model.Key // generated keys
	// order of generated ids
	genOrder []string
	// extra transform group for dead keys, and extra display entries
	deadGroup    *model.TransformGroup
	deadDisplays []model.DisplayEntry
	// specials of the containers without an object, by container name
	parentSpecials map[string][]string
	rootSpecials   []string
}

func (wr *writer) lost(kind, path string, detail map[string]any, format string, args ...any) {
	lost(wr.r, kind, loss.Where{Path: path}, detail, format, args...)
}

// Write renders a layout as a CLDR keyboard3 file. It reports what CLDR elements cannot hold with the kinds of
// SPEC §17.3; with targets.cldr.carryInSpecial (default true) those parts are carried in a kbl <special> element
// that Read restores. The bytes are returned even when the report has errors: the caller applies the policy.
func Write(l *model.Layout) ([]byte, *loss.Report) {
	carry := true
	if l.Targets != nil && l.Targets.CLDR != nil && l.Targets.CLDR.CarryInSpecial != nil {
		carry = *l.Targets.CLDR.CarryInSpecial
	}
	body, r := writeBody(l, "")
	if !carry {
		return body, r
	}
	// Read the elements back and carry every top-level property that did not survive (tests travel in their
	// own files and are not carried).
	back, _ := Read(body, "")
	repl := map[string]json.RawMessage{}
	orig := topLevel(l)
	got := map[string]json.RawMessage{}
	if back != nil {
		got = topLevel(back)
	}
	for k := range union(orig, got) {
		if k == "tests" {
			continue
		}
		a, b := orig[k], got[k]
		if !sameProperty(a, b) {
			if a == nil {
				a = json.RawMessage("null")
			}
			repl[k] = a
		}
	}
	if len(repl) == 0 {
		return body, r
	}
	out, _ := writeBody(l, (&kblSpecial{replace: repl}).encode())
	return out, r
}

// sameProperty compares two values of one top-level property; nil means absent. Wrapping both in an object makes
// kbljson.SameJSON treat an absent value like an empty one, as everywhere else.
func sameProperty(a, b json.RawMessage) bool {
	wrap := func(v json.RawMessage) []byte {
		if v == nil {
			return []byte("{}")
		}
		return append(append([]byte(`{"v":`), v...), '}')
	}
	return kbljson.SameJSON(wrap(a), wrap(b))
}

func topLevel(l *model.Layout) map[string]json.RawMessage {
	b, _ := json.Marshal(l)
	var m map[string]json.RawMessage
	json.Unmarshal(b, &m)
	return m
}

func union(a, b map[string]json.RawMessage) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func writeBody(l *model.Layout, special string) ([]byte, *loss.Report) {
	wr := &writer{l: l, r: &loss.Report{}, gen: map[string]*model.Key{}, parentSpecials: map[string][]string{}}
	for _, s := range l.CLDRSpecial {
		if strings.HasPrefix(s, "<!--kbl:parent=") {
			end := strings.Index(s, "-->")
			if end > 0 {
				p := s[len("<!--kbl:parent="):end]
				wr.parentSpecials[p] = append(wr.parentSpecials[p], s[end+3:])
				continue
			}
		}
		wr.rootSpecials = append(wr.rootSpecials, s)
	}
	wr.reportDrops()
	wr.prepareDeadKeys()
	rows := wr.layerRows() // needs the dead-key keys; generates keys
	w := &wr.w
	conforms := 45
	locale := l.Metadata.ID
	if t := l.Targets; t != nil && t.CLDR != nil {
		if t.CLDR.ConformsTo != 0 {
			conforms = t.CLDR.ConformsTo
		}
		if t.CLDR.Locale != "" {
			locale = t.CLDR.Locale
		}
	}
	w.line(`<?xml version="1.0" encoding="UTF-8"?>`)
	w.tag("keyboard3", []kv{{"xmlns", Namespace(conforms)}, {"locale", locale}, {"conformsTo", strconv.Itoa(conforms)},
		{"draft", l.Metadata.Draft}}, false)
	if len(l.Metadata.Locales) > 0 {
		w.tag("locales", nil, false)
		for _, lc := range l.Metadata.Locales {
			w.tag("locale", []kv{{"id", lc}}, true)
		}
		w.end("locales")
	}
	if l.Metadata.Version != "" {
		w.tag("version", []kv{{"number", l.Metadata.Version}}, true)
	}
	var authors []string
	for _, a := range l.Metadata.Authors {
		authors = append(authors, a.Name)
	}
	w.tag("info", []kv{{"name", l.Metadata.Name}, {"author", strings.Join(authors, ", ")},
		{"layout", l.Metadata.LayoutPattern}, {"indicator", l.Metadata.Indicator}, {"attribution", l.Metadata.Attribution}}, true)
	if l.Settings != nil && l.Settings.Normalization == "disabled" {
		w.tag("settings", []kv{{"normalization", "disabled"}}, true)
	}
	wr.displays()
	wr.keys()
	wr.flicks()
	wr.forms()
	wr.hardware(rows)
	wr.touch()
	wr.variables()
	wr.transforms()
	w.raw(wr.rootSpecials)
	if special != "" {
		w.line(special)
	}
	w.end("keyboard3")
	return []byte(w.b.String()), wr.r
}

// reportDrops lists the data CLDR elements cannot hold.
func (wr *writer) reportDrops() {
	l := wr.l
	md := l.Metadata
	var dropped []string
	if md.Description != "" {
		dropped = append(dropped, "description")
	}
	if md.License != "" {
		dropped = append(dropped, "license")
	}
	if md.Copyright != "" {
		dropped = append(dropped, "copyright")
	}
	if md.Organization != "" {
		dropped = append(dropped, "organization")
	}
	if len(md.LocalizedNames) > 0 {
		dropped = append(dropped, "localizedNames")
	}
	if md.Provenance != nil {
		dropped = append(dropped, "provenance")
	}
	for i, a := range md.Authors {
		if a.Email != "" || a.URL != "" || i > 0 {
			dropped = append(dropped, "authors")
			break
		}
	}
	if len(dropped) > 0 {
		wr.lost("general.metadataDropped", "/metadata", map[string]any{"fields": dropped}, "metadata without a CLDR attribute: %s", strings.Join(dropped, ", "))
	}
	if s := l.Settings; s != nil {
		if s.CapsLockOffBy == "shift" {
			wr.lost("cldr.windowsOnlyDropped", "/settings/capsLockOffBy", nil, "capsLockOffBy shift has no CLDR form")
		}
		if s.BidiMarksOnShiftBackspace != nil && *s.BidiMarksOnShiftBackspace {
			wr.lost("cldr.windowsOnlyDropped", "/settings/bidiMarksOnShiftBackspace", nil, "bidiMarksOnShiftBackspace has no CLDR form")
		}
	}
	for _, id := range sortedKeys(l.Positions) {
		p := l.Positions[id]
		if p.Name != "" || p.NumLock != nil || p.Description != "" {
			wr.lost("cldr.windowsOnlyDropped", "/positions", nil, "position names, numLock and descriptions have no CLDR form")
			break
		}
	}
	if l.Targets != nil && (l.Targets.Windows != nil || l.Targets.KLC != nil) {
		wr.lost("cldr.windowsOnlyDropped", "/targets", nil, "targets.windows and targets.klc have no CLDR form")
	}
	if len(l.Extensions) > 0 {
		wr.lost("general.extensionDropped", "/extensions", nil, "extensions have no CLDR form")
	}
	if l.OnLoss != nil {
		wr.lost("general.metadataDropped", "/onLoss", nil, "onLoss has no CLDR form")
	}
	if len(l.CustomModifiers) > 0 {
		wr.lost("cldr.customModifierDropped", "/customModifiers", map[string]any{"components": sortedKeys(l.CustomModifiers)},
			"custom modifiers and the layer sets that use them have no CLDR form")
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// patternText escapes a literal text for a transform pattern.
func patternText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`[](){}.*+?|^$\-&`, r) || needsEscape(r, 0) || r == ' ' {
			fmt.Fprintf(&b, `\u{%X}`, r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// deadValue writes a dead-key entry value for a pattern; false for CodeUnits.
func deadValue(v model.Value) (string, bool) {
	switch v.Kind {
	case model.ValueDeadKey:
		return `\m{` + v.DeadKey + `}`, true
	case model.ValueText:
		return patternText(v.Text), true
	}
	return "", false
}

// prepareDeadKeys writes deadKeys as CLDR markers and one group of transforms (coverage §3.6, reverse).
func (wr *writer) prepareDeadKeys() {
	l := wr.l
	if len(l.DeadKeys) == 0 {
		return
	}
	g := &model.TransformGroup{}
	for _, id := range sortedKeys(l.DeadKeys) {
		dk := l.DeadKeys[id]
		tag := `\m{` + id + `}`
		for i, e := range dk.Entries {
			from, ok1 := deadValue(e.Base)
			to, ok2 := deadValue(e.Result)
			if !ok1 || !ok2 {
				wr.lost("cldr.windowsOnlyDropped", fmt.Sprintf("/deadKeys/%s/entries/%d", model.Escape(id), i), nil, "a CodeUnits dead-key entry has no CLDR form")
				continue
			}
			g.Transforms = append(g.Transforms, model.Transform{From: tag + from, To: &to})
		}
		if dk.Spacing.Units != nil {
			wr.lost("cldr.windowsOnlyDropped", "/deadKeys/"+model.Escape(id)+"/spacing", nil, "a CodeUnits spacing has no CLDR form")
		}
		spacing := patternText(dk.Spacing.Text)
		switch dk.NoMatch {
		case "", "emitBoth":
			to := spacing + "$1"
			g.Transforms = append(g.Transforms, model.Transform{From: tag + "(.)", To: &to})
		case "emitSpacing":
			to := spacing
			g.Transforms = append(g.Transforms, model.Transform{From: tag + "(.)", To: &to})
		case "emitNothing":
			to := ""
			g.Transforms = append(g.Transforms, model.Transform{From: tag + "(.)", To: &to})
		}
		disp := dk.Spacing.Text
		if dk.Display != nil {
			disp = dk.Display.TextOf()
		}
		if disp != "" && disp != id {
			out := model.MarkedText{Items: []model.MarkedItem{{Marker: id}}, Array: true}
			wr.deadDisplays = append(wr.deadDisplays, model.DisplayEntry{Output: &out, Display: model.NewText(disp)})
		}
	}
	if len(g.Transforms) > 0 {
		wr.deadGroup = g
	}
}

// keyFor returns the key id a binding is written with, generating a key where needed.
func (wr *writer) keyFor(b *model.Binding, path string) string {
	l := wr.l
	switch b.Kind {
	case model.BindKey:
		return b.Key
	case model.BindDeadKey:
		for _, id := range sortedKeys(l.Keys) {
			if l.Keys[id].DeadKey == b.DeadKey && l.Keys[id].Output == nil {
				return id
			}
		}
		for _, id := range wr.genOrder {
			if wr.gen[id].DeadKey == b.DeadKey {
				return id
			}
		}
		return wr.generate("dk-"+b.DeadKey, &model.Key{DeadKey: b.DeadKey})
	case model.BindUnits:
		wr.lost("cldr.windowsOnlyDropped", path, nil, "a CodeUnits binding has no CLDR form")
		return "gap"
	}
	var out model.MarkedText
	if b.Kind == model.BindText {
		out = model.NewText(b.Text)
		if id, ok := impliedID(b.Text); ok {
			if _, defined := l.Keys[id]; !defined {
				return id
			}
		}
	} else {
		out = b.Output
	}
	want := encodeMarked(out)
	for _, id := range sortedKeys(l.Keys) {
		k := l.Keys[id]
		if k.Output != nil && encodeMarked(*k.Output) == want && plainKey(k) {
			return id
		}
	}
	for _, id := range wr.genOrder {
		if k := wr.gen[id]; k.Output != nil && encodeMarked(*k.Output) == want {
			return id
		}
	}
	var hex []string
	for _, u := range utf16.Encode([]rune(out.TextOf())) {
		hex = append(hex, fmt.Sprintf("%04x", u))
	}
	return wr.generate("kbl-"+strings.Join(hex, "-"), &model.Key{Output: &out})
}

// impliedID returns the implied key id of a text (SPEC §8).
func impliedID(s string) (string, bool) {
	if s == " " {
		return "space", true
	}
	if _, ok := impliedKey(s); ok {
		return s, true
	}
	return "", false
}

// plainKey reports a key with only an output (no gestures, no layer switch).
func plainKey(k *model.Key) bool {
	return k.DeadKey == "" && !k.Gap && k.Width == nil && !k.Stretch && k.LayerID == "" && k.FlickID == "" &&
		len(k.LongPressKeyIDs) == 0 && len(k.MultiTapKeyIDs) == 0 && k.Function == ""
}

func (wr *writer) generate(base string, k *model.Key) string {
	id := base
	for i := 2; ; i++ {
		_, a := wr.l.Keys[id]
		_, b := wr.gen[id]
		if !a && !b {
			break
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
	if len(id) > 64 {
		id = fmt.Sprintf("kbl-%d", len(wr.genOrder)+1)
	}
	wr.gen[id] = k
	wr.genOrder = append(wr.genOrder, id)
	return id
}

// formOf returns the hardware form id and its position rows.
func (wr *writer) formOf() (string, [][]string) {
	l := wr.l
	id := ""
	if l.Hardware != nil {
		id = l.Hardware.FormID
	}
	if id == "" {
		if len(l.Forms) == 1 {
			id = sortedKeys(l.Forms)[0]
		} else {
			id = "us"
		}
	}
	if f, ok := l.Forms[id]; ok {
		return id, f.Rows
	}
	rows, _ := impliedFormRows(id)
	var out [][]string
	for _, r := range rows {
		var row []string
		for _, c := range r {
			p, _, _ := positionFor(c)
			row = append(row, p)
		}
		out = append(out, row)
	}
	return id, out
}

type writtenLayer struct {
	id        string
	modifiers string
	rows      [][]string
	specials  []string
}

// layerRows computes the hardware layers as CLDR rows: custom-modifier sets dropped, Caps Lock fallback expanded,
// bindings turned into key ids.
func (wr *writer) layerRows() []writtenLayer {
	l := wr.l
	if l.Hardware == nil {
		return nil
	}
	_, rows := wr.formOf()
	inForm := map[string]bool{}
	for _, r := range rows {
		for _, p := range r {
			inForm[p] = true
		}
	}
	type layer struct {
		src  *model.HardwareLayer
		sets []model.ModifierSet
		bind map[string]*model.Binding
	}
	var layers []*layer
	for li, hl := range l.Hardware.Layers {
		var sets []model.ModifierSet
		for _, s := range hl.Modifiers {
			custom := false
			for _, c := range s {
				if _, ok := l.CustomModifiers[c]; ok {
					custom = true
				}
			}
			if !custom {
				sets = append(sets, s)
			}
		}
		if len(sets) == 0 {
			wr.lost("cldr.customModifierDropped", fmt.Sprintf("/hardware/layers/%d", li), nil, "a layer reached only with custom modifiers is dropped")
			continue
		}
		bind := map[string]*model.Binding{}
		for p, b := range hl.Bindings {
			bind[p] = b
		}
		layers = append(layers, &layer{src: hl, sets: sets, bind: bind})
	}
	fallback := l.Settings == nil || l.Settings.CapsLockFallback == nil || *l.Settings.CapsLockFallback
	if fallback {
		expanded := false
		key := func(s model.ModifierSet) string {
			c := append([]string(nil), s...)
			sort.Strings(c)
			return strings.Join(c, " ")
		}
		bySet := map[string]*layer{}
		for _, ly := range layers {
			for _, s := range ly.sets {
				bySet[key(s)] = ly
			}
		}
		n := len(layers)
		for _, ly := range layers[:n] {
			for _, s := range ly.sets {
				if len(s) == 1 && s[0] == "other" {
					continue
				}
				hasCaps := false
				for _, c := range s {
					hasCaps = hasCaps || c == "caps"
				}
				if hasCaps {
					continue
				}
				cs := append(append(model.ModifierSet(nil), s...), "caps")
				if m, ok := bySet[key(cs)]; ok {
					for p, b := range ly.bind {
						if _, has := m.bind[p]; !has {
							m.bind[p] = b
							expanded = true
						}
					}
					continue
				}
				nb := map[string]*model.Binding{}
				for p, b := range ly.bind {
					nb[p] = b
				}
				nl := &layer{sets: []model.ModifierSet{cs}, bind: nb}
				layers = append(layers, nl)
				bySet[key(cs)] = nl
				expanded = true
			}
		}
		if expanded {
			wr.lost("cldr.capsFallbackExpanded", "/settings/capsLockFallback", nil, "the Caps Lock fallback is written as explicit Caps Lock layers")
		}
	}
	var out []writtenLayer
	for _, ly := range layers {
		wl := writtenLayer{}
		if ly.src != nil {
			wl.id = ly.src.ID
			wl.specials = ly.src.CLDRSpecial
		}
		var sets []string
		for _, s := range ly.sets {
			if len(s) == 0 {
				sets = append(sets, "none")
			} else {
				sets = append(sets, strings.Join(s, " "))
			}
		}
		wl.modifiers = strings.Join(sets, ", ")
		for _, p := range sortedKeys(ly.bind) {
			if !inForm[p] {
				wr.lost("cldr.positionNotInForm", wr.l.BindingPointer(p, ly.bind[p]), map[string]any{"position": p},
					"position %s has no place in the form", p)
			}
		}
		for _, r := range rows {
			var keys []string
			for _, p := range r {
				b, ok := ly.bind[p]
				if !ok {
					keys = append(keys, "gap")
					continue
				}
				keys = append(keys, wr.keyFor(b, wr.l.BindingPointer(p, b)))
			}
			// trailing gaps carry no information
			for len(keys) > 0 && keys[len(keys)-1] == "gap" {
				keys = keys[:len(keys)-1]
			}
			if len(keys) == 0 {
				keys = []string{"gap"}
			}
			wl.rows = append(wl.rows, keys)
		}
		out = append(out, wl)
	}
	if len(wr.genOrder) > 0 {
		wr.lost("cldr.keyIdGenerated", "/hardware", map[string]any{"keys": wr.genOrder}, "key ids generated for %d bindings", len(wr.genOrder))
	}
	return out
}

func (wr *writer) displays() {
	l := wr.l
	var entries []model.DisplayEntry
	var base string
	var sp []string
	if l.Displays != nil {
		entries = append(entries, l.Displays.Entries...)
		base = l.Displays.BaseCharacter
		sp = l.Displays.CLDRSpecial
	}
	entries = append(entries, wr.deadDisplays...)
	if len(entries) == 0 && base == "" && len(sp) == 0 {
		return
	}
	w := &wr.w
	w.tag("displays", nil, false)
	for _, e := range entries {
		a := []kv{}
		if e.Output != nil {
			a = append(a, kv{"output", encodeMarked(*e.Output)})
		}
		a = append(a, kv{"keyId", e.KeyID}, kv{"display", encodeMarked(e.Display)})
		w.tag("display", a, true)
	}
	if base != "" {
		w.tag("displayOptions", []kv{{"baseCharacter", encodeText(base)}}, true)
	}
	w.raw(sp)
	w.end("displays")
}

func (wr *writer) keys() {
	l := wr.l
	all := map[string]*model.Key{}
	for id, k := range l.Keys {
		all[id] = k
	}
	for id, k := range wr.gen {
		all[id] = k
	}
	if len(all) == 0 && len(wr.parentSpecials["keys"]) == 0 {
		return
	}
	w := &wr.w
	w.tag("keys", nil, false)
	for _, id := range sortedKeys(all) {
		k := all[id]
		a := []kv{{"id", id}, {"flickId", k.FlickID}}
		if k.Gap {
			a = append(a, kv{"gap", "true"})
		}
		switch {
		case k.DeadKey != "":
			a = append(a, kv{"output", `\m{` + k.DeadKey + `}`})
		case k.Output != nil:
			a = append(a, kv{"output", encodeMarked(*k.Output)})
		}
		a = append(a, kv{"longPressKeyIds", strings.Join(k.LongPressKeyIDs, " ")}, kv{"longPressDefaultKeyId", k.LongPressDefaultKeyID},
			kv{"multiTapKeyIds", strings.Join(k.MultiTapKeyIDs, " ")})
		if k.Stretch {
			a = append(a, kv{"stretch", "true"})
		}
		a = append(a, kv{"layerId", k.LayerID})
		if k.Width != nil {
			a = append(a, kv{"width", strconv.FormatFloat(*k.Width, 'f', -1, 64)})
		}
		if k.Function != "" && k.Output == nil && k.DeadKey == "" && !k.Gap && k.LayerID == "" {
			wr.lost("cldr.windowsOnlyDropped", "/keys/"+model.Escape(id)+"/function", nil, "key %s has only a function, which CLDR cannot express", id)
			a = append(a, kv{"gap", "true"})
		}
		w.tag("key", a, true)
	}
	w.raw(wr.parentSpecials["keys"])
	w.end("keys")
}

func (wr *writer) flicks() {
	l := wr.l
	if len(l.Flicks) == 0 && len(wr.parentSpecials["flicks"]) == 0 {
		return
	}
	w := &wr.w
	w.tag("flicks", nil, false)
	for _, id := range sortedKeys(l.Flicks) {
		f := l.Flicks[id]
		w.tag("flick", []kv{{"id", id}}, false)
		for _, s := range f.Segments {
			w.tag("flickSegment", []kv{{"directions", strings.Join(s.Directions, " ")}, {"keyId", s.KeyID}}, true)
		}
		w.raw(f.CLDRSpecial)
		w.end("flick")
	}
	w.raw(wr.parentSpecials["flicks"])
	w.end("flicks")
}

// scanRow returns the scan codes of a row of positions, or false when a position has none.
func (wr *writer) scanRow(row []string) ([]string, bool) {
	var out []string
	for _, p := range row {
		pos, ok := wr.l.Positions[p]
		if !ok || pos.ScanCode == "" {
			return nil, false
		}
		out = append(out, strings.ToUpper(pos.ScanCode))
	}
	return out, true
}

// isImpliedCopy reports whether a form of the file equals the implied form of that id.
func (wr *writer) isImpliedCopy(id string, f *model.Form) bool {
	rows, ok := impliedFormRows(id)
	if !ok || len(rows) != len(f.Rows) {
		return false
	}
	for i, r := range f.Rows {
		sc, ok := wr.scanRow(r)
		if !ok || strings.Join(sc, " ") != strings.Join(rows[i], " ") {
			return false
		}
	}
	return true
}

func (wr *writer) forms() {
	l := wr.l
	var custom []string
	for _, id := range sortedKeys(l.Forms) {
		if !isImpliedForm(id) || !wr.isImpliedCopy(id, l.Forms[id]) {
			custom = append(custom, id)
		}
	}
	if len(custom) == 0 && len(wr.parentSpecials["forms"]) == 0 {
		return
	}
	w := &wr.w
	w.tag("forms", nil, false)
	for _, id := range custom {
		f := l.Forms[id]
		wr.lost("cldr.customForm", "/forms/"+model.Escape(id), nil, "form %s is not one of CLDR's implied forms", id)
		w.tag("form", []kv{{"id", id}}, false)
		for i, r := range f.Rows {
			sc, ok := wr.scanRow(r)
			if !ok {
				wr.lost("cldr.positionNotInForm", fmt.Sprintf("/forms/%s/rows/%d", model.Escape(id), i), nil, "a position of form %s has no scan code", id)
				continue
			}
			w.tag("scanCodes", []kv{{"codes", strings.Join(sc, " ")}}, true)
		}
		w.raw(f.CLDRSpecial)
		w.end("form")
	}
	w.raw(wr.parentSpecials["forms"])
	w.end("forms")
}

func (wr *writer) hardware(layers []writtenLayer) {
	l := wr.l
	if l.Hardware == nil {
		return
	}
	id, _ := wr.formOf()
	w := &wr.w
	mdw := ""
	if l.Hardware.MinDeviceWidth != 0 {
		mdw = strconv.Itoa(l.Hardware.MinDeviceWidth)
	}
	w.tag("layers", []kv{{"formId", id}, {"minDeviceWidth", mdw}}, false)
	for _, ly := range layers {
		w.tag("layer", []kv{{"id", ly.id}, {"modifiers", ly.modifiers}}, false)
		for _, r := range ly.rows {
			w.tag("row", []kv{{"keys", strings.Join(r, " ")}}, true)
		}
		w.raw(ly.specials)
		w.end("layer")
	}
	w.raw(l.Hardware.CLDRSpecial)
	w.end("layers")
}

func modifierString(sets []model.ModifierSet) string {
	var out []string
	for _, s := range sets {
		if len(s) == 0 {
			out = append(out, "none")
		} else {
			out = append(out, strings.Join(s, " "))
		}
	}
	return strings.Join(out, ", ")
}

func (wr *writer) touch() {
	w := &wr.w
	for _, set := range wr.l.Touch {
		mdw := ""
		if set.MinDeviceWidth != 0 {
			mdw = strconv.Itoa(set.MinDeviceWidth)
		}
		w.tag("layers", []kv{{"formId", "touch"}, {"minDeviceWidth", mdw}}, false)
		for _, tl := range set.Layers {
			a := []kv{{"id", tl.ID}}
			if tl.Modifiers != nil {
				a = append(a, kv{"modifiers", modifierString(tl.Modifiers)})
			}
			w.tag("layer", a, false)
			for _, r := range tl.Rows {
				w.tag("row", []kv{{"keys", strings.Join(r, " ")}}, true)
			}
			w.raw(tl.CLDRSpecial)
			w.end("layer")
		}
		w.raw(set.CLDRSpecial)
		w.end("layers")
	}
}

func (wr *writer) variables() {
	l := wr.l
	if len(l.Variables) == 0 && len(wr.parentSpecials["variables"]) == 0 {
		return
	}
	w := &wr.w
	w.tag("variables", nil, false)
	for _, v := range l.Variables {
		w.tag(v.Type, []kv{{"id", v.ID}, {"value", v.Value}}, true)
	}
	w.raw(wr.parentSpecials["variables"])
	w.end("variables")
}

func rawList(j json.RawMessage, boolean bool) string {
	if len(j) == 0 {
		return ""
	}
	if boolean {
		var b bool
		if json.Unmarshal(j, &b) == nil {
			return strconv.FormatBool(b)
		}
		var bs []bool
		json.Unmarshal(j, &bs)
		var s []string
		for _, x := range bs {
			s = append(s, strconv.FormatBool(x))
		}
		return strings.Join(s, " ")
	}
	var n int
	if json.Unmarshal(j, &n) == nil {
		return strconv.Itoa(n)
	}
	var ns []int
	json.Unmarshal(j, &ns)
	var s []string
	for _, x := range ns {
		s = append(s, strconv.Itoa(x))
	}
	return strings.Join(s, " ")
}

func (wr *writer) transforms() {
	t := wr.l.Transforms
	var simple, back []*model.TransformGroup
	var spS, spB []string
	if t != nil {
		simple, back = t.Simple, t.Backspace
		if t.CLDRSpecial != nil {
			spS, spB = t.CLDRSpecial.Simple, t.CLDRSpecial.Backspace
		}
	}
	if wr.deadGroup != nil {
		simple = append([]*model.TransformGroup{wr.deadGroup}, simple...)
	}
	wr.transformList("simple", simple, spS)
	wr.transformList("backspace", back, spB)
}

func (wr *writer) transformList(typ string, groups []*model.TransformGroup, specials []string) {
	if len(groups) == 0 && len(specials) == 0 {
		return
	}
	w := &wr.w
	w.tag("transforms", []kv{{"type", typ}}, false)
	for _, g := range groups {
		w.tag("transformGroup", nil, false)
		for _, tr := range g.Transforms {
			a := []kv{{"from", tr.From}}
			if tr.To != nil {
				// An empty "to" must still be written: absent and empty mean the same (delete).
				a = append(a, kv{"to", *tr.To})
			}
			w.tagKeepEmpty("transform", a, tr.To != nil)
		}
		for _, r := range g.Reorders {
			w.tag("reorder", []kv{{"from", r.From}, {"before", r.Before}, {"order", rawList(r.Order, false)},
				{"tertiary", rawList(r.Tertiary, false)}, {"tertiaryBase", rawList(r.TertiaryBase, true)},
				{"preBase", rawList(r.PreBase, true)}}, true)
		}
		w.raw(g.CLDRSpecial)
		w.end("transformGroup")
	}
	w.raw(specials)
	w.end("transforms")
}

// tagKeepEmpty writes an empty element whose last attribute is written even when empty.
func (w *xw) tagKeepEmpty(name string, attrs []kv, keepLast bool) {
	if !keepLast || len(attrs) == 0 || attrs[len(attrs)-1].v != "" {
		w.tag(name, attrs, true)
		return
	}
	var s strings.Builder
	s.WriteString("<" + name)
	for i, a := range attrs {
		if a.v != "" || i == len(attrs)-1 {
			s.WriteString(" " + a.k + `="` + attrEscape(a.v) + `"`)
		}
	}
	s.WriteString(" />")
	w.line(s.String())
}
