# wind#63: batch C live rows on the Mac rig (L4-2..L4-5, AMB, BB), 2026-11-02

**Build.** main cc7c512 (REL 0xCAC6980B, code hash 0x04765DD0). wwdev and ww-multiplayer were built from it and given the debugger entitlement (`~/ww-shared/rigs/wind63/bin`).

**Rig.**
- Shared `WW-mp.iso` and start save `start-mac-CAC6980B.sav`.
- `mac-rig.sh pair wind63`: A = 43017 (host), B = 43069 (client). Lease `ww/dolphin-slot-2`.
- `mp-local` with `MP_LOCAL_PORT=0`. Kinds and `WW_ENEMY_ANNEX` per run are below. Remote bodies are the default (step6) unless a run says `WW_ENEMY_REMOTE=off`.
- Crash sweep at the end: no ISI, DSI, invalid access or OSPanic in either `dolphin.log`.

Rig notes for whoever runs these next:
- **mp-local's start shift pushes B off the ledge in `M_NewD2 0 7`.** When both Links land on the room-7 start, mp-local moves B by (+50, 0, +50), and B falls out to `M_Dra09` (seen twice). Move B along the corridor first (`warp 5250 5400 100`), or set `MP_LOCAL_SHIFT_X/Y/Z=0`.
- **`M_NewD2 3 12` dropped B into a void-out loop, and `0 16` did the same on both games.** Each Link fell from its start, respawned and fell again. A save-state load (`F1` held through CGEvents, `rig key.sh`) recovered B, and A was recovered by loading B's state.
- **Most floor spots in `M_NewD2` room 3 are voids**, so a Link can't be put under the perched Kargarocs by `warp`.

## L4 (layout 4)

### L4-1 recheck (`l4-1-block-dump.txt`)
- Layout 4, kind_mask 0x8F.
- The knob table is valid for knob_gen on both games: A 3935 = 3935, B 27162 = 27162.
- fp_miss 0.

### L4-2 annex unchanged: PASS in part
**Annex bytes** (`l4-2-annex-A.txt`, `-B.txt`; `WW_ENEMY_ANNEX=bk:tg_hit_up=60`), at the layout-3 places on both games:
- aux at +0x1C8;
- annex seq at +0x1E8;
- override rows at +0x1EC, with BK TgHitUp 0x003C at +0x1F0;
- invuln waits at +0x1F8 and hook bytes at +0x1FC, all 0.

The v0.5.0 bytes recorded before the recapture don't exist on the Mac, because a PC save state doesn't load here. So the byte-for-byte comparison stays the offline `TestEnemyBlockPlacement`'s.

**B1 T6 regression** (`mo2+u,bk+u,cc+u,ki+u,am+u,bb`, `bk:tg_hit_up=60`). One host `enemy-kill` each:

| Kind | Where | Result |
|---|---|---|
| BK | `M_NewD2 0 0` | setID 1 |
| KI | `M_NewD2 7 4` | setID 3 |
| MO2 | `M_Dai 1 1` | setID 0 |
| AM | `kaze 2 2` | setID 0 |
| CC | Hyrule | pkey 0xF8F8E57A |

Each left the same list on both games (`l4-2-*-kill-A.txt`).

**The override still applies.** B's sword (`swing.py`) on BK setID 1 in room 0 gave `applied_at` ACKs with health 3, 3 and 1 (`l4-2-bk-client-acks.txt`).

**Hops** (`hops/h1..h11`): the P0-L4 list `0 0`, `6 3`, `7 4`, `0 7`, `14 8`, `15 10`, `3 12`, `0 16`, then `M_Dai 1 1`, `kaze 2 2` and Hyrule.
- Each room's kinds were adopted. Rooms 8, 10, 12 and 16 have no mirrored kind.
- PauseReasserts, AnmUnresolved, RevalidateFailures and canaries2 stayed 0 throughout. Only WatchdogReleases rose, +1 per load.
- `0 16` reached its room on A only (the void loop above).

### L4-3 live override for id >= 3: PASS
`M_NewD2 7 4`, host `enemy-kill` (SF2_LIGHT), B's copy watched with `enemy-watch --kind ki`:

| Run | DYING | Last seen | Shrink |
|---|---|---|---|
| `ki:quiet=60` (knob table quiet[3] = 60 on B) | 1.968 s | 3.904 s | about 58 frames |
| Control, no override | 1.968 s | 4.912 s | about 88 frames, the compiled 90 under light |

`l4-3-ki-*`.

