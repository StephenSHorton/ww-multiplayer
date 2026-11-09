"""stab2.py <actor_hex> [tries=20] [dist=80]: warp behind the actor (its shape_angle.y, +0x20E), face its back, B at once; stop when its health drops."""
import math, os, re, struct, subprocess, sys, time
R = os.path.expanduser("~/ww-shared/rigs/wind87"); W, M = R + "/wwdev", R + "/ww-multiplayer"
assert os.environ.get("WW_DOLPHIN_PID") in open(R + "/pair.pids").read().strip().split(",")
ACT = int(sys.argv[1], 16); N = int(sys.argv[2]) if len(sys.argv) > 2 else 20; D = float(sys.argv[3]) if len(sys.argv) > 3 else 80.0
run = lambda *a: subprocess.run(list(a), capture_output=True, text=True).stdout
peek = lambda a, n: [int(x, 16) for x in re.findall(r": 0x([0-9A-F]{8})", run(W, "peek", "%X" % a, str(n)))]
f = lambda u: struct.unpack(">f", struct.pack(">I", u))[0]
link = peek(0x803CA754, 4)[0]
for i in range(N):
    w = peek(ACT + 0x1F8, 0x1C)  # pos 1F8..203, current.angle 204.., shape 20C..
    p = [f(x) for x in w[:3]]; a = w[5] & 0xFFFF  # +0x20C word: shape_angle.x/y -> low half is y (+0x20E)
    r = a * math.pi / 32768
    tx, tz = p[0] - math.sin(r) * D, p[2] - math.cos(r) * D
    run(M, "warp", "%.1f" % tx, "%.1f" % (p[1] + 5), "%.1f" % tz)
    run(W, "poke-u32", "%X" % (link + 0x20C), "%X" % a)
    run(W, "input", "0x0200", "0", "0", "100")
    time.sleep(0.5)
    hp = (peek(ACT + 0x8, 0)) if False else None
    out = run(W, "actors", "400")
    m = re.search(r"0x%08X .*?hp (\S+)" % ACT, out)
    print("try %d ang %04X hp %s" % (i, a, m.group(1) if m else "gone"), flush=True)
    if not m or not m.group(1).startswith(os.environ.get("HP", "2/")):
        break
run(W, "input-release")
