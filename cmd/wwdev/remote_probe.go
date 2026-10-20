package main

// remote-watch, remote-flags and remote-slot: the probes of the
// remote-Link design's live checks (ww-multiplayer-handoff/design/
// remote-link-design.md, section 10, Steps 0-3) over the WWRP block
// (wwremote.h, wwapi/mailbox/remote.go) and the REL's exec-hook words
// (wwrel.h +0x1BC/+0x1C0).
//
// remote-watch reads ONE Dolphin every 16 ms and writes nothing. Every
// second it prints the REL words (linked, build, frames, exec_hook,
// swap_state and whether swap_state - WWREMOTE_KEEPER_SWAP_OFF is a 0xB5
// actor), the block's validity (magic, layout, seq_head against seq_tail,
// the ack's age against WWREMOTE_STALE_FRAMES), the flags by name with the
// two gates spelled out (bits 0x100/0x200 are *_GATE_OFF: a clear bit
// means the gate runs), the jail byte (*(u8*)0x803C4CBC & 0x0F; 0 means
// the rouya gate skips every mo2 swap, which is why swaps stays 0 on a
// save without pearls), mPlayerInfo[0].mpPlayer against mpPlayerPtr[0]
// (equal outside a swapped execute), the counters with their deltas and
// hook_calls per REL frame (Step 0: a few hundred), each slot with its
// body status and the body read back (magic 'WWRB', slot/gen/state,
// vptr == body + 0x320 and vt+0x1C, pos and eyePos, the pose feed of its
// pose slot, since a body without a live pose feed parks), the keeper's
// swap state (its intake, body slots and latch table, as
// inject/rel/remote.h lays them out), and the client apply record and its
// result.
// The hit ring is read every tick and each new entry is printed at once
// (Step 3 counts entries within 30 frames), as are body state changes.
//
// Per Moblin in Link's room it reads m714 at mo2 + 0xD28 (mDamageReaction
// at d_a_mo2.h:88 + m714 at c_damagereaction.h:170, the pointer
// search_target refreshes from getPlayer(0), d_a_mo2.cpp:2847-2850,
// 2903-2911) and resolves it to host / body N / null / other; a change is
// printed at once. With every flag 0 each Moblin must say host: that is
// the live check of the +0xD28 offset the design marked (I) (section 5.5),
// and Step 2's "mo2+0xD28 == body" is the same column.
//
// remote-flags writes the flags word (+0x00A, 2 bytes) and nothing else.
// It does not make the block valid: with no writer bumping the seq the REL
// treats it stale after WWREMOTE_STALE_FRAMES frames, so a flags write
// matters only while remote-slot (or a host pipeline) holds the block.
//
// remote-slot holds the block for single-Dolphin tests: every 16 ms it
// writes the whole Go span with seq + 1 (role host, layout 1, the
// RemoteDef* tunables, PROC_POLICY ALLOW mo2 when it seeds the block), one
// slot set by hand (active, TARGETABLE, the host's stage and room, the
// host Link's position plus an offset so the body follows Link), and the
// flags word read back from the game each tick, so remote-flags can step
// BODIES -> SWAP -> HITTABLE|DETECT mid-run. On stop it writes magic 0.
//
// Stage 4 (remote-link-stage4-plan.md): a body's kind is body_status
// +0x0F. A lite body reads as before; a FULL body (the real daPy_lk_c,
// proc 0x27 profile) reads the full tail (magic, slot, gen, iframes,
// state, phase, create_frames, shanm_diff, heap_err) and the daPy fields
// (vptr == __vt__9daPy_lk_c, mCurProc, mDamageWaitTimer, mCyl's Tg, Co and
// GObjTg SPrm). Every body prints mProfName (+0x0E: 0xB5 lite, 0x27
// full). The summary adds the block's FULL_ALLOW / HEAP_REASON / FLAGS2,
// a profile line (DMC[0x27] == DMC[0xB5], the profile list's slot 0x27
// and the profile it points at, read live) and a shanm line (the archive
// table at WWREL_SHANM_OFF: header validity, words, create/delete diffs,
// delete scan, windows, joint restores).

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

const (
	remoteWatchUsage = "remote-watch [secs=10, 0 = until Ctrl+C] [--csv <path>] [--latch-raw]"
	remoteFlagsUsage = "remote-flags <hex | name,name,...> --yes   (names: " + "bodies swap hittable detect apply attrib name_a9 guard ff_gate_off rouya_gate_off attacker group_wide both_words_enable iframe_stamp full_bodies; 0 = all off)"
	remoteSlotUsage  = "remote-slot <slot 0-3> --yes [secs=60, 0 = until Ctrl+C] [--flags <hex|names>] [--sflags <hex>] [--offset dx,dy,dz] [--at x,y,z] [--room n] [--stage name] [--role host|client] [--pose-slot n] [--gen n] [--iframes n] [--eye-dy f] [--cyl-r f] [--cyl-h f]"
)

// mo2OffM714 is mDamageReaction.m714, the fopAc_ac_c* the Moblin chases
// and bears on: damagereaction at mo2 +0x614 (d_a_mo2.h:88), m714 at
// +0x714 of it (c_damagereaction.h:170). Local until a live run confirms
// it; it belongs in wwapi/addr/mo2.go next to Mo2OffDR.
const mo2OffM714 = addr.Mo2OffDR + 0x714

// The keeper's swap state, remote_swap_state in inject/rel/remote.h (the
// REL's private struct at WWREMOTE_KEEPER_SWAP_OFF, reached through
// WWREL_SWAP_STATE_OFF): its intake words, the runtime body slots
// (rl_body) and the latch table (rl_latch, design section 5.4). It is not
// part of the shared contract (wwremote.h has no defines for it), so
// TestSwapStatePins checks these against the "// +0xNN" offsets of
// remote.h's field comments.
const (
	swLastSeq     = 0x00 // u32 the block seq last consumed
	swStaleFrames = 0x04 // u32 keeper executes since
	swFlags       = 0x10 // u16 WWREMOTE_FLAG_* while the block is valid, else 0
	swValid       = 0x12 // u8
	swRole        = 0x13 // u8
	swSwapReady   = 0x14 // u8 the hook's fast-path byte
	swLiveMask    = 0x15 // u8 bit i: body i a latch candidate
	swFullAllow   = 0x19 // u8 FULL_ALLOW latched at consume (Stage 4)
	swFlags2      = 0x1A // u16 FLAGS2 of the last consume
	swFullDenied  = 0x79 // u8 bit i: slot i spent its full attempt this stage
	swFullWait    = 0x7A // u8[MAX_REMOTE_LINKS] frames slot i waited for its allow bit
	swBodies      = 0x28 // rl_body[MAX_REMOTE_LINKS]
	swBodySize    = 0x14
	swBodyGen     = 0x0B // u8
	swBodyCool    = 0x0C // u16 cooldown
	swBodyDelSent = 0x11 // u8
	swBodyParked  = 0x12 // u8
	swBodyKind    = 0x13 // u8 WWREMOTE_KIND_* of the last create
	swLatch       = 0x7C // rl_latch[RL_LATCH_MAX]
	swSize        = 0x17C
	latchEntry    = 0x10
	latchCount    = 16 // RL_LATCH_MAX
	latchEnemyPID = 0x00
	latchTarget   = 0x04 // u32 pid, 0 = host Link
	latchForcePID = 0x08
	latchHold     = 0x0C // u8
	latchForceFr  = 0x0D // u8
	latchIsMo2    = 0x0E // u8
	// swapStateLen is what the keeper can hold after the swap state's
	// start (WWREMOTE_ACTOR_SIZE - WWREMOTE_KEEPER_SWAP_OFF); the probe
	// reads all of it (--latch-raw prints it).
	swapStateLen = mailbox.RemoteActorSize - mailbox.RemoteKeeperSwapOff
)

// remoteFlagNames names the WWREMOTE_FLAG_* bits, lower case, in bit
// order; remote-flags parses them.
var remoteFlagNames = []struct {
	bit  uint16
	name string
}{
	{mailbox.RemoteFlagBodies, "bodies"},
	{mailbox.RemoteFlagSwap, "swap"},
	{mailbox.RemoteFlagHittable, "hittable"},
	{mailbox.RemoteFlagDetect, "detect"},
	{mailbox.RemoteFlagApply, "apply"},
	{mailbox.RemoteFlagAttrib, "attrib"},
	{mailbox.RemoteFlagNameA9, "name_a9"},
	{mailbox.RemoteFlagGuard, "guard"},
	{mailbox.RemoteFlagFFGateOff, "ff_gate_off"},
	{mailbox.RemoteFlagRouyaGateOff, "rouya_gate_off"},
	{mailbox.RemoteFlagAttacker, "attacker"},
	{mailbox.RemoteFlagGroupWide, "group_wide"},
	{mailbox.RemoteFlagBothWordsEnable, "both_words_enable"},
	{mailbox.RemoteFlagIFrameStamp, "iframe_stamp"},
	{mailbox.RemoteFlagFullBodies, "full_bodies"},
}

// remoteFlagsText is f as hex, its names and the two gates in words.
func remoteFlagsText(f uint16) string {
	var names []string
	rest := f
	for _, n := range remoteFlagNames {
		if f&n.bit != 0 {
			names = append(names, n.name)
			rest &^= n.bit
		}
	}
	if rest != 0 {
		names = append(names, fmt.Sprintf("unknown 0x%04X", rest))
	}
	if len(names) == 0 {
		names = []string{"none"}
	}
	gate := func(off uint16) string {
		if f&off != 0 {
			return "OFF"
		}
		return "on"
	}
	return fmt.Sprintf("0x%04X %s (ff gate %s, rouya gate %s)", f, strings.Join(names, "|"), gate(mailbox.RemoteFlagFFGateOff), gate(mailbox.RemoteFlagRouyaGateOff))
}

