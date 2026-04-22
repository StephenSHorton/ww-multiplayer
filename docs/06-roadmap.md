# Roadmap

## ✅ Done

- Go project scaffold with Bubble Tea TUI
- Dolphin memory scanner (works on any Dolphin version)
- Real-time position reading at 50ms
- Proven writes to game data (rupees, Link's position/teleport)
- TCP networking with server and fake clients
- Freighter setup for C code compilation
- **Built proper ISO from CISO + patched DOL using `wit` + FST shift**
- **Confirmed `main01` at 0x80006338 is a one-shot init function**; per-frame is `fapGm_Execute` at 0x800231E4
- **Cracked the `ClearArena` wall** by patching OSInit directly (`lis r3, 0x8041 / addi r3, r3, 0x1000` at 0x8030181C-0x80301820) to set `__OSArenaLo = 0x80411000` before `ClearArena`'s memset runs
- **Discovered Freighter silently clobbers game code** to relocate stack/arena to its own aspirational values — found 5 regions in T0/T1 that must be reverted post-build to keep game rendering correct
- **Found workable inject address** (`0x80410000`) — Dolphin's DOL loader refuses `0x803FDxxx` (just past BSS) but accepts addresses further up; still unknown exactly what Dolphin checks
- **Relocated mailbox** from `0x803F6100` (actually inside game data section D6, corrupting real data) to `0x80410800` (orphan memory between T2 end and `__OSArenaLo`)
- **End-to-end code injection verified**: main01 hook fires, our C code runs, mailbox counter increments, game continues rendering correctly
- **Per-frame hook working**: callback-pointer shim at `0x80023204` inside `fapGm_Execute`, with bl-replay via LR-preserving `bctr` tail-call to `0x802449AC`. Mailbox counter ticks at 30Hz in-game. See `docs/05-known-issues.md` → "Per-Frame Hook — SOLVED" for the shim recipe.
- **Queued-spawn pipeline proven end-to-end** (2026-04-18): `fopAcM_create(PROC_GRASS, 0, link_pos, room, link_angle, 0, -1, 0)` queues cleanly (pid `0x232`), `fpcM_Management` constructs the actor next frame, grass tuft renders at Link's position. Frame hook + shim + queued spawn is a stable foundation for syncing a remote player's position to any resident actor. PROC_Obj_Barrel froze (archive not on Outset); PROC_GRASS is always resident.
- **Full position-sync pipeline proven** (2026-04-18): `PROC_TSUBO` (pot, param=0 → "Always" archive) spawns with valid model pointer at `actor + 0x298` (`mpModel`, not base-class `+0x24C`). Programmatically-spawned pots idle in `mode_hide` (`m678 = 0` at actor+0x678); writing `m678 = 2` (mode_wait) makes them render and accept position writes. Verified live: Go `./ww.exe move-puppet x y z` → mailbox f32s → frame-hook copies to actor+0x1F8 → pot visibly teleports in-game. End-to-end loop from a host-side command to a visible remote actor is complete.
- **End-to-end network multiplayer working** (2026-04-18): full round-trip
  verified on a single machine via loopback. Pipeline:
  `broadcast-link` reads Link's live position → TCP → `server` relays →
  `puppet-sync` applies client-side lerp smoothing (k=0.2) → mailbox →
  C frame hook → puppet actor follows Link around Outset with ~80 ms trail.
  On two machines, swap `broadcast-link` for the remote player's instance
  and you have actual multiplayer. Three CLI commands now form the runtime:
  `server`, `broadcast-link <name> <addr>`, `puppet-sync <name> <addr>`,
  plus one-shot `unhide-puppet` (the m678=2 poke must run ~3 s after spawn
  — doing it inside the C hook from frame 1 of phase 2 corrupts TSUBO's
  construction and freezes the game).
- **Rotation sync** (2026-04-18): puppet-sync writes the remote's RotY to
  `mailbox.p2_rot_*` (s16 BE) every tick; the frame hook copies both
  `current.angle` (+0x204) and `shape_angle` (+0x20C) so the puppet faces
  the direction the remote is facing. No angular lerp yet (would need
  shortest-arc handling); raw copy looks clean at 20 Hz send / 60 Hz apply.
- **Humanoid-ish proxy working: seagull** (2026-04-18): `PROC_KAMOME`
  (0x00C3) — archive is resident on every sea-adjacent island. Spawns
  constructed-but-invisible because `daKamome_Draw` guards on
  `mSwitchNo != 0 || mbNoDraw != 0`; with param=0 the spawner leaves
  `mSwitchNo = 1` at `actor + 0x2AA`. Zeroing that byte makes the bird
  render with its glide animation, tracks Link's position over real TCP,
  and rotates to match. `unhide-puppet` now dispatches by proc: writes
  `m678 = 2` for TSUBO, clears `mSwitchNo` for KAMOME.
- **Multi-puppet architecture** (2026-04-18): mailbox redesigned around
  `MAX_PUPPETS = 4` slots of 0x20 B each (`inject/include/mailbox.h`).
  Each slot is `{actor_ptr, active, pos_xyz, rot_xyz}`. C loops over
  slots every frame: spawns on active+unspawned, syncs spawned, drops
  bookkeeping when `active` clears or the actor dies. Go `puppet-sync`
  maps remote player IDs -> slot indices and writes each remote's pos/rot
  to its assigned slot. Verified live: two fake-clients on one machine
  drove two separate puppets (seagull + pot) following two distinct
  circle patterns on Outset.
- **Human NPC puppet: Rose** (2026-04-18): `PROC_NPC_OB1` (0x014D) —
  one of the outdoor Outset villagers — has her archive preloaded with
  the stage. Spawns cleanly, stays alive, accepts position/rotation writes
  like any other actor. No mode_hide-style render guard — she renders
  immediately without an unhide poke. This is the first **human**
  puppet: Slot 1 in the current demo spawns Rose; a remote player's
  position makes Rose-the-NPC walk around Outset driven by TCP. Kids
  (NPC_KO1) still self-destruct because their archive isn't resident
  outdoors; fairies (NPC_FA1) self-delete after healing — Rose is the
  proven path for now. Other outdoor villagers (Abe, Mesa, Sturgeon)
  are likely resident too.
- **Per-slot proc differentiation** (2026-04-18): slot 0 spawns KAMOME,
  slot 1 spawns NPC_OB1 (Rose), slot 2 KAMOME, slot 3 TSUBO. Each slot
  is visually distinct so multiple remote players are immediately
  identifiable. Proper color tinting would need a mid-draw hook (KAMOME's
  `daKamome_Draw` rebuilds `actor.tevStr` every frame via
  `g_env_light.setLightTevColorType`, clobbering any execute-phase
  color override); mixed procs gives stronger differentiation anyway.
- **Mini-Link pipeline — plumbing proven, rendering blocked**
  (2026-04-19): `getRes("Link", 0x18, &mObjectInfo[0], 64)` returns
  valid `J3DModelData`; `mDoExt_J3DModel__create` returns non-NULL
  `J3DModel*` allocated into ArchiveHeap. Freighter draw-phase hook
  installed at `0x80108210` (the `bl daPy_lk_c::draw` inside
  `daPy_Draw`) — our C shim calls Link's real draw at `0x80107308`
  and then our per-frame matrix+submit. Mailbox moved to `0x80410F00`
  flush against `__OSArenaLo`. Fixed a major `getRes` arg-count bug
  (static member mistaken for non-static — shifted every arg by one
  register and spammed `<System.arc> getRes: res nothing !!` at ~143
  logs/sec). Two blockers remain (see "Next Session Priority" and
  docs/05 "Mini-Link render pipeline"): `mDoExt_modelEntryDL` breaks
  sky rendering regardless of phase; `J3DModel::calc()` crashes Link
  via j3dSys global pollution.
- **First independent visible Link rendering via our pipeline**
  (2026-04-19 late): Two Links visible on Outset, no crash, sky clean,
  `draw_progress=38` (full path) every frame. The missing piece was
  `J3DModel::mUserArea` (offset 0x14): Link's joint callbacks (bound
  to the shared J3DModelData via J3DJoint subclasses) recover the
  owning `daPy_lk_c*` from `model->getUserArea()` during calc, then
  read its state. With our mini-Link's mUserArea = 0, the callback
  derefed NULL at PC 0x8010C53C inside checkEquipAnime. Wiring
  `*(u32*)(mini_link_model + 0x14) = (u32)link_actor` each frame in
  the draw hook routes the callback to Link #1, calc completes, the
  bone matrices populate, modelEntryDL submits cleanly. Full 0x128
  j3dSys snapshot around calc is kept as a safety net but the
  userArea write is the actual unblock. **Bisect path that found it**:
  step 1 (5-field j3dSys save/restore) → still stuck at 34. Step 2
  (full 0x128 snapshot) → still stuck at 35. Stub calc + modelEntryDL
  → 38 stable, infrastructure proven innocent. Re-enable calc only →
  back to stuck at 35. Confirmed calc was the singular failure. Hit
  zeldaret/tww decomp for J3DModel layout: `mUserArea @ +0x14`,
  every actor with bound callbacks does
  `model->setUserArea((u32)this)`. Fix verified live — second Link
  walks/idles/swings sword identically to Link #1 (joint callback
  acts on Link #1's state). Independent animation is the next wall.
