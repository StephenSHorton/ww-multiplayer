import subprocess,os,math,time,re,struct,sys
W=os.environ["WW_BIN"]+"/wwdev"; tx,tz=float(sys.argv[1]),float(sys.argv[2])
out=subprocess.run([W,"peek","80ABBAFC","12"],capture_output=True,text=True).stdout
v=[struct.unpack(">f",bytes.fromhex(m))[0] for m in re.findall(r": 0x([0-9A-F]{8})",out)]
x,z=v[0],v[2]; n=int(math.hypot(tx-x,tz-z)/800)+1
for i in range(1,n+1):
    subprocess.run([W,"poke-vec3","80ABBAFC","%.1f"%(x+(tx-x)*i/n),"0","%.1f"%(z+(tz-z)*i/n)],capture_output=True); time.sleep(0.2)
print("moved",n,"steps from",round(x),round(z))
