package inventory

import (
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// fresh returns a new file's image as far as the inventory goes: every
// slot, button and equipment byte empty (dSv_player_item_c::init and
// dSv_player_status_a_c::init, d_save.cpp:25-46, :88-92).
func fresh() world.Image {
	var img world.Image
	for i := range addr.SlotCount {
		img[addr.OffItems+i] = ItemNone
	}
	for i := range 5 {
		img[addr.OffSelectItem+i] = ItemNone
	}
	for i := range 4 {
		img[addr.OffSelectEquip+i] = ItemNone
	}
	return img
}

// with returns img with each (off, val) pair set.
func with(img world.Image, kv ...int) world.Image {
	for i := 0; i+1 < len(kv); i += 2 {
		img[kv[i]] = byte(kv[i+1])
	}
	return img
}

// noButtons is a context whose HUD shows nothing on any button.
func noButtons() Context {
	return Context{StageName: "sea", StageTypeOK: true, StageType: 7, SelectItem: [Buttons]byte{ItemNone, ItemNone, ItemNone}}
}

var (
	offBowFlags  = addr.OffGetItem + SlotBow
	offBombFlags = addr.OffGetItem + SlotBomb
	offBowSlot   = addr.OffItems + SlotBow
	offBombSlot  = addr.OffItems + SlotBomb
	offSword     = addr.OffSelectEquip + EquipSword
	offShield    = addr.OffSelectEquip + EquipShield
	offSwordBits = addr.OffCollectFlags + EquipSword
	offEv0801    = addr.OffEvent + addr.EventByte(0x0801).Off
)

// short renders a write as kind@offset=value for comparisons.
func short(w Write) string {
	var b strings.Builder
	b.WriteString(w.Kind.String())
	b.WriteByte('@')
	if w.Off >= 0 {
		b.WriteString(hex(uint32(w.Off), 3))
	} else {
		b.WriteString(hex(w.Addr, 8))
	}
	b.WriteByte('=')
	b.WriteString(hex(uint32(w.Val), 2))
	return b.String()
}

func hex(v uint32, n int) string {
	const digits = "0123456789ABCDEF"
	s := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		s[i] = digits[v&0xF]
		v >>= 4
	}
	return string(s)
}

func shorts(ws []Write) []string {
	var s []string
	for _, w := range ws {
		s = append(s, short(w))
	}
	return s
}

func heldShorts(hs []Held) []string {
	var s []string
	for _, h := range hs {
		s = append(s, short(h.Write))
	}
	return s
}

// TestTables checks the rule tables against the class table and the
// address table: every slot a plan derives is DERIVED and every flag it
// follows is OR, no bottle is derived, every flag byte's mask is inside
// its class mask, and every capacity is MAX.
func TestTables(t *testing.T) {
	seen := map[int]bool{}
	for _, s := range ItemSlots() {
		if s.Off != addr.OffItems+s.Index || s.FlagOff != addr.OffGetItem+s.Index {
			t.Errorf("%s: Off 0x%03X FlagOff 0x%03X for slot %d", s.Name, s.Off, s.FlagOff, s.Index)
		}
		if s.Index >= addr.SlotBottle0 && s.Index < addr.SlotBottle0+addr.SlotBottleCount {
			t.Errorf("%s: bottle slot %d is each player's own", s.Name, s.Index)
		}
		if seen[s.Index] {
			t.Errorf("slot %d listed twice", s.Index)
		}
		seen[s.Index] = true
		if c := class.Of(s.Off).Class; c != class.Derived {
			t.Errorf("%s slot: class %v, want DERIVED", s.Name, c)
		}
		if c := class.Of(s.FlagOff).Class; c != class.OR {
			t.Errorf("%s flags: class %v, want OR", s.Name, c)
		}
		if len(s.Variants) == 0 || s.Cite == "" {
			t.Errorf("%s: no variants or no cite", s.Name)
		}
	}
	if want := addr.SlotCount - addr.SlotBottleCount; len(seen) != want {
		t.Errorf("%d slots derived, want %d", len(seen), want)
	}
	for i, e := range EquipSlots() {
		if e.Index != i || e.Off != addr.OffSelectEquip+i || e.FlagOff != addr.OffCollectFlags+i {
			t.Errorf("%s: index %d Off 0x%03X FlagOff 0x%03X", e.Name, e.Index, e.Off, e.FlagOff)
		}
		if c := class.Of(e.Off).Class; c != class.Derived {
			t.Errorf("%s: class %v, want DERIVED", e.Name, c)
		}
	}
	// The class table shares exactly the flag bits the inventory takes,
	// so the world, the bind judgement and save-merge carry no others.
	for _, f := range flagBytes {
		r := class.Of(f.off)
		if !r.Class.Merges() || r.Class == class.Max || f.mask != r.Mask {
			t.Errorf("%s: class %v mask 0x%02X, flag mask 0x%02X", f.name, r.Class, r.Mask, f.mask)
		}
	}
	if !slices.IsSortedFunc(flagBytes, func(a, b flagByte) int { return a.off - b.off }) {
		t.Error("flag bytes are not in image order")
	}
	for i := range addr.GetItemSize {
		want := byte(0xFF)
		if i < bottleFlagBytes {
			want = 0x01
		}
		if got := FlagMask(addr.OffGetItem + i); got != want {
			t.Errorf("mGetItem[%d] mask 0x%02X, want 0x%02X", i, got, want)
		}
	}
	if got := FlagMask(addr.OffCollectFlags + 4); got != heroCharmOwned {
		t.Errorf("mCollect[4] mask 0x%02X, want only the charm owned bit", got)
	}
	for _, c := range capacities {
		if r := class.Of(c.off); r.Class != class.Max {
			t.Errorf("%s: class %v, want MAX", c.name, r.Class)
		}
	}
	for _, off := range []int{OffArrowNum, OffBombNum, addr.OffSelectItem, addr.OffItems + addr.SlotBottle0} {
		if class.Of(off).Class != class.PerPlayer || Owns(off) {
			t.Errorf("image byte 0x%03X: class %v, owned %v; want PER_PLAYER, not owned", off, class.Of(off).Class, Owns(off))
		}
	}
}

