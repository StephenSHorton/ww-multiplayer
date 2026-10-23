// em_mt.c: the Magtail row of the enemy mirror (kind 8, mt_class, proc
// 0xD9), batch D. Design: ww-multiplayer-handoff/design/enemy-mirror-
// batch-d.json R-D6, D8-D10, "magtail"; research/C1-magtail.md and
// C1b-magtail-redo.md. THE FIRST CUT (cut_order 1): everything MT lives
// here, under EMK_WITH_MT, plus one `#if EMK_WITH_MT` dispatch line per
// call site in enemy_mirror.c, EM_ROW(MT) and wwenemy_kinds.h's MT
// section; a cut sets EMK_WITH_MT 0 and EMK_KIND_COUNT 8 (emk_check_mt_count)
// and this unit compiles to nothing. Default off: Go sets kind bit 8 only
// for mt+u and a matching REL BuildID (D10).
//
// Batch C's cut row (archive/enemy-batch-c/mt-row-restore.patch, its
// enemy_mirror.c em_mt_* section) is restored here, gate, hits, adopt,
// consume, custody and respawn as they were (MT-1, MT-3 forwarding, MT-4,
// MT-5), but its hand-built body (em_mt_matrix, em_mt_colliders, em_mt_link,
// em_mt_snapshot and the fire particles) is gone: P0-MT found its sphere
// centres wrong (C1b section 1). The copy's body is now its own retail
// body_control1 (MOVE) or body_control2 (FIGHT), called in the loaded
// d_a_mt.rel behind a fingerprint, the BB tail_control precedent (R-14).
// Both are pure functions of the actor plus BG: they write all eight
// segment base matrices, set every sphere's centre and radius and enter
// all nine into cCcS (C1b section 2, 4). What a copy's paused execute no
// longer does around them, the body step does: the streamed head, the
// latch and the hit cooldown cleared, the sway clock ticked, the ring held
// on a stale sample (D9), the bite state cleared before FIGHT's builder,
// and the two mode edges. P0-MT2 verified the builders' model live (MOVE
// 0.000; FIGHT 0.000 but one 0.857 transition frame, archive/enemy-batch-d/
// P0.md). PINNED calls no builder (D8): body_control5 needs Execute's
// player follow (mt.rel 0x68F8-0x697C), so the REL keeps its collider
// effect instead.
//
// Offsets are the retail GZLE d_a_mt.rel's (SHA-1 9d7de862..., tww
// config.yml:1509-1510; the decomp has stubs only, d_a_mt.cpp:61-157),
// "mt.rel 0xNNNN" a .text offset; every name is wwenemy_kinds.h's EMK_MT_*.
#include "em_mt.h"

#if EMK_WITH_MT

// body_control1__FP8mt_class / body_control2__FP8mt_class (rels/d_a_mt/
// symbols.txt:18,30): void f(mt_class*), r3 = the actor (C++ EABI).
typedef void (*emt_builder_t)(void* a);

// The move_ad trail offsets per segment (.data 0x78, s32 in the REL). Its
// move_ad2 twin (.data 0x98) is picked while u8 +0xC00 != 0, which nothing
// in mt.rel ever stores (C1b check notes), so only move_ad is used.
static const s8 EMT_MOVE_AD[EMK_MT_SEGS] = { 0, -6, -12, -18, -24, -30, -36, -42 };

typedef char emt_check_rel[(EMK_MT_ID == EMK_KIND_MT && sizeof(EMK_MT_ARC) <= 4 && EMK_MT_MTX == EMK_MTX_MT && EMK_MT_COLL == EMK_COLL_MT &&
                            EMK_MT_ATTACH == EMK_ATTACH_NONE &&
                            !(EMK_MT_CAPS & (EMK_CAP_DR | EMK_CAP_TILT_CUR | EMK_CAP_MODEL_EQ)) &&
                            EMK_MT_TILT == EMK_AC_SHAPE_X && EMK_MT_SEGS == 8 && EMK_MT_RING_LEN == 64 &&
                            EMT_RING_MASK == 0x3F && EMK_HIT_MT_KILL > EMK_HIT_SPECIAL && EMK_HIT_MT_KILL < 0x100 &&
                            // a slot holds one kind: MT's kind_bits are BB's values
                            EM_XB_MT_FP_DONE == 0x04 && EM_XB_MT_FP_OK == 0x08 &&
                            !((EM_XB_MT_CUSTODY | EM_XB_MT_FRESH) & (EM_XB_MT_FP_DONE | EM_XB_MT_FP_OK)) &&
                            EM_XB_MT_CUSTODY != EM_XB_MT_FRESH && EM_XF_MT_BALL != EM_XF_ATTN &&
                            EMK_MT_MODE_MOVE == 0 && EMK_MT_MODE_FIGHT == 1 && EMK_MT_MODE_PINNED == 6 &&
                            EMK_MT_GATE + 1 == EMK_MT_ACTION && EMK_MT_COLOR_A + 4 == EMK_MT_COLOR_B &&
                            EMT_CC_SHAPE_CENTER == 0x118 && EMK_MT_BODY_SPH_CENTER == EMK_MT_BODY_SPH + EMT_CC_SHAPE_CENTER &&
                            EMK_MT_TG_CYL == EMK_MT_BODY_SPH &&
                            // flags2 bits 0-1 are MT's colour phase, which the S2
                            // readers of SF2_HAS_WEAPON / SF2_PLAY_SKIP / blur
                            // never see: no play_skip, blur or attach.
                            EMK_MT_F2_ATTN < WWENEMY_SF2_LIGHT && EMK_MT_F2_DIVE < WWENEMY_SF2_LIGHT &&
                            EMK_MT_PLAY_SKIP == 0 && EMK_MT_BLUR_ON == 0) ? 1 : -1];

