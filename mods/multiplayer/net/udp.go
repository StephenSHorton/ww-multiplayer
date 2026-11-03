package network

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// UDP pose transport (wind#91, issue #7, docs/12-udp-pose.md). Positions and
// poses go as datagrams, each a whole frame with a sequence number, so a lost
// one costs that frame alone instead of stalling the TCP stream behind it.
// Everything else stays on TCP. Off unless WW_UDP is on at both ends; a
// client that can't reach the server's UDP port keeps sending over TCP.

// CapUDP is caps bit 2 of a player hello: this connection can send and take
// its positions and poses over UDP. A server with UDP up answers it with the
// welcome's UDP tail (WelcomeMessageUDP); any other server ignores it.
const CapUDP uint16 = 1 << 2

// Datagram header: ['W' 'U'][ver][kind].
const (
	udpMagic0  = 'W'
	udpMagic1  = 'U'
	udpVersion = 1

	udpKindFrame = 1
	udpKindPing  = 2
	udpKindPong  = 3

	udpHeaderLen = 4
)

// MaxUDPDatagram caps a datagram, below every path MTU in practice: a frame
// that would not fit goes over TCP instead (a raw, unquantized pose).
const MaxUDPDatagram = 1200

const (
	// udpSeqRestart: a seq this far behind the newest means the sender
	// started counting again (a new connection), not a late datagram.
	udpSeqRestart = 4096
	// UDPReorderWindow: a late frame at most this many behind the newest
	// is still put in the playout buffer, in order.
	UDPReorderWindow = 8
)

// Tunable, like netconfig.go's: tests shorten them.
var (
	// udpLiveFor: a side counts the other's UDP as up while it has heard
	// from it this recently (the client a PONG, the server a PING or a
	// FRAME).
	udpLiveFor = 3 * time.Second
	// udpPingFast / udpPingSlow: the client's PING cadence before the
	// first PONG and after.
	udpPingFast = 250 * time.Millisecond
	udpPingSlow = 1 * time.Second
)

// UDPFromEnv reads WW_UDP: 1, on, true or yes turns the UDP pose transport
// on; anything else (or unset) leaves it off.
func UDPFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WW_UDP"))) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}

// Wire-stats pseudo types for the UDP streams (wirestats.go).
const (
	wireUDPPose byte = 0x01 // a FRAME with its pose
	wireUDPPos  byte = 0x02 // a FRAME without one
	wireUDPPing byte = 0x03 // PING and PONG
)

// WelcomeMessageUDP is WelcomeMessage with the UDP tail:
// [udpOffset:2 BE][token:8]. offset is the UDP port minus the TCP port, mod
// 2^16; the client adds it to the port it dialed.
func WelcomeMessageUDP(id byte, serverEpoch uint64, offset uint16, token uint64) []byte {
	data := WelcomeMessage(id, serverEpoch)
	data = binary.BigEndian.AppendUint16(data, offset)
	return binary.BigEndian.AppendUint64(data, token)
}

// ParseWelcomeUDP reads the welcome's UDP tail; ok is false when there is
// none (the server has no UDP, or the hello didn't ask).
func ParseWelcomeUDP(data []byte) (offset uint16, token uint64, ok bool) {
	if len(data) < 21 {
		return 0, 0, false
	}
	token = binary.BigEndian.Uint64(data[13:21])
	return binary.BigEndian.Uint16(data[11:13]), token, token != 0
}

func udpHeader(kind byte) []byte {
	return []byte{udpMagic0, udpMagic1, udpVersion, kind}
}

// parseUDPHeader returns a datagram's kind and the bytes after the header.
func parseUDPHeader(b []byte) (kind byte, rest []byte, ok bool) {
	if len(b) < udpHeaderLen || b[0] != udpMagic0 || b[1] != udpMagic1 || b[2] != udpVersion {
		return 0, nil, false
	}
	return b[3], b[udpHeaderLen:], true
}

// appendFrameBody appends [posLen:1][position + place trailer][pose]: one
// frame, what TCP carries as a MsgPosition then a MsgPose.
func appendFrameBody(b []byte, pos *PlayerPosition, place Place, hasPlace bool, pose []byte) []byte {
	p := pos.Serialize()
	if hasPlace {
		p = AppendPlace(p, place)
	}
	b = append(b, byte(len(p)))
	b = append(b, p...)
	return append(b, pose...)
}

// udpFrame is a parsed frame body. pose is the SerializePose payload (nil
// when the frame has none); joints, matrices and face are it decoded.
type udpFrame struct {
	pos      *PlayerPosition
	place    Place
	hasPlace bool
	badPlace bool
	pose     []byte
	joints   int
	matrices []byte
	face     []byte
}

