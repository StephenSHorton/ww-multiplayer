# wind#87: M6.1 P1 signatures for the Boko Baba and the Armos (GitHub #132), Mac rig, 2026-11-03

Rig `wind87`, lease `ww/dolphin-slot-2`, Dolphin 2603-342. Tools built from main a41488c (branch `wind87-p1-boko-armos`),
on a rig-local ISO patched from the vanilla disc on that build (BlobHash 0xA37EC8DB; the shared `WW-mp.iso` then still
carried 0x36DB799C). Both cards are wind#29's CryZe `9-Hyrule1` (`rigs/wind29/u*-h9card.gci`), recaptured on this build
(`mac-rig.sh recapture`). Host `mac-rig.sh mp-local wind87 --coop=<world> --yes` (placed kills on by default), first with
the default enemy mirror, then with `WW_ENEMY_SYNC=0` for the Armos (`am2+u` is a default mirror kind).

## Result

| Class | P1 | What its death shows | Verdict |
|---|---|---|---|
| Boko Baba (proc 0xD7) | **failed on main, passes with the fix** | health stays 1/1, the enemyice timer stays 0; u8 +0x2C4 (the mode) reads 2 from the hit until the delete | needs a class row: `combat.DeathMode` (mode 2) |
| Armos (proc 0xCC, AM2) | **pass, generic** | health 2 -> 1 -> 0 on two back-stabs; 0/2 for about 4.6 s before the delete | no class row |

## Boko Baba (kindan r1)

kindan r1 on this file has three unkeyed Boko Babas (params 0xFFFFFFFF, 0xFFFF01FF, 0xFFFFFFFF) on both games, 5 tracked
on each side with the room's two unkeyed ChuChus.

- **Driving a sword kill.** Lock-on is the analog L trigger, which since wind#80 is at **0x80415078** (mailbox 0x80414F00 +
  0x178), not WW-MAC-RULES' 0x80413078. `jump.py` warps Link 420 units in front of the Boko Baba, holds L (`input 0x0040` +
  `poke-u32 80415078 FF000000`), then B (draws the sword), A (jump attack), B, A. From 200 units it bites first (Link held,
  `mCmd 2`); from 420 the jump attack lands. Life was topped up between tries (`poke-u32 803C4C08 001D001D`).
- **On main: `nosig`.** Two sword kills on A and one on B. Each death read hp 1/1, enemyice mode 1, shrink timer 0 at every
  4 Hz sample until the actor left the walk. Each was counted `not sent: nosig` (A `nosig 2`, B `nosig 1`), and the partner's
  Boko Baba stayed.
- **Its death mode.** Full reads of the 0xFE4-byte actor at about 15 Hz during a kill (`dumpa.sh`, `diffdump.py`;
  `bo-mode-traces.txt`) show the u8 at +0x2C4 as its mode and +0x2C5 as its action: 0 while it waits and rises
  (actions 1-3, 5 on the hit), **1 while it bites Link** (actions 0x0B-0x0E, then back to 0: four bite cycles, never 2), and
  **2 from just after the hit (actions 0x14, 0x15) until the delete** (+0x0C going 0x02 -> 0x03), about 1.9 s. A hit that lands
  mid-bite takes the same path (B's kill: 0x010B -> 0x0005 -> 0x0214 -> 0x0215). +0x2C0 differs per placement and is not used.
- **The fix.** The BO K1 class reads that byte (layout +0x2C4, u8), `combat.DeathMode` counts mode 2 as the class's death
  signature, and `placedSig` adds it to health <= 0 and the light-shrink timer. The gate also holds a Boko Baba in mode 1
  (biting Link) or 2 (dying).
- **Live on the fix** (same rig, `WW_ENEMY_SYNC=0`, both re-hopped to kindan 0 1 so all three respawned): A's sword kill read
  `class mode 2; K1 held: a Boko Baba dying (mode 2)` for about 2 s (`bo-fix-pollA.txt`). A sent it, and B logged
  `placed: light-shrunk the partner's kill of kindan room 1 ordinal 0 (table 4): actor 0x80A8BD74`, the same placement.
  Then B killed another: A light-shrank ordinal 5 (0x80A79734). Each side counted the other's as `echo`, and both games were
  left with the same last Boko Baba (0x80A7AFB8).

## Armos (Siren r8)

Every unkeyed Armos in Siren (rooms 0, 6, 8 params 0x8001FFFF; room 1 0xE5010AFF; room 11 0x22010101) starts inactive
(`mStartsInactive` = 1, d_a_am2.cpp:1305-1306) behind a switch. A dormant statue runs only `body_atari_check`
(:802-808), so it cannot be hurt. In room 8 that switch is 0x80, dan bit 0 (0x803C53A8 bit 0). Nothing in the room set it
when Link walked in, so it was poked on A (`poke-u32 803C53A8 00000001`; the Armos's +0x2CF went 0x80 -> 0xFF and it woke).
Only the wake was forced: the hits and the death are the game's own. B's Armos stayed statues.

- `stab2.py` warps Link 80 units behind the Armos along its shape_angle.y, faces its back and presses B. The Armos turns
  to face Link fast, so most swings hit its front. Two back-stabs landed in about 15 swings: hp 2/2 -> 1/2 -> 0/2.
  `K1 held: health 0: dying` for about 4.6 s, then gone (`am2-pollA.txt`).
- A's `/status` went to `1 sent`, and B logged `placed: light-shrunk the partner's kill of Siren room 8 ordinal 0 (table 5):
  actor 0x80AA4354`, the Armos at the same home (-607.4, 229.4, -11443.9), still a statue on B. B counted `echo 1`, and both
  games were left with 3 Armos. The generic signature covers it, so the Armos needs no class row.

## Notes

- No ISI, DSI or invalid access in either dolphin.log, and no panic or ERROR in either mp-local log.
- A's Link died once to the ChuChus while B was being driven. Its game-over prompt saved to the rig's own u1 card (the
  cursor was on Yes). That touches only this rig's copy, not the fixture or the start save.
- The early runs poked the stale 0x80413078 (WW-MAC-RULES) on my A and on wind89's A. On that build (a41488c) the word
  read 0 on Dolphins that were never poked, and the last poke wrote 0 back. On later builds it may be T2 code: never poke it.
- Rig slip: early on, a zsh array index (`${P[0]}`, empty in zsh) left `WW_DOLPHIN_PID` unset, so one warp and about 3 s of
  inputs went to rig wind89's Dolphin A. wind#89 was told on its card. `jump.py`, `stab2.py`, `polla.sh` and `dumpa.sh` now
  refuse to run unless `WW_DOLPHIN_PID` is one of the rig's `pair.pids`.
- The tools here hard-code the rig folder `~/ww-shared/rigs/wind87`; change `R` for another rig.
