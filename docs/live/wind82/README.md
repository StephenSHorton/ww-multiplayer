# wind#82: co-op under latency, jitter and loss (issue #15), Mac rig, 2026-11-03

One agent (pine), rig `wind82`, lease `ww/dolphin-slot-3`, branch `pine/wind82-netem` on main 96803f2, REL 0xCAC6980B
(`WW-mp.iso`), Dolphin 2603-342. Every condition is a fresh pair from wind#62's pre-Gohma save state
(`rigs/wind62/start-gohma.sav`, both cards from `card-gohma.gci`), then `mp-local --coop=new` (A host, B client,
enemy sync and co-op on by default). Both Dolphins held 30 fps at the start and end of every run listed below.

## How the link was shaped

The Mac's agent shell has no sudo, so dummynet (`dnctl`/`pfctl`) was out. Instead, `MP_LOCAL_NETEM` (new,
`cmd/wwdev/mp_local_netem.go`) puts a userspace TCP proxy (`internal/netem`) between Dolphin B's four clients
(bcast-B, puppet-B, enemy-B, coop-B) and mp-local's server. A, the host, stays on loopback, as a host and an internet
joiner would. Nothing is left on the Mac afterwards: the proxy lives and dies with mp-local, and no pf or dummynet
rule is ever written. `wwdev netem <listen> <server> <spec>` runs the same proxy alone, for a `host` and a `join` in
separate processes.

- **Delay**: each direction gets RTT/2 plus a uniform jitter in [-J, +J]. A chunk is never written before the one
  ahead of it, so bytes never reorder. TCP never shows reordering to the application either.
- **Loss**: TCP never shows a lost packet to the application, only a stall. With probability `loss` per forwarded
  chunk, that chunk and everything behind it is held one extra `rto`. With the default `rto` = RTT + max(200 ms,
  4×jitter), a lost segment is recovered by timeout. That is the pessimistic case. With `rto` = RTT, recovery is
  roughly a fast retransmit (three duplicate ACKs), which a steady 30 Hz stream usually gets. Both were run.
- **Checked**: `ww-multiplayer status` through the proxy at `rtt=150ms,jitter=20ms` took 0.15 to 0.19 s, against 0.01 s
  direct. The proxy's own counters agree: at loss 0 the mean one-way delay was 42 ms at 80, 76 to 80 ms at 150 and 134 ms
  at 250. Its counts are in each `[netem]` log line.

- **Not modelled**: TCP congestion control. Real TCP halves its window at each loss, so one flow at 150 ms RTT and 1%
  loss tops out near MSS/(RTT·√p) ≈ 1448/(0.15 × 0.1) ≈ 95 KB/s. The joiner's streams need about 120 KB/s up and
  240 KB/s down in total (below), split over four connections. On a real lossy link a busy stream may therefore fall
  progressively behind, not just stall and jump as it did here. Only a real WAN run can show that (TESTING.md NC-5).

## The battery (the same for every condition, `cond.sh`)

1. **Walk**: `smooth-steps 5 A`, then `5 B`, at sea by Dragon Roost (each Link walks; the other game's copy is sampled
   every frame). "Behind" is how far the copy trails; the walker covers about 500 units a second.
2. **Hop and kills**: both `stage-hop M_NewD2 9 6`. A's three keyed Bokoblins (setID 0-2) are killed with
   `enemy-kill` (POKE). The check is that B's mirror copies are gone.
3. **Chest race**: both warped (`ww-multiplayer warp`, POKE) 200 units either side of DRC chest 3 at once. The check
   is the host's grant and deny, and when each reaches its player.
4. **Boss relay**: B at the DRC entrance (`M_NewD2 0 0`). A in Gohma's arena (`M_DragB 1 0`) with wind#62's end word
   poked (Btd +0x2E4 = 0x000B0032). The check is the time to B's `set t03 DRC boss defeated`. This includes A's own
   death demo, about 32 s.

`analyze.py` turns the timestamped log into `results.txt`. The timestamps come from `ts.py`; log lines carry none of
their own.

## Results

