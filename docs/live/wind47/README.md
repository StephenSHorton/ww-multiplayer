# wind#47: enemy rows that need aimed items or a wake trigger (Mac rig, 2026-11-01, walnut)

**Build.** main 2596369 (REL 0xA9DE0F1E, BlobHash 0x04765DD0). The tools were built from it and given the debugger entitlement (`~/ww-shared/rigs/wind47/bin`).

**Rig.**
- Shared `WW-mp.iso` and start save `start-mac-A9DE0F1E.sav`.
- `mac-rig.sh pair wind47`: A = 60998 (host), B = 61049 (client). Lease `ww/dolphin-slot-3`.
- `mp-local` with `MP_LOCAL_PORT=0` and every kind on: `WW_ENEMY_MIRROR_KINDS=mo2+u,bk+u,cc+u,ki+u,am+u,am2+u,tn+u,bb+u,mt+u`.
- **Taint, 18:48:33-18:48:56Z.** Another agent's Mac app (harbor, wind#13; since fixed in #274) attached to A by mistake and armed an enemy client on it. Two things followed:
  - Host Moblins setIDs 0 and 2 froze.
  - The first vanilla guard run overlapped that window.
- I discarded that run, restarted `mp-local` (shadow_mode back to 5 on both games) and re-hopped both games to `M_Dai 1 1` before every result below.

## Moblin guard (`M_Dai 1 1`, host Moblin setID 2): the guard is honoured

### Why wind#42 was inconclusive

wind#42's "guarded" half held R with the **sword sheathed**. With the sword sheathed, R is `procCrouch` (mCurProc **0x0E**, CROUCH), not a guard. d_a_player_main.cpp:4268-4282:
- R starts CROUCH_DEFENSE (0x0C) only when an item is in hand (mEquipItem != NONE). On B with the sword out, mEquipItem reads 0x0103.
- `checkPlayerGuard` (:1038) is CROUCH_DEFENSE, a guard slip, or the upper ATNG anime. So the REL's guard bit never applied.

`rig/stand.py` and `rig/guard2.py` now draw the sword (B, 150 ms) before they hold R. `guard2.py` draws it 600 units from the Moblin, so the draw never swings at it.

### Vanilla baseline: the host's own Link (A), the same Moblin, healer off

From `s1-guard-vanilla-life.txt`:

| Stance | Result |
|---|---|
| Guarded, held in place 120 units in front (AG2, AG3; 50 s) | Life 80 -> 80. mCurProc 0x0C throughout, with five guard slips (0x0D, blocked swings) in AG2. |
| Unguarded, the same spot (AU2, AU3; 50 s) | Life 80 -> 60 and 80 -> 68. Spear hits are -4 with LARGE_DAMAGE (0x68/0x69); the small hits are -1 (0x66). |

### Client (B), host Moblin's hits by PHIT/PACK, healer off

From `s1-guard2.txt`, `rig/guard2.py`:
- B is placed in front of the host Moblin from its position and facing as read on A.
- The host's attack fields (+0x2060/+0x2064) are sampled every 0.15 s to show the Moblin was swinging.

| Phase | PACKs |
|---|---|
| U1, U2 (unguarded) | 9: **7 `applied`** (-4 each, or -1), 1 `no_cross`, **0 `guarded`** |
| G1, G2 (mCurProc 0x0C) | 15: **6 `guarded/none life +0`**, each a 0x0C -> 0x0D -> 0x0C guard slip on B; 7 `applied`, 2 `no_cross` |

**PASS: the client honours the guard.**
- `guarded` ACKs appear only while B is in CROUCH_DEFENSE, and they cost no life.
- Unguarded, every crossing hit applied.

Some hits still land through 0x0C on both games:
- On A: AG1, while re-warped mid-swing.
- On B: 7 of 15.

So a Moblin swing can come through a crouch guard. One likely reason is a hit from outside the shield's arc while the Moblin circles. The run doesn't measure what share of hits get through on each game against the same swings; that would need the hit angle at PHIT time.

Side note, not a finding: the host Moblin's health fell 10 -> 2 over G1-G2 without any client ACK.

## AM2 (Armos), `kaze 4 4`: a client back-stab is one hit

All 7 keyed Armos adopt, at the same addresses on both games.

**The hit.** B used `rig/behind.py <copy> 6 55` with the sword: 55 units behind the copy, from its facing.

| setID | ACK (`s2-am2-backstab-acks.txt`) | Count, A and B |
|---|---|---|
| 0 | `applied_at/direct ... health 0` (from 2) | 7 -> 6 on both |
| 6 | `applied_at/direct ... health 0` (from 2) | 6 -> 5 on both |

**Timing** (`s2-am2-backstab-mon.txt`, both games sampled every 0.5 s):
- The host's health reads 0 at 30.9 s.
- The host keeps the Armos about 4.6 s: the dance window. Its mode stays 1 and its shrink timer 0.
- The host deletes it at 35.5 s. B's copy reads health 0 at 35.6 s and is gone by 36.1 s, about 0.5 s after the host.

