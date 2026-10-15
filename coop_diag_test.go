package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

var (
	diagSea = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	diagDRC = fakegame.Stage{Name: "M_NewD2", Point: 3, Room: 2, Layer: 2, Table: addr.TableDRC, KeyHUD: true}
)

func store(t *testing.T, m *mem.FakeMem, a uint32, b []byte) {
	t.Helper()
	if err := m.Store(a, b); err != nil {
		t.Fatal(err)
	}
}

func u16b(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// bitNo is the bit number of a one-bit mask.
func bitNo(mask byte) int { return bits.TrailingZeros8(mask) }

// TestSaveLabelsMatchTheGamesBitMath labels every flag the addr helpers
// can place (the game's own setter math) and checks the label names the
// same flag back: coop-watch's labels invert addr.Bit exactly.
func TestSaveLabelsMatchTheGamesBitMath(t *testing.T) {
	type want struct {
		off   int
		bit   int
		label string
	}
	var cases []want
	add := func(off int, b addr.Bit, label string) {
		cases = append(cases, want{off, bitNo(b.Mask), label})
	}
	for _, tbl := range []int{addr.TableSea, addr.TableDRC, addr.TableTest} {
		saved := addr.OffMemBit(tbl)
		for no := 0; no < addr.MemBitSwitchCount; no++ {
			_, b, err := addr.SwitchBit(no)
			if err != nil {
				t.Fatal(err)
			}
			add(saved+b.Off, b, fmt.Sprintf("saved t%02d switch 0x%02X", tbl, no))
			add(addr.InfoOffMemory+b.Off, b, fmt.Sprintf("live t%02d switch 0x%02X", addr.TableDRC, no))
		}
		for no := 0; no < addr.MemBitTboxCount; no++ {
			b, _ := addr.TboxBit(no)
			add(saved+b.Off, b, fmt.Sprintf("saved t%02d chest %d", tbl, no))
		}
		for no := 0; no < 32; no++ {
			b, _ := addr.MemBitItemBit(no)
			add(saved+b.Off, b, fmt.Sprintf("saved t%02d item %d", tbl, no))
		}
		for room := 0; room < addr.MemBitRoomCount; room++ {
			b, _ := addr.VisitedRoomBit(room)
			l := fmt.Sprintf("saved t%02d visited room %d", tbl, room)
			if room < 32 {
				// Items 32-63 land in the same bits (addr.MemBitItemBit).
				ib, _ := addr.MemBitItemBit(room + 32)
				if ib != b {
					t.Fatalf("item %d is not visited room %d's bit", room+32, room)
				}
				l += fmt.Sprintf(" or item %d", room+32)
			}
			add(saved+b.Off, b, l)
		}
		b, _ := addr.DungeonItemBit(addr.DungeonItemBossKey)
		add(saved+b.Off, b, fmt.Sprintf("saved t%02d boss key", tbl))
	}
	for no := addr.SwitchDanFirst; no < addr.SwitchZoneFirst; no++ {
		_, b, _ := addr.SwitchBit(no)
		add(addr.InfoOffDan+b.Off, b, fmt.Sprintf("dan switch 0x%02X", no))
	}
	for _, z := range []int{0, 7, addr.ZoneCount - 1} {
		base := addr.InfoOffZone + z*addr.ZoneSize
		for no := addr.SwitchZoneFirst; no < addr.SwitchEnd; no++ {
			_, b, _ := addr.SwitchBit(no)
			add(base+b.Off, b, fmt.Sprintf("zone %d switch 0x%02X", z, no))
		}
		for id := 0; id < addr.ZoneActorCount; id++ {
			b, _ := addr.ZoneActorBit(id)
			add(base+b.Off, b, fmt.Sprintf("zone %d actor %d", z, id))
		}
	}
	for grid := 0; grid < addr.OceanGridCount; grid++ {
		for bit := range 16 {
			b, _ := addr.OceanBit(grid, bit)
			add(addr.OffOcean+b.Off, b, fmt.Sprintf("ocean grid %d bit %d", grid, bit))
		}
	}
	for _, id := range []uint16{0x2D01, 0x0F80, 0x3D10, 0x0101} {
		b := addr.EventByte(id)
		add(addr.OffEvent+b.Off, b, fmt.Sprintf("event 0x%04X", id))
	}
	// Byte 0 has no named bit 0x40 (d_save_event_flag.inc), and bytes
	// 0x42-0x78 have none at all.
	add(addr.OffEvent+0x00, addr.Bit{Mask: 0x40}, "event 0x0040 (unnamed)")
	add(addr.OffEvent+0x60, addr.Bit{Mask: 0x01}, "event 0x6001 (unnamed)")
	add(addr.InfoOffTmp+0x12, addr.Bit{Mask: 0x08}, "tmp 0x1208")
	// mGetItem and mCollect[0..4] by their array index, though the class
	// table splits both: an empty bottle held (onBottleItem,
	// d_save.cpp:224-282), the boomerang, the Hero's Charm worn.
	add(addr.OffGetItem, addr.Bit{Mask: 0x04}, "mGetItem[0] bit 2")
	add(addr.OffGetItem+5, addr.Bit{Mask: 0x01}, "mGetItem[5] bit 0")
	add(addr.OffCollectFlags+4, addr.Bit{Mask: 0x02}, "mCollect[4] bit 1")

	for _, c := range cases {
		got, flags := saveLabel(c.off, c.bit, addr.TableDRC)
		if got != c.label || !flags {
			t.Errorf("saveLabel(0x%X, %d) = %q, %v; want %q, flags", c.off, c.bit, got, flags, c.label)
		}
	}

	// Values, not flags: their changes print as one byte.
	for _, c := range []struct {
		off   int
		label string
	}{
		{addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum, "saved t03 small keys"},
		{addr.InfoOffMemory + addr.MemBitOffKeyNum, "live t03 small keys"},
		{addr.OffMaxLife, "mMaxLife+0x0"},
		{addr.OffLife + 1, "mLife+0x1"},
		{addr.OffWalletSize, "mWalletSize"},
		{addr.OffEvent + 0x86, "event register byte 0x86"},
		{addr.OffReserve + 4, "mReserve+0x04"},
		{addr.InfoOffDan + addr.DanOffStageNo, "mDan.mStageNo (live table)"},
		{addr.InfoOffZone + 2*addr.ZoneSize, "zone 2 room"},
		{addr.InfoOffDataNum, "mDataNum"},
		{addr.InfoOffRestart + 3, "mRestart+0x03"},
	} {
		got, flags := saveLabel(c.off, -1, addr.TableDRC)
		if got != c.label || flags {
			t.Errorf("saveLabel(0x%X, -1) = %q, %v; want %q, a value", c.off, got, flags, c.label)
		}
	}

	// An invalid live table still labels, without a table. Byte 0 bit 0
	// of the big-endian first word is switch 0x18.
	if got, _ := saveLabel(addr.InfoOffMemory+addr.MemBitOffSwitch, 0, -1); got != "live t?? switch 0x18" {
		t.Errorf("live switch with no table: %q", got)
	}
}

// watchLines reads a snapshot and returns the watcher's lines for it.
func watchLines(t *testing.T, m mem.Mem, cw *coopWatcher) []string {
	t.Helper()
	s, err := snap.Read(m)
	if err != nil {
		t.Fatal(err)
	}
	return cw.step(s)
}

func wantLines(t *testing.T, step string, got []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s: no line %q in\n  %s", step, w, strings.Join(got, "\n  "))
		}
	}
}

