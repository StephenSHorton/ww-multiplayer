// The projectile events block in the mailbox slack (WWFX): other players'
// bombs, boat-cannon shots and arrows made real in this world (ww-online
// review idea 5; design: ww-multiplayer-handoff/archive/ww-online-review/
// DESIGN-big-items.md section (a), option A, world-only copies).
//
// Shared by the REL (inject/rel/fx.c, which reports the local Link's
// projectiles in the outbox and spawns the peers' from the inbox) and Go
// (wwapi/mailbox/fx.go mirrors every #define here, pinned by
// TestFxHeaderPins, which parses this file the way TestVisHeaderPins
// parses wwvis.h; mods/multiplayer/fxsync drains the outbox and fills the
// inbox). Keep every #define on one line as `#define NAME value //
// comment`, a plain decimal or hex number: no expressions, no negatives.
//
// Place: the top 0x2B0 bytes of the free slack, MAILBOX_ADDR + 0xE50
// (0x80413D50) up to the arena (+0x1100, 0x80414000; wwrefresh.h
// WWRF_SLACK_END), so the free span left below it (+0x990..+0xE50, the
// region probe's CanaryArenaHigh) stays in one piece for the next block.
// No T2 code reads or writes it. The span was inside CanaryArenaHigh when
// P0-L4 and P0-L5 armed it across their stage hops and reported the region
// intact (docs/TESTING.md, batch C and D); PR-1 there reruns it for this
// block.
//
// Owners:
//   +0x00 u32 send magic  Go (broadcast-pose): WWFX_MAGIC = report the local
//                         Link's projectiles in the outbox, else 0
//   +0x04 u32 recv magic  Go (puppet-sync): WWFX_MAGIC = spawn the inbox's
//                         events, else 0 (the inbox is then discarded)
//   +0x08 u32 out write   REL: events written to the outbox (the seq)
//   +0x0C u32 out read    Go: outbox events taken
//   +0x10 u32 in write    Go: events written to the inbox
//   +0x14 u32 in read     REL: inbox events taken
//   +0x18..+0x2F          REL: counters (wrap)
//   +0x30                 outbox: WWFX_RING events of WWFX_EV_SIZE (REL)
//   +0x170                inbox: WWFX_RING events of WWFX_EV_SIZE (Go)
// A ring's event n (0-based count) sits at entry n % WWFX_RING; the writer
// fills the entry first, then bumps its write word, and never runs more
// than WWFX_RING ahead of the reader's word. The slack is not zeroed at
// boot: the REL's first frame after a link takes Go's read words as its
// own (nothing old is sent, nothing old is spawned).
//
// Copies. A bomb copy is a real daBomb_c (d_a_bomb3.inc) made by
// fopAcM_fastCreate in the local Link's layer, with WWFX_BOMB_PRM_COPY in
// its params (a free bit: the params use state bits 0-7, cheapEff 16,
// angXZero 17 and version 31, d_a_bomb.h:149-160, and change_state keeps
// every other bit, d_a_bomb_static.cpp:103-108), field_0x6F0 cleared (else
// its explode and delete call the local Link's decrementBombCnt,
// d_a_bomb3.inc:846-848, :1274) and VsEnemy | VsPlayer cleared in its At
// SPrm (option A: it breaks walls, lights torches, hits switches and sets
// off bombs, never hurts an enemy or a player; enemy damage stays on the
// host-authoritative relay, so no hit counts twice). An arrow copy is a
// real daArrow_c, detached (no parent), found only by the REL's own pid
// table: an arrow's params are its state (d_a_arrow.cpp:602), so it has
// no free bit.
#ifndef WW_WWFX_H
#define WW_WWFX_H

#define WWFX_OFF                0xE50       // block start, from MAILBOX_ADDR (0x80413D50)
#define WWFX_SIZE               0x2B0       // ends +0x1100, the arena (WWRF_SLACK_END)

