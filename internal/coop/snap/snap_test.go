package snap

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
)

var (
	outset = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	drc    = fakegame.Stage{Name: "M_NewD2", Point: 3, Layer: 2, Table: addr.TableDRC, KeyHUD: true}
)

func mustRead(t *testing.T, m mem.Mem) *Snapshot {
	t.Helper()
	s, err := Read(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestRegions pins the region bounds to §4.2 and checks that every field
// the snapshot decodes lies inside the region it is decoded from.
func TestRegions(t *testing.T) {
	if SaveRegion.Size() != 0x12A0 || StageRegion.Size() != 0x17C || PlayerRegion.Size() != 0x190 {
		t.Fatalf("region sizes 0x%X 0x%X 0x%X", SaveRegion.Size(), StageRegion.Size(), PlayerRegion.Size())
	}
	if StageRegion.Start != addr.Play+0x3E94 || StageRegion.End != addr.Play+0x4010 ||
		PlayerRegion.Start != addr.Play+0x48AC || PlayerRegion.End != addr.Play+0x4A3C {
		t.Fatal("play blocks moved off their play offsets")
	}
	fields := []struct {
		r    addr.Region
		name string
		a    uint32
		n    int
	}{
		{StageRegion, "mCurStage", addr.CurStage, addr.StageSize},
		{StageRegion, "mNextStage.mEnable", addr.NextStageEnable, 1},
		{StageRegion, "mpStagInfo", addr.StagInfoPtr, 4},
		{StageRegion, "mOrderCount", addr.EvtOrderCount, 1},
		{StageRegion, "mMode", addr.EvtMode, 1},
		{PlayerRegion, "mpPlayerPtr[0]", addr.PlayerPtr, 4},
		{PlayerRegion, "mItemKeyNumCount", addr.ItemKeyNumCount, 2},
		{PlayerRegion, "mItemMaxLifeCount", addr.ItemMaxLifeCount, 2},
		{PlayerRegion, "mItemMaxMagicCount", addr.ItemMaxMagicCount, 2},
		{PlayerRegion, "play mSelectItem", addr.PlaySelectItem, 4},
		{PlayerRegion, "mPlayerInfoBufferStageNo", addr.PlayerInfoBufStageNo, 1},
		{PlayerRegion, "mMiniGameType", addr.MiniGameType, 1},
		{SaveRegion, "mDan.mStageNo", addr.DanStageNo, 1},
		{SaveRegion, "mDataNum", addr.DataNum, 1},
		{SaveRegion, "mZone[31]", addr.Zones + 31*addr.ZoneSize, addr.ZoneSize},
	}
	for _, f := range fields {
		if !f.r.Contains(f.a, f.n) {
			t.Errorf("%s (0x%08X+%d) is outside %s", f.name, f.a, f.n, f.r.Name)
		}
	}
}

// countingMem counts reads and bytes, and can fail reads at one address
// or tear the second read of the save.
type countingMem struct {
	mem.Mem
	mu        sync.Mutex
	reads     int
	bytes     int
	saveReads int
	failAt    uint32
	tear      func(b []byte) // applied to the second save read
}

var errInjected = errors.New("injected read failure")

func (c *countingMem) Read(a uint32, n int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failAt != 0 && a <= c.failAt && c.failAt < a+uint32(n) {
		return nil, errInjected
	}
	c.reads++
	c.bytes += n
	b, err := c.Mem.Read(a, n)
	if err == nil && a == SaveRegion.Start && n == saveSize {
		c.saveReads++
		if c.saveReads == 2 && c.tear != nil {
			c.tear(b)
		}
	}
	return b, err
}

// TestReadDecodes runs the fake game into known states and checks every
// decoded field against them.
func TestReadDecodes(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	cm := &countingMem{Mem: m}

	s := mustRead(t, cm)
	if s.StageName != "" || s.PlayerPtr != 0 || s.Table != -1 || s.StayNo != -1 || s.Heartbeat != 0 {
		t.Errorf("boot snapshot: %+v", *s)
	}
	// One read per region, per room status and per byte field, plus the
	// second save read: about 5 KiB, and the save twice.
	if want := 3 + 1 + addr.RoomCount + 2 + 1; cm.reads != want {
		t.Errorf("%d reads per snapshot, want %d", cm.reads, want)
	}
	if want := 2*0x12A0 + 0x17C + 0x190 + 1 + 2*addr.RoomCount + 1 + 4; cm.bytes != want {
		t.Errorf("%d bytes per snapshot, want %d", cm.bytes, want)
	}

	if err := g.NewGame(2, "Link"); err != nil {
		t.Fatal(err)
	}
	if err := g.LoadStage(drc); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	g.Frames(7)
	if err := g.SetNextStage(outset); err != nil {
		t.Fatal(err)
	}
	g.SetEvent(1, 2)
	g.SetPauseMenu(true)
	g.SetMiniGame(3)
	g.SetRefight(4)
	_ = mem.WriteU16(m, addr.ItemKeyNumCount, 0xFFFF) // -1: no key HUD consumption is wanted here
	_ = mem.WriteU16(m, addr.ItemMaxLifeCount, 4)
	_ = mem.WriteU16(m, addr.ItemMaxMagicCount, 0x10)

	s = mustRead(t, m)
	checks := []struct {
		name      string
		got, want any
	}{
		{"StageName", s.StageName, drc.Name},
		{"StagePoint", s.StagePoint, drc.Point},
		{"StageRoom", s.StageRoom, drc.Room},
		{"StageLayer", s.StageLayer, drc.Layer},
		{"NextEnable", s.NextEnable, int8(1)},
		{"StagInfo", s.StagInfo, fakegame.StagChunk},
		{"EvtMode", s.EvtMode, uint8(1)},
		{"EvtOrderCount", s.EvtOrderCount, int8(2)},
		{"PlayerPtr", s.PlayerPtr, fakegame.PlayerActor},
		{"KeyNumCount", s.KeyNumCount, int16(-1)},
		{"MaxLifeCount", s.MaxLifeCount, int16(4)},
		{"MaxMagicCount", s.MaxMagicCount, int16(0x10)},
		{"RefightStageNo", s.RefightStageNo, uint8(4)},
		{"MiniGameType", s.MiniGameType, uint8(3)},
		{"Table", s.Table, int8(addr.TableDRC)},
		{"DataNum", s.DataNum, uint8(2)},
		{"StayNo", s.StayNo, int8(1)},
		{"ZoneNo[0..2]", [3]int8{s.ZoneNo[0], s.ZoneNo[1], s.ZoneNo[2]}, [3]int8{0, 1, 2}},
		{"ZoneCount[0..2]", [3]int8{s.ZoneCount[0], s.ZoneCount[1], s.ZoneCount[2]}, [3]int8{1, 2, 2}},
		{"ZoneNo[3]", s.ZoneNo[3], int8(-1)},
		{"MenuPause", s.MenuPause, uint8(1)},
		{"Heartbeat", s.Heartbeat, uint32(7)},
		{"Torn", s.Torn, 0},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if raw, _ := m.Read(SaveRegion.Start, saveSize); !bytes.Equal(raw, s.Save[:]) {
		t.Error("an untorn snapshot's save differs from memory")
	}
}

// TestTornSaveRead tears the second save read and checks each byte kind
// folds as Read documents.
func TestTornSaveRead(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(drc); err != nil {
		t.Fatal(err)
	}
	tboxDRC := addr.SavedMemBit + uint32(addr.OffMemBit(addr.TableDRC)-addr.OffMemory) + addr.MemBitOffTbox + 3
	dungeonDRC := tboxDRC - 3 + addr.MemBitOffDungeonItem
	liveSwitch := addr.LiveMemBit + addr.MemBitOffSwitch + 3
	letter := addr.Event + uint32(addr.EventByte(0x7A03).Off) // LETTER_ROCK_SPIRE_SHOP_AD: MAX within mask 0x03
	zoneActor := addr.Zones + 5*addr.ZoneSize + addr.ZoneOffActor
	zoneRoom := addr.Zones + 5*addr.ZoneSize + addr.ZoneOffRoomNo

	type tc struct {
		name          string
		a             uint32
		first, second byte
		want          byte
		torn          bool
	}
	cases := []tc{
		{"saved tbox (OR)", tboxDRC, 0x0F, 0x3C, 0x0C, true},
		{"dungeon items (OR, mask 0x3F)", dungeonDRC, 0xC1, 0x02, 0xC0, true},
		{"live switch (OR_MASKED)", liveSwitch, 0x01, 0x00, 0x00, true},
		{"dan switch", addr.DanSwitch + 3, 0x80, 0x80, 0x80, false},
		{"zone dead actors", zoneActor, 0xFF, 0x0F, 0x0F, true},
		{"wallet (MAX)", addr.WalletSize, 2, 1, 1, true},
		{"letter register (MAX in 0x03)", letter, 0x83, 0x01, 0x81, true},
		{"letter register, local bit only", letter + 0x01, 0x80, 0x00, 0x80, false}, // 0x7B03, LETTER_ORCA
		{"life (per player)", addr.Life + 1, 12, 4, 12, false},
		{"zone room (a number)", zoneRoom, 0x07, 0xFF, 0x07, false},
	}
	if class.Of(int(letter+1-addr.SaveImage)).Mask != 0x03 {
		t.Fatal("test assumes register 0x7B is MAX within 0x03")
	}
	for _, c := range cases {
		_ = m.Store(c.a, []byte{c.first})
	}
	cm := &countingMem{Mem: m, tear: func(b []byte) {
		for _, c := range cases {
			b[c.a-SaveRegion.Start] = c.second
		}
	}}
	s := mustRead(t, cm)
	torn := 0
	for _, c := range cases {
		if got := s.Save[c.a-SaveRegion.Start]; got != c.want {
			t.Errorf("%s: first %#02x second %#02x folded to %#02x, want %#02x", c.name, c.first, c.second, got, c.want)
		}
		if c.torn {
			torn++
		}
	}
	if s.Torn != torn {
		t.Errorf("Torn = %d, want %d", s.Torn, torn)
	}
}

// TestFoldsFollowClassTable: the fold of every image byte comes from its
// class, the live memBit folds like a saved one, and the zone room bytes
// and the mDan table are numbers.
func TestFoldsFollowClassTable(t *testing.T) {
	for off := range addr.SaveImageSize {
		r, f := class.Of(off), folds[off]
		want := foldFirst
		switch r.Class {
		case class.OR, class.ORMasked:
			want = foldBits
		case class.Max:
			want = foldMax
		}
		if f.fold != want || (want != foldFirst && f.mask != r.Mask) {
			t.Errorf("image 0x%03X (%s %s): fold %v mask %#x", off, r.Field, r.Class, f.fold, f.mask)
		}
	}
	for i := range addr.MemBitSize {
		if folds[addr.InfoOffMemory+i] != folds[addr.OffMemBit(addr.TableET)+i] {
			t.Errorf("live memBit +0x%02X folds unlike a saved slot", i)
		}
	}
	for _, off := range []int{
		addr.InfoOffDan + addr.DanOffStageNo,
		addr.InfoOffDan + addr.DanOffGbaRupee,
		addr.InfoOffZone + addr.ZoneOffRoomNo,
		addr.InfoOffZone + 31*addr.ZoneSize + 1, // pad
		addr.InfoOffDataNum,
		addr.InfoOffTmp,
	} {
		if folds[off].fold != foldFirst {
			t.Errorf("dSv_info_c +0x%04X folds as %v, want first read", off, folds[off].fold)
		}
	}
	if folds[addr.InfoOffZone+31*addr.ZoneSize+addr.ZoneSize-1].fold != foldBits {
		t.Error("the last zone's last actor byte is not a flag")
	}
}

func TestReadErrors(t *testing.T) {
	m := mem.NewFakeMem()
	fakegame.New(m)
	for _, a := range []uint32{SaveRegion.Start, StageRegion.Start, PlayerRegion.Start, addr.StayNo,
		addr.RoomStatus + 5*addr.RoomStatusStride + addr.RoomStatusOffZoneNo, addr.MenuPause, addr.SpawnTrigger} {
		s, err := Read(&countingMem{Mem: m, failAt: a})
		if !errors.Is(err, errInjected) || s != nil {
			t.Errorf("failing read at 0x%08X: %v, %v", a, s, err)
		}
	}
	// The second save read failing fails the snapshot too.
	fail := &secondSaveFails{countingMem: &countingMem{Mem: m}}
	if _, err := Read(fail); !errors.Is(err, errInjected) {
		t.Errorf("second save read failure: %v", err)
	}
}

type secondSaveFails struct{ *countingMem }

func (f *secondSaveFails) Read(a uint32, n int) ([]byte, error) {
	if a == SaveRegion.Start && f.saveReads == 1 {
		return nil, errInjected
	}
	return f.countingMem.Read(a, n)
}

// TestImage: the live memBit replaces its table's saved slot, as putSave
// would leave it, and only while a table is loaded.
func TestImage(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	_ = m.Store(addr.Event, []byte{0x40})

	img := mustRead(t, m).Image()
	if raw, _ := m.Read(addr.SaveImage, addr.SaveImageSize); !bytes.Equal(img[:], raw) {
		t.Error("with no table loaded Image is not the saved image")
	}

	if err := g.LoadStage(drc); err != nil {
		t.Fatal(err)
	}
	if err := g.OnSwitch(0x10, 0); err != nil {
		t.Fatal(err)
	}
	_ = m.Store(addr.SavedMemBit+uint32(addr.OffMemBit(addr.TableDRC)-addr.OffMemory)+addr.MemBitOffKeyNum, []byte{9}) // stale saved copy
	s := mustRead(t, m)
	img = s.Image()
	live, _ := m.Read(addr.LiveMemBit, addr.MemBitSize)
	slot := addr.OffMemBit(addr.TableDRC)
	if !bytes.Equal(img[slot:slot+addr.MemBitSize], live) {
		t.Errorf("table %d in the image = %x, want the live %x", addr.TableDRC, img[slot:slot+addr.MemBitSize], live)
	}
	if img[addr.OffEvent] != 0x40 {
		t.Error("the rest of the image is not the saved image")
	}
	other := addr.OffMemBit(addr.TableSea)
	if !bytes.Equal(img[other:other+addr.MemBitSize], s.Save[other:other+addr.MemBitSize]) {
		t.Error("another table's slot changed")
	}
}

// TestZoneFor follows the fake game's zones: a live zone is found; a room
// whose zone was removed, a zone number naming another room's zone or no
// zone at all, and a room out of range are not.
func TestZoneFor(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(drc); err != nil {
		t.Fatal(err)
	}
	for _, r := range []int{1, 2, 3} {
		if err := g.EnterRoom(r); err != nil {
			t.Fatal(err)
		}
	}
	s := mustRead(t, m)
	if z, ok := s.ZoneFor(3); !ok || int8(z) != s.ZoneNo[3] {
		t.Errorf("ZoneFor(3) = %d, %v", z, ok)
	}
	if _, ok := s.ZoneFor(0); ok {
		t.Error("room 0's zone was removed two room changes ago, yet ZoneFor found it")
	}
	for _, r := range []int{-1, 9, addr.RoomCount} {
		if _, ok := s.ZoneFor(r); ok {
			t.Errorf("ZoneFor(%d) succeeded", r)
		}
	}
	// A zone number pointing at another room's zone, or past the 32.
	s.ZoneNo[9] = s.ZoneNo[3]
	if _, ok := s.ZoneFor(9); ok {
		t.Error("ZoneFor accepted a zone whose mRoomNo is another room")
	}
	s.ZoneNo[9] = 40
	if _, ok := s.ZoneFor(9); ok {
		t.Error("ZoneFor accepted zone 40")
	}
}

// TestReadWhileGameRuns reads snapshots while the game runs and sets
// flags. Under -race it checks the fake and the reader share memory
// safely; and since the game only sets flags here, a later snapshot never
// shows fewer of them.
func TestReadWhileGameRuns(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(drc); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 400 {
			g.Frame()
			_ = g.OnSwitch(i%addr.MemBitSwitchCount, 0)
			g.OnEventBit(uint16(0x0100 | 1<<(i%8)))
		}
	}()
	var prevHB uint32
	var prevOnes int
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		s := mustRead(t, m)
		if s.Heartbeat < prevHB {
			t.Fatalf("heartbeat went from %d to %d", prevHB, s.Heartbeat)
		}
		ones := 0
		for _, b := range s.Save[addr.InfoOffMemory+addr.MemBitOffSwitch : addr.InfoOffMemory+addr.MemBitOffItem] {
			for ; b != 0; b &= b - 1 {
				ones++
			}
		}
		if ones < prevOnes {
			t.Fatalf("live switches fell from %d to %d bits", prevOnes, ones)
		}
		prevHB, prevOnes = s.Heartbeat, ones
	}
}
