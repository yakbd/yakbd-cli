package cli

import (
	"flag"

	"github.com/yakbd/yakbd-cli/convert/dllkbl"
)

// importOptions are the import options of the running command (convert's -langid and -keep-keyboard-type).
var importOptions dllkbl.Options

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
