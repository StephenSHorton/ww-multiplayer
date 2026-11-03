#!/bin/bash
# hbmon.sh <secs> <out> <pid>...: heartbeat delta per second per Dolphin; a line per second.
W=~/ww-shared/rigs/wind89/bin/wwdev; S=$1; O=$2; shift 2
rd() { WW_DOLPHIN_PID=$1 $W peek 0x80414F00 4 2>/dev/null | tail -1 | awk '{print $2}'; }
i=0; for p in "$@"; do L[$i]=$(rd $p); i=$((i+1)); done
for t in $(seq 1 $S); do sleep 1; line="t=$t"; i=0
 for p in "$@"; do v=$(rd $p); d=$(python3 -c "print(($v-${L[$i]})&0xffffffff)" 2>/dev/null || echo ERR); line="$line $p:$d"; L[$i]=$v; i=$((i+1)); done
 echo "$line" >> $O; done
