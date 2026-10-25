package class

import (
	"math/bits"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestGoldenCoverage is the golden test of §3: every one of the 0x778
// image bytes has exactly one class, and the bytes per class are pinned.
// Reclassifying a byte means changing a number here, on purpose.
func TestGoldenCoverage(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("class table is broken:\n%v", err)
	}
	got := map[Class]int{}
	for i := 0; i < addr.SaveImageSize; i++ {
		r := Of(i)
		if r.Class == Unassigned {
			t.Fatalf("image byte 0x%03X is unassigned", i)
		}
		got[r.Class]++
	}
	want := map[Class]int{
		// mGetItem 21, beast and bait 2, mCollect 5, songs/shards/pearls 3,
		// charts 0x30, mFmapBits 49, Triforce charts 1, 16 x (tbox 4,
		// item 4, visited 8, dungeonItem 1), ocean 0x64, figurines 17,
		// warp pots 6, the pig pen 1.
		OR: 525,
		// 16 x 16 memory-switch bytes, 66 event-bit bytes.
		ORMasked: 322,
		// wallet, arrow max, bomb max, 12 letters, Beedle points, ghost ship,
		// Orca's and Koboli's levels, Sploosh Kaboom, barrel shooting.
		Max: 21,
		// mMaxLife 2, mSelectEquip 4, mMaxMagic 1, 17 item slots, 16 keyNum.
		Derived: 40,
		// padding, unused map bytes, event bytes 0x42-0x78.
		Never: 140,
		Tag:   addr.ReserveSize,
		// everything else.
		PerPlayer: 784,
	}
	total := 0
	for c, n := range want {
		total += n
		if got[c] != n {
			t.Errorf("%s covers %d bytes, want %d", c, got[c], n)
		}
	}
	if total != addr.SaveImageSize {
		t.Fatalf("golden counts sum to %d, want 0x%X", total, addr.SaveImageSize)
	}
}

// TestValidateCatchesGapsAndOverlaps proves the golden test can fail: a
// table missing a span, claiming a byte twice, or with a mask that
// contradicts its class is reported, and the built table fails closed.
func TestValidateCatchesGapsAndOverlaps(t *testing.T) {
	good := Spans()
	var life int
	for i, s := range good {
		if s.Off == addr.OffLife {
			life = i
		}
	}

	gap := append(append([]Span(nil), good[:life]...), good[life+1:]...)
	if err := validate(gap); err == nil || !strings.Contains(err.Error(), "gap at 0x002-0x003") {
		t.Errorf("missing mLife span: err = %v, want a gap at 0x002-0x003", err)
	}
	if r := buildTable(gap)[addr.OffLife]; r.Class != Unassigned {
		t.Errorf("gap byte built as %s, want UNASSIGNED", r.Class)
	}

	overlap := append(Spans(), Span{Off: addr.OffLife, Len: 1, Rule: Rule{Class: OR, Mask: 0xFF, Field: "bogus"}})
	if err := validate(overlap); err == nil || !strings.Contains(err.Error(), "overlap at 0x002: mLife and bogus") {
		t.Errorf("double-claimed mLife: err = %v, want an overlap at 0x002", err)
	}
	if r := buildTable(overlap)[addr.OffLife]; r.Class != Unassigned {
		t.Errorf("overlapping byte built as %s, want UNASSIGNED", r.Class)
	}

	for _, bad := range []Span{
		{Off: addr.SaveImageSize - 1, Len: 2, Rule: Rule{Class: Never, Field: "past end"}},
		{Off: 0, Len: 0, Rule: Rule{Class: Never, Field: "empty"}},
	} {
		if err := validate([]Span{bad}); err == nil || !strings.Contains(err.Error(), "outside") {
			t.Errorf("%s: err = %v, want outside-the-image", bad.Field, err)
		}
	}
	whole := func(r Rule) []Span { return []Span{{Off: 0, Len: addr.SaveImageSize, Rule: r}} }
	if err := validate(whole(Rule{Class: OR, Field: "no mask"})); err == nil {
		t.Error("OR span without a mask accepted")
	}
	if err := validate(whole(Rule{Class: PerPlayer, Mask: 1, Field: "masked"})); err == nil {
		t.Error("PER_PLAYER span with a mask accepted")
	}
	if err := validate(whole(Rule{Field: "unassigned"})); err == nil {
		t.Error("span without a class accepted")
	}
	if err := validate(whole(Rule{Class: Never, Field: "all"})); err != nil {
		t.Errorf("one span over the whole image: %v", err)
	}
}

