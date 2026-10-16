package session

// Start → Dolphin → in game (App PR 8, design §2.6). When the host
// presses Start, every app gets this player's Dolphin going: it attaches
// to one already running on this player's Dolphin user folder, launches
// the configured Dolphin with the patched disc, or, for a player who
// starts Dolphin themselves, waits for one. The engine does the work (it
// sees the processes and their memory); what it decides is decided here,
// by pure functions a test drives with fakes: DecideLaunch (attach,
// launch or wait), DolphinStateOf (what a probe of a Dolphin means), and
// the Start rules (host only, everyone ready or Start anyway).

import (
	"errors"
	"path/filepath"
	"strings"
)

// DolphinState is this player's Dolphin as the app sees it: the lobby's
// Dolphin chip. The first four are the lobby wire's values
// (network.DolphinNone, ...); the others are free-form values the wire
// carries as they are.
type DolphinState string

const (
	DolphinNone      DolphinState = "none"       // no Dolphin (not running, or it closed)
	DolphinLaunching DolphinState = "launching"  // the app started it; no game in its memory yet
	DolphinTitle     DolphinState = "title"      // the patched Wind Waker, at the title screen or a menu
	DolphinInGame    DolphinState = "in_game"    // the patched Wind Waker, Link in the world
	DolphinNoGame    DolphinState = "no_game"    // Dolphin is open but runs no game
	DolphinWrongGame DolphinState = "wrong_game" // Dolphin runs another game
	DolphinUnpatched DolphinState = "unpatched"  // Wind Waker without this build's mod
)

// Playable says the Dolphin runs the patched Wind Waker: the pose
// pipelines can run on it (remote Links show on the title screen, #38).
func (s DolphinState) Playable() bool { return s == DolphinTitle || s == DolphinInGame }

// Bad says the Dolphin runs something the session can't play on: the
// window says what to boot instead.
func (s DolphinState) Bad() bool { return s == DolphinWrongGame || s == DolphinUnpatched }

// GameID is the disc this build plays: Wind Waker (USA).
const GameID = "GZLE01"

// Probe is one look at a Dolphin process.
type Probe struct {
	// Running says the process is still there.
	Running bool
	// GameID is the ID at the start of its emulated RAM ("GZLE01"); ""
	// when it runs no game (yet).
	GameID string
	// Patched says the RAM holds this build's mod (its injected code).
	Patched bool
	// InGame says Link is in the world (Dolphin.IsInGame).
	InGame bool
}

// DolphinStateOf is what probe p of a Dolphin means. launching says the
// app started this Dolphin and its boot has not finished: no game in its
// memory then is "launching", not "no game".
func DolphinStateOf(p Probe, launching bool) DolphinState {
	switch {
	case !p.Running:
		return DolphinNone
	case p.GameID == "":
		if launching {
			return DolphinLaunching
		}
		return DolphinNoGame
	case p.GameID != GameID:
		return DolphinWrongGame
	case !p.Patched:
		return DolphinUnpatched
	case p.InGame:
		return DolphinInGame
	}
	return DolphinTitle
}

// LaunchOptions is how Start gets this player's Dolphin going: the app's
// settings (Setup), never the CLI's (it doesn't read them).
type LaunchOptions struct {
	Exe     string // the Dolphin program
	UserDir string // its user folder: what it is launched with (-u), and how a running one is recognized
	ISO     string // the patched disc it boots (-e)
	// Manual is "I'll start Dolphin myself": the app never launches one;
	// it waits for the player's.
	Manual bool
	// InstallCheats puts the bundled Gecko codes into a user folder that
	// has none, before a launch (dolphinenv.EnsureCheatsIni).
	InstallCheats bool
}

// RunningDolphin is one running Dolphin process.
type RunningDolphin struct {
	PID uint32
	// UserDir is the user folder it runs on: its -u argument, or the one
	// its program uses by default. "" when that can't be known (its
	// command line can't be read).
	UserDir string
	// Explicit says UserDir is the folder its command line names (-u or
	// --user): someone chose that folder for it, usually another app's
	// launch on another player's folder.
	Explicit bool
	// Claimed says another session already plays on it: its mailbox's
	// shadow_mode is set (a puppet-sync armed it).
	Claimed bool
	// State is its probe's meaning (DolphinStateOf, not launching).
	State DolphinState
}

// PlanKind is what DecideLaunch decided.
type PlanKind string

const (
	PlanAttach PlanKind = "attach" // play on the running Dolphin PID
	PlanLaunch PlanKind = "launch" // start the configured Dolphin
	PlanWait   PlanKind = "wait"   // the player starts it: look again later
)

