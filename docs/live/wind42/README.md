# wind#42: enemy kinds rows owed after wind#18 (Mac rig, 2026-11-01)

**Build.** main 8c98a86 (REL 0xA9DE0F1E, BlobHash 0x04765DD0). Tools were built from it and given the debugger entitlement.

**Rig.**
- Shared `WW-mp.iso` (re-patched for 0xA9DE0F1E, #270), start save `start-mac-A9DE0F1E.sav` (28-Hyrule3).
- `mac-rig.sh pair wind42`: A = 71337 (host), B = 71373 (client), both 30 fps. Lease `ww/dolphin-slot-3`.
- `mp-local` with `MP_LOCAL_PORT=0` and every kind on: `WW_ENEMY_MIRROR_KINDS=mo2+u,bk+u,cc+u,ki+u,am+u,am2+u,tn+u,bb+u,mt+u` (the default since #261 is `mo2,bk+u,cc+u,ki+u,am2+u,tn+u`).
- Healer on both games.
- Client hits come from `docs/live/wind20/rig/swing.py` (now with `SWING_BTN` / `SWING_WAIT` for an item on X) and the new `rig/behind.py`, which places Link behind an actor from its facing angle at +0x206.

## Adoption rows: pass

From `s1-adoption.txt`. In every room the goldens are equal on A and B (`enemy-match --dry`, diffed before ` pid=`) and the canaries are 0.

| Room | Host lists | B adopts |
|---|---|---|
| `kaze 1 1` | 2 keyed Armos | both (`adopted 0011`) |
| `Siren 0 8` | 4 unkeyed Armos + 2 Keese | the 4 Armos (`adopted 1111`, all `am2 u...`) |
| `Cave10 0 16` | 3 unkeyed Moblins + 2 unkeyed Darknuts | four of the five (`adopted 1111`: 3 `mo2 u...`, 1 `tn u...`), as T4 expects for four slots |

## TN (Darknut), `kaze 8 8`: client hits, armor break, quiet death

Three keyed Darknuts (setIDs 0 and 2 at 15 hp, setID 1 at 30); B adopted all three.

- **Client hits on setID 0's copy.** ACKs `applied_at/none` at health 13, 11, 9, 7, 5, 3, 1 (`s2-tn-acks.txt`).
- **Armor break.** The next hit gave `health 10`: the armor-break reset T2 describes. B's frame: `s2-tn-armor-broken-B.png`.
- **The kill, with a caveat.** setID 0 then died on the host at health 10. B's further swings mostly caught setID 1 beside it (30 -> 22), so the last hit on setID 0 was not a recorded client ACK.
- **T3 quiet death on the client.** B logged `proc 0x00C0 setID 0x0000 ... died here; its death switch 0x87 set` (`s2-tn-death-lines.txt`). The copy was deleted on both games. B's actor count fell by exactly that one (45 -> 44): no item, no new actor.

## AM2 (Armos), `kaze 4 4`: the Skull Hammer

**Setup.** B's X got the Skull Hammer by **poke** (`poke-u32 803C4C10 00140103`; after the hop the live X item read 0x33). The swings themselves are real X presses.

| From | Result |
|---|---|
| Behind (`behind.py`, 55 units) | **One strike kills.** `ACK setID 0x1 ... applied_at/direct ... health 0`, and the same for setID 0x5. Armos 7 -> 5 on both games (`s3-am2-hammer-acks.txt`). |
| The front | `dropped/front, health 2` (twice). One strike that caught setID 5 off-axis was `applied_at/none` with health 2. |

The row says "one hammer hit kills" without naming a side; from the front it doesn't on this build. That's an observation, not a bug claimed: T5's direct apply drops front hits by design, and whether vanilla's hammer kills an Armos from the front wasn't checked.

**Not run.** The back-stab dance (two sword hits; wind#18 got one back-stab kill), the statue handback (kaze room 9).

## Moblin guard: inconclusive

- **Setup.** `M_Dai 1 1`. B stood about 150 units from host Moblin setID 2: 12 s unguarded, then 12 s holding R (`input 0x0020 0 0 0` plus the trigger poke `80413078 00FF0000` every 2 s; B is crouched behind the shield in `s4-B-guard.png`).
- **Why inconclusive.** The Moblin reached B only during the guarded half. It struck 5 times: three `no_cross/none` and two `applied/none` (B lost 1-2 quarter hearts each). With no unguarded baseline, this doesn't show whether the guard is honoured. Not ticked.

## Not run (carded)

- **AM (Armos Knight).** Eye/light arrow, bomb swallow, Grappling Hook, `am:hooks=special`. They need aimed item use against a moving target, which blind input can't do yet. wind#11 (d)'s bombs/arrows fixture exists for whoever scripts aiming.
- **KI.** The ceiling wake and LUNGE (wind#18 found no trigger), and the Fire Keese pair.
- **TN.** The B* bcks after the break (only the reset was seen), and the cave kills.

## Recommendation: AM, BB and MT stay off by default

- **AM (Armos Knight).** Adoption and host kills pass (wind#18). None of its own mechanics has passed with a client yet: bomb swallow, eye arrow, hook. A default-on AM would hand those to players untested. **Keep off** until they pass.
- **BB (Kargaroc).** BB-1 adoption and BB-3's host kill with reload pass (wind#18). BB-2 client hits and BB-4/BB-5 (the rider refusal) don't. **Keep off.** It is the closest to a flip once BB-2 passes.
- **MT (Magtail).** Opt-in since batch D, and no live run on the Mac rig. **Keep off.**

These match what #261 chose (only `am2+u` and `tn+u` flipped).

## Sweep

- Both Dolphin logs: no ISI, DSI or invalid access.
- `mp-local`: 0 ERR_KEY, panic or reject lines.
- Canaries flat at the end: PauseReasserts, AnmUnresolved and RevalidateFailures 0, and canaries2 all 0. WatchdogReleases 6 (+1 per load).
- `crash-context` on both: no crash context.
