package replica

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// screen is what a snapshot shows, as far as the differ cares.
type screen uint8

const (
	// screenOut: no stage, the title, file select, or no save table
	// loaded. card_to_memory, New Game's dComIfGs_init and the
	// second-quest ExDataSave all run here (d_s_name.cpp:1213, 1373;
	// d_file_select.cpp:3626-3646), so nothing seen before is a baseline
	// for what comes after.
	screenOut screen = iota
	// screenNoLink: a stage is named but Link's actor is gone: the
	// frames of a stage change between dStage_Delete and the new
	// stage's player (d_stage.cpp:2248-2261).
	screenNoLink
	// screenGame: a stage with Link and a valid save table.
	screenGame
)

func screenOf(s *snap.Snapshot) screen {
	switch {
	case s.StageName == "", s.StageName == addr.StageTitle, s.StageName == addr.StageFileSelect:
		return screenOut
	case !addr.ValidTable(int(s.Table)):
		// mDan.mStageNo is -1 from dSv_info_c::init (d_save.cpp:1392)
		// until the next stage load: boot or a New Game.
		return screenOut
	case s.PlayerPtr == 0:
		return screenNoLink
	}
	return screenGame
}

// fileKey is which save file is loaded, as the baselines see it. RAM's
// mDateIPL changes only when card_to_memory loads a file (status_b init
// zeroes it, d_save.cpp:51-56; memory_to_card stamps only the card
// buffer, d_save.cpp:1715-1717), so a New Game over the same slot and
// name still reads as another file.
type fileKey struct {
	slot, clearCount uint8
	name             [bind.PlayerNameSize]byte
	dateIPL          uint64
}

func keyOf(f bind.File) fileKey {
	return fileKey{slot: f.Slot, clearCount: f.ClearCount, name: f.Name, dateIPL: f.DateIPL}
}

// obs is one tick's snapshot, decoded once for the differ, the planner
// and the session.
type obs struct {
	s      *snap.Snapshot
	st     gate.Status
	file   bind.File
	key    fileKey
	screen screen
	// raw is the canonical image (snap.Snapshot.Image), local bytes
	// included; local is its shared bits (world.Shared).
	raw, local world.Image
	// stagTable is the save table the loaded STAG chunk names
	// (dStage_stagInfo_GetSaveTbl, d_stage.h:1027-1029), or -1 when its
	// pointer is null or unreadable.
	stagTable int
	// boss: the loaded stage polls its table's boss bits every frame
	// (bossStage).
	boss bool
	// rooms is the M5 receiver's probe of the rooms (readRoomProbe), read
	// in game only.
	rooms roomProbe
}

// observe decodes s. It reads a few more bytes from m: the STAG chunk's
// mProp and stage type, through mStageData.mpStagInfo, and in game the
// rooms' probe for the M5 receiver (readRoomProbe).
func observe(m mem.Mem, s *snap.Snapshot, st gate.Status) *obs {
	o := &obs{s: s, st: st, file: bind.FileOf(s), screen: screenOf(s), stagTable: -1}
	o.key = keyOf(o.file)
	o.raw = world.Image(s.Image())
	o.local = world.Shared(&o.raw)
	table, typ, ok := readStag(m, s.StagInfo)
	if ok {
		o.stagTable = table
	}
	o.boss = bossStage(typ, ok, s.Table)
	if o.screen == screenGame {
		o.rooms = readRoomProbe(m)
	}
	return o
}

// consistent reports whether o's canonical image can be trusted: Link is
// in a stage and the STAG chunk names the table mDan holds. A stage load
// sets mpStagInfo, then getSave copies the new table's memBit into the
// live copy, then initDan sets mDan.mStageNo (d_stage.cpp:1670-1677). A
// snapshot taken between getSave and initDan would fold the new table's
// bits into the old table's slot, and every one of them would look like a
// gain; between mpStagInfo and initDan the STAG table and mDan's differ,
// so such a tick is skipped. snap.Read reads the save before the stage
// block that holds mpStagInfo, and observe reads mProp after both, so a
// tick whose save read caught the new live copy also sees the new STAG.
func (o *obs) consistent() bool {
	return o.screen == screenGame && o.stagTable == int(o.s.Table)
}

