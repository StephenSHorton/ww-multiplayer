package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop"
	"github.com/StephenSHorton/ww-multiplayer/mods/example"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// compiledMods is the static list of mods built into this app (design
// §6.1: no init() registration, no drop-in loading): the multiplayer mod,
// whose engine is the app's (newTUIEngine), and co-op, which requires it
// and reaches Dolphin through coopEnv. The CLI resolves this list (its
// commands, post-patch steps and `mods`); the app's faces run it under a
// wwapi.Runtime with an engine that knows whether co-op loaded (startApp
// in app_face.go: modList with newAppEngine, engine_config.go). D7 moves the list to
// cmd/ww-multiplayer/mods.go.
func compiledMods() []wwapi.Mod {
	return compiledModsWith(func() session.Engine { return newTUIEngine() })
}

// compiledModsWith is compiledMods with the multiplayer mod's engine built
// by newEngine (on the mod's first Engine call).
func compiledModsWith(newEngine func() session.Engine) []wwapi.Mod {
	return []wwapi.Mod{
		multiplayer.New(newEngine),
		coop.New(coopEnv),
	}
}

// devMods are the dev mods (manifest "dev": true), compiled in but listed
// only with --dev: the example mod (docs/api/03-writing-a-mod.md).
func devMods() []wwapi.Mod {
	return []wwapi.Mod{example.New()}
}

// modList is compiledMods, plus devMods with --dev.
func modList(dev bool, newEngine func() session.Engine) []wwapi.Mod {
	mods := compiledModsWith(newEngine)
	if dev {
		mods = append(mods, devMods()...)
	}
	return mods
}

// loadedMods are the compiled-in mods that load, in load order, resolved
// as `mods` resolves them (manifest defaults: the CLI never reads the
// app's settings). Nothing attaches to Dolphin or runs a mod's Init.
func loadedMods() []wwapi.Mod {
	return planMods(wwapi.Resolve(compiledMods(), wwapi.ResolveOptions{}))
}

// planMods are a plan's mods that load, in load order.
func planMods(p *wwapi.Plan) []wwapi.Mod {
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
// (wwapi.PostPatcher), in load order: patch.Options.PostPatch for `patch`.
// Co-op's builds the stage tables. The app's setup takes its own
// runtime's (patchSteps(runtimeMods(rt)) in guiOptions, app_face.go).
func postPatchSteps() []func(w io.Writer, image string) {
	return patchSteps(loadedMods())
}

func patchSteps(mods []wwapi.Mod) []func(w io.Writer, image string) {
	var steps []func(io.Writer, string)
	for _, m := range mods {
		if p, ok := m.(wwapi.PostPatcher); ok {
			steps = append(steps, p.PostPatch)
		}
	}
	return steps
}

const modsUsage = "mods [--json] [--dev] [--mods=a,b] [--no-mod=x]"

// runMods is the `mods` command: it resolves the compiled-in mods the way
// the CLI would load them (manifest defaults: the CLI never reads the
// app's settings), or, with the app's mod flags (app_face.go: --mods,
// --no-mod, --dev), the way the app would with nothing saved, and lists
// them with their status. It does not attach to Dolphin or run any mod's
// Init, so LOADED here means "would load".
func runMods(args []string) {
	mf, rest, err := parseModFlags(args)
	if err != nil {
		exitUsage(err, modsUsage)
	}
	asJSON := false
	for _, a := range rest {
		if a != "--json" {
			exitUsage(fmt.Errorf("unknown argument %q", a), modsUsage)
		}
		asJSON = true
	}
	if err := mf.check(modList(true, nil)); err != nil {
		exitUsage(err, modsUsage)
	}
	mods := modList(mf.dev, func() session.Engine { return newTUIEngine() })
	infos := wwapi.Resolve(mods, mf.resolveOptions()).Infos()
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
