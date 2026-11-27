package model

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yakbd/yakbd-cli/loss"
)

// Check applies the validation rules of SPEC §19 that the schema cannot express. References that do not resolve
// are general.unresolvedReference and overlapping layers general.layerOverlap (both fatal); the other rules have
// their own kinds (D42).
func (l *Layout) Check() *loss.Report {
	c := &checker{l: l, r: &loss.Report{}}
	c.run()
	return c.r
}

type checker struct {
	l *Layout
	r *loss.Report
}

func (c *checker) unresolved(path, format string, args ...any) {
	c.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Error, Code: "general.unresolvedReference",
		Where: loss.Where{Path: path}, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) diag(code, path, format string, args ...any) {
	kinds := map[string]string{"longPressDefault": "general.longPressDefaultInvalid", "multiTapSelf": "general.multiTapSelf",
		"scanCodeDuplicate": "general.duplicateScanCode", "bindingOnModifier": "general.bindingOnModifier",
		"touchBase": "general.touchBaseMissing", "variableDuplicate": "general.duplicateVariableId",
		"modifierSet": "general.modifierSetInvalid"}
	c.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Error, Code: kinds[code], Where: loss.Where{Path: path},
		Message: fmt.Sprintf(format, args...)})
}

func (c *checker) key(id, path string) {
	if _, ok := c.l.Keys[id]; ok {
		return
	}
	if _, ok := ImpliedKey(id); ok {
		return
	}
	c.unresolved(path, "key %q is not defined", id)
}

func (c *checker) dead(id, path string) {
	if _, ok := c.l.DeadKeys[id]; !ok {
		c.unresolved(path, "dead key %q is not defined", id)
	}
}

func (c *checker) position(id, path string) {
	if _, ok := c.l.Positions[id]; !ok {
		c.unresolved(path, "position %q is not defined", id)
	}
}

func (c *checker) marked(m *MarkedText, path string) {
	if m == nil {
		return
	}
	for _, it := range m.Items {
		if it.Variable == "" {
			continue
		}
		found := false
		for _, v := range c.l.Variables {
			found = found || v.ID == it.Variable && v.Type == "string"
		}
		if !found {
			c.unresolved(path, "string variable %q is not defined", it.Variable)
		}
	}
}

func (c *checker) run() {
	l := c.l
	// Keys and their references.
	for _, id := range sortedIDs(l.Keys) {
		k, path := l.Keys[id], "/keys/"+Escape(id)
		if k.DeadKey != "" {
			c.dead(k.DeadKey, path+"/deadKey")
		}
		c.marked(k.Output, path+"/output")
		if k.FlickID != "" {
			if _, ok := l.Flicks[k.FlickID]; !ok {
				c.unresolved(path+"/flickId", "flick %q is not defined", k.FlickID)
			}
		}
		for _, x := range k.LongPressKeyIDs {
			c.key(x, path+"/longPressKeyIds")
		}
		if k.LongPressDefaultKeyID != "" && !contains(k.LongPressKeyIDs, k.LongPressDefaultKeyID) {
			c.diag("longPressDefault", path, "longPressDefaultKeyId is not in longPressKeyIds")
		}
		for _, x := range k.MultiTapKeyIDs {
			c.key(x, path+"/multiTapKeyIds")
			if x == id {
				c.diag("multiTapSelf", path, "a key in its own multiTapKeyIds")
			}
		}
		if k.LayerID != "" && !c.touchLayer(k.LayerID) {
			c.unresolved(path+"/layerId", "touch layer %q is not defined", k.LayerID)
		}
	}
	for _, id := range sortedIDs(l.Flicks) {
		for i, s := range l.Flicks[id].Segments {
			c.key(s.KeyID, fmt.Sprintf("/flicks/%s/segments/%d", Escape(id), i))
		}
	}
	// Positions: unique scan codes.
	seen := map[string]string{}
	for _, id := range sortedIDs(l.Positions) {
		sc := strings.ToUpper(l.Positions[id].ScanCode)
		if sc == "" {
			continue
		}
		if o, ok := seen[sc]; ok {
			c.diag("scanCodeDuplicate", "/positions/"+id, "scan code %s is also on %s", sc, o)
		}
		seen[sc] = id
	}
	for _, id := range sortedIDs(l.Forms) {
		for _, row := range l.Forms[id].Rows {
			for _, p := range row {
				c.position(p, "/forms/"+Escape(id))
			}
		}
	}
	modPos := map[string]bool{}
	for _, name := range sortedIDs(l.CustomModifiers) {
		for _, p := range l.CustomModifiers[name].Positions {
			c.position(p, "/customModifiers/"+name)
			modPos[p] = true
		}
	}
	// Hardware layers.
	if hw := l.Hardware; hw != nil {
		if hw.FormID != "" {
			if _, ok := l.Forms[hw.FormID]; !ok {
				c.unresolved("/hardware/formId", "form %q is not defined", hw.FormID)
			}
		}
		for i, layer := range hw.Layers {
			path := fmt.Sprintf("/hardware/layers/%d", i)
			for _, m := range layer.Modifiers {
				c.modifierSet(m, path)
			}
			for _, p := range sortedIDs(layer.Bindings) {
				b := layer.Bindings[p]
				c.position(p, path+"/bindings/"+p)
				if modPos[p] {
					c.diag("bindingOnModifier", path+"/bindings/"+p, "position %s operates a modifier and cannot have a binding", p)
				}
				switch b.Kind {
				case BindKey:
					c.key(b.Key, path+"/bindings/"+p)
				case BindDeadKey:
					c.dead(b.DeadKey, path+"/bindings/"+p)
				case BindOutput:
					c.marked(&b.Output, path+"/bindings/"+p)
				case BindUnits:
					c.wholeChars(b.Units, path+"/bindings/"+p)
				}
			}
		}
		c.overlap()
	}
	for i, set := range l.Touch {
		hasBase := false
		for _, layer := range set.Layers {
			hasBase = hasBase || layer.ID == "base"
			for _, row := range layer.Rows {
				for _, k := range row {
					c.key(k, fmt.Sprintf("/touch/%d/layers/%s", i, layer.ID))
				}
			}
		}
		if !hasBase {
			c.diag("touchBase", fmt.Sprintf("/touch/%d", i), "a touch layer set needs a layer \"base\"")
		}
	}
	// Dead keys.
	for _, id := range sortedIDs(l.DeadKeys) {
		for i, e := range l.DeadKeys[id].Entries {
			for _, v := range []Value{e.Base, e.Result} {
				if v.Kind == ValueDeadKey {
					c.dead(v.DeadKey, fmt.Sprintf("/deadKeys/%s/entries/%d", Escape(id), i))
				}
			}
		}
	}
	if l.Displays != nil {
		for i, e := range l.Displays.Entries {
			if e.KeyID != "" {
				c.key(e.KeyID, fmt.Sprintf("/displays/entries/%d", i))
			}
		}
	}
	ids := map[string]bool{}
	for i, v := range l.Variables {
		if ids[v.ID] {
			c.diag("variableDuplicate", fmt.Sprintf("/variables/%d", i), "variable id %q twice", v.ID)
		}
		ids[v.ID] = true
	}
	if t := l.Targets; t != nil && t.Windows != nil {
		for _, p := range sortedIDs(t.Windows.Keys) {
			c.position(p, "/targets/windows/keys/"+p)
		}
		for _, d := range sortedIDs(t.Windows.DeadKeys) {
			c.dead(d, "/targets/windows/deadKeys/"+Escape(d))
		}
		for i, tb := range t.Windows.Tables {
			for _, r := range tb.Rows {
				if r.Position != "" {
					c.position(r.Position, fmt.Sprintf("/targets/windows/tables/%d", i))
				}
			}
		}
	}
}

