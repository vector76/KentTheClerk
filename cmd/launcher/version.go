package main

import (
	"regexp"
	"strconv"
	"strings"
)

// A version is MAJOR.MINOR.PATCH with an optional pre-release suffix, as in semantic versioning: 0.9.0,
// 0.9.0-dev, 1.2.3-rc.1.
var versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

func validVersion(v string) bool { return versionRE.MatchString(v) }

// compareVersions orders versions as semantic versioning does: by the numbers, then a version with a
// pre-release suffix below the same version without one, so 0.9.0-dev < 0.9.0 < 0.10.0-dev.
func compareVersions(a, b string) int {
	ac, ap, _ := strings.Cut(a, "-")
	bc, bp, _ := strings.Cut(b, "-")
	if c := compareDotted(ac, bc); c != 0 {
		return c
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return compareDotted(ap, bp)
}

// compareDotted compares dot-separated identifiers: numeric ones as numbers and below alphanumeric ones,
// alphanumeric ones as text, and a shorter list below a longer one it is a prefix of.
func compareDotted(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.ParseUint(as[i], 10, 64)
		bn, berr := strconv.ParseUint(bs[i], 10, 64)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}
