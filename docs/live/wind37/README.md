# wind#37: TotG C2, FF2 D2(6a) and the M2tower exit, the KoRL loop (Mac rig, 2026-11-01)

Follow-up to wind#22 (`docs/live/wind22-totg-ff-2026-11-01.md`, PR #248).

**Build.** main c1278b3 tools (`~/ww-shared/bin`), REL 0x1CE30685.

**Rig.**
- Mac rig `wind37`, Dolphin 2603-342, 30 fps. Irori lease `ww/dolphin-slot-2`.
- Saves: maple's Mac save states `start-ff.sav` (CryZe 9-Hyrule1) and `start-totg.sav` (CryZe 8-StartTogs), with the same GCI on both cards.
- Co-op: CLI `host --coop=new --coop-iso <the shared WW-mp.iso> --yes` / `join --coop --coop-iso ... --yes`, default boss rows (helmaroc included). Both joined with `image|puzzle|combat|live-switches|switches|story|inventory|keys|life|magic`.

Every stand-in poke is noted where it was made.

## (3) The King of Red Lions talk loop at the M2tower dock: rig, not co-op

- **Repro without co-op.** No host/join process was running. B alone: `stage-hop sea 16 1`. B sits in the boat at the dock and the talk opens by itself ("The power you must obtain lies ahead, hidden within the castle", `korl-solo-2.png`).
- **The loop.** After 14 A presses the talk is back at its first word (`korl-solo-after-A.png`), and `stage-hop` refuses (`not now: an event is running`).
- **Cause.** maple found it on wind#31: an A press beside the boat is "Speak", so each A reopens the talk. Close it with B, and press no A before moving away.
- **Verdict.** A rig artefact of driving the dock with A presses. No co-op hold is involved.

## (2) FF2 D2(6a): entry after the partner's Helmaroc kill, then the exit

World `869d6fb70b9c467d`.

1. **The kill (stand-in).** A at the tower top (`M2tower 30 0 3`). **POKE**: A's live dungeonItem |= 0x08 (`0x803C53A1`, FF STAGE_BOSS_ENEMY), standing in for A's kill.
2. **Held in FF.** B was in `MajyuE 0 0` and logged `holding t02 FF boss defeated back: the boss stage reads them every frame ...`. That is by design: `bossStage` (`mods/coop/replica/planner.go:178`) holds FF's boss bits in every table-2 stage.
3. **Landed on the sea.** B hopped `sea 0 44` (not the dock): `set t02 FF boss defeated (0x803C4FF1 |= 0x08)`, and B's saved table 2 reads 0x00080000. The FF1-sword hold released at the same time (`set mCollect[0..3] bit 0`, the Hero's Sword equipped).
4. **Entry.** B hopped `M2tower 30 0 3`. Pass:
   - **No bird.** B's one Bdk (proc 0x00F0) is parked at (300000, 300000, 300000) with hp 0/0 (`m2tower-bdk-lines.txt`). That is AFTER_FIGHT's create, per `design/ff-m2tower-exit.md` §1.3.
   - **Spikes down.** The Bdk's Ttoge word +0x62D4 reads 0xC3FA0000 (-500.0, lowered).
   - **No request.** No `boss_kill` line in B's log, and B's request block at 0x80413410 is all zero.
   - **Frame.** B's frame shows the empty top (`m2tower-B-3.png`).
5. **Control.** A, whose own save has the bit, left and re-entered the same way. Same Bdk line, same Ttoge -500.0, same frame (`m2tower-A-control.png`). The actor lists differ only by one `fpcNm_RECTANGLE_e` (0x00B5) at the arena centre on A. That isn't boss-related and wasn't looked into.
6. **The exit (real input after a warp).**
   - B warped in front of door10 (1891, 11124, -8450) and faced it. Stick forward plus A took B through door10 into `M2ganon` room 0.
   - **Then B's game hung:** 0 fps, a black frame, and `Invalid read from 0x00000000, PC = 0x802c1bf0` (`m2ganon-hang-dolphin-lines.txt`). The PCs are `JUTNameTab::calcKeyCode` +0x1c and `JUTNameTab::getName` +0x74 (decomp symbols), a name lookup on a null table. No JUTException.
   - **The same on A (control):** A took the same exit, with no co-op difference, and hung at the same PCs. So it isn't co-op.
   - **Likely cause [I].** M2ganon's `attack_ganon` plays with resources or state that the real tower-top fight and its demo set up, and a poked kill leaves them out.
   - **Follow-up card:** check with a real Helmaroc kill.

## (1) TotG C2: r12's floor switch and sw14

World `88a07e3c7c9606c5`. Raw peeks in `c2.txt`.

1. **The switch arrives.** **POKE**: A's sw14 (memory switch word 0x803C5384 |= 0x4000), with both in Siren r0. B got it live in under 6 s (`set t05 TotG switch 0x0E (0x803C5042, and its live copy |= 0x40)`), live 0x00004000 and saved 0x00004000. Co-op logged `small keys, table 5 (TotG): door 0x0E is counted after your next room change`, then `door 0x0E counted` once B left the room.
2. **B into r12.** B hopped `Siren 0 12`. Room 12 loaded, but **without Link** (a black frame at 30 fps, and later hops refused with `Link is not loaded`). So this is the room's actors running with no player in the room, not a walk-in.
3. **The answer: yes, the switch clears sw14.**
   - B's live sw14 read 0 at t+3 s, and B's saved table 5 read 0 at t+6 s.
   - It stayed 0 through t+40 s.
   - A kept 0x00004000 the whole time.
   - Co-op wrote nothing back to B and logged nothing about sw14 while B stayed in r12.
   - This is the type-1 floor switch's unpressed `offSwitch(sw)` (`design/coop-slice-totg.md:83, :135`), on the bit the r8↔r10 key door reads as its lock (`d_a_door10.cpp:31-60`).
4. **Not checked.**
   - The door's look (B couldn't leave r12).
   - Whether co-op re-sets sw14 when B next changes room.
   - A walk-in with Link present.
   - Key counts: 0 on both (8-StartTogs has no TotG key yet).

**What this means for A1.** A partner's sw14 counts B's key door as opened (co-op's derived key count follows it). In this run B's own r12 then cleared the bit for good and co-op did not restore it. So B could end up with a key spent and the door shut. The A1 exception rule should keep sw14 (and sw16/sw17, the same switch type) out of what A1 demotes, or co-op should re-assert the world's sw14 after a local clear. That choice belongs to A1's author. The open "re-set at the next room change" check is what tells the two apart.

## Sweep

- Dolphin logs: no ISI, DSI or invalid access except the M2ganon hang lines above, which occur on both games.
- Go logs (`ff-*.log`, `totg-*.log`, periodic lines stripped): no panic or error lines.
