package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

// dolphinOptions reads WW_DOLPHIN_INDEX and WW_DOLPHIN_PID into
// emu.Options. When several Dolphin instances run (two-Dolphin
// multiplayer), WW_DOLPHIN_INDEX picks one (0 = lowest PID, 1 = next, ...;
// default 0), and WW_DOLPHIN_PID names one outright. The environment is
// read here, in package main, and never in wwapi.
func dolphinOptions(getenv func(string) string, gameID string) (emu.Options, error) {
	o := emu.Options{GameID: gameID}
	if v := getenv("WW_DOLPHIN_INDEX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return o, fmt.Errorf("bad WW_DOLPHIN_INDEX=%q", v)
		}
		o.Index = n
	}
	if pidStr := getenv("WW_DOLPHIN_PID"); pidStr != "" {
		pid64, err := strconv.ParseUint(pidStr, 10, 32)
		if err != nil || pid64 == 0 {
			return o, fmt.Errorf("bad WW_DOLPHIN_PID=%q", pidStr)
		}
		o.PID = int(pid64)
	}
	return o, nil
}

// findDolphin attaches to the Dolphin the environment picks (see
// dolphinOptions) and maps its emulated RAM.
func findDolphin(gameID string) (*emu.Dolphin, error) {
	o, err := dolphinOptions(os.Getenv, gameID)
	if err != nil {
		return nil, err
	}
	return emu.Attach(o)
}
