package replica

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/inventory"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// Target is one part of the WriteGuard's allowlist (§4.2). A Guard is
// built for the targets its milestone writes, so a planner bug can't
// reach bytes a later milestone has yet to validate.
type Target uint16

const (
	// TargetStage is every save table's saved memBit and the live one:
	// chests, memory switches, pickups, visited rooms and dungeon items
	// 0-5, within their class masks (§3 "Stage progress bits", "Memory
	// switches"). M3.
	TargetStage Target = 1 << iota
	// TargetOcean is mOcean, the charts and the sea map (§3 "Ocean and
	// charts"). M3.
	TargetOcean
	// TargetStory is the named event bits of class.SyncMask and the
	// allowlisted event registers (§6.5, §3 "Event registers"), written
	// only as a stage change starts. M4.
	TargetStory
	// TargetInventory is the inventory's flag bits (inventory.FlagMask:
	// mGetItem, mCollect[0..4], the songs, Triforce, pearls, beast and
	// bait flags) and the MAX capacities (§6.6). M4.
	TargetInventory
	// TargetDerived is what each receiver derives: the item slots
	// inventory derives (every mItems slot but the bottles),
	// mSelectEquip[0..2] and every table's mKeyNum, saved and live
	// (§6.4, §6.6), written only by the derive path. mMaxLife and
	// mMaxMagic are not in it: they change only through their pending
	// counters (§3). M4.
	TargetDerived
	// TargetCounters is the HUD's pending counters mItemKeyNumCount,
	// mItemMaxLifeCount and mItemMaxMagicCount (§6.4), written only by
	// the derive path. M4.
	TargetCounters
	// TargetSelectItem is play.mSelectItem[0..2], the X, Y and Z
	// buttons' items, refreshed by the derive path after a slot changes
	// (§6.6). M4.
	TargetSelectItem
	// TargetScoped is the dan switch words and every zone's switch words
	// (§6.3), written only by the scoped path. The WriteGuard itself holds
	// each byte to the zone table of the tick's snapshot (Tick.Zones): a
	// zone byte only in the slot mStatus[room].mZoneNo of the room Link is
	// in, whose mRoomNo is that room, a dan byte only while mDan.mStageNo
	// holds a save table (§3.1). M5.
	TargetScoped
	// TargetKills is every zone's dead-actor words (§7.2), written only by
	// the kill path, each bit only into the zone of a loaded room whose
	// mRoomNo names it back, and only for a setID the tick's M6 plan takes
	// (WriteGuard.SetCombat: the partner's store and the M1 verdict). M6.
	TargetKills
	// TargetTag is the binding tag, the first 0x30 bytes of mReserve
	// (bind.TagRegion). Only a bind and a scrub write it (§5.2), and
	// they do it through package bind, never through a FrameWriter.
	TargetTag
	// TargetEscape is what the escape hatches write (§6.7, from M3):
	// mNextStage, for "refresh area" and a warp to a partner on another
	// stage, and the mailbox's warp request (warp_seq and warp_x/y/z,
	// mailbox_core.h), for a warp on the same stage. Each byte takes
	// only the values the game accepts there (escapeRules); warp_ack and
	// warp_force are not in it. They are no save data, and nothing of
	// the world: FrameWriter.ApplyFields writes them, on the user's word
	// alone, when IDLE.
	TargetEscape
	// TargetK1 is a live enemy's light-shrink timer and the CARRY byte of
	// the weapon it holds (§7.2): bytes on the heap, in no static list.
	// WriteGuard.checkActor lets through exactly two writes, each of an
	// actor the tick's walk has (FrameWriter.ApplyActors). M6.
	TargetK1
	// TargetBoss is the mailbox's boss_kill request (wwapi/mailbox
	// boss.go, inject/include/wwboss.h): its proc and tag, held to the
	// procs of bosskill.go's bossKillRows (Gohma, Kalle Demos) and the
	// request's tag, and its seq (Gohdan's too, batch D G-D1). The REL gives this game's own boss the
	// death a lethal hit gives it, so the boss bits the planner holds in
	// a boss stage (bossHold) are set by the game itself (bosskill.go;
	// design research C3-C6-gohma.md B5, C1-kalle-demos-relay.md). No save data:
	// FrameWriter.ApplyBossKill writes it, when IDLE.
	TargetBoss
	// TargetLock is a live key door's lock, dDoor_key2_c.mbEnabled (a
	// bool at the lock's +0x00, d_door.h:89; DOOR10's lock at +0x308,
	// d_a_door10.h:46, DOOR12's at +0x2E4, d_a_door12.h:51): one byte on
	// the heap, from 1 to 0, of a door the tick's walk has whose lock
	// switch the replica's own verified writes set (the stale-actor
	// refresh's Tier 1, refresh.go). WriteGuard.checkLock lets through
	// exactly that write (FrameWriter.ApplyLocks).
	TargetLock
	// TargetRefresh is the mailbox's actor_refresh request (wwapi/mailbox
	// refresh.go, inject/include/wwrefresh.h; enemy-mirror-batch-d.json
	// R-D4, G-D4, G-D5): its proc held to the allow-list's bytes, its tag,
	// its bit kind (1..3), the pid, params, room and bit number the relay
	// found (refresh_relay.go), and its seq. The REL re-derives and
	// re-reads the bit, runs its own gates and refuses any proc off its
	// allow-list, so a wrong request costs a refusal, never a wrong delete
	// (the design's safety rule). No save data: FrameWriter.ApplyRefresh
	// writes it, when IDLE.
	TargetRefresh

	// AllTargets is the most any milestone writes: NewGuard's allowlist.
	AllTargets = TargetStage | TargetOcean | TargetStory | TargetInventory | TargetDerived |
		TargetCounters | TargetSelectItem | TargetScoped | TargetKills | TargetTag | TargetEscape | TargetK1 | TargetBoss | TargetLock | TargetRefresh
	// M3Targets is what M3's reconciler writes (§9 M3): the stage
	// progress bits and memory switches, the ocean, the charts and the
	// sea map. A replica's WriteGuard adds TargetEscape for the escape
	// hatches M3 ships too.
	M3Targets = TargetStage | TargetOcean
	// M4Targets is what M4's receiver adds (§9 M4): the story bits and
	// registers, the inventory and capacities, the derived bytes, the
	// pending counters and the buttons' items.
	M4Targets = TargetStory | TargetInventory | TargetDerived | TargetCounters | TargetSelectItem
	// M5Targets is what M5's receiver adds (§9 M5): the dan and zone
	// switch words, the room switches of a room edge among them. A
	// replica that writes guards M3Targets | M4Targets | M5Targets |
	// TargetEscape.
	M5Targets = TargetScoped
	// M6Targets is what M6's receiver adds (§9 M6): the zones' dead-actor
	// words and the light-shrink. A replica that writes guards M3Targets |
	// M4Targets | M5Targets | M6Targets | TargetEscape | TargetBoss.
	M6Targets = TargetKills | TargetK1
	// RefreshTargets is what the stale-actor refresh adds
	// (design/stale-actor-refresh.md Tier 1): the key doors' locks. Its
	// Tier 2 is M6's light-shrink (TargetK1) with a second allow rule.
	// Its Tier 3 (TargetRefresh) is a request the REL acts on, which a
	// replica's WriteGuard adds beside TargetBoss.
	RefreshTargets = TargetLock
)

var targetNames = [...]string{
	"stage", "ocean", "story", "inventory", "derived",
	"counters", "select-item", "scoped", "kills", "tag", "escape", "k1", "boss", "lock", "refresh",
}

func (t Target) String() string {
	if t == 0 {
		return "none"
	}
	var parts []string
	for i, n := range targetNames {
		if t&(1<<i) != 0 {
			parts = append(parts, n)
		}
	}
	if rest := t &^ AllTargets; rest != 0 {
		parts = append(parts, fmt.Sprintf("0x%X", uint16(rest)))
	}
	return strings.Join(parts, "|")
}

// rule is how a write may change a byte.
type rule uint8

