package network

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
)

// LobbyClient is the app's side of a lobby connection (docs/10-lobby.md):
// a Client with RoleLobby that sends HELLO after the welcome, keeps the
// latest STATE, reconnects into its held slot with its token, and turns
// the server's sub-messages into callbacks. Its methods are safe for
// concurrent use; the callbacks run on the read loop and must not block.
type LobbyClient struct {
	c *Client

	mu        sync.Mutex
	hello     LobbyHelloBody // what HELLO sends; Token and Color follow the server
	welcome   LobbyWelcomeBody
	welcomed  bool
	state     LobbyStateBody
	haveState bool
	waiter    chan error // the Connect in progress waits on it for WELCOME
	stop      context.CancelCauseFunc

	// Callbacks, set before Connect or Run.
	OnWelcome func(LobbyWelcomeBody)
	OnState   func(LobbyStateBody)
	OnNotice  func(LobbyNoticeBody)
	OnStart   func(LobbyStartBody)
	OnGo      func(LobbyGoBody)
	OnRequest func(LobbyRequestBody)
	// OnLink, when set, follows Run's connection: up after each connect
	// that got a WELCOME; down with the error after each attempt that
	// failed; down with a nil error when a live connection dropped. It
	// is not called once Run's ctx has ended (a Leave, or a fatal
	// notice), and must not block.
	OnLink func(up bool, err error)
}

// LobbyNoticeError is a lobby refusal that reconnecting can't fix: the name
// is taken, the lobby is full, the host kicked us, or a newer connection
// took our slot. errors.Is(err, ErrFatal) holds for it.
type LobbyNoticeError struct {
	Notice LobbyNoticeBody
}

func (e *LobbyNoticeError) Error() string { return "lobby: " + e.Notice.Text }

// Is makes errors.Is(err, ErrFatal) match.
func (e *LobbyNoticeError) Is(target error) bool { return target == ErrFatal }

// fatalNotices end a lobby client's Run.
var fatalNotices = map[string]bool{NoticeNameTaken: true, NoticeFull: true, NoticeKicked: true, "replaced": true}

// IsLegacyHost reports whether err is a pre-lobby host refusing the lobby
// role (RejectRole): the app then joins directly, as before the lobby.
func IsLegacyHost(err error) bool {
	var rej *RejectError
	return errors.As(err, &rej) && rej.Code == RejectRole
}

// NewLobbyClient returns a lobby client joining as name. hello is what its
// HELLO says (app version, OS, color preference, Dolphin state); Token is
// managed by the client.
func NewLobbyClient(name string, hello LobbyHelloBody) *LobbyClient {
	c := NewClient(name)
	c.role = RoleLobby
	c.caps = LobbyCapJSON
	l := &LobbyClient{c: c, hello: hello}
	l.hello.Token = ""
	c.OnLobby = l.onLobby
	return l
}

// Client is the underlying connection: set OnLog and OnChat on it, read
// LastRTT and IsConnected from it.
func (l *LobbyClient) Client() *Client { return l.c }

// State returns the latest STATE; ok is false until one has arrived.
func (l *LobbyClient) State() (st LobbyStateBody, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state, l.haveState
}

// Welcome returns the latest WELCOME (our member ID, host flag); ok is
// false until one has arrived.
func (l *LobbyClient) Welcome() (w LobbyWelcomeBody, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.welcome, l.welcomed
}

