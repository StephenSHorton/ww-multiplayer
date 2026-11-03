#!/bin/bash
# measure.sh <label>: one two-Dolphin co-op run with per-stream wire counts (wind#84).
# At sea by Dragon Roost (idle 20 s, A walks 5 s, B walks 5 s), then both in DRC room 6 (idle 30 s).
R=${WW_SHARED:-~/ww-shared}/rigs/wind85; cd $R
L=$1
RIG=~/projects/ww-mp-wind85/scripts/mac-rig.sh
export WWDEV=$R/${WWDEV_BIN:-wwdev}
mark() { python3 -c 'import sys,time;print("%.3f MARK %s"%(time.time()," ".join(sys.argv[1:])))' "$@" >> mp-$L.ts; }
$RIG stop wind85 >/dev/null 2>&1; sleep 3
for u in u1 u2; do cp ~/ww-shared/rigs/wind62/card-gohma.gci "$u/GC/USA/Card A/01-GZLE-gczelda.gci"; done
$RIG pair wind85 $R/start-gohma.sav || exit 1
P=$(cat pair.pids); A=${P%,*}; B=${P#*,}
export MP_LOCAL_PIDS=$P USER_DIR_1=$R/u1 USER_DIR_2=$R/u2 WW_DEV_LOGS=$R/logs
sleep 25
: > console.in; rm -f stop; : > mp-$L.ts
(tail -f console.in | MP_WIRE_STATS=5 MP_LOCAL_STOP_FILE=$R/stop MP_LOCAL_PORT=0 WW_DEV_LOGS=$R/logs \
  $RIG mp-local wind85 --coop=new 2>&1 | python3 -u $R/ts.py >> mp-$L.ts) &
RUNPID=$!
for i in $(seq 180); do [ $(grep -c "bound the file" mp-$L.ts) -ge 2 ] && break; sleep 1; done
sleep 5; mark session ready
$RIG fps $A $B | tee fps-$L.txt
mark sea idle
[ -n "$SHOTS" ] && { $RIG shot $A shot-$L-sea-A.png; $RIG shot $B shot-$L-sea-B.png; }
sleep 20
for w in A B; do mark walk $w; $WWDEV smooth-steps 5 $w 2>&1 | grep walker | sed "s/^/$w: /" | tee -a smooth-$L.txt; done
mark hop r6
for p in $A $B; do WW_DOLPHIN_PID=$p $WWDEV stage-hop M_NewD2 9 6 --yes 2>&1 | tail -1; done
sleep 12
mark drc idle
if [ -n "$SHOTS" ]; then for w in A B; do $WWDEV smooth-steps 3 $w >/dev/null 2>&1; done; sleep 1; $RIG shot $A shot-$L-A.png; $RIG shot $B shot-$L-B.png; WW_DOLPHIN_PID=$A $WWDEV input 0 0 100 1500 >/dev/null 2>&1 & sleep 0.7; $RIG shot $B shot-$L-B-walk.png; sleep 1; fi
sleep 30
mark drc end
$RIG fps $A $B | tee -a fps-$L.txt
touch stop; for i in $(seq 60); do grep -q 'Done\.' mp-$L.ts && break; sleep 1; done
pkill -f "tail -f $R/console.in" 2>/dev/null; pkill -f "tail -f console.in" 2>/dev/null
sleep 3
$RIG stop wind85 >/dev/null 2>&1
grep -E 'ISI|DSI|Invalid (read|write)' u1/Logs/dolphin.log u2/Logs/dolphin.log | grep -v "Dolphin OS" | head -3 > crash-$L.txt
echo "done $L"
