package main

// app-found: check 2 of card #1165, "app-lobby founding with a bound
// save", live. It drives two real app processes (`ww-multiplayer ui
// --browser`) against two Dolphins that already run the game, through
// the window's own HTTP API, the way a player's clicks do: Host (with
// the app's default co-op choice, never SetCoop unless --mode), Join,
// Start anyway, then watches both windows' State. The 11-08 session
// (card #1157) had the host's save bound to a world that was not on the
// host's PC: the founding looped, GO never went out and the lobby sat on
// "Starting". Since #1173 (auto: the world follows the save, "adopted")
// and #1175 (GO at Start), both windows reach the game and the joiner's
// co-op runs in the save's world.
//
// Each app gets a fresh APPDATA, so its config is the default one and
// its co-op worlds folder (<APPDATA>/ww-multiplayer/worlds) is empty:
// the host's save, bound to W, names a world not on "this PC".
//
// Exit 0: every check passed; 1: a check failed; 2: it could not run
// (usage, no Dolphins, A's file not bound, an app that never served its
// page).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

const appFoundUsage = "app-found [--exe PATH] [--pids A,B] [--port N] [--mode new] [--timeout 30s] [--out FILE] [--world-root DIR]"

// appFoundSeed is how to bind both Dolphins' loaded files to one world
// in RAM, the precondition: a co-op session founds a world under an
// APPDATA of its own, which the run's fresh ones never see.
const appFoundSeed = `APPDATA="$(mktemp -d)" MP_LOCAL_PIDS=<A>,<B> wwdev smoke --no-boot -- --coop=new --yes`

// appFoundOpts are app-found's flags.
type appFoundOpts struct {
	exe       string
	pids      string        // "A,B"; "" for MP_LOCAL_PIDS or smoke's pick
	port      int           // the host's lobby port; 0 for a free one
	mode      string        // "" leaves the app's default co-op choice
	timeout   time.Duration // after Start
	out       string
	worldRoot string // where the fresh APPDATA folders go; "" for the temp folder
}

func parseAppFoundArgs(args []string, getenv func(string) string) (appFoundOpts, error) {
	o := appFoundOpts{timeout: 30 * time.Second}
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--exe":
			o.exe, err = val()
		case "--pids":
			o.pids, err = val()
		case "--port":
			var v string
			if v, err = val(); err == nil {
				o.port, err = strconv.Atoi(v)
				if err != nil || o.port < 0 || o.port > 65535 {
					err = fmt.Errorf("bad --port %q", v)
				}
			}
		case "--mode":
			o.mode, err = val()
		case "--timeout":
			var v string
			if v, err = val(); err == nil {
				o.timeout, err = time.ParseDuration(v)
				if err != nil || o.timeout <= 0 {
					err = fmt.Errorf("bad --timeout %q", v)
				}
			}
		case "--out":
			o.out, err = val()
		case "--world-root":
			o.worldRoot, err = val()
		default:
			err = fmt.Errorf("unknown argument %q", a)
		}
		if err != nil {
			return o, err
		}
	}
	// Only the app's default (auto) and the 11-08 host's choice (new)
	// have expectations a run can judge (appFoundVerdicts).
	switch o.mode {
	case "", "new":
	case "auto":
		o.mode = ""
	default:
		return o, fmt.Errorf("--mode %q: leave it out (the app's default, auto) or give new (the 11-08 host's choice)", o.mode)
	}
	if o.out == "" {
		o.out = filepath.Join(devLogsRootOr(getenv), "latest", "app-found.json")
	}
	return o, nil
}

// appFoundRequest is the host's co-op choice base with --mode applied:
// "auto", "new", "off", or "continue:<world>".
func appFoundRequest(base gui.CoopRequest, mode string) (gui.CoopRequest, error) {
	r := base
	switch m, w, _ := strings.Cut(mode, ":"); m {
	case "auto", "new", "off":
		if w != "" {
			return r, fmt.Errorf("--mode %s takes no world", m)
		}
		r.Mode = m
	case "continue":
		if w == "" {
			return r, errors.New("--mode continue needs its world: continue:<16 hex digits>")
		}
		r.Mode, r.World = m, w
	default:
		return r, fmt.Errorf("bad --mode %q: auto, new, off or continue:<world>", mode)
	}
	return r, nil
}

