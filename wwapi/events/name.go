package events

import (
	"fmt"
	"strings"
)

// CoreNamespace is the first segment of every event name the core bus
// emits. Mods cannot emit under it.
const CoreNamespace = "core"

// ModNamespace is the first segment of every mod event name:
// mod.<id>.<rest>.
const ModNamespace = "mod"

// MaxNameLen bounds an event name, and MaxModIDLen a mod id.
const (
	MaxNameLen  = 128
	MaxModIDLen = 64
)

// ValidModID reports whether id can own a namespace: a lowercase letter
// then lowercase letters, digits or underscores, optionally after one
// leading underscore (test fixtures), at most MaxModIDLen bytes, and not
// "core".
func ValidModID(id string) bool {
	if id == "" || len(id) > MaxModIDLen || id == CoreNamespace {
		return false
	}
	s := strings.TrimPrefix(id, "_")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !segByte(s[i]) {
			return false
		}
	}
	return true
}

func segByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

func validSegment(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !segByte(s[i]) {
			return false
		}
	}
	return true
}

// ParseName checks an event name and returns its owner: "core" for
// core.<rest>, or the mod id for mod.<id>.<rest>. Every segment is
// lowercase letters, digits and underscores, not starting with a digit,
// and the name has at least one segment after the owner.
func ParseName(name string) (owner string, err error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty", ErrBadName)
	}
	if len(name) > MaxNameLen {
		return "", fmt.Errorf("%w: %d bytes, the limit is %d", ErrBadName, len(name), MaxNameLen)
	}
	segs := strings.Split(name, ".")
	for _, s := range segs {
		if !validSegment(s) {
			return "", fmt.Errorf("%w: %q has a bad segment %q", ErrBadName, name, s)
		}
	}
	switch segs[0] {
	case CoreNamespace:
		if len(segs) < 2 {
			return "", fmt.Errorf("%w: %q needs a name after core.", ErrBadName, name)
		}
		return CoreNamespace, nil
	case ModNamespace:
		if len(segs) < 3 {
			return "", fmt.Errorf("%w: %q must be mod.<id>.<name>", ErrBadName, name)
		}
		if !ValidModID(segs[1]) {
			return "", fmt.Errorf("%w: %q has a bad mod id %q", ErrBadName, name, segs[1])
		}
		return segs[1], nil
	default:
		return "", fmt.Errorf("%w: %q must start with core. or mod.<id>.", ErrBadName, name)
	}
}
