# wind#84: what the joiner's link carries, per stream, and the trim (Mac rig, 2026-11-03)

One agent (marten), rig `wind84`, lease `ww/dolphin-slot-5`, branch `mac/wind84-bandwidth` on main dff9c29,
REL 0x3D7FEC97 (`WW-mp.iso`, the same bytes as `WW-mp-3D7FEC97.iso`), Dolphin 2603-342. wind#82 counted about
2.9 Mbit/s on the joiner's link with a proxy that sees bytes only (`docs/live/wind82/README.md`, "What degrades").
This card splits that by stream and cuts the biggest.

## How it was measured

- **Counters.** `MP_WIRE_STATS=<secs>` makes mp-local's server wrap every accepted connection in a counting conn
  (`mods/multiplayer/net/wirestats.go`) that parses the frames as they pass, both ways, and labels them with the
  connection's player name and role once its hello is read. Every `<secs>` mp-local logs a `[wire]` line per player:
  up (client to server) and down kbit/s, then each stream, biggest first, frame headers included. MsgEnemy and MsgCoop
  are split by their sub byte (`enemy/STATE`, `coop/0x03`, ...). PlayerB is the joiner: on the internet, its lines are
  its upload and download. PlayerA, the host, would be on loopback.
- **Battery** (`measure.sh`, timestamps by `ts.py`, phases by `analyze.py`). It uses a fresh pair from a pre-Gohma
  save recaptured on this REL from wind#62's `card-gohma.gci` (both cards from the same GCI). It runs
  `mp-local --coop=new` with enemy sync and co-op on (the defaults), and `MP_WIRE_STATS=5`. The phases are: at sea
  by Dragon Roost, idle 20 s; `smooth-steps 5 A`, then `5 B` (each Link walks; the other game's copy is sampled every
  frame); then both `stage-hop M_NewD2 9 6` into DRC room 6 (three keyed Bokoblins), idle 30 s. A 5 s window counts
  toward the phase it falls in whole, so the hop's loading is left out.
- **Runs.** `base` and `base2` ran the counters alone (the branch's first commit). `trim1` and `trim2` added the
  no-self-echo bit and the first dedup (send only a sample that differs). `trim3` and `trim4` ran the final dedup
  (`sendDue`, below). All six held 30 fps on both Dolphins at the start and end, and both `dolphin.log`s were clean.
  The raw lines are in `results.txt`.

## What the joiner's link carried (baseline)

The rows are PlayerB's mean kbit/s over the phase, from `base`; `base2` agrees within 3%.

| Stream | Sea, idle | Sea, walking | DRC room 6 |
|---|---|---|---|
| **down pose** | 2027 | 2028 | 2026 |
| **up pose** | 1013 | 1014 | 1013 |
| down pos | 48 | 48 | 48 |
| down enemy/STATE | 27.5 | 27.5 | 123.5 |
| up pos | 15.5 | 15.6 | 15.5 |
| up enemy/PSTATE | 3.5 | 3.8 | 4.3 |
| co-op (all subs), ping, pong | under 1 | under 1 | under 1 |
| **total up / down** | 1032 / 2102 | 1033 / 2104 | 1033 / 2198 |

That is 3.1 to 3.2 Mbit/s in all, a little more than wind#82's proxy count. Pose is 95% of it. Two things stood out:

- **Down pose was exactly twice up pose.** The joiner has two player connections: broadcast-pose, which sends its
  Link, and puppet-sync, which draws the others. Relay scoping already keeps poses off a broadcast-pose connection
  (place.go). But puppet-sync got its own broadcast-pose twin's poses (and positions and projectile events) under its
  own name, and dropped them on arrival by its self-filter (`feed.Feeder`, `fxsync`). That echo was half the
  download.
- **Up pose was about 60 frames a second of a 30 fps game.** broadcast-pose samples every 16 ms. The pose it reads
  is published once a drawn frame, so every other sample was the frame already sent: 2090 bytes each (42 joints of
  48 bytes, the 4-byte header, the 64-byte face trailer, the frame header).

Enemy STATE (27.5 kbit/s count-0 keepalives at sea, 123.5 in a room with three Bokoblins) and co-op (well under 1
kbit/s once the world is open) were small next to that.

## The trim

1. **No self-echo** (`CapNoSelfEcho`, caps bit 1 of a player hello). A puppet-sync whose self-filter is the name it
   joins with sets it (`Client.SkipSelfEcho`). The server then relays it no position, pose or projectile event from
   a connection under that exact name (`Player.relaysTo`). The bit needs no protocol bump. A server from before it
   ignores the bit and relays as before, and the client still drops the echo itself. A client from before it doesn't
   set the bit, so it gets what it always got. The CLI's `puppet-sync` with `WW_SELF_NAME` unset (the loopback
   mirror demo) sets nothing. `TestNoSelfEcho` pins all three receivers: the one with the bit, one without it, and
   another name.
