package gui

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// Start → Dolphin → in game, the window's side (App PR 8, design §2.6 and
// §2.7): the host's Start (with "Start anyway"), the per-player "I'll
// start Dolphin myself" setting, the Dolphin chips, the dialog for a
// Dolphin that runs the wrong game or an unpatched disc, the "Dolphin
// closed" banner, and the Session screen's players and minimap. As with
// the lobby (lobby.go), Go decides them from the controller's State and
// the frontend only draws them.

// Dialog kinds and actions of the game side.
const (
	dlgDolphin    = "dolphin"     // the Dolphin runs another game or an unpatched disc
	dlgLaunchFail = "launch_fail" // Dolphin couldn't be started
	actSetup      = "setup"       // open Setup
	actLaunch     = "launch"      // Bridge.LaunchDolphin
)

// sameAreaUnits is how near (in world units, on the ground plane) a
// remote Link counts as "same area" on the Session screen: about a big
// island's width. The pose carries no stage (without co-op), so nearness
// is the hint (design §2.7: "same area" from the pose position).
const sameAreaUnits = 3000

// GameDolphinView is this player's Dolphin (State.Dolphin) for the window.
type GameDolphinView struct {
	State     string `json:"state"` // session.DolphinState; "" before Start
	Label     string `json:"label"`
	PID       uint32 `json:"pid"`
	Launched  bool   `json:"launched"`
	Manual    bool   `json:"manual"`
	Pipelines bool   `json:"pipelines"`
	Err       string `json:"err,omitempty"`
}

// MapView is the Session screen's minimap (the TUI's, design §2.7): the
// other players' ground-plane offsets from this player, +X right and +Z
// down as the TUI draws them. SelfOK is false until this player's
// position is known.
type MapView struct {
	SelfOK  bool         `json:"selfOk"`
	Players []MapDotView `json:"players"`
	Color   string       `json:"color"` // this player's color, for the center dot
	Units   int          `json:"units"` // sameAreaUnits, for the map's ring
}

// MapDotView is one remote Link on the minimap.
type MapDotView struct {
	Name  string  `json:"name"`
	Color string  `json:"color"`
	DX    float64 `json:"dx"`
	DZ    float64 `json:"dz"`
}

// dolphinLabels are the Dolphin chip's words for each state.
var dolphinLabels = map[string]string{
	"":                               "",
	string(session.DolphinNone):      "Not running",
	string(session.DolphinLaunching): "Launching…",
	string(session.DolphinTitle):     "Title screen",
	string(session.DolphinInGame):    "In game",
	string(session.DolphinNoGame):    "No game loaded",
	string(session.DolphinWrongGame): "Wrong game",
	string(session.DolphinUnpatched): "Not patched",
}

// dolphinLabel is the chip's text for a Dolphin state, the state itself
// for one this build doesn't know (a newer build's).
func dolphinLabel(state string) string {
	if l, ok := dolphinLabels[state]; ok {
		return l
	}
	return state
}

func dolphinView(d session.DolphinInfo, mk masker) GameDolphinView {
	return GameDolphinView{State: string(d.State), Label: dolphinLabel(string(d.State)), PID: d.PID,
		Launched: d.Launched, Manual: d.Manual, Pipelines: d.Pipelines, Err: mk.text(d.Err)}
}

// canStart says the host's Start button works: we host the running
// lobby, and it is still gathering.
func canStart(l *session.Lobby, running bool) bool {
	return running && l != nil && l.Host && !l.Legacy && l.Phase == session.LobbyPhaseLobby
}

// gameDialog is the dialog this player's Dolphin calls for, or nil: it
// runs another game or an unpatched Wind Waker (design §2.9), or it
// couldn't be started. Live: it closes once the Dolphin is right.
func gameDialog(st session.State, running bool, mk masker) *DialogView {
	if !running || st.Lobby == nil || st.Lobby.Legacy {
		return nil
	}
	d := st.Dolphin
	key := func(kind string) string { return fmt.Sprintf("%d:%s:%s", st.Session, kind, d.State) }
	iso := d.ISO
	if iso == "" {
		iso = "your patched disc"
	}
	switch d.State {
	case session.DolphinWrongGame, session.DolphinUnpatched:
		what := "a different game"
		if d.State == session.DolphinUnpatched {
			what = "Wind Waker without this version's multiplayer mod"
		}
		return &DialogView{Key: key(dlgDolphin), Kind: dlgDolphin, Live: true, Title: "Wrong disc in Dolphin",
			Text:    fmt.Sprintf("Dolphin is running %s. Close that game and boot %s: the session starts playing as soon as Dolphin runs it.", what, iso),
			Detail:  "If the patched disc was made by another version of this app, patch your disc again in Setup. If you loaded a save state, it may have been taken on another version: boot the disc from Dolphin's game list instead, and capture a new save state once you are in the game.",
			Actions: []DialogAction{{ID: actOK, Label: "OK", Primary: true}, {ID: actSetup, Label: "Open Setup"}}}
	}
	if d.Err != "" && d.PID == 0 {
		// One dialog per attempt: a Try again that fails again opens anew.
		return &DialogView{Key: fmt.Sprintf("%d:%s:%d", st.Session, dlgLaunchFail, d.LaunchAttempt), Kind: dlgLaunchFail, Live: true, Title: "Couldn't start Dolphin",
			Text:    "The app couldn't start Dolphin with your patched disc. Check Dolphin and the disc in Setup, or start Dolphin yourself.",
			Detail:  mk.text(d.Err),
			Actions: []DialogAction{{ID: actLaunch, Label: "Try again", Primary: true}, {ID: actSetup, Label: "Open Setup"}, {ID: actOK, Label: "Close"}}}
	}
	return nil
}