// TestAddresses checks the play addresses this package adds against the
// play struct's base and the snapshot's player block.
func TestAddresses(t *testing.T) {
	for _, c := range []struct {
		name string
		a    uint32
		off  uint32
	}{
		{"mItemArrowNumCount", ItemArrowNumCount, PlayOffItemArrowNumCount},
		{"mItemBombNumCount", ItemBombNumCount, PlayOffItemBombNumCount},
	} {
		if c.a != addr.Play+c.off {
			t.Errorf("%s: 0x%08X, want Play+0x%X = 0x%08X", c.name, c.a, c.off, addr.Play+c.off)
		}
		if !snap.PlayerRegion.Contains(c.a, 2) {
			t.Errorf("%s: outside the snapshot's player block", c.name)
		}
	}
	if !snap.PlayerRegion.Contains(addr.PlaySelectItem, Buttons) {
		t.Error("play.mSelectItem outside the snapshot's player block")
	}
	if OffArrowNum != addr.OffItemRecord+3 || OffBombNum != addr.OffItemRecord+4 {
		t.Error("ammo offsets moved")
	}
}

// TestDeriveVariants: every multi-variant slot holds the best variant its
// flags give, fills only nothing or a lesser variant, never moves down,
// and keeps an item none of its variants names.
func TestDeriveVariants(t *testing.T) {
	slot := func(name string) Slot {
		for _, s := range append(ItemSlots(), EquipSlots()...) {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("no slot %q", name)
		return Slot{}
	}
	cases := []struct {
		slot  string
		cur   byte
		flags byte
		want  byte
	}{
		// The bow slot (d_s_play.cpp:1385-1392: light, then fire-ice, then bow).
		{"bow", ItemNone, 0x00, ItemNone},
		{"bow", ItemNone, 0x01, ItemBow},
		{"bow", ItemNone, 0x03, ItemMagicArrow},
		{"bow", ItemNone, 0x07, ItemLightArrow},
		{"bow", ItemNone, 0x04, ItemLightArrow},
		{"bow", ItemNone, 0x02, ItemMagicArrow},
		{"bow", ItemBow, 0x03, ItemMagicArrow},
		{"bow", ItemBow, 0x05, ItemLightArrow},
		{"bow", ItemMagicArrow, 0x07, ItemLightArrow},
		{"bow", ItemMagicArrow, 0x01, ItemMagicArrow}, // never down
		{"bow", ItemLightArrow, 0x03, ItemLightArrow},
		{"bow", ItemLightArrow, 0x00, ItemLightArrow}, // no flags: nothing taken
		{"bow", ItemBow, 0xF8, ItemBow},               // unnamed bits ignored
		{"bow", 0x50, 0x07, 0x50},                     // an item the slot can't name stays
		// The picto box slot (item_func_camera, camera2).
		{"picto box", ItemNone, 0x01, ItemPictoBox},
		{"picto box", ItemNone, 0x02, ItemDeluxePictoBox},
		{"picto box", ItemPictoBox, 0x03, ItemDeluxePictoBox},
		{"picto box", ItemDeluxePictoBox, 0x01, ItemDeluxePictoBox},
		// The sword: Hero's, Master (powerless, half, full).
		{"sword", ItemNone, 0x01, ItemSword},
		{"sword", ItemNone, 0x02, ItemMasterSword1},
		{"sword", ItemSword, 0x03, ItemMasterSword1},
		{"sword", ItemSword, 0x07, ItemMasterSword2},
		{"sword", ItemMasterSword1, 0x0F, ItemMasterSword3},
		{"sword", ItemMasterSword2, 0x08, ItemMasterSword3},
		{"sword", ItemMasterSword3, 0x01, ItemMasterSword3},
		{"sword", ItemMasterSword2, 0x03, ItemMasterSword2},
		// The shield: Hero's, Mirror.
		{"shield", ItemNone, 0x01, ItemShield},
		{"shield", ItemNone, 0x03, ItemMirrorShield},
		{"shield", ItemShield, 0x02, ItemMirrorShield},
		{"shield", ItemMirrorShield, 0x01, ItemMirrorShield},
		// Single-variant slots, and the bottle-content bits of mGetItem[0..2].
		{"telescope", ItemNone, 0x01, ItemTelescope},
		{"telescope", ItemNone, 0xFE, ItemNone},
		{"sail", ItemNone, 0xFE, ItemNone},
		{"Wind Waker", ItemNone, 0x03, ItemWindWaker},
		{"bombs", ItemNone, 0x01, ItemBombBag},
		{"bracelets", ItemNone, 0x01, ItemPowerBracelets},
	}
	for _, c := range cases {
		if got := slot(c.slot).Derive(c.cur, c.flags); got != c.want {
			t.Errorf("%s: Derive(0x%02X, flags 0x%02X) = 0x%02X, want 0x%02X", c.slot, c.cur, c.flags, got, c.want)
		}
	}
	// Every variant of every slot, from every start.
	for _, s := range append(ItemSlots(), EquipSlots()...) {
		for i, v := range s.Variants {
			if got := s.Derive(ItemNone, 1<<v.Bit); got != v.Item {
				t.Errorf("%s: bit %d alone gives 0x%02X, want 0x%02X", s.Name, v.Bit, got, v.Item)
			}
			for j, from := range s.Variants {
				want := s.Variants[max(i, j)].Item
				if got := s.Derive(from.Item, 1<<v.Bit); got != want {
					t.Errorf("%s: 0x%02X with bit %d gives 0x%02X, want 0x%02X", s.Name, from.Item, v.Bit, got, want)
				}
			}
		}
	}
}

// TestGrants: the planned writes for a receiver taking the world's items,
// in order: flags, capacities, slots, equipment, buttons; then the first
// grants' ammo.
func TestGrants(t *testing.T) {
	cases := []struct {
		name      string
		raw, view world.Image
		ctx       Context
		writes    []string
		ammo      []string
	}{
		{
			name:   "bow from nothing: flag, capacity 30, slot, 30 arrows",
			raw:    fresh(),
			view:   with(fresh(), offBowFlags, 0x01, addr.OffArrowMax, 30),
			ctx:    noButtons(),
			writes: []string{"flag@05D=01", "capacity@06F=1E", "slot@048=27"},
			ammo:   []string{"ammo@803CA789=1E"},
		},
		{
			name:   "bow and quiver upgrades: one capacity write to 99, fill only 30",
			raw:    fresh(),
			view:   with(fresh(), offBowFlags, 0x07, addr.OffArrowMax, 99),
			ctx:    noButtons(),
			writes: []string{"flag@05D=07", "capacity@06F=63", "slot@048=36"},
			ammo:   []string{"ammo@803CA789=1E"},
		},
		{
			name:   "bow grant over a larger capacity: the base never lowers it",
			raw:    with(fresh(), addr.OffArrowMax, 60),
			view:   with(fresh(), offBowFlags, 0x01, addr.OffArrowMax, 30),
			ctx:    noButtons(),
			writes: []string{"flag@05D=01", "slot@048=27"},
			ammo:   []string{"ammo@803CA789=1E"},
		},
		{
			name:   "bow grant with arrows in hand: fill tops up to 30",
			raw:    with(fresh(), OffArrowNum, 12),
			view:   with(fresh(), offBowFlags, 0x01, addr.OffArrowMax, 30),
			ctx:    noButtons(),
			writes: []string{"flag@05D=01", "capacity@06F=1E", "slot@048=27"},
			ammo:   []string{"ammo@803CA789=12"},
		},
		{
			name:   "bow grant with 30 arrows or more: no fill",
			raw:    with(fresh(), OffArrowNum, 40, addr.OffArrowMax, 99),
			view:   with(fresh(), offBowFlags, 0x01),
			ctx:    noButtons(),
			writes: []string{"flag@05D=01", "slot@048=27"},
		},
		{
			name:   "world bow without capacity: the grant's 30 floor",
			raw:    fresh(),
			view:   with(fresh(), offBowFlags, 0x01),
			ctx:    noButtons(),
			writes: []string{"flag@05D=01", "capacity@06F=1E", "slot@048=27"},
			ammo:   []string{"ammo@803CA789=1E"},
		},
		{
			name: "fire and ice arrows over the bow: flag, slot, button refresh, no fill",
			raw:  with(fresh(), offBowFlags, 0x01, offBowSlot, ItemBow, addr.OffArrowMax, 30, addr.OffSelectItem+1, SlotBow),
			view: with(fresh(), offBowFlags, 0x03, addr.OffArrowMax, 30),
			ctx: func() Context {
				c := noButtons()
				c.SelectItem[1] = ItemBow
				return c
			}(),
			writes: []string{"flag@05D=03", "slot@048=35", "select-item@803CA7DC=35"},
		},
		{
			name:   "quiver upgrade only: capacity, no ammo",
			raw:    with(fresh(), offBowFlags, 0x01, offBowSlot, ItemBow, addr.OffArrowMax, 30),
			view:   with(fresh(), offBowFlags, 0x01, addr.OffArrowMax, 60),
			ctx:    noButtons(),
			writes: []string{"capacity@06F=3C"},
		},
		{
			name:   "bombs from nothing",
			raw:    fresh(),
			view:   with(fresh(), offBombFlags, 0x01, addr.OffBombMax, 60),
			ctx:    noButtons(),
			writes: []string{"flag@05E=01", "capacity@070=3C", "slot@049=31"},
			ammo:   []string{"ammo@803CA78D=1E"},
		},
		{
			name:   "capacities above the game's largest are capped, never lowered",
			raw:    with(fresh(), addr.OffBombMax, 120),
			view:   with(fresh(), addr.OffWalletSize, 7, addr.OffArrowMax, 0xFF, addr.OffBombMax, 0xFF),
			ctx:    noButtons(),
			writes: []string{"capacity@012=02", "capacity@06F=63"},
		},
		{
			name:   "wallet",
			raw:    with(fresh(), addr.OffWalletSize, 1),
			view:   with(fresh(), addr.OffWalletSize, 2),
			ctx:    noButtons(),
			writes: []string{"capacity@012=02"},
		},
		{
			name:   "Master Sword: collect bit now, equipment at the stage entry",
			raw:    with(fresh(), offSwordBits, 0x01, offSword, ItemSword),
			view:   with(fresh(), offSwordBits, 0x03),
			ctx:    noButtons(),
			writes: []string{"flag@0B4=03", "equip@00E=39"},
		},
		{
			name: "songs, shards, pearls, spoils and bait, charm owned but not worn",
			raw:  with(fresh(), addr.OffTact, 0x01, addr.OffCollectFlags+4, 0x02),
			view: with(fresh(), addr.OffTact, 0x03, addr.OffTriforce, 0x80, addr.OffSymbol, 0x05,
				addr.OffBeastFlags, 0x11, addr.OffBaitFlags, 0x01, addr.OffCollectFlags+4, 0x03, addr.OffCollectFlags+3, 0x01),
			ctx:    noButtons(),
			writes: []string{"flag@094=11", "flag@095=01", "flag@0B7=01", "flag@0B8=03", "flag@0BD=03", "flag@0BE=80", "flag@0BF=05"},
		},
		{
			name:   "bottle-content flags stay each player's: only bit 0 of mGetItem[0..2]",
			raw:    fresh(),
			view:   with(fresh(), addr.OffGetItem, 0xFF, addr.OffGetItem+1, 0xFE, addr.OffGetItem+2, 0x1E, addr.OffGetItem+3, 0x01),
			ctx:    noButtons(),
			writes: []string{"flag@051=01", "flag@054=01", "slot@03C=20", "slot@03F=25"},
		},
		{
			name:   "worn charm bit alone is not taken",
			raw:    fresh(),
			view:   with(fresh(), addr.OffCollectFlags+4, 0x02),
			ctx:    noButtons(),
			writes: nil,
		},
		{
			name:   "bottles, bags, ammo, max life and the world's slots are ignored",
			raw:    fresh(),
			view:   with(fresh(), addr.OffItems+addr.SlotBottle0, 0x50, offBowSlot, ItemLightArrow, OffArrowNum, 99, addr.OffSelectItem, 3, offSword, ItemMasterSword3),
			ctx:    noButtons(),
			writes: nil,
		},
		{
			name:   "the game's own slot fill: raw flags without the slot",
			raw:    with(fresh(), addr.OffGetItem+SlotHookshot, 0x01),
			view:   fresh(),
			ctx:    noButtons(),
			writes: []string{"slot@04F=2F"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Pending(&c.view, &c.raw, c.ctx)
			if got := shorts(p.Writes); !slices.Equal(got, c.writes) {
				t.Errorf("writes %v, want %v", got, c.writes)
			}
			if got := shorts(p.Ammo); !slices.Equal(got, c.ammo) {
				t.Errorf("ammo %v, want %v", got, c.ammo)
			}
			if len(p.Held) != 0 {
				t.Errorf("held %v, want none", heldShorts(p.Held))
			}
			for _, w := range append(p.Writes, p.Ammo...) {
				if w.Reason == "" {
					t.Errorf("%s has no reason", short(w))
				}
			}
			checkIdempotent(t, &c.view, &c.raw, c.ctx, p)
		})
	}
}

// checkIdempotent applies p and checks the plan on the result writes
// nothing and holds what p held.
func checkIdempotent(t *testing.T, view, raw *world.Image, ctx Context, p Plan) {
	t.Helper()
	img, ctx2 := Apply(raw, ctx, append(slices.Clone(p.Writes), p.Ammo...))
	q := Pending(view, &img, ctx2)
	if len(q.Writes) != 0 || len(q.Ammo) != 0 {
		t.Errorf("plan after applying is not empty: writes %v ammo %v", shorts(q.Writes), shorts(q.Ammo))
	}
	if a, b := heldShorts(p.Held), heldShorts(q.Held); !slices.Equal(a, b) {
		t.Errorf("held %v after applying, was %v", b, a)
	}
}

// TestCapacityNeverLowered: for every capacity, receiver value, world
// value and first grant, the planned value is at least the receiver's,
// at most the largest of it and the game's largest, and every capacity
// write raises.
func TestCapacityNeverLowered(t *testing.T) {
	vals := []byte{0, 1, 2, 3, 29, 30, 31, 60, 99, 100, 0xFF}
	for _, c := range capacities {
		for _, r := range vals {
			for _, v := range vals {
				for _, bow := range []bool{false, true} {
					raw := with(fresh(), c.off, int(r))
					view := with(fresh(), c.off, int(v))
					if bow {
						view[offBowFlags], view[offBombFlags] = 0x01, 0x01
					}
					got, _ := Apply(&raw, noButtons(), Pending(&view, &raw, noButtons()).Writes)
					hi := max(r, min(v, c.most))
					if bow && c.off != addr.OffWalletSize {
						hi = max(hi, 30)
					}
					if got[c.off] != hi {
						t.Errorf("%s: receiver %d world %d (first grant %v): %d, want %d", c.name, r, v, bow, got[c.off], hi)
					}
				}
			}
		}
	}
}

// TestContextRules: the FF1 sword and GTower bow rules hold their writes
// while they hold, and the same writes come back once they don't.
func TestContextRules(t *testing.T) {
	inStage := func(name string, typ int, ok bool) Context {
		c := noButtons()
		c.StageName, c.StageType, c.StageTypeOK = name, typ, ok
		return c
	}
	swordView := with(fresh(), offSwordBits, 0x03, offShield, ItemShield, addr.OffCollectFlags+1, 0x01)
	ff1Raw := with(fresh(), offEv0801, 0x01)
	bowView := with(fresh(), offBowFlags, 0x07, addr.OffArrowMax, 60)
	cases := []struct {
		name       string
		raw, view  world.Image
		ctx, after Context
		writes     []string
		held       []string
		rule       Rule
	}{
		{
			name: "FF1 with 0x0801: no sword, the shield still comes",
			raw:  ff1Raw, view: swordView,
			ctx:    inStage("MajyuE", stageTypeFF1, true),
			after:  inStage("sea", 7, true),
			writes: []string{"flag@0B5=01", "equip@00F=3B"},
			held:   []string{"flag@0B4=03"},
			rule:   RuleFF1Sword,
		},
		{
			name: "FF1 with 0x0801 and the Master Sword bits already: equip held, not derived from them",
			raw:  with(ff1Raw, offSwordBits, 0x02), view: fresh(),
			ctx:   inStage("MajyuE", stageTypeFF1, true),
			after: inStage("sea", 7, true),
			held:  []string{"equip@00E=39"},
			rule:  RuleFF1Sword,
		},
		{
			name: "FF1 with 0x0801, STAG unreadable: held",
			raw:  ff1Raw, view: swordView,
			ctx:    inStage("MajyuE", 0, false),
			after:  inStage("MajyuE", 1, true),
			writes: []string{"flag@0B5=01", "equip@00F=3B"},
			held:   []string{"flag@0B4=03"},
			rule:   RuleFF1Sword,
		},
		{
			name: "GTower: no bow, no bow slot; the world's arrow capacity still comes",
			raw:  fresh(), view: bowView,
			ctx:    inStage("GTower", 1, true),
			after:  inStage("GanonK", 1, true),
			writes: []string{"capacity@06F=3C"},
			held:   []string{"flag@05D=07"},
			rule:   RuleGTowerBow,
		},
		{
			name: "GTower took the bow away: the slot is not refilled from the receiver's flags",
			raw:  with(fresh(), offBowFlags, 0x03, addr.OffArrowMax, 30), view: bowView,
			ctx:    inStage("GTower", 1, true),
			after:  inStage("sea", 7, true),
			writes: []string{"capacity@06F=3C"},
			held:   []string{"flag@05D=07", "slot@048=35"},
			rule:   RuleGTowerBow,
		},
		{
			name: "GTower: a bow button is not refreshed",
			raw:  with(fresh(), offBowFlags, 0x07, offBowSlot, ItemLightArrow, addr.OffArrowMax, 60, addr.OffSelectItem, SlotBow), view: bowView,
			ctx: func() Context {
				c := inStage("GTower", 1, true)
				c.SelectItem[0] = ItemMagicArrow
				return c
			}(),
			after: func() Context {
				c := inStage("sea", 7, true)
				c.SelectItem[0] = ItemMagicArrow
				return c
			}(),
			held: []string{"select-item@803CA7DB=36"},
			rule: RuleGTowerBow,
		},
		{
			name: "first bow grant with arrows pending: held until the HUD takes them",
			raw:  fresh(), view: bowView,
			ctx: func() Context {
				c := noButtons()
				c.ArrowCount = -1
				return c
			}(),
			after:  noButtons(),
			writes: []string{"capacity@06F=3C"},
			held:   []string{"flag@05D=07"},
			rule:   RuleAmmoBusy,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Pending(&c.view, &c.raw, c.ctx)
			if got := shorts(p.Writes); !slices.Equal(got, c.writes) {
				t.Errorf("writes %v, want %v", got, c.writes)
			}
			if got := heldShorts(p.Held); !slices.Equal(got, c.held) {
				t.Errorf("held %v, want %v", got, c.held)
			}
			for _, h := range p.Held {
				if h.Rule != c.rule {
					t.Errorf("%s held by %v, want %v", short(h.Write), h.Rule, c.rule)
				}
			}
			if len(p.Ammo) != 0 {
				t.Errorf("ammo %v while held", shorts(p.Ammo))
			}
			checkIdempotent(t, &c.view, &c.raw, c.ctx, p)

			// Deferred, never dropped: once the rule no longer holds, every
			// held byte is planned (to the same value or, as the world's
			// flags now merge too, a better one), and the equipment the
			// held flags give with it.
			img, _ := Apply(&c.raw, c.ctx, p.Writes)
			q := Pending(&c.view, &img, c.after)
			if len(q.Held) != 0 {
				t.Errorf("still held after the rule: %v", heldShorts(q.Held))
			}
			for _, h := range p.Held {
				i := slices.IndexFunc(q.Writes, func(w Write) bool { return w.Kind == h.Write.Kind && w.Addr == h.Write.Addr })
				if i < 0 || q.Writes[i].Val < h.Write.Val && h.Write.Kind != KindFlag || q.Writes[i].Val&h.Write.Val != h.Write.Val && h.Write.Kind == KindFlag {
					t.Errorf("held %s is not planned after the rule (plan %v)", short(h.Write), shorts(q.Writes))
				}
			}
			if c.rule == RuleFF1Sword && !slices.ContainsFunc(q.Writes, func(w Write) bool { return w.Kind == KindEquip && w.Off == offSword }) {
				t.Errorf("no sword equipment after the rule (plan %v)", shorts(q.Writes))
			}
		})
	}
}

