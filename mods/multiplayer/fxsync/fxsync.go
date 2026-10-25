// Package fxsync is the session side of the projectile events (ww-online
// review idea 5; design: ww-multiplayer-handoff/archive/ww-online-review/
// DESIGN-big-items.md section (a), option A): other players' bombs,
// boat-cannon shots and arrows made real, world-only, in this game.
//
// The REL (inject/rel/fx.c) does the frame-exact parts in the game: it
// watches the local Link's projectiles and writes an event to the
// projectile block's outbox the frame a bomb leaves his hands, explodes
// or sinks, the boat cannon fires, or an arrow leaves the bow (a 60 Hz
// read across the process boundary would miss most of those frames, and
// an arrow's launch point is only right on the one frame before its first
// procMove); it spawns the copies from the inbox. This package moves the
// events between the block (wwapi/mailbox fx.go, inject/include/wwfx.h)
// and the network (MsgFx, mods/multiplayer/net fx.go):
//
//   - Sender (broadcast-pose): arms the block's send word, drains the
//     outbox, stamps each event with this game's stage name and a seq,
//     and sends it. Events the connection cannot take are dropped (the
//     REL's ring holds WWFX_RING; an event older than MaxAge is stale).
//   - Receiver (puppet-sync): queues the relayed events, drops repeats
//     (per sender seq), its own twin's (the self-filter name), events
//     older than MaxAge, events in another stage or room (on the sea, by
//     distance from this Link instead: its rooms are grid squares a
//     cannonball crosses), in a boss stage or during this game's
//     minigame, and writes the rest into the inbox with the sender's ID.
//
// Off by default (Settings, WW_PROJECTILES): nothing here has run live,
// and a copy sets switches in this game directly, past co-op's holds
// (docs/TESTING.md "Projectiles" decides).
package fxsync

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// MaxAge is how old an event may be (from its drain on the sender, or its
// arrival here) before it is dropped: a bomb copy that arrives seconds
// late explodes nowhere near the sender's.
const MaxAge = 2 * time.Second

// SeaRange is how far (XZ world units) from this Link an event on the sea
// stage may be and still spawn. The sea's rooms are its grid squares, so
// a room match would drop a cannonball the moment it crossed one.
const SeaRange = 10000

// QueueMax caps the receiver's queue (events waiting for inbox room).
const QueueMax = 32

// Settings turns the feature on. The zero value is off.
type Settings struct {
	On bool
}

// SettingsFromEnv is WW_PROJECTILES: on, 1, true or yes turns the
// projectile events on (default off until the live checks pass).
func SettingsFromEnv() Settings {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WW_PROJECTILES"))) {
	case "on", "1", "true", "yes":
		return Settings{On: true}
	}
	return Settings{}
}

// actor_place current.pos in fopAc_ac_c (f_op_actor.h:288).
const offCurPos = 0x1F8

