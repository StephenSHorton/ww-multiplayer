package wwapi

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/commands"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// The watcher's poll rates (design decision 9): 10 Hz by default, 30 Hz
// as an option.
const (
	DefaultInterval = 100 * time.Millisecond
	FastInterval    = time.Second / 30
)

// The core commands a Game registers. actor.kill and actor.spawn return
// commands.ErrUnsupported until the C op ring exists (design §5.4).
const (
	CmdActorKill  = "core.actor.kill"
	CmdActorSpawn = "core.actor.spawn"
)

// Info is what a Game knows about the game it attached to (design §5.1).
type Info struct {
	PID           int    // the Dolphin process; 0 for a Game made by NewGame
	GameID        string // "GZLE01"
	ABI           uint16 // from the WWMB header (0 = no header: every blob before E1)
	LayoutVersion uint16 // from the same header
	BlobHash      uint32 // CRC-32 of T2 as read from RAM (see Contract.BlobHash)
	BlobMatches   bool   // T2 in RAM is this build's blob
	Features      Features
}

// GameOptions configure NewGame. The zero value is ready to use.
type GameOptions struct {
	// Contract configures the contract check; the zero value checks for
	// GZLE01 and this build's blob.
	Contract ContractOptions
	// Interval is the watcher's poll interval; DefaultInterval when 0.
	Interval time.Duration
	// Bus is the core event bus the watcher emits on; a new one, which
	// Close closes, when nil.
	Bus *events.Bus
	// Log reports the Game's own lines; discarded when nil.
	Log log.Reporter
	// Disc is the user's own disc, for Disc(); nil means no disc data.
	Disc tables.Source
	// CacheDir is where Disc().Tables caches the built tables; "" builds
	// them without a cache.
	CacheDir string
}

