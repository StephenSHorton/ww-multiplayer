# Dolphin Memory Access

## The Process Model

Dolphin is a normal process. When it runs a GameCube game, it allocates a block of virtual memory to hold the emulated GameCube's 24MB of RAM. External tools (like us) can read/write this memory. The scanner (`wwapi/emu/memory.go`) is the same on every OS; only the backend differs: `ReadProcessMemory` / `WriteProcessMemory` on Windows (`memory_windows.go`), `task_for_pid` / `mach_vm` on macOS (`memory_darwin.go`, which needs an unhardened Dolphin copy and the cs.debugger entitlement; see docs/05, "macOS: User-Copy Dolphin Silently Replaced by /Applications/"), and `memory_other.go` elsewhere. The rest of this page describes the Windows case.

## Finding the Emulated RAM

The tricky part: Dolphin's emulated RAM base address changes every launch. On a 64-bit system, it's typically a high address like `0x7FFFxxxxxxxx` or `0x1xxxxxxxxxx`.

The old C# Windwaker-coop used a hardcoded address (`0x803B0000`) which only worked for 32-bit Dolphin 5.0 Legacy. We scan for it dynamically:

### Scanner Algorithm (`wwapi/emu/memory.go`)

1. Use `VirtualQueryEx` to enumerate memory regions in Dolphin's process.
2. Look for committed, readable/writable regions >= 24MB (the stock GC MEM1; the patched disc runs with 48 MB since docs/05 "48 MB MEM1", and `addr.MEM1End` is 0x83000000, so a vanilla 24 MB game simply refuses reads past 0x81800000).
3. Read the first bytes of each candidate region.
4. If the bytes match the game ID (`"GZLE01"`), we've found the RAM base.
5. Fall back to scanning smaller regions at 4KB intervals if the first pass misses.

This works on **any Dolphin version** (tested on both 5.0 Legacy and 2603).

## Address Translation

Once the RAM base is found:
- **GameCube RAM address** (e.g., `0x803C4C0C` for rupees) is the cached virtual address the game uses.
- **Process address** = `gcRamBase + (gcAddress - 0x80000000)`

So reading rupees means:
```go
rupees, _ := d.Read(0x803C4C0C, 2) // bounds-checked to MEM1
// Internally: ReadProcessMemory at gcRamBase + 0x3C4C0C
```

## Endianness

GameCube PowerPC is **big-endian**. x86 is little-endian. All game values need byte-swapping:

```go
// Big-endian u32 read
binary.BigEndian.Uint32(buf)

// Big-endian float32 read
math.Float32frombits(binary.BigEndian.Uint32(buf))
```

## Read vs Write

### Reading — Reliable

Reading game data works perfectly. The game's memory writes propagate to the mapping we access.

Examples that work:
- Link's position updates in real-time (`actor + 0x1F8`)
- Rupee count (`0x803C4C0C`)
- Player pointer (`0x803CA754`)

### Writing — Works for Game Data, NOT Code

Writing to game **data** addresses works. The game eventually sees the change (sometimes needs a "trigger" like picking up an item to refresh the display).

Proven:
- Set rupees to 999 → game capped at wallet max (200) when rupee picked up
- Teleport Link 500 units up → Link floated, then fell due to gravity

Writing to **code addresses** (inside `.text` sections) to patch instructions DOES write to memory, but **Dolphin's JIT keeps the cached x86 translation**. The emulated CPU continues executing the cached version, ignoring our patch. This was the original blocker. It was solved by DOL injection at boot (docs/03, and docs/05 "The JIT Cache Wall — SOLVED").

## Dual Mapping Caveat

Dolphin uses "fastmem" — a 4GB virtual memory region where the emulated GC address directly maps to a host memory offset. This is SEPARATE from the mapping we find via the game ID scan.

- **Our scanner finds**: the shared backing memory (where the game's data lives)
- **JIT fastmem**: a separate mapping used when the JIT compiles memory accesses

For most game data, these point to the same physical memory — writes propagate both ways. But for addresses past the DOL end (like injected code), they diverge, which caused many of our failures.

## Performance

Pose and position reads run every 16 ms (~60 Hz, since v0.1.7; 50 ms before) with no measurable overhead. broadcast-pose reads the whole 2016-byte pose each tick, not just the position.
