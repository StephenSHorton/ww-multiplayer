package network

import (
	"context"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/netem"
)

// TestNetemCompare (wind#91) streams 30 Hz frames from a sender to a
// receiver, both through a shaped link (internal/netem: the TCP proxy and,
// beside it, the UDP one), once over TCP and once over UDP, and reports how
// smoothly new frames reach the receiver: the gaps between them, the stalls
// (gaps over 100 ms) and the longest. Opt-in: WW_NETEM_COMPARE=<spec>[;<spec>...]
// (netem.ParseParams), WW_NETEM_SECS (default 20).
func TestNetemCompare(t *testing.T) {
	specs := os.Getenv("WW_NETEM_COMPARE")
	if specs == "" {
		t.Skip("set WW_NETEM_COMPARE=rtt=150ms,jitter=20ms,loss=5%[;...] to run")
	}
	secs := 20
	fmt.Sscanf(os.Getenv("WW_NETEM_SECS"), "%d", &secs)
	for _, spec := range strings.Split(specs, ";") {
		p, err := netem.ParseParams(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, udp := range []bool{false, true} {
			r := netemRun(t, p, udp, time.Duration(secs)*time.Second)
			t.Logf("%-40s %s  %s", spec, map[bool]string{false: "TCP", true: "UDP"}[udp], r)
		}
	}
}

type netemResult struct {
	sent, seen int
	gaps       []time.Duration
	stalls     int
	stallTime  time.Duration
	udpUp      bool
}

func (r netemResult) String() string {
	sort.Slice(r.gaps, func(i, j int) bool { return r.gaps[i] < r.gaps[j] })
	q := func(f float64) time.Duration {
		if len(r.gaps) == 0 {
			return 0
		}
		return r.gaps[min(len(r.gaps)-1, int(f*float64(len(r.gaps))))].Round(time.Millisecond)
	}
	return fmt.Sprintf("sent %d, new frames seen %d, gap p50 %v p99 %v max %v, stalls>100ms %d (%v frozen), udp up %v",
		r.sent, r.seen, q(0.5), q(0.99), q(1), r.stalls, r.stallTime.Round(time.Millisecond), r.udpUp)
}

func netemRun(t *testing.T, p netem.Params, udp bool, dur time.Duration) netemResult {
	t.Helper()
	s := NewServer(0)
	s.UDP = udp
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := s.Addr().String()
	px, err := netem.Listen("127.0.0.1:0", addr, p)
	if err != nil {
		t.Fatal(err)
	}
	go px.Serve(ctx)
	if udp {
		ua := s.UDPAddr()
		listen, _ := netem.UDPBeside(px.Addr().String())
		// The server's UDP is at its TCP port + its offset; the proxy
		// listens at its own port + the same offset (1 unless the bind
		// fell back).
		if ua.Port != s.Addr().(*net.TCPAddr).Port+1 {
			t.Skip("server UDP not at port + 1")
		}
		upx, err := netem.ListenUDP(listen, ua.String(), p)
		if err != nil {
			t.Fatal(err)
		}
		go upx.Serve(ctx)
	}
	dial := px.Addr().String()
	send := NewClient("Ann")
	send.EnableUDP(udp)
	recv := NewClient("Bob")
	recv.EnableUDP(udp)
	for _, c := range []*Client{send, recv} {
		if err := c.Connect(dial); err != nil {
			t.Fatal(err)
		}
		defer c.Disconnect()
	}
	if udp && !waitFor(5*time.Second, func() bool { return send.UDPUp() && recv.UDPUp() }) {
		t.Fatal("UDP never came up through the proxy")
	}
	time.Sleep(300 * time.Millisecond)

	var r netemResult
	r.udpUp = send.UDPUp() && recv.UDPUp()
	pose := udpTestPose(0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(time.Second / 30)
		defer tk.Stop()
		end := time.Now().Add(dur)
		for i := 1; time.Now().Before(end); i++ {
			<-tk.C
			send.SendFrame(&PlayerPosition{PosX: float32(i)}, Place{Stage: "sea"}, true, 42, pose, nil)
			r.sent = i
		}
	}()
	last, lastAt := float32(0), time.Time{}
	poll := time.NewTicker(2 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-done:
			return r
		case <-poll.C:
		}
		rp, ok := remoteOf(recv, send.MyID())
		if !ok || rp.Position == nil || rp.Position.PosX <= last {
			continue
		}
		now := time.Now()
		if !lastAt.IsZero() {
			g := now.Sub(lastAt)
			r.gaps = append(r.gaps, g)
			if g > 100*time.Millisecond {
				r.stalls++
				r.stallTime += g
			}
		}
		last, lastAt = rp.Position.PosX, now
		r.seen++
	}
}
