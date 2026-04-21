#!/usr/bin/env bash
# Spin up a full two-Dolphin multiplayer demo on a single machine.
#
# Assumes BOTH Dolphin instances are already running and have a Wind
# Waker save loaded:
#   - Dolphin index 0: the older instance (lowest PID) — your usual one
#   - Dolphin index 1: launched via scripts/dolphin2.sh
#
# Spawns five background processes:
#   1. server               — relay hub on :25565
#   2. broadcast-pose @ A   — ships Dolphin-0's Link pose
#   3. broadcast-pose @ B   — ships Dolphin-1's Link pose
#   4. puppet-sync @ A      — receives B's pose, renders Link #2 in Dolphin-0
#   5. puppet-sync @ B      — receives A's pose, renders Link #2 in Dolphin-1
#
# Logs go to .omc/logs/mplay2/*.log (auto-created).
#
# Hit Ctrl+C to tear everything down.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WW="$ROOT/ww.exe"
LOG_DIR="$ROOT/.omc/logs/mplay2"
mkdir -p "$LOG_DIR"

if [[ ! -x "$WW" ]]; then
    echo "ERROR: $WW not found. Run 'go build -o ww.exe .' first."
    exit 1
fi

PIDS=()
launch() {
    local label="$1"; shift
    "$@" >"$LOG_DIR/$label.log" 2>&1 &
    local pid=$!
    PIDS+=($pid)
    echo "  [$label] pid=$pid"
}

cleanup() {
    echo
    echo "Tearing down..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    wait 2>/dev/null || true
    # Reset both Dolphins to baseline mirror so Link #2 doesn't stay frozen
    # from the last pose_buf write.
    WW_DOLPHIN_INDEX=0 "$WW" shadow-mode 0 >/dev/null 2>&1 || true
    WW_DOLPHIN_INDEX=1 "$WW" shadow-mode 0 >/dev/null 2>&1 || true
    echo "Done."
}
trap cleanup EXIT INT TERM

echo "Starting server..."
launch server "$WW" server

# Give server a moment to listen.
sleep 2

# WW_SELF_NAME tells each puppet-sync to ignore its co-located broadcast
# twin. Without this, puppet-A would write broadcast-A's stream (=
# Dolphin-0's own Link position) into a puppet actor inside Dolphin 0,
# which physics-collides with your real Link and knocks you into the
# ocean. Mirror on Dolphin 1.
#
# Stagger client launches by ~0.3s each — four simultaneous connects
# against a freshly-listening server occasionally produced "expected
# welcome, got error or wrong type" on Windows (TCP accept + welcome-
# write race).

echo "Connecting Dolphin index 0 ..."
WW_DOLPHIN_INDEX=0                      launch broadcast-A "$WW" broadcast-pose PlayerA localhost:25565
sleep 0.3
WW_DOLPHIN_INDEX=0 WW_SELF_NAME=PlayerA launch puppet-A    "$WW" puppet-sync    PlayerA localhost:25565
sleep 0.3

echo "Connecting Dolphin index 1 ..."
WW_DOLPHIN_INDEX=1                      launch broadcast-B "$WW" broadcast-pose PlayerB localhost:25565
sleep 0.3
WW_DOLPHIN_INDEX=1 WW_SELF_NAME=PlayerB launch puppet-B    "$WW" puppet-sync    PlayerB localhost:25565

echo
echo "Running. Logs: $LOG_DIR"
echo "Walk around in either Dolphin window — the OTHER instance should"
echo "render your Link as Link #2 at your real world coords."
echo
echo "Ctrl+C to stop."
wait
