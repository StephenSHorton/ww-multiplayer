package feed

import (
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// heartbeatStall is how long spawn_trigger (multiplayer_update's +1 per
// game frame, inject/src/core/hooks.c) may stand still before the feeder
// stops writing through heap pointers. The game runs at 30 fps, so a
// running game moves it every ~33 ms; 100 ms rides out a few dropped
// frames.
const heartbeatStall = 100 * time.Millisecond

// gameRun is the feeder's view of whether the game is running its frames:
// the reset gate (docs/05 "Soft reset"). Every pose and face write lands
// in a GameHeap block named by a mailbox pointer, and the tunic images do
// too. A reset leaves those pointers in the mailbox until main01_init
// zeroes them on the next boot, so between the reset request and that
// zeroing a write could land in whatever the reboot loads there.
type gameRun struct {
	last   uint32
	at     time.Time // when last changed (or was first read)
	read   bool      // last is a real read
	moving bool      // the counter has changed since the first read
	open   bool      // the last verdict
	why    string    // the closed verdict's reason, logged once per close
}

// running reports whether pose, face and tunic writes may be made now:
// mDoRst's mReset is clear (wwapi/addr ResetDataPtr; a reset was asked
// for, and play is being torn down or the console reboots) and the
// heartbeat has not stood still for heartbeatStall (the game's frames have
// stopped: a reboot, or emulation paused). Until the heartbeat has been
// seen moving at all it does not close the gate: the contract check
// already proved this build's mod is loaded. It logs each close and
// reopen once.
func (f *Feeder) running(now time.Time) bool {
	g := &f.run
	why := ""
	if hb, err := f.m.ReadU32(mailbox.SpawnTrigger); err != nil {
		why = "the game cannot be read"
	} else {
		switch {
		case !g.read:
			g.read, g.last, g.at = true, hb, now
		case hb != g.last:
			g.moving, g.last, g.at = true, hb, now
		}
		if g.moving && now.Sub(g.at) >= heartbeatStall {
			why = "the game's frames have stopped (a reboot, or emulation paused)"
		}
	}
	if why == "" {
		// The block's pointer is 0 only before main has set it, which is
		// the reboot itself.
		p, err := f.m.ReadU32(addr.ResetDataPtr)
		switch {
		case err != nil || !addr.InMEM1(p) || !addr.InMEM1(p+addr.ResetDataSize-1):
			why = "the game is rebooting"
		default:
			if r, err := f.m.ReadU32(p + addr.ResetDataOffReset); err != nil || r != 0 {
				why = "the game is resetting"
			}
		}
	}
	open := why == ""
	switch {
	case !open && why != g.why:
		log.Logf(f.rep, log.Warn, "pose feed paused: %s; no pose, face or tunic writes until it runs again", why)
	case open && !g.open && g.why != "":
		log.Logf(f.rep, log.OK, "pose feed resumed: the game is running again")
	}
	g.open, g.why = open, why
	return open
}

// Running is the last verdict of the reset gate (running): whether the
// game was running its frames at the last Feed. puppet-sync holds the
// tunic images to it.
func (f *Feeder) Running() bool { return f.run.open }
