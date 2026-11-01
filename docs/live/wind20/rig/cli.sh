#!/usr/bin/env bash
# cli.sh host|join <log-name> <dolphin-pid> <player-name> [port]: one ww-multiplayer host/join
# on one Dolphin, as the PC's l6-b2-cli.ps1. Only the WW_* vars you export for this call
# (WW_ENEMY_SYNC, WW_ENEMY_REMOTE, WW_BOATS, WW_RELAY_SCOPE, WW_ENEMY_ANNEX, WW_ENEMY_MIRROR_KINDS,
# WW_PROJECTILES) reach it: set them on the command line, e.g. WW_BOATS=off cli.sh host ...
# Logs to $RIG_DIR/logs/<log-name>{,.err}; PID to $RIG_DIR/pids/<log-name>.
set -euo pipefail; . "$(dirname "$0")/env.sh"
role=$1 log=$2 dpid=$3 name=$4 port=${5:-25566}
if [ "$role" = host ]; then args=(host "$name" --port "$port"); else args=(join "127.0.0.1:$port" "$name"); fi
cd "$RIG_DIR"
WW_DOLPHIN_PID=$dpid WW_DOLPHIN_INDEX= "$WW_BIN/ww-multiplayer" "${args[@]}" \
  >"$RIG_DIR/logs/$log" 2>"$RIG_DIR/logs/$log.err" </dev/null &
echo $! > "$RIG_DIR/pids/$log"; echo $!
