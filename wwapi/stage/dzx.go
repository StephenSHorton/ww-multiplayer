package stage

import (
	"encoding/binary"
	"fmt"
	"math"
	"slices"
)

// DZS (a stage's stage.dzs) and DZR (a room's room.dzr) are chunk files
// with one layout, all big-endian (dStage_fileHeader, d_stage.h:36-39):
//
//	0x00  s32 chunk count
//	0x04  that many chunk headers, 0xC bytes each (dStage_nodeHeader,
//	      d_stage.h:29-33):
//	      0x0  four-character tag, such as "ACTR"
//	      0x4  s32 entry count
//	      0x8  u32 offset of the entries from the start of the file
//
// Loading a file, the game turns every nonzero offset into a pointer
// (dStage_dt_c_offsetToPtr, d_stage.cpp:2046-2056), then walks a table of
// tags for the kind of file and calls each tag's function on the first
// chunk with that tag, comparing all four bytes (dStage_dt_c_decode,
// d_stage.cpp:2024-2043, which breaks out after the first match). A later
// chunk with the same tag is never read, so ParseDZx reads only the first.
//
// The chunks that place actors, the same set for a stage and a room
// (dStage_dt_c_stageLoader, d_stage.cpp:2154-2177; dStage_dt_c_roomReLoader,
// :2201-2211), in the order the game creates them:
//
//	ACTR TGOB TRES        0x20-byte entries, created by dStage_actorInit
//	                      (:1688-1709; TRES through dStage_stageTresureInit
//	                      :1973-1978 and dStage_roomTresureInit :1981-1985)
//	TGSC SCOB DOOR TGDR   0x24-byte entries, created by dStage_tgscInfoInit
//	                      (:1712-1730; TGDR through dStage_stageDrtgInfoInit
//	                      :2001-2006 and dStage_roomDrtgInfoInit :2009-2013)
//
// then the layer chunks, of which layerLoader reads only one layer's:
// ACTn and TREn (0x20 bytes, dStage_actorInit; dStage_layerTresureInit
// :1988-1991) and SCOn (0x24 bytes, dStage_tgscInfoInit), for the layer n
// that dComIfG_play_c::getLayerNo picks (0-11, written 0-9, a, b;
// d_stage.cpp:2071-2151; d_com_inf_game.cpp:185-269). There is no TGOn or
// any other layer variant. The stage's own layer chunks are read with
// room -1 (d_stage.cpp:2176).
//
// An 0x20-byte entry is stage_actor_data_class (d_stage.h:193-196; TRES's
// stage_tresure_data_class, :55-58, is the same): name[8], then
// fopAcM_prmBase_class (f_op_actor_mng.h:38-43). An 0x24-byte entry is
// stage_tgsc_data_class (d_stage.h:204-208): the same plus
// fopAcM_prmScale_class (f_op_actor_mng.h:45-49) and a pad byte.
//
//	0x00  char name[8], looked up in l_objectName (objname.go)
//	0x08  u32 parameters
//	0x0C  f32 position x, y, z
//	0x18  s16 angle x, y, z
//	0x1E  u16 setID
//	0x20  u8 scale x, y, z (0x24-byte entries only)
//
// dStage_actorInit skips an entry whose setID its room has recorded dead
// (dComIfGs_isActor, d_stage.cpp:1693); dStage_tgscInfoInit copies the
// setID but never checks it (:1721).
//
// STAG is read from the stage's file only (dStage_dt_c_stageInitLoader,
// d_stage.cpp:2059-2068; the room tables have no STAG, :2181-2193): its
// one entry at the chunk offset, whatever the count says
// (dStage_stagInfoInit, :1670-1678).
//
// PLYR, the player starts, has 0x20-byte entries like ACTR and is read
// from both kinds of file (dStage_dt_c_stageLoader, d_stage.cpp:2157;
// dStage_dt_c_roomLoader, :2182) by dStage_playerInit (:1428-1475), which
// creates Link from the first PLYR chunk the play scene decodes: once Link
// exists it returns at once (:1432). stage.dzs is decoded before any room,
// so a stage with a PLYR chunk always starts Link from it; otherwise the
// start room's room.dzr does. A start point >= 0 picks the entry whose
// (u8)angle.z equals it (Actor.StartPoint) and JUT_ASSERTs that one does
// (:1455-1463), even in an empty chunk, and JUT_ASSERT is not compiled
// out of the retail game (JUTAssert.h:9): a point the list lacks halts it.
// The matched entry's room (Actor.StartRoom) then becomes the start room
// (:1479-1480). For stage.dzs's starts that happens before any room is
// loaded, so the game loads that room (dStage_Create, :2231-2232) and the
// room the stage change asked for is ignored.
//
// EVNT, the stage's event list, has 0x18-byte entries (dStage_Event_dt_c,
// d_stage.h:329-340) and is read from stage.dzs only: dStage_eventInfoInit
// (d_stage.cpp:1798-1802) stores it on the stage, and a room's
// setEventInfo asserts (d_stage.h:757-761). A PLYR entry's start event
// indexes it (setStartDemo, d_event_manager.cpp:24-48).
const (
	dzxHeaderSize     = 4
	dzxChunkSize      = 0xC
	actorEntrySize    = 0x20
	scaledEntrySize   = 0x24
	stagSize          = 0x20
	eventEntrySize    = 0x18
	stagTag           = "STAG"
	playerTag         = "PLYR"
	eventTag          = "EVNT"
	layerTagSuffixes  = "0123456789ab"
	stagTypeShift     = 16
	stagTypeMask      = 7
	stagPropKeyHUD    = 0x01
	stagPropSaveShift = 1
	stagPropSaveMask  = 0x7F
)

