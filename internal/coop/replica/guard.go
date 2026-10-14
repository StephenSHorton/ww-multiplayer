package replica

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
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
	// TargetStory is the named event bits and the allowlisted event
	// registers (§6.5). M4.
	TargetStory
	// TargetInventory is mGetItem, mCollect[0..4], the songs, Triforce,
	// pearls, beast and bait flags, and the MAX capacities (§6.6). M4.
	TargetInventory
	// TargetDerived is what each receiver derives: the mItems slots,
	// mSelectEquip and every table's mKeyNum, saved and live (§6.4,
	// §6.6). mMaxLife and mMaxMagic are not in it: they change only
	// through their pending counters (§3). M4.
	TargetDerived
	// TargetCounters is the HUD's pending counters mItemKeyNumCount,
	// mItemMaxLifeCount and mItemMaxMagicCount (§6.4). M4.
	TargetCounters
	// TargetSelectItem is play.mSelectItem, refreshed after a slot
	// changes (§6.6). M4.
	TargetSelectItem
	// TargetScoped is the dan switch words and every zone's switch words,
	// which a writer still validates against the live zone table (§3.1).
	// M5.
	TargetScoped
	// TargetKills is every zone's dead-actor words (§7.2). M6. The K1
	// timer bytes live in actors on the heap, so M6 validates them per
	// write; they are in no static list.
	TargetKills
	// TargetTag is the binding tag, the first 0x30 bytes of mReserve
	// (bind.TagRegion). Only a bind and a scrub write it (§5.2), and
	// they do it through package bind, never through a FrameWriter.
	TargetTag
	// TargetEscape is what the escape hatches write (§6.7, from M3):
	// mNextStage, for "refresh area" and a warp to a partner on another
	// stage, and the mailbox's warp request (warp_seq and warp_x/y/z,
	// mailbox.h:221-225), for a warp on the same stage. Each byte takes
	// only the values the game accepts there (escapeRules); warp_ack and
	// warp_force are not in it. They are no save data, and nothing of
	// the world: FrameWriter.ApplyFields writes them, on the user's word
	// alone, when IDLE.
	TargetEscape

	// AllTargets is the most any milestone writes: NewGuard's allowlist.
	AllTargets = TargetStage | TargetOcean | TargetStory | TargetInventory | TargetDerived |
		TargetCounters | TargetSelectItem | TargetScoped | TargetKills | TargetTag | TargetEscape
	// M3Targets is what M3's reconciler writes (§9 M3): the stage
	// progress bits and memory switches, the ocean, the charts and the
	// sea map. A replica's WriteGuard adds TargetEscape for the escape
	// hatches M3 ships too.
	M3Targets = TargetStage | TargetOcean
)

var targetNames = [...]string{
	"stage", "ocean", "story", "inventory", "derived",
	"counters", "select-item", "scoped", "kills", "tag", "escape",
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
	ruleNone  rule = iota // not a write target
	ruleOR                // set bits within the mask, never clear one: OR and OR_MASKED bytes, the scoped words
	ruleMax               // raise the value within the mask, never lower it: MAX bytes
	ruleSet               // any value within the mask: DERIVED bytes, the counters, mSelectItem, the tag, the warp request
	ruleName              // a stage name's byte: NUL, or a letter, digit or '_'
	ruleRange             // a signed value in [lo, hi]
	ruleArm               // 0 to 1 and nothing else: mNextStage.mEnable
)

// byteRule is what the allowlist says of one byte.
type byteRule struct {
	mask   byte // the bits a write may change
	rule   rule
	target Target
	lo, hi int8 // ruleRange's bounds
}

// Guard is the static allowlist of the WriteGuard (§4.2): the bytes the
// co-op pipeline may write, the bits of each it may change and how, for a
// set of Targets. It is computed from the class table and the address
// table. AllTargets covers:
//
//   - the image bytes that merge (OR, OR_MASKED, MAX: their class mask)
//     and the DERIVED bytes a receiver recomputes (mItems slots,
//     mSelectEquip, each table's mKeyNum), except mMaxLife and
//     mMaxMagic, which change only through their pending counters (§3);
//   - the live memBit, with the classes of a saved slot;
//   - the dan switch words (mDan.mSwitch) and every zone's switch and
//     dead-actor words (dSv_zone_c +0x02..+0x07 and +0x0C..+0x4B; the
//     zone's own item bits and its mRoomNo are not), which a writer
//     still validates against the live zone table (§3.1);
//   - the three pending counters, mItemKeyNumCount, mItemMaxLifeCount
//     and mItemMaxMagicCount (§6.4);
//   - play.mSelectItem, refreshed after a slot changes (§6.6);
//   - the binding tag (bind.TagRegion), the first 0x30 bytes of the
//     image's mReserve;
//   - the escape hatches' mNextStage and mailbox warp request (§6.7),
//     each byte held to the values the game accepts (escapeRules).
//
// Nothing per player is in it: not mLife (a 0 kills Link, §5.1), not
// rupees, ammo, bottles, names or options, and no padding.
//
// A merging byte is grow-only: an OR byte may gain bits but never lose
// one, a MAX byte may rise but never fall. DERIVED bytes, the counters,
// mSelectItem and the tag take any value within the mask; M4 adds their
// own rules. A Guard is immutable and safe for concurrent use; a
// WriteGuard adds the gates and the policy.
type Guard struct {
	targets Target
	spans   []guardSpan // ascending, non-overlapping
}