// TestCoopWatchEdges drives the fake game's own setters and checks
// coop-watch labels each edge by store, table and room, notes whether the
// live memBit's saved slot has caught up, and folds a table change's bulk
// reload into one line.
func TestCoopWatchEdges(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(diagDRC); err != nil {
		t.Fatal(err)
	}
	cw := &coopWatcher{}
	if got := watchLines(t, m, cw); len(got) != 1 || !strings.Contains(got[0], `stage "M_NewD2" room 2, table 3 DRC`) {
		t.Fatalf("first step: %q", got)
	}
	if got := watchLines(t, m, cw); len(got) != 0 {
		t.Fatalf("nothing changed, yet: %q", got)
	}

	for _, err := range []error{
		g.OnSwitch(0x1A, 2), g.OnSwitch(0x85, 2), g.OnSwitch(0xC5, 2), g.OnSwitch(0xE3, 2),
		g.OnActor(7, 2), g.OnActor(300, 2),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	g.OnEventBit(0x2D01)
	g.OnEventBit(0x0040)
	got := watchLines(t, m, cw)
	wantLines(t, "game writes", got,
		"+ live t03 switch 0x1A (saved slot clear)",
		"+ dan switch 0x85",
		"+ zone 0 switch 0xC5 (room 2)",
		"+ zone 0 switch 0xE3 (room 2)",
		"+ zone 0 actor 7 (room 2)",
		"+ zone 0 actor 300 (room 2)",
		"+ event 0x2D01",
		"+ event 0x0040 (unnamed)",
	)
	if len(got) != 8 {
		t.Errorf("want exactly the 8 edges, got %q", got)
	}

	// The save menu's putSave brings the saved slot level with the live
	// copy; a later clear then shows the slot still set.
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	wantLines(t, "save", watchLines(t, m, cw), "+ saved t03 switch 0x1A")
	if err := g.OffSwitch(0x1A, 2); err != nil {
		t.Fatal(err)
	}
	wantLines(t, "clear", watchLines(t, m, cw), "- live t03 switch 0x1A (saved slot set)")

	// A value byte, and status_b, which only --all shows.
	store(t, m, addr.LiveKeyNum, []byte{2})
	store(t, m, addr.SaveInfo+addr.OffStatusB+4, []byte{0x40})
	got = watchLines(t, m, cw)
	wantLines(t, "values", got, "~ live t03 small keys: 0x00 -> 0x02")
	if len(got) != 1 {
		t.Errorf("status_b shown without --all: %q", got)
	}
	cwAll := &coopWatcher{all: true, prev: cw.prev}
	store(t, m, addr.SaveInfo+addr.OffStatusB+4, []byte{0x41})
	wantLines(t, "--all", watchLines(t, m, cwAll), "~ status_b+0x4: 0x40 -> 0x41")

	// Leaving for the sea: putSave, then getSave and initDan for table 0.
	if err := g.LoadStage(diagSea); err != nil {
		t.Fatal(err)
	}
	got = watchLines(t, m, cw)
	wantLines(t, "stage change", got,
		`== now stage "sea" room 44, table 0 sea, file 0`,
		"~ saved t03 small keys: 0x00 -> 0x02",
		"   (table 3 -> 0: getSave and initDan reloaded the live memBit and mDan; 3 edges not shown)",
	)
	for _, l := range got {
		if strings.Contains(l, "live t") || strings.Contains(l, "dan switch") {
			t.Errorf("bulk reload edge shown: %q", l)
		}
	}
}

// fakeActorType is what fpcBs_MakeOfType hands the first new type
// (f_pc_base.cpp:28-34).
const fakeActorType = 0x09130001

type fakeActor struct {
	at     uint32
	proc   int16
	setID  uint16
	group  uint8
	room   int8
	health int8
	pos    [3]float32
}

// layActors links actors into g_fopAcTg_Queue in order, the way
// fopAc_Create and cLs_Addition leave them, and returns their nodes.
func layActors(t *testing.T, m *mem.FakeMem, actors ...fakeActor) []uint32 {
	t.Helper()
	store(t, m, addr.FopAcType, u32b(fakeActorType))
	nodes := make([]uint32, len(actors))
	for i, a := range actors {
		nodes[i] = a.at + addr.ActorOffActorTag
	}
	for i, a := range actors {
		var prev, next uint32
		if i > 0 {
			prev = nodes[i-1]
		}
		if i+1 < len(nodes) {
			next = nodes[i+1]
		}
		n := nodes[i]
		store(t, m, n+addr.TagOffPrev, u32b(prev))
		store(t, m, n+addr.TagOffData, u32b(addr.ActorQueue))
		store(t, m, n+addr.TagOffNext, u32b(next))
		store(t, m, n+addr.TagOffTagData, u32b(a.at))
		store(t, m, a.at+addr.ActorOffActorType, u32b(fakeActorType))
		store(t, m, a.at+addr.ActorOffBsPcID, u32b(uint32(100+i)))
		store(t, m, a.at+addr.ActorOffProcName, u16b(uint16(a.proc)))
		store(t, m, a.at+addr.ActorOffSetID, u16b(a.setID))
		store(t, m, a.at+addr.ActorOffGroup, []byte{a.group})
		store(t, m, a.at+addr.ActorOffHomeRoomNo, []byte{byte(a.room)})
		store(t, m, a.at+addr.ActorOffHealth, []byte{byte(a.health)})
		store(t, m, a.at+actorOffMaxHealth, []byte{byte(a.health)})
		for j, f := range a.pos {
			store(t, m, a.at+actorOffCurPos+uint32(4*j), u32b(math.Float32bits(f)))
		}
	}
	var head, tail uint32
	if len(nodes) > 0 {
		head, tail = nodes[0], nodes[len(nodes)-1]
	}
	store(t, m, addr.ActorQueue+addr.ActorQueueOffHead, u32b(head))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffTail, u32b(tail))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffSize, u32b(uint32(len(nodes))))
	return nodes
}

