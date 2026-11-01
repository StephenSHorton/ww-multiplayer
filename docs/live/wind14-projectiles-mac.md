# wind#14: projectiles, P10 and the enemy mirror's B1 (Mac rig, 2026-11-01, maple)

Rig wind14 (slot 1), main b9d8b2a, Dolphin 2603-342, REL 0x1CE30685 (the REL is unchanged since #206).
Both games run fixture `d-28-Hyrule3-bombs-X-bow-Y.gci` from its Mac state, with bombs on X and the bow on Y. No `WW_PROJECTILES` is set, so projectiles are on (the default).

## P10 regression sweep: smoke PASS
- `wwdev smoke --no-boot` on the pair (`MP_LOCAL_PIDS` pinned, `MP_LOCAL_PORT=0`) passed 54 checks with 0 failed, including the hop and the after-hop walk.
- mp-local logged "Projectile events on" for bcast and puppet on both sides (A and B).
- Log sweep: no ERROR or panic in mp-local.log, and no ISI, DSI, invalid access or JUTException in either dolphin.log.

## P10: the enemy mirror's B1 (Darknut row, AM2) with projectiles on: PASS in part
Second session: `mp-local` with `WW_ENEMY_MIRROR_KINDS=mo2,bk+u,cc+u,ki+u,tn,am2+u`. All bombs below are real throws from A (X, then A).

**Darknuts: `stage-hop kaze 8 8` on both.**
- B adopted all three Darknuts: `adopted 0111 slots [0:tn][1:tn][2:tn]`.
- A threw a bomb. B's copy counter went 0 -> 1, and B saw the bomb's fuse trail (`b1-B-flight.png`).
- A warped in among the Darknuts and threw a second bomb.
  - B's counter went to 2.
  - The Darknuts woke and moved; B's copies followed A's with the mirror pause held (`b1-B-after2.png`).
  - Health was 15/30/15 on both games before and after, since the bomb landed short. So this run does not test damage on a bomb hit; that is P2's Moblin record.

**Armos: `stage-hop Cave09 0 7` on both.**
- B adopted all four unkeyed AM2 (`adopted 1111`).
- A threw a bomb among them, from the front. B's counter went to 3.
- Health stayed 2/2 on both games: a front hit does no damage, and nothing was doubled. The swallow counter stayed 0 (`b1-am2-B.png`).

**Arrows (Siren).** Two real arrows from A were each copied on B.

**Whole session:**
- puppet-B logged `7 spawned here, dropped map[]`; puppet-A logged `dropped map[self:7]`.
- B's block counters read 4/0/0/0/0/0 at the end: copies made, then refused, lost, takeover, swallow and cleared all 0. The copies count restarts at each stage hop.
- Mirror canaries were all 0, with 4 WatchdogReleases (one per hop or wake).
- No ERROR or panic, and no Dolphin crash lines.

**Not run:** T2-T6's hit and kill checks with a copy in the room, and a bomb that actually lands on a Darknut. A bomb's damage is P2's check.

## P4 / P5 / P6 / P9 "not run" halves: still not run (rig limit)
Blind input could not set up the shots:
- Siren 0 0 lands Link in the King of Red Lions at the r0 dock. In the boat, warps snap back and Y aims the boat's bow.
- On foot in Siren r5:
  - The game rewrites Link's angle every frame, so a poke to it (`+0x204`, `+0x20C`) doesn't stick.
  - Warps toward r5's far torches dropped A into the pit or voided A out.
  - In first person, A's camera clipped into B's puppet at the shared spawn.

The torch writers found for a fire-arrow test (from `coop-tables --section switches`):
- **Siren r0:** two timed torches, memory switches 3 and 4 of table 5. Each clears 601 frames after it's lit, so it can be retested.
- **Siren r5:** two room-store torches (0xE0/0xE1), which reset at each room load.

What these need: a way to face Link at a point (a `wwdev face <x> <z>` that writes the player's angle through its own setter), or Stephen's hands.
