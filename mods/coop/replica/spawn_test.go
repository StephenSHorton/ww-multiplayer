package replica

import (
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Ganon's Tower's stages, all of save table 8.
var (
	ganonL = fakegame.Stage{Name: "GanonL", Table: addr.TableGT}
	ganonK = fakegame.Stage{Name: "GanonK", Table: addr.TableGT}
	gTower = fakegame.Stage{Name: "GTower", Table: addr.TableGT}
)

// TestSpawnSwitchBits: the pairs are the switches the live logs name:
// t8 sw30 at 0x803C50AC mask 0x40 (D2(9), runbook-batch2-bosses §3) and
// sw31 the bit above it.
func TestSpawnSwitchBits(t *testing.T) {
	for _, c := range []struct {
		s    spawnSwitch
		a    uint32
		mask byte
	}{
		{spawnSwitches[0], 0x803C50AC, 0x40},
		{spawnSwitches[1], 0x803C50AC, 0x80},
	} {
		off, m := c.s.bit()
		if a := addr.SaveImage + uint32(off); a != c.a || m != c.mask {
			t.Errorf("%s: 0x%08X mask 0x%02X, want 0x%08X mask 0x%02X", c.s.what, a, m, c.a, c.mask)
		}
		if b := addr.EventByte(c.s.event); b.Mask == 0 {
			t.Errorf("%s: event 0x%04X has no bit", c.s.what, c.s.event)
		}
	}
}

// TestPlannerSpawnSwitchHold: a start event's spawn switch waits for its
// story bit in the receiver's own image, while the other switches the
// event set (the GanonK warp pot's sw7) go as usual.
func TestPlannerSpawnSwitchHold(t *testing.T) {
	sw30, m30 := spawnSwitches[0].bit()
	sw31, m31 := spawnSwitches[1].bit()
	sw7, m7 := switchImageByte(addr.TableGT, 0x07)
	o3B02, m3B02 := eventBit(ev3B02)
	o4002, m4002 := eventBit(ev4002)
	cases := []struct {
		name         string
		local        []uint16 // the receiver's story bits
		planned      byte     // of sw30/sw31's byte
		held30, held bool
	}{
		{"neither bit (GT D2(9) R4: the switches came first)", nil, 0, true, true},
		{"the receiver has 0x3B02 (R1, after the transition)", []uint16{ev3B02}, m30, false, true},
		{"and 0x4002", []uint16{ev3B02, ev4002}, m30 | m31, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var view, raw save.Image
			view[sw30] |= m30 | m31
			view[sw7] |= m7
			view[o3B02] |= m3B02
			view[o4002] |= m4002
			for _, id := range c.local {
				off, m := eventBit(id)
				raw[off] |= m
			}
			local := world.Shared(&raw)
			ws, held := NewPlanner(world.FullPolicy()).Plan(&view, &raw, &local, addr.TableHyrule)
			got := writesImage(ws, save.Image{})
			if got[sw30] != c.planned || got[sw7] != m7 {
				t.Fatalf("planned sw30/31 byte 0x%02X and sw7 0x%02X, want 0x%02X and 0x%02X", got[sw30], got[sw7], c.planned, m7)
			}
			var h save.Image
			for _, b := range held {
				if b.Hold.Why == "" {
					t.Error("a hold without a reason")
				}
				h[b.Off] |= b.Mask
			}
			if (h[sw30]&m30 != 0) != c.held30 || (h[sw31]&m31 != 0) != c.held {
				t.Fatalf("held 0x%02X of the byte, want sw30 %v, sw31 %v", h[sw30], c.held30, c.held)
			}
		})
	}
}

// gtPolicy is the session policy with t8 sw7, sw30 and sw31 synced, as
// the stage tables mark them.
func gtPolicy() *world.Policy {
	return proto.SessionPolicy(func(table, no int) bool {
		return table == addr.TableGT && (no == 0x07 || no == 0x1E || no == 0x1F)
	})
}

const gtFeatures = proto.FeatureImage | proto.FeatureStory | proto.FeatureSwitches

