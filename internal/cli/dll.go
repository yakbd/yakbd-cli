package cli

import (
	"flag"

	"github.com/yakbd/yakbd-cli/convert/dllkbl"
	"github.com/yakbd/yakbd-cli/loss"
	"github.com/yakbd/yakbd-cli/model"
)

// importOptions are the import options of the running command (convert's -langid and -keep-keyboard-type).
var importOptions dllkbl.Options

// readDLL imports a Windows keyboard-layout DLL (docs/dll-import.md).
func readDLL(b []byte, name string, r *loss.Report) *model.Layout {
	l, dr := dllkbl.FromDLL(b, name, importOptions)
	r.Merge(dr)
	if l == nil || dr.HasErrors() {
		return nil
	}
	return l
}

// parseInterleaved parses flags that may come before, between or after the positional arguments
// ("yakbd convert in.dll -o out.kbl.json") and returns the positional ones.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
