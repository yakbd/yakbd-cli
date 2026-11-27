package cldr

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/format/kbljson"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
)

// Namespace returns the CLDR keyboard namespace of a conformsTo version.
func Namespace(conformsTo int) string {
	return fmt.Sprintf("https://schemas.unicode.org/cldr/%d/keyboard3", conformsTo)
}

// reader holds the state of one Read.
type reader struct {
	r    *loss.Report
	name string
	l    *model.Layout
	// keyOrder lists the key ids in document order (imports inlined in place).
	keyOrder []string
}

func (rd *reader) at(n *node) loss.Where {
	w := loss.Where{File: rd.name}
	if n != nil {
		w.Line = n.line
	}
	return w
}

func (rd *reader) diag(n *node, code, format string, args ...any) {
	rd.r.Errorf("cldr.xml."+code, rd.at(n), format, args...)
}

// lost adds a Loss entry with the kind's default policy as its provisional severity.
func lost(r *loss.Report, kind string, where loss.Where, detail map[string]any, format string, args ...any) {
	sev := loss.Warning
	switch policy.Default(kind) {
	case "error", "fatal":
		sev = loss.Error
	case "accept":
		sev = loss.Accepted
	}
	r.Append(loss.Entry{Class: loss.Loss, Severity: sev, Code: kind, Where: where, Detail: detail,
		Message: fmt.Sprintf(format, args...)})
}

// Read parses a CLDR keyboard3 file into a layout. The layout is nil when the report has errors.
func Read(data []byte, name string) (*model.Layout, *loss.Report) {
	rd := &reader{r: &loss.Report{}, name: name}
	root, err := parseTree(data)
	if err != nil {
		rd.r.Errorf("cldr.xml.syntax", loss.Where{File: name}, "%v", err)
		return nil, rd.r
	}
	if root.name != "keyboard3" {
		rd.diag(root, "root", "the root element is <%s>, not <keyboard3>", root.name)
		return nil, rd.r
	}
	rd.read(root)
	if rd.r.HasErrors() {
		return nil, rd.r
	}
	// The reader checks what it reads; whatever the file leaves out that the format requires (an empty id, a missing
	// attribute) is caught here by the schema, so that Read never returns a layout the .kbl.json writer refuses.
	if _, wr := kbljson.Write(rd.l); wr.HasErrors() {
		for _, e := range wr.Entries {
			if e.Severity == loss.Error {
				rd.r.Errorf("cldr.xml.invalid", loss.Where{File: name, Path: e.Where.Path}, "the file gives a layout the format refuses: %s", e.Message)
			}
		}
		return nil, rd.r
	}
	return rd.l, rd.r
}

