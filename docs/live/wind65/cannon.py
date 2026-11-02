"""cannon.py <ship_hex_on_host_side_as_B_copy> [shots=3]: on B, face the boat (0x80ABB904) at the
copy, press X (raise), then X (fire); prints the copy's position, the angle and timings."""
import os, re, struct, subprocess, sys, time, math
BIN=os.environ["WW_BIN"]; W=BIN+"/wwdev"
T=int(sys.argv[1],16); N=int(sys.argv[2]) if len(sys.argv)>2 else 3; BOAT=0x80ABB904; LP=0x803CA754
run=lambda *a: subprocess.run(list(a),capture_output=True,text=True).stdout
peek=lambda a,n:[int(m.group(1),16) for m in re.finditer(r": 0x([0-9A-F]{8})",run(W,"peek","%X"%a,str(n)))]
f=lambda u: struct.unpack(">f",struct.pack(">I",u))[0]
def face():
    b=[f(x) for x in peek(BOAT+0x1F8,12)]; t=[f(x) for x in peek(T+0x1F8,12)]
    ang=int(math.degrees(math.atan2(t[0]-b[0],t[2]-b[2]))/360*65536)&0xFFFF
    for off in (0x204,0x20C):
        w=peek(BOAT+off,4)[0]; run(W,"poke-u32","%X"%(BOAT+off),"%X"%((w&0xFFFF0000)|ang))
    l=peek(LP,4)[0]
    for off in (0x204,0x20C):
        w=peek(l+off,4)[0]; run(W,"poke-u32","%X"%(l+off),"%X"%((w&0xFFFF0000)|ang))
    return b,t,ang,math.hypot(t[0]-b[0],t[2]-b[2])
t0=time.time()
for i in range(N):
    b,t,ang,d=face()
    print("%.1f shot %d: boat %.0f %.0f target %.0f %.0f angle 0x%04X dist %.0f"%(time.time()-t0,i,b[0],b[2],t[0],t[2],ang,d),flush=True)
    run(W,"input","0x0400","0","0","150"); time.sleep(1.2); face()
    run(W,"input","0x0400","0","0","150"); time.sleep(2.5)
