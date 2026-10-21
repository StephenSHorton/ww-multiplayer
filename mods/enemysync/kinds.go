package enemysync

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The kind table of the generalised enemy mirror (ww-multiplayer-handoff/
// design/enemy-mirror-general.json, "type_table" and "identity".per_kind):
// one row per enemy class the mirror knows, Moblin, Bokoblin and ChuChu.
// The pipelines (host.go, client.go) walk, sample and match by it over the
// kinds WW_ENEMY_MIRROR_KINDS enables (default DefaultMirrorKinds). The C copy of this table is
// inject/include/wwenemy_kinds.h, pinned to this one and to wwapi/addr by
// TestKindTablePins.

// KindID is a row of the table; its value is the kind's bit in a
// kind_mask (design block_layout header +0x24: 0 MO2, 1 BK, 2 CC).
type KindID uint8

const (
	KindMO2 KindID = 0
	KindBK  KindID = 1
	KindCC  KindID = 2
	// KindNone is no row.
	KindNone KindID = 0xFF
)

// Kind is one row.
type Kind struct {
	ID   KindID
	Name string // the WW_ENEMY_MIRROR_KINDS token: "mo2", "bk", "cc"
	Proc uint16 // mProcName
	// Arc is the archive name the class loads (and mObjectInfo stores);
	// its BCK indices are BckFirst..BckLast; BckWait is the handback row
	// (Mo2's own three rows stay in addr; 0 for none: the ChuChu hands
	// back to its own m320).
	Arc               string
	BckFirst, BckLast int
	BckWait           int
	// MorfOff is the body morf (mDoExt_McaMorf*); IceOff the enemyice.
	MorfOff, IceOff uint32
	// GateOff is the live u8 the static gate reads first (mo2 mMode, bk
	// mType, cc mBehaviorType); the walk reads it as actors.Actor.Mode.
	GateOff uint32
}

// BckCount is how many BCK pointers the kind's table holds.
func (k *Kind) BckCount() int { return k.BckLast - k.BckFirst + 1 }

// Kinds is the table, indexed by KindID.
var Kinds = [...]Kind{
	KindMO2: {ID: KindMO2, Name: "mo2", Proc: addr.ProcMo2, Arc: addr.Mo2Arc, BckFirst: addr.Mo2BckFirst, BckLast: addr.Mo2BckLast, BckWait: addr.Mo2BckWait,
		MorfOff: addr.Mo2OffMorf, IceOff: addr.Mo2OffIce, GateOff: addr.Mo2OffMode},
	KindBK: {ID: KindBK, Name: "bk", Proc: addr.ProcBK, Arc: addr.BkArc, BckFirst: addr.BkBckFirst, BckLast: addr.BkBckLast, BckWait: addr.BkBckWait,
		MorfOff: addr.BkOffMorf, IceOff: addr.BkOffIce, GateOff: addr.BkOffType},
	KindCC: {ID: KindCC, Name: "cc", Proc: addr.ProcCC, Arc: addr.CcArc, BckFirst: addr.CcBckFirst, BckLast: addr.CcBckLast,
		MorfOff: addr.CcOffMorf, IceOff: addr.CcOffIce, GateOff: addr.CcOffBehavior},
}

// KindOf returns the row of proc, KindNone when the table has none.
func KindOf(proc uint16) KindID {
	for i := range Kinds {
		if Kinds[i].Proc == proc {
			return Kinds[i].ID
		}
	}
	return KindNone
}

// KindByName returns the row named name ("mo2", "bk", "cc").
func KindByName(name string) (*Kind, bool) {
	for i := range Kinds {
		if Kinds[i].Name == name {
			return &Kinds[i], true
		}
	}
	return nil, false
}

// Row returns id's row, nil for KindNone or an unknown id.
func (id KindID) Row() *Kind {
	if int(id) >= len(Kinds) {
		return nil
	}
	return &Kinds[id]
}

func (id KindID) String() string {
	if k := id.Row(); k != nil {
		return k.Name
	}
	return "none"
}

// kindRowLayout is KindHash's version of the row encoding: bump it when
// a field joins or leaves the hash.
const kindRowLayout = 1

// KindHash is the hash a version-4 STATE carries (State.KindHash): FNV-1a
// over kindRowLayout and, per row in KindID order, ID u8, Proc BE16, the
// archive name (u8 length, bytes), BckFirst, BckLast, BckWait (u8 each),
// MorfOff, IceOff, GateOff (BE32 each). It covers the table's offsets and
// row layout only, not which kinds are enabled (that is the STATE's
// HostKindMask), so two builds whose tables differ refuse each other
// (design "wire".state_header_52) while two configurations do not.
func KindHash() uint32 {
	b := []byte{kindRowLayout}
	be32 := func(v uint32) { b = append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) }
	for i := range Kinds {
		k := &Kinds[i]
		b = append(b, byte(k.ID), byte(k.Proc>>8), byte(k.Proc), byte(len(k.Arc)))
		b = append(b, k.Arc...)
		b = append(b, byte(k.BckFirst), byte(k.BckLast), byte(k.BckWait))
		be32(k.MorfOff)
		be32(k.IceOff)
		be32(k.GateOff)
	}
	return fnv1a(b)
}