var hexRE = regexp.MustCompile(`^(0[xX])?[0-9a-fA-F]+$`)

// parseRemoteFlags reads a flags word as hex (with or without 0x) or as
// names joined by ',' '|' or '+'.
func parseRemoteFlags(s string) (uint16, error) {
	if hexRE.MatchString(s) {
		v, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"), 16, 16)
		if err != nil {
			return 0, fmt.Errorf("flags %q: %v", s, err)
		}
		return uint16(v), nil
	}
	var f uint16
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == '|' || r == '+' }) {
		i := slices.IndexFunc(remoteFlagNames, func(n struct {
			bit  uint16
			name string
		}) bool {
			return n.name == tok
		})
		if i < 0 {
			return 0, fmt.Errorf("flags %q: unknown flag %q", s, tok)
		}
		f |= remoteFlagNames[i].bit
	}
	return f, nil
}

// Names of the REL-written enums, for the lines.
var (
	remoteBodyStateNames = []string{"EMPTY", "QUEUED", "LIVE", "PARKED", "DYING"}
	remoteBErrNames      = []string{"none", "slot", "block", "vtable", "host_link", "cyl", "create", "lost", "tries", "gone",
		"heap_denied", "phase_timeout", "heap_errors", "profile", "shanm_alloc", "full_vtable"}
	remoteKindNames = []string{"lite", "FULL"}
	remoteFPNames   = []string{"NEW", "PARAMS", "INIT", "LIVE"}
	remoteHGNames   = []string{"none", "open", "no_sample", "walk_problem", "game_largest", "game_free", "zelda_largest",
		"counter_rose", "inflight", "stage_settle", "sticky", "archive_largest", "full_max", "queued"}
	remoteARNames = []string{"NONE", "DROPPED", "APPLIED", "GUARDED", "NO_CROSS", "NO_MIRROR", "LINK_ABSENT", "EARLY_OUT"}
	remoteADNames = []string{"none", "iframes", "go_gate", "not_in_play", "stale_epoch", "aborted"}
)

// enumName is names[v], or the number when out of range.
func enumName(names []string, v uint8) string {
	if int(v) < len(names) {
		return names[v]
	}
	return strconv.Itoa(int(v))
}

// The full body's daPy_lk_c fingerprint and mCyl fields (Step -1 dump,
// remote-link-design.md: Tg SPrm +0x18, Co SPrm +0x2C, GObjTg SPrm +0x94).
const (
	dapyLkVtable  uint32 = 0x8038FDC0 // __vt__9daPy_lk_c, SYM:15801
	actorOffProf         = 0x0E       // s16 mProfName (base_process_class)
	cylOffTgSPrm         = 0x18
	cylOffCoSPrm         = 0x2C
	cylOffGTgSPrm        = 0x94
	fullHeadLen          = mailbox.RemoteFullLinktexOff + 4 // proc, pos, eyePos, the daPy fields through mpCurrLinktex
	fullTailLen          = mailbox.RemoteFullEnd - mailbox.RemoteFullTailOff
	fullCylLen           = cylOffGTgSPrm + 4
)

// remoteBodyRead is one body actor as the probe read it back.
type remoteBodyRead struct {
	Read        bool   // the actor was in MEM1 and read
	Err         string // why not
	Kind        uint8  // body_status kind: which layout was read
	Proc        int16
	Prof        int16 // mProfName: 0xB5 lite, 0x27 full
	Magic       uint32
	Slot, Gen   uint8
	IFramesLeft uint8
	State       uint8
	VPtr, VT1C  uint32
	Pos, Eye    [3]float32
	LastPoseSeq uint32
	ParkSince   uint32
	// FULL bodies only.
	Phase, TailKind uint8
	CreateFrames    uint16
	ShanmDiff       uint32
	HeapErr         uint32
	Model, Linktex  uint32
	CurProc         uint32
	DamageWait      int16
	TgSPrm, CoSPrm  uint32
	GTgSPrm         uint32
	ShieldRange     int16
	ModeFlg, Reset0 uint32
}

// remoteShanmRead is the archive-heap shanm table's header (WWREL_SHANM_OFF).
type remoteShanmRead struct {
	Word                                                          uint32 // the slack word: 0 until the first FULL create of the boot
	Err                                                           string // the word is set but the header does not check out
	Words, CreateDiff, DeleteDiff, DeleteScan, Windows, JointDiff uint32
}

// remoteProfileRead is the second profile's wiring, read live: the DMC
// alias (DMC[0x27] == DMC[0xB5], the DOL name-table patch) and the
// profile list's slot 0x27 (the RELS.arc patch, applied by OSLink while
// module 0x58 is linked) with the profile it points at.
type remoteProfileRead struct {
	DMCFull, DMCREL uint32
	List            uint32 // g_fpcPf_ProfileList_p
	Slot27, SlotB5  uint32
	ProcName        int16 // of the slot-0x27 profile
	Size            uint32
	Err             string
}

// remoteSnap is one tick's read of everything remote-watch prints.
type remoteSnap struct {
	REL          mailbox.REL
	Hook         mailbox.RELHook
	Blk          mailbox.RemoteBlock
	Tail         uint32
	St           mailbox.RemoteStatus
	Shanm        remoteShanmRead
	Profile      remoteProfileRead
	Collect      uint8 // the collect byte at addr.Collect (all of it)
	Info0        uint32
	Player       uint32
	LinkPos      [3]float32
	LinkEyeDY    float32
	PoseStates   []byte
	PoseSeqs     []byte
	Bodies       [mailbox.RemoteMaxSlots]remoteBodyRead
	KeeperProc   int16
	KeeperOK     bool   // swap_state - KEEPER_SWAP_OFF is a 0xB5 actor
	SwapState    []byte // swapStateLen bytes at Hook.SwapState, nil when unread
	SwapStateErr string
}

// vec3At reads a big-endian f32 vector at off of b.
func vec3At(b []byte, off int) (v [3]float32) {
	for i := range 3 {
		v[i] = math.Float32frombits(binary.BigEndian.Uint32(b[off+4*i:]))
	}
	return v
}

// readRemoteSnap reads one tick.
func readRemoteSnap(m mem.Reader) (*remoteSnap, error) {
	s := &remoteSnap{}
	var err error
	if s.REL, err = mailbox.ReadREL(m); err != nil {
		return nil, err
	}
	if s.Hook, err = mailbox.ReadRELHook(m); err != nil {
		return nil, err
	}
	if s.Blk, s.Tail, s.St, err = mailbox.ReadRemoteBlock(m); err != nil {
		return nil, err
	}
	if s.Shanm, err = readShanm(m); err != nil {
		return nil, err
	}
	if s.Profile, err = readProfile(m); err != nil {
		return nil, err
	}
	if s.Collect, err = mem.ReadU8(m, mailbox.RemoteCollectAddr); err != nil {
		return nil, err
	}
	if s.Info0, err = mem.ReadU32(m, addr.PlayerInfo0Ptr); err != nil {
		return nil, err
	}
	if s.Player, err = mem.ReadU32(m, addr.PlayerPtr); err != nil {
		return nil, err
	}
	if addr.InMEM1(s.Player) {
		if b, err := m.Read(s.Player+addr.ActorOffCurPos, int(addr.ActorOffEyePos-addr.ActorOffCurPos)+12); err == nil && len(b) == int(addr.ActorOffEyePos-addr.ActorOffCurPos)+12 {
			s.LinkPos = vec3At(b, 0)
			s.LinkEyeDY = vec3At(b, int(addr.ActorOffEyePos-addr.ActorOffCurPos))[1] - s.LinkPos[1]
		}
	}
	pb, err := m.Read(mailbox.Mailbox+mailbox.OffPoseBufStates, mailbox.LenPoseBufStates)
	if err != nil {
		return nil, err
	}
	s.PoseStates = pb
	if s.PoseSeqs, err = m.Read(mailbox.Mailbox+mailbox.OffPoseSeqs, mailbox.LenPoseSeqs); err != nil {
		return nil, err
	}
	for i := range s.Bodies {
		s.Bodies[i] = readRemoteBody(m, s.St.Bodies[i].Actor, s.St.Bodies[i].Kind)
	}
	if ss := s.Hook.SwapState; ss != 0 {
		keeper := ss - mailbox.RemoteKeeperSwapOff
		if !addr.InMEM1(ss) || mem.CheckRange(ss, int(swapStateLen)) != nil {
			s.SwapStateErr = fmt.Sprintf("0x%08X is not in MEM1", ss)
		} else {
			if p, err := mem.ReadS16(m, keeper+addr.ActorOffProcName); err == nil {
				s.KeeperProc = p
				s.KeeperOK = uint16(p) == inject.RELProcID
			}
			b, err := m.Read(ss, int(swapStateLen))
			if err != nil || len(b) != int(swapStateLen) {
				s.SwapStateErr = fmt.Sprintf("read 0x%08X: %v", ss, err)
			} else {
				s.SwapState = b
			}
		}
	}
	return s, nil
}