var (
	diagLink = fakeActor{at: 0x80A00000, proc: 0xA9, setID: addr.ActorSetIDNone, group: 1, room: -1, pos: [3]float32{10, 20, 30}}
	diagBoko = fakeActor{at: 0x80A10000, proc: 0xBE, setID: 5, group: 2, room: 3, health: 4}
	diagPot  = fakeActor{at: 0x80A20000, proc: 0x1CB, setID: 9, group: 0, room: 3}
)

func TestWalkActors(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, diagLink, diagBoko, diagPot)
	// The Bokoblin's enemyice points back at it (d_a_bk.cpp:5270).
	store(t, m, diagBoko.at+k1Classes[0xBE].iceOff, u32b(diagBoko.at))
	store(t, m, diagBoko.at+k1Classes[0xBE].iceOff+6, []byte{0})

	w, err := walkActors(m, maxActorWalk)
	if err != nil {
		t.Fatal(err)
	}
	if w.Stop != "" || len(w.Notes) != 0 || len(w.Rows) != 3 || w.Size != 3 || w.Type != fakeActorType {
		t.Fatalf("walk: stop %q notes %q rows %d size %d type 0x%X", w.Stop, w.Notes, len(w.Rows), w.Size, w.Type)
	}
	link, boko := w.Rows[0], w.Rows[1]
	if link.Actor != diagLink.at || link.ProcName != 0xA9 || link.SetID != addr.ActorSetIDNone ||
		link.Group != 1 || link.HomeRoom != -1 || link.Pos != diagLink.pos || !link.DataOK || link.Ice != "" {
		t.Errorf("Link row: %+v", link)
	}
	if boko.ProcID != 101 || boko.SetID != 5 || boko.Health != 4 || boko.Ice != "enemyice ok, shrink timer 0" {
		t.Errorf("Bokoblin row: %+v", boko)
	}

	var out bytes.Buffer
	printActors(&out, w)
	for _, s := range []string{"proc 0x00A9 player", "proc 0x00BE bk", "set 5", "enemy", "hp 4/4", "3 actors\n"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("printActors lacks %q:\n%s", s, out.String())
		}
	}

	// The self-pointer catches a wrong enemyice offset.
	store(t, m, diagBoko.at+k1Classes[0xBE].iceOff, u32b(0))
	w, _ = walkActors(m, maxActorWalk)
	if got := w.Rows[1].Ice; got != "enemyice.mpActor 0x00000000 is not the actor" {
		t.Errorf("bad self-pointer: %q", got)
	}

	// An empty queue is a clean, empty walk.
	empty := mem.NewFakeMem()
	layActors(t, empty)
	if w, err := walkActors(empty, maxActorWalk); err != nil || w.Stop != "" || len(w.Rows) != 0 || len(w.Notes) != 0 {
		t.Errorf("empty queue: %+v, %v", w, err)
	}
}

