package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// MaxNameLen is the longest player name the UI accepts (design §2.1),
// shorter than the wire's network.MaxNameLen.
const MaxNameLen = 20

// HostRequest is the Host form.
type HostRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"` // a palette name or "#RRGGBB"; "" wears classic green
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

	// The setup's seams (setup.go): package main's co-op tables step, and
	// what a test replaces.
	coopTables func(io.Writer, string)
	patchRun   func(ctx context.Context, in, out string, w io.Writer, o patch.Options) (patch.Result, error)
	inspect    func(string) (patch.DiscState, error)
	denv       func() launch.Env
	procs      func() []launch.Process // the running Dolphins (Start's check)
	home       func() string
	macStatus  func() (launch.MacState, launch.MacFacts, error)
	prepareMac func(log.Reporter) error

	dlgMu sync.Mutex
	dlg   fileDialogs // the window's file dialogs; nil in a browser

	jobMu  sync.Mutex // guards job, jobSeq
	job    *patchJob
	jobSeq int

	preparing sync.Mutex // held while Prepare Dolphin runs

	// quitting is set once the window was closed during a patch: it stays
	// open until the cancelled patch ends (beforeClose).
	quitting atomic.Bool

	emu      sync.Mutex // guards emitters
	emitters map[int]func(EventView)
	emitSeq  int

	// The Lobby screen's seams (lobby.go): this machine's addresses, and
	// the window's clipboard and browser (nil in a browser, where the
	// page uses its own).
	hostIPs   func() []string
	clipMu    sync.Mutex // guards clipboard, openURL
	clipboard func(string) error
	openURL   func(string) error

	// Co-op (coop.go, cards.go): the card preview's cache and reader, a
	// joiner's own dry run (guarded by mu), and the file manager.
	cardc    cardCache
	findSave func(userDir string) (cardSave, error)
	myDryRun bool
	openPath func(dir string) error

	sessMu   sync.Mutex   // guards hostPort, lastJoin, joining
	hostPort int          // the port the last Host listens on
	lastJoin *JoinRequest // the last Join, for Retry
	// joining is the running join's address until it reaches the host,
	// when it becomes a recent host: a typo or an unreachable address
	// never does.
	joining *pendingHost

	// LAN discovery (lan.go): the hosted lobby's beacon and Home's
	// "Sessions on your network".
	lan lanState
}

// pendingHost is a join's address, waiting to reach its host.
type pendingHost struct {
	session session.ID
	addr    string
}

// NewBridge returns the Bridge for o. Call close when the app ends.
func NewBridge(o Options) *Bridge {
	b := &Bridge{ctrl: o.Controller, version: o.Version, cfgPath: o.ConfigPath, getenv: o.Getenv, now: time.Now,
		coopTables: o.CoopTables, patchRun: patch.Run, inspect: patch.Inspect, denv: o.DolphinEnv,
		home:      func() string { h, _ := os.UserHomeDir(); return h },
		macStatus: macStatusHere, prepareMac: launch.PrepareMacHere,
		emitters: map[int]func(EventView){}, hostIPs: o.HostIPs, findSave: findSave, openPath: openFolderHere}
	if b.getenv == nil {
		b.getenv = func(string) string { return "" }
	}
	if b.denv == nil {
		b.denv = launch.Host
	}
	b.procs = launch.Processes
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
		RecentHosts: []RecentHostView{}, DownloadsURL: DownloadsURL, ShowIPs: cfg.UI.ShowIPs,
	}
	if c, err := playercolor.Parse(eff.Color); err == nil && eff.Color != "" {
		v.Color = c.Hex()
	}
	for _, h := range eff.RecentHosts {
		label := h.Label
		if label == "" {
			label = addrLabel(h.Addr)
		}
		v.RecentHosts = append(v.RecentHosts, RecentHostView{Addr: h.Addr, Masked: hideIPs.addr(h.Addr), Label: label})
	}
	return v
}