// dolphinBanner is the "Dolphin closed" banner (design §2.7), or nil.
// After a failed launch it offers Try again, so a closed dialog still
// leaves the player a way to retry.
func dolphinBanner(st session.State, running bool) *BannerView {
	d := st.Dolphin
	if !running || st.Lobby == nil || st.Lobby.Legacy || d.State != session.DolphinNone || d.PID != 0 {
		return nil
	}
	if d.Err != "" && !d.Manual {
		return &BannerView{Kind: "dolphin", Title: "Couldn't start Dolphin.", Action: actLaunch, ActionLabel: "Try again",
			Text: "Check Dolphin and your patched disc in Setup, then try again, or Leave."}
	}
	if d.Manual {
		return &BannerView{Kind: "dolphin", Title: "Waiting for Dolphin…",
			Text: "Start Dolphin with your patched Wind Waker disc: the session plays as soon as it runs."}
	}
	return &BannerView{Kind: "dolphin", Title: "Dolphin closed.", Action: actLaunch, ActionLabel: "Launch Dolphin",
		Text: "Start it again to keep playing, or Leave."}
}

// mapView is the minimap of st.
func mapView(st session.State, self string, mk masker) *MapView {
	m := &MapView{SelfOK: st.LocalOK, Players: []MapDotView{}, Color: self, Units: sameAreaUnits}
	if !st.LocalOK {
		return m
	}
	for _, p := range st.Players {
		if p.X == 0 && p.Y == 0 && p.Z == 0 {
			continue // no position yet
		}
		dot := MapDotView{Name: mk.text(p.Name), DX: float64(p.X - st.LocalX), DZ: float64(p.Z - st.LocalZ)}
		if p.HasColor {
			dot.Color = p.Color.Hex()
		}
		m.Players = append(m.Players, dot)
	}
	return m
}

// area is a remote player's "same area" hint: "near" within
// sameAreaUnits on the ground plane, "far" beyond, "" while unknown.
func area(st session.State, name string) (string, int) {
	if !st.LocalOK {
		return "", 0
	}
	for _, p := range st.Players {
		if p.Name != name || (p.X == 0 && p.Y == 0 && p.Z == 0) {
			continue
		}
		d := math.Hypot(float64(p.X-st.LocalX), float64(p.Z-st.LocalZ))
		if d <= sameAreaUnits {
			return "near", int(d)
		}
		return "far", int(d)
	}
	return "", 0
}

// launchOptions are this player's Dolphin settings as Start uses them:
// the saved ones with the environment's overrides, the user folder that
// Dolphin uses when none is saved.
func (b *Bridge) launchOptions() session.LaunchOptions {
	c, _ := b.effective()
	o := session.LaunchOptions{Exe: c.Dolphin.Exe, UserDir: c.Dolphin.UserDir, ISO: c.ISO.Patched,
		Manual: !c.Dolphin.LaunchOnStart, InstallCheats: c.Dolphin.InstallCheats}
	if o.UserDir == "" && o.Exe != "" {
		o.UserDir, _ = launch.UserDir(b.denv(), o.Exe)
	}
	return o
}

// Start starts the session (the host's Start). Unless anyway, it is
// refused while someone isn't ready, naming them ("Start anyway" skips
// that). It is refused, too, when the app would have to launch this
// player's Dolphin and it isn't set up: not when one already runs on the
// player's folder (or WW_DOLPHIN_PID names one), which Start plays on.
func (b *Bridge) Start(anyway bool) (err error) {
	defer b.maskErr(&err)
	if o := b.launchOptions(); !o.Manual && (o.Exe == "" || o.ISO == "") && b.wouldLaunch(o) {
		return errors.New("choose your Dolphin and your patched disc in Setup first, or tick \"I'll start Dolphin myself\"")
	}
	if anyway {
		return b.ctrl.StartAnyway()
	}
	err = b.ctrl.Start()
	if errors.Is(err, session.ErrNotReady) {
		if l := b.ctrl.Snapshot().Lobby; l != nil {
			var names []string
			for _, m := range l.NotReady() {
				names = append(names, b.masker().text(m.Name))
			}
			return fmt.Errorf("not ready yet: %s (Start anyway starts without waiting)", strings.Join(names, ", "))
		}
	}
	return err
}

// wouldLaunch says Start would launch this player's Dolphin (no
// WW_DOLPHIN_PID, and none runs on o's user folder): session.DecideLaunch
// over the running Dolphins, as the engine decides.
func (b *Bridge) wouldLaunch(o session.LaunchOptions) bool {
	if strings.TrimSpace(b.getenv("WW_DOLPHIN_PID")) != "" {
		return false
	}
	env := b.denv()
	var running []session.RunningDolphin
	for _, p := range b.procs() {
		dir, _ := launch.ProcessUserDir(env, p)
		running = append(running, session.RunningDolphin{PID: p.PID, UserDir: dir})
	}
	return session.DecideLaunch(o, running).Kind == session.PlanLaunch
}

// SetStartDolphinMyself sets "I'll start Dolphin myself" and saves it:
// on, Start never launches Dolphin for this player, who starts it with
// the patched disc. It counts from the next Start.
func (b *Bridge) SetStartDolphinMyself(on bool) (err error) {
	defer b.maskErr(&err)
	b.mu.Lock()
	b.cfg.Dolphin.LaunchOnStart = !on
	b.mu.Unlock()
	return b.saveErr()
}

// LaunchDolphin starts this player's Dolphin again after Start (the
// "Dolphin closed" banner's Launch, the launch dialog's Try again).
func (b *Bridge) LaunchDolphin() (err error) {
	defer b.maskErr(&err)
	return b.ctrl.LaunchDolphin()
}
