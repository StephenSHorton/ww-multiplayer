# wind#20 block R8: mixed builds on macOS, the CLI half (2026-11-01)

**Build under test.** `~/ww-shared/bin/ww-multiplayer`, built from main c1278b3 (reports `dev`, network protocol 6).

**Old build.** v0.5.0's release asset `ww-multiplayer-macos.zip`, fetched with `gh release download`. Its sha256 127f34cb…6631d matches the release's SHA256SUMS. It was unpacked to scratch with `ditto -x -k`, and the quarantine attribute was removed on the scratch copy only. `version` prints `ww-multiplayer v0.5.0`.

**Rig.** One Mac, no Dolphin. Every process was on 127.0.0.1 with `WW_NO_UPDATE_CHECK=1`. The Mac's application firewall is off.

| Check | Result | Evidence |
|---|---|---|
| Old client → new server | **PASS** | v0.5.0 `fake-client OldFake 127.0.0.1:25612` against this build's `server`. The client printed `ERROR: host refused the connection: this host runs a newer ww-multiplayer (network protocol 6, yours is 3); please upgrade to the host's version`. The server logged `refused "OldFake": ...` with the same text. Files: `evidence/r8-old-to-new.log`, `evidence/r8-new-server2.log` (owner key redacted). |
| New client → old server | **PASS** | This build's `fake-client NewFake 127.0.0.1:25611` against v0.5.0's `server`. The client printed `ERROR: host refused the connection: this host runs an older ww-multiplayer (network protocol 3, yours is 6); both players need the same version`. The old server logged `refused "NewFake"` with the same text. Files: `evidence/r8-new-to-old.log`, `evidence/r8-old-server3.log`. |
| `status` probe | **PASS** (extra) | This build's `status 127.0.0.1:25611` against v0.5.0: `no status: an older ww-multiplayer or another program`, exit 3, as CLAUDE.md documents. Against this build's own server: `up (ww-multiplayer dev, protocol 6; ...)`, exit 0. v0.5.0 has no `status` command (`Unknown command: status`, exit 1). |

**Not run here: the app half.**
- Two `ui --browser` instances (v0.5.0 on 25701, this build on 25702) served on 127.0.0.1 and answered `curl` with 200.
- The connected Chrome could not open either page (Chrome's error page for both). It is likely not on this Mac.
- So two things were not checked on the Mac: the version dialog on both sides, and the LAN list greying the other build. The PC's LIVE-6 (handoff `archive/ww-online-review/live/LIVE-6.md`:223-225) passed both ways, except that the hosting app shows no dialog, only a `refused` log line. Whether the host needs a notice stays Stephen's call.
- A Mac rerun needs a browser on this Mac, for example headless Chrome driven by a script.

**Verdict.**
- R8's CLI refusal passes both ways on macOS too.
- R8 stays unticked, for the same open point as on the PC: the hosting app shows no dialog, which is the owner's call.

**End state.** No server, fake client or UI process from this block is running. The v0.5.0 copy stays in scratch only and was never committed.