// KindLayouts is the walk layout over every row: each class's enemyice
// and its live gate byte (actors.Actor.Mode). Layouts, the Moblin
// pipelines' own, is untouched.
var KindLayouts = func() actors.Layouts {
	l := actors.Layouts{}
	for _, k := range Kinds {
		l[int16(k.Proc)] = actors.Layout{Ice: k.IceOff, Mode: k.GateOff, ModeU8: true}
	}
	return l
}()

// KindSet is which kinds are enabled and, per kind, whether unkeyed
// (setID 0xFFFF) actors are taken too ("+u").
type KindSet struct {
	On      [len(Kinds)]bool
	Unkeyed [len(Kinds)]bool
}

// Has reports whether id is enabled.
func (s KindSet) Has(id KindID) bool { return int(id) < len(s.On) && s.On[id] }

// TakesUnkeyed reports whether id is enabled with "+u".
func (s KindSet) TakesUnkeyed(id KindID) bool { return s.Has(id) && s.Unkeyed[id] }

// Mask is the kind_mask of s (bit k = kind k enabled).
func (s KindSet) Mask() uint32 {
	var m uint32
	for i, on := range s.On {
		if on {
			m |= 1 << i
		}
	}
	return m
}

func (s KindSet) String() string {
	var parts []string
	for i, on := range s.On {
		if !on {
			continue
		}
		p := Kinds[i].Name
		if s.Unkeyed[i] {
			p += "+u"
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

// AllKinds enables every row, keyed and unkeyed (what the read-only
// probes list).
func AllKinds() KindSet {
	var s KindSet
	for i := range s.On {
		s.On[i], s.Unkeyed[i] = true, true
	}
	return s
}

// DefaultMirrorKinds is WW_ENEMY_MIRROR_KINDS's default: the Moblin by
// setID, the Bokoblin and the ChuChu by setID and unkeyed (live checks L6,
// L7 TyuTyu and Cave10, L8, L9). An unkeyed Moblin has had no live check.
const DefaultMirrorKinds = "mo2,bk+u,cc+u"

// ParseKinds parses a WW_ENEMY_MIRROR_KINDS value: kind names joined by
// "," (a "+u" suffix also takes that kind's unkeyed actors); "" is
// DefaultMirrorKinds and "none" the empty set. The host and the client
// read it at construction (MirrorKinds).
func ParseKinds(v string) (KindSet, error) {
	var s KindSet
	v = strings.TrimSpace(v)
	if v == "" {
		v = DefaultMirrorKinds
	}
	if strings.EqualFold(v, "none") {
		return s, nil
	}
	for _, tok := range strings.Split(v, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		name, unkeyed := strings.CutSuffix(tok, "+u")
		k, ok := KindByName(name)
		if !ok {
			return KindSet{}, fmt.Errorf("enemysync: unknown enemy kind %q (want %s)", tok, strings.Join(kindNames(), ", "))
		}
		if s.On[k.ID] {
			return KindSet{}, fmt.Errorf("enemysync: enemy kind %q listed twice", name)
		}
		s.On[k.ID] = true
		s.Unkeyed[k.ID] = unkeyed
	}
	return s, nil
}

func kindNames() []string {
	var out []string
	for _, k := range Kinds {
		out = append(out, k.Name)
	}
	return out
}

// LiveGate is the live class bytes a static gate reads: Mode is the
// kind's GateOff byte (mo2 mMode, bk mType, cc mBehaviorType); Color and
// InstaKill are the ChuChu's mColorType and m304 (zero for the others).
type LiveGate struct {
	Mode      uint8
	Color     uint8
	InstaKill uint8
}

// ParamsOK is the kind's gate on the actor's params (+0xB0), the value
// create decodes (design identity.per_kind):
//
//   - MO2: none. The Moblin's gate is its live mMode (MirrorsMode), as
//     KeyedMoblins has it; a params gate would change what the Moblin
//     pipelines take.
//   - BK: the type nibble params & 0xF in {0, 1, 4, 0xB} (create,
//     d_a_bk.cpp:4759-4766; 0xB is the torch Bokoblin, mType 4). Type 6
//     rewrites its home (d_a_bk.cpp:1461-1463), 9 runs, 5 rides a
//     Kargaroc, 2 and 3 sleep: refused.
//   - CC: create's own decode (d_a_cc.cpp:2564-2605): behaviour params &
//     0xFF (0xFF read as 0) in {0, 1, 3}; colour params >> 8 & 0xFF (0xFF
//     read as 0; 10.. less 10, 15 the insta-kill m304, refused) in
//     {0, 1, 2, 4}: black (3) is refused.
func (k *Kind) ParamsOK(params uint32) bool {
	switch k.ID {
	case KindMO2:
		return true
	case KindBK:
		switch params & addr.BkParamTypeMask {
		case 0, 1, 4, addr.BkTypeTorchShield:
			return true
		}
		return false
	case KindCC:
		b, c, insta := CcDecodeParams(params)
		return ccBehaviorOK(b) && ccColorOK(c) && !insta
	}
	return false
}

// CcDecodeParams is daCC_Create's params decode (d_a_cc.cpp:2564-2605,
// without the REG8 debug overrides): behaviour, colour and m304.
func CcDecodeParams(params uint32) (behavior, color uint8, instaKill bool) {
	behavior = uint8(params)
	color = uint8(params >> 8)
	if behavior == 0xFF {
		behavior = 0
	}
	if color == 0xFF {
		color = 0
	}
	if behavior != 4 && color >= 10 {
		color -= 10
		if color == 5 {
			instaKill, color = true, 1
		}
	}
	return behavior, color, instaKill
}

func ccBehaviorOK(b uint8) bool { return b == 0 || b == 1 || b == 3 }
func ccColorOK(c uint8) bool    { return c == 0 || c == 1 || c == 2 || c == 4 }

// LiveOK is the kind's gate on the live class bytes:
//
//   - MO2: mMode 0 or 1 (MirrorsMode; carried 5 and statue 0xF refused).
//   - BK: mType in {0, 1, 4} (create turned 0xB into 4).
//   - CC: mBehaviorType in {0, 1, 3}, mColorType in {0, 1, 2, 4}, m304 0.
func (k *Kind) LiveOK(g LiveGate) bool {
	switch k.ID {
	case KindMO2:
		return MirrorsMode(int32(g.Mode))
	case KindBK:
		return g.Mode == 0 || g.Mode == 1 || g.Mode == 4
	case KindCC:
		return ccBehaviorOK(g.Mode) && ccColorOK(g.Color) && g.InstaKill == 0
	}
	return false
}

// StaticOK is ParamsOK and LiveOK together.
func (k *Kind) StaticOK(params uint32, g LiveGate) bool {
	return k.ParamsOK(params) && k.LiveOK(g)
}

// Per-kind flags2 bits (design "flags".flags2; wwenemy_kinds.h
// EMK_BK_F2_* and EMK_CC_F2_*). The Moblin keeps the SlotF2* bits.
const (
	BkF2HasWeapon uint8 = 1 << 0 // m0B30 != 0
	BkF2PlaySkip  uint8 = 1 << 1 // m030C != 0
	BkF2BlurOn    uint8 = 1 << 2 // m02F0 != 0
	BkF2Attn      uint8 = 1 << 3 // attention_info.flags (+0x280) != 0

	CcF2Tg       uint8 = 1 << 0 // mCyl Tg mSPrm Set
	CcF2Co       uint8 = 1 << 1 // mCyl Co mSPrm Set
	CcF2Squash   uint8 = 1 << 2 // the squash runs (d_a_cc.cpp:88)
	CcF2LowEye   uint8 = 1 << 3 // m2F5 in [6, 11) or action 1
	CcF2NoShadow uint8 = 1 << 4 // m2F9 != 0
	CcF2Attn     uint8 = 1 << 5 // attention_info.flags (+0x280) != 0

	// KindF2Light is generic: the host's ice shrink timer runs while the
	// enemy dies (a light-arrow death).
	KindF2Light uint8 = 1 << 6
)

// The BCK names of each kind's archive, index First+i (generated from
// the decomp's resource headers; the probes print them).
var (
	// mo2BckNames: dRes_INDEX_MO2_BCK_*_e 0x3E.. (assets/GZLE01/res/Object/Mo2.h)
	mo2BckNames = []string{"ADOTSUKI", "AHAZUSHI01", "AHAZUSHI02", "AHAZUSHI03", "AKAITEN01", "AKAITEN02", "AKAITEN03", "ATATE01", "ATATE02", "ATATE03", "ATSUKI01", "ATSUKI02", "ATSUKI03", "AWATEDEMO", "BB_FLY", "BKYORO", "BWAIT", "BWALKFB", "BWALKLR", "DASH", "DEFAULT", "GAKEDEMO", "HIPDMG01", "HIPDMG02", "JEND", "JSTART", "KKEIKAI", "KNAGE", "KOKERUB", "KOKERUF", "KWAIT", "KWALK", "NABIGPUNCH01", "NABIGPUNCH02", "NABIGPUNCH03", "NAJAB", "NBWAIT", "NBWALKFB", "NBWALKLR", "NWALK", "NYWALK", "OKIRUA", "OKIRUB", "OKIRUU", "PAOMUKE", "PUTSUBUSE", "SCATCH", "SHAKKEN", "SKYORO", "SLIPDEMO", "SWALK", "TACKLEDEMO", "UKYADEMO", "WAIT", "WAITDEMO", "WALK"}
	// bkBckNames: dRes_INDEX_BK_BCK_*_e 0x29.. (assets/GZLE01/res/Object/Bk.h)
	bkBckNames = []string{"BK_AOMUKE", "BK_ATTACK1", "BK_ATTACK2", "BK_ATTACK3", "BK_BIKKURI", "BK_BOUGYO1", "BK_BOUGYO2", "BK_CATCH", "BK_HAKKEN", "BK_HAKOBI", "BK_HIDARIROT", "BK_JATTACK1", "BK_JATTACK2", "BK_JATTACK3", "BK_JUMP1", "BK_JUMP2", "BK_KERI1", "BK_KERI2", "BK_KIME", "BK_KOUKA", "BK_KYORO1", "BK_KYORO2", "BK_MIGIROT", "BK_NIGERU", "BK_NOBI", "BK_NOMWAIT", "BK_NOZOKU", "BK_OKIRUA", "BK_OKIRUU", "BK_OTISOU1", "BK_OTISOU2", "BK_RUN", "BK_SLEEP", "BK_SUWARI", "BK_TATAKU", "BK_TUTUKU1", "BK_TUTUKU2", "BK_TUTUKU3", "BK_TYAKU", "BK_UTUBUSE", "BK_WAIT", "BK_WALK", "BK_WALK2"}
	// ccBckNames: dRes_INDEX_CC_BCK_*_e 0x14.. (assets/GZLE01/res/Object/Cc.h)
	ccBckNames = []string{"ATACK01", "ATACK02", "ATACK03", "CC_BETA", "CC_PTCL", "DEKU_START", "HUKKATSU", "HUSE2TACHI", "HUSE_WALK", "MAHI", "START", "TACHI2HUSE", "TACHI_WALK", "TSTART01", "TSTART02"}
)

// BckName is the name of BCK idx in kind id's archive, "-" for none.
func BckName(id KindID, idx uint8) string {
	k := id.Row()
	if k == nil {
		return "-"
	}
	names := [...][]string{KindMO2: mo2BckNames, KindBK: bkBckNames, KindCC: ccBckNames}[id]
	i := int(idx) - k.BckFirst
	if i < 0 || i >= len(names) {
		return "-"
	}
	return names[i]
}

// --- Batch 5: the pipelines' knobs and per-kind tables ----------------------

// Enabled reports whether proc has a row and its kind is in s: the host's
// test for an enemy it mirrors (MirrorEnabled in the design, players.go
// phit) and the client's for a wire slot.
func (s KindSet) Enabled(proc uint16) bool { return s.Has(KindOf(proc)) }

// And is the kinds both s and o enable, "+u" only where both take it: a
// client mirrors kind k only when the host (State.HostKindMask) and this
// game enable it (design "wire".state_header_52).
func (s KindSet) And(o KindSet) KindSet {
	var out KindSet
	for i := range s.On {
		out.On[i] = s.On[i] && o.On[i]
		out.Unkeyed[i] = out.On[i] && s.Unkeyed[i] && o.Unkeyed[i]
	}
	return out
}

// MaskSet is the KindSet of a kind_mask (unkeyed is not in a mask: the
// set takes every kind's unkeyed actors, which And narrows to the local
// set's).
func MaskSet(mask uint32) KindSet {
	var s KindSet
	for i := range s.On {
		if mask&(1<<i) != 0 {
			s.On[i], s.Unkeyed[i] = true, true
		}
	}
	return s
}

// MirrorKinds is WW_ENEMY_MIRROR_KINDS parsed; a value ParseKinds refuses
// falls back to DefaultMirrorKinds with the error to log.
func MirrorKinds() (KindSet, error) {
	s, err := ParseKinds(os.Getenv("WW_ENEMY_MIRROR_KINDS"))
	if err != nil {
		d, _ := ParseKinds(DefaultMirrorKinds)
		return d, err
	}
	return s, nil
}

// DefaultQuietKinds is WW_ENEMY_QUIET_DEATH's default: every kind's copy
// dies quietly (it stays paused, shrinks in the REL and is deleted; design
// D7). The Moblin's legacy em_die, which drops an item on the client too,
// stays behind WW_ENEMY_QUIET_DEATH=bk,cc (live check L1b: a quiet Moblin
// death leaves nothing on the client).
const DefaultQuietKinds = "mo2,bk,cc"

// ParseQuietKinds parses a WW_ENEMY_QUIET_DEATH value: kind names joined
// by ","; "" is DefaultQuietKinds and "none" the empty set. A "+u" suffix
// is refused (a death is quiet or not whatever the key).
func ParseQuietKinds(v string) (KindSet, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		v = DefaultQuietKinds
	}
	if strings.Contains(v, "+u") {
		return KindSet{}, fmt.Errorf("enemysync: WW_ENEMY_QUIET_DEATH %q: \"+u\" means nothing here", v)
	}
	s, err := ParseKinds(v)
	if err != nil {
		return KindSet{}, err
	}
	return s, nil
}

// QuietKinds is WW_ENEMY_QUIET_DEATH parsed, DefaultQuietKinds (with the
// error) when it does not parse.
func QuietKinds() (KindSet, error) {
	s, err := ParseQuietKinds(os.Getenv("WW_ENEMY_QUIET_DEATH"))
	if err != nil {
		d, _ := ParseQuietKinds(DefaultQuietKinds)
		return d, err
	}
	return s, nil
}

// SelectMode is how slots are chosen: by distance with hysteresis (the
// host orders its candidates by player distance, each client keeps its
// MaxSlots by design slots.client_selectSlots), or SelectLegacy, the
// Moblin prototype's lowest setIDs (KeyedMoblins), for regression runs.
type SelectMode uint8

const (
	SelectDistance SelectMode = iota
	SelectLegacy
)

func (m SelectMode) String() string {
	if m == SelectLegacy {
		return "legacy"
	}
	return "distance"
}

// ParseSelect parses a WW_ENEMY_SELECT value: "distance" (or "") or
// "legacy".
func ParseSelect(v string) (SelectMode, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "distance":
		return SelectDistance, nil
	case "legacy":
		return SelectLegacy, nil
	}
	return SelectDistance, fmt.Errorf("enemysync: WW_ENEMY_SELECT %q (want distance or legacy)", v)
}

// SelectFromEnv is WW_ENEMY_SELECT parsed (SelectDistance, with the error,
// when it does not parse).
func SelectFromEnv() (SelectMode, error) { return ParseSelect(os.Getenv("WW_ENEMY_SELECT")) }

// State30Hz reports WW_ENEMY_STATE_30HZ=1: the host sends a STATE only when
// its REL's frame counter advanced since the last one (about half the
// bytes at 62.5 Hz ticks; design slots.knobs), and at least every
// KeepaliveEvery so a client's StaleAfter never trips while the game is
// paused.
func State30Hz() bool { return os.Getenv("WW_ENEMY_STATE_30HZ") == "1" }

// annexHookNames are WW_ENEMY_ANNEX's hook names (the EnemyHKD* bits).
var annexHookNames = map[string]uint8{
	"model_eq": mailbox.EnemyHKDModelEq, "attach": mailbox.EnemyHKDAttach, "eye": mailbox.EnemyHKDEye,
	"legacy_death": mailbox.EnemyHKDLegacyDeath, "attn": mailbox.EnemyHKDAttn, "mtx_plain": mailbox.EnemyHKDMtxPlain,
}

// ParseAnnex parses a WW_ENEMY_ANNEX value into the annex overrides (the
// block's per-kind override row and invuln wait; design block_layout.annex,
// the Go-only fixes after the one REL build): entries <kind>:<field>=<v>
// joined by ",", fields
//
//	tg_hit_up  s16  collider 2's centre height above pos (host em_hit_center; BK 56, CC 50 compiled)
//	invuln     u8   executes the host's hit applier waits on the invuln field (CC 8 compiled)
//	quiet      u8   quiet-death frames (client; 20 compiled, 90 under SF2_LIGHT)
//	hooks      u8   hook_disable bits: a number or names joined by "+"
//	                (model_eq attach eye legacy_death attn mtx_plain)
//
// e.g. "bk:tg_hit_up=60,cc:invuln=10,bk:hooks=attach". "" and "none" are
// no overrides; 0 is the compiled default. Each side writes its own block's
// annex, so a host value (tg_hit_up, invuln) is set on the host and a
// client value (quiet, hooks) on each client.
func ParseAnnex(v string) (mailbox.EnemyAnnex, error) {
	var a mailbox.EnemyAnnex
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "none") {
		return a, nil
	}
	for _, tok := range strings.Split(v, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		kn, rest, ok1 := strings.Cut(tok, ":")
		field, val, ok2 := strings.Cut(rest, "=")
		if !ok1 || !ok2 {
			return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q (want <kind>:<field>=<value>)", tok)
		}
		k, ok := KindByName(kn)
		if !ok || int(k.ID) >= mailbox.EnemyAnnexKinds {
			return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: unknown kind %q (want %s)", tok, kn, strings.Join(kindNames(), ", "))
		}
		o := &a.Override[k.ID]
		switch field {
		case "tg_hit_up":
			n, err := strconv.ParseInt(val, 0, 16)
			if err != nil {
				return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: %v", tok, err)
			}
			o.TgHitUp = int16(n)
		case "invuln", "quiet":
			n, err := strconv.ParseUint(val, 0, 8)
			if err != nil {
				return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: %v", tok, err)
			}
			if field == "invuln" {
				a.InvulnWait[k.ID] = uint8(n)
			} else {
				o.QuietFrames = uint8(n)
			}
		case "hooks":
			var bits uint8
			for _, h := range strings.Split(val, "+") {
				if b, ok := annexHookNames[h]; ok {
					bits |= b
					continue
				}
				n, err := strconv.ParseUint(h, 0, 8)
				if err != nil {
					return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: hook %q (want a number or model_eq, attach, eye, legacy_death, attn, mtx_plain)", tok, h)
				}
				bits |= uint8(n)
			}
			o.HookDisable = bits
		default:
			return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: unknown field %q (want tg_hit_up, invuln, quiet or hooks)", tok, field)
		}
	}
	return a, nil
}

