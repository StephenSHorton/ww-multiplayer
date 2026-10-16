package gui

import "github.com/StephenSHorton/ww-multiplayer/internal/redact"

// IP addresses are masked wherever the window shows them (owner decision
// 2026-10-16, "hide IP addresses by default (streamer-safe)"): someone
// streaming the app must not flash one. internal/redact does the masking;
// the real address still goes where it is needed (Copy, Join), and only
// what is drawn on screen is masked. The header's "Show IP addresses"
// switch (config ui.showIPs, Bridge.SetShowIPs) turns it off.

// masker masks what the window draws, or, with show set (the switch on),
// passes it through.
type masker struct{ show bool }

// hideIPs is the default: every address masked.
var hideIPs = masker{}

// addr is addr ("ip", "ip:port" or "[v6]:port") as the window shows it.
func (m masker) addr(addr string) string {
	if m.show {
		return addr
	}
	return redact.Addr(addr)
}

// text is a free line (log, chat, an error) as the window shows it.
func (m masker) text(text string) string {
	if m.show {
		return text
	}
	return redact.Text(text)
}
