# Co-op design (v1.0)

**Status:** the v1.0 co-op design, 2026-10-14. Until the owner answers the open questions (§10), the design uses the recommended defaults: per-flag merge, separate rupees, a partner's switches in your current room deferred to your next room change, and the arena for M7's code room. The relationship with ww-online (§10, question 5) is awaiting the owner.

**Decision record:** issue #23. On 2026-10-14 the owner decided that v1.0 is a real co-op mod, in three parts:
1. save-file merging,
2. combat sync (enemies die for both players),
3. puzzle and dungeon state (doors, switches and so on propagate).

v0.3.0 (Go-only hardening) has already shipped, as tag v0.3.0 on d6850b0. This document supersedes the "Post-1.0: co-op" line in the finish plan.

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

Small keys and heart containers are counted from what the party actually collected and opened. A key both players grab is never doubled, and a key one player spends is gone for both. If you are in the same dungeon as your partner, a latching crystal switch or a cleared room opens for both of you. An enemy your partner kills shrinks away on your screen and stays dead while either of you is in that stage.

Some things stay your own: hearts and magic you are carrying, rupees, ammo, bottles, time of day, and where you are. You roam freely and see your partner as the mini-Link.

The early releases (v0.4 to v0.8) do all of this from the Go process, with no re-patch. The last step before v1.0 adds a small injected routine that applies the shared world at the moment a stage loads, on the game's own thread. That removes the remaining write races and surprise cutscenes.

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
- **on load**: applied at the receiver's next room or stage change. Before M7 Go does both. From M7 the getSave shim does it at stage loads; Go still does the room-change case (current-table memory switches, §6.3), because room changes do not call `getSave`.
- **never**: not synced.