- **First visible geometry from our render pipeline — rigid model
  working end-to-end** (2026-04-19): Tsubo fragment
  (ALWAYS_BDL_MPM_TUBO=0x31) renders and tracks Link in real time,
  sky clean, no crashes. Recipe: `getRes("Always", 0x31, mObjectInfo,
  64)` → `mDoExt_J3DModel__create(data, 0x80000, 0x11000022)` in
  ArchiveHeap; each frame inside `daPy_Draw` hook (after Link's real
  draw returns): write base matrix @ J3DModel+0x24 → save
  `j3dSys.mModel`+`mCurrentMtxCalc` (offsets 0x38/0x30 in the j3dSys
  struct @ 0x803EDA58) → `J3DModel::calc` @ 0x802EE8C0 → restore →
  `mDoExt_modelEntryDL` @ 0x8000F974. **Unblocked two open blockers
  from the previous session**: (1) `modelEntryDL`'s sky breakage
  root cause was SHARED J3DModelData — entry()'s per-frame bucket
  insertion double-registered Link's material packets. Non-shared
  data submits cleanly. (2) `calc()`'s crash was confirmed as j3dSys
  global pollution; a minimal 2-field save/restore is sufficient for
  rigid models. Matrix propagation (base→node→drawMtx) also proven
  essential — without `calc()` the GX draw buffer stays uninitialized
  and renders at origin (invisible), which was silently defeating
  earlier "no calc" experiments. Skinned Link's model still crashes
  the same way under calc — its skeleton walk touches additional
  j3dSys fields beyond the 2 we save. Next session: expanded
  save/restore or separate Link archive copy. Diagnostic aid added:
  `mailbox.draw_progress` (+0x0C) so the draw hook's furthest
  execution point is observable independent of execute-phase writes.
- **Multi-Link plumbing + two-Dolphin launcher** (2026-04-19 latest):
  Mailbox pose fields converted to per-slot arrays sized
  `MAX_REMOTE_LINKS`. C side carries `mini_link_models[]` +
  `pose_bufs[]`, mode 5 iterates slots with per-slot lazy alloc + base
  matrix + first calc + pose copy + double-calc. Render gated on
  `pose_seqs[slot] != 0`. Go side `puppet-sync` elects link slots via
  `pickLinkSlot` and writes per-slot pose bufs; new `pose-fake-loop` CLI
  ships a captured pose on a +1000 X position offset for multi-Link
  diagnostic. `dolphin.Find()` selects between multiple Dolphin
  processes via `WW_DOLPHIN_INDEX` (or `WW_DOLPHIN_PID`). New
  `scripts/dolphin2.sh` bootstraps a "Dolphin Emulator 2" user dir +
  launches a 2nd Dolphin against the patched ISO; `scripts/mplay2.sh`
  spins up server + bidirectional broadcast/sync between both
  instances. Single-Link mode still works (Sender mirror east of Link
  via TCP). N>1 hits a J3D shared-`J3DModelData` material-packet
  pollution issue (per-instance puppets render at correct distinct
  positions but share the same frozen pose) — `MAX_REMOTE_LINKS = 1`
  cap until the per-instance packet allocation OR
  `mDoExt_modelEntry`+`mDoExt_modelUpdateDL` path is wired. Two-Dolphin
  multiplayer (the immediate MVP) only needs N=1.
- **Multi-Link N>1 unblocked + server write race fixed** (2026-04-20):
  Multiple independent Link puppets now animate correctly in a single
  scene. Root cause of the N>1 "both puppets stuck on one pose" was a
  single flag bit: `mDoExt_J3DModel__create(data, 0x80000, ...)` routed
  `createMatPacket` (`J3DModel.cpp:264`) into the "shared DL" branch,
  which pointed every instance's `mpMatPacket[i].mpDisplayListObj` at
  the display list object owned by `J3DModelData`. Each frame every
  instance's `J3DModel::entry()` called `J3DJoint::entryIn()` →
  `mesh->calc(anmMtx)` and `mesh->makeDisplayList()`, both of which
  write into `matPacket->mpDisplayListObj->mpData[active]` — one shared
  buffer for all N instances. Last writer wins. Flipping the flag to
  `0` makes `createMatPacket` take the private-DL branch (`J3DModel.cpp:296-309`)
  which calls `mpMatPacket[i].newDisplayList(size)` per instance. Cost:
  one DL alloc per material per instance at model-create time (paid
  once, Link has ~0x40 materials → tens of KB from ArchiveHeap). Also
  bumped `MAX_REMOTE_LINKS 1→2` in mailbox.h (the runtime loop cap)
  and `maxRemoteLinks 1→2` in main.go (Go slot loop + dump). Verified
  live: real `broadcast-pose Sender` + `pose-fake-loop FakePlayer` +
  `puppet-sync View` on one Dolphin produces 3 Links on Outset, the
  mirror animates live and the frozen decoy stays frozen — exactly
  the "mirror animates, frozen stays still" decisive test documented
  in docs/06 under the N>1 track. Full research report in
  `.omc/research/j3d-shared-modeldata.md` (gitignored; local).

  Second bug surfaced as a side-effect of enabling N=2: server TCP
  writes were racing. `broadcastExcept` called `WriteMessage(p.Conn, ...)`
  with no per-connection mutex, so concurrent sender goroutines
  interleaved bytes on each client's socket. Latent at N=1 (one
  broadcaster; single writer per socket most of the time) but
  GUARANTEED at N=2 (broadcast-A writes MsgPosition+MsgPose to
  puppet-B's socket while broadcast-B writes MsgPosition+MsgPose to
  puppet-A's socket, and the player-list broadcaster writes
  MsgPlayerList to everyone). Clients then read a message header
  whose body bytes came from a different in-flight message → garbage
  player names, and one such misparse tried to read 17169 bytes from
  a 16986-byte buffer and crashed `parsePlayerList` with a slice
  bounds panic. Fixed with `Player.SendMu` + per-connection
  lock/unlock around every server-side `WriteMessage` call. Clean
  logs, no crashes, post-fix.

  Single-remaining cosmetic: when an instance has rendered a second
  pose slot in a prior run (e.g. via `pose-fake-loop`), its GameHeap
  `pose_bufs[1]` and `pose_seqs[1]` persist until Dolphin restart, so
  a frozen decoy Link lingers at the old +1000 X offset even after
  the harness is torn down. Not blocking; next Dolphin boot clears it.
