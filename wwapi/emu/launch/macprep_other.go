//go:build !darwin

package launch

import "github.com/StephenSHorton/ww-multiplayer/wwapi/log"

// PrepareMacHere returns ErrNotMac: preparing Dolphin is a macOS step.
func PrepareMacHere(log.Reporter) error { return ErrNotMac }

// MacStatusHere returns ErrNotMac: only macOS needs a prepared copy.
func MacStatusHere() (MacState, MacFacts, error) { return MacNoDolphin, MacFacts{}, ErrNotMac }