// Edges are the rising edges one tick found.
type Edges struct {
	// Gains are the image bytes that gained bits (OR) or value (MAX)
	// since the last tick the differ diffed, within the policy, in
	// ascending offset order: GAINS c->s.
	Gains []world.Gain
	// Scoped are the dan, zone, room-edge and kill words that gained bits
	// in the scopes Link is in: SCOPED c->s.
	Scoped []wire.Scoped
	// Rebased: the image had no baseline, and this tick is the new one,
	// so whatever rose since the last diffed tick is in it unreported.
	// The replica follows it with a WORLD.
	Rebased bool
	// Waiting is what a paced Differ holds back after this tick, in
	// ascending offset order (Differ.Waiting): not in Gains yet.
	Waiting []world.Gain
	// Note says why the tick was skipped or what was re-baselined, or ""
	// for a plain diff.
	Note string
}

// Differ turns successive snapshots into rising edges (§4.2): bits that
// were clear at the last look and are set now. It never reports a
// cleared bit, so the game's bulk resets (getSave and initDan at a stage
// load, clearRoomSwitch at every room change, removeZone) cannot
// propagate, and it re-baselines, emitting nothing, wherever a reset
// could make bits appear that the player did not set.
//
// The image (the canonical dSv_save_c with the current table's live
// memBit folded in) keeps its baseline across room, stage and table
// changes: putSave and getSave move the current table's bits between its
// live and saved copies (d_save.cpp:1506-1515) and the fold sees the
// same bits either way, while initDan, clearRoomSwitch and removeZone
// never touch the image. So a bit the game sets as Link walks through a
// door, such as the new room's visited bit (dMap_c::mapMoveAll sets it
// in every stage but the sea and Forsaken Fortress types, d_map.cpp:
// 1662-1665), is still sent. The image baseline is dropped:
//
//   - off the game (no stage, title, file select, no save table), where
//     card_to_memory, New Game and ExDataSave rewrite the save;
//   - when another file is loaded (fileKey);
//   - when the heartbeat goes backwards: a save state was loaded (§5.5);
//   - when a snapshot can't be read.
//
// The tick that takes the new baseline reports no gains but says Rebased:
// bits that rose in the gap are in the baseline, so the replica sends the
// whole image (WORLD) after it rather than lose them.
//
// It is kept, but the tick skipped, while Link is between stages and on
// the tick a stage load is half done (obs.consistent).
//
// The scoped words are not in the image, and the resets do hit them, so
// they re-baseline on every change of scope: the dan words on a stage or
// table change (initDan, d_save.cpp:1225-1235), the current room's zone
// and room-edge words on a room change or a new zone slot
// (clearRoomSwitch, d_save.cpp:1273-1276; createZone, d_save.cpp:
// 1525-1537), each room's kill words when its zone is created, removed or
// reused (dSv_zone_c::init, d_save.cpp:1338-1342; removeZone,
// d_save.h:969). Zone state is read only through snap.Snapshot.ZoneFor,
// so a freed slot's stale bits are never read as a room's.
//
// From M4 a paced Differ (Pace) holds some of the image's edges back
// before it reports them, by the sender's rules of §6.5 and §6.6
// (differ_pace.go): the story bits and registers in batches, and the
// inventory until the game is IDLE.
//
// From M5 a sharing Differ (ShareScopes) proposes only the scoped bits
// the host's M1 verdict takes, and the latching dan and zone words whole
// on entering their scope and every second (differ_scoped.go).
//
// A Differ is not safe for concurrent use.
type Differ struct {
	policy *world.Policy

	have bool
	file fileKey
	base world.Image

	sc    scopeBase
	pace  pace
	share func(world.Key, int) uint32 // ShareScopes' filter; nil: M2's edges
	// hold is HoldScopes' filter, and taint the dan and zone bits it held
	// back as they rose (or as their scope was entered), by scope: masked
	// out of every SCOPED until the game clears them or Drop.
	hold  func(world.Key, int) uint32
	taint map[world.Key][2]uint32
}

