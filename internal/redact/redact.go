// Package redact masks IP addresses in what the app draws on screen
// (owner decision 2026-10-16, "hide IP addresses by default
// (streamer-safe)"): someone streaming the window or the TUI must not
// flash one. The GUI and the TUI share it. Only what is shown is masked;
// the real address still goes where it is needed (Join, Copy), and the
// CLI's host and join print theirs on purpose.
//
// Every mask has a fixed shape, so it gives away nothing but the last
// octet or group: an IPv4 is always "•••.•••.•••.42", an IPv6 always
// "••••:…:1a2b".
package redact

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
)

// Dot is the mask character.
const Dot = "•"

const (
	v4Mask = Dot + Dot + Dot + "." + Dot + Dot + Dot + "." + Dot + Dot + Dot + "."
	v6Mask = Dot + Dot + Dot + Dot + ":…:"
)

// IP is ip masked: an IPv4 (or an IPv4-mapped IPv6) keeps its last octet,
// an IPv6 its last group, and the zone is dropped.
func IP(ip netip.Addr) string {
	ip = ip.Unmap()
	if ip.Is4() {
		b := ip.As4()
		return fmt.Sprintf("%s%d", v4Mask, b[3])
	}
	b := ip.As16()
	return fmt.Sprintf("%s%x", v6Mask, uint16(b[14])<<8|uint16(b[15]))
}

// Addr masks the IP in addr ("ip", "[v6]", "ip:port" or "[v6]:port"); the
// port stays. Anything that isn't an IP (a host name, "") comes back
// unchanged.
func Addr(addr string) string {
	if ip, err := netip.ParseAddr(strings.Trim(addr, "[]")); err == nil {
		return IP(ip)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return addr
	}
	if ip.Unmap().Is4() {
		return IP(ip) + ":" + port
	}
	return "[" + IP(ip) + "]:" + port
}

var (
	// v6Candidate is a run that may hold an IPv6 literal: hex digits,
	// colons and dots (an embedded IPv4), an optional zone, brackets, and a
	// port after the closing one. Text checks each run before masking it.
	v6Candidate = regexp.MustCompile(`(?i)\[?[0-9a-f.]*:[0-9a-f:.]*(?:%[0-9a-z_.-]+)?\]?(?::[0-9]{1,5})?`)
	// v4Candidate is a dotted quad.
	v4Candidate = regexp.MustCompile(`[0-9]{1,3}(?:\.[0-9]{1,3}){3}`)
	// trailingPort is a candidate's ":port" end, for an unbracketed
	// "ip:port".
	trailingPort = regexp.MustCompile(`^(.+):([0-9]{1,5})$`)
)

// Text masks every IP address in free text (a log or chat line, a dial
// error): IPv4 and IPv6 literals, bracketed "[v6]:port" and "ip:port"
// forms. Text that only looks like one is left alone: a clock "12:34:56",
// a version "v0.3.0" or "1.2.3.4.5", a MAC address, "999.1.1.1", or a
// word like "std::map".
func Text(text string) string {
	if !strings.ContainsAny(text, ":.") {
		return text
	}
	text = replaceChecked(text, v6Candidate, maskV6Run)
	return replaceChecked(text, v4Candidate, func(s string) (string, bool) {
		ip, err := netip.ParseAddr(s)
		if err != nil || !ip.Is4() {
			return "", false
		}
		return IP(ip), true
	})
}

// maskV6Run masks one v6Candidate run, or says it holds no address.
func maskV6Run(run string) (string, bool) {
	// A run that is all hex letters ("add::", "cafe::") is a word more
	// likely than an address, and "::" alone is punctuation.
	if !strings.ContainsAny(run, "0123456789") {
		return "", false
	}
	if strings.HasPrefix(run, "[") {
		end := strings.Index(run, "]")
		if end < 0 {
			return "", false
		}
		ip, err := netip.ParseAddr(run[1:end])
		if err != nil || ip.Is4() {
			return "", false
		}
		return "[" + IP(ip) + "]" + run[end+1:], true
	}
	if strings.Contains(run, "]") {
		return "", false
	}
	if ip, err := netip.ParseAddr(run); err == nil && !ip.Is4() {
		return IP(ip), true
	}
	// An unbracketed "ip:port": an IPv6 one is ambiguous, but masked all
	// the same; an IPv4 one keeps its port.
	if m := trailingPort.FindStringSubmatch(run); m != nil {
		if ip, err := netip.ParseAddr(m[1]); err == nil {
			return IP(ip) + ":" + m[2], true
		}
	}
	return "", false
}

// replaceChecked replaces each match of re in text that stands on its own
// (no letter, digit or '_' right before or after it, no '.' before it nor
// a '.' and a digit after it) and that mask accepts. A match may end in
// '.' or one ':' of punctuation ("… at fd00::1." or "dial tcp fd00::1:
// refused"), which is left outside the mask.
func replaceChecked(text string, re *regexp.Regexp, mask func(string) (string, bool)) string {
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		for end > start && text[end-1] == '.' {
			end--
		}
		// One ':' after the address is punctuation; "::" ends an address
		// ("fe80::").
		if end-start > 1 && text[end-1] == ':' && text[end-2] != ':' {
			end--
		}
		if end <= start || !standsAlone(text, start, end) {
			continue
		}
		m, ok := mask(text[start:end])
		if !ok {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString(m)
		last = end
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

func standsAlone(text string, start, end int) bool {
	if start > 0 {
		if c := text[start-1]; isWord(c) || c == '.' {
			return false
		}
	}
	if end < len(text) {
		c := text[end]
		if isWord(c) {
			return false
		}
		if (c == '.' || c == ':') && end+1 < len(text) && isDigit(text[end+1]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWord(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}
