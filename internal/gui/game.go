package gui

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/internal/placename"
	"github.com/StephenSHorton/ww-multiplayer/internal/seachart"
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

// MapView is the Session screen's sea chart (wind#51): every Link at its
// place on the Great Sea, in world units (+X east, +Z south; the sea is
// 7x7 squares of seachart.SquareUnits centred on 0). A Link on the sea is
// where it is; one in an island's house, cave or dungeon is pinned to that
// island's centre with the place's name; one whose stage is unknown is
// listed but not drawn. The island art comes separately, from SeaChart.
type MapView struct {
	SelfOK  bool         `json:"selfOk"` // this player's own dot is known
	Self    MapDotView   `json:"self"`
	Players []MapDotView `json:"players"`
	Color   string       `json:"color"` // this player's color
	// ChartRev changes when the island art changes (SeaChart): 0 while
	// there is none, and the chart draws its grid alone.
	ChartRev int `json:"chartRev"`
}

// MapDotView is one Link on the chart.
type MapDotView struct {
	Name  string  `json:"name"`
	Color string  `json:"color"`
	X     float64 `json:"x"` // world units
	Z     float64 `json:"z"`
	// Drawn is false when the Link can't be placed (a stage the chart
	// doesn't know): it is listed, not drawn.
	Drawn bool `json:"drawn"`
	// Pinned: off the sea, drawn at its island's centre.
	Pinned bool   `json:"pinned"`
	Square string `json:"square"` // "B7" (Outset); "" when not drawn
	Island string `json:"island"` // the square's island
	Where  string `json:"where"`  // the place, "Dragon Roost Cavern"; "" on the sea
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
	string(session.DolphinUpdating):  "Updating disc…",
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
			Detail:  "If the patched disc was made by another version of this app, close Dolphin: the app updates your disc by itself (the Play page shows it), then Launch Dolphin. If you loaded a save state, it may have been taken on another version: boot the disc from Dolphin's game list instead, and capture a new save state once you are in the game.",
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
	if !running || st.Lobby == nil || st.Lobby.Legacy {
		return nil
	}
	// The wrong-disc dialog's banner, which stays after the dialog is
	// closed (#1164): the session can't play until Dolphin runs the disc.
	switch d.State {
	case session.DolphinWrongGame:
		return &BannerView{Kind: "dolphin", Title: "Wrong disc in Dolphin.", Action: actSetup, ActionLabel: "Open Setup",
			Text: "Dolphin is running a different game. Close it and boot your patched Wind Waker disc."}
	case session.DolphinUnpatched:
		return &BannerView{Kind: "dolphin", Title: "Wrong disc in Dolphin.", Action: actSetup, ActionLabel: "Open Setup",
			Text: "Dolphin is running Wind Waker without this version's mod: an old patched disc or a save state from another version. Close Dolphin and boot your patched disc."}
	}
	if d.State != session.DolphinNone || d.PID != 0 {
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

// mapView is the sea chart of st, placed with ch (nil: no disc read
// yet, so the known dungeons are still placed, the rest of the stages not).
func mapView(st session.State, self string, mk masker, ch *seachart.Chart) *MapView {
	m := &MapView{SelfOK: st.LocalOK, Players: []MapDotView{}, Color: self}
	if st.LocalOK {
		m.Self = placeDot(ch, "You", self, float64(st.LocalX), float64(st.LocalZ), st.LocalPlace, st.LocalHasPlace)
		m.SelfOK = m.Self.Drawn
	}
	for _, p := range st.Players {
		if p.X == 0 && p.Y == 0 && p.Z == 0 {
			continue // no position yet
		}
		color := ""
		if p.HasColor {
			color = p.Color.Hex()
		}
		m.Players = append(m.Players, placeDot(ch, mk.text(p.Name), color, float64(p.X), float64(p.Z), p.Place, p.HasPlace && p.Fresh))
	}
	return m
}

// placeDot places one Link. Without a place (a build from before the place
// trailer, or a game between stages) its position is taken as the sea's,
// as the chart drew it before.
func placeDot(ch *seachart.Chart, name, color string, x, z float64, pl session.Place, has bool) MapDotView {
	d := MapDotView{Name: name, Color: color}
	if !has || seachart.IsSea(pl.Stage) {
		sq := seachart.SquareAt(x, z)
		d.X, d.Z, d.Drawn, d.Square, d.Island = x, z, true, sq.Name(), seachart.IslandName(sq)
		return d
	}
	sq, where, ok := ch.Where(pl.Stage)
	if !ok {
		d.Where = pl.Stage
		return d
	}
	d.X, d.Z = float64(sq.GX*seachart.SquareUnits), float64(sq.GZ*seachart.SquareUnits)
	if ch != nil {
		for _, is := range ch.Islands {
			if is.Square == sq {
				d.X, d.Z = float64(is.CX), float64(is.CZ)
			}
		}
	}
	d.Drawn, d.Pinned, d.Square, d.Island, d.Where = true, true, sq.Name(), seachart.IslandName(sq), where
	if d.Where == "" {
		d.Where = d.Island
	}
	return d
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
		if !sameStage(st, p) {
			return "", 0
		}
		d := math.Hypot(float64(p.X-st.LocalX), float64(p.Z-st.LocalZ))
		if d <= sameAreaUnits {
			return "near", int(d)
		}
		return "far", int(d)
	}
	return "", 0
}

// memberVitals is a lobby row's hearts, magic and rupees (wind#55): this
// player's from its own game, another's from its pose trailer, matched by
// name as area does; nil while unknown.
func memberVitals(st session.State, name string, self bool) *VitalsView {
	vv := func(v session.Vitals) *VitalsView {
		return &VitalsView{Life: v.Life, MaxLife: v.MaxLife, Magic: v.Magic, MaxMagic: v.MaxMagic, Rupees: v.Rupees}
	}
	if self {
		if !st.LocalVitalsOK {
			return nil
		}
		return vv(st.LocalVitals)
	}
	for _, p := range st.Players {
		if p.Name == name && p.HasVitals {
			return vv(p.Vitals)
		}
	}
	return nil
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
	o.Widescreen = widescreenAspect(c) // the Display card's (display.go)
	return o
}

// Start starts the session (the host's Start). Unless anyway, it is
// refused while someone isn't ready, naming them ("Start anyway" skips
// that). It is refused, too, when the app would have to launch this
// player's Dolphin and it isn't set up: not when one already runs on the
// player's folder (or WW_DOLPHIN_PID names one), which Start plays on.
// It first checks the disc and waits for its update (discReady): a disc
// another version patched is patched again, then Start goes on. A second
// Start while one waits does nothing.
func (b *Bridge) Start(anyway bool) (err error) {
	defer b.maskErr(&err)
	if b.updating() {
		return errUpdating
	}
	if o := b.launchOptions(); !o.Manual && (o.Exe == "" || o.ISO == "") && b.wouldLaunch(o) {
		return errors.New("choose your Dolphin and your patched disc in Setup first, or tick \"I'll start Dolphin myself\"")
	}
	if !b.disc.starting.CompareAndSwap(false, true) {
		return nil // a Start already waits for the disc
	}
	b.emitState() // Start's button is off while it waits
	sess := b.ctrl.Snapshot().Session
	waited, err := b.discReady("start")
	b.disc.starting.Store(false)
	b.emitState()
	if err != nil {
		return err
	}
	if st := b.ctrl.Snapshot(); waited && (st.Session != sess || !canStart(st.Lobby, st.Session != 0 && st.Phase != session.Ended && st.Phase != session.Idle)) {
		return nil // left (or started, or hosted anew) while the disc was updated
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

// ErrStaleDisc is the error of Start and LaunchDolphin when the patched
// disc Setup saved was patched by another version of this app: its mod
// would not match this build's, and Dolphin would boot without it. The
// frontend recognises the text (staleDiscPrefix) and offers "Re-patch your
// disc".
var ErrStaleDisc = errors.New(staleDiscPrefix + " was patched by another version of this app, so its mod doesn't match this one and the game would run without it. Re-patch your disc, then Start")

const staleDiscPrefix = "Your patched disc"

// checkDiscVersion refuses a Start that would launch this player's Dolphin
// on a disc another version patched (an update just installed and its
// re-patch not done or interrupted). A player who starts Dolphin
// themselves, a missing or unreadable disc (Setup's business) pass.
func (b *Bridge) checkDiscVersion() error {
	c, _ := b.effective()
	if !c.Dolphin.LaunchOnStart || c.ISO.Patched == "" || !isFile(c.ISO.Patched) {
		return nil
	}
	if st, err := b.inspect(c.ISO.Patched); err == nil && st == patch.OtherVersion {
		return ErrStaleDisc
	}
	return nil
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
	if !b.disc.starting.CompareAndSwap(false, true) {
		return nil // a launch already waits for the disc
	}
	_, err = b.discReady("launch")
	b.disc.starting.Store(false)
	if err != nil {
		return err
	}
	return b.ctrl.LaunchDolphin()
}

// sameStage says a remote may be measured against this player: both
// places are known and name the same stage with both games in play, or
// either place is unknown (a build from before the place trailer), where
// the position alone is the hint as before. A partner on another stage,
// loading one, or gone quiet (a stale place) is never "same area" nor on
// the minimap: its coordinates are another space's, or old.
func sameStage(st session.State, p session.Player) bool {
	if !p.HasPlace {
		return true
	}
	if !p.Fresh || !p.Place.InPlay {
		return false
	}
	if !st.LocalHasPlace {
		return true
	}
	return st.LocalPlace.InPlay && st.LocalPlace.Stage == p.Place.Stage
}

// memberPlace is a remote member's Players-row place (MemberView.Place):
// its words, its state ("in", "loading", "stale" or "" while unknown) and,
// for a stale one, its age in whole seconds.
func memberPlace(st session.State, name string) (place, state string, lastSeen int) {
	for _, p := range st.Players {
		if p.Name != name || !p.HasPlace {
			continue
		}
		place = placename.Describe(p.Place.Stage, p.Place.Room)
		switch {
		case !p.Fresh:
			return place, "stale", int(p.PlaceAge / time.Second)
		case !p.Place.InPlay:
			return "", "loading", 0
		}
		return place, "in", 0
	}
	return "", "", 0
}
