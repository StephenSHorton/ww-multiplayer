// Package enemysync is the host-authoritative enemy mirror (the Moblin
// prototype, ww-multiplayer-handoff/design/host-authoritative-prototype.md
// and moblin-mirror-design.json): the host's Go samples its keyed Moblins
// and streams them as MsgEnemy STATE frames on a RoleEnemyHost
// connection; each joiner's Go matches them to its own Moblins, smooths
// the samples and writes one block per tick into the mailbox slack
// (wwapi/mailbox/enemy.go) for the REL module to mirror.
//
// This file is the wire format: the STATE, HIT and ACK payloads of a
// MsgEnemy frame ([type 'E'][len:2 BE][payload], payload =
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
)

// WireVersion is the ver byte every payload carries.
const WireVersion byte = 1

// The sub bytes and sizes, from the server's ingress rules.
const (
	SubState = network.EnemySubState
	SubHit   = network.EnemySubHit
	SubAck   = network.EnemySubAck

	StateHeaderLen = network.EnemyStateHeaderLen
	SlotLen        = network.EnemySlotLen
	MaxSlots       = network.EnemyMaxSlots
	HitLen         = network.EnemyHitLen
	AckLen         = network.EnemyAckLen

	// StageLen is the stage name field: char[8], NUL-padded, as
	// dStage_startStage_c::mName (d_stage.h:949).
	StageLen = 8
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

// ACK results.
const (
	AckDropped   uint8 = 0 // invulnerable, defending, ragdoll or stale: nothing applied
	AckAppliedAt uint8 = 1 // applied through an At sphere (v2)
	AckNoActor   uint8 = 2 // no such Moblin on the host
	AckShrink    uint8 = 3 // applied as the light-shrink kill (v1)
)

// Slot is one Moblin in a STATE.
type Slot struct {
	SetID       uint16
	Proc        uint16 // 0x00BD (fpcNm_MO2_e)
	Pos         [3]float32
	AngleY      int16 // current.angle.y (+0x206)
	Tilt        int16 // damagereaction m48C.x (+0xAA0)
	Action      int16 // damagereaction mAction (+0x61A)
	Mode        int16 // damagereaction mMode (+0x618)
	AttackType  uint8 // m2060
	AttackPhase uint8 // m2064
	MorfFrame   float32
	MorfRate    float32
	Health      int8
	Flags       uint8
}

// State is a STATE payload: the host's keyed Moblins in its room, or a
// count-0 keepalive.
type State struct {
	Flags    uint8
	Epoch    uint32
	Seq      uint32
	HostBeat uint32 // the host's spawn_trigger at sample time
	HostMS   uint32 // the host's monotonic ms, sender-relative (the smoother's clock)
	Stage    string // at most StageLen bytes
	Room     int8
	DeadMask uint8 // bit n: keyed setID n's dead-actor bit is set in this room's zone
	Slots    []Slot
}

// Hit is a HIT payload: a client's sword or arrow landed on a mirrored
// copy. Client is the enemy client's connection ID, which the server
// stamps on the way to the host; a client sends 0.
type Hit struct {
	Client      uint8
	Collider    uint8
	Epoch       uint32
	Stage       string
	Room        int8
	Atp         uint8
	SetID       uint16
	HitSeq      uint16
	AtType      uint32
	Pos         [3]float32
	ClientFrame uint32
}

// Ack is an ACK payload: what the host did with a HIT. Client is the HIT's
// stamped connection ID, which the server routes the ACK by.
type Ack struct {
	Client uint8
	Result uint8
	Epoch  uint32
	SetID  uint16
	HitSeq uint16
	Health int8
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

// EncodeState builds a STATE payload. It refuses more than MaxSlots
// slots, a stage name over StageLen bytes and a NaN or Inf anywhere.
func EncodeState(s *State) ([]byte, error) {
	if len(s.Slots) > MaxSlots {
		return nil, fmt.Errorf("enemysync: %d slots (max %d)", len(s.Slots), MaxSlots)
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
	b[28], b[29] = byte(s.Room), s.DeadMask
	for i := range s.Slots {
		if err := encodeSlot(b[StateHeaderLen+SlotLen*i:], &s.Slots[i]); err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
	}
	return b, nil
}

func encodeSlot(b []byte, sl *Slot) error {
	if err := finite(sl.Pos[0], sl.Pos[1], sl.Pos[2], sl.MorfFrame, sl.MorfRate); err != nil {
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
	if count > MaxSlots {
		return nil, fmt.Errorf("enemysync: STATE with %d slots (max %d)", count, MaxSlots)
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
	return finite(sl.Pos[0], sl.Pos[1], sl.Pos[2], sl.MorfFrame, sl.MorfRate)
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
	binary.BigEndian.PutUint32(b[24:], h.AtType)
	for i := range 3 {
		binary.BigEndian.PutUint32(b[28+4*i:], math.Float32bits(h.Pos[i]))
	}
	binary.BigEndian.PutUint32(b[40:], h.ClientFrame)
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
		HitSeq:      binary.BigEndian.Uint16(data[20:]),
		AtType:      binary.BigEndian.Uint32(data[24:]),
		ClientFrame: binary.BigEndian.Uint32(data[40:]),
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
		HitSeq: binary.BigEndian.Uint16(data[10:]),
		Health: int8(data[12]),
	}, nil
}
