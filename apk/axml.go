package apk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// Android binary XML: the compiled form of AndroidManifest.xml and of res/xml files. A file is one chunk
// (RES_XML_TYPE) holding a string pool, a resource map (the attribute resource ids of the first pool strings) and
// one chunk per node: start and end of a namespace, start and end of an element, character data. Layouts from
// AOSP's ResourceTypes.h (Apache-2.0), read for facts only.

// Chunk types.
const (
	chunkStringPool   = 0x0001
	chunkTable        = 0x0002
	chunkXML          = 0x0003
	chunkXMLStartNS   = 0x0100
	chunkXMLEndNS     = 0x0101
	chunkXMLStart     = 0x0102
	chunkXMLEnd       = 0x0103
	chunkXMLCData     = 0x0104
	chunkXMLResMap    = 0x0180
	chunkTablePackage = 0x0200
	chunkTableType    = 0x0201
	chunkTableSpec    = 0x0202
)

// Value types of Res_value.
const (
	TypeNull      = 0x00
	TypeReference = 0x01
	TypeAttribute = 0x02
	TypeString    = 0x03
	TypeFloat     = 0x04
	TypeDimension = 0x05
	TypeFraction  = 0x06
	TypeIntDec    = 0x10
	TypeIntHex    = 0x11
	TypeBoolean   = 0x12
)

// AndroidNS is the namespace of the platform's attributes.
const AndroidNS = "http://schemas.android.com/apk/res/android"

// noIndex is a ResStringPool_ref without a string.
const noIndex = 0xffffffff

// Value is a typed value (Res_value). For TypeString, Str is the text and Data is not used.
type Value struct {
	Type uint8
	Data uint32
	Str  string
}

// String returns a string value.
func String(s string) Value { return Value{Type: TypeString, Str: s} }

// Int returns a decimal integer value.
func Int(n int32) Value { return Value{Type: TypeIntDec, Data: uint32(n)} }

// Bool returns a boolean value (true is all bits set, as aapt writes it).
func Bool(b bool) Value {
	if b {
		return Value{Type: TypeBoolean, Data: 0xffffffff}
	}
	return Value{Type: TypeBoolean}
}

// Ref returns a reference to the resource id.
func Ref(id uint32) Value { return Value{Type: TypeReference, Data: id} }

// Attr is one attribute of an element.
type Attr struct {
	NS, Name string
	// ResID is the attribute's resource id (0x0101xxxx for the platform's android: attributes); 0 for none.
	ResID uint32
	// Raw is the attribute's text as written, kept for values that are not strings (nil: none). A string value's
	// raw text is always its Str.
	Raw   *string
	Value Value
}

// Namespace is one namespace declaration (prefix and URI).
type Namespace struct {
	Prefix, URI string
}

// Node is an element, or character data when IsText is set.
type Node struct {
	// Namespaces are declared just before this element and end just after it.
	Namespaces []Namespace
	NS, Name   string
	Attrs      []*Attr
	Children   []*Node
	Line       uint32
	IsText     bool
	Text       string
	TextValue  Value
}

// Attr returns the attribute ns:name of n, or nil.
func (n *Node) Attr(ns, name string) *Attr {
	for _, a := range n.Attrs {
		if a.NS == ns && a.Name == name {
			return a
		}
	}
	return nil
}

// SetAttr sets the attribute ns:name (with resID) to v, adding it when n has none.
func (n *Node) SetAttr(ns, name string, resID uint32, v Value) {
	if a := n.Attr(ns, name); a != nil {
		a.Value, a.Raw = v, nil
		if resID != 0 {
			a.ResID = resID
		}
		return
	}
	n.Attrs = append(n.Attrs, &Attr{NS: ns, Name: name, ResID: resID, Value: v})
}

// Elements returns the child elements of n called name.
func (n *Node) Elements(name string) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if !c.IsText && c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// Walk calls f for n and every element below it, depth first.
func (n *Node) Walk(f func(*Node)) {
	if n.IsText {
		return
	}
	f(n)
	for _, c := range n.Children {
		c.Walk(f)
	}
}

// Limits of what ParseXML accepts.
const (
	maxXMLDepth = 256
	maxXMLNodes = 1 << 20
)

