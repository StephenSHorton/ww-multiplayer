#!/usr/bin/env bash
# force.sh <A|B> <boss> [req]   -- rig shortcuts to reach each boss's relay gate fast (real fights preferred; note every poke in the record).
#   On A (the killer): force the boss into its applicable phase, then `req` pokes A's own boss request so A's REL kills it -> A's bit -> world.
#   On B (the receiver): force the same phase and do NOT poke a request: Go must ask (that is the regression under test).
# Offsets are from docs/TESTING.md (Btd :240, Bmd :261, Bpw JH-1, Bst P0-BST/gdforce.sh). Bwd and Bdk: use a real pull / the hammer
# scripts (handoff archive/enemy-batch-f/live/hammer, hkforce.sh) - no poke recipe here yet.
set -euo pipefail; d=$(dirname "$0"); . "$d/bits.sh" >/dev/null
side=${1:?A|B}; boss=${2:?boss}; doreq=${3:-}
on "$side" >/dev/null
P(){ "$WWDEV" poke-u32 "$1" "$2" >/dev/null; }
addr_of(){ "$WWDEV" actors | grep -i "proc 0x00$1" | grep -oE '0x8[0-9A-Fa-f]{7}' | head -1; }
request(){ local ack; ack=$(pk 80413418); P 80413414 "00$1424B"; P 80413410 "$(printf %08X $((ack+1)))"; sleep 0.8; req; }
case $boss in
gohma)   a=$(addr_of EB); [ -n "$a" ] || { echo "no Btd"; exit 1; }
         set1 "$(printf %X $((a+0x2E0)))" 01000000          # m02E0 = 1: armor broken (the only phase the relay applies in)
         [ -n "$doreq" ] && request EB ;;
kalle)   a=$(addr_of EC); [ -n "$a" ] || { echo "no Bmd"; exit 1; }
         # [I] open fallen flower: mMode 3, m302 2, m304 1, m310 0, mB74 0 (vanilla may move it on: re-run until `waiting` turns `applied`)
         P "$(printf %X $((a+0x300)))" 00030002
         w=$(pk "$(printf %X $((a+0x304)))"); P "$(printf %X $((a+0x304)))" "$(printf %08X $(( (w & 0xFFFF) | 0x10000 )))"
         w=$(pk "$(printf %X $((a+0x310)))"); P "$(printf %X $((a+0x310)))" "$(printf %08X $(( w & 0xFFFF )))"
         w=$(pk "$(printf %X $((a+0xB74)))"); P "$(printf %X $((a+0xB74)))" "$(printf %08X $(( w & 0xFFFF )))"
         [ -n "$doreq" ] && request EC ;;
jalhalla) b=$("$WWDEV" bpw-probe 1 2>&1 | grep -m1 -oiE 'body[^0-9]*0x8[0-9A-F]{7}' | grep -oE '0x8[0-9A-F]{7}')
         [ -n "$b" ] || { echo "no Bpw body (bpw-probe)"; exit 1; }
         P "$(printf %X $((b+0x444)))" 00030064               # JH-1 forced gate
         [ -n "$doreq" ] && request D4 ;;
gohdan)  echo "use the PC's gdforce.sh recipe (ported below in README); bst-probe for part addresses"; exit 2 ;;
*)       echo "no poke recipe for $boss: real fight (molgera: Hookshot pull; helmaroc: hammer scripts)"; exit 2 ;;
esac