func (rd *reader) read(root *node) {
	l := &model.Layout{Kbl: model.FormatVersion}
	rd.l = l
	l.Metadata.ID = root.get("locale")
	if l.Metadata.ID == "" {
		rd.diag(root, "locale", "keyboard3 has no locale")
	}
	l.Metadata.Draft = root.get("draft")
	prov := &model.Provenance{Source: rd.name, Format: "cldr"}
	if c := root.get("conformsTo"); c != "" {
		v, err := strconv.Atoi(c)
		if err != nil || v < 45 {
			rd.diag(root, "conformsTo", "conformsTo %q is not a CLDR version from 45 up", c)
		} else {
			prov.CLDRConformsTo = v
			l.Targets = &model.Targets{CLDR: &model.CLDRTarget{ConformsTo: v}}
			for _, a := range root.attrs {
				if a.Name.Local == "xmlns" && a.Name.Space == "" && a.Value != Namespace(v) {
					prov.Notes = "xmlns " + a.Value
				}
			}
		}
	}
	l.Metadata.Provenance = prov
	f := false
	l.Settings = &model.Settings{CapsLockFallback: &f} // D11: CLDR has no Caps Lock fallback

	var special *kblSpecial
	for _, c := range root.children {
		switch c.name {
		case "locales":
			for _, lc := range c.kids("locale") {
				l.Metadata.Locales = append(l.Metadata.Locales, lc.get("id"))
			}
		case "version":
			l.Metadata.Version = c.get("number")
		case "info":
			l.Metadata.Name = c.get("name")
			if a := c.get("author"); a != "" {
				l.Metadata.Authors = []model.Author{{Name: a}}
			}
			l.Metadata.LayoutPattern = c.get("layout")
			l.Metadata.Indicator = c.get("indicator")
			l.Metadata.Attribution = c.get("attribution")
		case "settings":
			if c.get("normalization") == "disabled" {
				l.Settings.Normalization = "disabled"
			}
		case "displays":
			rd.displays(rd.expand(c))
		case "keys":
			rd.keys(rd.expand(c))
		case "flicks":
			rd.flicks(rd.expand(c))
		case "forms":
			rd.forms(rd.expand(c))
		case "layers":
			rd.layers(rd.expand(c))
		case "variables":
			rd.variables(rd.expand(c))
		case "transforms":
			rd.transforms(rd.expand(c))
		case "special":
			if s, ok := parseKBLSpecial(c.raw); ok {
				if special != nil {
					rd.diag(c, "special", "a second kbl special element")
				}
				special = s
			} else {
				l.CLDRSpecial = append(l.CLDRSpecial, c.raw)
			}
		case "import":
			rd.diag(c, "import", "<import> directly under keyboard3 is not supported")
		default:
			rd.diag(c, "element", "unknown element <%s>", c.name)
		}
	}
	if l.Metadata.Name == "" {
		rd.diag(root, "info", "the keyboard has no info/@name")
	}
	rd.markerDeadKeys()
	if len(prov.CLDRImports) == 0 {
		prov.CLDRImports = nil
	}
	if special != nil {
		special.apply(l)
	}
	if l.Hardware == nil && len(l.Touch) == 0 {
		lost(rd.r, "general.noLayers", rd.at(root), nil, "the keyboard has no layers")
	}
}

// expand replaces the <import> children of a container by the children of the imported file's root, recording
// each import in the provenance and reporting general.importInlined.
func (rd *reader) expand(n *node) *node {
	out := &node{name: n.name, attrs: n.attrs, line: n.line}
	for _, c := range n.children {
		if c.name != "import" {
			out.children = append(out.children, c)
			continue
		}
		p := c.get("path")
		key := p
		if c.get("base") == "cldr" {
			key = "cldr:" + p
		} else {
			rd.diag(c, "import", "local import %q is not available; only CLDR imports are inlined", p)
			continue
		}
		data, err := importFile(p)
		if err != nil {
			rd.diag(c, "import", "CLDR import %q is not known to this tool", p)
			continue
		}
		root, err := parseTree(data)
		if err != nil {
			rd.diag(c, "import", "CLDR import %q: %v", p, err)
			continue
		}
		rd.l.Metadata.Provenance.CLDRImports = append(rd.l.Metadata.Provenance.CLDRImports, key)
		lost(rd.r, "general.importInlined", rd.at(c), map[string]any{"path": key}, "CLDR import %s inlined", key)
		if root.name != n.name {
			rd.diag(c, "import", "import %q has root <%s> inside <%s>", p, root.name, n.name)
			continue
		}
		out.children = append(out.children, root.children...)
	}
	return out
}

// containerSpecials keeps the <special> children of a container without an object of its own in the root
// cldrSpecial, each prefixed with the comment that names the parent (SPEC §18).
func (rd *reader) containerSpecials(n *node) {
	for _, s := range n.specials() {
		rd.l.CLDRSpecial = append(rd.l.CLDRSpecial, "<!--kbl:parent="+n.name+"-->"+s)
	}
}

func (rd *reader) marked(n *node, attr string) *model.MarkedText {
	v, ok := n.attr(attr)
	if !ok {
		return nil
	}
	m, err := parseMarked(v)
	if err != nil {
		rd.diag(n, "escape", "%s: %v", attr, err)
		return nil
	}
	return &m
}

func (rd *reader) displays(n *node) {
	d := &model.Displays{CLDRSpecial: n.specials()}
	for _, c := range n.children {
		switch c.name {
		case "display":
			e := model.DisplayEntry{Output: rd.marked(c, "output"), KeyID: c.get("keyId")}
			if m := rd.marked(c, "display"); m != nil {
				e.Display = *m
			}
			if (e.Output == nil) == (e.KeyID == "") {
				rd.diag(c, "display", "a display needs exactly one of output and keyId")
			}
			d.Entries = append(d.Entries, e)
		case "displayOptions":
			if b, ok := c.attr("baseCharacter"); ok {
				t, err := decodeEscapes(b)
				if err != nil {
					rd.diag(c, "escape", "%v", err)
				}
				d.BaseCharacter = t
			}
		}
	}
	rd.l.Displays = d
}

