package network

import (
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"
)

// udpTestPose is a 42-joint pose that quantizes: identity rotations and small
// translations, so its frame fits one datagram.
func udpTestPose(shift float32) []byte {
	const joints = 42
	m := make([]byte, joints*48)
	for j := 0; j < joints; j++ {
		for r := 0; r < 3; r++ {
			for c := 0; c < 4; c++ {
				v := float32(0)
				if r == c {
					v = 1
				}
				if c == 3 {
					v = shift + float32(j+r)
				}
				binary.BigEndian.PutUint32(m[(j*12+r*4+c)*4:], math.Float32bits(v))
			}
		}
	}
	return m
}

// udpSession starts a UDP server (or a TCP-only one) and returns it with
// its address.
func udpSession(t *testing.T, udp bool) (*Server, string) {
	t.Helper()
	s := NewServer(0)
	s.UDP = udp
	s.Wire = NewWireStats()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s, s.Addr().String()
}

func udpClient(t *testing.T, name, addr string, udp bool) *Client {
	t.Helper()
	c := NewClient(name)
	c.EnableUDP(udp)
	c.SkipSelfEcho()
	if err := c.Connect(addr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Disconnect)
	return c
}

func remoteOf(c *Client, id byte) (RemotePlayer, bool) {
	for _, rp := range c.GetRemotePlayers() {
		if rp.ID == id {
			return rp, true
		}
	}
	return RemotePlayer{}, false
}

func wireFrames(s *Server, name string, up bool, typ byte) int64 {
	return s.Wire.Snapshot()[WireKey{Name: name, Role: RolePlayer, Up: up, Type: typ}].Frames
}

// TestUDPFramesRelayed: with UDP at both ends, a frame goes up and down as
// datagrams and lands as the remote's position, place, pose and one playout
// sample, under the sender's seq.
func TestUDPFramesRelayed(t *testing.T) {
	s, addr := udpSession(t, true)
	if s.UDPAddr() == nil {
		t.Fatal("server has no UDP socket")
	}
	send := udpClient(t, "Ann", addr, true)
	recv := udpClient(t, "Bob", addr, true)
	if !waitFor(3*time.Second, func() bool { return send.UDPUp() && recv.UDPUp() }) {
		t.Fatalf("UDP not up: sender %v receiver %v", send.UDPUp(), recv.UDPUp())
	}
	if !waitFor(2*time.Second, func() bool { return s.udpLiveCount() == 2 }) {
		t.Fatal("server never saw both clients' UDP")
	}
	place := Place{Stage: "sea", Room: 3, InPlay: true}
	for i := 1; i <= 5; i++ {
		if err := send.SendFrame(&PlayerPosition{PosX: float32(i)}, place, true, 42, udpTestPose(float32(i)), []byte{1, 2, 3, 4}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waitFor(2*time.Second, func() bool {
		rp, ok := remoteOf(recv, send.MyID())
		return ok && rp.Position != nil && rp.Position.PosX == 5 && rp.PoseMatrices != nil
	}) {
		t.Fatal("receiver never got frame 5")
	}
	rp, _ := remoteOf(recv, send.MyID())
	if !rp.HasPlace || rp.Place != place {
		t.Errorf("place = %+v %v, want %+v", rp.Place, rp.HasPlace, place)
	}
	if string(rp.FaceState) != "\x01\x02\x03\x04" {
		t.Errorf("face = %x", rp.FaceState)
	}
	if _, _, _, ok := poseClose(rp.PoseMatrices, udpTestPose(5)); !ok {
		t.Error("pose differs from what was sent")
	}
	if n := len(rp.Recent); n == 0 || rp.Recent[n-1].PoseMatrices == nil || rp.Recent[n-1].Pos.PosX != 5 {
		t.Errorf("newest sample = %+v", rp.Recent)
	}
	for i := 1; i < len(rp.Recent); i++ {
		if rp.Recent[i].Seq <= rp.Recent[i-1].Seq {
			t.Errorf("Recent seqs not increasing: %d then %d", rp.Recent[i-1].Seq, rp.Recent[i].Seq)
		}
	}
	if up, down := wireFrames(s, "Ann", true, wireUDPPose), wireFrames(s, "Bob", false, wireUDPPose); up != 5 || down != 5 {
		t.Errorf("udp-pose frames up %d down %d, want 5 and 5", up, down)
	}
	if tcp := wireFrames(s, "Ann", true, MsgPose); tcp != 0 {
		t.Errorf("%d poses went over TCP", tcp)
	}
}

// TestUDPFallsBackToTCP: a client asking for UDP from a server without it
// gets no UDP tail and sends over TCP, as before.
func TestUDPFallsBackToTCP(t *testing.T) {
	s, addr := udpSession(t, false)
	send := udpClient(t, "Ann", addr, true)
	recv := udpClient(t, "Bob", addr, true)
	time.Sleep(300 * time.Millisecond)
	if send.UDPUp() || recv.UDPUp() {
		t.Fatal("UDP up against a server without it")
	}
	if err := send.SendFrame(&PlayerPosition{PosX: 7}, Place{}, false, 42, udpTestPose(0), nil); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool {
		rp, ok := remoteOf(recv, send.MyID())
		return ok && rp.Position != nil && rp.Position.PosX == 7 && rp.PoseMatrices != nil
	}) {
		t.Fatal("frame never arrived over TCP")
	}
	if n := wireFrames(s, "Ann", true, MsgPose); n != 1 {
		t.Errorf("TCP poses up = %d, want 1", n)
	}
}

// TestUDPUnreachableStaysOnTCP: when the server's UDP port can't be
// reached (here: closed after the welcome, as a firewall would drop it),
// the clients never go up and frames still arrive over TCP.
func TestUDPUnreachableStaysOnTCP(t *testing.T) {
	s, addr := udpSession(t, true)
	s.mu.Lock()
	s.udpConn.Close()
	s.mu.Unlock()
	send := udpClient(t, "Ann", addr, true)
	recv := udpClient(t, "Bob", addr, true)
	time.Sleep(400 * time.Millisecond)
	if send.UDPUp() || recv.UDPUp() {
		t.Fatal("UDP up with the server's socket closed")
	}
	if err := send.SendFrame(&PlayerPosition{PosX: 9}, Place{}, false, 42, udpTestPose(0), nil); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool {
		rp, ok := remoteOf(recv, send.MyID())
		return ok && rp.Position != nil && rp.Position.PosX == 9
	}) {
		t.Fatal("frame never arrived over TCP")
	}
}

