package xkbkbl

import (
	"sort"

	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/xkb/build"
)

// node is a prefix of Compose sequences that start with a dead key of the layout.
type node struct {
	result string
	has    bool // a sequence ends here
	kids   map[xkb.Keysym]*node
	order  []xkb.Keysym
	line   int
}

// deadKeys builds the dead keys of the layout from the Compose file (coverage/xkb.md §4): every sequence that
// starts with a dead key the layout types and continues with keysyms the layout types becomes an entry (longer
// ones a chain of dead keys); other sequences are not kept.
func (im *importer) deadKeys(l *model.Layout, x *model.XKBTarget) {
	c := im.o.Compose
	if c != nil {
		inc := false
		for _, ln := range c.Lines {
			if ln.Seq == nil && ln.Include == "%L" {
				inc = true
			} else if ln.Seq == nil {
				im.lost("xkb.syntaxDetailsIgnored", loss.Accepted, ln.Line, "the include %q of %s is not kept", ln.Include, im.o.ComposeName)
			}
		}
		x.Compose = &model.XKBCompose{IncludeLocale: &inc}
	}
	if len(im.deadSyms) == 0 {
		if c != nil && hasSequences(c) {
			im.lost("xkb.composeSequenceDropped", loss.Warning, 0, "the layout has no dead keys: the sequences of %s are not kept", im.o.ComposeName)
		}
		return
	}
	roots := map[xkb.Keysym]*node{}
	dropped := 0
	if c != nil {
		for _, ln := range c.Lines {
			if ln.Seq == nil {
				continue
			}
			res := ln.Result
			if res == "" && ln.Keysym != xkb.NoSymbol {
				if r, ok := ln.Keysym.Rune(); ok {
					res = string(r)
				}
			}
			if !im.deadSyms[ln.Seq[0]] || (res == "" && !ln.Empty) || !im.typable(ln.Seq[1:]) || len(ln.Seq) < 2 {
				dropped++
				continue
			}
			n := roots[ln.Seq[0]]
			if n == nil {
				n = &node{}
				roots[ln.Seq[0]] = n
			}
			for _, k := range ln.Seq[1:] {
				if n.kids == nil {
					n.kids = map[xkb.Keysym]*node{}
				}
				nx := n.kids[k]
				if nx == nil {
					nx = &node{}
					n.kids[k] = nx
					n.order = append(n.order, k)
				}
				n = nx
			}
			if !n.has {
				n.result, n.has, n.line = res, true, ln.Line
			}
		}
	}
	if dropped > 0 {
		im.lost("xkb.composeSequenceDropped", loss.Warning, 0, "%d sequences of %s use Multi_key, do not start with a dead key the layout types, use a key it does not type or give no character: they are not kept", dropped, im.o.ComposeName)
	}
	l.DeadKeys = map[string]*model.DeadKey{}
	syms := make([]xkb.Keysym, 0, len(im.deadSyms))
	for k := range im.deadSyms {
		syms = append(syms, k)
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i] < syms[j] })
	x.DeadKeys = map[string]*model.XKBDeadKey{}
	for _, k := range syms {
		n := roots[k]
		if n == nil {
			n = &node{}
			if c == nil {
				im.lost("xkb.deadKeyNoCompose", loss.Warning, 0, "the dead key %s has no Compose file: its compositions are those of the system, which a .kbl.json file cannot name", k.Name())
			} else {
				im.lost("xkb.deadKeyNoCompose", loss.Warning, 0, "the dead key %s has no sequence in %s", k.Name(), im.o.ComposeName)
			}
		}
		id := deadID(k)
		x.DeadKeys[id] = &model.XKBDeadKey{Keysym: k.Name()}
		im.addDead(l, id, spacingOf(k, n), n)
	}
}

func (im *importer) typable(seq []xkb.Keysym) bool {
	for _, k := range seq {
		if !im.typed[k] {
			return false
		}
	}
	return true
}

func hasSequences(c *xkb.Compose) bool {
	for _, l := range c.Lines {
		if l.Seq != nil {
			return true
		}
	}
	return false
}

// addDead adds the dead key id with the entries of node n; a step with longer sequences after it is a chained
// dead key id-<step> (the step's dead-key id or keysym name).
func (im *importer) addDead(l *model.Layout, id, spacing string, n *node) {
	dk := &model.DeadKey{NoMatch: "emitNothing", Entries: []model.DeadKeyEntry{}, Spacing: model.TextOrUnits{Text: spacing}}
	l.DeadKeys[id] = dk
	for _, b := range n.order {
		kid := n.kids[b]
		var e model.DeadKeyEntry
		step := b.Name()
		if b.IsDead() {
			step = deadID(b)
			e.Base = model.Value{Kind: model.ValueDeadKey, DeadKey: step}
		} else {
			r, _ := b.Rune()
			e.Base = model.Value{Kind: model.ValueText, Text: string(r)}
		}
		if len(kid.kids) > 0 {
			if kid.has {
				im.lost("xkb.composeSequenceDropped", loss.Warning, kid.line, "a sequence that is the start of longer ones gives %q: the longer ones are kept", kid.result)
			}
			sub := id + "-" + step
			sp := spacing
			if b.IsDead() {
				sp += spacingOf(b, &node{})
			}
			e.Result = model.Value{Kind: model.ValueDeadKey, DeadKey: sub}
			im.addDead(l, sub, sp, kid)
		} else {
			e.Result = model.Value{Kind: model.ValueText, Text: kid.result}
		}
		dk.Entries = append(dk.Entries, e)
	}
}

// spacingOf is the spacing text of a dead key: what <dead> <space> gives in the Compose file, else the first
// character the spacing table of targets/xkb.md §4 gives the keysym, else the empty text.
func spacingOf(k xkb.Keysym, n *node) string {
	if kid := n.kids[xkb.Keysym(' ')]; kid != nil && kid.has && kid.result != "" {
		return kid.result
	}
	return build.SpacingOf(k.Name())
}