// Game is the root handle on one running game (design §5.1): its memory
// (read-only), the watcher's last snapshot and gate status, Link, the
// avatar slots, the actor queue, the save, the disc, the core events and
// the commands.
//
// Every accessor is safe for concurrent use with the watcher.
type Game struct {
	m     mem.Mem
	ro    mem.Reader
	opts  GameOptions
	bus   *events.Bus
	ownsB bool
	cmds  *commands.Registry
	link  *player.Link
	close func()

	mu       sync.Mutex
	pid      int
	contract Contract
	watcher  Watcher
	tracker  gate.Tracker
	snap     *state.Snapshot
	status   gate.Status

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// Attach opens the Dolphin process o picks, runs the contract check and
// starts the watcher, which polls until ctx ends or Close. The error is
// for a Dolphin that cannot be opened or read; a contract the game fails
// still attaches, with writes refused (Contract().Err()).
func Attach(ctx context.Context, o emu.Options, rep log.Reporter) (*Game, error) {
	if o.GameID == "" {
		o.GameID = GameID
	}
	d, err := emu.Attach(o)
	if err != nil {
		return nil, err
	}
	g, err := NewGame(d, GameOptions{Contract: ContractOptions{GameID: o.GameID}, Log: rep})
	if err != nil {
		d.Close()
		return nil, err
	}
	g.pid = int(d.PID())
	g.close = d.Close
	g.Start(ctx)
	return g, nil
}

// NewGame makes a Game over any game memory: a FakeMem under fakegame in
// tests, or a Dolphin the caller attached. It runs the contract check
// and registers the core commands; the watcher does not run until Start
// (or Tick, one poll at a time).
func NewGame(m mem.Mem, o GameOptions) (*Game, error) {
	if m == nil {
		return nil, errors.New("wwapi: NewGame: nil memory")
	}
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.Log == nil {
		o.Log = log.Discard{}
	}
	g := &Game{m: m, ro: readOnly{m}, opts: o, bus: o.Bus, cmds: commands.New(), link: player.NewLink(readOnly{m})}
	if g.bus == nil {
		g.bus, g.ownsB = events.New(events.Options{}), true
	}
	c, err := CheckContract(m, o.Contract)
	if err != nil {
		return nil, err
	}
	g.contract = c
	g.cmds.SetWriteGate(func() error { return g.Contract().Err() })
	for _, name := range []string{CmdActorKill, CmdActorSpawn} {
		if err := g.cmds.Register(name, commands.Spec{Scope: commands.Authority}, commands.Unsupported(name)); err != nil {
			return nil, err
		}
	}
	o.Log.Log(log.Info, contractLine(c))
	return g, nil
}

// contractLine is the one line the contract check logs.
func contractLine(c Contract) string {
	hdr := "no WWMB header (pre-E1 blob)"
	if c.Header.Present {
		hdr = fmt.Sprintf("WWMB header ABI %d layout %d", c.ABI, c.Layout)
	}
	verdict := "writes allowed"
	if !c.WritesAllowed {
		verdict = "writes refused: " + c.Why
	}
	return fmt.Sprintf("game %s, %s, %s mode, features %s; %s", c.GameID, hdr, c.Mode, c.Features, verdict)
}

// readOnly hides Write, so the Reader Mem returns cannot be asserted
// back to a mem.Mem.
type readOnly struct{ m mem.Reader }

func (r readOnly) Read(a uint32, n int) ([]byte, error) { return r.m.Read(a, n) }

// Start runs the watcher in a goroutine until ctx ends or Close. Calling
// it again does nothing.
func (g *Game) Start(ctx context.Context) {
	g.once.Do(func() {
		g.stop, g.done = make(chan struct{}), make(chan struct{})
		go func() {
			defer close(g.done)
			g.run(ctx)
		}()
	})
}

func (g *Game) run(ctx context.Context) {
	t := time.NewTicker(g.opts.Interval)
	defer t.Stop()
	for {
		g.Tick()
		select {
		case <-ctx.Done():
			return
		case <-g.stop:
			return
		case <-t.C:
		}
	}
}

// Tick polls once: it reads a snapshot, runs the gates, diffs the
// snapshot against the last one and emits the events. A failed read is
// a tick with every gate closed; its error is returned. When the
// heartbeat went backwards (a save state was loaded) the contract is
// checked again, as the state may hold another blob.
func (g *Game) Tick() error {
	s, err := state.Read(g.ro, state.Options{})
	if err != nil {
		s = nil
	}
	g.mu.Lock()
	st := g.tracker.Observe(s)
	evs := g.watcher.Step(s, st)
	g.status = st
	if s != nil {
		g.snap = s
	}
	g.mu.Unlock()
	if st.Regressed {
		if c, cerr := CheckContract(g.ro, g.opts.Contract); cerr == nil {
			g.mu.Lock()
			changed := c.WritesAllowed != g.contract.WritesAllowed
			g.contract = c
			g.mu.Unlock()
			if changed {
				g.opts.Log.Log(log.Info, contractLine(c))
			}
		}
	}
	for _, e := range evs {
		_ = g.bus.Emit(e) // only ErrClosed, after Close
	}
	return err
}

// Close stops the watcher, closes the bus if NewGame made it, and releases
// the Dolphin process Attach opened.
func (g *Game) Close() {
	g.once.Do(func() {}) // a Game never started cannot start after Close
	if g.stop != nil {
		select {
		case <-g.stop:
		default:
			close(g.stop)
		}
		<-g.done
	}
	if g.ownsB {
		g.bus.Close()
	}
	if g.close != nil {
		g.close()
		g.close = nil
	}
}

// Info returns what the Game knows about the game.
func (g *Game) Info() Info {
	c := g.Contract()
	return Info{
		PID: g.pid, GameID: c.GameID, ABI: c.ABI, LayoutVersion: c.Layout,
		BlobHash: c.BlobHash, BlobMatches: c.BlobMatches, Features: c.Features,
	}
}

// Contract returns the last contract check.
func (g *Game) Contract() Contract {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.contract
}

// Features is what the patched blob provides.
func (g *Game) Features() Features { return g.Contract().Features }

// Mem is the game's memory, read-only and MEM1-bounded. Writes go
// through Commands.
func (g *Game) Mem() mem.Reader { return g.ro }

// Snapshot is the last snapshot the watcher took, or nil before the
// first good read.
func (g *Game) Snapshot() *state.Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snap
}

// Gate is the gates' status on the last tick.
func (g *Game) Gate() gate.Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.status
}

// Events is the core event bus the watcher emits on.
func (g *Game) Events() *events.Bus { return g.bus }

// Commands is the core command registry.
func (g *Game) Commands() *commands.Registry { return g.cmds }

// Player is Link, read through Mem.
func (g *Game) Player() *player.Link { return g.link }

