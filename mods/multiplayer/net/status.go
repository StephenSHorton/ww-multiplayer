package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// The status probe (docs/11-self-hosting.md "Health check"). A dedicated
// server's health check and a player asking "is that server up, and is it
// my version?" need an answer without joining: no name, no roster row, no
// lobby member. So a connection whose first frame is MsgStatus (instead of
// MsgJoin) gets one MsgStatus back, a JSON StatusBody, and the server
// closes it. Nothing is logged, so a health check every few seconds stays
// out of the server's log.
//
// It rides the game port on purpose. A second port (an HTTP /health, as
// datstarkey/ww-online's server has: WWOnline.Server/ServerApp.cs) would
// be one more port to open, forward and firewall, and it would answer even
// when the game port is the one that's broken; this probe fails exactly
// when players can't connect. A server from before the probe closes a
// connection whose first frame isn't MsgJoin without a word
// (handlePlayer), so the prober tells "an older server" (EOF, no frame)
// from "nothing there" (the dial fails). No protocol version bump: a
// player connection never sends MsgStatus.

// StatusApp is StatusBody.App: what a ww-multiplayer server answers.
const StatusApp = "ww-multiplayer"

// maxStatusBody caps a status answer the prober reads.
const maxStatusBody = 4096

// StatusBody is a status answer. Counts only: no names, no addresses.
type StatusBody struct {
	App      string `json:"app"`
	Version  string `json:"version,omitempty"` // the server's app version (Server.AppVersion)
	Protocol uint16 `json:"protocol"`          // its ProtocolVersion: a player must match it
	// Players counts the names among the player connections (a player
	// runs two under one name); Lobby the lobby members with a live
	// connection; Phase is the lobby's phase.
	Players int    `json:"players"`
	Lobby   int    `json:"lobby"`
	Phase   string `json:"phase"`
	// OwnerKey says the lobby has an owner key (its host role goes with
	// it, not with this machine).
	OwnerKey bool `json:"ownerKey,omitempty"`
	// UptimeSecs is how long the server has been listening.
	UptimeSecs int64 `json:"uptimeSecs"`
}

// status is the server's StatusBody now.
func (s *Server) status() StatusBody {
	b := StatusBody{App: StatusApp, Version: s.AppVersion, Protocol: ProtocolVersion}
	s.mu.RLock()
	names := map[string]bool{}
	for _, p := range s.players {
		names[lobbyKey(p.Name)] = true
	}
	b.Players = len(names)
	if !s.started.IsZero() {
		b.UptimeSecs = int64(time.Since(s.started) / time.Second)
	}
	s.mu.RUnlock()
	h := s.lobby
	h.mu.Lock()
	for _, mb := range h.m.members {
		if mb.conn != 0 {
			b.Lobby++
		}
	}
	b.Phase = h.m.phase
	b.OwnerKey = h.ownerKey != nil
	h.mu.Unlock()
	return b
}

// answerStatus writes the status answer and closes conn (handlePlayer's
// deferred Close; lingerClose lets the frame reach the prober first).
func (s *Server) answerStatus(conn net.Conn) {
	data, err := json.Marshal(s.status())
	if err != nil {
		return
	}
	s.writeConn(conn, MsgStatus, data)
	lingerClose(conn)
}

// ErrStatusLegacy is ProbeStatus's answer from a server that closed the
// connection without a status: a ww-multiplayer from before the probe, or
// something else listening on that port.
var ErrStatusLegacy = errors.New("the server closed the connection without a status: a ww-multiplayer from before the status probe, or another program on that port")

// ErrStatusNotAnswer wraps ProbeStatus's answer from something that
// replied, but not with a status frame: another program on that port (an
// HTTP or game server's reply read as a frame header ends mid-frame).
var ErrStatusNotAnswer = errors.New("the reply is not a ww-multiplayer status answer")

// ProbeStatus asks the server at addr (host:port) for its status, within
// timeout (0: 5 s) or ctx. A dial error comes back as it is (nothing
// listening, a firewall that drops, a bad name); ErrStatusLegacy is a
// server that closed the connection without sending a byte; any reply
// that isn't a status frame (an HTTP server's, a frame cut off) is
// another error (docs/11-self-hosting.md §5: exit 1, not 3).
func ProbeStatus(ctx context.Context, addr string, timeout time.Duration) (StatusBody, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return StatusBody{}, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if err := WriteMessage(conn, MsgStatus, nil); err != nil {
		return StatusBody{}, err
	}
	msg, err := ReadMessage(conn)
	switch {
	case err == io.EOF:
		// ReadMessage returns the header's io.ReadFull error unwrapped,
		// and that is io.EOF only when not one byte came: a closed
		// connection. A partial header (io.ErrUnexpectedEOF) or a short
		// body ("read data: ...") means something replied.
		return StatusBody{}, ErrStatusLegacy
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return StatusBody{}, fmt.Errorf("%w (it ended mid-frame): another program on that port", ErrStatusNotAnswer)
	case err != nil:
		return StatusBody{}, err
	case msg.Type != MsgStatus:
		return StatusBody{}, fmt.Errorf("%w: the server answered the probe with a %q frame", ErrStatusNotAnswer, msg.Type)
	case len(msg.Data) > maxStatusBody:
		return StatusBody{}, fmt.Errorf("status answer of %d bytes (max %d)", len(msg.Data), maxStatusBody)
	}
	var b StatusBody
	if err := json.Unmarshal(msg.Data, &b); err != nil {
		return StatusBody{}, fmt.Errorf("status answer: %w", err)
	}
	if b.App != StatusApp {
		return b, fmt.Errorf("the status answer is from %q, not %s", b.App, StatusApp)
	}
	return b, nil
}