// scopeBase is the scoped words' baselines.
type scopeBase struct {
	have  bool
	stage wire.Stage
	table int8
	dan   [2]uint32

	zoneHave bool
	room     int8 // the room zone holds
	slot     int
	zone     [3]uint16 // mSwitch[0..1] zone switches, mSwitch[2] room switches

	kills [addr.RoomCount]killBase

	// refresh counts the ticks until a sharing Differ proposes the dan
	// and zone words whole again (scopeRefresh).
	refresh int
}

type killBase struct {
	have  bool
	slot  int
	words [killWords]uint32
}

const killWords = addr.ZoneActorCount / 32

// NewDiffer returns a Differ that emits gains within policy (nil:
// world.DefaultPolicy).
func NewDiffer(policy *world.Policy) *Differ {
	if policy == nil {
		policy = world.DefaultPolicy()
	}
	return &Differ{policy: policy}
}

// Drop forgets every baseline, and what the pace holds back: the next good
// tick is a new baseline, after which the replica sends its whole WORLD.
func (d *Differ) Drop() {
	d.have = false
	d.sc = scopeBase{}
	d.taint = nil
	d.pace.drop()
}

// Has reports whether the image has a baseline.
func (d *Differ) Has() bool { return d.have }

// observe diffs one tick.
func (d *Differ) observe(o *obs) Edges {
	now := d.pace.now()
	if o.st.Regressed {
		d.Drop()
		return Edges{Note: "the heartbeat went backwards (a save state was loaded): baselines dropped"}
	}
	switch o.screen {
	case screenOut:
		d.Drop()
		return Edges{Note: "not in game: baselines dropped"}
	case screenNoLink:
		d.pace.quiet(now)
		return Edges{Note: "between stages: skipped", Waiting: d.Waiting()}
	}
	if !o.consistent() {
		d.pace.quiet(now)
		return Edges{Note: fmt.Sprintf("the loaded STAG names save table %d but mDan holds %d (a stage load in progress): skipped",
			o.stagTable, o.s.Table), Waiting: d.Waiting()}
	}
	var e Edges
	var gs []world.Gain
	switch {
	case d.have && d.file != o.key:
		d.Drop()
		fallthrough
	case !d.have:
		d.have, d.file, d.base = true, o.key, o.local
		e.Rebased, e.Note = true, "new baseline"
	default:
		gs = filterPolicy(world.Diff(&d.base, &o.local), d.policy)
		d.base = o.local
	}
	// A new baseline has nothing waiting (Drop), so its tick only keeps
	// the IDLE run going.
	e.Gains = d.pace.release(now, o.st.Idle, gs)
	e.Waiting = d.Waiting()
	e.Scoped = d.scoped(o.s)
	return e
}