// AnnexFromEnv is WW_ENEMY_ANNEX parsed (no overrides, with the error,
// when it does not parse). Only the overrides are set; Aux stays zero.
func AnnexFromEnv() (mailbox.EnemyAnnex, error) { return ParseAnnex(os.Getenv("WW_ENEMY_ANNEX")) }

// ColliderOff is the actor offset of a kind's hit collider code (the REL's
// em_hits codes, design hit_path.client): 1 the head sphere, 2 the Tg
// cylinder (MO2 0x22EC/0x21BC, BK 0xDE8/0xCB8, CC none/0x6BC; the
// wwenemy_kinds.h HEAD_SPH and TG_CYL, pinned by TestColliderOffPins). 0
// for a code the kind has no collider for.
func ColliderOff(id KindID, code uint8) uint32 {
	t := [...][2]uint32{
		KindMO2: {addr.Mo2OffHeadSph, addr.Mo2OffTgCyl},
		KindBK:  {addr.BkOffHeadSph, addr.BkOffTgCyl},
		KindCC:  {0, addr.CcOffCyl},
	}
	if int(id) >= len(t) || code < 1 || code > 2 {
		return 0
	}
	return t[id][code-1]
}

// DeathSwitchOff is the offset of a kind's u8 death switch, which its
// own death sets (onSwitch) and a quiet death on the client never runs:
// BK m02B8 (d_a_bk.h:20; d_a_bk.cpp:2644-2646), MO2 mDeathSwitch
// (d_a_mo2.h:42). 0 for the ChuChu, which has none (design
// death_and_drops.quiet_steps).
func DeathSwitchOff(id KindID) uint32 {
	switch id {
	case KindMO2:
		return addr.Mo2OffDeathSwitch
	case KindBK:
		return addr.BkOffDeathSwitch
	}
	return 0
}

