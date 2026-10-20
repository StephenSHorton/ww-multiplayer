package enemysync

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The kind table of the generalised enemy mirror (ww-multiplayer-handoff/
// design/enemy-mirror-general.json, "type_table" and "identity".per_kind):
// one row per enemy class the mirror knows, Moblin, Bokoblin and ChuChu.
// Batch 1 ("L0 read-only tooling") reads with it only: the Moblin
// pipelines (host.go, client.go, pipelines.go) still run on Layouts,
// KeyedMoblins, ReadSample and Mo2Res, untouched. The C copy of this
// table (wwenemy_kinds.h) and the pin between the two come with the REL
// build.

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

// DefaultMirrorKinds is WW_ENEMY_MIRROR_KINDS's default.
const DefaultMirrorKinds = "mo2"

// ParseKinds parses a WW_ENEMY_MIRROR_KINDS value: kind names joined by
// "," (a "+u" suffix also takes that kind's unkeyed actors); "" is
// DefaultMirrorKinds and "none" the empty set. Batch 1 parses it only;
// nothing in the pipelines reads it yet.
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

// Per-kind flags2 bits (design "flags".flags2). The Moblin keeps the
// SlotF2* bits; these are Go-side only in Batch 1 (the block and the wire
// carry nothing of a Bokoblin or ChuChu yet).
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
