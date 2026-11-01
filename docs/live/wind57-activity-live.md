# wind#57: the Activity tab, live on the Mac rig (2026-11-01, walnut)

Build: mac/wind57-activity on main cf165a5 (no wire or REL change). Rig `wind57`, lease ww/dolphin-slot-3, both Dolphins from the
Mac start save (`start-mac-A9DE0F1E.sav`, 28-Hyrule3). `mac-rig.sh mp-local wind57 --coop=new` (world 7f5777ea283a0866; both files
bound with the auto yes), then restarted with `--coop=7f5777ea283a0866` after the self-echo fix. mp-local's stdin was a file, so `/status`
could be read; its new `activity:` line is the Activity tab's newest entry (`wind57-activity-live.txt`).

The start save already holds every shared item, song, pearl and Triforce shard, so no inventory gain was possible; the chests were set
by poke on the game's live memBit (`0x803C5380`, Hyrule's tbox word), which the replica reads as an opened chest.

| Step | A (`PlayerA`) | B (`PlayerB`) |
|---|---|---|
| B's chest bit 2 (first run) | `PlayerB opened a chest in Hyrule Castle` | `a partner opened a chest ...`: the hub echoes every GAINS to its origin (hub.relay toOrigin), so B saw its own gain under an unknown origin. Fixed: an origin that is no partner and brings only bytes this replica sent is itself (`activityRing.isSelf`, `TestActivitySelf`) |
| B's chest bit 3 (after the fix) | `PlayerB opened a chest in Hyrule Castle` | `You opened a chest in Hyrule Castle` |
| A's chest bit 4 | `You opened a chest in Hyrule Castle` | `PlayerA opened a chest in Hyrule Castle` (and `co-op: set t09 Hyrule chest 4`) |
| B's switch bit (t09 switch word) | no entry | no entry |
| B `stage-hop M_NewD2 0 0` | `PlayerB entered Dragon Roost Cavern` | (no entry for its own move) |

Each entry was in `/status` within the 4 s after the poke, in the same relay as the `set t09 ...` write.

Not run here: a real item pickup or chest opened by input, and the app window in a live session (the window was checked with
uipreview's `?s=ingame&click=tab-activity` and `?s=ingame-ended` and `TestFrontendDOM`). No ISI, DSI or invalid access in either
Dolphin's log; no panic or ERR_KEY in mp-local's.