// TestRulesNeedTheirCondition: each rule holds only on its exact
// condition.
func TestRulesNeedTheirCondition(t *testing.T) {
	cases := []struct {
		name string
		raw  world.Image
		ctx  Context
		ff1  bool
		gt   bool
	}{
		{"FF1 type without 0x0801", fresh(), Context{StageName: "MajyuE", StageType: stageTypeFF1, StageTypeOK: true}, false, false},
		{"0x0801 outside FF1", with(fresh(), offEv0801, 0x01), Context{StageName: "MajyuE", StageType: 1, StageTypeOK: true}, false, false},
		{"0x0801 in FF1", with(fresh(), offEv0801, 0x01), Context{StageName: "MajyuE", StageType: stageTypeFF1, StageTypeOK: true}, true, false},
		{"0x0801, STAG unread", with(fresh(), offEv0801, 0x01), Context{StageName: "MajyuE"}, true, false},
		{"other bits of 0x08xx", with(fresh(), offEv0801, 0xFE), Context{StageType: stageTypeFF1, StageTypeOK: true}, false, false},
		{"GTower", fresh(), Context{StageName: "GTower", StageTypeOK: true}, false, true},
		{"GanonK", fresh(), Context{StageName: "GanonK", StageTypeOK: true}, false, false},
		{"GTower prefix only", fresh(), Context{StageName: "GTowe", StageTypeOK: true}, false, false},
	}
	for _, c := range cases {
		if got := c.ctx.ff1(&c.raw); got != c.ff1 {
			t.Errorf("%s: ff1 %v, want %v", c.name, got, c.ff1)
		}
		if got := c.ctx.gtower(); got != c.gt {
			t.Errorf("%s: gtower %v, want %v", c.name, got, c.gt)
		}
	}
}