**Against the row.**
- The row says "two hits, then the dance". On this build one direct back hit is enough (the B1 direct apply, as wind#18 and wind#42 saw).
- The spin itself was not caught on B's screen (`s2-am2-B-after-hit.png`). That is an observation, not a defect claim.
- A vanilla back-stab from A was not reproduced: the Armos turns to face the host Link it chases.

## AM2 statue handback, kaze room 9: PASS

`mp-local` ran with `WW_ENEMY_MIRROR_KINDS=am2` only, so the ChuChus don't hold the slots. Both games were in `kaze 9 9`. From `s3-statue.txt`:

**Adopted.** The host lists 3 Armos (setIDs 12, 13 and 14, params 0xC1010AFF). On both games they are mode 12, action 1, WAIT. B adopts all three (`adopted 0111`) and pauses them: `K1 held: paused: mirror`.

**Handback.** A hopped to `kaze 4 4`; B stayed in room 9. 12 s later and again 8 s after that, B's three read:
- `K1 ok`, released;
- still mode 12, action 1, at the same positions;
- animation rate 0.

They are back as statues and none woke. B's frame: `s3-B-statues-handback.png`.

## TN (Darknut)

### B* bcks after a client armor break, `kaze 8 8`: PASS

**The hits.** B's client hits on setID 0 (`rig/swing.py`, `s4-tn-acks.txt`):
- `applied_at` 15 -> 13 -> 11 -> 9 -> 7 -> 5 -> 3 -> 1.
- Then `health 10`: the armor break.

**On both games** (`enemy-watch --kind tn`; `s4-tn-bcks-A.txt`, `s4-tn-bcks-B.txt`):
- `pieces 0x0B [ARMOR B3 HELMET]` -> `0x0A [B3 HELMET]`, at 1.65 s on A and 1.81 s on B.
- After that, B's copy plays the host's B* bcks in the same order, each about 150 ms after the host: BRUN1, ASURI_FB1, BAT_YOKO1, BDOWN_A1, BOKIRU_A1, BAT_KERI1, BSURI_SFB1, BAT_SYOUTEI_S1, AKYORO1, BRUN2.
- The copy's own health byte stays 15. Health is the host's.

**The kill.** The host's setID 0 later went 10 -> 6 -> 2 and died. B logged `proc 0x00C0 setID 0x0000 ... died here; its death switch 0x87 set`, the T3 quiet death. Not every one of those hits has a client ACK.

### Cave kills, `Cave11 0 9` (`tn+u`): PASS

- B fills its four slots with the four unkeyed Darknuts (30 hp, armored).
- B's sword hits gave `applied_at` ACKs down through the armor-break reset (2 -> 20) and on to `health 0` (`s5-cave-kill.txt`).
- The Darknut went 4 -> 3 on both games. B's slot 0 emptied (`adopted 1110`); the canaries stayed 0.

## KI (Keese), `M_NewD2 7 4`: wake and ceiling return PASS; LUNGE not seen

### The wake trigger

`ki_wait_move` (d_a_ki.cpp:282-330), behaviour 1:
- A wait-type Keese wakes when player 0 (the host Link) is within `mMaxAttackMoveDist300` horizontally **and moving**: the player's position change is over 1 unit per frame.
- 10 frames later it cries WAKEUP.
- 40 frames after that it attacks only if the player is still moving.

wind#18 warped A under the Keese and stood still, so nothing woke.

### The run

A walked under the five ceiling Keese with stick input (1.5 s each way), with B parked away. From `s6-ki-watch-*-changes.txt`:

| | A (host) | B (copies of setIDs 0, 2, 3, 4) |
|---|---|---|
| Wake | All five: WAIT1 -> FLY1 at 4.8 s (action 10) | The same bcks at 4.9 s |
| Dive | FLY2 from 5.4 s | FLY2 from 5.5 s, about 70 units behind in position |

- **Ceiling return.** In the next 40 s window all five were back on the ceiling (`s6b-ki-watch-*-changes.txt`): action 0, WAIT1, at new ceiling spots (y about 3850). B's copies read the same positions.
- No ERR_KEY in the run.
- B's unadopted setID 1 stays at its home on B: B's own Keese only wakes for B's own Link.

**LUNGE: not seen.** In about 5000 samples the Tg radius stayed 40 and the Co 80 on both games. None of the Keese dives reached a Link, so the shrink to 20/0 never ran.

**Fire Keese pair: not run.** `M_NewD2 0 7` stays switch-hidden, and no verified hop shows the flame (as before).

## AM (Armos Knight): recorded as not drivable on this rig

- **Eye arrow and light arrow.** The bow fires in first person, along the camera's aim. Blind input can't aim it at a moving eye: the rig has warp and facing pokes, but no first-person aim.
- **Grappling Hook.** The start save has none.
- **Client bomb swallow.** It needs a client bomb in the open mouth. P3 passed it on the PC with the bomb placed by poke (handoff LIVE-6). A Mac rerun would need bombs on B and the same placement script.
- **`am:hooks=special`.** It switches the swallow off, so it waits on the swallow.

The recommendation stands: AM stays off by default.

## Sweep

- Both Dolphin logs: no ISI, DSI or invalid access. `crash-context`: none on either game.
- `mp-local` (four runs): 0 ERR_KEY, panic or reject lines.
- Canaries flat: PauseReasserts, AnmUnresolved and RevalidateFailures 0; canaries2 all 0; WatchdogReleases +1 per load.
- 30 fps on both games at the end.
