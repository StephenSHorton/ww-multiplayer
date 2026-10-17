package main

// The co-op console (#23; docs/08-coop-design.md §6.7, §9 M3): the
// commands a --coop session takes. The CLI types them on stdin, read by
// the same reader as the bind and scrub answers (coopPrompter), which
// hands it every line that starts with '/'; a UI gives them to the session
// controller (session.Controller.Command and CommandLine), which runs them
// here through sessionEngine (App PR 2). Both reach exec:
//
//	/refresh [player]       reload the area, by the entrance Link came in by
//	/warp [player]          go to the partner: same stage, onto it;
//	                        another stage, to its entrance, then onto it
//	/coop off|on [player]   stop, or resume, writing the world into the game
//	/status [player]        where co-op is (on a host, its hub's peers
//	                        and scoped stores too)
//	/help                   these lines
//
// player picks a Dolphin when the session runs two (mp-local); it may be
// left out when there is one, and /coop and /status without it act on all.
// The escapes are made by the replica on its next tick, behind the gates
// and the WriteGuard (replica.Replica.Escape): the console only asks and
// says what came of it.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// coopCommand is one parsed console line.
type coopCommand struct {
	verb string // "help", "refresh", "warp", "coop off", "coop on", "status"
	who  string // the player it is for, "" for the only one (or all)
}

// commandOf is the console command for a controller command, checked as
// a typed line is: an unknown verb or a bad player name is refused.
func commandOf(c session.Command) (coopCommand, error) {
	line := "/" + string(c.Verb)
	if c.Player != "" {
		line += " " + c.Player
	}
	cmd, err := parseCoopCommand(line)
	if err != nil {
		return coopCommand{}, err
	}
	if cmd.verb != string(c.Verb) || cmd.who != c.Player {
		return coopCommand{}, fmt.Errorf("no command %q for %q", c.Verb, c.Player)
	}
	return cmd, nil
}

// coopCommandHelp is /help's text, one command a line.
var coopCommandHelp = []string{
	"/refresh [player]      reload this area by the entrance Link came in by (a stale chest or door, or a softlock)",
	"/warp [player]         go to your partner: onto it on the same stage, else by its entrance first",
	"/coop off|on [player]  stop, or resume, writing the world into the game; pose sync goes on either way",
	"/status [player]       where co-op is",
	"/help                  this list",
}

// isCoopCommand reports whether a line typed is a console command rather
// than an answer to a question.
func isCoopCommand(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "/") }

// maxCommandLen bounds a console line: anything longer is no command.
const maxCommandLen = 200

// parseCoopCommand parses a console line.
func parseCoopCommand(line string) (coopCommand, error) {
	line = strings.TrimSpace(line)
	if len(line) > maxCommandLen {
		return coopCommand{}, fmt.Errorf("a %d-byte command: /help lists them", len(line))
	}
	f := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(f) == 0 {
		return coopCommand{}, errors.New("an empty command: /help lists them")
	}
	verb, args := strings.ToLower(f[0]), f[1:]
	switch verb {
	case "help", "?":
		if len(args) > 0 {
			return coopCommand{}, errors.New("/help takes nothing more")
		}
		return coopCommand{verb: "help"}, nil
	case "refresh", "warp", "status":
	case "coop":
		if len(args) == 0 {
			return coopCommand{}, errors.New("/coop off, or /coop on")
		}
		switch on := strings.ToLower(args[0]); on {
		case "off", "on":
			verb, args = "coop "+on, args[1:]
		default:
			return coopCommand{}, fmt.Errorf("/coop %s: say /coop off or /coop on", args[0])
		}
	default:
		return coopCommand{}, fmt.Errorf("no command /%s: /help lists them", f[0])
	}
	switch len(args) {
	case 0:
		return coopCommand{verb: verb}, nil
	case 1:
		if err := network.ValidatePlayerName(args[0]); err != nil {
			return coopCommand{}, fmt.Errorf("/%s %s: %v", verb, args[0], err)
		}
		return coopCommand{verb: verb, who: args[0]}, nil
	}
	return coopCommand{}, fmt.Errorf("/%s takes at most a player's name", verb)
}

// coopConsole runs the session's commands for its coop-sync pipelines.
type coopConsole struct {
	rep  report.Reporter
	cmds <-chan promptLine

	mu    sync.Mutex
	syncs []*coopSync
	once  sync.Once
	told  bool
	// ctx is the session's, from the first add: an escape's answer is
	// awaited under it. Nil before any pipeline runs.
	ctx context.Context
	// hubSt is the host's hub's Status, for /status's host lines; nil on
	// a joiner.
	hubSt func() (hub.Status, error)
}