// parseFrameBody checks a frame body as the TCP cases check their frames:
// the position must parse; a malformed place trailer is dropped (badPlace),
// not the position; a pose that DeserializePose refuses fails the frame.
func parseFrameBody(b []byte) (udpFrame, error) {
	var f udpFrame
	if len(b) < 1 {
		return f, errors.New("udp frame: empty")
	}
	n := int(b[0])
	if n < 18 || 1+n > len(b) {
		return f, fmt.Errorf("udp frame: position length %d of %d bytes", n, len(b)-1)
	}
	posBytes := b[1 : 1+n]
	f.pos = DeserializePosition(posBytes)
	if n > 18 {
		if f.place, f.hasPlace = ParsePlace(posBytes[18:]); !f.hasPlace {
			f.badPlace = true
		}
	}
	if rest := b[1+n:]; len(rest) > 0 {
		joints, m, face, err := DeserializePose(rest)
		if err != nil {
			return f, fmt.Errorf("udp frame: %w", err)
		}
		f.pose, f.joints, f.matrices, f.face = rest, joints, m, face
	}
	return f, nil
}

// newUDPToken is a nonzero random token.
func newUDPToken() uint64 {
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		if t := binary.BigEndian.Uint64(b[:]); t != 0 {
			return t
		}
	}
}

// ---- server ----

// udpPlayer is a player's UDP state on the server.
type udpPlayer struct {
	token uint64
	addr  atomic.Pointer[net.UDPAddr]
	heard atomic.Int64 // UnixNano of its last PING or FRAME
	// lastSeq is the newest FRAME seq taken (only the UDP loop touches
	// it); haveSeq is false until the first.
	lastSeq uint32
	haveSeq bool
	warned  bool // a bad frame was logged
}

// live reports whether p has a UDP address heard from within udpLiveFor.
func (u *udpPlayer) live(now time.Time) bool {
	return u != nil && u.addr.Load() != nil && now.UnixNano()-u.heard.Load() <= int64(udpLiveFor)
}

// seqNewer reports whether seq is a frame to take after last: newer, or
// so far behind that the sender started over.
func seqNewer(seq, last uint32) bool {
	d := int32(seq - last)
	return d > 0 || d < -udpSeqRestart
}

// startUDP binds the server's UDP socket at the TCP port + 1 on the TCP
// host, or an OS-assigned port when that fails. Failing both leaves UDP off.
func (s *Server) startUDP(host string, tcpPort int) {
	var conn *net.UDPConn
	var err error
	for _, port := range []int{tcpPort + 1, 0} {
		if port > 0xFFFF {
			continue
		}
		addr, rerr := net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(port)))
		if rerr != nil {
			err = rerr
			continue
		}
		if conn, err = net.ListenUDP("udp", addr); err == nil {
			break
		}
	}
	if conn == nil {
		s.log(fmt.Sprintf("UDP pose transport off: %v", err))
		return
	}
	udpPort := conn.LocalAddr().(*net.UDPAddr).Port
	s.mu.Lock()
	s.udpConn = conn
	s.udpOffset = uint16(udpPort - tcpPort)
	s.udpTokens = make(map[uint64]*Player)
	s.mu.Unlock()
	s.log(fmt.Sprintf("UDP pose transport on port %d", udpPort))
	go s.udpLoop(conn)
}

// UDPAddr is the server's UDP address, nil when UDP is off.
func (s *Server) UDPAddr() *net.UDPAddr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.udpConn == nil {
		return nil
	}
	return s.udpConn.LocalAddr().(*net.UDPAddr)
}

// udpRegisterLocked gives a CapUDP player its token (caller holds s.mu).
// ok is false when UDP is off.
func (s *Server) udpRegisterLocked(p *Player) (offset uint16, token uint64, ok bool) {
	if s.udpConn == nil {
		return 0, 0, false
	}
	token = newUDPToken()
	for s.udpTokens[token] != nil {
		token = newUDPToken()
	}
	p.udp = &udpPlayer{token: token}
	s.udpTokens[token] = p
	return s.udpOffset, token, true
}

// udpUnregisterLocked drops p's token (caller holds s.mu).
func (s *Server) udpUnregisterLocked(p *Player) {
	if p.udp != nil && s.udpTokens[p.udp.token] == p {
		delete(s.udpTokens, p.udp.token)
	}
}

func (s *Server) udpCount(p *Player, up bool, typ byte, n int) {
	if s.Wire != nil {
		s.Wire.add(WireKey{Name: p.Name, Role: RolePlayer, Up: up, Type: typ}, int64(n))
	}
}

