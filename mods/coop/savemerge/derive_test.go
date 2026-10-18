package savemerge

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys/itemsrc"
)

func TestDeriveSlots(t *testing.T) {
	const (
		bow, magicArrow, lightArrow = 0x27, 0x35, 0x36
		sword, ms1, ms3             = 0x38, 0x39, 0x3E
	)
	bowSlot := addr.OffItems + addr.SlotBow
	for _, c := range []struct {
		name       string
		a, b       func(*save.Image)
		off        int
		want       byte
		wantNote   string
		wantSilent bool
	}{
		{"empty slot filled", nil, func(b *save.Image) { b[addr.OffGetItem+19] = 1 }, addr.OffItems + 19, 0x2F, "hookshot", false},
		{"best bow variant", nil, func(b *save.Image) { b[addr.OffGetItem+addr.SlotBow] = 0x07 }, bowSlot, lightArrow, "bow", false},
		{"lesser variant raised", func(a *save.Image) { a[addr.OffGetItem+addr.SlotBow] = 1; a[bowSlot] = bow },
			func(b *save.Image) { b[addr.OffGetItem+addr.SlotBow] = 3 }, bowSlot, magicArrow, "0x27 -> 0x35", false},
		{"better variant kept", func(a *save.Image) { a[addr.OffGetItem+addr.SlotBow] = 7; a[bowSlot] = lightArrow },
			func(b *save.Image) { b[addr.OffGetItem+addr.SlotBow] = 1 }, bowSlot, lightArrow, "", true},
		{"unknown item kept", func(a *save.Image) { a[addr.OffItems+5] = 0x99 },
			func(b *save.Image) { b[addr.OffGetItem+5] = 1 }, addr.OffItems + 5, 0x99, "kept item 0x99", false},
		{"deluxe picto box", nil, func(b *save.Image) { b[addr.OffGetItem+8] = 3 }, addr.OffItems + 8, 0x26, "picto box", false},
		{"no flag, no item", nil, func(b *save.Image) { b[addr.OffItems+20] = 0x33 }, addr.OffItems + 20, save.ItemNone, "", true},
		{"sword equipped", nil, func(b *save.Image) { b[addr.OffCollectFlags] = 0x03 }, addr.OffSelectEquip, ms1, "sword", false},
		{"sword raised", func(a *save.Image) { a[addr.OffCollectFlags] = 1; a[addr.OffSelectEquip] = sword },
			func(b *save.Image) { b[addr.OffCollectFlags] = 0x0F }, addr.OffSelectEquip, ms3, "0x38 -> 0x3E", false},
		{"mirror shield", nil, func(b *save.Image) { b[addr.OffCollectFlags+1] = 3 }, addr.OffSelectEquip + 1, 0x3C, "shield", false},
		{"bracelets", nil, func(b *save.Image) { b[addr.OffCollectFlags+2] = 1 }, addr.OffSelectEquip + 2, 0x28, "mSelectEquip (power bracelets)", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := freshImage("Link"), freshImage("Tetra")
			if c.a != nil {
				c.a(&a)
			}
			c.b(&b)
			r, err := Merge(&a, &b, MergeOptions{Derive: Derive(nil, nil)})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Image[c.off]; got != c.want {
				t.Errorf("byte 0x%03X = 0x%02X, want 0x%02X", c.off, got, c.want)
			}
			notes := strings.Join(r.Notes, "\n")
			if c.wantSilent && notes != "" || !strings.Contains(notes, c.wantNote) {
				t.Errorf("notes %q, want %q", notes, c.wantNote)
			}
		})
	}
}

// keyTable is a passing table 3 with key chests 0-2 and doors on memory
// switches 0x10-0x12.
func keyTable(pass bool) *keys.Result {
	tb := keys.Table{SaveTable: 3, Dungeon: "DRC", Expected: 3, Pass: pass}
	for i := range 3 {
		tb.Sources = append(tb.Sources, itemsrc.Source{
			Kind: itemsrc.Chest, Item: itemsrc.ItemSmallKey, Flag: itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: 3, Bit: i},
		})
		tb.Doors = append(tb.Doors, keys.Door{Switch: 0x10 + i, Saved: true})
	}
	return &keys.Result{Tables: []keys.Table{tb}, Pass: pass}
}

