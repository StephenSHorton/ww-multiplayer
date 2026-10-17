package actors

import (
	"encoding/binary"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The walk's fuzz target, in the style of internal/network/fuzz_test.go.
// The seeds run as ordinary tests; to fuzz it:
//
//	go test -run='^$' -fuzz=FuzzWalk -fuzztime=30s ./wwapi/actors/

// fuzzBase is where FuzzWalk lays its nodes: four actor blocks of 0x400.
const fuzzBase uint32 = 0x80A00000

// FuzzWalk lays a queue header and four actor blocks from the input, each
// block's tag and fields as the bytes say, and checks what Read must hold
// for any memory: it never fails on MEM1 reads, takes at most the limit
// and the header's mSize, takes no node twice, and every actor it takes
// passed every check. It walks with testLayouts, so a block whose proc is
// one of theirs has that layout read too.
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
		w, err := ReadWith(m, int(limit), testLayouts)
		if err != nil {
			t.Fatalf("ReadWith: %v", err)
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
