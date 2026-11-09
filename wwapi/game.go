package wwapi

import (
	"context"
	"encoding/json"
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
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The watcher's poll rates (design decision 9): 10 Hz by default, 30 Hz
// as an option.
const (
	DefaultInterval = 100 * time.Millisecond
	FastInterval    = time.Second / 30
)

// The core commands a Game registers. actor.kill runs the C runtime's KILL
// op through the command queue (wwapi/ops, KillArgs) on a blob with the
// "ops" feature, and returns commands.ErrUnsupported on one without it;
// actor.spawn returns ErrUnsupported until a SPAWN op exists (design
// §5.4). player.warp is player.Warp over the Game's memory (WarpArgs).
const (
	CmdActorKill  = "core.actor.kill"
	CmdActorSpawn = "core.actor.spawn"
	CmdPlayerWarp = "core.player.warp"
)

// DefaultWarpTimeout is how long core.player.warp waits for the C warp
// handler when WarpArgs.TimeoutMS is 0.
const DefaultWarpTimeout = time.Second

// WarpArgs are core.player.warp's arguments: where to put Link, and how
// long to wait for the game to take the request (DefaultWarpTimeout
// when 0).
type WarpArgs struct {
	Pos       [3]float32 `json:"pos"`
	TimeoutMS int        `json:"timeout_ms,omitempty"`
}

// DefaultKillTimeout is how long core.actor.kill waits for the C side to
// run the command when KillArgs.TimeoutMS is 0.
const DefaultKillTimeout = time.Second

// KillArgs are core.actor.kill's arguments: the actor's process ID, its
// proc name and, unless nil, its setID. C reads the actor back and refuses
// (code "mismatch") when the ID now names another actor, "not_found" when
// it is gone, "refused" for a player or while Link does not exist. Only the
// actor goes: no save bit is set.
type KillArgs struct {
	PID       uint32  `json:"pid"`
	Proc      int16   `json:"proc"`
	SetID     *uint16 `json:"set_id,omitempty"`
	TimeoutMS int     `json:"timeout_ms,omitempty"`
}

// warpRange is what core.player.warp writes: warp_seq, warp_ack and
// warp_x/y/z of the mailbox.
var warpRange = write.Range{Addr: mailbox.Mailbox + mailbox.OffWarpSeq, Len: mailbox.OffWarpZ + 4 - mailbox.OffWarpSeq}

// Info is what a Game knows about the game it attached to (design §5.1).
type Info struct {
	PID           int      // the Dolphin process; 0 for a Game made by NewGame
	GameID        string   // "GZLE01"
	ABI           uint16   // from the WWMB header (0 = no header: every blob before E1)
	LayoutVersion uint16   // from the same header
	BlobHash      uint32   // CRC-32 of the blob's read-only sections as read from RAM (see Contract.BlobHash)
	BlobMatches   bool     // the code in RAM is this build's blob
	Features      Features // from the header's features word; known and empty without a header
	// The REL module (inject/rel), read from the mailbox slack
	// (Contract.REL): whether one is linked right now, and its build id
	// (0 when none). Reported, not enforced: a game without it is allowed.
	RELLinked  bool
	RELBuildID uint32
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
	ops   *ops.Queue
	link  *player.Link
	close func()

	mu       sync.Mutex
	pid      int
	contract Contract
	watcher  Watcher
	tracker  gate.Tracker
	snap     *state.Snapshot
	status   gate.Status

	stop      chan struct{}
	done      chan struct{}
	once      sync.Once // Start (and Close, so a closed Game never starts)
	closeOnce sync.Once
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
	g.ops = ops.New(m)
	kill := commands.Unsupported(CmdActorKill)
	var killWrites []write.Range
	if c.Features.Has("ops") {
		kill, killWrites = g.kill, []write.Range{opsRange}
	}
	if err := g.cmds.Register(CmdActorKill, commands.Spec{Scope: commands.Authority, Writes: killWrites}, kill); err != nil {
		return nil, err
	}
	if err := g.cmds.Register(CmdActorSpawn, commands.Spec{Scope: commands.Authority}, commands.Unsupported(CmdActorSpawn)); err != nil {
		return nil, err
	}
	if err := g.cmds.Register(CmdPlayerWarp, commands.Spec{Scope: commands.Local, Writes: []write.Range{warpRange}}, g.warp); err != nil {
		return nil, err
	}
	o.Log.Log(log.Info, ContractLine(c))
	return g, nil
}

// warp is core.player.warp: player.Warp over the Game's memory. The
// write gate (the contract check) has already passed.
func (g *Game) warp(_ context.Context, raw json.RawMessage) (commands.Result, error) {
	var a WarpArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		err = fmt.Errorf("%s: args: %w", CmdPlayerWarp, err)
		return commands.Result{Code: "bad_args", Err: err}, err
	}
	if a.TimeoutMS < 0 {
		err := fmt.Errorf("%s: negative timeout_ms %d", CmdPlayerWarp, a.TimeoutMS)
		return commands.Result{Code: "bad_args", Err: err}, err
	}
	timeout := DefaultWarpTimeout
	if a.TimeoutMS > 0 {
		timeout = time.Duration(a.TimeoutMS) * time.Millisecond
	}
	if err := player.Warp(g.m, a.Pos, timeout); err != nil {
		err = fmt.Errorf("%s: %w", CmdPlayerWarp, err)
		return commands.Result{Code: "warp_failed", Err: err}, err
	}
	return commands.Result{OK: true}, nil
}