// readShanm reads the shanm table's header through WWREL_SHANM_OFF.
func readShanm(m mem.Reader) (remoteShanmRead, error) {
	var r remoteShanmRead
	var err error
	if r.Word, err = mem.ReadU32(m, mailbox.Mailbox+mailbox.OffRELShanm); err != nil || r.Word == 0 {
		return r, err
	}
	const n = mailbox.RemoteSAJointDiffOff + 4
	if r.Word&3 != 0 || mem.CheckRange(r.Word, int(mailbox.RemoteShanmBytes)) != nil {
		r.Err = fmt.Sprintf("0x%08X is not an aligned MEM1 block of 0x%X bytes", r.Word, mailbox.RemoteShanmBytes)
		return r, nil
	}
	b, err := m.Read(r.Word, n)
	if err != nil || len(b) != n {
		r.Err = fmt.Sprintf("read 0x%08X: %v", r.Word, err)
		return r, nil
	}
	be := binary.BigEndian
	switch {
	case be.Uint32(b[mailbox.RemoteSAMagicOff:]) != mailbox.RemoteShanmMagic:
		r.Err = fmt.Sprintf("magic 0x%08X, not 'WWSA'", be.Uint32(b[mailbox.RemoteSAMagicOff:]))
	case be.Uint32(b[mailbox.RemoteSASizeOff:]) != mailbox.RemoteShanmBytes:
		r.Err = fmt.Sprintf("size 0x%X, not 0x%X", be.Uint32(b[mailbox.RemoteSASizeOff:]), mailbox.RemoteShanmBytes)
	case be.Uint32(b[mailbox.RemoteSASelfOff:]) != r.Word:
		r.Err = fmt.Sprintf("self 0x%08X, not the block", be.Uint32(b[mailbox.RemoteSASelfOff:]))
	}
	r.Words, r.CreateDiff = be.Uint32(b[mailbox.RemoteSAWordsOff:]), be.Uint32(b[mailbox.RemoteSACreateDiffOff:])
	r.DeleteDiff, r.DeleteScan = be.Uint32(b[mailbox.RemoteSADeleteDiffOff:]), be.Uint32(b[mailbox.RemoteSADeleteScanOff:])
	r.Windows, r.JointDiff = be.Uint32(b[mailbox.RemoteSAWindowsOff:]), be.Uint32(b[mailbox.RemoteSAJointDiffOff:])
	return r, nil
}

// readProfile reads the DMC alias and the profile list's slot 0x27.
func readProfile(m mem.Reader) (remoteProfileRead, error) {
	var r remoteProfileRead
	var err error
	if r.DMCFull, err = mem.ReadU32(m, mailbox.RemoteDMCFullAddr); err != nil {
		return r, err
	}
	if r.DMCREL, err = mem.ReadU32(m, mailbox.RemoteDMCRELAddr); err != nil {
		return r, err
	}
	if r.List, err = mem.ReadU32(m, mailbox.RemoteProfileListPtr); err != nil {
		return r, err
	}
	if r.List == 0 {
		r.Err = "no profile list (f_pc_profile_lst not linked)"
		return r, nil
	}
	if !addr.InMEM1(r.List) || mem.CheckRange(r.List, 4*int(inject.RELProcID)+4) != nil {
		r.Err = fmt.Sprintf("profile list 0x%08X is not in MEM1", r.List)
		return r, nil
	}
	if r.Slot27, err = mem.ReadU32(m, r.List+4*uint32(mailbox.RemoteFullProc)); err != nil {
		return r, err
	}
	if r.SlotB5, err = mem.ReadU32(m, r.List+4*uint32(inject.RELProcID)); err != nil {
		return r, err
	}
	if !addr.InMEM1(r.Slot27) || mem.CheckRange(r.Slot27, 0x14) != nil {
		r.Err = fmt.Sprintf("slot 0x27 0x%08X is not in MEM1", r.Slot27)
		return r, nil
	}
	b, err := m.Read(r.Slot27, 0x14)
	if err != nil || len(b) != 0x14 {
		r.Err = fmt.Sprintf("read 0x%08X: %v", r.Slot27, err)
		return r, nil
	}
	// process_profile_definition: mProcName s16 +0x08, mSize s32 +0x10
	// (f_pc_profile.h:8-17).
	r.ProcName, r.Size = int16(binary.BigEndian.Uint16(b[0x08:])), binary.BigEndian.Uint32(b[0x10:])
	return r, nil
}

// readRemoteBody reads the body actor at a (0: none) laid out as kind.
func readRemoteBody(m mem.Reader, a uint32, kind uint8) remoteBodyRead {
	r := remoteBodyRead{Kind: kind}
	if a == 0 {
		return r
	}
	if kind == mailbox.RemoteKindFull {
		return readFullBody(m, a, r)
	}
	if !addr.InMEM1(a) || mem.CheckRange(a, mailbox.RemoteBodyEnd) != nil {
		r.Err = fmt.Sprintf("0x%08X not in MEM1", a)
		return r
	}
	b, err := m.Read(a, mailbox.RemoteBodyEnd)
	if err != nil || len(b) != mailbox.RemoteBodyEnd {
		r.Err = fmt.Sprintf("read: %v", err)
		return r
	}
	be := binary.BigEndian
	r.Read = true
	r.Proc = int16(be.Uint16(b[addr.ActorOffProcName:]))
	r.Prof = int16(be.Uint16(b[actorOffProf:]))
	r.Magic = be.Uint32(b[mailbox.RemoteBodyMagicOff:])
	r.Slot = b[mailbox.RemoteBodySlotOff]
	r.Gen = b[mailbox.RemoteBodyGenOff]
	r.IFramesLeft = b[mailbox.RemoteBodyIFramesLeftOff]
	r.State = b[mailbox.RemoteBodyStateOff]
	r.VPtr = be.Uint32(b[mailbox.RemoteBodyVPtrOff:])
	r.VT1C = be.Uint32(b[mailbox.RemoteBodyVTOff+0x1C:])
	r.Pos = vec3At(b, int(addr.ActorOffCurPos))
	r.Eye = vec3At(b, int(addr.ActorOffEyePos))
	r.LastPoseSeq = be.Uint32(b[mailbox.RemoteBodyLastPoseSeqOff:])
	r.ParkSince = be.Uint32(b[mailbox.RemoteBodyParkSinceOff:])
	return r
}

// readFullBody reads a FULL body: the actor and daPy fields up to
// mpCurrLinktex, the tail, mCurProc, the shield range and mModeFlg, and
// mCyl's SPrm words.
func readFullBody(m mem.Reader, a uint32, r remoteBodyRead) remoteBodyRead {
	if !addr.InMEM1(a) || mem.CheckRange(a, int(mailbox.RemoteFullSize)) != nil {
		r.Err = fmt.Sprintf("0x%08X not in MEM1", a)
		return r
	}
	read := func(off uint32, n int) []byte {
		if r.Err != "" {
			return nil
		}
		b, err := m.Read(a+off, n)
		if err != nil || len(b) != n {
			r.Err = fmt.Sprintf("read +0x%X: %v", off, err)
			return nil
		}
		return b
	}
	h := read(0, fullHeadLen)
	t := read(mailbox.RemoteFullTailOff, fullTailLen)
	cp := read(mailbox.RemoteFullCurProcOff, 4)
	sr := read(mailbox.RemoteFullShieldRangeOff, 2)
	mf := read(mailbox.RemoteFullModeFlgOff, 4)
	cyl := read(mailbox.RemoteFullCylOff, fullCylLen)
	if r.Err != "" {
		return r
	}
	be := binary.BigEndian
	tl := func(off int) []byte { return t[off-mailbox.RemoteFullTailOff:] }
	r.Read = true
	r.Proc = int16(be.Uint16(h[addr.ActorOffProcName:]))
	r.Prof = int16(be.Uint16(h[actorOffProf:]))
	r.Pos = vec3At(h, int(addr.ActorOffCurPos))
	r.Eye = vec3At(h, int(addr.ActorOffEyePos))
	r.DamageWait = int16(be.Uint16(h[mailbox.RemoteFullDamageWaitOff:]))
	r.Reset0 = be.Uint32(h[mailbox.RemoteFullReset0Off:])
	r.VPtr = be.Uint32(h[mailbox.RemoteFullVPtrOff:])
	r.Model = be.Uint32(h[mailbox.RemoteFullModelOff:])
	r.Linktex = be.Uint32(h[mailbox.RemoteFullLinktexOff:])
	r.Magic = be.Uint32(tl(mailbox.RemoteFullMagicOff))
	r.Slot, r.Gen = tl(mailbox.RemoteFullSlotOff)[0], tl(mailbox.RemoteFullGenOff)[0]
	r.IFramesLeft, r.State = tl(mailbox.RemoteFullIFramesLeftOff)[0], tl(mailbox.RemoteFullStateOff)[0]
	r.LastPoseSeq = be.Uint32(tl(mailbox.RemoteFullLastPoseSeqOff))
	r.ParkSince = be.Uint32(tl(mailbox.RemoteFullParkSinceOff))
	r.Phase, r.TailKind = tl(mailbox.RemoteFullPhaseOff)[0], tl(mailbox.RemoteFullKindOff)[0]
	r.CreateFrames = be.Uint16(tl(mailbox.RemoteFullCreateFramesOff))
	r.ShanmDiff = be.Uint32(tl(mailbox.RemoteFullShanmDiffOff))
	r.HeapErr = be.Uint32(tl(mailbox.RemoteFullHeapErrOff))
	r.CurProc = be.Uint32(cp)
	r.ShieldRange = int16(be.Uint16(sr))
	r.ModeFlg = be.Uint32(mf)
	r.TgSPrm, r.CoSPrm, r.GTgSPrm = be.Uint32(cyl[cylOffTgSPrm:]), be.Uint32(cyl[cylOffCoSPrm:]), be.Uint32(cyl[cylOffGTgSPrm:])
	return r
}

// resolveTarget names a player pointer as the snapshot knows it: null,
// host (mpPlayerPtr[0]), body N (body_status[N].actor) or other.
func (s *remoteSnap) resolveTarget(p uint32) string {
	switch {
	case p == 0:
		return "null"
	case p == s.Player:
		return "host"
	}
	for i, b := range s.St.Bodies {
		if b.Actor != 0 && p == b.Actor {
			return fmt.Sprintf("body%d", i)
		}
	}
	return fmt.Sprintf("other 0x%08X", p)
}

