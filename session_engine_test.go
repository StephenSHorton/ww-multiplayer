package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// TestTUISessionErr: a fatal connect error was already logged by the
// session's ConnectLog, so the controller must not log it again; every other
// error is the session's end reason.
func TestTUISessionErr(t *testing.T) {
	if err := tuiSessionErr(nil); err != nil {
		t.Errorf("tuiSessionErr(nil) = %v", err)
	}
	if err := tuiSessionErr(fmt.Errorf("puppet-sync: %w", network.ErrFatal)); err != nil {
		t.Errorf("tuiSessionErr(fatal) = %v, want nil", err)
	}
	other := errors.New("server start: bind")
	if err := tuiSessionErr(other); err != other {
		t.Errorf("tuiSessionErr(other) = %v, want it unchanged", err)
	}
}

// TestLiveState_ChatSink: incoming chat goes to the sink sessionEngine set,
// is dropped without one, and survives begin() (which runs after the engine
// set it).
func TestLiveState_ChatSink(t *testing.T) {
	ls := &liveState{}
	ls.pushChat("Zelda", "dropped") // no sink: no panic

	var got []string
	ls.setChat(func(from, text string) { got = append(got, from+": "+text) })
	ls.begin("Join", "Link", nil)
	ls.pushChat("Zelda", "hi")
	ls.setChat(nil)
	ls.pushChat("Zelda", "after")

	if len(got) != 1 || got[0] != "Zelda: hi" {
		t.Errorf("sink got %q, want only the line sent while set", got)
	}
}

// TestSessionEngine_LiveIdle: with no session registered the engine reports
// zero live values, as the old hook accessors did.
func TestSessionEngine_LiveIdle(t *testing.T) {
	e := &sessionEngine{ls: &liveState{}}
	if got := e.Live(); got.PlayerCount != 0 || got.Players != nil || got.HostAddrs != nil || got.Latency != 0 || got.LocalOK {
		t.Errorf("idle Live() = %+v, want zero", got)
	}
	if err := e.SendChat("hi"); err != nil {
		t.Errorf("SendChat with no client = %v, want nil (dropped)", err)
	}
	var _ session.Engine = e
}
