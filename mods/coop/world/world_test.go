package world

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

var (
	testID      = ID{0xC0, 0x0B, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	testCreated = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
)

// offItem is mGetItem[3], the grappling hook's flag byte: an OR byte with
// mask 0xFF, the tests' shared flag byte of any bits. mGetItem[0..2] share
// only bit 0 (the rest record the bottle contents held).
const offItem = addr.OffGetItem + 3

// randImage is a random image: every byte random, so the shared bits, the
// bits outside the masks and the bytes that never merge are all exercised.
// Sparse makes most bytes zero, as a real image's flags are.
func randImage(r *rand.Rand, sparse bool) *save.Image {
	var img save.Image
	for off := range img {
		if !sparse || r.IntN(16) == 0 {
			img[off] = byte(r.Uint32())
		}
	}
	return &img
}

// randGains is a random batch: real offsets mostly, with duplicates, any
// order, some offsets past the image and values outside the masks.
func randGains(r *rand.Rand) []Gain {
	gs := make([]Gain, r.IntN(40))
	for i := range gs {
		off := r.IntN(ImageSize)
		if r.IntN(50) == 0 {
			off = ImageSize + r.IntN(100)
		}
		gs[i] = Gain{Off: uint16(off), Val: byte(r.Uint32())}
	}
	if len(gs) > 2 {
		gs = append(gs, gs[r.IntN(len(gs))]) // a duplicate
	}
	return gs
}

func newTestWorld() *World {
	w := New(testID, testCreated)
	w.SetPolicy(FullPolicy())
	return w
}

// checkGrows fails unless after is before grown under the class rules:
// no shared byte lowered, no bit outside a class mask, nothing in a byte
// that never merges.
func checkGrows(t *testing.T, what string, before, after *save.Image) {
	t.Helper()
	for off := range after {
		m := shared[off]
		if after[off]&^m != 0 {
			t.Fatalf("%s: byte 0x%03X (%s) = 0x%02X has bits outside its mask 0x%02X", what, off, class.Of(off).Field, after[off], m)
		}
		if isMax[off] {
			if after[off] < before[off] {
				t.Fatalf("%s: MAX byte 0x%03X lowered 0x%02X -> 0x%02X", what, off, before[off], after[off])
			}
		} else if before[off]&^after[off] != 0 {
			t.Fatalf("%s: OR byte 0x%03X lost bits 0x%02X -> 0x%02X", what, off, before[off], after[off])
		}
	}
}

// TestJoinLaws: the per-class merge is a join, commutative, associative
// and idempotent, never lowers either side and never leaves the masks.
func TestJoinLaws(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 200; i++ {
		a, b, c := randImage(r, i%2 == 0), randImage(r, i%3 == 0), randImage(r, true)
		ab, ba := Join(a, b), Join(b, a)
		if ab != ba {
			t.Fatalf("iteration %d: Join is not commutative", i)
		}
		bc := Join(b, c)
		if l, rr := Join(&ab, c), Join(a, &bc); l != rr {
			t.Fatalf("iteration %d: Join is not associative", i)
		}
		if aa, sa := Join(a, a), Shared(a); aa != sa {
			t.Fatalf("iteration %d: Join(a, a) != Shared(a)", i)
		}
		if again := Join(&ab, b); again != ab {
			t.Fatalf("iteration %d: joining b again changed the join", i)
		}
		sa, sb := Shared(a), Shared(b)
		checkGrows(t, "Join over a", &sa, &ab)
		checkGrows(t, "Join over b", &sb, &ab)
		// Diff takes a to the join, and nothing is left after.
		got := sa
		Apply(&got, Diff(&sa, b))
		if got != ab {
			t.Fatalf("iteration %d: a + Diff(a, b) != Join(a, b)", i)
		}
		if d := Diff(&ab, b); len(d) != 0 {
			t.Fatalf("iteration %d: Diff(Join(a, b), b) = %v, want none", i, d)
		}
	}
}

// TestMergeOrderFree: any order of the same proposals, with duplicates and
// re-sends, ends in the same image; every step's delta is exactly what
// changed, the seq moves only with a change, and nothing is ever lowered.
func TestMergeOrderFree(t *testing.T) {
	type op struct {
		img   *save.Image
		gains []Gain
	}
	for seed := uint64(0); seed < 25; seed++ {
		r := rand.New(rand.NewPCG(seed, 99))
		var ops []op
		want := save.Image{}
		for i := 0; i < 30; i++ {
			if r.IntN(3) == 0 {
				img := randImage(r, true)
				ops = append(ops, op{img: img})
				want = Join(&want, img)
			} else {
				gs := randGains(r)
				ops = append(ops, op{gains: gs})
				var img save.Image
				Apply(&img, gs)
				want = Join(&want, &img)
			}
		}
		for run := 0; run < 4; run++ {
			order := slices.Clone(ops)
			r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
			order = append(order, order[:r.IntN(len(order))]...) // re-sent proposals
			w := newTestWorld()
			for _, o := range order {
				before, seq := w.Image(), w.Seq()
				var res Result
				if o.img != nil {
					res = w.Merge(o.img)
				} else {
					res = w.ApplyGains(o.gains)
				}
				after := w.Image()
				checkGrows(t, "merge step", &before, &after)
				if d := Diff(&before, &after); !slices.Equal(d, res.Delta) {
					t.Fatalf("seed %d: delta %v, but the image changed by %v", seed, res.Delta, d)
				}
				if wantSeq := seq + uint64(min(len(res.Delta), 1)); res.Seq != wantSeq || w.Seq() != wantSeq {
					t.Fatalf("seed %d: seq %d -> %d with %d changed bytes", seed, seq, res.Seq, len(res.Delta))
				}
			}
			if got := w.Image(); got != want {
				t.Fatalf("seed %d run %d: image differs from the join of all proposals:\n%v", seed, run, Diff(&got, &want))
			}
			// Idempotent: the whole history again changes nothing.
			seq := w.Seq()
			for _, o := range ops {
				var res Result
				if o.img != nil {
					res = w.Merge(o.img)
				} else {
					res = w.ApplyGains(o.gains)
				}
				if res.Changed() || res.Seq != seq {
					t.Fatalf("seed %d: replaying a proposal changed the world: %v", seed, res.Delta)
				}
			}
		}
	}
}

// TestApplyGainsRefuses: what the hub must refuse, refused with the reason
// a NOTICE can carry, and the rest of the batch merged.
func TestApplyGainsRefuses(t *testing.T) {
	letter := addr.OffEvent + int(addr.EventByte(0x7A03).Off) // LETTER_ROCK_SPIRE_SHOP_AD, MAX within 0x03
	dungeon := addr.OffMemBit(3) + addr.MemBitOffDungeonItem  // OR within 0x3F
	sw := addr.OffMemBit(3) + addr.MemBitOffSwitch            // OR_MASKED
	tombed := offItem
	for _, c := range []struct {
		name   string
		policy *Policy
		gains  []Gain
		delta  []Gain
		reject []Reject
	}{
		{"offset past the image", FullPolicy(), []Gain{{ImageSize, 1}}, nil,
			[]Reject{{ImageSize, 1, ReasonOffset}}},
		{"mLife never merges", FullPolicy(), []Gain{{addr.OffLife, 0x0C}}, nil,
			[]Reject{{addr.OffLife, 0x0C, ReasonClass}}},
		{"the tag never merges", FullPolicy(), []Gain{{addr.OffReserve, 'W'}}, nil,
			[]Reject{{addr.OffReserve, 'W', ReasonClass}}},
		{"keyNum is derived", FullPolicy(), []Gain{{uint16(addr.OffMemBit(3) + addr.MemBitOffKeyNum), 2}}, nil,
			[]Reject{{uint16(addr.OffMemBit(3) + addr.MemBitOffKeyNum), 2, ReasonClass}}},
		{"OR bits outside the mask are cut", FullPolicy(), []Gain{{uint16(dungeon), 0xC1}},
			[]Gain{{uint16(dungeon), 0x01}}, []Reject{{uint16(dungeon), 0xC0, ReasonMask}}},
		{"a MAX value outside the mask is refused whole", FullPolicy(), []Gain{{uint16(letter), 0x05}}, nil,
			[]Reject{{uint16(letter), 0x05, ReasonMask}}},
		{"a MAX value inside merges", FullPolicy(), []Gain{{uint16(letter), 0x02}, {uint16(letter), 0x01}},
			[]Gain{{uint16(letter), 0x02}}, nil},
		{"switches fail closed by default", DefaultPolicy(), []Gain{{uint16(sw), 0x80}, {offItem, 0x01}},
			[]Gain{{offItem, 0x01}}, []Reject{{uint16(sw), 0x80, ReasonPolicy}}},
		{"tombstoned bits are refused", FullPolicy(), []Gain{{uint16(tombed), 0x03}},
			[]Gain{{uint16(tombed), 0x01}}, []Reject{{uint16(tombed), 0x02, ReasonTombstone}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := New(testID, time.Time{})
			w.SetPolicy(c.policy)
			if _, err := w.Tombstone(tombed, 0x02); err != nil {
				t.Fatal(err)
			}
			res := w.ApplyGains(c.gains)
			if !slices.Equal(res.Delta, c.delta) || !slices.Equal(res.Rejected, c.reject) {
				t.Errorf("delta %v rejected %v, want %v and %v", res.Delta, res.Rejected, c.delta, c.reject)
			}
		})
	}
}

// TestPolicyGrants: the default policy opens no memory switch or story bit;
// GrantSwitch and GrantEventBit open exactly the bit the game's setter
// sets, and refuse what is not in the image.
func TestPolicyGrants(t *testing.T) {
	p := DefaultPolicy()
	for off := range p {
		if want := shared[off]; isMasked[off] && p[off] != 0 || !isMasked[off] && p[off] != want {
			t.Fatalf("default policy byte 0x%03X = 0x%02X", off, p[off])
		}
	}
	// Switch 0x25 of DRC: mSwitch[1] bit 5, the word's last byte
	// (dSv_memBit_c::onSwitch, d_save.cpp:1084-1087).
	if err := p.GrantSwitch(addr.TableDRC, 0x25); err != nil {
		t.Fatal(err)
	}
	off := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffSwitch + 4 + 3
	if p[off] != 0x20 {
		t.Errorf("switch 0x25 opened byte 0x%03X = 0x%02X, want 0x20", off, p[off])
	}
	for _, c := range []struct{ t, no int }{{16, 1}, {-1, 1}, {3, 0x80}, {3, 0xC5}, {3, 0xFF}, {3, 0xF0}} {
		if err := p.GrantSwitch(c.t, c.no); err == nil {
			t.Errorf("GrantSwitch(%d, 0x%X) accepted", c.t, c.no)
		}
	}
	q := DefaultPolicy()
	q.GrantSwitches(func(t, no int) bool { return t == addr.TableDRC && no == 0x25 })
	if *q != *p {
		t.Error("GrantSwitches opened something else than GrantSwitch")
	}
	if err := p.GrantEventBit(0x0F80); err != nil { // MET_KORL (§6.5)
		t.Fatal(err)
	}
	if b := p[addr.OffEvent+0x0F]; b != 0x80 {
		t.Errorf("event 0x0F80 opened 0x%02X", b)
	}
	for _, id := range []uint16{0x4201, 0x7A03, 0x0100 | 0x10, 0x0000} { // unnamed byte, register, unnamed bit 0x0110, no bit
		if err := p.GrantEventBit(id); err == nil {
			t.Errorf("GrantEventBit(0x%04X) accepted", id)
		}
	}
	p.Grant(addr.OffLife, 0xFF)
	if p[addr.OffLife] != 0 {
		t.Error("Grant opened a per-player byte")
	}
	w := New(testID, time.Time{})
	w.SetPolicy(p)
	res := w.ApplyGains([]Gain{{uint16(off), 0x21}})
	if !slices.Equal(res.Delta, []Gain{{uint16(off), 0x20}}) || !slices.Equal(res.Rejected, []Reject{{uint16(off), 0x01, ReasonPolicy}}) {
		t.Errorf("granted switch: delta %v rejected %v", res.Delta, res.Rejected)
	}
}

// TestTombstone: a tombstone clears its bits from the image, refuses them
// from then on whatever is proposed, and fails for bytes that never merge.
func TestTombstone(t *testing.T) {
	w := newTestWorld()
	w.ApplyGains([]Gain{{offItem, 0xFF}})
	letter := addr.OffEvent + int(addr.EventByte(0x7A03).Off)
	w.ApplyGains([]Gain{{uint16(letter), 3}})
	seq := w.Seq()

	res, err := w.Tombstone(offItem, 0x81)
	if err != nil || !slices.Equal(res.Cleared, []Gain{{offItem, 0x81}}) || res.Seq != seq+1 {
		t.Fatalf("tombstone: %+v, %v", res, err)
	}
	if img := w.Image(); img[offItem] != 0x7E {
		t.Errorf("image keeps 0x%02X", img[offItem])
	}
	if res, _ := w.Tombstone(offItem, 0x01); res.Seq != seq+1 {
		t.Error("an existing tombstone bumped the seq")
	}
	// A MAX byte: tombstoning bit 1 drops the value 3 to 1, and refuses
	// 2 and 3 from then on.
	if res, err := w.Tombstone(letter, 0x02); err != nil || !slices.Equal(res.Cleared, []Gain{{uint16(letter), 0x02}}) {
		t.Fatalf("letter tombstone: %+v, %v", res, err)
	}
	if res := w.ApplyGains([]Gain{{uint16(letter), 3}, {uint16(letter), 2}}); res.Changed() || len(res.Rejected) != 2 {
		t.Errorf("letter 2 and 3 after the tombstone: %+v", res)
	}
	for _, c := range []struct {
		off  int
		mask byte
	}{{addr.OffLife, 1}, {addr.OffReserve, 1}, {-1, 1}, {ImageSize, 1}, {addr.OffMemBit(0) + addr.MemBitOffDungeonItem, 0xC0},
		{addr.OffGetItem, 0x04}, {addr.OffCollectFlags + 4, 0x02}} {
		if _, err := w.Tombstone(c.off, c.mask); err == nil {
			t.Errorf("Tombstone(0x%X, 0x%02X) accepted", c.off, c.mask)
		}
	}

	// Never return: random proposals of every kind.
	r := rand.New(rand.NewPCG(7, 7))
	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			w.Merge(randImage(r, false))
		} else {
			w.ApplyGains(randGains(r))
		}
		img := w.Image()
		tomb := w.Tombstones()
		for off := range img {
			if img[off]&tomb[off] != 0 {
				t.Fatalf("tombstoned bits 0x%02X of byte 0x%03X came back", img[off]&tomb[off], off)
			}
		}
	}
}