// valid is the block's validity in words.
func (s *remoteSnap) valid() string {
	var why []string
	switch s.Blk.Magic {
	case mailbox.RemoteMagic:
	case 0:
		why = append(why, "magic 0 (off: vanilla-equivalent)")
	default:
		why = append(why, fmt.Sprintf("magic 0x%08X unknown", s.Blk.Magic))
	}
	if s.Blk.Magic != 0 && s.Blk.Layout != mailbox.RemoteLayout {
		why = append(why, fmt.Sprintf("layout %d != %d", s.Blk.Layout, mailbox.RemoteLayout))
	}
	if s.Tail != s.Blk.Seq {
		why = append(why, fmt.Sprintf("TORN head %d tail %d", s.Blk.Seq, s.Tail))
	}
	if s.Blk.Magic == mailbox.RemoteMagic && s.St.AckSeq == 0 {
		why = append(why, "never acked (the REL has not consumed a write)")
	}
	// The +0x1C word: FULL_ALLOW only under FULL_BODIES, one bit, a slot
	// that exists; the client writes none of it; FLAGS2 bits known.
	b := &s.Blk
	switch {
	case b.Role == mailbox.RemoteRoleClient && (b.FullAllow != 0 || b.HeapReason != 0 || b.Flags2 != 0):
		why = append(why, fmt.Sprintf("client block carries full_allow 0x%02X heap_reason %d flags2 0x%04X", b.FullAllow, b.HeapReason, b.Flags2))
	case b.FullAllow != 0 && b.Flags&mailbox.RemoteFlagFullBodies == 0:
		why = append(why, fmt.Sprintf("full_allow 0x%02X without full_bodies", b.FullAllow))
	case b.FullAllow&(b.FullAllow-1) != 0 || b.FullAllow>>mailbox.MaxRemoteLinks != 0:
		why = append(why, fmt.Sprintf("full_allow 0x%02X is not one slot bit", b.FullAllow))
	}
	if b.HeapReason >= uint8(len(remoteHGNames)) {
		why = append(why, fmt.Sprintf("heap_reason %d unknown", b.HeapReason))
	}
	if b.Flags2&^mailbox.RemoteFlag2FullCo != 0 {
		why = append(why, fmt.Sprintf("flags2 0x%04X has unknown bits", b.Flags2))
	}
	if s.Blk.Magic == mailbox.RemoteMagic && s.St.AckFrame != 0 && s.REL.Frames-s.St.AckFrame > mailbox.RemoteStaleFrames {
		why = append(why, fmt.Sprintf("STALE: last ack %d REL frames ago", s.REL.Frames-s.St.AckFrame))
	}
	if len(why) == 0 {
		return "valid"
	}
	return strings.Join(why, ", ")
}

// remoteMoblin is one mo2 the watch tracks.
type remoteMoblin struct {
	a    actors.Actor
	m714 uint32
	ok   bool
	what string
}

// mo2sInRoom returns the executing Moblins of room (any mode, keyed or
// not: the swap gate only skips modes 5 and 0xF itself), sorted by setID.
func mo2sInRoom(w *actors.Walk, room int8) []actors.Actor {
	var out []actors.Actor
	for i := range w.Actors {
		a := &w.Actors[i]
		if a.Proc == int16(stage.ProcMO2) && a.InitState == 2 && a.HomeRoom == room {
			out = append(out, *a)
		}
	}
	slices.SortFunc(out, func(a, b actors.Actor) int { return int(a.SetID) - int(b.SetID) })
	return out
}

// runRemoteWatch is the remote-watch command.
func runRemoteWatch(cmd string, args []string) {
	pos, flags, values, err := splitValueFlags(args, []string{"csv"}, "latch-raw")
	if err != nil {
		exitUsage(err, remoteWatchUsage)
	}
	secs, err := diagIntArg(pos, 0, 10, 0, 86400, "secs")
	if err != nil {
		exitUsage(err, remoteWatchUsage)
	}
	var csv io.Writer
	if path := values["csv"]; path != "" {
		f, err := os.Create(path)
		if err != nil {
			exitUsage(err, remoteWatchUsage)
		}
		defer f.Close()
		csv = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		return remoteWatch(ctx, os.Stdout, csv, m, time.Duration(secs)*time.Second, flags["latch-raw"])
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// remoteWatchEvery is how often remote-watch prints its summary (a var so
// the test sees summaries inside a short run).
var remoteWatchEvery = time.Second

const remoteCSVHeader = "t_ms,rel_frames,exec_hook,swap_state,magic,seq_head,seq_tail,flags,role,ack_seq,ack_frame,collect_lo,info0,player," +
	"hook_calls,swaps,switches,attacker_overrides,denied,rouya_skips,park_rewrites,both_word_swaps,vt_refusals,gate_skips,hit_head," +
	"b0_state,b0_err,b0_actor,b0_pid,b0_hits,b0_latched_by,b0_frames_live,b1_state,b1_err,b1_actor,b1_pid,b1_hits,b1_latched_by,b1_frames_live," +
	"b2_state,b2_err,b2_actor,b2_pid,b2_hits,b2_latched_by,b2_frames_live,b3_state,b3_err,b3_actor,b3_pid,b3_hits,b3_latched_by,b3_frames_live," +
	"apply_seq,apply_ack,apply_result,apply_detail,life_before,life_after,full_allow,heap_reason,flags2,b0_kind,b1_kind,b2_kind,b3_kind,m714"

// remoteWatch runs the watch over m for d (0: until ctx ends).
func remoteWatch(ctx context.Context, w, csv io.Writer, m mem.Reader, d time.Duration, latchRaw bool) error {
	s, err := readRemoteSnap(m)
	if err != nil {
		return err
	}
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "remote-Link block at 0x%08X (0x%X bytes), hook words at 0x%08X/0x%08X; stage %q room %d; m714 at mo2+0x%X; swap state as inject/rel/remote.h lays it out\n",
		mailbox.RemoteBlockAddr, mailbox.RemoteSize, mailbox.Mailbox+mailbox.OffRELExecHook, mailbox.Mailbox+mailbox.OffRELSwapState, stageName, room, mo2OffM714)
	remoteHeader(w, s)
	if csv != nil {
		fmt.Fprintln(csv, remoteCSVHeader)
	}
	start := time.Now()
	var deadline time.Time
	if d > 0 {
		deadline = start.Add(d)
	}
	var (
		mos       []remoteMoblin
		nextWalk  time.Time
		nextLine  = start // the first tick prints a summary
		lastHead  = s.St.HitRingHead
		lastState [mailbox.RemoteMaxSlots]uint8
		prev      = s // the snapshot of the last summary
		prevAt    = start
	)
	for i, b := range s.St.Bodies {
		lastState[i] = b.State
	}
	// The ring as it stands, so a count of new entries starts from a
	// known head.
	for _, h := range s.St.HitsSince(0) {
		fmt.Fprintf(w, "hit ring at start (head %d): %s\n", s.St.HitRingHead, remoteHitText(&h))
	}
	walk := func(now time.Time) error {
		nextWalk = now.Add(enemyWalkEvery)
		stageName, room, err = enemyScope(m)
		if err != nil {
			return err
		}
		aw, err := actors.ReadWith(m, 0, mo2Layouts)
		if err != nil {
			return err
		}
		found := mo2sInRoom(&aw, room)
		was := make([]uint32, len(mos))
		for i := range mos {
			was[i] = mos[i].a.PID
		}
		now2 := make([]uint32, len(found))
		for i := range found {
			now2[i] = found[i].PID
		}
		if slices.Equal(was, now2) {
			return nil
		}
		old := mos
		mos = make([]remoteMoblin, len(found))
		for i := range found {
			mos[i].a = found[i]
			if j := slices.IndexFunc(old, func(o remoteMoblin) bool { return o.a.PID == found[i].PID }); j >= 0 {
				mos[i] = old[j]
				mos[i].a = found[i]
			}
		}
		fmt.Fprintf(w, "[%6.3fs] walk: %d Moblins in room %d of %q:", now.Sub(start).Seconds(), len(mos), room, stageName)
		for i := range mos {
			a := &mos[i].a
			fmt.Fprintf(w, " [setID %d pid %d 0x%08X mode %d]", a.SetID, a.PID, a.Addr, a.Mode)
		}
		fmt.Fprintln(w)
		return nil
	}
	t := time.NewTicker(enemyWatchTick)
	defer t.Stop()
	tick := func(now time.Time) error {
		if now.After(nextWalk) || nextWalk.IsZero() {
			if err := walk(now); err != nil {
				return err
			}
		}
		s, err := readRemoteSnap(m)
		if err != nil {
			return err
		}
		at := now.Sub(start).Seconds()
		for _, h := range s.St.HitsSince(lastHead) {
			fmt.Fprintf(w, "[%6.3fs] HIT %s\n", at, remoteHitText(&h))
		}
		lastHead = s.St.HitRingHead
		for i, b := range s.St.Bodies {
			if b.State != lastState[i] {
				fmt.Fprintf(w, "[%6.3fs] body %d: %s -> %s (kind %s err %s actor 0x%08X pid %d tries %d)\n", at, i,
					enumName(remoteBodyStateNames, lastState[i]), enumName(remoteBodyStateNames, b.State), enumName(remoteKindNames, b.Kind), enumName(remoteBErrNames, b.Err), b.Actor, b.PID, b.Tries)
				lastState[i] = b.State
			}
		}
		var m714s []string
		for i := range mos {
			mo := &mos[i]
			v, err := mem.ReadU32(m, mo.a.Addr+mo2OffM714)
			what := ""
			if err != nil {
				what = "unread"
			} else {
				what = s.resolveTarget(v)
			}
			if mo.ok && what != mo.what {
				fmt.Fprintf(w, "[%6.3fs] m714 setID %d pid %d: %s -> %s\n", at, mo.a.SetID, mo.a.PID, mo.what, what)
			}
			mo.m714, mo.ok, mo.what = v, true, what
			m714s = append(m714s, fmt.Sprintf("%d:%s", mo.a.SetID, what))
		}
		if csv != nil {
			remoteCSV(csv, now.Sub(start).Milliseconds(), s, strings.Join(m714s, ";"))
		}
		if !now.Before(nextLine) {
			nextLine = now.Add(remoteWatchEvery)
			remoteSummary(w, at, s, prev, now.Sub(prevAt), mos, latchRaw)
			prev, prevAt = s, now
		}
		return nil
	}
	if err := tick(start); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if !deadline.IsZero() && now.After(deadline) {
				return nil
			}
			if err := tick(now); err != nil {
				return err
			}
		}
	}
}

