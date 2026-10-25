package gui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// SetCoopShares records what the host asked (session.CoopShareEngine).
func (e *coopEngine) SetCoopShares(on []string) error {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	if e.sharesErr != nil {
		return e.sharesErr
	}
	e.shares = append(e.shares, slices.Clone(on))
	return nil
}

func ruleByID(t *testing.T, v *CoopRulesView, id string) CoopRuleView {
	t.Helper()
	for _, r := range v.Rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no rule %q", id)
	return CoopRuleView{}
}

// TestRulesViewHost: the host's rules are what its hub allows; every
// feature but the World is editable, Dry run is set at Start, Merging
// follows the lobby, and a rule a bound peer isn't granted names it.
func TestRulesViewHost(t *testing.T) {
	st := session.State{
		Lobby: &session.Lobby{Host: true, Settings: session.LobbySettings{Merge: true}},
		Live: session.Live{CoopHub: &session.CoopHubStatus{Features: "image|story|switches|keys|life", Peers: []session.CoopPeer{
			{Name: "Stephen", Host: true, State: "bound", Features: "image|story|switches|keys|life"},
			{Name: "Blake", State: "bound", Features: "image|story"},
			{Name: "Sam", State: "unbound", Features: "image"},
		}}},
	}
	v := rulesView(st, hideIPs)
	if v == nil || !v.Host || !v.Edit || v.Note != "" {
		t.Fatalf("host rules %+v", v)
	}
	if r := ruleByID(t, v, "image"); !r.On || r.Edit || r.Why == "" {
		t.Errorf("World %+v: want on, read-only with why", r)
	}
	if r := ruleByID(t, v, "story"); !r.On || !r.Edit || len(r.Missing) != 0 {
		t.Errorf("Story %+v", r)
	}
	if r := ruleByID(t, v, "switches"); !r.On || !r.Edit || !slices.Equal(r.Missing, []string{"Blake"}) || !strings.Contains(r.MissingWhy, "stage tables") {
		t.Errorf("Switches %+v: want Blake missing, for his tables (the host and the unbound Sam are not listed)", r)
	}
	if r := ruleByID(t, v, "life"); !slices.Equal(r.Missing, []string{"Blake"}) {
		t.Errorf("Hearts %+v: want Blake missing", r)
	}
	if r := ruleByID(t, v, "inventory"); r.On || !r.Edit || len(r.Missing) != 0 {
		t.Errorf("Items %+v: want off, editable", r)
	}
	if r := ruleByID(t, v, "merge"); !r.On || !r.Edit {
		t.Errorf("Merging %+v: want the lobby's merge, editable", r)
	}
	if r := ruleByID(t, v, "dry-run"); r.On || r.Edit || r.Why == "" {
		t.Errorf("Dry run %+v: want read-only with why", r)
	}
	st.CoopHub.Peers[1].Features = "image"
	if r := ruleByID(t, rulesView(st, hideIPs), "story"); !slices.Equal(r.Missing, []string{"Blake"}) || r.MissingWhy != "their game runs a dry run" {
		t.Errorf("Story for a dry-run Blake: %+v", r)
	}
	st.CoopHub.Features = "image|keys"
	if v := rulesView(st, hideIPs); v.Note != sharesNote {
		t.Errorf("keys without switches: note %q", v.Note)
	}
	// Before the hub serves the world the host can't edit yet.
	st.CoopHub = nil
	st.CoopStatus = []session.CoopStatus{{Shares: []string{"image", "story"}}}
	if v := rulesView(st, hideIPs); v.Edit || ruleByID(t, v, "story").Edit {
		t.Errorf("no hub yet: %+v", v)
	}
}

// TestRulesViewJoiner: a joiner sees what its game is granted, read-only,
// with no host-side reasons; a dry run is granted nothing.
func TestRulesViewJoiner(t *testing.T) {
	st := session.State{
		Lobby: &session.Lobby{Settings: session.LobbySettings{DryRun: false}},
		Live:  session.Live{CoopStatus: []session.CoopStatus{{Shares: []string{"image", "story", "inventory"}}}},
	}
	v := rulesView(st, hideIPs)
	if v.Host || v.Edit {
		t.Fatalf("joiner rules editable: %+v", v)
	}
	for _, r := range v.Rows {
		want := r.ID == "image" || r.ID == "story" || r.ID == "inventory"
		if r.On != want || r.Edit || r.Why != "" {
			t.Errorf("joiner %s: %+v", r.ID, r)
		}
	}
	st.CoopStatus[0].DryRun = true
	for _, r := range rulesView(st, hideIPs).Rows {
		if r.On {
			t.Errorf("dry run: %s on", r.ID)
		}
	}
	if rulesView(session.State{Lobby: &session.Lobby{}}, hideIPs) != nil {
		t.Error("no pipeline, no hub: want no rules")
	}
}

