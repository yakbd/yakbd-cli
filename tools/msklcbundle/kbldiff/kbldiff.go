// Package kbldiff compares two .kbl.json files by what they mean, not by how they are written: the file that
// `yakbd convert x.klc` writes and the file that `yakbd convert x.dll` writes for a DLL built from the same .klc
// (docs/dll-import.md §6). The two are written differently by design (coverage/windows.md §10 and §11 of the kbl
// specification): the DLL import has a position for every scan code, pins every VK, the scan-code flags, the
// tables and useTableA: false, and names the maker under metadata.attribution instead of metadata.organization.
// Facts flattens a file into keyed facts in which those differences of form disappear; Diff compares two such
// sets.
package kbldiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Facts are the meaning of one file, keyed by a readable path. Keys that start with "?" are compared only when
// both files have them (a .klc import pins a VK and the attributes only for the keys the file lists; a DLL import
// for every key).
type Facts map[string]string

// Read flattens a .kbl.json document. What it leaves out, and why (docs/dll-import.md §6):
//   - metadata.provenance (says where the file came from), metadata.version and targets.klc (the .klc VERSION
//     line; the DLL has only the version resource), targets.windows.versionInfo (version resource; MSKLC
//     writes its own names into it, OBS D7);
//   - positions without bindings, names, VK or attributes in the other file, and the position ids themselves:
//     everything is keyed by scan code;
//   - layer ids and their order: bindings are keyed by each of the layer's modifier sets;
//   - targets.windows.useTableA, .tables, .scanCodeTableSize, .keyboardType, .kbdVersion, .registration,
//     the keys' scanCodeFlags and numLock (pinned by the DLL import only; their effect is compared through the
//     tables, not here);
//   - metadata.organization and metadata.attribution are one fact, "maker" (D58: the import never names the
//     maker as organization).
func Read(doc []byte) (Facts, error) {
	d := json.NewDecoder(bytes.NewReader(doc))
	d.UseNumber()
	var root map[string]any
	if err := d.Decode(&root); err != nil {
		return nil, err
	}
	f := Facts{}
	meta, _ := root["metadata"].(map[string]any)
	// Metadata the .klc does not state is filled by each compiler's defaults (MSKLC's own, OBS D6, D7), so these
	// are compared only where both files have them; an id "und" (a .klc without LOCALENAME) states nothing. The name
	// is not compared: the .klc import takes KBD's description, the DLL import string 1000 (coverage §10 item 5, §11
	// item 12), which differ whenever DESCRIPTIONS does; string 1000 itself
	// is compared as windows.strings.layoutName.
	for _, k := range []string{"id", "copyright", "localizedNames"} {
		if v, ok := meta[k]; ok && !(k == "id" && v == "und") {
			f["?metadata."+k] = canon(v)
		}
	}
	if v, ok := meta["organization"]; ok {
		f["?maker"] = canon(v)
	} else if v, ok := meta["attribution"]; ok {
		f["?maker"] = canon(v)
	}
	sc := map[string]string{} // position id -> scan code (or "pos:<id>" for a position without one)
	positions, _ := root["positions"].(map[string]any)
	for id, p := range positions {
		pm, _ := p.(map[string]any)
		code, _ := pm["scanCode"].(string)
		if code == "" {
			code = "pos:" + id
		} else {
			code = strings.ToUpper(code)
		}
		sc[id] = code
		f["@position "+code] = id
		if n, ok := pm["name"]; ok {
			f["name "+code] = canon(n)
		}
		for k, v := range pm {
			if k == "numLock" {
				f["?numLock "+code] = canon(v)
			} else if k != "scanCode" && k != "name" && k != "description" {
				f["position "+code+" "+k] = canon(v)
			}
		}
	}
	key := func(id string) string {
		if c, ok := sc[id]; ok {
			return c
		}
		return "pos:" + id
	}
	if hw, ok := root["hardware"].(map[string]any); ok {
		layers, _ := hw["layers"].([]any)
		for _, l := range layers {
			lm, _ := l.(map[string]any)
			mods, _ := lm["modifiers"].([]any)
			bindings, _ := lm["bindings"].(map[string]any)
			for _, set := range mods {
				ms := modSet(set)
				for id, b := range bindings {
					f["@bound "+key(id)] = "yes"
					k := "bind " + key(id) + " [" + ms + "]"
					if old, dup := f[k]; dup && old != canon(b) {
						f[k] = old + " | " + canon(b)
					} else {
						f[k] = canon(b)
					}
				}
			}
		}
		for k, v := range hw {
			if k != "layers" {
				f["hardware."+k] = canon(v)
			}
		}
	}
	for k, v := range map[string]any{"settings": root["settings"], "customModifiers": root["customModifiers"]} {
		if m, ok := v.(map[string]any); ok {
			for kk, vv := range m {
				f[k+"."+kk] = canon(vv)
			}
		}
	}
	if dks, ok := root["deadKeys"].(map[string]any); ok {
		for id, dk := range dks {
			m, _ := dk.(map[string]any)
			for k, v := range m {
				if k == "entries" {
					continue
				}
				f["deadKey "+id+" "+k] = canon(v)
			}
			entries, _ := m["entries"].([]any)
			for _, e := range entries {
				em, _ := e.(map[string]any)
				k := "deadKey " + id + " entry " + canon(em["base"])
				if _, dup := f[k]; !dup { // the first entry for a base wins (dll §4.3)
					f[k] = canon(em["result"])
				}
			}
		}
	}
	targets, _ := root["targets"].(map[string]any)
	win, _ := targets["windows"].(map[string]any)
	skip := map[string]bool{"useTableA": true, "tables": true, "scanCodeTableSize": true, "keyboardType": true,
		"kbdVersion": true, "registration": true, "versionInfo": true, "keys": true, "onLoss": true}
	for k, v := range win {
		if skip[k] {
			continue
		}
		if k == "strings" {
			// A .klc without DESCRIPTIONS, LANGUAGENAMES or LOCALENAME leaves the strings to the build's
			// defaults, which the DLL then holds; an empty string is not written into a DLL (dll §1.6). So
			// strings are compared where both files have them, and an empty one counts as absent.
			s := Facts{}
			flatten(s, "windows.strings", v)
			for kk, vv := range s {
				if vv != `""` {
					f["?"+kk] = vv
				}
			}
			continue
		}
		flatten(f, "windows."+k, v)
	}
	if keys, ok := win["keys"].(map[string]any); ok {
		for id, kv := range keys {
			m, _ := kv.(map[string]any)
			for k, v := range m {
				switch k {
				case "scanCodeFlags", "numLock":
				case "vk", "attributes":
					f["?"+k+" "+key(id)] = canon(v)
				default:
					f["windows.keys "+key(id)+" "+k] = canon(v)
				}
			}
		}
	}
	return f, nil
}

