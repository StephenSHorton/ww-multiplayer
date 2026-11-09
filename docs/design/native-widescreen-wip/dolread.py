# usage: python3 -I dolread.py <addr-hex> [nwords]  -> words + float view from the vanilla DOL
import struct,sys
d=open('/Users/stephenhorton/.claude/jobs/9108197d/tmp/original.dol','rb').read()
offs=struct.unpack('>18I',d[0:72]); addrs=struct.unpack('>18I',d[72:144]); sizes=struct.unpack('>18I',d[144:216])
def f(a):
    for o,b,s in zip(offs,addrs,sizes):
        if s and b<=a<b+s: return o+(a-b)
    return None
a=int(sys.argv[1],16); n=int(sys.argv[2]) if len(sys.argv)>2 else 1
for i in range(n):
    x=a+4*i; o=f(x)
    if o is None: print(f'{x:08X}: not in DOL (bss?)'); continue
    w=struct.unpack('>I',d[o:o+4])[0]; fl=struct.unpack('>f',d[o:o+4])[0]
    print(f'{x:08X}: {w:08X}  f32={fl:g}  fileoff=0x{o:X}')