// relLinked reports whether the REL module is linked (its magic in the
// slack): only then are the block's words its.
func relLinked(m mem.Reader) bool {
	v, err := mem.ReadU32(m, mailbox.Mailbox+mailbox.OffRELMagic)
	return err == nil && v == mailbox.RELMagic
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func writeU32(w mem.Mem, a, v uint32) error {
	b := make([]byte, 4)
	mem.PutU32BE(b, v)
	return w.Write(a, b)
}

// --- sender ---------------------------------------------------------------

// Sender drains the outbox (broadcast-pose). Its seq survives reconnects.
type Sender struct {
	seq   uint32
	armed bool
	Sent  int // events handed to send
	Lost  int // events send refused (not connected) or that were stale
}

// Arm writes the send word: FxMagic when on, else 0. It is written again
// whenever the REL is linked and the word reads otherwise (a save state
// load or a soft reset puts other bytes there behind Go's back).
func (s *Sender) Arm(m mem.Mem, on bool) error {
	want := uint32(0)
	if on {
		want = mailbox.FxMagic
	}
	if v, err := mem.ReadU32(m, mailbox.FxAddr+mailbox.FxSendMagicOff); err == nil && v == want && s.armed == on {
		return nil
	}
	if err := writeU32(m, mailbox.FxAddr+mailbox.FxSendMagicOff, want); err != nil {
		return err
	}
	s.armed = on
	return nil
}

// Drain takes every event the REL wrote since the last call, stamps it
// with the current stage name and the next seq and hands it to send (nil
// while not connected: the events are then taken and lost, so a
// reconnect never replays stale ones). It returns how many it took.
func (s *Sender) Drain(m mem.Mem, send func(*network.FxEvent) error) (int, error) {
	if !relLinked(m) {
		return 0, nil
	}
	hdr, err := m.Read(mailbox.FxAddr+mailbox.FxOutWriteOff, 8)
	if err != nil {
		return 0, err
	}
	w, r := mem.U32BE(hdr[0:]), mem.U32BE(hdr[4:])
	if w == r {
		return 0, nil
	}
	if w-r > mailbox.FxRing {
		r = w - mailbox.FxRing // (the REL never overruns; if it did, the oldest are lost)
	}
	st, err := m.Read(addr.CurStageName, network.FxStageLen)
	if err != nil {
		return 0, err
	}
	stage := cString(st)
	n := 0
	for ; r != w; r++ {
		b, err := m.Read(mailbox.FxOutEntry(r), mailbox.FxEvSize)
		if err != nil {
			return n, err
		}
		ev, _ := mailbox.ParseFxEvent(b)
		n++
		if send == nil {
			s.Lost++
			continue
		}
		s.seq++
		e := FromMailbox(ev, stage, s.seq)
		if err := send(&e); err != nil {
			s.Lost++
			continue
		}
		s.Sent++
	}
	return n, writeU32(m, mailbox.FxAddr+mailbox.FxOutReadOff, w)
}

// FromMailbox is a ring entry as a wire event.
func FromMailbox(ev mailbox.FxEvent, stage string, seq uint32) network.FxEvent {
	return network.FxEvent{
		Seq: seq, Kind: ev.Kind, Variant: ev.Variant, Room: ev.Room, Stage: stage, ID: ev.ID,
		Pos: ev.Pos, SpeedF: ev.SpeedF, SpeedY: ev.SpeedY, Gravity: ev.Gravity, Angle: ev.Angle, Timer: ev.Timer,
	}
}

// ToMailbox is a wire event as an inbox entry from sender.
func ToMailbox(e network.FxEvent, sender byte) mailbox.FxEvent {
	return mailbox.FxEvent{
		Kind: e.Kind, Slot: sender, Variant: e.Variant, Room: e.Room, ID: e.ID,
		Pos: e.Pos, SpeedF: e.SpeedF, SpeedY: e.SpeedY, Gravity: e.Gravity, Angle: e.Angle, Timer: e.Timer,
	}
}

// --- receiver -------------------------------------------------------------

// Local is where this game is, for Accept.
type Local struct {
	Ready    bool // Link exists and no stage change is under way
	Stage    string
	Room     int8 // StayNo
	MiniGame bool
	X, Z     float32 // Link's position (the sea's distance rule)
}

// ReadLocal reads Local from the game.
func ReadLocal(m mem.Reader) (Local, error) {
	var l Local
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return l, err
	}
	next, err := mem.ReadS8(m, addr.NextStageEnable)
	if err != nil {
		return l, err
	}
	st, err := m.Read(addr.CurStageName, network.FxStageLen)
	if err != nil {
		return l, err
	}
	room, err := mem.ReadS8(m, addr.StayNo)
	if err != nil {
		return l, err
	}
	mg, err := m.Read(addr.MiniGameType, 1)
	if err != nil {
		return l, err
	}
	l.Stage, l.Room, l.MiniGame = cString(st), room, mg[0] != 0
	if addr.InMEM1(link) && next == 0 {
		pos, err := m.Read(link+offCurPos, 12)
		if err != nil {
			return l, err
		}
		l.X, l.Z = mem.F32BE(pos[0:]), mem.F32BE(pos[8:])
		l.Ready = true
	}
	return l, nil
}

// Drop reasons (Accept).
const (
	DropNone     = ""
	DropNotReady = "not ready"
	DropStage    = "another stage"
	DropRoom     = "another room"
	DropFar      = "too far on the sea"
	DropBoss     = "a boss stage"
	DropMiniGame = "a minigame"
)

// bossStages are the arenas where no copy is made (the boss-kill relay
// and the Heart Container stay free of them): bosskill.go's rows (Gohma,
// Kalle Demos, Gohdan, Molgera, Jalhalla, Helmaroc King), Puppet Ganon's
// GanonK (d_a_arrow.cpp:276) and every X refight stage (refresh.c
// rf_boss_stage).
var bossStages = []string{"M_DragB", "kinBOSS", "SirenB", "kazeB", "M_DaiB", "M2tower", "GanonK"}

// BossStage reports whether no copy is made in stage.
func BossStage(stage string) bool {
	if strings.HasPrefix(stage, "X") {
		return true
	}
	for _, s := range bossStages {
		if s == stage {
			return true
		}
	}
	return false
}

// Accept is the scope rule: the reason e must not spawn here, or DropNone.
func Accept(l Local, e network.FxEvent) string {
	switch {
	case !l.Ready:
		return DropNotReady
	case e.Stage != l.Stage:
		return DropStage
	case BossStage(l.Stage):
		return DropBoss
	case l.MiniGame:
		return DropMiniGame
	case l.Stage == "sea":
		dx, dz := float64(e.Pos[0]-l.X), float64(e.Pos[2]-l.Z)
		if math.Hypot(dx, dz) > SeaRange {
			return DropFar
		}
	case e.Room != l.Room:
		return DropRoom
	}
	return DropNone
}

type queued struct {
	from byte
	e    network.FxEvent
	at   time.Time
}

