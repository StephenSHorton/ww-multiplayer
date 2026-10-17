package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

// unbindFake is a fakeLobby whose co-op side can unbind the loaded file.
type unbindFake struct {
	*fakeLobby
	unbinds int
	world   string
}

func (e *unbindFake) UnbindFile(world string) error {
	e.unbinds++
	e.world = world
	return nil
}

// TestCoopAdvice: advice is kept in State, the newest of a kind per
// player, until dismissed.
func TestCoopAdvice(t *testing.T) {
	e := newFakeLobby()
	c := New(e, Options{})
	if _, err := c.JoinLobby(context.Background(), JoinOptions{Name: "B", Addr: "x"}); err != nil {
		t.Fatal(err)
	}
	defer c.Leave()
	out := e.out(t)
	out.Advise(Advice{Kind: AdviceNeedsMerge, Player: "B", Text: "one"})
	out.Advise(Advice{Kind: AdviceFarBehind, Player: "B", Text: "two"})
	out.Advise(Advice{Kind: AdviceNeedsMerge, Player: "B", Text: "three"})
	st := c.Snapshot()
	if len(st.Advice) != 2 || st.Advice[0].Kind != AdviceFarBehind || st.Advice[1].Text != "three" || st.Advice[1].ID != "a3" {
		t.Fatalf("advice %+v", st.Advice)
	}
	if err := c.DismissAdvice("a1"); !errors.Is(err, ErrNoAdvice) {
		t.Errorf("dismissing replaced advice = %v", err)
	}
	if err := c.DismissAdvice(st.Advice[0].ID); err != nil {
		t.Fatal(err)
	}
	if st := c.Snapshot(); len(st.Advice) != 1 || st.Advice[0].Text != "three" {
		t.Fatalf("after dismiss %+v", st.Advice)
	}
}

// TestCoopMergeRequest: a joiner's merge request reaches the host's
// State once per joiner; Allow sets merge in the host's settings (the
// only setting that may change after Start) and closes every merge
// request; Dismiss changes nothing. A joiner's RequestMerge goes on the
// wire.
func TestCoopMergeRequest(t *testing.T) {
	e := newFakeLobby()
	c := New(e, Options{})
	if _, err := c.HostLobby(context.Background(), HostOptions{Name: "Me"}); err != nil {
		t.Fatal(err)
	}
	defer c.Leave()
	out := e.out(t)
	l := lobbyView(LobbyPhaseInGame, true)
	l.Settings = LobbySettings{Coop: "0123456789abcdef", DryRun: true}
	out.LobbyState(l)
	out.LobbyRequest("merge", "Alice")
	out.LobbyRequest("merge", "Alice")
	out.LobbyRequest("merge", "Bob")
	st := c.Snapshot()
	if len(st.Requests) != 2 || st.Requests[0] != (Request{ID: "r1", Kind: "merge", From: "Alice"}) {
		t.Fatalf("requests %+v", st.Requests)
	}
	if err := c.DismissRequest("r2"); err != nil {
		t.Fatal(err)
	}
	if err := c.DismissRequest("r2"); !errors.Is(err, ErrNoRequest) {
		t.Errorf("second dismiss = %v", err)
	}
	if len(e.commands()) != 0 {
		t.Fatalf("dismiss sent %+v", e.commands())
	}
	out.LobbyRequest("merge", "Bob")
	if err := c.AllowMerge("r1"); err != nil {
		t.Fatal(err)
	}
	cmds := e.commands()
	if len(cmds) != 1 || cmds[0].Kind != LobbySetSettings || !cmds[0].Settings.Merge || cmds[0].Settings.Coop != "0123456789abcdef" || !cmds[0].Settings.DryRun {
		t.Fatalf("allow sent %+v", cmds)
	}
	if st := c.Snapshot(); len(st.Requests) != 0 {
		t.Fatalf("requests after allow %+v", st.Requests)
	}
	if err := c.AllowMerge("r1"); !errors.Is(err, ErrNoRequest) {
		t.Errorf("allow again = %v", err)
	}

	// A joiner asks.
	ej := newFakeLobby()
	cj := New(ej, Options{})
	if _, err := cj.JoinLobby(context.Background(), JoinOptions{Name: "B", Addr: "x"}); err != nil {
		t.Fatal(err)
	}
	defer cj.Leave()
	ej.out(t).LobbyState(lobbyView(LobbyPhaseInGame, false))
	if err := cj.RequestMerge(); err != nil {
		t.Fatal(err)
	}
	if cmds := ej.commands(); len(cmds) != 1 || cmds[0].Kind != LobbyRequestMerge {
		t.Fatalf("joiner sent %+v", cmds)
	}
	if err := cj.AllowMerge("r1"); !errors.Is(err, ErrNoRequest) {
		t.Errorf("joiner allow = %v", err)
	}
}

