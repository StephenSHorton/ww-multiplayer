package bind

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestBindWritesTheTag: a confirmed bind writes exactly the planned tag in
// one write, the tag decodes to what was asked for, and the rest of
// mReserve stays as it was.
func TestBindWritesTheTag(t *testing.T) {
	r := newRig(t)
	r.realFile(1, "Link")
	tail := bytes.Repeat([]byte{0x77}, addr.ReserveSize-TagSize)
	r.must(r.m.Store(addr.Reserve+TagSize, tail)) // whatever the rest holds, it isn't ours
	r.m.Record(true)

	s, st := r.settle()
	p, err := PlanBind(s, st, BindRequest{World: worldA, Nonce: nonce1, WorldSeq: 42})
	if err != nil {
		t.Fatal(err)
	}
	if p.Fresh {
		t.Error("a saved and loaded file's bind is marked fresh")
	}
	want := Tag{Layout: LayoutV1, Slot: 1, World: worldA, Nonce: nonce1, WorldSeq: 42, NameHash: NameHash([]byte("Link"))}
	if p.Tag != want || p.Bytes() != want.Encode() {
		t.Fatalf("planned %+v, want %+v", p.Tag, want)
	}
	p.Confirm()
	got, err := Bind(r.m, p, s, st)
	if err != nil || got != want {
		t.Fatalf("Bind = %+v, %v", got, err)
	}
	enc := want.Encode()
	ws := r.m.Writes()
	if len(ws) != 1 || ws[0].Addr != addr.Reserve || !bytes.Equal(ws[0].Data, enc[:]) {
		t.Errorf("writes: %+v", ws)
	}
	if b := r.tagBytes(); !bytes.Equal(b[TagSize:], tail) {
		t.Errorf("mReserve +0x30.. = % X, changed", b[TagSize:])
	}
	if _, err := Bind(r.m, p, s, st); !errors.Is(err, ErrUsed) {
		t.Errorf("a second Bind with the same plan: %v", err)
	}
	r.auditWrites()
}

// TestApplyRefuses: every reason a confirmed-or-not plan is not carried
// out, each without a single write.
func TestApplyRefuses(t *testing.T) {
	cases := []struct {
		name string
		// do runs between the plan and the write; it returns the tick to
		// apply on, or nil for a fresh settle.
		do      func(r *rig, p *Plan) error
		confirm bool
		want    error
	}{
		{"not confirmed", nil, false, ErrNotConfirmed},
		{"pause menu opened", func(r *rig, p *Plan) error {
			r.g.SetPauseMenu(true)
			return nil
		}, true, ErrGate},
		{"emulation paused", func(r *rig, p *Plan) error {
			s, err := snap.Read(r.m)
			if err != nil {
				return err
			}
			var tr gate.Tracker
			tr.Observe(s)
			st := tr.Observe(s) // no frame ran: the heartbeat stalled
			_, err = Bind(r.m, p, s, st)
			return err
		}, true, ErrGate},
		{"another file loaded", func(r *rig, p *Plan) error {
			r.load(2)
			return nil
		}, true, ErrChanged},
		{"renamed under the snapshot", func(r *rig, p *Plan) error {
			s, st := r.settle()
			r.must(r.m.Store(addr.PlayerName, []byte("Medli\x00")))
			_, err := Bind(r.m, p, s, st)
			return err
		}, true, ErrChanged},
		{"a stale tick's snapshot", func(r *rig, p *Plan) error {
			s, st := r.settle()
			s.Save[infoOffClear] = 1
			_, err := Bind(r.m, p, s, st)
			return err
		}, true, ErrChanged},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.realFile(2, "Aryll")
			r.realFile(0, "Link")
			s, st := r.settle()
			p, err := PlanBind(s, st, BindRequest{World: worldA})
			if err != nil {
				t.Fatal(err)
			}
			if c.confirm {
				p.Confirm()
			}
			r.m.Record(true)
			var got error
			if c.do != nil {
				got = c.do(r, p)
			}
			if got == nil {
				s, st := r.settle()
				_, got = Bind(r.m, p, s, st)
			}
			if !errors.Is(got, c.want) {
				t.Errorf("Bind: %v, want %v", got, c.want)
			}
			if ws := r.m.Writes(); len(ws) != 0 {
				t.Errorf("%d writes made: %+v", len(ws), ws)
			}
		})
	}
}

// dropWrites is a Mem whose writes vanish, as a write lost to the game's
// card_to_memory would.
type dropWrites struct{ mem.Mem }

func (dropWrites) Write(uint32, []byte) error { return nil }

// TestVerifyFails: a write that doesn't read back is ErrVerify, and the
// plan is spent.
func TestVerifyFails(t *testing.T) {
	r := newRig(t)
	r.realFile(0, "Link")
	s, st := r.settle()
	p, err := PlanBind(s, st, BindRequest{World: worldA})
	if err != nil {
		t.Fatal(err)
	}
	p.Confirm()
	if _, err := Bind(dropWrites{r.m}, p, s, st); !errors.Is(err, ErrVerify) {
		t.Errorf("Bind through a Mem that drops writes: %v", err)
	}
	if _, err := Bind(r.m, p, s, st); !errors.Is(err, ErrUsed) {
		t.Errorf("Bind again: %v", err)
	}
}

