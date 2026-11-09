package netem

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// UDPProxy is the datagram side of a shaped link (wind#91): what TCP turns
// into a retransmit stall, UDP shows as a missing datagram. Each datagram,
// either way, is dropped with probability Loss, else delayed by RTT/2 plus a
// uniform jitter in [-Jitter, +Jitter]. Unlike the TCP proxy, nothing keeps
// datagrams in order: a jittered one can overtake the one before it, as on
// the internet. RTO is not used.
//
// Each client source address gets its own socket toward To, so the server
// sees one peer per client and its answers find their way back.
type UDPProxy struct {
	To     *net.UDPAddr
	Params Params

	conn *net.UDPConn
	rng  *rand.Rand
	rngM sync.Mutex

	mu    sync.Mutex
	peers map[string]*net.UDPConn
	wg    sync.WaitGroup

	datagrams, dropped, bytes, up atomic.Int64
	delay                         atomic.Int64
}

// UDPStats counts what a UDP proxy did, both directions.
type UDPStats struct {
	Peers     int
	Datagrams int64 // forwarded
	Dropped   int64
	Bytes     int64 // forwarded
	Up        int64 // of Bytes, client to server
	DelaySum  time.Duration
}

func (s UDPStats) String() string {
	mean := time.Duration(0)
	if s.Datagrams > 0 {
		mean = s.DelaySum / time.Duration(s.Datagrams)
	}
	loss := 0.0
	if n := s.Datagrams + s.Dropped; n > 0 {
		loss = 100 * float64(s.Dropped) / float64(n)
	}
	return fmt.Sprintf("udp peers %d, datagrams %d, dropped %d (%.1f%%), bytes %d (up %d, down %d), mean one-way %v",
		s.Peers, s.Datagrams, s.Dropped, loss, s.Bytes, s.Up, s.Bytes-s.Up, mean.Round(time.Millisecond))
}

// ListenUDP starts a UDP proxy on listen toward to. Serve runs it; Close
// stops it.
func ListenUDP(listen, to string, p Params) (*UDPProxy, error) {
	la, err := net.ResolveUDPAddr("udp", listen)
	if err != nil {
		return nil, err
	}
	ta, err := net.ResolveUDPAddr("udp", to)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", la)
	if err != nil {
		return nil, err
	}
	seed := p.Seed
	if seed == 0 {
		seed = rand.Uint64()
	}
	return &UDPProxy{
		To: ta, Params: p, conn: conn,
		rng:   rand.New(rand.NewPCG(seed, seed^0x75647021)),
		peers: map[string]*net.UDPConn{},
	}, nil
}

// Addr is where the proxy listens.
func (px *UDPProxy) Addr() *net.UDPAddr { return px.conn.LocalAddr().(*net.UDPAddr) }

// Stats is a snapshot of the counters.
func (px *UDPProxy) Stats() UDPStats {
	px.mu.Lock()
	n := len(px.peers)
	px.mu.Unlock()
	return UDPStats{
		Peers: n, Datagrams: px.datagrams.Load(), Dropped: px.dropped.Load(),
		Bytes: px.bytes.Load(), Up: px.up.Load(), DelaySum: time.Duration(px.delay.Load()),
	}
}

// Close stops the proxy and its peer sockets.
func (px *UDPProxy) Close() error {
	err := px.conn.Close()
	px.mu.Lock()
	for _, c := range px.peers {
		c.Close()
	}
	px.mu.Unlock()
	return err
}

// shape picks a datagram's fate: drop, or the delay to send it after.
func (px *UDPProxy) shape() (drop bool, d time.Duration) {
	px.rngM.Lock()
	defer px.rngM.Unlock()
	if px.Params.Loss > 0 && px.rng.Float64() < px.Params.Loss {
		return true, 0
	}
	d = px.Params.RTT / 2
	if j := px.Params.Jitter; j > 0 {
		d += time.Duration(px.rng.Int64N(int64(2*j)+1)) - j
	}
	return false, max(d, 0)
}

// forward sends b to dst through write after its shaped delay, or drops it.
func (px *UDPProxy) forward(b []byte, up bool, write func([]byte)) {
	drop, d := px.shape()
	if drop {
		px.dropped.Add(1)
		return
	}
	px.datagrams.Add(1)
	px.bytes.Add(int64(len(b)))
	if up {
		px.up.Add(int64(len(b)))
	}
	px.delay.Add(int64(d))
	b = append([]byte(nil), b...)
	if d == 0 {
		write(b)
		return
	}
	time.AfterFunc(d, func() { write(b) })
}

// Serve forwards until ctx ends or Close is called; it returns nil then.
func (px *UDPProxy) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { px.Close() })
	defer stop()
	buf := make([]byte, 64<<10)
	for {
		n, from, err := px.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				px.wg.Wait()
				return nil
			}
			continue // ICMP from a gone client and the like
		}
		peer, err := px.peer(from)
		if err != nil {
			continue
		}
		px.forward(buf[:n], true, func(b []byte) { peer.Write(b) })
	}
}

// peer returns from's socket toward To, opening it (and its return loop)
// on the first datagram.
func (px *UDPProxy) peer(from *net.UDPAddr) (*net.UDPConn, error) {
	key := from.String()
	px.mu.Lock()
	defer px.mu.Unlock()
	if c := px.peers[key]; c != nil {
		return c, nil
	}
	c, err := net.DialUDP("udp", nil, px.To)
	if err != nil {
		return nil, err
	}
	px.peers[key] = c
	px.wg.Add(1)
	go func() {
		defer px.wg.Done()
		buf := make([]byte, 64<<10)
		for {
			n, err := c.Read(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			px.forward(buf[:n], false, func(b []byte) { px.conn.WriteToUDP(b, from) })
		}
	}()
	return c, nil
}

// UDPBeside is the UDP address beside a TCP one: the same host, port + 1,
// where the game server keeps its UDP pose socket (docs/12-udp-pose.md).
func UDPBeside(tcpAddr string) (string, error) {
	host, port, err := net.SplitHostPort(tcpAddr)
	if err != nil {
		return "", err
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil || p <= 0 || p >= 0xFFFF {
		return "", fmt.Errorf("netem: bad port in %q", tcpAddr)
	}
	return net.JoinHostPort(host, fmt.Sprint(p+1)), nil
}
