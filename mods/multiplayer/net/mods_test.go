package network

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// testMods is a known Mods for list (already sorted), hashed as
// wwapi.ModsIDOf hashes it.
func testMods(list ...string) Mods {
	return Mods{Hash: sha256.Sum256([]byte(strings.Join(list, "\n"))), List: list, Known: true}
}

var (
	modsBoth = testMods("coop@1.0.0", "multiplayer@1.0.0")
	modsMP   = testMods("multiplayer@1.0.0")
)

// TestHelloModsForm pins the player hello with a mods hash:
// [ver][role 0][caps CapModsHash][hasColor][rgb][hash:32].
func TestHelloModsForm(t *testing.T) {
	h := modsBoth.Hash
	pref := playercolor.Color{R: 1, G: 2, B: 3}
	for _, c := range []struct {
		name  string
		pref  *playercolor.Color
		color []byte
	}{
		{"no color", nil, []byte{0, 0, 0, 0}},
		{"color", &pref, []byte{1, 1, 2, 3}},
	} {
		got := HelloPlayerModsMessage(ProtocolVersion, c.pref, &h)
		want := append([]byte{0, byte(ProtocolVersion), RolePlayer, 0, byte(CapModsHash)}, c.color...)
		want = append(want, h[:]...)
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: hello = %x, want %x", c.name, got, want)
		}
		v, role, caps, err := ParseHelloRole(got)
		if err != nil || v != ProtocolVersion || role != RolePlayer || caps != CapModsHash {
			t.Fatalf("%s: ParseHelloRole = %d %d %#x %v", c.name, v, role, caps, err)
		}
		if hash, ok := ParseHelloModsHash(got); !ok || hash != h {
			t.Fatalf("%s: ParseHelloModsHash = %x %v", c.name, hash, ok)
		}
		if col := ParseHelloColor(got); (col == nil) != (c.pref == nil) || (col != nil && *col != pref) {
			t.Fatalf("%s: ParseHelloColor = %v", c.name, col)
		}
		// Cut off inside the hash: malformed.
		if _, _, _, err := ParseHelloRole(got[:len(got)-1]); err == nil {
			t.Fatalf("%s: a hello cut off in its hash parsed", c.name)
		}
		if _, ok := ParseHelloModsHash(got[:len(got)-1]); ok {
			t.Fatalf("%s: a cut-off hash was returned", c.name)
		}
	}
	// Without a hash the forms are today's.
	if got := HelloPlayerModsMessage(ProtocolVersion, nil, nil); !bytes.Equal(got, HelloMessage(ProtocolVersion)) {
		t.Errorf("no color, no hash = %x", got)
	}
	if got := HelloPlayerModsMessage(ProtocolVersion, &pref, nil); !bytes.Equal(got, HelloPlayerMessage(ProtocolVersion, &pref)) || len(got) != helloColorLen {
		t.Errorf("color, no hash = %x", got)
	}
	// Caps bit 0 means CapModsHash only for a player: a lobby hello with
	// LobbyCapJSON carries no hash and is not cut off.
	lobby := HelloRoleMessage(ProtocolVersion, RoleLobby, LobbyCapJSON)
	if _, _, _, err := ParseHelloRole(lobby); err != nil {
		t.Errorf("lobby hello: %v", err)
	}
	if _, ok := ParseHelloModsHash(append(lobby, make([]byte, 40)...)); ok {
		t.Error("a lobby hello returned a mods hash")
	}
	// No CapModsHash: the bytes after the color block are not a hash.
	if _, ok := ParseHelloModsHash(append(HelloPlayerMessage(ProtocolVersion, &pref), h[:]...)); ok {
		t.Error("a hello without CapModsHash returned a hash")
	}
}

// oldHostServer mimics a host from before mods (protocol 2 with colors):
// it reads the role and caps, ignores every byte after the color block
// and the caps bits, and welcomes the player. It reports the hello.
func oldHostServer(hellos chan<- []byte) func(net.Conn) {
	return func(conn net.Conn) {
		if msg, err := ReadMessage(conn); err != nil || msg.Type != MsgJoin {
			return
		}
		hello, err := ReadMessage(conn)
		if err != nil || hello.Type != MsgHello || len(hello.Data) < 2 {
			return
		}
		hellos <- hello.Data
		if binary.BigEndian.Uint16(hello.Data) != ProtocolVersion {
			return
		}
		if len(hello.Data) >= helloRoleLen && hello.Data[2] != RolePlayer {
			return
		}
		WriteMessage(conn, MsgWelcome, WelcomeMessage(1, 7))
		drain(conn)
	}
}

// TestModsNewClientOldHost: a client with a hash joins a host from before
// mods, which ignores the caps and the hash.
func TestModsNewClientOldHost(t *testing.T) {
	for _, pref := range []*playercolor.Color{nil, {R: 9, G: 8, B: 7}} {
		hellos := make(chan []byte, 1)
		addr := serveOnce(t, oldHostServer(hellos))
		c := NewClient("Link")
		c.SetColorPreference(pref)
		c.SetMods(modsBoth)
		if err := c.Connect(addr); err != nil {
			t.Fatalf("pref %v: Connect = %v", pref, err)
		}
		c.Disconnect()
		h := <-hellos
		if len(h) != helloModsLen {
			t.Fatalf("pref %v: hello is %d bytes, want %d", pref, len(h), helloModsLen)
		}
		// What the old host made of it: a player, and the color (or none).
		if got := ParseHelloColor(h); (got == nil) != (pref == nil) || (got != nil && *got != *pref) {
			t.Fatalf("pref %v: the old host reads color %v", pref, got)
		}
	}
}

