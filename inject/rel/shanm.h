// shanm.h: the shared-animator windows of the remote-Link full body
// (shanm.c; ww-multiplayer-handoff/design/remote-link-stage4-plan.md D4-4,
// section 3.6). A port of ww-online's puppet_sharedanm.c (MIT, below)
// without the per-actor swap list: the full body opens a window only
// around playerInit, makeBgWait and playerDelete, and every shared word
// that changed inside it is written back on close, so outside the windows
// Link's shared J3DModelData holds the local Link's animator pointers only.
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
#ifndef WW_SHANM_H
#define WW_SHANM_H

#include "wwapi.h"
#include "wwremote.h"

// The table: one archive-heap block per boot at
// *(u32*)(MAILBOX_ADDR + WWREL_SHANM_OFF), never freed, WWREMOTE_SHANM_BYTES
// long. The header's first 0x24 bytes are the Go-visible WWREMOTE_SA_*
// fields; the rest of it is the REL's (every mutable variable lives here,
// so the module keeps .bss and .data empty).
#define SHANM_FIRST_MODEL_OFF 0x24       // u32 the first tracked J3DModelData (vtable re-checked per window)
#define SHANM_JOINT_NUM_OFF   0x28       // u32 joint fields tracked (cap SHANM_JOINTS_MAX)
#define SHANM_MAT_NUM_OFF     0x2C       // u32 material fields tracked (cap SHANM_MATS_MAX, capped to fit val)
#define SHANM_MODELS_OFF      0x30       // u32 model datas found at the last refresh
#define SHANM_DEPTH_OFF       0x34       // u8 open windows (no nesting: an inner pair is a no-op)
#define SHANM_LIVE_OFF        0x35       // u8 the open window snapshotted (the archive was alive)
#define SHANM_CAPPED_OFF      0x36       // u8 the last refresh capped the materials
#define SHANM_HDR_SIZE        0x60
#define SHANM_JOINTS_OFF      0x060      // u32* joint_addr[SHANM_JOINTS_MAX]: &joint0->mMtxCalc
#define SHANM_JOINTS_MAX      64
#define SHANM_MATS_OFF        0x160      // u32* mat_addr[SHANM_MATS_MAX]: &material->mMaterialAnm
#define SHANM_MATS_MAX        256
#define SHANM_VAL_OFF         0x560      // u32 val[SHANM_WORDS_MAX]: the window's snapshot
#define SHANM_WORDS_MAX       4224

// The validated table (magic, size and self pointer), or 0.
u8*  shanm_table(void);
// The table, allocated from the archive heap on first use; 0 if that
// failed (WWREMOTE_BERR_SHANM_ALLOC).
u8*  shanm_ensure(void);
// Re-collect the tracked fields from the "Link" archive (create and delete).
void shanm_refresh(void);
// Open a window: snapshot every tracked word.
void shanm_begin(void);
// Close it: write every changed word back; returns how many changed.
u32  shanm_end(void);
// Tracked words whose value lies in [lo, hi) or [lo2, hi2) (a deleted
// body's instance and its solid heap). Must be 0.
u32  shanm_scan(u32 lo, u32 hi, u32 lo2, u32 hi2);

#endif
