package session

// Co-op in a lobby session (App PR 9, design §2.5-§2.8): what the app's
// co-op side reports beside its questions (the founding of a new world,
// the co-op options it runs with), the advice and the joiners' requests
// kept in State until answered, "Answer yes to co-op questions", and the
// commands the window's dialogs and buttons send.

import (
	"errors"
	"strconv"
)

// FoundingPhase is where the host's founding of a new world is.
type FoundingPhase string

const (
	// FoundingWaiting: the host's game is not in game and idle yet (or
	// its Dolphin is not running the game yet).
	FoundingWaiting FoundingPhase = "waiting"
	// FoundingBlocked: the loaded file can't found a world (Blocker says
	// why); the founding waits for another file, or for its tag to go.
	FoundingBlocked FoundingPhase = "blocked"
	// FoundingFounded: World is founded; the session goes on.
	FoundingFounded FoundingPhase = "founded"
	// FoundingFailed: the world could not be made (Err); co-op is off.
	FoundingFailed FoundingPhase = "failed"
)

// Blocker kinds: why a loaded file can't found a world.
const (
	BlockBound       = "bound"        // bound to a world already
	BlockForeign     = "foreign"      // a tag a newer build wrote
	BlockGarbage     = "garbage"      // dSv_reserve_c holds no co-op tag
	BlockSecondQuest = "second-quest" // co-op takes no second quest
)

// Founding is the host's founding of a new world from its loaded file
// (State.Founding).
type Founding struct {
	Phase FoundingPhase
	// Why is the in-game gate's reason while Waiting ("" once in game
	// and idle).
	Why string
	// File describes the loaded file ("" before one is loaded).
	File string
	// Blocker and BlockText say why the file can't found a world while
	// Blocked; TagWorld is the world its tag names (BlockBound), and
	// TagWorldHere says that world is on this computer, so Continue can
	// open it.
	Blocker, BlockText, TagWorld string
	TagWorldHere                 bool
	// World is the world founded (Founded).
	World string
	// Err is why the founding failed (Failed).
	Err string
}

// Request is a joiner's request to the host (State.Requests).
type Request struct {
	ID   string // "r1", "r2", ...
	Kind string // "merge"
	From string
}

// CoopOutput is what an Output of a lobby session adds for co-op; the
// engine finds it by a type assertion.
type CoopOutput interface {
	// Founding passes the founding's progress (State.Founding).
	Founding(f Founding)
	// Coop passes the co-op options the session runs with once its
	// co-op side starts (State.Coop), and again when they change (the
	// host allowed merging).
	Coop(o CoopOptions)
	// Backup passes the folder the session's memory-card backup went to
	// (State.CoopBackup).
	Backup(dir string)
}

// CoopFileEngine is what an engine whose co-op side can unbind the
// loaded file adds (the founding dialog's and a foreign file's "Remove
// the tag", as `coop-tag clear --yes` does).
type CoopFileEngine interface {
	// UnbindFile zeroes the co-op tag of the file loaded in this
	// player's game (in its memory: the card keeps it until a save),
	// when that tag still names world, the one the window showed ("" for
	// a file the window showed with no readable tag): a stale dialog
	// unbinds nothing else.
	UnbindFile(world string) error
}

// ErrNoAdvice and ErrNoRequest are returned for advice or a request that
// is not open (dismissed already, or another session's).
var (
	ErrNoAdvice  = errors.New("no such advice")
	ErrNoRequest = errors.New("no such request")
)

// founding records run id's founding.
func (c *Controller) founding(id ID, f Founding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != id || c.cur == nil || c.cur.id != id {
		return
	}
	if old := c.state.Founding; old != nil && *old == f {
		return
	}
	c.state.Founding = &f
	c.publish(Event{Kind: EventState, Session: id})
}

func (o *output) Founding(f Founding) { o.c.founding(o.id, f) }

// coopOn records the co-op options run id runs with.
func (c *Controller) coopOn(id ID, co CoopOptions) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != id || c.cur == nil || c.cur.id != id || c.state.Coop == co {
		return
	}
	c.state.Coop = co
	c.publish(Event{Kind: EventState, Session: id})
}

func (o *output) Coop(co CoopOptions) { o.c.coopOn(o.id, co) }

func (o *output) Backup(dir string) {
	c := o.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != o.id || c.state.CoopBackup == dir {
		return
	}
	c.state.CoopBackup = dir
	c.publish(Event{Kind: EventState, Session: o.id})
}