// Avatars reads the remote-Link (mini-Link) slots.
func (g *Game) Avatars() Avatars { return Avatars{g.ro} }

// Actors reads the actor queue.
func (g *Game) Actors() Actors { return Actors{g.ro} }

// Save reads the save from the watcher's last snapshot.
func (g *Game) Save() Save { return Save{g} }

// Disc reads stages and tables from the user's disc (GameOptions.Disc).
func (g *Game) Disc() Disc { return Disc{g.opts.Disc, g.opts.CacheDir} }

// Avatars reads the mailbox's mini-Link slots (wwapi/avatar writes them).
type Avatars struct{ m mem.Reader }

// AvatarSlot is one mini-Link slot as the mailbox holds it.
type AvatarSlot struct {
	PoseBuf  uint32 // pose_buf_ptrs[i]: the slot's pose buffer, 0 when unarmed
	Joints   uint16 // pose_joint_counts[i]
	BufState uint8  // pose_buf_states[i]
	PoseSeq  uint8  // pose_seqs[i]
	FaceSeq  uint8  // face_seqs[i]
}

// Cap is the number of mini-Link slots (MAILBOX_POSE_SLOT_CAP).
func (Avatars) Cap() int { return mailbox.MailboxPoseSlotCap }

// Slot reads slot i.
func (a Avatars) Slot(i int) (AvatarSlot, error) {
	if i < 0 || i >= mailbox.MailboxPoseSlotCap {
		return AvatarSlot{}, fmt.Errorf("wwapi: avatar slot %d out of range 0..%d", i, mailbox.MailboxPoseSlotCap-1)
	}
	var s AvatarSlot
	var err error
	at := func(off int) uint32 { return mailbox.Mailbox + uint32(off) }
	if s.PoseBuf, err = mem.ReadU32(a.m, at(mailbox.OffPoseBufPtrs+4*i)); err != nil {
		return s, err
	}
	if s.Joints, err = mem.ReadU16(a.m, at(mailbox.OffPoseJointCounts+2*i)); err != nil {
		return s, err
	}
	if s.BufState, err = mem.ReadU8(a.m, at(mailbox.OffPoseBufStates+i)); err != nil {
		return s, err
	}
	if s.PoseSeq, err = mem.ReadU8(a.m, at(mailbox.OffPoseSeqs+i)); err != nil {
		return s, err
	}
	s.FaceSeq, err = mem.ReadU8(a.m, at(mailbox.OffFaceSeqs+i))
	return s, err
}

// Actors reads the actor queue (wwapi/actors).
type Actors struct{ m mem.Reader }

// Read walks g_fopAcTg_Queue, at most limit actors (0: actors.Read's
// default).
func (a Actors) Read(limit int) (actors.Walk, error) { return actors.Read(a.m, limit) }

// Save reads the save from the watcher's last snapshot.
type Save struct{ g *Game }

// View is the save as the last snapshot holds it; false before the first
// good read.
func (s Save) View() (save.View, bool) {
	snap := s.g.Snapshot()
	if snap == nil {
		return save.View{}, false
	}
	return save.ViewOf(snap), true
}

// Image is the saved image (dSv_save_c) of the last snapshot; false
// before the first good read.
func (s Save) Image() (save.Image, bool) {
	var img save.Image
	snap := s.g.Snapshot()
	if snap == nil {
		return img, false
	}
	copy(img[:], snap.Save[:])
	return img, true
}

// ErrNoDisc is returned by Disc reads when the Game has no disc.
var ErrNoDisc = errors.New("wwapi: no disc (GameOptions.Disc is nil)")

// Disc reads stages and the M1 tables from the user's own disc.
type Disc struct {
	src tables.Source
	dir string
}

// Tables returns the disc's tables, from the cache when it is fresh
// (tables.Get).
func (d Disc) Tables() (*tables.Tables, tables.Stats, error) {
	if d.src == nil {
		return nil, tables.Stats{}, ErrNoDisc
	}
	return tables.Get(d.src, d.dir)
}

// Stage loads one stage's actor placements (stage.LoadStage).
func (d Disc) Stage(name string) (*stage.Stage, error) {
	if d.src == nil {
		return nil, ErrNoDisc
	}
	return stage.LoadStage(d.src, name)
}
