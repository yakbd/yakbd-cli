// Package build lowers a format-neutral layout (model.Layout) into the Windows intermediate (target/windows) by
// the pins of targets.windows and, where the file is silent, the default rules of the kbl specification's
// targets/windows.md. Every value a default rule decides is reported as a `windowsDefault.<rule>` entry and kept in
// Result.Pins, so that `pin` can write it into the file. Everything Windows cannot express is reported with the
// loss kinds of SPEC §17.3.
package build

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
	"github.com/yakbd/yakbd-cli/policy"
	"github.com/yakbd/yakbd-cli/target/windows"
	"github.com/yakbd/yakbd-cli/target/windows/vk"
)

// Options of a build.
type Options struct {
	ToolVersion [4]uint16
	// Policy, if set, is applied after the file's onLoss policies (for example the command line's -strict-defaults).
	Policy *model.OnLoss
}

// Result is a lowered layout.
type Result struct {
	DLL *windows.DLL
	// Targets are the requested builds the compiler supports, in the order of the file.
	Targets []windows.Target
	// Pins holds every value of targets.windows, pinned by the file or decided by a rule.
	Pins *model.WindowsTarget
	// AddedPositions are the Table A positions the DLL contains that the file lacks.
	AddedPositions map[string]*model.Position

	positions []PositionInfo
	columns   []int
}

// Lower lowers l. The result is nil when the report has errors after the file's onLoss policies are applied.
func Lower(l *model.Layout, opt Options) (*Result, *loss.Report) {
	b := &lowerer{l: l, r: &loss.Report{}, opt: opt}
	b.file = &model.WindowsTarget{}
	if l.Targets != nil && l.Targets.Windows != nil {
		b.file = l.Targets.Windows
	}
	b.pin = &model.WindowsTarget{}
	b.norm = l.Normalizes()
	res := b.run()
	policy.Resolve(b.r, l, "windows", opt.Policy)
	if b.r.HasErrors() {
		return nil, b.r
	}
	return res, b.r
}

type lowerer struct {
	l    *model.Layout
	r    *loss.Report
	opt  Options
	file *model.WindowsTarget // the file's pins (never nil)
	pin  *model.WindowsTarget // every decided value

	altGr         bool
	bits          []modBit
	states        []int         // column -> representative state
	colOf         map[int]int   // state -> column
	reps          []model.State // column -> representative modifier state
	modNum        []byte
	pos           []*posInfo
	posByID       map[string]*posInfo
	dead          map[string]uint16 // dead-key id -> dead character
	t             windows.Tables
	added         map[string]*model.Position
	capsUnmatched []string
	attrDefaulted []string
	norm          bool            // settings.normalization is enabled: outputs are NFC-normalised
	normSeen      map[string]bool // path and output already reported as normalised
}

type modBit struct {
	vk   byte
	bit  byte
	comp string
}

type posInfo struct {
	id        string
	scan      uint16
	vk        byte
	flags     uint16
	numLock   bool
	fromTable bool // added from Table A
	name      string
	attrs     []string
}