// TestButtons: play.mSelectItem is refreshed for each button bound to a
// derived slot whose item the HUD shows as nothing or a lesser variant,
// and for no other.
func TestButtons(t *testing.T) {
	type btn struct {
		slot  byte // the save's mSelectItem binding
		shown byte // play.mSelectItem
	}
	bow := with(fresh(), offBowFlags, 0x01, offBowSlot, ItemBow, addr.OffArrowMax, 30)
	magicView := with(fresh(), offBowFlags, 0x03)
	cases := []struct {
		name      string
		raw, view world.Image
		btns      [Buttons]btn
		want      []string
	}{
		{
			name: "two buttons bound to the bow, one to the telescope",
			raw:  with(bow, addr.OffGetItem+SlotTelescope, 0x01, addr.OffItems+SlotTelescope, ItemTelescope),
			view: magicView,
			btns: [Buttons]btn{{SlotBow, ItemBow}, {SlotTelescope, ItemTelescope}, {SlotBow, ItemBow}},
			want: []string{"select-item@803CA7DB=35", "select-item@803CA7DD=35"},
		},
		{
			name: "unbound, bottle-bound and bag-bound buttons are left alone",
			raw:  with(bow, addr.OffItems+addr.SlotBottle0, 0x50),
			view: magicView,
			btns: [Buttons]btn{{ItemNone, ItemNone}, {addr.SlotBottle0, 0x50}, {0x24, 0x82}},
		},
		{
			name: "a button showing an item the slot can't name is left alone",
			raw:  with(bow, addr.OffSelectItem, SlotBow),
			view: magicView,
			btns: [Buttons]btn{{SlotBow, 0x50}, {ItemNone, ItemNone}, {ItemNone, ItemNone}},
		},
		{
			name: "a button showing a better variant is not moved down",
			raw:  bow,
			view: magicView,
			btns: [Buttons]btn{{SlotBow, ItemLightArrow}, {ItemNone, ItemNone}, {ItemNone, ItemNone}},
		},
		{
			name: "the game's own stale button (deluxe picto box) is refreshed",
			raw:  with(fresh(), addr.OffGetItem+SlotCamera, 0x03, addr.OffItems+SlotCamera, ItemDeluxePictoBox),
			view: fresh(),
			btns: [Buttons]btn{{ItemNone, ItemNone}, {SlotCamera, ItemPictoBox}, {ItemNone, ItemNone}},
			want: []string{"select-item@803CA7DC=26"},
		},
		{
			name: "a bound button showing nothing gets the slot's item",
			raw:  fresh(),
			view: with(fresh(), addr.OffGetItem+SlotHookshot, 0x01),
			btns: [Buttons]btn{{ItemNone, ItemNone}, {ItemNone, ItemNone}, {SlotHookshot, ItemNone}},
			want: []string{"select-item@803CA7DD=2F"},
		},
		{
			name: "a button bound to an empty slot is left for the game to unbind",
			raw:  fresh(),
			view: fresh(),
			btns: [Buttons]btn{{SlotHookshot, ItemNone}, {ItemNone, ItemNone}, {ItemNone, ItemNone}},
		},
		{
			name: "already shown",
			raw:  with(bow, offBowFlags, 0x03, offBowSlot, ItemMagicArrow),
			view: magicView,
			btns: [Buttons]btn{{SlotBow, ItemMagicArrow}, {ItemNone, ItemNone}, {ItemNone, ItemNone}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := noButtons()
			for i, b := range c.btns {
				c.raw[addr.OffSelectItem+i] = b.slot
				ctx.SelectItem[i] = b.shown
			}
			p := Pending(&c.view, &c.raw, ctx)
			var got []string
			for _, w := range p.Writes {
				if w.Kind == KindSelectItem {
					got = append(got, short(w))
					if w.Old != ctx.SelectItem[w.Addr-addr.PlaySelectItem] || w.Op != OpSet || w.Off != -1 {
						t.Errorf("%s: Old 0x%02X Op %v Off %d", short(w), w.Old, w.Op, w.Off)
					}
				}
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("button writes %v, want %v", got, c.want)
			}
			// Buttons come after the slot writes they follow.
			last := -1
			for i, w := range p.Writes {
				if w.Kind == KindSlot {
					last = i
				}
				if w.Kind == KindSelectItem && i < last {
					t.Errorf("%s planned before a slot write", short(w))
				}
			}
			checkIdempotent(t, &c.view, &c.raw, ctx, p)
		})
	}
}

