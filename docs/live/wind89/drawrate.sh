#!/bin/bash
# drawrate.sh <secs> <pid>...: per game frame, mini-Link name tags queued (wwvis +0xE0) and equip models entered (+0xE4), over the heartbeat (+0x00).
W=~/ww-shared/rigs/wind89/bin/wwdev; S=$1; shift
rd() { WW_DOLPHIN_PID=$1 $W peek $2 4 2>/dev/null | tail -1 | awk '{print $2}'; }
i=0; for p in "$@"; do H[$i]=$(rd $p 0x80414F00); T[$i]=$(rd $p 0x804158B0); E[$i]=$(rd $p 0x804158B4); i=$((i+1)); done
sleep $S
i=0; for p in "$@"; do h1=$(rd $p 0x80414F00); t1=$(rd $p 0x804158B0); e1=$(rd $p 0x804158B4); err=$(rd $p 0x804158BC)
 python3 -c "h=($h1-${H[$i]})&0xffffffff; t=($t1-${T[$i]})&0xffffffff; e=($e1-${E[$i]})&0xffffffff; print('pid $p frames',h,'tags/frame %.4f'%(t/h),'equip/frame %.4f'%(e/h),'fps %.1f'%(h/$S),'vis_err $err')"; i=$((i+1)); done