func (s *Server) udpLoop(conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// ICMP errors and the like: never fatal.
			time.Sleep(10 * time.Millisecond)
			continue
		}
		kind, rest, ok := parseUDPHeader(buf[:n])
		if !ok || len(rest) < 8 {
			continue
		}
		token := binary.BigEndian.Uint64(rest[:8])
		s.mu.RLock()
		p := s.udpTokens[token]
		s.mu.RUnlock()
		if p == nil {
			continue
		}
		u := p.udp
		now := time.Now()
		switch kind {
		case udpKindPing:
			if len(rest) < 16 {
				continue
			}
			u.addr.Store(from)
			u.heard.Store(now.UnixNano())
			s.udpCount(p, true, wireUDPPing, n)
			pong := append(udpHeader(udpKindPong), rest[8:16]...)
			if _, err := conn.WriteToUDP(pong, from); err == nil {
				s.udpCount(p, false, wireUDPPing, len(pong))
			}
		case udpKindFrame:
			if len(rest) < 12 {
				continue
			}
			u.addr.Store(from)
			u.heard.Store(now.UnixNano())
			seq := binary.BigEndian.Uint32(rest[8:12])
			if u.haveSeq && !seqNewer(seq, u.lastSeq) {
				continue // stale or a duplicate: newest wins
			}
			f, err := parseFrameBody(rest[12:])
			if err != nil || f.pos == nil {
				if !u.warned {
					u.warned = true
					s.log(fmt.Sprintf("dropping bad UDP frames from %s: %v", p.Name, err))
				}
				continue
			}
			u.lastSeq, u.haveSeq = seq, true
			if f.pose != nil {
				s.udpCount(p, true, wireUDPPose, n)
			} else {
				s.udpCount(p, true, wireUDPPos, n)
			}
			s.relayFrame(conn, p, seq, f)
		}
	}
}

// relayFrame does for one UDP frame what the TCP MsgPosition and MsgPose
// cases do together: the sender's position and place, its posed flag, and
// the relays (the position to everyone, the pose where relayPose would send
// it), over UDP to receivers whose UDP is live and over TCP to the rest.
func (s *Server) relayFrame(conn *net.UDPConn, sender *Player, seq uint32, f udpFrame) {
	if f.badPlace {
		s.countScope(func(st *ScopeStats) { st.PlacesRejected++ })
	}
	s.mu.Lock()
	sender.Position = f.pos
	if f.hasPlace {
		sender.where = placeState{place: f.place, at: s.now()}
	}
	s.mu.Unlock()
	if f.pose != nil && !sender.posed.Swap(true) {
		s.lobby.playersChanged()
	}

	id := sender.ID
	posRelay := PositionMessage(id, f.pos)
	if f.hasPlace {
		posRelay = AppendPlace(posRelay, f.place)
	}
	var poseRelay []byte
	head := binary.BigEndian.AppendUint32(udpHeader(udpKindFrame), seq)
	head = append(head, id)
	withPose := appendFrameBody(append([]byte(nil), head...), f.pos, f.place, f.hasPlace, f.pose)
	noPose := appendFrameBody(append([]byte(nil), head...), f.pos, f.place, f.hasPlace, nil)
	if f.pose != nil {
		poseRelay = PoseRelayFrame(id, f.pose)
	}
	poseFrame := int64(len(poseRelay) + 3)

	now, wall := s.now(), time.Now()
	var failed []*Player
	var sent, scoped int
	s.mu.RLock()
	for _, p := range s.players {
		if p.ID == id || !sender.relaysTo(p) {
			continue
		}
		givePose := false
		if f.pose != nil {
			givePose = s.poseWantedLocked(sender, p, now)
			if givePose {
				sent++
			} else {
				scoped++
			}
		}
		if p.udp.live(wall) && len(withPose) <= MaxUDPDatagram {
			d := noPose
			if givePose {
				d = withPose
			}
			if _, err := conn.WriteToUDP(d, p.udp.addr.Load()); err == nil {
				if givePose {
					s.udpCount(p, false, wireUDPPose, len(d))
				} else {
					s.udpCount(p, false, wireUDPPos, len(d))
				}
				continue
			}
		}
		if err := s.writePlayer(p, MsgPosition, posRelay); err != nil {
			failed = append(failed, p)
			continue
		}
		if givePose {
			if err := s.writePlayer(p, MsgPose, poseRelay); err != nil {
				failed = append(failed, p)
			}
		}
	}
	s.mu.RUnlock()
	s.retirePlayers(failed)
	if s.scope && f.pose != nil {
		s.countScope(func(st *ScopeStats) {
			st.PosesSent += sent
			st.PosesScoped += scoped
			st.PoseBytesSent += int64(sent) * poseFrame
			st.PoseBytesScoped += int64(scoped) * poseFrame
		})
	}
}

