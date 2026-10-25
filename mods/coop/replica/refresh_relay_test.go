package replica

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The actor_refresh relay's fixture (refresh_relay.go): refresh.go's rig
// (DRC, Link in room 1 at the origin, memory switch refreshSw granted),
// with --coop-refresh set, a clock the test moves, and the REL module of
// this build linked unless asked otherwise. A boulder (Stone2) of room 1
// reads refreshSw as its switch ((params >> 8) & 0xFF, d_a_stone2.h:
// 50-51), 1000 units from Link.

// refreshStone2 is an Ebrock2 of room 1 whose switch is refreshSw.
func refreshStone2() fakegame.Enemy {
	return fakegame.Enemy{Stage: drc.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.Proc(mailbox.RefreshProcStone2),
		Params: 0xFFFF00FF&^0xFF00 | refreshSw<<mailbox.RefreshStone2SwShift, Pos: far}
}

// relayRig is a refreshRig with a clock.
type relayRig struct {
	*refreshRig
	clock *breakerClock
}

// newRelayRig is the fixture with --coop-refresh tokens and settle; rel
// links this build's REL module.
func newRelayRig(t *testing.T, tokens string, settle time.Duration, rel bool, es ...fakegame.Enemy) *relayRig {
	t.Helper()
	demoVerified(t, true)
	clock := &breakerClock{t: time.Unix(1_700_000_000, 0)}
	rec := newRecRigNoWelcome(t, Config{Apply: true, Tables: m6Tables(t), RefreshSettle: settle, Refresh: tokens, Now: clock.Now})
	rec.frames = 3
	for range gate.StableTicks + 2 {
		rec.tick()
	}
	rec.welcome(proto.FeatureImage | proto.FeatureSwitches | proto.FeatureCombat)
	rec.world(save.Image{})
	rec.tick()
	rec.hub.take()
	placed := fakegame.PlaceEnemies(es...)
	rec.bm.setActors(placed)
	rec.load(drcKeys)
	rec.enter(1, 2)
	rr := &relayRig{refreshRig: &refreshRig{recRig: rec, es: placed}, clock: clock}
	if rel {
		rr.linkREL(inject.RELBuildID)
	}
	if n := rr.ticks(gate.StableTicks + 4); n != 0 {
		t.Fatalf("%d bytes written before any bit came", n)
	}
	return rr
}

// linkREL writes the REL module's words for a module of build id.
func (rr *relayRig) linkREL(id uint32) {
	var b [8]byte
	binary.BigEndian.PutUint32(b[:], mailbox.RELMagic)
	binary.BigEndian.PutUint32(b[mailbox.OffRELBuildID-mailbox.OffRELMagic:], id)
	rr.store(mailbox.Mailbox+mailbox.OffRELMagic, b[:])
}

// request reads the Go-written half of the block.
func (rr *relayRig) request() mailbox.RefreshRequest {
	b := rr.read(mailbox.RefreshAddr, mailbox.RefreshGoSpan)
	be := binary.BigEndian
	return mailbox.RefreshRequest{Seq: be.Uint32(b), Proc: be.Uint16(b[mailbox.RefreshProcOff:]), PID: be.Uint32(b[mailbox.RefreshPIDOff:]),
		Params: be.Uint32(b[mailbox.RefreshParamsOff:]), Room: int8(b[mailbox.RefreshRoomOff]), BitKind: b[mailbox.RefreshBitKindOff],
		BitNo: be.Uint16(b[mailbox.RefreshBitNoOff:])}
}

// tag reads the request's tag.
func (rr *relayRig) tag() uint16 {
	return binary.BigEndian.Uint16(rr.read(mailbox.RefreshAddr+mailbox.RefreshTagOff, 2))
}

// rel acks seq with result r and gate g, as refresh.c does.
func (rr *relayRig) rel(seq uint32, r, g uint8) {
	var b [8]byte
	binary.BigEndian.PutUint32(b[:], seq)
	b[4], b[5] = r, g
	rr.store(mailbox.RefreshAddr+mailbox.RefreshAckOff, b[:])
}

// retry moves the clock past refreshRetry and ticks.
func (rr *relayRig) retry() {
	rr.clock.advance(refreshRetry)
	rr.ticks(1)
}

// TestParseRefresh: the token set, dry, none and the default; dry with no
// proc is refused (it would leave the relay off and log nothing).
func TestParseRefresh(t *testing.T) {
	var all []uint16
	for _, p := range refreshProcs {
		if p.pending == "" {
			all = append(all, p.proc)
		}
	}
	for _, c := range []struct {
		v     string
		procs []uint16
		dry   bool
		err   bool
	}{
		{"", nil, false, false},
		{"none", nil, false, false},
		{RefreshTokens, all, false, false},
		{"stone2,leaves,item,tbox,saku", []uint16{mailbox.RefreshProcStone2, mailbox.RefreshProcLeaves, mailbox.RefreshProcItem, mailbox.RefreshProcTbox, mailbox.RefreshProcSaku}, false, false},
		{" Stone2 ,dry", []uint16{mailbox.RefreshProcStone2}, true, false},
		{"swhit0,WARPT,vmc", []uint16{mailbox.RefreshProcSwhit0, mailbox.RefreshProcWarpt, mailbox.RefreshProcVmc}, false, false},
		{"tbox,door10", nil, false, true}, // pending ST1
		{"door12,dry", nil, false, true},
		{"dry", nil, false, true},
		{" DRY ", nil, false, true},
		{"stone2,door", nil, false, true},
	} {
		s, err := ParseRefresh(c.v)
		if (err != nil) != c.err {
			t.Fatalf("%q: %v", c.v, err)
		}
		if c.err {
			continue
		}
		if len(s.procs) != len(c.procs) || s.dry != c.dry || s.on() != (len(c.procs) > 0) {
			t.Fatalf("%q: %+v", c.v, s)
		}
		for _, p := range c.procs {
			if !s.procs[p] {
				t.Fatalf("%q lacks 0x%04X", c.v, p)
			}
		}
	}
	if _, err := New(Config{Mem: mem.NewFakeMem(), Refresh: "chest"}); err == nil {
		t.Fatal("New took an unknown --coop-refresh token")
	}
}

