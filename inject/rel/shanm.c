// shanm.c: see shanm.h. A port of ww-online's GameMod/src/puppet_link/
// puppet_sharedanm.c (MIT notice below) with the per-actor swap list
// removed and every variable moved into the archive-heap table's header.
//
// Tracked words (every J3DModelData of the "Link" archive: the BDL, BDLC,
// BDLI and BDLM index ranges of GZLE01's Link.arc):
//   - joint 0's J3DJoint::mMtxCalc (+0x58), one per model;
//   - each material's J3DMaterial::mMaterialAnm (+0x3C) and, when that is
//     set, the J3DMaterialAnm's 26 slots +0x04..+0x68.
// ww-online counted 2598 words live (152 materials, 92 with an anm, 54
// joint 0s); SHANM_WORDS_MAX 4224 is the cap if every material had one.
// A window costs ~23k instructions (flattened tables, unrolled 26-word
// copy and xor compare); the full body opens one only at create and
// delete.
//
// MIT License
//
// Copyright (c) 2026 datstarkey
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

#include "mailbox_core.h"
#include "wwrel.h"
#include "shanm.h"
#include "dapy.h"

typedef char shanm_check_layout[(SHANM_JOINTS_OFF == SHANM_HDR_SIZE &&
                                 SHANM_MATS_OFF == SHANM_JOINTS_OFF + 4 * SHANM_JOINTS_MAX &&
                                 SHANM_VAL_OFF == SHANM_MATS_OFF + 4 * SHANM_MATS_MAX &&
                                 SHANM_VAL_OFF + 4 * SHANM_WORDS_MAX == WWREMOTE_SHANM_BYTES &&
                                 WWREMOTE_SA_JOINT_DIFF_OFF + 4 <= SHANM_FIRST_MODEL_OFF &&
                                 SHANM_CAPPED_OFF < SHANM_HDR_SIZE) ? 1 : -1];

#define SA_U32(t, off)  (*(volatile u32*)((t) + (off)))
#define SA_U8(t, off)   (*(volatile u8*)((t) + (off)))
#define SA_PTR(t, off)  ((u32**)((t) + (off)))

// A MEM1 address (48 MB), word aligned.
#define SHANM_OK(p)     ((u32)(p) - 0x80000000u < 0x03000000u && ((u32)(p) & 3) == 0)

// Link.h (zeldaret/tww assets): dRes_INDEX_LINK_BDL_* of the BDL, BDLC,
// BDLI and BDLM folders (cl.bdl = 0x18).
static const u8 l_shanm_ranges[4][2] = { { 0x13, 0x29 }, { 0x2C, 0x2F }, { 0x32, 0x34 }, { 0x37, 0x4E } };
static const char l_shanm_arc[] = "Link";
#define SHANM_PROBE_INDEX LINK_BDL_CL

#define SHANM_REP26(X) X(0) X(1) X(2) X(3) X(4) X(5) X(6) X(7) X(8) X(9) X(10) X(11) X(12) \
    X(13) X(14) X(15) X(16) X(17) X(18) X(19) X(20) X(21) X(22) X(23) X(24) X(25)

u8* shanm_table(void) {
    u32 t = *(volatile u32*)(MAILBOX_ADDR + WWREL_SHANM_OFF);
    if (!SHANM_OK(t) || (t & 0x1F) != 0) return 0;
    if (SA_U32((u8*)t, WWREMOTE_SA_MAGIC_OFF) != WWREMOTE_SHANM_MAGIC ||
        SA_U32((u8*)t, WWREMOTE_SA_SIZE_OFF) != WWREMOTE_SHANM_BYTES ||
        SA_U32((u8*)t, WWREMOTE_SA_SELF_OFF) != t) return 0;
    return (u8*)t;
}

