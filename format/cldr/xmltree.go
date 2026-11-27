package cldr

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
)

// node is one XML element with its attributes in document order, its element children, and the line it starts
// on. A <special> element keeps its complete source text in raw.
type node struct {
	name     string
	attrs    []xml.Attr
	children []*node
	line     int
	raw      string
}

func (n *node) attr(name string) (string, bool) {
	for _, a := range n.attrs {
		if a.Name.Local == name && a.Name.Space == "" {
			return a.Value, true
		}
	}
	return "", false
}

func (n *node) get(name string) string {
	v, _ := n.attr(name)
	return v
}

func (n *node) kids(name string) []*node {
	var out []*node
	for _, c := range n.children {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

func (n *node) kid(name string) *node {
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

// specials returns the raw text of the <special> children.
func (n *node) specials() []string {
	var out []string
	for _, c := range n.children {
		if c.name == "special" {
			out = append(out, c.raw)
		}
	}
	return out
}

// lineAt counts the lines up to a byte offset.
func lineAt(data []byte, off int64) int {
	if off > int64(len(data)) {
		off = int64(len(data))
	}
	return bytes.Count(data[:off], []byte("\n")) + 1
}

// parseTree parses an XML document into a tree. Character data outside <special> is ignored: CLDR keyboard files
// carry all their data in attributes.
func parseTree(data []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	var stack []*node
	var root *node
	for {
		start := d.InputOffset()
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: t.Attr, line: lineAt(data, start)}
			if t.Name.Local == "special" {
				if len(stack) == 0 {
					return nil, fmt.Errorf("line %d: <special> as the root element", n.line)
				}
				if err := d.Skip(); err != nil {
					return nil, err
				}
				n.raw = string(data[start:d.InputOffset()])
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
				continue
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("line %d: a second root element", n.line)
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no root element")
	}
	return root, nil
}
