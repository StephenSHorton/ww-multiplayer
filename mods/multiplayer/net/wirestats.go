package network

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
)

// Wire statistics (wind#84): what each connection carries, by message
// type and, for MsgEnemy and MsgCoop, by sub-message. A Server with a
// non-nil Wire wraps every accepted connection in a counting conn that
// parses the frames ([type:1][length:2][data]) as the bytes pass, both
// ways, so the counts are exact frame sizes (header included) whatever
// the reads and writes look like. The label (player name and role) is set
// once the hello has been read; the join and hello themselves count under
// the empty name. Off (Wire nil) nothing is wrapped.
//
// "Up" is client to server, "down" server to client: for a joiner on the
// internet, its upload and its download.

// WireStats accumulates frame counts. The zero value is not usable; use
// NewWireStats. Safe for concurrent use.
type WireStats struct {
	mu     sync.Mutex
	counts map[WireKey]WireCount
}

// WireKey names one stream: a connection's player name and role, the
// direction, the message type and (MsgEnemy, MsgCoop) the sub byte.
type WireKey struct {
	Name string
	Role byte
	Up   bool
	Type byte
	Sub  byte
}

// WireCount is a stream's frames and bytes (headers included).
type WireCount struct {
	Frames int64
	Bytes  int64
}

// NewWireStats returns an empty WireStats.
func NewWireStats() *WireStats { return &WireStats{counts: make(map[WireKey]WireCount)} }

func (w *WireStats) add(k WireKey, n int64) {
	w.mu.Lock()
	c := w.counts[k]
	c.Frames++
	c.Bytes += n
	w.counts[k] = c
	w.mu.Unlock()
}

// Snapshot returns a copy of the counts so far.
func (w *WireStats) Snapshot() map[WireKey]WireCount {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[WireKey]WireCount, len(w.counts))
	for k, v := range w.counts {
		out[k] = v
	}
	return out
}

// wireHasSub reports whether a message type's first payload byte is a sub
// type worth splitting the count by.
func wireHasSub(t byte) bool { return t == MsgEnemy || t == MsgCoop }

// StreamName is a short, stable name for a stream: "pose", "pos",
// "enemy/STATE", "coop/0x04" and so on.
func (k WireKey) StreamName() string {
	switch k.Type {
	case MsgPose:
		return "pose"
	case MsgPosition:
		return "pos"
	case MsgPing:
		return "ping"
	case MsgPong:
		return "pong"
	case MsgPlayerList:
		return "roster"
	case MsgChat:
		return "chat"
	case MsgFx:
		return "fx"
	case MsgEnemy:
		names := map[byte]string{EnemySubState: "STATE", EnemySubHit: "HIT", EnemySubAck: "ACK",
			EnemySubPHit: "PHIT", EnemySubPAck: "PACK", EnemySubPState: "PSTATE"}
		if n, ok := names[k.Sub]; ok {
			return "enemy/" + n
		}
		return fmt.Sprintf("enemy/0x%02X", k.Sub)
	case MsgCoop:
		return fmt.Sprintf("coop/0x%02X", k.Sub)
	}
	if k.Type >= 0x20 && k.Type < 0x7F {
		return string(rune(k.Type))
	}
	return fmt.Sprintf("0x%02X", k.Type)
}

// FormatWireRates renders the change between two snapshots, over secs
// seconds, for one player name: total up and down in kbit/s, then each
// stream's share, biggest first. Streams under 0.05 kbit/s are left out.
func FormatWireRates(prev, cur map[WireKey]WireCount, name string, secs float64) string {
	type row struct {
		key   string
		bytes int64
		up    bool
	}
	var rows []row
	var up, down int64
	for k, c := range cur {
		if k.Name != name {
			continue
		}
		d := c.Bytes - prev[k].Bytes
		if d <= 0 {
			continue
		}
		if k.Up {
			up += d
		} else {
			down += d
		}
		rows = append(rows, row{k.StreamName(), d, k.Up})
	}
	// Merge rows that share a stream name and direction (two roles).
	merged := map[[2]string]int64{}
	for _, r := range rows {
		dir := "down"
		if r.up {
			dir = "up"
		}
		merged[[2]string{dir, r.key}] += r.bytes
	}
	rows = rows[:0]
	for k, b := range merged {
		rows = append(rows, row{k[1], b, k[0] == "up"})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].key < rows[j].key
	})
	kbps := func(b int64) float64 { return float64(b) * 8 / 1000 / secs }
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: up %.0f kbit/s, down %.0f kbit/s |", name, kbps(up), kbps(down))
	for _, r := range rows {
		if kbps(r.bytes) < 0.05 {
			continue
		}
		dir := "down"
		if r.up {
			dir = "up"
		}
		fmt.Fprintf(&sb, " %s %s %.1f", dir, r.key, kbps(r.bytes))
	}
	return sb.String()
}

// frameCounter follows one direction's byte stream frame by frame.
type frameCounter struct {
	hdr     [3]byte
	hdrN    int
	typ     byte
	total   int64 // this frame's size, header included
	left    int   // payload bytes still to come
	pending bool  // waiting for the first payload byte (the sub)
}

// feed takes the next bytes of the stream and reports each frame it
// completes the header (and sub) of.
func (f *frameCounter) feed(p []byte, done func(typ, sub byte, size int64)) {
	for len(p) > 0 {
		if f.left == 0 && !f.pending {
			// In a header.
			n := copy(f.hdr[f.hdrN:], p)
			f.hdrN += n
			p = p[n:]
			if f.hdrN < 3 {
				return
			}
			f.hdrN = 0
			f.typ = f.hdr[0]
			f.left = int(f.hdr[1])<<8 | int(f.hdr[2])
			f.total = int64(3 + f.left)
			if wireHasSub(f.typ) && f.left > 0 {
				f.pending = true
				continue
			}
			done(f.typ, 0, f.total)
			continue
		}
		if f.pending {
			f.pending = false
			done(f.typ, p[0], f.total)
		}
		n := f.left
		if n > len(p) {
			n = len(p)
		}
		f.left -= n
		p = p[n:]
	}
}

// wireConn counts the frames that pass a server connection.
type wireConn struct {
	net.Conn
	w *WireStats

	mu   sync.Mutex // guards name, role and both counters
	name string
	role byte
	rd   frameCounter
	wr   frameCounter
}

func (c *wireConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.count(&c.rd, true, p[:n])
	}
	return n, err
}

func (c *wireConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.count(&c.wr, false, p[:n])
	}
	return n, err
}

func (c *wireConn) count(f *frameCounter, up bool, p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f.feed(p, func(typ, sub byte, size int64) {
		c.w.add(WireKey{Name: c.name, Role: c.role, Up: up, Type: typ, Sub: sub}, size)
	})
}

// labelWire names a counted connection's streams once its hello is read.
// It does nothing for a connection that isn't counted.
func labelWire(conn net.Conn, name string, role byte) {
	if c, ok := conn.(*wireConn); ok {
		c.mu.Lock()
		c.name, c.role = name, role
		c.mu.Unlock()
	}
}

// CloseWrite half-closes the counted connection when the one under it can
// (a TCP connection), so a refusal's half-close (handshake.go) and the
// lobby's work as without counting. Otherwise it does nothing, which is
// what those callers do for such a connection.
func (c *wireConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
