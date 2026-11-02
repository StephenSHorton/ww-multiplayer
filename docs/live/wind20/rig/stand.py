"""stand.py (wind#47: draws the sword before R) <actor_hex> <secs> <guard 0|1> [dist=120]: keep this game's Link `dist` units in
front of the actor (from its facing, +0x206), facing it; re-placed every 2 s. guard=1 holds R
(input 0x0020 + the trigger poke 80413078 00FF0000, WW-MAC-RULES) the whole time."""
import math, os, re, struct, subprocess, sys, time
BIN = os.environ["WW_BIN"]; WWDEV, MAIN = BIN + "/wwdev", BIN + "/ww-multiplayer"
ACT = int(sys.argv[1], 16); SECS = float(sys.argv[2]); GUARD = sys.argv[3] == "1"
DIST = float(sys.argv[4]) if len(sys.argv) > 4 else 120.0
def run(*a): return subprocess.run(list(a), capture_output=True, text=True).stdout
def peek(addr, n): return [int(m.group(1), 16) for m in re.finditer(r": 0x([0-9A-F]{8})", run(WWDEV, "peek", "%X" % addr, str(n)))]
def f(u): return struct.unpack(">f", struct.pack(">I", u))[0]
T0 = time.time()
while time.time() - T0 < SECS:
    ap = [f(x) for x in peek(ACT + 0x1F8, 12)]
    ay = struct.unpack(">h", struct.pack(">H", peek(ACT + 0x204, 4)[0] & 0xFFFF))[0]
    th = ay * math.pi / 32768.0
    tx, tz = ap[0] + math.sin(th) * DIST, ap[2] + math.cos(th) * DIST
    face = (ay + 0x8000) & 0xFFFF
    link = peek(0x803CA754, 4)[0]
    lp = [f(x) for x in peek(link + 0x1F8, 12)]
    d = math.hypot(lp[0] - ap[0], lp[2] - ap[2])
    moved = d > 1.8 * DIST or abs(lp[1] - ap[1]) > 150
    if moved or time.time() - T0 < 0.5:
        run(MAIN, "warp", "%.1f" % tx, "%.1f" % ap[1], "%.1f" % tz)
    else:  # stay put, keep facing the actor
        face = int(round(math.atan2(ap[0] - lp[0], ap[2] - lp[2]) * 32768 / math.pi)) & 0xFFFF
    run(WWDEV, "poke-u32", "%X" % (link + 0x204), "%X" % face)
    run(WWDEV, "poke-u32", "%X" % (link + 0x20C), "%X" % face)
    if GUARD:  # R guards (CROUCH_DEFENSE 0x0C) only with the sword out; sheathed, R is CROUCH (0x0E)
        if moved or (peek(link + 0x3560, 4) or [0])[0] >> 16 == 0x0100:
            run(WWDEV, "input-release"); run(WWDEV, "input", "0x0200", "0", "0", "150"); time.sleep(0.5)
        run(WWDEV, "input", "0x0020", "0", "0", "0"); run(WWDEV, "poke-u32", "80413078", "00FF0000")
    procs = []
    for _ in range(int(os.environ.get("NS", "4"))):  # mCurProc (+0x31D8) NS times, 0.5 s apart
        time.sleep(float(os.environ.get("DT", "0.5"))); m = peek(ACT + 0x2BC, 4) or [0]; k = peek(ACT + 0x2060, 8) or [0, 0]; procs.append("%02X/%d m%X a%X.%X" % ((peek(link + 0x31D8, 4) or [0])[0], (peek(0x803C4C08, 4) or [0])[0] & 0xFFFF, m[0], k[0], k[1]))
    print("%5.1f actor %.0f %.0f %.0f ang %d Link d %.0f %s guard %d" % (time.time()-T0, ap[0], ap[1], ap[2], ay, d, "warped" if moved else "", GUARD) + " proc " + ",".join(procs), flush=True)
run(WWDEV, "input-release")
