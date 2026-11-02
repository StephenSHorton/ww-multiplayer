import os,re,struct,subprocess
W=os.environ["WW_BIN"]+"/wwdev"
run=lambda *a: subprocess.run(list(a),capture_output=True,text=True).stdout
peek=lambda a,n:[int(m.group(1),16) for m in re.finditer(r": 0x([0-9A-F]{8})",run(W,"peek","%X"%a,str(n)))]
f=lambda u: struct.unpack(">f",struct.pack(">I",u))[0]
base=peek(0x803F6B44,4)[0]; print("table 0x%08X"%base)
for i in range(160):
    e=base+4+i*0x38; w=peek(e,0x38)
    b=b''.join(struct.pack(">I",x) for x in w)
    room=struct.unpack(">b",b[0x2C:0x2D])[0]
    if room==-1: continue
    x,y,z=struct.unpack(">fff",b[0:12])
    print("i %3d room %d pos %.0f %.0f %.0f save %d b2E 0x%02X kind %d b30 0x%02X flag 0x%02X b32 0x%02X%02X"%(i,room,x,y,z,b[0x2D],b[0x2E],b[0x2F],b[0x30],b[0x31],b[0x32],b[0x33]))
