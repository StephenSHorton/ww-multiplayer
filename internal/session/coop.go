package session

// Co-op through the controller (docs/08-coop-design.md; App PR 2): the
// session's co-op choices, the yes/no questions its pipelines ask, the
// advice they give, and the console commands (/refresh, /warp, /coop
// off|on, /status, /help) as commands.

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// CoopWorldNew is CoopOptions.World for founding a new world from the
// host's loaded file (--coop=new).
const CoopWorldNew = "new"

// CoopOptions are a session's co-op choices, the CLI's --coop flags. The
// zero value is co-op off. A joiner leaves World, Merge and LiveSwitches
// unset: they are the host's to choose.
type CoopOptions struct {
	On bool
	// World is the world a host serves: "" for the only one on disk,
	// CoopWorldNew, or a world ID (16 hex digits).
	World        string
	Merge        bool   // --coop-merge
	LiveSwitches bool   // --coop-live-switches
	DryRun       bool   // --coop-dry-run
	ISO          string // --coop-iso; "" for the disc patch last wrote
	// Yes answers every question yes at once (--yes).
	Yes bool
	// BossKill is --coop-boss-kill, the boss-dead relay's row set; "" is
	// its default (gohma,kalle,molgera,jalhalla,helmaroc). Refresh is --coop-refresh, the
	// actor_refresh relay's token set; "" is off. Each side's own.
	BossKill string
	Refresh  string
}

// QuestionKind says what a yes would write.
type QuestionKind string

const (
	// QuestionBind: bind the loaded save file to the session's world.
	QuestionBind QuestionKind = "bind"
	// QuestionScrub: zero a co-op tag the file was never bound with.
	QuestionScrub QuestionKind = "scrub"
)

// Question is one yes/no question a co-op pipeline asks. Nothing is
// written until it is answered yes; no answer (the session ending, or the
// UI going away) is a no.
type Question struct {
	// ID is set by the Controller when the question is asked: "q1",
	// "q2", ... It names the question in Answer.
	ID   string
	Kind QuestionKind
	// Player is the player whose Dolphin asks (mp-local runs two).
	Player string
	// Title is the question in one line, e.g. "Bind this save file to
	// co-op world 0123456789abcdef?".
	Title string
	// Body is the whole question as the CLI prints it, several lines
	// with the question last.
	Body string
	// Details are a bind's preflight lines: the verdict, what the file
	// and the world add to each other by class, and the holds. Empty for
	// a scrub.
	Details []string
	// FileName and FileSlot (1-3, as the file select screen numbers it)
	// name the loaded file the question is about; FileSlot is 0 when
	// unknown.
	FileName string
	FileSlot int
	// World is the world a bind binds the file to; TagWorld the world
	// the file's current tag names ("" for none).
	World, TagWorld string
	// MaybeFresh says the current tag fails only V5: it may be a fresh
	// bind of TagWorld that only that world's host can tell from a leak.
	MaybeFresh bool
	// Failed are a scrub's failed checks, one line each ("V2 failed:
	// ...").
	Failed []string
}

// AdviceKind names a piece of co-op advice.
type AdviceKind string

const (
	// AdviceFarBehind: the world is far ahead of the file; copy the
	// host's save in, or the host runs with --coop-merge.
	AdviceFarBehind AdviceKind = "far-behind"
	// AdviceNeedsMerge: the file can't bind unless the host runs with
	// --coop-merge.
	AdviceNeedsMerge AdviceKind = "needs-merge"
	// AdviceNoBackup: no memory-card backup this session, so no tag is
	// written.
	AdviceNoBackup AdviceKind = "no-backup"
)

// Advice is co-op advice that asks nothing: Text is the line the CLI
// prints for it.
type Advice struct {
	// ID is set by the Controller when the advice is given: "a1", "a2",
	// ... It names the advice in DismissAdvice.
	ID     string
	Kind   AdviceKind
	Player string
	Text   string
}

// Verb is a console command.
type Verb string

const (
	VerbHelp    Verb = "help"     // the command list
	VerbRefresh Verb = "refresh"  // reload the area by the entrance Link came in by
	VerbWarp    Verb = "warp"     // go to the partner
	VerbCoopOff Verb = "coop off" // stop writing the world into the game
	VerbCoopOn  Verb = "coop on"  // resume it
	VerbStatus  Verb = "status"   // where co-op is
)