// TestDue: live writes need IDLE; equipment needs the stage-entry window
// too.
func TestDue(t *testing.T) {
	raw := with(fresh(), offSwordBits, 0x01, offSword, ItemSword)
	view := with(fresh(), offSwordBits, 0x03, addr.OffTact, 0x01)
	p := Pending(&view, &raw, noButtons())
	idle := gate.Status{Stable: true, Idle: true, Heartbeat: true}
	busy := gate.Status{Stable: true, Heartbeat: true}
	cases := []struct {
		st    gate.Status
		entry bool
		want  []string
	}{
		{idle, false, []string{"flag@0B4=03", "flag@0BD=01"}},
		{idle, true, []string{"flag@0B4=03", "flag@0BD=01", "equip@00E=39"}},
		{busy, true, nil},
		{gate.Status{Stable: true, Idle: true}, true, nil},
		{gate.Status{Idle: true, Heartbeat: true}, true, nil},
	}
	for _, c := range cases {
		if got := shorts(p.Due(c.st, c.entry)); !slices.Equal(got, c.want) {
			t.Errorf("Due(%+v, %v) = %v, want %v", c.st, c.entry, got, c.want)
		}
	}
}

// TestEntry: the stage-entry window is the first IDLE tick after a stage
// load, and only that one.
func TestEntry(t *testing.T) {
	idle := gate.Status{Stable: true, Idle: true, Heartbeat: true}
	closed := gate.Status{Heartbeat: true}
	type tick struct {
		stage  string
		player uint32
		next   int8
		room   int8
		st     gate.Status
		want   bool
	}
	ticks := []tick{
		{"sea", 0x80A00000, 0, 0, idle, false}, // session start mid-stage: no window
		{"sea", 0x80A00000, 0, 0, idle, false},
		{"sea", 0x80A00000, 0, 3, idle, false},   // a room change is no load
		{"sea", 0x80A00000, 1, 3, closed, false}, // a stage change is asked for
		{"sea", 0, 0, 3, closed, false},
		{"Asoko", 0x80A10000, 0, 0, closed, false},
		{"Asoko", 0x80A10000, 0, 0, idle, true}, // the window
		{"Asoko", 0x80A10000, 0, 0, idle, false},
		{"Asoko", 0x80A20000, 0, 0, closed, false}, // a reload of the same stage: new Link
		{"Asoko", 0x80A20000, 0, 0, gate.Status{Stable: true, Heartbeat: true}, false},
		{"Asoko", 0x80A20000, 0, 0, idle, true},
		{"Asoko", 0x80A20000, 0, 0, idle, false},
		{"Asoko", 0x80A20000, 1, 0, closed, false}, // a void-out to the same stage and Link
		{"Asoko", 0x80A20000, 0, 0, idle, true},
	}
	var e Entry
	for i, k := range ticks {
		s := &snap.Snapshot{StageName: k.stage, PlayerPtr: k.player, NextEnable: k.next, StayNo: k.room}
		if got := e.Tick(s, k.st); got != k.want {
			t.Errorf("tick %d (%+v): window %v, want %v", i, k, got, k.want)
		}
	}
}

