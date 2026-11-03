# wind#89 live: three real players moving at once, co-op on (Mac rig, 2026-11-03)

Issue #11 (N>2 players). Build: main a41488c, `wwdev` and `ww-multiplayer` built from it and entitled
into `rigs/wind89/bin`. Disc patched from it into `rigs/wind89/WW-mp.iso` (REL 0x3D7FEC97, BlobHash
0xA37EC8DB, the same ids as the shared disc; not byte-identical to `~/ww-shared/disc/WW-mp.iso`).
All games from `start-mac-3D7FEC97-A37EC8DB.sav` (file "Abahbob"). Leases `ww/dolphin-slot-1` and `-4`
(wind#87 and wind#88 held 2 and 3, so up to 7 Dolphins were running on the Mac).

- A (Aryll, PID 2670) and B (Tetra, 2709): rig `wind89`, `MP_LOCAL_PORT=0 mac-rig.sh mp-local wind89
  Aryll Tetra --coop=new`, console fed from a file, server 127.0.0.1:52034, world 6301883c5b061892.
- C (Medli): rig `wind89c` (`mac-rig.sh one`), `WW_DOLPHIN_PID=<C> USER_DIR_1=<wind89c>/u1
  ww-multiplayer join 127.0.0.1:52034 Medli --coop --coop-iso <rig disc> --yes`. C was relaunched
  twice (PIDs 56448, then 84720 for a solo repro, then 51036) and joined three times. Each join bound
  and armed in the same world.
- Movement: `walk.sh` (below) holds the stick for one side of a square, then turns, on each Dolphin
  at the same time, every call pinned with `WW_DOLPHIN_PID`.

Scripts: `docs/live/wind89/{walk,drawrate,hbmon}.sh`. Screenshots: `docs/live/wind89/*.png`.

## Draw counts (the flicker comparison #11 asks for)

How it was measured: the REL's visuals block counts the sword, shield and scabbard models it enters
for each mini-Link that T2 draws (`WWVIS_STAT_EQUIP_OFF`, 0x804158B4: three a slot a frame). Off
screen counts too, as long as the slot is drawn. `drawrate.sh` reads it and the mailbox heartbeat
(0x80414F00) at both ends of a window. Equip ÷ frames ÷ 3 is the share of frames on which each
remote was drawn, so a dropped draw shows as a value under 1. The name-tag counter (+0xE0) is
screen-culled, so it says nothing about flicker. Both are read with `peek`, and the reads are not
atomic with the frame, so ±1 frame per window is noise.

Both runs were in Hyrule's garden, with every player walking a 0.7 s square the whole time:

| Run | Dolphin | frames | equip/frame | per remote | fps | vis err |
| --- | --- | --- | --- | --- | --- | --- |
| N=2 (A, B), 120 s | A | 3607 | 3.0000 | 1.000 | 30.1 | 0 |
| | B | 3610 | 2.9992 | 1.000 | 30.1 | 0 |
| N=3 (A, B, C), 120 s | A | 3609 | 5.9983 | 1.000 | 30.1 | 0 |
| | B | 3610 | 6.0000 | 1.000 | 30.1 | 0 |
| | C | 3612 | 6.0017 | 1.000 | 30.1 | 0 |

**No draw is dropped at N=3, and nothing gets worse from N=2 to N=3.** An earlier 60 s three-way walk
gave the same result (5.9967 / 6.0033 / 6.0033 at 30.2 fps). A burst of 8 screenshots from C, with A
and B walking in front of it, shows both mini-Links whole (`hyrule-C-two-remotes.png`). This
measures draw entries, not pixels. A one-frame visual artefact (the old shared-J3DModelData flicker)
would only show up in a frame dump, and none was seen in the screenshots.

The same comparison on DRC's exterior (room 3) gave 2.5 of 6 and 0.72 of 3. That was the walkers
falling off the cliff path: each void-out reloads the stage, and mp-local logged 86 `between stages`
lines. It is not flicker. Walk somewhere flat for this measure.

## J3D chain cycles: stage hops under load

The failure #11 worries about would be a hang (heartbeat stops) when one game tears down or
rebuilds its models while remotes are drawn. `hbmon.sh` logged every Dolphin's heartbeat delta each
second while:

- C hopped Hyrule → `M_NewD2 6 3` → Hyrule, with A and B walking;
- then A, B and C each hopped to `M_NewD2 6 3`, one at a time, with the other two walking.

No Dolphin had a second under 20 frames (76 samples). After the hops all three were in DRC room 3.
A, B and C each drew both remotes there (2.00 name tags and 6.0 equip per frame, 30.7 fps), and the
show and hide lines were right (`is between stages: hidden`, then `is in this stage (M_NewD2):
shown`). `drc-A.png`: Medli's and Tetra's tags overlap because all three arrived on the same start.

**A hang that was my fault, not a bug:** a hop of C to `sea 44` hung C in the game's own OSPanic
path (`stw` to 0x01234567 at 0x80006d64, `Invalid write to 0x01234567` in dolphin.log). The same two
hops on a lone C with no join hung the same way. I ran `wwdev stage-hop` without `ISO_PATH`, so the
start check (cmd/wwdev/stagehop_check.go) could not read the disc and only warned, and my `tail`
cut the warning off. On the rig, set `ISO_PATH` (or run `stage-hop` from the `mac-rig.sh` env) so
bad starts are refused. Logs in `~/ww-shared/rigs/wind89/run/crash1/`.

## Co-op with three

All three bound one world (A co-op 1, B co-op 2, C co-op 3, then 4 and 5 after rejoins) with
`image|puzzle|combat|live-switches|switches|story|inventory|keys|life|magic|placed-kill|chest-claim`.

**Kills, passed.** DRC room 3 had three keyed enemies on all three games: Bokoblin setID 1 (the
host's; B and C mirror it, `K1 held: paused: mirror`) and Kargarocs setID 0 and 2. One kill each,
by `enemy-kill` (a light-shrink poke):

| Killer | Enemy | Where the kill was applied |
| --- | --- | --- |
| C (join) | bb setID 2 | A and B: `set the partner's dead setID 2 in room 3 (zone slot 0) (0x803C53BF \|= 0x04)` and `light-shrunk the partner's kill` |
| B | bb setID 0 | A and C: `set the partner's dead setID 0 ... (\|= 0x01)` and the light-shrink |
| A (host) | bk setID 1 | B and C: the mirror's death (no co-op line: their BK is the host's mirror) |

Within 12 s of each kill, the enemy was gone on all three. The room's zone word (0x803C53BC) read
0x00000007 on A, B and C: all three setIDs are recorded dead everywhere.

**Stage hops with co-op, passed** (above). C's `/status`-level view: `partners` hidden and shown
with each hop, and no DIVERGENT or ERROR in any log.

**Chest claim with three, not run.** CC-1's setup clears a chest's bit in both files before the bind
(TESTING.md M9). This file has the DRC chests open, and the world was founded from it. The 3- and
8-way claim contention ran on the server in wind#81 (docs/stress-test.md).

**Boss relay, not run** (wind#12 and wind#58 cover it with two players).

## The join's ~420 KB/s stream (wind#16 finding 3): now scoped

`nettop` on C's join over 15 s windows (four connections to the server):

| C's situation | big receive stream | pose out | enemy | co-op |
| --- | --- | --- | --- | --- |
| A, B, C in DRC room 3 | 68.3 KB/s in | 33.6 KB/s out | 3.4 KB/s in | ~0 |
| C alone in Hyrule | 2.0 KB/s in | 36.0 KB/s out | 3.4 KB/s in | ~0 |

The connection wind#16 saw flat at about 420 KB/s now carries 68 KB/s for two remotes in the stage
and drops to 2 KB/s when C is alone. Relay scoping covers it. C's own upload (33-36 KB/s) is the
same in both cases. The server scopes the fan-out, not the sender.

## Minor findings

1. **A join backs up a second card folder outside the rig.** With only `USER_DIR_1` set, C's
   `join --coop` listed and backed up `~/Library/Application Support/Dolphin 2/GC/USA/Card A` as its
   second slot. It only backs up; nothing there was written. On the shared rig, set `USER_DIR_2`
   to the rig's `u2` as well. In the product, a joiner with one Dolphin should probably not look at
   the default second folder.
2. **Stray input from another rig.** At about 08:40, wind#87's script sent a warp, inputs and pokes
   to A because a zsh array bug left `WW_DOLPHIN_PID` empty (their comment on wind#89). That window
   (A alone at the castle stairs, 0 tags on A) is left out above. Every call to the Dolphins must be
   pinned with `WW_DOLPHIN_PID`. In zsh, `set -- $a` does not split words (my own first walk loop
   broke the same way).

## Crash sweep

No ISI, DSI or invalid access in A's, B's or the last C's `dolphin.log`. The one `Invalid write`
was the `sea 44` hop above, and that log is kept in `crash1/`. No panic or ERROR in mp-local or the
joins. Dolphins stopped by PID, leases released.

## What is left on #11

- The 4th+ remote's actor-puppet churn and the F1 retry: #131 (wind#76).
- A pixel-level flicker check (a frame dump), if the draw count is not enough.
