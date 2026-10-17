package combat

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The re-read's fuzz target, in the style of internal/network/fuzz_test.go.
// The seeds run as ordinary tests; to fuzz it:
//
//	go test -run='^$' -fuzz=FuzzRevalidate -fuzztime=30s ./internal/coop/combat/
//
// Its hand-laid queue is wwapi/actors' (actors_test.go), with the K1
// layouts read.

// fakeActorType is what the hand-laid queues' g_fopAc_type and actor_type
// hold (any nonzero value; the game's counter starts at 0x9130001,
// f_pc_base.cpp:28-33).
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

func store(t testing.TB, m *mem.FakeMem, a uint32, b []byte) {
	t.Helper()
	if err := m.Store(a, b); err != nil {
		t.Fatal(err)
	}
}

func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func u16b(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }

// layActors links actors into g_fopAcTg_Queue in order, the way
// fopAc_Create and cTg_Addition leave them, every one executing (mInitState
// 2), and returns their nodes.
func layActors(t testing.TB, m *mem.FakeMem, actors ...fakeActor) []uint32 {
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
		store(t, m, n+addr.TagOffUse, []byte{1})
		store(t, m, a.at+addr.ActorOffActorType, u32b(fakeActorType))
		store(t, m, a.at+addr.ActorOffBsPcID, u32b(uint32(100+i)))
		store(t, m, a.at+addr.ActorOffProcName, u16b(uint16(a.proc)))
		store(t, m, a.at+addr.ActorOffInitState, []byte{2})
		store(t, m, a.at+addr.ActorOffSetID, u16b(a.setID))
		store(t, m, a.at+addr.ActorOffGroup, []byte{a.group})
		store(t, m, a.at+addr.ActorOffHomeRoomNo, []byte{byte(a.room)})
		store(t, m, a.at+addr.ActorOffHealth, []byte{byte(a.health)})
		store(t, m, a.at+addr.ActorOffMaxHealth, []byte{byte(a.health)})
		for j, f := range a.pos {
			store(t, m, a.at+addr.ActorOffCurPos+uint32(4*j), u32b(math.Float32bits(f)))
		}
	}
	var head, tail uint32
	if len(nodes) > 0 {
		head, tail = nodes[0], nodes[len(nodes)-1]
	}
	store(t, m, addr.ActorQueue+addr.ActorQueueOffHead, u32b(head))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffTail, u32b(tail))
	store(t, m, addr.ActorQueue+addr.ActorQueueOffSize, u32b(uint32(len(nodes))))
	store(t, m, addr.ProcessIDCounter, u32b(uint32(100+len(nodes))))
	return nodes
}

// iceUp gives a's enemyice its self-pointer and mode 1, as its create and
// enemy_ice's first call leave it.
func iceUp(t testing.TB, m *mem.FakeMem, a fakeActor) {
	t.Helper()
	c, _ := K1(a.proc)
	store(t, m, a.at+c.Ice, u32b(a.at))
	store(t, m, a.at+c.Ice+addr.EnemyIceOffMode, []byte{1})
}

var (
	link = fakeActor{at: 0x80A00000, proc: 0xA9, setID: addr.ActorSetIDNone, group: 1, room: -1, pos: [3]float32{10, 20, 30}}
	boko = fakeActor{at: 0x80A10000, proc: 0xBE, setID: 5, group: 2, room: 3, health: 4}
	pot  = fakeActor{at: 0x80A20000, proc: 0x1CB, setID: 9, group: 0, room: 3}
)

// TestWalkK1 reads a hand-laid Bokoblin with the K1 layouts: its
// enemyice, its timer address and its name.
func TestWalkK1(t *testing.T) {
	m := mem.NewFakeMem()
	layActors(t, m, link, boko, pot)
	iceUp(t, m, boko)
	w, err := Read(m, 0)
	if err != nil || !w.Complete() || len(w.Actors) != 3 {
		t.Fatalf("walk: %+v %v", w, err)
	}
	l, b := w.Actors[0], w.Actors[1]
	if l.Ice.Off != 0 || ProcName(l.Proc) != "player" {
		t.Errorf("Link: %+v", l)
	}
	if b.PID != 101 || !b.Ice.Read || !b.Ice.SelfOK || b.Ice.Mode != 1 || b.Ice.Timer != 0 || !b.HoldOK ||
		b.TimerAddr() != boko.at+0x126A || !b.Keyed() || ProcName(b.Proc) != "bk" {
		t.Errorf("Bokoblin: %+v", b)
	}
	if got, want := b.String(), "actor 0x80A10000 (pid 101, proc 0x00BE bk, setID 5, room 3)"; got != want {
		t.Errorf("String: %q, want %q", got, want)
	}
	if got, want := w.Actors[2].String(), "actor 0x80A20000 (pid 102, proc 0x01CB , setID 9, room 3)"; got != want {
		t.Errorf("String: %q, want %q", got, want)
	}

	// An enemyice past the end of MEM1 is not read, and no gate takes it.
	edge := boko
	edge.at = addr.MEM1End - addr.ActorSize
	m = mem.NewFakeMem()
	layActors(t, m, edge)
	if w, err := Read(m, 0); err != nil || len(w.Actors) != 1 || w.Actors[0].Ice.Read || Eligible(&w.Actors[0], false) != "enemyice unreadable" {
		t.Errorf("ice past MEM1: %+v %v", w.Actors, err)
	}
}