// remoteHeader prints the tunables, PROC_POLICY and the FF denylist.
func remoteHeader(w io.Writer, s *remoteSnap) {
	b := &s.Blk
	fmt.Fprintf(w, "tunables: epoch %d margin_q8 %d min_hold %d body_iframes %d role %d dy_max %.1f create_cooldown %d; %s\n",
		b.Epoch, b.MarginQ8, b.MinHold, b.BodyIFrames, b.Role, b.DYMax, b.CreateCooldown, fullFieldsText(b))
	var pol []string
	for _, p := range b.Policy {
		var f []string
		for _, n := range []struct {
			bit  uint8
			name string
		}{{mailbox.RemotePFDeny, "DENY"}, {mailbox.RemotePFAllow, "ALLOW"}, {mailbox.RemotePFBothWords, "BOTH_WORDS"}, {mailbox.RemotePFNoLatch, "NO_LATCH"}} {
			if p.Flags&n.bit != 0 {
				f = append(f, n.name)
			}
		}
		pol = append(pol, fmt.Sprintf("0x%04X %s", uint16(p.Proc), strings.Join(f, "|")))
	}
	fmt.Fprintf(w, "policy (%d): %s; ff denylist (%d): %q\n", len(b.Policy), strings.Join(pol, ", "), len(b.FFDeny), b.FFDeny)
}

// fullFieldsText is the +0x1C word: FULL_ALLOW, HEAP_REASON, FLAGS2.
func fullFieldsText(b *mailbox.RemoteBlock) string {
	co := "Co off"
	if b.Flags2&mailbox.RemoteFlag2FullCo != 0 {
		co = "Co on"
	}
	return fmt.Sprintf("full_allow 0x%02X heap_reason %s flags2 0x%04X (%s)", b.FullAllow, enumName(remoteHGNames, b.HeapReason), b.Flags2, co)
}

// profileText is the second profile's wiring.
func profileText(p *remoteProfileRead) string {
	alias := "aliased"
	switch {
	case p.DMCFull == 0 && p.DMCREL == 0:
		alias = "empty (not booted?)"
	case p.DMCFull != p.DMCREL:
		alias = "NOT ALIASED (no name-table patch: a FULL create is refused, BERR profile)"
	}
	out := fmt.Sprintf("DMC[0x27] 0x%08X DMC[0xB5] 0x%08X %s", p.DMCFull, p.DMCREL, alias)
	if p.Err != "" {
		return out + "; " + p.Err
	}
	prof := "g_profile_WWBODY (proc 0x00B5, size 0x4C80)"
	if uint16(p.ProcName) != inject.RELProcID || p.Size != mailbox.RemoteFullSize {
		prof = fmt.Sprintf("NOT the body profile (proc 0x%04X size 0x%X; module 0x58 not linked or no RELS.arc patch)", uint16(p.ProcName), p.Size)
	}
	return out + fmt.Sprintf("; list 0x%08X slot 0x27 -> 0x%08X %s, slot 0xB5 -> 0x%08X", p.List, p.Slot27, prof, p.SlotB5)
}

// shanmText is the shanm table's header.
func shanmText(t *remoteShanmRead) string {
	if t.Word == 0 {
		return "not allocated (no FULL create this boot)"
	}
	out := fmt.Sprintf("0x%08X", t.Word)
	if t.Err != "" {
		return out + " BAD: " + t.Err + " (the REL treats it as 0)"
	}
	scan := "ok"
	if t.DeleteScan != 0 {
		scan = "NOT 0: tracked words point into a deleted body"
	}
	return out + fmt.Sprintf(" 'WWSA' ok: words %d create_diff %d delete_diff %d delete_scan %d (%s) windows %d joint_diff %d",
		t.Words, t.CreateDiff, t.DeleteDiff, t.DeleteScan, scan, t.Windows, t.JointDiff)
}

// remoteSummary prints the once-a-second block.
func remoteSummary(w io.Writer, at float64, s, prev *remoteSnap, dt time.Duration, mos []remoteMoblin, latchRaw bool) {
	keeper := "no keeper (swap_state 0)"
	if s.Hook.SwapState != 0 {
		switch {
		case s.SwapStateErr != "":
			keeper = "swap_state BAD: " + s.SwapStateErr
		case s.KeeperOK:
			keeper = fmt.Sprintf("keeper 0x%08X proc 0x%04X ok", s.Hook.SwapState-mailbox.RemoteKeeperSwapOff, uint16(s.KeeperProc))
		default:
			keeper = fmt.Sprintf("keeper 0x%08X proc 0x%04X NOT 0x%04X", s.Hook.SwapState-mailbox.RemoteKeeperSwapOff, uint16(s.KeeperProc), inject.RELProcID)
		}
	}
	fmt.Fprintf(w, "[%6.3fs] REL linked=%v build 0x%08X frames %d; exec_hook 0x%08X swap_state 0x%08X (%s)\n",
		at, s.REL.Linked, s.REL.BuildID, s.REL.Frames, s.Hook.ExecHook, s.Hook.SwapState, keeper)
	jail := "pearls set: the rouya gate passes"
	switch {
	case s.Collect&mailbox.RemoteCollectJailMask != 0:
	case s.Blk.Flags&mailbox.RemoteFlagRouyaGateOff != 0:
		jail = "no pearls, but the rouya gate is OFF: jail Moblins swap"
	default:
		jail = "no pearls: the rouya gate skips every mo2 swap"
	}
	pp := "equal"
	if s.Info0 != s.Player {
		pp = "DIFFER"
	}
	fmt.Fprintf(w, "        block %s: seq %d ack_seq %d ack_frame %d flags %s role %d; collect 0x%02X & 0x0F = 0x%X (%s); mPlayerInfo[0] 0x%08X mpPlayerPtr[0] 0x%08X %s; Link eye_dy %.1f\n",
		s.valid(), s.Blk.Seq, s.St.AckSeq, s.St.AckFrame, remoteFlagsText(s.Blk.Flags), s.Blk.Role,
		s.Collect, s.Collect&mailbox.RemoteCollectJailMask, jail, s.Info0, s.Player, pp, s.LinkEyeDY)
	fmt.Fprintf(w, "        full: %s\n        profile: %s\n        shanm: %s\n", fullFieldsText(&s.Blk), profileText(&s.Profile), shanmText(&s.Shanm))
	var cs []string
	for i, c := range s.St.Counters {
		cs = append(cs, fmt.Sprintf("%s %d(+%d)", mailbox.RemoteCounterNames[i], c, c-prev.St.Counters[i]))
	}
	perFrame := "n/a"
	if df := s.REL.Frames - prev.REL.Frames; df > 0 {
		perFrame = fmt.Sprintf("%.1f", float64(s.St.Counters[mailbox.RemoteCtrHookCalls]-prev.St.Counters[mailbox.RemoteCtrHookCalls])/float64(df))
	}
	fmt.Fprintf(w, "        counters: %s; hook_calls/frame %s over %d REL frames (%.2fs); hit_head %d\n",
		strings.Join(cs, " "), perFrame, s.REL.Frames-prev.REL.Frames, dt.Seconds(), s.St.HitRingHead)
	for i := range mailbox.RemoteMaxSlots {
		sl := &s.Blk.Slots[i]
		bs := &s.St.Bodies[i]
		if !sl.Active && bs.State == mailbox.RemoteBodyEmpty && bs.Actor == 0 {
			continue
		}
		pose := "pose slot out of range"
		if int(sl.PoseSlot) < len(s.PoseStates) {
			ps := s.PoseStates[sl.PoseSlot]
			note := "ready"
			if ps != 1 {
				note = "NOT ready: the body parks"
			}
			pose = fmt.Sprintf("pose_buf_state %d (%s) pose_seq %d", ps, note, s.PoseSeqs[sl.PoseSlot])
		}
		fmt.Fprintf(w, "        slot %d: active %v sflags 0x%02X stage %q room %d pos %.1f %.1f %.1f ang %d shield %d eye_dy %.1f cyl %.1f/%.1f iframes %d cut %d/%d pid %d pose_slot %d gen %d; %s\n",
			i, sl.Active, sl.SFlags, sl.Stage, sl.Room, sl.Pos[0], sl.Pos[1], sl.Pos[2], sl.AngleY, sl.ShieldAngleY, sl.EyeDY, sl.CylR, sl.CylH, sl.IFrames, sl.CutType, sl.CutCount, sl.PlayerID, sl.PoseSlot, sl.Gen, pose)
		fmt.Fprintf(w, "          body: %s %s err %s actor 0x%08X pid %d hits %d latched_by %d frames_live %d tries %d; %s\n",
			enumName(remoteKindNames, bs.Kind), enumName(remoteBodyStateNames, bs.State), enumName(remoteBErrNames, bs.Err), bs.Actor, bs.PID, bs.Hits, bs.LatchedBy, bs.FramesLive, bs.Tries, remoteBodyText(bs.Actor, &s.Bodies[i]))
	}
	fmt.Fprintf(w, "        swap: %s\n        latch: %s\n", swapText(s), latchText(s))
	if latchRaw && s.SwapState != nil {
		for off := 0; off < len(s.SwapState); off += 0x20 {
			var ws []string
			for k := off; k < min(off+0x20, len(s.SwapState)); k += 4 {
				ws = append(ws, fmt.Sprintf("%08X", binary.BigEndian.Uint32(s.SwapState[k:])))
			}
			fmt.Fprintf(w, "          +0x%03X: %s\n", off, strings.Join(ws, " "))
		}
	}
	a := &s.Blk.Apply
	r := s.St.ApplyResult
	fmt.Fprintf(w, "        apply: seq %d setID %d atp %d se %d hitmark %d spl %d at_sprm 0x%02X flags 0x%02X attype 0x%08X rel_hit %.1f %.1f %.1f rvec %.2f %.2f %.2f; ack %d result %s detail %s executes %d sets %d life %d -> %d\n",
		a.Seq, a.SetID, a.Atp, a.Se, a.HitMark, a.Spl, a.AtSPrm, a.Flags, a.AtType, a.RelHit[0], a.RelHit[1], a.RelHit[2], a.RVec[0], a.RVec[1], a.RVec[2],
		s.St.ApplyAck, enumName(remoteARNames, r.Result), enumName(remoteADNames, r.Detail), r.Frames, r.Sets, s.St.LifeBefore, s.St.LifeAfter)
	var ms []string
	for i := range mos {
		mo := &mos[i]
		ms = append(ms, fmt.Sprintf("setID %d pid %d -> %s", mo.a.SetID, mo.a.PID, mo.what))
	}
	if len(ms) == 0 {
		ms = []string{"no Moblins in the room"}
	}
	fmt.Fprintf(w, "        m714: %s\n", strings.Join(ms, "; "))
}

