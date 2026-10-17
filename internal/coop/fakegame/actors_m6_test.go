package fakegame

import (
	"errors"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// enemyRoom is the room the M6 tests place their enemies in.
const enemyRoom = 1

func placeM6() *Enemies {
	return PlaceEnemies(
		Enemy{Stage: drc.Name, Room: enemyRoom, SetID: 1, Proc: stagedata.ProcBK, Weapon: true},
		Enemy{Stage: drc.Name, Room: enemyRoom, SetID: 2, Proc: stagedata.ProcCC},
		Enemy{Stage: drc.Name, Room: enemyRoom, SetID: addr.ActorSetIDNone, Proc: stagedata.ProcCC},
		Enemy{Stage: drc.Name, Room: enemyRoom, SetID: 4, Proc: stagedata.ProcRD},
		Enemy{Stage: drc.Name, Room: enemyRoom, SetID: 5, Proc: stagedata.ProcBB, FailCreate: true},
		Enemy{Stage: drc.Name, Room: 2, SetID: 6, Proc: stagedata.ProcBB},
	)
}

func frames(t *testing.T, g *FakeGame, es *Enemies, n int) {
	t.Helper()
	for range n {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
}

// queuePIDs walks g_fopAcTg_Queue as the tool does, checking every link.
func queuePIDs(t *testing.T, m mem.Mem) []uint32 {
	t.Helper()
	head := u32(t, m, addr.ActorQueue+addr.ActorQueueOffHead)
	size := int(u32(t, m, addr.ActorQueue+addr.ActorQueueOffSize))
	var pids []uint32
	var prev uint32
	for node := head; node != 0; node = u32(t, m, node+addr.TagOffNext) {
		actor := node - addr.ActorOffActorTag
		switch {
		case u32(t, m, node+addr.TagOffPrev) != prev, u32(t, m, node+addr.TagOffData) != addr.ActorQueue,
			u8(t, m, node+addr.TagOffUse) != 1, u32(t, m, node+addr.TagOffTagData) != actor,
			u32(t, m, actor+addr.ActorOffActorType) != FakeActorType:
			t.Fatalf("node 0x%08X is no queued actor_tag", node)
		}
		pids = append(pids, u32(t, m, actor+addr.ActorOffBsPcID))
		prev = node
	}
	if len(pids) != size || u32(t, m, addr.ActorQueue+addr.ActorQueueOffTail) != prev {
		t.Fatalf("walked %d nodes ending 0x%08X; the header says %d, tail 0x%08X", len(pids), prev, size, u32(t, m, addr.ActorQueue+addr.ActorQueueOffTail))
	}
	return pids
}

func TestEnemiesLifecycle(t *testing.T) {
	g, m := newGame(t)
	es := placeM6()
	load(t, g, drc)
	enter(t, g, enemyRoom)
	frames(t, g, es, 1)
	// Room 1's five entries and the Bokoblin's weapon are queued; room 2
	// has no zone, so its Kargaroc is not created.
	if n := len(queuePIDs(t, m)); n != 6 {
		t.Fatalf("%d actors queued, want 6: %+v", n, es.Live())
	}
	if c := u32(t, m, addr.ProcessIDCounter); c != 7 {
		t.Errorf("the process counter is %d after six creates", c)
	}
	frames(t, g, es, 2)
	// The failed create left; the rest execute, enemy_ice run once.
	if n := len(queuePIDs(t, m)); n != 5 {
		t.Fatalf("%d actors after the failed create, want 5", n)
	}
	bk, ok := es.Of(0)
	if !ok || bk.InitState != 2 || u32(t, m, bk.Addr+0x1264) != bk.Addr || u8(t, m, bk.Addr+0x1264+addr.EnemyIceOffMode) != 1 {
		t.Fatalf("the Bokoblin: %+v, enemyice self 0x%08X, mode %d", bk, u32(t, m, bk.Addr+0x1264), u8(t, m, bk.Addr+0x1264+addr.EnemyIceOffMode))
	}
	w, ok := es.WeaponOf(0)
	if !ok || w.Proc != procBoko || u32(t, m, bk.Addr+0x1200) != w.PID || u32(t, m, w.Addr+addr.ActorOffStatus)&addr.ActorStatusCarry == 0 {
		t.Fatalf("the Bokoblin's weapon: %+v", w)
	}

	// The light-shrink: one byte at the timer. Paused, nothing happens.
	cc, _ := es.Of(1)
	timer := cc.Addr + 0x81C + addr.EnemyIceOffTimer
	if err := m.Write(timer, []byte{1}); err != nil {
		t.Fatal(err)
	}
	g.SetPauseMenu(true)
	frames(t, g, es, 100)
	if u8(t, m, timer) != 1 {
		t.Fatal("the timer ran with the pause menu up")
	}
	g.SetPauseMenu(false)
	frames(t, g, es, 89)
	if _, ok := es.Of(1); !ok {
		t.Fatal("the ChuChu went before its 90 frames")
	}
	frames(t, g, es, 2)
	if _, ok := es.Of(1); ok || !slices.Equal(es.Shrunk(), []uint32{cc.PID}) {
		t.Fatalf("the ChuChu was not deleted by its shrink: %v", es.Shrunk())
	}
	z := zoneOf(t, m, enemyRoom)
	za, _ := addr.ZoneAddr(z)
	if !deadAt(t, m, za, 2) {
		t.Fatal("the shrink did not record setID 2 dead")
	}

	// A ReDead's own death records it on every frame before it goes.
	rd, _ := es.Of(3)
	if err := es.Kill(g, rd.PID); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 5)
	if _, ok := es.Of(3); !ok || !deadAt(t, m, za, 4) || int8(u8(t, m, rd.Addr+addr.ActorOffHealth)) > 0 {
		t.Fatal("the dying ReDead is gone, unrecorded or alive")
	}
	frames(t, g, es, 30)
	if _, ok := es.Of(3); ok {
		t.Fatal("the ReDead never went")
	}

	// Hidden, the room's actors go; shown, they come back but for the
	// dead keyed ones: the unkeyed ChuChu and the Bokoblin, and the
	// weapon.
	if err := g.HideRoom(enemyRoom); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 1)
	if n := len(queuePIDs(t, m)); n != 0 {
		t.Fatalf("%d actors in a hidden room", n)
	}
	if err := g.ShowRoom(enemyRoom); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 3)
	var placed []int
	for _, l := range es.Live() {
		placed = append(placed, l.Placed)
	}
	slices.Sort(placed)
	if !slices.Equal(placed, []int{-1, 0, 2}) {
		t.Fatalf("after the show: %v, want the weapon, the Bokoblin and the unkeyed ChuChu", placed)
	}
	// A Kargaroc's own death records nothing (d_a_bb.cpp:1969-1972).
	enter(t, g, 2, enemyRoom)
	frames(t, g, es, 3)
	bb, ok := es.Of(5)
	if !ok {
		t.Fatal("room 2's Kargaroc was not created")
	}
	if err := es.Kill(g, bb.PID); err != nil {
		t.Fatal(err)
	}
	if za2, _ := addr.ZoneAddr(zoneOf(t, m, 2)); deadAt(t, m, za2, 6) {
		t.Fatal("a Kargaroc's own death recorded it")
	}

	// Another stage: every actor goes.
	load(t, g, outset)
	frames(t, g, es, 1)
	if n := len(queuePIDs(t, m)); n != 0 {
		t.Fatalf("%d actors after the stage change", n)
	}
}