// TestModsOldClientNewHost: a host with mods admits clients that send no
// hash (the 2-byte hello and the color hello of older builds).
func TestModsOldClientNewHost(t *testing.T) {
	srv, addr := mustListen(t)
	defer srv.Stop()
	srv.SetMods(modsBoth)
	pref := playercolor.Color{R: 1, G: 2, B: 3}
	for _, p := range []*playercolor.Color{nil, &pref} {
		c := NewClient("Old")
		c.SetColorPreference(p)
		if err := c.Connect(addr); err != nil {
			t.Fatalf("old client (color %v): Connect = %v", p, err)
		}
		c.Disconnect()
	}
}

// TestModsMatchAndMismatch: the same hash joins; another is refused with
// RejectMods, fatal, naming the host's list against the client's.
func TestModsMatchAndMismatch(t *testing.T) {
	srv, addr := mustListen(t)
	defer srv.Stop()
	srv.SetMods(modsBoth)

	ok := NewClient("Same")
	ok.SetMods(modsBoth)
	if err := ok.Connect(addr); err != nil {
		t.Fatalf("matching mods: Connect = %v", err)
	}
	ok.Disconnect()

	bad := NewClient("Other")
	bad.SetMods(modsMP)
	err := bad.Connect(addr)
	var rej *RejectError
	if !errors.As(err, &rej) || rej.Code != RejectMods || !errors.Is(err, ErrFatal) {
		t.Fatalf("other mods: Connect = %v, want a fatal RejectMods", err)
	}
	want := "host refused the connection: the mods differ: the host runs coop@1.0.0, multiplayer@1.0.0; you run multiplayer@1.0.0. Both players need the same mods turned on."
	if err.Error() != want {
		t.Fatalf("reason = %q\nwant     %q", err.Error(), want)
	}

	// A host without mods (the zero Mods) checks nothing.
	srv2, addr2 := mustListen(t)
	defer srv2.Stop()
	free := NewClient("Any")
	free.SetMods(modsMP)
	if err := free.Connect(addr2); err != nil {
		t.Fatalf("host without mods: Connect = %v", err)
	}
	free.Disconnect()

	// A raw hello whose CapModsHash cuts off its hash is malformed.
	conn := dialRaw(t, addr)
	h := modsBoth.Hash
	cut := HelloPlayerModsMessage(ProtocolVersion, nil, &h)
	conn.Write(append(frame(MsgJoin, []byte("cut")), frame(MsgHello, cut[:len(cut)-3])...))
	if reason := expectReject(t, conn, RejectVersion); !strings.Contains(reason, "mods hash") {
		t.Fatalf("cut-off hash: reason %q", reason)
	}
}

// TestLobbyModsHash: a lobby HELLO with another ModsHash is refused with
// NoticeModsDiffer (fatal, the host's list against ours); a HELLO without
// one, or with the same, joins.
func TestLobbyModsHash(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	srv.SetMods(modsBoth)

	same := NewLobbyClient("Same", LobbyHelloBody{})
	same.SetMods(modsBoth)
	runLobby(t, same, addr)
	waitState(t, same, "same mods joined", func(st LobbyStateBody) bool { return len(st.Members) == 1 })

	old := NewLobbyClient("Old", LobbyHelloBody{})
	runLobby(t, old, addr)
	waitState(t, old, "no hash joined", func(st LobbyStateBody) bool { return len(st.Members) == 2 })

	other := NewLobbyClient("Other", LobbyHelloBody{})
	other.SetMods(modsMP)
	ow := watchLobby(other)
	err := runLobby(t, other, addr).result(t)
	var ne *LobbyNoticeError
	if !errors.As(err, &ne) || ne.Notice.Code != NoticeModsDiffer || !errors.Is(err, ErrFatal) {
		t.Fatalf("Run = %v, want a fatal mods_differ", err)
	}
	want := "the mods differ: the host runs coop@1.0.0, multiplayer@1.0.0; you run multiplayer@1.0.0. Both players need the same mods turned on."
	if ne.Notice.Text != want || ow.lastNotice().Text != want {
		t.Fatalf("notice %q (OnNotice %q)\nwant %q", ne.Notice.Text, ow.lastNotice().Text, want)
	}
	if st, _ := same.State(); len(st.Members) != 2 {
		t.Fatalf("members after the refusal: %+v", st.Members)
	}
}

// TestLobbyHelloModsHashWire: the field's JSON name, omitted when empty,
// and an old host's decoder (no such field) takes the rest of the HELLO.
func TestLobbyHelloModsHashWire(t *testing.T) {
	h := LobbyHelloBody{App: "v0.4.0", ModsHash: modsBoth.ModsHashHex()}
	data, err := EncodeLobby(LobbyHello, &h)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"modsHash":"`+modsBoth.ModsHashHex()+`"`)) {
		t.Fatalf("HELLO = %s", data)
	}
	_, body, _ := ParseLobby(data)
	var back LobbyHelloBody
	if err := DecodeLobby(body, &back); err != nil || back != h {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	var oldHello struct {
		Token, App, OS, Color, Dolphin string
	}
	if err := DecodeLobby(body, &oldHello); err != nil || oldHello.App != "v0.4.0" {
		t.Fatalf("an old host's decode = %+v, %v", oldHello, err)
	}
	if empty, _ := json.Marshal(LobbyHelloBody{}); bytes.Contains(empty, []byte("modsHash")) {
		t.Fatalf("an empty hash is sent: %s", empty)
	}
	if len(modsBoth.ModsHashHex()) != 2*ModsHashLen || (Mods{}).ModsHashHex() != "" {
		t.Fatal("ModsHashHex")
	}
}