// remoteBodyText is the body read back: magic, slot/gen/state, the vptr
// check (vptr == body + VT_OFF) and vt+0x1C, pos and eyePos.
func remoteBodyText(actor uint32, r *remoteBodyRead) string {
	if actor == 0 {
		return "no actor"
	}
	if !r.Read {
		return "unread: " + r.Err
	}
	if r.Kind == mailbox.RemoteKindFull {
		return fullBodyText(r)
	}
	magic := "'WWRB'"
	if r.Magic != mailbox.RemoteBodyMagic {
		magic = fmt.Sprintf("BAD 0x%08X", r.Magic)
	}
	vp := "own vt"
	if r.VPtr != actor+mailbox.RemoteBodyVTOff {
		vp = fmt.Sprintf("NOT own vt (want 0x%08X)", actor+mailbox.RemoteBodyVTOff)
	}
	return fmt.Sprintf("read: proc 0x%04X prof 0x%04X magic %s slot %d gen %d state %s iframes_left %d vptr 0x%08X %s vt+0x1C 0x%08X pos %.1f %.1f %.1f eye_dy %.1f last_pose_seq %d park_since %d",
		uint16(r.Proc), uint16(r.Prof), magic, r.Slot, r.Gen, enumName(remoteBodyStateNames, r.State), r.IFramesLeft, r.VPtr, vp, r.VT1C,
		r.Pos[0], r.Pos[1], r.Pos[2], r.Eye[1]-r.Pos[1], r.LastPoseSeq, r.ParkSince)
}

// fullBodyText is a FULL body read back: the tail (the magic is written
// last in create, so 0 while it is still creating), the vtable
// fingerprint and the daPy fields execute keeps.
func fullBodyText(r *remoteBodyRead) string {
	magic := "'WWRB'"
	if r.Magic != mailbox.RemoteBodyMagic {
		magic = fmt.Sprintf("0x%08X (not yet: creating, or BAD)", r.Magic)
	}
	vp := "daPy_lk_c ok"
	if r.VPtr != dapyLkVtable {
		vp = fmt.Sprintf("NOT __vt__9daPy_lk_c 0x%08X", dapyLkVtable)
	}
	prof := "ok"
	if uint16(r.Prof) != mailbox.RemoteFullProc || uint16(r.Proc) != inject.RELProcID {
		prof = fmt.Sprintf("WANT proc 0x%04X prof 0x%04X", inject.RELProcID, mailbox.RemoteFullProc)
	}
	co := "Co off"
	if r.CoSPrm&1 != 0 {
		co = "Co SET"
	}
	return fmt.Sprintf("FULL read: proc 0x%04X prof 0x%04X (%s) magic %s slot %d gen %d state %s iframes_left %d phase %s kind %d create_frames %d shanm_diff %d heap_err 0x%08X; vptr 0x%08X %s model 0x%08X linktex 0x%08X cur_proc 0x%X damage_wait %d reset0 0x%08X mode_flg 0x%08X shield %d; mCyl tg_sprm 0x%08X co_sprm 0x%08X (%s) gtg_sprm 0x%08X; pos %.1f %.1f %.1f eye_dy %.1f last_pose_seq %d park_since %d",
		uint16(r.Proc), uint16(r.Prof), prof, magic, r.Slot, r.Gen, enumName(remoteBodyStateNames, r.State), r.IFramesLeft,
		enumName(remoteFPNames, r.Phase), r.TailKind, r.CreateFrames, r.ShanmDiff, r.HeapErr,
		r.VPtr, vp, r.Model, r.Linktex, r.CurProc, r.DamageWait, r.Reset0, r.ModeFlg, r.ShieldRange,
		r.TgSPrm, r.CoSPrm, co, r.GTgSPrm, r.Pos[0], r.Pos[1], r.Pos[2], r.Eye[1]-r.Pos[1], r.LastPoseSeq, r.ParkSince)
}

// swapText is the keeper's intake words and runtime body slots.
func swapText(s *remoteSnap) string {
	if s.Hook.SwapState == 0 {
		return "no swap state"
	}
	if s.SwapState == nil {
		return "unread: " + s.SwapStateErr
	}
	be := binary.BigEndian
	b := s.SwapState
	out := fmt.Sprintf("valid %d role %d flags 0x%04X swap_ready %d live_mask %02b last_seq %d stale_frames %d full_allow %02b flags2 0x%04X full_denied %02b",
		b[swValid], b[swRole], be.Uint16(b[swFlags:]), b[swSwapReady], b[swLiveMask], be.Uint32(b[swLastSeq:]), be.Uint32(b[swStaleFrames:]),
		b[swFullAllow], be.Uint16(b[swFlags2:]), b[swFullDenied])
	for i := range mailbox.MaxRemoteLinks {
		o := b[swBodies+i*swBodySize:]
		out += fmt.Sprintf("; body%d parked %d gen %d cooldown %d del_sent %d kind %s full_wait %d", i, o[swBodyParked], o[swBodyGen], be.Uint16(o[swBodyCool:]), o[swBodyDelSent],
			enumName(remoteKindNames, o[swBodyKind]), b[swFullWait+i])
	}
	return out
}

// latchText is the latch table's used entries.
func latchText(s *remoteSnap) string {
	if s.Hook.SwapState == 0 {
		return "no swap state"
	}
	if s.SwapState == nil {
		return "unread: " + s.SwapStateErr
	}
	be := binary.BigEndian
	var es []string
	for i := range latchCount {
		o := s.SwapState[swLatch+i*latchEntry:]
		pid := be.Uint32(o[latchEnemyPID:])
		if pid == 0 {
			continue
		}
		tgt := be.Uint32(o[latchTarget:])
		t := "host"
		if tgt != 0 {
			t = fmt.Sprintf("pid %d", tgt)
			for k, b := range s.St.Bodies {
				if b.PID == tgt && b.PID != 0 {
					t = fmt.Sprintf("body%d (pid %d)", k, tgt)
				}
			}
		}
		es = append(es, fmt.Sprintf("[%d] enemy pid %d -> %s hold %d force pid %d/%d mo2 %d", i, pid, t,
			o[latchHold], be.Uint32(o[latchForcePID:]), o[latchForceFr], o[latchIsMo2]))
	}
	if len(es) == 0 {
		return "no entries"
	}
	return strings.Join(es, "; ")
}

// remoteHitText is one hit ring entry.
func remoteHitText(h *mailbox.RemoteHit) string {
	var f []string
	for _, n := range []struct {
		bit  uint8
		name string
	}{{mailbox.RemoteHFShield, "shield"}, {mailbox.RemoteHFNotMo2, "not_mo2"}, {mailbox.RemoteHFNotKeyed, "not_keyed"}} {
		if h.HFlags&n.bit != 0 {
			f = append(f, n.name)
		}
	}
	if len(f) == 0 {
		f = []string{"-"}
	}
	return fmt.Sprintf("seq %d slot %d hflags %s attacker pid %d setID %d proc 0x%04X attype 0x%08X atp %d se %d hitmark %d spl %d rel_hit %.1f %.1f %.1f rvec %.2f %.2f %.2f frame %d",
		h.Seq, h.Slot, strings.Join(f, "|"), h.PID, h.SetID, uint16(h.Proc), h.AtType, h.Atp, h.Se, h.HitMark, h.Spl,
		h.RelHit[0], h.RelHit[1], h.RelHit[2], h.RVec[0], h.RVec[1], h.RVec[2], h.Frame)
}

