#!/usr/bin/env bash
# sweep.sh <mp-local log>: relay lines + crash lines in the mp-local log, both Player logs and both dolphin.logs.
. "$(dirname "$0")/env.sh"; log="$RUN/${1:?log}"
echo "== relay lines ($log)"
grep -nE "boss_kill|the partner killed|leaves it out|holding t0[2-8]|set t0[2-8]|a subset of the world|can't bind|far ahead|bound the file|wrote [0-9]+ bytes|waits for|stage change starts|Warn|enemy sync off|not acked|other REL build" "$log" | tail -200
echo "== ISI/DSI / exceptions (rules: sweep after every live session)"
for f in "$log" "$log.err" "$RUN"/ud-A/Logs/dolphin.log "$RUN"/ud-B/Logs/dolphin.log "$RUN"/appdata/*/logs/latest/*.log; do
  [ -f "$f" ] && grep -HnE "ISI|DSI|JUTException|Invalid read|Invalid write|panic:" "$f" | head -20
done
echo "== done"
