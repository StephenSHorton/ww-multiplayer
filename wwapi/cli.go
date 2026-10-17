package wwapi

import "io"

// Command is one CLI subcommand a mod adds through the CLI capability
// (design §6.1). The app lists it with its own commands: printHelp shows
// it under Group unless it is Hidden, and `<exe> <Name> args...` runs it.
type Command struct {
	// Name is what the user types after the binary; Aliases run the same
	// command.
	Name    string
	Aliases []string
	// Hidden keeps the command out of the help (diagnostics and developer
	// tools, Group GroupDev).
	Hidden bool
	// Group is the help section the command is listed under: one of the
	// Group* labels.
	Group string
	// Usage (after the binary) and Summary are the help's line for a
	// listed command; a hidden one leaves them empty.
	Usage, Summary string
	// Run runs the command with its name (os.Args[1]) and the arguments
	// after it (os.Args[2:]). It may print and exit the process, as the
	// app's own commands do.
	Run func(name string, args []string)
}

// The help sections a Command's Group names.
const (
	GroupPlay  = "play"  // Play multiplayer
	GroupPatch = "patch" // Patch an ISO
	GroupLocal = "local" // Local two-Dolphin testing
	GroupLower = "lower" // Lower-level CLIs
	GroupDev   = "dev"   // diagnostics and developer tools (hidden)
)

// More optional Mod interfaces (see mod.go).
type (
	// CLI is a mod that adds CLI subcommands. The app appends a loaded
	// mod's commands to its own, in load order.
	CLI interface{ Commands() []Command }

	// PostPatcher is a mod with a step to run after `patch` (and the
	// app's setup) has written or found the patched disc image: image is
	// the disc Dolphin will run. The step writes at most a line or two on
	// w and never fails the patch (it has no error to return), so it
	// reports a problem as a warning line. Loaded mods' steps run in load
	// order.
	PostPatcher interface {
		PostPatch(w io.Writer, image string)
	}
)