func TestDeriveKeys(t *testing.T) {
	keyOff := addr.OffMemBit(3) + addr.MemBitOffKeyNum
	chest := func(img *save.Image, i int) {
		b, _ := addr.TboxBit(i)
		img[addr.OffMemBit(3)+b.Off] |= b.Mask
	}
	door := func(img *save.Image, i int) {
		_, b, _ := addr.SwitchBit(0x10 + i)
		img[addr.OffMemBit(3)+b.Off] |= b.Mask
	}
	for _, c := range []struct {
		name     string
		pass     bool
		a, b     func(*save.Image)
		want     byte
		wantNote string
		noNote   string // a note that must not show
	}{
		{"partner's key", true, nil, func(b *save.Image) { chest(b, 0); b[keyOff] = 1 }, 1, "0 -> 1", ""},
		{"same key, partner spent it", true,
			func(a *save.Image) { chest(a, 0); a[keyOff] = 1 },
			func(b *save.Image) { chest(b, 0); door(b, 0) }, 0, "1 -> 0", "beyond"},
		{"key on a door a opened too", true,
			func(a *save.Image) { chest(a, 0); door(a, 0) },
			func(b *save.Image) { chest(b, 0); door(b, 0) }, 0, "", ""},
		{"one key, two doors", true,
			func(a *save.Image) { chest(a, 0); door(a, 0) },
			func(b *save.Image) { chest(b, 0); door(b, 1) }, 0, "", ""},
		// a is the output of "one key, two doors": keys(t) is -1 over its
		// flags and its 0 keys are the HUD's clamp, no key from anywhere.
		{"a with more doors than sources", true,
			func(a *save.Image) { chest(a, 0); door(a, 0); door(a, 1) },
			func(b *save.Image) { chest(b, 0); chest(b, 1); door(b, 0); door(b, 1) }, 0, "", "kept"},
		{"a's key beyond its flags goes", true,
			func(a *save.Image) { chest(a, 0); a[keyOff] = 2 },
			func(b *save.Image) { chest(b, 0); door(b, 0) }, 0, "2 -> 0: 1 of 3 source(s) taken, 1 of 3 door(s) open; a held 1 key(s) beyond what its own flags give", ""},
		{"a's key beyond its flags, b's door debt", true,
			func(a *save.Image) { chest(a, 0); a[keyOff] = 2 },
			func(b *save.Image) { chest(b, 0); door(b, 0); door(b, 1) }, 0, "a held 1 key(s)", ""},
		{"past the HUD's 99", true,
			func(a *save.Image) { chest(a, 0); a[keyOff] = 120 },
			func(b *save.Image) { chest(b, 1) }, 2, "120 -> 2", ""},
		{"failing table kept", false, nil, func(b *save.Image) { chest(b, 0); chest(b, 1) }, 0, "not derived: its M1 key invariant fails", ""},
		{"untouched table kept", true, func(a *save.Image) { chest(a, 0); a[keyOff] = 0 }, nil, 0, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := freshImage("Link"), freshImage("Tetra")
			if c.a != nil {
				c.a(&a)
			}
			if c.b != nil {
				c.b(&b)
			}
			pol := world.DefaultPolicy()
			pol.GrantSwitches(func(table, no int) bool { return table == 3 && no >= 0x10 && no < 0x13 })
			kr := keyTable(c.pass)
			r, err := Merge(&a, &b, MergeOptions{Policy: pol, Derive: Derive(kr, nil)})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Image[keyOff]; got != c.want {
				t.Errorf("keys %d, want %d (notes %q)", got, c.want, r.Notes)
			}
			notes := strings.Join(r.Notes, "\n")
			if !strings.Contains(notes, c.wantNote) || c.noNote != "" && strings.Contains(notes, c.noNote) {
				t.Errorf("notes %q, want %q and not %q", notes, c.wantNote, c.noNote)
			}
			if !c.pass {
				return
			}
			// Where the merge took a source or opened a door, the live
			// derive's target over the merged flags.
			tb := &kr.Tables[0]
			sources, doors := keyCount(&r.Image, tb)
			sa, da := keyCount(&a, tb)
			if live := derive.KeyTarget(derive.KeyFacts{Taken: sources, Open: doors}, 0); (sources != sa || doors != da) && r.Image[keyOff] != byte(live) {
				t.Errorf("keys %d, the live derive's target %d", r.Image[keyOff], live)
			}
			// Files that hold what their own flags give merge to the same
			// keys either way round.
			sb, db := keyCount(&b, tb)
			if int(a[keyOff]) == derive.KeyTarget(derive.KeyFacts{Taken: sa, Open: da}, 0) &&
				int(b[keyOff]) == derive.KeyTarget(derive.KeyFacts{Taken: sb, Open: db}, 0) {
				rb, err := Merge(&b, &a, MergeOptions{Policy: pol, Derive: Derive(kr, nil)})
				if err != nil {
					t.Fatal(err)
				}
				if rb.Image[keyOff] != r.Image[keyOff] {
					t.Errorf("merged b into a: keys %d; a into b: %d", r.Image[keyOff], rb.Image[keyOff])
				}
			}
		})
	}
}

