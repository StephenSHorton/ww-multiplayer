package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/netem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// envNetem shapes Dolphin B's link to mp-local's server (wind#82, issue
// #15): "rtt=150ms,jitter=20ms,loss=1%[,rto=D][,seed=N]" (internal/netem).
// B's clients (bcast-B, puppet-B, enemy-B, coop-B) dial a proxy on
// 127.0.0.1 that adds the delay, jitter and loss stalls both ways; A, the
// host, stays on loopback, as a host and an internet joiner would. Unset or
// empty: no proxy. Nothing outlives mp-local (no pf or dummynet rules).
const envNetem = "MP_LOCAL_NETEM"

// netemStatsEvery is how often mp-local logs the proxy's counters.
var netemStatsEvery = 30 * time.Second

// mpLocalNetem starts MP_LOCAL_NETEM's proxy toward addr and returns the
// address B dials and a stop func. Without the env it returns addr itself.
func mpLocalNetem(ctx context.Context, rep log.Reporter, addr string) (string, func(), error) {
	spec := strings.TrimSpace(os.Getenv(envNetem))
	if spec == "" {
		return addr, func() {}, nil
	}
	p, err := netem.ParseParams(spec)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %v", envNetem, err)
	}
	px, err := netem.Listen("127.0.0.1:0", addr, p)
	if err != nil {
		return "", nil, fmt.Errorf("%s: proxy: %v", envNetem, err)
	}
	r := log.Prefix(rep, "[netem]")
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := px.Serve(pctx); err != nil {
			log.Logf(r, log.Err, "proxy stopped: %v", err)
		}
	}()
	go func() {
		t := time.NewTicker(netemStatsEvery)
		defer t.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-t.C:
				log.Logf(r, log.Info, "%v", px.Stats())
			}
		}
	}()
	log.Logf(r, log.OK, "Dolphin B dials %s, shaped %v, toward %s", px.Addr(), p, addr)
	stop := func() {
		cancel()
		<-done
		log.Logf(r, log.Info, "final: %v", px.Stats())
	}
	return px.Addr().String(), stop, nil
}

// cmdNetem runs the shaped proxy on its own, for a host and a join in
// separate processes: `wwdev netem <listen> <to> <spec>`, e.g.
// `wwdev netem 127.0.0.1:25600 localhost:25565 rtt=150ms,jitter=20ms,loss=1%`,
// then `join 127.0.0.1:25600`. Ctrl+C stops it; it logs its counters every
// 30 s and at the end.
func cmdNetem(_ string, args []string) {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: wwdev netem <listen addr> <server addr> rtt=150ms,jitter=20ms,loss=1%[,rto=D][,seed=N]")
		os.Exit(2)
	}
	p, err := netem.ParseParams(args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	px, err := netem.Listen(args[0], args[1], p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("netem: %s -> %s, %v\n", px.Addr(), args[1], p)
	go func() {
		t := time.NewTicker(netemStatsEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fmt.Printf("netem: %v\n", px.Stats())
			}
		}
	}()
	if err := px.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("netem: final: %v\n", px.Stats())
}
