import sys,time,json,os,re,shutil; sys.path.insert(0,'.')
from app import App
R=os.path.dirname(os.path.abspath(__file__))
class A(App):
    def __init__(s,label,base,tok): s.label=label;s.base,s.tok=base,tok
m=re.findall(r"(http://127\.0\.0\.1:\d+)/\?token=(\w+)",open(R+'/hostA.out').read())[-1]
A_=A('hostA',*m)
shutil.rmtree(R+'/appdata-B',ignore_errors=True); shutil.copy(R+'/rel/ww-multiplayer.exe',R+'/inst-B.exe')
B=App('joinB',R+'/inst-B.exe',R+'/appdata-B')
iso=A_.call('Setup')['patched']; dol=A_.call('Setup')['dolphinExe']
print(B.call('SaveProfile',{'name':'Bob','color':'blue'}))
print(B.call('StartPatch',{'path':iso,'patched':True}))
for _ in range(60):
    p=B.call('PatchStatus')
    if _>1 and not p['running']: break
    time.sleep(1)
print('B patch',p['stage'],p['error'])
print(B.call('SetDolphin',{'exe':dol,'userDir':os.path.expandvars(r'%APPDATA%\Dolphin Emulator 2')}))
print(B.call('FinishSetup'))
print('A setup',{k:v for k,v in A_.call('Setup').items() if k in('needed','patchedOK','patchedStale')},'B',{k:v for k,v in B.call('Setup').items() if k in('needed','patchedOK','patchedStale')})
print(A_.call('Host',{'name':'Alice','color':'red','port':0}))
time.sleep(2)
sa=A_.call('State'); print('host addrs',sa['hostAddrs'],sa['phase'])
port=sa['lobby'] and sa['addr']
print(json.dumps(sa)[:500])
open(R+'/B.url','w').write(B.base+' '+B.tok)
