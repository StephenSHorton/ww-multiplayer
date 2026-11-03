#!/bin/sh
# run.sh <label> [netem spec]: one co-op mp-local on this rig, console from console.in
cd ~/ww-shared/rigs/wind82
label=$1; spec=${2:-}
: > console.in
rm -f stop; tail -f console.in | MP_LOCAL_STOP_FILE=$PWD/stop MP_LOCAL_NETEM="$spec" MP_LOCAL_PORT=0 WWDEV=$HOME/ww-shared/rigs/wind82/wwdev \
  ~/projects/ww-mp-wind82/scripts/mac-rig.sh mp-local wind82 --coop=new > "mp-$label.log" 2>&1