const (
	ruleNone   rule = iota // not a write target
	ruleOR                 // set bits within the mask, never clear one: OR and OR_MASKED bytes, the scoped words
	ruleMax                // raise the value within the mask, never lower it: MAX bytes
	ruleSet                // any value within the mask: the counters' bytes, the tag, the warp request
	ruleName               // a stage name's byte: NUL, or a letter, digit or '_'
	ruleRange              // a signed value in [lo, hi]: mNextStage's numbers, a key count
	ruleArm                // 0 to 1 and nothing else: mNextStage.mEnable
	ruleSlot               // a derived slot: from nothing or a lesser variant of its item to a better one
	ruleButton             // a button's item: from nothing or a lesser variant to a better one of some derived slot
	ruleIn                 // one of a set of values (in): a byte of a boss_kill or actor_refresh proc
)

// byteRule is what the allowlist says of one byte.
type byteRule struct {
	mask   byte // the bits a write may change
	rule   rule
	target Target
	paths  path       // the FrameWriter calls that may write it
	lo, hi int8       // ruleRange's bounds
	slot   int8       // ruleSlot's slot, an index into derivedSlots
	in     *[256]bool // ruleIn's values
}

// path is a FrameWriter call a write comes through (§4.2). Each byte of
// the allowlist names the paths that may write it, and each path is
// judged as itself, so a byte reaches the game only through its own:
//
//   - pathMerge, FrameWriter.ApplyContext: the merging image bytes,
//     saved and live (OR, OR_MASKED and MAX), and the scoped words, each
//     a fresh read with the planned bits ORed in or the value raised;
//   - pathDerive, FrameWriter.ApplyDerived: the DERIVED bytes, the
//     pending counters and play.mSelectItem, each as part of a
//     DeriveWrite of the kind that owns it (WriteGuard.checkDerive), by
//     compare-and-set;
//   - pathEscape, FrameWriter.ApplyFields: mNextStage and the mailbox
//     warp request (§6.7);
//   - pathScoped, FrameWriter.ApplyScoped: the dan and zone switch bytes
//     (M5), each a fresh read with the planned bits ORed in, after the zone
//     table they rest on was read again (§3.1);
//   - pathTag: the binding tag, which package bind writes itself (§5.2);
//     no FrameWriter call takes it;
//   - pathKill, FrameWriter.ApplyKills: the zones' dead-actor bytes (M6),
//     each a fresh read with the planned bits ORed in, after the zone table
//     and the room's status they rest on were read again;
//   - pathActor, FrameWriter.ApplyActors: a light-shrink timer and a
//     weapon's CARRY byte (M6), each of an actor read again just before
//     (combat.Revalidate), judged by WriteGuard.checkActor;
//   - pathBoss, FrameWriter.ApplyBossKill: the mailbox's boss_kill
//     request (bosskill.go);
//   - pathRefresh, FrameWriter.ApplyRefresh: the mailbox's actor_refresh
//     request (refresh_relay.go).
type path uint16

const (
	pathMerge path = 1 << iota
	pathDerive
	pathEscape
	pathTag
	pathScoped
	pathKill
	pathActor
	pathBoss
	pathRefresh

	// pathAny is WriteGuard.Check's: a byte judged as written by its own
	// path, whichever that is.
	pathAny = pathMerge | pathDerive | pathEscape | pathTag | pathScoped | pathKill | pathActor | pathBoss | pathRefresh
)

func (p path) String() string {
	var parts []string
	for _, n := range []struct {
		p    path
		name string
	}{
		{pathMerge, "the merge path (FrameWriter.ApplyContext)"},
		{pathDerive, "the derive path (FrameWriter.ApplyDerived)"},
		{pathEscape, "an escape (FrameWriter.ApplyFields)"},
		{pathTag, "a bind or scrub (package bind)"},
		{pathScoped, "the scoped path (FrameWriter.ApplyScoped)"},
		{pathKill, "the kill path (FrameWriter.ApplyKills)"},
		{pathActor, "the light-shrink (FrameWriter.ApplyActors)"},
		{pathBoss, "the boss_kill request (FrameWriter.ApplyBossKill)"},
		{pathRefresh, "the actor_refresh request (FrameWriter.ApplyRefresh)"},
	} {
		if p&n.p != 0 {
			parts = append(parts, n.name)
		}
	}
	if len(parts) == 0 {
		return "no path"
	}
	return strings.Join(parts, " or ")
}

// derivedSlots are the item slots and equipment bytes a receiver derives
// from its flags (save.ItemSlots, save.EquipSlots): ruleSlot's
// variants. mItems' four bottles are each player's own and not among
// them, nor is mSelectEquip[3], which has no setter
// (dComIfGs_setSelectEquip, d_com_inf_game.cpp:1786-1822).
var derivedSlots = append(save.ItemSlots(), save.EquipSlots()...)

// buttonSlots are the slots a button's item may come from: the derived
// item slots (the equipment is on no button).
var buttonSlots = save.ItemSlots()

// derivedSlotAt returns the index in derivedSlots of the slot whose byte is
// image byte off, or -1.
func derivedSlotAt(off int) int {
	return slices.IndexFunc(derivedSlots, func(s save.Slot) bool { return s.Off == off })
}

// isKeyByte reports whether image byte off is a save table's mKeyNum
// (dSv_memBit_c +0x20, d_save.h:684-689).
func isKeyByte(off int) bool {
	_, rel, ok := memBitTable(off)
	return ok && rel == addr.MemBitOffKeyNum
}

// betterVariant reports whether s may go from old to val: val is one of
// its variants, and old is nothing or a lesser variant (save.Slot.
// Derive's rule: nothing is ever taken away, §6.6).
func betterVariant(s save.Slot, old, val byte) bool {
	rv, ro := s.Rank(val), s.Rank(old)
	return rv >= 0 && (old == save.ItemNone || ro >= 0 && ro < rv)
}

// Guard is the static allowlist of the WriteGuard (§4.2): the bytes the
// co-op pipeline may write, the bits of each it may change and how, and
// the FrameWriter call that may write it (path), for a set of Targets. It
// is computed from the class table and the address table. AllTargets
// covers:
//
//   - the image bytes that merge (OR, OR_MASKED, MAX: their class mask),
//     the story bits narrowed to class.SyncMask and the inventory's flag
//     bytes to inventory.FlagMask (so neither the LocalOnly, game-cleared
//     and refight story bits nor the bottle contents ever held and the
//     Hero's Charm worn are targets);
//   - the DERIVED bytes a receiver recomputes, through the derive path
//     only: the item slots and mSelectEquip[0..2] (derivedSlots), each
//     only ever moved up to a better variant, and each table's mKeyNum,
//     0..99 (dMeter_keyMove's clamp, d_meter.cpp:5248-5254). mMaxLife
//     and mMaxMagic are not in it: they change only through their pending
//     counters (§3);
//   - the live memBit, with the classes of a saved slot;
//   - the dan switch words (mDan.mSwitch) and every zone's switch words
//     (dSv_zone_c +0x02..+0x07), through the scoped path only, each held
//     to the zone table of the tick (WriteGuard.Check), and every zone's
//     dead-actor words (+0x0C..+0x4B), through the kill path only, each
//     held to a loaded room's zone and the tick's M6 plan
//     (Tick.killClosed); the zone's own item bits and its mRoomNo are not
//     in it;
//   - the three pending counters, mItemKeyNumCount, mItemMaxLifeCount
//     and mItemMaxMagicCount (§6.4), through the derive path only;
//   - play.mSelectItem[0..2], the buttons X, Y and Z (u8 mSelectItem[4],
//     d_com_inf_game.h:830; dItemBtn_COUNT_e is 3, :1115), refreshed
//     through the derive path after a slot changes (§6.6);
//   - the binding tag (bind.TagRegion), the first 0x30 bytes of the
//     image's mReserve;
//   - the escape hatches' mNextStage and mailbox warp request (§6.7),
//     each byte held to the values the game accepts (escapeRules).
//
// Nothing per player is in it: not mLife (a 0 kills Link, §5.1), not
// rupees, ammo, bottles, names or options, and no padding.
//
// A merging byte is grow-only: an OR byte may gain bits but never lose
// one, a MAX byte, a capacity included, may rise but never fall. A slot,
// an equipment byte or a button only ever moves to a better variant. A
// key count may fall (a key the receiver's own door took), within 0..99.
// The counters' bytes take any value here; the derive path holds each
// counter write to a delta the HUD takes, and max life and magic to a
// rise (WriteGuard.checkDerive). A Guard is immutable and safe for
// concurrent use; a WriteGuard adds the gates and the policy.
type Guard struct {
	targets Target
	list    write.Allowlist[byteRule]
}

