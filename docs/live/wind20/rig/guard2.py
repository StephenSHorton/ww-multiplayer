"""guard2.py <host_actor_hex> <secs> <guard 0|1> [dist=120]: B (the client) stands `dist` in front of
the HOST's actor (position and facing read on A), facing it. guard=1: draw the sword 600 units away
first (no swing near the actor), come back and hold R (CROUCH_DEFENSE 0x0C). Every 0.15 s logs the
host actor's attack fields (+0x2060/+0x2064) from A and B's mCurProc and life.
Env: WW_BIN, A, B (Dolphin PIDs)."""
import math, os, re, struct, subprocess, sys, time
BIN = os.environ["WW_BIN"]; A, B = os.environ["A"], os.environ["B"]
GP = os.environ.get("G", B)  # the guarding game: B (client), or G=$A for the vanilla baseline
ACT = int(sys.argv[1], 16); SECS = float(sys.argv[2]); GUARD = sys.argv[3] == "1"
DIST = float(sys.argv[4]) if len(sys.argv) > 4 else 120.0
def run(pid, tool, *a):
    return subprocess.run([BIN + "/" + tool] + list(a), capture_output=True, text=True, env=dict(os.environ, WW_DOLPHIN_PID=pid)).stdout
def peek(pid, addr, n): return [int(m.group(1), 16) for m in re.finditer(r": 0x([0-9A-F]{8})", run(pid, "wwdev", "peek", "%X" % addr, str(n)))]
f = lambda u: struct.unpack(">f", struct.pack(">I", u))[0]
def place(extra=0.0):
    ap = [f(x) for x in peek(A, ACT + 0x1F8, 12)]
    ay = struct.unpack(">h", struct.pack(">H", peek(A, ACT + 0x204, 4)[0] & 0xFFFF))[0]
    th = ay * math.pi / 32768.0; d = DIST + extra
    run(GP, "ww-multiplayer", "warp", "%.1f" % (ap[0] + math.sin(th) * d), "%.1f" % ap[1], "%.1f" % (ap[2] + math.cos(th) * d))
    link = peek(GP, 0x803CA754, 4)[0]; face = (ay + 0x8000) & 0xFFFF
    run(GP, "wwdev", "poke-u32", "%X" % (link + 0x204), "%X" % face); run(GP, "wwdev", "poke-u32", "%X" % (link + 0x20C), "%X" % face)
    return ap, link
T0 = time.time(); last = -9
while time.time() - T0 < SECS:
    ap, link = place(0.0) if time.time() - last > 4 else (None, peek(GP, 0x803CA754, 4)[0])
    if ap is not None:
        last = time.time()
        if GUARD:
            run(GP, "wwdev", "input-release")
            if peek(GP, link + 0x3560, 4)[0] >> 16 == 0x0100:  # sheathed: draw it far away
                place(600.0); run(GP, "wwdev", "input", "0x0200", "0", "0", "150"); time.sleep(0.6); place(0.0)
            run(GP, "wwdev", "input", "0x0020", "0", "0", "0"); run(GP, "wwdev", "poke-u32", "80413078", "00FF0000")
    k = peek(A, ACT + 0x2060, 8) or [0, 0]
    lp = [f(x) for x in peek(GP, link + 0x1F8, 12)]; hp = [f(x) for x in peek(A, ACT + 0x1F8, 12)]
    off = 0
    if os.environ.get("REFACE") == "1":  # keep B's Link (and shield) turned to the host actor
        face = int(round(math.atan2(hp[0] - lp[0], hp[2] - lp[2]) * 32768 / math.pi)) & 0xFFFF
        cur = peek(GP, link + 0x204, 4)[0] & 0xFFFF; off = ((face - cur + 0x8000) & 0xFFFF) - 0x8000
        run(GP, "wwdev", "poke-u32", "%X" % (link + 0x204), "%X" % face); run(GP, "wwdev", "poke-u32", "%X" % (link + 0x20C), "%X" % face)
    print("%5.2f host atk %X.%X hp %d | B proc %02X life %d d %.0f off %d" % (time.time() - T0, k[0], k[1],
          (peek(A, ACT + 0x284, 4) or [0])[0] >> 16 & 0xFF, peek(GP, link + 0x31D8, 4)[0],
          peek(GP, 0x803C4C08, 4)[0] & 0xFFFF, math.hypot(lp[0] - hp[0], lp[2] - hp[2]), off), flush=True)
    time.sleep(0.1)
run(GP, "wwdev", "input-release")
