package replica

import (
	"errors"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// consistentObs is a consistent tick of table t whose canonical image is
// raw, for driving a Planner without a game.
func consistentObs(t int, raw world.Image) *obs {
	return &obs{s: &state.Snapshot{Table: int8(t)}, screen: screenGame, stagTable: t, raw: raw, local: world.Shared(&raw)}
}

// firstOf returns the first image byte keep accepts.
func firstOf(t *testing.T, keep func(off int) bool) int {
	t.Helper()
	for off := range addr.SaveImageSize {
		if keep(off) {
			return off
		}
	}
	t.Fatal("no such image byte")
	return 0
}

func TestPlannerTargets(t *testing.T) {
	cur := addr.TableDRC
	_, swBit, _ := addr.SwitchBit(0x10)
	register := firstOf(t, func(off int) bool { return CategoryOf(off) == CatRegister && class.Of(off).Class == class.Max })
	registerOR := firstOf(t, func(off int) bool { return CategoryOf(off) == CatRegister && class.Of(off).Class == class.OR })
	zelda, zeldaMask := eventBit(evZeldaAwakened)
	live := func(t, rel int) uint32 { return addr.LiveMemBit + uint32(rel) }

	cases := []struct {
		name     string
		off      int
		val      byte
		cat      Category
		when     Timing
		max      bool
		liveAddr uint32
	}{
		{"a chest of the loaded table", addr.OffMemBit(cur) + 3, 0x01, CatStage, Live, false, live(cur, 3)},
		{"a chest of another table", addr.OffMemBit(addr.TableSea) + 3, 0x01, CatStage, OnLoad, false, 0},
		{"a memory switch of the loaded table", addr.OffMemBit(cur) + swBit.Off, swBit.Mask, CatSwitch, OnRoomChange, false, live(cur, swBit.Off)},
		{"a memory switch of another table", addr.OffMemBit(addr.TableFW) + swBit.Off, swBit.Mask, CatSwitch, OnLoad, false, 0},
		{"a visited room of the loaded table", addr.OffMemBit(cur) + addr.MemBitOffVisited, 0x80, CatStage, Live, false, live(cur, addr.MemBitOffVisited)},
		{"a dungeon item of the loaded table", addr.OffMemBit(cur) + addr.MemBitOffDungeonItem, 0x01, CatStage, Live, false, live(cur, addr.MemBitOffDungeonItem)},
		{"an ocean grid", addr.OffOcean + 5, 0x04, CatOcean, OnRoomChange, false, 0},
		{"a chart", addr.OffMapGet + 2, 0x10, CatOcean, OnRoomChange, false, 0},
		{"a story bit", zelda, zeldaMask, CatStory, OnTransition, false, 0},
		{"an OR event register", registerOR, class.Of(registerOR).Mask & -class.Of(registerOR).Mask, CatRegister, OnTransition, false, 0},
		{"a MAX event register", register, class.Of(register).Mask & -class.Of(register).Mask, CatRegister, OnTransition, true, 0},
		{"an item", addr.OffGetItem + 4, 0x02, CatInventory, Live, false, 0},
		{"the wallet", addr.OffWalletSize, 2, CatCapacity, Live, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var view, raw world.Image
			view[c.off] = c.val
			p := NewPlanner(world.FullPolicy())
			ws, held := p.Plan(&view, &raw, &raw, cur)
			want := Write{Off: uint16(c.off), Bits: c.val, Max: c.max, Category: c.cat, When: c.when,
				Saved: addr.SaveImage + uint32(c.off), Live: c.liveAddr}
			if len(held) != 0 || !slices.Equal(ws, []Write{want}) {
				t.Fatalf("plan %+v, held %v; want %+v", ws, held, want)
			}
			if got := Planned(&raw, ws); got[c.off] != c.val {
				t.Errorf("Planned byte = 0x%02X, want 0x%02X", got[c.off], c.val)
			}
		})
	}
}