### L4-4 freshness: PASS
- `poke-u32 0x80413750 DEADBEEF` on B. The table, gen echo and 0x80413294 were back within 0.3 s, and B's log said `knob table rewritten: seen G0 want G27162`.
- **After a save-state load on B** (slot 1, F1): epoch 2. Go wrote a new gen (27163), and the next clobber logged `seen G0 want G27163` and recovered.
- **With `bb:hooks=tail`, mirroring in `M_Dra09 1 9`:** 0x80413128 read 0x02 before the clobber, during it and after the rewrite (`l4-4-bbhooks-clobber.txt`).

### L4-5 wire, mixed builds: PASS (the refusal comes earlier than the row says)
- **HostKindMask** with `bb` on: kind_mask 0x9F (bit 7 set, bit 8 never) in every hop dump.
- **v0.5.0 peer** (built from tag v0.5.0) joining today's `mp-local`: refused at the hello. The server logged `refused "v050peer": this host runs a newer ww-multiplayer (network protocol 8, yours is 3)`, and the joiner showed the same text.
- **v0.5.0 exe against today's disc:** it refuses at the WWMB contract check (`the game's patch is ABI 1 layout 3, this version's is ABI 1 layout 2`), before any RELBuildID read. The disc is refused either way. `l4-5-v050-join.txt`.

## AMB (pot ambushers, `M_NewD2 0 7`)

### Finding: with remote bodies (step6, the default) the client's Link reveals the host's ambusher
- With `WW_ENEMY_REMOTE` at its default, B's Link warped 300 units from pot #6 made A's Bokoblin 0xFFFF3203 leap out while A's Link stood about 2900 units away. The copy then came out and followed it. Seen twice.
- tubo_wait reveals on `fopAcM_searchPlayerDistance` (d_a_bk.cpp:4226-4246), and step6's player swap lets the host's enemy measure to B's body.
- **Control** (`WW_ENEMY_REMOTE=off`): the same warp revealed nothing on either game (`amb-control-remoteoff-B-near.txt`).
- So the pot section's gap "Only the host's Link reveals an ambusher" holds only with remote bodies off. With the default, either player's Link reveals it, which is what co-op wants.

### AMB-2 hidden stick: PASS
- Default: B's copy's stick (boko pid 806, CARRY) has mpModel 0x81B065F0, and its base TR matrix translation is (-10000, -10000, 0) (`amb-2-stick-B.txt`).
- `amb-2-B-pot-hidden.png` (remote bodies off): the three pots with no stick.
- With `bk:hooks=ambush` the stick is at the hand, (2505, 5500, 124). `amb-2-B-hook-ambush-stick.png` shows it standing out of the middle pot (`amb-2-hook-ambush-B.txt`).

### Hidden: PASS (remote bodies off)
- B's sword broke pot #6 and its neighbours (`hidden-B-breaks-pot.txt`, `hidden-B-pot-broken.png`).
- No Bokoblin came out on B. The copy kept m02BA 0xFF at the pot and its stick stayed parked.
- A's ambusher was untouched.