const (
	// NumLayers is the number of layers, ACT0..ACTb (d_stage.cpp:2144-2148).
	NumLayers = 12
	// AllLayers is the Actor.Layer of an entry every layer creates.
	AllLayers = -1
)

// placeTag is a chunk that places actors.
type placeTag struct {
	tag    string
	layer  int  // AllLayers or 0-11
	scaled bool // 0x24-byte entries, created by dStage_tgscInfoInit
}

// placeTags lists the actor chunks in the order the game creates them
// (see above): the every-layer chunks, then each layer's ACTn, SCOn, TREn.
var placeTags = func() []placeTag {
	t := []placeTag{
		{"ACTR", AllLayers, false}, {"TGOB", AllLayers, false}, {"TRES", AllLayers, false},
		{"TGSC", AllLayers, true}, {"SCOB", AllLayers, true}, {"DOOR", AllLayers, true}, {"TGDR", AllLayers, true},
	}
	for n := range NumLayers {
		c := layerTagSuffixes[n : n+1]
		t = append(t, placeTag{"ACT" + c, n, false}, placeTag{"SCO" + c, n, true}, placeTag{"TRE" + c, n, false})
	}
	return t
}()

// DZx is a parsed DZS or DZR file. Its methods never modify it.
type DZx struct {
	Chunks []Chunk // the whole chunk table in file order, every tag, read or not
	Stag   *Stag   // the first STAG chunk's entry; nil when there is none
	Actors []Actor // the actor chunks' entries, every layer, in creation order
	// Players is the first PLYR chunk's entries, the player starts
	// (Layer AllLayers); empty when it has none, nil when there is no PLYR
	// chunk (HasPlayers).
	Players []Actor
	// Events is the first EVNT chunk's entries, the stage's event list;
	// nil when there is none.
	Events []Event
}

// Event is one EVNT entry (dStage_Event_dt_c, d_stage.h:329-340).
type Event struct {
	Index int // its index in the chunk, which a start event names
	// Name is mName[15] up to its first NUL. It starts at +0x01, right
	// after the u8 at +0x00; d_stage.h's comment says 0x04, but the
	// disc's names ("kugutu_ganon") start at +0x01.
	Name string
	// SpawnSwitch is mSpawnSwitchNo (+0x13): when a start event names
	// the entry, setStartDemo skips the event if the switch is on and
	// turns it on otherwise (d_event_manager.cpp:41-47). 0xFF is none.
	SpawnSwitch uint8
}