static int emt_in_mem1(u32 p) {
    return p >= EMT_MEM1_LO && p < EMT_MEM1_HI;
}

static u8* em_mt_body(void* a, int i) {
    return (u8*)a + EMK_MT_BODY_SPH + i * EMK_MT_BODY_SPH_STRIDE;
}

// daMt_Execute through the copy's own profile (sub_method -> mpExecuteFunc,
// f_op_actor.h:274, f_pc_method.h:13, as em_bb_tc), or 0.
static u32 emt_exec(const void* a) {
    u32 sm = EMT_AF(a, u32, EMK_AC_SUB_METHOD);
    u32 exec;
    if (!emt_in_mem1(sm)) return 0;
    exec = EMT_AF(sm, u32, EMK_METHOD_EXECUTE);
    if (!emt_in_mem1(exec) || exec < EMT_MEM1_LO + EMK_MT_TEXT_EXEC) return 0;
    return exec;
}

static u32 emt_builder(const void* a, u32 text) {
    u32 exec = emt_exec(a);
    return exec ? exec - EMK_MT_TEXT_EXEC + text : 0;
}

// MT-1, em_validate's MT case (0 = ERR_GATE; the AM2 precedent): not
// hidden until its appear switch (0x2BB != 0: Execute returns before any
// logic, Draw checks it too, mt.rel 0x61DC-0x6218, 0x338C), in a mode the
// mirror builds (MOVE, FIGHT, PINNED: 0x6460-0x6538), no death flag
// (0x18F8, 0x747C-0x7498) or killed flag (0x1CBC, 0x7CF4-0x7DF8), not
// shrinking (0x1906, the light arrow, 0x5CD8-0x5D10), not carried. All the
// copy's own fields: em_mt_consume writes only modes the gate admits.
int em_mt_gate(void* a, const em_kind* k) {
    u8 mode = EMT_AF(a, u8, k->gate);
    if (EMT_AF(a, u8, EMK_MT_APPEAR) != 0) return 0;
    if (mode != EMK_MT_MODE_MOVE && mode != EMK_MT_MODE_FIGHT && mode != EMK_MT_MODE_PINNED) return 0;
    if (EMT_AF(a, s8, EMK_MT_DEATH) != 0 || EMT_AF(a, u8, EMK_MT_KILLED) != 0 ||
        EMT_AF(a, s8, k->ice + EMK_ICE_SHRINK) != 0)
        return 0;
    return !(EMT_AF(a, u32, EMK_AC_STATUS) & EMK_AC_STATUS_CARRY);
}

