// Package ops is Go's side of the C runtime's rings (inject/include/wwops.h,
// co-op M7, wind#90): the Go -> C command queue, which generalises the warp
// handshake, and the C -> Go event ring of one-frame edges.
//
// A command is an op number and three words. Submit writes it into the
// queue; T2 runs at most two a frame, in order, writes a result and pushes a
// CMD event. Wait polls for the result. Each game has one writer: every
// submit for one game must go through one Queue.
//
// Events drains the event ring. Each game has one reader too: two readers
// would each see some of the events.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package ops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Core op numbers (wwops.h WWAP_OP_*). A mod's start at OpModFirst.
const (
	OpEcho     uint8 = mailbox.OpsOpEcho
	OpKill     uint8 = mailbox.OpsOpKill
	OpModFirst uint8 = mailbox.OpsOpModFirst
)

// Core event kinds (wwops.h WWAP_EV_*). A mod's start at EvModFirst.
const (
	EvBoot     uint16 = mailbox.OpsEvBoot
	EvCmd      uint16 = mailbox.OpsEvCmd
	EvEcho     uint16 = mailbox.OpsEvEcho
	EvModFirst uint16 = mailbox.OpsEvModFirst
)

// Result is what C wrote for a command (wwops.h WWAP_R_*). 0 means not run.
type Result uint8

const (
	ROK       Result = mailbox.OpsROK
	RBadCheck Result = mailbox.OpsRBadCheck
	RUnknown  Result = mailbox.OpsRUnknown
	RNotFound Result = mailbox.OpsRNotFound
	RMismatch Result = mailbox.OpsRMismatch
	RRefused  Result = mailbox.OpsRRefused
	RFailed   Result = mailbox.OpsRFailed
)

var resultNames = map[Result]string{
	ROK: "ok", RBadCheck: "bad_check", RUnknown: "unknown_op", RNotFound: "not_found",
	RMismatch: "mismatch", RRefused: "refused", RFailed: "failed",
}

// String is the result's code ("ok", "not_found", ...).
func (r Result) String() string {
	if s, ok := resultNames[r]; ok {
		return s
	}
	return fmt.Sprintf("result%d", uint8(r))
}

var (
	// ErrNoRuntime: the region has no 'WWAP' magic or another layout (a
	// blob before wind#90, or a game still booting).
	ErrNoRuntime = errors.New("ops: no C runtime (blob without the API region, or not booted)")
	// ErrFull: the queue holds OpsRing commands C has not run yet.
	ErrFull = errors.New("ops: command queue full")
	// ErrReset: the game rebooted (the region was cleared) after the
	// command was written; it may or may not have run.
	ErrReset = errors.New("ops: the game rebooted under the command")
	// ErrLost: the command's entry was reused before its result was read.
	ErrLost = errors.New("ops: the command's result was overwritten")
)

// Ticket names one submitted command.
type Ticket struct {
	N   uint32 // its count: the head it was written at
	Op  uint8
	Seq uint8
}

// Event is one entry of the event ring.
type Event struct {
	N       uint32 // its count
	Kind    uint16
	A, B, C uint32
}

// CmdEvent decodes an EvCmd event: the op, seq and result, the command's
// a and its count.
func (e Event) CmdEvent() (op, seq uint8, r Result, a, n uint32, ok bool) {
	if e.Kind != EvCmd {
		return 0, 0, 0, 0, 0, false
	}
	return uint8(e.A), uint8(e.A >> 8), Result(e.A >> 16), e.B, e.C, true
}

// Stats are the region's counters (they wrap).
type Stats struct {
	Done, Refused, BadCheck, Unknown, EventsDropped uint32
}

// Queue is the command queue and event ring of one game's memory.
type Queue struct {
	m mem.Mem
	// Poll is how often Wait reads the result; 5 ms when 0.
	Poll time.Duration

	mu sync.Mutex
}

// New returns a Queue over m.
func New(m mem.Mem) *Queue { return &Queue{m: m} }

func off(o uint32) uint32 { return mailbox.OpsAddr + o }

// Ready reports whether the C runtime is up: the magic and this build's
// layout.
func (q *Queue) Ready() error {
	b, err := q.m.Read(mailbox.OpsAddr, 6)
	if err != nil {
		return err
	}
	if mem.U32BE(b) != mailbox.OpsMagic || mem.U16BE(b[4:]) != mailbox.OpsLayout {
		return ErrNoRuntime
	}
	return nil
}

// Stats reads the counters.
func (q *Queue) Stats() (Stats, error) {
	b, err := q.m.Read(off(mailbox.OpsStatDoneOff), 20)
	if err != nil {
		return Stats{}, err
	}
	return Stats{mem.U32BE(b), mem.U32BE(b[4:]), mem.U32BE(b[8:]), mem.U32BE(b[12:]), mem.U32BE(b[16:])}, nil
}