func TestPlannerMissing(t *testing.T) {
	chestOff, chestMask := chest(addr.TableSea, 3)
	swOff, swMask := switchImageByte(addr.TableSea, 0x05)
	openOff := addr.OffMapOpen + 1
	wallet := addr.OffWalletSize
	story, storyMask := eventBit(evZeldaAwakened)

	t.Run("what the receiver has is not planned", func(t *testing.T) {
		var view, raw world.Image
		view[chestOff], raw[chestOff] = chestMask|0x10, chestMask
		ws, _ := NewPlanner(nil).Plan(&view, &raw, &raw, addr.TableSea)
		if len(ws) != 1 || ws[0].Bits != 0x10 {
			t.Fatalf("plan %+v, want only bit 0x10", ws)
		}
	})
	t.Run("a MAX byte only above the receiver's value", func(t *testing.T) {
		for _, c := range []struct{ view, local, want byte }{{2, 1, 2}, {1, 2, 0}, {2, 2, 0}} {
			var view, raw world.Image
			view[wallet], raw[wallet] = c.view, c.local
			ws, _ := NewPlanner(nil).Plan(&view, &raw, &raw, addr.TableSea)
			switch {
			case c.want == 0 && len(ws) != 0:
				t.Errorf("view %d local %d: plan %+v, want none", c.view, c.local, ws)
			case c.want != 0 && (len(ws) != 1 || ws[0].Bits != c.want || !ws[0].Max):
				t.Errorf("view %d local %d: plan %+v, want raise to %d", c.view, c.local, ws, c.want)
			}
		}
	})
	t.Run("the default policy plans no story bit", func(t *testing.T) {
		var view, raw world.Image
		view[story] = storyMask
		if ws, _ := NewPlanner(nil).Plan(&view, &raw, &raw, addr.TableSea); len(ws) != 0 {
			t.Fatalf("plan %+v", ws)
		}
	})
	t.Run("a bit the receiver ever had is suppressed", func(t *testing.T) {
		p := NewPlanner(world.FullPolicy())
		var had world.Image
		had[story], had[swOff], had[openOff], had[wallet] = storyMask, swMask, 0x04, 3
		p.observe(consistentObs(addr.TableSea, had))
		// The game cleared the story bit, the switch and the chart's
		// deciphered bit (and lowered the wallet); the world still has
		// them.
		var view, now world.Image
		view[story], view[swOff], view[openOff], view[wallet] = storyMask, swMask, 0x04, 3
		p.observe(consistentObs(addr.TableSea, now))
		if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 0 {
			t.Fatalf("plan %+v, want nothing: the game cleared them itself", ws)
		}
		if ever := p.EverHad(); !ever.Has(story, storyMask) || !ever.Has(swOff, swMask) {
			t.Error("EverHad lacks the story bit or the switch")
		}
		// A higher wallet than ever had is planned.
		view[wallet] = 4
		if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 1 || ws[0].Bits != 4 {
			t.Fatalf("plan %+v, want wallet 4", ws)
		}
	})
	t.Run("the dry run suppresses every bit the receiver ever had, as M2 did", func(t *testing.T) {
		p := NewPlanner(world.FullPolicy())
		var had, view, now world.Image
		had[chestOff], had[addr.OffMapGet] = chestMask, 0x01
		view = had
		p.observe(consistentObs(addr.TableSea, had))
		p.observe(consistentObs(addr.TableSea, now))
		if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 0 {
			t.Fatalf("plan %+v, want nothing", ws)
		}
		o := consistentObs(addr.TableSea, now)
		o.st.Regressed = true
		p.observe(o)
		if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 0 {
			t.Fatalf("plan %+v after a save state, want nothing", ws)
		}
	})
	t.Run("a bit the game never clears is planned again whenever it is missing", func(t *testing.T) {
		// Lost all the same, or never kept by a file loaded from the card
		// again: the stage progress bits, the ocean, the charts owned and
		// the sea map heal when the plan is written.
		visOff, visMask := visited(addr.TableDRC, 9)
		heal := []struct {
			off  int
			mask byte
		}{
			{chestOff, chestMask},
			{visOff, visMask},
			{addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem, 0x04},
			{addr.OffOcean + 7, 0x10},
			{addr.OffMapGet + 2, 0x01},
			{addr.OffFmapBits + 3, 0x02},
		}
		p := NewPlanner(world.FullPolicy())
		p.writes = true
		var had, view, now world.Image
		for _, h := range heal {
			had[h.off] |= h.mask
			view[h.off] |= h.mask
		}
		p.observe(consistentObs(addr.TableSea, had))
		p.observe(consistentObs(addr.TableSea, now))
		ws, _ := p.Plan(&view, &now, &now, addr.TableSea)
		if got := Planned(&now, ws); got != view {
			t.Fatalf("plan %+v: planned image lacks %s", ws, fmtGains(world.Diff(&got, &view)))
		}
	})
	t.Run("the charts deciphered and salvaged and the Triforce charts are apply-once", func(t *testing.T) {
		// Getting a chart clears its deciphered and salvaged bits
		// (item_func_collectmapNN, d_item.cpp:1476-2023; chart 8's
		// Triforce bit, :1958), and the sea chart menu clears them for a
		// chart not owned (d_menu_fmap2.cpp:3174-3192).
		for _, off := range []int{addr.OffMapOpen + 3, addr.OffMapComplete, addr.OffMapTriforce} {
			if healing(off) {
				t.Errorf("%s heals: the game clears it", DefaultLabel(off, -1))
			}
		}
		for _, off := range []int{addr.OffMapGet, addr.OffOcean, addr.OffFmapBits, addr.OffMemBit(addr.TableFW) + addr.MemBitOffItem} {
			if !healing(off) {
				t.Errorf("%s is apply-once: the game never clears it", DefaultLabel(off, -1))
			}
		}
		if healing(swOff) || healing(story) || healing(addr.OffGetItem) {
			t.Error("a switch, a story bit or an item heals")
		}
	})
	t.Run("a bit the reconciler wrote is ever-had before a tick reads it", func(t *testing.T) {
		p := NewPlanner(world.FullPolicy())
		p.writes = true
		var none, view world.Image
		p.observe(consistentObs(addr.TableSea, none))
		p.wrote(swOff, swMask)
		view[swOff] = swMask
		if ws, _ := p.Plan(&view, &none, &none, addr.TableSea); len(ws) != 0 {
			t.Fatalf("plan %+v: a switch written once and cleared at once is planned again", ws)
		}
	})
	t.Run("a save state or a trip through file select starts ever-had over", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			reset func(o *obs)
		}{
			{"a save state", func(o *obs) { o.st.Regressed = true }}, // the heartbeat went backwards
			{"file select", func(o *obs) { o.screen, o.stagTable = screenOut, -1 }},
		} {
			p := NewPlanner(world.FullPolicy())
			p.writes = true
			var had, view, now world.Image
			had[swOff] = swMask
			view[swOff] = swMask
			p.observe(consistentObs(addr.TableSea, had))
			p.observe(consistentObs(addr.TableSea, now))
			if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 0 {
				t.Fatalf("%s: plan %+v, want the cleared switch suppressed", c.name, ws)
			}
			o := consistentObs(addr.TableSea, now)
			c.reset(o)
			p.observe(o)
			p.observe(consistentObs(addr.TableSea, now))
			if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 1 || ws[0].Bits != swMask {
				t.Fatalf("%s: plan %+v, want the switch: the loaded game never had it", c.name, ws)
			}
		}
	})
	t.Run("an inconsistent tick adds nothing to ever-had", func(t *testing.T) {
		p := NewPlanner(world.FullPolicy())
		var had world.Image
		had[chestOff] = chestMask
		o := consistentObs(addr.TableSea, had)
		o.stagTable = addr.TableDRC
		p.observe(o)
		var view, now world.Image
		view[chestOff] = chestMask
		if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 1 {
			t.Fatalf("plan %+v, want the chest", ws)
		}
	})
	t.Run("another file starts ever-had over", func(t *testing.T) {
		p := NewPlanner(world.FullPolicy())
		var had world.Image
		had[chestOff] = chestMask
		p.observe(consistentObs(addr.TableSea, had))
		o := consistentObs(addr.TableSea, world.Image{})
		o.key.name[0] = 'Z'
		p.observe(o)
		var view, now world.Image
		view[chestOff] = chestMask
		if ws, _ := p.Plan(&view, &now, &now, addr.TableSea); len(ws) != 1 {
			t.Fatalf("plan %+v, want the chest for the new file", ws)
		}
	})
}

