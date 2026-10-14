package network

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// blockingDial stands in for a dial to a black-holed address: it never
// connects, and returns only when its ctx ends. entered is closed on the
// first call.
func blockingDial(entered chan struct{}) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

// TestConnectDialTimeout: a dial that never completes is cut off after
// dialTimeout, so one attempt against a wrong or firewalled address can't
// hang for the OS connect timeout.
func TestConnectDialTimeout(t *testing.T) {
	c := NewClient("dialer")
	c.dialTimeout = 50 * time.Millisecond
	c.dialContext = blockingDial(make(chan struct{}))

	start := time.Now()
	err := c.Connect("192.0.2.1:25565")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect = %v, want a dial deadline error", err)
	}
	if elapsed < c.dialTimeout || elapsed > time.Second {
		t.Fatalf("Connect returned after %v, want about dialTimeout (%v)", elapsed, c.dialTimeout)
	}
	if c.IsConnected() {
		t.Fatal("client reports connected after a failed dial")
	}
}

// TestConnectContextCancelDuringDial: cancelling ctx ends a dial in progress
// at once, long before dialTimeout.
func TestConnectContextCancelDuringDial(t *testing.T) {
	c := NewClient("dialer")
	c.dialTimeout = time.Hour
	entered := make(chan struct{})
	c.dialContext = blockingDial(entered)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.ConnectContext(ctx, "192.0.2.1:25565") }()

	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ConnectContext = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not end the dial")
	}
}

// TestConnectContextCancelDuringHandshake: a server that accepts the TCP
// connection but never sends the welcome would hold Connect for the whole
// handshake read deadline. Cancelling ctx must instead close the conn and
// return at once (the TUI Esc / Ctrl+C path), and the server must see the
// conn closed, not leaked.
func TestConnectContextCancelDuringHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	gotJoin := make(chan struct{})
	serverSawClose := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if msg, err := ReadMessage(conn); err != nil || msg.Type != MsgJoin {
			return
		}
		close(gotJoin)
		// Never answer. The next read ends only when the client closes.
		if _, err := conn.Read(make([]byte, 1)); err == io.EOF {
			close(serverSawClose)
		}
	}()

	c := NewClient("joiner")
	c.readTimeout = time.Hour // only the cancel can end the welcome wait
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.ConnectContext(ctx, ln.Addr().String()) }()

	select {
	case <-gotJoin:
	case <-time.After(time.Second):
		t.Fatal("server never received the join")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ConnectContext = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not end the welcome wait")
	}
	if c.IsConnected() {
		t.Fatal("client reports connected after a cancelled handshake")
	}
	select {
	case <-serverSawClose:
	case <-time.After(time.Second):
		t.Fatal("cancelled handshake left the conn open")
	}
}

// TestConnectHandshakeErrorNamesCause: a server that hangs up instead of
// sending the welcome yields an error that says what went wrong, because
// ConnectLog now shows that error to the user.
func TestConnectHandshakeErrorNamesCause(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		ReadMessage(conn)
		conn.Close() // hang up without a welcome
	}()

	err = NewClient("joiner").Connect(ln.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "expected welcome") || !errors.Is(err, io.EOF) {
		t.Fatalf("Connect = %v, want an \"expected welcome\" error wrapping EOF", err)
	}
}
