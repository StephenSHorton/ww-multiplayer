# The macOS test rig

How agents (and people) run the patched game on Dolphin on a Mac: one Dolphin, a two-Dolphin
`mp-local` loopback, screenshots, input and save states, with many rigs side by side on one Mac.
Brought up on 2026-11-01 (wind#3) on an M5 Max (18 cores, 128 GB), macOS 26.6, Dolphin
2603-342, main c1278b3 (BlobHash 0x04765DD0, REL 0x1CE30685).

`scripts/mac-rig.sh` wraps everything below; `scripts/mac-rig.sh` with no arguments lists its commands.

## One-time setup (per Mac)

1. Dolphin in `/Applications`, then `scripts/setup-mac-dolphin.sh`: an unhardened copy at
   `~/Applications/Dolphin.app` (bundle id `org.dolphin-emu.dolphin-unhardened`), which
   `task_for_pid` may attach to. Re-run it after every Dolphin update.
2. Build the tools and give them the debugger entitlement, or memory attach fails:
   ```bash
   go build -o ~/ww-shared/bin/wwdev ./cmd/wwdev
   go build -o ~/ww-shared/bin/ww-multiplayer ./cmd/ww-multiplayer
   codesign --force --sign - --entitlements scripts/cs-debugger.entitlements ~/ww-shared/bin/wwdev
   codesign --force --sign - --entitlements scripts/cs-debugger.entitlements ~/ww-shared/bin/ww-multiplayer
   ```
3. Patch the vanilla disc (`ww-multiplayer patch ww-vanilla.ciso ~/ww-shared/disc/WW-mp.iso`).
   The output is byte-identical to the PC's patch of the same disc (checked 2026-11-01: sha256
   `5fa98b9a…9267` on both), so a patch mismatch is never the cause of a Mac-only bug.
4. Permissions for the app that runs the agent's shell (Terminal, iTerm, the agent host), in
   System Settings > Privacy & Security: **Screen Recording** (screenshots and window titles) and
   **Accessibility** (synthetic keys). On the rig Mac both were already granted. The macOS
   firewall is off there (`/usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate`), so
   no dialog appears; tests still listen on 127.0.0.1 only.

Shared layout (one copy for every agent; nothing in it is ever committed or uploaded):

```
~/ww-shared/disc/ww-vanilla.ciso, WW-mp.iso      the disc and the patched disc
~/ww-shared/fixtures/                             dolphin-user-3/-4 (PC user dirs), saves/ (GCIs),
                                                  start-mac-1CE30685.sav (the Mac start save, below)
~/ww-shared/bin/                                  wwdev, ww-multiplayer (entitled), mac-rig.sh
~/ww-shared/rigs/<rig>/u1, u2                     each rig's own Dolphin user dirs
```

## Two things that differ from the PC

- **The 48 MB MEM1 override is required, and so is `-b`.** Without it the patched disc faults in
  `JKRHeap::alloc` before the title ("Invalid read from 0x00000069, PC = 0x802b04b4"; the boot
  log says `Memory 24 MB`) and the mailbox heartbeat stays at 1. `wwdev dolphin2`,
  `auto-recapture` and `mac-rig.sh` pass it; a hand launch must too:
  ```bash
  open -n -a ~/Applications/Dolphin.app --args -u <userdir> -b -e ~/ww-shared/disc/WW-mp.iso \
    -C Dolphin.Core.RAMOverrideEnable=True -C Dolphin.Core.MEM1Size=0x03000000 \
    -C Dolphin.Interface.UsePanicHandlers=False -C Dolphin.Interface.ConfirmStop=False \
    [--save_state=<file.sav>]
  ```
  Always `open -n -a` on the bundle, never `Contents/MacOS/Dolphin` directly (docs/05: the direct
  exec ends up in the hardened `/Applications` binary and attach is refused). The Dolphin binary
  is `~/Applications/Dolphin.app/Contents/MacOS/Dolphin` (`DOLPHIN_EXE`).
- **A save state from the PC's Dolphin (2609a) does not load in Mac Dolphin 2603.** It is
  silently ignored and the game sits on the title screen. Macs use their own start save, made
  unattended by the macOS `wwdev auto-recapture` (`scripts/mac-rig.sh recapture <rig>`): cold
  boot, menus through the pad override, then a held Shift+F1. The shared one is
  `~/ww-shared/fixtures/start-mac-1CE30685.sav` (file 1 of the fixture card, in Hyrule, Link in
  play). A REL/C change invalidates it: recapture under the `ww/rel-blob` lease and replace it.

## Daily use

