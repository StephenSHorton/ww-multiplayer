# Co-op design (v1.0)

**Status:** the v1.0 co-op design, 2026-10-14. Until the owner answers the open questions (§10), the design uses the recommended defaults: per-flag merge, separate rupees, a partner's switches in your current room deferred to your next room change, and the arena for M7's code room. The relationship with ww-online (§10, question 5) is awaiting the owner.

**Decision record:** issue #23. On 2026-10-14 the owner decided that v1.0 is a real co-op mod, in three parts:
1. save-file merging,
2. combat sync (enemies die for both players),
3. puzzle and dungeon state (doors, switches and so on propagate).

v0.3.0 (Go-only hardening) has already shipped, as tag v0.3.0 on d6850b0. **Current status (2026-10-24, main f9e1fe6):** v0.4.0 shipped M0-M6.0, in the CLI and in the desktop app's co-op worlds. v0.5.0 (2026-10-22) added shared enemies and the remote players' bodies, on by default, and the Gohma kill relay. Main since v0.5.0 (protocol 5, unreleased) adds the Kalle Demos, Molgera, Jalhalla and Helmaroc King relays (`--coop-boss-kill`, default `gohma,kalle,molgera,jalhalla,helmaroc`; Gohdan's row opt-in), the stale-actor refresh (Tiers 1-2 on with combat, Tier 3 behind `--coop-refresh`), mirrored Keese, and the per-dungeon co-op table changes; the app sets the relay options per player (#176). M6.1, M7 and M8 remain. This document supersedes the "Post-1.0: co-op" line in the finish plan.

**Conventions**
- `tww/` means the zeldaret/tww decomp at commit 4d78c994. References use `file:line`.
- `sym:N` means line N of `tww/config/GZLE01/symbols.txt`.
- All addresses are absolute GZLE01 (NTSC-U) addresses.
- **(inferred)** marks a claim read from code but never observed on a live Dolphin. None of this design has run on Dolphin yet. Every milestone's acceptance test includes the live check.

**Where this design comes from**
- The base is the "Go-first" design: co-op works through Go polling and writing memory, with no blob change, until a late C milestone.
- From the "host-authoritative world" design it takes: the file-binding tag, the C shim at `getSave`, the host-side world file, dry-run as the default for the first release, and a C-side fail-closed gate.
- From the "event-driven hooks" design it takes: the M0 diagnostics, the `isActor` shim, applying story bits on stage transitions, check bytes on ring entries, automatic disable, and memory-card backups.
- Appendix C lists every error the design reviews found and how this document fixes it.

---

## 1. Summary

Each player keeps their own Wind Waker save file. A file joins co-op only when the player *binds* it to a shared co-op world, and the tool backs up the memory card first. Your solo saves are never touched.

When either player opens a chest, hits a permanent switch, picks up a dungeon map, key, item, song, pearl or heart piece, or moves the story forward, the partner gets the same progress. Usually it arrives the next time the partner changes room or stage, so the partner's camera is not taken over mid-fight.

Small keys and heart containers are counted from what the party actually collected and opened. A key both players grab is never doubled, and a key one player spends is gone for both. If you are in the same dungeon as your partner, a latching crystal switch or a cleared room opens for both of you. An enemy your partner kills shrinks away on your screen and stays dead while either of you is in that stage, for the enemies the stage files key (M6.0); the others stay each player's own until M6.1.

Some things stay your own: hearts and magic you are carrying, rupees, ammo, bottles, time of day, and where you are. You roam freely and see your partner as the mini-Link.

v0.4.0 ships co-op M0-M6.0 from the Go process (the re-patch v0.4.0 needs is for the Phase E blob, not co-op C code). The last step before v1.0 adds a small injected routine that applies the shared world at the moment a stage loads, on the game's own thread. That removes the remaining write races and surprise cutscenes.

## 2. Goals and non-goals

**Goals (v1.0)**
1. **Shared persistent progress.** This covers:
   - per-stage chests, item pickups, visited rooms and dungeon items (map, compass, boss key, boss defeated, heart container taken);
   - latching memory switches;
   - salvage and ocean bits, charts and the sea map;
   - story event bits, under an allowlist;
   - unique items and their upgrades, songs, pearls, Triforce shards and capacities;
   - derived small keys, max hearts and max magic.
2. **Save merging.** Two bound files converge to the union of their shared classes. Merging happens on bind, on every reconnect and live during play. Offline progress a bound file made solo is merged on its next session.
3. **Puzzle and dungeon state.** Dungeon (dan) switches and room (zone) switches propagate between players in the same stage or room, for latching switches only.
4. **Combat.** Enemies placed by the stage die for both players, and stay dead while anyone is in that stage. That is vanilla-like respawn behaviour.
5. **Solo saves are never contaminated:** explicit binding, a validated tag, card backups, and dry-run first.
6. **The pose and mini-Link pipeline is unchanged.** Co-op can be turned off with one switch at runtime.
7. **Every milestone ships on its own.** Each is unit-tested on Linux CI and accepted in the two-Dolphin harness (`dolphin2` plus `mp-local`) with `screenshot`, `input` and `peek`.

**Non-goals (v1.0)**
- Host-simulated enemy AI, syncing enemy health, or the remote Link as a hitbox or target.
- Syncing a boss fight in progress, or minigame state.
- Shared current life, magic, rupees, ammo, bottles, bag contents, time of day or position.
- Live refresh of actors that only read their state when they are created (chests, freestanding items, bombable walls) in the receiver's current room. That is post-1.0 (M9).
- More than 2 players. The protocol supports N, but only 2 are tested.
- Second-quest files, and randomizer or Archipelago ISOs (game ID GZLE99).
- Writing memory-card files directly. Only the game's own save menu writes the card.

## 3. State classes

**Layout anchors**
- `g_dComIfG_gameInfo` is at 0x803C4C08 (`sym:17472`).
- `save` is a `dSv_info_c`, 0x12A0 bytes, at +0 (`tww/include/d/d_save.h:991-1002`).
- `play` is at 0x803C5EA8.
- `dSv_save_c` (`mSavedata`, 0x778 bytes, `d_save.h:908-914`) is the canonical image used on the wire and in the class table.
- *t* is a save-table slot 0..15 (`d_save.h:888-906`): 0 sea, 3 DRC, 4 FW, 5 TotG, 6 ET, 7 WT, 0xB interiors.

**Sync rules used in the table**
- **live**: applied as soon as the receiver is idle.
- **on load**: applied at the receiver's next room or stage change: on the first STABLE and IDLE tick after it (item 129), or, where the row says so, on the stage transition (§6.5). Before M7 Go does both. From M7 the getSave shim does it at stage loads; Go still does the room-change case (current-table memory switches, §6.3), because room changes do not call `getSave`. A save table other than the loaded one (a cold table) is not written on load: Go writes its saved slot as soon as STABLE, IDLE or not (item 128).
- **never**: not synced.

| Class | Examples | Where it lives (address / struct) | Sync rule | Merge rule | Needs C? |
|---|---|---|---|---|---|
| Stage progress bits | chest opened `mTbox`, pickup `mItem`, `mVisitedRoom`, `mDungeonItem` bits 0-5 (map, compass, boss key, boss dead, heart taken, boss demo; `d_save.h:637-690`) | `dSv_memBit_c`: saved 0x803C4F88+0x24·t (+0x00 / +0x14 / +0x18 / +0x21); live copy of the current table 0x803C5380 (`d_save.h:992`) | live for the current table: chests and pickups are read only when their actors are created (`d_a_tbox.cpp:447-455`, `d_a_item.cpp:227-237`), so a write fires no event. Dungeon items 3 and 5 (boss dead, boss demo) are the exception: a boss stage reads them every frame (`dMeter_recollect_boss_data`, `d_meter.cpp:1069-1085`; the boss gauge, `:3682-3687`; Gohma's floor, `d_a_dr2.cpp:560-575`; Helmaroc King, `d_a_bdk.cpp:2741-2744`), so in a boss stage they wait until Link leaves it (item 85). Item 4 (heart taken) waits there too, because the receiver's own boss drops its own container (item 85). For other tables: to the saved slot as soon as STABLE, IDLE or not (§6.3, item 128) | grow-only OR | No. M7 moves it to C. |
| Memory switches 0x00-0x7F | door unlocked, gate raised, room-clear switch, crystal (un-timed) | memBit `mSwitch[4]` +0x04 (live 0x803C5384); bit n is `mSwitch[n>>5] \|= 1<<(n&31)` (`d_save.cpp:1084-1087`) | on load by default (receiver's next room change, after the room hold, for the current table; for cold tables, to the saved slot as soon as STABLE, IDLE or not, item 128); live is opt-in (`--coop-live-switches`) | set-only OR, applied once per bit ("ever-had" set), **latching setters only** (M1 table), clears never sent | No. M7 makes stage-load application exact; the room-change write stays in Go (§6.3). |
| Small keys | `mKeyNum` per dungeon | memBit +0x20 (live 0x803C53A0); HUD pending counter `mItemKeyNumCount` 0x803CA77C | derived, applied when idle and settled | `keys(t) = \|key sources set\| − \|key doors set\|` over the receiver's *applied* flags (§6.4) | No |
| Ocean and charts | salvage bits, charts owned / deciphered / salvaged, `mFmapBits`, Triforce charts | `mOcean` 0x803C51C8 (0x64); `dSv_player_map_c` 0x803C4CCC (0x84) | on load | OR | No |
| Story event bits | bytes 0x00-0x41 (487 named bits, `d_save_event_flag.inc`) | `mEvent` 0x803C522C; bit id 0xHHLL = byte HH, mask LL (`d_save.cpp:1187-1199`) | on load (Go writes them during the receiver's stage transition; M7 shim writes them at getSave) | OR under the generated SyncMask, minus LocalOnly, D8 and refight bits; receiver-side holds (§6.5) | No. M7 makes it exact. |
| Event registers | bytes 0x79-0xFF (131 multi-bit registers, `setEventReg` is mask-and-set, `d_save.cpp:1202-1210`) | 0x803C52A5..0x803C532B | on load: from M4, on the stage transition with the story bits (Appendix C item 89) | per player, except an allowlist: the 17 figurine masks from `l_figure_comp` (`d_a_obj_figure.cpp:43-61`): 0x80FF-0x84FF, 0x8CFF-0x95FF, 0x9CFF, 0xB1FF use OR; the warp-pot registers 0x9F07, 0xA007, 0xA107, 0xA207, 0xA307, 0xA407 (`daObj_Warpt_c::m_event_reg`, `d_a_obj_warpt.cpp:37-44`) OR their 0x07 bits; Outset's pig pen 0xBF0F ORs its 0x0F bits; letter, Beedle-points and Ghost Ship registers, Orca's level 0xD007 and Koboli's level 0xC203 use MAX within their mask (monotone: inferred; item 169). The Sploosh Kaboom and barrel-shooting prize counts 0xFE07 and 0xB703 and Mrs. Marie's Joy Pendant count 0xC0FF stay per player (item 169) | No |
| Unnamed event bytes 0x42-0x78 | Archipelago uses 0x60-0x61 | 0x803C526E..0x803C52A4 | never | — | — |
| Inventory and collection | `mGetItem` flags, `mCollect[0..4]` (swords, shields, bracelets, charms), songs `mTact`, `mTriforce`, `mSymbol`, beast and bait flags | 0x803C4C59 (0x15), 0x803C4CBC-0x803C4CC7, 0x803C4C9C | live when idle | OR flags, then derive slots `mItems` 0x803C4C44 (best variant, fill empty or lesser slots only) and equipment; grants ordered base then upgrade; context rules for the FF1 sword and the GTower bow (§6.6). `mGetItem[0..2]` and `mCollect[4]` sync bit 0 only: the telescope, sail and Wind Waker, and the Hero's Charm owned (`item_func_heros_omamori`, `d_item.cpp:895-898`). Bits 1-7 of `mGetItem[0..2]` record which bottle contents the player has held (`onBottleItem`, `d_save.cpp:224-282`), and `mCollect[4]` bit 1 is the charm worn (`d_menu_collect.cpp:1283-1286`): both stay each player's (item 112) | No (Go mirrors the `item_func_*` side effects). M7 adds `execItemGet` as an option. |
| Capacities | wallet size, arrow max, bomb max | 0x803C4C1A, 0x803C4C77, 0x803C4C78 | live when idle | MAX; never lowered | No |
| Max magic | magic meter and its upgrade | `mMaxMagic` u8 0x803C4C1B; pending `mItemMaxMagicCount` 0x803CA784 | derived, live when idle | from source flags (Deku Leaf, event 0x3180); **add-only** | No |
| Max life | heart pieces and containers | `mMaxLife` u16 0x803C4C08 (quarter hearts); pending `mItemMaxLifeCount` 0x803CA77E | derived, live when idle | 12 + pieces + 4·containers from source flags; **add-only** | No |
| Dan switches 0x80-0xBF | dungeon-temporary puzzle state | `mDan.mSwitch[2]` 0x803C53A8 | live when IDLE, only while both players are in the same stage name and table (the receiver checks the partner's PRESENCE, item 122) and every shown room's actors have settled (item 121) | set-only, latching setters only, kept per (stage, table) while someone is there (item 114); never set again in a save table where the receiver's game had it (item 125) | No |
| Zone switches 0xC0-0xDF | room puzzle state | `mZone[z].mZoneBit.mSwitch[0..1]` 0x803C53B0+0x4C·z+2; z from `mStatus[room].mZoneNo` 0x803BDC88+0x114·room+0x107 | live when IDLE, only while both players are in the same stage and room, the receiver's zone exists and the room's actors have settled (items 121, 122) | set-only, latching only, kept per (stage, table, room) while that room is occupied | No |
| Room one-shot switches 0xE0-0xEF | drop-down ladders, room-visit state | `mZone[z].mSwitch[2]`, zeroed on every room change (`clearRoomSwitch`, `d_save.cpp:1273-1275`) | live edge only, both players in the same room at that moment | never stored; never re-applied after a room change | No |
| Enemy kills | stage-placed enemies by `setID` (keyed ones; unkeyed from M6.1) | zone actor bits 0x803C53BC+0x4C·z (512 bits); actor `setID` +0x1BC, `home.roomNo` +0x1E2 | live when IDLE, scoped to (stage, table, room), into any loaded room's zone | union per (stage, table, room) of the keys the M1 enemy table syncs, dropped when no player is in the stage | No (K1 light-shrink, §7.2). M7 adds the `isActor` shim and KILL. |
| Per player | `mLife` 0x803C4C0A, `mMagic`, rupees 0x803C4C0C, ammo and picture counts, bottles, bag contents, delivery bag and trade flags, `mSelectItem`, status_b (time, day, wind), `mReturnPlace` 0x803C4C38, `mInfo` (name, deaths, `mClearCount` 0x803C4DA8), config, `mPriest`, `mStatusC` | `dSv_player_c` | never | — | — |
| Temporary | `mTmp` 0x803C5D60, `mRestart` 0x803C5D30, `mTurnRestart` 0x803C5E60, `mDan.mStageNo` | `dSv_info_c` | never | — | — |
| Co-op tag | binding tag | `dSv_reserve_c` 0x803C532C (0x50 bytes, saved to card, unused by vanilla code) | owned by Go, per file | — | No |

A golden test assigns every byte of the 0x778-byte `dSv_save_c` to exactly one class: OR, OR_MASKED, MAX, DERIVED, PER_PLAYER, NEVER or TAG. It fails on any gap or overlap. The same table drives:
- the sender's diff,
- the server merge and validation,
- the receiver's WriteGuard,
- the offline `save-diff` / `save-merge` tool.

### 3.1 Write-target rules

**Live and saved copies.**
- On stage load, `dStage_stagInfoInit` (0x8004230C, `d_stage.cpp:1668-1677`) copies `mMemory[t]` into the live copy at 0x803C5380 (`getSave`), then runs `initDan`.
- `dStage_Delete` (0x80043464, `d_stage.cpp:2255-2256`) and the save menu (`d_menu_save.cpp:935`) copy live back to saved (`putSave`).

**Rule.** If `mDan.mStageNo` (0x803C53A4) == t, write the **live copy**. Also write the saved slot, the same way `dComIfGs_onStageTbox` does (`d_com_inf_game.cpp:694-702`). Otherwise write only the saved slot. Re-apply after every stage change, because a write lost to `putSave` heals on the next tick.

**Zone writes.**
- Write only if `0 <= mStatus[room].mZoneNo < 32` and `mZone[z].mRoomNo == room`.
- Key zone state by (stage name, room), never by zone slot. Slots are allocated first-free on each machine (`d_save.cpp:1525-1537`).

**Assert hazard.** `JUT_ASSERT` is live in retail and calls `OSPanic` (`JUTAssert.h:9`, `m_Do_printf.cpp:208-230`). The game halts on any `dSv_info_c` call with switch number 0xF0-0xFE, room outside 0..63, an unmapped zone, or actor id ≥ 512 (`d_save.cpp:1540-1701`). Go validates every such argument before any write or RPC.

### 3.2 Gates (evaluated from one snapshot per tick)

**STABLE** (needed for any write). All of these hold:
- `mpPlayerPtr[0]` (0x803CA754) is non-zero.
- The stage name (0x803C9D3C) is not "", "sea_T" or "Name".
- `mDan.mStageNo` is not 0xFF.
- `mNextStage.mEnable` (0x803C9D54) == 0, except for on-transition writes (§6.5).
- Stage and room have not changed for 4 ticks.

**IDLE** (needed for every write but two classes: the stage progress bits and memory switches of cold tables, which need STABLE only, so they are written while the pause menu is open too (§6.3, item 128); and the story bits and registers, which are written on the transition tick (§6.5). So IDLE is needed for the loaded table's stage progress bits and memory switches, the ocean, charts and sea map (item 129), the dan, zone and room switches, inventory and capacities, the derived bytes and pending counters, the binding tag, kills and the escapes). All of these hold:
- `mEvtCtrl.mMode` (0x803C9EA2) == 0 and `mOrderCount` (0x803C9EA0) == 0 (`d_event.h:152-154`; offsets inferred, no STATIC_ASSERT).
- `dMenu_pause` (0x803F7097) == 0.
- `mMiniGameType` (0x803CA8E2) == 0.
- `mPlayerInfoBufferStageNo` (0x803CA8C8) == 0, i.e. no Ganon's Tower boss-refight snapshot is active (`d_com_inf_game.cpp:1446-1457,1580-1581`).
- The pending counter about to be written is 0.

**HEARTBEAT.** The mailbox `spawn_trigger` (0x80412F00) advances. If it stalls (emulation paused, vanilla ISO), all writes stop and the TUI says why.

**ARMED.** The loaded file is bound to this world and passes tag rules V1-V6 (§5.2).

---

## 4. Architecture

### 4.1 Overview

```
host / join / mp-local --coop
 ├─ broadcast-pose, puppet-sync          (unchanged)
 └─ coop-sync (new goroutine; own Dolphin handle by PID; own TCP connection, Hello role=coop)
      Snapshotter(10 Hz) -> Gates -> Tag/Bind classifier -> Differ -> 'O' GAINS / SCOPED out
      'O' WORLD / GAINS / SCOPED in -> Planner (class rules, ever-had, holds, derived keys/hearts/slots)
          -> WriteGuard -> FrameWriter (byte writes after heartbeat) -> verify next tick
      CombatApplier: actor walk + K1 light-shrink
server (host process): network.Server + CoopHub (single goroutine)
      world image + per-class merge + validation + seq, world file (journal, backups, tombstones),
      presence, scoped stores (dan / zone / kill), relay only effective changes
wwapi/stage             : disc readers (Yaz0, RARC, DZS/DZR)                                    [M1]
wwapi/tables            : user's ISO -> keys, hearts, switch classes, push blocks, enemy ACTR;
                          cached by DOL hash, also built by `patch` (post-patch step)         [M1]
mods/coop/stagetables + synctables : which tables a session runs with, and the sync verdicts  [M1]
wwapi/savefile          : GCI / card parse, checksums, packed<->RAM map                         [M4]
mods/coop/savemerge : offline diff/merge + derive over two save images                      [M4]
C side: no co-op C until M7 (coop pump: getSave shim, isActor shim, coop_frame + op ring)
```

### 4.2 Go side

**Packages**, all testable on Linux:
- `wwapi/mem` (was `internal/coop/mem`): a `Mem` interface (a live `*emu.Dolphin` implements it, bounds-checked, over the unexported raw reads in `wwapi/emu/memory.go`), a 24 MB `FakeMem`; and `wwapi/fakegame`'s `FakeGame`, which models:
  - frames and the heartbeat;
  - `getSave`/`putSave` on table change;
  - `initDan`, `clearRoomSwitch`, zone create and remove;
  - random game `onSwitch`/`onEventBit`/`offSwitch` writers;
  - the HUD's pending-counter semantics (`d_meter.cpp:1420-1452`, `5242-5257`);
  - small-key doors that read their switch only at a room change of Link's (`fakegame.KeyDoor`, item 139);
  - `dComIfGs_init` leaving `mReserve` untouched;
  - `card_to_memory` and `ExDataSave`.
- `wwapi/addr` (was `internal/coop/addr`): every constant from Appendix A with its decomp citation, plus arithmetic tests (for example play = 0x803C4C08 + 0x12A0, and `mCurStage` = play + 0x3E94). The mod's own RAM layout (T2, the mailbox and the regions around them) is in `wwapi/mailbox`.
- `mods/coop/class`: the byte-class table, SyncMask, LocalOnly and hold lists, the register allowlist, and `policyVer`.
- `wwapi/state` (was `internal/coop/snap`): the snapshot reader. Per tick it reads:
  - 0x803C4C08 + 0x12A0;
  - 0x803C9D3C..0x803C9EB8;
  - 0x803CA754..0x803CA8E4;
  - `mStayNo`;
  - 64 `mZoneNo` bytes;
  - `dMenu_pause`;
  - the heartbeat;
  - the GBA link: 0x129 bytes of `g_mDoGaC_gbaCom` (0x803B904C), in one read, for the Tingle Tuner guard (§6.2, item 138).

  That is about 5 KB per tick. Sensitive fields are read twice and a bit is kept only if both reads agree, which rejects torn reads during `card_to_memory`. Which image bytes are sensitive is `state.Options.Sensitive`; its default, `state.DefaultSensitive`, is the merging bytes of the `class` table, which a `class` test pins byte for byte (state does not import `class`).
- `wwapi/gate` (was `internal/coop/gate`): the STABLE, IDLE and HEARTBEAT gates (§3.2), evaluated from one snapshot per tick.
- `mods/coop/replica`, the client:
  - **Differ.** Builds the local image: saved `dSv_save_c` with the current table's live copy folded in. It sends rising edges only. It never sends clears, so the bulk resets on transitions (`getSave`, `initDan`, `clearRoomSwitch`, `removeZone`) cannot propagate. On a table, stage or room change it re-baselines without emitting anything.
  - **Planner and reconciler.** Level-triggered: `missing = desired &^ local &^ suppressed`, where suppressed = ever-had history plus holds. It runs every tick and after every stage change.
  - **FrameWriter.** Spin-polls `spawn_trigger` with a 100 ms timeout. When it advances, it immediately does a fresh 1-byte read, OR, and 1-byte write for each planned byte, then verifies on the next snapshot. See §4.2.1 for the window.
  - **WriteGuard.** Every production write must fall inside an allowlist computed from the class table:
    - syncable image bytes,
    - the live memBit,
    - validated zone and dan words,
    - the three pending counters,
    - `play.mSelectItem` (0x803CA7DB),
    - the tag,
    - validated K1 timer bytes and the CARRY byte of the weapon a light-shrunk enemy holds (M6, §7.2).

    Anything else is refused and logged. The CI write audit uses the same guard.
  - **Auto-disable.** Co-op turns itself off, and the TUI says why, on any of:
    - more than 8 verify failures or WriteGuard refusals per minute;
    - a table-hash or layout mismatch;
    - a nonce storm from save-state loads.
- `wwapi/stage` (M1 disc readers; was `internal/coop/stagedata`), `wwapi/tables` (the M1 tables, cached by DOL hash and built by `patch`), `mods/coop/stagetables` and `mods/coop/synctables` (the session's tables and sync verdicts), and `wwapi/savefile` plus `mods/coop/savemerge` (M4; were `internal/coop/savefile`): see the milestones.
- `mods/coop/hub`, server side: the CoopHub (§4.4).

**Integration**
- Started from `runMultiplayerGoroutines` (`mods/multiplayer/engine/session.go`) for `host` and `join`; from wwdev `mp-local`'s `spawn` helper (`cmd/wwdev/mp_local.go`, `coopsession.RunSync`, one coop-sync per Dolphin PID); and from the app's engine when the lobby's Co-op is New world or Continue (restarted after errors, Appendix C item 165). All three go through `mods/coop/session`.
- Co-op is off by default until v1.0.
- `coop off` stops all writes within one tick.

#### 4.2.1 Write race and the frame-phase window (corrected)

The game sets bits with a word load, OR and store (`d_save.cpp:1084-1087`). A Go byte read-modify-write can erase a bit the game sets in the same byte between Go's read and write. Nothing could detect that afterwards.

`frame_shim` replaces the `bl` at 0x80023204 in `fapGm_Execute` (`f_ap_game.cpp:77-81`). It runs `multiplayer_update` and then tail-calls `cCt_Counter` (0x802449AC, `sym:8240`; `frame_shim` in `inject/src/core/hooks.c`), so `cCt_Counter` still runs. `multiplayer_update` (`src/core/hooks.c`) starts with `spawn_trigger++`.

In GZLE01, `fpcM_Management` runs these steps in order (`f_pc_manager.cpp:276-292`):
1. `cAPIGph_Painter` (the VSync wait),
2. `fpcDt_Handler` (deletes),
3. `fpcPi_Handler`,
4. **`fpcCt_Handler`, where actor `create()` runs and can call `onSwitch` or `onEventBit`**,
5. execute,
6. draw.

So a write made right after the heartbeat lands in the rest of the main loop or in the next Painter. That is before the next frame's creates and executes, but it is **not** a window where nothing writes save bits. FrameWriter shrinks the hazard to a game store into the same byte within a few microseconds. It does not remove it. The `frame-phase-probe` diagnostic measures the window on the owner's machine. Only M7's game-thread application removes the race.

### 4.3 C side

**M0-M6: no co-op C code.** The v0.4.0 blob (Phase E0-E3) still needs a re-patch and a fresh save state: the strict contract check refuses older blobs and save states taken on them (`wwapi.RepatchAdvice`).

**M7: the co-op pump.** It is the only C milestone: one blob regen, one Shift+F1 recapture and one user re-patch. It contains:

1. **`coop_getsave_shim(self, stageNo)`**
   - Installed with Freighter `hook_branchlink`, the technique all four existing hooks use (`inject/build.py`: `main01_init`, `frame_shim`, `daPy_draw_hook`, `pad_read_shim`), over the single `bl getSave__10dSv_info_cFi` (0x8005D8C8) inside `dStage_stagInfoInit` (0x8004230C-0x8004238C).
   - `d_stage.cpp:1675` is the only `getSave` call in the game. The exact `bl` address comes from `WW_DOL_PATH=inject/original.dol ./wwdev.exe ppc-disasm 0x8004230C 32`.
   - If `image_ok()` holds, the shim ORs the Go-maintained world image into `mSavedata` and then calls the real `getSave`. The image covers:
     - 16 stage tables, with the key byte +0x20 forced to 0 and switches pre-filtered to latching ones;
     - ocean;
     - map bit fields;
     - masked event bits and allowlisted registers.
   - STAG is decoded before ACTR, before the layer tables (`d_stage.cpp:2060`, 2150) and before the room `getLayerNo` (`d_s_room.cpp:210`). So actors spawn already consistent: no camera event, no `putSave` clobber, no Go race.
2. **`coop_isactor_shim(self, id, room)`**
   - `hook_branchlink` over the `bl isActor__10dSv_info_cFii` (0x8005E5F0) inside `dStage_actorInit` (0x800423C0-0x800424BC). That `bl` is the only call of `isActor` (`d_stage.cpp:1693`), so this is equivalent to overriding `isActor`'s return value, but needs no entry trampoline.
   - It returns true if the real `isActor` returns true, or if (stage, room, setID) is in a Go-written dead table. A partner-killed enemy is then never spawned.
3. **`coop_frame()`**
   - Called from the existing `multiplayer_update`, so no new hook is needed. When co-op is disabled it costs one load and one compare.
   - When IDLE, it ORs `image.stage[cur] & live_mask` into the live copy and the saved slot. `live_mask` covers tbox, item, visited and dungeonItem only, which fire no events.
   - It drains at most 2 ops per frame from a 16-entry ring.
4. **The op ring (whitelisted)**
   - Go validates every argument against live `mStatus` before enqueueing. C checks the whitelist again.
   - Ops:
     - `OR8(addr in syncable ranges, mask)`: race-free OR.
     - `GRANT(itemNo)`: `execItemGet` (0x800C2DFC, `d_item.cpp:534-536`), only if `dComIfGs_checkGetItem` (0x80053B30) reports the item is not owned.
     - `ADD_S16(addr in {0x803CA77C, 0x803CA77E, 0x803CA784})`.
     - `SET_U8(live or saved keyNum)`.
     - `KILL(procID, setID, room)`: `fopAcM_delete(fpc_ProcID)` (0x800244B8) plus `dSv_info_c::onActor` (0x8005E4BC), for enemies without `enemyice`.
     - `SET_NEXT_STAGE`: `dComIfGp_setNextStage` (0x800537C8), for refresh and warp.
     - `ON_SWITCH(no, room)`: `dSv_info_c::onSwitch` (0x8005DB24).
5. **`image_ok()`** is true only if every one of these holds:
   - the co-op region magic and `layoutVer` match;
   - `armed == 1`;
   - Go's heartbeat moved within the last 300 frames (dead-man switch);
   - the stage name is not "sea_T" or "Name";
   - `mDataNum` (0x803C5E98) == `image.slot`;
   - the loaded file's tag has magic 'WWCO' and worldID‖bindNonce equal to `image.fp`;
   - `mDateIPL` (0x803C4C20) != 0, or `image.freshBind` is set (§5.2 rule V5);
   - the refight byte 0x803CA8C8 == 0.

   The tag in the loaded save file is therefore the capability: C will not apply a world to an untagged or foreign file even if Go is wrong.

**Torn-image safety.** For the same world, a new image is always a superset of the previous one, so a torn read applies only valid bits. Replacing the image works in three steps:
1. Set `armed = 0` and wait at least 100 ms.
2. Write the new image and fingerprint.
3. Set `armed = 1`.

Ring entries carry `seq` plus an XOR check byte.

**Size.** A clang-18 `powerpc-unknown-eabi` sketch of items 1, 3 and 5 with GRANT and ADD_S16 measured **0x3F0 bytes of .text at -Os and 0x50C at -O2** (`scratchpad/coop/hostauth-sketch/coop_world.c`). With the isActor shim and the full op set the estimate is about 0x700 (inferred). devkitPPC gcc will differ. This does not fit today's 0xD8-byte gap, so §4.5 applies.

**Not in v1.0.** Entry hooks on the `dSv_*` setters, which need displaced-instruction trampolines. If they are ever needed, fill the displaced first instruction at patch time from the user's vanilla DOL, using marker words, PC-relative and r12/ctr checks, and a CRC. No game bytes would then ship in `blob.go`.

### 4.4 Network protocol v2

**Version bump.** `ProtocolVersion` became 2 here (`mods/multiplayer/net/handshake.go`; 1 was v0.3.x's handshake); enemy sync later raised it to 3. Peers with mismatched versions are already rejected with a clear message.

**Free type letters.** J W P L D C M K Q H R are taken (`protocol.go`, the `Msg*` constants). Co-op uses one new type, **'O' (0x4F)**. The frame format `[type:1][len:2 BE][payload]` with `MaxPayload` 0xFFFF is unchanged.

**Connection role.** The co-op pipeline opens its own connection: `MsgJoin(name)`, then `MsgHello` with payload `[version:u16 BE][role:u8][caps:u16 BE]`.
- `ParseHello` ignores trailing bytes (`ParseHello` in `handshake.go`), so the extension is compatible.
- Roles: 0 = player (the default when absent), 1 = coop.
- Role-1 connections live in the CoopHub, not in `s.players`. They get no pose, chat or player-list relay, do not appear in the roster, and keep the heartbeat.
- The server routes 'O' to the CoopHub (`case MsgCoop` in `server.go`). An 'O' frame on a role-0 connection is dropped.

**'O' payload: `[sub:u8][body]`.** All integers are big-endian. **No raw addresses travel.** Offsets index the 0x778-byte image and are checked against the class table; receivers derive every address locally.

| Sub | Dir | Body | Server action |
|---|---|---|---|
| 0x01 CO_HELLO | c→s | `[schema:u16][classHash:u64][stageTblHash:u64][worldID:8][bindNonce:8][slot:u8][clearCount:u8][nameHash:u32][fileState:u8 (0 unbound, 1 bound, 2 leaked, 3 foreign)][want:u32]` = 45 B | Classify. A `classHash` that differs from the host's (a modded build) is refused: features = 0, plus a NOTICE. A `stageTblHash` that differs from the host's (another disc, or rando data), or is 0 on either side (no stage tables), keeps the other features but loses `hub.TableFeatures` (memory switches, live switches, puzzle, combat), plus a NOTICE saying why (item 69) |
| 0x02 CO_WELCOME | s→c | `[status:u8][reason:u8][features:u32][worldID:8][seq:u64][freshBind:u8]` = 23 B | — |
| 0x03 WORLD | both | `[worldID:8][seq:u64][len:u16=0x778][image]` (PER_PLAYER, NEVER and TAG bytes zeroed; current-table live copy folded into its saved slot) ≈ 1.9 KB | c→s at join, reconnect and resync: merge per class, reply with the merged image, relay the effective delta as GAINS |
| 0x04 GAINS | c→s | `[batch:u32][n:u16] n×{off:u16, val:u8}` | Validate each offset's class; merge (OR within the class mask, or MAX); drop tombstoned bits |
| 0x04 GAINS | s→c | `[seq:u64][origin:u8][n:u16] n×{off:u16, val:u8}` | Only bytes that changed the world; n ≤ 21841 per frame ((0xFFFF − 12) / 3), and in practice n ≤ 0x778 |
| 0x05 PRESENCE | c→s, relayed | `[stage:8][table:s8][room:s8][layer:s8][point:s16][flags:u8 inGame, inEvent, transitioning, paused, armed]`, sent on change and at 1 Hz | Scopes the dan, zone and kill stores. Also usable by finish-plan C5 (#9). |
| 0x06 SCOPED | both | `[kind:u8 DAN=1, ZONE=2, ROOMEDGE=3, KILL=4][stage:8][table:s8][room:s8][n:u8] n×{word:u8, bits:u32}` | DAN: OR into store[stage, table] (the game keeps dan switches per table; item 114). ZONE: OR into store[stage, table, room]. ROOMEDGE: relay only, never stored. KILL: words 0-15 of the 512-bit actor field, OR into store[stage, table, room]. A dan, zone or room switch bit is kept or relayed only if the host's M1 table marks it latching for that scope; the rest are counted (item 113). Relayed only to peers whose presence matches and who are granted the kind's feature. |
| 0x07 NOTICE | s→c | `[code:u8][len:u8][utf8]` | Codes: WORLD_MISMATCH, NOT_BOUND, DIVERGENT, CLEARCOUNT, SCHEMA, REJECTED, TOMBSTONE |
| 0x08 CTRL | both | `[op:u8][arg:u32]` | Ops: RESYNC, COOP_OFF (host feature mask), TOMBSTONE (host only) |

**CoopHub** (one goroutine owns all world state):
- **World image** with per-class merge and a monotonic `seq`.
- **Scoped stores.** Dan per (stage, table); zone and kill per (stage, table, room). A dan or zone store is dropped when the last player leaves its stage and table, or its room; a kill store when the last player leaves the stage. A player entering a scope is sent what is stored there (items 113-114).
- **World file:** `os.UserConfigDir()/ww-multiplayer/worlds/<worldID>/world.wwco`.
  - Header "WWCOWRLD", version, worldID, seq, CRC32, then TLV sections.
  - Written atomically: tmp file, fsync, rename.
  - Flush 1 s after a change and on Stop.
  - `journal.log` records (seq, time, origin, offsets).
  - Five rotated backups, taken at session start and hourly.
- **Self-healing.** Commits not yet flushed when the host crashes are re-proposed by replicas that still hold the bits, so the world self-heals.
- **Tombstones.** `coop-world block <world-id> (byte <off> <mask> | event <0xHHLL> | switch|chest|item <table> <no>) [--yes] [--force]` (a hidden command of ww-multiplayer.exe) rejects future proposals of a bit. Replicas that already applied it keep it; this is documented.

### 4.5 Code-space plan

Current layout (`inject/build.py`: `inject_address` and the `__OSArenaLo` patch at 0x80301820; `MAILBOX_ADDR` in `mailbox_core.h`; `internal/inject/blob.go`):

| Range | Contents | Size |
|---|---|---|
| 0x803FCFA8..0x8040CFA8 | main stack (grows down; `OSClearStack` zeroes stack_end+4..SP, `OSThread.c:924-933`) | 64 KB |
| 0x8040CFA8..0x8040CFB0 | frame header above the stack top, **in use**: `__start` calls the nofralloc `__init_hardware` with r1 = `_stack_addr`, so `__OSPSInit` and `__OSCacheInit` save LR at 4(r1) = 0x8040CFAC, which keeps 0x8000336C on every booted game (`__start.c:21-23,125-126,173-183`; `OS.c:623-624`; `OSCache.c:446`; `__init_hardware` 0x80003354, sym:5) | 8 |
| 0x8040CFB0..0x80410000 | MetroTRK debug stack plus padding; `InitMetroTRK` runs only if a 32-bit word is 2 or 3: the word at +0xC of the block pointed to by 0x800000F4, or, when that pointer is 0 and 0x80000034 is non-zero, the word at 0x800030E8 (`__start.c:32-62`); free by inference | 0x3050 |
| 0x80410000..0x80412CA8 | T2 (code and data; 0x2E28 before Phase E0 removed the dead C experiments, 0x2588 after E0, 0x25A8 with E1's header writes, 0x2A88 after the E3 split (docs/api §6.6), 0x29C8 after F19 dropped the mini-Link's first calc, 0x2A88 after F20 opened the mini-Link's entry into its four steps (docs/05), 0x2CA8 with the REL loader `src/mp/relload.c`; `inject.T2End`) | 0x2CA8 |
| 0x80412CA8..0x80412F00 | free (`TestT2EndsBelowMailbox`) | **0x258** |
| 0x80412F00..0x804130B0 | mailbox (as below) | 0x1B0 |
| 0x804130B0..0x804130BC | the REL module's words: magic `'WWRL'`, build id, frame counter (`inject/include/wwrel.h`) | 0xC |
| 0x80412F00..0x804130B0 | mailbox (`frame_shim` hard-codes `lwz 12,0x2F08(12)`, `frame_shim` in `src/core/hooks.c`; the Go `mailbox.Mailbox` (wwapi/mailbox/layout_gen.go, generated from mailbox_core.h + mailbox_mp.h); +0x180..+0x187 is the WWMB header (magic, ABI, layout; E1), +0x188..+0x1A0 the docs/09 tunic block, +0x1A0 the features word and `_pad13` (E1)) | 0x1B0 |
| 0x80413100..0x80413298 | the enemy-mirror block (mailbox +0x200..+0x398, `inject/include/wwenemy.h`, layout 2): Go writes +0x00..+0x124 per tick, the REL module's `enemy_mirror.c` writes +0x124..+0x198 (the host-authoritative Moblin prototype, handoff `design/host-authoritative-prototype.md`, `moblin-s2-v2-final.txt`); `region-probe` arms the slack on either side of the enemy region (`mailbox.CanaryArena`, `CanaryArenaHigh`) and never over it | 0x198 |
| 0x804132A0..0x8041340C | the host's v2 At-sphere objects, REL-owned and static (mailbox +0x3A0 `WWENEMY_SPH_OFF`: a `dCcD_Sph` image, 0x12C; +0x4D0 `WWENEMY_STTS_OFF`: its `dCcD_Stts`, 0x3C): `cCcS::Set` keeps their pointers until `dScnPly_Draw`'s Move, so they live outside the module actor; enemy sync ends at mailbox +0x510 (`WWENEMY_REGION_END`) | 0x170 |
| 0x804130BC..0x80413100, 0x80413410..0x80414000 | unused, below arena (0x804130A0 before Phase E1 grew the mailbox to 0x1B0). Any co-op region placed in this slack starts at mailbox +0x510 = 0x80413410 (`WWENEMY_REGION_END`; +0x360 before layout 2 added the S2 slot bytes and the v2 collision objects); the option A region below sits past a moved mailbox and is unaffected | 0x44 + 0xBF0 |
| 0x80414000 | patched `__OSArenaLo` (`addi r3,r3,0x4000` at 0x80301820) | — |

**M0-M6** use no code space.

**M7 prerequisite: make room.**
1. Finish-plan **D2**: drop the dead shadow modes 1-4 and the echo ring. (Done in Phase E0, layout unchanged: T2 ends at 0x80412588, 0x978 bytes free; docs/api/00-design.md.)
2. **Option A, recommended.** It reuses a proven mechanism: the arena has been bumped in 4 KB steps before, and the mailbox has moved four times (the History comment in `mailbox_core.h`; the callback slot five times, per the `CALLBACK_PTR_ADDR` comment in `src/core/hooks.c`).
   - Change `addi r3,r3,0x4000` to `0x6000` at 0x80301820, so `__OSArenaLo` = **0x80416000**. That is +8 KB taken from ZeldaHeap.
   - Move the mailbox to **0x80415000**, layout unchanged. `frame_shim` becomes `lis 12,0x8041; lwz 12,0x5008(12)`. (Done already: `extract_blob.py` emits `MailboxAddr`, `LayoutVersion` and `ArenaLo` into `blob.go`.)
   - (Done in Phase E1.) A mailbox header `'WWMB'` plus `layout_version` at +0x180. Go checks it before any mailbox write, which also catches stale save states.
   - Co-op region at **0x80415200..0x80416000** (0xE00), layout below.
   - The T2 budget becomes 0x80410000..0x80415000 = **20 KB**, against about 0x33A8 needed after the pump (inferred).
   - Gate: the heap error counters `zeldaHeapErrors` 0x803F68FC, `gameHeapErrors` 0x803F68F8 and `archiveHeapErrors` 0x803F6904 stay 0 through boot, Outset (where the 1 MB bump failed, docs/05) and a 50-transition soak. The M0 `heap-free` probe measures the margin first.
3. **Option B (zero arena cost).** Set `inject_address` to 0x8040D000 (T2 budget 0x5F00) and leave the mailbox in place. Allowed only after M0's region canary survives boot, menus and 50 transitions on both Dolphins, and a live read-back of T2 at 0x8040D000 matches `T2Bytes` (the `wwdev.exe blob-verify-live` diagnostic, added to M0's list).
   - docs/05:63-73 reports that Dolphin "silently drops" a section at 0x803FD000. That address is inside the main stack, so `OSClearStack` is the likely cause (inferred). 0x8040D000 is above the stack top, but loader acceptance is only proven by `wwdev.exe blob-verify-live`.
4. **Patcher.** `dol.go` refuses an input DOL with any section overlapping the new T2 and co-op range. That refuses a re-patch of an already-patched ISO. Invariant tests check:
   - T2 end ≤ mailbox base;
   - the mailbox and co-op region lie within [T2 end, arenaLo);
   - arenaLo is decoded from `DOLPatches` merged over the vanilla instruction words, or from an `ArenaLo` constant that `extract_blob.py` emits (today's byte-wise diff holds only `40 00` at file 0x2FE762);
   - every hook site, merged the same way, decodes as a `bl` into T2.

**Co-op region layout (option A base 0x80415200; layoutVer 1)**

| Off | Field | Writer |
|---|---|---|
| +0x000 | magic 'WWCO', u16 layoutVer, u16 flags | C init |
| +0x008 | u8 armed, u8 enable (b0 live OR, b1 shim, b2 isActor, b3 ops), u8 gate_bits, u8 quiet_frames | Go / Go / C / C |
| +0x00C | u32 go_heartbeat | Go |
| +0x010 | u32 c_loads, u32 c_live_or, u32 c_rejected, u32 c_hb_age | C |
| +0x020 | fp: worldID[8] ‖ bindNonce[8]; +0x030 u8 slot, u8 freshBind | Go |
| +0x040 | dead-table stage names [2][8]; +0x050 next-stage name [8] | Go |
| +0x060 | ring head u32 (Go), ack u32 (C) | |
| +0x070 | ring[16] × 16 B: {op u8, seq u8, result u8, check u8, a u32, b u32, c u32} | Go writes, C sets result |
| +0x170 | dead[2][64] u16 = room<<9 or setID, 0xFFFF = empty | Go |
| +0x280 | image: stage[16]×0x24 (0x240), ocean 0x64 at +0x4C0, map 0x84 at +0x530, event 0x100 at +0x5C0, image_gen u32 at +0x6C0 | Go |

A REL module is not needed for this scope. It is reserved in case C co-op code ever exceeds about 16 KB.

---

## 5. Save safety

### 5.1 Principles
1. **Go never writes memory-card files.** The card changes only through the game's save menu (`d_menu_save.cpp:927-1016`), which brings its own checksums and `life < 12` fix-up (`d_save.cpp:1703-1791`).
2. **Unbound files get no game-data writes.** Outside the mod's own RAM, a file that is not BOUND is written in only one place: the 0x30-byte tag in `dSv_reserve_c`. Go writes the tag at an explicit bind (§5.3) and zeroes it on a LEAKED scrub (§5.2) or `coop-tag clear` (§5.5). CI audits that nothing else is written.
3. **Per-player fields are never written.** `mLife` in particular: writing 0 kills the player. No writes happen on the title or file-select screens, where `card_to_memory` overwrites everything and `init` overwrites everything except `mReserve` (§5.2).
4. **Validated semantics only.** Every write is semantic, validated twice (server class table, then the client WriteGuard), and from M7 a third time by the C whitelist.
5. **Dry run writes no game data.** Dry run (`--coop-dry-run`, or dry run under Advanced in the app's lobby) writes only the binding tag and leak scrubs in `dSv_reserve_c` (§5.2-5.3). Every other planned write is logged and not made. Any implausible read, layout mismatch or stalled heartbeat puts the replica into read-only mode, with the reason shown.

### 5.2 Binding tag (`dSv_reserve_c`, 0x803C532C; 0x30 of 0x50 bytes used, with +0x2C..+0x2F reserved and kept zero)

**Layout**

| Offset | Field |
|---|---|
| +0x00 | magic 'WWCO' |
| +0x04 | u8 layout, u8 flags, u8 slot (= `mDataNum` at bind), u8 clearCount |
| +0x08 | worldID[8] |
| +0x10 | bindNonce[8] |
| +0x18 | u64 worldSeq (kept current while armed, so a saved file records how up to date it was) |
| +0x20 | u32 nameHash (FNV-1a of `mPlayerName` 0x803C4D64) |
| +0x24 | u32 reserved |
| +0x28 | u32 CRC32 of +0x00..+0x27 |

**Why this region**
- It is saved and loaded with the file: `memory_to_card` copies it at `d_save.cpp:1780` and `card_to_memory` at `:1867`.
- No vanilla code reads it; only `d_save.cpp` references it.

**Vanilla leaks it to other files (verified):**
- `dSv_save_c::init` (`d_save.cpp:1494-1503`) does not reset `mReserve`. Only `initdata_to_card` does (`:2006-2010`).
- New Game runs `dComIfGs_init` (`d_s_name.cpp:1373`), so the previous file's tag stays in RAM and lands in the new file at its first save.
- A second quest runs `ExDataSave` = `reinit` + `memory_to_card` (`d_file_select.cpp:3625-3628`), which writes the stale tag to the card without the player saving.

**Validity rules** (all must hold for BOUND):

| Rule | Check | Catches |
|---|---|---|
| V1 | magic, layout and CRC are valid | garbage data |
| V2 | `slot == mDataNum` (0x803C5E98) | a tag carried into another slot |
| V3 | `clearCount == mClearCount` (0x803C4DA8) | the second-quest leak |
| V4 | `nameHash` matches `mPlayerName` | a renamed new file |
| V5 | `mDateIPL` (0x803C4C20) != 0, or this bindNonce is a pending fresh bind recorded in `world.wwco` (minted by Go, possibly in an earlier session, and cleared once the file is seen with `mDateIPL` != 0). `status_b.init` zeroes `mDateIPL` (`d_save.cpp:51-56`), and `memory_to_card` stamps the time only into the card buffer (`d_save.cpp:1715-1717`), so RAM stays 0 until `card_to_memory` reloads the file. | a never-saved New Game |
| V6 | worldID == the session's world | otherwise FOREIGN |

When Go sees a LEAKED tag (fails V2-V5) it **scrubs** it: it zeroes the 0x30 bytes so the stale tag never reaches that file's next save.

**Residual edge, documented:** a same-name New Game written over the co-op slot itself while Go was not running.

### 5.3 Binding flow and merge at bind
- `host --coop=new` (or New world in the app's lobby) founds a world from the host's loaded, idle file. It mints `worldID` and `bindNonce`, writes the tag, writes `world.wwco`, and tells the host to save in-game.
- A joiner connects, then its file is classified:

| Classification | Condition | Action |
|---|---|---|
| BOUND_OK | tag matches the world | arm |
| UNBOUND, subset | the file's shared classes are a subset of the world (fresh file, or an older copy of this world's file) | prompt "Bind this file?"; on yes, back up and arm |
| UNBOUND, divergent | both sides have unique progress | the preflight lists per-class gains for each side and the held bits; binding needs `--coop-merge` on the host |
| refused | `mClearCount` differs; either player is in a boss refight; the file is FOREIGN | refuse |
| far behind | a fresh file joining a world whose resume-stage milestones (RODE_KORL 0x2A08, MET_KORL 0x0F80, 0x0801, 0x0808, 0x2401; `d_com_inf_game.cpp:1302-1320`) are set | recommend copying the host's save in over a union (Dolphin's Memory Card Manager exports and imports it; the `coop-world export/import` once named here was never built, Appendix C item 107). The game keeps all three files in one card file, so the copy replaces all three of the joiner's files, and the copied file is the host's own, already bound. The host first saves in game and loads its file from the card (Appendix C item 133). A union is allowed only with `--coop-merge`, and holds apply. RODE_KORL is LocalOnly, so the check rests on the other four (item 88) |

- Binding is always an explicit prompt. Go never auto-binds a file that has progress.
- The hub judges a bind on every byte the world takes, the story bits and memory switches included, whatever the joiner is granted that session (a `--coop-dry-run` join, or stage tables that aren't the host's). A bound file is never judged again, and its later sessions merge its whole WORLD (§5.5). Files bound before M4 were never judged on story, since no world held story bits then: their first M4 session unions their story bits into the world (item 111).

### 5.4 Backups
- At every co-op session start, Go copies the Dolphin card (raw `.raw` image or GCI folder, read-only) to `<config>/ww-multiplayer/backups/<ts>/`. It keeps 10 rotations.
- The card path comes from `Config/Dolphin.ini`, or Dolphin's defaults for what it doesn't set (location inferred, confirmed in M2; Appendix C item 135).
- `coop-world restore <ts>` copies a backup back, but only while Dolphin is closed.

### 5.5 Lifecycle

| Situation | Behaviour |
|---|---|
| Joiner's network drops | The replica goes ARMED_OFFLINE. It stops applying, keeps detecting and queues gains. On reconnect: CO_HELLO, then WORLD both ways (merge), then queued gains. |
| Host server stops or crashes | The joiner behaves as above. The host restarts on the same world file; unflushed commits self-heal from the replicas. |
| Host runs a different world | NOTICE WORLD_MISMATCH: the joiner disarms. Pose still works. |
| A replica loads an older save or a Dolphin save state | The tag's `worldSeq` (+0x18) drops below the value Go last wrote, or the heartbeat regresses, which is detected (the bindNonce is unchanged). The replica is re-classified and catches up at its next stage load. Regressed bits are never proposed as clears. |
| A bound file is played solo (no Go) | Its progress is offline progress. It is merged into the world at the next session, with no bind judgement and no `--coop-merge`. The tag stays, and the game never touches it. So `save-merge` keeps a bound file's tag only when the other file adds nothing or is bound to the same world (item 108). |
| Playing solo afterwards, unbound | `coop-tag clear` zeroes the tag; it persists at the next in-game save. That file is never written again. |
| A different file is loaded mid-session | It is re-classified. Writes stop unless it is bound to this world. The TUI shows "file not bound". |
| Second quest | Refused (V3). The ExDataSave leak is scrubbed. |
| World file lost or corrupt | Restore from `world.wwco.1..5` (`coop-world restore`). (A `coop-world refound` was planned and is not built.) |

---

## 6. Puzzle and world sync plan

### 6.1 How live actors react to synced bits (from the decomp; behaviour inferred until M3/M5 tests)
- **Polling actors start camera events when a bit changes.**
  - Shutters (`d_a_shutter.cpp:133-144`, 233-267).
  - Switch-spawned chests (`d_a_tbox.cpp:1000-1016`, DEFAULT_TREASURE_APPEAR).
  - Ladders (`d_a_obj_ladder.cpp:150-181`) and lifts (`d_a_obj_hlift.cpp:110-164`).
  - Obey-save floor switches (`d_a_obj_swpush.cpp:482-604`).
  - `d_a_andsw2` (87-136).

  Each orders its own OTHER event with actor2 = player 0 (`f_op_actor_mng.cpp:692-740`). `demoCheck` does not check Link's state (`d_event.cpp:297-329`), so the receiver can lose the camera mid-combat.
- **A new shutter ignores its switch for 30 frames.** `daShutter_c::Create` reads the switch to stand open or shut and sets `mFrameTimer = 30` (`d_a_shutter.cpp:71-87`). Each execute counts the timer down and ends by copying the switch into `mbIsSwitch` (`d_a_shutter.cpp:133-142`). `demo` starts the opening only when the switch differs from `mbIsSwitch` and the timer has run out (`d_a_shutter.cpp:233-243`). A switch set within the first 30 executes is therefore never seen: the shutter stays shut with its switch set until its room's actors are created again, and a latched switch is not set again. The shutter does not execute while an event runs (`moveApproval` gives it NOMOVE, `d_event.cpp:626-670`; `fopAc_Execute`, `f_op_actor.cpp:178-200`) or while the pause menu is up (`f_op_actor.cpp:175`). A room's actors are created when a door shows the room (`d_door.cpp:282`; `objectSetCheck`, `d_s_room.cpp:72-76`) and deleted when it hides it (`d_door.cpp:305`; `d_s_room.cpp:79-83`). So M5's receiver holds a partner's switch until the actors of its room have run for 2 s (item 121). M3's memory switches of the current table wait for the same hold, since a shutter reads them the same way (item 126).
- **Light bridges react with no event** (`d_a_lbridge.cpp:135-189`). A remote *clear* makes the bridge vanish under the local Link. Clears are never sent.
- **Key doors** (`door10`/`door12`/`kddoor`): `execute` resets the action to `actionInit` on a frame `checkExecute()` returns 0 (`d_a_door10.cpp:811-812`; `d_a_door12.cpp:830-831`; `d_a_kddoor.cpp:947-948`). That happens on the one frame after Link's room (`mStayNo`) changes, since the door compares it with `mRoomNo2`, which each execute sets at its end (`d_a_door10.cpp:829`), and while Link is in neither of the door's rooms or neither room is shown (`d_door.cpp:155-171`). `adjoinPlayer` is room display, not distance (`d_door.cpp:63-75`). The next frame that returns 2 runs `actionInit`, which calls `setKey()` (`d_a_door10.cpp:747-759`, `:27-50`; `d_a_door12.cpp:765-773`; `d_a_kddoor.cpp:900-908`, `:17-25`), which re-reads the switch; the wait action never does. So **a key lock disappears only at the receiver's next room change** (or when the door's rooms are hidden and shown again), and only if the door's switch is in the receiver's live copy by then. A switch set while the receiver stays in one of the door's rooms leaves the lock up until it changes room. The door refuses to open while locked at `keyNum == 0` (`d_a_door10.cpp:359-372`). *Corrected (found on the owner's P2 run, item 139):* this said the lock disappears when the receiver next approaches the door.
- **Create-only state** (chests `d_a_tbox.cpp:447-455`, items `d_a_item.cpp:227-237`, `obj_stair` 150-151): a partner's change shows only after a room reload. Duplicate pickups in the receiver's current room are possible (§6.7).
- **Per-frame writers fight synced values.**
  - `d_a_swc00` type 0 sets or clears its switch every frame depending on local Link (`d_a_swc00.cpp:12-33`).
  - `d_a_tag_evsw` mirrors an event bit every frame (`d_a_tag_evsw.cpp:35-48`).
  - Timers clear switches: `d_a_obj_timer.cpp:72-74`; `d_a_andsw0.cpp:98-107`; `swhit0` timer types 347-367.

### 6.2 Switch classification (M1, from the user's own ISO)
- `wwapi/stage` (was `internal/coop/stagedata`) parses every stage's DZS/DZR (ACTR, TGOB, TRES, TGSC, SCOB, TGDR, DOOR and the ACTn/SCOn/TREn layer chunks; Appendix C item 16). It maps object names to procs through `l_objectName` (`d_stage.cpp:437-1262`, read from the user's own main.dol at .data 0x80372818, 0x26AC bytes, sym:14693; Appendix C item 15).
- **Latching setters (sync):**
  - un-timed crystals and torches;
  - floor switches types 0/3 (swpush, and swflat, the weighed `Hfbot1A-C`, below);
  - iron-boots switch type 3;
  - ALLdie (`param >> 8`);
  - chest switches;
  - breakables;
  - one-shot `andsw2`;
  - key-door switches (`prm & 0xFF`, set by `keyInit` below 0x80, `d_door.cpp:433-447`);
  - door and chest plants (`Ss`, proc 0xFD: `params >> 24` set as the cut plant dies, 0 and 0xFF none) and Mothulas (`gmos`/`gmos_g`/`gmos_f`, `GM`: `params >> 24` set as it dies, only 0xFF none, written only when `(params >> 8) & 0xFF` is 0 or 0xFF and the low byte of angle.z is not 0). `d_a_ss.cpp` and `d_a_gm.cpp` are NonMatching, so both were read from the retail RELs (`d_a_ss.rel` 0x2920-0x2950, 0x3204-0x3264; `d_a_gm.rel` 0x5854, 0x5A2C, 0x6860, 0x77C8, 0x7F24-0x7FE4); neither clears or toggles a switch (tables SchemaVersion 7);
  - statue slots (`Obj_Correct` types 4 and 5) with a twin statue. A slot sets its switch while a statue of its type sits in it and clears it when the statue leaves (`mode_off`/`mode_on`, `d_a_obj_correct.cpp:279-302`), so on its own it is unsafe. `Obj_Try` (NonMatching, read from the retail `d_a_obj_try.rel`) only reads its switch: the module's one switch call is `isSwitch` in `chk_appear` (0x428-0x50C, the call at 0x4A0, run once from `_create` at 0x688), and only for types 5 and 6, the two with `M_attr` +0x70 set (`.rodata` 0x320, 0x398). A statue appears unless the switch is on and params bit 31 clear, or off and bit 31 set. So a slot whose room holds an `Obj_Try` of the type it takes (`search_tryKeyGate` UNK_5 for slot type 4, `search_tryKeyDoor` UNK_6 for type 5, `d_a_obj_correct.cpp:186-229`), with bit 31 set and within the slot's on radius (`L_attr` `field_0x4` = 9.0, a squared distance, `:30-39`, `:289-292`), latches: in a game that receives the switch, the carried statue stays away, the twin appears in the slot, and the slot, created in `mode_on` (`:59-63`), keeps the switch on. TotG r0 has two: `CrTrM1`/`Hmon1d` on sw5 (the iron grate) and `CrTrM2`/`Hmon2d` on sw6 (the water spouts), both shared with Tingle Tuner switches, so they sync while no GBA is linked (SchemaVersion 8). Limits: a player who lifts the twin out clears the switch in his own game until it is next applied, and `chk_appear` refuses the twin while a statue of its type already exists (`M_attr` +0x71, 0x4D0-0x508), so a player carrying his own `Hmon1` into r0 after the partner's set gets no twin, and the slot clears the bit when that statue isn't in it. (`Obj_Try`'s `prm_set_swSave(0xFF)` when a statue leaves its home room, 0x1AC8-0x1B24, needs `M_attr` +0x72, which types 5 and 6 don't have, and writes params, not a switch.)
- **GBA writers (sync while no GBA is linked):** the Tingle Tuner switches (`agbsw0`, every type the game has, 0-0xE). Each writes its switches only on a frame where a Game Boy Advance is linked (`mDoGaC_GbaLink`, `m_Do_gba_com.cpp:258-270`): the actor itself (`ExeSubMW` clears sw0 at `d_a_agbsw0.cpp:1117` after returning while unlinked at `:1074`; `ExeSubT` and `ExeSubS` set at `:1158` and `:1198-1218` inside their `mDoGaC_GbaLink()` checks at `:1153` and `:1193`; the other types write none), and the GBA player through the Tuner actor (`SwitchOn`, `d_a_agb.cpp:834-853`, called only under `mDoGaC_GbaLink` at `:1682-1684`). They don't stop a switch from syncing, nor an input from being momentary (below), but a switch one of them shares, an AND fed by such a switch, and a one-shot AND fed by a momentary input one of them shares (FW sw7, whose crystal input sw9 a Tuner switch also names), syncs only while no GBA is linked: the tables mark it GBA-dependent, and a replica neither proposes nor writes it while a GBA may be linked to its game (§6.3, item 138). A type past `ExeSubTable`'s 15 entries is unknown, so it keeps its switches unsynced.
- **Unsafe (never sync):**
  - `SW_C00` area switches of type 0 (types 1-3 latch; Appendix C item 27);
  - `tag_evsw` (classed Event, carried by §6.5's story batch; Appendix C item 27). Type 1 only sets, so next to a latching setter it doesn't stop the switch syncing (below): Wind Temple r15's tags grow every Makar tree once 0x4004 is on, and the Earth Temple's set sw43/44/111 on 0x3A02. A switch only it writes still follows its event bit;
  - `ObjTime`;
  - timed `SW_HIT0` and torches;
  - momentary floor switches (swpush type 1; swheavy types 0/1, which clear on release, `d_a_obj_swheavy.cpp:364-390`; swflat types 1 and 2, below; sun switches, `Obj_Swlight` type 1, cleared when the light leaves, `d_a_obj_swlight.cpp:372-380`);
  - inverted floor switches (swpush type 2, FLAG_ON_IS_UP, which clears on press, `d_a_obj_swpush.cpp:544-551`) and toggling iron-boots switches (swheavy type 2, `rev_switch`, `d_a_obj_swheavy.cpp:333-334`);
  - continuous `andsw2`;
  - `andsw0` behaviours other than 0 and 3, which latch (Appendix C item 26);
  - any AND switch fed by an unsafe input (this prevents remote ambush triggers), except a one-shot AND (`andsw0` behaviours 0/3, `andsw2` type 0, which only ever set their output, `d_a_andsw0.cpp:40-57, 129-135`, `d_a_andsw2.cpp:87-135`) whose unsafe inputs are all momentary: each has a momentary writer, a floor switch that clears on release (swpush type 1, swheavy types 0/1, swflat types 1/2, swlight type 1: the Earth Temple's r3 and r16 sun-switch pairs) or a timed crystal (`SW_HIT0` with a timer, which clears it when the timer runs out, `d_a_swhit0.cpp:346-362`), and every other writer is momentary too or a Tingle Tuner switch (above). Its output latches and syncs, while the presses and hits never do (DRC sw41, the room 16 puzzle and the room 14 barricade, tables SchemaVersion 6; FW's room 13 five-crystal gate sw7 and kinMB room 10's crystal pair 0xE2, SchemaVersion 7; TotG's r0 pair sw50/sw51 into sw12, the r0-r5 door, and the r6 triple 0xE0-0xE2 into sw32, the lifts, SchemaVersion 8). Still not momentary, so an AND they feed stays unsafe: an area switch's per-frame writer (`SW_C00` type 0, so a partner walking in can't spring an ambush), and an input another writer clears (a switch timer, a timed AND's clear, a timed torch). Timed crystals are safe inputs because, on the owner's disc, only those two one-shot ANDs are fed by them (the two timed ANDs they feed, memory t8 0x13 and t13 0x00, never latch), and each crystal's stop switch (`getSwNo2`, angle.z & 0xFF, `d_a_swhit0.cpp:99-101`, `:352-354`) is the AND's own output, so once either side's AND fires the crystals stay lit;
  - push-block path switches (`d_a_obj_movebox.cpp:1153-1198`, which encode a path index; OR-ing two of them gives a corrupt third).
- **Weighed floor switches (`Obj_Swflat`, `Hfbot1A-C`, Tower of the Gods only).** `d_a_obj_swflat.cpp` is NonMatching with empty getters, so this is read from the retail `d_a_obj_swflat.rel`. `_create` (0x2C8-0x4A0): type `PrmAbstract(2, 0)`, sw `PrmAbstract(8, 8)`, sw2 `PrmAbstract(8, 16)` (0x340-0x374). A type 2 becomes type 0 when sw2 is already on and type 1 otherwise (0x434-0x474; `isSwitch` is false for 0xFF, `d_save.cpp:1586-1589`, so a type 2 without sw2 is a type 1), and it starts pressed when the switch it then watches is on (0x478-0x4A0). Pressed by an actor with `fopAcStts_FREEZE_e` (status & 0x400, 0xBC0-0xBC8: the player, statues, boxes, AM2, a CC in one state), it sets sw every frame (0xD80) unless the presser is the player (profile `fpcNm_PLAYER_e` 0xA9, 0xBD0-0xBDC, skip at 0xD64): the player's first press sets it once, after a camera event (0x1018, 0x1078, 0x10CC), and nothing re-sets it while Link stands there; 0xE5C, on any presser, runs only while the emitter's alpha (0x466) climbs back to 0xFA after a quick re-press (0xE28-0xE38). So a statue, box or other weight re-asserts sw every frame and Link does not. Unpressed, it clears sw every frame (0xF08). Only type 1 lets go when the weight leaves (0xC0C-0xC28); a type 2 whose sw2 turns on becomes type 0 (0xC2C-0xC8C) and is held down (0x10D0-0x10E0). Its create-time `offSwitch` pair (0x3DC-0x42C) runs only with `l_HIO`+0x19 set, which the HIO ctor zeroes (0x108). So type 1 is momentary; type 2 is momentary until its sw2 (on the disc, the AND it feeds) is on, then stays down; types 0 and 3 stay down once pressed (latching; none is placed). A remote Link's body can't press one: its profile status is 0x40000 only, with no 0x400 (the same finding as for swpush). TotG has 17, of which 4 share a bit with a latching setter: r0 #18 on sw0 (with the Hmlif and Kui setters; the r0-r4 door reads it) and r12 #0, #1, #4 on sw14 (the r8-r10 key door), sw16 (the boss-key chest) and sw17 (the r22 Joy Pendant chest). Counted as unsafe, they would stop those four from syncing; so a swflat writer beside a latching setter that is neither an AND nor a swflat counts as latching, and the four sync as they did before the decoder (`sharedFloorSwitches`, SchemaVersion 8). This is provisional until a live check (the TotG slice's C2) shows what vanilla does: in either game an unpressed r12 swflat clears the shared bit every frame, as in a single-player game, and a press there sets it, which the replica may then propose to the partner. sw15 stays unsafe: r12's continuous AND writes it. The other swflat bits (r4 sw35, r10 sw37, r14 sw30, r15 0xE0, r16 sw8) have no other setter and stay per game.
- **Start events' spawn switches.** Link's create starts its PLYR entry's event (params >> 24) unless the start mode picks a fixed one (`d_a_player_main.cpp:12280-12298`), and `setStartDemo` skips that stage EVNT entry's event while its spawn switch (byte 0x13) is on and sets it otherwise, in the room Link starts in (`d_event_manager.cpp:24-51`). The tables list each such switch with the PLYR entry as its writer (proc PLAYER, `startEvents`, SchemaVersion 10). It is an event writer, so each player watches their own cutscene, and it keeps the switch each player's even next to a latching setter (SchemaVersion 11). Before that it counted as a pre-set like a type-1 `tag_evsw`, so kenroom's `swing_sword` (table 9 sw13), which Hyroom's 16 torches also set, synced: a partner who lit them or drew the sword first made the receiver skip `swing_sword` and its move to layer 6 (the overworld slice's A2). Now the torches light only in the game that lit them, which is cosmetic. Two latch: Ganon's Tower's intros, GanonK `kugutu_ganon` (sw30) and GTower `g2before` (sw31), whose STBs set 0x3B02 and 0x4002, the bits that take those stages off their intro layer 8 (`d_com_inf_game.cpp:255-262`). Unsynced, a partner who got the bit loaded the time or fight layer and still started the intro event, whose staff only layer 8 places; synced, the receiver gets the switch with its bit, the vanilla retry state. GanonK `to_roof` (sw18) and GTower `endhr` (sw16, the ending) stay each player's. The sender's story batch couples the switch with its bit by timing only (§6.5 item 91 (c)), and the D2(9) rerun on #175 saw sw30 leave in a batch about 4 s into A's intro and 0x3B02 only after the intro ended, although A's save had the bit from the intro's start (timed rerun gt-R4b; which sender path kept the bit back is not yet traced): B, given sw30 into table 8's saved slot at once (another table's switch is written on any STABLE tick) and 0x3B02 never (it was not in the world at either of B's stage changes, the only ticks a story bit is written), loaded layer 8 with the intro skipped. So the receiver couples them (`mods/coop/replica` `spawnSwitch`, which lists the two pairs): the planner holds the switch while the receiver's own image lacks the bit (`holding t08 GT switch 0x1E back`), and the stage change that writes the bit writes the switch on the same tick, after the bit in the batch, saved and, with table 8 loaded, live. That tick's STABLE is closed, so the WriteGuard opens exactly those switch bits on it (`Tick.spawnMask`), armed and with HEARTBEAT. The FrameWriter writes those bits alone from a byte that also holds other planned bits (`WriteGuard.readyBits`); the rest wait for STABLE. The other way round, the receiver holds the bit while the switch cannot be written with it (the world lacks it, it does not sync, this game once had it, or the hub does not grant switches): alone it would start the intro on the time layer without its staff. A receiver entering before the partner's pair can go gets neither and plays its own intro; one entering after gets both. A missed transition tick leaves both unwritten, not one, and the next stage change writes the pair. A batch cut between the two writes (a memory error, co-op turned off) can leave the bit alone, accepted: the switch follows at its own timing. Live check: docs/TESTING.md "Ganon's Tower intros' spawn switches".
- A switch is synced only if it has a latching setter and every other writer of that (scope, number) is latching too, a GBA writer (above) or a type-1 `tag_evsw`; a start event's writer counts as latching only for the two Ganon's Tower intros (`startEventLatches`). The scope is the save table for memory and dan switches (dan switches are kept per table, Appendix C item 25) and the stage and room for zone and room switches.
- Appendix C items 25-31 list the setters and refinements M1 added from the decomp; item 138 the Tingle Tuner's.
- The table is cached under the user config directory, keyed by DOL hash, and also built by `patch`. Its hash travels in CO_HELLO. A change to the tables' schema or to any verdict changes that identity, so in a session of mixed builds the player whose build differs from the host's loses every table feature (`hub.TableFeatures`: memory and live switches, puzzles, combat, keys and life; items 137, 138).

**Running it (`coop-tables`, M1).** On the owner machine, run `./ww-multiplayer.exe coop-tables <your vanilla .iso or .ciso>` (the patched ISO works too; its DOL hash differs, so it gets its own cache file with the same tables).
- Output: a status line, then a header (main.dol SHA-256, stage and room counts), the **Acceptance** block, and the four section reports.
- The header also names the stages left out because the play scene can't load them. The owner's disc has at least `Name`, the file-select (name) scene's stage (Appendix C item 127).
- The header also names the stages left out on save table 15 (STAGE_TEST, `d_save.h:904`): the developer test stages. The debug map select lists most of them, and no retail exit is known to reach one (A_umikz's two SCLS entries to I_TestM are taken as leftovers, inferred; Appendix C item 137). They are read, then left out of all four sections, and the stage and room counts are of the stages kept. The owner's disc has 26 (Appendix C item 137).
- A build fails on an archive that doesn't parse, on one the disc lists as larger than the archive cap, and on a read error. The error names the failed archives (up to 10): each stage's Stage.arc first, in listing order, then the rooms of the stages whose Stage.arc loaded.
- Flags: `--section keys|hearts|switches|enemies` limits the report to one section. `--json` prints the tables as JSON on stdout, with the status on stderr. `--no-cache` rebuilds from the disc and neither reads nor writes the cache.
- The cache is `ww-multiplayer/coop-tables/<main.dol SHA-256>.json` under `os.UserConfigDir` (`%AppData%` on Windows, `~/Library/Application Support` on macOS). A file written with another schema, by another ww-multiplayer build, or for another stage-file listing is rebuilt over, as is one that doesn't parse. Development builds with uncommitted changes share one build ID, so after editing a builder, pass `--no-cache`.
- The status line says whether the tables came from the cache or were built, with the time of each step (main.dol, stage archives read and expanded, the builders). The M1 target is under 5 s for a build.
- Compare the Acceptance block with:
  - small keys and key doors (sources/doors): DRC 4/4, FW 1/1, TotG 2/2, ET 3/3, WT 2/2, and no key doors outside tables 3-7;
  - 44 heart pieces and 6 containers. The pieces are 30 from the stage files, one of them the Outset pig's dig spot, plus the 14 catalogue rewards, 3 of them unverified minigames. The containers are the 5 bosses' plus Helmaroc King's Bitem; Ganon's Tower's refights give none (Appendix C items 23, 137);
  - a non-empty push-block exclusion list;
  - for M6's go/no-go, the enemy setID share and the duplicate (stage, room, setID) keys. These are INFO lines with no target.
- A FAIL line gives the count. The section below lists every source, door and problem behind it. The keys section has no save table 15, whose stages are left out.
- Also read the switches section's "Undecoded switch users" (Appendix C item 30). Its counts line `Synced only while no GBA is linked (shared with the Tingle Tuner): N (…)` gives the switches the Tingle Tuner guard holds back while a GBA may be linked, and each dungeon marks them `ok*` (item 138). The owner's disc has 61: memory 53, dan 5, zone 1, room 2 (schema 5).

### 6.3 Apply policy
| Class | Default | Opt-in |
|---|---|---|
| Memory switches, cold tables | written to the saved slot as soon as STABLE; IDLE is not needed, so also while the pause menu is open or an event runs (item 128) | — |
| Memory switches, receiver's current table | **deferred to the receiver's next room change, then held until the rooms settle** (written on the first STABLE and IDLE tick after `mStayNo` changes on which every shown room's actors have had 2 s of IDLE frames since they were created: the room hold, items 121, 126, 129. That is at least 4 ticks (about 400 ms) after the change, and usually about 2 s; the pause menu or an event delays it until it ends, even in a room whose actors have settled. Until then the write waits; it is not dropped. The entered room is already loaded by then (`dStage_RoomCheck`, `d_stage.cpp:2263-2289`), so its polling actors still react, and a new shutter sees the switch change (§6.1); only actors of rooms still being created read the bit at creation (inferred)) | `--coop-live-switches`: apply when IDLE, once the room hold has passed |
| Dan switches 0x80-0xBF | live when IDLE, from the store of the receiver's stage name and save table, only while the partner's last PRESENCE puts it in that stage and table too (items 114, 122), once every shown room's actors have had 2 s of IDLE frames (the room hold, item 121) | — |
| Zone switches 0xC0-0xDF | live when IDLE, from the store of the receiver's stage, table and room, into the zone the receiver's own game has for that room, checked again on the write tick (item 115), only while the partner's last PRESENCE puts it in that room too (item 122), once the room's actors have had 2 s of IDLE frames (item 121) | — |
| Room switches 0xE0-0xEF | applied on the edge only, once: written if Link is still in the room he was in when the edge came (the same visit, item 124) on the first tick the gates open and the room has settled, after at most 10 ticks of closed gates; never stored or replayed (item 116) | — |
| Never | clears; unsafe switches; event registers outside the allowlist; `mTmp`; a dan, zone or room switch set again in a scope where the receiver's own game ever had it: a dan switch's scope is the save table (items 117, 125) | — |
| While a GBA may be linked | a switch shared with the Tingle Tuner (GBA-dependent, §6.2), in every store, is neither proposed nor written by that game's replica: memory switches wait (they are written once the link is gone, by the rules above), a dan or zone bit waits in its store, a room edge is dropped. One that rose in that game meanwhile is never proposed later: a memory switch is in the differ's baseline, a dan or zone bit is tainted out of every SCOPED (the whole words of the refresh and of a room entry too) until the game clears it or the baselines drop, a room edge was an edge (item 138) | — |

From M7 the getSave shim applies memory switches at every stage load as well. The deferred current-table write stays, because room changes do not call `getSave`.

**As built (M5).** Dan, zone and room switches travel in SCOPED (§4.4), and the M1 switch table judges them on both sides (`synctables.SyncedScopedSwitch`, item 113):
- The host's hub keeps and relays only the switches its table marks latching for that scope. Each writing replica proposes only those, and writes only those its own table marks latching.
- `hub.FeaturePuzzle` is a table feature: a player whose stage tables are not the host's is not granted it (item 69), and its replica says so.
- The sender proposes the latching dan and zone words whole on entering their scope and every 10 ticks, so players who meet in a room catch up. Room switches go as edges only (item 118).
- Every write is one byte ORed in, through the WriteGuard and the FrameWriter's scoped path. The path reads Link's room and its zone slot again on the write tick and verifies on the next one (item 115).
- A bit is never written twice in a scope, and never where the receiver's own game already had it, so a bit the game clears stays cleared (item 117). A dan bit's scope there is the save table, as the game keeps one `mDan` (item 125).
- The receiver writes a dan or zone store only while the partner's last PRESENCE is in the store's scope, and keeps a store only while it is in that scope itself. The hub sends the store again when the receiver enters (items 122, 123).
- Every write waits until the actors that read the switch have had 2 s of IDLE frames since they were created (the room hold, item 121). A shutter ignores a switch that changes in its first 30 frames (§6.1).
- `--coop-dry-run` asks for the image alone, so it gets and writes no room puzzle (item 119).

### 6.4 Derived small keys (coupled to door switches)
- **Sources and doors per table** come from M1:
  - the 12 vanilla sources: DRC 4, FW 1, TotG 2, ET 3, WT 2 (wwrando `logic/item_locations.txt`; ww-online `docs/small-keys.md` §0);
  - key doors are door10 types 4/5, door12 type 1 and kddoor type 2, each with switch `prm & 0xFF`. `keyInit` records the switch only below 0x80 (`d_door.cpp:433-447`; Appendix C items 19-20). `coop-watch` still confirms the `keyInit` events live.
- **Target per table:** `keys(t) = |sources set| − |doors set|`, computed over the receiver's **own applied flags** (live ∪ saved for t), not the world image. A key decrement therefore takes effect only once the matching door switch is in the receiver's copy. A door switch co-op itself wrote into the loaded table's live copy is **held**: it is not among the doors set until the receiver's next room change after the write, when the door reads it again and drops its lock (§6.1), or until the receiver leaves the door's stage or table, where the door is made anew. So no phantom-lock soft-block can occur. *Corrected (item 139):* this said the lock is gone when the receiver next approaches the door, and counted the door as soon as its switch was written, which left a door that still locked and no key (the owner's F13).
- **Correction** runs only when IDLE, settled (no local key change in the last 1 s) and with no write pending for that table:
  - Current table with the key HUD on (`stagInfo->mProp & 1`; stagInfo pointer 0x803C9DA0; `d_stage.h:1023-1029`): the delta goes into `mItemKeyNumCount` (0x803CA77C), which `dMeter_keyMove` applies and animates (`d_meter.cpp:5242-5257`).
  - Current table without the HUD: write the live `mKeyNum`.
  - Other tables: write the saved byte 0x803C4FA8 + 0x24·t.
- **Duplicates self-correct.** Both players open the same key chest, or a key is spent on a door the partner already opened: the count converges to the derived value.
- **Key sync is enabled per table** only when that table's M1 invariant passes: sources == doors == the vanilla count, and every source and door is recorded in a saved bit (Appendix C item 21). A key from an unknown source is never removed.
- **Known exploit, documented:** using a key on a *different* door during the deferral window can open one extra door. The window runs until the door's switch is written and, for the loaded table, until the receiver's next room change after that (item 139).
- **M4 as built** (`mods/coop/derive`; items 94-98):
  - The target is `min(max(keys(t) + extra, 0), 99)`. `extra` counts the keys from sources the table doesn't know: each rise of the count, while the Deriver tracks the file, that no known source explains. It starts at 0 on the Deriver's first tick of a file and after every reset (a failed read, a save state, the title, a restart of the tool): with the invariant passing, a count above `keys(t)` there is a door's key not yet taken or a duplicate, and is taken out. A door spends an extra key like any other (item 94).
  - The counter is written only while the stage shows the key HUD. For the loaded table without it, only the live `mKeyNum` is written, never the saved byte too, because `putSave` copies the live memBit over the saved slot (`d_save.cpp:1511-1514`). Every derive write is compare-and-set, since the game moves the same counter (+1 `d_item.cpp:666`, -1 with the door's switch `d_door.cpp:439`) (item 95).
  - The derive writes are a second heartbeat batch after the merge batch (`FrameWriter.ApplyDerived`), the only path the WriteGuard lets reach a DERIVED byte or a pending counter. Each counter is written whole, only while it reads 0, and only while IDLE and settled (1 s with no local change and no write of that table in flight) (item 98).
  - The hub grants `FeatureKeys` only when both sides' stage tables match (`hub.TableFeatures`). The startup log names the tables whose keys are derived and why any other is not, and `/status` gives each table's count, sources taken, doors open and target (`derive.Status`).
  - **Held doors** (item 139). The replica tells the Deriver of every bit the merge path set in the live memBit (`Deriver.WroteLive`). A key door's switch among them is held if the last tick had its table loaded, Link in a stage where the door is placed, and the switch not yet set in either copy. It is counted again on the first tick whose `mStayNo` differs from the first tick after the write, when Link leaves its stage or table, on every reset of the Deriver, or when the count falls by a key no door counted open explains. The receiver opening the locked door with a key it kept is most often counted by the room change through the door: `keyInit` spends the key only from the door's front room (`d_door.cpp:435`), and Link goes through in the door's own event, while the Deriver is not IDLE. The spent-key release covers an opening with no room change after it (a door whose two rooms are one). A key-counter write of the derive's own stays in flight while `mItemKeyNumCount` reads nonzero, past `DefaultInFlight`, so a -1 a stalled game takes late is not read as a key spent on a held door. The log says `small keys, table 3 (DRC): door 0x10 is counted after your next room change: …`, then `door 0x10 counted: your room changed, …`, and `/status` adds `(door 0x10 counted after your next room change: its lock stays until then)` to the table's line.

### 6.5 Story event bits
- **SyncMask:** every named bit in bytes 0x00-0x41, minus:
  - **LocalOnly:** every bit passed to `offEventBit` (50 call sites in 10 files, excluding `d_save.cpp`; 0x2D01's only clear is the debug map-select menu, `d_s_menu.cpp:244`), plus ENDLESS_NIGHT 0x0A02, RODE_KORL 0x2A08, 0x3820, 0x3B20 and 0x3F40 (`endhr.stb`, the ending: while it is set Link draws no sword or shield in any stage, `d_a_player_sword.inc:247-264`, so a partner's ending would hide them before this player's own Ganondorf fight);
  - the 34 game-cleared bits (D8): 0x0E08, 0x1108, 0x1110, 0x1140, 0x1301, 0x1302, 0x1304, 0x2002, 0x2004, 0x2080, 0x2680, 0x2801, 0x2802, 0x2804, 0x2940, 0x2980, 0x2D01, 0x2F01, 0x3080, 0x3410, 0x3820, 0x3B01, 0x3C02-0x3C80, 0x3F01, 0x4008, 0x4020, 0x4040, 0x4080. *Corrected (doc vs decomp, item 86):* in GZLE01 D8 is 31 bits. 0x0E08, 0x1108 and 0x1110 are cleared only by demo-build code, so they sync, and LocalOnly is 34 bits (35 with 0x3F40);
  - the refight snapshot bits 0x3D80, 0x3D40, 0x3D20 and 0x3D10 (`d_meter.cpp:1070-1085`).
- `scripts/gen_event_masks.py` generates the mask from the decomp, with citations. It is cross-checked against ww-online's 37-bit LocalOnly list (`docs/event-flags.md`, MIT) and committed as Go data.
- **Holds** (receiver side: the bits stay in the world but are not applied locally yet):
  - MET_KORL 0x0F80, 0x0801, 0x0808 and 0x2401 move the resume stage (`d_com_inf_game.cpp:1302-1320`). They are held until this player has MET_KORL locally.
  - 0x0101, 0x0E20 and 0x0520 pick Outset's layer (`sea` r44: 9, 2/3, 4/5; `d_com_inf_game.cpp:193-200`), and 0x0520 also Asoko's layer and deck exit (`:214-217`; `d_stage.cpp:2324-2329`). Layer 4/5 places no Tetra and no pirate ship, so a partner's 0x0520 would strand a player who has not sailed yet. They are held until this player has MET_KORL locally too (the overworld slice's A1).
  - 0x1820 (the ruined Forsaken Fortress: `sea` r1 layer 3, ma2room loading ma3room's files, `d_com_inf_game.cpp:207-208`, `d_resorce.cpp:708`) is set by `Warpdm20` on create once ZELDA_AWAKENED is set (`d_a_warpdm20.cpp:123-125`). It is held until this player's own rescue, 0x2D01, which is LocalOnly (the overworld slice's A3). Between that rescue and the player's own `attack_ganon` it can still land; the main path, `M2tower` to `M2ganon`, loads neither `sea` r1 nor ma2room.
  - 0x2D10 is held until a Master Sword is equipped.
  - 0x3804 is held until ZELDA_AWAKENED.
  - The last two lock boat boarding (`d_a_ship.cpp:4224-4229`, per ww-online).
- **Batching.** The sender sends a batch of new bits only after its game has been IDLE for at least 1 s, so a cutscene's side-effect bits travel together.
- **Applying.**
  - Before M7: Go writes the receiver's pending bits **on transition**, on the first tick where `mNextStage.mEnable` (0x803C9D54) == 1. Event bits are not copied by `getSave`/`putSave`, so they persist into the next stage, whose layers (`getLayerNo` 0x80052400, `d_com_inf_game.cpp:185-269`) and actors read them at creation (inferred).
  - From M7: the getSave shim applies them.
  - A cutscene-trigger switch (`daTag_Event` swbit, stage `mSpawnSwitchNo`, `d_event_manager.cpp:42-47`) is never sent without its batch.
- **M4 as built** (`mods/coop/story`, `mods/coop/class/syncmask_gen.go`; items 86-93):
  - **Holds.** MET_KORL itself is held until the receiver has it, so sync never applies it. RODE_KORL is LocalOnly, so far behind rests on the other four milestones (item 88). A third boat hold was added: 0x3E10 waits for 0x3F80 (`d_a_ship.cpp:4227`, `:4229`). Outset's layer bits 0x0101, 0x0E20 and 0x0520 wait for MET_KORL, and 0x1820 for the receiver's own 0x2D01; an `UntilLocal` hold may wait for a LocalOnly bit, since it reads only the receiver's own image. A WORLD carries no LocalOnly bit, so the hub's bind preflight leaves such a hold out of its NOTICE and the file's replica judges it (`replica.Preflight`). `/status` lists each held bit and why.
  - **Batching** is the sender's pace (`replica.Differ.Pace`, `differ_pace.go`), on only for a writing replica. Story bits and the allowlisted registers wait in one batch until the game has been IDLE for `story.BatchIdle` (1 s) and the batch has gained nothing for as long. A memory switch always goes with a batch: one that rises with no batch waiting starts one. A reconnect's WORLD leaves the waiting bits out (`Differ.Withhold`) (item 90). The switch coupling is partial (item 91).
  - **Registers** travel in the story batch and are written on the same transition tick as the bits, not on load (item 89).
  - **Policy.** The hub's `DefaultPolicy` opens the SyncMask, and a session's policy is `hub.SessionPolicy(verdict)` everywhere: the hub, each writing replica and the world a host founds (item 92). The hub gates the story bytes 0x00-0x41 with `FeatureStory`.
  - **Applying.** The receiver (`story.Receiver`) hands the pending bits to the merge path on the first tick with `mNextStage.mEnable` == 1, and the log says `a stage change starts: writing N story bits and M registers of the world`. The FrameWriter's recheck keeps that batch only while `mEnable` still reads what the snapshot read (`writer.go`).

### 6.6 Inventory and equipment
- Slots are derived from the merged `mGetItem` flags. The best variant is used (bow slot 12: bits 0/1/2 give bow, fire-ice, light), the same model the game uses itself (`d_s_play.cpp:1385-1392`). A slot is only filled if it is empty (0xFF) or holds a lesser variant; nothing is ever removed.
- **Ordered grants, capacities by MAX.**
  - `item_func_bow` sets arrow count and max to 30 **unconditionally** (`d_item.cpp:733-738`). The bomb func sets bomb max to 30 unconditionally (`d_item.cpp:796`). Quiver and bomb-bag upgrades set 60/99 absolutely (`d_item.cpp:1370-1388`).
  - So side effects are applied base first, then upgrade. Capacities are then forced to MAX(local, world) and are never lowered.
  - The same post-fix applies to M7 GRANT.
- **Context rules.**
  - No sword while 0x0801 is set and either the stage type is FF1 (stagInfo ST type, `d_stage.h:1031-1033`; `d_s_play.cpp:1369-1374`; `d_a_obj_demo_barrel.cpp:101-104`; the game also does `offCollect(0,0)`) or the stage is any Forsaken Fortress stage (MajyuE, majroom, Mjtower, ma2room, ma3room, M2tower, M2ganon: save table 2) while the receiver's own MET_KORL (0x0F80) is clear. Only MajyuE is FF1-type: majroom is a dungeon and Mjtower a boss stage, so without the second part the first majroom entry would hand the world's sword (owned since Outset) back before Link finds the dropped sword in MajyuE (`d_item.cpp:877-881`), and majroom's Moblins jail only a swordless Link (`d_a_mo2.cpp:3468-3472`). Each player picks up the sword in MajyuE; it merges again from Windfall on. The stages are matched by name, since the inventory context holds no save table.
  - No bow, magic arrows or light arrows while the start stage is "GTower" (`d_s_play.cpp:1376-1384`).
- **HUD refresh.** After a slot changes, refresh `play.mSelectItem[btn]` (0x803CA7DB) for any button bound to that slot (`d_com_inf_game.h:2924-2938`). Whether Link's sword model updates on a live `mSelectEquip` change is inferred; otherwise it updates at the next stage load.
- **Bottles, bags and the delivery bag** are per player in v1.0. This is a known limit: a bottle only one player found stays theirs.
- **M4 as built** (`mods/coop/inventory`; items 99-104). `inventory.Pending` plans in the grants' order: flags, then capacities, slots, equipment, then buttons.
  - The flags (OR) and capacities (MAX) go through the merge path while IDLE. The slots, equipment and buttons go through the derive path (`ApplyDerived`), each compare-and-set against the byte the plan read.
  - *Corrected (doc vs decomp):* `item_func_bomb_bag` sets both the bomb count and the max to 30 (`d_item.cpp:792-797`), not only the max. The upgrades are `d_item.cpp:1368-1390`, and they fill the ammo to 60/99 as well (item 100).
  - Only bit 0 of `mGetItem[0..2]` and of `mCollect[4]` is taken. Their other bits are the bottle contents ever held and the Hero's Charm worn, both each player's own (items 99, 112).
  - A first bow or bomb bag arrives with no ammo. Its fill of 30 would go through the HUD's ammo counters, which are no WriteGuard target before M7, so it is logged and not written (item 100).
  - Equipment (`mSelectEquip`) changes wait for the receiver's stage-entry window: the first IDLE tick after a stage load, when Link holds nothing (item 101). This settles the open question above about a live sword change.
  - A button is refreshed only when it shows nothing, or a lesser variant of its slot's item. Buttons bound to bottles or bags are never touched (item 102).
  - World capacities are capped at the most the game sets (wallet 2, arrows and bombs 99) before the MAX. The GTower rule matches the exact name "GTower". The FF1 rule fails closed when the STAG can't be read (item 103).

### 6.7 Create-only staleness and escape hatches
- **Until M9:** chests, freestanding items, bombable walls and other create-only actors in the receiver's *current* room stay stale until a room reload. Effects of a duplicate pickup:
  - unique items are idempotent;
  - keys self-correct (§6.4);
  - heart pieces may add one extra quarter (max life is add-only; logged);
  - rupees and ammo are duplicated (documented).
- **Refresh area** reloads the current stage, point and room. **Warp to partner:**
  - same stage: the existing mailbox `warp_seq` (`mailbox_core.h`);
  - other stage: the partner's PRESENCE stage and point.
- Before M7 both are Go writes of `mNextStage` (0x803C9D48..0x803C9D55). They are inferred and best-effort, because `dComIfGp_setNextStage` (0x800537C8, `d_com_inf_game.cpp:666-690`) also records the last scene's speed and mode, and the start point when asked, which a raw write skips. So an escape's arrival replays the mode of the last stage change the game asked for: Link's create picks its entry from it (crawling in, getting up from a fall, a demo) and takes the shields' state and the soup from it (`d_a_player_main.cpp:12280-12297`, `:12444-12550`, `:12601-12616`; item 76). The start point (`mStartCode`) matters only to the volcano and ice island actors. M7 replaces both writes with the `SET_NEXT_STAGE` op. Both ship from M3 as the softlock escape.
- **M3 as built** (items 70-75): the console's `/refresh` and `/warp`. Refresh writes back the request `mNextStage` still holds (`d_s_play.cpp:1277-1278`). The other-stage warp asks for the partner's stage by its entrance point, into the room that entrance starts in, which the replica learns from arrivals and never guesses, then warps onto the partner. A same-stage warp goes only into a room whose collision this game has loaded, and only once the partner has been in its stage for a second (item 82). Both land on the partner's own position, the one pose sync has, so the two Links overlap until one walks a step. Landing beside the partner needs a check of this game's own collision at the spot first, which needs the C side, so it waits for M7 (item 132).

---

## 7. Combat sync plan

### 7.1 Facts
- **Room actors** are created by `dStage_actorInit` (0x800423C0, `d_stage.cpp:1688-1708`). It copies each ACTR entry's u16 `setID` (ACTR +0x1E; wwrando `wwlib/dzx.py`) into `fopAc_ac_c::setID` (+0x1BC). It skips any actor whose `dComIfGs_isActor(setID, roomNo)` is set. That check covers only the 0x20-byte chunks (ACTR, TGOB, TRES, ACTn, TREn). Entries of the 0x24-byte TGSC, SCOB, DOOR, TGDR and SCOn chunks keep their setID but go through `dStage_tgscInfoInit`, which never checks it (`d_stage.cpp:1712-1730`; Appendix C item 32).
- **Runtime spawns** (`fopAcM_create`/`fastCreate`) get setID 0xFFFF (`f_op_actor_mng.cpp:78-91`). Their room is whatever the caller passes (`:104`), so filter on setID, not room. They can never be synced.
- **Death record.** `fopAcM_onActor` sets zone actor bit `setID` for the actor's `home.roomNo` (`f_op_actor_mng.h:444-446`; `d_save.cpp:1671-1683`).
- **Zone lifetime.** Zones are wiped on stage entry at a start point ≥ 0 (`d_stage.cpp:124-126`) and removed two room changes away (`:236`, `:248-266`), so vanilla respawns enemies on re-entry.
- **Enemy code lives in RELs at dynamic addresses** (`f_pc_load.cpp:29`), so only DOL functions can be hooked.
- **Writing `health = 0` does not kill an enemy.** Death transitions happen inside each enemy's own hit handler (for example `d_a_bk.cpp:3615-3628`, inferred from the pattern).
- **`enemy_ice()`** (0x8001C0B4, `c_damagereaction.cpp:117-171`) is polled at the top of execute by every enemy that embeds `enemyice`. When `mLightShrinkTimer` (enemyice +6, `c_damagereaction.h:12-16`) is non-zero, it runs the generic light-arrow death:
  - about a 90-frame shrink,
  - `fopAcM_delete`,
  - `fopAcM_onActor`,
  - item ball,
  - `onSwitch(mDeathSwitch)`.

### 7.2 M6.0 (Go-only): kill union of keyed enemies plus K1 light-shrink, as built

M6 ships in two parts (item 143). **M6.0**, this section, syncs the deaths the game itself records: those of keyed placed enemies, a setID below 0x200 in a room's file. **M6.1**, a later PR, adds placement-matched kills of unkeyed enemies (setID 0xFFFF), which no game records (`d_save.cpp:1671-1674`). Until it lands, every unkeyed enemy stays each player's own: the sea, Savage Labyrinth, KATA_RM, the caves, Hyrule and the rest.

**Identity**
- Key: (stage name at 0x803C9D3C, `home.roomNo` +0x1E2, `setID` +0x1BC), with setID < 0x200. 0xFFFF and room -1 are never recorded, and a setID of 0x200-0xFFFE halts the game in `isActor`/`onActor` (Appendix C item 33).
- A key syncs only if the M1 enemy table says so (`synctables.SyncedKill`, `enemies.Table.KeyedK1`): one of its entries is a K1 class's, Keyed, not BOSS on creation (`Class.IsBoss`), not Morth, and no other enemy on a common layer shares the key (Shared). A key one of whose K1 entries is excluded does not sync: one bit stands for all of them.
- The light-shrink finds a live enemy by its `home.roomNo` and setID, which `fopAc_Create` copies from the entry before the class's own create runs (`f_op_actor.cpp:270-283`). No `home.pos` cross-check is needed for a keyed actor; the cross-checks for unkeyed ones are M6.1's.

**Detection**
- The sender diffs every live zone's dead-actor words (`replica.Differ`, as built in M2), the room being the zone's `mRoomNo`, and proposes the rises as SCOPED KILL edges, only those the verdict syncs (`hub.Filter`, as for M5's switches). An echo of a death the receiver's own writer recorded is harmless: the union is idempotent.
- *Changed:* no observation path. A Kargaroc's own death deletes it without `onActor` (`d_a_bb.cpp:1969-1972`, `:2293-2294`), so it syncs only when it dies on ice or by a Light Arrow, through `enemy_ice` (`c_damagereaction.cpp:159`, `:369`). Morth is no target at all: every placed one is a generator (item 144).

**Store** (`hub.Config.SyncedKill`, item 150)
- KILL is kept per (stage, save table, room) while any player is in that stage, relayed to the partners there, and sent to a player entering it or granted `FeatureCombat` again (`world.Scopes`, `hub.regrant`).
- The host's hub keeps only the bits its own tables sync; the rest are dropped and counted in `PeerStatus.ScopedRefused`. A nil verdict (a host without tables) keeps none: combat fails closed.

**Apply** (`replica/reconcile_m6.go`), each tick after the scoped path's writes:
1. **Zones** (`FrameWriter.ApplyKills`, the kill path). For each room with a partner's store in Link's stage and table, the bits the store has, the verdict syncs and the zone lacks are ORed into the zone of that room, one byte at a time. The room may be any loaded room, Link's or a neighbour's, hidden or shown: writing a hidden neighbour's zone is what keeps its dead enemies from being created when a door shows it (`dStage_actorInit`'s `isActor`, `d_stage.cpp:1693`). The room must be loaded and settled: `mStatus[room].mFlags` (0x803BDC88 + 0x114·room + 0x104) has 0x01 set and 0x02/0x04 clear (`d_stage.cpp:214-240`), and its zone slot and `mRoomNo` name each other (§3.1). All of this is read again after the heartbeat, before each byte (`killHolds`). The next tick verifies each byte, unless the slot moved.
2. **Live enemies** (`FrameWriter.ApplyActors`, the light-shrink), only when some store bit is a synced key of a loaded room. The receiver walks `g_fopAcTg_Queue` (`wwapi/actors`, with the K1 layouts of `mods/coop/combat`; item 152) and targets each actor whose (`home.roomNo`, setID) bit is in the **partner's store** and the verdict, and that passes every gate below. It never asks its own zone: a ReDead this game is killing records its death on every frame for about ten seconds before it goes (`d_a_rd.cpp:836-853`; item 147).
   - Each target is read again just before its write (`combat.Revalidate`): the node still queued, the same process ID, proc, setID and room, every gate, and the home room's zone. An actor freed and its block reused is held, not written. So is a carrier whose weapon changed hands since the walk: one planned with a weapon must still hold it, and one planned with none must not have taken one up (item 156).
   - The **timer first**: one byte, 1, at `enemyice.mLightShrinkTimer` (an s8 at enemyice +6, `c_damagereaction.h:16`), read back. Only a timer that reads back nonzero lets the carrier's weapon go: CARRY cleared on the BOKO the carrier holds (item 148). "Holds" is read from both sides, never from the weapon field alone, which keeps a stale process ID after the weapon is lost: the BOKO's `mNowMode` (+0x2B8, `d_a_boko.h:128`) is `Mode_ENEMY_CARRY_e`, not Link's `Mode_PLAYER_CARRY_e`, and the carrier's own record says it holds one (a Bokoblin's `m0B30` ≠ 0, a Stalfos's `m1DD0` ≥ 2). A carrier whose record says it holds a weapon the BOKO does not yet agree with waits (item 156).
   - An actor written is never written again (by process ID, which the game never reuses, `f_pc_base.cpp:36-40`), except across a save-state load, which rewinds the heap and the process counter: the receiver then forgets what it shrank (item 158).
3. **Verify** on the next tick: a light-shrunk actor still queued under its process ID must read a nonzero timer. One gone, or counting, is fine.

The receiver also keeps its copy of a store only while Link is in the game, in the store's stage and table (`pruneScoped`), as for M5's dan and zone stores: off the game (a stage load's frames with no Link, the title) the hub places it nowhere and may drop the store, and a copy kept then would be ORed into the zones a reload of the same stage wiped (item 157). The tick's plan works on its own copy of the store's words, so the WriteGuard judges by the plan, never by bits that arrived after it (item 159). An old host hub relays KILL unfiltered (Appendix C item 113); the receiver applies its own tables' verdict anyway.

**Gates** (all read again after the heartbeat)
- Global: STABLE, IDLE, HEARTBEAT and ARMED (`gate.CanWriteIdle`); the stage, table and file unchanged since the tick (`recheck`, `recheckIdle`).
- Link's `mDemo.mDemoType` (daPy_py_c +0x304, a u16, `d_a_player.h:499`, `:153`) is 0. The offset was read live in K1 (0 at idle, 2 during a stage-entry event), so `combat.DemoTypeVerified` is true (item 146). With it false the gate fails closed: every light-shrink is held, logged and counted as `link demo unverified`. Zone writes never wait for it.
- Per actor (`combat.Eligible`): a K1 class, verified (`enemies.Class.K1Verified`); `mInitState` 2 (`f_pc_executor.cpp:31`, `:49`); group 2; a setID the game takes; the `enemyice.mpActor` self-pointer; its `mMode` 1 and freeze timer 0 (not frozen, `enemy_ice` has run); the timer 0; health above 0; neither BOSS nor CARRY status; `eventInfo.mCommand` 0; a ReDead not in `MODE_ATTACK` (7, its grab) or `MODE_SW_WAIT` (0xA, which runs no `enemy_ice`, `d_a_rd.cpp:1621-1630`); a Floormaster not in its grab or Link's start demo (modes 13-17, `mode_tbl`, `d_a_fm.cpp:2306-2420`); its home room's zone exists (`onActor` asserts it, `d_save.cpp:1678-1679`); not written before.

**K1 offsets** (enemyice member + 6; from the `include/d/actor/d_a_*.h` headers)

| Class | proc | Timer offset | Weapon (process ID field) | M6.0 |
|---|---|---|---|---|
| Bokoblin `bk` | 0xBE | +0x126A | +0x1200 `m1200` (`d_a_bk.h:112`), held while `m0B30` (+0xB30, `d_a_bk.h:74`) ≠ 0 | synced; seen live (item 141) |
| Moblin `mo2` | 0xBD | +0x2A5A | none: the spear is in the model | synced; seen live (item 141) |
| Stalfos `st` | 0xBF | +0x1FAE | +0x1DCC `mHeldWeaponEntityId` (`d_a_st.h:103`), held while `m1DD0` (+0x1DD0, `d_a_st.h:104`) ≥ 2 | synced |
| ReDead `rd` | 0xE1 | +0x6E6 | — | synced |
| ChuChu `cc` | 0xCF | +0x822 | — | synced; seen live (item 141) |
| Keese `ki` | 0xD8 | +0x92A | — | synced |
| Armos Knight `am` | 0xCB | +0xAC2 | — | synced |
| Armos `am2` | 0xCC | +0xA92 | — | synced |
| Kargaroc `bb` | 0xB6 | +0xC86 | — | synced; recorded only on ice or by a Light Arrow |
| Floormaster `fm` | 0x79 | +0xAEE | — | synced |
| Morth `ks` | 0xCE | +0x6AE | — | never: every placed one is a generator |
| Darknut `tn` | 0xC0 | +0x152A | +0x1410 `mBokoPcId` (`d_a_tn.h:122`, D44J01 layout, seen live) | synced; verified live in K1 (item 141) |
| Peahat `ph` | 0xD2 | +0x99E | — | synced; verified live in K1 (item 141) |
| Miniblin `pt` | 0xF9 | +0x7EA | — | synced; verified live in K1 (item 141) |
| Poe `pw` | 0xD5 | +0x88A | — | synced; verified live in K1 (item 141) |
| Boko Baba `bo` | 0xD7 | +0xA02 | — | synced; verified live in K1 (item 141) |

The proc numbers match the GZLE01 `fpcNm` enum (`f_pc_name.h`), as M1 checked. *Corrected (item 142):* the Bokoblin prelude +0xB34 and the Moblin prelude +0xDD5 this section named do nothing once the timer is set, since their `yari_off_check` runs after `enemy_ice`'s early return (`d_a_bk.cpp:4159-4169` against `:4420`; `d_a_mo2.cpp:3380-3388` against `:3570`). The Stalfos's +0x1DCC is its weapon's process ID, not a flag.

**Exclusions**
- Bosses and mini-bosses: no boss proc is a K1 class (Phantom Ganon and Big Octo never get BOSS status, item 36), and BOSS status on creation keeps an entry out of the verdict and an actor out of the gates. A Bokoblin of type 0xA stops its mini-boss music only by its own death (`d_a_bk.cpp:2639`).
- Morth; setID 0xFFFF and stage-level actors (room -1); Shared keys. A K1 class not verified would get its zone bits and no light-shrink; since K1 every class with an enemyice is verified.
- A keyed Stalfos killed after it splits: vanilla does not record that death either (`d_a_st.cpp:1676-1684`, `:2551-2566`).

**Consequences**
- Room-clear logic asks for the first living enemy of the room (`fopAcM_myRoomSearchEnemy`, `f_op_actor_mng.cpp:1272-1294`, which returns an actor or NULL; item 151). In a room whose every enemy-group entry, of any class, is a synced key of a verified class that records its own death, B's room clears by itself once A's kills reach it, and a room-clear switch (ALLdie, `d_a_alldie.cpp:28-47`) still syncs through M5, as the owner saw (item 141). "All keyed" in the coverage counts K1 entries only, so it is not that: of the owner disc's 68 all-keyed rooms, 48 clear on B by themselves; the other 20 also hold enemies of non-K1 classes (Wizzrobes, Bubbles, OQ, Dexivines and others), a BOSS Moblin, or a Kargaroc, whose own death is never recorded (item 160). Morth, in the ENV group, never blocks a room-clear (`enemySearchJugge`, `f_op_actor_mng.cpp:1275`).
- **Known blocker for B in the 14 mixed rooms, and in any room with enemies M6.0 does not remove** (non-K1 classes, Kargarocs killed normally; owner disc): room-clear code that waits for an empty room and sets no switch (`d_a_tbox.cpp:1036`, `d_a_mdoor.cpp:277`, `d_a_shutter2.cpp:51`, `d_a_lstair.cpp:102`, `d_a_kddoor.cpp:133`, `d_a_door10.cpp:140`, `d_a_door12.cpp:162`, `d_a_mbdoor.cpp:436`, `d_a_fire.cpp:368`) stays shut on B until B kills those enemies itself, or M6.1 (unkeyed enemies) lands.
- The light-shrink runs the class's generic death: each player gets its own item drop, and the death switch is set on both (inferred). It skips each class's own bookkeeping: a Floormaster's grab (its ice code cancels it, `d_a_fm.cpp:716-722`), an Armos Knight's swallowed bomb, a Miniblin's respawn flags.
- **Known artefact:** a room's actors created before the store's bits are written (B entering the stage after A's kill) spawn and then shrink. M7's `isActor` shim removes it.
- Coverage on the owner's disc (`coop-tables`, tables schema 4, item 153): 1403 K1 entries not BOSS; 333 (23.7%) keyed: 324 sync, 9 Kargaroc only by ice or light arrow, 0 Morth never live; 1070 (76.3%) unkeyed stay per player until M6.1. Of the 237 rooms with K1 enemies, 68 are all keyed (48 of them clear on B), 14 mixed and 155 have none keyed. The developer test stages on save table 15 are left out (Appendix C item 137); the three keyed Morths were all in them.

### 7.3 Later

**M6.1 (Go)**
- Placement-matched kills of unkeyed enemies (setID 0xFFFF), while both players are in the same stage, table and room: a new SCOPED kind behind its own feature bit (so a mixed-version pair keeps everything else), a death detector over the actor walk with per-class signatures, each enabled once the owner's live check confirms it, and the same light-shrink applier (item 143).

**M7 (C)**
- The `isActor` call-site shim with stage-keyed dead tables, so partner-killed enemies never spawn. Entries of the 0x24-byte chunks are not dead-checked and still respawn (Appendix C item 32).
- KILL op = `fopAcM_delete(procID)` plus `onActor` for stage-placed classes without `enemyice` (Magtail, Wizzrobe, Octorok, Rat, Bubble, Mothula). Gyorgs are excluded: GyCtrl spawns them at runtime with setID 0xFFFF (`d_a_gy_ctrl.cpp:345`). Each class needs a live check first.
- K1 stays the default wherever it applies, because it runs the death switch and the drop.

**Post-1.0**
- A faithful per-class death via a one-frame `AT_TYPE_LIGHT_ARROW` sphere (`cCcS::Set` 0x80242D04, `dCcD_Sph::Set` 0x800ABFC0).
- Optional "shared HP" that mirrors health drops, never to ≤ 0, for allowlisted classes.
- Remote-sword visual flinch.

**Rejected:** host-simulated enemies. AI targets local player 0 everywhere, and enemy code lives in RELs.

---

## 8. Testing plan

### 8.1 Linux CI (cloud)

**Unit and property tests**
- Address arithmetic tests for every Appendix A constant.
- Class-table golden test: all 0x778 bytes classified.
- Merge laws: commutative, associative and idempotent property tests, plus MAX and OR-mask tests.
- Event-mask generator tests: no bits outside 0x00-0x41 except the register allowlist; no LocalOnly or D8 bit in the mask.

**FakeMem and FakeGame scenarios** (under `-race`): two FakeGames, a real server and two replicas, with random interleavings, duplicates, reconnects and a host restart. Assertions:
- shared bytes converge;
- the write audit shows no PER_PLAYER byte written;
- no write while a gate is closed;
- no clear ever propagates through transition storms;
- the current table's switches are deferred to a room change;
- zone writes happen only for a valid `mZoneNo` with a matching room;
- room edges are applied once and never replayed after a room change, and no timed or unsafe switch travels (M5);
- derived keys and hearts match the formulas in scripted scenarios (same key twice, door opened remotely, key spent on an open door, phantom-free door coupling: no key door ever shows its lock over a set switch to a player with no key, item 139);
- capacity is never lowered by an ordered grant.

**Tag rules.** FakeGame reproduces New Game (`dComIfGs_init` keeps `mReserve`), `card_to_memory` and `ExDataSave`. V1-V6 reject every leak case and accept real bound files.

**World file.** Crash injection: truncated tmp file, kill between write and rename, CRC damage falling back to `.1`.

**Parsers.** Fuzz targets for every 'O' sub-codec, the extended Hello, the world file, GCI/card images and Yaz0/RARC/DZR, in the style of `mods/multiplayer/net/fuzz_test.go`. Stage readers are tested on synthetic fixtures built in test code; no Nintendo data ships.

**Combat logic.** Actor walk bounds and validation on FakeMem; the K1 gate matrix; kill-union scoping.

**From M7:**
- blob invariants: T2 end ≤ mailbox, region bounds, arenaLo from `DOLPatches` merged over vanilla words (§4.5), each hook is a `bl` into T2;
- generated constants are fresh (`extract_blob.py && git diff --exit-code`);
- optional clang `powerpc-eabi` size check on `coop.c` (.text ≤ 0x800).

### 8.2 Owner machine (two-Dolphin harness)

**Bootstrap:**
```
SAVE_STATE=$(pwd)/saves/start.sav ./wwdev.exe dolphin2
./wwdev.exe mp-local --coop
```
Run `mp-local` in the background.

**Validation tools:** `coop-probe`, `coop-watch`, `actors`, `zone-dump`, `peek`, `screenshot` and `input`. Anything visual is accepted only on a screenshot pair, never on a memory readback (CLAUDE.md "Visual validation").

**Fixtures.** Go-only milestones never invalidate save states. The owner captures each of these once with Shift+F1:
- F1 `saves/start.sav` (exists).
- F2 the first room of Dragon Roost Cavern, with a small-key chest reachable, on both cards.
- F3 an enemy room with Bokoblins and ChuChus: TF_06 rooms 2-5 (ChuChu, Bokoblin, Keese, Mothula), unkeyed, which test the light-shrink through `enemy-kill` (§8.2 K1).
- F4 a heart-piece location.
- F5 a puzzle room with an un-timed crystal and a barred door.

`stage-hop` (M0, debug) can stand in for F2-F5 if dungeons load without story progress (inferred).

**Running M2 (dry run).** M2 writes nothing to the game except the binding tag, and only after a yes. The planner only logs. Owner steps for the M2 acceptance:
1. Load the file to share in Dolphin 1 and stand in game, out of menus and cutscenes. Run `mp-local --coop=new` (or `host --coop=new`) to found the world from that file.
   - The first lines list the memory cards backed up, each with its files and whether Dolphin's settings select it ("in use": Dolphin.ini, or Dolphin's default for a slot it doesn't set), then the world ID. Check that they are your cards: the card location comes from Dolphin's source, not the decomp (§5.4). A warning that no card is in use means the backup may have missed yours: set `USER_DIR_1` to your Dolphin user folder.
   - Later sessions use `--coop` (serves the only world) or `--coop=<world-id>`.
   - A second machine joins with `join <host> --coop`.
2. Answer each question with `y` and Enter, or pass `--yes` to answer them all. "Bind it?" asks to bind the file; a leaked tag gets "Scrub it?" as well.
   - Then save in game, so the card keeps the tag.
   - After the save and a reload of the file from the card, `coop-tag show` (with `WW_DOLPHIN_INDEX`) must read BOUND with the V5 line "the file has been saved and loaded (mDateIPL 0x…)". BOUND alone does not show the reload on the host's machine, and so for both files under `mp-local`: there a file bound fresh and not yet loaded from the card reads BOUND too, through the world file's pending fresh bind, with "V5 held only through a pending fresh bind". On a joiner's machine such a file reads "LEAKED, OR A PENDING FRESH BIND".
   - Until a file bound fresh is loaded from the card, the first hello of each new `mp-local`, `host` or `join` process for it is provisional, since the replica starts knowing no pending fresh binds. It says hello as leaked (V5 only), and the host answers, with no NOTICE, that the nonce is a pending fresh bind. The host logs "co-op: … joined with a tag naming pending fresh bind …, as a file bound fresh does until it is loaded from the card; told its replica the bind is pending". The replica logs "the host has nonce … as a pending fresh bind: the file is bound" and says hello again as bound, one round trip later (item 133). A `join` process kept running across a host restart says hello bound at once. A NOTICE that the tag leaked from another file and should be scrubbed means its nonce is no pending fresh bind of the host's world. A leaked tag of another world gets a NOTICE saying that it leaked or, if only V5 fails, may be a fresh bind of that world, which only that world's host can tell. The prompt to bind such a file to the session's world says the same for a V5-only failure, and that binding replaces the tag; `--yes` accepts it (item 133).
   - A copy of a file bound fresh, loaded on another Dolphin in a session of the world before the original is loaded from the card, clears the host's pending fresh bind: the copy has been loaded from the card, so it says hello bound. Dolphin's Memory Card Manager makes such a copy (as the far-behind advice suggests), and so does `dolphin2 --reset`, which copies Dolphin 1's user folder with its cards. The original then gets the leak NOTICE in its next process, and a prompt to bind it again or to scrub it. Save in game and load the file from the card before copying it (item 133).
3. Play on both Dolphins. Each log shows its dry-run plan: "would set …" and "would raise …" lines for the partner's progress, never for life, rupees or another PER_PLAYER byte. `coop-world list`, run in another terminal, shows the world's seq growing and `in use: pid …` for it, not `stale session marker` (item 136).
4. Check that ww-multiplayer wrote nothing but the tag: `coop-watch` on each Dolphin shows only the game's own edges, and `peek 0x803C532C 48` holds the tag.
5. Run the recovery drills, with the session stopped (Ctrl+C):
   - `coop-world restore` lists the card backups; `coop-world restore <backup> --yes` puts one back, and only while Dolphin is closed.
   - `coop-world block <world-id> chest <table> <no> --yes` tombstones a bit.
   - `coop-tag clear --yes` unbinds a file.
   - Restart `mp-local --coop`: the world comes back at the same seq.
6. Run host, join and mp-local once without `--coop`: they behave exactly as before, except `mp-local`'s startup shift, which now moves Dolphin B's Link only when the two Links overlap and logs why when it does not (item 130).

From M3, `--coop` writes (below); add `--coop-dry-run` to run these M2 steps as M2 ran them.

**Running M3 (writes).** `--coop` now writes the world's chests, pickups, visited rooms, dungeon items, latching memory switches, ocean and charts into each bound game. `--coop-dry-run` gives M2's dry run instead. A1-A6 are M3's acceptance items and R1-R4 the escapes'. Nothing defined them before, so they are defined here (item 79).

Before the steps:
1. Run `patch` on your vanilla ISO with this build, which records the disc for the stage tables, or pass `--coop-iso <the ISO Dolphin runs>` to `mp-local`, `host` and `join`.
   - `patch`'s `co-op :` line says whether it built the tables. It records the disc even when they can't be built.
   - Each side's first lines must include `co-op: stage tables <id>, … N latching memory switch(es) in M save table(s) sync (K of them, shared with Tingle Tuner switches, only while no Game Boy Advance is linked)` (item 138).
   - Without tables memory switches don't sync. The rest syncs. That side's log says why in one `co-op:` warning, with what to do: no disc recorded (run `patch` with this build, or pass `--coop-iso`), a recorded image or a record that can't be read (run `patch` again, or pass `--coop-iso`), or a disc this build can't make tables from (patching it again won't help; report the line, or, if the warning names a disc Dolphin doesn't run, run `patch` on the one it does or pass `--coop-iso`). The hub's NOTICE only says which side has none (item 134).
2. Start `mp-local --coop` (both Dolphins in one session), or `host --coop` and `join <host> --coop` on two machines. Bind both files (M2 steps 1-2).
   - Each pipeline logs `armed in world …: the world's chests, … are written to this game`.
   - The log explains the console once: type `/help` for the list. Under `mp-local`, `/refresh` and `/warp` take the player's name (`/warp PlayerB`).
   - Under `--coop`, `mp-local` does not move Dolphin B's Link at start and says so (item 130). Two Dolphins booted from one save state start with the Links on top of each other: walk one a few steps away before a screenshot that needs both.
3. Take every screenshot as `WW_DOLPHIN_INDEX=<n> ./wwdev.exe screenshot shots/m3-<item>-<before|after>.png`. Read each pair by eye; a memory readback alone accepts nothing.

Acceptance:
- **A1 Chests.** On F2, A opens the small-key chest while B is in another room of the dungeon.
  - B's log says `set t03 DRC chest <n>`. When B walks into the chest's room, the chest stands open.
  - Pair: B's view of the closed chest before A opens it, and the open chest after B enters.
  - A chest in B's *current* room stays closed until B leaves and comes back, or types `/refresh` (R1, §6.7).
- **A2 Pickups, visited rooms, dungeon items.** A picks up the dungeon map (or compass) and walks through two new rooms.
  - B's pause-menu map shows the map and both rooms after B's next room change.
  - When A beats the boss, B's log says `holding … back: the boss stage reads them every frame` while B is in the boss room itself, and the bits are written once B leaves it (item 85).
  - Pair: B's map screen before and after.
- **A3 Latching switches, deferred for the current table.** On F5, A lights the un-timed crystal while B stands in that room.
  - Nothing changes in B's room. The log says `… waits for the next room or stage change`, then `set … switch 0x..` about 2 s after B leaves, once the actors of B's new room have run (the room hold, item 126).
  - When B comes back, the barred door stays open. Pair: B at the barred door before, and after the room change.
  - A push block or a timed switch must not sync (M1's verdict, §6.2).
  - With `--coop-live-switches` on the host, the switch applies once B is IDLE (no menu, no event) and the actors of every room B's game shows have run for 2 s. In a room B has been in for a while, that is at once.
- **A4 Ocean and charts.** In a sea square B hasn't charted, A throws All-Purpose Bait to the fishman, who charts the square. A also salvages a chart.
  - After B's next room change, B's sea chart shows the square charted and the chart owned.
  - Sailing into a square is not enough. It sets only the square's arrive bit for the Tingle Tuner (`mFmapBits` bit 1: `dMap_c::setArriveInfo`, `d_map.cpp:1937-1948`; `onSaveArriveGridForAgb`, `d_save.cpp:954-957`), which the sea chart does not draw. The chart draws bit 0 (`displayinit`, `d_menu_fmap.cpp:725-729`), and only the fishman sets it. Fed, he jumps to the boat and opens the sea chart in its fishman mode (`cutJumpMapopenProc`, `dComIfGp_fmapOpenFishOn`, `d_a_npc_so_cut.inc:468`; `d_menu_window.cpp:903-907`; that feeding him runs this cut is the event data's, inferred). That mode marks the square Link was in as the chart opened (`onSaveArriveGrid`, `d_menu_fmap.cpp:3271`; the square, `initialize`, `:622-624`, `:640-642`).
  - Pair: B's sea chart before and after.
- **A5 Nothing else is written, and nothing while a gate is closed.**
  - `coop-watch` on B shows the partner's progress arriving only as edges in chests, items, visited rooms, dungeon items, switches, the ocean and charts.
  - B keeps its own life, rupees, magic, inventory and story bits: `peek 0x803C4C08 4` before and after.
  - Hold B's pause menu open while A makes progress in the save table B has loaded (A in B's dungeon, or both on the sea; a sea chest that records in table 1, logged `t01 sea2 chest …`, is a cold table's: see the next bullet): none of it applies until the menu closes. A chest, pickup, visited room or dungeon item logs nothing while it waits; its `set …` line comes once the menu closes. A memory switch, the ocean and the charts also wait for B's next room change (`… waits for the next room or stage change`). After the change they wait for B to be STABLE and IDLE (a switch also for the room hold, A3), so a menu opened right after the change holds them until it closes (item 129). Those that arrive between B's room change and that change's first STABLE and IDLE tick (the menu opened within about 0.4 s of the change, or the door's event still running) wait only for the menu or the event to end, and log `… waits for this game to be idle (the pause menu is open): its room change is in` (`an event is running` during a cutscene). They are written as it ends, with no other room change. Once that tick has passed, with or without a write in it, progress that arrives waits for B's next room change again. A memory switch that arrives while the room hold still runs logs the hold's line instead (A3). One write planned on the tick before the menu opens can still land as it opens (item 128). The same holds during a cutscene. During a loading zone, and on the tick a door or a cutscene starts a stage change, only the world's story bits and event registers are written (`a stage change starts: writing …`, §6.5); nothing else is written until the new stage settles.
  - Progress in a save table B has not loaded (A in a dungeon B is not in, or a sea chest that records in table 1 while B is on the sea) goes to that table's saved slot as soon as B is STABLE, with the menu open or not, in a cutscene or not. B logs `set tNN … (0x… |= …)`, such as `set t01 sea2 chest …`, with no `and its live copy`. That is by design (§6.3, item 128). Check it when B next enters that dungeon (A2), or, for a sea chest, when B next comes into its room: the chest reads table 1's saved slot as it is created (`checkOpen`, `d_a_tbox.cpp:315-316`, from `CreateInit`, `:454`; `dComIfGs_isStageTbox`, `d_com_inf_game.cpp:705-713`).
- **A6 The off switches.**
  - Type `/coop off` (with B's name under `mp-local`). B's log says `co-op writes are off for <B>: you typed /coop off. Pose sync goes on; type /coop on to write the world again.`
  - A's progress no longer reaches B's game, while the mini-Link keeps moving: a screenshot pair shows it in two places.
  - `/coop on` resumes, and B catches up at its next room change. `/status` says where co-op is.
  - Then load save states on B five times within a minute. B turns its writes off once, saying `… save-state loads in …`, and pose sync goes on.

Escapes (§6.7):
- **R1 `/refresh`.** Stand in a room of B's with a chest A opened after B came in, and type `/refresh` for B.
  - The room reloads by the entrance B came in by, and the chest stands open. Pair: before and after.
  - After a void-out (Link restarted at point -1, `dStage_restartRoom`) or a turn restart (point -3, `dStage_turnRestart`, `dStage_escapeRestart`; `d_stage.cpp:2368-2404`), `/refresh` says to go through a door first and writes nothing.
  - After a death and Continue the game asked for the file's return place, an entrance (`d_a_player_dproc.inc:1091-1094`, `d_com_inf_game.cpp:1397-1406`), so `/refresh` reloads it.
- **R2 `/warp`, same stage.** Put A and B in different rooms of DRC and type `/warp` for B.
  - B's Link lands on A's own position, so the two Links overlap until one walks a step. The log says `warping to the partner at (x, y, z) in room r`. Walk B a few steps aside (about a body's width) before the shot after the warp, so that it shows both (item 132). Pair: B's view before and after.
  - If A's room isn't loaded in B's game, B first reloads the stage by A's entrance, then warps.
  - Right after A comes into the stage, `/warp` says `not now: the partner has only just come into …` for a second: pose sync may still have A's last stage's position (item 82).
- **R3 `/warp`, another stage.** A goes through a door on Outset while B stays in DRC. B must see A arrive, so start the session first: the room an entrance starts in is learned from arrivals (item 73). Then type `/warp` for B.
  - The log says `going to <stage> by point <p> into room <r>, layer -1, the partner's entrance …`.
  - B loads A's stage, then warps onto A's own position, as in R2: the two Links overlap until one walks a step, so walk B a few steps aside before the shot after the warp. Pairs: after the stage change, and after the warp.
  - If the entrance's room isn't known yet, the warp says so and writes nothing.
- **R4 Refusals.** With the pause menu open, or during a cutscene, `/refresh` and `/warp` say `not now: … try again in a moment` and write nothing. `peek 0x803C9D48 14` is unchanged.

Last: run `host`, `join` and `mp-local` once without `--coop` and once with `--coop --coop-dry-run`. Without `--coop` they behave exactly as before, except `mp-local`'s startup shift, which now moves Dolphin B's Link only when the two Links overlap and logs why when it does not (item 130). With `--coop --coop-dry-run` the M2 steps hold.

**Running M4 (shared progression).** `--coop` now also writes the world's story bits and allowlisted event registers (at the receiver's next stage change), its items, songs and capacities (while IDLE), and the small keys, max hearts and max magic each game's own flags then give. `--coop-dry-run` is still M2's dry run (item 106). P1-P9 are M4's acceptance items.

Before the steps:
1. Have the stage tables loaded as for M3 (M3 step 1). Each side's first lines then include:
   - `co-op: shared progression this session: story bits and event registers (written at stage changes), items, songs and capacities (when idle) and max magic; derived from the stage tables: the small keys of DRC, FW, TotG, ET, WT and max hearts.`
   - A `co-op: the small keys of table <t> (<dungeon>) are not derived: …` or `co-op: max hearts are not derived: …` warning for each derive whose M1 check fails. Without tables the line ends `without co-op stage tables no small keys or max hearts are derived.`
2. Start from one file on both cards, so both bind into one world as subsets (§5.3). Export the file to share from Dolphin 1's card with Dolphin's Memory Card Manager and import it into Dolphin 2's card, before either is bound (`coop-tag clear --yes` first if it ever was). P1-P2 need DRC open (F2), P3 needs F4, and P4-P6 need a file that can still learn a song, open the bow's chest and see a story cutscene. Save states of those spots taken after binding stay bound.
3. Start `mp-local --coop` and bind both files (M2 steps 1-2).
   - Each pipeline logs `armed in world …: written to this game: the world's chests, pickups, rooms, dungeon items, ocean and charts; its latching switches (at room changes); its story bits and event registers (at stage changes); its items, songs and capacities (when idle); the small keys, max hearts and max magic this game's own flags give`.
   - A class the host does not grant is named once: `co-op: the world's small keys and max hearts are not written to this game this session: …`, with the reason (the stage tables differ, see the host's NOTICE, or the host turned it off).
4. `/status` (with B's name) now adds `progression granted: story|inventory|keys|life|magic`, `story bits held back: none` (or the held bits and why), a `small keys of table <t> (<dungeon>): …` line per key table, and `max hearts: derived`.
5. Take each screenshot as `WW_DOLPHIN_INDEX=<n> ./wwdev.exe screenshot shots/m4-<item>-<before|after>.png` and read each pair by eye. A readback alone accepts nothing visual.

Acceptance:
- **P1 A chest key shows on B's key HUD.** On F2, B stands in DRC, out of the chest's room. A opens the small-key chest.
  - B's log says `set t03 DRC chest <n>`. Once B is IDLE and settled, it says `the derive path wrote 1 of 1 writes after heartbeat …`, then `wrote key counter +1 at 0x803CA77C: small keys, table 3: 0 -> 1: 1 of 4 source(s) taken, 0 of 4 door(s) open`.
  - B's key HUD counts up to 1 (`dMeter_keyMove`, `d_meter.cpp:5242-5257`). `peek 0x803C53A0 1` reads 1 and `/status` says `small keys of table 3 (DRC): 1 key(s), 1 of 4 source(s) taken, 0 of 4 door(s) open`.
  - Pair: B's HUD before and after.
  - Duplicate: had B stood in the chest's room, the chest would have stayed closed for B (§6.7). B opening it too counts up to 2, and the derive path takes the count back to 1 (item 94).
- **P2 A door A opens takes B's key.** Both hold that key. A unlocks a DRC key door while B is in another room.
  - B's count stays 1 while B stays in its room: the door's switch waits for B's next room change (§6.3), and the key with it.
  - About 2 s after B's room change (the room hold) the log says `set t03 DRC switch 0x..`, then `small keys, table 3 (DRC): door 0x.. is counted after your next room change: its switch is written, but the door shows its lock until then, and its key stays`. B's count stays 1, and `/status` says `small keys of table 3 (DRC): 1 key(s), 1 of 4 source(s) taken, 1 of 4 door(s) open (door 0x.. counted after your next room change: its lock stays until then)`. A key door reads its switch only as Link changes room (§6.1), so the door may still show its lock if B walks to it now: it offers "Open". From the door's front room opening it spends B's key (`keyInit`, `d_door.cpp:435`), and as B goes through the log says `door 0x.. counted: your room changed, …`, with no derive write after it. `this game spent a key on it` shows only if the count settles before a room change, as on a door whose two rooms are one. From its back room the game spends no key, and the door is counted at the room change through it, as below.
  - At B's next room change the log says `door 0x.. counted: your room changed, …`, then, once settled, `wrote key counter -1 … 1 -> 0: 1 of 4 source(s) taken, 1 of 4 door(s) open`.
  - Pairs: B's HUD before and after that second room change, and the door B reaches after it standing open with no lock.
  - *Corrected (item 139):* this expected the key to go at B's first room change, with the switch. On the owner's run (F13) the key went then, and the door B walked to still showed its lock and offered no "Open" until `/refresh` made it anew.
- **P3 A heart piece.** On F4, A takes the heart piece.
  - B's log shows the piece's flag set, then `wrote max-life counter +1 at 0x803CA77E: max life: <n> -> <n+1> quarter hearts: …`. `peek 0x803C4C08 2` on B goes up by 1.
  - B's hearts refill to the new maximum's whole hearts: that is the game's own meter (`d_meter.cpp:1426-1427`), not a co-op write.
  - Pair: B's quest-status screen (pause menu) piece count before and after.
- **P4 A song.** A learns a song (`item_func_tact_song1..6`, `d_item.cpp:1090-1117`).
  - Once B is IDLE, B's log says `set mTact bit <n>`. `peek 0x803C4CC5 1` on B has the bit.
  - Pair: B's collection screen (the songs) before and after.
- **P5 The bow, with arrows.** A opens the bow's chest.
  - Once B is IDLE, B's log says `set mGetItem bit 0 (0x803C4C65 |= 0x01)`, and the arrow max is raised to 30 (`peek 0x803C4C77 1` reads 0x1E). Then the derive path writes the bow into slot 12 (`peek 0x803C4C50 1` reads 0x27).
  - B's arrows stay as they were, 0 for a first bow, and the log says `not written: … (the HUD's ammo counters are no WriteGuard target before M7)` (item 100).
  - Pairs: B's item screen before and after, and B firing an arrow once it has picked some up.
  - If B already has a quiver upgrade, its 60 or 99 max stays: a capacity is never lowered.
- **P6 A story cutscene's bits arrive at B's next stage change.** A watches a story cutscene while B stays in one stage.
  - A's bits leave together, one batch about a second after A's game is IDLE again. `coop-watch` on A shows them set during the cutscene.
  - Nothing reaches B's game while B stays in its stage. At B's next loading zone, B's log says `a stage change starts: writing N story bits and M registers of the world`.
  - `coop-watch` on B shows the bits set on that tick only.
  - Pick a cutscene whose bits change a stage's layer (`getLayerNo`, `d_com_inf_game.cpp:185-269`; for example the Outset sea room reads 0x0101, 0x0E20 and 0x0520, `:193-199`). Pair: B's view of that stage before and after the stage change.
  - A resume milestone, or a boat bit, that B's game isn't ready for is listed by `/status` as `story bits held back until this game catches up: event 0x0F80 (moves where the file resumes, to Windfall)` and is not written.
- **P7 (optional) Max magic.** A gets the Deku Leaf. B's log says `wrote max-magic counter +16 …`.
  - B's meter appears, empty: the meter clamps magic to its maximum but does not fill it (`d_meter.cpp:4105-4107`), and the leaf's own fill is A's (item 97).
  - Pair: B's HUD before and after.
- **P8 save-diff after saving both files.** Stop moving, let both logs go quiet, take both through a loading zone, then save in game on both Dolphins.
  - Export each file with the Memory Card Manager. `save-diff a.gci b.gci` (or the cards' raw images, read only) prints the differences by class.
  - Expect PER_PLAYER (life, rupees, magic, ammo, bottles, the place) and TAG (each file's own tag).
  - OR may differ only by bits a game hasn't taken yet: the stale room's. The bottle contents above bit 0 of `mGetItem[0..2]` and the Hero's Charm worn are listed under PER_PLAYER (item 112).
  - OR_MASKED may differ only by LocalOnly and held story bits and unsynced switches. DERIVED may differ by a game's own `extra` keys or pieces. Nothing else.
- **P9 Far behind and divergent files.** Join with a fresh file (one that hasn't met the King of Red Lions) into a world that has.
  - B's log says `co-op: the world is far ahead of this file: copy the host's save in, or the host can run with --coop-merge to union them; pose sync goes on`, that the copy replaces all three of B's files with the host's, and that the host should first save in game and load its file from the card (item 133). Nothing is asked, bound or written.
  - Join again with `--coop --coop-dry-run` and the same file: the host's NOTICE still says the world is far ahead (item 111), and a bind asked for is refused.
  - With `--coop-merge` on the host, the bind is asked. After a yes, the milestones B lacks stay held (`/status`), so B's file still resumes where its own progress says.

Last: run P1 again with `--coop --coop-dry-run`. B's log says `dry-run: would set t03 DRC chest <n> …`, derives no key (a dry run loads no tables), and `peek 0x803C53A0 1` is unchanged. Run `host`, `join` and `mp-local` once without `--coop`: they behave exactly as before, except `mp-local`'s startup shift, which now moves Dolphin B's Link only when the two Links overlap and logs why when it does not (item 130).

**Running M5 (live room puzzles).** `--coop` now also writes a partner's latching dan switches (0x80-0xBF) while both players are in the same stage and save table, its latching zone switches (0xC0-0xDF) while both are in the same room, and its room switches (0xE0-0xEF) as edges while both are in the room. Each switch is written once the room's actors have run for 2 s (the room hold, item 121). `--coop-dry-run` is still M2's dry run, with no room puzzles (item 119). Z1-Z6 are M5's acceptance items. Nothing defined them before, so they are defined here (item 120).

Before the steps:
1. Have the stage tables loaded as for M3 (M3 step 1). Each side's first lines then include:
   - `co-op: room puzzles this session: a partner's latching dan switches (<n> in <t> save table(s)) sync while both are in the same stage, its zone switches (<n>) while both are in the same room, and its room switches (<n>) as it sets them, in <r> room(s); timed and other switches that clear never travel.`
   - Without tables, the warning `co-op: room puzzles are off this session: …` instead.
2. Pick the rooms with `coop-tables <iso> --section switches`. Each dungeon lists its switches by store, stage, room and number (`zone M_NewD2 r1 0xC3`, `dan t3 0x85`). A line marked `ok` syncs and names its setters (`set by …`); `ok*` syncs only while no GBA is linked, and names the Tingle Tuner switch it shares (item 138); an unmarked line says why it doesn't. The tests need:
   - Z1: a crystal switch (`SWHIT0`) marked `ok` in a room with a shutter or barred door: F5.
   - Z2: a room whose enemies, once all are dead, set a switch marked `ok`: the `ALLDIE` tag, which sets its switch 65 frames after the room's last enemy is gone (`daAlldie_c::actionCheck`/`actionTimer`, `d_a_alldie.cpp:28-47`). F3 if its switch is marked `ok`.
   - Z3: a timed crystal, which the list leaves unmarked: its writer clears the switch when its timer runs out (`daSwhit0_c::actionOnTimer`, `d_a_swhit0.cpp:346-362`).
   - Each switch must be a dan or zone switch (0x80-0xDF). A memory switch below 0x80 is M3's, written at B's next room change (§6.3).
3. Start `mp-local --coop` with both files bound (M2 steps 1-2; M4 step 2 for one file on both cards).
   - Once the host grants the room puzzles, each pipeline logs `co-op: room puzzles: written to this game when idle: the partner's latching dan switches (same stage and save table) and zone switches (same room), and its room switches as it sets them, while both are in the room`.
   - Otherwise `co-op: a partner's dan, zone and room switches are not written to this game: …` says why: the two sides' stage tables differ (the host's NOTICE says how), or the host turned it off.
4. `/status` adds a line per player: `room puzzles: on; Link is in <stage> (table <t>, room <r>), zone slot <z>; <n> partner switch(es) set, <n> refused as not latching here, <n> room edge(s) dropped`.
   - On the host it also prints `co-op: host: room puzzles allowed; scoped stores held: <n> dan, <n> zone, <n> kill`, then a line for each player: `<name> (<state>): in <place>; puzzles granted; <n> scoped bit(s) refused by the host's tables`.
   - With equal tables both refusal counts stay 0. A joiner on another machine sees only its own lines.
   - With tables that share switches with the Tingle Tuner (the owner's disc does), `/status` also says `Tingle Tuner: no Game Boy Advance linked; the <n> switch(es) shared with Tingle Tuner switches sync`. With a GBA linked in Dolphin it says they are neither proposed nor written, and the log said so once (item 138).
5. Take each screenshot as `WW_DOLPHIN_INDEX=<n> ./wwdev.exe screenshot shots/m5-<item>-<before|after>.png` and read each pair by eye. A readback alone accepts nothing visual.

Acceptance:
- **Z1 A latching crystal opens the shutter for both.** On F5 both players stand in the room, its shutter closed, and B has been in the room for more than 2 s. A hits the crystal.
  - B's log says `co-op: room puzzles: wrote 1 switch bytes after heartbeat …`, then `set the partner's zone switch 0x.. in room <r> (zone slot <z>) (0x… |= 0x..)`, or `dan switch 0x..` for a dan crystal.
  - B's shutter plays its opening with no input on B. The shutter polls its switch every frame and starts its open event when it changes (`daShutter_c::demo`, `d_a_shutter.cpp:233-243`).
  - `zone-dump` on B shows the bit in the slot `/status` names, which need not be A's slot. `/status` counts 1 more partner switch set.
  - Pairs: B's view of the shutter before and after, and A's.
- **Z2 A cleared room opens its shutter for both.** On F3 both players stand in the room. A kills the room's last enemy.
  - Once A's ALLDIE sets its switch, 65 frames after the last kill, B's log sets it too, and B's shutter opens, though B's own enemies may still stand: kills are M6's.
  - Pairs: B's shutter before and after.
- **Z3 A timed switch does not sync.** Both stand in the timed crystal's room. A hits it.
  - Nothing is logged on B, `zone-dump` on B doesn't show the bit, and B's door stays shut while A's opens and then closes again when the timer runs out. A's sender never proposes the bit, so the host's refusal count stays 0 too.
  - Pairs: B's door before and after A's hit, and A's door at the same moment.
- **Z4 Players in different rooms see nothing until they enter.** B waits in another room of the same dungeon stage. A hits Z1's crystal (reload F5 first, or use another `ok` crystal).
  - B's log stays quiet and B's `zone-dump` doesn't change. `/status` on the host shows both players' rooms.
  - B walks into A's room. B's log sets A's switch once B's zone for the room exists, B is IDLE, and the room's actors have run for 2 s since the door showed the room (the room hold, item 121). The shutter then opens, up to about 2 s after B arrives. It never stands open on arrival: B's shutter is created as the door shows the room, before the switch can be written, and a switch set in its first 30 frames would be lost (§6.1). *Corrected (doc vs decomp, item 121):* this said the shutter stands open or opens as B arrives.
  - Do the same for a dan switch with B in another stage of the dungeon (its boss stage, or the sea): nothing reaches B until B enters A's stage, though the game keeps dan switches per save table (item 114). If A leaves the stage before B is IDLE there, nothing reaches B (item 122).
  - Pairs: B's view of the shutter on arrival, and one from before B left its room.
- **Z5 (if a room has one) A room switch arrives once and is not replayed.** Find a room switch (0xE0-0xEF) marked `ok`. Both stand in that room, and A sets it.
  - B's log says `set the partner's room switch 0xE. …` once.
  - B leaves and comes back. B's game clears room switches on every room change (`clearRoomSwitch`, `d_save.cpp:1273-1276`), and nothing is written again: the edge is never stored.
  - Pairs: B's view after A sets it, and after B comes back.
- **Z6 A storm writes no wrong slot.** Both in Z1's room. B runs through a loading zone and back, and between rooms, several times while A hits switches.
  - `/status` on B keeps `0 verify failures`, and no co-op line warns. `zone-dump` on B shows co-op's bits only in the slot whose room is B's room at the time.
  - B's switches that its own game reset stay reset: a zone wiped at a stage entry, or by moving two rooms away, is not refilled from A (item 117).

Last: run Z1 again with `--coop --coop-dry-run`. B's `/status` says `room puzzles: none in a dry run, …`, B's log sets nothing, and `zone-dump` on B is unchanged. Run `host`, `join` and `mp-local` once without `--coop`: they behave exactly as before, except `mp-local`'s startup shift, which now moves Dolphin B's Link only when the two Links overlap and logs why when it does not (item 130).

**Running M6.0 (kill union).** `--coop` now also writes a partner's recorded deaths of keyed K1 enemies into this game's zones while either player is in the stage, and light-shrinks the live enemy (§7.2). Unkeyed enemies stay each player's own until M6.1. `--coop-dry-run` is still M2's dry run, with no combat. K1-K10 are M6.0's acceptance items (item 154). K1's offsets and Link's demo offset passed on the owner's machine on 2026-10-15 (below, item 141), so this build light-shrinks every K1 class and K2-K10 can run.

Before the steps:
1. Have the stage tables loaded as for M3 (M3 step 1). Each side's first lines then include:
   - `co-op: combat this session: a partner's kills of keyed enemies (<k> in <s> stage(s), K1 classes, no bosses) are recorded in this game and light-shrink the live enemy while both are in the same stage; <u> unkeyed enemies (the sea, Savage Labyrinth, KATA_RM, caves, Hyrule and the rest) are not shared yet: each player fights their own.`
   - No `co-op: combat: … waits for the live check …` line: it shows only while Link's demo offset or a class is unverified, and since K1 none is.
   - Without tables, the warning `co-op: combat is off this session: …` instead.
   - `coop-tables <iso>` shows `INFO  M6 kill coverage  K1 classes not BOSS: 1403 entries; 333 (23.7%) keyed: 324 sync, …`, and `--section enemies` ends with the coverage by class and by stage.
2. Pick the rooms. F3 is TF_06 rooms 2-5 (ChuChu, Bokoblin, Keese, Mothula). They are unkeyed, so they test only the light-shrink itself, through `enemy-kill` (K1), never the kill union. For the union, use keyed rooms of `M_Dai`, `kaze` and `kindan` (the stages with the most keyed entries, `coop-tables --section enemies`), and for the classes' live checks:

| Class | Keyed rooms |
|---|---|
| Darknut (TN) | kaze r8, M_Dai r17 |
| Peahat (PH) | Cave08 r2, kaze r2 |
| Miniblin (PT) | GanonN r0 |
| Poe (PW) | M_Dai r13 / r7 |
| Boko Baba (BO) | kindan r3 / r14 |
| Kargaroc (BB) | I_SubAN r9 |
| keyed Bokoblin and ChuChu | I_SubAN r11 |

3. Start `mp-local --coop` with both files bound (M2 steps 1-2; M4 step 2 for one file on both cards).
   - Once the host grants combat, each pipeline logs `co-op: combat: written to this game when idle: the partner's kills of keyed enemies (into each loaded room's zone, so they stay dead), and a live one of them light-shrinks; unkeyed enemies stay each player's own`. Otherwise `co-op: a partner's kills are not written to this game: …` says why.
4. `/status` adds a line per player: `combat: on; <n> partner kill bit(s) held for <stage> (table <t>); <n> written to zones, <n> light-shrunk, <n> held (<last reason>, link demo <n>), <n> refused, <n> verify failures`. On the host it also prints `co-op: host: combat allowed; <n> kill store(s) held` and each player's grant.
5. Take each screenshot as `WW_DOLPHIN_INDEX=<n> ./wwdev.exe screenshot shots/m6-<item>-<before|after>.png` and read each pair by eye. A readback alone accepts nothing visual.

Acceptance:
- **K1 Offsets, class behaviour and Link's demo (single Dolphin). Done on the owner's machine, 2026-10-15 (item 141; screenshots in `shots/`).**
  - One byte, 0x01, at each class's timer offset light-shrank and deleted it, and the game did not halt:
    - ChuChu +0x822 (TF_06 r2); a u16 write of 0x0001 there did nothing, as the timer is an s8;
    - Miniblin +0x7EA (SubD71 r0): the timer ran to 49, then it was deleted and dropped a rupee;
    - Poe +0x88A (SubD71 r1): the timer ran to 86, then it burst and dropped a heart;
    - Boko Baba +0xA02 (kindan r1): the timer ran to 89, then it was deleted;
    - Peahat +0x99E (kaze r2, keyed set 2): deleted, and its zone recorded actor 2 dead (`enemy_ice`'s `onActor`);
    - Bokoblin +0x126A (TF_06 r3, three of them);
    - Moblin +0x2A5A (`M_Dai` r1, keyed 0-2): the zone recorded 0, 1 and 2 dead, then the room's ALLdie set memory switch 0x03.
  - Darknut +0x152A: with raw writes its sword (proc 0x1D5, BOKO) stayed floating. With this build's `enemy-kill 2917 6 --unverified --yes` on a `kaze` r8 Darknut (keyed set 0), the timer climbed to 91, the actor was gone, `room 8's zone slot 0 records setID 0 dead: true`, and its sword's CARRY was cleared and the sword fell to the floor (`shots/m6-tn-after.png`). So its `mBokoPcId` (+0x1410) is verified too.
  - `peek-demo`: Link's `mDemo.mDemoType` (+0x304) read 0 at idle, 2 during a stage-entry event (Cave02 to `M_NewD2`) and 0 again after it. The offset is verified.
  - So `K1Verified` is set for TN, PH, PT, PW and BO (`wwapi/tables/enemies/classes.go`), the Darknut's weapon field is verified (`actors`), and `actors.DemoTypeVerified` is true.
  - `enemy-kill` and `peek-demo` first printed `Unknown command`: main's dispatch did not list them. It does now, and `TestCoopDiagDispatched` checks that main sends every co-op diagnostic to `runCoopDiag`.
  - Still open for the owner, under K7: `peek-demo 60` reading nonzero during a Floormaster's grab, a ReDead's grab and the Forsaken Fortress Moblin's holdup (`d_a_mo2.cpp:2829-2830`). Also worth watching in K2-K10: a Peahat flying, a Miniblin that respawns (the respawn must be the game's own, `pt.rel` .text 0x3F08-0x3F40), a Poe invisible and possessing, and a Boko Baba of type 0 only (children never run the timer).
  - While at it, `coop-watch` during each class's normal kills records its death signature, M6.1's input.
- **K2 A keyed kill in the same room.** Both in a keyed room (I_SubAN r11). A kills a keyed Bokoblin.
  - B's log says `co-op: combat: wrote … byte(s) after heartbeat …`, `set the partner's dead setID <n> in room <r> (zone slot <z>) …` and `light-shrunk the partner's kill: actor …, its weapon let go`. B's Bokoblin shrinks, and its spear lies on the ground.
  - Pairs: B's Bokoblin before, mid-shrink and after; the spear.
  - On B, knock another keyed Bokoblin's spear out of its hands and pick the spear up; then A kills that Bokoblin. B's Bokoblin shrinks, B's log says `light-shrunk the partner's kill: actor …` without `its weapon let go`, and the spear stays in B's Link's hands (item 156). Pair: B holding the spear before and after.
- **K3 A hidden neighbour.** B stands in a room next to A's, the door between them closed. A kills a keyed enemy of its own room.
  - `zone-dump` on B shows the bit in the slot of A's room. When B opens the door, the enemy is not there (or, if B's game created it before the bit came, it spawns and shrinks: record which).
- **K4 Another stage, then entry.** B at sea while A kills in a dungeon: nothing on B. B enters the dungeon: the hub sends the store, the bit is written, and the enemy spawns and shrinks (§7.2's artefact).
- **K5 Unkeyed enemies stay each player's own.** A kills an unkeyed enemy (TF_06): nothing on B, and the host's refusal count stays 0.
- **K6 Bosses and BOSS status untouched.** A kills a mini-boss Bokoblin (type 0xA) or a boss: B's stays, and no KILL carries it.
- **K7 Holds.** With B's pause menu open, during B's NPC talk, and while a Floormaster or ReDead holds B's Link, A's kill is held (`/status` names the reason), then applied once it ends. B's Link is never stuck. Run `peek-demo 60` on B through the grabs (and the Forsaken Fortress Moblin's holdup): it must read nonzero while they hold Link, the case K1 did not see.
- **K8 Kargaroc.** A kills a keyed Kargaroc normally: nothing on B. A kills one with a Light Arrow or on ice: B's shrinks.
- **K9 A room that clears on B.** A clears every enemy of `M_Dai` r1 (three Moblins), r7 (two Poes and two Moblins) or r8 (three ReDeads), the owner disc's only rooms with an ALLdie whose every enemy M6.0 removes on B (item 160): B's ALLDIE fires on its own (its room is empty), and the switch also arrives through M5 (Z2). K1 already saw r1's half of it on one Dolphin: the three Moblins light-shrunk, then the ALLdie set memory switch 0x03.
- **K10 A storm.** B runs through doors and loading zones while A kills: `/status` keeps `0 verify failures`, `zone-dump` shows co-op's bits only in the slots of the rooms they belong to, and the game never halts.

Last: run K2 again with `--coop --coop-dry-run`: B's `/status` says `combat: none in a dry run, …` and nothing is written. Run `host`, `join` and `mp-local` once without `--coop`: they behave exactly as before, except `mp-local`'s startup shift (item 130).

### 8.3 Day-0 read-only probes (runnable now with today's binary)
```
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C9D3C 12            # stage name ("sea\0..."), point s16, room, layer
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C53A4 4             # byte0 = save table (0 at sea; on the title it is sea_T's table; 0xFF only between dSv_info_c::init and the next stage load)
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C9EA0 4             # [mOrderCount][..][mMode][..]; mMode != 0 while talking
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C9D54 4             # byte0 = mNextStage.mEnable (1 briefly at a loading zone)
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C4C08 4             # maxLife u16 | life u16 in quarter hearts (0x000C000C new file)
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C5380 36            # live memBit of the current table
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C532C 48            # dSv_reserve_c: expect all zero on a vanilla file
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C5E98 4             # byte0 = mDataNum (file slot)
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x80372028 12            # actor queue head, tail, count
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x80412F00 4             # spawn_trigger; run twice, must advance
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803F773C 4             # JKRHeap::mCodeEnd: just above 0x80414000 on the patched ISO, about 0x80414040 (inferred). __OSArenaLo itself is moved to the arena top by JKRHeap::initArena
```

**Optional reversible HUD test, at sea only:**
1. `poke-u32 0x803CA77C 0x00000004` writes key count 0 and max life +4. Take a screenshot: 3 hearts become 4, refilled (`d_meter.cpp:1426-1427`).
2. `poke-u32 0x803CA77C 0x0000FFFC` brings it back to 3.

---

## 9. Milestones

**Where things are built**
- **cloud** = this Linux container: Go code and CI.
- **owner** = the Windows machine with Dolphin: live acceptance, devkitPPC builds, Shift+F1 recaptures, the user's ISO.

**Release map** (updated 2026-10-17: v0.4.0 shipped M0-M6.0 together, with live writes; the original plan spread them over v0.4.0-v0.8.0)

| M | Ships as | Needs C | Built where |
|---|---|---|---|
| M0 Foundations and live probes | v0.4.0 (shipped) | no | cloud; probes on owner |
| M1 Stage-data tables from the user's ISO | v0.4.0 (shipped) | no | cloud; ISO run on owner |
| M2 Binding, backups, protocol v2 (dry run) | v0.4.0 (shipped) | no | cloud; owner acceptance |
| M3 Shared world flags | v0.4.0 (shipped) | no | cloud; owner acceptance |
| M4 Shared progression and save merge | v0.4.0 (shipped) | no | cloud; owner acceptance |
| M5 Live room puzzles | v0.4.0 (shipped) | no | cloud; owner acceptance |
| M6.0 Combat sync v1: keyed kills and the K1 light-shrink | v0.4.0 (shipped) | no | cloud; owner acceptance |
| M6.1 Placement-matched kills of unkeyed enemies | after v0.4.0 (planned) | no | cloud; owner acceptance |
| M7 C co-op pump | before v1.0 (planned) | **yes** | C build, recapture and acceptance on owner; Go and invariants in cloud |
| M8 v1.0 release hardening | v1.0.0 | no | both |
| M9 (post-1.0) Create-only refresh, chest-claim arbitration | v1.1 | yes | owner |

**Dependencies**
- M0 → M1 → M2 → M3 → M4.
- M5 needs M1 and M3. M6 needs M1 and M3.
- M7 needs M3 plus finish-plan D2. M8 needs everything.
- M0 and M1 can run in parallel with finish-plan milestone C.
- C5 (#9, stage-mismatch hide) should reuse M0's address table and PRESENCE.

The structured milestone list gives scope and acceptance for each one. The summaries follow.

- **M0.** Mem, FakeMem and FakeGame; the cited address table; the class-table golden test. Diagnostics `coop-probe`, `coop-watch`, `actors`, `zone-dump`, `frame-phase-probe`, `heap-free`, `region-probe arm|check`, `wwdev.exe blob-verify-live` (live T2 read-back against `T2Bytes`), and debug `stage-hop`. Fix docs/04 and record #23.
- **M1.** Pure-Go Yaz0, RARC and DZS/DZR readers. `coop-tables <iso>`: keys and doors, heart sources, switch classes, push blocks, enemy ACTR with a setID report. That report is the go/no-go for M6.
- **M2.** Binding tag (V1-V6, scrub), backups, Hello role byte, 'O' codecs, CoopHub, world file, and the coop-sync pipeline in **dry run** with the join preflight.
- **M3.** The first release that writes game data. memBit OR classes, latching memory switches (deferred for the current table), ocean and charts. FrameWriter, WriteGuard, verify, auto-disable. Refresh area and warp.
- **M4.** Inventory union with ordered grants; derived keys, hearts and magic; story bits applied on transition, with holds; the register allowlist; `--coop-merge`; offline `save-diff`/`save-merge`.
- **M5.** Dan and zone sync for co-present players; room-edge relay. As built: §6.3 and items 113-120.
- **M6.0.** Kill union of keyed enemies and the K1 applier. As built: §7.2, §8.2 and items 141-155.
- **M6.1.** Placement-matched kills of unkeyed enemies, on M6.0's actor walk, re-read and fake actors, behind its own feature bit.
- **M7.** Make room; the getSave shim; the isActor shim; `coop_frame` live OR and the op ring. Go switches to image and ops. A spot check for `/warp`, so it can land beside the partner (a roof and ground check of B's collision at the spot, item 132).
- **M8.** TUI co-op panel (the app's panel shipped in v0.4.0), behind-by-N, feature toggles, soak tests, docs, release.

---

## 10. Open questions for the owner

1. **Save-merge model.** `Per-flag` or `Full-file`? **Recommended: Per-flag.** That means live union by class, plus a union when a file is bound, with a preview. About half of `dSv_save_c` is not monotonic (life, counters, registers, toggles), so a whole-file merge cannot be done safely or live. Per-flag sync also reconciles offline progress on every reconnect. The offline `save-merge` tool still provides a previewable full-file merge.
2. **Rupees.** `Separate` or `Shared`? **Recommended: Separate.** A shared wallet means applying deltas through `mItemRupeeCount` (0x803CA768), and chest duplicates would multiply the money. It can come later as an option.
3. **A partner's switch in your current room.** `Deferred` or `Live`? **Recommended: Deferred** as the default, with Live as an opt-in. Applying at your next room change avoids the partner's crystal starting your door cutscene mid-fight. Within the same room, dan and zone switches still sync live.
4. **Code room for M7.** `Arena` or `TRK`? **Recommended: Arena** (+8 KB, mailbox moves to 0x80415000). The mechanism is proven, and the heap error counters gate it. The TRK region costs no heap but is free only by inference.
5. **datstarkey/ww-online** (MIT, v0.7.0 on 2026-10-13, Dolphin with 48 MB MEM1 on Windows). `Port`, `Collaborate` or `Ignore`? **Recommended: Port.** Reuse its verified facts (LocalOnly lists, key and heart counts, switch classes, test checklists) with attribution in THIRD_PARTY notices. Keep our differentiators: Go/TUI, the packaged macOS app, and co-op combat. *(The 48 MB requirement was adopted on 2026-10-18 for the host-authoritative work, docs/05 "48 MB MEM1".)* ww-online also renders remote players as animated Links, so the mini-Link is parity, not a differentiator.
    - *Updated 2026-10-25 (the ww-online review, `ww-multiplayer-handoff/archive/ww-online-review/REPORT.md`):* it is no longer Windows-only. It vendors **Seaborne**, a native port of the decomp that runs the game inside its app on macOS, Apple silicon only, built from source on the player's Mac with no installer: developer-grade (its `README.md` "Playing natively on macOS (preview)"). macOS alone is therefore not a differentiator; a packaged app is.
    - It shows up to three remote players (`README.md` "See each other"; `PUPPET_MAX_SLOTS 3`, `GameMod/src/puppet_link/puppet_shared.h:33`); we show two (`MAX_REMOTE_LINKS`, `inject/include/mailbox_mp.h:26`).
    - It syncs no enemies or bosses, on purpose (`docs/live-world.md:10`). Our enemy mirrors, boss-kill relay (item 85) and hittable remote bodies have no counterpart there.

Also needed from the owner, and not a decision: capture the fixture save states F2-F5 once (§8.2).

---

## 11. Proposed GitHub sub-issues (all under parent #23)

**Epic E1: Co-op foundations**
1. **coop: Mem interface, FakeMem/FakeGame, cited address table.** Abstract Dolphin reads and writes behind `Mem`, and model the vanilla save behaviours this doc relies on. Every Appendix A address carries a decomp citation and an arithmetic test.
2. **coop: dSv_save_c byte-class table with golden coverage.** Classify all 0x778 bytes as OR, OR_MASKED, MAX, DERIVED, PER_PLAYER, NEVER or TAG. CI fails on any gap; the table drives the differ, the server and the WriteGuard.
3. **coop: read-only diagnostics.** Add `coop-probe` (stage, table, room, gates, pending counters), `coop-watch` (labelled bit edges) and `frame-phase-probe`. The owner runs them to confirm the inferred addresses.
4. **coop: actors and zone-dump via g_fopAcTg_Queue.** A bounded walk of 0x80372028, validated by `g_fopAc_type`. Print proc, setID, rooms, health, the enemyice self-pointer and the 32 zones.
5. **coop: heap-free probe and region canaries.** Walk the Zelda, Game and Archive heap free lists and read the error counters. Arm and check canaries over 0x8040CFB0-0x80410000 and 0x804130B0-0x80414000 (0x804130A0 before Phase E1 grew the mailbox to 0x1B0, 0x80413080 before the docs/09 tunic block grew it to 0x1A0; not 0x8040CFA8-0x8040CFB0, the stack-top frame header boot writes, §4.5).
6. **docs: record #23, fix the docs/04 save table, add co-op addresses.** Max and current life are u16 at 0x803C4C08 and 0x803C4C0A; the "add 0x803C4C0C" sentence is wrong. Add the event, memBit, stage, gate and pending-counter addresses, and the docs/05 frame_shim and 0x803FD000 corrections.

**Epic E2: Stage data from the user's ISO**
7. **stagedata: Yaz0, RARC and DZS/DZR readers.** Pure Go, reusing the `internal/inject` FST code. Tested on synthetic fixtures, with fuzzers.
8. **coop-tables: keys, hearts, switch classes, push blocks, enemy ACTR.** Cached by DOL hash and also built by `patch`. Report the invariants: keys and doors DRC 4/4, FW 1/1, TotG 2/2, ET 3/3, WT 2/2; 44 pieces plus 6 containers; setID uniqueness.

**Epic E3: Save safety and protocol v2**
9. **coop: binding tag in dSv_reserve_c with V1-V6 and leak scrub.** Covers the vanilla leaks (`d_save.cpp:1494-1503`, `d_s_name.cpp:1373`, `d_file_select.cpp:3625-3628`). Never write an unbound file's game data; only the tag is written (bind) or zeroed (scrub, clear).
10. **coop: memory-card backup at session start, plus restore.** Copy the raw card or GCI folder read-only, keeping 10 rotations. `coop-world restore` only runs while Dolphin is closed.
11. **network: protocol v2, Hello role byte and the 'O' co-op frame.** CO_HELLO, CO_WELCOME, WORLD, GAINS, PRESENCE, SCOPED, NOTICE and CTRL codecs, with fuzzers. Bump `ProtocolVersion` to 2; role-1 connections never join the roster.
12. **coop: CoopHub and world file.** A single-writer merge with validation and seq, scoped stores, and an atomic world file with journal, backups and tombstones. Includes the crash-injection tests.
13. **coop: coop-sync pipeline in dry run plus join preflight.** Snapshot, gates, differ, GAINS; the planner logs writes without making them. The preflight shows per-class gains and holds before binding.

**Epic E4: Shared world and progression**
14. **coop: reconciler, FrameWriter, WriteGuard, auto-disable.** Byte writes right after the heartbeat, verified on the next tick, inside the class allowlist. Auto-disable on repeated failures.
15. **coop: stage progress bits and latching memory switches.** OR tbox, item, visited and dungeonItem; live copy for the current table, saved slot for the others. Switches are set-only and latching, and deferred to the next room change for the current table.
16. **coop: inventory union with ordered grants and MAX capacities.** Derive slots like `d_s_play.cpp:1385-1392`, then re-apply capacities after the base grants. FF1 sword and GTower bow rules; `mSelectItem` refresh.
17. **coop: derived small keys coupled to applied door switches.** keys = sources set − doors set over the receiver's own applied flags, written through `mItemKeyNumCount` when the HUD is on. Enabled per table only when its invariant passes.
18. **coop: derived max hearts and magic, add-only.** Heart source table plus the NPC reward catalogue; apply through `mItemMaxLifeCount` while IDLE. Magic from the Deku Leaf and 0x3180.
19. **coop: story bits with a generated SyncMask, LocalOnly, holds and on-transition apply.** `gen_event_masks.py` works from the 50 `offEventBit` sites plus the D8 and refight lists. Holds cover MET_KORL, 0801, 0808, 2401, 2D10 and 3804.
20. **coop: offline save-diff and save-merge for GCI.** Parse `card_savedata` (`m_Do_MemCardRWmng.cpp:336-357` checksums) with the packed↔RAM map. Write only to a new file and verify by parsing it back.
21. **coop: refresh area and warp to partner.** A best-effort Go `mNextStage` write before M7 and the `SET_NEXT_STAGE` op after it. Same-stage warps use the mailbox `warp_seq`. A warp lands on the partner's own position; a landing beside it waits for M7, which can check B's own collision at the spot first (item 132).

**Epic E5: Live puzzles and combat**
22. **coop: dan and zone switch sync for co-present players.** Scoped by stage (dan) and by stage plus room (zone), using the latching table. 0xE0-0xEF are relayed live only and never stored.
23. **coop: kill union and the K1 light-shrink applier.** Zone actor-bit diffs, a scoped KILL store, and the self-pointer, health, event and boss gates. Includes an `enemy-kill <procID>` debug command and `peek-demo` (M6.0, items 141-155).
24. **coop: fixture save states and the combat/puzzle checklist.** Document F2-F5 and script screenshot pairs for each acceptance item.

**Epic E6: C co-op pump**
25. **inject: make room.** Arena 0x80414000 to 0x80416000; mailbox to 0x80415000. (Done already: the D2 cleanup in Phase E0, the 'WWMB' header in E1, the constants emitted into `blob.go`.) Layout invariants in CI, heap counters checked by the owner.
26. **inject: getSave shim in dStage_stagInfoInit, plus coop_frame live OR.** Locate the `bl` with `ppc-disasm`. ORs the world image into `mSavedata` under `image_ok` (armed, dead-man, slot, tag fingerprint, `mDateIPL`, refight).
27. **inject: isActor call-site shim and the op ring.** Dead tables per stage. Ops OR8, GRANT, ADD_S16, SET_U8, KILL, SET_NEXT_STAGE and ON_SWITCH, behind a whitelist and the IDLE gate. A spot query for `/warp` (RoofChk and GroundCross at a spot beside the partner, item 132).

**Epic E7: v1.0 release**
28. **tui: co-op panel, feature toggles, coop off.** (The app's co-op panel and Co-op sync log tab shipped in v0.4.0; what is left is TUI parity.) Bound/armed state, holds, last syncs, behind-by-N, at 80x24. Host-authoritative feature mask.
29. **release: soak tests, docs/08-coop-design.md, checklist.** A 50-transition soak and a 2 h soak with `save-diff` clean at the end. Blob-regen and generator freshness gates before tagging v1.0.0.

---

## Appendix A: Address table (GZLE01)

| Name | Address | Size | Source |
|---|---|---|---|
| `g_dComIfG_gameInfo` (save `dSv_info_c` at +0) | 0x803C4C08 | 0x1D1C8 | sym:17472; d_com_inf_game.h:903-928 |
| `play` (`dComIfG_play_c`) | 0x803C5EA8 | 0x4A7C | d_com_inf_game.h:914 (save is 0x12A0) |
| `mMaxLife` / `mLife` (u16, quarter hearts) / `mRupee` (u16, rupees) | 0x803C4C08 / 0x803C4C0A / 0x803C4C0C | 2 each | d_save.h:40-53 |
| `mSelectItem[5]` / `mSelectEquip[4]` / `mWalletSize` / `mMaxMagic` / `mMagic` | 0x803C4C11 / 0x803C4C16 / 0x803C4C1A / 0x803C4C1B / 0x803C4C1C | — | d_save.h:45-49 |
| status_b `mDateIPL` u64 (0 until first card load) | 0x803C4C20 | 8 | d_save.h:72-79; d_save.cpp:51-56 |
| `mReturnPlace` | 0x803C4C38 | 0xC | d_save.h:92-97 |
| `mItems[21]` | 0x803C4C44 | 0x15 | d_save.h:117; d_com_inf_game.h:1072-1110 |
| `mGetItem.mItemFlags[21]` | 0x803C4C59 | 0x15 | d_save.h:131; d_save.cpp:204-281 |
| `mItemMax` (arrow max 0x803C4C77, bomb max 0x803C4C78) | 0x803C4C76 | 8 | d_save.h:179-195 |
| `mGetBagItem` (beast/bait flags at +4/+5) | 0x803C4C98 | 0xC | d_save.h:246-249 |
| `mCollect` / `mTact` / `mTriforce` / `mSymbol` | 0x803C4CBC / 0x803C4CC5 / 0x803C4CC6 / 0x803C4CC7 | 0xD | d_save.h:291-296 |
| `mMap` (get 0x803C4CDC, open 0x803C4CEC, complete 0x803C4CFC, fmap 0x803C4D0C, tri-charts 0x803C4D4D) | 0x803C4CCC | 0x84 | d_save.h:432-437; d_save.cpp:827-963 |
| `mPlayerName` / `mClearCount` | 0x803C4D64 / 0x803C4DA8 | 17 / 1 | d_save.h:460-469 |
| `mMemory[16]` saved per table (tbox +0, switch +4, item +0x14, visited +0x18, keyNum +0x20, dungeonItem +0x21) | 0x803C4F88 + 0x24·t | 0x240 | d_save.h:684-690,909 |
| `mOcean` | 0x803C51C8 | 0x64 | d_save.h:700,910 |
| `mEvent` (bits 0x00-0x41; registers 0x79-0xFF) | 0x803C522C | 0x100 | d_save.h:716,911; d_save.cpp:1187-1210 |
| `dSv_reserve_c` (binding tag) | 0x803C532C | 0x50 | d_save.h:721-725,912; d_save.cpp:1780,1867 |
| live `mMemory` (current table; keyNum 0x803C53A0, dungeonItem 0x803C53A1) | 0x803C5380 | 0x24 | d_save.h:992; d_stage.cpp:1675,2256 |
| `mDan.mStageNo` / `mDan.mSwitch[2]` | 0x803C53A4 / 0x803C53A8 | 1 / 8 | d_save.h:752-755; d_save.cpp:1225-1236 |
| `mZone[32]` (mRoomNo +0, switch u16[3] +2, item +8, actor bits u32[16] +0xC) | 0x803C53B0 + 0x4C·z | 0x980 | d_save.h:757-798,994 |
| `mRestart` / `mTmp` / `mTurnRestart` | 0x803C5D30 / 0x803C5D60 / 0x803C5E60 | 0x30 / 0x100 / 0x38 | d_save.h:826-871,995-996 |
| `mDataNum` (file slot) | 0x803C5E98 | 1 | d_save.h:997-1001; d_file_select.cpp:811,3641 |
| `mCurStage` (name[8], point s16 +8, room +0xA, layer +0xB) | 0x803C9D3C | 0xC | d_com_inf_game.h:723; d_stage.h:949-952 |
| `mNextStage` (`mEnable` at +0xC = 0x803C9D54) | 0x803C9D48 | 0xE | d_com_inf_game.h:724; d_stage.h:955-968 |
| `mStageData.mpStagInfo` (mProp +9: table = (mProp>>1)&0x7F, key HUD = mProp&1) | 0x803C9DA0 | 4 | d_com_inf_game.h:726; d_stage.h:66-77,1023-1029 |
| `mEvtCtrl.mOrderCount` / `mMode` (inferred: no STATIC_ASSERT) | 0x803C9EA0 / 0x803C9EA2 | 1 / 1 | d_com_inf_game.h:728; d_event.h:133-160 |
| `mpPlayerPtr[0]` | 0x803CA754 | 4 | d_com_inf_game.h:791 |
| `mItemLifeCount` f32 / `mItemRupeeCount` s32 | 0x803CA764 / 0x803CA768 | 4 / 4 | d_com_inf_game.h:793-794 |
| `mItemKeyNumCount` / `mItemMaxLifeCount` / `mItemMaxMagicCount` (s16) | 0x803CA77C / 0x803CA77E / 0x803CA784 | 2 each | d_com_inf_game.h:799-803; d_meter.cpp:1420-1452,5242-5257 |
| play `mSelectItem[4]` / `mSelectEquip[4]` | 0x803CA7DB / 0x803CA7DF | 4 / 4 | d_com_inf_game.h:830-831,2924-2938 |
| `mPlayerInfoBufferStageNo` (refight active if != 0) | 0x803CA8C8 | 1 | d_com_inf_game.h:882; d_com_inf_game.cpp:1446-1457,1580-1581 |
| `mMiniGameType` | 0x803CA8E2 | 1 | d_com_inf_game.h:886 |
| `dStage_roomControl_c::mStatus[64]` (stride 0x114; mZoneCount +0x106, mZoneNo +0x107) | 0x803BDC88 | 0x4500 | sym:17429; d_stage.h:855-871 |
| `mStayNo` (current room) | 0x803F6A78 | 1 | sym:18667 |
| `dMenu_pause` | 0x803F7097 | 1 | sym:19130 |
| `g_mDoGaC_gbaCom` (`mDoGaC_agbCom_c`: mEnable +0, connect state +2, closed +4, probes +0x10E, context `field_0x128[0].field_0x0` +0x128) | 0x803B904C | 0x130 | sym:17367; m_Do_gba_com.h:67-110; m_Do_gba_com.cpp:258-270 |
| `g_fopAcTg_Queue` (head, tail, size; node = &actor->actor_tag; prev +0, data +4 (= 0x80372028 when queued), next +8; actor = *(u32*)(node + 0xC) (`mpTagData`), equivalently node − 0xC4) | 0x80372028 | 0xC | sym:14653; c_list.h:6-10; c_list.cpp:39-40; c_tag.h:10-14; c_tag.cpp:89-92; f_op_actor.h:272 |
| `g_fopAc_type` | 0x803F69D0 | 4 | sym:18627; f_op_actor.cpp:116-119 |
| `DMC` (REL module control table) | 0x803B9218 | 0x7D8 | sym:17371 |
| `__OSArenaLo` variable (moved to the arena top by `JKRHeap::initArena` at boot, `JKRHeap.cpp:77-95`; read `JKRHeap::mCodeEnd` 0x803F773C, sym:19563, for the patched base) | 0x803F66C0 | 4 | sym:18462 |
| heap ptrs zelda / game / archive | 0x803F6928 / 0x803F6920 / 0x803F6938 | 4 each | sym:18588-18594 |
| heap error counters zelda / game / archive | 0x803F68FC / 0x803F68F8 / 0x803F6904 | 4 each | sym:18577-18580 |
| actor fields | mBsPcId +0x04, mProcName +0x08, mParameters +0xB0, actor_type +0xC0, eventInfo.mCommand +0xF8, setID +0x1BC, group +0x1BE, actor_status +0x1C4, home.pos +0x1D0, home.roomNo +0x1E2, health +0x285 | fopAc_ac_c 0x290 | f_pc_base.h:13-32; f_op_actor.h:268-321 |
| mod T2 / mailbox / patched arenaLo | 0x80410000..0x80412A88 / 0x80412F00..0x804130B0 / 0x80414000 | — | inject/build.py (`inject_address`, the 0x80301820 arena patch); `MAILBOX_ADDR` in mailbox_core.h |
| `spawn_trigger` (heartbeat) / `warp_seq` | 0x80412F00 / 0x80412FC8 | 4 / 4 | `spawn_trigger`, `warp_seq` in mailbox_core.h |

**Functions**

| Function | Address | Source |
|---|---|---|
| `dStage_stagInfoInit` (getSave + initDan; **M7 shim site**) | 0x8004230C (size 0x80) | sym:1289; d_stage.cpp:1668-1677 |
| `dStage_actorInit` (sole `isActor` caller; **M7 shim site**) | 0x800423C0 (size 0xFC) | sym:1291; d_stage.cpp:1688-1708 |
| `dStage_Delete` (putSave) | 0x80043464 | d_stage.cpp:2247-2260 |
| `dSv_info_c::getSave` / `putSave` | 0x8005D8C8 / 0x8005D988 | sym:1938-1939; d_save.cpp:1506-1515 |
| `dSv_info_c::onSwitch` / `offSwitch` / `isSwitch` / `revSwitch` | 0x8005DB24 / 0x8005DCEC / 0x8005DE98 / 0x8005DFE0 | sym:1942-1946; d_save.cpp:1540-1627 |
| `dSv_info_c::onActor` / `isActor` | 0x8005E4BC / 0x8005E5F0 | sym:1949-1950; d_save.cpp:1671-1701 |
| `dSv_event_c::onEventBit` / `setEventReg` | 0x8005CB04 / 0x8005CB58 | sym:1907,1910; d_save.cpp:1187-1205 |
| `dComIfGs_onStageTbox` (writes both copies) | 0x8005388C | sym:1687; d_com_inf_game.cpp:694-702 |
| `dComIfGs_checkGetItem` | 0x80053B30 | sym:1692 |
| `dComIfGp_setNextStage` | 0x800537C8 | sym:1686; d_com_inf_game.cpp:666-684 |
| `execItemGet(u8)` / `checkItemGet` / `item_func_ptr` | 0x800C2DFC / 0x800C2E30 / 0x803888C8 | sym:3432-3433,15179; d_item.cpp:534-544 |
| `dDoor_key2_c::keyInit` | 0x8006C4A8 | sym:2259; d_door.cpp:434-448 |
| `enemy_ice` | 0x8001C0B4 | sym:552; c_damagereaction.cpp:117-171 |
| `fopAcM_delete(actor*)` / `fopAcM_delete(procID)` | 0x80024478 / 0x800244B8 | sym:643-644 |
| `fopAcM_myRoomSearchEnemy` | 0x80027970 | sym:711; f_op_actor_mng.cpp:1271-1294 |
| `dComIfG_play_c::getLayerNo` | 0x80052400 | sym:1640; d_com_inf_game.cpp:185-269 |
| `dMeter_LifeMove` / `dMeter_keyMove` | 0x801F15EC / 0x801FCF28 | sym:7264,7337 |
| `cCt_Counter` (still run by frame_shim's tail call) | 0x802449AC | sym:8240; f_ap_game.cpp:77-81 |
| `OSPanic` (JUT_ASSERT target) | 0x80006C4C | sym:55; m_Do_printf.cpp:208-230 |
| `dScnPly_Execute` (Archipelago's execItemGet hook, prior art) | 0x80234FD0 | sym:7941 |

## Appendix B: Behaviour references used by this design

**Save flow**
- Card format: block 0 of the save file is a header. `card_savedata` (0x2000 bytes: u32 save_count, u32 data_version, then 3 × {0x768 packed + u64 csum} from +8, and an outer csum at 0x1FFC) is at 1 × 0x2000, with a mirror at 2 × 0x2000. Sources: `m_Do_MemCardRWmng.h:29-42`, `m_Do_MemCardRWmng.cpp:35-69,336-357`; packed layout `d_save.h:922-932`.
- `memory_to_card` 0x8005E780 (`d_save.cpp:1703-1791`); `card_to_memory` 0x8005EA24.

**Switch routing** (`d_save.cpp:1540-1627`; constants `d_save.h:982-989`)

| Range | Store |
|---|---|
| < 0x80 | memBit (saved) |
| 0x80-0xBF | dan, cleared when the table changes |
| 0xC0-0xDF | zone |
| 0xE0-0xEF | zone room switches, cleared on every room change |
| 0xF0-0xFE | invalid |

**Polling and create-only actors.** See §6.1. Additional sources: `d_a_swhit0.cpp:224-367`, `d_a_obj_swpush.cpp:20-92`, `d_a_tag_event.cpp:53-263`, `d_event.cpp:37-80,297-329,565`.

**Prior art**
- Archipelago TWW client: https://github.com/ArchipelagoMW/Archipelago/blob/main/worlds/tww/TWWClient.py (lines 32-79 hold matching addresses).
- wwrando AP fork (`archipelago.asm:1-67`, execItemGet queue): https://github.com/tanjo3/wwrando/tree/archipelago
- datstarkey/ww-online (MIT; `docs/live-world.md`, `event-flags.md`, `small-keys.md`, `hearts.md`): https://github.com/datstarkey/ww-online
- wwrando data (`dzx.py` ACTR setID, `enemy_types.txt`, `item_locations.txt`): https://github.com/LagoLunatic/wwrando
- Flags and the US event-flag address: https://zeldaspeedruns.com/tww/general-knowledge/flags-and-triggers
- sm64coopdx death-only object sync: https://github.com/coop-deluxe/sm64coopdx
- Licences: Windwaker-coop (BrandenEK) has no licence, so its facts are re-derived rather than its code copied. Z64Online is GPL, so none of its code is copied.

## Appendix C: Corrections applied (from the design reviews)

1. **FrameWriter window.** In GZLE01, `fpcM_Management` runs Painter, deletes, `fpcPi`, then **actor creates**, then execute and draw (`f_pc_manager.cpp:276-292`). A Go write after the heartbeat lands before the creates, but creates can write save bits. The window is not write-free, so the reconciler keeps re-asserting, and M7 removes the race.
2. **0xE0-0xEF room switches** are zeroed on every room change (`d_save.cpp:1273-1275`). They are relayed live only between players in the same room, and never stored or re-applied.
3. **`dSv_reserve_c` leaks across files** (`d_save.cpp:1494-1503`, `d_s_name.cpp:1373`, `d_file_select.cpp:3625-3628`). A world ID stored there must pass V1-V6 and is scrubbed when it has leaked. The C shim also checks the tag fingerprint and `mDateIPL`.
4. **`frame_shim` tail-calls `cCt_Counter`** (0x802449AC), so it still runs. The `build.py` comment saying "we lose whatever 0x802449AC was doing" is stale, and docs/05 calls this function "heap bookkeeping". Both get fixed in issue 6.
5. **Item funcs set capacity unconditionally:** bow arrows 30 (`d_item.cpp:733-738`), bombs 30 (`:796`). Grants are ordered base then upgrade, with capacities re-applied by MAX afterwards.
6. **Key door locks** (a reviewer claimed they are create-only). They are re-evaluated whenever the door returns to `actionInit`, which happens every frame the local Link is not adjacent (`d_a_door10.cpp:804-813`, `748-749`; `d_a_door12.cpp:823-831`, `766`). The real hazard is applying a key decrement before the door switch, so keys are derived from the receiver's own applied flags (§6.4).
7. **Memory-switch sync is latching-only from its first release (M3), not just push-block exclusion.** `swc00` writes its switch every frame (`d_a_swc00.cpp:12-33`).
8. **Go writes to game-owned save memory exist until M7.** This doc claims no race-freedom before then.
9. **KILL defaults to K1, not delete.** `fopAcM_delete` skips the death switch and the drop (`c_damagereaction.cpp:150-160`) and risks dangling weapon or grab references. Delete is used only for classes without `enemyice`.
10. **docs/04 life fields** are u16 at +0x00 and +0x02 (0x803C4C08 and 0x803C4C0A), in quarter hearts. "Add 0x803C4C0C for absolute" is wrong.
11. **Heart totals:** vanilla is 44 pieces + 6 containers. 12 + 44 + 24 = 0x50 quarter hearts, the HUD cap (`d_meter.cpp:1432`). A research brief counted 46 piece rows in wwrando data; M1 resolves this against the user's ISO. The owner's disc gives 44 and 6 (item 137).
12. **docs/05:63-73, T2 at 0x803FD000.** The section was inside the main stack (0x803FCFA8..0x8040CFA8), so `OSClearStack` is the likely cause (inferred).
13. **Return values, if entry hooks are ever used:** a wrapper must return the trampoline's r3 (for example from `revSwitch` and `isActor`), not the incoming r3. This doc avoids entry hooks entirely.
14. **The TRK canary starts at 0x8040CFB0, not at `_stack_addr` (0x8040CFA8).** Boot saves LR at 4(`_stack_addr`) = 0x8040CFAC and never clears it (0x8000336C, §4.5 table), so a canary from 0x8040CFA8 found a busy word on every booted game and `region-probe arm` refused it without `--force`.

**Found while building M1 against the decomp** (zeldaret/tww at 4d78c99; the code cites each point at file:line):

*Stage readers (§6.2, §6.5)*

15. **`l_objectName` is declared at `d_stage.cpp:437`**, not :451. Its entries are lines 438-1262: 825 entries of 0xC bytes, 0x26AC bytes in all. Line 451 is the "door10" entry. The symbol (sym:14693, .data 0x80372818, size 0x26AC) is as the design says.
16. **Chunks.** The game also places actors from TRES (chests, `d_stage.cpp:2160`, `:2204`) and TGSC (`:2165`, `:2204`), so both are parsed. The only layer chunks are ACTn, SCOn and TREn (`:2072-2148`); TGOB is read on every layer. Only the first chunk of each tag is read: `dStage_dt_c_decode` stops at the first match (`:2032-2038`), so a duplicate chunk is ignored.
17. **`getLayerNo` runs `d_com_inf_game.cpp:185-269`**, not 185-245: the M2tower, GanonK and GTower cases come after 245. Only the sea gives different rooms different layers (`:192-209`).
    - Room numbers come from the `Room<N>.arc` names (`d_s_room.cpp:52`). RTBL, FILI and LBNK aren't needed; LBNK only picks the Demo archive (`:206-215`).
    - The game can't load a stage name longer than 7 characters (`d_resorce.cpp:707-708`).
18. **Inferred, not in the decomp:** RARC's "." and ".." entries in each node, the convention that a node's type is its directory name upper-cased and space-padded ("dzs" gives "DZS "), and the DOL header layout. The readers don't depend on the first two.
    - The game's RARC lookup lowercases the query and compares it with the stored name case-sensitively, with a hash (`JKRArchivePri.cpp:45-50`, `:189-224`). The reader folds both sides, which gives the same result for the game's lowercase names.
    - The game's unbuffered `JKRMemArchive::fetchResource` returns a compressed entry's raw bytes; only the buffered fetch decompresses (`JKRMemArchive.cpp:149-184`). The reader always decompresses.
    - Confirmed: setID is at ACTR +0x1E (`f_op_actor_mng.h:42` via `d_stage.h:195`). STAG `mProp` bit 0 is the key HUD and bits 1-7 the save table; the stage type is in `TypeAndSchbit` bits 16-18 (`d_stage.h:1023-1033`).
    - Names the design leaves implicit: torches are "bonbori" (`fpcNm_EP_e`); the room's heart container is "Bitem" (`fpcNm_BOSSITEM_e`); "ObjTime" is `fpcNm_Obj_Timer_e` (0x1E1), not `fpcNm_TIMER_e`, the HUD timer.

*Small keys (§6.4)*

19. **A third key-door class.** kddoor (`fpcNm_KDDOOR_e` 0x132; "doorKD", "ZenshKD", "K_ZshKD", "doorSH", "doorSZ", `d_stage.cpp:460-464`) has a small-key lock in type 2 (`chkMakeKey`, `d_a_kddoor.cpp:12-14`; `keyCreate(0)`, `:578-579`).
    - door10 types 4/5, door12 type 1 and switch = `prm & 0xFF` are confirmed (`d_door.cpp:16-18`, `:26-28`; `d_a_door10.cpp:13-24`; `d_a_door12.cpp:39-49`).
    - door10 type 1 and door12 type 3 are boss-key doors. door12's create forces type 3 when `getArg1` is 9 or 0xC (`d_a_door12.cpp:576-578`).
20. **The key-door switch encoding is no longer inferred**, but `keyInit` records the switch only below 0x80 (`d_door.cpp:433-447`).
    - A key door with a dan switch (0x80-0xBF) or 0xFF spends the key, but its lock comes back.
    - With a zone switch, `setKey` reads it for room -1, which `isSwitch` asserts against (`d_save.cpp:1596`).
    - M1 reports both as problems of the table.
21. **The key invariant** is checked three ways: sources == the vanilla count (§6.4), sources == doors, and every source and door recorded in a saved bit, since keys(t) can count only saved flags.

*Hearts (§3, §11 items 8 and 11)*

22. **Helmaroc King creates no heart container.** It sets `STAGE_BOSS_ENEMY` (`d_a_bdk.cpp:3232`, `:4235`), but unlike the other five bosses (`d_a_btd.cpp:1082`, `d_a_bmd.cpp:747`, `d_a_bst.cpp:2504`, `d_a_bpw.cpp:2856`, `d_a_bwd.cpp:1704`) it makes no container, so Forsaken Fortress's comes from a Bitem or a placed one. Gohma, Kalle Demos, Jalhalla and Molgera make none either in Ganon's Tower's refights (Xboss0-3), where they warp out instead (item 137).
    - Every container is recorded only by `STAGE_LIFE`: for the stage's save table, or for table 2 (Forsaken Fortress) when taken on the sea (`d_item.cpp:588-604`).
    - A Bitem is not a separate container: it re-creates the boss's while that bit is clear (`d_a_boss_item.cpp:24-41`).
23. **44 or 46 pieces (item 11) needs the real disc.** The catalogue has 11 decomp-verified rewards plus 3 minigames whose grant code is not decompiled (`d_a_npc_kg1`; `d_a_npc_kg2` `evn_createItem_init`, `:167-170`; `d_a_npc_bmcon1` `eventGetItemInit`, `:159-162`). The stage files must supply 30 to reach 44. Resolved by item 137: they do once the test stages are left out, and the 30th is the pig's dig spot on Outset (`TagKb`).
    - Carlov and Old Man Ho Ho appear to give item 7 (`d_a_npc_mt.cpp:823`, `d_a_npc_ah.cpp:550`), but only from bits nothing in the decomp sets, so they are not in the catalogue.
    - The decompiled mail-sorting game pays only rupees (`d_a_npc_bmsw.cpp:743`).
24. **Salvage ocean bits.** For kinds 2-4, entry checks the bit for the map room unless the save number is 31 (`d_salvage.cpp:96-105`). Kind 4's night check and `end_salvage` use the actor's room (`:115`; `d_a_salvage.cpp:323-326`, `:504`, `:517`). `isOceanSvBit` and `onOceanSvBit` assert on any grid past 0x31 or bit of 16 or more (`d_save.cpp:1164-1173`).
    - A map room past 0x31 (the actor's room, or `(prm >> 12) & 0xFF` for a room-0 point) halts the game. So does a kind-4 save number of 16-255, 31 included. This is the same halt as a setID past 0x1FF (item 33). M1 marks these sources `halt` and reports each one as a problem of its table.
    - Only a room-0 point whose map room is 1-0x31, with a save number up to 15, records into grid 0, which entry never reads. M1 counts that one as unsaved.
    - An item bit of 0x50-0x7E halts the same way (`dSv_info_c::isItem`, `d_save.cpp:1651-1652`; `fopAcM_createItemFromTable`, `f_op_actor_mng.cpp:806`). Its sources are reported as halting too.
    - Grass and flowers place no item. `daGrass_Create` keeps only items 0x20-0x3F, the random drop tables (`d_a_grass.cpp:144-145`, `:173-174`), so M1 reads none of their entries.

*Switches (§6.2, §6.3)*

25. **Dan switches (0x80-0xBF) are kept per save table, not per stage.** `dStage_stagInfoInit` calls `dComIfGs_initDan` with the STAG save table (`d_stage.cpp:1669-1677`), and `dSv_danBit_c::init` clears only when the table changes (`d_save.cpp:1225-1235`). M1 scopes them like memory switches. §6.3's stage-name match for live dan writes is kept as apply policy.
26. **`andsw0` is not unsafe across the board.**
    - Behaviours 0 (one-shot) and 3 (one-shot, 65 frames later) only set their output (`d_a_andsw0.cpp:40-57`, `:129-135`), so they are latching ANDs.
    - Behaviour 2 is timed and clears all its inputs on timeout (`:101-127`), so its output ends up unsafe through propagation.
    - The other behaviours are continuous and clear the output (`:59-82`), so they are unsafe.
    - num 0xFF is the Forest Haven special and sets no switch.
27. **`SW_C00` and the event tags.**
    - Only `SW_C00` type 0 sets and clears every frame, and clears at create (`d_a_swc00.cpp:11-33`, `:53-56`). Types 1-3 set once, delete themselves, and don't create if the switch is already on, so they latch.
    - `tag_evsw` type 1 never clears (`d_a_tag_evsw.cpp:17-23`, `:39-45`). It is classed Event, since it is derived from an event bit that §6.5's story batch carries. TAG_EVENT and TAG_ISLAND set their switches as their story events start, so they are Event too.
    - No Event writer is synced on its own.
28. **Floor and iron-boots switch types are 3 bits wide** (`d_a_obj_swpush.h` `PRM_TYPE_W` = 3; `d_a_obj_swheavy.h:31-32`). `M_attr` has only four entries, indexed without a check, so types 4-7 are classed Unknown.
    - Torch types 0 and 3, even untimed, also clear their switch when water covers the flame (`d_a_ep.cpp:311-319`, `:476-484`). They stay latching as §6.2 asks.
    - A torch timer of 0 counts as timed; only 0xFF means untimed.
29. **Setters §6.2 doesn't list.** M1 classifies these, with citations in `wwapi/tables/switches`.
    - Latching: kill-all door bars (door10 front type 2 and back type 5, door12 front type 2, kddoor front type 1 and back types 2/4, mbdoor type 1); chest open switches (`home.angle.z`) and enemy-chest switches routed in the room from `home.angle.x`; enemy death switches (BK, MO2, ST, TN); the boss Bokoblin's room switches 0xE0/0xE1; many breakables and one-shot objects.
    - Unsafe: Helmaroc King's dan 0x80, Jalhalla's light switches, Phantom Ganon's second switch, the Tingle Tuner switches (agbsw0; class GBA since item 138), the propeller switch (it toggles), Obj_Correct (except a statue slot with a twin, §6.2, SchemaVersion 8), the Ferris wheel, the pig dig spot (TAG_KB_ITEM), Tag_Volcano, Zephos/Cyclos (NPC_HR), the second switch of the hammer switch (MhmrSW0), and sun switches (swlight type 1).
30. **Switch users the decomp can't decode** are listed in the switches report under "Undecoded switch users".
    - They are NonMatching actors whose switch getters are empty: Itnak, Vds, Ashut, Ekskz, Firewall, Flame, Hami2, Htetu1, Kanoke, Msdan, Tapestry, TnTrap, Tribox, BigElf, Kk1, P2, PT, WZ. (Obj_Swflat, the Hfbot1A-C floor switches, was on this list until SchemaVersion 8 read it from the retail REL, §6.2.)
    - A switch one of them shares with latching setters can wrongly show as synced, so the owner reviews the list on the real ISO.
    - Obj_Try had a real switch field but no decoded code, so its writers were classed Unknown. Since SchemaVersion 8 it is read from the retail REL: it only reads its switch, and only for types 5 and 6 (§6.2, statue slots).
    - The switch a stage's EVNT entry sets as its event starts (`dEvent_exception_c::setStartDemo`, `d_event_manager.cpp:24-51`) was not covered while stagedata kept no EVNT entries. Since SchemaVersion 10 the tables list it (`startEvents`, §6.2).
31. **Line fixes:** the push-block path code is at `d_a_obj_movebox.cpp:1153-1198` (§6.2 said 1163-1206). The `enemy_ice` death switch is set at `c_damagereaction.cpp:165-166` and `:348-349`.

*Enemies (§7)*

32. **Only 0x20-byte entries are dead-checked.** §7.1's "`dStage_actorInit` skips actors whose setID is recorded dead" holds for ACTR, TGOB, TRES, ACTn and TREn only.
    - TGSC, SCOB, DOOR, TGDR and SCOn go through `dStage_tgscInfoInit`, which never checks the record (`d_stage.cpp:1712-1730`). An enemy there keeps its setID but respawns however dead it is recorded, so §7.3's `isActor` shim doesn't keep it away either.
    - The enemy report counts these as "unchecked".
33. **setID must be below 0x200, not just != 0xFFFF.** A zone holds setIDs 0-511 (`d_save.h:780`), and `isActor` and `onActor` assert above that (`d_save.cpp:1676`, `:1690-1692`). `JUT_ASSERT` is compiled into GZLE01 (`JUTAssert.h:9`) and its `OSPanic` halts the game (`m_Do_printf.cpp:208-230`).
    - So a setID of 0x200-0xFFFE halts the game when a dead-checked entry's room loads, or when an unchecked one's death is recorded. It never writes out of bounds.
    - M6 requires setID < 0x200. The enemy table counts such entries in `Halting`, named or not.
34. **Detection and `home.pos`.**
    - Morth (`ks`) never calls `fopAcM_onActor` either. Like Kargaroc it records its death only through `enemy_ice` (`c_damagereaction.cpp:159`, `:369`), so it needs §7.2's observation path.
    - Being in `fopAc_ENV_e`, Morth is never counted by room-clear logic (`enemySearchJugge`, `f_op_actor_mng.cpp:1275`).
    - Besides Keese and Kargaroc, a Bokoblin on a searchlight (`stand2`, `d_a_bk.cpp:1455-1463`), Kalle Demos (`d_a_bmd.cpp:2001`) and Helmaroc King (`d_a_bdk.cpp:680-690`) move `home.pos`.
35. **The `fopAc_ENEMY_e` group includes non-enemies:** Orca (NPC_JI1, `d_a_npc_ji1.cpp:5532`), the Warship (OSHIP), a Forbidden Woods tentacle that `d_a_shand.cpp` calls an Object, and boss-created parts (ATT, BWDG, FGMAHOU, BMDHAND, BMDFOOT).
    - A live filter of group == 2 alone would include them; the K1 allowlist excludes them.
    - setID also serves NPC event cuts as an actor handle ("SetId", `d_npc_event_cut.inc:106-111`, `:353`), so a nonzero setID on an NPC need not be a zone bit.
36. **The BOSS gate and the denylist.**
    - Phantom Ganon and Big Octo never get BOSS status (profiles at `d_a_fganon.cpp:2970`, `d_a_daiocta.cpp:1771`), so §7.2's proc denylist is required.
    - Some ordinary-looking K1 entries do get BOSS status at creation, so the BOSS gate always skips them: a Stalfos, Moblin or Darknut with a memory death switch (u8 of `angle.z` 1-0x7F), a Darknut with behaviour 13 or 14, and a Bokoblin of type 0xA.
    - Darknut's file matches D44J01 only (`configure.py:1858`), so its rule and `onActor` site hold for GZLE01 by inference.
37. **Minor citation fixes.** Zones are cleared at stage entry only when the start point is ≥ 0 (`d_stage.cpp:124-126`; §7.1 cited 118-140). `OSPanic` spans `m_Do_printf.cpp:208-230` (§3.2 and Appendix A said 208-233). Every §7.2 K1 proc number and timer offset (enemyice member + 6) matches `f_pc_name.h` and the headers.

**Found while building M2 against the decomp** (same checkout; the code cites each point at file:line):

*Protocol and codecs (§4.4)*

38. **SCOPED words index the game's own arrays.** §4.4's `{word:u8, bits:u32}` suggests u32 words everywhere, but zone switches are `u16 mSwitch[3]` (`d_save.h:770`), set at `i_no >> 4` (`d_save.cpp:1279-1282`). The codec follows the decomp:
    - DAN: words 0-1, u32 (`d_save.h:754`).
    - ZONE: words 0-1, u16, so bits must fit in 0xFFFF.
    - ROOMEDGE: word 2 only, u16; `clearRoomSwitch` zeroes it (`d_save.cpp:1274-1276`).
    - KILL: words 0-15, u32 (`mActorFlags[16]`, `d_save.h:782`).
39. **Wire ranges from the decomp.**
    - Stage names are `strcpy`'d into `char[8]` (`d_stage.cpp:112`, `d_stage.h:949`), so a name is at most 7 characters, and RAM can keep an older name's tail after the NUL. The wire takes only the canonical form: printable ASCII without spaces, then NUL padding.
    - CO_HELLO's slot is 0..2: `mDataNum` holds the file-select cursor (`d_file_select.cpp:458-471`), and the card has three files (`gamedata[3]`, `m_Do_MemCardRWmng.h:39`).
    - PRESENCE's layer is -1..11: -1 means the game picks the layer by the time of day (`d_com_inf_game.cpp:185-190`).
40. **WORLD strips more than §4.4 says.** Besides the PER_PLAYER, NEVER and TAG bytes, the bits outside a merging byte's class mask are zeroed (an event register's local bits, `mDungeonItem` bits 6-7). DERIVED bytes travel, because the hub's preflight reads them. `wire.StripLocal` makes exactly this image.
41. **Encodings §4.4 leaves open.**
    - CO_WELCOME status: 1 OK, 2 UNBOUND, 3 REFUSED; 0 is invalid, so an unset field never reads as OK. `reason` is a NOTICE code (0 for none); a REFUSED welcome has a reason and features 0.
    - NOTICE codes 1-7 and CTRL ops 1-3 follow §4.4's order. A TOMBSTONE's arg is `off<<8 | mask`, checked like a gain.
    - GAINS offsets are strictly ascending, so n ≤ 0x778 and each batch has one encoding (`wire.CanonicalGains`). An s→c origin is the co-op connection ID; 0 is the hub.
    - Feature bits live in `mods/coop/hub`: FeatureImage, FeaturePuzzle and FeatureCombat, plus the grant-only FeatureMerge (1<<8) and FeatureBind (1<<9) and the hello-only AskBind (1<<31) and AskUnsaved (1<<30), which carry §5.3's bind request and fresh bind.
    - A GAINS batch is a repeat when `int32(batch - last) <= 0`, so the counter can wrap.
    - NOTICE text is valid UTF-8 without control characters, at most 255 bytes, as player names are. `ClassHash` is FNV-1a 64 over the class spans.
42. **Kept as specified, a limit:** a relayed PRESENCE or SCOPED carries no sender. That is fine for two players; more would need an origin byte and a Schema bump.
43. **Player connections are protocol 1 apart from the version.** Players still send the 2-byte hello; only co-op connections send `[version][role][caps]`. Co-op connections get a normal MsgWelcome with an ID from their own 1..255 space. RejectRole (4) is fatal, so a co-op client stops at once against a host without `--coop`.
44. **A known client race, not fixed (mods/multiplayer/net).** `readLoop`'s error path marks the client disconnected without checking that its conn is still the current one; `abandonSession` does check. A Disconnect followed at once by a Connect on the same Client can lose the new session. RunCoop never hits it, and not because of a backoff: after a session that lived `minHealthySession` it redials at once. Its `live` ends a session early only after the read loop itself has cleared `connected`, which is that loop's last write, and `Replica.Session` returns only when its context ends, so no stale read loop is left to clear the next session's flag. A session function that returns while its connection is still open would need the race fixed first. coop-sync keeps one Client per pipeline, as the player pipelines do.

*Binding tag (§5.2)*

45. **What the tag hashes and checks.** `nameHash` is 32-bit FNV-1a over `mPlayerName` up to its NUL: the name is set with `strcpy` (`d_save.h:446`, `d_save.cpp:969`), which leaves an older name's bytes after the NUL. The CRC is CRC-32 (IEEE) over +0x00..+0x27.
    - Layout 1 defines no flags and writes 0. A layout-1 tag with flags or the reserved u32 set is a newer writer's, so it is Foreign; non-zero bytes at +0x2C..+0x2F are damage.
46. **V1 failures and leaks.**
    - All-zero bytes, bytes that are not "WWCO", and a damaged tag are Unbound; a newer layout is Foreign.
    - Bind refuses to write over bytes that are not "WWCO" (another tool may own them; `coop-tag clear` zeroes them), but writes over damaged and leaked tags.
    - A tag failing any of V2-V5 is Leaked even when it names another world: a stale tag binds nothing.
47. **Leak sources §5.2 doesn't list.** File select's Copy copies the whole `card_gamedata`, tag included, into another slot (`d_file_select.cpp:1817-1830`), which V2 catches. ExDataSave saves into the slot the player picks (`d_file_select.cpp:3626-3641`), so a second quest can fail V2 as well as V3.
48. **Gates and refusals.**
    - Bind and Scrub need STABLE, HEARTBEAT and IDLE (`gate.CanWriteIdle`): IDLE keeps the pause and save menus from copying `mReserve` to the card mid-write.
    - `PlanBind` and the hub refuse any file with `mClearCount` ≠ 0 (§2 non-goal; V3 rejects it anyway).
    - A remaining edge, like §5.2's documented one: while a fresh bind's nonce is pending, a same-name New Game in the same slot also passes V5. It is harmless, since both files are fresh.
49. **Scrub offers.** Failures of V2-V4 are offered at once. A V5-only failure naming the session's world is offered once a welcome says it is no pending fresh bind. A REFUSED welcome says nothing either way (its FreshBind is always false), so the replica learns fresh binds only from OK and UNBOUND welcomes: a host restarted on another build must not turn a pending fresh bind into a scrub offer. A V5-only failure naming another world is never offered, because only that world's host knows; `coop-tag clear` remains. The hub's NOTICE for a leaked tag naming another world says the same (item 133). This guards only the scrub: a bind to the session's world is still offered for such a file, and `--yes` accepts it, which replaces the tag as a scrub would. Its prompt says that the tag may be that world's fresh bind and that binding replaces it (item 133).
    - `coop-tag show` and `coop-tag clear` read the pending fresh binds from the world files on the machine. Without the tag's world file (a joiner), a V5-only failure is shown as "LEAKED, OR A PENDING FRESH BIND", with no advice to clear it, and clear warns that it may unbind the file.
50. **Citation fix:** `initdata_to_card` zeroes `dSv_reserve_c` at `d_save.cpp:2007-2010` (§5.2 said 2006-2010; 2006 is blank).

*Backups (§5.4)*

51. **The card locations come from Dolphin's source, not the decomp**, and need the owner's confirmation in M2 (§8.2). That covers the EXIDeviceType values (1 raw, 8 GCI folder), the default file and folder names, each slot's default device (item 135), how configured paths get a region code (a GCI folder path's trailing region directory becomes `USA` for Wind Waker, or `USA` is added, item 135), and which path Dolphin takes: a configured one over the default, and a GCI folder override over the GCI folder path (item 135).
    - Because a wrong guess means no backup, every card that exists is backed up, default and configured, except a GCI folder path that an override shadows, which Dolphin never uses. A GCI folder override's card is the folder it names, and a GCI folder path's the folder Dolphin makes of it. Folders near the setting (a path as written, and `USA` and the slot's `USA/Card A` or `USA/Card B` under the setting) are backed up too when they hold a Wind Waker save, in case a hand-edited setting meant one of them, and are never marked in use. The session start lists each card with the files copied from it and whether Dolphin's settings select it ("in use": the slot's device, from Dolphin.ini or Dolphin's default for a slot it doesn't set, is that kind of card, the card is not a default one that a configured path of its kind replaces, and it is not one of those folders tried near a configured GCI folder), and warns when they select none; `coop-world restore <backup>` marks them the same way, and the backup's `manifest.json` records them. GCI folders contribute Wind Waker's files (GZLE) only.
    - Restore refuses whenever the Dolphin process list is unavailable (Linux, macOS without cgo), and counts Dolphin's own tools as Dolphin.

*World file and hub (§4.4, §5.5)*

52. **Tombstones lower the world.** A tombstone also clears the bits from the image (the only operation that lowers a byte) and refuses them afterwards. On a MAX byte, any value containing a tombstoned bit is refused. Files that already have the bits keep them, as §4.4 says. The hub sends every tombstone (CTRL TOMBSTONE) with each bound peer's welcome.
53. **The default policy fails closed for OR_MASKED bytes.** Memory switches wait for the M1 latching grant (`world.GrantSwitches`) and story bits for M4's SyncMask: a bit accepted into a grow-only world stays forever. The policy filters proposals only; it is not saved.
54. **World file details.**
    - The journal replays: lines carry values and a CRC, and Load replays records above the file's seq, on top of replicas re-proposing. An old world file copied back by hand gets the later records replayed over it, so tombstones are the only way to take bits out.
    - A file of another version stops the load (`ErrVersion`), with no fallback past it, so an older build never overwrites a newer world.
    - The world file also keeps the pending fresh binds (V5), at most 16.
    - Every change bumps the one seq (image, tombstones, fresh binds), so GAINS seqs can skip.
    - Flushes wait at most 1 s; backups `.1`-`.5` are taken at session start and hourly.
    - Scoped stores are not saved: the game forgets them too.
55. **Scoped store lifetimes.** *Superseded in M5 by item 114:* a DAN store now lives while someone is in its stage and table, so the first and last bullets no longer hold, and `world.MaxDanStores` and `ErrScopesFull` are gone. The other bullets stand.
    - DAN lives while anyone is in that save table, keyed by (stage, table) for §6.3's stage-match rule. This is item 25's finding: `dSv_danBit_c::init` clears only on a table change (`d_save.cpp:1225-1235`). It is called from `dStage_stagInfoInit` (`d_stage.cpp:1669-1677`) and reset at `dSv_info_c::init` (`d_save.cpp:1392`) and at game start (`d_s_name.cpp:1489`).
    - ZONE is keyed by (stage, table, room) and dropped when the room empties.
    - KILL is keyed by room but kept alive by anyone in the stage, and sent on stage entry: zones are re-initialised at stage entry (`d_stage.cpp:124-126`, `dSv_zone_c::init` `d_save.cpp:1338-1342`).
    - ROOMEDGE is relayed, never stored.
    - A SCOPED is accepted only from a player whose PRESENCE is in that scope, so a replica sends PRESENCE first.
    - That bounds ZONE and KILL by where players are, but not DAN, which outlives its stage while a peer can name stage after stage in one table. Each table holds at most `world.MaxDanStores` (256) DAN stores, about twice the stages the game has (its audio's stage list, `JAIZelBasic::spot_dir_name`, `JAIZelScene.cpp:281-406`, names 115); past that a new one is refused with a NOTICE. M2 loads no M1 tables into a session (item 67), so unknown stage names can't be refused outright. The stores are indexed by stage and by table, so a PRESENCE costs what is near it, not every store.
56. **Hub choices §4.4 leaves open.**
    - A refused peer stays connected, because pose sync is unaffected. Policy refusals are counted silently; only tombstone refusals get a NOTICE, at most one per second.
    - The host is the loopback connection unless `Config.IsHost` says otherwise.
    - GAINS are relayed to every bound peer, sender included, so each sees what was accepted. A WORLD's delta goes to the other peers; the sender gets the merged image.
    - PRESENCE also reaches unbound peers, and a detach relays a "nowhere" PRESENCE.
    - A WORLD with a seq above the world's is logged as lost commits and merged.
57. **Far behind can't fire in M2.** The resume milestones (`l_checkData`, `d_com_inf_game.cpp:1304-1311`; §5.3 cited 1302-1320) are story bits, which the default policy keeps out of the world. The check is tested with the full policy and matters from M4.

*Replica (§4.2, §6.5)*

58. **Doc vs decomp: the Differ does not re-baseline on a table, stage or room change. The code follows the decomp.**
    - `getSave` and `putSave` only move the current table's bits between its live and saved copies (`d_save.cpp:1506-1515`), so the folded image sees the same bits either way. `initDan`, `clearRoomSwitch` and the zone init never touch the image.
    - `dMap_c::mapMoveAll` sets the visited-room bit on room entry outside sea- and FF1-type stages (`d_map.cpp:1662-1665`); a re-baseline on the room change would swallow it.
    - So the baseline is dropped only off the game (title, file select, no table), on another file, on a heartbeat regression or on a read error.
    - The tick that takes the new baseline reports no gains, so the replica follows it with its whole WORLD (`Edges.Rebased`). Otherwise what rose in the gap, such as a save state's chest or one opened across a failed read, would reach the world only at the next reconnect. The hub's merge only adds, so a WORLD from an older state is harmless.
    - A tick is skipped without Link, or while the STAG save table differs from `mDan.mStageNo`: `getSave` runs before `initDan` (`d_stage.cpp:1674-1675`).
    - Scoped words re-baseline per scope, as designed.
59. **Holds (§6.5).**
    - The boat lock has a third clause the doc omits: UNK_3E10 without UNK_3F80 (`d_a_ship.cpp:4224-4229`; ids at `d_save_event_flag.inc:476`, `:480`). It is a hold now.
    - The 0x2D10 hold uses `checkMasterSwordEquip`: `mSelectEquip[0]` is 0x39, 0x3A or 0x3E (`d_a_player.h:600-604`; `d_item_data.h:64-65`, `:69`).
60. **Smaller replica choices.**
    - A file with progress the world lacks, while the world adds nothing new ("ahead"), needs `--coop-merge`, as the hub denies such a bind without Merge.
    - Gains seen while offline are queued and sent after the reconnect WORLD.
    - The replica keeps a SCOPED only after an OK welcome granting its kind's feature, as the hub sends them, and only for the stage and table Link is in: every tick in game drops the others, and it keeps at most 258 (two stages' worth), so a hostile host can't grow it.
    - Labels come from `replica.DefaultLabel` or `Config.Label`.
    - The WriteGuard leaves out `mMaxLife` and `mMaxMagic` (they move only through their pending counters), the zone item bits, `mRoomNo` and the K1 timer bytes (M6 validates those per write). It covers the image merge masks, the DERIVED bytes, the tag, the live memBit, the dan and zone words, the three pending counters and `play.mSelectItem`.

*The `--coop` session (§5.3, §8.2)*

61. **Flags.**
    - `host` and `mp-local` take `--coop` (the only world on disk), `--coop=new` (found one) or `--coop=<world-id>`, plus `--coop-merge` and `--yes`. `join` takes `--coop` and `--yes`: the host picks the world and the merge.
    - A command line without `--coop` is parsed exactly as before.
    - §5.3's `host --coop new [--name]` became `--coop=new`. There is no `--name`: a world has no name field, and `coop-world list` shows the IDs.
    - The TUI passes no co-op flags; its panel and prompts are M8's.
62. **Founding (§5.3 differs).** §5.3 has the founding write the tag. In M2 the host founds `world.wwco` from its loaded, idle file, refusing a second quest and a Bound, Foreign or non-tag `dSv_reserve_c`. The host's own replica then binds the file through the usual prompt.
    - The file and the new world are the same, so the preflight says "same" and no `--coop-merge` is needed. This settles item 60's founding gap.
    - The tag is still written only after a yes (§5.1).
63. **Session start.** Every side, host and joiner, backs up the cards of both Dolphin user directories (USER_DIR_1/2). Without a backup, no tag is written that session: the bind is not asked and a scrub is declined, each with a reason.
64. **Prompts.**
    - Questions go one at a time on stdin, y/N; mp-local's two Dolphins share one prompter. A line typed before a question is dropped, and EOF or the end of the session means no. The reader starts with the session (without `--yes`) and stamps each line as it reads it; a question waits until the reader has had nothing to read for 50 ms, then takes only lines read after it was asked. So neither a line waiting in the terminal at session start nor extra lines typed for an earlier question answer it.
    - A bind is asked at most once per file (slot, clear count, name) per pipeline, reconnects included. After a no, a leaked tag still gets its scrub question.
65. **A host without `--coop`** refuses the co-op connection (RejectRole). The pipeline says "co-op is off for this session" once and waits for the session to end, so pose sync is unaffected. The co-op connection's dial failures are not logged: the pose pipelines report reachability.
66. **`coop-world block` (§4.4).**
    - The syntax is `coop-world block <world-id> (byte <off> <mask> | event <0xHHLL> | switch|chest|item <table> <no>) [--yes] [--force]`.
    - Event bits follow `onEventBit` (`d_save.cpp:1187-1189`). Chests, memory switches and pickups follow `onTbox`, `onSwitch` and `onItem` (`d_save.cpp:1072-1075`, `:1084-1087`, `:1112-1115`) at their `dSv_memBit_c` offsets (`d_save.h:684-686`).
    - `onItem` accepts 0..63 though `mItem` is a single u32, so items 32-63 land in `mVisitedRoom[0]`; block follows the game.
    - Register bytes (0x79-0xFF) are blocked with `byte`. Dan, zone and kill bits are not in the world image and can't be blocked.
    - Block edits the world file only while no session serves it: a running hub keeps the world in memory, and its next write would undo the edit. The host marks the world in use with `session.lock` in its directory, which records its pid, machine and process start time (item 136). A marker whose process has ended on this machine, or whose pid another process now has (where the marker records the process start time and this build can read it; see item 136), is stale: `coop-world list` says so, block goes on without `--force` and leaves the marker, and the next host start says the last session did not end cleanly. `--force` is needed only for a marker whose pid still runs, or one that can't be checked: written on another machine, naming no process, naming a process this user can't open (Windows), or read where this build has no process check.
    - In-session blocking is the hub's CTRL TOMBSTONE path (`hub.Hub.Tombstone`), which M8's panel will drive. Bound replicas learn a block at their next welcome.
    - `coop-world list` (read-only) was added beside block and restore.
67. **`stageTblHash` is 0 on both sides in M2:** no M1 tables are loaded into a session yet. M3 sends their identity.

*M3 in the `--coop` session (§6.3, §6.7, §8.2)*

68. **`--coop` writes from M3; `--coop-dry-run` is M2.**
    - With `--coop`, each coop-sync replica writes (`replica.Config.Apply`) with the session's stage tables. Their identity goes in CO_HELLO, and their memory-switch verdict is its policy, the host hub's own (`hub.SwitchPolicy`).
    - Its Breaker's reason is printed once, as a warning that ends "Pose sync goes on; type /coop on to write the world again." The pipeline keeps its connection, PRESENCE and view; pose sync runs apart from it.
    - `--coop-dry-run` builds M2's replica exactly as M2 did: no Apply, no tables, and a CO_HELLO that asks for the image only. A dry-run joiner doesn't load tables. A dry-run host still loads them and runs the M3 hub, which can serve writing joiners.
    - `--coop-dry-run`, `--coop-live-switches` and `--coop-iso <path>` are taken only once `--coop` is present. Without it, every command line parses exactly as before.
    - `--coop-live-switches` is the host's choice (`hub.FeatureLiveSwitches`), so `join` rejects it. The TUI still passes no co-op flags.
69. **Stage tables in a session.** Each side loads them once: the tables of `--coop-iso`, else those of the disc `patch` last recorded (`stagetables.Load`, mods/coop/stagetables). A failure is one warning, and that side runs without switch sync: for a `stageTblHash` of 0 on either side, or one that differs from the host's, the hub grants every feature but `hub.TableFeatures` (memory switches, live switches, puzzle, combat) and says in a NOTICE, once per reason, which side has none or that they differ; a side with none says why in its own log. Only a differing `classHash` is refused outright (features 0). This settles item 67. *Corrected (item 134):* the NOTICE first told a side without tables to run `patch` again or pass `--coop-iso`, though the hub can't know why it has none.
70. **The console.** §6.7 named no way to ask for an escape before M8's panel. A stdin line starting with `/` is a command:
    - The commands are `/refresh`, `/warp`, `/coop off|on`, `/status` and `/help`, each taking an optional player name for `mp-local`'s two Dolphins.
    - Commands are read by the bind prompts' one reader, and a command is never taken as an answer. So the reader now runs under `--yes` too (item 64 said it started only without it).
    - The session's first lines explain the console once.
    - The escapes go through `Replica.Escape`, which M8's panel can drive too, and are made on the replica's next tick.
71. **Refresh area, from the decomp.**
    - The new play scene copies `mNextStage` to `mCurStage` and only clears `mEnable` (`d_s_play.cpp:1277-1278`), so `mNextStage` still holds the request that loaded the stage.
    - Refresh writes that request back, then `mWipe` 0, then `mEnable` 1 last: the fields `dStage_nextStage_c::set` fills (`d_stage.cpp:26-31`). `dScnPly_Draw` then changes the scene behind the wipe, as for a loading zone (`d_s_play.cpp:291-308`).
    - It refuses a request that names another stage, and a restart point: -1..-3 take the save's restart data, not an entrance (`d_stage.cpp:1444-1454`).
72. **Request bounds (doc vs decomp: the design's "PRESENCE stage and point" needs bounds).** A point ≥ 0 is matched against the `(u8)angle.z` of the stage's PLYR entries, and no match is a JUT_ASSERT that halts the game (`d_stage.cpp:1455-1463`). The WriteGuard (TargetEscape) holds `mNextStage` to:
    - a point of 0..255;
    - a room of 0..63;
    - a layer of -1..11: -1 lets `getLayerNo` pick (`d_com_inf_game.cpp:185-190`), and 0..11 index the 12-entry `l_layerFuncTable_p` (`d_stage.cpp:2144-2150`);
    - a wipe of 0..11 (`l_wipeType` and its assert, `d_s_play.cpp:292-308`);
    - a name of NUL-terminated letters, digits and `_`.
    - The partner's point is safe, since its game just took it. Escapes ask with layer -1, as the game's own exits do (`d_stage.cpp:2362`).
73. **Another stage: the entrance's room is learned, never guessed (inferred).**
    - PRESENCE carries the stage and point but not the room that entrance starts in, which the game's exits ask for through SCLS (`d_stage.cpp:2362`). `dStage_Create` loads the requested room first (`d_stage.cpp:2221-2234`), then the PLYR entries are searched for the point. What a wrong room does depends on where the stage keeps them:
      - PLYR in `stage.dzs`: the entry found names Link's room, which overrides the request (`d_stage.cpp:1479-1480`), so a wrong room only loads a room for nothing first;
      - PLYR in the room files: only the requested room's are searched, and a point missing there is a JUT_ASSERT that halts the game (`d_stage.cpp:1455-1463`).
    - So the room is never guessed.
    - The replica learns the room from this game's own arrivals (exact): `mNextStage` after a load, while it names the current stage and point.
    - It also learns it from the partner's (inferred): the first settled PRESENCE after the partner changed stage.
    - *Corrected (doc vs decomp):* this item said the new play scene takes the new name from `mNextStage` before it clears `mpPlayerPtr`, so a first PRESENCE could carry the old table and room. The decomp has the old scene's delete clear `mpPlayerPtr` first (`dScnPly_Delete` ends in `dComIfGp_init`, `d_s_play.cpp:1136`; `d_com_inf_game.cpp:60-69`), before the new scene's create copies `mNextStage` (`d_s_play.cpp:1277-1278`). So a PRESENCE in game with the new name already has the new table and room.
    - The skip stays, relabelled as a guard against a snapshot read across those frames (`snap.Read` reads the name, the table and `mStayNo` apart): an arrival with the old table and room, and no PRESENCE without Link before it, is not learned from. It costs only an entrance whose room has the old one's number in the same table, which the warp then says it does not know.
    - Each map holds at most 512 entrances.
    - After the stage change the replica warps onto the partner once both are settled there, as a same-stage warp does (item 74), giving up after a minute or if the partner leaves.
74. **The same-stage warp, only into a loaded room.**
    - The warp writes `warp_x/y/z`, then `warp_seq` + 1 (warpLink's order; `warp_seq` in `mailbox_core.h`). `multiplayer_update` carries it out on the next frame Link exists and acknowledges it in `warp_ack` (`multiplayer_update` in `src/core/hooks.c`, `warp_frame` in `src/core/services.c`). The C side is unchanged.
    - The position is pose sync's: the puppet-sync client's one other remote (`partnerPos`). It is never the player's own, since `mp-local`'s twins hear each other, and there is none with two others.
    - The warp lands on that position itself, not beside it, so the two Links overlap until one walks a step. Landing beside the partner waits for M7, which can check this game's own collision at the spot first (item 132).
    - The warp waits while `warp_seq` ≠ `warp_ack`.
    - It goes only into a room whose collision this game has loaded, `mStatus[room].mpBgW` (`d_stage.h:865`; the BG actor sets and clears it, `d_a_bg.cpp:213`, `:241`). Otherwise it reloads by the partner's entrance first (inferred: an unloaded room has no floor).
75. **The escapes' allowlist and writes.**
    - TargetEscape covers `mNextStage`'s 14 bytes (item 72's rules), `warp_seq` and `warp_x/y/z`.
    - The brief named only `mNextStage` and `warp_seq`, but a `warp_seq` without its coordinates warps to wherever the last warp left them. `warp_ack` (the C side's) and `warp_force` (a diagnostic that warps every frame) stay out.
    - `M3Targets` stays the reconciler's; only a replica's own guard adds TargetEscape. The escapes need ARMED, STABLE, IDLE and HEARTBEAT, and are never made in a dry run or while co-op is off.
    - `FrameWriter.ApplyFields` checks every byte before the heartbeat wait (all or nothing) and writes `mEnable` and `warp_seq` last. It does not run the gates again after the wait; it reads again the game state they rest on (item 81): the heartbeat from the snapshot's on (no save state loaded since), the stage, `mNextStage.mEnable`, the loaded table and the loaded file (tag, slot, name, `mDateIPL`), and IDLE's inputs (the event mode and order count, the pause menu, the minigame, the refight). Any change drops the escape, writing nothing.
    - The escapes are not verified on the next tick: the game consumes them by clearing `mEnable` and acking `warp_seq`.
76. **Citation fix (§6.7):** `dComIfGp_setNextStage` runs `d_com_inf_game.cpp:666-690`, not 666-684. Besides `mNextStage` it records the last scene's speed and mode (`setLastSceneInfo`, with the Dragon shield, tinkle shield and soup bits) and, when asked, the start point (`:671-690`). A raw write keeps the previous ones, and their effect is on the escape's own arrival, not on a later death or reset (this item said so before; corrected):
    - Link's create reads the last scene mode as it enters: crawling in (modes 2, 3), getting up from a fall (4, 5), a demo (6), the ship's start, and the shields and soup from its high bits (`d_a_player_main.cpp:12280-12297`, `:12444-12550`, `:12601-12616`). So an escape's arrival replays the mode of the last stage change the game asked for: a `/refresh` after crawling out of a crawlspace crawls in again (inferred).
    - The start point (`dSv_restart_c::mStartCode`) is read only by the volcano and ice island actors (`d_a_obj_volcano.cpp:174`, `d_a_tag_volcano.cpp:37`, `:46`, `d_a_obj_iceisland.cpp:63`).
    - `/refresh` does not neutralise the mode: a write of the restart info would be a write outside TargetEscape, and M7's `SET_NEXT_STAGE` op calls `dComIfGp_setNextStage` itself.
77. **`/coop off` within a tick.**
    - It turns the Breaker off, and the FrameWriter checks the Breaker before every byte, so nothing is written after the tick it turns off on. `TestCoopSessionM3` checks this.
    - `/coop on` resets the Breaker, and the replica catches up at the next room change.
    - An escape asked while writes are off is refused at once, and so is one in a dry run.
78. **A WORLD before a joiner's welcome.**
    - A joiner learns the session's world from CO_WELCOME, which the hub sends before the WORLD (`hub.onUnboundHello`). M2's replica dropped a WORLD that came first, so an unbound joiner then waited for its next reconnect to get its preflight.
    - TCP never reorders a connection, but the integration tests do. M2's convergence test bound its joiner only because the leaked tag's scrub re-sent CO_HELLO.
    - The replica now keeps an early WORLD for the welcome, which takes it if it names the welcome's world.
79. **Acceptance and tests.**
    - A1-A6 and R1-R4 were not defined anywhere, so §8.2 defines them.
    - `TestCoopSessionConverges` runs with `--coop-dry-run`, as M2's test.
    - `TestCoopSessionM3` is M3's session test. It covers convergence on the M3 bytes with the fixture's latching switches, `/refresh`, both `/warp`s and a refusal, `/coop off|on`, a save-state storm, and §8.1's write audit over every byte both pipelines wrote.
    - The fake game has no BG actors, so the tests mark a room loaded through `mpBgW`.
80. **Left as they were.**
    - `mp-local`'s startup warp of Dolphin B (`MP_LOCAL_SHIFT_X/Y/Z`, through `warpLink`) predates co-op. It writes `warp_x/y/z` and `warp_seq` outside the WriteGuard, with or without `--coop`. *Corrected (item 130):* leaving it was wrong. It wrote A's position into B whatever stage either was in: with A in a dungeon and B at sea, that is the sea's centre square, where B voids out, as the owner's run is inferred to have done. It now never runs under `--coop`. Without it, it moves B from B's own position, and only when the two Links overlap in one stage and room with both games idle. A warp B's game does not take is withdrawn (item 131).
    - `patch` now prints one line about the stage tables after writing the ISO. The ISO is the same byte for byte.

*M3 review fixes*

81. **A save state loaded between the snapshot and the beat.** The tick's snapshot records the heartbeat, the loaded file (`bind.File`: tag, slot, clear count, name, `mDateIPL`) and the time it was read. The FrameWriter then follows the heartbeat from the snapshot's, not from its own first read:
    - its first read below the snapshot's, or further ahead than 2 frames plus 240 a second since the snapshot, is `ErrAborted`;
    - while it polls, a heartbeat that goes back, or jumps more than 2 frames at once, is `ErrAborted` (the game beats once a frame, 30 a second, `d_s_play.cpp:1352`);
    - after the beat, the recheck reads the file again; a save state of another file standing in the same stage and table, with its heartbeat where the game's would be, is caught there;
    - an escape also reads IDLE's inputs again (`recheckIdle`).
    - A batch the recheck drops writes nothing, and its verify on the next tick is skipped when the stage, the table or the file changed (the bytes then belong to another place).
82. **`/warp` waits for a partner still arriving.** The partner's position comes over pose sync, another connection than PRESENCE's, so the position heard as the partner arrives on Link's stage may still be its last stage's. A same-stage warp, the user's and a warp's second step, waits until the partner has been in its stage, in game with no change pending, at the same stage and point, for `replica.PartnerSettle` (a second; inferred). A room change in the stage keeps the partner settled; a reload of its stage does not. *Doc vs decomp:* the review read the partner's room as stale too; per item 73's correction the room is the new stage's, and only the position lags.
83. **A fatal connect error answers the escapes.** When `coopSync.run` gives up co-op for the session (the host refuses co-op), it turns the replica's writes off (the Breaker's "/coop on" hint is not printed), stops its ticks, and `/coop on` says it cannot help. `Replica.Run` answers the escape waiting for a tick, and any asked for later, with `replica.ErrStopped`, which is not `ErrNotNow`: the console does not say to try again.
84. **The audits work out room changes themselves.** `auditWrites` and `coAuditM3` record where the fake game is as each tick begins (stage name, `mDan.mStageNo`, `mStayNo`, `mNextStage.mEnable`, `mpPlayerPtr`). They accept a room-change write only on the first armed tick with the gates open since the place changed, or a later one when every such tick before it found the heartbeat stalled or the game moved on. They no longer trust `Report.RoomChange`.
85. **The boss bits wait in a boss stage.** Dungeon items 3 and 5 of the loaded table (boss defeated, boss demo seen) are read every frame in a boss stage (§3's table). In a stage whose STAG type is BOSS (`(mStageTypeAndSchbit >> 16) & 7 == 3`, `d_stage.h:20`, `:1031-1033`), in the FF table (Helmaroc King, `d_meter.cpp:3684`; inferred), or when the STAG can't be read, the planner holds them (`bossHold`), and the WriteGuard refuses them at the live memBit. They are written once Link leaves: live in another stage of the same table, else to the saved slot. A dry run plans as before. The boss-dead relay (`mods/coop/replica/bosskill.go`, `bossKillRows`) asks the REL to kill the receiver's own boss only in each row's arena: `M_DragB` (Gohma), `kinBOSS` (Kalle Demos), `SirenB` (Gohdan), `kazeB` (Molgera), `M_DaiB` (Jalhalla) and, since batch F, `M2tower` (Helmaroc King, `helmaroc`; `design/relay-helmaroc.md`). Gohdan is opt-in (`--coop-boss-kill`) until its live checks pass; Helmaroc King joined the default on 2026-10-24 after its live checks and a real hammer fight (docs/TESTING.md "Batch F"), and Molgera and Jalhalla the same day after their checks with a real Hookshot pull and a real light freeze and spike throw ("Batch E"); the default is `gohma,kalle,molgera,jalhalla,helmaroc`. In the FF table the hold covers every stage, but only `M2tower` asks: in `Mjtower`, `MajyuE`, `majroom`, `ma2room`, `ma3room` and `M2ganon` the bits stay held until Link leaves for the sea. Dungeon item 4 (`STAGE_LIFE`, the heart container taken) is held in the same stages under its own hold (`bossLifeBit`, `bossLifeHold`), since 2026-10-25 (the ww-online review, idea 4). No boss stage polls it (its one reader is `Bitem`'s create, `d_a_boss_item.cpp:33`), but each game's own boss drops a container at its death without testing it (`fopAcM_createItemForBoss`, `f_op_actor_mng.cpp:956-964`; Gohma `d_a_btd.cpp:1082`), the relayed death included. Had the partner's bit landed mid-fight, the derive would have credited the partner's container (+4) and the receiver's own pickup added 4 more, which add-only max life keeps. Held, it clears when the receiver takes its own container, or lands once Link leaves without it, and `Bitem` then drops none on the way back. The WriteGuard refuses it at the live memBit in a boss stage, as it does the boss bits. Because the hold reuses the boss bits' stage test, it also covers every FF-table stage, every BOSS-type stage and a STAG that can't be read, not only the arenas whose boss drops a container. In FF that is a delay, not a loss: when the partner takes Helmaroc King's container, a receiver in `Mjtower`, `MajyuE`, `majroom`, `ma2room`, `ma3room` or `M2ganon` gets its +4 only once Link reaches the sea, where FF's `STAGE_LIFE` goes to its saved slot (the container taken on the sea also sets FF's bit there, `d_item.cpp:596-599`) and the derive reads it from that cold table. `TestReconcileM4BossContainerOnce` runs the hold and the derive together through the reconciler: the partner's bit stays out in `M_DragB`, the receiver's own pickup counts once, and leaving without it, into the dungeon or by the warp flower to the sea (the saved slot), gives +4 once.

*M4: story bits and registers (§6.5)*

86. **D8 is 31 bits in GZLE01, not 34 (doc vs decomp; decomp followed).**
    - `scripts/gen_event_masks.py` (standard library; `--check` compares its output with the committed `mods/coop/class/syncmask_gen.go`) finds the design's 50 `offEventBit` call sites in 10 files outside `d_save.cpp`. 46 of them, in 9 files, are compiled into GZLE01; the script works that out from the `#if`/`VERSION_SELECT` guards, the splits and configure.py's match status.
    - 0x0E08, 0x1110 and 0x1108 are cleared only by demo-build code: `d_a_npc_rsh1.cpp:1703`, `:1709` and `:1715` under `#if VERSION == VERSION_DEMO` (`:1690-1718`), and `d_a_obj_YLzou.cpp:482` (`:480-485`). Both files match GZLE01, so those three bits sync.
    - LocalOnly is the 31 D8 bits plus 0x0A02, 0x2A08 and 0x3B20: 34 bits. 0x3820 is in both lists, still cleared in GZLE01 at `d_a_obj_YLzou.cpp:143`. 0x3F40 (the ending) was added later: 35 bits, SyncMask 448.
87. **0x2D01 stays LocalOnly (a gap).** Its only clear is the debug map-select toggle (`d_s_menu.cpp:244`), but §6.5 keeps it out, and so does the generator.
    - `getLayerNo` reads it for the Windfall sea room and M2tower (`d_com_inf_game.cpp:202`, `:252`). So a partner's milestone does not change the receiver's Windfall or tower layers until the receiver's own game sets the bit.
    - The story task's cross-check: LocalOnly and the four refight bits are ww-online's 37-bit list plus 0x2D01 (and 0x3F40, added with the GT slice).
88. **Holds as built.**
    - MET_KORL is itself held until the receiver has it (`UntilLocal`), so sync never applies it. It stays in the world and only releases 0x0801, 0x0808 and 0x2401, and since the overworld slice also Outset's layer bits 0x0101, 0x0E20 and 0x0520.
    - RODE_KORL (0x2A08) is LocalOnly, so no world ever holds it, and far behind (§5.3; `l_checkData`, `d_com_inf_game.cpp:1304-1311`) rests on MET_KORL, 0x0801, 0x0808 and 0x2401. The design's list of five is corrected.
    - MET_KORL is set as the MEETSHISHIOH event starts (`d_event_manager.cpp:599-600`). That it comes before boarding on every path is inferred.
    - A third boat hold, beyond §6.5's two: 0x3E10 waits for 0x3F80 (`d_a_ship.cpp:4227`, `:4229`; item 59). 0x2D10 waits for a Master Sword equipped (`checkMasterSwordEquip`, `d_a_player.h:600-604`), and HYRULE_COURTYARD_CUTSCENE 0x3804 for ZELDA_AWAKENED 0x2D02. A bit that waits for a partner bit may arrive in the same write as it.
    - 0x1820 waits for 0x2D01, the receiver's own rescue (`UntilLocal`; 0x2D01 is LocalOnly, item 87, so only the receiver's game ever sets it).
    - The hub's far-behind and divergent NOTICEs list the held bits by name, as many as fit in `wire.MaxNoticeLen` (255 bytes), then "and N more".
89. **Registers on the transition tick (changed from §3's "on load"; inferred).**
    - The allowlisted registers travel in the story batch, and the receiver writes them on the same transition tick as the story bits. The WriteGuard classes them with the story bits, and the planner's `timingOf` gives both `OnTransition`.
    - Each change is still an edge: an OR register's new bits, or a MAX register's higher value. The decomp doesn't show when registers are read.
    - *Citation fix:* `getEventReg` is `d_save.cpp:1207-1210`, and `setEventReg` `:1201-1205`. `:1196-1198` is `isEventBit`.
90. **The sender's pace (`replica.Differ.Pace`, `differ_pace.go`).**
    - It is on only for a writing replica (`Config.Apply`). A dry run is M2's, unpaced: every rising edge is sent on the tick it is found, as FuzzDiffer and the M2/M3 tests expect.
    - The pace's clock counts ticks (`TickEvery` each). The replica's ticker never runs faster and skips the ticks a slow one overran, so a batch waits at least `story.BatchIdle` (1 s) of real time.
    - The WORLD a writing replica sends when armed, and on every reconnect, goes through `Differ.Withhold`, which takes out what the batch holds. The waiting bits then follow as gains in their time. `Differ.Drop` resets the pace with the baseline, and the next tick's WORLD carries what waited.
    - Gap (accepted): an edge found on the very tick a WORLD is built isn't the pace's yet, so it rides in that WORLD unbatched. It costs at most one tick's bits arriving apart from their batch (`replica.go`).
    - The inventory, collection flags and capacities wait for the sender to be IDLE. That assumes a chest's demo item grants its flag as it is deleted, before its event ends (`daDitem_c::Delete`, `d_a_demo_item.cpp:438-441`; inferred). It also covers the Ganon's Tower refight's item swap (`d_s_play.cpp:1394-1400`; `d_com_inf_game.cpp:1440`, swapped back at `:1579`).
91. **Cutscene-trigger switches are coupled only in part.**
    - A memory switch waits with the batch, and one that rises with no batch waiting starts one. `setStartDemo` sets the EVNT spawn switch as its event starts and skips the event when the switch is already set (`d_event_manager.cpp:42-47`). `daTag_Event` sets its swbit on demo accept (`d_a_tag_event.cpp:222-228`, `:386-391`).
    - Not covered:
      - (a) Stage-temp (dan) and zone switches go as SCOPED words, which the batch never sees.
      - (b) On the receiver, the current table's switches apply at a room change, another table's at once, but story bits only at a stage transition. So a coupled switch can land first, and the receiver then skips that event. *Closed for the two synced start-event switches* (GanonK sw30, GTower sw31): the receiver holds each until it has its bit and writes it on the transition that writes the bit (§6.2, "Start events' spawn switches"; `spawnSwitch`). Other coupled switches stay open.
      - (c) *Closed (D2(9)):* a switch set on a tick that already reads IDLE, with no batch waiting, used to go at once. A stage event's spawn switch is set on such a tick, the one its event starts on, so live GT's sw30 (`kugutu_ganon`) reached the partner while A's intro still held 0x3B02, and the partner loaded layer 8 with the intro skipped. Such a switch now starts a batch and goes after the quiet second, so an event starting on its tick holds it with that event's bits (`story.Batcher`), but only up to the next quiet IDLE second: the reruns on #175 saw sw30 go about 4 s into A's intro and 0x3B02 only after it ended. For the synced spawn switches the receiver's hold, (b), is the guarantee. The cost: a switch now reaches the world after the sender's next quiet second, not on the tick it rose, so one followed by a camera cut, a chest, a stage load or an open pause menu waits until that ends plus `BatchIdle`. Receivers apply latching switches at room changes, and puzzles' dan, zone and room switches go SCOPED, outside the batch. Still open: a switch rising on the very tick an earlier batch falls due rides out with it; spawn switches cannot hit this, since a stage load restarts the IDLE run.
    - Fixing (b) means applying the coupled switches at the transition too. M7's getSave shim applies both at the stage load.
92. **One policy for the session.**
    - `hub.DefaultPolicy()` is `story.Policy(world.DefaultPolicy())`: it opens the SyncMask's story bits. `world.DefaultPolicy()` still fails closed on them, which keeps the world package, its tools and their tests unchanged.
    - A session's policy is `hub.SessionPolicy(verdict)` = `SwitchPolicy(DefaultPolicy(), verdict)`, and the hub, each writing replica (`newSync`) and the founding world use it.
    - *Bug fixed (latent since M3):* `foundFrom` merged the host's file into a world under `world.DefaultPolicy`, so the latching switches and, from M4, the story bits it held were left out of the founded world. The host's own file then looked ahead of the world at its bind. `foundFrom` now calls `SetPolicy` with the session's policy before the merge (`TestFoundFromSessionPolicy`).
    - Only the story bytes 0x00-0x41 are gated by a hub feature (`FeatureStory`). The inventory, collection flags, capacities and registers have been image bytes since M2: M2/M3 replicas and their tests already share them, and `FeatureInventory` only tells a replica to write them.
    - A bind is judged again when its request arrives, so a file can be refused (divergent or far behind) if the world grew after its WORLD (`TestBindJudgedAgain`).
93. **Citation fixes.** `dKy_checkEventNightStop`'s use in `getLayerNo` is `d_com_inf_game.cpp:189`, not `:188`. `checkMasterSwordEquip` is `d_a_player.h:600-604`.

*M4: small keys, max life and max magic (§6.4, §3)*

94. **Keys: `extra` reconciles "duplicates self-correct" with "a key from an unknown source is never removed".**
    - The target is `min(max(keys(t) + extra, 0), 99)`. `extra` counts the keys from sources the table doesn't know: every rise of the count, while the Deriver tracks the file, that no known source explains. It never goes down while the file is tracked. The clamp comes last, so a door takes its key from the whole: once `keys(t)` is 0 or below, a door (the player's or the partner's) spends an extra key, and the derive never gives it back.
    - `extra` starts at 0 on the Deriver's first tick of a file, and again after every reset: a failed snapshot read, a save state (heartbeat regression), the title or file select, a restart of the tool. The table is derived only while its M1 invariant passes, so every source its dungeon has is a known saved bit. A count above `keys(t)` then is a door's key not yet taken (a partner's door whose switch reached the file before the decrement could, or a door an M3 session synced with no key derive) or a duplicate left in the file, and the derive takes it out once the table settles. So "a key from an unknown source is never removed" holds only while the file is tracked: a cheat's key goes at the next reset.
    - *Fixed in review:* the first build measured `extra` on the first tick as the keys held beyond `max(keys(t), 0)`, and clamped `keys(t)` before adding it. A reset between a partner's door switch landing and its decrement turned that key into `extra` for good, and with `keys(t)` at 0 each door spent was refilled: one key opened every door of the table (`TestResetTakesTheDoorsKey`, `TestUnknownKey`, `TestM3FileDoorKey`).
    - A rise counts as a duplicate, and is taken back, only while a source in the current stage had its flag set during this visit. That is the stale actor of §6.7 (`d_a_tbox.cpp:447-455`, `d_a_item.cpp:227-237`).
    - This relies on the Deriver seeing a file's first tick before the replica's first write for it. The Deriver runs every tick, armed or not.
95. **Where a key write goes.**
    - The key counter is applied only while the stage shows the key HUD (`dMeter_keyMove`, `d_meter.cpp:5242-5257`). It takes a negative count and stops at 0.
    - Without the HUD the count would stay pending until the next HUD stage and land on that stage's table. Only `itemInit` at file select clears it (`d_com_inf_game.cpp:72-84`; `d_s_name.cpp:1253`, `:1374`). So the counter is never written without the HUD.
    - For the loaded table without the HUD, only the live `mKeyNum` is written. §3.1's general rule would write the live byte and the saved one, but `putSave` copies the live memBit over the saved slot (`d_save.cpp:1511-1514`).
    - The game moves the same counter (+1 `d_item.cpp:666`, -1 with the door's switch `d_door.cpp:439`). So every derive write carries the bytes it expects, the FrameWriter compares and sets, and a mismatch is `Dropped` and planned again.
96. **Max life is derived only when the whole M1 heart table passes.**
    - A real disc that misses the 44-piece / 6-container totals turns it off entirely; the startup log and `/status` say why. Until item 137 that was every retail disc (45 pieces and 8 containers on the owner's). With item 137 the owner's disc gives exactly 44 and 6 and its heart table passes, so max life is derived there; the owner's own run with that build has yet to confirm it. The rule stays strict: a disc one source short still turns max life off.
    - Rewards no save bit records (the three minigames, and those whose grant isn't decompiled) can't be counted. A player's own such pieces are kept by max life's own `extra`, which, unlike the keys' (item 94), is measured on the file's first tick: max life is add-only, so an over-measured `extra` can only fail to raise it, never give a heart twice.
    - A positive max-life counter also refills current life to the new maximum's whole hearts (`dMeter_LifeMove`, `d_meter.cpp:1426-1427`). So a partner's heart piece fully heals the receiver. That is the game's own pickup effect on per-player `mLife`, accepted and documented.
    - The 1 s settle and the in-flight rule apply to life and magic as to keys, so a write can't race a pickup still in progress.
97. **Max magic: event 0x3180 is not shown by the decomp (doc vs decomp).**
    - The Great Fairy actor isn't decompiled (`daBigelf_c::getEventFlag`, `d_a_bigelf.cpp:199-202`). 0x3180's only decompiled reader is a list of seven bits in `d_a_npc_photo.cpp:2190-2197`.
    - The upgrade is item 0xB2, `item_func_max_mp_up1`: +32 (`d_item.cpp:1396-1398`), capped at 0x20 (`d_meter.cpp:4069-4076`). So max magic is 0, 16 or 32.
    - The live derive follows §3 and reads 0x3180 (inferred); the SyncMask syncs the bit, so a partner's upgrade reaches the receiver. The offline save-merge doesn't read it: it takes max(a, b, 16 if the merged Deku Leaf flag is set). The two derives agree whenever a file with 32 has the bit.
    - The max-magic counter does not fill current magic: the meter only clamps `mMagic` to the maximum (`d_meter.cpp:4105-4107`). `item_func_deku_leaf`'s own +16 of current magic (`d_item.cpp:814`) isn't mirrored, because `mMagic` is per player, so a receiver whose meter came from co-op starts it empty.
    - Each Deku Leaf pickup adds 16 again (`d_item.cpp:813`), so taking the leaf twice can reach 32 without the upgrade. The derive is add-only, so that is kept, and logged.
    - The inventory package mirrors none of `item_func_deku_leaf`'s, `item_func_kakera_heart`'s, `item_func_utuwa_heart`'s or `item_func_max_mp_up1`'s counter side effects: the derive package owns those counters, and both would grant twice.
98. **The derive path.**
    - The derive writes (keys, life, magic, and the inventory's slots, equipment and buttons) are a second heartbeat batch made after the merge batch (`FrameWriter.ApplyDerived`). They are skipped when the merge batch stalled or aborted, because the slots follow the flags that batch carries.
    - The WriteGuard enforces the paths itself: `ApplyContext` writes merging bytes, `ApplyDerived` derived bytes, counters and buttons, `ApplyFields` the escapes, and bind the tag. A derived byte sent any other way is refused.
    - A counter is written whole, only while it reads 0, and only while IDLE. The HUD's drain is watched; one that never drains turns writes off through the Breaker.
    - The Deriver plans whatever the hub grants. A derive whose feature isn't granted is dropped, and planned again a settle time later, and the log says once why it is not written.
    - When the HUD takes a counter follows the decomp: max life only outside minigame 6 (Orca), max magic only while `mpPlayerPtr[1]` is null (`d_meter.cpp:815-816`, `:1561-1571`, `:4015-4040`). That the counters don't drain while the pause menu is open is inferred. The seagull (`m_hyoi_kamome`) case is not modelled, nor are the minigame, partner and pause cases in the fake game's HUD, so the tests drive those with heartbeat-only frames.

*M4: inventory (§6.6)*

99. **Two bytes merge fewer bits than the class table says (decomp followed).**
    - `mGetItem[0..2]` are classed OR 0xFF, but their bits above bit 0 record which bottle contents a player ever held (`onBottleItem`, `d_save.cpp:224-282`). The game reads them: `checkItemGet`'s first-get checks (`d_item.cpp:539-545`, `:2349-2392`), Beedle's shop (`d_a_npc_bs1.cpp:838`) and Grandma's soup dialogue (`d_a_npc_ba1.cpp:678`).
    - `mCollect[4]` bit 1 is whether the Hero's Charm is worn. The collection menu turns it on and off (`d_menu_collect.cpp:1283`, `:1286`), and the HUD (`d_meter.cpp:3669`, `:3682`) and Link (`d_a_player_main.cpp:5675`) read it.
    - The plan takes only bit 0 of those bytes (owned: `item_func_heros_omamori`, `d_item.cpp:894-896`, for the charm). The world still merges the other bits and the receiver never writes them. Classing these bytes ORMasked 0x01 in `mods/coop/class` would say so in the table too; it was left for the class table's owner. Item 112 does it, as OR 0x01.
100. **Bomb bag and ammo (doc vs decomp).**
    - `item_func_bomb_bag` sets both the bomb count and the max to 30 (`d_item.cpp:792-797`). The upgrades are `d_item.cpp:1368-1390`, and they fill the ammo to 60/99 too. The plan doesn't copy the upgrades' fill, because ammo is per player.
    - The plan's first-grant fill of 30 (a first bow or bomb bag) would go through the HUD's ammo counters, `mItemArrowNumCount` 0x803CA788 and `mItemBombNumCount` 0x803CA78C. Those are no WriteGuard target before M7, so the fill is logged once and not written, and a first bow arrives with no arrows.
    - `RuleAmmoBusy` holds a first bow or bomb bag grant while its ammo counter is not 0, so the fill would keep IDLE's rule that a counter is written only while it reads 0.
101. **Equipment waits for the stage-entry window** (the question §6.6 left open). That is the first IDLE tick after a stage load: Link is created with nothing in hand (`d_a_player_main.cpp:12179`), the blade is built when Link draws the sword (`:3847-3850`), and the game rebuilds it when it changes the sword itself (`d_a_player_dproc.inc:274-281`).
    - Leftover risk: a player who draws the sword in the 400 ms or more before the window opens.
    - A session started mid-stage equips at the next load. The `mCollect` flags themselves land live.
102. **The button refresh is upgrade-only.**
    - A button is refreshed only when it shows nothing or a lesser variant of its slot's item. That also corrects the game's own stale button after the deluxe picto box (`item_func_camera2`, `d_item.cpp:727-731`, which does no refresh), the same as `dComIfGp_setSelectItem` would do.
    - Buttons bound to bottles or bags are never touched.
    - *Doc vs decomp:* `play.mSelectItem[i]` is set to 0 at init (`d_com_inf_game.cpp:115`), not 0xFF. A button that still reads 0 is left to the game's own `dComIfGp_setSelectItem`.
103. **Caps and context rules.**
    - The world's capacities are capped at the most the game ever sets before the MAX: wallet 2, arrows and bombs 99. `getRupeeMax` treats 2 and anything higher as 5000 (`d_save.h:21-30`). The receiver's own higher value is never lowered.
    - The GTower rule matches the exact stage name "GTower", because `dComIfGp_getStartStageName` is `mCurStage` (`d_com_inf_game.h:283`). Other Ganon stages are not covered.
    - The FF1 rule fails closed when the STAG can't be read, but only while 0x0801 is set. It also holds in every Forsaken Fortress stage by name until the receiver's own MET_KORL (§6.6).
    - During a Ganon's Tower refight `revPlayerRecollectionData` restores collect, equipment and capacities from its snapshot (`d_com_inf_game.cpp:1579+`). No grant is lost only because IDLE stays closed while the refight stage number is nonzero.
104. **Compare-and-set.** The FrameWriter's merge path only ORs and MAXes. The slot, equipment and button writes go through `ApplyDerived` with the byte the plan read, and are made only while a fresh read still equals it. Otherwise they could overwrite a byte the game changed since the plan.

*M4 in the `--coop` session (§5.3, §8.2)*

105. **The wiring.**
    - With `--coop`, `newSync` gives each writing replica the session's stage tables (`Config.Tables`, which the Deriver needs) and the session's policy. The replica asks for `hub.M4Features` with M3's.
    - The startup log names M4's classes: `shared progression this session: …`. It names the key tables derived, and warns for each table whose M1 invariant fails, for a failing heart table, and when there are no tables.
    - The armed line says what is written to that game, by the features granted. A welcome that grants fewer M4 classes than asked says which are off and why, once per change: the stage tables (the host's NOTICE gives the reason) or the host.
    - `/status` adds the features granted, the held story bits and each key table's state (`derive.Status`).
    - The TUI still passes no co-op flags. Without `--coop`, `host`, `join`, `mp-local` and `patch` are unchanged.
106. **`--coop-dry-run` stays M2's dry run.** It has no tables, M2's policy, asks for the image only, and its Differ is unpaced.
    - So it plans no story bit, key or max heart. It logs the inventory's flags and slots, the capacities and max magic as `dry-run: would …`.
    - A world a writing host founds holds story bits that a dry-run replica's policy doesn't show. A dry run therefore never proposes or plans them.
107. **Far behind: copy the host's save (corrects §5.3).**
    - §5.3 recommended `coop-world export/import`, which was never built. The joiner's preflight now says `the world is far ahead of this file: copy the host's save in, or the host can run with --coop-merge to union them; pose sync goes on`, and the hub's refusal says the same.
    - The game keeps all three file-select files in one card file (`card_savedata.gamedata[3]`, `m_Do_MemCardRWmng.h:39`, in the one card file "gczelda" the game opens or creates, `m_Do_MemCard.cpp:116`, `:134`), and the Memory Card Manager moves that whole file. So the log goes on to say that the copy replaces all three of the joiner's files with the host's (export them first; the session's card backup holds them too), and that the copied file is the host's own, already bound to the world. It also says that the host should first save in game and load its file from the card: a copy of a file bound fresh and not loaded back since clears the host's pending fresh bind (item 133). The hub's NOTICE says `it replaces all three of your files`. No tool copies a single slot.
    - With `--coop-merge` on the host the bind is asked, and the milestones the file lacks stay held.
108. **Offline `save-diff` and `save-merge` (`wwapi/savefile` and `mods/coop/savemerge`, were `internal/coop/savefile`; `coop_save_cmds.go`, now in `mods/coop/cli`).**
    - They work on GCI files, or a raw card image read only. A slot is numbered 0-2 (`mDataNum`, top to bottom on the file select), printed "slot n (file n+1)". Second-quest slots are refused.
    - Merge: max life is a's value plus the pieces and containers the merge newly set, capped at 0x50 (`d_meter.cpp:1432`). The absolute 12 + pieces + 4·containers would ignore a's unrecorded rewards. A reward counts by its record as the live derive reads it (`tables.RecordOf`): an event bit, or a letter register reading READ. The letters merge by MAX, and the mailbox hands over only a stocked letter (`d_a_obj_toripost.cpp:270`) while nothing stocks a read one (`d_letter.cpp:53-62`), so a letter merged in as read brings its piece or none ever comes. *Fixed in review:* the first build counted event-bit rewards only, so a letter merged in as read lost its piece for good. Max magic is item 97's.
    - Keys, where the merge changed a source or door, are the live derive's target over the merged flags with no `extra`, `derive.KeyTarget` (`min(max(keys(t), 0), 99)`): what the live derive brings a's file to once those flags land, its baseline taking no `extra` (item 94). Keys a holds beyond its own flags are a door's key not yet taken or a duplicate, and go; the note says how many. *Fixed in review:* the first build kept them as `max(0, a's keys − keys(t)a)` with `keys(t)a` unclamped, so a file with more doors open than sources (§6.4's exploit, or a merge's own output) gained a key from nowhere, and its cap was 0xFF.
    - Without `--iso` every DERIVED byte and the memory switches stay a's, and a printed warning says so: a merged file may then hold, say, the hookshot's get-item flag with an empty slot.
    - Holds are judged against a's image after deriving, so a Master Sword derived from b's `mCollect` releases 0x2D10. The resume milestones print only a warning.
    - Kept from a: the header block (banner, icon, comment) and the photos. The other two slots are written as the game would have repaired them on load, the decomp's slot-1 quirk included (`m_Do_MemCardRWmng.cpp:139`).
    - Store-side facts reproduced: the game's load (Restore) never checks the outer `card_savedata` checksum. Store bumps `save_count`, sets `data_version` to 0, zeroes the padding and writes the same data to both copies (`m_Do_MemCardRWmng.cpp:32-106`).
    - a's binding tag (`mergedTag`): a bound file's progress enters its world at its next session unjudged (§5.5), so the tag stays only where that brings the world nothing it wouldn't get anyway: b adds no OR, OR_MASKED or MAX byte, or b's slot is bound to the same world (b's own next session brings the same), which the warning says. Otherwise the tag is cleared, the warning says why, and the merged file joins as an unbound file the host judges like any other: a subset binds after a prompt, and progress the world lacks or a far-behind file needs `--coop-merge`. A tag that binds a to no world (leaked, or a newer layout's) is kept as it is and named for what it is. Each slot's tag is classified by V1-V5 (`bind.Classify`; mDataNum is the slot, mDateIPL the card copy's stamp). *Fixed in review:* the first build kept a's tag and warned that b's progress "needs the host's --coop-merge", which no host checks for a bound file, and called a leaked tag a binding.
    - save-merge refuses an output that exists or names an input, writes it exclusively, and reads it back to check its checksums before it reports success.
109. **Left as they were.**
    - The replica's join preflight judges against the world it is sent, which the hub strips to its features (a dry run's has no story bits), and under its own policy. It can therefore offer a bind the hub then refuses with a NOTICE; the hub's judgement is the one that counts (item 111).
    - The planner's holds (`replica.Holds`) duplicate `class.Holds`; `save-merge` and `/status` print the planner's reasons.
    - The Deriver's first-tick notes repeat the startup log's off reasons, once per pipeline.
    - `coopStageTables.policy()` is M3's policy, memory switches without story bits. `newSync` and the world's founding use `hub.SessionPolicy` (`coopSession.policy`); `save-merge --iso` starts from `policy()` and adds the story bits itself, and the M3 session test plans with it. *Fixed in review:* `coop_tables_runtime.go`'s doc comment said the replica policy was `st.policy()`.
    - ww-online could not be read this round (403), so nothing was cross-checked against it.
110. **Acceptance and tests.**
    - `TestCoopSessionM4` extends M3's session test: two FakeGames, a real `network.Server` with the CoopHub on loopback, and two writing pipelines with the vanilla fixture's tables. Its scripted play covers:
      - the same heart piece taken twice, the Deku Leaf, the bow with its quiver, a register;
      - a cutscene's two story bits that leave together and reach B only at its next stage change, with MET_KORL's holds;
      - in DRC, the same key twice, a partner's key counted in, a door whose key goes only at B's next room change (no phantom lock), and a key spent twice on one door counted once.
    - Then 400 random steps with reordered, repeated and dropped messages, reconnects and a host restart from the world file.
    - It checks:
      - convergence on every M4 class, with keys, max life and max magic matching the formulas over each game's own flags;
      - that no capacity, max life or max magic ever fell;
      - `/status`;
      - the write audit: story bits only on a transition tick, derive targets only through the derive path, no PER_PLAYER, NEVER or TAG byte, and no write while a gate is closed.
    - `TestCoopSessionM3`'s audit now accepts M4 targets only under M4's rules (`coAuditM4Write`), and counts the derive writes against the replica's own count.
    - `TestReplicaPace`, `TestWrittenClasses`, `TestSayUngranted`, `TestFoundFromSessionPolicy`, `TestLogCoopProgression`, `TestProgressionStatus` and `derive.TestStatus` cover the wiring.
    - Random play stays clear of §6.4's documented exploit: no key is spent on another door while a world door waits.
    - P1-P9 are M4's owner checks (§8.2).

*M4 review fixes*

111. **A bind is judged on everything the world takes, whatever the session grants (§5.3).**
    - The first build judged a bind under the joiner's grant: without `FeatureStory` (a `--coop-dry-run` join asks for the image alone) neither the file's story bits nor the world's milestones counted, and without `FeatureSwitches` (stage tables not the host's) its memory switches didn't either, as since M3. But a bind outlasts the session: `onBoundHello` never judges a bound file again, and a later session that grants more merges its whole WORLD and writes the world's story into it. So a fresh Outset file bound by a dry run joined a later session far behind, with no `--coop-merge`.
    - `judge` now counts every byte the world's policy takes, and the milestones and holds, for every bind (`TestStoryBind`'s dry-run cases, `TestPreflightCountsSwitches`).
    - Not fixed: files bound under M2 or M3 were judged when no world held story bits, and a world doesn't record what a bind was judged on. Their first M4 session unions their story bits with the world's, holds aside (§5.3).
112. **Bottle history and the charm worn stay local.**
    - The class table now matches `inventory.FlagMask`: `mGetItem[0..2]` and `mCollect[4]` are OR with mask 0x01, not 0xFF. The world image, the hub's bind judgement (divergent or a subset), the join preflight and save-diff now carry and compare bit 0 of them only.
    - Bits 1-7 of `mGetItem[0..2]` record which bottle contents the player has held (`onBottleItem`, `d_save.cpp:224-282`). Bit 0 is the telescope, sail and Wind Waker. `mCollect[4]` bit 0 is the Hero's Charm owned (`item_func_heros_omamori`, `d_item.cpp:895-898`), and bit 1 is whether it is worn, which the collection menu turns on and off (`d_menu_collect.cpp:1283-1286`).
    - Why: under OR 0xFF a player who had held a potion the partner hadn't looked divergent at bind, and needed `--coop-merge`. The world kept each player's bottle history and charm worn, though no receiver wrote them (item 99), and save-diff listed them as OR differences. save-merge already took bit 0 only, through the planner's `inventory.FlagMask`, so its output does not change.
    - save-diff now lists a flag bit outside its byte's class mask under PER_PLAYER. coop-watch names `mGetItem` and `mCollect[0..4]` bits by their array index (`mGetItem[5] bit 0`), since the table now splits both arrays.
    - The class table's hash changes with it, so a peer on an older build is refused with NOTICE SCHEMA. A world file written before keeps those bits until it is loaded: its decode strips them and counts them, like any bit outside the table.

*M5: room puzzles (§4.4, §6.2, §6.3)*

113. **The latching verdict, on both sides.**
    - `synctables.SyncedScopedSwitch(t, table, stage, room, no)` (mods/coop/synctables) judges a dan switch per save table, and a zone or room switch per (stage, room) with a room of 0..63.
    - It is false for a memory switch; for -1 and 0xFF (no switch, `d_save.cpp:1544-1546`); for 0xF0 and above (the assert at `d_save.cpp:1541-1542`); for a table outside 0..15 (`d_save.cpp:1507`); for a switch no placed actor writes, or one that any of its writers clears (§6.2); and without tables.
    - The host's hub keeps and relays only the bits that verdict takes (`hub.Config.SyncedScoped`, through `hub.ScopeFilter`). The rest are counted per peer (`PeerStatus.ScopedRefused`), without a NOTICE, as the memory-switch policy's are.
    - A writing replica's sender proposes only what the same filter takes over its own tables (`Differ.ShareScopes`), and its receiver writes only what its own tables mark latching. `FeaturePuzzle` is a table feature (item 69), so a granted peer has the host's tables and its count stays 0. Only another build's sender is ever refused.
    - KILL bits pass unfiltered, for M6 to decide (§7.2).
    - *API change:* `hub.Config.ScopeFilter` (nil kept every bit; only hub tests set it) is replaced by `Config.SyncedScoped`. A nil verdict keeps no dan, zone or room switch bit.
114. **Dan switches are per save table in the game, and per (stage, table) in co-op. Corrects §4.4; supersedes item 55.**
    - The game keeps dan switches per save table: `dStage_stagInfoInit` calls `initDan` with the STAG's table at every stage load (`d_stage.cpp:1668-1677`), and `dSv_danBit_c::init` clears them only when the table differs from the last one (`d_save.cpp:1225-1235`). So a dungeon's stages share them: DRC's `M_NewD2` and its boss stage `M_DragB`.
    - §4.4 said `store[stage]`. The store is keyed by (stage, table) and lives while someone is in that stage and table; §6.3's apply rule (same stage name and table) is kept as policy. A receiver in another stage of the same table gets the partner's dan switch only once it enters the partner's stage, and only while the partner is still there (item 122). That is conservative: the bit sits in the one `mDan` either way.
    - Item 55's lifetime is gone: a store lived while anyone was in the table, with at most `world.MaxDanStores` (256) per table and a NOTICE past that. It never held for a lone player anyway. Between two stages the old play scene's delete clears the player pointer (`dScnPly_Delete` ends in `dComIfGp_init`, `d_s_play.cpp:1136`; `d_com_inf_game.cpp:60-69`), so PRESENCE says nowhere and the table's stores went.
    - `MaxDanStores` and `ErrScopesFull` are removed. The stores are bounded by where players are: one DAN store per occupied stage. What a player's game carries from one stage of a table to the next, its sender proposes again on entering it (item 118).
115. **Zone writes (§3.1).**
    - The receiver writes a zone switch into the zone its own game has for Link's room, `mStatus[room].mZoneNo` (`dSv_info_c::onSwitch`, `d_save.cpp:1552-1556`), never into the sender's slot: slots are taken first-free on each machine (`createZone`, `d_save.cpp:1525-1537`).
    - A zone is written only if `0 <= mZoneNo < 32` and its `mRoomNo` is the room. The game's own setter checks neither the room nor, past its assert, the zone (`d_save.cpp:1553-1556`).
    - The FrameWriter's scoped path (`FrameWriter.ApplyScoped`) reads both again on the write tick. The next tick's verify skips a byte if the stage, table, file, `mStayNo` or slot changed in between.
    - Writes are single bytes ORed in, not words: the game sets a dan switch with a u32 read-modify-write of `mDan.mSwitch` (`d_save.cpp:1237-1241`) and a zone switch with a u16 one (`d_save.cpp:1279-1282`), so a word write could undo a bit the game set in between.
    - A same-stage reload between plan and write that recreates the room's zone in the same slot does not stop the write: the slot is still the zone of Link's room, so the bit lands in the fresh zone and verifies. A zone moved to another slot, or a change of `mStayNo`, stops it.
    - The verify assumes a stage change lasts many ticks. On the console a stage change runs through a wipe overlap first (`fopScnM_ChangeReq` with an overlap from `l_wipeType`, `d_s_play.cpp:291-314`), so a zone is never wiped and recreated in place between one 100 ms tick and the next. The FakeGame does both in one call, so the session test wipes or moves a zone only when the pipeline's last tick wrote no scoped byte.
    - At a negative start point the game keeps the zones and each room's zone number across a stage load (`d_stage.cpp:124-126`, `:134-138`). That covers restart (-1, `d_stage.cpp:2369`), turn restart (-3, `:2380`) and back to the sea from a ship (-2, `:2317-2320`). A sender then proposes a kept zone's words for the new stage's room with that number, which its own game treats as that room's zone. The receiver still checks its own zone at every write.
116. **Room edges: the same visit, not the same tick (corrects §6.3).**
    - §6.3 said an edge applies only if both players are in the room "at that moment". Taken literally, every edge that arrives while a gate is briefly closed (an event, a menu) would be lost.
    - Built: an edge is kept for the visit Link is in when it arrives. A visit is the same stage, table, room, zone slot and `mOldStayNo`, unbroken, with no stage change pending and none of the visit's room switches gone (item 124). The edge is written on the first tick the gates open and the room has settled (item 121). It waits at most 10 ticks of closed gates (about a second, `edgeTicks`); otherwise it is dropped, logged once and counted in `/status`. Ticks of the room hold with the gates open do not count.
    - A pending stage change ends the visit, so a same-stage reload is a new visit. An edge is never stored and never replayed after a room change: `clearRoomSwitch` zeroes the room switches on every room change (`d_save.cpp:1273-1276`; `zoneCountCheck`, `d_stage.cpp:250-267`).
    - Room switches are sent only as edges, never whole, so a room switch set before the sender's first look at the room is never sent.
    - Known race, not fixed: the receiver's own sender sees a bit the reconciler wrote as a rising edge and proposes it. That is harmless for the stores, since the hub relays only what a store lacks. But an echoed room edge could reach the partner's next visit if the partner left and re-entered the room within the round trip: milliseconds, against room changes that take seconds.
117. **The ever-had set: a reset bit is not refilled.**
    - A bit the receiver's game ever had in a scope, seen on a consistent tick or written by the scoped path, is never written there again. So a bit its own game clears stays cleared, and a scope entered again gets only the bits it never had.
    - The set lives with the loaded file. For a writing replica it also starts over on a save-state load and at the title or file select. It is not cleared on a stage change. It is capped at 64 × (1 + 64) scopes: at the cap, other stages' and tables' scopes are dropped first, and the dan bits of Link's table are kept. Dan bits are kept per save table (item 125).
    - Consequence, by design: a receiver whose own game reset bits does not get them back from the partner, so the two diverge on those bits. The resets are `initDan` on a table change, a zone removed two room changes away (`d_stage.cpp:250-267`), and a reload's `initZone` (`d_stage.cpp:124-126`). The game resets those rooms for that player, and co-op does not undo it. *Corrected (item 125):* as first built, `initDan`'s resets were refilled from another stage of the same table, since the set kept dan bits per (stage, table).
    - The acceptance test checks convergence only on bits set after each game's last reset.
118. **The sender proposes whole words on entry and every 10 ticks (gap, decided).**
    - The hub keeps a store only while someone is in its scope, and M2's sender sent only edges. So players entering a room together never caught up with each other, nor did they after a reconnect, after a host restart, or when the sender wasn't armed on its entry tick.
    - A sharing Differ (`ShareScopes`, writing replicas only) proposes the latching dan and zone words whole on entering their scope and again every 10 ticks, one PRESENCE period. The hub relays only what a store lacks, so a refresh costs one SCOPED of each kind and no relay.
    - Room switches and kill words stay edges.

*M5 in the `--coop` session (§8.2)*

119. **The wiring.**
    - The host's hub gets `SyncedScoped: cs.tables.scopedVerdict()`, the session tables' `SyncedScopedSwitch`, which is nil without tables, so no puzzle bit is kept.
    - Writing replicas ask for `FeaturePuzzle` with M3's and M4's features. `--coop-dry-run` asks for the image alone, so it is sent no SCOPED and writes nothing. Its `/status` line says `room puzzles: none in a dry run, …`.
    - The startup log names the room puzzles and how many dan, zone and room switches the session's tables mark latching (`co-op: room puzzles this session: …`), or warns that they are off without tables. The replica says once per grant whether they are written, and why not.
    - `/status` adds a `room puzzles:` line per player: on or why not, the place (stage, table, room) and zone slot of the stores Link takes, and the partner switches set, refused as not latching here, and room edges dropped (`replica.Status.Puzzle`). "Link" there is the game's character, whatever the player is called.
    - On the host, `/status` also prints the hub's lines: whether it allows the room puzzles, the scoped stores it holds by kind, and each peer's place, grant and refused bits (`hub.Status`). They print even before a pipeline runs. A joiner's console has no hub and prints none.
    - `beatSlack` went from 2 to 4: a tick can now wait on the merge, derive and scoped batches before an escape's.
    - Without `--coop`, `host`, `join`, `mp-local`, `patch` and the TUI are unchanged.
120. **Acceptance and tests.**
    - `TestCoopSessionM5` extends M4's session test. It runs two FakeGames, a real `network.Server` with the CoopHub on loopback, and two writing pipelines, with a fixture whose DRC rooms hold latching and timed crystals for dan, zone and room switches. Its scripted play covers:
      - latching against timed switches;
      - a player in another room, then coming in;
      - the boss stage of the same table;
      - a zone wiped and not refilled;
      - a zone moved to another slot;
      - a switch the game cleared, never set again.
    - Then 1000 random steps, with reordered, repeated and dropped messages, reconnects and a host restart from the world file.
    - It checks:
      - co-present convergence on latching dan, zone and fresh room switches;
      - that no pipeline set a switch the partner's game never had in that scope, and no room switch more often than the partner raised it;
      - that a room edge is not replayed;
      - a tap on every SCOPED either way, which never carries an unsafe switch;
      - the startup lines, `/status` and the hub's lines.
    - `coAuditM3` now accepts dan and zone bytes only under M5's rules (`coAuditM5Write`): one byte; no clear; IDLE with the gates open; a zone byte only in the slot of Link's room as the write was made; only switches the tables mark latching; none set twice in a scope. Every earlier rule still holds.
    - `TestReconcileM5Status`, `TestLogCoopPuzzles`, `TestPuzzleStatus` and `TestHubPuzzleStatus` cover the wiring.
    - Z1-Z6 are M5's owner checks (§8.2).
    - `FuzzHubFrames` runs slowly (1-2k execs, then none), at HEAD as well, so it was not caused by M5.

*M5 review fixes*

121. **The room hold: a new shutter ignores its switch for 30 frames (gap, decided; corrects §6.1, §6.3 and Z4).**
    - The facts are in §6.1: `daShutter_c` sets a 30-frame timer in `Create` and sees only a switch change after it has run out (`d_a_shutter.cpp:71-87`, `133-142`, `233-243`). Its frames run only outside events and the pause menu. A partner's switch written within that window is lost on the receiver: the shutter stays shut while its switch reads set, and a latched switch is never set again. That is the case Z4 describes: B comes through a door, and B's store arrives as B's PRESENCE enters.
    - Built (`replica/reconcile_m5_rooms.go`): the receiver keeps a clock per room of the IDLE frames its actors have had. A clock counts the heartbeat's frames between two consecutive IDLE ticks, at most one tick's worth (`maxBeatRate` × `TickEvery`, 24 frames). A longer gap could hide an event, so it adds nothing. A clock starts over when anything its actors rest on changes:
      - the room's zone slot;
      - its room scene's `mProcID` (`mStatus[room]` +0x10C, written by `phase_0`, `d_s_room.cpp:150-152`);
      - its status flags 0x01-0x08 (+0x104: loaded, being created, being deleted, hidden; `d_stage.cpp:214-240`, `d_s_room.cpp:89-101`, `:120`).
      Every clock starts over on a save state, on a read that failed, and on a change of stage or save table. A hidden room's clock does not run.
    - A zone or room switch is written once the clock of Link's room reaches 60 frames (`roomSettle`, 2 s at 30 fps). A dan switch is written once every shown room's clock has, since every loaded room's actors read the dan switches. A dry run plans the same way.
    - The shutter needs 31 frames. The rest of the 2 s covers its own create, which waits for its archive (`dComIfG_resLoad`, `d_a_shutter.cpp:103-106`) after the room scene asked for it. That margin is inferred, not measured.
    - The probe is two reads per in-game tick, apart from the snapshot: `mStayNo` with `mOldStayNo` (2 bytes), and `mStatus` (0x4500 bytes). *Residual window:* a door that shows a room in the one or two frames between a tick's probe and its write is not seen by that write. Only a dan write is exposed, since the room shown is not Link's. This is not fixed.
    - The FakeGame models the window (`fakegame.Shutter`, `HideRoom`, `ShowRoom`, `ReloadRoom`). `TestReconcileM5RoomHold` opens a shutter for a zone switch through a door, a dan switch onto a newly shown room, and each with an event or the pause menu in between. `TestM5RoomClocks` covers the clocks, and `TestShutter` covers the model.
122. **Co-presence is checked by the receiver (corrects the M5 build; §2 and §6.3 claimed it).**
    - The hub keeps a store while either player is in its scope, and relays a bit as it comes. So a store sent while the receiver was not idle was written later, after the partner had left. The receiver also wrote the stores the hub sent as it entered a room the partner had already left.
    - Built (`Replica.partnerIn`): a dan store is written only while the partner's last relayed PRESENCE is in game in the same stage and save table. A zone store also needs the same room. The hub relays every PRESENCE as it comes and a gone peer's as nowhere (`hub.leaveView`); a relayed PRESENCE names no sender, which is enough for two players (item 42). A bit that came while the receiver was not idle waits for the partner to come back.
    - Room edges need no check: the hub relays them only between players in the room, and the receiver keeps one only for the visit it came in (item 116).
    - The partner's PRESENCE lags it by the round trip, so a store may be written that long after the partner left. The bit was the partner's while both were there.
123. **The receiver's copy of a store lives only while it is in the store's scope (corrects the M5 build).**
    - The replica kept a DAN or ZONE store the hub had dropped: across a room change, a stage reload, the title or another file. It then wrote the store into a room's fresh zone on a later visit, with no new SCOPED.
    - Built (`pruneScoped`): each tick drops a DAN store unless Link is in game in its stage and save table. It also drops a ZONE store unless he is in its room. A new session clears them all (`resetSession`). Link in no game is the title, file select or a stage load, when his PRESENCE says nowhere (`hub.placeOf`). This is the hub's own lifetime for the receiver, which sends the stores again as the receiver's PRESENCE enters their scope (`hub.onPresence`, `world.Scopes.SetPresence`), if anyone is still there. The dry run's record of what it logged follows the same rule.
    - `TestReconcileM5MirrorInScope` covers a room left until its zone is freed, a room left and re-entered, a stage reload, a title trip, another table and back, and a reconnect.
124. **Room bounces (extends item 116).**
    - Two room changes between two ticks (1 → 2 → 1) leave `mStayNo` and the zone slot as they were. Each change still clears every zone's room switches (`zoneCountCheck`, `d_stage.cpp:250-267`). An edge waiting across such a bounce was replayed. A room-edge byte written before it was judged a failed write when the game cleared it.
    - Built: the visit includes `mOldStayNo` (0x803F6A79, `sym:18668`), which `setStayNo` moves at every room change (`d_stage.cpp:171-173`). It also ends when a room switch the visit had reads clear.
    - The scoped path reads `mOldStayNo` again on the write tick and aborts if it moved. The verify does not judge a room-edge byte if `mOldStayNo` moved, or if the zone's room switches all read 0.
    - *Limit, not fixed:* a bounce back into the room Link came from leaves `mOldStayNo` as it was. With no room switch set, nothing a tick reads shows it. An edge waiting across it is written in the new visit. A room-edge byte written before it is judged a failed write if the game sets another room switch of that byte before the next tick. Both need two room changes in one tick (100 ms). Each room change takes a door or a walk, so that is inferred to be rare. The cost is a room switch the partner set while in the room, or one verify failure for the breaker. `TestReconcileM5RoomBounce` pins both sides of the limit.
125. **The ever-had set keeps dan bits per save table (corrects item 117).**
    - The set keyed dan bits by (stage, table). So a dan bit the receiver's game had in `M_NewD2` and then lost to `initDan` was written again in `M_DragB` from the partner's store. The game keeps one `mDan`, for the table `initDan` last ran for (`d_save.cpp:1225-1235`; `d_stage.cpp:1668-1677`). A bit had in one stage of a table was therefore had in all of them.
    - Built: the ever-had key for dan bits is the table alone (`everKey`). At the cap the set drops other stages' and tables' scopes first, and keeps the dan bits of Link's table. The write audits (`auditM5Writes`, `coAuditM3`) key "never set twice" the same way. `TestReconcileM5DanEverHadPerTable` and `TestM5EverHadBound` cover it.
    - The tests now also cover the slot's `mRoomNo` at the guard (`TestWriteGuardScopedZones`: Link's zone freed, or now another room's). They cover the writer's write-tick re-read: `mRoomNo`, `mOldStayNo`, and an event or the pause menu starting while the batch waits (`TestFrameWriterScoped`).
126. **M3's current-table memory-switch write waits for the room hold too (gap, fixed; corrects §6.3).**
    - Verified in the decomp: a memory switch reaches a shutter. `daShutter_c` takes its switch from its params (`getSwitchNo`, `prm & 0xFF`, `d_a_shutter.h:60`) and reads it with `fopAcM_isSwitch`, which calls `dComIfGs_isSwitch` with the actor's home room (`f_op_actor_mng.h:374-376`). `dSv_info_c::isSwitch` reads a switch below 0x80 (`MEMORY_SWITCH`, `d_save.h:982`) from the live `memBit` (`d_save.cpp:1586-1592`), the copy M3 writes for the loaded table. So a shutter on a memory switch has the 30-frame window of §6.1 (`mFrameTimer = 30`, `d_a_shutter.cpp:87`; `mbIsSwitch` copied each execute, `:142`; the opening only once the timer has run out, `:236`).
    - The old write came on the first STABLE tick after the room change, at least 4 ticks (about 400 ms, 12 frames) later. A door shows the next room, and creates its actors, as it opens (`d_door.cpp:282`; `d_s_room.cpp:72-76`). A shutter created less than 30 frames before the write ignored it and stayed shut. `TestReconcileSwitchRoomHold` shows this in the FakeGame: with the write not held, the shutter stays shut.
    - Of the actors read, only `daShutter_c` has the pattern. It alone checks for a change since its last execute with a timer that starts at its create. Others that keep the switch's last value set it in their create and react on their next execute, with no timer: `daShutter2_c` (`d_a_shutter2.cpp:60`, `98`, `166`), `lstair` (`d_a_lstair.cpp:101`, `177`, `205`), the wind tag (`d_a_wind_tag.cpp:139`, `353`), `obj_bemos` (`d_a_obj_bemos.cpp:426`, `1332`) and `fire` (`d_a_fire.cpp:122`, `316-325`). The ladder (`d_a_obj_ladder.cpp:150-153`), the hanging lift (`d_a_obj_hlift.cpp:110-114`, `159-163`), a chest waiting on a switch (`actionSwOnWait`, `d_a_tbox.cpp:1000-1016`) and `andsw2` read the switch every frame, so a late write is still seen.
    - Built (`replica/reconcile.go`): a room or stage change opens two windows. The ocean, the charts and the sea map keep the first STABLE and IDLE tick after the change (IDLE since item 129). The loaded table's memory switches get their own window: the first STABLE and IDLE tick after the change on which the room hold allows them. That is the dan switches' rule (`m5Rooms.calm`: every shown room's clock has reached `roomSettle`, item 121), on the same clocks. The window stays open until then, so a waiting switch is not dropped. When it lands, it goes through the same gates and WriteGuard checks as before. A switch gained between the change and the hold is written in the same window. With `--coop-live-switches` the switches are written when IDLE and the hold allows them. The clocks are now read before the reconcilers run (`m5Observe` moved into `tick`), so M3 and M5 read the same clocks each tick. Writes to cold tables are not affected. A room change seen while the session is not armed still closes both windows, on its first STABLE tick, IDLE or not (item 129). A stalled or aborted write keeps its window, as before.
    - The cost: the write now usually comes about 2 s after the room change, while Link is in the room, so polling actors there react to it in view, as with M5's dan switches. An event or the pause menu adds no frames to the clocks, so it delays the write further.
    - Tests: `TestReconcileSwitchRoomHold` lands a partner's memory switch right after a door, deferred and with `--coop-live-switches`, each also with an event or the pause menu after the door; the shutter opens. `fakegame.RoomAges` counts each room's actor executes; `TestRoomAges` covers it, and `TestShutter` now runs the shutter on memory, dan and zone switches. The write audits (`auditWrites`, `coAuditM3`) fail any current-table memory-switch write made while a shown room's actors have run fewer than 30 frames. `FuzzReconcileM5` adds world memory switches between its steps. The M3 timing tests (`TestReconcileSwitchWaitsForRoomChange`, `TestReconcileWindowIsOneTick`, `TestReconcileLiveSwitches`, `TestReconcileStallKeepsTheWindow`, `TestReconcileDropsAWindowSeenUnarmed`) now expect the hold.
127. **Stages only another scene loads (found on the owner's disc; corrects M1's loader).**
    - `coop-tables` failed on the owner's disc: `res/Stage/Name/Stage.arc` has no `stage.dzs`. The loader took that for a broken disc and built no tables. With no tables, M3's memory switches, M4's derived keys and hearts, and M5's room puzzles all stay off.
    - Verified in the decomp: only the play scene's loader reads `stage.dzs` (`dStage_infoCreate` and `dStage_Create`, `d_stage.cpp:2214-2226`, called from `d_s_play.cpp:1301` and `:1351`; the opening scenes share it, `d_s_play.cpp:1528-1566`). It halts on a missing one, since JUT_ASSERT panics in the retail build too (`JUTAssert.h:9`). `getStageRes` has no fallback: it matches the name in the loaded Stage archive or returns NULL (`d_resorce.cpp:657-673`). The file-select (name) scene loads `Name`'s Stage.arc and reads only a nested archive in it, resource 0x17, for its screens (`d_s_name.cpp:71`, `125-145`).
    - A `stage.dzs` with no STAG can't be played either. The stage info stays NULL (`dStage_stageDt_c::init`, `d_stage.cpp:385`); only the STAG sets it, and with it the save table (`dStage_stagInfoInit`, `:1670-1677`). The play scene then reads it unchecked as the stage starts (`d_s_play.cpp:1302`) and ends (`dStage_Delete`, `d_stage.cpp:2255`). That is a NULL read, not the assert.
    - Built: `stagedata.Load` leaves such a stage out, with its rooms, and returns it as skipped. The tables record it (`skipped`, schema 3), and the report header names it. An archive that doesn't parse, one over the archive cap and a read error still fail the build. The error now names the failed archives (up to 10): each stage's Stage.arc first, then the rooms of the stages whose Stage.arc loaded. A stage whose Stage.arc failed has its rooms left unread. `patch` and a `--coop` session's log put the list on one line.
    - `TestLoadSkips` covers both cases and `TestLoadErrors` the error list. The tables' vanilla fixture now has a `Name` stage like the real disc's, so every tables and session test builds past it.
128. **Cold tables are written while the pause menu is open (found on the owner's A5 run; corrects A5, §3 and §3.2).**
    - With B's pause menu open, B's session logged `set t04 FW visited room 0 (or item 32) (0x803C5033 |= 0x01)`, and the same for t05 TotG, as A entered those dungeons while B was on Outset or the sea. A5 said nothing applies while the menu is open. `/warp` was refused at the same time (an escape needs IDLE: `Replica.planEscape`'s `CanWriteIdle` case, `replica/escape.go`), so the gates did see the menu.
    - The code does what §6.3 says. A stage progress bit or memory switch of a save table other than the loaded one (a cold table) is planned `OnLoad` (`planner.timingOf`), which `Replica.pick` makes on STABLE and HEARTBEAT alone, and the WriteGuard opens `TargetStage` on the same gates. It goes to the saved slot only, so its log line has no `and its live copy`. Only the loaded table's progress waits for IDLE. A5 was wrong, and so were §3's Stage progress row ("on load" for other tables, which the legend defines as the next room change) and §3.2's IDLE list (it named story bits, which need only the transition tick, and left out the loaded table's progress). All three are corrected, as is the `gate` package doc.
    - Why writing a cold table under the menu is safe, verified in the decomp:
      - The game reads a cold slot whole only at `getSave`, as a stage loads (`d_save.cpp:1506-1509`, from `dStage_stagInfoInit`, `d_stage.cpp:1674-1675`), and writes it whole only at `putSave`, as a stage is deleted (`d_save.cpp:1512-1515`, `d_stage.cpp:2255-2256`). The menu opens only with no stage change pending (`CAN_PROCEED`, `d_menu_window.cpp:869`; the Start menu, `:930`).
      - Under the menu only NOPAUSE actors execute, and no actor draws (`f_op_actor.cpp:175`, `:126`).
      - The menus' only write to the stage tables is the save menu's `putSave` of the loaded one (`d_menu_save.cpp:935`). Its card copy memcpys all 16 slots into a buffer (`memory_to_card`, `d_save.cpp:1771`) before the checksum and the card write (`dataWrite`, `d_menu_save.cpp:1012-1017`). A byte Go ORs in meanwhile is in that save or not. It can't be torn: each write is one byte, with no invariant across bytes (unlike the tag, item 48).
      - The readers of cold slots that run under the menu only display them. The sea chart reads the ET and WT boss bits once, as it opens (`checkMarkCheck2`, `d_menu_fmap.cpp:770`, `950-960`; the saved-slot read, `d_com_inf_game.cpp:826-833`), and the NOPAUSE Tingle Tuner reads the same bits (`d_a_agb.cpp:1248-1249`, `:1966`). The dungeon map reads the loaded table's live copy (`d_menu_dmap.cpp:507`).
    - The game has cold-table writers of its own, each a load, OR and store (`d_save.cpp:1072-1075`, `1084-1087`, `1136-1139`). In events: a chest whose type records it in table 1, `dComIfGs_onStageTbox(STAGE_SEA2, …)` as it opens (`d_a_tbox.cpp:855`), and the Forsaken Fortress heart container got on the sea, `dComIfGs_onStageLife(STAGE_FF)` (`d_item.cpp:599`). In IDLE play: a Blue Chu Jelly picked up, `dComIfGs_onSaveSwitch(STAGE_BLUE_CHU_JELLY, …)` (`f_op_actor_mng.h:427`, from `d_a_item.cpp:772`). None runs under the menu, and each shares §4.2.1's window after the heartbeat with every other byte. So gating Go's cold writes on IDLE would remove no hazard.
    - The cost of gating them on IDLE, inferred: a partner's bit that arrives during the door event into that stage would miss its `getSave`, and a chest would read it only the next time its room loads (`d_a_tbox.cpp:447-455`).
    - *Caveat, not fixed:* the merge path does not read IDLE again after the heartbeat wait (`FrameWriter.ApplyContext` calls `recheck` only, `writer.go:557`). So a loaded-table write planned on the tick before the menu opens can still land in the frame it opens. Its readers act when an actor is created, or only draw, so this is harmless (inferred), but B may log one `set` line as the menu opens.
    - No code changed. `TestFrameWriterGatesClosedMeansNoWrite` (stage bits need no IDLE), `TestReconcileStageProgress` (another table's progress does not wait for an event) and `auditWrites` (IDLE only for the loaded table's bytes) already pin the behaviour.
129. **The room-change writes wait for IDLE too (design change, found with item 128; corrects §6.3 and item 126).**
    - The room-change windows opened on STABLE alone (`roomWatch.open`). So the ocean, the charts and the sea map were written on the first STABLE tick after a room change even if the pause menu had opened, or an event had started, since the change. A loaded-table memory switch went under the menu once the room hold had passed. That was what §6.3 and item 126 said, but it broke A5, and every other write the receiver's stage reads waits for IDLE: the loaded table's chests and pickups, the live switches (`--coop-live-switches`), and the dan and zone switches.
    - The ocean is the class that matters, verified in the decomp: the sea chart writes the same `dSv_player_map_c` bytes while `dMenu_pause` is set.
      - In its fishman mode, opened with `dMenu_flagSet(1)` (`d_menu_window.cpp:903-907`), it marks a square arrived (`onSaveArriveGrid`, `d_menu_fmap.cpp:3271`).
      - Opening a chart on the chart screen marks it opened (`onOpenCollectMap`, `d_menu_fmap2.cpp:1045`, `1691`).
      - As the chart screen starts, it clears the opened and completed bits of every chart not owned (`calcGetCollectMap`, `d_menu_fmap2.cpp:3174-3191`, called from `initialize`, `:487`).
      Each is a word or byte read-modify-write (`d_save.cpp:864-873`, `888-891`, `930-933`), so a Go write into the same byte shared §4.2.1's lost-update window with a writer that runs only under the menu. The chart also draws what it read as it opened (`displayinit`, `d_menu_fmap.cpp:725-729`), so a write made under it would show only the next time it opens.
    - The deferred memory switches were harmless under the menu: only NOPAUSE actors execute then, so a shutter sees the switch only once the menu closes (`f_op_actor.cpp:175`). They now wait as the live ones do, for one rule.
    - Built (`replica/reconcile.go`): `roomWatch.open` needs IDLE as well. The ocean's window is the first STABLE and IDLE tick after a room or stage change, and the memory switches' the first such tick the room hold allows. A window closes on that tick, whether or not a write was due in it, and not before, so a due write waits through the menu or the event and is not dropped. While the session is not armed, the windows still close on the first STABLE tick, IDLE or not: a room change seen before the session armed, even under the menu, is still not the deferred writes'. The WriteGuard now opens `TargetOcean` only when IDLE (`Tick.opens`). `TargetStage` stays on STABLE, as cold tables share it (item 128). So the only classes written while the game is not IDLE are the cold tables' stage progress bits and memory switches, and the story bits and registers on the transition tick.
    - The gain against lost updates is small. `dMap_c::setArriveInfo` still sets a sea-map bit when no event runs on the sea (`mFmapBits` bit 1, `d_map.cpp:1937-1948`, `d_save.cpp:954-957`). It runs on the square change, about 4 ticks before the window, so the two seldom meet. The menu also had to open within about 0.4 s of a room change. The main gain is one rule for the loaded table and an A5 that holds. Item 128's caveat applies here too: a write planned on the tick before the menu opens can land as it opens.
    - The wait log now tells a wait for IDLE from a wait for a room change (`Replica.logWaits`, which is passed the tick, with its gates). As first built it said `waits for the next room or stage change` for both, so a bit that arrived after the change, under the menu, said it waited for a room change and was then written as the menu closed, with no room change. Since the windows need IDLE, that wait can last as long as the menu or the event.
      - A bit that arrives before the room change logs `… waits for the next room or stage change`. It is logged once while it waits, so it is not logged again when, after the change, it waits for IDLE.
      - A bit that arrives after the change, while its window waits for IDLE, logs `… waits for this game to be idle (the pause menu is open): its room change is in`, with the IDLE gate's reason (`an event is running`, and so on). It does so from the change on, while STABLE settles too. A memory switch of the loaded table logs it only once the room hold has passed; before, it logs the hold's line.
      - A bit that arrives after the change, while STABLE settles with the game IDLE, logs nothing: it is written within ticks, as a chest is. If the menu opens before STABLE does, it logs the IDLE line then.
      - A stage change is in only once the new stage has loaded. `roomWatch` makes the next room due as soon as `mNextStage` is set, but a bit that arrives while the change is still pending (during a door's or a cutscene's exit event, with Link still in the old stage) waits for the load and the new stage's STABLE as well. So it logs `… waits for the next room or stage change`, as it did before the IDLE line was added. As the IDLE line was first built, such a bit got the IDLE line; `logWaits` now reads `mNextStage.mEnable` from the tick it is passed.
    - Tests:
      - `TestReconcileOceanAtRoomChange` adds the pause menu, and an event, right after a room change: nothing is written for 14 ticks, then all five bytes on the first IDLE tick, with `RoomChange` set.
      - `TestReconcileSwitchRoomHold` adds Link walking into a room whose shutter has settled, then the pause menu or an event: nothing is written until it ends, then the switch is, and the shutter opens.
      - `TestReconcileDropsAWindowSeenUnarmed` adds a room change seen unarmed under the pause menu: the switch waits for the next room change.
      - `TestReconcileLogsAnIdleWait` covers the wait log. An ocean bit arrives under the pause menu once STABLE has opened after a room change, in an event as STABLE settles, and as STABLE settles before the menu opens; each gets the IDLE line and no other wait line. So does a memory switch after Link walks into a settled room. An ocean bit that arrived before the room change keeps its one room-change line, also when the game is IDLE for two ticks after the change before the menu opens. Each is written on the first IDLE tick, in its window, with no other room change.
      - `TestReconcileLogsAPendingStageChange` has an ocean bit, and a memory switch of the loaded table whose room hold has passed, arrive during a door's exit event once `mNextStage` is set. Each gets the room-change line and no IDLE line, and is written in the new stage's window, once it is STABLE.
      - `TestWriteGuardGates` and `TestFrameWriterOceanNeedsIdle` cover the guard: an ocean byte under the pause menu is closed, with no write and no fault, and goes once the menu closes.
      - The write audits' room windows (`roomWindows`, `coRoomWindows`) now need IDLE. With the old STABLE-only rule there, `TestCoopSessionM5` fails on an ocean write the new rule moves to a later tick. `auditWrites` and `coAuditM3` also fail a deferred memory switch of the loaded table written while not IDLE.
130. **`mp-local`'s startup shift wrote A's coordinates into B's stage (found on the owner's M3 run; corrects item 80).**
    - On an `mp-local` restart, Dolphin B's Link, left standing on the Outset beach, was found swimming in open sea in room 25 (Six-Eye Reef), a quarter heart down. B's file then had a sea square A's file lacks.
    - Verified in the code: the startup shift ran on every `mp-local` start, before the co-op host started, so under `--coop` too. It wrote A's current position + (50, 0, 50) into B's warp request (`warpDolphinB`, through `warpLink`), with no check of either stage, room, gate or distance. The C warp handler carries a request out on the first frame Link exists, in whatever stage he is in (`multiplayer_update` in `src/core/hooks.c`, `warp_frame` in `src/core/services.c`). Nothing else writes Link's position or `mNextStage` when a session starts or stops, and the target was anchored on A, so it did not drift across restarts.
    - Verified in the decomp: on the sea, any position with |x| and |z| under 50000 is in the centre square, room 25 (`dIsleRoom_SixEyeReef_e`, `d_save.h:329`; Outset is 44, `:348`). The sea map's grid square is floor(pos / 100000 + 0.5) on each axis (`dMap_c::point2Grid`, `d_map.cpp:1750-1768`), numbered (x + 3) + (y + 3) * 7 (`d_menu_fmap.cpp:3271`), and a square's room is its number + 1 (`d_menu_fmap.cpp:747`). The void-out's restart quadrant, (350000 + pos) / 50000, picks sector 24 of 49 there (`d_a_player_main.cpp:10884-10899`); that sector 24's SCLS entries lead to room 25 is the sea's stage data, inferred. With no ground under Link, `execute` sets `mRestartPoint` to 0xFF and calls `checkFallCode` (`d_a_player_main.cpp:11426`, `:11504-11511`). Swimming there calls `startRestartRoom(5, 0xC9, -1.0f, 0)` (`:10738-10748`), whose `setDamagePoint(-1.0f)` costs one quarter heart (`:10850`).
    - Inferred, not reproduced (the run's log is not available): at that restart A was in a dungeon (`M_Dai`) and B at sea by Outset; the dungeon's coordinates lie within ±50000, so B landed in square 25; B's game had the sea's water there but no ground; and the square was marked in `mFmapBits`, which co-op syncs as the ocean and charts, by `dMap_c::setArriveInfo` as B's grid square changed (`d_map.cpp:1937-1950`) or by the sea chart (`d_menu_fmap.cpp:3271`). `setArriveInfo`'s caller is not in the decomp source.
    - *Caveat on where the void-out restarts:* on the sea, with `mRestartPoint` 0xFF, `startRestartRoom` reloads into the quadrant of the ship's position when a ship actor exists, else of Link's (`d_a_player_main.cpp:10873-10901`). The ship registers itself once created on a file with `MET_KORL` (`d_a_ship.cpp:4548-4550`, `:4746`). A file without `RODE_KORL` in room 44 or 11 goes back to point 0x80 instead (`:10853-10861`). So a restart into room 25 needs a file past `RODE_KORL` with no ship actor, or a ship in the centre square. Whether B's game had a ship then is not known. With one, the wrong-frame write still costs a void-out and a quarter heart.
    - Built (`mp_local_shift.go`): `startupShift` never runs under `--coop`, its dry run included, and says so in one line, which names `MP_LOCAL_SHIFT_X/Y/Z` as ignored only when one is set. Two Dolphins booted from one save state then start with the Links on top of each other until one walks a few steps away. Without `--coop` it reads each Dolphin's gates (`gateSnapshots`) and moves B only when all of these hold (`planShift`):
      - both games allow an IDLE write (`gate.Status.CanWriteIdle`);
      - both are in the same stage, layer, save table and room, with `mStayNo` known;
      - the two Links overlap: within 5 units across the ground and 5 in height (`current.pos`, actor +0x1F8).
      The target is B's own position + the shift, so it is in B's stage by construction. After one shift the Links stand about 70 apart, so a restart leaves them be. While only STABLE or HEARTBEAT is closed it looks again for about 3 s; any other reason it logs once and gives up. When either side's IDLE gate is closed, that is the reason it names, even if STABLE or HEARTBEAT is closed too on either side. A gate's reason is logged in parentheses, as some end in a question mark. A warp B's game does not take is withdrawn (item 131). `MP_LOCAL_SHIFT_X/Y/Z` still set the shift, and all three 0 turn it off. A NaN or infinite one, which `envFloat32` accepts, is refused.
    - Recovering a file the old shift hurt: the square is one `mFmapBits` bit. A file not yet bound is judged divergent at its bind for it (§5.3): bind it with `--coop-merge` on the host, which unions the square into the world, or restore its card from a backup taken before it was saved with the square (`coop-world restore`, §5.4). A file already bound needs neither: its next session merges the bit into the world (§5.5).
    - For the owner to confirm the chain: the console of that restart should show `Warped Dolphin B Link by (50, 0, 50) so the two players don't overlap.` while A was off the sea. With B on the Outset beach, `peek 0x803CA75C 4` reads `mpPlayerPtr[2]`, the ship. Non-zero means the void-out should have returned B to Outset's quadrant, so room 25 needs another look. To reproduce on a build before this fix, with a throwaway save: A in any dungeon, B on Outset, `mp-local` without `--coop`.
    - Tests: `TestPlanShift` covers the incident, the same coordinates in another stage, another room, layer and save table, Links 70 and 6 apart, a NaN position, a pending stage change on either side, an event, a queued event, the pause menu, no Link, a stalled heartbeat, the pause menu on B with A's heartbeat stalled or B still settling (the menu is named), and a shift of 0, NaN or infinity. `TestStartupShiftAcrossStagesWritesNothing` runs the incident on two FakeGames. `TestStartupShiftOverlap` moves B once from its own position, and a restart then skips. `TestStartupShiftRetries` covers the retry, and a paused A with B's menu open, which names the menu. `TestStartupShiftNeverUnderCoop` covers `--coop`, with and without a shift variable set.
131. **A warp the game does not take within 1 s is withdrawn (latent, found while checking item 130; the C side is unchanged).**
    - `warpLink` wrote `warp_x/y/z`, then `warp_seq` + 1, and waited 1 s for `warp_ack`. On a timeout it returned an error and left the request armed. `multiplayer_update` returns while Link is null, and otherwise warps whenever `warp_seq` ≠ `warp_ack`, with no stage check (`multiplayer_update` in `src/core/hooks.c`, `warp_frame` in `src/core/services.c`). Link is null across a scene change (item 73). So a request written as a stage change began, or while emulation was paused, landed later in whatever stage the game was in by then, where the coordinates mean another place. Until it landed, the co-op `/warp` refused with `a warp is still under way` (item 74). The owner's run did not hit it: both Dolphins sat idle.
    - Built (`warp_link.go`): `warpLinkMem` is `warpLink` over `mem.Mem`, and `warpLink` wraps it. On a timeout it reads `warp_ack` once more and returns nil if the game took the request. Otherwise it writes `warp_ack` = the new `warp_seq` and returns `warp not acknowledged within 1s (Link gone or emulation paused); withdrawn`.
    - The withdraw writes the value the C side writes, since the handler only ever copies `warp_seq` into `warp_ack` (`warp_frame` in `src/core/services.c`). If the handler takes the request between that last read and the write, both store the same value: Link is moved at most once, and the error then says withdrawn though he was moved. `warp_seq` never goes backwards, and a later `warp_seq` from another writer still differs from `warp_ack` and is carried out. Writing `warp_seq` back to `warp_ack` instead would move it backwards, could move Link twice in that race, and could cancel another writer's request. Before the withdraw it reads `warp_seq` again, and when that is no longer the new value (a save state was loaded, which restores the mailbox with the rest of MEM1, or another writer bumped it) it leaves the mailbox as it is and says `warp_seq is now <n>, not <new> (a save state loaded?); left as it is`, since a withdraw would arm a warp to the state's own `warp_x/y/z`, (0, 0, 0) in a state captured from a cold boot.
    - `warp_ack` stays outside TargetEscape (item 75), which is the co-op guard's. `warpLink` runs only from `mp-local`'s startup shift, never under `--coop` (item 130), and from the `warp` dev CLI, which writes outside the WriteGuard as it always has.
    - Tests: `TestWarpLinkWithdrawsOnTimeout` (with no acker, `warp_seq` and `warp_ack` agree after the timeout and a later frame with Link warps nothing), `TestWarpLinkAcked` (the 12 coordinate bytes are written before `warp_seq`), `TestWarpLinkAckRace` (the game takes the request just as it is withdrawn: one warp, and `warp_seq` = `warp_ack`) and `TestWarpLinkSaveStateLoaded` (a load in place of the `warp_seq` write: nothing more is written, and no warp follows).
132. **`/warp` lands on the partner, not beside it (found on the owner's R3 run; corrects R2, R3, §6.7 and items 73 and 74).**
    - In R3, B's Link landed on A's exact coordinates after the stage change, through the warp's second step. The two Links overlapped, so neither player saw the other. R2 and R3 said B stands at A's side, as §6.7 and item 73 did for the other-stage warp, while the code lands on A.
    - Verified in the code: `sameStageWarp` warps to `Config.PartnerPos()` unchanged, the latest position pose sync has of the partner (`partnerPos`), for the same-stage warp and the other-stage warp's second step alike (`finishWarp`). The C handler writes it into each of Link's position fields and `l_debug_keep_pos` (`warp_frame` in `src/core/services.c`). Each game draws the partner's Link #2 at the partner's own coordinates, so it was drawn inside the game's own Link. That this is why neither saw the other is inferred: nothing on the C side hides Link #2 by distance.
    - A landing beside A, built from A's recent path, was built and reviewed but not kept. Each tick it sampled the partner's position, and the warp landed 100-500 units back along a stretch the partner had walked in its room, or on the partner when there was none. No message says where a floor is in B's game: pose sync carries only the position and angle (`network.PlayerPosition`), and PRESENCE the stage, table, room, layer, point and flags. From positions alone it could not tell whether B can stand at a spot, and review found spots where B cannot:
      - A crawlspace: a crawl is slow and level, so it passed as a walk, but the roof there is 125 or less above Link (`checkNotCrawlStand`, `d_a_player_crawl.inc:308-312`), and B arrives standing, so his roof check hits (`dBgS_Acch::CrrPos`, `d_bg_s_acch.cpp:250-260`, with Link's roof height of 125, `d_a_player_main.cpp:12170`) and `checkRoofRestart`, which runs every frame (`:11564`), restarts the room as a crush, `startRestartRoom(5, 0xC9, -1.0f, 0)` (`:11118-11156`), at a cost of a quarter heart (`:10850`).
      - A narrow sidle ledge: a sidle is slow and level too, 8.5 units from the wall (`procWHideReady_init`, `d_a_player_whide.inc:276-281`; `d_a_player_HIO_data.inc:157-159`), but B arrives in his normal state, whose wall radius is 35 rather than the sidle's 8.5 (`setBgCheckParam`, `d_a_player_main.cpp:10667`, `:10689`), so the game pushes him 26.5 out from the wall, past the edge of a ledge less than about 35 deep (inferred: the depth of the game's sidle ledges was not traced).
      - A floor only the partner's game has: a light bridge registers its collision while its switch is set and releases it while it is not (`daLbridge_c::sw_check`, `d_a_lbridge.cpp:150-189`; `appear_bridge` and `disappear_bridge`, `:222-253`), and by default the partner's memory switch reaches B's game only at B's next room change (§6.3), so B, which asks for a warp just because it cannot cross, would land over the gap; a block the partner pushed or a platform its switch raised is the same (inferred).
      - The earlier review rounds: a Deku Leaf glide (`procFanGlide`, `d_a_player_fan.inc:410-447`) and a hookshot pull (`d_a_hookshot.cpp:320-333`) passed as walks across a gap, a warp after a jump landed B on the side the partner had left, and a moving platform's path lies over a gap once it has moved on; each fix narrowed what counted as a walk, and the cases above still passed.
    - For M7: a landing beside the partner needs a check of B's own collision at the spot before the warp is written, with the landing on the partner when it fails. The spot needs no roof 125 or less above it (`checkNotCrawlStand`'s test) and ground under it, a `GroundCross` within a few units of its height, taken where the wall push of a standing Link (radius 35) leaves him. Only the C side can make those checks in B's game, so it is deferred to M7.
    - The docs now describe the landing on A: R2, R3, §6.7 and items 73 and 74 say B lands on A's own position, so the two Links overlap until one walks a step, and R2 and R3 tell the owner to walk B a few steps aside before the shot after the warp. The landing is as it was on the R3 run, and logs `warping to the partner at (x, y, z) in room r`; only the other-stage warp's wording changed (`…, then onto the partner`, and `/help`'s line).
    - Tests: `TestCoopSessionM3` pins the landing on the partner's position and its log line, for the same-stage warp and for the second step of a warp to another stage. `TestEscapeWarpSameStage` and `TestEscapeWarpWaitsForThePartnersArrival` check the `warp_x` written, and `TestEscapeWarpOtherStage` the second step's log line.
133. **A fresh bind's first hello in a new process (found on the owner's M2 run; corrects the M2 build and §8.2).**
    - On an M2 restart, A's correctly bound file got the host's NOTICE "your file carries a co-op tag leaked from another file; it binds nothing and should be scrubbed", and its replica logged "this file is not bound to it (NOT_BOUND)". It was then welcomed and armed, and the host's `/status` said bound. Following the NOTICE with `coop-tag clear` would have unbound the file.
    - Verified in the code: the replica starts each process knowing no pending fresh binds (`Replica.fresh` is empty in `replica.New`), and V5 holds only through `mDateIPL` or a pending fresh bind the replica knows of (`bind.Classify`). A file bound fresh and not yet loaded from the card has `mDateIPL` 0 in RAM, so its first hello in a new process is LEAKED, with only V5 failing. That is how the replica asks: the hub finds the nonce pending and says so in its UNBOUND welcome (FreshBind), the replica records it, and its next hello is BOUND with AskUnsaved, which the hub welcomes OK, keeping the record. But the hub sent the leak NOTICE with every LEAKED hello, whatever it put in FreshBind, and the replica logged every UNBOUND welcome as not bound. Nothing was written and no scrub was offered: a V5-only failure is offered only once a welcome says its nonce is no pending fresh bind (item 49).
    - Verified in the decomp: RAM `mDateIPL` becomes non-zero only through a load from the card. `dSv_player_status_b_c::init` zeroes it (`d_save.cpp:51-56`), `memory_to_card` stamps the time only into the card buffer (`d_save.cpp:1715-1717`), and file select's load runs `card_to_memory` (`d_s_name.cpp:1213`), which copies the card's `mDateIPL` in (`d_save.cpp:1802`). An in-game save alone leaves it 0, so the NOTICE came back in every new process until the file was loaded from the card.
    - Inferred (the run's log is not available): at that restart A's file had not been loaded from the card since its fresh bind. Without the fix, `TestCoopFreshBindRestart` logs the owner's lines on its restart, and a file saved and loaded back arms on its first hello with no NOTICE, even with its record still pending. §8.2's check did not catch it: on the host's machine, and so under `mp-local`, `coop-tag show` reads the pending fresh binds from the world file, so a fresh-bound file not yet loaded from the card reads BOUND there, with "V5 held only through a pending fresh bind". The owner can check: until A's file is next loaded from the card, `WW_DOLPHIN_INDEX=<A> ./ww-multiplayer.exe coop-tag show` shows that line; and the restart log has no "Bind this save file", "asking to scrub" or "bound the file to world" line, any of which would mean a rebind or a scrub ran instead, as after a copy of the file was loaded on another Dolphin (below).
    - Built (`hub/session.go`, `onUnboundHello`): a LEAKED hello whose tag names the host's world and a pending fresh bind gets no NOTICE. Its welcome is unchanged (UNBOUND, NOT_BOUND, FreshBind), and the host logs "co-op: … joined with a tag naming pending fresh bind …, as a file bound fresh does until it is loaded from the card; told its replica the bind is pending". The hub cannot tell a V5-only failure from one where V2-V4 failed too; in that case the replica offers the scrub itself (item 49), and the line says only what a fresh-bound file does, not that this file is one.
    - Built (`onUnboundHello`): a LEAKED hello whose tag names another world, which may be a fresh bind of that world, no longer gets the leak NOTICE, whose "should be scrubbed" contradicted item 49. Its NOTICE is "your file carries a co-op tag of world <id>. It leaked from another file or, if only V5 fails, may be a fresh bind to that world not yet loaded from the card: only that world's host can tell. Nothing is synced until you bind the file", 245 bytes, within `wire.MaxNoticeLen`. The hub can't tell a V5-only failure from one where V2-V4 failed too, as CO_HELLO carries the tag's world and nonce but not the fields V2-V5 compare, so the NOTICE leaves that to the replica, whose prompt that follows names the rules that failed. Any other LEAKED hello (a tag of the host's world whose nonce is no pending fresh bind, or of no world) still gets the leak NOTICE.
    - Built (`bind/write.go`, `bindPrompt`): the prompt to bind a file whose tag names another world and fails only V5 no longer calls the tag stale, which contradicted the NOTICE a few lines before it in the same log. It says "Its current tag, of world <id>, fails only V5 (a never-saved New Game): it may be a fresh bind to that world not yet loaded from the card, or a tag leaked from another file, and only that world's host can tell. Binding replaces it; if it is that world's bind, the file leaves that world." A V5-only tag of the session's world is still called stale: the replica offers that bind only after the host's welcome has said its nonce is no pending fresh bind. So is a tag that fails V2-V4 as well.
    - Built (`replica/replica.go`, `onWelcome`): an UNBOUND welcome with FreshBind that answers a LEAKED hello, after which the file classifies BOUND, logs "the host has nonce … as a pending fresh bind: the file is bound" instead of "this file is not bound to it". A file that also fails V2-V4 keeps the not-bound line. Nothing changed on the wire or in the world file, and the replica still learns fresh binds only from welcomes, with no lookup of the host's world file.
    - §8.2's M2 step 2 now names the V5 line to check after the reload, "the file has been saved and loaded (mDateIPL 0x…)", and describes the provisional first hello, with the host's line for it, and what a copy of the file on another Dolphin does.
    - Not changed: a copy of a fresh-bound file, loaded on another Dolphin in a session of the world, clears the host's pending record. The copy has the same slot, name and tag, and `mDateIPL` ≠ 0 because it came from the card, so it says hello bound without AskUnsaved, and `onBoundHello` clears the nonce's record. The original, still in the host's RAM with `mDateIPL` 0, then finds no record in its next process: it gets the leak NOTICE and a prompt to bind it again or to scrub it, which `--yes` accepts. Such copies come from Dolphin's Memory Card Manager, which the far-behind advice (item 107) tells a joiner to use, and from `dolphin2 --reset`, which copies Dolphin 1's user folder with its cards (`internal/dolphinenv`'s `BootstrapUserDir`; `mp_local.go`'s `copyDirSkip` before App PR 3). This was reproduced with the fake game, where `--yes` rebound the original to a new nonce; it has not been seen on the owner's machine. The fix here is in words: §8.2's M2 step 2 says so, and the far-behind advice now says that the host should first save in game and load its file from the card. Two code changes could close it, neither made here: keep the record while a connected peer holds that nonce with AskUnsaved, or never clear records, which would make item 48's same-slot New Game edge permanent.
    - Not changed: a file whose tag names another world and fails only V5 is still offered a bind to the session's world, and `--yes` accepts it. Item 49's rule stops only the scrub, and the bind replaces the tag just the same. So a file bound fresh in world X and not yet loaded from the card, then joined to world Y with `--yes` in the same Dolphin run, is bound to Y. Back in X it is offered a bind to X again, since the Y tag also fails only V5 until the file is loaded from the card, and `--yes` rebinds it under a new nonce; once loaded from the card while bound to Y, it is FOREIGN in X. The prompt now says that binding replaces the tag. Not offering that bind, or not taking `--yes` for it, would close it; neither is made here.
    - Tests: `TestBindFlow` (no NOTICE for a LEAKED hello of a pending fresh nonce, and the host's line for it; the leak NOTICE and FreshBind false for another nonce of the same world; for another world's tag, the NOTICE above, whole, with no scrub and FreshBind false), `TestBindPromptOtherWorldV5` (the bind prompt above for another world's V5-only tag, with no "stale"; the stale line for a V5-only tag of the world bound to and for another world's tag that fails V2 too), `TestCoopFarBehindAdvice` (the far-behind advice, with the host's save and load first), `TestReplicaScrub` (the V5-only fresh case logs no not-bound line; "V2 and V5 fail, nonce pending fresh" keeps it and offers one scrub), `TestCoopFreshBindRestart` (a fresh-bound file never saved restarts armed and bound with neither line and no write; after a save and load, the next restart arms on its first hello and clears the record) and `TestCoopTagShowFreshBind` (after a load from the card, V5 holds through `mDateIPL` with the world file and without it).
134. **The stage tables' warnings name the cause (found on the owner's disc; corrects item 69 and §8.2).**
    - On the owner's disc the stage-table build failed (item 127). `patch` then recorded no disc, so the session said "patch has not recorded one. Run patch on your vanilla ISO again", and the hub's NOTICE gave the same advice. Patching again ran the same build, failed the same way and again recorded nothing. `patch`'s own warning, and its line for a panic, sent the user to `--coop-iso`, which runs the same build too.
    - The session also took any error matching `fs.ErrNotExist` for a missing record. A recorded image that is gone, with no cache this build can use (after an update, say), matched too: `GetPatched` wraps `inject.OpenDisc`'s error, which is `os.Open`'s.
    - Built (`coop_tables_runtime.go`, now `mods/coop/stagetables`; `wwapi/tables`):
      - `patch` records the disc even when its tables can't be built, and when they can't be cached. A session then reads the image again and gives the real cause, and a later build that can make the tables needs no new patch for them. The new record also replaces that of a disc patched before, whose tables a session would otherwise take while Dolphin runs the new one. That last case was checked with a probe on the code before this change (9c523b0): after patching a good disc and then one whose build fails, the session took the good disc's cached tables. `TestCoopTablesAfterAnotherDiscFails` now pins the new behaviour.
      - `tables.Get` returns a failed build as a `*tables.BuildError`, and sets `Stats.ID` before it builds, so `patch` can record the disc. A missing record is `tables.ErrNoRecord`, which the session tests in place of `fs.ErrNotExist`.
      - Each cause has its own advice, in the one `co-op:` warning of that side's log:
        - no disc recorded: run `patch` on the vanilla ISO with this build, or pass `--coop-iso`;
        - the recorded image gone, or the record stale or corrupt: run `patch` again, or pass `--coop-iso`, as before;
        - a disc this build can't make tables from: patching that disc again won't change that, and the line is worth a report if `patch` made it from the user's unmodified Wind Waker (GZLE01) image. The record names the disc patched last, even one whose build failed, and that need not be the disc Dolphin runs, so the warning adds: if Dolphin runs another disc, run `patch` on it or pass `--coop-iso`. `--coop-iso` names the disc the user chose, and its wording says only that patching again won't change it, with a report if it is the unmodified image or `patch` made it from one.
      - `patch`'s `co-op :` line chooses its warning the same way. A failed build says that patching again or `--coop-iso` won't change it, that the disc is recorded, so a later build that can read it needs no new patch for its tables, and to report the line if the user patched an unmodified GZLE01 image. If the disc can't be recorded either, the line names both errors and gives the same advice, without the sentence on the record. It says that sessions can't take this disc's tables, not that they run without memory switch sync: a record that can't be written leaves the one before it as it was (`writeAtomic`), so a session without `--coop-iso` takes the tables of the disc recorded before while Dolphin runs this one. The line says one may, and that the session's first lines name that disc. With no record before, the session says that no disc is recorded, and its advice to run `patch` won't help either. Both need a failed build and a failed record write at once; neither is changed here. The `*tables.BuildError` stays in the error's chain, beside a `notRecordedError`, so this line too says that `--coop-iso`, which runs the same build, won't help. Tables built but not cached are a note on the line, not a warning. Tables built but not recorded get the other failures' warning, which sends sessions to `--coop-iso`. A panic's line still says "please report it", and no longer suggests `--coop-iso`.
      - The hub knows only that a side's `stageTblHash` is 0, not why, so its NOTICEs give no advice: "the host has no co-op stage tables for its disc (the host's log says why)", and for a player "(the co-op warning in your log says why and what to do)". Both fit `wire.MaxNoticeLen` (200 and 227 bytes).
    - On the owner's disc the build now succeeds (item 127), so one run of `patch` with this build records the disc and its tables.
    - Tests: `TestCoopStageTablesWhy` checks the warning for each cause. `TestCoopTablesAfterAFailedPatch` patches a disc whose build fails, then puts a good disc at the same path; the session takes its tables with no new patch. `TestCacheCoopTablesForPatchUncached` covers the note, and `TestCacheCoopTablesForPatchNoRecord` a disc that can't be recorded either, whose tables can't be built or can't be cached. `TestCoopTablesAfterAnotherDiscFails` patches a good disc, then one whose build fails: the session gives the failure, not the good disc's tables, and `--coop-iso` on the good disc, or patching it again, gives them. `TestCacheCoopTablesForPatch` and `TestPatchCoopTables` now expect a failed build to be recorded, and `TestCacheCoopTablesForPatch` the session after it to name the same cause. `TestGetPatched`, `TestGetPatchedBuildFails` and `TestBuildErrors` cover `ErrNoRecord`, `BuildError` and `Stats.ID`. `TestHostWithoutTables` and `TestTablesNotice` check that the NOTICEs give no patch advice and arrive whole.
135. **A slot Dolphin.ini doesn't set takes Dolphin's default (found on the owner's first run; corrects M2's backup).**
    - Every `--coop` session warned "Dolphin.ini marks none of these cards as in use", though the owner's cards are Dolphin's default GCI folders. The backup took a slot Dolphin.ini doesn't set (no SlotA or SlotB, or one that doesn't parse) for unknown, and the session warns unless some card is in use. So the default GCI folder was found and backed up, but never marked in use.
    - Verified in Dolphin's source: a setting Dolphin.ini doesn't hold, or holds in a form that doesn't parse, reads as its default (`Config::GetUncached`, `Config.h`). The defaults are a GCI folder (EXIDeviceType 8) in slot A and nothing (0xFF) in slot B: `MAIN_SLOT_A` and `MAIN_SLOT_B` in `MainSettings.cpp`, the same in releases 2407, 2409, 2412, 2503, 2506, 2509 and 2609 and on master. Dolphin.ini holds only the settings that were set (`BaseConfigLayerLoader::Save`, `BaseConfigLoader.cpp`), and Dolphin's GameCube settings set SlotA and SlotB as the user changes a slot there (`GameCubePane::SaveSettings`). So a user who never changed a slot usually has neither key, and Dolphin uses `GC/USA/Card A` (`GetGCIFolderPath`, `MainSettings.cpp`). Dolphin 5.0 always wrote both keys and defaulted slot A to a raw card (`ConfigManager.cpp:252-253` and `:515-516` at the 5.0 tag), which is what the old reading matched.
    - Inferred, not seen: that the owner's Dolphin.ini has no SlotA, or one that doesn't parse. It is the only way the default GCI folder is found but not marked in use. Not checked: the development builds between 5.0 and 2407, which have no tags. A Dolphin.ini without SlotA from one that still defaulted to a raw card would now be marked wrongly. Only the mark and the warning rest on this: every card that exists is still backed up.
    - Wind Waker saves to slot A only: `mCardSlot = 0` in `mDoMemCd_Ctrl_c::ThdInit` is the only assignment (`m_Do_MemCard.cpp:34`), and every `CARDOpen` and `CARDCreate` of "gczelda" uses it (`:116`, `:134`, `:144`, `:230`, `:380`).
    - Built (`backup/locate.go`): a slot Dolphin.ini doesn't set, or sets to something that doesn't parse, takes Dolphin's default (`defaultDevice`). SlotA and SlotB are read as Dolphin's `TryParse` reads them (`StringUtil.h`): with `strtoll` and base 0, and only if it reads the whole value and the number fits an int. So `0x8` and `010` are 8, `08` doesn't parse, leading white space is skipped, and an empty value is 0, the Dummy device, which is no card (Dolphin's `IniFile` keeps an empty value, `IniFile::ParseLine` and `Section::Set` in `IniFile.cpp` on master). Go's `0o` and `_` forms are refused, as `strtoll` has neither. A `0b` prefix is read as binary, as a C23 `strtoll` reads it, which glibc 2.38 and later give C++ builds; a Dolphin whose C library's `strtoll` has no `0b` (an older glibc, and possibly MSVC's UCRT, which Dolphin's Windows builds use, or macOS's libc, neither checked) gives `0b1000` the slot's default. Dolphin never writes `0b`, so only a hand-edited SlotA or SlotB can read differently. A new backup's cards are always marked in use or not in use; `UseUnknown` stays for older builds' manifests, which `coop-world restore` still lists without a mark. The warning's condition is unchanged (no card in use, in either slot), and it now reads "Dolphin's settings (Dolphin.ini, or Dolphin's defaults where it sets none: a GCI folder in slot A) select none of these cards", with the same advice about `USER_DIR_1`. §5.4, §8.2's M2 step 1 and item 51 now name Dolphin's defaults too.
    - Also built (found in review), as the defaults made it matter: which path Dolphin takes for a slot's card. Dolphin takes `GCIFolder<X>PathOverride` over `GCIFolder<X>Path` and uses it as given (`CEXIMemoryCard::GetGCIFolderPath`, `EXI_DeviceMemoryCard.cpp:153-156`). It uses a configured `Memcard<X>Path` or GCI folder in place of the default card of its kind (`Config::GetMemcardPath` and `Config::GetGCIFolderPath`, `MainSettings.cpp`; the same rule in 2407 and 2609). Locate took the Path when both GCI folder keys were set, so it never looked at the folder Dolphin uses; with the defaults and no SlotA, it then marked the default `Card A` folder and the Path's folder in use, and nothing warned. It also marked a default card in use beside a configured one of its kind, as in the usual GUI flow: choosing a GCI folder in Dolphin's GameCube settings writes `GCIFolderAPath` and no SlotA (`GameCubePane::SetGCIFolder`). Now Locate reads the override first, and not the Path while the override is set. It marks the default card of a kind with a configured path not in use, unless the path comes to that card: a raw card path with the default's directory, name and extension, whatever its region code (Dolphin 5.0 filled the default path into `MemcardAPath` at boot and saved it, `ConfigManager.cpp:248` and `:894-901` at the 5.0 tag), or an override naming the default folder. A `GCIFolder<X>Path` never does, as Dolphin ends it with the region directory.
    - Also built (found in a later review): the folder Dolphin makes of a `GCIFolder<X>Path`. Locate tried the path as written, and with `USA` and `USA/Card A` added, and marked each it found in use when the slot's device was a GCI folder, as slot A's is by default. But `Config::GetGCIFolderPath` (`MainSettings.cpp`) turns `\` into `/` on Windows only (`UnifyPathSeparators`, `StringUtil.cpp`), drops trailing slashes, takes off one trailing `/USA`, `/JPN`, `/EUR` or `/DEV`, compared with its case, and adds the game's region directory, `/USA` for GZLE01. Dolphin's GameCube settings store a folder chosen there only if it ends in one of them (`GameCubePane::SetGCIFolder`). So a folder chosen as `D:/GC Saves/EUR` keeps Wind Waker's save in `D:/GC Saves/USA`: Locate marked `D:/GC Saves/EUR` in use and never found `D:/GC Saves/USA`, so the backup missed the save. Now the folder Dolphin makes is the configured card (`gciFolderPath`), in use when the slot's device is a GCI folder. The path as written and the two folders under it are only tried: they are listed when they hold a Wind Waker save, never as in use, and an older backup of them can still be restored. The override is used as given, as before, and the two folders under it are no longer marked in use either. Releases 2407, 2409, 2412, 2503, 2506, 2509 and 2512 take off `/USA`, `/JPN` and `/EUR` only, and so their GameCube settings accept only those; 2603, 2606, 2609 and master take off `/DEV` too. So for a path ending in `/DEV`, a release before 2603 uses `<path>/USA`, which Locate lists only as a tried folder, marked not in use.
    - Not changed: the warning still counts a card in use in either slot, though the game saves only to slot A. Counting slot A alone would also warn when slot A holds nothing and slot B a GCI folder; that is a separate decision.
    - Tests: `TestLocate` covers no Dolphin.ini, a [Core] with no SlotA or SlotB, values that don't parse, `SlotA = 1`, `0x1` with `0x8`, `SlotA = 255`, and an empty SlotA, with raw and GCI cards in both slots. It also covers a configured raw card and GCI folder beside the default ones, a configured path naming the default card or folder, both GCI folder keys set, an override with a save in `USA` under it, and a `GCIFolderAPath` ending in `EUR` (with and without trailing slashes), in `JPN` with a save in it, in `DEV` with a save in `DEV/USA`, and in `eur`. Its `GCIFolderAPath` case no longer lists the path as written, which holds no save itself. `TestGCIFolderPath` checks the folder made of a path, with `\` a separator on Windows only. `TestParseDevice` checks the parse against what `TryParse`'s code gives when built with g++ against glibc 2.39. `TestCoopSessionBackupLog` now expects a raw card with no Dolphin.ini to be logged `(default, not in use)`, with the warning, and adds the owner's layout: the default `Card A` folder with no Dolphin.ini is logged `(default, in use)`, with no warning.
136. **A session marker a killed host left is stale, not in use (found on the owner's first run; corrects item 66).**
    - After a hard kill (not Ctrl+C), `coop-world list` kept showing `in use: pid N` for a pid no process had, and `coop-world block` then needed `--force`.
    - Verified in the code: the marker was one line, `pid <n> since <time>`, and every reader took a marker that existed as in use: list, block, and the next host start, which warned. Only `coopSession.stop` removes it, as host and `mp-local` end. They end on the first Ctrl+C or SIGTERM, which `cliMultiplayerContext` in `main.go` catches and then stops catching. So a second Ctrl+C during teardown, `taskkill /F`, Task Manager's End task, a panic or an `os.Exit` path leave the marker. Closing the console window may still clean up: Go delivers `CTRL_CLOSE_EVENT` as SIGTERM, which gives the process a chance to clean up before Windows ends it (`os/signal`'s documentation).
    - Built (`coop_session.go`, `coop_procstate_*.go`; now `mods/coop/session` and `mods/coop/procstate`): the marker keeps its first line, so older versions still show it, joined with the new second line: `host=<name> start=<token>`. The host is `os.Hostname`, empty when that fails. The token is when the process started: on Windows its creation time (`GetProcessTimes`); on Linux the starttime field of `/proc/<pid>/stat`, counted from the last `)`, since the command name may hold spaces and parentheses; elsewhere none. `coopMarker.state` judges a marker:
      - this process's pid, with an equal or missing token: this process's own;
      - no pid, another host, or a check that fails: can't tell, taken as in use;
      - no process with its pid, or one with another start token (the pid was reused): stale;
      - otherwise in use, by a process with its pid: by its start token too when the marker and the check both have one, and by its pid alone when either has none.
      A marker with no host, as older versions wrote, is taken as this machine's. The host is recorded because the worlds are under `os.UserConfigDir`, which on Windows is `%APPDATA%`, and a roaming profile carries it between domain machines; a one-line marker another machine wrote that way can read stale. The time on the first line is not used: it is local time with no zone, so a clock or zone change could make a running host look newer than its marker.
    - The checks (`procState`): on Unix, `kill(pid, 0)`, where ESRCH is no process and EPERM a process of another user, which runs. On Windows, `OpenProcess` with `PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE`, where `ERROR_INVALID_PARAMETER` is no process and `ERROR_ACCESS_DENIED` a process this user may not open, which can't be checked (the error says so, and that after a restart it is most likely another program); then `WaitForSingleObject(h, 0)`, since a process that has exited keeps its pid while any handle to it is open. A build for any other OS can't tell. macOS is checked by pid alone: its start time needs `sysctl kern.proc.pid`, which would make `golang.org/x/sys` a direct dependency. So on macOS, and for a marker an older version wrote, a pid reused after a reboot reads in use and needs `--force`. On Windows, so does a killed host's pid that now belongs to a process this user can't open, such as a service; it reads `in use?` (can't tell). That is the safe direction, as is a Unix zombie, which answers `kill` until it is reaped, and a Linux pid that has the same starttime after a reboot (it counts clock ticks since boot).
    - What reads it: `coop-world list` shows `in use: …` (followed by `(by its pid alone: <why>)` when it is judged by its pid alone, as block and the host start say), `stale session marker (…; that process has ended)` or `in use?: … (can't tell: …)`. Block goes on past a stale marker with a one-line note and leaves the marker, so the next host start still says the session did not end cleanly. Without `--force` it refuses a marker in use or one it can't tell. It refuses a marker in use by its start token as `in use by a running session`, with `pass --force if you know no host serves the world`. It refuses a marker in use by its pid alone without claiming a running session: it says `a process with pid N runs, but this marker has no start time, so it may not be that host` (`its start time can't be read` when the marker has a token and the check gives none) and `pass --force if no host serves the world`. The host start logs a stale marker with `its last session did not end cleanly`, this process's own with `its earlier session here did not stop`, and one of another running process with a warning that two hosts on one world lose one host's changes; by its pid alone, the warning says that process may not be that host, as block does. It still starts, as before.
    - Not changed: the marker write is not atomic (a read, then a write), and the host start does not refuse a marker whose process runs.
    - The Windows check has not been run: the error codes and `WAIT_OBJECT_0` come from the Win32 documentation and Go's `syscall` wrappers, and CI runs the tests on Linux and macOS only. The owner should run `go test -count=1 -run "TestProcState|TestProcStateLiveChild|TestCoopMarker|TestCoopWorldBlock|TestSelectCoopWorld|TestStartCoopHost" .` once on Windows, in cmd.exe or PowerShell. `TestProcStateLiveChild` is the one that checks another running process, as block in one terminal checks the host in another.
    - Tests: `TestProcState` (this process runs, with a token on Linux and Windows; a child that has exited and pids 0 and -1 do not; pid 1 on Unix runs, and pid 4 on Windows runs or can't be opened), `TestProcStateLiveChild` (a marker a running child process wrote reads in use, by its start token on Linux and Windows, and stale once the child has exited), `TestStatStartTime` (a command name with spaces and parentheses), `TestCoopMarker` (the judgments above on a fake `coopProcState`, one-line markers, markers judged by pid alone, a failing check, a host with a control character, and a long marker cut between characters), `TestSelectCoopWorld` (list shows a stale marker, and a running process's by its start token and by its pid alone), `TestCoopWorldBlock` (this process's marker, a running process's by its start token and by its pid alone, and one from another machine refuse, `--force` goes on, and a dead child's marker is passed with the note and kept) and `TestStartCoopHost`'s restart (this process's marker, a dead child's, then a running process's by its start token and by its pid alone).
137. **The heart and key tables on a retail disc: test stages, Ganon's Tower's refights and the pig's dig spot (found on the owner's disc; corrects M1's heart and key tables, items 23 and 96).**
    - With item 127's loader, the owner's disc (506d861, schema 3: 155 stages, 497 rooms) gave a failing heart table and a failing keys section:
      - heart pieces 45, vanilla 44: 31 in stage files (chest 15, item 2, salvage 14), 11 rewards, 3 unverified. Two of the 31 are only in developer test stages on save table 15: tbox 15:0 (Amos_T and I_TestM) and item 15:0x33 (K_Test2 room 0, layer 7). One real source was not decoded at all: the pig's dig spot on Outset.
      - heart containers 8, vanilla 6 (boss 6, boss_item 1, item 1). One is a placed container in K_Test2 (STAGE_LIFE 15), one the Ganon's Tower refight bosses in Xboss0-3 (STAGE_LIFE 8), which never drop one.
      - keys: the Acceptance lines passed, but the section said FAIL (5 of 6 save tables pass): table 15 had 2 sources and no door.
      - So max life was off on every retail disc (item 96), and M4's P3 could not pass.
    - **Rule A: save table 15 is left out.** The table enum is `d_save.h:888-905`, with 0x0F = STAGE_TEST at `:904`; a stage's table is its STAG's (`dStage_stagInfo_GetSaveTbl`, `d_stage.h:1027`; used at `d_stage.cpp:1674`, `:2255`). Found on the owner's disc (a scratch dump of every stage's SCLS, not kept in the tree):
      - 26 stages are on table 15: Amos_T, A_R00, Ebesso, H_test, I_TestM, I_TestR, KATA_HB, KATA_RM, kazan, K_Test2-6, K_Test8, K_Test9, K_Testa-K_Teste, morocam, Msmoke, Mukao, TEST and VrTest.
      - No stage on another table has an SCLS exit into one, except A_umikz entries 0 and 1 to I_TestM. A_umikz holds no door or scene-change actor (Kamome, NBOX, Pirates, ky_tag1/2, sea), so those two are taken as leftovers (inferred: collision polygons can carry exit IDs too, `dStage_changeSceneExitId`, `d_stage.cpp:2298-2339`, and A_umikz's DZB was not parsed). I_TestM is also named by other test stages and by morocam's stage.dzs; main.dol was not scanned for stage names.
      - 22 of the 26 names appear only in the debug map select's data, `res/Menu/Menu1.dat` (loaded at `d_s_menu.cpp:314`); Ebesso, kazan, morocam and Mukao are not even there.
      - The only table-15 name in the decomp's code is "K_Testb", in `d_a_npc_mn.cpp:209-220`: it is only compared with the next stage (`chkPosNo`, `:1619-1629`), never set as one.
      - Why by table and not by reachability: a closure over SCLS exits alone leaves 54 real stages unreached, and would keep I_TestM through A_umikz's leftovers. Why not in `stagedata.Load`: item 127's `skipped` means the play scene can't load a stage, and the map select can load a test stage.
    - **Rule B: Ganon's Tower's refights drop no container (hearts only).** Each refight boss tests `dComIfGp_getStartStageName()[0] == 'X'` (the current stage's name, `d_com_inf_game.h:283`). In an X stage it sets its `*_TRIALS_CLEAR` event bit and tmp bit UNK_0480 and warps out with `dLib_setNextStageBySclsNum(0, room)`, never reaching the death that creates the container or sets STAGE_BOSS_ENEMY:
      - Gohma (`Btd`): `d_a_btd.cpp:906-911`; container `:1082`, boss flag `:2188`.
      - Kalle Demos (`Bkm`, proc BMD): `d_a_bmd.cpp:520-525`; container `:747`, boss flag `:562`.
      - Jalhalla (`big_pow`): `d_a_bpw.cpp:2154-2158`; container `:2856`, boss flag `:2891`.
      - Molgera (`Bwd`): `d_a_bwd.cpp:1021-1026`; container `:1704`, boss flag `:1766`.
      - A Bitem needs `isStageBossEnemy` and has no name check (`d_a_boss_item.cpp:24-41`); the X stages hold none, and the X path never sets the boss flag. Gohdan (`d_a_bst.cpp`) has no 'X' test and is in no X stage. The only X stages on the disc are Xboss0-3, on table 8. They are reachable, so their switches and enemies stay in the tables; only the hearts builder leaves their bosses out.
    - **Rule C: the pig's dig spot (`TagKb`) is an item source.** "TagKb" is `fpcNm_TAG_KB_ITEM_e`, 0x1A4 (`d_stage.cpp:583`, `f_pc_name.h:432`). Its parameters are item prm & 0xFF, item bit (prm >> 8) & 0xFF, enemy kind (prm >> 16) & 0xFF and switch prm >> 24 (`daTagKbItem_prm`, `d_a_tag_kb_item.cpp:9-13`). Grant path on GZLE01:
      - a pig (`fpcNm_KB_e`) calls `kb_dig` (`d_a_kb.cpp:1995-1997`; `d_com_static.cpp:381-386`), which runs `dig_main` (`d_com_static.cpp:389-449`);
      - `dig_main` acts only for a pig (`:408`) and an item that is not none (`:413`); it creates the item with `fopAcM_fastCreateItem(..., mItemNo, ..., mItemBitNo)` (`:420-423`), which packs the bit as a byte into the item's parameters (`f_op_actor_mng.cpp:1072-1076`). Then, if the spot's switch is not 0xFF, it sets the switch and keeps the spot, else deletes it (`:430-434`). A spot with an enemy kind sets the switch and deletes itself (`:439-443`);
      - the item keeps bit & 0x7F and is not created once that bit is set (`_daItem_create`, `d_a_item.cpp:227-233`); the spot is refused once its bit is set, unless the bit is 0x1F (`d_a_tag_kb_item.cpp:42-47`). So bit 0x1F needs no special case: the spot can come back, its item can't (inferred from the two together).
      - the bit is an s8 (`getItemBitNo`, `d_a_tag_kb_item.cpp:11`), sign-extended into the int `mItemBitNo` (`d_a_tag_kb_item.h:21`), and `_create` passes it unmasked to `fopAcM_isItem` for any item, Blue Chu Jelly included (`:42`). `isItem` asserts unless it is 0-0x4F, -1 or 127 (`d_save.cpp:1651-1652`), so a bit byte of 0x50-0x7E or 0x80-0xFE halts as the spot's room loads. Byte 0xFF is -1, which `isItem` reads as unset (`:1654-1656`); its item then gets 0x7F.
      - On the owner's disc the one heart-piece spot is in sea room 44 (Outset), SCOB#58, params 0xFFFF0207: item 0x07, item bit 0x02 of table 0. The room's other 9 spots are 2 red rupees and 7 enemy-kind spots with item none. So the decoder adds 1 piece and no key.
    - Built:
      - `itemsrc`: `ProcTagKbItem` and the kind `dig`, decoded as a field item is (Blue Chu Jelly's bit is a switch; item none places nothing), except that the bit is the whole byte: 0xFF is no bit, 0x80-0xFE is `bad` (halts), and a Blue Chu Jelly spot whose bit halts reports it. `BadFlag` now also covers a dig spot's negative bit. hearts and keys report such a spot as halting instead of counting it. The owner's disc is unchanged: its 10 spots use bytes 0x02, 0x03, 0x04 and 0xFF.
      - `hearts`: a boss in a stage whose name starts with 'X' is left out (`refight`); a Bitem or a placed container there still counts. Gohdan has no 'X' test but is left out there too, on purpose, as no X stage holds him.
      - `tables`: `playable` leaves the stages on save table 15 out before the four builders run, and the tables record them as `excluded` (name and why), beside `skipped`. The stage and room counts are of the stages kept. A disc with no stage outside table 15 fails the build. The report header has a line `left out N stages on save table 15 (STAGE_TEST, developer test stages): …`, apart from the "can't load" line. `ID` and the stage-file hash are unchanged: the listing is the same, and the rule is in code.
      - Schema 4. A schema change changes the tables' `Identity` (`identity.go:32-49`), and the hub takes every table feature (memory and live switches, puzzles, combat, keys and life; `TableFeatures`, `hub/features.go:134`) from a player whose tables are not the host's (`Hub.grant`, `hub/hub.go:574-584`). So both sides must run a build with this change: in a mixed session the player on the other build loses all of them, not only max life.
      - Not changed: `stagedata` still loads table-15 stages; `derive.LifeEnabled` (`derive/life.go`) and the merge's `deriveLife` (`savefile/derive.go`) still need the heart table to pass, which it now does on the owner's disc. Max life stays strict (owner's decision pending; the recommended option is kept): it is derived only when the table has exactly 44 pieces and 6 containers.
    - On the owner's disc with this build (`coop-tables --no-cache`, schema 4): 129 stages and 439 rooms, `Name` skipped, 26 stages excluded. Heart pieces 44 PASS (30 in stage files: chest 14, dig 1, item 1, salvage 14; 11 rewards; 3 unverified), heart containers 6 PASS (boss 5, boss_item 1: kazeB, kinBOSS, M_DaiB, M_DragB, SirenB, and Helmaroc King's Bitem in M2tower and the sea), hearts PASS, keys PASS (5 of 5 save tables), every Acceptance line PASS.
    - The switches and enemies sections changed only by the test stages leaving (compared as JSON with main's build, with every user in an excluded stage removed): switches 1683 → 1618, all 65 gone on table 15 (35 memory, 26 room, 3 zone, 1 dan), the other 1618 the same; switch users 5427 → 5028; undecoded users 417 → 365, all 52 in test stages; push blocks unchanged (21). Enemy entries 2096 → 1851, all 245 in test stages, the rest the same; entries 19048 → 18028; M6's INFO lines move with them: setID share 426 of 2096 (20.3%) → 397 of 1851 (21.4%), K1 classes not BOSS 355 of 1623 (21.9%) → 333 of 1403 (23.7%); the 5 duplicate keys are unchanged.
    - Tests: `TestDecode` (the Outset spot, an enemy-kind spot, a red rupee on a switch, bits 0xFF, 0x1F and 0x45, halting bits 0x50, 0x80 and 0xC5, Blue Chu Jelly with a plain bit and with halting bits 0x60 and 0x90), `TestDecodeAllBits` (a dig never places item none) and a `FuzzDecode` seed. Hearts' `TestBuildVanilla` adds a dig piece and Xboss0-3 with all four refight bosses and still gives 6 containers; `TestBuildFailures` adds a Gohma on table 8 in a stage not named X, which still drops one, and a Bitem and a heart piece in Xboss0, which still count (7 containers, 45 pieces): only the bosses of an X stage are left out. The tables fixture (`tablestest.Vanilla`) adds the dig piece, Xboss0 with Gohma and a table-15 K_Test2 with a piece, a key, a container and an enemy; `TestBuildVanilla` checks `excluded`, the counts and the unchanged Acceptance (the refight Gohma is still an enemy entry, so 10 of them); `TestPlayable` checks the rule and a nil stage; `TestBuildErrors` a disc of test stages only; `TestReportSections` the header line; `TestSchemaShape` schema 4.
    - Not verified:
      - Kalle Demos in Xboss1 (inferred): its container is created in `core_move` (`d_a_bmd.cpp:652-750`, run each frame from `:1595`), which has no 'X' test, after the core's whole death animation and 15 frames of the disappear actor (`d_a_disappear.cpp:21-28`). The warp is asked for on the first death frame, so the scene should unload first; neither the animation lengths nor the fade time were measured. Were it to spawn and be taken, it would set table 8's STAGE_LIFE (`d_item.cpp:590-603`), which no source counts: max life would be under-counted by that container for that player only, never granted twice.
      - The three minigame rewards still have no decompiled grant code, and `d_a_npc_ds1`, `gp1`, `kf1`, `p2`, `uk` and `sarace` are not decompiled: 44 is reached without them (inferred that none grants a piece). Exact totals don't prove each source right, as two errors could cancel; each of the 44 was matched to a named source, the islands from the sea grid and memory (inferred).
      - DmSpot0 (table 0, so kept) puts a blue rupee on item bit 0:0x02, the dig piece's bit. Retail does not load DmSpot0 (inferred), and it is no hearts source.
      - The owner's own run with this build, and M4's P3 on it.
138. **Switches shared with the Tingle Tuner sync while no GBA is linked (owner's decision; corrects item 29 and §6.2).**
    - On the owner's disc 56 switches (48 memory, among them DRC's key-door switches 0x3D and 0x3E and its Zenshut 0x3F, 5 dan, 2 room, 1 zone) were unsynced only because a Tingle Tuner switch (`agbsw0`, proc 0x1B6) also names them, and 5 more through ANDs fed by one of those. The owner decided they should sync when no GBA is linked.
    - Verified in the decomp, every switch write of the Tuner needs `mDoGaC_GbaLink` true on the frame it is made (`m_Do_gba_com.cpp:258-270`): `ExeSubMW` clears sw0 (`d_a_agbsw0.cpp:1117`) and returns at once while no GBA is linked (`:1074`); `ExeSubT` sets sw0 (`:1158`) inside `if (mDoGaC_GbaLink())` (`:1153`); `ExeSubS` sets sw0 and sw1 (`:1198-1200`, `:1216-1218`) inside `field_0x299 == 0 && mDoGaC_GbaLink()` (`:1193`), and its M_NewD2 room 0xC case only deletes the actor (`:1187-1190`). The other types (A, AT, M, A2, F2, F, R, B, D, FA, M2, M3) call no `on`/`off`/`revSwitch`: they read their switches and mail the GBA (`MailSend`, `:2537-2547`, which only queues the mail). `create` (`:149-275`) writes no switch. The names (`agbA`..`agbFA`, `agbTBOX` and `agbCSW` included, `d_stage.cpp:503-515`) are all this proc; the type, `home.angle.z & 0xFF`, picks the code, through `ExeSubTable`'s 15 entries (`:279-297`), which has no bound check. The GBA's answer reaches the Tuner actor, whose `SwitchOn` flips and sets switches (`d_a_agb.cpp:834-853`), called only under `mDoGaC_GbaLink` (`:1682-1684`); which switches the GBA program names there is not in the decomp, so the tables take them to be the actor's sw0 and sw1, either way (inferred). No write path is ungated.
    - Built (`wwapi/tables/switches`): a new writer class `gba`. Every `agbsw0` type 0-0xE is a `gba` toggle of sw0 and sw1; a type past the table is `unknown`, so it keeps its switches unsynced. A switch syncs when it has a latching setter and every other writer is latching or `gba`; it is then marked `gba` (synced only while no GBA is linked), as is, transitively, a switch a latching AND sets from such an input. A switch only `gba` writers write does not sync ("no latching setter"). The report adds the class to the writers line, a line `Synced only while no GBA is linked (shared with the Tingle Tuner): N (memory …, dan …, zone …, room …)`, and marks such switches `ok*` with `held while a GBA is linked (Tingle Tuner: …)`. `synctables.GBASwitch`, `GBAMemorySwitch` and `GBAScopedSwitch` give the mark to the runtime.
    - Schema 5: the JSON has the class and the `gba` field, and verdicts change. As with item 137, the tables' `Identity` changes, and the hub takes every table feature (`hub.TableFeatures`, `hub/features.go:123-134`; `Hub.grant`, `hub/hub.go:577-587`) from a player whose tables are not the host's, so in a mixed session the player on the other build loses all of them.
    - Built (the runtime guard): `addr.GbaCom` is `g_mDoGaC_gbaCom` (0x803B904C, size 0x130, sym:17367; its fields at `m_Do_gba_com.h:67-110`), and `snap.Read` reads its first 0x129 bytes once a tick (`snap.GBALink`). `GBALink.Linked` is `mDoGaC_GbaLink`'s own condition; the guard uses `MayLink`, a superset: `mEnable != 0 || field_0x128[0].field_0x0 != 0`. `mEnable` is set as the Tuner's upload starts (`onEnable`, `d_a_agb.cpp:491`) and the context once a GBA's checks out (`m_Do_gba_com.cpp:518`); only `mDoGaC_GbaReboot` (`:162`, `:168`) and `mDoGaC_Initial` (`:85`, `:124`) clear them. Without a GBA neither is ever set; with one, `MayLink` stays true through a dropped link until the game reboots it (a reset, or calling Tingle again), so a link that comes back between two ticks can't be missed. While it holds (`replica/gba.go`):
      - the GBA-dependent memory switches leave the replica's policy, which the differ (GAINS), the WORLD (a bind's too, and the join preflight), the planner and the M4 receiver share, and the WriteGuard shuts them (`Tick.GBA`);
      - the FrameWriter's recheck after the beat aborts a batch whose snapshot saw no GBA if one may be linked by then. It runs whenever the tables share any switch the replica syncs, in any store, with the Tuner (`WriterConfig.GBA`), so an M5 batch of dan or zone bits is rechecked too, even with no GBA-dependent memory switch in the policy;
      - the scoped sender leaves the GBA-dependent dan, zone and room switches out of SCOPED, whole words included, and the M5 receiver doesn't write them: a dan or zone bit waits in its store, a room edge is dropped, as edges are. They are logged `not written while a Game Boy Advance may be linked: …` once each and not counted as refused;
      - the log says once, as it starts and as it ends, `co-op: a Game Boy Advance may be linked to this game (the Tingle Tuner; …): the N switch(es) the stage tables share with Tingle Tuner switches are neither proposed nor written until the game drops the link (a reset, or calling Tingle again)` and `co-op: no Game Boy Advance is linked to this game any more: …`, and `/status` adds a `Tingle Tuner:` line (`gbaStatus`). The startup line counts them: `… N latching memory switch(es) in M save table(s) sync (K of them, shared with Tingle Tuner switches, only while no Game Boy Advance is linked)`.
      - A switch that rose in a game while a GBA may have been linked is never proposed later (it may be the Tuner's). A memory switch is in its differ's baseline, so it is never a gain. A dan or zone bit that rose in Link's scope while held, or that a scope Link entered while held already had, is tainted in the sharing Differ (`Differ.HoldScopes`), which masks it out of every SCOPED, the whole words of the scope refresh and of a room entry included, until the game clears it. A room edge that rose meanwhile is in the scoped baseline and never sent. A drop of the differ's baselines (the title, another file, a save state, an unreadable snapshot) forgets both: the WORLD after it carries a memory switch with the rest of the file, the next whole words a tainted bit. A bit the Tuner sets in the zone of a room Link is not in is not seen to rise; it is proposed when Link enters that room with no GBA linked.
      - Nothing changes for a partner's game with no GBA, and the hub's policy is unchanged: each replica guards its own game. `/status` and the log count the switches the replica syncs: the memory switches its policy opens, and the dan, zone and room switches.
    - On the owner's disc (`coop-tables --no-cache --section switches`, schema 5, against schema 4 on 15348f0): switches synced 767 → 828: memory 253 → 306, dan 96 → 101, zone 12 → 13, room 406 → 408; 61 of them marked `gba` (memory 53, dan 5, zone 1, room 2): the 56 above, 4 more whose AND writer had been demoted for an input among them, and memory t7 0x31, an AND of one. None lost. Writers: latching 1579 → 1585 (6 ANDs), unsafe 1157 → 249, `gba` 902, event 119 and unknown 4 unchanged; every `agbsw0` on the disc is type 0-0xC. Dragon Roost Cavern: 38 → 55 synced, with `ok* memory t3 0x3D  set by keyshut`, `ok* memory t3 0x3E  set by keyshut` and `ok* memory t3 0x3F  set by Zenshut`, each `held while a GBA is linked (Tingle Tuner: agbA)`.
    - Tests: `TestTingleTuner` (a key door, agbMW and agbCSW sharing latching switches sync and are `gba`; agbA alone and agbA beside a timed crystal don't sync; a type past `ExeSubTable` stays unknown; ANDs of a GBA input are `gba`, transitively; dan and zone routing), `TestMixedSetters` (latching and a Tuner in another stage), `TestReport`, `checkFixedPoint` (the `gba` mark is the fixed point) and `FuzzBuild`/`FuzzDecode` seeds; `TestGBASwitch` in `mods/coop/synctables` (was `internal/coop/tables`) (the accessors on the fixture, with `tablestest.TingleTuner`), a `FuzzDecode` seed and `TestSchemaShape` schema 5; `TestGbaComLayout` (the address against its neighbours and the header's arithmetic); `TestGBALink` and `TestReadDecodes` in `snap` (the condition, the superset, one read of 0x129 bytes); `FakeGame.SetGBALink`; in `replica`, `TestGBAGuardMask`, `TestGBAHoldsMemorySwitches`, `TestGBAHoldsProposals`, `TestGBAHoldsScopedWrites`, `TestGBAHoldsScopedProposals` (a dan bit set while linked is not proposed after, until a drop; one set after is), `TestGBATaintsScopeEntry` (a zone bit there as Link enters the room while linked), `TestGBARecheck`, `TestGBARecheckScoped` (tables whose only GBA-dependent switches are dan, zone and room ones), a `FuzzWriteGuardCheck` seed, and `FuzzReconcileM5` on the guard's tables with an op that links and unlinks a GBA (two seeds), auditing that no switch shared with the Tuner is written (`zoneAt.gba`) or proposed (GAINS, SCOPED) while a GBA may be linked; `TestGBAStatus` and `TestCoopISOTables`'s startup line.
    - Not verified: which switches the GBA program names in its answers (`SwitchOn`'s), taken to be the mailing actor's; and no GBA has been linked in Dolphin, so the guard has run only on the fake game. A `peek 0x803B904C 4` on the owner's Dolphin should read 0 in its first byte, and `peek 0x803B9174 1` (+0x128) 0, in any game that never called Tingle.
139. **A key door reads its switch only at a room change (found on the owner's P2 run, F13; corrects §6.1, §6.4, P2 and item 6).**
    - On the owner's machine B's key was taken at its room change, as P2 said, but the key door B then walked to still showed its lock and offered no "Open" until `/refresh` made the door anew. B had no key left, so nothing opened it.
    - Verified in the decomp: a key door reads its switch only in `actionInit`, through `setKey`, which takes the lock off once the switch is set (`daDoor10_c::setKey`, `d_a_door10.cpp:27-50`; `daDoor12_c::setKey`, `d_a_door12.cpp:52-73`; `daKddoor_c::setKey`, `d_a_kddoor.cpp:17-25`; `actionInit`, `d_a_door10.cpp:747-759`, `d_a_door12.cpp:765-773`, `d_a_kddoor.cpp:900-908`). `execute` goes back to `actionInit` only after a frame on which `checkExecute` returns 0 (`d_a_door10.cpp:811-812`; `d_a_door12.cpp:830-831`; `d_a_kddoor.cpp:947-948`).
    - `checkExecute` returns 0 on the frame `mStayNo` differs from the door's `mRoomNo2`, which each execute sets at its end (`d_door.cpp:164-165`; `d_a_door10.cpp:829`, `d_a_door12.cpp:848`, `d_a_kddoor.cpp:961`). It also returns 0 while Link is in neither of the door's rooms (`frontCheck`, `d_door.cpp:103-111`) or neither room is shown (`adjoinPlayer`, `d_door.cpp:63-75`, which checks room display, not distance). Otherwise it returns 2 (its status and event cases aside, `d_door.cpp:158-162`), and the wait action never reads the switch. So a door of the room Link is in keeps the lock it read as he came in until his next room change, or until its rooms are hidden and shown again.
    - M3 writes the loaded table's memory switches after the room hold, about 2 s after a room change (items 121, 126, 129). By then every door of the arrival room had run `actionInit`. The derive counted the door as soon as its switch was written and took the key, so the lock stayed with no key to open it: a phantom lock until the next room change.
    - Item 6 and §6.1 said the lock is read again "whenever the door returns to `actionInit`, which happens every frame the local Link is not adjacent", and that it disappears when the receiver next approaches the door. That holds only for a door of a room Link is not in. Item 140 corrects item 6.
    - Built (`mods/coop/derive/keys.go`, `replica/reconcile.go`): the replica tells the Deriver of every bit the merge path set in the live memBit (`Deriver.WroteLive`, from `Result.Wrote`). A key door whose switch is among them is held: `KeyFacts.Held`, and keys(t) counts the doors open less those held. A door is held only if the last tick had its table loaded, with Link in a stage where the door is placed, and its switch set in neither copy. So a door the receiver's own game opens is never held: its game already spent the key, and co-op writes nothing. Nor is a switch written only to a cold table's saved slot, or one in another stage of the loaded table: its door is made reading it as Link goes in (`getSave`, `d_save.cpp:1506-1509`). A held door is counted again:
      - on the first tick whose `mStayNo` differs from the one the first tick after the write read. Taking the room from the tick after the write, not the write's own tick, covers a room change between the snapshot and the heartbeat, when the door may have run `actionInit` before the write;
      - when Link leaves its stage or table (the door is made anew), when its switch reads clear, and on every reset of the Deriver (a failed read, a save state, the title or file select, another file, a restart of the tool), as `extra` is;
      - when, once settled, the count has fallen by more than the doors counted open since the derive last settled the table explain: the receiver opened the locked door with the key it kept, and `keyInit` took it (`d_door.cpp:433-447`). No derive write gives that key back. `keyInit` acts only from the door's front room (`d_door.cpp:435`), and Link goes through in the door's own event (`d_a_door10.cpp:545-600`; `openInitCom`, `closeEndCom`, `d_door.cpp:271-327`), when the Deriver is not IDLE and the room-change release runs first. So this release covers an opening with no room change after it, as on a door whose two rooms are one;
      - a key-counter write of the derive's own is not expired while the loaded table's `mItemKeyNumCount` reads nonzero: the HUD takes the counter only on a frame `dMeter_Execute` runs (`dMeter_keyMove`, `d_meter.cpp:5247-5258`, `:7395`). Expired after `DefaultInFlight` in a paused or stalled game, the -1 landing late read as a key spent on a held door, which was counted with its lock still up, and a second -1 followed.
    - The log says `small keys, table 3 (DRC): door 0x10 is counted after your next room change: its switch is written, but the door shows its lock until then, and its key stays`, then `door 0x10 counted: your room changed, so the door looked at its switch again and dropped its lock` (or `you left its stage, …`, or, with no room change after an opening, `this game spent a key on it`). `/status` adds `(door 0x10 counted after your next room change: its lock stays until then)` to the table's line (`derive.KeyStatus.Held`). A derive write planned while a door is held says `(n counted after your next room change)`.
    - The cost: a partner's door takes the receiver's key one room change later than before. So §6.4's known exploit (a key spent on another door while a partner's door waits) has a longer window: until the receiver's next room change after the switch lands. Item 94's `extra` is unchanged.
    - *Limits, not fixed:*
      - A bounce of two room changes between two ticks (1 → 2 → 1) leaves `mStayNo` as it was. The door stays held until the next room change, though the doors there have looked again, so the receiver keeps the key longer (the safe side).
      - A reset counts every held door at once. After a failed read, or a save state loaded while a door is held, the door can again show its lock with its key gone, until the next room change or `/refresh`. A failed read is rare, and a save state restores the doors as they were saved.
      - A door's rooms hidden and shown again (a door hides and shows rooms, `d_door.cpp:282`, `:305`) also make it look again, without a room change. The hold waits for the room change all the same.
    - Tests:
      - `fakegame.KeyDoor` models the door. It is made with its stage, runs `checkExecute`, `actionInit` and `setKey` as `daDoor10_c` does, and does not run while an event or the pause menu is up. `KeyDoor.Open` spends a key as `keyInit` does, from the door's front room only (`d_door.cpp:435`), and returns `ErrNoKey` for a lock at no key from either side. It does not move Link. `TestKeyDoor` covers it, a key from each side included.
      - `TestReconcileM4Keys` runs door 0x10 in the receiver's game. The switch lands while the door still locks, and the key is kept; the lock and the key both go at the next room change. Every tick it checks that the door never shows its lock over a set switch next to Link while the game holds no key. On origin/main that check fails, as F13 did. "a key spent on a door held" opens the held door with the kept key from its front room, and the derive writes nothing; "…, then through it" goes through to the other room right after, as the game does, and the door is counted at that room change.
      - `TestCoopSessionM4` gives both games DRC's four key doors. Its script now opens door 0x10 with the players' only key from the door's front room, as on the owner's run: B keeps its key, and the door its lock, after the room change that brings the switch, and both go at the next one. Every step it fails a player whose door stays shut to it with no key for more than 30 ticks (`coStuck`). On origin/main it fails there with `door 0x10 shows its lock over its set switch next to Link, with no key to open it`. Its random play opens a door next to Link through its actor, and treats a held door as a door that still waits.
      - In `derive`, `TestPhantomFreeCoupling`, `TestDoorOpenedRemotely` and `TestUnknownKey` now change room before the door's key goes. `TestHeldDoor` covers the releases and the doors never held, and "a late counter" a -1 a stalled game takes after `DefaultInFlight` while a door is held (the door stays held, and no second -1 follows), and `TestStatus` the `/status` line. `FuzzDeriver` adds co-op's live writes and room changes, and checks that every held door is an open one.
140. **Item 6's account of key doors was wrong (corrects item 6).** A key door does not go back to `actionInit` "every frame the local Link is not adjacent": `adjoinPlayer` is room display, not distance (`d_door.cpp:63-75`). It goes back on the frame Link's room changes, and while Link is in neither of its rooms or neither room is shown (`checkExecute`, `d_door.cpp:155-171`). So a door of Link's room keeps its lock while he stays in that room. Item 6's conclusion stands: keys are derived from the receiver's own applied flags. Since item 139, a switch co-op wrote into the live copy counts only from the receiver's next room change.

*M6.0: the kill union of keyed enemies and the K1 light-shrink (§7.2, §8.2)*

141. **The light-shrink, seen live (found on the owner's machine, main 506d861).** One byte, 0x01, written at actor +0x822 of three ChuChus of TF_06 room 2 (enemyice +0x81C, plus 6: the s8 `mLightShrinkTimer`) shrank and deleted each. A 16-bit write of 0x0001 at +0x822 did nothing: it puts the 1 in the pad byte at +0x823 and 0 in the timer. The room's ALLDIE then set dan switch 0x95 65 frames later, and M5 wrote it to B (`set the partner's dan switch 0x95`). So the ChuChu is verified (`enemies.Class.K1Verified`), and every timer write is one byte (`FrameWriter.ApplyActors`, `WriteGuard.checkActor`).
    - **K1 on the owner's machine, 2026-10-15.** The same one-byte write light-shrank and deleted a Miniblin at +0x7EA (SubD71 r0: the timer to 49, then a rupee drop), a Poe at +0x88A (SubD71 r1: the timer to 86, then it burst and dropped a heart), a Boko Baba at +0xA02 (kindan r1: the timer to 89), a Peahat at +0x99E (kaze r2, keyed set 2: its zone recorded actor 2 dead, through `enemy_ice`'s `onActor`), three Bokoblins at +0x126A (TF_06 r3) and three Moblins at +0x2A5A (`M_Dai` r1, keyed 0-2: the zone recorded 0, 1 and 2, then the room's ALLdie set memory switch 0x03).
    - The Darknut at +0x152A: raw writes deleted it but left its sword (proc 0x1D5, BOKO) floating with CARRY set. This build's `enemy-kill 2917 6 --unverified --yes` on a `kaze` r8 Darknut (keyed set 0) went through the receiver's path: the timer climbed to 91, the actor went, `room 8's zone slot 0 records setID 0 dead: true`, and its sword's CARRY was cleared and the sword fell to the floor (`shots/m6-tn-after.png`). So its `mBokoPcId` (+0x1410) names the sword in GZLE01 too.
    - `peek-demo` read Link's `mDemo.mDemoType` (+0x304) 0 at idle, 2 during a stage-entry event (Cave02 to `M_NewD2`), then 0 again (item 146).
    - So `K1Verified` is set for TN, PH, PT, PW and BO, the Darknut's weapon field is verified (`actors.Class.WeaponVerified`), and `actors.DemoTypeVerified` is true, each with a comment citing this check. Every K1 class with an enemyice is now verified: the coverage has no "after live checks" part and the startup line no wait. Kargaroc and Morth keep their rules (ice-only death; never a target). The unverified path stays for a class added later, and tests keep it covered by making a class unverified (`actors.SetVerified`).
    - The check found that main's dispatch did not list `enemy-kill` and `peek-demo`, so both printed `Unknown command`. They are listed now, and `TestCoopDiagDispatched` parses `main.go` and checks that the case calling `runCoopDiag` names exactly `coopDiagUsage`'s keys.
142. **§7.2's K1 details, corrected against the decomp.**
    - The timer is an s8 at enemyice +6: `c_damagereaction.h:16` (§7.2 cited :12-16; :15 is `mFreezeDuration`), with a pad byte at +7 (:17).
    - The Bokoblin prelude (+0xB30, +0xB34 = 2) and the Moblin one (+0xDD4, +0xDD5 = 2) do nothing once the timer is set: both classes run `yari_off_check` after `enemy_ice`'s early return (`d_a_bk.cpp:4159-4169` against `:4420`; `d_a_mo2.cpp:3380-3388` against `:3570`). A Bokoblin holds its BOKO with CARRY set (`fopAcM_setCarryNow`, `d_a_bk.cpp:4220-4224`) and keeps its process ID in `m1200` (`d_a_bk.h:112`), so M6 clears CARRY on that weapon instead. The Moblin's spear is in its model: no prelude.
    - The Stalfos's +0x1DCC is `mHeldWeaponEntityId` (`d_a_st.h:103`), not a flag; its own light-arrow hit cancels the weapon's carry (`d_a_st.cpp:1570-1577`), which M6 does the same way.
    - The Darknut's weapon field (`mBokoPcId`, +0x1410, `d_a_tn.h:122`) is the D44J01 layout, so it was unverified with the class until K1 saw it name the sword (item 141).
143. **Keyed now, placement later (a decision; corrects §7.2's scope).** M6.0 syncs only keyed deaths: what the game records, with no protocol change (`wire.Schema` 1, `network.ProtocolVersion` 2 and `tables.SchemaVersion` 4 unchanged; the kill index is built in memory and kept out of the cache's JSON). On the owner's disc (tables schema 4) that is 333 of the 1403 K1 entries not BOSS (23.7%). Of the 237 rooms with K1 enemies, 68 have every K1 entry keyed, but "all keyed" counts K1 entries only: 48 of them hold nothing but enemies M6.0 removes on B, so only those can be emptied for B (item 160). Unkeyed enemies, 76%, all of the sea, Savage Labyrinth and the caves among them, stay each player's own until M6.1, a second PR that matches them by placement behind its own feature bit. `FeatureCombat` was already a table feature (`hub/features.go:39`, `:134`), so a grant implies equal tables.
144. **No observation path in M6.0 (corrects §7.2's "Detection").**
    - A Kargaroc's own death deletes it without `onActor` (`d_a_bb.cpp:1969-1972`, `:2293-2294`), so it syncs only when it dies on ice or by a Light Arrow (`enemy_ice`, `c_damagereaction.cpp:159`, `:369`). That is a documented gap (K8).
    - Morth is off the allowlist: every placed one is a generator, types 0/1 spawning children and returning `cPhs_ERROR` (`d_a_ks.cpp:1582-1610`), type 6 spawning them and deleting itself (`:1219-1283`). Its children have setID 0xFFFF.
    - A keyed Stalfos killed after it splits is not recorded by vanilla either: the upper body it spawns has setID 0xFFFF, and the placed lower body deletes itself without `onActor` (`d_a_st.cpp:1676-1684`, `:2551-2566`).
145. **The gates M6 adds (§7.2).**
    - `mInitState` 2 (base_process_class +0xC): `fpcEx_Execute` runs a process only in that state and when it is not paused (`f_pc_executor.cpp:31`); the state is set once the process is in its layer's and line's execute queues (`fpcEx_ToLineQ`, `:49`), and 3 means it is leaving them (`:65`, `f_pc_deletor.cpp:89`). An actor is queued in `g_fopAcTg_Queue` before its class's create runs (`f_op_actor.cpp:259-263`), so the walk sees actors still creating.
    - `enemyice.mMode` 1 and its freeze timer 0: mode 0 means `enemy_ice` has not run (it sets the shrink's scales, `c_damagereaction.cpp:178-198`), 2-3 frozen.
    - Neither CARRY (Link holds it) nor BOSS status; `eventInfo.mCommand` 0.
    - A ReDead neither holding Link (`MODE_ATTACK`) nor waiting on its switch (`MODE_SW_WAIT`, whose `_execute` returns before `enemy_ice`, `d_a_rd.cpp:1621-1630`); a Floormaster in none of modes 13-17, its grabs and Link's start demo (`mode_tbl`, `d_a_fm.cpp:2306-2420`).
    - Health above 0. A ReDead's own death sets its group to ENV and calls `onActor` on every frame once its death animation has ended, then counts 300 frames before it deletes itself (`d_a_rd.cpp:836` sets the timer in `modeDeathInit`, `:840-854` is `modeDeath`); this game's zone bit says nothing of whether the partner killed it (item 147).
    - The home room's zone exists and its slot names it back: `onActor` asserts the zone (`d_save.cpp:1678-1679`), so a shrink that ends after the zone went would halt the game. It is checked as the target is planned and again just before the write (`actors.Revalidate`), and the fake game fails any `onActor` with no zone.
146. **Link's demo gate fails closed until verified.** `mDemo.mDemoType` is a u16 at daPy_py_c +0x304 (`d_a_player.h:499`; `daPy_demo_c` +0, `:153`), an offset of the decomp's struct layout, first read live in K1 (item 141: 0 at idle, 2 during a stage-entry event). It must read 0 in normal play and nonzero while a Floormaster or ReDead holds Link and during the Forsaken Fortress Moblin's holdup, which the Moblin sets on Link directly (`changeOriginalDemo` and `changeDemoMode(DEMO_HOLDUP_e)`, `d_a_mo2.cpp:2829-2830`; `d_a_player.h:517-523`) while also ordering an OTHER-type event until it is accepted (`d_a_mo2.cpp:1814`, `:2017`, `:2048`, `:3405-3411`). With `actors.DemoTypeVerified` false, every light-shrink is held as `link demo unverified`, counted in `/status`, and the zone writes go on; K1 set it true. A grab or the holdup reading nonzero is left to K7. `peek-demo` reads it.
147. **Only the partner's store is asked.** The light-shrink targets an enemy whose key is in the store the hub relayed, never one whose bit this game's own zone has: that bit may be this game's own enemy dying (item 145). The hub relays a SCOPED to every player in the scope but its sender (`world.Scopes.Apply`), and sends a player entering a stage what is stored there; the store then holds what either player recorded, which is the union §7.2 wants.
148. **The weapon goes after the timer.** The timer is written first and read back, and only a nonzero read lets the weapon's CARRY go (`WriteGuard.checkActor`'s (W) needs the carrier's (T) of the same tick, `noteShrunk`). Once the timer is set the carrier's execute returns before the code that keeps the weapon in hand (`d_a_bk.cpp:4159-4169`), so a late release is harmless; a timer refused leaves the weapon held, so no live Bokoblin stands with `m0B30` 1 and its weapon let go (`d_a_bk.cpp:4220-4224`). Only the CARRY bit is cleared: `fopAcM_cancelCarryNow` also sets the room layer and zeroes `shape_angle.z` (`f_op_actor_mng.cpp:1366-1376`), which a Go write leaves (inferred harmless: the weapon lies where it fell).
149. **Hidden neighbours, and the rooms' status in the fake game.** A dead-actor byte is written into the zone of any room whose status is loaded and settled (0x01 set, 0x02 and 0x04 clear), a hidden one too (`Tick.killClosed`, `FrameWriter.killHolds`). The FakeGame now models the loaded flag: `EnterRoom` sets 0x01 on the rooms it loads (`loadRoom`'s scene, `d_stage.cpp:233-239`, taken at once), a stage load clears every room's flags (the old play scene's room scenes are deleted, `d_s_room.cpp:120`), as does a zone removed, and `ReloadRoom` leaves the room loaded. `TestShutter`'s expected flags changed accordingly; the M5 room clocks, which follow flags 0x01-0x08, start at the same tick as before. The fake also models the actor queue, placed enemies created and deleted with their rooms, `enemy_ice`'s light-arrow death, a ReDead's dying, a Kargaroc's unrecorded death, a create that fails, a block freed and reused, and Link's demo (`fakegame/actors_m6.go`).
150. **The hub's kill verdict (`hub.Config.SyncedKill`, `hub.Filter`, `hub.KillFilter`).** As for M5's switches, the host's hub keeps and relays only the kill bits its tables sync, and each writing replica's sender proposes only those (`Differ.ShareScopes` with `hub.Filter`). `hub.ScopeFilter` still passes every kill bit; the session's filter is `Filter`. A nil verdict keeps none (a hub test whose config had none now sets one). `hub.Status` adds `CombatAllowed` and `KillStores`.
151. **Room clear, read again (corrects §7.2's "Consequences").** `fopAcM_myRoomSearchEnemy` does not count enemies: it returns player 0's grabbed actor if that is an enemy, else the first `fopAc_ENEMY_e` actor in the room's layer, or NULL (`f_op_actor_mng.cpp:1272-1294`); every caller only tests it against NULL. So a room-clear chest, door, shutter, stair or fire on B (the callers §7.2 lists) waits for B's own unkeyed enemies there: the blocker in the 14 mixed rooms, until M6.1. The design's "covers unkeyed spawns" through the room-clear switch holds only where the room-clear sets a switch that M3 or M5 syncs (ALLDIE's, `d_a_tbox.cpp:1044`).
152. **The actor walk is a package (`internal/coop/actors`).** `actors.Read` takes the M0 diagnostic's walk and checks more: a node's `mpData` must be `&g_fopAcTg_Queue` (`c_list.cpp:40`) and its `mbIsUse` 1 (`c_tag.cpp:74-80`), or the walk stops; it takes no more nodes than the queue's `mSize`; a `g_fopAc_type` of 0 is an empty walk (`f_pc_base.cpp:28-33`). It reads each actor's fields M6 judges (`mInitState`, params, home, status, parent, event command, attention flags) and, for a K1 class, its enemyice, gated mode and weapon field. The `actors` diagnostic prints them and each K1 actor's verdict. `enemy-kill <pid> [watch-secs] [--unverified] --yes` light-shrinks one actor through the receiver's own gates, re-read and guard (it takes unkeyed actors and ignores Link's demo gate, for the live checks), and `peek-demo [watch-secs]` reads Link's demo type; main's dispatch lists both (`TestCoopDiagDispatched`, item 141). `addr` gains the offsets with struct checks (`TestM6Offsets`), and the diagnostic's `k1Classes` table is gone: the classes come from `enemies.Classes` (`actors.Classes`).
153. **Coverage in `coop-tables`.** The acceptance block gains `INFO  M6 kill coverage` (`enemies.KillCoverage.Line`), and the enemies section a coverage report by class and by stage (`TestCoverageGolden`; `FuzzBuild` checks that every kill-mask bit is a Keyed K1 entry's, not BOSS, Morth or Shared, and that the counts add up). On the owner's disc the line reads as §7.2 quotes it.
154. **Acceptance and tests.**
    - `TestCoopSessionM6` runs two FakeGames with the M6 enemies, a real `network.Server` with the hub, and two writing pipelines: the same-room kill with the weapon let go, the hidden neighbour, another stage and back, both leaving, unkeyed, BOSS and Kargaroc deaths that never travel, a Kargaroc's light-arrow death that does, the pause menu and a ReDead's switch wait holding the shrink, a Darknut made unverified for the test recorded and never shrunk; then 1000 random steps with drops, reconnects and a host restart; then both entering afresh and converging on the union. `coAuditM3` accepts M6's bytes only by `coAuditM6Kill` and `coAuditM6Heap`, and `coM6Judge` checks each against what the partner's game recorded; a tap checks every SCOPED KILL.
    - Unit tests: `TestReconcileM6ZoneOR`, `…Shrink`, `…Gates`, `…Revalidate`, `…ZoneGoneAfterBeat`, `…Ever`, `…SaveState`, `…PlanCopiesStore`, `…StoreNotLocal`, `…WeaponOrder`, `…LinkHoldsStick`, `…WeaponTakenUp`, `…Unverified`, `…DemoUnverified`, `…StoreOffGame`, `…Status`; `TestWriteGuardKillSlot`, `…Actor`, `…Weapon`; `TestWeaponHeld`; `TestHubKillFilter`, `TestHubKillStoreLifetime`, `TestHubCombatNeedsEqualTables`; the actors package's walk, gate-matrix and re-read tests; `TestEnemiesLifecycle` and its neighbours in the fake game; `TestEnemyKill`, `TestPeekDemo`, `TestCoopDiagDispatched`, `TestLogCoopCombat`, `TestCombatStatus`, `TestHubCombatStatus`.
    - Fuzz: `FuzzWalk`, `FuzzRevalidate`, `FuzzReconcileM6`, `FuzzWriteGuardActor` (each against an independent reading of the rules; for `FuzzReconcileM6` see item 161), and `FuzzHubFrames` with a kill verdict.
    - K1-K10 are the owner's checks (§8.2).
155. **Left for later, and residual risks.**
    - M6.1: unkeyed enemies by placement (a feature bit, a PKILL scoped kind, a death detector with per-class signatures).
    - The owner's K7: `peek-demo` nonzero during the grabs and the holdup the demo gate exists for.
    - Between `Revalidate` and the timer write lie a few reads, not a frame: the game frees an actor at the next frame's start (`f_pc_manager.cpp:280`), after the write (inferred, as §4.2.1's window).
    - The light-shrink duplicates the item drop and the death switch on B, and skips each class's own death bookkeeping (§7.2 "Consequences").
    - `/status`'s refused count is a level: the store's bits this side's tables do not sync, as the last tick saw them.

156. **A weapon is let go only from the carrier's hand (found in review).** The first build took any BOKO with CARRY set whose process ID was in the carrier's weapon field as the weapon it held. But a Bokoblin's `m1200` keeps the stick's process ID after the stick is knocked away: `yari_off_check` cancels the carry and zeroes `m0B34` and `m0B30` only (`d_a_bk.cpp:100-137`), and only `daBk_wepon_view_check` rewrites `m1200` (`:826-837`). That runs in the search actions alone (`:1182`, `:1422`, `:1614`, `:2067`) and is skipped while `m02CC` is set (`:2812`). So during the get-up after the very hit that knocked the stick away, Link could pick the stick up (CARRY set, `Mode_PLAYER_CARRY_e`, `d_a_player_grab.inc:330-339`), and a partner's kill of that Bokoblin then cleared CARRY on the stick in Link's hands: `daBoko_c::procCarry` drops it at Link's feet while Link's grab still points at it (`d_a_boko.cpp:636-705`). CARRY alone is no sign of a held weapon either: the BOKO sets it on itself while it flies (`:406-410`, `:647-649`).
    - Now a weapon counts as held only when both sides say so (`actors.HeldBy`), in the plan (`WeaponOf`), in the re-read (`Revalidate`) and in the WriteGuard's (W). The BOKO's u8 `mNowMode` (+0x2B8, `d_a_boko.h:128`) must be `Mode_ENEMY_CARRY_e` (2, set by its `procWait` when an enemy takes it up, `d_a_boko.cpp:382-389`). The carrier's own record must say it holds one: a Bokoblin's int `m0B30` (+0xB30, `d_a_bk.h:74`) nonzero, as the game's own light-arrow hit asks before it lets the stick go (`d_a_bk.cpp:3601-3604`; set with CARRY at `:2821`, `:4223`, zeroed at `:137`, `:4414`); a Stalfos's s8 `m1DD0` (+0x1DD0, `d_a_st.h:104`) 2 or 10 (`d_a_st.cpp:2006`, `:2051`; it is 1 until its first draw sets CARRY, `:2271-2273`, and 0 once the mace falls, `:2022-2025`).
    - A weapon field that names a BOKO the carrier does not hold is ignored: the carrier is shrunk and the BOKO left alone. A carrier whose record says it holds a weapon the BOKO does not yet agree with (a Stalfos's mace before its draw, a stick in its first frame of carry) waits a tick. The Darknut has no such record in M6.0: its weapon needs only the BOKO in `Mode_ENEMY_CARRY_e`, as K1 saw (item 141).
    - The reverse gap: a carrier planned with no weapon can take one up before the write (a Bokoblin walks to the stick its `m1200` already names and calls `fopAcM_setCarryNow`, `d_a_bk.cpp:2821-2833`, or takes a spawned one, `:4219-4224`), and a timer written then left that weapon CARRY on a deleted carrier. `Revalidate` now holds such a carrier when its record says it holds a weapon or has one between hands (for a class without a record, when its weapon field changed), and the next tick plans it with the weapon.
    - Tests: `TestWeaponHeld` (actors: knocked away, in Link's hands, a record that disagrees, taken up after the walk, a Stalfos before its draw), `TestReconcileM6LinkHoldsStick`, `TestReconcileM6WeaponTakenUp`, the new cases of `TestWriteGuardWeapon`, `FuzzWriteGuardActor` with the stick knocked away or in Link's hands, `FuzzRevalidate`'s oracle (`m0B30` still 0) and `FuzzReconcileM6`'s new op. The FakeGame keeps `m0B30`, `m1DD0` and the BOKO's `mNowMode`, and gains `DropWeapon`, `GrabWeapon` and `TakeWeapon`. K2 adds the live check.
157. **A KILL store is dropped off the game (found in review).** `pruneScoped` kept KILL stores while Link was off the game. But the hub places such a receiver nowhere (no PRESENCE in game) and drops the stage's stores when the partner is not in it (`world.Scopes.vacate`), and it sends no drop. After a reload of the same stage (Continue after a game over, or any entry that wipes the zones, `d_stage.cpp:123-125`), the old store was ORed into the fresh zones and the respawned enemies light-shrunk, though nobody had kept the stage alive. KILL stores now go off the game as DAN and ZONE stores do. A partner still in the stage loses nothing: the hub sends every stored KILL of the stage again when the receiver's PRESENCE enters it (`Scopes.SetPresence`). `TestReconcileM6StoreOffGame` reloads the stage and finds nothing written.
158. **A save-state load forgets what was shrunk (found in review).** A load restores the actor heap and `fpcBs_MakeOfId`'s counter (`f_pc_base.cpp:36-40`). So an enemy shrunk after the state was saved comes back alive with the same process ID and a timer of 0, and a new actor can take a process ID the receiver remembers; `ever` kept such an enemy alive for good. On a tick whose gates saw the heartbeat go back (`gate.Status.Regressed`), the receiver now clears `ever`, `held` and `demoHeld`. `Eligible`'s timer gate still keeps a shrink under way from a second write. `TestReconcileM6SaveState`.
159. **The plan copies the store (found in review).** `reconcileM6` gathered the partner's stores by slice header, and the WriteGuard's `Kills` and `Allow` closures read them in the write phase, after `r.mu` is released, while `Handle` (`onScoped`) ORs new bits into the same words under `r.mu`. That was a data race, and a verdict that could count bits that came after the plan. The plan now clones each store's words (`slices.Clone`). `TestReconcileM6PlanCopiesStore` sends a bit of the same word after the plan and checks that the guard does not take it. The race detector needs cgo, which the owner's Windows toolchain lacks, so the check matrix runs no `-race` pass.
160. **"All keyed" is not "clears on B" (found in review; corrects item 143 and §7.2).** `KillCoverage.AllKeyed` counts a room whose every K1 entry without BOSS status is keyed. It never looked at the room's other enemies, nor at whether its keyed entries are removed on B, and a room-clear on B (`fopAcM_myRoomSearchEnemy`: any `fopAc_ENEMY_e` actor, `f_op_actor_mng.cpp:1272-1294`) waits for those too.
    - The coverage now also counts `Clearable`: the all-keyed rooms whose every enemy-group entry, of any class and on every layer, is a synced key of a verified class that records its own death (so not BOSS and not a Kargaroc). ENV-group entries (Morth) are not counted by room-clear and do not block.
    - On the owner's disc (tables schema 4, every K1 class verified) that is 48 of the 68. The other 20 also hold non-K1 enemies (Wizzrobes in `kaze` r0, r3, r7 and r10; Bubbles in `M_Dai` r4, r9, r13 and r17, `Siren` r14 and `kaze` r16; OQ in `kindan` r12 and `ma2room` r3; Dexivines in `kaze` r3, r7, r11 and r16 and `kindan` r12; `SS` and `SITEM` entries in `kindan` r0 and r8), a BOSS Moblin (`I_SubAN` r11), or a Kargaroc (`I_SubAN` r9, `kindan` r13, `M_NewD2` r3, `Siren` r18). Before K1, 6 more rooms waited for their classes (`Cave08` r2, `GanonN` r0, `kaze` r2, r5 and r8, `M_Dai` r7).
    - The line reads `68 all keyed (48 clear on B)`, and the report explains both. K9 now names its room: `M_Dai` r1, r7 and r8 are the owner disc's only ALLdie rooms whose every enemy is removed on B.
161. **`FuzzReconcileM6`'s audit judges each light-shrink by its actor (found in review).** The first audit accepted any heap byte going 0 to 1 at an offset that some verified class's timer has. Dropping the PID check of `Revalidate`, the `ever` check or the demo re-read would still have passed it, against what its comment and item 154 said.
    - The audit now records, for each write into the fake's actor heap, the block's process ID and proc, a BOKO's `mNowMode` and Link's `mDemo.mDemoType`, read just before the write (`beatMem.Write`).
    - A timer write must be the timer of the placed enemy whose actor held the block then (`fakegame.Enemies.Born`), of a verified class, keyed, its key sent and synced, not BOSS, with Link in no demo, and no process ID twice. A CARRY write must be on a BOKO in `Mode_ENEMY_CARRY_e` whose carrier (`Enemies.CarrierOf`) had its timer written earlier in the same tick.
    - With both demo gates removed, the fuzz reports `shrunk during Link demo` in under a second.
162. **The prompts and the console go through the session controller (App PR 2).** Items 64 and 70 stand as they are for the CLI; the questions and the console verbs now also have a UI path (docs/01-architecture.md, "Co-op through the controller").
    - A session asks through a `coopAsker`. The CLI's is item 64's stdin prompter, with no change: one question at a time, y/N, `--yes`, a line typed before the question dropped after the 50 ms settle, EOF or the session's end a no. The TUI's (`outputAsker`) logs the same question lines and puts a `session.Question` (kind, player, title, the whole prompt, a bind's preflight lines) to the controller; `Controller.Answer` answers it, and the session's end is a no. It never reads stdin, so Bubble Tea keeps the terminal.
    - The far-behind, needs-merge and no-backup advice keeps its log lines and is also published as `session.Advice`.
    - The console verbs of item 70 run in one place, `coopConsole.exec`: a typed line reaches it from the prompter's reader as before, and `Controller.Command` / `CommandLine` reach it from a UI. `/status`'s text is `formatStatus`; the controller's snapshot carries it per pipeline (`CoopStatus`).
    - `host` and `join` run on the controller (`runCLISession`). Their flags, output and exit codes are unchanged. `coop_golden_test.go` (now `mods/coop/session/golden_test.go`) checks it: the prompter, the offers and advice, and the console (dry run, writing, two Dolphins, a host's hub lines, lines typed on stdin) against transcripts recorded before the change, and the same console transcripts again through `Controller.Command` and `CommandLine`. `mp-local` still runs its pipelines directly.
163. **The TUI runs co-op (App PR 2; first part of §9 M8's panel).** Items 61, 68, 105 and 119 said the TUI passed no co-op flags; it now offers them.
    - The connect screen's Co-op row: a host picks Off, New world (`--coop=new`) or Continue a world on disk (`--coop=<id>`, listed as `coop-world list` lists them); a joiner picks Off or On (`--coop`). `--coop-merge`, `--coop-live-switches`, `--coop-dry-run`, `--coop-iso` and `--yes` stay CLI-only in the TUI; the app window offers them since App PR 9 (item 165). `--coop-boss-kill` and `--coop-refresh` stay CLI-only in the TUI too; the app offers them per player (item 165, *Later*).
    - On the dashboard a bind or scrub question replaces the log panel until `y` or `n` answers it (Esc leaves, which is a no). With co-op on, a chat line starting with `/` is a console command and its outcome goes to the log, so `/status`, `/warp` and the rest work as on the CLI. The status panel shows each pipeline's state and whether it writes.
164. **A TUI question waits for the keyboard to settle; the snapshot holds the open questions (App PR 2 review).** Item 64's rule, that input given before a question was shown never answers it, now holds for the TUI too.
    - The chat input has the focus while a question comes up, so a player typing "yeah ok" as a bind question appears would have bound with the `y`. A `y` or `n` now answers only once the question has been shown for `questionSettle` (600 ms) and no other key came in the 600 ms before it; a key sooner is swallowed and restarts the wait. The CLI's stdin prompter is unchanged (its 50 ms settle and per-line stamps).
    - A subscription drops its oldest events when full (`Subscribe`), question events included. The dashboard therefore takes its open questions from `Snapshot().Questions` on every status tick, ignores a second `EventQuestion` for an open one, clears them when the session ends, and drops a question whose `Answer` returns `ErrNoQuestion`. A lost `EventQuestion` can no longer leave a session waiting on a question nobody sees, nor a lost `EventQuestionClosed` leave a dead question taking every key. `Subscribe`'s doc gives the GUI the same rule.
    - `Controller.Command` returns the refusal that kept a command from every pipeline ("no Dolphin runs co-op yet", "no player X here", "say which player"), still logged as the CLI logs it, so a button can tell a refusal from success; a host's `/status` with no pipeline yet is no refusal (its hub's lines answer it). What a pipeline made of a command stays in the log and `CoopStatus`. `CommandLine` returns nil for a refused line, as before: the TUI echoes a returned error, and the line's outcome is already logged.
165. **Co-op in the app window (App PR 9).** The window runs the CLI's co-op session (`coop_session.go`, now `mods/coop/session`) with the same log lines; what is new is when its parts start, and a few app-only paths (docs/01-architecture.md, "Co-op in the app").
    - **Order.** The host chooses the world in the lobby (Off, New world, Continue; Advanced: dry run, allow merge, live switches, disc for the co-op tables), which the lobby carries (`SetCoopSettings`, docs/10). *Later (coop-remaining 1.2 option a):* every player, host or joiner, sets their own `--coop-boss-kill` rows and `--coop-refresh` tokens in the save-file panel ("Advanced: your game's relays", `Bridge.SetCoopRelays`). They are no lobby setting: each is checked by `replica.ParseBossKill`/`ParseRefresh` with the CLI's error text, saved in `config.Coop` (`bossKill`, `refresh`; a saved value the parsers refuse is dropped with a warning at load) and read at the next co-op start through `CoopFn`, as the disc is. On START the host backs up its cards, opens or founds the world, starts the hub, sets it on its server, and only then sends GO with the world's ID. A joiner starts its co-op side only on a GO that names a world (off the lobby connection's read path), so its co-op connection never meets a server without a hub. A failure to open or found the world is logged and GO goes out without a world: the session plays without co-op.
    - **Founding waits.** `--coop=new` in the CLI fails on a file that can't found a world (`foundCheck`: bound already, a foreign or garbage tag, a second quest). The app's founding instead says why once, reports it to the guided dialog and a banner, and waits for another file or for "Remove the tag".
    - **Remove the tag** (`UnbindFile`, `clearLoadedTag`) is `coop-tag clear --yes` from the window: it zeroes the loaded file's tag in game memory only, so the card keeps the tag until the next in-game save, and loading the file from the card brings it back. It carries the tag's world the dialog showed and refuses when the loaded file's tag is another (the file changed since), and it never removes a tag of the session's own world.
    - **Merging after Start.** `Hub.SetMerge` turns `--coop-merge` on or off while the hub runs: it logs the change and re-grants every welcomed peer, whose replica then learns the new features (`TestSetMerge`). A joiner whose file needed merging asks to bind again (`retryBinds`: it waits up to 15 s for the hub's features to arrive, else says to load the file again; `TestRetryBindsAfterMerge`).
    - **Restarts.** The app's game side starts coop-sync again after an error, a Dolphin relaunch or a new boot; the CLI runs it once a process. Each app run leaves the console when it stops (`appSyncOn`), the console keeps the session's context rather than the first run's, and the files a bind was asked for are kept per player for the session (`coopSession.askedFor`), so a no is still not asked again (`TestGameRunnerCoopRestart`).
    - **The window as a third asker.** The app answers through `outputAsker` like the TUI: every question is a dialog (Bind / Not now, Remove the tag / Leave it). Escape is the no; a close made by the window itself (another dialog taking its place) is not. Item 64's rule holds as in item 164: a question or a "Remove the tag" dialog takes the focus itself, not a button, and ignores clicks, Enter, Space and Escape for its first 600 ms (`SETTLE_MS` in ui.js). The per-session auto-yes (`SetAutoYes`, never saved) answers open and later questions yes, as `--yes` does.
166. **The casual shirt for remote players took 0x20 bytes of T2 (docs/09 §5.6).** `tunic_swap` gained a second enable value, `TUNIC_ENABLE_CASUAL` (2), which copies the casual header (mOtherLinktex while the viewer wears the hero's clothes) into the worn header around a remote's draw. No mailbox field was added: the enable byte carries it. T2 grew from 0x2E08 to 0x2E28 (devkitPPC 16.2, `-O2`, the helpers at `Os`), so it ends at 0x80412E28 and the free gap below the mailbox is 0xD8 bytes, down from 0xF8. M7 (about 0x700 bytes) still needs §4.5's arena move or finish-plan D2 first.

167. **The actor-puppet deletes took 0xC0 bytes of T2 (docs/05, "Actor puppets outlived their slot").** `puppet_reap` deletes a legacy actor puppet (KAMOME / NPC_OB1) when Go clears its slot and every one of them when a stage change starts, Link is gone or Link's actor changes; no mailbox field was added, and mailbox.h's comment on `Puppet.active` now states that delete contract (a comment only: the blob payload is the same). T2 grew from 0x2E28 to 0x2EE8 (devkitPPC 16.2), so it ends at 0x80412EE8 and the free gap below the mailbox is 0x18 bytes, down from 0xD8 (item 166). Any further C change needs §4.5's arena move or finish-plan D2 first. (Later: E0 removed the dead C experiments and the E3 split left T2 ending at 0x80412A88, 0x478 bytes below the mailbox; see §4.5.)

168. **The heart table counted two unreachable chests in place of Maggie and the withered trees (review of datstarkey/ww-online, idea 10; corrects item 137's "chest 14").**
    - Found by comparing with ww-online's heart catalogue (`docs/hearts.md` §1 and §3.4, `WWOnline.Patcher/WorldData/HeartCatalog.cs:97`; MIT, facts only, no code): 12 chests, 2 placed and dug-up items, 14 salvage points and 16 rewards make their 44. Ours made 44 from 14 chests and 14 rewards. The owner's cached coop-tables (schema 11, 2026-10-24; the disc itself was not read) name the two extra chests: Cave06 (table 12, tbox 18) and I_SubAN (table 12, tbox 7). The Wind Waker Randomizer's `logic/item_locations.txt` lists 12 heart chests and neither stage, and its two rewards our Catalogue lacked are Maggie (`d_a_npc_kp1.rel`) and the withered trees (`d_a_obj_ftree.rel`). So the old PASS was right in its total and wrong in two sources.
    - **Rule D: two stages are left out of the hearts section by name** (`hearts.unreachable`): Cave06, whose only entrance ww-online places in `sea_E` (the unused E3-demo sea), and I_SubAN, a developer sub-dungeon that sea room 47 names as an exit while Angular Isles' cave is SubD43. Not verified here: this tree reads no SCLS exits. The other three sections keep them, for item 137's reason (no reachability closure); their switches and enemies add only entries no player meets. ww-online's third stage, DmSpot0, holds no heart (item 137) and is not listed. The table records them as `unreachable` (name and why), and the report prints `left out <stage>: <why>` under the totals.
    - **The Catalogue** (`wwapi/tables/hearts/catalogue.go`):
      - Maggie and the withered trees join as unverified rewards (`ItemSeen` false, no `Record`): `d_a_npc_kp1` and `d_a_obj_ftree` are not decompiled. ww-online's records for them (Moblin's Letter gone with `mReserveFlags` bit 15 set; event bit 0x2E20) are REL readings and stay in `Note`. The trees are a new kind, `drop`.
      - The three NPC_PEOPLE rewards are now named Kreeb, Sam, and Linda and Anton, each with the event bit set as the resident picks the reward talk: 0x1B20 (`d_a_npc_people.cpp:7061-7063`), 0x1B10 (`:6880-6883`, every flower stand done) and 0x2280 (`:7033-7035`). The piece comes from the event's ItemNo cut, and `l_get_item_no` [0], [3] and [4] are its three pieces (`:3855-3866`). Which index each event names is event data: the pairing written down is the Randomizer's (its `d_a_npc_people.rel` offsets 0xC54B, 0xC557, 0xC55B), unverified, and the count does not depend on it.
      - The Goron trade stays unrecorded. 0x3E04 is a candidate only, written in `Note` as UNVERIFIED: it is set in the XY-talk branch as merchant 1 is shown the Shop Guru Statue (`d_a_npc_roten.cpp:2500-2503`, `isGetMap` `:2827-2833`, message `l_msg_try_force`). Its `l_get_item_no` holds the piece alone (`:1373-1375`), but whether that talk's event runs the ItemNo cut is event data, and max life is add-only, so a wrong record would raise both players' max life for good. (The first draft of this item recorded it; the review's fix round took it out.)
      - Sploosh Kaboom, barrel shooting and the Bird-Man contest stay unrecorded: ww-online's records (registers 0xFE07 and 0xB703 >= 1, event bit 0x2B40) are REL readings, and `tables.RecordOf` reads only an event bit or a read letter.
    - So the derive now counts 10 recorded rewards, not 7: a partner's Kreeb, Sam, or Linda and Anton piece raises the receiver's max life once its bit lands (all three bits are in the story SyncMask), and the receiver is not given it again (the NPC takes the other talk branch). Six rewards stay uncounted (`Untracked`).
    - GZLE01 only: in the Japanese release `l_get_item_no[0]` of `d_a_npc_people` is an orange rupee (`:3855-3860`, `VERSION <= VERSION_JPN`), so the resident on index 0 (Sam, by the Randomizer's pairing) pays no piece there. `tables.Build` accepts only the GZLE01 DOL and the hook only GZLE01, so this holds today; the three records need checking again before another disc is accepted.
    - Schema 12. As item 137 says, a schema change changes the tables' `Identity`, so both players need this build for every table feature, not only max life.
    - Tests: hearts' `TestBuildVanilla` adds Cave06 and I_SubAN with a heart chest each and checks they are left out, listed and reported; the counts move to 28 in stage files, 11 rewards and 5 unverified. `TestCatalogueRecords` pins the three new records and none for the Goron trade, `TestRecordsVerified` keeps any record whose entry says inferred or unverified out of the derive, `TestCountLife` 10 recorded, synctables' acceptance strings the new split, `TestSchemaShape` schema 12.
    - Not verified: the two stages' reachability (above); a `coop-tables --no-cache` run on the owner's disc with this build (docs/TESTING.md, "Co-op: side-quest registers and the heart table").

169. **Side-quest registers that can pay twice (review of datstarkey/ww-online, idea 9; extends §3's register allowlist).**
    - ww-online shares seven side-quest registers (`WWOnline.Shared/Models/StoryFlags.cs:84-105`). Taken, each checked against the decomp (`mods/coop/class/events.go`, mirrored in `wwapi/state/sensitive.go`):
      - 0xD007, Orca's record level, MAX. The enum's id is 0xD003, but `setClearRecord` (`d_a_npc_ji1.cpp:409-422`) writes level 4 at 1000 hits, and `setEventReg` clears only the id's mask before ORing the value in (`d_save.cpp:1202-1205`), so the byte holds 0x04 and the field must be 0x07. The level only rises: a new level is written only above the old one, and once 0x0F20 is set `isClearRecord` refuses (`:395-406`) and the other caller runs only at 1000 hits (`:4033-4051`), writing 4 again. It pays a purple and an orange rupee at levels 1 and 2 (`createItem`, `:1508-1512`). The heart piece is 0x0F10's, already in the story bits (the review's correction).
      - 0xC203, Koboli's mail-sorting level, MAX: each level's winning end message steps to the next level's write (`d_a_npc_bmsw.cpp:192-248`, `:365-380`, `:430-451`). It sets the pay rate, and at 3 Baito appears as Btsw (`:1144-1150`) while Btsw2 stays away (`d_a_npc_btsw2.cpp:545-547`): a partner's level 3 changes who stands in the receiver's post office.
      - 0xBF0F, the pigs put in Outset's pen, OR. The enum's id is 0xBFFF; the only setter ORs in `1 << (shape & 3)` (`d_a_kb.cpp:2170-2175`), and the only clear is behind the debug register `REG8_S(8)` (`:2496-2501`). Once 0x0520 is set the bits pick which pig Outset keeps (`:2504-2530`); no reward reads it.
    - Not taken: 0xFE07 and 0xB703, the Sploosh Kaboom and barrel-shooting prizes won. `d_a_npc_kg1` and `kg2` are not decompiled, so their meaning (first prizes won, capped at 3, the index into {heart piece, chart, rupees}) is ww-online's REL reading (`docs/hearts.md` §3.3); their field widths and whether they only rise are unchecked. Shared by MAX they would cost the partner a heart piece for good: neither minigame's piece has a record the life derive reads, so it never credits the winner's piece to the partner, while the raised index hands the partner the chart when they win. (The first draft of this item shared them; the review's fix round took them out.) They can join `maxRegs` once both pieces get a record the derive credits and a disassembly or a live readback confirms the reading. `TestUntrackedRewardRegistersStayLocal` keeps any register an untracked reward's Catalogue entry names per player.
    - Not taken: 0xC0FF, Mrs. Marie's Joy Pendant count. `receivePendant` takes the pendants from the giver's own spoils bag (`d_a_npc_ho.cpp:70-82`) and bag contents are per player (§1), so the count she keeps, with her first talk at 0 (`:398-405`) and the Hero's Charm at 40 (`:329`), goes with each player's bag.
    - Nothing to generate: `scripts/gen_event_masks.py` covers the bits of bytes 0x00-0x41 only. The registers travel in the story batch and are written on the transition tick (item 89), so no new timing.
    - The class table changes, so `wire.ClassHash` changes and the hub refuses co-op with a build without this (`hub/session.go:207`).
    - Tests: `TestSpotClasses` (each new register, the pen's OR field, 0xC0FF, 0xFE07 and 0xB703 per player), `TestGoldenCoverage` (OR 525, MAX 19, PER_PLAYER 786), `TestRegisterAllowlistUntouched`, `TestApplyRegister` (Orca's 4 over 3 and kept, Koboli's raise, the pen's field OR, the pendants and both minigame counts untouched), `TestApplyRegisters` (39), `TestByteKinds` (40 register bytes) and `TestUntrackedRewardRegistersStayLocal`; the state fold's equivalence test pins `wwapi/state` to the table.
