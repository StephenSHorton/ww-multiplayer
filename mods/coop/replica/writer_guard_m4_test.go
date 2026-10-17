package replica

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/inventory"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// transitionGates is the gates' verdict on the first tick of a stage
// change from s, after a settled run: OnTransition.
func transitionGates(s state.Snapshot) gate.Status {
	var hist []*state.Snapshot
	for i := range gate.StableTicks + 2 {
		c := s
		c.Heartbeat = uint32(i + 1)
		hist = append(hist, &c)
	}
	hist[len(hist)-1].NextEnable = 1
	return gate.Evaluate(hist)
}

// syncedStory is a story bit of the SyncMask that no hold names, and
// localStory one of the class mask outside it (LocalOnly).
func syncedStory(t *testing.T) (off int, m byte) {
	t.Helper()
	held := map[uint16]bool{}
	for _, h := range class.Holds() {
		held[h.ID] = true
	}
	for i := range addr.EventBitBytes {
		if sm := class.SyncMaskOf(i); sm != 0 && !held[uint16(i)<<8|uint16(sm&-sm)] {
			return addr.OffEvent + i, sm & -sm
		}
	}
	t.Fatal("no synced story bit")
	return 0, 0
}

func localStory(t *testing.T) (off int, m byte) {
	t.Helper()
	for _, b := range class.LocalOnly() {
		e := addr.EventByte(b.ID)
		if class.Of(addr.OffEvent+e.Off).Mask&e.Mask == e.Mask {
			return addr.OffEvent + e.Off, e.Mask
		}
	}
	t.Fatal("no LocalOnly story bit inside the class mask")
	return 0, 0
}

// registerOff is an allowlisted MAX event register.
func registerOff(t *testing.T) int {
	t.Helper()
	for off := addr.OffEvent + addr.EventBitBytes; off < addr.SaveImageSize; off++ {
		if CategoryOf(off) == CatRegister && class.Of(off).Class == class.Max {
			return off
		}
	}
	t.Fatal("no MAX register")
	return 0
}

// m4Guard is a replica's writing guard: M3's targets, M4's and the
// escapes, over the session policy.
func m4Guard(gates gate.Status, armed bool) *WriteGuard {
	wg := NewWriteGuard(GuardConfig{Targets: M3Targets | M4Targets | TargetEscape, Policy: proto.SessionPolicy(nil)})
	wg.SetTick(Tick{Gates: gates, Armed: armed, Table: addr.TableSea})
	return wg
}

// TestWriteGuardM4Gates: each M4 class under its gate and not otherwise,
// as the FrameWriter's own paths ask.
func TestWriteGuardM4Gates(t *testing.T) {
	inGame := state.Snapshot{PlayerPtr: 0x80A00000, StageName: "sea", Table: addr.TableSea}
	idle := settledGates(inGame)
	busy := inGame
	busy.EvtMode = 1
	lifePending := inGame
	lifePending.MaxLifeCount = 4
	trans := transitionGates(inGame)
	if !idle.CanWriteIdle() || settledGates(busy).Idle || !trans.CanWriteOnTransition() || trans.Stable || idle.OnTransition {
		t.Fatal("bad gate fixtures")
	}
	story, sm := syncedStory(t)
	reg := registerOff(t)
	regMask := class.Of(reg).Mask
	bow := imageAddr(addr.OffGetItem + save.SlotBow)
	slot := imageAddr(addr.OffItems + save.SlotTelescope)
	cases := []struct {
		name     string
		gates    gate.Status
		armed    bool
		p        path
		a        uint32
		old, val byte
		ok       bool
	}{
		{"a story bit as a stage change starts", trans, true, pathMerge, imageAddr(story), 0, sm, true},
		{"a story bit on a settled tick", idle, true, pathMerge, imageAddr(story), 0, sm, false},
		{"a story bit as a stage change starts, unarmed", trans, false, pathMerge, imageAddr(story), 0, sm, false},
		{"a register as a stage change starts", trans, true, pathMerge, imageAddr(reg), 0, regMask & -regMask, true},
		{"a register when idle", idle, true, pathMerge, imageAddr(reg), 0, regMask & -regMask, false},
		{"an inventory flag when idle", idle, true, pathMerge, bow, 0, 0x01, true},
		{"an inventory flag during an event", settledGates(busy), true, pathMerge, bow, 0, 0x01, false},
		{"an inventory flag as a stage change starts", trans, true, pathMerge, bow, 0, 0x01, false},
		{"a capacity when idle", idle, true, pathMerge, imageAddr(addr.OffArrowMax), 0, 30, true},
		{"a slot when idle", idle, true, pathDerive, slot, save.ItemNone, save.ItemTelescope, true},
		{"a slot during an event", settledGates(busy), true, pathDerive, slot, save.ItemNone, save.ItemTelescope, false},
		{"the max-life counter at 0", idle, true, pathDerive, addr.ItemMaxLifeCount + 1, 0, 4, true},
		{"the max-life counter still pending", settledGates(lifePending), true, pathDerive, addr.ItemMaxLifeCount + 1, 0, 4, false},
		{"a button when idle", idle, true, pathDerive, addr.PlaySelectItem, save.ItemNone, save.ItemTelescope, true},
		{"a button as a stage change starts", trans, true, pathDerive, addr.PlaySelectItem, save.ItemNone, save.ItemTelescope, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := m4Guard(c.gates, c.armed).check(c.a, c.old, c.val, c.p)
			if (err == nil) != c.ok {
				t.Fatalf("check = %v, want ok %v", err, c.ok)
			}
			var re *RefusalError
			if !c.ok && (!errors.As(err, &re) || re.Reason != RefuseGate) {
				t.Fatalf("check = %v, want a gate refusal", err)
			}
		})
	}
}

