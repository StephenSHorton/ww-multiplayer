#!/bin/bash
# cond.sh <label> [netem spec]: one condition of wind#82's battery on a fresh pair.
cd ~/ww-shared/rigs/wind82
L=$1; S=${2:-}
RIG=~/projects/ww-mp-wind82/scripts/mac-rig.sh; W=~/ww-shared/bin/ww-multiplayer
export WWDEV=$PWD/wwdev
mark() { python3 -c 'import sys,time;print("%.3f MARK %s"%(time.time()," ".join(sys.argv[1:])))' "$@" >> marks-$L.log; }
: > marks-$L.log
# fresh pair from the pre-Gohma save, cards from the same GCI
$RIG stop wind82 >/dev/null 2>&1; sleep 3
for u in u1 u2; do cp ~/ww-shared/rigs/wind62/card-gohma.gci "$u/GC/USA/Card A/01-GZLE-gczelda.gci"; done
$RIG pair wind82 ~/ww-shared/rigs/wind62/start-gohma.sav || exit 1
P=$(cat pair.pids); A=${P%,*}; B=${P#*,}
export MP_LOCAL_PIDS=$P USER_DIR_1=$PWD/u1 USER_DIR_2=$PWD/u2 WW_DEV_LOGS=$PWD/logs
sleep 25
: > mp-$L.log
./run.sh "$L" "$S" &
RUNPID=$!
tail -n0 -F mp-$L.log 2>/dev/null | python3 -u ts.py > ts-$L.log &
TSPID=$!
for i in $(seq 180); do [ $(grep -c "bound the file" mp-$L.log) -ge 2 ] && break; sleep 1; done
sleep 5; mark session ready
$RIG fps $A $B | tee fps-$L.txt
# 1. movement at sea (DRI), each walking
for w in A B; do mark walk $w; ./wwdev smooth-steps 5 $w 2>&1 | grep walker | sed "s/^/$w: /" | tee -a smooth-$L.txt; done
# 2. stage hop into DRC r6, then kills of its three keyed Bokoblins on A
mark hop r6
for p in $A $B; do WW_DOLPHIN_PID=$p ./wwdev stage-hop M_NewD2 9 6 --yes 2>&1 | tail -1; done
sleep 15
for pid in $(WW_DOLPHIN_PID=$A ./wwdev actors 2>&1 | awk '/proc 0x00BE bk/ {print $4}'); do
  mark kill $pid; WW_DOLPHIN_PID=$A ./wwdev enemy-kill $pid 3 --yes >/dev/null 2>&1
done
sleep 8
WW_DOLPHIN_PID=$B ./wwdev actors 2>&1 | grep -c "proc 0x00BE bk" | sed 's/^/B bk left: /' | tee -a kills-$L.txt
# 3. chest race: both warped 200 units either side of DRC chest 3 at once
mark chest race
(WW_DOLPHIN_PID=$A $W warp -2352 2860 -1317 >/dev/null 2>&1 &)
WW_DOLPHIN_PID=$B $W warp -2752 2860 -1317 >/dev/null 2>&1
sleep 10
# 4. boss relay: B waits at the DRC entrance, A kills Gohma in its arena
mark boss hops
WW_DOLPHIN_PID=$B ./wwdev stage-hop M_NewD2 0 0 --yes 2>&1 | tail -1
WW_DOLPHIN_PID=$A ./wwdev stage-hop M_DragB 1 0 --yes 2>&1 | tail -1
sleep 15
btd=$(WW_DOLPHIN_PID=$A ./wwdev actors 2>&1 | awk '/proc 0x00EB/ {print $2; exit}')
if [ -n "$btd" ]; then
  mark boss poke; WW_DOLPHIN_PID=$A ./wwdev poke-u32 $(printf '0x%08X' $(( btd + 0x2E4 ))) 0x000B0032 | tail -1
  for i in $(seq 120); do grep -q "set t03 DRC boss defeated" mp-$L.log && break; sleep 1; done
  mark boss done
else
  echo "no Btd on A"; mark boss none
fi
echo "/status" >> console.in; sleep 4
$RIG fps $A $B | tee -a fps-$L.txt
touch stop; for i in $(seq 60); do grep -q '^Done.' mp-$L.log && break; sleep 1; done; pkill -f 'tail -f console.in'; wait $RUNPID; sleep 2; kill $TSPID 2>/dev/null; pkill -f "tail -n0 -F mp-$L.log" 2>/dev/null
grep -E 'ISI|DSI|Invalid (read|write)' u1/Logs/dolphin.log u2/Logs/dolphin.log | grep -v "Dolphin OS" | head -3 > crash-$L.txt
echo "done $L"