// Allocated once per boot from the archive heap (the minilink.c pattern:
// an explicit heap, never the current one) and never freed: deletes that
// run after the keeper is gone still find it. The header is zeroed, the
// magic written last, then the mailbox word.
u8* shanm_ensure(void) {
    u8* t = shanm_table();
    u32 i;
    if (t) return t;
    t = (u8*)JKRHeap_alloc(WWREMOTE_SHANM_BYTES, 0x20, mDoExt_getArchiveHeap());
    if (!SHANM_OK(t)) return 0;
    for (i = 0; i < SHANM_HDR_SIZE; i += 4) SA_U32(t, i) = 0;
    SA_U32(t, WWREMOTE_SA_SIZE_OFF) = WWREMOTE_SHANM_BYTES;
    SA_U32(t, WWREMOTE_SA_SELF_OFF) = (u32)t;
    SA_U32(t, WWREMOTE_SA_MAGIC_OFF) = WWREMOTE_SHANM_MAGIC;
    *(volatile u32*)(MAILBOX_ADDR + WWREL_SHANM_OFF) = (u32)t;
    return t;
}

static void* shanm_get_res(u32 index) {
    return dRes_getRes_byIdx(l_shanm_arc, (s32)index, MOBJECT_INFO, OBJECT_INFO_COUNT);
}

void shanm_refresh(void) {
    u8* t = shanm_table();
    u32** jf;
    u32** mf;
    u32 r, i, joints = 0, mats = 0, models = 0, first = 0;
    if (!t) return;
    jf = SA_PTR(t, SHANM_JOINTS_OFF);
    mf = SA_PTR(t, SHANM_MATS_OFF);
    SA_U8(t, SHANM_CAPPED_OFF) = 0;
    // One probe first: getRes logs an error per call while the archive is
    // not loaded.
    if (shanm_get_res(SHANM_PROBE_INDEX) != 0) {
        for (r = 0; r < 4; r++) {
            for (i = l_shanm_ranges[r][0]; i <= l_shanm_ranges[r][1] && models < SHANM_JOINTS_MAX; i++) {
                u8* data = (u8*)shanm_get_res(i);
                u32 mtab, jtab;
                u16 n, m;
                if (!SHANM_OK(data) || *(u32*)data != DAPY_VT_MODELDATA) continue;
                if (!first) first = (u32)data;
                models++;
                mtab = *(u32*)(data + J3D_MD_MATS);
                n = *(u16*)(data + J3D_MD_MAT_NUM);
                if (SHANM_OK(mtab)) {
                    for (m = 0; m < n && mats < SHANM_MATS_MAX; m++) {
                        u32 mat = ((u32*)mtab)[m];
                        if (SHANM_OK(mat)) mf[mats++] = (u32*)(mat + J3D_MAT_ANM);
                    }
                }
                jtab = *(u32*)(data + J3D_MD_JOINTS);
                if (*(u16*)(data + J3D_MD_JOINT_NUM) != 0 && SHANM_OK(jtab) && SHANM_OK(((u32*)jtab)[0]))
                    jf[joints++] = (u32*)(((u32*)jtab)[0] + J3D_JOINT_MTX_CALC);
            }
        }
    }
    // Worst case every material has an anm: cap once here, not per word.
    if (joints + mats * (1 + J3D_MATANM_SLOTS) > SHANM_WORDS_MAX) {
        mats = (SHANM_WORDS_MAX - joints) / (1 + J3D_MATANM_SLOTS);
        SA_U8(t, SHANM_CAPPED_OFF) = 1;
    }
    SA_U32(t, SHANM_JOINT_NUM_OFF) = joints;
    SA_U32(t, SHANM_MAT_NUM_OFF) = mats;
    SA_U32(t, SHANM_MODELS_OFF) = models;
    SA_U32(t, SHANM_FIRST_MODEL_OFF) = first;
}

// Snapshot every tracked word into val.
static void shanm_save(u8* t) {
    u32* v = (u32*)(t + SHANM_VAL_OFF);
    u32** f = SA_PTR(t, SHANM_JOINTS_OFF);
    u32 n;
    for (n = SA_U32(t, SHANM_JOINT_NUM_OFF); n != 0; n--) *v++ = **f++;
    f = SA_PTR(t, SHANM_MATS_OFF);
    for (n = SA_U32(t, SHANM_MAT_NUM_OFF); n != 0; n--) {
        u32 anm = **f++;
        *v++ = anm;
        if (SHANM_OK(anm)) {
            const u32* src = (const u32*)(anm + 4);
#define SHANM_COPY(i) v[i] = src[i];
            SHANM_REP26(SHANM_COPY)
#undef SHANM_COPY
            v += J3D_MATANM_SLOTS;
        }
    }
    SA_U32(t, WWREMOTE_SA_WORDS_OFF) = (u32)(v - (u32*)(t + SHANM_VAL_OFF));
}