// TestUDPMixedReceivers: a UDP sender's frame reaches a TCP-only receiver
// as MsgPosition + MsgPose.
func TestUDPMixedReceivers(t *testing.T) {
	_, addr := udpSession(t, true)
	send := udpClient(t, "Ann", addr, true)
	old := udpClient(t, "Cid", addr, false)
	if !waitFor(3*time.Second, send.UDPUp) {
		t.Fatal("sender UDP not up")
	}
	if err := send.SendFrame(&PlayerPosition{PosX: 4}, Place{Stage: "sea"}, true, 42, udpTestPose(0), nil); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool {
		rp, ok := remoteOf(old, send.MyID())
		return ok && rp.Position != nil && rp.Position.PosX == 4 && rp.PoseMatrices != nil && rp.HasPlace
	}) {
		t.Fatal("TCP receiver never got the UDP sender's frame")
	}
}

// TestUDPServerRefusesStaleAndForged: the server drops a FRAME with a
// stale seq and one under an unknown token.
func TestUDPServerRefusesStaleAndForged(t *testing.T) {
	s, addr := udpSession(t, true)
	send := udpClient(t, "Ann", addr, true)
	recv := udpClient(t, "Bob", addr, true)
	if !waitFor(3*time.Second, func() bool { return send.UDPUp() && recv.UDPUp() }) {
		t.Fatal("UDP not up")
	}
	send.mu.RLock()
	l := send.udp
	send.mu.RUnlock()
	frame := func(token uint64, seq uint32, x float32) []byte {
		b := binary.BigEndian.AppendUint64(udpHeader(udpKindFrame), token)
		b = binary.BigEndian.AppendUint32(b, seq)
		return appendFrameBody(b, &PlayerPosition{PosX: x}, Place{}, false, nil)
	}
	posX := func() float32 {
		rp, ok := remoteOf(recv, send.MyID())
		if !ok || rp.Position == nil {
			return -1
		}
		return rp.Position.PosX
	}
	l.conn.Write(frame(l.token, 10, 10))
	if !waitFor(2*time.Second, func() bool { return posX() == 10 }) {
		t.Fatalf("seq 10 not relayed (x %v)", posX())
	}
	l.conn.Write(frame(l.token, 9, 9))     // stale
	l.conn.Write(frame(l.token^1, 11, 11)) // forged token
	l.conn.Write(frame(l.token, 10, 10.5)) // duplicate seq
	l.conn.Write(frame(l.token, 12, 12))   // fresh, behind them
	if !waitFor(2*time.Second, func() bool { return posX() == 12 }) {
		t.Fatalf("seq 12 not relayed (x %v)", posX())
	}
	if n := wireFrames(s, "Bob", false, wireUDPPos); n != 2 {
		t.Errorf("relayed %d frames, want 2 (seq 10 and 12)", n)
	}
}