// setHub hands the console the host's hub's Status.
func (c *coopConsole) setHub(status func() (hub.Status, error)) {
	c.mu.Lock()
	c.hubSt = status
	c.mu.Unlock()
}

func newCoopConsole(rep report.Reporter, cmds <-chan promptLine) *coopConsole {
	return &coopConsole{rep: rep, cmds: cmds}
}

// help says, once per session, how to use the console.
func (c *coopConsole) help() {
	c.mu.Lock()
	told := c.told
	c.told = true
	c.mu.Unlock()
	if told {
		return
	}
	c.rep.Log(report.Info, "co-op: commands (type one and press Enter): /refresh reloads this area, /warp takes you to your partner, /coop off|on stops or resumes co-op writes, /status, /help. With two Dolphins, add the player's name: /warp <name>.")
}

// add hands the console a pipeline, and starts the console's loop with
// the first one, under the session's ctx: the one setCtx gave, else ctx.
func (c *coopConsole) add(ctx context.Context, s *coopSync) {
	c.mu.Lock()
	c.syncs = append(c.syncs, s)
	if c.ctx == nil {
		c.ctx = ctx
	}
	run := c.ctx
	c.mu.Unlock()
	c.once.Do(func() { go c.run(run) })
}

// setCtx makes ctx the session's context, before any pipeline runs: the
// app's coop-sync runs, and restarts, under contexts of their own, which
// end before the session does.
func (c *coopConsole) setCtx(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
}

// remove takes a pipeline that stopped from the console: the app starts
// coop-sync again (a Dolphin relaunched, an error), and the commands, the
// status and "Remove the tag" are for the running one only.
func (c *coopConsole) remove(s *coopSync) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncs = slices.DeleteFunc(c.syncs, func(x *coopSync) bool { return x == s })
}

// run takes the commands until ctx ends or stdin closes.
func (c *coopConsole) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-c.cmds:
			if !ok {
				return
			}
			c.do(ctx, l.text)
		}
	}
}

// do runs one command line.
func (c *coopConsole) do(ctx context.Context, line string) {
	cmd, err := parseCoopCommand(line)
	if err != nil {
		report.Logf(c.rep, report.Warn, "co-op: %v", err)
		return
	}
	_ = c.exec(ctx, cmd) // logged
}

// sessionCtx is the session's context, for a command given before (or
// without) one that came with it.
func (c *coopConsole) sessionCtx() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// exec runs one parsed command: a typed line's (do) or the controller's.
// It returns the error that kept the command from any pipeline (no
// Dolphin runs co-op yet, no such player, say which player), which it
// has also logged, as a typed line's always was. What a pipeline made
// of the command is only logged (and shows in its status).
func (c *coopConsole) exec(ctx context.Context, cmd coopCommand) error {
	if cmd.verb == "help" {
		for _, l := range coopCommandHelp {
			c.rep.Log(report.Info, "co-op:   "+l)
		}
		return nil
	}
	ss, err := c.pick(cmd)
	if err != nil {
		report.Logf(c.rep, report.Warn, "co-op: %v", err)
	}
	for _, s := range ss {
		s.command(ctx, cmd)
	}
	if cmd.verb == "status" {
		// A host's hub, whether or not a pipeline runs yet.
		c.mu.Lock()
		status := c.hubSt
		c.mu.Unlock()
		if status != nil {
			logHubStatus(c.rep, status)
			return nil // a host's /status answers with its hub's
		}
	}
	return err
}

// pick returns the pipelines cmd is for: the named one, the only one, or,
// for /coop and /status, all of them.
func (c *coopConsole) pick(cmd coopCommand) ([]*coopSync, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var names []string
	for _, s := range c.syncs {
		if cmd.who != "" && strings.EqualFold(s.name, cmd.who) {
			return []*coopSync{s}, nil
		}
		names = append(names, s.name)
	}
	switch {
	case len(c.syncs) == 0:
		return nil, fmt.Errorf("/%s: no Dolphin runs co-op yet", cmd.verb)
	case cmd.who != "":
		return nil, fmt.Errorf("/%s %s: no player %s here (%s)", cmd.verb, cmd.who, cmd.who, strings.Join(names, ", "))
	case len(c.syncs) == 1, cmd.verb == "coop off", cmd.verb == "coop on", cmd.verb == "status":
		return append([]*coopSync(nil), c.syncs...), nil
	}
	return nil, fmt.Errorf("/%s: say which player: /%s %s", cmd.verb, cmd.verb, strings.Join(names, " or /"+cmd.verb+" "))
}

