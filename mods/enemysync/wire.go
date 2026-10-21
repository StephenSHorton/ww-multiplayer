// Package enemysync is the host-authoritative enemy mirror (the Moblin
// prototype, ww-multiplayer-handoff/design/host-authoritative-prototype.md
// and moblin-mirror-design.json): the host's Go samples its keyed Moblins
// and streams them as MsgEnemy STATE frames on a RoleEnemyHost
// connection; each joiner's Go matches them to its own Moblins, smooths
// the samples and writes one block per tick into the mailbox slack
// (wwapi/mailbox/enemy.go) for the REL module to mirror. The generalised
// mirror (enemy-mirror-general.json; kinds.go, identity.go, wire version
// 4) extends it to Bokoblins and ChuChus, keyed and unkeyed.
//
// This file is the wire format: the STATE, HIT, ACK and (wire version 3,
// the remote-Link design) PHIT, PACK and PSTATE payloads of a MsgEnemy
// frame ([type 'E'][len:2 BE][payload], payload =
// [sub:1][ver:1][body], every field big-endian). The server checks only
// the shape (network.EnemyStateLen and friends); the fields are encoded
// and decoded here. Decoders never panic and refuse a short or garbled
// payload; the encoders refuse NaN and Inf, which the REL would reject
// (and fopAc_Execute asserts on, f_op_actor.cpp:168-172).
package enemysync

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// WireVersion is the ver byte every payload carries. 2: the slot grew to
// 48 bytes with the animation identity (bck, attribute, anm_seq, morf,
// flags2, blur), the HIT to 48 with the sword object's Se/HitMark/Spl and
// At SPrm, and the ACK carries a detail, the REL's frame and Set counts.
// 3 (remote-link-design.md §8; the layouts in inject/include/wwremote.h's
// header comment): the HIT grew to 52 with the client Link's cut type and
// count and the server-stamped player id, and the PHIT, PACK and PSTATE
// subs carry an enemy hit on a remote player's body to that player.
// 4 (enemy-mirror-general.json "wire"): the STATE lists up to
// WireMaxSlots candidates of every enabled kind, each with its kind, pkey,
// a per-epoch handle, wire flags and aux[4]; the header carries the host's
// kind mask, the room's dead-actor words 0..3 and the kind table's hash;
// HIT and ACK name their target by handle, and PHIT's setID field carries
// the attacker's handle. Sub refuses every other version.
const WireVersion byte = 4

// The sub bytes and sizes, from the server's ingress rules.
const (
	SubState = network.EnemySubState
	SubHit   = network.EnemySubHit
	SubAck   = network.EnemySubAck
	// The remote-Link subs (wire version 3).
	SubPHit   = network.EnemySubPHit
	SubPAck   = network.EnemySubPAck
	SubPState = network.EnemySubPState

	StateHeaderLen = network.EnemyStateHeaderLen
	SlotLen        = network.EnemySlotLen
	// WireMaxSlots is how many candidates a STATE may list (16); MaxSlots
	// is how many the block and the REL mirror (4): each client picks its
	// MaxSlots from the WireMaxSlots.
	WireMaxSlots = network.EnemyMaxSlots
	MaxSlots     = mailbox.EnemyMaxSlots
	HitLen       = network.EnemyHitLen
	AckLen       = network.EnemyAckLen
	PHitLen      = network.EnemyPHitLen
	PAckLen      = network.EnemyPAckLen
	PStateLen    = network.EnemyPStateLen

	// StageLen is the stage name field: char[8], NUL-padded, as
	// dStage_startStage_c::mName (d_stage.h:949).
	StageLen = 8

	// DeadWords is how many u32 dead-actor words a STATE carries: the
	// room's zone words 0..3, keyed setIDs 0..127.
	DeadWords = 4
)

// Handles (wire version 4): the host names every listed enemy by a u16
// handle, assigned monotonically per host epoch from 1 and never 0 or
// 0xFFFF; HIT and ACK carry it, and PHIT's SetID field carries the
// attacker's (NoMirrorHandle: no mirrored attacker). A client never writes
// a handle into the REL's apply record: it translates it to an owner id
// (a keyed local setID, OwnerSlotBase|j for block slot j, or 0xFFFF).
const (
	HandleNone     uint16 = 0
	NoMirrorHandle uint16 = 0xFFFF
	OwnerSlotBase         = mailbox.EnemyOwnerSlotBase
)

// Wire slot flags (Slot.WFlags; the wire only, never the block).
const (
	WFlagEngaged uint8 = 1 << 0 // a client HIT or a host PHIT on this enemy within 3 s (CC also its At Set bit): the client keeps its slot
	WFlagTomb    uint8 = 1 << 1 // a tombstone: the enemy died or vanished on the host (sent DYING|RELEASE for 300 host frames)
)

// STATE header flags.
const (
	StateFlagAnim      uint8 = 1 << 0 // the slots' action/mode/attack/morf fields are valid (S2)
	StateFlagColliders uint8 = 1 << 1 // the host wants the mirrors' colliders re-submitted (S3)
	StateFlagInScope   uint8 = 1 << 2 // the host is in play with Link in a room (else a count-0 keepalive)
)

