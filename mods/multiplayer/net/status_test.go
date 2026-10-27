package network

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStatusProbe: the probe gets this server's version, protocol and
// counts (names, not connections; live lobby members), and the server
// logs nothing for it.
func TestStatusProbe(t *testing.T) {
	srv, addr := listenLobby(t, time.Second)
	srv.AppVersion = "v9.9.9-test"
	var mu sync.Mutex
	var logs []string
	srv.OnLog = func(s string) { mu.Lock(); logs = append(logs, s); mu.Unlock() }
	if err := srv.SetOwnerKey(testOwnerKey); err != nil {
		t.Fatal(err)
	}

	b, err := ProbeStatus(context.Background(), addr, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.App != StatusApp || b.Version != "v9.9.9-test" || b.Protocol != ProtocolVersion || b.Players != 0 ||
		b.Lobby != 0 || b.Phase != PhaseLobby || !b.OwnerKey {
		t.Fatalf("empty server: %+v", b)
	}

	// One CLI player with its two connections, and one lobby member.
	connectPlayer(t, addr, "Ann")
	connectPlayer(t, addr, "Ann")
	l := NewLobbyClient("Bo", LobbyHelloBody{})
	runLobby(t, l, addr)
	if !waitFor(5*time.Second, func() bool {
		b, err = ProbeStatus(context.Background(), addr, 0)
		return err == nil && b.Players == 1 && b.Lobby == 1
	}) {
		t.Fatalf("with players: %+v %v", b, err)
	}
	if srv.PlayerCount() != 2 {
		t.Fatalf("the probes became players: %d", srv.PlayerCount())
	}

	mu.Lock()
	defer mu.Unlock()
	for _, line := range logs {
		if strings.Contains(line, "status") || strings.Contains(line, "refused") {
			t.Fatalf("the probe was logged: %q", line)
		}
	}
}

// TestStatusProbeLegacyServer: a server from before the probe closes the
// connection at the unknown first frame (handlePlayer's MsgJoin check);
// the prober says so instead of a dial error.
func TestStatusProbeLegacyServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				// The pre-probe handlePlayer: read one frame, not a join,
				// return (and close).
				if msg, err := ReadMessage(conn); err == nil && msg.Type == MsgJoin {
					preLobbyServer(conn)
				}
			}()
		}
	}()
	_, err = ProbeStatus(context.Background(), ln.Addr().String(), 2*time.Second)
	if !errors.Is(err, ErrStatusLegacy) {
		t.Fatalf("legacy server: %v", err)
	}
}

// TestStatusProbeNothingThere: a closed port is a dial error, not
// ErrStatusLegacy; a silent listener times out.
func TestStatusProbeNothingThere(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if _, err := ProbeStatus(context.Background(), addr, time.Second); err == nil || errors.Is(err, ErrStatusLegacy) {
		t.Fatalf("closed port: %v", err)
	}

	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	start := time.Now()
	if _, err := ProbeStatus(context.Background(), silent.Addr().String(), 200*time.Millisecond); err == nil || errors.Is(err, ErrStatusLegacy) {
		t.Fatalf("silent listener: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the timeout took %v", d)
	}
}

// TestStatusProbeOtherAnswer: a status frame from something that isn't
// ww-multiplayer, or one that doesn't decode, is an error.
func TestStatusProbeOtherAnswer(t *testing.T) {
	for _, body := range []string{`{"app":"other","protocol":3}`, `nope`} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			ReadMessage(c)
			WriteMessage(c, MsgStatus, []byte(body))
		}()
		if _, err := ProbeStatus(context.Background(), ln.Addr().String(), time.Second); err == nil || errors.Is(err, ErrStatusLegacy) {
			t.Fatalf("%s: %v", body, err)
		}
		ln.Close()
	}
}

// TestStatusProbeNotAnswer: another program that replies (an HTTP server, a
// frame cut off after its header or mid-header) is ErrStatusNotAnswer, not
// ErrStatusLegacy; only a close with not one byte sent is legacy
// (docs/11-self-hosting.md §5: exit 1 and exit 3).
func TestStatusProbeNotAnswer(t *testing.T) {
	for _, c := range []struct {
		name   string
		reply  string
		legacy bool
	}{
		{"http", "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n", false},
		{"header only", "S\x00\x10", false},
		{"half a header", "S\x00", false},
		{"nothing", "", true},
	} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func(reply string) {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			c.Read(make([]byte, 3))
			c.Write([]byte(reply))
		}(c.reply)
		_, err = ProbeStatus(context.Background(), ln.Addr().String(), 2*time.Second)
		if c.legacy {
			if !errors.Is(err, ErrStatusLegacy) {
				t.Errorf("%s: %v, want ErrStatusLegacy", c.name, err)
			}
		} else if !errors.Is(err, ErrStatusNotAnswer) || errors.Is(err, ErrStatusLegacy) {
			t.Errorf("%s: %v, want ErrStatusNotAnswer", c.name, err)
		}
		ln.Close()
	}
}
