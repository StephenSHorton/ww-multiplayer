package bind

import (
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// outset is the stage the rig plays on. Any stage with Link opens the
// gates; the tag rules don't care which.
var outset = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}

// rig is a FakeGame driven the way a player drives the console, with the
// tool's side: settle reads snapshots tick by tick, as coop-sync does.
type rig struct {
	t *testing.T
	g *fakegame.FakeGame
	m *mem.FakeMem
}

func newRig(t *testing.T) *rig {
	t.Helper()
	m := mem.NewFakeMem()
	return &rig{t: t, g: fakegame.New(m), m: m}
}

func (r *rig) must(err error) {
	r.t.Helper()
	if err != nil {
		r.t.Fatal(err)
	}
}

// play loads the stage, as continuing from file select does.
func (r *rig) play() { r.t.Helper(); r.must(r.g.LoadStage(outset)) }

// newGame starts a new file in slot and plays it, unsaved.
func (r *rig) newGame(slot int, name string) {
	r.t.Helper()
	r.g.EnterFileSelect()
	r.must(r.g.NewGame(slot, name))
	r.play()
}

// save is the in-game save menu.
func (r *rig) save() { r.t.Helper(); r.g.Frames(30); r.must(r.g.Save()) }

// load loads slot from the card at file select and plays it.
func (r *rig) load(slot int) {
	r.t.Helper()
	r.g.EnterFileSelect()
	r.must(r.g.LoadFile(slot))
	r.play()
}

// realFile makes slot a file that has been saved and loaded back, so its
// mDateIPL is set: a file with progress.
func (r *rig) realFile(slot int, name string) {
	r.t.Helper()
	r.newGame(slot, name)
	r.save()
	r.load(slot)
}

// settle runs gate.StableTicks+1 ticks, a frame and a snapshot each, and
// returns the last snapshot and its gate status.
func (r *rig) settle() (*state.Snapshot, gate.Status) {
	r.t.Helper()
	var tr gate.Tracker
	var s *state.Snapshot
	var st gate.Status
	for range gate.StableTicks + 1 {
		r.g.Frames(6)
		var err error
		s, err = state.Read(r.m, state.Options{})
		r.must(err)
		st = tr.Observe(s)
	}
	return s, st
}

func (r *rig) classify(e Expect) Verdict {
	r.t.Helper()
	s, _ := r.settle()
	return Classify(FileOf(s), e)
}

// bind binds the loaded file to w with a confirmed plan and returns it.
func (r *rig) bind(w WorldID, fresh func(Nonce) bool) *Plan {
	r.t.Helper()
	s, st := r.settle()
	p, err := PlanBind(s, st, BindRequest{World: w, WorldSeq: 7, Fresh: fresh})
	r.must(err)
	p.Confirm()
	s, st = r.settle()
	_, err = Bind(r.m, p, s, st)
	r.must(err)
	return p
}

// scrub scrubs the loaded file's tag with a confirmed plan.
func (r *rig) scrub(e Expect) {
	r.t.Helper()
	s, st := r.settle()
	p, err := PlanScrub(s, st, e)
	r.must(err)
	p.Confirm()
	r.must(Scrub(r.m, p, s, st))
}

func (r *rig) tagBytes() []byte {
	r.t.Helper()
	b, err := r.m.Read(addr.Reserve, addr.ReserveSize)
	r.must(err)
	return b
}

// auditWrites fails the test unless every tool write since recording
// began fell inside the 0x30 tag bytes (so inside the 0x50 of
// dSv_reserve_c) and there was at least one.
func (r *rig) auditWrites() {
	r.t.Helper()
	ws := r.m.Writes()
	if len(ws) == 0 {
		r.t.Error("no tool writes were recorded")
	}
	whole := addr.Region{Name: "dSv_reserve_c", Start: addr.Reserve, End: addr.Reserve + addr.ReserveSize}
	for _, w := range ws {
		if !TagRegion.Contains(w.Addr, len(w.Data)) || !whole.Contains(w.Addr, len(w.Data)) {
			r.t.Errorf("tool write of %d bytes at 0x%08X is outside the tag 0x%08X-0x%08X",
				len(w.Data), w.Addr, TagRegion.Start, TagRegion.End)
		}
	}
}