func (rd *reader) keys(n *node) {
	if rd.l.Keys == nil {
		rd.l.Keys = map[string]*model.Key{}
	}
	rd.containerSpecials(n)
	for _, c := range n.kids("key") {
		id := c.get("id")
		if id == "" {
			rd.diag(c, "key", "a key without id")
			continue
		}
		k := &model.Key{Output: rd.marked(c, "output"), FlickID: c.get("flickId"), LayerID: c.get("layerId"),
			LongPressDefaultKeyID: c.get("longPressDefaultKeyId")}
		k.Gap = c.get("gap") == "true"
		k.Stretch = c.get("stretch") == "true"
		if w := c.get("width"); w != "" {
			v, err := strconv.ParseFloat(w, 64)
			if err != nil {
				rd.diag(c, "width", "width %q", w)
			} else {
				k.Width = &v
			}
		}
		k.LongPressKeyIDs = strings.Fields(c.get("longPressKeyIds"))
		k.MultiTapKeyIDs = strings.Fields(c.get("multiTapKeyIds"))
		if _, dup := rd.l.Keys[id]; !dup {
			rd.keyOrder = append(rd.keyOrder, id)
		}
		rd.l.Keys[id] = k // a later definition replaces an imported one
	}
}

func (rd *reader) flicks(n *node) {
	rd.containerSpecials(n)
	for _, c := range n.kids("flick") {
		f := &model.Flick{CLDRSpecial: c.specials()}
		for _, s := range c.kids("flickSegment") {
			f.Segments = append(f.Segments, model.FlickSegment{Directions: strings.Fields(s.get("directions")), KeyID: s.get("keyId")})
		}
		if rd.l.Flicks == nil {
			rd.l.Flicks = map[string]*model.Flick{}
		}
		rd.l.Flicks[c.get("id")] = f
	}
}

// addPosition records a position for a scan code and returns its id.
func (rd *reader) addPosition(n *node, code string) string {
	id, scan, err := positionFor(code)
	if err != nil {
		rd.diag(n, "scanCodes", "%v", err)
		return ""
	}
	if rd.l.Positions == nil {
		rd.l.Positions = map[string]*model.Position{}
	}
	if p, ok := rd.l.Positions[id]; ok {
		if p.ScanCode != scan {
			rd.diag(n, "scanCodes", "position %s with two scan codes", id)
		}
		return id
	}
	rd.l.Positions[id] = &model.Position{ScanCode: scan}
	return id
}

func (rd *reader) forms(n *node) {
	rd.containerSpecials(n)
	for _, c := range n.kids("form") {
		f := &model.Form{CLDRSpecial: c.specials()}
		for _, sc := range c.kids("scanCodes") {
			var row []string
			for _, code := range strings.Fields(sc.get("codes")) {
				if id := rd.addPosition(sc, code); id != "" {
					row = append(row, id)
				}
			}
			f.Rows = append(f.Rows, row)
		}
		if rd.l.Forms == nil {
			rd.l.Forms = map[string]*model.Form{}
		}
		rd.l.Forms[c.get("id")] = f
	}
}

// formRows returns the position rows of a form, inlining an implied form the file does not define.
func (rd *reader) formRows(n *node, id string) [][]string {
	if f, ok := rd.l.Forms[id]; ok {
		return f.Rows
	}
	rows, ok := impliedFormRows(id)
	if !ok {
		rd.diag(n, "formId", "form %q is neither defined nor implied", id)
		return nil
	}
	f := &model.Form{}
	for _, r := range rows {
		var row []string
		for _, code := range r {
			row = append(row, rd.addPosition(n, code))
		}
		f.Rows = append(f.Rows, row)
	}
	if rd.l.Forms == nil {
		rd.l.Forms = map[string]*model.Form{}
	}
	rd.l.Forms[id] = f
	lost(rd.r, "general.importInlined", rd.at(n), map[string]any{"form": id}, "implied form %s inlined", id)
	return f.Rows
}

