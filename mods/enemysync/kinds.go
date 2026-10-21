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
// design/enemy-mirror-general.json, "type_table" and "identity".per_kind;
// the allow batch's rows: enemy-mirror-allow.json "type_table"): one row
// per enemy class the mirror knows, Moblin, Bokoblin, ChuChu, Keese, Armos
// Knight and Armos.
// The pipelines (host.go, client.go) walk, sample and match by it over the
// kinds WW_ENEMY_MIRROR_KINDS enables (default DefaultMirrorKinds). The C copy of this table is
// inject/include/wwenemy_kinds.h, pinned to this one and to wwapi/addr by
// TestKindTablePins.

// KindID is a row of the table; its value is the kind's bit in a
// kind_mask (design block_layout header +0x24: 0 MO2, 1 BK, 2 CC; the
// allow batch's 3 KI, 4 AM, 5 AM2, wwenemy_kinds.h EMK_KIND_*).
type KindID uint8

const (
	KindMO2 KindID = 0
	KindBK  KindID = 1
	KindCC  KindID = 2
	KindKI  KindID = 3
	KindAM  KindID = 4
	KindAM2 KindID = 5
	// KindNone is no row.
	KindNone KindID = 0xFF
)

// Kind is one row.
type Kind struct {
	ID   KindID
	Name string // the WW_ENEMY_MIRROR_KINDS token: "mo2", "bk", "cc", "ki", "am", "am2"
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
	// mType, cc mBehaviorType, the allow kinds' mAction); the walk reads
	// it as actors.Actor.Mode.
	GateOff uint32
	// AngleOff is the s16 the slot's AngleY streams (design D13): the
	// current.angle.y of MO2, BK and CC, the shape_angle.y KI, AM and AM2
	// draw with (d_a_ki.cpp:1054; d_a_am.cpp:103; d_a_am2.cpp:78). Not
	// hashed (KindHash): the wire meaning is the row's, by id.
	AngleOff uint32
	// PKeyNoPos is EMK_CAP_PKEY_NO_POS (design D9): a keyed actor's pkey
	// hashes home.pos as zero (a wait-type Keese rewrites its home,
	// d_a_ki.cpp:675-682). Not hashed either.
	PKeyNoPos bool
}

// BckCount is how many BCK pointers the kind's table holds.
func (k *Kind) BckCount() int { return k.BckLast - k.BckFirst + 1 }