// State returns the current (or last) session's state.
func (b *Bridge) State() StateView {
	st := b.ctrl.Snapshot()
	running := st.Session != 0 && st.Phase != session.Ended && st.Phase != session.Idle
	b.noteReached(st)
	mk := b.masker()
	v := stateView(st, running, b.version, b.now(), mk)
	v.StartDolphinMyself = b.launchOptions().Manual
	v.Coop = b.coopView(st, running, mk)
	return v
}

// masker is how the window shows IP addresses now: masked, unless the
// "Show IP addresses" switch is on.
func (b *Bridge) masker() masker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return masker{show: b.cfg.UI.ShowIPs}
}

// maskErr masks the IPs in *err as the window shows them (masker.text),
// unless the switch is on. Every exported method that returns an error
// defers it first (TestBridgeErrorsMasked checks they all do): an error
// reaches the page as a toast or a form's message, and a network one
// names both ends ("write tcp 192.168.1.5:51234->192.168.1.42:25565:
// ..."). errors.Is and errors.As still see the error it masks.
func (b *Bridge) maskErr(err *error) {
	if *err == nil {
		return
	}
	if msg := b.masker().text((*err).Error()); msg != (*err).Error() {
		*err = maskedError{msg: msg, err: *err}
	}
}

// quoted is a "%q" in an error message.
var quoted = regexp.MustCompile(`"[^"]*"`)

// maskedError is an error with its IPs masked (maskErr).
type maskedError struct {
	msg string
	err error
}

func (e maskedError) Error() string { return e.msg }
func (e maskedError) Unwrap() error { return e.err }

// SetShowIPs sets the header's "Show IP addresses" switch and saves it.
// On, the window masks nothing; off (the default), every IP it shows is
// masked. Every page gets a "settings" event to draw itself again.
func (b *Bridge) SetShowIPs(on bool) {
	b.mu.Lock()
	b.cfg.UI.ShowIPs = on
	b.mu.Unlock()
	b.save()
	b.emit(EventView{Kind: "settings", Time: b.now().UTC().Format(time.RFC3339Nano)})
}

// Host starts a session that hosts a lobby on this machine.
func (b *Bridge) Host(r HostRequest) (err error) {
	defer b.maskErr(&err)
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
	port := r.Port
	if port == 0 {
		port = config.DefaultPort
	}
	listen := strings.TrimSpace(b.getenv(EnvListenHost))
	b.mu.Lock()
	b.myDryRun = false
	b.mu.Unlock()
	b.stopLanListen()
	id, err := b.ctrl.HostLobby(b.ctx, session.HostOptions{Name: name, Color: col, Port: port, ListenHost: listen, LaunchFn: b.launchOptions, CoopFn: b.coopOptions})
	if err != nil {
		return err
	}
	b.sessMu.Lock()
	b.hostPort = port
	b.sessMu.Unlock()
	b.startBeacon(id, port)
	b.remember(name, col, "", r.Port)
	return nil
}

// Join starts a session in the lobby of the host at r.Addr (joined
// directly when the host has no lobby). r.Addr may be a join code
// (netutil.JoinCode): the session joins the address it holds.
func (b *Bridge) Join(r JoinRequest) (err error) {
	defer b.maskErr(&err)
	addr := strings.TrimSpace(r.Addr)
	if addr == "" {
		return errors.New("type the host's address (the one its window shows)")
	}
	// As the TUI's Connect does: a typo never reaches the header, the log
	// or a dialog. The error quotes what was typed, masked as an address
	// (it may be most of an IP, or a host name with one in it).
	if err := netutil.ValidateAddr(addr); err != nil {
		msg := "Bad address: " + err.Error()
		if mk := b.masker(); !mk.show {
			msg = quoted.ReplaceAllStringFunc(msg, func(q string) string { return `"` + mk.addr(q[1:len(q)-1]) + `"` })
		}
		return errors.New(msg)
	}
	if ap, err := netutil.ParseJoinCode(addr); err == nil {
		addr = joinAddr(ap)
	}
	name, err := checkName(r.Name)
	if err != nil {
		return err
	}
	col, err := parseColor(r.Color)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.myDryRun = false
	b.mu.Unlock()
	b.stopLanListen()
	id, err := b.ctrl.JoinLobby(b.ctx, session.JoinOptions{Addr: addr, Name: name, Color: col, LaunchFn: b.launchOptions, CoopFn: b.coopOptions})
	if err != nil {
		return err
	}
	b.sessMu.Lock()
	b.lastJoin = &JoinRequest{Addr: addr, Name: name, Color: r.Color}
	b.joining = &pendingHost{session: id, addr: addr}
	b.sessMu.Unlock()
	b.remember(name, col, "", 0)
	b.noteReached(b.ctrl.Snapshot())
	return nil
}

