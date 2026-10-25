// The remote players' visuals block in the mailbox slack: name tags,
// sword, shield, scabbard and casual hair on the mini-Links, and the sea
// chart markers.
// Shared by T2 (src/mp/minilink.c calls the draw hook word, src/core/
// hooks.c's seachart_shim calls the sea chart word and main01_init zeroes
// the REL words at boot), the REL (inject/rel/visuals.c, which publishes
// the two function words and reads the rest) and Go (wwapi/mailbox/vis.go
// mirrors every offset; TestVisLayout checks them against this file).
//
// Ideas from datstarkey/ww-online (MIT): puppet_nametag.c, puppet_seachart.c,
// seachart_players.asm, puppet_draw.c. Their code is adapted in visuals.c,
// with the credit there.
//
// Place: MAILBOX_ADDR + 0x8D0 (0x804137D0), right after batch D's
// actor_refresh request (wwrefresh.h, +0x8B0..+0x8D0), in what was the
// start of the region probe's CanaryArenaHigh (wwapi/mailbox/mod.go, which
// now starts at WWVIS_OFF + WWVIS_SIZE). Nothing else writes here.
//
// Owners:
//   +0x00 draw fn      REL: &wwvis_draw_hook while linked (_prolog writes
//                      it last, _epilog clears it first), else 0; T2's
//                      mp_draw_post calls it per drawn mini-Link slot
//   +0x04 seachart fn  REL: &wwvis_seachart_draw, same rules; T2's
//                      seachart_shim calls it from dDlst_FMAP_c::draw
//   +0x08..+0xA3       Go: magic, flags, names, equip, sea entries
//   +0xA4..+0xDF       REL: the sword and shield J3DModels it made (archive
//                      heap, once per boot, never freed: the F18 rule); T2's
//                      main01_init zeroes them at boot like WWREL_SHANM_OFF
//   +0xE0..+0xEF       REL: diagnostics counters
//   +0xF0..+0x113      REL: the mirror shield, Master Sword scabbard and
//                      katsura J3DModels (WWVIS_MODELS2), same rules as
//                      +0xA4; main01_init zeroes +0xA4..+0x113 (models,
//                      counters and models2: WWVIS_ZERO_WORDS)
// WWMB_LAYOUT 3 (three mini-Link slots) grew the block from 0xC0 to 0xF0:
// names, equip and models are per slot, so everything from the equip
// entries on moved. The wave-3 looks (models2, three slots) grew it to
// 0x114, so the block now ends at +0x9E4.
// The REL reads Go's part only while the magic is WWVIS_MAGIC.
#ifndef WW_WWVIS_H
#define WW_WWVIS_H

#define WWVIS_OFF               0x8D0       // block start, from MAILBOX_ADDR (0x804137D0)
#define WWVIS_SIZE              0x114       // ends +0x9E4 (0x804138E4)

#define WWVIS_DRAW_FN_OFF       0x00        // u32 REL: void (*)(int slot, J3DModel* model, void* link, int phase)
#define WWVIS_SEACHART_FN_OFF   0x04        // u32 REL: void (*)(u8* fmap_dl, void* graf)
#define WWVIS_MAGIC_OFF         0x08        // u32 Go: WWVIS_MAGIC, 0 = block off
#define WWVIS_FLAGS_OFF         0x0C        // u32 Go: WWVIS_FLAG_*
#define WWVIS_NAMES_OFF         0x10        // char[WWVIS_SLOTS][WWVIS_NAME_BYTES], per mini-Link slot, NUL padded
#define WWVIS_EQUIP_OFF         0x58        // u8[WWVIS_SLOTS][4], per mini-Link slot (WWVIS_EQ_*)
#define WWVIS_SEA_OFF           0x64        // entry[WWVIS_SEA_MAX] x WWVIS_SEA_SIZE
#define WWVIS_MODELS_OFF        0xA4        // u32 J3DModel*[WWVIS_SLOTS][WWVIS_MODEL_KINDS] (REL)
#define WWVIS_STAT_TAGS_OFF     0xE0        // u32 REL: name tags queued (wraps)
#define WWVIS_STAT_EQUIP_OFF    0xE4        // u32 REL: sword/shield models entered (wraps)
#define WWVIS_STAT_SEA_OFF      0xE8        // u32 REL: sea chart markers drawn (wraps)
#define WWVIS_STAT_ERR_OFF      0xEC        // u32 REL: last WWVIS_ERR_*
#define WWVIS_MODELS2_OFF       0xF0        // u32 J3DModel*[WWVIS_SLOTS][WWVIS_MODEL2_KINDS] (REL)
#define WWVIS_ZERO_WORDS        ((WWVIS_SIZE - WWVIS_MODELS_OFF) / 4) // main01_init: models, counters, models2

#define WWVIS_MAGIC             0x57575653  // 'WWVS'
#define WWVIS_SLOTS             3           // = MAILBOX_POSE_SLOT_CAP (mini-Link slots)
#define WWVIS_NAME_BYTES        24          // NUL included: 23 printable ASCII characters

#define WWVIS_FLAG_NAMES        0x01        // draw the name tags
#define WWVIS_FLAG_EQUIP        0x02        // draw the sword, shield and scabbard from WWVIS_EQUIP
#define WWVIS_FLAG_SEACHART     0x04        // draw the sea chart markers
#define WWVIS_FLAG_HAIR         0x08        // a casual remote (WWVIS_EQF_CASUAL): katsura hair, no hat, no buckle

