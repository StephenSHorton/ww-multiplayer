package class

import (
	"math/bits"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// excludedMask is the OR of every LocalOnly, D8 and refight bit, per event
// byte.
func excludedMask(t *testing.T) [addr.EventBitBytes]byte {
	t.Helper()
	var ex [addr.EventBitBytes]byte
	for _, list := range [][]EventBit{localOnlyBits, gameClearedBits, refightBits} {
		for _, e := range list {
			b := addr.EventByte(e.ID)
			if b.Off >= addr.EventBitBytes {
				t.Fatalf("0x%04X (%s) is outside the event bits", e.ID, e.Name)
			}
			ex[b.Off] |= b.Mask
		}
	}
	return ex
}

// TestSyncMaskIsNamedMinusExcluded: the SyncMask is a subset of the named
// bits (§8.1 "no bits outside 0x00-0x41"), holds no LocalOnly, D8 or
// refight bit, and together with them makes up exactly the named bits.
func TestSyncMaskIsNamedMinusExcluded(t *testing.T) {
	ex := excludedMask(t)
	for i := range addr.EventBitBytes {
		m, named := syncMask[i], namedEventBits[i]
		if m&^named != 0 {
			t.Errorf("event byte 0x%02X: SyncMask 0x%02X has bits 0x%02X that name no event", i, m, m&^named)
		}
		if m&ex[i] != 0 {
			t.Errorf("event byte 0x%02X: SyncMask 0x%02X has excluded bits 0x%02X", i, m, m&ex[i])
		}
		if m|ex[i] != named {
			t.Errorf("event byte 0x%02X: SyncMask 0x%02X | excluded 0x%02X != named 0x%02X", i, m, ex[i], named)
		}
		// The class table's mask stays the most the sync may carry.
		if r := Of(addr.OffEvent + i); r.Class != ORMasked || r.Mask != named {
			t.Errorf("event byte 0x%02X is %s mask 0x%02X, want OR_MASKED 0x%02X", i, r.Class, r.Mask, named)
		}
	}
	for _, list := range [][]EventBit{localOnlyBits, gameClearedBits, refightBits} {
		for _, e := range list {
			if EventSyncs(e.ID) {
				t.Errorf("excluded bit 0x%04X (%s) syncs", e.ID, e.Name)
			}
			b := addr.EventByte(e.ID)
			if bits.OnesCount8(b.Mask) != 1 || namedEventBits[b.Off]&b.Mask == 0 {
				t.Errorf("0x%04X (%s) is not one named bit", e.ID, e.Name)
			}
			if e.Name == "" || e.Cite == "" {
				t.Errorf("0x%04X has no name or citation", e.ID)
			}
		}
	}
}

// TestStoryListsGolden pins the generated lists' sizes and the bits the
// doc names, so a regeneration that changes them does so on purpose.
func TestStoryListsGolden(t *testing.T) {
	n := 0
	for _, m := range syncMask {
		n += bits.OnesCount8(m)
	}
	// 487 named bits minus 34 LocalOnly (the 31 D8 among them) minus the
	// 4 refight bits.
	if n != 449 {
		t.Errorf("SyncMask holds %d bits, want 449", n)
	}
	gz := 0
	files := map[string]bool{}
	for _, s := range offEventBitSites {
		if s.GZLE01 {
			gz++
		}
		files[fileOf(s.At)] = true
	}
	if len(offEventBitSites) != 50 || len(files) != 10 || gz != 46 {
		t.Errorf("%d offEventBit calls in %d files, %d in GZLE01; want 50 in 10 (§6.5), 46 in GZLE01",
			len(offEventBitSites), len(files), gz)
	}
	if len(gameClearedBits) != 31 || len(localOnlyBits) != 34 || len(refightBits) != 4 {
		t.Errorf("D8 %d, LocalOnly %d, refight %d bits; want 31, 34, 4",
			len(gameClearedBits), len(localOnlyBits), len(refightBits))
	}

	in := func(list []EventBit, id uint16) bool {
		for _, e := range list {
			if e.ID == id {
				return true
			}
		}
		return false
	}
	for _, id := range []uint16{0x0A02, 0x2A08, 0x3820, 0x3B20, 0x2D01, 0x1301, 0x3C02, 0x4080} {
		if !in(localOnlyBits, id) {
			t.Errorf("0x%04X is not LocalOnly", id)
		}
	}
	for _, id := range []uint16{0x3D80, 0x3D40, 0x3D20, 0x3D10} {
		if !in(refightBits, id) {
			t.Errorf("0x%04X is not a refight bit", id)
		}
	}
	// Doc vs decomp: only the demo build clears these, so GZLE01 syncs them.
	for _, id := range []uint16{0x0E08, 0x1110, 0x1108} {
		if in(gameClearedBits, id) || !EventSyncs(id) {
			t.Errorf("0x%04X: game-cleared %v, syncs %v; want a synced bit", id, in(gameClearedBits, id), EventSyncs(id))
		}
	}
	// Story milestones the resume stage and the holds read sync.
	for _, id := range []uint16{0x0F80, 0x0801, 0x0808, 0x2401, 0x2D02, 0x2D10, 0x3804, 0x3E10, 0x3F80} {
		if !EventSyncs(id) {
			t.Errorf("0x%04X does not sync", id)
		}
	}
}

func fileOf(at string) string {
	for i := len(at) - 1; i >= 0; i-- {
		if at[i] == ':' {
			return at[:i]
		}
	}
	return at
}

// TestGameClearedMatchesSites: D8 is exactly the bits of the calls GZLE01
// compiles, and every D8 bit is LocalOnly.
func TestGameClearedMatchesSites(t *testing.T) {
	sites := map[uint16]bool{}
	for _, s := range offEventBitSites {
		if s.GZLE01 {
			sites[s.ID] = true
		}
	}
	d8 := map[uint16]bool{}
	for _, e := range gameClearedBits {
		d8[e.ID] = true
		if !sites[e.ID] {
			t.Errorf("D8 bit 0x%04X has no GZLE01 call", e.ID)
		}
	}
	for id := range sites {
		if !d8[id] {
			t.Errorf("0x%04X is cleared in GZLE01 but not D8", id)
		}
	}
	local := map[uint16]bool{}
	for i, e := range localOnlyBits {
		if local[e.ID] {
			t.Errorf("LocalOnly lists 0x%04X twice", e.ID)
		}
		local[e.ID] = true
		if i > 0 && localOnlyBits[i-1].ID >= e.ID {
			t.Errorf("LocalOnly is not in id order at 0x%04X", e.ID)
		}
	}
	for id := range d8 {
		if !local[id] {
			t.Errorf("D8 bit 0x%04X is not LocalOnly", id)
		}
	}
}

// TestEventSyncs covers the edges of the lookup.
func TestEventSyncs(t *testing.T) {
	cases := []struct {
		id   uint16
		want bool
	}{
		{0x0F80, true},  // MET_KORL
		{0x0A02, false}, // ENDLESS_NIGHT, LocalOnly
		{0x3D80, false}, // refight
		{0x0A00, false}, // no mask
		{0x0A01, true},  // the byte's other bits still sync
		{0x0A03, false}, // a mask with a LocalOnly bit in it
		{0x0040, false}, // not a named bit (byte 0x00 is 0xBF)
		{0x4180, true},  // the last named bit
		{0x4280, false}, // byte 0x42, unnamed
		{0x80FF, false}, // a register
	}
	for _, c := range cases {
		if got := EventSyncs(c.id); got != c.want {
			t.Errorf("EventSyncs(0x%04X) = %v, want %v", c.id, got, c.want)
		}
	}
	for _, i := range []int{-1, addr.EventBitBytes, 0x79, 0xFF, 0x100} {
		if SyncMaskOf(i) != 0 {
			t.Errorf("SyncMaskOf(0x%X) = 0x%02X, want 0", i, SyncMaskOf(i))
		}
	}
	if m := SyncMask(); m != syncMask {
		t.Error("SyncMask() differs from the table")
	}
}

// TestHolds: every hold is on a synced bit (a hold on a bit that never
// syncs would hide a list mistake), waits for a named bit or the Master
// Sword, and is listed once.
func TestHolds(t *testing.T) {
	seen := map[uint16]bool{}
	for _, h := range holds {
		if seen[h.ID] {
			t.Errorf("hold on 0x%04X listed twice", h.ID)
		}
		seen[h.ID] = true
		if !EventSyncs(h.ID) {
			t.Errorf("hold on 0x%04X (%s): the bit does not sync", h.ID, h.Name)
		}
		if h.Why == "" || h.Cite == "" {
			t.Errorf("hold on 0x%04X has no reason or citation", h.ID)
		}
		switch h.Until {
		case UntilLocal, UntilWith:
			if !EventSyncs(h.Arg) && h.Arg != h.ID {
				t.Errorf("hold on 0x%04X waits for 0x%04X, which does not sync", h.ID, h.Arg)
			}
		case UntilMasterSword:
			if h.Arg != 0 {
				t.Errorf("hold on 0x%04X waits for a sword but names bit 0x%04X", h.ID, h.Arg)
			}
		default:
			t.Errorf("hold on 0x%04X has condition %s", h.ID, h.Until)
		}
	}
	want := map[uint16]HoldUntil{
		0x0F80: UntilLocal, 0x0801: UntilLocal, 0x0808: UntilLocal, 0x2401: UntilLocal,
		0x2D10: UntilMasterSword, 0x3804: UntilWith, 0x3E10: UntilWith,
	}
	if len(holds) != len(want) {
		t.Errorf("%d holds, want %d", len(holds), len(want))
	}
	for _, h := range holds {
		if want[h.ID] != h.Until {
			t.Errorf("hold on 0x%04X is %s, want %s", h.ID, h.Until, want[h.ID])
		}
		if h.Until == UntilLocal && h.Arg != evMetKorl {
			t.Errorf("resume-stage hold on 0x%04X waits for 0x%04X, want MET_KORL", h.ID, h.Arg)
		}
	}
	if got := MasterSwords(); len(got) != 3 || got[0] != 0x39 || got[1] != 0x3A || got[2] != 0x3E {
		t.Errorf("MasterSwords() = % X", got)
	}
	if UntilLocal.String() != "until-local" || HoldUntil(9).String() != "HoldUntil(?)" {
		t.Error("HoldUntil.String")
	}
}

// TestRegisterAllowlistUntouched pins the register bytes' classes: the
// story sync adds nothing to them, so only l_figure_comp's 17 bitfields
// merge by OR and the 14 registers of maxRegs by MAX within their field.
func TestRegisterAllowlistUntouched(t *testing.T) {
	type rule struct {
		c    Class
		mask byte
	}
	want := map[int]rule{}
	for _, b := range []int{0x80, 0x81, 0x82, 0x83, 0x84, 0x8C, 0x8D, 0x8E, 0x8F, 0x90, 0x91, 0x92,
		0x93, 0x94, 0x95, 0x9C, 0xB1} {
		want[b] = rule{OR, 0xFF}
	}
	for _, b := range []int{0x7A, 0x7B, 0x7C, 0x7D, 0x8B, 0x9D, 0xAC, 0xAE, 0xAF, 0xB0, 0xB2, 0xB5, 0x88} {
		want[b] = rule{Max, 0x03}
	}
	want[0x86] = rule{Max, 0xFF}
	for i := addr.EventRegFirst; i < addr.EventSize; i++ {
		r := Of(addr.OffEvent + i)
		w, ok := want[i]
		if !ok {
			w = rule{PerPlayer, 0}
		}
		if r.Class != w.c || r.Mask != w.mask {
			t.Errorf("register byte 0x%02X is %s mask 0x%02X, want %s mask 0x%02X", i, r.Class, r.Mask, w.c, w.mask)
		}
	}
}

// TestAccessorsCopy: the exported lists are copies.
func TestAccessorsCopy(t *testing.T) {
	l, g, r, s, h, m := LocalOnly(), GameCleared(), Refight(), OffEventBitSites(), Holds(), MasterSwords()
	l[0].ID, g[0].ID, r[0].ID, s[0].ID, h[0].ID, m[0] = 0, 0, 0, 0, 0, 0
	if localOnlyBits[0].ID == 0 || gameClearedBits[0].ID == 0 || refightBits[0].ID == 0 ||
		offEventBitSites[0].ID == 0 || holds[0].ID == 0 || masterSwords[0] == 0 {
		t.Error("an accessor returned the table itself")
	}
}
