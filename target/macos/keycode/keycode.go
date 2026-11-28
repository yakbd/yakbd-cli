// Package keycode is the position table of the macOS target: the default virtual key code of each position name,
// with the standard output of keys such as Return and the arrows (kbl-spec targets/macos-default-keycode.tsv,
// embedded unchanged; rules keyCode, isoKeys and standardKeys of targets/macos.md).
package keycode

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

//go:embed macos-default-keycode.tsv
var table string

// SpecCommit is the kbl-spec commit (branch spec-0.2) the embedded table was copied from.
const SpecCommit = "079a3da5fbeb09ac0e7ba33af9756d412dfe64ee"

// Row is one row of the table.
type Row struct {
	Position string
	Code     int
	// Output is the standard output (rule standardKeys), "" for none.
	Output string
}

// Rows are the table's rows in file order.
var Rows = parse(table)

var (
	byPosition = map[string]Row{}
	byCode     = map[int]Row{}
)

func init() {
	for _, r := range Rows {
		byPosition[r.Position] = r
		if _, dup := byCode[r.Code]; !dup {
			byCode[r.Code] = r
		}
	}
}

func parse(s string) []Row {
	var rows []Row
	header := true
	for _, line := range strings.Split(s, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if header {
			header = false
			continue
		}
		if len(f) < 4 {
			panic(fmt.Sprintf("keycode: malformed row %q", line))
		}
		code, err := strconv.Atoi(f[1])
		if err != nil {
			panic(fmt.Sprintf("keycode: row %q: %v", line, err))
		}
		r := Row{Position: f[0], Code: code}
		if f[3] != "-" {
			var u []uint16
			for _, h := range strings.Fields(f[3]) {
				v, err := strconv.ParseUint(h, 16, 16)
				if err != nil {
					panic(fmt.Sprintf("keycode: row %q: %v", line, err))
				}
				u = append(u, uint16(v))
			}
			r.Output = string(utf16.Decode(u))
		}
		rows = append(rows, r)
	}
	return rows
}

// ForPosition returns the row of a position name.
func ForPosition(p string) (Row, bool) {
	r, ok := byPosition[p]
	return r, ok
}

// ForCode returns the row of a key code.
func ForCode(c int) (Row, bool) {
	r, ok := byCode[c]
	return r, ok
}
