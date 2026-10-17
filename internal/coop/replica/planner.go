package replica

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/inventory"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Timing is when the receiver applies a planned write: the "sync rule"
// column of §3, with §6.3's apply policy for memory switches and §6.5's
// for story bits. The reconciler (reconcile.go) makes each M3 write when
// its timing allows.
type Timing uint8

const (
	// Live: as soon as the receiver is IDLE (and STABLE). The current
	// table's stage progress bits (chests and pickups are read when
	// their actors are created, d_a_tbox.cpp:447-455, d_a_item.cpp:
	// 227-237, so a write fires no event), inventory, collection and
	// capacities. The boss bits are the exception: a boss stage polls
	// them every frame, so they are held there (bossHold).
	Live Timing = iota + 1
	// OnLoad: saved data the game reads when it loads it, written as
	// soon as the receiver is STABLE, IDLE or not: another table's memBit,
	// to its saved slot only (getSave copies that slot over the live copy
	// when the table loads, d_save.cpp:1506-1509; putSave copies the live
	// copy back when it unloads, d_save.cpp:1512-1515, so the saved slot
	// of a table not loaded is the only copy). No game code that runs
	// under the pause menu writes such a slot (Appendix C item 128).
	OnLoad
	// OnRoomChange: deferred to the receiver's next room or stage change
	// and written on the first STABLE and IDLE tick after it (§6.3), so a
	// partner's bit does not change what the actors of the receiver's
	// room are doing, and does not land while an event runs or the pause
	// menu is open, under which the sea chart writes the charts and the
	// sea map (d_menu_fmap.cpp:3271, d_menu_fmap2.cpp:1045; Appendix C
	// item 129). The current table's memory switches, which polling
	// actors answer with camera events (§6.1), wait as well for the room
	// hold (every shown room's actors have had roomSettle IDLE frames,
	// reconcile_m5_rooms.go), as a new shutter swallows a switch set in
	// its first 30 frames; --coop-live-switches (hub.FeatureLiveSwitches)
	// applies them when IDLE and past the room hold instead. And the
	// ocean, the charts and the sea map, §3's "on load": they have one
	// copy, which actors read at creation (salvage points register only
	// unsalvaged ones, d_salvage.cpp:85-110, called from daSalvage_c's
	// create, d_a_salvage.cpp:88-119; daOship_c::_create, d_a_oship.cpp:
	// 1023) and some every frame (daSalvage_c::proc_wait, d_a_salvage.cpp:
	// 286-325; the Ghost Ship, d_a_ghostship.cpp:333-336), so writing
	// them as the receiver changes room is the conservative choice. None
	// of those readers starts an event (inferred).
	OnRoomChange
	// OnTransition: story event bits and the allowlisted event
	// registers, written on the first tick of the receiver's next stage
	// change (§6.5; story.Receiver) so the next stage's layers and actors
	// read them at creation. A register lives beside the story bits in
	// dSv_event_c and is read as they are (getEventReg and isEventBit,
	// d_save.cpp:1196-1198, :1207-1209), so it takes their timing
	// (inferred: its readers were not each traced), and the WriteGuard
	// opens both on that tick alone (TargetStory).
	OnTransition
)

func (t Timing) String() string {
	switch t {
	case Live:
		return "live, when idle"
	case OnLoad:
		return "on load"
	case OnRoomChange:
		return "at the next room or stage change"
	case OnTransition:
		return "at the next stage transition"
	}
	return fmt.Sprintf("Timing(%d)", uint8(t))
}

// timingOf is image byte off's apply rule while table cur is loaded.
func timingOf(off, cur int) Timing {
	switch CategoryOf(off) {
	case CatStage, CatSwitch:
		t, _, _ := memBitTable(off)
		switch {
		case t != cur:
			return OnLoad
		case CategoryOf(off) == CatSwitch:
			return OnRoomChange
		}
		return Live
	case CatOcean:
		return OnRoomChange
	case CatStory, CatRegister:
		return OnTransition
	}
	return Live
}

