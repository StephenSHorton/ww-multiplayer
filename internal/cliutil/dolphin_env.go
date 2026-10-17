// Package cliutil holds what both of the repository's binaries need from
// a command line: ww-multiplayer (package main at the root) and wwdev
// (cmd/wwdev, the developer tools). It attaches to the Dolphin the
// environment picks and reports command-line mistakes. Like package main
// before it, it reads the environment, which wwapi never does.
package cliutil

import (
	"fmt"
	"os"
	"strconv"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

// DolphinOptions reads WW_DOLPHIN_INDEX and WW_DOLPHIN_PID into
// emu.Options. When several Dolphin instances run (two-Dolphin
// multiplayer), WW_DOLPHIN_INDEX picks one (0 = lowest PID, 1 = next, ...;
// default 0), and WW_DOLPHIN_PID names one outright. The environment is
// read here, on the binaries' side, and never in wwapi.
func DolphinOptions(getenv func(string) string, gameID string) (emu.Options, error) {
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

// FindDolphin attaches to the Dolphin the environment picks (see
// DolphinOptions) and maps its emulated RAM.
func FindDolphin(gameID string) (*emu.Dolphin, error) {
	o, err := DolphinOptions(os.Getenv, gameID)
	if err != nil {
		return nil, err
	}
	return emu.Attach(o)
}

// OpenDolphin is the multiplexer between env-driven Find() (CLI flows
// that respect WW_DOLPHIN_INDEX / WW_DOLPHIN_PID) and explicit-PID
// FindByPID (mp-local, which enumerates both Dolphins upfront so two
// in-process goroutines can address different instances without the
// env-var collision).
func OpenDolphin(pid uint32) (*emu.Dolphin, error) {
	if pid == 0 {
		return FindDolphin("GZLE01")
	}
	return emu.FindByPID(pid, "GZLE01")
}