// filterPolicy keeps the gains policy opens: the open bits of an OR or
// OR_MASKED gain, a MAX value whose every bit is open. gs is reused.
func filterPolicy(gs []world.Gain, p *world.Policy) []world.Gain {
	out := gs[:0]
	for _, g := range gs {
		allowed := p[g.Off]
		if class.Of(int(g.Off)).Class == class.Max {
			if g.Val&^allowed != 0 {
				continue
			}
		} else if g.Val &= allowed; g.Val == 0 {
			continue
		}
		out = append(out, g)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scoped diffs the scoped words of a consistent tick.
func (d *Differ) scoped(s *snap.Snapshot) []wire.Scoped {
	stage, err := wire.MakeStage(s.StageName)
	if err != nil {
		d.sc = scopeBase{}
		return nil
	}
	sc := &d.sc
	entered := !sc.have || sc.stage != stage || sc.table != s.Table
	if entered {
		*sc = scopeBase{have: true, stage: stage, table: s.Table}
		sc.dan = danWords(s)
	}
	// whole: a sharing Differ proposes the latching dan and zone words
	// whole this tick, not only their rises (differ_scoped.go).
	whole := d.share != nil && sc.due(entered)
	var out []wire.Scoped
	add := func(kind wire.ScopeKind, room int8, ws []wire.ScopedWord) {
		if ws = d.shared(kind, s, room, ws); len(ws) > 0 {
			out = append(out, wire.Scoped{Kind: kind, Stage: stage, Table: s.Table, Room: room, Words: ws})
		}
	}

	dan := danWords(s)
	if entered {
		d.taintRises(wire.ScopeDan, s, -1, [2]uint32{}, dan)
	} else {
		d.taintRises(wire.ScopeDan, s, -1, sc.dan, dan)
	}
	if whole {
		add(wire.ScopeDan, -1, rises32(make([]uint32, len(dan)), dan[:], 0))
	} else {
		add(wire.ScopeDan, -1, rises32(sc.dan[:], dan[:], 0))
	}
	sc.dan = dan

	if z, ok := s.ZoneFor(int(s.StayNo)); !ok {
		sc.zoneHave = false
	} else {
		zw := zoneWords(s, z)
		same := sc.zoneHave && sc.room == s.StayNo && sc.slot == z
		var was [2]uint32
		if same {
			was = [2]uint32{uint32(sc.zone[0]), uint32(sc.zone[1])}
		}
		d.taintRises(wire.ScopeZone, s, s.StayNo, was, [2]uint32{uint32(zw[0]), uint32(zw[1])})
		switch {
		case d.share != nil && (whole || !same):
			add(wire.ScopeZone, s.StayNo, rises16(make([]uint16, 2), zw[:2], 0))
		case same:
			add(wire.ScopeZone, s.StayNo, rises16(sc.zone[:2], zw[:2], 0))
		}
		if same {
			// The room switches only ever go as edges (§6.3).
			add(wire.ScopeRoomEdge, s.StayNo, rises16(sc.zone[2:], zw[2:], 2))
		}
		sc.zoneHave, sc.room, sc.slot, sc.zone = true, s.StayNo, z, zw
	}

	for room := range addr.RoomCount {
		kb := &sc.kills[room]
		z, ok := s.ZoneFor(room)
		if !ok {
			*kb = killBase{}
			continue
		}
		kw := killWordsOf(s, z)
		if kb.have && kb.slot == z {
			add(wire.ScopeKill, int8(room), rises32(kb.words[:], kw[:], 0))
		}
		*kb = killBase{have: true, slot: z, words: kw}
	}
	return out
}

func danWords(s *snap.Snapshot) (w [2]uint32) {
	b := s.Save[addr.InfoOffDan+addr.DanOffSwitch:]
	for i := range w {
		w[i] = binary.BigEndian.Uint32(b[4*i:])
	}
	return w
}

func zoneWords(s *snap.Snapshot, z int) (w [3]uint16) {
	b := s.Save[addr.InfoOffZone+z*addr.ZoneSize+addr.ZoneOffSwitch:]
	for i := range w {
		w[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return w
}

func killWordsOf(s *snap.Snapshot, z int) (w [killWords]uint32) {
	b := s.Save[addr.InfoOffZone+z*addr.ZoneSize+addr.ZoneOffActor:]
	for i := range w {
		w[i] = binary.BigEndian.Uint32(b[4*i:])
	}
	return w
}

// rises32 returns the words of cur with bits prev lacks, numbered from
// first.
func rises32(prev, cur []uint32, first int) []wire.ScopedWord {
	var out []wire.ScopedWord
	for i := range cur {
		if r := cur[i] &^ prev[i]; r != 0 {
			out = append(out, wire.ScopedWord{Word: uint8(first + i), Bits: r})
		}
	}
	return out
}

func rises16(prev, cur []uint16, first int) []wire.ScopedWord {
	var out []wire.ScopedWord
	for i := range cur {
		if r := cur[i] &^ prev[i]; r != 0 {
			out = append(out, wire.ScopedWord{Word: uint8(first + i), Bits: uint32(r)})
		}
	}
	return out
}
