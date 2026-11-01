# wind#20 block R7: enemy-host reconnect, CLI loopback, macOS (2026-11-01)

**Build and rig.**
- Build: as `LIVE-wind20-r5.md` (main c1278b3, REL 0x1CE30685).
- Games: A = 75194, later 83551 after the R7b restart; B = 75204. Both on the healer, both in `M_NewD2 0 0` (two Bokoblins, `adopted 0011`).
- Session: CLI `host HostA --port 25640` on A and `join 127.0.0.1:25640 JoinB` on B, no `WW_*` var.
- Suspends are `kill -STOP` / `-CONT` on the recorded PID (`rig/stop.sh`). The Dolphin stop is `kill` on A's recorded PID.

| Item | Result | Evidence (`evidence/r7-host.log`, `r7-join.log`, `r7b-host.log`) |
|---|---|---|
| **R7a, the joiner suspended 25 s** | **PASS** | **During.** The host's server dropped the silent JoinB at the 15 s read timeout (`JoinB (enemy client 2) disconnected`, `JoinB left the game`). The host's puppet froze and despawned B's slot. The server and the host never ended.<br>**On resume.** B reconnected by itself (players 5 and 6, enemy client 3), A re-armed the slot (`link slot 0 := player 5 (JoinB)`), and B drew HostA again (`link slot 0 resumed: player 1 pose fresh again`) and re-adopted both copies (`adopted 0011`).<br>**No race.** The old connection was already closed before the resume, so no stale connection raced the new one. The `replaced by a new connection under the same name` line belongs to a dedicated `server` with a suspended app host, which this rig doesn't have. |
| **R7a, the host suspended 25 s** (the CLI host holds the server) | **PASS** | **During.** B logged `the host's STATE is stale (a stall?); holding the copies frozen (up to 2.5s)`, then `not mirroring: the host's STATE is stale`, and despawned HostA's slot. The pose drop is expected, since the server was frozen with the host.<br>**On resume.** Everything reconnected within a second (01:24:01): `HostA (enemy host 5)`, `JoinB (enemy client 4)`, poses both ways (`link slot 0 := player 7 (JoinB)` on A; `:= player 10 (HostA)` on B). B logged `mirroring: the host lists 2 enemies in M_NewD2 room 0` and `adopted 0011`. Neither process ended. |
| **R7b, the host's Dolphin stopped and restarted** (CLI: the host restarted too, as TESTING says) | **PASS** | **After A's Dolphin stopped (01:24:45).** In 70 s the host wrote 6 lines in all:<br>- one `pose feed paused: the game cannot be read`;<br>- `enemy host paused: read failed at 0x803CA754 ...` three times, once per macOS kern_return the dying process gave (kr=1, 4, 268435459);<br>- two `enemy host still paused: ~1875 more in the last 30s`.<br>No per-tick flood (LIVE-6 b2's 8,193 lines in 137 s are gone). B froze, then released its copies, and despawned HostA.<br>**Restart.** A's Dolphin was relaunched on the same user dir (`mac-rig.sh one`, pid 83551, REL this build) and hopped to `M_NewD2 0 0`. The CLI host was stopped and restarted on it.<br>**On restart.** The joiner, never restarted, logged `server restarted (epoch changed): full puppet reset` and reconnected. It drew HostA (`link slot 0 := player 1 (HostA)`), logged `the host's epoch went 59559 -> 59821 (the host restarted); the smoothers re-base`, and adopted both Bokoblins again. A drew JoinB (`link slot 0 := player 4 (JoinB)`). |

**Observation (no bug claimed).** The R7b "paused" line came three times, not once. `Degradable` warns once per reason, and the reason text carries the kern_return, which changed as macOS tore the task down (1 KERN_INVALID_ADDRESS, 4 KERN_INVALID_ARGUMENT, 0x10000003 MACH_SEND_INVALID_DEST). It is bounded either way.

**Sweep.** All host/join stderr files are empty, with 0 `panic|fatal|exception|bug:` lines. Both Dolphin logs have 0 `ISI|DSI|Invalid read|Invalid write|xception|OSReboot`. `crash-context` on 83551 and 75204: `No crash context`.

**Not run.** The app variant of R7b: the app's GameRunner re-attaching without a host restart. It needs the app window.

**Verdict.** R7a and R7b pass on the CLI path, on macOS.
