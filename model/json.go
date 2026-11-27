package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Units is a CodeUnits value (SPEC §2.2): UTF-16 code units, written {"utf16": ["D83D", "DE00"]}.
type Units []uint16

type unitsJSON struct {
	UTF16 []string `json:"utf16"`
}

// MarshalJSON writes {"utf16": [...]} with upper-case hex digits.
func (u Units) MarshalJSON() ([]byte, error) {
	s := make([]string, len(u))
	for i, c := range u {
		s[i] = fmt.Sprintf("%04X", c)
	}
	return json.Marshal(unitsJSON{s})
}

// UnmarshalJSON reads {"utf16": [...]}.
func (u *Units) UnmarshalJSON(b []byte) error {
	var j unitsJSON
	if err := strictUnmarshal(b, &j); err != nil {
		return err
	}
	if len(j.UTF16) == 0 {
		return fmt.Errorf("utf16: empty")
	}
	out := make(Units, len(j.UTF16))
	for i, s := range j.UTF16 {
		v, err := strconv.ParseUint(s, 16, 16)
		if err != nil || len(s) != 4 {
			return fmt.Errorf("utf16: %q is not 4 hex digits", s)
		}
		out[i] = uint16(v)
	}
	*u = out
	return nil
}

func strictUnmarshal(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

func isObject(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '{'
}

// TextOrUnits is a Text or a CodeUnits value.
type TextOrUnits struct {
	Text  string
	Units Units // non-nil: the CodeUnits form
}

// UTF16 returns the value as UTF-16 units.
func (t TextOrUnits) UTF16() []uint16 {
	if t.Units != nil {
		return t.Units
	}
	return utf16.Encode([]rune(t.Text))
}

// MarshalJSON writes the form the value has.
func (t TextOrUnits) MarshalJSON() ([]byte, error) {
	if t.Units != nil {
		return t.Units.MarshalJSON()
	}
	return marshalString(t.Text)
}

// UnmarshalJSON reads either form.
func (t *TextOrUnits) UnmarshalJSON(b []byte) error {
	if isObject(b) {
		return t.Units.UnmarshalJSON(b)
	}
	return json.Unmarshal(b, &t.Text)
}

// marshalString writes a JSON string without HTML escaping.
func marshalString(s string) ([]byte, error) {
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ValueKind is the form of a dead-key entry's base or result.
type ValueKind int

const (
	ValueText ValueKind = iota
	ValueDeadKey
	ValueUnits
)

// Value is a dead-key entry's base or result: Text, {"deadKey": id} or CodeUnits (SPEC §10).
type Value struct {
	Kind    ValueKind
	Text    string
	DeadKey string
	Units   Units
}

// UTF16 returns a Text or CodeUnits value as units (nil for a dead-key reference).
func (v Value) UTF16() []uint16 {
	switch v.Kind {
	case ValueText:
		return utf16.Encode([]rune(v.Text))
	case ValueUnits:
		return v.Units
	}
	return nil
}

type deadKeyRef struct {
	DeadKey string `json:"deadKey"`
}

// MarshalJSON writes the value's form.
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case ValueDeadKey:
		return json.Marshal(deadKeyRef{v.DeadKey})
	case ValueUnits:
		return v.Units.MarshalJSON()
	}
	return marshalString(v.Text)
}