// TestWalkActorsStops breaks the list in each way the walk guards against
// and checks it stops there, having followed no pointer it did not check.
func TestWalkActorsStops(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		mut   func(m *mem.FakeMem, nodes []uint32)
		rows  int
		stop  string
	}{
		{"cycle", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[2]+addr.TagOffNext, u32b(n[0])) }, 3, "has a cycle"},
		{"self loop", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[0]+addr.TagOffNext, u32b(n[0])) }, 1, "has a cycle"},
		{"next outside MEM1", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffNext, u32b(0x90000000)) }, 2, "is outside MEM1"},
		{"next at the end of MEM1", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffNext, u32b(addr.MEM1End-8)) }, 2, "is outside MEM1"},
		{"head below its actor", 0, func(m *mem.FakeMem, n []uint32) { m.Store(addr.ActorQueue, u32b(addr.MEM1Base+0x10)) }, 0, "no room in MEM1"},
		{"misaligned", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[0]+addr.TagOffNext, u32b(n[1]+2)) }, 1, "not word-aligned"},
		{"prev changed", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffPrev, u32b(0x80123450)) }, 1, "changed during the walk"},
		{"tag data", 0, func(m *mem.FakeMem, n []uint32) { m.Store(n[1]+addr.TagOffTagData, u32b(0x80123450)) }, 1, "not its actor"},
		{"actor type", 0, func(m *mem.FakeMem, n []uint32) { m.Store(diagBoko.at+addr.ActorOffActorType, u32b(0)) }, 1, "not g_fopAc_type"},
		{"limit", 2, func(*mem.FakeMem, []uint32) {}, 2, "limit of 2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := mem.NewFakeMem()
			c.mut(m, layActors(t, m, diagLink, diagBoko, diagPot))
			limit := c.limit
			if limit == 0 {
				limit = maxActorWalk
			}
			w, err := walkActors(m, limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(w.Rows) != c.rows || !strings.Contains(w.Stop, c.stop) {
				t.Errorf("rows %d stop %q; want %d rows, stop with %q", len(w.Rows), w.Stop, c.rows, c.stop)
			}
		})
	}

	// mSize and mpTail disagreeing with a clean walk are notes, not stops.
	m := mem.NewFakeMem()
	layActors(t, m, diagLink, diagBoko)
	store(t, m, addr.ActorQueue+addr.ActorQueueOffSize, u32b(5))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffTail, u32b(0x80123450))
	w, _ := walkActors(m, maxActorWalk)
	if w.Stop != "" || len(w.Rows) != 2 || len(w.Notes) != 2 {
		t.Errorf("notes: stop %q rows %d notes %q", w.Stop, len(w.Rows), w.Notes)
	}

	// mpData other than the queue (cLs_Addition sets it, c_list.cpp:40)
	// is flagged per row.
	store(t, m, diagBoko.at+addr.ActorOffActorTag+addr.TagOffData, u32b(0))
	if w, _ := walkActors(m, maxActorWalk); w.Rows[0].DataOK != true || w.Rows[1].DataOK {
		t.Errorf("DataOK: %v %v", w.Rows[0].DataOK, w.Rows[1].DataOK)
	}

	// An enemyice offset past the end of MEM1 is reported, not read.
	edge := diagBoko
	edge.at = addr.MEM1End - addr.ActorSize
	m = mem.NewFakeMem()
	layActors(t, m, edge)
	if w, err := walkActors(m, maxActorWalk); err != nil || w.Rows[0].Ice != "enemyice unreadable" {
		t.Errorf("ice past MEM1: %+v %v", w.Rows, err)
	}
}

// TestK1Offsets pins the enemyice table to the design's §7.2 timer
// offsets (mLightShrinkTimer is enemyice +6).
func TestK1Offsets(t *testing.T) {
	timers := map[string]uint32{
		"bk": 0x126A, "mo2": 0x2A5A, "st": 0x1FAE, "rd": 0x6E6, "cc": 0x822, "ki": 0x92A,
		"am": 0xAC2, "am2": 0xA92, "bb": 0xC86, "fm": 0xAEE, "ks": 0x6AE, "tn": 0x152A,
		"ph": 0x99E, "pt": 0x7EA, "pw": 0x88A, "bo": 0xA02,
	}
	if len(k1Classes) != len(timers) {
		t.Fatalf("%d classes, want %d", len(k1Classes), len(timers))
	}
	for p, k := range k1Classes {
		if want, ok := timers[k.name]; !ok || k.iceOff+6 != want {
			t.Errorf("proc 0x%X %s: timer at 0x%X, design says 0x%X", p, k.name, k.iceOff+6, want)
		}
		if k.iceOff < addr.ActorSize {
			t.Errorf("%s: enemyice at 0x%X is inside fopAc_ac_c", k.name, k.iceOff)
		}
	}
}

// TestZoneDump checks the zone slots and the room map against the fake
// game's own zone allocation.
func TestZoneDump(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(diagDRC); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(3, 2); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{g.OnSwitch(0xC5, 2), g.OnSwitch(0xE1, 3), g.OnActor(7, 2), g.OnActor(300, 2)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	dump := func() string {
		s, err := snap.Read(m)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		zoneDump(&b, s)
		return b.String()
	}
	out := dump()
	for _, want := range []string{
		"zone  0  room  2  switches 0xC5  room switches -  items 0x0000  dead actors 7,300\n",
		"zone  1  room  3  switches -  room switches 0xE1  items 0x0000  dead actors -\n",
		"free and empty: zones 2,3,4,",
		"room  2 -> zone  0 (count 2)\n",
		"room  3 -> zone  1 (count 2)  <- Link\n",
		"62 rooms have no zone",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("zone-dump lacks %q:\n%s", want, out)
		}
	}

	// A zone slot that no longer holds its room is not valid for writes.
	store(t, m, addr.Zones+addr.ZoneSize+addr.ZoneOffRoomNo, []byte{5})
	if out := dump(); !strings.Contains(out, "room  3 -> zone  1 (count 2)  INVALID: zone 1 holds room 5  <- Link") {
		t.Errorf("stale zone not flagged:\n%s", out)
	}
}

