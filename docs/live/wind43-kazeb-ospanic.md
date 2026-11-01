# wind#43: the OSPanic in kazeB (2026-11-01)

From wind#23 (cove, rig cove23): B's Dolphin froze in kazeB's entry letterbox, and dolphin.log had `Invalid write to 0x01234567, PC = 0x80006d64`. Mac rig, main 5dcb364, REL 0x1CE30685, Dolphin 2603-342. Reproduced on ONE Dolphin with no mp-local and no co-op, so the relay and the mirror are not involved.

## What panics

- PC 0x80006d64 is inside the game's own `OSPanic` (`m_Do_printf.cpp`, 0x80006C4C-0x80006D84), at its deliberate `*(u32*)0x1234567 = 0x1234567` before `PPCHalt`. It prints its message with `vprintf` and its stack with plain `OSReport`, which the retail build drops, so dolphin.log only shows the store.
- `JUT_ASSERT` stays live in retail (`JUTAssert.h`: only `JUT_ASSERT_DEBUG` is compiled out). `showAssert` draws into the XFB only when its device bit is set, and these asserts don't, so nothing shows on screen either.
- The caller, found on the halted game: read the current thread (`0x800000E4` -> 0x803A2960), dump its stack (0x803FDF68-0x8040CF68), and take every word whose preceding instruction is `bl 0x80006C4C`. One hit: return address 0x80245B44, in `cXyz::normalize` (0x80245ADC), which asserts on a zero vector. Walking the back chain from that frame:
  `cXyz::normalize` <- `dDoor_info_c::checkArea` (0x8006BD00) <- REL module 132 text+0xC84 = `daDoor12_c::setEventPrm` (`d_a_door12`) <- door12's execute <- module 88 (the mod REL's execute hook, a pass-through here) <- `fopAc_Execute`.
- `dDoor_info_c::checkArea` (`d_door.cpp`) takes `player.pos - door.pos` and normalizes it in 3D. It is zero only when Link stands exactly on the door's origin.
- On the disc, kazeB's start point 0 (`PLYR #0`, params 0xFFFF6000, a door start) is at (0, 1500, 4896.65), exactly the position of kazeB's door12 (`TGDR`, params 0x0FFFF0FF). So a point-0 start puts Link on the door's origin.
- door12 only reaches `setEventPrm` -> `checkArea` from `daDoor12_actionWait` when it has no stop bars and isn't closing. With Molgera alive its bars are up, so the path is never reached. A normal door start plays the door's open demo first, which walks Link off the origin.

## Repro (one Dolphin, cove's WT start save, no mp-local)

| Sequence | Result |
|---|---|
| `kazeB 0 0` (boss alive) -> poke dungeonItem (0x803C53A1) \|= 0x08 -> `kaze 15 15` -> `kazeB 1 0` -> `kazeB 0 0` | panic, twice (0 fps in the entry letterbox, the same `0x01234567` write) |
| poke 0x08 -> `kaze 15 15` -> `kazeB 0 0` | clean |
| no poke -> `kazeB 1 0` -> `kazeB 0 0` | clean |
| poke 0x08 -> `kaze 15 15` -> `kazeB 0 0` -> `kazeB 1 0` -> `kazeB 0 0` | clean |
| poke 0x08 -> `kaze 15 15` -> `kazeB 0 0` -> `kaze 15 15` -> `kazeB 0 0` | clean |

So it needs the boss-dead bit set by hand while the game is inside kazeB with the fight not started, then the continue start (point 1, under the arena), then a `stage-hop` back to the door start. The door's open demo doesn't run on that hop, door12 is idle with no bars, and Link is on its origin.

## Can it happen in real play?

No, on this evidence. The trigger is three dev-only steps: a poked boss bit (a real kill sets 0x08 and demo-seen together, 0x28, and the clean rerun with 0x28 never panicked), point 1 (the continue start, reached only by a game over inside the arena), and a `stage-hop` from inside kazeB to its own door start. A player enters kazeB only through the boss door from kaze, which plays the door demo. Co-op's relay never pokes 0x08 alone: it sets the partner's whole boss state, and the record shows the 0x28 case clean.

Not a co-op or mirror bug, and not the door12 "estimated heap size" line: that OSReport_Error is vanilla, printed on every kazeB entry, panic or not.

Optional rig guard, not done: `stage-hop` could refuse a door-start point (PLYR type 6) when the target stage is the current one, or warn that a door start skips its door demo.
