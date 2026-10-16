package applog

import (
	"fmt"
	"strings"
)

// Diagnostics is what "Help → Copy diagnostics" puts on the clipboard:
// enough for a bug report, nothing secret, nothing uploaded.
type Diagnostics struct {
	App     string // versionLine(), e.g. "ww-multiplayer v0.4.0"
	OS      string // runtime.GOOS/GOARCH
	Dolphin string // the Dolphin executable in use
	// DolphinVersion is Dolphin's version string when known.
	DolphinVersion string
	UserDir        string
	LogFile        string
	// Status is the co-op /status text (formatStatus), one line each.
	Status []string
	// Log is the session's newest log lines (File.Tail), oldest first.
	Log []string
}

// Text renders d as plain text, every line redacted with r. Empty fields
// read "unknown"; the log is cut to its newest TailLines lines.
func (d Diagnostics) Text(r Redactor) string {
	var b strings.Builder
	val := func(s string) string {
		if strings.TrimSpace(s) == "" {
			return "unknown"
		}
		return s
	}
	line := func(k, v string) { fmt.Fprintf(&b, "%-16s %s\n", k+":", r.Redact(val(v))) }
	b.WriteString("WW Multiplayer diagnostics\n")
	line("app", d.App)
	line("os", d.OS)
	line("dolphin", d.Dolphin)
	line("dolphin version", d.DolphinVersion)
	line("user dir", d.UserDir)
	line("log file", d.LogFile)
	if len(d.Status) > 0 {
		b.WriteString("\nco-op status:\n")
		for _, s := range d.Status {
			b.WriteString(r.Redact(s) + "\n")
		}
	}
	logs := d.Log
	if len(logs) > TailLines {
		logs = logs[len(logs)-TailLines:]
	}
	fmt.Fprintf(&b, "\nlast %d log lines:\n", len(logs))
	for _, s := range logs {
		b.WriteString(r.Redact(s) + "\n")
	}
	return b.String()
}
