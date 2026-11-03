# wind#90: the C runtime's rings, live on the Mac rig (2026-11-03)

Build: branch `mac/wind90-m7-ring`, BlobHash 0x62DBA05C, REL 0x3D7FEC97 (unchanged), T2 0x80410000..0x80413408.
Disc: `~/ww-shared/disc/WW-mp-3D7FEC97-62DBA05C.iso` (patched from the vanilla ciso). Start save:
`~/ww-shared/fixtures/start-mac-3D7FEC97-62DBA05C.sav`, recaptured unattended (`mac-rig.sh recapture`).
Rig `wind90`, lease `ww/dolphin-slot-3`, Dolphins A (pid 91977) and B (pid 92038), both from that save.

## The region and ECHO, both Dolphins

```
API region @ 0x80414000: magic 57574150 layout 1 dir 3
  dir "EVTR" +0x040 size 0x110
  dir "CMDQ" +0x150 size 0x110
  dir "COOP" +0x300 size 0xC00
events write 1 read 0; commands head 0 tail 0
done 0 refused 0 bad_check 0 unknown 0 events_dropped 0
heap errors: game 0 zelda 0 archive 0 (solid 0)
echo: ok in 20ms            (B: 26ms)
  event 1 kind 3 a CAFEF00D b 00000007 c FFFFFFFF   (ECHO)
  event 2 kind 2 a 00010001 b CAFEF00D c 00000000   (CMD: op 1, seq 0, result ok)
```

## KILL on A (Hyrule; a Moblin, pid 333, proc 0x00BD, unkeyed)

```
kill pid 333 proc 0x0099: mismatch        wrong proc
kill pid 219 proc 0x00A9: refused         Link
kill pid 215 proc 0x00A7: refused         the ship
kill pid 9999 proc 0x00BD: not_found
kill pid 333 proc 0x00BD: mismatch        set 7 named, the Moblin's setID is 0xFFFF
kill pid 333 proc 0x00BD: ok              gone from the actor queue a second later
kill pid 333 proc 0x00BD: not_found
done 8 refused 6 bad_check 0 unknown 0 events_dropped 0
heap errors: game 0 zelda 0 archive 0 (solid 0)
```

## Two Dolphins with mp-local

`mac-rig.sh mp-local wind90` (log: `mp-local.log`): "Local multiplayer running.", enemy sync mirroring 6 enemies.
Link A walked forward with `wwdev input`; B draws it with its name tag (`B-sees-A.png`, `A-walked.png`): the
mini-Link draw now runs through the `WwModuleDesc` table's `draw_post`.

Stage loads on both Dolphins (`stage-hop`): sea, Hyrule, M_NewD2, kenroom, sea. After each, on both:
`heap errors: game 0 zelda 0 archive 0`; `solid` went to 1 at M_NewD2 and stayed there (vanilla's `door10`, docs/08 §4.5).
ECHO after the hops: ok on both (26 ms, 6 ms). No ISI/DSI/invalid read or write in either dolphin.log. mp-local stopped cleanly.