// noteReached saves the running join's address as a recent host once st
// shows it reached the host (its lobby, or a pre-lobby host joined
// directly).
func (b *Bridge) noteReached(st session.State) {
	b.sessMu.Lock()
	p := b.joining
	reached := p != nil && st.Session == p.session && st.Lobby != nil &&
		(st.Lobby.Link.State == session.LinkUp || st.Lobby.Legacy || st.Phase == session.InLobby)
	if reached {
		b.joining = nil
	}
	b.sessMu.Unlock()
	if !reached {
		return
	}
	b.mu.Lock()
	b.cfg.AddRecentHost(p.addr, "", b.now())
	b.mu.Unlock()
	b.save()
}

// Retry leaves the running session, if any, and joins the last Join's
// host again (the "Can't reach the host" dialog's Retry: a fresh first
// attempt instead of waiting out the backoff).
func (b *Bridge) Retry() (err error) {
	defer b.maskErr(&err)
	b.sessMu.Lock()
	last := b.lastJoin
	b.sessMu.Unlock()
	if last == nil {
		return errors.New("there is no join to retry")
	}
	b.ctrl.Leave()
	return b.Join(*last)
}

// Kick removes a member (or a CLI player's row) from the lobby; host
// only. The frontend asks first.
func (b *Bridge) Kick(member int) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.Kick(member)
}

// ShareAddrs is the host's share card: the session's port and this
// machine's addresses with it, labeled LAN, Radmin VPN, Tailscale or
// Other. It is empty (port 0) unless this player hosts the running
// session.
func (b *Bridge) ShareAddrs() ShareView {
	st := b.ctrl.Snapshot()
	if st.Role != session.RoleHost || !b.running() || b.hostIPs == nil {
		return ShareView{Addrs: []ShareAddrView{}}
	}
	b.sessMu.Lock()
	port := b.hostPort
	b.sessMu.Unlock()
	if port == 0 {
		port = config.DefaultPort
	}
	return ShareView{Port: port, Addrs: shareAddrs(b.hostIPs(), port)}
}

// Copy puts text on the clipboard (the window's; a browser page copies
// itself, so ui --browser doesn't serve this).
func (b *Bridge) Copy(text string) (err error) {
	defer b.maskErr(&err)
	b.clipMu.Lock()
	f := b.clipboard
	b.clipMu.Unlock()
	if f == nil {
		return errors.New("this window has no clipboard")
	}
	return f(text)
}

// OpenDownloads opens the releases page in the user's browser (the
// window's; a browser page opens it itself).
func (b *Bridge) OpenDownloads() (err error) {
	defer b.maskErr(&err)
	b.clipMu.Lock()
	f := b.openURL
	b.clipMu.Unlock()
	if f == nil {
		return errors.New("this window can't open a browser")
	}
	return f(DownloadsURL)
}

// setDesktop gives the Bridge the window's clipboard and browser.
func (b *Bridge) setDesktop(clipboard, openURL func(string) error) {
	b.clipMu.Lock()
	b.clipboard, b.openURL = clipboard, openURL
	b.clipMu.Unlock()
}

// Leave ends the running session and returns once it has torn down.
func (b *Bridge) Leave() {
	b.stopBeacon()
	b.ctrl.Leave()
}