// TestContextOf decodes the button items and the ammo counters from a
// snapshot, and the stage type from the STAG chunk.
func TestContextOf(t *testing.T) {
	s := &snap.Snapshot{StageName: "GTower", StagInfo: 0x80B00000}
	pl := func(a uint32) []byte { return s.Player[a-snap.PlayerRegion.Start:] }
	copy(pl(addr.PlaySelectItem), []byte{ItemBow, ItemNone, ItemTelescope, 0x77})
	copy(pl(ItemArrowNumCount), []byte{0xFF, 0xFF})
	copy(pl(ItemBombNumCount), []byte{0x00, 0x1E})
	m := mem.NewFakeMem()
	if err := m.Store(s.StagInfo+0x0C, []byte{0x00, 0x05 | 0xF8, 0x00, 0x00}); err != nil {
		t.Fatal(err)
	}
	c := ReadContext(m, s)
	want := Context{StageName: "GTower", StageType: stageTypeFF1, StageTypeOK: true,
		SelectItem: [Buttons]byte{ItemBow, ItemNone, ItemTelescope}, ArrowCount: -1, BombCount: 30}
	if c != want {
		t.Errorf("ReadContext = %+v, want %+v", c, want)
	}
	s.StagInfo = 0
	if c := ReadContext(m, s); c.StageTypeOK {
		t.Errorf("no STAG: %+v, want StageTypeOK false", c)
	}
	s.StagInfo = addr.MEM1End - 4 // the type byte is past MEM1: the read fails
	if c := ReadContext(m, s); c.StageTypeOK {
		t.Errorf("unreadable STAG: %+v, want StageTypeOK false", c)
	}
}

