// Wind Waker Multiplayer — the C runtime's rings (src/core; co-op M7,
// wind#90; docs/api/00-design.md §6.6 "Later"). The API region at WWAP_ADDR
// (include/wwops.h): its header and directory, the C -> Go event ring
// (ww_event_push) and the Go -> C command queue (ww_cmd_register and the
// per-frame drain), with the core's own ops, ECHO and KILL.

#include "wwapi.h"
#include "mailbox_core.h"
#include "wwops.h"
#include "core.h"

_Static_assert(WWAP_ADDR + WWAP_SIZE == MAILBOX_ADDR, "the API region ends where the mailbox starts");
_Static_assert(WWAP_EV_RING_OFF + WWAP_RING * WWAP_ENTRY_SIZE <= WWAP_CMD_HEAD_OFF &&
               WWAP_CMD_RING_OFF + WWAP_RING * WWAP_ENTRY_SIZE == WWAP_RING_END &&
               WWAP_RING_END <= WWAP_COOP_OFF && WWAP_COOP_OFF + WWAP_COOP_SIZE == WWAP_SIZE &&
               WWAP_DIR_OFF + WWAP_DIR_MAX * WWAP_DIR_ENTRY_SIZE <= WWAP_STAT_DONE_OFF,
               "wwops.h's parts overlap");
_Static_assert((WWAP_RING & (WWAP_RING - 1)) == 0, "the rings index by count & (WWAP_RING - 1)");

#define AP8(off)  (*(volatile u8*)(WWAP_ADDR + (off)))
#define AP16(off) (*(volatile u16*)(WWAP_ADDR + (off)))
#define AP32(off) (*(volatile u32*)(WWAP_ADDR + (off)))

// The ops, by number. Filled at boot (ops_init and the modules' init), so a
// soft reset, which reloads the DOL and T2's .bss with it, starts empty.
static ww_cmd_fn cmd_table[WWAP_MAX_OPS];

static u8 xor_word(u32 w) {
    return (u8)(w ^ (w >> 8) ^ (w >> 16) ^ (w >> 24));
}

int ww_cmd_register(u32 op, ww_cmd_fn fn) {
    if (op == 0 || op >= WWAP_MAX_OPS || cmd_table[op])
        return 0;
    cmd_table[op] = fn;
    return 1;
}

int ww_event_push(u32 kind, u32 a, u32 b, u32 c) {
    if (AP32(WWAP_MAGIC_OFF) != WWAP_MAGIC)
        return 0;
    u32 w = AP32(WWAP_EV_WRITE_OFF);
    if (w - AP32(WWAP_EV_READ_OFF) >= WWAP_RING) {
        AP32(WWAP_STAT_EVDROP_OFF)++;
        return 0;
    }
    u32 e = WWAP_EV_RING_OFF + (w & (WWAP_RING - 1)) * WWAP_ENTRY_SIZE;
    u8 seq = (u8)w;
    AP16(e + WWAP_E_KIND) = (u16)kind;
    AP8(e + WWAP_E_SEQ) = seq;
    AP32(e + WWAP_E_A) = a;
    AP32(e + WWAP_E_B) = b;
    AP32(e + WWAP_E_C) = c;
    AP8(e + WWAP_E_CHECK) = (u8)((kind >> 8) ^ kind ^ seq ^ xor_word(a) ^ xor_word(b) ^ xor_word(c));
    AP32(WWAP_EV_WRITE_OFF) = w + 1;
    return 1;
}

// --- Core ops -------------------------------------------------------

static u32 op_echo(u32 a, u32 b, u32 c) {
    ww_event_push(WWAP_EV_ECHO, a, b, c);
    return WWAP_R_OK;
}

// KILL: delete one actor by its process ID, the way the actor puppets go
// (fopAcM_delete; the delete runs in the next fpcM_Management). The caller
// names what it expects (proc, and the setID unless c is 0xFFFFFFFF), and C
// reads it back: the ID may have been reused since Go looked. No save bit is
// set (onActor asserts on a bad zone, d_save.cpp:1676-1679): a caller that
// wants the kill to stick writes that itself. Never a player.
#define ACTOR_PROC_NAME(a) (*(volatile s16*)((u8*)(a) + 0x008))   // base mProcName (f_pc_base.h:16)
#define ACTOR_SET_ID(a)    (*(volatile u16*)((u8*)(a) + 0x1BC))   // setID (f_op_actor.h:278)
static u32 op_kill(u32 pid, u32 proc, u32 set_id) {
    fopAc_ac_c* act = 0;
    int i;
    if (!PLAYER_PTR_ARRAY[0])
        return WWAP_R_REFUSED;
    if (pid == fpcM_ERROR_PROCESS_ID_e || !fopAcM_SearchByID(pid, &act) || !act)
        return WWAP_R_NOTFOUND;
    for (i = 0; i < 3; i++)   // mpPlayerPtr[3]: Link, the companion, the ship
        if (PLAYER_PTR_ARRAY[i] == act)
            return WWAP_R_REFUSED;
    if (ACTOR_PROC_NAME(act) != (s16)proc || (set_id != 0xFFFFFFFFU && ACTOR_SET_ID(act) != (u16)set_id))
        return WWAP_R_MISMATCH;
    return fopAcM_deleteByID(pid) ? WWAP_R_OK : WWAP_R_FAILED;
}

