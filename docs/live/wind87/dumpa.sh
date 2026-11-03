#!/bin/bash
# dumpa.sh <pid> <addr> <bytes> <secs>: timestamped full reads of one actor until the time runs out
R=~/ww-shared/rigs/wind87; grep -q "\b$1\b" $R/pair.pids && [ -n "$1" ] || { echo "pid $1 not this rig"; exit 2; }; end=$((SECONDS+$4))
while [ $SECONDS -lt $end ]; do echo "@ $(python3 -c 'import time;print("%.3f"%time.time())')"; WW_DOLPHIN_PID=$1 $R/wwdev peek $2 $3 | awk '{print $1, $2}'; done