func TestPlannerHolds(t *testing.T) {
	type state struct {
		view, local []uint16 // event bits
		sword       byte     // mSelectEquip[0]
	}
	cases := []struct {
		name    string
		st      state
		planned []uint16
		held    []uint16
	}{
		{"MET_KORL waits for the receiver's own", state{view: []uint16{evMetKorl}}, nil, []uint16{evMetKorl}},
		{"and the resume bits wait for MET_KORL", state{view: []uint16{evMetKorl, ev0801, ev0808, ev2401}},
			nil, []uint16{evMetKorl, ev0801, ev0808, ev2401}},
		{"the resume bits go once the receiver met Korl", state{view: []uint16{evMetKorl, ev0801, ev0808, ev2401}, local: []uint16{evMetKorl}},
			[]uint16{ev0801, ev0808, ev2401}, nil},
		{"0x2D10 waits for a Master Sword", state{view: []uint16{ev2D10}, sword: 0x38}, nil, []uint16{ev2D10}},
		{"0x2D10 with the Master Sword equipped", state{view: []uint16{ev2D10}, sword: 0x39}, []uint16{ev2D10}, nil},
		{"0x2D10 with the full-power Master Sword", state{view: []uint16{ev2D10}, sword: 0x3E}, []uint16{ev2D10}, nil},
		{"0x3804 waits for ZELDA_AWAKENED", state{view: []uint16{evHyruleCourtyard}}, nil, []uint16{evHyruleCourtyard}},
		{"0x3804 goes with ZELDA_AWAKENED in the same plan", state{view: []uint16{evHyruleCourtyard, evZeldaAwakened}},
			[]uint16{evZeldaAwakened, evHyruleCourtyard}, nil},
		{"0x3E10 waits for 0x3F80 (the decomp's third boat lock)", state{view: []uint16{ev3E10}}, nil, []uint16{ev3E10}},
		{"0x3E10 with 0x3F80 local", state{view: []uint16{ev3E10}, local: []uint16{ev3F80}}, []uint16{ev3E10}, nil},
	}
	ids := func(set []uint16) world.Image {
		var img world.Image
		for _, id := range set {
			off, m := eventBit(id)
			img[off] |= m
		}
		return img
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			view, raw := ids(c.st.view), ids(c.st.local)
			raw[addr.OffSelectEquip] = c.st.sword
			local := world.Shared(&raw)
			ws, held := NewPlanner(world.FullPolicy()).Plan(&view, &raw, &local, addr.TableSea)
			if got, want := writesImage(ws, world.Image{}), ids(c.planned); got != want {
				t.Errorf("planned %+v, want events %04X", ws, c.planned)
			}
			var heldImg world.Image
			for _, h := range held {
				heldImg[h.Off] |= h.Mask
				if h.Hold.Why == "" {
					t.Errorf("hold of 0x%04X has no reason", h.Hold.ID)
				}
			}
			if want := ids(c.held); heldImg != want {
				t.Errorf("held %+v, want events %04X", held, c.held)
			}
		})
	}
}

