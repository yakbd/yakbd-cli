package toolsetup

import (
	"regexp"
	"strconv"
	"strings"
)

// semverRE is a semantic version without build metadata (https://semver.org, 2.0.0).
var semverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func versionRE(v string) bool { return semverRE.MatchString(v) }

// CompareVersions orders two semantic versions by semver precedence (semver.org §11): the three numbers, then a
// version with a pre-release below the same version without, pre-release identifiers compared one by one (numbers
// numerically and below names, names in ASCII order, fewer identifiers below more). 0.1.0-preview.2 <
// 0.1.0-preview.3 < 0.1.0. Unlike installer/cmdline's comparison for the layout installers, a pre-release counts:
// the installers never put an older CLI or editor over a newer one.
func CompareVersions(a, b string) int {
	ca, pa, _ := strings.Cut(a, "-")
	cb, pb, _ := strings.Cut(b, "-")
	na, nb := strings.Split(ca, "."), strings.Split(cb, ".")
	for i := 0; i < 3; i++ {
		if c := cmpNum(field(na, i), field(nb, i)); c != 0 {
			return c
		}
	}
	switch {
	case pa == "" && pb == "":
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	}
	ia, ib := strings.Split(pa, "."), strings.Split(pb, ".")
	for i := 0; i < len(ia) && i < len(ib); i++ {
		x, xerr := strconv.ParseUint(ia[i], 10, 64)
		y, yerr := strconv.ParseUint(ib[i], 10, 64)
		switch {
		case xerr == nil && yerr == nil:
			if c := cmpNum(x, y); c != 0 {
				return c
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(ia[i], ib[i]); c != 0 {
				return c
			}
		}
	}
	return cmpNum(uint64(len(ia)), uint64(len(ib)))
}

func field(f []string, i int) uint64 {
	if i >= len(f) {
		return 0
	}
	n, _ := strconv.ParseUint(f[i], 10, 64)
	return n
}

func cmpNum(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