// probeField returns what coop-probe printed after label.
func probeField(out, label string) string {
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(l, label+" "); ok && len(label) <= 11 {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func TestCoopProbe(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(diagDRC); err != nil {
		t.Fatal(err)
	}
	probe := func() string {
		var b bytes.Buffer
		if err := coopProbe(&b, m, func() { g.Frames(3) }); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := probe()
	for label, want := range map[string]string{
		"stage":      `"M_NewD2" point 3 room 2 layer 2`,
		"save table": "3 DRC (mDan.mStageNo); STAG says table 3, key HUD true",
		"mReserve":   "all 0x50 bytes zero",
		"pending":    "key 0, max life 0, max magic 0",
		"heartbeat":  "spawn_trigger 12, +12 over 4 ticks",
		"STABLE":     "open",
		"IDLE":       "open",
		"HEARTBEAT":  "open",
	} {
		if got := probeField(out, label); !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want %q\n%s", label, got, want, out)
		}
	}

	store(t, m, addr.Reserve+2, []byte{'W'})
	g.SetPauseMenu(true)
	out = probe()
	if got := probeField(out, "mReserve"); !strings.HasPrefix(got, "NOT all zero: first non-zero byte at +0x02") {
		t.Errorf("mReserve: %q", got)
	}
	if got := probeField(out, "IDLE"); got != "closed: the pause menu is open" {
		t.Errorf("IDLE: %q", got)
	}

	// No heartbeat: the probe says why HEARTBEAT is closed.
	g.SetPauseMenu(false)
	var b bytes.Buffer
	if err := coopProbe(&b, m, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := probeField(b.String(), "HEARTBEAT"); !strings.HasPrefix(got, "closed: heartbeat has not moved") {
		t.Errorf("HEARTBEAT without frames: %q", got)
	}
}

// TestPhaseAccClean feeds a perfect 30 Hz heartbeat read every 50 µs.
func TestPhaseAccClean(t *testing.T) {
	const (
		frame = 33333 * time.Microsecond
		poll  = 50 * time.Microsecond
		took  = 5 * time.Microsecond
	)
	var a phaseAcc
	for at := time.Duration(0); at <= time.Second; at += poll {
		a.observe(at, uint32(at/frame), took)
	}
	r := a.report()
	if r.Changes != 30 || r.Beats != 30 || r.Skipped != 0 || r.Stalls != 0 || r.Regressed != 0 {
		t.Fatalf("report: %+v", r)
	}
	if d := r.Period - frame; d < -poll || d > poll {
		t.Errorf("period %v, want %v", r.Period, frame)
	}
	if r.Jitter > poll || r.StepMin < frame-poll || r.StepMax > frame+poll {
		t.Errorf("jitter %v, steps %v-%v", r.Jitter, r.StepMin, r.StepMax)
	}
	if r.DetectMax != poll || r.DetectMean != poll {
		t.Errorf("detection mean %v max %v, want %v", r.DetectMean, r.DetectMax, poll)
	}
	if r.ReadMin != took || r.ReadMax != took || r.ReadP50 != 8*time.Microsecond || r.ReadP99 != 8*time.Microsecond {
		t.Errorf("reads min %v max %v p50 %v p99 %v", r.ReadMin, r.ReadMax, r.ReadP50, r.ReadP99)
	}
}

// TestPhaseAccAnomalies: a gap in Go's polling skips a beat, a paused
// emulator stalls, and a save-state load sends the heartbeat backwards.
func TestPhaseAccAnomalies(t *testing.T) {
	const (
		frame = 33333 * time.Microsecond
		poll  = 50 * time.Microsecond
	)
	var a phaseAcc
	hb := func(at time.Duration) uint32 {
		switch {
		case at < 500*time.Millisecond:
			return uint32(at / frame)
		case at < 700*time.Millisecond:
			return uint32(500 * time.Millisecond / frame) // paused
		}
		return uint32((at - 200*time.Millisecond) / frame)
	}
	for at := time.Duration(0); at <= time.Second; at += poll {
		if at > 260*time.Millisecond && at < 310*time.Millisecond {
			continue // Go was descheduled for 50 ms, across a beat
		}
		a.observe(at, hb(at), time.Microsecond)
	}
	a.observe(time.Second+poll, 0, time.Microsecond) // a save state was loaded
	r := a.report()
	if r.Skipped != 1 || r.Stalls != 1 || r.Regressed != 1 {
		t.Errorf("skipped %d stalls %d regressed %d, want 1 each", r.Skipped, r.Stalls, r.Regressed)
	}
	if r.DetectMax < 50*time.Millisecond {
		t.Errorf("detection max %v misses the 50 ms gap", r.DetectMax)
	}
	if r.StepMax < 200*time.Millisecond {
		t.Errorf("step max %v misses the stall", r.StepMax)
	}
}

// fakeHeap lays out a JKRExpHeap at h with the free blocks given as
// {offset from the heap's start, content size}, linked in order.
func fakeHeap(t *testing.T, m *mem.FakeMem, h, start, end uint32, blocks [][2]uint32) {
	t.Helper()
	store(t, m, h, u32b(expHeapVtable))
	store(t, m, h+addr.HeapOffStart, u32b(start))
	store(t, m, h+addr.HeapOffEnd, u32b(end))
	store(t, m, h+addr.HeapOffSize, u32b(end-start))
	var head, tail uint32
	for i, b := range blocks {
		at := start + b[0]
		var prev, next uint32
		if i > 0 {
			prev = start + blocks[i-1][0]
		}
		if i+1 < len(blocks) {
			next = start + blocks[i+1][0]
		}
		store(t, m, at+addr.MemBlockOffSize, u32b(b[1]))
		store(t, m, at+addr.MemBlockOffPrev, u32b(prev))
		store(t, m, at+addr.MemBlockOffNext, u32b(next))
		if i == 0 {
			head = at
		}
		tail = at
	}
	store(t, m, h+addr.ExpHeapOffFreeHead, u32b(head))
	store(t, m, h+addr.ExpHeapOffFreeTail, u32b(tail))
}

func TestWalkHeap(t *testing.T) {
	const (
		heap  uint32 = 0x80C00000
		start uint32 = 0x80C00100
		end   uint32 = 0x80C10100
	)
	blocks := [][2]uint32{{0, 0x100}, {0x1000, 0x2000}, {0x8000, 0x40}}
	setup := func() *mem.FakeMem {
		m := mem.NewFakeMem()
		store(t, m, addr.ZeldaHeapPtr, u32b(heap))
		fakeHeap(t, m, heap, start, end, blocks)
		return m
	}
	h, err := walkHeap(setup(), "zelda", addr.ZeldaHeapPtr)
	if err != nil || h.Problem != "" || h.Blocks != 3 || h.Free != 0x2140 || h.Largest != 0x2000 || h.Size != 0x10000 {
		t.Fatalf("walk: %+v, %v", h, err)
	}

	cases := []struct {
		name string
		mut  func(m *mem.FakeMem)
		want string
	}{
		{"not created", func(m *mem.FakeMem) { m.Store(addr.ZeldaHeapPtr, u32b(0)) }, "not created"},
		{"pointer outside MEM1", func(m *mem.FakeMem) { m.Store(addr.ZeldaHeapPtr, u32b(0x7FFFFFF0)) }, "outside MEM1"},
		{"not an ExpHeap", func(m *mem.FakeMem) { m.Store(heap, u32b(0x8039CCC0)) }, "is not JKRExpHeap's"},
		{"bounds", func(m *mem.FakeMem) { m.Store(heap+addr.HeapOffSize, u32b(1)) }, "do not fit"},
		{"cycle", func(m *mem.FakeMem) { m.Store(start+0x8000+addr.MemBlockOffNext, u32b(start)) }, "has a cycle"},
		{"outside the heap", func(m *mem.FakeMem) { m.Store(start+0x1000+addr.MemBlockOffNext, u32b(end)) }, "outside the heap"},
		{"overrun", func(m *mem.FakeMem) { m.Store(start+0x8000+addr.MemBlockOffSize, u32b(0x10000)) }, "runs past"},
		{"prev", func(m *mem.FakeMem) { m.Store(start+0x1000+addr.MemBlockOffPrev, u32b(0)) }, "changed during the walk"},
		{"tail", func(m *mem.FakeMem) { m.Store(heap+addr.ExpHeapOffFreeTail, u32b(start)) }, "mTailFreeList"},
	}
	for _, c := range cases {
		m := setup()
		c.mut(m)
		h, err := walkHeap(m, "zelda", addr.ZeldaHeapPtr)
		if err != nil || !strings.Contains(h.Problem, c.want) {
			t.Errorf("%s: problem %q, err %v; want %q", c.name, h.Problem, err, c.want)
		}
	}

	var out bytes.Buffer
	m := setup()
	store(t, m, addr.JKRHeapCodeEnd, u32b(addr.PatchedArenaLo+0x20))
	store(t, m, addr.ZeldaHeapErrors, u32b(2))
	if err := heapFree(&out, m); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"zelda 2", "0x20 above the patched __OSArenaLo", "zelda    0x80C00000  0x80C00100-0x80C10100 (64 KiB): free 8 KiB in 3 blocks, largest 8 KiB", "[not created]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("heap-free lacks %q:\n%s", want, out.String())
		}
	}
}

func TestCompareBlob(t *testing.T) {
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	if d := compareBlob(slices.Clone(want), want, 8); d.Diff != 0 || len(d.Runs) != 0 || d.AllZero {
		t.Errorf("equal: %+v", d)
	}
	live := []byte{1, 0, 0, 4, 5, 6, 9, 8}
	if d := compareBlob(live, want, 8); d.Diff != 3 || !slices.Equal(d.Runs, []byteRun{{1, 2}, {6, 1}}) {
		t.Errorf("two runs: %+v", d)
	}
	if d := compareBlob(live, want, 1); len(d.Runs) != 1 || d.More != 1 {
		t.Errorf("capped runs: %+v", d)
	}
	if d := compareBlob(make([]byte, 8), want, 8); !d.AllZero || d.Diff != 8 {
		t.Errorf("all zero: %+v", d)
	}

	// The live check against the real blob, loaded and not.
	m := mem.NewFakeMem()
	var out bytes.Buffer
	if err := blobVerifyLive(&out, m); err != nil || !strings.Contains(out.String(), "all zero") {
		t.Errorf("not loaded: %q %v", out.String(), err)
	}
	store(t, m, inject.T2Address, inject.T2Bytes)
	out.Reset()
	if err := blobVerifyLive(&out, m); err != nil || !strings.HasSuffix(out.String(), "MATCH\n") {
		t.Errorf("loaded: %q %v", out.String(), err)
	}
	store(t, m, inject.T2Address+0x20, []byte{0xAA, 0xBB})
	out.Reset()
	blobVerifyLive(&out, m)
	if !strings.Contains(out.String(), "0x2 bytes differ in 1 runs; the first at +0x0020 (0x80410020)") {
		t.Errorf("mismatch: %q", out.String())
	}
}

// TestCanaryRegions pins the canary ranges to §4.5 and checks the guard
// refuses anything touching the main stack, T2, the mailbox or the arena.
func TestCanaryRegions(t *testing.T) {
	if addr.CanaryTRK.Start != 0x8040CFB0 || addr.CanaryTRK.End != 0x80410000 ||
		addr.CanaryArena.Start != 0x80413080 || addr.CanaryArena.End != 0x80414000 {
		t.Fatalf("canary ranges moved: %+v %+v", addr.CanaryTRK, addr.CanaryArena)
	}
	for _, r := range canaryRegions {
		if err := canarySafe(r); err != nil {
			t.Errorf("%s: %v", r.Name, err)
		}
	}
	for _, r := range []addr.Region{
		{Name: "into T2", Start: 0x8040FF00, End: 0x80410004},
		{Name: "slack after T2", Start: 0x80412E00, End: 0x80412E10},
		{Name: "mailbox", Start: addr.Mailbox, End: addr.Mailbox + 4},
		{Name: "mailbox tail", Start: 0x8041307C, End: 0x80413100},
		{Name: "main stack", Start: 0x8040CFA4, End: 0x8040D000},
		{Name: "stack header", Start: 0x8040CFA8, End: 0x8040D000},
		{Name: "boot LR slot", Start: 0x8040CFAC, End: 0x8040CFB0},
		{Name: "arena", Start: 0x80413F00, End: 0x80414004},
		{Name: "misaligned", Start: 0x8040CFAA, End: 0x8040D000},
		{Name: "empty", Start: 0x8040D000, End: 0x8040D000},
		{Name: "outside MEM1", Start: 0x7FFFFF00, End: 0x80000000},
	} {
		if err := canarySafe(r); err == nil {
			t.Errorf("%s 0x%08X-0x%08X passed the guard", r.Name, r.Start, r.End)
		}
	}
}

func TestArmAndCheckCanaries(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	for _, r := range canaryRegions {
		if err := armCanary(m, r, false); err != nil {
			t.Fatal(err)
		}
	}
	// Every write lies inside a canary region, and they cover both.
	covered := 0
	for _, w := range m.Writes() {
		in := false
		for _, r := range canaryRegions {
			in = in || r.Contains(w.Addr, len(w.Data))
		}
		if !in || len(w.Data) > canaryChunk {
			t.Errorf("write 0x%08X+0x%X is outside the canary regions or too big", w.Addr, len(w.Data))
		}
		covered += len(w.Data)
	}
	if want := int(addr.CanaryTRK.Size() + addr.CanaryArena.Size()); covered != want {
		t.Errorf("wrote 0x%X bytes, want 0x%X", covered, want)
	}
	for _, r := range canaryRegions {
		c, err := checkCanary(m, r)
		if err != nil || c.Intact != c.Words || c.Words != int(r.Size()/4) {
			t.Errorf("%s fresh: %+v %v", r.Name, c, err)
		}
	}

	// OSClearStack-style zeroing and one stray store.
	r := addr.CanaryTRK
	store(t, m, r.Start+0x100, make([]byte, 12))
	store(t, m, r.Start+0x400, u32b(0x80001234))
	c, _ := checkCanary(m, r)
	if c.Words-c.Intact != 4 || c.Zeroed != 3 ||
		!slices.Equal(c.Runs, []wordRun{{r.Start + 0x100, r.Start + 0x10C}, {r.Start + 0x400, r.Start + 0x404}}) {
		t.Errorf("after damage: %+v", c)
	}
	var out bytes.Buffer
	if err := regionCheck(&out, m); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"4 changed (3 now zero, 1 other)", "0x8040D0B0-0x8040D0BC (3 words)", "below arena  0x80413080-0x80414000 (992 words): all intact"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("region-probe check lacks %q:\n%s", want, out.String())
		}
	}

	// Data that is neither zero nor a canary means the memory is in use:
	// arming refuses unless forced. Re-arming over canaries is fine.
	m.ResetWrites()
	if err := armCanary(m, r, false); err == nil || !strings.Contains(err.Error(), "already hold data (first 0x8040D3B0 = 0x80001234)") {
		t.Errorf("armed over data: %v", err)
	}
	if len(m.Writes()) != 0 {
		t.Errorf("a refused arm wrote %d times", len(m.Writes()))
	}
	if err := armCanary(m, r, true); err != nil {
		t.Errorf("--force: %v", err)
	}
	if err := armCanary(m, addr.CanaryArena, false); err != nil {
		t.Errorf("re-arm over canaries: %v", err)
	}

	// Never armed.
	out.Reset()
	regionCheck(&out, mem.NewFakeMem())
	if !strings.Contains(out.String(), "no canaries (3092 words zero)") {
		t.Errorf("unarmed check: %s", out.String())
	}
}

