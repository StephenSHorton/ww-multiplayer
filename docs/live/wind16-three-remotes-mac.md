# wind#16 live: three remotes and relay scoping (Mac rig, 2026-11-01)

Build: main 2176bf8 (plus the RS7 doc fix on this branch), REL 0xA9DE0F1E, code hash 0x04765DD0;
wwdev and ww-multiplayer built from it and entitled into `rigs/wind16/bin`. Leases
`ww/dolphin-slot-1` and `-3` (a three-Dolphin run takes two slots, docs/mac-rig.md). All games
from `start-mac-A9DE0F1E.sav`. Players: marble (holder, A + B), marten (C, first join), moss
(standing by for a fourth game; RS7 doc fix f30d28e).

- A (Aryll, PID 28805) and B (Tetra, 28817): rig `wind16`, `MP_LOCAL_PORT=0 mac-rig.sh mp-local
  wind16 Aryll Tetra`, server 127.0.0.1:64505. `blob-verify-live`: only T2 data runs differ.
- C (Fake3): rig `wind16c` (`mac-rig.sh one`), `ww-multiplayer join 127.0.0.1:64505 Fake3`
  (Dolphin 42789, later 10678 after C was killed by the Moblins).
- Fake4: `wwdev pose-fake-loop Fake4` cloning B (PID 28817).
- `fps`: 30-31 on all three throughout.

Logs and screenshots: `~/ww-shared/runs/wind16/` (mp-local.log, join*.log, rw*.log, *.png).

## Results

| Row | Result |
| --- | --- |
| R3-1 | pass: link slots 0-2 armed on A and B; A's dump layout 3, three remote tunic buffers, pose[0..2] state 1 after two hops (Hyrule -> M_NewD2 -> Hyrule, every slot re-armed, all `shown`); Fake5 on actor-puppet slot 3 |
| R3-2, R3-4 | not re-run (passed 2026-10-27 on the PC build) |
| R3-3 | in part: C in link slot 2 at M_Dai 1 1; bodies lite LIVE for B and C (Fake4 none, no place), keeper `proc 0x00B5 ok`; C's sword hit on setID 2 `ACK ... applied_at/none` (not HD_NO_BODY); the Moblins latched onto C's body (`l2 t1`, attacker_overrides) and hit it 30 times. B's own hit not landed by blind input |
| RS1 | pass |
| RS2 | in part: B away and back while A stayed: `between stages: hidden` / `is in this stage: shown`, no frozen/despawned. `WW_RELAY_SCOPE=off` repeat not run |
| RS2b | in part: A away 8+ s and back: B and C shown at once, no frozen/despawned/resumed. B paused (SIGSTOP 3.5 s, 6 s): **no** `frozen: player N pose stalled` on A |
| RS3 | not run (needs a sailing B) |
| RS4 | pass (room change by stage-hop 0 <-> 4 in DRC, both directions): copies released at once, `lists 5`, never `lists 0`, no stale STATE |
| RS4b | pass: no `only thinned STATEs` all session |
| RS5 | in part: PACK client 2 seq 1-5 in A's room; none while B was in room 0; back in room 1 two Moblins re-latched body0 within seconds but no hit in 50 s |
| RS6 | in part: C's join SIGSTOPped 18 s: the whole join reconnected (enemy client 7) and C's body came back on the host (slot 2 s3 -> s2). Timing and the lingering-old-connection case not measured (B's enemy client lives in mp-local) |
| RS7 | pass: C in Hyrule, A+B in M_Dai: C hid both; A drew no Fake3 at C's last spot; C's pose-relay socket 15.3 -> 3.4 KB/s |

## Findings (carded on wind#16)

1. **A remote that leaves under relay scoping gets no hide line.** When C hopped M_Dai -> Hyrule
   (via M_Dai room 0), A and B logged nothing for C after `is in this stage (M_Dai): shown`.
   C was not drawn on A (screenshot `rs7-A-ghost.png`), so the effect is right, but the log no
   longer says why; RS2's lines only appear when the leaver's between-stages packets get through.
2. **RS2b's pause check did not fire.** A SIGSTOP of B's Dolphin for 3.5 s and 6 s gave no
   `frozen: player N pose stalled` on A; B's own puppet feed logged `pose feed paused` / `resumed`.
   Either mp-local's broadcaster stops sending while B's frames stand still (so A sees silence,
   not a stall) or the stall needs longer. Not checked against the PC.
3. **A ~420 KB/s stream to the join that scoping does not touch.** The fresh C join holds three
   connections: pose relay (15 KB/s, scoped), enemy (4.5 KB/s) and a third at about 420 KB/s that
   stayed flat whether C shared a stage or not. The first C join showed two connections at 130 and
   4.5 KB/s, neither changing. Worth a look before calling the egress saving done.
4. **RS7's text was stale about pose-fakes.** `pose-fake-loop` relays the live place of the game it
   captures (`mods/multiplayer/feed/fake.go`), so Fake4 hid and showed with B; fixed in TESTING.md.
5. Minor: `wwdev stage-hop` prints the stage table from before the load (marten).

## Crash sweep

No ISI, DSI or invalid access in A's, B's or C's `dolphin.log`; no panic in mp-local or the joins.
Dolphins stopped by PID, leases released.
