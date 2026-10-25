package bugreport

import (
	"regexp"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/redact"
)

// hostRe is a dotted host name, with a ":port" after it when there is one.
// The name's labels are letters, digits and dashes; its last is letters
// only (a TLD), which keeps versions, times and IPs (redact.Text's) out.
var hostRe = regexp.MustCompile(`(?i)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}(?::[0-9]{1,5})?`)

// hostSuffixes are where a player's own address lives: dynamic DNS,
// tunnels (playit, ngrok, Cloudflare), tailnets and LAN names. A name
// under one is masked even without a port; its suffix stays, so a report
// still says which service it went through.
var hostSuffixes = []string{
	"duckdns.org", "ts.net", "playit.gg", "ply.gg", "joinmc.link", "playit.plus",
	"ddns.net", "no-ip.org", "no-ip.com", "no-ip.biz", "hopto.org", "zapto.org", "sytes.net",
	"servegame.com", "myftp.org", "mooo.com", "afraid.org", "dynu.net", "dynu.com",
	"freeddns.org", "dynv6.net", "ngrok.io", "ngrok.app", "ngrok-free.app",
	"trycloudflare.com", "zerotier.net", "local", "lan", "home.arpa", "internal",
}

// fileExts are a "name.ext:N" that is a source or file location (a Go
// panic's "bug_report.go:74"), not a host and port.
var fileExts = map[string]bool{
	"go": true, "c": true, "cc": true, "cpp": true, "h": true, "hpp": true, "s": true,
	"js": true, "mjs": true, "ts": true, "rs": true, "py": true, "cs": true, "log": true,
	"txt": true, "json": true, "ini": true, "toml": true, "yaml": true, "yml": true,
	"dll": true, "exe": true, "sys": true, "rel": true, "dol": true, "html": true, "css": true,
}

// MaskHosts masks the host names in line that may be the address a player
// typed or hosted on, which redact.Text (made for IP literals) leaves be:
// a dotted name with a port ("stephen.duckdns.org:25570",
// "myhouse.example.com:25565"), and any name under a dynamic-DNS, tunnel,
// tailnet or LAN suffix (hostSuffixes) with or without one. The port, and
// a known service's suffix (else only the TLD), stay:
// "•••.duckdns.org:25570", "•••.com:25565". A bare name with no port and
// no such suffix ("github.com", "dolphin-emu.org") and a file location
// ("bug_report.go:74") are left as they are.
func MaskHosts(line string) string {
	if !strings.Contains(line, ".") {
		return line
	}
	idx := hostRe.FindAllStringIndex(line, -1)
	if idx == nil {
		return line
	}
	var b strings.Builder
	last := 0
	for _, ix := range idx {
		s, e := ix[0], ix[1]
		// Part of a longer word.
		if s > 0 && isNameByte(line[s-1]) {
			continue
		}
		if e < len(line) && isNameByte(line[e]) {
			continue
		}
		m := line[s:e]
		if masked, ok := maskHost(m); ok {
			b.WriteString(line[last:s])
			b.WriteString(masked)
			last = e
		}
	}
	b.WriteString(line[last:])
	return b.String()
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
}

// maskHost masks one hostRe match, or says it isn't a player's address.
func maskHost(m string) (string, bool) {
	host, port, hasPort := strings.Cut(m, ":")
	lower := strings.ToLower(host)
	tld := lower[strings.LastIndexByte(lower, '.')+1:]
	suffix := ""
	for _, s := range hostSuffixes {
		if strings.HasSuffix(lower, "."+s) {
			suffix = s
			break
		}
	}
	switch {
	case suffix != "":
	case !hasPort:
		return "", false
	case fileExts[tld]:
		return "", false
	default:
		suffix = tld
	}
	out := redact.Dot + redact.Dot + redact.Dot + "." + suffix
	if hasPort {
		out += ":" + port
	}
	return out, true
}
