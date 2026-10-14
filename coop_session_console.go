package main

// The co-op console (#23; docs/08-coop-design.md §6.7, §9 M3): the
// commands a --coop session takes on stdin, read by the same reader as the
// bind and scrub answers (coopPrompter), which hands it every line that
// starts with '/':
//
//	/refresh [player]       reload the area, by the entrance Link came in by
//	/warp [player]          go to the partner: same stage, to its side;
//	                        another stage, to its entrance, then its side
//	/coop off|on [player]   stop, or resume, writing the world into the game
//	/status [player]        where co-op is
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
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// coopCommand is one parsed console line.
type coopCommand struct {
	verb string // "help", "refresh", "warp", "coop off", "coop on", "status"
	who  string // the player it is for, "" for the only one (or all)
}

// coopCommandHelp is /help's text, one command a line.
var coopCommandHelp = []string{
	"/refresh [player]      reload this area by the entrance Link came in by (a stale chest or door, or a softlock)",
	"/warp [player]         go to your partner: to its side on the same stage, else to its entrance first",
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
// the first one, under ctx, the session's.
func (c *coopConsole) add(ctx context.Context, s *coopSync) {
	c.mu.Lock()
	c.syncs = append(c.syncs, s)
	c.mu.Unlock()
	c.once.Do(func() { go c.run(ctx) })
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
	if cmd.verb == "help" {
		for _, l := range coopCommandHelp {
			c.rep.Log(report.Info, "co-op:   "+l)
		}
		return
	}
	ss, err := c.pick(cmd)
	if err != nil {
		report.Logf(c.rep, report.Warn, "co-op: %v", err)
		return
	}
	for _, s := range ss {
		s.command(ctx, cmd)
	}
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
		st := s.r.Status()
		where := st.State.String()
		if st.Why != "" {
			where += ": " + st.Why
		}
		report.Logf(s.rep, report.Info, "co-op: %s: %s; world %s at seq %d; %s; %d bytes written, %d escapes, %d verify failures",
			s.name, where, st.World, st.Seq, writesState(st), st.Writes.Written, st.Writes.Escapes, st.Writes.Failed)
	}
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
