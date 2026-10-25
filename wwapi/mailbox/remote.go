package mailbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The remote-Link block in the mailbox slack (inject/include/wwremote.h,
// whose WWREMOTE_* defines these mirror; remote_test.go pins each one to
// the header). One 0x330-byte block at Mailbox+RemoteOff, past the enemy
// region: Go writes its first RemoteGoSpan bytes in one write per tick
// (RemoteBlock.Encode) with seq_head at +0x004 equal to seq_tail at
// +0x1E8, and the REL writes +0x1F0..+0x330, which Go reads back as a
// RemoteStatus. The block tells the host REL where each remote player is
// (the lite body it keeps per slot, the pointer swap's targets, the
// PROC_POLICY table and the FF denylist) and carries the client's apply
// record (a PHIT to replay as an At sphere on the client's own Link); the
// REL answers with body status, swap counters, a ring of enemy hits on the
// bodies and the apply result. Design:
// ww-multiplayer-handoff/design/remote-link-design.md §6 and its "Step −1
// results"; the header's comment carries the layout table, the wire v3
// shapes and the server's player-id mapping (pair by name).
//
// Magic 0 is today's behaviour, and so is every flag clear: the two
// safety gates are opt-outs (RemoteFlagFFGateOff, RemoteFlagRouyaGateOff).
// The slack is not zeroed at boot, so a block is valid only by its magic,
// layout, role and a moving seq; Go writes magic 0 when it stops.
//
// The REL's two remote-Link words just past its frame counter
// (OffRELExecHook, OffRELSwapState; inject/include/wwrel.h) are the REL's
// alone: T2's exec_shim branches to the first, so Go only reads them and
// the region probe never arms over them (CanaryArena ends before them).
const (
	// OffRELExecHook is WWREL_EXEC_HOOK_OFF: &wwrel_exec_hook while the
	// module is linked, else 0. OffRELSwapState is WWREL_SWAP_STATE_OFF:
	// the keeper's swap state (keeper actor + RemoteKeeperSwapOff), else 0.
	OffRELExecHook  uint32 = 0x1BC
	OffRELSwapState uint32 = 0x1C0
	// relHookWordsLen is the two words' span.
	relHookWordsLen = OffRELSwapState + 4 - OffRELExecHook
	// OffRELShanm is WWREL_SHANM_OFF: the full bodies' shanm table (an
	// archive-heap block of RemoteShanmBytes, RemoteSA* header), allocated
	// by the REL at the first full create of a boot and never freed; T2's
	// main01_init zeroes the word and the REL never clears it. Like the
	// hook words it is the REL's alone and never canary-armed (CanaryArena
	// ends at OffRELExecHook), but it is not part of RELHookRegion or
	// relHookWordsLen: Go reads it by itself.
	OffRELShanm uint32 = 0x1C4
)

const (
	RemoteMagic           uint32 = 0x57575250 // 'WWRP'
	RemoteOff             uint32 = 0x520      // block start, from Mailbox (0x80413420)
	RemoteSize            uint32 = 0x330      // whole block, ends +0x850 (0x80413750)
	RemoteLayout          uint16 = 1          // u16 at +0x08: bump on any layout change
	RemoteRegionStart     uint32 = 0x510      // what remote-Link owns, from Mailbox: EnemyRegionEnd..
	RemoteRegionEnd       uint32 = 0x850      // ..RemoteOff + RemoteSize
	RemoteMaxSlots               = 4          // slot / body_status entries in the layout (runtime: MAX_REMOTE_LINKS)
	RemoteProcPolicyMax          = 48
	RemoteFFDenyMax              = 4
	RemoteStageLen               = 8  // char[8] NUL-padded, as dStage_startStage_c::mName
	RemoteStaleFrames            = 90 // REL frames without a new seq: block stale
	RemotePoseStaleFrames        = 30 // pose seq frozen this long: the body parks

	// Go-written span: +0x000..+0x1EC
	RemoteMagicOff          = 0x000 // u32 RemoteMagic (0 = off)
	RemoteSeqHeadOff        = 0x004 // u32 ++ per tick, never 0
	RemoteLayoutOff         = 0x008 // u16 RemoteLayout
	RemoteFlagsOff          = 0x00A // u16 RemoteFlag*
	RemoteEpochOff          = 0x00C // u32 enemy-block rules (bumped on arm/stage change)
	RemoteMarginQ8Off       = 0x010 // u8 latch switch margin: d2_best < margin/256 * d2_cur
	RemoteMinHoldOff        = 0x011 // u8 latch min hold, frames
	RemoteBodyIFramesOff    = 0x012 // u8 body i-frames stamped on detection (RemoteFlagIFrameStamp)
	RemoteRoleOff           = 0x013 // u8 RemoteRole*
	RemoteDYMaxOff          = 0x014 // f32 latch candidate |dy| limit
	RemotePolicyCountOff    = 0x018 // u8 PROC_POLICY entries in use (0..48)
	RemoteFFCountOff        = 0x019 // u8 FF denylist entries in use (0..4)
	RemoteCreateCooldownOff = 0x01A // u16 frames after a failed body create
	RemoteFullAllowOff      = 0x01C // u8 bit i: slot i may begin one daPy_lk_c create (RemoteFlagFullBodies only; Go heap gate)
	RemoteHeapReasonOff     = 0x01D // u8 RemoteHG* why the gate is shut (REL: wait vs fall back only)
	RemoteFlags2Off         = 0x01E // u16 RemoteFlag2*; 0 = today
	RemoteSlotsOff          = 0x020 // slot[4] x RemoteSlotSize
	RemoteSlotSize          = 0x30
	RemoteApplyOff          = 0x0E0 // client apply record (RemoteAP*)
	RemoteApplySize         = 0x28
	RemoteFFDenyOff         = 0x108 // char[8] x RemoteFFDenyMax
	RemotePolicyOff         = 0x128 // PROC_POLICY[48] x RemotePolicySize
	RemotePolicySize        = 4
	RemoteSeqTailOff        = 0x1E8 // u32 = seq_head, the last Go-written word
	RemoteGoSpan            = 0x1EC // bytes Go writes per tick
	RemoteUnusedOff         = 0x1EC // u32 nobody writes

	// flags (+0x00A); all clear = today's behaviour
	RemoteFlagBodies          uint16 = 0x0001 // host: create lite bodies
	RemoteFlagSwap            uint16 = 0x0002 // pointer swap for eligible procs
	RemoteFlagHittable        uint16 = 0x0004 // body Tg Set each frame
	RemoteFlagDetect          uint16 = 0x0008 // host hit ring
	RemoteFlagApply           uint16 = 0x0010 // client apply ("v2 in reverse")
	RemoteFlagAttrib          uint16 = 0x0020 // client v2 hits attributed to bodies
	RemoteFlagNameA9          uint16 = 0x0040 // transient proc name 0xA9 on bodies inside eligible executes
	RemoteFlagGuard           uint16 = 0x0080 // body Tg shield + checkPlayerGuard stub
	RemoteFlagFFGateOff              = 0x0100 // disable the FF stage denylist gate (design NO_FF inverted: clear = gate on)
	RemoteFlagRouyaGateOff           = 0x0200 // disable the isCollect jail gate (design ROUYA inverted: clear = gate on)
	RemoteFlagAttacker        uint16 = 0x0400 // attacker wins: force latch, mo2 Tg colliders, spear hit
	RemoteFlagGroupWide       uint16 = 0x0800 // ENEMY group eligible unless DENY (else ALLOW only)
	RemoteFlagBothWordsEnable uint16 = 0x1000 // honour per-proc RemotePFBothWords (also swap 0x803CA754)
	RemoteFlagIFrameStamp     uint16 = 0x2000 // stamp body_iframes on detection
	RemoteFlagFullBodies      uint16 = 0x4000 // host: bodies are the real daPy_lk_c (proc 0x27 profile) when the slot's FULL_ALLOW bit is set; else lite

	// flags2 (+0x01E); all clear = today's behaviour
	RemoteFlag2FullCo uint16 = 0x0001 // full bodies keep mCyl's Co Set bit (default: Co off)

	// heap-gate reasons (+0x01D, Go-written; the REL waits on the transient ones)
	RemoteHGNone           uint8 = 0 // gate not run (FULL_BODIES off, client, or no sample yet this session)
	RemoteHGOpen           uint8 = 1
	RemoteHGNoSample       uint8 = 2 // no fresh sample in this stage (transient)
	RemoteHGWalkProblem    uint8 = 3 // walker reported a Problem (retry next sample)
	RemoteHGGameLargest    uint8 = 4
	RemoteHGGameFree       uint8 = 5
	RemoteHGZeldaLargest   uint8 = 6
	RemoteHGCounterRose    uint8 = 7  // Game/Zelda error counter rose this stage (or solid during a FULL create)
	RemoteHGInflight       uint8 = 8  // a FULL body is QUEUED (transient)
	RemoteHGStageSettle    uint8 = 9  // stage change, fewer than 2 good samples (transient)
	RemoteHGSticky         uint8 = 10 // two stages closed by counters: shut for the session
	RemoteHGArchiveLargest uint8 = 11 // shanm table not allocated yet and the archive heap is short
	RemoteHGFullMax        uint8 = 12 // FullMax FULL bodies already LIVE
	RemoteHGQueued         uint8 = 13 // open, the bit is another slot's; this one waits its turn (transient)

	RemoteRoleHost   uint8 = 1 // = EnemyRoleHost
	RemoteRoleClient uint8 = 2 // = EnemyRoleClient

	// tunable seeds Go writes (the f32 ones as whole numbers here)
	RemoteDefMarginQ8       uint8  = 164 // ~0.64
	RemoteDefMinHold        uint8  = 30
	RemoteDefBodyIFrames    uint8  = 30  // vanilla mDam field_0x0 (d_a_player_HIO_data.inc:214-216)
	RemoteDefDYMax                 = 400 // f32
	RemoteDefCreateCooldown uint16 = 30
	RemoteDefEyeDY                 = 110 // f32, tuned in Step 1 from host Link +0x260 - +0x1F8
	RemoteDefCylR                  = 30  // f32
	RemoteDefCylH                  = 120 // f32

	// slot fields, from the slot start
	RemoteSLActiveOff      = 0x00 // u8 1 = this slot has a remote
	RemoteSLSFlagsOff      = 0x01 // u8 RemoteSF*
	RemoteSLRoomOff        = 0x02 // s8 the remote's room
	RemoteSLCutTypeOff     = 0x03 // u8 remote Link +0x290 (d_a_player.h:479)
	RemoteSLCutCountOff    = 0x04 // u8 remote Link +0x291
	RemoteSLPlayerIDOff    = 0x05 // u8 server player id (diagnostic)
	RemoteSLIFramesOff     = 0x06 // s16 remote mDamageWaitTimer (+0x294) from PSTATE
	RemoteSLStageOff       = 0x08 // char[8] the remote's stage
	RemoteSLPosOff         = 0x10 // f32 x, y, z (Go-smoothed, as the mini-Link)
	RemoteSLAngleOff       = 0x1C // s16 angle.y
	RemoteSLShieldAngleOff = 0x1E // s16 shield_angle_y
	RemoteSLEyeDYOff       = 0x20 // f32 eyePos = pos + (0, eye_dy, 0)
	RemoteSLCylROff        = 0x24 // f32 body Tg cylinder radius
	RemoteSLCylHOff        = 0x28 // f32 body Tg cylinder height
	RemoteSLPoseSlotOff    = 0x2C // u8 pose-feed slot (index into mailbox +0xA8/+0xB4/+0xB6)
	RemoteSLGenOff         = 0x2D // u8 bumped by Go when the slot changes owner: body recreated
	RemoteSLPadOff         = 0x2E // u16 0

	RemoteSFTargetable uint8 = 0x01
	RemoteSFGuard      uint8 = 0x02
	RemoteSFNoHit      uint8 = 0x04
	RemoteSFCrouchDef  uint8 = 0x08
	RemoteSFDead       uint8 = 0x10
	RemoteSFInEvent    uint8 = 0x20
	RemoteSFSwim       uint8 = 0x40
	RemoteSFOnShip     uint8 = 0x80

	// PSTATE [3] bits (wire only; Go derives the slot sflags from them)
	RemotePSGuard      uint8 = 0x01 // mCurProc CROUCH_DEFENSE 0x0C or upper ATNG anime
	RemotePSCrouchDef  uint8 = 0x02
	RemotePSDamageMode uint8 = 0x04 // mModeFlg (+0x3618) DAMAGE bit
	RemotePSDead       uint8 = 0x08
	RemotePSInEvent    uint8 = 0x10
	RemotePSSwim       uint8 = 0x20
	RemotePSOnShip     uint8 = 0x40
	RemotePSLoading    uint8 = 0x80

	// client apply record (client role), from RemoteApplyOff
	RemoteAPSeqOff     = 0x00 // u32 ++ per PHIT Go accepted (the REL acks at RemoteApplyAckOff)
	RemoteAPSetIDOff   = 0x04 // u16 attacker setID (the client's mirrored Moblin)
	RemoteAPAtpOff     = 0x06 // u8 At power
	RemoteAPSeOff      = 0x07 // u8 dCcD_GObjAt mSe
	RemoteAPHitMarkOff = 0x08 // u8 dCcD_GObjAt mHitMark
	RemoteAPSplOff     = 0x09 // u8 dCcD_GObjAt mSpl
	RemoteAPAtSPrmOff  = 0x0A // u8 At SPrm, always RemoteApplyAtSPrm
	RemoteAPFlagsOff   = 0x0B // u8 RemoteAF*
	RemoteAPAtTypeOff  = 0x0C // u32 cCcD_ObjAt mType
	RemoteAPRelHitOff  = 0x10 // f32 x, y, z: hit point - body pos on the host
	RemoteAPRVecOff    = 0x1C // f32 x, y, z: the host Tg RVec

	RemoteApplyAtSPrm  uint8 = 0x05 // Set|VsPlayer: cannot hit mirrored Moblins
	RemoteAFHostShield uint8 = 0x01 // PHIT flags b0: the host saw a shield hit
	RemoteAFHostGuard  uint8 = 0x02 // PHIT flags b1: the host's guard flag for this remote

	// PROC_POLICY entry, from its start
	RemotePPProcOff  = 0x00 // s16 proc name (fpcNm_*)
	RemotePPFlagsOff = 0x02 // u8 RemotePF*
	RemotePPPadOff   = 0x03 // u8 0

	RemotePFDeny      uint8 = 0x01
	RemotePFAllow     uint8 = 0x02
	RemotePFBothWords uint8 = 0x04 // also swap 0x803CA754 (needs RemoteFlagBothWordsEnable)
	RemotePFNoLatch   uint8 = 0x08 // pure nearest, no hysteresis

	RemoteProcMo2 uint16 = 0x00BD // the only seed ALLOW (design section 3 audit)

	// REL-written span: +0x1F0..+0x330
	RemoteAckSeqOff      = 0x1F0 // u32 last valid seq consumed
	RemoteAckFrameOff    = 0x1F4 // u32 WWREL frame at that consume
	RemoteBodyStatusOff  = 0x1F8 // body_status[4] x RemoteBodyStatusSize
	RemoteBodyStatusSize = 0x10
	RemoteCountersOff    = 0x238 // u32[RemoteCounters], cumulative
	RemoteCounters       = 10
	RemoteHitRingHeadOff = 0x260 // u32 pushes so far
	RemoteHitRingOff     = 0x264 // hit[4] x RemoteHitSize
	RemoteHitRingLen     = 4
	RemoteHitSize        = 0x30
	RemoteApplyAckOff    = 0x324 // u32 apply seq consumed
	RemoteApplyResultOff = 0x328 // u32 BE: byte3 AR, byte2 AD, byte1 executes, byte0 Sets
	RemoteLifeBeforeOff  = 0x32C // u16 client Link life at submit
	RemoteLifeAfterOff   = 0x32E // u16 client Link life two executes later

	// body_status fields, from the entry start
	RemoteBSActorOff      = 0x00 // u32 body actor address (0 while unresolved or EMPTY)
	RemoteBSPIDOff        = 0x04 // u32 body pid (fopAcM_create's result)
	RemoteBSStateOff      = 0x08 // u8 RemoteBody*
	RemoteBSErrOff        = 0x09 // u8 RemoteBErr* (the last one)
	RemoteBSHitsOff       = 0x0A // u8 hits detected (saturating)
	RemoteBSLatchedByOff  = 0x0B // u8 latch entries targeting this body
	RemoteBSFramesLiveOff = 0x0C // u16 executes LIVE (saturating)
	RemoteBSTriesOff      = 0x0E // u8 creates this stage
	RemoteBSKindOff       = 0x0F // u8 RemoteKind* (was pad)

	RemoteKindLite uint8 = 0
	RemoteKindFull uint8 = 1

	RemoteBodyEmpty  uint8 = 0
	RemoteBodyQueued uint8 = 1
	RemoteBodyLive   uint8 = 2
	RemoteBodyParked uint8 = 3
	RemoteBodyDying  uint8 = 4

	RemoteBErrNone         uint8 = 0
	RemoteBErrSlot         uint8 = 1  // slot >= MAX_REMOTE_LINKS
	RemoteBErrBlock        uint8 = 2  // WWRP invalid, stale or not the host role
	RemoteBErrVtable       uint8 = 3  // __vt__9daPy_py_c / lk_c fingerprint failed
	RemoteBErrHostLink     uint8 = 4  // 0x803CA754 not an in-MEM1 0xA9, or != 0x803CA74C
	RemoteBErrCyl          uint8 = 5  // Link mCyl vptr != __vt__8dCcD_Cyl 0x8038AF08
	RemoteBErrCreate       uint8 = 6  // fopAcM_create returned 0xFFFFFFFF
	RemoteBErrLost         uint8 = 7  // creating ended and SearchByID found no 'WWRB'
	RemoteBErrTries        uint8 = 8  // 3 tries this stage spent
	RemoteBErrGone         uint8 = 9  // the pid vanished (stage teardown)
	RemoteBErrHeapDenied   uint8 = 10 // FULL wanted, allow bit clear (fell back to lite)
	RemoteBErrPhaseTimeout uint8 = 11 // full create still INIT after RemoteFullCreateFrames
	RemoteBErrHeapErrors   uint8 = 12 // GameHeapErrors or solidHeapErrors rose across playerInit
	RemoteBErrProfile      uint8 = 13 // DMC[0x27] != DMC[0xB5] or profile slot 0x27 != g_profile_WWBODY
	RemoteBErrShanmAlloc   uint8 = 14 // archive-heap shanm table alloc failed or invalid
	RemoteBErrFullVtable   uint8 = 15 // after __ct__9daPy_lk_cFv, vptr +0x31C != 0x8038FDC0

	// counters (u32 index from RemoteCountersOff)
	RemoteCtrHookCalls         = 0 // every exec hook entry, even with magic 0
	RemoteCtrSwaps             = 1
	RemoteCtrSwitches          = 2 // latch target changes
	RemoteCtrAttackerOverrides = 3
	RemoteCtrDenied            = 4 // ENEMY-group policy misses only
	RemoteCtrRouyaSkips        = 5 // jail/demo gate skips (mo2) and FF denylist skips
	RemoteCtrParkRewrites      = 6 // m714 rewrites
	RemoteCtrBothWordSwaps     = 7
	RemoteCtrVTRefusals        = 8
	RemoteCtrGateSkips         = 9 // host-Link gate skips

	// hit ring entry fields, from the entry start
	RemoteHESeqOff     = 0x00 // u16 (u16)push number, 0 = never written
	RemoteHESlotOff    = 0x02 // u8 body slot hit
	RemoteHEHFlagsOff  = 0x03 // u8 RemoteHF*
	RemoteHEPIDOff     = 0x04 // u32 attacker pid
	RemoteHESetIDOff   = 0x08 // u16 attacker setID
	RemoteHEProcOff    = 0x0A // s16 attacker proc name
	RemoteHEAtTypeOff  = 0x0C // u32 attacker cCcD_ObjAt mType (spear thrust 0x800)
	RemoteHEAtpOff     = 0x10 // u8 attacker mAtp (spear 1/2/4)
	RemoteHESeOff      = 0x11 // u8 attacker GObjAt mSe (collider +0x6C, Step -1; spear 3)
	RemoteHEHitMarkOff = 0x12 // u8 attacker GObjAt mHitMark (collider +0x6E; spear 0x0D)
	RemoteHESplOff     = 0x13 // u8 attacker GObjAt mSpl (collider +0x6F; spear 0/6/7)
	RemoteHERelHitOff  = 0x14 // f32 x, y, z: Tg HitPos (cyl+0xCC) - body pos
	RemoteHERVecOff    = 0x20 // f32 x, y, z: Tg RVec (cyl+0xC0)
	RemoteHEFrameOff   = 0x2C // u32 WWREL frame at detection

	RemoteHFShield      uint8  = 0x01  // Tg RPrm shield-hit bit (cyl+0x98 & 2)
	RemoteHFNotMo2      uint8  = 0x02  // attacker proc != RemoteProcMo2 (sent with enemysync.NoMirrorSetID)
	RemoteHFNotKeyed    uint8  = 0x04  // attacker setID >= RemoteKeyedSetIDMax (likewise)
	RemoteKeyedSetIDMax uint16 = 0x200 // = EM_KEYED_SETID_MAX (enemy_mirror.c:333)

	// apply results (byte3 of RemoteApplyResultOff; PACK [3])
	RemoteARNone       uint8 = 0 // no result yet
	RemoteARDropped    uint8 = 1 // dropped before a Set; the detail says why
	RemoteARApplied    uint8 = 2
	RemoteARGuarded    uint8 = 3 // crossed Link's Tg with its shield-hit bit
	RemoteARNoCross    uint8 = 4
	RemoteARNoMirror   uint8 = 5 // no longer published: a missing mirror takes the keeper stand-in owner
	RemoteARLinkAbsent uint8 = 6 // no client Link (0x803CA754 not an in-MEM1 0xA9)
	RemoteAREarlyOut   uint8 = 7 // crossed, but changeDamageProc took an early out

	// apply details (byte2 of RemoteApplyResultOff; PACK [6])
	RemoteADNone        uint8 = 0
	RemoteADIFrames     uint8 = 1 // Link's Tg Set bit clear (mDamageWaitTimer)
	RemoteADGoGate      uint8 = 2 // Go: epoch/stage/room mismatch (never reaches the REL)
	RemoteADNotInPlay   uint8 = 3 // Go: Link not in play
	RemoteADStaleEpoch  uint8 = 4 // Go: the PHIT's epoch is older than the block's
	RemoteADAborted     uint8 = 5 // REL: delete, epilog or magic 0 mid-request
	RemoteADClientStale uint8 = 6 // Go: the client held the PHIT past PHitClientStale (never reaches the REL)
	RemoteADBadHit      uint8 = 7 // Go: the PHIT has fields no hit has, phitSane (never reaches the REL)

	// ATTRIB extensions of the enemy block's hit_apply (wwenemy.h), section 8.6
	RemoteHAFAttribBody uint8 = 0x08 // bit in EnemyHAFlagsOff: attribute to the slot's body
	RemoteHAFSlot1      uint8 = 0x10 // bit in EnemyHAFlagsOff: body slot 1 (clear = slot 0)
	RemoteHDNoBody      uint8 = 16   // EnemyHitResultOff detail: ATTRIB asked, the body not LIVE (host-Link path ran)

	// the actor (both roles of the 0xB5 profile)
	RemoteActorSize      uint32 = 0x600      // profile mSize, max(keeper, lite body)
	RemoteKeeperSwapOff         = 0x458      // keeper swap state start (after enemy_mirror_state), ends <= 0x600
	RemoteBodyParamsTag  uint32 = 0x52000000 // body mParameters = tag | slot << 8 | gen; keeper = 0
	RemoteBodyParamsMask uint32 = 0xFF000000
	RemoteBodyMagic      uint32 = 0x57575242 // 'WWRB' at RemoteBodyMagicOff

	// lite body fields, from the body actor (section 5.2)
	RemoteBodyCutTypeOff     = 0x290 // u8 daPy_py_c mCutType image
	RemoteBodyCutCountOff    = 0x291 // u8 mCutCount image
	RemoteBodyDamageWaitOff  = 0x294 // s16 mDamageWaitTimer image (diagnostic)
	RemoteBodyGrabWearOff    = 0x2B0 // f32 0.0 (checkGrabWear false)
	RemoteBodyDemoOff        = 0x304 // daPy_demo_c, stays zero
	RemoteBodyVPtrOff        = 0x31C // daPy_py_c vptr = body + RemoteBodyVTOff
	RemoteBodyVTOff          = 0x320 // copy of __vt__9daPy_py_c, stubbed
	RemoteBodyVTSize         = 0x9C
	RemoteBodyCylOff         = 0x3C0 // dCcD_Cyl copied from Link +0x4024
	RemoteBodyCylSize        = 0x130
	RemoteBodySttsOff        = 0x4F0 // dCcD_Stts
	RemoteBodySttsSize       = 0x3C
	RemoteBodyMagicOff       = 0x530 // u32 RemoteBodyMagic
	RemoteBodySlotOff        = 0x534 // u8
	RemoteBodyGenOff         = 0x535 // u8
	RemoteBodyIFramesLeftOff = 0x536 // u8 provisional i-frames left (the body's own counter)
	RemoteBodyStateOff       = 0x537 // u8 RemoteBody* as the body sees itself
	RemoteBodyShieldAngleOff = 0x538 // s16 the cyl's mpShieldFrontRangeYAngle target
	RemoteBodyHandMtxOff     = 0x53C // Mtx 0x30, hand matrices (vt+0x08/+0x0C stubs)
	RemoteBodyLastPoseSeqOff = 0x56C // u32
	RemoteBodyParkSinceOff   = 0x570 // u32 WWREL frame parked at
	RemoteBodyEnd            = 0x574 // <= RemoteActorSize

	// the full body (RemoteFlagFullBodies; stage-4 plan D4-1, D4-6): a
	// daPy_lk_c of the REL's second profile g_profile_WWBODY in slot
	// RemoteFullProc, offsets from the body actor; the tail's first 0x48
	// bytes keep the lite body's +0x530..+0x578 field order
	RemoteFullProc            uint16 = 0x0027     // hijacked proc/profile slot (Seatag; never placed on the GZLE01 disc)
	RemoteFullNameEntry       uint32 = 0x80339938 // DynamicNameTable entry of proc 0x27 (SYM:13851 + 12*8)
	RemoteFullNamePtr         uint32 = 0x8033AE39 // "d_a_rectangle": patched into RemoteFullNameEntry + 4 (was 0x8033A6F3)
	RemoteDMCFullAddr         uint32 = 0x803B92B4 // DMC[0x27] (DMC 0x803B9218, SYM:17371)
	RemoteDMCRELAddr          uint32 = 0x803B94EC // DMC[0xB5]
	RemoteProfileListPtr      uint32 = 0x803F6A68 // g_fpcPf_ProfileList_p (SYM:18664); slot = list + 4 * proc
	RemoteFullSize            uint32 = 0x4C80     // profile mSize: sizeof(daPy_lk_c) 0x4C28 + tail 0x58
	RemoteFullDaPySize        uint32 = 0x4C28     // d_a_player_main.h:2375
	RemoteFullTailOff                = 0x4C28
	RemoteFullMagicOff               = 0x4C28     // u32 RemoteBodyMagic, written last in create
	RemoteFullSlotOff                = 0x4C2C     // u8
	RemoteFullGenOff                 = 0x4C2D     // u8
	RemoteFullIFramesLeftOff         = 0x4C2E     // u8
	RemoteFullStateOff               = 0x4C2F     // u8 RemoteBodyLive / RemoteBodyParked
	RemoteFullShieldAngleOff         = 0x4C30     // s16 slot shield angle (the live one is +0x3546)
	RemoteFullLastPoseSeqOff         = 0x4C64     // u32 (= lite +0x56C - 0x530 + tail)
	RemoteFullParkSinceOff           = 0x4C68     // u32
	RemoteFullPhaseOff               = 0x4C70     // u8 RemoteFP*
	RemoteFullKindOff                = 0x4C71     // u8 RemoteKindFull
	RemoteFullCreateFramesOff        = 0x4C72     // u16 create calls so far
	RemoteFullShanmDiffOff           = 0x4C74     // u32 shared words restored after the create windows
	RemoteFullHeapErrOff             = 0x4C78     // u32 byte3 game-errors delta, byte2 solid delta across playerInit
	RemoteFullEnd                    = 0x4C7C     // <= RemoteFullSize
	RemoteFullCreateFrames           = 120        // phase_2 gate + makeBgWait INIT retries before cPhs_ERROR_e
	RemoteFPNew               uint8  = 0          // phase_1 not run
	RemoteFPParams            uint8  = 1          // phase_1 done (params cached, then rewritten to 0xFF000040)
	RemoteFPInit              uint8  = 2          // ctor + playerInit done: delete must run playerDelete
	RemoteFPLive              uint8  = 3          // makeBgWait returned NEXT: create complete
	RemoteFullParams          uint32 = 0xFF000040 // params after phase_1: event 0xFF, mode 0, room 0, 0x40 skip setRestartRoom

	// daPy_lk_c fields the REL and the probe touch (DPMH / d_a_player.h)
	RemoteFullDamageWaitOff  = 0x294  // s16 mDamageWaitTimer (d_a_player.h:482)
	RemoteFullNoReset1Off    = 0x2A0  // u32 mNoResetFlg1 (CASUAL 0x08 cleared before delete)
	RemoteFullReset0Off      = 0x2A4  // u32 mResetFlg0 = 0 every execute
	RemoteFullVPtrOff        = 0x31C  // == 0x8038FDC0 after the ctor
	RemoteFullModelOff       = 0x32C  // J3DModel* mpCLModel (DPMH:1993)
	RemoteFullLinktexOff     = 0x338  // ResTIMG* mpCurrLinktex
	RemoteFullSttsOff        = 0x3FE8 // dCcD_Stts mStts
	RemoteFullCylOff         = 0x4024 // dCcD_Cyl mCyl (Step -1 offsets apply from here)
	RemoteFullCurProcOff     = 0x31D8 // int mCurProc (WAIT 0x04, CROUCH_DEFENSE 0x0C)
	RemoteFullShieldRangeOff = 0x3546 // s16 mShieldFrontRangeYAngle (mCyl's shield-angle pointer target)
	RemoteFullModeFlgOff     = 0x3618 // u32 mModeFlg (clear DAMAGE 0x8, IN_SHIP 0x2000, CRAWL 0x01000000)

	// shanm table header (archive block at *(u32*)(Mailbox + OffRELShanm))
	RemoteShanmMagic      uint32 = 0x57575341 // 'WWSA'
	RemoteShanmBytes      uint32 = 0x4760     // header 0x60 + joint_addr[64] + mat_addr[256] + val[4224]
	RemoteSAMagicOff             = 0x00       // u32
	RemoteSASizeOff              = 0x04       // u32 RemoteShanmBytes
	RemoteSASelfOff              = 0x08       // u32 the block's own address
	RemoteSAWordsOff             = 0x0C       // u32 words tracked at the last refresh (live ~2598, cap 4224)
	RemoteSACreateDiffOff        = 0x10       // u32 words restored after the last create window
	RemoteSADeleteDiffOff        = 0x14       // u32 words restored after the last delete window
	RemoteSADeleteScanOff        = 0x18       // u32 tracked words pointing into a deleted body's instance or heaps (must be 0)
	RemoteSAWindowsOff           = 0x1C       // u32 windows opened (cumulative)
	RemoteSAJointDiffOff         = 0x20       // u32 cl.bdl joints 0x02/0x1D mMtxCalc restores (cumulative)

	// jail gate (Step -1): the save's mCollect[0], a direct byte read
	RemoteCollectAddr     uint32 = 0x803C4CBC // wwapi/addr Collect (d_save.cpp:756-759)
	RemoteCollectJailMask uint8  = 0x0F       // none of isCollect(0, 0..3) set -> mo2 rouya mode

)

// RemoteBlockAddr is the block's address (0x80413420).
const RemoteBlockAddr = Mailbox + RemoteOff

// RemoteSlotAddr is the address of field off (a RemoteSL* offset) in slot i.
func RemoteSlotAddr(i int, off uint32) uint32 {
	return RemoteBlockAddr + RemoteSlotsOff + uint32(i)*RemoteSlotSize + off
}

// RemoteBlockRegion is the RAM remote-Link owns: from the enemy region's
// end (the 0x10 bytes before the block are alignment, owned so the slack
// still tiles) to the block's end. The region probe must not arm it
// (mod.go, CanaryArenaHigh, which starts past the knob table after it).
var RemoteBlockRegion = addr.Region{Name: "remote-Link block", Start: Mailbox + RemoteRegionStart, End: Mailbox + RemoteRegionEnd}

// RELHookRegion is the REL's exec-hook and swap-state words. A canary
// armed over the first would be branched to by T2's exec_shim.
var RELHookRegion = addr.Region{Name: "REL exec hook words", Start: Mailbox + OffRELExecHook, End: Mailbox + OffRELSwapState + 4}

// RemoteSlot is one remote player as the host REL sees it.
type RemoteSlot struct {
	Active       bool  // the slot has a remote (the REL keeps or creates its body)
	SFlags       uint8 // RemoteSF*, derived by Go (not PSTATE's RemotePS* byte)
	Room         int8
	CutType      uint8 // the remote Link's +0x290
	CutCount     uint8 // +0x291
	PlayerID     uint8 // the server's player id (diagnostic)
	IFrames      int16 // the remote's mDamageWaitTimer from PSTATE
	Stage        string
	Pos          [3]float32 // Go-smoothed, as the mini-Link
	AngleY       int16
	ShieldAngleY int16
	EyeDY        float32
	CylR         float32
	CylH         float32
	PoseSlot     uint8 // the pose-feed slot this remote renders in
	Gen          uint8 // bumped when the slot changes owner
}

// RemoteApply is the client role's apply record: one PHIT for the REL to
// replay on the client's own Link.
type RemoteApply struct {
	Seq     uint32 // ++ per PHIT Go accepted; the REL acks it at RemoteApplyAckOff
	SetID   uint16 // the attacker's setID (the client's mirrored Moblin)
	Atp     uint8
	Se      uint8
	HitMark uint8
	Spl     uint8
	AtSPrm  uint8 // RemoteApplyAtSPrm
	Flags   uint8 // RemoteAF*
	AtType  uint32
	RelHit  [3]float32 // hit point - body pos on the host
	RVec    [3]float32 // the host Tg RVec
}

// RemotePolicy is one PROC_POLICY entry.
type RemotePolicy struct {
	Proc  int16 // fpcNm_* proc name
	Flags uint8 // RemotePF*
}

// RemoteBlock is the Go-written span. Encode writes len(Policy) and
// len(FFDeny) as the two counts.
type RemoteBlock struct {
	Magic          uint32 // RemoteMagic, or 0 to turn the block off
	Seq            uint32 // written as both seq_head and seq_tail
	Layout         uint16
	Flags          uint16 // RemoteFlag*
	Epoch          uint32
	MarginQ8       uint8
	MinHold        uint8
	BodyIFrames    uint8
	Role           uint8 // RemoteRole*, the enemy block's role
	DYMax          float32
	CreateCooldown uint16
	FullAllow      uint8  // bit i: slot i may begin one full create (host, RemoteFlagFullBodies); 0 = none
	HeapReason     uint8  // RemoteHG*: why the heap gate is shut; 0 = gate not run
	Flags2         uint16 // RemoteFlag2*; 0 = today
	Slots          [RemoteMaxSlots]RemoteSlot
	Apply          RemoteApply
	FFDeny         []string       // at most RemoteFFDenyMax stage names
	Policy         []RemotePolicy // at most RemoteProcPolicyMax
}

// ErrRemoteNotFinite is Encode's error for a NaN or Inf.
var ErrRemoteNotFinite = errors.New("mailbox: NaN or Inf in the remote-Link block")

func remoteFinite(fs ...float32) error {
	for _, f := range fs {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return ErrRemoteNotFinite
		}
	}
	return nil
}

