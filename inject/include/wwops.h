// The API region (co-op M7, wind#90; docs/api/00-design.md §6.6 "Later",
// docs/08 §4.5): a fixed range below the mailbox that holds the C runtime's
// rings. T2 code ends below it (TestT2EndsBelowMailbox), and the mailbox
// starts right after it.
//
//   0x80410000..0x80414000  T2 (code and data; 0x4000 budget)
//   0x80414000..0x80414F00  this region (WWAP_SIZE 0xF00)
//   0x80414F00              the mailbox (mailbox_core.h MAILBOX_ADDR)
//
// Shared by T2 (src/core/ops.c, which owns the header, the event ring's
// writer side and the command queue's consumer side) and Go (wwapi/mailbox/
// ops.go mirrors every #define here, pinned by TestOpsHeaderPins; wwapi/ops
// reads the events and submits the commands). Keep every #define on one line
// as `#define NAME value // comment`, a plain decimal or hex number: no
// expressions, no negatives.
//
// Nothing else zeroes this RAM: main01_init clears the header and both rings
// at every boot (a soft reset leaves the old boot's words here), then writes
// the magic last. Go treats a region without the magic as "no runtime".
//
// Header (C):
//   +0x00 u32 magic 'WWAP'
//   +0x04 u16 layout, u16 dir count
//   +0x08 directory: WWAP_DIR_MAX entries of {u32 tag, u16 off, u16 size}
//   +0x28 u32 counters (C, wrap): commands done, refused (any result but OK),
//         bad check, unknown op, events dropped (Go not draining)
//
// Event ring (C writes, Go reads): one-frame edges for Go, ww_event_push.
//   +0x40 u32 write (C: events written), +0x44 u32 read (Go: events taken)
//   +0x50 WWAP_RING entries of WWAP_ENTRY_SIZE: {u16 kind, u8 seq, u8 check,
//         u32 a, u32 b, u32 c}; seq is the event's count & 0xFF, check the
//         XOR of the entry's other 15 bytes.
// Event n (0-based count) sits at entry n % WWAP_RING. C fills the entry,
// then bumps write, and never runs more than WWAP_RING ahead of read: a full
// ring drops the event and counts it.
//
// Command queue (Go writes, C runs): the generalised warp handshake.
//   +0x150 u32 head (Go: commands written), +0x154 u32 tail (C: commands run)
//   +0x160 WWAP_RING entries: {u8 op, u8 seq, u8 result, u8 check, u32 a,
//          u32 b, u32 c}; Go writes result 0, seq = the command's count &
//          0xFF, check = the XOR of op, seq and a, b, c's bytes.
// Go fills entry head % WWAP_RING (only while head - tail < WWAP_RING), then
// bumps head. C runs at most WWAP_CMD_PER_FRAME a frame, in order: it checks
// seq and check, looks the op up in the table ww_cmd_register filled, writes
// the result (never 0) into the entry, pushes a WWAP_EV_CMD event, then
// bumps tail. One writer per game: Go serialises its submits.
//
// +0x260..+0x300 reserved; +0x300..+0xF00 the co-op world region (docs/08
// §4.3, 0xC00), reserved for co-op M7's image and dead table.
#ifndef WW_WWOPS_H
#define WW_WWOPS_H

#define WWAP_ADDR               0x80414000  // region start (T2's budget ends here)
#define WWAP_SIZE               0xF00       // ends at MAILBOX_ADDR (0x80414F00)
#define WWAP_MAGIC              0x57574150  // 'WWAP'
#define WWAP_LAYOUT             1           // bump on any change below

#define WWAP_MAGIC_OFF          0x00        // u32 C
#define WWAP_LAYOUT_OFF         0x04        // u16 C
#define WWAP_DIR_COUNT_OFF      0x06        // u16 C: directory entries in use
#define WWAP_DIR_OFF            0x08        // directory: {u32 tag, u16 off, u16 size}
#define WWAP_DIR_MAX            4           // directory slots
#define WWAP_DIR_ENTRY_SIZE     8

