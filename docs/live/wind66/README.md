# wind#66: Remote-Link Stage 4 heap rows S4-1 and S4-12 (Mac rig, 2026-11-01, harbor)

## Builds

Each build was patched from `ww-vanilla.ciso` with that commit's own `ww-multiplayer patch`.

| Name | Commit | REL | RELS.arc growth |
|---|---|---|---|
| main | cc7c512 (#293) | 0xCAC6980B | +60992 bytes. The patched disc is byte-identical to the shared `WW-mp.iso`. |
| #151 | 8bf7295, Stage 4 merged | 0x8588D18B | +21376 bytes |
| #150 | 7e2fff8, the last commit before Stage 4 | 0x56B63740 | +15072 bytes |

## Rig

- One Dolphin, rig `wind66` (`mac-rig.sh one`), lease `ww/dolphin-slot-3`.
- The tools (`wwdev`, `ww-multiplayer`) were built from main and given the debugger entitlement.
  - `heap-free`, `stage-hop` and `coop-tag show` read only game addresses. The heap ranges and `__OSArenaLo` were identical on all three builds, so main's tools read the older discs correctly.
- **Start saves.**
  - main used `start-mac-CAC6980B.sav`.
  - #150 and #151 have no Mac save, so `wwdev auto-recapture` (main's) made one for each disc, unattended.
  - All three start the same way: file 1 of the fixture card, Hyrule room 0, from a save state.
- **The hop script.** [`hops.sh`](hops.sh) runs one `heap-free` before the first hop, then six stage hops: `A_mori 0 0`, `sea 0 44` (Outset), `M_Dai 1 1`, twice over.
  - It waits until `coop-tag show` reports the new stage, then 8 s more, then reads `heap-free`.
  - The seven counters are solid, game, zelda, command, archive, unknown and total.
- **No session.** No `mp-local` or remote ran on any build during the hop runs.

## S4-1 vanilla baseline (main, no session): PASS

Data: [`s4-1-main-hops.txt`](s4-1-main-hops.txt). Free and largest are in KiB; "blocks" is the free-list block count.

| Reading | Counters | Zelda free/blocks, largest | Game free/blocks, largest | Archive free/blocks, largest |
|---|---|---|---|---|
| before (Hyrule) | solid 0, rest 0 | 981/15, 969 | 4763/2, 4763 | 6369/223, 6368 |
| 1 A_mori | solid 0 | 952/16, 929 | 5193/3, 5167 | 8783/217, 7770 |
| 2 sea 44 | solid 0 | 801/31, 662 | 4891/2, 4890 | 7036/238, 6684 |
| 3 M_Dai | solid 1 | 1105/12, 1099 | 5003/2, 5003 | 5162/248, 5082 |
| 4 A_mori | solid 1 | 952/16, 929 | 5193/3, 5167 | 8783/217, 7770 |
| 5 sea 44 | solid 1 | 802/34, 662 | 4895/2, 4894 | 7036/238, 6684 |
| 6 M_Dai | solid 2 | 1105/12, 1099 | 5003/2, 5003 | 5162/249, 5078 |

- **Game and Zelda error counters are +0 over every load.** So are command, archive and unknown. This is the heap gate's baseline assumption.
- **The solid counter rises by 1 on each M_Dai load only. It stays the same on A_mori and sea loads.**
  - The row text ("Solid +1 per load", from HANDOFF.md:60) is too broad.
  - The archived #151 run `s4-hops-nobody.txt` showed the same: +1 on M_Dai, +0 on A_mori.
  - This is vanilla `fopAcM_entrySolidHeap` retrying (HANDOFF.md notes it is unrelated to the mod). It depends on the stage, not on the load.
- **No trend.** A second visit to a stage gives the same Game and Zelda free and largest, within 4 KiB. The archive's largest block falls by 4 KiB and its block count rises by 1 per M_Dai round.

## S4-12 regression: PASS for Stage 4's own footprint

### S4-0 again on main, `full_bodies` clear

This ran on one Dolphin. `wwdev remote-slot 0 --flags 0x24FF` gave slot 0 the step6 flags (bodies through guard, plus attacker and iframe_stamp; no full_bodies). Data: `s4-0-*.txt`.

- **Profile alias.** DMC[0x27] = DMC[0xB5] = 0x80AD2830. The list slot 0x27 points to 0x81128CC8, `g_profile_WWBODY` (proc 0x00B5, size 0x4C80).
- **Shanm.** Not allocated ("no FULL create this boot").
- **Full state.** full_allow 0x00, heap_reason none, full_denied 00.
- **Bodies.** body0 is kind lite. It was PARKED because no pose feed ran: no `mp-local` or `pose-fake-loop`, since another rig's session held the default port. The body was created with err none and tries 1.
- **The Outset to A_mori hop keeps 'WWRL'.** After the hop, 0x804130B0 reads 0x5757524C and the build word reads 0xCAC6980B. The profile alias and lite kind are unchanged.
- **The lite slot's heap cost on Outset.** Zelda −1 KiB. Game and Archive unchanged. Counters 0.

**Scope.** This is the S4-0 checks that `remote-watch` and `peek` read. The Batch A Step 0-6 and S2/v2 LC1-LC4 two-Dolphin checks were not rerun here.

### Heap compare against the pre-Stage-4 baseline

Data: [`s4-12-pre4-hops.txt`](s4-12-pre4-hops.txt) (#150) and [`s4-12-pr151-hops.txt`](s4-12-pr151-hops.txt) (#151). They used the same script, start stage and hop order as S4-1.

#### #150 to #151: Stage 4 alone, `full_bodies` clear, no session

| Heap | Difference |
|---|---|
| Zelda | None. |
| Game | None. |
| Archive | −10 KiB free and −10 KiB largest in every reading, with the same block count. |
| Counters | Identical. |

Free, block count and largest matched in every one of the 7 readings for Zelda and Game.

**Stage 4 alone changes nothing in Game or Zelda.** The only change is a 10 KiB larger archive allocation. The block count stays the same, so one existing block grew rather than a new block being added.

- This is not the shanm table. Shanm is allocated only at the first FULL create (0x4760 bytes, S4-2), and with the flag clear it is never allocated.
- The size fits the larger module 0x58 REL that Stage 4 added: RELS.arc +6304 bytes stored, plus its bss. That attribution is inferred from the sizes; the run did not trace the allocation.
- With `full_bodies` set, the shanm table (about 18 KiB, once per boot) comes on top, as S4-2 measured.

#### #151 to main: the work merged after Stage 4

Measured the same way. This is context, not part of the row.

| Heap | Difference |
|---|---|
| Zelda | Unchanged. |
| Game | Same free per stage, but one fewer free block and a much larger largest block: A_mori 5167 vs 4298 KiB, sea 4890 vs 4104, M_Dai 5003 vs 4153, Hyrule 4763 vs 3966. Main's GameHeap is less fragmented, which gives a FULL create's 0xB0090 solid heap more room. |
| Archive | A further −112 KiB free and +44 to 48 blocks, constant per stage. |

This archive change comes from later features, for example the larger REL (RELS.arc +60992 vs +21376 bytes) and the three-remote mini-Links. It is not from Stage 4, and this run did not attribute it further.

## Rig incidents (no effect on these results, but they touched other rigs)

1. **A stage-hop reached reed's Dolphin.**
   - What happened: a relaunch of this rig's Dolphin failed, so `u1.pid` was missing and one `stage-hop sea 0 44` ran with an empty `WW_DOLPHIN_PID`. wwdev then took the first Dolphin it found, rig wind64 u1 (reed). That Dolphin was moved to sea room 44.
   - Reported on wind#64 at once.
   - Fix: [`wd.sh`](wd.sh) now refuses to run unless the PID is this rig's own Dolphin, and `hops.sh` refuses an empty PID.
2. **`pose-fake-loop` joined sparrow's session.**
   - What happened: `wwdev pose-fake-loop -h` does not treat `-h` as help. It started and connected to localhost:25565, where it joined the wind63 session (sparrow) as player 5. It exited within about a second.
   - Reported on wind#63.
   - The tool has no help flag. Point it at your own `MP_LOCAL_PORT`, or don't run it on a shared Mac.
