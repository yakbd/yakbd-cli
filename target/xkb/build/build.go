// Package build turns a format-neutral layout (model.Layout) into a Linux XKB layout (format/xkb): a symbols
// file with one section, the registry entry of rules/evdev.xml, a Compose file with the dead-key sequences and,
// for keys no stock type fits, yakbd key types. It follows the pins of targets.xkb and, where the file is silent,
// the default rules of the kbl specification's targets/xkb.md; every value a default rule decides is a
// `xkbDefault.<rule>` entry and is kept in Result.Pins for `pin`. Everything XKB cannot express is reported with
// the loss kinds of SPEC §17.3.
package build

import (
	"fmt"
	"github.com/yakbd/yakbd-cli/target/postable"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target/xkb/keyname"
)

// Options of a build.
type Options struct {
	// Policy, if set, is applied after the file's onLoss policies (for example the command line's -strict-defaults).
	Policy *model.OnLoss
}

// The bits of a build state (targets/xkb.md §3).
const (
	StShift = 1
	StLock  = 2
	StL3    = 4
	StL5    = 8
)

// Value is what a key does in one state: text, a dead key, or nothing.
type Value struct {
	Text string
	Dead string // a dead-key id
}

// None reports nothing.
func (v Value) None() bool { return v.Text == "" && v.Dead == "" }

// Key is one built key.
type Key struct {
	Position string
	Name     string // XKB key name
	Type     string
	Levels   []Value
	Values   []Value // the value of each build state (what the layout types)
	// Keypad is the navigation keysym of a number-pad key, written as its first level (type KEYPAD); "" for none.
	Keypad string
}

// Result is a built layout.
type Result struct {
	Symbols *xkb.File
	// SymbolsText is the symbols file as written.
	SymbolsText []byte
	// Compose is the Compose file, nil when the build writes none; ComposeLines are its sequences without the
	// include line (for an install script that merges them into the user's file).
	Compose      *xkb.Compose
	ComposeText  []byte
	ComposeLines []byte
	// Registry is the rules/evdev.xml of the layout.
	Registry []byte
	// Types is the types file of the yakbd key types, nil for none; TypesName its file name.
	Types     []byte
	TypesName string
	// Rules is rules/evdev.post, which adds the types file to the layout's keymaps; nil without yakbd types.
	Rules []byte
	// Pins holds every value of targets.xkb, pinned by the file or decided by a rule.
	Pins *model.XKBTarget
	// Keys are the built keys in file order; States the number of build states (4, 8 or 16).
	Keys   []Key
	States int
	// Keysyms maps each dead key to its keysym.
	Keysyms map[string]xkb.Keysym
	// UsesL3, UsesL5 report whether the build has the level-3 and level-5 shifts.
	UsesL3, UsesL5 bool
	// X11Section is the name of the symbols file's X11 section (targets/xkb.md §3), "" when it has none.
	X11Section string
}

// Build builds l. The result is nil when the report has errors after the file's onLoss policies are applied.
func Build(l *model.Layout, opt Options) (*Result, *loss.Report) {
	b := &builder{l: l, r: &loss.Report{}, file: &model.XKBTarget{}, pin: &model.XKBTarget{}}
	if l.Targets != nil && l.Targets.XKB != nil {
		b.file = l.Targets.XKB
	}
	res := b.run()
	policy.Resolve(b.r, l, "xkb", opt.Policy)
	if b.r.HasErrors() || res == nil {
		return nil, b.r
	}
	return res, b.r
}

type builder struct {
	l    *model.Layout
	r    *loss.Report
	file *model.XKBTarget
	pin  *model.XKBTarget

	wide           *model.Layout // l with the layers the build keeps
	l3, l5         string        // the level components; "" for none
	usesL3         bool
	usesL5         bool
	levelThreeRule bool     // rule levelThree decided the level-3 component
	multi          []string // levels of several characters
	numpad         []string // number-pad keys built with KEYPAD
	capsLower      []string // keys that type a lower-case letter with Caps Lock alone
	states         int
	names          map[string]string // position -> key name
	order          []string          // positions in file order
	keys           []Key
	syms           map[string]xkb.Keysym // dead key -> keysym
	typeRule       []string              // members rule keyType filled
	typeValues     map[string]string     // position -> type from the rule
	seen           map[string]bool
}