// TestReconcileSpawnSwitchAheadOfItsBit is GT D2(9)'s R4 order: the
// partner's sw30 (and sw7) reach the world while its intro still runs and
// 0x3B02 does not; the receiver enters GanonL and then GanonK. sw7 is
// written, sw30 never: the receiver enters GanonK with neither, so its
// own intro plays on layer 8.
func TestReconcileSpawnSwitchAheadOfItsBit(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: gtPolicy()}, gtFeatures)
	sw30, m30 := spawnSwitches[0].bit()
	sw7, m7 := switchImageByte(addr.TableGT, 0x07)
	o3B02, m3B02 := eventBit(ev3B02)
	var img save.Image
	img[sw30] |= m30
	img[sw7] |= m7
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	if rr.savedByte(sw7)&m7 == 0 {
		t.Fatalf("sw7 not written to table 8's slot:\n%s", rr.log)
	}
	if !rr.log.has("holding t08 GT switch 0x1E back") {
		t.Fatalf("no hold logged:\n%s", rr.log)
	}
	for _, s := range []fakegame.Stage{ganonL, ganonK} {
		rr.changeStage(s)
		rr.settleRooms()
		if rr.savedByte(sw30)&m30 != 0 || rr.liveByte(sw30)&m30 != 0 {
			t.Fatalf("in %s: sw30 written ahead of 0x3B02:\n%s", s.Name, rr.log)
		}
	}
	if rr.savedByte(o3B02)&m3B02 != 0 {
		t.Fatal("0x3B02 set, the world has none")
	}
	rr.noGainsUp("after GanonK")

	// The live rerun's order goes on (gt-R4b): A's intro ends and its
	// 0x3B02 reaches the world while B is in GanonK; B's next stage
	// change, back to GanonL, writes the bit and the switch together.
	img[o3B02] |= m3B02
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	if rr.liveByte(sw30)&m30 != 0 {
		t.Fatal("sw30 written in GanonK ahead of 0x3B02")
	}
	n := len(rr.bm.written())
	tr := rr.changeStage(ganonL)
	ws := rr.writesSince(n)
	if len(ws) != 3 || ws[0].a != addr.SaveImage+uint32(o3B02) {
		t.Fatalf("the stage change wrote %+v, want 0x3B02, then sw30 live and saved", ws)
	}
	for _, w := range ws {
		if w.tick != tr {
			t.Fatalf("a write on tick %d, the transition is tick %d", w.tick, tr)
		}
	}
	if rr.liveByte(sw30)&m30 == 0 || rr.savedByte(o3B02)&m3B02 == 0 {
		t.Fatal("GanonL loaded without the pair")
	}
	rr.auditM4(false)
}

// TestReconcileSpawnSwitchWithItsBit is R1's order: the world has sw30
// and 0x3B02 before the receiver's next stage change, which writes both
// on its first tick, the bit first: the receiver reaches GanonL, and then
// GanonK, in the vanilla retry state. And GTower's from GanonK, table 8
// loaded: 0x4002 and sw31, saved and live, on GTower's transition.
func TestReconcileSpawnSwitchWithItsBit(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: gtPolicy()}, gtFeatures)
	sw30, m30 := spawnSwitches[0].bit()
	sw31, m31 := spawnSwitches[1].bit()
	o3B02, m3B02 := eventBit(ev3B02)
	o4002, m4002 := eventBit(ev4002)
	var img save.Image
	img[sw30] |= m30
	img[o3B02] |= m3B02
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	if ws := rr.bm.written(); len(ws) != 0 {
		t.Fatalf("written before the stage change: %+v", ws)
	}

	tr := rr.changeStage(ganonL)
	ws := rr.bm.written()
	if len(ws) != 2 || ws[0].a != addr.SaveImage+uint32(o3B02) || ws[1].a != addr.SaveImage+uint32(sw30) {
		t.Fatalf("the stage change wrote %+v, want 0x3B02 and then sw30's saved byte", ws)
	}
	for _, w := range ws {
		if w.tick != tr {
			t.Fatalf("a write on tick %d, the transition is tick %d", w.tick, tr)
		}
	}
	if rr.liveByte(sw30)&m30 == 0 {
		t.Fatal("GanonL loaded table 8 without sw30")
	}
	if !rr.log.has("and with its story bit, t08 GT switch 0x1E") {
		t.Fatalf("log:\n%s", rr.log)
	}
	n := len(rr.bm.written())
	rr.settleRooms()
	rr.changeStage(ganonK)
	rr.settleRooms()
	if ws := rr.writesSince(n); len(ws) != 0 {
		t.Fatalf("wrote again: %+v", ws)
	}

	// The partner's g2before while this game is in GanonK.
	img[sw31] |= m31
	img[o4002] |= m4002
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	if ws := rr.writesSince(n); len(ws) != 0 {
		t.Fatalf("written in GanonK: %+v", ws)
	}
	tr = rr.changeStage(gTower)
	ws = rr.writesSince(n)
	if len(ws) != 3 || ws[0].a != addr.SaveImage+uint32(o4002) {
		t.Fatalf("GTower's stage change wrote %+v, want 0x4002, then sw31 live and saved", ws)
	}
	for _, w := range ws {
		if w.tick != tr {
			t.Fatalf("a write on tick %d, the transition is tick %d", w.tick, tr)
		}
	}
	if rr.liveByte(sw31)&m31 == 0 || rr.savedByte(sw31)&m31 == 0 {
		t.Fatal("GTower loaded without sw31")
	}
	rr.noGainsUp("after GTower")
	rr.auditM4(false)
}