// R-D6: the retail GZLE d_a_mt.rel only. daMt_Execute's, body_control1's
// and body_control2's first four words and both builders' blr (offline:
// the retail REL; live: P0-MT2, fp=ok on all seven r8 Magtails). Checked
// once per pid (EM_XB_MT_FP_DONE; em_consume drops both bits on a pid
// change, em_adopt keeps them); a miss counts once in the block's
// saturating WWENEMY_FP_MISS_OFF (shared with BB's tail) and refuses the
// adoption: no copy runs without its body. Another revision (D44J01,
// GZLP01) fails closed.
int em_mt_fp_ok(void* a, em_ext_slot* xs) {
    u32 exec, bc1, bc2;
    int ok;
    if (xs->kind_bits & EM_XB_MT_FP_DONE) return (xs->kind_bits & EM_XB_MT_FP_OK) != 0;
    exec = emt_exec(a);
    bc1 = exec ? exec - EMK_MT_TEXT_EXEC + EMK_MT_TEXT_BC1 : 0;
    bc2 = exec ? exec - EMK_MT_TEXT_EXEC + EMK_MT_TEXT_BC2 : 0;
    ok = exec && emt_in_mem1(bc2) && emt_in_mem1(bc1 + EMK_MT_BC1_BLR_OFF) &&
         emt_in_mem1(bc2 + EMK_MT_BC2_BLR_OFF) && emt_in_mem1(exec + 0xC) &&
         EMT_AF(exec, u32, 0x0) == EMK_MT_FP_EXEC0 && EMT_AF(exec, u32, 0x4) == EMK_MT_FP_EXEC1 &&
         EMT_AF(exec, u32, 0x8) == EMK_MT_FP_EXEC2 && EMT_AF(exec, u32, 0xC) == EMK_MT_FP_EXEC3 &&
         EMT_AF(bc1, u32, 0x0) == EMK_MT_FP_BC1_0 && EMT_AF(bc1, u32, 0x4) == EMK_MT_FP_BC1_1 &&
         EMT_AF(bc1, u32, 0x8) == EMK_MT_FP_BC1_2 && EMT_AF(bc1, u32, 0xC) == EMK_MT_FP_BC1_3 &&
         EMT_AF(bc1, u32, EMK_MT_BC1_BLR_OFF) == EMK_MT_FP_BLR &&
         EMT_AF(bc2, u32, 0x0) == EMK_MT_FP_BC2_0 && EMT_AF(bc2, u32, 0x4) == EMK_MT_FP_BC2_1 &&
         EMT_AF(bc2, u32, 0x8) == EMK_MT_FP_BC2_2 && EMT_AF(bc2, u32, 0xC) == EMK_MT_FP_BC2_3 &&
         EMT_AF(bc2, u32, EMK_MT_BC2_BLR_OFF) == EMK_MT_FP_BLR;
    xs->kind_bits |= EM_XB_MT_FP_DONE | (ok ? EM_XB_MT_FP_OK : 0);
    if (!ok) {
        volatile u8* miss = (volatile u8*)(EMT_EB_ADDR + WWENEMY_FP_MISS_OFF);
        if (*miss != 0xFF) *miss = (u8)(*miss + 1);
    }
    return ok;
}

// Every hit record of the eye and / or the eight body spheres.
void em_mt_clr_hits(void* a, int eye, int body) {
    int i;
    if (eye) emt_ClrTgHit((u8*)a + EMK_MT_HEAD_SPH);
    if (body)
        for (i = 0; i < EMK_MT_SEGS; i++) emt_ClrTgHit(em_mt_body(a, i));
}

// MT-1 adopt: no lunge blur (u8 0x18D4: the br models draw while != 0,
// mt.rel 0x3028-0x3030), no hit from its own last frame on any sphere, At
// Set off on the eight body spheres once (body_control1 never writes the
// At SPrm, C1b "At bits"; FIGHT's sph0 is the body step's), and the ring
// (64) and chain (8) moved by the snap delta, so the first frame's
// segments trail from where the host's head is, not from the copy's spot.
// f470, the stretch, stays the copy's own (the builders ease it).
void em_mt_adopt(void* a, f32 dx, f32 dy, f32 dz) {
    f32* r = (f32*)((u8*)a + EMK_MT_RING_POS);
    f32* c = (f32*)((u8*)a + EMK_MT_CHAIN_POS);
    int i;
    EMT_AF(a, u8, EMK_MT_BLUR) = 0;
    em_mt_clr_hits(a, 1, 1);
    for (i = 0; i < EMK_MT_SEGS; i++) EMT_AF(em_mt_body(a, i), u32, EMT_CC_AT_SPRM) &= ~1u;
    for (i = 0; i < EMK_MT_RING_LEN * 3; i += 3) {
        r[i + 0] += dx;
        r[i + 1] += dy;
        r[i + 2] += dz;
    }
    for (i = 0; i < EMK_MT_SEGS * 3; i += 3) {
        c[i + 0] += dx;
        c[i + 1] += dy;
        c[i + 2] += dz;
    }
}