const tp = "/targets/xkb/"

func (b *builder) lost(kind, path, format string, args ...any) {
	sev := loss.Warning
	switch policy.Default(kind) {
	case "error", "fatal":
		sev = loss.Error
	case "accept":
		sev = loss.Accepted
	}
	b.r.Append(loss.Entry{Class: loss.Loss, Severity: sev, Code: kind, Where: loss.Where{Path: path},
		Message: fmt.Sprintf(format, args...)})
}

func (b *builder) once(key string) bool {
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	if b.seen[key] {
		return false
	}
	b.seen[key] = true
	return true
}

// defaulted reports a use of rule that filled the members at the JSON pointers fields.
func (b *builder) defaulted(rule string, fields []string, value any, format string, args ...any) {
	e := loss.Entry{Class: loss.Defaulted, Severity: loss.Accepted, Code: "xkbDefault." + rule,
		Where: loss.Where{Path: commonParent(fields)}, Message: fmt.Sprintf(format, args...), Detail: map[string]any{"value": value}}
	if len(fields) > 1 {
		e.Detail["fields"] = fields
	}
	b.r.Append(e)
}

func commonParent(ps []string) string {
	if len(ps) == 1 {
		return ps[0]
	}
	var common []string
	for i, p := range ps {
		toks := strings.Split(p, "/")
		if i == 0 {
			common = toks[:len(toks)-1]
			continue
		}
		n := 0
		for n < len(common) && n < len(toks)-1 && common[n] == toks[n] {
			n++
		}
		common = common[:n]
	}
	return strings.Join(common, "/")
}

func (b *builder) run() *Result {
	b.drops()
	if b.l.Hardware == nil || len(b.l.Hardware.Layers) == 0 {
		b.lost("xkb.noHardwareLayers", "/hardware", "the file has no hardware layers: an XKB layout needs them")
		return nil
	}
	b.registryNames()
	b.modifiers()
	b.layers()
	b.positions()
	b.deadKeysyms()
	b.characters()
	b.compose()
	for _, p := range b.order {
		b.key(p)
	}
	if len(b.multi) > 0 {
		b.lost("xkb.multiCharacterOutput", "/hardware", "levels of several characters (%s): libxkbcommon and Qt type them, GTK 4 types nothing, and an X11 server refuses the whole keymap (measured: Xvfb 21.1, XkbSetMap BadValue), so X11 sessions load the section %s-x11, where those levels are empty", strings.Join(b.multi, ", "), sectionName(b.pin))
	}
	if len(b.capsLower) > 0 {
		b.lost("xkb.capsLockStacks", "/hardware", "with Caps Lock on, %s type a lower-case letter (Caps Lock leaves it alone): libxkbcommon, Qt and GTK 4 on Wayland type it, GTK 4 on X11 types its upper case whatever the key type (measured with stock and yakbd types)", strings.Join(b.capsLower, ", "))
	}
	if len(b.numpad) > 0 {
		b.lost("xkb.numLockKeyReduced", "/positions", "the number-pad keys %s type their navigation keysym with Num Lock off and their base binding with Num Lock on (type KEYPAD); with Num Lock on, Shift gives the base binding with xkeyboard-config 2.48 and later and the navigation keysym (no text) with 2.42 and 2.46 (measured); their other states are not built", strings.Join(b.numpad, ", "))
	}
	if len(b.typeRule) > 0 {
		b.defaulted("keyType", b.typeRule, b.typeValues, "key types of %d keys: the first stock type that types each as the layout says", len(b.typeRule))
	}
	res := &Result{Pins: b.pin, Keys: b.keys, States: b.states, Keysyms: b.syms, UsesL3: b.usesL3, UsesL5: b.usesL5}
	b.write(res)
	return res
}