// TestRefreshRelayStone2: the partner's switch lands here while this
// game's boulder of it stands: the replica asks the REL to delete it,
// naming its pid, params, home room and bit (body first, the seq last);
// "gated" and "none" are asked again only after refreshRetry; "applied"
// ends the asking for that actor, logged once.
func TestRefreshRelayStone2(t *testing.T) {
	rr := newRelayRig(t, "stone2", -1, true, refreshStone2())
	rr.landSwitch(refreshSw)
	rr.ticks(2)
	q := rr.request()
	b, _ := rr.es.Of(0)
	want := mailbox.RefreshRequest{Seq: q.Seq, Proc: mailbox.RefreshProcStone2, PID: b.PID, Params: refreshStone2().Params, Room: 1,
		BitKind: mailbox.RefreshBitSw, BitNo: refreshSw}
	if q.Seq == 0 || q != want || rr.tag() != mailbox.RefreshTag {
		t.Fatalf("request %+v tag 0x%04X, want %+v; log:\n%s", q, rr.tag(), want, rr.log)
	}
	if st := rr.r.Status(); st.Refresh.Actors.Requests != 1 || st.Writes.Refreshes != 1 || st.Refresh.Actors.On != "stone2" {
		t.Fatalf("status %+v writes %+v", st.Refresh.Actors, st.Writes)
	}
	if n := len(rr.heapWrites()); n != 0 {
		t.Fatalf("%d heap writes: the relay writes only the request", n)
	}

	// Link near: gated, asked again after refreshRetry only.
	rr.rel(q.Seq, mailbox.RefreshRGate, mailbox.RefreshGateLinkNear)
	rr.ticks(2)
	if s := rr.request().Seq; s != q.Seq {
		t.Fatalf("asked again (%d) before refreshRetry", s)
	}
	rr.retry()
	s2 := rr.request().Seq
	if s2 != q.Seq+1 {
		t.Fatalf("after gated: seq %d, want %d; log:\n%s", s2, q.Seq+1, rr.log)
	}
	// Swallowed at a keeper relink: asked again too.
	rr.rel(s2, mailbox.RefreshRNone, 0)
	rr.ticks(1)
	rr.retry()
	s3 := rr.request().Seq
	if s3 != s2+1 {
		t.Fatalf("after none: seq %d, want %d", s3, s2+1)
	}
	// Applied: never asked again.
	rr.rel(s3, mailbox.RefreshRApplied, 0)
	rr.ticks(1)
	for range 4 {
		rr.retry()
	}
	if s := rr.request().Seq; s != s3 {
		t.Fatalf("asked again (%d) after applied", s)
	}
	st := rr.r.Status().Refresh
	if st.Actors.Applied != 1 || st.Actors.Result != "applied" || st.Actors.Requests != 3 {
		t.Fatalf("status %+v", st.Actors)
	}
	if st.ByKind[LandSwitch] != 1 {
		t.Errorf("landed by kind %v", st.ByKind)
	}
	if n := rr.log.count("asking the REL to delete the stale Stone2"); n != 1 {
		t.Errorf("ask logged %d times:\n%s", n, rr.log)
	}
	if n := rr.log.count(": gated for the stale Stone2"); n != 1 || !rr.log.has("Link near") {
		t.Errorf("gated logged %d times:\n%s", n, rr.log)
	}
}

// TestRefreshRelayFinal: each final answer ends the asking for its actor
// (bit not set, mismatch, refused, fingerprint miss, already in its set
// state).
func TestRefreshRelayFinal(t *testing.T) {
	for _, r := range []uint8{mailbox.RefreshRNotSet, mailbox.RefreshRMismatch, mailbox.RefreshRRefused, mailbox.RefreshRFpMiss, mailbox.RefreshRInState} {
		t.Run(mailbox.RefreshResultName(r), func(t *testing.T) {
			rr := newRelayRig(t, "stone2", -1, true, refreshStone2())
			rr.landSwitch(refreshSw)
			rr.ticks(2)
			s := rr.request().Seq
			rr.rel(s, r, 0)
			rr.ticks(1)
			for range 3 {
				rr.retry()
			}
			if n := rr.request().Seq; n != s {
				t.Fatalf("asked again (%d) after %s", n, mailbox.RefreshResultName(r))
			}
			if st := rr.r.Status().Refresh.Actors; st.Applied != 0 || st.Result != mailbox.RefreshResultName(r) {
				t.Fatalf("status %+v", st)
			}
		})
	}
}

