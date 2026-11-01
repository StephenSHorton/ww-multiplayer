# wind#9 live: co-op for three or more, C3 (Mac rig, 2026-11-01)

Build: main 9ed41df (#232, schema 2), wwdev and ww-multiplayer built from it and entitled; then
C's ww-multiplayer rebuilt with this PR's console fix for the warps below. Disc
`~/ww-shared/disc/WW-mp.iso`, code hash 0x04765DD0. Leases `ww/dolphin-slot-5` and `-6` (three
Dolphins). All three from `start-mac-1CE30685.sav` (file "Abahbob", Hyrule).

- A (Aryll, PID 87556) and B (Tetra, 87611): rig `wind9`, `MP_LOCAL_PORT=0 mac-rig.sh mp-local
  wind9 Aryll Tetra --coop=new --yes`, world e5e14b8bd95d563c, server on 127.0.0.1:49213.
- C (Medli, 87628): rig `wind9c` (`mac-rig.sh one`), `WW_DOLPHIN_PID=87628 USER_DIR_1=<wind9c>/u1
  ww-multiplayer join 127.0.0.1:49213 Medli --coop --yes`, its console fed from a file so the
  run could type `/status` and `/warp`.

All three bound and armed in one world (co-op 1, 2, then 3 for Medli).

## C3-1, passed in part

A hopped `M_NewD2 6 3`, B `M_NewD2 7 4`, C stayed in Hyrule. C's `/status`:
`co-op: Medli:   partners: Aryll in M_NewD2 room 3; Tetra in M_NewD2 room 4`. Each partner was
kept apart and by name. The part "C leaves: A's and B's lines drop C alone" was not checked from
A or B (no console on mp-local in the background).

## C3-3, found a bug, fixed, then passed

- Bare `/warp` on C: `say which partner to warp to: Aryll, Tetra`.
- `/warp to Tetra` on the merged build: `not now: a warp waits for its tick`, every time. The
  console asked `Escape(EscapeWarp)` first and then `WarpTo`, so an untargeted warp was queued
  and refused the named one (and itself fails with two partners). The app's Warp to on a
  Players row goes the same way. Fixed in this PR (console.go: `WarpTo` alone for a named warp);
  `TestCoopSessionM3` now warps with `/warp to Tetra` and fails on the old console.
- With the fix, across stages (C in Hyrule, Tetra in DRC): `going to M_NewD2 by point 6 into
  room 3, layer -1, Tetra's entrance (as the partner came in by it), then onto Tetra`, then
  `warping to Tetra at (-5169, 500, 1596) in room 3`. (A first try said the room of point 7 was
  not known: C had reconnected after Tetra's arrival, and a reload by the same entrance is not
  learned, by design; Tetra coming in by point 6 taught it.)
- Same stage, partners apart: B moved to (-5019, 500, 1596). `/warp to Tetra`: C at
  (-5035, 500, 1596), B's spot; A untouched at (-5169, 500, 1596). `/warp to Aryll`: C back at
  (-5169, 500, 1596), A's spot; B untouched. Screenshot from C: Aryll's mini-Link with its name
  tag where C stands (`~/ww-shared/runs/wind9-c3/c3-medli.png`).

## C3-4, in part

C's console was restarted twice. The hub logged Medli joining again as co-op 4 (then 5), and the
warps above used the reconnected C. A's and B's view of the reconnect (one Medli in their
partner lists) was not read.

## Not run

C3-2 (store scope with any partner, a switch set by one partner reaching another while a third
is elsewhere).

Crash sweep: no ISI, DSI or invalid access in the three `dolphin.log` files; no panic in the
session or join logs. Dolphins stopped by PID; leases released. Logs in
`~/ww-shared/runs/wind9-c3/`.

## Second run: C3-1, C3-2 and C3-4 (with #243's fix)

Three Dolphins again (leases `ww/dolphin-slot-4` and `-5`): A 38285, B 38336, C 38450, all
consoles fed from files, so A's `/status` could be read too. World 45a06e76caa16459 hosted with
`--coop-iso ~/ww-shared/disc/WW-mp.iso` (see "Stage tables" below), C joined with the same flag;
all three armed with room puzzles on (103 latching dan switches in 6 tables).

- **C3-2 passed** (DRC's one synced dan switch, 0x80, the `takara4` chest; `mDan.mSwitch[0]` at
  0x803C53A8 bit 0). A in M_NewD2 room 3, B in room 4, C in Hyrule. B set 0x80: A logged
  `set the partner's dan switch 0x80 (0x803C53AB |= 0x01)` within seconds; C's word stayed 0.
  Then B left for Hyrule and C hopped `M_NewD2 6 3` with A still there: C logged the same `set
  the partner's dan switch 0x80` and read 0x00000001. The store reached C through A, a partner
  in scope, after the switch's setter had gone: the "any partner" rule of item 171.
- **C3-1 passed**: A's `/status Aryll`: `partners: Medli in M_NewD2 room 3; Tetra in Hyrule room 0`.
  After C's process ended for good, A's `/warp Aryll to Medli`: `no partner Medli here (partners:
  Tetra)`: C dropped alone.
- **C3-4 passed**: C's join restarted (the hub: Medli co-op 3, then co-op 4). A's `/status`
  afterwards: still `partners: Medli in M_NewD2 room 3; Tetra in Hyrule room 0`, Medli once.

Crash sweep: no ISI, DSI or invalid access in the three `dolphin.log` files; no panic.

### Stage tables on the shared Mac

Without `--coop-iso`, co-op finds its stage tables through "the disc last patched", which is
one setting for the whole Mac user: any agent's `patch` moves it. In this run it named another
agent's deleted scratch ISO, so the host had no stage tables and turned room puzzles, memory
switches, keys and kills off for the session (NOTICE SCHEMA to each joiner). On the shared rig,
pass `--coop-iso ~/ww-shared/disc/WW-mp.iso` to `mp-local --coop` and to every `join --coop`.