// R-D6 the mode edges (C1b-magtail-redo.md MT-2 (e)), never through the
// latch +0xC01 itself (it also flips the mode and calls anm_init):
//   MOVE -> FIGHT or PINNED: body_control1's engage snapshot (mt.rel
//     0x21D8-0x2218): chain[i] = ring[k], chain_ang[i] = ring_ang[k], k =
//     (head + move_ad[i]) & 63, the head being the ring entry the last
//     body_control1 wrote (idx - 1, or idx itself when +0x48E held the
//     advance, 0x2238-0x2258); and +0x48E 0, as the latch clears it
//     (0x228C);
//   FIGHT or PINNED -> MOVE: body_control2's reseed (0x1404-0x1524): for
//     each segment i >= 1 and j = 0..5, ring[(move_ad[i] + j) & 63] =
//     chain[i] + j * (chain[i-1] - chain[i]) / 5 with chain_ang[i]'s angle;
//     then idx 0 and +0x48E 0 (0x1558-0x1574).
// FIGHT <-> PINNED keeps the chain. PINNED's At/Co clears on the body
// spheres stay, as after vanilla's body_control5 (no mt.rel code sets body
// sph 1..7's Co again, mt.rel 0x2EFC-0x2F30).
void em_mt_edge(void* a, u8 from, u8 to) {
    f32* rpos = (f32*)((u8*)a + EMK_MT_RING_POS);
    s16* rang = (s16*)((u8*)a + EMK_MT_RING_ANGLE);
    f32* cpos = (f32*)((u8*)a + EMK_MT_CHAIN_POS);
    s16* cang = (s16*)((u8*)a + EMK_MT_CHAIN_ANGLE);
    int i, j, n;
    if (from == to) return;
    if (from == EMK_MT_MODE_MOVE) {
        u32 head = EMT_AF(a, u32, EMK_MT_RING_IDX) - (EMT_AF(a, s16, EMK_MT_DIVE) == 0 ? 1u : 0u);
        for (i = 0; i < EMK_MT_SEGS; i++) {
            u32 k = (head + (u32)(s32)EMT_MOVE_AD[i]) & EMT_RING_MASK;
            for (n = 0; n < 3; n++) {
                cpos[i * 3 + n] = rpos[k * 3 + n];
                cang[i * 3 + n] = rang[k * 3 + n];
            }
        }
        EMT_AF(a, s16, EMK_MT_DIVE) = 0;
    } else if (to == EMK_MT_MODE_MOVE) {
        for (i = 1; i < EMK_MT_SEGS; i++) {
            const f32* p = cpos + i * 3;
            const f32* q = cpos + (i - 1) * 3;
            for (j = 0; j < EMT_RESEED_STEPS; j++) {
                u32 k = (u32)((s32)EMT_MOVE_AD[i] + j) & EMT_RING_MASK;
                f32 t = (f32)j;
                for (n = 0; n < 3; n++) {
                    rpos[k * 3 + n] = p[n] + t * ((q[n] - p[n]) / EMT_RESEED_DIV);
                    rang[k * 3 + n] = cang[i * 3 + n];
                }
            }
        }
        EMT_AF(a, u32, EMK_MT_RING_IDX) = 0;
        EMT_AF(a, s16, EMK_MT_DIVE) = 0;
    }
}

// em_consume_owned's MT case. Every consume: slot +0x18 (off is the slot's
// block offset) is the host's head wiggle +0x468 (G-D6), which body_control1
// pushes into the ring and both builders turn the head by. With the annex
// matched (aux its mailbox offset): aux[0] mode | action << 8, written only
// for a mode the gate admits (MOVE, FIGHT, PINNED: a host curling is
// EM_XF_MT_BALL, its death SF_DYING; either way the copy keeps its last
// mode), its edge first (em_mt_edge, unless EMK_MT_HKD_CHAIN; other
// Magtails' mt_check counts FIGHT copies, mt.rel 0x02D8-0x038C); aux[1] the
// ball timer 0x466; aux[3] shape_angle.z; unless EMK_MT_HKD_EXTRA the
// colour phase (flags2 bits 0-1) and the s32 btk/brk frames 0x2E8/0x2EC
// from aux[2] (Draw enters them, mt.rel 0x3464-0x3574).
void em_mt_consume(const em_slot* sl, void* a, u32 off, u32 aux, int annex, u8 hkd) {
    u16 w0, w2;
    u8 mode;
    EMT_AF(a, s16, EMK_MT_WIGGLE) = EMT_EB_S16(off + WWENEMY_SL_CC_M316Z_OFF);
    if (!annex) return;
    w0 = (u16)EMT_AX_S16(aux + 0);
    w2 = (u16)EMT_AX_S16(aux + 4);
    mode = (u8)w0;
    if (mode == EMK_MT_MODE_MOVE || mode == EMK_MT_MODE_FIGHT || mode == EMK_MT_MODE_PINNED) {
        if (!(hkd & EMK_MT_HKD_CHAIN)) em_mt_edge(a, EMT_AF(a, u8, EMK_MT_GATE), mode);
        EMT_AF(a, u8, EMK_MT_GATE) = mode;
        EMT_AF(a, u8, EMK_MT_ACTION) = (u8)(w0 >> 8);
    }
    EMT_AF(a, s16, EMK_MT_BALL_TIMER) = EMT_AX_S16(aux + 2);
    EMT_AF(a, s16, EMK_AC_SHAPE_Z) = EMT_AX_S16(aux + 6);
    if (!(hkd & EMK_MT_HKD_EXTRA)) {
        EMT_AF(a, u8, EMK_MT_COLOR_PHASE) = sl->tgt_flags2 & EMK_MT_F2_PHASE;
        EMT_AF(a, s32, EMK_MT_COLOR_A) = (u8)w2;
        EMT_AF(a, s32, EMK_MT_COLOR_B) = (u8)(w2 >> 8);
    }
}