// TestWriteGuardM4Refuses: every path a forbidden write can take is
// refused, whatever the gates say, and for the reason that names it.
func TestWriteGuardM4Refuses(t *testing.T) {
	inGame := state.Snapshot{PlayerPtr: 0x80A00000, StageName: "sea", Table: addr.TableSea}
	idle := settledGates(inGame)
	trans := transitionGates(inGame)
	local, lm := localStory(t)
	story, sm := syncedStory(t)
	bowSlot := imageAddr(addr.OffItems + save.SlotBow)
	sword := imageAddr(addr.OffSelectEquip + save.EquipSword)
	keyByte := imageAddr(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum)
	cases := []struct {
		name     string
		gates    gate.Status
		p        path
		a        uint32
		old, val byte
		want     Refusal
	}{
		// DERIVED bytes, the counters and the buttons off the derive path.
		{"a slot through the merge path", idle, pathMerge, bowSlot, save.ItemNone, save.ItemBow, RefusePath},
		{"the equipment through the merge path", idle, pathMerge, sword, save.ItemNone, save.ItemSword, RefusePath},
		{"a key count through the merge path", idle, pathMerge, keyByte, 0, 1, RefusePath},
		{"a counter through the merge path", idle, pathMerge, addr.ItemMaxLifeCount + 1, 0, 4, RefusePath},
		{"a counter through an escape", idle, pathEscape, addr.ItemKeyNumCount + 1, 0, 1, RefusePath},
		{"a button through the merge path", idle, pathMerge, addr.PlaySelectItem, save.ItemNone, save.ItemTelescope, RefusePath},
		// The merge path's bytes off it.
		{"a story bit through the derive path", trans, pathDerive, imageAddr(story), 0, sm, RefusePath},
		{"an inventory flag through an escape", idle, pathEscape, imageAddr(addr.OffGetItem + save.SlotBow), 0, 1, RefusePath},
		{"mNextStage through the derive path", idle, pathDerive, addr.NextStageEnable, 0, 1, RefusePath},
		// Never targets.
		{"mMaxLife", idle, pathDerive, imageAddr(addr.OffMaxLife + 1), 12, 16, RefuseTarget},
		{"mMaxMagic", idle, pathDerive, imageAddr(addr.OffMaxMagic), 0, 16, RefuseTarget},
		{"mLife", idle, pathAny, imageAddr(addr.OffLife + 1), 12, 0, RefuseTarget},
		{"rupees", idle, pathAny, imageAddr(addr.OffRupee + 1), 0, 99, RefuseTarget},
		{"current arrows", idle, pathAny, imageAddr(inventory.OffArrowNum), 0, 30, RefuseTarget},
		{"a bottle", idle, pathAny, imageAddr(addr.OffItems + 14), save.ItemNone, 0x50, RefuseTarget},
		{"mSelectEquip[3]", idle, pathAny, imageAddr(addr.OffSelectEquip + 3), 0, 1, RefuseTarget},
		{"play.mSelectItem[3]", idle, pathAny, addr.PlaySelectItem + 3, save.ItemNone, 0x20, RefuseTarget},
		{"the ammo counter", idle, pathAny, inventory.ItemArrowNumCount + 1, 0, 30, RefuseTarget},
		{"the tag (no target of a replica's guard)", idle, pathDerive, addr.Reserve, 0, 'W', RefuseTarget},
		// Bits outside the masks.
		{"a LocalOnly story bit", trans, pathMerge, imageAddr(local), 0, lm, RefuseMask},
		{"a bottle content held", idle, pathMerge, imageAddr(addr.OffGetItem), 0, 0x02, RefuseMask},
		{"the Hero's Charm worn", idle, pathMerge, imageAddr(addr.OffCollectFlags + 4), 0, 0x02, RefuseMask},
		// Clears and falls.
		{"a story bit cleared", trans, pathMerge, imageAddr(story), sm, 0, RefuseClear},
		{"an inventory flag cleared", idle, pathMerge, imageAddr(addr.OffGetItem + save.SlotBow), 1, 0, RefuseClear},
		{"a capacity lowered", idle, pathMerge, imageAddr(addr.OffArrowMax), 60, 30, RefuseClear},
		{"the wallet lowered", idle, pathMerge, imageAddr(addr.OffWalletSize), 2, 1, RefuseClear},
		{"a slot to a lesser variant", idle, pathDerive, imageAddr(addr.OffItems + save.SlotCamera), save.ItemDeluxePictoBox, save.ItemPictoBox, RefuseValue},
		{"a slot emptied", idle, pathDerive, bowSlot, save.ItemBow, save.ItemNone, RefuseValue},
		{"a slot to another slot's item", idle, pathDerive, bowSlot, save.ItemNone, save.ItemTelescope, RefuseValue},
		{"the sword to a lesser one", idle, pathDerive, sword, save.ItemMasterSword3, save.ItemSword, RefuseValue},
		{"a button to a lesser variant", idle, pathDerive, addr.PlaySelectItem + 1, save.ItemLightArrow, save.ItemBow, RefuseValue},
		{"a button to nothing", idle, pathDerive, addr.PlaySelectItem, save.ItemBow, save.ItemNone, RefuseValue},
		{"a key count above 99", idle, pathDerive, keyByte, 0, 100, RefuseValue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := m4Guard(c.gates, true).check(c.a, c.old, c.val, c.p)
			var re *RefusalError
			if !errors.As(err, &re) || re.Reason != c.want {
				t.Fatalf("check = %v, want a %v refusal", err, c.want)
			}
		})
	}

	// The tag is a bind's or a scrub's alone: no FrameWriter path writes
	// it, even under a guard of every target.
	all := NewWriteGuard(GuardConfig{Targets: AllTargets, Policy: proto.SessionPolicy(nil)})
	all.SetTick(Tick{Gates: idle, Armed: true, Table: addr.TableSea})
	for _, p := range []path{pathMerge, pathDerive, pathEscape} {
		var re *RefusalError
		if err := all.check(addr.Reserve, 0, 'W', p); !errors.As(err, &re) || re.Reason != RefusePath {
			t.Errorf("the tag through %v: %v, want a path refusal", p, err)
		}
	}
}

