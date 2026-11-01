# wind#9 live: GT Q5, the credits are off co-op (Mac rig, 2026-11-01)

Build: main 5b828ff (#218 GT Q5 and #232 co-op schema 2 in), wwdev built from it and entitled;
disc `~/ww-shared/disc/WW-mp.iso` (code hash 0x04765DD0, contract "matches this version" on both
Dolphins). Rig `wind9` (mac-rig.sh pair from `start-mac-1CE30685.sav`, file "Abahbob", Hyrule),
lease `ww/dolphin-slot-3`, Dolphin 2603 on the M5 Max. PIDs A 65098, B 65108.

Session: `MP_LOCAL_PORT=0 mac-rig.sh mp-local wind9 Aryll Tetra --coop=new --yes`. World
490a87ff241f440b founded from A's file; both files bound; both replicas armed with
image|puzzle|combat|live-switches|switches|story|inventory|keys|life|magic.

## Q5-1, in part

1. `WW_DOLPHIN_PID=65098 wwdev stage-hop sea_E 0 44 --yes`: A loads the epilogue stage (`sea_E`
   r44, save table 0). `coop-probe` on A: `STABLE closed: the credits are playing (GT Q5: this
   game has left the co-op world)`.
2. B's log, at A's first PRESENCE there: `[coop-B] co-op: Aryll has reached the credits; their
   game has left the co-op world, yours plays on`. B's pose feed hides Aryll ("in another stage").
3. With A in `sea_E`, sea chest 30's bit (live memBit 0x803C5380, mask 0x40) was poked on A.
   After 10 s A still had it, B's saved sea memBit (0x803C4F88) still read 0x00 in that byte, and
   neither replica nor the hub logged a GAINS, a set or a relay: nothing went out of the credits.

Not done here: A beating Ganondorf for real (the hop stands in for `endhr`), B finishing and
getting its own `ending`/`epilogue` events, and "back in play, nothing sent late". For that last
one, `wwdev stage-hop` cannot leave the credits: it writes only when the gates are open, and
STABLE is now closed there by design ("not now: the credits are playing"). In real play the game
leaves the epilogue for the title by itself. The offline `TestDifferCreditsSendNothing` covers
the re-baseline (it fails on the old differ). A GT-rig run (late-game fixture) still owes the
rest of Q5-1.

Crash sweep: no ISI, DSI or invalid access in either `dolphin.log`; no panic in the session
log. Both Dolphins stopped by PID; mp-local stopped by its stop file; lease released.

## Q5-1, the real ending (2026-11-01, later; grove)

Build: main b9d8b2d (REL 0xA9DE0F1E), wwdev built from it in the worktree and entitled; disc
`~/ww-shared/disc/WW-mp.iso` (code hash 0x04765DD0, "matches this version" on both). Rig
`wind9q5` (fresh `setup`), lease `ww/dolphin-slot-5`. Both cards replaced by the wind#11 fixture
`b-28-Hyrule3-GanonK-cleared.gci` (md5 c6b3c091…, as in MD5SUMS), cold-booted with
`mac-rig.sh recapture` to a rig start save, then `pair` from it (PIDs 72581, 72591, both 30 fps).
`MP_LOCAL_PORT=0 mac-rig.sh mp-local wind9q5 Aryll Tetra --coop=new`: world 63cbd1fd… founded
from A's file, both files bound, both replicas armed.

1. Baseline: t0 saved memBit byte 0x803C4F99 (sw114 mask 0x04, sw118 mask 0x40, from
   `addr.SwitchBit`) = 0x12 on both games: both clear.
2. `WW_DOLPHIN_PID=72581 wwdev stage-hop GTower 2 0 9 --yes` at 11:45:30. The real ending played
   on A: Ganondorf's last lines, Link and Tetra in the sea, Tetra's ship, the staff-roll scenes.
   An A-press loop (A for 150-200 ms every 1-3 s) ran through it to get past the text boxes.
3. Polled every 5 s: `mCurStage` (0x803C9D3C) read `GTower` for the whole ending (16 min), then
   `Name` at 12:01:59. It never read `ENDING`, `ENDumi` or `sea_E`. `coop-probe` on A during the
   ending: `STABLE closed: Link is not loaded`, not the credits reason.
4. B never logged "has reached the credits"; its puppet log said only `player 1 (Aryll) is out
   of this stage (GTower; here Hyrule)`. Nothing went out from A during the ending (no gains, no
   relay). B's 0x803C4F99 stayed 0x12 and so did A's: sw114/sw118 were never set on either.
5. A's card was unchanged afterwards (md5 still c6b3c091…): no clear save was written (the
   A-press loop was stopped at the NAMEEX screen).