#define WWAP_STAT_DONE_OFF      0x28        // u32 C: commands run (any result)
#define WWAP_STAT_REFUSED_OFF   0x2C        // u32 C: commands whose result was not OK
#define WWAP_STAT_BADCHECK_OFF  0x30        // u32 C: commands with a bad seq or check byte
#define WWAP_STAT_UNKNOWN_OFF   0x34        // u32 C: commands naming an op nobody registered
#define WWAP_STAT_EVDROP_OFF    0x38        // u32 C: events dropped on a full ring

#define WWAP_EV_WRITE_OFF       0x40        // u32 C
#define WWAP_EV_READ_OFF        0x44        // u32 Go
#define WWAP_EV_RING_OFF        0x50        // event entries
#define WWAP_CMD_HEAD_OFF       0x150       // u32 Go
#define WWAP_CMD_TAIL_OFF       0x154       // u32 C
#define WWAP_CMD_RING_OFF       0x160       // command entries
#define WWAP_RING_END           0x260       // the rings end here
#define WWAP_COOP_OFF           0x300       // the co-op world region (reserved)
#define WWAP_COOP_SIZE          0xC00       // to WWAP_SIZE

#define WWAP_RING               16          // entries per ring
#define WWAP_ENTRY_SIZE         16          // bytes per entry
#define WWAP_CMD_PER_FRAME      2           // commands C runs a frame, at most
#define WWAP_MAX_OPS            32          // ops 1..31 (0 is never an op)

#define WWAP_E_KIND             0x00        // event: u16 kind
#define WWAP_E_SEQ              0x02        // event: u8 count & 0xFF
#define WWAP_E_CHECK            0x03        // event: u8 XOR of the other 15 bytes
#define WWAP_E_A                0x04        // event: u32 a
#define WWAP_E_B                0x08        // event: u32 b
#define WWAP_E_C                0x0C        // event: u32 c
#define WWAP_C_OP               0x00        // command: u8 op
#define WWAP_C_SEQ              0x01        // command: u8 count & 0xFF
#define WWAP_C_RESULT           0x02        // command: u8 result (C; 0 = not run yet)
#define WWAP_C_CHECK            0x03        // command: u8 XOR of op, seq, a, b, c (result left out)
#define WWAP_C_A                0x04        // command: u32 a
#define WWAP_C_B                0x08        // command: u32 b
#define WWAP_C_C                0x0C        // command: u32 c

#define WWAP_TAG_EVENTS         0x45565452  // 'EVTR' directory tag: the event ring
#define WWAP_TAG_COMMANDS       0x434D4451  // 'CMDQ' directory tag: the command queue
#define WWAP_TAG_COOP           0x434F4F50  // 'COOP' directory tag: the co-op world region

// Results (WWAP_C_RESULT). An op returns one of these; 0 is never a result.
#define WWAP_R_OK               1           // done
#define WWAP_R_BADCHECK         2           // seq or check byte wrong (C ran nothing)
#define WWAP_R_UNKNOWN          3           // no op registered under that number
#define WWAP_R_NOTFOUND         4           // the op's target does not exist
#define WWAP_R_MISMATCH         5           // the target is not what the command said
#define WWAP_R_REFUSED          6           // not now (no Link, the wrong state) or never (a player)
#define WWAP_R_FAILED           7           // the game call failed

// Core ops (src/core/ops.c). Mods take numbers from WWAP_OP_MOD_FIRST.
#define WWAP_OP_ECHO            1           // a, b, c come back as a WWAP_EV_ECHO event
#define WWAP_OP_KILL            2           // a = fpc_ProcID, b = proc name (s16 in the low half), c = setID or 0xFFFFFFFF for any
#define WWAP_OP_MOD_FIRST       16          // the first op a mod may register

// Core events (WWAP_E_KIND). Mods take kinds from WWAP_EV_MOD_FIRST.
#define WWAP_EV_BOOT            1           // main01_init ran: a = WWAP_LAYOUT
#define WWAP_EV_CMD             2           // a command ran: a = op | seq << 8 | result << 16, b = the command's a, c = the command's count
#define WWAP_EV_ECHO            3           // WWAP_OP_ECHO's a, b, c
#define WWAP_EV_MOD_FIRST       256         // the first kind a mod may push

#endif