// Slot flags (wire and block share them: wwapi/mailbox.EnemySF*).
const (
	SlotSnap       uint8 = 1 << 0 // the client should snap, not lerp, to this sample
	SlotDying      uint8 = 1 << 1 // health <= 0, death action, or the shrink timer runs
	SlotInvuln     uint8 = 1 << 2 // m05B4 != 0 (d_a_mo2.h:67)
	SlotDefence    uint8 = 1 << 3 // damagereaction action 10 (shield up)
	SlotRagdoll    uint8 = 1 << 4 // damagereaction mode <= -100
	SlotBareHanded uint8 = 1 << 5 // m2943 (d_a_mo2.h:129)
	SlotHidden     uint8 = 1 << 6 // m2A1C (d_a_mo2.h:160)
	SlotRelease    uint8 = 1 << 7 // the client should release this slot (block only)
)

// HIT colliders.
const (
	ColliderHead  uint8 = 1 // mHeadSph (d_a_mo2.h:111)
	ColliderTgCyl uint8 = 2 // mTgCyl (d_a_mo2.h:110)
)

// ACK results (the block's EnemyHR*).
const (
	AckDropped   uint8 = 0 // nothing applied; Detail says why (HD*)
	AckAppliedAt uint8 = 1 // applied through the host's At sphere: the Moblin's own damage code ran (v2)
	AckNoActor   uint8 = 2 // no such Moblin on the host
	AckShrink    uint8 = 3 // applied as the light-shrink kill (v1)
	AckParried   uint8 = 4 // the Moblin's defence sphere took it (v2)
)

// ACK details (the block's EnemyHD*; wire_test pins them to the mailbox
// constants). HDGoGate on are Go-side drops acked at once.
const (
	HDNone           uint8 = 0
	HDNoCross        uint8 = 1  // two Sets, nothing crossed
	HDInvuln         uint8 = 2  // m05B4 still set after the wait
	HDRagdoll        uint8 = 3  // ragdolling
	HDDying          uint8 = 4  // dead or dying
	HDNoLink         uint8 = 5  // the host's player pointer is not Link (a Medli/Makar window)
	HDNoSphere       uint8 = 6  // the host REL's sphere image failed its sanity check
	HDWrongObj       uint8 = 7  // another object's Tg crossed first
	HDAborted        uint8 = 8  // the host REL returned early, released or was deleted mid-request
	HDDefence        uint8 = 9  // v1's pre-drop
	HDFallbackShrink uint8 = 10 // the shrink was written after a miss (WW_ENEMY_HIT_FALLBACK=shrink)
	HDGoGate         uint8 = 11 // epoch, scope, replay or no REL
	HDRate           uint8 = 12 // within HitRate of the last hit on that Moblin
	HDBusy           uint8 = 13 // the host's request queue is full or holds that (client, setID)
	HDGoFlags        uint8 = 14 // the sample's flags pre-dropped it
	HDDemo           uint8 = 15 // the host Link is in a demo
	// HDNoBody is the REL's: ATTRIB asked for the hit to be attributed to
	// the sender's remote body, but that body was not LIVE, so the host
	// Link's path ran (mailbox.RemoteHDNoBody).
	HDNoBody uint8 = mailbox.RemoteHDNoBody
	// HDKind is the REL's (layout 3): the target's kind has no row or is
	// not enabled on the host (mailbox.EnemyHDKind).
	HDKind uint8 = mailbox.EnemyHDKind
)

// ResultName and DetailName name an ACK's result and detail for logs.
func ResultName(r uint8) string {
	switch r {
	case AckDropped:
		return "dropped"
	case AckAppliedAt:
		return "applied_at"
	case AckNoActor:
		return "no_actor"
	case AckShrink:
		return "shrink"
	case AckParried:
		return "parried"
	}
	return fmt.Sprintf("result_%d", r)
}

func DetailName(d uint8) string {
	names := [...]string{"none", "no_cross", "invuln", "ragdoll", "dying", "no_link", "no_sphere", "wrong_obj", "aborted", "defence", "fallback_shrink", "go_gate", "rate", "busy", "go_flags", "demo", "no_body", "kind"}
	if int(d) < len(names) {
		return names[d]
	}
	return fmt.Sprintf("detail_%d", d)
}

