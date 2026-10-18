package gui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"
)

// `ww-multiplayer ui --browser` (design §4.3): the same frontend served
// over HTTP on 127.0.0.1, for tests (Playwright, later) and as a fallback
// where the window can't open. It is pure Go, so every OS has it.
//
// Security: the server binds 127.0.0.1 only; every /api request must carry
// the per-run token (a header for calls, the query for the event stream,
// which EventSource can't set headers on), and a Host header naming the
// loopback address and port, and an Origin, when sent, of the same; so a
// web page in the user's browser can neither call the API (no token, and a
// foreign Origin) nor reach it by DNS rebinding (a foreign Host).

// maxCallBody caps one /api/call request.
const maxCallBody = 64 << 10

// browserMethods are the Bridge methods ui --browser serves: every one but
// windowOnly, named here rather than found by reflection so a new method is
// reachable from the browser only once it is listed
// (TestBrowserMethodsAllowlist keeps the two lists and Bridge together).
var browserMethods = map[string]bool{
	"Info": true, "State": true, "Host": true, "Join": true, "Leave": true,
	"SetColor": true, "SetReady": true, "SendChat": true,
	// The first-run setup (App PR 6).
	"Setup": true, "SaveProfile": true, "CheckColor": true,
	"StartPatch": true, "CancelPatch": true, "PatchStatus": true,
	"DetectDolphin": true, "UserDirFor": true, "SetDolphin": true, "PrepareDolphin": true, "FinishSetup": true,
	// The Lobby screen (App PR 7).
	"Kick": true, "Retry": true, "ShareAddrs": true,
	// The header's "Show IP addresses" switch.
	"SetShowIPs": true,
	// Start → Dolphin → in game (App PR 8).
	"Start": true, "SetStartDolphinMyself": true, "LaunchDolphin": true,
	// Co-op (App PR 9).
	"SetCoop": true, "CoopWorlds": true, "SetMyCoopDryRun": true, "Answer": true, "SetAutoYes": true,
	"DismissAdvice": true, "RequestMerge": true, "AllowMerge": true, "DismissRequest": true, "SetMerge": true,
	"UnbindFile": true, "CoopCommand": true, "CoopStatusText": true,
	// Sessions on your network (App PR 11).
	"LanSessions": true,
	// The Mods page (Phase D D4).
	"Mods": true, "SetModEnabled": true, "CallMod": true,
	// One-click updates (update.go).
	"UpdateStatus": true, "CheckUpdate": true, "SetCheckUpdates": true, "InstallUpdate": true, "CancelUpdate": true, "Repatch": true, "RestartUpdate": true,
}

// windowOnly are the Bridge methods only the window has: PickFile opens a
// native dialog, which a browser page can't (the frontend asks for a typed
// path there); Copy and OpenDownloads use the window's clipboard and the
// desktop's browser, which a browser page has itself (navigator.clipboard,
// window.open), and which a page on 127.0.0.1 shouldn't drive on the
// machine serving it. OpenFolder opens the desktop's file manager, the
// same.
var windowOnly = map[string]bool{"PickFile": true, "Copy": true, "OpenDownloads": true, "OpenFolder": true, "OpenReleaseNotes": true}

// BrowserServer is a running ServeBrowser.
type BrowserServer struct {
	// URL is the page to open, with the token.
	URL string

	bridge *Bridge
	token  string
	addr   string // "127.0.0.1:port"
	srv    *http.Server
	ln     net.Listener

	mu      sync.Mutex
	clients map[chan EventView]struct{}
	stop    context.CancelFunc

	// quit closes once an update asks the app to close (Wait returns).
	quit     chan struct{}
	quitOnce sync.Once
}

// ServeBrowser starts the browser UI on 127.0.0.1:port (0 picks a free
// port) and returns at once; Close stops it and the session.
func ServeBrowser(o Options, port int) (*BrowserServer, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	return serveBrowser(NewBridge(o), ln)
}