// ParseXML reads an Android binary XML file. It reads string pools in UTF-8 and UTF-16 and refuses anything it
// would not write back with the same meaning: unknown node chunks, unbalanced elements or namespaces.
func ParseXML(b []byte) (*Node, error) {
	if len(b) < 8 {
		return nil, errors.New("apk: binary XML: too short")
	}
	typ, hsize, size := le16(b), int(le16(b[2:])), int(le32(b[4:]))
	if typ != chunkXML || hsize < 8 || size > len(b) || size < hsize {
		return nil, errors.New("apk: binary XML: not an XML chunk")
	}
	b = b[:size]
	var pool []string
	var resMap []uint32
	var root *Node
	var stack []*Node
	var pendingNS []Namespace
	var openNS [][]Namespace // namespaces of each open element, closed after its end
	var closing []Namespace  // namespaces still to end after the last closed element
	nodes := 0
	str := func(i uint32) (string, error) {
		if i == noIndex {
			return "", nil
		}
		if int(i) >= len(pool) {
			return "", fmt.Errorf("apk: binary XML: string %d of %d", i, len(pool))
		}
		return pool[i], nil
	}
	for off := hsize; off < size; {
		if size-off < 8 {
			return nil, errors.New("apk: binary XML: truncated chunk")
		}
		ct, ch, cs := le16(b[off:]), int(le16(b[off+2:])), int(le32(b[off+4:]))
		if cs < 8 || ch < 8 || ch > cs || cs > size-off || cs%4 != 0 {
			return nil, fmt.Errorf("apk: binary XML: bad chunk at %d", off)
		}
		c := b[off : off+cs]
		off += cs
		switch ct {
		case chunkStringPool:
			if pool != nil {
				return nil, errors.New("apk: binary XML: two string pools")
			}
			p, err := parsePool(c)
			if err != nil {
				return nil, err
			}
			pool = p
			continue
		case chunkXMLResMap:
			if resMap != nil {
				return nil, errors.New("apk: binary XML: two resource maps")
			}
			resMap = []uint32{}
			for i := ch; i+4 <= cs; i += 4 {
				resMap = append(resMap, le32(c[i:]))
			}
			continue
		}
		if ct < chunkXMLStartNS || ct > chunkXMLCData {
			return nil, fmt.Errorf("apk: binary XML: unknown chunk type 0x%04x", ct)
		}
		if pool == nil {
			return nil, errors.New("apk: binary XML: node before the string pool")
		}
		if ch != 16 {
			return nil, errors.New("apk: binary XML: node header size")
		}
		if nodes++; nodes > maxXMLNodes {
			return nil, errors.New("apk: binary XML: too many nodes")
		}
		line := le32(c[8:])
		ext := c[16:]
		switch ct {
		case chunkXMLStartNS, chunkXMLEndNS:
			if len(ext) < 8 {
				return nil, errors.New("apk: binary XML: namespace too short")
			}
			p, err := str(le32(ext))
			if err != nil {
				return nil, err
			}
			u, err := str(le32(ext[4:]))
			if err != nil {
				return nil, err
			}
			ns := Namespace{Prefix: p, URI: u}
			if ct == chunkXMLStartNS {
				if len(closing) > 0 {
					return nil, errors.New("apk: binary XML: namespace starts before the previous one ends")
				}
				pendingNS = append(pendingNS, ns)
				continue
			}
			if len(closing) == 0 || closing[len(closing)-1] != ns {
				return nil, errors.New("apk: binary XML: namespace ends out of order")
			}
			closing = closing[:len(closing)-1]
		case chunkXMLStart:
			if len(closing) > 0 {
				return nil, errors.New("apk: binary XML: element starts before a namespace ends")
			}
			if len(ext) < 20 {
				return nil, errors.New("apk: binary XML: element too short")
			}
			n := &Node{Line: line, Namespaces: pendingNS}
			pendingNS = nil
			var err error
			if n.NS, err = str(le32(ext)); err != nil {
				return nil, err
			}
			if n.Name, err = str(le32(ext[4:])); err != nil {
				return nil, err
			}
			astart, asize, acount := int(le16(ext[8:])), int(le16(ext[10:])), int(le16(ext[12:]))
			if asize < 20 || astart < 20 || astart+asize*acount > len(ext) {
				return nil, errors.New("apk: binary XML: attributes out of the element")
			}
			for i := 0; i < acount; i++ {
				ab := ext[astart+i*asize:]
				a := &Attr{}
				if a.NS, err = str(le32(ab)); err != nil {
					return nil, err
				}
				ni := le32(ab[4:])
				if a.Name, err = str(ni); err != nil {
					return nil, err
				}
				if int(ni) < len(resMap) {
					a.ResID = resMap[ni]
				}
				if raw := le32(ab[8:]); raw != noIndex {
					s, err := str(raw)
					if err != nil {
						return nil, err
					}
					a.Raw = &s
				}
				a.Value = Value{Type: ab[15], Data: le32(ab[16:])}
				if a.Value.Type == TypeString {
					if a.Value.Str, err = str(a.Value.Data); err != nil {
						return nil, err
					}
					a.Value.Data, a.Raw = 0, nil
				}
				n.Attrs = append(n.Attrs, a)
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("apk: binary XML: two root elements")
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			}
			if len(stack) >= maxXMLDepth {
				return nil, errors.New("apk: binary XML: too deep")
			}
			stack = append(stack, n)
			openNS = append(openNS, n.Namespaces)
		case chunkXMLEnd:
			if len(closing) > 0 {
				return nil, errors.New("apk: binary XML: element ends before a namespace ends")
			}
			if len(ext) < 8 || len(stack) == 0 {
				return nil, errors.New("apk: binary XML: unbalanced element end")
			}
			ns, err := str(le32(ext))
			if err != nil {
				return nil, err
			}
			name, err := str(le32(ext[4:]))
			if err != nil {
				return nil, err
			}
			top := stack[len(stack)-1]
			if top.NS != ns || top.Name != name {
				return nil, fmt.Errorf("apk: binary XML: </%s> closes <%s>", name, top.Name)
			}
			stack = stack[:len(stack)-1]
			closing = append([]Namespace(nil), openNS[len(openNS)-1]...)
			openNS = openNS[:len(openNS)-1]
		case chunkXMLCData:
			if len(closing) > 0 || len(pendingNS) > 0 {
				return nil, errors.New("apk: binary XML: text between namespace declarations")
			}
			if len(ext) < 12 || len(stack) == 0 {
				return nil, errors.New("apk: binary XML: text outside an element")
			}
			t := &Node{IsText: true, Line: line}
			var err error
			if t.Text, err = str(le32(ext)); err != nil {
				return nil, err
			}
			t.TextValue = Value{Type: ext[7], Data: le32(ext[8:])}
			if t.TextValue.Type == TypeString {
				if t.TextValue.Str, err = str(t.TextValue.Data); err != nil {
					return nil, err
				}
				t.TextValue.Data = 0
			}
			p := stack[len(stack)-1]
			p.Children = append(p.Children, t)
		}
	}
	if root == nil || len(stack) > 0 || len(closing) > 0 || len(pendingNS) > 0 {
		return nil, errors.New("apk: binary XML: unbalanced document")
	}
	return root, nil
}

