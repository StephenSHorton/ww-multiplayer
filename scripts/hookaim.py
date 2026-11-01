#!/usr/bin/env python3
"""hookaim: aim the Grappling Hook at a KUI (hook target) by Link's own stick input.

Usage: WW_DOLPHIN_PID=<pid> scripts/hookaim.py [--wwdev PATH] [--range 1200] [--no-walk] [--no-throw]

1) walk: hold the stick toward the KUI (proc 0x00FC) until Link is within --range
   (horizontal) of it. Stick angle = target yaw - camera yaw, the camera yaw read
   back as m34E8 - m34DC (as wwdev face does).
2) aim: press Y (rope ready / first person), then steer with short stick pulses until
   shape_angle.y (+0x20E) and mBodyAngle.x (+0x2B4) point from Link's head at the KUI.
   The rope picks a KUI within a yaw cone and a pitch window around the sight line
   (d_a_himo2.cpp search_target), so close is enough.
3) throw: press Y.

Only input is written (wwdev input). Reads go through wwdev actors / peek.
"""
import argparse, math, os, re, subprocess, sys, time

OFF_POS = 0x1F8
OFF_SHAPE_Y = 0x20E
OFF_BODY_X = 0x2B4
OFF_STICK = 0x34DC
OFF_MOVE = 0x34E8
HEAD_Y = 100.0  # throwRope's origin: current.pos.y + 100

ap = argparse.ArgumentParser()
ap.add_argument("--wwdev", default=os.environ.get("WWDEV", "./bin/wwdev"))
ap.add_argument("--range", type=float, default=1200.0)
ap.add_argument("--no-walk", action="store_true")
ap.add_argument("--no-throw", action="store_true")
ap.add_argument("--yaw-tol", type=int, default=300)
ap.add_argument("--pitch-tol", type=int, default=400)
ap.add_argument("--timeout", type=float, default=12.0)
ap.add_argument("--target", type=float, nargs=3, metavar=("X", "Y", "Z"), help="aim at a fixed point instead of a KUI (testing)")
ap.add_argument("--no-sheathe", action="store_true")
args = ap.parse_args()
W = args.wwdev


def run(*a):
    return subprocess.run([W, *a], capture_output=True, text=True).stdout


def peek_words(addr, n):
    out = run("peek", hex(addr), str(n * 4))
    vals = re.findall(r"0x[0-9A-Fa-f]{8}:\s+0x([0-9A-Fa-f]{8})", out)
    if len(vals) < n:
        sys.exit(f"peek {hex(addr)} failed: {out.strip()}")
    return [int(v, 16) for v in vals]


def s16(v):
    v &= 0xFFFF
    return v - 0x10000 if v >= 0x8000 else v


def f32(v):
    import struct
    return struct.unpack(">f", v.to_bytes(4, "big"))[0]


def hi16(w):
    return s16(w >> 16)


def lo16(w):
    return s16(w)


def actors():
    out = run("actors")
    pl = kui = None
    for line in out.splitlines():
        m = re.search(r"(0x8[0-9A-F]{7})\s+pid\s+\d+\s+proc (0x[0-9A-F]{4}).*\(\s*(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\)", line)
        if not m:
            continue
        a, proc = int(m.group(1), 16), m.group(2)
        if proc == "0x00A9":
            pl = a
        elif proc == "0x00FC":
            kui = a
    if pl is None or (kui is None and not args.target):
        sys.exit(f"need the player and a KUI (0x00FC) in this room: player {pl} kui {kui}")
    return pl, kui


def pos(a):
    if a is None:
        return tuple(args.target)
    w = peek_words(a + OFF_POS, 3)
    return tuple(f32(x) for x in w)


def sangle(a, off):
    w = peek_words(a + (off & ~3), 1)[0]
    return hi16(w) if off % 4 == 0 else lo16(w)


def yaw_to(p, t):
    return s16(round(math.atan2(t[0] - p[0], t[2] - p[2]) * 32768 / math.pi))


def adiff(a, b):
    return s16(a - b)


def clamp(v, m=127):
    return int(max(-m, min(m, round(v))))


def inp(btn, x, y, ms):
    run("input", f"{btn:x}", str(x), str(y), str(ms))


def stick_for(a, mag):
    r = a * math.pi / 32768
    return clamp(-math.sin(r) * mag), clamp(math.cos(r) * mag)


LIFE = 0x803C4C08  # u16 max life, u16 current life (quarter hearts)


