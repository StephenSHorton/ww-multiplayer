#!/usr/bin/env python3
"""analyze.py <label>...: one summary row per condition of wind#82's battery."""
import re, sys, os

os.chdir(os.path.dirname(os.path.abspath(__file__)))


def lines(path):
    try:
        with open(path, errors="replace") as f:
            return f.read().splitlines()
    except FileNotFoundError:
        return []


def ts_lines(label):
    out = []
    for l in lines(f"ts-{label}.log"):
        t, _, rest = l.partition(" ")
        try:
            out.append((float(t), rest))
        except ValueError:
            pass
    return out


def marks(label):
    m = []
    for l in lines(f"marks-{label}.log"):
        t, _, rest = l.partition(" MARK ")
        m.append((float(t), rest))
    return m


def first(ts, pat, after=0.0):
    r = re.compile(pat)
    for t, l in ts:
        if t >= after and r.search(l):
            return t, l
    return None, None


for label in sys.argv[1:]:
    ts, mk = ts_lines(label), marks(label)
    log = lines(f"mp-{label}.log")
    print(f"== {label}")
    for l in lines(f"fps-{label}.txt"):
        print("  fps:", l)
    for l in lines(f"smooth-{label}.txt"):
        print("  walk", l)
    # kills: first and last kill marks, then when B's mirror slots were all empty
    kills = [t for t, m in mk if m.startswith("kill")]
    if kills:
        t0 = kills[0]
        tB, _ = first(ts, r"\[enemy-B\].*adopted 0000", t0)
        tA, _ = first(ts, r"\[enemy-A\] enemy host: 0 enemies", t0)
        print(f"  kills: {len(kills)} poked; host room empty +{(tA or 0)-t0:.1f}s, B copies all gone +{(tB or 0)-t0:.1f}s (1 s log grain)")
    for l in lines(f"kills-{label}.txt"):
        print("  kills:", l)
    # chest race: who won, and the grant's trip to each player
    tc = next((t for t, m in mk if m.startswith("chest")), None)
    if tc:
        tg, lg = first(ts, r"\[server\] co-op: chest claim: granted", tc)
        if tg:
            who = re.search(r"granted (\S+)", lg).group(1)
            side = "B" if who == "PlayerB" else "A"
            tr, _ = first(ts, rf"\[coop-{side}\] co-op: chest claims: the host granted", tc)
            loser = "A" if side == "B" else "B"
            td, _ = first(ts, rf"\[coop-{loser}\] co-op: chest claims: the host denied", tc)
            print(f"  chest: granted {who} +{tg-tc:.2f}s after the warps; grant reached {side} +{(tr or tg)-tg:.3f}s; deny reached {loser} +{((td or tg)-tg):.3f}s after the grant")
        else:
            print("  chest: no grant")
    tp = next((t for t, m in mk if m == "boss poke"), None)
    if tp:
        tb, _ = first(ts, r"\[coop-B\] co-op: set t03 DRC boss defeated", tp)
        print(f"  boss: B set DRC boss defeated +{(tb - tp) if tb else float('nan'):.1f}s after A's poke (includes A's death demo)")
    # trouble
    pats = {
        "disconnects": r"disconnected",
        "reconnects": r"[Rr]econnect",
        "timeouts": r"timeout|deadline",
        "freeze/despawn": r"freez|despawn",
        "errors": r"^ERR|\bERROR\b|panic",
        "warnings": r"^WARN",
        "reader stalls": r"stall|stale",
    }
    cut = (mk[-1][0] + 5) if mk else float("inf")
    live = [l for t, l in ts if t < cut]
    for k, p in pats.items():
        n = sum(1 for l in live if re.search(p, l))
        if n:
            print(f"  {k}: {n}")
    for l in log:
        if "[netem]" in l:
            print("  ", l)
    for l in lines(f"crash-{label}.txt"):
        print("  CRASH:", l)