// The boss bits: mDungeonItem's STAGE_BOSS_ENEMY (bit 3, the boss
// defeated) and STAGE_BOSS_DEMO (bit 5, its opening demo seen)
// (d_save.h:639-646). Most readers run when an actor is created, but a
// boss stage's run every frame, reading the live memBit (dComIfGs_isStage-
// BossEnemy and -Demo, d_com_inf_game.h:1773-1775):
//
//   - dMeter_recollect_boss_data, from dMeter_mapMove each frame
//     (d_meter.cpp:5994, 7397): in a BOSS-type stage of DRC, FW, ET or WT
//     with the boss defeated, it copies the player's status and items as
//     the Ganon's Tower refight snapshot (dComIfGs_copyPlayerRecollection-
//     Data, d_com_inf_game.cpp:1409-1436) and sets the event bit that
//     stops it ever being taken again (d_meter.cpp:1069-1085);
//   - the boss gauge, shown only while the demo is seen and the boss is
//     not defeated (d_meter.cpp:3682-3687);
//   - Gohma's arena floor (yuka_move, from daDr2_Execute: d_a_dr2.cpp:
//     560-575, 635), dropped once the boss is defeated;
//   - Helmaroc King's obj_move, from daBdk_Execute (d_a_bdk.cpp:
//     2741-2744, 3603).
//
// Writing them while the receiver fights its own boss would end, or
// restart, that fight under it (§2's non-goal), so the reconciler holds
// them in such a stage (bossStage) and writes them once Link has left it:
// live in another stage of the same table, else to the saved slot.
const bossBits = 1<<addr.DungeonItemStageBossEnemy | 1<<addr.DungeonItemStageBossDemo

// The STAG chunk's stage type: dStage_stagInfo_GetSTType is
// (mStageTypeAndSchbit >> 16) & 7 (d_stage.h:1031-1033), the u32 at +0x0C
// (d_stage.h:72), so the low 3 bits of its big-endian second byte.
const (
	stagOffType   = 0x0D
	stageTypeBoss = 3 // dStageType_BOSS_e (d_stage.h:20)
)

// readStag reads, from the STAG chunk at stagInfo (mStageData.mpStagInfo),
// the save table its mProp names (dStage_stagInfo_GetSaveTbl,
// d_stage.h:1027-1029) and its stage type, in one read. ok is false for a
// null or unreadable pointer.
func readStag(m mem.Mem, stagInfo uint32) (table, typ int, ok bool) {
	if stagInfo == 0 {
		return 0, 0, false
	}
	b, err := m.Read(stagInfo+addr.StagOffProp, stagOffType-addr.StagOffProp+1)
	if err != nil {
		return 0, 0, false
	}
	return addr.StagSaveTable(b[0]), int(b[len(b)-1] & 7), true
}

// bossStage reports whether a stage of type typ (ok: the STAG could be
// read) with table loaded polls the boss bits every frame: a BOSS-type
// stage, whose type the meter and the refight snapshot go by
// (d_meter.cpp:1071, 3684), or any stage of Forsaken Fortress's table,
// where the meter takes Helmaroc King's actor for a boss stage
// (fopAcM_SearchByName(fpcNm_BDK_e), d_meter.cpp:3684). That Helmaroc King
// is met only in that table's stages is inferred. A STAG that can't be
// read counts as a boss stage's: the bits wait.
func bossStage(typ int, ok bool, table int8) bool {
	return !ok || typ == stageTypeBoss || table == addr.TableFF
}

// bossHold is the hold on a boss stage's boss bits.
var bossHold = Hold{Why: "the boss stage reads them every frame (d_meter.cpp:1071, d_a_dr2.cpp:561); written once Link leaves it"}

// Write is one planned write: OR Bits into image byte Off or, for a MAX
// byte, raise it to Bits.
type Write struct {
	Off      uint16
	Bits     byte
	Max      bool
	Category Category
	When     Timing
	// Saved is the image byte's address, addr.SaveImage + Off.
	Saved uint32
	// Live is the live memBit's copy of the byte when Off is in the
	// loaded table's saved slot, 0 otherwise. Such a write goes to both,
	// the way dComIfGs_onStageTbox does (d_com_inf_game.cpp:694-702),
	// so a putSave cannot lose it (§3.1).
	Live uint32
}

// Addrs returns where w writes: Saved, then Live if set.
func (w Write) Addrs() []uint32 {
	if w.Live != 0 {
		return []uint32{w.Saved, w.Live}
	}
	return []uint32{w.Saved}
}

