# wind#41: facing Link at a point, and the bow by input (Mac rig, 2026-11-01)

Rig `wind19`, one Dolphin (2603-342), `WW-mp.iso` (REL 0x1CE30685), the Mac start save
(CryZe 28-Hyrule3), lease `ww/dolphin-slot-1`. No co-op or multiplayer process.

## Why poking the angle fails

Link's facing is held in five 16-bit copies: old.angle.y (+0x1F2), current.angle.y (+0x206),
shape_angle.y (+0x20E), m34DE (+0x34DE) and an unnamed field at +0x3546. All five were found by
turning him with the stick and scanning his actor block for the new value. Poking any set of them,
all five together included, is undone within one frame. The game re-derives the facing from state
a poke does not reach.

## What works: `wwdev face <x> <z>`

While the stick is held, daPy_lk_c turns him toward m34E8 (+0x34E8), which is the stick angle
m34DC (+0x34DC) plus the camera yaw (d_a_player_main.cpp:10455-10490).

- Measured stick angles: up 0x0000, left 0x4000, down 0x8000, right 0xC000. That is
  cM_atan2s(-x, y).
- The camera yaw drifts as the camera follows him, so `face` re-reads it on every step.
- The pad's axis dead zone snaps a small component to 0. A stick at (9, -39) read as exactly
  0x8000, so a light stick can leave him up to ~13° short. That is why the close-in taps aim past
  the target.
- Five targets all around ended 30-241 units off (0.2°-1.3°). The light-stick coarse turn moved him
  0 units. Afterwards the warp put him back on the same spot every time.

## The bow by input (for P4/P5's aimed halves)

- Put the elemental bow on X: inventory slot 12 holds item 0x35. Poke the saved X slot index to
  0x803C4C11 = 0x0C and the live X item to 0x803CA7DB = 0x35.
- Hold X (`input 0x0400 0 0 0`) to draw. Link goes into first-person aim.
- Release (`input-release`) to shoot. The arrow count dropped by one per shot.
- R cycles the arrow type while drawn (`spActionTrigger` = BTN_R, d_a_player_bow.inc:152-156), but
  only with the analog trigger: `input 0x0420 0 0 0`, then poke 0x80413078 = 0x00FF0000, wait
  0.2 s, then poke it back to 0. The type stays chosen between shots.
- Order seen: normal → fire → ice. Normal → fire took one tap; three more taps from ice back to
  fire.

## Not done: a torch lit by a fire arrow (P5's open half)

- Siren r5's switch torches are `bonbori` (proc 0xBA): 0xE0 at (6918.4, -1542.2, 4185) and 0xE1 at
  (6571.2, -1542.2, 4185), latching room switches 0xE0/0xE1.
- Three fire or ice arrows were shot, from 2580 units and from about 180 units, and none lit a
  torch.
- At range the shot went wide. Up close Link faced the torch, but the first-person shot travels
  level at eye height and the torch's bowl sits above it.
- Next step: pitch the aim up with the stick in aim mode (first-person stick moves the aim), from
  about 400-600 units, and check the result by the room switch and a screenshot.
- Then the same shot on two Dolphins (A shoots, B's copy should light B's torch: P5). After that:
  a bomb at a wall or crystal switch (P4), the eye switch and a light arrow on a mirror (P5).
