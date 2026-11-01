#!/usr/bin/env bash
# mpl.sh <log-name> [boss rows] [refresh] [world]  -- bash port of the PC's two/mpl.ps1.
# Runs wwdev mp-local on the two Dolphins in $RUN/A.pid and B.pid (A = host), stdout to $RUN/<log>.
# Default rows = the default set without gohdan; add gohdan for the opt-in run.
set -euo pipefail; . "$(dirname "$0")/env.sh"
log=${1:?log name}; boss=${2:-gohma,kalle,molgera,jalhalla,helmaroc}; refresh=${3:-none}; world=${4:---coop=new}
export USER_DIR_1="$RUN/ud-A" USER_DIR_2="$RUN/ud-B"
export MP_LOCAL_PIDS="$(cat "$RUN/A.pid"),$(cat "$RUN/B.pid")"
export WW_ENEMY_MIRROR_KINDS=${WW_ENEMY_MIRROR_KINDS:-mo2,bk+u,cc+u,ki+u,mt+u}
export APPDATA="$RUN/appdata"; mkdir -p "$APPDATA"   # keeps the session config off the real one
cd "$WW"
args=(mp-local "$world" --coop-iso "$ISO" "--coop-boss-kill=$boss" "--coop-refresh=$refresh" --yes)
[ -n "${LIVE:-}" ] && args+=(--coop-live-switches)
nohup "$WWDEV" "${args[@]}" > "$RUN/$log" 2> "$RUN/$log.err" &
echo $! | tee "$RUN/mpl.pid"