func putRemoteStage(b []byte, stage string) error {
	if len(stage) > RemoteStageLen {
		return fmt.Errorf("mailbox: remote-Link stage %q is longer than %d bytes", stage, RemoteStageLen)
	}
	copy(b[:RemoteStageLen], stage)
	return nil
}

func getRemoteStage(b []byte) string {
	n := 0
	for n < RemoteStageLen && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func putRemoteVec(b []byte, v [3]float32) {
	for k := range 3 {
		binary.BigEndian.PutUint32(b[4*k:], math.Float32bits(v[k]))
	}
}

func getRemoteVec(b []byte) (v [3]float32) {
	for k := range 3 {
		v[k] = math.Float32frombits(binary.BigEndian.Uint32(b[4*k:]))
	}
	return v
}

// Encode lays the block out as the REL reads it: RemoteGoSpan bytes with
// seq_head == seq_tail == b.Seq. It refuses more than RemoteFFDenyMax FF
// names or RemoteProcPolicyMax policies, a stage name over RemoteStageLen
// bytes and a NaN or Inf.
func (b *RemoteBlock) Encode() ([]byte, error) {
	if len(b.FFDeny) > RemoteFFDenyMax {
		return nil, fmt.Errorf("mailbox: %d FF denylist stages (max %d)", len(b.FFDeny), RemoteFFDenyMax)
	}
	if len(b.Policy) > RemoteProcPolicyMax {
		return nil, fmt.Errorf("mailbox: %d PROC_POLICY entries (max %d)", len(b.Policy), RemoteProcPolicyMax)
	}
	if err := remoteFinite(b.DYMax); err != nil {
		return nil, fmt.Errorf("dy_max: %w", err)
	}
	out := make([]byte, RemoteGoSpan)
	be := binary.BigEndian
	be.PutUint32(out[RemoteMagicOff:], b.Magic)
	be.PutUint32(out[RemoteSeqHeadOff:], b.Seq)
	be.PutUint16(out[RemoteLayoutOff:], b.Layout)
	be.PutUint16(out[RemoteFlagsOff:], b.Flags)
	be.PutUint32(out[RemoteEpochOff:], b.Epoch)
	out[RemoteMarginQ8Off] = b.MarginQ8
	out[RemoteMinHoldOff] = b.MinHold
	out[RemoteBodyIFramesOff] = b.BodyIFrames
	out[RemoteRoleOff] = b.Role
	be.PutUint32(out[RemoteDYMaxOff:], math.Float32bits(b.DYMax))
	out[RemotePolicyCountOff] = byte(len(b.Policy))
	out[RemoteFFCountOff] = byte(len(b.FFDeny))
	be.PutUint16(out[RemoteCreateCooldownOff:], b.CreateCooldown)
	out[RemoteFullAllowOff] = b.FullAllow
	out[RemoteHeapReasonOff] = b.HeapReason
	be.PutUint16(out[RemoteFlags2Off:], b.Flags2)
	for i := range b.Slots {
		s := &b.Slots[i]
		if err := remoteFinite(s.Pos[0], s.Pos[1], s.Pos[2], s.EyeDY, s.CylR, s.CylH); err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
		o := out[RemoteSlotsOff+i*RemoteSlotSize:]
		if s.Active {
			o[RemoteSLActiveOff] = 1
		}
		o[RemoteSLSFlagsOff] = s.SFlags
		o[RemoteSLRoomOff] = byte(s.Room)
		o[RemoteSLCutTypeOff] = s.CutType
		o[RemoteSLCutCountOff] = s.CutCount
		o[RemoteSLPlayerIDOff] = s.PlayerID
		be.PutUint16(o[RemoteSLIFramesOff:], uint16(s.IFrames))
		if err := putRemoteStage(o[RemoteSLStageOff:], s.Stage); err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
		putRemoteVec(o[RemoteSLPosOff:], s.Pos)
		be.PutUint16(o[RemoteSLAngleOff:], uint16(s.AngleY))
		be.PutUint16(o[RemoteSLShieldAngleOff:], uint16(s.ShieldAngleY))
		be.PutUint32(o[RemoteSLEyeDYOff:], math.Float32bits(s.EyeDY))
		be.PutUint32(o[RemoteSLCylROff:], math.Float32bits(s.CylR))
		be.PutUint32(o[RemoteSLCylHOff:], math.Float32bits(s.CylH))
		o[RemoteSLPoseSlotOff] = s.PoseSlot
		o[RemoteSLGenOff] = s.Gen
	}
	a := &b.Apply
	if err := remoteFinite(a.RelHit[0], a.RelHit[1], a.RelHit[2], a.RVec[0], a.RVec[1], a.RVec[2]); err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	o := out[RemoteApplyOff:]
	be.PutUint32(o[RemoteAPSeqOff:], a.Seq)
	be.PutUint16(o[RemoteAPSetIDOff:], a.SetID)
	o[RemoteAPAtpOff] = a.Atp
	o[RemoteAPSeOff] = a.Se
	o[RemoteAPHitMarkOff] = a.HitMark
	o[RemoteAPSplOff] = a.Spl
	o[RemoteAPAtSPrmOff] = a.AtSPrm
	o[RemoteAPFlagsOff] = a.Flags
	be.PutUint32(o[RemoteAPAtTypeOff:], a.AtType)
	putRemoteVec(o[RemoteAPRelHitOff:], a.RelHit)
	putRemoteVec(o[RemoteAPRVecOff:], a.RVec)
	for i, name := range b.FFDeny {
		if err := putRemoteStage(out[RemoteFFDenyOff+i*RemoteStageLen:], name); err != nil {
			return nil, fmt.Errorf("FF denylist %d: %w", i, err)
		}
	}
	for i, p := range b.Policy {
		o := out[RemotePolicyOff+i*RemotePolicySize:]
		be.PutUint16(o[RemotePPProcOff:], uint16(p.Proc))
		o[RemotePPFlagsOff] = p.Flags
	}
	be.PutUint32(out[RemoteSeqTailOff:], b.Seq)
	return out, nil
}

// DecodeRemoteBlock parses the Go-written span back (dump probes; tests).
// b must be at least RemoteGoSpan bytes. SeqTail is returned apart so a
// torn block shows; the two counts are clamped to their maxima.
func DecodeRemoteBlock(b []byte) (blk RemoteBlock, seqTail uint32, err error) {
	if len(b) < RemoteGoSpan {
		return blk, 0, fmt.Errorf("mailbox: %d bytes, the remote-Link block's Go span is %d", len(b), RemoteGoSpan)
	}
	be := binary.BigEndian
	blk.Magic = be.Uint32(b[RemoteMagicOff:])
	blk.Seq = be.Uint32(b[RemoteSeqHeadOff:])
	blk.Layout = be.Uint16(b[RemoteLayoutOff:])
	blk.Flags = be.Uint16(b[RemoteFlagsOff:])
	blk.Epoch = be.Uint32(b[RemoteEpochOff:])
	blk.MarginQ8 = b[RemoteMarginQ8Off]
	blk.MinHold = b[RemoteMinHoldOff]
	blk.BodyIFrames = b[RemoteBodyIFramesOff]
	blk.Role = b[RemoteRoleOff]
	blk.DYMax = math.Float32frombits(be.Uint32(b[RemoteDYMaxOff:]))
	blk.CreateCooldown = be.Uint16(b[RemoteCreateCooldownOff:])
	blk.FullAllow = b[RemoteFullAllowOff]
	blk.HeapReason = b[RemoteHeapReasonOff]
	blk.Flags2 = be.Uint16(b[RemoteFlags2Off:])
	for i := range blk.Slots {
		o := b[RemoteSlotsOff+i*RemoteSlotSize:]
		blk.Slots[i] = RemoteSlot{
			Active:       o[RemoteSLActiveOff] != 0,
			SFlags:       o[RemoteSLSFlagsOff],
			Room:         int8(o[RemoteSLRoomOff]),
			CutType:      o[RemoteSLCutTypeOff],
			CutCount:     o[RemoteSLCutCountOff],
			PlayerID:     o[RemoteSLPlayerIDOff],
			IFrames:      int16(be.Uint16(o[RemoteSLIFramesOff:])),
			Stage:        getRemoteStage(o[RemoteSLStageOff:]),
			Pos:          getRemoteVec(o[RemoteSLPosOff:]),
			AngleY:       int16(be.Uint16(o[RemoteSLAngleOff:])),
			ShieldAngleY: int16(be.Uint16(o[RemoteSLShieldAngleOff:])),
			EyeDY:        math.Float32frombits(be.Uint32(o[RemoteSLEyeDYOff:])),
			CylR:         math.Float32frombits(be.Uint32(o[RemoteSLCylROff:])),
			CylH:         math.Float32frombits(be.Uint32(o[RemoteSLCylHOff:])),
			PoseSlot:     o[RemoteSLPoseSlotOff],
			Gen:          o[RemoteSLGenOff],
		}
	}
	o := b[RemoteApplyOff:]
	blk.Apply = RemoteApply{
		Seq:     be.Uint32(o[RemoteAPSeqOff:]),
		SetID:   be.Uint16(o[RemoteAPSetIDOff:]),
		Atp:     o[RemoteAPAtpOff],
		Se:      o[RemoteAPSeOff],
		HitMark: o[RemoteAPHitMarkOff],
		Spl:     o[RemoteAPSplOff],
		AtSPrm:  o[RemoteAPAtSPrmOff],
		Flags:   o[RemoteAPFlagsOff],
		AtType:  be.Uint32(o[RemoteAPAtTypeOff:]),
		RelHit:  getRemoteVec(o[RemoteAPRelHitOff:]),
		RVec:    getRemoteVec(o[RemoteAPRVecOff:]),
	}
	for i := range min(int(b[RemoteFFCountOff]), RemoteFFDenyMax) {
		blk.FFDeny = append(blk.FFDeny, getRemoteStage(b[RemoteFFDenyOff+i*RemoteStageLen:]))
	}
	for i := range min(int(b[RemotePolicyCountOff]), RemoteProcPolicyMax) {
		o := b[RemotePolicyOff+i*RemotePolicySize:]
		blk.Policy = append(blk.Policy, RemotePolicy{Proc: int16(be.Uint16(o[RemotePPProcOff:])), Flags: o[RemotePPFlagsOff]})
	}
	return blk, be.Uint32(b[RemoteSeqTailOff:]), nil
}

// WriteRemoteBlock encodes b and writes it at the block in one write.
func WriteRemoteBlock(m Writer, b *RemoteBlock) error {
	out, err := b.Encode()
	if err != nil {
		return err
	}
	if err := m.Write(RemoteBlockAddr, out); err != nil {
		return fmt.Errorf("mailbox: remote-Link block: %w", err)
	}
	return nil
}

// ClearRemoteBlock writes magic 0: the next REL frames park and delete the
// bodies and stop swapping. It is what a pipeline writes as it stops.
func ClearRemoteBlock(m Writer) error {
	if err := m.Write(RemoteBlockAddr+RemoteMagicOff, []byte{0, 0, 0, 0}); err != nil {
		return fmt.Errorf("mailbox: remote-Link block magic: %w", err)
	}
	return nil
}

// RemoteBodyStatus is one slot's REL-written body status.
type RemoteBodyStatus struct {
	Actor      uint32 // body actor address, 0 while unresolved or empty
	PID        uint32
	State      uint8 // RemoteBody*
	Err        uint8 // RemoteBErr*, the last one
	Hits       uint8 // hits detected (saturating)
	LatchedBy  uint8 // latch entries targeting this body
	FramesLive uint16
	Tries      uint8 // creates this stage
	Kind       uint8 // RemoteKind*: lite (0) or the full daPy_lk_c body
}

// RemoteHit is one hit ring entry: an enemy At that crossed a body's Tg.
type RemoteHit struct {
	Seq     uint16 // (u16) push number; 0 = never written
	Slot    uint8
	HFlags  uint8 // RemoteHF*
	PID     uint32
	SetID   uint16
	Proc    int16
	AtType  uint32
	Atp     uint8
	Se      uint8
	HitMark uint8
	Spl     uint8
	RelHit  [3]float32
	RVec    [3]float32
	Frame   uint32
}

// RemoteApplyResult is the apply result word: what the client REL did with
// the apply record RemoteStatus.ApplyAck names.
type RemoteApplyResult struct {
	Result uint8 // RemoteAR*
	Detail uint8 // RemoteAD*
	Frames uint8 // REL executes from the first Set to the result
	Sets   uint8 // cCcS::Set calls issued
}

// RemoteStatus is the REL-written span, +RemoteAckSeqOff..+RemoteSize.
type RemoteStatus struct {
	AckSeq      uint32
	AckFrame    uint32
	Bodies      [RemoteMaxSlots]RemoteBodyStatus
	Counters    [RemoteCounters]uint32 // RemoteCtr* index
	HitRingHead uint32                 // pushes so far
	Hits        [RemoteHitRingLen]RemoteHit
	ApplyAck    uint32
	ApplyResult RemoteApplyResult
	LifeBefore  uint16
	LifeAfter   uint16
}

// remoteStatusLen is the REL-written span's size.
const remoteStatusLen = RemoteSize - RemoteAckSeqOff

// RemoteCounterNames names the counters by RemoteCtr* index, for logs.
var RemoteCounterNames = [RemoteCounters]string{
	"hook_calls", "swaps", "switches", "attacker_overrides", "denied",
	"rouya_skips", "park_rewrites", "both_word_swaps", "vt_refusals", "gate_skips",
}

// HitsSince returns the ring entries pushed after push number last, oldest
// first: at most RemoteHitRingLen, and only those the REL has not
// overwritten since (entry seq == (u16) push number), so a reader more than
// a ring behind gets the newest ones and the gap shows in their seqs.
func (st *RemoteStatus) HitsSince(last uint32) []RemoteHit {
	head := st.HitRingHead
	if head <= last {
		return nil
	}
	first := last + 1
	if head-last > RemoteHitRingLen {
		first = head - RemoteHitRingLen + 1
	}
	var out []RemoteHit
	for n := first; n <= head; n++ {
		if e := st.Hits[(n-1)%RemoteHitRingLen]; e.Seq == uint16(n) {
			out = append(out, e)
		}
	}
	return out
}

// DecodeRemoteStatus parses the REL-written span from b, which must hold
// at least remoteStatusLen bytes starting at +RemoteAckSeqOff.
func DecodeRemoteStatus(b []byte) (RemoteStatus, error) {
	var st RemoteStatus
	if len(b) < int(remoteStatusLen) {
		return st, fmt.Errorf("mailbox: %d bytes, the remote-Link block's REL span is %d", len(b), remoteStatusLen)
	}
	be := binary.BigEndian
	at := func(off int) []byte { return b[off-RemoteAckSeqOff:] }
	st.AckSeq = be.Uint32(at(RemoteAckSeqOff))
	st.AckFrame = be.Uint32(at(RemoteAckFrameOff))
	for i := range st.Bodies {
		o := at(RemoteBodyStatusOff + i*RemoteBodyStatusSize)
		st.Bodies[i] = RemoteBodyStatus{
			Actor:      be.Uint32(o[RemoteBSActorOff:]),
			PID:        be.Uint32(o[RemoteBSPIDOff:]),
			State:      o[RemoteBSStateOff],
			Err:        o[RemoteBSErrOff],
			Hits:       o[RemoteBSHitsOff],
			LatchedBy:  o[RemoteBSLatchedByOff],
			FramesLive: be.Uint16(o[RemoteBSFramesLiveOff:]),
			Tries:      o[RemoteBSTriesOff],
			Kind:       o[RemoteBSKindOff],
		}
	}
	for i := range st.Counters {
		st.Counters[i] = be.Uint32(at(RemoteCountersOff + 4*i))
	}
	st.HitRingHead = be.Uint32(at(RemoteHitRingHeadOff))
	for i := range st.Hits {
		o := at(RemoteHitRingOff + i*RemoteHitSize)
		st.Hits[i] = RemoteHit{
			Seq:     be.Uint16(o[RemoteHESeqOff:]),
			Slot:    o[RemoteHESlotOff],
			HFlags:  o[RemoteHEHFlagsOff],
			PID:     be.Uint32(o[RemoteHEPIDOff:]),
			SetID:   be.Uint16(o[RemoteHESetIDOff:]),
			Proc:    int16(be.Uint16(o[RemoteHEProcOff:])),
			AtType:  be.Uint32(o[RemoteHEAtTypeOff:]),
			Atp:     o[RemoteHEAtpOff],
			Se:      o[RemoteHESeOff],
			HitMark: o[RemoteHEHitMarkOff],
			Spl:     o[RemoteHESplOff],
			RelHit:  getRemoteVec(o[RemoteHERelHitOff:]),
			RVec:    getRemoteVec(o[RemoteHERVecOff:]),
			Frame:   be.Uint32(o[RemoteHEFrameOff:]),
		}
	}
	st.ApplyAck = be.Uint32(at(RemoteApplyAckOff))
	r := at(RemoteApplyResultOff)
	st.ApplyResult = RemoteApplyResult{Result: r[0], Detail: r[1], Frames: r[2], Sets: r[3]}
	st.LifeBefore = be.Uint16(at(RemoteLifeBeforeOff))
	st.LifeAfter = be.Uint16(at(RemoteLifeAfterOff))
	return st, nil
}

// Encode lays the REL-written span out as the REL writes it
// (remoteStatusLen bytes from +RemoteAckSeqOff). It is for fakes and
// tests; nothing writes a live game's span but the REL.
func (st *RemoteStatus) Encode() []byte {
	b := make([]byte, remoteStatusLen)
	be := binary.BigEndian
	at := func(off int) []byte { return b[off-RemoteAckSeqOff:] }
	be.PutUint32(at(RemoteAckSeqOff), st.AckSeq)
	be.PutUint32(at(RemoteAckFrameOff), st.AckFrame)
	for i, s := range st.Bodies {
		o := at(RemoteBodyStatusOff + i*RemoteBodyStatusSize)
		be.PutUint32(o[RemoteBSActorOff:], s.Actor)
		be.PutUint32(o[RemoteBSPIDOff:], s.PID)
		o[RemoteBSStateOff] = s.State
		o[RemoteBSErrOff] = s.Err
		o[RemoteBSHitsOff] = s.Hits
		o[RemoteBSLatchedByOff] = s.LatchedBy
		be.PutUint16(o[RemoteBSFramesLiveOff:], s.FramesLive)
		o[RemoteBSTriesOff] = s.Tries
		o[RemoteBSKindOff] = s.Kind
	}
	for i, c := range st.Counters {
		be.PutUint32(at(RemoteCountersOff+4*i), c)
	}
	be.PutUint32(at(RemoteHitRingHeadOff), st.HitRingHead)
	for i, h := range st.Hits {
		o := at(RemoteHitRingOff + i*RemoteHitSize)
		be.PutUint16(o[RemoteHESeqOff:], h.Seq)
		o[RemoteHESlotOff] = h.Slot
		o[RemoteHEHFlagsOff] = h.HFlags
		be.PutUint32(o[RemoteHEPIDOff:], h.PID)
		be.PutUint16(o[RemoteHESetIDOff:], h.SetID)
		be.PutUint16(o[RemoteHEProcOff:], uint16(h.Proc))
		be.PutUint32(o[RemoteHEAtTypeOff:], h.AtType)
		o[RemoteHEAtpOff] = h.Atp
		o[RemoteHESeOff] = h.Se
		o[RemoteHEHitMarkOff] = h.HitMark
		o[RemoteHESplOff] = h.Spl
		putRemoteVec(o[RemoteHERelHitOff:], h.RelHit)
		putRemoteVec(o[RemoteHERVecOff:], h.RVec)
		be.PutUint32(o[RemoteHEFrameOff:], h.Frame)
	}
	be.PutUint32(at(RemoteApplyAckOff), st.ApplyAck)
	r := st.ApplyResult
	copy(at(RemoteApplyResultOff), []byte{r.Result, r.Detail, r.Frames, r.Sets})
	be.PutUint16(at(RemoteLifeBeforeOff), st.LifeBefore)
	be.PutUint16(at(RemoteLifeAfterOff), st.LifeAfter)
	return b
}

// ReadRemoteStatus reads the REL-written span from the game.
func ReadRemoteStatus(m Reader) (RemoteStatus, error) {
	b, err := m.Read(RemoteBlockAddr+RemoteAckSeqOff, int(remoteStatusLen))
	if err != nil {
		return RemoteStatus{}, fmt.Errorf("mailbox: remote-Link status: %w", err)
	}
	if len(b) != int(remoteStatusLen) {
		return RemoteStatus{}, fmt.Errorf("mailbox: remote-Link status: %d bytes, want %d", len(b), remoteStatusLen)
	}
	return DecodeRemoteStatus(b)
}

// ReadRemoteBlock reads the whole block: the Go span decoded, its tail
// seq, and the REL span.
func ReadRemoteBlock(m Reader) (blk RemoteBlock, seqTail uint32, st RemoteStatus, err error) {
	b, err := m.Read(RemoteBlockAddr, int(RemoteSize))
	if err != nil {
		return blk, 0, st, fmt.Errorf("mailbox: remote-Link block: %w", err)
	}
	if len(b) != int(RemoteSize) {
		return blk, 0, st, fmt.Errorf("mailbox: remote-Link block: %d bytes, want %d", len(b), RemoteSize)
	}
	if blk, seqTail, err = DecodeRemoteBlock(b); err != nil {
		return blk, 0, st, err
	}
	st, err = DecodeRemoteStatus(b[RemoteAckSeqOff:])
	return blk, seqTail, st, err
}

// RELHook is what the REL's remote-Link words say.
type RELHook struct {
	ExecHook  uint32 // &wwrel_exec_hook, 0 when the module is not linked
	SwapState uint32 // the keeper's swap state, 0 without a keeper
}

// ReadRELHook reads the REL's exec-hook and swap-state words. Go never
// writes them.
func ReadRELHook(m Reader) (RELHook, error) {
	b, err := m.Read(Mailbox+OffRELExecHook, int(relHookWordsLen))
	if err != nil {
		return RELHook{}, fmt.Errorf("mailbox: REL hook words: %w", err)
	}
	if len(b) != int(relHookWordsLen) {
		return RELHook{}, fmt.Errorf("mailbox: REL hook words: %d bytes, want %d", len(b), relHookWordsLen)
	}
	return RELHook{ExecHook: binary.BigEndian.Uint32(b), SwapState: binary.BigEndian.Uint32(b[OffRELSwapState-OffRELExecHook:])}, nil
}
