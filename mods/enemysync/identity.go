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

// ActorIdent is a's Ident, its pkey computed from the walk's fields.
func ActorIdent(a *actors.Actor) Ident {
	return Ident{Proc: uint16(a.Proc), SetID: a.SetID, PKey: PKey(uint16(a.Proc), a.SetID, a.Params, a.HomeRoom, a.HomePos)}
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
	// Live.InstaKill were (an actor refused before the gate read leaves
	// them zero, unread).
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
)

// ccGateSpan is the ChuChu's live gate read: mBehaviorType through m304.
const ccGateSpan = addr.CcOffInstaKill + 1 - addr.CcOffBehavior

// Classify walks the actors and returns every actor of an enabled kind
// whose home room is room, each with its Ident and why it is refused ("" when the mirror takes it), sorted by Ident. The rule
// (design identity.host_side): initState 2; keyed, or unkeyed with the
// kind's "+u"; home room == room; the kind's ParamsOK and LiveOK; and no
// other live actor with the same Ident (both are refused).
func Classify(m mem.Reader, room int8, set KindSet) ([]Enemy, error) {
	w, err := actors.ReadWith(m, 0, KindLayouts)
	if err != nil {
		return nil, err
	}
	return ClassifyWalk(m, &w, room, set)
}

// ClassifyWalk is Classify over a walk already read with KindLayouts.
func ClassifyWalk(m mem.Reader, w *actors.Walk, room int8, set KindSet) ([]Enemy, error) {
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
		if e.Refused == "" && !k.ParamsOK(a.Params) {
			e.Refused = RefuseParams
		}
		if e.Refused == "" && !k.LiveOK(e.Live) {
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
func Candidates(m mem.Reader, room int8, set KindSet) ([]Enemy, error) {
	all, err := Classify(m, room, set)
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