// R-D6, em_slot_frame's step 5 for EMK_MTX_MT (in em_base_matrix's place)
// unless EMK_MT_HKD_MTX (no writes, no call: the copy freezes, proving the
// bit), and only with EM_XB_MT_FP_OK. The generic step has written the
// smoothed pos +0x1F8 and shape.y (the slot AngleY, the host's post-sway
// shape_angle.y: FIGHT's per-frame shape is current.angle + the sway +0x49A,
// Execute 0x6674-0x6748, so the host streams shape, not current); here
// shape.x is the smoothed tilt and shape.z stays the consumed aux[3], right
// before the builder, which reads +0x20C..+0x210 (0x1F68-0x1F78). Then what
// the copy's Execute did before its builder: the latch +0xC01 0, the hit
// cooldown +0x460 0 (only Execute counts it down, 0x63B8-0x63C8, and PINNED
// sets 5, 0x6994: left set, every body sphere would stay at R -200), the
// sway clock +0x46A + 1. MOVE: +0x48E = 1 when this frame brought no fresh
// sample (EM_XB_MT_FRESH clear) or the host dives (EMK_MT_F2_DIVE), else 0
// (D9: the ring holds on a repeat, so a stream gap does not compress the
// trail, C1b-magtail-redo.md:68), then body_control1. FIGHT: the bite state
// +0xC04 0 (a copy adopted at 2 would keep a live bite, at 1 an unhittable
// head: body_control2 0x1314-0x1374), then body_control2. PINNED (D8): no
// call; the matrices and centres hold (body_control5 keeps sph0's centre
// too, 0x2E9C-0x2F3C). A dying copy (the quiet death) calls nothing and
// enters no collider (as every row while dying): its eight segment models'
// base scale is the actor's times the death's shrink, which Draw's calc
// reads (updateDL, m_Do_ext.cpp:1488-1497).
void em_mt_body_step(const em_slot* sl, em_ext_slot* xs, void* a, u8 hkd, f32 scale, int dying) {
    u8 mode;
    u32 fn;
    int i;
    if (hkd & EMK_MT_HKD_MTX) return;
    if (dying) {
        for (i = 0; i < EMK_MT_SEGS; i++) {
            u32 morf = EMT_AF(a, u32, EMK_MT_MORF + i * 4);
            u32 model = emt_in_mem1(morf) ? EMT_AF(morf, u32, EMT_MORF_MODEL) : 0;
            if (!emt_in_mem1(model)) continue;
            EMT_AF(model, f32, EMT_J3D_BASE_SCALE + 0) = EMT_AF(a, f32, EMT_AC_SCALE + 0) * scale;
            EMT_AF(model, f32, EMT_J3D_BASE_SCALE + 4) = EMT_AF(a, f32, EMT_AC_SCALE + 4) * scale;
            EMT_AF(model, f32, EMT_J3D_BASE_SCALE + 8) = EMT_AF(a, f32, EMT_AC_SCALE + 8) * scale;
        }
        return;
    }
    if (!(xs->kind_bits & EM_XB_MT_FP_OK)) return;
    mode = EMT_AF(a, u8, EMK_MT_GATE);
    EMT_AF(a, s16, EMK_AC_SHAPE_X) = sl->cur_tilt;
    EMT_AF(a, s16, EMK_AC_SHAPE_Y) = sl->cur_angle;
    EMT_AF(a, u8, EMK_MT_LATCH) = 0;
    EMT_AF(a, s16, EMK_MT_HIT_COOL) = 0;
    EMT_AF(a, s16, EMK_MT_SWAY_FRAME) = (s16)(EMT_AF(a, s16, EMK_MT_SWAY_FRAME) + 1);
    if (mode == EMK_MT_MODE_MOVE) {
        EMT_AF(a, s16, EMK_MT_DIVE) =
            (!(xs->kind_bits & EM_XB_MT_FRESH) || (sl->tgt_flags2 & EMK_MT_F2_DIVE)) ? 1 : 0;
        fn = emt_builder(a, EMK_MT_TEXT_BC1);
        if (fn) ((emt_builder_t)fn)(a);
    } else if (mode == EMK_MT_MODE_FIGHT) {
        EMT_AF(a, u8, EMK_MT_BITE) = 0;
        fn = emt_builder(a, EMK_MT_TEXT_BC2);
        if (fn) ((emt_builder_t)fn)(a);
    }
    xs->kind_bits &= (u8)~EM_XB_MT_FRESH;
}