// Chunk is one chunk header.
type Chunk struct {
	Tag    string
	Count  int    // s32 entry count
	Offset uint32 // from the start of the file
}

// Actor is one entry of an actor chunk.
type Actor struct {
	Tag    string     // its chunk: "ACTR", "DOOR", "SCO3", ...
	Layer  int        // AllLayers, or the layer (0-11) whose chunk holds it
	Index  int        // its index in the chunk
	Name   string     // name[8] up to its first NUL; all 8 bytes when it has none
	Params uint32     // fopAcM_prmBase_class parameters
	Pos    [3]float32 // position x, y, z
	Angle  [3]int16   // angle x, y, z
	SetID  uint16     // the actor's setID (fopAc_ac_c +0x1BC once created)
	Scale  [3]uint8   // scale x, y, z; zero unless Scaled
	Scaled bool       // a 0x24-byte entry: TGSC, SCOB, DOOR, TGDR or SCOn
}

// DeadChecked reports whether the game leaves the entry out once its
// setID is recorded dead in its room: true for the 0x20-byte entries
// dStage_actorInit creates (d_stage.cpp:1693), false for the 0x24-byte
// ones dStage_tgscInfoInit creates without the check.
func (a Actor) DeadChecked() bool { return !a.Scaled }

// OnLayer reports whether the game creates the entry when its file is
// read on layer layer (0-11).
func (a Actor) OnLayer(layer int) bool { return a.Layer == AllLayers || a.Layer == layer }

// StartPoint is the start point a PLYR entry answers to: the low byte of
// its angle z (dStage_playerInit, d_stage.cpp:1456). The high byte is
// something else (the ship's spawn point, 0xFF for none).
func (a Actor) StartPoint() int { return int(uint8(a.Angle[2])) }

// StartRoom is the room a PLYR entry starts Link in: its parameters' low
// 6 bits, which dStage_playerInit makes the start room
// (d_stage.cpp:1479-1480).
func (a Actor) StartRoom() int { return int(a.Params & 0x3F) }

// Stag is a stage's STAG entry (stage_stag_info_class, d_stage.h:66-77).
type Stag struct {
	Near, Far     float32 // 0x00, 0x04 mNearPlane, mFarPlane
	CameraType    uint8   // 0x08 mCameraMapToolID
	Prop          uint8   // 0x09 mProp: key HUD bit 0, save table bits 1-7
	ParticleScene uint16  // 0x0A mParticleSceneNo
	TypeAndSchbit uint32  // 0x0C mStageTypeAndSchbit: stage type in bits 16-18
	SchbitAndFar  uint32  // 0x10 mSchbitEnableAndFarPlane
	Rest          [3]uint32
}

// SaveTable is the save table (dSv_memory_c index) the stage's switches
// and keys live in (dStage_stagInfo_GetSaveTbl, d_stage.h:1027-1029).
func (s Stag) SaveTable() int { return int(s.Prop>>stagPropSaveShift) & stagPropSaveMask }

// KeyHUD reports whether the stage shows the small-key count
// (dStage_stagInfo_ChkKeyDisp, d_stage.h:1023-1025).
func (s Stag) KeyHUD() bool { return s.Prop&stagPropKeyHUD != 0 }

// StageType is the stage's type (dStage_stagInfo_GetSTType,
// d_stage.h:1031-1033).
func (s Stag) StageType() StageType {
	return StageType((s.TypeAndSchbit >> stagTypeShift) & stagTypeMask)
}

// StageType is a STAG stage type (enum StageType, d_stage.h:16-26). The
// enum's ninth value, 8, can't come out of the three-bit field.
type StageType uint8

const (
	StageOutdoors   StageType = iota // dStageType_OUTDOORS_e
	StageDungeon                     // dStageType_DUNGEON_e
	StageMisc                        // dStageType_MISC_e: mostly interiors
	StageBoss                        // dStageType_BOSS_e
	StageSubDungeon                  // dStageType_SUBDUNGEON_e
	StageFF1                         // dStageType_FF1_e
	StageMiniBoss                    // dStageType_MINIBOSS_e
	StageSea                         // dStageType_SEA_e
)