// TestWriteGuardCheckIsEveryPath: the exported Check judges a byte as
// written by its own path, so the derive package's routing (which asks
// byte by byte) still passes the derived bytes and counters.
func TestWriteGuardCheckIsEveryPath(t *testing.T) {
	inGame := state.Snapshot{PlayerPtr: 0x80A00000, StageName: "sea", Table: addr.TableSea}
	wg := m4Guard(settledGates(inGame), true)
	for _, c := range []struct {
		a        uint32
		old, val byte
	}{
		{addr.ItemKeyNumCount + 1, 0, 1},
		{imageAddr(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum), 0, 2},
		{imageAddr(addr.OffItems + save.SlotBow), save.ItemNone, save.ItemBow},
		{imageAddr(addr.OffGetItem + save.SlotBow), 0, 1},
	} {
		if err := wg.Check(c.a, c.old, c.val); err != nil {
			t.Errorf("Check(0x%08X) = %v", c.a, err)
		}
	}
}

// counterWrite is a pending-counter write of delta over 0.
func counterWrite(k DeriveKind, a uint32, table int, delta int16) DeriveWrite {
	d := make([]byte, 2)
	binary.BigEndian.PutUint16(d, uint16(delta))
	return DeriveWrite{Kind: k, Table: table, Addr: a, Expect: []byte{0, 0}, Data: d, Reason: "test"}
}