// drops reports the general data an XKB layout has no place for.
func (b *builder) drops() {
	l := b.l
	if len(l.Touch) > 0 || len(l.Flicks) > 0 {
		b.lost("general.touchDropped", "/touch", "touch layouts and gestures are not part of an XKB layout")
	} else {
		for _, id := range sortedKeys(l.Keys) {
			if k := l.Keys[id]; len(k.LongPressKeyIDs) > 0 || len(k.MultiTapKeyIDs) > 0 {
				b.lost("general.touchDropped", "/keys/"+model.Escape(id), "long-press and multi-tap choices are not part of an XKB layout")
				break
			}
		}
	}
	if l.Displays != nil {
		b.lost("general.displayDropped", "/displays", "displays are not part of an XKB layout")
	}
	if len(l.Tests) > 0 {
		b.lost("general.testsDropped", "/tests", "tests are not part of an XKB layout")
	}
	if len(l.Extensions) > 0 || len(l.CLDRSpecial) > 0 {
		b.lost("general.extensionDropped", "/extensions", "extensions and CLDR special elements are not part of an XKB layout")
	}
	m := l.Metadata
	if len(m.Locales) > 0 || m.Description != "" || len(m.Authors) > 0 || m.Attribution != "" || m.LayoutPattern != "" ||
		m.Indicator != "" || m.License != "" || m.Copyright != "" || m.Organization != "" || m.Draft != "" || m.Provenance != nil || m.Version != "" {
		b.lost("general.metadataDropped", "/metadata", "metadata beyond the name is not part of an XKB layout")
	}
	if t := l.Transforms; t != nil {
		for i := range t.Simple {
			b.lost("xkb.transformNotCompiled", fmt.Sprintf("/transforms/simple/%d", i), "transforms are not compiled")
		}
		for i := range t.Backspace {
			b.lost("xkb.transformNotCompiled", fmt.Sprintf("/transforms/backspace/%d", i), "backspace transforms are not compiled")
		}
	}
	if s := l.Settings; s != nil {
		if s.CapsLockOffBy == "shift" {
			b.lost("xkb.settingNotCompiled", "/settings/capsLockOffBy", "Shift turning Caps Lock off is not part of the layout (an XKB option, caps:shift... , does it)")
		}
		if s.BidiMarksOnShiftBackspace != nil && *s.BidiMarksOnShiftBackspace {
			b.lost("xkb.settingNotCompiled", "/settings/bidiMarksOnShiftBackspace", "bidi marks on Shift+Backspace have no XKB form")
		}
	}
	if l.Normalizes() {
		b.lost("general.normalizationNotApplied", "/settings/normalization", "each output is NFC-normalised at build time; text that several keystrokes make together is not (XKB does not normalise while typing)")
	}
}

var fileNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Slug is the slug of targets/xkb.md: lower case, runs of characters outside a-z and 0-9 as "-", trimmed;
// "layout" when nothing is left.
func Slug(s string) string {
	var out []byte
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && len(out) > 0 {
				out = append(out, '-')
			}
			dash = false
			out = append(out, byte(r))
		} else {
			dash = true
		}
	}
	if len(out) == 0 {
		return "layout"
	}
	return string(out)
}

// names are rules symbolsName, variant, description, shortDescription; languages has no rule.
func (b *builder) registryNames() {
	f, p := b.file, b.pin
	p.SymbolsName = f.SymbolsName
	if p.SymbolsName == "" {
		p.SymbolsName = "yakbd-" + Slug(b.l.Metadata.ID)
		if len(p.SymbolsName) > 64 {
			p.SymbolsName = strings.TrimRight(p.SymbolsName[:64], "-")
		}
		b.defaulted("symbolsName", []string{tp + "symbolsName"}, p.SymbolsName, "the symbols file and registry name from metadata.id")
	}
	p.Variant = f.Variant
	if p.Variant == "" {
		p.Variant = "basic"
		b.defaulted("variant", []string{tp + "variant"}, "basic", "no variant: the section basic is the file's default and the registry lists the layout %s", p.SymbolsName)
	} else if !strings.HasPrefix(p.SymbolsName, "yakbd-") {
		b.lost("xkb.systemLayoutShadowed", tp+"variant", "the variant %s goes into the user's symbols file %s: if %s is a system layout, libxkbcommon before 1.13.2 then gives this variant for the layout %s alone (measured: 1.7.0, 1.13.1; 1.13.2 keeps the system layout)", p.Variant, p.SymbolsName, p.SymbolsName, p.SymbolsName)
	}
	p.Description = f.Description
	if p.Description == "" {
		p.Description = b.l.Metadata.Name
		b.defaulted("description", []string{tp + "description"}, p.Description, "the layout's name from metadata.name")
	}
	p.ShortDescription = f.ShortDescription
	if p.ShortDescription == "" {
		lang, _, _ := strings.Cut(b.l.Metadata.ID, "-")
		p.ShortDescription = strings.ToLower(lang)
		if len(p.ShortDescription) > 16 || p.ShortDescription == "" {
			p.ShortDescription = "yk"
		}
		b.defaulted("shortDescription", []string{tp + "shortDescription"}, p.ShortDescription, "the short label from the language subtag of metadata.id")
	}
	p.Languages = f.Languages
	p.Compose = f.Compose
	p.CustomTypes = f.CustomTypes
}