// appFoundSetting is the lobby's co-op setting a request leads to.
func appFoundSetting(r gui.CoopRequest) string {
	if r.Mode == "continue" {
		return strings.ToLower(r.World)
	}
	return r.Mode
}

// defaultAppExe is ww-multiplayer next to wwdev, else in the working
// folder.
func defaultAppExe() string {
	name := "ww-multiplayer"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(self), name); appFoundIsFile(p) {
			return p
		}
	}
	return "." + string(filepath.Separator) + name
}

func appFoundIsFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// appURLRe is the page address `ui --browser` prints.
var appURLRe = regexp.MustCompile(`(http://127\.0\.0\.1:\d+)/\?token=([0-9A-Fa-f]+)`)

// parseAppURL reads the page's base URL and token out of one line of the
// app's output.
func parseAppURL(line string) (base, token string, ok bool) {
	m := appURLRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// appCallReply is /api/call's answer: {"result": ...} or {"error": "..."}
// (a method's own error comes with 200, a malformed call with 400).
type appCallReply struct {
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// decodeAppReply reads one /api/call answer.
func decodeAppReply(status int, body []byte) (json.RawMessage, error) {
	var r appCallReply
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	return r.Result, nil
}

// appChild is one app process serving its window over HTTP.
type appChild struct {
	label   string
	appData string
	userDir string
	pid     uint32 // its Dolphin's
	cmd     *exec.Cmd
	base    string
	token   string
	logPath string
	done    chan struct{} // closed when the process has exited
	waitErr error
	http    *http.Client
}

// appChildEnv is env with the run's own values: every key of set
// replaces any spelling of it, and drop removes keys outright.
func appChildEnv(env []string, set map[string]string, drop ...string) []string {
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if slices.ContainsFunc(drop, func(d string) bool { return strings.EqualFold(d, k) }) {
			continue
		}
		replaced := false
		for s := range set {
			if strings.EqualFold(s, k) {
				replaced = true
			}
		}
		if !replaced {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if set[k] != "" {
			out = append(out, k+"="+set[k])
		}
	}
	return out
}

// startAppChild starts exe's `ui --browser --no-open` with env and waits
// up to wait for the page address it prints.
func startAppChild(ctx context.Context, c *appChild, exe string, env []string, wait time.Duration) error {
	lf, err := os.Create(c.logPath)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	c.cmd = exec.Command(exe, "ui", "--browser", "--no-open")
	c.cmd.Env = env
	c.cmd.Stdout, c.cmd.Stderr = pw, pw
	if err := c.cmd.Start(); err != nil {
		lf.Close()
		return fmt.Errorf("start %s: %w", exe, err)
	}
	c.done = make(chan struct{})
	go func() {
		c.waitErr = c.cmd.Wait()
		pw.Close()
		close(c.done)
	}()
	urlc := make(chan [2]string, 1)
	go func() {
		defer lf.Close()
		sc := bufio.NewScanner(pr)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			fmt.Fprintln(lf, line)
			if !sent {
				if base, tok, ok := parseAppURL(line); ok {
					urlc <- [2]string{base, tok}
					sent = true
				}
			}
		}
		io.Copy(io.Discard, pr) // a line too long for the scanner: keep the pipe drained
	}()
	select {
	case u := <-urlc:
		c.base, c.token = u[0], u[1]
		c.http = &http.Client{Timeout: 10 * time.Second}
		return nil
	case <-c.done:
		return fmt.Errorf("app %s exited before serving its page (%v); see %s", c.label, c.waitErr, c.logPath)
	case <-time.After(wait):
		return fmt.Errorf("app %s printed no page address in %s; see %s", c.label, wait, c.logPath)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// call runs one Bridge method by name with args.
func (c *appChild) call(method string, args ...any) (json.RawMessage, error) {
	if args == nil {
		args = []any{}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.base+"/api/call/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WW-Token", c.token)
	req.Header.Set("Origin", c.base)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", c.label, method, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", c.label, method, err)
	}
	res, err := decodeAppReply(resp.StatusCode, b)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", c.label, method, err)
	}
	return res, nil
}

