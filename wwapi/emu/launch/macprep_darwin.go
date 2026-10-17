//go:build darwin

package launch

import (
	"os"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// The macOS prepare step on this Mac (#16): the app's first-run setup
// calls these; the plan itself (MacPlan) and the decision (MacCheck) are
// pure and tested on every OS.

// PrepareMacHere prepares ~/Applications/Dolphin.app from
// /Applications/Dolphin.app on this Mac (scripts/setup-mac-dolphin.sh as
// Go: MacPlan, then MacVerify).
func PrepareMacHere(rep log.Reporter) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return PrepareMac(rep, ExecRunner{}, MacApp, MacPreparedApp(home))
}

// MacStatusHere says whether this Mac needs the prepare step
// (MacCheck of GatherMacFacts), with the facts it decided from.
func MacStatusHere() (MacState, MacFacts, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return MacNoDolphin, MacFacts{}, err
	}
	f := GatherMacFacts(home, statExists, os.ReadFile, ExecRunner{})
	return MacCheck(f), f, nil
}