// refreshSwhit0 is an untimed crystal switch of room 1 whose switch is
// refreshSw (getSwNo (u8)params, getTimer (u8)(params >> 20) 0xFF = 0,
// d_a_swhit0.cpp:73-96).
func refreshSwhit0() fakegame.Enemy {
	return fakegame.Enemy{Stage: drc.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.Proc(mailbox.RefreshProcSwhit0),
		Params: 0x0FF0FF00 | refreshSw, Pos: far}
}

// TestRefreshRelayRecreate: a row whose create builds its set state (the
// crystal switch) is asked for by its token, logged as a re-create, and a
// RECREATED answer ends the asking for that pid, counts as applied and
// re-created, and logs the new pid; CREATE_FAILED counts as applied only.
func TestRefreshRelayRecreate(t *testing.T) {
	for _, c := range []struct {
		r         uint8
		recreated int
		line      string
	}{
		{mailbox.RefreshRRecreated, 1, "new pid 4660"},
		{mailbox.RefreshRCreateFailed, 0, "gone until its room reloads"},
	} {
		t.Run(mailbox.RefreshResultName(c.r), func(t *testing.T) {
			rr := newRelayRig(t, "swhit0", -1, true, refreshSwhit0())
			rr.landSwitch(refreshSw)
			rr.ticks(2)
			q := rr.request()
			b, _ := rr.es.Of(0)
			want := mailbox.RefreshRequest{Seq: q.Seq, Proc: mailbox.RefreshProcSwhit0, PID: b.PID, Params: refreshSwhit0().Params, Room: 1,
				BitKind: mailbox.RefreshBitSw, BitNo: refreshSw}
			if q.Seq == 0 || q != want {
				t.Fatalf("request %+v, want %+v; log:\n%s", q, want, rr.log)
			}
			if n := rr.log.count("asking the REL to re-create the stale Swhit0"); n != 1 {
				t.Fatalf("ask logged %d times:\n%s", n, rr.log)
			}
			var pid [4]byte
			binary.BigEndian.PutUint32(pid[:], 0x1234)
			rr.store(mailbox.RefreshAddr+mailbox.RefreshNewPIDOff, pid[:])
			rr.rel(q.Seq, c.r, 0)
			rr.ticks(1)
			for range 4 {
				rr.retry()
			}
			if s := rr.request().Seq; s != q.Seq {
				t.Fatalf("asked again (%d) after %s", s, mailbox.RefreshResultName(c.r))
			}
			st := rr.r.Status().Refresh.Actors
			if st.Applied != 1 || st.Recreated != c.recreated || st.Result != mailbox.RefreshResultName(c.r) {
				t.Fatalf("status %+v", st)
			}
			if !rr.log.has(c.line) {
				t.Fatalf("no %q:\n%s", c.line, rr.log)
			}
		})
	}
}

// TestRefreshRowDecode: each row's switch as refresh.c rf_derive_row
// derives it, and what the REL refuses is no candidate (a timed crystal
// switch, a warp jar of type 1 or 2..4 or with a zone switch, a DOOR12 of
// arg1 8, no switch); the key-door rows are no candidates while pending.
func TestRefreshRowDecode(t *testing.T) {
	m := mem.NewFakeMem()
	for _, c := range []struct {
		name   string
		a      actors.Actor
		ok     bool
		no     uint16
		redo   bool
		doorRm bool
	}{
		{"wall", actors.Actor{Proc: int16(mailbox.RefreshProcWall), Params: 0x0000012A, HomeRoom: 3}, true, 0x2A, false, false},
		{"wall none", actors.Actor{Proc: int16(mailbox.RefreshProcWall), Params: 0x000001FF, HomeRoom: 3}, false, 0, false, false},
		{"mkie", actors.Actor{Proc: int16(mailbox.RefreshProcMkie), Params: 0x00451000, HomeRoom: 3}, true, 0x45, false, false},
		{"vmc", actors.Actor{Proc: int16(mailbox.RefreshProcVmc), Params: 0xFFFF33FF, HomeRoom: 3}, true, 0x33, true, false},
		{"mflft", actors.Actor{Proc: int16(mailbox.RefreshProcMflft), Params: 0x21FFFF01, HomeRoom: 3}, true, 0x21, true, false},
		{"kokiie", actors.Actor{Proc: int16(mailbox.RefreshProcKokiie), Params: 0x2FFFFF00, HomeRoom: 5}, true, 0x2F, true, false},
		{"swhit0 untimed 0", actors.Actor{Proc: int16(mailbox.RefreshProcSwhit0), Params: 0x00000010, HomeRoom: 3}, true, 0x10, true, false},
		{"swhit0 timed", actors.Actor{Proc: int16(mailbox.RefreshProcSwhit0), Params: 0x00500010, HomeRoom: 3}, false, 0, false, false},
		{"warpt", actors.Actor{Proc: int16(mailbox.RefreshProcWarpt), Params: 0x00000000, HomeAngle: [3]int16{0x0718, 0, 0}, HomeRoom: 3}, true, 0x18, true, false},
		{"warpt type 1", actors.Actor{Proc: int16(mailbox.RefreshProcWarpt), Params: 0x00000001, HomeAngle: [3]int16{0x0718, 0, 0}, HomeRoom: 3}, false, 0, false, false},
		{"warpt sp", actors.Actor{Proc: int16(mailbox.RefreshProcWarpt), Params: 0x00000003, HomeAngle: [3]int16{0x0718, 0, 0}, HomeRoom: 3}, false, 0, false, false},
		{"warpt zone", actors.Actor{Proc: int16(mailbox.RefreshProcWarpt), Params: 0x00000000, HomeAngle: [3]int16{0x07C5, 0, 0}, HomeRoom: 3}, false, 0, false, false},
		{"door10 pending", actors.Actor{Proc: int16(mailbox.RefreshProcDoor10), Params: 0x00000020, HomeRoom: -1}, false, 0, false, false},
		{"tbox", actors.Actor{Proc: int16(mailbox.RefreshProcTbox), Params: 3 << mailbox.RefreshTboxBitShift, HomeRoom: 3}, true, 3, true, false},
		{"stone2", actors.Actor{Proc: int16(mailbox.RefreshProcStone2), Params: 0x00001100, HomeRoom: 3}, true, 0x11, false, false},
	} {
		got, ok, err := refreshDecode(m, &c.a, 0)
		if err != nil || ok != c.ok {
			t.Fatalf("%s: ok %v err %v, want %v", c.name, ok, err, c.ok)
		}
		if !ok {
			continue
		}
		if got.bitKind != mailbox.RefreshBitSw && got.bitKind != mailbox.RefreshBitTbox || got.bitNo != c.no || got.recreate != c.redo || got.doorRooms != c.doorRm {
			t.Fatalf("%s: %+v, want bit 0x%02X recreate %v", c.name, got, c.no, c.redo)
		}
		if v := got.verb(); (v == "re-create") != c.redo {
			t.Fatalf("%s: verb %q", c.name, v)
		}
	}
	// The door rows decode once the define is flipped (refreshRowBit):
	// memory switches only, DOOR12's arg1 8 refused.
	d10 := refreshProcOf(mailbox.RefreshProcDoor10)
	if no, ok := refreshRowBit(d10, &actors.Actor{Proc: int16(mailbox.RefreshProcDoor10), Params: 0x20}); !ok || no != 0x20 {
		t.Fatalf("door10 sw 0x20: %d %v", no, ok)
	}
	if _, ok := refreshRowBit(d10, &actors.Actor{Proc: int16(mailbox.RefreshProcDoor10), Params: 0x85}); ok {
		t.Fatal("door10 took a dan switch")
	}
	d12 := refreshProcOf(mailbox.RefreshProcDoor12)
	if _, ok := refreshRowBit(d12, &actors.Actor{Proc: int16(mailbox.RefreshProcDoor12), Params: 0x20, HomeAngle: [3]int16{0, 0, 0x0800}}); ok {
		t.Fatal("door12 took arg1 8")
	}
	if mailbox.RefreshDoorRows != 0 || d10.pending == "" || d12.pending == "" || strings.Contains(RefreshTokens, "door1") {
		t.Fatal("the key-door rows are on while ST1 is pending")
	}
}

