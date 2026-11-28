package xkb

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
)

// Resolver returns the bytes of a symbols file named by an include statement ("latin", "level3",
// "sun_vndr/us"), and false when it has none.
type Resolver func(file string) ([]byte, bool)

// Includes resolves include statements and keeps every file it read, so that several Compile calls read each
// included file once.
type Includes struct {
	res   Resolver
	files map[string]*File
}

// NewIncludes returns the include files of res (nil: none).
func NewIncludes(res Resolver) *Includes { return &Includes{res: res, files: map[string]*File{}} }

// Keymap is a section with its includes applied: the keys as libxkbcommon merges them, each group's type decided.
type Keymap struct {
	Keys    map[string]*ResolvedKey
	Order   []string       // key names in the order first defined
	Names   map[int]string // name[GroupN]
	ModMaps []ModMapDef
	// Inlined lists the includes read, as file(section), in the order first read.
	Inlined []string
}

// ResolvedKey is a key after merging.
type ResolvedKey struct {
	Name   string
	Groups []ResolvedGroup
	// Props are the fields other than type of its definitions (virtualMods, repeat, ...), as "name = value".
	Props []string
}

// ResolvedGroup is one group of a resolved key.
type ResolvedGroup struct {
	Type     string // the type named or the automatic one
	Explicit bool   // the type was named (type[GroupN], key.type or type =)
	Levels   []Level
	Actions  string
}

// Bounds of include resolution.
const (
	maxIncludeDepth = 16
	maxIncludes     = 512
)

type compiler struct {
	inc     *Includes
	r       *loss.Report
	name    string
	count   int
	inlined []string
}

// Compile applies the includes of the section sec of f (named name in reports) through inc (nil: none). An include that
// cannot be resolved, a cycle and a group other than the first of an included file (":2") are reported
// (xkb.includeUnresolved) and skipped.
func Compile(f *File, sec *Section, name string, inc *Includes) (*Keymap, *loss.Report) {
	if inc == nil {
		inc = NewIncludes(nil)
	}
	c := &compiler{inc: inc, r: &loss.Report{}, name: name}
	km := c.section(f, sec, name, nil)
	seen := map[string]bool{}
	for _, id := range c.inlined {
		if !seen[id] {
			seen[id] = true
			km.Inlined = append(km.Inlined, id)
		}
	}
	for _, n := range km.Order {
		k := km.Keys[n]
		for i := range k.Groups {
			g := &k.Groups[i]
			if g.Type == "" {
				g.Type = AutomaticType(trimLevels(g.Levels))
				if g.Type == "" {
					g.Type = "ONE_LEVEL"
				}
			}
		}
	}
	return km, c.r
}

func newKeymap() *Keymap { return &Keymap{Keys: map[string]*ResolvedKey{}, Names: map[int]string{}} }

// includeRE is one part of an include string: an optional merge operator, a file, an optional (section) and an
// optional :group.
var includeRE = regexp.MustCompile(`^([+|^]?)([^()+|^:]+)(?:\(([^()]*)\))?(?::([0-9]+))?`)

func (c *compiler) section(f *File, sec *Section, file string, stack []string) *Keymap {
	km := newKeymap()
	defType := ""
	for _, st := range sec.Stmts {
		at := loss.Where{File: file, Line: st.Line}
		switch st.Kind {
		case Include:
			mode := st.Merge
			s := st.Text
			first := true
			for s != "" {
				m := includeRE.FindStringSubmatch(s)
				if m == nil {
					c.r.Warnf("xkb.includeUnresolved", at, "the include %q cannot be read", st.Text)
					break
				}
				s = s[len(m[0]):]
				part := mode
				if !first {
					part = "override"
					if m[1] == "|" {
						part = "augment"
					}
				}
				first = false
				if m[4] != "" && m[4] != "1" {
					c.r.Warnf("xkb.includeUnresolved", at, "%s:%s puts a layout in group %s; only group 1 is read", m[2], m[4], m[4])
					continue
				}
				inc := c.include(m[2], m[3], at, stack)
				if inc != nil {
					merge(km, inc, part)
				}
			}
		case GroupName:
			if _, ok := km.Names[st.Group]; !ok || st.Merge != "augment" {
				km.Names[st.Group] = st.Text
			}
		case KeyDef:
			k := resolvedFrom(st.Key, defType)
			one := newKeymap()
			one.Keys[k.Name] = k
			one.Order = []string{k.Name}
			merge(km, one, st.Merge)
		case ModMap:
			km.ModMaps = append(km.ModMaps, *st.ModMap)
		case KeyDefault:
			if strings.HasPrefix(st.Text, "type") {
				if i := strings.IndexByte(st.Text, '"'); i >= 0 {
					defType = strings.Trim(st.Text[i:], `"`)
				}
			}
		}
	}
	return km
}

