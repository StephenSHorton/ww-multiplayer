# wind#91: UDP pose transport under loss (issue #7), synthetic, 2026-11-03

`TestNetemCompare` (mods/multiplayer/net/udp_netem_test.go, opt-in) runs a server, a sender and a receiver
on one Mac. Both clients go through `internal/netem`: the TCP proxy (loss as retransmit stalls, as in
wind#82) and, at its port + 1, the UDP proxy (real drops, may reorder). The sender sends a 42-joint
quantized pose, a position and a place at 30 Hz for 20 s. The receiver polls every 2 ms and notes each
newer position. A **stall** is a gap of more than 100 ms between new frames, about three frames at 30 Hz.
Both directions are shaped, so this is worse than mp-local's `MP_LOCAL_NETEM`, which shapes only B.

```
WW_NETEM_SECS=20 WW_NETEM_COMPARE='rtt=150ms,jitter=20ms;rtt=150ms,jitter=20ms,loss=1%;rtt=150ms,jitter=20ms,loss=5%;rtt=250ms,jitter=30ms,loss=5%' \
  go test -count=1 -timeout 20m -run TestNetemCompare -v ./mods/multiplayer/net/
```

| link | transport | new frames seen (of 600) | gap p50 / p99 / max | stalls > 100 ms | time frozen |
| --- | --- | --- | --- | --- | --- |
| rtt 150, jitter 20 | TCP | 545 | 35 / 82 / 89 ms | 0 | 0 s |
| rtt 150, jitter 20 | UDP | 550 | 35 / 85 / 98 ms | 0 | 0 s |
| rtt 150, jitter 20, 1% | TCP | 472 | 36 / 384 / 437 ms | 10 | 3.5 s |
| rtt 150, jitter 20, 1% | UDP | 525 | 36 / 88 / 96 ms | 0 | 0 s |
| rtt 150, jitter 20, 5% | TCP | 217 | 40 / 404 / 722 ms | 42 | 13.6 s |
| rtt 150, jitter 20, 5% | UDP | 496 | 37 / 104 / 132 ms | 7 | 0.8 s |
| rtt 250, jitter 30, 5% | TCP | 153 | 48 / 487 / 542 ms | 49 | 16.1 s |
| rtt 250, jitter 30, 5% | UDP | 425 | 44 / 124 / 168 ms | 15 | 1.8 s |

- With no loss the two transports are the same: UDP costs nothing on a clean link.
- At 1% loss UDP has no stall at all, where TCP freezes 10 times (3.5 s of 20).
- At 5% TCP freezes for most of the run (13.6 to 16.1 s of 20). UDP's worst gap is 132 to 168 ms: two or three
  frames lost in a row, on both hops. The playout buffer (WW_PLAYOUT_MS, default 100 ms) covers most of that.
- "New frames seen" under 600 with no loss is the 2 ms poll: frames that land together count once.
- The proxy's loss is independent per datagram. Real loss comes in bursts, which this doesn't model. NC-5 (a real WAN run) still stands.

The two-Dolphin check (mp-local, `MP_LOCAL_NETEM` at 1% and 5%, `smooth-steps`) is below once it has run.
