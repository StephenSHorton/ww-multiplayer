package mailbox

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// wwenemyHeader is inject/include/wwenemy.h, which lands with the REL
// PR; until then the pin test skips.
const wwenemyHeader = "../../inject/include/wwenemy.h"

// headerDefines reads every `#define NAME value` of path the way
// scripts/extract_blob.py's source_define does (one per line, a decimal
// or hex number, an optional // comment).
func headerDefines(t *testing.T, path string) map[string]uint64 {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(path))
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not here yet (it lands with the REL PR); nothing to pin against", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^#define\s+(\w+)\s+(0x[0-9A-Fa-f]+|\d+)\s*(?://.*)?$`)
	out := map[string]uint64{}
	for _, m := range re.FindAllSubmatch(src, -1) {
		v, err := strconv.ParseUint(string(m[2]), 0, 32)
		if err != nil {
			t.Fatalf("%s: %s", m[1], err)
		}
		out[string(m[1])] = v
	}
	return out
}

// TestEnemyHeaderPins: every Go constant equals its WWENEMY_* define, and
// every define has a Go constant.
func TestEnemyHeaderPins(t *testing.T) {
	defs := headerDefines(t, wwenemyHeader)
	pins := map[string]uint64{
		"WWENEMY_MAGIC":             uint64(EnemyMagic),
		"WWENEMY_OFF":               uint64(EnemyOff),
		"WWENEMY_SIZE":              uint64(EnemySize),
		"WWENEMY_LAYOUT":            uint64(EnemyLayout),
		"WWENEMY_MAX_SLOTS":         EnemyMaxSlots,
		"WWENEMY_MAGIC_OFF":         EnemyMagicOff,
		"WWENEMY_SEQ_HEAD_OFF":      EnemySeqHeadOff,
		"WWENEMY_HOST_BEAT_OFF":     EnemyHostBeatOff,
		"WWENEMY_GO_WRITE_MS_OFF":   EnemyGoWriteMSOff,
		"WWENEMY_COUNT_OFF":         EnemyCountOff,
		"WWENEMY_FLAGS_OFF":         EnemyFlagsOff,
		"WWENEMY_LAYOUT_OFF":        EnemyLayoutOff,
		"WWENEMY_EPOCH_OFF":         EnemyEpochOff,
		"WWENEMY_STAGE_OFF":         EnemyStageOff,
		"WWENEMY_ROOM_OFF":          EnemyRoomOff,
		"WWENEMY_ROLE_OFF":          EnemyRoleOff,
		"WWENEMY_RESERVED_OFF":      EnemyReservedOff,
		"WWENEMY_SLOTS_OFF":         EnemySlotsOff,
		"WWENEMY_SLOT_SIZE":         EnemySlotSize,
		"WWENEMY_HIT_APPLY_OFF":     EnemyHitApplyOff,
		"WWENEMY_SEQ_TAIL_OFF":      EnemySeqTailOff,
		"WWENEMY_GO_SPAN":           EnemyGoSpan,
		"WWENEMY_FLAG_MIRROR":       uint64(EnemyFlagMirror),
		"WWENEMY_FLAG_COLLIDERS":    uint64(EnemyFlagColliders),
		"WWENEMY_FLAG_ANIM":         uint64(EnemyFlagAnim),
		"WWENEMY_FLAG_RAW":          uint64(EnemyFlagRaw),
		"WWENEMY_ROLE_HOST":         uint64(EnemyRoleHost),
		"WWENEMY_ROLE_CLIENT":       uint64(EnemyRoleClient),
		"WWENEMY_SL_PID_OFF":        EnemySLPIDOff,
		"WWENEMY_SL_POS_OFF":        EnemySLPosOff,
		"WWENEMY_SL_ANGLE_OFF":      EnemySLAngleOff,
		"WWENEMY_SL_TILT_OFF":       EnemySLTiltOff,
		"WWENEMY_SL_ACTION_OFF":     EnemySLActionOff,
		"WWENEMY_SL_MODE_OFF":       EnemySLModeOff,
		"WWENEMY_SL_ATTACK_OFF":     EnemySLAttackOff,
		"WWENEMY_SL_PHASE_OFF":      EnemySLPhaseOff,
		"WWENEMY_SL_HEALTH_OFF":     EnemySLHealthOff,
		"WWENEMY_SL_FLAGS_OFF":      EnemySLFlagsOff,
		"WWENEMY_SL_FRAME_OFF":      EnemySLFrameOff,
		"WWENEMY_SL_RATE_OFF":       EnemySLRateOff,
		"WWENEMY_SL_SAMPLE_OFF":     EnemySLSampleOff,
		"WWENEMY_SF_SNAP":           uint64(EnemySFSnap),
		"WWENEMY_SF_DYING":          uint64(EnemySFDying),
		"WWENEMY_SF_INVULN":         uint64(EnemySFInvuln),
		"WWENEMY_SF_DEFENCE":        uint64(EnemySFDefence),
		"WWENEMY_SF_RAGDOLL":        uint64(EnemySFRagdoll),
		"WWENEMY_SF_BARE_HANDED":    uint64(EnemySFBareHanded),
		"WWENEMY_SF_HIDDEN":         uint64(EnemySFHidden),
		"WWENEMY_SF_RELEASE":        uint64(EnemySFRelease),
		"WWENEMY_HA_SEQ_OFF":        EnemyHASeqOff,
		"WWENEMY_HA_SETID_OFF":      EnemyHASetIDOff,
		"WWENEMY_HA_COLLIDER_OFF":   EnemyHAColliderOff,
		"WWENEMY_HA_ATTYPE_OFF":     EnemyHAAtTypeOff,
		"WWENEMY_HA_ATP_OFF":        EnemyHAAtpOff,
		"WWENEMY_HA_POS_OFF":        EnemyHAPosOff,
		"WWENEMY_HA_SIZE":           EnemyHASize,
		"WWENEMY_ACK_SEQ_OFF":       EnemyAckSeqOff,
		"WWENEMY_ACK_FRAME_OFF":     EnemyAckFrameOff,
		"WWENEMY_ADOPTED_OFF":       EnemyAdoptedOff,
		"WWENEMY_EPOCH_SEEN_OFF":    EnemyEpochSeenOff,
		"WWENEMY_STATUS_OFF":        EnemyStatusOff,
		"WWENEMY_STATUS_SIZE":       EnemyStatusSize,
		"WWENEMY_HITS_OFF":          EnemyHitsOff,
		"WWENEMY_HIT_SIZE":          EnemyHitSize,
		"WWENEMY_HIT_APPLY_ACK_OFF": EnemyHitApplyAckOff,
		"WWENEMY_CANARIES_OFF":      EnemyCanariesOff,
		"WWENEMY_RESERVED2_OFF":     EnemyReserved2Off,
		"WWENEMY_ST_ACTOR_OFF":      EnemySTActorOff,
		"WWENEMY_ST_PID_OFF":        EnemySTPIDOff,
		"WWENEMY_ST_STATE_OFF":      EnemySTStateOff,
		"WWENEMY_ST_PAUSE_PREV_OFF": EnemySTPausePrevOff,
		"WWENEMY_ST_LAST_ERR_OFF":   EnemySTLastErrOff,
		"WWENEMY_ST_FRAMES_OFF":     EnemySTFramesOff,
		"WWENEMY_STATE_UNRESOLVED":  uint64(EnemyStateUnresolved),
		"WWENEMY_STATE_MIRRORED":    uint64(EnemyStateMirrored),
		"WWENEMY_STATE_RELEASED":    uint64(EnemyStateReleased),
		"WWENEMY_STATE_MISSING":     uint64(EnemyStateMissing),
		"WWENEMY_STATE_DYING":       uint64(EnemyStateDying),
		"WWENEMY_STATE_REFUSED":     uint64(EnemyStateRefused),
		"WWENEMY_ERR_NONE":          uint64(EnemyErrNone),
		"WWENEMY_ERR_PID":           uint64(EnemyErrPID),
		"WWENEMY_ERR_PROC":          uint64(EnemyErrProc),
		"WWENEMY_ERR_INIT":          uint64(EnemyErrInit),
		"WWENEMY_ERR_SETID":         uint64(EnemyErrSetID),
		"WWENEMY_ERR_MORF":          uint64(EnemyErrMorf),
		"WWENEMY_ERR_RANGE":         uint64(EnemyErrRange),
		"WWENEMY_ERR_MODE":          uint64(EnemyErrMode),
		"WWENEMY_HR_DROPPED":        uint64(EnemyHRDropped),
		"WWENEMY_HR_APPLIED_AT":     uint64(EnemyHRAppliedAt),
		"WWENEMY_HR_NO_ACTOR":       uint64(EnemyHRNoActor),
		"WWENEMY_HR_SHRINK":         uint64(EnemyHRShrink),
		"WWENEMY_STALE_FRAMES":      EnemyStaleFrames,
	}
	for name, want := range defs {
		got, ok := pins[name]
		if !ok {
			t.Errorf("%s = 0x%X in wwenemy.h has no Go constant", name, want)
			continue
		}
		if got != want {
			t.Errorf("%s: Go 0x%X, wwenemy.h 0x%X", name, got, want)
		}
	}
	for name := range pins {
		if _, ok := defs[name]; !ok {
			t.Errorf("Go pins %s, which wwenemy.h does not define", name)
		}
	}
}