// LaunchPlan is DecideLaunch's decision.
type LaunchPlan struct {
	Kind PlanKind
	PID  uint32 // PlanAttach
	// Why says it in the session log's words.
	Why string
}

// DecideLaunch decides how this player's Dolphin gets going (design §2.6
// step 2), from the running Dolphins, oldest first:
//
//   - A running Dolphin on this player's user folder (or one whose folder
//     can't be known) is attached to, one running Wind Waker first. Two
//     players on one computer each have their own folder, so neither
//     takes the other's Dolphin.
//   - Otherwise "I'll start Dolphin myself" attaches to a Dolphin that
//     runs Wind Waker on another folder (the player may have opened
//     Dolphin without -u, on its default folder), or waits for one. It
//     never takes a Dolphin started with an explicit, different -u (that
//     is another player's, usually another app's launch) or one another
//     session already plays on (Claimed).
//   - Otherwise the configured Dolphin is launched.
//
// A Dolphin found running another game or an unpatched Wind Waker is
// still attached to: the window says what to boot, and the session plays
// as soon as that Dolphin runs the patched disc.
func DecideLaunch(o LaunchOptions, running []RunningDolphin) LaunchPlan {
	var mine []RunningDolphin
	for _, r := range running {
		if r.UserDir == "" || o.UserDir == "" || SamePath(r.UserDir, o.UserDir) {
			mine = append(mine, r)
		}
	}
	if r, ok := pickDolphin(mine); ok {
		return LaunchPlan{Kind: PlanAttach, PID: r.PID, Why: "a Dolphin is already running on your user folder"}
	}
	if o.Manual {
		for _, r := range running {
			if r.Claimed || (r.Explicit && r.UserDir != "" && o.UserDir != "" && !SamePath(r.UserDir, o.UserDir)) {
				continue // another player's
			}
			if r.State == DolphinTitle || r.State == DolphinInGame || r.State == DolphinUnpatched {
				return LaunchPlan{Kind: PlanAttach, PID: r.PID, Why: "you start Dolphin yourself, and this one runs Wind Waker"}
			}
		}
		return LaunchPlan{Kind: PlanWait, Why: "you start Dolphin yourself"}
	}
	return LaunchPlan{Kind: PlanLaunch, Why: "no Dolphin runs on your user folder"}
}

// pickDolphin is the Dolphin to attach to among rs: the first running
// Wind Waker (patched first), else the first.
func pickDolphin(rs []RunningDolphin) (RunningDolphin, bool) {
	for _, want := range []func(DolphinState) bool{
		DolphinState.Playable,
		func(s DolphinState) bool { return s == DolphinUnpatched },
		func(DolphinState) bool { return true },
	} {
		for _, r := range rs {
			if want(r.State) {
				return r, true
			}
		}
	}
	return RunningDolphin{}, false
}

// SamePath says a and b name one folder: cleaned, without a trailing
// separator, and without case (Windows' and macOS's default file systems
// ignore it).
func SamePath(a, b string) bool {
	norm := func(p string) string {
		p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"`))
		p = filepath.Clean(strings.ReplaceAll(p, `\`, "/"))
		return strings.ToLower(strings.TrimRight(p, "/"))
	}
	return norm(a) == norm(b)
}

// DolphinInfo is State.Dolphin: this player's Dolphin in a lobby session
// once the host pressed Start.
type DolphinInfo struct {
	State DolphinState
	// PID is the Dolphin played on; 0 before one was found or launched.
	PID uint32
	// Launched says the app started it (an attached one was running).
	Launched bool
	// Manual is LaunchOptions.Manual.
	Manual bool
	// GameID is the game a wrong-game Dolphin runs.
	GameID string
	// ISO is the patched disc the window tells a wrong-game or unpatched
	// Dolphin to boot.
	ISO string
	// Err is why the launch failed ("" for none).
	Err string
	// LaunchAttempt counts the app's launches (and launch failures) of
	// this player's Dolphin: each failure is a new dialog.
	LaunchAttempt int
	// Pipelines says the pose pipelines run on it.
	Pipelines bool
}

// ErrNotReady is returned by Start while someone isn't ready: the host
// may StartAnyway.
var ErrNotReady = errors.New("not everyone is ready")

// NotReady lists the lobby's members, other than this player, who are not
// ready (an away member counts as not ready). Start waits for them;
// StartAnyway doesn't. The host's own ready flag is not asked for:
// pressing Start says it.
func (l Lobby) NotReady() []LobbyMember {
	var out []LobbyMember
	for _, m := range l.Members {
		if m.ID == l.Member && m.Via == ViaApp {
			continue
		}
		if !m.Ready || m.Away {
			out = append(out, m)
		}
	}
	return out
}
