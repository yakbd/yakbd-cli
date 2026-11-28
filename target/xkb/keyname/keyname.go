// Package keyname is the position table of the XKB target: the default XKB key name (xkeyboard-config's evdev
// keycodes) of each position name, with the keycode, the aliases layouts use for the key and the navigation keysym
// of the number-pad keys (kbl-spec targets/xkb-default-keyname.tsv, embedded unchanged; rules keyName and keyType
// of targets/xkb.md).
package keyname

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed xkb-default-keyname.tsv
var table string

// SpecCommit is the kbl-spec commit (branch spec-0.2) the embedded table was copied from.
const SpecCommit = "6972db4f96e6d8a0c694682ac82023f40195bf70"

// Row is one row of the table.
type Row struct {
	Position string
	KeyName  string
	Keycode  int      // X keycode
	Aliases  []string // other names of the key
	Keypad   string   // the navigation keysym of a number-pad key, "" for none
}

// Rows are the table's rows in file order.
var Rows = parse(table)

var byPos, byName = func() (map[string]Row, map[string]Row) {
	p, n := map[string]Row{}, map[string]Row{}
	for _, r := range Rows {
		p[r.Position] = r
		n[r.KeyName] = r
		for _, a := range r.Aliases {
			if _, dup := n[a]; !dup {
				n[a] = r
			}
		}
	}
	return p, n
}()

func parse(s string) []Row {
	var rows []Row
	header := true
	for _, line := range strings.Split(s, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if header {
			header = false
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 6 {
			panic(fmt.Sprintf("keyname: malformed row %q", line))
		}
		code, err := strconv.Atoi(f[2])
		if err != nil {
			panic(fmt.Sprintf("keyname: row %q: %v", line, err))
		}
		r := Row{Position: f[0], KeyName: f[1], Keycode: code}
		if f[3] != "-" {
			r.Aliases = strings.Split(f[3], ",")
		}
		if f[4] != "-" {
			r.Keypad = f[4]
		}
		rows = append(rows, r)
	}
	return rows
}

// ForPosition returns the row of a position.
func ForPosition(pos string) (Row, bool) { r, ok := byPos[pos]; return r, ok }

// ForKeyName returns the position of a key name or alias.
func ForKeyName(name string) (string, bool) { r, ok := byName[name]; return r.Position, ok }

// Canonical returns the key name of an alias, or the name itself.
func Canonical(name string) string {
	if r, ok := byName[name]; ok {
		return r.KeyName
	}
	return name
}