// parsePool reads a string pool chunk (UTF-8 or UTF-16); styles are skipped.
func parsePool(c []byte) ([]string, error) {
	if len(c) < 28 || le16(c[2:]) < 28 {
		return nil, errors.New("apk: string pool too short")
	}
	count, flags, start := int(le32(c[8:])), le32(c[16:]), int(le32(c[20:]))
	styles := int(le32(c[12:]))
	hs := int(le16(c[2:]))
	if count < 0 || styles < 0 || hs+4*(count+styles) > len(c) || start > len(c) {
		return nil, errors.New("apk: string pool out of range")
	}
	utf8Pool := flags&(1<<8) != 0
	out := make([]string, count)
	for i := 0; i < count; i++ {
		o := start + int(le32(c[hs+4*i:]))
		if o < start || o >= len(c) {
			return nil, errors.New("apk: string out of the pool")
		}
		s, err := poolString(c[o:], utf8Pool)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}

// poolString decodes one string at the start of b.
func poolString(b []byte, utf8Pool bool) (string, error) {
	bad := errors.New("apk: string pool entry out of range")
	if utf8Pool {
		// UTF-16 length, then UTF-8 length, each in one byte or two (high bit set), then the bytes and a NUL.
		n8len := func(b []byte) (int, int, bool) {
			if len(b) < 1 {
				return 0, 0, false
			}
			if b[0]&0x80 == 0 {
				return int(b[0]), 1, true
			}
			if len(b) < 2 {
				return 0, 0, false
			}
			return int(b[0]&0x7f)<<8 | int(b[1]), 2, true
		}
		_, k, ok := n8len(b)
		if !ok {
			return "", bad
		}
		n, k2, ok := n8len(b[k:])
		if !ok || k+k2+n > len(b) {
			return "", bad
		}
		s := b[k+k2 : k+k2+n]
		if !utf8.Valid(s) {
			return "", errors.New("apk: string pool: invalid UTF-8")
		}
		return string(s), nil
	}
	if len(b) < 2 {
		return "", bad
	}
	n, k := int(le16(b)), 2
	if n&0x8000 != 0 {
		if len(b) < 4 {
			return "", bad
		}
		n, k = (n&0x7fff)<<16|int(le16(b[2:])), 4
	}
	if k+2*n > len(b) {
		return "", bad
	}
	u := make([]uint16, n)
	for i := range u {
		u[i] = le16(b[k+2*i:])
	}
	return string(utf16.Decode(u)), nil
}

// EncodeXML writes root as Android binary XML: a UTF-16 string pool whose first strings are the attribute names
// with resource ids (in the order of the resource map, ascending id), then every other string in order of first
// use; the attributes of each element sorted by resource id (those without one last, in their order), as the
// platform's attribute lookup expects.
func EncodeXML(root *Node) ([]byte, error) {
	if root == nil || root.IsText {
		return nil, errors.New("apk: binary XML: no root element")
	}
	// Attribute names with a resource id get their own slots (an attribute without an id must never share one:
	// the resource map would give it that id).
	type idName struct {
		id   uint32
		name string
	}
	idSlots := map[idName]bool{}
	root.Walk(func(n *Node) {
		for _, a := range n.Attrs {
			if a.ResID != 0 {
				idSlots[idName{a.ResID, a.Name}] = true
			}
		}
	})
	var ids []idName
	for k := range idSlots {
		ids = append(ids, k)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].id != ids[j].id {
			return ids[i].id < ids[j].id
		}
		return ids[i].name < ids[j].name
	})
	var pool []string
	slot := map[idName]uint32{}
	for _, k := range ids {
		slot[k] = uint32(len(pool))
		pool = append(pool, k.name)
	}
	index := map[string]uint32{}
	intern := func(s string) uint32 {
		if i, ok := index[s]; ok {
			return i
		}
		i := uint32(len(pool))
		index[s] = i
		pool = append(pool, s)
		return i
	}
	optional := func(s string) uint32 {
		if s == "" {
			return noIndex
		}
		return intern(s)
	}
	var body []byte
	node := func(typ uint16, line uint32, ext []byte) {
		h := make([]byte, 16, 16+len(ext))
		put16(h, typ)
		put16(h[2:], 16)
		put32(h[4:], uint32(16+len(ext)))
		put32(h[8:], line)
		put32(h[12:], noIndex)
		body = append(body, append(h, ext...)...)
	}
	nsExt := func(ns Namespace) []byte {
		e := make([]byte, 8)
		put32(e, optional(ns.Prefix))
		put32(e[4:], intern(ns.URI))
		return e
	}
	var emit func(n *Node, depth int) error
	emit = func(n *Node, depth int) error {
		if depth > maxXMLDepth {
			return errors.New("apk: binary XML: too deep")
		}
		if n.IsText {
			e := make([]byte, 12)
			put32(e, intern(n.Text))
			put16(e[4:], 8)
			v := n.TextValue
			if v.Type == TypeString {
				v.Data = intern(v.Str)
			}
			e[7] = v.Type
			put32(e[8:], v.Data)
			node(chunkXMLCData, n.Line, e)
			return nil
		}
		if n.Name == "" {
			return errors.New("apk: binary XML: element without a name")
		}
		for _, ns := range n.Namespaces {
			node(chunkXMLStartNS, n.Line, nsExt(ns))
		}
		attrs := append([]*Attr(nil), n.Attrs...)
		sort.SliceStable(attrs, func(i, j int) bool {
			a, b := attrs[i].ResID, attrs[j].ResID
			if (a == 0) != (b == 0) {
				return a != 0
			}
			return a < b
		})
		if len(attrs) > 0xffff {
			return errors.New("apk: binary XML: too many attributes")
		}
		e := make([]byte, 20+20*len(attrs))
		put32(e, optional(n.NS))
		put32(e[4:], intern(n.Name))
		put16(e[8:], 20)
		put16(e[10:], 20)
		put16(e[12:], uint16(len(attrs)))
		for i, a := range attrs {
			ab := e[20+20*i:]
			put32(ab, optional(a.NS))
			if a.ResID != 0 {
				put32(ab[4:], slot[idName{a.ResID, a.Name}])
			} else {
				put32(ab[4:], intern(a.Name))
			}
			v := a.Value
			raw := uint32(noIndex)
			if v.Type == TypeString {
				v.Data = intern(v.Str)
				raw = v.Data
			} else if a.Raw != nil {
				raw = intern(*a.Raw)
			}
			put32(ab[8:], raw)
			put16(ab[12:], 8)
			ab[15] = v.Type
			put32(ab[16:], v.Data)
			if a.NS == "" {
				switch a.Name {
				case "id":
					put16(e[14:], uint16(i+1))
				case "class":
					put16(e[16:], uint16(i+1))
				case "style":
					put16(e[18:], uint16(i+1))
				}
			}
		}
		node(chunkXMLStart, n.Line, e)
		for _, c := range n.Children {
			if err := emit(c, depth+1); err != nil {
				return err
			}
		}
		end := make([]byte, 8)
		put32(end, optional(n.NS))
		put32(end[4:], intern(n.Name))
		node(chunkXMLEnd, n.Line, end)
		for i := len(n.Namespaces) - 1; i >= 0; i-- {
			node(chunkXMLEndNS, n.Line, nsExt(n.Namespaces[i]))
		}
		return nil
	}
	if err := emit(root, 0); err != nil {
		return nil, err
	}
	sp := encodePool(pool, false)
	rm := make([]byte, 8+4*len(ids))
	put16(rm, chunkXMLResMap)
	put16(rm[2:], 8)
	put32(rm[4:], uint32(len(rm)))
	for i, k := range ids {
		put32(rm[8+4*i:], k.id)
	}
	out := make([]byte, 8, 8+len(sp)+len(rm)+len(body))
	put16(out, chunkXML)
	put16(out[2:], 8)
	out = append(out, sp...)
	if len(ids) > 0 {
		out = append(out, rm...)
	}
	out = append(out, body...)
	put32(out[4:], uint32(len(out)))
	return out, nil
}