// writesImage ORs ws into img.
func writesImage(ws []Write, img world.Image) world.Image {
	for _, w := range ws {
		img[w.Off] |= w.Bits
	}
	return img
}

// fullView is a world with every bit the class table shares set, MAX
// bytes at their mask.
func fullView() world.Image {
	var v world.Image
	for off := range v {
		v[off] = world.SharedMask(off)
	}
	return v
}

func TestPlannedReachesTheWorld(t *testing.T) {
	// With nothing held and nothing ever had, the planned image is the
	// join of the receiver and the world within the policy.
	for _, pol := range []*world.Policy{world.DefaultPolicy(), world.FullPolicy()} {
		view := fullView()
		var raw world.Image
		raw[addr.OffSelectEquip] = 0x39
		for _, id := range []uint16{evMetKorl, evZeldaAwakened, ev3F80} {
			off, m := eventBit(id)
			raw[off] |= m
		}
		local := world.Shared(&raw)
		ws, held := NewPlanner(pol).Plan(&view, &raw, &local, addr.TableSea)
		if len(held) != 0 {
			t.Fatalf("held %+v", held)
		}
		got := Planned(&local, ws)
		for off := range got {
			want := local[off] | view[off]&pol[off]&planMask(off)
			if class.Of(off).Class == class.Max {
				want = max(local[off], view[off]&pol[off])
			}
			if got[off] != want {
				t.Fatalf("planned byte %s = 0x%02X, want 0x%02X", DefaultLabel(off, -1), got[off], want)
			}
		}
	}
}

func TestGuardAllowsEveryPlannedWrite(t *testing.T) {
	g := NewGuard()
	view := fullView()
	var raw world.Image
	for cur := range addr.TableCount {
		ws, _ := NewPlanner(world.FullPolicy()).Plan(&view, &raw, &raw, cur)
		if len(ws) == 0 {
			t.Fatal("nothing planned")
		}
		for _, w := range ws {
			for _, a := range w.Addrs() {
				m := g.Mask(a)
				if m == 0 || (!w.Max && w.Bits&^m != 0) || (w.Max && w.Bits&^m != 0) {
					t.Fatalf("table %d: write %+v at 0x%08X outside the guard (mask 0x%02X)", cur, w, a, m)
				}
				if err := g.Check(a, []byte{0}, []byte{w.Bits}); err != nil {
					t.Fatalf("table %d: %v", cur, err)
				}
			}
		}
	}
}

