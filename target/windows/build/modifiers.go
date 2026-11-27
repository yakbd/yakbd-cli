package build

import (
	"fmt"
	"sort"

	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// customBits are the bits rule modifierBits gives custom held modifiers, in order.
var customBits = []byte{0x08, 0x10, 0x20, 0x80}

// modifiers decides VK_TO_BIT and ModNumber (targets/windows.md §4).
func (b *lowerer) modifiers() {
	l, f, p := b.l, b.file, b.pin
	if f.Modifiers != nil && len(f.Modifiers.Bits) > 0 {
		for i, mb := range f.Modifiers.Bits {
			v, ok := vk.Parse(mb.VK)
			if !ok {
				b.lost("windows.modifierUnmapped", fmt.Sprintf("/targets/windows/modifiers/bits/%d", i), "unknown virtual key %q", mb.VK)
				continue
			}
			b.bits = append(b.bits, modBit{v, byte(mb.Bit), mb.Component})
		}
	} else {
		b.bits = []modBit{{0x10, windows.KBDSHIFT, "shift"}, {0x11, windows.KBDCTRL, "ctrl"}, {0x12, windows.KBDALT, "alt"}}
		var held []string
		for _, name := range sortedKeys(l.CustomModifiers) {
			if l.CustomModifiers[name].Kind == "held" {
				held = append(held, name)
			}
		}
		for i, name := range held {
			cm := l.CustomModifiers[name]
			if i >= len(customBits) {
				b.lost("windows.modifierUnmapped", "/customModifiers/"+name, "a fifth custom modifier has no Windows bit")
				continue
			}
			var v byte
			if k := f.Keys[cm.Positions[0]]; k != nil && k.VK != "" {
				v, _ = vk.Parse(k.VK)
			}
			if v == 0 {
				b.lost("windows.modifierUnmapped", "/customModifiers/"+name, "custom modifier %s: pin targets.windows.keys.%s.vk", name, cm.Positions[0])
				continue
			}
			b.bits = append(b.bits, modBit{v, customBits[i], name})
		}
		b.defaulted("modifierBits", []string{"/targets/windows/modifiers/bits"}, b.bitsPin(), "VK_TO_BIT: SHIFT, CONTROL, MENU and %d custom modifier(s)", len(b.bits)-3)
	}
	p.Modifiers = &model.WindowsModifiers{Bits: b.bitsPin()}
	if f.Modifiers != nil {
		p.Modifiers.KanaLockComponent = f.Modifiers.KanaLockComponent
	}
	for _, mb := range b.bits {
		b.t.Modifiers.VKToBit = append(b.t.Modifiers.VKToBit, windows.VKBit{VK: mb.vk, Bits: mb.bit})
	}
	for name, cm := range l.CustomModifiers {
		if cm.Kind == "lock" && (f.Modifiers == nil || f.Modifiers.KanaLockComponent != name) {
			b.lost("windows.lockUnmapped", "/customModifiers/"+name, "custom lock %s: only the pinned Kana lock is supported", name)
		}
	}

	// Columns.
	if f.Modifiers != nil && len(f.Modifiers.ModNumber) > 0 {
		b.modNum = make([]byte, len(f.Modifiers.ModNumber))
		for i, c := range f.Modifiers.ModNumber {
			b.modNum[i] = windows.SHFT_INVALID
			if c != nil {
				b.modNum[i] = byte(*c)
			}
		}
	} else {
		b.modNum = b.defaultModNumber()
		b.defaulted("modNumber", []string{"/targets/windows/modifiers/modNumber"}, modNumberPin(b.modNum), "ModNumber from the layers' states")
	}
	p.Modifiers.ModNumber = modNumberPin(b.modNum)
	b.t.Modifiers.ModNumber = b.modNum
	b.colOf = map[int]int{}
	cols := 0
	for s, c := range b.modNum {
		if c == windows.SHFT_INVALID {
			continue
		}
		if _, ok := b.colOf[s]; !ok {
			b.colOf[s] = int(c)
		}
		cols = max(cols, int(c)+1)
	}
	if cols > windows.MaxColumns {
		b.lost("windows.tooManyColumns", "/hardware/layers", "%d columns; Windows has 15", cols)
	}
	b.states = make([]int, cols)
	for i := range b.states {
		b.states[i] = -1
	}
	for s, c := range b.modNum {
		if c != windows.SHFT_INVALID && int(c) < cols && b.states[c] < 0 {
			b.states[c] = s
		}
	}
	for c, s := range b.states {
		if s < 0 {
			b.lost("windows.unreachableState", "/targets/windows/modifiers/modNumber", "column %d has no modifier state", c)
			b.states[c] = 0
		}
		b.reps = append(b.reps, b.representative(s))
	}
	b.checkLayers()
}

func (b *lowerer) bitsPin() []model.ModifierBit {
	var out []model.ModifierBit
	for _, mb := range b.bits {
		out = append(out, model.ModifierBit{VK: vk.Name(mb.vk), Bit: int(mb.bit), Component: mb.comp})
	}
	return out
}

func modNumberPin(mn []byte) []*int {
	out := make([]*int, len(mn))
	for i, c := range mn {
		if c != windows.SHFT_INVALID {
			v := int(c)
			out[i] = &v
		}
	}
	return out
}

func (b *lowerer) bitOf(comp string) (byte, bool) {
	for _, mb := range b.bits {
		if mb.comp == comp {
			return mb.bit, true
		}
	}
	return 0, false
}

// stateOf is the Windows state of a modifier set (§4): ok is false for sets with caps or a lock, and for other.
func (b *lowerer) stateOf(m model.ModifierSet) (int, bool) {
	if m.IsOther() {
		return 0, false
	}
	s := 0
	for _, c := range m {
		switch c {
		case "shift":
			s |= 1
		case "ctrl", "ctrlL", "ctrlR":
			s |= 2
		case "alt", "altL":
			s |= 4
		case "altR":
			if b.altGr {
				s |= 6
			} else {
				s |= 4
			}
		case "caps":
			return 0, false
		default:
			if cm := b.l.CustomModifiers[c]; cm != nil && cm.Kind == "lock" {
				return 0, false
			}
			if bit, ok := b.bitOf(c); ok {
				s |= int(bit)
			}
		}
	}
	return s, true
}

func (b *lowerer) defaultModNumber() []byte {
	set := map[int]bool{}
	other := false
	if b.l.Hardware != nil {
		for _, layer := range b.l.Hardware.Layers {
			for _, m := range layer.Modifiers {
				if m.IsOther() {
					other = true
				}
				if s, ok := b.stateOf(m); ok {
					set[s] = true
				}
			}
		}
	}
	set[0] = set[0] || len(set) == 0
	var states []int
	for s := range set {
		states = append(states, s)
	}
	sort.Ints(states)
	maxS := states[len(states)-1]
	mn := make([]byte, maxS+1)
	for i := range mn {
		mn[i] = windows.SHFT_INVALID
	}
	for c, s := range states {
		mn[s] = byte(c)
	}
	if other {
		oc := byte(len(states))
		for s := range mn {
			if mn[s] == windows.SHFT_INVALID && !(s&4 != 0 && s&2 == 0) {
				mn[s] = oc
			}
		}
	}
	return mn
}

// representative is the modifier state of a Windows state (§4).
func (b *lowerer) representative(s int) model.State {
	st := model.State{Custom: map[string]bool{}}
	if s&1 != 0 {
		st.Shift = true
	}
	if s&6 == 6 && b.altGr {
		st.AltR = true
	} else {
		st.CtrlL = s&2 != 0
		st.AltL = s&4 != 0
	}
	for _, mb := range b.bits {
		if mb.bit >= 8 && s&int(mb.bit) != 0 {
			st.Custom[mb.comp] = true
		}
	}
	return st
}

// stateFromSet turns a modifier set into a concrete state (left keys for ctrl and alt).
func stateFromSet(m model.ModifierSet) model.State {
	st := model.State{Custom: map[string]bool{}}
	for _, c := range m {
		switch c {
		case "shift":
			st.Shift = true
		case "ctrl", "ctrlL":
			st.CtrlL = true
		case "ctrlR":
			st.CtrlR = true
		case "alt", "altL":
			st.AltL = true
		case "altR":
			st.AltR = true
		case "caps":
			st.Caps = true
		default:
			st.Custom[c] = true
		}
	}
	return st
}

// checkLayers reports layers Windows widens or never reaches (§4).
func (b *lowerer) checkLayers() {
	if b.l.Hardware == nil {
		return
	}
	for i, layer := range b.l.Hardware.Layers {
		for _, m := range layer.Modifiers {
			path := fmt.Sprintf("/hardware/layers/%d", i)
			if m.Has("ctrlL") || m.Has("ctrlR") || m.Has("altL") || (m.Has("altR") && !b.altGr) {
				b.lost("windows.sideModifierWidened", path, "modifier set %v also answers the other side on Windows", []string(m))
			}
			if s, ok := b.stateOf(m); ok && s&4 != 0 && s&2 == 0 {
				b.lost("windows.altOnlyLayer", path, "modifier set %v is reached only with Alt and without Ctrl: Windows never reaches it", []string(m))
			}
		}
	}
}