func (c *compiler) include(file, section string, at loss.Where, stack []string) *Keymap {
	id := file
	if section != "" {
		id += "(" + section + ")"
	}
	for _, s := range stack {
		if s == id {
			c.r.Warnf("xkb.includeUnresolved", at, "the include %s includes itself", id)
			return nil
		}
	}
	if len(stack) >= maxIncludeDepth || c.count >= maxIncludes {
		c.r.Warnf("xkb.includeUnresolved", at, "includes nested too deeply or too many: %s is skipped", id)
		return nil
	}
	c.count++
	f, ok := c.inc.files[file]
	if !ok {
		var data []byte
		if c.inc.res != nil {
			data, ok = c.inc.res(file)
		}
		if !ok {
			c.r.Warnf("xkb.includeUnresolved", at, "the symbols file %q is not available: its keys are not read", file)
			c.inc.files[file] = nil
			return nil
		}
		var rr *loss.Report
		f, rr = Read(data, file)
		if f == nil {
			c.r.Warnf("xkb.includeUnresolved", at, "the symbols file %q does not read: %s", file, rr.Summary())
		}
		c.inc.files[file] = f
	}
	if f == nil {
		return nil
	}
	sec := f.Section(section)
	if sec == nil {
		c.r.Warnf("xkb.includeUnresolved", at, "the symbols file %q has no section %q", file, section)
		return nil
	}
	c.inlined = append(c.inlined, id)
	return c.section(f, sec, file, append(stack, id))
}

func resolvedFrom(k *Key, defType string) *ResolvedKey {
	rk := &ResolvedKey{Name: k.Name}
	all := ""
	for _, p := range k.Props {
		if p.Name == "type" {
			all = strings.Trim(p.Value, `"`)
		} else {
			rk.Props = append(rk.Props, p.Name+" = "+p.Value)
		}
	}
	for _, g := range k.Groups {
		rg := ResolvedGroup{Type: g.Type, Levels: g.Levels, Actions: g.Actions}
		if rg.Type == "" {
			rg.Type = all
		}
		if rg.Type == "" && (g.HasSyms || g.HasActs) {
			rg.Type = defType
		}
		rg.Explicit = rg.Type != ""
		rk.Groups = append(rk.Groups, rg)
	}
	return rk
}

// merge merges from into into by mode: "replace" replaces whole keys, "augment" keeps what into has, anything
// else overrides level by level (libxkbcommon's rules; levels one side lacks come from the other).
func merge(into, from *Keymap, mode string) {
	for _, n := range from.Order {
		fk := from.Keys[n]
		ik, ok := into.Keys[n]
		if !ok || mode == "replace" {
			if !ok {
				into.Order = append(into.Order, n)
			}
			into.Keys[n] = cloneKey(fk)
			continue
		}
		clobber := mode != "augment"
		for len(ik.Groups) < len(fk.Groups) {
			ik.Groups = append(ik.Groups, ResolvedGroup{})
		}
		ik.Props = append(ik.Props, fk.Props...)
		for i, fg := range fk.Groups {
			ig := &ik.Groups[i]
			if fg.Type != "" && (ig.Type == "" || clobber) {
				ig.Type, ig.Explicit = fg.Type, fg.Explicit
			}
			levels := make([]Level, max(len(ig.Levels), len(fg.Levels)))
			for j := range levels {
				var a, b Level
				if j < len(ig.Levels) {
					a = ig.Levels[j]
				}
				if j < len(fg.Levels) {
					b = fg.Levels[j]
				}
				switch {
				case len(b) == 0:
					levels[j] = a
				case len(a) == 0 || clobber:
					levels[j] = b
				default:
					levels[j] = a
				}
			}
			ig.Levels = levels
			if fg.Actions != "" && (ig.Actions == "" || clobber) {
				ig.Actions = fg.Actions
			}
		}
	}
	for g, s := range from.Names {
		if _, ok := into.Names[g]; !ok || mode != "augment" {
			into.Names[g] = s
		}
	}
	into.ModMaps = append(into.ModMaps, from.ModMaps...)
}

func cloneKey(k *ResolvedKey) *ResolvedKey {
	c := &ResolvedKey{Name: k.Name, Groups: make([]ResolvedGroup, len(k.Groups)), Props: append([]string(nil), k.Props...)}
	copy(c.Groups, k.Groups)
	for i := range c.Groups {
		c.Groups[i].Levels = append([]Level(nil), k.Groups[i].Levels...)
	}
	return c
}

// TypeOf returns the key type of a resolved group: a stock type, or for a name the package does not know (ok
// false) the type the key would have without one.
func TypeOf(g ResolvedGroup) (*KeyType, bool) {
	if t := StockType(g.Type); t != nil {
		return t, true
	}
	if t := StockType(AutomaticType(trimLevels(g.Levels))); t != nil {
		return t, false
	}
	return StockType("ONE_LEVEL"), false
}

// trimLevels drops trailing NoSymbol levels, as libxkbcommon 1.9 and later do before the automatic type.
func trimLevels(ls []Level) []Level {
	for len(ls) > 0 && len(ls[len(ls)-1]) == 0 {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// String is a short description of a resolved group, for messages.
func (g ResolvedGroup) String() string {
	parts := make([]string, len(g.Levels))
	for i, l := range g.Levels {
		parts[i] = LevelText(l, WriteOptions{})
	}
	return fmt.Sprintf("%s [%s]", g.Type, strings.Join(parts, ", "))
}
