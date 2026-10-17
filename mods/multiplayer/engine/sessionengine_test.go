package engine

import (
	"errors"
	"fmt"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// TestSessionErr: a fatal connect error was already logged by the
// session's ConnectLog, so the controller must not log it again, but the
// session still failed (the CLI exits 1); every other error is the
// session's end reason, unchanged.
func TestSessionErr(t *testing.T) {
	if err := SessionErr(nil); err != nil {
		t.Errorf("SessionErr(nil) = %v", err)
	}
	if err := SessionErr(fmt.Errorf("puppet-sync: %w", network.ErrFatal)); !errors.Is(err, session.ErrReported) || !errors.Is(err, network.ErrFatal) {
		t.Errorf("SessionErr(fatal) = %v, want it marked ErrReported", err)
	}
	other := errors.New("server start: bind")
	if err := SessionErr(other); err != other {
		t.Errorf("SessionErr(other) = %v, want it unchanged", err)
	}
}

// TestSessionEngine_LiveIdle: with no session registered the engine reports
// zero live values, as the old hook accessors did.
func TestSessionEngine_LiveIdle(t *testing.T) {
	e := NewTUIEngine(Config{})
	if got := e.Live(); got.PlayerCount != 0 || got.Players != nil || got.HostAddrs != nil || got.Latency != 0 || got.LocalOK {
		t.Errorf("idle Live() = %+v, want zero", got)
	}
	if err := e.SendChat("hi"); err != nil {
		t.Errorf("SendChat with no client = %v, want nil (dropped)", err)
	}
	var _ session.Engine = e
	var _ session.CoopEngine = e
	var _ session.LobbyEngine = e
	var _ session.CoopFileEngine = e
	if cli := NewCLIEngine(Config{}); cli.Live().PlayerCount != 0 || cli.SendChat("hi") != nil {
		t.Error("the CLI engine has live values or a chat")
	}
}