// Receiver queues the relayed events (Push, from the client's read loop)
// and writes them into the inbox (Flush, from puppet-sync's loop).
type Receiver struct {
	mu    sync.Mutex
	queue []queued
	seen  map[byte]uint32 // per sender ID: the last seq taken
	names map[byte]string // per sender ID: the name its seq belongs to
	epoch uint64          // the server epoch seen and names belong to

	Written int
	Dropped map[string]int // by reason, plus "repeat", "self", "stale", "queue full"
}

// NewReceiver returns an empty Receiver.
func NewReceiver() *Receiver {
	return &Receiver{seen: map[byte]uint32{}, names: map[byte]string{}, Dropped: map[string]int{}}
}

// Push queues e from sender at now (the client's OnFx).
func (r *Receiver) Push(sender byte, e network.FxEvent, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queue) >= QueueMax {
		r.Dropped["queue full"]++
		return
	}
	r.queue = append(r.queue, queued{from: sender, e: e, at: now})
}

// SetEpoch forgets every sender's seq (and the queue) when the server's
// epoch changes: a restarted server hands out its IDs from 1 again, and a
// sender whose app restarted with it counts its seq from 1 again under
// the same ID and name, so its events would all read as repeats. Called
// with client.ServerEpoch() before each Flush.
func (r *Receiver) SetEpoch(epoch uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if epoch == r.epoch {
		return
	}
	r.epoch = epoch
	r.queue = r.queue[:0]
	clear(r.seen)
	clear(r.names)
}

// Arm writes the recv word: FxMagic when on, else 0 (then the REL
// discards the inbox).
func (r *Receiver) Arm(m mem.Mem, on bool) error {
	want := uint32(0)
	if on {
		want = mailbox.FxMagic
	}
	if v, err := mem.ReadU32(m, mailbox.FxAddr+mailbox.FxRecvMagicOff); err == nil && v == want {
		return nil
	}
	return writeU32(m, mailbox.FxAddr+mailbox.FxRecvMagicOff, want)
}

// Flush writes the queued events that pass into the inbox, as far as it
// has room; the rest wait (until MaxAge). remotes names each sender ID
// (a sender's seq starts over when its ID names someone else, or once
// its ID has left remotes: a sender that leaves and comes back, under the
// same ID and name, counts from 1 again), self is puppet-sync's
// self-filter name (its twin's events are its own game's).
func (r *Receiver) Flush(m mem.Mem, remotes []network.RemotePlayer, self string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make(map[byte]string, len(remotes))
	for _, rp := range remotes {
		names[rp.ID] = rp.Name
	}
	for id := range r.seen {
		if _, ok := names[id]; !ok {
			delete(r.seen, id)
			delete(r.names, id)
		}
	}
	if len(r.queue) == 0 {
		return nil
	}
	if !relLinked(m) {
		r.drop(len(r.queue), DropNotReady)
		r.queue = r.queue[:0]
		return nil
	}
	l, err := ReadLocal(m)
	if err != nil {
		return err
	}
	hdr, err := m.Read(mailbox.FxAddr+mailbox.FxInWriteOff, 8)
	if err != nil {
		return err
	}
	w, rd := mem.U32BE(hdr[0:]), mem.U32BE(hdr[4:])
	keep := make([]queued, 0, len(r.queue))
	for i, q := range r.queue {
		if now.Sub(q.at) > MaxAge {
			r.Dropped["stale"]++
			continue
		}
		name := names[q.from]
		if self != "" && name == self {
			r.Dropped["self"]++
			continue
		}
		if prev, ok := r.names[q.from]; !ok || prev != name {
			r.names[q.from] = name
			delete(r.seen, q.from)
		}
		if last, ok := r.seen[q.from]; ok && q.e.Seq <= last {
			r.Dropped["repeat"]++
			continue
		}
		if why := Accept(l, q.e); why != DropNone {
			r.seen[q.from] = q.e.Seq
			r.Dropped[why]++
			continue
		}
		if w-rd >= mailbox.FxRing {
			keep = append(keep, q) // no room yet: it waits
			continue
		}
		if err := m.Write(mailbox.FxInEntry(w), ToMailbox(q.e, q.from).Bytes()); err != nil {
			r.queue = append(keep, r.queue[i:]...)
			return err
		}
		w++
		if err := writeU32(m, mailbox.FxAddr+mailbox.FxInWriteOff, w); err != nil {
			r.queue = append(keep, r.queue[i+1:]...)
			return err
		}
		r.seen[q.from] = q.e.Seq
		r.Written++
	}
	r.queue = keep
	return nil
}

func (r *Receiver) drop(n int, why string) {
	r.Dropped[why] += n
}

// Summary is one log line of the receiver's counts, "" before any event.
func (r *Receiver) Summary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Written == 0 && len(r.Dropped) == 0 {
		return ""
	}
	return fmt.Sprintf("projectiles: %d spawned here, dropped %v", r.Written, r.Dropped)
}
