//go:build !darwin

package dolphinenv

import "github.com/StephenSHorton/ww-multiplayer/internal/report"

// PrepareMacHere returns ErrNotMac: preparing Dolphin is a macOS step.
func PrepareMacHere(report.Reporter) error { return ErrNotMac }

// MacStatusHere returns ErrNotMac: only macOS needs a prepared copy.
func MacStatusHere() (MacState, MacFacts, error) { return MacNoDolphin, MacFacts{}, ErrNotMac }
