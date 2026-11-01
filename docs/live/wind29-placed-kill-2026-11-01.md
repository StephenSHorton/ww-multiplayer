# wind#29: M6.1 placement-matched kills, P3-P5 on the Mac rig (2026-11-01)

Rig: Mac, Dolphin 2603-342, lease ww/dolphin-slot-4. main 4cc405f (M6.1 #217, off by default). Two Dolphins on the rig's own cards,
both booted from CryZe `9-Hyrule1` (before Forsaken Fortress's second visit: ma2room r1 has its 7 Miniblins on this file; on
`28-Hyrule3` the room has none). Host `mac-rig.sh mp-local alder --coop=new --coop-placed-kill --yes` (both files bound),
then both `stage-hop ma2room 2 1` (room 1's starts are points 2 and 14). Kills use `wwdev enemy-kill <pid> 5 --yes`, a light-shrink.
To read `/status`, mp-local's stdin was a FIFO (restarted with `--coop=<world>`; the counters reset).

## P3 Miniblins, ma2room r1 (pass)
Both games list the same 7 `pt` (proc 0xF9, params 0xE4FF0F90, no byte-2 switch) at identical addresses and positions.
- Cycle 1, A kills: B logs `placed: light-shrunk the partner's kill of ma2room room 1 ordinal 1 (table 2): actor 0x80AA0D70`. That is the same
  address A killed. Both games now have 6 left, the same set.
- Cycle 2, B kills: A shrinks ordinal 3 (0x80A9F1C0). Cycle 3, A kills: B shrinks ordinal 4 (0x80A9E3E8). Both have 4, the same four addresses,
  unchanged at +30 s and +60 s (no respawn while in the room).
- Both re-enter room 1: both respawn all 7 at the same placements. Nothing diverged over three cycles.

## P4 different rooms (pass)
B `stage-hop ma2room 0 0` (room 0, which has no Miniblins), A stays in room 1 and kills one: B applies nothing (no new `light-shrunk`, 0 `pt`
loaded on B). A's `/status` then reads `0 tracked, 0 sent, not sent: none`. With the partner elsewhere, nothing in that room was
tracked at all, so the kill is not counted under `not sent`. The `offroom` case (A leaves before the actor vanishes) was not run.

## P5 simultaneous kill (pass in part)
With B back in room 1, A and B ran `enemy-kill` on the same placement (0x80A9BA60) at once. Each game lost exactly that one Miniblin
(A 5 to 4, B 7 to 6). No other enemy shrank, and each dropped only its own item. Both `/status` lines read `1 sent ... 0 gone, 1 expired`.
So the partner's late PKILL found no live instance and expired by TTL; it was not counted `gone`, as TESTING's wording expects.
The behaviour is right (nothing extra shrank). Only the counter differs: the `gone` rule (`placedGone`) needs the room's walk
to see the placement missing, and the pending entry timed out first.

No ISI/DSI or invalid access in either dolphin.log, and no panic or fatal in the mp-local output.
Not run here: P1 signatures, P6 layer twin, P7 stacked Keese, P8 v0.6.1 joiner.
