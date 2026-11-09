# Native widescreen (#1186): research in progress

Work in progress for card #1186. Remove this folder once `docs/design/native-widescreen.md`
is written from it.

- `map-<area>.json`: what each area agent found (every 4:3 / 640x480 assumption, with the decomp
  file:line, DOL addresses, scope (shared, per screen, per element), anchoring and a proposed fix).
  Areas: 3d, gfx2d (2D foundation, fades, EFB copies), hud, menus, text (text boxes, picto box,
  telescope, movies), gecko (Dolphin's own "$16:9 Widescreen" Gecko code for GZLE01, decoded),
  platform (EFB limits, Dolphin modes, T2/REL budget, the setting, our own 2D).
- `verify-<area>.json`: an adversarial re-check of each item (confirmed / corrected / refuted /
  unverified) and what the finder missed. Apply the corrections before using a finding.
- The cross-area completeness critic had not finished at wind-down; re-run it if needed.
- `gecko-decoded.txt`: the Gecko code's patch addresses mapped to decomp symbols.
- Tools used (paths are the original job's; edit them): `dolread.py` (DOL words and floats from a
  vanilla main.dol), `symlook.py` (address to symbol from ~/projects/tww symbols.txt). The vanilla
  DOL was extracted from `~/ww-shared/disc/ww-vanilla.ciso` with `inject.LoadImage` (DOL offset at
  0x420, FST at 0x424). Offline disassembly: `WW_DOL_PATH=<dol> wwdev ppc-disasm <addr> <n>`.

Design model the agents worked to: anamorphic. The game keeps its 640-wide frame, Dolphin outputs
16:9 with its hack off, the mod widens the 3D aspect by F = aspect / (4/3) (mode 2 of the #1184
block, docs/design/widescreen-culling.md section 9), compresses 2D about the centre, widens
full-screen 2D back, and re-anchors HUD parts to the edges.