// Slot is one enemy in a STATE. The fields are the Moblin's; a Bokoblin
// or ChuChu fills them per kind (enemy-mirror-general.json "flags"): Tilt
// is BK dr.m48C.x or CC m316.x, a ChuChu's Action/Mode are its
// mCurrAction/m2F5, and its AttackType/AttackPhase carry m316.z
// (SetCcM316Z).
type Slot struct {
	SetID       uint16 // < 0x200 keyed, 0xFFFF unkeyed
	Proc        uint16 // the kind's proc (0x00BD, 0x00BE, 0x00CF)
	Pos         [3]float32
	AngleY      int16 // current.angle.y (+0x206)
	Tilt        int16 // MO2 dr.m48C.x (+0xAA0), BK dr.m48C.x (+0x7FC), CC m316.x
	Action      int16 // damagereaction mAction (+0x61A)
	Mode        int16 // damagereaction mMode (+0x618)
	AttackType  uint8 // m2060
	AttackPhase uint8 // m2064
	MorfFrame   float32
	MorfRate    float32
	Health      int8
	Flags       uint8
	// The animation identity (S2): the Mo2 BCK index of mpMorf's mpAnm
	// (addr.Mo2BckFirst..Last, addr.Mo2BckNone unresolved), J3DFrameCtrl's
	// attribute (0xFF unknown), the restart counter, setAnm's morf argument
	// recovered from mMorfStep, the SlotF2* flags and the sword blur.
	Bck       uint8
	AnmAttr   uint8
	AnmSeq    uint8
	Morf      uint8
	Flags2    uint8
	BlurShape uint8
	BlurScale float32
	// Version 4: wire flags (WFlag*), the kind (the client checks it
	// against KindOf(Proc); the decoder does not, so a newer row does not
	// fail the whole STATE), the host actor's placement key, its handle
	// and the kind's four aux values (the block annex's).
	WFlags uint8
	Kind   KindID
	PKey   uint32
	Handle uint16
	Aux    [4]int16
}

// SetCcM316Z stores a ChuChu's m316.z in the AttackType/AttackPhase bytes
// (big-endian), where a Moblin carries its attack.
func (sl *Slot) SetCcM316Z(v int16) {
	sl.AttackType, sl.AttackPhase = uint8(uint16(v)>>8), uint8(v)
}

// CcM316Z is the m316.z SetCcM316Z stored.
func (sl *Slot) CcM316Z() int16 { return int16(uint16(sl.AttackType)<<8 | uint16(sl.AttackPhase)) }

// Ident is the slot's identity.
func (sl *Slot) Ident() Ident { return Ident{Proc: sl.Proc, SetID: sl.SetID, PKey: sl.PKey} }

// Slot flags2 (wire and block share them: wwapi/mailbox.EnemySF2*).
const (
	SlotF2HasWeapon uint8 = 1 << 0 // mbHasInnateWeapon (+0xDD4): the spear shape is drawn
	SlotF2PlaySkip  uint8 = 1 << 1 // m05B2 != 0: the host skips play()
	SlotF2BlurOn    uint8 = 1 << 2 // m0594 != 0: the sword blur mesh is drawn
	SlotF2AnmGate   uint8 = 1 << 3 // m05B0 != 0: anm_init is gated
)

// Animation attributes (J3DFrameCtrl mAttribute, J3DAnimation.h:830-842).
const (
	AnmAttrNone        uint8 = 0
	AnmAttrReset       uint8 = 1
	AnmAttrLoop        uint8 = 2
	AnmAttrReverse     uint8 = 3
	AnmAttrLoopReverse uint8 = 4
	AnmAttrUnknown     uint8 = 0xFF
)

// State is a STATE payload: the host's candidates in its room (at most
// WireMaxSlots, of every enabled kind), or a count-0 keepalive.
type State struct {
	Flags    uint8
	Epoch    uint32
	Seq      uint32
	HostBeat uint32 // the host's spawn_trigger at sample time
	HostMS   uint32 // the host's monotonic ms, sender-relative (the smoother's clock)
	Stage    string // at most StageLen bytes
	Room     int8
	DeadMask uint8 // bit n: keyed setID n's dead-actor bit is set in this room's zone; == uint8(Dead[0]) (SetDead)
	// Version 4: the host's enabled kinds (bit KindID; the client mirrors
	// a kind only when both sides enable it), the room's zone dead-actor
	// words 0..3 (setIDs 0..127) and the host's KindHash (a client
	// refuses a STATE whose hash is not its own).
	HostKindMask uint8
	Dead         [DeadWords]uint32
	KindHash     uint32
	Slots        []Slot
}

// SetDead sets the dead words and DeadMask, their low byte, together.
func (s *State) SetDead(words [DeadWords]uint32) {
	s.Dead, s.DeadMask = words, uint8(words[0])
}

// IsDead reports whether keyed setID id's dead-actor bit is set in the
// STATE's words (false past setID 127).
func (s *State) IsDead(id uint16) bool {
	return int(id) < 32*DeadWords && s.Dead[id/32]&(1<<(id%32)) != 0
}

