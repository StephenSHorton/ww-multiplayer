# Source me: peek/poke helpers from runbook-batch2-bosses.md §0, plus the boss-bit clears a relay run needs.
# Every call needs WW_DOLPHIN_PID (use: on A / on B).
. "$(dirname "${BASH_SOURCE[0]}")/env.sh"
on(){ export WW_DOLPHIN_PID=$(cat "$RUN/$1.pid"); echo "-> $1 pid $WW_DOLPHIN_PID"; }
pk(){ "$WWDEV" peek "$1" 4 | awk '{print $2}' | head -1; }
clr(){ local w; w=$(pk "$1"); "$WWDEV" poke-u32 "$1" "$(printf %08X $(( w & ~0x$2 & 0xFFFFFFFF )))" >/dev/null; echo "$1 $w -> $(pk "$1")"; }
set1(){ local w; w=$(pk "$1"); "$WWDEV" poke-u32 "$1" "$(printf %08X $(( w | 0x$2 )))" >/dev/null; echo "$1 $w -> $(pk "$1")"; }
stage(){ "$WWDEV" peek 803C9D3C 8; }
req(){ "$WWDEV" peek 80413410 16; }        # boss request: seq, proc|tag, ack, result
# Saved word holding table t's mDungeonItem (byte 1 of the word): 0x803C4FA8 + t*0x24.
dword(){ printf %X $(( 0x803C4FA8 + $1 * 0x24 )); }
# A relay run needs the boss ALIVE on both files: clear bit 3 (dead) on both, keep bit 5 (intro seen)
# so `<arena> 1 0` skips the intro. Clear before mp-local starts (switch/ever-had rules, runbook 1.2 step 4).
# Also clear the live word 0x803C53A0 if this Dolphin stands in table t right now.
boss_alive(){ local t=$1; clr "$(dword "$t")" 00080000; }
# Rows: name table arena hop proc
ROWS="gohma 3 M_DragB 'M_DragB 1 0' EB
kalle 4 kinBOSS 'kinBOSS 1 0' EC
gohdan 5 SirenB 'SirenB 1 0' F2
jalhalla 6 M_DaiB 'M_DaiB 0 0' D4
molgera 7 kazeB 'kazeB 0 0' DA
helmaroc 2 M2tower 'M2tower 30 0 3' F0"
