package wwapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	fixtures "github.com/StephenSHorton/ww-multiplayer/mods/_fixtures"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
)

const waitFor = 5 * time.Second

// eventually polls cond until it holds or waitFor passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func state(rt *wwapi.Runtime, id string) string {
	s, ok := rt.Status(id)
	if !ok {
		return "(none)"
	}
	return s.String()
}

// runAsync starts rt.Run and returns its result channel.
func runAsync(rt *wwapi.Runtime, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	return done
}

func await(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(waitFor):
		t.Fatal("Run did not return")
	}
	return nil
}

// TestSupervisorFixtures runs every fixture: Init errors fail, a Run
// panic and a handler panic quarantine only their mod (with
// ModQuarantined on the core bus), dependents see their dependency's
// public API, and Stop goes in reverse load order.
func TestSupervisorFixtures(t *testing.T) {
	tr := &fixtures.Trace{}
	mods := fixtures.All(tr)
	log := &logRec{}
	var userPanics atomic.Int32
	rt := wwapi.New(mods, wwapi.Options{Log: log, Events: events.Options{
		OnPanic: func(events.Panic) { userPanics.Add(1) },
	}})
	quarantined, stopQ := events.Chan[wwapi.ModQuarantined](rt.Bus(), 8)
	defer stopQ()
	// pings is a core subscriber that is still live after the quarantine:
	// it shows when each ping has been dispatched.
	pings, stopP := events.Chan[fixtures.Ping](rt.Bus(), 8)
	defer stopP()

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(rt, ctx)

	q := <-quarantined
	if q.ID != "_run_panic" || q.Reason != "panic: fixture Run panic" || q.EventMeta().Origin != "core" {
		t.Errorf("ModQuarantined = %+v", q)
	}
	eventually(t, "the long runners to start", func() bool {
		steps := strings.Join(tr.Steps(), " ")
		return strings.Contains(steps, "run:_ok_base") && strings.Contains(steps, "run:_ok_dependent") && strings.Contains(steps, "run:_handler_panic")
	})

	if err := rt.Bus().Emit(fixtures.Ping{N: 7}); err != nil {
		t.Fatal(err)
	}
	q = <-quarantined
	if q.ID != "_handler_panic" || q.Reason != "panic in its handler for core.fixture_ping: fixture handler panic on ping 7" {
		t.Errorf("ModQuarantined = %+v", q)
	}
	eventually(t, "_handler_panic's Run to stop", func() bool {
		return strings.Contains(strings.Join(tr.Steps(), " "), "stop:_handler_panic")
	})
	for id, want := range map[string]string{
		"_init_error":    "FAILED(Init: fixture Init failed on purpose)",
		"_run_panic":     "QUARANTINED(panic: fixture Run panic)",
		"_handler_panic": "QUARANTINED(panic in its handler for core.fixture_ping: fixture handler panic on ping 7)",
		"_ok_base":       "LOADED",
		"_ok_dependent":  "LOADED",
		"_cycle_a":       "FAILED(dependency cycle: _cycle_a -> _cycle_b -> _cycle_a)",
	} {
		if got := state(rt, id); got != want {
			t.Errorf("%s = %s, want %s", id, got, want)
		}
	}
	// A second ping no longer reaches the quarantined handler: view.Close
	// removed it. Checked through the handler's own trace and the panic
	// counts, not the log (a second quarantine would log nothing).
	if p := <-pings; p.N != 7 {
		t.Fatalf("first ping = %d", p.N)
	}
	if err := rt.Bus().Emit(fixtures.Ping{N: 8}); err != nil {
		t.Fatal(err)
	}
	if p := <-pings; p.N != 8 {
		t.Fatalf("second ping = %d", p.N)
	}
	// On dispatch is asynchronous: give a handler that was wrongly left in
	// place time to run before checking that it did not.
	time.Sleep(50 * time.Millisecond)
	if got := filter(tr.Steps(), "handle:"); !reflect.DeepEqual(got, []string{"handle:_handler_panic:7"}) {
		t.Errorf("handler calls %v, want only ping 7", got)
	}
	if n := rt.Bus().Panics(); n != 1 {
		t.Errorf("bus Panics = %d, want 1", n)
	}
	if n := userPanics.Load(); n != 1 {
		t.Errorf("Options.Events.OnPanic called %d times, want 1", n)
	}

	cancel()
	if err := await(t, done); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if got := fixtures.SeenAPI(mods[slicesIndex(fixtures.IDs, "_ok_dependent")]); got != fixtures.BaseAPI {
		t.Errorf("_ok_dependent saw %v", got)
	}
	steps := tr.Steps()
	inits := filter(steps, "init:")
	if want := []string{"init:_handler_panic", "init:_init_error", "init:_ok_base", "init:_ok_dependent", "init:_run_panic"}; !reflect.DeepEqual(inits, want) {
		t.Errorf("init order %v", inits)
	}
	if stops := filter(steps, "stop:"); !reflect.DeepEqual(stops, []string{"stop:_handler_panic", "stop:_ok_dependent", "stop:_ok_base"}) {
		t.Errorf("stop order %v (want reverse load order)", stops)
	}
	if strings.Contains(strings.Join(steps, " "), "run:_init_error") {
		t.Error("a mod whose Init failed was run")
	}
	text := log.text()
	for _, want := range []string{
		"mod _init_error failed: Init: fixture Init failed on purpose",
		"mod _run_panic quarantined: panic: fixture Run panic",
		"mod _run_panic: stack:",
		"mod _handler_panic quarantined: panic in its handler",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
	if n := rt.Bus().Panics(); n != 1 {
		t.Errorf("bus Panics after Run = %d, want 1", n)
	}
	if err := rt.Run(context.Background()); !errors.Is(err, wwapi.ErrRunning) {
		t.Errorf("second Run = %v", err)
	}
}

func filter(steps []string, prefix string) []string {
	var out []string
	for _, s := range steps {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

func slicesIndex(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TestSupervisorRunError: an error from Run quarantines that mod only;
// a nil return just finishes it.
func TestSupervisorRunError(t *testing.T) {
	failing := mk(t, "failing", "1.0.0", "")
	failing.run = func(context.Context) error { return errors.New("disk full") }
	quick := mk(t, "quick", "1.0.0", "")
	waiting := mk(t, "waiting", "1.0.0", "")
	stopped := make(chan struct{})
	waiting.run = func(ctx context.Context) error { <-ctx.Done(); close(stopped); return nil }
	canceled := mk(t, "canceled", "1.0.0", "")
	canceled.run = func(ctx context.Context) error { <-ctx.Done(); return fmt.Errorf("wrapped: %w", ctx.Err()) }

	rt := wwapi.New([]wwapi.Mod{failing, quick, waiting, canceled}, wwapi.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(rt, ctx)
	eventually(t, "failing to be quarantined", func() bool { return state(rt, "failing") == "QUARANTINED(disk full)" })
	select {
	case <-stopped:
		t.Fatal("one mod's error stopped another")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"quick", "waiting", "canceled"} {
		if got := state(rt, id); got != "LOADED" {
			t.Errorf("%s = %s", id, got)
		}
	}
}

// TestSupervisorFatal: ErrFatal from the net provider ends the session
// and is returned as is; from another mod it only quarantines that mod.
func TestSupervisorFatal(t *testing.T) {
	fatalErr := fmt.Errorf("can't join: %w", wwapi.ErrFatal)
	other := mk(t, "other", "1.0.0", "")
	other.run = func(context.Context) error { return fatalErr }
	bystander := mk(t, "bystander", "1.0.0", "")
	bystanderStopped := make(chan struct{})
	bystander.run = func(ctx context.Context) error { <-ctx.Done(); close(bystanderStopped); return nil }
	net := netMod{mk(t, "mp", "1.0.0", `"provides":["net"],"dependencies":{"other":"*"}`)}
	release := make(chan struct{})
	net.run = func(context.Context) error { <-release; return fatalErr }

	log := &logRec{}
	rt := wwapi.New([]wwapi.Mod{net, other, bystander}, wwapi.Options{Log: log})
	done := runAsync(rt, context.Background())
	eventually(t, "other to be quarantined", func() bool { return strings.HasPrefix(state(rt, "other"), "QUARANTINED(can't join") })
	close(release)
	if err := await(t, done); err != fatalErr {
		t.Fatalf("Run = %v, want the provider's error as is", err)
	}
	<-bystanderStopped
	if got := state(rt, "mp"); got != "LOADED" {
		t.Errorf("mp = %s (a fatal end is not a quarantine)", got)
	}
	if strings.Contains(log.text(), "mod mp") {
		t.Errorf("the fatal error was logged:\n%s", log.text())
	}
}

// TestSupervisorInitFailures: an Init panic fails the mod; a mod that
// requires it fails too, and one that only optionally uses it loads.
func TestSupervisorInitFailures(t *testing.T) {
	boom := mk(t, "boom", "1.0.0", "")
	boom.init = func(*wwapi.Context) error { panic("init boom") }
	var subClosed bool
	leaky := mk(t, "leaky", "1.0.0", "")
	var ch <-chan fixtures.Ping
	leaky.init = func(c *wwapi.Context) error {
		ch, _ = events.Chan[fixtures.Ping](c.Events(), 1)
		return errors.New("no")
	}
	needs := mk(t, "needs", "1.0.0", `"dependencies":{"boom":"*"}`)
	ran := false
	needs.run = func(context.Context) error { ran = true; return nil }
	opt := mk(t, "opt", "1.0.0", `"optional_dependencies":{"boom":"*"}`)
	var hasBoom bool
	opt.init = func(c *wwapi.Context) error { hasBoom = c.Has("boom"); return nil }

	log := &logRec{}
	rt := wwapi.New([]wwapi.Mod{boom, leaky, needs, opt}, wwapi.Options{Log: log})
	rt.Init()
	rt.Init() // once only
	_, open := <-ch
	subClosed = !open
	for id, want := range map[string]string{
		"boom":  "FAILED(Init: panic: init boom)",
		"leaky": "FAILED(Init: no)",
		"needs": "FAILED(requires boom, which did not load)",
		"opt":   "LOADED",
	} {
		if got := state(rt, id); got != want {
			t.Errorf("%s = %s, want %s", id, got, want)
		}
	}
	if !subClosed {
		t.Error("a failed Init's subscriptions stayed open")
	}
	if hasBoom {
		t.Error("Has reported a failed mod")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rt.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("a failed mod ran")
	}
	if !strings.Contains(log.text(), "Init panic stack") {
		t.Errorf("no stack logged:\n%s", log.text())
	}
	if n := strings.Count(log.text(), "mod boom failed"); n != 1 {
		t.Errorf("boom logged %d times", n)
	}
}

// cEvent is an event in mod c's namespace.
type cEvent struct{ events.Base }

func (cEvent) EventName() string { return "mod.c.thing" }

type cfgSource map[string]string

func (c cfgSource) ModEnabled(id string) (bool, bool) {
	v, ok := c["enabled:"+id]
	return v == "true", ok
}

func (c cfgSource) ModSettings(id string) json.RawMessage {
	if v, ok := c["settings:"+id]; ok {
		return json.RawMessage(v)
	}
	return nil
}

func TestContext(t *testing.T) {
	type settings struct {
		Rate  int    `json:"rate"`
		Label string `json:"label"`
	}
	var (
		got      settings
		rate     int
		hasRate  bool
		hasNope  bool
		emptyDec settings
		ctxs     = map[string]*wwapi.Context{}
	)
	a := mk(t, "a", "1.0.0", "")
	a.init = func(c *wwapi.Context) error {
		ctxs["a"] = c
		c.Log().Log(0, "hello")
		if err := c.Config().Decode(&got); err != nil {
			return err
		}
		var err error
		if hasRate, err = c.Config().Get("rate", &rate); err != nil {
			return err
		}
		var s string
		hasNope, _ = c.Config().Get("nope", &s)
		return c.Events().Emit(fixtures.Ping{}) // core.* is not a's namespace
	}
	b := mk(t, "b", "1.0.0", `"dependencies":{"a":"*"}`)
	b.init = func(c *wwapi.Context) error {
		ctxs["b"] = c
		emptyDec = settings{Rate: 3}
		return c.Config().Decode(&emptyDec)
	}
	withAPI := apiMod{mk(t, "c", "1.0.0", ""), 42}
	withAPI.init = func(c *wwapi.Context) error { ctxs["c"] = c; return nil }
	gated := mk(t, "gated", "1.0.0", "")
	cfg := cfgSource{"settings:a": `{"rate":30,"label":"x"}`, "enabled:gated": "false"}
	log := &logRec{}
	root := t.TempDir()
	rt := wwapi.New([]wwapi.Mod{a, b, withAPI, gated}, wwapi.Options{
		Log: log, Config: cfg, DataRoot: root,
		Resolve: wwapi.ResolveOptions{Features: wwapi.KnownFeatures("face")},
	})
	rt.Init()

	if got := state(rt, "a"); !strings.HasPrefix(got, "FAILED(Init: events: name outside") {
		t.Errorf("a emitting core.* = %s", got)
	}
	if got != (settings{30, "x"}) || !hasRate || rate != 30 || hasNope {
		t.Errorf("config: %+v %v %d %v", got, hasRate, rate, hasNope)
	}
	if !strings.Contains(log.text(), "0 [a] hello") {
		t.Errorf("log: %s", log.text())
	}
	if got := state(rt, "gated"); got != "DISABLED" {
		t.Errorf("gated = %s (ConfigSource.ModEnabled ignored)", got)
	}
	if got := state(rt, "b"); got != "FAILED(requires a, which did not load)" {
		t.Errorf("b = %s", got)
	}
	if emptyDec.Rate != 0 && ctxs["b"] != nil {
		t.Errorf("b's Init ran although a failed")
	}
	c := ctxs["c"]
	if c.ID() != "c" || c.Manifest().Version.String() != "1.0.0" || c.DataDir() != filepath.Join(root, "c") {
		t.Errorf("c: %s %s %s", c.ID(), c.Manifest().Version, c.DataDir())
	}
	if !c.Features().Has("face") || c.Events().Origin() != "c" {
		t.Errorf("c: features %v origin %s", c.Features(), c.Events().Origin())
	}
	if v, ok := c.Mod("c"); !ok || v != 42 {
		t.Errorf("Mod(c) = %v %v", v, ok)
	}
	for _, id := range []string{"a", "gated", "nobody"} {
		if v, ok := c.Mod(id); ok || c.Has(id) {
			t.Errorf("Mod(%s) = %v %v, Has = %v", id, v, ok, c.Has(id))
		}
	}
	if raw := c.Config().Raw(); raw != nil {
		t.Errorf("c's settings = %s", raw)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rt.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Events().Emit(cEvent{}); !errors.Is(err, events.ErrClosed) {
		t.Error("the bus is still open after Run")
	}
}

func TestContextConfigErrors(t *testing.T) {
	m := mk(t, "m", "1.0.0", "")
	var getErr, decErr error
	m.init = func(c *wwapi.Context) error {
		var v int
		_, getErr = c.Config().Get("x", &v)
		decErr = c.Config().Decode(&v)
		return nil
	}
	rt := wwapi.New([]wwapi.Mod{m}, wwapi.Options{Config: cfgSource{"settings:m": `[1]`}})
	rt.Init()
	if getErr == nil || decErr == nil {
		t.Fatalf("Get %v, Decode %v", getErr, decErr)
	}
	if rt.Plan().Entries[0].Manifest.ID != "m" {
		t.Fatal("Plan")
	}
	if infos := rt.Mods(); len(infos) != 1 || infos[0].Status != wwapi.Loaded {
		t.Fatalf("Mods = %+v", infos)
	}
	if _, ok := rt.Status("nobody"); ok {
		t.Fatal("Status of an unknown id")
	}
}

// TestSupervisorQuarantineEndsSubscriptions: a quarantined mod's channel
// subscriptions are closed and its Run context is cancelled, and the
// user's OnPanic hook is still called.
func TestSupervisorQuarantineEndsSubscriptions(t *testing.T) {
	var sub <-chan fixtures.Ping
	m := mk(t, "m", "1.0.0", "")
	runEnded := make(chan error, 1)
	m.init = func(c *wwapi.Context) error {
		sub, _ = events.Chan[fixtures.Ping](c.Events(), 4)
		events.On(c.Events(), func(fixtures.Ping) { panic("handler") })
		return nil
	}
	running := make(chan struct{})
	m.run = func(ctx context.Context) error { close(running); <-ctx.Done(); runEnded <- ctx.Err(); return nil }
	hooked := make(chan events.Panic, 1)
	rt := wwapi.New([]wwapi.Mod{m}, wwapi.Options{Events: events.Options{OnPanic: func(p events.Panic) { hooked <- p }}})
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(rt, ctx)
	<-running
	if err := rt.Bus().Emit(fixtures.Ping{N: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-hooked:
		if p.Owner != "m" {
			t.Errorf("OnPanic owner %s", p.Owner)
		}
	case <-time.After(waitFor):
		t.Fatal("OnPanic not called")
	}
	select {
	case err := <-runEnded:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run ctx err %v", err)
		}
	case <-time.After(waitFor):
		t.Fatal("quarantine did not cancel Run")
	}
	eventually(t, "the channel to close", func() bool {
		select {
		case _, ok := <-sub:
			return !ok
		default:
			return false
		}
	})
	if got := state(rt, "m"); got != "QUARANTINED(panic in its handler for core.fixture_ping: handler)" {
		t.Errorf("m = %s", got)
	}
	cancel()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
}

// TestSupervisorCorePanic: a panic in a core handler is logged, and no
// mod is quarantined.
func TestSupervisorCorePanic(t *testing.T) {
	log := &logRec{}
	m := mk(t, "m", "1.0.0", "")
	rt := wwapi.New([]wwapi.Mod{m}, wwapi.Options{Log: log})
	hit := make(chan struct{})
	events.On(rt.Bus(), func(fixtures.Ping) { defer close(hit); panic("core boom") })
	_ = rt.Bus().Emit(fixtures.Ping{})
	<-hit
	eventually(t, "the log line", func() bool {
		return strings.Contains(log.text(), "panic in a core handler for core.fixture_ping: core boom")
	})
	if got := state(rt, "m"); got != "LOADED" {
		t.Errorf("m = %s", got)
	}
}

// TestSupervisorShutdownErrorStaysLoaded: a mod that returns a plain
// error once its context has ended (a net provider whose closed sockets
// report "use of closed network connection") is stopping, not failing.
// That holds on a normal shutdown and when the net provider's ErrFatal
// ends the session; only a mod still running when it errors, or one that
// panics, is quarantined.
func TestSupervisorShutdownErrorStaysLoaded(t *testing.T) {
	closing := func(t *testing.T, id string) *tmod {
		m := mk(t, id, "1.0.0", "")
		m.run = func(ctx context.Context) error {
			<-ctx.Done()
			return errors.New("use of closed network connection")
		}
		return m
	}

	t.Run("ctx ends", func(t *testing.T) {
		log := &logRec{}
		rt := wwapi.New([]wwapi.Mod{closing(t, "aa")}, wwapi.Options{Log: log})
		quarantined, stopQ := events.Chan[wwapi.ModQuarantined](rt.Bus(), 8)
		defer stopQ()
		ctx, cancel := context.WithCancel(context.Background())
		done := runAsync(rt, ctx)
		cancel()
		if err := await(t, done); err != nil {
			t.Fatalf("Run = %v", err)
		}
		if got := state(rt, "aa"); got != "LOADED" {
			t.Errorf("aa = %s, want LOADED", got)
		}
		for q := range quarantined { // closed with the bus
			t.Errorf("ModQuarantined %+v on shutdown", q)
		}
		text := log.text()
		if strings.Contains(text, "quarantined") {
			t.Errorf("shutdown logged a quarantine:\n%s", text)
		}
		if want := fmt.Sprintf("%d mod aa stopped: use of closed network connection", report.Info); !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	})

	t.Run("net provider ErrFatal", func(t *testing.T) {
		net := netMod{mk(t, "net", "1.0.0", `"provides":["net"]`)}
		fatal := fmt.Errorf("lost the server: %w", wwapi.ErrFatal)
		started := make(chan struct{})
		net.run = func(context.Context) error { <-started; return fatal }
		aa := closing(t, "aa")
		inner := aa.run
		aa.run = func(ctx context.Context) error { close(started); return inner(ctx) }
		rt := wwapi.New([]wwapi.Mod{net, aa}, wwapi.Options{})
		if err := await(t, runAsync(rt, context.Background())); !errors.Is(err, fatal) {
			t.Fatalf("Run = %v, want the fatal error", err)
		}
		if got := state(rt, "aa"); got != "LOADED" {
			t.Errorf("aa = %s, want LOADED", got)
		}
	})

	t.Run("error while running", func(t *testing.T) {
		aa := mk(t, "aa", "1.0.0", "")
		aa.run = func(context.Context) error { return errors.New("broke") }
		rt := wwapi.New([]wwapi.Mod{aa}, wwapi.Options{})
		ctx, cancel := context.WithCancel(context.Background())
		done := runAsync(rt, ctx)
		eventually(t, "aa to be quarantined", func() bool { return state(rt, "aa") == "QUARANTINED(broke)" })
		cancel()
		if err := await(t, done); err != nil {
			t.Fatalf("Run = %v", err)
		}
	})

	t.Run("panic on shutdown", func(t *testing.T) {
		aa := mk(t, "aa", "1.0.0", "")
		aa.run = func(ctx context.Context) error { <-ctx.Done(); panic("late") }
		rt := wwapi.New([]wwapi.Mod{aa}, wwapi.Options{})
		ctx, cancel := context.WithCancel(context.Background())
		done := runAsync(rt, ctx)
		cancel()
		if err := await(t, done); err != nil {
			t.Fatalf("Run = %v", err)
		}
		if got := state(rt, "aa"); got != "QUARANTINED(panic: late)" {
			t.Errorf("aa = %s, want QUARANTINED(panic: late)", got)
		}
	})
}

// TestSupervisorInitThenRunConcurrently: Run called while Init is still
// running on another goroutine waits for it, so it never starts a mod
// whose Init has not finished or is about to fail.
func TestSupervisorInitThenRunConcurrently(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	slow := mk(t, "slow", "1.0.0", "")
	slow.init = func(*wwapi.Context) error {
		close(entered)
		<-release
		return errors.New("late failure")
	}
	var slowRan atomic.Bool
	slow.run = func(context.Context) error { slowRan.Store(true); return nil }
	// after loads after slow but does not need it.
	after := mk(t, "after", "1.0.0", `"optional_dependencies":{"slow":"*"}`)
	var afterInited, afterRanEarly, afterRan atomic.Bool
	after.init = func(*wwapi.Context) error { afterInited.Store(true); return nil }
	after.run = func(ctx context.Context) error {
		if !afterInited.Load() {
			afterRanEarly.Store(true)
		}
		afterRan.Store(true)
		<-ctx.Done()
		return nil
	}
	rt := wwapi.New([]wwapi.Mod{slow, after}, wwapi.Options{})

	initDone := make(chan struct{})
	go func() { rt.Init(); close(initDone) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(rt, ctx)
	time.Sleep(20 * time.Millisecond) // a Run that did not wait would start the mods now
	if slowRan.Load() || afterRan.Load() {
		t.Fatal("Run started mods while Init was still running")
	}
	close(release)
	<-initDone
	eventually(t, "after to run", afterRan.Load)
	cancel()
	if err := await(t, done); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if slowRan.Load() {
		t.Error("a mod whose Init failed was run")
	}
	if afterRanEarly.Load() {
		t.Error("after ran before its Init")
	}
	if got := state(rt, "slow"); got != "FAILED(Init: late failure)" {
		t.Errorf("slow = %s", got)
	}
	if got := state(rt, "after"); got != "LOADED" {
		t.Errorf("after = %s", got)
	}
}