// ---- client ----

// udpLink is a client connection's UDP side.
type udpLink struct {
	conn     *net.UDPConn
	token    uint64
	seq      atomic.Uint32
	lastPong atomic.Int64 // UnixNano
	pinged   atomic.Int64 // UnixNano of the last PING sent
}

// up reports whether a PONG came within udpLiveFor.
func (l *udpLink) up(now time.Time) bool {
	return l != nil && now.UnixNano()-l.lastPong.Load() <= int64(udpLiveFor)
}

// EnableUDP turns the UDP pose transport on or off for this player client
// (default: WW_UDP); it takes effect at the next connect.
func (c *Client) EnableUDP(on bool) {
	if c.role != RolePlayer {
		return
	}
	if on {
		c.caps |= CapUDP
	} else {
		c.caps &^= CapUDP
	}
}

// UDPUp reports whether this client's frames go over UDP now.
func (c *Client) UDPUp() bool {
	c.mu.RLock()
	l := c.udp
	c.mu.RUnlock()
	return l.up(time.Now())
}

// startUDP opens the UDP side for a welcome with the UDP tail: the dialed
// host at the dialed port + the offset. A failure leaves the client on TCP.
func (c *Client) startUDP(conn net.Conn, welcome []byte) *udpLink {
	if c.role != RolePlayer || c.caps&CapUDP == 0 {
		return nil
	}
	offset, token, ok := ParseWelcomeUDP(welcome)
	if !ok {
		return nil
	}
	raddr, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil
	}
	uaddr := &net.UDPAddr{IP: raddr.IP, Port: int(uint16(raddr.Port) + offset), Zone: raddr.Zone}
	uc, err := net.DialUDP("udp", nil, uaddr)
	if err != nil {
		c.log(fmt.Sprintf("UDP pose transport unavailable, staying on TCP: %v", err))
		return nil
	}
	return &udpLink{conn: uc, token: token}
}

// start runs l's PING and read loops until stop is closed.
func (l *udpLink) start(c *Client, stop chan struct{}) {
	go c.udpPingLoop(l, stop)
	go c.udpReadLoop(l)
}

func (c *Client) udpPingLoop(l *udpLink, stop chan struct{}) {
	t := time.NewTicker(udpPingFast)
	defer t.Stop()
	defer l.conn.Close()
	wasUp := false
	ping := func(now time.Time) {
		b := binary.BigEndian.AppendUint64(udpHeader(udpKindPing), l.token)
		b = binary.BigEndian.AppendUint64(b, uint64(now.UnixNano()))
		l.conn.Write(b) // an error (ICMP refused) just leaves UDP down
		l.pinged.Store(now.UnixNano())
	}
	ping(time.Now())
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		now := time.Now()
		up := l.up(now)
		if up != wasUp {
			if up {
				c.log("UDP pose transport up")
			} else {
				c.log("UDP pose transport down, using TCP")
			}
			wasUp = up
		}
		if !up || now.UnixNano()-l.pinged.Load() >= int64(udpPingSlow) {
			ping(now)
		}
	}
}

func (c *Client) udpReadLoop(l *udpLink) {
	buf := make([]byte, 2048)
	for {
		n, err := l.conn.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(10 * time.Millisecond) // ICMP refused and the like
			continue
		}
		kind, rest, ok := parseUDPHeader(buf[:n])
		if !ok {
			continue
		}
		switch kind {
		case udpKindPong:
			l.lastPong.Store(time.Now().UnixNano())
		case udpKindFrame:
			if c.role != RolePlayer || len(rest) < 5 {
				continue
			}
			seq := binary.BigEndian.Uint32(rest[:4])
			from := rest[4]
			f, err := parseFrameBody(rest[5:])
			if err != nil || f.pos == nil {
				continue
			}
			// Copy what outlives buf.
			if f.matrices != nil {
				f.matrices = append([]byte(nil), f.matrices...)
			}
			if len(f.face) > 0 {
				f.face = append([]byte(nil), f.face...)
			} else {
				f.face = nil
			}
			c.applyUDPFrame(from, seq, f)
		}
	}
}

