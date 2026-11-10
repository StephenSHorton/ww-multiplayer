// The widescreen block in the mailbox slack (WWWS): one real aspect for the
// camera's projection, the clipper and (from later stages) the 2D layers.
// Plans: docs/design/widescreen-culling.md (the cause, the clipper hook),
// docs/design/native-widescreen.md (modes, the 3D knob, stages).
//
// Shared by T2 (src/ws/widescreen.c, the only writer) and Go
// (wwapi/mailbox/widescreen.go mirrors every #define here, pinned by
// TestWidescreenHeaderPins; Go reads it and changes it through the
// WWAP_OP_WS_SET op). Keep every #define on one line as
// `#define NAME value // comment`, a plain decimal or hex number: no
// expressions, no negatives.
//
// Place: MAILBOX_ADDR + 0xA6C (0x8041696C), right after the boats block
// (wwboat.h, +0x9E4..+0xA6C), at the start of what was CanaryArenaHigh
// (which now starts at this block's end, +0xA84).
//
// Layout (all C-written):
//   +0x00 u32 magic     WWWS_MAGIC
//   +0x04 u8  version   WWWS_VERSION
//   +0x05 u8  mode      WWWS_MODE_*: 0 off, 1 clip (Dolphin's hack is on:
//                       widen the clipper only), 2 native (widen the
//                       camera's aspect through the 4/3 knob)
//   +0x08 f32 factor    output aspect / (4/3), in [1, 3]
//   +0x0C u8  flags     WWWS_F_* (stored & WWWS_FLAGS_SUPPORTED)
//   +0x0D u8  ui_mode   latched 2D mode: 2 while 2D is compressed, else 0
//   +0x10 f32 ui_factor latched 2D factor; 1.0 unless ui_mode is 2
//   +0x14 f32 ui_shift  latched HUD edge shift 329.5 * (ui_factor - 1)
//                       with WWWS_F_HUD, else 0
// The ui_* fields move only at boot and when an EPOCH screen loads
// (main_parts1.blo, main_parts_fileselect.blo: src/ws ws_latch).
#ifndef WW_WWWS_H
#define WW_WWWS_H

#define WWWS_OFF              0xA6C       // block start, from MAILBOX_ADDR (0x8041696C)
#define WWWS_SIZE             0x18        // ends +0xA84 (0x80416984)
#define WWWS_MAGIC            0x57575753  // 'WWWS'
#define WWWS_VERSION          1           // layout version in +0x04
#define WWWS_MAGIC_OFF        0x00        // u32
#define WWWS_VERSION_OFF      0x04        // u8
#define WWWS_MODE_OFF         0x05        // u8
#define WWWS_FACTOR_OFF       0x08        // f32
#define WWWS_FLAGS_OFF        0x0C        // u8
#define WWWS_UI_MODE_OFF      0x0D        // u8
#define WWWS_UI_FACTOR_OFF    0x10        // f32
#define WWWS_UI_SHIFT_OFF     0x14        // f32
#define WWWS_MODE_OFF_VAL     0           // mode: nothing widened
#define WWWS_MODE_CLIP        1           // mode: the clipper only (Dolphin's hack widens the picture)
#define WWWS_MODE_NATIVE      2           // mode: the camera's aspect, through the 3D knob
#define WWWS_F_2D             0x01        // flags: compress 2D in mode 2 (stage 2)
#define WWWS_F_HUD            0x02        // flags: anchor the HUD to the edges (stage 3)
#define WWWS_FLAGS_SUPPORTED  0x03        // the flags this build supports (stage 3: WWWS_F_2D | WWWS_F_HUD)
#define WWWS_KNOB_ADDR        0x803FA998  // the 4/3 literal preparation() reads for view->mAspect
#define WWWS_KNOB_VANILLA     0x3FAAAAAB  // 4/3 as an f32
#define WWWS_FACTOR_MAX_BITS  0x40400000  // 3.0f
#define WWWS_FACTOR_DEFAULT   0x3FAAAAAB  // 4/3 as an f32: 16:9, the default factor in mode 1

#endif
