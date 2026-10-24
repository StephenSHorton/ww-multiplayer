package enemysync

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Enemy identity for the generalised mirror (design "identity"): an
// actor is named by {proc, setID, pkey}. A keyed placement (setID <
// 0x200, home room 0..63) pairs across machines by (proc, setID); an
// unkeyed one (setID 0xFFFF, home room 0..63) by its pkey, which hashes
// what both machines' placement gave it and nothing the actor changes.

// Ident names one enemy.
type Ident struct {
	Proc  uint16
	SetID uint16
	PKey  uint32
}

// Keyed reports whether the Ident is a keyed placement.
func (id Ident) Keyed() bool { return id.SetID < addr.ZoneActorCount }

func (id Ident) String() string {
	return fmt.Sprintf("proc 0x%04X setID 0x%04X pkey 0x%08X", id.Proc, id.SetID, id.PKey)
}

// compareIdent orders Idents by proc, setID, then pkey.
func compareIdent(a, b Ident) int {
	return cmp.Or(cmp.Compare(a.Proc, b.Proc), cmp.Compare(a.SetID, b.SetID), cmp.Compare(a.PKey, b.PKey))
}

// FNV-1a-32 (design identity.pkey).
const (
	fnvOffset uint32 = 0x811C9DC5
	fnvPrime  uint32 = 0x01000193
)

// PKeyLen is how many bytes PKey hashes: proc BE16, setID BE16, params
// BE32, home room s8, and the home position's three f32s as raw BE32
// bits. The design says 19; the fields it lists add up to 21.
const PKeyLen = 2 + 2 + 4 + 1 + 3*4