// advise records advice a for run id, the newest of its kind per player,
// and publishes it.
func (c *Controller) advise(id ID, a Advice) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextA++
	a.ID = "a" + strconv.FormatUint(c.nextA, 10)
	if c.state.Session == id {
		kept := c.state.Advice[:0:0]
		for _, o := range c.state.Advice {
			if o.Kind != a.Kind || o.Player != a.Player {
				kept = append(kept, o)
			}
		}
		c.state.Advice = append(kept, a)
	}
	c.publish(Event{Kind: EventAdvice, Session: id, Advice: a})
}

// DismissAdvice closes the advice id (its dialog was closed).
func (c *Controller) DismissAdvice(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, a := range c.state.Advice {
		if a.ID == id {
			c.state.Advice = append(c.state.Advice[:i:i], c.state.Advice[i+1:]...)
			c.publish(Event{Kind: EventState, Session: c.state.Session})
			return nil
		}
	}
	return ErrNoAdvice
}

// request records a joiner's request to the host of run id: one per
// kind and joiner.
func (c *Controller) request(id ID, kind, from string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != id || c.cur == nil || c.cur.id != id {
		return
	}
	for _, r := range c.state.Requests {
		if r.Kind == kind && r.From == from {
			return
		}
	}
	c.nextR++
	c.state.Requests = append(c.state.Requests, Request{ID: "r" + strconv.FormatUint(c.nextR, 10), Kind: kind, From: from})
	c.publish(Event{Kind: EventState, Session: id})
}

// takeRequest removes and returns the open request id.
func (c *Controller) takeRequest(id string) (Request, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, r := range c.state.Requests {
		if r.ID == id {
			c.state.Requests = append(c.state.Requests[:i:i], c.state.Requests[i+1:]...)
			c.publish(Event{Kind: EventState, Session: c.state.Session})
			return r, true
		}
	}
	return Request{}, false
}

// DismissRequest answers the request id no: nothing changes.
func (c *Controller) DismissRequest(id string) error {
	if _, ok := c.takeRequest(id); !ok {
		return ErrNoRequest
	}
	return nil
}

// AllowMerge answers the merge request id yes (host): the session's
// settings get merge on, which lets files with progress the world lacks,
// or far behind it, bind from now on. Every open merge request is
// answered with it.
func (c *Controller) AllowMerge(id string) error {
	c.mu.Lock()
	var l *Lobby
	if c.state.Lobby != nil {
		l = c.state.Lobby.clone()
	}
	ok := false
	for _, r := range c.state.Requests {
		ok = ok || (r.ID == id && r.Kind == "merge")
	}
	c.mu.Unlock()
	if !ok {
		return ErrNoRequest
	}
	if l == nil {
		return ErrNoLobby
	}
	s := l.Settings
	s.Merge = true
	if err := c.SetCoopSettings(s); err != nil {
		return err
	}
	c.mu.Lock()
	kept := c.state.Requests[:0:0]
	for _, r := range c.state.Requests {
		if r.Kind != "merge" {
			kept = append(kept, r)
		}
	}
	c.state.Requests = kept
	c.publish(Event{Kind: EventState, Session: c.state.Session})
	c.mu.Unlock()
	return nil
}

// SetAutoYes turns "Answer yes to co-op questions" on or off for the
// running session: on, every question is answered yes as it is asked,
// and the open ones at once. It is never saved; a new session starts
// with it off.
func (c *Controller) SetAutoYes(on bool) error {
	c.mu.Lock()
	if c.cur == nil {
		c.mu.Unlock()
		return ErrNoSession
	}
	c.state.AutoYes = on
	var open []string
	if on {
		for _, q := range c.state.Questions {
			open = append(open, q.ID)
		}
	}
	c.publish(Event{Kind: EventState, Session: c.state.Session})
	c.mu.Unlock()
	for _, id := range open {
		_ = c.Answer(id, true)
	}
	return nil
}

// UnbindFile zeroes the co-op tag of the file loaded in this player's
// game (CoopFileEngine), so a file bound to another world can found or
// join this one; world is the tag's world the window showed.
func (c *Controller) UnbindFile(world string) error {
	c.mu.Lock()
	running := c.cur != nil
	c.mu.Unlock()
	if !running {
		return ErrNoSession
	}
	fe, ok := c.engine.(CoopFileEngine)
	if !ok {
		return ErrNoCoop
	}
	return fe.UnbindFile(world)
}

// SetCoopShares changes what co-op shares while the session runs: on is
// every session feature the host's hub allows from now on (the app's
// owner-editable rules; CoopShareEngine). Host only: a joiner's engine
// refuses it.
func (c *Controller) SetCoopShares(on []string) error {
	c.mu.Lock()
	running := c.cur != nil
	c.mu.Unlock()
	if !running {
		return ErrNoSession
	}
	se, ok := c.engine.(CoopShareEngine)
	if !ok {
		return ErrNoCoop
	}
	return se.SetCoopShares(on)
}