// TestGuardSpawnSwitchOnTransition: on a TRANSITION tick, whose STABLE is
// closed, the guard lets a spawn switch bit through, saved and live, and
// no other bit of its byte; on any other tick STABLE rules it as usual.
func TestGuardSpawnSwitchOnTransition(t *testing.T) {
	sw30, m30 := spawnSwitches[0].bit()
	_, m25 := switchImageByte(addr.TableGT, 0x19)
	wg := NewWriteGuard(GuardConfig{Policy: gtPolicyWith25()})
	tr := gate.Status{OnTransition: true, Heartbeat: true, StableWhy: "a stage change is pending"}
	saved := addr.SaveImage + uint32(sw30)
	_, rel, _ := memBitTable(sw30)
	live := addr.LiveMemBit + uint32(rel)

	wg.SetTick(Tick{Gates: tr, Armed: true, Table: addr.TableGT})
	for _, a := range []uint32{saved, live} {
		if err := wg.Check(a, 0, m30); err != nil {
			t.Errorf("0x%08X: sw30 on the transition tick refused: %v", a, err)
		}
		if err := wg.Check(a, 0, m25); err == nil {
			t.Errorf("0x%08X: sw25 on the transition tick let through", a)
		}
	}
	wg.SetTick(Tick{Gates: tr, Armed: false, Table: addr.TableGT})
	if err := wg.Check(saved, 0, m30); err == nil {
		t.Error("an unarmed transition let sw30 through")
	}
	wg.SetTick(Tick{Gates: gate.Status{Heartbeat: true, StableWhy: "settling"}, Armed: true, Table: addr.TableGT})
	if err := wg.Check(saved, 0, m30); err == nil {
		t.Error("sw30 let through with STABLE closed off a transition")
	}
}

// gtPolicyWith25 is gtPolicy with sw25 synced too, a bit of sw30's byte.
func gtPolicyWith25() *world.Policy {
	return proto.SessionPolicy(func(table, no int) bool {
		return table == addr.TableGT && (no == 0x07 || no == 0x19 || no == 0x1E || no == 0x1F)
	})
}

// TestFrameWriterSpawnBitOfAMergedByte: on a stage change's first tick a
// batch whose byte merged sw30 with another synced switch of its byte
// (sw25) writes sw30 alone; sw25 waits for STABLE as a closed gate, no
// refusal and no fault. A lone sw25 on that tick is closed too.
func TestFrameWriterSpawnBitOfAMergedByte(t *testing.T) {
	r := newWriterRig(t, GuardConfig{Policy: gtPolicyWith25()})
	if err := r.g.SetNextStage(ganonL); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if !r.st.OnTransition {
		t.Fatalf("no transition tick: %s", r.st.Why())
	}
	sw30, m30 := spawnSwitches[0].bit()
	_, m25 := switchImageByte(addr.TableGT, 0x19)
	a := addr.SaveImage + uint32(sw30)

	res, err := r.apply(plannedOR(sw30, m25, addr.TableSea), plannedOR(sw30, m30, addr.TableSea))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := r.byteAt(a); got != m30 {
		t.Fatalf("byte 0x%02X, want sw30 alone (0x%02X)", got, m30)
	}
	if res.Closed != 1 || res.Refused != 0 || r.wg.Stats().Refused != 0 || r.brk.Err() != nil {
		t.Fatalf("closed %d, refused %d (guard %d), breaker %v; want sw25 closed and nothing refused", res.Closed, res.Refused, r.wg.Stats().Refused, r.brk.Err())
	}

	res, err = r.apply(plannedOR(sw30, m25, addr.TableSea))
	if !errors.Is(err, ErrClosed) || len(res.Wrote) != 0 || r.wg.Stats().Refused != 0 {
		t.Fatalf("a lone sw25: %v, wrote %+v, refused %d; want closed", err, res.Wrote, r.wg.Stats().Refused)
	}
}

