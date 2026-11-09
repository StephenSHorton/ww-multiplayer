# usage: python3 -I symlook.py <addr-hex>...   -> containing symbol from tww symbols.txt
#        python3 -I symlook.py -n <substring>  -> symbols whose name contains substring
import re,sys,bisect
syms=[]
for l in open('/Users/stephenhorton/projects/tww/config/GZLE01/symbols.txt'):
    m=re.match(r'(\S+) = (\.\w+):0x([0-9A-Fa-f]+);(?:.*?size:0x([0-9A-Fa-f]+))?',l)
    if m: syms.append((int(m.group(3),16),int(m.group(4),16) if m.group(4) else 0,m.group(1),m.group(2)))
syms.sort(); addrs=[s[0] for s in syms]
if sys.argv[1]=='-n':
    for a,s,n,sec in syms:
        if sys.argv[2] in n: print(f'{a:08X} size 0x{s:X} {sec} {n}')
    sys.exit()
for x in sys.argv[1:]:
    a=int(x,16); i=bisect.bisect_right(addrs,a)-1
    b,s,n,sec=syms[i]; print(f'{a:08X}: {n}+0x{a-b:X} ({sec}, size 0x{s:X})')
