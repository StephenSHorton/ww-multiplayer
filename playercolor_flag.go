package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
)

// colorPref is the tunic color this player asks the server for
// (docs/09-player-colors.md): WW_COLOR, overridden by a --color option
// anywhere before a bare "--". nil lets the server pick.
var colorPref *playercolor.Color

// initColorPref reads WW_COLOR and strips any --color option from args,
// returning the remaining args. A bad value is an error rather than a
// silent fallback, so a typo doesn't quietly give the player another color.
func initColorPref(args []string, env string) (*playercolor.Color, []string, error) {
	var pref *playercolor.Color
	if env != "" {
		c, err := playercolor.Parse(env)
		if err != nil {
			return nil, args, fmt.Errorf("WW_COLOR: %w", err)
		}
		pref = &c
	}
	flag, rest, err := extractColorFlag(args)
	if err != nil {
		return nil, args, err
	}
	if flag != nil {
		pref = flag
	}
	return pref, rest, nil
}

// extractColorFlag removes a "--color X" pair or a "--color=X" arg (also
// spelled with one dash) from args and parses it. Options end at a bare
// "--", which is kept, so a player name after it is never taken as the
// option. The last --color wins.
func extractColorFlag(args []string) (*playercolor.Color, []string, error) {
	var pref *playercolor.Color
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "-color") {
			a = "-" + a
		}
		var val string
		switch {
		case a == "--color":
			if i+1 >= len(args) {
				return nil, args, errors.New("--color needs a value, e.g. --color red or --color #FF8000")
			}
			i++
			val = args[i]
		case strings.HasPrefix(a, "--color="):
			val = strings.TrimPrefix(a, "--color=")
		default:
			rest = append(rest, args[i])
			continue
		}
		c, err := playercolor.Parse(val)
		if err != nil {
			return nil, args, fmt.Errorf("--color: %w", err)
		}
		pref = &c
	}
	return pref, rest, nil
}

// newPlayerClient is network.NewClient with this player's color
// preference set.
func newPlayerClient(name string) *network.Client {
	c := network.NewClient(name)
	c.SetColorPreference(colorPref)
	return c
}

// applyColorPrefFromArgs is main's first step: it sets colorPref and strips
// --color from os.Args so the subcommand parsers never see it.
func applyColorPrefFromArgs() {
	pref, rest, err := initColorPref(os.Args[1:], os.Getenv("WW_COLOR"))
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(2)
	}
	colorPref = pref
	os.Args = append(os.Args[:1:1], rest...)
}
