// fullbody.h: the remote-Link full body (fullbody.c), the methods of the
// second profile g_profile_WWBODY (rel_main.c): a real daPy_lk_c that never
// runs a proc, posed every frame from the pose feed and hit through its own
// mCyl (ww-multiplayer-handoff/design/remote-link-stage4-plan.md D4-6,
// sections 3.3-3.8; a port of ww-online's GameMod/src/puppet_link/puppet.c
// create/delete recipe, MIT notice below). Created only by the keeper
// (remote.c rl_body_issue) when FLAG_FULL_BODIES is set and Go's heap gate
// allowed the slot; the tail (rl_full_tail, remote.h) follows sizeof
// (daPy_lk_c) at WWREMOTE_FULL_TAIL_OFF.
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
#ifndef WW_FULLBODY_H
#define WW_FULLBODY_H

#include "remote.h"

// The profile's methods (rel_main.c l_wwbody_method). create runs in
// fpcCt_Handler once per frame until it returns cPhs_COMPLEATE_e or
// cPhs_ERROR_e (the game then deletes the process); delete also runs after
// a create ERROR and is phase-safe; draw draws nothing.
int fullbody_create(void* actor);
int fullbody_delete(void* actor);
int fullbody_execute(void* actor);
int fullbody_draw(void* actor);

#endif
