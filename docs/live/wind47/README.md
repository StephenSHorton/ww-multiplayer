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

## Not run yet

AM2 back-stab dance and statue handback; TN B* bcks after the armor break and the cave kills; KI ceiling wake, LUNGE and the Fire Keese pair; AM bomb swallow, eye/light arrow, Grappling Hook, `am:hooks=special`.