// modifiers is rule levelThree; level five has no rule.
func (b *builder) modifiers() {
	b.pin.Modifiers = &model.XKBModifiers{}
	if m := b.file.Modifiers; m != nil {
		*b.pin.Modifiers = *m
	}
	if b.pin.Modifiers.LevelThree == "" {
		b.pin.Modifiers.LevelThree, b.levelThreeRule = "altR", true
	}
	b.l3, b.l5 = b.pin.Modifiers.LevelThree, b.pin.Modifiers.LevelFive
	for _, c := range []string{b.l3, b.l5} {
		if c == "" || c == "altR" || c == "alt" {
			continue
		}
		if cm := b.l.CustomModifiers[c]; cm == nil || cm.Kind != "held" {
			b.lost("xkb.modifierUnmapped", tp+"modifiers", "the level component %q is not a held custom component", c)
		}
	}
}

// isLevel reports whether a component is the level-3 or level-5 component.
func (b *builder) isLevel(c string) (three, five bool) {
	return c == b.l3 || (b.l3 == "alt" && (c == "altL" || c == "altR")), b.l5 != "" && c == b.l5
}

// layers keeps the modifier sets XKB can reach: sets of shift, caps and the level components (targets/xkb.md §3);
// a layer left with no set is left out.
func (b *builder) layers() {
	l := b.l
	w := *l
	w.Hardware = &model.Hardware{}
	for i, layer := range l.Hardware.Layers {
		path := fmt.Sprintf("/hardware/layers/%d", i)
		var sets []model.ModifierSet
		for j, m := range layer.Modifiers {
			ok := true
			l3, l5 := false, false
			for _, c := range m {
				if c == "shift" || c == "caps" || c == "other" {
					continue
				}
				three, five := b.isLevel(c)
				switch {
				case three:
					l3 = true
				case five:
					l5 = true
				case model.Builtin[c]:
					b.lost("xkb.controlAltLayerDropped", fmt.Sprintf("%s/modifiers/%d", path, j), "the modifier set needs %q: XKB layouts do not type with Control or Alt (programs read those shortcuts themselves); it is left out", c)
					ok = false
				default:
					if cm := l.CustomModifiers[c]; cm != nil && cm.Kind == "lock" {
						b.lost("xkb.lockUnmapped", fmt.Sprintf("%s/modifiers/%d", path, j), "the modifier set needs the lock %q; Caps Lock is the only lock an XKB layout types with (left out)", c)
					} else {
						b.lost("xkb.modifierUnmapped", fmt.Sprintf("%s/modifiers/%d", path, j), "the modifier set needs the component %q, which is neither the level-3 nor the level-5 shift (left out)", c)
					}
					ok = false
				}
				if !ok {
					break
				}
			}
			if ok {
				sets = append(sets, m)
				b.usesL3 = b.usesL3 || l3
				b.usesL5 = b.usesL5 || l5
			}
		}
		if len(sets) > 0 {
			nl := *layer
			nl.Modifiers = sets
			w.Hardware.Layers = append(w.Hardware.Layers, &nl)
		}
	}
	b.wide = &w
	b.states = 4
	if b.usesL3 {
		b.states = 8
	}
	if b.usesL5 {
		b.states = 16
	}
	if b.usesL3 && b.levelThreeRule {
		b.defaulted("levelThree", []string{tp + "modifiers/levelThree"}, "altR", "Right Alt is the level-3 shift (AltGr)")
	}
	if !b.usesL3 && b.levelThreeRule {
		b.pin.Modifiers.LevelThree = ""
	}
	if *b.pin.Modifiers == (model.XKBModifiers{}) {
		b.pin.Modifiers = nil
	}
}

