package hub

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// A native fuzz target for the hub end to end: every 'O' payload a peer
// sends goes through wire.Decode and then the session rules, so this
// drives both with arbitrary bytes. The seed corpus runs as an ordinary
// test under `go test`; to fuzz:
//
//	go test -run='^$' -fuzz=FuzzHubFrames -fuzztime=20s ./internal/coop/hub/

// step is one entry of a fuzz stream: [who:u8][len:u16 BE][payload]. who's
// low bits pick the peer (a joiner, a second joiner, the host's own
// connection); with 0x80 set the peer first drops and comes back on a new
// connection with the same co-op ID.
type step struct {
	who byte
	msg wire.Msg
	raw []byte
}

func stream(t testing.TB, steps ...step) []byte {
	var b []byte
	for _, s := range steps {
		p := s.raw
		if s.msg != nil {
			var err error
			if p, err = wire.Encode(s.msg); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		b = append(b, s.who)
		b = binary.BigEndian.AppendUint16(b, uint16(len(p)))
		b = append(b, p...)
	}
	return b
}

func seedStreams(t testing.TB) [][]byte {
	bound := func(n byte) *wire.CoHello { return hello(wire.FileBound, testWorld, n) }
	unbound := hello(wire.FileUnbound, world.ID{}, 0)
	ask := hello(wire.FileUnbound, world.ID{}, 0)
	ask.WorldID, ask.BindNonce = wire.ID(testWorld), wire.ID{9}
	ask.Want |= uint32(AskBind | AskUnsaved)
	at := presence("M_NewD2", 3, 1)
	return [][]byte{
		nil,
		stream(t,
			step{who: 0, msg: bound(1)}, step{who: 1, msg: bound(2)}, step{who: 2, msg: bound(3)},
			step{who: 0, msg: worldMsg(testWorld, 0, world.Gain{Off: offOR, Val: 1}, world.Gain{Off: offMax, Val: 2})},
			step{who: 1, msg: gains(1, wire.Gain{Off: offOR, Val: 3}, wire.Gain{Off: offSwitch, Val: 0x80})},
			step{who: 1, msg: gains(1, wire.Gain{Off: offOR2, Val: 1})},
			step{who: 0, msg: at}, step{who: 1, msg: at},
			step{who: 0, msg: scoped(wire.ScopeZone, "M_NewD2", 3, 1, wire.ScopedWord{Word: 0, Bits: 1})},
			step{who: 1, msg: scoped(wire.ScopeKill, "M_NewD2", 3, 1, wire.ScopedWord{Word: 15, Bits: 1 << 31})},
			step{who: 0, msg: scoped(wire.ScopeRoomEdge, "M_NewD2", 3, 1, wire.ScopedWord{Word: 2, Bits: 0x8000})},
			step{who: 2, msg: &wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(offOR, 1)}},
			step{who: 0, msg: &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(FeatureImage)}},
			step{who: 0x80, msg: bound(1)},
			step{who: 0, msg: &wire.Ctrl{Op: wire.CtrlResync}},
		),
		stream(t,
			step{who: 0, msg: unbound}, step{who: 0, msg: worldMsg(world.ID{}, 0)},
			step{who: 0, msg: ask}, step{who: 0, msg: bound(9)},
			step{who: 1, msg: hello(wire.FileLeaked, otherWorld, 4)},
			step{who: 1, msg: gains(1, wire.Gain{Off: offOR, Val: 1})},
		),
		stream(t,
			step{who: 0, raw: []byte{0x04, 0, 0, 0, 1, 0, 1, 0x07, 0x78, 1}}, // GAINS before a hello, offset past the image
			step{who: 1, raw: []byte{0x01, 0, 2}},                            // CO_HELLO of schema 2
			step{who: 2, raw: []byte{0x08, 0x03, 0, 0, 0, 0}},                // TOMBSTONE of no bit
			step{who: 0x81, raw: []byte{0x02}},                               // a sub that travels down only
		),
	}
}

// syncHub is a hub driven on the test's goroutine: no hub goroutine and no
// writers, so a run depends on its input alone and the fuzzer's coverage
// signal with it.
type syncHub struct {
	t     *testing.T
	h     *Hub
	store *world.Store
	log   *logLines
}

func newSyncHub(t *testing.T) *syncHub {
	t.Helper()
	st, err := world.Create(t.TempDir(), world.New(testWorld, time.Unix(1_800_000_000, 0)), world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := &syncHub{t: t, store: st, log: &logLines{}}
	cfg := fullCfg()
	cfg.Store, cfg.StageTblHash, cfg.Logf = st, testTables, s.log.logf
	cfg.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	if s.h, err = newHub(cfg, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.h.shutdown)
	return s
}

func (s *syncHub) attach(c *fakeConn) {
	l := &link{conn: c, out: make(chan []byte, outboxLen), done: make(chan struct{})}
	reply := make(chan error, 1)
	s.h.handle(event{kind: evAttach, link: l, reply: reply})
	if err := <-reply; err != nil {
		s.t.Fatalf("attach %d: %v", c.id, err)
	}
}

func (s *syncHub) detach(c *fakeConn) {
	if l := s.h.links[c]; l != nil {
		delete(s.h.links, c)
		s.h.handle(event{kind: evDetach, link: l})
	}
}

func (s *syncHub) frame(c *fakeConn, payload []byte) {
	if l := s.h.links[c]; l != nil {
		l.pending.Add(1)
		s.h.handle(event{kind: evFrame, link: l, payload: payload})
	}
}

func FuzzHubFrames(f *testing.F) {
	for _, s := range seedStreams(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s := newSyncHub(t)
		ids := []byte{1, 2, 3}
		mk := func(i int) *fakeConn {
			if i == 2 {
				return hostConn(ids[i], "host")
			}
			return newConn(ids[i], "joiner")
		}
		conns := make([]*fakeConn, len(ids))
		for i := range conns {
			conns[i] = mk(i)
			s.attach(conns[i])
		}
		var seq uint64
		for len(data) >= 3 {
			who, n := data[0], int(binary.BigEndian.Uint16(data[1:3]))
			data = data[3:]
			n = min(n, len(data))
			payload := bytes.Clone(data[:n])
			data = data[n:]

			i := int(who&0x7F) % len(conns)
			if who&0x80 != 0 {
				s.detach(conns[i])
				conns[i] = mk(i)
				s.attach(conns[i])
			}
			s.frame(conns[i], payload)

			for _, c := range conns {
				c.take(t) // fails on anything that does not decode
			}
			if q := s.store.Seq(); q < seq {
				t.Fatalf("seq went from %d to %d", seq, q)
			} else {
				seq = q
			}
			if img := s.store.Image(); world.Shared(&img) != img {
				t.Fatal("the world holds bits that are not shared")
			}
			if s.log.has("internal error") || s.log.has("bug:") {
				t.Fatalf("the hub hit a bug: %q", s.log.lines)
			}
		}
		for _, p := range s.h.order {
			if p.state == StateRefused && p.features != 0 {
				t.Fatalf("refused peer %s has features %s", p, p.features)
			}
			if p.state != StateBound {
				if _, in := s.h.scopes.PlaceOf(p.id); in {
					t.Fatalf("%s is in the scoped stores while %s", p, p.state)
				}
			}
		}
		for _, k := range s.h.scopes.Keys() {
			if k.Kind == world.ScopeRoomEdge {
				t.Fatalf("a room-edge store is held: %v", k)
			}
		}
	})
}