// remoteCSV writes one tick's row (remoteCSVHeader).
func remoteCSV(w io.Writer, ms int64, s *remoteSnap, m714 string) {
	x := func(v uint32) string { return fmt.Sprintf("0x%08X", v) }
	u := func(v uint64) string { return strconv.FormatUint(v, 10) }
	cols := []string{u(uint64(ms)), u(uint64(s.REL.Frames)), x(s.Hook.ExecHook), x(s.Hook.SwapState), x(s.Blk.Magic), u(uint64(s.Blk.Seq)), u(uint64(s.Tail)),
		fmt.Sprintf("0x%04X", s.Blk.Flags), u(uint64(s.Blk.Role)), u(uint64(s.St.AckSeq)), u(uint64(s.St.AckFrame)), u(uint64(s.Collect & mailbox.RemoteCollectJailMask)), x(s.Info0), x(s.Player)}
	for _, c := range s.St.Counters {
		cols = append(cols, u(uint64(c)))
	}
	cols = append(cols, u(uint64(s.St.HitRingHead)))
	for _, b := range s.St.Bodies {
		cols = append(cols, u(uint64(b.State)), u(uint64(b.Err)), x(b.Actor), u(uint64(b.PID)), u(uint64(b.Hits)), u(uint64(b.LatchedBy)), u(uint64(b.FramesLive)))
	}
	r := s.St.ApplyResult
	cols = append(cols, u(uint64(s.Blk.Apply.Seq)), u(uint64(s.St.ApplyAck)), u(uint64(r.Result)), u(uint64(r.Detail)), u(uint64(s.St.LifeBefore)), u(uint64(s.St.LifeAfter)),
		fmt.Sprintf("0x%02X", s.Blk.FullAllow), u(uint64(s.Blk.HeapReason)), fmt.Sprintf("0x%04X", s.Blk.Flags2))
	for _, b := range s.St.Bodies {
		cols = append(cols, u(uint64(b.Kind)))
	}
	cols = append(cols, m714)
	fmt.Fprintln(w, strings.Join(cols, ","))
}

// runRemoteFlags is the remote-flags command.
func runRemoteFlags(cmd string, args []string) {
	pos, flags, err := diag.SplitFlags(args, "yes")
	if err != nil {
		exitUsage(err, remoteFlagsUsage)
	}
	if len(pos) != 1 {
		exitUsage(errors.New("one flags value"), remoteFlagsUsage)
	}
	f, err := parseRemoteFlags(pos[0])
	if err != nil {
		exitUsage(err, remoteFlagsUsage)
	}
	fmt.Printf("remote-flags writes the remote-Link flags word (0x%08X, 2 bytes) of this Dolphin: %s.\n", mailbox.RemoteBlockAddr+mailbox.RemoteFlagsOff, remoteFlagsText(f))
	if !flags["yes"] {
		fmt.Println("Not written: re-run with --yes.")
		os.Exit(1)
	}
	err = diag.WithMem(openDolphin, func(m mem.Mem) error { return remoteFlags(os.Stdout, m, f) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// remoteFlagsSettle is how long remote-flags waits before it checks its
// write held and whether a writer is bumping the seq (a var for tests).
var remoteFlagsSettle = 150 * time.Millisecond

// remoteFlags writes f into the flags word, re-reading it after
// remoteFlagsSettle and writing again (up to 3 times) when a holder's
// tick overwrote it. It says whether a writer holds the block.
func remoteFlags(w io.Writer, m mem.Mem, f uint16) error {
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		return err
	}
	if !c.WritesAllowed {
		return errors.New("this Dolphin's game is not this version's patch: " + c.Why)
	}
	blk, _, _, err := mailbox.ReadRemoteBlock(m)
	if err != nil {
		return err
	}
	if blk.Magic != mailbox.RemoteMagic {
		fmt.Fprintf(w, "warning: the block's magic is 0x%08X, not 'WWRP': the REL ignores the flags until remote-slot (or a host pipeline) writes the block\n", blk.Magic)
	}
	fmt.Fprintf(w, "flags were %s\n", remoteFlagsText(blk.Flags))
	at := mailbox.RemoteBlockAddr + mailbox.RemoteFlagsOff
	for try := 1; ; try++ {
		if err := mem.WriteU16(m, at, f); err != nil {
			return err
		}
		time.Sleep(remoteFlagsSettle)
		now, err := mem.ReadU16(m, at)
		if err != nil {
			return err
		}
		if now == f {
			break
		}
		if try == 3 {
			return fmt.Errorf("the flags word reads 0x%04X after 3 writes of 0x%04X: another writer is not preserving it", now, f)
		}
	}
	after, _, _, err := mailbox.ReadRemoteBlock(m)
	if err != nil {
		return err
	}
	held := "no writer bumped the seq: the block goes stale after 90 REL frames (run remote-slot to hold it)"
	if after.Seq != blk.Seq {
		held = fmt.Sprintf("a writer holds the block (seq %d -> %d)", blk.Seq, after.Seq)
	}
	fmt.Fprintf(w, "flags now %s; %s\n", remoteFlagsText(f), held)
	return nil
}

// remoteSlotOpts is remote-slot's command line.
type remoteSlotOpts struct {
	Slot     int
	Flags    *uint16 // nil: keep the block's (0 when seeding)
	SFlags   uint8
	Offset   [3]float32
	At       *[3]float32 // fixed position instead of Link + Offset
	Room     *int8
	Stage    string
	Role     uint8
	PoseSlot uint8
	Gen      uint8
	IFrames  int16
	EyeDY    float32
	CylR     float32
	CylH     float32
}

// parseVec3 reads "x,y,z".
func parseVec3(s string) ([3]float32, error) {
	var v [3]float32
	p := strings.Split(s, ",")
	if len(p) != 3 {
		return v, fmt.Errorf("%q is not x,y,z", s)
	}
	for i := range 3 {
		f, err := strconv.ParseFloat(strings.TrimSpace(p[i]), 32)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return v, fmt.Errorf("%q is not x,y,z", s)
		}
		v[i] = float32(f)
	}
	return v, nil
}

// parseRemoteSlotArgs reads remote-slot's arguments (without --yes).
func parseRemoteSlotArgs(pos []string, values map[string]string) (o remoteSlotOpts, secs int, err error) {
	o = remoteSlotOpts{SFlags: mailbox.RemoteSFTargetable, Offset: [3]float32{150, 0, 0}, Role: mailbox.RemoteRoleHost, Gen: 1,
		EyeDY: mailbox.RemoteDefEyeDY, CylR: mailbox.RemoteDefCylR, CylH: mailbox.RemoteDefCylH}
	if len(pos) < 1 {
		return o, 0, errors.New("a slot number")
	}
	if o.Slot, err = diagIntArg(pos[:1], 0, 0, 0, mailbox.RemoteMaxSlots-1, "slot"); err != nil {
		return o, 0, err
	}
	o.PoseSlot = uint8(o.Slot)
	if secs, err = diagIntArg(pos[1:], 0, 60, 0, 86400, "secs"); err != nil {
		return o, 0, err
	}
	num := func(name string, lo, hi int64) (int64, bool, error) {
		v, ok := values[name]
		if !ok {
			return 0, false, nil
		}
		n, err := strconv.ParseInt(v, 0, 64)
		if err != nil || n < lo || n > hi {
			return 0, false, fmt.Errorf("--%s must be a number from %d to %d, not %q", name, lo, hi, v)
		}
		return n, true, nil
	}
	flt := func(name string, def float32) (float32, error) {
		v, ok := values[name]
		if !ok {
			return def, nil
		}
		f, err := strconv.ParseFloat(v, 32)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return 0, fmt.Errorf("--%s must be a number >= 0, not %q", name, v)
		}
		return float32(f), nil
	}
	if v, ok := values["flags"]; ok {
		f, err := parseRemoteFlags(v)
		if err != nil {
			return o, 0, err
		}
		o.Flags = &f
	}
	if v, ok := values["sflags"]; ok {
		n, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(v, "0x"), "0X"), 16, 8)
		if err != nil {
			return o, 0, fmt.Errorf("--sflags %q is not a hex byte", v)
		}
		o.SFlags = uint8(n)
	}
	if v, ok := values["offset"]; ok {
		if o.Offset, err = parseVec3(v); err != nil {
			return o, 0, fmt.Errorf("--offset: %w", err)
		}
	}
	if v, ok := values["at"]; ok {
		at, err := parseVec3(v)
		if err != nil {
			return o, 0, fmt.Errorf("--at: %w", err)
		}
		o.At = &at
	}
	if n, ok, err := num("room", -1, 63); err != nil {
		return o, 0, err
	} else if ok {
		r := int8(n)
		o.Room = &r
	}
	if v, ok := values["stage"]; ok {
		if len(v) > mailbox.RemoteStageLen {
			return o, 0, fmt.Errorf("--stage %q is longer than %d bytes", v, mailbox.RemoteStageLen)
		}
		o.Stage = v
	}
	switch values["role"] {
	case "", "host":
	case "client":
		o.Role = mailbox.RemoteRoleClient
	default:
		return o, 0, fmt.Errorf("--role must be host or client, not %q", values["role"])
	}
	if n, ok, err := num("pose-slot", 0, mailbox.MailboxPoseSlotCap-1); err != nil {
		return o, 0, err
	} else if ok {
		o.PoseSlot = uint8(n)
	}
	if n, ok, err := num("gen", 0, 255); err != nil {
		return o, 0, err
	} else if ok {
		o.Gen = uint8(n)
	}
	if n, ok, err := num("iframes", -32768, 32767); err != nil {
		return o, 0, err
	} else if ok {
		o.IFrames = int16(n)
	}
	if o.EyeDY, err = flt("eye-dy", o.EyeDY); err != nil {
		return o, 0, err
	}
	if o.CylR, err = flt("cyl-r", o.CylR); err != nil {
		return o, 0, err
	}
	if o.CylH, err = flt("cyl-h", o.CylH); err != nil {
		return o, 0, err
	}
	return o, secs, nil
}

