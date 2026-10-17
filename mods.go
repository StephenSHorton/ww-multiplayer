package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// compiledMods is the static list of mods built into this app (design
// §6.1: no init() registration, no drop-in loading): the multiplayer mod,
// whose engine is the app's (newTUIEngine; the faces still build their
// own the same way until D4), and co-op, which requires it and reaches
// Dolphin through coopEnv. D7 moves the list to cmd/ww-multiplayer/mods.go.
func compiledMods() []wwapi.Mod {
	return []wwapi.Mod{
		multiplayer.New(func() session.Engine { return newTUIEngine() }),
		coop.New(coopEnv),
	}
}

// loadedMods are the compiled-in mods that load, in load order, resolved
// as `mods` resolves them (manifest defaults: the CLI never reads the
// app's settings). Nothing attaches to Dolphin or runs a mod's Init.
func loadedMods() []wwapi.Mod {
	p := wwapi.Resolve(compiledMods(), wwapi.ResolveOptions{})
	mods := make([]wwapi.Mod, 0, len(p.Order))
	for _, i := range p.Order {
		mods = append(mods, p.Entries[i].Mod)
	}
	return mods
}

// modCommands are the loaded mods' CLI commands (wwapi.CLI), in load
// order, as registry entries.
func modCommands() []command {
	var cs []command
	for _, m := range loadedMods() {
		c, ok := m.(wwapi.CLI)
		if !ok {
			continue
		}
		for _, mc := range c.Commands() {
			cs = append(cs, command{name: mc.Name, aliases: mc.Aliases, group: modGroup(mc.Group), hidden: mc.Hidden,
				usage: mc.Usage, summary: mc.Summary, run: mc.Run})
		}
	}
	return cs
}

// modGroup is the help section a mod command's group label names; an
// unknown label is the lower-level CLIs.
func modGroup(label string) cmdGroup {
	switch label {
	case wwapi.GroupPlay:
		return groupPlay
	case wwapi.GroupPatch:
		return groupPatch
	case wwapi.GroupLocal:
		return groupLocal
	case wwapi.GroupDev:
		return groupDev
	}
	return groupLower
}

// postPatchSteps are the loaded mods' post-patch steps
// (wwapi.PostPatcher), in load order: patch.Options.PostPatch for `patch`
// and the app's setup. Co-op's builds the stage tables.
func postPatchSteps() []func(w io.Writer, image string) {
	var steps []func(io.Writer, string)
	for _, m := range loadedMods() {
		if p, ok := m.(wwapi.PostPatcher); ok {
			steps = append(steps, p.PostPatch)
		}
	}
	return steps
}

const modsUsage = "mods [--json]"

// runMods is the `mods` command: it resolves the compiled-in mods the way
// the CLI would load them (manifest defaults: the CLI never reads the
// app's settings) and lists them with their status. It does not attach
// to Dolphin or run any mod's Init, so LOADED here means "would load".
func runMods(args []string) {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			exitUsage(fmt.Errorf("unknown argument %q", a), modsUsage)
		}
		asJSON = true
	}
	infos := wwapi.Resolve(compiledMods(), wwapi.ResolveOptions{}).Infos()
	if asJSON {
		b, err := json.MarshalIndent(infos, "", "  ")
		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(b))
		return
	}
	fmt.Println(modsText(infos))
}

// modsText is the plain listing: one line per mod, or a note when there
// are none.
func modsText(infos []wwapi.ModInfo) string {
	if len(infos) == 0 {
		return "No mods are built into this version (wwapi " + wwapi.Version + ")."
	}
	s := fmt.Sprintf("Mods (wwapi %s):", wwapi.Version)
	for _, mi := range infos {
		id := mi.ID
		if id == "" {
			id = "(no id)"
		}
		s += fmt.Sprintf("\n  %-24s %-12s %s", id, mi.Version, mi.StatusString())
	}
	return s
}
