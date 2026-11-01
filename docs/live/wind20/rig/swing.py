"""swing.py <actor_hex> [swings=6] [dist=90] [dy=0]: warp this game's Link `dist`
units from the actor's live position (and dy above its y), facing it, then press B;
repeat. Ported from the PC's archive/s2v2-live/lc1_fight.py for the Mac rig (the
entitled tools in ~/ww-shared/bin). WW_DOLPHIN_PID selects the Dolphin.
Prints the actor's health byte (+0x284 hi16 & 0xFF, as lc1_fight) before each swing."""
import math, os, re, struct, subprocess, sys, time

BIN = os.environ.get("WW_BIN", os.path.expanduser("~/ww-shared/bin"))
WWDEV, MAIN = BIN + "/wwdev", BIN + "/ww-multiplayer"
ACT = int(sys.argv[1], 16)
SWINGS = int(sys.argv[2]) if len(sys.argv) > 2 else 6
DIST = float(sys.argv[3]) if len(sys.argv) > 3 else 90.0
DY = float(sys.argv[4]) if len(sys.argv) > 4 else 0.0
PLAYER_PTR = 0x803CA754

def run(*a):
    return subprocess.run(list(a), capture_output=True, text=True).stdout

def peek(addr, n):
    return [int(m.group(1), 16) for m in re.finditer(r": 0x([0-9A-F]{8})", run(WWDEV, "peek", "%X" % addr, str(n)))]

def f(u):
    return struct.unpack(">f", struct.pack(">I", u))[0]

T0 = time.time()
for i in range(SWINGS):
    link = peek(PLAYER_PTR, 4)[0]
    ap = [f(x) for x in peek(ACT + 0x1F8, 12)]
    lp = [f(x) for x in peek(link + 0x1F8, 12)]
    dx, dz = lp[0] - ap[0], lp[2] - ap[2]
    d = math.hypot(dx, dz) or 1.0
    tx, tz = ap[0] + dx / d * DIST, ap[2] + dz / d * DIST
    ang = int(round(math.atan2(ap[0] - tx, ap[2] - tz) * 32768 / math.pi)) & 0xFFFF
    hp = (peek(ACT + 0x284, 4) or [0])[0] >> 16 & 0xFF
    print("%6.1f swing %d: actor %08X at %.0f %.0f %.0f hp %d" % (time.time() - T0, i, ACT, ap[0], ap[1], ap[2], hp), flush=True)
    run(MAIN, "warp", "%.1f" % tx, "%.1f" % (ap[1] + DY), "%.1f" % tz)
    run(WWDEV, "poke-u32", "%X" % (link + 0x204), "%X" % ang)
    run(WWDEV, "poke-u32", "%X" % (link + 0x20C), "%X" % ang)
    run(WWDEV, "input", "0x0200", "0", "0", "120"); time.sleep(0.8)
run(WWDEV, "input-release")
