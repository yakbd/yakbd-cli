package build

import (
	"sort"
	"strings"

	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// numpadOn maps a number-pad key's Num-Lock-off VK to its Num-Lock-on VK (windows-behaviour §3.2).
var numpadOn = map[byte]byte{0x24: 0x67, 0x26: 0x68, 0x21: 0x69, 0x25: 0x64, 0x0C: 0x65, 0x27: 0x66, 0x23: 0x61,
	0x28: 0x62, 0x22: 0x63, 0x2D: 0x60, 0x2E: 0x6E}

// numLockDefault are the Table A positions that are numLock positions by default (§5).
var numLockDefault = map[string]bool{"Numpad0": true, "Numpad1": true, "Numpad2": true, "Numpad3": true,
	"Numpad4": true, "Numpad5": true, "Numpad6": true, "Numpad7": true, "Numpad8": true, "Numpad9": true,
	"NumpadDecimal": true}

func (b *lowerer) bound(id string) bool {
	if b.l.Hardware != nil {
		for _, layer := range b.l.Hardware.Layers {
			if _, ok := layer.Bindings[id]; ok {
				return true
			}
		}
	}
	return false
}

// positions decides the VK and flags of every position and builds the scan-code tables (§5).
func (b *lowerer) positions() {
	l, f, p := b.l, b.file, b.pin
	tableA := f.UseTableA == nil || *f.UseTableA
	p.UseTableA = f.UseTableA
	b.posByID = map[string]*posInfo{}
	needed := map[string]bool{}
	for _, cm := range l.CustomModifiers {
		for _, id := range cm.Positions {
			needed[id] = true
		}
	}
	for _, t := range f.Tables {
		for _, r := range t.Rows {
			if r.Position != "" {
				needed[r.Position] = true
			}
		}
	}
	var defaultedVK, defaultedFlags []string
	usedScan := map[uint16]bool{}
	for _, id := range sortedKeys(l.Positions) {
		ps := l.Positions[id]
		binds := b.bound(id)
		if ps.ScanCode == "" {
			if binds {
				b.lost("windows.scanCodeMissing", "/positions/"+id, "position %s has bindings and no scanCode", id)
			}
			continue
		}
		sc, _ := scanOf(ps.ScanCode)
		if sc>>8 == vk.PrefixE1 && sc != 0xE11D {
			b.lost("windows.scanCodeUnsupported", "/positions/"+id, "scan code %s cannot be in a Windows table", ps.ScanCode)
			continue
		}
		pi := &posInfo{id: id, scan: sc, name: ps.Name}
		wk := f.Keys[id]
		if wk != nil && wk.VK != "" {
			v, ok := vk.Parse(wk.VK)
			if !ok {
				b.lost("windows.vkMissing", "/targets/windows/keys/"+id+"/vk", "unknown virtual key %q", wk.VK)
				continue
			}
			pi.vk = v
		} else if !tableA {
			// useTableA false (D54): no default VK
		} else if d, ok := vk.DefaultForPosition(id); ok {
			pi.vk = byte(d.VK)
			defaultedVK = append(defaultedVK, id)
		} else if d, ok := vk.DefaultFor(sc); ok {
			pi.vk = byte(d)
			defaultedVK = append(defaultedVK, id)
		}
		if pi.vk == 0 {
			if binds || needed[id] {
				b.lost("windows.vkMissing", "/positions/"+id, "position %s needs a virtual key: pin targets.windows.keys.%s.vk", id, id)
			}
			b.keyName(pi) // key names are per scan code and need no VK
			continue
		}
		if wk != nil && wk.ScanCodeFlags != nil {
			for _, n := range wk.ScanCodeFlags {
				bit, _ := vk.FlagBit(n)
				pi.flags |= bit
			}
		} else {
			pi.flags = vk.DefaultFlags(sc)
			if true {
				defaultedFlags = append(defaultedFlags, id)
			}
		}
		if ps.NumLock != nil {
			pi.numLock = *ps.NumLock
		} else {
			pi.numLock = tableA && numLockDefault[id]
		}
		if wk != nil {
			pi.attrs = wk.Attributes
		}
		b.addPos(pi)
		usedScan[sc] = true
	}
	// Table A rows whose scan code no position of the file uses, whatever VKs the positions have (§5, D86).
	b.added = map[string]*model.Position{}
	var addedIDs []string
	for _, d := range vk.Defaults {
		if !tableA {
			break // useTableA false (D54): no row is added
		}
		v := byte(d.VK)
		if usedScan[d.Scan] {
			continue
		}
		if _, clash := b.posByID[d.Position]; clash {
			continue
		}
		pi := &posInfo{id: d.Position, scan: d.Scan, vk: v, flags: d.VK & 0xFF00, fromTable: true, numLock: numLockDefault[d.Position]}
		b.addPos(pi)
		b.added[d.Position] = &model.Position{ScanCode: scanString(d.Scan)}
		addedIDs = append(addedIDs, d.Position)
	}
	if len(defaultedVK) > 0 || len(addedIDs) > 0 {
		b.defaulted("vk", map[string]any{"positions": defaultedVK, "addedFromTableA": addedIDs},
			"virtual keys from Table A for %d position(s); %d Table A position(s) added", len(defaultedVK), len(addedIDs))
	}
	if len(defaultedFlags) > 0 {
		b.defaulted("scanCodeFlags", defaultedFlags, "scan-code flags by rule for %d position(s)", len(defaultedFlags))
	}
	// Duplicates (§5); not reported with useTableA false, where every VK is explicit (D59), nor between a position
	// of the file and a Table A row added above (D86: the added row only gives the key its VK; it never owns the
	// character row).
	byVK := map[byte][]*posInfo{}
	for _, pi := range b.pos {
		if pi.fromTable {
			continue
		}
		byVK[pi.vk] = append(byVK[pi.vk], pi)
	}
	for v, list := range byVK {
		if tableA && len(list) > 1 && v != 0x0D && v != vk.None && !b.tableAShares(list) {
			b.lost("windows.vkDuplicate", "/positions/"+list[1].id, "positions %s and %s both get %s", list[0].id, list[1].id, vk.Name(v))
		}
	}
	// Scan-code tables.
	size := vk.MaxVSC
	for _, pi := range b.pos {
		if pi.scan>>8 == 0 && int(pi.scan)+1 > size {
			size = int(pi.scan) + 1
		}
	}
	if f.ScanCodeTableSize != nil {
		size = *f.ScanCodeTableSize
	} else {
		b.defaulted("scanCodeTableSize", size, "bMaxVSCtoVK %02x", size)
	}
	p.ScanCodeTableSize = &size
	b.t.ScanToVK = make([]uint16, size)
	for i := range b.t.ScanToVK {
		b.t.ScanToVK[i] = vk.None
	}
	p.Keys = map[string]*model.WindowsKey{}
	for _, pi := range b.pos {
		v := uint16(pi.vk) | pi.flags
		// A position pinned to _none_ (FF) without flags only keeps its scan code free of a Table A row: Windows
		// reads FF as "no key", and an E0 or E1 entry of that kind is left out (docs/dll-import.md).
		absent := pi.vk == vk.None && pi.flags == 0
		switch pi.scan >> 8 {
		case 0:
			if int(pi.scan) < len(b.t.ScanToVK) {
				b.t.ScanToVK[pi.scan] = v
			}
		case vk.PrefixE0:
			if !absent {
				b.t.E0 = append(b.t.E0, windows.ScanVK{Scan: byte(pi.scan), VK: v})
			}
		case vk.PrefixE1:
			if !absent {
				b.t.E1 = append(b.t.E1, windows.ScanVK{Scan: byte(pi.scan), VK: v})
			}
		}
		p.Keys[pi.id] = &model.WindowsKey{VK: vk.Name(pi.vk), ScanCodeFlags: vk.FlagList(pi.flags), Attributes: pinnedOr(pi.attrs)}
		b.keyName(pi)
	}
	sort.SliceStable(b.t.KeyNames, func(i, j int) bool { return b.t.KeyNames[i].Scan < b.t.KeyNames[j].Scan })
	sort.SliceStable(b.t.KeyNamesExt, func(i, j int) bool { return b.t.KeyNamesExt[i].Scan < b.t.KeyNamesExt[j].Scan })
}

// keyName adds a position's name to pKeyNames (unprefixed) or pKeyNamesExt (E0).
func (b *lowerer) keyName(pi *posInfo) {
	if pi.name == "" {
		return
	}
	kn := windows.KeyName{Scan: byte(pi.scan), Name: pi.name}
	if pi.scan>>8 == vk.PrefixE0 {
		b.t.KeyNamesExt = append(b.t.KeyNamesExt, kn)
	} else if pi.scan>>8 == 0 {
		b.t.KeyNames = append(b.t.KeyNames, kn)
	}
}

// tableAShares reports a VK that Table A itself gives to two positions (D38): Enter and NumpadEnter, the number-pad
// keys and their navigation twins, SysRq and Print Screen.
func (b *lowerer) tableAShares(list []*posInfo) bool {
	// D38: a VK that Table A itself gives to every one of the positions, and that each of them keeps.
	for _, pi := range list {
		if d, ok := vk.DefaultFor(pi.scan); !ok || byte(d) != pi.vk {
			return false
		}
	}
	return true
}

func (b *lowerer) addPos(pi *posInfo) {
	b.pos = append(b.pos, pi)
	b.posByID[pi.id] = pi
	sort.SliceStable(b.pos, func(i, j int) bool { return b.pos[i].scan < b.pos[j].scan })
}

func scanString(sc uint16) string {
	if sc>>8 != 0 {
		return strings.ToUpper(strconvHex(sc, 4))
	}
	return strings.ToUpper(strconvHex(sc, 2))
}

func strconvHex(v uint16, n int) string {
	const digits = "0123456789abcdef"
	out := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		out[i] = digits[v&0xF]
		v >>= 4
	}
	return string(out)
}

func pinnedOr(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}