// TestPlannerSpawnBitHold: the story bit waits while the receiver lacks
// the switch and the plan cannot write it with the bit: the world lacks
// it, the policy does not open it, or the hub does not grant switches.
func TestPlannerSpawnBitHold(t *testing.T) {
	sw30, m30 := spawnSwitches[0].bit()
	o3B02, m3B02 := eventBit(ev3B02)
	noSw30 := world.FullPolicy()
	noSw30[sw30] &^= m30
	cases := []struct {
		name       string
		world      bool // the world has sw30
		local      bool // the receiver has sw30
		pol        *world.Policy
		noSwitches bool
		held       bool
	}{
		{"the switch comes with it", true, false, world.FullPolicy(), false, false},
		{"the world lacks the switch", false, false, world.FullPolicy(), false, true},
		{"the switch does not sync", true, false, noSw30, false, true},
		{"switches not granted", true, false, world.FullPolicy(), true, true},
		{"the receiver has the switch", false, true, world.FullPolicy(), true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var view, raw save.Image
			view[o3B02] = m3B02
			if c.world {
				view[sw30] = m30
			}
			if c.local {
				raw[sw30] = m30
			}
			p := NewPlanner(c.pol)
			p.noSwitches = c.noSwitches
			local := world.Shared(&raw)
			ws, held := p.Plan(&view, &raw, &local, addr.TableHyrule)
			planned := writesImage(ws, save.Image{})[o3B02]&m3B02 != 0
			isHeld := false
			for _, h := range held {
				if int(h.Off) == o3B02 && h.Mask&m3B02 != 0 {
					isHeld = true
				}
			}
			if isHeld != c.held || planned == c.held {
				t.Fatalf("0x3B02 held %v, planned %v; want held %v", isHeld, planned, c.held)
			}
		})
	}
}

// TestReconcileSpawnBitWithoutItsSwitch: with story on and switches off,
// and with the bit reaching the world before the switch, the stage change
// writes neither, logs the hold once, and the receiver enters GanonK with
// neither (its own intro). Once the switch is in the world as well (the
// switches granted case), the next stage change writes both.
func TestReconcileSpawnBitWithoutItsSwitch(t *testing.T) {
	sw30, m30 := spawnSwitches[0].bit()
	o3B02, m3B02 := eventBit(ev3B02)
	t.Run("switches not granted", func(t *testing.T) {
		rr := newRecRig(t, Config{Apply: true, Policy: gtPolicy()}, proto.FeatureImage|proto.FeatureStory)
		var img save.Image
		img[sw30], img[o3B02] = m30, m3B02
		rr.world(img)
		rr.ticks(gate.StableTicks + 2)
		for _, s := range []fakegame.Stage{ganonL, ganonK} {
			rr.changeStage(s)
			rr.settleRooms()
		}
		if ws := rr.bm.written(); len(ws) != 0 {
			t.Fatalf("written: %+v", ws)
		}
		if rr.log.count("holding event 0x3B02 back") != 1 {
			t.Fatalf("no hold logged:\n%s", rr.log)
		}
		rr.auditM4(false)
	})
	t.Run("the bit before the switch", func(t *testing.T) {
		rr := newRecRig(t, Config{Apply: true, Policy: gtPolicy()}, gtFeatures)
		var img save.Image
		img[o3B02] = m3B02
		rr.world(img)
		rr.ticks(gate.StableTicks + 2)
		rr.changeStage(ganonL)
		rr.settleRooms()
		if ws := rr.bm.written(); len(ws) != 0 {
			t.Fatalf("0x3B02 written without sw30: %+v", ws)
		}
		img[sw30] = m30
		rr.world(img)
		rr.ticks(gate.StableTicks + 2)
		if ws := rr.bm.written(); len(ws) != 0 {
			t.Fatalf("written in GanonL: %+v", ws)
		}
		tr := rr.changeStage(ganonK)
		ws := rr.bm.written()
		if len(ws) != 3 || ws[0].a != addr.SaveImage+uint32(o3B02) {
			t.Fatalf("GanonK's stage change wrote %+v, want 0x3B02, then sw30 live and saved", ws)
		}
		for _, w := range ws {
			if w.tick != tr {
				t.Fatalf("a write on tick %d, the transition is tick %d", w.tick, tr)
			}
		}
		rr.auditM4(false)
	})
}

// TestReconcileOwnIntroWhileHeld: B enters GanonK while sw30 is held (the
// world has the switch, not the bit), plays its own intro, which sets
// sw30 and 0x3B02 in its own game, and nothing is written afterwards,
// before or after the partner's 0x3B02 reaches the world.
func TestReconcileOwnIntroWhileHeld(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: gtPolicy()}, gtFeatures)
	sw30, m30 := spawnSwitches[0].bit()
	o3B02, m3B02 := eventBit(ev3B02)
	var img save.Image
	img[sw30] = m30
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	rr.changeStage(ganonK)
	if rr.liveByte(sw30)&m30 != 0 {
		t.Fatal("GanonK loaded with sw30: the intro would be skipped")
	}
	// The intro: setStartDemo sets sw30, the STB 0x3B02.
	rr.onSwitch(0x1E, 0)
	rr.g.OnEventBit(ev3B02)
	n := len(rr.bm.written())
	rr.settleRooms()
	img[o3B02] = m3B02
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	rr.changeStage(ganonL)
	rr.settleRooms()
	if ws := rr.writesSince(n); len(ws) != 0 {
		t.Fatalf("written after B's own intro: %+v", ws)
	}
	rr.auditM4(false)
}
