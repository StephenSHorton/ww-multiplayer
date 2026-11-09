import sys,time,json,os,shutil; sys.path.insert(0,'.')
from app import App
R=os.path.dirname(os.path.abspath(__file__))
shutil.rmtree(R+'/appdata-A',ignore_errors=True)
shutil.copy(R+'/old-v0.6.0/ww-multiplayer.exe', R+'/inst-A.exe')  # the installed copy that will update itself
a=App('hostA', R+'/inst-A.exe', R+'/appdata-A')
disc=os.path.expandvars(r'%USERPROFILE%\Desktop\Dolphin-x64\Roms\Legend of Zelda, The - The Wind Waker (USA, Canada).ciso')
dol=os.path.expandvars(r'%USERPROFILE%\Desktop\Dolphin-x64\Dolphin.exe')
print(a.call('SaveProfile',{'name':'Alice','color':'red'}))
print(a.call('StartPatch',{'path':disc}))
for _ in range(300):
    p=a.call('PatchStatus')
    if not p['running'] and p['stage'] in('done','failed','idle') and _>2: break
    time.sleep(2)
print('PATCH',p['stage'],p['error'],p['lines'][-5:])
print(a.call('SetDolphin',{'exe':dol,'userDir':os.path.expandvars(r'%APPDATA%\Dolphin Emulator')}))
print(a.call('FinishSetup'))
print(json.dumps(a.call('CheckUpdate',True)))
print(json.dumps(a.call('InstallUpdate')))
for _ in range(15):
    time.sleep(2)
    try: u=a.call('UpdateStatus'); print(u['stage'],u['current'],u['repatch'],u['error']); 
    except Exception as e: print('gone',e); break
time.sleep(8)
print('base now',a.base, open(R+'/hostA.out').read()[-800:])
