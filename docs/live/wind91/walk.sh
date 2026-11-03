#!/bin/bash
# walk.sh <label> <netem spec> <udp 0|1>: one condition of wind#91's live check.
# A fresh pair from the Mac start save, mp-local with B behind MP_LOCAL_NETEM
# (TCP proxy, and the UDP proxy beside it), WW_UDP on or off for the server and
# every client, then smooth-steps 5 A and 5 B, fps before and after, and a
# crash sweep of both Dolphin logs. Results land in the rig folder.
#   RIG_NAME  the rig (default wind91): ~/ww-shared/rigs/$RIG_NAME, with its own
#             wwdev and ww-multiplayer built and entitled from REPO
#   REPO      the checkout (default ~/projects/ww-mp-$RIG_NAME)
RIG_NAME=${RIG_NAME:-wind91}
cd ~/ww-shared/rigs/$RIG_NAME || exit 2
L=$1; S=$2; U=${3:-0}
REPO=${REPO:-~/projects/ww-mp-$RIG_NAME}
RIG=$REPO/scripts/mac-rig.sh
export WWDEV=$PWD/wwdev RIG_NAME REPO
[ -x "$WWDEV" ] || { echo "missing $WWDEV"; exit 2; }
$RIG stop $RIG_NAME >/dev/null 2>&1; sleep 3
$RIG pair $RIG_NAME || exit 1
P=$(cat pair.pids); A=${P%,*}; B=${P#*,}
[ -n "$A" ] && [ -n "$B" ] || { echo "no pids"; exit 1; }
export MP_LOCAL_PIDS=$P USER_DIR_1=$PWD/u1 USER_DIR_2=$PWD/u2 WW_DEV_LOGS=$PWD/logs
sleep 25
for p in $A $B; do
  WW_DOLPHIN_PID=$p ./wwdev dump 2>&1 | grep -q "matches this version" || { echo "Dolphin $p: save/build mismatch"; $RIG stop $RIG_NAME; exit 2; }
done
rm -f stop
udp=""; [ "$U" = 1 ] && udp=1
MP_LOCAL_STOP_FILE=$PWD/stop MP_LOCAL_NETEM="$S" MP_LOCAL_PORT=0 WW_UDP=$udp \
  $RIG mp-local $RIG_NAME > mp-$L.log 2>&1 &
RUNPID=$!
for i in $(seq 120); do grep -q "Local multiplayer running" mp-$L.log && break; sleep 1; done
sleep 8
$RIG fps $A $B | tee fps-$L.txt
: > smooth-$L.txt
for w in A B; do ./wwdev smooth-steps 5 $w 2>&1 | grep walker | sed "s/^/$w: /" | tee -a smooth-$L.txt; done
$RIG fps $A $B | tee -a fps-$L.txt
touch stop; for i in $(seq 60); do grep -q 'Done\.' mp-$L.log && break; sleep 1; done
wait $RUNPID
grep -E 'ISI|DSI|Invalid (read|write)' u1/Logs/dolphin.log u2/Logs/dolphin.log | grep -v "Dolphin OS" | head -3 > crash-$L.txt
$RIG stop $RIG_NAME >/dev/null 2>&1
echo "done $L"
