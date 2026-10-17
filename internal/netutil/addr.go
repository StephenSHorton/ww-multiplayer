// Package netutil holds small address-parsing helpers shared by the CLI
// entry points (host/join/server) that don't belong in mods/multiplayer/net
// (which only deals with already-resolved host:port strings).
package netutil

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
)

// NormalizeAddr ensures addr carries an explicit port, appending
// defaultPort when the caller omitted one. It's IPv6-aware: bracketed
// literals ("[::1]", "[::1]:1234") and bare literals ("::1") are all
// handled, in addition to plain hostnames and IPv4 addresses.
//
// Addresses that already specify a port (in any of the accepted forms)
// are returned unchanged (modulo normalization through
// net.JoinHostPort, e.g. re-bracketing an IPv6 host).
//
// A valid join code ("WW-K7Q2-9XHM", see joincode.go) becomes the
// "ip:port" it holds, whatever defaultPort is: the code carries its port.
func NormalizeAddr(addr string, defaultPort int) string {
	addr = strings.TrimSpace(addr)
	if ap, err := ParseJoinCode(addr); err == nil {
		return ap.String()
	}
	port := strconv.Itoa(defaultPort)

	// Bracketed IPv6 literal: "[::1]" or "[::1]:1234".
	if strings.HasPrefix(addr, "[") {
		if host, p, err := net.SplitHostPort(addr); err == nil {
			return net.JoinHostPort(host, p)
		}
		// No port after the closing bracket — strip the brackets and
		// append the default port.
		host := strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
		return net.JoinHostPort(host, port)
	}

	// Bare IPv6 literal with no brackets and no port: "::1", "fe80::1".
	// A literal IPv6 address has at least two colons; host:port and
	// ipv4:port forms have exactly one, so this check doesn't collide
	// with them. (Bare IPv6 + port, e.g. "::1:1234", is inherently
	// ambiguous without brackets and isn't something we try to handle —
	// callers should bracket it.)
	if strings.Count(addr, ":") > 1 {
		if ip := net.ParseIP(addr); ip != nil {
			return net.JoinHostPort(addr, port)
		}
	}

	// Already host:port or ipv4:port.
	if host, p, err := net.SplitHostPort(addr); err == nil {
		return net.JoinHostPort(host, p)
	}

	// Bare hostname or IPv4 literal with no port.
	return net.JoinHostPort(addr, port)
}

// ValidateAddr reports whether addr looks like something a joiner can dial:
// an IPv4/IPv6 literal or a hostname, with an optional port, in any form
// NormalizeAddr accepts, or a join code (joincode.go). It catches the typos that would otherwise turn
// into a silent reconnect loop: an empty field, embedded spaces, a
// partial IP such as "192.168.1." or "192.168.1", and a port outside
// 1-65535. It does no DNS lookup, so a well-formed but unknown hostname
// still passes. The trailing-dot FQDN form ("host.example.") is rejected
// on purpose: it is far more often a half-typed IP than a real FQDN.
func ValidateAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return errors.New("address is empty")
	}
	if strings.IndexFunc(addr, unicode.IsSpace) >= 0 {
		return errors.New("address must not contain spaces")
	}
	if strings.Contains(addr, "://") {
		return errors.New("leave out the scheme (e.g. http://)")
	}
	// A join code: valid, or a reason that doesn't echo it (it is as
	// private as the address it holds).
	if LooksLikeJoinCode(addr) {
		if _, err := ParseJoinCode(addr); err != nil {
			return err
		}
		return nil
	}

	// Validate exactly what NormalizeAddr will hand to the dialer. The
	// default port only fills in a missing one, so any valid value works.
	host, port, err := net.SplitHostPort(NormalizeAddr(addr, 1))
	if err != nil {
		return fmt.Errorf("%q is not a valid host:port", addr)
	}
	if !validPort(port) {
		return fmt.Errorf("port %q is not a number from 1 to 65535", port)
	}
	if !validHost(host) {
		return fmt.Errorf("%q is not a valid IP address or hostname", host)
	}
	return nil
}

// validPort reports whether p is a decimal port number in 1-65535. Signs
// and other forms strconv.Atoi would accept ("+5") are rejected.
func validPort(p string) bool {
	if p == "" || len(p) > 5 {
		return false
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535
}

// validHost accepts an IP literal (IPv6 zones included) or a hostname made
// of dot-separated labels of letters, digits, '-' and '_' (underscores show
// up in Windows machine names). A hostname whose last label is all digits
// is rejected: that is a mistyped IPv4 address ("192.168.1",
// "10.0.0.256"), never a real name.
func validHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
				c >= '0' && c <= '9' || c == '-' || c == '_'
			if !ok {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	return strings.TrimLeft(last, "0123456789") != ""
}
