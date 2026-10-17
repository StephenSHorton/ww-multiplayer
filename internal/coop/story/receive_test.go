package story

import (
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Sword slot items (d_item_data.h:63-65, :69).
const (
	heroSword    = 0x38
	masterSword1 = 0x39
	masterSword3 = 0x3E
)

// image returns an image with gs set (OR; a register gain as its value).
func image(gs ...world.Gain) world.Image {
	var img world.Image
	for _, g := range gs {
		img[g.Off] |= g.Val
	}
	return img
}

// heldIDs lists p's held bits by event id.
func heldIDs(p Plan) []uint16 {
	var ids []uint16
	for _, h := range p.Held {
		if h.Mask != addr.EventByte(h.Hold.ID).Mask || int(h.Off) != addr.OffEvent+addr.EventByte(h.Hold.ID).Off {
			ids = append(ids, 0xFFFF) // a held bit that is not its hold's
			continue
		}
		ids = append(ids, h.Hold.ID)
	}
	return ids
}

// bitIDs lists p's story bits by event id, one per bit.
func bitIDs(p Plan) []uint16 {
	var ids []uint16
	for _, g := range p.Bits {
		for b := 0; b < 8; b++ {
			if g.Val&(1<<b) != 0 {
				ids = append(ids, uint16(int(g.Off)-addr.OffEvent)<<8|1<<b)
			}
		}
	}
	return ids
}

// TestPendingSubtracts: the story bits planned are the world's SyncMask
// bits minus the receiver's own and its ever-had set.
func TestPendingSubtracts(t *testing.T) {
	view := image(ev(ev0520), ev(ev1820), ev(evZeldaAwakened), ev(evRodeKorl), ev(ev3F80))
	raw := image(ev(ev1820))
	var ever world.BitSet
	ever.Add(int(ev(ev3F80).Off), ev(ev3F80).Val)

	p := Pending(&view, &raw, &ever, nil)
	if got, want := bitIDs(p), []uint16{ev0520, evZeldaAwakened}; !slices.Equal(got, want) {
		t.Errorf("planned %04X, want %04X", got, want)
	}
	if len(p.Regs) != 0 || len(p.Held) != 0 {
		t.Errorf("regs %v held %v", p.Regs, p.Held)
	}
	if p.Empty() || !slices.Equal(p.Writes(), p.Bits) {
		t.Errorf("Writes %v", p.Writes())
	}

	// A nil ever-had set is empty.
	if got := bitIDs(Pending(&view, &raw, nil, nil)); !slices.Equal(got, []uint16{ev0520, evZeldaAwakened, ev3F80}) {
		t.Errorf("with no history: %04X", got)
	}
	// Nothing to do.
	if p := Pending(&raw, &raw, nil, nil); !p.Empty() || p.Writes() != nil {
		t.Errorf("a receiver with the world planned %v", p)
	}
}

// TestPendingHolds: each hold keeps its bit back until its condition holds
// in the receiver's own game, and UntilWith is met by a bit the same write
// sets.
func TestPendingHolds(t *testing.T) {
	sword := func(it byte) world.Gain { return world.Gain{Off: addr.OffSelectEquip, Val: it} }
	cases := []struct {
		name      string
		view, raw []world.Gain
		ever      []world.Gain
		bits      []uint16
		held      []uint16
	}{
		{"MET_KORL waits for the receiver's own",
			[]world.Gain{ev(evMetKorl)}, nil, nil,
			nil, []uint16{evMetKorl}},
		{"the resume bits wait for MET_KORL",
			[]world.Gain{ev(evMetKorl), ev(ev0801), ev(ev0808), ev(ev2401), ev(ev0520)}, nil, nil,
			[]uint16{ev0520}, []uint16{evMetKorl, ev0801, ev0808, ev2401}},
		{"the resume bits go once the receiver met Korl",
			[]world.Gain{ev(evMetKorl), ev(ev0801), ev(ev0808), ev(ev2401)}, []world.Gain{ev(evMetKorl)}, nil,
			[]uint16{ev0801, ev0808, ev2401}, nil},
		{"0x2D10 waits for a Master Sword",
			[]world.Gain{ev(ev2D10)}, []world.Gain{sword(heroSword)}, nil,
			nil, []uint16{ev2D10}},
		{"0x2D10 goes with the Master Sword",
			[]world.Gain{ev(ev2D10)}, []world.Gain{sword(masterSword1)}, nil,
			[]uint16{ev2D10}, nil},
		{"0x2D10 goes with the full-power Master Sword",
			[]world.Gain{ev(ev2D10)}, []world.Gain{sword(masterSword3)}, nil,
			[]uint16{ev2D10}, nil},
		{"0x3804 waits for ZELDA_AWAKENED",
			[]world.Gain{ev(ev3804)}, nil, nil,
			nil, []uint16{ev3804}},
		{"0x3804 goes with ZELDA_AWAKENED in the same write",
			[]world.Gain{ev(ev3804), ev(evZeldaAwakened)}, nil, nil,
			[]uint16{evZeldaAwakened, ev3804}, nil},
		{"0x3804 goes when the receiver has ZELDA_AWAKENED",
			[]world.Gain{ev(ev3804)}, []world.Gain{ev(evZeldaAwakened)}, nil,
			[]uint16{ev3804}, nil},
		{"0x3804 waits when ZELDA_AWAKENED is ever-had but gone",
			[]world.Gain{ev(ev3804), ev(evZeldaAwakened)}, nil, []world.Gain{ev(evZeldaAwakened)},
			nil, []uint16{ev3804}},
		{"0x3E10 waits for 0x3F80",
			[]world.Gain{ev(ev3E10)}, nil, nil,
			nil, []uint16{ev3E10}},
		{"0x3E10 goes with 0x3F80",
			[]world.Gain{ev(ev3E10), ev(ev3F80)}, nil, nil,
			[]uint16{ev3E10, ev3F80}, nil},
		{"a hold the receiver already passed holds nothing",
			[]world.Gain{ev(ev3804)}, []world.Gain{ev(ev3804)}, nil,
			nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			view, raw, ev := image(c.view...), image(c.raw...), image(c.ever...)
			var ever world.BitSet
			ever.AddImage(&ev)
			p := Pending(&view, &raw, &ever, nil)
			if got := bitIDs(p); !slices.Equal(got, c.bits) {
				t.Errorf("planned %04X, want %04X", got, c.bits)
			}
			if got := heldIDs(p); !slices.Equal(got, c.held) {
				t.Errorf("held %04X, want %04X", got, c.held)
			}
			// The plan never releases a hold: applied, every held bit's
			// hold is still unmet, or the bit is not set.
			after := ApplyPlan(&raw, p)
			for _, h := range class.Holds() {
				if eventSet(&after, h.ID) && !eventSet(&raw, h.ID) && !Released(h, &raw, &after) {
					t.Errorf("0x%04X applied while its hold is unmet", h.ID)
				}
			}
		})
	}
}