def life():
    return peek_words(LIFE, 1)[0] & 0xFFFF


def guard():
    """Stop on any damage: hand Link back to the player rather than walk on blind."""
    if life() < life0:
        inp(0, 0, 0, 30)
        sys.exit(f"stopped: Link took damage (life {life()} < {life0} quarter hearts)")


pl, kui = actors()
life0 = life()
print(f"player {pl:#x}  target {(hex(kui) if kui else args.target)}  life {life0}")

if not args.no_walk:
    cam = None
    t0 = time.time()
    while True:
        if time.time() - t0 > args.timeout:
            inp(0, 0, 0, 30)
            sys.exit("walk: timed out before reaching range")
        guard()
        p, k = pos(pl), pos(kui)
        d = math.hypot(k[0] - p[0], k[2] - p[2])
        if d <= args.range:
            print(f"walk: in range {d:.0f}")
            break
        ty = yaw_to(p, k)
        if cam is None:
            cam = adiff(sangle(pl, OFF_SHAPE_Y), 0)  # first guess: camera behind Link
        sx, sy = stick_for(adiff(ty, cam), 127)
        inp(0, sx, sy, 150)
        st, mv = sangle(pl, OFF_STICK), sangle(pl, OFF_MOVE)
        cam = adiff(mv, st)
        print(f"walk: d {d:.0f} target yaw {ty} cam {cam}")
    inp(0, 0, 0, 30)

def body():
    return sangle(pl, OFF_SHAPE_Y), sangle(pl, OFF_BODY_X)


def aiming():
    """Aim mode answers a stick-up with a negative body.x (clamped <= 0) and
    Link stays put; out of it the same stick walks him."""
    p0 = pos(pl)
    inp(0, 0, -60, 200)
    _, b = body()
    moved = math.dist(p0, pos(pl))
    inp(0, 0, 60, 200)  # level the view again
    return b < -300 and moved < 5, b, moved


# Y raises the hook only from idle with the sword away (oak, on A: a Y tap
# with the sword drawn showed Defend/Put Away). Y while aiming throws, so
# probe first.
guard()
ok_aim, b, moved = aiming()
if not ok_aim:
    if moved >= 5:
        print(f"not aiming (walked {moved:.0f}); raising the hook")
    if not args.no_sheathe:
        inp(0, 0, 0, 400)
    inp(0x800, 0, 0, 150)
    time.sleep(1.0)
    guard()
    ok_aim, b, moved = aiming()
    if not ok_aim:
        inp(0, 0, 0, 30)
        sys.exit(f"aim: not in aim mode after Y (body.x {b}, moved {moved:.0f}); stopping")
sgn_pitch = 1
y0, _ = body()
inp(0, 50, 0, 150)
y1, _ = body()
sgn_yaw = 1 if adiff(y1, y0) > 0 else -1
print(f"aim: in aim mode; stick +x turns yaw {adiff(y1, y0):+d}")

t0 = time.time()
ok = False
while time.time() - t0 < args.timeout:
    guard()
    p, k = pos(pl), pos(kui)
    eye =(p[0], p[1] + HEAD_Y, p[2])
    h = math.hypot(k[0] - eye[0], k[2] - eye[2])
    ty = yaw_to(eye, k)
    tp = s16(round(-math.atan2(k[1] - eye[1], h) * 32768 / math.pi))
    cy, cb = body()
    ey, ep = adiff(ty, cy), tp - cb
    print(f"aim: yaw err {ey:+6d} pitch err {ep:+6d} (body.x {cb}, want {tp}) d3 {math.hypot(h, k[1]-eye[1]):.0f}")
    if abs(ey) <= args.yaw_tol and abs(ep) <= args.pitch_tol:
        ok = True
        break
    # measured on B (Mac rig): stick y -80 for 300 ms took body.x 0 -> -4581
    sx = clamp(sgn_yaw * ey / 12, 100) if abs(ey) > args.yaw_tol else 0
    sy = clamp(sgn_pitch * ep / 12, 100) if abs(ep) > args.pitch_tol else 0
    if sx and abs(sx) < 30: sx = 30 * (1 if sx > 0 else -1)
    if sy and abs(sy) < 30: sy = 30 * (1 if sy > 0 else -1)
    inp(0, sx, sy, 50)

if not ok:
    sys.exit("aim: timed out (Link hit or out of aim mode?)")
if args.no_throw:
    print("aim: on target (not thrown)")
else:
    inp(0x800, 0, 0, 150)
    print("throw: Y")
