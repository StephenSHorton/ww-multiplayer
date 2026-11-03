import sys, collections
snaps=[]; cur=None
for line in open(sys.argv[1]):
    if line.startswith("@"):
        cur=[float(line.split()[1]),{}]; snaps.append(cur)
    else:
        a,v=line.split(); cur[1][int(a.rstrip(":"),16)]=int(v,16)
base=min(snaps[0][1]); t0=snaps[0][0]
# the actor is alive while +0x1F8 x matches the first snapshot's
x0=snaps[0][1][base+0x1F8]
alive=[s for s in snaps if s[1].get(base+0x1F8)==x0 and s[1].get(base+0x8)==snaps[0][1][base+0x8]]
print("snaps",len(snaps),"alive-looking",len(alive),"last alive t=%.2f"%(alive[-1][0]-t0))
for s in snaps:
    if s not in alive: print("first non-alive t=%.2f"%(s[0]-t0)); break
early=[s for s in alive if s[0]-t0<2.5]
for off in sorted(snaps[0][1]):
    ev={s[1][off] for s in early}
    if len(ev)!=1: continue
    seq=[(s[0]-t0,s[1][off]) for s in alive]
    chg=[(t,v) for (t,v) in seq if v not in ev]
    if chg:
        vals=[]; 
        for t,v in seq:
            if not vals or vals[-1][1]!=v: vals.append((t,v))
        print("+0x%03X early %08X -> %s"%(off-base,list(ev)[0]," ".join("%.2f:%08X"%x for x in vals[1:8])))