// parseModifiers reads layer/@modifiers: sets separated by commas, components by spaces; none = [].
func parseModifiers(s string) []model.ModifierSet {
	if strings.TrimSpace(s) == "" {
		return []model.ModifierSet{{}}
	}
	var out []model.ModifierSet
	for _, set := range strings.Split(s, ",") {
		ms := model.ModifierSet{}
		for _, c := range strings.Fields(set) {
			if c != "none" {
				ms = append(ms, c)
			}
		}
		out = append(out, ms)
	}
	return out
}

// impliedKey reports the output of an implied key id (SPEC §8): a single letter or digit, or space.
func impliedKey(id string) (string, bool) {
	if id == "space" {
		return " ", true
	}
	if len(id) == 1 {
		c := id[0]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return id, true
		}
	}
	return "", false
}

func (rd *reader) layers(n *node) {
	formID := n.get("formId")
	if len(n.kids("layer")) == 0 {
		rd.diag(n, "layers", "<layers> has no <layer> (CLDR requires one or more)")
		return
	}
	mdw := 0
	if v := n.get("minDeviceWidth"); v != "" {
		w, err := strconv.Atoi(v)
		if err != nil || w < 1 || w > 999 {
			rd.diag(n, "minDeviceWidth", "minDeviceWidth %q", v)
		}
		mdw = w
	}
	if formID == "touch" {
		set := &model.TouchLayerSet{MinDeviceWidth: mdw, CLDRSpecial: n.specials()}
		for _, c := range n.kids("layer") {
			tl := &model.TouchLayer{ID: c.get("id"), CLDRSpecial: c.specials()}
			if m, ok := c.attr("modifiers"); ok {
				tl.Modifiers = parseModifiers(m)
			}
			for _, row := range c.kids("row") {
				tl.Rows = append(tl.Rows, strings.Fields(row.get("keys")))
			}
			if tl.ID == "" || len(tl.Rows) == 0 {
				rd.diag(c, "layer", "a touch layer needs an id and at least one <row> (kbl SPEC §9.4)")
				continue
			}
			set.Layers = append(set.Layers, tl)
		}
		rd.l.Touch = append(rd.l.Touch, set)
		return
	}
	if rd.l.Hardware != nil {
		rd.diag(n, "layers", "a second hardware layer set")
		return
	}
	if formID == "" {
		formID = "us"
	}
	rows := rd.formRows(n, formID)
	hw := &model.Hardware{FormID: formID, MinDeviceWidth: mdw, CLDRSpecial: n.specials()}
	for _, c := range n.kids("layer") {
		hl := &model.HardwareLayer{ID: c.get("id"), Modifiers: parseModifiers(c.get("modifiers")),
			Bindings: map[string]*model.Binding{}, CLDRSpecial: c.specials()}
		for ri, row := range c.kids("row") {
			keys := strings.Fields(row.get("keys"))
			if ri >= len(rows) {
				rd.diag(row, "row", "layer row %d has no row in form %s", ri+1, formID)
				break
			}
			if len(keys) > len(rows[ri]) {
				rd.diag(row, "row", "row %d has %d keys, form %s has %d positions", ri+1, len(keys), formID, len(rows[ri]))
				keys = keys[:len(rows[ri])]
			}
			for ki, id := range keys {
				if b := rd.binding(row, id); b != nil {
					hl.Bindings[rows[ri][ki]] = b
				}
			}
		}
		hw.Layers = append(hw.Layers, hl)
	}
	rd.l.Hardware = hw
}

// binding turns a row's key id into the binding of its position (coverage §2.1, row/@keys).
func (rd *reader) binding(n *node, id string) *model.Binding {
	if k, ok := rd.l.Keys[id]; ok {
		if k.Gap {
			return nil
		}
		return &model.Binding{Kind: model.BindKey, Key: id}
	}
	if id == "gap" {
		return nil
	}
	if out, ok := impliedKey(id); ok {
		return model.TextBinding(out)
	}
	lost(rd.r, "general.unresolvedReference", rd.at(n), map[string]any{"keyId": id}, "key %q is neither defined nor implied", id)
	return &model.Binding{Kind: model.BindKey, Key: id}
}