func (c *appChild) state() (gui.StateView, error) {
	var v gui.StateView
	res, err := c.call("State")
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(res, &v)
}

// stop leaves the session and ends the process.
func (c *appChild) stop() {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return
	}
	select {
	case <-c.done:
		return
	default:
	}
	if c.http != nil {
		c.call("Leave")
	}
	c.cmd.Process.Kill()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
	}
}

// appTag is one Dolphin's loaded file as its co-op tag reads.
type appTag struct {
	PID   uint32 `json:"pid"`
	Slot  uint8  `json:"slot"`
	Name  string `json:"name"`
	State string `json:"state"`
	World string `json:"world,omitempty"`
	Why   string `json:"why"`
	Err   string `json:"err,omitempty"`
	file  bind.File
	ok    bool
}

// readAppTag reads Dolphin pid's loaded file (as coop-tag show does).
func readAppTag(pid uint32) appTag {
	t := appTag{PID: pid}
	d, err := emu.FindByPID(pid, "GZLE01")
	if err != nil {
		t.Err = err.Error()
		return t
	}
	defer d.Close()
	f, err := bind.ReadFile(d)
	if err != nil {
		t.Err = err.Error()
		return t
	}
	return appTagOf(pid, f)
}

func appTagOf(pid uint32, f bind.File) appTag {
	v := bind.Classify(f, bind.Expect{})
	t := appTag{PID: pid, Slot: f.Slot, Name: f.PlayerName(), State: v.State.String(), Why: v.Why, file: f, ok: true}
	if v.State == bind.Bound && v.HasTag {
		t.World = v.Tag.World.String()
	}
	return t
}

// appSide is one window's state as the report keeps it.
type appSide struct {
	Phase       string              `json:"phase"`
	LobbyPhase  string              `json:"lobby_phase,omitempty"`
	LobbyCoop   string              `json:"lobby_coop,omitempty"`
	Members     int                 `json:"members"`
	CoopOn      bool                `json:"coop_on"`
	CoopSetting string              `json:"coop_setting,omitempty"`
	CoopWorld   string              `json:"coop_world,omitempty"`
	Banner      *gui.BannerView     `json:"banner,omitempty"`
	Dialog      *gui.DialogView     `json:"dialog,omitempty"`
	Dolphin     gui.GameDolphinView `json:"dolphin"`
}

func appSideOf(v gui.StateView) appSide {
	s := appSide{Phase: v.Phase, Banner: v.Banner, Dialog: v.Dialog, Dolphin: v.Dolphin}
	if l := v.Lobby; l != nil {
		s.LobbyPhase, s.LobbyCoop, s.Members = l.Phase, l.Coop, len(l.Members)
	}
	if c := v.Coop; c != nil {
		s.CoopOn, s.CoopSetting, s.CoopWorld = c.On, c.Setting, c.World
	}
	return s
}

// The phases as the window's State names them: StateView.Phase is
// session.Phase.String(), LobbyView.Phase the lobby's.
const (
	appPhaseInGame      = "in game"
	appLobbyPhaseInGame = "in_game"
)

// blockedFounding says what in v shows a blocked founding ("" for
// nothing): the host's guided dialog on a file that can't found a world
// (its check line names the loaded file), its banner offering "Remove
// the tag", or any window reading the host's "can't found a world".
func blockedFounding(v gui.StateView) string {
	if d := v.Dialog; d != nil && d.Kind == "founding" && d.Check != nil && strings.HasPrefix(d.Check.Text, "Loaded: ") {
		return "founding dialog: " + d.Text
	}
	if b := v.Banner; b != nil {
		if b.Title == "Founding the new world:" && b.ActionLabel == "Remove the tag" {
			return "founding banner: " + b.Text
		}
		if strings.Contains(b.Text, "can't found a world") {
			return "banner: " + b.Title + " " + b.Text
		}
	}
	return ""
}