// TestRefreshRelayGateBackoff: an actor the REL keeps answering GATE is
// asked again after refreshRetry, then twice as long each time up to
// refreshRetry << refreshGateBackoff, where it stays; a NONE between waits
// the flat refreshRetry.
func TestRefreshRelayGateBackoff(t *testing.T) {
	rr := newRelayRig(t, "stone2", -1, true, refreshStone2())
	rr.landSwitch(refreshSw)
	rr.ticks(2)
	s := rr.request().Seq
	if s == 0 {
		t.Fatalf("no request:\n%s", rr.log)
	}
	for i := range refreshGateBackoff + 2 {
		rr.rel(s, mailbox.RefreshRGate, mailbox.RefreshGateProc)
		rr.ticks(1)
		wait := refreshRetry << min(i, refreshGateBackoff)
		rr.clock.advance(wait - time.Millisecond)
		rr.ticks(1)
		if n := rr.request().Seq; n != s {
			t.Fatalf("gate %d: asked again (%d) before %v", i, n, wait)
		}
		rr.clock.advance(time.Millisecond)
		rr.ticks(1)
		if n := rr.request().Seq; n != s+1 {
			t.Fatalf("gate %d: seq %d after %v, want %d", i, n, wait, s+1)
		}
		s++
	}
	rr.rel(s, mailbox.RefreshRNone, 0)
	rr.ticks(1)
	rr.retry()
	if n := rr.request().Seq; n != s+1 {
		t.Fatalf("after none: seq %d, want %d (the flat refreshRetry)", n, s+1)
	}
	if n := rr.log.count(": gated for the stale Stone2"); n != 1 {
		t.Errorf("gated logged %d times:\n%s", n, rr.log)
	}
}

// TestRefreshRelayAckTimeout: a request left unacked is given up after
// refreshAckWait and asked again after refreshRetry, logged once however
// often it times out.
func TestRefreshRelayAckTimeout(t *testing.T) {
	rr := newRelayRig(t, "stone2", -1, true, refreshStone2())
	rr.landSwitch(refreshSw)
	rr.ticks(2)
	s := rr.request().Seq
	rr.clock.advance(refreshAckWait / 2)
	rr.ticks(1)
	if n := rr.request().Seq; n != s {
		t.Fatalf("asked again (%d) before refreshAckWait", n)
	}
	for i := range 3 {
		rr.clock.advance(refreshAckWait)
		rr.ticks(1)
		rr.retry()
		if n := rr.request().Seq; n != s+uint32(i)+1 {
			t.Fatalf("timeout %d: seq %d, want %d", i, n, s+uint32(i)+1)
		}
	}
	if n := rr.log.count("not acked after"); n != 1 {
		t.Fatalf("not acked logged %d times:\n%s", n, rr.log)
	}
}

