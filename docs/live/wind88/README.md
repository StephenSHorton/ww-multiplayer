# wind#88: the unrun co-op checks with a progress save (Mac rig, 2026-11-03, maple)

GitHub #128's remainder: M3 A4-A6 and M4 P3-P9 from [docs/08 §8.2](../../08-coop-design.md#82-owner-machine-two-dolphin-harness).

Rig `wind88` (lease `ww/dolphin-slot-3`), main a41488c, Dolphin 2603-342, REL 0x3D7FEC97, BlobHash 0xA37EC8DB. The rig's own
`wwdev` and `ww-multiplayer` were built and entitled from that tree, and its own `WW-mp.iso` was patched by it.

**The save.** CryZe `100pct-E/5-BombsEarly.gci` on both cards (a local fixture, never in the repo), recaptured on this build
as the rig's save state. `save-diff` over the CryZe files picked it: it is after DRC and the Wind Waker, and before the
Deku Leaf (`6-StartFW` adds mGetItem[6] and max magic 16), the bow (`9-Hyrule1` adds mGetItem[12] and arrow max 30)
and Ballad of Gales (mTact bit 1). Link starts on Windfall (sea room 11). `mp-local --coop=new`, console through a file
(`tail -f console.in | mp-local`). World 07bbbc6bce94f7cd. Both granted
`image|puzzle|combat|live-switches|switches|story|inventory|keys|life|magic|placed-kill|chest-claim`. A = host, B = client.

**How the source side ran.** Every gain on A is a POKE: A's own flag set with `poke-u32`, as the game's get would set
it. Opening a chest, learning a song or feeding the fishman can't be driven blind on the rig. What each check tests is
the trip from A's flag to B's game: B's log, B's memory and B's screen. The game's own pickup on A is not tested. Each
result below says which.

## P3 A heart piece: PASS (source by POKE), no quest-screen pair
- A in Cave01 (table 12, hopped), B on the sea at Windfall. POKE: A's live memBit tbox bit 5 (`0x803C5380 = 0x20`),
  the heart-piece chest `tbox 12:5` in `coop-tables --section hearts`.
- B: `set t12 subdungeon chest 5 (0x803C513B |= 0x20)` (table 12's saved slot: B isn't in it), then
  `wrote max-life counter +1 at 0x803CA77E: max life: 20 -> 21 quarter hearts: 2 of 28 piece(s), ...`.
  `peek 0x803C4C08` on B: 0x00140014 -> 0x00150014. A's own derive wrote the same +1 on A (the poke set only the flag).
- Not taken: the quest-status pair. `input 0x20` (R) plus the trigger poke didn't change the pause menu's subscreen.

## P4 A song: PASS (source by POKE), no collection-screen pair
- POKE: A's mTact `0x803C4CC5` 0x01 -> 0x03 (Ballad of Gales). B at once (idle): `set mTact bit 1 (0x803C4CC5 |= 0x02)`.
- Not taken: the collection-screen pair (the same subscreen problem as P3).

## P5 The bow: PASS (source by POKE)
- POKE: A's mGetItem[12] `0x803C4C65 |= 0x01`. B: `set mGetItem bit 0 (0x803C4C65 |= 0x01)`, arrow max bits 1-4
  (`peek 0x803C4C77` = 0x1E), then `wrote item slot 0x803C4C50 FF -> 27: bow slot: nothing -> the bow`, and
  `not written: ammo ... (the HUD's ammo counters are no WriteGuard target before M7)`.
- Pair: [items-B-before.png](items-B-before.png) has no bow, [items-B-after.png](items-B-after.png) has the bow with 0 arrows.
- Not run: B firing an arrow after picking some up.

## P7 Max magic: PASS (source by POKE)
- POKE: A's mGetItem[6] `0x803C4C5F |= 0x01`. B: the Deku Leaf into slot 0x803C4C4A (FF -> 34), then
  `wrote max-magic counter +16 at 0x803CA784: max magic: 0 -> 16: Deku Leaf true, ...; the meter is not filled`.
  `peek 0x803C4C18` on B: 0xFFFF0000 -> 0xFFFF0010, magic stays 0.
- Pair: [p3-B-before.png](p3-B-before.png) has no meter. [p7-B-hud-after.png](p7-B-hud-after.png) shows an empty
  magic meter under the hearts. The items screen after shows the Deku Leaf.

## A4 Ocean and charts: PASS (source by POKE)
- B at Windfall. POKE on A: sea map 12 bit 0 (`0x803C4D18` 0x02 -> 0x03, the fishman's charted bit) and chart 23 owned
  (`0x803C4CDD |= 0x80`; chart 23 is one `7-AfterFW` adds).
- B: `sea map 12 bit 0 waits for the next room or stage change`, the same for the chart. After B's stage-hop back
  into sea room 11: `set sea map 12 bit 0 (0x803C4D18 |= 0x01)` and `set chart 23 owned (0x803C4CDD |= 0x80)`.
- Pair: [a4-B-chart-before.png](a4-B-chart-before.png) shows 3 charted squares and charts x04.
  [a4-B-chart-after2.png](a4-B-chart-after2.png) shows square 12 (row 2, column 6) charted and charts x05.
- A first try with "chart 64 owned" (`0x803C4CE7 |= 0x01`) synced the same way but moved no counter: bit 64 is no
  chart the game counts. That was a bad pick, not a bug. It stays in the world.
- Not run: the real fishman feed and the real salvage.

## A5 Nothing else, and nothing while a gate is closed: PASS (coop-watch not run)
- Pause gate: B's pause menu open, POKE on A mTact bit 2. Nothing on B for 8 s (B's mTact still 0x03). Right after
  the menu closed: `set mTact bit 2 (0x803C4CC5 |= 0x04)`.
