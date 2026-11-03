import sys,time
for l in sys.stdin:
    sys.stdout.write("%.3f %s"%(time.time(),l)); sys.stdout.flush()