// SetColor asks the lobby for a tunic color.
func (b *Bridge) SetColor(color string) (err error) {
	defer b.maskErr(&err)
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
func (b *Bridge) SetReady(ready bool) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.SetReady(ready)
}

// SendChat sends a chat line to the session and returns it as the chat
// panel shows it (its IPs masked unless the switch is on): the host
// relays a line to everyone but its sender, so the page echoes this.
// An empty line sends nothing and returns an empty Text.
func (b *Bridge) SendChat(text string) (_ EventView, err error) {
	defer b.maskErr(&err)
	text = strings.TrimSpace(text)
	if text == "" {
		return EventView{Kind: "chat"}, nil
	}
	if strings.HasPrefix(text, "/") && b.ctrl.Snapshot().Coop.On {
		// A console line (/help, /status, /warp ...), as the CLI takes
		// them: its outcome is in the log.
		return EventView{Kind: "chat"}, b.ctrl.CommandLine(text)
	}
	if err := b.ctrl.SendChat(text); err != nil {
		return EventView{}, err
	}
	st := b.ctrl.Snapshot()
	v := eventView(session.Event{Kind: session.EventChat, Session: st.Session, Time: b.now(), Text: text}, b.masker())
	v.From = st.Name // this player's own name: not masked, as its lobby row
	return v, nil
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

// parseColor reads a form's color: "" is no preference (nil), which
// wears classic green.
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
	b.emu.Lock()
	b.emitSeq++
	id := b.emitSeq
	b.emitters[id] = emit
	b.emu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stop()
		defer func() {
			b.emu.Lock()
			delete(b.emitters, id)
			b.emu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				emit(eventView(ev, b.masker()))
			}
		}
	}()
	return done
}

// emit hands an event of the Bridge's own (a patch's progress) to every
// pump's emit, as the controller's events go.
func (b *Bridge) emit(ev EventView) {
	b.emu.Lock()
	fns := make([]func(EventView), 0, len(b.emitters))
	for _, f := range b.emitters {
		fns = append(fns, f)
	}
	b.emu.Unlock()
	for _, f := range fns {
		f(ev)
	}
}

// setDialogs gives the Bridge the window's file dialogs.
func (b *Bridge) setDialogs(d fileDialogs) {
	b.dlgMu.Lock()
	b.dlg = d
	b.dlgMu.Unlock()
}

func (b *Bridge) dialogs() fileDialogs {
	b.dlgMu.Lock()
	defer b.dlgMu.Unlock()
	return b.dlg
}

// running says whether a session is running (window close asks first).
func (b *Bridge) running() bool { return b.State().Running }

// close ends the running session (the same teardown as Leave), cancels a
// running patch and waits for it to stop, however long its disc takes to
// finish writing (see setup_patch.go), and ends the Bridge's context.
func (b *Bridge) close() {
	b.stopBeacon()
	b.stopLanListen()
	b.ctrl.Leave()
	b.CancelPatch()
	b.waitPatch()
	b.cancel()
}

// Close questions beforeClose asks.
const (
	askStopPatch    = "patch"   // "Stop patching?"
	askLeaveSession = "session" // "Leave the session?"
)

// beforeClose decides whether the window may close (false) or stays open
// (true). It asks every question first (ask returns true for "close
// anyway") and changes nothing unless the answer is to close, so "Stay"
// leaves a patch running. Closing during a patch cancels it and keeps the
// window open, showing "Stopping…", until the patch has ended (a disc
// being written is finished first); then quit closes the window, and
// beforeClose, asked again, lets it.
func (b *Bridge) beforeClose(ask func(question string) bool, quit func()) bool {
	if b.quitting.Load() {
		return b.patching() // waiting for the cancelled patch to end
	}
	patching := b.patching()
	if patching && !ask(askStopPatch) {
		return true
	}
	if b.running() && !ask(askLeaveSession) {
		return true
	}
	if !patching {
		return false
	}
	b.quitting.Store(true)
	b.CancelPatch()
	go func() {
		b.waitPatch()
		quit()
	}()
	return true
}