func (l *LobbyClient) onLobby(payload []byte) {
	sub, body, err := ParseLobby(payload)
	if err != nil {
		l.c.log("Ignoring a lobby message: " + err.Error())
		return
	}
	switch sub {
	case LobbyWelcome:
		var w LobbyWelcomeBody
		if DecodeLobby(body, &w) != nil {
			return
		}
		l.mu.Lock()
		l.welcome, l.welcomed = w, true
		l.hello.Token = w.Token
		l.haveState = false // a new server's rev starts over
		l.signalLocked(nil)
		l.mu.Unlock()
		if l.OnWelcome != nil {
			l.OnWelcome(w)
		}
	case LobbyState:
		var st LobbyStateBody
		if DecodeLobby(body, &st) != nil {
			return
		}
		l.mu.Lock()
		if l.haveState && st.Rev <= l.state.Rev {
			l.mu.Unlock()
			return
		}
		l.state, l.haveState = st, true
		for _, m := range st.Members {
			if m.ID == l.welcome.Member && m.Via == ViaApp {
				l.hello.Color = m.Color // a reconnect to a new server asks for it again
			}
		}
		l.mu.Unlock()
		if l.OnState != nil {
			l.OnState(st)
		}
	case LobbyNotice:
		var b LobbyNoticeBody
		if DecodeLobby(body, &b) != nil {
			return
		}
		// The callback first: a fatal notice ends Connect and Run, and
		// what waits on those (the controller ending the session) must
		// come after the notice is delivered.
		if l.OnNotice != nil {
			l.OnNotice(b)
		}
		if fatalNotices[b.Code] {
			err := &LobbyNoticeError{Notice: b}
			l.mu.Lock()
			l.signalLocked(err)
			stop := l.stop
			l.mu.Unlock()
			if stop != nil {
				stop(err)
			}
		}
	case LobbyStart:
		var b LobbyStartBody
		if DecodeLobby(body, &b) == nil && l.OnStart != nil {
			l.OnStart(b)
		}
	case LobbyGo:
		var b LobbyGoBody
		if DecodeLobby(body, &b) == nil && l.OnGo != nil {
			l.OnGo(b)
		}
	case LobbyRequest:
		var b LobbyRequestBody
		if DecodeLobby(body, &b) == nil && l.OnRequest != nil {
			l.OnRequest(b)
		}
	}
}

// signalLocked ends the Connect waiting for WELCOME, if any. l.mu held.
func (l *LobbyClient) signalLocked(err error) {
	if l.waiter != nil {
		select {
		case l.waiter <- err:
		default:
		}
		l.waiter = nil
	}
}

// Connect opens the lobby connection and waits for WELCOME. A pre-lobby
// host fails it with a *RejectError with RejectRole (IsLegacyHost); a
// taken name or a full lobby with a *LobbyNoticeError. Both are fatal
// (errors.Is ErrFatal).
func (l *LobbyClient) Connect(ctx context.Context, addr string) error {
	wait := make(chan error, 1)
	l.mu.Lock()
	l.waiter = wait
	hello := l.hello
	l.mu.Unlock()
	if err := l.c.ConnectContext(ctx, addr); err != nil {
		l.clearWaiter(wait)
		return err
	}
	if err := l.send(LobbyHello, hello); err != nil {
		l.clearWaiter(wait)
		l.c.Disconnect()
		return fmt.Errorf("lobby hello: %w", err)
	}
	timeout := time.NewTimer(l.c.readTimeout)
	defer timeout.Stop()
	poll := time.NewTicker(coopDropPoll)
	defer poll.Stop()
	for {
		select {
		case err := <-wait:
			if err != nil {
				l.c.Disconnect()
			}
			return err
		case <-ctx.Done():
			l.clearWaiter(wait)
			l.c.Disconnect()
			return fmt.Errorf("connect: %w", ctx.Err())
		case <-timeout.C:
			l.clearWaiter(wait)
			l.c.Disconnect()
			return errors.New("the host did not answer the lobby hello in time")
		case <-poll.C:
			if !l.c.IsConnected() {
				// Dropped before WELCOME; a refusal notice may have
				// raced the close.
				select {
				case err := <-wait:
					if err != nil {
						return err
					}
					return nil
				default:
				}
				l.clearWaiter(wait)
				return errors.New("the host closed the lobby connection before welcoming us")
			}
		}
	}
}

func (l *LobbyClient) clearWaiter(w chan error) {
	l.mu.Lock()
	if l.waiter == w {
		l.waiter = nil
	}
	l.mu.Unlock()
}

// Disconnect closes the lobby connection.
func (l *LobbyClient) Disconnect() { l.c.Disconnect() }