func TestGuardRefuses(t *testing.T) {
	g := NewGuard()
	refused := []struct {
		name string
		a    uint32
	}{
		{"mLife: a 0 kills Link", addr.SaveImage + addr.OffLife},
		{"mRupee", addr.SaveImage + addr.OffRupee},
		{"mMaxLife: through mItemMaxLifeCount only", addr.SaveImage + addr.OffMaxLife},
		{"mMaxMagic: through mItemMaxMagicCount only", addr.SaveImage + addr.OffMaxMagic},
		{"mPlayerName", addr.PlayerName},
		{"mReserve past the tag", addr.Reserve + bind.TagSize},
		{"mDataNum", addr.DataNum},
		{"the heartbeat", mailbox.SpawnTrigger},
		{"mDan.mStageNo", addr.DanStageNo},
		{"a zone's mRoomNo", func() uint32 { a, _ := addr.ZoneAddr(3); return a + addr.ZoneOffRoomNo }()},
		{"a zone's item bits", func() uint32 { a, _ := addr.ZoneAddr(3); return a + addr.ZoneOffItem }()},
		{"play.mSelectItem[3]: no button", addr.PlaySelectItem + 3},
		{"mSelectEquip[3]: no setter", addr.SaveImage + addr.OffSelectEquip + 3},
		{"below MEM1", 0x7FFFFFFF},
		{"above MEM1", 0x81800000},
	}
	for _, c := range refused {
		if g.Allows(c.a, 1) {
			t.Errorf("%s (0x%08X) is allowed", c.name, c.a)
		}
		if err := g.Check(c.a, []byte{0}, []byte{1}); !errors.Is(err, ErrGuard) {
			t.Errorf("%s: Check = %v, want ErrGuard", c.name, err)
		}
	}

	allowed := []struct {
		name string
		a    uint32
		n    int
	}{
		{"the whole tag", addr.Reserve, bind.TagSize},
		{"the live memBit", addr.LiveMemBit, addr.MemBitSize - 2},
		{"the dan switches", addr.DanSwitch, 8},
		{"a zone's switches", func() uint32 { a, _ := addr.ZoneAddr(31); return a + addr.ZoneOffSwitch }(), 6},
		{"a zone's dead actors", func() uint32 { a, _ := addr.ZoneAddr(0); return a + addr.ZoneOffActor }(), addr.ZoneSize - addr.ZoneOffActor},
		{"the pending counters", addr.ItemKeyNumCount, 4},
		{"mItemMaxMagicCount", addr.ItemMaxMagicCount, 2},
		{"play.mSelectItem[0..2]", addr.PlaySelectItem, 3},
		{"mSelectEquip[0..2]", addr.SaveImage + addr.OffSelectEquip, 3},
	}
	for _, c := range allowed {
		if !g.Allows(c.a, c.n) {
			t.Errorf("%s (0x%08X, %d bytes) is refused", c.name, c.a, c.n)
		}
	}

	// A bottle content ever held, above mGetItem[0]'s bit 0, is each
	// player's own (inventory.FlagMask).
	if err := g.Check(addr.SaveImage+addr.OffGetItem, []byte{0}, []byte{0x02}); !errors.Is(err, ErrGuard) {
		t.Errorf("mGetItem[0] bit 1, a bottle content held: Check = %v, want ErrGuard", err)
	}

	// A changed bit outside its byte's mask is refused.
	di := addr.SaveImage + uint32(addr.OffMemBit(addr.TableDRC)+addr.MemBitOffDungeonItem)
	if m := g.Mask(di); m != addr.DungeonItemMask {
		t.Fatalf("mDungeonItem mask 0x%02X, want 0x%02X", m, addr.DungeonItemMask)
	}
	if err := g.Check(di, []byte{0}, []byte{^byte(addr.DungeonItemMask)}); !errors.Is(err, ErrGuard) {
		t.Errorf("bits outside mDungeonItem's mask: %v", err)
	}
	if err := g.Check(di, []byte{0x80}, []byte{0x80 | 0x01}); err != nil {
		t.Errorf("an unchanged bit outside the mask is fine: %v", err)
	}
	if err := g.Check(di, []byte{0}, []byte{1, 2}); !errors.Is(err, ErrGuard) {
		t.Errorf("length mismatch: %v", err)
	}
}