// TestPlanBindRefuses: the files a bind is never planned for.
func TestPlanBindRefuses(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *rig)
		req   BindRequest
		want  error
	}{
		{"already bound", func(r *rig) { r.bind(worldA, nil) }, BindRequest{World: worldA}, ErrAlreadyBound},
		{"bound to another world", func(r *rig) { r.bind(worldB, nil) }, BindRequest{World: worldA}, ErrForeign},
		{"a newer tool's tag", func(r *rig) {
			b := sampleTag().Encode()
			b[offLayout] = 2
			r.must(r.m.Store(addr.Reserve, b[:]))
		}, BindRequest{World: worldA}, ErrForeign},
		{"another tool's bytes", func(r *rig) { r.must(r.m.Store(addr.Reserve, []byte("ARCH"))) }, BindRequest{World: worldA}, ErrNotOurs},
		{"second quest", func(r *rig) { r.must(r.m.Store(addr.ClearCount, []byte{1})) }, BindRequest{World: worldA}, ErrSecondQuest},
		{"pause menu", func(r *rig) { r.g.SetPauseMenu(true) }, BindRequest{World: worldA}, ErrGate},
		{"a boss refight", func(r *rig) { r.g.SetRefight(2) }, BindRequest{World: worldA}, ErrGate},
		{"file select", func(r *rig) { r.g.EnterFileSelect() }, BindRequest{World: worldA}, ErrGate},
		{"no world", nil, BindRequest{}, nil},
		{"no file slot", func(r *rig) { r.must(r.m.Store(addr.DataNum, []byte{3})) }, BindRequest{World: worldA}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.realFile(0, "Link")
			if c.setup != nil {
				c.setup(r)
			}
			s, st := r.settle()
			p, err := PlanBind(s, st, c.req)
			if p != nil || err == nil || (c.want != nil && !errors.Is(err, c.want)) {
				t.Errorf("PlanBind = %v, %v; want %v", p, err, c.want)
			}
		})
	}
}

// TestBindOverStaleTags: a leaked or damaged tag is ours and binds over.
func TestBindOverStaleTags(t *testing.T) {
	for _, name := range []string{"leaked", "damaged"} {
		r := newRig(t)
		r.realFile(0, "Link")
		r.bind(worldB, nil)
		r.save()
		if name == "leaked" {
			r.realFile(1, "Tetra") // a New Game saved and loaded with world B's tag
		} else {
			r.must(r.m.Store(addr.Reserve+offWorldSeq, []byte{0xFF}))
		}
		before := r.classify(Expect{World: worldA})
		if (name == "leaked") != (before.State == Leaked) || (name == "damaged") != before.Damaged {
			t.Fatalf("%s: set up %s (%s)", name, before.State, before.Why)
		}
		p := r.bind(worldA, nil)
		if !strings.Contains(p.Prompt, "replaced") {
			t.Errorf("%s: the prompt doesn't say the old tag goes:\n%s", name, p.Prompt)
		}
		if v := r.classify(Expect{World: worldA}); v.State != Bound {
			t.Errorf("%s: after binding: %s (%s)", name, v.State, v.Why)
		}
	}
}

// TestBindPromptOtherWorldV5: a tag of another world that fails only V5
// may be that world's fresh bind, not yet loaded from the card, which only
// that world's host can tell, so the bind prompt does not call it stale:
// it says so, and that binding replaces it. A V5-only tag of the world
// bound to (whose host has said its nonce is no pending fresh bind) and
// another world's tag that fails V2 as well are stale (Appendix C item
// 133).
func TestBindPromptOtherWorldV5(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(r *rig)
		maybe bool // the prompt says the tag may be a fresh bind
	}{
		{"another world's tag, V5 only", func(r *rig) { r.bind(worldB, nil) }, true},
		{"this world's tag, V5 only", func(r *rig) { r.bind(worldA, nil) }, false},
		{"another world's tag, V2 and V5", func(r *rig) { r.bind(worldB, nil); r.newGame(1, "Link") }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.newGame(0, "Link")
			c.setup(r)
			s, st := r.settle()
			p, err := PlanBind(s, st, BindRequest{World: worldA})
			if err != nil {
				t.Fatal(err)
			}
			maybe := []string{
				"Its current tag, of world " + worldB.String() + ", fails only V5: the file has not been loaded from the",
				"card since it was tagged. It may be a fresh bind to that world, or a tag leaked from another",
				"only that world's host can tell. Binding replaces it;",
			}
			stale := []string{"Its current tag is stale and will be replaced: the tag leaked from another file ("}
			want, not := stale, maybe
			if c.maybe {
				want, not = maybe, []string{"stale"}
			}
			for _, sub := range want {
				if !strings.Contains(p.Prompt, sub) {
					t.Errorf("the prompt lacks %q:\n%s", sub, p.Prompt)
				}
			}
			for _, sub := range not {
				if strings.Contains(p.Prompt, sub) {
					t.Errorf("the prompt has %q:\n%s", sub, p.Prompt)
				}
			}
		})
	}
}

