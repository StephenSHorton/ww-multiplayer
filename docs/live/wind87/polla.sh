#!/bin/bash
# polla.sh <pid> <grep> <secs> : print timestamped actor lines matching <grep> about 5x a second
R=~/ww-shared/rigs/wind87; grep -q "\b$1\b" $R/pair.pids && [ -n "$1" ] || { echo "pid $1 not this rig"; exit 2; }; end=$((SECONDS+$3))
while [ $SECONDS -lt $end ]; do
  echo "$(date +%H:%M:%S.%N | cut -c1-12) $(WW_DOLPHIN_PID=$1 $R/wwdev actors 400 2>&1 | grep -E "$2" | tr -s ' ' | tr '\n' '|')"
  sleep 0.15
done