// stateOf is the modifier state of a build state.
func (b *builder) stateOf(s int) model.State {
	st := model.State{Shift: s&StShift != 0, Caps: s&StLock != 0, Custom: map[string]bool{}}
	set := func(c string) {
		switch c {
		case "altR":
			st.AltR = true
		case "alt":
			st.AltR = true
		case "":
		default:
			st.Custom[c] = true
		}
	}
	if s&StL3 != 0 {
		set(b.l3)
	}
	if s&StL5 != 0 {
		set(b.l5)
	}
	return st
}

// value is what position p types in build state s.
func (b *builder) value(p string, s int) Value {
	if s&StL3 != 0 && !b.usesL3 || s&StL5 != 0 && !b.usesL5 {
		return Value{}
	}
	o := b.wide.Effective(b.wide.Resolve(p, b.stateOf(s)))
	if o.Markers && b.once("marker:"+p) {
		b.lost("xkb.markerNotCompiled", "/hardware", "markers of %s are not compiled", p)
	}
	if o.DeadKey != "" {
		if b.l.DeadKeys[o.DeadKey] == nil {
			return Value{}
		}
		return Value{Dead: o.DeadKey}
	}
	if len(o.Units) == 0 {
		return Value{}
	}
	if b.l.Normalizes() {
		n := o.NFC()
		if string(utf16.Decode(n.Units)) != string(utf16.Decode(o.Units)) && b.once("nfc:"+p+string(utf16.Decode(o.Units))) {
			b.lost("general.outputNormalized", "/positions/"+model.Escape(p), "an output of %s is NFC-normalised", p)
		}
		o = n
	}
	return Value{Text: unitsText(o.Units)}
}

