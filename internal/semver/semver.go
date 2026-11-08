// Package semver compares versions like 1.5.1 and 0.2.0-beta.1.
package semver

import (
	"regexp"
	"strconv"
	"strings"
)

var re = regexp.MustCompile(`^(\d{1,6})\.(\d{1,6})\.(\d{1,6})(?:-([0-9A-Za-z.-]{1,32}))?$`)

// Valid reports whether v is MAJOR.MINOR.PATCH with an optional
// pre-release.
func Valid(v string) bool { return re.MatchString(v) }

// Compare returns -1, 0 or 1. A pre-release sorts before its release.
// Invalid versions sort before valid ones.
func Compare(a, b string) int {
	ma, mb := re.FindStringSubmatch(a), re.FindStringSubmatch(b)
	switch {
	case ma == nil && mb == nil:
		return strings.Compare(a, b)
	case ma == nil:
		return -1
	case mb == nil:
		return 1
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			return cmp(x, y)
		}
	}
	pa, pb := ma[4], mb[4]
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	}
	return comparePre(pa, pb)
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, xerr := strconv.Atoi(as[i])
		y, yerr := strconv.Atoi(bs[i])
		switch {
		case xerr == nil && yerr == nil:
			if x != y {
				return cmp(x, y)
			}
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmp(len(as), len(bs))
}

func cmp(x, y int) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}