// command runs cmd on s. An escape's answer comes on the replica's next
// tick: it is awaited on its own goroutine, so the console takes the next
// line meanwhile. The replica logs what an escape did; the console says
// why one was not made.
func (s *coopSync) command(ctx context.Context, cmd coopCommand) {
	switch cmd.verb {
	case "refresh", "warp":
		kind := replica.EscapeRefresh
		if cmd.verb == "warp" {
			kind = replica.EscapeWarp
		}
		ch := s.r.Escape(kind)
		go func() {
			select {
			case <-ctx.Done():
			case res := <-ch:
				switch {
				case res.Err == nil:
				case errors.Is(res.Err, replica.ErrNotNow):
					report.Logf(s.rep, report.Info, "co-op: /%s for %s: %v; try again in a moment", cmd.verb, s.name, res.Err)
				default:
					report.Logf(s.rep, report.Warn, "co-op: /%s for %s: %v", cmd.verb, s.name, res.Err)
				}
			}
		}()
	case "coop off":
		if st := s.r.Status(); st.DryRun || st.WritesOff != "" {
			report.Logf(s.rep, report.Info, "co-op: %s writes nothing already (%s)", s.name, writesState(st))
			return
		}
		s.r.TurnOff("co-op writes are off for " + s.name + ": you typed /coop off")
	case "coop on":
		if why := s.gaveUp(); why != "" {
			report.Logf(s.rep, report.Warn, "co-op: %s: /coop on cannot help: %s", s.name, why)
			return
		}
		st := s.r.Status()
		if st.DryRun || st.WritesOff == "" {
			report.Logf(s.rep, report.Info, "co-op: %s: nothing to turn on (%s)", s.name, writesState(st))
			return
		}
		s.r.TurnOn()
		report.Logf(s.rep, report.OK, "co-op: writes are on again for %s (they were off: %s)", s.name, st.WritesOff)
	case "status":
		for _, l := range formatStatus(s.name, s.r.Status()) {
			s.rep.Log(report.Info, l)
		}
	}
}

// replicaWhere is st's state, with why when it says.
func replicaWhere(st replica.Status) string {
	where := st.State.String()
	if st.Why != "" {
		where += ": " + st.Why
	}
	return where
}

// formatStatus is /status's text for player's pipeline at st, one line a
// log line: what the CLI prints, and what a UI copies for a bug report.
func formatStatus(player string, st replica.Status) []string {
	out := []string{fmt.Sprintf("co-op: %s: %s; world %s at seq %d; %s; %d bytes written, %d escapes, %d verify failures",
		player, replicaWhere(st), st.World, st.Seq, writesState(st), st.Writes.Written, st.Writes.Escapes, st.Writes.Failed)}
	lines := append(progressionStatus(st), puzzleStatus(st), combatStatus(st))
	if l := gbaStatus(st); l != "" {
		lines = append(lines, l)
	}
	for _, l := range lines {
		out = append(out, fmt.Sprintf("co-op: %s:   %s", player, l))
	}
	return out
}

// coopStatusOf is player's pipeline at st as a UI shows it: /status's
// lines, and the same structured for the app's co-op panel.
func coopStatusOf(player string, st replica.Status) session.CoopStatus {
	cs := session.CoopStatus{
		Player:      player,
		State:       replicaWhere(st),
		World:       st.World.String(),
		Seq:         st.Seq,
		Writes:      writesState(st),
		Written:     st.Writes.Written,
		Escapes:     st.Writes.Escapes,
		Failed:      st.Writes.Failed,
		Lines:       formatStatus(player, st),
		StateCode:   stateCode(st.State),
		Why:         st.Why,
		DryRun:      st.DryRun,
		WritesOff:   st.WritesOff,
		Progression: progressionStatus(st),
		Puzzle:      puzzleStatus(st),
		Combat:      combatStatus(st),
		Tuner:       gbaStatus(st),
		PartnerRoom: -1,
	}
	if st.World.IsZero() {
		cs.World = ""
	}
	if st.State != replica.StateOff {
		cs.FileState = st.Verdict.State.String()
		if st.Verdict.HasTag {
			cs.TagWorld = st.Verdict.Tag.World.String()
		}
	}
	if pf := st.Preflight; pf != nil {
		cs.NeedsMerge, cs.FarBehind = pf.NeedsMerge, pf.Verdict == replica.PreflightFarBehind
	}
	if p := st.Partner; p != nil && !p.Stage.IsEmpty() {
		cs.PartnerStage, cs.PartnerRoom = p.Stage.String(), int(p.Room)
	}
	return cs
}