// heartTable is a passing heart table: pieces in sea chests 0-1, the Ivan
// reward on event bit 0x1340, containers by table 3's and 4's STAGE_LIFE.
func heartTable(pass bool) *hearts.Result {
	h := &hearts.Result{Pass: pass}
	for i := range 2 {
		h.Pieces = append(h.Pieces, itemsrc.Source{Item: itemsrc.ItemHeartPiece, Flag: itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: 0, Bit: i}})
	}
	h.Rewards = []hearts.Reward{
		{ID: "ivan", Record: "event bit 0x1340 (UNK_1340, d_save_event_flag.inc:143)"},
		{ID: "letter", Record: "letter register 0xAC03 (LETTER_BAITOS_MOM)"},
		{ID: "minigame"},
	}
	for _, tb := range []int{3, 4} {
		h.Containers = append(h.Containers, itemsrc.Source{Item: itemsrc.ItemHeartContainer, Flag: itemsrc.Flag{Kind: itemsrc.StageLifeFlag, Table: tb}})
	}
	return h
}

func TestDeriveLife(t *testing.T) {
	piece := func(img *save.Image, i int) {
		b, _ := addr.TboxBit(i)
		img[addr.OffMemBit(0)+b.Off] |= b.Mask
	}
	container := func(img *save.Image, table int) {
		img[addr.OffMemBit(table)+addr.MemBitOffDungeonItem] |= 1 << addr.DungeonItemStageLife
	}
	for _, c := range []struct {
		name     string
		pass     bool
		maxLife  int
		a, b     func(*save.Image)
		want     int
		wantNote string
	}{
		{"partner's piece and container", true, 12, nil,
			func(b *save.Image) { piece(b, 1); container(b, 4) }, 17, "1 heart piece(s) and 1 container(s)"},
		{"piece both took counts once", true, 13,
			func(a *save.Image) { piece(a, 0) },
			func(b *save.Image) { piece(b, 0); piece(b, 1) }, 14, "13 -> 14"},
		{"reward by its event bit", true, 12, nil, func(b *save.Image) { setEvent(b, 0x1340) }, 13, "1 heart piece"},
		// The letter register merges by MAX, so a's file reads the letter
		// read. The mailbox hands over only a stocked letter
		// (d_a_obj_toripost.cpp:270) and nothing stocks a read one
		// (d_letter.cpp:53-62): its piece comes with the merge or never.
		{"reward by its letter read", true, 12, nil, func(b *save.Image) { b[addr.OffEvent+0xAC] |= 0x03 }, 13, "1 heart piece"},
		{"a letter not yet read", true, 12, nil, func(b *save.Image) { b[addr.OffEvent+0xAC] |= 0x02 }, 12, ""},
		// a's 16 holds a minigame piece no flag records: the merge adds to it.
		{"a's unrecorded reward kept", true, 16, nil, func(b *save.Image) { piece(b, 0) }, 17, "16 -> 17"},
		{"cap", true, 0x4E, nil, func(b *save.Image) { container(b, 3) }, 0x50, "78 -> 80"},
		{"at the cap", true, 0x50, nil, func(b *save.Image) { piece(b, 1) }, 0x50, "the cap"},
		{"failing table kept", false, 12, nil, func(b *save.Image) { piece(b, 1) }, 12, "fails its check"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := freshImage("Link"), freshImage("Tetra")
			a[addr.OffMaxLife], a[addr.OffMaxLife+1] = byte(c.maxLife>>8), byte(c.maxLife)
			if c.a != nil {
				c.a(&a)
			}
			c.b(&b)
			r, err := Merge(&a, &b, MergeOptions{Policy: world.FullPolicy(), Derive: Derive(nil, heartTable(c.pass))})
			if err != nil {
				t.Fatal(err)
			}
			if got := int(be16(r.Image[addr.OffMaxLife:])); got != c.want {
				t.Errorf("max life %d, want %d (notes %q)", got, c.want, r.Notes)
			}
			if notes := strings.Join(r.Notes, "\n"); !strings.Contains(notes, c.wantNote) {
				t.Errorf("notes %q, want %q", notes, c.wantNote)
			}
		})
	}
}

