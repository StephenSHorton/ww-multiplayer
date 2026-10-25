package gui

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/backup"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
)

// Co-op in the window (App PR 9, design §2.5, §2.7, §2.8): the host's
// world choice (Off, New world, Continue with the worlds on disk, and the
// Advanced switches), every player's card-file preview (cards.go), the
// guided New-world dialog, every co-op question and piece of advice as a
// dialog, the host's merge requests, the Session screen's co-op panel
// from the pipelines' structured status (not /status text), its buttons
// (the console verbs) and the sync feed. As with the lobby, Go decides
// what to show from the controller's State and the frontend draws it.
// Co-op stays optional: Off is the default and a choice like the others
// (owner decision 2026-10-16).

// CoopRequest is the host's co-op choice.
type CoopRequest struct {
	Mode         string `json:"mode"` // "off", "new" or "continue"
	World        string `json:"world"`
	DryRun       bool   `json:"dryRun"`
	Merge        bool   `json:"merge"`
	LiveSwitches bool   `json:"liveSwitches"`
	ISO          string `json:"iso"` // the disc for the co-op tables; "" for the one patch last wrote
}

// CoopRelays are this player's own relay choices, host or joiner: the
// CLI's --coop-boss-kill and --coop-refresh (internal/session CoopOptions
// BossKill, Refresh). Neither is a lobby setting: each side asks its own
// game's REL, so each side picks its own.
type CoopRelays struct {
	// BossKill is the boss-dead relay's rows: "" for the default
	// (gohma,kalle,molgera,jalhalla,helmaroc), "none", or replica.BossKillTokens joined by ",".
	BossKill string `json:"bossKill"`
	// Refresh is the actor_refresh relay's tokens: "" or "none" for off,
	// or replica.RefreshTokens joined by ",", with "dry" to log only.
	Refresh string `json:"refresh"`
}

// WorldView is one co-op world on disk, for the host's Continue list.
type WorldView struct {
	ID      string `json:"id"`
	Seq     uint64 `json:"seq"`
	Created string `json:"created"` // local date and time it was founded
	InUse   bool   `json:"inUse"`
	Stale   bool   `json:"stale"`
	Err     string `json:"err,omitempty"`
	Text    string `json:"text"` // its `coop-world list` line
}

// CoopView is the session's co-op side for the window.
type CoopView struct {
	// Setting is the lobby's co-op setting ("off", "new" or a world ID);
	// Mode the same as off/new/continue, World the world it names (or
	// the world the session runs, once known).
	Setting      string `json:"setting"`
	Mode         string `json:"mode"`
	World        string `json:"world"`
	DryRun       bool   `json:"dryRun"`
	Merge        bool   `json:"merge"`
	LiveSwitches bool   `json:"liveSwitches"`
	// On says the session runs co-op now (its co-op side started).
	On bool `json:"on"`
	// MyDryRun is a joiner's own dry run (SetMyCoopDryRun).
	MyDryRun bool `json:"myDryRun"`
	// AutoYes is "Answer yes to co-op questions" (per session).
	AutoYes bool `json:"autoYes"`
	// Relays is this player's saved relay choices (SetCoopRelays), which
	// the save-file panel's fields start from.
	Relays CoopRelays `json:"relays"`
	// Choice is the host's saved choice, which the host panel starts
	// from.
	Choice CoopRequest `json:"choice"`
	// Cards is this player's card preview while co-op is chosen; nil
	// otherwise.
	Cards *CardsView `json:"cards"`
	// Panel is the Session screen's co-op panel once co-op runs.
	Panel *CoopPanelView `json:"panel"`
	// Backup is the folder this session's card backup went to.
	Backup string `json:"backup,omitempty"`
}

// CoopPanelView is the Session screen's co-op panel.
type CoopPanelView struct {
	Pipes []CoopPipeView `json:"pipes"`
	Hub   *CoopHubView   `json:"hub"`
	// Partners are the other players: Warp to partner needs one (v1.0
	// is two players, so the warp goes to the one other player).
	Partners []string `json:"partners"`
}

// CoopPipeView is one coop-sync pipeline (this player's game).
type CoopPipeView struct {
	Player string `json:"player"`
	// State is the replica's state code; Headline says it in words and
	// Tone colors it ("ok", "dry", "warn", "wait").
	State    string `json:"state"`
	Headline string `json:"headline"`
	Tone     string `json:"tone"`
	World    string `json:"world"`
	Seq      uint64 `json:"seq"`
	Written  int    `json:"written"`
	Escapes  int    `json:"escapes"`
	Failed   int    `json:"failed"`
	// Paused says the writes are off (Pause co-op writes, or an
	// auto-disable): the button says Resume.
	Paused      bool     `json:"paused"`
	DryRun      bool     `json:"dryRun"`
	Progression []string `json:"progression"`
	Puzzle      string   `json:"puzzle"`
	Combat      string   `json:"combat"`
	Tuner       string   `json:"tuner,omitempty"`
	// Partner is where the partner is ("" while unknown).
	Partner string `json:"partner,omitempty"`
	// Shares, Held, Keys, Life and LifeWhy are Progression's facts for
	// the progress card (session.CoopStatus): the features the host
	// granted by name ("story", "keys", ...; empty in a dry run), the
	// story bits held back ("label (why)"), each dungeon's small keys,
	// and whether max hearts are derived (LifeWhy: why not). NoKeyTable:
	// no small keys are derived at all (no co-op stage tables).
	Shares     []string      `json:"shares"`
	Held       []string      `json:"held"`
	Keys       []CoopKeyView `json:"keys"`
	NoKeyTable bool          `json:"noKeyTable,omitempty"`
	Life       bool          `json:"life"`
	LifeWhy    string        `json:"lifeWhy,omitempty"`
}

