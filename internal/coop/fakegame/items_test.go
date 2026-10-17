package fakegame

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestItemGets: each item get sets its flag and hands its item over the
// way the game's does, the counted ones through the HUD's pending
// counters, which the next frame takes.
func TestItemGets(t *testing.T) {
	tbox := func(t *testing.T, m mem.Mem, no int) bool {
		t.Helper()
		b, err := addr.TboxBit(no)
		if err != nil {
			t.Fatal(err)
		}
		return u8(t, m, addr.LiveMemBit+uint32(b.Off))&b.Mask != 0
	}
	cases := []struct {
		name  string
		at    Stage
		do    func(g *FakeGame) error
		check func(t *testing.T, g *FakeGame)
	}{
		{"a key chest", drc, func(g *FakeGame) error { return g.OpenKeyChest(1) }, func(t *testing.T, g *FakeGame) {
			if !tbox(t, g.m, 1) || u8(t, g.m, addr.LiveKeyNum) != 1 || u16(t, g.m, addr.ItemKeyNumCount) != 0 {
				t.Errorf("tbox %v, keys %d", tbox(t, g.m, 1), u8(t, g.m, addr.LiveKeyNum))
			}
		}},
		{"a key chest where the key counter is hidden", outset, func(g *FakeGame) error { return g.OpenKeyChest(1) }, func(t *testing.T, g *FakeGame) {
			if u8(t, g.m, addr.LiveKeyNum) != 0 || u16(t, g.m, addr.ItemKeyNumCount) != 1 {
				t.Errorf("keys %d, pending %d: the count waits for a stage that shows it", u8(t, g.m, addr.LiveKeyNum), u16(t, g.m, addr.ItemKeyNumCount))
			}
		}},
		{"a key and its door", drc, func(g *FakeGame) error {
			if err := g.OpenKeyChest(2); err != nil {
				return err
			}
			g.Frame()
			return g.UnlockKeyDoor(0x10)
		}, func(t *testing.T, g *FakeGame) {
			if !switchSet(t, g.m, 0x10, -1) || u8(t, g.m, addr.LiveKeyNum) != 0 {
				t.Errorf("switch %v, keys %d", switchSet(t, g.m, 0x10, -1), u8(t, g.m, addr.LiveKeyNum))
			}
		}},
		{"a heart piece", outset, func(g *FakeGame) error { return g.OpenPieceChest(0) }, func(t *testing.T, g *FakeGame) {
			if !tbox(t, g.m, 0) || u16(t, g.m, addr.MaxLife) != 13 {
				t.Errorf("tbox %v, max life %d", tbox(t, g.m, 0), u16(t, g.m, addr.MaxLife))
			}
		}},
		{"the bow and its quiver", outset, func(g *FakeGame) error {
			if err := g.GetItem(addr.SlotBow, 0, 0x27); err != nil {
				return err
			}
			return g.SetCapacity(addr.OffArrowMax, 30)
		}, func(t *testing.T, g *FakeGame) {
			if u8(t, g.m, addr.SaveImage+uint32(addr.OffGetItem+addr.SlotBow)) != 0x01 ||
				u8(t, g.m, addr.SaveImage+uint32(addr.OffItems+addr.SlotBow)) != 0x27 ||
				u8(t, g.m, addr.SaveImage+uint32(addr.OffArrowMax)) != 30 {
				t.Error("the bow's flag, slot or capacity is missing")
			}
		}},
		{"the Deku Leaf", outset, (*FakeGame).GetDekuLeaf, func(t *testing.T, g *FakeGame) {
			if u8(t, g.m, addr.SaveImage+uint32(addr.OffGetItem+6)) != 0x01 || u8(t, g.m, addr.SaveImage+uint32(addr.OffItems+6)) != 0x34 ||
				u8(t, g.m, addr.MaxMagic) != 16 {
				t.Errorf("max magic %d", u8(t, g.m, addr.MaxMagic))
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _ := newGame(t)
			if err := g.NewGame(0, "Link"); err != nil {
				t.Fatal(err)
			}
			load(t, g, c.at)
			if err := c.do(g); err != nil {
				t.Fatal(err)
			}
			g.Frame()
			c.check(t, g)
		})
	}
}

func TestItemGetRefusals(t *testing.T) {
	g, _ := newGame(t)
	if err := g.OpenKeyChest(1); err == nil {
		t.Error("a chest opened with no stage loaded")
	}
	if err := g.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	load(t, g, drc)
	for name, err := range map[string]error{
		"a chest past tbox 63":    g.OpenKeyChest(64),
		"a door on a room switch": g.UnlockKeyDoor(0xC0),
		"a slot past the last":    g.GetItem(addr.ItemsSize, 0, 1),
		"a ninth flag bit":        g.GetItem(0, 8, 1),
		"the bottle as capacity":  g.SetCapacity(addr.OffItems, 1),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestStageFacts: the stage type and the partner are the live facts the
// receiver reads beyond the save.
func TestStageFacts(t *testing.T) {
	g, m := newGame(t)
	if err := g.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	load(t, g, drc)
	prop := u8(t, m, StagChunk+addr.StagOffProp)
	g.SetStageType(2)
	if got := u8(t, m, StagChunk+0x0D) & 7; got != 2 || u8(t, m, StagChunk+addr.StagOffProp) != prop {
		t.Errorf("stage type %d, mProp 0x%02X (was 0x%02X)", got, u8(t, m, StagChunk+addr.StagOffProp), prop)
	}
	g.SetPartner(true)
	if got := u32(t, m, addr.PlayerPtr+4); got != PartnerActor {
		t.Errorf("mpPlayerPtr[1] = 0x%08X", got)
	}
	g.SetPartner(false)
	if got := u32(t, m, addr.PlayerPtr+4); got != 0 {
		t.Errorf("mpPlayerPtr[1] = 0x%08X after the partner left", got)
	}
}
