package session

import (
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
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
	// EventQuestion asks a co-op question: Question, with its ID for
	// Controller.Answer. It is also in State.Questions until closed.
	EventQuestion
	// EventQuestionClosed says Question (by ID) is answered, Yes, or
	// went unanswered (a no) because the session ended.
	EventQuestionClosed
	// EventAdvice is co-op advice: Advice.
	EventAdvice
	// EventLobbyNotice: a lobby notice or refusal: Notice (and Text).
	EventLobbyNotice
	// EventLobbyStart: the host pressed Start: Settings.
	EventLobbyStart
	// EventLobbyGo: the host's side is ready: Text is the co-op world, if
	// any.
	EventLobbyGo
	// EventLobbyRequest: a joiner asks the host (host side): From, and
	// Text the kind ("merge").
	EventLobbyRequest
	// EventLegacyHost: the host has no lobby, so the session joined
	// directly.
	EventLegacyHost
	// EventMod is a mod's, from the mod runtime's event bus
	// (Options.Bus; mods.go): Mod.ID is the mod. Either its status
	// changed (Payload nil; Mod has the new status and reason, and Text
	// is Mod.String()), or it emitted one of its mod.<id>.* events
	// (Payload; Text is the event's name). Its Session is 0: mods are
	// the app's, not one session's.
	EventMod
)

// Event is one thing that happened in a session. Session is the ID of the
// run it came from, so a UI can ignore a late event from a run it has left.
type Event struct {
	Kind    EventKind
	Session ID
	Time    time.Time
	Level   log.Level // EventLog
	From    string    // EventChat
	Text    string    // EventLog, EventChat, EventEnded
	// Question is EventQuestion's question, or the closed one's.
	Question Question
	Yes      bool   // EventQuestionClosed: the answer
	Advice   Advice // EventAdvice
	// Lobby events.
	Notice   Notice        // EventLobbyNotice
	Settings LobbySettings // EventLobbyStart
	// EventMod: the mod (with its status on a status change) and the
	// mod's own event, if that is what it was.
	Mod     ModStatus
	Payload events.Event
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
//
// Any other event may be dropped, EventQuestion and EventQuestionClosed
// too. A UI must not keep its open questions from those events alone:
// State.Questions (Snapshot) is their source of truth, so a UI re-reads
// it as it polls the live values (the TUI's status tick does), and shows
// no question once the session has ended. The events only show a change
// sooner. An Answer returning ErrNoQuestion likewise says the question
// is no longer open.
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
