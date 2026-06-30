// Package netutil holds small address-parsing helpers shared by the CLI
// entry points (host/join/server) that don't belong in internal/network
// (which only deals with already-resolved host:port strings).
package netutil

import (
	"net"
	"strconv"
	"strings"
)

// NormalizeAddr ensures addr carries an explicit port, appending
// defaultPort when the caller omitted one. It's IPv6-aware: bracketed
// literals ("[::1]", "[::1]:1234") and bare literals ("::1") are all
// handled, in addition to plain hostnames and IPv4 addresses.
//
// Addresses that already specify a port (in any of the accepted forms)
// are returned unchanged (modulo normalization through
// net.JoinHostPort, e.g. re-bracketing an IPv6 host).
func NormalizeAddr(addr string, defaultPort int) string {
	addr = strings.TrimSpace(addr)
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