// applyUDPFrame takes one relayed frame into its remote: newest wins, a
// late one within UDPReorderWindow goes into Recent in order, the rest are
// dropped.
func (c *Client) applyUDPFrame(from byte, seq uint32, f udpFrame) {
	now := time.Now()
	c.mu.Lock()
	rp, ok := c.remotes[from]
	if !ok {
		rp = &RemotePlayer{ID: from}
		c.remotes[from] = rp
	}
	newest := rp.addUDPFrame(seq, &f, now)
	c.mu.Unlock()
	if newest && f.matrices != nil && c.OnPose != nil {
		c.OnPose(from, f.matrices)
	}
}

// addUDPFrame applies a frame to rp; it reports whether it was the newest.
func (rp *RemotePlayer) addUDPFrame(seq uint32, f *udpFrame, now time.Time) bool {
	rebase := !rp.udpOn
	if !rebase {
		d := int32(seq - rp.udpLast)
		switch {
		case d > udpSeqRestart || d < -udpSeqRestart:
			rebase = true
		case d == 0:
			return false
		case d < 0:
			if -d <= UDPReorderWindow {
				rp.insertLate(uint64(int64(seq)+rp.udpOff), f, now)
			}
			return false
		}
	}
	if rebase {
		rp.udpOff = int64(rp.seq+1) - int64(seq)
		rp.udpOn = true
	}
	rp.udpLast = seq
	s := uint64(int64(seq) + rp.udpOff)
	rp.seq = s
	pos := *f.pos
	rp.Position = &pos
	rp.Place, rp.HasPlace = f.place, f.hasPlace
	if f.hasPlace {
		rp.PlaceAt = now
	}
	if f.matrices != nil {
		rp.PoseJoints, rp.PoseMatrices, rp.FaceState = f.joints, f.matrices, f.face
		rp.LastPose = now
	}
	keep := rp.Recent
	if len(keep) >= RecentCap {
		keep = keep[len(keep)-RecentCap+1:]
	}
	next := make([]Sample, len(keep), len(keep)+1)
	copy(next, keep)
	rp.Recent = append(next, frameSample(s, f, now))
	return true
}

func frameSample(seq uint64, f *udpFrame, at time.Time) Sample {
	smp := Sample{Seq: seq, At: at, Pos: *f.pos}
	if f.matrices != nil {
		smp.PoseJoints, smp.PoseMatrices, smp.FaceState = f.joints, f.matrices, f.face
	}
	return smp
}

// insertLate puts a late frame into Recent at its place by Seq, unless it
// is older than the whole buffer or already there.
func (rp *RemotePlayer) insertLate(seq uint64, f *udpFrame, now time.Time) {
	r := rp.Recent
	if len(r) == 0 || seq < r[0].Seq {
		return
	}
	i := len(r)
	for i > 0 && r[i-1].Seq > seq {
		i--
	}
	if i > 0 && r[i-1].Seq == seq {
		return
	}
	next := make([]Sample, 0, len(r)+1)
	next = append(next, r[:i]...)
	next = append(next, frameSample(seq, f, now))
	next = append(next, r[i:]...)
	if len(next) > RecentCap {
		next = next[len(next)-RecentCap:]
	}
	rp.Recent = next
}

// SendFrame sends one frame, the position (with its place when hasPlace)
// and the pose when matrices is not nil: as one UDP datagram while UDP is
// up and it fits, else over TCP as a MsgPosition then a MsgPose.
func (c *Client) SendFrame(pos *PlayerPosition, place Place, hasPlace bool, joints int, matrices, face []byte) error {
	var pose []byte
	if matrices != nil {
		if pose = SerializePose(joints, matrices, face); pose == nil {
			return fmt.Errorf("invalid pose: joints=%d len=%d", joints, len(matrices))
		}
	}
	c.mu.RLock()
	l := c.udp
	c.mu.RUnlock()
	if c.connected.Load() && l.up(time.Now()) {
		b := binary.BigEndian.AppendUint64(udpHeader(udpKindFrame), l.token)
		b = binary.BigEndian.AppendUint32(b, l.seq.Load()+1)
		b = appendFrameBody(b, pos, place, hasPlace, pose)
		if len(b) <= MaxUDPDatagram {
			if _, err := l.conn.Write(b); err == nil {
				l.seq.Add(1)
				return nil
			}
		}
	}
	var err error
	if hasPlace {
		err = c.SendPositionAt(pos, place)
	} else {
		err = c.SendPosition(pos)
	}
	if err != nil {
		return fmt.Errorf("send position: %w", err)
	}
	if pose != nil {
		if err := c.send(MsgPose, pose); err != nil {
			return fmt.Errorf("send pose: %w", err)
		}
	}
	return nil
}
