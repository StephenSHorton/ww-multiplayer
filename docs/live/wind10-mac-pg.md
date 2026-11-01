# wind#10: PG1/PG2 on the Mac rig (2026-11-01)

Main 24f89cf (BlobHash 0x04765DD0, REL 0x1CE30685). Mac rig per docs/mac-rig.md: M5 Max, macOS 26.6, Dolphin 2603-342 (unhardened copy), MEM1 48 MB, the Mac start save. Pair A = host, B = client: `wwdev mp-local` with MP_LOCAL_PIDS pinned, enemies on by default (kinds mo2,bk+u,cc+u,ki+u, remote bodies step6, flags 0x24FF), lease ww/dolphin-slot-3. Both games full speed (30 fps). B's life is the u16 at 0x803C4C0A (quarter hearts; max 0x50).

Setup: both games `stage-hop M_Dai 1 1` (3 keyed Moblins). Links spawn about 2,700 units from them, so nothing latched; B was warped (`ww-multiplayer warp -4950 0 319`) beside Moblin setID 2, A stayed at the spawn. Within 4 s the host showed body 0 `l2` (two Moblins latched), swaps climbing and the first PHIT with its PACK.

## PG2 (range gate): B teleports away mid-fight, then back

```
01:01:00 fighting B max/life=0x00500043 bodies [0:s2 e0 h4 l2 t1 k0] phits 4 packs 4
01:01:10 fighting+10s B max/life=0x0050003B bodies [0:s2 e0 h6 l2 t1 k0] phits 6 packs 6
01:01:10 warped away B max/life=0x0050003B bodies [0:s2 e0 h6 l2 t1 k0] phits 6 packs 6
01:01:15 away+5s B max/life=0x0050003B bodies [0:s2 e0 h6 l2 t1 k0] phits 6 packs 6
01:01:20 away+10s B max/life=0x0050003B bodies [0:s2 e0 h6 l2 t1 k0] phits 6 packs 6
01:01:25 away+15s B max/life=0x0050003B bodies [0:s2 e0 h6 l2 t1 k0] phits 6 packs 6
01:01:30 away+20s B max/life=0x0050003B bodies [0:s2 e0 h6 l2 t1 k0] phits 6 packs 6
01:01:31 back B max/life=0x0050003B bodies [0:s2 e0 h6 l2 t1 k0] phits 6 packs 6
01:01:36 back+5s B max/life=0x00500037 
01:01:41 back+10s B max/life=0x00500037 
01:01:46 back+15s B max/life=0x00500037 bodies [0:s2 e0 h9 l2 t1 k0] phits 9 packs 9
01:01:51 back+20s B max/life=0x00500033 
```

While away (20 s): phits flat at 6, life flat at 0x3B. Back beside the Moblins: hits land again (9 phits, life 0x33). Blank status columns are samples taken between the host's status lines.

## PG1 (stage gate): B changes stage mid-fight

```
01:02:00 fighting B max/life=0x0050002D 
now in stage "sea" room 1, table 6 ET, file 0
01:02:02 hopped B max/life=0x0050002D 
01:02:07 after+5s B max/life=0x0050002D bodies [0:s0 e0 h13 l0 t1 k0] phits 13 packs 13
01:02:12 after+10s B max/life=0x0050002D bodies [0:s0 e0 h13 l0 t1 k0] phits 13 packs 13
01:02:17 after+15s B max/life=0x0050002D bodies [0:s0 e0 h13 l0 t1 k0] phits 13 packs 13
01:02:22 after+20s B max/life=0x0050002D bodies [0:s0 e0 h13 l0 t1 k0] phits 13 packs 13
01:02:27 after+25s B max/life=0x0050002D bodies [0:s0 e0 h13 l0 t1 k0] phits 13 packs 13
```

After the hop B's body is `s0 l0`, phits stay at 13 = packs 13, life stays 0x2D. The whole mp-local log has no `dropped` / `go_gate` and no `over 8 PHITs` line. Final host counters: swaps=6384 switches=2 attacker_overrides=1 park_rewrites=2 gate_skips=0 phits 13 packs 13.

Not run: walking through a door (a stage hop stood in), the TyuTyu ChuChu crowd (Hyrule room 0's four ChuChus never swapped onto B's body: `denied` only, swaps 0), Ballad of Gales and a void-out (a teleport stood in).

Crash sweep: B's dolphin.log has no ISI/DSI or invalid access. A was launched by hand without WW_DOLPHIN_DEVLOG, so A's dolphin.log is empty and was not swept. (`mac-rig.sh pair` then launched only Dolphin 2 while other rigs' Dolphins ran; fixed in #226.)