// R-D6/D8 em_colliders' EMK_COLL_MT case unless EMK_MT_HKD_COLL, after
// em_hits (off 0), or, with off 1, in its place while the slot's colliders
// are off (WWENEMY_FLAG_COLLIDERS clear or SF_HIDDEN). The builders entered
// all nine spheres into cCcS during the body step; cCcS::Set only lists
// the pointer (c_cc_s.cpp:42-59), and the frame's later cCcS::Move takes
// each shape's AABB and crosses them then (:405-441; a sphere pair crosses
// while rA + rB - dist > 0, c_m3d.cpp:1437-1438), so a radius written here
// still counts, and Set leaves the Tg hit records em_hits read alone.
// off: every sphere at the builders' own
// disabled R (-200). MOVE/FIGHT: At Set off on the eight body spheres
// (body_control2 rewrites sph0's every call, 0x12FC-0x1374: the bite is the
// host's, its PHIT hurts, never the copy); the host head's Shield bit
// (EMK_MT_F2_SHIELD: on in FIGHT, 0x682C-0x68A0) on body sph[0]; a pending
// hit withholds its collider at R -200 (all eight bodies for code 2, which
// em_hits clears together; the eye for code 1, and while the host is
// invulnerable). PINNED (D8, no builder ran): body_control5's collider
// effect, At and Co off on the eight body spheres and the eye left Set with
// Tg (mt.rel 0x2EFC-0x2F30), all nine entered here at their held centres
// unless withheld, as em_colliders' other rows do.
void em_mt_coll_post(const em_slot* sl, void* a, int off) {
    u8 mode = EMT_AF(a, u8, EMK_MT_GATE);
    u8* eye = (u8*)a + EMK_MT_HEAD_SPH;
    u8* s0 = em_mt_body(a, 0);
    int body_off = off || sl->hit_withheld == EMK_HIT_TG;
    int eye_off = off || sl->hit_withheld == EMK_HIT_HEAD || (sl->tgt_flags & WWENEMY_SF_INVULN);
    int i;
    if (mode == EMK_MT_MODE_PINNED) {
        for (i = 0; i < EMK_MT_SEGS; i++) {
            u8* sph = em_mt_body(a, i);
            EMT_AF(sph, u32, EMT_CC_AT_SPRM) &= ~1u;
            EMT_AF(sph, u32, EMT_CC_CO_SPRM) &= ~1u;
            if (!body_off) emt_cCcS_Set(EMT_CCS, sph);
        }
        if (!eye_off) emt_cCcS_Set(EMT_CCS, eye);
        return;
    }
    if (!off)
        EMT_AF(s0, u32, EMK_GTG_SPRM) = (EMT_AF(s0, u32, EMK_GTG_SPRM) & ~(u32)EMK_GTG_SPRM_SHIELD) |
                                        ((sl->tgt_flags2 & EMK_MT_F2_SHIELD) ? EMK_GTG_SPRM_SHIELD : 0);
    for (i = 0; i < EMK_MT_SEGS; i++) {
        u8* sph = em_mt_body(a, i);
        EMT_AF(sph, u32, EMT_CC_AT_SPRM) &= ~1u;
        if (body_off) EMT_AF(sph, f32, EMT_CC_SHAPE_RADIUS) = EMT_SPH_OFF_R;
    }
    if (eye_off) EMT_AF(eye, f32, EMT_CC_SHAPE_RADIUS) = EMT_SPH_OFF_R;
}

