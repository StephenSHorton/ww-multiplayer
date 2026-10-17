package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// compiledMods is the static list of mods built into this app (design
// §6.1: no init() registration, no drop-in loading): the multiplayer mod,
// whose engine is the app's (newTUIEngine; the faces still build their
// own the same way until D4). Co-op joins it in D2; D7 moves the list to
// cmd/ww-multiplayer/mods.go.
func compiledMods() []wwapi.Mod {
	return []wwapi.Mod{
		multiplayer.New(func() session.Engine { return newTUIEngine() }),
	}
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