// TestEnemyBlockPlacement: the block lies in the slack between the
// Mailbox struct and the patched arena, clear of the REL words, and its
// layout adds up: the REL span starts where the Go span ends, the slots
// and status entries fit, the magic spells 'WWEN', and the canary regions
// are the slack less the block.
func TestEnemyBlockPlacement(t *testing.T) {
	slack := addr.Region{Name: "slack", Start: Mailbox + MailboxSize, End: PatchedArenaLo}
	if !slack.Contains(EnemyBlockAddr, int(EnemySize)) {
		t.Errorf("enemy block 0x%08X+0x%X is not inside the slack 0x%08X-0x%08X", EnemyBlockAddr, EnemySize, slack.Start, slack.End)
	}
	relStart, relEnd := Mailbox+OffRELMagic, Mailbox+OffRELFrame+4
	if EnemyBlockAddr < relEnd && relStart < EnemyBlockAddr+EnemySize {
		t.Errorf("enemy block 0x%08X-0x%08X overlaps the REL words 0x%08X-0x%08X", EnemyBlockAddr, EnemyBlockAddr+EnemySize, relStart, relEnd)
	}
	if EnemyBlockAddr != 0x80413100 || EnemyBlockAddr+EnemySize != 0x80413260 {
		t.Errorf("enemy block at 0x%08X-0x%08X, the design says 0x80413100-0x80413260", EnemyBlockAddr, EnemyBlockAddr+EnemySize)
	}
	if EnemyBlockRegion != (addr.Region{Name: "enemy block", Start: EnemyBlockAddr, End: EnemyBlockAddr + EnemySize}) {
		t.Errorf("EnemyBlockRegion %+v", EnemyBlockRegion)
	}
	if CanaryArena.End != EnemyBlockRegion.Start || CanaryArenaHigh.Start != EnemyBlockRegion.End {
		t.Error("the canary regions do not stop at the enemy block")
	}
	if !CanaryArena.Contains(relStart, int(relEnd-relStart)) {
		t.Error("the REL words left the low canary region")
	}
	if string(binary.BigEndian.AppendUint32(nil, EnemyMagic)) != "WWEN" {
		t.Errorf("EnemyMagic 0x%08X is not 'WWEN'", EnemyMagic)
	}
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"slots end", EnemySlotsOff + EnemyMaxSlots*EnemySlotSize, EnemyHitApplyOff},
		{"hit_apply end", EnemyHitApplyOff + EnemyHASize, EnemyHitApplyOff + 0x1C},
		{"seq_tail", EnemySeqTailOff + 4, EnemyGoSpan},
		{"REL span start", EnemyAckSeqOff, EnemyGoSpan},
		{"status end", EnemyStatusOff + EnemyMaxSlots*EnemyStatusSize, EnemyHitsOff},
		{"hits end", EnemyHitsOff + EnemyMaxSlots*EnemyHitSize, EnemyHitApplyAckOff},
		{"reserved2 end", EnemyReserved2Off + 4, int(EnemySize)},
		{"slot sample end", EnemySLSampleOff + 4, EnemySlotSize},
		{"status frames end", EnemySTFramesOff + 4, EnemyStatusSize},
	} {
		if c.got != c.want {
			t.Errorf("%s: 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if EnemyHitApplyOff+EnemyHASize > EnemySeqTailOff {
		t.Error("hit_apply runs into seq_tail")
	}
	if EnemySlotAddr(3, EnemySLSampleOff)+4 != EnemyBlockAddr+EnemyHitApplyOff {
		t.Errorf("EnemySlotAddr(3, sample) = 0x%08X", EnemySlotAddr(3, EnemySLSampleOff))
	}
}

func sampleEnemyBlock() *EnemyBlock {
	return &EnemyBlock{
		Magic: EnemyMagic, Seq: 0x01020304, HostBeat: 777, GoWriteMS: 123456, Flags: EnemyFlagMirror | EnemyFlagRaw,
		Layout: EnemyLayout, Epoch: 3, Stage: "M_Dai", Room: 1, Role: EnemyRoleClient,
		Slots: []EnemySlot{
			{PID: 0x1234, Pos: [3]float32{-100.5, 20, 3e4}, AngleY: -0x4000, Tilt: 2, Action: 20, Mode: -100,
				AttackType: 1, AttackPhase: 2, Health: 3, Flags: EnemySFSnap | EnemySFDying, MorfFrame: 7.5, MorfRate: 1, SampleSeq: 99},
			{PID: 0, Pos: [3]float32{1, 2, 3}},
		},
		HitApply: EnemyHitApply{Seq: 5, SetID: 2, Collider: 1, AtType: 0x80000, Atp: 2, Pos: [3]float32{4, 5, 6}},
	}
}

// TestEnemyBlockEncode: Encode is EnemyGoSpan bytes with seq_head ==
// seq_tail, every field at its header offset, and DecodeEnemyBlock reads
// it back; the encoder refuses NaN, too many slots and a long stage.
func TestEnemyBlockEncode(t *testing.T) {
	b := sampleEnemyBlock()
	out, err := b.Encode()
	if err != nil {
		t.Fatal(err)
	}
	be := binary.BigEndian
	if len(out) != EnemyGoSpan || be.Uint32(out[EnemySeqHeadOff:]) != b.Seq || be.Uint32(out[EnemySeqTailOff:]) != b.Seq {
		t.Fatalf("%d bytes, head %08X tail %08X", len(out), be.Uint32(out[EnemySeqHeadOff:]), be.Uint32(out[EnemySeqTailOff:]))
	}
	if be.Uint32(out[EnemyMagicOff:]) != EnemyMagic || out[EnemyCountOff] != 2 || out[EnemyFlagsOff] != b.Flags ||
		be.Uint16(out[EnemyLayoutOff:]) != 1 || be.Uint32(out[EnemyEpochOff:]) != 3 ||
		!bytes.Equal(out[EnemyStageOff:EnemyStageOff+8], []byte("M_Dai\x00\x00\x00")) ||
		out[EnemyRoomOff] != 1 || out[EnemyRoleOff] != EnemyRoleClient {
		t.Errorf("header %x", out[:EnemySlotsOff])
	}
	s0 := out[EnemySlotsOff:]
	if be.Uint32(s0[EnemySLPIDOff:]) != 0x1234 || math.Float32frombits(be.Uint32(s0[EnemySLPosOff+8:])) != 3e4 ||
		int16(be.Uint16(s0[EnemySLAngleOff:])) != -0x4000 || int16(be.Uint16(s0[EnemySLModeOff:])) != -100 ||
		s0[EnemySLFlagsOff] != EnemySFSnap|EnemySFDying || be.Uint32(s0[EnemySLSampleOff:]) != 99 {
		t.Errorf("slot 0 %x", s0[:EnemySlotSize])
	}
	ha := out[EnemyHitApplyOff:]
	if be.Uint32(ha[EnemyHASeqOff:]) != 5 || be.Uint16(ha[EnemyHASetIDOff:]) != 2 || ha[EnemyHAColliderOff] != 1 ||
		be.Uint32(ha[EnemyHAAtTypeOff:]) != 0x80000 || ha[EnemyHAAtpOff] != 2 || math.Float32frombits(be.Uint32(ha[EnemyHAPosOff+4:])) != 5 {
		t.Errorf("hit_apply %x", ha[:EnemyHASize])
	}
	got, tail, err := DecodeEnemyBlock(out)
	if err != nil || tail != b.Seq || !reflect.DeepEqual(&got, b) {
		t.Errorf("decode: tail %08X, %v\n got %+v\nwant %+v", tail, err, got, *b)
	}

	b.Slots[1].Pos[0] = float32(math.NaN())
	if _, err := b.Encode(); !errors.Is(err, ErrEnemyNotFinite) {
		t.Errorf("NaN: %v", err)
	}
	b = sampleEnemyBlock()
	b.Slots = make([]EnemySlot, 5)
	if _, err := b.Encode(); err == nil {
		t.Error("5 slots encoded")
	}
	b = sampleEnemyBlock()
	b.Stage = "123456789"
	if _, err := b.Encode(); err == nil {
		t.Error("a 9-byte stage encoded")
	}
	if _, _, err := DecodeEnemyBlock(out[:EnemyGoSpan-1]); err == nil {
		t.Error("a short block decoded")
	}
}

// TestEnemyBlockWriteReadStatus: WriteEnemyBlock is one write of the Go
// span at the block; ClearEnemyBlock writes the magic word alone;
// ReadEnemyStatus decodes what a REL would leave in the tail.
func TestEnemyBlockWriteReadStatus(t *testing.T) {
	m := &fakeWords{}
	if err := WriteEnemyBlock(m, sampleEnemyBlock()); err != nil {
		t.Fatal(err)
	}
	blk, tail, st, err := ReadEnemyBlock(m)
	if err != nil || blk.Magic != EnemyMagic || tail != 0x01020304 || st != (EnemyStatus{}) {
		t.Fatalf("read back: %+v tail %08X status %+v %v", blk, tail, st, err)
	}
	// The REL's tail.
	tailBytes := make([]byte, EnemySize-EnemyGoSpan)
	be := binary.BigEndian
	at := func(off int) []byte { return tailBytes[off-EnemyGoSpan:] }
	be.PutUint32(at(EnemyAckSeqOff), 0x01020304)
	be.PutUint32(at(EnemyAckFrameOff), 4242)
	be.PutUint32(at(EnemyAdoptedOff), 0b0101)
	be.PutUint32(at(EnemyEpochSeenOff), 3)
	st2 := at(EnemyStatusOff + 2*EnemyStatusSize)
	be.PutUint32(st2[EnemySTActorOff:], 0x80B00000)
	be.PutUint32(st2[EnemySTPIDOff:], 0x1234)
	st2[EnemySTStateOff] = EnemyStateMirrored
	st2[EnemySTPausePrevOff] = 0x02
	st2[EnemySTLastErrOff] = EnemyErrNone
	be.PutUint16(st2[EnemySTFramesOff:], 300)
	h1 := at(EnemyHitsOff + 1*EnemyHitSize)
	be.PutUint16(h1, 7)
	h1[2], h1[3] = 2, 1
	be.PutUint32(at(EnemyHitApplyAckOff), 5)
	copy(at(EnemyCanariesOff), []byte{1, 2, 3, 4})
	be.PutUint32(at(EnemyReserved2Off), uint32(EnemyHRShrink))
	if err := m.Write(EnemyBlockAddr+EnemyGoSpan, tailBytes); err != nil {
		t.Fatal(err)
	}
	st, err = ReadEnemyStatus(m)
	if err != nil {
		t.Fatal(err)
	}
	want := EnemyStatus{AckSeq: 0x01020304, AckFrame: 4242, AdoptedMask: 5, EpochSeen: 3, HitApplyAck: 5,
		Canaries: EnemyCanaries{1, 2, 3, 4}, LastResult: EnemyHRShrink}
	want.Status[2] = EnemySlotStatus{Actor: 0x80B00000, PID: 0x1234, State: EnemyStateMirrored, PausePrev: 2, Frames: 300}
	want.Hits[1] = EnemyHitEntry{HitSeq: 7, Collider: 2, Pending: 1}
	if st != want {
		t.Errorf("status\n got %+v\nwant %+v", st, want)
	}
	if _, _, st3, err := ReadEnemyBlock(m); err != nil || st3 != want {
		t.Errorf("ReadEnemyBlock status %+v %v", st3, err)
	}

	if err := ClearEnemyBlock(m); err != nil {
		t.Fatal(err)
	}
	blk, tail, _, err = ReadEnemyBlock(m)
	if err != nil || blk.Magic != 0 || blk.Seq != 0x01020304 || tail != 0x01020304 || blk.Role != EnemyRoleClient {
		t.Errorf("after clear: %+v tail %08X %v", blk, tail, err)
	}
	if _, err := DecodeEnemyStatus(tailBytes[:len(tailBytes)-1]); err == nil {
		t.Error("a short status decoded")
	}
}