// newWrite places a write of bits at off while table cur is loaded.
func newWrite(off int, bits byte, cur int) Write {
	w := Write{
		Off:      uint16(off),
		Bits:     bits,
		Max:      class.Of(off).Class == class.Max,
		Category: CategoryOf(off),
		When:     timingOf(off, cur),
		Saved:    addr.SaveImage + uint32(off),
	}
	if t, rel, ok := memBitTable(off); ok && t == cur {
		w.Live = addr.LiveMemBit + uint32(rel)
	}
	return w
}

// Hold is a story bit the receiver keeps out of its game until a
// condition holds locally, although the world has it (§6.5): applying it
// early would move the receiver's resume stage or lock its boat.
type Hold struct {
	ID  uint16 // event bit 0xHHLL
	Why string
	// released reports whether the hold is over, from the receiver's
	// canonical image (raw, local bytes included) and that image with
	// the plan's other story bits in it (after).
	released func(raw, after *world.Image) bool
}

func eventSet(img *world.Image, id uint16) bool {
	b := addr.EventByte(id)
	return img[addr.OffEvent+b.Off]&b.Mask == b.Mask
}

// Story bits the holds name (d_save_event_flag.inc).
const (
	evMetKorl         = 0x0F80 // MET_KORL, :111
	ev0801            = 0x0801 // UNK_0801, :66
	ev0808            = 0x0808 // UNK_0808, :63
	ev2401            = 0x2401 // UNK_2401, :279
	ev2D10            = 0x2D10 // UNK_2D10, :342
	evZeldaAwakened   = 0x2D02 // ZELDA_AWAKENED, :345
	evHyruleCourtyard = 0x3804 // HYRULE_COURTYARD_CUTSCENE, :430
	ev3E10            = 0x3E10 // UNK_3E10, :476
	ev3F80            = 0x3F80 // UNK_3F80, :480
)

// masterSwords are the items daPy_lk_c::checkMasterSwordEquip accepts in
// mSelectEquip[0] (d_a_player.h:599-603; dItemNo_MASTER_SWORD_1-3,
// d_item_data.h:64-69).
var masterSwords = [...]byte{0x39, 0x3A, 0x3E}

func masterSwordEquipped(raw *world.Image) bool {
	for _, it := range masterSwords {
		if raw[addr.OffSelectEquip] == it {
			return true
		}
	}
	return false
}

// Holds are §6.5's receiver-side holds. The four resume-stage bits pick
// where a loaded file starts (l_checkData in dComIfGs_setGameStartStage,
// d_com_inf_game.cpp:1304-1311): each but MET_KORL is held until the
// receiver has MET_KORL itself, and MET_KORL until it gets it in its own
// game. The other three lock boarding the boat while their condition is
// unmet (daShip_c, d_a_ship.cpp:4224-4229). §6.5 names two of them; the
// decomp's third clause, UNK_3E10 without UNK_3F80, is held the same way.
var Holds = []Hold{
	{evMetKorl, "MET_KORL moves the resume stage to Windfall", func(raw, _ *world.Image) bool {
		return eventSet(raw, evMetKorl)
	}},
	{ev0801, "0x0801 moves the resume stage to MajyuE", func(_, after *world.Image) bool {
		return eventSet(after, evMetKorl)
	}},
	{ev0808, "0x0808 moves the resume stage to MajyuE", func(_, after *world.Image) bool {
		return eventSet(after, evMetKorl)
	}},
	{ev2401, "0x2401 moves the resume stage to A_umikz", func(_, after *world.Image) bool {
		return eventSet(after, evMetKorl)
	}},
	{ev2D10, "0x2D10 locks the boat until a Master Sword is equipped", func(raw, _ *world.Image) bool {
		return masterSwordEquipped(raw)
	}},
	{evHyruleCourtyard, "0x3804 locks the boat until ZELDA_AWAKENED", func(_, after *world.Image) bool {
		return eventSet(after, evZeldaAwakened)
	}},
	{ev3E10, "0x3E10 locks the boat until 0x3F80", func(_, after *world.Image) bool {
		return eventSet(after, ev3F80)
	}},
}