// TestRefreshRelayNoREL: without the REL module, or with one of another
// build, nothing is written; the log says why once.
func TestRefreshRelayNoREL(t *testing.T) {
	rr := newRelayRig(t, "stone2", -1, false, refreshStone2())
	rr.landSwitch(refreshSw)
	rr.ticks(2)
	rr.retry()
	if s := rr.request().Seq; s != 0 {
		t.Fatalf("a request without the REL: seq %d", s)
	}
	if n := rr.log.count("actor_refresh: the REL module is not linked"); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, rr.log)
	}
	rr.linkREL(inject.RELBuildID ^ 1)
	rr.retry()
	rr.retry()
	if s := rr.request().Seq; s != 0 {
		t.Fatalf("a request to another REL build: seq %d", s)
	}
	if n := rr.log.count(fmt.Sprintf("is build %08X, this build's is %08X; stale actors", inject.RELBuildID^1, inject.RELBuildID)); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, rr.log)
	}
	rr.linkREL(inject.RELBuildID)
	rr.retry()
	if s := rr.request().Seq; s == 0 {
		t.Fatalf("no request once the REL matches:\n%s", rr.log)
	}
}

// TestRefreshRelayOff: --coop-refresh empty (the default), or without the
// proc's token, asks nothing however the bit landed; its dry token logs
// one line per candidate and writes nothing.
func TestRefreshRelayOff(t *testing.T) {
	for _, tokens := range []string{"", "none", "leaves,item,tbox,saku"} {
		rr := newRelayRig(t, tokens, -1, true, refreshStone2())
		rr.landSwitch(refreshSw)
		rr.ticks(4)
		if s := rr.request().Seq; s != 0 {
			t.Fatalf("%q: a request: seq %d", tokens, s)
		}
		if st := rr.r.Status(); st.Writes.Refreshes != 0 || (tokens == "" && st.Refresh.Actors.On != "") {
			t.Fatalf("%q: status %+v", tokens, st.Refresh.Actors)
		}
	}
	rr := newRelayRig(t, "stone2,dry", -1, true, refreshStone2())
	rr.landSwitch(refreshSw)
	rr.ticks(2)
	rr.retry()
	rr.retry()
	if s := rr.request().Seq; s != 0 || rr.r.Status().Writes.Refreshes != 0 {
		t.Fatalf("the dry run wrote a request: seq %d", s)
	}
	if n := rr.log.count("actor_refresh dry run: would ask the REL to delete the stale Stone2"); n != 1 {
		t.Fatalf("dry run logged %d times:\n%s", n, rr.log)
	}
	if st := rr.r.Status().Refresh.Actors; !st.Dry || st.Candidates != 1 {
		t.Fatalf("status %+v", st)
	}
}

// TestRefreshRelayLocalBit: a boulder whose switch this game's own player
// set (no write of the replica's landed it) never gets a request.
func TestRefreshRelayLocalBit(t *testing.T) {
	rr := newRelayRig(t, "stone2", -1, true, refreshStone2())
	rr.onSwitch(refreshSw, -1)
	rr.enter(2, 1)
	rr.ticks(gate.StableTicks + 6)
	rr.retry()
	if s := rr.request().Seq; s != 0 {
		t.Fatalf("a request for the local player's own bit: seq %d", s)
	}
	if _, ok := rr.r.writer.Landed().Has(SwitchKey(addr.TableDRC, -1, refreshSw)); ok {
		t.Fatal("the local bit landed")
	}
}

// TestRefreshRelayHeld: the Go-side gates hold the candidate, each with
// why in /status: the settle time, a mirror copy, an event.
func TestRefreshRelayHeld(t *testing.T) {
	for _, c := range []struct {
		name   string
		settle time.Duration
		set    func(rr *relayRig)
		why    string
	}{
		{"settling", time.Hour, func(*relayRig) {}, "settling"},
		{"a mirror copy", -1, func(rr *relayRig) {
			b, _ := rr.es.Of(0)
			rr.store(b.Addr+addr.ActorOffPauseFlag, []byte{1})
		}, "paused"},
		{"an event ordered it", -1, func(rr *relayRig) {
			b, _ := rr.es.Of(0)
			rr.store(b.Addr+addr.ActorOffEventCommand, []byte{0, 3})
		}, "an event ordered it"},
		{"Link in a demo", -1, func(rr *relayRig) { rr.g.SetLinkDemo(2) }, "link demo 2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newRelayRig(t, "stone2", c.settle, true, refreshStone2())
			c.set(rr)
			rr.landSwitch(refreshSw)
			rr.ticks(3)
			if s := rr.request().Seq; s != 0 {
				t.Fatalf("a request: seq %d", s)
			}
			if st := rr.r.Status().Refresh.Actors; st.Held != 1 || !strings.Contains(st.LastHeld, c.why) {
				t.Fatalf("status %+v, want a hold for %q", st, c.why)
			}
		})
	}
}

// TestRefreshRelayStageHop: a stage change while a request waits for its
// ack drops it (logged), and the new visit asks nothing: its landed set is
// empty.
func TestRefreshRelayStageHop(t *testing.T) {
	rr := newRelayRig(t, "stone2", -1, true, refreshStone2())
	rr.landSwitch(refreshSw)
	rr.ticks(2)
	s := rr.request().Seq
	if s == 0 {
		t.Fatalf("no request:\n%s", rr.log)
	}
	rr.load(woods)
	rr.ticks(gate.StableTicks + 2)
	if !rr.log.has(fmt.Sprintf("actor_refresh %d: the stage visit ended before its ack", s)) {
		t.Fatalf("the dropped request not logged:\n%s", rr.log)
	}
	rr.rel(s, mailbox.RefreshRGate, mailbox.RefreshGateStageChange)
	rr.retry()
	rr.retry()
	if n := rr.request().Seq; n != s {
		t.Fatalf("asked again (%d) in the new stage", n)
	}
}