// appFoundObs is what the run saw, folded from the windows' states.
type appFoundObs struct {
	Mode            string    `json:"mode"`
	World           string    `json:"world"` // W: A's file's world before the run
	CoopBeforeStart string    `json:"coop_before_start"`
	Start           time.Time `json:"-"`
	// Milliseconds after Start; -1 until seen.
	HostInGameMs int64  `json:"host_in_game_ms"`
	JoinInGameMs int64  `json:"join_in_game_ms"`
	JoinCoopMs   int64  `json:"join_coop_ms"`
	AdoptedMs    int64  `json:"adopted_ms"`
	JoinCoopOn   bool   `json:"join_coop_on"`
	JoinWorld    string `json:"join_world"`
	// Blocked is the first blocked founding any window showed ("" none).
	Blocked     string   `json:"blocked,omitempty"`
	HostBanners []string `json:"host_banners,omitempty"` // each distinct host banner text, in order
	AdoptedPath string   `json:"adopted_path"`
	Adopted     bool     `json:"adopted"`
	Polls       int      `json:"polls"`
	Host        appSide  `json:"host"`
	Join        appSide  `json:"join"`
}

func newAppFoundObs(mode, world, adoptedPath string) *appFoundObs {
	return &appFoundObs{Mode: mode, World: world, AdoptedPath: adoptedPath, HostInGameMs: -1, JoinInGameMs: -1, JoinCoopMs: -1, AdoptedMs: -1}
}

// observe folds one poll of both windows, at now; adopted says the
// adopted marker exists.
func (o *appFoundObs) observe(host, join gui.StateView, adopted bool, now time.Time) {
	o.Polls++
	at := now.Sub(o.Start).Milliseconds()
	mark := func(p *int64, ok bool) {
		if ok && *p < 0 {
			*p = at
		}
	}
	mark(&o.HostInGameMs, host.Phase == appPhaseInGame)
	mark(&o.JoinInGameMs, join.Phase == appPhaseInGame)
	jc := join.Coop != nil && join.Coop.On
	o.JoinCoopOn = jc
	o.JoinWorld = ""
	if join.Coop != nil {
		o.JoinWorld = join.Coop.World
	}
	mark(&o.JoinCoopMs, jc && o.JoinWorld == o.World)
	o.Adopted = adopted
	mark(&o.AdoptedMs, adopted)
	if o.Blocked == "" {
		if b := blockedFounding(host); b != "" {
			o.Blocked = "host " + b
		} else if b := blockedFounding(join); b != "" {
			o.Blocked = "joiner " + b
		}
	}
	if b := host.Banner; b != nil && b.Text != "" {
		t := strings.TrimSpace(b.Title + " " + b.Text)
		if len(o.HostBanners) == 0 || o.HostBanners[len(o.HostBanners)-1] != t {
			o.HostBanners = append(o.HostBanners, t)
		}
	}
	o.Host, o.Join = appSideOf(host), appSideOf(join)
}

// settled says the run has what it watches for: both in game, the
// joiner's co-op in W, the world adopted.
func (o *appFoundObs) settled() bool {
	if o.Mode == "new" {
		return o.HostInGameMs >= 0 && o.JoinInGameMs >= 0 && o.Blocked != ""
	}
	return o.HostInGameMs >= 0 && o.JoinInGameMs >= 0 && o.JoinCoopMs >= 0 && o.Adopted
}