// HeldBit is a bit of the world a hold keeps out of the receiver's game.
type HeldBit struct {
	Off  uint16
	Mask byte
	Hold Hold
}

// Planner computes what the receiver's game lacks of the world (§4.2):
//
//	missing = desired &^ local &^ suppressed
//
// where desired is the world's image within the policy, local the
// receiver's canonical image, and suppressed the bits the receiver's game
// ever had (so a bit the game cleared itself, a timer or a game-cleared
// story bit, is not set again) plus the holds. A MAX byte is missing when
// the world's value is above every value the receiver ever had. It is
// level-triggered: Plan works from the current state alone, so a write
// lost to the game is planned again next tick, and after every stage
// change.
//
// The ever-had set is the receiver's own history: every bit its game had
// on a consistent tick, and, when the plan is written (M3), every bit the
// reconciler wrote, so a switch applied once and cleared by the
// receiver's game is never applied again, even if the game clears it
// before the next tick reads it. The set starts over when another file is
// loaded.
//
// NewPlanner's Planner is M2's, which the dry run (--coop-dry-run) keeps:
// the ever-had set suppresses every bit. The replica's reconciler
// (reconcile.go), which writes the plan's M3 classes when their Timing
// allows, plans with writes set, which changes three things. The ever-had
// set suppresses only the bits the game clears in play (see healing): a
// bit of a byte the game never clears is written again whenever it is
// missing, so one lost all the same heals (§3.1). The set starts over
// when a save state is loaded (the heartbeat goes backwards) and when the
// receiver passes through the title or file select (a file loaded from
// the card again has lost what it had since it was saved): that game
// never had what came after, so the world's bits are applied to it
// afresh, as at a session start. And the reconciler's writes go into it
// (wrote). A Planner is not safe for concurrent use.
type Planner struct {
	policy *world.Policy
	writes bool // the reconciler writes the plan (M3); false: M2's dry run

	have    bool
	file    fileKey
	ever    world.BitSet
	everMax world.Image
	boss    bool // the last tick's stage holds the boss bits (bossStage)
}

// NewPlanner returns a Planner for policy (nil: world.DefaultPolicy).
func NewPlanner(policy *world.Policy) *Planner {
	if policy == nil {
		policy = world.DefaultPolicy()
	}
	return &Planner{policy: policy}
}

// healing reports whether the receiver's game never clears a bit of image
// byte off in play, so the reconciler's planner writes a missing one
// whatever the ever-had set says. That is the stage progress bits: dSv_memBit_c has no
// offTbox, offItem or offVisitedRoom (d_save.cpp:1072-1150), and its
// offDungeonItem (d_save.cpp:1141-1144) is reached only through
// dComIfGs_off* wrappers nothing calls (d_com_inf_game.cpp:725-886,
// d_com_inf_game.h:1733-1799). The ocean's salvage bits have no clear
// (dSv_ocean_c, d_save.cpp:1157-1176), nor have the charts owned and the
// sea map (onGetMap, onFmapBit and onSaveArriveGrid, d_save.cpp:852-855,
// 930-945). The charts deciphered and salvaged and the Triforce charts are
// cleared: getting a chart clears its two bits (item_func_collectmap01-61,
// d_item.cpp:1476-2023, and chart 8's Triforce bit, :1958), and the sea
// chart menu clears them for every chart not owned
// (dMenu_Fmap2_c::calcGetCollectMap, d_menu_fmap2.cpp:3174-3192). Those,
// the memory switches (offSwitch, d_save.cpp:1563-1583) and every class
// M4 writes stay apply-once.
func healing(off int) bool {
	switch CategoryOf(off) {
	case CatStage:
		return true
	case CatOcean:
		return (off < addr.OffMapOpen || off >= addr.OffFmapBits) && off != addr.OffMapTriforce
	}
	return false
}

