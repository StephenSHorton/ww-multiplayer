# wind#58: `wwdev boss-end`, every GT D3 fight ended the game's own way (PC, 2026-11-01)

wind#44 ran GT D2 with stand-in wins (event-bit pokes), because nothing could end these fights:
`enemy-kill` refuses in a refight (`gate.idleClosed`, RefightStageNo != 0) and refuses Fganon ("not a K1 class"),
the REL's boss request answers R_GATE in X stages (`inject/rel/bosskill.c:296`), and Ganondorf ends in a demo, not a death.
`wwdev boss-end` writes only the boss actor's own fields (the same writes the game's lethal hit makes), so the game
runs its own ending: demo, warp, and the real event/switch bits. It never writes the save, event bits or the REL request.

Recipes: one decomp reader per boss, each finding checked by a skeptic against `~/projects/tww`; the corrected
orders are in the code comments (`cmd/wwdev/boss_end.go`).

## Rig

Windows PC, one Dolphin 2609a, branch `pc/wind58-boss-end` (on main eb7948f, REL 0xA9DE0F1E), disc patched from the
vanilla CISO by this build. Save: CryZe's `28-Hyrule3` (100pct-E), recaptured on this REL with `auto-recapture`
(keyboard-hotkey fixture user dir). Each fight reached with `stage-hop`; dev log on (`OSREPORT`, `MI`, `MASTER`).

## Results (all live, one Dolphin)

| boss | stage | writes | what the game did | bits seen | time |
|---|---|---|---|---|---|
| Gohma refight | Xboss0 | Btd+0x2E4 = 000B0032 | end()'s X branch: warp to `GanonA` point 5 room 1 | 0x3240, tmp 0x0480 | 2.6 s |
| Kalle Demos refight | Xboss1 | `--step flower` (force.sh gate), then the lethal block, mMode 0xB last | warp to `GanonA` 5/1; the wipe beat the heart container and Makar (neither was created) | 0x3220, tmp 0x0480 | 2.6 s |
| Jalhalla refight | Xboss2 | health 0, then +0x444 = 00030064 | 0x65 spin, landing 4/0x6E, 0x6F X branch, warp to `GanonA` 5/1, no Poes | 0x3210, tmp 0x0480 | 4.2 s |
| Molgera refight | Xboss3 | hit counts, m18AE=3 before m18B0=2 | m18AE 0xC, warp to `GanonA` 5/1 | 0x3208, tmp 0x0480 | 2.9 s |
| Maze Phantom Ganon | GanonJ r13 | mAction 3, then mMode 0 | fail() death (modes 1..4), the Light Arrow chest appears | room 13 switch 232 | 6.0 s |
| Third Phantom Ganon | GanonM r0 | mAction 22 first, then mMode 0 | last_end: demo mB54 101..103, then the actor deletes itself | table 8 switch 0 | 10.4 s |
| Vfan door (`pg3 --step door`) | GanonM r0 | Vfan+0x440 = 1 | its own break event | 0x3A08, table 8 switch 24 | 5.2 s |
| Puppet Ganon form 1 | GanonK | bgn health 0, hensin (mC748 6) | transform demo, m02B5 0 to 1 | (none, as vanilla) | 27.6 s |
| Puppet Ganon form 2 | GanonK | bgn2 health 0, its lethal action | transform demo, m02B5 1 to 2 | | 12.4 s |
| Puppet Ganon form 3 | GanonK | bgn3 lethal block, m0FD8A 3 last | defeat demo (mCSMode 20..22), setNextStage `GanonK` 4 layer 9 | 0x3F10 | 19.0 s |
| Ganondorf | GTower | sub already 1 (park skipped), action 0x1E (finish), sub 0 once | finish demo 0x64/0x66 to frame 0x7C, reload `GTower` point 2 layer 9 (endhr): "Ugghn... The wind... It is blowing..." | 0x3F40 | 7.2 s |

dolphin.log: no ISI, DSI, invalid read/write or JUTException over the whole run (only vanilla OSReport heap-estimate lines).
Screenshots: `wind58/gohma-warp-ganona.png`, `wind58/pg-maze-chest.png`, `wind58/ganondorf-ending.png`.

## Fixture notes (not done by the tool)

- Molgera waits (m18B0 -10) until Link drops below y 900 (`d_a_bwd.cpp:430-437`); `stage-hop Xboss3 0 0` lands him on the
  ledge at y 1500, so one real stick push (`input 0 0 127 2500`) dropped him in. `boss-end` refuses until then.
- The third Phantom Ganon only appears once Link holds the Light Arrow (`standby`, `d_a_fganon.cpp:1064-1068`). 28-Hyrule3
  has none, so the Light Arrow get bit (0x803C4C65 |= 0x04) and the bow slot (0x36) were poked before the hop, as wind#44 did.
  `pg-maze` refuses when the arrow is held (that Phantom Ganon is parked at y -20000 then).
- The PG3 fight starts when Link is within m2BD*10 = 1000 units of its home: two stick pushes from the spawn.
- Puppet Ganon's and Ganondorf's intros: A presses (`input 0x100 0 0 100`) until no event runs; `boss-end` refuses while one does.

## Changed after the live run

- `pg3`'s watch: done when table 8 switch 0 is set. last_end sets it with the actor still queued (mMode 5) and the actor
  deletes itself later; the first run timed out (exit 3) waiting for both.

## Not run

- Two players: the GT D3 run itself (hub, four trials, both Phantom Ganons, Puppet Ganon, Ganondorf by two players, with
  the co-op relay carrying the bits) still needs a pair; these recipes make every win real on each side.
- `--variant natural` / `--variant parry` (one real hit after a health poke): not run.
- The Mac (Dolphin 2603, 48 MB override): offsets are the same disc's, but not run there.
