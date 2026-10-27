// The peer boats block in the mailbox slack (WWBT): the other players'
// King of Red Lions, drawn where they sail and where they left it (ww-online
// review, peer boats; docs/05 "Peer boats").
//
// Shared by the REL (inject/rel/boats.c, which builds, poses and draws one
// boat per entry) and Go (wwapi/mailbox/boat.go mirrors every #define here,
// pinned by TestBoatHeaderPins, which parses this file the way
// TestFxHeaderPins parses wwfx.h; mods/multiplayer/boatsync fills the
// entries from the pose trailers). Keep every #define on one line as
// `#define NAME value // comment`, a plain decimal or hex number: no
// expressions, no negatives.
//
// Place: MAILBOX_ADDR + 0x9E4 (0x804138E4), right after the visuals block
// (wwvis.h, +0x8D0..+0x9E4), at the bottom of what was the free span below
// the mini-Links' eye packets (mailbox_mp.h MP_EYE_PACKETS_OFF, +0xD00). The
// region probe's CanaryArenaHigh now starts at this block's end (+0xA6C)
// and src/mp/minilink.c asserts the blocks do not overlap. No T2 code reads
// or writes it.
//
// Owners:
//   +0x00 u32 magic     Go (puppet-sync): WWBOAT_MAGIC = draw the entries,
//                       else 0 (the REL then frees every boat)
//   +0x04 u32 flags     Go: WWBOAT_F_*
//   +0x08 u32 drawn     REL: boats entered into the draw lists (wraps)
//   +0x0C u32 err       REL: the last WWBOAT_ERR_*
//   +0x10 entries       Go: WWBOAT_MAX entries of WWBOAT_E_SIZE
// Go writes the whole block at once; an entry is a remote's boat on this
// game's stage, in player id order, unused entries zero.
//
// A boat is never an actor: a second daShip_c would take over the global
// ship pointer (dComIfGp_setShipActor, d_a_ship.cpp:4746, mpPlayerPtr[2]).
// The REL makes the models itself, only while this game's "Ship" archive
// is resident (the sea and the stages with a ship), in a solid heap of its
// own per entry that it destroys when the entry empties, the archive goes,
// a stage change starts, the magic drops or the module unlinks.
#ifndef WW_WWBOAT_H
#define WW_WWBOAT_H

#define WWBOAT_OFF            0x9E4       // block start, from MAILBOX_ADDR (0x804138E4)
#define WWBOAT_SIZE           0x88        // ends +0xA6C (0x8041396C)

#define WWBOAT_MAGIC_OFF      0x00        // u32 Go: WWBOAT_MAGIC, 0 = off
#define WWBOAT_FLAGS_OFF      0x04        // u32 Go: WWBOAT_F_*
#define WWBOAT_STAT_DRAWN_OFF 0x08        // u32 REL: boats entered (wraps)
#define WWBOAT_STAT_ERR_OFF   0x0C        // u32 REL: last WWBOAT_ERR_*
#define WWBOAT_ENTRIES_OFF    0x10        // entry[WWBOAT_MAX]

#define WWBOAT_MAGIC          0x57574254  // 'WWBT'
#define WWBOAT_MAX            3           // boats drawn at once
#define WWBOAT_E_SIZE         0x28

#define WWBOAT_F_SAIL         0x01        // draw the sail cloth (the game's daGrid_c code on an imitation grid)
#define WWBOAT_F_PARTS        0x02        // draw the cannon or the salvage crane when the remote has one out

// Entry fields (0x28 bytes).
#define WWBOAT_E_FLAGS        0x00        // u8 WWBOAT_EF_*
#define WWBOAT_E_PART         0x01        // u8 daShip_c::mPart (d_a_ship.h:74-77): 2 cannon, 3 crane, else none
#define WWBOAT_E_MAST_FRAME   0x02        // u8 the mast bck's frame (mpBodyAnm), whole frames
#define WWBOAT_E_HEAD_BCK     0x03        // u8 the head's "Ship" bck index (m03B4), 0 = keep the last
#define WWBOAT_E_HEAD_FRAME   0x04        // u8 the head bck's frame (mpHeadAnm)
#define WWBOAT_E_R            0x05        // u8 sail colour: the remote's tunic colour; 0,0,0 = the vanilla sail
#define WWBOAT_E_G            0x06
#define WWBOAT_E_B            0x07
#define WWBOAT_E_POS          0x08        // f32[3] the hull's world position (current.pos)
#define WWBOAT_E_ANGLE        0x14        // s16[3] shape_angle x, y, z
#define WWBOAT_E_SAIL_ANGLE   0x1A        // s16 mSailAngle (+0x364): J_FN_SAIL1 turns by -this about Z
#define WWBOAT_E_TILLER       0x1C        // s16 m0366: J_FN_STEER1 and J_FN_KAJI turn by this about Z
#define WWBOAT_E_PART_A       0x1E        // s16 cannon: m0394 (CANON1 about X); crane: m0398 + m039C (V_CRANE_ROTATION about -Z)
#define WWBOAT_E_PART_B       0x20        // s16 cannon: m0396 (CANON2 about -Y)
#define WWBOAT_E_PAD          0x22        // 6 bytes, 0

#define WWBOAT_EF_SHOW        0x01        // the entry is a boat to draw
#define WWBOAT_EF_MAST_ON     0x02        // the mast bck is FN_MAST_ON2 (m0392 == 0x0B), else FN_MAST_OFF2
#define WWBOAT_EF_MAST_HIDE   0x04        // m03E8 < 0.5: the mast scaled to 0.001 (the cannon or crane in its place)
#define WWBOAT_EF_SAIL_ON     0x08        // mStateFlag daSFLG_SAIL_ON_e (0x200): the sail is set (ho_move's wind)
#define WWBOAT_EF_PARKED      0x10        // nobody aboard: off the sea the REL floats it on this game's water at its XZ

#define WWBOAT_ERR_NONE       0
#define WWBOAT_ERR_HEAP       1           // mDoExt_createSolidHeapFromGame returned NULL
#define WWBOAT_ERR_MODEL      2           // a hull or head morf, or its J3DSkinDeform, failed (no boat)
#define WWBOAT_ERR_SAIL       3           // the sail block did not fit or new_ho1 is not C8/RGB565 (no sail)

#endif