| Class | Examples | Where it lives (address / struct) | Sync rule | Merge rule | Needs C? |
|---|---|---|---|---|---|
| Stage progress bits | chest opened `mTbox`, pickup `mItem`, `mVisitedRoom`, `mDungeonItem` bits 0-5 (map, compass, boss key, boss dead, heart taken, boss demo; `d_save.h:637-690`) | `dSv_memBit_c`: saved 0x803C4F88+0x24·t (+0x00 / +0x14 / +0x18 / +0x21); live copy of the current table 0x803C5380 (`d_save.h:992`) | live for the current table (these bits are read only when actors are created, `d_a_tbox.cpp:447-455`, `d_a_item.cpp:227-237`, so a write fires no event); on load for other tables | grow-only OR | No. M7 moves it to C. |
| Memory switches 0x00-0x7F | door unlocked, gate raised, room-clear switch, crystal (un-timed) | memBit `mSwitch[4]` +0x04 (live 0x803C5384); bit n is `mSwitch[n>>5] \|= 1<<(n&31)` (`d_save.cpp:1084-1087`) | on load by default (receiver's next room change for the current table; immediately for cold tables); live is opt-in (`--coop-live-switches`) | set-only OR, applied once per bit ("ever-had" set), **latching setters only** (M1 table), clears never sent | No. M7 makes stage-load application exact; the room-change write stays in Go (§6.3). |
| Small keys | `mKeyNum` per dungeon | memBit +0x20 (live 0x803C53A0); HUD pending counter `mItemKeyNumCount` 0x803CA77C | derived, applied when idle and settled | `keys(t) = \|key sources set\| − \|key doors set\|` over the receiver's *applied* flags (§6.4) | No |
| Ocean and charts | salvage bits, charts owned / deciphered / salvaged, `mFmapBits`, Triforce charts | `mOcean` 0x803C51C8 (0x64); `dSv_player_map_c` 0x803C4CCC (0x84) | on load | OR | No |
| Story event bits | bytes 0x00-0x41 (487 named bits, `d_save_event_flag.inc`) | `mEvent` 0x803C522C; bit id 0xHHLL = byte HH, mask LL (`d_save.cpp:1187-1199`) | on load (Go writes them during the receiver's stage transition; M7 shim writes them at getSave) | OR under the generated SyncMask, minus LocalOnly, D8 and refight bits; receiver-side holds (§6.5) | No. M7 makes it exact. |
| Event registers | bytes 0x79-0xFF (131 multi-bit registers, `setEventReg` is mask-and-set, `d_save.cpp:1202-1210`) | 0x803C52A5..0x803C532B | on load | per player, except an allowlist: the 17 figurine masks from `l_figure_comp` (`d_a_obj_figure.cpp:43-61`): 0x80FF-0x84FF, 0x8CFF-0x95FF, 0x9CFF, 0xB1FF use OR; letter, Beedle-points and Ghost Ship registers use MAX within their mask (monotone: inferred) | No |
| Unnamed event bytes 0x42-0x78 | Archipelago uses 0x60-0x61 | 0x803C526E..0x803C52A4 | never | — | — |
| Inventory and collection | `mGetItem` flags, `mCollect[0..4]` (swords, shields, bracelets, charms), songs `mTact`, `mTriforce`, `mSymbol`, beast and bait flags | 0x803C4C59 (0x15), 0x803C4CBC-0x803C4CC7, 0x803C4C9C | live when idle | OR flags, then derive slots `mItems` 0x803C4C44 (best variant, fill empty or lesser slots only) and equipment; grants ordered base then upgrade; context rules for the FF1 sword and the GTower bow (§6.6) | No (Go mirrors the `item_func_*` side effects). M7 adds `execItemGet` as an option. |
| Capacities | wallet size, arrow max, bomb max | 0x803C4C1A, 0x803C4C77, 0x803C4C78 | live when idle | MAX; never lowered | No |
| Max magic | magic meter and its upgrade | `mMaxMagic` u8 0x803C4C1B; pending `mItemMaxMagicCount` 0x803CA784 | derived, live when idle | from source flags (Deku Leaf, event 0x3180); **add-only** | No |
| Max life | heart pieces and containers | `mMaxLife` u16 0x803C4C08 (quarter hearts); pending `mItemMaxLifeCount` 0x803CA77E | derived, live when idle | 12 + pieces + 4·containers from source flags; **add-only** | No |
| Dan switches 0x80-0xBF | dungeon-temporary puzzle state | `mDan.mSwitch[2]` 0x803C53A8 | live, only when both players are in the same stage name and table | set-only, latching setters only | No |
| Zone switches 0xC0-0xDF | room puzzle state | `mZone[z].mZoneBit.mSwitch[0..1]` 0x803C53B0+0x4C·z+2; z from `mStatus[room].mZoneNo` 0x803BDC88+0x114·room+0x107 | live, only when both players are in the same stage and room and the receiver's zone exists | set-only, latching only, kept per (stage, room) while that room is occupied | No |
| Room one-shot switches 0xE0-0xEF | drop-down ladders, room-visit state | `mZone[z].mSwitch[2]`, zeroed on every room change (`clearRoomSwitch`, `d_save.cpp:1273-1275`) | live edge only, both players in the same room at that moment | never stored; never re-applied after a room change | No |
| Enemy kills | stage-placed enemies by `setID` | zone actor bits 0x803C53BC+0x4C·z (512 bits); actor `setID` +0x1BC, `home.roomNo` +0x1E2 | live, scoped to (stage, room) | union per (stage, room), dropped when no player is in the stage | No (K1 light-shrink). M7 adds the `isActor` shim and KILL. |
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

**IDLE** (needed for inventory, counters, story, live switches and kills). All of these hold:
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
internal/coop/stagedata : user's ISO -> keys, hearts, switch classes, push blocks, enemy ACTR   [M1]
internal/coop/savefile  : GCI / card parse, checksums, packed<->RAM map, diff/merge             [M4]
C side: unchanged until M7 (coop pump: getSave shim, isActor shim, coop_frame + op ring)
```

### 4.2 Go side

**Packages**, all testable on Linux:
- `internal/coop/mem`: a `Mem` interface over `dolphin.ReadAbsolute`/`WriteAbsolute` (`internal/dolphin/memory.go:117-128`), a 24 MB `FakeMem`, and a `FakeGame`. FakeGame models:
  - frames and the heartbeat;
  - `getSave`/`putSave` on table change;
  - `initDan`, `clearRoomSwitch`, zone create and remove;
  - random game `onSwitch`/`onEventBit`/`offSwitch` writers;
  - the HUD's pending-counter semantics (`d_meter.cpp:1420-1452`, `5242-5257`);
  - `dComIfGs_init` leaving `mReserve` untouched;
  - `card_to_memory` and `ExDataSave`.
- `internal/coop/addr`: every constant from Appendix A with its decomp citation, plus arithmetic tests (for example play = 0x803C4C08 + 0x12A0, and `mCurStage` = play + 0x3E94).
- `internal/coop/class`: the byte-class table, SyncMask, LocalOnly and hold lists, the register allowlist, and `policyVer`.
- `internal/coop/snap`: the snapshot reader. Per tick it reads:
  - 0x803C4C08 + 0x12A0;
  - 0x803C9D3C..0x803C9EB8;
  - 0x803CA754..0x803CA8E4;
  - `mStayNo`;
  - 64 `mZoneNo` bytes;
  - `dMenu_pause`;
  - the heartbeat.

  That is about 5 KB per tick. Sensitive fields are read twice and a bit is kept only if both reads agree, which rejects torn reads during `card_to_memory`.
- `internal/coop/replica`, the client:
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
    - validated K1 timer bytes.

    Anything else is refused and logged. The CI write audit uses the same guard.
  - **Auto-disable.** Co-op turns itself off, and the TUI says why, on any of:
    - more than 8 verify failures or WriteGuard refusals per minute;
    - a table-hash or layout mismatch;
    - a nonce storm from save-state loads.
- `internal/coop/stagedata` (M1) and `internal/coop/savefile` (M4): see the milestones.
- `internal/coop/hub`, server side: the CoopHub (§4.4).

**Integration**
- Started from `runMultiplayerGoroutines` (`main.go:7088`) for `host` and `join`, and from `runMpLocal`'s `spawn` helper (`mp_local.go:322-339`, one coop-sync per Dolphin PID) for `mp-local`, when `--coop` is passed.
- Co-op is off by default until v1.0.
- `coop off` stops all writes within one tick.

#### 4.2.1 Write race and the frame-phase window (corrected)

The game sets bits with a word load, OR and store (`d_save.cpp:1084-1087`). A Go byte read-modify-write can erase a bit the game sets in the same byte between Go's read and write. Nothing could detect that afterwards.

`frame_shim` replaces the `bl` at 0x80023204 in `fapGm_Execute` (`f_ap_game.cpp:77-81`). It runs `multiplayer_update` and then tail-calls `cCt_Counter` (0x802449AC, `sym:8240`; `inject/src/multiplayer.c:290-310`), so `cCt_Counter` still runs. `multiplayer_update` starts with `spawn_trigger++` (`multiplayer.c:341-342`).

In GZLE01, `fpcM_Management` runs these steps in order (`f_pc_manager.cpp:276-292`):
1. `cAPIGph_Painter` (the VSync wait),
2. `fpcDt_Handler` (deletes),
3. `fpcPi_Handler`,
4. **`fpcCt_Handler`, where actor `create()` runs and can call `onSwitch` or `onEventBit`**,
5. execute,
6. draw.

So a write made right after the heartbeat lands in the rest of the main loop or in the next Painter. That is before the next frame's creates and executes, but it is **not** a window where nothing writes save bits. FrameWriter shrinks the hazard to a game store into the same byte within a few microseconds. It does not remove it. The `frame-phase-probe` diagnostic measures the window on the owner's machine. Only M7's game-thread application removes the race.

### 4.3 C side

**M0-M6: no C changes.** The blob, the mailbox at 0x80412F00 and `saves/start.sav` all stay valid, and users do not need to re-patch.

**M7: the co-op pump.** It is the only C milestone: one blob regen, one Shift+F1 recapture and one user re-patch. It contains:

1. **`coop_getsave_shim(self, stageNo)`**
   - Installed with Freighter `hook_branchlink`, the technique all four existing hooks use (`inject/build.py:39,48,59,69`), over the single `bl getSave__10dSv_info_cFi` (0x8005D8C8) inside `dStage_stagInfoInit` (0x8004230C-0x8004238C).
   - `d_stage.cpp:1675` is the only `getSave` call in the game. The exact `bl` address comes from `WW_DOL_PATH=inject/original.dol ./ww-multiplayer.exe ppc-disasm 0x8004230C 32`.
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

**Size.** A clang-18 `powerpc-unknown-eabi` sketch of items 1, 3 and 5 with GRANT and ADD_S16 measured **0x3F0 bytes of .text at -Os and 0x50C at -O2** (`scratchpad/coop/hostauth-sketch/coop_world.c`). With the isActor shim and the full op set the estimate is about 0x700 (inferred). devkitPPC gcc will differ. This does not fit today's 0x258-byte gap, so §4.5 applies.

**Not in v1.0.** Entry hooks on the `dSv_*` setters, which need displaced-instruction trampolines. If they are ever needed, fill the displaced first instruction at patch time from the user's vanilla DOL, using marker words, PC-relative and r12/ctr checks, and a CRC. No game bytes would then ship in `blob.go`.

### 4.4 Network protocol v2

**Version bump.** `ProtocolVersion` goes from 1 to 2 (`internal/network/handshake.go:40`). Peers with mismatched versions are already rejected with a clear message.

**Free type letters.** J W P L D C M K Q H R are taken (`protocol.go:13-40`). Co-op uses one new type, **'O' (0x4F)**. The frame format `[type:1][len:2 BE][payload]` with `MaxPayload` 0xFFFF is unchanged.

**Connection role.** The co-op pipeline opens its own connection: `MsgJoin(name)`, then `MsgHello` with payload `[version:u16 BE][role:u8][caps:u16 BE]`.
- `ParseHello` ignores trailing bytes (`handshake.go:90-95`), so the extension is compatible.
- Roles: 0 = player (the default when absent), 1 = coop.
- Role-1 connections live in the CoopHub, not in `s.players`. They get no pose, chat or player-list relay, do not appear in the roster, and keep the heartbeat.
- The server routes 'O' to the CoopHub (new case in `server.go:214-264`). An 'O' frame on a role-0 connection is dropped.

**'O' payload: `[sub:u8][body]`.** All integers are big-endian. **No raw addresses travel.** Offsets index the 0x778-byte image and are checked against the class table; receivers derive every address locally.

| Sub | Dir | Body | Server action |
|---|---|---|---|
| 0x01 CO_HELLO | c→s | `[schema:u16][classHash:u64][stageTblHash:u64][worldID:8][bindNonce:8][slot:u8][clearCount:u8][nameHash:u32][fileState:u8 (0 unbound, 1 bound, 2 leaked, 3 foreign)][want:u32]` = 45 B | Classify; a mismatched `classHash` or `stageTblHash` (modded or rando data) gets features = 0 plus a NOTICE |
| 0x02 CO_WELCOME | s→c | `[status:u8][reason:u8][features:u32][worldID:8][seq:u64][freshBind:u8]` = 23 B | — |
| 0x03 WORLD | both | `[worldID:8][seq:u64][len:u16=0x778][image]` (PER_PLAYER, NEVER and TAG bytes zeroed; current-table live copy folded into its saved slot) ≈ 1.9 KB | c→s at join, reconnect and resync: merge per class, reply with the merged image, relay the effective delta as GAINS |
| 0x04 GAINS | c→s | `[batch:u32][n:u16] n×{off:u16, val:u8}` | Validate each offset's class; merge (OR within the class mask, or MAX); drop tombstoned bits |
| 0x04 GAINS | s→c | `[seq:u64][origin:u8][n:u16] n×{off:u16, val:u8}` | Only bytes that changed the world; n ≤ 21841 per frame ((0xFFFF − 12) / 3), and in practice n ≤ 0x778 |
| 0x05 PRESENCE | c→s, relayed | `[stage:8][table:s8][room:s8][layer:s8][point:s16][flags:u8 inGame, inEvent, transitioning, paused, armed]`, sent on change and at 1 Hz | Scopes the dan, zone and kill stores. Also usable by finish-plan C5 (#9). |
| 0x06 SCOPED | both | `[kind:u8 DAN=1, ZONE=2, ROOMEDGE=3, KILL=4][stage:8][table:s8][room:s8][n:u8] n×{word:u8, bits:u32}` | DAN: OR into store[stage]. ZONE: OR into store[stage, room]. ROOMEDGE: relay only, never stored. KILL: words 0-15 of the 512-bit actor field, OR into store[stage, room]. Relayed only to peers whose presence matches. |
| 0x07 NOTICE | s→c | `[code:u8][len:u8][utf8]` | Codes: WORLD_MISMATCH, NOT_BOUND, DIVERGENT, CLEARCOUNT, SCHEMA, REJECTED, TOMBSTONE |
| 0x08 CTRL | both | `[op:u8][arg:u32]` | Ops: RESYNC, COOP_OFF (host feature mask), TOMBSTONE (host only) |

**CoopHub** (one goroutine owns all world state):
- **World image** with per-class merge and a monotonic `seq`.
- **Scoped stores.** Dan per stage; zone and kill per (stage, room). Each is dropped when its last occupant leaves.
- **World file:** `os.UserConfigDir()/ww-multiplayer/worlds/<worldID>/world.wwco`.
  - Header "WWCOWRLD", version, worldID, seq, CRC32, then TLV sections.
  - Written atomically: tmp file, fsync, rename.
  - Flush 1 s after a change and on Stop.
  - `journal.log` records (seq, time, origin, offsets).
  - Five rotated backups, taken at session start and hourly.
- **Self-healing.** Commits not yet flushed when the host crashes are re-proposed by replicas that still hold the bits, so the world self-heals.
- **Tombstones.** `coop-world block <class> <index>` rejects future proposals of a bit. Replicas that already applied it keep it; this is documented.

### 4.5 Code-space plan

Current layout (`inject/build.py:109,142-145`; `mailbox.h:33`; `internal/inject/blob.go`):

| Range | Contents | Size |
|---|---|---|
| 0x803FCFA8..0x8040CFA8 | main stack (grows down; `OSClearStack` zeroes stack_end+4..SP, `OSThread.c:924-933`) | 64 KB |
| 0x8040CFA8..0x8040CFB0 | frame header above the stack top, **in use**: `__start` calls the nofralloc `__init_hardware` with r1 = `_stack_addr`, so `__OSPSInit` and `__OSCacheInit` save LR at 4(r1) = 0x8040CFAC, which keeps 0x8000336C on every booted game (`__start.c:21-23,125-126,173-183`; `OS.c:623-624`; `OSCache.c:446`; `__init_hardware` 0x80003354, sym:5) | 8 |
| 0x8040CFB0..0x80410000 | MetroTRK debug stack plus padding; `InitMetroTRK` runs only if a 32-bit word is 2 or 3: the word at +0xC of the block pointed to by 0x800000F4, or, when that pointer is 0 and 0x80000034 is non-zero, the word at 0x800030E8 (`__start.c:32-62`); free by inference | 0x3050 |
| 0x80410000..0x80412CA8 | T2 (code and data) | 0x2CA8 |
| 0x80412CA8..0x80412F00 | free, unchecked | **0x258** |
| 0x80412F00..0x80413080 | mailbox (`frame_shim` hard-codes `lwz 12,0x2F08(12)`, `multiplayer.c:297`; the Go `mailboxBase` const in `main.go`) | 0x180 |
| 0x80413080..0x80414000 | unused, below arena | 0xF80 |
| 0x80414000 | patched `__OSArenaLo` (`addi r3,r3,0x4000` at 0x80301820) | — |

**M0-M6** use no code space.

**M7 prerequisite: make room.**
1. Finish-plan **D2**: drop the dead shadow modes 1-4 and the echo ring.
2. **Option A, recommended.** It reuses a proven mechanism: the arena has been bumped in 4 KB steps before, and the mailbox has moved four times (`mailbox.h:10`; the callback slot five times, per the `multiplayer.c` header).
   - Change `addi r3,r3,0x4000` to `0x6000` at 0x80301820, so `__OSArenaLo` = **0x80416000**. That is +8 KB taken from ZeldaHeap.
   - Move the mailbox to **0x80415000**, layout unchanged. `frame_shim` becomes `lis 12,0x8041; lwz 12,0x5008(12)`. `extract_blob.py` emits `MailboxAddr`, `MailboxLayoutVersion` and `ArenaLo` into `blob.go`, so `main.go` stops hard-coding them.
   - Add a mailbox header `'WWMB'` plus `layout_version` at +0x180. Go checks it before any mailbox write, which also catches stale save states.
   - Co-op region at **0x80415200..0x80416000** (0xE00), layout below.
   - The T2 budget becomes 0x80410000..0x80415000 = **20 KB**, against about 0x33A8 needed after the pump (inferred).
   - Gate: the heap error counters `zeldaHeapErrors` 0x803F68FC, `gameHeapErrors` 0x803F68F8 and `archiveHeapErrors` 0x803F6904 stay 0 through boot, Outset (where the 1 MB bump failed, docs/05) and a 50-transition soak. The M0 `heap-free` probe measures the margin first.
3. **Option B (zero arena cost).** Set `inject_address` to 0x8040D000 (T2 budget 0x5F00) and leave the mailbox in place. Allowed only after M0's region canary survives boot, menus and 50 transitions on both Dolphins, and a live read-back of T2 at 0x8040D000 matches `T2Bytes` (a new `blob-verify --live` diagnostic, added to M0's list).
   - docs/05:63-73 reports that Dolphin "silently drops" a section at 0x803FD000. That address is inside the main stack, so `OSClearStack` is the likely cause (inferred). 0x8040D000 is above the stack top, but loader acceptance is only proven by `blob-verify --live`.
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
5. **The first co-op release (M2) is dry-run for game data.** Its only writes are the binding tag and leak scrubs in `dSv_reserve_c` (§5.2-5.3). Every other planned write is logged and not made. Any implausible read, layout mismatch or stalled heartbeat puts the replica into read-only mode, with the reason shown.

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
- `host --coop new [--name]` founds a world from the host's loaded, idle file. It mints `worldID` and `bindNonce`, writes the tag, writes `world.wwco`, and tells the host to save in-game.
- A joiner connects, then its file is classified:

| Classification | Condition | Action |
|---|---|---|
| BOUND_OK | tag matches the world | arm |
| UNBOUND, subset | the file's shared classes are a subset of the world (fresh file, or an older copy of this world's file) | prompt "Bind this file?"; on yes, back up and arm |
| UNBOUND, divergent | both sides have unique progress | the preflight lists per-class gains for each side and the held bits; binding needs `--coop-merge` on the host |
| refused | `mClearCount` differs; either player is in a boss refight; the file is FOREIGN | refuse |
| far behind | a fresh file joining a world whose resume-stage milestones (RODE_KORL 0x2A08, MET_KORL 0x0F80, 0x0801, 0x0808, 0x2401; `d_com_inf_game.cpp:1302-1320`) are set | recommend `coop-world export/import` (copy the host's file) over a union; a union is allowed only with `--coop-merge`, and holds apply |

- Binding is always an explicit prompt. Go never auto-binds a file that has progress.

### 5.4 Backups
- At every co-op session start, Go copies the Dolphin card (raw `.raw` image or GCI folder, read-only) to `<config>/ww-multiplayer/backups/<ts>/`. It keeps 10 rotations.
- The card path comes from `Config/Dolphin.ini` (location inferred, confirmed in M2).
- `coop-world restore <ts>` copies a backup back, but only while Dolphin is closed.

### 5.5 Lifecycle

| Situation | Behaviour |
|---|---|
| Joiner's network drops | The replica goes ARMED_OFFLINE. It stops applying, keeps detecting and queues gains. On reconnect: CO_HELLO, then WORLD both ways (merge), then queued gains. |
| Host server stops or crashes | The joiner behaves as above. The host restarts on the same world file; unflushed commits self-heal from the replicas. |
| Host runs a different world | NOTICE WORLD_MISMATCH: the joiner disarms. Pose still works. |
| A replica loads an older save or a Dolphin save state | The tag's `worldSeq` (+0x18) drops below the value Go last wrote, or the heartbeat regresses, which is detected (the bindNonce is unchanged). The replica is re-classified and catches up at its next stage load. Regressed bits are never proposed as clears. |
| A bound file is played solo (no Go) | Its progress is offline progress. It is merged into the world at the next session. The tag stays, and the game never touches it. |
| Playing solo afterwards, unbound | `coop-tag clear` zeroes the tag; it persists at the next in-game save. That file is never written again. |
| A different file is loaded mid-session | It is re-classified. Writes stop unless it is bound to this world. The TUI shows "file not bound". |
| Second quest | Refused (V3). The ExDataSave leak is scrubbed. |
| World file lost or corrupt | Restore from `world.wwco.1..5`, or `coop-world refound --from-loaded-file` (the bound replica's tag carries the worldID; joiners re-propose). |

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
- **Light bridges react with no event** (`d_a_lbridge.cpp:135-189`). A remote *clear* makes the bridge vanish under the local Link. Clears are never sent.
- **Key doors** (`door10`/`door12`): `execute` resets the action to `actionInit` whenever `checkExecute()` returns 0 (`d_a_door10.cpp:804-813`; `d_a_door12.cpp:823-831`). That happens when Link is not adjacent or is in another room (`d_door.cpp:156-171`). `actionInit` calls `setKey()` (`d_a_door10.cpp:748-749`; `d_a_door12.cpp:766`), which re-reads the switch. So **a key lock disappears when the receiver next approaches**, but only if the door's switch is already in the receiver's live copy. The door refuses to open while locked at `keyNum == 0` (`d_a_door10.cpp:359-372`).
- **Create-only state** (chests `d_a_tbox.cpp:447-455`, items `d_a_item.cpp:227-237`, `obj_stair` 150-151): a partner's change shows only after a room reload. Duplicate pickups in the receiver's current room are possible (§6.7).
- **Per-frame writers fight synced values.**
  - `d_a_swc00` type 0 sets or clears its switch every frame depending on local Link (`d_a_swc00.cpp:12-33`).
  - `d_a_tag_evsw` mirrors an event bit every frame (`d_a_tag_evsw.cpp:35-48`).
  - Timers clear switches: `d_a_obj_timer.cpp:72-74`; `d_a_andsw0.cpp:98-107`; `swhit0` timer types 347-367.

### 6.2 Switch classification (M1, from the user's own ISO)
- `internal/coop/stagedata` parses every stage's DZS/DZR (ACTR, TGOB, TRES, TGSC, SCOB, TGDR, DOOR and the ACTn/SCOn/TREn layer chunks; Appendix C item 16). It maps object names to procs through `l_objectName` (`d_stage.cpp:437-1262`, read from the user's own main.dol at .data 0x80372818, 0x26AC bytes, sym:14693; Appendix C item 15).
- **Latching setters (sync):**
  - un-timed crystals and torches;
  - floor switches types 0/3;
  - iron-boots switch type 3;
  - ALLdie (`param >> 8`);
  - chest switches;
  - breakables;
  - one-shot `andsw2`;
  - key-door switches (`prm & 0xFF`, set by `keyInit` below 0x80, `d_door.cpp:433-447`).
- **Unsafe (never sync):**
  - `SW_C00` area switches of type 0 (types 1-3 latch; Appendix C item 27);
  - `tag_evsw` (classed Event, carried by §6.5's story batch; Appendix C item 27);
  - `ObjTime`;
  - timed `SW_HIT0` and torches;
  - momentary floor switches (swpush type 1; swheavy types 0/1, which clear on release, `d_a_obj_swheavy.cpp:364-390`);
  - inverted floor switches (swpush type 2, FLAG_ON_IS_UP, which clears on press, `d_a_obj_swpush.cpp:544-551`) and toggling iron-boots switches (swheavy type 2, `rev_switch`, `d_a_obj_swheavy.cpp:333-334`);
  - continuous `andsw2`;
  - `andsw0` behaviours other than 0 and 3, which latch (Appendix C item 26);
  - any AND switch fed by an unsafe input (this prevents remote ambush triggers);
  - push-block path switches (`d_a_obj_movebox.cpp:1153-1198`, which encode a path index; OR-ing two of them gives a corrupt third).
- A switch is synced only if every setter of that (scope, number) is latching. The scope is the save table for memory and dan switches (dan switches are kept per table, Appendix C item 25) and the stage and room for zone and room switches.
- Appendix C items 25-31 list the setters and refinements M1 added from the decomp.
- The table is cached under the user config directory, keyed by DOL hash, and also built by `patch`. Its hash travels in CO_HELLO.

**Running it (`coop-tables`, M1).** On the owner machine, run `./ww-multiplayer.exe coop-tables <your vanilla .iso or .ciso>` (the patched ISO works too; its DOL hash differs, so it gets its own cache file with the same tables).
- Output: a status line, then a header (main.dol SHA-256, stage and room counts), the **Acceptance** block, and the four section reports.
- Flags: `--section keys|hearts|switches|enemies` limits the report to one section. `--json` prints the tables as JSON on stdout, with the status on stderr. `--no-cache` rebuilds from the disc and neither reads nor writes the cache.
- The cache is `ww-multiplayer/coop-tables/<main.dol SHA-256>.json` under `os.UserConfigDir` (`%AppData%` on Windows, `~/Library/Application Support` on macOS). A file written with another schema, by another ww-multiplayer build, or for another stage-file listing is rebuilt over, as is one that doesn't parse. Development builds with uncommitted changes share one build ID, so after editing a builder, pass `--no-cache`.
- The status line says whether the tables came from the cache or were built, with the time of each step (main.dol, stage archives read and expanded, the builders). The M1 target is under 5 s for a build.
- Compare the Acceptance block with:
  - small keys and key doors (sources/doors): DRC 4/4, FW 1/1, TotG 2/2, ET 3/3, WT 2/2, and no key doors outside tables 3-7;
  - 44 heart pieces and 6 containers. The pieces are counted from the stage files plus the 14 catalogue rewards, 3 of them unverified minigames (Appendix C item 23);
  - a non-empty push-block exclusion list;
  - for M6's go/no-go, the enemy setID share and the duplicate (stage, room, setID) keys. These are INFO lines with no target.
- A FAIL line gives the count. The section below lists every source, door and problem behind it.
- Also read the switches section's "Undecoded switch users" (Appendix C item 30).

### 6.3 Apply policy
| Class | Default | Opt-in |
|---|---|---|
| Memory switches, cold tables | written to the saved slot as soon as STABLE | — |
| Memory switches, receiver's current table | **deferred to the receiver's next room change** (written on the first STABLE tick after `mStayNo` changes, at least 4 ticks or about 400 ms later. The entered room is already loaded by then (`dStage_RoomCheck`, `d_stage.cpp:2263-2289`), so its polling actors still react; only actors of rooms still being created read the bit at creation (inferred)) | `--coop-live-switches`: apply when IDLE |
| Dan switches 0x80-0xBF | live when IDLE, only if the stage name and table match | — |
| Zone switches 0xC0-0xDF | live when IDLE, only if the stage and room match and the zone exists | — |
| Room switches 0xE0-0xEF | applied on the edge only, if both players are in that room at that moment; never stored or replayed | — |
| Never | clears; unsafe switches; event registers outside the allowlist; `mTmp` | — |

From M7 the getSave shim applies memory switches at every stage load as well. The deferred current-table write stays, because room changes do not call `getSave`.

### 6.4 Derived small keys (coupled to door switches)
- **Sources and doors per table** come from M1:
  - the 12 vanilla sources: DRC 4, FW 1, TotG 2, ET 3, WT 2 (wwrando `logic/item_locations.txt`; ww-online `docs/small-keys.md` §0);
  - key doors are door10 types 4/5, door12 type 1 and kddoor type 2, each with switch `prm & 0xFF`. `keyInit` records the switch only below 0x80 (`d_door.cpp:433-447`; Appendix C items 19-20). `coop-watch` still confirms the `keyInit` events live.
- **Target per table:** `keys(t) = |sources set| − |doors set|`, computed over the receiver's **own applied flags** (live ∪ saved for t), not the world image. A key decrement therefore takes effect only once the matching door switch is in the receiver's copy. When the receiver next approaches that door, its lock is gone (§6.1). No phantom-lock soft-block can occur.
- **Correction** runs only when IDLE, settled (no local key change in the last 1 s) and with no write pending for that table:
  - Current table with the key HUD on (`stagInfo->mProp & 1`; stagInfo pointer 0x803C9DA0; `d_stage.h:1023-1029`): the delta goes into `mItemKeyNumCount` (0x803CA77C), which `dMeter_keyMove` applies and animates (`d_meter.cpp:5242-5257`).
  - Current table without the HUD: write the live `mKeyNum`.
  - Other tables: write the saved byte 0x803C4FA8 + 0x24·t.
- **Duplicates self-correct.** Both players open the same key chest, or a key is spent on a door the partner already opened: the count converges to the derived value.
- **Key sync is enabled per table** only when that table's M1 invariant passes: sources == doors == the vanilla count, and every source and door is recorded in a saved bit (Appendix C item 21). A key from an unknown source is never removed.
- **Known exploit, documented:** using a key on a *different* door during the deferral window can open one extra door.

### 6.5 Story event bits
- **SyncMask:** every named bit in bytes 0x00-0x41, minus:
  - **LocalOnly:** every bit passed to `offEventBit` (50 call sites in 10 files, excluding `d_save.cpp`; 0x2D01's only clear is the debug map-select menu, `d_s_menu.cpp:244`), plus ENDLESS_NIGHT 0x0A02, RODE_KORL 0x2A08, 0x3820 and 0x3B20;
  - the 34 game-cleared bits (D8): 0x0E08, 0x1108, 0x1110, 0x1140, 0x1301, 0x1302, 0x1304, 0x2002, 0x2004, 0x2080, 0x2680, 0x2801, 0x2802, 0x2804, 0x2940, 0x2980, 0x2D01, 0x2F01, 0x3080, 0x3410, 0x3820, 0x3B01, 0x3C02-0x3C80, 0x3F01, 0x4008, 0x4020, 0x4040, 0x4080;
  - the refight snapshot bits 0x3D80, 0x3D40, 0x3D20 and 0x3D10 (`d_meter.cpp:1070-1085`).
- `scripts/gen_event_masks.py` generates the mask from the decomp, with citations. It is cross-checked against ww-online's 37-bit LocalOnly list (`docs/event-flags.md`, MIT) and committed as Go data.
- **Holds** (receiver side: the bits stay in the world but are not applied locally yet):
  - MET_KORL 0x0F80, 0x0801, 0x0808 and 0x2401 move the resume stage (`d_com_inf_game.cpp:1302-1320`). They are held until this player has MET_KORL locally.
  - 0x2D10 is held until a Master Sword is equipped.
  - 0x3804 is held until ZELDA_AWAKENED.
  - The last two lock boat boarding (`d_a_ship.cpp:4224-4229`, per ww-online).
- **Batching.** The sender sends a batch of new bits only after its game has been IDLE for at least 1 s, so a cutscene's side-effect bits travel together.
- **Applying.**
  - Before M7: Go writes the receiver's pending bits **on transition**, on the first tick where `mNextStage.mEnable` (0x803C9D54) == 1. Event bits are not copied by `getSave`/`putSave`, so they persist into the next stage, whose layers (`getLayerNo` 0x80052400, `d_com_inf_game.cpp:185-269`) and actors read them at creation (inferred).
  - From M7: the getSave shim applies them.
  - A cutscene-trigger switch (`daTag_Event` swbit, stage `mSpawnSwitchNo`, `d_event_manager.cpp:42-47`) is never sent without its batch.

### 6.6 Inventory and equipment
- Slots are derived from the merged `mGetItem` flags. The best variant is used (bow slot 12: bits 0/1/2 give bow, fire-ice, light), the same model the game uses itself (`d_s_play.cpp:1385-1392`). A slot is only filled if it is empty (0xFF) or holds a lesser variant; nothing is ever removed.
- **Ordered grants, capacities by MAX.**
  - `item_func_bow` sets arrow count and max to 30 **unconditionally** (`d_item.cpp:733-738`). The bomb func sets bomb max to 30 unconditionally (`d_item.cpp:796`). Quiver and bomb-bag upgrades set 60/99 absolutely (`d_item.cpp:1370-1388`).
  - So side effects are applied base first, then upgrade. Capacities are then forced to MAX(local, world) and are never lowered.
  - The same post-fix applies to M7 GRANT.
- **Context rules.**
  - No sword while the stage type is FF1 (stagInfo ST type, `d_stage.h:1031-1033`) and 0x0801 is set (`d_s_play.cpp:1369-1374`; `d_a_obj_demo_barrel.cpp:101-104`; the game also does `offCollect(0,0)`).
  - No bow, magic arrows or light arrows while the start stage is "GTower" (`d_s_play.cpp:1376-1384`).
- **HUD refresh.** After a slot changes, refresh `play.mSelectItem[btn]` (0x803CA7DB) for any button bound to that slot (`d_com_inf_game.h:2924-2938`). Whether Link's sword model updates on a live `mSelectEquip` change is inferred; otherwise it updates at the next stage load.
- **Bottles, bags and the delivery bag** are per player in v1.0. This is a known limit: a bottle only one player found stays theirs.

### 6.7 Create-only staleness and escape hatches
- **Until M9:** chests, freestanding items, bombable walls and other create-only actors in the receiver's *current* room stay stale until a room reload. Effects of a duplicate pickup:
  - unique items are idempotent;
  - keys self-correct (§6.4);
  - heart pieces may add one extra quarter (max life is add-only; logged);
  - rupees and ammo are duplicated (documented).
- **Refresh area** reloads the current stage, point and room. **Warp to partner:**
  - same stage: the existing mailbox `warp_seq` (`mailbox.h:211-225`);
  - other stage: the partner's PRESENCE stage and point.
- Before M7 both are Go writes of `mNextStage` (0x803C9D48..0x803C9D55). They are inferred and best-effort, because `dComIfGp_setNextStage` (0x800537C8, `d_com_inf_game.cpp:666-684`) also sets Link restart mode bits that a raw write skips. M7 replaces them with the `SET_NEXT_STAGE` op. Both ship from M3 as the softlock escape.

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

### 7.2 MVP (M6, Go-only): kill union plus K1 light-shrink

**Identity**
- Key: (stage name at 0x803C9D3C, `home.roomNo` +0x1E2, `setID` +0x1BC), with setID < 0x200. 0xFFFF is never recorded, and a setID of 0x200-0xFFFE halts the game in `isActor`/`onActor` (Appendix C item 33).
- Cross-checked against the M1 enemy ACTR table: procName +0x08, `home.pos` +0x1D0, params +0xB0. Keese, Kargaroc, a searchlight Bokoblin (`stand2`), Kalle Demos and Helmaroc King move `home.pos` (Appendix C item 34), so for them it is captured at first sight by process ID.
- The cross-checks catch layer mismatches: the two players can load different layers (`getLayerNo` 0x80052400).

**Detection**
- Diff the zone actor bits of each live zone; the room is `mZone[z].mRoomNo`. Send SCOPED KILL.
- For allowlisted classes that never call `onActor` (Kargaroc `bb`, Morth `ks`, and the NonMatching stubs once checked live), use observation instead: last health ≤ 0, or group leaves ENEMY, then the actor disappears while the stage is unchanged and its room is still loaded. The killer's Go then writes **its own** zone bit, so everything flows through one union.

**Store**
- KILL is kept per (stage, room) while any player is in that stage, and sent to a player when they join or enter the stage.

**Apply** (per tick)
1. OR the bits into the local zone if it exists.
2. Walk `g_fopAcTg_Queue` (0x80372028):
   - node = `&actor->actor_tag` (actor + 0xC4), a `create_tag_class` `{prev +0, mpData +4 (= &g_fopAcTg_Queue once queued, c_list.cpp:40), next +8, mpTagData +0xC}`; actor = `*(u32*)(node + 0xC)` (equivalently node − 0xC4);
   - validate `actor_type` (+0xC0) == `*g_fopAc_type` (0x803F69D0);
   - stop after 1024 nodes; keep every pointer inside MEM1;
   - re-validate the actor before any write.
3. For each live enemy (group +0x1BE == 2, or group 3 for the Morth `ks` class, `d_a_ks.cpp:1788`) whose bit is set, write **1** to `enemyice.mLightShrinkTimer` when all of these hold:
   - its class is on the allowlist;
   - `*(actor + eiOff) == actor` (the `enemyice.mpActor` self-pointer, e.g. `d_a_bk.cpp:5270`, `d_a_rd.cpp:1869`);
   - health (+0x285) > 0;
   - the timer is 0;
   - BOSS status (`actor_status` +0x1C4 & 0x04000000) is clear;
   - `eventInfo.mCommand` (+0xF8) == 0;
   - the global IDLE gate is open.
4. **Preludes**, written first:
   - Bokoblin: if +0xB30 != 0, write +0xB34 = 2.
   - Moblin: if +0xDD4 != 0, write +0xDD5 = 2.
   - Stalfos: clear CARRY on the held weapon at +0x1DCC.

**K1 offsets** (enemyice member + 6; from the `include/d/actor/d_a_*.h` headers):

| Class | proc | Timer offset | Status |
|---|---|---|---|
| Bokoblin `bk` | 0xBE | +0x126A | matching |
| Moblin `mo2` | 0xBD | +0x2A5A | matching |
| Stalfos `st` | 0xBF | +0x1FAE | matching |
| ReDead `rd` | 0xE1 | +0x6E6 | matching |
| ChuChu `cc` | 0xCF | +0x822 | matching |
| Keese `ki` | 0xD8 | +0x92A | matching |
| Armos Knight `am` | 0xCB | +0xAC2 | matching |
| Armos `am2` | 0xCC | +0xA92 | matching |
| Kargaroc `bb` | 0xB6 | +0xC86 | matching |
| Floormaster `fm` | 0x79 | +0xAEE | matching |
| Morth `ks` | 0xCE | +0x6AE | matching; group ENV |
| Darknut `tn` | 0xC0 | +0x152A | matches D44J01 only; live check first |
| Peahat `ph` | 0xD2 | +0x99E | NonMatching; live check first |
| Miniblin `pt` | 0xF9 | +0x7EA | NonMatching; live check first |
| Poe `pw` | 0xD5 | +0x88A | NonMatching; live check first |
| Boko Baba `bo` | 0xD7 | +0xA02 | NonMatching; live check first |

The proc numbers are from the research briefs. M1 checked them against the GZLE01 `fpcNm` enum (`f_pc_name.h`); all 16 match, as do the timer offsets.

**Exclusions**
- Bosses and mini-bosses: BOSS status, plus the proc denylist bdk, bmd, bwd, bst, btd, bgn*, gnd, bpw, fganon, daiocta, and the gm (Mothula) and wz mini-boss variants. Ordinary Mothulas and Wizzrobes stay eligible for the M7 KILL op.
- setID 0xFFFF and stage-level actors (room -1).
- A ReDead while its grab event runs (`d_a_rd.cpp:1081-1160`).
- Boss outcomes travel through `STAGE_BOSS_ENEMY` and the switches instead.

**Consequences**
- Room-clear logic (ALLdie, genocide chests, kill-all bars) polls for living enemies (`fopAcM_myRoomSearchEnemy`, `f_op_actor_mng.cpp:1271-1294`), so it fires on the receiver by itself.
- The room-clear switch is also synced (§6.2), which covers unkeyed spawns.
- Each player gets their own random item drop.
- **Known MVP artefact:** entering a room the partner cleared shows keyed enemies spawn and then shrink. M7 removes this.

### 7.3 Later

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
- derived keys and hearts match the formulas in scripted scenarios (same key twice, door opened remotely, key spent on an open door, phantom-free door coupling);
- capacity is never lowered by an ordered grant.

**Tag rules.** FakeGame reproduces New Game (`dComIfGs_init` keeps `mReserve`), `card_to_memory` and `ExDataSave`. V1-V6 reject every leak case and accept real bound files.

**World file.** Crash injection: truncated tmp file, kill between write and rename, CRC damage falling back to `.1`.

**Parsers.** Fuzz targets for every 'O' sub-codec, the extended Hello, the world file, GCI/card images and Yaz0/RARC/DZR, in the style of `internal/network/fuzz_test.go`. Stage readers are tested on synthetic fixtures built in test code; no Nintendo data ships.

**Combat logic.** Actor walk bounds and validation on FakeMem; the K1 gate matrix; kill-union scoping.

**From M7:**
- blob invariants: T2 end ≤ mailbox, region bounds, arenaLo from `DOLPatches` merged over vanilla words (§4.5), each hook is a `bl` into T2;
- generated constants are fresh (`extract_blob.py && git diff --exit-code`);
- optional clang `powerpc-eabi` size check on `coop.c` (.text ≤ 0x800).

### 8.2 Owner machine (two-Dolphin harness)

**Bootstrap:**
```
SAVE_STATE=$(pwd)/saves/start.sav ./ww-multiplayer.exe dolphin2
./ww-multiplayer.exe mp-local --coop
```
Run `mp-local` in the background.

**Validation tools:** `coop-probe`, `coop-watch`, `actors`, `zone-dump`, `peek`, `screenshot` and `input`. Anything visual is accepted only on a screenshot pair, never on a memory readback (CLAUDE.md "Visual validation").

**Fixtures.** Go-only milestones never invalidate save states. The owner captures each of these once with Shift+F1:
- F1 `saves/start.sav` (exists).
- F2 the first room of Dragon Roost Cavern, with a small-key chest reachable, on both cards.
- F3 an enemy room with Bokoblins and ChuChus.
- F4 a heart-piece location.
- F5 a puzzle room with an un-timed crystal and a barred door.

`stage-hop` (M0, debug) can stand in for F2-F5 if dungeons load without story progress (inferred).

**Running M2 (dry run).** M2 writes nothing to the game except the binding tag, and only after a yes. The planner only logs. Owner steps for the M2 acceptance:
1. Load the file to share in Dolphin 1 and stand in game, out of menus and cutscenes. Run `mp-local --coop=new` (or `host --coop=new`) to found the world from that file.
   - The first lines list the memory cards backed up, each with its files and whether Dolphin.ini selects it ("in use"), then the world ID. Check that they are your cards: the card location comes from Dolphin's source, not the decomp (§5.4). A warning that no card is in use means the backup may have missed yours: set `USER_DIR_1` to your Dolphin user folder.
   - Later sessions use `--coop` (serves the only world) or `--coop=<world-id>`.
   - A second machine joins with `join <host> --coop`.
2. Answer each question with `y` and Enter, or pass `--yes` to answer them all. "Bind it?" asks to bind the file; a leaked tag gets "Scrub it?" as well.
   - Then save in game, so the card keeps the tag.
   - `coop-tag show` (with `WW_DOLPHIN_INDEX`) must read Bound after a save and reload. Before the reload, a file bound fresh reads Bound only on the host, whose world file records the pending fresh bind; on a joiner's machine it reads "leaked, or a pending fresh bind".
3. Play on both Dolphins. Each log shows its dry-run plan: "would set …" and "would raise …" lines for the partner's progress, never for life, rupees or another PER_PLAYER byte. `coop-world list` shows the world's seq growing.
4. Check that ww-multiplayer wrote nothing but the tag: `coop-watch` on each Dolphin shows only the game's own edges, and `peek 0x803C532C 48` holds the tag.
5. Run the recovery drills, with the session stopped (Ctrl+C):
   - `coop-world restore` lists the card backups; `coop-world restore <backup> --yes` puts one back, and only while Dolphin is closed.
   - `coop-world block <world-id> chest <table> <no> --yes` tombstones a bit.
   - `coop-tag clear --yes` unbinds a file.
   - Restart `mp-local --coop`: the world comes back at the same seq.
6. Run host, join and mp-local once without `--coop`: they behave exactly as before.

### 8.3 Day-0 read-only probes (runnable now with today's binary)
```
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C9D3C 12   # stage name ("sea\0..."), point s16, room, layer
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C53A4 4    # byte0 = save table (0 at sea; on the title it is sea_T's table; 0xFF only between dSv_info_c::init and the next stage load)
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C9EA0 4    # [mOrderCount][..][mMode][..]; mMode != 0 while talking
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C9D54 4    # byte0 = mNextStage.mEnable (1 briefly at a loading zone)
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C4C08 4    # maxLife u16 | life u16 in quarter hearts (0x000C000C new file)
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C5380 36   # live memBit of the current table
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C532C 48   # dSv_reserve_c: expect all zero on a vanilla file
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803C5E98 4    # byte0 = mDataNum (file slot)
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x80372028 12   # actor queue head, tail, count
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x80412F00 4    # spawn_trigger; run twice, must advance
WW_DOLPHIN_INDEX=0 ./ww-multiplayer.exe peek 0x803F773C 4    # JKRHeap::mCodeEnd: just above 0x80414000 on the patched ISO, about 0x80414040 (inferred). __OSArenaLo itself is moved to the arena top by JKRHeap::initArena
```

**Optional reversible HUD test, at sea only:**
1. `poke-u32 0x803CA77C 0x00000004` writes key count 0 and max life +4. Take a screenshot: 3 hearts become 4, refilled (`d_meter.cpp:1426-1427`).
2. `poke-u32 0x803CA77C 0x0000FFFC` brings it back to 3.

---

## 9. Milestones

**Where things are built**
- **cloud** = this Linux container: Go code and CI.
- **owner** = the Windows machine with Dolphin: live acceptance, devkitPPC builds, Shift+F1 recaptures, the user's ISO.

**Release map**

| M | Ships as | Needs C | Built where |
|---|---|---|---|
| M0 Foundations and live probes | v0.4.0-dev | no | cloud; probes on owner |
| M1 Stage-data tables from the user's ISO | v0.4.0-dev | no | cloud; ISO run on owner |
| M2 Binding, backups, protocol v2 (dry run) | v0.4.0 (co-op preview) | no | cloud; owner acceptance |
| M3 Shared world flags | v0.5.0 | no | cloud; owner acceptance |
| M4 Shared progression and save merge | v0.6.0 | no | cloud; owner acceptance |
| M5 Live room puzzles | v0.7.0 | no | cloud; owner acceptance |
| M6 Combat sync v1 | v0.8.0 | no | cloud; owner acceptance |
| M7 C co-op pump | v0.9.0 | **yes** | C build, recapture and acceptance on owner; Go and invariants in cloud |
| M8 v1.0 release hardening | v1.0.0 | no | both |
| M9 (post-1.0) Create-only refresh, chest-claim arbitration | v1.1 | yes | owner |

**Dependencies**
- M0 → M1 → M2 → M3 → M4.
- M5 needs M1 and M3. M6 needs M1 and M3.
- M7 needs M3 plus finish-plan D2. M8 needs everything.
- M0 and M1 can run in parallel with finish-plan milestone C.
- C5 (#9, stage-mismatch hide) should reuse M0's address table and PRESENCE.

The structured milestone list gives scope and acceptance for each one. The summaries follow.

- **M0.** Mem, FakeMem and FakeGame; the cited address table; the class-table golden test. Diagnostics `coop-probe`, `coop-watch`, `actors`, `zone-dump`, `frame-phase-probe`, `heap-free`, `region-probe arm|check`, `blob-verify --live` (live T2 read-back against `T2Bytes`), and debug `stage-hop`. Fix docs/04 and record #23.
- **M1.** Pure-Go Yaz0, RARC and DZS/DZR readers. `coop-tables <iso>`: keys and doors, heart sources, switch classes, push blocks, enemy ACTR with a setID report. That report is the go/no-go for M6.
- **M2.** Binding tag (V1-V6, scrub), backups, Hello role byte, 'O' codecs, CoopHub, world file, and the coop-sync pipeline in **dry run** with the join preflight.
- **M3.** The first release that writes game data. memBit OR classes, latching memory switches (deferred for the current table), ocean and charts. FrameWriter, WriteGuard, verify, auto-disable. Refresh area and warp.
- **M4.** Inventory union with ordered grants; derived keys, hearts and magic; story bits applied on transition, with holds; the register allowlist; `--coop-merge`; offline `save-diff`/`save-merge`.
- **M5.** Dan and zone sync for co-present players; room-edge relay.
- **M6.** Kill union and the K1 applier.
- **M7.** Make room; the getSave shim; the isActor shim; `coop_frame` live OR and the op ring. Go switches to image and ops.
- **M8.** TUI co-op panel, soak tests, docs, release.

---

## 10. Open questions for the owner

1. **Save-merge model.** `Per-flag` or `Full-file`? **Recommended: Per-flag.** That means live union by class, plus a union when a file is bound, with a preview. About half of `dSv_save_c` is not monotonic (life, counters, registers, toggles), so a whole-file merge cannot be done safely or live. Per-flag sync also reconciles offline progress on every reconnect. The offline `save-merge` tool still provides a previewable full-file merge.
2. **Rupees.** `Separate` or `Shared`? **Recommended: Separate.** A shared wallet means applying deltas through `mItemRupeeCount` (0x803CA768), and chest duplicates would multiply the money. It can come later as an option.
3. **A partner's switch in your current room.** `Deferred` or `Live`? **Recommended: Deferred** as the default, with Live as an opt-in. Applying at your next room change avoids the partner's crystal starting your door cutscene mid-fight. Within the same room, dan and zone switches still sync live.
4. **Code room for M7.** `Arena` or `TRK`? **Recommended: Arena** (+8 KB, mailbox moves to 0x80415000). The mechanism is proven, and the heap error counters gate it. The TRK region costs no heap but is free only by inference.
5. **datstarkey/ww-online** (MIT, v0.7.0 on 2026-10-13, Windows-only, needs 48 MB MEM1). `Port`, `Collaborate` or `Ignore`? **Recommended: Port.** Reuse its verified facts (LocalOnly lists, key and heart counts, switch classes, test checklists) with attribution in THIRD_PARTY notices. Keep our differentiators: macOS, Go/TUI, and no 48 MB requirement. ww-online also renders remote players as animated Links, so the mini-Link is parity, not a differentiator.

Also needed from the owner, and not a decision: capture the fixture save states F2-F5 once (§8.2).

---

## 11. Proposed GitHub sub-issues (all under parent #23)

**Epic E1: Co-op foundations**
1. **coop: Mem interface, FakeMem/FakeGame, cited address table.** Abstract Dolphin reads and writes behind `Mem`, and model the vanilla save behaviours this doc relies on. Every Appendix A address carries a decomp citation and an arithmetic test.
2. **coop: dSv_save_c byte-class table with golden coverage.** Classify all 0x778 bytes as OR, OR_MASKED, MAX, DERIVED, PER_PLAYER, NEVER or TAG. CI fails on any gap; the table drives the differ, the server and the WriteGuard.
3. **coop: read-only diagnostics.** Add `coop-probe` (stage, table, room, gates, pending counters), `coop-watch` (labelled bit edges) and `frame-phase-probe`. The owner runs them to confirm the inferred addresses.
4. **coop: actors and zone-dump via g_fopAcTg_Queue.** A bounded walk of 0x80372028, validated by `g_fopAc_type`. Print proc, setID, rooms, health, the enemyice self-pointer and the 32 zones.
5. **coop: heap-free probe and region canaries.** Walk the Zelda, Game and Archive heap free lists and read the error counters. Arm and check canaries over 0x8040CFB0-0x80410000 and 0x80413080-0x80414000 (not 0x8040CFA8-0x8040CFB0, the stack-top frame header boot writes, §4.5).
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
21. **coop: refresh area and warp to partner.** A best-effort Go `mNextStage` write before M7 and the `SET_NEXT_STAGE` op after it. Same-stage warps use the mailbox `warp_seq`.

**Epic E5: Live puzzles and combat**
22. **coop: dan and zone switch sync for co-present players.** Scoped by stage (dan) and by stage plus room (zone), using the latching table. 0xE0-0xEF are relayed live only and never stored.
23. **coop: kill union and the K1 light-shrink applier.** Zone actor-bit diffs, a scoped KILL store, and the self-pointer, health, event and boss gates. Includes an `enemy-kill <procID>` debug command.
24. **coop: fixture save states and the combat/puzzle checklist.** Document F2-F5 and script screenshot pairs for each acceptance item.

**Epic E6: C co-op pump**
25. **inject: make room.** D2 cleanup; arena 0x80414000 to 0x80416000; mailbox to 0x80415000 with a 'WWMB' header; constants emitted into `blob.go`. Layout invariants in CI, heap counters checked by the owner.
26. **inject: getSave shim in dStage_stagInfoInit, plus coop_frame live OR.** Locate the `bl` with `ppc-disasm`. ORs the world image into `mSavedata` under `image_ok` (armed, dead-man, slot, tag fingerprint, `mDateIPL`, refight).
27. **inject: isActor call-site shim and the op ring.** Dead tables per stage. Ops OR8, GRANT, ADD_S16, SET_U8, KILL, SET_NEXT_STAGE and ON_SWITCH, behind a whitelist and the IDLE gate.

**Epic E7: v1.0 release**
28. **tui: co-op panel, feature toggles, coop off.** Bound/armed state, holds, last syncs, behind-by-N, at 80x24. Host-authoritative feature mask.
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
| `g_fopAcTg_Queue` (head, tail, size; node = &actor->actor_tag; prev +0, data +4 (= 0x80372028 when queued), next +8; actor = *(u32*)(node + 0xC) (`mpTagData`), equivalently node − 0xC4) | 0x80372028 | 0xC | sym:14653; c_list.h:6-10; c_list.cpp:39-40; c_tag.h:10-14; c_tag.cpp:89-92; f_op_actor.h:272 |
| `g_fopAc_type` | 0x803F69D0 | 4 | sym:18627; f_op_actor.cpp:116-119 |
| `DMC` (REL module control table) | 0x803B9218 | 0x7D8 | sym:17371 |
| `__OSArenaLo` variable (moved to the arena top by `JKRHeap::initArena` at boot, `JKRHeap.cpp:77-95`; read `JKRHeap::mCodeEnd` 0x803F773C, sym:19563, for the patched base) | 0x803F66C0 | 4 | sym:18462 |
| heap ptrs zelda / game / archive | 0x803F6928 / 0x803F6920 / 0x803F6938 | 4 each | sym:18588-18594 |
| heap error counters zelda / game / archive | 0x803F68FC / 0x803F68F8 / 0x803F6904 | 4 each | sym:18577-18580 |
| actor fields | mBsPcId +0x04, mProcName +0x08, mParameters +0xB0, actor_type +0xC0, eventInfo.mCommand +0xF8, setID +0x1BC, group +0x1BE, actor_status +0x1C4, home.pos +0x1D0, home.roomNo +0x1E2, health +0x285 | fopAc_ac_c 0x290 | f_pc_base.h:13-32; f_op_actor.h:268-321 |
| mod T2 / mailbox / patched arenaLo | 0x80410000..0x80412CA8 / 0x80412F00..0x80413080 / 0x80414000 | — | inject/build.py:109,142-145; mailbox.h:33 |
| `spawn_trigger` (heartbeat) / `warp_seq` | 0x80412F00 / 0x80412FC8 | 4 / 4 | mailbox.h:109,221 |

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
11. **Heart totals:** vanilla is 44 pieces + 6 containers. 12 + 44 + 24 = 0x50 quarter hearts, the HUD cap (`d_meter.cpp:1432`). A research brief counted 46 piece rows in wwrando data; M1 resolves this against the user's ISO.
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

22. **Helmaroc King creates no heart container.** It sets `STAGE_BOSS_ENEMY` (`d_a_bdk.cpp:3232`, `:4235`), but unlike the other five bosses (`d_a_btd.cpp:1082`, `d_a_bmd.cpp:747`, `d_a_bst.cpp:2504`, `d_a_bpw.cpp:2856`, `d_a_bwd.cpp:1704`) it makes no container, so Forsaken Fortress's comes from a Bitem or a placed one.
    - Every container is recorded only by `STAGE_LIFE`: for the stage's save table, or for table 2 (Forsaken Fortress) when taken on the sea (`d_item.cpp:588-604`).
    - A Bitem is not a separate container: it re-creates the boss's while that bit is clear (`d_a_boss_item.cpp:24-41`).
23. **44 or 46 pieces (item 11) needs the real disc.** The catalogue has 11 decomp-verified rewards plus 3 minigames whose grant code is not decompiled (`d_a_npc_kg1`; `d_a_npc_kg2` `evn_createItem_init`, `:167-170`; `d_a_npc_bmcon1` `eventGetItemInit`, `:159-162`). The stage files must supply 30 to reach 44.
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
29. **Setters §6.2 doesn't list.** M1 classifies these, with citations in `internal/coop/tables/switches`.
    - Latching: kill-all door bars (door10 front type 2 and back type 5, door12 front type 2, kddoor front type 1 and back types 2/4, mbdoor type 1); chest open switches (`home.angle.z`) and enemy-chest switches routed in the room from `home.angle.x`; enemy death switches (BK, MO2, ST, TN); the boss Bokoblin's room switches 0xE0/0xE1; many breakables and one-shot objects.
    - Unsafe: Helmaroc King's dan 0x80, Jalhalla's light switches, Phantom Ganon's second switch, the Tingle Tuner switches (agbsw0), the propeller switch (it toggles), Obj_Correct, the Ferris wheel, the pig dig spot (TAG_KB_ITEM), Tag_Volcano, Zephos/Cyclos (NPC_HR), the second switch of the hammer switch (MhmrSW0), and sun switches (swlight type 1).
30. **Switch users the decomp can't decode** are listed in the switches report under "Undecoded switch users".
    - They are Obj_Swflat (the Hfbot1A-C floor switches) and NonMatching actors whose switch getters are empty: Itnak, Vds, Ashut, Ekskz, Firewall, Flame, Hami2, Htetu1, Kanoke, Msdan, Tapestry, TnTrap, Tribox, BigElf, Kk1, P2, PT, WZ.
    - A switch one of them shares with latching setters can wrongly show as synced, so the owner reviews the list on the real ISO.
    - Obj_Try has a real switch field but no decoded code, so its writers are classed Unknown.
    - The switch a stage's EVNT entry sets as its event starts (`dEvent_exception_c::setStartDemo`, `d_event_manager.cpp:23-48`) is not covered: stagedata keeps no EVNT entries.
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
    - Feature bits live in `internal/coop/hub`: FeatureImage, FeaturePuzzle and FeatureCombat, plus the grant-only FeatureMerge (1<<8) and FeatureBind (1<<9) and the hello-only AskBind (1<<31) and AskUnsaved (1<<30), which carry §5.3's bind request and fresh bind.
    - A GAINS batch is a repeat when `int32(batch - last) <= 0`, so the counter can wrap.
    - NOTICE text is valid UTF-8 without control characters, at most 255 bytes, as player names are. `ClassHash` is FNV-1a 64 over the class spans.
42. **Kept as specified, a limit:** a relayed PRESENCE or SCOPED carries no sender. That is fine for two players; more would need an origin byte and a Schema bump.
43. **Player connections are protocol 1 apart from the version.** Players still send the 2-byte hello; only co-op connections send `[version][role][caps]`. Co-op connections get a normal MsgWelcome with an ID from their own 1..255 space. RejectRole (4) is fatal, so a co-op client stops at once against a host without `--coop`.
44. **A known client race, not fixed (internal/network).** `readLoop`'s error path marks the client disconnected without checking that its conn is still the current one; `abandonSession` does check. A Disconnect followed at once by a Connect on the same Client can lose the new session. RunCoop never hits it, and not because of a backoff: after a session that lived `minHealthySession` it redials at once. Its `live` ends a session early only after the read loop itself has cleared `connected`, which is that loop's last write, and `Replica.Session` returns only when its context ends, so no stale read loop is left to clear the next session's flag. A session function that returns while its connection is still open would need the race fixed first. coop-sync keeps one Client per pipeline, as the player pipelines do.

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
49. **Scrub offers.** Failures of V2-V4 are offered at once. A V5-only failure naming the session's world is offered once a welcome says it is no pending fresh bind. A REFUSED welcome says nothing either way (its FreshBind is always false), so the replica learns fresh binds only from OK and UNBOUND welcomes: a host restarted on another build must not turn a pending fresh bind into a scrub offer. A V5-only failure naming another world is never offered, because only that world's host knows; `coop-tag clear` remains.
    - `coop-tag show` and `coop-tag clear` read the pending fresh binds from the world files on the machine. Without the tag's world file (a joiner), a V5-only failure is shown as "leaked, or a pending fresh bind", with no advice to clear it, and clear warns that it may unbind the file.
50. **Citation fix:** `initdata_to_card` zeroes `dSv_reserve_c` at `d_save.cpp:2007-2010` (§5.2 said 2006-2010; 2006 is blank).

*Backups (§5.4)*

51. **The card locations come from Dolphin's source, not the decomp**, and need the owner's confirmation in M2 (§8.2). That covers the EXIDeviceType values (1 raw, 8 GCI folder), the default file and folder names, and how configured paths get a region code.
    - Because a wrong guess means no backup, every card that exists is backed up. The session start lists each card with the files copied from it and whether Dolphin.ini selects it ("in use"), and warns when it selects none; `coop-world restore <backup>` marks them the same way, and the backup's `manifest.json` records them. GCI folders contribute Wind Waker's files (GZLE) only.
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
55. **Scoped store lifetimes.**
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
    - Block edits the world file only while no session serves it: a running hub keeps the world in memory, and its next write would undo the edit. The host marks the world in use with `session.lock` in its directory. `--force` overrides a marker a crash left, and the next host start warns about it.
    - In-session blocking is the hub's CTRL TOMBSTONE path (`hub.Hub.Tombstone`), which M8's panel will drive. Bound replicas learn a block at their next welcome.
    - `coop-world list` (read-only) was added beside block and restore.
67. **`stageTblHash` is 0 on both sides in M2:** no M1 tables are loaded into a session yet. M3 sends their identity.