// flatten writes objects one level per key, arrays and scalars as one canonical value.
func flatten(f Facts, path string, v any) {
	if m, ok := v.(map[string]any); ok && len(m) > 0 {
		for k, vv := range m {
			flatten(f, path+"."+k, vv)
		}
		return
	}
	f[path] = canon(v)
}

func modSet(v any) string {
	arr, _ := v.([]any)
	var s []string
	for _, x := range arr {
		s = append(s, fmt.Sprint(x))
	}
	sort.Strings(s)
	return strings.Join(s, " ")
}

// canon is JSON with sorted object keys (encoding/json sorts map keys) and no HTML escaping.
func canon(v any) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(v)
	return strings.TrimSpace(b.String())
}

// Difference is one fact that the two files do not share.
type Difference struct {
	Key, A, B string // A or B is "" when only the other file has the fact
}

func (d Difference) String() string {
	switch {
	case d.A == "":
		return fmt.Sprintf("%s: only in the second (%s)", d.Key, d.B)
	case d.B == "":
		return fmt.Sprintf("%s: only in the first (%s)", d.Key, d.A)
	}
	return fmt.Sprintf("%s: %s vs %s", d.Key, d.A, d.B)
}

// Diff returns the facts of a and b that differ, sorted by key. a is the file of the .klc, b the file of the DLL.
// Three rules keep differences of form out:
//   - a fact only one file has is a difference, except a compare-when-both fact ("?") and per-key facts of b for a
//     scan code that has no position in a, or a position without any binding (a DLL import has a position for
//     every scan code and gives every position of a VK that VK's row; the .klc file leaves such keys to Windows'
//     defaults, whose effect the table comparison checks);
//   - a Caps Lock binding only one file has equals that file's other binding without Caps Lock when the files
//     keep Caps Lock fallback (settings.capsLockFallback, true unless false): the build writes the fallback into
//     the Caps Lock row, and the import reads it back as a binding of its own;
//   - "@" keys are bookkeeping and never reported.
func Diff(a, b Facts) []Difference {
	var out []Difference
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			if !strings.HasPrefix(k, "?") && !strings.HasPrefix(k, "@") && !capsFallback(a, b, k, va) {
				out = append(out, Difference{k, va, ""})
			}
			continue
		}
		if va != vb {
			out = append(out, Difference{strings.TrimPrefix(k, "?"), va, vb})
		}
	}
	for k, vb := range b {
		if _, ok := a[k]; ok || strings.HasPrefix(k, "?") || strings.HasPrefix(k, "@") {
			continue
		}
		if code := factCode(k); code != "" {
			if _, ok := a["@position "+code]; !ok {
				continue
			}
			if _, ok := a["@bound "+code]; !ok && strings.HasPrefix(k, "bind ") {
				continue
			}
		}
		if capsFallback(b, a, k, vb) {
			continue
		}
		out = append(out, Difference{k, "", vb})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// capsFallback says whether the binding k = v of one file, which the other file lacks, is the other file's
// Caps Lock fallback: k has "caps" in its modifier set and the other file binds the same key and set without
// caps to v.
func capsFallback(has, lacks Facts, k, v string) bool {
	if !strings.HasPrefix(k, "bind ") || lacks["settings.capsLockFallback"] == "false" {
		return false
	}
	i := strings.IndexByte(k, '[')
	if i < 0 {
		return false
	}
	var rest []string
	caps := false
	for _, m := range strings.Fields(strings.Trim(k[i:], "[]")) {
		if m == "caps" {
			caps = true
		} else {
			rest = append(rest, m)
		}
	}
	if !caps {
		return false
	}
	return lacks[k[:i]+"["+strings.Join(rest, " ")+"]"] == v
}

// factCode is the scan code a per-position fact is about ("" for other facts).
func factCode(k string) string {
	for _, p := range []string{"bind ", "name ", "position "} {
		if strings.HasPrefix(k, p) {
			rest := k[len(p):]
			if i := strings.IndexByte(rest, ' '); i >= 0 {
				return rest[:i]
			}
			return rest
		}
	}
	return ""
}

// Files reads and compares two documents.
func Files(a, b []byte) ([]Difference, error) {
	fa, err := Read(a)
	if err != nil {
		return nil, fmt.Errorf("first file: %w", err)
	}
	fb, err := Read(b)
	if err != nil {
		return nil, fmt.Errorf("second file: %w", err)
	}
	return Diff(fa, fb), nil
}

// Redacted is String without the second file's value for the facts a compiler fills with its own defaults when
// the .klc leaves them open (metadata, maker, strings: MSKLC writes its own names there, OBS D6, D7), so that no
// text of MSKLC's leaves the guest.
func (d Difference) Redacted() string {
	k := d.Key
	if !strings.HasPrefix(k, "metadata.") && k != "maker" && !strings.HasPrefix(k, "windows.strings.") {
		return d.String()
	}
	switch {
	case d.A == "":
		return k + ": only in the second"
	case d.B == "":
		return fmt.Sprintf("%s: only in the first (%s)", k, d.A)
	}
	return fmt.Sprintf("%s: %s vs another value", k, d.A)
}
