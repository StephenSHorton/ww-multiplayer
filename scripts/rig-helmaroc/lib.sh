# lib.sh: helpers for the Helmaroc fight on the Mac rig (ported from the PC's enemy-batch-f/live/hammer).
# Needs WW_DOLPHIN_PID and WWDEV (an entitled wwdev); WWMP = an entitled ww-multiplayer.
WWDEV=${WWDEV:-$HOME/ww-shared/bin/wwdev}; WWMP=${WWMP:-$HOME/ww-shared/bin/ww-multiplayer}
pk(){ "$WWDEV" peek "$1" 4 | awk '{print $2}' | head -1; }
f32(){ python3 -I -c "import struct,sys;print(struct.unpack('>f',struct.pack('>I',int(sys.argv[1],16)))[0])" "$(pk "$1")"; }
hx(){ printf 0x%X "$1"; }
# face <x> <z>: turn Link toward (x, z) through his own stick input (wwdev face, wind#41);
# a poke of +0x204/+0x20C does not stick (the game re-derives it every frame), which
# is likely why wind#38's scripted crest hits never landed.
face(){ "$WWDEV" face "$1" "$2" 512 >/dev/null 2>&1; }