// appFoundVerdicts are the run's checks over what it saw and the two
// files read after it (nil: not read). Both files are in W before the
// run (appFoundRun refuses otherwise), so their binds are a
// precondition, not evidence; "files in W" after it says the founding
// tagged no file to another world.
//
// With the app's default (auto), the 11-08 save is adopted: both in game,
// the joiner's co-op on in W, the adopted marker, no blocked founding.
// With --mode new, the 11-08 host's choice, the founding still can't use
// a save bound elsewhere, but since #1175 GO goes out at Start (both in
// game, never stuck on Starting) and since #1163 a window says why: the
// blocked founding is expected there, and co-op stays off.
func appFoundVerdicts(o *appFoundObs, fa, fb *appTag) []smokeCheck {
	var out []smokeCheck
	if o.Mode == "" {
		out = append(out, passIf(o.CoopBeforeStart == "auto", "lobby coop auto",
			fmt.Sprintf("the host's lobby read co-op %q before Start with no SetCoop (want \"auto\")", o.CoopBeforeStart), nil))
	} else {
		out = append(out, smokeCheck{Name: "lobby coop auto", Skipped: true,
			Detail: fmt.Sprintf("--mode %s set the host's choice; the lobby read %q", o.Mode, o.CoopBeforeStart)})
	}
	out = append(out, passIf(o.HostInGameMs >= 0 && o.JoinInGameMs >= 0, "both in game",
		fmt.Sprintf("host phase %q (lobby %q), joiner phase %q (lobby %q)", o.Host.Phase, o.Host.LobbyPhase, o.Join.Phase, o.Join.LobbyPhase),
		map[string]any{"host_ms": o.HostInGameMs, "join_ms": o.JoinInGameMs}))
	if o.Mode == "new" {
		out = append(out, passIf(o.Blocked != "", "blocked founding says why",
			fmt.Sprintf("with new and a save bound elsewhere a window must say why co-op isn't running; saw %q", o.Blocked), nil))
		out = append(out, smokeCheck{Name: "joiner coop in W", Skipped: true, Detail: "--mode new: co-op stays off by design"})
	} else {
		out = append(out, passIf(o.JoinCoopMs >= 0, "joiner coop in W",
			fmt.Sprintf("joiner co-op on %v, world %q (want %s)", o.JoinCoopOn, o.JoinWorld, o.World), map[string]any{"ms": o.JoinCoopMs}))
		out = append(out, passIf(o.Adopted, "world adopted on host", o.AdoptedPath, map[string]any{"ms": o.AdoptedMs}))
		detail := "no window showed a blocked founding"
		if o.Blocked != "" {
			detail = o.Blocked
		}
		out = append(out, passIf(o.Blocked == "", "no blocked founding", detail, nil))
	}
	switch {
	case fa == nil || fb == nil || !fa.ok || !fb.ok:
		why := "the files were not read"
		for _, t := range []*appTag{fa, fb} {
			if t != nil && t.Err != "" {
				why = t.Err
			}
		}
		out = append(out, passIf(false, "files in W", why, nil))
	default:
		out = append(out, passIf(fa.World == o.World && fb.World == o.World, "files in W",
			fmt.Sprintf("A's file in %q, B's in %q (want %s)", fa.World, fb.World, o.World), nil))
	}
	return out
}

// appFoundReport is app-found.json.
type appFoundReport struct {
	Started     time.Time         `json:"started"`
	Ended       time.Time         `json:"ended"`
	Outcome     string            `json:"outcome"` // pass, fail, cannot run: <why>, interrupted
	ExitCode    int               `json:"exit_code"`
	Args        []string          `json:"args"`
	Exe         string            `json:"exe"`
	PIDs        [2]uint32         `json:"pids"`
	AppData     [2]string         `json:"appdata"`
	UserDirs    [2]string         `json:"user_dirs"`
	Pages       [2]string         `json:"pages,omitempty"` // each app's base URL (no token)
	HostPort    int               `json:"host_port"`
	TagsBefore  [2]appTag         `json:"tags_before"`
	TagsAfter   []appTag          `json:"tags_after,omitempty"`
	StartedAtMs int64             `json:"start_pressed_ms,omitempty"` // after the run began
	Obs         *appFoundObs      `json:"observed,omitempty"`
	Checks      []smokeCheck      `json:"checks"`
	Failed      []string          `json:"failed,omitempty"`
	Logs        map[string]string `json:"logs,omitempty"`
}