// CoopKeyView is one dungeon's small keys on the progress card.
type CoopKeyView struct {
	Dungeon string `json:"dungeon"` // short name ("DRC"), "" for a table with none
	Name    string `json:"name"`    // the dungeon's name ("Dragon Roost Cavern"), else Dungeon, else "table N"
	// On: its keys are derived (Why: why not); Seen: the loaded file has
	// been measured, so the counts mean something.
	On   bool   `json:"on"`
	Seen bool   `json:"seen"`
	Why  string `json:"why,omitempty"`
	// Have is the keys the game holds; Taken of Sources the key sources
	// whose flag is set, Open of Doors the key doors open; Target the
	// count it should hold (a write is Pending, or waits for IDLE).
	Have    int  `json:"have"`
	Taken   int  `json:"taken"`
	Sources int  `json:"sources"`
	Open    int  `json:"open"`
	Doors   int  `json:"doors"`
	Held    int  `json:"held"`
	Target  int  `json:"target"`
	Pending bool `json:"pending"`
}

// CoopHubView is the host's hub.
type CoopHubView struct {
	World    string         `json:"world"`
	Seq      uint64         `json:"seq"`
	Features string         `json:"features"`
	Merge    bool           `json:"merge"`
	Peers    []CoopPeerView `json:"peers"`
	Lines    []string       `json:"lines"`
}

// CoopPeerView is one co-op connection of the hub.
type CoopPeerView struct {
	Name  string `json:"name"`
	State string `json:"state"`
	File  string `json:"file"`
	Where string `json:"where,omitempty"`
}

// DialogCard is one option card of an advice dialog.
type DialogCard struct {
	Title string   `json:"title"`
	Text  string   `json:"text"`
	Steps []string `json:"steps,omitempty"`
	Warn  string   `json:"warn,omitempty"`
	// Action is a button on the card (an action ID), Label its text.
	Action string `json:"action,omitempty"`
	Arg    string `json:"arg,omitempty"`
	Label  string `json:"label,omitempty"`
}

// Co-op dialog kinds and actions.
const (
	dlgFounding = "founding"   // the guided New-world dialog
	dlgFounded  = "founded"    // the world is founded
	dlgBind     = "bind"       // "Bind this file?"
	dlgScrub    = "scrub"      // "Scrub its tag?"
	dlgMergeReq = "merge_req"  // host: a joiner asks to merge
	dlgAdvice   = "advice"     // far behind / needs merge / no backup
	dlgForeign  = "foreign"    // the loaded file is bound to another world
	actYes      = "answer-yes" // Arg: the question
	actNo       = "answer-no"  // Arg: the question
	actAllow    = "allow-merge"
	actDismissR = "dismiss-request"
	actDismissA = "dismiss-advice"
	actAskMerge = "request-merge"
	actSetMerge = "set-merge" // the host turns merging on itself
	actUnbind   = "unbind"
	actBackups  = "open-backups"
	actUserDir  = "open-userdir"
	actCopyText = "copy-text" // Arg: the text
)

// coopModeOf is a lobby co-op setting as off/new/continue.
func coopModeOf(setting string) (mode, world string) {
	switch setting {
	case "", "off":
		return "off", ""
	case session.CoopWorldNew:
		return "new", ""
	}
	return "continue", setting
}

var worldIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// settingsOf is the lobby's settings for the host's choice r.
func settingsOf(r CoopRequest) (session.LobbySettings, error) {
	s := session.LobbySettings{DryRun: r.DryRun, Merge: r.Merge, LiveSwitches: r.LiveSwitches}
	switch r.Mode {
	case "", "off":
		s.Coop = "off"
	case "new":
		s.Coop = session.CoopWorldNew
	case "continue":
		w := strings.ToLower(strings.TrimSpace(r.World))
		if !worldIDRe.MatchString(w) {
			return s, errors.New("pick a world to continue")
		}
		s.Coop = w
	default:
		return s, fmt.Errorf("co-op mode %q is not off, new or continue", r.Mode)
	}
	return s, nil
}

// requestOf is the host's choice for lobby settings s (and the disc).
func requestOf(s session.LobbySettings, iso string) CoopRequest {
	mode, world := coopModeOf(s.Coop)
	return CoopRequest{Mode: mode, World: world, DryRun: s.DryRun, Merge: s.Merge, LiveSwitches: s.LiveSwitches, ISO: iso}
}

