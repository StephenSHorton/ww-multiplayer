package enemysync

import (
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The staged kinds of the allow batch (ww-multiplayer-handoff/design/
// enemy-mirror-allow.json, "type_table" KI, AM, AM2; batch A0): the
// Keese, the Armos Knight and the Armos, read on the host by the probes
// (enemy-watch --kind ki|am|am2, enemy-match --dry) and nothing else.
// They are NOT rows of Kinds: KindHash, KindLayouts, ParseKinds, the
// KindSet arrays and the pipelines see only MO2, BK and CC until batch A1
// moves these rows into the table with their REL cases. Their ids are the
// ones wwenemy_kinds.h stages (EMK_KIND_KI 3, EMK_KIND_AM 4, EMK_KIND_AM2
// 5); KindID.Row() is nil for them, StagedRow() is not.

// The staged ids.
const (
	KindKI  KindID = 3
	KindAM  KindID = 4
	KindAM2 KindID = 5
)

// StagedKinds is the staged rows, KindKI first; GateOff is each class's
// u8 mAction (the walk's actors.Actor.Mode). The design's AngleOff (D13)
// is not a field yet: A0's enemy-watch prints current.angle and
// shape_angle side by side to settle it.
var StagedKinds = [...]Kind{
	{ID: KindKI, Name: "ki", Proc: addr.ProcKI, Arc: addr.KiArc, BckFirst: addr.KiBckFirst, BckLast: addr.KiBckLast, BckWait: addr.KiBckWait,
		MorfOff: addr.KiOffMorf, IceOff: addr.KiOffIce, GateOff: addr.KiOffAction},
	{ID: KindAM, Name: "am", Proc: addr.ProcAM, Arc: addr.AmArc, BckFirst: addr.AmBckFirst, BckLast: addr.AmBckLast, BckWait: addr.AmBckWait,
		MorfOff: addr.AmOffMorf, IceOff: addr.AmOffIce, GateOff: addr.AmOffAction},
	{ID: KindAM2, Name: "am2", Proc: addr.ProcAM2, Arc: addr.Am2Arc, BckFirst: addr.Am2BckFirst, BckLast: addr.Am2BckLast, BckWait: addr.Am2BckWait,
		MorfOff: addr.Am2OffMorf, IceOff: addr.Am2OffIce, GateOff: addr.Am2OffAction},
}

// StagedRow returns id's staged row, nil when id is not staged.
func (id KindID) StagedRow() *Kind {
	if id < KindKI || int(id-KindKI) >= len(StagedKinds) {
		return nil
	}
	return &StagedKinds[id-KindKI]
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

// StagedByName returns the staged row named name ("ki", "am", "am2").
func StagedByName(name string) (*Kind, bool) {
	for i := range StagedKinds {
		if StagedKinds[i].Name == name {
			return &StagedKinds[i], true
		}
	}
	return nil, false
}

// ProbeLayouts is KindLayouts plus every staged row: the walk the probes
// read, so a staged actor's enemyice and mAction are read too. The
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

// The staged kinds' flags2 bits (design type_table sampler; the REL's
// EMK_*_F2_* land with A1). KindF2Light stays generic.
const (
	KiF2Attn  uint8 = 1 << 0 // attention_info.flags (+0x280) != 0
	KiF2Lunge uint8 = 1 << 2 // mAction 3 or 10 with mBehaviorType 3 (d_a_ki.cpp:502, 577-580)

	AmF2Attn     uint8 = 1 << 0 // attention_info.flags != 0
	AmF2PlaySkip uint8 = 1 << 1 // mCountDownTimers[1] != 0 (d_a_am.cpp:862, 1034-1036)

	Am2F2Attn    uint8 = 1 << 0 // attention_info.flags != 0
	Am2F2WeakTg  uint8 = 1 << 1 // mWeakSph Tg mSPrm Set
	Am2F2BodyCo  uint8 = 1 << 2 // mBodyCyl Co mSPrm Set (cleared while carried, d_a_am2.cpp:694, 709)
	Am2F2Carried uint8 = 1 << 3 // actor_status CARRY (info)
)

// The staged kinds' BCK names, index First+i (assets/GZLE01/res/Object/
// Ki.h:18-23, Am.h:25-36, Am2.h:11-20).
var stagedBckNames = [...][]string{
	{"ATTACK1", "BITA1", "DAMAGE1", "FLY1", "FLY2", "WAIT1"},
	{"BOM_NOMI", "CLOSE", "CLOSE_LOOP", "DAMAGE", "DAMAGE_END", "DAMAGE_LOOP", "DEAD", "OKIRU", "OPEN", "OPEN_LOOP", "SLEEP", "SLEEP_LOOP"},
	{"BURUBURU", "DAMAGE", "DEAD1", "DEAD2", "DEAD3", "JUMP", "MAHI", "SLEEP", "START", "WAIT"},
}

// stagedParamsOK is the design's params gate for the staged rows
// (go_changes ParamsOK): a keyed Keese takes any params, an unkeyed one
// only params & 0xFF == 1; the Armos Knight and the Armos take any.
func stagedParamsOK(id KindID, setID uint16, params uint32) bool {
	if id == KindKI && setID == addr.ActorSetIDNone {
		return params&0xFF == 1
	}
	return true
}

// ClassifyStaged is ClassifyWalk for the staged kinds in ids (every one
// when ids is empty) over a walk read with ProbeLayouts: initState 2;
// keyed, or unkeyed (the probe takes every staged kind's unkeyed actors);
// home room == room; the gate byte read; the design's params gate; no
// duplicate Ident. There is no live gate yet: the design's LiveOK cases
// (KI mAction and m2DC, AM's ITAI modes, AM2's carry, MODORU, abyss and
// dance) land with the rows in A1, so "ok" here means only that the
// placement and params would be taken. Sorted by Ident.
func ClassifyStaged(w *actors.Walk, room int8, ids ...KindID) []Enemy {
	var out []Enemy
	for i := range w.Actors {
		a := &w.Actors[i]
		id := StagedOf(uint16(a.Proc))
		if id == KindNone || (len(ids) > 0 && !slices.Contains(ids, id)) || a.HomeRoom != room {
			continue
		}
		e := Enemy{Kind: id, Ident: ActorIdent(a), Addr: a.Addr, PID: a.PID, Params: a.Params, HomeRoom: a.HomeRoom, HomePos: a.HomePos}
		e.Live.Mode, e.LiveModeOK = uint8(a.Mode), a.ModeOK
		switch {
		case a.InitState != 2:
			e.Refused = RefuseInit
		case a.SetID == addr.ActorSetIDNone && a.HomeRoom >= 0 && int(a.HomeRoom) < addr.RoomCount:
		case !a.Keyed():
			e.Refused = RefuseNotKeyed
		}
		if e.Refused == "" && !a.ModeOK {
			e.Refused = RefuseNoGate
		}
		if e.Refused == "" && !stagedParamsOK(id, a.SetID, a.Params) {
			e.Refused = RefuseParams
		}
		out = append(out, e)
	}
	RefuseDuplicates(out)
	slices.SortStableFunc(out, func(a, b Enemy) int { return compareIdent(a.Ident, b.Ident) })
	return out
}