// ErrGuard is matched (errors.Is) by every refusal Check returns.
var ErrGuard = errors.New("replica: write refused by the WriteGuard")

// NewGuard computes the allowlist of every target (AllTargets).
func NewGuard() *Guard { return NewGuardFor(AllTargets) }

// NewGuardFor computes the allowlist of targets t.
func NewGuardFor(t Target) *Guard {
	g := &Guard{targets: t & AllTargets}
	add := func(start uint32, rules []byteRule, name string) {
		mask := make([]byte, len(rules))
		some := false
		for i := range rules {
			if rules[i].target&g.targets == 0 {
				rules[i] = byteRule{}
			}
			mask[i] = rules[i].mask
			some = some || mask[i] != 0
		}
		if some {
			if err := g.list.Add(write.Span[byteRule]{Start: start, Mask: mask, Rules: rules, Name: name}); err != nil {
				panic(err) // the spans are fixed: TestGuardSpans
			}
		}
	}
	whole := func(n int, r rule, tg Target, p path) []byteRule {
		rs := make([]byteRule, n)
		for i := range rs {
			rs[i] = byteRule{mask: 0xFF, rule: r, target: tg, paths: p}
		}
		return rs
	}

	img := make([]byteRule, addr.SaveImageSize)
	for off := range img {
		img[off] = imageRule(off)
	}
	add(addr.SaveImage, img, "dSv_save_c")

	live := make([]byteRule, addr.MemBitSize)
	for i := range live {
		live[i] = imageRule(addr.OffMemBit(0) + i)
	}
	add(addr.LiveMemBit, live, "live memBit")

	add(addr.DanSwitch, whole(addr.DanSize-addr.DanOffSwitch, ruleOR, TargetScoped, pathScoped), "mDan.mSwitch")
	for z := range addr.ZoneCount {
		base, _ := addr.ZoneAddr(z)
		add(base+addr.ZoneOffSwitch, whole(addr.ZoneOffItem-addr.ZoneOffSwitch, ruleOR, TargetScoped, pathScoped), fmt.Sprintf("mZone[%d] switches", z))
		add(base+addr.ZoneOffActor, whole(addr.ZoneSize-addr.ZoneOffActor, ruleOR, TargetKills, pathKill), fmt.Sprintf("mZone[%d] dead actors", z))
	}

	add(addr.ItemKeyNumCount, whole(4, ruleSet, TargetCounters, pathDerive), "mItemKeyNumCount, mItemMaxLifeCount") // adjacent s16s (d_com_inf_game.h:799-800)
	add(addr.ItemMaxMagicCount, whole(2, ruleSet, TargetCounters, pathDerive), "mItemMaxMagicCount")
	add(addr.PlaySelectItem, whole(inventory.Buttons, ruleButton, TargetSelectItem, pathDerive), "play.mSelectItem")

	add(addr.NextStage, escapeRules(), "mNextStage")
	add(mailbox.WarpSeq, whole(4, ruleSet, TargetEscape, pathEscape), "mailbox.warp_seq")
	add(WarpPos, whole(12, ruleSet, TargetEscape, pathEscape), "mailbox.warp_x/y/z")
	add(mailbox.BossAddr+mailbox.BossSeqOff, whole(4, ruleSet, TargetBoss, pathBoss), "mailbox.boss_kill seq")
	add(mailbox.BossAddr+mailbox.BossProcOff, bossRules(), "mailbox.boss_kill proc and tag")
	add(mailbox.RefreshAddr+mailbox.RefreshSeqOff, whole(4, ruleSet, TargetRefresh, pathRefresh), "mailbox.actor_refresh seq")
	add(mailbox.RefreshAddr+mailbox.RefreshProcOff, refreshRules(), "mailbox.actor_refresh request")
	return g
}

// bossRules is the boss_kill request's proc and tag (wwapi/mailbox
// boss.go), each byte held to what the REL runs: a proc of bossKillRows
// (Gohma's mailbox.BossProcBtd, Kalle Demos' mailbox.BossProcBmd,
// Gohdan's mailbox.BossProcBst, Molgera's mailbox.BossProcBwd,
// Jalhalla's mailbox.BossProcBpw and Helmaroc King's mailbox.BossProcBdk,
// whatever --coop-boss-kill picks) and mailbox.BossTag. Each proc byte is
// held to the rows' bytes there (ruleIn: 0x00, and 0xD4, 0xDA, 0xEB, 0xEC,
// 0xF0 or 0xF2; TestBossGuardRules checks every byte value), so Bmd's
// hands and the rest in between (0xED..0xEF), Helmaroc King's Bdkobj
// (0xF1), Jalhalla's Poes (0xD5), Molgera's larvae (BWDS 0xDB) and its
// sand floor (BWDG 0xDC) are no value.
func bossRules() []byteRule {
	procs := make([]uint16, len(bossKillRows))
	for i, row := range bossKillRows {
		procs[i] = row.proc
	}
	return procTagRules(procs, mailbox.BossTag, TargetBoss, pathBoss)
}

// procTagRules is a request's u16 proc and u16 tag, big-endian, as rules
// of target tg on path p: each proc byte one of procs' bytes there
// (ruleIn), the tag exactly tag.
func procTagRules(procs []uint16, tag uint16, tg Target, p path) []byteRule {
	var in [2][256]bool
	for _, proc := range procs {
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], proc)
		for j, v := range b {
			in[j][v] = true
		}
	}
	var tb [2]byte
	binary.BigEndian.PutUint16(tb[:], tag)
	rs := make([]byteRule, 4)
	for j := range in {
		rs[j] = byteRule{mask: 0xFF, rule: ruleIn, target: tg, paths: p, in: &in[j]}
	}
	for j, b := range tb {
		rs[2+j] = byteRule{mask: 0xFF, rule: ruleRange, target: tg, paths: p, lo: int8(b), hi: int8(b)}
	}
	return rs
}

// refreshRules is the actor_refresh request's body, +0x04..+0x14
// (wwapi/mailbox refresh.go, mailbox.RefreshRequest.Fields): the proc's
// bytes each one of the allow-list's bytes there (refreshProcs; the REL
// refuses any other proc, so a cross of the bytes, 0x0126 with 0x0192 say,
// costs a refusal), the tag exactly mailbox.RefreshTag, the bit kind a
// mailbox.RefreshBit*, and the pid, params, room and bit number any value
// (the REL compares each with the actor it looks up itself, MISMATCH).
func refreshRules() []byteRule {
	procs := make([]uint16, 0, len(refreshProcs))
	for _, p := range refreshProcs {
		procs = append(procs, p.proc)
	}
	rs := procTagRules(procs, mailbox.RefreshTag, TargetRefresh, pathRefresh)
	set := byteRule{mask: 0xFF, rule: ruleSet, target: TargetRefresh, paths: pathRefresh}
	for range mailbox.RefreshBitKindOff - mailbox.RefreshPIDOff {
		rs = append(rs, set) // pid, params, room
	}
	rs = append(rs, byteRule{mask: 0xFF, rule: ruleRange, target: TargetRefresh, paths: pathRefresh,
		lo: int8(mailbox.RefreshBitSw), hi: int8(mailbox.RefreshBitItem)})
	return append(rs, set, set) // bit no
}