// A Tg hit whose attacker collider belongs to a Magtail (the client's own
// carried ball thrown at the copy: damage_check's type 6, mt.rel
// 0x5D58-0x5D98) is not forwarded (batch C design mt "gaps": not modelled).
static int em_mt_thrown(const u8* sph) {
    u32 obj = EMT_AF(sph, u32, EMT_CC_TG_HIT_OBJ);
    u32 stts = emt_in_mem1(obj) ? EMT_AF(obj, u32, EMT_CC_STTS) : 0;
    u32 owner = emt_in_mem1(stts) ? EMT_AF(stts, u32, EMT_CC_STTS_ACTOR) : 0;
    return emt_in_mem1(owner) && (u16)EMT_AF(owner, s16, EMT_AC_PROC_NAME) == EMK_MT_PROC;
}

// MT-3, em_hits' MT case: the eye (EMK_HIT_HEAD) or the first body sphere
// with a Tg hit (EMK_HIT_TG; Go's mtHitCollider finds it by its hit object),
// a thrown Magtail's hit dropped. The rest of the same swing is cleared
// (every other body sphere and the eye), so the reported sphere is the only
// one with a hit object while it is pending; nothing reported: all cleared.
int em_mt_hit_collider(em_slot* sl, void* a) {
    u8* eye = (u8*)a + EMK_MT_HEAD_SPH;
    int i, hit = -1;
    (void)sl;
    if ((EMT_AF(eye, u32, EMT_CC_TG_RPRM) & 1) && !em_mt_thrown(eye)) {
        em_mt_clr_hits(a, 0, 1);
        return EMK_HIT_HEAD;
    }
    for (i = 0; i < EMK_MT_SEGS && hit < 0; i++) {
        u8* sph = em_mt_body(a, i);
        if ((EMT_AF(sph, u32, EMT_CC_TG_RPRM) & 1) && !em_mt_thrown(sph)) hit = i;
    }
    emt_ClrTgHit(eye);
    for (i = 0; i < EMK_MT_SEGS; i++)
        if (i != hit) emt_ClrTgHit(em_mt_body(a, i));
    return hit < 0 ? 0 : EMK_HIT_TG;
}

// Host, em_hit_vulnerable's MT case: the hit cooldown (5 after any hit,
// while the body spheres sit at R -200, mt.rel 0x21A0-0x21C4; C1 "timers")
// is over. The row has no invuln word (EMK_MT_INVULN 0).
int em_mt_vulnerable(void* a) {
    return EMT_AF(a, s16, EMK_MT_HIT_COOL) == 0;
}

// Host, em_hit_center's MT case: collider 2 at body sph[EMK_MT_HIT_CENTER_SEG]'s
// centre (the head sphere clanks off its FIGHT Shield bit).
void em_mt_hit_center(void* a, cXyz* c) {
    const u8* ctr = em_mt_body(a, EMK_MT_HIT_CENTER_SEG) + EMT_CC_SHAPE_CENTER;
    c->x = EMT_AF(ctr, f32, 0);
    c->y = EMT_AF(ctr, f32, 4);
    c->z = EMT_AF(ctr, f32, 8);
}

// MT-4, em_handback's MT case: every sphere's hit cleared and the lunge blur
// off; then, when the host has curled (EM_XF_MT_BALL) and EMK_MT_HKD_CUSTODY
// is clear, the copy curls on this game (the BALL entry of FIGHT sub 20's
// landing, mt.rel 0x4E44-0x4F34): mode BALL, action 0, the host's ball timer
// (aux[1]), colour phase 1 and frames (aux[2]), the host's health (when
// streamed), eye HP 0, the head's Shield bit off, 0x478/0x48A/0x330 0, and
// the slot watches it (EM_XB_MT_CUSTODY). Otherwise, and always when the
// release is a reassignment (same 0: xs, the annex aux and the slot health
// are already the next actor's, em_consume), it resumes its last streamed
// mode and action (em_mt_consume).
#define EMT_M330 0x330   // f32, 0 at the curl (mt.rel 0x4E80)
#define EMT_M478 0x478   // f32, 0 at the curl (0x4E74)
#define EMT_M48A 0x48A   // s16, 0 at the curl (0x4E7C)
void em_mt_handback(void* a, em_ext_slot* xs, u8 hkd, int slot, int same) {
    u32 aux = WWENEMY_ANNEX_OFF + slot * WWENEMY_ANNEX_AUX_SIZE;
    s8 hp = EMT_EB_S8(WWENEMY_SLOTS_OFF + slot * WWENEMY_SLOT_SIZE + WWENEMY_SL_HEALTH_OFF);
    u16 w2 = (u16)EMT_AX_S16(aux + 4);
    em_mt_clr_hits(a, 1, 1);
    EMT_AF(a, u8, EMK_MT_BLUR) = 0;
    if (!same || !(xs->flags_ext & EM_XF_MT_BALL) || (hkd & EMK_MT_HKD_CUSTODY)) return;
    EMT_AF(a, u8, EMK_MT_GATE) = EMK_MT_MODE_BALL;
    EMT_AF(a, u8, EMK_MT_ACTION) = 0;
    EMT_AF(a, s16, EMK_MT_BALL_TIMER) = EMT_AX_S16(aux + 2);
    EMT_AF(a, u8, EMK_MT_COLOR_PHASE) = 1;
    EMT_AF(a, s32, EMK_MT_COLOR_A) = (u8)w2;
    EMT_AF(a, s32, EMK_MT_COLOR_B) = (u8)(w2 >> 8);
    if (hp > 0) EMT_AF(a, s8, EMT_AC_HEALTH) = hp;
    EMT_AF(a, s8, EMK_MT_EYE_HP) = 0;
    EMT_AF(a, u32, EMK_MT_BODY_SHIELD_SPRM) &= ~(u32)EMK_GTG_SPRM_SHIELD;
    EMT_AF(a, f32, EMT_M478) = 0.0f;
    EMT_AF(a, s16, EMT_M48A) = 0;
    EMT_AF(a, f32, EMT_M330) = 0.0f;
    xs->kind_bits |= EM_XB_MT_CUSTODY;
}