// --- Boot and the per-frame drain -------------------------------------

// From main01_init: clear the region's header and rings, write the
// directory, register the core ops, then the magic (last: Go reads no
// half-made header) and the boot event.
void ops_init(void) {
    u32 off;
    for (off = 0; off < WWAP_RING_END; off += 4)
        AP32(off) = 0;
    AP16(WWAP_LAYOUT_OFF) = WWAP_LAYOUT;
    AP32(WWAP_DIR_OFF + 0x00) = WWAP_TAG_EVENTS;
    AP16(WWAP_DIR_OFF + 0x04) = WWAP_EV_WRITE_OFF;
    AP16(WWAP_DIR_OFF + 0x06) = WWAP_CMD_HEAD_OFF - WWAP_EV_WRITE_OFF;
    AP32(WWAP_DIR_OFF + 0x08) = WWAP_TAG_COMMANDS;
    AP16(WWAP_DIR_OFF + 0x0C) = WWAP_CMD_HEAD_OFF;
    AP16(WWAP_DIR_OFF + 0x0E) = WWAP_RING_END - WWAP_CMD_HEAD_OFF;
    AP32(WWAP_DIR_OFF + 0x10) = WWAP_TAG_COOP;
    AP16(WWAP_DIR_OFF + 0x14) = WWAP_COOP_OFF;
    AP16(WWAP_DIR_OFF + 0x16) = WWAP_COOP_SIZE;
    AP16(WWAP_DIR_COUNT_OFF) = 3;
    ww_cmd_register(WWAP_OP_ECHO, op_echo);
    ww_cmd_register(WWAP_OP_KILL, op_kill);
    AP32(WWAP_MAGIC_OFF) = WWAP_MAGIC;
    ww_event_push(WWAP_EV_BOOT, WWAP_LAYOUT, 0, 0);
}

// Every frame, Link or not (the ops check what they need): run at most
// WWAP_CMD_PER_FRAME queued commands, in order.
void ops_frame(void) {
    int n;
    for (n = 0; n < WWAP_CMD_PER_FRAME; n++) {
        u32 t = AP32(WWAP_CMD_TAIL_OFF);
        if (AP32(WWAP_CMD_HEAD_OFF) == t)
            return;
        u32 e = WWAP_CMD_RING_OFF + (t & (WWAP_RING - 1)) * WWAP_ENTRY_SIZE;
        u32 op = AP8(e + WWAP_C_OP);
        u8 seq = AP8(e + WWAP_C_SEQ);
        u32 a = AP32(e + WWAP_C_A), b = AP32(e + WWAP_C_B), c = AP32(e + WWAP_C_C);
        u32 r;
        if (seq != (u8)t || AP8(e + WWAP_C_CHECK) != (u8)(op ^ seq ^ xor_word(a) ^ xor_word(b) ^ xor_word(c))) {
            r = WWAP_R_BADCHECK;
            AP32(WWAP_STAT_BADCHECK_OFF)++;
        } else if (op >= WWAP_MAX_OPS || !cmd_table[op]) {
            r = WWAP_R_UNKNOWN;
            AP32(WWAP_STAT_UNKNOWN_OFF)++;
        } else {
            r = cmd_table[op](a, b, c);
            if (r == 0)
                r = WWAP_R_FAILED;
        }
        AP8(e + WWAP_C_RESULT) = (u8)r;
        AP32(WWAP_STAT_DONE_OFF)++;
        if (r != WWAP_R_OK)
            AP32(WWAP_STAT_REFUSED_OFF)++;
        ww_event_push(WWAP_EV_CMD, op | (u32)seq << 8 | r << 16, a, t);
        AP32(WWAP_CMD_TAIL_OFF) = t + 1;
    }
}