func serveBrowser(b *Bridge, ln net.Listener) (*BrowserServer, error) {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		return nil, err
	}
	s := &BrowserServer{bridge: b, token: hex.EncodeToString(tok), addr: ln.Addr().String(), ln: ln, clients: map[chan EventView]struct{}{}, quit: make(chan struct{})}
	b.setQuit(func() { s.quitOnce.Do(func() { close(s.quit) }) }, true)
	s.URL = "http://" + s.addr + "/?token=" + s.token
	assets, err := Frontend()
	if err != nil {
		ln.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/call/", s.guard(http.HandlerFunc(s.call)))
	mux.Handle("/api/events", s.guard(http.HandlerFunc(s.events)))
	mux.Handle("/", s.hostOnly(http.FileServer(http.FS(assets))))
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	b.pump(ctx, s.broadcast)
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Close stops the server, ends the session (Leave) and the event pump,
// and then starts the new version an update installed, if one did.
func (s *BrowserServer) Close() error {
	s.stop()
	s.bridge.close()
	err := s.srv.Close()
	s.bridge.relaunchNow()
	return err
}

// Wait blocks until ctx ends or an update closes the app, then closes s.
func (s *BrowserServer) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
	case <-s.quit:
	}
	return s.Close()
}

// hostOnly refuses a request whose Host isn't this server's address
// (DNS rebinding) or whose Origin, when present, is foreign.
func (s *BrowserServer) hostOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.sameHost(r.Host) {
			http.Error(w, "wrong host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Scheme != "http" || !s.sameHost(u.Host) {
				http.Error(w, "wrong origin", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// guard is hostOnly plus the token.
func (s *BrowserServer) guard(h http.Handler) http.Handler {
	return s.hostOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-WW-Token")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
}

func (s *BrowserServer) sameHost(host string) bool {
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return false
	}
	return host == s.addr || host == "localhost:"+port
}

// call runs POST /api/call/<Method> with a JSON array of arguments and
// answers {"result": ...} or {"error": "..."}.
func (s *BrowserServer) call(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/call/")
	if !browserMethods[name] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("no method %q", name)})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCallBody+1))
	if err != nil || len(body) > maxCallBody {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request too large or unreadable"})
		return
	}
	res, err := callMethod(s.bridge, name, body)
	if err != nil {
		code := http.StatusOK // the method's own error, as Wails rejects the promise
		var ce callError
		if errors.As(err, &ce) {
			code = http.StatusBadRequest
		}
		// The Bridge masks its methods' errors (maskErr); this covers the
		// rest, a malformed call's echo of its arguments among them.
		writeJSON(w, code, map[string]string{"error": s.bridge.masker().text(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res})
}

// events is GET /api/events: the controller's events as server-sent
// events, one JSON EventView per message.
func (s *BrowserServer) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	ch := make(chan EventView, 256)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}

// broadcast hands ev to every event stream, dropping it for one that is
// full (a stalled browser tab mustn't stall the others).
func (s *BrowserServer) broadcast(ev EventView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- ev:
		default:
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// callError is a malformed call (unknown method, bad arguments), as
// opposed to the method's own error.
type callError struct{ msg string }

func (e callError) Error() string { return e.msg }

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// callMethod calls b's exported method name with the JSON array args, the
// way the Wails binding does: each argument decodes into its parameter's
// type; a trailing error result is the call's error, and the other result,
// if any, its value.
func callMethod(b *Bridge, name string, args []byte) (any, error) {
	m := reflect.ValueOf(b).MethodByName(name)
	if name == "" || !m.IsValid() {
		return nil, callError{fmt.Sprintf("no method %q", name)}
	}
	var raw []json.RawMessage
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &raw); err != nil {
			return nil, callError{"arguments must be a JSON array: " + err.Error()}
		}
	}
	t := m.Type()
	if len(raw) != t.NumIn() {
		return nil, callError{fmt.Sprintf("%s takes %d arguments, got %d", name, t.NumIn(), len(raw))}
	}
	in := make([]reflect.Value, len(raw))
	for i, a := range raw {
		p := reflect.New(t.In(i))
		if err := json.Unmarshal(a, p.Interface()); err != nil {
			return nil, callError{fmt.Sprintf("%s argument %d: %v", name, i+1, err)}
		}
		in[i] = p.Elem()
	}
	out := m.Call(in)
	var res any
	for _, o := range out {
		if o.Type() == errorType {
			if !o.IsNil() {
				return nil, o.Interface().(error)
			}
			continue
		}
		res = o.Interface()
	}
	return res, nil
}

// Frontend is the embedded frontend, rooted at its index.html.
func Frontend() (fs.FS, error) { return fs.Sub(frontendFS, "frontend") }
