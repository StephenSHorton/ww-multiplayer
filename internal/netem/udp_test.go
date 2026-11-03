package netem

import (
	"context"
	"net"
	"testing"
	"time"
)

// udpEcho is a UDP server that answers each datagram with itself.
func udpEcho(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			c.WriteToUDP(buf[:n], from)
		}
	}()
	return c
}

func udpProxy(t *testing.T, to string, p Params) *UDPProxy {
	t.Helper()
	px, err := ListenUDP("127.0.0.1:0", to, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go px.Serve(ctx)
	t.Cleanup(cancel)
	return px
}

// TestUDPProxyDelays: with no loss every datagram comes back, about one
// RTT later.
func TestUDPProxyDelays(t *testing.T) {
	echo := udpEcho(t)
	px := udpProxy(t, echo.LocalAddr().String(), Params{RTT: 80 * time.Millisecond, Seed: 1})
	c, err := net.DialUDP("udp", nil, px.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	c.Write([]byte("hi"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	n, err := c.Read(buf)
	if err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("read %q, %v", buf[:n], err)
	}
	if rtt := time.Since(start); rtt < 75*time.Millisecond || rtt > 500*time.Millisecond {
		t.Fatalf("round trip %v, want about 80ms", rtt)
	}
}

// TestUDPProxyDrops: at 20% loss each way about 64% of echoes come back,
// and the counters see the drops.
func TestUDPProxyDrops(t *testing.T) {
	echo := udpEcho(t)
	px := udpProxy(t, echo.LocalAddr().String(), Params{Loss: 0.2, Seed: 7})
	c, err := net.DialUDP("udp", nil, px.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	const n = 1000
	got := make(chan int, 1)
	go func() {
		buf := make([]byte, 16)
		k := 0
		for {
			c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			if _, err := c.Read(buf); err != nil {
				got <- k
				return
			}
			k++
		}
	}()
	for i := 0; i < n; i++ {
		c.Write([]byte{byte(i)})
		if i%50 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	k := <-got
	if k < 560 || k > 720 {
		t.Fatalf("%d of %d came back, want about 640", k, n)
	}
	if st := px.Stats(); st.Dropped < 250 || st.Peers != 1 {
		t.Fatalf("stats %v", st)
	}
}

func TestUDPBeside(t *testing.T) {
	if a, err := UDPBeside("127.0.0.1:25565"); err != nil || a != "127.0.0.1:25566" {
		t.Fatalf("%q %v", a, err)
	}
	if _, err := UDPBeside("nope"); err == nil {
		t.Fatal("no error for a bad address")
	}
}