- **Retired the v0.0 Bubble Tea TUI** (2026-04-22, v0.1.2): deleted
  `internal/tui/` (five files), removed the `tui.Run()` fallback in
  `main.go` so `ww.exe` (no args) prints help pointing at `host`/`join`,
  and `go mod tidy` dropped every external dep (Bubble Tea, Lip Gloss,
  Charm Log, the x/ansi + x/cellbuf + x/term + x/exp/strings tree — the
  whole thing was only used by the TUI). First time this repo's `go.mod`
  has no `require` block. The TUI predated the pose-feed protocol and
  silently didn't engage the rendering pipeline, which had new users
  thinking the tool was broken — now there's no path to the broken-mode
  behavior. A successor TUI on top of `host`/`join` remains tracked as a
  polish item if someone misses the interface (one status panel + log
  tail + shutdown button — small scope now that all the real work lives
  in the CLI).
- **`ww.exe host` / `ww.exe join` + graceful shutdown** (2026-04-22,
  v0.1.1): collapsed the five-terminal v0.1.0 workflow (server +
  broadcast-pose + puppet-sync per player) into one process per player.
  Host prints its LAN IPs via `net.InterfaceAddrs` so the joiner knows
  what to type; joiner accepts bare `ww.exe join 192.168.1.42` (defaults
  port to `:25565`). Both commands install a SIGINT/SIGTERM handler that
  cancels a shared `context.Context`, waits for the broadcast-pose +
  puppet-sync goroutines to exit, then writes `shadow_mode = 0` and
  clears `pose_seqs[*]` in the Dolphin mailbox so Link #2 disappears the
  instant the user Ctrl+Cs (closes docs/06 item #8's loose end — the
  mplay2.sh shutdown that left Link #2 frozen forever is now impossible
  because the shutdown path isn't a best-effort script-side cleanup
  anymore, it's inline with the ctx cancel). Also resolves both v0.1.0
  user-testing bugs documented below: (a) TUI no longer matters because
  `ww.exe host/join` is the real user entry point (TUI is now
  vestigial — listed in retire-or-rebuild track), and (b) the self-echo
  ghost-Link bug from running broadcast-pose + puppet-sync on the same
  machine is fixed automatically — host/join pass the player name as
  `runPuppetSyncCtx`'s `selfFilter` param, so users never need to set
  `WW_SELF_NAME` manually. `runBroadcastPose` + `runPuppetSync` were
  refactored into context-aware `*Ctx` variants (`time.Sleep` → `select
  { <-ctx.Done(); <-time.After }`, `os.Exit` → `return err`, watcher
  goroutine that calls `client.Disconnect()` on ctx cancel to break the
  `for client.IsConnected()` loop immediately). The old CLI wrappers
  still call the new functions with `context.Background()` + `os.Exit`
  on error, so `scripts/mplay2.sh` and every existing `./ww.exe server
  / broadcast-pose / puppet-sync` invocation keeps working unchanged
  (WW_SELF_NAME env var is now piped through the wrapper into the
  selfFilter param).
- **Link #2 hidden by default** (2026-04-21): standalone-booted patched
  ISO now looks visually identical to vanilla Wind Waker — no duplicate
  Link mirroring the player. Two C-side gates in `daPy_draw_hook`:
  (a) `shadow_mode == 0` returns early before any setup (mailbox is
  zero-initialized, so a fresh boot defaults to OFF); previous "mode 0
  = baseline mirror" semantics retired since they were a leftover
  debug artifact, not a real user mode. (b) inside mode 5, slot 0's
  `mDoExt_modelEntryDL` now gates on `mailbox->pose_seqs[0] != 0` —
  same as slots 1+ already did. Without this, the brief window between
  mplay2 startup and the first remote pose arrival flashed a
  mirror-Link onto Link #1 (first calc with the real basicMtxCalc
  populates mpNodeMtx with Link #1's pose). Other modes (1-4, dev/
  debug) bypass the pose-seq gate so they still render unconditionally
  for development. Updated `./ww.exe shadow-mode` CLI usage + labels
  accordingly (`0=off`, `1=mirror-refresh`, `2=mirror-freeze`, etc.)
  and `puppet-sync` now clears `pose_seqs[linkSlot]` when a remote
  disconnects gracefully so Link #2 disappears as soon as the other
  player leaves the session. Verified live both directions on Outset.

  Known limitation: ungraceful `mplay2` shutdown (Ctrl+C the script,
  process kill, etc.) doesn't run the per-remote-leave cleanup — no
  TCP "remote left" event reaches the puppet-sync loop because both
  ends of the connection die simultaneously. Result: Link #2 stays
  frozen at the last received pose until the user manually runs
  `./ww.exe shadow-mode 0` (now the explicit kill switch) or restarts
  Dolphin. A signal handler in `puppet-sync` could write
  shadow_mode=0 on SIGINT/SIGTERM to fix this; left as a small
  follow-up.
- **Save-reload safety DONE** (2026-04-21): reloading a save in either
  Dolphin while `mplay2.sh` is running no longer freezes that Dolphin.
  Verified live: D1 reload survives + D2's view of D1 recovers within
  ~1 frame; symmetric reload from D2 also clean. Three orthogonal bugs
  surfaced + fixed in this work. (a) **The actual hang** — game tears
  down Link's J3DModelData during stage unload, our cached
  `mini_link_model` referenced freed ArchiveHeap memory, next frame's
  `J3DModel::calc` derefed it. Fix in `inject/src/multiplayer.c`:
  defensive re-fetch of `*(daPy_lk_c + 0x0328) = mpCLModelData` in
  BOTH `multiplayer_update` (execute) and `daPy_draw_hook` (draw).
  Mismatch ⇒ `mini_link_reset_state()` (NULLs all per-instance state +
  mailbox bookkeeping; never `JKRHeap_free`s — ArchiveHeap is reset
  wholesale on stage unload, so freeing would scramble the freelists).
  Existing `mini_link_state == 0` init path picks up next frame and
  rebuilds against the new mpCLModelData. Belt-and-suspenders: both
  phases check, so stage teardowns landing between execute and draw
  are also caught. (b) **`broadcast-pose` crash** — `runBroadcastPose`
  unconditionally derefed `pos.PosX/Y/Z` in its status `Printf`, but
  `ReadPlayerPosition` returns nil whenever `PLAYER_PTR_ARRAY[0] == 0`
  (precisely the brief window during a save reload). The panic killed
  broadcast-A → TCP dropped → receiving Dolphin's puppet locked at the
  last received pose forever. Guarded the Printf with `pos != nil` and
  print a `[no link — reload in progress?]` sentinel instead.
  (c) **`puppet-sync` cached pose_buf_ptr forever** — `armPoseSlot`
  returned early as soon as `poseBufPtrs[slot] != 0`. If the C-side
  ever lazy-realloced the pose_buf at a new address (e.g. after the
  reset_state above), Go would keep writing fresh poses to the OLD
  address and the receiving Link would render only the seed pose
  forever. Now re-polls `mailbox.pose_buf_states[slot]` and
  `pose_buf_ptrs[slot]` every call; refreshes the cached pointer on
  change, drops it on state == 0, re-enters the lazy-arm wait if
  needed. Logs `re-armed` with the old/new addresses on swap so it's
  observable. (d) **`puppet-sync` never re-asserted shadow_mode = 5**
  — only written inside the lazy-arm path, so a fresh `mplay2` against
  a Dolphin that had `shadow_mode` drifted to 0 (from a previous
  manual `./ww.exe shadow-mode 0`, or any future reset path that
  clears it) would silently fall back to local-mirror Link with no
  pose-feed engagement, even with `pose_seq` bumping correctly each
  tick. Now writes `shadow_mode = 5` once at puppet-sync startup
  before the main loop, idempotent and cheap.

  Diagnosis path was load-bearing: live `./ww.exe dump` of both
  Dolphins' mailboxes is what caught (d) — `pose_seq=129/231` (proving
  Go was writing fresh poses) but `shadow_mode=0` (proving C side was
  ignoring them) is a single-line tell that no amount of code-reading
  would have produced. Mailbox diagnostic fields earned their keep.

  Open mild: D1's flicker on Link #2 in the regression test is "every
  2-4 seconds, very minimal" — same shared-J3DModelData state pollution
  the previous session noted. Not blocking; punted to track #2 below.
