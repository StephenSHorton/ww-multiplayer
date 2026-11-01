# wind#31: salvage SV1-SV7 on the Mac rig (2026-11-01, maple)

Rig wind31 (slot 4), main ecfd46e, Dolphin 2603-342, REL 0x1CE30685. CryZe 28-Hyrule3 (Grappling Hook, charts) as file 1 on both, save state from it.
`mp-local --coop=new --yes` (the rig script adds --coop-iso): world 8c3040c854daa23c, both granted image|puzzle|combat|live-switches|switches|story|inventory|keys|life|magic|chest-claim.
A = host pid 42024, B = client 42034, 30 fps. The salvage table was read with a dump of `*0x803F6B44` per wwapi/addr/salvage.go.

## SV1 form (a): PASS, with the mark line (A's pull poked)
- Both at `sea 0 44` (Outset). B's table: room-44 points saves 0-3 kind 3 flag 0x00, save 15 kind 2 flag 0x01.
- B hopped to the neighbouring square `sea 0 43`.
- POKE: A's ocean grid 44 bit 0 (0x803C5221 |= 0x01) stands in for A's crane pull of save 0 at (-225066, 0, 291995).
- B, still in 43: `co-op: ocean grid 44 bit 0 waits for the next room or stage change`.
- B hopped into 44 (room change): `set ocean grid 44 bit 0 (0x803C5221 |= 0x01)`, `salvage: wrote 1 byte(s) after heartbeat 4950`, `salvage: marked salvage point 129 (sea grid 44 bit 0, kind 3) emptied: the partner salvaged it`. B's table then reads save 0 flag 0x01 (unusable): the light is gone and there's nothing to crane. Form (a) gives the mark line, while wind#17's form (b) gave none.

## SV2 (chart treasure) and SV3 (Big Octo point): not available on this file
- A stage-hopped through sea squares 45, 36, 37, 38, 29, 30, 31 and 22 and dumped each salvage table: no kind 0 (chart) or kind 2 (Big Octo) point with flag 0x00. 28-Hyrule3 is late in the 100% route and has salvaged them. These need a crafted fixture (an unsalvaged, deciphered chart, or an unbeaten Big Octo square).

## SV4-SV6 (B's own crane): not drivable by blind pad on this rig
- B on Outset's pier, the King of Red Lions 380 units away. Stick toward it: Link jumped off the pier and overshot into the water. A near the boat is "Speak": it opens the King of Red Lions' talk ("What are you doing? The path to Hyrule has already been opened! ..."), so A can't be used to board. Swimming into the boat without A: B ran out of air and voided back onto the pier.
- Boarding, then sailing to a light, stopping and lowering the crane needs closed-loop steering (camera-relative stick, screenshots between moves). That's far beyond a time box with blind taps. A pad driver, or a person, is needed.
- Side finding for wind#37: the FF2 "King of Red Lions talk loop" was the same thing. A near the boat reopens the talk, so my A presses kept restarting it. That was the rig, not co-op.

## SV7: in part
- No verify-failure line and no co-op shutdown in the session log.

## Logs

- No ISI, DSI or invalid access in either dolphin.log.