// TestCoopAutoYes: "Answer yes to co-op questions" answers the open
// question at once and every later one as it is asked; a new session
// starts with it off.
func TestCoopAutoYes(t *testing.T) {
	e := newFakeLobby()
	c := New(e, Options{})
	if err := c.SetAutoYes(true); !errors.Is(err, ErrNoSession) {
		t.Errorf("SetAutoYes with no session = %v", err)
	}
	if _, err := c.HostLobby(context.Background(), HostOptions{Name: "Me"}); err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan bool, 1)
	go func() { first <- out.Ask(ctx, Question{Kind: QuestionBind, Title: "Bind?"}) }()
	waitFor(t, func() bool { return len(c.Snapshot().Questions) == 1 })
	if err := c.SetAutoYes(true); err != nil {
		t.Fatal(err)
	}
	if !<-first {
		t.Error("the open question was answered no")
	}
	if !out.Ask(ctx, Question{Kind: QuestionScrub, Title: "Scrub?"}) {
		t.Error("a later question was answered no")
	}
	if st := c.Snapshot(); !st.AutoYes || len(st.Questions) != 0 {
		t.Fatalf("state %+v", st)
	}
	c.Leave()
	if _, err := c.HostLobby(context.Background(), HostOptions{Name: "Me"}); err != nil {
		t.Fatal(err)
	}
	defer c.Leave()
	e.out(t)
	if c.Snapshot().AutoYes {
		t.Error("a new session starts with auto-yes on")
	}
}

// TestCoopFoundingAndOptions: the engine's founding progress and co-op
// options land in State; UnbindFile reaches an engine that can.
func TestCoopFoundingAndOptions(t *testing.T) {
	e := &unbindFake{fakeLobby: newFakeLobby()}
	c := New(e, Options{})
	if err := c.UnbindFile(""); !errors.Is(err, ErrNoSession) {
		t.Errorf("UnbindFile with no session = %v", err)
	}
	if _, err := c.HostLobby(context.Background(), HostOptions{Name: "Me"}); err != nil {
		t.Fatal(err)
	}
	defer c.Leave()
	out := e.out(t).(CoopOutput)
	out.Founding(Founding{Phase: FoundingBlocked, Blocker: BlockBound, TagWorld: "0868da446caca9bf"})
	out.Coop(CoopOptions{On: true, World: CoopWorldNew, DryRun: true})
	st := c.Snapshot()
	if st.Founding == nil || st.Founding.Phase != FoundingBlocked || st.Founding.TagWorld != "0868da446caca9bf" || !st.Coop.On || !st.Coop.DryRun {
		t.Fatalf("state %+v", st)
	}
	st.Founding.Phase = FoundingFailed // a snapshot is a copy
	if c.Snapshot().Founding.Phase != FoundingBlocked {
		t.Error("the snapshot shares Founding")
	}
	if err := c.UnbindFile("0868da446caca9bf"); err != nil || e.unbinds != 1 || e.world != "0868da446caca9bf" {
		t.Fatalf("UnbindFile = %v, %d, %q", err, e.unbinds, e.world)
	}
	// An engine without the file side refuses.
	c2 := New(newFakeLobby(), Options{})
	if _, err := c2.Host(context.Background(), HostOptions{}); err != nil {
		t.Fatal(err)
	}
	defer c2.Leave()
	if err := c2.UnbindFile(""); !errors.Is(err, ErrNoCoop) {
		t.Errorf("UnbindFile without the file side = %v", err)
	}
}

// waitFor polls ok until it holds, failing after 5 s.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
