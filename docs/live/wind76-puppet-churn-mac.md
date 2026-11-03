# wind#76 live: actor puppet respawn churn and the F1 rerun (Mac rig, 2026-11-03)

Issue #131. Rig `wind76` (`~/ww-shared/rigs/wind76`): `WW-mp-76.iso` patched from the vanilla disc
with branch `wind76-puppet-churn`, `start-76.sav` recaptured on it (Hyrule), wwdev and
ww-multiplayer built from the branch and entitled. Lease `ww/dolphin-slot-1`. Raw notes:
`runs/evidence.md`, `runs/sweep.txt`, `runs/sweep-hidden.txt`, `runs/sweep-hidden-hops.txt`,
`runs/f1-hops-hidden.txt`.

## The churn on main

Shared `WW-mp.iso`, `move-puppet` next to Link, `wwdev actors` every 0.5 s:

- Outset (`stage-hop sea 0 44`), slot 1 (NPC_OB1, Rose): pid 718, 720, 722 ... 735, a new actor
  about every 0.5 s.
- `M_Dai 0 0`, slots 0 (KAMOME) and 1 (NPC_OB1): NPC_OB1 918 ... 954, KAMOME 947 ... 963.

Cause: both creates fail, the engine deletes the actor, and `puppets.c` phase 2 spawns it again.
KAMOME parameter 0 asks for path 0 of its room (`d_a_kamome.cpp:1496`), which `M_Dai` has not.
NPC_OB1's create (disassembled from `d_a_npc_ob1.rel`) also needs a room path, and type 0 exists
only before event bit 0x520, types 1/2 only after it, by day / by night.

## A drawn KAMOME hangs non-sea stages

First fix tried: parameter `0xFFFF0000` (no path, no switch). No churn, but the gull is then
drawn (parameter 0's switch 0 had always hidden it), and a drawn gull hangs the game in every
non-sea stage tried: Hyroom, M_NewD2, kindan, Siren, kaze, GanonK and a fresh spawn in M_Dai.
Sea rooms 11, 13 and 44 were fine. GDB stub while hung: an endless loop under
`J3DDrawBuffer::draw` (`J3DShape::drawFast`, `J3DShapeMtxMultiConcatView::load`) from
`mDoGph_Painter`. Main hangs the same way in Hyrule after `wwdev unhide-puppet`. The F1 recipe
on that build hung both games in Hyrule (frames 1185 / 1175) as soon as the puppet spawned.

Stephen's call (wind#76 b1): keep the puppet hidden, no churn.

## The fix: `KAMOME_PUPPET_PARAM 0xBFFF0000`

Every slot is a KAMOME, path 0xFF, hidden behind dungeon switch 0xBF (`mSwitchNo` 0xC0).
A zone switch (0xEF) was tried first: it froze the game twice, once on the hop from Outset
to M_Dai with puppets live (frames 1459 -> 1459) and once in Outset right after a puppet spawned
with Link in room -1 (the arrival point), since a zone switch looks up `mStatus[room]`; the gull itself
was still hidden (`mSwitchNo` 0xF0). Dungeon switches do not use the room.

One Dolphin, slot 0 off, `stage-hop`, 18 s, slot 0 on next to Link, then frames and the
KAMOME pids 3 s apart (`runs/sweep-hidden.txt`): Hyroom, M_NewD2, kindan, Siren, kaze, sea 11,
sea 13, sea 44, M_Dai and GanonK all ok, frames advancing, pids unchanged.

Across hops with slots 0 and 1 live (`runs/sweep-hidden-hops.txt`): Outset pids 710, 711, then
M_Dai 764, 765, unchanged over 4 s, frames 1940 -> 2061. Actor +0x2A8 reads `0x0000C000`
(`mSwitchNo` 0xC0). Slot 1 to 0 deletes its gull.

## F1 rerun

`mac-rig.sh pair`, `mac-rig.sh mp-local wind76 Aryll Tetra`, then
`ww-multiplayer fake-client Fake3` (position only, so it rides an actor puppet). Both games at
30 fps in Hyrule with Fake3's puppet up (pid 349 / 348, unchanged). Then both games hopped
`M_Dai 0 0`, `sea 0 44`, `M_Dai 0 0`, `sea 0 44` (`runs/f1-hops-hidden.txt`): 30 fps on both
after every hop, Link at each stage's start point, and the puppet gull's pid unchanged within
each stop (M_Dai 840 / 399, then 1289 / 846). mp-local logged no error. F1 does not reproduce.

Note: an earlier hop loop in this card passed `"sea 0 44"` as one argument, so its hops were
refused ("stage name ... must be 1-7 characters") and the games never left Hyrule. The run
above used a bash script and checked Link's position after each hop.

## Rerun on the rebased build (main e5ac972, 2026-11-03)

The branch was rebased onto main e5ac972 (#313's mailbox at 0x80414F00, #314's REL 0x3D7FEC97, #317
and #318's pose changes) and the blob rebuilt. Rig `w76m`: `WW-mp-76m.iso` patched from the vanilla
disc with that build, `start-76m.sav` recaptured on it (Hyrule), lease `ww/dolphin-slot-5`.

F1 recipe again: `mac-rig.sh pair`, `mac-rig.sh mp-local w76m Aryll Tetra`, `ww-multiplayer
fake-client Fake3` (position only: an actor puppet on both games). At each stop, 15 s after the hop,
both games' fps and the puppet KAMOME (proc 0x00C3 at Fake3's position, y 200) sampled twice 3 s apart:

| Stop | Puppet pid A / B (same 3 s later) | fps A / B |
|---|---|---|
| Hyrule (start) | 349 / 348 | 30 / 30 |
| `sea 0 44` (Outset) | 709 / 708 | 30 / 30 |
| `M_Dai 0 0` | 767 / 764 | 30 / 30 |
| `M_NewD2 0 0` | 909 / 905 | 30 / 30 |
| `sea 0 44` | 1270 / 1265 | 30 / 30 |
| `M_Dai 0 0` | 1327 / 1320 | 30 / 30 |

The puppet followed Fake3's circle (its position changed between the samples) and kept its pid: no
churn. No game hung in the non-sea stages, so the gull stayed hidden. mp-local logged no error. The
native Outset gulls (pids 512-516) were untouched.
