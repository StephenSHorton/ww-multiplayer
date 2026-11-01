# wind#15: soft reset SR-1 and SR-2 on the Mac rig (2026-11-01)

Rig `ridge` (docs/mac-rig.md), lease `ww/dolphin-slot-2`. Dolphin 2603-342 (macOS, 48 MB MEM1),
`WW-mp.iso` (BlobHash 0x04765DD0, REL 0x1CE30685), wwdev at main 88794f6 plus `MP_LOCAL_PORT`
(#229; mp-local on a free port because another rig held :25565). Both Dolphins started from
`start-mac-1CE30685.sav` (Hyrule, in play). Peer boats on (default): both games draw the other's
King of Red Lions in Hyrule's basin (drawn counter at 0x804138EC rising about 30 a second, error word 0).

## SR-1 reset to the title (rerun on this build)

- `wwdev input 0x1600 0 0 1500` (Start+B+X) on A with mp-local running: A went to the title with
  no reboot (one `Dolphin OS $Revision` boot in dolphin.log).
- mp-local: `[puppet-A] pose feed paused: the game is resetting` then `pose feed resumed`.
- `dump` at the title: `pose[0]` 0x81FF1120 state 1 and tunic slot 0 0x81FEE300, the same as before the reset.
- File loaded by pad (9x Start, then A): each game shows the other's mini-Link and name tag. No
  ISI/DSI/invalid access in either dolphin.log.

## SR-2 reset that reboots

- **A pad reset held through the logo does nothing.** Start+B+X held (`input ... 0`) from 1 s after a
  cold boot to 14 s: no reset, the frame counter was continuous, and the game reached the title. The pad
  reset is not acted on in the logo scene, or the held combo never registers as a new press there.
  Not looked into further.
- **The reboot, by poke.** On a cold boot, `mReset` (`*0x803F6968` = 0x80414000, +0) was poked to 1
  every 250 ms from about 2 s. The logo scene took the reboot path: the frame counter at 0x80412F00 fell
  147 -> 22, and dolphin.log logs `--- OSReboot ---`. The poke stands in for the press (`onReset`
  stores the same 1).
- **Before a file loads**, `dump` shows REL not linked, progress 1, `pose[0..2]` buf 0 and state 0,
  tunic state 0 and buf 0 (`main01_init` zeroed them).
- **File loaded by pad, mp-local restarted** (pids pinned): `pose[0]` 0x81FF1120 state 1, tunic slot 0
  0x81FEE300 enabled. These are the same addresses as the boot before. A fresh heap on the same
  path lays out the same, and the states were 0 in between, so these are new allocations, not stale
  pointers. Each game sees the other's Link and name tag, and B's boat is drawn on A again.
  No ISI/DSI.
- **Not covered:** mp-local cannot run across a cold boot (`waitForReady` needs both games in play),
  so "pose feed paused through the reboot" cannot be seen on this path. A reboot *from play* with the
  feed live needs the disc-cover path (a reset with the cover open), which the rig cannot drive
  unattended. That is still BT-9's reboot half.
