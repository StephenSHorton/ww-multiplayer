// Package semver parses semantic versions and version ranges for wwapi
// mod manifests.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
//
// Versions follow Semantic Versioning 2.0.0 (MAJOR.MINOR.PATCH, an
// optional -prerelease and an optional +build, which is ignored when
// comparing). Each numeric part is at most [MaxPart].
//
// Ranges use the npm syntax that manifests need, see [ParseRange].
package semver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MaxPart bounds MAJOR, MINOR, PATCH and numeric prerelease identifiers,
// so a range's upper bound (a part plus one) can never overflow.
const MaxPart = 1<<31 - 1

// MaxLen bounds the text of a version or a range.
const MaxLen = 256

var (
	ErrVersion = errors.New("semver: bad version")
	ErrRange   = errors.New("semver: bad range")
)

// Version is a parsed semantic version. The zero value is 0.0.0.
type Version struct {
	Major, Minor, Patch int
	Pre                 []string // prerelease identifiers, nil when none
	Build               string   // build metadata, without the '+'
}

// Parse parses a full semantic version such as "1.2.3", "0.1.0-rc.1" or
// "1.0.0+abc". A leading "v" is not accepted.
func Parse(s string) (Version, error) {
	if len(s) > MaxLen {
		return Version{}, fmt.Errorf("%w: %d bytes, the limit is %d", ErrVersion, len(s), MaxLen)
	}
	v, n, err := parseParts(s)
	if err != nil {
		return Version{}, err
	}
	if n != 3 {
		return Version{}, fmt.Errorf("%w: %q needs MAJOR.MINOR.PATCH", ErrVersion, s)
	}
	return v, nil
}

// MustParse is Parse that panics on an error. Use it only for constants.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// parseParts parses MAJOR[.MINOR[.PATCH]][-pre][+build] and returns how
// many numeric parts were present. A prerelease or build needs all three.
func parseParts(s string) (Version, int, error) {
	var v Version
	core := s
	if i := strings.IndexByte(core, '+'); i >= 0 {
		v.Build = core[i+1:]
		core = core[:i]
		if !validIdents(v.Build, false) {
			return Version{}, 0, fmt.Errorf("%w: %q has bad build metadata", ErrVersion, s)
		}
	}
	hasPre := false
	if i := strings.IndexByte(core, '-'); i >= 0 {
		pre := core[i+1:]
		core = core[:i]
		if !validIdents(pre, true) {
			return Version{}, 0, fmt.Errorf("%w: %q has a bad prerelease", ErrVersion, s)
		}
		v.Pre = strings.Split(pre, ".")
		hasPre = true
	}
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return Version{}, 0, fmt.Errorf("%w: %q has more than three parts", ErrVersion, s)
	}
	nums := [3]*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		n, err := parseNum(p)
		if err != nil {
			return Version{}, 0, fmt.Errorf("%w: %q: %v", ErrVersion, s, err)
		}
		*nums[i] = n
	}
	if (hasPre || v.Build != "") && len(parts) != 3 {
		return Version{}, 0, fmt.Errorf("%w: %q: a prerelease or build needs MAJOR.MINOR.PATCH", ErrVersion, s)
	}
	return v, len(parts), nil
}

func parseNum(p string) (int, error) {
	if p == "" {
		return 0, errors.New("empty part")
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return 0, fmt.Errorf("part %q is not a number", p)
		}
	}
	if len(p) > 1 && p[0] == '0' {
		return 0, fmt.Errorf("part %q has a leading zero", p)
	}
	n, err := strconv.ParseUint(p, 10, 32)
	if err != nil || n > MaxPart {
		return 0, fmt.Errorf("part %q is larger than %d", p, MaxPart)
	}
	return int(n), nil
}

// validIdents checks dot-separated [0-9A-Za-z-]+ identifiers; in a
// prerelease, numeric identifiers may not have leading zeros and are
// bounded by MaxPart.
func validIdents(s string, pre bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for i := 0; i < len(id); i++ {
			c := id[i]
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				numeric = false
			default:
				return false
			}
		}
		if pre && numeric {
			if _, err := parseNum(id); err != nil {
				return false
			}
		}
	}
	return true
}

// String formats v in canonical form.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// Compare returns -1, 0 or +1 by semver precedence. Build metadata is
// ignored.
func (v Version) Compare(w Version) int {
	for _, d := range [3]int{v.Major - w.Major, v.Minor - w.Minor, v.Patch - w.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	switch {
	case len(v.Pre) == 0 && len(w.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1 // a release is above its prereleases
	case len(w.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(w.Pre); i++ {
		if c := compareIdent(v.Pre[i], w.Pre[i]); c != 0 {
			return c
		}
	}
	return sign(len(v.Pre) - len(w.Pre))
}

func compareIdent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return sign(an - bn)
	case aerr == nil:
		return -1 // numeric identifiers sort below alphanumeric ones
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func sign(d int) int {
	switch {
	case d < 0:
		return -1
	case d > 0:
		return 1
	}
	return 0
}

func (v Version) sameTuple(w Version) bool {
	return v.Major == w.Major && v.Minor == w.Minor && v.Patch == w.Patch
}

// MarshalText makes a Version encode as its string in JSON.
func (v Version) MarshalText() ([]byte, error) { return []byte(v.String()), nil }

// UnmarshalText parses a Version from JSON text.
func (v *Version) UnmarshalText(b []byte) error {
	p, err := Parse(string(b))
	if err != nil {
		return err
	}
	*v = p
	return nil
}
