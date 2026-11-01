# wind#45: aimed projectile halves by input (Mac rig, 2026-11-01)

Rig `wind45` (slot 2), Dolphin 2603-342, `WW-mp.iso` (REL 0xA9DE0F1E, main b9d8b2d), the Mac
start save. wwdev built from main (has `face`, #271), signed with `scripts/cs-debugger.entitlements`.

## 1. A fire arrow lights Siren r5's torch by input (one Dolphin): PASS

The recipe, all by `wwdev` (`WW_DOLPHIN_PID` pinned, `ISO_PATH=~/ww-shared/disc/WW-mp.iso`):

1. `stage-hop Siren 0 5 --yes` lands Link at (6748, -1617, 1612) in r5's entry hall.
2. The torches stand on a raised ledge at the far end, above the flooded box pit. Walking there drops
   Link into the water; a warp in front of the ledge (z 4000) does too. The ledge's floor is at
   y -1542.2, between the torches: `warp-force 6700 -1520 4185`, 0.5 s, `warp-force-off`.
3. Bow on X: `poke-u32 0x803C4C10 0x000C0103` (X slot index 0x0C; the other bytes as read) and
   `poke-u32 0x803CA7D8 0x00000035`.
4. `face 6918 4185 --stay` (torch 0xE0, 218 units away), then `input 0x0400 0 0 0` to draw.
5. Fire arrow: `input 0x0420 0 0 0`, `poke-u32 0x80413078 0x00FF0000`, 0.2 s, poke 0, back to
   `input 0x0400 0 0 0`. One tap from normal.
6. Pitch: **in first-person aim the stick's Y is inverted: negative Y pitches up.** `-60` for 0.4 s
   looks at the ceiling; `+60` for 0.28 s back down; then `-45` for 0.1 s nudges of a few degrees.
   The shot goes to the centre of the letterboxed view (about y 535 of the 1280x1016 window
   capture), not the arrow's tip. Aim with the basket at the centre (`wind45/1-aim-e0.png`).
7. `input-release` shoots. A shot with the basket's lower rim at the centre missed (count 99 -> 98,
   nothing lit); one more nudge put the basket's middle there and lit it (98 -> 97).

Check: the room switch word `0x803C53B4` went 0 -> 1 (0xE0 is bit 0 of byte 0x803C53B7) and the
torch burns (`wind45/1-lit-e0.png`).

Why wind#41's shots missed: the bowl sits about two Link heights above the ledge, and wind#41
shot level from the pit side; the ledge is the only footing within a few hundred units.

## Two Dolphins (mp-local, projectiles on by default, no co-op)

`MP_LOCAL_PORT=0 scripts/mac-rig.sh mp-local wind45` on the pair (A 69696 host, B 69707). Both
games `stage-hop` to the same room. "B copies" is word 1 of B's projectile block (`peek 0x80413D68`).
Session end: puppet-B `12 spawned here, dropped map[]`, puppet-A `dropped map[self:12]`; no ERROR
or panic in mp-local; no ISI, DSI, invalid access or JUTException in either dolphin.log.

### 2. P5 fire arrow lights a torch on B: PASS

Both in Siren r5 on the torch ledge (A at 6700,4185, B at 6600). A shot one fire arrow by the
recipe above. B's copies 0 -> 1; the room switch word `0x803C53B4` went 0 -> 1 on **both** games
(0xE0); B's screenshot shows 0xE0 burning and 0xE1 dark (`wind45/2-B-e0-lit.png`; A's aim
`wind45/2-A-aim.png`).

### 3. P4 bomb at a bombable wall: PASS