// TestDiffAndApply checks the two replica-side helpers on known bytes.
func TestDiffAndApply(t *testing.T) {
	var a, b save.Image
	a[offItem], b[offItem] = 0x03, 0x06                 // OR: b adds 0x04
	a[addr.OffWalletSize], b[addr.OffWalletSize] = 2, 1 // MAX: b is lower
	a[addr.OffArrowMax], b[addr.OffArrowMax] = 30, 60   // MAX: b is higher
	a[addr.OffLife], b[addr.OffLife] = 0, 0x0C          // never merges
	want := []Gain{{offItem, 0x04}, {addr.OffArrowMax, 60}}
	if got := Diff(&a, &b); !slices.Equal(got, want) {
		t.Errorf("Diff = %v, want %v", got, want)
	}
	// Apply keeps a replica's local bits outside a MAX mask.
	reg := addr.OffEvent + int(addr.EventByte(0x7A03).Off)
	a[reg] = 0xF1
	if n := Apply(&a, []Gain{{uint16(reg), 2}, {uint16(reg), 0x04}, {addr.OffLife, 1}, {ImageSize, 1}}); n != 1 || a[reg] != 0xF2 {
		t.Errorf("Apply: %d changed, register 0x%02X", n, a[reg])
	}
}

// TestBottleHistoryAndCharmWorn: bits 1-7 of mGetItem[0..2] (the bottle
// contents a player has held, onBottleItem, d_save.cpp:224-282) and bit 1
// of mCollect[4] (the Hero's Charm worn, d_menu_collect.cpp:1283-1286) are
// each player's. A file that differs from the world only there adds no
// gain, so neither the hub's bind judgement (Merge) nor the join
// preflight (Diff) finds it divergent; the world never takes those bits;
// bit 0 of each byte still syncs.
func TestBottleHistoryAndCharmWorn(t *testing.T) {
	const charm = addr.OffCollectFlags + 4
	w := newTestWorld()
	w.ApplyGains([]Gain{{addr.OffGetItem, 0x01}, {charm, 0x01}, {offItem, 0x01}}) // telescope, charm owned, grappling hook
	cur := w.Image()
	file := cur
	file[addr.OffGetItem] |= 0x04   // an empty bottle held
	file[addr.OffGetItem+1] |= 0x04 // a fairy in a bottle held
	file[charm] |= 0x02             // the charm worn

	if gs := Diff(&cur, &file); len(gs) != 0 {
		t.Errorf("Diff(world, file) = %v, want no gain", gs)
	}
	if s := Shared(&file); s != cur {
		t.Error("Shared(file) kept a bottle-history or charm-worn bit")
	}
	if res := w.Merge(&file); res.Changed() || len(res.Delta) != 0 {
		t.Errorf("merging the file changed the world: %v", res.Delta)
	}
	res := w.ApplyGains([]Gain{{addr.OffGetItem, 0x04}, {charm, 0x02}})
	wantRej := []Reject{{addr.OffGetItem, 0x04, ReasonMask}, {charm, 0x02, ReasonMask}}
	if res.Changed() || !slices.Equal(res.Rejected, wantRej) {
		t.Errorf("gains of the local bits: delta %v rejected %v, want none and %v", res.Delta, res.Rejected, wantRej)
	}

	// Bit 0 of the same bytes still merges, from a file of a fresh world.
	fresh := newTestWorld()
	if res := fresh.Merge(&file); !slices.Equal(res.Delta, []Gain{{addr.OffGetItem, 0x01}, {offItem, 0x01}, {charm, 0x01}}) {
		t.Errorf("a fresh world's merge of the file: delta %v, want bit 0 of each byte", res.Delta)
	}
}

