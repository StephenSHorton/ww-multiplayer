package actors

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
)

// Fuzz targets for the walk and the re-read, in the style of
// internal/network/fuzz_test.go. The seeds run as ordinary tests; to fuzz
// one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzWalk -fuzztime=30s ./internal/coop/actors/

// fuzzBase is where FuzzWalk lays its nodes: four actor blocks of 0x400.
const fuzzBase uint32 = 0x80A00000

// FuzzWalk lays a queue header and four actor blocks from the input, each
// block's tag and fields as the bytes say, and checks what Read must hold
// for any memory: it never fails on MEM1 reads, takes at most the limit
// and the header's mSize, takes no node twice, and every actor it takes
// passed every check.
func FuzzWalk(f *testing.F) {
	ok := make([]byte, 0, 16+4*0x40)
	ok = binary.BigEndian.AppendUint32(ok, fuzzBase+addr.ActorOffActorTag) // head
	ok = binary.BigEndian.AppendUint32(ok, fuzzBase+0x400+addr.ActorOffActorTag)
	ok = binary.BigEndian.AppendUint32(ok, 2)
	ok = binary.BigEndian.AppendUint32(ok, fakeActorType)
	f.Add(ok, uint8(0))
	f.Add([]byte{}, uint8(1))
	f.Add(make([]byte, 64), uint8(3))
	f.Add(append(make([]byte, 4), "00000000"...), uint8('E')) // a size, no actor type
	f.Fuzz(func(t *testing.T, b []byte, limit uint8) {
		m := mem.NewFakeMem()
		word := func(i int) uint32 {
			if 4*i+4 > len(b) {
				return 0
			}
			return binary.BigEndian.Uint32(b[4*i:])
		}
		// The header: head, tail, size, then g_fopAc_type.
		store(t, m, addr.ActorQueue, b2(word(0), word(1), word(2)))
		store(t, m, addr.FopAcType, u32b(word(3)))
		// Four blocks: a tag (prev, data, next, tagData, use) and the
		// actor_type, each word taken from the input where it reaches.
		for i := range 4 {
			at := fuzzBase + uint32(i)*0x400
			w := func(j int) uint32 { return word(4 + i*8 + j) }
			node := at + addr.ActorOffActorTag
			store(t, m, node, b2(w(0), w(1), w(2), w(3)))
			store(t, m, node+addr.TagOffUse, []byte{byte(w(4))})
			store(t, m, at+addr.ActorOffActorType, u32b(w(5)))
			store(t, m, at+addr.ActorOffProcName, u16b(uint16(w(6))))
		}
		w, err := Read(m, int(limit))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		max := int(limit)
		if max == 0 {
			max = MaxNodes
		}
		seen := map[uint32]bool{}
		for _, a := range w.Actors {
			if len(w.Actors) > max || int32(len(w.Actors)) > w.Size || seen[a.Node] || NodeBad(a.Node) != "" ||
				a.Addr != a.Node-addr.ActorOffActorTag {
				t.Fatalf("took %+v (of %d, limit %d, mSize %d)", a, len(w.Actors), max, w.Size)
			}
			seen[a.Node] = true
			tag, _ := m.Read(a.Node, addr.TagSize)
			typ, _ := mem.ReadU32(m, a.Addr+addr.ActorOffActorType)
			if tagBad(a.Node, tag) != "" || typ != w.Type || typ == 0 {
				t.Fatalf("took an actor that fails a check: %+v", a)
			}
		}
		if w.Complete() && (int32(len(w.Actors)) != w.Size || len(w.Actors) > 0 && w.Actors[len(w.Actors)-1].Node != w.Tail) {
			t.Fatalf("complete, but %d actors against mSize %d", len(w.Actors), w.Size)
		}
	})
}

func b2(ws ...uint32) []byte {
	var out []byte
	for _, w := range ws {
		out = binary.BigEndian.AppendUint32(out, w)
	}
	return out
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
		if err != nil && !errors.Is(err, ErrChanged) {
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
