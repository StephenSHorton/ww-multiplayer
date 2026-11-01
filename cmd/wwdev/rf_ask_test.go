package main

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// rfAskRig is a fake game for rf-ask: actors by pid, and a REL that
// answers the request at the first poll with result (and, for
// RECREATED, moves the actor to newPID with newState).
type rfAskRig struct {
	m        *mem.FakeMem
	byPID    map[uint32]actors.Actor
	result   uint32 // the REL's result word: byte3 result, byte2 gate, bytes 1..0 state
	newPID   uint32
	newState uint32
	answered bool
	noREL    bool
}

func (r *rfAskRig) env() rfAskEnv {
	return rfAskEnv{
		find: func(_ mem.Reader, pid uint32) (actors.Actor, bool, error) {
			a, ok := r.byPID[pid]
			return a, ok, nil
		},
		wait: 100 * time.Millisecond,
		sleep: func(time.Duration) {
			if r.noREL || r.answered {
				return
			}
			r.answered = true
			be := binary.BigEndian
			seq, _ := mem.ReadU32(r.m, mailbox.RefreshAddr+mailbox.RefreshSeqOff)
			w := make([]byte, 4)
			be.PutUint32(w, r.result)
			_ = r.m.Store(mailbox.RefreshAddr+mailbox.RefreshResultOff, w)
			be.PutUint32(w, r.newPID)
			_ = r.m.Store(mailbox.RefreshAddr+mailbox.RefreshNewPIDOff, w)
			if r.result>>24 == uint32(mailbox.RefreshRRecreated) {
				old := r.byPID[1234]
				delete(r.byPID, 1234)
				old.PID, old.Addr = r.newPID, 0x80600000
				r.byPID[r.newPID] = old
				be.PutUint32(w, r.newState)
				_ = r.m.Store(old.Addr+0x4B8, w)
			}
			be.PutUint32(w, seq)
			_ = r.m.Store(mailbox.RefreshAddr+mailbox.RefreshAckOff, w)
		},
	}
}

// newVmcRig: a WT soil mound (proc 0x36, switch 0x2A at params >> 8) of
// pid 1234 in room 3, state 0 (BASE_MAIN), and a stale seq 7 acked.
func newVmcRig(t *testing.T) *rfAskRig {
	t.Helper()
	m := mem.NewFakeMem()
	a := actors.Actor{PID: 1234, Addr: 0x80500000, Proc: int16(mailbox.RefreshProcVmc), InitState: 2, HomeRoom: 3, Params: 0x00002A00, Pos: [3]float32{100, 0, 200}}
	be := binary.BigEndian
	w := make([]byte, 4)
	be.PutUint32(w, 7)
	_ = m.Store(mailbox.RefreshAddr+mailbox.RefreshSeqOff, w)
	_ = m.Store(mailbox.RefreshAddr+mailbox.RefreshAckOff, w)
	return &rfAskRig{m: m, byPID: map[uint32]actors.Actor{1234: a}}
}

func TestRfAskRecreated(t *testing.T) {
	r := newVmcRig(t)
	r.result = uint32(mailbox.RefreshRRecreated)<<24 | 0x0000
	r.newPID, r.newState = 1300, 3
	r.m.Record(true)
	var out strings.Builder
	if err := rfAsk(&out, r.m, 1234, r.env()); err != nil {
		t.Fatal(err)
	}
	ws := r.m.Writes()
	if len(ws) != 2 {
		t.Fatalf("%d writes, want 2 (body, then seq)", len(ws))
	}
	if ws[0].Addr != mailbox.RefreshAddr+mailbox.RefreshProcOff || ws[1].Addr != mailbox.RefreshAddr+mailbox.RefreshSeqOff {
		t.Fatalf("writes at 0x%08X then 0x%08X: the seq must be written last", ws[0].Addr, ws[1].Addr)
	}
	if seq := binary.BigEndian.Uint32(ws[1].Data); seq != 8 {
		t.Errorf("seq %d, want 8", seq)
	}
	body := ws[0].Data
	if p := binary.BigEndian.Uint16(body[0:]); p != mailbox.RefreshProcVmc {
		t.Errorf("proc 0x%04X", p)
	}
	if tag := binary.BigEndian.Uint16(body[2:]); tag != mailbox.RefreshTag {
		t.Errorf("tag 0x%04X", tag)
	}
	if room, kind, no := int8(body[mailbox.RefreshRoomOff-4]), body[mailbox.RefreshBitKindOff-4], binary.BigEndian.Uint16(body[mailbox.RefreshBitNoOff-4:]); room != 3 || kind != mailbox.RefreshBitSw || no != 0x2A {
		t.Errorf("room %d kind %d bit 0x%X, want 3 1 0x2A", room, kind, no)
	}
	s := out.String()
	for _, want := range []string{
		"re-create, bit kind 1 bit 42 (0x2A)",
		"before: state +0x4B8 u32 = 0 (idle)",
		"seq 8: re-created (result 8)",
		"new pid 1300",
		"after, pid 1234: gone",
		"after, new pid 1300: state +0x4B8 u32 = 3 (done)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

func TestRfAskGate(t *testing.T) {
	r := newVmcRig(t)
	r.result = uint32(mailbox.RefreshRGate)<<24 | uint32(mailbox.RefreshGateLinkNear)<<16
	var out strings.Builder
	if err := rfAsk(&out, r.m, 1234, r.env()); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "(result 4)") || !strings.Contains(s, "Link near (gate 8)") || !strings.Contains(s, "after, pid 1234: state +0x4B8 u32 = 0 (idle)") {
		t.Errorf("gate output:\n%s", s)
	}
}

func TestRfAskRefusals(t *testing.T) {
	r := newVmcRig(t)
	if err := rfAsk(&strings.Builder{}, r.m, 99, r.env()); err == nil || !strings.Contains(err.Error(), "no actor of pid 99") {
		t.Errorf("unknown pid: %v", err)
	}
	// No switch (0xFF): no candidate, nothing written.
	a := r.byPID[1234]
	a.Params = 0x0000FF00
	r.byPID[1234] = a
	r.m.Record(true)
	if err := rfAsk(&strings.Builder{}, r.m, 1234, r.env()); err == nil || !strings.Contains(err.Error(), "no refresh candidate") {
		t.Errorf("no switch: %v", err)
	}
	if n := len(r.m.Writes()); n != 0 {
		t.Errorf("%d writes for a non-candidate", n)
	}
	// A pending request (seq != ack): refused, nothing written.
	r = newVmcRig(t)
	w := make([]byte, 4)
	binary.BigEndian.PutUint32(w, 9)
	_ = r.m.Store(mailbox.RefreshAddr+mailbox.RefreshSeqOff, w)
	r.m.Record(true)
	if err := rfAsk(&strings.Builder{}, r.m, 1234, r.env()); err == nil || !strings.Contains(err.Error(), "still pending") {
		t.Errorf("pending: %v", err)
	}
	if n := len(r.m.Writes()); n != 0 {
		t.Errorf("%d writes with a request pending", n)
	}
	// No REL: the ack never comes.
	r = newVmcRig(t)
	r.noREL = true
	if err := rfAsk(&strings.Builder{}, r.m, 1234, r.env()); err == nil || !strings.Contains(err.Error(), "not acked") {
		t.Errorf("no REL: %v", err)
	}
}