// runRemoteSlot is the remote-slot command.
func runRemoteSlot(cmd string, args []string) {
	pos, flags, values, err := splitValueFlags(args, []string{"flags", "sflags", "offset", "at", "room", "stage", "role", "pose-slot", "gen", "iframes", "eye-dy", "cyl-r", "cyl-h"}, "yes")
	if err != nil {
		exitUsage(err, remoteSlotUsage)
	}
	o, secs, err := parseRemoteSlotArgs(pos, values)
	if err != nil {
		exitUsage(err, remoteSlotUsage)
	}
	fmt.Printf("remote-slot holds the remote-Link block of this Dolphin (0x%08X): slot %d is a remote player beside Link, written every 16 ms; magic 0 is written when it stops.\n", mailbox.RemoteBlockAddr, o.Slot)
	if !flags["yes"] {
		fmt.Println("Not written: re-run with --yes.")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		return remoteSlot(ctx, os.Stdout, m, o, time.Duration(secs)*time.Second)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// remoteSlotProbe is how long remote-slot watches the seq for another
// writer before it takes the block; remoteSlotEvery is its line period;
// remoteSlotAfter the wait after magic 0 before the last status read
// (vars for tests).
var (
	remoteSlotProbe = 120 * time.Millisecond
	remoteSlotEvery = time.Second
	remoteSlotAfter = 200 * time.Millisecond
)

// remoteSlotBase is the block remote-slot starts from: the live one when
// it is 'WWRP' layout 1 (its other slots, policy and tunables kept), else
// the seed: role, RemoteDef* tunables, PROC_POLICY ALLOW mo2, a new epoch.
func remoteSlotBase(live *mailbox.RemoteBlock, now time.Time) (mailbox.RemoteBlock, bool) {
	if live.Magic == mailbox.RemoteMagic && live.Layout == mailbox.RemoteLayout {
		return *live, true
	}
	return mailbox.RemoteBlock{Magic: mailbox.RemoteMagic, Layout: mailbox.RemoteLayout, Epoch: uint32(now.Unix() & 0xFFFF),
		MarginQ8: mailbox.RemoteDefMarginQ8, MinHold: mailbox.RemoteDefMinHold, BodyIFrames: mailbox.RemoteDefBodyIFrames,
		DYMax: mailbox.RemoteDefDYMax, CreateCooldown: mailbox.RemoteDefCreateCooldown,
		Policy: []mailbox.RemotePolicy{{Proc: int16(mailbox.RemoteProcMo2), Flags: mailbox.RemotePFAllow}}}, false
}

// remoteSlot holds the block over m for d (0: until ctx ends).
func remoteSlot(ctx context.Context, w io.Writer, m mem.Mem, o remoteSlotOpts, d time.Duration) error {
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		return err
	}
	if !c.WritesAllowed {
		return errors.New("this Dolphin's game is not this version's patch: " + c.Why)
	}
	if !c.REL.Linked || c.REL.BuildID != inject.RELBuildID {
		return fmt.Errorf("the REL module is not linked with this build (linked %v, build %08X, want %08X): nothing would consume the block", c.REL.Linked, c.REL.BuildID, inject.RELBuildID)
	}
	scope, err := enemysync.ReadScope(m)
	if err != nil {
		return err
	}
	if !scope.InPlay {
		return fmt.Errorf("the game is not in play (stage %q)", scope.Stage)
	}
	live, _, _, err := mailbox.ReadRemoteBlock(m)
	if err != nil {
		return err
	}
	if live.Magic == mailbox.RemoteMagic {
		time.Sleep(remoteSlotProbe)
		again, _, _, err := mailbox.ReadRemoteBlock(m)
		if err != nil {
			return err
		}
		if again.Seq != live.Seq {
			return fmt.Errorf("another writer holds the block (seq %d -> %d in %v): stop it first, or use remote-flags", live.Seq, again.Seq, remoteSlotProbe)
		}
		live = again
	}
	start := time.Now()
	blk, kept := remoteSlotBase(&live, start)
	blk.Role = o.Role
	if o.Flags != nil {
		blk.Flags = *o.Flags
	} else if !kept {
		blk.Flags = 0
	}
	if eb, _, _, err := mailbox.ReadEnemyBlock(m); err == nil && eb.Magic == mailbox.EnemyMagic && eb.Role != blk.Role {
		fmt.Fprintf(w, "warning: the enemy block's role is %d, the remote block's %d (the design wants them equal)\n", eb.Role, blk.Role)
	}
	from := "seeded (defaults, PROC_POLICY ALLOW mo2 0x00BD)"
	if kept {
		from = fmt.Sprintf("kept from the live block (seq %d, epoch %d)", live.Seq, live.Epoch)
	}
	fmt.Fprintf(w, "stage %q room %d; block %s; role %d; flags %s; REL build %08X frame %d\n", scope.Stage, scope.Room, from, blk.Role, remoteFlagsText(blk.Flags), c.REL.BuildID, c.REL.Frames)
	ps, err := m.Read(mailbox.Mailbox+mailbox.OffPoseBufStates+uint32(o.PoseSlot), 1)
	if err == nil && len(ps) == 1 && ps[0] != 1 {
		fmt.Fprintf(w, "note: pose_buf_states[%d] is %d, not 1 (ready): with no live pose feed the REL parks the body (design 5.2 park check); run mp-local or pose-fake-loop for a mini-Link in pose slot %d\n", o.PoseSlot, ps[0], o.PoseSlot)
	}
	sl := &blk.Slots[o.Slot]
	*sl = mailbox.RemoteSlot{Active: true, SFlags: o.SFlags, IFrames: o.IFrames, EyeDY: o.EyeDY, CylR: o.CylR, CylH: o.CylH, PoseSlot: o.PoseSlot, Gen: o.Gen}
	var deadline time.Time
	if d > 0 {
		deadline = start.Add(d)
	}
	seq := live.Seq
	nextLine := start.Add(remoteSlotEvery)
	defer func() {
		if err := mailbox.ClearRemoteBlock(m); err != nil {
			fmt.Fprintf(w, "clearing the block: %v\n", err)
			return
		}
		time.Sleep(remoteSlotAfter)
		st, err := mailbox.ReadRemoteStatus(m)
		if err != nil {
			fmt.Fprintf(w, "reading the status after the clear: %v\n", err)
			return
		}
		b := st.Bodies[o.Slot]
		fmt.Fprintf(w, "stopped after %d writes: magic 0 written; body %d now %s err %s actor 0x%08X\n", seq-live.Seq, o.Slot,
			enumName(remoteBodyStateNames, b.State), enumName(remoteBErrNames, b.Err), b.Actor)
	}()
	write := func(now time.Time) error {
		scope, err := enemysync.ReadScope(m)
		if err != nil {
			return err
		}
		sl.Stage, sl.Room = scope.Stage, scope.Room
		if o.Stage != "" {
			sl.Stage = o.Stage
		}
		if o.Room != nil {
			sl.Room = *o.Room
		}
		if len(sl.Stage) > mailbox.RemoteStageLen {
			sl.Stage = sl.Stage[:mailbox.RemoteStageLen]
		}
		if o.At != nil {
			sl.Pos = *o.At
		} else if link, err := mem.ReadU32(m, addr.PlayerPtr); err == nil && addr.InMEM1(link) {
			b, err := m.Read(link+addr.ActorOffCurPos, int(addr.ActorOffShapeAngle-addr.ActorOffCurPos)+4)
			if err == nil && len(b) == int(addr.ActorOffShapeAngle-addr.ActorOffCurPos)+4 {
				p := vec3At(b, 0)
				for i := range 3 {
					sl.Pos[i] = p[i] + o.Offset[i]
				}
				sl.AngleY = int16(binary.BigEndian.Uint16(b[addr.ActorOffShapeAngle-addr.ActorOffCurPos+2:]))
			}
		}
		// The flags word as the game has it now: remote-flags may have
		// changed it since the last write.
		if seq != live.Seq {
			f, err := mem.ReadU16(m, mailbox.RemoteBlockAddr+mailbox.RemoteFlagsOff)
			if err != nil {
				return err
			}
			blk.Flags = f
		}
		seq++
		blk.Seq = seq
		if err := mailbox.WriteRemoteBlock(m, &blk); err != nil {
			return err
		}
		if !now.Before(nextLine) {
			nextLine = now.Add(remoteSlotEvery)
			st, err := mailbox.ReadRemoteStatus(m)
			if err != nil {
				return err
			}
			b := st.Bodies[o.Slot]
			fmt.Fprintf(w, "[%5.1fs] seq %d ack_seq %d ack_frame %d flags %s slot %d %q room %d pos %.1f %.1f %.1f; body %s err %s actor 0x%08X pid %d frames_live %d tries %d hits %d latched_by %d; swaps %d hook_calls %d rouya_skips %d hit_head %d\n",
				now.Sub(start).Seconds(), seq, st.AckSeq, st.AckFrame, remoteFlagsText(blk.Flags), o.Slot, sl.Stage, sl.Room, sl.Pos[0], sl.Pos[1], sl.Pos[2],
				enumName(remoteBodyStateNames, b.State), enumName(remoteBErrNames, b.Err), b.Actor, b.PID, b.FramesLive, b.Tries, b.Hits, b.LatchedBy,
				st.Counters[mailbox.RemoteCtrSwaps], st.Counters[mailbox.RemoteCtrHookCalls], st.Counters[mailbox.RemoteCtrRouyaSkips], st.HitRingHead)
		}
		return nil
	}
	if err := write(start); err != nil {
		return err
	}
	t := time.NewTicker(enemysync.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if !deadline.IsZero() && now.After(deadline) {
				return nil
			}
			if err := write(now); err != nil {
				return err
			}
		}
	}
}