// encodePool writes a string pool chunk without styles, in UTF-16 or UTF-8.
func encodePool(pool []string, utf8Pool bool) []byte {
	var data []byte
	offs := make([]uint32, len(pool))
	for i, s := range pool {
		offs[i] = uint32(len(data))
		if utf8Pool {
			n16 := len(utf16.Encode([]rune(s)))
			data = appendLen8(data, n16)
			data = appendLen8(data, len(s))
			data = append(data, s...)
			data = append(data, 0)
			continue
		}
		u := utf16.Encode([]rune(s))
		if len(u) > 0x7fff {
			data = binary.LittleEndian.AppendUint16(data, uint16(0x8000|len(u)>>16))
		}
		data = binary.LittleEndian.AppendUint16(data, uint16(len(u)))
		for _, x := range u {
			data = binary.LittleEndian.AppendUint16(data, x)
		}
		data = append(data, 0, 0)
	}
	for len(data)%4 != 0 {
		data = append(data, 0)
	}
	h := make([]byte, 28+4*len(pool))
	put16(h, chunkStringPool)
	put16(h[2:], 28)
	put32(h[4:], uint32(len(h)+len(data)))
	put32(h[8:], uint32(len(pool)))
	if utf8Pool {
		put32(h[16:], 1<<8)
	}
	if len(pool) > 0 {
		put32(h[20:], uint32(len(h)))
	}
	for i, o := range offs {
		put32(h[28+4*i:], o)
	}
	return append(h, data...)
}

// appendLen8 appends a length of a UTF-8 pool: one byte below 0x80, else two with the high bit set.
func appendLen8(b []byte, n int) []byte {
	if n < 0x80 {
		return append(b, byte(n))
	}
	return append(b, byte(0x80|n>>8), byte(n))
}

func le16(b []byte) uint16                { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32                { return binary.LittleEndian.Uint32(b) }
func put16(b []byte, v uint16)            { binary.LittleEndian.PutUint16(b, v) }
func put32(b []byte, v uint32)            { binary.LittleEndian.PutUint32(b, v) }
func put64(b []byte, v uint64)            { binary.LittleEndian.PutUint64(b, v) }
func appendU32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