func TestEnemiesFreeAndReuse(t *testing.T) {
	g, m := newGame(t)
	es := placeM6()
	load(t, g, drc)
	enter(t, g, enemyRoom)
	frames(t, g, es, 3)
	cc, _ := es.Of(1)
	pid, err := es.FreeAndReuse(g, cc.PID, 0x1CB)
	if err != nil {
		t.Fatal(err)
	}
	pids := queuePIDs(t, m)
	if pid == cc.PID || !slices.Contains(pids, pid) || slices.Contains(pids, cc.PID) ||
		u32(t, m, cc.Addr+addr.ActorOffBsPcID) != pid || u16(t, m, cc.Addr+addr.ActorOffProcName) != 0x1CB {
		t.Fatalf("the reused block: pid %d (was %d), queue %v", pid, cc.PID, pids)
	}
	if _, err := es.FreeAndReuse(g, cc.PID, 0x1CB); !errors.Is(err, ErrNoActor) {
		t.Errorf("reusing a gone pid: %v", err)
	}
}

// TestEnemiesOnActorAsserts: a keyed death recorded once its room's zone
// is gone is the assert that halts the game (d_save.cpp:1678-1679).
func TestEnemiesOnActorAsserts(t *testing.T) {
	g, _ := newGame(t)
	es := placeM6()
	load(t, g, drc)
	enter(t, g, enemyRoom)
	frames(t, g, es, 3)
	bk, _ := es.Of(0)
	g.mu.Lock()
	g.setZoneNo(enemyRoom, -1)
	g.mu.Unlock()
	if err := es.Kill(g, bk.PID); !errors.Is(err, ErrAssert) {
		t.Fatalf("a death with no zone: %v, want an assert", err)
	}
}

func TestSetLinkDemo(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc)
	g.SetLinkDemo(3)
	if u16(t, m, PlayerActor+addr.PlayerOffDemoType) != 3 {
		t.Fatal("the demo type was not set")
	}
}

// deadAt reports whether setID id is recorded dead in the zone at za.
func deadAt(t *testing.T, m mem.Mem, za uint32, id int) bool {
	t.Helper()
	bit, err := addr.ZoneActorBit(id)
	if err != nil {
		t.Fatal(err)
	}
	return u8(t, m, za+uint32(bit.Off))&bit.Mask != 0
}