func TestGuardSpans(t *testing.T) {
	g := NewGuard()
	spans := g.list.Spans()
	for i := 1; i < len(spans); i++ {
		if a, b := spans[i-1], spans[i]; a.End() > b.Start {
			t.Errorf("%s [0x%08X, 0x%08X) overlaps %s at 0x%08X", a.Name, a.Start, a.End(), b.Name, b.Start)
		}
	}
	for _, s := range spans {
		if err := mem.CheckRange(s.Start, len(s.Mask)); err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
		// The image and live memBit spans take the class masks, padding
		// and all; every other span is whole bytes.
		if s.Name != "dSv_save_c" && s.Name != "live memBit" && slices.Contains(s.Mask, 0) {
			t.Errorf("%s has a byte with no writable bit", s.Name)
		}
	}
}

func TestFrameWriterIsADryRun(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	w := &FrameWriter{Mem: m, Guard: NewGuard()}
	view := fullView()
	var raw world.Image
	ws, _ := NewPlanner(world.FullPolicy()).Plan(&view, &raw, &raw, addr.TableSea)
	if err := w.Apply(ws); !errors.Is(err, ErrDryRun) {
		t.Fatalf("Apply = %v, want ErrDryRun", err)
	}
	if n := len(m.Writes()); n != 0 {
		t.Fatalf("Apply wrote %d times", n)
	}
}

// TestPlannerBossHold: while the loaded stage is a boss stage, the
// reconciler's planner holds the loaded table's boss bits (bits 3 and 5
// of mDungeonItem) and plans the rest of the byte; another table's boss
// bits, which go to its saved slot only, are not held, and neither is
// anything in a dry run, M2's planner.
func TestPlannerBossHold(t *testing.T) {
	drcItems := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem
	fwItems := addr.OffMemBit(addr.TableFW) + addr.MemBitOffDungeonItem
	var view world.Image
	view[drcItems], view[fwItems] = 0x2B, 0x08
	cases := []struct {
		name   string
		writes bool
		boss   bool
		drc    byte // what is planned of DRC's byte
		held   byte
	}{
		{"a boss stage", true, true, 0x03, bossBits & 0x2B},
		{"a dungeon stage of the same table", true, false, 0x2B, 0},
		{"a dry run", false, true, 0x2B, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := NewPlanner(world.FullPolicy())
			p.writes = c.writes
			var now world.Image
			o := consistentObs(addr.TableDRC, now)
			o.boss = c.boss
			p.observe(o)
			ws, held := p.Plan(&view, &now, &now, addr.TableDRC)
			got := writesImage(ws, world.Image{})
			if got[drcItems] != c.drc || got[fwItems] != 0x08 {
				t.Fatalf("planned DRC 0x%02X, FW 0x%02X; want DRC 0x%02X and FW's boss bit", got[drcItems], got[fwItems], c.drc)
			}
			var h byte
			for _, b := range held {
				if int(b.Off) == drcItems {
					h |= b.Mask
					if b.Hold.Why == "" {
						t.Error("a hold without a reason")
					}
				}
			}
			if h != c.held {
				t.Fatalf("held 0x%02X of DRC's dungeon items, want 0x%02X", h, c.held)
			}
		})
	}
}

// TestBossStage: the stages that poll the boss bits every frame.
func TestBossStage(t *testing.T) {
	cases := []struct {
		name  string
		typ   int
		ok    bool
		table int8
		want  bool
	}{
		{"a BOSS-type stage", stageTypeBoss, true, addr.TableDRC, true},
		{"a dungeon", 1, true, addr.TableDRC, false},
		{"a miniboss room", 6, true, addr.TableDRC, false},
		{"the sea", 7, true, addr.TableSea, false},
		{"Forsaken Fortress's table, Helmaroc King's", 1, true, addr.TableFF, true},
		{"an unreadable STAG", 0, false, addr.TableSea, true},
	}
	for _, c := range cases {
		if got := bossStage(c.typ, c.ok, c.table); got != c.want {
			t.Errorf("%s: bossStage = %v, want %v", c.name, got, c.want)
		}
	}
	m := mem.NewFakeMem()
	const stag = 0x80A01000
	if err := m.Store(stag+addr.StagOffProp, []byte{byte(addr.TableDRC << 1), 0, 0, 0, 0x03}); err != nil {
		t.Fatal(err)
	}
	if table, typ, ok := readStag(m, stag); !ok || table != addr.TableDRC || typ != stageTypeBoss {
		t.Fatalf("readStag = %d, %d, %v", table, typ, ok)
	}
	if _, _, ok := readStag(m, 0); ok {
		t.Fatal("readStag of a null pointer")
	}
}
