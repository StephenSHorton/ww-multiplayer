package enemysync

import (
	"encoding/binary"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The warship's row helpers (wind#46, docs/design/enemy-rows-next.md
// "Enemy mirror row: Warship (OSHIP, proc 0xB4)"; the row Kinds[KindOSHIP],
// off by default: oship+u, every disc placement being unkeyed). Option B:
// no hit relay (the client's cannonball reaches the host's real ship as
// its fxsync copy), so it has no colliders here and no hit scan. The REL's
// side is em_oship_gate, em_oship_body (EMK_MTX_MODEL), EMK_COLL_OSHIP,
// the OSHIP handback and em_oship_sink, the copy's own-delete death
// (inject/rel/enemy_mirror.c).

// OshipF2Attn is the warship's flags2 attention bit (wwenemy_kinds.h
// EMK_OSHIP_F2_ATTN, pinned by TestKindTablePins).
const OshipF2Attn uint8 = 1 << 0

// OshipFields is what ReadSampleOship reads beyond the Slot fields.
type OshipFields struct {
	// Proc is mCurrentProc (3 DELETE), SubMode mSubMode, SwitchA its sink
	// switch, ModelType mModelType (0xFF normal, 00 the gold ship).
	Proc                        int32
	SubMode, SwitchA, ModelType uint8
	// HitTimer is mHitTimer (INVULN while nonzero); AimY and AimX are
	// mAimRotY / mAimRotX (aux[0], aux[1]).
	HitTimer   int32
	AimY, AimX int16
	// Model is mpModel.
	Model uint32
}

// The warship's spans (ReadSampleOship).
const (
	oshipReadMidOff     = addr.OshipOffProc             // mCurrentProc .. m2B7
	oshipReadMidLen     = 0x2B8 - addr.OshipOffProc     // 0xC
	oshipReadTailOff    = addr.OshipOffHitTimer         // mHitTimer .. mAimRotXTarget
	oshipReadTailLen    = 0xD8C - addr.OshipOffHitTimer // 0x1C
	OshipReadsPerSample = 4
)

// OshipLiveRefusal is the warship's LiveOK: "" when the live bytes pass,
// else why not: the gold ship (mModelType 00, its GOLD_SHIP_DELETE event;
// Mode is mModelType).
func OshipLiveRefusal(g LiveGate) string {
	if uint8(g.Mode) != addr.OshipModelNormal {
		return "gold"
	}
	return ""
}

// ReadSampleOship reads a warship's tick in four Reads (design §3): R1 the
// actor (shape_angle.y is AngleY, the row's AngleOff), R2 +0x2AC..+0x2B8
// (mCurrentProc .. mModelType), R3 +0xD70..+0xD8C (mHitTimer ..
// mAimRotXTarget), R4 mpModel. Mode is mModelType, Action mSubMode; Tilt
// is shape.x and TiltZ shape.z (slot +0x18), raw: the host's wave roll and
// attack sway are in them; Aux {mAimRotY, mAimRotX, 0, 0}. Flags: DYING
// mCurrentProc 3 (sinking) or health <= 0; INVULN mHitTimer. HIDDEN never.
// flags2: ATTN. No morf: MorfOK stays false (no BCK stream, Kind.NoAnim).
func ReadSampleOship(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindOSHIP, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcOship)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcOship
	allowActorFields(&s, b)
	s.Bck, s.AnmAttr = addr.Mo2BckNone, AnmAttrUnknown
	be := binary.BigEndian
	o := &s.Oship

	d, err := readExact(m, e.Addr+oshipReadMidOff, oshipReadMidLen, "warship mCurrentProc..mModelType")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-oshipReadMidOff:] }
	o.Proc = int32(be.Uint32(mid(addr.OshipOffProc)))
	o.SubMode = mid(addr.OshipOffSubMode)[0]
	o.SwitchA = mid(addr.OshipOffSwitchA)[0]
	o.ModelType = mid(addr.OshipOffModelType)[0]

	t, err := readExact(m, e.Addr+oshipReadTailOff, oshipReadTailLen, "warship mHitTimer..mAimRotXTarget")
	if err != nil {
		return s, err
	}
	tail := func(off uint32) []byte { return t[off-oshipReadTailOff:] }
	o.HitTimer = int32(be.Uint32(tail(addr.OshipOffHitTimer)))
	o.AimY = int16(be.Uint16(tail(addr.OshipOffAimY)))
	o.AimX = int16(be.Uint16(tail(addr.OshipOffAimX)))

	mp, err := readExact(m, e.Addr+addr.OshipOffModel, 4, "warship mpModel")
	if err != nil {
		return s, err
	}
	o.Model = be.Uint32(mp)

	s.Mode, s.Action = int16(o.ModelType), int16(o.SubMode)
	s.Tilt, s.TiltZ = s.St.ShapeAngle[0], s.St.ShapeAngle[2]
	s.Aux = [4]int16{o.AimY, o.AimX, 0, 0}
	if o.Proc == addr.OshipProcDelete || s.Health <= 0 {
		s.Flags |= SlotDying
	}
	if int16(o.HitTimer) != 0 {
		s.Flags |= SlotInvuln
	}
	if s.Attn != 0 {
		s.Flags2 |= OshipF2Attn
	}
	return s, allowFinish(&s, e, "warship")
}
