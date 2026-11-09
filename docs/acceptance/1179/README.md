# Acceptance run: update in the app, Start, co-op with no dialog (#1179, #1166 card 9)

Run 2026-11-09 on one PC (Dolphin 2609b, Windows 11), main at `9b7feba`
(cards #1171-#1178 all merged). Two apps, two Dolphins, in-game input by
the pad shim (`wwdev input`), the app driven through its own HTTP API
(`ui --browser`, the way `wwdev app-found` does) for the buttons a player
clicks. No memory pokes. This is **not** the two-machine run the card
names: both players are on this PC over loopback. The Mac or a second PC
is still to do (see "What this did not cover").

Files: `shots/` (screenshots), `logs/` (both apps' session logs, the
joiner's card-tags log), `rig/` (the driver scripts; `app.py` talks to the
window API).

## Rig

- Older release: **v0.6.0** from GitHub (not v0.6.1: v0.6.1 patches the
  same bytes as main, so the disc stays "keep" and the re-patch is never
  exercised; v0.6.0 patched REL build `0x1CE30685`, sha `5fa98b9a`).
- The Update button's target: a local server (`WW_UPDATE_URL`) serving a
  main build stamped `v0.7.0` with a `SHA256SUMS`. The build's version
  string must equal the release tag, or the post-install smoke check
  rejects it ("the new version didn't start").
- Dolphin profiles are throwaway copies, so the real memory card is
  never touched: A = a copy of the owner's profile (File 1 "STEPHEN",
  bound to world `0868da44…`, a world not on this PC: the 11-08 case),
  B = Config only, no memory card at all (a fresh player).

## Results

| Step | Result | Dialogs answered |
|---|---|---|
| v0.6.0 patches the vanilla ciso in Setup | done | 0 |
| Update button: download, SHA256SUMS, smoke, swap, relaunch | v0.6.0 -> v0.7.0, the app relaunched itself | 0 |
| First start after the update | log: `patched by another version; patch it again`, then `disc update: patching ... (the vanilla disc Setup saved)`; disc `5fa98b9a` -> main's bytes `3689cfdd`; later checks say `patched; keep` | 0 |
| Host + Join (loopback), joiner presses Ready, Start | both apps launch their own Dolphin, both reach `in_game` | 0 (the Ready button is a normal control) |
| Host: file 1 bound to a world not on this PC, loaded by pad | `co-op: founded world 0868da446caca9bf from file slot 0 ... saved and loaded`; the founding panel closed itself; notice "your save belongs to world 0868da44…, which wasn't on this computer, so it was started here from your save"; `Armed` | 0 |
| Joiner: new game by pad (title, file select, name entry, the whole opening) | `Armed` in the same world `0868da44…`; backup of the card taken first | **2 from the game itself** (below) |
| Host closes Dolphin without saving, relaunches, loads the file again | banner "Dolphin closed. ... Launch Dolphin" (one button); card file hash unchanged (`bf98320c…`) before and after; the file still reads bound to `0868da44…`; `Armed` again in the same world | 0 |

Screenshots: `a0` (title), `a3` / `final-A` (host in game, Outset),
`a-relaunch` and `final-A2` (host after the close and reload), `b1` /
`b2` / `b7` / `b8` (joiner's game-side prompts and file select),
`b9-3` (name entry), `b13` / `final-B` (joiner in game, opening).

## What still asked (the target is none)

1. **The joiner's profile has no memory card.** The game itself asks
   "Would you like to create a save file ... on the Memory Card in
   Slot A? Yes / No", then says "A save file has been created". Two
   screens a new player must get through; picking No ends in "The game
   cannot be saved. Would you like to continue anyway?", which leaves
   them unable to carry the world tag. Not co-op's dialog, but the app
   could make the card (or the file) before launch. Card filed.
2. Informational, nothing to answer: the host's "Found a new world"
   panel (Hide button) stays up while the file is being loaded; the
   "your save belongs to world ..." notice; the "Dolphin closed"
   banner (Launch Dolphin).

## What this did not cover

- A second physical machine (the Mac). Everything above is loopback on
  one PC.
- The joiner's tag landing on its card: its new-game file isn't saved
  yet, so "close without saving, relaunch, the tag stays" was run for
  the host only.
- The Update button against a real GitHub release (none carries cards
  1-8 yet; v0.6.1 is the latest).