// TestCanaryBesideBootLR: every booted game holds __init_hardware's
// return at 4(_stack_addr). Arming must succeed beside it, unforced, and
// leave it alone.
func TestCanaryBesideBootLR(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, addr.BootLRSlot, u32b(addr.BootLR))
	m.Record(true)
	for _, r := range canaryRegions {
		if err := armCanary(m, r, false); err != nil {
			t.Error(err)
		}
	}
	for _, w := range m.Writes() {
		if w.Addr < addr.MainStackHeader.End && addr.MainStackHeader.Start < w.Addr+uint32(len(w.Data)) {
			t.Errorf("write 0x%08X+0x%X touches the stack header", w.Addr, len(w.Data))
		}
	}
	if v, err := mem.ReadU32(m, addr.BootLRSlot); err != nil || v != addr.BootLR {
		t.Errorf("boot LR now 0x%08X %v", v, err)
	}
	var out bytes.Buffer
	if err := regionCheck(&out, m); err != nil || strings.Count(out.String(), "all intact") != 2 {
		t.Errorf("region-probe check: %v\n%s", err, out.String())
	}
}

// TestCanaryPreflight: arming needs the patched layout running.
func TestCanaryPreflight(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	tick := func() { g.Frames(1) }
	if err := canaryPreflight(m, tick); err == nil || !strings.Contains(err.Error(), "mCodeEnd") {
		t.Errorf("vanilla arena: %v", err)
	}
	store(t, m, addr.JKRHeapCodeEnd, u32b(0x8040EFE0)) // the vanilla __ArenaLo plus OSInitAlloc's table
	if err := canaryPreflight(m, tick); err == nil {
		t.Error("armed with the vanilla arena")
	}
	store(t, m, addr.JKRHeapCodeEnd, u32b(addr.PatchedArenaLo+0x20))
	if err := canaryPreflight(m, func() {}); err == nil || !strings.Contains(err.Error(), "heartbeat") {
		t.Errorf("paused: %v", err)
	}
	if err := canaryPreflight(m, tick); err != nil {
		t.Errorf("patched and running: %v", err)
	}
	var out bytes.Buffer
	if err := regionArm(&out, m, tick, false); err != nil || strings.Count(out.String(), "armed: ") != 2 {
		t.Errorf("regionArm: %v\n%s", err, out.String())
	}
}

