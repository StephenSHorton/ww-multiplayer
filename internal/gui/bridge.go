package gui

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// MaxNameLen is the longest player name the UI accepts (design §2.1),
// shorter than the wire's network.MaxNameLen.
const MaxNameLen = 20

// HostRequest is the Host form.
type HostRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"` // a palette name or "#RRGGBB"; "" takes the first free one
	Port  int    `json:"port"`  // 0 is the default port
}

// JoinRequest is the Join form.
type JoinRequest struct {
	Addr  string `json:"addr"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Bridge is the Go side of the frontend: every exported method is callable
// from JS, by the Wails binding in the window and by ServeBrowser's /api/call
// in a browser, with JSON arguments and results. Nothing else is exported,
// because Wails binds every exported method of a bound struct.
//
// Bridge only turns the forms into controller commands and the controller's
// state and events into views; the session logic stays in the controller.
type Bridge struct {
	ctrl    *session.Controller
	version string
	cfgPath string
	getenv  func(string) string
	now     func() time.Time

	// ctx is the app's lifetime: sessions run under it, and it ends at
	// shutdown.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex // guards cfg, cfgWarns
	cfg      config.Config
	cfgWarns []string
}

// NewBridge returns the Bridge for o. Call close when the app ends.
func NewBridge(o Options) *Bridge {
	b := &Bridge{ctrl: o.Controller, version: o.Version, cfgPath: o.ConfigPath, getenv: o.Getenv, now: time.Now}
	if b.getenv == nil {
		b.getenv = func(string) string { return "" }
	}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	if b.cfgPath == "" {
		if p, err := config.Path(); err == nil {
			b.cfgPath = p
		}
	}
	if b.cfgPath != "" {
		b.cfg, b.cfgWarns = config.Load(b.cfgPath)
	} else {
		b.cfg = config.Defaults()
	}
	return b
}

// Info returns the build, the palette and the forms' starting values.
func (b *Bridge) Info() InfoView {
	b.mu.Lock()
	cfg, warns := b.cfg, append([]string(nil), b.cfgWarns...)
	b.mu.Unlock()
	eff, _ := cfg.WithEnv(b.getenv)
	v := InfoView{
		Version: b.version, OS: runtime.GOOS, Palette: paletteView(),
		Name: eff.Name, Port: eff.Port, MaxName: MaxNameLen, Warnings: warns,
		RecentHosts: []RecentHostView{},
	}
	if c, err := playercolor.Parse(eff.Color); err == nil && eff.Color != "" {
		v.Color = c.Hex()
	}
	for _, h := range eff.RecentHosts {
		v.RecentHosts = append(v.RecentHosts, RecentHostView{Addr: h.Addr, Label: h.Label})
	}
	return v
}

// State returns the current (or last) session's state.
func (b *Bridge) State() StateView {
	st := b.ctrl.Snapshot()
	running := st.Session != 0 && st.Phase != session.Ended && st.Phase != session.Idle
	return stateView(st, running)
}

// Host starts a session that hosts a lobby on this machine.
func (b *Bridge) Host(r HostRequest) error {
	name, err := checkName(r.Name)
	if err != nil {
		return err
	}
	col, err := parseColor(r.Color)
	if err != nil {
		return err
	}
	if r.Port < 0 || r.Port > 65535 {
		return fmt.Errorf("port %d is out of range (1-65535)", r.Port)
	}
	if _, err := b.ctrl.HostLobby(b.ctx, session.HostOptions{Name: name, Color: col, Port: r.Port}); err != nil {
		return err
	}
	b.remember(name, col, "", r.Port)
	return nil
}

// Join starts a session in the lobby of the host at r.Addr (joined
// directly when the host has no lobby).
func (b *Bridge) Join(r JoinRequest) error {
	addr := strings.TrimSpace(r.Addr)
	if addr == "" {
		return errors.New("type the host's address (the one its window shows)")
	}
	name, err := checkName(r.Name)
	if err != nil {
		return err
	}
	col, err := parseColor(r.Color)
	if err != nil {
		return err
	}
	if _, err := b.ctrl.JoinLobby(b.ctx, session.JoinOptions{Addr: addr, Name: name, Color: col}); err != nil {
		return err
	}
	b.remember(name, col, addr, 0)
	return nil
}

// Leave ends the running session and returns once it has torn down.
func (b *Bridge) Leave() { b.ctrl.Leave() }

// SetColor asks the lobby for a tunic color.
func (b *Bridge) SetColor(color string) error {
	c, err := playercolor.Parse(color)
	if err != nil {
		return err
	}
	if err := b.ctrl.SetColor(c); err != nil {
		return err
	}
	b.mu.Lock()
	b.cfg.Color = c.Hex()
	b.mu.Unlock()
	b.save()
	return nil
}

// SetReady sets this player's ready flag in the lobby.
func (b *Bridge) SetReady(ready bool) error { return b.ctrl.SetReady(ready) }

// SendChat sends a chat line to the session.
func (b *Bridge) SendChat(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return b.ctrl.SendChat(text)
}

// checkName trims and validates a player name with the wire's rules and
// the UI's shorter limit.
func checkName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("type a name for yourself")
	}
	if n := utf8.RuneCountInString(s); n > MaxNameLen {
		return "", fmt.Errorf("names are at most %d characters (that one has %d)", MaxNameLen, n)
	}
	if err := network.ValidatePlayerName(s); err != nil {
		return "", err
	}
	return s, nil
}

// parseColor reads a form's color: "" is automatic (nil).
func parseColor(s string) (*playercolor.Color, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	c, err := playercolor.Parse(s)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// remember saves the form's choices for next time.
func (b *Bridge) remember(name string, col *playercolor.Color, addr string, port int) {
	b.mu.Lock()
	b.cfg.Name = name
	if col != nil {
		b.cfg.Color = col.Hex()
	}
	if addr != "" {
		b.cfg.AddRecentHost(addr, "", b.now())
	}
	if port != 0 {
		b.cfg.Port = port
	}
	b.mu.Unlock()
	b.save()
}

// save writes the config, best effort: a settings file that can't be
// written must not fail a session.
func (b *Bridge) save() {
	if b.cfgPath == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.cfg.Save(b.cfgPath)
}

// pump subscribes to the controller's events now and forwards them to
// emit from a goroutine until ctx ends; the returned channel closes when
// it has stopped. Subscribing before returning means no event published
// after pump returns is missed.
func (b *Bridge) pump(ctx context.Context, emit func(EventView)) <-chan struct{} {
	ch, stop := b.ctrl.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				emit(eventView(ev))
			}
		}
	}()
	return done
}

// running says whether a session is running (window close asks first).
func (b *Bridge) running() bool { return b.State().Running }

// close ends the running session (the same teardown as Leave) and the
// Bridge's context.
func (b *Bridge) close() {
	b.ctrl.Leave()
	b.cancel()
}