// MT-4, em_slot_frame's refusal path: while the copy handed back curled is
// the slot's actor (same pid, EM_XB_MT_CUSTODY: em_consume drops it when
// the slot's pid changes, and re-adoption, once both have uncurled, zeroes
// kind_bits), the slot reports it as its actor (Go's readHit needs the
// status pid and actor), and its own death (mode DEAD, the bakuha flag
// 0x18F8 >= 2, or shrinking) once as a hit entry with collider
// EMK_HIT_MT_KILL (no Tg record; written when no other entry is pending). A
// copy that left BALL alive but is still refused (the host still curled)
// stays watched. Returns 1 while the slot watches one.
int em_mt_custody_watch(em_slot* sl, em_ext_slot* xs, void* a, int slot) {
    u32 off = WWENEMY_HITS_OFF + slot * WWENEMY_HIT_SIZE;
    if (!(xs->kind_bits & EM_XB_MT_CUSTODY)) return 0;
    sl->actor = (u32)a;
    if (EMT_AF(a, u8, EMK_MT_GATE) != EMK_MT_MODE_DEAD && EMT_AF(a, s8, EMK_MT_DEATH) < 2 &&
        EMT_AF(a, s8, EMK_MT_ICE + EMK_ICE_SHRINK) == 0)
        return 1;
    if (EMT_EB_U8(off + 3) != 0) return 1;
    xs->kind_bits &= (u8)~EM_XB_MT_CUSTODY;
    sl->hit_seq++;
    EMT_EB_U16(off + 0) = sl->hit_seq;
    EMT_EB_U8(off + 2) = EMK_HIT_MT_KILL;
    EMT_EB_U8(off + 3) = WWENEMY_HP_PENDING;
    return 1;
}

// MT-4 host, em_hit_apply's code-4 branch: the client's custody BALL died;
// the host's Magtail runs its own bakuha (flag 2 at the end of its next
// execute, mt.rel 0x747C-0x7498) only while it is still curled with no death
// flag. 0 = stale (it uncurled, or is dying already).
int em_mt_kill_apply(void* a) {
    if (EMT_AF(a, u8, EMK_MT_GATE) != EMK_MT_MODE_BALL || EMT_AF(a, s8, EMK_MT_DEATH) != 0) return 0;
    EMT_AF(a, s8, EMK_MT_DEATH) = 2;
    return 1;
}

// MT-5, before both fopAcM_delete sites (em_release's quiet-dying branch,
// em_die_quiet's last frame) unless EMK_MT_HKD_RESPAWN (inverted: set =
// skip): daMt_Delete re-creates a Magtail at home only when 0x1CBC != 0
// and its respawn bit 0x2B6 is set (mt.rel 0x7CF4-0x7DF8); its own deaths
// write it before their deletes (0x68A8-0x68F4, 0x041C-0x0454), so the
// copy respawns as the host's does.
void em_mt_mark_respawn(void* a, u8 hkd) {
    if (!(hkd & EMK_MT_HKD_RESPAWN) && EMT_AF(a, u8, EMK_MT_RESPAWN_BIT) != 0) EMT_AF(a, u8, EMK_MT_KILLED) = 1;
}

#endif
