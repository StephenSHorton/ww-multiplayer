#!/usr/bin/env python3
"""crest.py <pid> [tries]: scripted sword hit on Helmaroc's crest (M2tower 30 0 3), Mac port of the PC's hammer/crest.sh idea.
Waits for the stuck beak (Bdk mAction 5, mState 5/6), warps Link 110 units from the head (Bdk +0x1150) on his own side,
turns him at it (+0x206 current.angle.y, +0x20E shape_angle.y), presses B. Every hit is a real swing; the warp stands in for walking."""
import math, os, re, struct, subprocess, sys, time
W = os.environ.get("WWDEV", os.path.expanduser("~/ww-shared/bin/wwdev")); pid = sys.argv[1]; tries = int(sys.argv[2]) if len(sys.argv) > 2 else 6
env = dict(os.environ, WW_DOLPHIN_PID=pid)
def run(*a): return subprocess.run([W, *a], env=env, capture_output=True, text=True).stdout
def word(addr): return int(run("peek", "%X" % addr, "4").split()[1], 16)
def poke(addr, v): run("poke-u32", "%X" % addr, "%08X" % (v & 0xFFFFFFFF))
def f32(addr): return struct.unpack(">f", struct.pack(">I", word(addr)))[0]
def pf(addr, x): poke(addr, struct.unpack(">I", struct.pack(">f", x))[0])
def probe():
    o = run("bdk-probe", "1"); m = re.search(r"bdk at (0x[0-9A-Fa-f]+).*?\+0x284 ([0-9A-F]{8}).*?mAction 0x([0-9A-F]+).*?mState (\d+)", o)
    return (int(m.group(1), 16), m.group(2), int(m.group(3), 16), int(m.group(4))) if m else None
hits = 0
for _ in range(400):
    p = probe()
    if not p: print("no Bdk"); break
    bdk, hp, act, st = p
    if act == 0xA: print("death: mAction 0xA"); break
    if act == 5 and st in (5, 6):
        hx, hy, hz = f32(bdk + 0x1150), f32(bdk + 0x1154), f32(bdk + 0x1158)
        link = word(0x803CA754); lx, ly, lz = f32(link + 0x1F8), f32(link + 0x1FC), f32(link + 0x200)
        d = math.hypot(lx - hx, lz - hz) or 1.0
        nx, nz = hx + (lx - hx) / d * 110, hz + (lz - hz) / d * 110
        pf(link + 0x1F8, nx); pf(link + 0x200, nz)
        ang = int(math.atan2(hx - nx, hz - nz) * 0x8000 / math.pi) & 0xFFFF
        for off in (0x204, 0x20C):
            w = word(link + off); poke(link + off, (w & 0xFFFF0000) | ang)
        run("input", "0x0200", "0", "0", "150"); hits += 1
        print(f"swing {hits}: head ({hx:.0f},{hy:.0f},{hz:.0f}) hp {hp} act 0x{act:X} st {st}")
        if hits >= tries: break
        time.sleep(1.5)
    else:
        time.sleep(0.3)
p = probe(); print("final:", p and ("hp %s mAction 0x%X mState %d" % (p[1], p[2], p[3])))