// Equip entry (4 bytes per slot): the remote's mSelectEquip[0] / [1]
// (dItemNo_*, 0xFF none) and what it holds.
#define WWVIS_EQ_SWORD          0x00        // u8 mSelectEquip[0]: 0x38 hero's sword, 0x39/0x3A/0x3E Master Sword, 0xFF none
#define WWVIS_EQ_SHIELD         0x01        // u8 mSelectEquip[1]: 0x3B hero's shield, 0x3C mirror shield, 0xFF none
//                                          (both drawn; the mirror shield with its own J3DSkinDeform, as Link's)
#define WWVIS_EQ_FLAGS          0x02        // u8 WWVIS_EQF_*
#define WWVIS_EQF_VALID         0x01        // the entry is the remote's (else the slot draws nothing of it and the scabbard mirrors Link #1)
#define WWVIS_EQF_SWORD_HAND    0x02        // mEquipItem == daPyItem_SWORD_e (0x103): the sword is out
#define WWVIS_EQF_SHIELD_HAND   0x04        // the shield's base matrix is the right hand's (Link's own rule, d_a_player_main.cpp:1304-1311)
#define WWVIS_EQF_CASUAL        0x08        // the remote wears the casual clothes (appearance bit 0); set without VALID too

// Sea chart entry (0x10 bytes). Go writes one per remote on the sea stage.
#define WWVIS_SEA_MAX           4
#define WWVIS_SEA_SIZE          0x10
#define WWVIS_SE_X              0x00        // f32 world x
#define WWVIS_SE_Z              0x04        // f32 world z
#define WWVIS_SE_ANGLE          0x08        // s16 shape_angle.y
#define WWVIS_SE_R              0x0A        // u8 tint
#define WWVIS_SE_G              0x0B
#define WWVIS_SE_B              0x0C
#define WWVIS_SE_FLAGS          0x0D        // u8 bit 0 = show
#define WWVIS_SEF_SHOW          0x01

// The REL's models per slot (archive heap, Link.arc's model data).
#define WWVIS_MODEL_KINDS       5
#define WWVIS_MK_GRIP_A         0           // LINK_BDL_SWGRIPA (0x26) hero's sword hilt
#define WWVIS_MK_GRIP_MS        1           // LINK_BDL_SWGRIPMS (0x45) Master Sword hilt
#define WWVIS_MK_BLADE_A        2           // LINK_BDL_SWA (0x25) hero's sword blade (in hand)
#define WWVIS_MK_BLADE_MS       3           // LINK_BDL_SWMS (0x46) Master Sword blade (in hand)
#define WWVIS_MK_SHIELD_A       4           // LINK_BDL_SHA (0x24) hero's shield
// ... and at WWVIS_MODELS2_OFF (kind - WWVIS_MODEL_KINDS per slot):
#define WWVIS_MODEL2_KINDS      3
#define WWVIS_MK_SHIELD_MS      5           // LINK_BDL_SHMS (0x43) mirror shield (+ J3DSkinDeform)
#define WWVIS_MK_POD_MS         6           // LINK_BDL_PODMS (0x22) Master Sword scabbard
#define WWVIS_MK_KATSURA        7           // LINK_BDL_KATSURA (0x20) casual clothes hair
#define WWVIS_MK_ALL            (WWVIS_MODEL_KINDS + WWVIS_MODEL2_KINDS)

// The block tiles with no overlap (WWMB_LAYOUT 3, three slots, models2).
_Static_assert(WWVIS_NAMES_OFF + WWVIS_SLOTS * WWVIS_NAME_BYTES <= WWVIS_EQUIP_OFF &&
               WWVIS_EQUIP_OFF + WWVIS_SLOTS * 4 <= WWVIS_SEA_OFF &&
               WWVIS_SEA_OFF + WWVIS_SEA_MAX * WWVIS_SEA_SIZE <= WWVIS_MODELS_OFF &&
               WWVIS_MODELS_OFF + WWVIS_SLOTS * WWVIS_MODEL_KINDS * 4 <= WWVIS_STAT_TAGS_OFF &&
               WWVIS_STAT_ERR_OFF + 4 <= WWVIS_MODELS2_OFF &&
               WWVIS_MODELS2_OFF + WWVIS_SLOTS * WWVIS_MODEL2_KINDS * 4 == WWVIS_SIZE,
               "wwvis.h: the visuals block's parts overlap or leave its end");

#define WWVIS_ERR_NONE          0
#define WWVIS_ERR_RES           1           // dRes_getRes_byIdx returned NULL
#define WWVIS_ERR_CREATE        2           // mDoExt_J3DModel__create returned NULL
#define WWVIS_ERR_FONT          3           // mDoExt_font0 not usable
#define WWVIS_ERR_SKIN          4           // the mirror shield's J3DSkinDeform: no memory or setSkinDeform failed (not drawn)

// Draw hook phases (T2 calls the hook twice per slot).
#define WWVIS_PHASE_PRE         0           // before the slot's body entry: the scabbard shape, the casual hat and buckle
#define WWVIS_PHASE_POST        1           // after it: scabbard restored, sword/shield/scabbard/hair, name tag

#endif