// runAppFound is `app-found`: it returns the exit code.
func runAppFound(args []string) int {
	o, err := parseAppFoundArgs(args, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "app-found: %v\nusage: wwdev %s\n", err, appFoundUsage)
		return smokeExitCannot
	}
	if o.exe == "" {
		o.exe = defaultAppExe()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rep := &appFoundReport{Started: time.Now(), Args: args, Exe: o.exe, Checks: []smokeCheck{}}
	cannot := appFoundRun(ctx, o, rep)
	return finishAppFound(ctx, o, rep, cannot)
}

func appFoundLogf(format string, a ...any) { fmt.Printf("app-found: "+format+"\n", a...) }

// appFoundRun runs the check into rep; it returns why it could not run
// ("" when it ran).
func appFoundRun(ctx context.Context, o appFoundOpts, rep *appFoundReport) string {
	if !appFoundIsFile(o.exe) {
		return fmt.Sprintf("no app at %s (build it: go build -o ww-multiplayer.exe ./cmd/ww-multiplayer, or pass --exe)", o.exe)
	}
	// The Dolphins.
	running, _ := emu.ListPIDs()
	var pidA, pidB uint32
	if o.pids != "" {
		var ok bool
		if pidA, pidB, ok = pinnedPIDs(o.pids, running); !ok {
			return fmt.Sprintf("--pids %s doesn't name two running Dolphins (running: %v)", o.pids, running)
		}
	} else {
		var err error
		if pidA, pidB, err = smokePIDsFrom(running, os.Getenv, readLaunchRecords(devLogsRootOr(os.Getenv)), processStart); err != nil {
			return err.Error()
		}
	}
	rep.PIDs = [2]uint32{pidA, pidB}
	rep.UserDirs = [2]string{os.Getenv("USER_DIR_1"), os.Getenv("USER_DIR_2")}

	// A's file must be bound to a world W.
	rep.TagsBefore = [2]appTag{readAppTag(pidA), readAppTag(pidB)}
	ta := rep.TagsBefore[0]
	appFoundLogf("A (pid %d): file %d %q %s %s; B (pid %d): file %d %q %s %s", pidA, ta.Slot, ta.Name, ta.State, ta.World,
		pidB, rep.TagsBefore[1].Slot, rep.TagsBefore[1].Name, rep.TagsBefore[1].State, rep.TagsBefore[1].World)
	switch {
	case ta.Err != "":
		return fmt.Sprintf("reading A's file (pid %d): %s", pidA, ta.Err)
	case ta.World == "":
		return fmt.Sprintf("A's file is not bound (%s: %s); seed both files with: %s", ta.State, ta.Why, appFoundSeed)
	}
	w := ta.World
	// B's too, or the joiner gets a Bind question nobody answers.
	if tb := rep.TagsBefore[1]; tb.Err != "" || tb.World != w {
		return fmt.Sprintf("B's file is not bound to A's world %s (%s %s %s); seed both files with: %s", w, tb.State, tb.World, tb.Err, appFoundSeed)
	}

	// Two fresh APPDATA folders: default configs, no worlds.
	if o.worldRoot != "" {
		if err := os.MkdirAll(o.worldRoot, 0o755); err != nil {
			return err.Error()
		}
	}
	for i, l := range []string{"A", "B"} {
		d, err := os.MkdirTemp(o.worldRoot, "app-found-"+l+"-")
		if err != nil {
			return err.Error()
		}
		rep.AppData[i] = d
	}
	adopted := filepath.Join(rep.AppData[0], "ww-multiplayer", "worlds", w, "adopted")
	if _, err := os.Stat(filepath.Dir(adopted)); err == nil {
		return "world " + w + " is already in A's fresh worlds folder"
	}
	obs := newAppFoundObs(o.mode, w, adopted)
	rep.Obs = obs

	port := o.port
	if port == 0 {
		var err error
		if port, err = appFoundFreePort(); err != nil {
			return err.Error()
		}
	}
	rep.HostPort = port

	// The two apps.
	dir := filepath.Dir(o.out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err.Error()
	}
	rep.Logs = map[string]string{}
	kids := [2]*appChild{}
	defer func() {
		for _, c := range kids {
			c.stop()
		}
	}()
	for i, l := range []string{"A", "B"} {
		c := &appChild{label: l, appData: rep.AppData[i], userDir: rep.UserDirs[i], pid: rep.PIDs[i],
			logPath: filepath.Join(dir, "app-found-"+l+".log")}
		rep.Logs["app_"+l] = c.logPath
		set := map[string]string{
			"APPDATA":        c.appData,
			"USER_DIR_1":     c.userDir,
			"WW_DOLPHIN_PID": strconv.FormatUint(uint64(c.pid), 10),
			"ISO_PATH":       os.Getenv("ISO_PATH"),
			"WW_LAN":         "off",
		}
		if i == 0 {
			set["WW_LISTEN_HOST"] = "127.0.0.1"
		}
		env := appChildEnv(os.Environ(), set, "USER_DIR_2", "WW_DOLPHIN_INDEX", "WW_LISTEN_HOST", "MP_LOCAL_PIDS")
		kids[i] = c // stopped at the end even when it never served its page
		if err := startAppChild(ctx, c, o.exe, env, 30*time.Second); err != nil {
			return err.Error()
		}
		rep.Pages[i] = c.base
		appFoundLogf("app %s at %s (APPDATA %s, Dolphin %d)", l, c.base, c.appData, c.pid)
	}
	a, b := kids[0], kids[1]
	for _, c := range kids {
		if _, err := c.call("SetStartDolphinMyself", true); err != nil {
			return err.Error()
		}
	}
	if _, err := a.call("Host", map[string]any{"name": "Host", "port": port}); err != nil {
		return err.Error()
	}
	if _, err := b.call("Join", map[string]any{"addr": fmt.Sprintf("127.0.0.1:%d", port), "name": "Joiner"}); err != nil {
		return err.Error()
	}
	va, err := waitAppState(ctx, a, 20*time.Second, func(v gui.StateView) bool { return v.Lobby != nil && len(v.Lobby.Members) == 2 })
	if err != nil {
		return "the joiner never showed in the host's lobby: " + err.Error()
	}
	if o.mode != "" {
		base := gui.CoopRequest{}
		if va.Coop != nil {
			base = va.Coop.Choice
		}
		r, _ := appFoundRequest(base, o.mode)
		if _, err := a.call("SetCoop", r); err != nil {
			return err.Error()
		}
		want := appFoundSetting(r)
		va, _ = waitAppState(ctx, a, 5*time.Second, func(v gui.StateView) bool { return v.Lobby != nil && v.Lobby.Coop == want })
	} else {
		// The app sends its saved choice once its lobby is up (Host's
		// sendHostCoop); a fresh config's is auto. Never set here.
		va, _ = waitAppState(ctx, a, 5*time.Second, func(v gui.StateView) bool { return v.Lobby != nil && v.Lobby.Coop != "" && v.Lobby.Coop != "off" })
	}
	if va.Lobby != nil {
		obs.CoopBeforeStart = va.Lobby.Coop
	}
	appFoundLogf("the host's lobby reads co-op %q before Start", obs.CoopBeforeStart)

	obs.Start = time.Now()
	rep.StartedAtMs = obs.Start.Sub(rep.Started).Milliseconds()
	if _, err := a.call("Start", true); err != nil {
		return err.Error()
	}
	appFoundLogf("Start pressed; watching both windows for up to %s", o.timeout)
	deadline := obs.Start.Add(o.timeout)
	var settledAt time.Time
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for ctx.Err() == nil && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		for _, c := range kids {
			select {
			case <-c.done:
				return fmt.Sprintf("app %s exited mid-run (%v); see %s", c.label, c.waitErr, c.logPath)
			default:
			}
		}
		sa, errA := a.state()
		sb, errB := b.state()
		if errA != nil || errB != nil {
			continue
		}
		_, statErr := os.Stat(adopted)
		obs.observe(sa, sb, statErr == nil, time.Now())
		if obs.settled() {
			if settledAt.IsZero() {
				settledAt = time.Now()
			}
			// The host's one adopted line comes after the founding: a short
			// look for it (secondary, never the pass signal).
			if hostAdoptedLine(obs.HostBanners) || time.Since(settledAt) > 2*time.Second {
				break
			}
		}
	}
	if ctx.Err() != nil {
		return ""
	}
	rep.TagsAfter = []appTag{readAppTag(pidA), readAppTag(pidB)}
	rep.Checks = appFoundVerdicts(obs, &rep.TagsAfter[0], &rep.TagsAfter[1])
	return ""
}