// Command is one console command. Player picks the Dolphin when the
// session runs two; "" is the only one, or, for /coop and /status, all.
type Command struct {
	Verb   Verb
	Player string
}

// CoopWorld is one co-op world on disk, as a host's world choice lists
// it.
type CoopWorld struct {
	ID      string // 16 hex digits
	Created time.Time
	Seq     uint64
	// Text is the world's line in `coop-world list`.
	Text string
	// Err is why the world can't be loaded; "" when it can.
	Err string
	// InUse says its session marker names a process that may still
	// serve it; Stale that its marker names a process that has ended
	// (its last session did not end cleanly).
	InUse, Stale bool
}

// CoopStatus is one co-op pipeline's status, as /status reports it.
type CoopStatus struct {
	Player string
	// State is the replica's state, with why when it says ("off: not
	// connected").
	State string
	World string // the world's ID
	Seq   uint64
	// Writes says whether it writes: "writing", "a dry run
	// (--coop-dry-run)" or "writes off: <why>".
	Writes                   string
	Written, Escapes, Failed int
	// Lines are /status's lines for the pipeline, as the CLI prints
	// them.
	Lines []string

	// The same, structured for a panel (App PR 9).
	//
	// StateCode is the replica's state alone: "off", "hello", "unbound",
	// "armed", "offline" or "refused"; Why its reason.
	StateCode, Why string
	DryRun         bool
	// WritesOff is why the writes are off ("" while they run, and in a
	// dry run).
	WritesOff string
	// FileState is the loaded file's tag as the replica last judged it:
	// "bound", "unbound", "leaked" or "foreign" ("" before a look);
	// TagWorld the world its tag names.
	FileState, TagWorld string
	// NeedsMerge and FarBehind are the last join preflight's (an unbound
	// file): it binds only with the host's merge, and it is far behind
	// the world.
	NeedsMerge, FarBehind bool
	// Progression, Puzzle, Combat and Tuner are /status's lines on the
	// shared progression (one line each: granted, held story bits, small
	// keys, max hearts), the room puzzles, combat and the Tingle Tuner
	// guard ("" for none).
	Progression           []string
	Puzzle, Combat, Tuner string
	// Shares, HeldStory, Keys, Life and LifeWhy are the Progression
	// lines' facts, structured for the app's progress card: the session
	// features the host granted by name ("story", "keys", ...; none in a
	// dry run), the story bits held back with why ("label (why)"), each
	// key table, and whether max hearts are derived (LifeWhy: why not).
	// NoKeyTable: no small keys are derived at all, as /status's "small
	// keys: not derived (no co-op stage tables)" says.
	Shares     []string
	HeldStory  []string
	Keys       []CoopKeys
	NoKeyTable bool
	Life       bool
	LifeWhy    string
	// Partner is where the partner is (its last PRESENCE): Stage "" for
	// unknown, Room -1 at a stage start.
	PartnerStage string
	PartnerRoom  int
}

// CoopKeys is one key table's small keys as the replica's Deriver last
// saw them (derive.KeyStatus, flattened): Dungeon its short name ("DRC";
// "" for none), On whether its keys are derived (Why: why not), Seen
// whether the loaded file has been measured; the counts mean nothing
// before. Target is the count the table should hold, Pending a key write
// in flight.
type CoopKeys struct {
	Table                     int
	Dungeon                   string
	On, Seen, Pending         bool
	Why                       string
	Have, Taken, Sources      int
	Open, Doors, Held, Target int
}

// CoopHubStatus is a host's hub: the world it serves and its peers.
type CoopHubStatus struct {
	World    string
	Seq      uint64
	Features string // what the host allows
	Merge    bool   // files with progress the world lacks may bind
	Peers    []CoopPeer
	// Lines are /status's hub lines (puzzles, combat).
	Lines []string
}

// CoopPeer is one co-op connection of the hub.
type CoopPeer struct {
	Name   string
	Host   bool
	State  string // "hello", "refused", "unbound" or "bound"
	File   string // the file state its hello reported
	Reason string // why it is refused or unbound
	Stage  string // where it is ("" for unknown)
	Room   int
}

