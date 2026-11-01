# wind#19 live record 3: two Dolphins over Go (RC-10, Tier 3 leaves/saku/dry) (2026-11-01, Mac rig)

Follows records 1 and 2 (docs/live/wind19-recreate-2026-11-01.md, -b.md). Rig `wind19`, lease
`ww/dolphin-slot-6`, `mac-rig.sh pair` (Dolphin 2603-342, `WW-mp.iso`, `start-mac-1CE30685.sav`),
REL 0x1CE30685. `mac-rig.sh mp-local wind19 --coop=new --coop-refresh=<token>` with
`MP_LOCAL_PORT=0` and `MP_LOCAL_STOP_FILE`; wwdev from 5764ca6. One token per run; between runs
both files were unbound (`ww-multiplayer coop-tag clear --yes`, the rig's own card copies) and a
new world founded, so each world started with the test bit clear. The partner's "break" is a poke
of A's own switch byte (as the PC's runs did); the co-op writer carries it to B.

First live use of #242: co-op backed up `rigs/wind19/u1` and `u2`'s cards (not the default Dolphin
folders), built its tables from `ww-shared/disc/WW-mp.iso`, and took `--yes` at the bind prompt.

No crash: `crash-context` on both games caught nothing; no ISI/DSI or invalid access in either
`dolphin.log`; no panic or ERROR line in the four mp-local logs.

| Check | Result | Evidence |
|---|---|---|
| Tier 3 `leaves` (kindan r5 Obj_Leaves params 0x5FFFF, sw47) | **PASS** | sw47 cleared on both and the room reloaded (the fixture had it cut): a leaf pile on both. A's sw47 set: B logs `the partner's bit landed: asking the REL to delete the stale Obj_Leaves ... (actor_refresh 1)` once, then `actor_refresh 1: applied`; B's pile is gone, B's Link ~1980 away. A, whose own bit it is, sends no request (0 `asking` lines on A). |
| Tier 3 `leaves,dry` | **PASS** | New world, sw47 clear on both, then set on A: B logs `actor_refresh dry run: would ask the REL to delete the stale Obj_Leaves ... (bit kind 1, bit 0x2F); nothing is written` once; B's seq at 0x804137B0 stays 1; B's pile stays, its bit landed. |
| Tier 3 `saku` (kindan r3 Ksaku pair, room switches 227/228, the scoped path) | **PASS** | A's room byte 0x803C53B7 \|= 0x18: B logs `set the partner's room switch 0xE3, 0xE4 in room 3`. With B's Link 104 XZ from the barricade (1550 below it) nothing is asked and nothing logged; moved to 800 XZ: `asking the REL to delete the stale Saku ... (actor_refresh 4)` once, then `applied` (state 0x0101, both halves standing); gone on B. |
| RC-10 `kokiie` (kindan r5 flower house, sw28) | **PASS** | sw28 cleared on both, reloaded: the house hangs (y 6920) with five SHAND on both. A's sw28 set: B logs `asking the REL to re-create the stale Kokiie ... (actor_refresh 3)` once, then `re-created ... (new pid 1423)`; on B the house is at y 1380 (fallen) and no SHAND is left; A sends none. Screenshot not useful (the house is below B's camera). |
| Tier 3 `stone2` rerun on 0x1CE30685 (M_NewD2 r2 Ebrock2 sw17) | **PASS** | Second session (lease `ww/dolphin-slot-2`, new world). A's sw17 set (0x803C5385 \|= 0x02): B logs `asking the REL to delete the stale Stone2 ... (actor_refresh 1)` once, then `applied`; gone on B; A sends none. |
| Tier 3 `item` (M_NewD2 r3 daItem 0x15, item bit 3) | **PASS** | Same session. A's item bit 3 set (0x803C5397 \|= 0x08): B logs `asking the REL to delete the stale daItem ... (actor_refresh 2)` once, then `applied` (state 0x0002); the pickup is gone on B; A sends none. |
| RC-10 other rows (swhit0, warpt, mflft, vmc) and their dry runs | **NOT RUN** | One-Dolphin passes only (records 1 and 2). |

## Findings

- **Go's reach check is XZ, the REL's is 3D.** The relay holds a candidate while Link is within
  300 XZ units (`farFrom`, mods/coop/replica/writer_lock.go:108), even 1550 units below it, and
  logs nothing for that hold. Safe (stricter than the REL), but a player standing under a
  barricade on another floor never gets the refresh until they walk away. Worth one `/status`
  line, or the 3D distance, when the default-on decision comes up.
- **rf-ask during a dry relay.** One `rf-ask` was sent to B while the `leaves,dry` relay ran
  (seq == ack, so it was not refused). It asked about the already-fallen house and got
  `IN_STATE`; nothing changed. rf-ask's guard catches only a pending request, not a running relay.
