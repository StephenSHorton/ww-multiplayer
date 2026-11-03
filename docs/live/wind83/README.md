# wind#83: a playout buffer for remote poses, rerun of wind#82's battery, Mac rig, 2026-11-03

wind#82 found that under loss the remote Link stops and then jumps: TCP shows a lost segment only as a stall
and then a burst, and the receiver (`mods/multiplayer/feed/feeder.go`) eased toward the newest pose. wind#83 adds
a playout buffer (`mods/multiplayer/feed/playout.go`):

- The client keeps each remote's last 48 positions, each with the pose that followed it, numbered as they arrive
  (`network.RemotePlayer.Recent`). The sender sends one sample a frame, so a sample's number is its clock. Arrival
  times are used only to learn the frame period.
- The remote is shown `WW_PLAYOUT_MS` (100 ms) behind its newest sample, interpolated between the two samples around
  the playhead (position, angles the short way, pose matrices).
- The playhead runs faster while more than 100 ms is buffered (after a burst) and slower while less is (as a stall
  starts). The rate is clamped to 0.5 to 1.5, under smooth-steps' jump bound (twice the walker's median step).
- When the buffer runs dry, the copy keeps moving along its last motion for up to `WW_EXTRAPOLATE_MS` (200 ms) and
  then holds.
- The old easing (lerpK, poseLerpK) still runs on top. `WW_PLAYOUT_MS=0` turns the buffer off (the old behaviour).

## The run

The rig was `wind83` (lease `ww/dolphin-slot-3`), with wind#82's `cond.sh`, `run.sh` and `analyze.py` and paths
pointed at the rig. The build was branch `wind83-playout` on 9fa887b (#312's head), REL 0xCAC6980B, Dolphin 2603.
Each condition was a fresh pair from `rigs/wind62/start-gohma.sav`, and both Dolphins held 30 fps at the start
and end of every run. The loss runs used the pessimistic timeout recovery (`rto` = RTT + 200 ms = 350 ms), as
wind#82's did. `n150off` is the same 1% loss run with `WW_PLAYOUT_MS=0`, the old behaviour, in the same session.

| Condition (`MP_LOCAL_NETEM`) | Walk A on B: stalls / jumps / behind / cv | Walk B on A: stalls / jumps / behind / cv |
|---|---|---|
| none, wind#82 | 0 / 1 / 38 / 0.29-0.36 | 0 / 1 / 47 / 0.29-0.36 |
| none, **playout** | 0 / 2 / 84 / 0.360 | 0 / 0 / 74 / 0.297 |
| rtt 150, jitter 20, loss 1%, wind#82 | 21 / 16 / 114 | 17 / 9 / 110 |
| rtt 150, jitter 20, loss 1%, buffer off (same session) | 17 / 9 / 97 / 0.727 | 20 / 11 / 88 / 0.780 |
| rtt 150, jitter 20, loss 1%, **playout** | 0 / 6 / 126 / 0.461 | 0 / 3 / 114 / 0.381 |
| rtt 150, jitter 20, loss 5%, wind#82 | 9 / 17 / 172 | 27 / 19 / 146 |
| rtt 150, jitter 20, loss 5%, **playout** | 0 / 3 / 190 / 0.415 | 1 / 3 / 148 / 0.419 |

- **Stalls are gone.** At 1% loss there are 0 stalls against 17 to 21. At 5% there are 0 and 1 against 9 and 27.
- **Jumps** are down from 9 to 19 to 3 to 6. What is left is mostly the extrapolation being corrected when real
  samples arrive, on uneven ground.
- **cv** under loss is 0.38 to 0.46, against 0.73 and 0.78 with the buffer off.
- **Baseline smoothness is unchanged** (0 stalls, cv 0.30 to 0.36). The cost is the playout delay: the copy trails
  about 40 more units at walking speed (500 units a second, so about 80 ms more with the easing).
- **The rest of the battery** was unchanged in every run: B's copies were gone 10.7 to 11.7 s after the kills,
  the boss relay landed 32.4 to 32.5 s after A's poke, and nothing disconnected.
- **The chest race** produced no grant in any of the four runs, the buffer-off run included, so it isn't this
  change. The warp it starts (`~/ww-shared/bin/ww-multiplayer warp`, output discarded) didn't put the players at
  the chest. wind#86 found why: that shared binary was built after wind#80's mailbox move, and the save state still
  ran the old blob, so the warp went unanswered. The fixed `cond.sh` is described in `docs/live/wind82/README.md`.

The full lines are in `results.txt`.