#define WWFX_SEND_MAGIC_OFF     0x00        // u32 Go (broadcast-pose): WWFX_MAGIC = report, 0 = off
#define WWFX_RECV_MAGIC_OFF     0x04        // u32 Go (puppet-sync): WWFX_MAGIC = spawn, 0 = off
#define WWFX_OUT_WRITE_OFF      0x08        // u32 REL: outbox events written
#define WWFX_OUT_READ_OFF       0x0C        // u32 Go: outbox events taken
#define WWFX_IN_WRITE_OFF       0x10        // u32 Go: inbox events written
#define WWFX_IN_READ_OFF        0x14        // u32 REL: inbox events taken
#define WWFX_STAT_SPAWNED_OFF   0x18        // u32 REL: copies made (bombs, cannonballs, arrows)
#define WWFX_STAT_REJECTED_OFF  0x1C        // u32 REL: inbox events refused (bad values, no free entry, create failed)
#define WWFX_STAT_DROPPED_OFF   0x20        // u32 REL: outbox events lost (Go not draining)
#define WWFX_STAT_LOCAL_OFF     0x24        // u32 REL: copies this world took over (carried, swallowed, fuse set lower)
#define WWFX_STAT_SWALLOW_OFF   0x28        // u32 REL: copies an Armos Knight mirror swallowed, not reported (enemy_mirror.c)
#define WWFX_STAT_CLEARED_OFF   0x2C        // u32 REL: copies deleted at a stage change
#define WWFX_OUT_RING_OFF       0x30        // outbox events
#define WWFX_IN_RING_OFF        0x170       // inbox events

#define WWFX_MAGIC              0x57574658  // 'WWFX'
#define WWFX_RING               8           // events per ring
#define WWFX_EV_SIZE            0x28        // bytes per event

// Event (WWFX_EV_SIZE bytes, big-endian like the game).
#define WWFX_EV_KIND            0x00        // u8 WWFX_KIND_*
#define WWFX_EV_SLOT            0x01        // u8 inbox: the sender's player id; outbox: 0
#define WWFX_EV_VARIANT         0x02        // u8 an arrow's type (WWFX_ARROW_*), else 0
#define WWFX_EV_ROOM            0x03        // s8 the projectile's own current.roomNo
#define WWFX_EV_ID              0x04        // u32 the sender's projectile (its process id)
#define WWFX_EV_POS             0x08        // f32 x, y, z: current.pos
#define WWFX_EV_SPEED_F         0x14        // f32 speedF
#define WWFX_EV_SPEED_Y         0x18        // f32 speed.y
#define WWFX_EV_GRAVITY         0x1C        // f32 gravity
#define WWFX_EV_ANGLE           0x20        // s16 x, y: current.angle; z: shape_angle.z
#define WWFX_EV_TIMER           0x26        // s16 a bomb's mRestTime, a cannonball's mNoGravityTime, else 0

#define WWFX_KIND_BOMB_THROW    1           // a hand bomb left the sender's hands (thrown or put down)
#define WWFX_KIND_BOMB_PICKUP   2           // the sender carries it again: the copy goes
#define WWFX_KIND_EXPLODE       3           // the sender's bomb or cannonball exploded
#define WWFX_KIND_REMOVE        4           // it is gone without exploding (sank, fell out of the world)
#define WWFX_KIND_CANNON        5           // the boat cannon fired this cannonball
#define WWFX_KIND_ARROW         6           // an arrow left the sender's bow
#define WWFX_KIND_MAX           6

#define WWFX_ARROW_MAX          3           // daArrow_c TYPE_NORMAL 0, FIRE 1, ICE 2, LIGHT 3 (d_a_arrow.h:15-20)

// The copy marker in a bomb copy's params (see the top). enemy_mirror.c's
// Armos Knight swallow reads it too.
#define WWFX_BOMB_PRM_COPY      0x01000000

#endif