// TestAddUDPFrameOrdering: newest wins; a late frame within the window is
// inserted in order; a duplicate or an older one is dropped; a gap keeps its
// width in Seq; a big fall back is a restart; a TCP sample in between keeps
// Seq rising.
func TestAddUDPFrameOrdering(t *testing.T) {
	rp := &RemotePlayer{}
	now := time.Now()
	add := func(seq uint32, x float32) bool {
		return rp.addUDPFrame(seq, &udpFrame{pos: &PlayerPosition{PosX: x}}, now)
	}
	seqs := func() (out []uint64) {
		for _, s := range rp.Recent {
			out = append(out, s.Seq)
		}
		return
	}
	xs := func() (out []float32) {
		for _, s := range rp.Recent {
			out = append(out, s.Pos.PosX)
		}
		return
	}
	for _, s := range []uint32{100, 101, 103} {
		if !add(s, float32(s)) {
			t.Fatalf("seq %d not newest", s)
		}
	}
	if add(102, 102) { // late, within the window
		t.Fatal("late seq 102 taken as newest")
	}
	if add(102, 0) || add(103, 0) { // duplicates
		t.Fatal("a duplicate was taken as newest")
	}
	add(90, 90) // older than the window: dropped
	if got, want := xs(), []float32{100, 101, 102, 103}; !equalF(got, want) {
		t.Fatalf("Recent x = %v, want %v", got, want)
	}
	if rp.Position.PosX != 103 {
		t.Fatalf("Position = %v, want 103", rp.Position.PosX)
	}
	add(110, 110) // a gap of 7
	s := seqs()
	if s[len(s)-1]-s[len(s)-2] != 7 {
		t.Fatalf("gap not kept: %v", s)
	}
	// TCP sample, then UDP again from a restarted sender at seq 1.
	rp.addSample(&PlayerPosition{PosX: 200}, now)
	add(1, 1)
	add(2, 2)
	s = seqs()
	for i := 1; i < len(s); i++ {
		if s[i] <= s[i-1] {
			t.Fatalf("Seq not rising across TCP and a restart: %v", s)
		}
	}
	if rp.Position.PosX != 2 {
		t.Fatalf("Position = %v, want 2", rp.Position.PosX)
	}
	// A restart without a TCP sample in between: seq falls far back.
	add(50000, 5)
	add(3, 3)
	if rp.Position.PosX != 3 {
		t.Fatalf("restart not taken: Position = %v", rp.Position.PosX)
	}
}

func equalF(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWelcomeUDPTail: old readers still read the welcome; the tail
// round-trips; a welcome without it has none.
func TestWelcomeUDPTail(t *testing.T) {
	w := WelcomeMessageUDP(7, 99, 0xFFFF, 0xABCDEF)
	if id, epoch, ok := ParseWelcome(w); id != 7 || epoch != 99 || !ok {
		t.Fatalf("ParseWelcome = %d %d %v", id, epoch, ok)
	}
	if v, ok := WelcomeProtocolVersion(w); !ok || v != ProtocolVersion {
		t.Fatalf("version = %d %v", v, ok)
	}
	if off, tok, ok := ParseWelcomeUDP(w); !ok || off != 0xFFFF || tok != 0xABCDEF {
		t.Fatalf("tail = %d %x %v", off, tok, ok)
	}
	if _, _, ok := ParseWelcomeUDP(WelcomeMessage(7, 99)); ok {
		t.Fatal("tail found in a plain welcome")
	}
}

// TestUDPOffsetFollowsTheDialedPort: the client sends UDP to the dialed
// port + the offset, so a proxy in front of the server stays in the path.
func TestUDPOffsetFollowsTheDialedPort(t *testing.T) {
	c := NewClient("Ann")
	c.EnableUDP(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if conn, err := ln.Accept(); err == nil {
			defer conn.Close()
			time.Sleep(time.Second)
		}
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	l := c.startUDP(conn, WelcomeMessageUDP(1, 1, 3, 42))
	if l == nil {
		t.Fatal("no UDP link")
	}
	defer l.conn.Close()
	want := ln.Addr().(*net.TCPAddr).Port + 3
	if got := l.conn.RemoteAddr().(*net.UDPAddr).Port; got != want {
		t.Fatalf("UDP target port %d, want %d", got, want)
	}
}

// udpLiveCount counts players whose UDP is live (tests).
func (s *Server) udpLiveCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, p := range s.players {
		if p.udp.live(time.Now()) {
			n++
		}
	}
	return n
}