// Hit is a HIT payload: a client's sword or arrow landed on a mirrored
// copy. Client is the enemy client's connection ID, which the server
// stamps on the way to the host; a client sends 0. Version 4: Handle
// names the target (the host resolves it); SetID stays for logs.
type Hit struct {
	Client      uint8
	Collider    uint8
	Epoch       uint32
	Stage       string
	Room        int8
	Atp         uint8
	SetID       uint16
	Handle      uint16
	HitSeq      uint16
	AtType      uint32
	Pos         [3]float32
	ClientFrame uint32
	// The sword object's dCcD_GObjAt: mSe, mHitMark, mSpl and the low byte
	// of its mSPrm (bit 1 NoHitMark), which the host's At sphere copies.
	Se      uint8
	HitMark uint8
	Spl     uint8
	AtSPrm  uint8
	// Version 3: the client Link's mCutType and mCutCount (+0x290/+0x291,
	// d_a_player.h:479-480) at the hit, which the host stages into the
	// sender's remote body for at_power_check (ATTRIB), and the sender's
	// player id, which the server stamps (network.EnemyHitPlayerIDOff; 0 =
	// unmapped, attributed to the host Link); a client sends 0.
	CutType  uint8
	CutCount uint8
	PlayerID uint8
}

// Ack is an ACK payload: what the host did with a HIT. Client is the HIT's
// stamped connection ID, which the server routes the ACK by; Handle is
// the HIT's (version 4).
type Ack struct {
	Client uint8
	Result uint8
	Epoch  uint32
	SetID  uint16
	Handle uint16
	HitSeq uint16
	Health int8
	Detail uint8 // HD*
	Frames uint8 // REL executes from the first Set to the result
	Sets   uint8 // cCcS::Set calls issued
}

// ErrNotFinite is returned by an encoder for a NaN or Inf.
var ErrNotFinite = errors.New("enemysync: NaN or Inf in a wire field")

func finite(fs ...float32) error {
	for _, f := range fs {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return ErrNotFinite
		}
	}
	return nil
}

func putStage(b []byte, stage string) error {
	if len(stage) > StageLen {
		return fmt.Errorf("enemysync: stage name %q is longer than %d bytes", stage, StageLen)
	}
	for i := range StageLen {
		b[i] = 0
	}
	copy(b, stage)
	return nil
}