// TestReleased: the hold conditions read the receiver's snapshot.
func TestReleased(t *testing.T) {
	byID := map[uint16]class.Hold{}
	for _, h := range class.Holds() {
		byID[h.ID] = h
	}
	raw := image(ev(evMetKorl), world.Gain{Off: addr.OffSelectEquip, Val: heroSword})
	with := image(ev(evMetKorl), ev(evZeldaAwakened))
	cases := []struct {
		id         uint16
		raw, after *world.Image
		want       bool
	}{
		{ev0801, &raw, nil, true},
		{ev0801, &world.Image{}, &with, false}, // UntilLocal: the write's own MET_KORL does not count
		{ev3804, &raw, nil, false},
		{ev3804, &raw, &with, true},
		{ev2D10, &raw, &with, false},
	}
	for _, c := range cases {
		if got := Released(byID[c.id], c.raw, c.after); got != c.want {
			t.Errorf("0x%04X: Released %v, want %v", c.id, got, c.want)
		}
	}
	if Released(class.Hold{EventBit: class.EventBit{ID: ev0520}}, &raw, &with) {
		t.Error("a hold with no condition released")
	}
}

// TestPendingRegisters: a figurine bitfield is planned by OR minus the
// receiver's bits and ever-had set, a MAX register when the world's field
// is above both its own and the most it ever had, and no other register.
func TestPendingRegisters(t *testing.T) {
	cases := []struct {
		name    string
		reg     int
		w, l, e byte // world, local, ever (ever-had bits, or everMax)
		want    byte // 0: not planned
	}{
		{"figurine bits the receiver lacks", regFigure, 0x0F, 0x03, 0, 0x0C},
		{"figurine bits ever had are not set again", regFigure, 0x0F, 0x03, 0x04, 0x08},
		{"figurine bits the receiver has", regFigure, 0x0F, 0x1F, 0, 0},
		{"a letter the world has read", regLetter, 3, 1, 1, 3},
		{"a letter field, the byte's other bits not the world's", regLetter, 0xF2, 0xA1, 0, 2},
		{"a letter the receiver is ahead on", regLetter, 1, 2, 2, 0},
		{"a letter the receiver had further once", regLetter, 2, 1, 3, 0},
		{"a letter equal", regLetter, 2, 0xF2, 0, 0},
		{"Beedle points", regBeedle, 40, 30, 35, 40},
		{"Beedle points the receiver spent", regBeedle, 40, 10, 50, 0},
		{"a register outside the allowlist", regLocal, 0xFF, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			off := addr.OffEvent + c.reg
			var view, raw, everMax world.Image
			var ever world.BitSet
			view[off], raw[off] = c.w, c.l
			if class.Of(off).Class == class.Max {
				everMax[off] = c.e
			} else {
				ever.Add(off, c.e)
			}
			p := Pending(&view, &raw, &ever, &everMax)
			var want []world.Gain
			if c.want != 0 {
				want = []world.Gain{{Off: uint16(off), Val: c.want}}
			}
			if !slices.Equal(p.Regs, want) {
				t.Errorf("regs %v, want %v", p.Regs, want)
			}
			if len(p.Bits) != 0 {
				t.Errorf("bits %v", p.Bits)
			}
		})
	}
}