- **Two-Dolphin multiplayer working — MVP** (2026-04-20):
  `scripts/mplay2.sh` drives two real Dolphin instances on the same
  machine: each player sees the OTHER's Link walking around Outset at
  the other's actual world coords, no `WW_LINK2_OFFSET` needed. Three
  bugs surfaced in the first session touching the two-Dolphin path and
  got fixed here. (a) **Mailbox layout mismatch**: commit `1398e8e`
  refactored scalar `pose_buf_*` fields into `MAX_REMOTE_LINKS`-sized
  arrays but Go kept offsets designed for 2-slot spacing while C packed
  1-slot tight — `armPoseSlot` polled `state==1` at the wrong byte and
  timed out silently, so pose writes never happened and the C
  `pose_seqs[slot]==0` gate skipped rendering. Fixed by introducing
  `MAILBOX_POSE_SLOT_CAP=2` (struct layout constant decoupled from
  runtime `MAX_REMOTE_LINKS`) so Go's hardcoded offsets stay valid
  regardless of runtime slot count. (b) **Self-echo NPC spawn**:
  `puppet-sync` co-located with `broadcast-pose` on the same Dolphin
  (same `name`) saw its twin's stream as a "remote player", assigned it
  actor slot 1 → spawned an NPC_OB1 (Rose) AT D's own Link's position,
  which then physics-collided and knocked Link into the ocean. Fixed
  with `WW_SELF_NAME` env var that filters same-name remotes (mplay2.sh
  sets it per Dolphin). (c) **Actor spawn for pose-driven remotes**:
  even without self-echo, the actor-slot logic ran before pose-feed
  armed → every pose-driven remote also spawned their actor-puppet
  (KAMOME/Rose) one tick before `active=0` was written. C-side actor
  "cleanup" only stops syncing (doesn't despawn — actor persists in the
  stage until stage unload) so the frozen NPC stuck around forever.
  Fixed by skipping actor-slot activation when the remote's first
  message carries pose. Other infra hardening: `scripts/dolphin2.sh`
  now excludes `Cache/` when bootstrapping the second user dir (primary
  Dolphin holds exclusive locks on its shader cache); `scripts/mplay2.sh`
  staggers client connects by 0.3 s each (four simultaneous connects
  against a freshly-listening server intermittently produced
  "expected welcome, got error or wrong type" on Windows). One known
  artifact: Link #2 flickers occasionally — leftover shared-J3DModelData
  state pollution, matches the N>1 render bug described below but mild
  at N=1. One known risk: reloading a save in one Dolphin while mplay2
  is running freezes that Dolphin (our `mini_link_model` holds a
  dangling reference for a frame while the game tears down Link's
  J3DModel); defensive re-fetch is future work, for now just tear down
  mplay2 before reloading saves.
