package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/applog"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// tuiSessionLog is the TUI controller's session.Options.SessionLog: each
// session the TUI runs writes its log lines to a file of its own under
// <UserConfigDir>/ww-multiplayer/logs (internal/applog), since a TUI user
// has no scrollback once the app closes. The CLI's host and join don't:
// their lines are on the terminal, as they always were. A log that can't
// be opened is skipped without a word; the session runs the same.
func tuiSessionLog(st session.State) (log.Reporter, func()) {
	dir, err := applog.DefaultDir()
	if err != nil {
		return nil, nil
	}
	return openSessionLog(dir, st, time.Now)
}

// openSessionLog opens st's log file in dir, writes its header line, and
// returns it with its Close.
func openSessionLog(dir string, st session.State, now func() time.Time) (log.Reporter, func()) {
	f, err := applog.Open(dir, string(st.Role), now)
	if err != nil {
		return nil, nil
	}
	f.Log(log.Info, sessionLogHeader(st))
	return f, func() { _ = f.Close() }
}

// sessionLogHeader is a log file's first line: the build, the OS, and what
// the session is.
func sessionLogHeader(st session.State) string {
	what := fmt.Sprintf("%s session %d as %q", st.Role, st.Session, st.Name)
	if st.Addr != "" {
		what += " to " + st.Addr
	}
	if st.Coop.On {
		what += " with co-op"
	}
	return fmt.Sprintf("%s %s/%s: %s", versionLine(), runtime.GOOS, runtime.GOARCH, what)
}
