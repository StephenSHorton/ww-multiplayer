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