- **MVP — network pose multiplayer end-to-end** (2026-04-19 latest):
  Player A's Link animations travel through TCP and render on Player
  B's Link #2 at ~50 ms lag. New `shadow_mode = 5` (pose-feed) lazy-allocs
  a 2 KB GameHeap pose buffer, seeds it from current `mpNodeMtx` so the
  first-frame overwrite is identity, then runs the same double-calc as
  mode 4 with the pose source switched from the local echo ring to a
  Go-populated buffer. Wire protocol added `MsgPose='M'` carrying
  `[joints:u16][pad:u16][Mtx[joints]:48*N]` (2020 B/packet for Link;
  ~40 KB/s at 20 Hz). Sender reads `daPy_lk_c + 0x032C → mpCLModel +
  0x8C → mpNodeMtx`, ships the raw 2016 B unmodified (PowerPC big-endian
  matches GameCube native, no byteswap). Receiver writes straight to
  `mailbox.pose_buf_ptr` and bumps `pose_seq`. New CLIs:
  `./ww.exe broadcast-pose <name> <addr>` (sends Link's pose every 50ms
  alongside the existing position broadcast) and `./ww.exe pose-test
  [mirror|freeze]` (offline smoke test that animates pose_buf from a
  local capture buffer; freeze proved Link #2 was rendering correctly
  on top of Link #1 in loopback). `puppet-sync` extended to elect a
  link-driver remote and write pose to pose_buf each tick. Verified
  live: server + broadcast-pose + puppet-sync on one Dolphin instance,
  Link #2 mirrored Link #1 over TCP. Two-Dolphin verification is the
  next milestone. See `docs/05-known-issues.md` "Pose Sync DONE" for
  the recipe + mailbox layout + ISO-FST shift bookkeeping.
- **Independent Link #2 pose via mpNodeMtx capture + delayed replay +
  double-calc** (2026-04-19 late-late-late): `shadow_mode = 4` +
  `echo-delay 30` gives a clean Link #2 animating 0.5 s behind Link #1
  on Outset with no rubber-banding. Track 1 of the docs/06 "drive
  Link #2" plan complete. Recipe:

  1. First `J3DModel::calc` runs with Link's real `basicMtxCalc` —
     `mpNodeMtx` and `mpDrawMtxBuf` both reflect current-frame pose.
  2. `memcpy` mpNodeMtx into a 60-slot GameHeap ring buffer
     (42 × 48 B/slot = ~120 KB total). Replay slot
     `(write_idx - echo_delay) % 60` back into mpNodeMtx.
  3. Swap `basicMtxCalc` (shared J3DModelData + 0x24) to a no-op
     J3DMtxCalc (16-slot vtable of `blr` stubs) and run `calc` a
     SECOND time. Walker is stubbed (mpNodeMtx keeps our delayed
     overwrite) but calc's envelope/draw-matrix pass rebuilds
     `mpDrawMtxBuf` from the delayed `mpNodeMtx` → skin and rigid
     share the same delayed pose.
  4. Restore `basicMtxCalc`, restore j3dSys, `mDoExt_modelEntryDL`.

  **Rubber-band diagnostic was load-bearing.** Single-calc +
  post-calc mpNodeMtx overwrite rendered with rigid joints (hat,
  sword, hands, feet, head, belt-buckle) tracking the delayed pose
  but enveloped skin (torso, upper limbs) on current pose. The
  stretch between them pinned the cause: `mpDrawMtxBuf` was baked
  from current-pose mpNodeMtx inside the first calc and never re-read
  our overwrite. Fix was one extra `calc` call with a no-op walker.
  Addresses learned: `J3DModel + 0x8C = mpNodeMtx`, `J3DModel +
  0x94 = mpDrawMtxBuf`, `J3DJointTree + 0x18 = mJointNum` (accessed
  as J3DModelData + 0x28; Link = 42 joints). Side bumps: mod grew
  past `__OSArenaLo = 0x80411000` so the arena immediate was bumped
  to `0x80412000` and the mailbox moved to `0x80411F00`; Freighter's
  default `.eh_frame` + `.eh_frame_hdr` stripped via `-fno-exceptions
  -fno-unwind-tables -fno-asynchronous-unwind-tables` to buy 0x18C
  bytes of mod budget.

## 🔬 Next Session Priority

**v0.1.0 RELEASED (2026-04-21).** First public release. `ww.exe patch
<vanilla.iso>` produces a working multiplayer ISO from the user's own
legitimately-acquired Wind Waker disc; `ww.exe` runs the multiplayer
client. Auto-released on tag push via `.github/workflows/release.yml`.

**FIRST OVER-THE-INTERNET MULTIPLAYER GAME PLAYED (2026-04-21 night).**
Two physically separate machines, two real players, mutual Link
rendering, animations syncing — confirmed working end-to-end by the
project author against a friend over the public internet. Two UX
bugs surfaced in that session, captured below.

**LINK #2 HIDDEN BY DEFAULT (2026-04-21).** Standalone boot of the
patched ISO now looks vanilla — no Link #2 unless mplay2 is engaged AND
a remote has actually sent a pose. `shadow_mode = 0` is the explicit
kill switch / default; mode 5 slot 0 gates on pose-seq.

**SAVE-RELOAD SAFETY DONE (2026-04-21).** mplay2 now survives a save
reload from either Dolphin: D1 doesn't freeze, D2's view of D1 recovers
within a frame, and the inverse holds. Three collateral bugs (broadcast
panic on nil Link, stale puppet-sync pointer cache, missing shadow_mode
re-assertion) were caught + fixed in the same session — see Done entry.

**TWO-DOLPHIN MULTIPLAYER + MULTI-LINK N>1 REACHED (2026-04-20).** Two
real Dolphin instances running side-by-side, each rendering the other
player's Link as Link #2 at the remote's actual world coords — driven
by `scripts/mplay2.sh`. Multi-Link N>1 also unblocked the same day
(`mDoExt_J3DModel__create` flag `0x80000 → 0` so each instance gets
private material DLs instead of sharing one with every peer). Server
write race fixed in lockstep (`Player.SendMu`).

### v0.1.0 user-testing bugs (top priority for v0.1.1)

Both bugs surfaced the first time a non-author user tried v0.1.0 (well,
the author tried with a friend, same difference). Both have the same
fix: `ww.exe host` and `ww.exe join <ip>` subcommands that internally
orchestrate `server` + `broadcast-pose` + `puppet-sync` as goroutines
in one process per player, with `WW_SELF_NAME` wired automatically.

1. **TUI doesn't engage the rendering pipeline.** `ww.exe` (no args)
   launches the Bubble Tea TUI from `internal/tui/`. The TUI's "host"
   mode shows local Link position + a log + a connection state, but
   **does not** start the multiplayer rendering pipeline — no
   `server` listener that the joiner's `puppet-sync`/`broadcast-pose`
   protocol will reach, and no `shadow_mode = 5` write to the
   receiving Dolphin's mailbox. Result: a host running the TUI never
   sees the joiner's Link, the joiner never sees the host's Link,
   and neither side's TUI logs the connection (the TUI's network
   layer predates the pose-feed protocol entirely). The TUI is a
   leftover from the project's "shared map dot" era and was never
   updated when the rendering work landed.

   Workaround tonight: run `server` + `broadcast-pose` +
   `puppet-sync` in 3 terminals on the host and 2 terminals on the
   joiner. Worked on first try, confirmed real over-the-internet
   multiplayer.

   Real fix: `ww.exe host` (binds server, starts both broadcast-pose
   and puppet-sync goroutines, all pointing at localhost:25565) and
   `ww.exe join <ip>` (just broadcast-pose + puppet-sync goroutines
   pointing at the host's IP). README quick-start should point at
   these. TUI should either be rewritten on top of these subcommands
   or marked deprecated until rebuilt.

2. **Self-echo when running broadcast-pose + puppet-sync on the same
   machine.** `broadcast-pose Foo` and `puppet-sync Foo` against the
   same server are two separate TCP connections, both carrying the
   name "Foo" but tagged with different player IDs by the server.
   Server's `broadcastExcept` filters by ID, not name — so the
   `broadcast-pose` stream from "Foo" gets relayed to "Foo"'s own
   `puppet-sync`, which writes that pose into a remote-link slot and
   renders it as Link #2 next to the player's real Link. Visible
   symptom: an extra ghost Link mirroring your own movements,
   alongside the real remote player's Link.

   Workaround tonight: set `WW_SELF_NAME=Foo` env var on
   `puppet-sync`. The puppet-sync filter at `main.go:1117-1131`
   skips remotes whose name matches `WW_SELF_NAME`. Documented in
   `mplay2.sh` (which sets it per-Dolphin) but not surfaced anywhere
   user-visible.

   Real fix: same as #1. The new `host` / `join` subcommands know
   the player's name and set the self-filter automatically. Users
   never have to know WW_SELF_NAME exists.

### Pick next (any order)

1. ~~**`ww.exe host` and `ww.exe join <ip>` subcommands.**~~ SHIPPED
   in v0.1.1 (2026-04-22). See the Done entry above. Both v0.1.0
   user-testing bugs and item #8's graceful-shutdown loose end are
   closed in the same change.
2. ~~**Retire / rebuild the TUI.**~~ SHIPPED in v0.1.2 (2026-04-22,
   cheap path): deleted `internal/tui/` (and its Bubble Tea / Lip
   Gloss dep tree — `go mod tidy` now reports zero external
   dependencies, first time this repo has had that), and `ww.exe`
   (no args) now prints the help text. A successor TUI built on top
   of `host`/`join` (status panel, log tail, Ctrl+C-safe shutdown
   button) is tracked as a separate "polish" item if/when someone
   misses the interface.
3. **Visual differentiation.** Two Links look identical. Color tinting
   via TEV color override (every draw frame, post-mini-link entry).
   Easier than the actor-side work for KAMOME because we own the model.
4. **Anim-state sync (bandwidth).** Replace 2 KB raw matrix dumps with
   anim ID + frame counter (~16 B/tick). Requires REing Link's anim
   layer stack (bck/bca/bnk/bnn). Defer until LAN-only assumption breaks.
5. **Stage / room transitions.** Detect when remote crosses to another
   room and either despawn or freeze Link #2. Currently he just renders
   wherever the last received world coord was.
6. **Reconnect / lossy network.** Tonight the protocol assumes TCP
   reliable delivery; UDP with sequence numbers would let us drop
   stale poses without head-of-line blocking.
7. **N=1 mild flicker on Link #2.** Observed during this session's
   regression test as "every 2-4 seconds, very minimal" — same
   shared-J3DModelData state pollution noted previously. Cosmetic,
   not blocking; either a TEV bucket residue from cross-instance
   entry() ordering or a per-frame race against Link #1's own draw.
8. ~~**`puppet-sync` graceful-shutdown signal handler.**~~ SHIPPED
   in v0.1.1 (2026-04-22) as part of `ww.exe host/join`. The signal
   handler lives on the host/join goroutine orchestrator rather
   than inside `puppet-sync` proper, so mplay2.sh's Ctrl+C path
   still has the same limitation (use `./ww.exe host/join` instead,
   or `./ww.exe shadow-mode 0` after teardown). If someone keeps
   running `./ww.exe puppet-sync` standalone, adding a signal
   handler to the CLI wrapper is a 5-line follow-up.

### Echo-Link DONE (2026-04-19 late-late-late)

Track 1 of the original "Next Session Priority" — independent pose via
delayed mpNodeMtx replay + double-calc. `shadow_mode = 4` + `echo-delay 30`
produces a clean Link #2 animating 0.5 s behind Link #1 on Outset: no
stretch, no crash, sky clean, draw_progress 41.

The rubber-banding encountered mid-session (skin stretching between
delayed joint terminals and current-pose body) was the critical
diagnostic. Fix: double-calc — swap `basicMtxCalc` to a no-op between
our mpNodeMtx replay and a SECOND `J3DModel::calc`, so calc's
envelope/draw-matrix pass rebuilds `mpDrawMtxBuf` (+0x94) from our
delayed `mpNodeMtx` (+0x8C). Skin and rigid now share the same pose.

See `docs/05-known-issues.md` "Echo-Link DONE" for the recipe, address
table, and the mailbox/__OSArenaLo shift that had to come with it.

### Working modes (use via `./ww.exe shadow-mode <N>`)

- `0` — baseline mirror (userArea = Link #1). Cheap, default.
- `3` — freeze (no-op basicMtxCalc around calc). Link #2 holds his
  last pose. Useful kill-switch.
- `4` — echo-ring. `echo-delay 0` = identity sanity (same visual as
  mode 0). `echo-delay 1..59` = replay a past frame. Pose authoring
  proven; can drive Link #2 from any mpNodeMtx source.

### Next session — wire protocol for network pose

With authoring unlocked, the remaining block is getting real remote-
player pose data into `mpNodeMtx`. Two sub-problems:

1. **Host-side**: at broadcast time, extract sender's current joint
   matrices and put them on the wire. Simplest: read the sender's
   daPy_lk_c → J3DModel → mpNodeMtx each broadcast tick (50 ms).
   That's 42 × 48 B = 2016 B per packet. Over 20 Hz it's 40 KB/s —
   fine for LAN. Wire format: raw Mtx[42] blob behind an opcode.
   Faster path than animation-id+frame sync.

2. **Receiver-side**: replace the echo-ring's "capture from own
   calc" with "read from a fixed mailbox region that Go writes
   from the network". Same `mpNodeMtx` overwrite path; same
   double-calc to rebuild mpDrawMtxBuf. Drop the ring buffer —
   just one pose slot per remote player.

Concretely:

- Add `MAX_REMOTE_POSES` × 42 × 48 B region to the mailbox (or a
  separate runtime alloc from GameHeap if it crowds the mailbox).
- Extend `puppet-sync`: when a remote is driving Link-slot, write
  their joint matrices into the pose slot each tick.
- Network protocol: add a `PoseUpdate` opcode carrying `{player_id,
  joints_count, Mtx[]}`. 2 KB per update.
- Add `./ww.exe pose-test` that animates slot 0's pose from a local
  capture buffer as a smoke test without needing the server live.
- Wire Link #2's mpNodeMtx overwrite source to the slot instead of
  the echo ring.

The alternative (capture ANIM STATE — anmId/frame/transition — and
let the remote game run `J3DMtxCalcAnm` locally) is more principled
but requires RE-ing Link's anim-blend state (bck/bca/bnk/bnn layer
stack, IK, look-at). Save it for after raw-matrix sync is proven.

### Fall-back tracks (kept for reference, not blocking)

- **Custom J3DMtxCalc subclass**: write our own `recursiveCalc` that
  composes joint transforms from a `J3DTransformInfo[]` we own. Skip
  the second calc entirely. Cleaner architecturally; adds one more
  J3D grok before it pays off.
- **Anim-state sync via bck/bca IDs**: send animation ID + frame
  counter, let Link #2's own `J3DMtxCalcAnm` run locally. Bandwidth-
  efficient but requires replicating Link's anim layer stack.

### Working skinned-Link recipe (baseline)

`inject/src/multiplayer.c`: `PROBE_ARCNAME = "Link"`, `PROBE_BDL_IDX =
LINK_BDL_CL`, `modelEntryDL` with `calc()` wrapped in full 0x128-byte
j3dSys snapshot AND `mUserArea` set to Link #1's actor each frame.
`shadow_mode` mailbox byte (0x90) selects:
- 0: baseline (mirror) — userArea = this_, basicMtxCalc untouched
- 1/2: shadow userArea via in-heap copy of daPy_lk_c (kept as lab;
  didn't affect pose — see docs/05)
- 3: no-op J3DMtxCalc swapped into basicMtxCalc around calc → Link #2
  freezes at last pose while Link #1 keeps moving. **Decoupling
  proven.**
- 4: echo-ring (pose authoring). Captures `mpNodeMtx` each draw frame
  into a 60-slot GameHeap ring buffer, replays a slot `echo-delay`
  frames old, runs a SECOND `J3DModel::calc` with no-op basicMtxCalc
  to rebuild `mpDrawMtxBuf` from the delayed mpNodeMtx (skin and
  rigid now share the delayed pose — no rubber-band). **Independent
  pose authoring proven.**

Toggle/observe: `./ww.exe shadow-mode <N>` and `./ww.exe echo-delay <N>`
plus `./ww.exe dump` for diagnostics (joint_num, mpNodeMtx pointer,
ring_state, delay).

### Solved this session (2026-04-19 evening through late-late)

- `modelEntryDL` sky breakage root cause: shared J3DModelData, not
  the function itself.
- `calc()` crash for rigid: 2-field j3dSys save/restore (mModel +
  mCurrentMtxCalc) fully sufficient.
- `calc()` crash for skinned Link: NOT a j3dSys issue. 5-field and
  full 0x128 snapshot both still crashed. Real cause was joint
  callbacks reading `model->getUserArea()` and finding NULL. Set
  it to the live Link actor and the callbacks complete cleanly.
- Matrix propagation understood: `mBaseTransformMtx` @ +0x24 is input
  only; GX actually uploads from `mpDrawMtxBuf` @ +0x94, populated by
  `calc()` via `mpNodeMtx[]` @ +0x8C. No calc = invisible at origin.
- Bisect technique that pinned calc as the singular failure: stub
  `calc + modelEntryDL` separately, watch `draw_progress` markers.
  Cheap, decisive.
- **Pose source identified and decoupled.** `mBasicMtxCalc` at
  J3DModelData+0x24, shared between Link #1 and mini-Link via shared
  J3DModelData, drives the skeletal walk. Swap it to a no-op and Link
  #2 holds his last pose. `calcAnmMtx @ 0x802EE5D8` is the dispatch
  site (PC +0x64 = 0x802EE63C is where NULL basicMtxCalc triggered
  the exception that forced the fix). `JKRHeap::alloc @ 0x802B0434`
  used for the shadow buffer (kept around for future per-actor
  state).

---

**Original Option B plan (below) is still the direction — these are
sub-problems within it, not a pivot.**

Path B: network-replicated pose. A second Link that coexists with Link
#1 and is driven by the remote player's animation state over the wire.
Remote Link swings sword when the remote player swings. Not a
shared-state puppet, not a cosmetic draw-twice — a real independent
Link whose state comes from the network rather than from local input.

### Why Path B (and not Path A)

`daPy_lk_c` is written as a singleton. Its construction hijacks
`PLAYER_PTR_ARRAY[0]` (`phase_1` at `0x80125CC8` calls
`dComIfGp_setPlayer(0, this) / setLinkPlayer(this)`), and its runtime
writes pepper `dComIfGp_setPlayerStatus*(0, ...)` globals from dozens
of sites across `d_a_player_*.inc`. Making the class fully re-entrant
(Path A) means rewriting dozens of call sites inside 10k lines of
player code — weeks of work. Draw-hook / render-twice (old Angle 3)
is rejected outright: remote Link would mirror local Link's animation,
which is not real multiplayer.

Path B takes the pragmatic middle: run execute() for Link #2 with its
I/O redirected (global writes filtered, input zeroed) and feed anim
state from the network. Draw runs naturally, pose tracks the wire,
Link #1 is never corrupted.

### Path B plan

~~1. **Carve a 1 MB heap region for Link #2** by bumping `__OSArenaLo`
   to `0x80511000`.~~ **TRIED AND FAILED (2026-04-18).** Patch landed
   cleanly (boot log confirmed `Arena : 0x80511000 - 0x817f2120`), but
   ZeldaHeap shrank by exactly 1 MB (`HeapSize=004f98e0` ≈ 5.1 MB, was
   ~6.1 MB), which broke Outset archive loads: dozens of `Cannot
   allocate ... ZeldaHeap` OSReports, `デモデータ読み込みエラー！！`, and
   the game fell into a demo-fallback path instead of the title screen.
   **Empirical lesson: MEM1 has no spare megabyte. ZeldaHeap consumes
   essentially all of the arena.** Reverted to `__OSArenaLo = 0x80411000`.
   See docs/05-known-issues.md "ClearArena Wall" for the reverted patch.

The rest of the original plan (intercepting the alloc, patching phase_1,
filtering global writes, zeroing input for Link #2, applying network
anim state, extending wire protocol) still holds. But step 1 — where the
704 KB for Link #2's heap comes from — needs a completely different
strategy. Three candidates, none confirmed:

### Revised step 1 — where does Link #2's 704 KB live?

**Option A: Early-allocate from GameHeap at boot.** In `main01_init`
call `mDoExt_createSolidHeap(0xB0000, mDoExt_getGameHeap(), 0x20)` once,
hold the heap pointer, hand it to the `fopAcM_entrySolidHeap` hook when
Link #2 constructs. GameHeap shrinks by 704 KB for everyone else.
**Risk — probably fatal on Outset:** docs/05 OSReport shows Outset's
GameHeap has ~245 KB free after normal actor loading today; taking
another 704 KB means Outset's own stage actors OOM by ~470 KB. Could
still work on lighter stages (Link's house interior, Grandma's house,
Windfall shop) where normal actor load is smaller.

**Option B: Custom "mini-Link" proc that shares Link's model data.**
Write a new actor class (not `daPy_lk_c`) that loads only `LINK_BDL_CL`
from the shared object archive and the minimum anim machinery — no
aura, hands, hair, sword grips, bombs, arrows, masks, etc. Estimated
footprint under 100 KB instead of 704 KB, easily fits in GameHeap.
Sidesteps the singleton problem (we write the class, it never calls
`dComIfGp_setPlayer(0, this)` and never writes slot-0 status). **Cost:**
we reimplement just enough of the Link render path (J3DModel, anim
frame ctrl, matrix calc) to drive Link's rig from the network. Real
work, probably multiple sessions, but bounded.

**Option C: Grow GameHeap by stealing from CommandHeap or ArchiveHeap.**
`m_Do_machine.cpp:466` computes `sysHeap = arenaSize - CommandHeapSize
- ArchiveHeapSize - GameHeapSize`. Those three sizes are constants in
the DOL. If CommandHeap or ArchiveHeap have slack, bump GameHeapSize at
their expense — binary patch the constant. Need to measure slack before
committing. ZeldaHeap has zero slack (proven above), but command/
archive are untested.

### Recommendation → committed: Option B

Option C was attempted (heap-slack measurement via `JKRExpHeap::do_getFreeSize`
calls sampled once per second). Even after switching from the virtual-dispatch
wrapper `JKRHeap::getFreeSize @ 0x802B0868` (which produced repeated Dolphin
MMU invalid-read warnings at PC `0x802b0874` — likely C++ this-adjust / MI
vtable-layout mismatch against raw C function-pointer calls) to the concrete
`JKRExpHeap::do_getFreeSize @ 0x802B22C4`, the game froze once in-game.
Option C abandoned. Committed to Option B.

### Option B — mini-Link rendering path (research complete 2026-04-18)

Plan: render a second Link from OUR injection code, outside the actor
system. Reuses Link #1's already-loaded `J3DModelData` (shared via the
object archive), creates a separate `J3DModel` instance with its own
base matrix + anim state, submits it each frame via `mDoExt_modelEntryDL`.
No PROC_PLAYER spawn, no phase_1 singleton hijack, no 704 KB heap.

### Key addresses (GZLE01)

**Resource lookup:**
- `dRes_control_c::getRes(arcName, index, info[], count) @ 0x8006F208` — static, 4 args
- `&g_dComIfG_gameInfo.mResControl.mObjectInfo[0] = 0x803E0BC8` (`g_dComIfG_gameInfo @ 0x803C4C08` + `mResControl` at +0x1BFC0 + `mObjectInfo` at +0x0)
- `ARRAY_SIZE(mObjectInfo) = 64`
- Link's archive name: string literal `"Link"`
- `LINK_BDL_CL = 0x18` (main body model file index)

**Model creation + draw:**
- `mDoExt_J3DModel__create(J3DModelData*, u32 modelFlag, u32 dlistFlag) @ 0x80016BB8`
- `mDoExt_modelEntryDL(J3DModel*) @ 0x8000F974` — queues to the draw phase, safe to call from our execute-phase hook
- `J3DModel::setBaseTRMtx(Mtx)` — sets the base transform; base matrix lives at J3DModel struct offset 0x24

**Matrix building** (GX standard):
- `PSMTXTrans`, `PSMTXRotRad` from `dolphin/mtx/` — build translation + Y-axis yaw

### First prototype scope

1. Once at start (after Link #1 exists so his archive is resident): call
   `getRes("Link", 0x18, (dRes_info_c*)0x803E0BC8, 64)` → J3DModelData*.
2. Create J3DModel via `mDoExt_J3DModel__create(modelData, 0x80000, 0)`. Hold
   the pointer.
3. Each frame: build a translation matrix at `link_pos + (100, 0, 0)` (offset
   so we can see it), `setBaseTRMtx(model, mtx)`, `modelEntryDL(model)`.
4. Expected outcome: a **T-posed Link** renders next to the real Link, same
   animation/none, just proving the render pipeline. T-pose because we're
   not yet driving anim state — that's step 2 of Option B.

### Unknowns remaining

- **When is Link's archive actually available?** Link #1's `daPy_createHeap`
  does the loads via `dComIfG_getObjectRes(l_arcName, ...)`. After Link #1's
  phase_2 completes, archive is resident. Gate our `getRes` call behind
  `PLAYER_PTR_ARRAY[0] != NULL` and a few seconds more to be safe.
- **Where does our new J3DModel's heap allocation go?** `mDoExt_J3DModel__create`
  allocates from the CURRENT heap (whatever that is at call time). We'd want
  to control this — allocating from ZeldaHeap (where Link #1's J3DModel also
  lives) would be natural. May need to `becomeCurrentHeap` before the call.
- **Per-joint matrix buffers.** J3DModel with skinning needs bone matrices.
  Creating a plain J3DModel might not be enough — Link has 0x2A joints and
  per-joint matrices. If `mDoExt_J3DModel__create` doesn't allocate these
  automatically, we need to track down the allocator.
- **Shared vs separate animation.** If we share `mpCLModelData`'s anim-callbacks
  with Link #1's model, their animations will clobber each other. Need to
  verify whether the per-instance J3DModel keeps its own bone state.

### Known unknowns we'll hit along the way

- `daPy_Execute` references 100+ fields in the 10k-line `daPy_lk_c`.
  "Filter globals / zero input" is probably more than one filter — we
  may need to handle HUD-icon state (`setAStatus` / `setDoStatus` /
  `setRStatus`), metronome state, and event-flag I/O.
- Camera ID: `mCameraInfoIdx = dComIfGp_getPlayerCameraID(0)` in
  execute() and playerInit(). Link #2 inherits Link #1's camera idx.
  Probably fine since Link #2's draw uses the view we're already
  rendering; worth checking once we see Link #2 alive.
- `daPy_getPlayerActorClass() == this` gates (6 sites in main).
  Flips for Link #2. Pay attention to what the guarded code does;
  some may be intentional "only the primary Link runs this."
- Once Link #2 renders, input from the Pad singleton may still reach
  Link #2 in ways we didn't anticipate — if so, patch the input
  read-site(s) per-instance.

### Rejected approaches (for the record)

- **Angle 1 naive ("just fix the 704 KB heap").** Heap fix alone
  isn't enough; the class hijacks singleton state at construction
  and corrupts it every frame in execute. Angle 1 is *subsumed*
  by Path B (steps 1-2), it just isn't sufficient on its own.
- **Angle 2 (full resource-sharing refactor of `daPy_lk_c`).**
  Intractable in any reasonable time budget; the class is 10k
  lines of tangled state.
- **Angle 3 (render Link's model twice).** Shared animation state
  makes remote Link mirror local Link. Fails the project goal of
  "each remote player has independent animations."

### History: 2026-04-18 diagnosis

The "PROC_PLAYER crashes after ~23s" behavior is **not** a singleton
`JUT_ASSERT`. Dolphin's OSReport log during the crash:

```
Error: Cannot allocate memory 721040 (0xb0090) byte ... from 81523910
FreeSize=0003d770 TotalFreeSize=0003de00 HeapType=EXPH HeapSize=002ce770 GameHeap
見積もりヒープが確保できませんでした。
```

Link #2 wants ~704 KB (`fopAcM_entrySolidHeap(this, daPy_createHeap, 0xB0000)`
at `d_a_player_main.cpp:12173`); GameHeap only has ~245 KB free on
Outset. 23-second delay = heap fragmentation tipping it over.
`OSPanic` / `PPCHalt` is the downstream effect of a null alloc, not
an invariant-assertion failure.

### Known-good baseline at start of this plan

- `fopAcM_create(...)` queues spawns; `fpcM_Management` constructs
  next frame.
- Per-frame hook + callback shim stable (docs/05).
- Multi-puppet architecture: 4 slots, mailbox, per-slot sync for
  KAMOME / TSUBO / NPC_OB1 (Rose).
- `inject/src/multiplayer.c` current code spawns non-Link procs
  per slot. It'll stay in place while we build Link #2; eventually
  slot 0 will switch to PROC_PLAYER.

## Hook + shim recipe (current working baseline)

Documented in `docs/05-known-issues.md` → "Per-Frame Hook — SOLVED". The
shim at `0x80023204` + callback pointer at `0x80410F08` (inside the
mailbox at `0x80410F00`) + `main01_init` hook at `0x80006338` is the
stable foundation for anything per-frame. A third `hook_branchlink` was
added 2026-04-19 at `0x80108210` inside `daPy_Draw` for running code in
Link's draw phase — see `docs/05-known-issues.md` → "Mini-Link render
pipeline" for the shape.
- [ ] Wire up network → actor position pipeline (Player A's pos → server → Player B's mailbox → Player B's Link #2 renders)
- [ ] Add animation state sync (`mCurProc` at actor + `0x31D8`)
- [ ] Add rotation sync (`shape_angle` at `0x20C`)
- [ ] Color-differentiate Player 2 (modify TEV palette data)
- [ ] Handle room/stage transitions (despawn/respawn Player 2 when players change rooms)
- [ ] Implement presence indicator (show "Player 2 is on Outset Island" when out of view)
- [ ] Handle Player 2 disconnection gracefully

## 🏗️ Build Pipeline

The full loop, current as of this session:

1. CISO source: `Dolphin-x64/Roms/Legend of Zelda, The - The Wind Waker (USA, Canada).ciso`
2. Freighter project at `inject/` produces `patched.dol` via `python build.py`
   - Inject address: `0x80410000`
   - Hooks:
     - `main01 @ 0x80006338` → `main01_init` (one-shot; publishes callback pointer)
     - `fapGm_Execute bl @ 0x80023204` → `frame_shim` → `multiplayer_update` (per-frame execute)
     - `daPy_Draw bl @ 0x80108210` → `daPy_draw_hook` → `daPy_lk_c::draw` then mini-Link submit (per-frame draw)
   - Post-build patches: OSInit immediates (four writes) + revert five Freighter clobbers
3. `wit copy <ciso> <iso> --iso --trunc --overwrite` decompresses to plain ISO
4. Python snippet writes `patched.dol` at the ISO's DOL offset and shifts the FST past the DOL end (we use a 0x1000-aligned new FST offset)
5. Update the ISO header's FST offset field (at disc offset `0x424`)
6. Delete `%APPDATA%/Dolphin Emulator/Cache/gamelist.cache` (if present) and restart Dolphin
7. Boot the patched ISO — **no Gecko codes / Dolphin patches enabled** (they fight the DOL)
8. `./ww.exe dump` to verify: mailbox counter at `0x80410F00` increments, T2 code at `0x80410000` is intact, main01 hook at `0x80006338` shows `0x484XXXXX` (a `bl`)

## 🚀 Polish

- [ ] Better TUI dashboard: show remote players' positions on a mini-map
- [ ] Chat system (already have protocol support, need UI)
- [ ] Audio notifications when players join/leave
- [ ] Configurable port / multiplayer settings
- [ ] Installer / distribution: bundle Go binary + DOL patcher tool

## 🤔 Long-Term Questions

- How do we handle save files? Two players have separate saves with their own progress.
- What about combat? If one player attacks an enemy, does the other see it die?
- Puzzle rooms: one player solves a puzzle, does the door open for both?
- This starts to sound like a genuine co-op mod, not just a multiplayer viewer. Scope carefully.

## Known Untested

- Multiple clients connecting at once (server was tested with 2 fake clients, not stress-tested)
- IPv6 support
- Network reliability over the internet (tested on LAN only)
- Firewall / NAT traversal for non-LAN play
