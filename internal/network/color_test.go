package network

import (
	"bytes"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
)

// Tunic colors (docs/09-player-colors.md): the hello preference, the player
// list trailer, and the server's per-name assignment.

var (
	pal   = playercolor.Palette()
	green = pal[0].Color
	red   = pal[1].Color
	blue  = pal[2].Color
)

func TestPlayerListColorTrailerRoundTrip(t *testing.T) {
	in := []PlayerEntry{
		{ID: 3, Name: "alice", Color: red, HasColor: true},
		{ID: 7, Name: "bob", Color: blue, HasColor: true},
	}
	data, err := PlayerListMessage(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParsePlayerList(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != in[0] || out[1] != in[1] {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func TestPlayerListWithoutColorsHasNoTrailer(t *testing.T) {
	// A partly colored list (never sent by the server) and an uncolored
	// one both encode exactly as before docs/09.
	for _, in := range [][]PlayerEntry{
		{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}},
		{{ID: 1, Name: "a", Color: red, HasColor: true}, {ID: 2, Name: "b"}},
	} {
		data, _ := PlayerListMessage(in)
		want := []byte{2, 1, 1, 'a', 2, 1, 'b'}
		if !bytes.Equal(data, want) {
			t.Errorf("list %+v encoded %x, want %x", in, data, want)
		}
	}
	if data, _ := PlayerListMessage(nil); !bytes.Equal(data, []byte{0}) {
		t.Errorf("empty list encoded %x", data)
	}
}

// An old client reads a colored list as the same roster: the trailer is
// just bytes after the last entry.
func TestColoredListParsesForOldReaders(t *testing.T) {
	data, _ := PlayerListMessage([]PlayerEntry{{ID: 1, Name: "a", Color: red, HasColor: true}})
	old := []byte{1, 1, 1, 'a'}
	if !bytes.HasPrefix(data, old) || len(data) != len(old)+2+4 {
		t.Fatalf("colored list %x does not extend the old encoding %x by one trailer", data, old)
	}
}

func TestBadColorTrailerIsIgnoredNotFatal(t *testing.T) {
	base := []byte{2, 1, 1, 'a', 2, 1, 'b'}
	for name, tail := range map[string][]byte{
		"wrong tag":        {0xC1, 2, 1, 9, 9, 9, 2, 9, 9, 9},
		"wrong count":      {0xC0, 1, 1, 9, 9, 9},
		"short":            {0xC0, 2, 1, 9, 9, 9, 2, 9},
		"ids out of order": {0xC0, 2, 2, 9, 9, 9, 1, 9, 9, 9},
		"unknown id":       {0xC0, 2, 1, 9, 9, 9, 5, 9, 9, 9},
	} {
		out, err := ParsePlayerList(append(append([]byte(nil), base...), tail...))
		if err != nil || len(out) != 2 {
			t.Errorf("%s: roster dropped: %v %+v", name, err, out)
			continue
		}
		if out[0].HasColor || out[1].HasColor {
			t.Errorf("%s: took colors from a bad trailer: %+v", name, out)
		}
	}
}

func TestHelloColorPreference(t *testing.T) {
	if h := HelloPlayerMessage(ProtocolVersion, nil); !bytes.Equal(h, HelloMessage(ProtocolVersion)) {
		t.Errorf("no-preference hello changed: %x", h)
	}
	if ParseHelloColor(HelloMessage(ProtocolVersion)) != nil {
		t.Error("a 2-byte hello parsed as carrying a color")
	}
	pref := playercolor.Color{R: 1, G: 2, B: 3}
	h := HelloPlayerMessage(ProtocolVersion, &pref)
	// A server from before colors reads it as a player hello.
	v, role, caps, err := ParseHelloRole(h)
	if err != nil || v != ProtocolVersion || role != RolePlayer || caps != 0 {
		t.Fatalf("color hello as role hello = %d %d %d %v", v, role, caps, err)
	}
	if got := ParseHelloColor(h); got == nil || *got != pref {
		t.Errorf("ParseHelloColor = %v, want %v", got, pref)
	}
	// A co-op hello is not a color hello.
	if ParseHelloColor(HelloRoleMessage(ProtocolVersion, RoleCoop, 0x8001)) != nil {
		t.Error("a co-op hello parsed as carrying a color")
	}
}

func TestRemoteTunicColorFallback(t *testing.T) {
	rp := RemotePlayer{ID: 2}
	if rp.TunicColor() != playercolor.ForID(2) {
		t.Error("no server color should fall back to ForID")
	}
	rp.Color, rp.HasColor = blue, true
	if rp.TunicColor() != blue {
		t.Error("server color ignored")
	}
}

func colorClient(t *testing.T, addr, name string, pref *playercolor.Color) *Client {
	t.Helper()
	c := NewClient(name)
	c.SetColorPreference(pref)
	if err := c.Connect(addr); err != nil {
		t.Fatalf("%s connect: %v", name, err)
	}
	return c
}

func myColor(t *testing.T, c *Client) playercolor.Color {
	t.Helper()
	var got playercolor.Color
	if !waitFor(2*time.Second, func() bool {
		var ok bool
		got, ok = c.MyColor()
		return ok
	}) {
		t.Fatalf("%s never learned its color", c.name)
	}
	return got
}

func TestServerAssignsDistinctColorsPerName(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	srv, addr := mustListen(t)
	defer srv.Stop()

	alice := colorClient(t, addr, "alice", nil)
	defer alice.Disconnect()
	if got := myColor(t, alice); got != green {
		t.Errorf("first player got %v, want green", got)
	}

	// The same human's second connection shares the color.
	aliceTwin := colorClient(t, addr, "alice", &blue)
	defer aliceTwin.Disconnect()
	if got := myColor(t, aliceTwin); got != green {
		t.Errorf("alice's twin got %v, want alice's green", got)
	}

	bob := colorClient(t, addr, "bob", nil)
	defer bob.Disconnect()
	if got := myColor(t, bob); got != red {
		t.Errorf("second player got %v, want red", got)
	}

	// carol asks for bob's red: moved to the next free palette color.
	carol := colorClient(t, addr, "carol", &red)
	defer carol.Disconnect()
	if got := myColor(t, carol); got != blue {
		t.Errorf("clashing preference got %v, want blue", got)
	}

	// Everyone sees everyone's color.
	if !waitFor(2*time.Second, func() bool {
		seen := map[string]playercolor.Color{}
		for _, rp := range alice.GetRemotePlayers() {
			if rp.HasColor {
				seen[rp.Name] = rp.Color
			}
		}
		return seen["bob"] == red && seen["carol"] == blue && seen["alice"] == green
	}) {
		t.Errorf("alice's view of the roster colors: %+v", alice.GetRemotePlayers())
	}
}

func TestServerHonorsFreePreferenceAndRemembersIt(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	srv, addr := mustListen(t)
	defer srv.Stop()

	purple := pal[3].Color
	dave := colorClient(t, addr, "dave", &purple)
	if got := myColor(t, dave); got != purple {
		t.Fatalf("free preference got %v, want purple", got)
	}
	dave.Disconnect()
	if !waitFor(2*time.Second, func() bool { return srv.PlayerCount() == 0 }) {
		t.Fatal("dave never left")
	}

	// dave reconnects without a preference: he keeps purple.
	dave = colorClient(t, addr, "dave", nil)
	defer dave.Disconnect()
	if got := myColor(t, dave); got != purple {
		t.Errorf("reconnect got %v, want the remembered purple", got)
	}
}

func TestPickColorLockedIsBounded(t *testing.T) {
	s := NewServer(0)
	for i := 0; i < maxRememberedColors+10; i++ {
		s.pickColorLocked(string(rune('A'+i%26))+string(rune('a'+i/26)), nil)
	}
	if len(s.colorByName) > maxRememberedColors {
		t.Errorf("colorByName grew to %d", len(s.colorByName))
	}
}

// TestPoseAppearanceByteRoundTrips: docs/09 §5.4 appends a 17th face
// byte (appearance flags, bit 0 = casual clothes). It must survive the
// pose payload as-is, and stay inside MaxFaceBytes.
func TestPoseAppearanceByteRoundTrips(t *testing.T) {
	face := make([]byte, 17)
	for i := range face {
		face[i] = byte(i + 1)
	}
	face[16] = 0x01
	m := make([]byte, 42*48)
	p := SerializePose(42, m, face)
	if p == nil {
		t.Fatal("SerializePose refused a 17-byte face trailer")
	}
	j, mm, f, err := DeserializePose(p)
	if err != nil || j != 42 || len(mm) != len(m) || !bytes.Equal(f, face) {
		t.Fatalf("round trip: joints %d face %x err %v", j, f, err)
	}
	if len(face) > MaxFaceBytes {
		t.Fatalf("17 > MaxFaceBytes %d", MaxFaceBytes)
	}
}
