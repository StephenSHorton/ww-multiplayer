#!/usr/bin/env bash
# hops.sh <pid> <wwdev> <ww-multiplayer> [n]: one Dolphin, no session; heap-free before and after each stage hop.
P=$1; WD=$2; WM=$3; N=${4:-5}
[ -n "$P" ] || { echo "hops.sh: no pid" >&2; exit 2; }
st=("A_mori 0 0" "sea 0 44" "M_Dai 1 1")
echo "=== before (stage: $(WW_DOLPHIN_PID=$P $WM coop-tag show 2>&1 | head -1))"
WW_DOLPHIN_PID=$P $WD heap-free 2>&1
for i in $(seq 1 $N); do
  s=${st[$(( (i-1) % 3 ))]}; t=${s%% *}
  WW_DOLPHIN_PID=$P $WD stage-hop $s --yes >/dev/null 2>&1
  ok=0; for k in $(seq 1 60); do sleep 1; WW_DOLPHIN_PID=$P $WM coop-tag show 2>&1 | head -1 | grep -q "\"$t\"" && { ok=1; break; }; done
  sleep 8   # let the stage settle (actors created, archives loaded)
  echo "=== hop $i $s arrived=$ok"
  WW_DOLPHIN_PID=$P $WD heap-free 2>&1
done