func (rd *reader) variables(n *node) {
	rd.containerSpecials(n)
	for _, c := range n.children {
		switch c.name {
		case "string", "set", "uset":
			rd.l.Variables = append(rd.l.Variables, &model.Variable{ID: c.get("id"), Type: c.name, Value: c.get("value")})
		}
	}
}

// intOrList reads a reorder number attribute: one integer, or a space-separated list (kept as a JSON array).
func intOrList(n *node, attr string, boolean bool, diag func(string)) json.RawMessage {
	v, ok := n.attr(attr)
	if !ok {
		return nil
	}
	var parts []string
	for _, f := range strings.Fields(v) {
		if boolean {
			if f != "true" && f != "false" {
				diag(fmt.Sprintf("%s %q is not true or false", attr, v))
				return nil
			}
		} else if _, err := strconv.Atoi(f); err != nil {
			diag(fmt.Sprintf("%s %q is not a list of integers", attr, v))
			return nil
		}
		parts = append(parts, f)
	}
	if len(parts) == 1 {
		return json.RawMessage(parts[0])
	}
	return json.RawMessage("[" + strings.Join(parts, ",") + "]")
}

func (rd *reader) transforms(n *node) {
	typ := n.get("type")
	if typ != "simple" && typ != "backspace" {
		rd.diag(n, "transforms", "transforms type %q", typ)
		return
	}
	if rd.l.Transforms == nil {
		rd.l.Transforms = &model.Transforms{}
	}
	t := rd.l.Transforms
	if sp := n.specials(); len(sp) > 0 {
		if t.CLDRSpecial == nil {
			t.CLDRSpecial = &model.TransformsCLDRSpecial{}
		}
		if typ == "simple" {
			t.CLDRSpecial.Simple = append(t.CLDRSpecial.Simple, sp...)
		} else {
			t.CLDRSpecial.Backspace = append(t.CLDRSpecial.Backspace, sp...)
		}
	}
	for _, g := range n.kids("transformGroup") {
		grp := &model.TransformGroup{CLDRSpecial: g.specials()}
		for _, c := range g.children {
			switch c.name {
			case "transform":
				tr := model.Transform{From: c.get("from")}
				if to, ok := c.attr("to"); ok {
					tr.To = &to
				}
				grp.Transforms = append(grp.Transforms, tr)
			case "reorder":
				d := func(msg string) { rd.diag(c, "reorder", "%s", msg) }
				grp.Reorders = append(grp.Reorders, model.Reorder{From: c.get("from"), Before: c.get("before"),
					Order: intOrList(c, "order", false, d), Tertiary: intOrList(c, "tertiary", false, d),
					TertiaryBase: intOrList(c, "tertiaryBase", true, d), PreBase: intOrList(c, "preBase", true, d)})
			}
		}
		if len(grp.Transforms) > 0 && len(grp.Reorders) > 0 {
			rd.diag(g, "transformGroup", "a group with both transforms and reorders")
			continue
		}
		if len(grp.Transforms) == 0 && len(grp.Reorders) == 0 {
			continue
		}
		if typ == "simple" {
			t.Simple = append(t.Simple, grp)
		} else {
			t.Backspace = append(t.Backspace, grp)
		}
	}
}

// literalPattern matches a transform pattern without CLDR pattern syntax (escapes \u{...} allowed).
var patternSyntax = regexp.MustCompile(`[\[\](){}.*+?|^$]|\\[^u]|\\u[^{]`)