// observe records a consistent tick's local bits in the ever-had set,
// starting over when another file is loaded and, when the plan is
// written, when a save state is loaded or the receiver passes through the
// title or file select.
func (p *Planner) observe(o *obs) {
	p.boss = o.boss
	if p.writes && (o.st.Regressed || o.screen == screenOut) {
		p.have = false
	}
	if !o.consistent() {
		return
	}
	if !p.have || p.file != o.key {
		p.have, p.file = true, o.key
		p.ever, p.everMax = world.BitSet{}, world.Image{}
	}
	for off, b := range o.local {
		if b == 0 {
			continue
		}
		if class.Of(off).Class == class.Max {
			p.everMax[off] = max(p.everMax[off], b)
		} else {
			p.ever.Add(off, b)
		}
	}
}

// wrote records in the ever-had set bits the reconciler set in image byte
// off of the file the planner last saw.
func (p *Planner) wrote(off int, b byte) {
	if p.have && b != 0 && class.Of(off).Class != class.Max {
		p.ever.Add(off, b)
	}
}

// EverHad returns a copy of the ever-had set of OR bits.
func (p *Planner) EverHad() world.BitSet { return p.ever }

// planMask is the bits of image byte off a plan may hold: its class mask
// (world.SharedMask), narrowed for the story bits to class.SyncMask
// (§6.5: the LocalOnly, game-cleared and refight bits stay each player's)
// and for the inventory's flag bytes to inventory.FlagMask (§6.6: the
// bottle contents ever held and the Hero's Charm worn stay each
// player's), the bits the WriteGuard opens.
func planMask(off int) byte {
	m := world.SharedMask(off)
	switch CategoryOf(off) {
	case CatStory:
		m &= class.SyncMaskOf(off - addr.OffEvent)
	case CatInventory:
		m &= inventory.FlagMask(off)
	}
	return m
}

// Plan returns the writes that would bring the receiver (raw, its
// canonical image; local, its shared bits) up to view while table cur is
// loaded, in ascending offset order, and the bits holds keep back: §6.5's
// story-bit holds and, when the plan is written, table cur's boss bits
// while the last observed stage is a boss stage (bossHold).
func (p *Planner) Plan(view, raw, local *world.Image, cur int) ([]Write, []HeldBit) {
	var miss world.Image
	for off := range view {
		m := planMask(off) & p.policy[off]
		if m == 0 {
			continue
		}
		if class.Of(off).Class == class.Max {
			v := view[off] & world.SharedMask(off)
			if v&^m == 0 && v > local[off] && v > p.everMax[off] {
				miss[off] = v
			}
			continue
		}
		v := view[off] & m &^ local[off]
		if !p.writes || !healing(off) {
			v &^= p.ever.Mask(off)
		}
		miss[off] = v
	}

	// Holds, to a fixed point: a hold is judged on the receiver's state
	// with the plan's unheld story bits in it, and holding a bit can
	// keep back what another hold waits for (MET_KORL for the other
	// resume-stage bits).
	var held []HeldBit
	holding := make([]bool, len(Holds))
	for changed := true; changed; {
		changed = false
		after := *raw
		for i := addr.OffEvent; i < addr.OffEvent+addr.EventBitBytes; i++ {
			after[i] |= miss[i]
		}
		for i, h := range Holds {
			if holding[i] || h.released(raw, &after) {
				continue
			}
			holding[i], changed = true, true
			b := addr.EventByte(h.ID)
			off := addr.OffEvent + b.Off
			if c := miss[off] & b.Mask; c != 0 {
				miss[off] &^= c
				held = append(held, HeldBit{Off: uint16(off), Mask: c, Hold: h})
			}
		}
	}

	if p.writes && p.boss && addr.ValidTable(cur) {
		off := addr.OffMemBit(cur) + addr.MemBitOffDungeonItem
		if c := miss[off] & bossBits; c != 0 {
			miss[off] &^= c
			held = append(held, HeldBit{Off: uint16(off), Mask: c, Hold: bossHold})
		}
	}

	var ws []Write
	for off, b := range miss {
		if b != 0 {
			ws = append(ws, newWrite(off, b, cur))
		}
	}
	return ws, held
}

// Planned returns local with ws applied: the image the receiver would
// reach if every planned write landed.
func Planned(local *world.Image, ws []Write) world.Image {
	out := *local
	for _, w := range ws {
		if w.Max {
			m := world.SharedMask(int(w.Off))
			out[w.Off] = out[w.Off]&^m | w.Bits
		} else {
			out[w.Off] |= w.Bits
		}
	}
	return out
}