// Submit writes one command and returns its ticket; it does not wait for C.
func (q *Queue) Submit(op uint8, a, b, c uint32) (Ticket, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.Ready(); err != nil {
		return Ticket{}, err
	}
	ht, err := q.m.Read(off(mailbox.OpsCmdHeadOff), 8)
	if err != nil {
		return Ticket{}, err
	}
	head, tail := mem.U32BE(ht), mem.U32BE(ht[4:])
	if head-tail >= mailbox.OpsRing {
		return Ticket{}, ErrFull
	}
	seq := uint8(head)
	e := make([]byte, mailbox.OpsEntrySize)
	e[mailbox.OpsCOp] = op
	e[mailbox.OpsCSeq] = seq
	e[mailbox.OpsCCheck] = mailbox.OpsCmdCheck(op, seq, a, b, c)
	mem.PutU32BE(e[mailbox.OpsCA:], a)
	mem.PutU32BE(e[mailbox.OpsCB:], b)
	mem.PutU32BE(e[mailbox.OpsCC:], c)
	if err := q.m.Write(entry(mailbox.OpsCmdRingOff, head), e); err != nil {
		return Ticket{}, err
	}
	// The entry first, then the head (C reads the head, then the entry).
	if err := mem.WriteU32(q.m, off(mailbox.OpsCmdHeadOff), head+1); err != nil {
		return Ticket{}, err
	}
	return Ticket{N: head, Op: op, Seq: seq}, nil
}

func entry(ring uint32, n uint32) uint32 {
	return off(ring) + (n%mailbox.OpsRing)*mailbox.OpsEntrySize
}

// Result reads t's result once C ran it: done is false while it waits.
func (q *Queue) Result(t Ticket) (r Result, done bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.Ready(); err != nil {
		return 0, false, err
	}
	ht, err := q.m.Read(off(mailbox.OpsCmdHeadOff), 8)
	if err != nil {
		return 0, false, err
	}
	head, tail := mem.U32BE(ht), mem.U32BE(ht[4:])
	if int32(head-t.N) <= 0 {
		return 0, false, ErrReset // the head is back at or before the ticket: a reboot cleared the queue
	}
	if int32(tail-t.N) <= 0 {
		return 0, false, nil // not run yet
	}
	if head-t.N > mailbox.OpsRing {
		return 0, false, ErrLost
	}
	e, err := q.m.Read(entry(mailbox.OpsCmdRingOff, t.N), mailbox.OpsEntrySize)
	if err != nil {
		return 0, false, err
	}
	if e[mailbox.OpsCSeq] != t.Seq || e[mailbox.OpsCOp] != t.Op {
		return 0, false, ErrLost
	}
	return Result(e[mailbox.OpsCRes]), true, nil
}

// Wait polls t's result until C ran it or ctx ends.
func (q *Queue) Wait(ctx context.Context, t Ticket) (Result, error) {
	poll := q.Poll
	if poll <= 0 {
		poll = 5 * time.Millisecond
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		r, done, err := q.Result(t)
		if err != nil || done {
			return r, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-tick.C:
		}
	}
}

// Run is Submit then Wait.
func (q *Queue) Run(ctx context.Context, op uint8, a, b, c uint32) (Result, error) {
	t, err := q.Submit(op, a, b, c)
	if err != nil {
		return 0, err
	}
	return q.Wait(ctx, t)
}

// Events takes every event C wrote since the last call and returns them in
// order. An entry whose seq or check byte is wrong is skipped (counted in
// bad). The read word moves past everything taken.
func (q *Queue) Events() (evs []Event, bad int, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.Ready(); err != nil {
		return nil, 0, err
	}
	wr, err := q.m.Read(off(mailbox.OpsEvWriteOff), 8)
	if err != nil {
		return nil, 0, err
	}
	w, r := mem.U32BE(wr), mem.U32BE(wr[4:])
	if w-r > mailbox.OpsRing {
		// Never more than a ring ahead unless the region was reset
		// under us: start over from C's word.
		r = w
	}
	for n := r; n != w; n++ {
		e, err := q.m.Read(entry(mailbox.OpsEvRingOff, n), mailbox.OpsEntrySize)
		if err != nil {
			return evs, bad, err
		}
		ev := Event{N: n, Kind: mem.U16BE(e[mailbox.OpsEKind:]), A: mem.U32BE(e[mailbox.OpsEA:]), B: mem.U32BE(e[mailbox.OpsEB:]), C: mem.U32BE(e[mailbox.OpsEC:])}
		if e[mailbox.OpsESeq] != uint8(n) || e[mailbox.OpsECheck] != mailbox.OpsEventCheck(ev.Kind, uint8(n), ev.A, ev.B, ev.C) {
			bad++
			continue
		}
		evs = append(evs, ev)
	}
	if w != r || mem.U32BE(wr[4:]) != w {
		if err := mem.WriteU32(q.m, off(mailbox.OpsEvReadOff), w); err != nil {
			return evs, bad, err
		}
	}
	return evs, bad, nil
}