func TestDeriveMagic(t *testing.T) {
	for _, c := range []struct {
		name       string
		a, b       byte
		bLeaf      bool
		want       byte
		wantSilent bool
	}{
		{"partner's Deku Leaf", 0, 16, true, 16, false},
		{"Deku Leaf flag alone", 0, 0, true, 16, false},
		{"partner's upgrade", 16, 32, false, 32, false},
		{"a's larger kept", 32, 16, true, 32, true},
		{"capped", 0, 0x40, false, 0x20, false},
		{"nothing", 0, 0, false, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := freshImage("Link"), freshImage("Tetra")
			a[addr.OffMaxMagic], b[addr.OffMaxMagic] = c.a, c.b
			if c.bLeaf {
				b[addr.OffGetItem+slotLeaf] = 1
			}
			r, err := Merge(&a, &b, MergeOptions{Derive: Derive(nil, nil)})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Image[addr.OffMaxMagic]; got != c.want {
				t.Errorf("max magic %d, want %d", got, c.want)
			}
			magic := false
			for _, n := range r.Notes {
				magic = magic || strings.HasPrefix(n, "max magic")
			}
			if magic == c.wantSilent {
				t.Errorf("notes %q", r.Notes)
			}
		})
	}
}

func TestFlagSet(t *testing.T) {
	var img save.Image
	img[addr.OffMapComplete+3] = 0x01 // chart 1: bit 0 of the first word
	img[addr.OffMapComplete+4] = 0x80 // chart 64: bit 63, the second word's top
	ob, _ := addr.OceanBit(9, 3)
	img[addr.OffOcean+ob.Off] |= ob.Mask
	for _, c := range []struct {
		f       itemsrc.Flag
		set, ok bool
	}{
		{itemsrc.Flag{Kind: itemsrc.ChartFlag, Bit: 1}, true, true},
		{itemsrc.Flag{Kind: itemsrc.ChartFlag, Bit: 64}, true, true},
		{itemsrc.Flag{Kind: itemsrc.ChartFlag, Bit: 2}, false, true},
		{itemsrc.Flag{Kind: itemsrc.ChartFlag, Bit: 0}, false, false},
		{itemsrc.Flag{Kind: itemsrc.OceanFlag, Room: 9, Bit: 3}, true, true},
		{itemsrc.Flag{Kind: itemsrc.OceanFlag, Room: 0x40, Bit: 3}, false, false},
		{itemsrc.Flag{Kind: itemsrc.TboxFlag, Table: 16, Bit: 0}, false, false},
		{itemsrc.Flag{Kind: itemsrc.ItemFlag, Table: 1, Bit: 64}, false, false},
		{itemsrc.Flag{Kind: itemsrc.ZoneItemFlag, Room: 1, Bit: 0x40}, false, false},
	} {
		if set, ok := flagSet(&img, c.f); set != c.set || ok != c.ok {
			t.Errorf("%s: set %v ok %v, want %v %v", c.f, set, ok, c.set, c.ok)
		}
	}
	reg := addr.EventByte(0xAC03) // LETTER_BAITOS_MOM's register
	for _, c := range []struct {
		name string
		rec  tables.Record
		set  func(*save.Image)
		want bool
	}{
		{"event bit", tables.Record{Kind: tables.RecordEventBit, ID: 0x1340}, func(img *save.Image) { setEvent(img, 0x1340) }, true},
		{"event bit unset", tables.Record{Kind: tables.RecordEventBit, ID: 0x1340}, func(img *save.Image) { setEvent(img, 0x1380) }, false},
		{"letter read", tables.Record{Kind: tables.RecordLetterRead, ID: 0xAC03}, func(img *save.Image) { img[addr.OffEvent+reg.Off] = 0x03 }, true},
		{"letter in the mailbox", tables.Record{Kind: tables.RecordLetterRead, ID: 0xAC03}, func(img *save.Image) { img[addr.OffEvent+reg.Off] = 0x02 }, false},
		{"letter read, the byte's other fields set", tables.Record{Kind: tables.RecordLetterRead, ID: 0xAC03}, func(img *save.Image) { img[addr.OffEvent+reg.Off] = 0xF3 }, true},
		{"no record", tables.Record{}, func(img *save.Image) { setEvent(img, 0x1340) }, false},
	} {
		var img save.Image
		c.set(&img)
		if got := rewardGiven(&img, c.rec); got != c.want {
			t.Errorf("rewardGiven %s = %v, want %v", c.name, got, c.want)
		}
	}
}
