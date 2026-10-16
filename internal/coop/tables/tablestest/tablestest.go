// Package tablestest builds the synthetic disc the coop-tables tests run
// on: a GZLE01 image (stagetest's ISO) whose main.dol carries an
// l_objectName of the names placed here, and stages shaped like the
// vanilla game where M1's acceptance looks (docs/08-coop-design.md §9
// M1): the vanilla small-key sources and key doors of each dungeon, 44
// heart pieces with the hearts Catalogue and 6 containers, a push block
// storing its path in switches, and enemies with setIDs, one key shared.
// No Nintendo data is in it; the entries are encoded as the table
// builders' own tests encode them (their decoders cite the decomp).
package tablestest

import (
	"encoding/binary"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// Names are the l_objectName entries the fixture places, with the
// d_stage.cpp line of each.
func Names() []stagetest.ObjectName {
	n := func(name string, p stagedata.Proc) stagetest.ObjectName {
		return stagetest.ObjectName{Name: name, Proc: int16(p), Subtype: -1}
	}
	return []stagetest.ObjectName{
		n("door10", stagedata.ProcDoor10),   // d_stage.cpp:451
		n("takara", stagedata.ProcTbox),     // :483
		n("SW_HIT0", stagedata.ProcSwhit0),  // :531
		n("osiBLK0", stagedata.ProcMovebox), // :541
		n("item", stagedata.ProcItem),       // :647
		n("c_green", stagedata.ProcCC),      // :681
		n("big_pow", stagedata.ProcBPW),     // :694
		n("Bk", stagedata.ProcBK),           // :811
		n("Btd", stagedata.ProcBTD),         // :843
		n("Bkm", stagedata.ProcBMD),         // :1187
		n("Bst", stagedata.ProcBST),         // :1188
		n("Bitem", stagedata.ProcBossItem),  // :1189
		n("Bwd", stagedata.ProcBWD),         // :1191
		n("TagKb", itemsrc.ProcTagKbItem),   // :583
	}
}

// DOL is a main.dol carrying Names.
func DOL() []byte { return stagetest.MainDOL(Names()...) }

// Save tables the fixture uses beyond keys': Ganon's Tower's
// (dSv_save_c::STAGE_GT, d_save.h:897) and the developer test stages'
// (STAGE_TEST, d_save.h:904).
const (
	TableGT   = 0x8
	TableTest = 0xF
)

// Entries, encoded as the builders' tests encode them. Only enemies get a
// setID; everything else has 0xFFFF, which no zone records.
const noSetID = 0xFFFF

// KeyDoor is a door10 of type 4, the small-key lock (keys.DecodeDoor),
// with switch sw between rooms front and back.
func KeyDoor(sw uint32, front, back int) stagetest.Actor {
	return stagetest.Actor{Name: "door10", Params: 4<<8 | sw, Angle: [3]int16{int16(back<<6 | front), 0, 0}, SetID: noSetID}
}

// Chest is a chest of item recorded in tbox bit bit (itemsrc), with no
// open switch (0xFF).
func Chest(bit uint32, item uint8) stagetest.Actor {
	return stagetest.Actor{Name: "takara", Params: bit << 7, Angle: [3]int16{0, 0, int16(uint16(item)<<8 | 0xFF)}, SetID: noSetID}
}

// KeyChest is a small-key chest; PieceChest a heart-piece chest.
func KeyChest(bit uint32) stagetest.Actor   { return Chest(bit, itemsrc.ItemSmallKey) }
func PieceChest(bit uint32) stagetest.Actor { return Chest(bit, itemsrc.ItemHeartPiece) }

// DigPiece is a pig's dig spot of a heart piece recorded in item bit
// bit, with no enemy kind or switch (itemsrc's TagKb decoder).
func DigPiece(bit uint32) stagetest.Actor {
	return stagetest.Actor{Name: "TagKb", Params: 0xFFFF0000 | bit<<8 | uint32(itemsrc.ItemHeartPiece), SetID: noSetID}
}

// Boss is a boss entry; Bitem the heart container a beaten boss leaves,
// checking save table table; Crystal an untimed crystal switch setting
// sw (latching).
func Boss(name string) stagetest.Actor { return stagetest.Actor{Name: name, SetID: noSetID} }
func Bitem(table uint32) stagetest.Actor {
	return stagetest.Actor{Name: "Bitem", Params: table, SetID: noSetID}
}
func Crystal(sw uint32) stagetest.Actor {
	return stagetest.Actor{Name: "SW_HIT0", Params: 0xFF<<20 | sw, SetID: noSetID}
}

// Enemy is an enemy entry named name with setID setID.
func Enemy(name string, setID uint16) stagetest.Actor {
	return stagetest.Actor{Name: name, SetID: setID, Pos: [3]float32{100, 0, -50}}
}

// PushBlock is a push block storing path point path in switches lo and
// hi (switches' movebox decoder).
func PushBlock(path, lo, hi uint32) stagetest.Actor {
	return stagetest.Actor{Name: "osiBLK0", Params: 1<<24 | lo<<8, Angle: [3]int16{0, 0, int16(hi<<8 | path)}, SetID: noSetID}
}

// Stage is a stage of save table table and type typ, with its own
// entries dzs in an ACTR chunk and doors in a TGDR chunk, and rooms.
func Stage(name string, table int, typ stagedata.StageType, dzs, doors []stagetest.Actor, rooms ...stagetest.Room) stagetest.Stage {
	chunks := []stagetest.Chunk{stagetest.StagChunk(stagetest.StagInfo(table, int(typ), typ == stagedata.StageDungeon))}
	if len(dzs) > 0 {
		chunks = append(chunks, stagetest.Actors("ACTR", dzs...))
	}
	if len(doors) > 0 {
		chunks = append(chunks, stagetest.Actors("TGDR", doors...))
	}
	return stagetest.Stage{Name: name, DZS: stagetest.DZx(chunks...), Rooms: rooms, Yaz0: true}
}

// Room is room no holding entries in an ACTR chunk.
func Room(no int, entries ...stagetest.Actor) stagetest.Room {
	return stagetest.Room{No: no, DZR: stagetest.DZx(stagetest.Actors("ACTR", entries...))}
}

// Dungeon is one dungeon's stage: as many key doors (switches from 0x10)
// and key chests (tbox bits from 1, in room 1) as keys.Dungeons gives
// table, and the room-1 entries extra.
func Dungeon(name string, table int, extra ...stagetest.Actor) stagetest.Stage {
	n := 0
	for _, d := range keys.Dungeons {
		if d.Table == table {
			n = d.Keys
		}
	}
	var doors, chests []stagetest.Actor
	for i := range n {
		doors = append(doors, KeyDoor(0x10+uint32(i), i, i+1))
		chests = append(chests, KeyChest(1+uint32(i)))
	}
	return Stage(name, table, stagedata.StageDungeon, nil, doors, Room(0), Room(1, append(chests, extra...)...))
}

// Vanilla is the fixture whose acceptance checks all pass: 13 stages, of
// which the tables build from 12, with 12 rooms. The sea holds the
// stage-file heart pieces, one of them a pig's dig spot, and Forsaken
// Fortress's Bitem; each dungeon its keys and doors; each boss stage its
// boss, Dragon Roost's with the Bitem too. Dragon Roost Cavern's room 1
// also has a latching crystal, a push block and enemies, two of them on
// one key. Xboss0 is a Ganon's Tower refight, whose Gohma drops no
// container (hearts) but is still an enemy entry. K_Test2, on save table
// 15, is a developer test stage holding a piece, a key, a container and
// an enemy, all of which the tables leave out (Tables.Excluded). Last
// comes "Name", which the tables leave out too: like the real disc's, its
// Stage.arc has no stage.dzs, since only the name scene loads it
// (stagedata.Load).
func Vanilla() []stagetest.Stage {
	pieces := []stagetest.Actor{DigPiece(0x02)}
	for bit := uint32(0); len(pieces) < hearts.ExpectedPieces-len(hearts.Catalogue); bit++ {
		pieces = append(pieces, PieceChest(bit))
	}
	return []stagetest.Stage{
		Stage("sea", 0, stagedata.StageSea, nil, nil, Room(1, append(pieces, Bitem(2))...)),
		Dungeon("M_NewD2", keys.TableDRC,
			Crystal(0x20), PushBlock(1, 0x30, 0x31),
			Enemy("Bk", 1), Enemy("Bk", 1), Enemy("c_green", 2), Enemy("Bk", 0xFFFF)),
		Stage("M_DragB", keys.TableDRC, stagedata.StageDungeon, []stagetest.Actor{Boss("Btd")}, nil, Room(0, Bitem(keys.TableDRC))),
		Dungeon("kindan", keys.TableFW),
		Stage("kinBOSS", keys.TableFW, stagedata.StageDungeon, []stagetest.Actor{Boss("Bkm")}, nil),
		Dungeon("Siren", keys.TableTotG),
		Stage("SirenB", keys.TableTotG, stagedata.StageDungeon, []stagetest.Actor{Boss("Bst")}, nil),
		Dungeon("M_Dai", keys.TableET),
		Stage("M_DaiB", keys.TableET, stagedata.StageDungeon, []stagetest.Actor{Boss("big_pow")}, nil),
		Dungeon("kaze", keys.TableWT),
		Stage("kazeB", keys.TableWT, stagedata.StageDungeon, []stagetest.Actor{Boss("Bwd")}, nil),
		Stage("Xboss0", TableGT, stagedata.StageBoss, []stagetest.Actor{Boss("Btd")}, nil),
		Stage("K_Test2", TableTest, stagedata.StageOutdoors, nil, nil, Room(0,
			PieceChest(0), KeyChest(1), stagetest.Actor{Name: "item", Params: 0x7F<<8 | uint32(itemsrc.ItemHeartContainer), SetID: noSetID},
			Enemy("Bk", 7))),
		{Name: "Name"},
	}
}

// Others are files outside res/Stage that a real disc has and the
// tables never read.
var Others = []struct {
	Path string
	Size int
}{
	{"res/Object/Link.arc", 0x4000},
	{"Audiores/Stream/demo.afc", 0x10000},
	{"res/Msg/bmgres.arc", 0x800},
}

// Disc is an in-memory disc of stages and Others.
func Disc(stages []stagetest.Stage) *stagetest.Disc {
	d := new(stagetest.Disc)
	for _, o := range Others[:1] {
		d.Add(o.Path, make([]byte, o.Size))
	}
	for _, s := range stages {
		d.AddStage(s)
	}
	for _, o := range Others[1:] {
		d.Add(o.Path, make([]byte, o.Size))
	}
	return d
}

// ISO is a disc image of Disc(stages) with DOL as its main.dol.
func ISO(stages []stagetest.Stage) []byte { return Disc(stages).ISO(DOL()) }

// PatchableDOL is a main.dol carrying Names that inject.PatchDOL can patch,
// which DOL is too short for: its text section, a blr and zeros, runs past
// every in-DOL patch site of this build's blob (inject.DOLPatches), and
// l_objectName follows in its data section, which no patch site reaches.
// So patching it changes the DOL's bytes, and so its hash and its cache
// file, but not the tables read from it.
func PatchableDOL() []byte {
	const textAddr, textOff, before, after = 0x80003100, 0x100, 0x18, 0x24
	end := uint32(textOff + 0x20)
	for _, p := range inject.DOLPatches {
		end = max(end, p.FileOff+uint32(len(p.Bytes)))
	}
	textSize := (end - textOff + 0x1F) &^ 0x1F
	data := make([]byte, before, before+stagetest.ObjectNameCount*0xC+after)
	data = append(data, stagetest.ObjectNameTable(Names()...)...)
	data = append(data, make([]byte, after)...)
	dataOff := textOff + textSize
	out := make([]byte, int(dataOff)+len(data))
	be := binary.BigEndian
	be.PutUint32(out[0x00:], textOff)
	be.PutUint32(out[0x48:], textAddr)
	be.PutUint32(out[0x90:], textSize)
	be.PutUint32(out[0x1C:], dataOff)
	be.PutUint32(out[0x64:], stagetest.ObjectNameAddr-before)
	be.PutUint32(out[0xAC:], uint32(len(data)))
	be.PutUint32(out[0xE0:], textAddr)
	be.PutUint32(out[textOff:], 0x4E800020) // blr
	copy(out[dataOff:], data)
	return out
}

// PatchableISO is a disc image of Disc(stages) with PatchableDOL as its
// main.dol: one `patch` accepts (inject.PatchISOInMemory). Zeros follow
// the DOL, room for the T2 section the patch appends to it (as `wit copy`
// leaves before the FST), since the files would otherwise start right
// where the patched DOL grows.
func PatchableISO(stages []stagetest.Stage) []byte {
	dol := PatchableDOL()
	return Disc(stages).ISO(append(dol, make([]byte, len(inject.T2Bytes)+0x100)...))
}
