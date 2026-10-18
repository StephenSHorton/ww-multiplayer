package coopsession

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// ContractEvery is how often coop-sync runs the strict contract check
// again while it plays, so a save state from another version loaded
// mid-session turns the writes off within this long (as puppet-sync's
// feed.ContractEvery).
const ContractEvery = 2 * time.Second

// contractErr runs the strict contract check (Phase E1,
// wwapi.CheckContract) on m: nil when this build may write to the game,
// else an error matching wwapi.ErrContract that says why and what to do.
func contractErr(m mem.Reader) error {
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		return err
	}
	if c.WritesAllowed {
		return nil
	}
	return fmt.Errorf("co-op: this Dolphin's game is not this version's patch: %w. %s", c.Err(), wwapi.RepatchAdvice)
}

// contractErrPID is contractErr on the Dolphin env opens for pid (0: the
// one WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select).
func contractErrPID(env Env, pid uint32) error {
	d, err := env.Open(pid)
	if err != nil {
		return err
	}
	defer d.Close()
	return contractErr(d)
}

// guardContract re-runs the contract check on m every every (ContractEvery)
// until ctx ends. While the game is refused it keeps the replica's
// writes off (TurnOff, again after a /coop on), saying why once; when
// the game is this version's again it says that /coop on resumes them.
func guardContract(ctx context.Context, m mem.Reader, r interface{ TurnOff(string) }, rep log.Reporter, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	refused := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := contractErr(m)
		switch {
		case err == nil && refused != "":
			refused = ""
			rep.Log(log.Info, "co-op: this Dolphin's game is this version's patch again; type /coop on to write the world again.")
		case err != nil && !errors.Is(err, wwapi.ErrContract):
			// A read error keeps the last verdict.
		case err != nil:
			r.TurnOff(err.Error())
			if err.Error() != refused {
				refused = err.Error()
				rep.Log(log.Err, err.Error()+" Co-op writes are off; pose sync goes on.")
			}
		}
	}
}