// TestWriteGuardDeriveShape: a derive write is let through only with its
// kind's address, size, expected bytes and delta.
func TestWriteGuardDeriveShape(t *testing.T) {
	inGame := state.Snapshot{PlayerPtr: 0x80A00000, StageName: "M_NewD2", Table: addr.TableDRC}
	idle := settledGates(inGame)
	savedFW, _ := addr.SavedMemBitAddr(addr.TableFW)
	savedDRC, _ := addr.SavedMemBitAddr(addr.TableDRC)
	one := func(k DeriveKind, a uint32, table int, old, val byte) DeriveWrite {
		return DeriveWrite{Kind: k, Table: table, Addr: a, Expect: []byte{old}, Data: []byte{val}, Reason: "test"}
	}
	cases := []struct {
		name string
		w    DeriveWrite
		want Refusal // 0: let through
	}{
		{"a key into the counter", counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, 1), 0},
		{"a key out of the counter", counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, -1), 0},
		{"a key counter for a table not loaded", counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableFW, 1), RefuseTarget},
		{"a key counter delta of 0", counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, 0), RefuseValue},
		{"a key counter delta of 100", counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, 100), RefuseValue},
		{"a heart piece", counterWrite(DeriveLifeCounter, addr.ItemMaxLifeCount, -1, 1), 0},
		{"max life lowered", counterWrite(DeriveLifeCounter, addr.ItemMaxLifeCount, -1, -4), RefuseValue},
		{"max life past the cap", counterWrite(DeriveLifeCounter, addr.ItemMaxLifeCount, -1, derive.LifeCap+1), RefuseValue},
		{"max magic", counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicLeaf), 0},
		{"max magic lowered", counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, -16), RefuseValue},
		{"max magic past the cap", counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicCap+1), RefuseValue},
		{"a life delta into the key counter", counterWrite(DeriveLifeCounter, addr.ItemKeyNumCount, -1, 1), RefuseTarget},
		{"a counter over a pending count", DeriveWrite{Kind: DeriveMagicCounter, Table: -1, Addr: addr.ItemMaxMagicCount,
			Expect: []byte{0, 1}, Data: []byte{0, 0x11}}, RefuseValue},
		{"half a counter", DeriveWrite{Kind: DeriveLifeCounter, Table: -1, Addr: addr.ItemMaxLifeCount + 1,
			Expect: []byte{0}, Data: []byte{4}}, RefuseTarget},
		{"the loaded table's live key byte", one(DeriveKeyLive, addr.LiveKeyNum, addr.TableDRC, 0, 2), 0},
		{"the live key byte for another table", one(DeriveKeyLive, addr.LiveKeyNum, addr.TableFW, 0, 2), RefuseTarget},
		{"another table's saved key byte", one(DeriveKeySaved, savedFW+addr.MemBitOffKeyNum, addr.TableFW, 0, 1), 0},
		{"the loaded table's saved key byte", one(DeriveKeySaved, savedDRC+addr.MemBitOffKeyNum, addr.TableDRC, 0, 1), RefuseTarget},
		{"a saved key byte at another table's place", one(DeriveKeySaved, savedDRC+addr.MemBitOffKeyNum, addr.TableFW, 0, 1), RefuseTarget},
		{"a slot", one(DeriveSlot, imageAddr(addr.OffItems+save.SlotBow), -1, save.ItemNone, save.ItemBow), 0},
		{"the equipment as a slot", one(DeriveSlot, imageAddr(addr.OffSelectEquip), -1, save.ItemNone, save.ItemSword), RefuseTarget},
		{"a slot as the equipment", one(DeriveEquip, imageAddr(addr.OffItems+save.SlotBow), -1, save.ItemNone, save.ItemBow), RefuseTarget},
		{"the sword", one(DeriveEquip, imageAddr(addr.OffSelectEquip), -1, save.ItemNone, save.ItemSword), 0},
		{"a bottle as a slot", one(DeriveSlot, imageAddr(addr.OffItems+14), -1, save.ItemNone, 0x50), RefuseTarget},
		{"a button", one(DeriveButton, addr.PlaySelectItem+2, -1, save.ItemNone, save.ItemTelescope), 0},
		{"mSelectItem[3] as a button", one(DeriveButton, addr.PlaySelectItem+3, -1, save.ItemNone, save.ItemTelescope), RefuseTarget},
		{"mMaxLife as a slot", one(DeriveSlot, imageAddr(addr.OffMaxLife+1), -1, 12, 16), RefuseTarget},
		{"the tag", one(DeriveSlot, addr.Reserve, -1, 0, 'W'), RefuseTarget},
		{"no kind", one(0, imageAddr(addr.OffItems), -1, save.ItemNone, save.ItemTelescope), RefuseTarget},
		{"expect and data of other sizes", DeriveWrite{Kind: DeriveSlot, Table: -1, Addr: imageAddr(addr.OffItems + save.SlotBow),
			Expect: []byte{0xFF, 0xFF}, Data: []byte{save.ItemBow}}, RefuseTarget},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wg := NewWriteGuard(GuardConfig{Targets: M3Targets | M4Targets | TargetEscape, Policy: proto.SessionPolicy(nil)})
			wg.SetTick(Tick{Gates: idle, Armed: true, Table: addr.TableDRC})
			errShape := wg.checkDeriveShape(c.w)
			err := wg.checkDerive(c.w, c.w.Expect)
			var re *RefusalError
			switch {
			case c.want == 0 && (err != nil || errShape != nil):
				t.Fatalf("checkDerive = %v, checkDeriveShape = %v, want both nil", err, errShape)
			case c.want != 0 && (!errors.As(err, &re) || re.Reason != c.want):
				t.Fatalf("checkDerive = %v, want a %v refusal", err, c.want)
			case c.want != 0 && !errors.Is(errShape, ErrGuard):
				t.Fatalf("checkDeriveShape = %v, want a refusal", errShape)
			}
		})
	}

	// A derive write over bytes that no longer read what it expects is
	// refused, though the shape is good.
	wg := NewWriteGuard(GuardConfig{Targets: M3Targets | M4Targets, Policy: proto.SessionPolicy(nil)})
	wg.SetTick(Tick{Gates: idle, Armed: true, Table: addr.TableDRC})
	w := counterWrite(DeriveLifeCounter, addr.ItemMaxLifeCount, -1, 4)
	var re *RefusalError
	if err := wg.checkDerive(w, []byte{0, 1}); !errors.As(err, &re) || re.Reason != RefuseValue {
		t.Fatalf("checkDerive over a pending count = %v, want a value refusal", err)
	}
}

