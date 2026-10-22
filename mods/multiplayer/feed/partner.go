package feed

import (
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// The stage gate (coop-slice-drc A3): a pose carries no stage, so a
// partner in another stage would be drawn at its raw coordinates in this
// one. Co-op's PRESENCE says which stage the partner is in; the co-op
// pipeline of the same session publishes it here (PartnerFor), and the
// Feeder hides the partner's mini-Link (and its actor puppet) while that
// stage is not this game's. PRESENCE travels both ways (the hub relays
// each player's to the other), so the gate works on the host and the
// joiner alike. It names no sender, which is enough for the two players
// co-op supports: the gate applies only while the feed draws exactly one
// remote.
//
// Without co-op (or before the first PRESENCE, or once it goes stale)
// nothing is published and nobody is hidden, as before the gate.

// PartnerStale is how long a published place is believed: a replica
// repeats PRESENCE every second (replica.PresenceEvery), so a place older
// than this belongs to a co-op connection that has gone.
const PartnerStale = 3 * time.Second

// PartnerPlace is where one session's co-op partner last said it is. It
// is safe for concurrent use: the co-op pipeline publishes, PuppetSync
// reads.
type PartnerPlace struct {
	mu     sync.Mutex
	stage  string // mCurStage.mName; "" for unknown
	inGame bool   // a stage with Link is loaded
	moving bool   // a stage change is pending
	at     time.Time
}

// Publish records a PRESENCE as of now: the partner's stage ("" when it
// names none, as the hub's PRESENCE for a peer that left does), whether
// it is in game, and whether a stage change of its is pending.
func (p *PartnerPlace) Publish(stage string, inGame, transitioning bool, now time.Time) {
	p.mu.Lock()
	p.stage, p.inGame, p.moving, p.at = stage, inGame, transitioning, now
	p.mu.Unlock()
}

// Snapshot returns the last published place and when it was published
// (the zero time before the first Publish).
func (p *PartnerPlace) Snapshot() (stage string, inGame, transitioning bool, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stage, p.inGame, p.moving, p.at
}

// elsewhere reports whether the partner is known (a fresh place naming a
// stage) and, if so, whether it is out of stage here: not in game, on its
// way to another stage, or in a different one. there is its stage.
func (p *PartnerPlace) elsewhere(here string, now time.Time) (away, known bool, there string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stage == "" || p.at.IsZero() || now.Sub(p.at) > PartnerStale {
		return false, false, ""
	}
	return !p.inGame || p.moving || p.stage != here, true, p.stage
}

var (
	partnersMu sync.Mutex
	partners   = map[rosterKey]*PartnerPlace{}
)

// PartnerFor returns the partner place of the session that runs as name
// on Dolphin pid, creating it on first use; it is keyed as RosterFor is.
func PartnerFor(name string, pid uint32) *PartnerPlace {
	partnersMu.Lock()
	defer partnersMu.Unlock()
	k := rosterKey{name, pid}
	p := partners[k]
	if p == nil {
		p = &PartnerPlace{}
		partners[k] = p
	}
	return p
}

// partnerAway decides the stage gate for one Feed of remotes at now: true
// while the one remote drawn is, by its PRESENCE, out of this game's
// stage. While this game is between stages (not in play) the last
// decision holds, so a stage change of either game does not flash the
// mini-Link up for a tick. It logs each change of the decision.
func (f *Feeder) partnerAway(remotes []network.RemotePlayer, now time.Time) bool {
	if f.partner == nil {
		return false
	}
	var only *network.RemotePlayer
	for i := range remotes {
		rp := &remotes[i]
		if rp.Position == nil || f.selfFilter != "" && rp.Name == f.selfFilter {
			continue
		}
		if only != nil {
			return f.setAway(false, nil, "", "") // PRESENCE names no sender
		}
		only = rp
	}
	if only == nil {
		return f.setAway(false, nil, "", "")
	}
	here, inPlay := f.stageHere()
	if !inPlay {
		return f.away
	}
	away, known, there := f.partner.elsewhere(here, now)
	if !known {
		return f.setAway(false, only, here, "")
	}
	return f.setAway(away, only, here, there)
}

// setAway records the stage gate's decision, logging a change of it.
func (f *Feeder) setAway(away bool, rp *network.RemotePlayer, here, there string) bool {
	if away == f.away {
		return away
	}
	f.away = away
	switch {
	case away:
		log.Logf(f.rep, log.Info, "player %d (%s) is out of this stage (%s; here %s): hidden until it is back", rp.ID, rp.Name, there, here)
	case rp != nil && there != "":
		log.Logf(f.rep, log.Info, "player %d (%s) is in this stage (%s): shown", rp.ID, rp.Name, here)
	default:
		f.rep.Log(log.Info, "stage gate off (no co-op place for a single partner): remotes are shown")
	}
	return away
}