// Run keeps the lobby connection up until ctx ends, reconnecting with the
// player connection's backoff and reclaiming the held slot with the token.
// It returns nil when ctx ends, else the fatal error that stopped it: a
// pre-lobby host (IsLegacyHost), a version mismatch, or a
// *LobbyNoticeError (name taken, full, kicked, replaced).
func (l *LobbyClient) Run(ctx context.Context, addr string, connLog *ConnectLog) error {
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	l.mu.Lock()
	l.stop = stop
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.stop = nil
		l.mu.Unlock()
	}()
	connect := func(ctx context.Context) error {
		err := l.Connect(ctx, addr)
		// Only retryable outcomes count as link changes: a fatal answer
		// (version mismatch, pre-lobby host, a notice) ends Run instead.
		if l.OnLink != nil && ctx.Err() == nil && !errors.Is(err, ErrFatal) {
			l.OnLink(err == nil, err)
		}
		if connLog != nil {
			return connLog.Observe(ctx, err)
		}
		return err
	}
	live := func(ctx context.Context) error {
		t := time.NewTicker(coopDropPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				if !l.c.IsConnected() {
					if l.OnLink != nil && ctx.Err() == nil {
						l.OnLink(false, nil)
					}
					return nil
				}
			}
		}
	}
	run := func(ctx context.Context) error { return RunSession(ctx, l.c.Disconnect, live) }
	err := ReconnectLoop(ctx, connect, run, NewBackoff(), SleepCtx)
	var ne *LobbyNoticeError
	if cause := context.Cause(ctx); errors.As(cause, &ne) {
		return cause
	}
	return err
}

func (l *LobbyClient) send(sub byte, body any) error {
	data, err := EncodeLobby(sub, body)
	if err != nil {
		return err
	}
	return l.c.send(MsgLobby, data)
}

// SetColor asks for a tunic color. The answer is a STATE with it: every
// request is granted, whoever else wears the color.
func (l *LobbyClient) SetColor(c playercolor.Color) error {
	h := c.Hex()
	return l.send(LobbySet, LobbySetBody{Color: &h})
}

// SetReady sets the ready flag.
func (l *LobbyClient) SetReady(ready bool) error {
	return l.send(LobbySet, LobbySetBody{Ready: &ready})
}

// SetDolphin reports this player's Dolphin state (DolphinNone, ...).
func (l *LobbyClient) SetDolphin(state string) error {
	l.mu.Lock()
	l.hello.Dolphin = state
	l.mu.Unlock()
	return l.send(LobbySet, LobbySetBody{Dolphin: &state})
}

// SetFile reports this player's save-file hint.
func (l *LobbyClient) SetFile(hint string) error {
	return l.send(LobbySet, LobbySetBody{File: &hint})
}

// SetSettings sets the co-op settings (host only).
func (l *LobbyClient) SetSettings(s LobbySettingsBody) error { return l.send(LobbySettings, s) }

// Start starts the session (host only): LOBBY -> STARTING.
func (l *LobbyClient) Start() error {
	l.mu.Lock()
	s := l.state.Settings
	l.mu.Unlock()
	return l.send(LobbyStart, LobbyStartBody{Settings: s})
}

// Go says the host's side is ready (host only): STARTING -> IN_GAME. world
// is the co-op world it serves, or "".
func (l *LobbyClient) Go(world string) error { return l.send(LobbyGo, LobbyGoBody{World: world}) }

// Kick removes a member, or a CLI player by its row's ID (host only). The
// name stays refused (lobby, player and co-op connections) until the lobby
// empties.
func (l *LobbyClient) Kick(member int) error { return l.send(LobbyKick, LobbyKickBody{Member: member}) }

// RequestMerge asks the host to allow merging.
func (l *LobbyClient) RequestMerge() error {
	return l.send(LobbyRequest, LobbyRequestBody{Kind: "merge"})
}

// SendChat sends one chat line to everyone, in the lobby and in game.
func (l *LobbyClient) SendChat(text string) error { return l.c.SendChat(text) }
