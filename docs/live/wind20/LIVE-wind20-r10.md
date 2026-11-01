# wind#20 block R10: escape hatches, what LIVE-6 left, macOS (2026-11-01)

**Build and rig.** As in `LIVE-wind20-r5.md`: main c1278b3, REL 0x1CE30685. Pair A = 75194 (host), B = 75204 (client), both in `M_NewD2` room 0. CLI host/join on 127.0.0.1:25640.

LIVE-6 b1 passed the first five hatches on the PC: enemy sync off, remote bodies off, boats off, relay scope off, and one A→B bomb with projectiles unset (the old default). It left three things:
- the B→A bomb;
- `--coop-live-switches=off`;
- `--coop-boss-kill=` without gohdan.
The last two need a bound co-op session, so they run in the R6 block.

**Poke (noted as the rules ask).** B's file had no bombs on a button. Before the throws, B got them on X by two pokes, the ones WW-MAC-RULES names for wind#11 (d):
- `poke-u32 803C4C10 000D0103`: `mSelectItem` X = slot 0x0D;
- `poke-u32 803CA7D8 00000031`: the live X item = 0x31.

The HUD then showed the bomb bag with 98 (`evidence/r10-B-bombs-on-X.png`). The throws themselves are real input: X to draw, X to throw, via `wwdev input 0x0400`.

| Item | Result | Evidence |
|---|---|---|
| **Control: B → A bomb, projectiles on (the default since v0.6.1)** | **copy made** | **Setup.** The host and join from the override run, no `WW_PROJECTILES`.<br>**Log lines.** Both logged `Projectile events on (WW_PROJECTILES=off turns them off) ...`. After B's throw, the host logged `projectiles: 1 spawned here, dropped map[]` and B logged `0 spawned here, dropped map[self:1]` (its own echo).<br>**Picture.** A's frame shows B's lit bomb over JoinB (`evidence/r10-ctrl-A.png`). B's count 98 → 97.<br>Lines: `evidence/r10-ctrl-projectile-lines.txt`. |
| **`WW_PROJECTILES=off` on both: B → A bomb** | **PASS** | **Setup.** Host and join restarted with `WW_PROJECTILES=off`.<br>**Log lines.** Neither side printed the `Projectile events on` banner or any `projectiles:` counter line (`evidence/r10p-host.log`, `r10p-join.log`).<br>**Throw.** B threw the same way: count 97 → 96, and the bomb is on B's screen next to HostA (`evidence/r10-off-B.png`).<br>**On A.** A's frame shows no bomb by JoinB (`evidence/r10-off-A.png`), and A's projectile block at 0x80413D50 reads 0. |

**Verdict.**
- This closes R10's owed B → A bomb.
- R10 stays unticked until the two co-op hatches pass in the R6 block (`LIVE-wind20-r6.md`).