func TestParseStageHop(t *testing.T) {
	good := []struct {
		args []string
		want stageHop
	}{
		{[]string{"M_NewD2"}, stageHop{"M_NewD2", 0, 0, -1}},
		{[]string{"sea", "3", "44"}, stageHop{"sea", 3, 44, -1}},
		{[]string{"M_NewD2", "3", "2", "2"}, stageHop{"M_NewD2", 3, 2, 2}},
		{[]string{"A_umikz", "0", "0", "11"}, stageHop{"A_umikz", 0, 0, 11}},
	}
	for _, c := range good {
		if got, err := parseStageHop(c.args); err != nil || got != c.want {
			t.Errorf("%q: %+v, %v", c.args, got, err)
		}
	}
	for _, args := range [][]string{
		nil,
		{"ABCDEFGH"},            // mName is char[8]
		{"a-b"},                 // not a stage name
		{"sea", "-1"},           // restart points
		{"sea", "0", "64"},      // rooms 0..63
		{"sea", "0", "0", "12"}, // l_layerFuncTable_p has 12 entries
		{"sea", "0", "0", "-2"}, // -1 is the only negative layer
		{"sea", "x"},            // not a number
		{"sea", "0", "0", "0", "0"},
	} {
		if _, err := parseStageHop(args); err == nil {
			t.Errorf("%q parsed", args)
		}
	}
}

