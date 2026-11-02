#!/usr/bin/env bash
# wd.sh <wwdev|wm> args...: run a tool against this rig's own Dolphin only (u1.pid, checked to be on rigs/wind66/u1).
set -e; R=$(cd "$(dirname "$0")" && pwd)
P=$(cat "$R/u1.pid" 2>/dev/null || true)
[ -n "$P" ] || { echo "wd.sh: no u1.pid" >&2; exit 2; }
ps -p "$P" -o command= | grep -q -- "-u $R/u1 " || { echo "wd.sh: pid $P is not this rig's Dolphin" >&2; exit 2; }
t=$1; shift; case $t in wwdev) b=${WD:-$R/bin/wwdev};; wm) b=${WM:-$R/bin/ww-multiplayer};; *) exit 2;; esac
WW_DOLPHIN_PID=$P exec "$b" "$@"
