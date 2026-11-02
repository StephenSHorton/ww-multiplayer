#!/bin/bash
# fight.sh <pid> <bdk-addr> <mask|crest> [secs=240]: in the mask fight each time the beak sticks (mAction 5, mState 5/6),
# warp Link 110 units from the head toward where he stands, face the head, swing: X (hammer, mask) or B (sword, crest).
# After each swing Link goes back to the arena middle (4300, 9810, -4300): by the wall the bird walks and never pecks.
# crest: press A first to put the Skull Hammer away; with it in hand B never touched the crest (wind#38, pine).
# lunge: B with the stick toward the head from 160 units (LIVE-2's crest recipe). Stops at KAMEN_DEMO (mAction 9) / m8F8 4 (mask) or DEAD (mAction 10, crest).
. "$(dirname "$0")/lib.sh"; export WW_DOLPHIN_PID=$1; bdk=$2; mode=$3
btn=0x0400; sy=0; dist=110; [ "$mode" = crest ] && btn=0x0200; [ "$mode" = lunge ] && { btn=0x0200; sy=100; dist=160; mode=crest; }
A(){ hx $((bdk+$1)); }
[ "$mode" = crest ] && { "$WWDEV" input 0x0100 0 0 150 >/dev/null; sleep 1; }
t=0; c=0; end=$((SECONDS+${4:-240})); lastm=-1
while [ $SECONDS -lt $end ]; do
  w=$(pk $(A 0x2C4)); act=$(( w & 0xFFFF )); ms=$(( $(pk $(A 0x2C8)) >> 16 )); m8=$(( $(pk $(A 0x8F8)) >> 24 ))
  [ $m8 -ne $lastm ] && { echo "m8F8 $m8 after $t swings"; lastm=$m8; }
  if [ "$mode" = mask ] && { [ $act -eq 9 ] || [ $m8 -ge 4 ]; }; then echo "mask broken: mAction $act m8F8 $m8 after $t swings"; break; fi
  if [ "$mode" = crest ] && [ $act -eq 10 ]; then echo "DEAD after $t swings"; break; fi
  if [ $act -eq 5 ] && { [ $ms -eq 5 ] || [ $ms -eq 6 ]; }; then
    hxv=$(f32 $(A 0x1150)); hyv=$(f32 $(A 0x1154)); hzv=$(f32 $(A 0x1158))
    L=$(pk 0x803CA754); lx=$(f32 $(hx $((L+0x1F8)))); lz=$(f32 $(hx $((L+0x200))))
    read wx wz <<< $(python3 -I -c "import math,sys;hx,hz,lx,lz,D=map(float,sys.argv[1:6]);d=math.hypot(lx-hx,lz-hz) or 1;print(round(hx+(lx-hx)/d*D),round(hz+(lz-hz)/d*D))" $hxv $hzv $lx $lz $dist)
    "$WWMP" warp $wx 9810 $wz >/dev/null 2>&1; face $hxv $hzv
    "$WWDEV" input $btn 0 $sy 300 >/dev/null; t=$((t+1)); echo "swing $t: head ($hxv $hyv $hzv) link -> ($wx $wz)"; sleep 2.5; "$WWMP" warp 4300 9810 -4300 >/dev/null 2>&1
  else sleep 0.15; fi; c=$((c+1)); [ $((c%40)) -eq 0 ] && echo "tick $SECONDS act $act ms $ms m8 $m8"
done
"$WWDEV" input-release >/dev/null
