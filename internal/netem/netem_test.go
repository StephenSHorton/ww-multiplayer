package netem

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestParseParams(t *testing.T) {
	p, err := ParseParams("rtt=150ms, jitter=20ms,loss=5%,seed=7")
	if err != nil {
		t.Fatal(err)
	}
	if p.RTT != 150*time.Millisecond || p.Jitter != 20*time.Millisecond || p.Loss != 0.05 || p.Seed != 7 {
		t.Fatalf("got %+v", p)
	}
	if got, want := p.rto(), 350*time.Millisecond; got != want {
		t.Fatalf("default rto %v, want %v (rtt + max(200ms, 4*jitter))", got, want)
	}
	if p, _ := ParseParams("rtt=80ms,loss=0.01,rto=1s"); p.rto() != time.Second || p.Loss != 0.01 {
		t.Fatalf("explicit rto/loss: %+v", p)
	}
	for _, bad := range []string{"rtt", "rtt=fast", "speed=1", "loss=2", "rtt=20ms,jitter=15ms", "rtt=-1ms"} {
		if _, err := ParseParams(bad); err == nil {
			t.Errorf("ParseParams(%q): want an error", bad)
		}
	}
}

// echoServer echoes every connection until the listener closes.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String()
}

func startProxy(t *testing.T, to string, p Params) *Proxy {
	t.Helper()
	px, err := Listen("127.0.0.1:0", to, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { px.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return px
}

// roundTrip writes msg through c and reads it back, returning how long it took.
func roundTrip(t *testing.T, c net.Conn, msg []byte) time.Duration {
	t.Helper()
	start := time.Now()
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo changed the bytes: %q != %q", got, msg)
	}
	return time.Since(start)
}

// TestRTT: an echo through the proxy takes about RTT (both directions shaped).
func TestRTT(t *testing.T) {
	px := startProxy(t, echoServer(t), Params{RTT: 60 * time.Millisecond, Seed: 1})
	c, err := net.Dial("tcp", px.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := range 5 {
		d := roundTrip(t, c, []byte("ping"))
		if d < 55*time.Millisecond || d > 400*time.Millisecond {
			t.Fatalf("round trip %d took %v, want about 60ms", i, d)
		}
	}
	if s := px.Stats(); s.Conns != 1 || s.Chunks != 10 || s.Stalls != 0 || s.Bytes != 40 || s.Up != 20 {
		t.Fatalf("stats %v", s)
	}
}

// TestOrderUnderJitter: with jitter as large as it may be, a stream of
// small writes still arrives whole and in order.
func TestOrderUnderJitter(t *testing.T) {
	px := startProxy(t, echoServer(t), Params{RTT: 20 * time.Millisecond, Jitter: 10 * time.Millisecond, Seed: 2})
	c, err := net.Dial("tcp", px.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var want bytes.Buffer
	go func() {
		for i := range 300 {
			b := []byte{byte(i), byte(i >> 8)}
			c.Write(b)
			time.Sleep(200 * time.Microsecond)
		}
	}()
	for i := range 300 {
		want.Write([]byte{byte(i), byte(i >> 8)})
	}
	got := make([]byte, want.Len())
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatal("bytes arrived out of order or changed")
	}
}

// TestStalls: loss=1 stalls every chunk by the RTO; loss=0.3 stalls about
// that share of them.
func TestStalls(t *testing.T) {
	px := startProxy(t, echoServer(t), Params{RTT: 10 * time.Millisecond, Loss: 1, RTO: 50 * time.Millisecond, Seed: 3})
	c, err := net.Dial("tcp", px.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if d := roundTrip(t, c, []byte("x")); d < 110*time.Millisecond {
		t.Fatalf("loss=1 round trip %v, want at least rtt + 2 stalls (110ms)", d)
	}
	c.Close()

	px = startProxy(t, echoServer(t), Params{Loss: 0.3, RTO: time.Millisecond, Seed: 4})
	c, err = net.Dial("tcp", px.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for range 200 {
		roundTrip(t, c, []byte("y"))
	}
	s := px.Stats()
	if share := float64(s.Stalls) / float64(s.Chunks); share < 0.2 || share > 0.4 {
		t.Fatalf("stall share %.2f over %d chunks, want about 0.3", share, s.Chunks)
	}
}

// TestCloseEndsConnections: Close ends Serve and the open connections.
func TestCloseEndsConnections(t *testing.T) {
	px, err := Listen("127.0.0.1:0", echoServer(t), Params{})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- px.Serve(context.Background()) }()
	c, err := net.Dial("tcp", px.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	roundTrip(t, c, []byte("z"))
	px.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("a connection stayed open after Close")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}