type guardSpan struct {
	start uint32
	mask  []byte // the bits each byte may change
	rules []byteRule
	name  string
}

func (s guardSpan) end() uint32 { return s.start + uint32(len(s.mask)) }

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
			g.spans = append(g.spans, guardSpan{start: start, mask: mask, rules: rules, name: name})
		}
	}
	whole := func(n int, r rule, tg Target) []byteRule {
		rs := make([]byteRule, n)
		for i := range rs {
			rs[i] = byteRule{mask: 0xFF, rule: r, target: tg}
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

	add(addr.DanSwitch, whole(addr.DanSize-addr.DanOffSwitch, ruleOR, TargetScoped), "mDan.mSwitch")
	for z := range addr.ZoneCount {
		base, _ := addr.ZoneAddr(z)
		add(base+addr.ZoneOffSwitch, whole(addr.ZoneOffItem-addr.ZoneOffSwitch, ruleOR, TargetScoped), fmt.Sprintf("mZone[%d] switches", z))
		add(base+addr.ZoneOffActor, whole(addr.ZoneSize-addr.ZoneOffActor, ruleOR, TargetKills), fmt.Sprintf("mZone[%d] dead actors", z))
	}

	add(addr.ItemKeyNumCount, whole(4, ruleSet, TargetCounters), "mItemKeyNumCount, mItemMaxLifeCount") // adjacent s16s (d_com_inf_game.h:799-800)
	add(addr.ItemMaxMagicCount, whole(2, ruleSet, TargetCounters), "mItemMaxMagicCount")
	add(addr.PlaySelectItem, whole(4, ruleSet, TargetSelectItem), "play.mSelectItem")

	add(addr.NextStage, escapeRules(), "mNextStage")
	add(addr.WarpSeq, whole(4, ruleSet, TargetEscape), "mailbox.warp_seq")
	add(WarpPos, whole(12, ruleSet, TargetEscape), "mailbox.warp_x/y/z")

	slices.SortFunc(g.spans, func(a, b guardSpan) int { return cmp.Compare(a.start, b.start) })
	return g
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
		rs[i] = byteRule{mask: 0xFF, rule: r, target: TargetEscape, lo: lo, hi: hi}
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
			return byteRule{mask: 0xFF, rule: ruleSet, target: TargetTag}
		}
		return byteRule{}
	case r.Class == class.Derived:
		if off >= addr.OffMaxLife && off < addr.OffMaxLife+2 || off == addr.OffMaxMagic {
			return byteRule{} // through mItemMaxLifeCount and mItemMaxMagicCount only
		}
		return byteRule{mask: 0xFF, rule: ruleSet, target: TargetDerived}
	case !r.Class.Merges():
		return byteRule{} // PER_PLAYER, NEVER
	}
	br := byteRule{mask: r.Mask, rule: ruleOR}
	if r.Class == class.Max {
		br.rule = ruleMax
	}
	switch CategoryOf(off) {
	case CatStage, CatSwitch:
		br.target = TargetStage
	case CatOcean:
		br.target = TargetOcean
	case CatStory, CatRegister:
		br.target = TargetStory
	default: // CatInventory, CatCapacity
		br.target = TargetInventory
	}
	return br
}

// Targets returns the targets g covers.
func (g *Guard) Targets() Target { return g.targets }