// TestStageHop sends a hop through the fake game: the bytes land where
// ChangeStage reads them, mEnable is written last, nothing else is
// written, and closed gates refuse.
func TestStageHop(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	for _, s := range []fakegame.Stage{diagDRC, diagSea} { // the fake learns both STAGs
		if err := g.LoadStage(s); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() { g.Frames(3) }
	_, st, _, err := gateSnapshots(m, tick)
	if err != nil {
		t.Fatal(err)
	}
	m.Record(true)
	h := stageHop{Name: "M_NewD2", Point: 3, Room: 2, Layer: 2}
	if err := applyStageHop(m, h, st); err != nil {
		t.Fatal(err)
	}
	ws := m.Writes()
	next := addr.Region{Name: "mNextStage", Start: addr.NextStage, End: addr.NextStage + addr.NextStageSize}
	for _, w := range ws {
		if !next.Contains(w.Addr, len(w.Data)) {
			t.Errorf("write 0x%08X+%d is outside mNextStage", w.Addr, len(w.Data))
		}
	}
	if last := ws[len(ws)-1]; last.Addr != addr.NextStageEnable || !bytes.Equal(last.Data, []byte{1}) {
		t.Errorf("last write %+v, want mEnable = 1", last)
	}
	if err := g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	s, err := snap.Read(m)
	if err != nil {
		t.Fatal(err)
	}
	if s.StageName != "M_NewD2" || s.StagePoint != 3 || s.StageRoom != 2 || s.StageLayer != 2 || s.Table != addr.TableDRC {
		t.Errorf("after the hop: %q point %d room %d layer %d table %d", s.StageName, s.StagePoint, s.StageRoom, s.StageLayer, s.Table)
	}

	// Closed gates and a pending change refuse, writing nothing.
	m.ResetWrites()
	g.SetEvent(1, 0)
	_, st, _, _ = gateSnapshots(m, tick)
	if err := applyStageHop(m, h, st); err == nil || !strings.Contains(err.Error(), "an event is running") {
		t.Errorf("during an event: %v", err)
	}
	g.SetEvent(0, 0)
	_, st, _, _ = gateSnapshots(m, tick)
	if err := g.SetNextStage(diagSea); err != nil {
		t.Fatal(err)
	}
	if err := applyStageHop(m, h, st); err == nil || !strings.Contains(err.Error(), "asked for a stage change itself") {
		t.Errorf("with a change pending: %v", err)
	}
	if len(m.Writes()) != 0 {
		t.Errorf("refused hops wrote: %+v", m.Writes())
	}
	if st := (gate.Status{}); applyStageHop(m, h, st) == nil {
		t.Error("closed gates allowed a hop")
	}
}

func TestSplitDiagFlags(t *testing.T) {
	pos, flags, err := splitDiagFlags([]string{"arm", "--yes"}, "yes", "force")
	if err != nil || !slices.Equal(pos, []string{"arm"}) || !flags["yes"] || flags["force"] {
		t.Errorf("%q %v %v", pos, flags, err)
	}
	if _, _, err := splitDiagFlags([]string{"--yes"}); err == nil {
		t.Error("--yes accepted where no flag is")
	}
	for cmd := range coopDiagFlags {
		if _, ok := coopDiagUsage[cmd]; !ok {
			t.Errorf("%s has flags but no usage", cmd)
		}
	}
	for cmd, u := range coopDiagUsage {
		if !strings.HasPrefix(u, cmd) {
			t.Errorf("usage %q does not start with %s", u, cmd)
		}
	}
}