2. **One send per drawn frame** (`feed.sendDue`). A sample whose pose matrices changed goes out at once. One that
   differs only in its position, place or face trailer waits 25 ms (`trailerEvery`). An exact repeat goes out as a
   250 ms keepalive (`poseRepeatEvery`), far inside the receivers' 2 s freeze and 5 s despawn timers and the 3 s
   `PlaceStale`.
   - The first version sent any sample that differed at all (`trim1`, `trim2`). It sent 31 frames a second in DRC,
     but 35 to 49 at sea while idle. The position and the ship, in the face trailer, are read live and move in the
     game's calc, while the pose is published at draw time. So two reads of one drawn frame differed there, the ship
     bobbing beside the dock above all.
   - Gating on the pose with the 25 ms floor brought the sea down to the DRC rate (`trim3`, `trim4`).
   - `WW_POSE_TEST_RATE` (`testRateLoop`, wwdev's test knob) still sends every sample.

## After (`trim4`; `trim3` within 3%)

| Stream | Sea, idle | Sea, walking | DRC room 6 |
|---|---|---|---|
| down pose | 541 | 550 | 511 |
| up pose | 540 | 536 | 513 |
| down pos | 17.1 | 17.4 | 16.1 |
| down enemy/STATE | 27.5 | 27.5 | 123.4 |
| up pos | 8.3 | 8.2 | 7.9 |
| **total up / down** | 552 / 586 | 548 / 594 | 525 / 651 |

The joiner's whole run went from 1015 up and 2105 down (`base`, 3.1 Mbit/s) to 534 up and 607 down (`trim3`,
1.14 Mbit/s), **63% less**. Upload fell 47% and download 71%.

## No visible regression

`smooth-steps` (the copy of the walking Link in the other game, sampled every frame):

| Run | A walks, seen on B: cv / stalls / jumps / jerk / behind | B walks, seen on A |
|---|---|---|
| base | 0.304 / 0 / 1 / 2.58 / 46 | 0.307 / 0 / 1 / 3.32 / 43 |
| base2 | 0.309 / 0 / 1 / 3.76 / 43 | 0.326 / 0 / 1 / 4.75 / 48 |
| trim1 | 0.286 / 0 / 1 / 1.56 / 46 | 0.341 / 0 / 3 / 5.11 / 41 |
| trim2 | 0.316 / 0 / 1 / 3.08 / 39 | 0.338 / 0 / 1 / 4.31 / 50 |
| trim3 | 0.345 / 0 / 1 / 5.12 / 41 | 0.358 / 0 / 1 / 5.29 / 51 |
| trim4 | 0.341 / 0 / 1 / 4.72 / 45 | 0.346 / 0 / 1 / 5.03 / 45 |

- No run stalled, and every run but one jumped once a walk (trim1's B walk jumped 3 times; trim2's B walk, the
  same build, once). The copies trailed by the same 39 to 51 units.
- cv and jerk sit at the top of the baseline's range, or a little above it (cv 0.34 to 0.36 against 0.30 to 0.33;
  wind#82's loopback baselines ran 0.29 to 0.36). With one pose per drawn frame, the receiver's 60 Hz ease
  (`poseLerpK`) steps toward a target that moves every other tick instead of every tick. That is no worse than the
  frames the game itself draws, but the per-tick step log sees it.
- Screenshots of the copies in DRC room 6 and at sea, on both games, looked as before: the pose, tunic, name tag
  and ship were right.

## Left as is

- **Pose is still 90% of what remains** (about 520 kbit/s each way). The next cut is the matrices themselves:
  - Rotation as int16 and the localized translation as int16 at 1/32 unit would take 48 to 24 bytes a joint, about
    half again.
  - The receiver writes the bytes straight into `mpNodeMtx`, so this needs a decode step there and a
    `ProtocolVersion` bump. Deltas against the last frame don't fit, because relay scoping drops frames per
    receiver.
  - Carded on the board as a follow-up.
- **Enemy STATE in a room with enemies** (123 kbit/s down) is the next largest. It is 62.5 Hz with up to 16 slots
  of 64 bytes. `WW_ENEMY_STATE_30HZ=1` already halves it and is off by default; it wasn't measured here.
