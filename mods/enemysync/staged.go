package enemysync

import (
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The staged kinds of the TN batch (ww-multiplayer-handoff/design/
// enemy-mirror-tn.json; step B0, the pattern of the allow batch's A0):
// the Darknut, read on the host by the probes (enemy-watch --kind tn,
// enemy-match --dry) and nothing else. It is NOT a row of Kinds:
// KindHash, KindLayouts, ParseKinds, the KindSet arrays, ReadKindSample
// and the pipelines see only MO2..AM2 until a later step moves the row
// into the table with its REL cases. Its id is the one the design gives
// EMK_KIND_TN (D1: 6); KindID.Row() is nil for it, StagedRow() is not.

// KindTN is the staged Darknut id (design D1).
const KindTN KindID = 6

// StagedKinds is the staged rows, KindTN first. GateOff is u8
// mBehaviorType (the walk's actors.Actor.Mode; dr.mAction is an s16 the
// classification reads itself, go_changes kinds.go). AngleOff is
// current.angle.y (design D8: daTn_Execute copies shape_angle from it and
// builds the matrix from it, d_a_tn.cpp:3551, 3615); kindActorFields
// falls back to it anyway for a kind with no Kinds row.
var StagedKinds = [...]Kind{
	{ID: KindTN, Name: "tn", Proc: addr.ProcTN, Arc: addr.TnArc, BckFirst: addr.TnBckFirst, BckLast: addr.TnBckLast, BckWait: addr.TnBckWait,
		MorfOff: addr.TnOffMorf, IceOff: addr.TnOffIce, GateOff: addr.TnOffBehavior, AngleOff: addr.ActorOffCurAngleY},
}

// StagedRow returns id's staged row, nil when id is not staged.
func (id KindID) StagedRow() *Kind {
	if id < KindTN || int(id-KindTN) >= len(StagedKinds) {
		return nil
	}
	return &StagedKinds[id-KindTN]
}

// StagedOf returns the staged row of proc, KindNone when there is none.
func StagedOf(proc uint16) KindID {
	for i := range StagedKinds {
		if StagedKinds[i].Proc == proc {
			return StagedKinds[i].ID
		}
	}
	return KindNone
}

// StagedByName returns the staged row named name ("tn").
func StagedByName(name string) (*Kind, bool) {
	for i := range StagedKinds {
		if StagedKinds[i].Name == name {
			return &StagedKinds[i], true
		}
	}
	return nil, false
}

// StagedSet is which staged kinds a probe lists and, per kind, whether it
// takes their unkeyed (setID 0xFFFF) actors ("+u": the cave Darknuts are
// unkeyed, design D1).
type StagedSet struct {
	On      [len(StagedKinds)]bool
	Unkeyed [len(StagedKinds)]bool
}

// AllStaged enables every staged kind, keyed and unkeyed.
func AllStaged() StagedSet {
	var s StagedSet
	for i := range s.On {
		s.On[i], s.Unkeyed[i] = true, true
	}
	return s
}

// Enable turns id on (with "+u" when unkeyed); false when id is not
// staged.
func (s *StagedSet) Enable(id KindID, unkeyed bool) bool {
	if id.StagedRow() == nil {
		return false
	}
	s.On[id-KindTN], s.Unkeyed[id-KindTN] = true, unkeyed
	return true
}

// Has reports whether id is enabled.
func (s StagedSet) Has(id KindID) bool { return id.StagedRow() != nil && s.On[id-KindTN] }

// TakesUnkeyed reports whether id is enabled with "+u".
func (s StagedSet) TakesUnkeyed(id KindID) bool { return s.Has(id) && s.Unkeyed[id-KindTN] }

// Any reports whether any staged kind is enabled.
func (s StagedSet) Any() bool { return slices.Contains(s.On[:], true) }

func (s StagedSet) String() string {
	var parts []string
	for i, on := range s.On {
		if !on {
			continue
		}
		p := StagedKinds[i].Name
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

// ProbeLayouts is KindLayouts plus every staged row: the walk the probes
// read, so a staged actor's enemyice and gate byte are read too. The
// pipelines keep KindLayouts.
var ProbeLayouts = func() actors.Layouts {
	l := actors.Layouts{}
	for p, v := range KindLayouts {
		l[p] = v
	}
	for _, k := range StagedKinds {
		l[int16(k.Proc)] = actors.Layout{Ice: k.IceOff, Mode: k.GateOff, ModeU8: true}
	}
	return l
}()

// The Darknut's flags2 bits (design rel_changes R-1 Flags2; the REL's
// EMK_TN_F2_* land with the REL step). KindF2Light stays generic.
const (
	TnF2HasWeapon uint8 = 1 << 0 // m0C34 != 0 (d_a_tn.cpp:478)
	TnF2PlaySkip  uint8 = 1 << 1 // m03EE != 0; == mailbox.EnemySF2PlaySkip
	TnF2Attn      uint8 = 1 << 3 // attention_info.flags != 0 (as BK)
)

// tnBckNames is Tn.arc's body BCKs, index TnBckFirst+i
// (assets/GZLE01/res/Object/Tn.h:44-89; design D7).
var tnBckNames = []string{
	"AAT_KAITEN1", "AAT_KAITEN2", "AAT_KAITEN3", "AAT_KAITEN4", "AAT_KAITEN_FB1", "AAT_KAITEN_LR1", "AAT_TATE1", "AAT_YOKO1",
	"AGUARD1", "AGUARD2", "AHAJIKI1", "AHAKAI1", "AHAZURE1", "AKAMAE1", "AKIDUKU1", "AKYORO1", "AMAHI1", "ANIOU1",
	"ASURI_FB1", "ASURI_LR1", "ATATE_ON1", "AWAIT1", "AWALK1", "BAT_JUMP1", "BAT_KAITEN2", "BAT_KAITEN3", "BAT_KAITEN4",
	"BAT_KERI1", "BAT_SYOUTEI_L1", "BAT_SYOUTEI_S1", "BAT_TATE1", "BAT_YOKO1", "BBACK1", "BBACK_S1", "BDOWN_A1", "BDOWN_U1",
	"BHIROU_K1", "BKAMAE_S1", "BOKIRU_A1", "BOKIRU_U1", "BRUN1", "BRUN2", "BSURI_SFB1", "BSURI_SLR1", "S_DEMO_M1", "S_DEMO_M2",
}

// stagedBckNames is each staged row's names, by id - KindTN.
var stagedBckNames = [...][]string{tnBckNames}

// stagedParamsOK is the design's params gate for the staged rows
// (go_changes ParamsOK, "a second check"): a Darknut whose behaviour
// nibble params & 0xF is 12..15 is refused (12 and 13/14 start in S_DEMO
// and 13/14 are BOSS, d_a_tn.cpp:4244-4262; 15 is the frozen-in-time
// statue).
func stagedParamsOK(id KindID, params uint32) bool {
	if id == KindTN {
		return params&0xF < 12
	}
	return true
}

// TnLiveRefusal is the design's TN LiveOK (go_changes kinds.go, mirroring
// the REL gate R-4): "" when the live bytes pass, else why not: BOSS
// status (D3), behaviour 15, dr.mAction 23 (D_DOZOU) or 25 (S_DEMO),
// m02C1 or m1518 (hidden), m14F8 (demo camera), m140C (the sword is not
// attached yet) or a cape (mEquipmentType >= 4, D5). B0 only prints it.
func TnLiveRefusal(g LiveGate) string {
	switch {
	case g.Boss:
		return "boss"
	case g.Mode == addr.TnBehaviorFrozen:
		return "frozen"
	case g.DrAction == addr.TnActDDozou:
		return "d_dozou"
	case g.DrAction == addr.TnActSDemo:
		return "s_demo"
	case g.M02C1 != 0:
		return "m02c1"
	case g.M1518 != 0:
		return "m1518"
	case g.M14F8 != 0:
		return "m14f8"
	case g.M140C != 0:
		return "m140c"
	case g.Equip >= addr.TnEquipCape:
		return "cape"
	}
	return ""
}

// readTnGate reads a Darknut's LiveGate bytes beyond Mode (the design's
// readAllowGate TN case: s16 dr.mAction 0x47A, 0x2C1, 0x1518, 0x14F8,
// 0x140C, 0x3C5); Boss is the walk's status word.
func readTnGate(m mem.Reader, a *actors.Actor, g *LiveGate) error {
	var err error
	u8 := func(off uint32) uint8 {
		if err != nil {
			return 0
		}
		var v uint8
		v, err = mem.ReadU8(m, a.Addr+off)
		return v
	}
	g.Boss = a.Boss()
	g.M02C1 = u8(addr.TnOffM02C1)
	g.Equip = u8(addr.TnOffEquip)
	g.M140C = u8(addr.TnOffM140C)
	g.M14F8 = u8(addr.TnOffM14F8)
	g.M1518 = u8(addr.TnOffM1518)
	if err != nil {
		return err
	}
	v, err := mem.ReadU16(m, a.Addr+addr.TnOffDRAction)
	g.DrAction = int16(v)
	return err
}

// ClassifyStaged is ClassifyWalk for the staged kinds s enables over a
// walk read with ProbeLayouts: initState 2; keyed, or unkeyed with the
// kind's "+u"; home room == room; the gate byte read; the design's params
// gate; no duplicate Ident. A Darknut's live gate bytes are read into
// Live (LiveSpanOK) but do not refuse it: B0's probes sample it through
// the states the gate refuses at adopt (a D_DOZOU statue waking, S_DEMO)
// and print TnLiveRefusal beside it. Sorted by Ident.
func ClassifyStaged(m mem.Reader, w *actors.Walk, room int8, s StagedSet) ([]Enemy, error) {
	var out []Enemy
	for i := range w.Actors {
		a := &w.Actors[i]
		id := StagedOf(uint16(a.Proc))
		if id == KindNone || !s.Has(id) || a.HomeRoom != room {
			continue
		}
		e := Enemy{Kind: id, Ident: ActorIdent(a), Addr: a.Addr, PID: a.PID, Params: a.Params, HomeRoom: a.HomeRoom, HomePos: a.HomePos}
		e.Live.Mode, e.LiveModeOK = uint8(a.Mode), a.ModeOK
		e.Live.Carried = a.Carried()
		switch {
		case a.InitState != 2:
			e.Refused = RefuseInit
		case a.SetID == addr.ActorSetIDNone && a.HomeRoom >= 0 && int(a.HomeRoom) < addr.RoomCount:
			if !s.TakesUnkeyed(id) {
				e.Refused = RefuseUnkeyedOff
			}
		case !a.Keyed():
			e.Refused = RefuseNotKeyed
		}
		if e.Refused == "" && !a.ModeOK {
			e.Refused = RefuseNoGate
		}
		if e.Refused == "" && id == KindTN {
			if err := readTnGate(m, a, &e.Live); err != nil {
				return nil, err
			}
			e.LiveSpanOK = true
		}
		if e.Refused == "" && !stagedParamsOK(id, a.Params) {
			e.Refused = RefuseParams
		}
		out = append(out, e)
	}
	RefuseDuplicates(out)
	slices.SortStableFunc(out, func(a, b Enemy) int { return compareIdent(a.Ident, b.Ident) })
	return out, nil
}

// ReadStagedSample samples a staged kind's e (the probes' path; the
// pipelines' ReadKindSample knows only the Kinds rows).
func ReadStagedSample(m mem.Reader, e *Enemy) (KindSample, error) {
	if e.Kind == KindTN {
		return ReadSampleTN(m, e)
	}
	return KindSample{}, fmt.Errorf("enemysync: no staged sampler for kind %s (proc 0x%04X)", e.Kind, e.Ident.Proc)
}

// TnFields is what ReadSampleTN reads beyond the Slot fields: what P0-b
// checks live (design test_plan) and the enemy-watch probe prints.
type TnFields struct {
	// Behavior is mBehaviorType, DeathSwitch mDisableSpawnOnDeathSwitch,
	// M02C1 the spawn-switch wait, Equip mEquipmentType, ArmorColor
	// mArmorColorIndex, Pieces mRemainingEquipmentPieces (addr.TnPiece*).
	Behavior, DeathSwitch, M02C1, Equip, ArmorColor uint8
	Pieces                                          int8
	// ShieldMorf and ArmorMorf are the other two morfs; Parts each tn_p's
	// m08 and m09 (part_draw skips m08 < 0).
	ShieldMorf, ArmorMorf uint32
	Parts                 [3][2]int8
	// PlaySkip m03EE (PLAY_SKIP), Invuln m03F0 (INVULN), Weapon m0C34
	// (HAS_WEAPON), BokoPID mBokoPcId (the sword).
	PlaySkip, Invuln int16
	Weapon           int32
	BokoPID          uint32
	// M140C (sword not attached yet), M1444 (the parry window), M14F8
	// (demo camera), M1518 (hidden), M1402 (the part wobble).
	M140C, M1444, M14F8, M1518 int8
	M1402                      int16
	// DefCenter is m13E4 and DefR the defence sphere's radius.
	DefCenter [3]float32
	DefR      float32
	// SubMtd is the actor's actor_method_class* (+0xEC); the P0-c recipe
	// reads its execute (+8).
	SubMtd uint32
}

// The Darknut's spans (ReadSampleTN).
const (
	tnReadMidOff  = addr.TnOffBehavior                        // mBehaviorType .. m0C34
	tnReadMidLen  = addr.TnOffWeapon + 4 - addr.TnOffBehavior // 0x97C: through +0xC38
	tnReadCollOff = addr.TnOffCoCyl                           // mCoCyl .. mWepon2Sph
	tnReadCollLen = addr.TnOffHeadCenter - addr.TnOffCoCyl    // 0x710
	tnReadTailOff = addr.TnOffHeadCenter                      // m1384 .. mEnemyIce
	tnReadTailLen = addr.TnOffIce + addr.EnemyIceSize - addr.TnOffHeadCenter
	// TnReadsPerSample is how many Reads ReadSampleTN makes (four when the
	// morf is not in MEM1). KindReadsPerSample (BK, CC) is unchanged.
	TnReadsPerSample = 5
)

// tnColliders is the Darknut's colliders in header order (d_a_tn.h:94-99).
var tnColliders = []colliderSpec{{"co", addr.TnOffCoCyl, true}, {"tg", addr.TnOffTgCyl, true}, {"head", addr.TnOffHeadSph, false},
	{"def", addr.TnOffDefSph, false}, {"wepon", addr.TnOffWeponSph, false}, {"wepon2", addr.TnOffWepon2Sph, false}}

// ReadSampleTN reads a Darknut's tick in five Reads (design go_changes
// block.go ReadSampleTN): R1 the actor (current.angle is AngleY, D8; the
// method table pointer +0xEC), R2 +0x2BC..+0xC38 (mBehaviorType ..
// m0C34: the morfs, pieces, parts, equipment, m03EE, m03F0, dr), R3 the
// six colliders (+0xC74..+0x1384), R4 +0x1384..+0x1534 (m1384 .. the
// enemyice: m13E4, m1402, m140C, mBokoPcId, m1444, m14F8, m1518, the ice
// timers), R5 the body morf. Action is dr.mAction, Mode dr.mMode, Tilt
// dr.m48C.x, TiltZ m48C.z; Aux {m48C.z, 0, 0, 0}; BlurShape pieces & 7
// (never BlurScale). Flags: DYING dr.mAction 20..22 or the ice shrink
// timer, never health (c_damagereaction.cpp:1147-1152); INVULN m03F0;
// DEFENCE dr.mAction 10; RAGDOLL dr.mMode <= -100; HIDDEN m02C1 or m1518.
// flags2: HAS_WEAPON m0C34, PLAY_SKIP m03EE, ATTN. A Darknut is never
// dormant (go_changes host.go).
func ReadSampleTN(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindTN, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcTN)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcTN
	allowActorFields(&s, b)
	be := binary.BigEndian
	tn := &s.Tn
	tn.SubMtd = be.Uint32(b[addr.ActorOffSubMtd:])

	d, err := readExact(m, e.Addr+tnReadMidOff, tnReadMidLen, "Darknut mBehaviorType..m0C34")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-tnReadMidOff:] }
	tn.Behavior = mid(addr.TnOffBehavior)[0]
	tn.DeathSwitch = mid(addr.TnOffDeathSwitch)[0]
	tn.M02C1 = mid(addr.TnOffM02C1)[0]
	morf := be.Uint32(mid(addr.TnOffMorf))
	tn.ShieldMorf = be.Uint32(mid(addr.TnOffShieldMorf))
	tn.ArmorMorf = be.Uint32(mid(addr.TnOffArmorMorf))
	tn.Pieces = int8(mid(addr.TnOffPieces)[0])
	for i := range tn.Parts {
		p := uint32(addr.TnOffParts + i*addr.TnPartStride)
		tn.Parts[i] = [2]int8{int8(mid(p + addr.TnPartOffM08)[0]), int8(mid(p + addr.TnPartOffM09)[0])}
	}
	tn.ArmorColor = mid(addr.TnOffArmorColor)[0]
	tn.Equip = mid(addr.TnOffEquip)[0]
	tn.PlaySkip = int16(be.Uint16(mid(addr.TnOffPlaySkip)))
	tn.Invuln = int16(be.Uint16(mid(addr.TnOffInvuln)))
	s.Mode = int16(be.Uint16(mid(addr.TnOffDRMode)))
	s.Action = int16(be.Uint16(mid(addr.TnOffDRAction)))
	s.Tilt = int16(be.Uint16(mid(addr.TnOffTilt)))
	s.TiltZ = int16(be.Uint16(mid(addr.TnOffTiltZ)))
	s.DRInvincible = int16(be.Uint16(mid(addr.TnOffDRInvincible)))
	tn.Weapon = int32(be.Uint32(mid(addr.TnOffWeapon)))
	s.Aux = [4]int16{s.TiltZ, 0, 0, 0}
	s.BlurShape = uint8(tn.Pieces) & (addr.TnPieceArmor | addr.TnPieceHelmet | addr.TnPieceShield)

	st := &s.St
	if st.Colliders, err = readColliders(m, e.Addr, tnReadCollOff, tnReadCollLen, tnColliders); err != nil {
		return s, err
	}
	tn.DefR = st.Colliders[3].R

	tail, err := readExact(m, e.Addr+tnReadTailOff, tnReadTailLen, "Darknut m1384..enemyice")
	if err != nil {
		return s, err
	}
	t := func(off uint32) []byte { return tail[off-tnReadTailOff:] }
	for i := range uint32(3) {
		tn.DefCenter[i] = math.Float32frombits(be.Uint32(t(addr.TnOffDefCenter + 4*i)))
	}
	tn.M1402 = int16(be.Uint16(t(addr.TnOffM1402)))
	tn.M140C = int8(t(addr.TnOffM140C)[0])
	tn.BokoPID = be.Uint32(t(addr.TnOffBokoPID))
	tn.M1444 = int8(t(addr.TnOffM1444)[0])
	tn.M14F8 = int8(t(addr.TnOffM14F8)[0])
	tn.M1518 = int8(t(addr.TnOffM1518)[0])
	s.IceTimer = int8(t(addr.TnOffIceShrink)[0])
	s.IceFreeze = int16(be.Uint16(t(addr.TnOffIceFreeze)))

	if err := readMorfAnim(m, morf, &s.Sample); err != nil {
		return s, err
	}

	if (s.Action >= addr.TnActFail && s.Action <= addr.TnActWaterFail) || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if tn.Invuln != 0 {
		s.Flags |= SlotInvuln
	}
	if s.Action == addr.TnActDefence {
		s.Flags |= SlotDefence
	}
	if s.Mode <= -100 {
		s.Flags |= SlotRagdoll
	}
	if tn.M02C1 != 0 || tn.M1518 != 0 {
		s.Flags |= SlotHidden
		s.Hidden = true
	}
	if tn.Weapon != 0 {
		s.Flags2 |= TnF2HasWeapon
	}
	if tn.PlaySkip != 0 {
		s.Flags2 |= TnF2PlaySkip
	}
	if s.Attn != 0 {
		s.Flags2 |= TnF2Attn
	}
	st.Dormant = false
	return s, allowFinish(&s, e, "Darknut")
}
