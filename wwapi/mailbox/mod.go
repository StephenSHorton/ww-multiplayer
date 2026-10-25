// Package mailbox holds the mod's own RAM layout: T2, the Go <-> C
// mailbox and the regions around them. The mailbox's address, size and
// field offsets are generated from inject/include/mailbox_core.h into
// layout_gen.go (tools/gen_mailbox); the rest of this file is
// hand-written.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package mailbox

//go:generate go run ../../tools/gen_mailbox

import "github.com/StephenSHorton/ww-multiplayer/wwapi/addr"

// The mod's own RAM, between the end of the vanilla DOL image and the
// patched arena (§4.5). None of these come from the decomp: they are set
// by inject/build.py and inject/include/mailbox_core.h.
const (
	// T2Base is where the patcher's appended text section loads
	// (inject/build.py:109). Its end moves with every blob regen, so it is
	// read from len(inject.T2Bytes), never hard-coded.
	T2Base uint32 = 0x80410000

	// The Go <-> C mailbox itself, Mailbox (MAILBOX_ADDR) and
	// MailboxSize, is generated into layout_gen.go from mailbox_core.h.

	// SpawnTrigger is mailbox.spawn_trigger, which multiplayer_update
	// bumps once per frame: the co-op heartbeat (§3.2). It stalls while
	// emulation is paused and never moves on a vanilla ISO.
	SpawnTrigger = Mailbox + OffSpawnTrigger

	// WarpSeq is mailbox.warp_seq.
	WarpSeq = Mailbox + OffWarpSeq

	// PatchedArenaLo is the __OSArenaLo the patched OSInit computes
	// (addi r3,r3,0x4000 at 0x80301820, inject/build.py:144).
	PatchedArenaLo uint32 = 0x80414000
)

// Memory the M0 region probe arms with canaries and checks after boot,
// menus and stage transitions (§4.5 option B, §11 item 5). Both are free
// only by inference, which is what the canaries test.
var (
	// MainStack is the game's main stack, growing down from its top.
	// OSClearStack zeroes below the stack pointer (OSThread.c:924-933),
	// so nothing of ours may live here. The bounds are the end of .sbss2
	// (splits.txt:3328) plus 64 KiB; the top is _stack_addr, loaded into
	// r1 by __start (__start.c:125-126), and is inferred.
	MainStack = addr.Region{Name: "main stack", Start: 0x803FCFA8, End: 0x8040CFA8}

	// MainStackHeader is the 8-byte frame header just above the stack
	// top, which boot writes. __start calls __init_hardware with r1 =
	// _stack_addr before it pushes a frame (__start.c:21-23,125-126);
	// __init_hardware is nofralloc, so __OSPSInit and __OSCacheInit save
	// LR at 4(r1) (__start.c:173-183; OS.c:623-624; OSCache.c:446).
	// Nothing clears the word afterwards: OSClearStack stops below SP.
	MainStackHeader = addr.Region{Name: "main stack header", Start: 0x8040CFA8, End: 0x8040CFB0}

	// CanaryTRK is the MetroTRK debug stack plus the gap up to T2, above
	// the stack header. The vanilla __ArenaLo was 0x8040EFC0
	// (inject/build.py:104); the patched OSInit starts the arena past the
	// mod instead, so this is outside the arena. InitMetroTRK only runs
	// on debug boot info (§4.5).
	CanaryTRK = addr.Region{Name: "TRK stack", Start: MainStackHeader.End, End: T2Base}

	// CanaryArena and CanaryArenaHigh are the slack between the mailbox
	// and the patched arena, less what the mod writes there. Between them
	// lie the REL's exec-hook words (remote.go, RELHookRegion: T2's
	// exec_shim branches to the first, so a canary there would be
	// executed), the enemy-mirror region (enemy.go, EnemyBlockRegion: the
	// block and the host REL's static collision objects past it, to
	// EnemyRegionEnd), the remote-Link block (remote.go,
	// RemoteBlockRegion, to RemoteRegionEnd), layout 4's enemy knob
	// table right after it (enemy.go, EnemyKnobRegion, +0x850..+0x8B0;
	// batch C layout4.knob_table.canaries) and batch D's actor_refresh
	// request after that (refresh.go, RefreshRegion, +0x8B0..+0x8D0, G-D5;
	// P0-L5 proved it free before the move) and the remote players'
	// visuals block after that (vis.go, VisRegion, +0x8D0..+0x9E4: T2
	// branches through its first two words) and the peer boats block after
	// that (boat.go, BoatRegion, +0x9E4..+0xA6C), and CanaryArenaHigh stops at
	// the mini-Links' eye packets, below the projectile events block (fx.go,
	// FxRegion, +0xE50..+0x1100, the top of the slack). Go writes the blocks every
	// tick and the REL every frame while they run, so a canary armed over
	// them would be reported changed by the mod's own traffic, and arming
	// would tear a block the REL is reading. The region probe arms and
	// checks the two parts and skips the rest; the REL's shanm word
	// (OffRELShanm, +0x1C4) goes unprobed, and the 0x38 bytes after it, up
	// to the enemy block (+0x1C8..+0x200), are the enemy block's annex
	// (layout 3), inside EnemyBlockRegion.
	// CanaryArena still holds the REL module's first three words
	// (OffRELMagic..), which only a linked module writes. CanaryArenaHigh
	// stops at the mini-Links' eye packets (EyePacketsRegion, +0xD00..
	// +0xE50 since the wave-3 in-game batch), which T2 writes at boot and
	// the game's draw lists every frame.
	CanaryArena     = addr.Region{Name: "below arena", Start: Mailbox + MailboxSize, End: Mailbox + OffRELExecHook}
	CanaryArenaHigh = addr.Region{Name: "below arena (past the boats block)", Start: Mailbox + BoatOff + BoatSize, End: Mailbox + MpEyePacketsOff}

	// EyePacketsRegion is the mini-Links' copies of the eye-decal preset
	// packets, MAX_REMOTE_LINKS sets of four J3DPackets (mailbox_mp.h
	// MP_EYE_PACKETS_*, inject/src/mp/minilink.c), right below FxRegion.
	EyePacketsRegion = addr.Region{Name: "mini-Link eye packets", Start: Mailbox + MpEyePacketsOff, End: Mailbox + MpEyePacketsOff + MpEyePacketsSize}
)

const (
	// BootLRSlot is the LR save word in MainStackHeader, and BootLR what
	// a booted game leaves there: the return from bl __OSCacheInit, the
	// sixth instruction of __init_hardware (0x80003354, sym:5;
	// __start.c:175-180).
	BootLRSlot uint32 = 0x8040CFAC
	BootLR     uint32 = 0x8000336C
)
