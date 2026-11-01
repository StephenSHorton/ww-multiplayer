"""behind.py <actor_hex> [swings=3] [dist=90] [btn=0x0200]: warp this game's Link `dist`
units behind the actor (from its facing angle, s16 at +0x206, as wind#18 placed B for
the Armos back-stab), face the actor's back, press btn; repeat. WW_DOLPHIN_PID picks the
Dolphin; WW_BIN the entitled tools (default ~/ww-shared/bin)."""
import math, os, re, struct, subprocess, sys, time

BIN = os.environ.get("WW_BIN", os.path.expanduser("~/ww-shared/bin"))
WWDEV, MAIN = BIN + "/wwdev", BIN + "/ww-multiplayer"
ACT = int(sys.argv[1], 16)
SWINGS = int(sys.argv[2]) if len(sys.argv) > 2 else 3
DIST = float(sys.argv[3]) if len(sys.argv) > 3 else 90.0
BTN = sys.argv[4] if len(sys.argv) > 4 else "0x0200"
PLAYER_PTR = 0x803CA754

def run(*a):
    return subprocess.run(list(a), capture_output=True, text=True).stdout

def peek(addr, n):
    return [int(m.group(1), 16) for m in re.finditer(r": 0x([0-9A-F]{8})", run(WWDEV, "peek", "%X" % addr, str(n)))]

def f(u):
    return struct.unpack(">f", struct.pack(">I", u))[0]

for i in range(SWINGS):
    ap = [f(x) for x in peek(ACT + 0x1F8, 12)]
    w = peek(ACT + 0x204, 4)[0]  # +0x204 angle x (hi), +0x206 angle y (lo)
    ay = struct.unpack(">h", struct.pack(">H", w & 0xFFFF))[0]
    th = ay * math.pi / 32768.0
    tx, tz = ap[0] - math.sin(th) * DIST, ap[2] - math.cos(th) * DIST  # behind = opposite its facing
    face = ay & 0xFFFF  # Link faces the way the actor faces: at its back
    hp = (peek(ACT + 0x284, 4) or [0])[0] >> 16 & 0xFF
    print("swing %d: actor %08X at %.0f %.0f %.0f angY %d hp %d" % (i, ACT, ap[0], ap[1], ap[2], ay, hp), flush=True)
    run(MAIN, "warp", "%.1f" % tx, "%.1f" % ap[1], "%.1f" % tz)
    link = peek(PLAYER_PTR, 4)[0]
    run(WWDEV, "poke-u32", "%X" % (link + 0x204), "%X" % face)
    run(WWDEV, "poke-u32", "%X" % (link + 0x20C), "%X" % face)
    run(WWDEV, "input", BTN, "0", "0", "120")
    time.sleep(float(os.environ.get("SWING_WAIT", "1.6")))
run(WWDEV, "input-release")