// TestRefreshDecode: each proc's bit as the REL derives it (wwrefresh.h),
// and the allow table's refusals: Tbox funcType 6..8, daItem bit 0x7F, a
// Blue Jelly or a bit the landed set cannot hold, a zone switch with no
// home room, a Saku half with no switch, and any proc off the allow-list.
func TestRefreshDecode(t *testing.T) {
	m := mem.NewFakeMem()
	const at = 0x80B00000
	items := uint32(0)
	item := func(bit uint32, no byte) *actors.Actor { // each its own block
		items++
		a := at + items*0x1000
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], bit)
		if err := m.Write(a+mailbox.RefreshItemBitNoOff, b[:]); err != nil {
			t.Fatal(err)
		}
		if err := m.Write(a+mailbox.RefreshItemNoOff, []byte{no}); err != nil {
			t.Fatal(err)
		}
		return &actors.Actor{Addr: a, Proc: int16(mailbox.RefreshProcItem), HomeRoom: 2}
	}
	act := func(proc uint16, params uint32, room int8) *actors.Actor {
		return &actors.Actor{Addr: at, Proc: int16(proc), Params: params, HomeRoom: room}
	}
	// A chest of the stage file, as every DRC chest is (P0-RF: room -1),
	// in room angleX & 0x3F (d_a_tbox.cpp:1241).
	stageTbox := func(params uint32, angleX int16) *actors.Actor {
		a := act(mailbox.RefreshProcTbox, params, -1)
		a.HomeAngle[0] = angleX
		return a
	}
	const tb = addr.TableDRC
	sw := func(no int, room int) []LandedBit { return []LandedBit{SwitchKey(tb, room, no)} }
	for _, c := range []struct {
		name string
		a    *actors.Actor
		ok   bool
		kind uint8
		no   uint16
		keys []LandedBit
		room int // refreshCand.room; 99 is the actor's home room
	}{
		{"Stone2 sw 0x11", act(mailbox.RefreshProcStone2, 0xFFFF11FF, 2), true, mailbox.RefreshBitSw, 0x11, sw(0x11, -1), 99},
		{"Stone2 no switch", act(mailbox.RefreshProcStone2, 0xFFFFFFFF, 2), false, 0, 0, nil, 99},
		{"Stone2 zone sw, room 2", act(mailbox.RefreshProcStone2, 0x0000C500, 2), true, mailbox.RefreshBitSw, 0xC5, sw(0xC5, 2), 99},
		{"Stone2 zone sw, no room", act(mailbox.RefreshProcStone2, 0x0000C500, -1), false, 0, 0, nil, 99},
		{"Leaves sw 0x2A", act(mailbox.RefreshProcLeaves, 0x2A<<13, 1), true, mailbox.RefreshBitSw, 0x2A, sw(0x2A, -1), 99},
		{"Tbox bit 9 type 0", act(mailbox.RefreshProcTbox, 9<<7, 6), true, mailbox.RefreshBitTbox, 9, []LandedBit{{LandTbox, tb, -1, 9}}, 99},
		{"Tbox type 6", act(mailbox.RefreshProcTbox, 9<<7|6, 6), false, 0, 0, nil, 99},
		{"Tbox type 7", act(mailbox.RefreshProcTbox, 9<<7|7, 6), false, 0, 0, nil, 99},
		{"Tbox type 8", act(mailbox.RefreshProcTbox, 9<<7|8, 6), false, 0, 0, nil, 99},
		{"Tbox type 9", act(mailbox.RefreshProcTbox, 3<<7|9, 6), true, mailbox.RefreshBitTbox, 3, []LandedBit{{LandTbox, tb, -1, 3}}, 99},
		{"Tbox of the stage file in room 2", stageTbox(9<<7, 0x40|2), true, mailbox.RefreshBitTbox, 9, []LandedBit{{LandTbox, tb, -1, 9}}, 2},
		{"Tbox of the stage file, no room", stageTbox(9<<7, 0x3F), true, mailbox.RefreshBitTbox, 9, []LandedBit{{LandTbox, tb, -1, 9}}, -1},
		{"item bit 4", item(0x84, 0x01), true, mailbox.RefreshBitItem, 4, []LandedBit{{LandItem, tb, -1, 4}}, 99},
		{"item no bit", item(0x7F, 0x01), false, 0, 0, nil, 99},
		{"item a Blue Jelly", item(4, mailbox.RefreshItemNoBlueJelly), false, 0, 0, nil, 99},
		{"item bit 40", item(40, 0x01), false, 0, 0, nil, 99},
		{"Saku one half", act(mailbox.RefreshProcSaku, 0x00FF1200, 3), true, mailbox.RefreshBitSw, 0x12, sw(0x12, -1), 99},
		{"Saku two halves", act(mailbox.RefreshProcSaku, 0x00131201, 3), true, mailbox.RefreshBitSw, 0x12, append(sw(0x12, -1), sw(0x13, -1)...), 99},
		{"Saku half 1 without a switch", act(mailbox.RefreshProcSaku, 0x00FF1201, 3), false, 0, 0, nil, 99},
		{"Saku half 0 without a switch", act(mailbox.RefreshProcSaku, 0x0013FF01, 3), false, 0, 0, nil, 99},
		{"Link", act(0x00A9, 0, 0), false, 0, 0, nil, 99},
		{"Gohdan", act(mailbox.BossProcBst, 0, 0), false, 0, 0, nil, 99},
		{"a door", act(uint16(stage.ProcDoor10), refreshDoorParams, -1), false, 0, 0, nil, 99},
	} {
		got, ok, err := refreshDecode(m, c.a, tb)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		room := c.room
		if room == 99 {
			room = int(c.a.HomeRoom)
		}
		if ok != c.ok || ok && (got.bitKind != c.kind || got.bitNo != c.no || fmt.Sprint(got.keys) != fmt.Sprint(c.keys) || got.room != room) {
			t.Errorf("%s: ok %v kind %d no 0x%X keys %v room %d, want %v %d 0x%X %v %d", c.name, ok, got.bitKind, got.bitNo, got.keys, got.room,
				c.ok, c.kind, c.no, c.keys, room)
		}
	}
}