### Client first: PASS (remote bodies off; the half size not seen)
1. A was in room 0 and B in room 7. B's Link at the pot revealed B's own unadopted Bokoblin (m02BA 0). It came out at scale 1.0, because it revealed by distance before the pot broke; the half size is only for a pot that is gone first.
2. A entered room 7. Within 4 s B adopted the slot, and the copy was back in its pot (m02BA 0 -> 0xFF, Go's ambusher hide) at scale 1.0.
3. A's Link went to the pot. A's Bokoblin came out, and the copy came out with it at full size (`client-first-B.txt`).

### AMB-4 hidden handback leap: PASS (the hook control: FAIL, wind#69)
- **Default** (`amb-4-default-B.txt`): the copy was adopted hidden, then A hopped to `M_NewD2 0 0`. B's copy read pid unchanged, action 15, mode 0, m0300 0, m0310 0, scale 1.0, mType 3 and m02BA 0xFF.
- **The leap:** B's Link warped 300 units from the pot. The copy played BK_JATTACK1 (mode 1), then JATTACK2 and JATTACK3 (`amb-4-default-B-leap-watch.txt`, `amb-4-B-leap.png`).
- **Hook control** (`bk:hooks=ambush`): the copy also stayed at action 15 (`amb-4-hook-ambush-B.txt`).
  - Mirror.disarm writes its count-0 block with `mailbox.EnemyAnnex{}` (mods/enemysync/client.go:1279; the dump showed HookDisable 0 and hkd_hi 0 in that block).
  - em_release's handback reads em_hkd from that zeroed annex, so the hook never applies at a scope-exit handback.
  - **wind#69** has the fix. The default settings are not affected.
- **Regression** (in part: room 3's BK adopted in every visit but was not killed):
  - the room 6 pair (`9 6`, keyed setIDs 0 and 2, type 2, plus the type-4 setID 1): host kills of setIDs 0 and 1 left only setID 2 on both games;
  - r1 0xFFFF6443 (`20 1`) and r11 0xFFFF1E03 (`2 11`): after each host kill the BK was gone on both games, no ghost.
  - Side note: after the r1 kill, B's quiet death left its stick lying at the pot while A's stick fell out of the world. That only happens because `enemy-kill` reached a still-hidden ambusher.
  - The torch Bokoblin kept its stick in hand (boko mode 2, enemy-carry, in every room-7 watch).

## BB (Kargaroc)

### BB-1 adopt and look: PASS in part
- `M_NewD2 6 3`, `bb`: `enemy-match --dry --kinds bb` diffs clean before ` pid=` on A and B: set 0 type 4, set 2 type 7, both `bb_live=ok`. B adopts two kind-7 slots plus BK setID 1. fp_miss 0.
- Perched (WAIT), the copies match the host's position and yaw exactly.
- **Startle (A's bird 0x2DD poked to 3):** both played LAND01 -> LAND02 -> WAIT, with yaw within 30 units and tilt -11/-6 vs -35/-12 at the same sample.
- **In flight:** A's missed swings put set 2 into FLY01, and B's copy hovered at the same spot with FLY01.
- Not run: the screenshot pairs in flight, the 9 tail models, the beak, and the bisect. No Link could be put under a bird (void floors), so there were no flight frames to compare.

### BB-2 client hits: PASS with a difference
- `backstab.py` patched as the row says (`~/ww-shared/rigs/wind63/tools/backstab_bb.py`: current.angle.y at +0x206, warp to the bird's own y).
- B's swings at the perched set 0 gave one `ACK setID 0x0 ... applied_at/none ... health -2`. A single sword hit took the host's bird from 2 below 0, not 2 -> 1 -> 0.
- The bird died on the host and B's copy went with it (`bb-2-acks.txt`).
- One hit is the sword's own damage, not a double apply: A's own sword in BB-3 took set 2 from 2 to -2 and -6 (`bb-3-sword-A.txt`, `hp 254`, `hp 250`).

### BB-3 deaths: PASS in part
- **`enemy-kill`** on set 2: B's copy went DYING at 0.98 s and was deleted by 3.94 s, about 89 frames, the 90 light quiet frames. No item spawned on B (`bb-3-kill-*`).
- **A's sword** (`swing.py` at the hovering bird): B's copy went DYING with DAMAGEP at 4.0 s and was gone at 4.64 s, about 19 frames (`bb-3-sword-*`).
- The room-3 reload: a walk-in reload wasn't possible (room 3 can't be re-entered on foot from the rig, and a stage-hop restarts the stage's zone memory). Seen instead: after A's void-out restart, both keyed birds (sets 0 and 2) were alive again on A, while B's zone still held both deaths and B had no birds.

### BB-4 synthetic hidden gate and handback: PASS in part
- **A's 0x2F2 poked to 0xE6:** B's slot showed flags 0x40 HIDDEN. B's copy kept its own 0x2F2 0, so it keeps drawing, frozen.
- **B's copy also poked to 0xE6:** it stayed 0xE6 (the REL leaves it while hidden).
- **A poked back to 0:** B's slot flags 0x00, and the REL cleared B's 0x2F2 to 0 (`bb-4-hidden.txt`).
- **Handback** (A hopped to `0 0`, `bb-4-handback-B.txt`): 0x31A read 437 on the first sample and counted down, so the 450 seed was written. But 0x2DD read 4, not 3. The bird's own `bb_atack_move` put it straight back to its perch type, because B's Link was beyond its range (d_a_bb.cpp:1594-1600). So no attack was seen.
- Not run: the screenshot pair. Perch type 7's snap wasn't checked (set 2 had died in that load).

### BB-5 rider refusal: NOT RUN (rig gap)
- `bb+u,bk+u,mo2+u`, `M_Dra09 1 9`: the two unkeyed BKs (type 4) were adopted, diffed clean and were killed by host `enemy-kill`.
- No Kargaroc or Moblin is ever created in room 9 on either game.
- daBb_Create refuses when event bit 0x1101 is set and the switch in home.angle.z is set in the room (d_a_bb.cpp:2701). The fixture card is likely past that point; its event bits weren't read.
- A file before that point is needed: **wind#70**.