// escapeRules is mNextStage's allowlist (dStage_nextStage_c, d_stage.h:
// 949-968; addr.NextStage), each byte held to what the stage change it
// asks for accepts:
//
//   - the name, char[8]: NUL or a letter, digit or '_', and NUL in the
//     last byte, as dStage_startStage_c::set strcpys it (d_stage.cpp:
//     112-117);
//   - the point, s16, 0..255: a point >= 0 is looked up as the
//     (u8)angle.z of a PLYR entry, and none matching is a JUT_ASSERT
//     (dStage_playerInit, d_stage.cpp:1455-1463). The restart points
//     -1..-3 (d_stage.cpp:1444-1454) take the save's restart data: no
//     escape asks for them;
//   - the room, s8, 0..63 (mStatus[64], d_stage.h:926);
//   - the layer, s8, -1..11: -1 lets the game pick (getLayerNo,
//     d_com_inf_game.cpp:185-190), else it indexes the 12-entry
//     l_layerFuncTable_p (d_stage.cpp:2144-2150);
//   - mEnable, s8, from 0 to 1 only: a write never clears a request the
//     game made, and never re-arms one already pending;
//   - mWipe, s8, 0..11: dScnPly_Draw JUT_ASSERTs it indexes its
//     12-entry l_wipeType (d_s_play.cpp:291-307).
func escapeRules() []byteRule {
	rs := make([]byteRule, addr.NextStageSize)
	set := func(i int, r rule, lo, hi int8) {
		rs[i] = byteRule{mask: 0xFF, rule: r, target: TargetEscape, paths: pathEscape, lo: lo, hi: hi}
	}
	for i := range addr.StageNameLen - 1 {
		set(addr.StageOffName+i, ruleName, 0, 0)
	}
	set(addr.StageOffName+addr.StageNameLen-1, ruleRange, 0, 0)
	set(addr.StageOffPoint, ruleRange, 0, 0) // the s16's high byte
	set(addr.StageOffPoint+1, ruleSet, 0, 0)
	set(addr.StageOffRoomNo, ruleRange, 0, addr.RoomCount-1)
	set(addr.StageOffLayer, ruleRange, -1, MaxStageLayer)
	set(addr.NextOffEnable, ruleArm, 0, 0)
	set(addr.NextOffWipe, ruleRange, 0, MaxWipe)
	return rs
}

// imageRule is what the allowlist says of image byte off, for every
// target.
func imageRule(off int) byteRule {
	r := class.Of(off)
	switch {
	case r.Class == class.Tag:
		// The tag is mReserve +0x00..+0x2F; the rest of mReserve stays
		// zero (§5.2).
		if a := addr.SaveImage + uint32(off); bind.TagRegion.Contains(a, 1) {
			return byteRule{mask: 0xFF, rule: ruleSet, target: TargetTag, paths: pathTag}
		}
		return byteRule{}
	case r.Class == class.Derived:
		derived := byteRule{mask: 0xFF, target: TargetDerived, paths: pathDerive}
		switch i := derivedSlotAt(off); {
		case i >= 0:
			derived.rule, derived.slot = ruleSlot, int8(i)
		case isKeyByte(off):
			derived.rule, derived.lo, derived.hi = ruleRange, 0, derive.MaxKeys
		default:
			// mMaxLife and mMaxMagic change through mItemMaxLifeCount and
			// mItemMaxMagicCount only; mSelectEquip[3] has no setter.
			return byteRule{}
		}
		return derived
	case !r.Class.Merges():
		return byteRule{} // PER_PLAYER, NEVER
	}
	br := byteRule{mask: r.Mask, rule: ruleOR, paths: pathMerge}
	if r.Class == class.Max {
		br.rule = ruleMax
	}
	switch CategoryOf(off) {
	case CatStage, CatSwitch:
		br.target = TargetStage
	case CatOcean:
		br.target = TargetOcean
	case CatStory:
		br.target = TargetStory
		br.mask &= class.SyncMaskOf(off - addr.OffEvent)
	case CatRegister:
		br.target = TargetStory
	case CatInventory:
		br.target = TargetInventory
		br.mask &= inventory.FlagMask(off)
	default: // CatCapacity
		br.target = TargetInventory
	}
	if br.mask == 0 {
		return byteRule{}
	}
	return br
}

// Targets returns the targets g covers.
func (g *Guard) Targets() Target { return g.targets }

// at returns the allowlist's rule for the byte at a; ok is false outside
// it.
func (g *Guard) at(a uint32) (span string, br byteRule, ok bool) {
	return g.list.At(a)
}

// Ranges returns the address set the allowlist spans (write.Range).
func (g *Guard) Ranges() []write.Range { return g.list.Ranges() }

// Mask returns the bits a write may change in the byte at a, 0 if a is
// outside the allowlist.
func (g *Guard) Mask(a uint32) byte {
	_, br, _ := g.at(a)
	return br.mask
}

// TargetOf returns the target the byte at a belongs to, 0 if a is
// outside the allowlist.
func (g *Guard) TargetOf(a uint32) Target {
	_, br, _ := g.at(a)
	return br.target
}

// Allows reports whether every byte of [a, a+n) is in the allowlist.
func (g *Guard) Allows(a uint32, n int) bool {
	if mem.CheckRange(a, n) != nil {
		return false
	}
	for i := range n {
		if g.Mask(a+uint32(i)) == 0 {
			return false
		}
	}
	return true
}

// Check reports why writing b over old at a would be refused: a byte
// outside the allowlist, a changed bit outside its byte's mask, a bit an
// OR byte would lose or a MAX value that would fall. old is what a fresh
// read of the bytes returned, as FrameWriter's read-OR-write does (§4.2).
// It knows nothing of the gates or the policy: WriteGuard.Check does.
func (g *Guard) Check(a uint32, old, b []byte) error {
	if len(old) != len(b) {
		return fmt.Errorf("%w: %d bytes over %d read", ErrGuard, len(b), len(old))
	}
	if err := mem.CheckRange(a, len(b)); err != nil {
		return fmt.Errorf("%w: %v", ErrGuard, err)
	}
	for i := range b {
		at := a + uint32(i)
		_, br, _ := g.at(at)
		if reason, why := judgeByte(br, old[i], b[i]); reason != 0 {
			return &RefusalError{Addr: at, Old: old[i], New: b[i], Reason: reason, Why: why}
		}
	}
	return nil
}

// judgeByte checks a write of val over old against br.
func judgeByte(br byteRule, old, val byte) (Refusal, string) {
	m := br.mask
	if br.rule == ruleNone || m == 0 {
		return RefuseTarget, "it is not a co-op write target"
	}
	if d := (old ^ val) &^ m; d != 0 {
		return RefuseMask, fmt.Sprintf("bits 0x%02X are outside its mask 0x%02X", d, m)
	}
	switch br.rule {
	case ruleOR:
		if c := old &^ val; c != 0 {
			return RefuseClear, fmt.Sprintf("it would clear bits 0x%02X", c)
		}
	case ruleMax:
		if val&m < old&m {
			return RefuseClear, fmt.Sprintf("it would lower %d to %d", old&m, val&m)
		}
	case ruleName:
		if !stageNameByte(val) {
			return RefuseValue, fmt.Sprintf("0x%02X is no stage-name character", val)
		}
	case ruleRange:
		if v := int8(val); v < br.lo || v > br.hi {
			return RefuseValue, fmt.Sprintf("%d is outside %d..%d", v, br.lo, br.hi)
		}
	case ruleIn:
		if br.in == nil || !br.in[val] {
			return RefuseValue, fmt.Sprintf("0x%02X is none of the values it may hold", val)
		}
	case ruleArm:
		if old != 0 || val != 1 {
			return RefuseValue, fmt.Sprintf("it may only go from 0 to 1, not from %d to %d", int8(old), int8(val))
		}
	case ruleSlot:
		if s := derivedSlots[br.slot]; old != val && !betterVariant(s, old, val) {
			return RefuseValue, fmt.Sprintf("the %s slot may only move from nothing or a lesser variant to a better one, not from 0x%02X to 0x%02X",
				s.Name, old, val)
		}
	case ruleButton:
		if old != val && !slices.ContainsFunc(buttonSlots, func(s save.Slot) bool { return betterVariant(s, old, val) }) {
			return RefuseValue, fmt.Sprintf("a button may only move from nothing or a lesser variant to a better one of a derived slot, not from 0x%02X to 0x%02X", old, val)
		}
	}
	return 0, ""
}

// stageNameByte is write.StageNameByte.
func stageNameByte(b byte) bool { return write.StageNameByte(b) }

// Refusal is why the WriteGuard refused a write.
type Refusal uint8

const (
	RefuseOff    Refusal = iota + 1 // co-op is off (turned off, or auto-disabled)
	RefuseTarget                    // the byte is not in the allowlist, the write's addresses don't match its image byte, or a zone byte is not of the zone of Link's room
	RefuseGate                      // a gate the byte's target needs is closed this tick
	RefusePolicy                    // the policy opens none of the byte's bits (an unsynced switch)
	RefuseMask                      // a changed bit outside the byte's mask (class mask and policy)
	RefuseClear                     // a bit an OR byte would lose, or a MAX value that would fall
	RefuseValue                     // a value the byte may not hold: an escape byte the game would reject, a lesser variant, a counter's bad delta
	RefusePath                      // the byte came by a FrameWriter call that may not write it: a DERIVED byte, a counter or a button off the derive path
	numRefusals
)