// choiceOf is the host's saved choice.
func choiceOf(c config.Coop) CoopRequest {
	return CoopRequest{Mode: c.LastMode, World: c.LastWorld, DryRun: c.DryRun, Merge: c.Merge, LiveSwitches: c.LiveSwitches, ISO: c.ISO}
}

// SetCoop sets the host's co-op choice: saved for next time, and sent to
// the lobby when this player hosts one (in the lobby phase; after Start
// only merging can change). The world list goes with it, a summary the
// joiners' windows show.
func (b *Bridge) SetCoop(r CoopRequest) (err error) {
	defer b.maskErr(&err)
	s, err := settingsOf(r)
	if err != nil {
		return err
	}
	if r.Mode != "" && r.Mode != "off" && !b.coopMod() {
		return errNoCoopMod
	}
	b.mu.Lock()
	b.cfg.Coop.LastMode = r.Mode
	if r.Mode == "" {
		b.cfg.Coop.LastMode = "off"
	}
	if r.Mode == "continue" {
		b.cfg.Coop.LastWorld = s.Coop
	}
	b.cfg.Coop.DryRun, b.cfg.Coop.Merge, b.cfg.Coop.LiveSwitches = r.DryRun, r.Merge, r.LiveSwitches
	b.cfg.Coop.ISO = strings.TrimSpace(r.ISO)
	b.mu.Unlock()
	b.save()
	st := b.ctrl.Snapshot()
	if st.Lobby == nil || !st.Lobby.Host || !b.running() {
		return nil
	}
	if ws, err := b.ctrl.CoopWorlds(); err == nil {
		for i, w := range ws {
			if i == 16 || w.Err != "" {
				continue
			}
			s.Worlds = append(s.Worlds, session.LobbyWorld{ID: w.ID, Seq: w.Seq, InUse: w.InUse})
		}
	}
	return b.ctrl.SetCoopSettings(s)
}

// CoopWorlds lists the co-op worlds on this computer, newest first, for
// the host's Continue list.
func (b *Bridge) CoopWorlds() (_ []WorldView, err error) {
	defer b.maskErr(&err)
	ws, err := b.ctrl.CoopWorlds()
	if err != nil {
		return nil, err
	}
	out := []WorldView{}
	for _, w := range ws {
		v := WorldView{ID: w.ID, Seq: w.Seq, InUse: w.InUse, Stale: w.Stale, Err: w.Err, Text: w.Text}
		if !w.Created.IsZero() {
			v.Created = w.Created.Local().Format("2 Jan 2006 15:04")
		}
		out = append(out, v)
	}
	return out, nil
}

// SetMyCoopDryRun is a joiner's own dry run: its game gets no writes but
// a binding tag after a yes, whatever the host chose (the CLI's
// `join --coop --coop-dry-run`). It counts from the host's GO; it is not
// saved.
func (b *Bridge) SetMyCoopDryRun(on bool) {
	b.mu.Lock()
	b.myDryRun = on
	b.mu.Unlock()
	b.emit(EventView{Kind: "state"})
}

// SetCoopRelays sets this player's own relay choices, host or joiner,
// and saves them. They are checked as the CLI checks --coop-boss-kill and
// --coop-refresh (replica.ParseBossKill, ParseRefresh), so the window
// refuses what the CLI refuses, in the same words. Like the disc, they
// count from the next co-op start: a session already running keeps the
// ones it started with.
func (b *Bridge) SetCoopRelays(r CoopRelays) (err error) {
	defer b.maskErr(&err)
	r.BossKill, r.Refresh = strings.TrimSpace(r.BossKill), strings.TrimSpace(r.Refresh)
	if _, err := replica.ParseBossKill(r.BossKill); err != nil {
		return err
	}
	if _, err := replica.ParseRefresh(r.Refresh); err != nil {
		return err
	}
	b.mu.Lock()
	b.cfg.Coop.BossKill, b.cfg.Coop.Refresh = r.BossKill, r.Refresh
	b.mu.Unlock()
	b.save()
	b.emit(EventView{Kind: "state"})
	return nil
}

// coopOptions is this player's own co-op choices, read when co-op
// starts (HostOptions.CoopFn). The co-op tables come from the disc the
// host chose under Advanced, else from the patched disc Dolphin boots
// (Setup's): the stage files they are built from are the same.
func (b *Bridge) coopOptions() session.CoopOptions {
	b.mu.Lock()
	o := session.CoopOptions{ISO: b.cfg.Coop.ISO, DryRun: b.myDryRun, BossKill: b.cfg.Coop.BossKill, Refresh: b.cfg.Coop.Refresh}
	b.mu.Unlock()
	if o.ISO == "" {
		o.ISO = b.launchOptions().ISO
	}
	return o
}

// Answer answers the co-op question id: yes binds (or scrubs).
func (b *Bridge) Answer(id string, yes bool) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.Answer(id, yes)
}

// SetAutoYes turns "Answer yes to co-op questions" on or off for this
// session.
func (b *Bridge) SetAutoYes(on bool) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.SetAutoYes(on)
}