// Write every word that differs from the snapshot back; count them. The
// material slots are followed through the SAVED anm pointer, so the slots
// visited are the local Link's even when the window replaced the anm.
static u32 shanm_restore(u8* t) {
    const u32* v = (const u32*)(t + SHANM_VAL_OFF);
    u32** f = SA_PTR(t, SHANM_JOINTS_OFF);
    u32 n, changed = 0;
    for (n = SA_U32(t, SHANM_JOINT_NUM_OFF); n != 0; n--, f++, v++) {
        if (**f != *v) {
            **f = *v;
            changed++;
        }
    }
    f = SA_PTR(t, SHANM_MATS_OFF);
    for (n = SA_U32(t, SHANM_MAT_NUM_OFF); n != 0; n--, f++) {
        u32 saved = *v++;
        if (**f != saved) {
            **f = saved;
            changed++;
        }
        if (SHANM_OK(saved)) {
            u32* dst = (u32*)(saved + 4);
            u32 diff = 0;
#define SHANM_XOR(i) diff |= dst[i] ^ v[i];
            SHANM_REP26(SHANM_XOR)
#undef SHANM_XOR
            if (diff != 0) {
                int i;
                for (i = 0; i < J3D_MATANM_SLOTS; i++) {
                    if (dst[i] != v[i]) {
                        dst[i] = v[i];
                        changed++;
                    }
                }
            }
            v += J3D_MATANM_SLOTS;
        }
    }
    return changed;
}

void shanm_begin(void) {
    u8* t = shanm_table();
    u32 first;
    if (!t) return;
    if (SA_U8(t, SHANM_DEPTH_OFF)++ != 0) return;
    SA_U32(t, WWREMOTE_SA_WINDOWS_OFF) = SA_U32(t, WWREMOTE_SA_WINDOWS_OFF) + 1;
    // The archive is resident; if it ever went away, touch none of it.
    first = SA_U32(t, SHANM_FIRST_MODEL_OFF);
    SA_U8(t, SHANM_LIVE_OFF) = (SHANM_OK(first) && *(u32*)first == DAPY_VT_MODELDATA) ? 1 : 0;
    if (SA_U8(t, SHANM_LIVE_OFF)) shanm_save(t);
}

u32 shanm_end(void) {
    u8* t = shanm_table();
    if (!t || SA_U8(t, SHANM_DEPTH_OFF) == 0) return 0;
    if (--SA_U8(t, SHANM_DEPTH_OFF) != 0) return 0;
    if (!SA_U8(t, SHANM_LIVE_OFF)) return 0;
    SA_U8(t, SHANM_LIVE_OFF) = 0;
    return shanm_restore(t);
}

static u32 shanm_in(u32 w, u32 lo, u32 hi, u32 lo2, u32 hi2) {
    return (w >= lo && w < hi) || (w >= lo2 && w < hi2);
}

u32 shanm_scan(u32 lo, u32 hi, u32 lo2, u32 hi2) {
    u8* t = shanm_table();
    u32** f;
    u32 n, first, hits = 0;
    int s;
    if (!t) return 0;
    first = SA_U32(t, SHANM_FIRST_MODEL_OFF);
    if (!SHANM_OK(first) || *(u32*)first != DAPY_VT_MODELDATA) return 0;
    f = SA_PTR(t, SHANM_JOINTS_OFF);
    for (n = SA_U32(t, SHANM_JOINT_NUM_OFF); n != 0; n--, f++) hits += shanm_in(**f, lo, hi, lo2, hi2);
    f = SA_PTR(t, SHANM_MATS_OFF);
    for (n = SA_U32(t, SHANM_MAT_NUM_OFF); n != 0; n--, f++) {
        u32 anm = **f;
        hits += shanm_in(anm, lo, hi, lo2, hi2);
        if (SHANM_OK(anm)) {
            for (s = 0; s < J3D_MATANM_SLOTS; s++) hits += shanm_in(((u32*)(anm + 4))[s], lo, hi, lo2, hi2);
        }
    }
    return hits;
}
