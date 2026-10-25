package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/crash"
)

// runCrashContext prints the crash JUTException caught on the selected
// Dolphin (WW_DOLPHIN_INDEX / WW_DOLPHIN_PID): the error, the registers,
// a 20-frame stack walk and the linked REL modules, each address named
// from the decomp's symbols.txt (WW_TWW_SYMBOLS, default
// ~/projects/tww/config/GZLE01/symbols.txt), T2's map (WW_T2_MAP,
// default inject/build/WWMultiplayer.map) or a REL module's section and
// offset (wwapi/crash). It only reads. Idea from datstarkey/ww-online's
// scripts/dolphin-crash-context.py (MIT), written clean-room.
func runCrashContext(string, []string) {
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	rep, err := crash.Read(d)
	if err != nil {
		rep.WritePartial(os.Stdout) // the error code, DSISR and DAR it did read
		fmt.Println(err)
		os.Exit(1)
	}
	r := crash.Resolver{OwnModule: inject.RELModuleID}
	if rep.Exception.Caught {
		r.DOL = loadSymbols(crashSymbolsPath(), crash.ParseDOLSymbols)
		r.T2 = loadSymbols(crashT2MapPath(), crash.ParseDolphinMap)
	}
	rep.Write(os.Stdout, r)
}

// crashSymbolsPath is WW_TWW_SYMBOLS, else the decomp checked out next to
// this one under ~/projects/tww.
func crashSymbolsPath() string {
	if p := os.Getenv("WW_TWW_SYMBOLS"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "projects", "tww", "config", "GZLE01", "symbols.txt")
}

// crashT2MapPath is WW_T2_MAP, else the map inject/build.py writes,
// relative to the working directory (the repo root).
func crashT2MapPath() string {
	if p := os.Getenv("WW_T2_MAP"); p != "" {
		return p
	}
	return filepath.Join("inject", "build", "WWMultiplayer.map")
}

// loadSymbols parses the file at path, or returns nil (and says so) when
// it can't: the report then prints "?" for those addresses.
func loadSymbols(path string, parse func(io.Reader) (*crash.Symbols, error)) *crash.Symbols {
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("(symbols: %v)\n", err)
		return nil
	}
	defer f.Close()
	s, err := parse(f)
	if err != nil {
		fmt.Printf("(symbols: %v)\n", err)
		return nil
	}
	return s
}
