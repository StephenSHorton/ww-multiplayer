#!/usr/bin/env bash
# hkforce.sh <pid> <mode>: down | mask | zero | req   -- Mac port of the PC's archive/enemy-batch-f/live/hkforce.sh
# (Helmaroc King, Bdk proc 0xF0 in M2tower 30 0 3). Uses $WWDEV (default the shared entitled build).
WWDEV=${WWDEV:-$HOME/ww-shared/bin/wwdev}; export WW_DOLPHIN_PID=$1
P(){ "$WWDEV" poke-u32 $1 $2 >/dev/null 2>&1; }
word(){ "$WWDEV" peek $1 4 | awk '{print $2}' | head -1; }
RMWA(){ w=$(( $(word $1) )); P $1 $(printf %08X $(( (w & $2) | $3 ))); }
A(){ printf %X $((bdk+$1)); }
RMW(){ RMWA $(A $1) $2 $3; }
down(){ RMW 0x2C8 0x0000FFFF 0x00000000; RMW 0x2C4 0xFFFF0000 0x00000067; }
mask(){ RMW 0x8F8 0x00FFFFFF 0x03000000; RMW 0x2C4 0xFFFF0000 0x00000009; RMW 0x2C8 0x0000FFFF 0x00000000; }
zero(){ RMW 0x284 0xFF00FFFF 0x00000000; }
req(){ ack=$(word 80413418); P 80413414 00F0424B; P 80413410 $(printf %08X $((ack+1))); sleep 0.8; "$WWDEV" peek 80413410 16; }
case $2 in
down|mask|zero) bdk=$("$WWDEV" bdk-probe 1 2>&1 | awk '/^  bdk at/ {print $3; exit}')
  [ -n "$bdk" ] || { echo "no Bdk (bdk-probe)"; exit 1; }; $2 ;;
req) req ;;
*) echo "usage: hkforce.sh <pid> down|mask|zero|req"; exit 2 ;;
esac
