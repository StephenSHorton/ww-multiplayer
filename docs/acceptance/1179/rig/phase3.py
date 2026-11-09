import sys,time,json,os,re; sys.path.insert(0,'.')
from app import App
R=os.path.dirname(os.path.abspath(__file__))
class A(App):
    def __init__(s,label,base,tok): s.label=label;s.base,s.tok=base,tok
m=re.findall(r"(http://127\.0\.0\.1:\d+)/\?token=(\w+)",open(R+'/hostA.out').read())[-1]
a=A('hostA',*m); b=A('joinB',*open(R+'/B.url').read().split())
PORT=25570

log=open(R+'/states.jsonl','a'); last={}
def snap(tag):
    for n,x in(('A',a),('B',b)):
        s=x.call('State'); log.write(json.dumps({'t':tag,'who':n,'s':s})+'\n'); log.flush()
        key=(s['phase'],json.dumps(s.get('dialog')),s['dolphin'].get('state') if isinstance(s['dolphin'],dict) else '',json.dumps(s.get('coop',{}) and s['coop'].get('panel')))
        if last.get(n)!=key: last[n]=key; print(tag,n,'phase',s['phase'],'dialog',s.get('dialog'),'dolphin',s['dolphin'],'panel',(s.get('coop') or {}).get('panel'),'disc',s['disc'].get('state'),'wait',s['startWaiting'],flush=True)
print('ready',b.call('SetReady',True))
for i in range(5): snap(i); time.sleep(1)
print('start',a.call('Start',False),flush=True)
for i in range(120): snap(100+i); time.sleep(1)
