package addr

// The mod's own RAM, between the end of the vanilla DOL image and the
// patched arena (§4.5). None of these come from the decomp: they are set
// by inject/build.py and inject/include/mailbox.h.
const (
	// T2Base is where the patcher's appended text section loads
	// (inject/build.py:109). Its end moves with every blob regen, so it is
	// read from len(inject.T2Bytes), never hard-coded.
	T2Base uint32 = 0x80410000

	// Mailbox is the Go <-> C mailbox (MAILBOX_ADDR, mailbox.h:33; the
	// mailboxBase const in main.go's mailbox layout block). Its last field
	// is tunic_swaps, the u32 at +0x19C (mailbox.h, "TUNIC COLORS");
	// TestMailboxLayout (package main) pins this size to the C struct.
	Mailbox     uint32 = 0x80412F00
	MailboxSize        = 0x1A0

	// SpawnTrigger is mailbox.spawn_trigger, which multiplayer_update
	// bumps once per frame (mailbox.h:109): the co-op heartbeat (§3.2).
	// It stalls while emulation is paused and never moves on a vanilla ISO.
	SpawnTrigger uint32 = 0x80412F00

	// WarpSeq is mailbox.warp_seq (mailbox.h:221).
	WarpSeq uint32 = 0x80412FC8

	// PatchedArenaLo is the __OSArenaLo the patched OSInit computes
	// (addi r3,r3,0x4000 at 0x80301820, inject/build.py:144).
	PatchedArenaLo uint32 = 0x80414000
)

// Region is a half-open range [Start, End) of MEM1.
type Region struct {
	Name       string
	Start, End uint32
}

// Size is End - Start.
func (r Region) Size() uint32 { return r.End - r.Start }

// Contains reports whether [a, a+n) lies inside r.
func (r Region) Contains(a uint32, n int) bool {
	return n >= 0 && a >= r.Start && uint64(a)+uint64(n) <= uint64(r.End)
}

// Memory the M0 region probe arms with canaries and checks after boot,
// menus and stage transitions (§4.5 option B, §11 item 5). Both are free
// only by inference, which is what the canaries test.
var (
	// MainStack is the game's main stack, growing down from its top.
	// OSClearStack zeroes below the stack pointer (OSThread.c:924-933),
	// so nothing of ours may live here. The bounds are the end of .sbss2
	// (splits.txt:3328) plus 64 KiB; the top is _stack_addr, loaded into
	// r1 by __start (__start.c:125-126), and is inferred.
	MainStack = Region{Name: "main stack", Start: 0x803FCFA8, End: 0x8040CFA8}

	// MainStackHeader is the 8-byte frame header just above the stack
	// top, which boot writes. __start calls __init_hardware with r1 =
	// _stack_addr before it pushes a frame (__start.c:21-23,125-126);
	// __init_hardware is nofralloc, so __OSPSInit and __OSCacheInit save
	// LR at 4(r1) (__start.c:173-183; OS.c:623-624; OSCache.c:446).
	// Nothing clears the word afterwards: OSClearStack stops below SP.
	MainStackHeader = Region{Name: "main stack header", Start: 0x8040CFA8, End: 0x8040CFB0}

	// CanaryTRK is the MetroTRK debug stack plus the gap up to T2, above
	// the stack header. The vanilla __ArenaLo was 0x8040EFC0
	// (inject/build.py:104); the patched OSInit starts the arena past the
	// mod instead, so this is outside the arena. InitMetroTRK only runs
	// on debug boot info (§4.5).
	CanaryTRK = Region{Name: "TRK stack", Start: MainStackHeader.End, End: T2Base}

	// CanaryArena is the slack between the mailbox and the patched
	// arena.
	CanaryArena = Region{Name: "below arena", Start: Mailbox + MailboxSize, End: PatchedArenaLo}
)

const (
	// BootLRSlot is the LR save word in MainStackHeader, and BootLR what
	// a booted game leaves there: the return from bl __OSCacheInit, the
	// sixth instruction of __init_hardware (0x80003354, sym:5;
	// __start.c:175-180).
	BootLRSlot uint32 = 0x8040CFAC
	BootLR     uint32 = 0x8000336C
)