// DismissAdvice closes a piece of co-op advice.
func (b *Bridge) DismissAdvice(id string) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.DismissAdvice(id)
}

// RequestMerge asks the host to allow merging (a joiner's far-behind or
// needs-merge advice).
func (b *Bridge) RequestMerge() (err error) {
	defer b.maskErr(&err)
	return b.ctrl.RequestMerge()
}

// AllowMerge answers a joiner's merge request yes (host).
func (b *Bridge) AllowMerge(id string) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.AllowMerge(id)
}

// DismissRequest answers a joiner's request no (host).
func (b *Bridge) DismissRequest(id string) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.DismissRequest(id)
}

// SetMerge turns merging on for the running session (the host's own
// "Allow merging" on its own advice).
func (b *Bridge) SetMerge(on bool) (err error) {
	defer b.maskErr(&err)
	st := b.ctrl.Snapshot()
	if st.Lobby == nil {
		return session.ErrNoLobby
	}
	s := st.Lobby.Settings
	s.Merge = on
	return b.ctrl.SetCoopSettings(s)
}

// UnbindFile removes the co-op tag of the file loaded in this player's
// game (in memory: the card keeps it until a save), so a file bound to
// another world can found or join this one. world is the tag's world the
// dialog showed (the action's Arg): a file loaded since, with another
// tag, is left alone.
func (b *Bridge) UnbindFile(world string) (err error) {
	defer b.maskErr(&err)
	return b.ctrl.UnbindFile(world)
}

// coopVerbs are the console verbs the panel's buttons send.
var coopVerbs = map[string]session.Verb{
	"refresh": session.VerbRefresh, "warp": session.VerbWarp,
	"pause": session.VerbCoopOff, "resume": session.VerbCoopOn, "status": session.VerbStatus,
}

// CoopCommand runs one of the panel's buttons: "refresh" (Refresh area),
// "warp" (Warp to partner: co-op v1.0 is two players, so the warp goes to
// the one other player), "pause" / "resume" (co-op writes), "status". It
// goes to this player's own pipeline; what came of it is in the log.
func (b *Bridge) CoopCommand(button string) (err error) {
	defer b.maskErr(&err)
	v, ok := coopVerbs[button]
	if !ok {
		return fmt.Errorf("no co-op button %q", button)
	}
	return b.ctrl.Command(session.Command{Verb: v})
}

// CoopStatusText is /status's text for every pipeline (and the host's
// hub), for Copy status: what a bug report wants.
func (b *Bridge) CoopStatusText() string {
	st := b.ctrl.Snapshot()
	var lines []string
	for _, cs := range st.CoopStatus {
		lines = append(lines, cs.Lines...)
	}
	if h := st.CoopHub; h != nil {
		lines = append(lines, fmt.Sprintf("co-op: hub: world %s at seq %d; allows %s; merge %v", h.World, h.Seq, h.Features, h.Merge))
		for _, p := range h.Peers {
			lines = append(lines, fmt.Sprintf("co-op: hub:   %s: %s, file %s %s", p.Name, p.State, p.File, p.Reason))
		}
		for _, l := range h.Lines {
			lines = append(lines, "co-op: hub:   "+l)
		}
	}
	if len(lines) == 0 {
		return "co-op: not running"
	}
	return b.masker().text(strings.Join(lines, "\n"))
}