// positions are the positions with a binding in a kept layer, and the level-shift keys; rule keyName.
func (b *builder) positions() {
	used := map[string]bool{}
	for _, layer := range b.wide.Hardware.Layers {
		for p := range layer.Bindings {
			used[p] = true
		}
	}
	for _, c := range []string{b.l3, b.l5} {
		if cm := b.l.CustomModifiers[c]; cm != nil {
			for _, p := range cm.Positions {
				used[p] = true
			}
		}
	}
	var ps []string
	for p := range used {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	// file order: the table's order, then the others by name
	idx := map[string]int{}
	for i, r := range keyname.Rows {
		idx[r.Position] = i
	}
	sort.SliceStable(ps, func(i, j int) bool {
		a, aok := idx[ps[i]]
		c, cok := idx[ps[j]]
		if aok != cok {
			return aok
		}
		return aok && a < c
	})
	b.names = map[string]string{}
	pinKeys := map[string]*model.XKBKey{}
	var fromRule []string
	v := map[string]string{}
	byName := map[string]string{}
	for _, p := range ps {
		name := ""
		if k := b.file.Keys[p]; k != nil && k.KeyName != "" {
			name = k.KeyName
		} else if r, ok := keyname.ForPosition(p); ok {
			name = r.KeyName
			fromRule = append(fromRule, tp+"keys/"+model.Escape(p)+"/keyName")
			v[p] = name
		} else if postable.Known(p) {
			b.lost("xkb.positionWithoutKey", "/positions/"+model.Escape(p), "XKB has no key of its own for the position %s: its bindings are left out (pin targets.xkb.keys.%s.keyName to build them)", p, p)
			continue
		} else {
			b.lost("xkb.keyNameMissing", "/positions/"+model.Escape(p), "the position %s has no XKB key name: pin targets.xkb.keys.%s.keyName", p, p)
			continue
		}
		if other, dup := byName[name]; dup {
			b.lost("xkb.keyNameDuplicate", tp+"keys/"+model.Escape(p), "the positions %s and %s have the same key name <%s>", other, p, name)
			continue
		}
		byName[name] = p
		b.names[p] = name
		b.order = append(b.order, p)
		pinKeys[p] = &model.XKBKey{KeyName: name}
	}
	if len(fromRule) > 0 {
		b.defaulted("keyName", fromRule, v, "XKB key names of %d positions from the position table", len(fromRule))
	}
	if len(pinKeys) > 0 {
		b.pin.Keys = pinKeys
	}
}

// deadKeysym is rule deadKeysym (targets/xkb.md §4): the dead keys a key types, in ascending order of their ids,
// get the first of (1) dead_<id> when that is a dead keysym, (2) the keysym of their spacing, (3) the first spare
// keysym, that no other dead key has taken; step 1 runs for every dead key before step 2.
func (b *builder) deadKeysyms() {
	b.syms = map[string]xkb.Keysym{}
	typed := map[string]bool{}
	for _, layer := range b.wide.Hardware.Layers {
		for _, bd := range layer.Bindings {
			if o := b.wide.Effective(bd); o.DeadKey != "" && b.l.DeadKeys[o.DeadKey] != nil {
				typed[o.DeadKey] = true
			}
		}
	}
	ids := sortedKeys(typed)
	if len(ids) == 0 {
		return
	}
	taken := map[xkb.Keysym]string{}
	var rule []string
	for _, id := range ids {
		if d := b.file.DeadKeys[id]; d != nil && d.Keysym != "" {
			k, ok := xkb.KeysymFromName(d.Keysym)
			if !ok || !deadKeysyms[k.Name()] {
				b.lost("xkb.deadKeysymMissing", tp+"deadKeys/"+model.Escape(id)+"/keysym", "%s is not a dead keysym", d.Keysym)
				continue
			}
			if other, dup := taken[k]; dup {
				b.lost("xkb.deadKeysymDuplicate", tp+"deadKeys/"+model.Escape(id)+"/keysym", "the dead keys %s and %s have the same keysym %s", other, id, d.Keysym)
				continue
			}
			taken[k] = id
			b.syms[id] = k
		} else {
			rule = append(rule, id)
		}
	}
	var fields []string
	v := map[string]string{}
	pick := func(id, name string) bool {
		if !deadKeysyms[name] {
			return false
		}
		k, _ := xkb.KeysymFromName(name)
		if _, dup := taken[k]; dup {
			return false
		}
		taken[k] = id
		b.syms[id] = k
		fields = append(fields, tp+"deadKeys/"+model.Escape(id)+"/keysym")
		v[id] = name
		return true
	}
	var rest []string
	for _, id := range rule {
		if !pick(id, "dead_"+id) {
			rest = append(rest, id)
		}
	}
	for _, id := range rest {
		if pick(id, spacingKeysym(b.l.DeadKeys[id].Spacing)) {
			continue
		}
		done := false
		for _, s := range spareKeysyms {
			if done = pick(id, s); done {
				break
			}
		}
		if !done {
			b.lost("xkb.deadKeysymMissing", "/deadKeys/"+model.Escape(id), "no dead keysym is left for the dead key %s", id)
		}
	}
	if len(b.syms) > 0 {
		b.pin.DeadKeys = map[string]*model.XKBDeadKey{}
		for id, k := range b.syms {
			b.pin.DeadKeys[id] = &model.XKBDeadKey{Keysym: k.Name()}
		}
	}
	if len(fields) > 0 {
		b.defaulted("deadKeysym", fields, v, "dead keysyms of %d dead keys", len(fields))
	}
}

// deadKeysyms are the dead keysyms of targets/xkb.md §4.
var deadKeysyms = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(`dead_grave dead_acute dead_circumflex dead_tilde dead_perispomeni dead_macron
		dead_breve dead_abovedot dead_diaeresis dead_abovering dead_doubleacute dead_caron dead_cedilla dead_ogonek
		dead_iota dead_voiced_sound dead_semivoiced_sound dead_belowdot dead_hook dead_horn dead_stroke
		dead_abovecomma dead_psili dead_abovereversedcomma dead_dasia dead_doublegrave dead_belowring
		dead_belowmacron dead_belowcircumflex dead_belowtilde dead_belowbreve dead_belowdiaeresis dead_invertedbreve
		dead_belowcomma dead_currency dead_lowline dead_aboveverticalline dead_belowverticalline
		dead_longsolidusoverlay dead_a dead_A dead_e dead_E dead_i dead_I dead_o dead_O dead_u dead_U
		dead_small_schwa dead_schwa dead_capital_schwa dead_SCHWA dead_greek dead_hamza dead_apostrophe`) {
		m[n] = true
	}
	return m
}()