// TestSpotClasses checks representative bytes, located through addr.
// mGetItem[0..2] and mCollect[4] share bit 0 only: their other bits are
// the bottle contents held (onBottleItem, d_save.cpp:224-282) and the
// Hero's Charm worn (d_menu_collect.cpp:1283-1286), each player's.
func TestSpotClasses(t *testing.T) {
	type want struct {
		c    Class
		mask byte
	}
	ev := func(id uint16) int { return addr.OffEvent + addr.EventByte(id).Off }
	cases := map[string]struct {
		off int
		want
	}{
		"mMaxLife hi":         {addr.OffMaxLife, want{Derived, 0}},
		"mMaxLife lo":         {addr.OffMaxLife + 1, want{Derived, 0}},
		"mLife":               {addr.OffLife, want{PerPlayer, 0}},
		"mLife lo":            {addr.OffLife + 1, want{PerPlayer, 0}},
		"mRupee":              {addr.OffRupee, want{PerPlayer, 0}},
		"mMagic":              {addr.OffMagic, want{PerPlayer, 0}},
		"mMaxMagic":           {addr.OffMaxMagic, want{Derived, 0}},
		"mWalletSize":         {addr.OffWalletSize, want{Max, 0xFF}},
		"arrow max":           {addr.OffArrowMax, want{Max, 0xFF}},
		"bomb max":            {addr.OffBombMax, want{Max, 0xFF}},
		"mDateIPL":            {addr.OffDateIPL, want{PerPlayer, 0}},
		"bow slot":            {addr.OffItems + addr.SlotBow, want{Derived, 0}},
		"bottle 0":            {addr.OffItems + addr.SlotBottle0, want{PerPlayer, 0}},
		"bottle 3":            {addr.OffItems + addr.SlotBottle0 + 3, want{PerPlayer, 0}},
		"delivery bag slot":   {addr.OffItems + addr.SlotDeliveryBag, want{Derived, 0}},
		"mGetItem bow":        {addr.OffGetItem + addr.SlotBow, want{OR, 0xFF}},
		"mGetItem[0]":         {addr.OffGetItem, want{OR, 0x01}},
		"mGetItem[2]":         {addr.OffGetItem + 2, want{OR, 0x01}},
		"mGetItem[3]":         {addr.OffGetItem + 3, want{OR, 0xFF}},
		"mCollect[3]":         {addr.OffCollectFlags + 3, want{OR, 0xFF}},
		"mCollect[4]":         {addr.OffCollectFlags + 4, want{OR, 0x01}},
		"mCollect[5]":         {addr.OffCollectFlags + 5, want{PerPlayer, 0}},
		"mBeastFlags":         {addr.OffBeastFlags, want{OR, 0xFF}},
		"mReserveFlags":       {addr.OffReserveFlags, want{PerPlayer, 0}},
		"mTact":               {addr.OffTact, want{OR, 0xFF}},
		"mSymbol":             {addr.OffSymbol, want{OR, 0xFF}},
		"Triforce charts":     {addr.OffMapTriforce, want{OR, 0xFF}},
		"map row 0":           {addr.OffMapRow0, want{Never, 0}},
		"mPlayerName":         {addr.OffPlayerName, want{PerPlayer, 0}},
		"mClearCount":         {addr.OffClearCount, want{PerPlayer, 0}},
		"sea tbox":            {addr.OffMemBit(addr.TableSea) + addr.MemBitOffTbox, want{OR, 0xFF}},
		"DRC tbox last byte":  {addr.OffMemBit(addr.TableDRC) + addr.MemBitOffTbox + 3, want{OR, 0xFF}},
		"DRC switch":          {addr.OffMemBit(addr.TableDRC) + addr.MemBitOffSwitch, want{ORMasked, 0xFF}},
		"test switch end":     {addr.OffMemBit(addr.TableTest) + addr.MemBitOffItem - 1, want{ORMasked, 0xFF}},
		"FW item":             {addr.OffMemBit(addr.TableFW) + addr.MemBitOffItem, want{OR, 0xFF}},
		"WT visited":          {addr.OffMemBit(addr.TableWT) + addr.MemBitOffVisited + 7, want{OR, 0xFF}},
		"DRC keyNum":          {addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum, want{Derived, 0}},
		"ET dungeonItem":      {addr.OffMemBit(addr.TableET) + addr.MemBitOffDungeonItem, want{OR, addr.DungeonItemMask}},
		"memBit padding":      {addr.OffMemBit(addr.TableMisc) + addr.MemBitOffPad, want{Never, 0}},
		"ocean first":         {addr.OffOcean, want{OR, 0xFF}},
		"ocean last":          {addr.OffOcean + addr.OceanSize - 1, want{OR, 0xFF}},
		"event 0x00":          {addr.OffEvent, want{ORMasked, 0xBF}},
		"RODE_KORL byte":      {ev(0x2A08), want{ORMasked, 0xFF}},
		"event 0x41":          {addr.OffEvent + 0x41, want{ORMasked, 0x80}},
		"event 0x42":          {addr.OffEvent + 0x42, want{Never, 0}},
		"event 0x60 (AP)":     {addr.OffEvent + 0x60, want{Never, 0}},
		"event 0x78":          {addr.OffEvent + 0x78, want{Never, 0}},
		"register 0x790F":     {ev(0x790F), want{PerPlayer, 0}},
		"figurine 0x80FF":     {ev(0x80FF), want{OR, 0xFF}},
		"figurine 0x95FF":     {ev(0x95FF), want{OR, 0xFF}},
		"figurine 0x9CFF":     {ev(0x9CFF), want{OR, 0xFF}},
		"figurine 0xB1FF":     {ev(0xB1FF), want{OR, 0xFF}},
		"0x85FF, not figure":  {ev(0x85FF), want{PerPlayer, 0}},
		"warp pot 0x9F07":     {ev(0x9F07), want{OR, 0x07}},
		"warp pot 0xA207":     {ev(0xA207), want{OR, 0x07}},
		"warp pot 0xA407":     {ev(0xA407), want{OR, 0x07}},
		"0xA507, not a pot":   {ev(0xA507), want{PerPlayer, 0}},
		"Beedle points":       {ev(0x86FF), want{Max, 0xFF}},
		"GHOST_SHIP":          {ev(0x8803), want{Max, 0x03}},
		"LETTER_ARYLL":        {ev(0x8B03), want{Max, 0x03}},
		"LETTER_KOMALIS":      {ev(0xB503), want{Max, 0x03}},
		"Orca's level":        {ev(0xD007), want{Max, 0x07}},
		"Koboli's level":      {ev(0xC203), want{Max, 0x03}},
		"Sploosh Kaboom":      {ev(0xFE07), want{Max, 0x07}},
		"barrel shooting":     {ev(0xB703), want{Max, 0x03}},
		"pig pen":             {ev(0xBF0F), want{OR, 0x0F}},
		"Joy Pendants 0xC0FF": {ev(0xC0FF), want{PerPlayer, 0}},
		"0x89FF (2nd quest)":  {ev(0x89FF), want{PerPlayer, 0}},
		"unnamed reg 0xB3":    {addr.OffEvent + 0xB3, want{PerPlayer, 0}},
		"register 0xFF07":     {ev(0xFF07), want{PerPlayer, 0}},
		"mReserve first":      {addr.OffReserve, want{Tag, 0}},
		"mReserve tag end":    {addr.OffReserve + addr.ReserveTagBytes - 1, want{Tag, 0}},
		"mReserve last":       {addr.OffReserve + addr.ReserveSize - 1, want{Tag, 0}},
		"image padding":       {addr.OffImagePad, want{Never, 0}},
		"image padding (end)": {addr.SaveImageSize - 1, want{Never, 0}},
	}
	for name, c := range cases {
		r := Of(c.off)
		if r.Class != c.c || r.Mask != c.mask {
			t.Errorf("%s (0x%03X) = %s mask 0x%02X (%s), want %s mask 0x%02X",
				name, c.off, r.Class, r.Mask, r.Field, c.c, c.mask)
		}
	}
	for _, off := range []int{-1, addr.SaveImageSize, addr.SaveImageSize + 0x100} {
		if r := Of(off); r.Class != Unassigned {
			t.Errorf("Of(%d) = %s, want UNASSIGNED", off, r.Class)
		}
	}
}