```bash
scripts/mac-rig.sh setup <rig>        # u1, u2 from the fixtures; hotkeys on the keyboard
# take the Irori lease ww/dolphin-slot-<n> first
scripts/mac-rig.sh pair <rig>         # two Dolphins (u1, u2) from the Mac start save; PIDs in <rig>/pair.pids
scripts/mac-rig.sh mp-local <rig>     # wwdev mp-local with MP_LOCAL_PIDS pinned (run in background;
                                      #   MP_LOCAL_STOP_FILE=<path> stops it cleanly)
scripts/mac-rig.sh shot <pid> a.png   # screenshot one Dolphin's render window
scripts/mac-rig.sh fps <pid> <pid>    # 30 = full speed
scripts/mac-rig.sh stop <rig>         # end this rig's Dolphins by PID; then release the lease
```

- **Give each rig its own `MP_LOCAL_PORT`** (default 25565, which a second rig's mp-local then
  fails on: "bind: address already in use"). `MP_LOCAL_PORT=0` takes a free port on 127.0.0.1 and
  logs it ("Server listening on localhost:<port>"); pass that address to `pose-fake-loop`.
- **Don't use bare `wwdev dolphin2` on the shared Mac**: it adopts any running Dolphin as Dolphin 1, which is another agent's. `mac-rig.sh pair` launches both itself and finds each by its own `-u` argument.
- **Pass `--coop-iso ~/ww-shared/disc/WW-mp.iso`** to `mp-local --coop` and every `join --coop`: without it co-op takes its stage tables from "the disc last patched", one setting for the whole Mac user that any agent's `patch` moves (once it named another agent's deleted scratch ISO, and the session ran with room puzzles, memory switches, keys and kills off; `docs/live/wind9-c3-mac.md`).
- **Always pin `MP_LOCAL_PIDS`** (the script does): other agents' Dolphins run on the same Mac and
  enumeration order would otherwise pick theirs. For one Dolphin use `WW_DOLPHIN_PID=<pid>`.
- **Co-op on the rig** (`mac-rig.sh mp-local <rig> --coop=new ...`): the script exports the rig's
  `USER_DIR_1/2` (`u1`, `u2`), so co-op backs up, reads and binds those cards, never the default
  Dolphin folders. It also adds `--coop-iso ~/ww-shared/disc/WW-mp.iso` unless you name one: without
  it the stage tables come from the disc `patch` last wrote, a per-user record that any agent's
  `patch` run moves. And it adds `--yes`, since nobody is at a TTY to answer the bind prompt and the
  rig's cards are copies of the fixtures. Flags after a `--` are passed through unchanged.
- **Input**: `wwdev input` / `input-release` work unchanged (they write the mailbox; no OS
  permission needed). Lock-on/guard still need the trigger poke (WW-MAC-RULES).
- **Screenshots**: `wwdev screenshot` is Win32-only. `mac-rig.sh shot` finds the PID's render
  window (title contains ` | `) with CGWindowList and runs `screencapture -l`. Windows may
  overlap on screen; `-l` captures the window itself, not what covers it.
- **Hotkeys**: Dolphin's Quartz keyboard polls the HID key state once a frame, so a synthetic key
  must be held (the macOS auto-recapture holds Shift+F1 for 300 ms via CGEvents). The user
  dir's `Config/Hotkeys.ini` must bind the keyboard (`Device = Quartz/0/Keyboard & Mouse`,
  `Save State/Save State Slot 1 = @(Shift+F1)`); the PC fixtures bind a Steam Controller, which
  ignores synthetic keys. `mac-rig.sh setup` rewrites it.
- **Killing**: only by recorded PID (`mac-rig.sh stop`, `kill <pid>`), never `pkill Dolphin` or
  a command-line match. The macOS auto-recapture never kills other Dolphins.
- **Crash sweep**: `grep -E 'ISI|DSI|Invalid (read|write)' <rig>/u*/Logs/dolphin.log` after
  each live session (WW_DOLPHIN_DEVLOG writes them; "Dolphin OS $Revision" lines match a bare
  `DSI` grep and are harmless).

## Capacity: leases ww/dolphin-slot-1..4

Measured 2026-11-01 with every Dolphin in game from the Mac start save: 2, 4, 6 and 8 Dolphins
all held 30 game frames per second (mailbox heartbeat over 5 s) while other agents were building
(load average 56 at 8). The two Dolphins of a pair, mp-local and enemy sync included, take one
slot. **N = 4**: `ww/dolphin-slot-1` .. `ww/dolphin-slot-4`. A three- or four-Dolphin test takes
two slots. If `fps` shows under 28 on a live run, say so on the card before trusting timing
results.

## Verified on the rig (wind#3, 2026-11-01)

- One Dolphin: cold boot at 48 MB, heartbeat climbing, `blob-verify-live` code sections match.
- macOS auto-recapture: boot, menus and Shift+F1 with nobody at the keyboard.
- Two-Dolphin `mp-local` loopback (R12): both "in game", "Local multiplayer running."; Link A
  walked by `wwdev input` shows up in Dolphin B with its name tag; the enemy client adopted the
  host's four ChuChus (`adopted 1111`, canaries 0); no ISI/DSI or invalid access in either log.
