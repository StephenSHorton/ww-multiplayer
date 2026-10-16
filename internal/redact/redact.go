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
// port stays. Anything else is what a user typed in an address box, so it
// is masked harder than free text: Text's masks, then every run of three
// or more digit groups joined by dots or dashes, wherever it stands. A host
// name with an IP in it ("26.12.34.56.nip.io", "x.26-12-34-56.sslip.io"),
// an address with a typo ("26.12.34.566", "192.168.1.42;25565") or one
// short of an octet ("26.12.34") shows no more than an IP's last group; a
// plain host name ("localhost:25570", "host.ts.net") and "" stay as they
// are.
func Addr(addr string) string {
	if ip, err := netip.ParseAddr(strings.Trim(addr, "[]")); err == nil {
		return IP(ip)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addrRuns(Text(addr))
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return addrRuns(Text(addr))
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
	// A run never starts at a lone ':' ("host:192.168.1.42:25565" is a
	// word, then an address), only at hex digits and a ':' or at "::".
	v6Candidate = regexp.MustCompile(`(?i)\[?(?:[0-9a-f.]+:|::)[0-9a-f:.]*(?:%[0-9a-z_.-]+)?\]?(?::[0-9]{1,5})?`)
	// v4Candidate is a dotted quad, or a near one with a group of four
	// digits; Text masks one that doesn't parse too (a typo).
	v4Candidate = regexp.MustCompile(`[0-9]{1,4}(?:\.[0-9]{1,4}){3}`)
	// dashCandidate is an IPv4 written with dashes, as a host name of the
	// sslip.io kind holds one ("26-12-34-56.sslip.io").
	dashCandidate = regexp.MustCompile(`[0-9]{1,3}(?:-[0-9]{1,3}){3}`)
	// addrRun is, in an address that didn't parse, any run of digit groups
	// that may be most of an IP (addrRuns).
	addrRun = regexp.MustCompile(`[0-9]{1,4}(?:[.-][0-9]{1,4}){2,}`)
	// trailingPort is a candidate's ":port" end, for an unbracketed
	// "ip:port".
	trailingPort = regexp.MustCompile(`^(.+):([0-9]{1,5})$`)
	// nearQuad is a whole v4Candidate.
	nearQuad = regexp.MustCompile(`^` + v4Candidate.String() + `$`)
)

// nearMask is the mask of s, a dotted quad that doesn't parse: an IPv4's
// shape, keeping only its last group.
func nearMask(s string) string { return v4Mask + s[strings.LastIndexByte(s, '.')+1:] }

// Text masks every IP address in free text (a log or chat line, a dial
// error): IPv4 and IPv6 literals, bracketed "[v6]:port" and "ip:port"
// forms, an IPv4 inside a host name ("26.12.34.56.nip.io",
// "26-12-34-56.sslip.io"), and a dotted quad that is an address with a
// typo ("26.12.34.566", "999.1.1.1", "192.168.001.042"). Text that only
// looks like one is left alone: a clock "12:34:56", a version "v0.3.0",
// "0.2.9" or "1.2.3.4.5", a date, a MAC address, or a word like
// "std::map".
func Text(text string) string {
	if !strings.ContainsAny(text, ":.") {
		return text
	}
	text = replaceChecked(text, v6Candidate, maskV6Run)
	text = replaceChecked(text, v4Candidate, func(s string) (string, bool) {
		if ip, err := netip.ParseAddr(s); err == nil && ip.Is4() {
			return IP(ip), true
		}
		// A dotted quad that doesn't parse is a mistyped address far more
		// often than anything else ("26.12.34.566", "192.168.001.042"),
		// and three of its groups are real: masked to the same shape,
		// keeping only its last group.
		return nearMask(s), true
	})
	if !strings.Contains(text, "-") {
		return text
	}
	return replaceChecked(text, dashCandidate, func(s string) (string, bool) {
		ip, err := netip.ParseAddr(strings.ReplaceAll(s, "-", "."))
		if err != nil || !ip.Is4() {
			return "", false
		}
		return IP(ip), true
	})
}

// addrRuns masks every addrRun in addr (Addr's fallback), keeping only its
// last group.
func addrRuns(addr string) string {
	return addrRun.ReplaceAllStringFunc(addr, func(run string) string {
		return v4Mask + run[strings.LastIndexAny(run, ".-")+1:]
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
		if nearQuad.MatchString(m[1]) { // a mistyped "ip:port" (Text)
			return nearMask(m[1]) + ":" + m[2], true
		}
	}
	return "", false
}

// replaceChecked replaces each match of re in text that stands on its own
// (no letter, digit or '_' right before or after it, no digit and '.'
// before it nor a '.' and a digit after it) and that mask accepts. A match may end in
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
		c := text[start-1]
		if isWord(c) {
			return false
		}
		// A '.' before it is a host name's label ("x.26.12.34.56.nip.io"),
		// unless a digit comes before that: the tail of a longer run of
		// numbers ("1.2.3.4.5.6.7.8").
		if c == '.' && (start < 2 || isDigit(text[start-2])) {
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