// hostAdoptedLine says the host's banner said its save's world was
// started here (autoFoundedText).
func hostAdoptedLine(banners []string) bool {
	return slices.ContainsFunc(banners, func(s string) bool { return strings.Contains(s, "wasn't on this computer") })
}

// waitAppState polls c's State every 250 ms until cond holds; it returns
// the last state read.
func waitAppState(ctx context.Context, c *appChild, wait time.Duration, cond func(gui.StateView) bool) (gui.StateView, error) {
	end := time.Now().Add(wait)
	var last gui.StateView
	var lastErr error
	for {
		v, err := c.state()
		if err == nil {
			last, lastErr = v, nil
			if cond(v) {
				return v, nil
			}
		} else {
			lastErr = err
		}
		if time.Now().After(end) || ctx.Err() != nil {
			if lastErr == nil {
				lastErr = fmt.Errorf("not within %s (phase %q)", wait, last.Phase)
			}
			return last, lastErr
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// appFoundFreePort is a port free on 127.0.0.1 now.
func appFoundFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// finishAppFound settles the outcome, writes the report and returns the
// exit code.
func finishAppFound(ctx context.Context, o appFoundOpts, rep *appFoundReport, cannot string) int {
	rep.Ended = time.Now()
	for _, c := range rep.Checks {
		if !c.Pass && !c.Skipped {
			rep.Failed = append(rep.Failed, c.Name)
		}
	}
	switch {
	case ctx.Err() != nil:
		rep.Outcome, rep.ExitCode = "interrupted", smokeExitCannot
	case cannot != "":
		rep.Outcome, rep.ExitCode = "cannot run: "+cannot, smokeExitCannot
	case len(rep.Failed) > 0:
		rep.Outcome, rep.ExitCode = "fail", smokeExitFail
	default:
		rep.Outcome, rep.ExitCode = "pass", smokeExitPass
	}
	for _, c := range rep.Checks {
		v := "PASS"
		switch {
		case c.Skipped:
			v = "SKIP"
		case !c.Pass:
			v = "FAIL"
		}
		fmt.Printf("  %s  %-24s %s\n", v, c.Name, c.Detail)
	}
	if rep.Obs != nil && len(rep.Obs.HostBanners) > 0 {
		appFoundLogf("host banner: %s", rep.Obs.HostBanners[len(rep.Obs.HostBanners)-1])
	}
	appFoundLogf("%s (%d checks, %d failed%s)", rep.Outcome, len(rep.Checks), len(rep.Failed), failedList(rep.Failed))
	if err := os.MkdirAll(filepath.Dir(o.out), 0o755); err == nil {
		if b, err := json.MarshalIndent(rep, "", "  "); err == nil {
			if err := os.WriteFile(o.out, append(b, '\n'), 0o644); err == nil {
				appFoundLogf("report: %s", o.out)
			} else {
				appFoundLogf("report: %v", err)
			}
		}
	}
	return rep.ExitCode
}
