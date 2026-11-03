// Package netem is a userspace TCP proxy that makes a loopback link behave
// like an internet one: a round-trip delay, jitter, and the stalls packet
// loss causes on TCP (issue #15, wind#82).
//
// It never drops, reorders or alters bytes, because TCP never shows that to
// the application. Loss reaches a TCP application as a retransmit stall: the
// stream freezes for about one retransmission timeout, then everything behind
// the lost segment arrives at once. The proxy models a lost segment that way:
// with probability Loss per forwarded chunk, the chunk (and so everything
// after it) is held an extra RTO. It is an approximation of the kernel
// shaper (dummynet), chosen because it needs no root and leaves nothing
// behind on the Mac once it stops.
//
// Each direction is delayed by RTT/2 plus a uniform jitter in [-Jitter,
// +Jitter]. A chunk is never sent before the one in front of it
// (send = max(target, previous send)), so jitter delays bursts but never
// reorders them.
package netem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Params is a link's shape.
type Params struct {
	RTT    time.Duration // round trip; each direction gets RTT/2
	Jitter time.Duration // each chunk's delay varies by up to ±Jitter
	Loss   float64       // chance per chunk of a retransmit stall, 0..1
	RTO    time.Duration // a stall's length; 0 picks DefaultRTO
	Seed   uint64        // 0 picks a random seed
}

// DefaultRTO is a stall's length when Params.RTO is 0: one RTT plus the
// larger of 200 ms (the usual minimum retransmission timeout) and four
// jitters, as RFC 6298's RTO = SRTT + max(G, 4*RTTVAR) would give.
func (p Params) DefaultRTO() time.Duration {
	return p.RTT + max(200*time.Millisecond, 4*p.Jitter)
}

func (p Params) rto() time.Duration {
	if p.RTO > 0 {
		return p.RTO
	}
	return p.DefaultRTO()
}

// String is the Params in ParseParams' form.
func (p Params) String() string {
	return fmt.Sprintf("rtt=%v,jitter=%v,loss=%g,rto=%v", p.RTT, p.Jitter, p.Loss, p.rto())
}

// ParseParams reads "rtt=150ms,jitter=20ms,loss=0.01[,rto=400ms][,seed=7]".
// Every key is optional; loss may also be a percentage ("5%").
func ParseParams(s string) (Params, error) {
	var p Params
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return p, fmt.Errorf("netem: %q: want key=value", kv)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		var err error
		switch k {
		case "rtt":
			p.RTT, err = time.ParseDuration(v)
		case "jitter":
			p.Jitter, err = time.ParseDuration(v)
		case "rto":
			p.RTO, err = time.ParseDuration(v)
		case "loss":
			if pct, isPct := strings.CutSuffix(v, "%"); isPct {
				p.Loss, err = strconv.ParseFloat(pct, 64)
				p.Loss /= 100
			} else {
				p.Loss, err = strconv.ParseFloat(v, 64)
			}
		case "seed":
			p.Seed, err = strconv.ParseUint(v, 10, 64)
		default:
			return p, fmt.Errorf("netem: unknown key %q (want rtt, jitter, loss, rto, seed)", k)
		}
		if err != nil {
			return p, fmt.Errorf("netem: %s=%q: %v", k, v, err)
		}
	}
	switch {
	case p.RTT < 0 || p.Jitter < 0 || p.RTO < 0:
		return p, errors.New("netem: rtt, jitter and rto can't be negative")
	case p.Jitter > p.RTT/2:
		return p, fmt.Errorf("netem: jitter %v is more than half the rtt (%v): a one-way delay would go below 0", p.Jitter, p.RTT)
	case p.Loss < 0 || p.Loss > 1:
		return p, fmt.Errorf("netem: loss %g is outside 0..1", p.Loss)
	}
	return p, nil
}

// Stats counts what a proxy did, over all its connections.
type Stats struct {
	Conns  int64 // connections accepted
	Chunks int64 // chunks forwarded, both directions
	Bytes  int64 // bytes forwarded, both directions
	Up     int64 // of Bytes, client to server
	Stalls int64 // retransmit stalls applied
	// DelaySum is the total delay the chunks were given (send - read),
	// stalls included; DelaySum/Chunks is the mean one-way delay.
	DelaySum time.Duration
}

func (s Stats) String() string {
	mean := time.Duration(0)
	if s.Chunks > 0 {
		mean = s.DelaySum / time.Duration(s.Chunks)
	}
	return fmt.Sprintf("conns %d, chunks %d, bytes %d (up %d, down %d), stalls %d, mean one-way %v",
		s.Conns, s.Chunks, s.Bytes, s.Up, s.Bytes-s.Up, s.Stalls, mean.Round(time.Millisecond))
}

