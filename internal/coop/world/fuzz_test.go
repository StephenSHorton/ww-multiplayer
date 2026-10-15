package world

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// Fuzz targets for everything here that reads bytes someone else wrote:
// the world file and the journal (any program, or a bad disk, may have
// written the user's config directory) and the hub's merge and scoped
// stores (fed from peers' frames). The seed corpora are built in code and
// run as ordinary tests under `go test`; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzDecode -fuzztime=20s ./internal/coop/world/

// checkWorld fails unless w keeps the world's invariants: only shared
// bits in the image and the tombstones, no tombstoned bit in the image,
// at most MaxFreshBinds pending binds.
func checkWorld(t *testing.T, w *World) {
	t.Helper()
	for off := range w.image {
		if w.image[off]&^shared[off] != 0 || w.tomb[off]&^shared[off] != 0 || w.image[off]&w.tomb[off] != 0 {
			t.Fatalf("byte 0x%03X: image 0x%02X tomb 0x%02X mask 0x%02X", off, w.image[off], w.tomb[off], shared[off])
		}
	}
	if len(w.fresh) > MaxFreshBinds {
		t.Fatalf("%d fresh binds", len(w.fresh))
	}
}

func FuzzDecode(f *testing.F) {
	good := Encode(sampleWorld(f))
	empty := Encode(New(testID, testCreated))
	optional := reseal(append(slices.Clone(good), section(0x8001, []byte("later"))...))
	for _, seed := range [][]byte{good, empty, optional, good[:len(good)/2], good[:headerLen], nil, []byte(FileMagic)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		w, err := Decode(data)
		if err != nil {
			return
		}
		checkWorld(t, w)
		// What decodes reads back the same once written.
		b := Encode(w)
		again, err := Decode(b)
		if err != nil {
			t.Fatalf("re-decoding: %v", err)
		}
		if !worldsEqual(w, again) || !bytes.Equal(Encode(again), b) {
			t.Fatal("decode is not stable")
		}
	})
}

func FuzzParseRecord(f *testing.F) {
	for _, r := range []Record{
		{Seq: 42, Time: testTime, Origin: 2, Op: OpGains, Gains: []Gain{{0x051, 0x04}, {0x3A4, 0x80}}},
		{Seq: 3, Time: testTime, Op: OpWorld, Gains: []Gain{{0x380, 0xFF}}},
		{Seq: 4, Time: testTime, Op: OpTombstone, Gains: []Gain{{0x051, 0x01}}},
		{Seq: 5, Time: testTime, Op: OpFresh, Nonce: testID, Minted: testTime},
		{Seq: 6, Time: testTime, Op: OpUnfresh, Nonce: testID},
	} {
		f.Add(r.Format())
	}
	f.Add("")
	f.Add("1 x 0 gains 000:01 00000000")
	f.Fuzz(func(t *testing.T, line string) {
		r, err := ParseRecord(line)
		if err != nil {
			return
		}
		if r.Format() != line {
			t.Fatalf("%q formats back as %q", line, r.Format())
		}
		// Replaying any record keeps the world whole.
		w := sampleWorld(t)
		replay(w, []Record{r})
		checkWorld(t, w)
	})
}

// FuzzMerge feeds the merge arbitrary proposals: data is read as gains,
// three bytes each, and every 0x778 bytes of it also as an image.
func FuzzMerge(f *testing.F) {
	f.Add([]byte{0x00, 0x51, 0xFF, 0x07, 0x77, 0x01, 0x00, 0x02, 0x0C})
	f.Add(bytes.Repeat([]byte{0xFF}, ImageSize+30))
	f.Add([]byte{byte(addr.OffWalletSize >> 8), byte(addr.OffWalletSize), 3, byte(addr.OffWalletSize >> 8), byte(addr.OffWalletSize), 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		w := sampleWorld(t)
		if len(data) > 0 {
			w.Tombstone(int(data[0])%ImageSize, data[len(data)-1])
		}
		var gs []Gain
		for b := data; len(b) >= 3; b = b[3:] {
			gs = append(gs, Gain{Off: binary.BigEndian.Uint16(b), Val: b[2]})
		}
		before := w.Image()
		res := w.ApplyGains(gs)
		after := w.Image()
		checkGrows(t, "ApplyGains", &before, &after)
		if !slices.Equal(Diff(&before, &after), res.Delta) {
			t.Fatal("delta is not what changed")
		}
		for b := data; len(b) >= ImageSize; b = b[ImageSize:] {
			img := Image(b[:ImageSize])
			before = w.Image()
			res := w.Merge(&img)
			after = w.Image()
			checkGrows(t, "Merge", &before, &after)
			if !slices.Equal(Diff(&before, &after), res.Delta) {
				t.Fatal("delta is not what changed")
			}
		}
		checkWorld(t, w)
		// Idempotent.
		if res := w.ApplyGains(gs); res.Changed() {
			t.Fatalf("the same gains again changed %v", res.Delta)
		}
	})
}