// PKey is the placement key: FNV-1a-32 (offset 0x811C9DC5, prime
// 0x01000193) over proc, setID, params (+0xB0, base_process_class
// mParameters, f_pc_base.h:30), home.roomNo (+0x1E2) and home.pos's raw
// bits (+0x1D0..+0x1DB, actor_place home, f_op_actor.h:285-287), all
// big-endian; a hash of 0 is returned as 1 (0 means "no key" in a block
// slot). The C copy (em_pkey) must hash the same bytes in the same order.
func PKey(proc, setID uint16, params uint32, homeRoom int8, homePos [3]uint32) uint32 {
	var b [PKeyLen]byte
	b[0], b[1] = byte(proc>>8), byte(proc)
	b[2], b[3] = byte(setID>>8), byte(setID)
	b[4], b[5], b[6], b[7] = byte(params>>24), byte(params>>16), byte(params>>8), byte(params)
	b[8] = byte(homeRoom)
	for i, v := range homePos {
		o := 9 + 4*i
		b[o], b[o+1], b[o+2], b[o+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	return pkeyFinish(fnv1a(b[:]))
}

func fnv1a(b []byte) uint32 {
	h := fnvOffset
	for _, c := range b {
		h ^= uint32(c)
		h *= fnvPrime
	}
	return h
}

func pkeyFinish(h uint32) uint32 {
	if h == 0 {
		return 1
	}
	return h
}

// PKeyFor is PKey under kind k's rule: a keyed (setID < 0x200) actor of
// a PKeyNoPos row hashes home.pos as zero (allow design D9; REL em_pkey
// with EMK_CAP_PKEY_NO_POS, R-16): a wait-type Keese rewrites its home on
// its ceiling return (d_a_ki.cpp:675-682), and a keyed setID is unique per
// room anyway. An unkeyed actor keeps its home (D10), and so does every
// other row. k nil is PKey.
func PKeyFor(k *Kind, proc, setID uint16, params uint32, homeRoom int8, homePos [3]uint32) uint32 {
	if k != nil && k.PKeyNoPos && setID < addr.ZoneActorCount {
		homePos = [3]uint32{}
	}
	return PKey(proc, setID, params, homeRoom, homePos)
}

// ActorIdent is a's Ident, its pkey computed from the walk's fields under
// its kind's rule (PKeyFor; KindNone hashes as PKey).
func ActorIdent(a *actors.Actor) Ident {
	k := KindOf(uint16(a.Proc)).Row()
	return Ident{Proc: uint16(a.Proc), SetID: a.SetID, PKey: PKeyFor(k, uint16(a.Proc), a.SetID, a.Params, a.HomeRoom, a.HomePos)}
}

// Enemy is an actor of a known kind a walk found in the room, with its
// identity and the static gate's verdict.
type Enemy struct {
	Kind      KindID
	Ident     Ident
	Addr, PID uint32
	Params    uint32
	HomeRoom  int8
	HomePos   [3]uint32 // home.pos, raw f32 bits
	Live      LiveGate
	// LiveModeOK reports whether Live.Mode was read (the walk read the
	// class's mode); LiveSpanOK whether a ChuChu's Live.Color and
	// Live.InstaKill, or an allow kind's Live.SubMode, Fade and Abyss,
	// were (an actor refused before the gate read leaves them zero,
	// unread).
	LiveModeOK, LiveSpanOK bool
	// Refused is why the mirror would not take it ("" when it would).
	Refused string
}

// Why the classification refuses an actor: short tokens the probes print
// as is.
const (
	RefuseNotKeyed   = "not-keyed"   // setID >= 0x200 and not 0xFFFF, or home room outside 0..63
	RefuseUnkeyedOff = "unkeyed-off" // setID 0xFFFF and the kind has no +u
	RefuseInit       = "not-executing"
	RefuseParams     = "params-gate"
	RefuseLive       = "live-gate"
	RefuseDuplicate  = "duplicate-ident"
	RefuseNoGate     = "gate-unreadable"
	RefuseStage      = "stage-gate" // the kind is refused on this stage (an unkeyed Keese on GanonK)
)

// StageGanonK is Puppet Ganon's stage, whose Keese swarms the boss spawns
// unkeyed (d_a_bgn.cpp:3343, d_a_bgn2.cpp:1048) and whose Keese die
// through their own table (d_a_ki.cpp:830).
const StageGanonK = "GanonK"

// StageOK is the kind's gate on the stage (allow design D10): an unkeyed
// Keese is refused on GanonK, a second guard beside UnkeyedParamsOK
// (the swarms are params 0xFFFF0003, which that already refuses). stage
// "" (a caller that has none) passes.
func (k *Kind) StageOK(stage string, setID uint16) bool {
	return !(k.ID == KindKI && setID == addr.ActorSetIDNone && stage == StageGanonK)
}

// ccGateSpan is the ChuChu's live gate read: mBehaviorType through m304.
const ccGateSpan = addr.CcOffInstaKill + 1 - addr.CcOffBehavior

// Classify walks the actors and returns every actor of an enabled kind
// whose home room is room, each with its Ident and why it is refused ("" when the mirror takes it), sorted by Ident. The rule
// (design identity.host_side): initState 2; keyed, or unkeyed with the
// kind's "+u"; home room == room; the kind's StageOK on stage, ParamsOK
// and LiveOK; and no other live actor with the same Ident (both are
// refused).
func Classify(m mem.Reader, stage string, room int8, set KindSet) ([]Enemy, error) {
	w, err := actors.ReadWith(m, 0, KindLayouts)
	if err != nil {
		return nil, err
	}
	return classifyWalk(m, &w, stage, room, set, nil)
}

// The allow kinds' live gate reads beyond the walk's mAction (LiveGate):
// the Keese's m2DC, the Armos Knight's mMode, the Armos's mMode through
// mInAbyssTimer.
const (
	kiGateOff  = addr.KiOffShrinkDelete
	amGateOff  = addr.AmOffMode
	am2GateOff = addr.Am2OffMode
	am2GateLen = addr.Am2OffAbyssTimer + 1 - addr.Am2OffMode
)

// readAllowGate reads an allow kind's LiveGate bytes beyond Mode into g
// (one Read); false when it came back short. The Darknut's are readTnGate's
// (tn.go: six fields across the class, and Boss from the walk's actor).
func readAllowGate(m mem.Reader, id KindID, base uint32, g *LiveGate) (bool, error) {
	var off uint32
	n := 1
	switch id {
	case KindKI:
		off = kiGateOff
	case KindAM:
		off = amGateOff
	case KindAM2:
		off, n = am2GateOff, am2GateLen
	default:
		return true, nil
	}
	b, err := m.Read(base+off, n)
	if err != nil || len(b) != n {
		return false, err
	}
	switch id {
	case KindKI:
		g.Fade = b[0]
	case KindAM:
		g.SubMode = b[0]
	case KindAM2:
		g.SubMode, g.Abyss = b[0], b[addr.Am2OffAbyssTimer-am2GateOff]
	}
	return true, nil
}

// ClassifyWalk is Classify over a walk already read with KindLayouts.
func ClassifyWalk(m mem.Reader, w *actors.Walk, stage string, room int8, set KindSet) ([]Enemy, error) {
	return classifyWalk(m, w, stage, room, set, nil)
}

// liveSticky reports whether kind id's LiveOK is an adopt gate only (the
// allow kinds: the REL's em_validate, design R-8): its refused states (an
// Armos Knight's swallow and death, an Armos's dance, carry and abyss, a
// Keese's FIRE_SET, FAIL and fade) are ones a live enemy passes through,
// and the samplers stream them (ReadSampleKI/AM/AM2: SlotDying,
// SlotInvuln, Am2F2Carried). The Darknut's too (TN design R-4 and the P0
// death order): its gate refuses the ragdoll, dying, D_DOZOU and S_DEMO
// at adopt only, and ReadSampleTN streams them, but a listed Darknut whose
// BOSS status turns on is refused all the same (stickyRefused). Batch
// C's Kargaroc (a knockback is transient, and its death is streamed) and
// Magtail (BALL is the custody signal, DEAD streamed) too.
// MO2/BK/CC keep refusing on every walk. The host's walk passes its whole
// cache (CandidatesSince); the client's match passes only its listed
// Magtails (mtWas), so a custody BALL keeps its block slot.
func liveSticky(id KindID) bool {
	switch id {
	case KindKI, KindAM, KindAM2, KindTN, KindBB:
		return true
	case KindMT:
		return true
	}
	return false
}

// stickyRefused reports a refusal liveSticky does not cover: a Darknut's
// BOSS status (D3; in practice per pkey, since behaviours 13/14 are
// params-refused, but re-read on every walk all the same).
func stickyRefused(id KindID, g LiveGate) bool {
	return id == KindTN && g.Boss
}

// wasListed reports whether e is an actor of was (same PID, address and
// Ident): one an earlier walk took.
func wasListed(was []Enemy, e *Enemy) bool {
	for i := range was {
		if was[i].PID == e.PID && was[i].Addr == e.Addr && was[i].Ident == e.Ident {
			return true
		}
	}
	return false
}

// classifyWalk is ClassifyWalk; an allow-kind actor of was (the caller's
// previous taken list) skips LiveOK (liveSticky), so a host enemy stays
// listed through the states its sampler mirrors.
func classifyWalk(m mem.Reader, w *actors.Walk, stage string, room int8, set KindSet, was []Enemy) ([]Enemy, error) {
	var out []Enemy
	for i := range w.Actors {
		a := &w.Actors[i]
		id := KindOf(uint16(a.Proc))
		if id == KindNone || !set.Has(id) {
			continue
		}
		if a.HomeRoom != room {
			continue
		}
		k := id.Row()
		e := Enemy{Kind: id, Ident: ActorIdent(a), Addr: a.Addr, PID: a.PID, Params: a.Params, HomeRoom: a.HomeRoom, HomePos: a.HomePos}
		e.Live.Mode, e.LiveModeOK = uint8(a.Mode), a.ModeOK
		e.Live.Carried = a.Carried()
		switch {
		case a.InitState != 2:
			e.Refused = RefuseInit
		case a.SetID == addr.ActorSetIDNone && a.HomeRoom >= 0 && int(a.HomeRoom) < addr.RoomCount:
			if !set.TakesUnkeyed(id) {
				e.Refused = RefuseUnkeyedOff
			}
		case !a.Keyed():
			e.Refused = RefuseNotKeyed
		}
		if e.Refused == "" && !k.StageOK(stage, a.SetID) {
			e.Refused = RefuseStage
		}
		if e.Refused == "" && !a.ModeOK {
			e.Refused = RefuseNoGate
		}
		if e.Refused == "" && id == KindCC {
			b, err := m.Read(a.Addr+addr.CcOffBehavior, ccGateSpan)
			if err != nil {
				return nil, err
			}
			if len(b) != ccGateSpan {
				e.Refused = RefuseNoGate
			} else {
				e.Live = LiveGate{Mode: b[0], Color: b[addr.CcOffColor-addr.CcOffBehavior], InstaKill: b[addr.CcOffInstaKill-addr.CcOffBehavior]}
				e.LiveModeOK, e.LiveSpanOK = true, true
			}
		}
		if e.Refused == "" && (id == KindKI || id == KindAM || id == KindAM2) {
			ok, err := readAllowGate(m, id, a.Addr, &e.Live)
			if err != nil {
				return nil, err
			}
			if !ok {
				e.Refused = RefuseNoGate
			} else {
				e.LiveSpanOK = true
			}
		}
		if e.Refused == "" && id == KindTN {
			if err := readTnGate(m, a, &e.Live); err != nil {
				return nil, err
			}
			e.LiveSpanOK = true
		}
		if e.Refused == "" && id == KindBB {
			ok, err := readBbGate(m, a, &e.Live)
			if err != nil {
				return nil, err
			}
			if !ok {
				e.Refused = RefuseNoGate
			} else {
				e.LiveSpanOK = true
			}
		}
		if e.Refused == "" && id == KindMT {
			if err := readMtGate(m, a, &e.Live); err != nil {
				return nil, err
			}
			e.LiveSpanOK = true
		}
		if e.Refused == "" && (!k.ParamsOK(a.Params) || (a.SetID == addr.ActorSetIDNone && !k.UnkeyedParamsOK(a.Params))) {
			e.Refused = RefuseParams
		}
		if e.Refused == "" && !k.LiveOK(e.Live) && (!liveSticky(id) || !wasListed(was, &e) || stickyRefused(id, e.Live)) {
			e.Refused = RefuseLive
		}
		out = append(out, e)
	}
	RefuseDuplicates(out)
	slices.SortStableFunc(out, func(a, b Enemy) int { return compareIdent(a.Ident, b.Ident) })
	return out, nil
}

// RefuseDuplicates marks every taken Enemy whose Ident another taken
// Enemy shares as refused (both of them: neither machine can tell which
// is which). Refused ones do not count.
func RefuseDuplicates(es []Enemy) {
	n := map[Ident]int{}
	for i := range es {
		if es[i].Refused == "" {
			n[es[i].Ident]++
		}
	}
	for i := range es {
		if es[i].Refused == "" && n[es[i].Ident] > 1 {
			es[i].Refused = RefuseDuplicate
		}
	}
}

// Candidates is Classify's taken actors only: the room's mirrorable
// enemies of the enabled kinds, sorted by Ident. Unlike KeyedMoblins it
// does not cap at MaxSlots (picking slots is the client's selection).
func Candidates(m mem.Reader, stage string, room int8, set KindSet) ([]Enemy, error) {
	return CandidatesSince(m, stage, room, set, nil)
}

// CandidatesSince is Candidates after a walk that took was (the host's
// cache): an allow-kind enemy already listed is not dropped by LiveOK
// (liveSticky). Its sampler streams the state instead: the Armos
// Knight's swallow as SlotInvuln and its death as SlotDying, the Armos's
// dance and carry, the Keese's FAIL as SlotDying.
func CandidatesSince(m mem.Reader, stage string, room int8, set KindSet, was []Enemy) ([]Enemy, error) {
	w, err := actors.ReadWith(m, 0, KindLayouts)
	if err != nil {
		return nil, err
	}
	all, err := classifyWalk(m, &w, stage, room, set, was)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, e := range all {
		if e.Refused == "" {
			out = append(out, e)
		}
	}
	return out, nil
}
