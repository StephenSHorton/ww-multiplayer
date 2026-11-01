# Runbook: actor_refresh re-create rows (RC-7..RC-10) and Tier 3 on the Mac rig

For the open rows of docs/TESTING.md "actor_refresh re-create and the table
rows" and the open "Two-Dolphin refresh" box (leaves, item, saku, `dry`).
Written for the Mac rig (wind#3's `docs/mac-rig.md`); paths are the
`~/ww-shared` layout of the Mac rules. The live results go in a record
next to this file (`wind19-recreate-<date>.md`) and as ticks in
docs/TESTING.md.

## Before any Dolphin

- Take one `ww/dolphin-slot-N` lease (a two-Dolphin run takes one for the pair).
- Your own user dirs: copy `~/ww-shared/fixtures/dolphin-user-3` and `-4`
  into a scratch dir and point `USER_DIR_1`/`USER_DIR_2` there. Never run
  on the shared originals.
- Build on main's blob (REL 0x1CE30685 at v0.6.1; check `wwdev dump` for
  the contract verdict and the REL hash). Any other REL: record it.
- After each session, sweep both players' full Player/run logs and each
  `dolphin.log` for ISI/DSI.

## The tool: `wwdev rf-ask <pid> [wait-ms=2000]`

One request by hand, on one Dolphin with no `--coop-refresh` relay
running (it refuses while a request is pending, seq != ack). It names the
actor as the relay would (`replica.RefreshAskOf`), writes the request in
the relay's order (body, then the seq), waits for the ack and prints:

```
pid 1234 Obj_Vmc 0x80500000 params 0x00002A00 room 3 pos (...): re-create, bit kind 1 bit 42 (0x2A), create room 3
before: state +0x4B8 u32 = 0 (idle)
seq 8: re-created (result 8), state 0x0000, new pid 1300
after, pid 1234: gone
after, new pid 1300: state +0x4B8 u32 = 3 (done) ...
```

`before`/`after` is the row's state field (`mailbox.RefreshRows`, which
`TestRefreshRowsMatchC` holds to refresh.c's `rf_rows`), named `idle`,
`done` or `busy` against the row. That is RC-8's "read the state field
first" check: an `idle` actor answered `GATE 9`, or a `done` one answered
anything but `IN_STATE`, means a wrong offset, width or set in the C table.
Record the before line of every row, pass or not.

rf-ask does not set the bit. Set it first, read it back, and record both
words (as LIVE-4 did): find the switch byte with `ww-multiplayer coop-tables`
(the switch users list gives each actor's stage, room, params and switch)
and `wwdev actors` / `peek`, then `poke-u32`. Restore the cards afterwards.

Result numbers: 1 APPLIED, 2 NO_ACTOR, 3 MISMATCH, 4 GATE, 5 NOT_SET,
6 REFUSED, 7 FP_MISS, 8 RECREATED, 9 IN_STATE, 10 CREATE_FAILED. Gates:
8 Link near, 9 the per-proc gate, 10 event mode, 11 event order queued.

## Per row

Each row: (1) bit clear: `NOT_SET`; (2) bit set, Link far: the row's
answer; (3) ask again for the old pid: `NO_ACTOR`; for a new pid:
`IN_STATE`; (4) the gate case; (5) a room reload afterwards: the actor in
its set state, one of it. Take a screenshot of the end state where the
row asks for one (Link warped in view, never into lava, never during a
void-out respawn).

| Row | Where (from the slice docs; confirm with `actors`) | Bit | Extra the box asks for |
|---|---|---|---|
| RC-7 vmc | WT `kaze` r3 VmcBS #0/#1 (sw34/35, trees only); r16 VmcBS #0-#3 (sw37-40, hookshot targets) | `(params >> 8) & 0xFF` | the grown tree with collision; hookshot latches (r16); Link within 300 XZ: GATE 8; mid-grow (state 1..2): GATE 9 |
| RC-8 floor | WT `kaze` FLOOR `Hhyu1`: r1 sw131, r5 sw132 (dan) | `params & 0xFF` | stand Link on it > 300 from its origin and ask: record whether it vanishes under him |
| RC-8 ice | an `Obj_Ice` block (find one: `actors` proc 0x1D2) | `params & 0xFF` | not melting (+0x458 = 0); stand Link on one too |
| RC-8 wall | a bombable WALL (proc 0x1B1) | `params & 0xFF` | +0x5AC 0 standing |
| RC-8 mjdoor | a Moblin-wood door (proc 0x47; FF has MjDoor readers, coop-slice-ff.md:194) | `params & 0xFF`, current room | |
| RC-8 mkie, mkiek | ET (`M_Dai`) light statue (0x4D, sw `params >> 16`) and light wall (0x4E) | per row | |
| RC-8 mknjd | ET/WT song tablet `MknjD` (0x4F) | `params & 0xFF` | state 0 or 8 idle |
| RC-8 vfan | GT `GanonM` r0, Phantom Ganon's door (0x38) | `params & 0xFF` | |
| RC-9 | any row: ask while Link opens a chest or talks | | GATE 10 or 11 until the event ends, then the row's answer |

RC-3..RC-6's leftovers ride along when the rig is in those rooms: a timed
SWHIT0's REFUSED (kindan r13, GanonM) and the door reacting; a type-0
WARPT (GanonK); MFLFT seen dropped and GATE 8; KOKIIE climb and GATE 8/9.

## Two Dolphins (RC-10 and the Tier 3 box)

`mp-local --coop` on bound fixture cards, `--coop-refresh=<token>` on B
only, one token per run, on a bit `coop-tables` marks synced. A breaks,
lights, cuts or takes its copy; B, far from its copy and in no event,
logs `asking the REL to <verb> the stale ...` once, then the answer. B's
own breaks send no request. Then `<token>,dry`: one `would ask` line per
candidate and the seq at 0x804137B0 does not move. `/status` shows the
`actor_refresh (...)` part.

Tier 3 tokens owed (coop-remaining §2C): `leaves` (FW r5 leaf pile #105
sw47), `item` (a DRC rupee), `saku` (FW r3 Ksaku pair sw227/228, the
scoped path), and `dry`. #199 rewrote refresh.c into tables, so the old
stone2 pass is re-run on this REL too.

## Toward default-on

`--coop-refresh` defaults to none, and coop-remaining's B2 recommends it
stay none while RC-* are open. The record should end with one line per
token: passed live (which rows), passed in part, or not run, so Stephen
can decide per token. Raise that as one board_block on the card with the
recommendation; don't flip a default in this card.
