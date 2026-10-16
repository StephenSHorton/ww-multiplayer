package session

import (
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// EventKind says which fields of an Event are set.
type EventKind int

const (
	// EventLog is one Reporter line: Level and Text.
	EventLog EventKind = iota + 1
	// EventChat is one incoming chat line: From and Text.
	EventChat
	// EventState says the session's State changed (it started or ended);
	// call Snapshot for the new values. Live values change without it:
	// poll Snapshot for those.
	EventState
	// EventEnded says the session returned: Text is its end reason. It is
	// published before the EventState that shows Phase Ended.
	EventEnded
)

// Event is one thing that happened in a session. Session is the ID of the
// run it came from, so a UI can ignore a late event from a run it has left.
type Event struct {
	Kind    EventKind
	Session ID
	Time    time.Time
	Level   report.Level // EventLog
	From    string       // EventChat
	Text    string       // EventLog, EventChat, EventEnded
}

// Subscribe returns a channel that receives every event published from now
// on, and a func that ends the subscription and closes the channel (safe to
// call more than once).
//
// A subscription never blocks a session: when its buffer is full the oldest
// queued event is dropped to make room, so a slow reader loses old log lines
// rather than stalling broadcast-pose or puppet-sync. A run's last two
// events (EventEnded, then EventState) always get in; New keeps the buffer
// at MinBuffer or more for that.
func (c *Controller) Subscribe() (<-chan Event, func()) {
	s := &subscription{ch: make(chan Event, c.opts.Buffer)}
	c.subMu.Lock()
	c.subs[s] = struct{}{}
	c.subMu.Unlock()
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			c.subMu.Lock()
			delete(c.subs, s)
			close(s.ch)
			c.subMu.Unlock()
		})
	}
}

type subscription struct {
	ch chan Event
}

// send queues ev, dropping the oldest queued events while the buffer is
// full. The caller holds Controller.subMu, so it is the only sender and the
// loop ends: each pass either queues ev or frees a slot (or finds the reader
// just freed one).
func (s *subscription) send(ev Event) {
	for {
		select {
		case s.ch <- ev:
			return
		default:
		}
		select {
		case <-s.ch:
		default:
		}
	}
}