// FuzzScopes runs arbitrary presence changes and scoped reports: four
// bytes per step (player, op, a, b). It never panics; every store left is
// non-empty and kept alive by someone in its scope; every relay goes only
// to other players in the scope; and no store holds, and no relay
// carries, a switch bit the filter (SwitchFilter over a verdict that
// syncs one switch in three) refuses: none of 0xF0-0xFF, of a room
// outside 0..63, or outside its kind's store.
func FuzzScopes(f *testing.F) {
	f.Add([]byte{1, 0, 0, 1, 2, 0, 0, 1, 1, 1, 0, 0xFF, 1, 2, 1, 0x0F, 2, 3, 0, 0})
	f.Add([]byte{1, 0, 3, 3, 1, 4, 2, 9, 2, 0, 3, 3, 2, 5, 0, 0})
	f.Add([]byte{1, 0, 1, 4, 2, 0, 1, 4, 1, 2, 0, 0xFF, 1, 3, 2, 0xFF, 1, 4, 2, 0xFF, 2, 0, 7, 4})
	stages := []string{"sea", "M_NewD2", "M_DragB", "sea_T", "", "kindan"}
	synced := func(k Key, no int) bool { return no%3 == 0 }
	filter := SwitchFilter(synced)
	allowed := func(k Key, w int, bits uint32) bool {
		return k.Kind == ScopeKill || bits&^filter(k, w) == 0
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s := NewScopes()
		s.Filter = filter
		for b := data; len(b) >= 4; b = b[4:] {
			p, op, a, c := Player(b[0]%4), b[1]%8, int(b[2]), int(b[3])
			switch op {
			case 0:
				s.SetPresence(p, Place{Stage: stages[a%len(stages)], Table: c%18 - 1, Room: a%66 - 1})
			case 1:
				s.Leave(p)
			default:
				place, _ := s.PlaceOf(p)
				k := Key{Kind: ScopeKind(op - 1), Stage: place.Stage, Table: place.Table, Room: place.Room}
				if k.Kind == ScopeDan {
					k.Room = -1
				}
				r, err := s.Apply(p, k, []Word{{Word: uint8(a % 18), Bits: uint32(c) << (a % 25)}})
				if err != nil {
					continue
				}
				for _, w := range r.Delta {
					if !allowed(k, int(w.Word), w.Bits) {
						t.Fatalf("%s relays word %d bits 0x%X the filter refuses", k, w.Word, w.Bits)
					}
				}
				for _, q := range r.To {
					if at, _ := s.PlaceOf(q); q == p || !k.reaches(at) {
						t.Fatalf("%s relayed to %d at %s", k, q, at)
					}
				}
			}
		}
		for _, k := range s.Keys() {
			alive := false
			for _, at := range s.at {
				alive = alive || k.reaches(at)
			}
			if !alive || len(s.Get(k)) == 0 || k.Kind == ScopeRoomEdge {
				t.Fatalf("store %s: alive %v, words %v", k, alive, s.Get(k))
			}
			for _, w := range s.Get(k) {
				if !allowed(k, int(w.Word), w.Bits) {
					t.Fatalf("store %s holds word %d bits 0x%X the filter refuses", k, w.Word, w.Bits)
				}
			}
		}
		if err := checkIndex(s); err != nil {
			t.Fatal(err)
		}
	})
}