// OpenFolder opens a folder in the file manager: "backups" (the card
// backups), "userdir" (this player's Dolphin user folder) or "bugreports"
// (where SaveBugReport writes).
func (b *Bridge) OpenFolder(which string) (err error) {
	defer b.maskErr(&err)
	var dir string
	switch which {
	case "backups":
		if dir = b.ctrl.Snapshot().CoopBackup; dir == "" {
			if dir, err = backup.Dir(); err != nil {
				return err
			}
		}
	case "userdir":
		if dir = b.launchOptions().UserDir; dir == "" {
			return errors.New("no Dolphin user folder is set: choose it in Setup")
		}
	case "bugreports":
		if dir, err = b.bug.reportDir(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("no folder %q", which)
	}
	return b.openPath(dir)
}

// openFolderHere opens dir in this computer's file manager.
func openFolderHere(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}

// coopView is the session's co-op side for the window; nil outside a
// lobby session.
func (b *Bridge) coopView(st session.State, running bool, mk masker) *CoopView {
	l := st.Lobby
	if l == nil || l.Legacy || !b.coopMod() {
		return nil
	}
	b.mu.Lock()
	choice, myDry := choiceOf(b.cfg.Coop), b.myDryRun
	relays := CoopRelays{BossKill: b.cfg.Coop.BossKill, Refresh: b.cfg.Coop.Refresh}
	b.mu.Unlock()
	v := &CoopView{Setting: l.Settings.Coop, DryRun: l.Settings.DryRun, Merge: l.Settings.Merge, LiveSwitches: l.Settings.LiveSwitches,
		On: st.Coop.On, MyDryRun: myDry, AutoYes: st.AutoYes, Relays: relays, Choice: choice, Backup: st.CoopBackup}
	if v.Setting == "" {
		v.Setting = "off"
	}
	v.Mode, v.World = coopModeOf(v.Setting)
	if st.Coop.On && st.Coop.World != "" && st.Coop.World != session.CoopWorldNew {
		v.World = st.Coop.World
	}
	if f := st.Founding; f != nil && f.World != "" {
		v.World = f.World
	}
	gathering := l.Phase == session.LobbyPhaseLobby || l.Phase == session.LobbyPhaseStarting
	switch {
	case v.Mode != "off" && running:
		v.Cards = b.cards(b.launchOptions().UserDir, v.Setting, l.Host)
		if gathering {
			b.noteFileHint(st.Session, v.Cards.Short)
		}
	case running && gathering:
		b.noteFileHint(st.Session, "") // co-op went back to Off: no file to show
	}
	if st.Coop.On && running {
		v.Panel = coopPanel(st, mk)
	}
	return v
}

// coopPanel is the Session screen's co-op panel from the pipelines'
// structured status.
func coopPanel(st session.State, mk masker) *CoopPanelView {
	p := &CoopPanelView{Pipes: []CoopPipeView{}, Partners: []string{}}
	for _, cs := range st.CoopStatus {
		p.Pipes = append(p.Pipes, pipeView(cs))
	}
	if h := st.CoopHub; h != nil {
		hv := &CoopHubView{World: h.World, Seq: h.Seq, Features: h.Features, Merge: h.Merge, Peers: []CoopPeerView{}, Lines: append([]string{}, h.Lines...)}
		for _, pr := range h.Peers {
			pv := CoopPeerView{Name: mk.text(pr.Name), State: pr.State, File: pr.File}
			if pr.Stage != "" {
				pv.Where = fmt.Sprintf("%s room %d", pr.Stage, pr.Room)
			}
			hv.Peers = append(hv.Peers, pv)
		}
		p.Hub = hv
	}
	if l := st.Lobby; l != nil {
		for _, m := range l.Members {
			if !(m.ID == l.Member && m.Via == session.ViaApp) {
				p.Partners = append(p.Partners, mk.text(m.Name))
			}
		}
	}
	return p
}

// pipeView is one pipeline's panel card.
func pipeView(cs session.CoopStatus) CoopPipeView {
	v := CoopPipeView{Player: cs.Player, State: cs.StateCode, World: cs.World, Seq: cs.Seq, Written: cs.Written,
		Escapes: cs.Escapes, Failed: cs.Failed, DryRun: cs.DryRun, Paused: cs.WritesOff != "" && !cs.DryRun,
		Progression: append([]string{}, cs.Progression...), Puzzle: cs.Puzzle, Combat: cs.Combat, Tuner: cs.Tuner,
		Shares: append([]string{}, cs.Shares...), Held: append([]string{}, cs.HeldStory...), Keys: []CoopKeyView{},
		NoKeyTable: cs.NoKeyTable, Life: cs.Life, LifeWhy: cs.LifeWhy}
	for _, k := range cs.Keys {
		v.Keys = append(v.Keys, CoopKeyView{Dungeon: k.Dungeon, Name: dungeonName(k.Table, k.Dungeon), On: k.On, Seen: k.Seen, Why: k.Why,
			Have: k.Have, Taken: k.Taken, Sources: k.Sources, Open: k.Open, Doors: k.Doors, Held: k.Held, Target: k.Target, Pending: k.Pending})
	}
	if cs.PartnerStage != "" {
		v.Partner = fmt.Sprintf("%s, room %d", cs.PartnerStage, cs.PartnerRoom)
	}
	why := ""
	if cs.Why != "" {
		why = ": " + cs.Why
	}
	switch cs.StateCode {
	case "armed":
		switch {
		case cs.DryRun:
			v.Headline, v.Tone = "Armed: dry run (nothing is written but the tag)", "dry"
		case cs.WritesOff != "":
			v.Headline, v.Tone = "Writes off: "+cs.WritesOff, "warn"
		default:
			v.Headline, v.Tone = "Armed: the world is written into your game", "ok"
		}
	case "offline":
		v.Headline, v.Tone = "Armed offline: queueing until the host is back", "wait"
	case "unbound":
		v.Headline, v.Tone = "File not bound"+why, "warn"
	case "refused":
		v.Headline, v.Tone = "Refused by the host"+why, "warn"
	case "hello":
		v.Headline, v.Tone = "Waiting for your file in game"+why, "wait"
	default:
		v.Headline, v.Tone = "Not connected"+why, "wait"
	}
	return v
}

// dungeonName is a key table's name on the progress card: the dungeon's
// (wwapi/tables/keys.Dungeons), else its short name, else "table N".
func dungeonName(table int, short string) string {
	for _, d := range keys.Dungeons {
		if d.Table == table || (short != "" && d.Short == short) {
			return d.Name
		}
	}
	if short != "" {
		return short
	}
	return fmt.Sprintf("table %d", table)
}

// coopBanner is the co-op banner the state calls for (design §2.7): the
// host's founding while it waits or is blocked (its dialog may have been
// closed, and nothing else shows it), "Co-op writes turned off: reason"
// after an auto-disable, or "File not bound"; nil for none.
func coopBanner(st session.State, running bool) *BannerView {
	if !running || !st.Coop.On {
		return nil
	}
	if f := st.Founding; f != nil && (f.Phase == session.FoundingWaiting || f.Phase == session.FoundingBlocked) {
		b := &BannerView{Kind: "coop", Title: "Founding the new world:"}
		switch {
		case f.Phase == session.FoundingWaiting && f.Why != "":
			b.Text = f.Why + "."
		case f.Phase == session.FoundingWaiting:
			b.Text = "in game and idle: founding the world."
		default:
			b.Text = f.BlockText + "."
		}
		if f.Phase == session.FoundingBlocked && (f.Blocker == session.BlockBound || f.Blocker == session.BlockForeign || f.Blocker == session.BlockGarbage) {
			b.Action, b.ActionLabel, b.ActionArg = actUnbind, "Remove the tag", f.TagWorld
		}
		return b
	}
	world := st.Coop.World
	for _, cs := range st.CoopStatus {
		if cs.World != "" {
			world = cs.World
		}
		switch {
		case cs.StateCode == "armed" && cs.WritesOff != "" && !cs.DryRun:
			return &BannerView{Kind: "coop", Title: "Co-op writes turned off:", Text: cs.WritesOff + ". Pose sync goes on.",
				Action: "coop-resume", ActionLabel: "Resume co-op writes"}
		case cs.StateCode == "unbound" || cs.StateCode == "refused":
			text := "Your loaded file takes no part in co-op yet"
			if cs.Why != "" {
				text += ": " + cs.Why
			}
			b := &BannerView{Kind: "coop", Title: "File not bound.", Text: text + "."}
			// A file bound to another world (its dialog kept): its tag can
			// still be removed from here.
			if (cs.FileState == "foreign" || cs.FileState == "bound") && cs.TagWorld != "" && world != "" && world != session.CoopWorldNew && cs.TagWorld != world {
				b.Action, b.ActionLabel, b.ActionArg = actUnbind, "Remove the tag", cs.TagWorld
			}
			return b
		}
	}
	return nil
}

// coopDialog is the co-op dialog the state calls for, or nil: the
// founding (host), the oldest open question, a joiner's merge request
// (host), the oldest advice, or a file bound to another world.
func coopDialog(st session.State, running bool, mk masker) *DialogView {
	if !running || st.Lobby == nil {
		return nil
	}
	key := func(parts ...string) string { return fmt.Sprintf("%d:%s", st.Session, strings.Join(parts, ":")) }
	// The founding while it waits; the founded message last, after
	// whatever asks something (the host's bind question comes next).
	founding := foundingDialog(st.Founding, key)
	if founding != nil && founding.Kind != dlgFounded {
		return founding
	}
	if len(st.Questions) > 0 {
		return questionDialog(st.Questions[0], st, key)
	}
	if len(st.Requests) > 0 {
		r := st.Requests[0]
		from := mk.text(r.From)
		return &DialogView{Key: key("r", r.ID), Kind: dlgMergeReq, Live: true, Title: "Allow merging?",
			Text:    fmt.Sprintf("%s's file has progress the world lacks, or is far behind it. Allow merging? Their progress then joins the world, and the world's joins their file.", from),
			Detail:  "Merging stays on for the rest of this session.",
			Cancel:  actDismissR + ":" + r.ID,
			Actions: []DialogAction{{ID: actAllow, Arg: r.ID, Label: "Allow merging", Primary: true}, {ID: actDismissR, Arg: r.ID, Label: "Not now"}}}
	}
	if len(st.Advice) > 0 {
		return adviceDialog(st.Advice[0], st, key)
	}
	// A file bound to another world: the replica judged it against no
	// world yet (bound) or against the session's (foreign).
	world := st.Coop.World
	for _, cs := range st.CoopStatus {
		if cs.World != "" {
			world = cs.World
		}
		if (cs.FileState == "foreign" || cs.FileState == "bound") && cs.TagWorld != "" && world != "" && world != session.CoopWorldNew && cs.TagWorld != world {
			return &DialogView{Key: key("foreign", cs.TagWorld), Kind: dlgForeign, Live: true, Title: "This file is bound to another co-op world",
				Text:    fmt.Sprintf("The file you loaded is bound to world %s, so it can't join this session's world. Remove its tag to bind it here instead: you'll be asked to bind it next.", short(cs.TagWorld)),
				Detail:  "Only your game's memory changes: the file leaves world " + short(cs.TagWorld) + " at your next in-game save, and loading it from the card again brings the tag back.",
				Actions: []DialogAction{{ID: actUnbind, Arg: cs.TagWorld, Label: "Remove the tag", Primary: true}, {ID: actOK, Label: "Keep it"}}}
		}
	}
	return founding
}

// short is a world ID shortened for a title ("3fa9…").
func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// foundingDialog is the guided New-world dialog (design §2.5): load the
// file to share and stand in game, a live check from the in-game gate,
// then the founded message.
func foundingDialog(f *session.Founding, key func(...string) string) *DialogView {
	if f == nil {
		return nil
	}
	steps := []string{
		"Load the file you want to share in your Dolphin.",
		"Stand in game: out of menus, cutscenes and the pause screen.",
		"The world is founded from that file, and everyone joins it.",
	}
	switch f.Phase {
	case session.FoundingFounded:
		return &DialogView{Key: key("founded"), Kind: dlgFounded, Title: "World " + short(f.World) + " founded",
			Text:    "Your file founded co-op world " + f.World + ". Save in game so your card keeps the tag.",
			Detail:  "Your file joins it through the Bind question (say Bind); then save in game.",
			Actions: []DialogAction{{ID: actOK, Label: "OK", Primary: true}}}
	case session.FoundingFailed:
		return &DialogView{Key: key("found", "failed"), Kind: dlgFounding, Title: "No world was founded",
			Text: "The new world couldn't be made, so this session plays without co-op.", Detail: f.Err,
			Actions: []DialogAction{{ID: actOK, Label: "OK", Primary: true}}}
	case session.FoundingBlocked:
		// Keyed by the file too: closed, it opens again for another file
		// (and the founding banner keeps "Remove the tag" meanwhile).
		d := &DialogView{Key: key("found", f.Blocker, f.File), Kind: dlgFounding, Live: true, Title: "Found a new world", List: steps,
			Check: &CheckView{Text: "Loaded: " + f.File}}
		switch f.Blocker {
		case session.BlockBound:
			if f.TagWorldHere {
				d.Text = fmt.Sprintf("This file is already bound to world %s. Continue that world instead (Leave, then pick it under Continue), or remove its tag to found a new world from this file.", f.TagWorld)
			} else {
				d.Text = fmt.Sprintf("This file is bound to world %s, which isn't on this computer (another player hosts it). Remove its tag to found a new world from this file, or load another file.", f.TagWorld)
			}
			d.Detail = "Removing the tag changes only your game's memory: the file leaves world " + short(f.TagWorld) + " at your next in-game save."
			d.Actions = []DialogAction{{ID: actUnbind, Arg: f.TagWorld, Label: "Remove the tag", Primary: true}, {ID: actOK, Label: "Close"}}
		case session.BlockGarbage, session.BlockForeign:
			d.Text = "This file holds a tag this version can't use, so it can't found a world. Remove it to found the world from this file, or load another file."
			d.Detail = f.BlockText
			d.Actions = []DialogAction{{ID: actUnbind, Arg: f.TagWorld, Label: "Remove the tag", Primary: true}, {ID: actOK, Label: "Close"}}
		default:
			d.Text = "This file can't found a world: load another file."
			d.Detail = f.BlockText
			d.Actions = []DialogAction{{ID: actOK, Label: "Close", Primary: true}}
		}
		return d
	}
	d := &DialogView{Key: key("found", ""), Kind: dlgFounding, Live: true, Title: "Found a new world", List: steps,
		Text:    "The new co-op world is founded from the file you load. Load the file you want to share and stand in game.",
		Actions: []DialogAction{{ID: actOK, Label: "Hide", Primary: true}}}
	if f.Why != "" {
		d.Check = &CheckView{Text: "Waiting: " + f.Why}
	} else {
		d.Check = &CheckView{OK: true, Text: "In game and idle: founding the world…"}
	}
	if f.File != "" {
		d.Detail = "Loaded: " + f.File
	}
	return d
}

// questionDialog is a co-op question as a dialog (design §2.8).
func questionDialog(q session.Question, st session.State, key func(...string) string) *DialogView {
	file := "this file"
	if name := strings.TrimSpace(q.FileName); name != "" {
		file = fmt.Sprintf("“%s” (File %d)", name, q.FileSlot)
	}
	d := &DialogView{Key: key("q", q.ID), Live: true, Cancel: actNo + ":" + q.ID, AutoYes: true}
	if q.Kind == session.QuestionScrub {
		d.Kind, d.Title = dlgScrub, "This file carries a co-op tag from another file"
		d.Text = fmt.Sprintf("%s carries a tag for world %s that it was never bound with: it was copied from another file or slot, or carried into a new game. Removing it keeps the stale tag out of your next save; nothing else is written.", capFirst(file), short(q.TagWorld))
		if q.MaybeFresh {
			d.Text = fmt.Sprintf("%s carries a tag for world %s that fails only one check: the file hasn't been loaded from the card since it was tagged. It may be a fresh bind to that world, or a tag leaked from another file, and only that world's host can tell. Removing it unbinds the file from that world.", capFirst(file), short(q.TagWorld))
		}
		d.List = q.Failed
		d.Actions = []DialogAction{{ID: actYes, Arg: q.ID, Label: "Remove the tag", Primary: true}, {ID: actNo, Arg: q.ID, Label: "Leave it"}}
		return d
	}
	d.Kind = dlgBind
	d.Title = fmt.Sprintf("Bind %s to world %s?", file, short(q.World))
	d.Text = "Binding ties this save file to the co-op world: from then on the world's progress is written into it, and its progress joins the world. The app writes a small tag into the file, which reaches your memory card at your next in-game save; nothing else in the file is written."
	switch {
	case q.MaybeFresh:
		d.Text += fmt.Sprintf(" Its current tag, of world %s, may be a fresh bind to that world that only its host can tell from a leak; binding replaces it.", short(q.TagWorld))
	case q.TagWorld != "" && q.TagWorld != q.World:
		d.Text += " Its old tag is replaced."
	}
	d.List = q.Details
	if st.CoopBackup != "" {
		d.Note = "Your memory card was backed up to " + st.CoopBackup + "."
		d.NoteAction = &DialogAction{ID: actBackups, Label: "Open folder"}
	}
	d.Actions = []DialogAction{{ID: actYes, Arg: q.ID, Label: "Bind", Primary: true}, {ID: actNo, Arg: q.ID, Label: "Not now"}}
	return d
}

// adviceDialog is a piece of co-op advice as a dialog (design §2.8).
func adviceDialog(a session.Advice, st session.State, key func(...string) string) *DialogView {
	d := &DialogView{Key: key("a", a.ID), Kind: dlgAdvice, Live: true, Cancel: actDismissA + ":" + a.ID,
		Actions: []DialogAction{{ID: actDismissA, Arg: a.ID, Label: "Close", Primary: true}}}
	ask := DialogCard{Title: "Ask the host to allow merging", Action: actAskMerge, Label: "Ask the host",
		Text: "The host can let your file merge with the world: your progress joins the world, and the world's joins your file."}
	if st.Role == session.RoleHost {
		ask = DialogCard{Title: "Allow merging", Action: actSetMerge, Label: "Allow merging",
			Text: "Merge files with progress the world lacks, or far behind it, for the rest of this session (as --coop-merge)."}
	}
	switch a.Kind {
	case session.AdviceFarBehind:
		d.Title = "The world is far ahead of your file"
		d.Text = "Your file is far behind the world: a merge would move where it resumes. Two ways on:"
		if st.Role == session.RoleHost {
			// The host's save is this file: copying it is no way on.
			d.Cards = []DialogCard{
				{Title: "Load a file bound to this world", Text: "Load the file that is already bound to this world (the card preview marks it), then stand in game: it carries the world's progress."},
				ask,
			}
			break
		}
		d.Cards = []DialogCard{
			{Title: "Copy the host's save", Text: "Start from the host's file, already bound to the world:",
				Steps: []string{
					"Export your own save first in Dolphin's Memory Card Manager (Tools › Memory Card Manager); this session's card backup holds it too.",
					"The host saves in game and loads its file from the card again.",
					"The host exports its save, and you import it with Dolphin closed: it replaces all three of your files.",
				},
				Warn: "The copy replaces all three files of your card with the host's."},
			ask,
		}
	case session.AdviceNeedsMerge:
		d.Title = "This file can't bind without merging"
		d.Text = "Your file has progress the world lacks, so it binds only if the host merges files."
		d.Cards = []DialogCard{ask}
	case session.AdviceNoBackup:
		d.Title = "No memory-card backup"
		d.Text = "Co-op won't bind files without a backup of your memory card, and none could be made this session."
		d.Detail = a.Text
		d.Actions = []DialogAction{{ID: actUserDir, Label: "Open Dolphin user folder"}, {ID: actSetup, Label: "Choose user folder…"}, {ID: actDismissA, Arg: a.ID, Label: "Close", Primary: true}}
	default:
		d.Title = "Co-op"
		d.Text = a.Text
	}
	return d
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// CheckView is a dialog's live check line.
type CheckView struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

// syncNoise are co-op lines the sync feed leaves to All logs: the start
// listing, the console help, the preflight's detail.
var syncNoise = []string{
	"co-op:   ", "co-op: stage tables", "co-op: shared progression", "co-op: room puzzles: written to",
	"co-op: combat: written to", "co-op: preflight", "co-op: backed up", "co-op: dry-run: ... and", "co-op: ... and",
	"co-op: world file", "co-op: no co-op stage tables",
}

// syncLine says a log line belongs in the sync feed: a co-op line (the
// replica's, or the host's hub's in its server log) users care about
// ("set t03 DRC chest 4", "holding … back", the reasons writes turned
// off), not the start listing.
func syncLine(level log.Level, text string) bool {
	t := text
	if strings.HasPrefix(t, "[srv ") {
		if i := strings.Index(t, "] "); i >= 0 {
			t = t[i+2:]
		}
	}
	if strings.HasPrefix(t, "Co-op world ") || strings.HasPrefix(t, "co-op writes are off") {
		return true // the world's line; a pipeline's writes turned off (/coop off, an auto-disable)
	}
	if !strings.HasPrefix(t, "co-op: ") {
		return false
	}
	for _, n := range syncNoise {
		if strings.HasPrefix(t, n) {
			return false
		}
	}
	return true
}