Why (zeldaret/tww, main): `dScnPly_Draw` turns a next stage of "ENDING" into the ending scene
(`fpcNm_ENDING_SCENE_e`, d_s_play.cpp:310-312) instead of a play scene, so `mCurStage` keeps
GTower. The ending scene is `d_s_title.cpp`'s movie: when it ends, clear count 0 goes to NAMEEX
(`d_s_title.cpp:72-80`), whose create sets stage `Name` and clear count 1 and opens the save menu
(`d_s_name.cpp:105-169`); `changeGameScene` then starts the second quest. No code names ENDumi;
sea_E only in a weather check. So the stage list Q5 keyed on is reachable only by dev hops, and in
real play the credits notice never fired. Nothing leaked: the movie has no Link, and the second
quest's file is refused (`NoticeClearCount`).

Fix (this PR): `state.Snapshot.InCredits` adds `mNextStage` = "ENDING" (the request stays in
mNextStage through the movie until NAMEEX's "Name"); the gate checks it first, the differ,
watcher and derives use it, and PRESENCE says ENDING through the movie, so the partner line fires
and the hub places the finisher nowhere. Offline tests in docs/TESTING.md §GT Q5.

Caveats: the stage was read every 5 s, so a short stretch between two reads is not ruled out;
the A-press loop may have skipped prompts. Still owed: a rerun on this fix (B's partner line at
the ending request), B's own ending, and Q5-2 (let NAMEEX save, then `coop-tag show` and the
refusal on loading the cleared file).

Crash sweep: no ISI, DSI or invalid access in either `dolphin.log`; no panic in the session log.
Both Dolphins stopped by PID, mp-local by its stop file, lease released (handed to reed, wind#44).

## Q5-1 rerun on the fix (2026-11-01, 12:38-12:58; grove)

Build: main cf165a5 (#275: the credits are mNextStage "ENDING"), REL 0xA9DE0F1E, wwdev built from
it and entitled. Rig `wind9q5b` (fresh `setup`), lease `ww/dolphin-slot-3`, both cards the
GanonK-cleared GCI (md5 c6b3c091…), `pair` from the rig start save made earlier from that GCI
(PIDs 64451, 64563, both 30 fps). `mp-local wind9q5b Aryll Tetra --coop=new`: world a2a1b3be…,
both bound and armed. A's mCurStage, mNextStage, mEnable and Link, and B's t0 byte 0x803C4F99,
logged every ~1 s (`poll.log`).

1. 12:41:10 `stage-hop GTower 2 0 9 --yes` on A; A pressed for 150 ms every 2 s only until
   mNextStage read ENDING, then hands off.
2. 12:44:20 the ending request: `cur=GTower next=ENDING en=0x01 link=0x80ABAB8C`. In the same
   second B logged `[coop-B] co-op: Aryll has reached the credits; their game has left the
   co-op world, yours plays on`. From 12:44:22 `link=0` (the movie), mCurStage still GTower.
3. `coop-probe` on A during the movie: `STABLE closed: the credits are playing (GT Q5: this game
   has left the co-op world)` (before #275 it said "Link is not loaded").
4. Nothing left A from the request on: no gains, sets or relays in either log.
5. 12:52:49 NAMEEX (`cur=NameG`). B's 0x803C4F99 read 0x12 throughout (sw114 0x04, sw118 0x40
   clear).
6. Q5-2, in part: Yes on NAMEEX's save, Quest Log 1, Yes on save over. A's clear count read 1
   in RAM, and mReserve still held the world tag. The game then sat on Choose a Quest Log; A
   presses by `wwdev input` did not start the second quest, so the refusal on loading the
   cleared file (`NoticeClearCount`) was not seen live. The GCI on disk was unchanged after the
   Dolphins stopped (Dolphin's GCI folder may not have flushed).

Crash sweep: no ISI, DSI or invalid access in either `dolphin.log`; no panic in the session log.
Dolphins stopped by PID, mp-local by its stop file, lease released (handed to walnut, wind#57).