// TestApply lands writes as the writer would: OR, MAX and compare-and-set
// against what the byte reads.
func TestApply(t *testing.T) {
	raw := with(fresh(), addr.OffTact, 0x10, addr.OffArrowMax, 60, offBowSlot, ItemMagicArrow)
	ctx := noButtons()
	ctx.SelectItem[2] = ItemTelescope
	ctx.BombCount = 0x0100
	ws := []Write{
		imageWrite(KindFlag, addr.OffTact, OpOR, 0x00, 0x03, Live, "or"),
		imageWrite(KindCapacity, addr.OffArrowMax, OpMax, 0, 30, Live, "max below"),
		imageWrite(KindSlot, offBowSlot, OpSet, ItemNone, ItemBow, Live, "stale set"),
		{Kind: KindSelectItem, Addr: addr.PlaySelectItem + 2, Off: -1, Op: OpSet, Old: ItemTelescope, Val: ItemBow},
		{Kind: KindAmmo, Addr: ItemArrowNumCount + 1, Off: -1, Op: OpSet, Old: 0, Val: 30},
		{Kind: KindAmmo, Addr: ItemBombNumCount + 1, Off: -1, Op: OpSet, Old: 0, Val: 30},
	}
	img, got := Apply(&raw, ctx, ws)
	if img[addr.OffTact] != 0x13 || img[addr.OffArrowMax] != 60 || img[offBowSlot] != ItemMagicArrow {
		t.Errorf("image: tact 0x%02X arrow max %d bow slot 0x%02X", img[addr.OffTact], img[addr.OffArrowMax], img[offBowSlot])
	}
	if got.SelectItem[2] != ItemBow || got.ArrowCount != 30 || got.BombCount != 0x011E {
		t.Errorf("context %+v", got)
	}
}

// TestKindStrings covers the names the logs use.
func TestKindStrings(t *testing.T) {
	for _, s := range []string{KindAmmo.String(), Kind(0).String(), OpSet.String(), Op(9).String(),
		AtStageEntry.String(), Timing(9).String(), RuleFF1Sword.String(), RuleGTowerBow.String(), RuleAmmoBusy.String(), Rule(0).String()} {
		if s == "" {
			t.Error("empty name")
		}
	}
	w := imageWrite(KindFlag, addr.OffTact, OpOR, 0x01, 0x03, Live, "why")
	if w.Bits() != 0x02 || !strings.Contains(w.String(), "why") {
		t.Errorf("Bits 0x%02X, String %q", w.Bits(), w.String())
	}
}