| Condition (`MP_LOCAL_NETEM`) | Walk A on B: stalls / jumps / behind | Walk B on A: stalls / jumps / behind | B's copies gone | Chest: winner, deny or grant reaching B | Boss relay | Disconnects |
|---|---|---|---|---|---|---|
| none (baseline) | 0 / 1 / 38 | 0 / 1 / 47 | +11.0 s | B, grant +0 ms | +32.5 s | 0 |
| rtt 80, jitter 10 | 0 / 4 / 57 | 0 / 2 / 58 | +10.9 s | B, grant +49 ms | +32.4 s | 0 |
| rtt 150, jitter 0 | 0 / 4 / 75 | 0 / 1 / 66 | +11.1 s | A, deny +150 ms | +32.7 s | 0 |
| rtt 150, jitter 20 | 0 / 0 / 70 | 0 / 3 / 77 | +10.8 s | A, deny +177 ms | +32.5 s | 0 |
| rtt 250, jitter 30 | 0 / 3 / 97 | 0 / 2 / 98 | +10.9 s | A, deny +281 ms | +32.6 s | 0 |
| rtt 80, jitter 10, loss 1% | 13 / 13 / 81 | 11 / 8 / 62 | +11.1 s | A, deny +84 ms | +32.5 s | 0 |
| rtt 150, jitter 20, loss 1% | 21 / 16 / 114 | 17 / 9 / 110 | +11.0 s | A, deny +111 ms | +32.5 s | 0 |
| rtt 150, jitter 20, loss 5% | 9 / 17 / 172 | 27 / 19 / 146 | +10.9 s | A, deny +155 ms | +32.5 s | 0 |
| rtt 250, jitter 30, loss 1% | 32 / 21 / 158 | 26 / 8 / 119 | +10.9 s | A, deny +207 ms | +32.5 s | 0 |
| rtt 250, jitter 30, loss 5% | 10 / 16 / 243 | 26 / 16 / 180 | +11.0 s | A, deny +245 ms | +32.5 s | 0 |
| rtt 150, jitter 20, loss 1%, rto 150 ms | 0 / 2 / 83 | 2 / 2 / 75 | +11.1 s | A, deny +148 ms | +32.8 s | 0 |
| rtt 150, jitter 20, loss 5%, rto 150 ms | 0 / 11 / 105 | 2 / 3 / 79 | +11.8 s | A, deny +149 ms | +32.4 s | 0 |

The baseline smoothness is cv 0.29 to 0.36 with the walker at 16.9 ± 7.5 units a frame (uneven ground). Under the
pessimistic loss runs, cv rose to 0.65 to 1.14. The full lines are in `results.txt`.

## What works

- **No session broke.** In all twelve runs nothing disconnected, reconnected, froze or despawned before the
  planned stop. Both dolphin.logs were clean every run (no ISI, DSI or invalid access).
- **Delay and jitter alone are fine.** From 80 to 250 ms RTT with 10 to 30 ms jitter, the remote Link moves as
  smoothly as on loopback (0 stalls). It only trails further behind: 57, 70 to 77 and 97 units against 38 to 47,
  about one one-way delay of walking.
- **Kills, stage hops and the boss relay are unaffected** within what can be seen. B's mirror copies were gone about
  11 s after the pokes in every run, the light-shrink's own length. B's boss bit landed 32.4 to 32.8 s after A's poke
  in every run, A's death demo plus the co-op tick. A few hundred ms of network delay doesn't show at this grain.
- **Chest arbitration stays correct.** In every run exactly one player was granted the chest and the other denied,
  with nothing lost or duplicated. The second answer reaches the joiner one shaped one-way delay later: 84 to 281 ms.

## What degrades

- **Loss makes the remote Link stop and jump.** With losses recovered by timeout, 1% loss already gives 11 to 32
  stalls and 8 to 21 jumps per 5 s walk at every RTT. With faster recovery (`rto` = RTT), 1% is nearly clean (0 to 2
  stalls), while 5% still gives up to 11 jumps. The cause is in `mods/multiplayer/feed/feeder.go`: the receiver eases
  each frame toward the newest pose (poseLerpK 0.5, lerpK 0.2), with no playout buffer and no extrapolation. A TCP
  stall freezes the target, so the copy stops; the burst after it moves the target at once, so the copy jumps.
  Carded as a follow-up: a small playout delay and short extrapolation for remote poses.
- **The host wins near-ties.** The host's own claim crosses loopback while the joiner's crosses the internet. With
  the link shaped, A (the host) won 10 of the 11 shaped races (B won at 80 ms without loss); unshaped, B won. This is the
  arbitration working as designed: first request in, first served. It means a joiner reaching a chest in the same
  instant as the host loses it. One caveat: A's warp starts a moment before B's in `cond.sh`, so this race is not
  perfectly even.
- **The joiner's link carries about 2.9 Mbit/s** (the proxy's 30 s counts: about 11 MB per 30 s, 3.7 MB up and 7.3 MB
  down, i.e. 1.0 Mbit/s up and 1.9 Mbit/s down)
  in DRC and at sea, with enemy sync and co-op on. That is a lot for a home uplink. Carded as a follow-up: measure it
  per stream (pose matrices, enemy STATE, co-op) and trim it. Done in wind#84 (`docs/live/wind84/README.md`): pose was 95% of it (half the
  download was the joiner's own pose echoed back, and every other pose repeated a frame); the joiner now carries
  about 1.1 Mbit/s.

## Bugs

None that break play. The two follow-ups above are improvements, carded on the board (see wind#82).

## Rig notes

- The first 250 ms / 5% run was spoiled half way: the Mac's load average reached about 400 (system daemons, not
  this rig), and both Dolphins fell to 0 fps and stopped answering a normal kill. It was killed by PID, kept in `bad/`
  on the rig, and re-run after the load fell below 10. The re-run is the row above.
- `mac-rig.sh stop` sends a normal kill. A Dolphin hung like that one ignores it, and `pair` then refuses the rig
  ("a Dolphin already runs"). Check `pgrep -fl <rig>/u` after a stop.
