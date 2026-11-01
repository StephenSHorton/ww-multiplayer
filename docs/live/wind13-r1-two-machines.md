# wind#13: R1, the app path on two machines (2026-11-01)

Main 5458f9d (BlobHash 0x04765DD0, REL 0xA9DE0F1E) on both sides, plus the macOS fix below on the Mac (a671b7e, Go only). No co-op world (Off). Enemies on by default: kinds `mo2,bk+u,cc+u,ki+u,am2+u,tn+u`, remote bodies step6.

- **PC host** (Lead-PC): Windows desktop 10.0.0.227, `ww-multiplayer.exe` built `-tags desktop,production`, the real app window (its controls driven through Windows UI Automation), isolated app config, Dolphin 2609a on a scratch copy of fixture user dir 4, disc freshly patched by this build. The app's Start booted Dolphin to the title; start.sav (md5 5504e1e8) loaded with F1. The exe's Private-profile firewall rule already existed, so no prompt.
- **Mac joiner** (harbor): M5 Max, Dolphin 2603-342 (the prepared copy), `WW Multiplayer.app` from `scripts/build-mac.sh` on the same main, a fresh rig user dir (`mac-rig.sh setup`), the shared WW-mp.iso. There is no desktop control on that Mac, so the Mac app ran as `ww-multiplayer ui --browser` and was driven through its bridge (`/api/call/Join`, `SetReady`; the same Bridge methods the window's buttons call); the Wails shell itself was not clicked. The app's config was written by hand (Setup not run). The Mac start save (start-mac-A9DE0F1E.sav) was loaded into state slot 1 with a held F1 CGEvent.
- Roles: the PC hosts, since the Mac sits behind the Google Wifi double NAT and the PC can't reach it.

## Bug found: the Mac app attached to another player's Dolphin

On the first START the Mac app did not launch a Dolphin. Its log said `Playing on the Dolphin that is running (pid 60998): a Dolphin is already running on your user folder.`, but 60998 was another agent's rig Dolphin (`-u .../rigs/wind47/u1`). The session armed puppet-sync, pose broadcast and the enemy client on it for 23 s (no enemy mirroring: the host was not in play yet) before Leave.

Cause: on macOS `launch.Processes` never read a Dolphin's command line (`proc_other.go`, `ArgsKnown` false), and `session.DecideLaunch` counts a Dolphin whose folder is unknown as possibly this player's. So on any Mac with a patched Wind Waker Dolphin already open (two players on one Mac, a rig) the app plays on that Dolphin instead of starting its own.

Fix (a671b7e): `proc_darwin.go` reads each Dolphin's program and arguments with `sysctl kern.procargs2` (`TestParseProcArgs2`). Checked against the 8 rig Dolphins then running: each reported its own `-u` folder. With it the Mac app launched its own Dolphin (below).

## The run (after the fix)

Mac app log (session `20261101T185142Z-join.log`):

```
18:51:42 Joining the lobby at 10.0.0.227:25565 as "mac".   (lobby: PC host + mac, link up, via app; mac Ready)
18:51:44 Starting Dolphin with your patched disc.
18:51:44 Dolphin started (pid 32397).
         (args: -u <rig u1> -e WW-mp.iso -C Dolphin.Core.RAMOverrideEnable=True -C Dolphin.Core.MEM1Size=0x03000000)
18:51:46 enemy client: mirroring the host's enemies (kinds mo2,bk+u,cc+u,ki+u,am2+u,tn+u), remote bodies step6; ...
18:51:46 enemy client: game GZLE01, WWMB header ABI 1 layout 3, code hash 04765DD0 (matches this version), strict mode
18:51:59 link slot 0 := player 3 (PC)
18:52:02 enemy client: mirroring: the host lists 6 enemies in Hyrule room 0
18:52:03 ... adopted 1111 slots [cc x4] ... canaries all 0
18:53:01 player 3 (PC) is between stages: hidden until it is back here (Hyrule)
18:53:08 enemy client: not mirroring: the host is in M_Dai room 1, this game in Hyrule room 0
18:59:11 player 3 (PC) is in this stage (M_Dai): shown
18:59:16 enemy client: mirroring: the host lists 3 enemies in M_Dai room 1
18:59:25 ... adopted 0111 slots [mo2 0][mo2 1][mo2 2] ... WatchdogReleases:1 (the stage change), others 0
18:59:39 enemy client: ACK setID 0x0 handle 7 hit 1 applied_at/none frames 1 sets 1 health 8
19:00:27 Left the game
```

PC host app log (Lead-PC, wind#13 comments): `enemy host: host-authoritative enemies on (kinds mo2,bk+u,cc+u,ki+u,am2+u,tn+u), remote Link bodies step6`; `REL module linked (build A9DE0F1E); host hits apply`; `player 6 (mac) is in this stage (M_Dai): shown` after hiding it while the Mac was in Hyrule. The Mac's sword hits as PACKs: `PACK client 3 seq 1 applied/none life 80 -4`, then seq 3, 4, 5 (`life 75`, `71`, `67`, each -4). Body slot 0 `s2 ... h5 l2 t1` (Moblins latched on the Mac's body), swaps 0 -> 1704, 5 PHITs sent to mac by 18:59:49Z; the Mac's Link lost most of a heart (screenshot). Ack within 1-2 seq at about 61-63 per second on both sides.

Screens: the Mac drew the PC's purple Link with its "PC" name tag in Hyrule; the PC drew "mac" tagged on the M_Dai stairs.

Sweep: no ERR, `bug:` or panic line in either app's session log. Neither app-launched Dolphin writes OSReport/MI lines (the app never sets the dev log), so ISI/DSI is judged from both games running on without a fault screen to the end, not from a log grep.

## Not covered

- The Mac app's Wails window itself (no desktop control on that Mac); its first-run Setup.
- Bokoblins and Keese (none in M_Dai room 1 or the Hyrule start); a host-side kill; a direct join without a lobby on two machines.
