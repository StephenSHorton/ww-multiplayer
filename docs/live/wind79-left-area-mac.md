# wind#79: a partner who leaves the stage (issue #9), Mac rig

2026-11-02. Mac rig (docs/mac-rig.md), rig h79, two Dolphins from
`start-mac-CAC6980B.sav` (REL 0xCAC6980B), `mac-rig.sh mp-local`, no co-op.
Both games start in Hyrule. B leaves by `stage-hop sea 0 44` and comes back by
`stage-hop Hyrule 0`. A's slot 0 `pose_seq` is read with `wwdev peek 80412FBC`
(byte 0xBD is slot 0; it is 0 when the slot is cleared, and nothing is drawn).

## Verdict: main 96803f2 already does what #9 asks

When the partner's stage loads, A hides the partner's mini-Link (no frozen
ghost), logs `player N (B) is between stages: hidden until it is back here
(...)`, and draws it again when B comes back (`is in this stage (...): shown`).
This is the stage gate in `mods/multiplayer/feed/partner.go`: the place rides
on every position, so it works with relay scoping on and off.

- Relay scoping on: 7 hides on main and 5 more away-and-back rounds, over three
  mp-local sessions and two launches of the Dolphin pair. Away: `pose_seq` 0
  and one Link on A ([away-hidden.png](wind79/away-hidden.png)). Back: B drawn
  again with its name tag ([back-shown.png](wind79/back-shown.png); both spawn
  on Hyrule point 0, so B stands on A). No frozen, despawned or resumed line.
- `WW_RELAY_SCOPE=off` on mp-local (checked in the process environment), 3
  rounds: the same gate hides B (away `pose_seq` 0) and shows it again (back
  `pose_seq` 0x40-0x42 and counting). This is RS2's scope-off repeat.
- The last 8 rounds ran a build that re-cleared a hidden slot each tick, with a
  Warn when it found one set. The Warn never fired, so those rounds behaved as
  main does, and that change was dropped.
- One early run is discarded. From 17:39:54 to 17:41:58, another agent's stray
  `mp-local` (no `MP_LOCAL_PIDS`) also drove A's Dolphin. It drew a remote
  named `--help` in slot 0, and its force-quit left slot 0's `pose_seq` at
  0x54. That is the Link and the "help" tag left standing after B's first hop
  in that window. It was not the gate.
- A partner in another room of the same stage is still drawn where they really
  are. Positions keep flowing, so there is no ghost. Stephen chose to keep
  hiding by stage only (wind#79 b1).
- A "left the area" indicator: the app's Players rows already show each
  partner's place (wind#54).
