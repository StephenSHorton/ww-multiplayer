"""jump.py <actor_hex> [dist=250] [mode=jump|slash]: put Link `dist` in front of the actor (along -z or its facing), face it,
lock on (L: input 0x0040 + trigger poke FF000000), then A (jump attack) or B (slash) a few times."""
import math, os, re, struct, subprocess, sys, time
R = os.path.expanduser("~/ww-shared/rigs/wind87"); WWDEV, MAIN = R + "/wwdev", R + "/ww-multiplayer"
PIDS = open(R + "/pair.pids").read().strip().split(",")
assert os.environ.get("WW_DOLPHIN_PID") in PIDS, "WW_DOLPHIN_PID must be one of this rig's pids %s" % PIDS
ACT = int(sys.argv[1], 16); DIST = float(sys.argv[2]) if len(sys.argv) > 2 else 250.0
MODE = sys.argv[3] if len(sys.argv) > 3 else "jump"
def run(*a): return subprocess.run(list(a), capture_output=True, text=True).stdout
def peek(addr, n): return [int(m.group(1), 16) for m in re.finditer(r": 0x([0-9A-F]{8})", run(WWDEV, "peek", "%X" % addr, str(n)))]
def f(u): return struct.unpack(">f", struct.pack(">I", u))[0]
ap = [f(x) for x in peek(ACT + 0x1F8, 12)]
ay = struct.unpack(">h", struct.pack(">H", peek(ACT + 0x204, 4)[0] & 0xFFFF))[0]
th = ay * math.pi / 32768.0
tx, tz = ap[0] + math.sin(th) * DIST, ap[2] + math.cos(th) * DIST
print("actor", ["%.0f" % v for v in ap], "ang", ay, "-> Link to %.0f %.0f" % (tx, tz), flush=True)
run(MAIN, "warp", "%.1f" % tx, "%.1f" % (ap[1] + 20), "%.1f" % tz); time.sleep(0.6)
link = peek(0x803CA754, 4)[0]
lp = [f(x) for x in peek(link + 0x1F8, 12)]
face = int(round(math.atan2(ap[0] - lp[0], ap[2] - lp[2]) * 32768 / math.pi)) & 0xFFFF
for _ in range(3):
    run(WWDEV, "poke-u32", "%X" % (link + 0x204), "%X" % face); run(WWDEV, "poke-u32", "%X" % (link + 0x20C), "%X" % face)
print("Link at", ["%.0f" % v for v in lp], "d %.0f" % math.hypot(lp[0]-ap[0], lp[2]-ap[2]), flush=True)
run(WWDEV, "input", "0x0040", "0", "0", "0"); run(WWDEV, "poke-u32", "80415078", "FF000000"); time.sleep(0.5)
seq = os.environ.get("SEQ", "B,A,B,A").split(",")
for i, k in enumerate(seq):
    btn = 0x0240 if k == "B" else 0x0140
    run(WWDEV, "input", "0x%04X" % btn, "0", "0", "0"); run(WWDEV, "poke-u32", "80415078", "FF000000"); time.sleep(0.12)
    run(WWDEV, "input", "0x0040", "0", "0", "0"); run(WWDEV, "poke-u32", "80415078", "FF000000"); time.sleep(float(os.environ.get("GAP", "0.9")))
    hp = peek(ACT + 0x1F8, 4)
    print("press", i, "Link proc %02X" % ((peek(link + 0x31D8, 4) or [0])[0]), flush=True)
run(WWDEV, "input-release"); run(WWDEV, "poke-u32", "80415078", "00000000")