// TestSetCoopShares: the host's change goes to the engine without the
// World, and what is off is saved for the next hosted session (Live
// switches stays the lobby's Start choice); a name that is no rule, or
// one co-op can't change while it runs, never reaches the engine.
func TestSetCoopShares(t *testing.T) {
	b, e, path := newCoopBridge(t)
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	if err := b.SetCoopShares([]string{"image", "story", "keys", "live-switches"}); err != nil {
		t.Fatal(err)
	}
	e.cmu.Lock()
	got := slices.Clone(e.shares)
	e.cmu.Unlock()
	if len(got) != 1 || !slices.Equal(got[0], []string{"story", "keys", "live-switches"}) {
		t.Fatalf("engine got %q", got)
	}
	const off = "inventory,switches,puzzle,combat,life,magic"
	if cfg, _ := config.Load(path); cfg.Coop.SharesOff != off {
		t.Errorf("saved SharesOff %q, want %q", cfg.Coop.SharesOff, off)
	}
	if o := b.coopOptions(); o.SharesOff != off {
		t.Errorf("the next co-op start reads %q", o.SharesOff)
	}
	for _, bad := range [][]string{{"merge"}, {"teleport"}, {"story", "story"}} {
		if err := b.SetCoopShares(bad); err == nil {
			t.Errorf("SetCoopShares(%q) took it", bad)
		}
	}
	e.sharesErr = errors.New("only the host changes what co-op shares")
	if err := b.SetCoopShares([]string{"story"}); err == nil {
		t.Error("the engine's refusal was lost")
	}
	if cfg, _ := config.Load(path); cfg.Coop.SharesOff != off {
		t.Errorf("a refused change was saved: %q", cfg.Coop.SharesOff)
	}
	e.cmu.Lock()
	n := len(e.shares)
	e.cmu.Unlock()
	if n != 1 {
		t.Errorf("%d engine calls, want 1 (refused names never reach it)", n)
	}
}

// TestMemberCoop: the host's rows come from its hub's peers; a joiner
// knows only its one partner's place; Warp to is the one other player's.
func TestMemberCoop(t *testing.T) {
	st := session.State{Coop: session.CoopOptions{On: true}, Live: session.Live{
		CoopHub:    &session.CoopHubStatus{Peers: []session.CoopPeer{{Name: "Blake", State: "bound", Stage: "M_NewD2", Room: 3}, {Name: "Sam", State: ""}}},
		CoopStatus: []session.CoopStatus{{PartnerStage: "M_NewD2", PartnerRoom: 3}}}}
	if chip, warp := memberCoop(st, "Blake", false, 1); chip != "Bound · M_NewD2 room 3" || !warp {
		t.Errorf("host's Blake: %q, %v", chip, warp)
	}
	if chip, warp := memberCoop(st, "Sam", false, 2); chip != "" || warp {
		t.Errorf("host's Sam with two others: %q, %v", chip, warp)
	}
	if _, warp := memberCoop(st, "Link", true, 1); warp {
		t.Error("Warp to on your own row")
	}
	st.CoopHub = nil
	if chip, warp := memberCoop(st, "Blake", false, 1); chip != "M_NewD2 room 3" || !warp {
		t.Errorf("joiner's partner: %q, %v", chip, warp)
	}
	// At a stage start the room isn't known yet (-1): the stage alone.
	st.CoopStatus[0].PartnerRoom = -1
	if chip, _ := memberCoop(st, "Blake", false, 1); chip != "M_NewD2" {
		t.Errorf("joiner's partner at a stage start: %q", chip)
	}
	st.CoopHub = &session.CoopHubStatus{Peers: []session.CoopPeer{{Name: "Blake", State: "bound", Stage: "M_NewD2", Room: -1}}}
	if chip, _ := memberCoop(st, "Blake", false, 1); chip != "Bound · M_NewD2" {
		t.Errorf("host's Blake at a stage start: %q", chip)
	}
	st.Coop.On = false
	if chip, warp := memberCoop(st, "Blake", false, 1); chip != "" || warp {
		t.Errorf("co-op off: %q, %v", chip, warp)
	}
}

// TestRoomView: whole hearts and the quarters past them.
func TestRoomView(t *testing.T) {
	if roomView(nil) != nil {
		t.Error("nil room")
	}
	v := roomView(&session.CoopRoom{Items: []session.CoopRoomItem{{Slot: "bow", Name: "the bow", Have: true}}, Hearts: true, MaxLife: 22, ChartsTotal: 49})
	if v.MaxHearts != 5 || v.MaxQuarters != 2 || len(v.Items) != 1 || !v.Items[0].Have || v.ChartsTotal != 49 || v.Songs == nil {
		t.Errorf("room view %+v", v)
	}
}