func literal(p string) (string, bool) {
	stripped := escapeRun.ReplaceAllString(p, "")
	if patternSyntax.MatchString(stripped) || strings.Contains(stripped, `\`) {
		return "", false
	}
	t, err := decodeEscapes(p)
	return t, err == nil
}

// markerDeadKeys converts marker dead keys into deadKeys where coverage §3.6 allows it: the marker is the whole
// output of every key that has it, every transform that mentions it is in one simple group and is "\m{m}" +
// literal → literal (or a set form, D88), nothing else mentions it or \m{.} (except a later-group catch-all, D95),
// and at least one such transform exists.
func (rd *reader) markerDeadKeys() {
	l := rd.l
	if l.Transforms == nil {
		return
	}
	all := func(f func(s string) bool) bool {
		for _, groups := range [][]*model.TransformGroup{l.Transforms.Simple, l.Transforms.Backspace} {
			for _, g := range groups {
				for _, t := range g.Transforms {
					if f(t.From) || (t.To != nil && f(*t.To)) {
						return true
					}
				}
				for _, r := range g.Reorders {
					if f(r.From) || f(r.Before) {
						return true
					}
				}
			}
		}
		return false
	}
	// \m{.}(.) → $1 and \m{.} → nothing say what noMatch emitText says: they stay in the file and block only the
	// markers whose transforms are not in an earlier group (rule 3, kbl D95). Any other mention of \m{.} blocks all.
	var catchAll []int
	for gi, g := range l.Transforms.Simple {
		for _, t := range g.Transforms {
			if (t.From == `\m{.}(.)` && t.To != nil && *t.To == "$1") || (t.From == `\m{.}` && (t.To == nil || *t.To == "")) {
				catchAll = append(catchAll, gi)
			}
		}
	}
	if all(func(s string) bool { return strings.Contains(s, `\m{.}`) && s != `\m{.}(.)` && s != `\m{.}` }) {
		return
	}
	for _, groups := range [][]*model.TransformGroup{l.Transforms.Simple, l.Transforms.Backspace} {
		for _, g := range groups {
			for _, t := range g.Transforms {
				if strings.Contains(t.From, `\m{.}`) && !((t.From == `\m{.}(.)` && t.To != nil && *t.To == "$1") || (t.From == `\m{.}` && (t.To == nil || *t.To == ""))) {
					return
				}
			}
		}
	}
	for _, g := range l.Transforms.Backspace {
		for _, t := range g.Transforms {
			if strings.Contains(t.From, `\m{.}`) {
				return // the exception is for simple groups only
			}
		}
	}
	// markers used as key outputs
	var markers []string
	seen := map[string]bool{}
	for _, id := range rd.keyOrder {
		k := l.Keys[id]
		if k.Output == nil {
			continue
		}
		for _, it := range k.Output.Items {
			if it.Marker != "" && !seen[it.Marker] {
				seen[it.Marker] = true
				markers = append(markers, it.Marker)
			}
		}
	}
	for _, m := range markers {
		rd.convertMarker(m, catchAll)
	}
	// Drop empty groups and containers.
	var simple []*model.TransformGroup
	for _, g := range l.Transforms.Simple {
		if len(g.Transforms) > 0 || len(g.Reorders) > 0 {
			simple = append(simple, g)
		}
	}
	l.Transforms.Simple = simple
	if len(l.Transforms.Simple) == 0 && len(l.Transforms.Backspace) == 0 && l.Transforms.CLDRSpecial == nil {
		l.Transforms = nil
	}
}

func (rd *reader) convertMarker(m string, catchAll []int) {
	l := rd.l
	tag := `\m{` + m + `}`
	for _, k := range l.Keys {
		if k.Output == nil {
			continue
		}
		for _, it := range k.Output.Items {
			if it.Marker == m && len(k.Output.Items) != 1 {
				return // rule 1
			}
		}
	}
	group := -1
	var entries []model.DeadKeyEntry
	seenBase := map[string]bool{}
	for gi, g := range l.Transforms.Simple {
		for _, t := range g.Transforms {
			if !strings.Contains(t.From, tag) && (t.To == nil || !strings.Contains(*t.To, tag)) {
				continue
			}
			if group >= 0 && group != gi {
				return // rule 2: one group
			}
			group = gi
			if !strings.HasPrefix(t.From, tag) || t.To == nil {
				return
			}
			pairs, ok := rd.deadPairs(strings.TrimPrefix(t.From, tag), *t.To)
			if !ok {
				return
			}
			for _, p := range pairs {
				if !seenBase[p[0]] {
					seenBase[p[0]] = true // a later pair with the same base could never match (D88)
					entries = append(entries, model.DeadKeyEntry{Base: model.Value{Text: p[0]}, Result: model.Value{Text: p[1]}})
				}
			}
		}
	}
	for _, g := range l.Transforms.Backspace {
		for _, t := range g.Transforms {
			if strings.Contains(t.From, tag) || (t.To != nil && strings.Contains(*t.To, tag)) {
				return // rule 3
			}
		}
	}
	if group < 0 {
		return // no composition: keep the marker
	}
	for _, gi := range catchAll {
		if gi <= group {
			return // the catch-all must come in a later group than the dead key (D95)
		}
	}
	g := l.Transforms.Simple[group]
	var keep []model.Transform
	for _, t := range g.Transforms {
		if !strings.Contains(t.From, tag) {
			keep = append(keep, t)
		}
	}
	g.Transforms = keep
	dk := &model.DeadKey{Spacing: model.TextOrUnits{Text: m}, Entries: entries, NoMatch: "emitText"}
	if l.Displays != nil {
		var rest []model.DisplayEntry
		for _, e := range l.Displays.Entries {
			if e.Output != nil && len(e.Output.Items) == 1 && e.Output.Items[0].Marker == m {
				dk.Spacing = model.TextOrUnits{Text: e.Display.TextOf()}
				continue
			}
			rest = append(rest, e)
		}
		l.Displays.Entries = rest
		if len(rest) == 0 && l.Displays.BaseCharacter == "" && len(l.Displays.CLDRSpecial) == 0 {
			l.Displays = nil
		}
	}
	if l.DeadKeys == nil {
		l.DeadKeys = map[string]*model.DeadKey{}
	}
	l.DeadKeys[m] = dk
	for _, k := range l.Keys {
		if k.Output != nil && len(k.Output.Items) == 1 && k.Output.Items[0].Marker == m {
			k.Output = nil
			k.DeadKey = m
		}
	}
}

// setPattern is the from of a set-form dead-key transform after the marker (coverage §3.6 rule 2, D88).
var setPattern = regexp.MustCompile(`^\(\$\[([A-Za-z0-9_]+)\]\)$`)

// mappedSet is a to of the form $[1:w].
var mappedSet = regexp.MustCompile(`^\$\[1:([A-Za-z0-9_]+)\]$`)

// setRef is a set member that names another set variable.
var setRef = regexp.MustCompile(`^\$\[([A-Za-z0-9_]+)\]$`)

// escapeRun is one \u{...} escape.
var escapeRun = regexp.MustCompile(`\\u\{[0-9A-Fa-f ]+\}`)

// deadPairs returns the (base, result) pairs of one dead-key transform, from = the text after the marker:
// literal → literal, or ($[v]) → $1 + literal or $[1:w], one pair per member of v in set order (D88).
func (rd *reader) deadPairs(from, to string) ([][2]string, bool) {
	if strings.Contains(to, `\m{`) {
		return nil, false
	}
	if m := setPattern.FindStringSubmatch(from); m != nil {
		members, ok := rd.setMembers(m[1], 0)
		if !ok || len(members) == 0 {
			return nil, false
		}
		var out [][2]string
		if w := mappedSet.FindStringSubmatch(to); w != nil {
			results, ok := rd.setMembers(w[1], 0)
			if !ok || len(results) != len(members) {
				return nil, false
			}
			for i, b := range members {
				out = append(out, [2]string{b, results[i]})
			}
			return out, true
		}
		if !strings.HasPrefix(to, "$1") {
			return nil, false
		}
		rest, ok := literal(strings.TrimPrefix(to, "$1"))
		if !ok {
			return nil, false
		}
		for _, b := range members {
			out = append(out, [2]string{b, b + rest})
		}
		return out, true
	}
	base, ok1 := literal(from)
	res, ok2 := literal(to)
	if !ok1 || !ok2 || base == "" {
		return nil, false
	}
	return [][2]string{{base, res}}, true
}

// setMembers lists the members of a set variable, set variables inside it expanded in place. A member with other
// pattern syntax (a string variable, a UnicodeSet) makes the set unusable for the expansion.
func (rd *reader) setMembers(id string, depth int) ([]string, bool) {
	if depth > 16 {
		return nil, false
	}
	for _, v := range rd.l.Variables {
		if v.ID != id {
			continue
		}
		if v.Type != "set" {
			return nil, false
		}
		var out []string
		for _, tok := range strings.Fields(v.Value) {
			if m := setRef.FindStringSubmatch(tok); m != nil {
				sub, ok := rd.setMembers(m[1], depth+1)
				if !ok {
					return nil, false
				}
				out = append(out, sub...)
				continue
			}
			t, err := decodeEscapes(tok) // set members are strings, not patterns
			if err != nil || t == "" {
				return nil, false
			}
			out = append(out, t)
		}
		return out, true
	}
	return nil, false
}