// at returns the allowlist's rule for the byte at a; ok is false outside
// it.
func (g *Guard) at(a uint32) (span string, br byteRule, ok bool) {
	i, found := slices.BinarySearchFunc(g.spans, a, func(s guardSpan, a uint32) int {
		switch {
		case a < s.start:
			return 1
		case a >= s.end():
			return -1
		}
		return 0
	})
	if !found {
		return "", byteRule{}, false
	}
	s := g.spans[i]
	br = s.rules[a-s.start]
	return s.name, br, br.mask != 0
}

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
	case ruleArm:
		if old != 0 || val != 1 {
			return RefuseValue, fmt.Sprintf("it may only go from 0 to 1, not from %d to %d", int8(old), int8(val))
		}
	}
	return 0, ""
}

// stageNameByte reports whether b may be a byte of a stage name in
// mNextStage: NUL, or a letter, digit or '_', as every stage name is
// (and stage-hop takes).
func stageNameByte(b byte) bool {
	return b == 0 || b == '_' || b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

// Refusal is why the WriteGuard refused a write.
type Refusal uint8

const (
	RefuseOff    Refusal = iota + 1 // co-op is off (turned off, or auto-disabled)
	RefuseTarget                    // the byte is not in the allowlist, or the write's addresses don't match its image byte
	RefuseGate                      // a gate the byte's target needs is closed this tick
	RefusePolicy                    // the policy opens none of the byte's bits (an unsynced switch)
	RefuseMask                      // a changed bit outside the byte's mask (class mask and policy)
	RefuseClear                     // a bit an OR byte would lose, or a MAX value that would fall
	RefuseValue                     // a value the byte may not hold: an escape byte the game would reject
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
// gates, ARMED, and the save table the live memBit holds.
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
}

// opens returns "" if target tg may be written at a this tick, or why
// not. The rules follow §3.2 and §6.3-6.5:
//
//   - every target but the tag needs ARMED (a bind writes the tag of a
//     file not yet bound);
//   - stage progress bits, memory switches and the ocean need STABLE and
//     HEARTBEAT, the gates of any write. When within that a planner may
//     write them (the current table's switches at a room change, §6.3)
//     is the planner's rule, which the CI write audit checks;
//   - story bits are written only on the tick a stage change starts,
//     with HEARTBEAT (§6.5);
//   - a pending counter needs IDLE and to read 0 (§3.2);
//   - everything else needs IDLE as well.
//
// The live memBit's boss bits are shut apart (bossClosed).
func (t *Tick) opens(tg Target, a uint32) string {
	st := t.Gates
	if tg != TargetTag && !t.Armed {
		return "the file is not armed in this world"
	}
	switch tg {
	case TargetStage, TargetOcean:
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
//   - a byte outside the allowlist: PER_PLAYER, NEVER, DERIVED or TAG
//     bytes in M3, padding, the mod's own RAM;
//   - any write while a gate the byte's target needs is closed, before
//     the first tick and after Close;
//   - a bit the policy does not open (an unsynced memory switch), or any
//     other bit outside the byte's mask;
//   - a clear: the new byte must hold every bit of the old one (a MAX
//     byte must not fall);
//   - a change to the live memBit's boss bits while a boss stage is
//     loaded (Tick.Boss).
//
// A live memBit byte takes the classes and policy of the table SetTick
// says is loaded, and nothing while none is. A WriteGuard is safe for
// concurrent use.
type WriteGuard struct {
	allow  *Guard
	policy world.Policy
	brk    *Breaker
	logf   func(format string, args ...any)
	label  func(off, bit int) string

	mu      sync.Mutex
	tick    Tick
	have    bool   // SetTick was called
	closed  string // why the guard is shut until the next SetTick
	checked int
	refused [numRefusals]int
	logged  int
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
// the loaded table's), 0 when the byte is no target or no table is
// loaded. It does not look at the gates.
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
// 1-byte read returned, to val this tick. A refusal is a *RefusalError;
// it is counted and logged, and reported to the Breaker unless co-op was
// already off.
func (w *WriteGuard) Check(a uint32, old, val byte) error {
	w.mu.Lock()
	w.checked++
	what, reason, why := w.judge(a, old, val)
	if reason == 0 {
		w.mu.Unlock()
		return nil
	}
	return w.refuse(&RefusalError{Addr: a, Old: old, New: val, Reason: reason, What: what, Why: why})
}

// judge is Check's verdict. w.mu is held.
func (w *WriteGuard) judge(a uint32, old, val byte) (string, Refusal, string) {
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
	switch {
	case !w.have:
		return what, RefuseGate, "no tick has opened the gates yet"
	case w.closed != "":
		return what, RefuseGate, w.closed
	}
	if why := w.tick.opens(br.target, a); why != "" {
		return what, RefuseGate, why
	}
	if why := w.tick.bossClosed(a, old, val); why != "" {
		return what, RefuseGate, why
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
