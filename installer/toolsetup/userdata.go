package toolsetup

import (
	"fmt"
	"strings"
)

// UserSystem is what removing the starting user's settings needs, in that user's own (unelevated) process
// (docs/installer-cli.md §12.3): the user's environment, and the user's own registry hive and folders.
type UserSystem interface {
	// Getenv is the starting user's environment variable (LOCALAPPDATA, APPDATA).
	Getenv(name string) string
	// UserKeyExists and DeleteUserKeyTree work on HKEY_CURRENT_USER; the path is below it ("Software\YAKBD").
	UserKeyExists(path string) bool
	// UserKeyEmpty reports whether a key has no values and no subkeys.
	UserKeyEmpty(path string) bool
	DeleteUserKeyTree(path string) error
	DirExists(path string) bool
	// RemoveTree deletes a folder and everything in it; RemoveDir an empty folder.
	RemoveTree(path string) error
	RemoveDir(path string) error
}

// resolve turns a declaration into the place it names for this user: the HKCU subkey ("Software\..."), or the
// folder with its variable expanded; "" when the variable is not set.
func resolve(us UserSystem, u UserData) (string, bool) {
	if u.Kind == "key" {
		return u.Path[len(`HKCU\`):], true
	}
	v, rest, _ := strings.Cut(u.Path[1:], `%\`)
	base := strings.TrimRight(us.Getenv(v), `\`)
	if base == "" {
		return "", false
	}
	return base + `\` + rest, true
}

func parent(p string) string { return p[:strings.LastIndex(p, `\`)] }

// HasUserData reports whether the starting user has any of the declared settings of the given components.
func HasUserData(us UserSystem, decl []UserData, comps map[string]bool) bool {
	for _, u := range decl {
		if CheckUserData(u) != nil || !comps[u.Component] {
			continue
		}
		p, ok := resolve(us, u)
		if !ok {
			continue
		}
		if u.Kind == "key" && us.UserKeyExists(p) || u.Kind == "dir" && us.DirExists(p) {
			return true
		}
	}
	return false
}

// RemoveUserData deletes the starting user's declared settings of the given components, and each parent marked
// ParentIfEmpty when it is empty afterwards. Every declaration is checked again (CheckUserData), so a damaged record
// names nothing outside the user's settings.
func RemoveUserData(us UserSystem, decl []UserData, comps map[string]bool, logf func(string, ...any)) error {
	var problems []string
	for _, u := range decl {
		if CheckUserData(u) != nil || !comps[u.Component] {
			continue
		}
		p, ok := resolve(us, u)
		if !ok {
			continue
		}
		switch u.Kind {
		case "key":
			if us.UserKeyExists(p) {
				if err := us.DeleteUserKeyTree(p); err != nil {
					problems = append(problems, fmt.Sprintf(`HKCU\%s: %v`, p, err))
					continue
				}
				logf(`HKCU\%s deleted`, p)
			}
			if u.ParentIfEmpty && us.UserKeyExists(parent(p)) && us.UserKeyEmpty(parent(p)) {
				if us.DeleteUserKeyTree(parent(p)) == nil {
					logf(`HKCU\%s deleted (empty)`, parent(p))
				}
			}
		case "dir":
			if us.DirExists(p) {
				if err := us.RemoveTree(p); err != nil {
					problems = append(problems, fmt.Sprintf("%s: %v", p, err))
					continue
				}
				logf("%s deleted", p)
			}
			if u.ParentIfEmpty && us.DirExists(parent(p)) && us.RemoveDir(parent(p)) == nil {
				logf("%s deleted (empty)", parent(p))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("settings not removed: %s", strings.Join(problems, "; "))
	}
	return nil
}