func (r Refusal) String() string {
	switch r {
	case RefuseOff:
		return "co-op off"
	case RefuseTarget:
		return "not a target"
	case RefuseGate:
		return "gate closed"
	case RefusePolicy:
		return "not in the policy"
	case RefuseMask:
		return "outside the mask"
	case RefuseClear:
		return "a clear"
	case RefuseValue:
		return "a bad value"
	case RefusePath:
		return "the wrong path"
	}
	return fmt.Sprintf("Refusal(%d)", uint8(r))
}

// RefusalError is a refused write. It matches ErrGuard, and ErrOff too
// when co-op was off.
type RefusalError struct {
	Addr     uint32
	Old, New byte
	Reason   Refusal
	What     string // the byte, as the label names it; "" if unnamed
	Why      string
}

func (e *RefusalError) Error() string {
	what := ""
	if e.What != "" {
		what = " (" + e.What + ")"
	}
	return fmt.Sprintf("%v: 0x%08X%s 0x%02X -> 0x%02X: %s", ErrGuard, e.Addr, what, e.Old, e.New, e.Why)
}

// Is makes errors.Is match ErrGuard, and ErrOff for a RefuseOff.
func (e *RefusalError) Is(target error) bool {
	return target == ErrGuard || target == ErrOff && e.Reason == RefuseOff
}

// Tick is what one tick's snapshot tells the WriteGuard (§3.2): the
// gates, ARMED, the save table the live memBit holds and the zone table.
type Tick struct {
	Gates gate.Status
	// Armed: the loaded file is bound to the session's world and passes
	// V1-V6 (§5.2), and the session runs.
	Armed bool
	// Table is mDan.mStageNo: the table whose bits the live memBit holds,
	// -1 when none is loaded.
	Table int8
	// Boss: the loaded stage polls its boss bits every frame
	// (bossStage), so the live memBit's may not change.
	Boss bool
	// Zones is the snapshot's zone table: the room Link is in, each
	// room's zone slot and each slot's room. A zone switch byte is let
	// through only into the zone of the room Link is in (scopedClosed).
	Zones ZoneTable
	// GBA: a Game Boy Advance may be linked to the game
	// (state.GBALink.MayLink), so the memory switches the stage tables
	// share with the Tingle Tuner (GuardConfig.GBA) are shut.
	GBA bool
}

// ZoneTable is the zone allocation one snapshot read (§3.1): mStayNo,
// each room's mStatus.mZoneNo and each zone slot's mRoomNo. Slots are
// allocated first-free on each machine (createZone, d_save.cpp:1525-1537)
// and freed by removeZone setting mRoomNo to -1 alone (d_save.h:969,
// :793), so a slot is a room's only while the two name each other.
type ZoneTable struct {
	// Valid: the table was read (ZoneTableOf); the zero ZoneTable lets no
	// zone byte through.
	Valid  bool
	Stay   int8
	ZoneNo [addr.RoomCount]int8
	RoomNo [addr.ZoneCount]int8
}

// ZoneTableOf is s's zone table. mStayNo, mZoneNo and the zones' mRoomNo
// come from the snapshot's own reads (state.Read).
func ZoneTableOf(s *state.Snapshot) ZoneTable {
	zt := ZoneTable{Valid: true, Stay: s.StayNo, ZoneNo: s.ZoneNo}
	for z := range addr.ZoneCount {
		zt.RoomNo[z] = int8(s.Save[addr.InfoOffZone+z*addr.ZoneSize+addr.ZoneOffRoomNo])
	}
	return zt
}

// SlotOf returns the zone slot of room: mStatus[room].mZoneNo, if it is
// 0..31 and that slot's mRoomNo is room (the §3.1 rule, as
// state.Snapshot.ZoneFor has it).
func (zt *ZoneTable) SlotOf(room int) (int, bool) {
	if !zt.Valid || room < 0 || room >= addr.RoomCount {
		return 0, false
	}
	z := int(zt.ZoneNo[room])
	if z < 0 || z >= addr.ZoneCount || int(zt.RoomNo[z]) != room {
		return 0, false
	}
	return z, true
}

// scopedClosed returns why the byte at a, a dan or zone switch byte
// (TargetScoped), may not be written this tick, or "":
//
//   - a dan byte needs a save table in mDan.mStageNo: the dan switches
//     are the table initDan last ran for (dSv_danBit_c::init,
//     d_save.cpp:1225-1235);
//   - a zone byte needs the zone table, and its slot to be the zone of the
//     room Link is in: the slot's mRoomNo is that room, and the room's
//     mZoneNo names the slot back, as onSwitch's lookup and asserts have it
//     (dComIfGp_roomControl_getZoneNo, d_save.cpp:1552-1556). A freed slot
//     keeps its old bits (removeZone, d_save.h:969), and a slot reused for
//     another room is that room's, so neither takes a write.
func (t *Tick) scopedClosed(a uint32) string {
	if a >= addr.DanSwitch && a < addr.DanSwitch+addr.DanSize-addr.DanOffSwitch {
		if !addr.ValidTable(int(t.Table)) {
			return "mDan.mStageNo holds no save table, so the dan switches are no table's"
		}
		return ""
	}
	if a < addr.Zones || a >= addr.Zones+addr.ZoneCount*addr.ZoneSize {
		return "it is no dan or zone switch byte"
	}
	z := int(a-addr.Zones) / addr.ZoneSize
	zt := &t.Zones
	room := int(zt.RoomNo[z])
	switch slot, ok := zt.SlotOf(room); {
	case !zt.Valid:
		return "no zone table was read this tick"
	case !ok || slot != z:
		zn := int8(-1)
		if room >= 0 && room < addr.RoomCount {
			zn = zt.ZoneNo[room]
		}
		return fmt.Sprintf("zone slot %d is no room's: its mRoomNo is %d, whose mZoneNo is %d", z, room, zn)
	case room != int(zt.Stay):
		return fmt.Sprintf("zone slot %d is room %d's, but Link is in room %d", z, room, zt.Stay)
	}
	return ""
}

// opens returns "" if target tg may be written at a this tick, or why
// not. The rules follow §3.2 and §6.3-6.5:
//
//   - every target but the tag needs ARMED (a bind writes the tag of a
//     file not yet bound);
//   - stage progress bits and memory switches need STABLE and HEARTBEAT,
//     the gates of any write: another table's are written while the
//     game is not IDLE too (Appendix C item 128). When within that a
//     planner may write them (the current table's bits when IDLE, its
//     switches at a room change, §6.3) is the planner's rule, which the
//     CI write audit checks;
//   - the ocean, the charts and the sea map need IDLE as well: the sea
//     chart writes them while the pause menu is open (d_menu_fmap.cpp:
//     3271, d_menu_fmap2.cpp:1045; Appendix C item 129);
//   - story bits and the allowlisted registers (TargetStory) are written
//     only on the TRANSITION tick (§6.5): gate.Status.OnTransition, with
//     HEARTBEAT and ARMED. OnTransition is STABLE's other conditions
//     (Link loaded, mpPlayerPtr[0] != 0; a stage that is neither the
//     title nor file select; a valid save table in mDan.mStageNo; the
//     stage, its start point, room and layer, the table and mStayNo
//     unchanged for gate.StableTicks ticks before this one) on the first
//     tick mNextStage.mEnable reads 1, the one STABLE condition it waives.
//     IDLE is not needed: a door or a cutscene's end asks for the stage
//     change from inside its event;
//   - a pending counter needs IDLE and to read 0 on this tick's snapshot
//     (§3.2); the derive path also reads it 0 again after the beat;
//   - everything else, the inventory, the derived bytes and the buttons,
//     needs IDLE as well.
//
// The live memBit's boss bits are shut apart (bossClosed).
func (t *Tick) opens(tg Target, a uint32) string {
	st := t.Gates
	if tg != TargetTag && !t.Armed {
		return "the file is not armed in this world"
	}
	switch tg {
	case TargetStage:
		if !st.CanWrite() {
			return closedWhy(st)
		}
	case TargetStory:
		switch {
		case !st.Heartbeat:
			return closedWhy(st)
		case !st.OnTransition:
			return "story bits are written only as a stage change starts (§6.5)"
		}
	case TargetCounters:
		c := counterAt(a)
		switch {
		case !st.CanWriteIdle():
			return closedWhy(st)
		case !st.CanWriteCounter(c):
			return fmt.Sprintf("the %s counter still holds %d: the HUD has not taken it yet", c, st.Pending(c))
		}
	default:
		if !st.CanWriteIdle() {
			return closedWhy(st)
		}
	}
	return ""
}