func (b *lowerer) lost(kind, path, format string, args ...any) {
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

// defaulted reports a use of default rule `rule` that filled the members at the JSON pointers fields
// (DefaultedEntry).
func (b *lowerer) defaulted(rule string, fields []string, value any, format string, args ...any) {
	b.r.Append(DefaultedEntry(rule, fields, value, fmt.Sprintf(format, args...)))
}

// DefaultedEntry is the report entry of a default rule that filled the members at the JSON pointers fields (at
// least one): its location is the one member, or the deepest object holding them all with the pointers in
// detail.fields (docs/report-file.md §4).
func DefaultedEntry(rule string, fields []string, value any, msg string) loss.Entry {
	e := loss.Entry{Class: loss.Defaulted, Severity: loss.Accepted, Code: "windowsDefault." + rule,
		Where: loss.Where{Path: commonParent(fields)}, Message: msg, Detail: map[string]any{"value": value}}
	if len(fields) > 1 {
		e.Detail["fields"] = fields
	}
	return e
}

// commonParent is the one pointer of ps, or the longest pointer that is a parent of all of them.
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

func (b *lowerer) run() *Result {
	b.meta()
	b.modifiers()
	b.positions()
	b.deadCharacters()
	b.rows()
	b.deadKeys()
	b.drops()
	d := b.resources()
	res := &Result{DLL: d, Targets: b.targets(), Pins: b.pin, AddedPositions: b.added, columns: b.states}
	for _, pi := range b.pos {
		rv := pi.vk
		if on, ok := numpadOn[pi.vk]; ok && pi.numLock {
			rv = on
		}
		res.positions = append(res.positions, PositionInfo{ID: pi.id, Scan: pi.scan, VK: pi.vk, RowVK: rv, Flags: pi.flags, NumLock: pi.numLock, FromTableA: pi.fromTable})
	}
	return res
}

// meta decides dllName, builds and the flags (targets/windows.md §2, §3).
func (b *lowerer) meta() {
	l, f, p := b.l, b.file, b.pin
	p.DLLName = f.DLLName
	if p.DLLName == "" {
		p.DLLName = DefaultDLLName(l.Metadata.ID)
		b.defaulted("dllName", []string{"/targets/windows/dllName"}, p.DLLName, "DLL name %s from metadata.id", p.DLLName)
	}
	p.Builds = f.Builds
	if len(p.Builds) == 0 {
		// D39: every build the tool can make; arm64x since its writer passed the gate of D44 (VM-6, D66).
		p.Builds = []string{"x86", "x64", "wow64", "arm64x"}
		b.r.Append(loss.Entry{Class: loss.Defaulted, Severity: loss.Accepted, Code: "windowsDefault.builds",
			Where: loss.Where{Path: "/targets/windows/builds"}, Message: "builds x86, x64, wow64, arm64x",
			Detail: map[string]any{"value": p.Builds, "skipped": []string{}}})
	}
	has := map[string]bool{}
	for _, x := range p.Builds {
		has[x] = true
	}
	if (has["x64"] || has["arm64x"]) && !has["wow64"] {
		b.lost("windows.wow64Missing", "/targets/windows/builds", "x64 or arm64x without wow64: 32-bit programs cannot load the layout")
	}
	lf := &model.LocaleFlags{}
	if f.LocaleFlags != nil {
		*lf = *f.LocaleFlags
	}
	if lf.AltGr == nil {
		v := false
		if l.Hardware != nil {
			for _, layer := range l.Hardware.Layers {
				for _, m := range layer.Modifiers {
					v = v || m.Has("altR")
				}
			}
		}
		lf.AltGr = &v
		b.defaulted("altGr", []string{"/targets/windows/localeFlags/altGr"}, v, "KLLF_ALTGR %v (a layer uses altR: %v)", v, v)
	}
	if lf.ShiftLock == nil {
		v := l.Settings != nil && l.Settings.CapsLockOffBy == "shift"
		lf.ShiftLock = &v
		b.defaulted("shiftLock", []string{"/targets/windows/localeFlags/shiftLock"}, v, "KLLF_SHIFTLOCK %v from settings.capsLockOffBy", v)
	}
	if lf.LrmRlm == nil {
		v := l.Settings != nil && l.Settings.BidiMarksOnShiftBackspace != nil && *l.Settings.BidiMarksOnShiftBackspace
		lf.LrmRlm = &v
		b.defaulted("lrmRlm", []string{"/targets/windows/localeFlags/lrmRlm"}, v, "KLLF_LRM_RLM %v from settings.bidiMarksOnShiftBackspace", v)
	}
	p.LocaleFlags = lf
	b.altGr = *lf.AltGr
	if *lf.AltGr {
		b.t.LocaleFlags |= windows.KLLF_ALTGR
	}
	if *lf.ShiftLock {
		b.t.LocaleFlags |= windows.KLLF_SHIFTLOCK
	}
	if *lf.LrmRlm {
		b.t.LocaleFlags |= windows.KLLF_LRM_RLM
	}
	p.KbdVersion = f.KbdVersion
	if p.KbdVersion == nil {
		v := windows.KBD_VERSION
		p.KbdVersion = &v
		b.defaulted("kbdVersion", []string{"/targets/windows/kbdVersion"}, v, "version word %d", v)
	}
	b.t.Version = uint16(*p.KbdVersion)
	p.KeyboardType = f.KeyboardType
	if p.KeyboardType == nil {
		p.KeyboardType = &model.KeyboardType{}
		b.defaulted("keyboardType", []string{"/targets/windows/keyboardType"}, map[string]int{"type": 0, "subType": 0}, "dwType and dwSubType 0")
	}
	b.t.Type, b.t.SubType = p.KeyboardType.Type, p.KeyboardType.SubType
}

// DefaultDLLName is rule dllName: metadata.id in lower case with everything outside a-z, 0-9 removed, prefixed
// with k if empty or starting with a digit, cut to 8 characters.
func DefaultDLLName(id string) string {
	var s []byte
	for _, c := range []byte(strings.ToLower(id)) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			s = append(s, c)
		}
	}
	if len(s) == 0 || s[0] >= '0' && s[0] <= '9' {
		s = append([]byte{'k'}, s...)
	}
	if len(s) > 8 {
		s = s[:8]
	}
	return string(s)
}

func (b *lowerer) targets() []windows.Target {
	var out []windows.Target
	for _, x := range b.pin.Builds {
		switch x {
		case "x64":
			out = append(out, windows.TargetAMD64)
		case "wow64":
			out = append(out, windows.TargetWOW64)
		case "x86":
			out = append(out, windows.TargetX86)
		case "arm64x":
			out = append(out, windows.TargetARM64X)
		}
	}
	return out
}

// ParseVK parses a VK of the format: a name of the table (any case, without VK_), 0-9, A-Z, or 0xNN.
func ParseVK(s string) (byte, bool) { return vk.Parse(s) }

func scanOf(s string) (uint16, bool) {
	v, err := strconv.ParseUint(s, 16, 16)
	return uint16(v), err == nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PositionInfo is one position of the built DLL.
type PositionInfo struct {
	ID         string
	Scan       uint16
	VK         byte // the position's own VK
	RowVK      byte // the VK of its character row (the Num-Lock-on VK for number-pad keys)
	Flags      uint16
	NumLock    bool
	FromTableA bool
}

// Positions returns the positions of the build in scan-code order.
func (r *Result) PositionList() []PositionInfo { return r.positions }

// Columns returns the representative Windows state of each column.
func (r *Result) Columns() []int { return r.columns }

// fieldsOf is the pointer /targets/windows/<object>/<id>/<member> of each id.
func fieldsOf(ids []string, object, member string) []string {
	var out []string
	for _, id := range ids {
		out = append(out, model.Pointer("targets", "windows", object, id, member))
	}
	return out
}

// memberFields is the pointer /targets/windows/<object>/<member> of each member.
func memberFields(object string, members []string) []string {
	var out []string
	for _, m := range members {
		out = append(out, model.Pointer("targets", "windows", object, m))
	}
	return out
}

// vkFields are the members rule vk fills: the virtual key of each position, those added from Table A included
// (whose /positions/<id> pin writes too; detail.value.addedFromTableA names them).
func vkFields(defaulted, added []string) []string {
	return fieldsOf(append(append([]string{}, defaulted...), added...), "keys", "vk")
}
