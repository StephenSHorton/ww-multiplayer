#!/usr/bin/env bash
# stop.sh: end mp-local and both Dolphins by recorded PID only (never by command-line match).
. "$(dirname "$0")/env.sh"
for f in mpl A B; do p=$(cat "$RUN/$f.pid" 2>/dev/null) && kill "$p" 2>/dev/null && echo "stopped $f ($p)"; done