// spawnMask returns the start-event spawn switch bits (spawnSwitch) of the
// byte at a, saved or, with its table loaded, live, on a TRANSITION tick
// of an armed file (CanWriteOnTransition), and 0 otherwise. On that tick,
// whose STABLE is closed, the story write's stage change may write these
// bits, and only these, with the story bit their event sets
// (Replica.spawnWrites), so the receiver never enters the stage with the
// bit and not its switch.
func (t *Tick) spawnMask(a uint32) byte {
	if !t.Armed || !t.Gates.CanWriteOnTransition() {
		return 0
	}
	switch {
	case a >= addr.SaveImage && a < addr.SaveImage+addr.SaveImageSize:
		return spawnMask(int(a - addr.SaveImage))
	case a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize && addr.ValidTable(int(t.Table)):
		return spawnMask(addr.OffMemBit(int(t.Table)) + int(a-addr.LiveMemBit))
	}
	return 0
}

// bossClosed returns why a write of val over old at a, the live memBit's
// mDungeonItem, may not change its boss bits this tick, or "": the stage
// loaded polls them every frame (bossStage), so the planner holds them
// there (bossHold), and a plan with them is a bug.
func (t *Tick) bossClosed(a uint32, old, val byte) string {
	if t.Boss && a == addr.LiveMemBit+addr.MemBitOffDungeonItem && (old^val)&bossBits != 0 {
		return "the boss stage loaded reads the boss bits every frame (d_meter.cpp:1071, d_a_dr2.cpp:561)"
	}
	return ""
}

// closedWhy is why a gate of st is closed. It is never "", which opens
// reads as open: a Status whose gates are closed without a reason (one
// not made by gate.Gates) still shuts the guard.
func closedWhy(st gate.Status) string {
	if why := st.Why(); why != "" {
		return why
	}
	switch {
	case !st.Heartbeat:
		return "HEARTBEAT is closed"
	case !st.Stable:
		return "STABLE is closed"
	}
	return "IDLE is closed"
}

// counterAt is the pending counter whose bytes hold a.
func counterAt(a uint32) gate.Counter {
	switch {
	case a >= addr.ItemMaxLifeCount && a < addr.ItemMaxLifeCount+2:
		return gate.MaxLife
	case a >= addr.ItemMaxMagicCount && a < addr.ItemMaxMagicCount+2:
		return gate.MaxMagic
	}
	return gate.KeyNum
}

// GuardConfig sets up a WriteGuard.
type GuardConfig struct {
	// Targets is what the guard lets through; 0 is M3Targets.
	Targets Target
	// Policy narrows the merging image bytes, saved and live, to the bits
	// that sync: the memory switches the M1 table marks latching
	// (world.Policy.GrantSwitches). Nil is world.DefaultPolicy, which
	// opens no switch.
	Policy *world.Policy
	// GBA is the memory switch bits that sync only while no Game Boy
	// Advance is linked (synctables.GBAMemorySwitch): shut on a tick
	// whose Tick.GBA is set. Nil shuts none.
	GBA *world.Policy
	// Breaker gets every refusal (but those made while co-op is off) as
	// a fault; nil makes one with the default thresholds.
	Breaker *Breaker
	// Logf gets the refusals, rate-limited; nil discards them.
	Logf func(format string, args ...any)
	// Label names an image byte (bit -1) for the log; nil is DefaultLabel.
	Label func(off, bit int) string
}

// WriteGuard is the guard every co-op game write passes (§4.2): the
// Guard's allowlist for the configured targets, narrowed by the policy,
// opened per tick by the gates (SetTick) and shut for good by its
// Breaker. Check refuses, logs and counts, and reports to the Breaker:
//
//   - any write while co-op is off;
//   - a byte outside the allowlist: PER_PLAYER, NEVER and TAG bytes,
//     mMaxLife and mMaxMagic, padding, the mod's own RAM, and in M3 the
//     DERIVED bytes;
//   - a byte that came by a FrameWriter call its rule does not name
//     (path): a DERIVED byte, a pending counter or a button's item off
//     the derive path, an image byte through an escape, mNextStage
//     through a plan; and a derive write whose address, size, expected
//     bytes or delta are not those of its kind (checkDerive);
//   - any write while a gate the byte's target needs is closed, before
//     the first tick and after Close;
//   - a bit the policy does not open (an unsynced memory switch), or any
//     other bit outside the byte's mask, and, while a GBA may be linked
//     (Tick.GBA), a memory switch shared with the Tingle Tuner
//     (GuardConfig.GBA);
//   - a clear: the new byte must hold every bit of the old one (a MAX
//     byte, a capacity, must not fall), and a slot, an equipment byte or
//     a button must not move to a lesser variant;
//   - a change to the live memBit's boss bits while a boss stage is
//     loaded (Tick.Boss);
//   - a dan switch byte while mDan holds no save table, and a zone switch
//     byte outside the zone of the room Link is in, by the tick's zone
//     table (Tick.scopedClosed).
//
// A live memBit byte takes the classes and policy of the table SetTick
// says is loaded, and nothing while none is. A WriteGuard is safe for
// concurrent use.
type WriteGuard struct {
	allow  *Guard
	policy world.Policy
	gba    world.Policy // GuardConfig.GBA
	brk    *Breaker
	logf   func(format string, args ...any)
	label  func(off, bit int) string

	mu   sync.Mutex
	tick Tick
	have bool // SetTick was called
	// combat is the tick's M6 plan (SetCombat), and shrunk the carriers
	// whose timer this tick's light-shrink wrote and read back (noteShrunk):
	// both start over at SetTick.
	combat   CombatTick
	combatOK bool
	shrunk   map[uint32]uint32 // carrier address -> its pid
	closed   string            // why the guard is shut until the next SetTick
	checked  int
	refused  [numRefusals]int
	logged   int
}

// NewWriteGuard returns a WriteGuard, shut until the first SetTick.
func NewWriteGuard(cfg GuardConfig) *WriteGuard {
	t := cfg.Targets
	if t == 0 {
		t = M3Targets
	}
	w := &WriteGuard{allow: NewGuardFor(t), brk: cfg.Breaker, logf: cfg.Logf, label: cfg.Label}
	if cfg.Policy != nil {
		w.policy = *cfg.Policy
	} else {
		w.policy = *world.DefaultPolicy()
	}
	if cfg.GBA != nil {
		w.gba = *cfg.GBA
	}
	if w.brk == nil {
		w.brk = NewBreaker(BreakerConfig{Logf: cfg.Logf})
	}
	if w.logf == nil {
		w.logf = func(string, ...any) {}
	}
	if w.label == nil {
		w.label = DefaultLabel
	}
	return w
}

// Allowlist returns the static allowlist the guard checks against.
func (w *WriteGuard) Allowlist() *Guard { return w.allow }

// Breaker returns the guard's Breaker.
func (w *WriteGuard) Breaker() *Breaker { return w.brk }

// SetTick opens the guard for one tick's gates, replacing the last tick's
// and any Close.
func (w *WriteGuard) SetTick(t Tick) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tick, w.have, w.closed = t, true, ""
	w.combat, w.combatOK, w.shrunk = CombatTick{}, false, nil
}

// Close shuts the guard until the next SetTick: the game moved on since
// the tick's snapshot (FrameWriter's re-check), or the snapshot could not
// be read.
func (w *WriteGuard) Close(why string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = why
	if w.closed == "" {
		w.closed = "closed"
	}
}

// Mask returns the bits a write may change in the byte at a this tick:
// the allowlist's mask narrowed by the policy (for a live memBit byte,
// the loaded table's) and, while a GBA may be linked, without the
// switches shared with the Tingle Tuner; 0 when the byte is no target or
// no table is loaded. It does not look at the gates.
func (w *WriteGuard) Mask(a uint32) byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, br, _ := w.rule(a)
	return br.mask
}