var stageTypeNames = [...]string{"outdoors", "dungeon", "misc", "boss", "subdungeon", "FF1", "miniboss", "sea"}

func (t StageType) String() string {
	if int(t) < len(stageTypeNames) {
		return stageTypeNames[t]
	}
	return fmt.Sprintf("StageType(%d)", uint8(t))
}

// ParseDZx parses the DZS or DZR file b. Every chunk header must lie
// inside b; for the chunks it reads (STAG, the actor chunks, PLYR and
// EVNT) so must their entries, a nonzero count needs a nonzero offset
// (the game would follow a null pointer), and a negative count is an
// error. The entries of all the read chunks together may not hold more
// bytes than b has: a real file stores each chunk once, and a crafted one
// whose chunks overlap could otherwise multiply what parsing it
// allocates. Chunks the game doesn't read for placement are listed in
// Chunks but not checked.
func ParseDZx(b []byte) (*DZx, error) {
	be := binary.BigEndian
	if len(b) < dzxHeaderSize {
		return nil, fmt.Errorf("dzx: header truncated (%d bytes)", len(b))
	}
	n := int32(be.Uint32(b))
	if n < 0 {
		return nil, fmt.Errorf("dzx: negative chunk count %d", n)
	}
	if uint64(dzxHeaderSize)+uint64(n)*dzxChunkSize > uint64(len(b)) {
		return nil, fmt.Errorf("dzx: %d chunk headers run past the end (%d bytes)", n, len(b))
	}
	str := string(b) // one copy; every tag and name is a slice of it
	d := &DZx{Chunks: make([]Chunk, n)}
	first := make(map[string]int, len(placeTags)+1) // tag -> first chunk with it
	for i := range d.Chunks {
		h := dzxHeaderSize + i*dzxChunkSize
		d.Chunks[i] = Chunk{
			Tag:    str[h : h+4],
			Count:  int(int32(be.Uint32(b[h+4:]))),
			Offset: be.Uint32(b[h+8:]),
		}
		if _, ok := first[d.Chunks[i].Tag]; !ok {
			first[d.Chunks[i].Tag] = i
		}
	}

	// entries returns chunk i's count*size bytes of entries, checked.
	budget := uint64(len(b))
	entries := func(i int, size uint64) (int, error) {
		c := d.Chunks[i]
		switch {
		case c.Count < 0:
			return 0, fmt.Errorf("dzx: chunk %d (%q) has a negative entry count %d", i, c.Tag, c.Count)
		case c.Count == 0:
			return 0, nil
		case c.Offset == 0:
			return 0, fmt.Errorf("dzx: chunk %d (%q) has %d entries at offset 0", i, c.Tag, c.Count)
		}
		total := uint64(c.Count) * size
		if uint64(c.Offset)+total > uint64(len(b)) {
			return 0, fmt.Errorf("dzx: chunk %d (%q): %d entries of 0x%x bytes at 0x%x run past the end (%d bytes)",
				i, c.Tag, c.Count, size, c.Offset, len(b))
		}
		if total > budget {
			return 0, fmt.Errorf("dzx: chunk %d (%q): the actor chunks' entries add up to more bytes than the file's %d; do chunks overlap?",
				i, c.Tag, len(b))
		}
		budget -= total
		return c.Count, nil
	}

	if i, ok := first[stagTag]; ok {
		c := d.Chunks[i]
		if c.Offset == 0 || uint64(c.Offset)+stagSize > uint64(len(b)) {
			return nil, fmt.Errorf("dzx: STAG entry at 0x%x is null or runs past the end (%d bytes)", c.Offset, len(b))
		}
		e := b[c.Offset:]
		d.Stag = &Stag{
			Near:          math.Float32frombits(be.Uint32(e[0x00:])),
			Far:           math.Float32frombits(be.Uint32(e[0x04:])),
			CameraType:    e[0x08],
			Prop:          e[0x09],
			ParticleScene: be.Uint16(e[0x0A:]),
			TypeAndSchbit: be.Uint32(e[0x0C:]),
			SchbitAndFar:  be.Uint32(e[0x10:]),
			Rest:          [3]uint32{be.Uint32(e[0x14:]), be.Uint32(e[0x18:]), be.Uint32(e[0x1C:])},
		}
	}

	for _, pt := range placeTags {
		i, ok := first[pt.tag]
		if !ok {
			continue
		}
		size := uint64(actorEntrySize)
		if pt.scaled {
			size = scaledEntrySize
		}
		count, err := entries(i, size)
		if err != nil {
			return nil, err
		}
		off := int(d.Chunks[i].Offset)
		d.Actors = slices.Grow(d.Actors, count)
		for k := range count {
			d.Actors = append(d.Actors, readActor(b, str, off+k*int(size), pt.tag, pt.layer, k, pt.scaled))
		}
	}

	if i, ok := first[playerTag]; ok {
		count, err := entries(i, actorEntrySize)
		if err != nil {
			return nil, err
		}
		d.Players = make([]Actor, 0, count)
		off := int(d.Chunks[i].Offset)
		for k := range count {
			d.Players = append(d.Players, readActor(b, str, off+k*actorEntrySize, playerTag, AllLayers, k, false))
		}
	}

	if i, ok := first[eventTag]; ok {
		count, err := entries(i, eventEntrySize)
		if err != nil {
			return nil, err
		}
		d.Events = make([]Event, 0, count)
		off := int(d.Chunks[i].Offset)
		for k := range count {
			e := off + k*eventEntrySize
			d.Events = append(d.Events, Event{Index: k, Name: cName(str[e+0x01 : e+0x10]), SpawnSwitch: b[e+0x13]})
		}
	}
	return d, nil
}

