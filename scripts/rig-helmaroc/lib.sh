# lib.sh: helpers for the Helmaroc fight on the Mac rig (ported from the PC's enemy-batch-f/live/hammer).
# Needs WW_DOLPHIN_PID and WWDEV (an entitled wwdev); WWMP = an entitled ww-multiplayer.
WWDEV=${WWDEV:-$HOME/ww-shared/bin/wwdev}; WWMP=${WWMP:-$HOME/ww-shared/bin/ww-multiplayer}
pk(){ "$WWDEV" peek "$1" 4 | awk '{print $2}' | head -1; }
f32(){ python3 -I -c "import struct,sys;print(struct.unpack('>f',struct.pack('>I',int(sys.argv[1],16)))[0])" "$(pk "$1")"; }
hx(){ printf 0x%X "$1"; }
# face <x> <z>: turn Link (current.angle.y +0x204 and shape_angle.y +0x20C) toward (x, z)
face(){ local L x z a ad w; L=$(pk 0x803CA754); x=$(f32 $(hx $((L+0x1F8)))); z=$(f32 $(hx $((L+0x200))))
  a=$(python3 -I -c "import math,sys;x,z,tx,tz=map(float,sys.argv[1:]);print(int(round(math.atan2(tx-x,tz-z)*32768/math.pi))&0xFFFF)" $x $z $1 $2)
  for o in 0x204 0x20C; do ad=$(hx $((L+o))); w=$(pk $ad); "$WWDEV" poke-u32 $ad $(printf 0x%08X $(( (w & 0xFFFF0000) | a ))) >/dev/null; done; }