// rule is the allowlist's rule for a with the policy applied, and what
// to call the byte. w.mu is held.
func (w *WriteGuard) rule(a uint32) (string, byteRule, Refusal) {
	span, br, ok := w.allow.at(a)
	if !ok {
		return w.name(a, span), byteRule{}, RefuseTarget
	}
	off := -1
	switch {
	case a >= addr.SaveImage && a < addr.SaveImage+addr.SaveImageSize:
		off = int(a - addr.SaveImage)
	case a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize:
		if !addr.ValidTable(int(w.tick.Table)) {
			return w.name(a, span), byteRule{}, RefuseTarget
		}
		off = addr.OffMemBit(int(w.tick.Table)) + int(a-addr.LiveMemBit)
	}
	if off >= 0 && (br.rule == ruleOR || br.rule == ruleMax) {
		br.mask &= w.policy[off]
		if w.tick.GBA {
			br.mask &^= w.gba[off]
		}
		if br.mask == 0 {
			return w.name(a, span), byteRule{}, RefusePolicy
		}
	}
	return w.name(a, span), br, 0
}

// name is what the log calls the byte at a. w.mu is held.
func (w *WriteGuard) name(a uint32, span string) string {
	switch {
	case a >= addr.SaveImage && a < addr.SaveImage+addr.SaveImageSize:
		return w.label(int(a-addr.SaveImage), -1)
	case a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize:
		rel := int(a - addr.LiveMemBit)
		if addr.ValidTable(int(w.tick.Table)) {
			return "live " + w.label(addr.OffMemBit(int(w.tick.Table))+rel, -1)
		}
		return fmt.Sprintf("live memBit+0x%02X", rel)
	}
	return span
}

// Check reports whether the byte at a may go from old, what a fresh
// 1-byte read returned, to val this tick, written by the path the byte's
// rule names: it is the verdict on one byte, whoever asks. The
// FrameWriter's own calls each ask as themselves (check), so a byte is
// written only by its path, and the derive path asks for a whole
// DeriveWrite at once (checkDerive). A refusal is a *RefusalError; it is
// counted and logged, and reported to the Breaker unless co-op was
// already off.
func (w *WriteGuard) Check(a uint32, old, val byte) error { return w.check(a, old, val, pathAny) }

// check is Check for a write that came by path p.
func (w *WriteGuard) check(a uint32, old, val byte, p path) error {
	w.mu.Lock()
	w.checked++
	what, reason, why := w.judge(a, old, val, p)
	if reason == 0 {
		w.mu.Unlock()
		return nil
	}
	return w.refuse(&RefusalError{Addr: a, Old: old, New: val, Reason: reason, What: what, Why: why})
}

// judge is check's verdict. w.mu is held.
func (w *WriteGuard) judge(a uint32, old, val byte, p path) (string, Refusal, string) {
	if w.brk.Off() {
		return "", RefuseOff, w.brk.Why()
	}
	what, br, reason := w.rule(a)
	switch reason {
	case RefuseTarget:
		if a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize && w.allow.Mask(a) != 0 {
			return what, reason, "no save table is loaded, so the live memBit is no table's"
		}
		return what, reason, "it is not a co-op write target"
	case RefusePolicy:
		return what, reason, "the policy opens none of its bits (a switch that does not sync)"
	}
	if br.paths&p == 0 {
		return what, RefusePath, fmt.Sprintf("it is written only by %s, not by %s", br.paths, p)
	}
	switch {
	case !w.have:
		return what, RefuseGate, "no tick has opened the gates yet"
	case w.closed != "":
		return what, RefuseGate, w.closed
	}
	if why := w.tick.opens(br.target, a); why != "" {
		m := byte(0)
		if br.target == TargetStage {
			m = w.tick.spawnMask(a)
		}
		if m == 0 {
			return what, RefuseGate, why
		}
		br.mask &= m // a stage change's spawn switch: those bits alone
	}
	if why := w.tick.bossClosed(a, old, val); why != "" {
		return what, RefuseGate, why
	}
	if br.target == TargetScoped {
		if why := w.tick.scopedClosed(a); why != "" {
			return what, RefuseTarget, why
		}
	}
	if br.target == TargetKills {
		if why := w.killClosed(a, old, val); why != "" {
			return what, RefuseTarget, why
		}
	}
	r, why := judgeByte(br, old, val)
	return what, r, why
}

// refuse counts and logs e, reports it to the Breaker, and returns it.
// w.mu is held and is released.
func (w *WriteGuard) refuse(e *RefusalError) error {
	w.refused[e.Reason]++
	if e.Reason == RefuseOff {
		w.mu.Unlock()
		return e
	}
	w.logged++
	n := w.logged
	w.mu.Unlock()
	if n <= 16 || n%100 == 0 {
		w.logf("co-op: %v (refusal %d)", e, n)
	}
	w.brk.Fault(FaultRefusal, e.Error())
	return e
}

// CheckWrite checks that a planned write's addresses are the ones its
// image byte has this tick: Saved is addr.SaveImage + Off, and Live is
// set exactly when Off is in the saved slot of the table the live memBit
// holds, at that byte's place in it. A write planned for another table
// would put its bits into the wrong table's live copy. A mismatch is a
// refusal (RefuseTarget), counted like Check's.
func (w *WriteGuard) CheckWrite(wr Write) error {
	w.mu.Lock()
	why := ""
	off := int(wr.Off)
	switch t, rel, inMem := memBitTable(off); {
	case off >= addr.SaveImageSize:
		why = fmt.Sprintf("image offset 0x%X is outside the image", off)
	case wr.Saved != addr.SaveImage+uint32(off):
		why = fmt.Sprintf("its saved address 0x%08X is not image byte 0x%03X's", wr.Saved, off)
	case wr.Live == 0:
		if inMem && w.have && t == int(w.tick.Table) {
			why = fmt.Sprintf("it lacks the live copy of table %d, which is loaded", t)
		}
	case !inMem || wr.Live != addr.LiveMemBit+uint32(rel):
		why = fmt.Sprintf("its live address 0x%08X is not image byte 0x%03X's", wr.Live, off)
	case !w.have || t != int(w.tick.Table):
		why = fmt.Sprintf("it was planned with table %d loaded, but the live memBit holds table %d", t, w.tick.Table)
	}
	if why == "" {
		w.mu.Unlock()
		return nil
	}
	w.checked++
	return w.refuse(&RefusalError{Addr: wr.Saved, Reason: RefuseTarget, What: w.label(off, -1), Why: why})
}

// checkDerive reports whether dw, a write of the derive path
// (FrameWriter.ApplyDerived), may be made over old, what a fresh read of
// its bytes returned, this tick: it must have its kind's shape
// (deriveShape), its bytes must still read dw.Expect (the writer drops one
// that does not before asking), and each byte must pass as the derive
// path's (judge: the targets, ARMED and the gates, the policy, the
// byte's value rule). It is the one way a DERIVED byte, a pending counter
// or a button's item is let through. A refusal is counted, logged and
// reported as Check's.
func (w *WriteGuard) checkDerive(dw DeriveWrite, old []byte) error {
	w.mu.Lock()
	w.checked++
	e := &RefusalError{Addr: dw.Addr, What: dw.Kind.String()}
	if len(old) > 0 && len(dw.Data) > 0 {
		e.Old, e.New = old[0], dw.Data[0]
	}
	if w.brk.Off() {
		e.Reason, e.Why = RefuseOff, w.brk.Why()
		return w.refuse(e)
	}
	if e.Reason, e.Why = w.deriveShape(dw, old); e.Reason != 0 {
		return w.refuse(e)
	}
	for i := range dw.Data {
		a := dw.Addr + uint32(i)
		if what, reason, why := w.judge(a, old[i], dw.Data[i], pathDerive); reason != 0 {
			return w.refuse(&RefusalError{Addr: a, Old: old[i], New: dw.Data[i], Reason: reason, What: what, Why: why})
		}
	}
	w.mu.Unlock()
	return nil
}