// CoopEngine is what an Engine with co-op adds: the console, and the
// worlds a host may serve. The Controller finds it by a type assertion,
// so an Engine without co-op need not implement it.
type CoopEngine interface {
	// Command runs one console command on the running session's co-op
	// side. Its outcome is logged, as the CLI's is. It returns
	// ErrNoCoop when the session runs without co-op, an error for a
	// malformed command, and the error that kept the command from
	// every pipeline (none runs yet, no such player, which player?),
	// which is also logged. What a pipeline made of the command is in
	// the log and its CoopStatus only.
	Command(c Command) error
	// CommandLine runs one console line as typed ("/warp Tetra"),
	// logging a line that is no command the way the CLI does.
	CommandLine(line string) error
	// CoopWorlds lists the co-op worlds on disk, newest first.
	CoopWorlds() ([]CoopWorld, error)
}

var (
	// ErrNoCoop is returned by the co-op commands when the session (or
	// the engine) has no co-op side.
	ErrNoCoop = errors.New("co-op is not on in this session")
	// ErrNoQuestion is returned by Answer for a question that is not
	// open (answered already, or its session ended).
	ErrNoQuestion = errors.New("no such open question")
	// ErrReported wraps an engine's returned error that the session
	// already logged (a fatal connect error): the Controller does not
	// log it again, but still counts the session as failed.
	ErrReported = errors.New("already reported")
)

// Answer answers the open question id: true is yes.
func (c *Controller) Answer(id string, yes bool) error {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if !ok {
		return ErrNoQuestion
	}
	ch <- yes // buffered: the asker takes it, or has gone
	return nil
}

// Command runs one console command on the running session.
func (c *Controller) Command(cmd Command) error {
	ce, err := c.coopEngine()
	if err != nil {
		return err
	}
	return ce.Command(cmd)
}

// CommandLine runs one console line as typed ("/warp Tetra").
func (c *Controller) CommandLine(line string) error {
	ce, err := c.coopEngine()
	if err != nil {
		return err
	}
	return ce.CommandLine(line)
}

// CoopWorlds lists the co-op worlds on disk, for a host's choice. It
// needs no running session.
func (c *Controller) CoopWorlds() ([]CoopWorld, error) {
	ce, ok := c.engine.(CoopEngine)
	if !ok {
		return nil, ErrNoCoop
	}
	return ce.CoopWorlds()
}

func (c *Controller) coopEngine() (CoopEngine, error) {
	c.mu.Lock()
	running, on := c.cur != nil, c.state.Coop.On
	c.mu.Unlock()
	if !running {
		return nil, ErrNoSession
	}
	ce, ok := c.engine.(CoopEngine)
	if !ok || !on {
		return nil, ErrNoCoop
	}
	return ce, nil
}

// ask is Output.Ask for run id: it opens q, publishes EventQuestion and
// waits for Answer, or for ctx to end, which is a no. EventQuestionClosed
// follows either way.
func (c *Controller) ask(ctx context.Context, id ID, q Question) bool {
	ch := make(chan bool, 1)
	c.mu.Lock()
	c.nextQ++
	q.ID = "q" + strconv.FormatUint(c.nextQ, 10)
	c.pending[q.ID] = ch
	if c.state.Session == id {
		c.state.Questions = append(c.state.Questions, q)
		if c.state.AutoYes {
			ch <- true // "Answer yes to co-op questions" (SetAutoYes)
		}
	}
	c.publish(Event{Kind: EventQuestion, Session: id, Question: q})
	c.mu.Unlock()

	var yes bool
	select {
	case yes = <-ch:
	case <-ctx.Done():
	}

	c.mu.Lock()
	delete(c.pending, q.ID)
	if c.state.Session == id {
		qs := c.state.Questions[:0:0]
		for _, o := range c.state.Questions {
			if o.ID != q.ID {
				qs = append(qs, o)
			}
		}
		c.state.Questions = qs
	}
	c.publish(Event{Kind: EventQuestionClosed, Session: id, Question: q, Yes: yes})
	c.mu.Unlock()
	return yes
}