func (c *checker) touchLayer(id string) bool {
	for _, set := range c.l.Touch {
		for _, layer := range set.Layers {
			if layer.ID == id {
				return true
			}
		}
	}
	return false
}

func (c *checker) modifierSet(m ModifierSet, path string) {
	has := map[string]bool{}
	for _, x := range m {
		has[x] = true
		if !Builtin[x] && c.l.CustomModifiers[x] == nil {
			c.unresolved(path, "modifier component %q is not defined", x)
		}
	}
	if has["other"] && len(m) > 1 || has["none"] {
		c.diag("modifierSet", path, "%v: other stands alone and none is written as []", []string(m))
	}
	if has["ctrl"] && (has["ctrlL"] || has["ctrlR"]) || has["alt"] && (has["altL"] || has["altR"]) ||
		has["ctrlL"] && has["ctrlR"] || has["altL"] && has["altR"] {
		c.diag("modifierSet", path, "modifier set %v mixes a component with its sides", []string(m))
	}
}

// overlap checks that no two hardware layers match one state (SPEC §7.3), over every combination of Shift,
// both Controls, both Alts, Caps Lock and the custom components.
func (c *checker) overlap() {
	custom := sortedIDs(c.l.CustomModifiers)
	if len(custom) > 8 {
		return
	}
	n := 6 + len(custom)
	reported := map[[2]int]bool{}
	for mask := 0; mask < 1<<n; mask++ {
		s := State{Shift: mask&1 != 0, CtrlL: mask&2 != 0, CtrlR: mask&4 != 0, AltL: mask&8 != 0, AltR: mask&16 != 0,
			Caps: mask&32 != 0, Custom: map[string]bool{}}
		for i, name := range custom {
			s.Custom[name] = mask&(64<<i) != 0
		}
		first := -1
		for i, layer := range c.l.Hardware.Layers {
			matched := false
			for _, m := range layer.Modifiers {
				matched = matched || m.Matches(s)
			}
			if !matched {
				continue
			}
			if first >= 0 && !reported[[2]int{first, i}] {
				reported[[2]int{first, i}] = true
				c.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Error, Code: "general.layerOverlap",
					Where:   loss.Where{Path: fmt.Sprintf("/hardware/layers/%d", i)},
					Message: fmt.Sprintf("layers %d and %d match the same modifier state", first, i)})
			}
			if first < 0 {
				first = i
			}
		}
	}
}

// wholeChars warns when a CodeUnits value is valid text (SPEC §19 item 6).
func (c *checker) wholeChars(u Units, path string) {
	for i := 0; i < len(u); i++ {
		switch {
		case u[i] >= 0xD800 && u[i] <= 0xDBFF:
			if i+1 >= len(u) || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
				return
			}
			i++
		case u[i] >= 0xDC00 && u[i] <= 0xDFFF, u[i] >= 0xF000 && u[i] <= 0xF002:
			return
		}
	}
	c.r.Append(loss.Entry{Class: loss.Loss, Severity: loss.Warning, Code: "general.codeUnitsAreText", Where: loss.Where{Path: path},
		Message: fmt.Sprintf("CodeUnits %q is valid text; write it as Text", string(utf16.Decode(u)))})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedIDs[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