// readActor reads the entry at off of b (str is b as a string), checked
// to fit by the caller.
func readActor(b []byte, str string, off int, tag string, layer, index int, scaled bool) Actor {
	be := binary.BigEndian
	e := b[off:]
	a := Actor{
		Tag: tag, Layer: layer, Index: index,
		Name:   cName(str[off:][:8]),
		Params: be.Uint32(e[0x08:]),
		Pos: [3]float32{
			math.Float32frombits(be.Uint32(e[0x0C:])),
			math.Float32frombits(be.Uint32(e[0x10:])),
			math.Float32frombits(be.Uint32(e[0x14:])),
		},
		Angle:  [3]int16{int16(be.Uint16(e[0x18:])), int16(be.Uint16(e[0x1A:])), int16(be.Uint16(e[0x1C:]))},
		SetID:  be.Uint16(e[0x1E:]),
		Scaled: scaled,
	}
	if scaled {
		a.Scale = [3]uint8{e[0x20], e[0x21], e[0x22]}
	}
	return a
}

// HasPlayers reports whether the file has a PLYR chunk: whether the game
// starts Link from it when it is the first such file decoded.
func (d *DZx) HasPlayers() bool { return d.Players != nil }

// OnLayer returns the entries the game creates reading the file on layer
// layer (0-11): every-layer chunks' and that layer's, in creation order.
func (d *DZx) OnLayer(layer int) []Actor {
	var out []Actor
	for _, a := range d.Actors {
		if a.OnLayer(layer) {
			out = append(out, a)
		}
	}
	return out
}

// Layers returns the layers (0-11) that have at least one entry of their
// own, in order.
func (d *DZx) Layers() []int {
	var seen [NumLayers]bool
	for _, a := range d.Actors {
		if a.Layer >= 0 {
			seen[a.Layer] = true
		}
	}
	var out []int
	for n, s := range seen {
		if s {
			out = append(out, n)
		}
	}
	return out
}

// Chunk returns the first chunk tagged tag, the one the game reads.
func (d *DZx) Chunk(tag string) (Chunk, bool) {
	for _, c := range d.Chunks {
		if c.Tag == tag {
			return c, true
		}
	}
	return Chunk{}, false
}

// cName is the C string in the fixed-size field s: up to the first NUL.
// strcmp against an l_objectName entry (d_stage.cpp:1270) stops there too;
// a name filling all 8 bytes matches no entry, whose names have at most 7.
func cName(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return s[:i]
		}
	}
	return s
}