// TestEventMasks: the named-bit masks hold exactly the enum's 487 bits, and
// no register sneaks into the bit area or vice versa.
func TestEventMasks(t *testing.T) {
	n := 0
	for _, m := range namedEventBits {
		n += bits.OnesCount8(m)
	}
	if n != addr.EventNamedBits {
		t.Errorf("named event masks hold %d bits, want %d", n, addr.EventNamedBits)
	}
	if namedEventBits[addr.EventBitBytes-1] == 0 {
		t.Error("byte 0x41 holds 0x4180, the last named bit")
	}
	for i := 0; i < addr.EventSize; i++ {
		r := Of(addr.OffEvent + i)
		if (r.Class == ORMasked) != (i < addr.EventBitBytes) {
			t.Errorf("event byte 0x%02X is %s", i, r.Class)
		}
	}
}

// TestRegisterAllowlist: every allowlisted register is a register (byte
// 0x79-0xFF) with a field mask, appears once, the figurine set is the
// 17 of l_figure_comp and the warp pot set the 6 of m_event_reg.
func TestRegisterAllowlist(t *testing.T) {
	if len(figurineRegs) != 17 {
		t.Errorf("%d figurine registers, want l_figure_comp's 17", len(figurineRegs))
	}
	if len(warpPotRegs) != 6 {
		t.Errorf("%d warp pot registers, want m_event_reg's 6", len(warpPotRegs))
	}
	seen := map[int]uint16{}
	for _, list := range [][]uint16{figurineRegs, warpPotRegs, orRegs, maxRegs} {
		for _, id := range list {
			b := addr.EventByte(id)
			if b.Off < addr.EventRegFirst || b.Mask == 0 {
				t.Errorf("0x%04X is not a register with a field", id)
			}
			if prev, dup := seen[b.Off]; dup {
				t.Errorf("register byte 0x%02X listed twice (0x%04X, 0x%04X)", b.Off, prev, id)
			}
			seen[b.Off] = id
		}
	}
	for _, id := range figurineRegs {
		if id&0xFF != 0xFF {
			t.Errorf("figurine register 0x%04X is not a whole byte", id)
		}
	}
	// The design's range 0x80-0x95 also covers 0x85-0x8B, which l_figure_comp
	// skips; none of those may be an OR byte.
	for i := 0x85; i <= 0x8B; i++ {
		if Of(addr.OffEvent+i).Class == OR {
			t.Errorf("event byte 0x%02X is OR but is not a figurine register", i)
		}
	}
}

// TestMergingClassesHaveMasks restates the mask invariant over the built
// table, so a consumer can rely on Mask == 0 meaning "not shared".
func TestMergingClassesHaveMasks(t *testing.T) {
	for i := 0; i < addr.SaveImageSize; i++ {
		r := Of(i)
		if r.Class.Merges() != (r.Mask != 0) {
			t.Errorf("byte 0x%03X: %s with mask 0x%02X", i, r.Class, r.Mask)
		}
		if r.Field == "" {
			t.Errorf("byte 0x%03X has no field name", i)
		}
	}
	for _, c := range []Class{Derived, PerPlayer, Never, Tag, Unassigned} {
		if c.Merges() {
			t.Errorf("%s merges", c)
		}
	}
	if Class(99).String() != "Class(99)" {
		t.Errorf("unknown class prints %q", Class(99).String())
	}
}