// Kinds is the table, indexed by KindID.
var Kinds = [...]Kind{
	KindMO2: {ID: KindMO2, Name: "mo2", Proc: addr.ProcMo2, Arc: addr.Mo2Arc, BckFirst: addr.Mo2BckFirst, BckLast: addr.Mo2BckLast, BckWait: addr.Mo2BckWait,
		MorfOff: addr.Mo2OffMorf, IceOff: addr.Mo2OffIce, GateOff: addr.Mo2OffMode, AngleOff: addr.ActorOffCurAngleY},
	KindBK: {ID: KindBK, Name: "bk", Proc: addr.ProcBK, Arc: addr.BkArc, BckFirst: addr.BkBckFirst, BckLast: addr.BkBckLast, BckWait: addr.BkBckWait,
		MorfOff: addr.BkOffMorf, IceOff: addr.BkOffIce, GateOff: addr.BkOffType, AngleOff: addr.ActorOffCurAngleY},
	KindCC: {ID: KindCC, Name: "cc", Proc: addr.ProcCC, Arc: addr.CcArc, BckFirst: addr.CcBckFirst, BckLast: addr.CcBckLast,
		MorfOff: addr.CcOffMorf, IceOff: addr.CcOffIce, GateOff: addr.CcOffBehavior, AngleOff: addr.ActorOffCurAngleY},
	// The allow batch (enemy-mirror-allow.json type_table go_row): the gate
	// byte is each class's u8 mAction.
	KindKI: {ID: KindKI, Name: "ki", Proc: addr.ProcKI, Arc: addr.KiArc, BckFirst: addr.KiBckFirst, BckLast: addr.KiBckLast, BckWait: addr.KiBckWait,
		MorfOff: addr.KiOffMorf, IceOff: addr.KiOffIce, GateOff: addr.KiOffAction, AngleOff: addr.ActorOffShapeAngleY, PKeyNoPos: true},
	KindAM: {ID: KindAM, Name: "am", Proc: addr.ProcAM, Arc: addr.AmArc, BckFirst: addr.AmBckFirst, BckLast: addr.AmBckLast, BckWait: addr.AmBckWait,
		MorfOff: addr.AmOffMorf, IceOff: addr.AmOffIce, GateOff: addr.AmOffAction, AngleOff: addr.ActorOffShapeAngleY},
	KindAM2: {ID: KindAM2, Name: "am2", Proc: addr.ProcAM2, Arc: addr.Am2Arc, BckFirst: addr.Am2BckFirst, BckLast: addr.Am2BckLast, BckWait: addr.Am2BckWait,
		MorfOff: addr.Am2OffMorf, IceOff: addr.Am2OffIce, GateOff: addr.Am2OffAction, AngleOff: addr.ActorOffShapeAngleY},
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

// KindByName returns the row named name ("mo2", "bk", "cc", "ki", "am", "am2").
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
	if k := id.StagedRow(); k != nil {
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
// MorfOff, IceOff, GateOff (BE32 each; AngleOff and PKeyNoPos are not
// hashed). It covers the table's offsets and
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
// kind's GateOff byte (mo2 mMode, bk mType, cc mBehaviorType, the allow
// kinds' mAction); Color and InstaKill are the ChuChu's mColorType and
// m304 (zero for the others). The allow kinds' (REL em_validate, allow
// design R-8): SubMode the Armos Knight's or the Armos's mMode (+0x2C5,
// +0x2CD), Fade the Keese's m2DC shrink-delete (+0x2DC), Abyss the
// Armos's mInAbyssTimer (+0x2D2), Carried actor_status CARRY (the walk's
// status word). The staged Darknut's (TN design R-4, go_changes LiveGate;
// read by ClassifyStaged, judged by TnLiveRefusal, probe-only in B0):
// Boss actor_status BOSS, DrAction the s16 dr.mAction (+0x47A), Equip
// mEquipmentType, and m02C1, m1518, m14F8, m140C.
type LiveGate struct {
	Mode      uint8
	Color     uint8
	InstaKill uint8
	SubMode   uint8
	Fade      uint8
	Abyss     uint8
	Carried   bool

	Boss                       bool
	DrAction                   int16
	Equip                      uint8
	M02C1, M1518, M14F8, M140C uint8
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
//   - KI, AM, AM2: none here (allow design go_changes ParamsOK). An
//     unkeyed Keese has its own gate, UnkeyedParamsOK.
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
	case KindKI, KindAM, KindAM2:
		return true
	}
	return false
}

// UnkeyedParamsOK is the params gate an unkeyed (setID 0xFFFF) actor
// passes on top of ParamsOK: a Keese only with params & 0xFF ==
// KiTypeFly (design D10: the fly type; the Puppet Ganon swarms spawn
// 0xFFFF0003, d_a_bgn.cpp:3343, d_a_bgn2.cpp:1048, and an unkeyed
// wait-type Keese moves its home with no setID to pair by). Every other
// kind: true.
func (k *Kind) UnkeyedParamsOK(params uint32) bool {
	if k.ID == KindKI {
		return params&0xFF == KiTypeFly
	}
	return true
}

// KiTypeFly is the Keese's fly-type params low byte (EMK_KI_TYPE_FLY;
// mParameters = params & 0xFF, d_a_ki.cpp:1372-1377).
const KiTypeFly = 1

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
//   - KI: mAction not 2 (FIRE_SET) nor 30 (FAIL_MOVE), and m2DC 0 (the
//     shrink-delete fade scales the actor, d_a_ki.cpp:961-967; D16).
//   - AM: not ITAI (3) from mMode 44 on (the bomb swallow and the death,
//     d_a_am.cpp:849-935).
//   - AM2: not carried, not MODORU (4: it scales, D16), mInAbyssTimer 0,
//     and not ITAI (2) from mMode 22 on (the death dance).
//
// The allow kinds' rules are the REL's em_validate (allow design R-8),
// which refuses the same at adopt: an adopt gate only, so the host's walk
// keeps an enemy it already listed (liveSticky, CandidatesSince).
func (k *Kind) LiveOK(g LiveGate) bool {
	switch k.ID {
	case KindMO2:
		return MirrorsMode(int32(g.Mode))
	case KindBK:
		return g.Mode == 0 || g.Mode == 1 || g.Mode == 4
	case KindCC:
		return ccBehaviorOK(g.Mode) && ccColorOK(g.Color) && g.InstaKill == 0
	case KindKI:
		return g.Mode != addr.KiActFireSet && g.Mode != addr.KiActFail && g.Fade == 0
	case KindAM:
		return !(g.Mode == addr.AmActItai && g.SubMode >= addr.AmModeBombSwallow)
	case KindAM2:
		return !g.Carried && g.Mode != addr.Am2ActModoru && g.Abyss == 0 && !(g.Mode == addr.Am2ActItai && g.SubMode >= addr.Am2ModeDance)
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

	// The allow kinds' (wwenemy_kinds.h EMK_KI_F2_*, EMK_AM_F2_*,
	// EMK_AM2_F2_*; design type_table sampler). Each row's attention bit
	// is its EMK_*_ATTN_F2 (D4).
	KiF2Attn  uint8 = 1 << 0 // attention_info.flags != 0
	KiF2Lunge uint8 = 1 << 2 // mAction 3 or 10 with mBehaviorType 3 (d_a_ki.cpp:502, 577-580): Tg R 20, Co R 0

	AmF2Attn     uint8 = 1 << 0 // attention_info.flags != 0
	AmF2PlaySkip uint8 = 1 << 1 // mCountDownTimers[1] != 0 (d_a_am.cpp:862, 1034-1036); == mailbox.EnemySF2PlaySkip

	Am2F2Attn    uint8 = 1 << 0 // attention_info.flags != 0
	Am2F2WeakTg  uint8 = 1 << 1 // mWeakSph Tg mSPrm Set (D6)
	Am2F2BodyCo  uint8 = 1 << 2 // mBodyCyl Co mSPrm Set (cleared while carried, d_a_am2.cpp:694, 709)
	Am2F2Carried uint8 = 1 << 3 // actor_status CARRY (info)

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
	// kiBckNames, amBckNames, am2BckNames: assets/GZLE01/res/Object/
	// Ki.h:18-23, Am.h:25-36, Am2.h:11-20.
	kiBckNames  = []string{"ATTACK1", "BITA1", "DAMAGE1", "FLY1", "FLY2", "WAIT1"}
	amBckNames  = []string{"BOM_NOMI", "CLOSE", "CLOSE_LOOP", "DAMAGE", "DAMAGE_END", "DAMAGE_LOOP", "DEAD", "OKIRU", "OPEN", "OPEN_LOOP", "SLEEP", "SLEEP_LOOP"}
	am2BckNames = []string{"BURUBURU", "DAMAGE", "DEAD1", "DEAD2", "DEAD3", "JUMP", "MAHI", "SLEEP", "START", "WAIT"}
)

// bckNames is each row's names, by KindID.
var bckNames = [len(Kinds)][]string{KindMO2: mo2BckNames, KindBK: bkBckNames, KindCC: ccBckNames, KindKI: kiBckNames, KindAM: amBckNames, KindAM2: am2BckNames}

// BckName is the name of BCK idx in kind id's archive (a staged row's
// too), "-" for none.
func BckName(id KindID, idx uint8) string {
	var names []string
	k := id.Row()
	switch {
	case k != nil:
		names = bckNames[id]
	case id.StagedRow() != nil:
		k, names = id.StagedRow(), stagedBckNames[id-KindTN]
	default:
		return "-"
	}
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
// death leaves nothing on the client). The allow kinds are listed too, and
// the REL forces their quiet death whatever this says (allow design D2,
// R-15: em_die would drop a duplicate item on the client).
const DefaultQuietKinds = "mo2,bk,cc,ki,am,am2"

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
	"special": mailbox.EnemyHKDSpecial, "extra": mailbox.EnemyHKDExtra,
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
//	                (model_eq attach eye legacy_death attn mtx_plain special extra)
//
// e.g. "bk:tg_hit_up=60,cc:invuln=10,bk:hooks=attach". "" and "none" are
// no overrides; 0 is the compiled default. Each side writes its own block's
// annex, so a host value (tg_hit_up, invuln) is set on the host and a
// client value (quiet, hooks) on each client.
//
// The allow kinds (ki, am, am2) take hooks= only, into the annex's
// HookDisableX bytes (+0x1FC.., allow design D3): their quiet frames,
// tg_hit_up and invuln wait are compiled in the REL, which reads no
// override for kind ids >= mailbox.EnemyAnnexKinds, so those fields are
// refused for them rather than written where nothing reads them.
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
		if !ok {
			return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: unknown kind %q (want %s)", tok, kn, strings.Join(kindNames(), ", "))
		}
		id := int(k.ID)
		x := id - mailbox.EnemyAnnexKinds // the HookDisableX index of an allow kind
		if x >= mailbox.EnemyAnnexHKDXKinds {
			return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: kind %s has no annex byte", tok, k.Name)
		}
		if x >= 0 && field != "hooks" {
			switch field {
			case "tg_hit_up", "invuln", "quiet":
				return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: kind %s takes hooks= only (its %s is compiled in the REL)", tok, k.Name, field)
			}
			return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: unknown field %q (kind %s takes hooks= only)", tok, field, k.Name)
		}
		switch field {
		case "tg_hit_up":
			n, err := strconv.ParseInt(val, 0, 16)
			if err != nil {
				return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: %v", tok, err)
			}
			a.Override[id].TgHitUp = int16(n)
		case "invuln", "quiet":
			n, err := strconv.ParseUint(val, 0, 8)
			if err != nil {
				return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: %v", tok, err)
			}
			if field == "invuln" {
				a.InvulnWait[id] = uint8(n)
			} else {
				a.Override[id].QuietFrames = uint8(n)
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
					return mailbox.EnemyAnnex{}, fmt.Errorf("enemysync: WW_ENEMY_ANNEX entry %q: hook %q (want a number or model_eq, attach, eye, legacy_death, attn, mtx_plain, special, extra)", tok, h)
				}
				bits |= uint8(n)
			}
			if x >= 0 {
				a.HookDisableX[x] = bits
			} else {
				a.Override[id].HookDisable = bits
			}
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
// em_hits codes, design hit_path.client; wwenemy_kinds.h EMK_HIT_*): 1 the
// head sphere, 2 the Tg collider (MO2 0x22EC/0x21BC, BK 0xDE8/0xCB8, CC
// none/0x6BC, KI none/0x7D8, AM 0x604/0x85C, AM2 0x92C/0x5A0; the
// wwenemy_kinds.h HEAD_SPH and TG_CYL, pinned by TestColliderOffPins), 3
// the kind's special collider (AM: mMouthSph 0x730, the bomb swallow,
// allow design D11). 0 for a code the kind has no collider for.
func ColliderOff(id KindID, code uint8) uint32 {
	t := [...][3]uint32{
		KindMO2: {addr.Mo2OffHeadSph, addr.Mo2OffTgCyl, 0},
		KindBK:  {addr.BkOffHeadSph, addr.BkOffTgCyl, 0},
		KindCC:  {0, addr.CcOffCyl, 0},
		KindKI:  {0, addr.KiOffTgSph, 0},
		KindAM:  {addr.AmOffEyeSph, addr.AmOffBodyCyl, addr.AmOffMouthSph},
		KindAM2: {addr.Am2OffWeakSph, addr.Am2OffBodyCyl, 0},
	}
	if int(id) >= len(t) || code < HitHead || code > HitSpecial {
		return 0
	}
	return t[id][code-1]
}

// The hit collider codes (wwenemy_kinds.h EMK_HIT_*; the block's
// hit_apply collider byte).
const (
	HitHead    uint8 = 1 // the row's head sphere
	HitTg      uint8 = 2 // the row's Tg collider
	HitSpecial uint8 = 3 // AM's bomb swallow (no v2 sphere; WWENEMY_HKD_SPECIAL turns it off)
)

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