func getStage(b []byte) string {
	n := 0
	for n < StageLen && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

// EncodeState builds a STATE payload. It refuses more than WireMaxSlots
// slots, a stage name over StageLen bytes and a NaN or Inf anywhere.
func EncodeState(s *State) ([]byte, error) {
	if len(s.Slots) > WireMaxSlots {
		return nil, fmt.Errorf("enemysync: %d slots (max %d)", len(s.Slots), WireMaxSlots)
	}
	b := make([]byte, StateHeaderLen+SlotLen*len(s.Slots))
	b[0], b[1], b[2], b[3] = SubState, WireVersion, s.Flags, byte(len(s.Slots))
	binary.BigEndian.PutUint32(b[4:], s.Epoch)
	binary.BigEndian.PutUint32(b[8:], s.Seq)
	binary.BigEndian.PutUint32(b[12:], s.HostBeat)
	binary.BigEndian.PutUint32(b[16:], s.HostMS)
	if err := putStage(b[20:28], s.Stage); err != nil {
		return nil, err
	}
	b[28], b[29], b[30], b[31] = byte(s.Room), s.DeadMask, s.HostKindMask, 0
	for i, w := range s.Dead {
		binary.BigEndian.PutUint32(b[32+4*i:], w)
	}
	binary.BigEndian.PutUint32(b[48:], s.KindHash)
	for i := range s.Slots {
		if err := encodeSlot(b[StateHeaderLen+SlotLen*i:], &s.Slots[i]); err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
	}
	return b, nil
}

func encodeSlot(b []byte, sl *Slot) error {
	if err := finite(sl.Pos[0], sl.Pos[1], sl.Pos[2], sl.MorfFrame, sl.MorfRate, sl.BlurScale); err != nil {
		return err
	}
	binary.BigEndian.PutUint16(b[0:], sl.SetID)
	binary.BigEndian.PutUint16(b[2:], sl.Proc)
	for i := range 3 {
		binary.BigEndian.PutUint32(b[4+4*i:], math.Float32bits(sl.Pos[i]))
	}
	binary.BigEndian.PutUint16(b[16:], uint16(sl.AngleY))
	binary.BigEndian.PutUint16(b[18:], uint16(sl.Tilt))
	binary.BigEndian.PutUint16(b[20:], uint16(sl.Action))
	binary.BigEndian.PutUint16(b[22:], uint16(sl.Mode))
	b[24], b[25] = sl.AttackType, sl.AttackPhase
	binary.BigEndian.PutUint32(b[26:], math.Float32bits(sl.MorfFrame))
	binary.BigEndian.PutUint32(b[30:], math.Float32bits(sl.MorfRate))
	b[34], b[35] = byte(sl.Health), sl.Flags
	b[36], b[37], b[38], b[39], b[40], b[41] = sl.Bck, sl.AnmAttr, sl.AnmSeq, sl.Morf, sl.Flags2, sl.BlurShape
	b[42], b[43] = sl.WFlags, uint8(sl.Kind)
	binary.BigEndian.PutUint32(b[44:], math.Float32bits(sl.BlurScale))
	binary.BigEndian.PutUint32(b[48:], sl.PKey)
	binary.BigEndian.PutUint16(b[52:], sl.Handle)
	b[54], b[55] = 0, 0
	for i, v := range sl.Aux {
		binary.BigEndian.PutUint16(b[56+2*i:], uint16(v))
	}
	return nil
}

// Sub returns a payload's sub byte, or an error for an empty payload or a
// version this build does not speak.
func Sub(data []byte) (byte, error) {
	if len(data) < 2 {
		return 0, fmt.Errorf("enemysync: %d-byte payload is shorter than its sub and version bytes", len(data))
	}
	if data[1] != WireVersion {
		return 0, fmt.Errorf("enemysync: wire version %d, this build speaks %d", data[1], WireVersion)
	}
	return data[0], nil
}

// DecodeState parses a STATE payload. The error names the first thing
// wrong: a short payload, another sub, a bad version, too many slots, a
// length that does not match the count, or a NaN or Inf in a slot.
func DecodeState(data []byte) (*State, error) {
	sub, err := Sub(data)
	if err != nil {
		return nil, err
	}
	if sub != SubState {
		return nil, fmt.Errorf("enemysync: sub %d is not STATE", sub)
	}
	if len(data) < StateHeaderLen {
		return nil, fmt.Errorf("enemysync: %d-byte STATE is shorter than its %d-byte header", len(data), StateHeaderLen)
	}
	count := int(data[3])
	if count > WireMaxSlots {
		return nil, fmt.Errorf("enemysync: STATE with %d slots (max %d)", count, WireMaxSlots)
	}
	if len(data) != StateHeaderLen+SlotLen*count {
		return nil, fmt.Errorf("enemysync: %d-byte STATE with %d slots (want %d bytes)", len(data), count, StateHeaderLen+SlotLen*count)
	}
	s := &State{
		Flags:    data[2],
		Epoch:    binary.BigEndian.Uint32(data[4:]),
		Seq:      binary.BigEndian.Uint32(data[8:]),
		HostBeat: binary.BigEndian.Uint32(data[12:]),
		HostMS:   binary.BigEndian.Uint32(data[16:]),
		Stage:    getStage(data[20:28]),
		Room:     int8(data[28]),
		DeadMask: data[29],

		HostKindMask: data[30],
		KindHash:     binary.BigEndian.Uint32(data[48:]),
	}
	for i := range s.Dead {
		s.Dead[i] = binary.BigEndian.Uint32(data[32+4*i:])
	}
	if count > 0 {
		s.Slots = make([]Slot, count)
	}
	for i := range s.Slots {
		if err := decodeSlot(data[StateHeaderLen+SlotLen*i:], &s.Slots[i]); err != nil {
			return nil, fmt.Errorf("enemysync: slot %d: %w", i, err)
		}
	}
	return s, nil
}

func decodeSlot(b []byte, sl *Slot) error {
	sl.SetID = binary.BigEndian.Uint16(b[0:])
	sl.Proc = binary.BigEndian.Uint16(b[2:])
	for i := range 3 {
		sl.Pos[i] = math.Float32frombits(binary.BigEndian.Uint32(b[4+4*i:]))
	}
	sl.AngleY = int16(binary.BigEndian.Uint16(b[16:]))
	sl.Tilt = int16(binary.BigEndian.Uint16(b[18:]))
	sl.Action = int16(binary.BigEndian.Uint16(b[20:]))
	sl.Mode = int16(binary.BigEndian.Uint16(b[22:]))
	sl.AttackType, sl.AttackPhase = b[24], b[25]
	sl.MorfFrame = math.Float32frombits(binary.BigEndian.Uint32(b[26:]))
	sl.MorfRate = math.Float32frombits(binary.BigEndian.Uint32(b[30:]))
	sl.Health, sl.Flags = int8(b[34]), b[35]
	sl.Bck, sl.AnmAttr, sl.AnmSeq, sl.Morf, sl.Flags2, sl.BlurShape = b[36], b[37], b[38], b[39], b[40], b[41]
	sl.WFlags, sl.Kind = b[42], KindID(b[43])
	sl.BlurScale = math.Float32frombits(binary.BigEndian.Uint32(b[44:]))
	sl.PKey = binary.BigEndian.Uint32(b[48:])
	sl.Handle = binary.BigEndian.Uint16(b[52:])
	for i := range sl.Aux {
		sl.Aux[i] = int16(binary.BigEndian.Uint16(b[56+2*i:]))
	}
	return finite(sl.Pos[0], sl.Pos[1], sl.Pos[2], sl.MorfFrame, sl.MorfRate, sl.BlurScale)
}

// EncodeHit builds a HIT payload (HitLen bytes). Client goes out as given;
// the server overwrites it with the sender's connection ID.
func EncodeHit(h *Hit) ([]byte, error) {
	if err := finite(h.Pos[0], h.Pos[1], h.Pos[2]); err != nil {
		return nil, err
	}
	b := make([]byte, HitLen)
	b[0], b[1], b[network.EnemyClientIDOff], b[3] = SubHit, WireVersion, h.Client, h.Collider
	binary.BigEndian.PutUint32(b[4:], h.Epoch)
	if err := putStage(b[8:16], h.Stage); err != nil {
		return nil, err
	}
	b[16], b[17] = byte(h.Room), h.Atp
	binary.BigEndian.PutUint16(b[18:], h.SetID)
	binary.BigEndian.PutUint16(b[20:], h.HitSeq)
	b[22], b[23] = h.Se, h.HitMark
	binary.BigEndian.PutUint32(b[24:], h.AtType)
	for i := range 3 {
		binary.BigEndian.PutUint32(b[28+4*i:], math.Float32bits(h.Pos[i]))
	}
	binary.BigEndian.PutUint32(b[40:], h.ClientFrame)
	b[44], b[45] = h.Spl, h.AtSPrm
	b[46], b[47], b[network.EnemyHitPlayerIDOff] = h.CutType, h.CutCount, h.PlayerID
	binary.BigEndian.PutUint16(b[52:], h.Handle)
	return b, nil
}

// DecodeHit parses a HIT payload.
func DecodeHit(data []byte) (*Hit, error) {
	sub, err := Sub(data)
	if err != nil {
		return nil, err
	}
	if sub != SubHit {
		return nil, fmt.Errorf("enemysync: sub %d is not HIT", sub)
	}
	if len(data) != HitLen {
		return nil, fmt.Errorf("enemysync: %d-byte HIT (want %d)", len(data), HitLen)
	}
	h := &Hit{
		Client:      data[network.EnemyClientIDOff],
		Collider:    data[3],
		Epoch:       binary.BigEndian.Uint32(data[4:]),
		Stage:       getStage(data[8:16]),
		Room:        int8(data[16]),
		Atp:         data[17],
		SetID:       binary.BigEndian.Uint16(data[18:]),
		Handle:      binary.BigEndian.Uint16(data[52:]),
		HitSeq:      binary.BigEndian.Uint16(data[20:]),
		AtType:      binary.BigEndian.Uint32(data[24:]),
		ClientFrame: binary.BigEndian.Uint32(data[40:]),
		Se:          data[22],
		HitMark:     data[23],
		Spl:         data[44],
		AtSPrm:      data[45],
		CutType:     data[46],
		CutCount:    data[47],
		PlayerID:    data[network.EnemyHitPlayerIDOff],
	}
	for i := range 3 {
		h.Pos[i] = math.Float32frombits(binary.BigEndian.Uint32(data[28+4*i:]))
	}
	if err := finite(h.Pos[0], h.Pos[1], h.Pos[2]); err != nil {
		return nil, err
	}
	return h, nil
}

// EncodeAck builds an ACK payload (AckLen bytes).
func EncodeAck(a *Ack) []byte {
	b := make([]byte, AckLen)
	b[0], b[1], b[network.EnemyClientIDOff], b[3] = SubAck, WireVersion, a.Client, a.Result
	binary.BigEndian.PutUint32(b[4:], a.Epoch)
	binary.BigEndian.PutUint16(b[8:], a.SetID)
	binary.BigEndian.PutUint16(b[10:], a.HitSeq)
	b[12] = byte(a.Health)
	b[13], b[14], b[15] = a.Detail, a.Frames, a.Sets
	binary.BigEndian.PutUint16(b[16:], a.Handle)
	return b
}

// DecodeAck parses an ACK payload.
func DecodeAck(data []byte) (*Ack, error) {
	sub, err := Sub(data)
	if err != nil {
		return nil, err
	}
	if sub != SubAck {
		return nil, fmt.Errorf("enemysync: sub %d is not ACK", sub)
	}
	if len(data) != AckLen {
		return nil, fmt.Errorf("enemysync: %d-byte ACK (want %d)", len(data), AckLen)
	}
	return &Ack{
		Client: data[network.EnemyClientIDOff],
		Result: data[3],
		Epoch:  binary.BigEndian.Uint32(data[4:]),
		SetID:  binary.BigEndian.Uint16(data[8:]),
		Handle: binary.BigEndian.Uint16(data[16:]),
		HitSeq: binary.BigEndian.Uint16(data[10:]),
		Health: int8(data[12]),
		Detail: data[13],
		Frames: data[14],
		Sets:   data[15],
	}, nil
}

// --- remote Link (wire version 3) --------------------------------------------

// PSTATE flags (wire byte [3]; mailbox.RemotePS*). They are not the WWRP
// slot's sflags: the host translates them (players.go slotFlags).
const (
	PSGuard      = mailbox.RemotePSGuard      // checkPlayerGuard (d_a_player_main.cpp:1038-1040)
	PSCrouchDef  = mailbox.RemotePSCrouchDef  // mCurProc CROUCH_DEFENSE or its slip
	PSDamageMode = mailbox.RemotePSDamageMode // mModeFlg DAMAGE
	PSDead       = mailbox.RemotePSDead       // life 0 or the death demo
	PSInEvent    = mailbox.RemotePSInEvent    // an event runs (mEvtCtrl.mMode != 0) or a player demo
	PSSwim       = mailbox.RemotePSSwim       // mModeFlg SWIM
	PSOnShip     = mailbox.RemotePSOnShip     // mModeFlg IN_SHIP
	PSLoading    = mailbox.RemotePSLoading    // not in play, or a stage change is pending
)

// PHIT flags (wire byte [3]; the apply record's mailbox.RemoteAF*).
const (
	PHitHostShield = mailbox.RemoteAFHostShield // the host saw a shield hit on the body
	PHitHostGuard  = mailbox.RemoteAFHostGuard  // the host's guard flag for this remote
)

// PACK results and details (mailbox.RemoteAR*, RemoteAD*).
const (
	PAckNone       = mailbox.RemoteARNone
	PAckDropped    = mailbox.RemoteARDropped
	PAckApplied    = mailbox.RemoteARApplied
	PAckGuarded    = mailbox.RemoteARGuarded
	PAckNoCross    = mailbox.RemoteARNoCross
	PAckNoMirror   = mailbox.RemoteARNoMirror
	PAckLinkAbsent = mailbox.RemoteARLinkAbsent
	PAckEarlyOut   = mailbox.RemoteAREarlyOut

	PADNone       = mailbox.RemoteADNone
	PADIFrames    = mailbox.RemoteADIFrames
	PADGoGate     = mailbox.RemoteADGoGate
	PADNotInPlay  = mailbox.RemoteADNotInPlay
	PADStaleEpoch = mailbox.RemoteADStaleEpoch
	PADAborted    = mailbox.RemoteADAborted
)

// PAckResultName and PAckDetailName name a PACK's result and detail for
// logs.
func PAckResultName(r uint8) string {
	names := [...]string{"none", "dropped", "applied", "guarded", "no_cross", "no_mirror", "link_absent", "early_out"}
	if int(r) < len(names) {
		return names[r]
	}
	return fmt.Sprintf("result_%d", r)
}

func PAckDetailName(d uint8) string {
	names := [...]string{"none", "iframes", "go_gate", "not_in_play", "stale_epoch", "aborted"}
	if int(d) < len(names) {
		return names[d]
	}
	return fmt.Sprintf("detail_%d", d)
}

// PState is a PSTATE payload: the client Link's scope and combat state,
// which the host writes into that player's WWRP slot
// (remote-link-design.md §8.1). Client and PlayerID are the server's
// stamps (a client sends 0). Epoch is the host epoch of the client's
// latest STATE (0 before one): diagnostic, the host does not gate on it.
type PState struct {
	Client       uint8
	Flags        uint8 // PS*
	Epoch        uint32
	Stage        string
	Room         int8
	CutType      uint8 // Link +0x290
	CutCount     uint8 // Link +0x291
	PlayerID     uint8
	IFrames      int16 // Link mDamageWaitTimer (+0x294)
	ShieldAngleY int16 // Link mShieldFrontRangeYAngle (+0x3546)
}

// PHit is a PHIT payload: an enemy's At crossed the remote body of the
// player whose enemy connection is Client, on the host (§8.2). Seq counts
// per target client; the attacker fields are the host REL's hit ring
// entry's (mailbox.RemoteHit). Version 4: SetID carries the attacker's
// handle (NoMirrorHandle when the host lists no such enemy); the client
// translates it to an owner id before it reaches the REL.
type PHit struct {
	Client    uint8
	Flags     uint8 // PHit*
	Epoch     uint32
	Stage     string
	Room      int8
	Atp       uint8
	SetID     uint16
	Seq       uint16
	Se        uint8
	HitMark   uint8
	AtType    uint32
	Spl       uint8
	RelHit    [3]float32 // Tg HitPos - body pos
	RVec      [3]float32 // Tg RVec
	HostFrame uint32     // the host REL's frame at detection
}

// PAck is a PACK payload: what the client did with a PHIT (§8.3). Client
// is the server's stamp; Seq is the PHIT's.
type PAck struct {
	Client      uint8
	Result      uint8 // PAck*
	Seq         uint16
	Detail      uint8 // PAD*
	LifeBefore  uint8 // quarter hearts, clamped to 255
	ClientFrame uint32
	LifeDelta   int16 // life after - life before, quarter hearts
}

// EncodePState builds a PSTATE payload (PStateLen bytes). It refuses a
// stage name over StageLen bytes.
func EncodePState(p *PState) ([]byte, error) {
	b := make([]byte, PStateLen)
	b[0], b[1], b[network.EnemyClientIDOff], b[3] = SubPState, WireVersion, p.Client, p.Flags
	binary.BigEndian.PutUint32(b[4:], p.Epoch)
	if err := putStage(b[8:16], p.Stage); err != nil {
		return nil, err
	}
	b[16], b[17], b[18], b[network.EnemyPStatePlayerIDOff] = byte(p.Room), p.CutType, p.CutCount, p.PlayerID
	binary.BigEndian.PutUint16(b[20:], uint16(p.IFrames))
	binary.BigEndian.PutUint16(b[22:], uint16(p.ShieldAngleY))
	return b, nil
}

// DecodePState parses a PSTATE payload.
func DecodePState(data []byte) (*PState, error) {
	if err := wantSub(data, SubPState, "PSTATE", PStateLen); err != nil {
		return nil, err
	}
	return &PState{
		Client:       data[network.EnemyClientIDOff],
		Flags:        data[3],
		Epoch:        binary.BigEndian.Uint32(data[4:]),
		Stage:        getStage(data[8:16]),
		Room:         int8(data[16]),
		CutType:      data[17],
		CutCount:     data[18],
		PlayerID:     data[network.EnemyPStatePlayerIDOff],
		IFrames:      int16(binary.BigEndian.Uint16(data[20:])),
		ShieldAngleY: int16(binary.BigEndian.Uint16(data[22:])),
	}, nil
}

// EncodePHit builds a PHIT payload (PHitLen bytes). It refuses a NaN or
// Inf and a stage name over StageLen bytes.
func EncodePHit(p *PHit) ([]byte, error) {
	if err := finite(p.RelHit[0], p.RelHit[1], p.RelHit[2], p.RVec[0], p.RVec[1], p.RVec[2]); err != nil {
		return nil, err
	}
	b := make([]byte, PHitLen)
	b[0], b[1], b[network.EnemyClientIDOff], b[3] = SubPHit, WireVersion, p.Client, p.Flags
	binary.BigEndian.PutUint32(b[4:], p.Epoch)
	if err := putStage(b[8:16], p.Stage); err != nil {
		return nil, err
	}
	b[16], b[17] = byte(p.Room), p.Atp
	binary.BigEndian.PutUint16(b[18:], p.SetID)
	binary.BigEndian.PutUint16(b[20:], p.Seq)
	b[22], b[23] = p.Se, p.HitMark
	binary.BigEndian.PutUint32(b[24:], p.AtType)
	b[28] = p.Spl
	for i := range 3 {
		binary.BigEndian.PutUint32(b[32+4*i:], math.Float32bits(p.RelHit[i]))
		binary.BigEndian.PutUint32(b[44+4*i:], math.Float32bits(p.RVec[i]))
	}
	binary.BigEndian.PutUint32(b[56:], p.HostFrame)
	return b, nil
}

// DecodePHit parses a PHIT payload; it refuses a NaN or Inf.
func DecodePHit(data []byte) (*PHit, error) {
	if err := wantSub(data, SubPHit, "PHIT", PHitLen); err != nil {
		return nil, err
	}
	p := &PHit{
		Client:    data[network.EnemyClientIDOff],
		Flags:     data[3],
		Epoch:     binary.BigEndian.Uint32(data[4:]),
		Stage:     getStage(data[8:16]),
		Room:      int8(data[16]),
		Atp:       data[17],
		SetID:     binary.BigEndian.Uint16(data[18:]),
		Seq:       binary.BigEndian.Uint16(data[20:]),
		Se:        data[22],
		HitMark:   data[23],
		AtType:    binary.BigEndian.Uint32(data[24:]),
		Spl:       data[28],
		HostFrame: binary.BigEndian.Uint32(data[56:]),
	}
	for i := range 3 {
		p.RelHit[i] = math.Float32frombits(binary.BigEndian.Uint32(data[32+4*i:]))
		p.RVec[i] = math.Float32frombits(binary.BigEndian.Uint32(data[44+4*i:]))
	}
	if err := finite(p.RelHit[0], p.RelHit[1], p.RelHit[2], p.RVec[0], p.RVec[1], p.RVec[2]); err != nil {
		return nil, err
	}
	return p, nil
}

// EncodePAck builds a PACK payload (PAckLen bytes).
func EncodePAck(a *PAck) []byte {
	b := make([]byte, PAckLen)
	b[0], b[1], b[network.EnemyClientIDOff], b[3] = SubPAck, WireVersion, a.Client, a.Result
	binary.BigEndian.PutUint16(b[4:], a.Seq)
	b[6], b[7] = a.Detail, a.LifeBefore
	binary.BigEndian.PutUint32(b[8:], a.ClientFrame)
	binary.BigEndian.PutUint16(b[12:], uint16(a.LifeDelta))
	return b
}

// DecodePAck parses a PACK payload.
func DecodePAck(data []byte) (*PAck, error) {
	if err := wantSub(data, SubPAck, "PACK", PAckLen); err != nil {
		return nil, err
	}
	return &PAck{
		Client:      data[network.EnemyClientIDOff],
		Result:      data[3],
		Seq:         binary.BigEndian.Uint16(data[4:]),
		Detail:      data[6],
		LifeBefore:  data[7],
		ClientFrame: binary.BigEndian.Uint32(data[8:]),
		LifeDelta:   int16(binary.BigEndian.Uint16(data[12:])),
	}, nil
}

// wantSub checks a fixed-size payload's version, sub and length.
func wantSub(data []byte, sub byte, name string, n int) error {
	got, err := Sub(data)
	if err != nil {
		return err
	}
	if got != sub {
		return fmt.Errorf("enemysync: sub %d is not %s", got, name)
	}
	if len(data) != n {
		return fmt.Errorf("enemysync: %d-byte %s (want %d)", len(data), name, n)
	}
	return nil
}