// spareKeysyms are step 3 of rule deadKeysym.
var spareKeysyms = []string{"dead_lowline", "dead_aboveverticalline", "dead_belowverticalline", "dead_longsolidusoverlay"}

// spacingTable is the spacing table of targets/xkb.md §4, in its order: a spacing character and its keysym.
var spacingTable = [][2]string{
	{"`", "dead_grave"}, {"̀", "dead_grave"},
	{"´", "dead_acute"}, {"'", "dead_acute"}, {"́", "dead_acute"},
	{"^", "dead_circumflex"}, {"ˆ", "dead_circumflex"}, {"̂", "dead_circumflex"},
	{"~", "dead_tilde"}, {"˜", "dead_tilde"}, {"̃", "dead_tilde"},
	{"¯", "dead_macron"}, {"̄", "dead_macron"},
	{"˘", "dead_breve"}, {"̆", "dead_breve"},
	{"˙", "dead_abovedot"}, {"̇", "dead_abovedot"},
	{"¨", "dead_diaeresis"}, {"\"", "dead_diaeresis"}, {"̈", "dead_diaeresis"},
	{"˚", "dead_abovering"}, {"°", "dead_abovering"}, {"̊", "dead_abovering"},
	{"˝", "dead_doubleacute"}, {"̋", "dead_doubleacute"},
	{"ˇ", "dead_caron"}, {"̌", "dead_caron"},
	{"¸", "dead_cedilla"}, {"̧", "dead_cedilla"},
	{"˛", "dead_ogonek"}, {"̨", "dead_ogonek"},
	{"̣", "dead_belowdot"}, {"̦", "dead_belowcomma"}, {"¤", "dead_currency"},
}

// spacingKeysym is step 2 of rule deadKeysym: the keysym of a spacing (also with a space before or after it).
func spacingKeysym(t model.TextOrUnits) string {
	s := t.Text
	if s == "" && len(t.Units) > 0 {
		s = string(utf16.Decode(t.Units))
	}
	if len([]rune(s)) == 2 {
		s = strings.TrimPrefix(strings.TrimSuffix(s, " "), " ")
	}
	for _, r := range spacingTable {
		if r[0] == s {
			return r[1]
		}
	}
	return ""
}

// SpacingOf is the first spacing character the spacing table gives a dead keysym, "" for none (the importer's
// spacing, coverage/xkb.md §4).
func SpacingOf(keysym string) string {
	if k, ok := xkb.KeysymFromName(keysym); ok {
		keysym = k.Name()
	}
	for _, r := range spacingTable {
		if r[1] == keysym {
			return r[0]
		}
	}
	return ""
}

// characters is rule characters.
func (b *builder) characters() {
	b.pin.Characters = b.file.Characters
	if b.pin.Characters == "" {
		b.pin.Characters = "keysyms"
		b.defaulted("characters", []string{tp + "characters"}, "keysyms", "characters as keysym names and Uxxxx (every libxkbcommon and xkbcomp read them)")
	}
	if b.pin.Characters == "utf8" {
		b.lost("xkb.utf8Characters", tp+"characters", "characters are written as UTF-8 strings: libxkbcommon before 1.9 and xkbcomp (X11) refuse the file")
	}
}

// keysymsOf turns a text into keysyms; false when a character has no keysym (controls other than the named
// ones) or the text is not valid UTF-8 (code units that are not well-formed UTF-16, see unitsText). A real
// U+FFFD is a character like any other.
func keysymsOf(t string) (xkb.Level, bool) {
	var l xkb.Level
	for i, r := range t {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(t[i:]); size == 1 {
				return nil, false
			}
		}
		if r == 0 || (r < 0x20 && !namedControl(r)) || (r >= 0x80 && r < 0xa0) {
			return nil, false
		}
		l = append(l, xkb.Sym{K: xkb.FromRune(r)})
	}
	return l, len(l) > 0
}

