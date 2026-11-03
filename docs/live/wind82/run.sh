#!/bin/sh
# run.sh <label> [netem spec]: one co-op mp-local on this rig, console from console.in
# RIG_NAME and REPO as in cond.sh (default wind82, ~/projects/ww-mp-$RIG_NAME).
RIG_NAME=${RIG_NAME:-wind82}; REPO=${REPO:-$HOME/projects/ww-mp-$RIG_NAME}
cd ~/ww-shared/rigs/$RIG_NAME || exit 2
label=$1; spec=${2:-}
: > console.in
rm -f stop; tail -f console.in | MP_LOCAL_STOP_FILE=$PWD/stop MP_LOCAL_NETEM="$spec" MP_LOCAL_PORT=0 WWDEV=$PWD/wwdev \
  "$REPO/scripts/mac-rig.sh" mp-local $RIG_NAME --coop=new > "mp-$label.log" 2>&1