// DeathSwitchSet reports whether a death switch byte names a switch:
// create turns 0xFF into 0 (d_a_bk.cpp create, design facts), so 0 and
// 0xFF are none.
func DeathSwitchSet(no uint8) bool { return no != 0 && no != 0xFF }

// QuietCarryGuard is the Go copy of the REL's guard on a quiet Bokoblin
// death's CARRY clear (design critic_applied "BK quiet death: CARRY clear
// on a stale m1200", combat.HeldBy's rule): the BOKO the copy's m1200
// names is cleared only when the copy holds a weapon (m0B30 != 0), the
// actor is a BOKO (proc 0x1D5), its CARRY bit is set and its mNowMode is
// Mode_ENEMY_CARRY_e. A stale m1200 naming a stick in Link's hands
// (Mode_PLAYER_CARRY_e) or on the ground (CARRY clear) is left alone.
// "" means clear it; otherwise why not.
func QuietCarryGuard(hold uint32, boko *actors.Actor) string {
	switch {
	case hold == 0:
		return "the copy holds no weapon (m0B30 0)"
	case boko == nil:
		return "m1200 names no live actor"
	case boko.Proc != addr.ProcBoko:
		return fmt.Sprintf("m1200 names proc 0x%04X, not BOKO", uint16(boko.Proc))
	case !boko.Carried():
		return "the stick is not carried (on the ground)"
	case !boko.ModeOK:
		return "the stick's mNowMode is unreadable"
	case boko.Mode == addr.BokoModePlayerCarry:
		return "Link holds the stick"
	case boko.Mode != addr.BokoModeEnemyCarry:
		return fmt.Sprintf("the stick is in mNowMode %d, not ENEMY_CARRY", boko.Mode)
	}
	return ""
}