// TestPlanScrubAndClear: a scrub is planned only for a leak; a clear for
// any non-zero tag bytes, and it zeroes only the tag's 0x30.
func TestPlanScrubAndClear(t *testing.T) {
	r := newRig(t)
	r.realFile(0, "Link")
	s, st := r.settle()
	if _, err := PlanScrub(s, st, Expect{World: worldA}); !errors.Is(err, ErrNotLeaked) {
		t.Errorf("scrub of an unbound file: %v", err)
	}
	if _, err := PlanClear(s, st, Expect{}); !errors.Is(err, ErrNoTag) {
		t.Errorf("clear of an unbound file: %v", err)
	}
	r.bind(worldA, nil)
	s, st = r.settle()
	for _, e := range []Expect{{World: worldA}, {World: worldB}, {}} {
		if _, err := PlanScrub(s, st, e); !errors.Is(err, ErrNotLeaked) {
			t.Errorf("scrub of a bound file, expecting %s: %v", e.World, err)
		}
	}

	// Clear a bound file: the tag goes, the rest of mReserve stays.
	tail := bytes.Repeat([]byte{0x55}, addr.ReserveSize-TagSize)
	r.must(r.m.Store(addr.Reserve+TagSize, tail))
	r.m.Record(true)
	s, st = r.settle()
	p, err := PlanClear(s, st, Expect{World: worldA})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Prompt, worldA.String()) || !strings.HasSuffix(p.Prompt, "?") {
		t.Errorf("clear prompt:\n%s", p.Prompt)
	}
	if err := Scrub(r.m, p, s, st); !errors.Is(err, ErrNotConfirmed) {
		t.Errorf("unconfirmed clear: %v", err)
	}
	p.Confirm()
	if _, err := Bind(r.m, p, s, st); err == nil {
		t.Error("Bind took a clear plan")
	}
	if err := Scrub(r.m, p, s, st); err != nil {
		t.Fatal(err)
	}
	b := r.tagBytes()
	if !allZero(b[:TagSize]) || !bytes.Equal(b[TagSize:], tail) {
		t.Errorf("mReserve after the clear: % X", b)
	}
	r.auditWrites()

	// Another tool's bytes can be cleared too, and only its first 0x30.
	r.must(r.m.Store(addr.Reserve, bytes.Repeat([]byte{0xAB}, addr.ReserveSize)))
	s, st = r.settle()
	p, err = PlanClear(s, st, Expect{})
	if err != nil {
		t.Fatal(err)
	}
	p.Confirm()
	r.must(Scrub(r.m, p, s, st))
	b = r.tagBytes()
	if !allZero(b[:TagSize]) || !bytes.Equal(b[TagSize:], bytes.Repeat([]byte{0xAB}, addr.ReserveSize-TagSize)) {
		t.Errorf("mReserve after clearing garbage: % X", b)
	}
	r.auditWrites()
}

// TestPrompts: each prompt names the file and the bytes, and asks.
func TestPrompts(t *testing.T) {
	r := newRig(t)
	r.realFile(0, "Link")
	s, st := r.settle()
	p, err := PlanBind(s, st, BindRequest{World: worldA})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{worldA.String(), `"Link"`, "slot 0", "0x803C532C", "0x30-byte"} {
		if !strings.Contains(p.Prompt, want) {
			t.Errorf("bind prompt lacks %q:\n%s", want, p.Prompt)
		}
	}
	p.Confirm()
	s, st = r.settle()
	if _, err := Bind(r.m, p, s, st); err != nil {
		t.Fatal(err)
	}
	r.newGame(2, "Tetra")
	s, st = r.settle()
	sp, err := PlanScrub(s, st, Expect{World: worldA})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"V2 failed", "V4 failed", "V5 failed", `"Tetra"`, "Scrub it?"} {
		if !strings.Contains(sp.Prompt, want) {
			t.Errorf("scrub prompt lacks %q:\n%s", want, sp.Prompt)
		}
	}
	if strings.Contains(sp.Prompt, "V3") {
		t.Errorf("scrub prompt names a rule that held:\n%s", sp.Prompt)
	}

	r = newRig(t)
	r.newGame(1, "Aryll")
	s, st = r.settle()
	fp, err := PlanBind(s, st, BindRequest{World: worldA})
	if err != nil {
		t.Fatal(err)
	}
	if !fp.Fresh || !strings.Contains(fp.Prompt, "save in-game soon") {
		t.Errorf("fresh bind prompt:\n%s", fp.Prompt)
	}
}