Both in Siren r3 (`stage-hop Siren 0 3`). Bombs on X: `poke-u32 0x803C4C10 0x000D0103`,
`poke-u32 0x803CA7D8 0x00000031`. A warped to (5170, -1600, 260), faced the cracked `Wall`
(proc 0x1B1) at (5170.3, -1502.4, 420), then `input 0x0400 0 0 250` (pull) and `input 0x0100 0 0 250`
(throw). On **both** games the wall's state (+0x5AC) went 0 -> 0x01000000 and memory switch 0x30
landed (`0x803C5388` 0x11802741 -> 0x11812741, table 5's 0x2E-0x30 wall row); the other two walls
stayed 0. B's copies 1 -> 2. No co-op ran, so B's wall was broken by A's bomb copy alone
(`wind45/3-A-wall-blast.png`, `wind45/3-B-wall-open.png`: B sees the alcove and its pot).
Not run: a crystal switch (TotG's SW_HIT0 rows are timed).

### 4. P5 eye switch: NOT DRIVEN (A's own arrow never registers)

The eye switch is `Hys`/`Hys2` (proc 0x1C8, `d_a_hys.cpp`: Tg takes normal/fire/ice/light arrows,
`fopAcM_onSwitch(params & 0xFF)` on a hit; mode at +0x450, 0 wait / 1 on).
- Siren r9 Hys2 (1793, 485, -12950), switch 0x12: from the fixed platform at (3750, 350, -12250),
  about 2000 units, a fire arrow with the eye centred: no hit. The room's two fire Bubbles (`bl`,
  not K1, `enemy-kill` refuses) knock Link off the platform; the lifts (proc 0x2A) move.
- Siren r1 Hys (-5467.5, -1177.1, -2358.3), switch 7. The save has 7 set, so the eye starts on:
  cleared first (`0x803C5384` &= ~0x80, both games) and re-entered; the eye then reads mode 0.
  Five shots with the eye at the centre of the aim view, from 1100 units (fire x2, normal x1) and
  from about 500 units (normal x2): **no hit on A itself** (mode stays 0, switch 7 stays clear on A
  and B); B made a copy of each (copies 6 -> 10). `wind45/4-A-eye-aim.png` is the aim of the last
  one, `wind45/4-A-eye-after.png` after it.
So the eye half is **not driven, cause unknown**: the thrower's own shot doesn't register, which is
before any sync. It is not shown to be undrivable. The eye's Tg sphere is small: centred on the
actor's `current.pos`, radius `m_tg_r[mType]` = 35 (Hys) or 70 (Hys2) (`d_a_hys.cpp:21,101`), and
every shot was aimed at the drawn eye on the assumption that the shot goes to the view's centre at
every range (true for the torch at 218 units, not checked at 500+). Next run:
1. After one normal-arrow shot, `wwdev actors` and read the arrow's resting position (a stuck arrow
   stays an actor; no shaft was seen on the wall, so it went somewhere) against the eye at
   (-5467.5, -1177.1, -2358.3). That says high, low, wide or short.
2. Correct the aim by that offset, or shoot from a ledge level with the eye, and read +0x450 and
   switch 7 again.

### 5. P5 light arrow on a client mirror: PASS in part

Read as TESTING.md words it: B's copy of A's light arrow on B's **enemy-mirror** copy relays
nothing. Siren r1, B's enemy client adopted both Armos (`adopted 0011 [0:am2][1:am2]`). Light
arrows: `poke-u32 0x803C4C50 0x36315554` (slot 12 = 0x36), `poke-u32 0x803CA7D8 0x00000036`, three
R taps from normal (normal -> fire -> ice -> light). A shot one at the Armos at (-4047.8, -2625.0)
from (-4915, -2352). B's copies 10 -> 11, B shows the light arrow and its burst
(`wind45/5-B-light-copy.png`). Host `phits 3 packs 3` before and after; B's canaries all 0
(`HitNoCross 0 HitAborted 0`); both Armos 2/2 on both games. **But A's own arrow did not damage
A's dormant Armos either** (`wind45/5-A-light-after.png`), so this shows a light copy flying past a
client mirror with nothing relayed, not a copy that touched one.

Next run: a mirrored enemy that A's own light arrow does kill. Siren r11's ChuChus (`cc`, K1 ok;
the default mirror kinds include `cc+u`). A kills one with a light arrow; on B the copy passes
through B's mirrored ChuChu, and the host's `phits` and B's canaries stay flat while B's copy dies
by the mirror (one quiet death, no second hit).

## Rig notes

- `wwdev face x z tol mag` refuses the mag argument ("too many arguments: [60]"); `x z tol` works.
- A warp onto r9's centre pole grabs the pole; `face` walking drops Link off small platforms
  (use the default put-back, not `--stay`, there).
