package gui

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

func startBrowser(t *testing.T) (*BrowserServer, *fakeEngine) {
	t.Helper()
	b, e, _ := newTestBridge(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := serveBrowser(b, ln)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, e
}

// api posts a call and returns the status and decoded body.
func api(t *testing.T, s *BrowserServer, method, args string, mod func(*http.Request)) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+s.addr+"/api/call/"+method, strings.NewReader(args))
	req.Header.Set("X-WW-Token", s.token)
	if mod != nil {
		mod(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestBrowserGuards(t *testing.T) {
	s, _ := startBrowser(t)
	if !strings.HasPrefix(s.URL, "http://127.0.0.1:") || !strings.Contains(s.URL, "token="+s.token) {
		t.Fatalf("URL = %q", s.URL)
	}
	for name, c := range map[string]struct {
		mod  func(*http.Request)
		want int
	}{
		"no token":       {func(r *http.Request) { r.Header.Del("X-WW-Token") }, http.StatusUnauthorized},
		"wrong token":    {func(r *http.Request) { r.Header.Set("X-WW-Token", "00") }, http.StatusUnauthorized},
		"rebinding host": {func(r *http.Request) { r.Host = "evil.example:80" }, http.StatusForbidden},
		"foreign origin": {func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, http.StatusForbidden},
		"same origin":    {func(r *http.Request) { r.Header.Set("Origin", "http://"+s.addr) }, http.StatusOK},
		"query token":    {func(r *http.Request) { r.Header.Del("X-WW-Token"); r.URL.RawQuery = "token=" + s.token }, http.StatusOK},
	} {
		if code, _ := api(t, s, "State", "[]", c.mod); code != c.want {
			t.Errorf("%s: status %d, want %d", name, code, c.want)
		}
	}
	// The page itself needs no token but the right Host.
	resp, err := http.Get("http://" + s.addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "transport") && !strings.Contains(string(page), "app.js") {
		t.Fatalf("index: %d %q", resp.StatusCode, page)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+s.addr+"/", nil)
	req.Host = "evil.example"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("index with a foreign Host: %d", resp.StatusCode)
	}
}

func TestBrowserCalls(t *testing.T) {
	s, e := startBrowser(t)
	if code, body := api(t, s, "Nope", "[]", nil); code != http.StatusBadRequest || body["error"] == nil {
		t.Fatalf("unknown method: %d %v", code, body)
	}
	if code, body := api(t, s, "Host", "[]", nil); code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "1 arguments") {
		t.Fatalf("wrong arity: %d %v", code, body)
	}
	if code, _ := api(t, s, "Host", `[{"name": 5}]`, nil); code != http.StatusBadRequest {
		t.Fatalf("bad argument: %d", code)
	}
	if code, _ := api(t, s, "pump", "[]", nil); code != http.StatusBadRequest {
		t.Fatalf("an unexported method was callable: %d", code)
	}
	// A method's own error is a 200 with "error", as Wails rejects.
	if code, body := api(t, s, "Host", `[{"name": ""}]`, nil); code != 200 || body["error"] == nil {
		t.Fatalf("Host with no name: %d %v", code, body)
	}

	code, body := api(t, s, "Info", "[]", nil)
	if code != 200 || body["result"].(map[string]any)["version"] != "v9.9.9" {
		t.Fatalf("Info: %d %v", code, body)
	}
	if code, body := api(t, s, "Host", `[{"name": "Link", "color": "green", "port": 25571}]`, nil); code != 200 || body["error"] != nil {
		t.Fatalf("Host: %d %v", code, body)
	}
	e.out(t)
	_, body = api(t, s, "State", "[]", nil)
	st := body["result"].(map[string]any)
	if st["phase"] != "lobby" || st["running"] != true || st["lobby"].(map[string]any)["host"] != true {
		t.Fatalf("State: %v", st)
	}
	if code, body := api(t, s, "SetReady", "[true]", nil); code != 200 || body["error"] != nil {
		t.Fatalf("SetReady: %d %v", code, body)
	}
	if code, body := api(t, s, "Leave", "[]", nil); code != 200 || body["error"] != nil {
		t.Fatalf("Leave: %d %v", code, body)
	}
	_, body = api(t, s, "State", "[]", nil)
	if st := body["result"].(map[string]any); st["running"] != false || st["phase"] != "ended" {
		t.Fatalf("State after Leave: %v", st)
	}
}

func TestBrowserEvents(t *testing.T) {
	s, e := startBrowser(t)
	resp, err := http.Get("http://" + s.addr + "/api/events?token=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	r := bufio.NewReader(resp.Body)
	if l, _ := r.ReadString('\n'); !strings.HasPrefix(l, ":") { // the stream is open
		t.Fatalf("first line %q", l)
	}
	if code, body := api(t, s, "Join", `[{"addr": "10.0.0.9", "name": "Medli"}]`, nil); code != 200 || body["error"] != nil {
		t.Fatalf("Join: %d %v", code, body)
	}
	e.out(t).Log(report.OK, "welcome aboard")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		data, ok := strings.CutPrefix(strings.TrimSpace(l), "data: ")
		if !ok {
			continue
		}
		var ev EventView
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Kind == "log" && ev.Text == "welcome aboard" && ev.Level == "ok" {
			return
		}
	}
	t.Fatal("the log event never arrived")
}

// TestFrontendCallsBridgeMethods checks the frontend only calls methods the
// Bridge has, and the Bridge exports nothing the frontend doesn't call:
// Wails binds every exported method, so an extra one is reachable from JS.
func TestFrontendCallsBridgeMethods(t *testing.T) {
	assets, err := Frontend()
	if err != nil {
		t.Fatal(err)
	}
	called := map[string]bool{}
	re := regexp.MustCompile(`call\("([A-Za-z]+)"`)
	err = fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		b, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			called[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	bt := reflect.TypeOf(&Bridge{})
	var exported []string
	for i := 0; i < bt.NumMethod(); i++ {
		exported = append(exported, bt.Method(i).Name)
	}
	sort.Strings(exported)
	for name := range called {
		if _, ok := bt.MethodByName(name); !ok {
			t.Errorf("the frontend calls %s, which Bridge doesn't have", name)
		}
	}
	for _, name := range exported {
		if !called[name] {
			t.Errorf("Bridge exports %s, which the frontend never calls", name)
		}
	}
	for _, f := range []string{"index.html", "app.css", "app.js", "transport.js", "fonts/OFL.txt"} {
		if _, err := fs.Stat(assets, f); err != nil {
			t.Errorf("frontend is missing %s", f)
		}
	}
}
