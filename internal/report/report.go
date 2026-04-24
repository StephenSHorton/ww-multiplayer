// Package report exposes a tiny Reporter interface so the multiplayer
// session goroutines (runBroadcastPoseCtx, runPuppetSyncCtx, the
// network.Server, etc.) can emit log lines without knowing whether
// they are running under the CLI (write to stdout) or the TUI (push
// onto a tea.Cmd channel for the dashboard log panel).
package report

import "fmt"

type Level int

const (
	Info Level = iota
	Net   // [net] tag — wire-level events from the network layer
	Warn
	Err
	OK // green-styled success messages
)

type Reporter interface {
	Log(level Level, msg string)
}

// Logf is a Printf-style helper so callers can write
// `report.Logf(rep, report.Info, "x=%d", x)` instead of constructing
// a fmt.Sprintf manually at every site.
func Logf(r Reporter, level Level, format string, args ...any) {
	r.Log(level, fmt.Sprintf(format, args...))
}

// Stdout is a Reporter that prints to os.Stdout with a per-level
// prefix matching the historical CLI output (`[net] foo`, `ERROR: bar`).
// Used by every CLI subcommand so terminal users see the same log
// format they did before the Reporter refactor.
type Stdout struct{}

func (Stdout) Log(level Level, msg string) {
	switch level {
	case Net:
		fmt.Printf("[net] %s\n", msg)
	case Err:
		fmt.Printf("ERROR: %s\n", msg)
	case Warn:
		fmt.Printf("WARN: %s\n", msg)
	default:
		fmt.Println(msg)
	}
}

// Discard drops every message. Useful for tests and the rare CLI
// path that wants the side effects without the chatter.
type Discard struct{}

func (Discard) Log(Level, string) {}
