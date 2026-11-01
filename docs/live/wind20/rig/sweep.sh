#!/usr/bin/env bash
# sweep.sh: the full-log sweep after every live block (rules: ISI/DSI etc. in every log).
. "$(dirname "$0")/env.sh"
echo "== Go logs: stderr and bad lines"; for f in "$RIG_DIR"/logs/*.err; do [ -s "$f" ] && echo "non-empty: $f"; done
grep -n -i -E 'panic|fatal|exception|bug:|error' "$RIG_DIR"/logs/* 2>/dev/null | grep -v -E '\.png:' | head -50
echo "== WARN counts"; grep -h -o -E 'WARN: [^:]+: [a-z ]{0,40}' "$RIG_DIR"/logs/* 2>/dev/null | sort | uniq -c | sort -rn | head -20
echo "== Dolphin logs"; for d in "$RIG_DIR"/u1 "$RIG_DIR"/u2; do l="$d/Logs/dolphin.log"; [ -f "$l" ] || continue
  echo "$l: $(wc -l <"$l") lines"; grep -n -E 'Invalid read|Invalid write|ISI|DSI|xception|getRes|panic|OSReboot' "$l" | tail -20; done