// TestFreshBinds: pending fresh binds are kept, cleared, capped oldest
// first, and each change bumps the seq.
func TestFreshBinds(t *testing.T) {
	w := New(testID, time.Time{})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < MaxFreshBinds+2; i++ {
		if ok, err := w.AddFreshBind(ID{byte(i + 1)}, base.Add(time.Duration(i)*time.Minute)); !ok || err != nil {
			t.Fatalf("add %d: %v %v", i, ok, err)
		}
	}
	if got := len(w.FreshBinds()); got != MaxFreshBinds {
		t.Fatalf("%d fresh binds, want %d", got, MaxFreshBinds)
	}
	if w.IsFreshBind(ID{1}) || w.IsFreshBind(ID{2}) || !w.IsFreshBind(ID{3}) {
		t.Error("the cap did not drop the two oldest")
	}
	if ok, _ := w.AddFreshBind(ID{3}, base); ok {
		t.Error("re-adding a pending bind counted as new")
	}
	if _, err := w.AddFreshBind(ID{}, base); err == nil {
		t.Error("a zero nonce was accepted")
	}
	seq := w.Seq()
	if !w.ClearFreshBind(ID{3}) || w.ClearFreshBind(ID{3}) || w.Seq() != seq+1 {
		t.Error("ClearFreshBind")
	}
	if seq != MaxFreshBinds+2 {
		t.Errorf("seq %d after %d adds", seq, MaxFreshBinds+2)
	}
}

func TestParseID(t *testing.T) {
	id, err := ParseID(testID.String())
	if err != nil || id != testID {
		t.Fatalf("ParseID(%s) = %s, %v", testID, id, err)
	}
	for _, s := range []string{"", "c00b01020304050", "c00b0102030405060", "zz0b010203040506"} {
		if _, err := ParseID(s); err == nil {
			t.Errorf("ParseID(%q) accepted", s)
		}
	}
}

func TestBitSet(t *testing.T) {
	var s BitSet
	if !s.Add(5, 0x03) || s.Add(5, 0x01) || !s.Has(5, 0x03) || s.Has(5, 0x07) || s.Add(-1, 1) || s.Add(ImageSize, 1) {
		t.Error("Add/Has")
	}
	img := save.Image{9: 0x80}
	s.AddImage(&img)
	if s.Count() != 3 || !slices.Equal(s.Entries(), []Gain{{5, 0x03}, {9, 0x80}}) || s.Mask(9) != 0x80 || s.Mask(-1) != 0 {
		t.Errorf("set %v (%d bits)", s.Entries(), s.Count())
	}
}
