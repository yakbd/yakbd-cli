package toolsetup

import (
	"fmt"
	"strings"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
)

// TakeProps takes the installers' own properties out of a command line (docs/installer-cli.md §12.3): ADDLOCAL,
// REMOVE (except REMOVE=ALL, which stays for installer/cmdline: an uninstall), ADDTOPATH and REMOVEUSERDATA. The
// rest goes to cmdline.Parse, which the layout installers share; Action and the switches come from there.
func TakeProps(args []string) (rest []string, req Request, err *cmdline.Error) {
	for _, a := range args {
		name, value, ok := strings.Cut(a, "=")
		if !ok || a == "" || a[0] == '/' || a[0] == '-' {
			rest = append(rest, a)
			continue
		}
		value = strings.Trim(value, `"`)
		switch strings.ToUpper(name) {
		case "ADDLOCAL":
			if strings.EqualFold(value, "ALL") {
				req.AddLocal = nil // every component the installer carries for the machine: its defaults
				continue
			}
			if value == "" {
				req.AddLocal = []string{} // none besides what is installed (the wizard's choice)
				continue
			}
			ids, e := componentList("ADDLOCAL", value)
			if e != nil {
				return nil, req, e
			}
			req.AddLocal = ids
		case "REMOVE":
			if strings.EqualFold(value, "ALL") {
				rest = append(rest, a)
				continue
			}
			ids, e := componentList("REMOVE", value)
			if e != nil {
				return nil, req, e
			}
			req.Remove = append(req.Remove, ids...)
		case "ADDTOPATH", "REMOVEUSERDATA":
			if value != "0" && value != "1" {
				return nil, req, &cmdline.Error{Code: cmdline.InvalidCommandLine, Msg: fmt.Sprintf("%s=%s: the value is 0 or 1", strings.ToUpper(name), value)}
			}
			if strings.EqualFold(name, "ADDTOPATH") {
				on := value == "1"
				req.AddToPath = &on
			} else {
				req.RemoveUserData = value == "1"
			}
		default:
			rest = append(rest, a)
		}
	}
	return rest, req, nil
}

// componentList reads "CLI,Editor" (letter case ignored) into component ids.
func componentList(prop, v string) ([]string, *cmdline.Error) {
	var out []string
	for _, part := range strings.Split(v, ",") {
		id := ""
		for _, c := range ComponentIDs {
			if strings.EqualFold(strings.TrimSpace(part), c) {
				id = c
			}
		}
		if id == "" {
			return nil, &cmdline.Error{Code: cmdline.InvalidCommandLine, Msg: fmt.Sprintf("%s=%s: %q is not a component (CLI, Editor)", prop, v, part)}
		}
		out = append(out, id)
	}
	return out, nil
}

// PropArgs is a request as properties again, for the elevated copy the first process starts: ADDLOCAL, REMOVE and
// ADDTOPATH. REMOVEUSERDATA is never passed on: the starting user's settings are removed by the first process.
func PropArgs(req Request) []string {
	var out []string
	if req.AddLocal != nil {
		if len(req.AddLocal) == 0 {
			out = append(out, "ADDLOCAL=")
		} else {
			out = append(out, "ADDLOCAL="+strings.Join(req.AddLocal, ","))
		}
	}
	if len(req.Remove) > 0 {
		out = append(out, "REMOVE="+strings.Join(req.Remove, ","))
	}
	if req.AddToPath != nil {
		out = append(out, map[bool]string{true: "ADDTOPATH=1", false: "ADDTOPATH=0"}[*req.AddToPath])
	}
	return out
}