// UnmarshalJSON reads any of the three forms.
func (v *Value) UnmarshalJSON(b []byte) error {
	if !isObject(b) {
		v.Kind = ValueText
		return json.Unmarshal(b, &v.Text)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	if _, ok := probe["deadKey"]; ok {
		var r deadKeyRef
		if err := strictUnmarshal(b, &r); err != nil {
			return err
		}
		v.Kind, v.DeadKey = ValueDeadKey, r.DeadKey
		return nil
	}
	v.Kind = ValueUnits
	return v.Units.UnmarshalJSON(b)
}

// MarkedItem is one item of a MarkedText: exactly one field is set.
type MarkedItem struct {
	Text     string `json:"-"`
	Marker   string `json:"marker,omitempty"`
	Variable string `json:"variable,omitempty"`
}

// MarkedText is a Text or an array of texts, markers and variables (SPEC §2.3).
type MarkedText struct {
	Items []MarkedItem
	// Array records that the file used the array form for text only.
	Array bool
}

// PlainText returns m as a plain text when it has no markers and variables.
func (m MarkedText) PlainText() (string, bool) {
	var b strings.Builder
	for _, it := range m.Items {
		if it.Marker != "" || it.Variable != "" {
			return "", false
		}
		b.WriteString(it.Text)
	}
	return b.String(), true
}

// TextOf returns the text of m with markers dropped and variables left out.
func (m MarkedText) TextOf() string {
	var b strings.Builder
	for _, it := range m.Items {
		b.WriteString(it.Text)
	}
	return b.String()
}

// NewText returns a MarkedText holding plain text.
func NewText(s string) MarkedText { return MarkedText{Items: []MarkedItem{{Text: s}}} }

// MarshalJSON writes the plain form for text only, unless the file had the array form.
func (m MarkedText) MarshalJSON() ([]byte, error) {
	if s, ok := m.PlainText(); ok && !m.Array {
		return marshalString(s)
	}
	parts := make([]json.RawMessage, len(m.Items))
	for i, it := range m.Items {
		var err error
		switch {
		case it.Marker != "":
			parts[i], err = json.Marshal(map[string]string{"marker": it.Marker})
		case it.Variable != "":
			parts[i], err = json.Marshal(map[string]string{"variable": it.Variable})
		default:
			parts[i], err = marshalString(it.Text)
		}
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(parts)
}

// UnmarshalJSON reads either form.
func (m *MarkedText) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*m = NewText(s)
		return nil
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(b, &parts); err != nil {
		return err
	}
	m.Items, m.Array = nil, true
	for _, p := range parts {
		var it MarkedItem
		if isObject(p) {
			var o struct {
				Marker   string `json:"marker"`
				Variable string `json:"variable"`
			}
			if err := strictUnmarshal(p, &o); err != nil {
				return err
			}
			it.Marker, it.Variable = o.Marker, o.Variable
		} else if err := json.Unmarshal(p, &it.Text); err != nil {
			return err
		}
		m.Items = append(m.Items, it)
	}
	return nil
}

// BindingKind is the form of a binding (SPEC §9.2).
type BindingKind int

const (
	BindText BindingKind = iota
	BindOutput
	BindKey
	BindDeadKey
	BindUnits
)

// Binding is what a position does in a layer.
type Binding struct {
	Kind    BindingKind
	Text    string     // BindText
	Output  MarkedText // BindOutput
	Key     string     // BindKey
	DeadKey string     // BindDeadKey
	Units   Units      // BindUnits
}

// TextBinding returns a Text binding.
func TextBinding(s string) *Binding { return &Binding{Kind: BindText, Text: s} }

// MarshalJSON writes the binding's form.
func (b Binding) MarshalJSON() ([]byte, error) {
	switch b.Kind {
	case BindOutput:
		out, err := b.Output.MarshalJSON()
		if err != nil {
			return nil, err
		}
		return []byte(`{"output":` + string(out) + `}`), nil
	case BindKey:
		return json.Marshal(map[string]string{"key": b.Key})
	case BindDeadKey:
		return json.Marshal(deadKeyRef{b.DeadKey})
	case BindUnits:
		return b.Units.MarshalJSON()
	}
	return marshalString(b.Text)
}

// UnmarshalJSON reads any form.
func (b *Binding) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		b.Kind = BindText
		return json.Unmarshal(data, &b.Text)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if len(probe) != 1 {
		return fmt.Errorf("binding: want one property")
	}
	for k, v := range probe {
		switch k {
		case "output":
			b.Kind = BindOutput
			return b.Output.UnmarshalJSON(v)
		case "key":
			b.Kind = BindKey
			return json.Unmarshal(v, &b.Key)
		case "deadKey":
			b.Kind = BindDeadKey
			return json.Unmarshal(v, &b.DeadKey)
		case "utf16":
			b.Kind = BindUnits
			return b.Units.UnmarshalJSON(data)
		default:
			return fmt.Errorf("binding: unknown property %q", k)
		}
	}
	return nil
}

// CellKind is the form of a Windows extra-row cell.
type CellKind int

const (
	CellNone CellKind = iota
	CellText
	CellUnits
	CellDeadKey
)

// WinCell is a cell of targets.windows.extraRows: Text, CodeUnits, {"deadKey"} or null.
type WinCell struct {
	Kind    CellKind
	Text    string
	Units   Units
	DeadKey string
}

// MarshalJSON writes the cell's form.
func (c WinCell) MarshalJSON() ([]byte, error) {
	switch c.Kind {
	case CellText:
		return marshalString(c.Text)
	case CellUnits:
		return c.Units.MarshalJSON()
	case CellDeadKey:
		return json.Marshal(deadKeyRef{c.DeadKey})
	}
	return []byte("null"), nil
}

// UnmarshalJSON reads any form.
func (c *WinCell) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case string(b) == "null":
		*c = WinCell{}
	case isObject(b):
		var probe map[string]json.RawMessage
		json.Unmarshal(b, &probe)
		if _, ok := probe["deadKey"]; ok {
			var r deadKeyRef
			if err := strictUnmarshal(b, &r); err != nil {
				return err
			}
			*c = WinCell{Kind: CellDeadKey, DeadKey: r.DeadKey}
			return nil
		}
		c.Kind = CellUnits
		return c.Units.UnmarshalJSON(b)
	default:
		c.Kind = CellText
		return json.Unmarshal(b, &c.Text)
	}
	return nil
}

// MarshalJSON writes a PositionId or {"vk": VK}.
func (r TableRow) MarshalJSON() ([]byte, error) {
	if r.VK != "" {
		return json.Marshal(map[string]string{"vk": r.VK})
	}
	return json.Marshal(r.Position)
}

// UnmarshalJSON reads either form.
func (r *TableRow) UnmarshalJSON(b []byte) error {
	if isObject(b) {
		var o struct {
			VK string `json:"vk"`
		}
		if err := strictUnmarshal(b, &o); err != nil {
			return err
		}
		*r = TableRow{VK: o.VK}
		return nil
	}
	return json.Unmarshal(b, &r.Position)
}