// TestRefreshLandedWhy: a chest and a pickup bit judged by the landed set
// and the snapshot's live memBit: not landed (no candidate), landed and
// set (taken), landed but clear, settling, in a boss stage.
func TestRefreshLandedWhy(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tbox := LandedBit{LandTbox, addr.TableDRC, -1, 9}
	item := LandedBit{LandItem, addr.TableDRC, -1, 4}
	s := &state.Snapshot{StageName: drc.Name, Table: addr.TableDRC}
	tick := func(set bool, at time.Time, boss bool) *refreshTick {
		*s = state.Snapshot{StageName: drc.Name, Table: addr.TableDRC}
		if set {
			for _, b := range []func() (addr.Bit, error){func() (addr.Bit, error) { return addr.TboxBit(9) }, func() (addr.Bit, error) { return addr.MemBitItemBit(4) }} {
				bit, err := b()
				if err != nil {
					t.Fatal(err)
				}
				setSnapByte(t, s, addr.LiveMemBit+uint32(bit.Off), bit.Mask)
			}
		}
		return &refreshTick{landed: Landed{Stage: drc.Name, Table: addr.TableDRC, At: map[LandedBit]time.Time{tbox: at, item: at}},
			settle: time.Second, now: now, s: s, boss: boss}
	}
	for _, b := range []LandedBit{tbox, item} {
		if why, cand := tick(true, now.Add(-2*time.Second), false).landedWhy(b, 1); !cand || why != "" {
			t.Errorf("%s set and settled: %q %v", b, why, cand)
		}
		if why, _ := tick(false, now.Add(-2*time.Second), false).landedWhy(b, 1); !strings.Contains(why, "reads clear") {
			t.Errorf("%s clear: %q", b, why)
		}
		if why, _ := tick(true, now, false).landedWhy(b, 1); !strings.Contains(why, "settling") {
			t.Errorf("%s just landed: %q", b, why)
		}
		if why, _ := tick(true, now.Add(-2*time.Second), true).landedWhy(b, 1); !strings.Contains(why, "boss stage") {
			t.Errorf("%s in a boss stage: %q", b, why)
		}
	}
	if _, cand := tick(true, now, false).landedWhy(LandedBit{LandTbox, addr.TableDRC, -1, 10}, 1); cand {
		t.Error("a chest that never landed is a candidate")
	}
	on, _ := ParseRefresh("tbox")
	if !tick(true, now, false).anyRefresh(on) {
		t.Error("a landed chest does not read the walk for tbox")
	}
	on, _ = ParseRefresh("stone2,dry")
	if tick(true, now, false).anyRefresh(on) {
		t.Error("a landed chest reads the walk for stone2")
	}
}

// TestRefreshPlanStageTbox: a chest of the stage file (home room -1, as
// every DRC chest is: P0-RF) is planned on the room its create takes,
// home.angle.x & 0x3F (d_a_tbox.cpp:1241): a candidate while that room is
// loaded and settled, held while it is not, and with 0x3F (no room) on no
// room's status at all. The request still names home room -1.
func TestRefreshPlanStageTbox(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tbox := LandedBit{LandTbox, addr.TableDRC, -1, 9}
	s := &state.Snapshot{StageName: drc.Name, Table: addr.TableDRC}
	bit, err := addr.TboxBit(9)
	if err != nil {
		t.Fatal(err)
	}
	setSnapByte(t, s, addr.LiveMemBit+uint32(bit.Off), bit.Mask)
	on, err := ParseRefresh("tbox")
	if err != nil {
		t.Fatal(err)
	}
	plan := func(angleX int16, flags uint8, flagsOK bool) *Replica {
		t.Helper()
		r := &Replica{m: mem.NewFakeMem(), rfOn: on, ar: newActorRefresh(), m6: newM6()}
		rt := &refreshTick{landed: Landed{Stage: drc.Name, Table: addr.TableDRC, At: map[LandedBit]time.Time{tbox: now.Add(-2 * time.Second)}},
			settle: time.Second, now: now, s: s, flagsOK: flagsOK, linkOK: true}
		rt.flags[2] = flags
		w := actors.Walk{Actors: []actors.Actor{{PID: 77, Proc: int16(mailbox.RefreshProcTbox), Params: 9 << 7, HomeRoom: -1,
			HomeAngle: [3]int16{angleX, 0, 0}, InitState: mailbox.RefreshAcInitRunning, Pos: far}}}
		r.planRefresh(rt, &w, true, "")
		return r
	}
	r := plan(2, addr.RoomFlagLoaded, true)
	if len(r.ar.cands) != 1 || r.ar.cands[0].a.HomeRoom != -1 || r.ar.cands[0].room != 2 {
		t.Fatalf("room 2 loaded: cands %+v held %v", r.ar.cands, r.ar.held)
	}
	r = plan(2, addr.RoomFlagLoaded|addr.RoomFlagCreating, true)
	if len(r.ar.cands) != 0 || !strings.Contains(r.ar.held[77], "room 2 is not loaded and settled") {
		t.Fatalf("room 2 creating: cands %d held %v", len(r.ar.cands), r.ar.held)
	}
	r = plan(0x3F, 0, false)
	if len(r.ar.cands) != 1 || r.ar.cands[0].room != -1 {
		t.Fatalf("no room: cands %+v held %v", r.ar.cands, r.ar.held)
	}
}