// opsRange is what core.actor.kill writes: the API region's header and
// rings (the command entry and head; the event read word stays the
// reader's).
var opsRange = write.Range{Addr: mailbox.OpsAddr, Len: mailbox.OpsRingEnd}

// Ops is the Game's command queue and event ring (wwapi/ops). It is the
// one writer of the queue for this game: a mod that runs its own ops
// submits through it.
func (g *Game) Ops() *ops.Queue { return g.ops }

// kill is core.actor.kill: the KILL op through the command queue.
func (g *Game) kill(ctx context.Context, raw json.RawMessage) (commands.Result, error) {
	var a KillArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		err = fmt.Errorf("%s: args: %w", CmdActorKill, err)
		return commands.Result{Code: "bad_args", Err: err}, err
	}
	if a.TimeoutMS < 0 || a.PID == 0xFFFFFFFF {
		err := fmt.Errorf("%s: bad timeout_ms %d or pid 0x%08X", CmdActorKill, a.TimeoutMS, a.PID)
		return commands.Result{Code: "bad_args", Err: err}, err
	}
	timeout := DefaultKillTimeout
	if a.TimeoutMS > 0 {
		timeout = time.Duration(a.TimeoutMS) * time.Millisecond
	}
	setID := uint32(0xFFFFFFFF)
	if a.SetID != nil {
		setID = uint32(*a.SetID)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := g.ops.Run(ctx, ops.OpKill, a.PID, uint32(uint16(a.Proc)), setID)
	if err != nil {
		err = fmt.Errorf("%s: %w", CmdActorKill, err)
		return commands.Result{Code: "kill_failed", Err: err}, err
	}
	if r != ops.ROK {
		err := fmt.Errorf("%s: pid %d: %s", CmdActorKill, a.PID, r)
		return commands.Result{Code: r.String(), Err: err}, err
	}
	return commands.Result{OK: true}, nil
}

// ContractLine is the one line the contract check logs: the game, the
// header, the code hash and the verdict (with RepatchAdvice when it
// refuses).
func ContractLine(c Contract) string {
	hdr := "no WWMB header"
	if c.Header.Present {
		hdr = fmt.Sprintf("WWMB header ABI %d layout %d", c.ABI, c.Layout)
	}
	match := "matches this version"
	if !c.BlobMatches {
		match = fmt.Sprintf("this version's is %08X", c.WantHash)
	}
	verdict := "writes allowed"
	if !c.WritesAllowed {
		verdict = "writes refused: " + c.Why + ". " + RepatchAdvice
	}
	return fmt.Sprintf("game %s, %s, code hash %08X (%s), %s mode, features %s; %s", c.GameID, hdr, c.BlobHash, match, c.Mode, c.Features, verdict)
}

// readOnly hides Write, so the Reader Mem returns cannot be asserted
// back to a mem.Mem.
type readOnly struct{ m mem.Reader }

func (r readOnly) Read(a uint32, n int) ([]byte, error) { return r.m.Read(a, n) }

// Start runs the watcher in a goroutine until ctx ends or Close. Calling
// it again does nothing.
//
// The first tick runs at once and emits StageChanged{First: true}; a
// subscription made after Start can miss it (events.Wait and Chan only
// see events emitted after they subscribe). Subscribe before Start, or
// read events.Last.
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
				g.opts.Log.Log(log.Info, ContractLine(c))
			}
		}
	}
	for _, e := range evs {
		_ = g.bus.Emit(e) // only ErrClosed, after Close
	}
	g.drainRuntime()
	return err
}

// drainRuntime emits the C runtime's events (wwapi/ops) as
// events.RuntimeEvent, on a blob with the "ops" feature while the
// contract allows writes (taking them moves the ring's read word).
func (g *Game) drainRuntime() {
	c := g.Contract()
	if !c.WritesAllowed || !c.Features.Has("ops") {
		return
	}
	evs, _, err := g.ops.Events()
	if err != nil {
		return
	}
	for _, e := range evs {
		_ = g.bus.Emit(events.RuntimeEvent{N: e.N, Kind: e.Kind, A: e.A, B: e.B, C: e.C})
	}
}

// Close stops the watcher, closes the bus if NewGame made it, and releases
// the Dolphin process Attach opened. It is safe to call more than once
// and from several goroutines; every call returns once the first has
// finished.
func (g *Game) Close() {
	g.closeOnce.Do(g.closeNow)
}

func (g *Game) closeNow() {
	g.once.Do(func() {}) // a Game never started cannot start after Close
	if g.stop != nil {
		close(g.stop)
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
		RELLinked: c.REL.Linked, RELBuildID: c.REL.BuildID,
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