// checkDeriveShape is checkDerive before the beat, without a read: dw
// must have its kind's shape over its own Expect (deriveShape), and every
// byte must be the derive path's (the targets, the policy, the path),
// whatever the gates say this tick. A write that fails it is a bug in its
// plan, refused before the wait as a byte off the allowlist is; a closed
// gate is ready's to report. A refusal is counted, logged and reported as
// Check's.
func (w *WriteGuard) checkDeriveShape(dw DeriveWrite) error {
	w.mu.Lock()
	e := &RefusalError{Addr: dw.Addr, What: dw.Kind.String()}
	if len(dw.Expect) > 0 && len(dw.Data) > 0 {
		e.Old, e.New = dw.Expect[0], dw.Data[0]
	}
	switch {
	case w.brk.Off():
		e.Reason, e.Why = RefuseOff, w.brk.Why()
	default:
		e.Reason, e.Why = w.deriveShape(dw, dw.Expect)
	}
	for i := 0; e.Reason == 0 && i < len(dw.Data); i++ {
		a := dw.Addr + uint32(i)
		what, br, reason := w.rule(a)
		why := ""
		switch {
		case reason == RefuseTarget:
			why = "it is not a co-op write target"
		case reason == RefusePolicy:
			why = "the policy opens none of its bits"
		case br.paths&pathDerive == 0:
			reason, why = RefusePath, fmt.Sprintf("it is written only by %s, not by %s", br.paths, pathDerive)
		}
		if reason != 0 {
			e = &RefusalError{Addr: a, Old: dw.Expect[i], New: dw.Data[i], Reason: reason, What: what, Why: why}
		}
	}
	if e.Reason == 0 {
		w.mu.Unlock()
		return nil
	}
	w.checked++
	return w.refuse(e)
}

// deriveShape returns why dw is not a write of its kind over old, or 0:
//
//   - a counter (DeriveKeyCounter, DeriveLifeCounter, DeriveMagicCounter)
//     is its s16, both bytes in one write, over a counter that reads 0,
//     with a delta the HUD takes whole: a key count of -99..99, not 0,
//     for the loaded table (dMeter_keyMove clamps mKeyNum to 0..99,
//     d_meter.cpp:5248-5254); max life 1..0x50 quarter hearts and max
//     magic 1..0x20, so neither is ever lowered (the HUD's caps,
//     dMeter_LifeMove, d_meter.cpp:1432-1436; dMeter_magicGaugeMove,
//     :4069-4076);
//   - a key byte is the loaded table's live mKeyNum (DeriveKeyLive) or
//     another table's saved one (DeriveKeySaved): the loaded table's
//     saved byte is putSave's to write (d_save.cpp:1512-1515);
//   - a slot or equipment byte is one derivedSlots names, of its kind;
//   - a button is one of play.mSelectItem[0..2].
//
// w.mu is held.
func (w *WriteGuard) deriveShape(dw DeriveWrite, old []byte) (Refusal, string) {
	n := len(dw.Data)
	switch {
	case n == 0 || len(dw.Expect) != n || len(old) != n:
		return RefuseTarget, fmt.Sprintf("%d bytes over %d expected and %d read", n, len(dw.Expect), len(old))
	case !bytes.Equal(old, dw.Expect):
		return RefuseValue, fmt.Sprintf("its bytes read % X, not the % X it expects", old, dw.Expect)
	}
	loaded := int(w.tick.Table)
	if !w.have {
		loaded = -1
	}
	at := func(a uint32, size int) string {
		if dw.Addr != a || n != size {
			return fmt.Sprintf("a %s write is %d byte(s) at 0x%08X, not %d at 0x%08X", dw.Kind, size, a, n, dw.Addr)
		}
		return ""
	}
	switch dw.Kind {
	case DeriveKeyCounter, DeriveLifeCounter, DeriveMagicCounter:
		a, lo, hi := addr.ItemKeyNumCount, -derive.MaxKeys, derive.MaxKeys
		switch dw.Kind {
		case DeriveLifeCounter:
			a, lo, hi = addr.ItemMaxLifeCount, 1, derive.LifeCap
		case DeriveMagicCounter:
			a, lo, hi = addr.ItemMaxMagicCount, 1, derive.MagicCap
		}
		if why := at(a, 2); why != "" {
			return RefuseTarget, why
		}
		if dw.Expect[0] != 0 || dw.Expect[1] != 0 {
			return RefuseValue, "a pending counter is written only while it reads 0"
		}
		if d := int(int16(binary.BigEndian.Uint16(dw.Data))); d == 0 || d < lo || d > hi {
			return RefuseValue, fmt.Sprintf("a %s delta of %d is outside %d..%d (never 0, and max life and magic only rise)", dw.Kind, d, lo, hi)
		}
		if dw.Kind == DeriveKeyCounter && dw.Table != loaded {
			return RefuseTarget, fmt.Sprintf("the key counter lands on the loaded table %d, not table %d", loaded, dw.Table)
		}
	case DeriveKeyLive:
		if why := at(addr.LiveKeyNum, 1); why != "" {
			return RefuseTarget, why
		}
		if dw.Table != loaded {
			return RefuseTarget, fmt.Sprintf("the live mKeyNum is table %d's, not table %d's", loaded, dw.Table)
		}
	case DeriveKeySaved:
		saved, err := addr.SavedMemBitAddr(dw.Table)
		if err != nil {
			return RefuseTarget, err.Error()
		}
		if why := at(saved+addr.MemBitOffKeyNum, 1); why != "" {
			return RefuseTarget, why
		}
		if dw.Table == loaded {
			return RefuseTarget, fmt.Sprintf("table %d is loaded: its count is the live mKeyNum, which putSave copies to this one", loaded)
		}
	case DeriveSlot, DeriveEquip:
		i := -1
		if dw.Addr >= addr.SaveImage && dw.Addr < addr.SaveImage+addr.SaveImageSize {
			i = derivedSlotAt(int(dw.Addr - addr.SaveImage))
		}
		if isEquip := i >= len(buttonSlots); i < 0 || n != 1 || isEquip != (dw.Kind == DeriveEquip) {
			return RefuseTarget, fmt.Sprintf("0x%08X is no derived %s byte", dw.Addr, dw.Kind)
		}
	case DeriveButton:
		if n != 1 || dw.Addr < addr.PlaySelectItem || dw.Addr >= addr.PlaySelectItem+inventory.Buttons {
			return RefuseTarget, fmt.Sprintf("0x%08X is no button of play.mSelectItem[0..%d]", dw.Addr, inventory.Buttons-1)
		}
	default:
		return RefuseTarget, fmt.Sprintf("%v is no derive write", dw.Kind)
	}
	return 0, ""
}

// on reports whether path p may write the byte at a: false outside the
// allowlist.
func (w *WriteGuard) on(a uint32, p path) bool {
	_, br, ok := w.allow.at(a)
	return ok && br.paths&p != 0
}

// ready returns nil if the byte at a, which the allowlist has, may be
// written this tick as far as the gates go, or ErrClosed with why not. A
// byte outside the allowlist is ready: Check refuses it and counts it.
func (w *WriteGuard) ready(a uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.brk.Off() {
		return w.brk.Err()
	}
	_, br, ok := w.allow.at(a)
	switch {
	case !ok:
		return nil
	case !w.have:
		return fmt.Errorf("%w: no tick has opened the gates yet", ErrClosed)
	case w.closed != "":
		return fmt.Errorf("%w: %s", ErrClosed, w.closed)
	}
	if why := w.tick.opens(br.target, a); why != "" {
		return fmt.Errorf("%w: %s", ErrClosed, why)
	}
	return nil
}

// readyBits is ready for a merge-path byte that would set bits at a: it
// returns the bits that may be written this tick. Where ready closes the
// byte, a stage change's first tick still opens its start-event spawn
// switch bits (Tick.spawnMask), and only those: the rest of bits stays
// closed (ErrClosed, no fault), so a batch that merged a spawn bit with
// another bit of its byte still writes the spawn bit alone.
func (w *WriteGuard) readyBits(a uint32, bits byte) (byte, error) {
	err := w.ready(a)
	if err == nil || !errors.Is(err, ErrClosed) {
		return bits, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, br, ok := w.allow.at(a); ok && br.target == TargetStage && w.have && w.closed == "" {
		if m := w.tick.spawnMask(a) & bits; m != 0 {
			return m, nil
		}
	}
	return 0, err
}

// GuardStats are a WriteGuard's counts.
type GuardStats struct {
	Checked  int // writes checked
	Refused  int // writes refused, for any reason
	ByReason map[Refusal]int
}

// Stats returns the guard's counts.
func (w *WriteGuard) Stats() GuardStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := GuardStats{Checked: w.checked, ByReason: map[Refusal]int{}}
	for r, n := range w.refused {
		if n > 0 {
			st.ByReason[Refusal(r)] = n
			st.Refused += n
		}
	}
	return st
}
