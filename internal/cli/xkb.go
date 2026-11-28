package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/convert/xkbkbl"
	"github.com/yakbd/yakbd-cli/format/xkb"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// xkbOptions are convert's options of an XKB input or output (-variant, -compose, -include).
type xkbOptions struct {
	variant  string   // the xkb_symbols section; "" for the file's default one
	compose  string   // a Compose file: read with an input, written with an output
	includes []string // folders of the symbols files includes name; empty: the input's folder
	dir      string   // the input's folder
}

// xkbIO are the XKB options of the running command.
var xkbIO xkbOptions

// forcedFormat is the format convert's -from names for its input ("" for by extension).
var forcedFormat struct{ path, name string }

// formatByName returns the format of a name (-from, -to).
func formatByName(name string) *fileFormat {
	for i := range formats {
		if formats[i].name == name {
			return &formats[i]
		}
	}
	return nil
}

// formatNames lists the format names -from and -to take.
func formatNames() string {
	var s []string
	for _, f := range formats {
		s = append(s, f.name)
	}
	return strings.Join(s, ", ")
}

func readXKB(b []byte, name string) (*model.Layout, *loss.Report) {
	f, r := xkb.Read(b, name)
	if f == nil || r.HasErrors() {
		return nil, r
	}
	sec := f.Section(xkbIO.variant)
	if sec == nil {
		r.Errorf("io.variant", loss.Where{File: name}, "the file has no xkb_symbols section %q", xkbIO.variant)
		return nil, r
	}
	dirs := xkbIO.includes
	if len(dirs) == 0 {
		dirs = []string{xkbIO.dir}
	}
	res := func(file string) ([]byte, bool) {
		if filepath.IsAbs(file) || strings.Contains(file, "..") {
			return nil, false
		}
		for _, d := range dirs {
			if b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(file))); err == nil {
				return b, true
			}
		}
		return nil, false
	}
	km, cr := xkb.Compile(f, sec, name, xkb.NewIncludes(res))
	r.Merge(cr)
	o := xkbkbl.Options{}
	if xkbIO.compose != "" {
		cb, err := os.ReadFile(xkbIO.compose)
		if err != nil {
			r.Errorf("io.read", loss.Where{File: xkbIO.compose}, "%v", err)
			return nil, r
		}
		c, crr := xkb.ReadCompose(cb, filepath.Base(xkbIO.compose))
		r.Merge(crr)
		if c == nil {
			return nil, r
		}
		o.Compose, o.ComposeName = c, filepath.Base(xkbIO.compose)
	}
	src := name
	if sec.Name != "" {
		src = name + "(" + sec.Name + ")"
	}
	l, ir := xkbkbl.FromXKB(km, src, o)
	r.Merge(ir)
	checked(l, r)
	return l, r
}
