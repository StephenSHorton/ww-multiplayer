package network

import (
	"sync"
	"testing"
	"time"
)

// TestChatRelayRoundTrip exercises the [nameLen][name][text] payload helpers,
// including the too-short / malformed guards ParseChatRelay must survive.
func TestChatRelayRoundTrip(t *testing.T) {
	cases := []struct{ name, text string }{
		{"Link", "hello"},
		{"", "no-name"},
		{"Zelda", ""},
		{"weird: name", "text with : colons"}, // name containing ": " must round-trip
	}
	for _, c := range cases {
		gotName, gotText := ParseChatRelay(ChatRelayMessage(c.name, c.text))
		if gotName != c.name || gotText != c.text {
			t.Errorf("round-trip(%q,%q) = (%q,%q)", c.name, c.text, gotName, gotText)
		}
	}

	// Malformed payloads must yield empty strings, never panic.
	for _, bad := range [][]byte{nil, {}, {5, 'a'}, {200}} {
		if n, tx := ParseChatRelay(bad); n != "" || tx != "" {
			t.Errorf("ParseChatRelay(%v) = (%q,%q), want empty", bad, n, tx)
		}
	}
}

// chatRecorder captures OnChat callbacks (invoked from the read-loop goroutine)
// under a lock so tests can assert on them safely.
type chatRecorder struct {
	mu  sync.Mutex
	got []string // "from|text"
}

func (r *chatRecorder) fn() func(from, text string) {
	return func(from, text string) {
		r.mu.Lock()
		r.got = append(r.got, from+"|"+text)
		r.mu.Unlock()
	}
}
func (r *chatRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}
func (r *chatRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

// TestChatRelayReachesOthersNotSender is the end-to-end relay guarantee: a chat
// from one client reaches OTHER players via the server, is NOT echoed back to
// the sender's own connection, and is self-filtered by the sender's co-located
// twin (a second connection under the same name).
func TestChatRelayReachesOthersNotSender(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 20 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	var aliceRec, bobRec, twinRec chatRecorder

	alice := NewClient("alice")
	alice.OnChat = aliceRec.fn()
	if err := alice.Connect(addr); err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	defer alice.Disconnect()

	bob := NewClient("bob")
	bob.OnChat = bobRec.fn()
	if err := bob.Connect(addr); err != nil {
		t.Fatalf("bob connect: %v", err)
	}
	defer bob.Disconnect()

	// alice's co-located second connection (same display name).
	twin := NewClient("alice")
	twin.OnChat = twinRec.fn()
	if err := twin.Connect(addr); err != nil {
		t.Fatalf("twin connect: %v", err)
	}
	defer twin.Disconnect()

	if !waitFor(2*time.Second, func() bool { return srv.PlayerCount() == 3 }) {
		t.Fatalf("server never saw 3 connections (got %d)", srv.PlayerCount())
	}

	if err := alice.SendChat("hello"); err != nil {
		t.Fatalf("alice send: %v", err)
	}

	// A different human receives it exactly once, with the authoritative name.
	if !waitFor(2*time.Second, func() bool { return bobRec.len() >= 1 }) {
		t.Fatal("bob never received alice's chat")
	}
	if got := bobRec.snapshot(); len(got) != 1 || got[0] != "alice|hello" {
		t.Fatalf("bob got %v, want [alice|hello]", got)
	}

	// Let any stray relay arrive before asserting its absence.
	time.Sleep(100 * time.Millisecond)

	if n := aliceRec.len(); n != 0 {
		t.Fatalf("sender received her own chat %d time(s): %v", n, aliceRec.snapshot())
	}
	if n := twinRec.len(); n != 0 {
		t.Fatalf("same-name twin surfaced the sender's own chat %d time(s): %v", n, twinRec.snapshot())
	}
}
