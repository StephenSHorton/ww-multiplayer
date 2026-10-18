package feed

import (
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// ContractEvery is how often puppet-sync runs the contract check again
// while it plays: a save state loaded mid-session (another version's)
// is caught within this long.
const ContractEvery = 2 * time.Second

// contractGate is puppet-sync's strict contract check (Phase E1,
// wwapi.CheckContract): it writes into the game only while the game runs
// this build's mod, and says plainly why not and what to do when it
// doesn't (an unpatched game, a disc patched by another version, or a
// save state taken on one).
type contractGate struct {
	m    mem.Reader
	rep  log.Reporter
	ok   bool
	why  string // the refusal last logged, "" when none
	next time.Time
	ran  bool
}

func newContractGate(m mem.Reader, rep log.Reporter) *contractGate {
	return &contractGate{m: m, rep: rep}
}

// check runs the contract check when ContractEvery has passed since the
// last one (always the first time) and returns whether writes are
// allowed, and whether they became allowed again with this check (the
// caller re-arms the mailbox then). A refusal is logged once per reason;
// a read error keeps the last verdict.
func (g *contractGate) check(now time.Time) (ok, regained bool) {
	if g.ran && now.Before(g.next) {
		return g.ok, false
	}
	g.next = now.Add(ContractEvery)
	c, err := wwapi.CheckContract(g.m, wwapi.ContractOptions{})
	if err != nil {
		if !g.ran {
			g.ran = true
			g.ok = false
			g.rep.Log(log.Warn, "Contract check: cannot read the game yet ("+err.Error()+"); remote players are drawn once it can.")
		}
		return g.ok, false
	}
	was, first := g.ok, !g.ran
	g.ran = true
	g.ok = c.WritesAllowed
	switch {
	case !g.ok && c.Why != g.why:
		g.why = c.Why
		g.rep.Log(log.Err, "This Dolphin's game is not this version's patch: "+c.Why+". "+wwapi.RepatchAdvice+" Remote players are not drawn until it is.")
	case g.ok && (!was || first):
		g.why = ""
		g.rep.Log(log.OK, wwapi.ContractLine(c))
	}
	return g.ok, g.ok && !was && !first
}
