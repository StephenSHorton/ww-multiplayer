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

# wind#29 continued (newt, 2026-11-01): P1 Miniblin, P8

## P1 signatures: Miniblin passes; Boko Baba not driven; Armos not run
Rig wind29 (lease ww/dolphin-slot-3), main b9d8b2d, REL 0xA9DE0F1E, Dolphin 2603-342. Both Dolphins were booted from a fresh CryZe
`9-Hyrule1` state recaptured on this REL (alder's earlier `h9.sav` predates #267). Host `mac-rig.sh mp-local wind29 --coop=new --coop-placed-kill`,
both `stage-hop ma2room 2 1`. The Miniblins (proc 0xF9, params 0xE4FF0F90) are hp 2/2 and `/status` reads `7 tracked` on each side.
- A killed two Miniblins with the sword, driven by `wwdev input` B presses after `face`. The first one took two hits (hp 2 -> 1 -> gone).
  A's `/status`: `5 tracked, 2 sent`. B logged `placed: light-shrunk the partner's kill of ma2room room 1 ordinal 6 ... actor 0x80A9C838`
  and `... ordinal 1 ... actor 0x80AA0D70`, the same two addresses A killed. B's own `/status` shows `not sent: echo 2, 2 taken`, so it did
  not echo them back. So the generic signature (health <= 0) covers a real Miniblin death, and the Miniblin needs no class row.
- **Respawn asymmetry (new):** A's game re-created both Miniblins at their homes within about a second of the sword deaths, as new actors
  (pids 414, 419, hp 2/2), so A shows 7 again. B's light-shrunk Miniblins did not come back: B stayed at 5 for 30 s and more. P3 saw both games respawn
  all 7 only on re-entering the room. A real kill makes the room re-spawn its Miniblin at once, but a light-shrink does not. So inside the room
  A sees 7 and B sees 5 until B re-enters. Nothing extra shrinks and nothing echoes; the counts differ until the next room load.
- Link A then died to the Miniblins (the run went on with Link taking hits), and the pair was restarted from the state.
- Boko Baba (kindan r3, one unkeyed at 618/4293/3785, params 0xFFFF01FF, tracked on both): every approach by warp + `face` + B got Link
  bitten (the actor shows mCmd 2, an event holds it) before a slash connected; d_a_bo.cpp is a stub in the decomp. Not driven; this needs
  a person or a lock-on/guard poke recipe.
- Armos (Siren r0/r8): not run. Note that `am2+u` is a default mirror kind (kinds.go DefaultMirrorKinds), so these rows need `WW_ENEMY_SYNC=0`.

## P8 mixed versions (pass, on the last builds that still pair with v0.6.1)
- A v0.6.1 joiner cannot reach today's main: #267 bumped ProtocolVersion 6 -> 7, so the hello is refused.
- With a host at 4cc405f (M6.1, protocol 6), the joiner connects, but the host refuses its co-op:
  `co-op: JoinB (co-op 2) refused: ... co-op schema mismatch: CO_HELLO schema 1, this build speaks 2; co-op is off for this session,
  pose sync still works`. The co-op schema went 1 -> 2 in #232 (PRESENCE names its sender).
- The real P8 pairing is a host after #217 and before #232: bbfd10e (`e006882^`). It has the same blob/REL stamp as v0.6.1 (REL 0x1CE30685), so both
  sides use `WW-mp-1CE30685.iso` and `start-mac-1CE30685.sav`. Host `ww-multiplayer host --coop=<world> --coop-merge --coop-placed-kill --yes`
  on Dolphin A, joiner `ww-multiplayer(v0.6.1) join 127.0.0.1:<port> --coop --yes` on Dolphin B, each with its own USER_DIR_1.
  - The host's features: `...|life|magic|placed-kill|merge`. The joiner's: `image|puzzle|combat|live-switches|switches|story|inventory|keys|life|magic|merge`.
    It gets every other co-op feature, and placed-kill is not granted to it.
  - Both `stage-hop kindan 0 1`. Host `/status`: `5 tracked`. On A, `enemy-kill` on the unkeyed ChuChu 0x80A9E73C (params 0xFF190004)
    gave `4 tracked, 1 sent`. On B (v0.6.1) both unkeyed ChuChus stayed (2 before, 2 after), and B's `/status` shows `0 light-shrunk`.
    Nothing on the old joiner was shrunk.
- No ISI/DSI or invalid access in either dolphin.log, and no panic in either process.

So since main's #232/#267 the mixed-version case is moot for releases: a v0.6.1 player can't play co-op with any later build at all.

## P5, P6, P7 (newt, second slot, 2026-11-01)
Rig wind29 (lease ww/dolphin-slot-1), main eb7948f, REL 0xA9DE0F1E, CryZe `9-Hyrule1` state, mp-local `--coop=new --coop-placed-kill` with `WW_ENEMY_SYNC=0`.
- **P6 Hyroom r0 (pass, same layer):** this file loads the room's enemy layer, with 7 Darknuts (proc 0xC0) and 10 Moblins (proc 0xBD), all unkeyed and the same on both sides.
  `/status` read 0 tracked right after the hop and 17 a few seconds later, once the room had settled. A's `enemy-kill` on Moblin 0x80A81020 gave
  `1 sent`, and B logged `placed: light-shrunk the partner's kill of Hyroom room 0 ordinal 16 (table 9): actor 0x80A81020`. Both games went to 9 Moblins.
  Both games had the same layer; two different layers at once needs B on a later progression and was not run.
- **P7 sea r44 stacked Keese (pass):** alder's start-save try found no Keese, and they are not night-only: setting mTime
  (status_b+0xC, 0x803C4C2C, 180.0 = noon) to 10.0 and hopping still loaded layer 5 (6 ChuChus, 4 Miniblins). The disc places the
  room's Keese on layers 6, 7 and 11 (12, 8 and 6 entries; `coop-tables --section enemies --json`). `stage-hop sea 0 44 6` on both
  gave 12 Keese (proc 0xD8, params 0xFFFF0101) and 12 tracked each. A killed 0x80A5EEFC, and B logged `... sea room 44 ordinal 10 (table 0):
  actor 0x80A64974`, a different member of the same interchangeable stack (B's lowest-pid live one). Both went to 11. The hop to the same
  room on another layer counted the 6 departing layer-5 tracks as `nosig` (not sent); this is harmless.
- **P5 counter:** `placedGone` counts `gone` only when this game saw a member of the placement's class after the PKILL arrived
  (`k.seen`). In the simultaneous Miniblin kill, B's own instance was deleted before A's PKILL came, and each Miniblin is its own class
  (distinct homes), so nothing was ever seen, and the pending kill waited out `placedTTL` (100 ticks) and counted `expired`. That
  behaviour is correct (nothing extra shrank); TESTING's P5 wording is widened to say so.
- Not driven: the Boko Baba and Armos sword kills (P1). Blind input can't land a slash before the Boko Baba bites, and the Armos needs a hit
  to its back. Both need a person or a guard/lock-on recipe.
- No ISI/DSI or invalid access in either dolphin.log, and no panic in mp-local.