// TestLeakCases: starting from a real file bound to world A in slot 0,
// each way vanilla carries the tag to a file it doesn't belong to (§5.2)
// classifies as Leaked with the rules that catch it, and the bound file
// itself survives a save and reload. Every case then scrubs the leak and
// checks the card keeps the scrubbed state. The write recorder sees only
// the tag bytes, through all of it.
func TestLeakCases(t *testing.T) {
	cases := []struct {
		name   string
		do     func(r *rig)
		state  State
		failed []Rule
	}{
		{"the bound file, saved and reloaded",
			func(r *rig) { r.save(); r.load(0) }, Bound, nil},
		{"the bound file, played on",
			func(r *rig) { r.g.Frames(600) }, Bound, nil},
		{"New Game in another slot (dComIfGs_init keeps mReserve)",
			func(r *rig) { r.newGame(1, "Tetra") }, Leaked, []Rule{V2, V4, V5}},
		{"New Game over the bound slot, another name",
			func(r *rig) { r.newGame(0, "Tetra") }, Leaked, []Rule{V4, V5}},
		{"New Game over the bound slot, the same name",
			func(r *rig) { r.newGame(0, "Link") }, Leaked, []Rule{V5}},
		{"a leaked New Game saved without Go, then loaded",
			func(r *rig) { r.newGame(1, "Tetra"); r.save(); r.load(1) }, Leaked, []Rule{V2, V4}},
		{"second quest into the same slot, as ExDataSave leaves RAM",
			func(r *rig) { r.g.EnterFileSelect(); r.must(r.g.SecondQuestSave(0)); r.play() }, Leaked, []Rule{V3, V5}},
		{"second quest into the same slot, loaded from the card",
			func(r *rig) { r.g.EnterFileSelect(); r.must(r.g.SecondQuestSave(0)); r.load(0) }, Leaked, []Rule{V3}},
		{"second quest into another slot, loaded",
			func(r *rig) { r.g.EnterFileSelect(); r.must(r.g.SecondQuestSave(2)); r.load(2) }, Leaked, []Rule{V2, V3}},
		{"file select's copy into another slot, loaded (d_file_select.cpp:1817-1830)",
			func(r *rig) {
				r.save()
				c, err := r.g.Card(0)
				r.must(err)
				r.must(r.g.SetCard(1, c))
				r.load(1)
			}, Leaked, []Rule{V2}},
		{"card_to_memory of an unbound file",
			func(r *rig) { r.load(2) }, Unbound, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.realFile(2, "Aryll") // unbound, on the card first
			r.realFile(0, "Link")
			r.m.Record(true)
			bound := r.bind(worldA, nil)
			if v := r.classify(Expect{World: worldA}); v.State != Bound {
				t.Fatalf("just bound: %s (%s)", v.State, v.Why)
			}

			c.do(r)
			v := r.classify(Expect{World: worldA})
			if v.State != c.state || !slices.Equal(v.Failed(), c.failed) {
				t.Fatalf("got %s failing %v (%s); want %s failing %v", v.State, v.Failed(), v.Why, c.state, c.failed)
			}
			if c.state == Leaked && v.Tag.Nonce != bound.Tag.Nonce {
				t.Errorf("the leaked tag has nonce %s, not the bind's %s", v.Tag.Nonce, bound.Tag.Nonce)
			}
			// The same verdict with no session world, and with a pending
			// fresh bind for some other nonce: a leak is a leak whatever
			// world is asked about.
			other := func(n Nonce) bool { return n == nonce1 }
			if v2 := r.classify(Expect{Fresh: other}); v2.State != c.state || !slices.Equal(v2.Failed(), c.failed) {
				t.Errorf("without a session world: %s failing %v", v2.State, v2.Failed())
			}
			if c.state == Bound {
				if v := r.classify(Expect{World: worldB}); v.State != Foreign || !slices.Equal(v.Failed(), []Rule{V6}) {
					t.Errorf("against world B: %s failing %v, want foreign failing [V6]", v.State, v.Failed())
				}
			}

			if c.state == Leaked {
				r.scrub(Expect{World: worldA})
				if b := r.tagBytes(); !allZero(b[:TagSize]) {
					t.Errorf("tag after the scrub: % X", b[:TagSize])
				}
				if v := r.classify(Expect{World: worldA}); v.State != Unbound || !v.Empty {
					t.Errorf("after the scrub: %s (%s)", v.State, v.Why)
				}
				// The scrubbed state is what the card gets.
				s, _ := r.settle()
				r.save()
				r.load(int(s.DataNum))
				if v := r.classify(Expect{World: worldA}); v.State != Unbound || !v.Empty {
					t.Errorf("reloaded after the scrub and a save: %s (%s)", v.State, v.Why)
				}
			}
			r.auditWrites()
		})
	}
}