// TestRefreshGuardRules: the allowlist takes the request's seq, its proc
// only as the allow-list's bytes, its tag exactly, a bit kind 1..3 and any
// pid, params, room and bit number, on pathRefresh alone; the REL's ack
// and result are no target, and a guard without TargetRefresh takes none.
func TestRefreshGuardRules(t *testing.T) {
	g := NewGuardFor(AllTargets)
	judge := func(a uint32, val byte) Refusal {
		t.Helper()
		_, br, ok := g.at(a)
		if !ok {
			return RefuseTarget
		}
		if br.target != TargetRefresh || br.paths != pathRefresh {
			t.Fatalf("0x%08X: target %s, paths %s", a, br.target, br.paths)
		}
		r, _ := judgeByte(br, 0, val)
		return r
	}
	for _, p := range refreshProcs {
		if p.pending != "" {
			continue
		}
		req := mailbox.RefreshRequest{Seq: 7, Proc: p.proc, PID: 0xDEADBEEF, Params: 0xFFFFFFFF, Room: -1, BitKind: mailbox.RefreshBitItem, BitNo: 0xFFFF}
		body, seq := req.Fields()
		for i, b := range body {
			if r := judge(mailbox.RefreshAddr+mailbox.RefreshProcOff+uint32(i), b); r != 0 {
				t.Fatalf("%s: body byte %d 0x%02X refused: %s", p.key, i, b, r)
			}
		}
		for i, b := range seq {
			if r := judge(mailbox.RefreshAddr+mailbox.RefreshSeqOff+uint32(i), b); r != 0 {
				t.Fatalf("%s: seq byte %d refused: %s", p.key, i, r)
			}
		}
	}
	at := mailbox.RefreshAddr
	for _, c := range []struct {
		name string
		off  uint32
		val  byte
	}{
		{"Link's proc low byte 0xA9", mailbox.RefreshProcOff + 1, 0xA9},
		{"the keeper's 0xB5", mailbox.RefreshProcOff + 1, 0xB5},
		{"Gohdan's 0xF2", mailbox.RefreshProcOff + 1, 0xF2},
		{"DOOR10's 0x2E (pending ST1)", mailbox.RefreshProcOff + 1, byte(mailbox.RefreshProcDoor10 & 0xFF)},
		{"DOOR12's 0x2F (pending ST1)", mailbox.RefreshProcOff + 1, byte(mailbox.RefreshProcDoor12 & 0xFF)},
		{"proc high byte 0x02", mailbox.RefreshProcOff, 0x02},
		{"another tag", mailbox.RefreshTagOff + 1, 'X'},
		{"bit kind 0", mailbox.RefreshBitKindOff, 0},
		{"bit kind 4", mailbox.RefreshBitKindOff, 4},
	} {
		if r := judge(at+c.off, c.val); r != RefuseValue {
			t.Errorf("%s: %v, want a value refusal", c.name, r)
		}
	}
	if g.Allows(at+mailbox.RefreshAckOff, 8) || g.Mask(at+mailbox.RefreshAckOff) != 0 || g.Mask(at+mailbox.RefreshNewPIDOff) != 0 {
		t.Fatal("the REL's ack, result or reserved word is in the allowlist")
	}
	if NewGuardFor(M3Targets|M4Targets|M5Targets|M6Targets|RefreshTargets|TargetEscape|TargetBoss).Allows(at, 4) {
		t.Fatal("the refresh request is in a guard without TargetRefresh")
	}
	if !strings.Contains(TargetRefresh.String(), "refresh") || AllTargets&TargetRefresh == 0 || !strings.Contains(pathRefresh.String(), "ApplyRefresh") {
		t.Errorf("names %s %s", TargetRefresh, pathRefresh)
	}
}

// setSnapByte ORs mask into s's copy of the byte at a (snapByte's regions).
func setSnapByte(t *testing.T, s *state.Snapshot, a uint32, mask byte) {
	t.Helper()
	for _, r := range []struct {
		reg addr.Region
		b   []byte
	}{{state.SaveRegion, s.Save[:]}, {state.StageRegion, s.Stage[:]}, {state.PlayerRegion, s.Player[:]}} {
		if r.reg.Contains(a, 1) {
			r.b[a-r.reg.Start] |= mask
			return
		}
	}
	t.Fatalf("0x%08X is in no snapshot region", a)
}
