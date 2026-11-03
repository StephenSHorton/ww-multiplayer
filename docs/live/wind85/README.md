# wind#85: quantized poses on the joiner's link (Mac rig, 2026-11-03)

One agent (walnut), rig `wind85`, lease `ww/dolphin-slot-2`, branch `wind85-pose-int16` on top of wind#84's
`mac/wind84-bandwidth` (PR #315). REL 0x3D7FEC97 (`WW-mp.iso`), Dolphin 2603-342.

The pose was still about 90% of the joiner's traffic after wind#84: 42 joints of 48-byte float matrices, about 31
frames a second each way. This card sends them as int16.

## The format (protocol 9)

`MsgPose`'s header byte 2, a pad before, now names the block's format (`mods/multiplayer/net/protocol.go`):

- **1, int16:** a 12-byte origin (joint 0's translation, 3 x f32), then 24 bytes a joint. Those are 9 x i16 rotation
  elements at 1/8192 and 3 x i16 translation offsets from the origin at 1/32 unit. For Link that is 4 + 12 + 1008 =
  1024 bytes against 2020.
- **0, raw:** the old 48-byte matrices. A sender uses it for a frame in which any value does not fit: a rotation
  element of 4 or more (a scaled joint), a joint 1024 units or more from joint 0, or a NaN or infinity.
- `DeserializePose` always hands back raw GameCube Mtx bytes, so puppet-sync still writes them straight into the
  pose buffer. The server checks a payload and relays it byte for byte (`PoseRelayFrame`), so a quantized pose stays
  quantized for every receiver.
- The step is far below what shows: 0.00012 in a rotation element and 1/32 unit in position. Deltas against the
  last frame would cut more, but relay scoping drops frames per receiver, so each frame has to stand alone.
- An older build would read the format byte as pad and take the int16 block for raw matrices, so `ProtocolVersion`
  goes from 8 to 9 and a mixed pair is refused at the hello.

Tests: `pose_quant_test.go` covers a Link-like rig at three world positions (up to +-300000, as far out as the Great
Sea goes): the frame is 51% of raw and every value comes back within one step. It also covers the four raw
fallbacks and an unknown format. `FuzzDeserializePose` now checks that re-sending a received pose keeps it within
one step, and `FuzzParsePoseRelayMessage` that the relay is byte for byte.

## Measured

wind#84's battery (`measure.sh`, with `docs/live/wind84/ts.py` and `analyze.py`): two Dolphins from the pre-Gohma
save, `mp-local --coop=new`, `MP_WIRE_STATS=5`. The phases are idle at sea by Dragon Roost, then `smooth-steps` with
each Link walking, then both in DRC room 6 for 30 s. `base` is wind#84's build (#315's head) on the same Mac the same
hour. `q1` and `q2` are this branch. The rows are PlayerB's (the joiner's) mean kbit/s. The raw lines are in
`results.txt`.

| Stream | base: sea idle | sea walking | DRC room 6 | q1: sea idle | sea walking | DRC room 6 |
|---|---|---|---|---|---|---|
| up pose | 544 | 568 | 532 | 282 | 281 | 272 |
| down pose | 531 | 530 | 538 | 283 | 266 | 271 |
| **total up / down** | 557 / 576 | 580 / 574 | 545 / 679 | 294 / 327 | 294 / 310 | 284 / 411 |

`q2` agrees with `q1` within 5% (up pose 286 / 272 / 278, down pose 285 / 280 / 270).

- **Pose bytes fell 48 to 51%** in every phase, as the format predicts (1041 against 2037 bytes a frame with the
  face trailer, plus the frame header). Almost no frame went raw.
- **The joiner's whole link** went from about 1.15 Mbit/s to about 0.62 (sea) and from 1.22 to 0.70 (DRC), **46%
  less**. What is left is the pose (about 270 kbit/s each way) and, in a room with enemies, enemy STATE (123 kbit/s
  down, unchanged).

## No visible regression

All three runs held 30 fps on both Dolphins at the start and end, and both `dolphin.log`s were clean.

| Run | A walks, seen on B: cv / stalls / jumps / jerk / behind | B walks, seen on A |
|---|---|---|
| base | 0.370 / 1 / 3 / 6.68 / 42 | 0.380 / 0 / 2 / 6.54 / 47 |
| q1 | 0.320 / 0 / 1 / 3.82 / 40 | 0.404 / 0 / 2 / 7.14 / 49 |
| q2 | 0.325 / 0 / 1 / 4.39 / 40 | 0.379 / 0 / 2 / 6.55 / 51 |

The quantized runs sit inside the same-hour baseline's spread, and inside wind#84's (cv 0.29 to 0.36 on loopback).
Screenshots of the copies at sea and in DRC room 6 (on the card, not committed) showed the pose, tunic and name tag
as before.