- Each player's own: POKE on A life 0x14 -> 0x10, rupees 200 -> 291, magic 0 -> 8. Then B went through a stage change.
  B's `0x803C4C08`/`0x803C4C0C`/`0x803C4C1C` stayed 0x00150014 / 0x00C80000 / 0x00000000, and B's log had no line about them.
- Every write line on B over the session is in the classes A5 names, plus items, songs and story at their own gates.
  `coop-watch` itself was not run.

## A6 The off switches: PASS
- `/coop off PlayerB`: `co-op writes are off for PlayerB: you typed /coop off. Pose sync goes on; type /coop on to write
  the world again.` POKE on A mTact bit 3: nothing on B (mTact 0x07 stays).
- A warped twice beside B: [a6-B-off-1.png](a6-B-off-1.png), [a6-B-off-2.png](a6-B-off-2.png) show the PlayerA copy
  in two places.
- `/coop on PlayerB`: `writes are on again for PlayerB ...`, then `set mTact bit 3`. It is an item, so it landed once
  B was idle, without waiting for a room change. `/status` says `armed; ... writing`.
- Save states: Shift+F1 on B, then F1 five times 8 s apart (keys held through CGEvents, the same way auto-recapture
  sends them): `co-op turned itself off: 5 save-state loads in 1m0s (the heartbeat went backwards each time); load one
  state, then restart co-op. Pose sync goes on`. Each load logged one `max life: keeping 2 quarter heart(s) ...` line.

## P6 Story bits at the next stage change: PASS (source by POKE), no layer pair
- POKE on A: events 0x2A01 and 0x2104 (two that `6-StartFW` adds over `5-BombsEarly`). Nothing on B for 10 s while B
  stayed on the sea (`0x803C5254` stays 0x00008800).
- B's stage-hop: `a stage change starts: writing 2 story bits and 0 registers of the world`, then
  `set event 0x2104` and `set event 0x2A01`, on that tick.
- Not run: a real cutscene, and the layer-change pair. These two bits change no layer that B could show.

## P8 save-diff after saving both: PASS
- Both stage-hopped, then saved in game through the pause menu by pad (Start, down x3 to SAVE, A, Yes, then Yes to
  continue). Both GCI files were rewritten.
- `save-diff a.gci b.gci` ([p8-save-diff.txt](p8-save-diff.txt)): only PER_PLAYER (life, rupees, magic, status_b,
  which are A's poked values and the place) and TAG (each file's own tag). No OR, OR_MASKED, MAX or DERIVED difference.

## P9 A far-behind file: PASS
- B restarted on CryZe `1-Spoil's Bag` (Link on the pirate ship, 3 hearts), recaptured on rig `wind88b`. A kept its
  bound file. `mp-local --coop=07bbbc6bce94f7cd`.
- Plain `--coop` ([mp-p9.log](mp-p9.log)): `preflight: far behind: the world is past 0x0F80, 0x0801`, then
  `co-op: the world is far ahead of this file: copy the host's save in, or the host can run with --coop-merge ...`,
  with the three-files and save-and-reload advice. Nothing was asked or bound, and B logged no write.
- `--coop-dry-run` ([mp-p9dry.log](mp-p9dry.log)): B's own preflight said `subset` (a dry run loads no tables), so it
  asked to bind. The host refused it: `may not bind: the world is far ahead of your file (story milestones 0x0F80,
  0x0801) ... held until your file catches up: MET_KORL, UNK_0801, UNK_0520`, then `the host did not accept the bind
  (DIVERGENT); nothing was written`.
- `--coop-merge` ([mp-p9merge.log](mp-p9merge.log)): the bind was asked and accepted (`far behind, merged under
  --coop-merge, 3 holds`). B logged `holding event 0x0F80 back: MET_KORL moves the resume stage to Windfall`, the same for
  0x0801 and 0x0520, and `/status` lists them under `story bits held back until this game catches up`. 64 bytes were
  written, with 0 verify failures. Not checked: that B's file resumes on its own place after a save and reload.

## Findings
- `stage-hop`'s `now in stage ...` line printed the stage before the hop (`now in stage "Cave01" room 11, table 0 sea`
  for a hop into Cave01, and `"sea" room 0, table 12` for the hop back). The hop itself was right: `/status` and the
  host agreed.
- In a `--coop-dry-run` session, a far-behind joiner's own preflight says `subset` and asks to bind, which the host
  then refuses. The spec allows that ("a bind asked for is refused"). A dry run's preflight has no tables to judge with.
- The pause menu's R and L subscreens weren't reachable by `input` plus the trigger poke, so the quest-status and
  collection pairs need another way in.

## Not run, with reasons
- Real gameplay sources on A (chest, song, bow chest, fishman, salvage, cutscene): not drivable blind on the rig.
- Quest-status and collection-screen pairs (P3, P4), firing an arrow (P5), a stage-layer pair (P6), and `coop-watch` (A5).

## Logs
- Both dolphin.logs swept: no ISI, DSI or invalid access. mp-local: no panic. 0 verify failures in every session.
- The mp-local logs here have the enemy, puppet and broadcast lines removed, and home paths written as `~`.
