package fakegame

import (
	"bytes"
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Stages the tests load. The names are only labels; the tables are the
// ones the real stages use.
var (
	outset  = Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	drc     = Stage{Name: "M_NewD2", Table: addr.TableDRC, KeyHUD: true}
	drcBoss = Stage{Name: "M_DragB", Table: addr.TableDRC, KeyHUD: true}
)

func newGame(t *testing.T) (*FakeGame, *mem.FakeMem) {
	t.Helper()
	m := mem.NewFakeMem()
	return New(m), m
}

func load(t *testing.T, g *FakeGame, s Stage) {
	t.Helper()
	if err := g.LoadStage(s); err != nil {
		t.Fatalf("LoadStage(%s): %v", s.Name, err)
	}
}

func enter(t *testing.T, g *FakeGame, room int, neighbours ...int) {
	t.Helper()
	if err := g.EnterRoom(room, neighbours...); err != nil {
		t.Fatalf("EnterRoom(%d): %v", room, err)
	}
}

func onSwitch(t *testing.T, g *FakeGame, no, room int) {
	t.Helper()
	if err := g.OnSwitch(no, room); err != nil {
		t.Fatalf("OnSwitch(0x%X, %d): %v", no, room, err)
	}
}

func u8(t *testing.T, m mem.Mem, a uint32) uint8 {
	t.Helper()
	v, err := mem.ReadU8(m, a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func u16(t *testing.T, m mem.Mem, a uint32) uint16 {
	t.Helper()
	v, err := mem.ReadU16(m, a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func u32(t *testing.T, m mem.Mem, a uint32) uint32 {
	t.Helper()
	v, err := mem.ReadU32(m, a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func read(t *testing.T, m mem.Mem, a uint32, n int) []byte {
	t.Helper()
	b, err := m.Read(a, n)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// switchSet reports whether switch no is set in the store it routes to,
// reading memory directly rather than through the fake.
func switchSet(t *testing.T, m mem.Mem, no, room int) bool {
	t.Helper()
	store, bit, err := addr.SwitchBit(no)
	if err != nil {
		t.Fatal(err)
	}
	var base uint32
	switch store {
	case addr.SwitchMemory:
		base = addr.LiveMemBit
	case addr.SwitchDan:
		base = addr.Dan
	default:
		za, _ := addr.ZoneNoAddr(room)
		base = zoneAddr(int(int8(u8(t, m, za))))
	}
	return u8(t, m, base+uint32(bit.Off))&bit.Mask != 0
}

func zoneOf(t *testing.T, m mem.Mem, room int) int {
	t.Helper()
	a, _ := addr.ZoneNoAddr(room)
	return int(int8(u8(t, m, a)))
}

func zoneRoom(t *testing.T, m mem.Mem, z int) int {
	t.Helper()
	return int(int8(u8(t, m, zoneAddr(z)+addr.ZoneOffRoomNo)))
}

func TestBootState(t *testing.T) {
	g, m := newGame(t)
	if v := u8(t, m, addr.DanStageNo); v != addr.DanStageNoInvalid {
		t.Errorf("mDan.mStageNo = %#x, want 0xFF", v)
	}
	if v := u16(t, m, addr.MaxLife); v != 12 {
		t.Errorf("mMaxLife = %d, want 12", v)
	}
	if v := u16(t, m, addr.Life); v != 12 {
		t.Errorf("mLife = %d, want 12", v)
	}
	if b := read(t, m, addr.Items, addr.ItemsSize); !bytes.Equal(b, bytes.Repeat([]byte{0xFF}, addr.ItemsSize)) {
		t.Errorf("mItems = %x, want all empty", b)
	}
	for z := range addr.ZoneCount {
		if r := zoneRoom(t, m, z); r != -1 {
			t.Errorf("zone %d belongs to room %d at boot", z, r)
		}
	}
	for room := range addr.RoomCount {
		if z := zoneOf(t, m, room); z != -1 {
			t.Errorf("room %d has zone %d at boot", room, z)
		}
	}
	if v := int8(u8(t, m, addr.StayNo)); v != -1 {
		t.Errorf("mStayNo = %d, want -1", v)
	}
	if v := u32(t, m, addr.PlayerPtr); v != 0 {
		t.Errorf("mpPlayerPtr[0] = %#x with no stage", v)
	}
	if v := u32(t, m, mailbox.SpawnTrigger); v != 0 || g.FrameCount() != 0 {
		t.Errorf("heartbeat %d, frames %d at boot", v, g.FrameCount())
	}
}

// TestHeartbeat: spawn_trigger moves once per frame, on stage or off it.
func TestHeartbeat(t *testing.T) {
	g, m := newGame(t)
	g.Frame()
	if v := u32(t, m, mailbox.SpawnTrigger); v != 1 {
		t.Fatalf("after 1 frame heartbeat = %d", v)
	}
	g.Frames(9)
	load(t, g, outset)
	g.Frames(5)
	if v := u32(t, m, mailbox.SpawnTrigger); v != 15 || g.FrameCount() != 15 {
		t.Fatalf("after 15 frames heartbeat = %d, frames = %d", v, g.FrameCount())
	}
	// It is a plain u32 counter: it wraps.
	_ = m.Store(mailbox.SpawnTrigger, []byte{0xFF, 0xFF, 0xFF, 0xFF})
	g.Frame()
	if v := u32(t, m, mailbox.SpawnTrigger); v != 0 {
		t.Fatalf("heartbeat after 0xFFFFFFFF = %#x", v)
	}
}

// TestGetSavePutSave follows one table's memBit through stage loads:
// getSave fills the live copy, game writes land only there, putSave
// carries them back, and a tool write to the current table's saved slot
// alone is lost at the next putSave (why §3.1 writes both copies).
func TestGetSavePutSave(t *testing.T) {
	g, m := newGame(t)
	savedDRC, _ := addr.SavedMemBitAddr(addr.TableDRC)
	savedSea, _ := addr.SavedMemBitAddr(addr.TableSea)

	// A chest the file already had, as card_to_memory would leave it.
	_ = m.Store(savedDRC+addr.MemBitOffTbox+3, []byte{0x01})

	load(t, g, drc)
	if got, want := read(t, m, addr.LiveMemBit, addr.MemBitSize), read(t, m, savedDRC, addr.MemBitSize); !bytes.Equal(got, want) {
		t.Fatalf("live after getSave = %x, want saved %x", got, want)
	}
	if v := int8(u8(t, m, addr.DanStageNo)); v != addr.TableDRC {
		t.Fatalf("mDan.mStageNo = %d, want %d", v, addr.TableDRC)
	}

	onSwitch(t, g, 0x05, 0)
	if !switchSet(t, m, 0x05, 0) {
		t.Fatal("switch 5 not set in the live memBit")
	}
	if b := read(t, m, savedDRC+addr.MemBitOffSwitch, 16); !bytes.Equal(b, make([]byte, 16)) {
		t.Fatalf("game switch write reached the saved slot before putSave: %x", b)
	}
	// A tool write to the current table's saved slot only.
	_ = m.Write(savedDRC+addr.MemBitOffItem+3, []byte{0x02})

	load(t, g, outset)
	_, bit, _ := addr.SwitchBit(0x05)
	if v := u8(t, m, savedDRC+uint32(bit.Off)); v&bit.Mask == 0 {
		t.Fatal("putSave did not carry switch 5 into the saved slot")
	}
	if v := u8(t, m, savedDRC+addr.MemBitOffItem+3); v != 0 {
		t.Fatalf("saved-only write survived putSave: %#x", v)
	}
	if got, want := read(t, m, addr.LiveMemBit, addr.MemBitSize), read(t, m, savedSea, addr.MemBitSize); !bytes.Equal(got, want) {
		t.Fatalf("live after getSave(sea) = %x, want %x", got, want)
	}

	// A cold-table write (the saved slot only) is picked up at the next
	// load of that table.
	_ = m.Write(savedDRC+addr.MemBitOffVisited+7, []byte{0x04})
	load(t, g, drc)
	live := read(t, m, addr.LiveMemBit, addr.MemBitSize)
	if live[addr.MemBitOffTbox+3] != 0x01 || live[bit.Off]&bit.Mask == 0 || live[addr.MemBitOffVisited+7] != 0x04 {
		t.Fatalf("round trip lost bits: live = %x", live)
	}
}

// TestInitDan: dan switches survive room changes and stages sharing the
// table, and reset when the table changes.
func TestInitDan(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc)
	onSwitch(t, g, 0x80, 0)
	onSwitch(t, g, 0xBF, 0)
	_ = m.Store(addr.Dan+addr.DanOffGbaRupee, []byte{7})
	want := []byte{0, 0, 0, 0x01, 0x80, 0, 0, 0}
	if b := read(t, m, addr.DanSwitch, 8); !bytes.Equal(b, want) {
		t.Fatalf("dan switches = %x, want %x", b, want)
	}

	enter(t, g, 1)
	load(t, g, drcBoss)
	if b := read(t, m, addr.DanSwitch, 8); !bytes.Equal(b, want) {
		t.Fatalf("dan switches after a same-table stage = %x, want %x", b, want)
	}
	if v := u8(t, m, addr.Dan+addr.DanOffGbaRupee); v != 7 {
		t.Fatalf("GBA rupees reset on a same-table stage: %d", v)
	}

	load(t, g, outset)
	if b := read(t, m, addr.DanSwitch, 8); !bytes.Equal(b, make([]byte, 8)) {
		t.Fatalf("dan switches after a table change = %x", b)
	}
	if v := u8(t, m, addr.DanStageNo); v != addr.TableSea {
		t.Fatalf("mDan.mStageNo = %d", v)
	}
	if v := u8(t, m, addr.Dan+addr.DanOffGbaRupee); v != 0 {
		t.Fatalf("GBA rupees = %d after a table change", v)
	}
}

// TestClearRoomSwitch: a room change zeroes zone switches 0xE0-0xEF and
// nothing else: not 0xC0-0xDF, the zone items or dead actors, nor the
// memory or dan switches.
func TestClearRoomSwitch(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc)
	for _, no := range []int{0x00, 0x7F, 0x80, 0xBF, 0xC0, 0xDF, 0xE0, 0xEF} {
		onSwitch(t, g, no, 0)
	}
	if err := g.OnActor(5, 0); err != nil {
		t.Fatal(err)
	}
	z := zoneOf(t, m, 0)
	za := zoneAddr(z)
	_ = m.Store(za+addr.ZoneOffItem, []byte{0x80, 0x01})
	before := read(t, m, za, addr.ZoneSize)
	if got := u16(t, m, za+addr.ZoneOffSwitch+4); got != 0x8001 {
		t.Fatalf("room switches before = %#04x", got)
	}

	enter(t, g, 1)

	after := read(t, m, za, addr.ZoneSize)
	want := append([]byte(nil), before...)
	want[addr.ZoneOffSwitch+4], want[addr.ZoneOffSwitch+5] = 0, 0
	if !bytes.Equal(after, want) {
		t.Fatalf("zone after the room change\n got %x\nwant %x", after, want)
	}
	for _, no := range []int{0x00, 0x7F, 0x80, 0xBF} {
		if !switchSet(t, m, no, 0) {
			t.Errorf("switch 0x%X cleared by the room change", no)
		}
	}
	// Every live zone loses its room switches, not only the room left.
	onSwitch(t, g, 0xE5, 1)
	enter(t, g, 0)
	if switchSet(t, m, 0xE5, 1) {
		t.Error("room 1's room switch survived leaving it")
	}
}

// TestZoneLifetime traces zone slots through room changes: first-free
// allocation, removal two room changes after a room was last loaded
// (except when stepping back), stale bits in a freed slot, and the slot's
// reuse wiping them.
func TestZoneLifetime(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc) // room 0 -> zone 0
	enter(t, g, 0, 1, 2)
	if a, b, c := zoneOf(t, m, 0), zoneOf(t, m, 1), zoneOf(t, m, 2); a != 0 || b != 1 || c != 2 {
		t.Fatalf("zones = %d %d %d, want 0 1 2", a, b, c)
	}
	onSwitch(t, g, 0xC1, 0)
	onSwitch(t, g, 0xC2, 2)

	enter(t, g, 1) // every zone counts down once; room 1 is reloaded
	enter(t, g, 2) // rooms 0 and 2 reach zero; room 2 is recreated first-free

	if z := zoneOf(t, m, 0); z != -1 {
		t.Errorf("room 0 still has zone %d", z)
	}
	if r := zoneRoom(t, m, 2); r != -1 {
		t.Errorf("zone 2 still belongs to room %d", r)
	}
	if !bitAt(t, m, zoneAddr(2), 0xC2) {
		t.Error("removeZone cleared the freed slot's bits; the game leaves them")
	}
	if z, r := zoneOf(t, m, 2), zoneRoom(t, m, 0); z != 0 || r != 2 {
		t.Errorf("room 2 has zone %d (zone 0 is room %d), want the first free slot 0", z, r)
	}
	if bitAt(t, m, zoneAddr(0), 0xC1) {
		t.Error("createZone kept the previous room's bits")
	}

	enter(t, g, 3) // room 1 reaches zero; room 3 takes the lowest free slot
	if z := zoneOf(t, m, 3); z != 1 {
		t.Errorf("room 3 has zone %d, want 1", z)
	}

	// Stepping back to the room just left counts nothing down: room 3
	// keeps its full count, where any other move would take it to 1.
	countOf := func(room int) int8 {
		a, _ := addr.RoomStatusAddr(room)
		return int8(u8(t, m, a+addr.RoomStatusOffZoneCount))
	}
	enter(t, g, 2)
	if c2, c3 := countOf(2), countOf(3); c2 != 2 || c3 != 2 || zoneOf(t, m, 3) != 1 {
		t.Errorf("after stepping back: room 2 count %d, room 3 count %d zone %d; want 2, 2, 1",
			c2, c3, zoneOf(t, m, 3))
	}

	// A stage entry at a start point wipes every zone.
	load(t, g, drcBoss)
	for z := 1; z < addr.ZoneCount; z++ {
		if r := zoneRoom(t, m, z); r != -1 {
			t.Errorf("zone %d belongs to room %d after a stage load", z, r)
		}
	}
	if r := zoneRoom(t, m, 0); r != 0 {
		t.Errorf("the start room has zone room %d", r)
	}
}

// bitAt reports whether zone switch no is set in the zone at za.
func bitAt(t *testing.T, m mem.Mem, za uint32, no int) bool {
	t.Helper()
	_, bit, _ := addr.SwitchBit(no)
	return u8(t, m, za+uint32(bit.Off))&bit.Mask != 0
}

// TestZonesRunOut: with all 32 zones taken a room gets none, and a zone
// switch there is the game's assert.
func TestZonesRunOut(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc)
	rooms := make([]int, 0, addr.ZoneCount)
	for r := 1; r <= addr.ZoneCount; r++ {
		rooms = append(rooms, r)
	}
	enter(t, g, 0, rooms...)
	if z := zoneOf(t, m, addr.ZoneCount); z != -1 {
		t.Fatalf("room %d got zone %d of %d", addr.ZoneCount, z, addr.ZoneCount)
	}
	if err := g.OnSwitch(0xC0, addr.ZoneCount); !errors.Is(err, ErrAssert) {
		t.Fatalf("zone switch in a zoneless room: %v, want ErrAssert", err)
	}
}

func TestHUDCounters(t *testing.T) {
	g, m := newGame(t)
	m.Record(true)
	load(t, g, drc) // key HUD on
	w16 := func(a uint32, v int16) {
		if err := mem.WriteU16(m, a, uint16(v)); err != nil {
			t.Fatal(err)
		}
	}

	// A heart container: +4 raises max life and refills.
	_ = m.Store(addr.Life, []byte{0, 6})
	w16(addr.ItemMaxLifeCount, 4)
	g.Frame()
	if mx, l, c := u16(t, m, addr.MaxLife), u16(t, m, addr.Life), u16(t, m, addr.ItemMaxLifeCount); mx != 16 || l != 16 || c != 0 {
		t.Fatalf("after +4: max %d life %d pending %d, want 16 16 0", mx, l, c)
	}
	// Capped at 20 hearts.
	_ = m.Store(addr.MaxLife, []byte{0, 0x4E})
	w16(addr.ItemMaxLifeCount, 4)
	g.Frame()
	if mx := u16(t, m, addr.MaxLife); mx != 0x50 {
		t.Fatalf("max life over the cap = %#x", mx)
	}
	// A negative count lowers it and clips life to the new maximum.
	_ = m.Store(addr.MaxLife, []byte{0, 16})
	_ = m.Store(addr.Life, []byte{0, 16})
	w16(addr.ItemMaxLifeCount, -4)
	g.Frame()
	if mx, l := u16(t, m, addr.MaxLife), u16(t, m, addr.Life); mx != 12 || l != 12 {
		t.Fatalf("after -4: max %d life %d, want 12 12", mx, l)
	}

	// Keys, clamped to 0..99.
	for _, c := range []struct {
		start uint8
		add   int16
		want  uint8
	}{{0, 2, 2}, {2, -5, 0}, {50, 200, 99}} {
		_ = m.Store(addr.LiveKeyNum, []byte{c.start})
		w16(addr.ItemKeyNumCount, c.add)
		g.Frame()
		if k, p := u8(t, m, addr.LiveKeyNum), u16(t, m, addr.ItemKeyNumCount); k != c.want || p != 0 {
			t.Errorf("keys %d%+d = %d (pending %d), want %d", c.start, c.add, k, p, c.want)
		}
	}

	// Max magic, capped at 0x20.
	w16(addr.ItemMaxMagicCount, 0x10)
	g.Frame()
	w16(addr.ItemMaxMagicCount, 0x20)
	g.Frame()
	if v, p := u8(t, m, addr.MaxMagic), u16(t, m, addr.ItemMaxMagicCount); v != 0x20 || p != 0 {
		t.Fatalf("max magic %#x pending %d, want 0x20 0", v, p)
	}

	// Without the key HUD the key count stays pending.
	load(t, g, outset)
	keys := u8(t, m, addr.LiveKeyNum)
	w16(addr.ItemKeyNumCount, 1)
	g.Frames(10)
	if k, p := u8(t, m, addr.LiveKeyNum), u16(t, m, addr.ItemKeyNumCount); k != keys || p != 1 {
		t.Fatalf("no key HUD: keys %d pending %d, want %d 1", k, p, keys)
	}

	// Off the stage nothing is consumed.
	g.EnterFileSelect()
	w16(addr.ItemMaxLifeCount, 4)
	g.Frames(10)
	if p := u16(t, m, addr.ItemMaxLifeCount); p != 4 {
		t.Fatalf("file select consumed the max-life count: %d", p)
	}

	// Only the test's own Writes were recorded: every game write is a
	// Store.
	for _, w := range m.Writes() {
		switch w.Addr {
		case addr.ItemMaxLifeCount, addr.ItemKeyNumCount, addr.ItemMaxMagicCount:
		default:
			t.Errorf("recorded a write at 0x%08X the test did not make", w.Addr)
		}
	}
}

func TestSwitchRouting(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc)
	for _, no := range []int{0x00, 0x2A, 0x7F, 0x80, 0xBF, 0xC0, 0xDF, 0xE0, 0xEF} {
		onSwitch(t, g, no, 0)
		if !switchSet(t, m, no, 0) {
			t.Errorf("OnSwitch(0x%X) not set", no)
		}
		if err := g.OffSwitch(no, 0); err != nil {
			t.Fatal(err)
		}
		if switchSet(t, m, no, 0) {
			t.Errorf("OffSwitch(0x%X) left it set", no)
		}
	}

	// "No switch" is ignored; everything else the game asserts on is
	// refused and changes nothing.
	snapshot := read(t, m, addr.SaveInfo, addr.SaveInfoSize)
	for _, no := range []int{-1, 0xFF} {
		if err := g.OnSwitch(no, 0); err != nil {
			t.Errorf("OnSwitch(%d) = %v, want nil", no, err)
		}
	}
	for _, c := range []struct{ no, room int }{
		{0xF0, 0}, {0xFE, 0}, {0x100, 0}, {-2, 0},
		{0xC0, -1}, {0xC0, 64}, // room out of range
		{0xE0, 5}, // room 5 has no zone
	} {
		if err := g.OnSwitch(c.no, c.room); !errors.Is(err, ErrAssert) {
			t.Errorf("OnSwitch(0x%X, %d) = %v, want ErrAssert", c.no, c.room, err)
		}
	}
	for _, c := range []struct{ id, room int }{{512, 0}, {-2, 0}, {0, 64}, {0, 5}} {
		if err := g.OnActor(c.id, c.room); !errors.Is(err, ErrAssert) {
			t.Errorf("OnActor(%d, %d) = %v, want ErrAssert", c.id, c.room, err)
		}
	}
	for _, c := range []struct{ id, room int }{{-1, 0}, {0xFFFF, 0}, {3, -1}} {
		if err := g.OnActor(c.id, c.room); err != nil {
			t.Errorf("OnActor(%d, %d) = %v, want a no-op", c.id, c.room, err)
		}
	}
	if b := read(t, m, addr.SaveInfo, addr.SaveInfoSize); !bytes.Equal(b, snapshot) {
		t.Error("a refused or ignored call changed the save")
	}

	// Event bits are a byte OR into the saved mEvent.
	g.OnEventBit(0x2A08)
	g.OnEventBit(0x2A01)
	if v := u8(t, m, addr.Event+0x2A); v != 0x09 {
		t.Fatalf("mEvent[0x2A] = %#x, want 0x09", v)
	}
	g.OffEventBit(0x2A08)
	if v := u8(t, m, addr.Event+0x2A); v != 0x01 {
		t.Fatalf("mEvent[0x2A] after off = %#x", v)
	}

	// Dead actor bits: setID n is word n/32, bit n%32.
	if err := g.OnActor(33, 0); err != nil {
		t.Fatal(err)
	}
	za := zoneAddr(zoneOf(t, m, 0))
	if v := u32(t, m, za+addr.ZoneOffActor+4); v != 1<<1 {
		t.Fatalf("actor word 1 = %#x, want 0x2", v)
	}
}

// TestSetNextStage: the first pending change wins, and Link stays until
// the change runs.
func TestSetNextStage(t *testing.T) {
	g, m := newGame(t)
	load(t, g, outset)
	if err := g.SetNextStage(drc); err != nil {
		t.Fatal(err)
	}
	if err := g.SetNextStage(drcBoss); err != nil {
		t.Fatal(err)
	}
	if v := u8(t, m, addr.NextStageEnable); v != 1 {
		t.Fatalf("mNextStage.mEnable = %d", v)
	}
	if u32(t, m, addr.PlayerPtr) != PlayerActor {
		t.Fatal("Link left before the stage change ran")
	}
	if name, _ := mem.ReadString(m, addr.NextStage, addr.StageNameLen); name != drc.Name {
		t.Fatalf("mNextStage = %q, want the first request %q", name, drc.Name)
	}
	if err := g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	if name, _ := mem.ReadString(m, addr.CurStageName, addr.StageNameLen); name != drc.Name {
		t.Fatalf("mCurStage = %q", name)
	}
	if v := u8(t, m, addr.NextStageEnable); v != 0 {
		t.Fatalf("mEnable = %d after the change", v)
	}
	if err := g.ChangeStage(); err == nil {
		t.Fatal("ChangeStage with nothing pending succeeded")
	}

	// A tool's raw mNextStage write (the pre-M7 warp, §6.7) is carried out
	// like the game's own, for a stage whose STAG the fake knows.
	warp := []byte("sea\x00\x00\x00\x00\x00\x00\x05\x2D\x00\x01")
	if err := m.Write(addr.NextStage, warp); err != nil {
		t.Fatal(err)
	}
	if err := g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	if room, point := int8(u8(t, m, addr.CurStageRoomNo)), u16(t, m, addr.CurStagePoint); room != 45 || point != 5 {
		t.Fatalf("warped to room %d point %d, want 45 5", room, point)
	}
	if v := u8(t, m, addr.DanStageNo); v != addr.TableSea || int8(u8(t, m, addr.StayNo)) != 45 {
		t.Fatalf("warp loaded table %d room %d", v, int8(u8(t, m, addr.StayNo)))
	}
	unknown := append([]byte("M_Nowhr\x00\x00\x00\x00\x00"), 1)
	_ = m.Write(addr.NextStage, unknown)
	if err := g.ChangeStage(); err == nil {
		t.Fatal("ChangeStage to a stage with no known STAG succeeded")
	}
	_ = m.Write(addr.NextStageEnable, []byte{0})
	if err := g.LoadStage(Stage{Name: "toolongname", Table: 0}); err == nil {
		t.Fatal("an 11-byte stage name was accepted")
	}
	if err := g.LoadStage(Stage{Name: "x", Table: 16}); !errors.Is(err, ErrAssert) {
		t.Fatalf("table 16: %v, want ErrAssert", err)
	}
	g.EnterFileSelect()
	if u32(t, m, addr.PlayerPtr) != 0 {
		t.Fatal("Link still loaded on file select")
	}
	if err := g.EnterRoom(1); err == nil {
		t.Fatal("EnterRoom with no stage succeeded")
	}
}