// Proxy forwards each connection it accepts to To through a shaped link.
type Proxy struct {
	To     string
	Params Params

	ln     net.Listener
	seed   atomic.Uint64
	conns  atomic.Int64
	chunks atomic.Int64
	bytes  atomic.Int64
	up     atomic.Int64
	stalls atomic.Int64
	delay  atomic.Int64

	mu   sync.Mutex
	open map[net.Conn]struct{}
	wg   sync.WaitGroup
}

// Listen starts a proxy on listen (e.g. "127.0.0.1:0") toward to. Serve runs
// it; Close stops it and every connection.
func Listen(listen, to string, p Params) (*Proxy, error) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, err
	}
	px := &Proxy{To: to, Params: p, ln: ln, open: map[net.Conn]struct{}{}}
	seed := p.Seed
	if seed == 0 {
		seed = rand.Uint64()
	}
	px.seed.Store(seed)
	return px, nil
}

// Addr is where the proxy listens.
func (px *Proxy) Addr() net.Addr { return px.ln.Addr() }

// Stats is a snapshot of the counters.
func (px *Proxy) Stats() Stats {
	return Stats{
		Conns:    px.conns.Load(),
		Chunks:   px.chunks.Load(),
		Bytes:    px.bytes.Load(),
		Up:       px.up.Load(),
		Stalls:   px.stalls.Load(),
		DelaySum: time.Duration(px.delay.Load()),
	}
}

// Serve accepts until ctx ends or Close is called. It returns nil then.
func (px *Proxy) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { px.Close() })
	defer stop()
	for {
		c, err := px.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				px.wg.Wait()
				return nil
			}
			return err
		}
		px.conns.Add(1)
		px.wg.Add(1)
		go func() {
			defer px.wg.Done()
			px.handle(c)
		}()
	}
}

// Close stops listening and closes every open connection.
func (px *Proxy) Close() error {
	err := px.ln.Close()
	px.mu.Lock()
	for c := range px.open {
		c.Close()
	}
	px.mu.Unlock()
	return err
}

func (px *Proxy) track(c net.Conn, on bool) {
	px.mu.Lock()
	defer px.mu.Unlock()
	if on {
		px.open[c] = struct{}{}
	} else {
		delete(px.open, c)
	}
}

func (px *Proxy) handle(client net.Conn) {
	server, err := net.DialTimeout("tcp", px.To, 5*time.Second)
	if err != nil {
		client.Close()
		return
	}
	px.track(client, true)
	px.track(server, true)
	defer func() {
		px.track(client, false)
		px.track(server, false)
		client.Close()
		server.Close()
	}()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); px.pipe(server, client, px.seed.Add(1), true) }()
	go func() { defer wg.Done(); px.pipe(client, server, px.seed.Add(1), false) }()
	wg.Wait()
}

type chunk struct {
	data []byte
	at   time.Time // when it may be written
	read time.Time
}

// pipe copies src to dst through the shaped link, then half-closes dst.
// up is the client-to-server direction.
func (px *Proxy) pipe(dst, src net.Conn, seed uint64, up bool) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	p := px.Params
	oneWay := p.RTT / 2
	q := make(chan chunk, 4096)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for c := range q {
			if d := time.Until(c.at); d > 0 {
				time.Sleep(d)
			}
			if _, err := dst.Write(c.data); err != nil {
				src.Close()   // end the reader too
				for range q { // drain so the reader isn't stuck
				}
				return
			}
			px.delay.Add(int64(time.Since(c.read)))
		}
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		} else {
			dst.Close()
		}
	}()
	var last time.Time
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			now := time.Now()
			d := oneWay
			if p.Jitter > 0 {
				d += time.Duration(rng.Int64N(int64(2*p.Jitter)+1)) - p.Jitter
			}
			if p.Loss > 0 && rng.Float64() < p.Loss {
				d += p.rto()
				px.stalls.Add(1)
			}
			at := now.Add(d)
			if at.Before(last) {
				at = last
			}
			last = at
			px.chunks.Add(1)
			px.bytes.Add(int64(n))
			if up {
				px.up.Add(int64(n))
			}
			q <- chunk{data: append([]byte(nil), buf[:n]...), at: at, read: now}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// A reset or a Close: stop the other side too.
				dst.Close()
			}
			break
		}
	}
	close(q)
	<-done
}
