#!/usr/bin/env bash
# gdforce.sh <pid> <request:0|1>: P0-BST recipe (Gohdan, SirenB 1 0), re-forcing parts that leave it, until bst-probe says APPLIED;
# with 1, poke the boss_kill request too. Mac port of the PC's archive/enemy-batch-d/live/two/gdforce.sh.
WWDEV=${WWDEV:-$HOME/ww-shared/bin/wwdev}; export WW_DOLPHIN_PID=$1
P(){ "$WWDEV" poke-u32 $1 $2 >/dev/null 2>&1; }
word(){ "$WWDEV" peek $1 4 | awk '{print $2}' | head -1; }
probe(){ out=$("$WWDEV" bst-probe 1 2>&1 | head -6); }
probe
head=$(echo "$out" | grep -m1 "  head" | awk '{print $4}'); h1=$(echo "$out" | grep -m1 "  hand1" | awk '{print $4}'); h2=$(echo "$out" | grep -m1 "  hand2" | awk '{print $4}')
[ -n "$head" ] && [ -n "$h1" ] && [ -n "$h2" ] || { echo "parts not found"; echo "$out"; exit 1; }
fh(){ P $(printf %X $(($1+0x10D4))) 00000006; P $(printf %X $(($1+0x10D8))) 00000000; P $(printf %X $(($1+0x10FC))) 0000012C; }
fhead(){ P $(printf %X $((head+0x10D4))) 00000007; P $(printf %X $((head+0x10D8))) 00000000; P $(printf %X $((head+0x2E7C))) 0C000000; }
fh $h1; fh $h2; fhead
for i in $(seq 1 180); do
  sleep 0.5; probe
  echo "$out" | grep -q "would answer APPLIED" && break
  w=$(echo "$out" | grep "would answer")
  echo "$w" | grep -q "head act [0-9]* != 7" && fhead
  echo "$w" | grep -q "hand1 act" && fh $h1
  echo "$w" | grep -q "hand2 act" && fh $h2
done
echo "$out" | grep "would answer"
if [ "$2" = 1 ] && echo "$out" | grep -q "would answer APPLIED"; then ack=$(word 80413418); P 80413414 00F2424B; P 80413410 $(printf %08X $((ack+1))); sleep 0.8; "$WWDEV" peek 80413410 16; fi
