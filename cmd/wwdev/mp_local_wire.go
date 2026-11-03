package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	network "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// mpLocalWireEvery reads MP_WIRE_STATS=<secs>: mp-local's server counts
// every connection's frames (network.WireStats, wind#84) and logs each
// player's up and down rates per stream every <secs> seconds, then the
// whole run's at teardown. Unset, 0 or not a number = off.
func mpLocalWireEvery(rep log.Reporter) time.Duration {
	v := os.Getenv("MP_WIRE_STATS")
	if v == "" {
		return 0
	}
	secs, err := strconv.ParseFloat(v, 64)
	if err != nil || secs <= 0 {
		log.Logf(rep, log.Warn, "MP_WIRE_STATS=%q is not a number of seconds above 0: wire stats off", v)
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// logWireStats logs one [wire] line per player every interval, and the
// totals since the start once ctx ends.
func logWireStats(ctx context.Context, rep log.Reporter, w *network.WireStats, every time.Duration, names ...string) {
	start := time.Now()
	prev, prevAt := w.Snapshot(), start
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			cur := w.Snapshot()
			secs := time.Since(start).Seconds()
			for _, n := range names {
				rep.Log(log.Info, fmt.Sprintf("run %.0f s, %s", secs, network.FormatWireRates(nil, cur, n, secs)))
			}
			return
		case now := <-t.C:
			cur := w.Snapshot()
			secs := now.Sub(prevAt).Seconds()
			for _, n := range names {
				rep.Log(log.Info, fmt.Sprintf("%.0f s, %s", secs, network.FormatWireRates(prev, cur, n, secs)))
			}
			prev, prevAt = cur, now
		}
	}
}