// stateCode is a replica state as session.CoopStatus.StateCode names it.
func stateCode(s replica.State) string {
	if s == replica.StateOffline {
		return "offline"
	}
	return s.String()
}

// coopHubStatus is the host's hub as a UI shows it; nil without one.
func coopHubStatus(status func() (hub.Status, error)) *session.CoopHubStatus {
	if status == nil {
		return nil
	}
	hs, err := status()
	if err != nil {
		return nil
	}
	out := &session.CoopHubStatus{World: hs.World.String(), Seq: hs.Seq, Features: hs.Features.String(), Merge: hs.Merge}
	out.Lines = append(hubPuzzleStatus(hs), hubCombatStatus(hs)...)
	for _, p := range hs.Peers {
		cp := session.CoopPeer{Name: p.Name, Host: p.Host, State: p.State.String(), File: p.File.String(), Stage: p.Place.Stage, Room: p.Place.Room}
		if p.Reason != 0 {
			cp.Reason = p.Reason.String()
		}
		out.Peers = append(out.Peers, cp)
	}
	return out
}

// hubStatus is the console's hub Status, nil on a joiner.
func (c *coopConsole) hubStatus() func() (hub.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hubSt
}

// statuses is every pipeline's status, in the order they started.
func (c *coopConsole) statuses() []session.CoopStatus {
	c.mu.Lock()
	ss := append([]*coopSync(nil), c.syncs...)
	c.mu.Unlock()
	var out []session.CoopStatus
	for _, s := range ss {
		out = append(out, coopStatusOf(s.name, s.r.Status()))
	}
	return out
}

// gbaStatus is /status's line on the Tingle Tuner guard (§6.2, §6.3):
// whether a Game Boy Advance may be linked to this game, and the switches
// shared with Tingle Tuner switches that it holds back. "" when the stage
// tables give none.
func gbaStatus(st replica.Status) string {
	g := st.GBA
	switch {
	case g.Switches == 0:
		return ""
	case g.MayLink:
		return fmt.Sprintf("Tingle Tuner: a Game Boy Advance may be linked to this game, so the %d switch(es) shared with Tingle Tuner switches are neither proposed nor written until the game drops the link (a reset, or calling Tingle again)", g.Switches)
	}
	return fmt.Sprintf("Tingle Tuner: no Game Boy Advance linked; the %d switch(es) shared with Tingle Tuner switches sync", g.Switches)
}

// progressionStatus is /status's M4 lines for st (§9 M4): the classes the
// host grants, the story bits the holds keep out of the game and why, and
// with writes each key table's small keys and max life as the Deriver
// last saw them. A dry run, M2's replica, derives nothing.
func progressionStatus(st replica.Status) []string {
	var held []string
	for _, h := range st.Held {
		if replica.CategoryOf(int(h.Off)) != replica.CatStory {
			continue
		}
		for b := range 8 {
			if h.Mask&(1<<b) != 0 {
				held = append(held, fmt.Sprintf("%s (%s)", replica.DefaultLabel(int(h.Off), b), h.Hold.Why))
			}
		}
	}
	story := "story bits held back: none"
	if len(held) > 0 {
		story = "story bits held back until this game catches up: " + strings.Join(held, ", ")
	}
	if st.DryRun {
		return []string{story}
	}
	out := []string{"progression granted: " + (st.Features & hub.M4Features).String(), story}
	d := st.Derive
	if d.NoKeyTable {
		out = append(out, "small keys: not derived (no co-op stage tables)")
	}
	for _, k := range d.Keys {
		if k.On || k.Dungeon != "" {
			out = append(out, "small keys of "+k.String())
		}
	}
	if d.Life {
		out = append(out, "max hearts: derived")
	} else {
		out = append(out, "max hearts: not derived: "+d.LifeWhy)
	}
	return out
}

// writesState says whether st's replica writes.
func writesState(st replica.Status) string {
	switch {
	case st.DryRun:
		return "a dry run (--coop-dry-run)"
	case st.WritesOff != "":
		return "writes off: " + st.WritesOff
	}
	return "writing"
}
