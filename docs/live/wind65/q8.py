"""q8.py <copy_hex>: B swings at the Stalfos copy until it hides (m02b9=1 at +0x2B9),
then warps B onto the copy's spot and walks through it, sampling every ~0.12 s: m02b9,
sphere-1 (+0x678) Co SPrm/RPrm/HitObj (+0x2C/+0x30/+0x34), copy pos, Link pos."""
import os, re, struct, subprocess, sys, time, math
BIN = os.environ["WW_BIN"]; W, M = BIN + "/wwdev", BIN + "/ww-multiplayer"
C = int(sys.argv[1], 16); S = C + 0x678; LP = 0x803CA754
def run(*a): return subprocess.run(list(a), capture_output=True, text=True).stdout
def peek(a, n): return [int(m.group(1), 16) for m in re.finditer(r": 0x([0-9A-F]{8})", run(W, "peek", "%X" % a, str(n)))]
f = lambda u: struct.unpack(">f", struct.pack(">I", u))[0]
def hidden(): return (peek(C + 0x2B8, 4)[0] >> 16) & 0xFF
def cpos(): return [f(x) for x in peek(C + 0x1F8, 12)]
t0 = time.time()
for i in range(40):
    if hidden(): break
    p = cpos(); l = peek(LP, 4)[0]
    run(M, "warp", "%.1f" % (p[0] + 85), "%.1f" % p[1], "%.1f" % p[2])
    run(W, "poke-u32", "%X" % (l + 0x204), "%X" % 0xC000); run(W, "poke-u32", "%X" % (l + 0x20C), "%X" % 0xC000)
    run(W, "input", "0x0200", "0", "0", "120"); time.sleep(0.7)
print("hidden after %.1f s: m02b9=%d" % (time.time() - t0, hidden()), flush=True)
for k in range(10):
    q = cpos(); h = hidden()
    run(M, "warp", "%.1f" % q[0], "%.1f" % q[1], "%.1f" % q[2])
    time.sleep(0.15)
    co = peek(S + 0x2C, 12)
    lp = peek(LP, 4)[0]; lk = [f(x) for x in peek(lp + 0x1F8, 12)] if lp else [0, 0, 0]
    print("%5.2f m02b9 %d->%d co sprm 0x%X rprm 0x%X hitobj 0x%08X copy %.1f %.1f link %.1f %.1f d %.1f" % (time.time() - t0, h, hidden(), co[0], co[1], co[2], q[0], q[2], lk[0], lk[2], math.hypot(q[0]-lk[0], q[2]-lk[2])), flush=True)