func namedControl(r rune) bool {
	switch r {
	case 0x08, 0x09, 0x0a, 0x0b, 0x0d, 0x1b:
		return true
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// caseStable reports a value no character of which has the Unicode property Changes_When_Uppercased: its simple
// uppercase mapping differs, or SpecialCasing.txt gives it an unconditional uppercase of several characters
// (fullUpper; Unicode 18.0.0).
func caseStable(v Value) bool {
	for _, r := range v.Text {
		if unicode.ToUpper(r) != r || fullUpper[r] {
			return false
		}
	}
	return true
}

// fullUpper are the characters SpecialCasing.txt (Unicode 18.0.0) gives an unconditional uppercase of several
// characters.
var fullUpper = func() map[rune]bool {
	m := map[rune]bool{}
	for _, r := range []rune{0x00df, 0xfb00, 0xfb01, 0xfb02, 0xfb03, 0xfb04, 0xfb05, 0xfb06, 0x1df95, 0x0587, 0xfb13, 0xfb14, 0xfb15, 0xfb16, 0xfb17, 0x0149, 0x0390, 0x03b0, 0x01f0, 0x1e96, 0x1e97, 0x1e98, 0x1e99, 0x1e9a, 0x1f50, 0x1f52, 0x1f54, 0x1f56, 0x1fb6, 0x1fc6, 0x1fd2, 0x1fd3, 0x1fd6, 0x1fd7, 0x1fe2, 0x1fe3, 0x1fe4, 0x1fe6, 0x1fe7, 0x1ff6, 0x1f80, 0x1f81, 0x1f82, 0x1f83, 0x1f84, 0x1f85, 0x1f86, 0x1f87, 0x1f88, 0x1f89, 0x1f8a, 0x1f8b, 0x1f8c, 0x1f8d, 0x1f8e, 0x1f8f, 0x1f90, 0x1f91, 0x1f92, 0x1f93, 0x1f94, 0x1f95, 0x1f96, 0x1f97, 0x1f98, 0x1f99, 0x1f9a, 0x1f9b, 0x1f9c, 0x1f9d, 0x1f9e, 0x1f9f, 0x1fa0, 0x1fa1, 0x1fa2, 0x1fa3, 0x1fa4, 0x1fa5, 0x1fa6, 0x1fa7, 0x1fa8, 0x1fa9, 0x1faa, 0x1fab, 0x1fac, 0x1fad, 0x1fae, 0x1faf, 0x1fb3, 0x1fbc, 0x1fc3, 0x1fcc, 0x1ff3, 0x1ffc, 0x1fb2, 0x1fb4, 0x1fc2, 0x1fc4, 0x1ff2, 0x1ff4, 0x1fb7, 0x1fc7, 0x1ff7} {
		m[r] = true
	}
	return m
}()

// VariantOf is the variant of the layout: "" when the section is basic (the file's default, a layout of its own,
// rule variant), else its name.
func VariantOf(p *model.XKBTarget) string {
	if p.Variant == "basic" {
		return ""
	}
	return p.Variant
}

// sectionName is the name of the build's section: basic, or the variant.
func sectionName(p *model.XKBTarget) string {
	if v := VariantOf(p); v != "" {
		return v
	}
	return "basic"
}

// unitsText is the text of UTF-16 code units. A lone surrogate, which has no character, becomes an invalid byte
// (not U+FFFD, which utf16.Decode would make of it), so that keysymsOf reports it.
func unitsText(u []uint16) string {
	var b []byte
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		switch {
		case c >= 0xd800 && c < 0xdc00 && i+1 < len(u) && u[i+1] >= 0xdc00 && u[i+1] < 0xe000:
			b = utf8.AppendRune(b, utf16.DecodeRune(c, rune(u[i+1])))
			i++
		case utf16.IsSurrogate(c):
			b = append(b, 0xff)
		default:
			b = utf8.AppendRune(b, c)
		}
	}
	return string(b)
}
