"""lk.py [n=1] [dt=0.5]: this game's Link: pos, mCurProc (+0x31D8), mEquipItem (u16 +0x3560), life."""
import os, re, struct, subprocess, sys, time
W = os.environ["WW_BIN"] + "/wwdev"
def peek(a, n): return [int(m.group(1), 16) for m in re.finditer(r": 0x([0-9A-F]{8})", subprocess.run([W, "peek", "%X" % a, str(n)], capture_output=True, text=True).stdout)]
f = lambda u: struct.unpack(">f", struct.pack(">I", u))[0]
n = int(sys.argv[1]) if len(sys.argv) > 1 else 1; dt = float(sys.argv[2]) if len(sys.argv) > 2 else 0.5
for i in range(n):
    l = peek(0x803CA754, 4)[0]; p = [f(x) for x in peek(l + 0x1F8, 12)]
    print("pos %.0f %.0f %.0f proc %02X equip %04X life %d" % (p[0], p[1], p[2], peek(l + 0x31D8, 4)[0], peek(l + 0x3560, 4)[0] >> 16, peek(0x803C4C08, 4)[0] & 0xFFFF), flush=True)
    if i < n - 1: time.sleep(dt)