// TestM4GuardAllowlist walks the image: M4's targets hold exactly the
// story bits of the SyncMask, the allowlisted registers, the inventory's
// flag bits and capacities and the DERIVED bytes the receiver derives,
// and nothing PER_PLAYER, NEVER or TAG.
func TestM4GuardAllowlist(t *testing.T) {
	g := NewGuardFor(M4Targets)
	for off := range addr.SaveImageSize {
		r := class.Of(off)
		got := g.Mask(addr.SaveImage + uint32(off))
		var want byte
		switch c := CategoryOf(off); {
		case r.Class == class.Derived:
			if derivedSlotAt(off) >= 0 || isKeyByte(off) {
				want = 0xFF
			}
		case !r.Class.Merges():
		case c == CatStory:
			want = r.Mask & class.SyncMaskOf(off-addr.OffEvent)
		case c == CatRegister, c == CatCapacity:
			want = r.Mask
		case c == CatInventory:
			want = r.Mask & inventory.FlagMask(off)
		}
		if got != want {
			t.Errorf("%s: M4 mask 0x%02X, want 0x%02X", DefaultLabel(off, -1), got, want)
		}
		switch r.Class {
		case class.PerPlayer, class.Never, class.Tag:
			if got != 0 {
				t.Errorf("%s, a %v byte, is an M4 target", DefaultLabel(off, -1), r.Class)
			}
		}
		if got != 0 && !inventory.Owns(off) && CategoryOf(off) != CatStory && CategoryOf(off) != CatRegister && !isKeyByte(off) {
			t.Errorf("%s is an M4 target that neither story, inventory nor the keys write", DefaultLabel(off, -1))
		}
	}
	if !g.Allows(addr.ItemKeyNumCount, 4) || !g.Allows(addr.ItemMaxMagicCount, 2) || !g.Allows(addr.PlaySelectItem, inventory.Buttons) {
		t.Error("the counters or the buttons are not M4 targets")
	}
	for _, a := range []uint32{imageAddr(addr.OffMaxLife), imageAddr(addr.OffMaxLife + 1), imageAddr(addr.OffMaxMagic),
		addr.PlaySelectItem + inventory.Buttons, inventory.ItemArrowNumCount, bind.TagRegion.Start} {
		if g.Mask(a) != 0 {
			t.Errorf("0x%08X is an M4 target", a)
		}
	}
	// The M4 receiver's merge-path writes come from story's and
	// inventory's plans, the planner's agree: a full world plans nothing
	// the guard refuses.
	view := fullView()
	var raw world.Image
	ws, _ := NewPlanner(world.FullPolicy()).Plan(&view, &raw, &raw, addr.TableSea)
	full := NewGuardFor(AllTargets)
	for _, w := range ws {
		if m := full.Mask(w.Saved); m == 0 || !w.Max && w.Bits&^m != 0 {
			t.Errorf("planned %+v outside the guard (mask 0x%02X)", w, m)
		}
	}
}
