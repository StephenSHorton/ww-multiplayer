import subprocess, os, re, json, time, urllib.request, sys
RIG = os.path.dirname(os.path.abspath(__file__))
class App:
    def __init__(s, label, exe, appdata, extra_env=None, args=("ui","--browser")):
        s.label=label; s.appdata=appdata; os.makedirs(appdata, exist_ok=True)
        env=dict(os.environ, APPDATA=appdata, WW_UPDATE_URL="http://127.0.0.1:8099/latest.json"); env.update(extra_env or {})
        s.log=open(os.path.join(RIG, label+".out"),"w")
        s.p=subprocess.Popen([exe,*args],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
        s.base=s.tok=None; s.lines=[]
        import threading
        threading.Thread(target=s._pump,daemon=True).start()
        for _ in range(100):
            if s.base: break
            time.sleep(.2)
    def _pump(s):
        for l in s.p.stdout:
            s.log.write(l); s.log.flush(); s.lines.append(l)
            m=re.search(r"(http://127\.0\.0\.1:\d+)/\?token=([0-9A-Fa-f]+)",l)
            if m: s.base,s.tok=m.group(1),m.group(2)
    def call(s,m,*a):
        r=urllib.request.Request(s.base+"/api/call/"+m,data=json.dumps(list(a)).encode(),method="POST",headers={"X-WW-Token":s.tok,"Origin":s.base,"Content-Type":"application/json"})
        try: b=urllib.request.urlopen(r,timeout=60).read()
        except urllib.error.HTTPError as e: b=e.read()
        d=json.loads(b); 
        if d.get("error"): raise RuntimeError(m+": "+d["error"])
        return d.get("result")