// TestFreshBind: a New Game bound before its first save has mDateIPL 0
// (status_b init, d_save.cpp:56; memory_to_card stamps only the card,
// d_save.cpp:1717), so V5 holds only through the world file's pending
// fresh bind until the file is saved and loaded back.
func TestFreshBind(t *testing.T) {
	r := newRig(t)
	r.newGame(1, "Tetra")
	r.m.Record(true)
	var pending Nonce
	fresh := func(n Nonce) bool { return !pending.IsZero() && n == pending }

	s, st := r.settle()
	p, err := PlanBind(s, st, BindRequest{World: worldA, Fresh: fresh})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Fresh {
		t.Fatal("a never-saved New Game's bind is not marked fresh")
	}
	pending = p.Tag.Nonce // the world file records it before the write
	p.Confirm()
	if _, err := Bind(r.m, p, s, st); err != nil {
		t.Fatal(err)
	}

	if v := r.classify(Expect{World: worldA, Fresh: fresh}); v.State != Bound || !v.Fresh {
		t.Errorf("with the pending fresh bind: %s fresh %v (%s)", v.State, v.Fresh, v.Why)
	}
	if v := r.classify(Expect{World: worldA}); v.State != Leaked || !slices.Equal(v.Failed(), []Rule{V5}) {
		t.Errorf("without it: %s failing %v", v.State, v.Failed())
	}
	// Saving doesn't change RAM's mDateIPL; loading the file back does.
	r.save()
	if v := r.classify(Expect{World: worldA}); v.State != Leaked {
		t.Errorf("saved, not reloaded: %s", v.State)
	}
	r.load(1)
	if v := r.classify(Expect{World: worldA}); v.State != Bound || v.Fresh {
		t.Errorf("reloaded: %s fresh %v (%s)", v.State, v.Fresh, v.Why)
	}
	r.auditWrites()
}

// TestClassifyV1: the bytes that fail V1, and the state each gets.
func TestClassifyV1(t *testing.T) {
	good := sampleTag().Encode()
	file := File{Tag: good, Slot: 2}
	copy(file.Name[:], "Link")
	file.DateIPL = 1
	if v := Classify(file, Expect{World: worldA}); v.State != Bound {
		t.Fatalf("the sample file: %s (%s)", v.State, v.Why)
	}
	cases := []struct {
		name  string
		mut   func(b *[TagSize]byte)
		state State
		flag  func(Verdict) bool
	}{
		{"empty", func(b *[TagSize]byte) { *b = [TagSize]byte{} }, Unbound, func(v Verdict) bool { return v.Empty }},
		{"garbage", func(b *[TagSize]byte) { b[0] = 'X' }, Unbound, func(v Verdict) bool { return v.Garbage }},
		{"damaged", func(b *[TagSize]byte) { b[offNameHash] ^= 1 }, Unbound, func(v Verdict) bool { return v.Damaged }},
		{"newer layout", func(b *[TagSize]byte) { b[offLayout] = 9 }, Foreign, func(v Verdict) bool { return !v.HasTag }},
	}
	for _, c := range cases {
		f := file
		c.mut(&f.Tag)
		v := Classify(f, Expect{World: worldA})
		wantFailed := []Rule{V1}
		if c.name == "empty" {
			wantFailed = nil
		}
		if v.State != c.state || !c.flag(v) || !slices.Equal(v.Failed(), wantFailed) || v.HasTag {
			t.Errorf("%s: %s failing %v (%+v)", c.name, v.State, v.Failed(), v)
		}
		for _, ch := range v.Checks[1:] {
			if ch.Ran {
				t.Errorf("%s: %s ran after V1 failed", c.name, ch.Rule)
			}
		}
	}
}
