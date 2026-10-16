package itemsrc

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
)

// testNames is an l_objectName with one real name for each class Decode
// reads (their d_stage.cpp lines are cited where the procs are defined),
// plus "Orphan", a name whose proc decodes nothing.
func testNames(t testing.TB) *stagedata.ObjectNames {
	t.Helper()
	o, err := stagedata.ParseObjectNames(stagetest.ObjectNameTable(
		stagetest.ObjectName{Name: "takara", Proc: int16(stagedata.ProcTbox), Subtype: -1},
		stagetest.ObjectName{Name: "item", Proc: int16(stagedata.ProcItem), Subtype: -1},
		stagetest.ObjectName{Name: "itemFLY", Proc: int16(stagedata.ProcItem), Subtype: -1},
		stagetest.ObjectName{Name: "Ritem", Proc: int16(ProcRaceItem), Subtype: -1},
		stagetest.ObjectName{Name: "Salvage", Proc: int16(ProcSalvage), Subtype: -1},
		stagetest.ObjectName{Name: "STBox", Proc: int16(ProcSalvageChest), Subtype: -1},
		stagetest.ObjectName{Name: "Kmtub", Proc: int16(ProcTsubo), Subtype: 2},
		stagetest.ObjectName{Name: "koisi1", Proc: int16(ProcStone), Subtype: 0},
		stagetest.ObjectName{Name: "Ebrock", Proc: int16(ProcStone2), Subtype: 0},
		stagetest.ObjectName{Name: "Kkiba", Proc: int16(stagedata.ProcMovebox), Subtype: 2},
		stagetest.ObjectName{Name: "Vochi", Proc: int16(ProcLeaves), Subtype: 0},
		stagetest.ObjectName{Name: "Hcbh", Proc: int16(ProcHcbh), Subtype: 0},
		stagetest.ObjectName{Name: "Homen1", Proc: int16(ProcHomen), Subtype: 0},
		stagetest.ObjectName{Name: "Hmos1", Proc: int16(ProcBemos), Subtype: 0},
		stagetest.ObjectName{Name: "ITat00", Proc: int16(ProcSwItem), Subtype: -1},
		stagetest.ObjectName{Name: "Ospbox", Proc: int16(ProcOspbox), Subtype: -1},
		stagetest.ObjectName{Name: "Ktarur", Proc: int16(ProcBarrel2), Subtype: -1},
		stagetest.ObjectName{Name: "kusax1", Proc: int16(ProcGrass), Subtype: -1},
		stagetest.ObjectName{Name: "TagKb", Proc: int16(ProcTagKbItem), Subtype: -1},
		stagetest.ObjectName{Name: "Orphan", Proc: int16(stagedata.ProcSwc00), Subtype: -1},
	))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// load reads stages through a synthetic disc, the way coop-tables does.
func load(t testing.TB, stages ...stagetest.Stage) []*stagedata.Stage {
	t.Helper()
	d := new(stagetest.Disc)
	for _, s := range stages {
		d.AddStage(s)
	}
	out, _, err := stagedata.Load(d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// stage is a stage on save table table with chunks in its stage.dzs and
// the given rooms.
func stage(name string, table int, dzs []stagetest.Chunk, rooms ...stagetest.Room) stagetest.Stage {
	chunks := append([]stagetest.Chunk{stagetest.StagChunk(stagetest.StagInfo(table, int(stagedata.StageDungeon), true))}, dzs...)
	return stagetest.Stage{Name: name, DZS: stagetest.DZx(chunks...), Rooms: rooms}
}

func room(no int, chunks ...stagetest.Chunk) stagetest.Room {
	return stagetest.Room{No: no, DZR: stagetest.DZx(chunks...)}
}

func all(uint8) bool { return true }

func TestDecode(t *testing.T) {
	type a = stagetest.Actor
	z := func(v uint16) [3]int16 { return [3]int16{0, 0, int16(v)} }
	none := Flag{}
	item := func(bit int) Flag { return Flag{Kind: ItemFlag, Table: 3, Bit: bit} }
	cases := []struct {
		name   string
		tag    string // "ACTR" when empty
		room   int    // 2 when zero
		actor  a
		ok     bool
		kind   Kind
		item   uint8
		flag   Flag
		layerN int
	}{
		{name: "chest key", actor: a{Name: "takara", Params: 5 << 7, Angle: z(0x15FF)}, ok: true, kind: Chest, item: 0x15, flag: Flag{Kind: TboxFlag, Table: 3, Bit: 5}},
		{name: "chest on a switch", actor: a{Name: "takara", Params: 0x2A<<12 | 31<<7 | 1, Angle: z(0x0700)}, ok: true, kind: Chest, item: 7, flag: Flag{Kind: TboxFlag, Table: 3, Bit: 31}},
		{name: "chest, sea2 table (type 7)", actor: a{Name: "takara", Params: 2<<7 | 7, Angle: z(0x0712)}, ok: true, kind: Chest, item: 7, flag: Flag{Kind: TboxFlag, Table: 1, Bit: 2}},
		{name: "chest, sea2 table (type 8)", actor: a{Name: "takara", Params: 2<<7 | 8, Angle: z(0x0700)}, ok: true, kind: Chest, item: 7, flag: Flag{Kind: TboxFlag, Table: 1, Bit: 2}},
		{name: "chest, high bits ignored", actor: a{Name: "takara", Params: 0xFFF00000 | 9<<7, Angle: z(0x8000)}, ok: true, kind: Chest, item: 0x80, flag: Flag{Kind: TboxFlag, Table: 3, Bit: 9}},
		{name: "empty chest", actor: a{Name: "takara", Angle: z(0xFF00)}},
		{name: "item, memory bit", actor: a{Name: "item", Params: 0x12<<8 | 0x15}, ok: true, kind: FieldItem, item: 0x15, flag: item(0x12)},
		{name: "item, high bit of the bit byte", actor: a{Name: "itemFLY", Params: 0x92<<8 | 0x07}, ok: true, kind: FieldItem, item: 7, flag: item(0x12)},
		{name: "item, no bit", actor: a{Name: "item", Params: 0x7F<<8 | 0x15}, ok: true, kind: FieldItem, item: 0x15, flag: none},
		{name: "item, zone bit", actor: a{Name: "item", Params: 0x45<<8 | 0x15}, ok: true, kind: FieldItem, item: 0x15, flag: Flag{Kind: ZoneItemFlag, Room: 2, Bit: 0x45}},
		{name: "item, bad bit", actor: a{Name: "item", Params: 0x55<<8 | 0x15}, ok: true, kind: FieldItem, item: 0x15, flag: Flag{Kind: BadFlag, Bit: 0x55}},
		{name: "blue jelly's bit is a switch", actor: a{Name: "item", Params: 0x05<<8 | 0x4B}, ok: true, kind: FieldItem, item: 0x4B, flag: none},
		{name: "item none", actor: a{Name: "item", Params: 0x05<<8 | 0xFF}},
		{name: "race item", actor: a{Name: "Ritem", Params: 0x3<<15 | 0x21<<8 | 0x07}, ok: true, kind: RaceItem, item: 7, flag: item(0x21)},
		{name: "pot key", actor: a{Name: "Kmtub", Params: 0x30<<16 | 0x3F<<8 | 0x15}, ok: true, kind: Pot, item: 0x15, flag: item(0x30)},
		{name: "pot, random table", actor: a{Name: "Kmtub", Params: 0x30<<16 | 0x25}},
		{name: "pot, nothing", actor: a{Name: "Kmtub", Params: 0x30<<16 | 0x3F}},
		{name: "pot, 6-bit item", actor: a{Name: "Kmtub", Params: 0x7F<<16 | 0xC7}, ok: true, kind: Pot, item: 7, flag: none},
		{name: "rock", actor: a{Name: "koisi1", Params: 0x01<<16 | 0x07}, ok: true, kind: Rock, item: 7, flag: item(1)},
		{name: "rock2", actor: a{Name: "Ebrock", Params: 0x02<<16 | 0x08}, ok: true, kind: Rock2, item: 8, flag: item(2)},
		{name: "crate", actor: a{Name: "Kkiba", Params: 0x03<<16 | 0x15}, ok: true, kind: Box, item: 0x15, flag: item(3)},
		{name: "leaves", actor: a{Name: "Vochi", Params: 0x04<<6 | 0x07}, ok: true, kind: Leaves, item: 7, flag: item(4)},
		{name: "face pillar", actor: a{Name: "Hcbh", Params: 0xFF<<13 | 0x05<<6 | 0x15}, ok: true, kind: FacePillar, item: 0x15, flag: item(5)},
		{name: "face stone", actor: a{Name: "Homen1", Params: 0x07<<12 | 0x06}, ok: true, kind: FaceStone, item: 7, flag: item(6)},
		{name: "face stone, nothing", actor: a{Name: "Homen1", Params: 0x3F<<12 | 0x06}},
		{name: "beamos", actor: a{Name: "Hmos1", Params: 0xFFFFFFFF, Angle: z(0x08<<6 | 0x15)}, ok: true, kind: Beamos, item: 0x15, flag: item(8)},
		{name: "beamos, sign bit", actor: a{Name: "Hmos1", Angle: z(0x8000 | 0x7F<<6 | 0x07)}, ok: true, kind: Beamos, item: 7, flag: none},
		{name: "switch item", actor: a{Name: "ITat00", Params: 0x09<<14 | 0x07<<8 | 0x22}, ok: true, kind: SwitchItem, item: 7, flag: item(9)},
		{name: "ospbox", actor: a{Name: "Ospbox", Params: 4<<8 | 0x07}, ok: true, kind: Ospbox, item: 7, flag: none},
		{name: "ospbox pig", actor: a{Name: "Ospbox", Params: 2<<8 | 0x07}},
		{name: "barrel", actor: a{Name: "Ktarur", Params: 0x0A<<16 | 0x07}, ok: true, kind: Barrel, item: 7, flag: item(10)},
		// daGrass_Create turns items below 0x20 into none (d_a_grass.cpp:
		// 144-145, :173-174): grass and flowers never place a fixed item.
		{name: "grass, piece number", actor: a{Name: "kusax1", Params: 0x07<<6 | 0<<4}},
		{name: "grass, container number", actor: a{Name: "kusax1", Params: 0x08<<6 | 0<<4}},
		{name: "grass, random table", actor: a{Name: "kusax1", Params: 0x25<<6 | 0<<4}},
		{name: "white flower, key number", actor: a{Name: "kusax1", Params: 0x15<<6 | 2<<4}},
		{name: "pink flower, key number", actor: a{Name: "kusax1", Params: 0x15<<6 | 3<<4}},
		{name: "tree", actor: a{Name: "kusax1", Params: 0x07<<6 | 1<<4}},
		{name: "salvage chart", room: 23, actor: a{Name: "Salvage", Params: 0<<28 | 12<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: ChartFlag, Bit: 12}},
		{name: "salvage ocean", room: 23, actor: a{Name: "Salvage", Params: 3<<28 | 4<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: OceanFlag, Room: 23, Bit: 4}},
		// The ocean asserts (d_save.cpp:1164-1173) halt the game.
		{name: "salvage ocean, kind 4 save number 31", room: 23, actor: a{Name: "Salvage", Params: 4<<28 | 31<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: HaltFlag, Room: 23, Bit: 31}},
		{name: "salvage ocean, kind 4 save number 20", room: 23, actor: a{Name: "Salvage", Params: 4<<28 | 20<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: HaltFlag, Room: 23, Bit: 20}},
		{name: "salvage ocean, kind 4 save number 15", room: 23, actor: a{Name: "Salvage", Params: 4<<28 | 15<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: OceanFlag, Room: 23, Bit: 15}},
		{name: "salvage ocean, room past 0x31", room: 50, actor: a{Name: "Salvage", Params: 3<<28 | 4<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: HaltFlag, Room: 50, Bit: 4}},
		{name: "salvage ocean, room 0x31", room: 0x31, actor: a{Name: "Salvage", Params: 2<<28 | 4<<20 | 0x07<<4, Angle: z(0x10)}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: OceanFlag, Room: 0x31, Bit: 4}},
		{name: "salvage ocean, room 0 map room past 0x31", room: 0, actor: a{Name: "Salvage", Params: 3<<28 | 4<<20 | 50<<12 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: HaltFlag, Room: 50, Bit: 4}},
		{name: "salvage ocean, room 0 save number 31", room: 0, actor: a{Name: "Salvage", Params: 4<<28 | 31<<20 | 50<<12 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: HaltFlag, Room: 0, Bit: 31}},
		{name: "salvage ocean, room 0 records grid 0", room: 0, actor: a{Name: "Salvage", Params: 3<<28 | 4<<20 | 40<<12 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: none},
		{name: "salvage chart, map room past 0x31", room: 50, actor: a{Name: "Salvage", Params: 0<<28 | 12<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: ChartFlag, Bit: 12}},
		{name: "salvage behind a switch", room: 23, actor: a{Name: "Salvage", Params: 2<<28 | 1<<20 | 0x07<<4, Angle: z(0x10)}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: OceanFlag, Room: 23, Bit: 1}},
		{name: "salvage event", room: 23, actor: a{Name: "Salvage", Params: 6<<28 | 2<<20 | 0x07<<4}, ok: true, kind: Salvage, item: 7, flag: Flag{Kind: SalvageEventFlag, Bit: 2}},
		{name: "salvage, room 0 names its map room", room: 0, actor: a{Name: "Salvage", Params: 1<<28 | 40<<12 | 0x08<<4}, ok: true, kind: Salvage, item: 8, flag: none},
		{name: "salvage, kind 2 needs a switch", room: 23, actor: a{Name: "Salvage", Params: 2<<28 | 1<<20 | 0x07<<4, Angle: z(0xFF)}},
		{name: "salvage, kind 3 save number over 15", room: 23, actor: a{Name: "Salvage", Params: 3<<28 | 16<<20 | 0x07<<4}},
		{name: "salvage, chart 0", room: 23, actor: a{Name: "Salvage", Params: 0<<28 | 0<<20 | 0x07<<4}},
		{name: "salvage, chart 129", room: 23, actor: a{Name: "Salvage", Params: 0<<28 | 129<<20 | 0x07<<4}},
		{name: "salvage, room 0 without a map room", room: 0, actor: a{Name: "Salvage", Params: 1<<28 | 0x07<<4}},
		{name: "salvage, room 0 map room 65", room: 0, actor: a{Name: "Salvage", Params: 1<<28 | 65<<12 | 0x07<<4}},
		{name: "salvage, stage file", room: -1, actor: a{Name: "Salvage", Params: 1<<28 | 0x07<<4}},
		// Outset's dig piece on the owner's disc: item 7, item bit 2, no
		// enemy kind or switch.
		{name: "dig piece", actor: a{Name: "TagKb", Params: 0xFFFF0207}, ok: true, kind: Dig, item: 7, flag: item(2)},
		{name: "dig, enemy kind only", actor: a{Name: "TagKb", Params: 0xE101FFFF}},
		{name: "dig, red rupee on a switch", actor: a{Name: "TagKb", Params: 0x05FF0304}, ok: true, kind: Dig, item: 4, flag: item(3)},
		{name: "dig, bit 0xFF is none", actor: a{Name: "TagKb", Params: 0xFFFFFF07}, ok: true, kind: Dig, item: 7, flag: none},
		{name: "dig, bit 0x1F", actor: a{Name: "TagKb", Params: 0xFFFF1F07}, ok: true, kind: Dig, item: 7, flag: item(0x1F)},
		{name: "dig, zone bit", actor: a{Name: "TagKb", Params: 0xFFFF4507}, ok: true, kind: Dig, item: 7, flag: Flag{Kind: ZoneItemFlag, Room: 2, Bit: 0x45}},
		{name: "dig, blue jelly's bit is a switch", actor: a{Name: "TagKb", Params: 0xFFFF054B}, ok: true, kind: Dig, item: 0x4B, flag: none},
		// The spot's bit is an s8 that _create checks unmasked
		// (d_a_tag_kb_item.cpp:11, :42), so a negative one halts.
		{name: "dig, bit 0x80 halts", actor: a{Name: "TagKb", Params: 0xFFFF8007}, ok: true, kind: Dig, item: 7, flag: Flag{Kind: BadFlag, Bit: 0x80}},
		{name: "dig, bit 0xC5 halts", actor: a{Name: "TagKb", Params: 0xFFFFC507}, ok: true, kind: Dig, item: 7, flag: Flag{Kind: BadFlag, Bit: 0xC5}},
		{name: "dig, bit 0x50 halts", actor: a{Name: "TagKb", Params: 0xFFFF5007}, ok: true, kind: Dig, item: 7, flag: Flag{Kind: BadFlag, Bit: 0x50}},
		{name: "dig, blue jelly's bad bit still halts", actor: a{Name: "TagKb", Params: 0xFFFF904B}, ok: true, kind: Dig, item: 0x4B, flag: Flag{Kind: BadFlag, Bit: 0x90}},
		{name: "dig, blue jelly's bit 0x60 halts", actor: a{Name: "TagKb", Params: 0xFFFF604B}, ok: true, kind: Dig, item: 0x4B, flag: Flag{Kind: BadFlag, Bit: 0x60}},
		{name: "salvage chest", actor: a{Name: "STBox", Params: 0x0107}, ok: true, kind: SalvageChest, item: 7, flag: none},
		{name: "scaled chunk", tag: "SCOB", actor: a{Name: "takara", Params: 1 << 7, Angle: z(0x1500)}, ok: true, kind: Chest, item: 0x15, flag: Flag{Kind: TboxFlag, Table: 3, Bit: 1}},
		{name: "layer chunk", tag: "ACT4", actor: a{Name: "takara", Params: 1 << 7, Angle: z(0x1500)}, ok: true, kind: Chest, item: 0x15, flag: Flag{Kind: TboxFlag, Table: 3, Bit: 1}, layerN: 4},
		{name: "unknown name", actor: a{Name: "Nobody", Params: 0x15}},
		{name: "known name, other class", actor: a{Name: "Orphan", Params: 0x15}},
	}
	names := testNames(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tag, rno := c.tag, c.room
			if tag == "" {
				tag = "ACTR"
			}
			if rno == 0 && !strings.Contains(c.name, "room 0") {
				rno = 2
			}
			chunk := stagetest.Actors(tag, c.actor)
			var s stagetest.Stage
			if rno < 0 {
				s = stage("D_Test", 3, []stagetest.Chunk{chunk})
			} else {
				s = stage("D_Test", 3, nil, room(rno, chunk))
			}
			got := Find(load(t, s), names, all)
			if !c.ok {
				if len(got) != 0 {
					t.Fatalf("Find = %+v, want nothing", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("Find = %+v, want one placement", got)
			}
			p := got[0]
			layer := stagedata.AllLayers
			if c.layerN != 0 {
				layer = c.layerN
			}
			if p.Kind != c.kind || p.Item != c.item || p.Flag != c.flag || p.Stage != "D_Test" || p.Room != rno ||
				p.Tag != tag || p.Layer != layer || p.Name != c.actor.Name {
				t.Errorf("got %+v\nwant kind %s item 0x%02X flag %+v room %d tag %s layer %d", p, c.kind, c.item, c.flag, rno, tag, layer)
			}
		})
	}
}

// Every decoder survives every parameter word and angle: no panic, and an
// item the table semantics allow.
func TestDecodeAllBits(t *testing.T) {
	s := &stagedata.Stage{Name: "x", SaveTable: 3}
	for proc, d := range decoders {
		for _, prm := range []uint32{0, 0xFFFFFFFF, 0x80000000, 0x7FFFFFFF, 0x12345678, 0xFEDCBA98} {
			for _, z := range []int16{0, -1, -0x8000, 0x7FFF, 0x1234} {
				for _, rno := range []int{-1, 0, 1, 63} {
					p := stagedata.Placed{Room: rno, Actor: stagedata.Actor{Params: prm, Angle: [3]int16{z, z, z}}}
					pl, ok := Decode(s, p, proc)
					if !ok {
						continue
					}
					if pl.Kind != d.kind || pl.Proc != proc {
						t.Fatalf("%v: %+v", proc, pl)
					}
					switch d.kind {
					case Chest, FieldItem, RaceItem, Salvage, SalvageChest, Dig:
						if pl.Item == ItemNone {
							t.Errorf("%v prm %08x: item none placed", proc, prm)
						}
					default:
						if pl.Item >= 0x20 {
							t.Errorf("%v prm %08x: table item 0x%02X placed", proc, prm, pl.Item)
						}
					}
				}
			}
		}
	}
}

func TestGroup(t *testing.T) {
	type a = stagetest.Actor
	key := func(bit uint32) a { return a{Name: "takara", Params: bit << 7, Angle: [3]int16{0, 0, 0x1500}} }
	stages := load(t,
		stage("D_One", 3, []stagetest.Chunk{stagetest.Actors("ACTR", key(1))},
			room(0, stagetest.Actors("ACTR", key(2)), stagetest.Actors("ACT1", key(2)), stagetest.Actors("ACT2", key(2))),
			room(1,
				stagetest.Actors("ACTR",
					a{Name: "item", Params: 0x7F<<8 | 0x15}, a{Name: "item", Params: 0x7F<<8 | 0x15}, // unsaved: two sources
					a{Name: "Kmtub", Params: 2<<16 | 0x15},                           // memory item bit 2, not tbox bit 2
					a{Name: "takara", Params: 2 << 7, Angle: [3]int16{0, 0, 0x0700}}, // same bit, another item
				)),
		),
		stage("D_Two", 3, nil, room(0, stagetest.Actors("ACTR", key(1)))), // the same table and bit as D_One's
		stage("D_Six", 6, nil, room(0, stagetest.Actors("ACTR", key(1)))), // another table
	)
	got := Group(Find(stages, testNames(t), all))
	type want struct {
		item uint8
		flag string
		at   int
	}
	exp := []want{
		{0x15, "tbox 3:1", 2},
		{0x15, "tbox 3:2", 3},
		{0x15, "unsaved", 1},
		{0x15, "unsaved", 1},
		{0x15, "item 3:0x02", 1},
		{0x07, "tbox 3:2", 1},
		{0x15, "tbox 6:1", 1},
	}
	if len(got) != len(exp) {
		t.Fatalf("Group = %d sources, want %d: %+v", len(got), len(exp), got)
	}
	for i, w := range exp {
		if g := got[i]; g.Item != w.item || g.Flag.String() != w.flag || len(g.At) != w.at {
			t.Errorf("source %d = %+v, want item 0x%02X flag %s at %d places", i, g, w.item, w.flag, w.at)
		}
	}
	if at := got[0].At; at[0].Stage != "D_One" || at[0].Room != -1 || at[1].Stage != "D_Two" {
		t.Errorf("first source's places: %+v", at)
	}
	if at := got[1].At; at[0].Layer != stagedata.AllLayers || at[1].Layer != 1 || at[2].Layer != 2 {
		t.Errorf("layered source's places: %+v", at)
	}
}

func TestFindFilters(t *testing.T) {
	stages := load(t, stage("D_One", 3, nil, room(0, stagetest.Actors("ACTR",
		stagetest.Actor{Name: "item", Params: 0x7F<<8 | 0x15},
		stagetest.Actor{Name: "item", Params: 0x7F<<8 | 0x07},
	))))
	got := Find(append(stages, nil), testNames(t), func(i uint8) bool { return i == ItemHeartPiece })
	if len(got) != 1 || got[0].Item != ItemHeartPiece || got[0].Index != 1 {
		t.Errorf("Find(heart pieces) = %+v", got)
	}
}

func TestStrings(t *testing.T) {
	w := Where{Stage: "M_NewD2", Room: 3, Layer: stagedata.AllLayers, Tag: "ACTR", Index: 12, Name: "takara", SetID: 0x1C}
	if got, want := w.String(), `M_NewD2 room 3, all layers, ACTR#12 "takara" setID 0x001C`; got != want {
		t.Errorf("Where.String = %q, want %q", got, want)
	}
	w.Room, w.Layer = -1, 2
	if got, want := w.String(), `M_NewD2 stage file, layer 2, ACTR#12 "takara" setID 0x001C`; got != want {
		t.Errorf("Where.String = %q, want %q", got, want)
	}
	for f, want := range map[Flag]string{
		{}:                                       "unsaved",
		{Kind: TboxFlag, Table: 3, Bit: 5}:       "tbox 3:5",
		{Kind: ItemFlag, Table: 4, Bit: 0x2A}:    "item 4:0x2A",
		{Kind: ZoneItemFlag, Room: 2, Bit: 0x41}: "zone item room 2:0x41 (unsaved)",
		{Kind: ChartFlag, Bit: 12}:               "chart 12",
		{Kind: OceanFlag, Room: 23, Bit: 4}:      "ocean room 23:4",
		{Kind: SalvageEventFlag, Bit: 3}:         "salvage event label 3 (game-cleared)",
		{Kind: StageLifeFlag, Table: 2}:          "stage life 2",
		{Kind: BadFlag, Bit: 0x55}:               "bad item bit 0x55 (halts)",
		{Kind: HaltFlag, Room: 50, Bit: 4}:       "ocean grid 50:4 (halts)",
		{Kind: "other"}:                          `flag "other"`,
	} {
		if got := f.String(); got != want {
			t.Errorf("%+v.String() = %q, want %q", f, got, want)
		}
	}
	saved := map[FlagKind]bool{TboxFlag: true, ItemFlag: true, ChartFlag: true, OceanFlag: true, StageLifeFlag: true}
	halts := map[FlagKind]bool{BadFlag: true, HaltFlag: true}
	for _, k := range []FlagKind{NoFlag, TboxFlag, ItemFlag, ZoneItemFlag, ChartFlag, OceanFlag, SalvageEventFlag, StageLifeFlag, BadFlag, HaltFlag} {
		if got := (Flag{Kind: k}).Saved(); got != saved[k] {
			t.Errorf("%q Saved = %v", k, got)
		}
		if got := (Flag{Kind: k}).Halts(); got != halts[k] {
			t.Errorf("%q Halts = %v", k, got)
		}
	}
}
