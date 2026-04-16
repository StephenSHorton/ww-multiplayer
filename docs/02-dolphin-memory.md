# Dolphin Memory Access

## The Process Model

Dolphin is a normal Windows process. When it runs a GameCube game, it allocates a block of virtual memory to hold the emulated GameCube's 24MB of RAM. External tools (like us) can read/write this memory using `ReadProcessMemory` and `WriteProcessMemory`.

## Finding the Emulated RAM

The tricky part: Dolphin's emulated RAM base address changes every launch. On a 64-bit system, it's typically a high address like `0x7FFFxxxxxxxx` or `0x1xxxxxxxxxx`.

The old C# Windwaker-coop used a hardcoded address (`0x803B0000`) which only worked for 32-bit Dolphin 5.0 Legacy. We scan for it dynamically:

### Scanner Algorithm (`internal/dolphin/memory.go`)

1. Use `VirtualQueryEx` to enumerate memory regions in Dolphin's process.
2. Look for committed, readable/writable regions >= 24MB (size of GC MEM1).
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
rupees, _ := d.ReadAbsolute(0x803C4C0C, 2)
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

Writing to **code addresses** (inside `.text` sections) to patch instructions DOES write to memory, but **Dolphin's JIT keeps the cached x86 translation**. The emulated CPU continues executing the cached version, ignoring our patch. This is the main blocker — see `docs/05-known-issues.md`.

## Dual Mapping Caveat

Dolphin uses "fastmem" — a 4GB virtual memory region where the emulated GC address directly maps to a host memory offset. This is SEPARATE from the mapping we find via the game ID scan.

- **Our scanner finds**: the shared backing memory (where the game's data lives)
- **JIT fastmem**: a separate mapping used when the JIT compiles memory accesses

For most game data, these point to the same physical memory — writes propagate both ways. But for addresses past the DOL end (like injected code), they diverge, which caused many of our failures.

## Performance

Position reads at 50ms intervals (~20Hz) work flawlessly with no measurable overhead. Could easily do 16ms (60Hz) for smoother sync.