// FuzzRevalidate lays an eligible Bokoblin in a room with a zone, lets the
// input overwrite up to four bytes of its block (tag, fields, enemyice) or
// of the zone table, and checks Revalidate's verdict against an
// independent reading of the same memory: it passes only if the node is
// still queued, the actor the same process, every gate holds, the zone is
// there, and, planned with no weapon, it has taken none up (m0B30 0).
func FuzzRevalidate(f *testing.F) {
	f.Add(uint16(0), uint8(0), uint16(0), uint8(0))
	f.Add(uint16(addr.ActorOffBsPcID+3), uint8(9), uint16(0x1264+addr.EnemyIceOffTimer), uint8(1))
	f.Add(uint16(addr.ActorOffActorTag+addr.TagOffUse), uint8(0), uint16(addr.ActorOffHealth), uint8(0))
	f.Add(uint16(0xFFFF), uint8(0xFF), uint16(addr.ActorOffStatus), uint8(4))
	f.Fuzz(func(t *testing.T, off1 uint16, v1 uint8, off2 uint16, v2 uint8) {
		m := mem.NewFakeMem()
		room := int(boko.room)
		layActors(t, m, boko)
		iceUp(t, m, boko)
		zn, _ := addr.ZoneNoAddr(room)
		za, _ := addr.ZoneAddr(4)
		store(t, m, zn, []byte{4})
		store(t, m, za+addr.ZoneOffRoomNo, []byte{byte(room)})
		w, err := Read(m, 0)
		if err != nil || len(w.Actors) != 1 || Eligible(&w.Actors[0], false) != "" {
			t.Fatalf("the base Bokoblin: %+v %v", w, err)
		}
		a := w.Actors[0]
		poke := func(off uint16, v uint8) {
			switch {
			case off == 0xFFFF:
				store(t, m, zn, []byte{v}) // the room's mZoneNo
			case off == 0xFFFE:
				store(t, m, za+addr.ZoneOffRoomNo, []byte{v})
			case uint32(off) < 0x1280:
				store(t, m, boko.at+uint32(off), []byte{v})
			}
		}
		poke(off1, v1)
		poke(off2, v2)
		now, _, err := Revalidate(m, &a, nil, false)
		if err != nil && !errors.Is(err, actors.ErrChanged) {
			t.Fatalf("Revalidate: %v", err)
		}
		// The oracle: read the bytes as the game holds them.
		blk, _ := m.Read(boko.at, 0x1280)
		u8 := func(o uint32) uint8 { return blk[o] }
		u32 := func(o uint32) uint32 { return binary.BigEndian.Uint32(blk[o:]) }
		node := uint32(addr.ActorOffActorTag)
		z, _ := mem.ReadU8(m, zn)
		zr, _ := mem.ReadU8(m, za+addr.ZoneOffRoomNo)
		ice := uint32(0x1264)
		pass := u32(node+addr.TagOffData) == addr.ActorQueue && u8(node+addr.TagOffUse) == 1 &&
			u32(node+addr.TagOffTagData) == boko.at && u32(addr.ActorOffActorType) == fakeActorType &&
			u32(addr.ActorOffBsPcID) == a.PID && binary.BigEndian.Uint16(blk[addr.ActorOffProcName:]) == uint16(a.Proc) &&
			binary.BigEndian.Uint16(blk[addr.ActorOffSetID:]) == a.SetID && int8(u8(addr.ActorOffHomeRoomNo)) == a.HomeRoom &&
			u8(addr.ActorOffInitState) == 2 && u8(addr.ActorOffGroup) == 2 && int8(u8(addr.ActorOffHealth)) > 0 &&
			u32(addr.ActorOffStatus)&(addr.ActorStatusBoss|addr.ActorStatusCarry) == 0 &&
			binary.BigEndian.Uint16(blk[addr.ActorOffEventCommand:]) == 0 &&
			u32(ice) == boko.at && u8(ice+addr.EnemyIceOffTimer) == 0 && u8(ice+addr.EnemyIceOffMode) == 1 &&
			binary.BigEndian.Uint16(blk[ice+addr.EnemyIceOffFreezeTimer:]) == 0 &&
			u32(holdBK) == 0 && // m0B30: it took no weapon up since the walk
			z == 4 && zr == uint8(room)
		if pass != (err == nil) {
			t.Fatalf("Revalidate %v (%+v); the oracle says %v", err, now, pass)
		}
	})
}
