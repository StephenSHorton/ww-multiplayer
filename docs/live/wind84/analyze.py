#!/usr/bin/env python3
"""analyze.py <mp-LABEL.ts>...: the joiner's (PlayerB's) wire rates per phase (wind#84).

Reads measure.sh's timestamped log: MARK lines and mp-local's [wire] lines
(MP_WIRE_STATS=5). A 5 s window counts toward the phase it ends in, and only
when it starts after that phase's mark (the hop's load is left out).
Prints, per phase, the mean up and down kbit/s and the biggest streams."""
import re, sys
from collections import defaultdict

PHASES = [("sea idle", "sea idle", "walk A"), ("sea walking", "walk A", "hop r6"), ("DRC room 6", "drc idle", "drc end")]

def parse(path):
    marks, wins = {}, []
    for line in open(path, errors="replace"):
        m = re.match(r"([\d.]+) MARK (.*)", line)
        if m:
            marks.setdefault(m.group(2).strip(), float(m.group(1)))
            continue
        m = re.match(r"([\d.]+) .*\[wire\] ([\d.]+) s, PlayerB: up (\d+) kbit/s, down (\d+) kbit/s \|(.*)", line)
        if m:
            t, secs = float(m.group(1)), float(m.group(2))
            streams = {}
            for d, name, v in re.findall(r" (up|down) (\S+) ([\d.]+)", m.group(5)):
                streams[d + " " + name] = float(v)
            wins.append((t - secs, t, int(m.group(3)), int(m.group(4)), streams))
    return marks, wins

for path in sys.argv[1:]:
    marks, wins = parse(path)
    print(path)
    for label, a, b in PHASES:
        if a not in marks or b not in marks:
            continue
        sel = [w for w in wins if w[0] >= marks[a] - 0.5 and w[1] <= marks[b] + 0.5]
        if not sel:
            print("  %-12s no whole window" % label)
            continue
        up = sum(w[2] for w in sel) / len(sel)
        down = sum(w[3] for w in sel) / len(sel)
        agg = defaultdict(float)
        for w in sel:
            for k, v in w[4].items():
                agg[k] += v / len(sel)
        top = sorted(agg.items(), key=lambda kv: -kv[1])[:6]
        print("  %-12s %d windows: up %4.0f, down %4.0f kbit/s | %s" % (label, len(sel), up, down,
              ", ".join("%s %.1f" % kv for kv in top)))
