#!/bin/bash
# cond.sh <label> [netem spec]: one condition of wind#82's battery on a fresh pair.
#
# The rig folder needs its own wwdev and ww-multiplayer, both built (and
# entitled, docs/mac-rig.md) from one tree, and a start save captured on that
# tree's REL. A save state carries the C blob in RAM: a save from another
# build runs another mailbox, and the tools' writes (the chest race's warp
# above all) go unanswered (wind#86). The check before the battery stops on a
# mismatch instead.
#   RIG_NAME  the rig name (default wind82): ~/ww-shared/rigs/$RIG_NAME
#   REPO      the checkout whose scripts/mac-rig.sh runs it (default ~/projects/ww-mp-$RIG_NAME)
#   SAVE      the start save (default ~/ww-shared/fixtures/start-gohma-3D7FEC97.sav, the
#             pre-Gohma save on REL 0x3D7FEC97; recapture it from card-gohma.gci after a REL change)
RIG_NAME=${RIG_NAME:-wind82}
cd ~/ww-shared/rigs/$RIG_NAME || exit 2
L=$1; S=${2:-}
REPO=${REPO:-~/projects/ww-mp-$RIG_NAME}
SAVE=${SAVE:-~/ww-shared/fixtures/start-gohma-3D7FEC97.sav}
RIG=$REPO/scripts/mac-rig.sh; W=$PWD/ww-multiplayer
export WWDEV=$PWD/wwdev RIG_NAME REPO
for t in "$WWDEV" "$W"; do [ -x "$t" ] || { echo "missing $t: build it from $REPO and entitle it"; exit 2; }; done
mark() { python3 -c 'import sys,time;print("%.3f MARK %s"%(time.time()," ".join(sys.argv[1:])))' "$@" >> marks-$L.log; }
: > marks-$L.log
# fresh pair from the pre-Gohma save, cards from the same GCI
$RIG stop $RIG_NAME >/dev/null 2>&1; sleep 3
for u in u1 u2; do cp ~/ww-shared/rigs/wind62/card-gohma.gci "$u/GC/USA/Card A/01-GZLE-gczelda.gci"; done
$RIG pair $RIG_NAME "$SAVE" || exit 1
P=$(cat pair.pids); A=${P%,*}; B=${P#*,}
export MP_LOCAL_PIDS=$P USER_DIR_1=$PWD/u1 USER_DIR_2=$PWD/u2 WW_DEV_LOGS=$PWD/logs
sleep 25
# The save and the tools must be one build (see the top).
for p in $A $B; do
  if ! WW_DOLPHIN_PID=$p ./wwdev dump 2>&1 | grep -q "matches this version"; then
    echo "Dolphin $p: the save's build is not this rig's wwdev:"; WW_DOLPHIN_PID=$p ./wwdev dump 2>&1 | grep contract
    $RIG stop $RIG_NAME >/dev/null 2>&1; exit 2
  fi
done
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
# Each warp's answer goes to warp-$L.txt; a failed warp is said, not dropped.
(WW_DOLPHIN_PID=$A $W warp -2352 2860 -1317 > warp-$L-A.txt 2>&1 || echo "warp A failed: $(cat warp-$L-A.txt)" &)
WW_DOLPHIN_PID=$B $W warp -2752 2860 -1317 > warp-$L-B.txt 2>&1 || echo "warp B failed: $(cat warp-$L-B.txt)"
sleep 1; cat warp-$L-A.txt warp-$L-B.txt > warp-$L.txt; rm -f warp-$L-A.txt warp-$L-B.txt
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
