package fxsync

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const testLink = 0x80800000

func u32(v uint32) []byte { b := make([]byte, 4); mem.PutU32BE(b, v); return b }

func f32(v float32) []byte { b := make([]byte, 4); mem.PutF32BE(b, v); return b }

// game is a FakeMem with the REL linked and Link in stage, room.
func game(t *testing.T, stage string, room int8) *mem.FakeMem {
	t.Helper()
	m := mem.NewFakeMem()
	st := make([]byte, 8)
	copy(st, stage)
	for _, w := range []struct {
		a uint32
		b []byte
	}{
		{mailbox.Mailbox + mailbox.OffRELMagic, u32(mailbox.RELMagic)},
		{addr.PlayerPtr, u32(testLink)},
		{addr.CurStageName, st},
		{addr.StayNo, []byte{byte(room)}},
		{testLink + offCurPos, append(append(f32(100), f32(0)...), f32(-200)...)},
	} {
		if err := m.Store(w.a, w.b); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func word(t *testing.T, m mem.Reader, off uint32) uint32 {
	t.Helper()
	v, err := mem.ReadU32(m, mailbox.FxAddr+off)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// relEmits is the REL writing n outbox events (fx.c fx_emit).
func relEmits(t *testing.T, m *mem.FakeMem, evs ...mailbox.FxEvent) {
	t.Helper()
	w := word(t, m, mailbox.FxOutWriteOff)
	for _, e := range evs {
		if err := m.Store(mailbox.FxOutEntry(w), e.Bytes()); err != nil {
			t.Fatal(err)
		}
		w++
	}
	if err := m.Store(mailbox.FxAddr+mailbox.FxOutWriteOff, u32(w)); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsFromEnv: on by default since v0.6.1 (unset, empty, on or
// any other value); off only for an off spelling, in any case, trimmed.
func TestSettingsFromEnv(t *testing.T) {
	for v, want := range map[string]bool{"": true, "on": true, "1": true, "TRUE": true, " yes ": true, "maybe": true,
		"off": false, "OFF": false, "0": false, "false": false, "False": false, "no": false, " no ": false} {
		t.Setenv("WW_PROJECTILES", v)
		if got := SettingsFromEnv().On; got != want {
			t.Errorf("WW_PROJECTILES=%q: on %v, want %v", v, got, want)
		}
	}
	os.Unsetenv("WW_PROJECTILES")
	if !SettingsFromEnv().On {
		t.Error("off with WW_PROJECTILES unset")
	}
}

// TestSenderDrain: the outbox's events go out in order, stamped with the
// stage and a seq that keeps counting; the read word follows; nothing is
// read without the REL; a refused send is lost, not replayed.
func TestSenderDrain(t *testing.T) {
	m := game(t, "M_NewD2", 3)
	var s Sender
	if err := s.Arm(m, true); err != nil || word(t, m, mailbox.FxSendMagicOff) != mailbox.FxMagic {
		t.Fatalf("arm: %v", err)
	}
	relEmits(t, m, mailbox.FxEvent{Kind: mailbox.FxKindBombThrow, Room: 3, ID: 11, Pos: [3]float32{1, 2, 3}, Timer: 90},
		mailbox.FxEvent{Kind: mailbox.FxKindExplode, Room: 3, ID: 11})
	var got []network.FxEvent
	send := func(e *network.FxEvent) error { got = append(got, *e); return nil }
	if n, err := s.Drain(m, send); err != nil || n != 2 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 || got[0].Stage != "M_NewD2" || got[0].Kind != mailbox.FxKindBombThrow ||
		got[0].ID != 11 || got[0].Timer != 90 || got[0].Pos[2] != 3 || got[1].Kind != mailbox.FxKindExplode {
		t.Fatalf("sent %+v", got)
	}
	if word(t, m, mailbox.FxOutReadOff) != 2 {
		t.Errorf("read word %d", word(t, m, mailbox.FxOutReadOff))
	}
	if n, _ := s.Drain(m, send); n != 0 {
		t.Errorf("drained %d again", n)
	}
	// Wire-valid: what the sender builds passes the server's check.
	b := got[0].Bytes()
	if _, err := network.ParseFx(b); err != nil {
		t.Errorf("the wire event fails ParseFx: %v", err)
	}

	relEmits(t, m, mailbox.FxEvent{Kind: mailbox.FxKindArrow, Variant: 1, ID: 12})
	refused := func(*network.FxEvent) error { return errors.New("not connected") }
	if n, _ := s.Drain(m, refused); n != 1 || s.Lost != 1 || word(t, m, mailbox.FxOutReadOff) != 3 {
		t.Errorf("refused: %d taken, %d lost, read %d", n, s.Lost, word(t, m, mailbox.FxOutReadOff))
	}
	relEmits(t, m, mailbox.FxEvent{Kind: mailbox.FxKindArrow, ID: 13})
	got = nil
	if n, _ := s.Drain(m, send); n != 1 || len(got) != 1 || got[0].Seq != 4 || got[0].ID != 13 {
		t.Errorf("after a refusal: %d, %+v", n, got)
	}

	if err := m.Store(mailbox.Mailbox+mailbox.OffRELMagic, u32(0)); err != nil {
		t.Fatal(err)
	}
	relEmits(t, m, mailbox.FxEvent{Kind: mailbox.FxKindArrow, ID: 14})
	if n, _ := s.Drain(m, send); n != 0 {
		t.Errorf("drained %d without the REL", n)
	}
	if err := s.Arm(m, false); err != nil || word(t, m, mailbox.FxSendMagicOff) != 0 {
		t.Errorf("disarm: %v", err)
	}
}

// TestSenderOverrun: a write word more than a ring ahead keeps the newest.
func TestSenderOverrun(t *testing.T) {
	m := game(t, "sea", 0)
	var s Sender
	for i := 0; i < mailbox.FxRing+3; i++ {
		relEmits(t, m, mailbox.FxEvent{Kind: mailbox.FxKindArrow, ID: uint32(i)})
	}
	var ids []uint32
	n, _ := s.Drain(m, func(e *network.FxEvent) error { ids = append(ids, e.ID); return nil })
	if n != mailbox.FxRing || ids[0] != 3 || ids[len(ids)-1] != mailbox.FxRing+2 {
		t.Errorf("overrun: %d taken, ids %v", n, ids)
	}
}

func TestAccept(t *testing.T) {
	here := Local{Ready: true, Stage: "M_NewD2", Room: 3}
	ev := network.FxEvent{Stage: "M_NewD2", Room: 3}
	sea := Local{Ready: true, Stage: "sea", Room: 44, X: 0, Z: 0}
	cases := []struct {
		l    Local
		e    network.FxEvent
		want string
	}{
		{here, ev, DropNone},
		{Local{Stage: "M_NewD2", Room: 3}, ev, DropNotReady},
		{here, network.FxEvent{Stage: "M_NewD2", Room: 4}, DropRoom},
		{here, network.FxEvent{Stage: "sea", Room: 3}, DropStage},
		{Local{Ready: true, Stage: "M_NewD2", Room: 3, MiniGame: true}, ev, DropMiniGame},
		{Local{Ready: true, Stage: "M_DragB"}, network.FxEvent{Stage: "M_DragB"}, DropBoss},
		{Local{Ready: true, Stage: "Xboss1"}, network.FxEvent{Stage: "Xboss1"}, DropBoss},
		{Local{Ready: true, Stage: "GanonK"}, network.FxEvent{Stage: "GanonK"}, DropBoss},
		// The sea: another square is fine, distance decides.
		{sea, network.FxEvent{Stage: "sea", Room: 45, Pos: [3]float32{SeaRange - 1, 0, 0}}, DropNone},
		{sea, network.FxEvent{Stage: "sea", Room: 44, Pos: [3]float32{SeaRange, 0, SeaRange}}, DropFar},
	}
	for i, c := range cases {
		if got := Accept(c.l, c.e); got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
}

func inbox(t *testing.T, m mem.Reader) []mailbox.FxEvent {
	t.Helper()
	var out []mailbox.FxEvent
	for n := word(t, m, mailbox.FxInReadOff); n != word(t, m, mailbox.FxInWriteOff); n++ {
		b, err := m.Read(mailbox.FxInEntry(n), mailbox.FxEvSize)
		if err != nil {
			t.Fatal(err)
		}
		e, _ := mailbox.ParseFxEvent(b)
		out = append(out, e)
	}
	return out
}

// TestReceiverFlush: events pass into the inbox with the sender's ID;
// repeats, the twin's, other rooms' and stale ones are dropped; a full
// inbox makes them wait; a new name on an ID starts its seq over.
func TestReceiverFlush(t *testing.T) {
	m := game(t, "M_NewD2", 3)
	r := NewReceiver()
	if err := r.Arm(m, true); err != nil || word(t, m, mailbox.FxRecvMagicOff) != mailbox.FxMagic {
		t.Fatalf("arm: %v", err)
	}
	remotes := []network.RemotePlayer{{ID: 2, Name: "bob"}, {ID: 5, Name: "me"}}
	now := time.Unix(100, 0)
	ev := func(seq uint32, room int8) network.FxEvent {
		return network.FxEvent{Seq: seq, Kind: mailbox.FxKindBombThrow, Room: room, Stage: "M_NewD2", ID: 40 + seq, Timer: 60}
	}
	r.Push(2, ev(1, 3), now)
	r.Push(2, ev(1, 3), now) // a repeat
	r.Push(5, ev(1, 3), now) // the twin
	r.Push(2, ev(2, 4), now) // another room
	r.Push(2, ev(3, 3), now.Add(-3*time.Second))
	if err := r.Flush(m, remotes, "me", now); err != nil {
		t.Fatal(err)
	}
	got := inbox(t, m)
	if len(got) != 1 || got[0].Slot != 2 || got[0].ID != 41 || got[0].Timer != 60 || got[0].Kind != mailbox.FxKindBombThrow {
		t.Fatalf("inbox %+v", got)
	}
	if r.Dropped["repeat"] != 1 || r.Dropped["self"] != 1 || r.Dropped[DropRoom] != 1 || r.Dropped["stale"] != 1 || r.Written != 1 {
		t.Errorf("counts %d written, dropped %v", r.Written, r.Dropped)
	}
	if r.Summary() == "" {
		t.Error("no summary")
	}

	// Fill the ring: the overflow waits for the REL.
	for i := uint32(0); i < mailbox.FxRing+2; i++ {
		r.Push(2, ev(10+i, 3), now)
	}
	if err := r.Flush(m, remotes, "me", now); err != nil {
		t.Fatal(err)
	}
	if n := len(inbox(t, m)); n != mailbox.FxRing {
		t.Fatalf("%d in the inbox, want a full ring %d", n, mailbox.FxRing)
	}
	if err := m.Store(mailbox.FxAddr+mailbox.FxInReadOff, u32(word(t, m, mailbox.FxInWriteOff))); err != nil { // the REL took them
		t.Fatal(err)
	}
	if err := r.Flush(m, remotes, "me", now); err != nil {
		t.Fatal(err)
	}
	if got := inbox(t, m); len(got) != 3 || got[0].ID != 40+10+mailbox.FxRing-1 {
		t.Errorf("after the REL drained: %+v", got)
	}

	// ID 2 now names someone else (a new process): its seq starts over.
	r.Push(2, ev(1, 3), now)
	if err := r.Flush(m, []network.RemotePlayer{{ID: 2, Name: "carol"}}, "me", now); err != nil {
		t.Fatal(err)
	}
	if got := inbox(t, m); got[len(got)-1].ID != 41 || r.Dropped["repeat"] != 1 {
		t.Errorf("a new name's seq 1 was not taken: %+v, %v", got, r.Dropped)
	}

	// Without the REL nothing is written and the queue empties.
	if err := m.Store(mailbox.Mailbox+mailbox.OffRELMagic, u32(0)); err != nil {
		t.Fatal(err)
	}
	before := word(t, m, mailbox.FxInWriteOff)
	r.Push(2, ev(50, 3), now)
	if err := r.Flush(m, remotes, "me", now); err != nil || word(t, m, mailbox.FxInWriteOff) != before || r.Dropped[DropNotReady] != 1 {
		t.Errorf("without the REL: %v, write %d, %v", err, word(t, m, mailbox.FxInWriteOff), r.Dropped)
	}
	if err := r.Arm(m, false); err != nil || word(t, m, mailbox.FxRecvMagicOff) != 0 {
		t.Errorf("disarm: %v", err)
	}
}

// TestReceiverSenderRestart: a sender that starts its seq over under the
// same ID and name is not dropped as a repeat once the server's epoch
// changed (the host restarted the app, and with it the server and its
// broadcast-pose), or once its ID left the remotes and came back; with
// the same epoch and the ID present throughout, a repeat still is one.
func TestReceiverSenderRestart(t *testing.T) {
	m := game(t, "M_NewD2", 3)
	r := NewReceiver()
	if err := r.Arm(m, true); err != nil {
		t.Fatal(err)
	}
	host := []network.RemotePlayer{{ID: 1, Name: "host"}}
	now := time.Unix(100, 0)
	ev := func(seq uint32) network.FxEvent {
		return network.FxEvent{Seq: seq, Kind: mailbox.FxKindBombThrow, Room: 3, Stage: "M_NewD2", ID: 100 + seq, Timer: 60}
	}
	flush := func(remotes []network.RemotePlayer) {
		t.Helper()
		if err := r.Flush(m, remotes, "me", now); err != nil {
			t.Fatal(err)
		}
		// The REL takes every event at once.
		if err := m.Store(mailbox.FxAddr+mailbox.FxInReadOff, u32(word(t, m, mailbox.FxInWriteOff))); err != nil {
			t.Fatal(err)
		}
	}

	run := func(from, to uint32) { // in ring-sized batches
		t.Helper()
		for seq := from; seq <= to; seq++ {
			r.Push(1, ev(seq), now)
			if (seq-from+1)%mailbox.FxRing == 0 || seq == to {
				flush(host)
			}
		}
	}

	r.SetEpoch(7)
	run(1, 30)
	if r.Written != 30 {
		t.Fatalf("%d written, want 30", r.Written)
	}

	// (c) same epoch, ID 1 present throughout: seq 5 again is a repeat.
	r.Push(1, ev(5), now)
	flush(host)
	if r.Written != 30 || r.Dropped["repeat"] != 1 {
		t.Fatalf("a repeat in the same session: %d written, %v", r.Written, r.Dropped)
	}

	// (a) the server restarted (a new epoch): ID 1 is "host" again and
	// its seq starts at 1.
	r.SetEpoch(8)
	r.Push(1, ev(1), now)
	flush(host)
	if r.Written != 31 || r.Dropped["repeat"] != 1 {
		t.Errorf("after a new epoch: %d written, %v (the restarted sender's seq 1 was dropped)", r.Written, r.Dropped)
	}
	r.SetEpoch(8) // the same epoch again forgets nothing
	r.Push(1, ev(1), now)
	flush(host)
	if r.Dropped["repeat"] != 2 {
		t.Errorf("the same epoch again forgot the seq: %v", r.Dropped)
	}

	// (b) same epoch: ID 1 leaves the remotes for a Flush with nothing
	// queued, then comes back under the same name counting from 1.
	run(2, 20)
	flush(nil)
	written := r.Written
	r.Push(1, ev(1), now)
	flush(host)
	if r.Written != written+1 || r.Dropped["repeat"] != 2 {
		t.Errorf("a sender that left and came back: %d written (want %d), %v", r.Written, written+1, r.Dropped)
	}

	// A new epoch drops what was queued for the old one.
	r.Push(1, ev(2), now)
	r.SetEpoch(9)
	flush(host)
	if r.Written != written+1 {
		t.Errorf("an old epoch's queued event was written: %d", r.Written)
	}
}

// TestReceiverQueueCap: a flood past QueueMax is dropped at Push.
func TestReceiverQueueCap(t *testing.T) {
	r := NewReceiver()
	for i := 0; i < QueueMax+5; i++ {
		r.Push(1, network.FxEvent{Seq: uint32(i)}, time.Now())
	}
	if r.Dropped["queue full"] != 5 {
		t.Errorf("dropped %v", r.Dropped)
	}
}

// TestReadLocalStageChange: a stage change under way is not ready.
func TestReadLocalStageChange(t *testing.T) {
	m := game(t, "sea", 44)
	l, err := ReadLocal(m)
	if err != nil || !l.Ready || l.Stage != "sea" || l.Room != 44 || l.X != 100 || l.Z != -200 {
		t.Fatalf("%+v %v", l, err)
	}
	if err := m.Store(addr.NextStageEnable, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if l, _ := ReadLocal(m); l.Ready {
		t.Error("ready during a stage change")
	}
}

// TestMailboxWireRoundTrip: an outbox entry survives the wire into an
// inbox entry, with the sender's ID as its slot.
func TestMailboxWireRoundTrip(t *testing.T) {
	in := mailbox.FxEvent{Kind: mailbox.FxKindCannon, Room: -3, ID: 99, Pos: [3]float32{1, 2, 3}, SpeedF: 4, SpeedY: 5, Gravity: -6,
		Angle: [3]int16{7, 8, 9}, Timer: 10}
	wire := FromMailbox(in, "sea", 1)
	parsed, err := network.ParseFx(wire.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	out := ToMailbox(parsed, 4)
	in.Slot = 4
	if out != in {
		t.Errorf("%+v, want %+v", out, in)
	}
}