// TestPlanWrites: Writes lists the story bits, then the registers, in
// ascending order; ApplyPlan reaches the world without clearing anything.
func TestPlanWrites(t *testing.T) {
	view := image(ev(ev0520), ev(evZeldaAwakened), reg(regLetter, 2), reg(regFigure, 0x30))
	raw := image(reg(regLetter, 0xFC|1), reg(regFigure, 0x01))
	p := Pending(&view, &raw, nil, nil)
	ws := p.Writes()
	if !slices.IsSortedFunc(ws, func(a, b world.Gain) int { return int(a.Off) - int(b.Off) }) || len(ws) != 4 {
		t.Fatalf("Writes %v", ws)
	}
	got := ApplyPlan(&raw, p)
	want := raw
	want[ev(ev0520).Off] |= ev(ev0520).Val
	want[ev(evZeldaAwakened).Off] |= ev(evZeldaAwakened).Val
	want[addr.OffEvent+regLetter] = 0xFC | 2
	want[addr.OffEvent+regFigure] = 0x31
	if got != want {
		t.Errorf("ApplyPlan differs")
	}
	if again := Pending(&view, &got, nil, nil); !again.Empty() {
		t.Errorf("after the plan: %v", again)
	}
}

// good returns a snapshot that passes every gate, at heartbeat hb, with
// mNextStage.mEnable enable.
func good(hb uint32, enable int8) *snap.Snapshot {
	return &snap.Snapshot{
		StageName:  "sea",
		StageRoom:  44,
		PlayerPtr:  fakegame.PlayerActor,
		Table:      addr.TableSea,
		StayNo:     44,
		Heartbeat:  hb,
		NextEnable: enable,
	}
}

// TestReceiverOnTransition: the plan is written on the first tick of a
// stage change only, and never without a history, with a stalled
// heartbeat or when the caller says no.
func TestReceiverOnTransition(t *testing.T) {
	view := image(ev(ev0520))
	var raw world.Image
	type step struct {
		hb     uint32
		enable int8
		ok     bool
		write  bool
	}
	settle := func(ok bool) []step {
		var s []step
		for hb := uint32(1); hb <= 5; hb++ {
			s = append(s, step{hb, 0, ok, false})
		}
		return s
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"the first enabled tick writes, the next ones do not",
			append(settle(true), step{6, 1, true, true}, step{7, 1, true, false}, step{8, 1, true, false})},
		{"a stable tick does not write", settle(true)},
		{"not while the caller says no",
			append(settle(false), step{6, 1, false, false}, step{7, 1, true, false})},
		{"not with a stalled heartbeat",
			append(settle(true), step{5, 1, true, false}, step{6, 1, true, false})},
		{"not before the stage settled",
			[]step{{1, 0, true, false}, {2, 0, true, false}, {3, 1, true, false}}},
		{"the next stage change writes again",
			append(append(settle(true), step{6, 1, true, true}, step{7, 0, true, false}),
				step{8, 0, true, false}, step{9, 0, true, false}, step{10, 0, true, false}, step{11, 0, true, false},
				step{12, 1, true, true})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var tr gate.Tracker
			var r Receiver
			for _, s := range c.steps {
				st := tr.Observe(good(s.hb, s.enable))
				ws, p := r.Tick(st, s.enable, s.ok, &view, &raw, nil, nil)
				if got := ws != nil; got != s.write {
					t.Errorf("heartbeat %d enable %d: wrote %v, want %v", s.hb, s.enable, got, s.write)
				}
				if ws != nil && !slices.Equal(ws, p.Writes()) {
					t.Errorf("wrote %v, plan %v", ws, p.Writes())
				}
				if !slices.Equal(p.Bits, []world.Gain{ev(ev0520)}) {
					t.Errorf("plan %v", p.Bits)
				}
			}
		})
	}

	// A Receiver with no history writes nothing, even where the gates
	// say the stage change starts; Reset forgets the history.
	tr := gate.Tracker{}
	var st gate.Status
	for hb := uint32(1); hb <= 6; hb++ {
		e := int8(0)
		if hb == 6 {
			e = 1
		}
		st = tr.Observe(good(hb, e))
	}
	if !st.CanWriteOnTransition() {
		t.Fatal("the gates do not open")
	}
	var fresh Receiver
	if ws, _ := fresh.Tick(st, 1, true, &view, &raw, nil, nil); ws != nil {
		t.Error("a Receiver with no history wrote")
	}
	var r Receiver
	r.Tick(gate.Status{}, 0, true, &view, &raw, nil, nil)
	r.Reset()
	if ws, _ := r.Tick(st, 1, true, &view, &raw, nil, nil); ws != nil {
		t.Error("a Receiver wrote after Reset")
	}
	// A transition with nothing pending writes nothing.
	r = Receiver{}
	r.Tick(gate.Status{}, 0, true, &raw, &raw, nil, nil)
	if ws, p := r.Tick(st, 1, true, &raw, &raw, nil, nil); ws != nil || !p.Empty() {
		t.Errorf("wrote %v with nothing pending", ws)
	}
}
